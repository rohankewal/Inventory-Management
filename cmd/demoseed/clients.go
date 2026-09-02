package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// client describes one retail account and its doors.
type client struct {
	code, name, terms string
	contact           core.Contact
	stores            []storeSpec
	programs          []programSpec
}

type storeSpec struct {
	code, name string
	address    core.Address
	routing    string
}

type programSpec struct {
	code, name, season string
	status             core.ProgramStatus
	deliveryInDays     int
	// skus are the products this buy covers.
	skus []string
	// poPrefix is how the client numbers the POs for this program.
	poPrefix string
}

var clients = []client{
	{
		code: "MACYS", name: "Macy's", terms: "Net 60",
		contact: core.Contact{Name: "Home Buying Office", Email: "home.buying@example.com"},
		stores: []storeSpec{
			{"0047", "Herald Square", core.Address{
				Line1: "151 W 34th St", Line2: "Receiving Dock B", City: "New York",
				Region: "NY", PostalCode: "10001", Country: "USA",
			}, "Appointment required 24h ahead. GS1-128 carton labels on the short side."},
			{"0100", "Roosevelt Field", core.Address{
				Line1: "630 Old Country Rd", City: "Garden City",
				Region: "NY", PostalCode: "11530", Country: "USA",
			}, "Deliveries 06:00–11:00 only."},
			{"0233", "Union Square", core.Address{
				Line1: "170 O'Farrell St", City: "San Francisco",
				Region: "CA", PostalCode: "94102", Country: "USA",
			}, "Rear dock on Stockton. No pallets over 48in."},
			{"0451", "State Street", core.Address{
				Line1: "111 N State St", City: "Chicago",
				Region: "IL", PostalCode: "60602", Country: "USA",
			}, ""},
		},
		programs: []programSpec{
			{"FW26-THROWS", "FW26 Sherpa Throws", "Fall/Winter 2026",
				core.ProgramInProduction, 45, []string{"THROW-SHRP", "THROW-KNIT"}, "MCY"},
			{"SS26-TOWELS", "SS26 Bath Towels", "Spring/Summer 2026",
				core.ProgramDelivering, 12, []string{"TOWEL-BATH", "TOWEL-HAND"}, "MCY"},
		},
	},
	{
		code: "KOHLS", name: "Kohl's", terms: "Net 45",
		contact: core.Contact{Name: "Softlines Buying", Email: "softlines@example.com"},
		stores: []storeSpec{
			{"0012", "Menomonee Falls", core.Address{
				Line1: "N56 W17000 Ridgewood Dr", City: "Menomonee Falls",
				Region: "WI", PostalCode: "53051", Country: "USA",
			}, "EDI 856 ASN required 24h before delivery."},
			{"0088", "Schaumburg", core.Address{
				Line1: "1450 E Golf Rd", City: "Schaumburg",
				Region: "IL", PostalCode: "60173", Country: "USA",
			}, ""},
			{"0175", "Plano", core.Address{
				Line1: "1901 Preston Rd", City: "Plano",
				Region: "TX", PostalCode: "75093", Country: "USA",
			}, "Pallet deliveries only. Book via supplier portal."},
		},
		programs: []programSpec{
			{"FW26-BLANKET", "FW26 Weighted Blankets", "Fall/Winter 2026",
				core.ProgramShipping, 28, []string{"BLKT-WGT"}, "KHL"},
		},
	},
	{
		code: "TARGET", name: "Target", terms: "Net 30",
		contact: core.Contact{Name: "Home Essentials", Email: "home@example.com"},
		stores: []storeSpec{
			{"T-1420", "Minneapolis Nicollet", core.Address{
				Line1: "900 Nicollet Mall", City: "Minneapolis",
				Region: "MN", PostalCode: "55403", Country: "USA",
			}, "Routing guide v9. Chargeback for unlabelled cartons."},
			{"T-2201", "Brooklyn Atlantic", core.Address{
				Line1: "139 Flatbush Ave", City: "Brooklyn",
				Region: "NY", PostalCode: "11217", Country: "USA",
			}, ""},
		},
		programs: []programSpec{
			{"SS26-STORAGE", "SS26 Storage Baskets", "Spring/Summer 2026",
				core.ProgramConfirmed, 70, []string{"BSKT-SEAG", "BSKT-COTT"}, "TGT"},
		},
	},
}

// clientProducts are the goods these programs are built around: private-label
// items sourced overseas rather than catalogue stock.
var clientProducts = []item{
	{"THROW-SHRP", "0750002000018", "Sherpa Throw 50x60 Charcoal", "Home Textiles", "Hanoi Textile Co", "program", core.UnitEach, "24.99", "9.40", 2800, 400, 1200, 0},
	{"THROW-KNIT", "0750002000025", "Cable Knit Throw 50x60 Cream", "Home Textiles", "Hanoi Textile Co", "program", core.UnitEach, "29.99", "11.80", 1100, 400, 1200, 0},
	{"TOWEL-BATH", "0750002000032", "Bath Towel 30x54 White", "Home Textiles", "Coimbatore Mills", "program", core.UnitEach, "12.99", "4.15", 5200, 800, 2400, 0},
	{"TOWEL-HAND", "0750002000049", "Hand Towel 16x28 White", "Home Textiles", "Coimbatore Mills", "program", core.UnitEach, "6.99", "2.05", 6400, 800, 2400, 0},
	{"BLKT-WGT", "0750002000056", "Weighted Blanket 15lb Grey", "Home Textiles", "Shaoxing Home", "program", core.UnitEach, "59.99", "23.60", 820, 250, 600, 0},
	{"BSKT-SEAG", "0750002000063", "Seagrass Basket Large", "Home Storage", "Cirebon Weavers", "program", core.UnitEach, "34.99", "13.20", 380, 200, 600, 0},
	{"BSKT-COTT", "0750002000070", "Cotton Rope Basket Medium", "Home Storage", "Cirebon Weavers", "program", core.UnitEach, "22.99", "8.70", 1600, 200, 600, 0},
}

