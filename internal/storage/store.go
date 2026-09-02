// Package storage defines the persistence contract the rest of the application
// programs against.
//
// Every backend implements these interfaces and is validated by the single
// conformance suite in storage/storetest. That suite is what keeps "SQLite for
// one user, Postgres for a team" an honest promise rather than a hope: a
// behaviour that differs between drivers fails the build.
package storage

import (
	"context"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// Store is the root handle. Repositories obtained from it share its underlying
// connection or transaction.
type Store interface {
	Products() ProductRepo
	Movements() MovementRepo
	Locations() LocationRepo
	Settings() SettingsRepo

	// The client side: who buys, where it ships, and against which PO.
	Customers() CustomerRepo
	Stores() StoreRepo
	Programs() ProgramRepo
	Orders() OrderRepo

	// InTx runs fn against a Store bound to a single transaction, committing
	// when fn returns nil and rolling back otherwise. Nested calls join the
	// enclosing transaction rather than opening a new one.
	InTx(ctx context.Context, fn func(Store) error) error

	// Ping verifies the backend is reachable.
	Ping(ctx context.Context) error

	// Dialect names the SQL dialect, matching the migrate package constants.
	Dialect() string

	Close() error
}

// SortDirection orders a listing.
type SortDirection string

const (
	Ascending  SortDirection = "asc"
	Descending SortDirection = "desc"
)

// ProductSort names a sortable column. Sort keys are an enum rather than a
// free string so that a column name can never reach SQL from user input.
type ProductSort string

const (
	SortProductName     ProductSort = "name"
	SortProductSKU      ProductSort = "sku"
	SortProductBarcode  ProductSort = "barcode"
	SortProductCategory ProductSort = "category"
	SortProductSupplier ProductSort = "supplier"
	SortProductPrice    ProductSort = "price"
	SortProductCost     ProductSort = "cost"
	SortProductStock    ProductSort = "stock"
	SortProductValue    ProductSort = "value"
	SortProductStatus   ProductSort = "status"
	SortProductCreated  ProductSort = "created_at"
	SortProductUpdated  ProductSort = "updated_at"
)

// Valid reports whether s is a sort key this build supports.
func (s ProductSort) Valid() bool {
	switch s {
	case SortProductName, SortProductSKU, SortProductBarcode,
		SortProductCategory, SortProductSupplier, SortProductPrice,
		SortProductCost, SortProductStock, SortProductValue,
		SortProductStatus, SortProductCreated, SortProductUpdated:
		return true
	}
	return false
}

// StockState narrows a listing to products in a particular stock condition.
type StockState string

const (
	// StockAny applies no stock condition.
	StockAny StockState = ""
	// StockNeedsReorder selects stocked items at or below their reorder point.
	// This is the reorder report, and it uses each product's own point rather
	// than one threshold across the catalogue — a threshold that is right for
	// screws is wrong for engines.
	StockNeedsReorder StockState = "needs_reorder"
	// StockOut selects tracked items with nothing on hand.
	StockOut StockState = "out"
	// StockInStock selects tracked items with something on hand.
	StockInStock StockState = "in_stock"
	// StockNegative selects tracked items below zero, which always indicates
	// a data problem worth surfacing.
	StockNegative StockState = "negative"
)

// ProductFilter selects and pages a product listing.
type ProductFilter struct {
	// Search matches SKU, barcode or name, case-insensitively, as a substring.
	Search string
	// IncludeInactive includes archived products. Off by default so archived
	// items stay out of everyday screens.
	IncludeInactive bool
	// LocationID scopes the on-hand quantity. Zero means the default location.
	LocationID core.ID

	// Category, Supplier and Tag are exact-match facets driven by the filter
	// bar. Empty means no restriction.
	Category string
	Supplier string
	Tag      string

	// Stock narrows by stock condition.
	Stock StockState

	// NonStockOnly selects non-stock items such as services.
	NonStockOnly bool

	Sort      ProductSort
	Direction SortDirection
	Limit     int
	Offset    int
}

// ProductRepo stores products.
type ProductRepo interface {
	// Create inserts p, returning core.ErrConflict if the SKU is taken.
	Create(ctx context.Context, p *core.Product) error

	// Update writes p only if the stored row still has p.Version, returning
	// core.ErrConflict when another client got there first. On success it
	// advances p.Version and p.UpdatedAt in place.
	Update(ctx context.Context, p *core.Product) error

	// Delete removes the row outright. It fails while ledger history refers to
	// the product; archive such products with SetActive instead.
	Delete(ctx context.Context, id core.ID) error

	// SetActive archives or restores a product.
	SetActive(ctx context.Context, id core.ID, active bool) error

	Get(ctx context.Context, id core.ID) (core.Product, error)
	GetBySKU(ctx context.Context, sku string) (core.Product, error)

	// GetByBarcode resolves a scanned code to a product. It is separate from
	// GetBySKU because a scanner reads whichever code is printed on the label,
	// and the two namespaces are allowed to overlap.
	GetByBarcode(ctx context.Context, barcode string) (core.Product, error)

	// Categories and Suppliers return the distinct values in use, for the
	// filter bar and for autocomplete on the edit form.
	Categories(ctx context.Context) ([]string, error)
	Suppliers(ctx context.Context) ([]string, error)
	// Tags returns every tag in use, sorted.
	Tags(ctx context.Context) ([]string, error)

	// List returns products with their on-hand quantity at the filter's
	// location, already sorted and paged.
	List(ctx context.Context, f ProductFilter) ([]core.ProductWithStock, error)

	// Count returns the total matching f, ignoring Limit and Offset, so the UI
	// can show "showing 50 of 8,312".
	Count(ctx context.Context, f ProductFilter) (int, error)
}

// MovementFilter selects and pages a ledger listing.
type MovementFilter struct {
	ProductID  core.ID
	LocationID core.ID
	Reason     core.MovementReason
	RefType    string
	RefID      core.ID

	// Direction orders by occurred_at. Newest first by default.
	Direction SortDirection
	Limit     int
	Offset    int
}

// MovementRepo appends to and reads the stock ledger.
type MovementRepo interface {
	// Append writes one ledger entry and updates the cached level for the
	// affected product and location in the same transaction.
	Append(ctx context.Context, m *core.StockMovement) error

	List(ctx context.Context, f MovementFilter) ([]core.StockMovement, error)
	Count(ctx context.Context, f MovementFilter) (int, error)

	// OnHand reads the cached level for one product at one location.
	OnHand(ctx context.Context, productID, locationID core.ID) (int64, error)

	// Levels returns every non-zero level for a product across locations.
	Levels(ctx context.Context, productID core.ID) ([]core.StockLevel, error)

	// Recompute rebuilds the cached levels for a product from the ledger. It
	// is the repair path if a cache row is ever wrong, and the assertion the
	// conformance suite uses to prove the cache matches the ledger.
	Recompute(ctx context.Context, productID core.ID) error

	// LastMovedAt returns, for each product with any history, when its stock
	// last moved. Aging and dead-stock reports need this for the whole
	// catalogue at once, and a query per product would make them unusable.
	LastMovedAt(ctx context.Context, locationID core.ID) (map[core.ID]time.Time, error)

	// CostHistory returns every movement carrying a cost for the given
	// products, oldest first, which is what valuation replays. Passing no
	// product ids returns the history for all of them.
	CostHistory(ctx context.Context, locationID core.ID, productIDs ...core.ID) (map[core.ID][]core.StockMovement, error)

	// ExpiringLots lists movements whose lot expires on or before the cutoff
	// and which still have stock behind them.
	ExpiringLots(ctx context.Context, before time.Time) ([]core.StockMovement, error)
}

// SettingsRepo stores configuration that belongs to the database rather than
// the machine. A valuation method is an accounting policy every workstation
// must agree on, not a per-user preference.
type SettingsRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	All(ctx context.Context) (map[string]string, error)
}

// Setting keys.
const (
	SettingValuationMethod = "valuation_method"
	SettingCompanyName     = "company_name"
	SettingLowStockDigest  = "low_stock_digest"
)

// LocationRepo stores locations.
type LocationRepo interface {
	Create(ctx context.Context, l *core.Location) error
	Update(ctx context.Context, l *core.Location) error
	Get(ctx context.Context, id core.ID) (core.Location, error)
	GetByCode(ctx context.Context, code string) (core.Location, error)

	// Default returns the location used when the caller does not name one.
	Default(ctx context.Context) (core.Location, error)

	// List returns locations ordered by code; inactive ones are included only
	// when includeInactive is set.
	List(ctx context.Context, includeInactive bool) ([]core.Location, error)
}
