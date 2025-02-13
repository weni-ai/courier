package wac

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
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/handlers/meta"
	"github.com/nyaruka/courier/handlers/meta/metacommons"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"gopkg.in/go-playground/assert.v1"
)

var testChannelsWAC = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c568c", "WAC", "12345", "", map[string]interface{}{courier.ConfigAuthToken: "a123", "webhook": `{"url": "https://webhook.site", "method": "POST", "headers": {}}`}),
}

func addValidSignatureWAC(r *http.Request) {
	body, _ := handlers.ReadBody(r, 100000)
	sig, _ := metacommons.FBCalculateSignature("wac_app_secret", body)
	r.Header.Set(metacommons.SignatureHeader, fmt.Sprintf("sha1=%s", string(sig)))
}

func TestDescribeWAC(t *testing.T) {
	handler := meta.NewHandler("WAC", "Cloud API WhatsApp", false).(courier.URNDescriber)

	tcs := []struct {
		urn      urns.URN
		metadata map[string]string
	}{{"whatsapp:1337", map[string]string{}},
		{"whatsapp:4567", map[string]string{}}}

	for _, tc := range tcs {
		metadata, _ := handler.DescribeURN(context.Background(), testChannelsWAC[0], tc.urn)
		assert.Equal(t, metadata, tc.metadata)
	}
}

func TestResolveMediaURL(t *testing.T) {

	tcs := []struct {
		id    string
		token string
		url   string
		err   string
	}{{"id_media", "", "", "missing token for WAC channel"},
		{"id_media", "token", "", `unsupported protocol scheme ""`}}

	meta.GraphURL = "url"

	for _, tc := range tcs {
		_, err := meta.ResolveMediaURL(testChannelsWAC[0], tc.id, tc.token)
		assert.Equal(t, err.Error(), tc.err)
	}
}

var wacReceiveURL = "/c/wac/receive"

