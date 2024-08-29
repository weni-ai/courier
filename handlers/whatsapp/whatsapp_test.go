package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"github.com/stretchr/testify/assert"
)

var testChannels = []courier.Channel{
	courier.NewMockChannel(
		"8eb23e93-5ecb-45ba-b726-3b064e0c568c",
		"WA",
		"250788383383",
		"RW",
		map[string]interface{}{
			"auth_token": "the-auth-token",
			"base_url":   "https://foo.bar/",
		}),
	courier.NewMockChannel(
		"8eb23e93-5ecb-45ba-b726-3b064e0c568c",
		"D3",
		"250788383383",
		"RW",
		map[string]interface{}{
			"auth_token": "the-auth-token",
			"base_url":   "https://foo.bar/",
		}),
	courier.NewMockChannel(
		"8eb23e93-5ecb-45ba-b726-3b064e0c568c",
		"TXW",
		"250788383383",
		"RW",
		map[string]interface{}{
			"auth_token": "the-auth-token",
			"base_url":   "https://foo.bar/",
		}),
}

var helloMsg = `{
	"contacts":[{
		"profile": {
			"name": "Jerry Cooney"
		},
		"wa_id": "250788123123"
	}],
  "messages": [{
    "from": "250788123123",
    "id": "41",
    "timestamp": "1454119029",
    "text": {
      "body": "hello world"
    },
    "type": "text"
   }]
}`

var duplicateMsg = `{
	"messages": [{
	  "from": "250788123123",
	  "id": "41",
	  "timestamp": "1454119029",
	  "text": {
		"body": "hello world"
	  },
	  "type": "text"
	}, {
	  "from": "250788123123",
	  "id": "41",
	  "timestamp": "1454119029",
	  "text": {
		"body": "hello world"
	  },
	  "type": "text"
	}]
}`

var audioMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "audio",
		"audio": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature"
		}
	}]
}`

var buttonMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "button",
		"button": {
			"payload": null,
			"text": "BUTTON1"
		}
	}]
}`

var documentMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "document",
		"document": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature",
			"caption": "the caption",
			"filename": "filename.type"
		}
	}]
}`

var imageMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "image",
		"image": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature",
			"caption": "the caption"
		}
	}]
}`

var stickerMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "sticker",
		"sticker": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature"
		}
	}]
}`

var interactiveButtonMsg = `{
  "messages": [{
		"from": "250788123123",
		"id": "41",
		"interactive": {
			"button_reply": {
				"id": "0",
				"title": "BUTTON1"
			},
			"type": "button_reply"
		},
		"timestamp": "1454119029",
		"type": "interactive"
	}]
}`

var interactiveListMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"interactive": {
			"list_reply": {
				"id": "0",
				"title": "ROW1"
			},
			"type": "list_reply"
		},
		"timestamp": "1454119029",
		"type": "interactive"
	}]
}`

var orderMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"order": {
			"catalog_id": "800683284849775",
			"product_items": [
			  {
				"product_retailer_id": "1031",
				"quantity": 1,
				"item_price": 599.9,
				"currency": "BRL"
			  },
			  {
				"product_retailer_id": "10320",
				"quantity": 1,
				"item_price": 2399,
				"currency": "BRL"
			  }
			]
		  },
		"timestamp": "1454119029",
		"type": "order"
	}]
}`

var locationMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "location",
		"location": {
			"address": "some address",
			"latitude": 0.00,
			"longitude": 1.00,
			"name": "some name",
			"url": "https://example.org/"
		}
	}]
}`

var videoMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "video",
		"video": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature"
		}
	}]
}`

var voiceMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "voice",
		"voice": {
			"file": "/path/to/v1/media/41",
			"id": "41",
			"link": "https://example.org/v1/media/41",
			"mime_type": "text/plain",
			"sha256": "the-sha-signature"
		}
	}]
}`

var contactMsg = `{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"type": "contacts",
		"contacts": [{
			"addresses": [],
			"emails": [],
			"ims": [],
			"name": {
				"first_name": "John Cruz",
				"formatted_name": "John Cruz"
			},
			"org": {},
			"phones": [
				{
					"phone": "+1 415-858-6273",
					"type": "CELL",
					"wa_id": "14158586273"
				},
				{
					"phone": "+1 415-858-6274",
					"type": "CELL",
					"wa_id": "14158586274"
				}
			],
			"urls": []
		}]
	}]
}`

