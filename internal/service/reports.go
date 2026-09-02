package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// ReportScope narrows a report to a location and, optionally, a slice of the
// catalogue.
type ReportScope struct {
	LocationID      core.ID
	Category        string
	Supplier        string
	IncludeInactive bool
}

func (r ReportScope) filter() storage.ProductFilter {
	return storage.ProductFilter{
		LocationID:      r.LocationID,
		Category:        r.Category,
		Supplier:        r.Supplier,
		IncludeInactive: r.IncludeInactive,
		Sort:            storage.SortProductSKU,
	}
}

// Valuation computes what the stock on hand is worth, and what has been issued
// out, by replaying the ledger under the configured accounting policy.
//
// It is computed from movements rather than read from a stored figure, so
// changing the method re-values history instead of only applying to stock
// received afterwards — which is what "apply a costing method consistently"
// actually requires.
func (s *Inventory) Valuation(ctx context.Context, scope ReportScope) (core.Valuation, error) {
	method, err := s.ValuationMethod(ctx)
	if err != nil {
		return core.Valuation{}, err
	}

	products, err := s.store.Products().List(ctx, scope.filter())
	if err != nil {
		return core.Valuation{}, err
	}

	location := s.locationOrDefault(scope.LocationID)
	history, err := s.store.Movements().CostHistory(ctx, location)
	if err != nil {
		return core.Valuation{}, err
	}

	valuation := core.Valuation{
		Method:   method,
		Currency: s.defaultCurrency,
		AsOf:     s.now(),
		Total:    core.Zero(s.defaultCurrency),
		COGS:     core.Zero(s.defaultCurrency),
	}

	for _, product := range products {
		if product.NonStock {
			continue
		}

		currency := product.Cost.Currency
		if currency == "" {
			currency = s.defaultCurrency
		}

		onHand, value, cogs := core.ValueLedger(history[product.ID], method, currency)

		line := core.ProductValuation{
			ProductID: product.ID,
			SKU:       product.SKU,
			Name:      product.Name,
			Category:  product.Category,
			OnHand:    onHand,
			Value:     value,
			COGS:      cogs,
		}
		if onHand > 0 {
			line.UnitCost = core.NewMoney(value.Minor/onHand, currency)
		} else {
			line.UnitCost = product.Cost
		}

		// Only amounts in the reporting currency roll into the totals. A
		// mixed-currency catalogue still lists every line; it just does not
		// pretend the sum of two currencies is a number.
		if currency == valuation.Currency {
			valuation.Total.Minor += value.Minor
			valuation.COGS.Minor += cogs.Minor
		}
		valuation.Lines = append(valuation.Lines, line)
	}

	sort.SliceStable(valuation.Lines, func(i, j int) bool {
		return valuation.Lines[i].Value.Minor > valuation.Lines[j].Value.Minor
	})
	return valuation, nil
}

// ABCAnalysis ranks products by their share of stock value, so attention and
// counting effort go where the money actually is.
func (s *Inventory) ABCAnalysis(ctx context.Context, scope ReportScope) ([]core.ABCLine, error) {
	valuation, err := s.Valuation(ctx, scope)
	if err != nil {
		return nil, err
	}
	return core.ClassifyABC(valuation.Lines), nil
}

// AgingLine is one product's place in a stock-aging report.
type AgingLine struct {
	Product core.ProductWithStock
	Bucket  core.AgingBucket
	// DaysSinceMovement is -1 for stock that has never moved at all.
	DaysSinceMovement int
	Value             core.Money
}

// AgingReport groups stock by how long it has sat without moving. It is the
// report that finds capital tied up in things nobody is buying.
type AgingReport struct {
	AsOf     time.Time
	Currency core.Currency
	Lines    []AgingLine
	// Totals is the value held in each bucket.
	Totals map[core.AgingBucket]core.Money
	// Counts is how many products sit in each bucket.
	Counts map[core.AgingBucket]int
}

