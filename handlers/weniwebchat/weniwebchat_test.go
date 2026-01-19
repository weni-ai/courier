package weniwebchat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
)

const channelUUID = "8eb23e93-5ecb-45ba-b726-3b064e0c568c"

var testChannels = []courier.Channel{
	courier.NewMockChannel(channelUUID, "WWC", "250788383383", "", map[string]interface{}{"catalog_id": "test-catalog-123"}),
}

var testChannelsWithoutCatalog = []courier.Channel{
	courier.NewMockChannel(channelUUID, "WWC", "250788383383", "", map[string]interface{}{}),
}

// ReceiveMsg test

var receiveURL = fmt.Sprintf("/c/wwc/%s/receive", channelUUID)

// Order metadata for tests
var orderMetadata1 = json.RawMessage(`{"order":{"catalog_id":"test-catalog-123","text":"Order placed","product_items":[{"product_retailer_id":"product-001","quantity":2,"item_price":29.99,"currency":"BRL"},{"product_retailer_id":"product-002","quantity":1,"item_price":49.99,"currency":"BRL"}]}}`)
var orderMetadata2 = json.RawMessage(`{"order":{"catalog_id":"test-catalog-456","text":"I want to buy these items","product_items":[{"product_retailer_id":"product-abc","quantity":3,"item_price":19.99,"currency":"USD"}]}}`)

const (
	textMsgTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"text",
			"timestamp":%q,
			"text":%q
		}
	}
	`

	imgMsgTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"image",
			"timestamp":%q,
			"media_url":%q,
			"caption":%q
		}
	}
	`

	locationMsgTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"location",
			"timestamp":%q,
			"latitude":%q,
			"longitude":%q
		}
	}
	`

	orderMsgTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"order",
			"timestamp":%q,
			"order":{
				"catalog_id":"test-catalog-123",
				"text":"Order placed",
				"product_items":[
					{
						"product_retailer_id":"product-001",
						"quantity":2,
						"item_price":29.99,
						"currency":"BRL"
					},
					{
						"product_retailer_id":"product-002",
						"quantity":1,
						"item_price":49.99,
						"currency":"BRL"
					}
				]
			}
		}
	}
	`

	orderMsgWithTextTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"order",
			"timestamp":%q,
			"order":{
				"catalog_id":"test-catalog-456",
				"text":%q,
				"product_items":[
					{
						"product_retailer_id":"product-abc",
						"quantity":3,
						"item_price":19.99,
						"currency":"USD"
					}
				]
			}
		}
	}
	`

	invalidMsgTemplate = `
	{
		"type":"foo",
		"from":"bar",
		"message": {
			"id":"000001",
			"type":"text",
			"timestamp":"1616586927"
		}
	}
	`
)

var testCases = []ChannelHandleTestCase{
	{
		Label:    "Receive Valid Text Msg",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgTemplate, "2345678", "1616586927", "Hello Test!"),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("Hello Test!"),
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:      "Receive Valid Media",
		URL:        receiveURL,
		Data:       fmt.Sprintf(imgMsgTemplate, "2345678", "1616586927", "https://link.to/image.png", "My Caption"),
		Name:       Sp("2345678"),
		URN:        Sp("ext:2345678"),
		Text:       Sp("My Caption"),
		Attachment: Sp("https://link.to/image.png"),
		Status:     200,
		Response:   "Accepted",
	},
	{
		Label:      "Receive Valid Location",
		URL:        receiveURL,
		Data:       fmt.Sprintf(locationMsgTemplate, "2345678", "1616586927", "-9.6996104", "-35.7794614"),
		Name:       Sp("2345678"),
		URN:        Sp("ext:2345678"),
		Attachment: Sp("geo:-9.6996104,-35.7794614"),
		Status:     200,
		Response:   "Accepted",
	},
	{
		Label:    "Receive Valid Order",
		URL:      receiveURL,
		Data:     fmt.Sprintf(orderMsgTemplate, "2345678", "1616586927"),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("Order placed"),
		Metadata: &orderMetadata1,
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:    "Receive Order With Custom Text",
		URL:      receiveURL,
		Data:     fmt.Sprintf(orderMsgWithTextTemplate, "2345678", "1616586927", "I want to buy these items"),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("I want to buy these items"),
		Metadata: &orderMetadata2,
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:  "Receive Invalid JSON",
		URL:    receiveURL,
		Data:   "{}",
		Status: 400,
	},
	{
		Label:    "Receive Text Msg With Blank Message Text",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgTemplate, "2345678", "1616586927", ""),
		Status:   400,
		Response: "blank message, media, location or order",
	},
	{
		Label:    "Receive Invalid Timestamp",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgTemplate, "2345678", "foo", "Hello Test!"),
		Status:   400,
		Response: "invalid timestamp: foo",
	},
	{
		Label:    "Receive Invalid Message",
		URL:      receiveURL,
		Data:     invalidMsgTemplate,
		Status:   200,
		Response: "ignoring request, unknown message type",
	},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), testCases)
}

func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newHandler(), testCases)
}

// SendMsg test

func prepareSendMsg(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, s.URL)
	timestamp = "1616700878"
}

func mockAttachmentURLs(mediaServer *httptest.Server, testCases []ChannelSendTestCase) []ChannelSendTestCase {
	casesWithMockedUrls := make([]ChannelSendTestCase, len(testCases))

	for i, testCase := range testCases {
		mockedCase := testCase

		for j, attachment := range testCase.Attachments {
			mockedCase.Attachments[j] = strings.Replace(attachment, "https://foo.bar", mediaServer.URL, 1)
		}
		casesWithMockedUrls[i] = mockedCase
	}
	return casesWithMockedUrls
}

var sendTestCases = []ChannelSendTestCase{
	{
		Label:          "Plain Send",
		Text:           "Simple Message",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"Simple Message"},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
	},
	{
		Label:          "Unicode Send",
		Text:           "☺",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"☺"},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
	},
	{
		Label:       "invalid Text Send",
		Text:        "Error",
		URN:         "ext:371298371241",
		Status:      string(courier.MsgFailed),
		Path:        "/send",
		Headers:     map[string]string{"Content-type": "application/json"},
		RequestBody: `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"Error"},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		SendPrep:    prepareSendMsg,

		ResponseStatus: 500,
	},
	{
		Label: "Medias Send",
		Text:  "Medias",
		Attachments: []string{
			"audio/mp3:https://foo.bar/audio.mp3",
			"application/pdf:https://foo.bar/file.pdf",
			"image/jpg:https://foo.bar/image.jpg",
			"video/mp4:https://foo.bar/video.mp4",
		},
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
	},
	{
		Label:          "Invalid Media Type Send",
		Text:           "Medias",
		Attachments:    []string{"foo/bar:https://foo.bar/foo.bar"},
		URN:            "ext:371298371241",
		Status:         string(courier.MsgFailed),
		ResponseStatus: 400,
		SendPrep:       prepareSendMsg,
	},
	{
		Label:       "Invalid Media Send",
		Text:        "Medias",
		Attachments: []string{"image/png:https://foo.bar/image.png"},
		URN:         "ext:371298371241",
		Status:      string(courier.MsgFailed),
		SendPrep:    prepareSendMsg,

		ResponseStatus: 500,
	},
	{
		Label:          "No Timestamp Prepare",
		Text:           "No prepare",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		ResponseStatus: 200,
		SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
			c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, s.URL)
			timestamp = ""
		},
	},
	{
		Label:          "Quick Replies Send",
		Text:           "Simple Message",
		QuickReplies:   []string{"Yes", "No", "\\/Slash", "\\\\Backslash"},
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"Simple Message","quick_replies":["Yes","No","/Slash","\\Backslash"]},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
	},
}

