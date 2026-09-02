package core

import (
	"strings"
	"unicode/utf8"
)

// Field limits for addresses and contacts.
const (
	MaxAddressLineLen = 200
	MaxCityLen        = 100
	MaxPostalCodeLen  = 20
	MaxCountryLen     = 60
	MaxContactLen     = 120
	MaxEmailLen       = 254
	MaxPhoneLen       = 40
)

// Address is a physical destination.
//
// It is stored as flat columns rather than a normalised address table because
// a ship-to address is a snapshot of where goods went, not a shared reference:
// when a store relocates, last season's delivery paperwork must still show the
// address the goods actually arrived at.
type Address struct {
	Line1      string
	Line2      string
	City       string
	Region     string // state, province or county
	PostalCode string
	Country    string
}

// Normalize trims each part.
func (a *Address) Normalize() {
	a.Line1 = strings.TrimSpace(a.Line1)
	a.Line2 = strings.TrimSpace(a.Line2)
	a.City = strings.TrimSpace(a.City)
	a.Region = strings.TrimSpace(a.Region)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	a.Country = strings.TrimSpace(a.Country)
}

// IsEmpty reports whether no part of the address is set.
func (a Address) IsEmpty() bool {
	return a.Line1 == "" && a.Line2 == "" && a.City == "" &&
		a.Region == "" && a.PostalCode == "" && a.Country == ""
}

// Lines renders the address the way it would be printed on a label, omitting
// the parts that are not set.
func (a Address) Lines() []string {
	var out []string
	for _, part := range []string{a.Line1, a.Line2} {
		if part != "" {
			out = append(out, part)
		}
	}

	// City, region and postal code share a line, in the order most of the
	// world writes them.
	var locality []string
	if a.City != "" {
		locality = append(locality, a.City)
	}
	if a.Region != "" {
		locality = append(locality, a.Region)
	}
	line := strings.Join(locality, ", ")
	if a.PostalCode != "" {
		if line != "" {
			line += " "
		}
		line += a.PostalCode
	}
	if line != "" {
		out = append(out, line)
	}

	if a.Country != "" {
		out = append(out, a.Country)
	}
	return out
}

// SingleLine renders the address on one line, for a table cell.
func (a Address) SingleLine() string { return strings.Join(a.Lines(), ", ") }

// Validate appends any problems, prefixing field names so a form can attach
// each message to the right input.
func (a Address) Validate(prefix string, v *ValidationError) {
	check := func(field, value string, max int) {
		if utf8.RuneCountInString(value) > max {
			v.Add(prefix+field, "%s must be %d characters or fewer", labelFor(field), max)
		}
	}

	check("line1", a.Line1, MaxAddressLineLen)
	check("line2", a.Line2, MaxAddressLineLen)
	check("city", a.City, MaxCityLen)
	check("region", a.Region, MaxCityLen)
	check("postal_code", a.PostalCode, MaxPostalCodeLen)
	check("country", a.Country, MaxCountryLen)
}

func labelFor(field string) string {
	switch field {
	case "line1":
		return "Address line 1"
	case "line2":
		return "Address line 2"
	case "city":
		return "City"
	case "region":
		return "State or region"
	case "postal_code":
		return "Postal code"
	case "country":
		return "Country"
	}
	return field
}

// Contact is who to reach at a place.
type Contact struct {
	Name  string
	Email string
	Phone string
}

// Normalize trims each part and lower-cases the email.
func (c *Contact) Normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	c.Phone = strings.TrimSpace(c.Phone)
}

// IsEmpty reports whether no part of the contact is set.
func (c Contact) IsEmpty() bool {
	return c.Name == "" && c.Email == "" && c.Phone == ""
}

// Validate appends any problems.
//
// The email check is deliberately shallow — one "@" with something either side.
// Rejecting a real address because it fails a clever pattern is worse than
// accepting a typo, which the first bounced email reveals anyway.
func (c Contact) Validate(prefix string, v *ValidationError) {
	if utf8.RuneCountInString(c.Name) > MaxContactLen {
		v.Add(prefix+"contact_name", "Contact name must be %d characters or fewer", MaxContactLen)
	}
	if utf8.RuneCountInString(c.Phone) > MaxPhoneLen {
		v.Add(prefix+"contact_phone", "Phone must be %d characters or fewer", MaxPhoneLen)
	}

	if c.Email == "" {
		return
	}
	if utf8.RuneCountInString(c.Email) > MaxEmailLen {
		v.Add(prefix+"contact_email", "Email must be %d characters or fewer", MaxEmailLen)
		return
	}
	local, domain, found := strings.Cut(c.Email, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		v.Add(prefix+"contact_email", "%q does not look like an email address", c.Email)
	}
}
