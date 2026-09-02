# Development tasks. `make help` lists them.

APP_NAME    := inventory
CTL_NAME    := inventoryctl
MODULE      := github.com/rohankewalramani/inventory-sys
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -X main.version=$(VERSION)
GO          ?= go

# The sandbox lives beside the source. It is quoted everywhere it is used
# because a project path containing a space is entirely ordinary on a desktop.
SANDBOX     := $(CURDIR)/local

.DEFAULT_GOAL := help

## help: List the available targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /' | sort

## tidy: Sync go.mod and go.sum.
tidy:
	$(GO) mod tidy

## fmt: Format every Go file.
fmt:
	$(GO) fmt ./...

## fmt-check: Fail if any file is unformatted. Used by CI.
fmt-check:
	@unformatted=$$(gofmt -l . | grep -v '^vendor/' || true); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi

## vet: Run go vet.
vet:
	$(GO) vet ./...

## lint: Run golangci-lint. Install it with `make tools`.
lint:
	golangci-lint run ./...

## test: Run the full test suite with the race detector.
test:
	$(GO) test -race -count=1 ./...

## test-short: Run tests without the race detector, for a fast inner loop.
test-short:
	$(GO) test -count=1 ./...

## cover: Run tests and open a coverage report.
cover:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report written to coverage.html"

## build: Build both binaries into ./bin.
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(APP_NAME) ./cmd/inventory
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(CTL_NAME) ./cmd/inventoryctl

## run: Build and launch the desktop app with console logging on.
run:
	INVENTORY_CONSOLE_LOG=1 $(GO) run -ldflags "$(LDFLAGS)" ./cmd/inventory

## run-sandbox: Launch the app against a throwaway database under ./local.
run-sandbox:
	@mkdir -p local
	INVENTORY_CONSOLE_LOG=1 INVENTORY_DATA_DIR="$(SANDBOX)" \
		$(GO) run -ldflags "$(LDFLAGS)" ./cmd/inventory

## ctl: Run the admin CLI, e.g. `make ctl ARGS=status`.
ctl:
	$(GO) run -ldflags "$(LDFLAGS)" ./cmd/inventoryctl $(ARGS)

## demo: Fill the sandbox database under ./local with a year of realistic data.
demo:
	@mkdir -p local
	INVENTORY_DATA_DIR="$(SANDBOX)" $(GO) run ./cmd/demoseed

## shots: Render every screen to ./local/shots without needing a display.
shots:
	@mkdir -p local/shots
	INVENTORY_SHOT_DIR="$(SANDBOX)/shots" \
	INVENTORY_SHOT_DB="$(SANDBOX)/inventory.db" \
	$(GO) test ./internal/ui/ -run TestRenderScreens -count=1 -v

## check: Everything CI runs, in one command.
check: fmt-check vet test

## tools: Install the development tools this project uses.
tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

## clean: Remove build output and local scratch data.
clean:
	rm -rf $(BIN_DIR) dist coverage.out coverage.html local

.PHONY: help tidy fmt fmt-check vet lint test test-short cover build run run-sandbox ctl demo shots check tools clean
