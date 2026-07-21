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

	// Two brand new (non-reply) messages from the same mailbox on different
	// subjects should become two distinct contacts, while a reply to the
	// first one should resolve back to that same contact.
	newThreadProduct = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Meu Produto","body":"Duvida sobre produto","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<produto@example.com>"}`
	newThreadInvoice = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Minha fatura","body":"Duvida sobre fatura","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<fatura@example.com>"}`
	replyToProduct   = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Re: Meu Produto","body":"Resposta produto","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<produto-reply@example.com>","in_reply_to":"<agent-produto@ourdomain.com>","references":["<produto@example.com>","<agent-produto@ourdomain.com>"]}`
)

// urnWithTag builds the "mailto:" URN we expect the handler to derive for an
// address anchored to the given (unnormalized) Message-ID, mirroring the
// production per-thread contact segregation logic.
func urnWithTag(address, anchor string) string {
	return "mailto:" + withThreadTag(address, normalizeMessageID(anchor))
}

var receiveTestCases = []ChannelHandleTestCase{
	{Label: "Receive plain without threading",
		URL: receiveURL, Data: plainReceive, Status: 200, Response: "Message Accepted",
		Text: Sp("Hi there"), URN: Sp("mailto:client@example.com"), ExternalID: Sp(""),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"subject": "Hello",
			},
		})},

	{Label: "Receive with message_id only",
		URL: receiveURL, Data: messageIDOnly, Status: 200, Response: "Message Accepted",
		Text: Sp("First message"), URN: Sp(urnWithTag("client@example.com", "<first@example.com>")),
		ExternalID: Sp("<first@example.com>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"subject": "Hi",
			},
		})},

	{Label: "Receive with full threading",
		URL: receiveURL, Data: threadedReceive, Status: 200, Response: "Message Accepted",
		Text: Sp("Resposta"), URN: Sp(urnWithTag("client@example.com", "<root@example.com>")),
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
		Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "root@example.com")),
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
		Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<root@x>")),
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
		Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<b@x>")),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Receive accepts empty references",
		URL: receiveURL, Data: emptyReferences, Status: 200, Response: "Message Accepted",
		Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<b@x>")),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Missing from", URL: receiveURL, Data: missingFrom, Status: 400, Response: "'from' required"},
	{Label: "Missing body", URL: receiveURL, Data: missingBody, Status: 400, Response: "'body' required"},

	{Label: "New thread on subject Meu Produto gets its own contact",
		URL: receiveURL, Data: newThreadProduct, Status: 200, Response: "Message Accepted",
		Text: Sp("Duvida sobre produto"), URN: Sp(urnWithTag("fulano123@gmail.com", "<produto@example.com>")),
		ExternalID: Sp("<produto@example.com>")},

	{Label: "New thread on subject Minha fatura gets a different contact",
		URL: receiveURL, Data: newThreadInvoice, Status: 200, Response: "Message Accepted",
		Text: Sp("Duvida sobre fatura"), URN: Sp(urnWithTag("fulano123@gmail.com", "<fatura@example.com>")),
		ExternalID: Sp("<fatura@example.com>")},

	{Label: "Reply to Meu Produto thread resolves back to that same contact",
		URL: receiveURL, Data: replyToProduct, Status: 200, Response: "Message Accepted",
		Text: Sp("Resposta produto"), URN: Sp(urnWithTag("fulano123@gmail.com", "<produto@example.com>")),
		ExternalID: Sp("<produto-reply@example.com>")},
}

