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

const (
	InteractiveProductSingleType         = "product"
	InteractiveProductListType           = "product_list"
	InteractiveProductCatalogType        = "catalog_product"
	InteractiveProductCatalogMessageType = "catalog_message"
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
	Type         string          `json:"type"      validate:"required"`
	TimeStamp    string          `json:"timestamp" validate:"required"`
	Text         string          `json:"text,omitempty"`
	MediaURL     string          `json:"media_url,omitempty"`
	Caption      string          `json:"caption,omitempty"`
	Latitude     string          `json:"latitude,omitempty"`
	Longitude    string          `json:"longitude,omitempty"`
	QuickReplies []string        `json:"quick_replies,omitempty"`
	Interactive  *wwcInteractive `json:"interactive,omitempty"`
}

// Product-related structures for interactive messages
type wwcInteractive struct {
	Type   string `json:"type"`
	Header *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"header,omitempty"`
	Body struct {
		Text string `json:"text"`
	} `json:"body,omitempty"`
	Footer *struct {
		Text string `json:"text,omitempty"`
	} `json:"footer,omitempty"`
	Action *struct {
		Button            string                 `json:"button,omitempty"`
		CatalogID         string                 `json:"catalog_id,omitempty"`
		Sections          []wwcSection           `json:"sections,omitempty"`
		Buttons           []wwcButton            `json:"buttons,omitempty"`
		ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
		Name              string                 `json:"name,omitempty"`
		Parameters        map[string]interface{} `json:"parameters,omitempty"`
	} `json:"action,omitempty"`
}

type wwcSection struct {
	Title        string           `json:"title,omitempty"`
	Rows         []wwcSectionRow  `json:"rows,omitempty"`
	ProductItems []wwcProductItem `json:"product_items,omitempty"`
}

type wwcSectionRow struct {
	ID          string `json:"id" validate:"required"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type wwcButton struct {
	Type  string `json:"type" validate:"required"`
	Reply struct {
		ID    string `json:"id" validate:"required"`
		Title string `json:"title" validate:"required"`
	} `json:"reply" validate:"required"`
}

type wwcProductItem struct {
	ProductRetailerID string `json:"product_retailer_id" validate:"required"`
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

	// Check for product messages first
	if len(msg.Products()) > 0 || msg.SendCatalog() {
		return h.sendProductMessage(ctx, msg, status, sendURL, start)
	}

	payload := newOutgoingMessage("message", msg.URN().Path(), msg.Channel().Address(), msg.QuickReplies(), msg.Channel().UUID().String())
	lenAttachments := len(msg.Attachments())
	if lenAttachments > 0 {

	attachmentsLoop:
		for i, attachment := range msg.Attachments() {
			mimeType, attachmentURL := handlers.SplitAttachment(attachment)
			payload.Message.TimeStamp = getTimestamp()
			// parse attachment type
			if strings.HasPrefix(mimeType, "audio") {
				payload.Message = moMessage{
					Type:     "audio",
					MediaURL: attachmentURL,
				}
			} else if strings.HasPrefix(mimeType, "application") {
				payload.Message = moMessage{
					Type:     "file",
					MediaURL: attachmentURL,
				}
			} else if strings.HasPrefix(mimeType, "image") {
				payload.Message = moMessage{
					Type:     "image",
					MediaURL: attachmentURL,
				}
			} else if strings.HasPrefix(mimeType, "video") {
				payload.Message = moMessage{
					Type:     "video",
					MediaURL: attachmentURL,
				}
			} else {
				elapsed := time.Since(start)
				log := courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, fmt.Errorf("unknown attachment mime type: %s", mimeType))
				logs = append(logs, log)
				status.SetStatus(courier.MsgFailed)
				break attachmentsLoop
			}

			// add a caption to the first attachment
			if i == 0 {
				payload.Message.Caption = msg.Text()
			}

			// add quickreplies on last message
			if i == lenAttachments-1 {
				qrs := normalizeQuickReplies(msg.QuickReplies())
				payload.Message.QuickReplies = qrs
			}

			// build request
			var body []byte
			body, err := json.Marshal(&payload)
			if err != nil {
				elapsed := time.Since(start)
				log := courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, err)
				logs = append(logs, log)
				status.SetStatus(courier.MsgFailed)
				break attachmentsLoop
			}
			req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			idempotencyKey := fmt.Sprintf("%s-%d", msg.UUID().String(), time.Now().UnixNano())
			res, err := utils.MakeHTTPRequestWithRetry(ctx, req, 3, 500*time.Millisecond, idempotencyKey)
			if res != nil {
				log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), res).WithError("Message Send Error", err)
				logs = append(logs, log)
			}
			if err != nil {
				status.SetStatus(courier.MsgFailed)
				break attachmentsLoop
			}
		}
	} else {
		qrs := normalizeQuickReplies(msg.QuickReplies())
		payload.Message = moMessage{
			Type:         "text",
			TimeStamp:    getTimestamp(),
			Text:         msg.Text(),
			QuickReplies: qrs,
		}
		// build request
		body, err := json.Marshal(&payload)
		if err != nil {
			elapsed := time.Since(start)
			log := courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, err)
			logs = append(logs, log)
			status.SetStatus(courier.MsgFailed)
		} else {
			req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			idempotencyKey := fmt.Sprintf("%s-%d", msg.UUID().String(), time.Now().UnixNano())
			res, err := utils.MakeHTTPRequestWithRetry(ctx, req, 3, 500*time.Millisecond, idempotencyKey)
			if res != nil {
				log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), res).WithError("Message Send Error", err)
				logs = append(logs, log)
			}
			if err != nil {
				status.SetStatus(courier.MsgFailed)
			}
		}

	}

	for _, log := range logs {
		status.AddLog(log)
	}

	return status, nil
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
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("Weni Webchat API error (%d): %s", res.StatusCode, string(res.Body))
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