func TestSending(t *testing.T) {
	mediaServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		res.WriteHeader(200)

		res.Write([]byte("media bytes"))
	}))
	mockedSendTestCases := mockAttachmentURLs(mediaServer, sendTestCases)
	mediaServer.Close()

	RunChannelSendTestCases(t, testChannels[0], newHandler(), mockedSendTestCases, nil)
}

// setSendURL takes care of setting the send_url to our test server host
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, s.URL)
}

var ActionTestCases = []ChannelSendTestCase{
	{Label: "Send Typing Indicator",
		URN:            "ext:123",
		Metadata:       json.RawMessage(`{"action_type":"typing_indicator"}`),
		ResponseStatus: 200,
		RequestBody:    `{"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c","from":"ai-assistant","to":"123","type":"typing_start"}`,
		SendPrep:       setSendURL},
}

func TestActions(t *testing.T) {
	RunChannelActionTestCases(t, testChannels[0], newHandler(), ActionTestCases, nil)
}

// Product message tests
var productSendTestCases = []ChannelSendTestCase{
	{
		Label:          "Single Product Send",
		Text:           "Check out this product!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Check out this product!","interactive":{"type":"product","action":{"catalog_id":"test-catalog-123","product_retailer_id":"product-123","name":"View Product"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Product Name","product_retailer_ids":["product-123"]}],"action":"View Product"}`),
	},
	{
		Label:          "Catalog Message Send",
		Text:           "Browse our catalog!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Browse our catalog!","interactive":{"type":"catalog_message","action":{"name":"catalog_message"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"send_catalog":true,"action":"Browse Catalog"}`),
	},
	{
		Label:          "Multiple Products Send",
		Text:           "Here are some products!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Here are some products!","interactive":{"type":"product_list","action":{"catalog_id":"test-catalog-123","sections":[{"title":"Electronics","product_items":[{"product_retailer_id":"product-1"},{"product_retailer_id":"product-2"}]}],"name":"View Products"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Electronics","product_retailer_ids":["product-1","product-2"]}],"action":"View Products"}`),
	},
	{
		Label:    "Product Message Without Catalog ID",
		Text:     "Product without catalog",
		URN:      "ext:371298371241",
		Status:   string(courier.MsgFailed),
		Error:    "Catalog ID not found in channel config",
		SendPrep: prepareSendMsg,
		Metadata: json.RawMessage(`{"products":[{"product":"Product Name","product_retailer_ids":["product-123"]}],"action":"View Product"}`),
	},
}

func TestProductSending(t *testing.T) {
	// Test cases with catalog_id
	casesWithCatalog := productSendTestCases[:3] // First 3 test cases
	RunChannelSendTestCases(t, testChannels[0], newHandler(), casesWithCatalog, nil)

	// Test case without catalog_id
	casesWithoutCatalog := productSendTestCases[3:4] // Last test case
	RunChannelSendTestCases(t, testChannelsWithoutCatalog[0], newHandler(), casesWithoutCatalog, nil)
}
