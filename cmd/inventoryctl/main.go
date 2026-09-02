// Command inventoryctl administers an inventory database from the terminal.
//
// It exists so that migrating a server, taking a backup or repairing a stock
// level does not require the desktop app, a SQL client, or physical access to
// the machine the GUI runs on.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/bootstrap"
	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

var version = "dev"

const commandTimeout = 5 * time.Minute

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "inventoryctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	command, rest := args[0], args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	switch command {
	case "status":
		return cmdStatus(ctx, rest)
	case "migrate":
		return cmdMigrate(ctx, rest)
	case "verify":
		return cmdVerify(ctx, rest)
	case "backup":
		return cmdBackup(ctx, rest)
	case "import":
		return cmdImport(ctx, rest)
	case "export":
		return cmdExport(ctx, rest)
	case "template":
		return cmdTemplate(ctx, rest)
	case "import-stores":
		return cmdImportClientData(ctx, rest, "import-stores")
	case "import-orders":
		return cmdImportClientData(ctx, rest, "import-orders")
	case "orders":
		return cmdOrders(ctx, rest)
	case "version":
		fmt.Println("inventoryctl", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `inventoryctl administers an inventory database.

Usage:
  inventoryctl <command> [flags]

Commands:
  status     Show the configuration, schema version and record counts
  migrate    Apply pending schema migrations
  verify     Rebuild cached stock levels from the ledger and report differences
  backup     Write a consistent snapshot to a file
  import     Load products from a CSV file
  export     Write products to a CSV file
  template   Write a blank CSV file with the expected headings

  import-stores  Load a customer's store list from a CSV file
  import-orders  Load store purchase orders from a CSV file
  orders         List open orders, or those past their cancel date
  version    Print the build version

The database is chosen by the same configuration the desktop app uses. Override
it for one run with the `+"`INVENTORY_DRIVER`"+` and `+"`INVENTORY_DSN`"+` environment variables.
`)
}

// cmdStatus reports what a support conversation needs to know first: which
// database is in use, whether its schema is current, and how much is in it.
func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Opening without migrating: inspecting a database must never change it.
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{SkipMigrate: true})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "version\t%s\n", version)
	fmt.Fprintf(w, "data directory\t%s\n", cfg.Dir())
	fmt.Fprintf(w, "driver\t%s\n", cfg.Driver)
	if cfg.Driver == config.DriverSQLite {
		fmt.Fprintf(w, "database\t%s\n", cfg.DatabasePath())
	}
	fmt.Fprintf(w, "currency\t%s\n", cfg.Currency)

	pending, err := pendingMigrations(ctx, store)
	if err != nil {
		return err
	}
	if reporter, ok := store.(storage.SchemaReporter); ok {
		schemaVersion, err := reporter.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "schema version\t%d\n", schemaVersion)

		if len(pending) == 0 {
			fmt.Fprintf(w, "pending migrations\tnone\n")
		} else {
			fmt.Fprintf(w, "pending migrations\t%d (run `inventoryctl migrate`)\n", len(pending))
			for _, name := range pending {
				fmt.Fprintf(w, "\t  %s\n", name)
			}
		}
	}

	// Counting rows in tables a pending migration has not created yet would
	// fail with a raw SQL error, which is exactly the wrong thing to show
	// someone whose database simply needs migrating.
	if len(pending) > 0 {
		fmt.Fprintf(w, "records\tnot counted until the schema is migrated\n")
		return w.Flush()
	}

	products, err := store.Products().Count(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		return err
	}
	activeProducts, err := store.Products().Count(ctx, storage.ProductFilter{})
	if err != nil {
		return err
	}
	movements, err := store.Movements().Count(ctx, storage.MovementFilter{})
	if err != nil {
		return err
	}
	locations, err := store.Locations().List(ctx, true)
	if err != nil {
		return err
	}

	fmt.Fprintf(w, "products\t%d (%d active, %d archived)\n",
		products, activeProducts, products-activeProducts)
	fmt.Fprintf(w, "stock movements\t%d\n", movements)
	fmt.Fprintf(w, "locations\t%d\n", len(locations))

	return w.Flush()
}

func cmdMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "list pending migrations without applying them")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{SkipMigrate: true})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	pending, err := pendingMigrations(ctx, store)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("Schema is up to date; nothing to apply.")
		return nil
	}

	if *dryRun {
		fmt.Printf("%d migration(s) would be applied:\n", len(pending))
		for _, name := range pending {
			fmt.Println("  " + name)
		}
		return nil
	}

	migrator, ok := store.(interface {
		Migrate(context.Context) ([]string, error)
	})
	if !ok {
		return fmt.Errorf("the %s backend cannot be migrated by this tool", cfg.Driver)
	}

	fmt.Printf("Applying %d migration(s). Back up the database first if it holds live data.\n", len(pending))
	applied, err := migrator.Migrate(ctx)
	if err != nil {
		return err
	}
	for _, name := range applied {
		fmt.Println("  applied " + name)
	}
	fmt.Println("Done.")
	return nil
}

// cmdVerify compares every cached stock level against the ledger it is
// derived from. The ledger is the source of truth, so a difference means the
// cache is wrong; -fix rebuilds it.
func cmdVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fix := fs.Bool("fix", false, "rebuild cached levels that disagree with the ledger")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{SkipMigrate: true})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	if err := requireCurrentSchema(ctx, store); err != nil {
		return err
	}

	products, err := store.Products().List(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		return err
	}

	var checked, differing, repaired int
	for _, p := range products {
		checked++

		cached, err := cachedLevels(ctx, store, p.ID)
		if err != nil {
			return err
		}
		actual, err := ledgerLevels(ctx, store, p.ID)
		if err != nil {
			return err
		}

		mismatches := compareLevels(cached, actual)
		if len(mismatches) == 0 {
			continue
		}
		differing++

		for _, m := range mismatches {
			fmt.Printf("  %-20s location %s: cached %d, ledger %d\n",
				p.SKU, m.location, m.cached, m.actual)
		}
		if *fix {
			if err := store.Movements().Recompute(ctx, p.ID); err != nil {
				return err
			}
			repaired++
		}
	}

	if differing == 0 {
		fmt.Printf("Checked %d product(s); every cached level matches the ledger.\n", checked)
		return nil
	}
	if *fix {
		fmt.Printf("Checked %d product(s); rebuilt %d from the ledger.\n", checked, repaired)
		return nil
	}
	fmt.Printf("Checked %d product(s); %d disagree with the ledger. Re-run with -fix to rebuild them.\n",
		checked, differing)
	return nil
}

// cachedLevels reads the stock_levels cache for a product.
func cachedLevels(ctx context.Context, store storage.Store, productID core.ID) (map[core.ID]int64, error) {
	levels, err := store.Movements().Levels(ctx, productID)
	if err != nil {
		return nil, err
	}
	out := make(map[core.ID]int64, len(levels))
	for _, l := range levels {
		out[l.LocationID] = l.OnHand
	}
	return out, nil
}

// ledgerLevels sums the ledger itself, which is what the cache is supposed to
// equal.
func ledgerLevels(ctx context.Context, store storage.Store, productID core.ID) (map[core.ID]int64, error) {
	movements, err := store.Movements().List(ctx, storage.MovementFilter{ProductID: productID})
	if err != nil {
		return nil, err
	}
	out := map[core.ID]int64{}
	for _, m := range movements {
		out[m.LocationID] += m.QtyDelta
	}
	// A location that nets to zero is not cached, so drop it before comparing.
	for location, qty := range out {
		if qty == 0 {
			delete(out, location)
		}
	}
	return out, nil
}

type levelMismatch struct {
	location core.ID
	cached   int64
	actual   int64
}

func compareLevels(cached, actual map[core.ID]int64) []levelMismatch {
	seen := map[core.ID]bool{}
	var out []levelMismatch

	for location, want := range actual {
		seen[location] = true
		if got := cached[location]; got != want {
			out = append(out, levelMismatch{location: location, cached: got, actual: want})
		}
	}
	for location, got := range cached {
		if !seen[location] && got != 0 {
			out = append(out, levelMismatch{location: location, cached: got, actual: 0})
		}
	}
	return out
}

