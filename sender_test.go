package courier

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtractTemplateLastDispatchDataSkipsInvalidMetadata(t *testing.T) {
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})

	tests := []struct {
		name     string
		metadata json.RawMessage
	}{
		{name: "missing metadata", metadata: nil},
		{name: "missing templating", metadata: json.RawMessage(`{"foo":"bar"}`)},
		{name: "missing meta template id", metadata: json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test"},"language":"por"}}`)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := NewMockOutgoingMsg(channel, "hello", tc.metadata)
			_, ok := ExtractTemplateLastDispatchData(msg)
			require.False(t, ok)
		})
	}
}

func TestExtractTemplateLastDispatchDataValid(t *testing.T) {
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	data, ok := ExtractTemplateLastDispatchData(msg)
	require.True(t, ok)
	require.Equal(t, "44019537-9afe-4898-9626-a5c724d169ef", data.TemplateUUID)
	require.Equal(t, "template_test", data.Name)
	require.Equal(t, "123456789", data.MetaTemplateID)
}

func TestQueueTemplateLastDispatchSkipsInvalidMetadata(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	msg := NewMockOutgoingMsg(channel, "hello", nil)

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 0)
}

func TestQueueTemplateLastDispatchEnqueuesValidTemplate(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 1)
	require.Equal(t, "123456789", backend.templateLastDispatches[0].data.MetaTemplateID)
}

func TestQueueTemplateLastDispatchUsesSentOnWhenPresent(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	sentOn := time.Now().Add(-time.Minute).UTC().Round(time.Microsecond)
	msg.(*mockMsg).sentOn = &sentOn

	queueTemplateLastDispatch(backend, msg)
	require.Len(t, backend.templateLastDispatches, 1)
	require.Equal(t, sentOn, backend.templateLastDispatches[0].firedOn.UTC())
}

func TestQueueTemplateLastDispatchDoesNotFailSendPath(t *testing.T) {
	backend := NewMockBackend()
	channel := NewMockChannel("dbc126ed-66bc-4e28-b67b-81dc3327c95d", "WAC", "+12345", "US", map[string]interface{}{})
	metadata := json.RawMessage(`{"templating":{"template":{"uuid":"44019537-9afe-4898-9626-a5c724d169ef","name":"template_test","id":"123456789"},"language":"por"}}`)
	msg := NewMockOutgoingMsg(channel, "hello", metadata)

	status := backend.NewMsgStatusForID(channel, msg.ID(), MsgWired)
	require.NotNil(t, status)

	queueTemplateLastDispatch(backend, msg)
	require.Equal(t, MsgWired, status.Status())
}
