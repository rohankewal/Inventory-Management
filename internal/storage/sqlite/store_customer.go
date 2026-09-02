package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type storeRepo struct{ s *Store }

const storeColumns = `s.id, s.customer_id, s.code, s.name,
	s.ship_to_line1, s.ship_to_line2, s.ship_to_city, s.ship_to_region,
	s.ship_to_postal, s.ship_to_country,
	s.contact_name, s.contact_email, s.contact_phone,
	s.routing_notes, s.active, s.created_at, s.updated_at, s.version`

func (r *storeRepo) Create(ctx context.Context, st *core.CustomerStore) error {
	st.Normalize()
	if err := st.Validate(); err != nil {
		return err
	}
	if st.ID.IsZero() {
		st.ID = core.NewID()
	}
	now := time.Now().UTC()
	if st.CreatedAt.IsZero() {
		st.CreatedAt = now
	}
	st.UpdatedAt = now
	st.Version = 1

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO customer_stores
			(id, customer_id, code, name,
			 ship_to_line1, ship_to_line2, ship_to_city, ship_to_region,
			 ship_to_postal, ship_to_country,
			 contact_name, contact_email, contact_phone,
			 routing_notes, active, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.CustomerID, st.Code, st.Name,
		st.ShipTo.Line1, st.ShipTo.Line2, st.ShipTo.City, st.ShipTo.Region,
		st.ShipTo.PostalCode, st.ShipTo.Country,
		st.Contact.Name, st.Contact.Email, st.Contact.Phone,
		st.RoutingNotes, boolToInt(st.Active),
		fmtTime(st.CreatedAt), fmtTime(st.UpdatedAt), st.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create store %q", st.Code))
	}
	return nil
}

func (r *storeRepo) Update(ctx context.Context, st *core.CustomerStore) error {
	st.Normalize()
	if err := st.Validate(); err != nil {
		return err
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE customer_stores SET
			code = ?, name = ?,
			ship_to_line1 = ?, ship_to_line2 = ?, ship_to_city = ?, ship_to_region = ?,
			ship_to_postal = ?, ship_to_country = ?,
			contact_name = ?, contact_email = ?, contact_phone = ?,
			routing_notes = ?, active = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		st.Code, st.Name,
		st.ShipTo.Line1, st.ShipTo.Line2, st.ShipTo.City, st.ShipTo.Region,
		st.ShipTo.PostalCode, st.ShipTo.Country,
		st.Contact.Name, st.Contact.Email, st.Contact.Phone,
		st.RoutingNotes, boolToInt(st.Active), fmtTime(updatedAt), st.ID, st.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update store %s", st.ID))
	}
	if err := requireOneRow(res, func() error {
		_, getErr := r.Get(ctx, st.ID)
		return getErr
	}, "update store", st.ID); err != nil {
		return err
	}

	st.UpdatedAt = updatedAt
	st.Version++
	return nil
}

func (r *storeRepo) SetActive(ctx context.Context, id core.ID, active bool) error {
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE customer_stores SET active = ?, updated_at = ?, version = version + 1 WHERE id = ?`,
		boolToInt(active), fmtTime(time.Now().UTC()), id)
	if err != nil {
		return mapError(err, fmt.Sprintf("set store %s active", id))
	}
	return requireOneRow(res, nil, "set store active", id)
}

func (r *storeRepo) Get(ctx context.Context, id core.ID) (core.CustomerStore, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+storeColumns+` FROM customer_stores s WHERE s.id = ?`, id)
	st, err := scanStore(row.Scan)
	if err != nil {
		return core.CustomerStore{}, mapError(err, fmt.Sprintf("get store %s", id))
	}
	return st, nil
}

func (r *storeRepo) GetByCode(ctx context.Context, customerID core.ID, code string) (core.CustomerStore, error) {
	code = strings.TrimSpace(code)
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+storeColumns+` FROM customer_stores s
		 WHERE s.customer_id = ? AND s.code = ? COLLATE NOCASE`, customerID, code)
	st, err := scanStore(row.Scan)
	if err != nil {
		return core.CustomerStore{}, mapError(err, fmt.Sprintf("get store %q", code))
	}
	return st, nil
}

func (r *storeRepo) List(ctx context.Context, f storage.StoreFilter) ([]core.CustomerStore, error) {
	where, args := storeWhere(f)

	query := `SELECT ` + storeColumns + ` FROM customer_stores s` + where +
		` ORDER BY s.code COLLATE NOCASE ASC, s.id ASC`
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list stores")
	}
	defer func() { _ = rows.Close() }()

	var out []core.CustomerStore
	for rows.Next() {
		st, err := scanStore(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list stores: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stores: %w", err)
	}
	return out, nil
}

func (r *storeRepo) Count(ctx context.Context, f storage.StoreFilter) (int, error) {
	where, args := storeWhere(f)
	var n int
	err := r.s.read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM customer_stores s`+where, args...).Scan(&n)
	if err != nil {
		return 0, mapError(err, "count stores")
	}
	return n, nil
}

func (r *storeRepo) Delete(ctx context.Context, id core.ID) error {
	res, err := r.s.write().ExecContext(ctx, `DELETE FROM customer_stores WHERE id = ?`, id)
	if err != nil {
		return mapError(err, fmt.Sprintf("delete store %s", id))
	}
	return requireOneRow(res, nil, "delete store", id)
}

func storeWhere(f storage.StoreFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !f.CustomerID.IsZero() {
		clauses = append(clauses, "s.customer_id = ?")
		args = append(args, f.CustomerID)
	}
	if !f.IncludeInactive {
		clauses = append(clauses, "s.active = 1")
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses,
			`(s.code LIKE ? ESCAPE '\' OR s.name LIKE ? ESCAPE '\' OR s.ship_to_city LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanStore(scan func(dest ...any) error) (core.CustomerStore, error) {
	var (
		st      core.CustomerStore
		active  int
		created string
		updated string
	)
	err := scan(&st.ID, &st.CustomerID, &st.Code, &st.Name,
		&st.ShipTo.Line1, &st.ShipTo.Line2, &st.ShipTo.City, &st.ShipTo.Region,
		&st.ShipTo.PostalCode, &st.ShipTo.Country,
		&st.Contact.Name, &st.Contact.Email, &st.Contact.Phone,
		&st.RoutingNotes, &active, &created, &updated, &st.Version)
	if err != nil {
		return core.CustomerStore{}, err
	}

	st.Active = active != 0
	if st.CreatedAt, err = parseTime(created); err != nil {
		return core.CustomerStore{}, err
	}
	if st.UpdatedAt, err = parseTime(updated); err != nil {
		return core.CustomerStore{}, err
	}
	return st, nil
}

var _ storage.StoreRepo = (*storeRepo)(nil)
