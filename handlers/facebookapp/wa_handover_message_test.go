package facebookapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	handoverMsgSummaryFixture            = "./testdata/wac/handoverMessageSummaryWAC.json"
	handoverMsgNoContextFixture          = "./testdata/wac/handoverMessageNoContextWAC.json"
	handoverMsgSummaryNoTimestampFixture = "./testdata/wac/handoverMessageSummaryNoTimestampWAC.json"
	handoverMsgNoContextNoTSFixture      = "./testdata/wac/handoverMessageNoContextNoTimestampWAC.json"

	handoverMsgURN          = "whatsapp:5588991234567"
	handoverMsgTimestamp    = "1795694400"
	handoverMsgSummaryText  = "O cliente informou que o pedido chegou com um item faltando"
	handoverMsgMetaFreeText = "Cliente solicitou atendimento humano"
)

// the real payloads carry the production phone_number_id, which is how the WAC receive resolves the channel
var handoverMsgChannel = courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c568c", "WAC", "987654321098765", "", map[string]interface{}{courier.ConfigAuthToken: "a123", "webhook": `{"url": "https://webhook.site", "method": "POST", "headers": {}}`})

var handoverMsgNewEvents = []string{
	wacHandoverEventDuplicate,
	wacHandoverEventDedupFailed,
	wacHandoverEventDedupSkipped,
	wacHandoverEventPendingCheckFailed,
	wacHandoverEventMsgWriteFailed,
	wacHandoverEventMsgWritten,
}

func newWACHandoverMessageHarness(t *testing.T) (*courier.MockBackend, courier.Server, courier.Channel, urns.URN) {
	t.Helper()
	mb, s := newWACHandoverHarnessFor(t, handoverMsgChannel)
	return mb, s, handoverMsgChannel, urns.URN(handoverMsgURN)
}

func newWACHandoverHarnessFor(t *testing.T, channel courier.Channel) (*courier.MockBackend, courier.Server) {
	t.Helper()
	mb := courier.NewMockBackend()
	s := handlers.NewTestServer(mb)
	mb.AddChannel(channel)
	newHandler("WAC", "Cloud API WhatsApp", false).Initialize(s)
	return mb, s
}

func postHandover(t *testing.T, s courier.Server, fixture string, replaceTS string, status int, response string) {
	t.Helper()
	body := string(courier.ReadFile(fixture))
	if replaceTS != "" {
		body = strings.Replace(body, handoverMsgTimestamp, replaceTS, 1)
	}
	handlers.TestHandlerRequest(t, s, wacReceiveURL, nil, body, nil, status, handlers.Sp(response), addValidSignatureWAC)
}

func handoverSeenKeys(t *testing.T, mb *courier.MockBackend) []string {
	t.Helper()
	rc := mb.RedisPool().Get()
	defer rc.Close()
	keys, err := redis.Strings(rc.Do("KEYS", wacHandoverSeenKeyPrefix+":*"))
	require.NoError(t, err)
	return keys
}

func lastQueuedMsg(t *testing.T, mb *courier.MockBackend) courier.Msg {
	t.Helper()
	msg, err := mb.GetLastQueueMsg()
	require.NoError(t, err)
	return msg
}

func handoverMsgMetadata(t *testing.T, msg courier.Msg) map[string]interface{} {
	t.Helper()
	got := map[string]interface{}{}
	require.NoError(t, json.Unmarshal(msg.Metadata(), &got))
	return got
}

func handoverEventEntries(entries []*logrus.Entry, event string) []*logrus.Entry {
	found := []*logrus.Entry{}
	for _, entry := range entries {
		if entry.Data["event"] == event {
			found = append(found, entry)
		}
	}
	return found
}

// assertNoPIIInNewHandoverLogs checks only the log lines this feature adds. The older "wa conversation handover
// received" INFO line carries URN and name by design and is out of scope.
func assertNoPIIInNewHandoverLogs(t *testing.T, entries []*logrus.Entry) {
	t.Helper()
	for _, entry := range entries {
		event, _ := entry.Data["event"].(string)
		isNew := false
		for _, name := range handoverMsgNewEvents {
			if event == name {
				isNew = true
			}
		}
		if !isNew {
			continue
		}
		rendered := fmt.Sprint(entry.Data) + " " + entry.Message
		for _, forbidden := range []string{"whatsapp:", "5588991234567", handoverMsgSummaryText, handoverMsgMetaFreeText, "context_text"} {
			assert.NotContains(t, rendered, forbidden, "event %s", event)
		}
		_, hasURN := entry.Data["contact_urn"]
		_, hasName := entry.Data["contact_name"]
		_, hasMeta := entry.Data["handover_metadata"]
		assert.False(t, hasURN || hasName || hasMeta, "event %s", event)
	}
}

