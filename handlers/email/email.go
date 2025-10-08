package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
)

var (
	sendURL   = ""
	authToken = ""
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("EM"), "Email")}
}

func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	sendURL = s.Config().EmailProxyURL
	if sendURL == "" {
		return errors.New("EMAIL_PROXY_URL is not set")
	}
	authToken = s.Config().EmailProxyAuthToken
	if authToken == "" {
		return errors.New("EMAIL_PROXY_AUTH_TOKEN is not set")
	}
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	if payload.From == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("field 'from' required"))
	}

	if payload.Body == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("field 'body' required"))
	}

	urn, err := urns.NewURNFromParts(urns.EmailScheme, payload.From, "", "")
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Body)

	for _, attachment := range payload.Attachments {
		msg.WithAttachment(attachment)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

type moPayload struct {
	UUID        courier.MsgUUID     `json:"uuid,omitempty"`
	From        string              `json:"from,omitempty"`
	To          string              `json:"to,omitempty"`
	Body        string              `json:"body,omitempty"`
	Subject     string              `json:"subject,omitempty"`
	Attachments []string            `json:"attachments,omitempty"`
	ChannelUUID courier.ChannelUUID `json:"channel_uuid,omitempty"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	attachments := []string{}
	for _, attachment := range msg.Attachments() {
		_, attachmentURL := handlers.SplitAttachment(attachment)
		attachments = append(attachments, attachmentURL)
	}

	senderEmail := msg.Channel().StringConfigForKey("username", "")
	if senderEmail == "" {
		return nil, errors.New("could not send msg without a sender email")
	}

	payload := moPayload{
		UUID:        msg.UUID(),
		From:        senderEmail,
		To:          msg.URN().Path(),
		Subject:     subjectFromMsg(msg),
		Body:        msg.Text(),
		Attachments: attachments,
		ChannelUUID: msg.Channel().UUID(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))

	rr, err := utils.MakeHTTPRequest(req)
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	if err == nil {
		status.SetStatus(courier.MsgWired)
	}
	status.AddLog(log)
	return status, nil
}

func trunkString(str string, maxSize int) string {
	if len(str) < maxSize {
		return str
	}
	return str[:maxSize]
}

// subjectFromMsg extracts the subject from the message, it will return the first line of the
// message, and return truncated with first 56 characteres if the first line is longer than 56 characters
func subjectFromMsg(msg courier.Msg) string {
	lines := strings.Split(msg.Text(), "\n")
	if len(lines) == 0 {
		return trunkString(msg.Text(), 56)
	}
	return trunkString(lines[0], 56)
}
