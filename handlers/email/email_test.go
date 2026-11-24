package email

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaruka/courier"
	. "github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
	"github.com/stretchr/testify/require"
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
	spamNewThread   = `{"from":"spam@evil.com","to":"support@company.com","subject":"Buy now","body":"spam body","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<spam-new@evil.com>"}`

	// Two brand new (non-reply) messages from the same mailbox on different
	// subjects should become two distinct contacts, while a reply to the
	// first one should resolve back to that same contact.
	newThreadProduct = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Meu Produto","body":"Duvida sobre produto","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<produto@example.com>"}`
	newThreadInvoice = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Minha fatura","body":"Duvida sobre fatura","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<fatura@example.com>"}`
	replyToProduct   = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Re: Meu Produto","body":"Resposta produto","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<produto-reply@example.com>","in_reply_to":"<agent-produto@ourdomain.com>","references":["<produto@example.com>","<agent-produto@ourdomain.com>"]}`
	legacyReply      = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Re: Oi","body":"Resposta do contato legado","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<legacy-reply@example.com>","in_reply_to":"<agent-legacy@ourdomain.com>","references":["<agent-legacy@ourdomain.com>"]}`
	legacyRefsReply  = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Re: Oi","body":"Resposta citada na cadeia","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<legacy-refs@example.com>","references":["<unknown-root@example.com>","<agent-legacy@ourdomain.com>"]}`
	legacyCaseReply  = `{"from":"Fulano123@Gmail.com","to":"support@company.com","subject":"Re: Oi","body":"Resposta com caixa diferente","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<legacy-case@example.com>","in_reply_to":"<agent-legacy@ourdomain.com>"}`
	mixedParentReply = `{"from":"fulano123@gmail.com","to":"support@company.com","subject":"Re: Meu Produto","body":"Resposta da thread","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<mixed@example.com>","in_reply_to":"<agent-produto@ourdomain.com>","references":["<produto@example.com>","<agent-legacy@ourdomain.com>","<agent-produto@ourdomain.com>"]}`
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
		Name: Sp("client@example.com"), Text: Sp("Hi there"), URN: Sp("mailto:client@example.com"), ExternalID: Sp(""),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"subject": "Hello",
			},
		})},

	{Label: "Receive with message_id only",
		URL: receiveURL, Data: messageIDOnly, Status: 200, Response: "Message Accepted",
		Name: Sp("client@example.com"), Text: Sp("First message"), URN: Sp(urnWithTag("client@example.com", "<first@example.com>")),
		ExternalID: Sp("<first@example.com>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"subject": "Hi",
			},
		})},

	{Label: "Receive with full threading",
		URL: receiveURL, Data: threadedReceive, Status: 200, Response: "Message Accepted",
		Name: Sp("client@example.com"), Text: Sp("Resposta"), URN: Sp(urnWithTag("client@example.com", "<root@example.com>")),
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
		Name: Sp("client@example.com"), Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "root@example.com")),
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
		Name: Sp("client@example.com"), Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<root@x>")),
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
		Name: Sp("client@example.com"), Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<b@x>")),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Receive accepts empty references",
		URL: receiveURL, Data: emptyReferences, Status: 200, Response: "Message Accepted",
		Name: Sp("client@example.com"), Text: Sp("resp"), URN: Sp(urnWithTag("client@example.com", "<b@x>")),
		ExternalID: Sp("<a@x>"),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<b@x>",
				"subject":     "Re:",
			},
		})},

	{Label: "Missing from", URL: receiveURL, Data: missingFrom, Status: 400, Response: "'from' required"},
	{Label: "Missing body", URL: receiveURL, Data: missingBody, Status: 400, Response: "'body' required"},

	// Blocking any virtual contact for a mailbox must stop a brand-new thread
	// from the same real address (server returns empty 200, no queued msg).
	{Label: "Receive from blocked mailbox is ignored",
		URL: receiveURL, Data: spamNewThread, Status: 200, Response: "",
		PrepBackend: func(mb *courier.MockBackend) {
			mb.SetEmailMailboxBlocked("spam@evil.com", true)
		}},

	{Label: "New thread on subject Meu Produto gets its own contact",
		URL: receiveURL, Data: newThreadProduct, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Duvida sobre produto"), URN: Sp(urnWithTag("fulano123@gmail.com", "<produto@example.com>")),
		ExternalID: Sp("<produto@example.com>")},

	{Label: "New thread on subject Minha fatura gets a different contact",
		URL: receiveURL, Data: newThreadInvoice, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Duvida sobre fatura"), URN: Sp(urnWithTag("fulano123@gmail.com", "<fatura@example.com>")),
		ExternalID: Sp("<fatura@example.com>")},

	{Label: "Reply to Meu Produto thread resolves back to that same contact",
		URL: receiveURL, Data: replyToProduct, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta produto"), URN: Sp(urnWithTag("fulano123@gmail.com", "<produto@example.com>")),
		ExternalID: Sp("<produto-reply@example.com>")},

	{Label: "Reply to a legacy contact reuses the plain URN",
		URL: receiveURL, Data: legacyReply, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta do contato legado"),
		URN:        Sp("mailto:fulano123@gmail.com"),
		ExternalID: Sp("<legacy-reply@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123@gmail.com"), "Oi").
				WithExternalID("<agent-legacy@ourdomain.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Reply reuses a legacy contact when only References cites the sent message",
		URL: receiveURL, Data: legacyRefsReply, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta citada na cadeia"),
		URN:        Sp("mailto:fulano123@gmail.com"),
		ExternalID: Sp("<legacy-refs@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123@gmail.com"), "Oi").
				WithExternalID("<agent-legacy@ourdomain.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Reply reuses a legacy contact when From differs only by case",
		URL: receiveURL, Data: legacyCaseReply, Status: 200, Response: "Message Accepted",
		Name: Sp("Fulano123@Gmail.com"), Text: Sp("Resposta com caixa diferente"),
		URN:        Sp("mailto:fulano123@gmail.com"),
		ExternalID: Sp("<legacy-case@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123@gmail.com"), "Oi").
				WithExternalID("<agent-legacy@ourdomain.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Reply that cites another mailbox keeps a new thread URN",
		URL: receiveURL, Data: legacyReply, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta do contato legado"),
		URN:        Sp(urnWithTag("fulano123@gmail.com", "<agent-legacy@ourdomain.com>")),
		ExternalID: Sp("<legacy-reply@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:other@example.com"), "Oi").
				WithExternalID("<agent-legacy@ourdomain.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Direct reply to a threaded message ignores an older legacy reference",
		URL: receiveURL, Data: mixedParentReply, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta da thread"),
		URN:        Sp("mailto:fulano123+wt-aaaaaaaa@gmail.com"),
		ExternalID: Sp("<mixed@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			threaded := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123+wt-aaaaaaaa@gmail.com"), "Produto").
				WithExternalID("<agent-produto@ourdomain.com>")
			mb.AddMsgByExternalID(threaded)
			legacy := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123@gmail.com"), "Oi").
				WithExternalID("<agent-legacy@ourdomain.com>")
			mb.AddMsgByExternalID(legacy)
		}},

	{Label: "Reply that cites a threaded contact keeps the thread URN",
		URL: receiveURL, Data: replyToProduct, Status: 200, Response: "Message Accepted",
		Name: Sp("fulano123@gmail.com"), Text: Sp("Resposta produto"),
		URN:        Sp("mailto:fulano123+wt-aaaaaaaa@gmail.com"),
		ExternalID: Sp("<produto-reply@example.com>"),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN("mailto:fulano123+wt-aaaaaaaa@gmail.com"), "Produto").
				WithExternalID("<agent-produto@ourdomain.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Copy reply cites a stored thread Message-ID and stays on the central URN",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"copy-author@example.com","to":"support@company.com","body":"Estou de acordo","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<copy-reply@example.com>","in_reply_to":"<copy-root@example.com>"}`,
		Name: Sp("copy-opener@example.com"),
		Text: Sp("copy-author@example.com:\n\nEstou de acordo"),
		URN:  Sp(urnWithTag("copy-opener@example.com", "<copy-root@example.com>")),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN(urnWithTag("copy-opener@example.com", "<copy-root@example.com>")), "Abertura").
				WithExternalID("<copy-root@example.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Central author body is stored unchanged",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"Central-Opener@Example.com","to":"support@company.com","body":"Continuo eu","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<central-root@example.com>"}`,
		Name: Sp("Central-Opener@Example.com"),
		Text: Sp("Continuo eu"),
		URN:  Sp(urnWithTag("central-opener@example.com", "<central-root@example.com>")),
		PrepBackend: func(mb *courier.MockBackend) {
			parent := mb.NewIncomingMsg(testChannels[0], urns.URN(urnWithTag("central-opener@example.com", "<central-root@example.com>")), "Abertura").
				WithExternalID("<central-root@example.com>")
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "First message with cc stores the copy list without the channel mailbox or the opener",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"list-opener@example.com","to":"support@company.com","subject":"Pedido","body":"Oi","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<list-first@example.com>","cc":["support@company.com","fulano_de_tal@email.com","other@example.com"]}`,
		Name: Sp("list-opener@example.com"), Text: Sp("Oi"),
		URN: Sp(urnWithTag("list-opener@example.com", "<list-first@example.com>")),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"subject": "Pedido",
				"cc":      []string{"fulano_de_tal@email.com", "other@example.com"},
			},
		})},

	{Label: "Copy reply grows the list and keeps earlier addresses",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"grow-other@example.com","to":"support@company.com","body":"Aguardo retorno","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<grow-root@example.com>","cc":["fulano_de_tal@email.com","third@example.com"]}`,
		Name: Sp("grow-opener@example.com"),
		Text: Sp("grow-other@example.com:\n\nAguardo retorno"),
		URN:  Sp(urnWithTag("grow-opener@example.com", "<grow-root@example.com>")),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<grow-root@example.com>",
				"cc":          []string{"fulano_de_tal@email.com", "third@example.com", "grow-other@example.com"},
			},
		}),
		PrepBackend: func(mb *courier.MockBackend) {
			urn := urns.URN(urnWithTag("grow-opener@example.com", "<grow-root@example.com>"))
			parent := mb.NewIncomingMsg(testChannels[0], urn, "Abertura").WithExternalID("<grow-root@example.com>")
			mb.AddMsgByExternalID(parent)
			latest := mb.NewIncomingMsg(testChannels[0], urn, "antes").
				WithMetadata(json.RawMessage(`{"email":{"cc":["fulano_de_tal@email.com"]}}`))
			mb.AddLastMsgByContactURN(latest)
		}},

	{Label: "Copy list keeps an address the new message no longer repeats",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"subset-opener@example.com","to":"support@company.com","body":"Segue","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<subset-root@example.com>","cc":["new@example.com"]}`,
		Name: Sp("subset-opener@example.com"), Text: Sp("Segue"),
		URN: Sp(urnWithTag("subset-opener@example.com", "<subset-root@example.com>")),
		Metadata: Jp(map[string]interface{}{
			"email": map[string]interface{}{
				"in_reply_to": "<subset-root@example.com>",
				"cc":          []string{"kept@example.com", "new@example.com"},
			},
		}),
		PrepBackend: func(mb *courier.MockBackend) {
			urn := urns.URN(urnWithTag("subset-opener@example.com", "<subset-root@example.com>"))
			parent := mb.NewIncomingMsg(testChannels[0], urn, "Abertura").WithExternalID("<subset-root@example.com>")
			mb.AddMsgByExternalID(parent)
			latest := mb.NewIncomingMsg(testChannels[0], urn, "antes").
				WithMetadata(json.RawMessage(`{"email":{"cc":["kept@example.com"]}}`))
			mb.AddLastMsgByContactURN(latest)
		}},

	{Label: "cc that is not an array is rejected",
		URL: receiveURL, Status: 400, Response: "cannot unmarshal",
		Data: `{"from":"badcc@example.com","to":"support@company.com","body":"oi","cc":"fulano@example.com","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab"}`},

	{Label: "Latest-message lookup failure on receive writes nothing",
		URL: receiveURL, Status: 400, Response: "reading email cc list",
		Data: `{"from":"err-opener@example.com","to":"support@company.com","body":"oi","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<err-root@example.com>"}`,
		PrepBackend: func(mb *courier.MockBackend) {
			urn := urns.URN(urnWithTag("err-opener@example.com", "<err-root@example.com>"))
			parent := mb.NewIncomingMsg(testChannels[0], urn, "Abertura").WithExternalID("<err-root@example.com>")
			mb.AddMsgByExternalID(parent)
			mb.SetLastMsgByContactURNErr(testChannels[0], urn, errors.New("db down"))
		}},

	{Label: "Sender with no stored Message-ID opens a new conversation",
		URL: receiveURL, Status: 200, Response: "Message Accepted",
		Data: `{"from":"cc-only-sender@example.com","to":"support@company.com","body":"Nova conversa","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<newconv@x>"}`,
		Name: Sp("cc-only-sender@example.com"), Text: Sp("Nova conversa"),
		URN: Sp(urnWithTag("cc-only-sender@example.com", "<newconv@x>")),
		PrepBackend: func(mb *courier.MockBackend) {
			other := urns.URN(urnWithTag("someone-else@example.com", "<other-root@x>"))
			parent := mb.NewIncomingMsg(testChannels[0], other, "Outra").
				WithExternalID("<other-root@x>").
				WithMetadata(json.RawMessage(`{"email":{"cc":["cc-only-sender@example.com"]}}`))
			mb.AddMsgByExternalID(parent)
		}},

	{Label: "Blocked copy author is ignored on a thread reply",
		URL: receiveURL, Status: 200, Response: "",
		Data: `{"from":"copy-blocked@example.com","to":"support@company.com","body":"oi","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<blocked-root@example.com>"}`,
		PrepBackend: func(mb *courier.MockBackend) {
			mb.SetEmailMailboxBlocked("copy-blocked@example.com", true)
			urn := urns.URN(urnWithTag("blocked-opener@example.com", "<blocked-root@example.com>"))
			parent := mb.NewIncomingMsg(testChannels[0], urn, "Abertura").WithExternalID("<blocked-root@example.com>")
			mb.AddMsgByExternalID(parent)
		}},
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

func TestRealAddressFromURNPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"spam+wt-a1b2c3d4@evil.com", "spam@evil.com"},
		{"spam@evil.com", "spam@evil.com"},
		{"person+work@gmail.com", "person+work@gmail.com"},
		{"person+work+wt-deadbeef@gmail.com", "person+work@gmail.com"},
	}
	for _, tc := range cases {
		if got := realAddressFromURNPath(tc.in); got != tc.want {
			t.Errorf("realAddressFromURNPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
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

	{Label: "Send uses the central address and the cc of the latest message on the URN",
		Text: "Agent reply", URN: "mailto:send-opener+wt-0123abcd@example.com",
		ResponseToExternalID: "<cc-parent@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"send-opener@example.com","cc":["fulano_de_tal@email.com","other@example.com"],"body":"Agent reply","subject":"Re: Pedido","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<cc-parent@x>","references":["<cc-parent@x>"]}`,
		StatusMetadata:       json.RawMessage(`{"email":{"in_reply_to":"<cc-parent@x>","references":["<cc-parent@x>"],"subject":"Re: Pedido","cc":["fulano_de_tal@email.com","other@example.com"]}}`),
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL,
		PrepBackend: func(mb *courier.MockBackend) {
			urn := urns.URN("mailto:send-opener+wt-0123abcd@example.com")
			parent := mb.NewIncomingMsg(defaultChannel, urn, "Pedido").
				WithExternalID("<cc-parent@x>").
				WithMetadata(json.RawMessage(`{"email":{"subject":"Pedido"}}`))
			mb.AddMsgByExternalID(parent)
			latest := mb.NewIncomingMsg(defaultChannel, urn, "later").
				WithMetadata(json.RawMessage(`{"email":{"cc":["fulano_de_tal@email.com","other@example.com"]}}`))
			mb.AddLastMsgByContactURN(latest)
		}},

	{Label: "Latest message on the URN without cc omits the key",
		Text: "Follow up", URN: "mailto:omit-cc+wt-0123abcd@example.com",
		ResponseToExternalID: "<omit-parent@x>",
		Status:               "W",
		RequestBody:          `{"uuid":"00000000-0000-0000-0000-000000000000","from":"test@example.com","to":"omit-cc@example.com","body":"Follow up","subject":"Re: Pedido","channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","in_reply_to":"<omit-parent@x>","references":["<omit-parent@x>"]}`,
		ResponseBody:         `{"status":"sent"}`, ResponseStatus: 200,
		SendPrep: setSendURL,
		PrepBackend: func(mb *courier.MockBackend) {
			urn := urns.URN("mailto:omit-cc+wt-0123abcd@example.com")
			parent := mb.NewIncomingMsg(defaultChannel, urn, "Pedido").
				WithExternalID("<omit-parent@x>").
				WithMetadata(json.RawMessage(`{"email":{"subject":"Pedido"}}`))
			mb.AddMsgByExternalID(parent)
			latest := mb.NewIncomingMsg(defaultChannel, urn, "later").
				WithMetadata(json.RawMessage(`{"email":{"subject":"Pedido"}}`))
			mb.AddLastMsgByContactURN(latest)
		}},

	{Label: "Latest-message lookup failure on send does not call the proxy",
		Text: "Agent reply", URN: "mailto:fail-lookup+wt-0123abcd@example.com",
		Status:         "E",
		ResponseBody:   `{"status":"sent"}`,
		ResponseStatus: 200,
		SendPrep:       setSendURL,
		PrepBackend: func(mb *courier.MockBackend) {
			mb.SetLastMsgByContactURNErr(defaultChannel, urns.URN("mailto:fail-lookup+wt-0123abcd@example.com"), errors.New("db down"))
		}},
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

