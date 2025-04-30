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
)

const updateContactLastSeenSQL = `
UPDATE contacts_contact SET 
	last_seen_on = c.last_seen_on::timestamp
FROM
	(VALUES(:id, :last_seen_on))
AS
	c(id, last_seen_on)
WHERE 
	contacts_contact.id = c.id::integer
RETURNING 
	contacts_contact.id
`

type DBContactLastSeen struct {
	ID_         ContactID `json:"id" db:"id"`
	LastSeenOn_ time.Time `json:"last_seen_on" db:"last_seen_on"`
}

func (c *DBContactLastSeen) RowID() string {
	return c.ID_.String()
}

func writeContactLastSeen(ctx context.Context, b *backend, contact *DBContact, lastSeenOn time.Time) error {
	contactLastSeen := &DBContactLastSeen{
		ID_:         contact.ID(),
		LastSeenOn_: lastSeenOn,
	}

	err := writeContactLastSeenToDB(ctx, b, contactLastSeen)

	if err == courier.ErrMsgNotFound {
		return err
	}

	// failed writing, write to our spool instead
	if err != nil {
		err = courier.WriteToSpool(b.config.SpoolDir, "contact_last_seens", contact)
	}

	return err
}

func writeContactLastSeenToDB(ctx context.Context, b *backend, contact *DBContactLastSeen) error {
	var rows *sqlx.Rows
	var err error

	rows, err = b.db.NamedQueryContext(ctx, updateContactLastSeenSQL, contact)

	if err != nil {
		return err
	}
	defer rows.Close()

	if rows.Next() {
		rows.Scan(&contact.ID_)
	} else {
		return courier.ErrContactNotFound
	}

	return nil
}

func (b *backend) flushContactLastSeenFile(filename string, contents []byte) error {
	contactLastSeen := &DBContactLastSeen{}
	err := json.Unmarshal(contents, contactLastSeen)
	if err != nil {
		log.Printf("ERROR unmarshalling spool file '%s', renaming: %s\n", filename, err)
		os.Rename(filename, fmt.Sprintf("%s.error", filename))
		return nil
	}

	// try to flush to our db
	err = writeContactLastSeenToDB(context.Background(), b, contactLastSeen)

	// not finding the contact is ok for last seen updates
	if err == courier.ErrContactNotFound {
		return nil
	}

	return err
}
