package courier

import (
	"strings"
	"time"

	"github.com/nyaruka/gocommon/urns"
)

// CtwaReferralSource holds campaign/source data from a Meta referral object.
type CtwaReferralSource struct {
	SourceID   string
	SourceType string
	SourceURL  string
	Headline   string
	Body       string
}

// CtwaEvent represents a click-to-WhatsApp conversion event to persist.
type CtwaEvent struct {
	CtwaClid      string
	ContactUrn    urns.URN
	Timestamp     time.Time
	ChannelUUID   ChannelUUID
	Waba          string
	PhoneNumberID string
	MessageID     string
	Referral      CtwaReferralSource
}

// NormalizeCtwaSourceType maps Meta source_type values to the ctwa_referral_sources constraint.
func NormalizeCtwaSourceType(sourceType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(sourceType)) {
	case "ad":
		return "ad", true
	case "post":
		return "post", true
	default:
		return "", false
	}
}