var testCasesWAC = []handlers.ChannelHandleTestCase{
	{Label: "Receive Message WAC", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/helloWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Hello World"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Duplicate Valid Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/duplicateWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Hello World"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},

	{Label: "Receive Valid Voice Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/voiceWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp(""), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Voice"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},

	{Label: "Receive Valid Button Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/buttonWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("No"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},

	{Label: "Receive Referral WAC", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/referralWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Hello World"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), Metadata: handlers.Jp(&struct {
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
		}{Headline: "Our new product", Body: "This is a great product", SourceType: "SOURCE_TYPE", SourceID: "SOURCE_ID", SourceURL: "SOURCE_URL", Image: nil, Video: nil}),
		PrepRequest: addValidSignatureWAC},

	{Label: "Receive Order WAC", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/orderWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), Metadata: handlers.Jp(map[string]interface{}{
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
		}),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive NFM Reply WAC", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/flowWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), Metadata: handlers.Jp(map[string]interface{}{"nfm_reply": map[string]interface{}{
			"name":          "Flow Wpp",
			"response_json": map[string]interface{}{"flow_token": "<FLOW_TOKEN>", "optional_param1": "<value1>", "optional_param2": "<value2>"},
		}}),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Document Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/documentWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("80skaraokesonglistartist"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Document"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Image Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/imageWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Check out my new phone!"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Image"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Sticker Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/stickerWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp(""), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Sticker"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Video Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/videoWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Check out my new phone!"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Video"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Audio Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/audioWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Check out my new phone!"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Attachment: handlers.Sp("https://foo.bar/attachmentURL_Audio"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Location Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/locationWAC.json")), Status: 200, Response: `"type":"msg"`,
		Text: handlers.Sp(""), Attachment: handlers.Sp("geo:0.000000,1.000000;name:Main Street Beach;address:Main Street Beach, Santa Cruz, CA"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Interactive Button Reply Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/buttonReplyWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Yes"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Interactive List Reply Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/listReplyWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Yes"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)),
		PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Contact Message", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/contactWAC.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("+1 415-858-6273, +1 415-858-6274"), URN: handlers.Sp("whatsapp:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 1, 30, 1, 57, 9, 0, time.UTC)), PrepRequest: addValidSignatureWAC},
	{Label: "Receive Invalid JSON", URL: wacReceiveURL, Data: "not json", Status: 400, Response: "unable to parse", PrepRequest: addValidSignatureWAC},
	{Label: "Receive Invalid JSON", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/invalidFrom.json")), Status: 400, Response: "invalid whatsapp id", PrepRequest: addValidSignatureWAC},
	{Label: "Receive Invalid JSON", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/invalidTimestamp.json")), Status: 400, Response: "invalid timestamp", PrepRequest: addValidSignatureWAC},

	{Label: "Receive Valid Status", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/validStatusWAC.json")), Status: 200, Response: `"type":"status"`,
		MsgStatus: handlers.Sp("S"), ExternalID: handlers.Sp("external_id"), PrepRequest: addValidSignatureWAC},
	{Label: "Receive Valid Delivered Status", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/validDeliveredStatusWAC.json")), Status: 200, Response: `"type":"status"`,
		MsgStatus: handlers.Sp("D"), ExternalID: handlers.Sp("external_id"), PrepRequest: addValidSignatureWAC},
	{Label: "Receive Invalid Status", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/invalidStatusWAC.json")), Status: 400, Response: `"unknown status: in_orbit"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Ignore Status", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/ignoreStatusWAC.json")), Status: 200, Response: `"ignoring status: deleted"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Not Changes", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/notchangesWAC.json")), Status: 400, Response: `"no changes found"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Not Channel Address", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/notchanneladdressWAC.json")), Status: 400, Response: `"no channel address found"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Empty Entry", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/emptyEntryWAC.json")), Status: 400, Response: `"no entries found"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Empty Changes", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/emptyChangesWAC.json")), Status: 200, Response: `"Events Handled"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Empty Contacts", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/emptyContactsWAC.json")), Status: 400, Response: `"no shared contact"`, PrepRequest: addValidSignatureWAC},
	{Label: "Receive Unsupported Message Type", URL: wacReceiveURL, Data: string(courier.ReadFile("./testdata/wac/invalidTypeMsgWAC.json")), Status: 200, Response: `"Events Handled"`, PrepRequest: addValidSignatureWAC},
}

func TestHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessToken := r.Header.Get("Authorization")
		defer r.Body.Close()

		// invalid auth token
		if accessToken != "Bearer a123" && accessToken != "Bearer wac_admin_system_user_token" {
			fmt.Printf("Access token: %s\n", accessToken)
			http.Error(w, "invalid auth token", 403)
			return
		}

		if strings.HasSuffix(r.URL.Path, "image") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Image"}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "audio") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Audio"}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "voice") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Voice"}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "video") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Video"}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "document") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Document"}`))
			return
		}

		if strings.HasSuffix(r.URL.Path, "sticker") {
			w.Write([]byte(`{"url": "https://foo.bar/attachmentURL_Sticker"}`))
		}

		// valid token
		w.Write([]byte(`{"url": "https://foo.bar/attachmentURL"}`))

	}))
	meta.GraphURL = server.URL

	handlers.RunChannelTestCases(t, testChannelsWAC, meta.NewHandler("WAC", "Cloud API WhatsApp", false), testCasesWAC)
}

func TestVerify(t *testing.T) {
	handlers.RunChannelTestCases(t, testChannelsWAC, meta.NewHandler("WAC", "WhatsApp Cloud", false), []handlers.ChannelHandleTestCase{
		{Label: "Valid Secret", URL: "/c/wac/receive?hub.mode=subscribe&hub.verify_token=wac_webhook_secret&hub.challenge=yarchallenge", Status: 200,
			Response: "yarchallenge", NoQueueErrorCheck: true, NoInvalidChannelCheck: true},
		{Label: "Verify No Mode", URL: "/c/wac/receive", Status: 400, Response: "unknown request"},
		{Label: "Verify No Secret", URL: "/c/wac/receive?hub.mode=subscribe", Status: 400, Response: "token does not match secret"},
		{Label: "Invalid Secret", URL: "/c/wac/receive?hub.mode=subscribe&hub.verify_token=blah", Status: 400, Response: "token does not match secret"},
		{Label: "Valid Secret", URL: "/c/wac/receive?hub.mode=subscribe&hub.verify_token=wac_webhook_secret&hub.challenge=yarchallenge", Status: 200, Response: "yarchallenge"},
	})
}

// setSendURL takes care of setting the send_url to our test server host
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	meta.SendURL = s.URL
	meta.GraphURL = s.URL
}

