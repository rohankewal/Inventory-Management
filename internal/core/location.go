package core

import (
	"strings"
	"time"
	"unicode/utf8"
)

// DefaultLocationID is seeded by the initial migration. Single-site installs
// never see it, but every ledger row is keyed by location from day one so that
// adding warehouses later is a feature, not a data migration.
const DefaultLocationID ID = "01930000-0000-7000-8000-000000000001"

// Location is a place stock can sit: a warehouse, a shop floor, a van, a bin.
type Location struct {
	ID        ID
	Code      string
	Name      string
	IsDefault bool
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Normalize trims whitespace and upper-cases the short code.
func (l *Location) Normalize() {
	l.Code = strings.ToUpper(strings.TrimSpace(l.Code))
	l.Name = strings.TrimSpace(l.Name)
}

// Validate reports every problem with the location at once.
func (l *Location) Validate() error {
	var v ValidationError

	if l.Code == "" {
		v.Add("code", "Code is required")
	} else if utf8.RuneCountInString(l.Code) > MaxSKULen {
		v.Add("code", "Code must be %d characters or fewer", MaxSKULen)
	}
	if l.Name == "" {
		v.Add("name", "Name is required")
	} else if utf8.RuneCountInString(l.Name) > MaxNameLen {
		v.Add("name", "Name must be %d characters or fewer", MaxNameLen)
	}

	return v.ErrOrNil()
}
