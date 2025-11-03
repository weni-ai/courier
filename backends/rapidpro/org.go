package rapidpro

import (
	"context"
	"database/sql/driver"

	"github.com/jmoiron/sqlx"
	"github.com/nyaruka/null"
)

// OrgID is our type for database Org ids
type OrgID null.Int

// NilOrgID is our nil value for OrgID
var NilOrgID = OrgID(0)

// MarshalJSON marshals into JSON. 0 values will become null
func (i OrgID) MarshalJSON() ([]byte, error) {
	return null.Int(i).MarshalJSON()
}

// UnmarshalJSON unmarshals from JSON. null values become 0
func (i *OrgID) UnmarshalJSON(b []byte) error {
	return null.UnmarshalInt(b, (*null.Int)(i))
}

// Value returns the db value, null is returned for 0
func (i OrgID) Value() (driver.Value, error) {
	return null.Int(i).Value()
}

// Scan scans from the db value. null values become 0
func (i *OrgID) Scan(value interface{}) error {
	return null.ScanInt(value, (*null.Int)(i))
}

const lookupOrgFromChannelUUID = `
SELECT
       ch.org_id as id,
       org.proj_uuid as proj_uuid
FROM
       channels_channel ch
       JOIN orgs_org org on ch.org_id = org.id
WHERE
       ch.uuid = $1 AND
       ch.is_active = true AND
       ch.org_id IS NOT NULL`

type DBOrg struct {
	ID_       OrgID  `db:"id"`
	ProjUUID_ string `db:"proj_uuid"`
}

func getProjectUUIDFromChannelUUID(ctx context.Context, db *sqlx.DB, channelUUID string) (string, error) {
	var org DBOrg
	err := db.GetContext(ctx, &org, lookupOrgFromChannelUUID, channelUUID)
	if err != nil {
		return "", err
	}
	return org.ProjUUID_, nil
}
