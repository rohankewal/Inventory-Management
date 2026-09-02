package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// --- customers and stores ---------------------------------------------------

// CreateCustomer registers a client business.
func (s *Inventory) CreateCustomer(ctx context.Context, c core.Customer) (core.Customer, error) {
	if c.Currency == "" {
		c.Currency = s.defaultCurrency
	}
	c.Active = true

	if err := s.store.Customers().Create(ctx, &c); err != nil {
		return core.Customer{}, err
	}
	s.log.Info("customer created", "customer_id", c.ID, "code", c.Code)
	return c, nil
}

// UpdateCustomer saves an edit, failing if the record changed since it was read.
func (s *Inventory) UpdateCustomer(ctx context.Context, c core.Customer) (core.Customer, error) {
	if err := s.store.Customers().Update(ctx, &c); err != nil {
		return core.Customer{}, err
	}
	return c, nil
}

// CustomerPage is one page of a customer listing plus the unpaged total.
type CustomerPage struct {
	Items []core.CustomerWithStores
	Total int
}

// ListCustomers returns customers with their store and open-order counts.
func (s *Inventory) ListCustomers(ctx context.Context, f storage.CustomerFilter) (CustomerPage, error) {
	items, err := s.store.Customers().List(ctx, f)
	if err != nil {
		return CustomerPage{}, err
	}
	total, err := s.store.Customers().Count(ctx, f)
	if err != nil {
		return CustomerPage{}, err
	}
	return CustomerPage{Items: items, Total: total}, nil
}

// GetCustomer reads one client business.
func (s *Inventory) GetCustomer(ctx context.Context, id core.ID) (core.Customer, error) {
	return s.store.Customers().Get(ctx, id)
}

// ArchiveCustomer hides a client without deleting its history.
func (s *Inventory) ArchiveCustomer(ctx context.Context, id core.ID, archived bool) error {
	return s.store.Customers().SetActive(ctx, id, !archived)
}

// DeleteCustomer removes a client that has no orders. One that does is
// archived instead, since deleting it would orphan the paperwork.
func (s *Inventory) DeleteCustomer(ctx context.Context, id core.ID) (DeleteOutcome, error) {
	outcome := OutcomeDeleted

	err := s.store.InTx(ctx, func(st storage.Store) error {
		if _, err := st.Customers().Get(ctx, id); err != nil {
			return err
		}

		orders, err := st.Orders().Count(ctx, storage.OrderFilter{CustomerID: id})
		if err != nil {
			return err
		}
		if orders > 0 {
			outcome = OutcomeArchived
			return st.Customers().SetActive(ctx, id, false)
		}
		return st.Customers().Delete(ctx, id)
	})
	if err != nil {
		return "", err
	}

	s.log.Info("customer removed", "customer_id", id, "outcome", outcome)
	return outcome, nil
}

// CreateStore adds a ship-to destination to a customer.
func (s *Inventory) CreateStore(ctx context.Context, st core.CustomerStore) (core.CustomerStore, error) {
	st.Active = true
	if err := s.store.Stores().Create(ctx, &st); err != nil {
		return core.CustomerStore{}, err
	}
	s.log.Info("store created", "store_id", st.ID, "customer_id", st.CustomerID, "code", st.Code)
	return st, nil
}

// UpdateStore saves an edit to a ship-to destination.
func (s *Inventory) UpdateStore(ctx context.Context, st core.CustomerStore) (core.CustomerStore, error) {
	if err := s.store.Stores().Update(ctx, &st); err != nil {
		return core.CustomerStore{}, err
	}
	return st, nil
}

// ListStores returns a customer's ship-to destinations.
func (s *Inventory) ListStores(ctx context.Context, f storage.StoreFilter) ([]core.CustomerStore, error) {
	return s.store.Stores().List(ctx, f)
}

// GetStore reads one ship-to destination.
func (s *Inventory) GetStore(ctx context.Context, id core.ID) (core.CustomerStore, error) {
	return s.store.Stores().Get(ctx, id)
}

// ArchiveStore hides a store that has closed.
func (s *Inventory) ArchiveStore(ctx context.Context, id core.ID, archived bool) error {
	return s.store.Stores().SetActive(ctx, id, !archived)
}

// --- programs ---------------------------------------------------------------

// CreateProgram registers a buy agreed with a customer.
func (s *Inventory) CreateProgram(ctx context.Context, p core.Program) (core.Program, error) {
	if err := s.store.Programs().Create(ctx, &p); err != nil {
		return core.Program{}, err
	}
	s.log.Info("program created", "program_id", p.ID, "code", p.Code)
	return p, nil
}

