-- Initial schema.
--
-- Design notes that later migrations must respect:
--   * Identifiers are UUIDv7 stored as TEXT, identical across backends.
--   * Money is stored as whole minor units in an INTEGER column, never REAL.
--   * Timestamps are RFC3339 UTC strings.
--   * On-hand stock is derived from stock_movements; stock_levels is a cache
--     maintained in the same transaction as the ledger write.

CREATE TABLE locations (
    id         TEXT    PRIMARY KEY,
    code       TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    is_default INTEGER NOT NULL DEFAULT 0,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    version    INTEGER NOT NULL DEFAULT 1
);

CREATE UNIQUE INDEX ux_locations_code ON locations (code COLLATE NOCASE);

-- At most one location may be flagged default.
CREATE UNIQUE INDEX ux_locations_default ON locations (is_default) WHERE is_default = 1;

CREATE TABLE products (
    id          TEXT    PRIMARY KEY,
    sku         TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    description TEXT    NOT NULL DEFAULT '',
    price_minor INTEGER NOT NULL DEFAULT 0,
    currency    TEXT    NOT NULL DEFAULT 'USD',
    active      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL,
    version     INTEGER NOT NULL DEFAULT 1
);

-- SKUs are matched case-insensitively: "abc-1" and "ABC-1" are the same item
-- to everyone except the database, and that mismatch creates duplicates.
CREATE UNIQUE INDEX ux_products_sku ON products (sku COLLATE NOCASE);
CREATE INDEX ix_products_name ON products (name COLLATE NOCASE);
CREATE INDEX ix_products_active ON products (active);

CREATE TABLE stock_movements (
    id          TEXT    PRIMARY KEY,
    product_id  TEXT    NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    location_id TEXT    NOT NULL REFERENCES locations (id) ON DELETE RESTRICT,
    qty_delta   INTEGER NOT NULL,
    reason      TEXT    NOT NULL,
    note        TEXT    NOT NULL DEFAULT '',
    ref_type    TEXT    NOT NULL DEFAULT '',
    ref_id      TEXT,
    actor_id    TEXT,
    occurred_at TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    CHECK (qty_delta <> 0)
);

CREATE INDEX ix_movements_product  ON stock_movements (product_id, occurred_at);
CREATE INDEX ix_movements_location ON stock_movements (location_id, occurred_at);
CREATE INDEX ix_movements_ref      ON stock_movements (ref_type, ref_id);
CREATE INDEX ix_movements_occurred ON stock_movements (occurred_at);

-- The ledger is append-only. A mistake is corrected by posting an offsetting
-- entry, never by editing history. Enforced in the database so that a bug in
-- the application layer cannot quietly rewrite an audit trail.
CREATE TRIGGER trg_movements_no_update
BEFORE UPDATE ON stock_movements
BEGIN
    SELECT RAISE(ABORT, 'stock_movements is append-only');
END;

CREATE TRIGGER trg_movements_no_delete
BEFORE DELETE ON stock_movements
BEGIN
    SELECT RAISE(ABORT, 'stock_movements is append-only');
END;

CREATE TABLE stock_levels (
    product_id  TEXT    NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    location_id TEXT    NOT NULL REFERENCES locations (id) ON DELETE CASCADE,
    on_hand     INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT    NOT NULL,
    PRIMARY KEY (product_id, location_id)
);

CREATE INDEX ix_levels_location ON stock_levels (location_id);

-- Every install has one location from the start, so single-site users never
-- have to think about locations and multi-site support needs no backfill.
INSERT INTO locations (id, code, name, is_default, active, created_at, updated_at, version)
VALUES (
    '01930000-0000-7000-8000-000000000001',
    'MAIN',
    'Main Location',
    1,
    1,
    '2024-01-01T00:00:00Z',
    '2024-01-01T00:00:00Z',
    1
);
