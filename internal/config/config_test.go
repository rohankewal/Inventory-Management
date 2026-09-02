package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/config"
)

// useTempDataDir points the package at a throwaway directory so tests never
// touch the developer's real configuration.
func useTempDataDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Setenv(config.EnvDataDir, dir)
	return dir
}

func TestLoadWritesDefaultsOnFirstRun(t *testing.T) {
	dir := useTempDataDir(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Driver != config.DriverSQLite {
		t.Errorf("Driver = %q, want %q", cfg.Driver, config.DriverSQLite)
	}
	if cfg.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", cfg.Currency)
	}

	// The file must exist afterwards, so the user has something to edit.
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Errorf("Load() did not write a config file: %v", err)
	}
}

func TestLoadRoundTripsEdits(t *testing.T) {
	dir := useTempDataDir(t)

	written := `{"driver":"sqlite","dsn":"","currency":"GBP","log_level":"debug"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(written), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Currency != "GBP" || cfg.LogLevel != "debug" {
		t.Errorf("Load() = %q/%q, want GBP/debug", cfg.Currency, cfg.LogLevel)
	}
}

func TestEnvironmentOverridesFile(t *testing.T) {
	useTempDataDir(t)
	t.Setenv(config.EnvCurrency, "EUR")
	t.Setenv(config.EnvLogLevel, "warn")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Currency != "EUR" || cfg.LogLevel != "warn" {
		t.Errorf("Load() = %q/%q, want the environment values EUR/warn", cfg.Currency, cfg.LogLevel)
	}
}

func TestDatabasePathDefaultsInsideDataDir(t *testing.T) {
	dir := useTempDataDir(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Writing the database beside the config, rather than in the working
	// directory, is what makes the data the same whether the app was launched
	// from a Dock icon or a terminal.
	want := filepath.Join(dir, "inventory.db")
	if got := cfg.DatabasePath(); got != want {
		t.Errorf("DatabasePath() = %q, want %q", got, want)
	}
}

func TestPostgresRequiresADSN(t *testing.T) {
	useTempDataDir(t)
	t.Setenv(config.EnvDriver, config.DriverPostgres)

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() with the postgres driver and no DSN returned nil error")
	}
	if !strings.Contains(err.Error(), "connection string") {
		t.Errorf("error %q does not explain that a connection string is required", err)
	}
}

func TestUnknownDriverIsRejected(t *testing.T) {
	useTempDataDir(t)
	t.Setenv(config.EnvDriver, "mysql")

	if _, err := config.Load(); err == nil {
		t.Error("Load() with an unknown driver returned nil error")
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := useTempDataDir(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cfg.Currency = "CAD"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// No temporary files may be left behind for the next Load to trip over.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading data dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("Save() left a temporary file behind: %s", e.Name())
		}
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("Load() after Save() error = %v", err)
	}
	if reloaded.Currency != "CAD" {
		t.Errorf("Currency = %q after save and reload, want CAD", reloaded.Currency)
	}
}
