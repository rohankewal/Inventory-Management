package core

import (
	"database/sql/driver"
	"fmt"

	"github.com/google/uuid"
)

// ID is a UUIDv7 held as its canonical string form.
//
// v7 is time-ordered, so IDs sort by creation and index well as a primary key.
// Storing the string keeps SQLite and Postgres byte-identical, which is what
// lets one conformance suite cover both drivers.
type ID string

// NilID is the zero value, used for "not set" and for optional references.
const NilID ID = ""

// NewID returns a fresh time-ordered identifier.
func NewID() ID {
	u, err := uuid.NewV7()
	if err != nil {
		// NewV7 only fails if the system entropy source does, which we cannot
		// meaningfully continue past.
		panic(fmt.Sprintf("core: generating uuid: %v", err))
	}
	return ID(u.String())
}

// ParseID validates s and returns it as an ID.
func ParseID(s string) (ID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return NilID, fmt.Errorf("%w: %q is not a valid id", ErrInvalid, s)
	}
	return ID(u.String()), nil
}

// IsZero reports whether the ID is unset.
func (id ID) IsZero() bool { return id == NilID }

func (id ID) String() string { return string(id) }

// Value implements driver.Valuer so IDs can be passed directly as query args.
func (id ID) Value() (driver.Value, error) {
	if id.IsZero() {
		return nil, nil
	}
	return string(id), nil
}

// Scan implements sql.Scanner.
func (id *ID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*id = NilID
	case string:
		*id = ID(v)
	case []byte:
		*id = ID(v)
	default:
		return fmt.Errorf("core: cannot scan %T into ID", src)
	}
	return nil
}
