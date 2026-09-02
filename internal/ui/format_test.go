package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

func TestParseQuantity(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"", 0},
		{"0", 0},
		{"42", 42},
		{" 7 ", 7},
		{"1,200", 1200},
		{"-3", -3},
	}

	for _, tc := range tests {
		got, err := parseQuantity(tc.input)
		if err != nil {
			t.Errorf("parseQuantity(%q) error = %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseQuantity(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}

	for _, input := range []string{"abc", "1.5", "12x"} {
		if _, err := parseQuantity(input); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("parseQuantity(%q) error = %v, want core.ErrInvalid", input, err)
		}
	}
}

func TestDescribeCount(t *testing.T) {
	tests := []struct {
		shown, total int
		want         string
	}{
		{0, 0, "0 products"},
		{1, 1, "1 product"},
		{12, 12, "12 products"},
		{50, 812, "showing 50 of 812"},
	}

	for _, tc := range tests {
		if got := describeCount(tc.shown, tc.total); got != tc.want {
			t.Errorf("describeCount(%d, %d) = %q, want %q", tc.shown, tc.total, got, tc.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate() shortened a string that fits: %q", got)
	}
	if got := truncate("a much longer product name", 10); got != "a much lo…" {
		t.Errorf("truncate() = %q, want %q", got, "a much lo…")
	}
	// Multi-byte characters must be cut on rune boundaries, not bytes.
	if got := truncate("日本語のとても長い商品名", 5); len([]rune(got)) != 5 {
		t.Errorf("truncate() produced %d runes, want 5", len([]rune(got)))
	}
}

// TestHumanErrorHidesInternals is the point of the mapping: an operator should
// never be shown SQL, and should always be told something they can act on.
func TestHumanErrorHidesInternals(t *testing.T) {
	raw := errors.New(`sqlite: SQL logic error: near "SELCT": syntax error (1)`)

	got := humanError(raw)
	if strings.Contains(got, "SELCT") || strings.Contains(got, "sqlite") {
		t.Errorf("humanError() leaked internals: %q", got)
	}
	if !strings.Contains(got, "log file") {
		t.Errorf("humanError() = %q, want it to point at the log file", got)
	}
}

func TestHumanErrorListsEveryValidationProblem(t *testing.T) {
	var ve core.ValidationError
	ve.Add("sku", "SKU is required")
	ve.Add("name", "Name is required")

	got := humanError(ve.ErrOrNil())
	for _, want := range []string{"SKU is required", "Name is required"} {
		if !strings.Contains(got, want) {
			t.Errorf("humanError() = %q, want it to mention %q", got, want)
		}
	}
}

func TestHumanErrorForSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"not found", core.ErrNotFound, "no longer exists"},
		{"permission", core.ErrPermission, "permission"},
		{"timeout", context.DeadlineExceeded, "did not respond in time"},
		{"cancelled", context.Canceled, "cancelled"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanError(tc.err); !strings.Contains(strings.ToLower(got), tc.want) {
				t.Errorf("humanError() = %q, want it to mention %q", got, tc.want)
			}
		})
	}

	if humanError(nil) != "" {
		t.Error("humanError(nil) returned a message")
	}
}

// TestHumanErrorKeepsTheSpecificReason checks that the useful part of a
// wrapped error survives: "only 10 available" is actionable, "conflict" is not.
func TestHumanErrorKeepsTheSpecificReason(t *testing.T) {
	err := wrapf(
		fmt.Errorf("%w: removing 15 would leave -5 on hand; only 10 is available", core.ErrInvalid),
		"the adjustment failed")

	got := humanError(err)
	if !strings.Contains(got, "only 10 is available") {
		t.Errorf("humanError() = %q, want the specific reason preserved", got)
	}
}

// TestPluralizeReadsCorrectlyInSentences guards the phrasing bug where a
// pluralised subject and a following verb both carry the verb — "3 products
// are are short".
func TestPluralizeReadsCorrectlyInSentences(t *testing.T) {
	tests := []struct {
		n        int
		singular string
		plural   string
		want     string
	}{
		{1, "product is", "products are", "1 product is"},
		{3, "product is", "products are", "3 products are"},
		{0, "order", "orders", "0 orders"},
		{1, "order", "orders", "1 order"},
		{2500, "order", "orders", "2,500 orders"},
	}

	for _, tc := range tests {
		if got := pluralize(tc.n, tc.singular, tc.plural); got != tc.want {
			t.Errorf("pluralize(%d, %q, %q) = %q, want %q",
				tc.n, tc.singular, tc.plural, got, tc.want)
		}
	}

	// The subject already carries its verb, so the sentence must not add one.
	sentence := sprintf("%s short of what has been promised.",
		pluralize(3, "product is", "products are"))
	if strings.Contains(sentence, "are are") || strings.Contains(sentence, "is is") {
		t.Errorf("sentence reads %q", sentence)
	}
}
