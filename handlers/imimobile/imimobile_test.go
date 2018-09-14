package imimobile

import (
	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"net/http/httptest"
	"strings"
	"testing"
)

var testChannels = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "IMI", "2020", "IN", map[string]interface{}{"username": "imi-username", "password": "imi-password", "api-key": "123456"}),
}

var (
	receiveURL = "/c/imi/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive"
	validReceive = "msisdn=254791541111&sms=Msg&tid=20170503&src=765939061"
	invalidURN = "msisdn=invalid&sms=Msg&tid=20170503&src=765939061"
	missingMessage = "msisdn=invalid&tid=20170503&src=765939061"
	missingMobileNumber = "sms=Msg&tid=20170503&src=765939061"
	missingTransactionId = "msisdn=254791541111&sms=Msg&src=765939061"
	missingShortcode = "msisdn=254791541111&sms=Msg&tid=20170503"
	invalidData = `{}`
)

var testCases = []ChannelHandleTestCase{
	{Label: "Receive Valid", URL: receiveURL, Data: validReceive, Status: 200,
	 Response: "Message Accepted", Text: Sp("Msg"), URN: Sp("tel:+254791541111")},
	{Label: "Invalid URN", URL: receiveURL, Data: invalidURN, Status: 400,
	 Response: "phone number supplied is not a number"},
	{Label: "Missing message", URL: receiveURL, Data: missingMessage, Status: 400,
	 Response: "validation for 'Message' failed on the 'required'"},
	{Label: "Missing mobile number", URL: receiveURL, Data: missingMobileNumber, Status: 400,
	 Response: "validation for 'MobileNumber' failed on the 'required'"},
	{Label: "Missing transaction ID", URL: receiveURL, Data: missingTransactionId, Status: 400,
	 Response: "validation for 'TransactionId' failed on the 'required'"},
	{Label: "Missing shortcode", URL: receiveURL, Data: missingShortcode, Status: 400,
	 Response: "validation for 'Shortcode' failed on the 'required'"},
	{Label: "Invalid data", URL: receiveURL, Data: invalidData, Status: 400},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), testCases)
}

func BenchmarkHandler(b *testing.B) {
	RunChannelBenchmarks(b, testChannels, newHandler(), testCases)
}

// setSendURL takes care of setting the sendURL to call
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	sendURL = s.URL
}

var defaultSendTestCases = []ChannelSendTestCase{
	{Label: "Plain Send",
		Text:           "Simple Message ☺",
		URN:            "tel:+250788383383",
		Status:         "W",
		ExternalID:     "",
		ResponseBody:   `{"transid": "001_1235","status_code": "000","status_desc": "Success","refId": "636725493361935289_35"}`,
		ResponseStatus: 200,
		SendPrep:    setSendURL},
	{Label: "Long Send",
		Text: strings.Repeat("This is a very long message", 10),
		URN: "tel:+250788383383",
		Status: "W",
		ExternalID: "",
		ResponseBody: `{"transid": "001_1235","status_code": "000","status_desc": "Success","refId": "636725493361935289_35"}`,
		ResponseStatus: 200,
		SendPrep:    setSendURL},
	{Label: "Send Attachment",
		Text: "MMS is not supported",
		URN: "tel:+250788383383",
		Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Status: "E",
		ExternalID: "",
		ResponseBody: "",
		ResponseStatus: 400,
		SendPrep: setSendURL},
	{Label: "Invalid Parameters",
		Text: "Invalid Parameters",
		URN: "tel:+250788383383",
		Status: "E",
		ResponseBody: "",
		ResponseStatus: 400,
		SendPrep: setSendURL},
	{Label: "Error Response",
		Text: "Error Response",
		URN: "tel:+250788383383",
		Status: "E",
		ResponseBody: "",
		ResponseStatus: 400,
		SendPrep: setSendURL},
}

func TestSending(t *testing.T) {
	var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "IMI", "2020", "IN", map[string]interface{}{"username": "imi-username", "password": "imi-password", "api_key": "123456"})
	RunChannelSendTestCases(t, defaultChannel, newHandler(), defaultSendTestCases, nil)
}