// sendProductMessage handles sending product messages
func (h *handler) sendProductMessage(ctx context.Context, msg courier.Msg, status courier.MsgStatus, sendURL string, start time.Time) (courier.MsgStatus, error) {
	catalogID := msg.Channel().StringConfigForKey("catalog_id", "")
	if catalogID == "" {
		status.SetStatus(courier.MsgFailed)
		return status, errors.New("Catalog ID not found in channel config")
	}

	products := msg.Products()

	isUnitaryProduct := true
	var unitaryProduct string
	for _, product := range products {
		retailerIDs := toStringSlice(product["product_retailer_ids"])
		if len(products) > 1 || len(retailerIDs) > 1 {
			isUnitaryProduct = false
		} else {
			unitaryProduct = retailerIDs[0]
		}
	}

	var interactiveType string
	if msg.SendCatalog() {
		interactiveType = InteractiveProductCatalogMessageType
	} else if !isUnitaryProduct {
		interactiveType = InteractiveProductListType
	} else {
		interactiveType = InteractiveProductSingleType
	}

	interactive := wwcInteractive{
		Type: interactiveType,
	}

	interactive.Body = struct {
		Text string `json:"text"`
	}{
		Text: msg.Text(),
	}

	if msg.Header() != "" && !isUnitaryProduct && !msg.SendCatalog() {
		interactive.Header = &struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}{
			Type: "text",
			Text: msg.Header(),
		}
	}

	if msg.Footer() != "" {
		interactive.Footer = &struct {
			Text string `json:"text,omitempty"`
		}{
			Text: parseBackslashes(msg.Footer()),
		}
	}

	if msg.SendCatalog() {
		interactive.Action = &struct {
			Button            string                 `json:"button,omitempty"`
			CatalogID         string                 `json:"catalog_id,omitempty"`
			Sections          []wwcSection           `json:"sections,omitempty"`
			Buttons           []wwcButton            `json:"buttons,omitempty"`
			ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
			Name              string                 `json:"name,omitempty"`
			Parameters        map[string]interface{} `json:"parameters,omitempty"`
		}{
			Name: "catalog_message",
		}
		payload := moPayload{
			Type:        "interactive",
			To:          msg.URN().Path(),
			From:        msg.Channel().Address(),
			ChannelUUID: msg.Channel().UUID().String(),
			Message: moMessage{
				Type:      "interactive",
				TimeStamp: getTimestamp(),
			},
		}
		payload.Message.Interactive = &interactive
		return h.sendPayload(ctx, payload, status, sendURL, start, msg.Channel(), msg.ID())
	} else if len(products) > 0 {
		if !isUnitaryProduct {
			actions := [][]wwcSection{}
			sections := []wwcSection{}
			totalProductsPerMsg := 0

			for _, product := range products {
				retailerIDs := toStringSlice(product["product_retailer_ids"])

				title := product["product"].(string)
				if title == "product_retailer_id" {
					title = "items"
				}
				if len(title) > 24 {
					title = title[:24]
				}

				var sproducts []wwcProductItem

				for _, p := range retailerIDs {
					// If there is still room for the product in the current message
					if totalProductsPerMsg < 30 {
						sproducts = append(sproducts, wwcProductItem{
							ProductRetailerID: p,
						})
						totalProductsPerMsg++
						continue
					}

					// When reaching 30 products, close current section and start new one
					if len(sproducts) > 0 {
						sections = append(sections, wwcSection{Title: title, ProductItems: sproducts})
						sproducts = []wwcProductItem{}
					}

					// Save current section to actions and restart for new message
					if len(sections) > 0 {
						actions = append(actions, sections)
						sections = []wwcSection{}
						totalProductsPerMsg = 0
					}

					// Start new section with current product
					sproducts = append(sproducts, wwcProductItem{ProductRetailerID: p})
					totalProductsPerMsg++
				}

				// After the inner loop, add the current section with the product
				if len(sproducts) > 0 {
					sections = append(sections, wwcSection{Title: title, ProductItems: sproducts})
				}
			}

			if len(sections) > 0 {
				actions = append(actions, sections)
			}

			for _, sections := range actions {
				interactive.Action = &struct {
					Button            string                 `json:"button,omitempty"`
					CatalogID         string                 `json:"catalog_id,omitempty"`
					Sections          []wwcSection           `json:"sections,omitempty"`
					Buttons           []wwcButton            `json:"buttons,omitempty"`
					ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
					Name              string                 `json:"name,omitempty"`
					Parameters        map[string]interface{} `json:"parameters,omitempty"`
				}{
					CatalogID: catalogID,
					Sections:  sections,
					Name:      msg.Action(),
				}

				payload := moPayload{
					Type:        "interactive",
					To:          msg.URN().Path(),
					From:        msg.Channel().Address(),
					ChannelUUID: msg.Channel().UUID().String(),
					Message: moMessage{
						Type:      "interactive",
						TimeStamp: getTimestamp(),
					},
				}
				payload.Message.Interactive = &interactive
				status, err := h.sendPayload(ctx, payload, status, sendURL, start, msg.Channel(), msg.ID())
				if err != nil {
					return status, err
				}
			}
		} else {
			interactive.Action = &struct {
				Button            string                 `json:"button,omitempty"`
				CatalogID         string                 `json:"catalog_id,omitempty"`
				Sections          []wwcSection           `json:"sections,omitempty"`
				Buttons           []wwcButton            `json:"buttons,omitempty"`
				ProductRetailerID string                 `json:"product_retailer_id,omitempty"`
				Name              string                 `json:"name,omitempty"`
				Parameters        map[string]interface{} `json:"parameters,omitempty"`
			}{
				CatalogID:         catalogID,
				Name:              msg.Action(),
				ProductRetailerID: unitaryProduct,
			}
			payload := moPayload{
				Type:        "interactive",
				To:          msg.URN().Path(),
				From:        msg.Channel().Address(),
				ChannelUUID: msg.Channel().UUID().String(),
				Message: moMessage{
					Type:      "interactive",
					TimeStamp: getTimestamp(),
				},
			}
			payload.Message.Interactive = &interactive
			status, err := h.sendPayload(ctx, payload, status, sendURL, start, msg.Channel(), msg.ID())
			if err != nil {
				return status, err
			}
		}
	}

	return status, nil
}

