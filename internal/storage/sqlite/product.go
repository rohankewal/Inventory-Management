package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type productRepo struct{ s *Store }

const productColumns = `p.id, p.sku, p.barcode, p.name, p.description, p.category,
	p.supplier, p.tags, p.notes, p.price_minor, p.cost_minor, p.currency, p.unit,
	p.non_stock, p.track_lots, p.reorder_point, p.reorder_quantity,
	p.image_path, p.custom_fields, p.weight_grams, p.active,
	p.created_at, p.updated_at, p.version`

func (r *productRepo) Create(ctx context.Context, p *core.Product) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID.IsZero() {
		p.ID = core.NewID()
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	p.Version = 1

	customFields, err := p.CustomFields.Encode()
	if err != nil {
		return fmt.Errorf("create product %q: encoding custom fields: %w", p.SKU, err)
	}

	_, err = r.s.write().ExecContext(ctx, `
		INSERT INTO products
			(id, sku, barcode, name, description, category, supplier, tags, notes,
			 price_minor, cost_minor, currency, unit, non_stock, track_lots,
			 reorder_point, reorder_quantity, image_path, custom_fields, weight_grams,
			 active, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.SKU, p.Barcode, p.Name, p.Description, p.Category, p.Supplier,
		p.Tags.Storage(), p.Notes, p.Price.Minor, p.Cost.Minor, string(p.Price.Currency),
		string(p.Unit), boolToInt(p.NonStock), boolToInt(p.TrackLots),
		p.ReorderPoint, p.ReorderQuantity, p.ImagePath, customFields, p.WeightGrams,
		boolToInt(p.Active), fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt), p.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create product %q", p.SKU))
	}
	return nil
}

func (r *productRepo) Update(ctx context.Context, p *core.Product) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}
	if p.ID.IsZero() {
		return fmt.Errorf("update product: %w: missing id", core.ErrInvalid)
	}

	customFields, err := p.CustomFields.Encode()
	if err != nil {
		return fmt.Errorf("update product %s: encoding custom fields: %w", p.ID, err)
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE products SET
			sku = ?, barcode = ?, name = ?, description = ?, category = ?,
			supplier = ?, tags = ?, notes = ?, price_minor = ?, cost_minor = ?,
			currency = ?, unit = ?, non_stock = ?, track_lots = ?,
			reorder_point = ?, reorder_quantity = ?, image_path = ?,
			custom_fields = ?, weight_grams = ?, active = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		p.SKU, p.Barcode, p.Name, p.Description, p.Category, p.Supplier,
		p.Tags.Storage(), p.Notes, p.Price.Minor, p.Cost.Minor, string(p.Price.Currency),
		string(p.Unit), boolToInt(p.NonStock), boolToInt(p.TrackLots),
		p.ReorderPoint, p.ReorderQuantity, p.ImagePath, customFields, p.WeightGrams,
		boolToInt(p.Active), fmtTime(updatedAt), p.ID, p.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update product %s", p.ID))
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update product %s: %w", p.ID, err)
	}
	if n == 0 {
		// Nothing matched: either the row is gone or somebody else saved
		// first. Distinguishing the two is what lets the UI say which.
		if _, getErr := r.Get(ctx, p.ID); getErr != nil {
			return getErr
		}
		return fmt.Errorf("update product %s: %w: the record was changed by someone else since you opened it",
			p.ID, core.ErrConflict)
	}

	p.UpdatedAt = updatedAt
	p.Version++
	return nil
}

func (r *productRepo) Delete(ctx context.Context, id core.ID) error {
	res, err := r.s.write().ExecContext(ctx, `DELETE FROM products WHERE id = ?`, id)
	if err != nil {
		return mapError(err, fmt.Sprintf("delete product %s", id))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete product %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("delete product %s: %w", id, core.ErrNotFound)
	}
	return nil
}

func (r *productRepo) SetActive(ctx context.Context, id core.ID, active bool) error {
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE products SET active = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
		boolToInt(active), fmtTime(time.Now().UTC()), id)
	if err != nil {
		return mapError(err, fmt.Sprintf("set product %s active", id))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set product %s active: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("set product %s active: %w", id, core.ErrNotFound)
	}
	return nil
}

func (r *productRepo) Get(ctx context.Context, id core.ID) (core.Product, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+productColumns+` FROM products p WHERE p.id = ?`, id)
	p, err := scanProduct(row.Scan)
	if err != nil {
		return core.Product{}, mapError(err, fmt.Sprintf("get product %s", id))
	}
	return p, nil
}

func (r *productRepo) GetBySKU(ctx context.Context, sku string) (core.Product, error) {
	sku = strings.TrimSpace(sku)
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+productColumns+` FROM products p WHERE p.sku = ? COLLATE NOCASE`, sku)
	p, err := scanProduct(row.Scan)
	if err != nil {
		return core.Product{}, mapError(err, fmt.Sprintf("get product by sku %q", sku))
	}
	return p, nil
}

func (r *productRepo) GetByBarcode(ctx context.Context, barcode string) (core.Product, error) {
	barcode = strings.TrimSpace(barcode)
	if barcode == "" {
		return core.Product{}, fmt.Errorf("get product by barcode: %w: no code given", core.ErrNotFound)
	}
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+productColumns+` FROM products p WHERE p.barcode = ? COLLATE NOCASE AND p.barcode <> ''`,
		barcode)
	p, err := scanProduct(row.Scan)
	if err != nil {
		return core.Product{}, mapError(err, fmt.Sprintf("get product by barcode %q", barcode))
	}
	return p, nil
}

func (r *productRepo) List(ctx context.Context, f storage.ProductFilter) ([]core.ProductWithStock, error) {
	where, args := productWhere(f)

	query := `
		SELECT ` + productColumns + `, COALESCE(sl.on_hand, 0) AS on_hand
		FROM products p
		LEFT JOIN stock_levels sl ON sl.product_id = p.id AND sl.location_id = ?` +
		where + productOrderBy(f)

	// The join's location parameter precedes every WHERE parameter.
	args = append([]any{locationOrDefault(f.LocationID)}, args...)
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list products")
	}
	defer func() { _ = rows.Close() }()

	var out []core.ProductWithStock
	for rows.Next() {
		var pw core.ProductWithStock
		p, err := scanProduct(func(dest ...any) error {
			return rows.Scan(append(dest, &pw.OnHand)...)
		})
		if err != nil {
			return nil, fmt.Errorf("list products: %w", err)
		}
		pw.Product = p
		out = append(out, pw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return out, nil
}

func (r *productRepo) Count(ctx context.Context, f storage.ProductFilter) (int, error) {
	where, args := productWhere(f)
	query := `
		SELECT COUNT(*)
		FROM products p
		LEFT JOIN stock_levels sl ON sl.product_id = p.id AND sl.location_id = ?` + where
	args = append([]any{locationOrDefault(f.LocationID)}, args...)

	var n int
	if err := r.s.read().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, mapError(err, "count products")
	}
	return n, nil
}

func (r *productRepo) Categories(ctx context.Context) ([]string, error) {
	return r.distinct(ctx, "category")
}

func (r *productRepo) Suppliers(ctx context.Context) ([]string, error) {
	return r.distinct(ctx, "supplier")
}

// distinct returns the non-empty values of one column, which drives the filter
// bar and the edit form's autocomplete. The column name is chosen by this
// package, never by a caller, so it cannot come from user input.
func (r *productRepo) distinct(ctx context.Context, column string) ([]string, error) {
	rows, err := r.s.read().QueryContext(ctx, fmt.Sprintf(`
		SELECT DISTINCT %s FROM products
		WHERE %s <> ''
		ORDER BY %s COLLATE NOCASE`, column, column, column))
	if err != nil {
		return nil, mapError(err, "list "+column+" values")
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("list %s values: %w", column, err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list %s values: %w", column, err)
	}
	return out, nil
}

func (r *productRepo) Tags(ctx context.Context) ([]string, error) {
	rows, err := r.s.read().QueryContext(ctx,
		`SELECT tags FROM products WHERE tags <> ''`)
	if err != nil {
		return nil, mapError(err, "list tags")
	}
	defer func() { _ = rows.Close() }()

	// Tags live in one delimited column, so the distinct set is assembled here
	// rather than by the database. The catalogue sizes this has to handle make
	// that cheaper than maintaining a join table.
	seen := map[string]string{}
	for rows.Next() {
		var stored string
		if err := rows.Scan(&stored); err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		for _, tag := range core.ParseTagStorage(stored) {
			seen[strings.ToLower(tag)] = tag
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}

	out := make([]string, 0, len(seen))
	for _, tag := range seen {
		out = append(out, tag)
	}
	sortStringsFold(out)
	return out, nil
}

// productWhere builds the shared predicate for List and Count so the two can
// never drift apart and report different totals.
func productWhere(f storage.ProductFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if !f.IncludeInactive {
		clauses = append(clauses, "p.active = 1")
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses,
			`(p.sku LIKE ? ESCAPE '\' OR p.name LIKE ? ESCAPE '\' OR p.barcode LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if category := strings.TrimSpace(f.Category); category != "" {
		clauses = append(clauses, "p.category = ? COLLATE NOCASE")
		args = append(args, category)
	}
	if supplier := strings.TrimSpace(f.Supplier); supplier != "" {
		clauses = append(clauses, "p.supplier = ? COLLATE NOCASE")
		args = append(args, supplier)
	}
	if tag := strings.TrimSpace(f.Tag); tag != "" {
		clauses = append(clauses, `p.tags LIKE ? ESCAPE '\'`)
		args = append(args, core.TagPattern(escapeLike(tag)))
	}
	if f.NonStockOnly {
		clauses = append(clauses, "p.non_stock = 1")
	}

	switch f.Stock {
	case storage.StockNeedsReorder:
		// Each product is compared against its own reorder point. One
		// threshold across a catalogue is meaningless: the right number for
		// screws is wrong for engines.
		clauses = append(clauses,
			"p.non_stock = 0 AND COALESCE(sl.on_hand, 0) <= p.reorder_point")
	case storage.StockOut:
		clauses = append(clauses, "p.non_stock = 0 AND COALESCE(sl.on_hand, 0) <= 0")
	case storage.StockInStock:
		clauses = append(clauses, "p.non_stock = 0 AND COALESCE(sl.on_hand, 0) > 0")
	case storage.StockNegative:
		clauses = append(clauses, "p.non_stock = 0 AND COALESCE(sl.on_hand, 0) < 0")
	}

	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// productOrderBy maps the sort enum onto a column. The trailing tiebreak on id
// keeps paging stable when many rows share a sort value.
func productOrderBy(f storage.ProductFilter) string {
	col := "p.name COLLATE NOCASE"
	switch f.Sort {
	case storage.SortProductSKU:
		col = "p.sku COLLATE NOCASE"
	case storage.SortProductBarcode:
		col = "p.barcode COLLATE NOCASE"
	case storage.SortProductCategory:
		col = "p.category COLLATE NOCASE"
	case storage.SortProductSupplier:
		col = "p.supplier COLLATE NOCASE"
	case storage.SortProductPrice:
		col = "p.price_minor"
	case storage.SortProductCost:
		col = "p.cost_minor"
	case storage.SortProductStock:
		col = "on_hand"
	case storage.SortProductValue:
		col = "(COALESCE(sl.on_hand, 0) * p.cost_minor)"
	case storage.SortProductStatus:
		// Order by urgency rather than alphabetically: out of stock first,
		// then low, then everything else. Sorting the status column by name
		// would put "In stock" above "Low", which is the wrong way round for
		// the one screen this sort exists to serve.
		col = `CASE
			WHEN p.non_stock = 1 THEN 3
			WHEN COALESCE(sl.on_hand, 0) <= 0 THEN 0
			WHEN p.reorder_point > 0 AND COALESCE(sl.on_hand, 0) <= p.reorder_point THEN 1
			ELSE 2 END`
	case storage.SortProductCreated:
		col = "p.created_at"
	case storage.SortProductUpdated:
		col = "p.updated_at"
	}
	return " ORDER BY " + col + " " + direction(f.Direction) + ", p.id ASC"
}

// scanProduct reads the standard product column list through any scan function,
// so row and rows scanning share one mapping.
func scanProduct(scan func(dest ...any) error) (core.Product, error) {
	var (
		p            core.Product
		priceMinor   int64
		costMinor    int64
		currency     string
		unit         string
		tags         string
		customFields string
		nonStock     int
		trackLots    int
		active       int
		created      string
		updated      string
	)
	err := scan(&p.ID, &p.SKU, &p.Barcode, &p.Name, &p.Description, &p.Category,
		&p.Supplier, &tags, &p.Notes, &priceMinor, &costMinor, &currency, &unit,
		&nonStock, &trackLots, &p.ReorderPoint, &p.ReorderQuantity,
		&p.ImagePath, &customFields, &p.WeightGrams, &active,
		&created, &updated, &p.Version)
	if err != nil {
		return core.Product{}, err
	}

	p.Price = core.NewMoney(priceMinor, core.Currency(currency))
	p.Cost = core.NewMoney(costMinor, core.Currency(currency))
	p.Unit = core.UnitOfMeasure(unit)
	p.Tags = core.ParseTagStorage(tags)
	p.CustomFields = core.DecodeCustomFields(customFields)
	p.NonStock = nonStock != 0
	p.TrackLots = trackLots != 0
	p.Active = active != 0

	if p.CreatedAt, err = parseTime(created); err != nil {
		return core.Product{}, err
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Product{}, err
	}
	return p, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func locationOrDefault(id core.ID) core.ID {
	if id.IsZero() {
		return core.DefaultLocationID
	}
	return id
}

var _ storage.ProductRepo = (*productRepo)(nil)