// UpdateProgram saves an edit.
func (s *Inventory) UpdateProgram(ctx context.Context, p core.Program) (core.Program, error) {
	if err := s.store.Programs().Update(ctx, &p); err != nil {
		return core.Program{}, err
	}
	return p, nil
}

// ListPrograms returns the buys matching a filter.
func (s *Inventory) ListPrograms(ctx context.Context, f storage.ProgramFilter) ([]core.Program, error) {
	return s.store.Programs().List(ctx, f)
}

// GetProgram reads one buy.
func (s *Inventory) GetProgram(ctx context.Context, id core.ID) (core.Program, error) {
	return s.store.Programs().Get(ctx, id)
}

// --- orders -----------------------------------------------------------------

// SaveOrder creates or updates a store purchase order with its lines.
//
// Header and lines are written together: an order is one document, and saving
// it in two steps leaves a window where the paperwork disagrees with itself.
func (s *Inventory) SaveOrder(ctx context.Context, o core.StoreOrder, lines []core.StoreOrderLine) (core.StoreOrder, error) {
	if o.Currency == "" {
		o.Currency = s.defaultCurrency
	}

	// The store must belong to the customer, or the goods go to the wrong
	// company. A picker cannot catch this; the database will not either,
	// because both references are individually valid.
	store, err := s.store.Stores().Get(ctx, o.StoreID)
	if err != nil {
		return core.StoreOrder{}, err
	}
	if store.CustomerID != o.CustomerID {
		return core.StoreOrder{}, fmt.Errorf(
			"%w: store %s does not belong to that customer", core.ErrInvalid, store.Code)
	}
	if !o.ProgramID.IsZero() {
		program, err := s.store.Programs().Get(ctx, o.ProgramID)
		if err != nil {
			return core.StoreOrder{}, err
		}
		if program.CustomerID != o.CustomerID {
			return core.StoreOrder{}, fmt.Errorf(
				"%w: program %s belongs to a different customer", core.ErrInvalid, program.Code)
		}
	}

	if o.ID.IsZero() {
		if err := s.store.Orders().Create(ctx, &o, lines); err != nil {
			return core.StoreOrder{}, err
		}
		s.log.Info("order created",
			"order_id", o.ID, "po", o.CustomerPONumber, "store", store.Code, "lines", len(lines))
		return o, nil
	}

	err = s.store.InTx(ctx, func(st storage.Store) error {
		if err := st.Orders().Update(ctx, &o); err != nil {
			return err
		}
		return st.Orders().ReplaceLines(ctx, o.ID, lines)
	})
	if err != nil {
		return core.StoreOrder{}, err
	}

	s.log.Info("order updated", "order_id", o.ID, "po", o.CustomerPONumber)
	return o, nil
}

// SetOrderStatus moves an order through its lifecycle.
//
// Statuses that describe fulfilment progress are derived from the lines rather
// than set by hand, so that the screen can never claim an order shipped when
// nothing left the building.
func (s *Inventory) SetOrderStatus(ctx context.Context, id core.ID, status core.OrderStatus) error {
	if status == core.OrderPartial || status == core.OrderShipped {
		return fmt.Errorf(
			"%w: %q is set by shipping the order, not chosen by hand",
			core.ErrInvalid, status.Label())
	}
	if err := s.store.Orders().SetStatus(ctx, id, status); err != nil {
		return err
	}

	s.log.Info("order status changed", "order_id", id, "status", status)
	return nil
}

// ConfirmOrder acknowledges a draft to the client.
func (s *Inventory) ConfirmOrder(ctx context.Context, id core.ID) error {
	return s.SetOrderStatus(ctx, id, core.OrderConfirmed)
}

// CancelOrder withdraws an order that has not shipped.
func (s *Inventory) CancelOrder(ctx context.Context, id core.ID) error {
	detail, err := s.store.Orders().Detail(ctx, id)
	if err != nil {
		return err
	}
	if detail.Totals.Shipped > 0 {
		return fmt.Errorf(
			"%w: %s has already shipped %d unit(s) and cannot be cancelled outright",
			core.ErrConflict, detail.CustomerPONumber, detail.Totals.Shipped)
	}
	return s.SetOrderStatus(ctx, id, core.OrderCancelled)
}

// GetOrder returns an order with its customer, store, program and lines.
func (s *Inventory) GetOrder(ctx context.Context, id core.ID) (core.OrderDetail, error) {
	return s.store.Orders().Detail(ctx, id)
}

// OrderPage is one page of an order listing plus the unpaged total.
type OrderPage struct {
	Items []core.OrderSummary
	Total int
}

