package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rohankewalramani/inventory-sys/internal/core"
)

// humanError turns an error into something worth showing a user.
//
// Raw errors leak SQL and internal identifiers, and a message like
// "constraint failed: 2067" tells an operator nothing they can act on. The
// full error still reaches the log; this is only what appears on screen.
func humanError(err error) string {
	if err == nil {
		return ""
	}

	var validation *core.ValidationError
	if errors.As(err, &validation) {
		parts := make([]string, len(validation.Fields))
		for i, f := range validation.Fields {
			parts[i] = f.Message
		}
		return strings.Join(parts, "\n")
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "The database did not respond in time. If this keeps happening, check the connection settings."
	case errors.Is(err, context.Canceled):
		return "The operation was cancelled."
	case errors.Is(err, core.ErrNotFound):
		return "That record no longer exists. It may have been removed by someone else."
	case errors.Is(err, core.ErrConflict):
		return conflictMessage(err)
	case errors.Is(err, core.ErrPermission):
		return "You do not have permission to do that."
	case errors.Is(err, core.ErrInvalid):
		return trimSentinel(err.Error())
	}
	return "Something went wrong. The details have been written to the log file."
}

// conflictMessage keeps the store's specific explanation when it wrote one,
// because "a record with that identifier already exists" is more useful than
// a generic conflict notice.
func conflictMessage(err error) string {
	if msg := trimSentinel(err.Error()); msg != "" {
		return msg
	}
	return "That change conflicts with existing data."
}

// trimSentinel strips the wrapped-error prefixes so the user sees the specific
// explanation rather than the chain of operations that produced it.
func trimSentinel(msg string) string {
	for _, sentinel := range []string{
		core.ErrInvalid.Error() + ": ",
		core.ErrConflict.Error() + ": ",
		core.ErrNotFound.Error() + ": ",
	} {
		if idx := strings.LastIndex(msg, sentinel); idx >= 0 {
			msg = msg[idx+len(sentinel):]
		}
	}
	if msg == "" {
		return ""
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

// wrapf annotates an error with the user-facing action that failed.
func wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), err)
}
