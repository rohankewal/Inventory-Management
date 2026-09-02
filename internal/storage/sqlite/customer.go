package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type customerRepo struct{ s *Store }

const customerColumns = `c.id, c.code, c.name, c.currency, c.terms,
	c.contact_name, c.contact_email, c.contact_phone,
	c.bill_to_line1, c.bill_to_line2, c.bill_to_city, c.bill_to_region,
	c.bill_to_postal, c.bill_to_country,
	c.notes, c.active, c.created_at, c.updated_at, c.version`

func (r *customerRepo) Create(ctx context.Context, c *core.Customer) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	if c.ID.IsZero() {
		c.ID = core.NewID()
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	c.Version = 1

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO customers
			(id, code, name, currency, terms, contact_name, contact_email, contact_phone,
			 bill_to_line1, bill_to_line2, bill_to_city, bill_to_region,
			 bill_to_postal, bill_to_country, notes, active, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Code, c.Name, string(c.Currency), c.Terms,
		c.Contact.Name, c.Contact.Email, c.Contact.Phone,
		c.BillTo.Line1, c.BillTo.Line2, c.BillTo.City, c.BillTo.Region,
		c.BillTo.PostalCode, c.BillTo.Country,
		c.Notes, boolToInt(c.Active), fmtTime(c.CreatedAt), fmtTime(c.UpdatedAt), c.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create customer %q", c.Code))
	}
	return nil
}

func (r *customerRepo) Update(ctx context.Context, c *core.Customer) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE customers SET
			code = ?, name = ?, currency = ?, terms = ?,
			contact_name = ?, contact_email = ?, contact_phone = ?,
			bill_to_line1 = ?, bill_to_line2 = ?, bill_to_city = ?, bill_to_region = ?,
			bill_to_postal = ?, bill_to_country = ?,
			notes = ?, active = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		c.Code, c.Name, string(c.Currency), c.Terms,
		c.Contact.Name, c.Contact.Email, c.Contact.Phone,
		c.BillTo.Line1, c.BillTo.Line2, c.BillTo.City, c.BillTo.Region,
		c.BillTo.PostalCode, c.BillTo.Country,
		c.Notes, boolToInt(c.Active), fmtTime(updatedAt), c.ID, c.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update customer %s", c.ID))
	}
	if err := requireOneRow(res, func() error {
		_, getErr := r.Get(ctx, c.ID)
		return getErr
	}, "update customer", c.ID); err != nil {
		return err
	}

	c.UpdatedAt = updatedAt
	c.Version++
	return nil
}

func (r *customerRepo) SetActive(ctx context.Context, id core.ID, active bool) error {
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE customers SET active = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
		boolToInt(active), fmtTime(time.Now().UTC()), id)
	if err != nil {
		return mapError(err, fmt.Sprintf("set customer %s active", id))
	}
	return requireOneRow(res, nil, "set customer active", id)
}

func (r *customerRepo) Get(ctx context.Context, id core.ID) (core.Customer, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+customerColumns+` FROM customers c WHERE c.id = ?`, id)
	c, err := scanCustomer(row.Scan)
	if err != nil {
		return core.Customer{}, mapError(err, fmt.Sprintf("get customer %s", id))
	}
	return c, nil
}

func (r *customerRepo) GetByCode(ctx context.Context, code string) (core.Customer, error) {
	code = strings.TrimSpace(code)
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+customerColumns+` FROM customers c WHERE c.code = ? COLLATE NOCASE`, code)
	c, err := scanCustomer(row.Scan)
	if err != nil {
		return core.Customer{}, mapError(err, fmt.Sprintf("get customer %q", code))
	}
	return c, nil
}

func (r *customerRepo) List(ctx context.Context, f storage.CustomerFilter) ([]core.CustomerWithStores, error) {
	where, args := customerWhere(f)

	// Store and open-order counts come from correlated subqueries rather than
	// a query per row. At the scale a customer list reaches — tens, not
	// thousands — this is far cheaper than the alternative.
	query := `
		SELECT ` + customerColumns + `,
			(SELECT COUNT(*) FROM customer_stores s WHERE s.customer_id = c.id),
			(SELECT COUNT(*) FROM customer_stores s WHERE s.customer_id = c.id AND s.active = 1),
			(SELECT COUNT(*) FROM store_orders o
			   WHERE o.customer_id = c.id
			     AND o.status IN ('draft', 'confirmed', 'partially_shipped'))
		FROM customers c` + where + `
		ORDER BY c.name COLLATE NOCASE ASC, c.id ASC`
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list customers")
	}
	defer func() { _ = rows.Close() }()

	var out []core.CustomerWithStores
	for rows.Next() {
		var cw core.CustomerWithStores
		c, err := scanCustomer(func(dest ...any) error {
			return rows.Scan(append(dest, &cw.StoreCount, &cw.ActiveStores, &cw.OpenOrders)...)
		})
		if err != nil {
			return nil, fmt.Errorf("list customers: %w", err)
		}
		cw.Customer = c
		out = append(out, cw)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list customers: %w", err)
	}
	return out, nil
}

func (r *customerRepo) Count(ctx context.Context, f storage.CustomerFilter) (int, error) {
	where, args := customerWhere(f)
	var n int
	err := r.s.read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customers c`+where, args...).Scan(&n)
	if err != nil {
		return 0, mapError(err, "count customers")
	}
	return n, nil
}

func (r *customerRepo) Delete(ctx context.Context, id core.ID) error {
	res, err := r.s.write().ExecContext(ctx, `DELETE FROM customers WHERE id = ?`, id)
	if err != nil {
		return mapError(err, fmt.Sprintf("delete customer %s", id))
	}
	return requireOneRow(res, nil, "delete customer", id)
}

func customerWhere(f storage.CustomerFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !f.IncludeInactive {
		clauses = append(clauses, "c.active = 1")
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses, `(c.code LIKE ? ESCAPE '\' OR c.name LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanCustomer(scan func(dest ...any) error) (core.Customer, error) {
	var (
		c        core.Customer
		currency string
		active   int
		created  string
		updated  string
	)
	err := scan(&c.ID, &c.Code, &c.Name, &currency, &c.Terms,
		&c.Contact.Name, &c.Contact.Email, &c.Contact.Phone,
		&c.BillTo.Line1, &c.BillTo.Line2, &c.BillTo.City, &c.BillTo.Region,
		&c.BillTo.PostalCode, &c.BillTo.Country,
		&c.Notes, &active, &created, &updated, &c.Version)
	if err != nil {
		return core.Customer{}, err
	}

	c.Currency = core.Currency(currency)
	c.Active = active != 0
	if c.CreatedAt, err = parseTime(created); err != nil {
		return core.Customer{}, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Customer{}, err
	}
	return c, nil
}

var _ storage.CustomerRepo = (*customerRepo)(nil)
