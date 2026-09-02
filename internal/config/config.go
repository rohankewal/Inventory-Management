// Package config resolves where the application keeps its data and which
// storage backend it talks to.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppName is the directory name used under the OS configuration root and the
// display name shown in the window title and installers.
const AppName = "InventorySys"

// Storage drivers.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// Environment overrides, useful for tests, portable installs, and for pointing
// a workstation at a shared server without editing a file by hand.
const (
	EnvDataDir  = "INVENTORY_DATA_DIR"
	EnvDriver   = "INVENTORY_DRIVER"
	EnvDSN      = "INVENTORY_DSN"
	EnvLogLevel = "INVENTORY_LOG_LEVEL"
	EnvCurrency = "INVENTORY_CURRENCY"
)

const fileName = "config.json"

// Config is the on-disk application configuration.
type Config struct {
	// Driver selects the storage backend.
	Driver string `json:"driver"`
	// DSN is the SQLite file path or the Postgres connection string. Empty
	// means "the default database file inside the data directory".
	DSN string `json:"dsn"`
	// Currency is the ISO 4217 code new prices default to.
	Currency string `json:"currency"`
	// LogLevel is one of debug, info, warn, error.
	LogLevel string `json:"log_level"`

	// dir is where this config was loaded from. Not serialised.
	dir string
}

// Default returns the configuration a fresh install starts with.
func Default() Config {
	return Config{
		Driver:   DriverSQLite,
		DSN:      "",
		Currency: "USD",
		LogLevel: "info",
	}
}

// DataDir returns the directory holding the config file, database and logs,
// creating it if necessary.
//
// Writing to the OS-standard location rather than the working directory is
// what makes the database survive being launched from a Dock icon, a Start
// menu shortcut and a terminal, all of which have different working
// directories.
func DataDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(EnvDataDir)); custom != "" {
		if err := os.MkdirAll(custom, 0o700); err != nil {
			return "", fmt.Errorf("config: creating data directory %s: %w", custom, err)
		}
		return custom, nil
	}

	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: locating user config directory: %w", err)
	}
	dir := filepath.Join(root, AppName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("config: creating data directory %s: %w", dir, err)
	}
	return dir, nil
}

// Load reads the configuration, writing out the defaults on first run.
func Load() (Config, error) {
	dir, err := DataDir()
	if err != nil {
		return Config{}, err
	}

	cfg := Default()
	cfg.dir = dir

	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := cfg.Save(); err != nil {
			return Config{}, err
		}
	case err != nil:
		return Config{}, fmt.Errorf("config: reading %s: %w", fileName, err)
	default:
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: parsing %s: %w", fileName, err)
		}
		cfg.dir = dir
	}

	cfg.applyEnv()
	return cfg, cfg.Validate()
}

func (c *Config) applyEnv() {
	if v := strings.TrimSpace(os.Getenv(EnvDriver)); v != "" {
		c.Driver = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvDSN)); v != "" {
		c.DSN = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvLogLevel)); v != "" {
		c.LogLevel = v
	}
	if v := strings.TrimSpace(os.Getenv(EnvCurrency)); v != "" {
		c.Currency = v
	}
}

// Validate checks the values this build knows how to act on.
func (c Config) Validate() error {
	switch c.Driver {
	case DriverSQLite, DriverPostgres:
	default:
		return fmt.Errorf("config: unknown driver %q (expected %q or %q)",
			c.Driver, DriverSQLite, DriverPostgres)
	}
	if c.Driver == DriverPostgres && strings.TrimSpace(c.DSN) == "" {
		return errors.New("config: the postgres driver requires a connection string in dsn")
	}
	return nil
}

// Dir returns the resolved data directory.
func (c Config) Dir() string { return c.dir }

// DatabasePath returns the SQLite file this configuration points at.
func (c Config) DatabasePath() string {
	if strings.TrimSpace(c.DSN) != "" {
		return c.DSN
	}
	return filepath.Join(c.dir, "inventory.db")
}

// LogDir returns the directory log files are written to.
func (c Config) LogDir() string { return filepath.Join(c.dir, "logs") }

// Save writes the configuration back to disk atomically, so an interrupted
// write cannot leave a truncated file that the next launch refuses to parse.
func (c Config) Save() error {
	if c.dir == "" {
		dir, err := DataDir()
		if err != nil {
			return err
		}
		c.dir = dir
	}

	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encoding: %w", err)
	}
	raw = append(raw, '\n')

	final := filepath.Join(c.dir, fileName)
	tmp, err := os.CreateTemp(c.dir, fileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("config: setting permissions: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return fmt.Errorf("config: replacing %s: %w", final, err)
	}
	return nil
}
