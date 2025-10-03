package externalv2

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
)

var (
	receiveAttachment = "/c/e2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
	receiveNoParams   = "/c/e2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
)

var (
	configReceiveTemplateTest       = `{"messages":[{"urn_path":"{{.from}}","urn_auth":"{{.session_id}}","text":"{{.text}}"{{if .date}},"date":"{{.date}}"{{end}}{{if .media}},"attachments":["{{.media}}"]{{end}}}]}`
	configReceiveTemplateTest2      = `{"messages":[{"urn_path":"{{.message.from.id}}","text":"{{.message.text}}"{{if .date}},"date":"{{.date}}"{{end}},"contact_name":"{{.message.from.username}}","id":"{{.message.message_id}}"}]}`
	configMOResponseContentTypeTest = "application/json"
	configMOResponseTest            = `{"status":"received"}`
)

var testChannels = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US", map[string]interface{}{
		configReceiveTemplate:       configReceiveTemplateTest,
		configMOResponseContentType: configMOResponseContentTypeTest,
		configMOResponse:            configMOResponseTest,
	}),
}

var testChannels2 = []courier.Channel{
	courier.NewMockChannel("e7152f4f-7189-4458-a91a-747d5404b50a", "E2", "2020", "US", map[string]interface{}{
		configReceiveTemplate:       configReceiveTemplateTest2,
		configMOResponseContentType: configMOResponseContentTypeTest,
		configMOResponse:            `{"status":"Accepted"}`,
	}).WithSchemes([]string{"telegram"}),
}

var helloMsg = `{
  "update_id": 174114370,
  "message": {
	"message_id": 41,
	"from": {
		"id": 3527065,
		"first_name": "John",
		"last_name": "Doe",
		"username": "johndoe"
	},
	"chat": {
		"id": 3527065,
		"first_name": "John",
		"last_name": "Doe",
		"type": "private"
	},
	"date": 1454119029,
	"text": "Hello World"
  }
}`

var handleTestCases = []ChannelHandleTestCase{
	{
		Label: "Receive Valid Message With Attachment", URL: receiveAttachment,
		Data:   `{"from":"+2349067554729","text":"Join","media":"https://example.com/image.jpg"}`,
		Status: 200, Response: `{"status":"received"}`,
		Text: Sp("Join"), URN: Sp("tel:+2349067554729"), Attachments: []string{"https://example.com/image.jpg"},
	},
	{
		Label: "Receive Valid Message With Contact Session", URL: receiveNoParams,
		Data:   `{"from":"+2349067554729", "session_id":"1234567890","text":"Join","date":"2017-06-23T12:30:00.500Z"}`,
		Status: 200, Response: `{"status":"received"}`,
		URNAuth: Sp("1234567890"), URN: Sp("tel:+2349067554729"),
		Text: Sp("Join"), Date: Tp(time.Date(2017, 6, 23, 12, 30, 0, int(500*time.Millisecond), time.UTC)),
	},
}

var handleTestCases2 = []ChannelHandleTestCase{
	{Label: "Receive Valid Message", URL: "/c/e2/e7152f4f-7189-4458-a91a-747d5404b50a/receive/",
		Data: helloMsg, Status: 200, Response: "Accepted",
		Name: Sp("johndoe"), Text: Sp("Hello World"), URN: Sp("telegram:3527065"),
		ExternalID: Sp("41"),
	},
}

var templateTestCases = []ChannelHandleTestCase{
	{Label: "Receive Valid Template", URL: receiveNoParams, Data: `{"from":"+2349067554729","text":"Join","date":"2017-06-23T12:30:00.500Z"}`, Status: 200, Response: `{"status":"received"}`,
		Text: Sp("Join"), URN: Sp("tel:+2349067554729"), Date: Tp(time.Date(2017, 6, 23, 12, 30, 0, int(500*time.Millisecond), time.UTC))},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), handleTestCases)
}

func TestHandler2(t *testing.T) {
	RunChannelTestCases(t, testChannels2, newHandler(), handleTestCases2)
}

func TestTemplateHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), templateTestCases)
}

