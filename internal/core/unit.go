package core

import "strings"

// UnitOfMeasure is how a product is counted.
//
// Quantities stay integers throughout the ledger; a product sold by weight is
// tracked in its smallest whole unit (grams, millilitres) rather than as a
// float, for the same reason money is. Fractional stock arithmetic accumulates
// error exactly where it is least acceptable — the count on the shelf.
type UnitOfMeasure string

const (
	UnitEach       UnitOfMeasure = "each"
	UnitBox        UnitOfMeasure = "box"
	UnitCase       UnitOfMeasure = "case"
	UnitPallet     UnitOfMeasure = "pallet"
	UnitPack       UnitOfMeasure = "pack"
	UnitPair       UnitOfMeasure = "pair"
	UnitSet        UnitOfMeasure = "set"
	UnitGram       UnitOfMeasure = "g"
	UnitKilogram   UnitOfMeasure = "kg"
	UnitPound      UnitOfMeasure = "lb"
	UnitOunce      UnitOfMeasure = "oz"
	UnitMillilitre UnitOfMeasure = "ml"
	UnitLitre      UnitOfMeasure = "l"
	UnitMetre      UnitOfMeasure = "m"
	UnitCentimetre UnitOfMeasure = "cm"
	UnitFoot       UnitOfMeasure = "ft"
	UnitHour       UnitOfMeasure = "hour"
)

// DefaultUnit is what a product uses when nothing is chosen.
const DefaultUnit = UnitEach

// Units lists every unit the UI offers, in the order it shows them.
var Units = []UnitOfMeasure{
	UnitEach, UnitBox, UnitCase, UnitPallet, UnitPack, UnitPair, UnitSet,
	UnitGram, UnitKilogram, UnitPound, UnitOunce,
	UnitMillilitre, UnitLitre,
	UnitMetre, UnitCentimetre, UnitFoot,
	UnitHour,
}

var unitLabels = map[UnitOfMeasure]string{
	UnitEach: "Each", UnitBox: "Box", UnitCase: "Case", UnitPallet: "Pallet",
	UnitPack: "Pack", UnitPair: "Pair", UnitSet: "Set",
	UnitGram: "Gram (g)", UnitKilogram: "Kilogram (kg)",
	UnitPound: "Pound (lb)", UnitOunce: "Ounce (oz)",
	UnitMillilitre: "Millilitre (ml)", UnitLitre: "Litre (L)",
	UnitMetre: "Metre (m)", UnitCentimetre: "Centimetre (cm)", UnitFoot: "Foot (ft)",
	UnitHour: "Hour",
}

// Label is the human-readable name shown in a picker.
func (u UnitOfMeasure) Label() string {
	if label, ok := unitLabels[u.Normalize()]; ok {
		return label
	}
	if u == "" {
		return unitLabels[DefaultUnit]
	}
	return string(u)
}

// Normalize lower-cases and trims a unit so "Each", "each" and " EACH " are
// one unit rather than three.
func (u UnitOfMeasure) Normalize() UnitOfMeasure {
	n := UnitOfMeasure(strings.ToLower(strings.TrimSpace(string(u))))
	if n == "" {
		return DefaultUnit
	}
	return n
}

// Valid reports whether the unit is one this build knows about. Unknown units
// are allowed through — a business with its own unit should not be blocked —
// but the UI only offers the known ones.
func (u UnitOfMeasure) Valid() bool {
	_, ok := unitLabels[u.Normalize()]
	return ok
}

// ParseUnit resolves user input to a unit, accepting either the code or the
// label so that a CSV saying "Kilogram (kg)" imports the same as one saying
// "kg".
func ParseUnit(s string) UnitOfMeasure {
	trimmed := strings.ToLower(strings.TrimSpace(s))
	if trimmed == "" {
		return DefaultUnit
	}
	for unit, label := range unitLabels {
		if trimmed == string(unit) || trimmed == strings.ToLower(label) {
			return unit
		}
	}
	// Also accept the bare word from a label such as "kilogram".
	for unit, label := range unitLabels {
		if name, _, ok := strings.Cut(strings.ToLower(label), " ("); ok && trimmed == name {
			return unit
		}
	}
	return UnitOfMeasure(trimmed)
}