// ListOrders returns a filtered, sorted, paged order listing.
func (s *Inventory) ListOrders(ctx context.Context, f storage.OrderFilter) (OrderPage, error) {
	items, err := s.store.Orders().List(ctx, f)
	if err != nil {
		return OrderPage{}, err
	}
	total, err := s.store.Orders().Count(ctx, f)
	if err != nil {
		return OrderPage{}, err
	}
	return OrderPage{Items: items, Total: total}, nil
}

// DeleteOrder removes an order that has never shipped.
func (s *Inventory) DeleteOrder(ctx context.Context, id core.ID) error {
	detail, err := s.store.Orders().Detail(ctx, id)
	if err != nil {
		return err
	}
	if detail.Totals.Shipped > 0 {
		return fmt.Errorf(
			"%w: %s has shipped and must be kept for the record; cancel it instead",
			core.ErrConflict, detail.CustomerPONumber)
	}

	if err := s.store.Orders().Delete(ctx, id); err != nil {
		return err
	}
	s.log.Info("order deleted", "order_id", id, "po", detail.CustomerPONumber)
	return nil
}

// --- lookup -----------------------------------------------------------------

// LookupResult is what the global search box resolved a term to.
type LookupResult struct {
	Product *core.ProductWithStock
	Orders  []core.OrderSummary
}

// Found reports whether anything matched.
func (r LookupResult) Found() bool { return r.Product != nil || len(r.Orders) > 0 }

// Lookup resolves a scanned or typed code to whatever it identifies.
//
// It tries a customer PO number first, then a barcode, then a SKU. PO numbers
// come first because that is what somebody reading an email or a packing slip
// is most often holding, and because a PO number is the one identifier a client
// will use on the phone.
func (s *Inventory) Lookup(ctx context.Context, code string, locationID core.ID) (LookupResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return LookupResult{}, fmt.Errorf("look up: %w: nothing to search for", core.ErrInvalid)
	}

	orders, err := s.store.Orders().FindByPONumber(ctx, code)
	if err != nil {
		return LookupResult{}, err
	}
	if len(orders) > 0 {
		summaries, err := s.store.Orders().List(ctx, storage.OrderFilter{Search: code, Limit: 25})
		if err != nil {
			return LookupResult{}, err
		}
		// Keep only exact PO matches; the search filter is deliberately looser
		// than an identifier lookup should be.
		var exact []core.OrderSummary
		for _, summary := range summaries {
			if strings.EqualFold(summary.CustomerPONumber, code) {
				exact = append(exact, summary)
			}
		}
		if len(exact) > 0 {
			return LookupResult{Orders: exact}, nil
		}
	}

	product, err := s.LookupByCode(ctx, code, locationID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return LookupResult{}, fmt.Errorf(
				"nothing matches %q — not a PO number, barcode or SKU: %w", code, core.ErrNotFound)
		}
		return LookupResult{}, err
	}
	return LookupResult{Product: &product}, nil
}

// --- order insight ----------------------------------------------------------

// OrderBook summarises open demand, for the dashboard.
type OrderBook struct {
	OpenOrders   int
	OpenUnits    int64
	OpenValue    core.Money
	Late         int
	ShippingSoon int
	// Customers is how many clients have open orders.
	Customers int
}

// ShippingSoonWindow is how far ahead the dashboard counts orders as imminent.
const ShippingSoonWindow = 14 * 24 * time.Hour

// OrderBookSummary computes the open-demand figures.
func (s *Inventory) OrderBookSummary(ctx context.Context) (OrderBook, error) {
	open, err := s.store.Orders().List(ctx, storage.OrderFilter{OpenOnly: true})
	if err != nil {
		return OrderBook{}, err
	}

	now := s.now()
	soonest := now.Add(ShippingSoonWindow)

	book := OrderBook{OpenValue: core.Zero(s.defaultCurrency)}
	customers := map[core.ID]bool{}

	for _, order := range open {
		book.OpenOrders++
		book.OpenUnits += order.Totals.Outstanding
		customers[order.CustomerID] = true

		if order.Totals.Value.Currency == book.OpenValue.Currency {
			book.OpenValue.Minor += order.Totals.Value.Minor
		}
		if order.Late(now) {
			book.Late++
		}
		if !order.RequestedShipDate.IsZero() && order.RequestedShipDate.Before(soonest) {
			book.ShippingSoon++
		}
	}
	book.Customers = len(customers)

	return book, nil
}

// CustomerByCode resolves a client by the short code used on documents, which
// is how the admin CLI names one.
func (s *Inventory) CustomerByCode(ctx context.Context, code string) (core.Customer, error) {
	return s.store.Customers().GetByCode(ctx, code)
}
