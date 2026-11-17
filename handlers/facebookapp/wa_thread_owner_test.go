package facebookapp

import (
	"fmt"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func threadOwnerHash(t *testing.T, mb *courier.MockBackend, ch courier.Channel, urnStr string) map[string]string {
	t.Helper()
	rc := mb.RedisPool().Get()
	defer rc.Close()
	got, err := redis.StringMap(rc.Do("HGETALL", wacThreadOwnerKey(ch.UUID(), urns.URN(urnStr))))
	require.NoError(t, err)
	if got == nil {
		return map[string]string{}
	}
	return got
}

func seedThreadOwner(t *testing.T, mb *courier.MockBackend, ch courier.Channel, urnStr, state, ts string) {
	t.Helper()
	rc := mb.RedisPool().Get()
	defer rc.Close()
	key := wacThreadOwnerKey(ch.UUID(), urns.URN(urnStr))
	_, err := rc.Do("HSET", key, "state", state)
	require.NoError(t, err)
	_, err = rc.Do("HSET", key, "ts", ts)
	require.NoError(t, err)
	_, err = rc.Do("EXPIRE", key, 86400)
	require.NoError(t, err)
}

func assertNoWACOwnershipPII(t *testing.T, entries []*logrus.Entry) {
	t.Helper()
	for _, entry := range entries {
		event, _ := entry.Data["event"].(string)
		switch event {
		case "wac_thread_owner_applied", "wac_thread_owner_update_failed", "wac_send_blocked", "wac_send_ownership_read_failed", "wac_pending_context_write_failed":
			_, hasURN := entry.Data["contact_urn"]
			_, hasPhone := entry.Data["sender_phone_number"]
			assert.False(t, hasURN, "event %s must not include contact_urn", event)
			assert.False(t, hasPhone, "event %s must not include sender_phone_number", event)
			assert.NotContains(t, fmt.Sprint(entry.Data), "whatsapp:")
			assert.NotContains(t, entry.Message, "whatsapp:")
		}
	}
}

func TestWACThreadOwner(t *testing.T) {
	channel := testChannelsWAC[0]
	urn, err := urns.NewWhatsAppURN("5678")
	require.NoError(t, err)

	tcs := []struct {
		label       string
		seedState   string
		seedTS      string
		ts          int64
		state       string
		mode        string
		wantOutcome string
		wantState   string
		wantTS      string
		wantAbsent  bool
	}{
		{
			label:       "set creates owner",
			ts:          100,
			state:       wacThreadOwnerStateOwner,
			mode:        wacThreadOwnerModeSet,
			wantOutcome: wacThreadOwnerCreated,
			wantState:   wacThreadOwnerStateOwner,
			wantTS:      "100",
		},
		{
			label:       "set creates not_owner",
			ts:          100,
			state:       wacThreadOwnerStateNone,
			mode:        wacThreadOwnerModeSet,
			wantOutcome: wacThreadOwnerCreated,
			wantState:   wacThreadOwnerStateNone,
			wantTS:      "100",
		},
		{
			label:       "newer timestamp updates",
			seedState:   wacThreadOwnerStateNone,
			seedTS:      "100",
			ts:          101,
			state:       wacThreadOwnerStateOwner,
			mode:        wacThreadOwnerModeSet,
			wantOutcome: wacThreadOwnerUpdated,
			wantState:   wacThreadOwnerStateOwner,
			wantTS:      "101",
		},
		{
			label:       "older timestamp is stale",
			seedState:   wacThreadOwnerStateOwner,
			seedTS:      "100",
			ts:          50,
			state:       wacThreadOwnerStateNone,
			mode:        wacThreadOwnerModeSet,
			wantOutcome: wacThreadOwnerStale,
			wantState:   wacThreadOwnerStateOwner,
			wantTS:      "100",
		},
		{
			label:       "equal timestamp is stale",
			seedState:   wacThreadOwnerStateOwner,
			seedTS:      "100",
			ts:          100,
			state:       wacThreadOwnerStateNone,
			mode:        wacThreadOwnerModeSet,
			wantOutcome: wacThreadOwnerStale,
			wantState:   wacThreadOwnerStateOwner,
			wantTS:      "100",
		},
		{
			label:       "update_if_exists missing is absent",
			ts:          100,
			state:       wacThreadOwnerStateOwner,
			mode:        wacThreadOwnerModeUpdate,
			wantOutcome: wacThreadOwnerAbsent,
			wantAbsent:  true,
		},
		{
			label:       "update_if_exists updates existing",
			seedState:   wacThreadOwnerStateNone,
			seedTS:      "1",
			ts:          1454119029,
			state:       wacThreadOwnerStateOwner,
			mode:        wacThreadOwnerModeUpdate,
			wantOutcome: wacThreadOwnerUpdated,
			wantState:   wacThreadOwnerStateOwner,
			wantTS:      "1454119029",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.label, func(t *testing.T) {
			mb := courier.NewMockBackend()
			rc := mb.RedisPool().Get()
			defer rc.Close()

			key := wacThreadOwnerKey(channel.UUID(), urn)
			_, err := rc.Do("DEL", key)
			require.NoError(t, err)

			if tc.seedTS != "" {
				_, err = rc.Do("HSET", key, "state", tc.seedState)
				require.NoError(t, err)
				_, err = rc.Do("HSET", key, "ts", tc.seedTS)
				require.NoError(t, err)
				_, err = rc.Do("EXPIRE", key, 100)
				require.NoError(t, err)
			}

			hook := handlers.CaptureLogs(t)
			outcome, err := applyWACThreadOwner(rc, channel.UUID(), urn, tc.ts, tc.state, tc.mode, "control_passed")
			require.NoError(t, err)
			assert.Equal(t, tc.wantOutcome, outcome)

			got, err := redis.StringMap(rc.Do("HGETALL", key))
			require.NoError(t, err)
			if tc.wantAbsent {
				assert.Empty(t, got)
				state, err := getWACThreadOwner(rc, channel.UUID(), urn)
				require.NoError(t, err)
				assert.Empty(t, state)
				return
			}

			assert.Equal(t, tc.wantState, got["state"])
			assert.Equal(t, tc.wantTS, got["ts"])
			ttl, err := redis.Int64(rc.Do("TTL", key))
			require.NoError(t, err)
			if tc.wantOutcome == wacThreadOwnerStale {
				assert.Greater(t, ttl, int64(0))
				assert.LessOrEqual(t, ttl, int64(100))
			} else {
				assert.Greater(t, ttl, int64(0))
				assert.LessOrEqual(t, ttl, int64(86400))
			}

			state, err := getWACThreadOwner(rc, channel.UUID(), urn)
			require.NoError(t, err)
			assert.Equal(t, tc.wantState, state)

			foundApplied := false
			for _, entry := range hook.Entries() {
				if entry.Data["event"] == "wac_thread_owner_applied" {
					foundApplied = true
					assert.Equal(t, channel.UUID(), entry.Data["channel_uuid"])
					assert.Equal(t, tc.wantOutcome, entry.Data["outcome"])
					assert.Equal(t, tc.state, entry.Data["state"])
					assert.Equal(t, "control_passed", entry.Data["source"])
				}
			}
			assert.True(t, foundApplied)
			assertNoWACOwnershipPII(t, hook.Entries())
		})
	}

	t.Run("get missing", func(t *testing.T) {
		mb := courier.NewMockBackend()
		rc := mb.RedisPool().Get()
		defer rc.Close()
		missing, err := urns.NewWhatsAppURN("9999")
		require.NoError(t, err)
		state, err := getWACThreadOwner(rc, channel.UUID(), missing)
		require.NoError(t, err)
		assert.Empty(t, state)
	})
}
