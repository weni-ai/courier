package ig

import (
	"context"
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

var testChannelsIG = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c568c", "IG", "12345", "", map[string]interface{}{courier.ConfigAuthToken: "a123"}),
}

var testCasesIG = []handlers.ChannelHandleTestCase{
	{Label: "Receive Message", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/helloMsgIG.json")), Status: 200, Response: "Handled", NoQueueErrorCheck: true, NoInvalidChannelCheck: true,
		Text: handlers.Sp("Hello World"), URN: handlers.Sp("instagram:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 4, 7, 1, 11, 27, 970000000, time.UTC)),
		PrepRequest: addValidSignature},

	{Label: "Receive Invalid Signature", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/helloMsgIG.json")), Status: 400, Response: "invalid request signature", PrepRequest: addInvalidSignature},

	{Label: "No Duplicate Receive Message", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/duplicateMsgIG.json")), Status: 200, Response: "Handled",
		Text: handlers.Sp("Hello World"), URN: handlers.Sp("instagram:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 4, 7, 1, 11, 27, 970000000, time.UTC)),
		PrepRequest: addValidSignature},

	{Label: "Receive Attachment", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/attachmentIG.json")), Status: 200, Response: "Handled",
		Text: handlers.Sp(""), Attachments: []string{"https://image-url/foo.png"}, URN: handlers.Sp("instagram:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 4, 7, 1, 11, 27, 970000000, time.UTC)),
		PrepRequest: addValidSignature},

	{Label: "Receive Like Heart", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/like_heart.json")), Status: 200, Response: "Handled",
		Text: handlers.Sp(""), URN: handlers.Sp("instagram:5678"), ExternalID: handlers.Sp("external_id"), Date: handlers.Tp(time.Date(2016, 4, 7, 1, 11, 27, 970000000, time.UTC)),
		PrepRequest: addValidSignature},

	{Label: "Receive Icebreaker Get Started", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/icebreakerGetStarted.json")), Status: 200, Response: "Handled",
		URN: handlers.Sp("instagram:5678"), Date: handlers.Tp(time.Date(2016, 4, 7, 1, 11, 27, 970000000, time.UTC)), ChannelEvent: handlers.Sp(courier.NewConversation),
		ChannelEventExtra: map[string]interface{}{"title": "icebreaker question", "payload": "get_started"},
		PrepRequest:       addValidSignature},
	{Label: "Different Page", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/differentPageIG.json")), Status: 200, Response: `"data":[]`, PrepRequest: addValidSignature},
	{Label: "Echo", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/echoIG.json")), Status: 200, Response: `ignoring echo`, PrepRequest: addValidSignature},
	{Label: "No Entries", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/noEntriesIG.json")), Status: 400, Response: "no entries found", PrepRequest: addValidSignature},
	{Label: "Not Instagram", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/notInstagram.json")), Status: 400, Response: "object expected 'page', 'instagram' or 'whatsapp_business_account', found notinstagram", PrepRequest: addValidSignature},
	{Label: "No Messaging Entries", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/noMessagingEntriesIG.json")), Status: 200, Response: "Handled", PrepRequest: addValidSignature},
	{Label: "Unknown Messaging Entry", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/unknownMessagingEntryIG.json")), Status: 200, Response: "Handled", PrepRequest: addValidSignature},
	{Label: "Not JSON", URL: "/c/ig/receive", Data: "not JSON", Status: 400, Response: "Error", PrepRequest: addValidSignature},
	{Label: "Invalid URN", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/invalidURNIG.json")), Status: 400, Response: "invalid instagram id", PrepRequest: addValidSignature},
	{Label: "Story Mention", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/storyMentionIG.json")), Status: 200, Response: `ignoring story_mention`, PrepRequest: addValidSignature},
	{Label: "Message unsent", URL: "/c/ig/receive", Data: string(courier.ReadFile("./testdata/ig/unsentMsgIG.json")), Status: 200, Response: `msg deleted`, PrepRequest: addValidSignature},
}

func addValidSignature(r *http.Request) {
	body, _ := handlers.ReadBody(r, 100000)
	sig, _ := metacommons.FBCalculateSignature("fb_app_secret", body)
	r.Header.Set(metacommons.SignatureHeader, fmt.Sprintf("sha1=%s", string(sig)))
}

func addInvalidSignature(r *http.Request) {
	r.Header.Set(metacommons.SignatureHeader, "invalidsig")
}

// mocks the call to the Facebook graph API
func buildMockFBGraphIG(testCases []handlers.ChannelHandleTestCase) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessToken := r.URL.Query().Get("access_token")
		defer r.Body.Close()

		// invalid auth token
		if accessToken != "a123" {
			http.Error(w, "invalid auth token", 403)
		}

		// user has a name
		if strings.HasSuffix(r.URL.Path, "1337") {
			w.Write([]byte(`{ "name": "John Doe"}`))
			return
		}

		// no name
		w.Write([]byte(`{ "name": ""}`))
	}))
	meta.GraphURL = server.URL

	return server
}

func TestDescribeIG(t *testing.T) {
	fbGraph := buildMockFBGraphIG(testCasesIG)
	defer fbGraph.Close()

	handler := meta.NewHandler("IG", "Instagram", false).(courier.URNDescriber)
	tcs := []struct {
		urn      urns.URN
		metadata map[string]string
	}{{"instagram:1337", map[string]string{"name": "John Doe"}},
		{"instagram:4567", map[string]string{"name": ""}}}

	for _, tc := range tcs {
		metadata, _ := handler.DescribeURN(context.Background(), testChannelsIG[0], tc.urn)
		assert.Equal(t, metadata, tc.metadata)
	}
}

func TestHandler(t *testing.T) {
	handlers.RunChannelTestCases(t, testChannelsIG, meta.NewHandler("IG", "Instagram", false), testCasesIG)
}

func BenchmarkHandler(b *testing.B) {
	fbServiceIG := buildMockFBGraphIG(testCasesIG)

	handlers.RunChannelBenchmarks(b, testChannelsIG, meta.NewHandler("IG", "Instagram", false), testCasesIG)
	fbServiceIG.Close()
}

func TestVerify(t *testing.T) {
	handlers.RunChannelTestCases(t, testChannelsIG, meta.NewHandler("IG", "Instagram", false), []handlers.ChannelHandleTestCase{
		{Label: "Valid Secret", URL: "/c/ig/receive?hub.mode=subscribe&hub.verify_token=fb_webhook_secret&hub.challenge=yarchallenge", Status: 200,
			Response: "yarchallenge", NoQueueErrorCheck: true, NoInvalidChannelCheck: true},
		{Label: "Verify No Mode", URL: "/c/ig/receive", Status: 400, Response: "unknown request"},
		{Label: "Verify No Secret", URL: "/c/ig/receive?hub.mode=subscribe", Status: 400, Response: "token does not match secret"},
		{Label: "Invalid Secret", URL: "/c/ig/receive?hub.mode=subscribe&hub.verify_token=blah", Status: 400, Response: "token does not match secret"},
		{Label: "Valid Secret", URL: "/c/ig/receive?hub.mode=subscribe&hub.verify_token=fb_webhook_secret&hub.challenge=yarchallenge", Status: 200, Response: "yarchallenge"},
	})
}

// setSendURL takes care of setting the send_url to our test server host
func setSendURL(s *httptest.Server, h courier.ChannelHandler, c courier.Channel, m courier.Msg) {
	meta.SendURL = s.URL
	meta.GraphURL = s.URL
}

var SendTestCasesIG = []handlers.ChannelSendTestCase{
	{Label: "Plain Send",
		Text: "Simple Message", URN: "instagram:12345",
		Status: "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"UPDATE","recipient":{"id":"12345"},"message":{"text":"Simple Message"}}`,
		SendPrep:    setSendURL},
	{Label: "Plain Response",
		Text: "Simple Message", URN: "instagram:12345",
		Status: "W", ExternalID: "mid.133", ResponseToExternalID: "23526",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"RESPONSE","recipient":{"id":"12345"},"message":{"text":"Simple Message"}}`,
		SendPrep:    setSendURL},
	{Label: "Quick Reply",
		Text: "Are you happy?", URN: "instagram:12345", QuickReplies: []string{"Yes", "No"},
		Status: "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"UPDATE","recipient":{"id":"12345"},"message":{"text":"Are you happy?","quick_replies":[{"title":"Yes","payload":"Yes","content_type":"text"},{"title":"No","payload":"No","content_type":"text"}]}}`,
		SendPrep:    setSendURL},
	{Label: "Long Message",
		Text: "This is a long message which spans more than one part, what will actually be sent in the end if we exceed the max length?",
		URN:  "instagram:12345", QuickReplies: []string{"Yes", "No"}, Topic: "agent",
		Status: "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"MESSAGE_TAG","tag":"HUMAN_AGENT","recipient":{"id":"12345"},"message":{"text":"we exceed the max length?","quick_replies":[{"title":"Yes","payload":"Yes","content_type":"text"},{"title":"No","payload":"No","content_type":"text"}]}}`,
		SendPrep:    setSendURL},
	{Label: "Send Photo",
		URN: "instagram:12345", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		Status: "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"UPDATE","recipient":{"id":"12345"},"message":{"attachment":{"type":"image","payload":{"url":"https://foo.bar/image.jpg","is_reusable":true}}}}`,
		SendPrep:    setSendURL},
	{Label: "Send caption and photo with Quick Reply",
		Text: "This is some text.",
		URN:  "instagram:12345", Attachments: []string{"image/jpeg:https://foo.bar/image.jpg"},
		QuickReplies: []string{"Yes", "No"},
		Status:       "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"UPDATE","recipient":{"id":"12345"},"message":{"text":"This is some text.","quick_replies":[{"title":"Yes","payload":"Yes","content_type":"text"},{"title":"No","payload":"No","content_type":"text"}]}}`,
		SendPrep:    setSendURL},
	{Label: "Tag Human Agent",
		Text: "Simple Message", URN: "instagram:12345",
		Status: "W", ExternalID: "mid.133", Topic: "agent",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"MESSAGE_TAG","tag":"HUMAN_AGENT","recipient":{"id":"12345"},"message":{"text":"Simple Message"}}`,
		SendPrep:    setSendURL},
	{Label: "Send Document",
		URN: "instagram:12345", Attachments: []string{"application/pdf:https://foo.bar/document.pdf"},
		Status: "W", ExternalID: "mid.133",
		ResponseBody: `{"message_id": "mid.133"}`, ResponseStatus: 200,
		RequestBody: `{"messaging_type":"UPDATE","recipient":{"id":"12345"},"message":{"attachment":{"type":"file","payload":{"url":"https://foo.bar/document.pdf","is_reusable":true}}}}`,
		SendPrep:    setSendURL},
	{Label: "ID Error",
		Text: "ID Error", URN: "instagram:12345",
		Status:       "E",
		ResponseBody: `{ "is_error": true }`, ResponseStatus: 200,
		SendPrep: setSendURL},
	{Label: "Error",
		Text: "Error", URN: "instagram:12345",
		Status:       "E",
		ResponseBody: `{ "is_error": true }`, ResponseStatus: 403,
		SendPrep: setSendURL},
}

func TestSending(t *testing.T) {
	uuids.SetGenerator(uuids.NewSeededGenerator(1234))
	defer uuids.SetGenerator(uuids.DefaultGenerator)

	meta.MaxMsgLengthIG = 100
	var ChannelIG = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "IG", "12345", "", map[string]interface{}{courier.ConfigAuthToken: "a123"})
	handlers.RunChannelSendTestCases(t, ChannelIG, meta.NewHandler("IG", "Instagram", false), SendTestCasesIG, nil)

}