// StockAging builds the aging report.
func (s *Inventory) StockAging(ctx context.Context, scope ReportScope) (AgingReport, error) {
	products, err := s.store.Products().List(ctx, scope.filter())
	if err != nil {
		return AgingReport{}, err
	}

	location := s.locationOrDefault(scope.LocationID)
	lastMoved, err := s.store.Movements().LastMovedAt(ctx, location)
	if err != nil {
		return AgingReport{}, err
	}

	// Values come from the same ledger replay the valuation report uses.
	// Costing this report separately, at the product's standing cost, would
	// give two screens two different answers for the value of one shelf — and
	// whichever a manager saw first would be the one they quoted.
	valuation, err := s.Valuation(ctx, scope)
	if err != nil {
		return AgingReport{}, err
	}
	values := make(map[core.ID]core.Money, len(valuation.Lines))
	for _, line := range valuation.Lines {
		values[line.ProductID] = line.Value
	}

	now := s.now()
	report := AgingReport{
		AsOf:     now,
		Currency: s.defaultCurrency,
		Totals:   map[core.AgingBucket]core.Money{},
		Counts:   map[core.AgingBucket]int{},
	}
	for _, bucket := range core.AgingBuckets {
		report.Totals[bucket] = core.Zero(s.defaultCurrency)
	}

	for _, product := range products {
		// Aging is about capital sitting still. An item with nothing on hand
		// is tying up nothing, so it is not what this report is looking for.
		if product.NonStock || product.OnHand <= 0 {
			continue
		}

		days := -1
		bucket := core.AgingDead
		if when, ok := lastMoved[product.ID]; ok && !when.IsZero() {
			days = int(now.Sub(when).Hours() / 24)
			bucket = core.BucketForAge(days)
		}

		value, ok := values[product.ID]
		if !ok {
			value = core.Zero(s.defaultCurrency)
		}

		product.LastMovementAt = lastMoved[product.ID]
		line := AgingLine{
			Product:           product,
			Bucket:            bucket,
			DaysSinceMovement: days,
			Value:             value,
		}

		report.Lines = append(report.Lines, line)
		report.Counts[bucket]++
		if line.Value.Currency == report.Currency {
			total := report.Totals[bucket]
			total.Minor += line.Value.Minor
			report.Totals[bucket] = total
		}
	}

	// Oldest first: the point of the report is the stock at the far end.
	sort.SliceStable(report.Lines, func(i, j int) bool {
		return report.Lines[i].DaysSinceMovement > report.Lines[j].DaysSinceMovement
	})
	return report, nil
}

// ReorderLine is one suggestion on the reorder report.
type ReorderLine struct {
	Product core.ProductWithStock
	// Suggested is how much to buy to clear the reorder point.
	Suggested int64
	// EstimatedCost is the suggested quantity at the product's standing cost.
	EstimatedCost core.Money
}

// ReorderReport lists what has fallen to its reorder point, grouped so a buyer
// can work supplier by supplier.
type ReorderReport struct {
	Lines []ReorderLine
	// BySupplier groups the same lines by supplier, since purchase orders are
	// raised per supplier, not per product.
	BySupplier map[string][]ReorderLine
	Total      core.Money
}

// ReorderSuggestions builds the reorder report.
func (s *Inventory) ReorderSuggestions(ctx context.Context, scope ReportScope) (ReorderReport, error) {
	filter := scope.filter()
	filter.Stock = storage.StockNeedsReorder

	products, err := s.store.Products().List(ctx, filter)
	if err != nil {
		return ReorderReport{}, err
	}

	report := ReorderReport{
		BySupplier: map[string][]ReorderLine{},
		Total:      core.Zero(s.defaultCurrency),
	}
	for _, product := range products {
		suggested := product.SuggestedOrderQuantity()
		if suggested <= 0 {
			continue
		}

		line := ReorderLine{
			Product:       product,
			Suggested:     suggested,
			EstimatedCost: product.Cost.MulQty(suggested),
		}
		report.Lines = append(report.Lines, line)

		supplier := product.Supplier
		if supplier == "" {
			supplier = "(no supplier set)"
		}
		report.BySupplier[supplier] = append(report.BySupplier[supplier], line)

		if line.EstimatedCost.Currency == report.Total.Currency {
			report.Total.Minor += line.EstimatedCost.Minor
		}
	}
	return report, nil
}

// ExpiringLot is one lot approaching or past its expiry date.
type ExpiringLot struct {
	Movement core.StockMovement
	Product  core.Product
	// DaysRemaining is negative once the lot has expired.
	DaysRemaining int
}

// ExpiringLots lists lot-tracked stock expiring within the given window. It is
// what makes a recall or a write-off something you plan rather than discover.
func (s *Inventory) ExpiringLots(ctx context.Context, within time.Duration) ([]ExpiringLot, error) {
	cutoff := s.now().Add(within)

	movements, err := s.store.Movements().ExpiringLots(ctx, cutoff)
	if err != nil {
		return nil, err
	}

	now := s.now()
	products := map[core.ID]core.Product{}
	out := make([]ExpiringLot, 0, len(movements))

	for _, m := range movements {
		product, ok := products[m.ProductID]
		if !ok {
			product, err = s.store.Products().Get(ctx, m.ProductID)
			if err != nil {
				return nil, err
			}
			products[m.ProductID] = product
		}

		out = append(out, ExpiringLot{
			Movement:      m,
			Product:       product,
			DaysRemaining: int(m.ExpiryDate.Sub(now).Hours() / 24),
		})
	}
	return out, nil
}