var referralMsg = fmt.Sprintf(`{
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"text": {
			"body": "Hello World"
		},
		"type": "text",
		"referral": {
			"headline": "Our new product",
			"body": "This is a great product",
			"source_type": "SOURCE_TYPE",
			"source_id": "SOURCE_ID",
			"source_url": "SOURCE_URL"
		}
	}]
}`)

var contactBomb = fmt.Sprintf(
	`{
	"contacts":[{
		"profile": {
			"name": "Jerry Cooney"
		},
		"wa_id": "250788123123"
	}],
	"messages": [{
		"from": "250788123123",
		"id": "41",
		"timestamp": "1454119029",
		"text": {
			"body": " %s "
		},
		"type": "text"
		}]
}`, strings.Repeat("b", 1000000))

var invalidFrom = `{
  "messages": [{
    "from": "notnumber",
    "id": "41",
    "timestamp": "1454119029",
    "text": {
      "body": "hello world"
    },
    "type": "text"
  }]
}`

var invalidTimestamp = `{
  "messages": [{
    "from": "notnumber",
    "id": "41",
    "timestamp": "asdf",
    "text": {
      "body": "hello world"
    },
    "type": "text"
  }]
}`

var invalidMsg = `not json`

var validStatus = `
{
  "statuses": [{
    "id": "9712A34B4A8B6AD50F",
    "recipient_id": "16315555555",
    "status": "sent",
    "timestamp": "1518694700"
  }]
}
`
var invalidStatus = `
{
  "statuses": [{
    "id": "9712A34B4A8B6AD50F",
    "recipient_id": "16315555555",
    "status": "in_orbit",
    "timestamp": "1518694700"
  }]
}
`
var ignoreStatus = `
{
  "statuses": [{
    "id": "9712A34B4A8B6AD50F",
    "recipient_id": "16315555555",
    "status": "deleted",
    "timestamp": "1518694700"
  }]
}
`

var (
	waReceiveURL = "/c/wa/8eb23e93-5ecb-45ba-b726-3b064e0c568c/receive"
	d3ReceiveURL = "/c/d3/8eb23e93-5ecb-45ba-b726-3b064e0c568c/receive"
	txReceiveURL = "/c/txw/8eb23e93-5ecb-45ba-b726-3b064e0c568c/receive"
)

