// Package storetest is the conformance suite every storage backend must pass.
//
// It exists so that "SQLite for one user, Postgres for a team" is a verified
// claim rather than an intention. A behaviour that differs between backends —
// case sensitivity of a SKU, whether a stale write is rejected, whether the
// cached level matches the ledger — fails here instead of in a customer's
// month-end reconciliation.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// Factory opens a fresh, empty, migrated store for one test.
type Factory func(t *testing.T) storage.Store

// Run executes the full suite against a backend.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	tests := []namedTest{
		{"Locations/DefaultIsSeeded", testDefaultLocation},
		{"Locations/CreateAndList", testLocationCreateAndList},
		{"Locations/DuplicateCodeConflicts", testLocationDuplicateCode},
		{"Products/CreateAndGet", testProductCreateAndGet},
		{"Products/DuplicateSKUConflicts", testProductDuplicateSKU},
		{"Products/SKULookupIsCaseInsensitive", testProductSKUCaseInsensitive},
		{"Products/GetMissingIsNotFound", testProductGetMissing},
		{"Products/UpdateBumpsVersion", testProductUpdate},
		{"Products/StaleUpdateConflicts", testProductStaleUpdate},
		{"Products/DeleteAndArchive", testProductDeleteAndArchive},
		{"Products/ListFiltersAndSorts", testProductList},
		{"Products/ListPagesWithStableTotal", testProductPaging},
		{"Products/PriceRoundTripsExactly", testProductPriceExact},
		{"Products/BarcodeLookup", testProductBarcode},
		{"Products/TagsRoundTripAndFilter", testProductTags},
		{"Products/CustomFieldsRoundTrip", testProductCustomFields},
		{"Movements/AppendUpdatesLevel", testMovementAppend},
		{"Movements/LedgerIsSourceOfTruth", testMovementRecompute},
		{"Movements/FilterAndCount", testMovementFilter},
		{"Movements/RejectsZeroDelta", testMovementZeroDelta},
		{"Movements/LevelsPerLocation", testMovementLevels},
		{"Movements/CostAndLotRoundTrip", testMovementCostAndLot},
		{"Movements/CostHistoryIsOrdered", testMovementCostHistory},
		{"Movements/LastMovedAt", testMovementLastMovedAt},
		{"Settings/RoundTrip", testSettings},
		{"Tx/RollsBackOnError", testTxRollback},
		{"Tx/NestedJoinsOuter", testTxNested},
	}

	tests = append(tests, orderTests...)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			tc.fn(t, store)
		})
	}
}

// namedTest is one conformance case.
type namedTest struct {
	name string
	fn   func(*testing.T, storage.Store)
}

// --- locations --------------------------------------------------------------

func testDefaultLocation(t *testing.T, s storage.Store) {
	ctx := context.Background()

	loc, err := s.Locations().Default(ctx)
	if err != nil {
		t.Fatalf("Default() error = %v, want a seeded default location", err)
	}
	if loc.ID != core.DefaultLocationID {
		t.Errorf("Default().ID = %q, want %q", loc.ID, core.DefaultLocationID)
	}
	if !loc.IsDefault || !loc.Active {
		t.Errorf("Default() = %+v, want IsDefault and Active true", loc)
	}
}

func testLocationCreateAndList(t *testing.T, s storage.Store) {
	ctx := context.Background()

	// Codes are normalised to upper case on write.
	warehouse := core.Location{Code: "wh2", Name: "Overflow Warehouse", Active: true}
	if err := s.Locations().Create(ctx, &warehouse); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if warehouse.Code != "WH2" {
		t.Errorf("Code = %q, want it normalised to %q", warehouse.Code, "WH2")
	}

	got, err := s.Locations().GetByCode(ctx, "wh2")
	if err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}
	if got.ID != warehouse.ID {
		t.Errorf("GetByCode().ID = %q, want %q", got.ID, warehouse.ID)
	}

	all, err := s.Locations().List(ctx, false)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List() returned %d locations, want 2", len(all))
	}
	// The default sorts first so it is the obvious pick in a location chooser.
	if !all[0].IsDefault {
		t.Errorf("List()[0] = %q, want the default location first", all[0].Code)
	}
}

func testLocationDuplicateCode(t *testing.T, s storage.Store) {
	ctx := context.Background()

	first := core.Location{Code: "DOCK", Name: "Loading Dock", Active: true}
	if err := s.Locations().Create(ctx, &first); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	second := core.Location{Code: "dock", Name: "Duplicate", Active: true}
	err := s.Locations().Create(ctx, &second)
	if !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate code error = %v, want core.ErrConflict", err)
	}
}

// --- products ---------------------------------------------------------------