func TestMergeEmailMetadata(t *testing.T) {
	emailBlock := json.RawMessage(`{"email":{"in_reply_to":"<a@x>","references":["<a@x>"],"subject":"Re: Hi"}}`)

	t.Run("empty emailBlock returns nil", func(t *testing.T) {
		if got := mergeEmailMetadata(json.RawMessage(`{"ticketer_id":1}`), nil); got != nil {
			t.Fatalf("expected nil, got %s", got)
		}
	})

	t.Run("empty object emailBlock returns nil", func(t *testing.T) {
		if got := mergeEmailMetadata(nil, json.RawMessage(`{}`)); got != nil {
			t.Fatalf("expected nil, got %s", got)
		}
	})

	t.Run("nil existing returns emailBlock", func(t *testing.T) {
		got := mergeEmailMetadata(nil, emailBlock)
		assertJSONEqual(t, emailBlock, got)
	})

	t.Run("preserves unrelated existing keys", func(t *testing.T) {
		existing := json.RawMessage(`{"ticketer_id":1,"chats_msg_uuid":"abc"}`)
		got := mergeEmailMetadata(existing, emailBlock)
		assertJSONEqual(t, json.RawMessage(`{"chats_msg_uuid":"abc","email":{"in_reply_to":"<a@x>","references":["<a@x>"],"subject":"Re: Hi"},"ticketer_id":1}`), got)
	})

	t.Run("overwrites existing email key", func(t *testing.T) {
		existing := json.RawMessage(`{"email":{"subject":"Old"},"ticketer_id":1}`)
		got := mergeEmailMetadata(existing, emailBlock)
		assertJSONEqual(t, json.RawMessage(`{"email":{"in_reply_to":"<a@x>","references":["<a@x>"],"subject":"Re: Hi"},"ticketer_id":1}`), got)
	})

	t.Run("invalid existing falls back to emailBlock", func(t *testing.T) {
		got := mergeEmailMetadata(json.RawMessage(`["not","object"]`), emailBlock)
		assertJSONEqual(t, emailBlock, got)
	})
}

