package http

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// requestIDHeader is the HTTP header carrying the request id. It is read from
// the incoming request and written back onto the response, so a client can
// match the error it saw against the server logs.
const requestIDHeader = "X-Request-Id"

// maxRequestIDLen is the upper bound accepted for a request id coming from
// outside. Longer values are rejected; that stops them from being used to
// inflate log lines and response headers.
const maxRequestIDLen = 128

// requestIDPrefix is the prefix of the generated ids (plan Section 8:
// prefixed ids).
const requestIDPrefix = "req_"

// redactedPlaceholder is written in place of the values masked in the logs.
const redactedPlaceholder = "REDACTED"

// contextKey is the private type this package uses for its context keys.
// Because it is unexported it cannot clash with another package's keys.
type contextKey int

const (
	// requestIDKey is the key of the request id in the context.
	requestIDKey contextKey = iota
	// loggerKey is the key of the request-scoped logger in the context.
	loggerKey
)

// sensitiveQueryMarkers are the fragments that, when they appear INSIDE a
// query parameter's name, require the value to be masked. The list is
// deliberately broad: masking by accident is cheaper than leaking by accident
// (plan Section 8).
//
// The list covers not only credentials but PERSONAL DATA. An access log is
// long-lived and the set of people who can see it is wider than the set who run
// the application; the auth module deliberately does not log the email address,
// and having the same value reach the access log through a
// "GET /admin/v1/users?email=..." filter would undo that decision.
var sensitiveQueryMarkers = []string{
	// Credentials and tokens.
	"token", "secret", "password", "passwd", "passphrase", "pwd", "key",
	"auth", "signature", "credential", "session", "cookie", "jwt", "otp",
	// Personal data. The "mail" fragment catches "email", "e-mail" and
	// compounds such as "customer_email"; writing "email" as well would
	// lengthen the list without widening the coverage.
	"mail", "posta", "phone", "telefon", "msisdn", "gsm",
	"iban", "tckn", "vkn", "cvv", "cvc",
}

// sensitiveQueryExactKeys are the parameter names masked only on an EXACT
// match.
//
// These names are too short to search for as substrings: "pin" occurs inside
// the innocent "shipping" and "tel" inside "telemetry". Put into
// sensitiveQueryMarkers they would make the access log's most useful filters
// unreadable — and the point of masking is not to make the log unusable. The
// cost is deliberate: a compound name such as "card_pin" is not caught here,
// and if such a parameter appears it belongs in sensitiveQueryMarkers.
var sensitiveQueryExactKeys = map[string]struct{}{
	"pin": {},
	"tel": {},
}

// RequestID is the middleware assigning an id to every request.
//
// A valid incoming "X-Request-Id" header is preserved (so the chain does not
// break in distributed tracing); otherwise a new id is generated. The id is put
// into the context (read with RequestIDFromContext) and written to the response
// header of the same name.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// WithRequestID puts the given request id into the context.
// It is used to carry the same correlation id through non-HTTP flows (a worker,
// a cron job).
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the request id put into the context.
// With no id it returns an empty string; the caller needs no separate nil
// check.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithLogger puts the request-scoped logger into the context.
// WriteError and WriteJSON report failures through it.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, log)
}

// LoggerFromContext returns the logger from the context.
// With no logger in the context the slog default is used; the value returned is
// never nil.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if log, ok := ctx.Value(loggerKey).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

// Recoverer produces the middleware that catches handler panics.
//
// On a panic the stack trace is logged and a 500 JSON response goes to the
// client; the process stays up. A panic with http.ErrAbortHandler is the
// stdlib's "drop this request silently" contract and is re-raised as it is.
//
// When the response has already started, no second body is written on top of
// it. To make that reliable Recoverer gives the handler its own wrapper: the
// "has it been written" fact is not guessed from a type another middleware
// produced, so it is independent of the middleware order and of any wrapper in
// between.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	log = loggerOrDefault(log)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithLogger(r.Context(), log)
			r = r.WithContext(ctx)
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// ctx is passed as a parameter rather than re-derived inside the
			// closure, so the cancellation and value chain is preserved.
			defer func(ctx context.Context) {
				rec := recover()
				if rec == nil {
					return
				}

				// The stdlib contract: ErrAbortHandler is not caught and no
				// response is written.
				if err, ok := rec.(error); ok && coreerrors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				log.ErrorContext(ctx, "the handler panicked",
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", RequestIDFromContext(ctx),
				)

				// When the response has already started, writing a second body
				// on top of it sends broken JSON to the client; it is only
				// logged and left alone.
				if rw.wroteHeader {
					return
				}

				WriteJSON(ctx, rw, http.StatusInternalServerError,
					newErrorResponse(ctx, defaultInternalCode, genericInternalMessage, nil))
			}(ctx)

			next.ServeHTTP(rw, r)
		})
	}
}