// Dashboard is the summary shown when the application opens: the handful of
// numbers that tell someone whether anything needs their attention today.
type Dashboard struct {
	Currency core.Currency
	AsOf     time.Time

	ActiveProducts   int
	ArchivedProducts int
	TotalUnits       int64
	StockValue       core.Money
	ValuationMethod  core.ValuationMethod

	NeedsReorder  int
	OutOfStock    int
	NegativeStock int
	ExpiringSoon  int

	// RecentActivity is the tail of the ledger, which doubles as an activity
	// feed until the audit log arrives in Phase 2. Each entry carries the
	// product it refers to: a feed that says only "opening balance, 7 minutes
	// ago" eleven times tells the reader nothing.
	RecentActivity []ActivityEntry
	// TopValue is the handful of products holding the most capital.
	TopValue []core.ProductValuation
}

// ActivityEntry is one ledger movement with the product it refers to.
type ActivityEntry struct {
	Movement core.StockMovement
	SKU      string
	Name     string
	Unit     core.UnitOfMeasure
}

// recentActivityDepth is how many movements the dashboard feed shows.
const recentActivityDepth = 8

// ExpirySoonWindow is how far ahead the dashboard looks for expiring lots.
const ExpirySoonWindow = 30 * 24 * time.Hour

// Summary builds the dashboard.
func (s *Inventory) Summary(ctx context.Context, locationID core.ID) (Dashboard, error) {
	location := s.locationOrDefault(locationID)

	counts := []struct {
		filter storage.ProductFilter
		into   *int
	}{
		{storage.ProductFilter{LocationID: location}, nil},
		{storage.ProductFilter{LocationID: location, Stock: storage.StockNeedsReorder}, nil},
		{storage.ProductFilter{LocationID: location, Stock: storage.StockOut}, nil},
		{storage.ProductFilter{LocationID: location, Stock: storage.StockNegative}, nil},
		{storage.ProductFilter{LocationID: location, IncludeInactive: true}, nil},
	}

	results := make([]int, len(counts))
	for i, c := range counts {
		n, err := s.store.Products().Count(ctx, c.filter)
		if err != nil {
			return Dashboard{}, err
		}
		results[i] = n
	}

	valuation, err := s.Valuation(ctx, ReportScope{LocationID: location})
	if err != nil {
		return Dashboard{}, err
	}

	recent, err := s.store.Movements().List(ctx, storage.MovementFilter{
		LocationID: location,
		Limit:      recentActivityDepth,
	})
	if err != nil {
		return Dashboard{}, err
	}

	activity, err := s.DescribeMovements(ctx, recent)
	if err != nil {
		return Dashboard{}, err
	}

	expiring, err := s.ExpiringLots(ctx, ExpirySoonWindow)
	if err != nil {
		return Dashboard{}, err
	}

	dashboard := Dashboard{
		Currency:         s.defaultCurrency,
		AsOf:             s.now(),
		ActiveProducts:   results[0],
		NeedsReorder:     results[1],
		OutOfStock:       results[2],
		NegativeStock:    results[3],
		ArchivedProducts: results[4] - results[0],
		StockValue:       valuation.Total,
		ValuationMethod:  valuation.Method,
		ExpiringSoon:     len(expiring),
		RecentActivity:   activity,
	}

	for _, line := range valuation.Lines {
		dashboard.TotalUnits += line.OnHand
	}
	if len(valuation.Lines) > 5 {
		dashboard.TopValue = valuation.Lines[:5]
	} else {
		dashboard.TopValue = valuation.Lines
	}

	return dashboard, nil
}

// DescribeMovements attaches product details to ledger entries, resolving each
// product once rather than once per row.
//
// The ledger stores only product ids, and a list of ids is unreadable. Every
// screen that shows movements needs this, so it lives here rather than being
// re-implemented per view.
func (s *Inventory) DescribeMovements(ctx context.Context, movements []core.StockMovement) ([]ActivityEntry, error) {
	products := map[core.ID]core.Product{}
	out := make([]ActivityEntry, 0, len(movements))

	for _, m := range movements {
		product, ok := products[m.ProductID]
		if !ok {
			loaded, err := s.store.Products().Get(ctx, m.ProductID)
			if err != nil {
				if errors.Is(err, core.ErrNotFound) {
					// A product removed before it ever moved. The ledger entry
					// is still real, so it is shown without a name rather than
					// dropped, which would make the history lie by omission.
					out = append(out, ActivityEntry{Movement: m, SKU: "(removed)"})
					continue
				}
				return nil, err
			}
			product = loaded
			products[m.ProductID] = product
		}
		out = append(out, ActivityEntry{
			Movement: m, SKU: product.SKU, Name: product.Name, Unit: product.Unit,
		})
	}
	return out, nil
}