func TestWACHandoverMessageMetadata(t *testing.T) {
	tcs := []struct {
		label string
		owner resolvedPreviousOwner
		want  map[string]interface{}
	}{
		{
			label: "all fields",
			owner: resolvedPreviousOwner{AppID: "app", AppRole: "partner", BusinessID: "biz", NewOwnerRole: "escalation", Metadata: "free text"},
			want: map[string]interface{}{
				"wa_handover": true, "previous_owner_app_role": "partner", "previous_owner_app_id": "app",
				"previous_owner_business_id": "biz", "new_owner_role": "escalation",
			},
		},
		{
			label: "roles only omits empty fields",
			owner: resolvedPreviousOwner{AppRole: "marketing", NewOwnerRole: "escalation"},
			want:  map[string]interface{}{"wa_handover": true, "previous_owner_app_role": "marketing", "new_owner_role": "escalation"},
		},
		{
			label: "no owner",
			owner: resolvedPreviousOwner{},
			want:  map[string]interface{}{"wa_handover": true},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			got := map[string]interface{}{}
			require.NoError(t, json.Unmarshal(wacHandoverMessageMetadata(tc.owner), &got))
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "context_text")
			assert.NotContains(t, got, "metadata")
		})
	}
}

func TestWACHandoverSeenKey(t *testing.T) {
	channel := testChannelsWAC[0]
	urn := urns.URN(handoverMsgURN)
	assert.Equal(t, "wac:handover:seen:8eb23e93-5ecb-45ba-b726-3b064e0c568c:whatsapp:5588991234567:1795694400", wacHandoverSeenKey(channel.UUID(), urn, 1795694400))
}

