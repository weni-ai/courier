package rapidpro

import (
	"context"
	"encoding/json"
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

func (ts *BackendTestSuite) TestWriteWAConversationHandover() {
	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "Summary text"))
	ts.NoError(err)

	var count int
	err = ts.b.db.Get(&count, `SELECT count(*) FROM wa_conversation_handover WHERE context_type = 'summary' AND context_text = 'Summary text'`)
	ts.NoError(err)
	ts.Equal(1, count)
}

func (ts *BackendTestSuite) TestWriteWAConversationHandoverUpsertsPending() {
	uuid, err := courier.NewChannelUUID(whatsappCloudChannelUUID)
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextSummary, "First summary"))
	ts.NoError(err)

	err = writeWAConversationHandoverToDB(context.Background(), ts.b, newTestWAHandoverEvent(uuid, courier.WAHandoverContextHistory, "[user] hi"))
	ts.NoError(err)

	rows := []struct {
		ContextType string `db:"context_type"`
		ContextText string `db:"context_text"`
	}{}
	err = ts.b.db.Select(&rows, `SELECT context_type, context_text FROM wa_conversation_handover WHERE consumed_on IS NULL`)
	ts.NoError(err)
	ts.Len(rows, 1)
	ts.Equal("history", rows[0].ContextType)
	ts.Equal("[user] hi", rows[0].ContextText)
}
