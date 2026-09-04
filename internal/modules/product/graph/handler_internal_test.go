package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file tests from INSIDE the package and there is a single reason for
// that: the claims here cannot be observed from OUTSIDE the handler. The
// sentence "a rejected document must not enter the cache" leaves no trace in
// the response — even if the query is sent twice the second response looks the
// same — and the streaming behavior of the response wrapper is never triggered
// by today's transport, which does a single Write. Not testing what cannot be
// tested from outside is better than bending production to test it; but here
// production is reachable without being bent.

// silentStorefront is the storefront that returns no data at all.
//
// The cache claims have no data: what is measured is not the content of the
// response but whether the document was stored.
type silentStorefront struct{}

// ListStoreProducts returns an empty list.
//
// The count is filled in as ZERO if it was asked for: the "count: Int!" in the
// schema does not accept nil and this file's documents are of the form
// "{ products { count } }" — leaving it nil would fail the cache claim with an
// unrelated field error.
func (silentStorefront) ListStoreProducts(
	_ context.Context,
	opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	if opts.SkipCount {
		return service.ListResult[service.StoreProduct]{}, nil
	}

	zero := 0

	return service.ListResult[service.StoreProduct]{Count: &zero}, nil
}

// GetStoreProduct returns an empty product.
func (silentStorefront) GetStoreProduct(
	_ context.Context,
	_ string,
	_ []string,
) (service.StoreProduct, error) {
	return service.StoreProduct{}, nil
}

// postToServer POSTs the document to the gqlgen server and returns the response
// body.
func postToServer(t *testing.T, srv http.Handler, document string) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{"query": document})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	return rec.Body.String()
}

// TestAcceptedDocumentIsCached verifies that the cache REALLY works.
//
// The order deliberately puts this test first: the two tests below say "the
// document must not be in the cache" and a broken cache that stores nothing at
// all would pass them too. Without the positive claim the negative claims
// measure nothing.
func TestAcceptedDocumentIsCached(t *testing.T) {
	t.Parallel()

	srv, cache := newServer(silentStorefront{}, Options{})

	const document = `{ products { count } }`

	response := postToServer(t, srv, document)
	require.NotContains(t, response, `"errors"`, "a legitimate document must pass: %s", response)

	_, stored := cache.Get(t.Context(), document)
	assert.True(t, stored, "a document that passes the limits must enter the cache")
}

// TestRejectedDocumentDoesNotEnterTheCache verifies that a document caught by a
// limit takes up no room.
//
// gqlgen adds the document to the cache IMMEDIATELY AFTER parsing and
// validating it; the limit extensions run AFTER that. That is, before the fix a
// document that never reached the service was taking up room in the cache too.
// Measured: 100 rejected documents of 65 KB left 171.8 MiB of permanent heap
// after runtime.GC — 26 times the 6.5 MB upload.
//
// The cost was not only memory: because the LRU filled up, the storefront's
// REAL documents were being evicted from the cache, that is, an attacker could
// have everyone's query reparsed with a single quota.
func TestRejectedDocumentDoesNotEnterTheCache(t *testing.T) {
	t.Parallel()

	srv, cache := newServer(silentStorefront{}, Options{})

	document := `{ products(limit: 100) { items {` + aliasedSelection(489, "description") + `} } }`

	response := postToServer(t, srv, document)
	require.Contains(t, response, "FIELD_REPETITION_LIMIT_EXCEEDED",
		"the document should have been rejected: %s", response)

	_, stored := cache.Get(t.Context(), document)
	assert.False(t, stored, "a document caught by a limit must not take up room in the cache")
}

// TestOversizedDocumentIsNotCached verifies that a document which PASSES the
// limits but is too large is not stored.
//
// The two rules do not replace each other: the admission gate asks "did it
// pass", the byte limit asks "is it worth storing". Without the second one, a
// hundred documents of 60 KB that pass the limits comfortably would still
// bloat the cache — without any of them being rejected.
//
// The document below is flawless: a single field, a single argument; its size
// comes only from the length of the argument.
func TestOversizedDocumentIsNotCached(t *testing.T) {
	t.Parallel()

	srv, cache := newServer(silentStorefront{}, Options{})

	document := `{ product(handle: "` + strings.Repeat("x", maxCachedDocumentBytes) + `") { id } }`
	require.Less(t, len(document), maxQueryBytes,
		"the document must be small enough to pass the body gate")

	response := postToServer(t, srv, document)
	require.NotContains(t, response, `"errors"`,
		"the document should have passed the limits: %s", response)

	_, stored := cache.Get(t.Context(), document)
	assert.False(t, stored, "a document above the byte limit must not be stored")
}

