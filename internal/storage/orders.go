package storage

import (
	"context"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// CustomerFilter selects and pages a customer listing.
type CustomerFilter struct {
	// Search matches code or name, case-insensitively, as a substring.
	Search          string
	IncludeInactive bool
	Limit           int
	Offset          int
}

// CustomerRepo stores client businesses and their ship-to stores.
type CustomerRepo interface {
	Create(ctx context.Context, c *core.Customer) error
	Update(ctx context.Context, c *core.Customer) error
	SetActive(ctx context.Context, id core.ID, active bool) error

	Get(ctx context.Context, id core.ID) (core.Customer, error)
	GetByCode(ctx context.Context, code string) (core.Customer, error)

	// List returns customers with their store and open-order counts, which is
	// what the customer screen shows and what would otherwise be a query per
	// row.
	List(ctx context.Context, f CustomerFilter) ([]core.CustomerWithStores, error)
	Count(ctx context.Context, f CustomerFilter) (int, error)

	// Delete removes a customer outright. It fails once any order refers to
	// the customer; deactivate instead.
	Delete(ctx context.Context, id core.ID) error
}

// StoreFilter selects a customer's stores.
type StoreFilter struct {
	CustomerID      core.ID
	Search          string
	IncludeInactive bool
	Limit           int
	Offset          int
}

// StoreRepo stores customer ship-to destinations.
type StoreRepo interface {
	Create(ctx context.Context, s *core.CustomerStore) error
	Update(ctx context.Context, s *core.CustomerStore) error
	SetActive(ctx context.Context, id core.ID, active bool) error

	Get(ctx context.Context, id core.ID) (core.CustomerStore, error)
	// GetByCode resolves a store within one customer, since store codes repeat
	// across customers.
	GetByCode(ctx context.Context, customerID core.ID, code string) (core.CustomerStore, error)

	List(ctx context.Context, f StoreFilter) ([]core.CustomerStore, error)
	Count(ctx context.Context, f StoreFilter) (int, error)

	Delete(ctx context.Context, id core.ID) error
}

// ProgramFilter selects programs.
type ProgramFilter struct {
	CustomerID core.ID
	Status     core.ProgramStatus
	Search     string
	// OpenOnly excludes closed and cancelled programs.
	OpenOnly bool
	Limit    int
	Offset   int
}

// ProgramRepo stores the buys agreed with customers.
type ProgramRepo interface {
	Create(ctx context.Context, p *core.Program) error
	Update(ctx context.Context, p *core.Program) error

	Get(ctx context.Context, id core.ID) (core.Program, error)
	GetByCode(ctx context.Context, customerID core.ID, code string) (core.Program, error)

	List(ctx context.Context, f ProgramFilter) ([]core.Program, error)
	Count(ctx context.Context, f ProgramFilter) (int, error)

	Delete(ctx context.Context, id core.ID) error
}

// OrderSort names a sortable column on the orders screen.
type OrderSort string

const (
	SortOrderPONumber   OrderSort = "po_number"
	SortOrderCustomer   OrderSort = "customer"
	SortOrderStore      OrderSort = "store"
	SortOrderStatus     OrderSort = "status"
	SortOrderShipDate   OrderSort = "requested_ship_date"
	SortOrderCancelDate OrderSort = "cancel_after_date"
	SortOrderOrderedAt  OrderSort = "ordered_at"
	SortOrderValue      OrderSort = "value"
	SortOrderUnits      OrderSort = "units"
)

// Valid reports whether s is a sort key this build supports.
func (s OrderSort) Valid() bool {
	switch s {
	case SortOrderPONumber, SortOrderCustomer, SortOrderStore, SortOrderStatus,
		SortOrderShipDate, SortOrderCancelDate, SortOrderOrderedAt,
		SortOrderValue, SortOrderUnits:
		return true
	}
	return false
}

// OrderFilter selects and pages an order listing.
type OrderFilter struct {
	// Search matches the customer's PO number, the store code or name, or the
	// customer name.
	Search string

	CustomerID core.ID
	StoreID    core.ID
	ProgramID  core.ID
	Status     core.OrderStatus

	// OpenOnly limits results to orders still being worked on, which is the
	// default view: a screen that opens on three years of shipped history is
	// useless.
	OpenOnly bool
	// ShipBefore and ShipAfter bound the requested ship date.
	ShipBefore time.Time
	ShipAfter  time.Time
	// LateOnly selects orders whose cancel date has passed while they are
	// still open — the ones that lose money.
	LateOnly bool

	Sort      OrderSort
	Direction SortDirection
	Limit     int
	Offset    int
}

// OrderRepo stores store purchase orders and their lines.
type OrderRepo interface {
	// Create inserts the order and its lines in one call, because an order
	// with no lines is not a document anybody wants.
	Create(ctx context.Context, o *core.StoreOrder, lines []core.StoreOrderLine) error
	// Update writes the header only; lines are replaced with ReplaceLines.
	Update(ctx context.Context, o *core.StoreOrder) error
	// ReplaceLines swaps the whole line set, which is how an edited order is
	// saved. Lines already shipped against cannot be removed.
	ReplaceLines(ctx context.Context, orderID core.ID, lines []core.StoreOrderLine) error
	SetStatus(ctx context.Context, id core.ID, status core.OrderStatus) error

	Get(ctx context.Context, id core.ID) (core.StoreOrder, error)
	// GetByPONumber resolves the client's own reference within one customer.
	GetByPONumber(ctx context.Context, customerID core.ID, poNumber string) (core.StoreOrder, error)
	// FindByPONumber resolves a PO number across every customer, for the global
	// search box where the person typing does not say which client it is.
	FindByPONumber(ctx context.Context, poNumber string) ([]core.StoreOrder, error)

	// Detail returns the order with its customer, store, program and lines
	// resolved, which is what the detail screen and every document need.
	Detail(ctx context.Context, id core.ID) (core.OrderDetail, error)

	List(ctx context.Context, f OrderFilter) ([]core.OrderSummary, error)
	Count(ctx context.Context, f OrderFilter) (int, error)

	Delete(ctx context.Context, id core.ID) error
}
