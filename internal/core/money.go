package core

import (
	"fmt"
	"strconv"
	"strings"
)

// Currency is an ISO 4217 alphabetic code.
type Currency string

// DefaultCurrency is used when an install has not chosen one.
const DefaultCurrency Currency = "USD"

// exponents holds currencies whose minor unit is not two digits. Anything not
// listed here uses 2, which covers the overwhelming majority of ISO 4217.
var exponents = map[Currency]int{
	"BIF": 0, "CLP": 0, "DJF": 0, "GNF": 0, "ISK": 0, "JPY": 0, "KMF": 0,
	"KRW": 0, "PYG": 0, "RWF": 0, "UGX": 0, "UYI": 0, "VND": 0, "VUV": 0,
	"XAF": 0, "XOF": 0, "XPF": 0,
	"BHD": 3, "IQD": 3, "JOD": 3, "KWD": 3, "LYD": 3, "OMR": 3, "TND": 3,
}

var symbols = map[Currency]string{
	"USD": "$", "CAD": "$", "AUD": "$", "NZD": "$",
	"EUR": "€", "GBP": "£", "JPY": "¥", "CNY": "¥",
	"INR": "₹", "KRW": "₩", "BRL": "R$", "MXN": "$", "CHF": "CHF",
}

// Exponent returns the number of digits in the currency's minor unit.
func (c Currency) Exponent() int {
	if e, ok := exponents[c.Normalize()]; ok {
		return e
	}
	return 2
}

// Normalize upper-cases the code and trims stray whitespace.
func (c Currency) Normalize() Currency {
	return Currency(strings.ToUpper(strings.TrimSpace(string(c))))
}

// Valid reports whether the code looks like an ISO 4217 alphabetic code.
func (c Currency) Valid() bool {
	s := string(c.Normalize())
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// Money is an exact monetary amount held as a whole number of minor units
// (cents for USD, yen for JPY). Float64 is never used for arithmetic: at 8000
// products a fraction of a cent per row compounds into a valuation report that
// does not reconcile.
type Money struct {
	Minor    int64
	Currency Currency
}

// NewMoney builds an amount from a count of minor units.
func NewMoney(minor int64, c Currency) Money {
	if c == "" {
		c = DefaultCurrency
	}
	return Money{Minor: minor, Currency: c.Normalize()}
}

// Zero returns a zero amount in the given currency.
func Zero(c Currency) Money { return NewMoney(0, c) }

// ParseMoney reads a human-entered amount such as "12.50", "$1,234.56" or
// "-3" into exact minor units.
//
// It rejects, rather than rounds, an amount with more decimal places than the
// currency supports: silently turning an imported "10.999" into "11.00" is the
// kind of change nobody notices until the books disagree.
//
// A comma is always a thousands separator and a full stop is always the
// decimal point. Continental notation such as "1.234,56" is therefore not
// understood, and that is deliberate: the two conventions are ambiguous for
// short amounts, and guessing wrong moves the decimal point by two places. A
// locale setting is the right way to support it, not a heuristic.
func ParseMoney(s string, c Currency) (Money, error) {
	if c == "" {
		c = DefaultCurrency
	}
	c = c.Normalize()
	exp := c.Exponent()

	// Only decoration is dropped — currency symbols, spaces and thousands
	// separators. Letters are not decoration: stripping them would turn
	// "not-a-price" into a sign and two empty parts, and quietly import it as
	// zero. Anything left that is not a number is an error, not a hint.
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r == ',', r == ' ', r == '\u00a0', r == '_', r == '\'':
			return -1
		case r == '$', r == '€', r == '£', r == '¥', r == '₹', r == '₩':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(s))

	if cleaned == "" {
		return Money{}, fmt.Errorf("%w: %q is not an amount", ErrInvalid, s)
	}

	neg := false
	switch cleaned[0] {
	case '-':
		neg, cleaned = true, cleaned[1:]
	case '+':
		cleaned = cleaned[1:]
	}

	whole, frac, hasFrac := strings.Cut(cleaned, ".")
	if !isDigits(whole) || !isDigits(frac) {
		return Money{}, fmt.Errorf("%w: %q is not an amount", ErrInvalid, s)
	}
	if whole == "" && frac == "" {
		return Money{}, fmt.Errorf("%w: %q is not an amount", ErrInvalid, s)
	}
	if whole == "" {
		whole = "0"
	}
	if hasFrac && len(frac) > exp {
		return Money{}, fmt.Errorf("%w: %s allows at most %d decimal place(s), got %q",
			ErrInvalid, c, exp, s)
	}

	digits := whole + frac + strings.Repeat("0", exp-len(frac))
	minor, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q is out of range", ErrInvalid, s)
	}
	if neg {
		minor = -minor
	}
	return Money{Minor: minor, Currency: c}, nil
}

