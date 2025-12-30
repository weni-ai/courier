package courier

import (
	"errors"
	"strings"

	"github.com/gofrs/uuid"
)

// ErrContactNotFound is returned when trying to queue the last seen for a Contact that doesn't exit
var ErrContactNotFound = errors.New("contact not found")

// ContactUUID is our typing of a contact's UUID
type ContactUUID struct {
	uuid.UUID
}

// NilContactUUID is our nil value for contact UUIDs
var NilContactUUID = ContactUUID{uuid.Nil}

// NewContactUUID creates a new ContactUUID for the passed in string
func NewContactUUID(u string) (ContactUUID, error) {
	contactUUID, err := uuid.FromString(strings.ToLower(u))
	if err != nil {
		return NilContactUUID, err
	}
	return ContactUUID{contactUUID}, nil
}

//-----------------------------------------------------------------------------
// Contact Interface
//-----------------------------------------------------------------------------

// Contact defines the attributes on a contact, for our purposes that is just a contact UUID
type Contact interface {
	UUID() ContactUUID
}
