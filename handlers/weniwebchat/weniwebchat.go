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

	// Check for product messages first
	if len(msg.Products()) > 0 {
		fmt.Println("Sending product message")
		fmt.Println(msg.Products())
		return h.sendProductMessage(ctx, msg, status, sendURL, start)
	}

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

// sendProductMessage handles sending product messages
// All products are sent as product_list with sections containing product_items
func (h *handler) sendProductMessage(ctx context.Context, msg courier.Msg, status courier.MsgStatus, sendURL string, start time.Time) (courier.MsgStatus, error) {
	fmt.Println("sendProductMessage() - Starting")

	products := msg.Products()
	fmt.Printf("sendProductMessage() - Products from msg.Products(): %d items\n", len(products))
	if len(products) > 0 {
		fmt.Printf("sendProductMessage() - Products content: %+v\n", products)
	}

	// Extract sections with their products
	sections := extractProductSections(products)
	fmt.Printf("sendProductMessage() - Extracted sections: %d\n", len(sections))

	// Count total products
	totalProducts := 0
	for i, section := range sections {
		productCount := len(section.ProductItems)
		totalProducts += productCount
		fmt.Printf("sendProductMessage() - Section %d: title='%s', products=%d\n", i, section.Title, productCount)
	}

	fmt.Printf("sendProductMessage() - Total products: %d\n", totalProducts)
	if totalProducts == 0 {
		fmt.Println("sendProductMessage() - No products found, returning early")
		return status, nil
	}

	// Build base payload
	basePayload := moPayload{
		Type:        "message",
		To:          msg.URN().Path(),
		From:        msg.Channel().Address(),
		ChannelUUID: msg.Channel().UUID().String(),
		Message: moMessage{
			Type:      "interactive",
			TimeStamp: getTimestamp(),
		},
	}

	if msg.Text() != "" {
		basePayload.Message.Text = msg.Text()
		fmt.Printf("sendProductMessage() - Message text: %s\n", msg.Text())
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
		fmt.Printf("sendProductMessage() - Header: %s\n", msg.HeaderText())
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
		fmt.Printf("sendProductMessage() - Footer: %s\n", msg.Footer())
	}

	fmt.Printf("sendProductMessage() - Action: %s\n", msg.Action())

	// Build message batches respecting limits (30 products, 10 sections per message)
	allBatches := buildProductBatches(sections)
	fmt.Printf("sendProductMessage() - Built %d batches\n", len(allBatches))

	// Send each batch as a separate message
	for i, batch := range allBatches {
		fmt.Printf("sendProductMessage() - Sending batch %d with %d sections\n", i+1, len(batch))
		interactive := wwcInteractive{
			Type:   InteractiveProductListType,
			Header: header,
			Footer: footer,
			Action: &wwcAction{
				Sections: batch,
				Name:     msg.Action(),
			},
		}

		payload := basePayload
		payload.Message.Interactive = &interactive

		payloadJSON, _ := json.Marshal(payload)
		fmt.Printf("sendProductMessage() - Payload JSON: %s\n", string(payloadJSON))

		status, err := h.sendPayload(ctx, payload, status, sendURL, start, msg.Channel(), msg.ID())
		if err != nil {
			fmt.Printf("sendProductMessage() - Error sending payload: %v\n", err)
			return status, err
		}
		fmt.Printf("sendProductMessage() - Batch %d sent successfully\n", i+1)
	}

	fmt.Println("sendProductMessage() - Completed successfully")
	return status, nil
}

// extractProductSections extracts sections with their products from the products map
func extractProductSections(products []map[string]interface{}) []wwcSection {
	fmt.Println("extractProductSections() - Starting")
	fmt.Printf("extractProductSections() - Input products count: %d\n", len(products))

	var sections []wwcSection

	for i, product := range products {
		fmt.Printf("extractProductSections() - Processing product %d: %+v\n", i+1, product)
		section := wwcSection{}

		// Get section title from "product" field
		if title, ok := product["product"].(string); ok {
			section.Title = title
			fmt.Printf("extractProductSections() - Product %d - Section title: %s\n", i+1, title)
		} else {
			fmt.Printf("extractProductSections() - Product %d - No 'product' field found or not a string\n", i+1)
		}

		// Extract product_retailer_info as products for this section
		if priData, ok := product["product_retailer_info"]; ok {
			fmt.Printf("extractProductSections() - Product %d - Found product_retailer_info\n", i+1)
			if priList, ok := priData.([]interface{}); ok {
				fmt.Printf("extractProductSections() - Product %d - product_retailer_info is array with %d items\n", i+1, len(priList))
				for j, pri := range priList {
					if priMap, ok := pri.(map[string]interface{}); ok {
						fmt.Printf("extractProductSections() - Product %d, Item %d - Processing: %+v\n", i+1, j+1, priMap)
						item := wwcProductItem{}
						if name, ok := priMap["name"].(string); ok {
							item.Name = name
							fmt.Printf("extractProductSections() - Product %d, Item %d - Name: %s\n", i+1, j+1, name)
						}
						if retailerID, ok := priMap["retailer_id"].(string); ok {
							item.ProductRetailerID = retailerID
							fmt.Printf("extractProductSections() - Product %d, Item %d - RetailerID: %s\n", i+1, j+1, retailerID)
						}
						if price, ok := priMap["price"].(string); ok {
							item.Price = price
							fmt.Printf("extractProductSections() - Product %d, Item %d - Price: %s\n", i+1, j+1, price)
						}
						if salePrice, ok := priMap["sale_price"].(string); ok {
							item.SalePrice = salePrice
							fmt.Printf("extractProductSections() - Product %d, Item %d - SalePrice: %s\n", i+1, j+1, salePrice)
						}
						if image, ok := priMap["image"].(string); ok {
							item.Image = image
							fmt.Printf("extractProductSections() - Product %d, Item %d - Image: %s\n", i+1, j+1, image)
						}
						if description, ok := priMap["description"].(string); ok {
							item.Description = description
							fmt.Printf("extractProductSections() - Product %d, Item %d - Description: %s\n", i+1, j+1, description)
						}
						if sellerID, ok := priMap["seller_id"].(string); ok {
							item.SellerID = sellerID
							fmt.Printf("extractProductSections() - Product %d, Item %d - SellerID: %s\n", i+1, j+1, sellerID)
						}
						section.ProductItems = append(section.ProductItems, item)
						fmt.Printf("extractProductSections() - Product %d, Item %d - Added to section\n", i+1, j+1)
					} else {
						fmt.Printf("extractProductSections() - Product %d, Item %d - Not a map, skipping\n", i+1, j+1)
					}
				}
			} else {
				fmt.Printf("extractProductSections() - Product %d - product_retailer_info is not an array, type: %T\n", i+1, priData)
			}
		} else {
			fmt.Printf("extractProductSections() - Product %d - No 'product_retailer_info' field found\n", i+1)
		}

		if len(section.ProductItems) > 0 {
			sections = append(sections, section)
			fmt.Printf("extractProductSections() - Product %d - Section added with %d items\n", i+1, len(section.ProductItems))
		} else {
			fmt.Printf("extractProductSections() - Product %d - Section not added (no items)\n", i+1)
		}
	}

	fmt.Printf("extractProductSections() - Completed, returning %d sections\n", len(sections))
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
