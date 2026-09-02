package core_test

import (
	"errors"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

func TestParseMoney(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		currency core.Currency
		want     int64
	}{
		{"whole number", "12", "USD", 1200},
		{"two decimals", "12.50", "USD", 1250},
		{"one decimal", "12.5", "USD", 1250},
		{"single cent", "0.01", "USD", 1},
		{"zero", "0", "USD", 0},
		{"leading dot", ".99", "USD", 99},
		{"negative", "-4.25", "USD", -425},
		{"explicit plus", "+4.25", "USD", 425},
		{"currency symbol", "$1,234.56", "USD", 123456},
		{"spaces", " 19.99 ", "USD", 1999},
		{"zero-decimal currency", "1500", "JPY", 1500},
		{"three-decimal currency", "1.234", "KWD", 1234},
		{"large value", "99999999.99", "USD", 9999999999},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := core.ParseMoney(tc.input, tc.currency)
			if err != nil {
				t.Fatalf("ParseMoney(%q, %s) error = %v", tc.input, tc.currency, err)
			}
			if got.Minor != tc.want {
				t.Errorf("ParseMoney(%q, %s).Minor = %d, want %d",
					tc.input, tc.currency, got.Minor, tc.want)
			}
		})
	}
}

func TestParseMoneyRejectsExcessPrecision(t *testing.T) {
	// Rounding "10.999" to "11.00" behind the user's back is how an imported
	// price list quietly stops matching the supplier's invoice.
	tests := []struct {
		input    string
		currency core.Currency
	}{
		{"10.999", "USD"},
		{"1.5", "JPY"},
		{"0.0001", "USD"},
	}

	for _, tc := range tests {
		if _, err := core.ParseMoney(tc.input, tc.currency); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseMoney(%q, %s) error = %v, want core.ErrInvalid",
				tc.input, tc.currency, err)
		}
	}
}

func TestParseMoneyRejectsGarbage(t *testing.T) {
	for _, input := range []string{"", "abc", "-", "1.2.3", "$", "   "} {
		if _, err := core.ParseMoney(input, "USD"); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseMoney(%q) error = %v, want core.ErrInvalid", input, err)
		}
	}
}

func TestMoneyStringRoundTrips(t *testing.T) {
	for _, input := range []string{"0.00", "0.01", "9.99", "1234567.89", "-4.25"} {
		m := core.MustParseMoney(input, "USD")
		if got := m.String(); got != input {
			t.Errorf("MustParseMoney(%q).String() = %q, want %q", input, got, input)
		}
	}
}

func TestMoneyDisplay(t *testing.T) {
	tests := []struct {
		input    string
		currency core.Currency
		want     string
	}{
		{"1234.56", "USD", "$1,234.56"},
		{"1234567.89", "USD", "$1,234,567.89"},
		{"999.99", "USD", "$999.99"},
		{"-42.00", "USD", "-$42.00"},
		{"1500", "JPY", "¥1,500"},
		{"10.00", "SEK", "SEK 10.00"},
	}

	for _, tc := range tests {
		got := core.MustParseMoney(tc.input, tc.currency).Display()
		if got != tc.want {
			t.Errorf("Display(%q %s) = %q, want %q", tc.input, tc.currency, got, tc.want)
		}
	}
}

// TestMoneyArithmeticIsExact is the reason Money exists. The float version of
// this sum is 0.30000000000000004.
func TestMoneyArithmeticIsExact(t *testing.T) {
	a := core.MustParseMoney("0.10", "USD")
	b := core.MustParseMoney("0.20", "USD")

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if sum.String() != "0.30" {
		t.Errorf("0.10 + 0.20 = %s, want 0.30", sum)
	}

	// A thousand cents must be exactly ten dollars, not 9.999999.
	total := core.Zero("USD")
	for range 1000 {
		total, err = total.Add(core.MustParseMoney("0.01", "USD"))
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if total.String() != "10.00" {
		t.Errorf("1000 x 0.01 = %s, want 10.00", total)
	}
}

func TestMoneyMulQty(t *testing.T) {
	line := core.MustParseMoney("19.99", "USD").MulQty(3)
	if line.String() != "59.97" {
		t.Errorf("19.99 x 3 = %s, want 59.97", line)
	}
}

func TestMoneyRejectsMixedCurrency(t *testing.T) {
	usd := core.MustParseMoney("1.00", "USD")
	eur := core.MustParseMoney("1.00", "EUR")

	if _, err := usd.Add(eur); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Add() across currencies error = %v, want core.ErrInvalid", err)
	}
	if _, err := usd.Sub(eur); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Sub() across currencies error = %v, want core.ErrInvalid", err)
	}
}

func TestCurrencyValid(t *testing.T) {
	valid := []core.Currency{"USD", "eur", " GBP "}
	for _, c := range valid {
		if !c.Valid() {
			t.Errorf("Currency(%q).Valid() = false, want true", c)
		}
	}
	invalid := []core.Currency{"", "US", "USDD", "US1"}
	for _, c := range invalid {
		if c.Valid() {
			t.Errorf("Currency(%q).Valid() = true, want false", c)
		}
	}
}

// TestParseMoneyRejectsTextThatContainsDigits is the regression that matters
// most for CSV import: a cell reading "not-a-price" or "12 units" must fail,
// not quietly become an amount by having its letters discarded.
func TestParseMoneyRejectsTextThatContainsDigits(t *testing.T) {
	for _, input := range []string{
		"not-a-price",
		"n/a",
		"12 units",
		"USD 12",
		"1.2.3",
		"--5",
		"5-",
		"1e5",
		"free",
		"TBC",
		"#REF!",
	} {
		if got, err := core.ParseMoney(input, "USD"); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("ParseMoney(%q) = %s with error %v, want core.ErrInvalid", input, got, err)
		}
	}
}

// TestParseMoneyTreatsCommaAsGrouping pins the documented rule. Continental
// notation is not supported, and the important part is that it is not silently
// misread: "1.234,56" must fail rather than become 1.23.
func TestParseMoneyTreatsCommaAsGrouping(t *testing.T) {
	got, err := core.ParseMoney("1,234", "USD")
	if err != nil {
		t.Fatalf("ParseMoney(%q) error = %v", "1,234", err)
	}
	if got.Minor != 123400 {
		t.Errorf("ParseMoney(%q).Minor = %d, want 123400", "1,234", got.Minor)
	}

	if _, err := core.ParseMoney("1.234,56", "USD"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("ParseMoney(%q) error = %v, want it rejected rather than misread", "1.234,56", err)
	}
}

// TestParseMoneyKeepsAcceptingRealDecoration checks the strictness above did
// not break the formatting people actually paste in.
func TestParseMoneyKeepsAcceptingRealDecoration(t *testing.T) {
	tests := map[string]int64{
		"$1,234.56": 123456,
		"£99.99":    9999,
		"1'234.50":  123450, // Swiss grouping
		"  42  ":    4200,
		"+7.50":     750,
	}

	for input, want := range tests {
		got, err := core.ParseMoney(input, "USD")
		if err != nil {
			t.Errorf("ParseMoney(%q) error = %v", input, err)
			continue
		}
		if got.Minor != want {
			t.Errorf("ParseMoney(%q).Minor = %d, want %d", input, got.Minor, want)
		}
	}
}