func testProductCreateAndGet(t *testing.T, s storage.Store) {
	ctx := context.Background()

	want := newProduct("WIDGET-1", "Blue Widget", "12.50")
	if err := s.Products().Create(ctx, &want); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if want.ID.IsZero() {
		t.Fatal("Create() left ID unset, want a generated id")
	}
	if want.Version != 1 {
		t.Errorf("Create() Version = %d, want 1", want.Version)
	}
	if want.CreatedAt.IsZero() || want.UpdatedAt.IsZero() {
		t.Error("Create() left timestamps unset")
	}

	got, err := s.Products().Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.SKU != want.SKU || got.Name != want.Name {
		t.Errorf("Get() = %q/%q, want %q/%q", got.SKU, got.Name, want.SKU, want.Name)
	}
	if got.Price.Minor != 1250 || got.Price.Currency != "USD" {
		t.Errorf("Get().Price = %d %s, want 1250 USD", got.Price.Minor, got.Price.Currency)
	}
	// Timestamps must survive the round trip to within the storage precision.
	if delta := got.CreatedAt.Sub(want.CreatedAt); delta > time.Millisecond || delta < -time.Millisecond {
		t.Errorf("CreatedAt drifted by %v across the round trip", delta)
	}
}

func testProductDuplicateSKU(t *testing.T, s storage.Store) {
	ctx := context.Background()

	first := newProduct("DUP-1", "First", "1.00")
	if err := s.Products().Create(ctx, &first); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	second := newProduct("DUP-1", "Second", "2.00")
	err := s.Products().Create(ctx, &second)
	if !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate SKU error = %v, want core.ErrConflict", err)
	}
}

func testProductSKUCaseInsensitive(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("Abc-100", "Mixed Case SKU", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// "abc-100" and "ABC-100" are the same item to a human, so they must be
	// the same item to the database or duplicates creep in.
	got, err := s.Products().GetBySKU(ctx, "ABC-100")
	if err != nil {
		t.Fatalf("GetBySKU() with different case error = %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("GetBySKU().ID = %q, want %q", got.ID, p.ID)
	}

	clash := newProduct("abc-100", "Should Collide", "6.00")
	if err := s.Products().Create(ctx, &clash); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a case-different SKU error = %v, want core.ErrConflict", err)
	}
}

func testProductGetMissing(t *testing.T, s storage.Store) {
	ctx := context.Background()

	if _, err := s.Products().Get(ctx, core.NewID()); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get() on a missing id error = %v, want core.ErrNotFound", err)
	}
	if _, err := s.Products().GetBySKU(ctx, "NOPE"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetBySKU() on a missing sku error = %v, want core.ErrNotFound", err)
	}
}