func assertJSONEqual(t *testing.T, want, got json.RawMessage) {
	t.Helper()
	var wantVal, gotVal interface{}
	if err := json.Unmarshal(want, &wantVal); err != nil {
		t.Fatalf("want unmarshal: %v", err)
	}
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("got unmarshal: %v\ngot=%s", err, got)
	}
	wantJSON, _ := json.Marshal(wantVal)
	gotJSON, _ := json.Marshal(gotVal)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("mismatch\nwant: %s\ngot:  %s", wantJSON, gotJSON)
	}
}

func TestAccumulateCopyList(t *testing.T) {
	central := urns.URN("mailto:opener+wt-0123abcd@example.com")
	mailboxes := []string{"support@company.com"}
	base := &moPayload{From: "author@example.com"}

	cases := []struct {
		name    string
		prev    []string
		payload *moPayload
		boxes   []string
		want    []string
		dropped int
	}{
		{
			name:    "previous order kept and payload cc appended",
			prev:    []string{"a@example.com", "b@example.com"},
			payload: &moPayload{From: "author@example.com", CC: []string{"c@example.com"}},
			boxes:   mailboxes,
			want:    []string{"a@example.com", "b@example.com", "c@example.com", "author@example.com"},
		},
		{
			name:    "from not appended when it is the central contact",
			payload: &moPayload{From: "Opener@Example.com", CC: []string{"a@example.com"}},
			boxes:   mailboxes,
			want:    []string{"a@example.com"},
		},
		{
			name:    "channel mailbox and opener dropped",
			payload: &moPayload{From: "author@example.com", CC: []string{"support@company.com", "opener@example.com", "a@example.com"}},
			boxes:   mailboxes,
			want:    []string{"a@example.com", "author@example.com"},
			dropped: 2,
		},
		{
			name:    "inbound To is excluded even when it differs from username",
			payload: &moPayload{From: "author@example.com", To: "alias-box@example.com", CC: []string{"alias-box@example.com", "a@example.com"}},
			boxes:   []string{"support@company.com", "alias-box@example.com"},
			want:    []string{"a@example.com", "author@example.com"},
			dropped: 1,
		},
		{
			name:    "plus tag stripped and case-insensitive first spelling kept",
			prev:    []string{"fulano@x.com"},
			payload: &moPayload{From: "author@example.com", CC: []string{"FULANO+wt-deadbeef@x.com", "  ", "not an email", "Name <bad@x.com>"}},
			boxes:   mailboxes,
			want:    []string{"fulano@x.com", "author@example.com"},
			dropped: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.payload
			if payload == nil {
				payload = base
			}
			got, dropped := accumulateCopyList(tc.prev, payload, central, tc.boxes)
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.dropped, dropped)
		})
	}
}

