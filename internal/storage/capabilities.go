package storage

import "context"

// Optional capabilities. Not every backend can offer these — "copy the
// database to a file" means something quite different on a hosted Postgres
// than on a local file — so they are separate interfaces a caller type-asserts
// for, rather than methods every backend must stub out.

// Backupper can copy the database to a single file.
type Backupper interface {
	// BackupTo writes a consistent snapshot to dest, which must not already
	// exist.
	BackupTo(ctx context.Context, dest string) error
}

// SchemaReporter exposes the applied schema version, for diagnostics and for
// refusing to run against a database a newer build has already migrated.
type SchemaReporter interface {
	SchemaVersion(ctx context.Context) (int, error)
	PendingMigrations(ctx context.Context) ([]string, error)
}
