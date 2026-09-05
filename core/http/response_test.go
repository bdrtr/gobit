package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

func TestStatusForMapsEveryKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{"not found", coreerrors.NotFound("product_not_found", "no such product"), http.StatusNotFound},
		{"invalid", coreerrors.Invalid("invalid_body", "the body is invalid"), http.StatusUnprocessableEntity},
		{"conflict", coreerrors.Conflict("sku_exists", "the sku is taken"), http.StatusConflict},
		{"unauthorized", coreerrors.Unauthorized("no_token", "no token"), http.StatusUnauthorized},
		{"forbidden", coreerrors.Forbidden("no_scope", "not permitted"), http.StatusForbidden},
		{"unavailable", coreerrors.Unavailable("db_down", "the database is down"), http.StatusServiceUnavailable},
		{"internal", coreerrors.Internal("boom", "it blew up"), http.StatusInternalServerError},
		{"untyped error", coreerrors.New("an ordinary error"), http.StatusInternalServerError},
		{"nil error", nil, http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, corehttp.StatusFor(tt.err))
		})
	}
}

func TestStatusForReadsThroughWrapping(t *testing.T) {
	t.Parallel()

	// Wrapping with core/errors.Wrap.
	wrapped := coreerrors.Wrap(coreerrors.New("pq: deadlock detected"),
		coreerrors.KindConflict, "order_conflict", "the order could not be updated")
	assert.Equal(t, http.StatusConflict, corehttp.StatusFor(wrapped))

	// Wrapping with fmt.Errorf %w: the typed error sits deep in the chain.
	deep := fmt.Errorf("service layer: %w",
		fmt.Errorf("repository: %w", coreerrors.NotFound("cart_not_found", "no such cart")))
	assert.Equal(t, http.StatusNotFound, corehttp.StatusFor(deep))

	// The untyped error a typed error wraps does not decide the kind; the
	// outermost typed error does.
	unavailable := coreerrors.Wrap(coreerrors.New("i/o timeout"),
		coreerrors.KindUnavailable, "redis_down", "redis is unreachable")
	assert.Equal(t, http.StatusServiceUnavailable, corehttp.StatusFor(unavailable))
}

// looksNil reports whether the error appears nil through the error interface.
//
// The comparison is kept in a separate function: at the call site the concrete
// type is known, so static analysis reports "always true", while the test wants
// to prove exactly this runtime behavior. testify's NotNil would not do
// either — it uses reflection, counts a typed-nil pointer as nil and would hide
// the trap.
func looksNil(err error) bool { return err == nil }

// typedNilError imitates the trap a real service layer produces: errors.Wrap
// returns (*Error)(nil) when the error being wrapped is nil.
func typedNilError(underlying error) error {
	return coreerrors.Wrap(underlying, coreerrors.KindInternal, "db_failed", "the query failed")
}

func TestStatusForDoesNotPanicOnTypedNil(t *testing.T) {
	t.Parallel()

	err := typedNilError(nil)
	require.False(t, looksNil(err), "a typed-nil pointer does not look nil through the error interface")

	require.NotPanics(t, func() {
		assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(err),
			"an error that cannot be classified must be a 500")
	})

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { corehttp.WriteError(ctx, rec, err) })

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body must be JSON: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Equal(t, "an unexpected server error occurred", body.Error.Message)
	assert.Contains(t, buf.String(), "request ended with a server error", "the error must still be logged")
}

func TestStatusForDoesNotPanicOnTypedNilDeepInTheChain(t *testing.T) {
	t.Parallel()

	var empty *coreerrors.Error
	deep := fmt.Errorf("service layer: %w", empty)

	require.NotPanics(t, func() {
		assert.Equal(t, http.StatusInternalServerError, corehttp.StatusFor(deep))
	})
	require.NotPanics(t, func() {
		corehttp.WriteError(t.Context(), httptest.NewRecorder(), deep)
	})
}

func TestWriteErrorDoesNotLeakTheUnderlyingMessageOnInternal(t *testing.T) {
	t.Parallel()

	const (
		secretDriverError = `pq: password authentication failed for user "gobit_admin"`
		secretQuery       = "SELECT secret FROM api_keys WHERE id = $1"
	)

	log, buf := testLogger()
	ctx := corehttp.WithLogger(corehttp.WithRequestID(t.Context(), "req_internal"), log)

	err := coreerrors.Wrap(coreerrors.New(secretDriverError),
		coreerrors.KindInternal, "db_query_failed", "the query failed: %s", secretQuery)

	rec := httptest.NewRecorder()
	corehttp.WriteError(ctx, rec, err)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	body := rec.Body.String()
	assert.NotContains(t, body, secretDriverError, "the underlying driver error must not reach the client")
	assert.NotContains(t, body, "password authentication")
	assert.NotContains(t, body, secretQuery, "the typed error's Message must not leak either")
	assert.NotContains(t, body, "SELECT")

	var parsed corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &parsed), "the body must be JSON: %s", body)
	assert.Equal(t, "db_query_failed", parsed.Error.Code, "the machine code is preserved")
	assert.Equal(t, "an unexpected server error occurred", parsed.Error.Message)
	assert.Equal(t, "req_internal", parsed.Error.RequestID)
	assert.Nil(t, parsed.Error.Details, "Details do not reach the client on an internal error")

	// PROOF: the real error is not lost, it is written to the log.
	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "request ended with a server error", records[0]["msg"])
	assert.Contains(t, records[0]["error"], secretDriverError, "the underlying driver error must be logged")
	assert.Contains(t, records[0]["error"], secretQuery, "the wrapping message must be logged too")
	assert.Equal(t, "req_internal", records[0]["request_id"])
}