func testProductUpdate(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("UPD-1", "Original", "10.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	p.Name = "Renamed"
	p.Price = core.MustParseMoney("19.99", "USD")
	if err := s.Products().Update(ctx, &p); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if p.Version != 2 {
		t.Errorf("Update() Version = %d, want 2", p.Version)
	}

	got, err := s.Products().Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "Renamed" || got.Price.Minor != 1999 {
		t.Errorf("Get() = %q at %d, want %q at 1999", got.Name, got.Price.Minor, "Renamed")
	}
	if got.Version != 2 {
		t.Errorf("stored Version = %d, want 2", got.Version)
	}
}

func testProductStaleUpdate(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("RACE-1", "Contended", "10.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Two clients read the same row.
	clientA, err := s.Products().Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	clientB, err := s.Products().Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	clientA.Name = "Saved by A"
	if err := s.Products().Update(ctx, &clientA); err != nil {
		t.Fatalf("first Update() error = %v", err)
	}

	// B is now working from a version that no longer exists. Silently
	// overwriting A's edit is the exact failure optimistic concurrency is here
	// to prevent.
	clientB.Name = "Saved by B"
	err = s.Products().Update(ctx, &clientB)
	if !errors.Is(err, core.ErrConflict) {
		t.Fatalf("stale Update() error = %v, want core.ErrConflict", err)
	}

	got, _ := s.Products().Get(ctx, p.ID)
	if got.Name != "Saved by A" {
		t.Errorf("stored Name = %q, want A's edit to have survived", got.Name)
	}
}

func testProductDeleteAndArchive(t *testing.T, s storage.Store) {
	ctx := context.Background()

	// A product with no history can be removed outright.
	clean := newProduct("DEL-1", "Never Stocked", "1.00")
	if err := s.Products().Create(ctx, &clean); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := s.Products().Delete(ctx, clean.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := s.Products().Get(ctx, clean.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get() after Delete() error = %v, want core.ErrNotFound", err)
	}
	if err := s.Products().Delete(ctx, clean.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("second Delete() error = %v, want core.ErrNotFound", err)
	}

	// A product that appears in the ledger must not be erasable, or the
	// history would point at nothing.
	stocked := newProduct("DEL-2", "Has History", "1.00")
	if err := s.Products().Create(ctx, &stocked); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendMovement(t, s, stocked.ID, 5, core.ReasonOpeningBalance)

	if err := s.Products().Delete(ctx, stocked.ID); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Delete() on a product with history error = %v, want core.ErrConflict", err)
	}

	// Archiving is the supported alternative.
	if err := s.Products().SetActive(ctx, stocked.ID, false); err != nil {
		t.Fatalf("SetActive(false) error = %v", err)
	}
	active, err := s.Products().List(ctx, storage.ProductFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(active) != 0 {
		t.Errorf("List() returned %d products, want archived ones hidden by default", len(active))
	}
	all, err := s.Products().List(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List(IncludeInactive) returned %d products, want 1", len(all))
	}
}

func testProductList(t *testing.T, s storage.Store) {
	ctx := context.Background()

	seed := []struct {
		sku, name, price, category string
		reorderPoint               int64
		stock                      int64
	}{
		{"AAA-1", "Anvil", "30.00", "Tools", 5, 3},
		{"BBB-2", "Bolt Cutter", "10.00", "Tools", 10, 50},
		{"CCC-3", "Cable Tie", "1.00", "Consumables", 0, 0},
	}
	for _, sp := range seed {
		p := newProduct(sp.sku, sp.name, sp.price)
		p.Category = sp.category
		p.ReorderPoint = sp.reorderPoint
		if err := s.Products().Create(ctx, &p); err != nil {
			t.Fatalf("Create(%s) error = %v", sp.sku, err)
		}
		if sp.stock != 0 {
			appendMovement(t, s, p.ID, sp.stock, core.ReasonOpeningBalance)
		}
	}

	t.Run("search matches sku and name", func(t *testing.T) {
		bySKU, err := s.Products().List(ctx, storage.ProductFilter{Search: "bbb"})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(bySKU) != 1 || bySKU[0].SKU != "BBB-2" {
			t.Errorf("search by sku returned %d rows, want just BBB-2", len(bySKU))
		}

		byName, err := s.Products().List(ctx, storage.ProductFilter{Search: "cable"})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(byName) != 1 || byName[0].SKU != "CCC-3" {
			t.Errorf("search by name returned %d rows, want just CCC-3", len(byName))
		}
	})

	t.Run("wildcards in the search term are literal", func(t *testing.T) {
		// Typing "%" must search for a percent sign, not match everything.
		got, err := s.Products().List(ctx, storage.ProductFilter{Search: "%"})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 0 {
			t.Errorf("search for %q returned %d rows, want 0", "%", len(got))
		}
	})

	t.Run("on-hand comes from the ledger", func(t *testing.T) {
		got, err := s.Products().List(ctx, storage.ProductFilter{Sort: storage.SortProductSKU})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		want := []int64{3, 50, 0}
		for i, w := range want {
			if got[i].OnHand != w {
				t.Errorf("%s OnHand = %d, want %d", got[i].SKU, got[i].OnHand, w)
			}
		}
	})

	t.Run("sorts by every supported key", func(t *testing.T) {
		cases := []struct {
			sort  storage.ProductSort
			dir   storage.SortDirection
			first string
		}{
			{storage.SortProductName, storage.Ascending, "AAA-1"},
			{storage.SortProductName, storage.Descending, "CCC-3"},
			{storage.SortProductSKU, storage.Descending, "CCC-3"},
			{storage.SortProductPrice, storage.Ascending, "CCC-3"},
			{storage.SortProductPrice, storage.Descending, "AAA-1"},
			{storage.SortProductStock, storage.Descending, "BBB-2"},
			{storage.SortProductStock, storage.Ascending, "CCC-3"},
		}
		for _, tc := range cases {
			got, err := s.Products().List(ctx, storage.ProductFilter{Sort: tc.sort, Direction: tc.dir})
			if err != nil {
				t.Fatalf("List(%s %s) error = %v", tc.sort, tc.dir, err)
			}
			if len(got) == 0 || got[0].SKU != tc.first {
				t.Errorf("List(%s %s) first = %v, want %s", tc.sort, tc.dir, skus(got), tc.first)
			}
		}
	})

	t.Run("reorder state uses each product's own point", func(t *testing.T) {
		// AAA-1 has 3 against a point of 5, CCC-3 has nothing at all. BBB-2
		// has 50 against a point of 10 and must not appear: one catalogue-wide
		// threshold would get this wrong in both directions.
		got, err := s.Products().List(ctx, storage.ProductFilter{
			Stock: storage.StockNeedsReorder, Sort: storage.SortProductSKU,
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if want := []string{"AAA-1", "CCC-3"}; !equalStrings(skus(got), want) {
			t.Errorf("needs-reorder returned %v, want %v", skus(got), want)
		}
	})

	t.Run("out of stock and in stock states", func(t *testing.T) {
		out, err := s.Products().List(ctx, storage.ProductFilter{Stock: storage.StockOut})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if want := []string{"CCC-3"}; !equalStrings(skus(out), want) {
			t.Errorf("out of stock returned %v, want %v", skus(out), want)
		}

		in, err := s.Products().List(ctx, storage.ProductFilter{
			Stock: storage.StockInStock, Sort: storage.SortProductSKU,
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if want := []string{"AAA-1", "BBB-2"}; !equalStrings(skus(in), want) {
			t.Errorf("in stock returned %v, want %v", skus(in), want)
		}
	})

	t.Run("category facet", func(t *testing.T) {
		got, err := s.Products().List(ctx, storage.ProductFilter{
			Category: "tools", Sort: storage.SortProductSKU,
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if want := []string{"AAA-1", "BBB-2"}; !equalStrings(skus(got), want) {
			t.Errorf("category filter returned %v, want %v", skus(got), want)
		}

		categories, err := s.Products().Categories(ctx)
		if err != nil {
			t.Fatalf("Categories() error = %v", err)
		}
		if want := []string{"Consumables", "Tools"}; !equalStrings(categories, want) {
			t.Errorf("Categories() = %v, want %v", categories, want)
		}
	})

	t.Run("status sort puts the urgent items first", func(t *testing.T) {
		got, err := s.Products().List(ctx, storage.ProductFilter{Sort: storage.SortProductStatus})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) == 0 || got[0].SKU != "CCC-3" {
			t.Errorf("status sort returned %v, want the out-of-stock item first", skus(got))
		}
		if got[len(got)-1].SKU != "BBB-2" {
			t.Errorf("status sort returned %v, want the healthy item last", skus(got))
		}
	})
}

func testProductPaging(t *testing.T, s storage.Store) {
	ctx := context.Background()

	for _, sku := range []string{"P-1", "P-2", "P-3", "P-4", "P-5"} {
		p := newProduct(sku, "Product "+sku, "1.00")
		if err := s.Products().Create(ctx, &p); err != nil {
			t.Fatalf("Create(%s) error = %v", sku, err)
		}
	}

	filter := storage.ProductFilter{Sort: storage.SortProductSKU, Limit: 2}
	var seen []string
	for offset := 0; offset < 6; offset += 2 {
		filter.Offset = offset
		page, err := s.Products().List(ctx, filter)
		if err != nil {
			t.Fatalf("List(offset=%d) error = %v", offset, err)
		}
		seen = append(seen, skus(page)...)
	}
	if len(seen) != 5 {
		t.Errorf("paging produced %v, want all 5 rows exactly once", seen)
	}

	// Count must ignore paging, or the UI cannot report a total.
	total, err := s.Products().Count(ctx, filter)
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if total != 5 {
		t.Errorf("Count() = %d, want 5 regardless of Limit and Offset", total)
	}
}

func testProductPriceExact(t *testing.T, s storage.Store) {
	ctx := context.Background()

	// 0.1 + 0.2 style values are exactly where float storage loses money.
	prices := []string{"0.01", "0.10", "0.29", "19.99", "1234567.89", "0.00"}
	for i, raw := range prices {
		p := newProduct("MONEY-"+raw, "Price "+raw, raw)
		p.SKU = "MONEY-" + string(rune('A'+i))
		if err := s.Products().Create(ctx, &p); err != nil {
			t.Fatalf("Create(%s) error = %v", raw, err)
		}
		got, err := s.Products().Get(ctx, p.ID)
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got.Price.String() != raw {
			t.Errorf("price %q round-tripped as %q", raw, got.Price.String())
		}
	}
}

// --- movements --------------------------------------------------------------

func testMovementAppend(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("MOVE-1", "Tracked Item", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	appendMovement(t, s, p.ID, 100, core.ReasonOpeningBalance)
	appendMovement(t, s, p.ID, -30, core.ReasonSale)
	appendMovement(t, s, p.ID, 5, core.ReasonReturn)

	onHand, err := s.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand() error = %v", err)
	}
	if onHand != 75 {
		t.Errorf("OnHand() = %d, want 75", onHand)
	}
}

func testMovementRecompute(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("LEDGER-1", "Reconciled Item", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, delta := range []int64{40, -7, 12, -3} {
		reason := core.ReasonAdjustment
		if delta > 0 {
			reason = core.ReasonReceipt
		}
		appendMovement(t, s, p.ID, delta, reason)
	}

	before, err := s.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand() error = %v", err)
	}

	// Recomputing from the ledger must land on exactly the cached value. If it
	// does not, the cache and the ledger have diverged and every report built
	// on the cache is wrong.
	if err := s.Movements().Recompute(ctx, p.ID); err != nil {
		t.Fatalf("Recompute() error = %v", err)
	}
	after, err := s.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand() error = %v", err)
	}

	if before != after {
		t.Errorf("cached level %d disagrees with the ledger sum %d", before, after)
	}
	if after != 42 {
		t.Errorf("OnHand() = %d, want 42", after)
	}
}

func testMovementFilter(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("FILT-1", "Filtered", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendMovement(t, s, p.ID, 10, core.ReasonOpeningBalance)
	appendMovement(t, s, p.ID, -2, core.ReasonSale)
	appendMovement(t, s, p.ID, -1, core.ReasonSale)

	all, err := s.Movements().List(ctx, storage.MovementFilter{ProductID: p.ID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List() returned %d movements, want 3", len(all))
	}
	// Newest first is the default, because that is what a history panel shows.
	if !all[0].OccurredAt.After(all[2].OccurredAt) && !all[0].OccurredAt.Equal(all[2].OccurredAt) {
		t.Errorf("List() is not ordered newest first")
	}

	sales, err := s.Movements().List(ctx, storage.MovementFilter{
		ProductID: p.ID, Reason: core.ReasonSale,
	})
	if err != nil {
		t.Fatalf("List(reason) error = %v", err)
	}
	if len(sales) != 2 {
		t.Errorf("List(reason=sale) returned %d, want 2", len(sales))
	}

	n, err := s.Movements().Count(ctx, storage.MovementFilter{ProductID: p.ID})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if n != 3 {
		t.Errorf("Count() = %d, want 3", n)
	}
}

func testMovementZeroDelta(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("ZERO-1", "No-op", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A zero-quantity movement carries no information and would pad the audit
	// trail with noise.
	m := core.StockMovement{
		ProductID: p.ID, LocationID: core.DefaultLocationID,
		QtyDelta: 0, Reason: core.ReasonAdjustment,
	}
	if err := s.Movements().Append(ctx, &m); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Append() with a zero delta error = %v, want core.ErrInvalid", err)
	}
}

func testMovementLevels(t *testing.T, s storage.Store) {
	ctx := context.Background()

	second := core.Location{Code: "WH2", Name: "Second Site", Active: true}
	if err := s.Locations().Create(ctx, &second); err != nil {
		t.Fatalf("Create(location) error = %v", err)
	}

	p := newProduct("MULTI-1", "Split Across Sites", "5.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	appendMovementAt(t, s, p.ID, core.DefaultLocationID, 10, core.ReasonOpeningBalance)
	appendMovementAt(t, s, p.ID, second.ID, 25, core.ReasonOpeningBalance)

	main, err := s.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand(main) error = %v", err)
	}
	if main != 10 {
		t.Errorf("OnHand(main) = %d, want 10", main)
	}

	levels, err := s.Movements().Levels(ctx, p.ID)
	if err != nil {
		t.Fatalf("Levels() error = %v", err)
	}
	if len(levels) != 2 {
		t.Fatalf("Levels() returned %d rows, want one per location", len(levels))
	}

	var total int64
	for _, l := range levels {
		total += l.OnHand
	}
	if total != 35 {
		t.Errorf("total across locations = %d, want 35", total)
	}

	// A default-location listing must not silently include other sites' stock.
	listed, err := s.Products().List(ctx, storage.ProductFilter{LocationID: core.DefaultLocationID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].OnHand != 10 {
		t.Errorf("List(main).OnHand = %v, want 10", listed)
	}
}

// --- transactions -----------------------------------------------------------

func testTxRollback(t *testing.T, s storage.Store) {
	ctx := context.Background()

	sentinel := errors.New("deliberate failure")
	err := s.InTx(ctx, func(st storage.Store) error {
		p := newProduct("TX-1", "Should Not Persist", "1.00")
		if err := st.Products().Create(ctx, &p); err != nil {
			return err
		}
		appendMovementIn(t, ctx, st, p.ID, 5, core.ReasonOpeningBalance)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx() error = %v, want the sentinel to propagate", err)
	}

	if _, err := s.Products().GetBySKU(ctx, "TX-1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("product survived a rolled-back transaction: err = %v", err)
	}
	count, err := s.Movements().Count(ctx, storage.MovementFilter{})
	if err != nil {
		t.Fatalf("Count() error = %v", err)
	}
	if count != 0 {
		t.Errorf("%d ledger rows survived a rolled-back transaction, want 0", count)
	}
}

func testTxNested(t *testing.T, s storage.Store) {
	ctx := context.Background()

	sentinel := errors.New("inner failure")
	err := s.InTx(ctx, func(outer storage.Store) error {
		p := newProduct("TX-2", "Nested", "1.00")
		if err := outer.Products().Create(ctx, &p); err != nil {
			return err
		}
		// A nested call must join the outer transaction, not commit
		// independently, or a later failure would leave half the work behind.
		return outer.InTx(ctx, func(inner storage.Store) error {
			appendMovementIn(t, ctx, inner, p.ID, 5, core.ReasonOpeningBalance)
			return sentinel
		})
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx() error = %v, want the sentinel to propagate", err)
	}

	if _, err := s.Products().GetBySKU(ctx, "TX-2"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("outer work committed despite an inner failure: err = %v", err)
	}
}

// --- helpers ----------------------------------------------------------------

func newProduct(sku, name, price string) core.Product {
	return core.Product{
		SKU:    sku,
		Name:   name,
		Price:  core.MustParseMoney(price, "USD"),
		Active: true,
	}
}

func appendMovement(t *testing.T, s storage.Store, productID core.ID, delta int64, reason core.MovementReason) {
	t.Helper()
	appendMovementAt(t, s, productID, core.DefaultLocationID, delta, reason)
}

func appendMovementAt(t *testing.T, s storage.Store, productID, locationID core.ID, delta int64, reason core.MovementReason) {
	t.Helper()
	appendMovementIn(t, context.Background(), s, productID, delta, reason, locationID)
}

func appendMovementIn(t *testing.T, ctx context.Context, s storage.Store, productID core.ID, delta int64, reason core.MovementReason, locationID ...core.ID) {
	t.Helper()

	location := core.DefaultLocationID
	if len(locationID) > 0 {
		location = locationID[0]
	}
	m := core.StockMovement{
		ProductID:  productID,
		LocationID: location,
		QtyDelta:   delta,
		Reason:     reason,
		// Movements are timestamped a millisecond apart so ordering assertions
		// are not at the mercy of clock resolution.
		OccurredAt: time.Now().UTC().Add(time.Duration(delta) * time.Microsecond),
	}
	if err := s.Movements().Append(ctx, &m); err != nil {
		t.Fatalf("Append(%d %s) error = %v", delta, reason, err)
	}
}

func skus(items []core.ProductWithStock) []string {
	out := make([]string, len(items))
	for i, p := range items {
		out[i] = p.SKU
	}
	return out
}

// --- product master ---------------------------------------------------------

func testProductBarcode(t *testing.T, s storage.Store) {
	ctx := context.Background()

	scanned := newProduct("SCAN-1", "Scanned Item", "3.00")
	scanned.Barcode = "0123456789012"
	if err := s.Products().Create(ctx, &scanned); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Products().GetByBarcode(ctx, "0123456789012")
	if err != nil {
		t.Fatalf("GetByBarcode() error = %v", err)
	}
	if got.ID != scanned.ID {
		t.Errorf("GetByBarcode().ID = %q, want %q", got.ID, scanned.ID)
	}

	// Two products must never share a barcode, or a scan is ambiguous.
	clash := newProduct("SCAN-2", "Duplicate Code", "4.00")
	clash.Barcode = "0123456789012"
	if err := s.Products().Create(ctx, &clash); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate barcode error = %v, want core.ErrConflict", err)
	}

	// Most products have no barcode, so blank must not collide with blank.
	for _, sku := range []string{"NOCODE-1", "NOCODE-2"} {
		p := newProduct(sku, "No Barcode", "1.00")
		if err := s.Products().Create(ctx, &p); err != nil {
			t.Fatalf("Create(%s) with no barcode error = %v", sku, err)
		}
	}

	if _, err := s.Products().GetByBarcode(ctx, "9999999999999"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetByBarcode(unknown) error = %v, want core.ErrNotFound", err)
	}

	// Searching must find a product by the code on its label, because that is
	// what someone types when the scanner will not read a damaged barcode.
	found, err := s.Products().List(ctx, storage.ProductFilter{Search: "012345678"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(found) != 1 || found[0].SKU != "SCAN-1" {
		t.Errorf("search by barcode returned %v, want SCAN-1", skus(found))
	}
}

func testProductTags(t *testing.T, s storage.Store) {
	ctx := context.Background()

	tagged := newProduct("TAG-1", "Tagged Item", "1.00")
	tagged.Tags = core.ParseTags("fragile, hazmat, Fragile")
	if err := s.Products().Create(ctx, &tagged); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(tagged.Tags) != 2 {
		t.Errorf("Tags = %v, want the duplicate collapsed", tagged.Tags)
	}

	other := newProduct("TAG-2", "Other Item", "1.00")
	other.Tags = core.ParseTags("card")
	if err := s.Products().Create(ctx, &other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Products().Get(ctx, tagged.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.Tags.Has("hazmat") {
		t.Errorf("Tags = %v, want them to survive the round trip", got.Tags)
	}

	filtered, err := s.Products().List(ctx, storage.ProductFilter{Tag: "fragile"})
	if err != nil {
		t.Fatalf("List(tag) error = %v", err)
	}
	if len(filtered) != 1 || filtered[0].SKU != "TAG-1" {
		t.Errorf("tag filter returned %v, want TAG-1", skus(filtered))
	}

	// A tag filter must match whole tags. Searching "card" must not also match
	// a product tagged "cardboard", which is what a naive substring would do.
	cardboard := newProduct("TAG-3", "Boxes", "1.00")
	cardboard.Tags = core.ParseTags("cardboard")
	if err := s.Products().Create(ctx, &cardboard); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	exact, err := s.Products().List(ctx, storage.ProductFilter{Tag: "card"})
	if err != nil {
		t.Fatalf("List(tag) error = %v", err)
	}
	if len(exact) != 1 || exact[0].SKU != "TAG-2" {
		t.Errorf("tag filter for %q returned %v, want only TAG-2", "card", skus(exact))
	}

	all, err := s.Products().Tags(ctx)
	if err != nil {
		t.Fatalf("Tags() error = %v", err)
	}
	if want := []string{"card", "cardboard", "fragile", "hazmat"}; !equalStrings(all, want) {
		t.Errorf("Tags() = %v, want %v", all, want)
	}
}

func testProductCustomFields(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("CF-1", "Custom Fields", "1.00")
	p.CustomFields = core.CustomFields{"Shelf": "A-14", "Warranty": "24 months"}
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Products().Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.CustomFields["Shelf"] != "A-14" || got.CustomFields["Warranty"] != "24 months" {
		t.Errorf("CustomFields = %v, want them preserved", got.CustomFields)
	}

	// Clearing them must actually clear them.
	got.CustomFields = nil
	if err := s.Products().Update(ctx, &got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	cleared, err := s.Products().Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(cleared.CustomFields) != 0 {
		t.Errorf("CustomFields = %v, want empty", cleared.CustomFields)
	}
}

// --- ledger reporting -------------------------------------------------------

func testMovementCostAndLot(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("LOT-1", "Perishable", "5.00")
	p.TrackLots = true
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	expiry := time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC)
	m := core.StockMovement{
		ProductID:  p.ID,
		LocationID: core.DefaultLocationID,
		QtyDelta:   40,
		Reason:     core.ReasonReceipt,
		UnitCost:   core.MustParseMoney("2.35", "USD"),
		LotNumber:  "LOT-2027-A",
		ExpiryDate: expiry,
	}
	if err := s.Movements().Append(ctx, &m); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	got, err := s.Movements().List(ctx, storage.MovementFilter{ProductID: p.ID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List() returned %d movements, want 1", len(got))
	}
	if got[0].UnitCost.Minor != 235 {
		t.Errorf("UnitCost = %d, want 235", got[0].UnitCost.Minor)
	}
	if got[0].LotNumber != "LOT-2027-A" {
		t.Errorf("LotNumber = %q, want %q", got[0].LotNumber, "LOT-2027-A")
	}
	if !got[0].ExpiryDate.Equal(expiry) {
		t.Errorf("ExpiryDate = %v, want %v", got[0].ExpiryDate, expiry)
	}

	// Expiry reporting must find the lot before its date and ignore it after.
	due, err := s.Movements().ExpiringLots(ctx, expiry)
	if err != nil {
		t.Fatalf("ExpiringLots() error = %v", err)
	}
	if len(due) != 1 {
		t.Errorf("ExpiringLots(on the expiry date) returned %d, want 1", len(due))
	}
	notDue, err := s.Movements().ExpiringLots(ctx, expiry.AddDate(0, 0, -1))
	if err != nil {
		t.Fatalf("ExpiringLots() error = %v", err)
	}
	if len(notDue) != 0 {
		t.Errorf("ExpiringLots(before the expiry date) returned %d, want 0", len(notDue))
	}
}

// testMovementCostHistory checks the ordering valuation depends on. FIFO
// consumes cost layers in receipt order, so history returned out of order
// silently produces a different valuation rather than an error.
func testMovementCostHistory(t *testing.T, s storage.Store) {
	ctx := context.Background()

	p := newProduct("HIST-1", "Costed", "10.00")
	if err := s.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	base := time.Now().UTC().Add(-100 * time.Hour)
	costs := []struct {
		offset time.Duration
		qty    int64
		cost   string
	}{
		{72 * time.Hour, 10, "3.00"},
		{0, 5, "1.00"},
		{24 * time.Hour, 8, "2.00"},
	}
	for _, c := range costs {
		m := core.StockMovement{
			ProductID:  p.ID,
			LocationID: core.DefaultLocationID,
			QtyDelta:   c.qty,
			Reason:     core.ReasonReceipt,
			UnitCost:   core.MustParseMoney(c.cost, "USD"),
			OccurredAt: base.Add(c.offset),
		}
		if err := s.Movements().Append(ctx, &m); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	history, err := s.Movements().CostHistory(ctx, core.DefaultLocationID, p.ID)
	if err != nil {
		t.Fatalf("CostHistory() error = %v", err)
	}
	movements := history[p.ID]
	if len(movements) != 3 {
		t.Fatalf("CostHistory() returned %d movements, want 3", len(movements))
	}
	for i := 1; i < len(movements); i++ {
		if movements[i].OccurredAt.Before(movements[i-1].OccurredAt) {
			t.Fatalf("CostHistory() is not ordered oldest first: %v then %v",
				movements[i-1].OccurredAt, movements[i].OccurredAt)
		}
	}
	if movements[0].UnitCost.Minor != 100 {
		t.Errorf("first layer cost = %d, want the earliest receipt at 100", movements[0].UnitCost.Minor)
	}

	// With no product ids, every product's history comes back.
	all, err := s.Movements().CostHistory(ctx, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("CostHistory(all) error = %v", err)
	}
	if len(all[p.ID]) != 3 {
		t.Errorf("CostHistory(all) returned %d movements for the product, want 3", len(all[p.ID]))
	}
}

func testMovementLastMovedAt(t *testing.T, s storage.Store) {
	ctx := context.Background()

	moved := newProduct("MOVED-1", "Has Moved", "1.00")
	if err := s.Products().Create(ctx, &moved); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	still := newProduct("STILL-1", "Never Moved", "1.00")
	if err := s.Products().Create(ctx, &still); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	older := time.Now().UTC().Add(-48 * time.Hour)
	newer := time.Now().UTC().Add(-1 * time.Hour)
	for _, when := range []time.Time{older, newer} {
		m := core.StockMovement{
			ProductID: moved.ID, LocationID: core.DefaultLocationID,
			QtyDelta: 5, Reason: core.ReasonReceipt, OccurredAt: when,
		}
		if err := s.Movements().Append(ctx, &m); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	last, err := s.Movements().LastMovedAt(ctx, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("LastMovedAt() error = %v", err)
	}
	if got, ok := last[moved.ID]; !ok {
		t.Error("LastMovedAt() has no entry for a product that moved")
	} else if delta := got.Sub(newer); delta > time.Second || delta < -time.Second {
		t.Errorf("LastMovedAt() = %v, want the most recent movement at %v", got, newer)
	}

	// A product that never moved must be absent rather than dated zero, so
	// callers can tell "never" from "long ago".
	if _, ok := last[still.ID]; ok {
		t.Error("LastMovedAt() has an entry for a product that never moved")
	}
}

func testSettings(t *testing.T, s storage.Store) {
	ctx := context.Background()

	// The valuation method is seeded, because an install with no accounting
	// policy at all cannot value its stock.
	method, err := s.Settings().Get(ctx, storage.SettingValuationMethod)
	if err != nil {
		t.Fatalf("Get(valuation_method) error = %v", err)
	}
	if method != string(core.ValuationWeightedAverage) {
		t.Errorf("valuation_method = %q, want %q", method, core.ValuationWeightedAverage)
	}

	if err := s.Settings().Set(ctx, storage.SettingValuationMethod, string(core.ValuationFIFO)); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	updated, err := s.Settings().Get(ctx, storage.SettingValuationMethod)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if updated != string(core.ValuationFIFO) {
		t.Errorf("valuation_method = %q after update, want %q", updated, core.ValuationFIFO)
	}

	if err := s.Settings().Set(ctx, storage.SettingCompanyName, "Acme Supplies"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	all, err := s.Settings().All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v", err)
	}
	if all[storage.SettingCompanyName] != "Acme Supplies" {
		t.Errorf("All() = %v, want the company name present", all)
	}

	if _, err := s.Settings().Get(ctx, "no_such_setting"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get(unknown) error = %v, want core.ErrNotFound", err)
	}
}

// equalStrings compares two string slices element by element.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