var waTestCases = []ChannelHandleTestCase{
	{Label: "Receive Valid Message", URL: waReceiveURL, Data: helloMsg, Status: 200, Response: `"type":"msg"`,
		Name: Sp("Jerry Cooney"), Text: Sp("hello world"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Duplicate Valid Message", URL: waReceiveURL, Data: duplicateMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("hello world"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Audio Message", URL: waReceiveURL, Data: audioMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp(""), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Button Message", URL: waReceiveURL, Data: buttonMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("BUTTON1"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Document Message", URL: waReceiveURL, Data: documentMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("the caption"), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Image Message", URL: waReceiveURL, Data: imageMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("the caption"), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Sticker Message", URL: waReceiveURL, Data: stickerMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp(""), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Interactive Button Message", URL: waReceiveURL, Data: interactiveButtonMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("BUTTON1"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Interactive List Message", URL: waReceiveURL, Data: interactiveListMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("ROW1"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Location Message", URL: waReceiveURL, Data: locationMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp(""), Attachment: Sp("geo:0.000000,1.000000"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Video Message", URL: waReceiveURL, Data: videoMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp(""), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Voice Message", URL: waReceiveURL, Data: voiceMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp(""), Attachment: Sp("https://foo.bar/v1/media/41"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Valid Contact Message", URL: waReceiveURL, Data: contactMsg, Status: 200, Response: `"type":"msg"`,
		Text: Sp("+1 415-858-6273, +1 415-858-6274"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC))},
	{Label: "Receive Referral WA", URL: waReceiveURL, Data: referralMsg, Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: Sp("Hello World"), URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), Metadata: Jp(&struct {
			Headline   string `json:"headline"`
			Body       string `json:"body"`
			SourceType string `json:"source_type"`
			SourceID   string `json:"source_id"`
			SourceURL  string `json:"source_url"`
			Image      *struct {
				Caption  string `json:"caption"`
				Filename string `json:"filename"`
				ID       string `json:"id"`
				Mimetype string `json:"mime_type"`
				SHA256   string `json:"sha256"`
			} `json:"image"`
			Video *struct {
				Caption  string `json:"caption"`
				Filename string `json:"filename"`
				ID       string `json:"id"`
				Mimetype string `json:"mime_type"`
				SHA256   string `json:"sha256"`
			} `json:"video"`
		}{Headline: "Our new product", Body: "This is a great product", SourceType: "SOURCE_TYPE", SourceID: "SOURCE_ID", SourceURL: "SOURCE_URL", Image: nil, Video: nil})},
	{Label: "Receive Order WA", URL: waReceiveURL, Data: orderMsg, Status: 200, Response: `"type":"msg"`,
		URN: Sp("whatsapp:250788123123"), ExternalID: Sp("41"), Date: Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), NoQueueErrorCheck: false, NoInvalidChannelCheck: true, Metadata: Jp(map[string]interface{}{
			"order": map[string]interface{}{
				"catalog_id": "800683284849775",
				"text":       "",
				"product_items": []map[string]interface{}{
					{
						"product_retailer_id": "1031",
						"quantity":            1,
						"item_price":          599.9,
						"currency":            "BRL",
					},
					{
						"product_retailer_id": "10320",
						"quantity":            1,
						"item_price":          2399,
						"currency":            "BRL",
					},
				},
			},
		})},
	{Label: "Receive Invalid JSON", URL: waReceiveURL, Data: invalidMsg, Status: 400, Response: "unable to parse"},
	{Label: "Receive Invalid From", URL: waReceiveURL, Data: invalidFrom, Status: 400, Response: "invalid whatsapp id"},
	{Label: "Receive Invalid Timestamp", URL: waReceiveURL, Data: invalidTimestamp, Status: 400, Response: "invalid timestamp"},
	{Label: "Receive Contact Bomb", URL: waReceiveURL, Data: contactBomb, Status: 200},

	{Label: "Receive Valid Status", URL: waReceiveURL, Data: validStatus, Status: 200, Response: `"type":"status"`,
		MsgStatus: Sp("S"), ExternalID: Sp("9712A34B4A8B6AD50F")},
	{Label: "Receive Invalid JSON", URL: waReceiveURL, Data: "not json", Status: 400, Response: "unable to parse"},
	{Label: "Receive Invalid Status", URL: waReceiveURL, Data: invalidStatus, Status: 400, Response: `"unknown status: in_orbit"`},
	{Label: "Receive Ignore Status", URL: waReceiveURL, Data: ignoreStatus, Status: 200, Response: `"ignoring status: deleted"`},
}

func TestBuildMediaRequest(t *testing.T) {
	mb := courier.NewMockBackend()

	waHandler := &handler{NewBaseHandler(courier.ChannelType("WA"), "WhatsApp")}
	req, _ := waHandler.BuildDownloadMediaRequest(context.Background(), mb, testChannels[0], "https://example.org/v1/media/41")
	assert.Equal(t, "https://example.org/v1/media/41", req.URL.String())
	assert.Equal(t, "Bearer the-auth-token", req.Header.Get("Authorization"))

	d3Handler := &handler{NewBaseHandler(courier.ChannelType("D3"), "360Dialog")}
	req, _ = d3Handler.BuildDownloadMediaRequest(context.Background(), mb, testChannels[1], "https://example.org/v1/media/41")
	assert.Equal(t, "https://example.org/v1/media/41", req.URL.String())
	assert.Equal(t, "the-auth-token", req.Header.Get("D360-API-KEY"))

	txHandler := &handler{NewBaseHandler(courier.ChannelType("TXW"), "TextIt")}
	req, _ = txHandler.BuildDownloadMediaRequest(context.Background(), mb, testChannels[0], "https://example.org/v1/media/41")
	assert.Equal(t, "https://example.org/v1/media/41", req.URL.String())
	assert.Equal(t, "Bearer the-auth-token", req.Header.Get("Authorization"))
}

