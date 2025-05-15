package rapidpro

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nyaruka/courier"
	"github.com/nyaruka/gocommon/urns"
	"github.com/nyaruka/gocommon/uuids"
	"github.com/nyaruka/librato"
	"github.com/nyaruka/null"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// used by unit tests to slow down urn operations to test races
var urnSleep bool

// ContactID is our representation of our database contact id
type ContactID null.Int

// NilContactID represents our nil value for ContactID
var NilContactID = ContactID(0)

// MarshalJSON marshals into JSON. 0 values will become null
func (i ContactID) MarshalJSON() ([]byte, error) {
	return null.Int(i).MarshalJSON()
}

// UnmarshalJSON unmarshals from JSON. null values become 0
func (i *ContactID) UnmarshalJSON(b []byte) error {
	return null.UnmarshalInt(b, (*null.Int)(i))
}

// Value returns the db value, null is returned for 0
func (i ContactID) Value() (driver.Value, error) {
	return null.Int(i).Value()
}

// Scan scans from the db value. null values become 0
func (i *ContactID) Scan(value interface{}) error {
	return null.ScanInt(value, (*null.Int)(i))
}

// String returns a string representation of the id
func (i ContactID) String() string {
	if i != NilContactID {
		return strconv.FormatInt(int64(i), 10)
	}
	return "null"
}

const insertContactSQL = `
INSERT INTO 
	contacts_contact(org_id, is_active, status, uuid, created_on, modified_on, created_by_id, modified_by_id, name, ticket_count) 
              VALUES(:org_id, TRUE, 'A', :uuid, :created_on, :modified_on, :created_by_id, :modified_by_id, :name, 0)
RETURNING id
`

// insertContact inserts the passed in contact, the id field will be populated with the result on success
func insertContact(tx *sqlx.Tx, contact *DBContact) error {
	rows, err := tx.NamedQuery(insertContactSQL, contact)
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		err = rows.Scan(&contact.ID_)
	}
	return err
}

const lookupContactFromURNSQL = `
SELECT 
	c.org_id, 
	c.id, 
	c.uuid, 
	c.modified_on, 
	c.created_on, 
	c.name, 
	u.id as "urn_id",
	c.status,
	c.last_seen_on
FROM 
	contacts_contact AS c, 
	contacts_contacturn AS u 
WHERE 
	u.identity = $1 AND 
	u.contact_id = c.id AND 
	u.org_id = $2 AND 
	c.is_active = TRUE
`

const lookupContactFromTeamsURNSQL = `
SELECT 
	c.org_id, 
	c.id, 
	c.uuid, 
	c.modified_on, 
	c.created_on, 
	c.name, 
	u.id as "urn_id",
	c.status,
	c.last_seen_on
FROM 
	contacts_contact AS c, 
	contacts_contacturn AS u 
WHERE 
	u.identity ~ $1 AND 
	u.contact_id = c.id AND 
	u.org_id = $2 AND 
	c.is_active = TRUE
	ORDER BY c.modified_on ASC
	LIMIT 1
`

func contactForURNTeams(ctx context.Context, b *backend, urn urns.URN, org OrgID) (*DBContact, error) {
	contact := &DBContact{}

	urnIdentity := strings.Split(urn.Identity().String(), ":serviceURL:")
	err := b.db.GetContext(ctx, contact, lookupContactFromTeamsURNSQL, urnIdentity[0], org)
	if err != nil && err != sql.ErrNoRows {
		logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
		return contact, err
	}

	if err == sql.ErrNoRows {
		return contact, err
	}

	err = updateContactTeamsURN(ctx, b.db, contact.URNID_, string(urn.Identity()))
	if err != nil {
		logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error updating contact urn")
		return contact, err
	}
	return contact, nil
}

