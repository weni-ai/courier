package rapidpro

import (
	"context"
	"time"

	"github.com/nyaruka/courier"
	"github.com/sirupsen/logrus"
)

const insertCtwaSQL = `
INSERT INTO 
	conversion_events_ctwa(ctwa_clid, contact_urn, timestamp, channel_uuid, waba)
	VALUES(:ctwa_clid, :contact_urn, :timestamp, :channel_uuid, :waba)
`

// DBCtwa is our DB specific struct for ctwa records
type DBCtwa struct {
	CtwaClid    string    `db:"ctwa_clid"`
	ContactUrn  string    `db:"contact_urn"`
	Timestamp   time.Time `db:"timestamp"`
	ChannelUUID string    `db:"channel_uuid"`
	Waba        string    `db:"waba"`
}

// RowID satisfies our batch.Value interface, we are always inserting ctwa records so we have no row id
func (c *DBCtwa) RowID() string {
	return ""
}

// writeCtwa writes the passed in ctwa record to the database
func writeCtwa(ctx context.Context, b *backend, ctwa *DBCtwa) error {
	err := writeCtwaToDB(ctx, b, ctwa)

	// failed writing, write to our spool instead
	if err != nil {
		logrus.WithError(err).WithField("ctwa_clid", ctwa.CtwaClid).Error("error writing ctwa to db")
		err = courier.WriteToSpool(b.config.SpoolDir, "ctwa", ctwa)
	}

	return err
}

// writeCtwaToDB writes the passed in ctwa record to the database
func writeCtwaToDB(ctx context.Context, b *backend, ctwa *DBCtwa) error {
	_, err := b.db.NamedExecContext(ctx, insertCtwaSQL, ctwa)
	if err != nil {
		return err
	}

	return nil
}