func TestHandleWithUrnAuth(t *testing.T) {
	var channel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US",
		map[string]interface{}{
			configReceiveTemplate:       `{"messages":[{"urn_path":"{{.from}}","urn_auth":"{{.session_id}}","text":"{{.text}}"}]}`,
			configMOResponseContentType: configMOResponseContentTypeTest,
			configMOResponse:            configMOResponseTest,
		}).WithSchemes([]string{"whatsapp"})

	testCases := []ChannelHandleTestCase{
		{Label: "Receive Valid Message With URN Auth", URL: receiveNoParams,
			Data:   `{"from":"2349067554729", "session_id":"1234567890","text":"Join"}`,
			Status: 200, Response: `{"status":"received"}`,
			URNAuth: Sp("1234567890"), URN: Sp("whatsapp:2349067554729"),
			Text: Sp("Join"),
		},
	}

	RunChannelTestCases(t, []courier.Channel{channel}, newHandler(), testCases)
}

func TestHandleWithUrnAuth2(t *testing.T) {
	var channel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "E2", "2020", "US",
		map[string]interface{}{
			configReceiveTemplate:       `{"messages":[{"urn_path":"{{.urn_path}}","text":"{{.text}}","urn_auth":"{{.urn_auth}}"}]}`,
			configMOResponseContentType: configMOResponseContentTypeTest,
			configMOResponse:            configMOResponseTest,
		}).WithSchemes([]string{"whatsapp"})

	testCases := []ChannelHandleTestCase{
		{Label: "Receive Valid Message With URN Auth", URL: receiveNoParams,
			Data:   `{"urn_path":"2349067554729", "urn_auth":"1234567890","text":"Join"}`,
			Status: 200, Response: `{"status":"received"}`,
			URNAuth: Sp("1234567890"), URN: Sp("whatsapp:2349067554729"),
			Text: Sp("Join"),
		},
	}

	RunChannelTestCases(t, []courier.Channel{channel}, newHandler(), testCases)
}

var sendTestCases = []ChannelSendTestCase{
	{Label: "Plain Send",
		Text: "Simple Message", URN: "tel:+250788383383",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_001"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"Simple Message"}`,
		SendPrep:    setSendURL},
	{Label: "Plain Send with URN auth",
		Text: "Simple Message", URN: "tel:+250788383383", URNAuth: "1234567890",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_001"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"Simple Message","session_id":"1234567890"}`,
		SendPrep:    setSendURL},
	{Label: "Unicode Send",
		Text: "☺", URN: "tel:+250788383383",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_002"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"☺"}`,
		SendPrep:    setSendURL},
	{Label: "Error Sending",
		Text: "Error Message", URN: "tel:+250788383383",
		Status:       "E",
		ExternalID:   "",
		ResponseBody: `{"status":"error","message":"failed"}`, ResponseStatus: 401,
		SendPrep: setSendURL},
	{Label: "Send Attachment",
		Text: "My pic!", URN: "tel:+250788383383", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_003"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"My pic!","media":[image/jpeg:https://foo.bar/image.jpg]}`,
		SendPrep:    setSendURL},
}

func TestSending(t *testing.T) {
	var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:     "http://example.com/send",
			courier.ConfigSendMethod:  "POST",
			configSendTemplate:        `{"to":"{{.urn.path}}","text":"{{.text}}"{{if .attachments}},"media":{{.attachments}}{{end}}{{if .urn_auth}},"session_id":"{{.urn_auth}}"{{end}}}`,
			courier.ConfigContentType: "json",
			configMTResponseCheck:     "",
		})

	RunChannelSendTestCases(t, defaultChannel, newHandler(), sendTestCases, nil)
}

// TestCustomSending tests custom template sending
// Note: Custom templates with urlencoded content type are complex to test
// as they depend on specific server configurations

