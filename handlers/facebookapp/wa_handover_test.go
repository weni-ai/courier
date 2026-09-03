package facebookapp

import (
	"strings"
	"testing"

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
