package weniwebchat

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
	er "github.com/pkg/errors"
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("WWC"), "Weni Web Chat")}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMsg)
	return nil
}

type miPayload struct {
	Type    string    `json:"type"           validate:"required"`
	From    string    `json:"from,omitempty" validate:"required"`
	Message miMessage `json:"message"`
}

type miMessage struct {
	Type      string `json:"type"          validate:"required"`
	TimeStamp string `json:"timestamp"     validate:"required"`
	Text      string `json:"text,omitempty"`
	MediaURL  string `json:"media_url,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
}

func (h *handler) receiveMsg(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &miPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// check message type
	if payload.Type != "message" || (payload.Message.Type != "text" && payload.Message.Type != "image" && payload.Message.Type != "video" && payload.Message.Type != "audio" && payload.Message.Type != "file" && payload.Message.Type != "location") {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "ignoring request, unknown message type")
	}

	// check empty content
	if payload.Message.Text == "" && payload.Message.MediaURL == "" && (payload.Message.Latitude == "" || payload.Message.Longitude == "") {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("blank message, media or location"))
	}

	// build urn
	urn, err := urns.NewURNFromParts(urns.ExternalScheme, payload.From, "", "")
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// parse timestamp
	ts, err := strconv.ParseInt(payload.Message.TimeStamp, 10, 64)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("invalid timestamp: %s", payload.Message.TimeStamp))
	}

	// parse medias
	var mediaURL string
	if payload.Message.Type == "location" {
		mediaURL = fmt.Sprintf("geo:%s,%s", payload.Message.Latitude, payload.Message.Longitude)
	} else if payload.Message.MediaURL != "" {
		mediaURL = payload.Message.MediaURL
		payload.Message.Text = payload.Message.Caption
	}

	// build message
	date := time.Unix(ts, 0).UTC()
	msg := h.Backend().NewIncomingMsg(channel, urn, payload.Message.Text).WithReceivedOn(date).WithContactName(payload.From)

	// write the contact last seen
	h.Backend().WriteContactLastSeen(ctx, msg, date)

	if mediaURL != "" {
		msg.WithAttachment(mediaURL)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

var timestamp = ""

type moPayload struct {
	Type        string    `json:"type" validate:"required"`
	To          string    `json:"to"   validate:"required"`
	From        string    `json:"from" validate:"required"`
	Message     moMessage `json:"message"`
	ChannelUUID string    `json:"channel_uuid" validate:"required"`
}

type moMessage struct {
	Type         string               `json:"type"      validate:"required"`
	TimeStamp    string               `json:"timestamp" validate:"required"`
	Text         string               `json:"text,omitempty"`
	MediaURL     string               `json:"media_url,omitempty"`
	Caption      string               `json:"caption,omitempty"`
	Latitude     string               `json:"latitude,omitempty"`
	Longitude    string               `json:"longitude,omitempty"`
	QuickReplies []string             `json:"quick_replies,omitempty"`
	ListMessage  *courier.ListMessage `json:"list_message,omitempty"`
	CTAMessage   *courier.CTAMessage  `json:"cta_message,omitempty"`
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	start := time.Now()
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgSent)

	baseURL := msg.Channel().StringConfigForKey(courier.ConfigBaseURL, "")
	if baseURL == "" {
		return nil, errors.New("blank base_url")
	}

	sendURL := fmt.Sprintf("%s/send", baseURL)
	var logs []*courier.ChannelLog

	payload := newOutgoingMessage("message", msg.URN().Path(), msg.Channel().Address(), msg.QuickReplies(), msg.Channel().UUID().String())

	// sendPayload marshals and sends the payload, collecting logs
	sendPayload := func() error {
		body, err := json.Marshal(&payload)
		if err != nil {
			elapsed := time.Since(start)
			logs = append(logs, courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, err))
			return err
		}
		req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		idempotencyKey := fmt.Sprintf("%s-%d", msg.UUID().String(), time.Now().UnixNano())
		res, err := utils.MakeHTTPRequestWithRetry(ctx, req, 3, 500*time.Millisecond, idempotencyKey)
		if res != nil {
			logs = append(logs, courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), res).WithError("Message Send Error", err))
		}
		return err
	}

	// addInteractiveElements adds quick replies, list message, and CTA to the payload
	addInteractiveElements := func() {
		payload.Message.QuickReplies = normalizeQuickReplies(msg.QuickReplies())
		if len(msg.ListMessage().ListItems) > 0 {
			listMessage := msg.ListMessage()
			payload.Message.ListMessage = &listMessage
		}
		if msg.CTAMessage() != nil {
			payload.Message.CTAMessage = msg.CTAMessage()
		}
	}

	lenAttachments := len(msg.Attachments())
	if lenAttachments > 0 {
		for i, attachment := range msg.Attachments() {
			mimeType, attachmentURL := handlers.SplitAttachment(attachment)

			msgType, ok := mimeTypeToMessageType(mimeType)
			if !ok {
				elapsed := time.Since(start)
				logs = append(logs, courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, fmt.Errorf("unknown attachment mime type: %s", mimeType)))
				status.SetStatus(courier.MsgFailed)
				break
			}

			payload.Message = moMessage{
				Type:      msgType,
				TimeStamp: getTimestamp(),
				MediaURL:  attachmentURL,
			}

			// add caption to first attachment
			if i == 0 {
				payload.Message.Caption = msg.Text()
			}

			// add interactive elements on last message
			if i == lenAttachments-1 {
				addInteractiveElements()
			}

			if err := sendPayload(); err != nil {
				status.SetStatus(courier.MsgFailed)
				break
			}
		}
	} else {
		payload.Message = moMessage{
			Type:      "text",
			TimeStamp: getTimestamp(),
			Text:      msg.Text(),
		}
		addInteractiveElements()

		if err := sendPayload(); err != nil {
			status.SetStatus(courier.MsgFailed)
		}
	}

	for _, log := range logs {
		status.AddLog(log)
	}

	return status, nil
}

// mimeTypeToMessageType maps MIME type prefixes to message types
func mimeTypeToMessageType(mimeType string) (string, bool) {
	prefixMap := map[string]string{
		"audio":       "audio",
		"application": "file",
		"image":       "image",
		"video":       "video",
	}
	for prefix, msgType := range prefixMap {
		if strings.HasPrefix(mimeType, prefix) {
			return msgType, true
		}
	}
	return "", false
}

var _ courier.ActionSender = (*handler)(nil)

// SendAction sends a specific action to the Weni Webchat API.
// This method is specific to the Weni Webchat handler.
func (h *handler) SendAction(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	baseURL := msg.Channel().StringConfigForKey(courier.ConfigBaseURL, "")
	if baseURL == "" {
		return nil, errors.New("blank base_url")
	}

	sendURL := fmt.Sprintf("%s/send", baseURL)

	// Create payload for typing indicator
	payload := map[string]interface{}{
		"type":         "typing_start",
		"to":           msg.URN().Path(),
		"from":         "ai-assistant",
		"channel_uuid": msg.Channel().UUID().String(),
	}

	// build request
	body, err := json.Marshal(&payload)
	if err != nil {
		return nil, er.Wrap(err, "HTTP request failed")
	}

	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := utils.MakeHTTPRequest(req)
	if err != nil {
		return nil, er.Wrap(err, "HTTP request failed")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("weni webchat API error (%d): %s", res.StatusCode, string(res.Body))
	}

	return nil, nil
}

func newOutgoingMessage(payType, to, from string, quickReplies []string, channelUUID string) *moPayload {
	return &moPayload{
		Type: payType,
		To:   to,
		From: from,
		Message: moMessage{
			QuickReplies: quickReplies,
		},
		ChannelUUID: channelUUID,
	}
}

func getTimestamp() string {
	if timestamp != "" {
		return timestamp
	}

	return fmt.Sprint(time.Now().Unix())
}

func normalizeQuickReplies(quickReplies []string) []string {
	var text string
	var qrs []string
	for _, qr := range quickReplies {
		if strings.Contains(qr, "\\/") {
			text = strings.Replace(qr, "\\", "", -1)
		} else if strings.Contains(qr, "\\\\") {
			text = strings.Replace(qr, "\\\\", "\\", -1)
		} else {
			text = qr
		}
		qrs = append(qrs, text)
	}
	return qrs
}
