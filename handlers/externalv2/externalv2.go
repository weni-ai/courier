package externalv2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"

	"github.com/pkg/errors"
)

const (
	contentURLEncoded = "urlencoded"
	contentJSON       = "json"

	configMOResponseContentType = "mo_response_content_type"
	configMOResponse            = "mo_response"

	configMTResponseCheck = "mt_response_check"

	ConfigSendTemplate    = "send_template"
	ConfigReceiveTemplate = "receive_template"

	ConfigSendAttachmentInParts = "send_attachment_in_parts"
)

var contentTypeMappings = map[string]string{
	contentURLEncoded: "application/x-www-form-urlencoded",
	contentJSON:       "application/json",
}

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("X2"), "ExternalV2")}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	s.AddHandlerRoute(h, http.MethodGet, "receive", h.receiveMessage)

	sentHandler := h.buildStatusHandler("sent")
	s.AddHandlerRoute(h, http.MethodGet, "sent", sentHandler)
	s.AddHandlerRoute(h, http.MethodPost, "sent", sentHandler)

	deliveredHandler := h.buildStatusHandler("delivered")
	s.AddHandlerRoute(h, http.MethodGet, "delivered", deliveredHandler)
	s.AddHandlerRoute(h, http.MethodPost, "delivered", deliveredHandler)

	failedHandler := h.buildStatusHandler("failed")
	s.AddHandlerRoute(h, http.MethodGet, "failed", failedHandler)
	s.AddHandlerRoute(h, http.MethodPost, "failed", failedHandler)

	s.AddHandlerRoute(h, http.MethodPost, "stopped", h.receiveStopContact)
	s.AddHandlerRoute(h, http.MethodGet, "stopped", h.receiveStopContact)

	return nil
}

type stopContactForm struct {
	From string `validate:"required" name:"from"`
}

func (h *handler) receiveStopContact(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	form := &stopContactForm{}
	err := handlers.DecodeAndValidateForm(form, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// create our URN
	var urn urns.URN
	if channel.Schemes()[0] == urns.TelScheme {
		urn, err = handlers.StrictTelForCountry(form.From, channel.Country())
	} else {
		urn, err = urns.NewURNFromParts(channel.Schemes()[0], form.From, "", "")
	}
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}
	urn = urn.Normalize("")

	// create a stop channel event
	channelEvent := h.Backend().NewChannelEvent(channel, courier.StopContact, urn)
	err = h.Backend().WriteChannelEvent(ctx, channelEvent)
	if err != nil {
		return nil, err
	}
	return []courier.Event{channelEvent}, courier.WriteChannelEventSuccess(ctx, w, r, channelEvent)
}

func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {

	receiveBodyTemplate := channel.StringConfigForKey(ConfigReceiveTemplate, "")
	if receiveBodyTemplate == "" {
		return nil, fmt.Errorf("receive body template is empty. It must be defined in the channel config")
	}

	tmpl, err := template.New("mapping").Parse(string(receiveBodyTemplate))
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unable to parse receive body template: %s", err))
	}

	bodyPayload := make(map[string]any)

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(10000000); err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unable to parse multipart form: %s", err))
		}
		for k, v := range r.Form {
			bodyPayload[k] = v[0]
		}
	} else if contentType == contentJSON {
		if err := json.NewDecoder(r.Body).Decode(&bodyPayload); err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unable to decode request body: %s", err))
		}
	} else {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unsupported content type: %s", contentType))
	}

	var bodyBuffer bytes.Buffer
	if err := tmpl.Execute(&bodyBuffer, bodyPayload); err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unable to execute receive body template: %s", err))
	}

	if bodyBuffer.String() == "" {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("receive body template returned empty"))
	}

	moPayload := &receivePayload{}
	if err := json.Unmarshal(bodyBuffer.Bytes(), moPayload); err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unable to decode mo payload: %s", err))
	}

	var msgs []courier.Msg = make([]courier.Msg, 0, len(moPayload.Messages))

	for _, pMsg := range moPayload.Messages {
		from := pMsg.From
		text := pMsg.Text
		mediaURL := pMsg.MediaURL
		dateString := pMsg.Date

		var urn urns.URN
		urn, err = urns.NewURNFromParts(channel.Schemes()[0], from, "", "")
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}
		urn = urn.Normalize(channel.Country())

		msg := h.Backend().NewIncomingMsg(channel, urn, text)
		if mediaURL != "" {
			msg.WithAttachment(mediaURL)
		}

		// if we have a date, parse it
		if dateString != "" {
			date, err := time.Parse(time.RFC3339Nano, dateString)
			if err != nil {
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("invalid date format, must be RFC 3339"))
			}
			msg.WithReceivedOn(date)
		}
		msgs = append(msgs, msg)
	}

	return handlers.WriteMsgsAndResponse(ctx, h, msgs, w, r)
}

// WriteMsgSuccessResponse writes our response in TWIML format
func (h *handler) WriteMsgSuccessResponse(ctx context.Context, w http.ResponseWriter, r *http.Request, msgs []courier.Msg) error {
	moResponse := msgs[0].Channel().StringConfigForKey(configMOResponse, "")
	if moResponse == "" {
		return courier.WriteMsgSuccess(ctx, w, r, msgs)
	}
	moResponseContentType := msgs[0].Channel().StringConfigForKey(configMOResponseContentType, "")
	if moResponseContentType != "" {
		w.Header().Set("Content-Type", moResponseContentType)
	}
	w.WriteHeader(200)
	_, err := fmt.Fprint(w, moResponse)
	return err
}

