package core_test

import (
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// ledger builds a movement sequence for the valuation tests. Costs are given
// as strings so the fixtures read the way an invoice does.
func ledger(entries ...[2]any) []core.StockMovement {
	out := make([]core.StockMovement, 0, len(entries))
	for _, e := range entries {
		qty := int64(e[0].(int))
		out = append(out, core.StockMovement{
			QtyDelta: qty,
			UnitCost: core.MustParseMoney(e[1].(string), "USD"),
		})
	}
	return out
}

// TestValueLedgerFIFO walks the textbook example: three receipts at rising
// prices, then an issue that eats the first layer and part of the second.
func TestValueLedgerFIFO(t *testing.T) {
	movements := ledger(
		[2]any{100, "1.00"},
		[2]any{100, "2.00"},
		[2]any{-150, "0.00"},
	)

	onHand, value, cogs := core.ValueLedger(movements, core.ValuationFIFO, "USD")

	if onHand != 50 {
		t.Errorf("OnHand = %d, want 50", onHand)
	}
	// The 150 issued take all 100 at $1 and 50 at $2.
	if cogs.String() != "200.00" {
		t.Errorf("COGS = %s, want 200.00", cogs)
	}
	// What remains is the newest stock: 50 at $2.
	if value.String() != "100.00" {
		t.Errorf("Value = %s, want 100.00", value)
	}
}

// TestValueLedgerWeightedAverage runs the same ledger under the other policy.
// The two must disagree — that difference is the whole reason the choice is an
// accounting policy rather than a display preference.
func TestValueLedgerWeightedAverage(t *testing.T) {
	movements := ledger(
		[2]any{100, "1.00"},
		[2]any{100, "2.00"},
		[2]any{-150, "0.00"},
	)

	onHand, value, cogs := core.ValueLedger(movements, core.ValuationWeightedAverage, "USD")

	if onHand != 50 {
		t.Errorf("OnHand = %d, want 50", onHand)
	}
	// 200 units cost $300, so the average is $1.50. 150 issued cost $225.
	if cogs.String() != "225.00" {
		t.Errorf("COGS = %s, want 225.00", cogs)
	}
	if value.String() != "75.00" {
		t.Errorf("Value = %s, want 75.00", value)
	}

	fifoValue := core.Zero("USD")
	_, fifoValue, _ = core.ValueLedger(movements, core.ValuationFIFO, "USD")
	if fifoValue.Minor == value.Minor {
		t.Error("FIFO and weighted average produced the same value; one of them is not being applied")
	}
}

func TestValueLedgerEmpty(t *testing.T) {
	for _, method := range core.ValuationMethods {
		onHand, value, cogs := core.ValueLedger(nil, method, "USD")
		if onHand != 0 || !value.IsZero() || !cogs.IsZero() {
			t.Errorf("%s on an empty ledger = %d/%s/%s, want zeros", method, onHand, value, cogs)
		}
	}
}

// TestValueLedgerFullyIssued checks the boundary that most often produces a
// stray fraction: everything received is issued back out.
func TestValueLedgerFullyIssued(t *testing.T) {
	movements := ledger(
		[2]any{30, "3.33"},
		[2]any{-30, "0.00"},
	)

	for _, method := range core.ValuationMethods {
		onHand, value, cogs := core.ValueLedger(movements, method, "USD")
		if onHand != 0 {
			t.Errorf("%s OnHand = %d, want 0", method, onHand)
		}
		if !value.IsZero() {
			t.Errorf("%s left %s of value behind after everything was issued", method, value)
		}
		if cogs.String() != "99.90" {
			t.Errorf("%s COGS = %s, want 99.90", method, cogs)
		}
	}
}

// TestValueLedgerNegativeStock covers the case an install with negative stock
// will hit. It must still produce a number rather than a nonsense one.
func TestValueLedgerNegativeStock(t *testing.T) {
	movements := ledger(
		[2]any{10, "5.00"},
		[2]any{-15, "0.00"},
	)

	for _, method := range core.ValuationMethods {
		onHand, value, cogs := core.ValueLedger(movements, method, "USD")
		if onHand != -5 {
			t.Errorf("%s OnHand = %d, want -5", method, onHand)
		}
		// Stock you do not have is worth nothing; it is not worth a negative
		// amount, which would quietly reduce the total valuation.
		if !value.IsZero() {
			t.Errorf("%s valued negative stock at %s, want zero", method, value)
		}
		if cogs.IsZero() {
			t.Errorf("%s reported no cost of goods for 15 units issued", method)
		}
	}
}

// TestValueLedgerAveragePrecision checks that repeatedly issuing at an average
// that does not divide evenly does not drift.
func TestValueLedgerAveragePrecision(t *testing.T) {
	movements := []core.StockMovement{
		{QtyDelta: 3, UnitCost: core.MustParseMoney("1.00", "USD")},
		{QtyDelta: -1},
		{QtyDelta: -1},
		{QtyDelta: -1},
	}

	onHand, value, cogs := core.ValueLedger(movements, core.ValuationWeightedAverage, "USD")
	if onHand != 0 {
		t.Errorf("OnHand = %d, want 0", onHand)
	}
	if !value.IsZero() {
		t.Errorf("Value = %s, want zero once everything is issued", value)
	}
	if cogs.String() != "3.00" {
		t.Errorf("COGS = %s, want 3.00", cogs)
	}
}

func TestClassifyABC(t *testing.T) {
	lines := []core.ProductValuation{
		{SKU: "SMALL", Value: core.MustParseMoney("10.00", "USD")},
		{SKU: "BIG", Value: core.MustParseMoney("800.00", "USD")},
		{SKU: "MEDIUM", Value: core.MustParseMoney("150.00", "USD")},
		{SKU: "TINY", Value: core.MustParseMoney("40.00", "USD")},
	}

	got := core.ClassifyABC(lines)
	if len(got) != 4 {
		t.Fatalf("ClassifyABC() returned %d lines, want 4", len(got))
	}

	// Highest value first, so the ranking reads as a priority list.
	if got[0].SKU != "BIG" {
		t.Errorf("first line = %q, want BIG", got[0].SKU)
	}
	if got[0].Class != core.ClassA {
		t.Errorf("BIG is class %q, want A", got[0].Class)
	}
	if got[len(got)-1].Class != core.ClassC {
		t.Errorf("last line is class %q, want C", got[len(got)-1].Class)
	}

	// Shares must total 100 and rise monotonically.
	last := 0.0
	for _, line := range got {
		if line.CumulativeShare < last {
			t.Errorf("cumulative share fell from %.1f to %.1f", last, line.CumulativeShare)
		}
		last = line.CumulativeShare
	}
	if last < 99.9 || last > 100.1 {
		t.Errorf("cumulative share ends at %.2f%%, want 100%%", last)
	}
}

// TestClassifyABCAllZero guards the division by zero that an empty or
// zero-valued catalogue would otherwise cause.
func TestClassifyABCAllZero(t *testing.T) {
	lines := []core.ProductValuation{
		{SKU: "A", Value: core.Zero("USD")},
		{SKU: "B", Value: core.Zero("USD")},
	}

	got := core.ClassifyABC(lines)
	if len(got) != 2 {
		t.Fatalf("ClassifyABC() returned %d lines, want 2", len(got))
	}
	for _, line := range got {
		if line.ShareOfValue != 0 || line.CumulativeShare != 0 {
			t.Errorf("%s reported a share of a zero total", line.SKU)
		}
	}

	if len(core.ClassifyABC(nil)) != 0 {
		t.Error("ClassifyABC(nil) returned lines")
	}
}

func TestBucketForAge(t *testing.T) {
	tests := []struct {
		days int
		want core.AgingBucket
	}{
		{0, core.AgingFresh},
		{29, core.AgingFresh},
		{30, core.Aging30to90},
		{89, core.Aging30to90},
		{90, core.Aging90to180},
		{179, core.Aging90to180},
		{180, core.Aging180to365},
		{364, core.Aging180to365},
		{365, core.AgingDead},
		{5000, core.AgingDead},
	}

	for _, tc := range tests {
		if got := core.BucketForAge(tc.days); got != tc.want {
			t.Errorf("BucketForAge(%d) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

func TestValuationMethodValid(t *testing.T) {
	for _, method := range core.ValuationMethods {
		if !method.Valid() {
			t.Errorf("%q is listed but reports itself invalid", method)
		}
		if method.Label() == string(method) {
			t.Errorf("%q has no human-readable label", method)
		}
	}
	if core.ValuationMethod("lifo").Valid() {
		t.Error("LIFO reports as valid but is not implemented")
	}
}