// sendPayload sends a payload to the Weni Webchat API
func (h *handler) sendPayload(ctx context.Context, payload moPayload, status courier.MsgStatus, sendURL string, start time.Time, channel courier.Channel, msgID courier.MsgID) (courier.MsgStatus, error) {
	body, err := json.Marshal(&payload)
	if err != nil {
		elapsed := time.Since(start)
		log := courier.NewChannelLogFromError("Error sending message", channel, msgID, elapsed, err)
		status.AddLog(log)
		status.SetStatus(courier.MsgFailed)
		return status, err
	}

	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	idempotencyKey := fmt.Sprintf("%s-%d", payload.ChannelUUID, time.Now().UnixNano())
	res, err := utils.MakeHTTPRequestWithRetry(ctx, req, 3, 500*time.Millisecond, idempotencyKey)
	if res != nil {
		log := courier.NewChannelLogFromRR("Message Sent", channel, msgID, res).WithError("Message Send Error", err)
		status.AddLog(log)
	}
	if err != nil {
		status.SetStatus(courier.MsgFailed)
	}

	return status, err
}

// toStringSlice converts interface{} to []string
func toStringSlice(v interface{}) []string {
	if v == nil {
		return []string{}
	}
	switch val := v.(type) {
	case []string:
		return val
	case []interface{}:
		result := make([]string, len(val))
		for i, item := range val {
			if str, ok := item.(string); ok {
				result[i] = str
			}
		}
		return result
	default:
		return []string{}
	}
}

// parseBackslashes handles backslash parsing similar to facebookapp
func parseBackslashes(baseText string) string {
	var text string
	if strings.Contains(baseText, "\\/") {
		text = strings.Replace(baseText, "\\", "", -1)
	} else if strings.Contains(baseText, "\\\\") {
		text = strings.Replace(baseText, "\\\\", "\\", -1)
	} else {
		text = baseText
	}
	return text
}
