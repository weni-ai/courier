package courier

import (
	"encoding/json"
	"testing"

	"github.com/nyaruka/courier/billing"
	"github.com/nyaruka/gocommon/urns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingMsgEventsClient records every SendAsync call (routing key + payload).
type recordingMsgEventsClient struct {
	calls []struct {
		routingKey string
		msg        billing.Message
	}
}

func (c *recordingMsgEventsClient) Send(msg billing.Message, routingKey string) error {
	c.calls = append(c.calls, struct {
		routingKey string
		msg        billing.Message
	}{routingKey: routingKey, msg: msg})
	return nil
}

func (c *recordingMsgEventsClient) SendAsync(msg billing.Message, routingKey string, pre func(), post func()) {
	if pre != nil {
		pre()
	}
	_ = c.Send(msg, routingKey)
	if post != nil {
		post()
	}
}

func (c *recordingMsgEventsClient) routingKeys() []string {
	keys := make([]string, len(c.calls))
	for i, call := range c.calls {
		keys[i] = call.routingKey
	}
	return keys
}

func newTestOutgoingMsg(channelType string, metadata map[string]interface{}) (Msg, MsgStatus) {
	channel := NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", channelType, "12345", "BR", map[string]interface{}{})
	msg := &mockMsg{
		channel:     channel,
		id:          NewMsgID(10),
		text:        "hello from agent",
		urn:         urns.URN("whatsapp:5511999999999"),
		contactName: "Contact",
	}
	if metadata != nil {
		raw, _ := json.Marshal(metadata)
		msg.metadata = raw
	}

	status := &mockMsgStatus{
		channel:    channel,
		id:         msg.id,
		status:     MsgWired,
		externalID: "wamid.HBgNNTUxMTk5OTk5OTk5ORUCABIYFjNBMDJCOTk5OTk5OTk5OTk5OTk5AA==",
	}
	return msg, status
}

func TestPublishOutgoingMsgEvents_AB2StillPublishesWAC(t *testing.T) {
	client := &recordingMsgEventsClient{}
	msg, status := newTestOutgoingMsg("WAC", map[string]interface{}{
		"chats_msg_uuid": "68f2c4c7-fa2a-40a6-b518-3b609a0bd413",
	})

	publishOutgoingMsgEvents(client, msg, status, true, true, false, nil)

	require.Len(t, client.calls, 1, "AB2 must still publish WAMID return event")
	assert.Equal(t, billing.RoutingKeyWAC, client.calls[0].routingKey)
	assert.Equal(t, "68f2c4c7-fa2a-40a6-b518-3b609a0bd413", client.calls[0].msg.ChatsUUID)
	assert.Equal(t, status.ExternalID(), client.calls[0].msg.MessageID)
	assert.NotContains(t, client.routingKeys(), billing.RoutingKeyCreate, "billing create must stay suppressed for AB2")
}

func TestPublishOutgoingMsgEvents_AB1PublishesWACAndCreate(t *testing.T) {
	client := &recordingMsgEventsClient{}
	msg, status := newTestOutgoingMsg("WAC", map[string]interface{}{
		"chats_msg_uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})

	publishOutgoingMsgEvents(client, msg, status, true, false, false, nil)

	assert.Equal(t, []string{billing.RoutingKeyWAC, billing.RoutingKeyCreate}, client.routingKeys())
	assert.Equal(t, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", client.calls[0].msg.ChatsUUID)
	assert.Equal(t, status.ExternalID(), client.calls[0].msg.MessageID)
}

func TestPublishOutgoingMsgEvents_NonWACSkipsWACKey(t *testing.T) {
	client := &recordingMsgEventsClient{}
	msg, status := newTestOutgoingMsg("WA", map[string]interface{}{
		"chats_msg_uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})

	publishOutgoingMsgEvents(client, msg, status, true, false, false, nil)

	assert.Equal(t, []string{billing.RoutingKeyCreate}, client.routingKeys())
}

func TestPublishOutgoingMsgEvents_FailedSendPublishesNothing(t *testing.T) {
	client := &recordingMsgEventsClient{}
	msg, status := newTestOutgoingMsg("WAC", map[string]interface{}{
		"chats_msg_uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})
	status.SetStatus(MsgFailed)

	publishOutgoingMsgEvents(client, msg, status, false, false, false, nil)

	assert.Empty(t, client.calls)
}

func TestPublishOutgoingMsgEvents_NilClientIsNoop(t *testing.T) {
	msg, status := newTestOutgoingMsg("WAC", nil)
	assert.NotPanics(t, func() {
		publishOutgoingMsgEvents(nil, msg, status, true, false, false, nil)
	})
}
