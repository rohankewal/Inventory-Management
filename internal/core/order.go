package core

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Field limits for programs and orders.
const (
	MaxProgramCodeLen = 32
	MaxSeasonLen      = 60
	MaxPONumberLen    = 64
)

// ProgramStatus tracks a buy from agreement through to delivery.
type ProgramStatus string

const (
	ProgramDraft        ProgramStatus = "draft"
	ProgramSourcing     ProgramStatus = "sourcing"
	ProgramConfirmed    ProgramStatus = "confirmed"
	ProgramInProduction ProgramStatus = "in_production"
	ProgramShipping     ProgramStatus = "shipping"
	ProgramReceiving    ProgramStatus = "receiving"
	ProgramDelivering   ProgramStatus = "delivering"
	ProgramClosed       ProgramStatus = "closed"
	ProgramCancelled    ProgramStatus = "cancelled"
)

// ProgramStatuses lists the statuses in lifecycle order.
var ProgramStatuses = []ProgramStatus{
	ProgramDraft, ProgramSourcing, ProgramConfirmed, ProgramInProduction,
	ProgramShipping, ProgramReceiving, ProgramDelivering, ProgramClosed,
	ProgramCancelled,
}

// Label is the human-readable form.
func (s ProgramStatus) Label() string {
	switch s {
	case ProgramDraft:
		return "Draft"
	case ProgramSourcing:
		return "Sourcing"
	case ProgramConfirmed:
		return "Confirmed"
	case ProgramInProduction:
		return "In production"
	case ProgramShipping:
		return "Shipping"
	case ProgramReceiving:
		return "Receiving"
	case ProgramDelivering:
		return "Delivering to stores"
	case ProgramClosed:
		return "Closed"
	case ProgramCancelled:
		return "Cancelled"
	}
	return string(s)
}

// Valid reports whether the status is one this build knows.
func (s ProgramStatus) Valid() bool {
	for _, known := range ProgramStatuses {
		if s == known {
			return true
		}
	}
	return false
}