func replaceTestcaseURLs(tcs []ChannelHandleTestCase, url string) []ChannelHandleTestCase {
	replaced := make([]ChannelHandleTestCase, len(tcs))
	for i, tc := range tcs {
		tc.URL = url
		replaced[i] = tc
	}
	return replaced
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newWAHandler(courier.ChannelType("WA"), "WhatsApp"), waTestCases)
	RunChannelTestCases(t, testChannels, newWAHandler(courier.ChannelType("D3"), "360Dialog"), replaceTestcaseURLs(waTestCases, d3ReceiveURL))
	RunChannelTestCases(t, testChannels, newWAHandler(courier.ChannelType("TXW"), "TextIt"), replaceTestcaseURLs(waTestCases, txReceiveURL))
}

func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newWAHandler(courier.ChannelType("WA"), "WhatsApp"), waTestCases)
	RunChannelBenchmarks(b, testChannels, newWAHandler(courier.ChannelType("D3"), "360Dialog"), replaceTestcaseURLs(waTestCases, d3ReceiveURL))
	RunChannelBenchmarks(b, testChannels, newWAHandler(courier.ChannelType("TXW"), "TextIt"), replaceTestcaseURLs(waTestCases, txReceiveURL))
}

// setSendURL takes care of setting the base_url to our test server host
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	retryParam = "retry"
	c.(*courier.MockChannel).SetConfig("base_url", s.URL)
}