var SendTestCasesWAC = []handlers.ChannelSendTestCase{
	{Label: "Plain Send",
		Text: "Simple Message", URN: "whatsapp:250788123123", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"Simple Message"}}`,
		SendPrep:    setSendURL},
	{Label: "Unicode Send",
		Text: "☺", URN: "whatsapp:250788123123", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"☺"}}`,
		SendPrep:    setSendURL},
	{Label: "Audio Send",
		Text:   "audio caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"audio/mpeg:https://foo.bar/audio.mp3"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"audio","audio":{"link":"https://foo.bar/audio.mp3"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"audio caption"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Sticker Send",
		Text:   "sticker caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"image/webp:https://foo.bar/sticker.webp"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"sticker","sticker":{"link":"https://foo.bar/sticker.webp"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"sticker caption"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Document Send",
		Text:   "document caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document.pdf"}, ResponseStatus: 201,
		ResponseBody: `{ "contacts":[{"input":"5511987654321", "wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`,
		RequestBody:  `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"document","document":{"link":"https://foo.bar/document.pdf","caption":"document caption","filename":"document.pdf"}}`,
		SendPrep:     setSendURL},
	{Label: "Image Send",
		Text:   "image caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "contacts":[{"input":"5511987654321", "wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"image caption"}}`,
		SendPrep:    setSendURL},
	{Label: "Video Send",
		Text:   "video caption",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"video/mp4:https://foo.bar/video.mp4"},
		ResponseBody: `{ "contacts":[{"input":"5511987654321", "wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"video","video":{"link":"https://foo.bar/video.mp4","caption":"video caption"}}`,
		SendPrep:    setSendURL},
	{Label: "Template Send",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]}]}}`,
		SendPrep:    setSendURL,
	},
	{Label: "Template Country Language",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]}]}}`,
		SendPrep:    setSendURL,
	},
	{Label: "Template Invalid Language",
		Text: "templated message", URN: "whatsapp:250788123123",
		Error:    `unable to decode template: {"templating": { "template": { "name": "revive_issue", "uuid": "8ca114b4-bee2-4d3b-aaf1-9aa6b48d41e8" }, "language": "bnt", "variables": ["Chef", "tomorrow"]}} for channel: 8eb23e93-5ecb-45ba-b726-3b064e0c56ab: unable to find mapping for language: bnt`,
		Metadata: json.RawMessage(`{"templating": { "template": { "name": "revive_issue", "uuid": "8ca114b4-bee2-4d3b-aaf1-9aa6b48d41e8" }, "language": "bnt", "variables": ["Chef", "tomorrow"]}}`),
	},
	{Label: "Interactive Button Message Send",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send with Slashes",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"\\\\BUTTON1", "/BUTTON2", "\\/BUTTON3"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"\\BUTTON1"}},{"type":"reply","reply":{"id":"1","title":"/BUTTON2"}},{"type":"reply","reply":{"id":"2","title":"/BUTTON3"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive List Message Send",
		Text: "Interactive List Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"ROW1", "ROW2", "ROW3", "ROW4"},
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"list","body":{"text":"Interactive List Msg"},"action":{"button":"Menu","sections":[{"rows":[{"id":"0","title":"ROW1"},{"id":"1","title":"ROW2"},{"id":"2","title":"ROW3"},{"id":"3","title":"ROW4"}]}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send with attachment",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		RequestBody:  `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"image","image":{"link":"https://foo.bar/image.jpg"}},"body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		SendPrep: setSendURL},
	{Label: "Interactive List Message Send with attachment",
		Text: "Interactive List Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"ROW1", "ROW2", "ROW3", "ROW4"},
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"list","body":{"text":"Interactive List Msg"},"action":{"button":"Menu","sections":[{"rows":[{"id":"0","title":"ROW1"},{"id":"1","title":"ROW2"},{"id":"2","title":"ROW3"},{"id":"3","title":"ROW4"}]}]}}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Interactive Button Message Send with audio attachment",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON0", "BUTTON1", "BUTTON2"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"audio/mp3:https://foo.bar/audio.mp3"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"audio","audio":{"link":"https://foo.bar/audio.mp3"}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			handlers.MockedRequest{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON0"}},{"type":"reply","reply":{"id":"1","title":"BUTTON1"}},{"type":"reply","reply":{"id":"2","title":"BUTTON2"}}]}}}`,
			}: handlers.MockedResponse{
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Media Message Template Send - Image",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"image","image":{"link":"https://foo.bar/image.jpg"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Media Message Template Send - Video",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"video/mp4:https://foo.bar/video.mp4"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"video","video":{"link":"https://foo.bar/video.mp4"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Media Message Template Send - Document",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments:  []string{"application/pdf:https://foo.bar/document.pdf"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"document","document":{"link":"https://foo.bar/document.pdf","filename":"document.pdf"}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Link Sending",
		Text: "Link Sending https://link.com", URN: "whatsapp:250788123123", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"Link Sending https://link.com","preview_url":true}}`,
		SendPrep:    setSendURL},
	{Label: "Update URN with wa_id returned",
		Text: "Simple Message", URN: "whatsapp:5511987654321", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "contacts":[{"input":"5511987654321", "wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"5511987654321","type":"text","text":{"body":"Simple Message"}}`,
		SendPrep:    setSendURL,
		NewURN:      "whatsapp:551187654321"},
	{Label: "Attachment with Caption",
		Text: "Simple Message", URN: "whatsapp:5511987654321", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "contacts":[{"input":"5511987654321", "wa_id":"551187654321"}], "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"5511987654321","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"Simple Message"}}`,
		SendPrep:    setSendURL,
		NewURN:      "whatsapp:551187654321"},
	{Label: "Catalog Message Send 1 product",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "products":[{"Product": "Product1","ProductRetailerIDs":["p90duct-23t41l32-1D"]}], "action": "View Products", "send_catalog":false}`),
		Text:     "Catalog Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"product","body":{"text":"Catalog Body Msg"},"action":{"catalog_id":"c4t4l0g-1D","product_retailer_id":"p90duct-23t41l32-1D","name":"View Products"}}}`,
		SendPrep:    setSendURL},
	{Label: "Catalog Message Send 2 products",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "products": [{"Product": "product1","ProductRetailerIDs":["p1"]},{"Product": "long product name greate than 24","ProductRetailerIDs":["p2"]}], "action": "View Products", "send_catalog":false}`),
		Text:     "Catalog Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"product_list","body":{"text":"Catalog Body Msg"},"action":{"sections":[{"title":"product1","product_items":[{"product_retailer_id":"p1"}]},{"title":"long product name greate","product_items":[{"product_retailer_id":"p2"}]}],"catalog_id":"c4t4l0g-1D","name":"View Products"}}}`,
		SendPrep:    setSendURL},
	{Label: "Catalog Message Send 2 products - With Header - With Footer",
		Metadata: json.RawMessage(`{"header": "header text", "footer": "footer text", "body":"Catalog Body Msg", "products": [{"Product": "product1","ProductRetailerIDs":["p1"]},{"Product": "long product name greate than 24","ProductRetailerIDs":["p2"]}], "action": "View Products", "send_catalog":false}`),
		Text:     "Catalog Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"product_list","header":{"type":"text","text":"header text"},"body":{"text":"Catalog Body Msg"},"footer":{"text":"footer text"},"action":{"sections":[{"title":"product1","product_items":[{"product_retailer_id":"p1"}]},{"title":"long product name greate","product_items":[{"product_retailer_id":"p2"}]}],"catalog_id":"c4t4l0g-1D","name":"View Products"}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Product Catalog",
		Metadata: json.RawMessage(`{"body":"Catalog Body Msg", "action": "View Products", "send_catalog":true}`),
		Text:     "Catalog Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"catalog_message","body":{"text":"Catalog Body Msg"},"action":{"name":"catalog_message"}}}`,
		SendPrep:    setSendURL},
	{Label: "Send CTA Url",
		Metadata: json.RawMessage(`{"cta_message":{"display_text":"link button","url":"https://foo.bar"},"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"cta_url","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"cta_url","header":{"type":"text","text":"header text"},"body":{"text":"msg text"},"footer":{"text":"footer text"},"action":{"name":"cta_url","parameters":{"display_text":"link button","url":"https://foo.bar"}}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Flow Message",
		Metadata: json.RawMessage(`{"flow_message":{"flow_id": "29287124123", "flow_screen": "WELCOME_SCREEN", "flow_cta": "Start Flow", "flow_data": {"name": "John Doe", "list": [1, 2]},"flow_mode":"published"},"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"flow_msg","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"flow","header":{"type":"text","text":"header text"},"body":{"text":"msg text"},"footer":{"text":"footer text"},"action":{"name":"flow","parameters":{"flow_action":"navigate","flow_action_payload":{"data":{"list":[1,2],"name":"John Doe"},"screen":"WELCOME_SCREEN"},"flow_cta":"Start Flow","flow_id":"29287124123","flow_message_version":"3","flow_token":"c00e5d67-c275-4389-aded-7d8b151cbd5b","mode":"published"}}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Flow Message without flow data",
		Metadata: json.RawMessage(`{"flow_message":{"flow_id": "29287124123", "flow_screen": "WELCOME_SCREEN", "flow_cta": "Start Flow", "flow_data": {}, "flow_mode":"published"},"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"flow_msg","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"flow","header":{"type":"text","text":"header text"},"body":{"text":"msg text"},"footer":{"text":"footer text"},"action":{"name":"flow","parameters":{"flow_action":"navigate","flow_action_payload":{"screen":"WELCOME_SCREEN"},"flow_cta":"Start Flow","flow_id":"29287124123","flow_message_version":"3","flow_token":"cdf7ed27-5ad5-4028-b664-880fc7581c77","mode":"published"}}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Order Details Message - With Attachment",
		Metadata: json.RawMessage(`{"order_details_message":{"reference_id":"220788123125","total_amount":18200,"order":{"catalog_id":"14578923723","items":[{"retailer_id":"789236789","name":"item 1","amount":{"offset":100,"value":200},"quantity":2},{"retailer_id":"59016733","name":"item 2","amount":{"offset":100,"value":4000},"quantity":9,"sale_amount":{"offset":100,"value":2000}}],"subtotal":36400,"tax":{"value":500,"description":"tax description"},"shipping":{"value":900,"description":"shipping description"},"discount":{"value":1000,"description":"discount description","program_name":"discount program name"}},"payment_settings":{"type":"digital-goods","payment_link":"https://foo.bar","pix_config":{"key":"pix-key","key_type":"EMAIL","merchant_name":"merchant name","code":"pix-code"}}},"footer":"footer text","header_type":"media","interaction_type":"order_details","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"order_details","header":{"type":"image","image":{"link":"https://foo.bar/image.jpg"}},"body":{"text":"msg text"},"footer":{"text":"footer text"},"action":{"name":"review_and_pay","parameters":{"currency":"BRL","order":{"catalog_id":"c4t4l0g-1D","discount":{"description":"discount description","discount_program_name":"discount program name","offset":100,"value":1000},"items":[{"amount":{"offset":100,"value":200},"name":"item 1","quantity":2,"retailer_id":"789236789"},{"amount":{"offset":100,"value":4000},"name":"item 2","quantity":9,"retailer_id":"59016733","sale_amount":{"offset":100,"value":2000}}],"shipping":{"description":"shipping description","offset":100,"value":900},"status":"pending","subtotal":{"offset":100,"value":36400},"tax":{"description":"tax description","offset":100,"value":500}},"payment_settings":[{"payment_link":{"uri":"https://foo.bar"},"type":"payment_link"},{"pix_dynamic_code":{"code":"pix-code","key":"pix-key","key_type":"EMAIL","merchant_name":"merchant name"},"type":"pix_dynamic_code"}],"payment_type":"br","reference_id":"220788123125","total_amount":{"offset":100,"value":18200},"type":"digital-goods"}}}}`,
		SendPrep:    setSendURL},
	{Label: "Send Order Details Message",
		Metadata: json.RawMessage(`{"order_details_message":{"reference_id":"220788123125","total_amount":18200,"order":{"catalog_id":"14578923723","items":[{"retailer_id":"789236789","name":"item 1","amount":{"offset":100,"value":200},"quantity":2},{"retailer_id":"59016733","name":"item 2","amount":{"offset":100,"value":4000},"quantity":9,"sale_amount":{"offset":100,"value":2000}}],"subtotal":36400,"tax":{"value":500,"description":"tax description"},"shipping":{"value":900,"description":"shipping description"},"discount":{"value":1000,"description":"discount description","program_name":"discount program name"}},"payment_settings":{"type":"digital-goods","payment_link":"https://foo.bar","pix_config":{"key":"pix-key","key_type":"EMAIL","merchant_name":"merchant name","code":"pix-code"}}},"footer":"footer text","interaction_type":"order_details","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"order_details","body":{"text":"msg text"},"footer":{"text":"footer text"},"action":{"name":"review_and_pay","parameters":{"currency":"BRL","order":{"catalog_id":"c4t4l0g-1D","discount":{"description":"discount description","discount_program_name":"discount program name","offset":100,"value":1000},"items":[{"amount":{"offset":100,"value":200},"name":"item 1","quantity":2,"retailer_id":"789236789"},{"amount":{"offset":100,"value":4000},"name":"item 2","quantity":9,"retailer_id":"59016733","sale_amount":{"offset":100,"value":2000}}],"shipping":{"description":"shipping description","offset":100,"value":900},"status":"pending","subtotal":{"offset":100,"value":36400},"tax":{"description":"tax description","offset":100,"value":500}},"payment_settings":[{"payment_link":{"uri":"https://foo.bar"},"type":"payment_link"},{"pix_dynamic_code":{"code":"pix-code","key":"pix-key","key_type":"EMAIL","merchant_name":"merchant name"},"type":"pix_dynamic_code"}],"payment_type":"br","reference_id":"220788123125","total_amount":{"offset":100,"value":18200},"type":"digital-goods"}}}}`,
		SendPrep:    setSendURL},
	{Label: "Message Template - Order Details",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "variables": ["Chef", "tomorrow"]},"order_details_message":{"reference_id":"220788123125","total_amount":18200,"order":{"catalog_id":"14578923723","items":[{"retailer_id":"789236789","name":"item 1","amount":{"offset":100,"value":200},"quantity":2},{"retailer_id":"59016733","name":"item 2","amount":{"offset":100,"value":4000},"quantity":9,"sale_amount":{"offset":100,"value":2000}}],"subtotal":36400,"tax":{"value":500,"description":"tax description"},"shipping":{"value":900,"description":"shipping description"},"discount":{"value":1000,"description":"discount description","program_name":"discount program name"}},"payment_settings":{"type":"digital-goods","payment_link":"https://foo.bar","pix_config":{"key":"pix-key","key_type":"EMAIL","merchant_name":"merchant name","code":"pix-code"}}}}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"button","sub_type":"order_details","index":0,"parameters":[{"type":"action","action":{"order_details":{"reference_id":"220788123125","type":"digital-goods","payment_type":"br","payment_settings":[{"type":"payment_link","payment_link":{"uri":"https://foo.bar"}},{"type":"pix_dynamic_code","pix_dynamic_code":{"code":"pix-code","merchant_name":"merchant name","key":"pix-key","key_type":"EMAIL"}}],"currency":"BRL","total_amount":{"value":18200,"offset":100},"order":{"status":"pending","catalog_id":"c4t4l0g-1D","items":[{"retailer_id":"789236789","name":"item 1","quantity":2,"amount":{"value":200,"offset":100}},{"retailer_id":"59016733","name":"item 2","quantity":9,"amount":{"value":4000,"offset":100},"sale_amount":{"value":2000,"offset":100}}],"subtotal":{"value":36400,"offset":100},"tax":{"value":500,"offset":100,"description":"tax description"},"shipping":{"value":900,"offset":100,"description":"shipping description"},"discount":{"value":1000,"offset":100,"description":"discount description","discount_program_name":"discount program name"}}}}}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Message Template - Buttons",
		Text:   "templated message",
		URN:    "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:     json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "language": "eng", "variables": ["Chef", "tomorrow"]},"buttons":[{"sub_type":"url","parameters":[{"type":"text","text":"first param"}]}]}`),
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 200,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"button","sub_type":"url","index":0,"parameters":[{"type":"text","text":"first param"}]}]}}`,
		SendPrep:    setSendURL},
	{Label: "Plain Send - Link preview on a long message",
		Text: "https://link.com Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Aenean commodo ligula eget dolor. Aenean m",
		URN:  "whatsapp:250788123123", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"https://link.com Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Aenean commodo ligula","preview_url":true}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"eget dolor. Aenean m"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Plain Send - Long message",
		Text: "Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Aenean commodo ligula eget dolor. Aenean massa. Cum sociis natoque.",
		URN:  "whatsapp:250788123123", Path: "/12345_ID/messages",
		Status: "W", ExternalID: "157b5e14568e8",
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"Lorem ipsum dolor sit amet, consectetuer adipiscing elit. Aenean commodo ligula eget dolor. Aenean"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"text","text":{"body":"massa. Cum sociis natoque."}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Interactive Button Message Send - With Footer - With Header",
		Metadata: json.RawMessage(`{"footer":"footer text","header_text":"header text","header_type":"text"}`),
		Text:     "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"text","text":"header text"},"body":{"text":"Interactive Button Msg"},"footer":{"text":"footer text"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send - With Attachment - With Footer",
		Metadata: json.RawMessage(`{"footer":"footer text"}`),
		Text:     "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"image","image":{"link":"https://foo.bar/image.jpg"}},"body":{"text":"Interactive Button Msg"},"footer":{"text":"footer text"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send - With Attachment Video - With Footer",
		Metadata: json.RawMessage(`{"footer":"footer text"}`),
		Text:     "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"video/mp4:https://foo.bar/video.mp4"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"video","video":{"link":"https://foo.bar/video.mp4"}},"body":{"text":"Interactive Button Msg"},"footer":{"text":"footer text"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send - With unknown attachment - With Footer",
		Metadata: json.RawMessage(`{"footer":"footer text"}`),
		Text:     "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments:  []string{"unknown/unknown:https://foo.bar/video.unknown"},
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","body":{"text":"Interactive Button Msg"},"footer":{"text":"footer text"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive List Message Send - With list items - With Header - With Footer",
		Metadata: json.RawMessage(`{"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"list","list_message":{"button_text":"button text","list_items":[{"uuid":"123","title":"title1","description":"description1"},{"uuid":"456","title":"title2"}]}}`),
		Text:     "Interactive List Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"list","header":{"type":"text","text":"header text"},"body":{"text":"Interactive List Msg"},"footer":{"text":"footer text"},"action":{"button":"button text","sections":[{"rows":[{"id":"123","title":"title1","description":"description1"},{"id":"456","title":"title2"}]}]}}}`,
		SendPrep:    setSendURL},
	{Label: "Interactive List Message Send - With list items - With Attachments - With Footer",
		Metadata: json.RawMessage(`{"footer":"footer text","interaction_type":"list","list_message":{"button_text":"button text","list_items":[{"uuid":"123","title":"title1","description":"description1"},{"uuid":"456","title":"title2"}]}}`),
		Text:     "Interactive List Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"list","body":{"text":"Interactive List Msg"},"footer":{"text":"footer text"},"action":{"button":"button text","sections":[{"rows":[{"id":"123","title":"title1","description":"description1"},{"id":"456","title":"title2"}]}]}}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Interactive Location Request",
		Metadata: json.RawMessage(`{"interaction_type":"location"}`),
		Text:     "Interactive Location Request", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		ResponseBody: `{ "messages": [{"id": "157b5e14568e8"}] }`, ResponseStatus: 201,
		RequestBody: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"location_request_message","body":{"text":"Interactive Location Request"},"action":{"name":"send_location"}}}`,
		SendPrep:    setSendURL},
	{Label: "[VERIFY] Interactive Location Request - With attachment - Sending only the attachment (is this ok? shouldn't the location request be a priority?)", // TODO: Verify, is Location + Attachment a valid combination? I believe that the Send WhatsApp Message card does not allow this
		Metadata: json.RawMessage(`{"interaction_type":"location"}`),
		Text:     "Interactive Location Request With Attachment", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8", TextLanguage: "en-US",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"Interactive Location Request With Attachment"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "[VERIFY] Send CTA Url - With Attachment - Sending only the attachment (is this ok too?)", // TODO: Verify, is CTA + Attachment a valid combination? I believe that the Send WhatsApp Message card does not allow this
		Metadata: json.RawMessage(`{"cta_message":{"display_text":"link button","url":"https://foo.bar"},"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"cta_url","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"msg text"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Send Flow Message - With Attachment - Sending only the attachment (is this ok too?)", // TODO: Verify, is Flow + Attachment a valid combination? I believe that the Send WhatsApp Message card does not allow this
		Metadata: json.RawMessage(`{"flow_message":{"flow_id": "29287124123", "flow_screen": "WELCOME_SCREEN", "flow_cta": "Start Flow", "flow_data": {"name": "John Doe", "list": [1, 2]},"flow_mode":"published"},"footer":"footer text","header_text":"header text","header_type":"text","interaction_type":"flow_msg","text":"msgs text"}`),
		Text:     "msg text", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg","caption":"msg text"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Media Message Template Send - Unknown attachment type",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Metadata:    json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments: []string{"unknown/unknown:https://foo.bar/unknown.unknown"},
		Error:       "unknown attachment mime type: unknown",
		SendPrep:    setSendURL},
	{Label: "Interactive Button Message Send with too many replies",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
		Error:    "too many quick replies WAC supports only up to 10 quick replies",
		SendPrep: setSendURL},
	{Label: "Interactive Button Message Send with too many replies and attachments", // TODO: attachment is sent, but the list message fails, is this correct?
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"},
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Error:       "too many quick replies WAC supports only up to 10 quick replies",
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"image","image":{"link":"https://foo.bar/image.jpg"}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Mesage without text and with quick replies and attachments should not be sent",
		Text: "", URN: "whatsapp:250788123123",
		QuickReplies: []string{"Yes", "No"},
		Attachments:  []string{"video/mp4:https://foo.bar/video.mp4"},
		Error:        `message body cannot be empty`,
	},
}

