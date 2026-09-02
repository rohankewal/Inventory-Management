// Package logging sets up structured application logging.
//
// A desktop app has no terminal to read once it is installed, so logs go to a
// rotating file in the user's data directory. That file is the only thing a
// support request has to go on when something fails on a machine nobody can
// reach.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	logFileName = "inventory.log"
	// maxFileBytes is the size at which the current log is rotated.
	maxFileBytes = 5 << 20 // 5 MiB
	// maxBackups is how many rotated files are kept, bounding disk use at
	// roughly (maxBackups+1) * maxFileBytes.
	maxBackups = 3
)

// Options configure Setup.
type Options struct {
	// Dir is the directory to write log files into.
	Dir string
	// Level is one of debug, info, warn, error. Unrecognised values fall back
	// to info rather than failing startup.
	Level string
	// Console also writes to stderr, which is what you want when running from
	// a terminal during development.
	Console bool
}

// Setup installs a logger as the slog default and returns it along with a
// closer for the log file.
func Setup(opts Options) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("logging: creating log directory %s: %w", opts.Dir, err)
	}

	w := &rotatingWriter{path: filepath.Join(opts.Dir, logFileName)}
	if err := w.open(); err != nil {
		return nil, nil, err
	}

	var out io.Writer = w
	if opts.Console {
		out = io.MultiWriter(w, os.Stderr)
	}

	handler := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: parseLevel(opts.Level)})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger, w, nil
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// rotatingWriter appends to a log file, rolling it over once it grows past
// maxFileBytes. It is deliberately small: pulling in a rotation dependency for
// this would add more surface than it saves.
type rotatingWriter struct {
	path string

	mu   sync.Mutex
	file *os.File
	size int64
}

func (w *rotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("logging: opening %s: %w", w.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("logging: inspecting %s: %w", w.path, err)
	}
	w.file, w.size = f, info.Size()
	return nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > maxFileBytes {
		if err := w.rotate(); err != nil {
			// Losing rotation is not worth losing the log line, so fall
			// through and keep writing to the oversized file.
			fmt.Fprintf(os.Stderr, "logging: rotation failed: %v\n", err)
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes the current file, shifts the backups along, and reopens.
func (w *rotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil

	// Drop the oldest, then shift each remaining backup one slot older.
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, maxBackups))
	for i := maxBackups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		// Reopen regardless so logging continues.
		_ = w.open()
		return err
	}
	return w.open()
}

func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// Recover logs a panic with its stack and hands it to onPanic, which is how the
// UI gets a chance to show the user a dialog before the process exits. Install
// it with defer at the top of every goroutine that runs user work.
func Recover(logger *slog.Logger, onPanic func(v any)) {
	v := recover()
	if v == nil {
		return
	}
	logger.Error("recovered from panic", "panic", fmt.Sprint(v), "stack", stack())
	if onPanic != nil {
		onPanic(v)
	}
}

func stack() string {
	buf := make([]byte, 8<<10)
	n := runtimeStack(buf)
	return string(buf[:n])
}
