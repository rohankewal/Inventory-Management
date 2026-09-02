// Command inventory is the desktop inventory management client.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/bootstrap"
	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/logging"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/ui"
)

// version is stamped by the release build via -ldflags.
var version = "dev"

// startupTimeout bounds opening and migrating the database, so a locked or
// unreachable database reports an error instead of hanging on a splash screen.
const startupTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		// The window may never have opened, so stderr is the only channel left.
		fmt.Fprintf(os.Stderr, "inventory: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log, closeLog, err := logging.Setup(logging.Options{
		Dir:     cfg.LogDir(),
		Level:   cfg.LogLevel,
		Console: os.Getenv("INVENTORY_CONSOLE_LOG") != "",
	})
	if err != nil {
		return err
	}
	defer func() { _ = closeLog.Close() }()

	// A panic that escapes the UI must still be recorded; an installed desktop
	// app has no terminal for the runtime's own trace to land in.
	defer logging.Recover(log, nil)

	log.Info("starting",
		"version", version,
		"driver", cfg.Driver,
		"data_dir", cfg.Dir())

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	store, err := bootstrap.OpenStore(ctx, cfg, bootstrap.OpenOptions{})
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Error("closing the database", "error", err)
		}
	}()

	svc := service.NewInventory(store,
		service.WithLogger(log),
		service.WithDefaultCurrency(core.Currency(cfg.Currency)),
	)

	ui.New(svc, cfg, log).Run()

	log.Info("stopped")
	return nil
}
