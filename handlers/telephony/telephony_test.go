package telephony

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
)

const (
	channelUUID = "8eb23e93-5ecb-45ba-b726-3b064e0c568c"
	channelDID  = "+15551234567"
)

var testChannels = []courier.Channel{
	courier.NewMockChannel(channelUUID, "TPH", channelDID, "US", map[string]interface{}{
		courier.ConfigAuthToken: "secret",
	}),
}

var receiveURL = "/c/tph/receive"

const receiveMsgTemplate = `
{
  "type": "message",
  "origin": "pstn",
  "did": %q,
  "caller_id": %q,
  "call_id": %q,
  "message": {
    "type": "text",
    "timestamp": %q,
    "text": %q,
    "message_id": %q
  }
}
`

var testCases = []ChannelHandleTestCase{
	{
		Label:                 "Receive Valid Text Message",
		URL:                   receiveURL,
		Data:                  fmt.Sprintf(receiveMsgTemplate, channelDID, "+15559876543", "call-1", "1721567890", "Hello", "turn-1"),
		Status:                200,
		Response:              "Message Accepted",
		NoInvalidChannelCheck: true,
		Text:                  Sp("Hello"),
		URN:                   Sp("tel:+15559876543"),
		ExternalID:            Sp("turn-1"),
		Date:                  Tp(time.Unix(1721567890, 0).UTC()),
	},
	{
		Label:                 "Receive Withheld Caller ID",
		URL:                   receiveURL,
		Data:                  fmt.Sprintf(receiveMsgTemplate, channelDID, "", "call-withheld", "1721567890", "Hello", ""),
		Status:                200,
		Response:              "Message Accepted",
		NoInvalidChannelCheck: true,
		Text:                  Sp("Hello"),
		URN:                   Sp("tel:withheld-call-withheld"),
		Date:                  Tp(time.Unix(1721567890, 0).UTC()),
	},
	{
		Label:                 "Receive Blank Text",
		URL:                   receiveURL,
		Data:                  fmt.Sprintf(receiveMsgTemplate, channelDID, "+15559876543", "call-1", "1721567890", "", ""),
		Status:                400,
		Response:              "blank message text",
		NoInvalidChannelCheck: true,
	},
	{
		Label:                 "Receive Invalid Origin",
		URL:                   receiveURL,
		Data:                  `{"type":"message","origin":"whatsapp","did":"+15551234567","call_id":"call-1","message":{"type":"text","timestamp":"1721567890","text":"Hi"}}`,
		Status:                400,
		Response:              "unsupported origin",
		NoInvalidChannelCheck: true,
	},
	{
		Label:                 "Receive Unknown DID",
		URL:                   receiveURL,
		Data:                  fmt.Sprintf(receiveMsgTemplate, "+19999999999", "+15559876543", "call-1", "1721567890", "Hello", ""),
		Status:                400,
		Response:              "channel not found",
		NoInvalidChannelCheck: true,
	},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), testCases)
}

var sendTestCases = []ChannelSendTestCase{
	{
		Label:   "Plain Send",
		Text:    "Your order is on the way",
		URN:     "tel:+15559876543",
		Headers: map[string]string{"Authorization": "Bearer secret", "Content-Type": "application/json"},
		Path:    "/send",
		Responses: map[MockedRequest]MockedResponse{
			{
				Method:       "POST",
				Path:         "/send",
				BodyContains: `"text":"Your order is on the way"`,
			}: {
				Status: 200,
				Body:   `{"status":"ok"}`,
			},
		},
		Status:   string(courier.MsgSent),
		SendPrep: prepareSendMsg,
	},
	{
		Label: "Missing Base URL",
		Text:  "Hello",
		URN:   "tel:+15559876543",
		Error: "blank base_url",
		SendPrep: func(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
			c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, "")
		},
	},
}

func TestSending(t *testing.T) {
	RunChannelSendTestCases(t, testChannels[0], newHandler(), sendTestCases, nil)
}

func prepareSendMsg(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	c.(*courier.MockChannel).SetConfig(courier.ConfigBaseURL, s.URL)
}
