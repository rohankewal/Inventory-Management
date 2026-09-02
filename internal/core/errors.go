package core

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors the storage and service layers wrap. Callers match on these
// with errors.Is rather than on driver-specific errors, so the UI behaves the
// same regardless of which backend is configured.
var (
	// ErrNotFound means the requested record does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict means the write collided with existing data, such as a
	// duplicate SKU or a stale optimistic-concurrency version.
	ErrConflict = errors.New("conflict")
	// ErrInvalid means the input failed validation before any write happened.
	ErrInvalid = errors.New("invalid input")
	// ErrPermission means the actor is not allowed to perform the action.
	ErrPermission = errors.New("permission denied")
)

// FieldError names the specific field that failed validation so the UI can
// attach the message to the right form control.
type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ValidationError collects every field problem found in one pass, so a form
// can show all of its errors at once instead of one per submission.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return ErrInvalid.Error()
	}
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Error()
	}
	return fmt.Sprintf("%s: %s", ErrInvalid, strings.Join(parts, "; "))
}

// Unwrap lets errors.Is(err, ErrInvalid) match a ValidationError.
func (e *ValidationError) Unwrap() error { return ErrInvalid }

// Add records a problem with one field.
func (e *ValidationError) Add(field, format string, args ...any) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
}

// ErrOrNil returns nil when nothing failed, so validators can end with
// `return v.ErrOrNil()`.
func (e *ValidationError) ErrOrNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}
