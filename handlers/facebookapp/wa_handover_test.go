package facebookapp

import (
	"strings"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/stretchr/testify/assert"
)

func TestRenderWAHandoverContextText(t *testing.T) {
	tcs := []struct {
		label       string
		ctx         *wacConversationContext
		contextType string
		contextText string
		ok          bool
	}{
		{
			label: "summary",
			ctx: &wacConversationContext{
				Summary: &struct {
					Text string `json:"text"`
				}{Text: "  Customer asked about pricing.  "},
			},
			contextType: courier.WAHandoverContextSummary,
			contextText: "Customer asked about pricing.",
			ok:          true,
		},
		{
			label: "history transcript",
			ctx: &wacConversationContext{
				History: &struct {
					Items []wacHistoryItem `json:"items"`
				}{
					Items: []wacHistoryItem{
						{From: "user", Type: "text", Text: &struct{ Body string `json:"body"` }{Body: "Hi"}},
						{Role: "business", Type: "text", Text: &struct{ Body string `json:"body"` }{Body: "Hello"}},
						{From: "user", Type: "image"},
					},
				},
			},
			contextType: courier.WAHandoverContextHistory,
			contextText: "[user] Hi\n[business] Hello\n[user] <image>",
			ok:          true,
		},
		{
			label: "empty summary and history",
			ctx: &wacConversationContext{
				Summary: &struct {
					Text string `json:"text"`
				}{Text: "   "},
			},
			ok: false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			contextType, contextText, ok := renderWAHandoverContextText(tc.ctx)
			assert.Equal(t, tc.ok, ok)
			if ok {
				assert.Equal(t, tc.contextType, contextType)
				assert.Equal(t, tc.contextText, contextText)
			}
		})
	}
}

func TestTruncateWAHandoverContextText(t *testing.T) {
	longText := strings.Repeat("a", maxWAHandoverContextTextLen+10)
	truncated := truncateWAHandoverContextText(longText)

	assert.True(t, strings.HasPrefix(truncated, "[truncated]"))
	assert.Equal(t, maxWAHandoverContextTextLen+len("[truncated]"), len([]rune(truncated)))
}

func TestMessagingHandoverLogFields(t *testing.T) {
	occurredOn := time.Unix(1454119029, 0).UTC()
	value := wacHandoverValue{
		Type:      wacHandoverTypeControlPassed,
		Timestamp: "1454119029",
		Sender:    &wacHandoverSender{WaID: "5678"},
		ControlPassed: &wacHandoverControlPassed{
			Metadata: "customer requested agent",
			PreviousOwner: &struct {
				AppID      string `json:"app_id"`
				AppRole    string `json:"app_role"`
				BusinessID string `json:"business_id"`
			}{
				AppID:      "prev-app",
				AppRole:    "partner",
				BusinessID: "biz-1",
			},
		},
		ConversationContext: &wacConversationContext{
			History: &struct {
				Items []wacHistoryItem `json:"items"`
			}{
				Items: []wacHistoryItem{
					{From: "user", Type: "text", Text: &struct{ Body string `json:"body"` }{Body: "Hi"}},
				},
			},
		},
	}

	fields := messagingHandoverLogFields(value, occurredOn, "5678", "Kerry Fisher", courier.WAHandoverContextHistory, "persisted")

	assert.Equal(t, wacHandoverTypeControlPassed, fields["handover_type"])
	assert.Equal(t, occurredOn, fields["occurred_on"])
	assert.Equal(t, "persisted", fields["outcome"])
	assert.Equal(t, "5678", fields["sender_wa_id"])
	assert.Equal(t, "5678", fields["contact_urn"])
	assert.Equal(t, "Kerry Fisher", fields["contact_name"])
	assert.Equal(t, courier.WAHandoverContextHistory, fields["context_type"])
	assert.Equal(t, 1, fields["history_item_count"])
	assert.Equal(t, "customer requested agent", fields["handover_metadata"])
	assert.Equal(t, "prev-app", fields["previous_owner_app_id"])
	assert.NotContains(t, fields, "context_text")
}

func TestResolveWAHandoverConversationContext(t *testing.T) {
	topLevel := &wacConversationContext{
		Summary: &struct {
			Text string `json:"text"`
		}{Text: "top level"},
	}
	nested := &wacConversationContext{
		Summary: &struct {
			Text string `json:"text"`
		}{Text: "nested"},
	}

	assert.Equal(t, topLevel, resolveWAHandoverConversationContext(wacHandoverValue{
		ConversationContext: topLevel,
		ControlPassed: &wacHandoverControlPassed{
			ConversationContext: nested,
		},
	}))
	assert.Equal(t, nested, resolveWAHandoverConversationContext(wacHandoverValue{
		ControlPassed: &wacHandoverControlPassed{
			ConversationContext: nested,
		},
	}))
	assert.Nil(t, resolveWAHandoverConversationContext(wacHandoverValue{}))
}

func TestResolveWAHandoverSenderURN(t *testing.T) {
	urn, err := resolveWAHandoverSenderURN(&wacHandoverSender{PhoneNumber: "558893565901"})
	assert.NoError(t, err)
	assert.Equal(t, "558893565901", urn.Path())

	_, err = resolveWAHandoverSenderURN(&wacHandoverSender{})
	assert.Error(t, err)
}

func TestWACPhoneNumberID(t *testing.T) {
	metadata := &struct {
		DisplayPhoneNumber string `json:"display_phone_number"`
		PhoneNumberID      string `json:"phone_number_id"`
	}{PhoneNumberID: "meta-id"}
	recipient := &wacHandoverRecipient{PhoneNumberID: "recipient-id"}

	assert.Equal(t, "meta-id", wacPhoneNumberID(metadata, recipient))
	assert.Equal(t, "recipient-id", wacPhoneNumberID(nil, recipient))
	assert.Equal(t, "", wacPhoneNumberID(nil, nil))
}
