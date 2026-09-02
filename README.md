# InventorySys

A desktop application for a **sourcing and distribution business**: you agree a
program with a retail client, source and import the goods, hold them, then ship
against one purchase order per store.

> **Status: Phases 0–2 complete.** The foundation, the product master and stock
> intelligence, and the client side — customers, their stores, programs and
> store POs — are built. Allocation, shipping and client documents are next.
> See [Roadmap](#roadmap).

## What problem it solves

When a client asks *"where is the product for store 47?"*, the answer today
spans a factory overseas, a container on the water, your warehouse and a truck —
and lives across email, spreadsheets and somebody's memory. This application
exists to make that one coherent record.

The organizing structure is:

```
Customer  →  Store  →  Store PO  →  Lines
   Macy's      0047      MCY-0123     240 × Sherpa Throw
```

A client raises a separate PO per door — `MCY-0123` for one store, `MCY-0124`
for the next — and a **program** groups them so head office can see the whole
buy at once.

## Running it

```sh
make run           # launch against your real data directory
make run-sandbox   # launch against a throwaway database under ./local
make demo          # fill the sandbox with a year of realistic data
make shots         # render every screen to ./local/shots, no display needed
make check         # formatting, vet and the full test suite
```

To see the application with something in it:

```sh
make run-sandbox &   # creates ./local and migrates it
make demo            # 30 products, 3 retail clients, 9 stores, 13 store POs
```

Requires Go 1.26 or newer. On Linux you also need the GUI development headers:

```sh
sudo apt-get install -y gcc libgl1-mesa-dev libxi-dev libxcursor-dev \
  libxrandr-dev libxinerama-dev libxkbcommon-dev xorg-dev
```

## Where your data lives

The database, configuration and logs are written to the OS-standard
per-user location, not the working directory:

| Platform | Path |
| --- | --- |
| macOS | `~/Library/Application Support/InventorySys/` |
| Linux | `~/.config/InventorySys/` |
| Windows | `%AppData%\InventorySys\` |

Override it for one run with `INVENTORY_DATA_DIR`.

## What it does

**The client side.** Customers with their own currency and terms; a store list
per customer with full ship-to addresses, receiving contacts and **routing
notes** — the delivery requirements large retailers charge back for getting
wrong. Programs group a season's buy. Store POs carry the client's own PO
number, which is unique per customer and is the first thing the search box
resolves, because that is what a client says on the phone.

**Order intake.** One spreadsheet from a client becomes one order per store:
rows are grouped by PO number, store codes are matched against that client's
doors, and headings are read loosely so a file exported from the retailer's own
system usually works unchanged. Every import previews before it writes.

**Order coverage.** For every open order line, is there stock to ship it? What
is short, how much does closing the gap cost, and which date is it needed by.
This replaces reorder points for this business — demand is not a forecast, it
is sitting in signed purchase orders.

**Product master.** SKU, barcode, name, description, category, supplier, tags,
custom fields, notes, unit of measure, weight, sale price *and* unit cost,
reorder point and reorder quantity, non-stock items for services, and optional
lot/batch tracking with expiry dates.

**Stock control.** Receive, issue and count as three distinct operations, each
recording why. Manual adjustments with reason codes. Every change is an
immutable ledger entry; the database itself rejects any attempt to edit or
delete one.

**Reports.**

| Report | Answers |
| --- | --- |
| Order coverage | Can we ship what we have promised, and what is short |
| Valuation | What is the stock worth, by FIFO or weighted average cost, and what has been issued out |
| What to reorder | What has fallen to its reorder point, how much to buy, grouped by supplier |
| ABC analysis | Which items hold 80% of the value and deserve the closest attention |
| Stock aging | What has not moved, and how much capital is sitting in it |
| Expiring lots | Which batches are close to their date, and which are past it |

**Working with data.** CSV import with a mandatory preview, per-row error
reporting, and header matching that accepts the spellings other systems export
(`Qty`, `Item Code`, `UPC`, `Min Stock`…). CSV export of whatever the filter bar
is currently showing. A downloadable template.

**In the application.** A dashboard that only raises alerts worth acting on, a
sortable and filterable catalogue with an inspector panel, a global scan box
that resolves a barcode from any screen, full keyboard shortcuts, and a light
and dark theme.

## Administration

`inventoryctl` administers a database without the GUI:

```sh
make ctl ARGS=status                    # configuration, schema version, record counts
make ctl ARGS="migrate --dry-run"       # list pending schema migrations
make ctl ARGS=migrate                   # apply them
make ctl ARGS=verify                    # check cached stock levels against the ledger
make ctl ARGS="verify -fix"             # rebuild any that disagree
make ctl ARGS="backup snapshot.db"      # consistent snapshot to a file
make ctl ARGS="import catalogue.csv"    # preview an import
make ctl ARGS="import -apply -update catalogue.csv"
make ctl ARGS="export -category Tools out.csv"
make ctl ARGS="template blank.csv"      # a file to fill in

make ctl ARGS="orders -customer MACYS"  # the order book for one client
make ctl ARGS="orders -late"            # anything past its cancel date
make ctl ARGS="import-stores -customer MACYS -apply doors.csv"
make ctl ARGS="import-orders -customer MACYS -apply po-sheet.csv"
```

## Configuration

`config.json` in the data directory, with environment overrides:

| Setting | Environment variable | Default |
| --- | --- | --- |
| `driver` | `INVENTORY_DRIVER` | `sqlite` |
| `dsn` | `INVENTORY_DSN` | the database file in the data directory |
| `currency` | `INVENTORY_CURRENCY` | `USD` |
| `log_level` | `INVENTORY_LOG_LEVEL` | `info` |

Set `INVENTORY_CONSOLE_LOG=1` to mirror the log to stderr while developing.

## Architecture

```
cmd/inventory        desktop client
cmd/inventoryctl     admin CLI
cmd/demoseed         fills a sandbox database with a realistic year of data
internal/core        domain types and rules — no SQL, no Fyne
internal/storage     the persistence contract
  ├── migrate        embedded, versioned, forward-only migrations
  ├── sqlite         the SQLite backend
  └── storetest      the conformance suite every backend must pass
internal/service     business rules, transactions, and later permissions and audit
internal/bootstrap   configuration to a concrete backend
internal/ui          Fyne desktop UI
internal/config      data directories and settings
internal/logging     rotating structured logs
```

Four decisions shape everything above:

**Money is an integer.** `core.Money` holds whole minor units — cents for USD,
yen for JPY — and refuses arithmetic across currencies. A `REAL` price column
loses fractions of a cent, and at a few thousand rows that becomes a valuation
report that does not reconcile. Parsing is strict: `"not-a-price"` is an error,
not a zero, and a comma is always a thousands separator so an amount is never
silently moved by two decimal places.

**Stock is derived, never assigned.** There is no quantity column on a product.
On-hand is the sum of an append-only `stock_movements` ledger, cached in
`stock_levels` and updated in the same transaction. Every change to a level has
a reason, an actor and a timestamp, and the database itself rejects any attempt
to update or delete a ledger row. `inventoryctl verify` proves the cache still
matches the ledger.

**Storage is a contract, not an implementation.** Everything programs against
the interfaces in `internal/storage`, and every backend is validated by the
single conformance suite in `internal/storage/storetest`. That is what keeps
"SQLite for one person, Postgres for a team" an honest promise: a behaviour
that differs between backends fails the build.

**Nothing touches the database on the UI goroutine.** Fyne redraws there, so a
query that takes a second freezes the window for a second. Work runs in a
goroutine with a bounded timeout and comes back through an explicit dispatch
seam, which tests replace with inline calls so they are deterministic.

**Cost lives on the ledger, not only on the product.** Each movement records
what a unit cost at the time. A single cost field on a product can answer what
an item costs today; only the ledger can answer what the shelf is worth, and
that is what lets the valuation method be changed and history re-valued. It is
also what makes landed cost possible when importing arrives in Phase 6.

**A customer's stores are not your locations.** `locations` holds the warehouses
where stock physically sits; `customer_stores` holds the doors goods are sent
to. They are separate tables because conflating "where it is" with "where it is
going" would make on-hand meaningless.

**A PO number is unique per customer, not globally.** Clients number their own
paperwork and cannot be expected to coordinate, so two retailers may both raise
a `PO-777`. The search box resolves across customers and will show both.

## Roadmap

| Phase | Scope | Status |
| --- | --- | --- |
| 0 | Foundation: module layout, money, ledger schema, migrations, storage contract, service layer, logging, CI | **done** |
| 1 | Product master, stock control, valuation and reports, CSV import/export, and the desktop UI | **done** |
| 2 | Customers, stores, programs, store POs, order intake and coverage | **done** |
| 3 | Allocation, shipping, and client documents (acknowledgements, packing lists, backorder reports) | next |
| 4 | Users, roles and audit trail |  |
| 5 | Sourcing: vendors, vendor POs, production tracking |  |
| 6 | Import: containers, landed cost, multi-currency — and the full timeline |  |
| 7 | Postgres backend and a hosted service |  |
| 8 | Client access: shared status links, then a portal with logins |  |
| 9 | Packaging: signed installers for macOS, Windows and Linux |  |

**The feature the product is for** lands in Phase 6: one timeline per store PO,
running program → vendor → container → warehouse → allocation → store delivery.
Phases 3–5 build the pieces it joins up.

Deliberately not built yet, and worth knowing about: **EDI** (850/856), which
large retailers often require and which is a project of its own; **invoicing and
accounts receivable**; product **variants** and **kits**; **barcode label
printing**; and **lot-level allocation** — lots are recorded and reportable
today, but issuing stock does not yet consume a specific lot.

## Testing

```sh
make test    # race detector, all packages
make cover   # writes coverage.html
```

The conformance suite in `internal/storage/storetest` is the important one. It
runs against every backend and covers case-insensitive SKU matching, optimistic
concurrency, transaction rollback, exact price round-tripping, barcode
uniqueness, tag filtering, the ledger remaining the source of truth for stock,
store codes being unique per customer rather than globally, PO numbers being
unique per customer, and a shipped order line being impossible to delete.

The UI is tested against Fyne's headless driver: every screen is constructed and
navigated, which is what catches a widget callback firing during construction —
a class of bug that is invisible to the compiler and fatal on first launch.

`make shots` renders every screen to a PNG without a display, which is how the
layout gets reviewed.
