package core

import (
	"encoding/json"
	"sort"
	"strings"
)

// Tags are free-form labels on a product.
//
// They are stored in one column rather than a join table because the only
// query that matters is "does this product carry this tag", and a delimited
// column answers that with a LIKE against an index while keeping import,
// export and bulk edit trivial. A join table earns its cost when tags become
// first-class objects with their own colours and permissions; they are not
// that yet.
type Tags []string

// MaxTagLen bounds one tag.
const MaxTagLen = 40

// ParseTags reads a comma-separated list, discarding blanks and duplicates.
func ParseTags(s string) Tags {
	if strings.TrimSpace(s) == "" {
		return nil
	}

	seen := map[string]bool{}
	var out Tags
	for _, raw := range strings.Split(s, ",") {
		tag := normalizeTag(raw)
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		out = append(out, tag)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}

func normalizeTag(s string) string {
	// Commas would corrupt the delimited storage form, and newlines would
	// break every list rendering.
	s = strings.TrimSpace(strings.NewReplacer(",", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s))
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) > MaxTagLen {
		s = string([]rune(s)[:MaxTagLen])
	}
	return s
}

// String renders tags for display and CSV export.
func (t Tags) String() string { return strings.Join(t, ", ") }

// Storage renders the delimited form written to the database.
//
// It is wrapped in delimiters — "|urgent|fragile|" — so that a search for
// "|fragile|" matches the whole tag and never a prefix of another one. Without
// the wrapping, filtering on "car" would also match "cardboard".
func (t Tags) Storage() string {
	if len(t) == 0 {
		return ""
	}
	return "|" + strings.Join(t, "|") + "|"
}

// ParseTagStorage reads the delimited database form back into tags.
func ParseTagStorage(s string) Tags {
	s = strings.Trim(s, "|")
	if s == "" {
		return nil
	}

	var out Tags
	for _, tag := range strings.Split(s, "|") {
		if tag = strings.TrimSpace(tag); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// TagPattern is the LIKE pattern matching one tag exactly.
func TagPattern(tag string) string {
	return "%|" + normalizeTag(tag) + "|%"
}

// Has reports whether the tag is present, ignoring case.
func (t Tags) Has(tag string) bool {
	for _, candidate := range t {
		if strings.EqualFold(candidate, tag) {
			return true
		}
	}
	return false
}

// CustomFields are user-defined attributes on a product — a serial prefix, a
// shelf code, a warranty period. Every business tracks something the schema
// did not anticipate, and the alternative to supporting that is watching them
// overload the description field.
type CustomFields map[string]string

// MaxCustomFields bounds how many a single product may carry, so a bad import
// cannot turn one row into a document.
const MaxCustomFields = 30

// Encode renders custom fields for storage. Key order is stable so that saving
// a product twice without changes produces identical bytes.
func (c CustomFields) Encode() (string, error) {
	if len(c) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DecodeCustomFields reads the stored form. A value that cannot be parsed
// yields no fields rather than an error: one malformed row must not make a
// product unopenable.
func DecodeCustomFields(s string) CustomFields {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out CustomFields
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

// Keys returns the field names in a stable order for rendering.
func (c CustomFields) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
