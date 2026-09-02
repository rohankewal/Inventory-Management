package core

import (
	"sort"
	"time"
)

// ValuationMethod is how the worth of on-hand stock is calculated.
//
// The choice is an accounting policy, not a preference: it changes reported
// profit, and a business is expected to apply one consistently. Both supported
// methods are computed from the same ledger, so switching re-values history
// rather than only affecting stock received afterwards.
type ValuationMethod string

const (
	// ValuationWeightedAverage blends every receipt into one running average
	// cost. It smooths price swings and is the easier method to defend.
	ValuationWeightedAverage ValuationMethod = "weighted_average"
	// ValuationFIFO assumes the oldest stock is consumed first, so the value
	// on hand reflects the most recent purchase prices.
	ValuationFIFO ValuationMethod = "fifo"
)

// Label is the human-readable name.
func (m ValuationMethod) Label() string {
	switch m {
	case ValuationFIFO:
		return "FIFO (first in, first out)"
	case ValuationWeightedAverage:
		return "Weighted average cost"
	}
	return string(m)
}

// Valid reports whether the method is supported.
func (m ValuationMethod) Valid() bool {
	return m == ValuationFIFO || m == ValuationWeightedAverage
}

// ValuationMethods lists the supported methods in display order.
var ValuationMethods = []ValuationMethod{ValuationWeightedAverage, ValuationFIFO}

// ProductValuation is one product's contribution to the value of stock.
type ProductValuation struct {
	ProductID ID
	SKU       string
	Name      string
	Category  string
	OnHand    int64
	UnitCost  Money
	Value     Money
	// COGS is the cost of everything issued out over the valued period, which
	// is the figure that reaches the profit and loss account.
	COGS Money
}

// Valuation is a whole-catalogue stock valuation.
type Valuation struct {
	Method   ValuationMethod
	Currency Currency
	AsOf     time.Time
	Lines    []ProductValuation
	// Total is the value of everything on hand; COGS is the cost of
	// everything issued out over the valued period.
	Total Money
	COGS  Money
}

// costLayer is one batch of stock received at a known price, used by FIFO.
type costLayer struct {
	quantity int64
	unitCost Money
}

// ValueLedger computes the value of what remains on hand, and the cost of what
// was issued, from an ordered ledger.
//
// Movements must be ordered oldest first. Outbound movements are costed from
// the layers they consume, which is what makes FIFO meaningful; a movement
// that consumes more than the layers hold is costed at the last known price
// rather than dropped, so a negative-stock install still produces a number.
func ValueLedger(movements []StockMovement, method ValuationMethod, currency Currency) (onHand int64, value Money, cogs Money) {
	value = Zero(currency)
	cogs = Zero(currency)

	if method == ValuationWeightedAverage {
		return valueWeightedAverage(movements, currency)
	}
	return valueFIFO(movements, currency)
}

func valueWeightedAverage(movements []StockMovement, currency Currency) (int64, Money, Money) {
	var (
		onHand    int64
		totalCost = Zero(currency)
		cogs      = Zero(currency)
	)

	for _, m := range movements {
		switch {
		case m.QtyDelta > 0:
			onHand += m.QtyDelta
			totalCost = addMoney(totalCost, m.UnitCost.MulQty(m.QtyDelta))

		case m.QtyDelta < 0:
			issued := -m.QtyDelta
			unitCost := averageUnitCost(totalCost, onHand, currency)

			// Never issue more value than is held, or the average goes
			// negative and every later figure is wrong.
			costed := issued
			if costed > onHand {
				costed = onHand
			}
			issuedCost := unitCost.MulQty(costed)

			totalCost = subMoney(totalCost, issuedCost)
			cogs = addMoney(cogs, unitCost.MulQty(issued))
			onHand -= issued
			if onHand <= 0 {
				onHand = min64(onHand, 0)
				totalCost = Zero(currency)
			}
		}
	}

	if onHand <= 0 {
		return onHand, Zero(currency), cogs
	}
	return onHand, totalCost, cogs
}

func valueFIFO(movements []StockMovement, currency Currency) (int64, Money, Money) {
	var (
		layers []costLayer
		onHand int64
		cogs   = Zero(currency)
		last   = Zero(currency)
	)

	for _, m := range movements {
		switch {
		case m.QtyDelta > 0:
			onHand += m.QtyDelta
			last = m.UnitCost
			layers = append(layers, costLayer{quantity: m.QtyDelta, unitCost: m.UnitCost})

		case m.QtyDelta < 0:
			remaining := -m.QtyDelta
			onHand -= remaining

			for remaining > 0 && len(layers) > 0 {
				layer := &layers[0]
				take := min64(remaining, layer.quantity)

				cogs = addMoney(cogs, layer.unitCost.MulQty(take))
				layer.quantity -= take
				remaining -= take
				if layer.quantity == 0 {
					layers = layers[1:]
				}
			}
			// Issuing beyond every layer means stock went negative. Cost the
			// excess at the most recent known price so the number is still
			// meaningful.
			if remaining > 0 {
				cogs = addMoney(cogs, last.MulQty(remaining))
			}
		}
	}

	value := Zero(currency)
	for _, layer := range layers {
		value = addMoney(value, layer.unitCost.MulQty(layer.quantity))
	}
	if onHand <= 0 {
		return onHand, Zero(currency), cogs
	}
	return onHand, value, cogs
}

