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
	courier.NewMockChannel(channelUUID, "WWC", "250788383383", "", map[string]interface{}{}),
}

// ReceiveMsg test

var receiveURL = fmt.Sprintf("/c/wwc/%s/receive", channelUUID)

// Order metadata for tests
var orderMetadata1 = json.RawMessage(`{"order":{"text":"Order placed","product_items":[{"product_retailer_id":"product-001","name":"Smart TV 50\"","price":"2999.90","image":"https://example.com/tv.jpg","description":"Smart TV 4K 50 inches","seller_id":"seller-001","quantity":2,"item_price":29.99,"currency":"BRL"},{"product_retailer_id":"product-002","name":"Smartphone","price":"1999.90","image":"https://example.com/phone.jpg","description":"Latest smartphone model","seller_id":"seller-002","quantity":1,"item_price":49.99,"currency":"BRL"}]}}`)
var orderMetadata2 = json.RawMessage(`{"order":{"text":"I want to buy these items","product_items":[{"product_retailer_id":"product-abc","name":"Headphones","price":"299.90","image":"https://example.com/headphones.jpg","description":"Wireless headphones","seller_id":"audio-seller","quantity":3,"item_price":19.99,"currency":"USD"}]}}`)

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

	textMsgWithContactFieldsTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":"text",
			"timestamp":%q,
			"text":%q
		},
		"contact_fields":%s
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

	mediaMsgTemplate = `
	{
		"type":"message",
		"from":%q,
		"message":{
			"type":%q,
			"timestamp":%q,
			"media_url":%q
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
				"text":"Order placed",
				"product_items":[
					{
						"product_retailer_id":"product-001",
						"name":"Smart TV 50\"",
						"price":"2999.90",
						"image":"https://example.com/tv.jpg",
						"description":"Smart TV 4K 50 inches",
						"seller_id":"seller-001",
						"quantity":2,
						"item_price":29.99,
						"currency":"BRL"
					},
					{
						"product_retailer_id":"product-002",
						"name":"Smartphone",
						"price":"1999.90",
						"image":"https://example.com/phone.jpg",
						"description":"Latest smartphone model",
						"seller_id":"seller-002",
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
				"text":%q,
				"product_items":[
					{
						"product_retailer_id":"product-abc",
						"name":"Headphones",
						"price":"299.90",
						"image":"https://example.com/headphones.jpg",
						"description":"Wireless headphones",
						"seller_id":"audio-seller",
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
		Label:    "Receive Valid Text Msg With Contact Fields",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgWithContactFieldsTemplate, "2345678", "1616586927", "Hello With Fields!", `{"name":"John Doe","email":"john@example.com"}`),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("Hello With Fields!"),
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:    "Receive Valid Text Msg With Empty Contact Fields",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgWithContactFieldsTemplate, "2345678", "1616586927", "Hello Empty Fields!", `{}`),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("Hello Empty Fields!"),
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:    "Receive Valid Text Msg With Null Contact Fields",
		URL:      receiveURL,
		Data:     fmt.Sprintf(textMsgWithContactFieldsTemplate, "2345678", "1616586927", "Hello Null Fields!", `null`),
		Name:     Sp("2345678"),
		URN:      Sp("ext:2345678"),
		Text:     Sp("Hello Null Fields!"),
		Status:   200,
		Response: "Accepted",
	},
	{
		Label:      "Receive Valid Image With Caption",
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
		Label:      "Receive Valid Audio",
		URL:        receiveURL,
		Data:       fmt.Sprintf(mediaMsgTemplate, "2345678", "audio", "1616586927", "https://link.to/audio.mp3"),
		Name:       Sp("2345678"),
		URN:        Sp("ext:2345678"),
		Attachment: Sp("https://link.to/audio.mp3"),
		Status:     200,
		Response:   "Accepted",
	},
	{
		Label:      "Receive Valid Video",
		URL:        receiveURL,
		Data:       fmt.Sprintf(mediaMsgTemplate, "2345678", "video", "1616586927", "https://link.to/video.mp4"),
		Name:       Sp("2345678"),
		URN:        Sp("ext:2345678"),
		Attachment: Sp("https://link.to/video.mp4"),
		Status:     200,
		Response:   "Accepted",
	},
	{
		Label:      "Receive Valid File",
		URL:        receiveURL,
		Data:       fmt.Sprintf(mediaMsgTemplate, "2345678", "file", "1616586927", "https://link.to/document.pdf"),
		Name:       Sp("2345678"),
		URN:        Sp("ext:2345678"),
		Attachment: Sp("https://link.to/document.pdf"),
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
		Label:       "Invalid Text Send",
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
		Label:          "Single Image With Caption",
		Text:           "This is the caption",
		Attachments:    []string{"image/jpg:https://foo.bar/image.jpg"},
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
	},
	{
		Label:          "Attachment With Quick Replies",
		Text:           "Choose an option",
		Attachments:    []string{"image/jpg:https://foo.bar/image.jpg"},
		QuickReplies:   []string{"Yes", "No"},
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
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
	{
		Label:          "List Message Send",
		Text:           "Please choose:",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"Please choose:","list_message":{"button_text":"Options","list_items":[{"uuid":"1","title":"Option 1","description":"First option"},{"uuid":"2","title":"Option 2","description":"Second option"}]}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		Metadata:       json.RawMessage(`{"interaction_type":"list","list_message":{"button_text":"Options","list_items":[{"uuid":"1","title":"Option 1","description":"First option"},{"uuid":"2","title":"Option 2","description":"Second option"}]}}`),
		SendPrep:       prepareSendMsg,
	},
	{
		Label:          "CTA Message Send",
		Text:           "Click the button below:",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"message","to":"371298371241","from":"250788383383","message":{"type":"text","timestamp":"1616700878","text":"Click the button below:","cta_message":{"url":"https://example.com","display_text":"Visit Website"}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		Metadata:       json.RawMessage(`{"interaction_type":"cta_url","cta_message":{"url":"https://example.com","display_text":"Visit Website"}}`),
		SendPrep:       prepareSendMsg,
	},
	{
		Label: "Blank Base URL",
		Text:  "Hello",
		URN:   "ext:371298371241",
		Error: "blank base_url",
		SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
			c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, "")
			timestamp = "1616700878"
		},
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
	{
		Label:          "Send Typing Indicator",
		URN:            "ext:123",
		Metadata:       json.RawMessage(`{"action_type":"typing_indicator"}`),
		ResponseStatus: 200,
		RequestBody:    `{"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c","from":"ai-assistant","to":"123","type":"typing_start"}`,
		SendPrep:       setSendURL,
	},
}

func TestActions(t *testing.T) {
	RunChannelActionTestCases(t, testChannels[0], newHandler(), ActionTestCases, nil)
}

// Unit tests for helper functions
func TestMimeTypeToMessageType(t *testing.T) {
	tests := []struct {
		mimeType    string
		expected    string
		shouldMatch bool
	}{
		{"audio/mp3", "audio", true},
		{"audio/wav", "audio", true},
		{"audio/ogg", "audio", true},
		{"application/pdf", "file", true},
		{"application/json", "file", true},
		{"application/octet-stream", "file", true},
		{"image/png", "image", true},
		{"image/jpeg", "image", true},
		{"image/gif", "image", true},
		{"video/mp4", "video", true},
		{"video/quicktime", "video", true},
		{"text/plain", "", false},
		{"foo/bar", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.mimeType, func(t *testing.T) {
			result, ok := mimeTypeToMessageType(tt.mimeType)
			if ok != tt.shouldMatch {
				t.Errorf("mimeTypeToMessageType(%q) match = %v, want %v", tt.mimeType, ok, tt.shouldMatch)
			}
			if result != tt.expected {
				t.Errorf("mimeTypeToMessageType(%q) = %q, want %q", tt.mimeType, result, tt.expected)
			}
		})
	}
}

func TestNormalizeQuickReplies(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name:     "no transformation needed",
			input:    []string{"Yes", "No", "Maybe"},
			expected: []string{"Yes", "No", "Maybe"},
		},
		{
			name:     "escaped forward slash",
			input:    []string{"\\/option"},
			expected: []string{"/option"},
		},
		{
			name:     "escaped backslash",
			input:    []string{"\\\\backslash"},
			expected: []string{"\\backslash"},
		},
		{
			name:     "mixed escapes",
			input:    []string{"normal", "\\/slash", "\\\\backslash"},
			expected: []string{"normal", "/slash", "\\backslash"},
		},
		{
			name:     "empty input",
			input:    []string{},
			expected: nil,
		},
		{
			name:     "nil input",
			input:    nil,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeQuickReplies(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("normalizeQuickReplies(%v) length = %d, want %d", tt.input, len(result), len(tt.expected))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("normalizeQuickReplies(%v)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
				}
			}
		})
	}
}

// Product message tests - all products are sent as product_list with sections
var productSendTestCases = []ChannelSendTestCase{
	{
		Label:          "Single Product Send (as product_list)",
		Text:           "Check out this product!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Check out this product!","interactive":{"type":"product_list","action":{"sections":[{"title":"Product Name","product_items":[{"product_retailer_id":"product-123","name":"Smart TV 50\"","price":"2999.90","image":"https://example.com/tv.jpg","description":"Smart TV 4K 50 inches","seller_id":"seller-001"}]}],"name":"View Product"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Product Name","product_retailer_info":[{"retailer_id":"product-123","name":"Smart TV 50\"","price":"2999.90","image":"https://example.com/tv.jpg","description":"Smart TV 4K 50 inches","seller_id":"seller-001"}]}],"action":"View Product"}`),
	},
	{
		Label:          "Multiple Products in One Section",
		Text:           "Here are some products!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Here are some products!","interactive":{"type":"product_list","action":{"sections":[{"title":"Electronics","product_items":[{"product_retailer_id":"product-1","name":"TV 4K","price":"1999.00","image":"https://example.com/tv.jpg","description":"Smart TV 4K","seller_id":"seller-001"},{"product_retailer_id":"product-2","name":"Smartphone","price":"999.00","image":"https://example.com/phone.jpg","description":"Latest smartphone","seller_id":"seller-002"}]}],"name":"View Products"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Electronics","product_retailer_info":[{"retailer_id":"product-1","name":"TV 4K","price":"1999.00","image":"https://example.com/tv.jpg","description":"Smart TV 4K","seller_id":"seller-001"},{"retailer_id":"product-2","name":"Smartphone","price":"999.00","image":"https://example.com/phone.jpg","description":"Latest smartphone","seller_id":"seller-002"}]}],"action":"View Products"}`),
	},
	{
		Label:          "Multiple Sections Send",
		Text:           "Browse our products!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Browse our products!","interactive":{"type":"product_list","action":{"sections":[{"title":"Electronics","product_items":[{"product_retailer_id":"tv-001","name":"Smart TV","price":"2500.00","image":"https://example.com/tv.jpg","description":"55 inch Smart TV","seller_id":"electronics-seller"}]},{"title":"Clothing","product_items":[{"product_retailer_id":"shirt-001","name":"T-Shirt","price":"49.90","image":"https://example.com/shirt.jpg","description":"Cotton T-Shirt","seller_id":"clothing-seller"}]}],"name":"Shop Now"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Electronics","product_retailer_info":[{"retailer_id":"tv-001","name":"Smart TV","price":"2500.00","image":"https://example.com/tv.jpg","description":"55 inch Smart TV","seller_id":"electronics-seller"}]},{"product":"Clothing","product_retailer_info":[{"retailer_id":"shirt-001","name":"T-Shirt","price":"49.90","image":"https://example.com/shirt.jpg","description":"Cotton T-Shirt","seller_id":"clothing-seller"}]}],"action":"Shop Now"}`),
	},
	{
		Label:          "Products with Header and Footer",
		Text:           "Check our catalog!",
		URN:            "ext:371298371241",
		Status:         string(courier.MsgSent),
		Path:           "/send",
		Headers:        map[string]string{"Content-type": "application/json"},
		RequestBody:    `{"type":"interactive","to":"371298371241","from":"250788383383","message":{"type":"interactive","timestamp":"1616700878","text":"Check our catalog!","interactive":{"type":"product_list","header":{"type":"text","text":"Special Offers"},"footer":{"text":"Limited time only!"},"action":{"sections":[{"title":"Deals","product_items":[{"product_retailer_id":"deal-001","name":"Headphones","price":"199.90","image":"https://example.com/headphones.jpg","description":"Wireless Headphones","seller_id":"audio-seller"}]}],"name":"View Deals"}}},"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c568c"}`,
		ResponseStatus: 200,
		SendPrep:       prepareSendMsg,
		Metadata:       json.RawMessage(`{"products":[{"product":"Deals","product_retailer_info":[{"retailer_id":"deal-001","name":"Headphones","price":"199.90","image":"https://example.com/headphones.jpg","description":"Wireless Headphones","seller_id":"audio-seller"}]}],"action":"View Deals","header":"Special Offers","footer":"Limited time only!"}`),
	},
}

func TestProductSending(t *testing.T) {
	RunChannelSendTestCases(t, testChannels[0], newHandler(), productSendTestCases, nil)
}
