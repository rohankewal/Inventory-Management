package core_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

func TestProductValidate(t *testing.T) {
	valid := core.Product{SKU: "A-1", Name: "Anvil", Price: core.MustParseMoney("10.00", "USD")}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() on a good product error = %v", err)
	}

	tests := []struct {
		name    string
		product core.Product
		field   string
	}{
		{"missing sku", core.Product{Name: "X", Price: core.Zero("USD")}, "sku"},
		{"missing name", core.Product{SKU: "A-1", Price: core.Zero("USD")}, "name"},
		{"negative price", core.Product{SKU: "A-1", Name: "X", Price: core.MustParseMoney("-1.00", "USD")}, "price"},
		{"bad currency", core.Product{SKU: "A-1", Name: "X", Price: core.NewMoney(1, "XX")}, "price"},
		{"negative cost", core.Product{SKU: "A-1", Name: "X", Price: core.Zero("USD"), Cost: core.MustParseMoney("-1.00", "USD")}, "cost"},
		{"mismatched cost currency", core.Product{SKU: "A-1", Name: "X", Price: core.Zero("USD"), Cost: core.NewMoney(1, "EUR")}, "cost"},
		{"reorder quantity with no point", core.Product{SKU: "A-1", Name: "X", Price: core.Zero("USD"), ReorderQuantity: 10}, "reorder_point"},
		{"long sku", core.Product{SKU: strings.Repeat("x", 65), Name: "X", Price: core.Zero("USD")}, "sku"},
		{"long name", core.Product{SKU: "A-1", Name: strings.Repeat("x", 201), Price: core.Zero("USD")}, "name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.product.Validate()
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("Validate() error = %v, want core.ErrInvalid", err)
			}

			var ve *core.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("Validate() error is not a *core.ValidationError")
			}
			if !hasField(ve, tc.field) {
				t.Errorf("Validate() reported %v, want a problem on %q", ve.Fields, tc.field)
			}
		})
	}
}

// TestProductValidateReportsEveryProblem matters for form UX: fixing one field
// at a time across six round trips is what makes data entry feel hostile.
func TestProductValidateReportsEveryProblem(t *testing.T) {
	p := core.Product{Price: core.MustParseMoney("-1.00", "USD")}

	var ve *core.ValidationError
	if !errors.As(p.Validate(), &ve) {
		t.Fatal("Validate() did not return a *core.ValidationError")
	}
	if len(ve.Fields) < 3 {
		t.Errorf("Validate() reported %d problems, want sku, name and price together", len(ve.Fields))
	}
}

func TestProductNormalize(t *testing.T) {
	p := core.Product{SKU: "  A-1  ", Name: "  Anvil ", Description: " heavy "}
	p.Normalize()

	if p.SKU != "A-1" || p.Name != "Anvil" || p.Description != "heavy" {
		t.Errorf("Normalize() = %q/%q/%q, want trimmed values", p.SKU, p.Name, p.Description)
	}
	if p.Price.Currency != core.DefaultCurrency {
		t.Errorf("Normalize() left currency %q, want the default %q", p.Price.Currency, core.DefaultCurrency)
	}
}

func TestMovementValidate(t *testing.T) {
	good := core.StockMovement{
		ProductID: core.NewID(), LocationID: core.DefaultLocationID,
		QtyDelta: 5, Reason: core.ReasonReceipt,
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("Validate() on a good movement error = %v", err)
	}

	tests := []struct {
		name     string
		movement core.StockMovement
		field    string
	}{
		{"zero delta", core.StockMovement{ProductID: core.NewID(), LocationID: core.DefaultLocationID, Reason: core.ReasonReceipt}, "quantity"},
		{"no product", core.StockMovement{LocationID: core.DefaultLocationID, QtyDelta: 1, Reason: core.ReasonReceipt}, "product_id"},
		{"no location", core.StockMovement{ProductID: core.NewID(), QtyDelta: 1, Reason: core.ReasonReceipt}, "location_id"},
		{"unknown reason", core.StockMovement{ProductID: core.NewID(), LocationID: core.DefaultLocationID, QtyDelta: 1, Reason: "made_up"}, "reason"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ve *core.ValidationError
			if !errors.As(tc.movement.Validate(), &ve) {
				t.Fatalf("Validate() did not return a *core.ValidationError")
			}
			if !hasField(ve, tc.field) {
				t.Errorf("Validate() reported %v, want a problem on %q", ve.Fields, tc.field)
			}
		})
	}
}

func TestIDRoundTrip(t *testing.T) {
	id := core.NewID()
	if id.IsZero() {
		t.Fatal("NewID() returned the zero value")
	}

	parsed, err := core.ParseID(id.String())
	if err != nil {
		t.Fatalf("ParseID(%q) error = %v", id, err)
	}
	if parsed != id {
		t.Errorf("ParseID(%q) = %q, want the same id", id, parsed)
	}

	if _, err := core.ParseID("not-a-uuid"); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("ParseID(garbage) error = %v, want core.ErrInvalid", err)
	}
}

// TestNewIDIsTimeOrdered guards the UUIDv7 choice: IDs generated in sequence
// must sort in that order, which is what keeps primary-key inserts append-only
// in the index rather than scattered across it.
func TestNewIDIsTimeOrdered(t *testing.T) {
	const n = 50
	ids := make([]core.ID, n)
	for i := range ids {
		ids[i] = core.NewID()
	}
	for i := 1; i < n; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("id %d (%q) does not sort after id %d (%q)", i, ids[i], i-1, ids[i-1])
		}
	}
}

func hasField(ve *core.ValidationError, field string) bool {
	for _, f := range ve.Fields {
		if f.Field == field {
			return true
		}
	}
	return false
}