// isDigits reports whether s is empty or made only of decimal digits, which is
// what remains once a valid amount has had its sign and decimal point removed.
func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// MustParseMoney is ParseMoney for constants and tests.
func MustParseMoney(s string, c Currency) Money {
	m, err := ParseMoney(s, c)
	if err != nil {
		panic(err)
	}
	return m
}

// Add returns m+n. Currencies must match; mixing them is a bug, not a
// conversion request.
func (m Money) Add(n Money) (Money, error) {
	if err := m.sameCurrency(n); err != nil {
		return Money{}, err
	}
	return Money{Minor: m.Minor + n.Minor, Currency: m.Currency}, nil
}

// Sub returns m-n.
func (m Money) Sub(n Money) (Money, error) {
	if err := m.sameCurrency(n); err != nil {
		return Money{}, err
	}
	return Money{Minor: m.Minor - n.Minor, Currency: m.Currency}, nil
}

// MulQty scales an amount by a whole quantity, as in a line total.
func (m Money) MulQty(qty int64) Money {
	return Money{Minor: m.Minor * qty, Currency: m.Currency}
}

// Neg returns the amount with its sign flipped.
func (m Money) Neg() Money { return Money{Minor: -m.Minor, Currency: m.Currency} }

// IsZero reports whether the amount is exactly zero.
func (m Money) IsZero() bool { return m.Minor == 0 }

// IsNegative reports whether the amount is below zero.
func (m Money) IsNegative() bool { return m.Minor < 0 }

// Compare returns -1, 0 or 1. Amounts in different currencies are ordered by
// code so that sorting a mixed column stays deterministic.
func (m Money) Compare(n Money) int {
	if m.Currency != n.Currency {
		return strings.Compare(string(m.Currency), string(n.Currency))
	}
	switch {
	case m.Minor < n.Minor:
		return -1
	case m.Minor > n.Minor:
		return 1
	}
	return 0
}

// String renders the bare number, e.g. "1234.56". Use it for CSV export and
// anywhere the value will be re-parsed.
func (m Money) String() string {
	exp := m.Currency.Exponent()
	minor := m.Minor
	sign := ""
	if minor < 0 {
		sign = "-"
		minor = -minor
	}
	if exp == 0 {
		return sign + strconv.FormatInt(minor, 10)
	}
	unit := int64(1)
	for range exp {
		unit *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", sign, minor/unit, exp, minor%unit)
}

// Display renders the amount for a human, with a symbol and thousands
// separators, e.g. "$1,234.56".
func (m Money) Display() string {
	num := m.String()
	sign := ""
	if strings.HasPrefix(num, "-") {
		sign, num = "-", num[1:]
	}
	whole, frac, hasFrac := strings.Cut(num, ".")
	whole = group(whole)
	if hasFrac {
		whole += "." + frac
	}
	if sym, ok := symbols[m.Currency]; ok {
		return sign + sym + whole
	}
	return fmt.Sprintf("%s%s %s", sign, m.Currency, whole)
}

// Float converts to float64 for display and export only. Never feed the result
// back into arithmetic.
func (m Money) Float() float64 {
	exp := m.Currency.Exponent()
	div := 1.0
	for range exp {
		div *= 10
	}
	return float64(m.Minor) / div
}

func (m Money) sameCurrency(n Money) error {
	if m.Currency != n.Currency {
		return fmt.Errorf("%w: cannot combine %s and %s", ErrInvalid, m.Currency, n.Currency)
	}
	return nil
}

// group inserts thousands separators into a run of digits.
func group(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	lead := len(s) % 3
	if lead > 0 {
		b.WriteString(s[:lead])
	}
	for i := lead; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}