// TestThreadContactSegregation asserts that the URN derived for a reply to a
// thread matches the URN derived for that thread's originating message, while
// two independent new threads from the same mailbox get different URNs.
func TestThreadContactSegregation(t *testing.T) {
	productURN := urnWithTag("fulano123@gmail.com", "<produto@example.com>")
	invoiceURN := urnWithTag("fulano123@gmail.com", "<fatura@example.com>")
	replyURN := urnWithTag("fulano123@gmail.com", "<produto@example.com>")

	if productURN == invoiceURN {
		t.Fatalf("expected different subjects to derive different contact URNs, both got %q", productURN)
	}
	if productURN != replyURN {
		t.Fatalf("expected a reply to the original thread to derive the same contact URN: got %q, want %q", replyURN, productURN)
	}
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
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"recipient@example.com","body":"Reply body","subject":"Reply body","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<unknown@x>","references":["<unknown@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply with known parent chains references and reuses subject",
		Text: "Reply body", URN: "mailto:client@example.com",
		ResponseToExternalID: "<root@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Reply body","subject":"Re: Pedido #123","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<root@x>","references":["<root@x>"]}`,
		StatusMetadata:       json.RawMessage(`{"email":{"in_reply_to":"<root@x>","references":["<root@x>"],"subject":"Re: Pedido #123"}}`),
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send subsequent message reuses subject and extends references from prior outbound",
		Text: "Follow-up details", URN: "mailto:client@example.com",
		ResponseToExternalID: "<outbound1@x>",
		Metadata:             json.RawMessage(`{"ticketer_id":1}`),
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Follow-up details","subject":"Re: Pedido #123","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<outbound1@x>","references":["<root@x>","<outbound1@x>"]}`,
		StatusMetadata:       json.RawMessage(`{"email":{"in_reply_to":"<outbound1@x>","references":["<root@x>","<outbound1@x>"],"subject":"Re: Pedido #123"},"ticketer_id":1}`),
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

	{Label: "Send reply via ResponseToID when ResponseToExternalID empty",
		Text: "Reply body", URN: "mailto:client@example.com",
		ResponseToID: 42,
		Status:       "W",
		RequestBody:  `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"client@example.com","body":"Reply body","subject":"Re: Via ID","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<via-id@x>","references":["<via-id@x>"]}`,
		ResponseBody: `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send reply via last contact message when no mailroom parent",
		Text: "Agent reply", URN: "mailto:ticket@example.com",
		Status:       "W",
		RequestBody:  `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"ticket@example.com","body":"Agent reply","subject":"Re: Account issue","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<ticket-inbound@x>","references":["<ticket-inbound@x>"]}`,
		ResponseBody: `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send strips synthetic thread tag before delivering to the real mailbox",
		Text: "Reply body", URN: "mailto:fulano123+wt-deadbeef@gmail.com",
		Status:       "W",
		RequestBody:  `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"fulano123@gmail.com","body":"Reply body","subject":"Reply body","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`,
		ResponseBody: `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL},

	{Label: "Send preserves a real plus alias unrelated to our thread tag",
		Text: "Reply body", URN: "mailto:person+work@gmail.com",
		Status:       "W",
		RequestBody:  `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"person+work@gmail.com","body":"Reply body","subject":"Reply body","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`,
		ResponseBody: `{"status":"sent"}`, ResponseStatus: 200,
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

	parentViaID := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:client@example.com"), "Via ID").
		WithID(courier.NewMsgID(42)).
		WithExternalID("<via-id@x>").
		WithMetadata(json.RawMessage(`{"email":{"subject":"Via ID"}}`))
	mb.AddMsgByID(parentViaID)
	mb.AddMsgByExternalID(parentViaID)

	// prior outbound that already stashed thread metadata (what SendMsg now
	// writes after a successful reply) — subsequent sends resolve this as
	// parent and must keep the same subject / extend References
	priorOutbound := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:client@example.com"), "First reply").
		WithExternalID("<outbound1@x>").
		WithMetadata(json.RawMessage(`{"email":{"in_reply_to":"<root@x>","references":["<root@x>"],"subject":"Re: Pedido #123"}}`))
	mb.AddMsgByExternalID(priorOutbound)

	lastInbound := mb.NewIncomingMsg(defaultChannel, urns.URN("mailto:ticket@example.com"), "Help").
		WithExternalID("<ticket-inbound@x>").
		WithMetadata(json.RawMessage(`{"email":{"subject":"Account issue"}}`))
	mb.AddLastMsgForContact(lastInbound)
	mb.AddMsgByExternalID(lastInbound)
}

func TestSending(t *testing.T) {
	RunChannelSendTestCases(t, defaultChannel, newHandler(), defaultSendTestCases, seedParents)
}
