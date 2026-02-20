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
	InteractiveProductListType = "product_list"
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
	Type          string            `json:"type"           validate:"required"`
	From          string            `json:"from,omitempty" validate:"required"`
	Message       miMessage         `json:"message"`
	ContactFields map[string]string `json:"contact_fields,omitempty"`
}

type miMessage struct {
	Type      string `json:"type"          validate:"required"`
	TimeStamp string `json:"timestamp"     validate:"required"`
	Text      string `json:"text,omitempty"`
	MediaURL  string `json:"media_url,omitempty"`
	Caption   string `json:"caption,omitempty"`
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
	Order     struct {
		ProductItems []miProductItem `json:"product_items"`
	} `json:"order,omitempty"`
}

type miProductItem struct {
	ProductRetailerID string `json:"product_retailer_id"`
	Name              string `json:"name"`
	Price             string `json:"price"`
	Image             string `json:"image"`
	Description       string `json:"description"`
	SellerID          string `json:"seller_id"`
	Quantity          int    `json:"quantity"`
}

func (h *handler) receiveMsg(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &miPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// check message type
	if payload.Type != "message" || (payload.Message.Type != "text" && payload.Message.Type != "image" && payload.Message.Type != "video" && payload.Message.Type != "audio" && payload.Message.Type != "file" && payload.Message.Type != "location" && payload.Message.Type != "order") {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "ignoring request, unknown message type")
	}

	// check empty content
	hasOrder := payload.Message.Type == "order" && len(payload.Message.Order.ProductItems) > 0
	if payload.Message.Text == "" && payload.Message.MediaURL == "" && (payload.Message.Latitude == "" || payload.Message.Longitude == "") && !hasOrder {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, errors.New("blank message, media, location or order"))
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
	msg := h.Backend().
		NewIncomingMsg(channel, urn, payload.Message.Text).
		WithReceivedOn(date).
		WithContactName(payload.From).
		WithNewContactFields(payload.ContactFields)

	// write the contact last seen
	h.Backend().WriteContactLastSeen(ctx, msg, date)

	if mediaURL != "" {
		msg.WithAttachment(mediaURL)
	}

	// add order metadata if present
	if payload.Message.Type == "order" && len(payload.Message.Order.ProductItems) > 0 {
		orderM := map[string]interface{}{
			"order": payload.Message.Order,
			"overwrite_message": map[string]interface{}{
				"order": payload.Message.Order,
			},
		}
		orderJSON, err := json.Marshal(orderM)
		if err == nil {
			metadata := json.RawMessage(orderJSON)
			msg.WithMetadata(metadata)
		}
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
	Interactive  *wwcInteractive      `json:"interactive,omitempty"`
}