var defaultSendTestCases = []ChannelSendTestCase{
	{Label: "Link Sending",
		Text: "Link Sending https://link.com", URN: "whatsapp:250788123123", Path: "/v1/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"text","preview_url":true,"text":{"body":"Link Sending https://link.com"}}`,
		SendPrep:    setSendURL},
	{Label: "Plain Send",
		Text: "Simple Message", URN: "whatsapp:250788123123", Path: "/v1/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"text","text":{"body":"Simple Message"}}`,
		SendPrep:    setSendURL},
	{Label: "Unicode Send",
		Text: "☺", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"text","text":{"body":"☺"}}`,
		SendPrep:    setSendURL},
	{Label: "Error",
		Text: "Error", URN: "whatsapp:250788123123",
		Status:       "E",
		ResponseBody: `{ "errors": [{ "title": "Error Sending" }] }`, ResponseStatus: 403,
		RequestBody: `{"to":"250788123123","type":"text","text":{"body":"Error"}}`,
		SendPrep:    setSendURL},
	{Label: "No Message ID",
		Text: "Error", URN: "whatsapp:250788123123",
		Status:       "E",
		ResponseBody: `{ "messages": [] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"text","text":{"body":"Error"}}`,
		SendPrep:    setSendURL},
	{Label: "Error Field",
		Text: "Error", URN: "whatsapp:250788123123",
		Status:       "E",
		ResponseBody: `{ "errors": [{"title":"Error Sending"}] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"text","text":{"body":"Error"}}`,
		SendPrep:    setSendURL},
	{Label: "Audio Send",
		Text:   "audio caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"audio/mpeg:https://foo.bar/audio.mp3"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"audio","audio":{"link":"https://foo.bar/audio.mp3"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			}, MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"text","text":{"body":"audio caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Document Send",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document.pdf"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"document","document":{"link":"https://foo.bar/document.pdf","caption":"document caption","filename":"document.pdf"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Image Send",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"document caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Sticker Send",
		Text:   "sticker caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"image/webp:https://foo.bar/sticker.webp"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"sticker","sticker":{"link":"https://foo.bar/sticker.webp"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			}, MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"text","text":{"body":"sticker caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Video Send",
		Text:   "video caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"video/mp4:https://foo.bar/video.mp4"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"video","video":{"link":"https://foo.bar/video.mp4","caption":"video caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Template Send",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"waba_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]}]}}`,
		SendPrep:    setSendURL,
	},
	{Label: "Template Country Language",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"waba_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]}]}}`,
		SendPrep:    setSendURL,
	},
	{Label: "Template Namespace",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"wa_template_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]}]}}`,
		SendPrep:    setSendURL,
	},
	{Label: "Template Invalid Language",
		Text: "templated message", URN: "whatsapp:250788123123",
		Error:    `unable to decode template: {"templating": { "template": { "name": "revive_issue", "uuid": "8ca114b4-bee2-4d3b-aaf1-9aa6b48d41e8" }, "language": "bnt", "variables": ["Chef", "tomorrow"]}} for channel: 8eb23e93-5ecb-45ba-b726-3b064e0c56ab: unable to find mapping for language: bnt`,
		Metadata: json.RawMessage(`{"templating": { "template": { "name": "revive_issue", "uuid": "8ca114b4-bee2-4d3b-aaf1-9aa6b48d41e8" }, "language": "bnt", "variables": ["Chef", "tomorrow"]}}`),
	},
	{Label: "WhatsApp Contact Error",
		Text: "contact status error", URN: "whatsapp:250788123123",
		Status: "E",
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"text","text":{"body":"contact status error"}}`,
			}: MockedResponse{
				Status: 404,
				Body:   `{"errors": [{"code":1006,"title":"Resource not found","details":"unknown contact"}]}`,
			},
			MockedRequest{
				Method: "POST",
				Path:   "/v1/contacts",
				Body:   `{"blocking":"wait","contacts":["+250788123123"],"force_check":true}`,
			}: MockedResponse{
				Status: 200,
				Body:   `{"contacts":[{"input":"+250788123123","status":"invalid"}]}`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Try Messaging Again After WhatsApp Contact Check",
		Text: "try again", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"text","text":{"body":"try again"}}`,
			}: MockedResponse{
				Status: 404,
				Body:   `{"errors": [{"code": 1006, "title": "Resource not found", "details": "unknown contact"}]}`,
			},
			MockedRequest{
				Method: "POST",
				Path:   "/v1/contacts",
				Body:   `{"blocking":"wait","contacts":["+250788123123"],"force_check":true}`,
			}: MockedResponse{
				Status: 200,
				Body:   `{"contacts": [{"input": "+250788123123", "status": "valid", "wa_id": "250788123123"}]}`,
			},
			MockedRequest{
				Method:   "POST",
				Path:     "/v1/messages",
				RawQuery: "retry=1",
				Body:     `{"to":"250788123123","type":"text","text":{"body":"try again"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{"messages": [{"id": "157b5e14568e8"}]}`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Try Messaging Again After WhatsApp Contact Check With Returned WhatsApp ID",
		Text: "try again", URN: "whatsapp:5582999887766",
		Status: "W", ExternalID: "157b5e14568e8",
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"5582999887766","type":"text","text":{"body":"try again"}}`,
			}: MockedResponse{
				Status: 404,
				Body:   `{"errors": [{"code": 1006, "title": "Resource not found", "details": "unknown contact"}]}`,
			},
			MockedRequest{
				Method: "POST",
				Path:   "/v1/contacts",
				Body:   `{"blocking":"wait","contacts":["+5582999887766"],"force_check":true}`,
			}: MockedResponse{
				Status: 200,
				Body:   `{"contacts": [{"input": "+5582999887766", "status": "valid", "wa_id": "558299887766"}]}`,
			},
			MockedRequest{
				Method:   "POST",
				Path:     "/v1/messages",
				RawQuery: "retry=1",
				Body:     `{"to":"558299887766","type":"text","text":{"body":"try again"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{"messages": [{"id": "157b5e14568e8"}]}`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Interactive Button Message Send",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive List Message Send",
		Text: "Interactive List Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"ROW1", "ROW2", "ROW3", "ROW4"},
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "pt-BR",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"list","body":{"text":"Interactive List Msg"},"action":{"button":"Menu","sections":[{"rows":[{"id":"0","title":"ROW1"},{"id":"1","title":"ROW2"},{"id":"2","title":"ROW3"},{"id":"3","title":"ROW4"}]}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send with Slashes",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"\\BUTTON1", "/BUTTON2", "\\\\BUTTON3"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"\\BUTTON1"}},{"type":"reply","reply":{"id":"1","title":"/BUTTON2"}},{"type":"reply","reply":{"id":"2","title":"\\BUTTON3"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send Without Media", Metadata: json.RawMessage(`{"footer": "footer is here!", "header_text": "Header text"}`),
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"text","text":"Header text","video":{},"image":{},"document":{}},"body":{"text":"Interactive Button Msg"},"footer":{"text":"footer is here!"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive List Message Send Without Media",
		Text: "Interactive List Msg", URN: "whatsapp:250788123123", Metadata: json.RawMessage(`{"interaction_type":"list","footer":"footer is here!","list_message":{"button_text":"Selecionar Item","list_items":[{"uuid":"123e4567-e89b-12d3-a456-426614174000","title":"Item 1","description":"Descrição do Item 1"},{"uuid":"223e4567-e89b-12d3-a456-426614174001","title":"Item 2","description":"Descrição do Item 2"},{"uuid":"323e4567-e89b-12d3-a456-426614174002","title":"Item 3","description":"Descrição do Item 3"}]}}`),
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "pt-BR",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"list","body":{"text":"Interactive List Msg"},"footer":{"text":"footer is here!"},"action":{"button":"Selecionar Item","sections":[{"rows":[{"id":"123e4567-e89b-12d3-a456-426614174000","title":"Item 1","description":"Descrição do Item 1"},{"id":"223e4567-e89b-12d3-a456-426614174001","title":"Item 2","description":"Descrição do Item 2"},{"id":"323e4567-e89b-12d3-a456-426614174002","title":"Item 3","description":"Descrição do Item 3"}]}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send With Media", Metadata: json.RawMessage(`{"footer": "footer is here!", "header_text": "Header text"}`),
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"}, Attachments: []string{"video/mp4:https://foo.bar/video.mp4"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"video","video":{"link":"https://foo.bar/video.mp4"},"image":{},"document":{}},"body":{"text":"Interactive Button Msg"},"footer":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Media Message Template Send - Image",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"wa_template_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"image","image":{"link":"https://foo.bar/image.jpg"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Media Message Template Send - Video",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"video/mp4:https://foo.bar/video.mp4"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"wa_template_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"video","video":{"link":"https://foo.bar/video.mp4"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Media Message Template Send - Document",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"application/pdf:https://foo.bar/document.pdf"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"250788123123","type":"template","template":{"namespace":"wa_template_namespace","name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"document","document":{"link":"https://foo.bar/document.pdf","filename":"document.pdf"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Update URN with wa_id returned",
		Text: "Simple Message", URN: "whatsapp:5511987654321", Path: "/v1/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "contacts":[{"input":"5511987654321","wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"5511987654321","type":"text","text":{"body":"Simple Message"}}`,
		SendPrep:    setSendURL,
		NewURN:      "whatsapp:551187654321"},
	{Label: "Catalog Message Send 1 product",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "products":[{"Product": "Product1","ProductRetailerIDs":["p90duct-23t41l32-1D"]}], "action": "View Products", "send_catalog":false}`),
		Text:     "Catalog Msg", URN: "whatsapp:5511987654321",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"5511987654321","type":"interactive","interactive":{"type":"product","body":{"text":"Catalog Body Msg"},"action":{"catalog_id":"c4t4l0g-1D","product_retailer_id":"p90duct-23t41l32-1D","name":"View Products"}}}`,
		SendPrep:    setSendURL},
	{Label: "Catalog Message Send 2 products",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "products": [{"Product": "product1","ProductRetailerIDs":["p1"]},{"Product": "product2","ProductRetailerIDs":["p2"]}], "action": "View Products", "send_catalog":false}`),
		Text:     "Catalog Msg", URN: "whatsapp:5511987654321",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"5511987654321","type":"interactive","interactive":{"type":"product_list","body":{"text":"Catalog Body Msg"},"action":{"sections":[{"title":"product1","product_items":[{"product_retailer_id":"p1"}]},{"title":"product2","product_items":[{"product_retailer_id":"p2"}]}],"catalog_id":"c4t4l0g-1D","name":"View Products"}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Product Catalog",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "action": "View Products", "send_catalog":true}`),
		Text:     "Catalog Msg", URN: "whatsapp:5511987654321",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"to":"5511987654321","type":"interactive","interactive":{"type":"catalog_message","body":{"text":"Catalog Body Msg"},"action":{"name":"catalog_message"}}}`,
		SendPrep:    setSendURL},
}

var mediaCacheSendTestCases = []ChannelSendTestCase{
	{Label: "Media Upload Error",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document.pdf"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/media",
				Body:   "media bytes",
			}: MockedResponse{
				Status: 401,
				Body:   `{ "errors": [{"code":1005,"title":"Access denied","details":"Invalid credentials."}] }`,
			},
			MockedRequest{
				Method:       "POST",
				Path:         "/v1/messages",
				BodyContains: `/document.pdf`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Previous Media Upload Error",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document.pdf"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method:       "POST",
				Path:         "/v1/messages",
				BodyContains: `/document.pdf`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Media Upload OK",
		Text:   "video caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"video/mp4:https://foo.bar/video.mp4"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/media",
				Body:   "media bytes",
			}: MockedResponse{
				Status: 200,
				Body:   `{ "media" : [{"id": "36c484d1-1283-4b94-988d-7276bdec4de2"}] }`,
			},
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"video","video":{"id":"36c484d1-1283-4b94-988d-7276bdec4de2","caption":"video caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Cached Media",
		Text:   "video caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"video/mp4:https://foo.bar/video.mp4"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"video","video":{"id":"36c484d1-1283-4b94-988d-7276bdec4de2","caption":"video caption"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{
		Label:  "Document Upload OK",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document2.pdf"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/media",
				Body:   "media bytes",
			}: MockedResponse{
				Status: 200,
				Body:   `{ "media" : [{"id": "25c484d1-1283-4b94-988d-7276bdec4ef3"}] }`,
			},
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"document","document":{"id":"25c484d1-1283-4b94-988d-7276bdec4ef3","caption":"document caption","filename":"document2.pdf"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
	{Label: "Cached Document",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document2.pdf"},
		Responses: map[MockedRequest]MockedResponse{
			MockedRequest{
				Method: "POST",
				Path:   "/v1/messages",
				Body:   `{"to":"250788123123","type":"document","document":{"id":"25c484d1-1283-4b94-988d-7276bdec4ef3","caption":"document caption","filename":"document2.pdf"}}`,
			}: MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL,
	},
}

var hsmSupportSendTestCases = []ChannelSendTestCase{
	{Label: "Template Send",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"to":"250788123123","type":"hsm","hsm":{"namespace":"waba_namespace","element_name":"revive_issue","language":{"policy":"deterministic","code":"en"},"localizable_params":[{"default":"Chef"},{"default":"tomorrow"}]}}`,
		SendPrep:    setSendURL,
	},
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

