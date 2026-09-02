// Package migrate applies embedded, versioned schema migrations.
//
// Migrations are forward-only and each runs in its own transaction. There are
// no "down" migrations on purpose: rolling a customer's production schema
// backwards is a restore-from-backup operation, not a button.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed sqlite/*.sql
var files embed.FS

// Supported SQL dialects.
const (
	DialectSQLite   = "sqlite"
	DialectPostgres = "postgres"
)

// Migration is one numbered schema change.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

func (m Migration) String() string { return fmt.Sprintf("%04d_%s", m.Version, m.Name) }

// Load returns every migration for a dialect, ordered by version.
func Load(dialect string) ([]Migration, error) {
	entries, err := fs.ReadDir(files, dialect)
	if err != nil {
		return nil, fmt.Errorf("migrate: no migrations for dialect %q: %w", dialect, err)
	}

	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, name, err := parseName(e.Name())
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrate: version %d used by both %q and %q", version, prev, e.Name())
		}
		seen[version] = e.Name()

		body, err := files.ReadFile(path.Join(dialect, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: reading %s: %w", e.Name(), err)
		}
		out = append(out, Migration{Version: version, Name: name, SQL: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// parseName splits "0001_init.sql" into its version and name.
func parseName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	numStr, name, ok := strings.Cut(base, "_")
	if !ok {
		return 0, "", fmt.Errorf("migrate: %q must be named <version>_<name>.sql", filename)
	}
	version, err := strconv.Atoi(numStr)
	if err != nil || version <= 0 {
		return 0, "", fmt.Errorf("migrate: %q has a non-numeric version prefix", filename)
	}
	return version, name, nil
}

// Current returns the highest applied migration version, or 0 on a fresh
// database.
func Current(ctx context.Context, db *sql.DB) (int, error) {
	if err := ensureTable(ctx, db); err != nil {
		return 0, err
	}
	var version sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("migrate: reading schema version: %w", err)
	}
	return int(version.Int64), nil
}

// Pending returns the migrations that have not been applied yet.
func Pending(ctx context.Context, db *sql.DB, dialect string) ([]Migration, error) {
	all, err := Load(dialect)
	if err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return nil, err
	}

	var pending []Migration
	for _, m := range all {
		if !applied[m.Version] {
			pending = append(pending, m)
		}
	}

	// A migration numbered below the current high-water mark that was never
	// applied means two branches picked the same range. Applying it now would
	// run it against a schema it was never written for.
	if len(pending) > 0 {
		var maxApplied int
		for v := range applied {
			if v > maxApplied {
				maxApplied = v
			}
		}
		if pending[0].Version < maxApplied {
			return nil, fmt.Errorf(
				"migrate: %s is numbered below the applied version %d; renumber it above %d",
				pending[0], maxApplied, maxApplied)
		}
	}
	return pending, nil
}

// Apply runs every pending migration and returns those it applied. It is a
// no-op on an up-to-date database.
func Apply(ctx context.Context, db *sql.DB, dialect string) ([]Migration, error) {
	if err := ensureTable(ctx, db); err != nil {
		return nil, err
	}
	pending, err := Pending(ctx, db, dialect)
	if err != nil {
		return nil, err
	}

	for _, m := range pending {
		if err := applyOne(ctx, db, m); err != nil {
			return nil, err
		}
	}
	return pending, nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrate: begin %s: %w", m, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("migrate: applying %s: %w", m, err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("migrate: recording %s: %w", m, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrate: commit %s: %w", m, err)
	}
	return nil
}

func ensureTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("migrate: creating schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	if err := ensureTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("migrate: listing applied migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("migrate: scanning applied migration: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrate: iterating applied migrations: %w", err)
	}
	return applied, nil
}

// ErrDialectUnknown is returned for a driver with no migration set.
var ErrDialectUnknown = errors.New("migrate: unknown dialect")