var CachedSendTestCasesWAC = []handlers.ChannelSendTestCase{
	{Label: "Interactive Button Message Send with attachment with cached attachment",
		Text: "Interactive Button Msg", URN: "whatsapp:250788123123", QuickReplies: []string{"BUTTON1"},
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{`application/pdf:https://foo.bar/document.pdf`},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method:       "POST",
				Path:         "/12345_ID/media",
				BodyContains: "media bytes",
			}: {
				Status: 201,
				Body:   `{"id":"157b5e14568e8"}`,
			},
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"interactive","interactive":{"type":"button","header":{"type":"document","document":{"id":"157b5e14568e8","filename":"document.pdf"}},"body":{"text":"Interactive Button Msg"},"action":{"buttons":[{"type":"reply","reply":{"id":"0","title":"BUTTON1"}}]}}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Media Message Template Send - Image with cached attachment",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status:      "W",
		Metadata:    json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method:       "POST",
				Path:         "/12345_ID/media",
				BodyContains: "media bytes",
			}: {
				Status: 201,
				Body:   `{"id":"157b5e14568e8"}`,
			},
			{
				Method:       "POST",
				Path:         "/12345_ID/messages",
				BodyContains: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"image","image":{"id":"157b5e14568e8"}}]}]}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Media Message Template Send - Image with cached attachment failing to upload",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status:      "E",
		Metadata:    json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments: []string{"image/jpeg:https://foo.bar/image2.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method:       "POST",
				Path:         "/12345_ID/media",
				BodyContains: "media bytes",
			}: {
				Status: 400,
				Body:   `error`,
			},
		},
		SendPrep: setSendURL},
	{Label: "Media Message Template Send - Document (Cached)",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Attachments: []string{"application/pdf:https://foo.bar/document2.pdf"},
		Metadata:    json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method:       "POST",
				Path:         "/12345_ID/media",
				BodyContains: "media bytes",
			}: {
				Status: 201,
				Body:   `{"id":"268c6f24679f9"}`,
			},
			{
				Method: "POST",
				Path:   "/12345_ID/messages",
				Body:   `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"document","document":{"id":"268c6f24679f9","filename":"document2.pdf"}}]}]}}`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
}