// contactForURN first tries to look up a contact for the passed in URN, if not finding one then creating one
func contactForURN(ctx context.Context, b *backend, org OrgID, channel *DBChannel, urn urns.URN, auth string, name string) (*DBContact, error) {
	// try to look up our contact by URN
	contact := &DBContact{}

	// debug query raw sql response
	rows, err := b.db.QueryxContext(ctx, lookupContactFromURNSQL, urn.Identity(), org)
	if err != nil {
		logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
		return nil, err
	}
	defer rows.Close()

	if rows.Next() {
		err = rows.Scan(&contact.OrgID_, &contact.ID_, &contact.UUID_, &contact.ModifiedOn_, &contact.CreatedOn_, &contact.Name_, &contact.URNID_, &contact.Status_, &contact.LastSeenOn_)
		if err != nil {
			logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
			return nil, err
		}
	}

	err = b.db.GetContext(ctx, contact, lookupContactFromURNSQL, urn.Identity(), org)
	if err != nil && err != sql.ErrNoRows {
		logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
		return nil, err
	}

	if urn.Scheme() == "teams" && err == sql.ErrNoRows {
		contact, err = contactForURNTeams(ctx, b, urn, org)
		if err != nil && err != sql.ErrNoRows {
			logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
			return nil, err
		}
	}

	if err == sql.ErrNoRows {
		// we not found a contact with the given urn, so try find one with another variation with or without extra 9
		if urn.Scheme() == urns.WhatsAppScheme && strings.HasPrefix(urn.Path(), "55") {
			urnVariation := newWhatsappURNVariation(urn)
			if urnVariation != nil {
				err = b.db.GetContext(ctx, contact, lookupContactFromURNSQL, urnVariation.Identity(), org)
				if err != nil && err != sql.ErrNoRows {
					logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
					return nil, err
				}
			}
		}
	}

	// we found it, return it
	if err != sql.ErrNoRows {
		// insert it
		tx, err := b.db.BeginTxx(ctx, nil)
		if err != nil {
			logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
			return nil, err
		}

		err = setDefaultURN(tx, channel, contact, urn, auth)
		if err != nil {
			logrus.WithError(err).WithField("urn", urn.Identity()).WithField("org_id", org).Error("error looking up contact")
			tx.Rollback()
			return nil, err
		}
		return contact, tx.Commit()
	}

	// didn't find it, we need to create it instead
	contact.OrgID_ = org
	contact.UUID_, _ = courier.NewContactUUID(string(uuids.New()))
	contact.CreatedOn_ = time.Now()
	contact.ModifiedOn_ = time.Now()
	contact.LastSeenOn_ = nil
	contact.IsNew_ = true

	// if we aren't an anonymous org, we want to look up a name if possible and set it
	if !channel.OrgIsAnon() {
		// no name was passed in, see if our handler can look up information for this URN
		if name == "" {
			handler := courier.GetHandler(channel.ChannelType())
			if handler != nil {
				describer, isDescriber := handler.(courier.URNDescriber)
				if isDescriber {
					atts, err := describer.DescribeURN(ctx, channel, urn)

					// in the case of errors, we log the error but move onwards anyways
					if err != nil {
						logrus.WithField("channel_uuid", channel.UUID()).WithField("channel_type", channel.ChannelType()).WithField("urn", urn).WithError(err).Error("unable to describe URN")
					} else {
						name = atts["name"]
					}
				}
			}
		}

		if name != "" {
			if utf8.RuneCountInString(name) > 128 {
				name = string([]rune(name)[:127])
			}

			contact.Name_ = null.String(name)
		}
	}

	// TODO: Set these to a system user
	contact.CreatedBy_ = 1
	contact.ModifiedBy_ = 1

	// insert it
	tx, err := b.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}

	err = insertContact(tx, contact)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// used for unit testing contact races
	if urnSleep {
		time.Sleep(time.Millisecond * 50)
	}

	// associate our URN
	// If we've inserted a duplicate URN then we'll get a uniqueness violation.
	// That means this contact URN was written by someone else after we tried to look it up.
	contactURN, err := contactURNForURN(tx, channel, contact.ID_, urn, auth)
	if err != nil {
		tx.Rollback()
		if pqErr, ok := err.(*pq.Error); ok {
			// if this was a duplicate URN, start over with a contact lookup
			if pqErr.Code.Name() == "unique_violation" {
				return contactForURN(ctx, b, org, channel, urn, auth, name)
			}
		}
		return nil, err
	}

	// we stole the URN from another contact, roll back and start over
	if contactURN.PrevContactID != NilContactID {
		tx.Rollback()
		return contactForURN(ctx, b, org, channel, urn, auth, name)
	}

	// all is well, we created the new contact, commit and move forward
	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	// store this URN on our contact
	contact.URNID_ = contactURN.ID

	// log that we created a new contact to librato
	librato.Gauge("courier.new_contact", float64(1))

	// and return it
	return contact, nil
}

func newWhatsappURNVariation(urn urns.URN) *urns.URN {
	path := urn.Path()
	pathVariation := ""
	if urn.Scheme() == urns.WhatsAppScheme && strings.HasPrefix(path, "55") {
		addNine := !(len(path) == 13 && string(path[4]) == "9")
		if addNine {
			// provide with extra 9
			pathVariation = path[:4] + "9" + path[4:]
		} else {
			// provide without extra 9
			pathVariation = path[:4] + path[5:]
		}
	}

	if pathVariation != "" {
		urnVariation, _ := urns.NewURNFromParts(urn.Scheme(), pathVariation, "", "")
		return &urnVariation
	}

	return nil
}

// DBContact is our struct for a contact in the database
type DBContact struct {
	OrgID_ OrgID               `db:"org_id"`
	ID_    ContactID           `db:"id"`
	UUID_  courier.ContactUUID `db:"uuid"`
	Name_  null.String         `db:"name"`

	URNID_ ContactURNID `db:"urn_id"`

	CreatedOn_  time.Time  `db:"created_on"`
	ModifiedOn_ time.Time  `db:"modified_on"`
	LastSeenOn_ *time.Time `db:"last_seen_on"`

	CreatedBy_  int `db:"created_by_id"`
	ModifiedBy_ int `db:"modified_by_id"`

	IsNew_  bool
	Status_ string `db:"status"`
}

// UUID returns the UUID for this contact
func (c *DBContact) UUID() courier.ContactUUID { return c.UUID_ }

// ID returns the ID for this contact
func (c *DBContact) ID() ContactID { return c.ID_ }
