// Package service holds the application's business rules.
//
// Everything that changes data goes through here rather than through a
// repository directly, because this is the layer that composes multi-step
// writes into one transaction and, from Phase 2, enforces permissions and
// writes the audit trail. A UI that reaches past it can bypass both.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// Inventory is the product and stock service.
type Inventory struct {
	store storage.Store
	log   *slog.Logger
	now   func() time.Time

	// defaultCurrency is applied to prices submitted without one.
	defaultCurrency core.Currency
	// allowNegativeStock permits an operation to drive on-hand below zero.
	// Off by default: negative stock is almost always a data-entry mistake,
	// and letting it through quietly corrupts every valuation downstream.
	allowNegativeStock bool
}

// Option customises an Inventory service.
type Option func(*Inventory)

// WithLogger sets the logger. Defaults to slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(s *Inventory) {
		if l != nil {
			s.log = l
		}
	}
}

// WithClock replaces the time source, which tests use to get stable
// timestamps.
func WithClock(now func() time.Time) Option {
	return func(s *Inventory) {
		if now != nil {
			s.now = now
		}
	}
}

// WithDefaultCurrency sets the currency applied to prices that arrive without
// one.
func WithDefaultCurrency(c core.Currency) Option {
	return func(s *Inventory) {
		if c.Valid() {
			s.defaultCurrency = c.Normalize()
		}
	}
}

// AllowNegativeStock lets stock fall below zero. Some operations genuinely
// need it (selling from a shipment not yet received), so it is a deliberate
// setting rather than a hardcoded rule.
func AllowNegativeStock(allow bool) Option {
	return func(s *Inventory) { s.allowNegativeStock = allow }
}

