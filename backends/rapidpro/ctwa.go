package rapidpro

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/null"
	"github.com/sirupsen/logrus"
)

const upsertCtwaReferralSourceSQL = `
INSERT INTO ctwa_referral_sources
	(source_id, source_type, source_url, headline, body, first_seen_at, last_seen_at)
VALUES
	(:source_id, :source_type, :source_url, :headline, :body, NOW(), NOW())
ON CONFLICT (source_id, source_type) DO UPDATE SET
	source_url = EXCLUDED.source_url,
	headline = COALESCE(NULLIF(EXCLUDED.headline, ''), ctwa_referral_sources.headline),
	body = COALESCE(NULLIF(EXCLUDED.body, ''), ctwa_referral_sources.body),
	last_seen_at = NOW(),
	updated_at = NOW()
RETURNING id
`

const insertCtwaSQL = `
INSERT INTO conversion_events_ctwa
	(ctwa_clid, contact_urn, timestamp, channel_uuid, waba, phone_number_id, referral_source_id, message_id)
VALUES
	(:ctwa_clid, :contact_urn, :timestamp, :channel_uuid, :waba, :phone_number_id, :referral_source_id, :message_id)
ON CONFLICT (ctwa_clid) DO NOTHING
`

type DBCtwaReferralSource struct {
	SourceID   string      `db:"source_id"`
	SourceType string      `db:"source_type"`
	SourceURL  null.String `db:"source_url"`
	Headline   null.String `db:"headline"`
	Body       null.String `db:"body"`
}

type DBCtwa struct {
	CtwaClid         null.String `db:"ctwa_clid"`
	ContactUrn       string      `db:"contact_urn"`
	Timestamp        time.Time   `db:"timestamp"`
	ChannelUUID      string      `db:"channel_uuid"`
	Waba             string      `db:"waba"`
	PhoneNumberID    null.String `db:"phone_number_id"`
	ReferralSourceID int64       `db:"referral_source_id"`
	MessageID        null.String `db:"message_id"`
}

func (c *DBCtwa) RowID() string {
	return ""
}

func writeCtwa(ctx context.Context, b *backend, event courier.CtwaEvent) error {
	err := writeCtwaToDB(ctx, b, event)
	if err != nil {
		logrus.WithError(err).WithField("ctwa_clid", event.CtwaClid).Error("error writing ctwa to db")
		err = courier.WriteToSpool(b.config.SpoolDir, "ctwa", event)
	}

	return err
}

func writeCtwaToDB(ctx context.Context, b *backend, event courier.CtwaEvent) error {
	sourceType, ok := courier.NormalizeCtwaSourceType(event.Referral.SourceType)
	if !ok {
		logrus.WithField("source_type", event.Referral.SourceType).Warn("invalid ctwa source_type, skipping")
		return nil
	}
	if event.Referral.SourceID == "" {
		logrus.Warn("missing ctwa source_id, skipping")
		return nil
	}

	tx, err := b.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	referralSourceID, err := upsertCtwaReferralSource(tx, &DBCtwaReferralSource{
		SourceID:   event.Referral.SourceID,
		SourceType: sourceType,
		SourceURL:  nullStringFromValue(event.Referral.SourceURL),
		Headline:   nullStringFromValue(event.Referral.Headline),
		Body:       nullStringFromValue(event.Referral.Body),
	})
	if err != nil {
		return err
	}

	ctwa := &DBCtwa{
		CtwaClid:         nullStringFromValue(event.CtwaClid),
		ContactUrn:       event.ContactUrn.String(),
		Timestamp:        event.Timestamp,
		ChannelUUID:      event.ChannelUUID.String(),
		Waba:             event.Waba,
		PhoneNumberID:    nullStringFromValue(event.PhoneNumberID),
		ReferralSourceID: referralSourceID,
		MessageID:        nullStringFromValue(event.MessageID),
	}

	_, err = tx.NamedExecContext(ctx, insertCtwaSQL, ctwa)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func upsertCtwaReferralSource(tx *sqlx.Tx, referral *DBCtwaReferralSource) (int64, error) {
	rows, err := tx.NamedQuery(upsertCtwaReferralSourceSQL, referral)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, fmt.Errorf("no id returned from ctwa referral source upsert")
	}

	var referralSourceID int64
	if err := rows.Scan(&referralSourceID); err != nil {
		return 0, err
	}

	return referralSourceID, nil
}

func nullStringFromValue(value string) null.String {
	return null.String(value)
}

func (b *backend) flushCtwaFile(filename string, contents []byte) error {
	event := courier.CtwaEvent{}
	err := json.Unmarshal(contents, &event)
	if err != nil {
		log.Printf("ERROR unmarshalling spool file '%s', renaming: %s\n", filename, err)
		os.Rename(filename, fmt.Sprintf("%s.error", filename))
		return nil
	}

	return writeCtwaToDB(context.Background(), b, event)
}
