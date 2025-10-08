package email

import (
	"net/http/httptest"
	"testing"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
)

// setSendURL sets the email proxy URL for testing
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	sendURL = s.URL
	authToken = "test-auth-token"
}

var defaultSendTestCases = []ChannelSendTestCase{
	{Label: "Test Subject",
		Text: "Title\nSubtitle\nBody Content", URN: "mailto:recipient@example.com",
		Status:       "W",
		RequestBody:  `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"recipient@example.com","body":"Title\nSubtitle\nBody Content","subject":"Title","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`,
		ResponseBody: `{"status": "sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},
	{Label: "Plain Send",
		Text: "Simple email message", URN: "mailto:recipient@example.com",
		Status:       "W",
		ResponseBody: `{"status": "sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},
	{Label: "Send with Attachments",
		Text: "Email with attachments", URN: "mailto:recipient@example.com",
		Attachments:  []string{"image/jpeg:https://foo.bar/image.jpg", "application/pdf:https://foo.bar/doc.pdf"},
		Status:       "W",
		ResponseBody: `{"status": "sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},
	{Label: "Send Error - API Error",
		Text: "Error message", URN: "mailto:recipient@example.com",
		Status:       "E",
		ResponseBody: `{"error": "API Error"}`, ResponseStatus: 500,
		SendPrep: setSendURL},
}

func TestSending(t *testing.T) {
	var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "EM", "2020", "US",
		map[string]interface{}{
			courier.ConfigUsername: "test@example.com",
		})

	RunChannelSendTestCases(t, defaultChannel, newHandler(), defaultSendTestCases, nil)
}
