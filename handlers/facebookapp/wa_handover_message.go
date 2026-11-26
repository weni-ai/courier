package facebookapp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/sirupsen/logrus"
)

const (
	// wacHandoverSeenKeyPrefix keys the redelivery guard of control_passed. Meta sends no event id, so the key is
	// channel + URN + timestamp.
	wacHandoverSeenKeyPrefix = "wac:handover:seen"
	wacHandoverSeenTTL       = 24 * time.Hour

	wacHandoverMetadataMarker                = "wa_handover"
	wacHandoverMetadataPreviousOwnerAppRole  = "previous_owner_app_role"
	wacHandoverMetadataPreviousOwnerAppID    = "previous_owner_app_id"
	wacHandoverMetadataPreviousOwnerBusiness = "previous_owner_business_id"
	wacHandoverMetadataNewOwnerRole          = "new_owner_role"

	wacHandoverInfoNoContext      = "control_passed without conversation context"
	wacHandoverInfoPersisted      = "wa conversation handover persisted"
	wacHandoverInfoMessageWritten = "wa conversation handover message written"
	wacHandoverInfoDuplicate      = "wa conversation handover already processed"

	wacHandoverEventDuplicate          = "wac_handover_duplicate"
	wacHandoverEventDedupFailed        = "wac_handover_dedup_failed"
	wacHandoverEventDedupSkipped       = "wac_handover_dedup_skipped"
	wacHandoverEventPendingCheckFailed = "wac_handover_pending_check_failed"
	wacHandoverEventMsgWriteFailed     = "wac_handover_msg_write_failed"
	wacHandoverEventMsgWritten         = "wac_handover_msg_written"
)

// wacHandoverClaimSeen is a var only so tests can inject a Redis error at the claim without breaking the
// ownership update that runs before it.
var wacHandoverClaimSeen = claimWACHandoverSeen

func wacHandoverSeenKey(channelUUID courier.ChannelUUID, urn urns.URN, ts int64) string {
	return fmt.Sprintf("%s:%s:%s:%d", wacHandoverSeenKeyPrefix, channelUUID, urn.Identity(), ts)
}