// seedClients creates the retail accounts, their doors, the programs, and a
// store PO per door — which is the shape the business actually works in.
func seedClients(ctx context.Context, store storage.Store, rng *rand.Rand, now time.Time) error {
	at := func(when time.Time) *service.Inventory {
		return service.NewInventory(store, service.WithClock(func() time.Time { return when }))
	}
	svc := at(now)

	productIDs := map[string]core.ID{}
	for _, it := range clientProducts {
		created, err := at(now.AddDate(-1, 0, 0)).CreateProduct(ctx, core.Product{
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
			return fmt.Errorf("create %s: %w", it.sku, err)
		}
		productIDs[it.sku] = created.ID
	}

	poCounter := 1200
	var orders int

	for _, c := range clients {
		customer, err := svc.CreateCustomer(ctx, core.Customer{
			Code: c.code, Name: c.name, Currency: "USD", Terms: c.terms, Contact: c.contact,
		})
		if err != nil {
			return fmt.Errorf("create customer %s: %w", c.code, err)
		}

		storeIDs := make([]core.ID, 0, len(c.stores))
		for _, s := range c.stores {
			created, err := svc.CreateStore(ctx, core.CustomerStore{
				CustomerID: customer.ID, Code: s.code, Name: s.name,
				ShipTo: s.address, RoutingNotes: s.routing,
			})
			if err != nil {
				return fmt.Errorf("create store %s: %w", s.code, err)
			}
			storeIDs = append(storeIDs, created.ID)
		}

		for _, p := range c.programs {
			program, err := svc.CreateProgram(ctx, core.Program{
				CustomerID: customer.ID, Code: p.code, Name: p.name, Season: p.season,
				Status:             p.status,
				TargetDeliveryDate: now.AddDate(0, 0, p.deliveryInDays),
			})
			if err != nil {
				return fmt.Errorf("create program %s: %w", p.code, err)
			}

			// Every door gets its own PO, numbered the way a retailer does it.
			for _, storeID := range storeIDs {
				poCounter++
				poNumber := fmt.Sprintf("%s-%04d", p.poPrefix, poCounter)

				shipDate := now.AddDate(0, 0, p.deliveryInDays-rng.Intn(10))
				lines := make([]core.StoreOrderLine, 0, len(p.skus))
				for _, sku := range p.skus {
					quantity := int64((rng.Intn(8) + 3) * 60)
					lines = append(lines, core.StoreOrderLine{
						ProductID: productIDs[sku],
						Quantity:  quantity,
						UnitPrice: core.MustParseMoney(sellPriceFor(sku), "USD"),
					})
				}

				status := core.OrderConfirmed
				if p.status == core.ProgramConfirmed {
					status = core.OrderDraft
				}

				order := core.StoreOrder{
					CustomerID: customer.ID, StoreID: storeID, ProgramID: program.ID,
					CustomerPONumber:  poNumber,
					Status:            status,
					RequestedShipDate: shipDate,
					CancelAfterDate:   shipDate.AddDate(0, 0, 14),
				}
				if _, err := at(now.AddDate(0, 0, -rng.Intn(40)-5)).
					SaveOrder(ctx, order, lines); err != nil {
					return fmt.Errorf("create order %s: %w", poNumber, err)
				}
				orders++
			}
		}
	}

	// One order deliberately left past its cancel date, so the late-order
	// alert has something real to show.
	late, err := svc.ListOrders(ctx, storage.OrderFilter{OpenOnly: true, Limit: 1})
	if err != nil {
		return err
	}
	if len(late.Items) > 0 {
		overdue := late.Items[0].StoreOrder
		overdue.RequestedShipDate = now.AddDate(0, 0, -18)
		overdue.CancelAfterDate = now.AddDate(0, 0, -4)

		detail, err := svc.GetOrder(ctx, overdue.ID)
		if err != nil {
			return err
		}
		lines := make([]core.StoreOrderLine, len(detail.Lines))
		for i, line := range detail.Lines {
			lines[i] = line.StoreOrderLine
		}
		if _, err := svc.SaveOrder(ctx, overdue, lines); err != nil {
			return err
		}
	}

	log.Printf("seeded %d customers, %d store orders", len(clients), orders)
	return nil
}

func sellPriceFor(sku string) string {
	for _, it := range clientProducts {
		if it.sku == sku {
			return it.price
		}
	}
	return "10.00"
}
