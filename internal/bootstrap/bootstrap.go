// Package bootstrap wires configuration to a concrete storage backend.
//
// It lives apart from the storage package so that package storage stays a pure
// contract with no dependency on any implementation of it. Both the desktop
// app and the admin CLI open their database through here, which is what keeps
// their behaviour identical.
package bootstrap

import (
	"context"
	"fmt"

	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
)

// OpenOptions adjust how the store is opened.
type OpenOptions struct {
	// SkipMigrate opens the database without applying pending migrations.
	// The CLI uses it so that inspecting a database never changes it.
	SkipMigrate bool
}

// OpenStore connects to the backend named by the configuration.
func OpenStore(ctx context.Context, cfg config.Config, opts OpenOptions) (storage.Store, error) {
	switch cfg.Driver {
	case config.DriverSQLite:
		return sqlite.Open(ctx, sqlite.Options{
			Path:        cfg.DatabasePath(),
			SkipMigrate: opts.SkipMigrate,
		})

	case config.DriverPostgres:
		// Landing here means the configuration passed validation for a backend
		// this build cannot open, so say exactly that rather than failing with
		// a connection error that sends someone debugging their network.
		return nil, fmt.Errorf(
			"the postgres backend is not available in this build; set %s=%s or upgrade",
			config.EnvDriver, config.DriverSQLite)

	default:
		return nil, fmt.Errorf("unknown storage driver %q", cfg.Driver)
	}
}
