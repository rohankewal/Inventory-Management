package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type orderRepo struct{ s *Store }

const orderColumns = `o.id, o.customer_id, o.store_id, o.program_id,
	o.customer_po_number, o.status, o.currency,
	o.ordered_at, o.requested_ship_date, o.cancel_after_date,
	o.notes, o.created_at, o.updated_at, o.version`

const orderLineColumns = `l.id, l.order_id, l.product_id, l.line_no, l.quantity,
	l.unit_price, l.currency, l.allocated_qty, l.shipped_qty, l.cancelled_qty, l.notes`

// Create writes the order and its lines together. An order with no lines is
// not a document anybody wants, and creating one in two steps leaves a window
// where it exists as one.
func (r *orderRepo) Create(ctx context.Context, o *core.StoreOrder, lines []core.StoreOrderLine) error {
	o.Normalize()
	if err := o.Validate(); err != nil {
		return err
	}
	if len(lines) == 0 {
		var v core.ValidationError
		v.Add("lines", "An order needs at least one line")
		return v.ErrOrNil()
	}

	if o.ID.IsZero() {
		o.ID = core.NewID()
	}
	now := time.Now().UTC()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	if o.OrderedAt.IsZero() {
		o.OrderedAt = now
	}
	o.UpdatedAt = now
	o.Version = 1

	if r.s.tx == nil {
		return r.s.InTx(ctx, func(st storage.Store) error {
			return st.Orders().Create(ctx, o, lines)
		})
	}

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO store_orders
			(id, customer_id, store_id, program_id, customer_po_number, status, currency,
			 ordered_at, requested_ship_date, cancel_after_date, notes,
			 created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.CustomerID, o.StoreID, o.ProgramID, o.CustomerPONumber,
		string(o.Status), string(o.Currency),
		fmtTime(o.OrderedAt), fmtDate(o.RequestedShipDate), fmtDate(o.CancelAfterDate),
		o.Notes, fmtTime(o.CreatedAt), fmtTime(o.UpdatedAt), o.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create order %q", o.CustomerPONumber))
	}

	return r.insertLines(ctx, o.ID, o.Currency, lines)
}

func (r *orderRepo) Update(ctx context.Context, o *core.StoreOrder) error {
	o.Normalize()
	if err := o.Validate(); err != nil {
		return err
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE store_orders SET
			store_id = ?, program_id = ?, customer_po_number = ?, status = ?, currency = ?,
			ordered_at = ?, requested_ship_date = ?, cancel_after_date = ?, notes = ?,
			updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		o.StoreID, o.ProgramID, o.CustomerPONumber, string(o.Status), string(o.Currency),
		fmtTime(o.OrderedAt), fmtDate(o.RequestedShipDate), fmtDate(o.CancelAfterDate),
		o.Notes, fmtTime(updatedAt), o.ID, o.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update order %s", o.ID))
	}
	if err := requireOneRow(res, func() error {
		_, getErr := r.Get(ctx, o.ID)
		return getErr
	}, "update order", o.ID); err != nil {
		return err
	}

	o.UpdatedAt = updatedAt
	o.Version++
	return nil
}

// ReplaceLines swaps the whole line set.
//
// A line that has already shipped cannot be removed: the ledger records goods
// that physically left, and an order that no longer mentions them would make
// the shipment untraceable.
func (r *orderRepo) ReplaceLines(ctx context.Context, orderID core.ID, lines []core.StoreOrderLine) error {
	if r.s.tx == nil {
		return r.s.InTx(ctx, func(st storage.Store) error {
			return st.Orders().ReplaceLines(ctx, orderID, lines)
		})
	}

	order, err := r.Get(ctx, orderID)
	if err != nil {
		return err
	}

	existing, err := r.lines(ctx, orderID)
	if err != nil {
		return err
	}

	keeping := map[core.ID]bool{}
	for _, line := range lines {
		if !line.ID.IsZero() {
			keeping[line.ID] = true
		}
	}
	for _, line := range existing {
		if line.ShippedQty > 0 && !keeping[line.ID] {
			return fmt.Errorf(
				"update order %s: %w: line %d has already shipped and cannot be removed",
				orderID, core.ErrConflict, line.LineNo)
		}
	}

	if _, err := r.s.write().ExecContext(ctx,
		`DELETE FROM store_order_lines WHERE order_id = ?`, orderID); err != nil {
		return mapError(err, fmt.Sprintf("replace lines on order %s", orderID))
	}
	return r.insertLines(ctx, orderID, order.Currency, lines)
}

