package sqlite

import (
	"context"
	"fmt"
	"os"

	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/migrate"
)

// BackupTo writes a consistent snapshot of the database to dest.
//
// VACUUM INTO is used rather than copying the file: with WAL enabled the file
// on disk is only part of the picture, so a plain copy can capture a database
// missing its most recent commits.
func (s *Store) BackupTo(ctx context.Context, dest string) error {
	if dest == "" {
		return fmt.Errorf("sqlite: no backup destination given")
	}
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("sqlite: %s already exists; refusing to overwrite a backup", dest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("sqlite: checking %s: %w", dest, err)
	}

	// VACUUM cannot run inside a transaction, so this deliberately uses the
	// pool rather than any enclosing transaction.
	if _, err := s.writeDB.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("sqlite: writing backup to %s: %w", dest, err)
	}
	return nil
}

// SchemaVersion returns the highest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return migrate.Current(ctx, s.writeDB)
}

// PendingMigrations names the migrations this build would apply.
func (s *Store) PendingMigrations(ctx context.Context) ([]string, error) {
	pending, err := migrate.Pending(ctx, s.writeDB, migrate.DialectSQLite)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(pending))
	for i, m := range pending {
		names[i] = m.String()
	}
	return names, nil
}

// Migrate applies pending migrations and returns their names.
func (s *Store) Migrate(ctx context.Context) ([]string, error) {
	applied, err := migrate.Apply(ctx, s.writeDB, migrate.DialectSQLite)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(applied))
	for i, m := range applied {
		names[i] = m.String()
	}
	return names, nil
}

var (
	_ storage.Backupper      = (*Store)(nil)
	_ storage.SchemaReporter = (*Store)(nil)
)