var FailingCachedSendTestCasesWAC = []handlers.ChannelSendTestCase{
	{Label: "Media Message Template Send - Image with failing cached attachment should send the default attachment URL",
		Text: "Media Message Msg", URN: "whatsapp:250788123123",
		Status: "W", ExternalID: "157b5e14568e8",
		Metadata:    json.RawMessage(`{ "templating": { "template": { "name": "revive_issue", "uuid": "171f8a4d-f725-46d7-85a6-11aceff0bfe3" }, "namespace": "wa_template_namespace", "language": "eng", "country": "US", "variables": ["Chef", "tomorrow"]}}`),
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Responses: map[handlers.MockedRequest]handlers.MockedResponse{
			{
				Method:       "POST",
				Path:         "/12345_ID/messages",
				BodyContains: `{"messaging_product":"whatsapp","recipient_type":"individual","to":"250788123123","type":"template","template":{"name":"revive_issue","language":{"policy":"deterministic","code":"en_US"},"components":[{"type":"body","parameters":[{"type":"text","text":"Chef"},{"type":"text","text":"tomorrow"}]},{"type":"header","parameters":[{"type":"image","image":{"link":"`,
			}: {
				Status: 201,
				Body:   `{ "messages": [{"id": "157b5e14568e8"}] }`,
			},
		},
		SendPrep: setSendURL},
}