func (r *orderRepo) insertLines(ctx context.Context, orderID core.ID, currency core.Currency, lines []core.StoreOrderLine) error {
	for i := range lines {
		line := &lines[i]
		line.OrderID = orderID
		if line.LineNo == 0 {
			line.LineNo = i + 1
		}
		if line.UnitPrice.Currency == "" {
			line.UnitPrice.Currency = currency
		}
		if err := line.Validate(); err != nil {
			return err
		}
		if line.ID.IsZero() {
			line.ID = core.NewID()
		}

		_, err := r.s.write().ExecContext(ctx, `
			INSERT INTO store_order_lines
				(id, order_id, product_id, line_no, quantity, unit_price, currency,
				 allocated_qty, shipped_qty, cancelled_qty, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			line.ID, line.OrderID, line.ProductID, line.LineNo, line.Quantity,
			line.UnitPrice.Minor, string(line.UnitPrice.Currency),
			line.AllocatedQty, line.ShippedQty, line.CancelledQty, line.Notes)
		if err != nil {
			return mapError(err, fmt.Sprintf("write line %d of order %s", line.LineNo, orderID))
		}
	}
	return nil
}

func (r *orderRepo) SetStatus(ctx context.Context, id core.ID, status core.OrderStatus) error {
	if !status.Valid() {
		return fmt.Errorf("set order status: %w: %q is not a known status", core.ErrInvalid, status)
	}

	res, err := r.s.write().ExecContext(ctx, `
		UPDATE store_orders SET status = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
		string(status), fmtTime(time.Now().UTC()), id)
	if err != nil {
		return mapError(err, fmt.Sprintf("set order %s status", id))
	}
	return requireOneRow(res, nil, "set order status", id)
}

func (r *orderRepo) Get(ctx context.Context, id core.ID) (core.StoreOrder, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+orderColumns+` FROM store_orders o WHERE o.id = ?`, id)
	o, err := scanOrder(row.Scan)
	if err != nil {
		return core.StoreOrder{}, mapError(err, fmt.Sprintf("get order %s", id))
	}
	return o, nil
}

func (r *orderRepo) GetByPONumber(ctx context.Context, customerID core.ID, poNumber string) (core.StoreOrder, error) {
	poNumber = strings.TrimSpace(poNumber)
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+orderColumns+` FROM store_orders o
		 WHERE o.customer_id = ? AND o.customer_po_number = ? COLLATE NOCASE`,
		customerID, poNumber)
	o, err := scanOrder(row.Scan)
	if err != nil {
		return core.StoreOrder{}, mapError(err, fmt.Sprintf("get order %q", poNumber))
	}
	return o, nil
}