// TestCopyThreadReceiveThenSend walks the first two steps of the flow: Ana
// opens a thread with people in copy, then the agent answers.
func TestCopyThreadReceiveThenSend(t *testing.T) {
	var sent map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&sent))
		_, _ = w.Write([]byte(`{"status":"sent","message_id":"<r1@proxy>"}`))
	}))
	defer server.Close()

	mb := courier.NewMockBackend()
	mb.AddChannel(defaultChannel)
	cfg := courier.NewConfig()
	cfg.EmailProxyURL = "http://example"
	cfg.EmailProxyAuthToken = "test-auth-token"
	h := newHandler().(*handler)
	require.NoError(t, h.Initialize(courier.NewServer(cfg, mb)))
	sendURL = server.URL
	authToken = "test-auth-token"

	// 1. Ana writes to the channel with two people in copy
	body := `{"from":"ana@cliente.com","to":"test@example.com","subject":"Pedido","body":"Preciso de ajuda",` +
		`"channel_uuid":"8eb23e93-5ecb-45ba-b726-3b064e0c56ab","message_id":"<m1@cliente.com>",` +
		`"cc":["test@example.com","fulano@x.com","outro@y.com"]}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	_, err := h.receiveEvent(context.Background(), defaultChannel, httptest.NewRecorder(), req)
	require.NoError(t, err)

	stored, err := mb.GetLastQueueMsg()
	require.NoError(t, err)
	require.Equal(t, urnWithTag("ana@cliente.com", "<m1@cliente.com>"), string(stored.URN()))
	require.Equal(t, "Preciso de ajuda", stored.Text())

	// the backend would now hold that row as the thread's latest copy list
	mb.AddMsgByExternalID(stored)
	mb.AddLastMsgByContactURN(stored)

	// 2. the agent answers
	out := mb.NewOutgoingMsg(defaultChannel, courier.NewMsgID(20), stored.URN(), "Já estou vendo", false, nil, "", 0, "<m1@cliente.com>", "")
	status, err := h.SendMsg(context.Background(), out)
	require.NoError(t, err)
	require.Equal(t, "W", string(status.Status()))

	require.Equal(t, "ana@cliente.com", sent["to"])
	require.Equal(t, []interface{}{"fulano@x.com", "outro@y.com"}, sent["cc"])
}

func TestSendHTTPErrorDoesNotStoreCopyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"API Error"}`))
	}))
	defer server.Close()

	mb := courier.NewMockBackend()
	mb.AddChannel(defaultChannel)
	cfg := courier.NewConfig()
	cfg.EmailProxyURL = "http://example"
	cfg.EmailProxyAuthToken = "test-auth-token"
	h := newHandler().(*handler)
	require.NoError(t, h.Initialize(courier.NewServer(cfg, mb)))
	sendURL = server.URL
	authToken = "test-auth-token"

	urn := urns.URN("mailto:err-send+wt-0123abcd@example.com")
	latest := mb.NewIncomingMsg(defaultChannel, urn, "antes").
		WithMetadata(json.RawMessage(`{"email":{"cc":["fulano_de_tal@email.com"]}}`))
	mb.AddLastMsgByContactURN(latest)

	msg := mb.NewOutgoingMsg(defaultChannel, courier.NewMsgID(10), urn, "resposta", false, nil, "", 0, "", "")
	status, err := h.SendMsg(context.Background(), msg)
	require.NoError(t, err)
	require.Equal(t, "E", string(status.Status()))
	require.Empty(t, status.Metadata())
}
