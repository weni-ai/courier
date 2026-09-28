package telephony

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
)

const (
	originPSTN = "pstn"
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandlerWithParams(courier.ChannelType("TPH"), "Telephony PSTN", false)}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	s.AddHandlerRoute(h, http.MethodGet, "resolve", h.resolveChannel)
	return nil
}

type receivePayload struct {
	Type     string         `json:"type" validate:"required"`
	Origin   string         `json:"origin" validate:"required"`
	DID      string         `json:"did" validate:"required"`
	CallerID string         `json:"caller_id"`
	CallID   string         `json:"call_id" validate:"required"`
	Message  receiveMessage `json:"message"`
}

type receiveMessage struct {
	Type      string `json:"type" validate:"required"`
	Timestamp string `json:"timestamp" validate:"required"`
	Text      string `json:"text"`
	MessageID string `json:"message_id,omitempty"`
}

type resolveResponse struct {
	ChannelUUID string `json:"channel_uuid"`
	ProjectUUID string `json:"project_uuid"`
}

func isResolveRequest(r *http.Request) bool {
	return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/resolve")
}

func (h *handler) authorizeResolve(r *http.Request) error {
	expected := strings.TrimSpace(h.Server().Config().TelephonyResolveToken)
	if expected == "" {
		return nil
	}

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return errors.New("missing or invalid authorization")
	}

	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if subtle.ConstantTimeCompare([]byte(token), []byte(expected)) != 1 {

		return errors.New("missing or invalid authorization")
	}

	return nil
}

// GetChannel resolves the PSTN channel from the dialed number (DID).
func (h *handler) GetChannel(ctx context.Context, r *http.Request) (courier.Channel, error) {
	if isResolveRequest(r) {
		if err := h.authorizeResolve(r); err != nil {
			return nil, err
		}

		did := strings.TrimSpace(r.URL.Query().Get("did"))
		if did == "" {
			return nil, errors.New("did is required")
		}

		return h.Backend().GetChannelByAddress(ctx, h.ChannelType(), courier.ChannelAddress(did))
	}

	payload := &receivePayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, err
	}

	if payload.Origin != originPSTN {
		return nil, fmt.Errorf("unsupported origin %q", payload.Origin)
	}

	return h.Backend().GetChannelByAddress(ctx, h.ChannelType(), courier.ChannelAddress(payload.DID))
}

func (h *handler) resolveChannel(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	projectUUID, err := h.Backend().GetProjectUUIDFromChannelUUID(ctx, channel.UUID())
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resolveResponse{
		ChannelUUID: channel.UUID().String(),
		ProjectUUID: projectUUID,
	}); err != nil {
		return nil, err
	}

	return nil, nil
}

func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &receivePayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	if payload.Type != "message" {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "ignoring request, unknown payload type")
	}

	if payload.Origin != originPSTN {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unsupported origin %q", payload.Origin))
	}

	if payload.Message.Type != "text" {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "ignoring request, unknown message type")
	}

	if strings.TrimSpace(payload.Message.Text) == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("blank message text"))
	}

	urn, err := buildContactURN(payload.CallerID, payload.CallID, channel.Country())
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	ts, err := strconv.ParseInt(payload.Message.Timestamp, 10, 64)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("invalid timestamp: %s", payload.Message.Timestamp))
	}

	date := time.Unix(ts, 0).UTC()
	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Message.Text).WithReceivedOn(date)

	if payload.Message.MessageID != "" {
		msg = msg.WithExternalID(payload.Message.MessageID)
	}

	metadata, err := callMetadata(payload.CallID)
	if err == nil {
		msg = msg.WithMetadata(metadata)
	}

	h.Backend().WriteContactLastSeen(ctx, msg, date)

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

func buildContactURN(callerID, callID, country string) (urns.URN, error) {
	callerID = strings.TrimSpace(callerID)
	if callerID == "" {
		if strings.TrimSpace(callID) == "" {
			return urns.NilURN, errors.New("caller_id and call_id cannot both be empty")
		}
		return urns.Parse(fmt.Sprintf("tel:withheld-%s", callID))
	}

	if strings.HasPrefix(callerID, "+") {
		return urns.NewTelURNForCountry(callerID, country)
	}

	if country != "" {
		return handlers.StrictTelForCountry(callerID, country)
	}

	return urns.NewTelURNForCountry(callerID, "")
}

func callMetadata(callID string) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]string{"call_id": callID})
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(body)
	return raw, nil
}

// SendMsg acknowledges outbound TPH messages without calling a gateway REST URL.
// Agent replies reach the live call via Nexus gRPC to the voice gateway, the same
// path as Weni Web Chat streaming — Flows persists the message; Courier must not POST /send.
func (h *handler) SendMsg(_ context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	return h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgSent), nil
}
