-- Expand the product master to what a working business actually needs, and
-- record cost on the ledger so stock can be valued.
--
-- Every column is added with a default so the migration is safe on a database
-- that already holds products.

ALTER TABLE products ADD COLUMN barcode          TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN category         TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN supplier         TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN tags             TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN notes            TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN cost_minor       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN unit             TEXT    NOT NULL DEFAULT 'each';
ALTER TABLE products ADD COLUMN non_stock        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN track_lots       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN reorder_point    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN reorder_quantity INTEGER NOT NULL DEFAULT 0;
ALTER TABLE products ADD COLUMN image_path       TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN custom_fields    TEXT    NOT NULL DEFAULT '';
ALTER TABLE products ADD COLUMN weight_grams     INTEGER NOT NULL DEFAULT 0;

-- A barcode identifies exactly one product, but most products have none, so
-- the uniqueness applies only to rows that set it.
CREATE UNIQUE INDEX ux_products_barcode
    ON products (barcode COLLATE NOCASE)
    WHERE barcode <> '';

CREATE INDEX ix_products_category ON products (category COLLATE NOCASE);
CREATE INDEX ix_products_supplier ON products (supplier COLLATE NOCASE);

-- Reorder screens ask "which stocked items are at or below their point", so
-- the index carries both columns.
CREATE INDEX ix_products_reorder ON products (non_stock, reorder_point);

-- Cost on the ledger row is what makes weighted-average and FIFO valuation
-- computable after the fact. A single cost field on the product can only ever
-- answer what an item costs now, never what the shelf is worth.
ALTER TABLE stock_movements ADD COLUMN unit_cost_minor INTEGER NOT NULL DEFAULT 0;
ALTER TABLE stock_movements ADD COLUMN currency        TEXT    NOT NULL DEFAULT 'USD';
ALTER TABLE stock_movements ADD COLUMN lot_number      TEXT    NOT NULL DEFAULT '';
ALTER TABLE stock_movements ADD COLUMN expiry_date     TEXT    NOT NULL DEFAULT '';

CREATE INDEX ix_movements_lot    ON stock_movements (lot_number) WHERE lot_number <> '';
CREATE INDEX ix_movements_expiry ON stock_movements (expiry_date) WHERE expiry_date <> '';

-- Application settings that belong to the database rather than the machine:
-- a valuation method is an accounting policy every workstation must share, not
-- a per-user preference.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO settings (key, value, updated_at) VALUES
    ('valuation_method', 'weighted_average', '2024-01-01T00:00:00Z');
