package core

import (
	"time"
	"unicode/utf8"
)

// MovementReason explains why stock changed. Every ledger row carries one, so
// a variance can always be traced back to the operation that caused it.
type MovementReason string

const (
	ReasonOpeningBalance MovementReason = "opening_balance"
	ReasonAdjustment     MovementReason = "adjustment"
	ReasonReceipt        MovementReason = "receipt"
	ReasonSale           MovementReason = "sale"
	ReasonReturn         MovementReason = "return"
	ReasonTransferOut    MovementReason = "transfer_out"
	ReasonTransferIn     MovementReason = "transfer_in"
	ReasonStockCount     MovementReason = "stock_count"
	ReasonWriteOff       MovementReason = "write_off"
)

// Valid reports whether r is a reason this build knows about.
func (r MovementReason) Valid() bool {
	switch r {
	case ReasonOpeningBalance, ReasonAdjustment, ReasonReceipt, ReasonSale,
		ReasonReturn, ReasonTransferOut, ReasonTransferIn, ReasonStockCount,
		ReasonWriteOff:
		return true
	}
	return false
}

// Label is the human-readable form shown in the UI.
func (r MovementReason) Label() string {
	switch r {
	case ReasonOpeningBalance:
		return "Opening balance"
	case ReasonAdjustment:
		return "Manual adjustment"
	case ReasonReceipt:
		return "Goods received"
	case ReasonSale:
		return "Sale"
	case ReasonReturn:
		return "Return"
	case ReasonTransferOut:
		return "Transfer out"
	case ReasonTransferIn:
		return "Transfer in"
	case ReasonStockCount:
		return "Stock count"
	case ReasonWriteOff:
		return "Write-off"
	}
	return string(r)
}

// AdjustmentReasons are the reasons a user may pick directly. The others are
// written by the system as a side effect of a document (a receipt, a transfer)
// and must not be selectable by hand, or the ledger stops reconciling with the
// documents it is supposed to explain.
var AdjustmentReasons = []MovementReason{
	ReasonAdjustment,
	ReasonStockCount,
	ReasonWriteOff,
	ReasonReturn,
}

// StockMovement is one append-only entry in the stock ledger. Rows are never
// updated or deleted; a mistake is corrected by posting an offsetting entry.
type StockMovement struct {
	ID         ID
	ProductID  ID
	LocationID ID
	QtyDelta   int64 // signed: positive receives stock, negative removes it
	Reason     MovementReason
	Note       string

	// UnitCost is what one unit cost on an incoming movement. Carrying it on
	// the ledger row is what makes both weighted-average and FIFO valuation
	// computable after the fact: a single cost field on the product can only
	// ever answer "what does it cost now", never "what is this shelf worth".
	UnitCost Money

	// LotNumber and ExpiryDate are recorded for products with TrackLots set,
	// which is what makes a recall answerable. Full lot-level allocation
	// arrives with multi-location transfers.
	LotNumber  string
	ExpiryDate time.Time

	// RefType and RefID link the movement back to the document that caused it
	// (a purchase order, a transfer, a sales order), empty for manual entries.
	RefType string
	RefID   ID

	ActorID    ID
	OccurredAt time.Time
	CreatedAt  time.Time
}

// Validate checks the movement before it is appended to the ledger.
func (m *StockMovement) Validate() error {
	var v ValidationError

	if m.ProductID.IsZero() {
		v.Add("product_id", "Movement must reference a product")
	}
	if m.LocationID.IsZero() {
		v.Add("location_id", "Movement must reference a location")
	}
	if m.QtyDelta == 0 {
		v.Add("quantity", "Quantity change cannot be zero")
	}
	if !m.Reason.Valid() {
		v.Add("reason", "%q is not a known movement reason", m.Reason)
	}
	if utf8.RuneCountInString(m.Note) > MaxNoteLen {
		v.Add("note", "Note must be %d characters or fewer", MaxNoteLen)
	}
	if utf8.RuneCountInString(m.LotNumber) > MaxSKULen {
		v.Add("lot_number", "Lot number must be %d characters or fewer", MaxSKULen)
	}
	if m.UnitCost.IsNegative() {
		v.Add("unit_cost", "Unit cost cannot be negative")
	}

	return v.ErrOrNil()
}

// IsInbound reports whether the movement adds stock.
func (m StockMovement) IsInbound() bool { return m.QtyDelta > 0 }

// ExtendedCost is the movement's quantity valued at its unit cost.
func (m StockMovement) ExtendedCost() Money {
	quantity := m.QtyDelta
	if quantity < 0 {
		quantity = -quantity
	}
	return m.UnitCost.MulQty(quantity)
}

// StockLevel is the cached on-hand quantity for one product at one location.
// It is maintained inside the same transaction as the ledger write, so reads
// stay fast without the ledger ever ceasing to be the source of truth.
type StockLevel struct {
	ProductID  ID
	LocationID ID
	OnHand     int64
	UpdatedAt  time.Time
}