// averageUnitCost divides a total cost across a quantity, rounding to the
// nearest minor unit.
func averageUnitCost(total Money, quantity int64, currency Currency) Money {
	if quantity <= 0 {
		return Zero(currency)
	}
	// Round half away from zero so repeated issues do not systematically
	// under-report cost.
	minor := total.Minor
	half := quantity / 2
	if minor >= 0 {
		return NewMoney((minor+half)/quantity, currency)
	}
	return NewMoney((minor-half)/quantity, currency)
}

// addMoney and subMoney combine amounts already known to share a currency,
// which is guaranteed inside a single valuation run.
func addMoney(a, b Money) Money {
	return Money{Minor: a.Minor + b.Minor, Currency: a.Currency}
}

func subMoney(a, b Money) Money {
	return Money{Minor: a.Minor - b.Minor, Currency: a.Currency}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// ABCClass groups products by how much of the stock value they represent, so
// attention goes where the money is. The split follows the conventional
// Pareto bands.
type ABCClass string

const (
	// ClassA is the top 80% of value, typically a small share of the items.
	ClassA ABCClass = "A"
	// ClassB is the next 15%.
	ClassB ABCClass = "B"
	// ClassC is the final 5%, usually the bulk of the catalogue.
	ClassC ABCClass = "C"
)

// Label describes what the class means, for a report legend.
func (c ABCClass) Label() string {
	switch c {
	case ClassA:
		return "A — top 80% of value"
	case ClassB:
		return "B — next 15% of value"
	case ClassC:
		return "C — final 5% of value"
	}
	return string(c)
}

// ABCLine is one product's place in an ABC analysis.
type ABCLine struct {
	ProductValuation
	Class ABCClass
	// ShareOfValue is this product's percentage of total stock value.
	ShareOfValue float64
	// CumulativeShare is the running total through the ranking, which is what
	// the class boundaries are drawn on.
	CumulativeShare float64
}

// ClassifyABC ranks valuation lines by value and assigns each a class.
func ClassifyABC(lines []ProductValuation) []ABCLine {
	ranked := make([]ProductValuation, len(lines))
	copy(ranked, lines)
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].Value.Minor > ranked[j].Value.Minor
	})

	var total int64
	for _, line := range ranked {
		total += line.Value.Minor
	}

	out := make([]ABCLine, 0, len(ranked))
	var cumulative int64
	for _, line := range ranked {
		cumulative += line.Value.Minor

		entry := ABCLine{ProductValuation: line, Class: ClassC}
		if total > 0 {
			entry.ShareOfValue = float64(line.Value.Minor) / float64(total) * 100
			entry.CumulativeShare = float64(cumulative) / float64(total) * 100
		}

		switch {
		case entry.CumulativeShare <= 80:
			entry.Class = ClassA
		case entry.CumulativeShare <= 95:
			entry.Class = ClassB
		}
		out = append(out, entry)
	}
	return out
}

// AgingBucket groups stock by how long it has sat without moving.
type AgingBucket string

const (
	AgingFresh    AgingBucket = "0-30"
	Aging30to90   AgingBucket = "30-90"
	Aging90to180  AgingBucket = "90-180"
	Aging180to365 AgingBucket = "180-365"
	AgingDead     AgingBucket = "365+"
)

// AgingBuckets lists the buckets oldest-last, in report order.
var AgingBuckets = []AgingBucket{AgingFresh, Aging30to90, Aging90to180, Aging180to365, AgingDead}

// Label is the human-readable bucket name.
func (b AgingBucket) Label() string {
	switch b {
	case AgingFresh:
		return "Moved in the last 30 days"
	case Aging30to90:
		return "30 to 90 days"
	case Aging90to180:
		return "90 to 180 days"
	case Aging180to365:
		return "180 days to a year"
	case AgingDead:
		return "Over a year — dead stock"
	}
	return string(b)
}

// BucketForAge places a number of days since the last movement into a bucket.
func BucketForAge(days int) AgingBucket {
	switch {
	case days < 30:
		return AgingFresh
	case days < 90:
		return Aging30to90
	case days < 180:
		return Aging90to180
	case days < 365:
		return Aging180to365
	}
	return AgingDead
}
