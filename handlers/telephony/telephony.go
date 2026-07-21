package telephony

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
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

type outboundPayload struct {
	Type   string          `json:"type"`
	Origin string          `json:"origin"`
	To     string          `json:"to"`
	From   string          `json:"from"`
	CallID string          `json:"call_id,omitempty"`
	Msg    outboundMessage `json:"message"`
}

type outboundMessage struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
}

// GetChannel resolves the PSTN channel from the dialed number (DID) in the request body.
func (h *handler) GetChannel(ctx context.Context, r *http.Request) (courier.Channel, error) {
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
		return urns.NewURNFromParts(urns.TelScheme, fmt.Sprintf("withheld-%s", callID), "", "")
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

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	start := time.Now()
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgSent)

	baseURL := strings.TrimRight(msg.Channel().StringConfigForKey(courier.ConfigBaseURL, ""), "/")
	if baseURL == "" {
		return nil, errors.New("blank base_url")
	}

	callID := extractCallID(msg.Metadata())
	payload := outboundPayload{
		Type:   "message",
		Origin: originPSTN,
		To:     msg.URN().Path(),
		From:   msg.Channel().Address(),
		CallID: callID,
		Msg: outboundMessage{
			Type:      "text",
			Timestamp: strconv.FormatInt(time.Now().Unix(), 10),
			Text:      msg.Text(),
		},
	}

	body, err := json.Marshal(&payload)
	if err != nil {
		elapsed := time.Since(start)
		status.AddLog(courier.NewChannelLogFromError("Error marshalling outbound payload", msg.Channel(), msg.ID(), elapsed, err))
		return status, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/send", bytes.NewReader(body))
	if err != nil {
		elapsed := time.Since(start)
		status.AddLog(courier.NewChannelLogFromError("Error creating outbound request", msg.Channel(), msg.ID(), elapsed, err))
		return status, err
	}

	req.Header.Set("Content-Type", "application/json")
	authToken := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := utils.MakeHTTPRequest(req)
	elapsed := time.Since(start)
	if resp != nil {
		status.AddLog(courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), resp).WithError("Message Send Error", err))
	}
	if err != nil {
		status.SetStatus(courier.MsgErrored)
		return status, err
	}

	if resp.StatusCode/100 != 2 {
		err = fmt.Errorf("received non-success response: %d", resp.StatusCode)
		status.SetStatus(courier.MsgErrored)
		status.AddLog(courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, err))
		return status, err
	}

	return status, nil
}

func extractCallID(metadata json.RawMessage) string {
	if len(metadata) == 0 {
		return ""
	}
	var data map[string]string
	if err := json.Unmarshal(metadata, &data); err != nil {
		return ""
	}
	return data["call_id"]
}