// aliasedSelection builds the list that selects the same field n times with
// aliases.
//
// It is a copy of its twin in limits_test.go and has to be: that file tests
// from OUTSIDE the package (graph_test) and is invisible from here. The
// alternative was putting a helper only the test uses into the production
// package.
func aliasedSelection(n int, field string) string {
	var selections strings.Builder

	for i := range n {
		selections.WriteString(" a" + strconv.Itoa(i) + ": " + field)
	}

	return selections.String()
}

// TestResponseCounterWritesAFullEnvelopeInOnePiece exercises what happens when
// the limit is hit while no byte has gone out yet.
//
// That is today's transport's behavior: gqlgen encodes the response into memory
// first and writes it with a single Write, that is, the wrapper can reject the
// body without ever sending it to the client. In that case a COMPLETE error
// envelope is written instead of a partial document — the client gets a
// response that states the reason, not a broken body.
func TestResponseCounterWritesAFullEnvelopeInOnePiece(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	counter := &responseCounter{ResponseWriter: rec, limit: 64, remaining: 64}

	n, err := counter.Write(bytes.Repeat([]byte("v"), 4096))

	assert.Zero(t, n, "not a single byte of the exceeding body may be written")
	require.ErrorIs(t, err, errResponseTooLarge)
	assert.False(t, counter.aborted, "if no byte has gone out the connection must not be dropped")

	body := rec.Body.String()
	assert.NotContains(t, body, "vvv", "a truncated body must not leak")

	var envelope struct {
		Errors []struct {
			Message    string         `json:"message"`
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &envelope),
		"the envelope must be decodable: %s", body)
	require.NotEmpty(t, envelope.Errors)
	assert.Equal(t, codeResponseExceeded, envelope.Errors[0].Extensions["code"])
}

// TestResponseCounterAbortsTheConnectionOnAPartialBody exercises what happens
// when the limit is hit while part of the body has already gone out.
//
// This branch is UNREACHABLE today: the single transport (POST) writes the
// response with one Write. Even so the decision has been made here and has a
// test, because the http.ResponseWriter contract allows partial writes and the
// day a streaming transport (SSE, @defer) is added to the endpoint these lines
// will silently come into play.
//
// The decision: half a JSON IS NOT SENT. A truncated body either drops the
// client into a parse error whose reason it cannot know, or — worse — is
// mistaken for a short result. Dropping the connection is honest: the client
// sees a transport error and that is exactly what happened. The panic value is
// http.ErrAbortHandler because that is the stdlib's "drop this request
// silently" contract; the core's Recoverer re-panics it as well (see corehttp).
func TestResponseCounterAbortsTheConnectionOnAPartialBody(t *testing.T) {
	t.Parallel()

	streaming := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"data":`))
		assert.NoError(t, err, "the first piece is under the limit, it must pass")

		_, err = w.Write(bytes.Repeat([]byte("v"), 4096))
		assert.ErrorIs(t, err, errResponseTooLarge, "the exceeding piece must be rejected")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, Path, http.NoBody)

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		responseLimit(streaming, 64).ServeHTTP(rec, req)
	})

	assert.Equal(t, `{"data":`, rec.Body.String(),
		"neither a truncated body nor a second envelope may be written over the piece that went out")
}

// TestResponseCounterLetsThroughWhatIsUnderTheLimit verifies that the wrapper
// does not touch an ordinary response.
//
// A wrapper that rejects every write would pass the two tests above as well.
func TestResponseCounterLetsThroughWhatIsUnderTheLimit(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	counter := &responseCounter{ResponseWriter: rec, limit: 64, remaining: 64}

	n, err := counter.Write([]byte(`{"data":{"products":{"count":0}}}`))

	require.NoError(t, err)
	assert.Equal(t, 33, n)
	assert.Equal(t, `{"data":{"products":{"count":0}}}`, rec.Body.String())
}