// buildStatusHandler deals with building a handler that takes what status is received in the URL
func (h *handler) buildStatusHandler(status string) courier.ChannelHandleFunc {
	return func(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
		return h.receiveStatus(ctx, status, channel, w, r)
	}
}

type statusForm struct {
	ID int64 `name:"id" validate:"required"`
}

var statusMappings = map[string]courier.MsgStatusValue{
	"failed":    courier.MsgFailed,
	"sent":      courier.MsgSent,
	"delivered": courier.MsgDelivered,
}

// receiveStatus is our HTTP handler function for status updates
func (h *handler) receiveStatus(ctx context.Context, statusString string, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	form := &statusForm{}
	err := handlers.DecodeAndValidateForm(form, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// get our status
	msgStatus, found := statusMappings[strings.ToLower(statusString)]
	if !found {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, fmt.Errorf("unknown status '%s', must be one failed, sent or delivered", statusString))
	}

	// write our status
	status := h.Backend().NewMsgStatusForID(channel, courier.NewMsgID(form.ID), msgStatus)
	return handlers.WriteMsgStatusAndResponse(ctx, h, channel, status, w, r)
}

// SendMsg sends the passed in message, returning any error
func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	sendURL := msg.Channel().StringConfigForKey(courier.ConfigSendURL, "")
	if sendURL == "" {
		return nil, fmt.Errorf("no send url set for EX channel")
	}

	// configs
	responseContent := msg.Channel().StringConfigForKey(configMTResponseCheck, "")
	sendMethod := msg.Channel().StringConfigForKey(courier.ConfigSendMethod, http.MethodPost)
	sendBody := msg.Channel().StringConfigForKey(ConfigSendTemplate, "")
	contentType := msg.Channel().StringConfigForKey(courier.ConfigContentType, contentURLEncoded)
	contentTypeHeader := contentTypeMappings[contentType]
	if contentTypeHeader == "" {
		contentTypeHeader = contentType
	}

	sendAttachmentInParts := msg.Channel().StringConfigForKey(ConfigSendAttachmentInParts, "false")
	sendAttachmentInPartsBool, err := strconv.ParseBool(sendAttachmentInParts)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid send attachment in parts")
	}

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	defaultBody := map[string]any{
		"id":                    msg.ID().String(),
		"uuid":                  msg.UUID().String(),
		"text":                  msg.Text(),
		"attachments":           msg.Attachments(),
		"contact":               msg.URN().Path(),
		"channel":               msg.Channel().Address(),
		"channel_uuid":          msg.Channel().UUID().String(),
		"quick_replies":         msg.QuickReplies(),
		"products":              msg.Products(),
		"header":                msg.Header(),
		"body":                  msg.Body(),
		"footer":                msg.Footer(),
		"action":                msg.Action(),
		"send_catalog":          msg.SendCatalog(),
		"header_type":           msg.HeaderType(),
		"header_text":           msg.HeaderText(),
		"list_message":          msg.ListMessage(),
		"interaction_type":      msg.InteractionType(),
		"cta_message":           msg.CTAMessage(),
		"flow_message":          msg.FlowMessage(),
		"order_details_message": msg.OrderDetailsMessage(),
		"buttons":               msg.Buttons(),
		"action_type":           msg.ActionType(),
	}

	tmpl, err := template.New("mapping").Parse(string(sendBody))
	if err != nil {
		return nil, errors.Wrapf(err, "invalid send params map")
	}

	var outputBuffer bytes.Buffer
	if err := tmpl.Execute(&outputBuffer, defaultBody); err != nil {
		return nil, errors.Wrapf(err, "failed to execute template")
	}

	var req *http.Request

	switch contentTypeHeader {
	case "application/x-www-form-urlencoded":
		var body map[string]any
		if err := json.Unmarshal(outputBuffer.Bytes(), &body); err != nil {
			return nil, err
		}

		// body from map[string]any to url.Values
		bodyValues := url.Values{}
		for k, v := range body {
			bodyValues.Add(k, fmt.Sprintf("%v", v))
		}

		req, err = http.NewRequest(sendMethod, sendURL, strings.NewReader(bodyValues.Encode()))
		if err != nil {
			return nil, err
		}
	case "application/json":
		var body io.Reader
		if sendMethod == http.MethodPost || sendMethod == http.MethodPut {
			body = strings.NewReader(outputBuffer.String())
		}
		req, err = http.NewRequest(sendMethod, sendURL, body)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported content type: %s", contentTypeHeader)
	}

	req.Header.Set("Content-Type", contentTypeHeader)

	authorization := msg.Channel().StringConfigForKey(courier.ConfigSendAuthorization, "")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	status.AddLog(log)
	if err != nil {
		return status, nil
	}

	if responseContent == "" || strings.Contains(string(rr.Body), responseContent) {
		status.SetStatus(courier.MsgWired)
	} else {
		log.WithError("Message Send Error", fmt.Errorf("received invalid response content: %s", string(rr.Body)))
	}

	return status, nil
}

type receivePayload struct {
	Messages []struct {
		ID       string `json:"id"`
		From     string `json:"from"`
		Date     string `json:"date"`
		MediaURL string `json:"media_url"`
		Text     string `json:"text"`
	} `json:"messages"`
}
