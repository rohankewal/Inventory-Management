// Command demoseed fills a database with a realistic catalogue and a year of
// stock history, for evaluating screens and reports against something that
// behaves like a real business rather than a handful of round numbers.
//
// It refuses to touch a database that already holds products.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/bootstrap"
	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type item struct {
	sku, barcode, name, category, supplier, tags string
	unit                                         core.UnitOfMeasure
	price, cost                                  string
	opening                                      int64
	reorderPoint, reorderQty                     int64
	// turns is roughly how many times a year the item cycles, which drives how
	// much history it gets.
	turns int
}

var catalogue = []item{
	{"ANV-001", "0740001000015", "Cast Iron Anvil 55lb", "Tools", "Acme Supply Co", "heavy", core.UnitEach, "249.99", "142.00", 14, 5, 10, 1},
	{"HMR-002", "0740001000022", "Claw Hammer 16oz", "Tools", "Acme Supply Co", "bestseller", core.UnitEach, "19.99", "8.40", 90, 25, 100, 6},
	{"WRN-003", "0740001000039", "Adjustable Wrench 10in", "Tools", "Globex Industrial", "", core.UnitEach, "32.50", "15.75", 60, 20, 50, 4},
	{"SAW-004", "0740001000046", "Hand Saw 22in", "Tools", "Acme Supply Co", "", core.UnitEach, "27.40", "12.90", 35, 12, 30, 3},
	{"CBL-100", "0740001001005", "Cable Tie 100pk", "Consumables", "Globex Industrial", "bulky", core.UnitPack, "6.25", "2.10", 480, 100, 500, 8},
	{"CBL-200", "0740001002002", "Cable Tie 200pk", "Consumables", "Globex Industrial", "bulky", core.UnitPack, "11.00", "4.05", 210, 50, 200, 5},
	{"TAP-050", "0740001003009", "Duct Tape 50m", "Consumables", "Initech Supplies", "", core.UnitEach, "8.99", "3.20", 340, 60, 200, 7},
	{"GLV-010", "0740001004006", "Work Gloves L", "Safety", "Initech Supplies", "ppe", core.UnitPair, "14.50", "6.80", 180, 40, 120, 6},
	{"GLV-011", "0740001004013", "Work Gloves XL", "Safety", "Initech Supplies", "ppe", core.UnitPair, "14.50", "6.80", 120, 40, 120, 6},
	{"HLM-020", "0740001005003", "Hard Hat White", "Safety", "Initech Supplies", "ppe", core.UnitEach, "27.00", "12.10", 95, 25, 60, 3},
	{"GOG-021", "0740001005010", "Safety Goggles", "Safety", "Initech Supplies", "ppe", core.UnitEach, "9.75", "3.85", 150, 50, 150, 5},
	{"BLT-M8", "0740001006000", "Bolt M8x40 Zinc", "Fasteners", "Fastener World", "bulky", core.UnitBox, "18.75", "7.90", 110, 30, 90, 4},
	{"BLT-M10", "0740001007007", "Bolt M10x50 Zinc", "Fasteners", "Fastener World", "", core.UnitBox, "22.40", "10.15", 85, 30, 90, 4},
	{"NUT-M8", "0740001008004", "Nut M8 Zinc", "Fasteners", "Fastener World", "", core.UnitBox, "12.00", "4.60", 200, 50, 150, 4},
	{"WSH-M8", "0740001008011", "Washer M8 Zinc", "Fasteners", "Fastener World", "", core.UnitBox, "6.40", "2.15", 240, 60, 150, 3},
	{"PNT-BLU", "0740001009001", "Enamel Paint Blue 1L", "Finishes", "Globex Industrial", "fragile", core.UnitEach, "24.00", "11.30", 60, 15, 40, 3},
	{"PNT-RED", "0740001010007", "Enamel Paint Red 1L", "Finishes", "Globex Industrial", "fragile", core.UnitEach, "24.00", "11.30", 45, 15, 40, 2},
	{"BRS-040", "0740001011004", "Paint Brush 40mm", "Finishes", "Globex Industrial", "", core.UnitEach, "5.75", "1.95", 520, 80, 200, 6},
	// Deliberately left to gather dust, so the aging and dead-stock reports
	// have something real to find.
	{"LDR-8FT", "0740001012001", "Step Ladder 8ft", "Access", "Acme Supply Co", "bulky", core.UnitEach, "189.00", "96.50", 12, 3, 6, 0},
	{"TRL-CRT", "0740001013008", "Platform Trolley", "Access", "Acme Supply Co", "bulky", core.UnitEach, "245.00", "128.00", 6, 2, 4, 0},
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func main() {
	force := flag.Bool("force", false, "seed even if the database already holds products")
	flag.Parse()

	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	existing, err := store.Products().Count(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		log.Fatal(err)
	}
	if existing > 0 && !*force {
		fmt.Fprintf(os.Stderr,
			"demoseed: the database already holds %d product(s); pass -force to seed anyway\n", existing)
		os.Exit(1)
	}

	// A fixed seed keeps the generated history identical between runs, so a
	// screenshot taken today and one taken next week are comparable.
	rng := rand.New(rand.NewSource(20260902))
	now := time.Now().UTC()
	start := now.AddDate(-1, 0, 0)

	// at builds a service whose clock is fixed at a past moment, which is how
	// history gets real timestamps without reaching past the service layer.
	at := func(when time.Time) *service.Inventory {
		return service.NewInventory(store, service.WithClock(func() time.Time { return when }))
	}

	ids := map[string]core.ID{}
	for _, it := range catalogue {
		created, err := at(start).CreateProduct(ctx, core.Product{
			SKU: it.sku, Barcode: it.barcode, Name: it.name,
			Category: it.category, Supplier: it.supplier,
			Tags:         core.ParseTags(it.tags),
			Unit:         it.unit,
			Price:        core.MustParseMoney(it.price, "USD"),
			Cost:         core.MustParseMoney(it.cost, "USD"),
			ReorderPoint: it.reorderPoint, ReorderQuantity: it.reorderQty,
		}, service.OpeningStock{
			Quantity: it.opening,
			UnitCost: core.MustParseMoney(it.cost, "USD"),
		})
		if err != nil {
			log.Fatalf("create %s: %v", it.sku, err)
		}
		ids[it.sku] = created.ID
	}

	// Walk the year forward. Each item receives and issues at a rate set by
	// its turns, with costs drifting so valuation has real layers to work on.
	for _, it := range catalogue {
		if it.turns == 0 {
			continue
		}

		baseCost := core.MustParseMoney(it.cost, "USD")
		for cycle := range it.turns {
			progress := float64(cycle+1) / float64(it.turns+1)
			when := start.Add(time.Duration(progress * float64(now.Sub(start))))

			// Costs drift by up to ±8% over the year.
			drift := 1 + (rng.Float64()-0.35)*0.16
			cost := core.NewMoney(int64(float64(baseCost.Minor)*drift), "USD")

			receiveQty := it.reorderQty
			if receiveQty == 0 {
				receiveQty = 10
			}
			if _, err := at(when).ReceiveStock(ctx, service.AdjustStockInput{
				ProductID: ids[it.sku], Delta: receiveQty,
				UnitCost: cost, Note: "purchase order",
			}); err != nil {
				log.Fatalf("receive %s: %v", it.sku, err)
			}

			// Issue slightly more than was received, so levels trend down and
			// the reorder report has something to say — but never more than is
			// on the shelf, which the service rightly refuses.
			issueAt := when.Add(time.Duration(rng.Intn(20)+3) * 24 * time.Hour)
			if issueAt.After(now) {
				issueAt = now.Add(-time.Hour)
			}

			current, err := at(issueAt).GetProduct(ctx, ids[it.sku], core.NilID)
			if err != nil {
				log.Fatalf("read %s: %v", it.sku, err)
			}

			// Aim to leave the item somewhere around its reorder point, which
			// is where a real catalogue sits: mostly healthy, a handful low,
			// one or two out. Draining everything every cycle would make every
			// screen look like a crisis.
			target := int64(float64(it.reorderPoint) * (0.55 + rng.Float64()*1.5))
			issueQty := min64(current.OnHand-target, current.OnHand)
			if issueQty <= 0 {
				continue
			}
			if _, err := at(issueAt).IssueStock(ctx, service.AdjustStockInput{
				ProductID: ids[it.sku], Delta: issueQty, Note: "issued to site",
			}); err != nil {
				log.Fatalf("issue %s: %v", it.sku, err)
			}
		}
	}

	// A quarterly count that finds a discrepancy, and a breakage.
	svc := service.NewInventory(store)
	if _, err := at(now.AddDate(0, 0, -9)).SetStock(ctx, service.SetStockInput{
		ProductID: ids["BLT-M8"], Counted: 44, Note: "quarterly count — three boxes unaccounted for",
	}); err != nil {
		log.Fatal(err)
	}
	if _, err := at(now.AddDate(0, 0, -4)).AdjustStock(ctx, service.AdjustStockInput{
		ProductID: ids["PNT-RED"], Delta: -2,
		Reason: core.ReasonWriteOff, Note: "two tins damaged in transit",
	}); err != nil {
		log.Fatal(err)
	}

	// A lot-tracked consumable with batches at different stages of their life.
	adhesive, err := at(now.AddDate(0, -8, 0)).CreateProduct(ctx, core.Product{
		SKU: "ADH-500", Barcode: "0740001014005", Name: "Two-Part Adhesive 500ml",
		Category: "Consumables", Supplier: "Initech Supplies",
		Tags: core.ParseTags("hazmat, fragile"), Unit: core.UnitEach, TrackLots: true,
		Price: core.MustParseMoney("38.00", "USD"), Cost: core.MustParseMoney("17.25", "USD"),
		ReorderPoint: 20, ReorderQuantity: 40,
		Description: "Shelf life 12 months from manufacture",
	}, service.OpeningStock{})
	if err != nil {
		log.Fatal(err)
	}
	for _, lot := range []struct {
		number   string
		qty      int64
		received int
		expires  int
	}{
		{"B2508-A", 24, -210, 9},  // expiring within the fortnight
		{"B2511-C", 30, -120, 68}, // expiring this quarter
		{"B2602-F", 18, -30, 244}, // plenty of life left
	} {
		if _, err := at(now.AddDate(0, 0, lot.received)).ReceiveStock(ctx, service.AdjustStockInput{
			ProductID: adhesive.ID, Delta: lot.qty,
			UnitCost:   core.MustParseMoney("17.25", "USD"),
			LotNumber:  lot.number,
			ExpiryDate: now.AddDate(0, 0, lot.expires),
		}); err != nil {
			log.Fatalf("lot %s: %v", lot.number, err)
		}
	}

	// A non-stock service line, to prove it stays out of the valuation.
	if _, err := at(start).CreateProduct(ctx, core.Product{
		SKU: "SVC-FIT", Name: "On-site Fitting (per hour)", Category: "Services",
		Unit: core.UnitHour, NonStock: true,
		Price: core.MustParseMoney("85.00", "USD"), Cost: core.MustParseMoney("42.00", "USD"),
		Description: "Charged in whole hours, minimum two",
	}, service.OpeningStock{}); err != nil {
		log.Fatal(err)
	}

	// An archived line, so the "show archived" filter has something to reveal.
	retired, err := at(start).CreateProduct(ctx, core.Product{
		SKU: "PNT-GRN", Name: "Enamel Paint Green 1L (discontinued)",
		Category: "Finishes", Supplier: "Globex Industrial", Unit: core.UnitEach,
		Price: core.MustParseMoney("24.00", "USD"), Cost: core.MustParseMoney("11.30", "USD"),
	}, service.OpeningStock{Quantity: 8, UnitCost: core.MustParseMoney("11.30", "USD")})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := svc.DeleteProduct(ctx, retired.ID, core.NilID); err != nil {
		log.Fatal(err)
	}

	if err := seedClients(ctx, store, rng, now); err != nil {
		log.Fatal(err)
	}

	products, _ := store.Products().Count(ctx, storage.ProductFilter{IncludeInactive: true})
	movements, _ := store.Movements().Count(ctx, storage.MovementFilter{})
	customers, _ := store.Customers().Count(ctx, storage.CustomerFilter{})
	orders, _ := store.Orders().Count(ctx, storage.OrderFilter{})
	fmt.Printf("Seeded %d products, %d stock movements, %d customers and %d store orders.\n",
		products, movements, customers, orders)
}
