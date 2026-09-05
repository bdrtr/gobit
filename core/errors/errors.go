// Package errors provides gobit's typed error model.
//
// Services return the *Error values produced by this package, and the HTTP
// layer turns the Kind field into a status code (plan Sections 2.7 and 8). The
// package also re-exports the stdlib errors helpers As/Is/Join/New/Unwrap, so a
// caller never has to import two different errors packages.
package errors

import (
	stderrors "errors"
	"fmt"
)

// The stdlib errors helpers, so this package can be imported in its place.
var (
	As     = stderrors.As
	Is     = stderrors.Is
	Join   = stderrors.Join
	New    = stderrors.New
	Unwrap = stderrors.Unwrap
)

// Kind is an error's class. Its zero value is KindInternal, so an unclassified
// error never accidentally behaves like a "not found".
type Kind uint8

// The error classes. For the HTTP mapping see the core/http package.
const (
	// KindInternal is an unexpected server error (the zero value).
	KindInternal Kind = iota
	// KindNotFound reports that the requested resource does not exist.
	KindNotFound
	// KindInvalid reports that the input did not pass validation.
	KindInvalid
	// KindConflict reports a clash with the current state (e.g. a duplicate
	// record).
	KindConflict
	// KindUnauthorized reports that authentication is missing or invalid.
	KindUnauthorized
	// KindForbidden reports that the identity is verified but the privileges
	// are not enough.
	KindForbidden
	// KindUnavailable reports that a subsystem is temporarily unreachable.
	KindUnavailable
	// KindTooManyRequests reports that the client exceeded the rate limit.
	KindTooManyRequests
)

// String returns the Kind's readable name.
//
// These strings are part of the WIRE CONTRACT, not prose: they appear in a
// response body as the fallback error code when a call gave none, and clients
// branch on them. They are never translated (ADR 0012, decision 4).
func (k Kind) String() string {
	switch k {
	case KindNotFound:
		return "not_found"
	case KindInvalid:
		return "invalid"
	case KindConflict:
		return "conflict"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindUnavailable:
		return "unavailable"
	case KindTooManyRequests:
		return "too_many_requests"
	case KindInternal:
		return "internal"
	default:
		return "internal"
	}
}

// Error is gobit's typed error.
type Error struct {
	// Kind is the error's class; the transport layer picks the status code
	// from it.
	Kind Kind
	// Code is the machine-readable, stable code (e.g. "product_not_found").
	// Clients may branch on it; unlike Message it is considered immutable.
	Code string
	// Message is the human-readable explanation. It must contain no sensitive
	// data.
	Message string
	// Details is optional structural context (e.g. which field was invalid).
	Details map[string]any
	// err is the wrapped underlying error; it is reached through Unwrap.
	err error
}

// Error satisfies the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	msg := e.Message
	if e.Code != "" {
		msg = e.Code + ": " + msg
	}
	if e.err != nil {
		return msg + ": " + e.err.Error()
	}
	return msg
}

// Unwrap returns the wrapped error; the errors.Is/As chain needs it.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

// WithDetails adds structural context to the error and returns the same error.
// Details is created when nil; existing keys are overwritten.
func (e *Error) WithDetails(kv map[string]any) *Error {
	if e == nil {
		return nil
	}
	if e.Details == nil {
		e.Details = make(map[string]any, len(kv))
	}
	for k, v := range kv {
		e.Details[k] = v
	}
	return e
}

// newError is the shared constructor.
func newError(kind Kind, code, format string, a ...any) *Error {
	return &Error{
		Kind:    kind,
		Code:    code,
		Message: fmt.Sprintf(format, a...),
	}
}

// NotFound builds an error reporting that the requested resource does not
// exist.
func NotFound(code, format string, a ...any) *Error {
	return newError(KindNotFound, code, format, a...)
}

// Invalid builds an error reporting that the input did not pass validation.
func Invalid(code, format string, a ...any) *Error {
	return newError(KindInvalid, code, format, a...)
}

// Conflict builds an error reporting a clash with the current state.
func Conflict(code, format string, a ...any) *Error {
	return newError(KindConflict, code, format, a...)
}

// Unauthorized reports that authentication is missing or invalid.
func Unauthorized(code, format string, a ...any) *Error {
	return newError(KindUnauthorized, code, format, a...)
}

// Forbidden reports that the identity is verified but the privileges are not
// enough.
func Forbidden(code, format string, a ...any) *Error {
	return newError(KindForbidden, code, format, a...)
}

// TooManyRequests builds an error reporting that the rate limit was exceeded.
func TooManyRequests(code, format string, a ...any) *Error {
	return newError(KindTooManyRequests, code, format, a...)
}

// Unavailable reports that a subsystem is temporarily unreachable.
func Unavailable(code, format string, a ...any) *Error {
	return newError(KindUnavailable, code, format, a...)
}

// Internal reports an unexpected server error.
func Internal(code, format string, a ...any) *Error {
	return newError(KindInternal, code, format, a...)
}

// Wrap wraps an existing error in a typed error. It returns nil when err is
// nil, so the caller needs no separate nil check.
func Wrap(err error, kind Kind, code, format string, a ...any) *Error {
	if err == nil {
		return nil
	}
	e := newError(kind, code, format, a...)
	e.err = err
	return e
}

// KindOf returns the Kind of the first *Error in the chain.
// With no typed error in the chain it returns KindInternal (the safe default).
func KindOf(err error) Kind {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Kind
	}
	return KindInternal
}

// CodeOf returns the Code of the first *Error in the chain, or an empty string.
func CodeOf(err error) string {
	var e *Error
	if stderrors.As(err, &e) {
		return e.Code
	}
	return ""
}

// HasKind reports whether the error is of the given class (along the chain).
// It is named separately so it is not confused with the stdlib Is.
func HasKind(err error, kind Kind) bool {
	return KindOf(err) == kind
}

// IsNotFound reports whether the error is of class KindNotFound.
func IsNotFound(err error) bool { return HasKind(err, KindNotFound) }

// IsInvalid reports whether the error is of class KindInvalid.
func IsInvalid(err error) bool { return HasKind(err, KindInvalid) }

// IsConflict reports whether the error is of class KindConflict.
func IsConflict(err error) bool { return HasKind(err, KindConflict) }

// IsUnauthorized reports whether the error is of class KindUnauthorized.
func IsUnauthorized(err error) bool { return HasKind(err, KindUnauthorized) }

// IsForbidden reports whether the error is of class KindForbidden.
func IsForbidden(err error) bool { return HasKind(err, KindForbidden) }

// IsTooManyRequests reports whether the error is of class KindTooManyRequests.
func IsTooManyRequests(err error) bool { return HasKind(err, KindTooManyRequests) }
