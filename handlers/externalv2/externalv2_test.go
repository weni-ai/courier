package externalv2

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
)

var (
	receiveAttachment = "/c/x2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
	receiveNoParams   = "/c/x2/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive/"
)

var (
	configReceiveTemplateTest       = `{"messages":[{"urn_identity":"{{.from}}","urn_auth":"{{.session_id}}","text":"{{.text}}"{{if .date}},"date":"{{.date}}"{{end}}{{if .media}},"attachments":["{{.media}}"]{{end}}}]}`
	configMOResponseContentTypeTest = "application/json"
	configMOResponseTest            = `{"status":"received"}`
)

var testChannels = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "X2", "2020", "US", map[string]interface{}{
		configReceiveTemplate:       configReceiveTemplateTest,
		configMOResponseContentType: configMOResponseContentTypeTest,
		configMOResponse:            configMOResponseTest,
	}),
}

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

var templateTestCases = []ChannelHandleTestCase{
	{Label: "Receive Valid Template", URL: receiveNoParams, Data: `{"from":"+2349067554729","text":"Join","date":"2017-06-23T12:30:00.500Z"}`, Status: 200, Response: `{"status":"received"}`,
		Text: Sp("Join"), URN: Sp("tel:+2349067554729"), Date: Tp(time.Date(2017, 6, 23, 12, 30, 0, int(500*time.Millisecond), time.UTC))},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), handleTestCases)
}

func TestTemplateHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), templateTestCases)
}

var sendTestCases = []ChannelSendTestCase{
	{Label: "Plain Send",
		Text: "Simple Message", URN: "tel:+250788383383",
		Status:       "W",
		ExternalID:   "",
		ResponseBody: `{"status":"success","message_id":"msg_001"}`, ResponseStatus: 200,
		RequestBody: `{"to":"+250788383383","text":"Simple Message"}`,
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
			configSendTemplate:        `{"to":"{{.contact}}","text":"{{.text}}"{{if .attachments}},"media":{{.attachments}}{{end}}}`,
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
			configSendTemplate:              `{"to":"{{.contact}}","text":"{{.text}}"}`,
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

// BenchmarkHandler runs benchmarks on our handler
func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newHandler(), handleTestCases)
}