// Interactive message structures
type wwcInteractive struct {
	Type   string `json:"type"`
	Header *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"header,omitempty"`
	Footer *struct {
		Text string `json:"text,omitempty"`
	} `json:"footer,omitempty"`
	Action *wwcAction `json:"action,omitempty"`
}

type wwcAction struct {
	Sections []wwcSection `json:"sections,omitempty"`
	Name     string       `json:"name,omitempty"`
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

type wwcProductItem struct {
	ProductRetailerID string `json:"product_retailer_id"`
	Name              string `json:"name"`
	Price             string `json:"price"`
	SalePrice         string `json:"sale_price,omitempty"`
	Image             string `json:"image"`
	Description       string `json:"description"`
	SellerID          string `json:"seller_id"`
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
		log, err := sendMessagePayload(ctx, payload, sendURL, msg, start)
		if log != nil {
			logs = append(logs, log)
		}
		return err
	}

	// addInteractiveElements adds quick replies, list message, CTA and products to the payload
	addInteractiveElements := func() {
		payload.Message.QuickReplies = normalizeQuickReplies(msg.QuickReplies())
		if len(msg.ListMessage().ListItems) > 0 {
			listMessage := msg.ListMessage()
			payload.Message.ListMessage = &listMessage
		}
		if msg.CTAMessage() != nil {
			payload.Message.CTAMessage = msg.CTAMessage()
		}

		hasProducts := len(msg.Products()) > 0
		if hasProducts {
			interactives := buildProductInteractives(msg)
			if len(interactives) > 0 {
				msgText := strings.TrimSpace(msg.Text())
				if msgText == "" {
					msgText = strings.TrimSpace(msg.Body())
				}

				for _, interactive := range interactives {
					payload.Message = moMessage{
						Type:      "interactive",
						TimeStamp: getTimestamp(),
						Text:      msgText,
					}
					payload.Message.Interactive = interactive
				}
			}
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

// sendMessagePayload marshals the payload and sends it to the given URL with retry logic
func sendMessagePayload(ctx context.Context, payload *moPayload, sendURL string, msg courier.Msg, start time.Time) (*courier.ChannelLog, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		elapsed := time.Since(start)
		return courier.NewChannelLogFromError("Error sending message", msg.Channel(), msg.ID(), elapsed, err), err
	}
	req, _ := http.NewRequest(http.MethodPost, sendURL, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	idempotencyKey := fmt.Sprintf("%s-%d", msg.UUID().String(), time.Now().UnixNano())
	res, err := utils.MakeHTTPRequestWithRetry(ctx, req, 3, 500*time.Millisecond, idempotencyKey)
	if res != nil {
		return courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), res).WithError("Message Send Error", err), err
	}
	return nil, err
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

// buildProductInteractives builds interactive product payloads from message products, respecting batch limits
func buildProductInteractives(msg courier.Msg) []*wwcInteractive {
	products := msg.Products()
	sections := extractProductSections(products)

	// Count total products
	totalProducts := 0
	for _, section := range sections {
		totalProducts += len(section.ProductItems)
	}

	if totalProducts == 0 {
		return nil
	}

	// Build header if present
	var header *struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	if msg.HeaderText() != "" {
		header = &struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		}{
			Type: "text",
			Text: msg.HeaderText(),
		}
	}

	// Build footer if present
	var footer *struct {
		Text string `json:"text,omitempty"`
	}
	if msg.Footer() != "" {
		footer = &struct {
			Text string `json:"text,omitempty"`
		}{
			Text: msg.Footer(),
		}
	}

	// Build message batches respecting limits (30 products, 10 sections per message)
	allBatches := buildProductBatches(sections)

	var interactives []*wwcInteractive
	for _, batch := range allBatches {
		interactive := &wwcInteractive{
			Type:   InteractiveProductListType,
			Header: header,
			Footer: footer,
			Action: &wwcAction{
				Sections: batch,
				Name:     msg.Action(),
			},
		}
		interactives = append(interactives, interactive)
	}

	return interactives
}

// extractProductSections extracts sections with their products from the products map
func extractProductSections(products []map[string]interface{}) []wwcSection {
	var sections []wwcSection

	for _, product := range products {
		section := wwcSection{}

		// Get section title from "product" field
		if title, ok := product["product"].(string); ok {
			section.Title = title
		}

		// Extract product_retailer_info as products for this section
		if priData, ok := product["product_retailer_info"]; ok {
			if priList, ok := priData.([]interface{}); ok {
				for _, pri := range priList {
					if priMap, ok := pri.(map[string]interface{}); ok {
						item := wwcProductItem{}
						if name, ok := priMap["name"].(string); ok {
							item.Name = name
						}
						if retailerID, ok := priMap["retailer_id"].(string); ok {
							item.ProductRetailerID = retailerID
						}
						if price, ok := priMap["price"].(string); ok {
							item.Price = price
						}
						if salePrice, ok := priMap["sale_price"].(string); ok {
							item.SalePrice = salePrice
						}
						if image, ok := priMap["image"].(string); ok {
							item.Image = image
						}
						if description, ok := priMap["description"].(string); ok {
							item.Description = description
						}
						if sellerID, ok := priMap["seller_id"].(string); ok {
							item.SellerID = sellerID
						}
						section.ProductItems = append(section.ProductItems, item)
					}
				}
			}
		}

		if len(section.ProductItems) > 0 {
			sections = append(sections, section)
		}
	}

	return sections
}

// buildProductBatches builds message batches respecting limits: max 30 products and 10 sections per message
func buildProductBatches(sections []wwcSection) [][]wwcSection {
	const maxProductsPerMsg = 30
	const maxSectionsPerMsg = 10

	var allBatches [][]wwcSection
	var currentBatch []wwcSection
	var currentProductCount int

	for _, section := range sections {
		sectionProductCount := len(section.ProductItems)

		// Check if adding this section would exceed limits
		wouldExceedProducts := currentProductCount+sectionProductCount > maxProductsPerMsg
		wouldExceedSections := len(currentBatch) >= maxSectionsPerMsg

		// If adding this section exceeds limits, save current batch and start new one
		if len(currentBatch) > 0 && (wouldExceedProducts || wouldExceedSections) {
			allBatches = append(allBatches, currentBatch)
			currentBatch = []wwcSection{}
			currentProductCount = 0
		}

		// If this single section has more products than limit, split it
		if sectionProductCount > maxProductsPerMsg {
			// Split the section into multiple parts
			for i := 0; i < len(section.ProductItems); i += maxProductsPerMsg {
				end := i + maxProductsPerMsg
				if end > len(section.ProductItems) {
					end = len(section.ProductItems)
				}

				splitSection := wwcSection{
					Title:        section.Title,
					ProductItems: section.ProductItems[i:end],
				}

				// Save as individual batch since it's at max capacity
				if len(splitSection.ProductItems) == maxProductsPerMsg {
					allBatches = append(allBatches, []wwcSection{splitSection})
				} else {
					currentBatch = append(currentBatch, splitSection)
					currentProductCount = len(splitSection.ProductItems)
				}
			}
		} else {
			currentBatch = append(currentBatch, section)
			currentProductCount += sectionProductCount
		}
	}

	// Add remaining batch
	if len(currentBatch) > 0 {
		allBatches = append(allBatches, currentBatch)
	}

	return allBatches
}
