package rocketchat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
	"net/http"
)

const (
	configBaseURL     = "base_url"
	configSecret      = "secret"
	configBotUsername = "bot_username"
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("RC"), "RocketChat")}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	return nil
}

type moPayload struct {
	User struct {
		URN      string `json:"urn"     validate:"required"`
		Username string `json:"username"`
		FullName string `json:"full_name"`
	} `json:"user" validate:"required"`
	Text        string   `json:"text"        validate:"required"`
	Attachments []string `json:"attachments"`
}

// receiveMessage is our HTTP handler function for incoming messages
func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	// check authorization
	secret := channel.StringConfigForKey(configSecret, "")
	if fmt.Sprintf("Token %s", secret) != r.Header.Get("Authorization") {
		return nil, courier.WriteAndLogUnauthorized(ctx, w, r, channel, fmt.Errorf("invalid Authorization header"))
	}

	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	urn, err := urns.NewURNFromParts(urns.RocketChatScheme, payload.User.URN, "", payload.User.Username)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Text).WithContactName(payload.User.FullName)
	for _, attachment := range payload.Attachments {
		msg.WithAttachment(attachment)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

type Attachment struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type mtPayload struct {
	UserURN     string       `json:"userURN"`
	BotUsername string       `json:"botUsername"`
	Text        string       `json:"text"`
	Attachments []Attachment `json:"attachments"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	baseURL := msg.Channel().StringConfigForKey(configBaseURL, "")
	secret := msg.Channel().StringConfigForKey(configSecret, "")
	botUsername := msg.Channel().StringConfigForKey(configBotUsername, "")

	// the status that will be written for this message
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	payload := &mtPayload{
		UserURN:     msg.URN().Path(),
		BotUsername: botUsername,
		Text:        msg.Text(),
	}
	for _, attachment := range msg.Attachments() {
		mimeType, url := handlers.SplitAttachment(attachment)
		payload.Attachments = append(payload.Attachments, Attachment{mimeType, url})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return status, err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL + "/message", bytes.NewReader(body))
	if err != nil {
		return status, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", secret))
	res, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return status, err
	}
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), res).WithError("Message Send Error", err)
	status.AddLog(log)
	status.SetStatus(courier.MsgSent)

	msgID, err := jsonparser.GetString(res.Body, "id")
	if err == nil {
		status.SetExternalID(msgID)
	}
	return status, nil
}