// pendingMigrations names the migrations this build would apply, or nothing if
// the backend does not report schema state.
func pendingMigrations(ctx context.Context, store storage.Store) ([]string, error) {
	reporter, ok := store.(storage.SchemaReporter)
	if !ok {
		return nil, nil
	}
	return reporter.PendingMigrations(ctx)
}

// requireCurrentSchema refuses to read a database whose schema this build has
// not finished migrating, so the failure names the fix instead of surfacing a
// missing-table error from the driver.
func requireCurrentSchema(ctx context.Context, store storage.Store) error {
	pending, err := pendingMigrations(ctx, store)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("the database has %d pending migration(s); run `inventoryctl migrate` first", len(pending))
	}
	return nil
}

// openService builds the service layer over the configured backend, which the
// data commands need and the schema commands do not.
func openService(ctx context.Context) (*service.Inventory, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{SkipMigrate: true})
	if err != nil {
		return nil, nil, err
	}
	if err := requireCurrentSchema(ctx, store); err != nil {
		_ = store.Close()
		return nil, nil, err
	}

	svc := service.NewInventory(store, service.WithDefaultCurrency(core.Currency(cfg.Currency)))
	return svc, func() { _ = store.Close() }, nil
}

// cmdImport loads a CSV. It previews by default: an import that rewrites a
// catalogue without showing what it will change first is not something to
// offer from a terminal either.
func cmdImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the changes; without this the run is a preview only")
	update := fs.Bool("update", false, "update products whose SKU already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: inventoryctl import [-apply] [-update] <file.csv>")
	}

	file, err := os.Open(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("opening %s: %w", fs.Arg(0), err)
	}
	defer func() { _ = file.Close() }()

	svc, closeStore, err := openService(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	result, err := svc.ImportCSV(ctx, file, service.ImportOptions{
		DryRun:         !*apply,
		UpdateExisting: *update,
	})
	if err != nil {
		return err
	}

	fmt.Println(result.Summary())
	if len(result.Mapped) > 0 {
		fmt.Println("  columns recognised:", strings.Join(result.Mapped, ", "))
	}
	if len(result.Ignored) > 0 {
		fmt.Println("  columns ignored:   ", strings.Join(result.Ignored, ", "))
	}
	for _, problem := range result.Problems {
		fmt.Println("  " + problem.Error())
	}
	if !*apply {
		fmt.Println("\nThis was a preview. Re-run with -apply to write the changes.")
	}
	return nil
}

func cmdExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	category := fs.String("category", "", "export only this category")
	supplier := fs.String("supplier", "", "export only this supplier")
	archived := fs.Bool("include-archived", false, "include archived products")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: inventoryctl export [-category X] [-supplier Y] <file.csv>")
	}

	svc, closeStore, err := openService(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	file, err := os.Create(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("creating %s: %w", fs.Arg(0), err)
	}
	defer func() { _ = file.Close() }()

	count, err := svc.ExportCSV(ctx, file, storage.ProductFilter{
		Category:        *category,
		Supplier:        *supplier,
		IncludeInactive: *archived,
	})
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", fs.Arg(0), err)
	}

	fmt.Printf("Wrote %d product(s) to %s.\n", count, fs.Arg(0))
	return nil
}

func cmdTemplate(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("template", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: inventoryctl template <file.csv>")
	}

	file, err := os.Create(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("creating %s: %w", fs.Arg(0), err)
	}
	defer func() { _ = file.Close() }()

	if err := service.CSVTemplate(file); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", fs.Arg(0), err)
	}

	fmt.Printf("Wrote an import template to %s.\n", fs.Arg(0))
	return nil
}

