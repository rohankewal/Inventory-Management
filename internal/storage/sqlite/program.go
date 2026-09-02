package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type programRepo struct{ s *Store }

const programColumns = `p.id, p.customer_id, p.code, p.name, p.season, p.status,
	p.target_delivery_date, p.notes, p.created_at, p.updated_at, p.version`

func (r *programRepo) Create(ctx context.Context, p *core.Program) error {
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

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO programs
			(id, customer_id, code, name, season, status, target_delivery_date,
			 notes, created_at, updated_at, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.CustomerID, p.Code, p.Name, p.Season, string(p.Status),
		fmtDate(p.TargetDeliveryDate), p.Notes,
		fmtTime(p.CreatedAt), fmtTime(p.UpdatedAt), p.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("create program %q", p.Code))
	}
	return nil
}

func (r *programRepo) Update(ctx context.Context, p *core.Program) error {
	p.Normalize()
	if err := p.Validate(); err != nil {
		return err
	}

	updatedAt := time.Now().UTC()
	res, err := r.s.write().ExecContext(ctx, `
		UPDATE programs SET
			code = ?, name = ?, season = ?, status = ?, target_delivery_date = ?,
			notes = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?`,
		p.Code, p.Name, p.Season, string(p.Status), fmtDate(p.TargetDeliveryDate),
		p.Notes, fmtTime(updatedAt), p.ID, p.Version)
	if err != nil {
		return mapError(err, fmt.Sprintf("update program %s", p.ID))
	}
	if err := requireOneRow(res, func() error {
		_, getErr := r.Get(ctx, p.ID)
		return getErr
	}, "update program", p.ID); err != nil {
		return err
	}

	p.UpdatedAt = updatedAt
	p.Version++
	return nil
}

func (r *programRepo) Get(ctx context.Context, id core.ID) (core.Program, error) {
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+programColumns+` FROM programs p WHERE p.id = ?`, id)
	p, err := scanProgram(row.Scan)
	if err != nil {
		return core.Program{}, mapError(err, fmt.Sprintf("get program %s", id))
	}
	return p, nil
}

func (r *programRepo) GetByCode(ctx context.Context, customerID core.ID, code string) (core.Program, error) {
	code = strings.TrimSpace(code)
	row := r.s.read().QueryRowContext(ctx,
		`SELECT `+programColumns+` FROM programs p
		 WHERE p.customer_id = ? AND p.code = ? COLLATE NOCASE`, customerID, code)
	p, err := scanProgram(row.Scan)
	if err != nil {
		return core.Program{}, mapError(err, fmt.Sprintf("get program %q", code))
	}
	return p, nil
}

func (r *programRepo) List(ctx context.Context, f storage.ProgramFilter) ([]core.Program, error) {
	where, args := programWhere(f)

	query := `SELECT ` + programColumns + ` FROM programs p` + where +
		` ORDER BY p.target_delivery_date ASC, p.code COLLATE NOCASE ASC, p.id ASC`
	query += limitClause(f.Limit, f.Offset, &args)

	rows, err := r.s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err, "list programs")
	}
	defer func() { _ = rows.Close() }()

	var out []core.Program
	for rows.Next() {
		p, err := scanProgram(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("list programs: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list programs: %w", err)
	}
	return out, nil
}

func (r *programRepo) Count(ctx context.Context, f storage.ProgramFilter) (int, error) {
	where, args := programWhere(f)
	var n int
	err := r.s.read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM programs p`+where, args...).Scan(&n)
	if err != nil {
		return 0, mapError(err, "count programs")
	}
	return n, nil
}

func (r *programRepo) Delete(ctx context.Context, id core.ID) error {
	res, err := r.s.write().ExecContext(ctx, `DELETE FROM programs WHERE id = ?`, id)
	if err != nil {
		return mapError(err, fmt.Sprintf("delete program %s", id))
	}
	return requireOneRow(res, nil, "delete program", id)
}

func programWhere(f storage.ProgramFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if !f.CustomerID.IsZero() {
		clauses = append(clauses, "p.customer_id = ?")
		args = append(args, f.CustomerID)
	}
	if f.Status != "" {
		clauses = append(clauses, "p.status = ?")
		args = append(args, string(f.Status))
	}
	if f.OpenOnly {
		clauses = append(clauses, "p.status NOT IN ('closed', 'cancelled')")
	}
	if term := strings.TrimSpace(f.Search); term != "" {
		pattern := "%" + escapeLike(term) + "%"
		clauses = append(clauses,
			`(p.code LIKE ? ESCAPE '\' OR p.name LIKE ? ESCAPE '\' OR p.season LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func scanProgram(scan func(dest ...any) error) (core.Program, error) {
	var (
		p       core.Program
		status  string
		target  string
		created string
		updated string
	)
	err := scan(&p.ID, &p.CustomerID, &p.Code, &p.Name, &p.Season, &status,
		&target, &p.Notes, &created, &updated, &p.Version)
	if err != nil {
		return core.Program{}, err
	}

	p.Status = core.ProgramStatus(status)
	if p.TargetDeliveryDate, err = parseDate(target); err != nil {
		return core.Program{}, err
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return core.Program{}, err
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Program{}, err
	}
	return p, nil
}

var _ storage.ProgramRepo = (*programRepo)(nil)
