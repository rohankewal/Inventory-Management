package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type movementRepo struct{ s *Store }

const movementColumns = `id, product_id, location_id, qty_delta, reason, note,
	unit_cost_minor, currency, lot_number, expiry_date,
	ref_type, ref_id, actor_id, occurred_at, created_at`

// Append writes the ledger entry and folds it into the cached level. The two
// must happen together or the cache stops matching the ledger, so when the
// caller has not already opened a transaction, Append opens one itself.
func (r *movementRepo) Append(ctx context.Context, m *core.StockMovement) error {
	if m.LocationID.IsZero() {
		m.LocationID = core.DefaultLocationID
	}
	if err := m.Validate(); err != nil {
		return err
	}
	if m.ID.IsZero() {
		m.ID = core.NewID()
	}
	now := time.Now().UTC()
	if m.OccurredAt.IsZero() {
		m.OccurredAt = now
	}
	m.CreatedAt = now
	if m.UnitCost.Currency == "" {
		m.UnitCost.Currency = core.DefaultCurrency
	}

	if r.s.tx == nil {
		return r.s.InTx(ctx, func(st storage.Store) error {
			return st.Movements().Append(ctx, m)
		})
	}
	return r.appendInTx(ctx, m)
}

func (r *movementRepo) appendInTx(ctx context.Context, m *core.StockMovement) error {
	w := r.s.write()

	_, err := w.ExecContext(ctx, `
		INSERT INTO stock_movements
			(id, product_id, location_id, qty_delta, reason, note,
			 unit_cost_minor, currency, lot_number, expiry_date,
			 ref_type, ref_id, actor_id, occurred_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ProductID, m.LocationID, m.QtyDelta, string(m.Reason), m.Note,
		m.UnitCost.Minor, string(m.UnitCost.Currency), m.LotNumber, fmtDate(m.ExpiryDate),
		m.RefType, m.RefID, m.ActorID, fmtTime(m.OccurredAt), fmtTime(m.CreatedAt))
	if err != nil {
		return mapError(err, fmt.Sprintf("append stock movement for product %s", m.ProductID))
	}

	// Upsert rather than read-modify-write: the accumulate happens inside the
	// statement, so two concurrent appends cannot each read the same starting
	// value and lose one of the deltas.
	_, err = w.ExecContext(ctx, `
		INSERT INTO stock_levels (product_id, location_id, on_hand, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (product_id, location_id) DO UPDATE SET
			on_hand    = stock_levels.on_hand + excluded.on_hand,
			updated_at = excluded.updated_at`,
		m.ProductID, m.LocationID, m.QtyDelta, fmtTime(m.CreatedAt))
	if err != nil {
		return mapError(err, fmt.Sprintf("update stock level for product %s", m.ProductID))
	}
	return nil
}

func (r *movementRepo) List(ctx context.Context, f storage.MovementFilter) ([]core.StockMovement, error) {
	where, args := movementWhere(f)
	dir := "DESC"
	if f.Direction == storage.Ascending {
		dir = "ASC"
	}

	query := `SELECT ` + movementColumns + ` FROM stock_movements` + where +
		` ORDER BY occurred_at ` + dir + `, id ` + dir
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list stock movements")
	}
	defer func() { _ = rows.Close() }()

	var out []core.StockMovement
	for rows.Next() {
		m, err := scanMovement(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list stock movements: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stock movements: %w", err)
	}
	return out, nil
}

func (r *movementRepo) Count(ctx context.Context, f storage.MovementFilter) (int, error) {
	where, args := movementWhere(f)
	var n int
	err := r.s.read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM stock_movements`+where, args...).Scan(&n)
	if err != nil {
		return 0, mapError(err, "count stock movements")
	}
	return n, nil
}

func (r *movementRepo) OnHand(ctx context.Context, productID, locationID core.ID) (int64, error) {
	var onHand int64
	err := r.s.read().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(on_hand), 0) FROM stock_levels
		WHERE product_id = ? AND location_id = ?`,
		productID, locationOrDefault(locationID)).Scan(&onHand)
	if err != nil {
		return 0, mapError(err, fmt.Sprintf("read stock level for product %s", productID))
	}
	return onHand, nil
}

func (r *movementRepo) Levels(ctx context.Context, productID core.ID) ([]core.StockLevel, error) {
	rows, err := r.s.read().QueryContext(ctx, `
		SELECT product_id, location_id, on_hand, updated_at
		FROM stock_levels
		WHERE product_id = ? AND on_hand <> 0
		ORDER BY location_id`, productID)
	if err != nil {
		return nil, mapError(err, fmt.Sprintf("list stock levels for product %s", productID))
	}
	defer func() { _ = rows.Close() }()

	var out []core.StockLevel
	for rows.Next() {
		var (
			l       core.StockLevel
			updated string
		)
		if err := rows.Scan(&l.ProductID, &l.LocationID, &l.OnHand, &updated); err != nil {
			return nil, fmt.Errorf("list stock levels: %w", err)
		}
		if l.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stock levels: %w", err)
	}
	return out, nil
}

// Recompute rebuilds every cached level for a product straight from the ledger.
func (r *movementRepo) Recompute(ctx context.Context, productID core.ID) error {
	if r.s.tx == nil {
		return r.s.InTx(ctx, func(st storage.Store) error {
			return st.Movements().Recompute(ctx, productID)
		})
	}

	w := r.s.write()
	if _, err := w.ExecContext(ctx,
		`DELETE FROM stock_levels WHERE product_id = ?`, productID); err != nil {
		return mapError(err, fmt.Sprintf("clear stock levels for product %s", productID))
	}

	_, err := w.ExecContext(ctx, `
		INSERT INTO stock_levels (product_id, location_id, on_hand, updated_at)
		SELECT product_id, location_id, SUM(qty_delta), ?
		FROM stock_movements
		WHERE product_id = ?
		GROUP BY product_id, location_id`,
		fmtTime(time.Now().UTC()), productID)
	if err != nil {
		return mapError(err, fmt.Sprintf("rebuild stock levels for product %s", productID))
	}
	return nil
}

// LastMovedAt returns when each product's stock last moved. Aging and
// dead-stock reports need this for the whole catalogue, and a query per
// product would make them unusable on any real database.
func (r *movementRepo) LastMovedAt(ctx context.Context, locationID core.ID) (map[core.ID]time.Time, error) {
	query := `SELECT product_id, MAX(occurred_at) FROM stock_movements`
	var args []any
	if !locationID.IsZero() {
		query += ` WHERE location_id = ?`
		args = append(args, locationID)
	}
	query += ` GROUP BY product_id`

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "read last movement dates")
	}
	defer func() { _ = rows.Close() }()

	out := map[core.ID]time.Time{}
	for rows.Next() {
		var (
			id       core.ID
			occurred string
		)
		if err := rows.Scan(&id, &occurred); err != nil {
			return nil, fmt.Errorf("read last movement dates: %w", err)
		}
		when, err := parseTime(occurred)
		if err != nil {
			return nil, err
		}
		out[id] = when
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read last movement dates: %w", err)
	}
	return out, nil
}

// CostHistory returns the movements valuation replays, grouped by product and
// ordered oldest first within each group. Ordering is essential: FIFO consumes
// layers in receipt order, so an out-of-order replay silently produces a
// different number.
func (r *movementRepo) CostHistory(ctx context.Context, locationID core.ID, productIDs ...core.ID) (map[core.ID][]core.StockMovement, error) {
	var (
		clauses []string
		args    []any
	)
	if !locationID.IsZero() {
		clauses = append(clauses, "location_id = ?")
		args = append(args, locationID)
	}
	if len(productIDs) > 0 {
		placeholders := make([]string, len(productIDs))
		for i, id := range productIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "product_id IN ("+strings.Join(placeholders, ", ")+")")
	}

	query := `SELECT ` + movementColumns + ` FROM stock_movements`
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += ` ORDER BY product_id, occurred_at ASC, id ASC`

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "read cost history")
	}
	defer func() { _ = rows.Close() }()

	out := map[core.ID][]core.StockMovement{}
	for rows.Next() {
		m, err := scanMovement(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read cost history: %w", err)
		}
		out[m.ProductID] = append(out[m.ProductID], m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cost history: %w", err)
	}
	return out, nil
}

// ExpiringLots lists lot-tracked receipts expiring on or before the cutoff.
func (r *movementRepo) ExpiringLots(ctx context.Context, before time.Time) ([]core.StockMovement, error) {
	rows, err := r.s.read().QueryContext(ctx, `
		SELECT `+movementColumns+` FROM stock_movements
		WHERE lot_number <> '' AND expiry_date <> '' AND expiry_date <= ?
		  AND qty_delta > 0
		ORDER BY expiry_date ASC`, fmtDate(before))
	if err != nil {
		return nil, mapError(err, "list expiring lots")
	}
	defer func() { _ = rows.Close() }()

	var out []core.StockMovement
	for rows.Next() {
		m, err := scanMovement(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list expiring lots: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list expiring lots: %w", err)
	}
	return out, nil
}

func movementWhere(f storage.MovementFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !f.ProductID.IsZero() {
		clauses = append(clauses, "product_id = ?")
		args = append(args, f.ProductID)
	}
	if !f.LocationID.IsZero() {
		clauses = append(clauses, "location_id = ?")
		args = append(args, f.LocationID)
	}
	if f.Reason != "" {
		clauses = append(clauses, "reason = ?")
		args = append(args, string(f.Reason))
	}
	if f.RefType != "" {
		clauses = append(clauses, "ref_type = ?")
		args = append(args, f.RefType)
	}
	if !f.RefID.IsZero() {
		clauses = append(clauses, "ref_id = ?")
		args = append(args, f.RefID)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanMovement(scan func(dest ...any) error) (core.StockMovement, error) {
	var (
		m         core.StockMovement
		reason    string
		costMinor int64
		currency  string
		expiry    string
		occurred  string
		created   string
	)
	err := scan(&m.ID, &m.ProductID, &m.LocationID, &m.QtyDelta, &reason, &m.Note,
		&costMinor, &currency, &m.LotNumber, &expiry,
		&m.RefType, &m.RefID, &m.ActorID, &occurred, &created)
	if err != nil {
		return core.StockMovement{}, err
	}

	m.Reason = core.MovementReason(reason)
	m.UnitCost = core.NewMoney(costMinor, core.Currency(currency))
	if m.ExpiryDate, err = parseDate(expiry); err != nil {
		return core.StockMovement{}, err
	}
	if m.OccurredAt, err = parseTime(occurred); err != nil {
		return core.StockMovement{}, err
	}
	if m.CreatedAt, err = parseTime(created); err != nil {
		return core.StockMovement{}, err
	}
	return m, nil
}

var _ storage.MovementRepo = (*movementRepo)(nil)