func TestWriteErrorMasksUntypedErrorsToo(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)

	rec := httptest.NewRecorder()
	corehttp.WriteError(ctx, rec, coreerrors.New("open /etc/gobit/secrets.yaml: permission denied"))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secrets.yaml", "an unclassified error is not leaked either")

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal_error", body.Error.Code, "an error without a code gets the default one")
	assert.Equal(t, "an unexpected server error occurred", body.Error.Message)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Contains(t, records[0]["error"], "secrets.yaml", "the real error must be logged")
}

func TestWriteErrorPassesTheMessageThroughForOtherKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", coreerrors.NotFound("product_not_found", "product not found: %s", "prod_1"), http.StatusNotFound, "product_not_found"},
		{"invalid", coreerrors.Invalid("invalid_email", "the email is invalid"), http.StatusUnprocessableEntity, "invalid_email"},
		{"conflict", coreerrors.Conflict("sku_exists", "the sku is already taken"), http.StatusConflict, "sku_exists"},
		{"unauthorized", coreerrors.Unauthorized("token_expired", "the token has expired"), http.StatusUnauthorized, "token_expired"},
		{"forbidden", coreerrors.Forbidden("scope_missing", "you are not permitted to do this"), http.StatusForbidden, "scope_missing"},
		{"unavailable", coreerrors.Unavailable("payment_provider_down", "the payment provider is unreachable"), http.StatusServiceUnavailable, "payment_provider_down"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			corehttp.WriteError(t.Context(), rec, tt.err)

			assert.Equal(t, tt.wantStatus, rec.Code)
			assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

			var body corehttp.ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body must be JSON: %s", rec.Body.String())
			assert.Equal(t, tt.wantCode, body.Error.Code)

			var typed *coreerrors.Error
			require.True(t, coreerrors.As(tt.err, &typed))
			assert.Equal(t, typed.Message, body.Error.Message, "outside internal, the message passes through as it is")
		})
	}
}

func TestWriteErrorPassesDetailsThrough(t *testing.T) {
	t.Parallel()

	err := coreerrors.Invalid("validation_failed", "the input could not be validated").
		WithDetails(map[string]any{"field": "email", "rule": "format"})

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, err)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body must be JSON: %s", rec.Body.String())
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, map[string]any{"field": "email", "rule": "format"}, body.Error.Details)
}

func TestWriteErrorFallsBackToTheKindNameWhenThereIsNoCode(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, coreerrors.NotFound("", "not found"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "not_found", body.Error.Code)
}

func TestWriteErrorPutsTheRequestIDInTheBody(t *testing.T) {
	t.Parallel()

	// The id is produced by the real middleware chain, not injected by hand.
	h := corehttp.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corehttp.WriteError(r.Context(), w, coreerrors.NotFound("cart_not_found", "cart not found"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/store/v1/carts/cart_1", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_body_id")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body must be JSON: %s", rec.Body.String())
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "req_body_id", body.Error.RequestID)
	assert.Equal(t, rec.Header().Get(requestIDHeaderName), body.Error.RequestID,
		"the id in the body must match the one in the response header")
}

func TestWriteErrorDropsTheFieldWhenThereIsNoRequestID(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteError(t.Context(), rec, coreerrors.NotFound("x_not_found", "not there"))

	assert.NotContains(t, rec.Body.String(), "request_id", "an empty id is not written to the body")
}

func TestWriteJSONWritesTheBodyAndTheHeaders(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(t.Context(), rec, http.StatusCreated, map[string]any{"id": "prod_1", "count": 2})

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "prod_1", body["id"])
	assert.InDelta(t, 2.0, body["count"], 0)
}

func TestWriteJSONWritesNoBodyForNil(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(t.Context(), rec, http.StatusNoContent, nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// unencodableValue is a type JSON cannot encode; it exercises WriteJSON's error
// path.
type unencodableValue struct {
	Fn func() `json:"fn"`
}

func TestWriteJSONReturns500OnAnEncodingError(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	ctx := corehttp.WithLogger(t.Context(), log)

	rec := httptest.NewRecorder()
	corehttp.WriteJSON(ctx, rec, http.StatusOK, unencodableValue{Fn: func() {}})

	assert.Equal(t, http.StatusInternalServerError, rec.Code, "a body that cannot be encoded must not return a 200")
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the fallback body must be valid JSON: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Contains(t, buf.String(), "response body could not be encoded")
}

// TestWriteErrorMasksUnknownKind proves a Kind value outside the enum is masked
// too.
//
// Regression: the masking decision hung on the equality `kind == KindInternal`.
// coreerrors.Kind is a uint8 and the Error.Kind field is exported, so a caller
// can construct a value outside the enum; such an error returned a 500 but its
// body was NOT MASKED, which leaked internal server detail (a DSN, a query) to
// the client.
func TestWriteErrorMasksUnknownKind(t *testing.T) {
	const secret = "dsn=postgres://user:password@db.internal/gobit"

	unknown := &coreerrors.Error{
		Kind:    coreerrors.Kind(99), // outside the enum
		Code:    "unexpected",
		Message: "the connection could not be established " + secret,
	}

	rec := httptest.NewRecorder()
	corehttp.WriteError(context.Background(), rec, unknown)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, secret) {
		t.Errorf("a Kind outside the enum leaked the body: %s", body)
	}
	if got := corehttp.StatusFor(unknown); got != http.StatusInternalServerError {
		t.Errorf("StatusFor() = %d, want 500", got)
	}
}
