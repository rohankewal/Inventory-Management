-- Customers, their stores, programs, and one purchase order per store.
--
-- This is the organizing spine of the application. A customer's stores are
-- ship-to destinations and are deliberately a separate concept from the
-- `locations` table, which holds the warehouses where stock physically sits.

CREATE TABLE customers (
    id                TEXT    PRIMARY KEY,
    code              TEXT    NOT NULL,
    name              TEXT    NOT NULL,
    currency          TEXT    NOT NULL DEFAULT 'USD',
    terms             TEXT    NOT NULL DEFAULT '',
    contact_name      TEXT    NOT NULL DEFAULT '',
    contact_email     TEXT    NOT NULL DEFAULT '',
    contact_phone     TEXT    NOT NULL DEFAULT '',
    bill_to_line1     TEXT    NOT NULL DEFAULT '',
    bill_to_line2     TEXT    NOT NULL DEFAULT '',
    bill_to_city      TEXT    NOT NULL DEFAULT '',
    bill_to_region    TEXT    NOT NULL DEFAULT '',
    bill_to_postal    TEXT    NOT NULL DEFAULT '',
    bill_to_country   TEXT    NOT NULL DEFAULT '',
    notes             TEXT    NOT NULL DEFAULT '',
    active            INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL,
    version           INTEGER NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX ux_customers_code ON customers (code COLLATE NOCASE);
CREATE INDEX ix_customers_name ON customers (name COLLATE NOCASE);

CREATE TABLE customer_stores (
    id                TEXT    PRIMARY KEY,
    customer_id       TEXT    NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
    code              TEXT    NOT NULL,
    name              TEXT    NOT NULL,
    ship_to_line1     TEXT    NOT NULL DEFAULT '',
    ship_to_line2     TEXT    NOT NULL DEFAULT '',
    ship_to_city      TEXT    NOT NULL DEFAULT '',
    ship_to_region    TEXT    NOT NULL DEFAULT '',
    ship_to_postal    TEXT    NOT NULL DEFAULT '',
    ship_to_country   TEXT    NOT NULL DEFAULT '',
    contact_name      TEXT    NOT NULL DEFAULT '',
    contact_email     TEXT    NOT NULL DEFAULT '',
    contact_phone     TEXT    NOT NULL DEFAULT '',
    routing_notes     TEXT    NOT NULL DEFAULT '',
    active            INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL,
    version           INTEGER NOT NULL DEFAULT 1
);

-- A store code is unique within its customer, not globally: two retailers both
-- numbering a store 001 is entirely normal.
CREATE UNIQUE INDEX ux_stores_customer_code
    ON customer_stores (customer_id, code COLLATE NOCASE);
CREATE INDEX ix_stores_customer ON customer_stores (customer_id, active);
CREATE INDEX ix_stores_name ON customer_stores (name COLLATE NOCASE);

CREATE TABLE programs (
    id                    TEXT    PRIMARY KEY,
    customer_id           TEXT    NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    code                  TEXT    NOT NULL,
    name                  TEXT    NOT NULL,
    season                TEXT    NOT NULL DEFAULT '',
    status                TEXT    NOT NULL DEFAULT 'draft',
    target_delivery_date  TEXT    NOT NULL DEFAULT '',
    notes                 TEXT    NOT NULL DEFAULT '',
    created_at            TEXT    NOT NULL,
    updated_at            TEXT    NOT NULL,
    version               INTEGER NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX ux_programs_customer_code
    ON programs (customer_id, code COLLATE NOCASE);
CREATE INDEX ix_programs_status ON programs (status);

CREATE TABLE store_orders (
    id                    TEXT    PRIMARY KEY,
    customer_id           TEXT    NOT NULL REFERENCES customers (id) ON DELETE RESTRICT,
    store_id              TEXT    NOT NULL REFERENCES customer_stores (id) ON DELETE RESTRICT,
    program_id            TEXT    REFERENCES programs (id) ON DELETE SET NULL,
    customer_po_number    TEXT    NOT NULL,
    status                TEXT    NOT NULL DEFAULT 'draft',
    currency              TEXT    NOT NULL DEFAULT 'USD',
    ordered_at            TEXT    NOT NULL DEFAULT '',
    requested_ship_date   TEXT    NOT NULL DEFAULT '',
    cancel_after_date     TEXT    NOT NULL DEFAULT '',
    notes                 TEXT    NOT NULL DEFAULT '',
    created_at            TEXT    NOT NULL,
    updated_at            TEXT    NOT NULL,
    version               INTEGER NOT NULL DEFAULT 1
);

-- The client's own PO number is how everyone refers to the order, so it must
-- be unique within the customer and fast to look up. Two different customers
-- happening to use the same number is not a conflict.
CREATE UNIQUE INDEX ux_orders_customer_po
    ON store_orders (customer_id, customer_po_number COLLATE NOCASE);
CREATE INDEX ix_orders_po       ON store_orders (customer_po_number COLLATE NOCASE);
CREATE INDEX ix_orders_store    ON store_orders (store_id);
CREATE INDEX ix_orders_program  ON store_orders (program_id);
CREATE INDEX ix_orders_status   ON store_orders (status, requested_ship_date);
CREATE INDEX ix_orders_cancel   ON store_orders (cancel_after_date) WHERE cancel_after_date <> '';

CREATE TABLE store_order_lines (
    id             TEXT    PRIMARY KEY,
    order_id       TEXT    NOT NULL REFERENCES store_orders (id) ON DELETE CASCADE,
    product_id     TEXT    NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    line_no        INTEGER NOT NULL,
    quantity       INTEGER NOT NULL,
    unit_price     INTEGER NOT NULL DEFAULT 0,
    currency       TEXT    NOT NULL DEFAULT 'USD',
    allocated_qty  INTEGER NOT NULL DEFAULT 0,
    shipped_qty    INTEGER NOT NULL DEFAULT 0,
    cancelled_qty  INTEGER NOT NULL DEFAULT 0,
    notes          TEXT    NOT NULL DEFAULT '',
    CHECK (quantity > 0),
    CHECK (allocated_qty >= 0 AND shipped_qty >= 0 AND cancelled_qty >= 0),
    -- Shipping more than was ordered is always a mistake, and catching it in
    -- the database means no code path can create the state.
    CHECK (shipped_qty + cancelled_qty <= quantity)
);

CREATE UNIQUE INDEX ux_order_lines_order_line ON store_order_lines (order_id, line_no);
CREATE INDEX ix_order_lines_product ON store_order_lines (product_id);

-- Ledger movements gain an order reference so a shipment can be traced back to
-- the store PO that caused it. ref_type/ref_id already exist for exactly this,
-- and the index makes "what shipped against MCY-0123" a single lookup.
CREATE INDEX ix_movements_order ON stock_movements (ref_id) WHERE ref_type = 'store_order';
