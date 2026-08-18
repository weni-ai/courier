package rapidpro

import (
	"context"
	"testing"
	"time"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/stretchr/testify/assert"
)

func TestShouldQueueConversationStarted(t *testing.T) {
	assert.True(t, shouldQueueConversationStarted(1))
	assert.False(t, shouldQueueConversationStarted(0))
	assert.False(t, shouldQueueConversationStarted(2))
}

const (
	org1ChannelUUID = "dbc126ed-66bc-4e28-b67b-81dc3327c95d"
	org2ChannelUUID = "dbc126ed-66bc-4e28-b67b-81dc3327333a"
)

type ctwaSourceRow struct {
	ID         int64     `db:"id"`
	OrgID      int64     `db:"org_id"`
	SourceID   string    `db:"source_id"`
	SourceType string    `db:"source_type"`
	Headline   *string   `db:"headline"`
	Body       *string   `db:"body"`
	LastSeenAt time.Time `db:"last_seen_at"`
}

func newTestCtwaEvent(channelUUID courier.ChannelUUID, clid, sourceID string, referral courier.CtwaReferralSource) courier.CtwaEvent {
	urn, _ := urns.NewWhatsAppURN("12065551212")
	if referral.SourceType == "" {
		referral.SourceType = "ad"
	}
	referral.SourceID = sourceID
	return courier.CtwaEvent{
		CtwaClid:    clid,
		ContactUrn:  urn,
		Timestamp:   time.Now().UTC(),
		ChannelUUID: channelUUID,
		Waba:        "waba-test",
		Referral:    referral,
	}
}

func (ts *BackendTestSuite) writeTestCtwa(channelUUID string, clid, sourceID string, referral courier.CtwaReferralSource) error {
	uuid, err := courier.NewChannelUUID(channelUUID)
	ts.NoError(err)
	return writeCtwaToDB(context.Background(), ts.b, newTestCtwaEvent(uuid, clid, sourceID, referral))
}

func (ts *BackendTestSuite) loadReferralSources(sourceID string) []ctwaSourceRow {
	rows := []ctwaSourceRow{}
	err := ts.b.db.Select(&rows, `
		SELECT id, org_id, source_id, source_type, headline, body, last_seen_at
		FROM ctwa_referral_sources
		WHERE source_id = $1
		ORDER BY org_id
	`, sourceID)
	ts.NoError(err)
	return rows
}

func (ts *BackendTestSuite) TestWriteCtwaSetsOrgFromChannel() {
	sourceID := "ad-org-1"
	err := ts.writeTestCtwa(org1ChannelUUID, "clid-org-1", sourceID, courier.CtwaReferralSource{
		Headline: "Hello",
		Body:     "World",
	})
	ts.NoError(err)

	sources := ts.loadReferralSources(sourceID)
	ts.Len(sources, 1)
	ts.Equal(int64(1), sources[0].OrgID)
	ts.Equal("ad", sources[0].SourceType)
	ts.Equal("Hello", *sources[0].Headline)
	ts.Equal("World", *sources[0].Body)

	var eventCount int
	err = ts.b.db.Get(&eventCount, `SELECT count(*) FROM conversion_events_ctwa WHERE ctwa_clid = $1`, "clid-org-1")
	ts.NoError(err)
	ts.Equal(1, eventCount)
}

func (ts *BackendTestSuite) TestWriteCtwaClonesSourceAcrossOrgs() {
	sourceID := "shared-ad"
	err := ts.writeTestCtwa(org1ChannelUUID, "clid-shared-org-1", sourceID, courier.CtwaReferralSource{
		Headline: "Shared",
	})
	ts.NoError(err)

	err = ts.writeTestCtwa(org2ChannelUUID, "clid-shared-org-2", sourceID, courier.CtwaReferralSource{
		Headline: "Shared",
	})
	ts.NoError(err)

	sources := ts.loadReferralSources(sourceID)
	ts.Len(sources, 2)
	ts.Equal(int64(1), sources[0].OrgID)
	ts.Equal(int64(2), sources[1].OrgID)
	ts.NotEqual(sources[0].ID, sources[1].ID)

	var referralIDs []int64
	err = ts.b.db.Select(&referralIDs, `
		SELECT referral_source_id FROM conversion_events_ctwa
		WHERE ctwa_clid IN ('clid-shared-org-1', 'clid-shared-org-2')
		ORDER BY ctwa_clid
	`)
	ts.NoError(err)
	ts.Len(referralIDs, 2)
	ts.NotEqual(referralIDs[0], referralIDs[1])
}

func (ts *BackendTestSuite) TestWriteCtwaUpdatesExistingSourceForSameOrg() {
	sourceID := "repeat-ad"
	err := ts.writeTestCtwa(org1ChannelUUID, "clid-repeat-1", sourceID, courier.CtwaReferralSource{
		Headline: "First",
		Body:     "Original",
	})
	ts.NoError(err)

	_, err = ts.b.db.Exec(`
		UPDATE ctwa_referral_sources
		SET last_seen_at = NOW() - INTERVAL '1 hour'
		WHERE source_id = $1 AND org_id = 1
	`, sourceID)
	ts.NoError(err)

	before := ts.loadReferralSources(sourceID)
	ts.Len(before, 1)

	err = ts.writeTestCtwa(org1ChannelUUID, "clid-repeat-2", sourceID, courier.CtwaReferralSource{
		Headline: "Updated",
		Body:     "New body",
	})
	ts.NoError(err)

	after := ts.loadReferralSources(sourceID)
	ts.Len(after, 1)
	ts.Equal(before[0].ID, after[0].ID)
	ts.Equal("Updated", *after[0].Headline)
	ts.Equal("New body", *after[0].Body)
	ts.True(after[0].LastSeenAt.After(before[0].LastSeenAt))
}

func (ts *BackendTestSuite) TestWriteCtwaErrorsWhenChannelMissing() {
	unknownUUID, err := courier.NewChannelUUID("11111111-1111-1111-1111-111111111111")
	ts.NoError(err)

	err = writeCtwaToDB(context.Background(), ts.b, newTestCtwaEvent(unknownUUID, "clid-missing", "missing-ad", courier.CtwaReferralSource{}))
	ts.Error(err)
	ts.Contains(err.Error(), "unable to find channel")

	sources := ts.loadReferralSources("missing-ad")
	ts.Empty(sources)
}
