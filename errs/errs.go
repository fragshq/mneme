// Package errs holds the sentinel errors shared by every Mneme package.
//
// Callers should test errors with errors.Is; Mneme wraps these sentinels with
// context, and the root package re-exports them for convenience.
package errs

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound reports that a record type definition or record does not exist.
	ErrNotFound = errors.New("mneme: not found")
	// ErrConflict reports a uniqueness violation (duplicate version or record ID).
	ErrConflict = errors.New("mneme: conflict")
	// ErrInvalid reports invalid caller input: a malformed schema, ID spec,
	// filter, or a record that does not conform to its schema.
	ErrInvalid = errors.New("mneme: invalid input")
	// ErrNoTenant reports that a store has tenancy enabled but the context
	// names no tenant. Stores fail closed with it instead of falling back to
	// a shared default. It is a deployment problem, not caller input, so it
	// does not match ErrInvalid.
	ErrNoTenant = errors.New("mneme: tenancy is enabled but the context names no tenant")
	// ErrQuota reports that a write would exceed a configured maximum (record
	// types, or records per type). The input itself is fine, so it does not
	// match ErrInvalid; deleting data makes room again.
	ErrQuota = errors.New("mneme: quota exceeded")
)

// Invalidf formats an error that matches ErrInvalid: the message is
// "mneme: invalid input: " followed by the formatted text. Every caller-input
// problem is reported this way.
func Invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}