// Program is a buy agreed with a customer: the thing the client and the company
// sat down and specified.
//
// It is the spine that ties client demand to sourcing. Every store order and,
// later, every vendor order and container, hangs off one, which is what makes
// "where is the product for store 47" answerable in a single query rather than
// a chain of emails.
type Program struct {
	ID         ID
	CustomerID ID
	Code       string // e.g. "FW26-THROWS"
	Name       string
	Season     string // e.g. "Fall/Winter 2026"
	Status     ProgramStatus

	// TargetDeliveryDate is what was promised to the client. It is the date
	// every downstream lead time is worked back from.
	TargetDeliveryDate time.Time
	Notes              string

	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Normalize trims and upper-cases the code.
func (p *Program) Normalize() {
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Name = strings.TrimSpace(p.Name)
	p.Season = strings.TrimSpace(p.Season)
	p.Notes = strings.TrimSpace(p.Notes)
	if p.Status == "" {
		p.Status = ProgramDraft
	}
}

// Validate reports every problem at once.
func (p *Program) Validate() error {
	var v ValidationError

	if p.CustomerID.IsZero() {
		v.Add("customer_id", "A program must belong to a customer")
	}
	if p.Code == "" {
		v.Add("code", "Program code is required")
	} else if utf8.RuneCountInString(p.Code) > MaxProgramCodeLen {
		v.Add("code", "Program code must be %d characters or fewer", MaxProgramCodeLen)
	}
	if p.Name == "" {
		v.Add("name", "Program name is required")
	} else if utf8.RuneCountInString(p.Name) > MaxCustomerNameLen {
		v.Add("name", "Program name must be %d characters or fewer", MaxCustomerNameLen)
	}
	if utf8.RuneCountInString(p.Season) > MaxSeasonLen {
		v.Add("season", "Season must be %d characters or fewer", MaxSeasonLen)
	}
	if !p.Status.Valid() {
		v.Add("status", "%q is not a known program status", p.Status)
	}
	if utf8.RuneCountInString(p.Notes) > MaxNotesLen {
		v.Add("notes", "Notes must be %d characters or fewer", MaxNotesLen)
	}

	return v.ErrOrNil()
}

// OrderStatus tracks one store's purchase order.
type OrderStatus string

const (
	// OrderDraft has been entered but not acknowledged to the client.
	OrderDraft OrderStatus = "draft"
	// OrderConfirmed has been acknowledged; the client is expecting it.
	OrderConfirmed OrderStatus = "confirmed"
	// OrderPartial has been shipped in part.
	OrderPartial OrderStatus = "partially_shipped"
	// OrderShipped has been shipped in full.
	OrderShipped OrderStatus = "shipped"
	// OrderCancelled was withdrawn before shipping.
	OrderCancelled OrderStatus = "cancelled"
	// OrderClosed is finished and no longer worked on.
	OrderClosed OrderStatus = "closed"
)

// OrderStatuses lists the statuses in lifecycle order.
var OrderStatuses = []OrderStatus{
	OrderDraft, OrderConfirmed, OrderPartial, OrderShipped, OrderCancelled, OrderClosed,
}

// Label is the human-readable form.
func (s OrderStatus) Label() string {
	switch s {
	case OrderDraft:
		return "Draft"
	case OrderConfirmed:
		return "Confirmed"
	case OrderPartial:
		return "Partially shipped"
	case OrderShipped:
		return "Shipped"
	case OrderCancelled:
		return "Cancelled"
	case OrderClosed:
		return "Closed"
	}
	return string(s)
}

// Valid reports whether the status is one this build knows.
func (s OrderStatus) Valid() bool {
	for _, known := range OrderStatuses {
		if s == known {
			return true
		}
	}
	return false
}

// Open reports whether the order is still being worked on, which is what the
// default filter on the orders screen shows.
func (s OrderStatus) Open() bool {
	return s == OrderDraft || s == OrderConfirmed || s == OrderPartial
}

// StoreOrder is one purchase order raised by a customer for one of its stores.
//
// One order has exactly one ship-to store. Clients that operate many stores
// raise a separate PO per store — Macy's sending MCY-0123 for one and MCY-0124
// for the next — and a Program groups them so head office can see the whole buy.
type StoreOrder struct {
	ID         ID
	CustomerID ID
	StoreID    ID
	ProgramID  ID // optional

	// CustomerPONumber is the client's own reference, and is what everyone
	// actually says out loud. It is unique within the customer and is the
	// first thing the search box resolves.
	CustomerPONumber string

	Status   OrderStatus
	Currency Currency

	OrderedAt time.Time
	// RequestedShipDate is when the client wants it to leave.
	RequestedShipDate time.Time
	// CancelAfterDate is the date past which the client will refuse the
	// delivery. Retailers enforce these strictly, so it drives the urgency
	// ordering on the orders screen.
	CancelAfterDate time.Time

	Notes string

	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Normalize trims and upper-cases the PO number.
func (o *StoreOrder) Normalize() {
	o.CustomerPONumber = strings.ToUpper(strings.TrimSpace(o.CustomerPONumber))
	o.Notes = strings.TrimSpace(o.Notes)
	if o.Status == "" {
		o.Status = OrderDraft
	}
	if o.Currency == "" {
		o.Currency = DefaultCurrency
	}
	o.Currency = o.Currency.Normalize()
}

// Validate reports every problem at once.
func (o *StoreOrder) Validate() error {
	var v ValidationError

	if o.CustomerID.IsZero() {
		v.Add("customer_id", "An order must belong to a customer")
	}
	if o.StoreID.IsZero() {
		v.Add("store_id", "An order must name the store it ships to")
	}
	if o.CustomerPONumber == "" {
		v.Add("customer_po_number", "The customer's PO number is required")
	} else if utf8.RuneCountInString(o.CustomerPONumber) > MaxPONumberLen {
		v.Add("customer_po_number", "PO number must be %d characters or fewer", MaxPONumberLen)
	}
	if !o.Status.Valid() {
		v.Add("status", "%q is not a known order status", o.Status)
	}
	if !o.Currency.Valid() {
		v.Add("currency", "%q is not a valid currency code", o.Currency)
	}
	if utf8.RuneCountInString(o.Notes) > MaxNotesLen {
		v.Add("notes", "Notes must be %d characters or fewer", MaxNotesLen)
	}

	// A cancel date before the requested ship date is always a data-entry
	// error, and it would make every urgency calculation wrong.
	if !o.RequestedShipDate.IsZero() && !o.CancelAfterDate.IsZero() &&
		o.CancelAfterDate.Before(o.RequestedShipDate) {
		v.Add("cancel_after_date", "The cancel date cannot be before the requested ship date")
	}

	return v.ErrOrNil()
}

// StoreOrderLine is one product on one store's order.
type StoreOrderLine struct {
	ID        ID
	OrderID   ID
	ProductID ID
	LineNo    int

	Quantity  int64
	UnitPrice Money

	// AllocatedQty and ShippedQty are maintained as fulfilment progresses.
	// CancelledQty records quantity the client withdrew or that was short-shipped
	// and will not follow.
	AllocatedQty int64
	ShippedQty   int64
	CancelledQty int64

	Notes string
}

// Outstanding is what still has to ship.
func (l StoreOrderLine) Outstanding() int64 {
	remaining := l.Quantity - l.ShippedQty - l.CancelledQty
	if remaining < 0 {
		return 0
	}
	return remaining
}

// LineTotal is the ordered quantity at the agreed price.
func (l StoreOrderLine) LineTotal() Money { return l.UnitPrice.MulQty(l.Quantity) }

// Validate reports every problem at once.
func (l *StoreOrderLine) Validate() error {
	var v ValidationError

	if l.ProductID.IsZero() {
		v.Add("product_id", "An order line must name a product")
	}
	if l.Quantity <= 0 {
		v.Add("quantity", "Quantity must be greater than zero")
	}
	if l.UnitPrice.IsNegative() {
		v.Add("unit_price", "Price cannot be negative")
	}
	if l.ShippedQty < 0 || l.AllocatedQty < 0 || l.CancelledQty < 0 {
		v.Add("quantity", "Shipped, allocated and cancelled quantities cannot be negative")
	}
	if l.ShippedQty+l.CancelledQty > l.Quantity {
		v.Add("quantity", "Shipped and cancelled together cannot exceed the ordered quantity")
	}
	if utf8.RuneCountInString(l.Notes) > MaxNoteLen {
		v.Add("notes", "Notes must be %d characters or fewer", MaxNoteLen)
	}

	return v.ErrOrNil()
}

// OrderDetail is an order with everything a screen or document needs, so a
// view never has to issue a query per line.
type OrderDetail struct {
	StoreOrder
	Customer Customer
	Store    CustomerStore
	Program  *Program
	Lines    []OrderLineDetail
	Totals   OrderTotals
}

// OrderLineDetail pairs a line with the product it refers to.
type OrderLineDetail struct {
	StoreOrderLine
	SKU  string
	Name string
	Unit UnitOfMeasure
	// OnHand is the stock available at the fulfilling location, so a picker
	// can see at a glance whether the line can go.
	OnHand int64
}

// OrderTotals summarises an order's lines.
type OrderTotals struct {
	Lines       int
	Units       int64
	Shipped     int64
	Outstanding int64
	Cancelled   int64
	Value       Money
}

// Progress is the fraction of ordered units that have shipped, between 0 and 1.
func (t OrderTotals) Progress() float64 {
	if t.Units <= 0 {
		return 0
	}
	return float64(t.Shipped) / float64(t.Units)
}

// SummariseOrder computes totals from lines.
func SummariseOrder(lines []OrderLineDetail, currency Currency) OrderTotals {
	totals := OrderTotals{Lines: len(lines), Value: Zero(currency)}

	for _, line := range lines {
		totals.Units += line.Quantity
		totals.Shipped += line.ShippedQty
		totals.Cancelled += line.CancelledQty
		totals.Outstanding += line.Outstanding()

		if line.UnitPrice.Currency == currency {
			totals.Value.Minor += line.LineTotal().Minor
		}
	}
	return totals
}

// DaysUntil returns whole days from now until a date, negative once it has
// passed. It returns 0 for an unset date, which callers check separately.
func DaysUntil(date time.Time, now time.Time) int {
	if date.IsZero() {
		return 0
	}
	return int(date.Sub(now).Hours() / 24)
}

// OrderSummary is one row on the orders screen: the order plus the names and
// totals it is displayed with, resolved in the same query.
type OrderSummary struct {
	StoreOrder
	CustomerCode string
	CustomerName string
	StoreCode    string
	StoreName    string
	ProgramCode  string
	Totals       OrderTotals
}

// Late reports whether an open order has passed the date the client will
// refuse it.
func (o OrderSummary) Late(now time.Time) bool {
	if !o.Status.Open() || o.CancelAfterDate.IsZero() {
		return false
	}
	return o.CancelAfterDate.Before(now)
}

// DaysToShip is whole days until the requested ship date, negative once it has
// passed.
func (o OrderSummary) DaysToShip(now time.Time) int {
	return DaysUntil(o.RequestedShipDate, now)
}
