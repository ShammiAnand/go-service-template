// Package apperr defines domain errors. Services return these (or wrap
// them); the HTTP layer maps them to status codes in one place
// (httpapi.MapError). Handlers never pick status codes for errors.
package apperr

import (
	"errors"
	"fmt"
)

// Kind classifies an error for transport mapping.
type Kind int

// Error kinds.
const (
	KindInvalid Kind = iota + 1
	KindNotFound
	KindConflict
	KindUnauthorized
	KindForbidden
)

// Error is a domain error with a message safe to show to callers.
type Error struct {
	Kind Kind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(k Kind, msg string, wrapped []error) *Error {
	return &Error{Kind: k, Msg: msg, Err: errors.Join(wrapped...)}
}

// Invalid reports bad input that schema validation cannot catch.
func Invalid(msg string, wrapped ...error) error { return newErr(KindInvalid, msg, wrapped) }

// NotFound reports a missing resource.
func NotFound(msg string, wrapped ...error) error { return newErr(KindNotFound, msg, wrapped) }

// Conflict reports a state conflict, for example a duplicate.
func Conflict(msg string, wrapped ...error) error { return newErr(KindConflict, msg, wrapped) }

// Unauthorized reports a missing or bad credential.
func Unauthorized(msg string, wrapped ...error) error { return newErr(KindUnauthorized, msg, wrapped) }

// Forbidden reports a valid caller without permission.
func Forbidden(msg string, wrapped ...error) error { return newErr(KindForbidden, msg, wrapped) }

// As returns the *Error in err's chain, if any.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
