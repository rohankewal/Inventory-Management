package core

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Field length limits. They exist so a paste accident cannot write a megabyte
// into a column that every list query reads.
const (
	MaxSKULen         = 64
	MaxBarcodeLen     = 64
	MaxNameLen        = 200
	MaxCategoryLen    = 100
	MaxSupplierLen    = 200
	MaxDescriptionLen = 2000
	MaxNotesLen       = 5000
	MaxNoteLen        = 1000
)

// StockStatus summarises where a product sits against its reorder point. It is
// the single most-read piece of information on an inventory screen, so it is a
// computed type rather than something each view re-derives.
type StockStatus string

const (
	StatusOutOfStock StockStatus = "out_of_stock"
	StatusLow        StockStatus = "low"
	StatusOK         StockStatus = "ok"
	StatusUntracked  StockStatus = "untracked"
)

// Label is the human-readable form.
func (s StockStatus) Label() string {
	switch s {
	case StatusOutOfStock:
		return "Out of stock"
	case StatusLow:
		return "Low"
	case StatusOK:
		return "In stock"
	case StatusUntracked:
		return "Not tracked"
	}
	return string(s)
}

// Product is a stock-keeping item.
//
// It deliberately carries no quantity field. On-hand stock is derived from the
// StockMovement ledger, so that every change to a level has a recorded reason,
// actor and timestamp. See movement.go.
type Product struct {
	ID      ID
	SKU     string
	Barcode string // UPC, EAN or any scanned code; unique when set
	Name    string

	Description string
	Category    string
	Supplier    string // free text until Phase 4 introduces supplier records
	Tags        Tags
	Notes       string

	// Price is what the item sells for. Cost is what it was last bought for
	// and is what valuation and margin are calculated from — a system that
	// only knows the sale price cannot tell you what the shelf is worth.
	Price Money
	Cost  Money

	Unit UnitOfMeasure

	// NonStock marks services and other items that belong in the catalogue and
	// on documents but have no meaningful quantity.
	//
	// The flag is negative on purpose. Almost every product is stocked, so the
	// zero value of a Product must be a stocked one; a positive StockTracked
	// field would silently make every struct literal a service.
	NonStock bool
	// TrackLots records a lot or batch number against every movement, which
	// food, cosmetics and pharmaceuticals need for recall traceability.
	TrackLots bool

	// ReorderPoint is the level at or below which the item is flagged for
	// reordering. ReorderQuantity is how much to buy when that happens.
	ReorderPoint    int64
	ReorderQuantity int64

	ImagePath    string
	CustomFields CustomFields
	WeightGrams  int64

	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time

	// Version increments on every update. Writers pass the version they read
	// and the store rejects the write if it has moved on, which is what stops
	// two clerks on a shared Postgres from silently overwriting each other.
	Version int64
}

// ProductWithStock pairs a product with the derived figures every list and
// detail screen needs, so a view never has to issue a query per row.
type ProductWithStock struct {
	Product
	OnHand int64
	// LastMovementAt is when stock last moved, which is what dead-stock and
	// aging reports are built on. Zero means it has never moved.
	LastMovementAt time.Time
}

// TracksStock reports whether the product has a meaningful quantity.
func (p Product) TracksStock() bool { return !p.NonStock }

// Status reports where the product sits against its reorder point.
func (p ProductWithStock) Status() StockStatus {
	switch {
	case p.NonStock:
		return StatusUntracked
	case p.OnHand <= 0:
		return StatusOutOfStock
	case p.ReorderPoint > 0 && p.OnHand <= p.ReorderPoint:
		return StatusLow
	}
	return StatusOK
}

// NeedsReorder reports whether the item has fallen to its reorder point.
func (p ProductWithStock) NeedsReorder() bool {
	if p.NonStock {
		return false
	}
	return p.OnHand <= p.ReorderPoint && (p.ReorderPoint > 0 || p.OnHand <= 0)
}

// SuggestedOrderQuantity is how much to buy to clear the reorder point.
//
// It prefers the configured reorder quantity, but never suggests an amount
// that would still leave the item short — ordering the standard pack size is
// pointless if demand has already eaten through two of them.
func (p ProductWithStock) SuggestedOrderQuantity() int64 {
	if !p.NeedsReorder() {
		return 0
	}

	shortfall := p.ReorderPoint - p.OnHand
	if shortfall < 0 {
		shortfall = 0
	}
	if p.ReorderQuantity <= 0 {
		if shortfall == 0 {
			return 1
		}
		return shortfall
	}

	// Round up to whole multiples of the reorder quantity.
	quantity := p.ReorderQuantity
	for quantity < shortfall {
		quantity += p.ReorderQuantity
	}
	return quantity
}

// StockValue is the on-hand quantity valued at unit cost.
func (p ProductWithStock) StockValue() Money {
	return p.Cost.MulQty(p.OnHand)
}