// FindByPONumber resolves a PO number without knowing the customer, which is
// the case when somebody reads one off an email and types it into the search
// box. It can legitimately return more than one, since the number is only
// unique within a customer.
func (r *orderRepo) FindByPONumber(ctx context.Context, poNumber string) ([]core.StoreOrder, error) {
	poNumber = strings.TrimSpace(poNumber)
	if poNumber == "" {
		return nil, nil
	}

	rows, err := r.s.read().QueryContext(ctx,
		`SELECT `+orderColumns+` FROM store_orders o
		 WHERE o.customer_po_number = ? COLLATE NOCASE
		 ORDER BY o.created_at DESC`, poNumber)
	if err != nil {
		return nil, mapError(err, fmt.Sprintf("find order %q", poNumber))
	}
	defer func() { _ = rows.Close() }()

	var out []core.StoreOrder
	for rows.Next() {
		o, err := scanOrder(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("find order %q: %w", poNumber, err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find order %q: %w", poNumber, err)
	}
	return out, nil
}

func (r *orderRepo) Detail(ctx context.Context, id core.ID) (core.OrderDetail, error) {
	order, err := r.Get(ctx, id)
	if err != nil {
		return core.OrderDetail{}, err
	}

	detail := core.OrderDetail{StoreOrder: order}

	if detail.Customer, err = (&customerRepo{r.s}).Get(ctx, order.CustomerID); err != nil {
		return core.OrderDetail{}, err
	}
	if detail.Store, err = (&storeRepo{r.s}).Get(ctx, order.StoreID); err != nil {
		return core.OrderDetail{}, err
	}
	if !order.ProgramID.IsZero() {
		program, err := (&programRepo{r.s}).Get(ctx, order.ProgramID)
		if err == nil {
			detail.Program = &program
		}
	}

	// Lines carry the product's SKU, name, unit and current on-hand so the
	// detail screen and every document render from one query.
	rows, err := r.s.read().QueryContext(ctx, `
		SELECT `+orderLineColumns+`, p.sku, p.name, p.unit, COALESCE(sl.on_hand, 0)
		FROM store_order_lines l
		JOIN products p ON p.id = l.product_id
		LEFT JOIN stock_levels sl ON sl.product_id = l.product_id AND sl.location_id = ?
		WHERE l.order_id = ?
		ORDER BY l.line_no ASC`, core.DefaultLocationID, id)
	if err != nil {
		return core.OrderDetail{}, mapError(err, fmt.Sprintf("read lines of order %s", id))
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			ld   core.OrderLineDetail
			unit string
		)
		line, err := scanOrderLine(func(dest ...any) error {
			return rows.Scan(append(dest, &ld.SKU, &ld.Name, &unit, &ld.OnHand)...)
		})
		if err != nil {
			return core.OrderDetail{}, fmt.Errorf("read lines of order %s: %w", id, err)
		}
		ld.StoreOrderLine = line
		ld.Unit = core.UnitOfMeasure(unit)
		detail.Lines = append(detail.Lines, ld)
	}
	if err := rows.Err(); err != nil {
		return core.OrderDetail{}, fmt.Errorf("read lines of order %s: %w", id, err)
	}

	detail.Totals = core.SummariseOrder(detail.Lines, order.Currency)
	return detail, nil
}

// lines reads the bare line rows, used by ReplaceLines.
func (r *orderRepo) lines(ctx context.Context, orderID core.ID) ([]core.StoreOrderLine, error) {
	rows, err := r.s.read().QueryContext(ctx,
		`SELECT `+orderLineColumns+` FROM store_order_lines l
		 WHERE l.order_id = ? ORDER BY l.line_no ASC`, orderID)
	if err != nil {
		return nil, mapError(err, fmt.Sprintf("read lines of order %s", orderID))
	}
	defer func() { _ = rows.Close() }()

	var out []core.StoreOrderLine
	for rows.Next() {
		line, err := scanOrderLine(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("read lines of order %s: %w", orderID, err)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lines of order %s: %w", orderID, err)
	}
	return out, nil
}

func (r *orderRepo) List(ctx context.Context, f storage.OrderFilter) ([]core.OrderSummary, error) {
	where, args := orderWhere(f)

	// Line totals are aggregated in the query. Summing them per row in Go
	// would turn one screen into a query per order.
	query := `
		SELECT ` + orderColumns + `,
			c.code, c.name, s.code, s.name, COALESCE(pr.code, ''),
			COALESCE(t.lines, 0), COALESCE(t.units, 0), COALESCE(t.shipped, 0),
			COALESCE(t.cancelled, 0), COALESCE(t.value, 0)
		FROM store_orders o
		JOIN customers c ON c.id = o.customer_id
		JOIN customer_stores s ON s.id = o.store_id
		LEFT JOIN programs pr ON pr.id = o.program_id
		LEFT JOIN (
			SELECT order_id,
			       COUNT(*)                        AS lines,
			       SUM(quantity)                   AS units,
			       SUM(shipped_qty)                AS shipped,
			       SUM(cancelled_qty)              AS cancelled,
			       SUM(quantity * unit_price)      AS value
			FROM store_order_lines GROUP BY order_id
		) t ON t.order_id = o.id` + where + orderOrderBy(f)
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list orders")
	}
	defer func() { _ = rows.Close() }()

	var out []core.OrderSummary
	for rows.Next() {
		var (
			summary   core.OrderSummary
			lineCount int
			units     int64
			shipped   int64
			cancelled int64
			value     int64
		)
		order, err := scanOrder(func(dest ...any) error {
			return rows.Scan(append(dest,
				&summary.CustomerCode, &summary.CustomerName,
				&summary.StoreCode, &summary.StoreName, &summary.ProgramCode,
				&lineCount, &units, &shipped, &cancelled, &value)...)
		})
		if err != nil {
			return nil, fmt.Errorf("list orders: %w", err)
		}

		summary.StoreOrder = order
		summary.Totals = core.OrderTotals{
			Lines:       lineCount,
			Units:       units,
			Shipped:     shipped,
			Cancelled:   cancelled,
			Outstanding: max64(units-shipped-cancelled, 0),
			Value:       core.NewMoney(value, order.Currency),
		}
		out = append(out, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return out, nil
}

func (r *orderRepo) Count(ctx context.Context, f storage.OrderFilter) (int, error) {
	where, args := orderWhere(f)
	query := `
		SELECT COUNT(*)
		FROM store_orders o
		JOIN customers c ON c.id = o.customer_id
		JOIN customer_stores s ON s.id = o.store_id
		LEFT JOIN programs pr ON pr.id = o.program_id` + where

	var n int
	if err := r.s.read().QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, mapError(err, "count orders")
	}
	return n, nil
}

func (r *orderRepo) Delete(ctx context.Context, id core.ID) error {
	res, err := r.s.write().ExecContext(ctx, `DELETE FROM store_orders WHERE id = ?`, id)
	if err != nil {
		return mapError(err, fmt.Sprintf("delete order %s", id))
	}
	return requireOneRow(res, nil, "delete order", id)
}

func orderWhere(f storage.OrderFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if term := strings.TrimSpace(f.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses, `(o.customer_po_number LIKE ? ESCAPE '\'
			OR s.code LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\'
			OR c.name LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern, pattern)
	}
	if !f.CustomerID.IsZero() {
		clauses = append(clauses, "o.customer_id = ?")
		args = append(args, f.CustomerID)
	}
	if !f.StoreID.IsZero() {
		clauses = append(clauses, "o.store_id = ?")
		args = append(args, f.StoreID)
	}
	if !f.ProgramID.IsZero() {
		clauses = append(clauses, "o.program_id = ?")
		args = append(args, f.ProgramID)
	}
	if f.Status != "" {
		clauses = append(clauses, "o.status = ?")
		args = append(args, string(f.Status))
	}
	if f.OpenOnly {
		clauses = append(clauses, "o.status IN ('draft', 'confirmed', 'partially_shipped')")
	}
	if !f.ShipAfter.IsZero() {
		clauses = append(clauses, "o.requested_ship_date <> '' AND o.requested_ship_date >= ?")
		args = append(args, fmtDate(f.ShipAfter))
	}
	if !f.ShipBefore.IsZero() {
		clauses = append(clauses, "o.requested_ship_date <> '' AND o.requested_ship_date <= ?")
		args = append(args, fmtDate(f.ShipBefore))
	}
	if f.LateOnly {
		// Past the date the client will refuse it, and still open.
		clauses = append(clauses,
			`o.cancel_after_date <> '' AND o.cancel_after_date < ?
			 AND o.status IN ('draft', 'confirmed', 'partially_shipped')`)
		args = append(args, fmtDate(time.Now().UTC()))
	}

	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func orderOrderBy(f storage.OrderFilter) string {
	// Default: the orders that have to leave soonest, first. An orders screen
	// sorted alphabetically is a list nobody can work from.
	col := "o.requested_ship_date"
	switch f.Sort {
	case storage.SortOrderPONumber:
		col = "o.customer_po_number COLLATE NOCASE"
	case storage.SortOrderCustomer:
		col = "c.name COLLATE NOCASE"
	case storage.SortOrderStore:
		col = "s.code COLLATE NOCASE"
	case storage.SortOrderStatus:
		col = "o.status"
	case storage.SortOrderCancelDate:
		col = "o.cancel_after_date"
	case storage.SortOrderOrderedAt:
		col = "o.ordered_at"
	case storage.SortOrderValue:
		col = "COALESCE(t.value, 0)"
	case storage.SortOrderUnits:
		col = "COALESCE(t.units, 0)"
	}

	dir := direction(f.Direction)
	// An unset ship date sorts last ascending rather than first, since a blank
	// string would otherwise top a list meant to show what is most urgent.
	if f.Sort == "" || f.Sort == storage.SortOrderShipDate {
		return fmt.Sprintf(" ORDER BY (o.requested_ship_date = '') ASC, %s %s, o.id ASC", col, dir)
	}
	return " ORDER BY " + col + " " + dir + ", o.id ASC"
}

func scanOrder(scan func(dest ...any) error) (core.StoreOrder, error) {
	var (
		o         core.StoreOrder
		programID any
		status    string
		currency  string
		ordered   string
		shipDate  string
		cancelBy  string
		created   string
		updated   string
	)
	err := scan(&o.ID, &o.CustomerID, &o.StoreID, &programID,
		&o.CustomerPONumber, &status, &currency,
		&ordered, &shipDate, &cancelBy, &o.Notes, &created, &updated, &o.Version)
	if err != nil {
		return core.StoreOrder{}, err
	}

	if err := o.ProgramID.Scan(programID); err != nil {
		return core.StoreOrder{}, err
	}
	o.Status = core.OrderStatus(status)
	o.Currency = core.Currency(currency)

	if o.OrderedAt, err = parseTime(ordered); err != nil {
		return core.StoreOrder{}, err
	}
	if o.RequestedShipDate, err = parseDate(shipDate); err != nil {
		return core.StoreOrder{}, err
	}
	if o.CancelAfterDate, err = parseDate(cancelBy); err != nil {
		return core.StoreOrder{}, err
	}
	if o.CreatedAt, err = parseTime(created); err != nil {
		return core.StoreOrder{}, err
	}
	if o.UpdatedAt, err = parseTime(updated); err != nil {
		return core.StoreOrder{}, err
	}
	return o, nil
}

func scanOrderLine(scan func(dest ...any) error) (core.StoreOrderLine, error) {
	var (
		l         core.StoreOrderLine
		unitPrice int64
		currency  string
	)
	err := scan(&l.ID, &l.OrderID, &l.ProductID, &l.LineNo, &l.Quantity,
		&unitPrice, &currency, &l.AllocatedQty, &l.ShippedQty, &l.CancelledQty, &l.Notes)
	if err != nil {
		return core.StoreOrderLine{}, err
	}

	l.UnitPrice = core.NewMoney(unitPrice, core.Currency(currency))
	return l, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var _ storage.OrderRepo = (*orderRepo)(nil)