func TestSendingWithAuth(t *testing.T) {
	var authChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:           "http://example.com/send",
			courier.ConfigSendMethod:        "POST",
			courier.ConfigSendAuthorization: "Bearer secret123",
			configSendTemplate:              `{"to":"{{.urn.path}}","text":"{{.text}}"}`,
			courier.ConfigContentType:       "json",
		})

	authTestCases := []ChannelSendTestCase{
		{Label: "Send with Auth",
			Text: "Auth Message", URN: "tel:+250788383383",
			Status:       "W",
			ExternalID:   "",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Auth Message"}`,
			Headers:     map[string]string{"Authorization": "Bearer secret123"},
			SendPrep:    setSendURL},
	}

	RunChannelSendTestCases(t, authChannel, newHandler(), authTestCases, nil)
}

func TestSendingInParts(t *testing.T) {
	var partsChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendURL:       "http://example.com/send",
			courier.ConfigSendMethod:    "POST",
			configSendTemplate:          `{"to":"{{.urn.path}}","text":"{{.text}}",{{range .attachments}}"media":"{{attURL .}}"{{end}}}`,
			courier.ConfigContentType:   "json",
			configSendAttachmentInParts: true,
			configSendMediaURL:          "http://example.com/media",
		})

	partsTestCases := []ChannelSendTestCase{
		{Label: "Send Text and Media in Parts",
			Text: "Check this out", URN: "tel:+250788383383", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
			Status:       "W",
			ExternalID:   "",
			RequestBody:  `{"to":"+250788383383","text":"Check this out","media":"https://foo.bar/image.jpg"}`,
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			SendPrep: setSendURL},
	}

	RunChannelSendTestCases(t, partsChannel, newHandler(), partsTestCases, nil)
}

func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(courier.ConfigSendURL, s.URL)
}

func TestSimpleSend(t *testing.T) {
	var simpleSendChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendMethod:  "POST",
			courier.ConfigContentType: "json",
			configSendTemplate:        `{"contact": "{{.urn.path}}", "text": "{{.text}}", "urn_auth": "{{.urn_auth}}"}`,
		})

	simpleSendTestCases := []ChannelSendTestCase{
		{Label: "Simple Send",
			Text: "Simple Message", URN: "tel:+250788383383",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"contact": "+250788383383", "text": "Simple Message", "urn_auth": "1234567890"}`,
			SendPrep:    setSendURL,
			URNAuth:     "1234567890",
		},
	}

	RunChannelSendTestCases(t, simpleSendChannel, newHandler(), simpleSendTestCases, nil)
}

func TestSendingWithCustomURL(t *testing.T) {
	var customURLChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US",
		map[string]interface{}{
			courier.ConfigSendMethod:  "POST",
			courier.ConfigContentType: "json",
			configSendTemplate:        `{"to":"{{.urn.path}}","text":"{{.text}}","urn_auth":"{{.urn_auth}}"}`,
		})

	customURLTestCases := []ChannelSendTestCase{
		{Label: "Send with Custom URL with Query param",
			Text: "Simple Message", URN: "tel:+250788383383",
			URNAuth:      "DUMMY_URN_AUTH_VALUE",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Simple Message","urn_auth":"DUMMY_URN_AUTH_VALUE"}`,
			SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
				c.(*courier.MockChannel).SetConfig(configSendUrlTemplate, s.URL+"?custom_param={{.urn_auth}}")
			},
			URLParams: map[string]string{"custom_param": "DUMMY_URN_AUTH_VALUE"},
		},
		{Label: "Send with Custom URL",
			Text: "Simple Message", URN: "tel:+250788383383",
			URNAuth:      "DUMMY_URN_AUTH_VALUE",
			Status:       "W",
			ResponseBody: `{"status":"success"}`, ResponseStatus: 200,
			RequestBody: `{"to":"+250788383383","text":"Simple Message","urn_auth":"DUMMY_URN_AUTH_VALUE"}`,
			SendPrep:    setSendCustomURL,
			Path:        "/DUMMY_URN_AUTH_VALUE",
		},
	}

	RunChannelSendTestCases(t, customURLChannel, newHandler(), customURLTestCases, nil)
}

func setSendCustomURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(configSendUrlTemplate, s.URL+"/{{.urn_auth}}")
}

// BenchmarkHandler runs benchmarks on our handler
func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newHandler(), handleTestCases)
}