// RequestLogger produces the middleware that logs every request structurally.
//
// The fields recorded: method, path, the (masked) query, status, bytes,
// duration_ms and request_id. Sensitive data is not logged (plan Section 8):
// the request headers — Authorization and Cookie above all — are never read,
// the values of query parameters whose names carry a token or personal data are
// masked (see redactQuery), and neither request nor response bodies enter the
// log.
//
// path is NOT masked: the route template is the access log's primary axis and
// masking it would leave nothing traceable. The counterpart is that routes must
// not put personal data into a path segment and must address resources by id.
//
// The line is written with defer: the access record is not lost even when the
// handler panics (or drops the request with http.ErrAbortHandler) — and those
// are exactly the requests most worth following. When a panic passed through
// the chain before the response started, nothing reached the client and the
// status is recorded as 500.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	log = loggerOrDefault(log)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			ctx := WithLogger(r.Context(), log)
			finished := false

			defer func() {
				status := rw.status
				if !finished && !rw.wroteHeader {
					status = http.StatusInternalServerError
				}

				elapsed := time.Since(start)
				log.Log(ctx, levelForStatus(status), "request completed",
					"method", r.Method,
					"path", r.URL.Path,
					"query", redactQuery(r.URL.RawQuery),
					"status", status,
					"bytes", rw.bytes,
					"duration_ms", float64(elapsed.Microseconds())/1000.0,
					"request_id", RequestIDFromContext(ctx),
				)
			}()

			next.ServeHTTP(rw, r.WithContext(ctx))
			finished = true
		})
	}
}

// responseWriter is the http.ResponseWriter wrapper counting the response's
// status code and the bytes written.
//
// It stays as transparent to the handlers as it can: Unwrap forwards the
// http.ResponseController path, Flush forwards streaming and Hijack forwards a
// protocol upgrade.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

// WriteHeader records the status code and forwards it to the underlying
// writer. Calls after the first are ignored; that is what keeps the stdlib's
// "superfluous WriteHeader" warning from firing.
func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

// Write writes the body and accumulates the number of bytes written.
func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap returns the underlying ResponseWriter.
// This method is what lets http.ResponseController find capabilities such as
// Hijack and SetDeadline behind the wrapper.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack hands the underlying TCP connection over to the handler.
//
// WebSockets, protocol upgrades and some SSE libraries make a direct
// w.(http.Hijacker) type assertion instead of using http.ResponseController;
// without this method the wrapper would hide that capability and those handlers
// would break. When the underlying writer does not support hijacking it returns
// http.ErrNotSupported.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	conn, buf, err := h.Hijack()
	if err == nil {
		// The connection was taken over: a response can no longer be written
		// the normal way.
		w.wroteHeader = true
	}
	return conn, buf, err
}

// Flush flushes the underlying writer; it keeps the wrapper transparent for
// streaming (SSE) handlers.
func (w *responseWriter) Flush() {
	f, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	// Flush causes the headers to be sent; the state record is updated
	// accordingly.
	w.wroteHeader = true
	f.Flush()
}

// newRequestID generates a new, cryptographically random request id.
func newRequestID() string {
	return requestIDPrefix + rand.Text()
}

// sanitizeRequestID validates an id coming from outside.
//
// Only printable ASCII and at most maxRequestIDLen characters are accepted;
// every value outside the rule is reduced to an empty string (fail-closed) and
// the caller generates a new id instead. That way the client controls neither
// the log lines nor the response header.
func sanitizeRequestID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxRequestIDLen {
		return ""
	}
	if strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0 {
		return ""
	}
	return v
}

// redactQuery makes the query string loggable: the values of parameters with a
// sensitive name are masked while the rest (pagination, filters) stays as it
// is. A query that cannot be parsed is masked entirely.
//
// The masking is a DENY-LIST and its cost is accepted openly: a new sensitive
// parameter that is not on the list — a "?recovery_hint=..." added tomorrow —
// is logged unmasked, and the protection arrives only when the name is added.
// The opposite option, an allow-list, was impossible at this layer: the core
// does not know the modules (Principle 2.4), so core/http cannot know any
// module's filter names in advance. An allow-list would either stay
// permanently incomplete — masking every new endpoint's query in full and
// blinding the access log — or force the modules to register names with the
// core, reversing the direction of the dependency.
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return redactedPlaceholder
	}

	for key, vals := range values {
		if !isSensitiveKey(key) {
			continue
		}
		for i := range vals {
			vals[i] = redactedPlaceholder
		}
	}

	return values.Encode()
}

// isSensitiveKey reports whether a parameter name carries a sensitive value.
//
// The comparison is made in lower case: the client decides the key's name, and
// writing "?Email=" or "?EMAIL=" must not be enough to slip past the masking.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	if _, ok := sensitiveQueryExactKeys[lower]; ok {
		return true
	}
	for _, marker := range sensitiveQueryMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// levelForStatus picks the log level matching the status code:
// 5xx is error, 4xx is warning and the rest is info.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// loggerOrDefault puts the slog default in place of a nil logger.
func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