// claimWACHandoverSeen reports whether this is the first delivery of the handover. The connection goes back to
// the pool before the caller touches Postgres.
func claimWACHandoverSeen(pool *redis.Pool, channelUUID courier.ChannelUUID, urn urns.URN, ts int64) (bool, error) {
	rc := pool.Get()
	defer rc.Close()

	_, err := redis.String(rc.Do("SET", wacHandoverSeenKey(channelUUID, urn, ts), "1", "NX", "EX", int(wacHandoverSeenTTL.Seconds())))
	if err == redis.ErrNil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func releaseWACHandoverSeen(pool *redis.Pool, channelUUID courier.ChannelUUID, urn urns.URN, ts int64) {
	rc := pool.Get()
	defer rc.Close()
	_, _ = rc.Do("DEL", wacHandoverSeenKey(channelUUID, urn, ts))
}

// wacHandoverMessageMetadata carries only the marker and role/id strings. The conversation text lives in the
// pending row and Meta's free-text metadata is not copied.
func wacHandoverMessageMetadata(owner resolvedPreviousOwner) json.RawMessage {
	metadata := map[string]interface{}{wacHandoverMetadataMarker: true}
	if owner.AppRole != "" {
		metadata[wacHandoverMetadataPreviousOwnerAppRole] = owner.AppRole
	}
	if owner.AppID != "" {
		metadata[wacHandoverMetadataPreviousOwnerAppID] = owner.AppID
	}
	if owner.BusinessID != "" {
		metadata[wacHandoverMetadataPreviousOwnerBusiness] = owner.BusinessID
	}
	if owner.NewOwnerRole != "" {
		metadata[wacHandoverMetadataNewOwnerRole] = owner.NewOwnerRole
	}

	// the map only holds strings and a bool, so marshalling cannot fail
	raw, _ := json.Marshal(metadata)
	return raw
}

func wacHandoverLog(channel courier.Channel, event string) *logrus.Entry {
	return logrus.WithFields(logrus.Fields{"event": event, "channel_uuid": channel.UUID()})
}

// processWACControlPassed handles control_passed for WAC after the ownership update: dedup claim, upsert of the
// webhook context, pending-context check and the synthetic inbound message that makes Mailroom consume the row.
func (h *handler) processWACControlPassed(
	ctx context.Context,
	channel courier.Channel,
	urn urns.URN,
	value wacHandoverValue,
	occurredOn time.Time,
) (string, error) {
	pool := h.Backend().RedisPool()
	ts := occurredOn.Unix()

	// ts 0 means neither value.timestamp nor entry.time came in; a shared key would block unrelated handovers
	claimed := false
	if ts == 0 {
		wacHandoverLog(channel, wacHandoverEventDedupSkipped).WithField("reason", "no_timestamp").Warn("wac handover dedup skipped")
	} else {
		first, err := wacHandoverClaimSeen(pool, channel.UUID(), urn, ts)
		switch {
		case err != nil:
			// fail-open: processing twice is better than losing the handover
			wacHandoverLog(channel, wacHandoverEventDedupFailed).WithError(err).Warn("wac handover dedup failed")
		case !first:
			wacHandoverLog(channel, wacHandoverEventDuplicate).Info("wac handover redelivery ignored")
			return wacHandoverInfoDuplicate, nil
		default:
			claimed = true
		}
	}

	info := wacHandoverInfoNoContext
	contextType, contextText, ok := renderWAHandoverContextText(resolveWAHandoverConversationContext(value))
	if ok {
		contactName := wacHandoverContactName(value.Contacts, urn)
		conversationContext := resolveWAHandoverConversationContext(value)

		logMessagingHandoverReceived(channel, messagingHandoverLogFields(value, occurredOn, string(urn.Identity()), contactName, contextType, "persisted"))

		if err := h.persistRenderedWAHandover(ctx, channel, urn, contactName, contextType, contextText, conversationContext, occurredOn, value.ControlPassed); err != nil {
			// release the key so Meta's redelivery is processed instead of ignored
			if claimed {
				releaseWACHandoverSeen(pool, channel.UUID(), urn, ts)
			}
			return "", err
		}
		info = wacHandoverInfoPersisted
	} else {
		logMessagingHandoverReceived(channel, messagingHandoverLogFields(value, occurredOn, string(urn.Identity()), "", "", "skipped_no_context"))
	}

	// Mailroom returns the empty text to the router when there is no pending row, so only write when there is one
	pending, err := h.Backend().HasPendingWAConversationHandover(ctx, channel.UUID(), urn)
	if err != nil {
		wacHandoverLog(channel, wacHandoverEventPendingCheckFailed).WithError(err).Error("wac handover pending check failed")
		return info, nil
	}
	if !pending {
		return info, nil
	}

	if !h.writeWACHandoverMessage(ctx, channel, urn, ts, occurredOn, value.ControlPassed) {
		return info, nil
	}
	if !ok {
		return wacHandoverInfoMessageWritten, nil
	}
	return info, nil
}

// writeWACHandoverMessage writes the empty inbound message. It has no ExternalID on purpose: Mailroom reuses it as
// ResponseToExternalID and Courier would quote a wamid that does not exist. A failure does not fail the webhook,
// since a redelivery would append the same context to the pending row again.
func (h *handler) writeWACHandoverMessage(
	ctx context.Context,
	channel courier.Channel,
	urn urns.URN,
	ts int64,
	occurredOn time.Time,
	controlPassed *wacHandoverControlPassed,
) bool {
	msg := h.Backend().NewIncomingMsg(channel, urn, "").WithMetadata(wacHandoverMessageMetadata(resolveControlPassedOwner(controlPassed)))
	if ts != 0 {
		msg = msg.WithReceivedOn(occurredOn)
	}

	if err := h.Backend().WriteMsg(ctx, msg); err != nil {
		wacHandoverLog(channel, wacHandoverEventMsgWriteFailed).WithError(err).WithField("msg_uuid", msg.UUID().String()).Error("wac handover message write failed")
		return false
	}

	wacHandoverLog(channel, wacHandoverEventMsgWritten).WithField("msg_uuid", msg.UUID().String()).Info("wac handover message written")
	return true
}