// NewInventory builds the service over a store.
func NewInventory(store storage.Store, opts ...Option) *Inventory {
	s := &Inventory{
		store:           store,
		log:             slog.Default(),
		now:             func() time.Time { return time.Now().UTC() },
		defaultCurrency: core.DefaultCurrency,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Currency reports the currency new records default to.
func (s *Inventory) Currency() core.Currency { return s.defaultCurrency }

// --- products ---------------------------------------------------------------

// OpeningStock is the quantity a product starts life with. It is posted as an
// opening_balance ledger entry rather than written to a level, so that even
// the very first quantity has a recorded origin.
type OpeningStock struct {
	Quantity   int64
	UnitCost   core.Money
	LocationID core.ID
	LotNumber  string
	ExpiryDate time.Time
}

// CreateProduct registers a product and posts its opening balance atomically.
func (s *Inventory) CreateProduct(ctx context.Context, p core.Product, opening OpeningStock) (core.ProductWithStock, error) {
	if opening.Quantity < 0 {
		var v core.ValidationError
		v.Add("opening_stock", "Opening stock cannot be negative")
		return core.ProductWithStock{}, v.ErrOrNil()
	}

	s.applyDefaults(&p)
	p.ID = core.NewID()
	p.Active = true
	p.CreatedAt = s.now()

	// An opening balance with no stated cost is valued at the product's own
	// cost, which is what a user filling in one form expects.
	if opening.UnitCost.IsZero() {
		opening.UnitCost = p.Cost
	}
	location := s.locationOrDefault(opening.LocationID)

	err := s.store.InTx(ctx, func(st storage.Store) error {
		if err := st.Products().Create(ctx, &p); err != nil {
			return err
		}
		if opening.Quantity == 0 {
			return nil
		}
		movement := core.StockMovement{
			ProductID:  p.ID,
			LocationID: location,
			QtyDelta:   opening.Quantity,
			Reason:     core.ReasonOpeningBalance,
			UnitCost:   opening.UnitCost,
			LotNumber:  opening.LotNumber,
			ExpiryDate: opening.ExpiryDate,
			OccurredAt: s.now(),
		}
		return st.Movements().Append(ctx, &movement)
	})
	if err != nil {
		return core.ProductWithStock{}, err
	}

	s.log.Info("product created",
		"product_id", p.ID, "sku", p.SKU, "opening_stock", opening.Quantity)
	return core.ProductWithStock{Product: p, OnHand: opening.Quantity}, nil
}

// UpdateProduct saves an edit, failing with core.ErrConflict if the record
// changed since it was read. The caller passes the product it loaded, so
// p.Version carries the concurrency check.
func (s *Inventory) UpdateProduct(ctx context.Context, p core.Product) (core.Product, error) {
	s.applyDefaults(&p)
	if err := s.store.Products().Update(ctx, &p); err != nil {
		return core.Product{}, err
	}

	s.log.Info("product updated", "product_id", p.ID, "sku", p.SKU)
	return p, nil
}

// applyDefaults fills in what a partially-completed form leaves out.
func (s *Inventory) applyDefaults(p *core.Product) {
	if p.Price.Currency == "" {
		p.Price.Currency = s.defaultCurrency
	}
	if p.Cost.Currency == "" {
		p.Cost.Currency = p.Price.Currency
	}
	if p.Unit == "" {
		p.Unit = core.DefaultUnit
	}
}

// DeleteOutcome reports what removing a product actually did.
type DeleteOutcome string

const (
	// OutcomeDeleted means the row was removed outright.
	OutcomeDeleted DeleteOutcome = "deleted"
	// OutcomeArchived means ledger history referred to the product, so it was
	// marked inactive instead.
	OutcomeArchived DeleteOutcome = "archived"
)

// DeleteProduct removes a product, or archives it when it has stock history.
//
// Hard-deleting an item that appears in the ledger would leave movements
// pointing at nothing and make historical reports unreadable, so a product
// that has ever moved is retired rather than erased.
func (s *Inventory) DeleteProduct(ctx context.Context, id core.ID, actorID core.ID) (DeleteOutcome, error) {
	outcome := OutcomeDeleted

	err := s.store.InTx(ctx, func(st storage.Store) error {
		if _, err := st.Products().Get(ctx, id); err != nil {
			return err
		}

		history, err := st.Movements().Count(ctx, storage.MovementFilter{ProductID: id})
		if err != nil {
			return err
		}
		if history > 0 {
			outcome = OutcomeArchived
			return st.Products().SetActive(ctx, id, false)
		}
		return st.Products().Delete(ctx, id)
	})
	if err != nil {
		return "", err
	}

	s.log.Info("product removed", "product_id", id, "outcome", outcome, "actor_id", actorID)
	return outcome, nil
}

// RestoreProduct returns an archived product to active use.
func (s *Inventory) RestoreProduct(ctx context.Context, id core.ID) error {
	return s.store.Products().SetActive(ctx, id, true)
}

// SetArchived archives or restores many products at once, which is what makes
// tidying a catalogue after an import bearable.
func (s *Inventory) SetArchived(ctx context.Context, ids []core.ID, archived bool) (int, error) {
	var changed int
	err := s.store.InTx(ctx, func(st storage.Store) error {
		for _, id := range ids {
			if err := st.Products().SetActive(ctx, id, !archived); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

// BulkEdit applies the same change to many products. Only the fields whose
// pointer is non-nil are touched, so clearing a category and leaving the
// supplier alone are distinguishable operations.
type BulkEdit struct {
	Category *string
	Supplier *string
	// AddTags and RemoveTags adjust tags without replacing the whole set,
	// because the usual bulk operation is "also mark these fragile", not
	// "replace every tag on these items".
	AddTags      []string
	RemoveTags   []string
	ReorderPoint *int64
	Unit         *core.UnitOfMeasure
}

// ApplyBulkEdit updates many products in one transaction, returning how many
// changed.
func (s *Inventory) ApplyBulkEdit(ctx context.Context, ids []core.ID, edit BulkEdit) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	var changed int
	err := s.store.InTx(ctx, func(st storage.Store) error {
		for _, id := range ids {
			product, err := st.Products().Get(ctx, id)
			if err != nil {
				return err
			}

			if edit.Category != nil {
				product.Category = *edit.Category
			}
			if edit.Supplier != nil {
				product.Supplier = *edit.Supplier
			}
			if edit.ReorderPoint != nil {
				product.ReorderPoint = *edit.ReorderPoint
			}
			if edit.Unit != nil {
				product.Unit = *edit.Unit
			}
			if len(edit.AddTags) > 0 || len(edit.RemoveTags) > 0 {
				product.Tags = adjustTags(product.Tags, edit.AddTags, edit.RemoveTags)
			}

			if err := st.Products().Update(ctx, &product); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	s.log.Info("bulk edit applied", "products", changed)
	return changed, nil
}

func adjustTags(current core.Tags, add, remove []string) core.Tags {
	keep := make([]string, 0, len(current)+len(add))
	for _, tag := range current {
		dropped := false
		for _, r := range remove {
			if strings.EqualFold(tag, strings.TrimSpace(r)) {
				dropped = true
				break
			}
		}
		if !dropped {
			keep = append(keep, tag)
		}
	}
	keep = append(keep, add...)
	return core.ParseTags(strings.Join(keep, ","))
}

// GetProduct reads one product with its on-hand quantity at a location.
func (s *Inventory) GetProduct(ctx context.Context, id core.ID, locationID core.ID) (core.ProductWithStock, error) {
	product, err := s.store.Products().Get(ctx, id)
	if err != nil {
		return core.ProductWithStock{}, err
	}
	return s.withStock(ctx, product, locationID)
}

// GetProductBySKU reads a product by the identifier people actually use.
// SKUs are matched case-insensitively, the same way the unique index treats
// them.
func (s *Inventory) GetProductBySKU(ctx context.Context, sku string, locationID core.ID) (core.ProductWithStock, error) {
	product, err := s.store.Products().GetBySKU(ctx, sku)
	if err != nil {
		return core.ProductWithStock{}, err
	}
	return s.withStock(ctx, product, locationID)
}

// LookupByCode resolves whatever a scanner or a person typed into a product.
//
// It tries the barcode first, then the SKU. A handheld scanner is just a
// keyboard, so the same field has to accept both, and asking an operator to
// pick which kind of code they are holding defeats the point of scanning.
func (s *Inventory) LookupByCode(ctx context.Context, code string, locationID core.ID) (core.ProductWithStock, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return core.ProductWithStock{}, fmt.Errorf("look up product: %w: no code given", core.ErrInvalid)
	}

	product, err := s.store.Products().GetByBarcode(ctx, code)
	if errors.Is(err, core.ErrNotFound) {
		product, err = s.store.Products().GetBySKU(ctx, code)
	}
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return core.ProductWithStock{}, fmt.Errorf(
				"no product matches the code %q: %w", code, core.ErrNotFound)
		}
		return core.ProductWithStock{}, err
	}
	return s.withStock(ctx, product, locationID)
}

func (s *Inventory) withStock(ctx context.Context, product core.Product, locationID core.ID) (core.ProductWithStock, error) {
	onHand, err := s.store.Movements().OnHand(ctx, product.ID, s.locationOrDefault(locationID))
	if err != nil {
		return core.ProductWithStock{}, err
	}
	return core.ProductWithStock{Product: product, OnHand: onHand}, nil
}

// ProductPage is one page of a product listing plus the unpaged total, so the
// UI can render "showing 50 of 8,312".
type ProductPage struct {
	Items  []core.ProductWithStock
	Total  int
	Limit  int
	Offset int
}

// ListProducts returns a filtered, sorted, paged product listing.
func (s *Inventory) ListProducts(ctx context.Context, f storage.ProductFilter) (ProductPage, error) {
	if !f.Sort.Valid() {
		f.Sort = storage.SortProductName
	}
	f.LocationID = s.locationOrDefault(f.LocationID)

	items, err := s.store.Products().List(ctx, f)
	if err != nil {
		return ProductPage{}, err
	}
	total, err := s.store.Products().Count(ctx, f)
	if err != nil {
		return ProductPage{}, err
	}

	// Attach last-movement dates so a list can show how stale a line is
	// without issuing a query per row.
	lastMoved, err := s.store.Movements().LastMovedAt(ctx, f.LocationID)
	if err != nil {
		return ProductPage{}, err
	}
	for i := range items {
		items[i].LastMovementAt = lastMoved[items[i].ID]
	}

	return ProductPage{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}, nil
}

// Categories, Suppliers and Tags return the values in use, for filter menus
// and form autocomplete.
func (s *Inventory) Categories(ctx context.Context) ([]string, error) {
	return s.store.Products().Categories(ctx)
}

func (s *Inventory) Suppliers(ctx context.Context) ([]string, error) {
	return s.store.Products().Suppliers(ctx)
}

func (s *Inventory) TagsInUse(ctx context.Context) ([]string, error) {
	return s.store.Products().Tags(ctx)
}

// --- stock ------------------------------------------------------------------

// AdjustStockInput posts a manual change to a stock level.
type AdjustStockInput struct {
	ProductID  core.ID
	LocationID core.ID
	// Delta is signed: positive adds stock, negative removes it.
	Delta   int64
	Reason  core.MovementReason
	Note    string
	ActorID core.ID

	UnitCost   core.Money
	LotNumber  string
	ExpiryDate time.Time
}

// AdjustStock appends a ledger entry and returns the resulting on-hand level.
func (s *Inventory) AdjustStock(ctx context.Context, in AdjustStockInput) (int64, error) {
	if in.Reason == "" {
		in.Reason = core.ReasonAdjustment
	}
	if !isUserSelectable(in.Reason) {
		return 0, fmt.Errorf("%w: %q is posted by the system and cannot be chosen manually",
			core.ErrInvalid, in.Reason)
	}
	return s.postMovement(ctx, in)
}

// ReceiveStock books goods in at a known cost, which is what makes the receipt
// a valuation event rather than just a quantity change.
func (s *Inventory) ReceiveStock(ctx context.Context, in AdjustStockInput) (int64, error) {
	if in.Delta <= 0 {
		var v core.ValidationError
		v.Add("quantity", "Receiving quantity must be greater than zero")
		return 0, v.ErrOrNil()
	}
	in.Reason = core.ReasonReceipt
	return s.postMovement(ctx, in)
}

// IssueStock books goods out, for example against a sale or a job.
func (s *Inventory) IssueStock(ctx context.Context, in AdjustStockInput) (int64, error) {
	if in.Delta <= 0 {
		var v core.ValidationError
		v.Add("quantity", "Issue quantity must be greater than zero")
		return 0, v.ErrOrNil()
	}
	in.Delta = -in.Delta
	in.Reason = core.ReasonSale
	return s.postMovement(ctx, in)
}

func (s *Inventory) postMovement(ctx context.Context, in AdjustStockInput) (int64, error) {
	location := s.locationOrDefault(in.LocationID)
	var onHand int64

	err := s.store.InTx(ctx, func(st storage.Store) error {
		product, err := st.Products().Get(ctx, in.ProductID)
		if err != nil {
			return err
		}
		if product.NonStock {
			return fmt.Errorf("%w: %s is a non-stock item and has no quantity to adjust",
				core.ErrInvalid, product.SKU)
		}
		if product.TrackLots && in.Delta > 0 && strings.TrimSpace(in.LotNumber) == "" {
			return fmt.Errorf("%w: %s is lot-tracked, so incoming stock needs a lot number",
				core.ErrInvalid, product.SKU)
		}

		current, err := st.Movements().OnHand(ctx, in.ProductID, location)
		if err != nil {
			return err
		}
		onHand = current + in.Delta
		if onHand < 0 && !s.allowNegativeStock {
			return fmt.Errorf("%w: removing %d would leave %d on hand; only %d is available",
				core.ErrInvalid, -in.Delta, onHand, current)
		}

		unitCost := in.UnitCost
		if unitCost.IsZero() {
			// An unpriced movement is valued at the product's standing cost,
			// so a quick adjustment does not silently value stock at zero and
			// drag the whole valuation down with it.
			unitCost = product.Cost
		}

		movement := core.StockMovement{
			ProductID:  in.ProductID,
			LocationID: location,
			QtyDelta:   in.Delta,
			Reason:     in.Reason,
			Note:       in.Note,
			UnitCost:   unitCost,
			LotNumber:  strings.TrimSpace(in.LotNumber),
			ExpiryDate: in.ExpiryDate,
			ActorID:    in.ActorID,
			OccurredAt: s.now(),
		}
		if err := st.Movements().Append(ctx, &movement); err != nil {
			return err
		}

		// A receipt at a new price updates the standing cost, so the figure on
		// the product stays the one a buyer would quote today.
		if in.Delta > 0 && !in.UnitCost.IsZero() && in.UnitCost.Minor != product.Cost.Minor {
			product.Cost = in.UnitCost
			if err := st.Products().Update(ctx, &product); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	s.log.Info("stock moved",
		"product_id", in.ProductID, "location_id", location,
		"delta", in.Delta, "reason", in.Reason, "on_hand", onHand)
	return onHand, nil
}

// SetStockInput records the result of a physical count.
type SetStockInput struct {
	ProductID  core.ID
	LocationID core.ID
	// Counted is the quantity actually found on the shelf.
	Counted int64
	Note    string
	ActorID core.ID
}

// SetStock reconciles the system to a counted quantity by posting the variance
// as a stock_count movement. It never writes the level directly, so the
// difference between what the system believed and what was on the shelf stays
// visible in the ledger.
func (s *Inventory) SetStock(ctx context.Context, in SetStockInput) (int64, error) {
	if in.Counted < 0 {
		var v core.ValidationError
		v.Add("counted", "Counted quantity cannot be negative")
		return 0, v.ErrOrNil()
	}

	location := s.locationOrDefault(in.LocationID)
	current, err := s.store.Movements().OnHand(ctx, in.ProductID, location)
	if err != nil {
		return 0, err
	}
	if variance := in.Counted - current; variance != 0 {
		return s.AdjustStock(ctx, AdjustStockInput{
			ProductID:  in.ProductID,
			LocationID: location,
			Delta:      variance,
			Reason:     core.ReasonStockCount,
			Note:       in.Note,
			ActorID:    in.ActorID,
		})
	}
	return current, nil
}

// MovementHistory returns ledger entries, newest first by default.
func (s *Inventory) MovementHistory(ctx context.Context, f storage.MovementFilter) ([]core.StockMovement, error) {
	return s.store.Movements().List(ctx, f)
}

// VerifyStockLevels rebuilds the cached level for a product from the ledger.
// Exposed so support can repair a database without direct SQL access.
func (s *Inventory) VerifyStockLevels(ctx context.Context, productID core.ID) error {
	return s.store.Movements().Recompute(ctx, productID)
}

// --- locations and settings -------------------------------------------------

// Locations lists the active locations.
func (s *Inventory) Locations(ctx context.Context) ([]core.Location, error) {
	return s.store.Locations().List(ctx, false)
}

// DefaultLocation returns the location used when the caller does not name one.
func (s *Inventory) DefaultLocation(ctx context.Context) (core.Location, error) {
	loc, err := s.store.Locations().Default(ctx)
	if errors.Is(err, core.ErrNotFound) {
		return core.Location{}, fmt.Errorf(
			"no default location is configured; the database may not be fully migrated: %w", err)
	}
	return loc, err
}

// ValuationMethod reads the configured accounting policy.
func (s *Inventory) ValuationMethod(ctx context.Context) (core.ValuationMethod, error) {
	raw, err := s.store.Settings().Get(ctx, storage.SettingValuationMethod)
	if errors.Is(err, core.ErrNotFound) {
		return core.ValuationWeightedAverage, nil
	}
	if err != nil {
		return "", err
	}

	method := core.ValuationMethod(raw)
	if !method.Valid() {
		s.log.Warn("unknown valuation method in settings, falling back",
			"configured", raw, "using", core.ValuationWeightedAverage)
		return core.ValuationWeightedAverage, nil
	}
	return method, nil
}

// SetValuationMethod changes the accounting policy. It re-values history rather
// than only affecting stock received afterwards, so it is deliberately a
// setting an administrator changes rather than a per-report toggle.
func (s *Inventory) SetValuationMethod(ctx context.Context, method core.ValuationMethod) error {
	if !method.Valid() {
		return fmt.Errorf("%w: %q is not a supported valuation method", core.ErrInvalid, method)
	}
	if err := s.store.Settings().Set(ctx, storage.SettingValuationMethod, string(method)); err != nil {
		return err
	}

	s.log.Info("valuation method changed", "method", method)
	return nil
}

// Setting reads one stored setting, returning fallback when it is unset.
func (s *Inventory) Setting(ctx context.Context, key, fallback string) (string, error) {
	value, err := s.store.Settings().Get(ctx, key)
	if errors.Is(err, core.ErrNotFound) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// SetSetting writes one stored setting.
func (s *Inventory) SetSetting(ctx context.Context, key, value string) error {
	return s.store.Settings().Set(ctx, key, value)
}

func (s *Inventory) locationOrDefault(id core.ID) core.ID {
	if id.IsZero() {
		return core.DefaultLocationID
	}
	return id
}

// isUserSelectable reports whether a reason may be chosen by hand, as opposed
// to one the system posts as a side effect of a document.
func isUserSelectable(r core.MovementReason) bool {
	for _, allowed := range core.AdjustmentReasons {
		if r == allowed {
			return true
		}
	}
	return false
}

// Backupper exposes the store's backup capability when the backend has one,
// so the UI can offer the action without knowing which backend is configured.
func (s *Inventory) Backupper() (storage.Backupper, bool) {
	backupper, ok := s.store.(storage.Backupper)
	return backupper, ok
}