// Margin is the difference between sale price and cost.
func (p Product) Margin() Money {
	margin, err := p.Price.Sub(p.Cost)
	if err != nil {
		// Mixed currencies cannot be subtracted; report no margin rather than
		// a wrong one.
		return Zero(p.Price.Currency)
	}
	return margin
}

// MarginPercent is the margin as a percentage of the sale price, rounded to
// one decimal place. It returns 0 when there is no price to divide by.
func (p Product) MarginPercent() float64 {
	if p.Price.Minor == 0 || p.Price.Currency != p.Cost.Currency {
		return 0
	}
	return float64(p.Price.Minor-p.Cost.Minor) / float64(p.Price.Minor) * 100
}

// Normalize trims incidental whitespace and applies defaults before validation
// and storage.
func (p *Product) Normalize() {
	p.SKU = strings.TrimSpace(p.SKU)
	p.Barcode = strings.TrimSpace(p.Barcode)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Category = strings.TrimSpace(p.Category)
	p.Supplier = strings.TrimSpace(p.Supplier)
	p.Notes = strings.TrimSpace(p.Notes)
	p.ImagePath = strings.TrimSpace(p.ImagePath)

	p.Unit = p.Unit.Normalize()
	p.Tags = ParseTags(p.Tags.String())

	if p.Price.Currency == "" {
		p.Price.Currency = DefaultCurrency
	}
	p.Price.Currency = p.Price.Currency.Normalize()

	// Cost follows the sale price's currency unless it was given one, so a
	// half-filled form cannot produce a product whose two amounts cannot be
	// compared.
	if p.Cost.Currency == "" {
		p.Cost.Currency = p.Price.Currency
	}
	p.Cost.Currency = p.Cost.Currency.Normalize()

	if p.ReorderPoint < 0 {
		p.ReorderPoint = 0
	}
	if p.ReorderQuantity < 0 {
		p.ReorderQuantity = 0
	}
}

// Validate reports every problem with the product at once.
func (p *Product) Validate() error {
	var v ValidationError

	if p.SKU == "" {
		v.Add("sku", "SKU is required")
	} else if utf8.RuneCountInString(p.SKU) > MaxSKULen {
		v.Add("sku", "SKU must be %d characters or fewer", MaxSKULen)
	}

	if utf8.RuneCountInString(p.Barcode) > MaxBarcodeLen {
		v.Add("barcode", "Barcode must be %d characters or fewer", MaxBarcodeLen)
	}

	if p.Name == "" {
		v.Add("name", "Name is required")
	} else if utf8.RuneCountInString(p.Name) > MaxNameLen {
		v.Add("name", "Name must be %d characters or fewer", MaxNameLen)
	}

	if utf8.RuneCountInString(p.Category) > MaxCategoryLen {
		v.Add("category", "Category must be %d characters or fewer", MaxCategoryLen)
	}
	if utf8.RuneCountInString(p.Supplier) > MaxSupplierLen {
		v.Add("supplier", "Supplier must be %d characters or fewer", MaxSupplierLen)
	}
	if utf8.RuneCountInString(p.Description) > MaxDescriptionLen {
		v.Add("description", "Description must be %d characters or fewer", MaxDescriptionLen)
	}
	if utf8.RuneCountInString(p.Notes) > MaxNotesLen {
		v.Add("notes", "Notes must be %d characters or fewer", MaxNotesLen)
	}

	if p.Price.IsNegative() {
		v.Add("price", "Price cannot be negative")
	}
	if !p.Price.Currency.Valid() {
		v.Add("price", "%q is not a valid currency code", p.Price.Currency)
	}
	if p.Cost.IsNegative() {
		v.Add("cost", "Cost cannot be negative")
	}
	// An unset cost currency means "the same as the price", which Normalize
	// fills in. Only a currency that was actually chosen, and differs, is a
	// problem worth reporting — otherwise a half-built struct fails validation
	// for a reason the user never expressed.
	if p.Cost.Currency != "" {
		if !p.Cost.Currency.Valid() {
			v.Add("cost", "%q is not a valid currency code", p.Cost.Currency)
		} else if p.Price.Currency != "" && p.Price.Currency != p.Cost.Currency {
			v.Add("cost", "Cost must be in the same currency as the price (%s)", p.Price.Currency)
		}
	}

	if p.ReorderQuantity > 0 && p.ReorderPoint == 0 {
		v.Add("reorder_point", "Set a reorder point, or the reorder quantity will never be used")
	}

	if len(p.CustomFields) > MaxCustomFields {
		v.Add("custom_fields", "A product may have at most %d custom fields", MaxCustomFields)
	}
	for key := range p.CustomFields {
		if strings.TrimSpace(key) == "" {
			v.Add("custom_fields", "Custom field names cannot be blank")
			break
		}
	}

	if p.WeightGrams < 0 {
		v.Add("weight", "Weight cannot be negative")
	}

	return v.ErrOrNil()
}
