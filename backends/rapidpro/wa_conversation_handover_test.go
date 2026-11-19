package rapidpro

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
)

const whatsappCloudChannelUUID = "dbc126ed-66bc-4e28-b67b-81dc3327c95d"

func newTestWAHandoverEvent(channelUUID courier.ChannelUUID, contextType, contextText string) courier.WAConversationHandoverEvent {
	urn, _ := urns.NewWhatsAppURN("12065551212")
	return courier.WAConversationHandoverEvent{
		ChannelUUID:    channelUUID,
		ContactURN:     urn,
		ContactName:    "Test Contact",
		ContextType:    contextType,
		ContextText:    contextText,
		ContextPayload: json.RawMessage(`{"summary":{"text":"` + contextText + `"}}`),
		OccurredOn:     time.Now().UTC(),
	}
}

func (ts *BackendTestSuite) deleteWAHandoverRows() {
	_, err := ts.b.db.Exec(`DELETE FROM wa_conversation_handover WHERE channel_id = (SELECT id FROM channels_channel WHERE uuid = $1)`, whatsappCloudChannelUUID)
	ts.NoError(err)
}

func (ts *BackendTestSuite) pendingHandoverRows() []struct {
	ContextType        string    `db:"context_type"`
	ContextText        string    `db:"context_text"`
	ContextPayload     string    `db:"context_payload"`
	PreviousOwnerAppID *string   `db:"previous_owner_app_id"`
	PreviousOwnerRole  *string   `db:"previous_owner_app_role"`
	OccurredOn         time.Time `db:"occurred_on"`
} {
	rows := []struct {
		ContextType        string    `db:"context_type"`
		ContextText        string    `db:"context_text"`
		ContextPayload     string    `db:"context_payload"`
		PreviousOwnerAppID *string   `db:"previous_owner_app_id"`
		PreviousOwnerRole  *string   `db:"previous_owner_app_role"`
		OccurredOn         time.Time `db:"occurred_on"`
	}{}
	err := ts.b.db.Select(&rows, `
		SELECT context_type, context_text, context_payload::text AS context_payload,
			previous_owner_app_id, previous_owner_app_role, occurred_on
		FROM wa_conversation_handover
		WHERE consumed_on IS NULL
		  AND channel_id = (SELECT id FROM channels_channel WHERE uuid = $1)`, whatsappCloudChannelUUID)
	ts.NoError(err)
	return rows
}

func (ts *BackendTestSuite) TestWriteWAConversationHandover() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "Summary text"))
	ts.NoError(err)

	var count int
	err = ts.b.db.Get(&count, `
		SELECT count(*) FROM wa_conversation_handover
		WHERE context_type = 'summary' AND context_text = 'Summary text'
		  AND channel_id = (SELECT id FROM channels_channel WHERE uuid = $1)`, whatsappCloudChannelUUID)
	ts.NoError(err)
	ts.Equal(1, count)
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverUpsertsPending() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "First summary"))
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextHistory, "[user] hi"))
	ts.NoError(err)

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal("history", rows[0].ContextType)
	ts.Equal("First summary\n[user] hi", rows[0].ContextText)
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverSummaryStaysSummary() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "First summary"))
	ts.NoError(err)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "Second summary"))
	ts.NoError(err)

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal("summary", rows[0].ContextType)
	ts.Equal("First summary\nSecond summary", rows[0].ContextText)
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverTruncatesCombined() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	first := strings.Repeat("a", 20000)
	second := strings.Repeat("b", 20000)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, first))
	ts.NoError(err)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, second))
	ts.NoError(err)

	combined := first + "\n" + second
	runes := []rune(combined)
	want := "[truncated]" + string(runes[len(runes)-32000:])

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal(want, rows[0].ContextText)
	ts.Equal(32000+len("[truncated]"), len([]rune(rows[0].ContextText)))
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverInsertsWhenConsumed() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "kept"))
	ts.NoError(err)

	_, err = ts.b.db.Exec(`
		UPDATE wa_conversation_handover SET consumed_on = NOW()
		WHERE context_text = 'kept'
		  AND channel_id = (SELECT id FROM channels_channel WHERE uuid = $1)`, whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "fresh"))
	ts.NoError(err)

	var consumed string
	err = ts.b.db.Get(&consumed, `
		SELECT context_text FROM wa_conversation_handover
		WHERE consumed_on IS NOT NULL
		  AND channel_id = (SELECT id FROM channels_channel WHERE uuid = $1)`, whatsappCloudChannelUUID)
	ts.NoError(err)
	ts.Equal("kept", consumed)

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal("fresh", rows[0].ContextText)
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverPreservesOwnerMetadata() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	first := newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "one")
	first.PreviousOwnerAppID = "app-1"
	first.PreviousOwnerAppRole = "partner"
	first.OccurredOn = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	first.ContextPayload = json.RawMessage(`{"summary":{"text":"one"}}`)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, first)
	ts.NoError(err)

	secondOn := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	second := newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "two")
	second.OccurredOn = secondOn
	second.ContextPayload = json.RawMessage(`{"summary":{"text":"two"}}`)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, second)
	ts.NoError(err)

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	if ts.NotNil(rows[0].PreviousOwnerAppID) {
		ts.Equal("app-1", *rows[0].PreviousOwnerAppID)
	}
	if ts.NotNil(rows[0].PreviousOwnerRole) {
		ts.Equal("partner", *rows[0].PreviousOwnerRole)
	}
	ts.Contains(rows[0].ContextPayload, `"two"`)
	ts.True(rows[0].OccurredOn.Equal(secondOn), "occurred_on %s", rows[0].OccurredOn)

	third := newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "three")
	third.PreviousOwnerAppID = "app-2"
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, third)
	ts.NoError(err)

	rows = ts.pendingHandoverRows()
	ts.Len(rows, 1)
	if ts.NotNil(rows[0].PreviousOwnerAppID) {
		ts.Equal("app-2", *rows[0].PreviousOwnerAppID)
	}
	if ts.NotNil(rows[0].PreviousOwnerRole) {
		ts.Equal("partner", *rows[0].PreviousOwnerRole)
	}
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverEmptyTextNewline() {
	ts.deleteWAHandoverRows()

	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, ""))
	ts.NoError(err)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "hi"))
	ts.NoError(err)

	rows := ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal("hi", rows[0].ContextText)

	ts.deleteWAHandoverRows()
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "hi"))
	ts.NoError(err)
	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, ""))
	ts.NoError(err)

	rows = ts.pendingHandoverRows()
	ts.Len(rows, 1)
	ts.Equal("hi", rows[0].ContextText)
}
