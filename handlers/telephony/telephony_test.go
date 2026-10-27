package telephony

import (
	"fmt"
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

const resolveToken = "tph-resolve-token"

var (
	receiveURL     = "/c/tph/receive"
	resolveURL     = "/c/tph/resolve?did=%2B15551234567"
	resolveHeaders = map[string]string{"Authorization": "Bearer " + resolveToken}
)

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

var resolveTestCases = []ChannelHandleTestCase{
	{
		Label:                 "Resolve Valid DID",
		URL:                   resolveURL,
		Headers:               resolveHeaders,
		Status:                200,
		Response:              channelUUID,
		NoInvalidChannelCheck: true,
		NoQueueErrorCheck:     true,
	},
	{
		Label:                 "Resolve Unknown DID",
		URL:                   "/c/tph/resolve?did=%2B19999999999",
		Headers:               resolveHeaders,
		Status:                400,
		Response:              "channel not found",
		NoInvalidChannelCheck: true,
		NoQueueErrorCheck:     true,
	},
	{
		Label:                 "Resolve Missing DID",
		URL:                   "/c/tph/resolve",
		Headers:               resolveHeaders,
		Status:                400,
		Response:              "did is required",
		NoInvalidChannelCheck: true,
		NoQueueErrorCheck:     true,
	},
	{
		Label:                 "Resolve Missing Authorization",
		URL:                   resolveURL,
		Status:                400,
		Response:              "missing or invalid authorization",
		NoInvalidChannelCheck: true,
		NoQueueErrorCheck:     true,
	},
	{
		Label:                 "Resolve Invalid Token",
		URL:                   resolveURL,
		Headers:               map[string]string{"Authorization": "Bearer wrong-token"},
		Status:                400,
		Response:              "missing or invalid authorization",
		NoInvalidChannelCheck: true,
		NoQueueErrorCheck:     true,
	},
}

func TestResolveHandler(t *testing.T) {
	mb := courier.NewMockBackend()
	s := NewTestServer(mb)
	s.Config().TelephonyResolveToken = resolveToken
	for _, ch := range testChannels {
		mb.AddChannel(ch)
	}
	if err := newHandler().Initialize(s); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	for _, tc := range resolveTestCases {
		t.Run(tc.Label, func(t *testing.T) {
			TestHandlerRequest(t, s, tc.URL, tc.Headers, tc.Data, tc.MultipartFormFields, tc.Status, &tc.Response, tc.PrepRequest)
		})
	}
}

func TestResolveDisabledWithoutToken(t *testing.T) {
	mb := courier.NewMockBackend()
	s := NewTestServer(mb)
	s.Config().TelephonyResolveToken = ""
	mb.AddChannel(testChannels[0])
	if err := newHandler().Initialize(s); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	response := "resolve endpoint disabled"
	TestHandlerRequest(t, s, resolveURL, resolveHeaders, "", nil, 400, &response, nil)
}

var sendTestCases = []ChannelSendTestCase{
	{
		Label:  "Outbound Acknowledged Without Gateway REST",
		Text:   "Your order is on the way",
		URN:    "tel:+15559876543",
		Status: string(courier.MsgSent),
	},
}

func TestSending(t *testing.T) {
	RunChannelSendTestCases(t, testChannels[0], newHandler(), sendTestCases, nil)
}