func mockAttachmentURLs(mediaServer *httptest.Server, testCases []handlers.ChannelSendTestCase) []handlers.ChannelSendTestCase {
	casesWithMockedUrls := make([]handlers.ChannelSendTestCase, len(testCases))

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
	uuids.SetGenerator(uuids.NewSeededGenerator(1234))
	defer uuids.SetGenerator(uuids.DefaultGenerator)

	mediaServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		res.WriteHeader(200)
		res.Write([]byte("media bytes"))
	}))

	failingMediaServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		defer req.Body.Close()
		res.WriteHeader(400)
		res.Write([]byte("error"))
	}))
	defer mediaServer.Close()
	CachedSendTestCasesWAC := mockAttachmentURLs(mediaServer, CachedSendTestCasesWAC)
	FailingCachedSendTestCasesWAC := mockAttachmentURLs(failingMediaServer, FailingCachedSendTestCasesWAC)
	SendTestCasesWAC = append(SendTestCasesWAC, CachedSendTestCasesWAC...)
	SendTestCasesWAC = append(SendTestCasesWAC, FailingCachedSendTestCasesWAC...)

	// shorter max msg length for testing
	meta.MaxMsgLengthWAC = 100
	var ChannelWAC = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "WAC", "12345_ID", "", map[string]interface{}{courier.ConfigAuthToken: "a123", "catalog_id": "c4t4l0g-1D"})
	handlers.RunChannelSendTestCases(t, ChannelWAC, meta.NewHandler("WAC", "Cloud API WhatsApp", false), SendTestCasesWAC, nil)
}