// cmdImportClientData loads a customer's stores or orders. Both take the same
// arguments and preview by default, so they share one implementation.
func cmdImportClientData(ctx context.Context, args []string, command string) error {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	customerCode := fs.String("customer", "", "the customer code these rows belong to (required)")
	apply := fs.Bool("apply", false, "write the changes; without this the run is a preview only")
	update := fs.Bool("update", false, "update records that already exist")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *customerCode == "" {
		return fmt.Errorf("usage: inventoryctl %s -customer CODE [-apply] [-update] <file.csv>", command)
	}

	file, err := os.Open(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("opening %s: %w", fs.Arg(0), err)
	}
	defer func() { _ = file.Close() }()

	svc, closeStore, err := openService(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	customer, err := svc.CustomerByCode(ctx, *customerCode)
	if err != nil {
		return err
	}

	opts := service.ImportOptions{DryRun: !*apply, UpdateExisting: *update}
	var result service.ImportResult
	if command == "import-stores" {
		result, err = svc.ImportStoresCSV(ctx, customer.ID, file, opts)
	} else {
		result, err = svc.ImportOrdersCSV(ctx, customer.ID, file, opts)
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s — %s\n", customer.Name, result.Summary())
	if len(result.Mapped) > 0 {
		fmt.Println("  columns recognised:", strings.Join(result.Mapped, ", "))
	}
	if len(result.Ignored) > 0 {
		fmt.Println("  columns ignored:   ", strings.Join(result.Ignored, ", "))
	}
	for _, problem := range result.Problems {
		fmt.Println("  " + problem.Error())
	}
	if !*apply {
		fmt.Println("\nThis was a preview. Re-run with -apply to write the changes.")
	}
	return nil
}

// cmdOrders prints the order book, which is what somebody wants from a
// terminal when they are asked "where are we on Macy's".
func cmdOrders(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("orders", flag.ContinueOnError)
	customerCode := fs.String("customer", "", "limit to one customer code")
	late := fs.Bool("late", false, "only orders past their cancel date")
	all := fs.Bool("all", false, "include shipped and cancelled orders")
	if err := fs.Parse(args); err != nil {
		return err
	}

	svc, closeStore, err := openService(ctx)
	if err != nil {
		return err
	}
	defer closeStore()

	filter := storage.OrderFilter{OpenOnly: !*all, LateOnly: *late}
	if *customerCode != "" {
		customer, err := svc.CustomerByCode(ctx, *customerCode)
		if err != nil {
			return err
		}
		filter.CustomerID = customer.ID
	}

	page, err := svc.ListOrders(ctx, filter)
	if err != nil {
		return err
	}
	if len(page.Items) == 0 {
		fmt.Println("No matching orders.")
		return nil
	}

	now := time.Now()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PO\tCUSTOMER\tSTORE\tSTATUS\tOUTSTANDING\tSHIP BY\tCANCEL AFTER\tVALUE")

	for _, order := range page.Items {
		flag := ""
		if order.Late(now) {
			flag = "  LATE"
		}
		fmt.Fprintf(w, "%s\t%s\t%s %s\t%s\t%d\t%s\t%s%s\t%s\n",
			order.CustomerPONumber, order.CustomerName,
			order.StoreCode, order.StoreName, order.Status.Label(),
			order.Totals.Outstanding,
			formatDateOrDash(order.RequestedShipDate),
			formatDateOrDash(order.CancelAfterDate), flag,
			order.Totals.Value.Display())
	}
	if err := w.Flush(); err != nil {
		return err
	}

	fmt.Printf("\n%d order(s).\n", page.Total)
	return nil
}

func formatDateOrDash(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("2006-01-02")
}

func cmdBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: inventoryctl backup <destination-file>")
	}
	dest := fs.Arg(0)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{SkipMigrate: true})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	backupper, ok := store.(storage.Backupper)
	if !ok {
		return fmt.Errorf("the %s backend does not support file backups; use its own tooling, such as pg_dump", cfg.Driver)
	}
	if err := backupper.BackupTo(ctx, dest); err != nil {
		return err
	}

	info, err := os.Stat(dest)
	if err != nil {
		return fmt.Errorf("backup written but could not be inspected: %w", err)
	}
	fmt.Printf("Wrote %s (%.1f MiB).\n", dest, float64(info.Size())/(1<<20))
	return nil
}
