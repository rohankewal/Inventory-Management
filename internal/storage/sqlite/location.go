package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type locationRepo struct{ s *Store }

const locationColumns = `id, code, name, is_default, active, created_at, updated_at, version`

func (r *locationRepo) Create(ctx context.Context, l *core.Location) error {
	l.Normalize()
	if err := l.Validate(); err != nil {
		return err
	}
	if l.ID.IsZero() {
		l.ID = core.NewID()
	}
	now := time.Now().UTC()
	if l.CreatedAt.IsZero() {
		l.CreatedAt = now
	}
	l.UpdatedAt = now
	l.Version = 1

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO locations (id, code, name, is_default, active, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Code, l.Name, boolToInt(l.IsDefault), boolToInt(l.Active),
		fmtTime(l.CreatedAt), fmtTime(l.UpdatedAt), l.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create location %q", l.Code))
	}
	return nil
}

func (r *locationRepo) Update(ctx context.Context, l *core.Location) error {
	l.Normalize()
	if err := l.Validate(); err != nil {
		return err
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE locations SET
			code = ?, name = ?, is_default = ?, active = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		l.Code, l.Name, boolToInt(l.IsDefault), boolToInt(l.Active),
		fmtTime(updatedAt), l.ID, l.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update location %s", l.ID))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update location %s: %w", l.ID, err)
	}
	if n == 0 {
		if _, getErr := r.Get(ctx, l.ID); getErr != nil {
			return getErr
		}
		return fmt.Errorf("update location %s: %w: the record was changed by someone else",
			l.ID, core.ErrConflict)
	}

	l.UpdatedAt = updatedAt
	l.Version++
	return nil
}

func (r *locationRepo) Get(ctx context.Context, id core.ID) (core.Location, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM locations WHERE id = ?`, id)
	l, err := scanLocation(row.Scan)
	if err != nil {
		return core.Location{}, mapError(err, fmt.Sprintf("get location %s", id))
	}
	return l, nil
}

func (r *locationRepo) GetByCode(ctx context.Context, code string) (core.Location, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM locations WHERE code = ? COLLATE NOCASE`, code)
	l, err := scanLocation(row.Scan)
	if err != nil {
		return core.Location{}, mapError(err, fmt.Sprintf("get location %q", code))
	}
	return l, nil
}

func (r *locationRepo) Default(ctx context.Context) (core.Location, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM locations WHERE is_default = 1`)
	l, err := scanLocation(row.Scan)
	if err != nil {
		return core.Location{}, mapError(err, "get default location")
	}
	return l, nil
}

func (r *locationRepo) List(ctx context.Context, includeInactive bool) ([]core.Location, error) {
	query := `SELECT ` + locationColumns + ` FROM locations`
	if !includeInactive {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY is_default DESC, code COLLATE NOCASE ASC`

	rows, err := r.s.read().QueryContext(ctx, query)
	if err != nil {
		return nil, mapError(err, "list locations")
	}
	defer func() { _ = rows.Close() }()

	var out []core.Location
	for rows.Next() {
		l, err := scanLocation(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list locations: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list locations: %w", err)
	}
	return out, nil
}

func scanLocation(scan func(dest ...any) error) (core.Location, error) {
	var (
		l         core.Location
		isDefault int
		active    int
		created   string
		updated   string
	)
	err := scan(&l.ID, &l.Code, &l.Name, &isDefault, &active, &created, &updated, &l.Version)
	if err != nil {
		return core.Location{}, err
	}

	l.IsDefault = isDefault != 0
	l.Active = active != 0
	if l.CreatedAt, err = parseTime(created); err != nil {
		return core.Location{}, err
	}
	if l.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Location{}, err
	}
	return l, nil
}

var _ storage.LocationRepo = (*locationRepo)(nil)
var _ storage.Store = (*Store)(nil)
