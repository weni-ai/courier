package facebookapp

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
)

const (
	wacThreadOwnerTTL        = 24 * time.Hour
	wacThreadOwnerKeyPrefix  = "wac:thread_owner"
	wacThreadOwnerStateOwner = "owner"
	wacThreadOwnerStateNone  = "not_owner"
	wacThreadOwnerModeSet    = "set"
	wacThreadOwnerModeUpdate = "update_if_exists"
	wacThreadOwnerCreated    = "created"
	wacThreadOwnerUpdated    = "updated"
	wacThreadOwnerStale      = "stale"
	wacThreadOwnerAbsent     = "absent"
)

// Incoming ts <= stored is stale so Meta redelivery of the same webhook does not refresh TTL or flip state.
var luaWACThreadOwnerApply = redis.NewScript(1, `-- KEYS[1]=key ARGV[1]=ts ARGV[2]=state ARGV[3]=ttl ARGV[4]=mode
local incoming = tonumber(ARGV[1])
if incoming == nil then
	return redis.error_reply("invalid timestamp")
end
local stored = redis.call("HGET", KEYS[1], "ts")
if stored then
	if incoming <= tonumber(stored) then
		return "stale"
	end
	redis.call("HSET", KEYS[1], "state", ARGV[2])
	redis.call("HSET", KEYS[1], "ts", ARGV[1])
	redis.call("EXPIRE", KEYS[1], tonumber(ARGV[3]))
	return "updated"
end
if ARGV[4] == "update_if_exists" then
	return "absent"
end
redis.call("HSET", KEYS[1], "state", ARGV[2])
redis.call("HSET", KEYS[1], "ts", ARGV[1])
redis.call("EXPIRE", KEYS[1], tonumber(ARGV[3]))
return "created"
`)

func wacThreadOwnerKey(channelUUID courier.ChannelUUID, urn urns.URN) string {
	return fmt.Sprintf("%s:%s:%s", wacThreadOwnerKeyPrefix, channelUUID, urn.Identity())
}

func applyWACThreadOwner(rc redis.Conn, channelUUID courier.ChannelUUID, urn urns.URN, ts int64, state, mode, source string) (string, error) {
	ttl := int(wacThreadOwnerTTL.Seconds())
	outcome, err := redis.String(luaWACThreadOwnerApply.Do(
		rc,
		wacThreadOwnerKey(channelUUID, urn),
		strconv.FormatInt(ts, 10),
		state,
		strconv.Itoa(ttl),
		mode,
	))
	if err != nil {
		return "", err
	}

	logrus.WithFields(logrus.Fields{
		"event":        "wac_thread_owner_applied",
		"channel_uuid": channelUUID,
		"outcome":      outcome,
		"state":        state,
		"source":       source,
	}).Info("wac thread owner applied")

	return outcome, nil
}

func getWACThreadOwner(rc redis.Conn, channelUUID courier.ChannelUUID, urn urns.URN) (string, error) {
	state, err := redis.String(rc.Do("HGET", wacThreadOwnerKey(channelUUID, urn), "state"))
	if err == redis.ErrNil {
		return "", nil
	}
	return state, err
}

func (h *handler) blockWACSendIfNotOwner(msg courier.Msg) (bool, courier.MsgStatus) {
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)
	rc := h.Backend().RedisPool().Get()
	state, err := getWACThreadOwner(rc, msg.Channel().UUID(), msg.URN())
	rc.Close()
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"event":        "wac_send_ownership_read_failed",
			"channel_uuid": msg.Channel().UUID(),
			"msg_uuid":     msg.UUID().String(),
		}).Error("wac send ownership read failed")
		return false, nil
	}
	if state != wacThreadOwnerStateNone {
		return false, nil
	}

	status.SetStatus(courier.MsgFailed)
	logrus.WithFields(logrus.Fields{
		"event":        "wac_send_blocked",
		"channel_uuid": msg.Channel().UUID(),
		"msg_uuid":     msg.UUID().String(),
		"reason":       "not_owner",
	}).Info("wac send blocked")
	return true, status
}
