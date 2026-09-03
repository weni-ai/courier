package courier

import (
	"encoding/json"
	"time"

	"github.com/nyaruka/gocommon/urns"
)

const (
	WAHandoverContextHistory = "history"
	WAHandoverContextSummary = "summary"
)

// WAConversationHandoverEvent represents a WhatsApp Conversation Orchestration handover to persist.
type WAConversationHandoverEvent struct {
	ChannelUUID ChannelUUID
	ContactURN  urns.URN
	ContactName string

	ContextType    string
	ContextText    string
	ContextPayload json.RawMessage

	PreviousOwnerAppID      string
	PreviousOwnerAppRole    string
	PreviousOwnerBusinessID string
	HandoverMetadata        string

	OccurredOn time.Time
}
