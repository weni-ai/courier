package email

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
)

var testChannels = []courier.Channel{
	courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "EM", "support@company.com", "US",
		map[string]interface{}{
			courier.ConfigUsername: "support@company.com",
		}),
}

var defaultChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "EM", "2020", "US",
	map[string]interface{}{
		courier.ConfigUsername: "test@example.com",
	})

const receiveURL = "/c/em/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive"

var (
	plainReceive    = `{"from":"client@example.com","to":"support@company.com","subject":"Hello","body":"Hi there","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`
	messageIDOnly   = `{"from":"client@example.com","to":"support@company.com","subject":"Hi","body":"First message","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<first@example.com>"}`
	threadedReceive = `{"from":"client@example.com","to":"support@company.com","subject":"Re: Pedido #123","body":"Resposta","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<CABc@mail.gmail.com>","in_reply_to":"<root@example.com>","references":["<root@example.com>"]}`
	missingBrackets = `{"from":"client@example.com","to":"support@company.com","subject":"Re:","body":"resp","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"abc@example.com","in_reply_to":"parent@example.com","references":["root@example.com","parent@example.com"]}`
	dupReferences   = `{"from":"client@example.com","to":"support@company.com","subject":"Re:","body":"resp","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<a@x>","in_reply_to":"<root@x>","references":["<root@x>","<root@x>","<mid@x>"]}`
	nullReferences  = `{"from":"client@example.com","to":"support@company.com","subject":"Re:","body":"resp","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<a@x>","in_reply_to":"<b@x>","references":null}`
	emptyReferences = `{"from":"client@example.com","to":"support@company.com","subject":"Re:","body":"resp","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<a@x>","in_reply_to":"<b@x>","references":[]}`
	missingFrom     = `{"to":"support@company.com","body":"hi","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`
	missingBody     = `{"from":"client@example.com","to":"support@company.com","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`
)

var receiveTestCases = []ChannelHandleTestCase{
	{Label: "Receive plain without threading",
		URL: receiveURL, Data: plainReceive, Status: 200, Response: "Message Accepted",
		Text: Sp("Hi there"), URN: Sp("mailto:client@example.com"), ExternalID: Sp("")},

	{Label: "Receive with message_id only",
		URL: receiveURL, Data: messageIDOnly, Status: 200, Response: "Message Accepted",
		Text: Sp("First message"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<first@example.com>")},

	{Label: "Receive with full threading",
		URL: receiveURL, Data: threadedReceive, Status: 200, Response: "Message Accepted",
		Text: Sp("Resposta"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<CABc@mail.gmail.com>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<root@example.com>",
				"references":  []string{"<root@example.com>"},
				"subject":     "Re: Pedido #123",
			},
		})},

	{Label: "Receive normalizes missing angle brackets",
		URL: receiveURL, Data: missingBrackets, Status: 200, Response: "Message Accepted",
		Text: Sp("resp"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<abc@example.com>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<parent@example.com>",
				"references":  []string{"<root@example.com>", "<parent@example.com>"},
				"subject":     "Re:",
			},
		})},

	{Label: "Receive dedupes references",
		URL: receiveURL, Data: dupReferences, Status: 200, Response: "Message Accepted",
		Text: Sp("resp"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<root@x>",
				"references":  []string{"<root@x>", "<mid@x>"},
				"subject":     "Re:",
			},
		})},

	{Label: "Receive accepts null references",
		URL: receiveURL, Data: nullReferences, Status: 200, Response: "Message Accepted",
		Text: Sp("resp"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Receive accepts empty references",
		URL: receiveURL, Data: emptyReferences, Status: 200, Response: "Message Accepted",
		Text: Sp("resp"), URN: Sp("mailto:client@example.com"),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Missing from", URL: receiveURL, Data: missingFrom, Status: 400, Response: "'from' required"},
	{Label: "Missing body", URL: receiveURL, Data: missingBody, Status: 400, Response: "'body' required"},
}

func TestHandler(t *testing.T) {
	RunChannelTestCases(t, testChannels, newHandler(), receiveTestCases)
}

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
	{Label: "Send stores returned message_id as external id",
		Text: "Reply body", URN: "mailto:recipient@example.com",
		Status:       "W",
		ExternalID:   "<generated@your-domain>",
		ResponseBody: `{"message": "Email send requested", "message_id": "<generated@your-domain>"}`, ResponseStatus: 200,
		SendPrep: setSendURL},
	{Label: "Send normalizes returned message_id without brackets",
		Text: "Reply body", URN: "mailto:recipient@example.com",
		Status:       "W",
		ExternalID:   "<generated@your-domain>",
		ResponseBody: `{"message_id": "generated@your-domain"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply with no parent in store sends single-entry references",
		Text: "Reply body", URN: "mailto:recipient@example.com",
		ResponseToExternalID: "<unknown@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"recipient@example.com","body":"Reply body","subject":"Re: Reply body","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<unknown@x>","references":["<unknown@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply with known parent chains references and reuses subject",
		Text: "Reply body", URN: "mailto:client@example.com",
		ResponseToExternalID: "<root@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Reply body","subject":"Re: Pedido #123","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<root@x>","references":["<root@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply extends multi-turn references chain",
		Text: "Body", URN: "mailto:client@example.com",
		ResponseToExternalID: "<turn2@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Body","subject":"Re: Pedido","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<turn2@x>","references":["<root@x>","<turn1@x>","<turn2@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply does not double-prefix Re when parent subject already has it",
		Text: "Body", URN: "mailto:client@example.com",
		ResponseToExternalID: "<existingre@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Body","subject":"Re: Already prefixed","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<existingre@x>","references":["<r@x>","<existingre@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply normalizes ResponseToExternalID without angle brackets",
		Text: "Body", URN: "mailto:client@example.com",
		ResponseToExternalID: "root@x",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Body","subject":"Re: Pedido #123","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<root@x>","references":["<root@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},
}

// seedParents pre-populates the mock backend with parent inbound messages so
// the outbound threading tests can exercise the LookupMsgByExternalID path.
func seedParents(mb *courier.MockBackend) {
	parent := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:client@example.com"), "Pedido #123").
		WithExternalID("<root@x>").
		WithMetadata(json.RawMessage(`{"email":{"references":["<root@x>"],"subject":"Pedido #123"}}`))
	mb.AddMsgByExternalID(parent)

	parent2 := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:client@example.com"), "Pedido").
		WithExternalID("<turn2@x>").
		WithMetadata(json.RawMessage(`{"email":{"references":["<root@x>","<turn1@x>"],"subject":"Pedido"}}`))
	mb.AddMsgByExternalID(parent2)

	parent3 := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:client@example.com"), "X").
		WithExternalID("<existingre@x>").
		WithMetadata(json.RawMessage(`{"email":{"references":["<r@x>"],"subject":"Re: Already prefixed"}}`))
	mb.AddMsgByExternalID(parent3)
}

func TestSending(t *testing.T) {
	RunChannelSendTestCases(t, defaultChannel, newHandler(), defaultSendTestCases, seedParents)
}