func TestWACHandoverMessageFlow(t *testing.T) {
	t.Run("payload A upserts then writes the empty message", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)

		events := mb.WAHandoverEvents()
		require.Len(t, events, 1)
		assert.Contains(t, events[0].ContextText, handoverMsgSummaryText)
		require.Equal(t, 1, mb.LenQueuedMsgs())
		assert.Equal(t, []string{"wa_handover", "msg"}, mb.BackendCalls())

		msg := lastQueuedMsg(t, mb)
		assert.Equal(t, "", msg.Text())
		assert.Equal(t, urn, msg.URN())
		assert.Equal(t, "", msg.ExternalID())
		require.NotNil(t, msg.ReceivedOn())
		assert.Equal(t, time.Unix(1795694400, 0).UTC(), msg.ReceivedOn().UTC())
		assert.Equal(t, map[string]interface{}{
			"wa_handover": true, "previous_owner_app_role": "ai_agent", "new_owner_role": "escalation",
		}, handoverMsgMetadata(t, msg))

		got := threadOwnerHash(t, mb, channel, handoverMsgURN)
		assert.Equal(t, wacThreadOwnerStateOwner, got["state"])
		assert.Equal(t, handoverMsgTimestamp, got["ts"])
		assert.Len(t, handoverSeenKeys(t, mb), 1)

		assert.Len(t, handoverEventEntries(hook.Entries(), wacHandoverEventMsgWritten), 1)
		assertNoPIIInNewHandoverLogs(t, hook.Entries())
	})

	t.Run("metadata never carries the context text or Meta free text", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)

		raw := string(lastQueuedMsg(t, mb).Metadata())
		assert.NotContains(t, raw, "context_text")
		assert.NotContains(t, raw, handoverMsgMetaFreeText)
		assert.NotContains(t, raw, handoverMsgSummaryText)
	})

	t.Run("payload B with pending context writes the message without a new upsert", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		mb.SeedPendingWAConversationHandover(channel.UUID(), urn)

		postHandover(t, s, handoverMsgNoContextFixture, "", 200, wacHandoverInfoMessageWritten)

		assert.Empty(t, mb.WAHandoverEvents())
		require.Equal(t, 1, mb.LenQueuedMsgs())
		msg := lastQueuedMsg(t, mb)
		assert.Equal(t, "", msg.Text())
		assert.Equal(t, "", msg.ExternalID())
		assert.Equal(t, map[string]interface{}{
			"wa_handover": true, "previous_owner_app_role": "marketing", "new_owner_role": "escalation",
		}, handoverMsgMetadata(t, msg))
	})

	t.Run("payload B with context accumulated by an earlier webhook", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		require.NoError(t, mb.WriteWAConversationHandover(context.Background(), courier.WAConversationHandoverEvent{
			ChannelUUID: channel.UUID(), ContactURN: urn, ContextType: courier.WAHandoverContextSummary,
			ContextText: "accumulated by standby", OccurredOn: time.Now().UTC(),
		}))

		postHandover(t, s, handoverMsgNoContextFixture, "", 200, wacHandoverInfoMessageWritten)

		assert.Len(t, mb.WAHandoverEvents(), 1, "only the seed event, no new upsert")
		assert.Equal(t, 1, mb.LenQueuedMsgs())
	})

	t.Run("payload B without pending context writes nothing", func(t *testing.T) {
		mb, s, channel, _ := newWACHandoverMessageHarness(t)

		postHandover(t, s, handoverMsgNoContextFixture, "", 200, wacHandoverInfoNoContext)

		assert.Equal(t, 0, mb.LenQueuedMsgs())
		assert.Empty(t, mb.WAHandoverEvents())
		assert.Equal(t, 1, mb.PendingCheckCalls())
		got := threadOwnerHash(t, mb, channel, handoverMsgURN)
		assert.Equal(t, wacThreadOwnerStateOwner, got["state"])
		assert.Equal(t, handoverMsgTimestamp, got["ts"])
	})

	t.Run("payload B after Mailroom consumed the row writes nothing", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		mb.SeedPendingWAConversationHandover(channel.UUID(), urn)
		mb.ConsumePendingWAConversationHandover(channel.UUID(), urn)

		postHandover(t, s, handoverMsgNoContextFixture, "", 200, wacHandoverInfoNoContext)
		assert.Equal(t, 0, mb.LenQueuedMsgs())
	})

	t.Run("redelivery of the same webhook repeats neither upsert nor message", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)
		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoDuplicate)

		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 1, mb.LenQueuedMsgs())
		assert.Len(t, handoverEventEntries(hook.Entries(), wacHandoverEventDuplicate), 1)
		assertNoPIIInNewHandoverLogs(t, hook.Entries())
	})

	t.Run("two handovers with different timestamps write two messages", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)
		postHandover(t, s, handoverMsgSummaryFixture, "1795694500", 200, wacHandoverInfoPersisted)

		assert.Len(t, mb.WAHandoverEvents(), 2)
		assert.Equal(t, 2, mb.LenQueuedMsgs())
		assert.Len(t, handoverSeenKeys(t, mb), 2)
	})

	t.Run("upsert error deletes the key and fails the webhook, redelivery then succeeds", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		mb.SetErrorOnWAHandover(true)

		postHandover(t, s, handoverMsgSummaryFixture, "", 400, "Error")
		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 0, mb.LenQueuedMsgs())
		assert.Equal(t, 0, mb.PendingCheckCalls())

		mb.SetErrorOnWAHandover(false)
		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)
		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 1, mb.LenQueuedMsgs())
	})

	t.Run("WriteMsg error is fail-open and keeps the pending row", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		mb.SetErrorOnQueue(true)
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)

		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 0, mb.LenQueuedMsgs())
		failed := handoverEventEntries(hook.Entries(), wacHandoverEventMsgWriteFailed)
		require.Len(t, failed, 1)
		assert.Equal(t, logrus.ErrorLevel, failed[0].Level)
		assert.Equal(t, channel.UUID(), failed[0].Data["channel_uuid"])
		assert.Empty(t, handoverEventEntries(hook.Entries(), wacHandoverEventMsgWritten))
		assertNoPIIInNewHandoverLogs(t, hook.Entries())

		pending, err := mb.HasPendingWAConversationHandover(context.Background(), channel.UUID(), urn)
		require.NoError(t, err)
		assert.True(t, pending)

		// the key stays: Meta's redelivery must not append the same context again
		assert.Len(t, handoverSeenKeys(t, mb), 1)
		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoDuplicate)
		assert.Len(t, mb.WAHandoverEvents(), 1)
	})

	t.Run("pending check error skips the message", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		mb.SetErrorOnWAPendingCheck(true)
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)

		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 0, mb.LenQueuedMsgs())
		failed := handoverEventEntries(hook.Entries(), wacHandoverEventPendingCheckFailed)
		require.Len(t, failed, 1)
		assert.Equal(t, logrus.ErrorLevel, failed[0].Level)
		assertNoPIIInNewHandoverLogs(t, hook.Entries())
	})

	t.Run("Redis error on the dedup claim is fail-open", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		original := wacHandoverClaimSeen
		wacHandoverClaimSeen = func(*redis.Pool, courier.ChannelUUID, urns.URN, int64) (bool, error) {
			return false, errors.New("redis down at claim")
		}
		t.Cleanup(func() { wacHandoverClaimSeen = original })
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryFixture, "", 200, wacHandoverInfoPersisted)

		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 1, mb.LenQueuedMsgs())
		assert.Len(t, handoverEventEntries(hook.Entries(), wacHandoverEventDedupFailed), 1)
		assertNoPIIInNewHandoverLogs(t, hook.Entries())
	})

	t.Run("upsert error without a claimed key leaves no key behind", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		original := wacHandoverClaimSeen
		wacHandoverClaimSeen = func(*redis.Pool, courier.ChannelUUID, urns.URN, int64) (bool, error) {
			return false, errors.New("redis down at claim")
		}
		t.Cleanup(func() { wacHandoverClaimSeen = original })
		mb.SetErrorOnWAHandover(true)

		postHandover(t, s, handoverMsgSummaryFixture, "", 400, "Error")
		assert.Empty(t, handoverSeenKeys(t, mb))
	})

	t.Run("no timestamp anywhere writes no key and never blocks a later handover", func(t *testing.T) {
		mb, s, _, _ := newWACHandoverMessageHarness(t)
		hook := handlers.CaptureLogs(t)

		postHandover(t, s, handoverMsgSummaryNoTimestampFixture, "", 200, wacHandoverInfoPersisted)
		postHandover(t, s, handoverMsgSummaryNoTimestampFixture, "", 200, wacHandoverInfoPersisted)

		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Len(t, handoverEventEntries(hook.Entries(), wacHandoverEventDedupSkipped), 2)
		assert.Len(t, mb.WAHandoverEvents(), 2)
		require.Equal(t, 2, mb.LenQueuedMsgs())
		assert.Nil(t, lastQueuedMsg(t, mb).ReceivedOn(), "epoch 0 must not become received_on")
		assertNoPIIInNewHandoverLogs(t, hook.Entries())
	})

	t.Run("no timestamp, payload B with pending context", func(t *testing.T) {
		mb, s, channel, urn := newWACHandoverMessageHarness(t)
		mb.SeedPendingWAConversationHandover(channel.UUID(), urn)

		postHandover(t, s, handoverMsgNoContextNoTSFixture, "", 200, wacHandoverInfoMessageWritten)
		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 1, mb.LenQueuedMsgs())
	})
}

