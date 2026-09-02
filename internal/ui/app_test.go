package ui

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
)

// newTestApp builds the whole shell against Fyne's headless driver.
func newTestApp(t *testing.T) *App {
	t.Helper()

	store, err := sqlite.Open(context.Background(), sqlite.Options{
		Path: filepath.Join(t.TempDir(), "app.db"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	svc := service.NewInventory(store, service.WithLogger(slog.Default()))
	cfg := config.Default()

	a := newApp(test.NewApp(), svc, cfg, slog.Default())
	t.Cleanup(func() {
		a.cancel()
		// Stop any pending debounce so a timer from one test cannot fire while
		// the next one is building its own widgets.
		if products, ok := a.views[viewProducts].(*productsView); ok && products.debounce != nil {
			products.debounce.Stop()
		}
		a.status.stopTimer()
	})

	// Run every query inline on the test goroutine. Fyne's headless driver
	// executes a marshalled closure on whichever goroutine submitted it, so
	// leaving the real threading in place would put a background query and the
	// test's own assertions inside Fyne's widget code simultaneously.
	a.run = func(fn func(ctx context.Context)) {
		defer a.endWork()
		fn(context.Background())
	}
	a.onMain = func(fn func()) { fn() }
	a.status.onMain = a.onMain

	return a
}

// TestBuildEveryView constructs the whole application. It exists because the
// crash it guards against — a widget's change handler firing during
// construction and touching a field that does not exist yet — is invisible to
// the compiler, invisible to a unit test of any single function, and fatal on
// the first launch after it is introduced.
func TestBuildEveryView(t *testing.T) {
	a := newTestApp(t)

	if len(a.views) == 0 {
		t.Fatal("newApp() built no views")
	}
	for id, v := range a.views {
		if v.object() == nil {
			t.Errorf("view %q has no content", id)
		}
		if v.title() == "" {
			t.Errorf("view %q has no title", id)
		}
		for i, action := range v.actions() {
			if action == nil {
				t.Errorf("view %q action %d is nil", id, i)
			}
		}
	}
}

// TestSwitchToEveryView drives the navigation the way a user would, which is
// what exercises each view's header, actions and content swap.
func TestSwitchToEveryView(t *testing.T) {
	a := newTestApp(t)

	for _, id := range []viewID{
		viewDashboard, viewOrders, viewCustomers, viewProducts,
		viewMovements, viewReports, viewSettings,
	} {
		a.show(id)

		if a.active != id {
			t.Errorf("show(%q) left the active view as %q", id, a.active)
		}
		if got := a.titleLabel.Text; got != a.views[id].title() {
			t.Errorf("show(%q) set the header to %q, want %q", id, got, a.views[id].title())
		}
		if len(a.content.Objects) != 1 {
			t.Errorf("show(%q) put %d objects in the content area, want 1", id, len(a.content.Objects))
		}
	}
}

// TestProductFiltersDoNotPanic drives the filter bar's controls, which is
// where the construction-order crash originally surfaced.
func TestProductFiltersDoNotPanic(t *testing.T) {
	a := newTestApp(t)
	a.show(viewProducts)

	products, ok := a.views[viewProducts].(*productsView)
	if !ok {
		t.Fatal("the products view is not a *productsView")
	}

	products.searchBox.SetText("anvil")
	products.archivedChk.SetChecked(true)
	for i := range stockFilterOptions {
		products.stockSel.SetSelectedIndex(i)
	}
	products.categorySel.SetOptions([]string{"Tools"})
	products.categorySel.SetSelected("Tools")
	products.clearFilters()

	if products.filter.Search != "" {
		t.Errorf("clearFilters() left the search term %q", products.filter.Search)
	}
	if products.filter.IncludeInactive {
		t.Error("clearFilters() left archived products included")
	}
	if products.filter.Stock != "" {
		t.Errorf("clearFilters() left the stock filter as %q", products.filter.Stock)
	}
}

// TestReportTabsBuild switches through every report tab.
func TestReportTabsBuild(t *testing.T) {
	a := newTestApp(t)
	a.show(viewReports)

	reports, ok := a.views[viewReports].(*reportsView)
	if !ok {
		t.Fatal("the reports view is not a *reportsView")
	}

	// Every report id must map to a distinct tab. A duplicate mapping is
	// invisible until somebody clicks a dashboard card and lands on the wrong
	// screen.
	seen := map[int]reportID{}
	for _, id := range []reportID{
		reportCoverage, reportValuation, reportReorder, reportABC, reportAging, reportExpiry,
	} {
		reports.showTab(id)
		index := reports.tabs.SelectedIndex()
		if previous, clash := seen[index]; clash {
			t.Errorf("report %q and %q both open tab %d", id, previous, index)
		}
		seen[index] = id
	}
	if len(seen) != len(reports.tabs.Items) {
		t.Errorf("%d report ids map onto %d tabs; every tab should be reachable",
			len(seen), len(reports.tabs.Items))
	}
}

// TestStatusBarLevels exercises each status level.
func TestStatusBarLevels(t *testing.T) {
	a := newTestApp(t)

	a.status.info("checking %d", 1)
	if a.status.label.Text != "checking 1" {
		t.Errorf("info() set %q", a.status.label.Text)
	}

	a.status.failure("it broke")
	if a.status.label.Text != "it broke" {
		t.Errorf("failure() set %q", a.status.label.Text)
	}
	// An error must stay on screen rather than clearing itself, because an
	// error the user missed is one they will hit again.
	if a.status.clearTimer != nil {
		t.Error("failure() scheduled the message to clear")
	}

	a.status.success("done")
	if a.status.clearTimer == nil {
		t.Error("success() did not schedule the message to clear")
	}
	a.status.stopTimer()
}

// TestScanBoxFallsBackToSearch checks the behaviour that makes one box serve
// both a scanner and a person: a code that matches nothing becomes a search.
func TestScanBoxFallsBackToSearch(t *testing.T) {
	a := newTestApp(t)
	a.show(viewProducts)

	products := a.views[viewProducts].(*productsView)
	a.searchCatalogue("not-a-real-code")

	if products.searchBox.Text != "not-a-real-code" {
		t.Errorf("search box = %q, want the unmatched code", products.searchBox.Text)
	}
	if a.active != viewProducts {
		t.Errorf("active view = %q, want the catalogue", a.active)
	}
}

func TestFilterByTagSwitchesAndFilters(t *testing.T) {
	a := newTestApp(t)
	a.filterByTag("fragile")

	products := a.views[viewProducts].(*productsView)
	if products.filter.Tag != "fragile" {
		t.Errorf("filter tag = %q, want fragile", products.filter.Tag)
	}
	if a.active != viewProducts {
		t.Errorf("active view = %q, want the catalogue", a.active)
	}
}

func TestDetailPanelRendersAndClears(t *testing.T) {
	a := newTestApp(t)
	panel := newProductDetailPanel(a)

	panel.clear()
	if panel.body.Visible() {
		t.Error("the detail body is visible with nothing selected")
	}

	product := core.ProductWithStock{
		Product: core.Product{
			SKU: "ANV-1", Name: "Anvil", Unit: core.UnitEach,
			Price:        core.MustParseMoney("249.99", "USD"),
			Cost:         core.MustParseMoney("142.00", "USD"),
			Tags:         core.ParseTags("heavy, bestseller"),
			ReorderPoint: 5,
		},
		OnHand: 3,
	}
	panel.show(product)

	if !panel.body.Visible() {
		t.Error("the detail body is hidden after selecting a product")
	}
	if panel.nameLabel.Text != "Anvil" {
		t.Errorf("name = %q, want Anvil", panel.nameLabel.Text)
	}
	// Three on hand against a reorder point of five is low, and the panel must
	// say so rather than just showing the number.
	if panel.statusLabel.Text == "" {
		t.Error("the panel gave no commentary on a low stock level")
	}
}

// TestDetailPanelHandlesEdgeCases renders products that would divide by zero
// or dereference something absent.
func TestDetailPanelHandlesEdgeCases(t *testing.T) {
	a := newTestApp(t)
	panel := newProductDetailPanel(a)

	cases := map[string]core.ProductWithStock{
		"zero price": {Product: core.Product{SKU: "Z-1", Name: "Free", Price: core.Zero("USD")}},
		"non-stock":  {Product: core.Product{SKU: "S-1", Name: "Service", NonStock: true}},
		"negative":   {Product: core.Product{SKU: "N-1", Name: "Negative"}, OnHand: -7},
		"empty":      {},
	}
	for name, product := range cases {
		t.Run(name, func(t *testing.T) { panel.show(product) })
	}
}