func TestSending(t *testing.T) {
	var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "WA", "250788383383", "US",
		map[string]interface{}{
			"auth_token":   "token123",
			"base_url":     "https://foo.bar/",
			"fb_namespace": "waba_namespace",
			"version":      "v2.35.2",
			"catalog_id":   "c4t4l0g-1D",
		})

	var hsmSupportChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "WA", "250788383383", "US",
		map[string]interface{}{
			"auth_token":   "token123",
			"base_url":     "https://foo.bar/",
			"fb_namespace": "waba_namespace",
			"hsm_support":  true,
			"version":      "v2.35.2",
			"catalog_id":   "c4t4l0g-1D",
		})

	var d3Channel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "D3", "250788383383", "US",
		map[string]interface{}{
			"auth_token":   "token123",
			"base_url":     "https://foo.bar/",
			"fb_namespace": "waba_namespace",
			"version":      "v2.35.2",
			"catalog_id":   "c4t4l0g-1D",
		})

	var txwChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "TXW", "250788383383", "US",
		map[string]interface{}{
			"auth_token":   "token123",
			"base_url":     "https://foo.bar/",
			"fb_namespace": "waba_namespace",
			"version":      "v2.35.2",
			"catalog_id":   "c4t4l0g-1D",
		})

	RunChannelSendTestCases(t, defaultChannel, newWAHandler(courier.ChannelType("WA"), "WhatsApp"), defaultSendTestCases, nil)
	RunChannelSendTestCases(t, hsmSupportChannel, newWAHandler(courier.ChannelType("WA"), "WhatsApp"), hsmSupportSendTestCases, nil)
	RunChannelSendTestCases(t, d3Channel, newWAHandler(courier.ChannelType("D3"), "360Dialog"), defaultSendTestCases, nil)
	RunChannelSendTestCases(t, txwChannel, newWAHandler(courier.ChannelType("TXW"), "TextIt"), defaultSendTestCases, nil)

	mediaServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		res.WriteHeader(200)
		res.Write([]byte("media bytes"))
	}))
	defer mediaServer.Close()
	mediaCacheSendTestCases := mockAttachmentURLs(mediaServer, mediaCacheSendTestCases)

	RunChannelSendTestCases(t, defaultChannel, newWAHandler(courier.ChannelType("WA"), "WhatsApp"), mediaCacheSendTestCases, nil)
}