func TestWACHandoverMessageNotTriggered(t *testing.T) {
	t.Run("control_taken", func(t *testing.T) {
		// these fixtures use the older test channel (phone_number_id 12345) and sender 5678
		mb, s := newWACHandoverHarnessFor(t, testChannelsWAC[0])
		mb.SeedPendingWAConversationHandover(testChannelsWAC[0].UUID(), urns.URN("whatsapp:5678"))

		postHandover(t, s, "./testdata/wac/handoverTakenWAC.json", "", 200, "control_taken recorded")

		assert.Equal(t, 0, mb.LenQueuedMsgs())
		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 0, mb.PendingCheckCalls())
	})

	t.Run("control_passed without sender", func(t *testing.T) {
		mb, s := newWACHandoverHarnessFor(t, testChannelsWAC[0])

		postHandover(t, s, "./testdata/wac/handoverNoSenderWAC.json", "", 400, "no sender identifier in handover")

		assert.Equal(t, 0, mb.LenQueuedMsgs())
		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 0, mb.PendingCheckCalls())
	})

	t.Run("WCD keeps today's behavior", func(t *testing.T) {
		mb := courier.NewMockBackend()
		s := handlers.NewTestServer(mb)
		wcd := courier.NewMockChannel("8eb23e93-5ecb-45ba-b726-3b064e0c56ab", "WCD", "987654321098765", "", map[string]interface{}{courier.ConfigAuthToken: "a123"})
		mb.AddChannel(wcd)
		newWACDemoHandler("WCD", "WhatsApp Cloud Demo").Initialize(s)
		mb.SeedPendingWAConversationHandover(wcd.UUID(), urns.URN(handoverMsgURN))

		body := string(courier.ReadFile(handoverMsgSummaryFixture))
		handlers.TestHandlerRequest(t, s, "/c/wcd/8eb23e93-5ecb-45ba-b726-3b064e0c56ab/receive", nil, body, nil, 200, nil, nil)

		assert.Len(t, mb.WAHandoverEvents(), 1)
		assert.Equal(t, 0, mb.LenQueuedMsgs())
		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 0, mb.PendingCheckCalls())
	})

	t.Run("other webhooks do not touch the handover dedup or pending check", func(t *testing.T) {
		mb, s := newWACHandoverHarnessFor(t, testChannelsWAC[0])

		handlers.TestHandlerRequest(t, s, wacReceiveURL, nil, string(courier.ReadFile("./testdata/wac/helloWAC.json")), nil, 200, handlers.Sp("Handled"), addValidSignatureWAC)

		assert.Empty(t, handoverSeenKeys(t, mb))
		assert.Equal(t, 0, mb.PendingCheckCalls())
	})
}
