package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// contentTypeJSON is the Content-Type of JSON responses.
const contentTypeJSON = "application/json; charset=utf-8"

// defaultInternalCode is the code reported to the client for unclassified
// server errors.
const defaultInternalCode = "internal_error"

// genericInternalMessage is the fixed message returned to the client for
// KindInternal errors. The underlying error's text is NEVER written to the
// client: it may contain SQL fragments, connection strings or file paths
// (plan Section 8).
const genericInternalMessage = "an unexpected server error occurred"

// fallbackErrorBody is the fixed response written when the body cannot be
// encoded. Re-encoding is not attempted, so there is no risk of an endless
// loop.
const fallbackErrorBody = `{"error":{"code":"internal_error","message":"the response could not be produced"}}` + "\n"

// ErrorResponse is the outer envelope of error responses.
// Every error body is gathered under a single "error" key.
type ErrorResponse struct {
	// Error holds the details of the failure.
	Error ErrorBody `json:"error"`
}

// ErrorBody is the content of the error envelope.
type ErrorBody struct {
	// Code is the machine-readable, stable error code (e.g. "product_not_found").
	Code string `json:"code"`
	// Message is the human-readable explanation.
	Message string `json:"message"`
	// Details is optional structural context (e.g. the invalid fields).
	Details map[string]any `json:"details,omitempty"`
	// RequestID is the request's correlation id; it ties a support ticket to
	// the log.
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON writes the given value to the response as JSON.
//
// The body is encoded into memory FIRST; if encoding fails the status code has
// not been sent yet, so the client gets a 500 rather than half a body. When v
// is nil only the header and the status are written.
func WriteJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if v != nil {
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			LoggerFromContext(ctx).ErrorContext(ctx, "response body could not be encoded",
				"error", err,
				"request_id", RequestIDFromContext(ctx),
			)
			w.Header().Set("Content-Type", contentTypeJSON)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(fallbackErrorBody))
			return
		}
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if buf.Len() == 0 {
		return
	}
	if _, err := buf.WriteTo(w); err != nil {
		// Nothing can be done once the status code has gone out (the client
		// may have closed the connection); it is only recorded.
		LoggerFromContext(ctx).ErrorContext(ctx, "response body could not be written",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// contentTypeHTML is the Content-Type of HTML responses.
const contentTypeHTML = "text/html; charset=utf-8"

// headerContentTypeOptions and nosniff stop the browser from reinterpreting the
// body according to its own guess. JSON responses do not need it (a browser does
// not execute them); HTML and asset responses do, and file serving writes it for
// the same reason.
const (
	headerContentTypeOptions = "X-Content-Type-Options"
	nosniff                  = "nosniff"
)

// WriteHTML writes an already-rendered HTML body to the response.
//
// # The body must be produced BEFOREHAND
//
// The signature deliberately takes a []byte and not a template: the caller
// renders the template into memory FIRST, calls [WriteError] on failure, and
// only reaches this function on success. In a design that streams the template
// straight to the writer, an error arising in the MIDDLE of the template leaves
// HALF a page carrying a 200 status: the header is already out, so neither the
// panic recoverer nor the error writer can do anything and the failure goes
// silent on the client. The same reasoning is why [WriteJSON] encodes into
// memory first.
//
// # The status code is FREE
//
// Unlike [WriteJSON] there is no 2xx requirement here, and that is deliberate:
// returning the login page to an unidentified browser with a 401 is more honest
// than sending it somewhere else with a 303 — a redirect erases the failure
// from the status code.
//
// The only user of this surface today is the admin panel (ADR 0011). The
// framework's JSON error envelope is UNCHANGED: an API endpoint still writes
// errors through [WriteError], and the panel's existence does not split that
// policy into a second one anywhere.
func WriteHTML(ctx context.Context, w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", contentTypeHTML)
	w.Header().Set(headerContentTypeOptions, nosniff)
	w.WriteHeader(status)

	if len(body) == 0 {
		return
	}
	if _, err := w.Write(body); err != nil {
		// Nothing can be done once the status code has gone out.
		LoggerFromContext(ctx).ErrorContext(ctx, "HTML body could not be written",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// WriteRedirect sends the browser to another path.
//
// net/http's own redirector is NOT used: it writes the body outside the core,
// and the repository-wide rule that a response body passes through a single
// door rejects it structurally. The implementation here has no body; a
// redirect's body is not shown by any browser anyway.
//
// The status code is 303 and is not left to the caller: after a form POST, 303
// tells the browser to "go to the target with GET" and stops a refresh from
// resubmitting the form. That is the only legitimate reason for a redirect in
// the panel.
func WriteRedirect(_ context.Context, w http.ResponseWriter, target string) {
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusSeeOther)
}

// WriteAsset writes an embedded static asset.
//
// It stands apart from [WriteHTML] because of caching: assets are embedded in
// the binary, so they do NOT change within a release and there is no reason for
// the browser to fetch them again on every page. HTML pages, by contrast,
// depend on the session and on data and cannot be cached; gathering the two
// into one surface would mean writing the header that is right for one of them
// onto the other.
//
// etag is the version stamp supplied by the caller; when empty no cache header
// is written.
func WriteAsset(ctx context.Context, w http.ResponseWriter, contentType, etag string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set(headerContentTypeOptions, nosniff)
	if etag != "" {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.WriteHeader(http.StatusOK)

	if len(body) == 0 {
		return
	}
	if _, err := w.Write(body); err != nil {
		LoggerFromContext(ctx).ErrorContext(ctx, "asset body could not be written",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// WriteError writes the error with the status code matching its kind and a
// consistent JSON envelope.
//
// For KindInternal errors the underlying error text is not leaked to the
// client: a generic message goes into the body while the real error (including
// the wrapped chain) is recorded with the logger from the context. For the
// other kinds Message and Details are considered safe; a service author does
// not put sensitive data into those fields (plan Section 8).
//
// A nil or typed-nil error is handled safely too: a 500 is written, no panic is
// raised.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	typed, ok := typedError(err)

	kind := coreerrors.KindInternal
	var code string
	if ok {
		kind = typed.Kind
		code = typed.Code
	}
	policy := policyForKind(kind)
	status := policy.status

	if !policy.clientSafe {
		LoggerFromContext(ctx).ErrorContext(ctx, "request ended with a server error",
			"error", err,
			"code", code,
			"status", status,
			"request_id", RequestIDFromContext(ctx),
		)
		if code == "" {
			code = defaultInternalCode
		}
		WriteJSON(ctx, w, status, newErrorResponse(ctx, code, genericInternalMessage, nil))
		return
	}

	message := kind.String()
	var details map[string]any
	if ok {
		if typed.Message != "" {
			message = typed.Message
		}
		details = typed.Details
	}
	if code == "" {
		code = kind.String()
	}

	WriteJSON(ctx, w, status, newErrorResponse(ctx, code, message, details))
}

// StatusFor returns the HTTP status code corresponding to the error's kind.
//
// The mapping is defined in plan Section 8. An untyped (or nil) error counts as
// KindInternal and yields 500, so an unclassified error is never accidentally
// reported as a client error. Finding a typed-nil *errors.Error in the chain
// (see typedError) ends the same way, with a 500.
func StatusFor(err error) int {
	kind := coreerrors.KindInternal
	if typed, ok := typedError(err); ok {
		kind = typed.Kind
	}
	return policyForKind(kind).status
}

// typedError returns the first *errors.Error in the chain.
//
// When the pointer found is nil the second result is false. The trap is real:
// errors.Wrap returns (*Error)(nil) when the error being wrapped is nil, that
// value makes "err != nil" true once it is put into the error interface, and
// reaching for its fields would panic. The HTTP layer treats such an error as
// unclassified.
func typedError(err error) (*coreerrors.Error, bool) {
	var typed *coreerrors.Error
	if coreerrors.As(err, &typed) && typed != nil {
		return typed, true
	}
	return nil, false
}

// kindPolicy decides an error kind's HTTP counterpart and whether its body may
// be handed to the client as it is.
type kindPolicy struct {
	status int
	// clientSafe true means the Message written by the service reaches the
	// client verbatim. False means the message is masked and the real error is
	// only logged.
	clientSafe bool
}

// policyForKind returns the policy matching the kind.
//
// coreerrors.Kind is a uint8 and the Error.Kind field is exported, so a caller
// can construct a value outside the enum. Such a value falls to the SAFE side:
// 500 and masking. Otherwise an unrecognized kind would leak internal server
// detail (a DSN, a query, a file path) to the client.
func policyForKind(kind coreerrors.Kind) kindPolicy {
	switch kind {
	case coreerrors.KindNotFound:
		return kindPolicy{status: http.StatusNotFound, clientSafe: true}
	case coreerrors.KindInvalid:
		return kindPolicy{status: http.StatusUnprocessableEntity, clientSafe: true}
	case coreerrors.KindConflict:
		return kindPolicy{status: http.StatusConflict, clientSafe: true}
	case coreerrors.KindUnauthorized:
		return kindPolicy{status: http.StatusUnauthorized, clientSafe: true}
	case coreerrors.KindForbidden:
		return kindPolicy{status: http.StatusForbidden, clientSafe: true}
	case coreerrors.KindUnavailable:
		return kindPolicy{status: http.StatusServiceUnavailable, clientSafe: true}
	case coreerrors.KindTooManyRequests:
		return kindPolicy{status: http.StatusTooManyRequests, clientSafe: true}
	case coreerrors.KindInternal:
		return kindPolicy{status: http.StatusInternalServerError, clientSafe: false}
	default:
		return kindPolicy{status: http.StatusInternalServerError, clientSafe: false}
	}
}

// newErrorResponse builds the error envelope and adds the request id from the
// context.
func newErrorResponse(ctx context.Context, code, message string, details map[string]any) ErrorResponse {
	return ErrorResponse{
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: RequestIDFromContext(ctx),
		},
	}
}
