package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// countingRouter is a router wrapper that counts how many times it is WALKED.
//
// The cache is invisible from outside: producing the same document twice and
// producing it once and keeping it give the SAME body. The only thing that can
// be told apart is how many times the tree was walked — [openapi.Doc.Handler]
// walks it once per request for the identity, and once more while BUILDING.
type countingRouter struct {
	chi.Router
	walks *int
}

// Routes increments the counter and returns the wrapped router's routes.
func (s countingRouter) Routes() []chi.Route {
	*s.walks++

	return s.Router.Routes()
}

// request calls the handler and returns the status code and the body.
func request(t *testing.T, h http.HandlerFunc) (code int, body string) {
	t.Helper()

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody))

	return rec.Code, rec.Body.String()
}

// TestTheDocumentIsNotRebuiltWhileTheInputIsUnchanged verifies that the cache
// really holds.
//
// The endpoint can be mounted outside the identity and quota gates; walking the
// whole route tree and building and encoding the document on every request
// would turn a small GET into the most expensive work in the process.
func TestTheDocumentIsNotRebuiltWhileTheInputIsUnchanged(t *testing.T) {
	t.Parallel()

	walks := 0
	r := countingRouter{Router: buildRouter(t), walks: &walks}
	h := openapi.New("test", "v1").Handler(r)

	code, body := request(t, h)
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	require.Equal(t, 2, walks, "the first request walks the tree for the identity and for the build")

	code, second := request(t, h)
	require.Equal(t, http.StatusOK, code)

	assert.Equal(t, 3, walks,
		"the second request has to walk the tree for the IDENTITY only; if the document is "+
			"rebuilt the cache is gaining nothing")
	assert.JSONEq(t, body, second, "the body from the cache has to match the first one")
}

// TestARouteAddedLaterEntersTheDocument verifies that the cache rests on the
// document's inputs rather than on an ASSUMPTION.
//
// When the tree freezes is not something the core can know: modules bind their
// routes during bootstrap and plugins after that, and there is no guarantee
// about the order the handler was registered in. A cache saying "build once at
// startup" would SILENTLY drop every endpoint bound after the handler — and not
// doing that is the document's whole reason to exist.
func TestARouteAddedLaterEntersTheDocument(t *testing.T) {
	t.Parallel()

	r := buildRouter(t)
	h := openapi.New("test", "v1").Handler(r)

	code, first := request(t, h)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, first, "/store/v1/later")

	r.Get("/store/v1/later", func(http.ResponseWriter, *http.Request) {})

	code, second := request(t, h)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, second, "/store/v1/later",
		"an endpoint added to the tree later has to appear in the document")
}

// TestAnEndpointDescribedLaterEntersTheDocument verifies that description
// records invalidate the cache too.
//
// The document can change WITHOUT the route tree changing: the body schema and
// the summary are carried by [openapi.Doc.Describe]. A cache watching the tree
// alone would go on showing an endpoint described after setup as bodyless.
func TestAnEndpointDescribedLaterEntersTheDocument(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	h := doc.Handler(buildRouter(t))

	code, first := request(t, h)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, first, "lists the products")

	doc.Describe(http.MethodGet, "/store/v1/products", openapi.Operation{
		Summary: "lists the products",
	})

	code, second := request(t, h)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, second, "lists the products",
		"an endpoint described later has to appear in the document")
}

// TestAnUnbuildableDocumentComesBackInTheCoreErrorEnvelope verifies that the
// endpoint's error goes through the core's policy.
//
// To make the build fail, a type clashing with the core's shared component
// ([Error], schema_test.go) is registered; the document cannot be built and the
// endpoint returns an error.
//
// The endpoint is a JSON API; a plain-text error body means the client cannot
// parse the error. More importantly the TEXT of a build error carries the
// PACKAGE PATHS of the clashing types and this endpoint can be called without
// an identity: writing the internals as they are describes the server's source
// tree to the outside.
func TestAnUnbuildableDocumentComesBackInTheCoreErrorEnvelope(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.SchemaOf(Error{})

	code, body := request(t, doc.Handler(buildRouter(t)))
	require.Equal(t, http.StatusInternalServerError, code, "body: %s", body)

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &envelope),
		"the error body has to be the shared JSON envelope; got: %s", body)

	assert.Equal(t, "openapi_document_unavailable", envelope.Error.Code,
		"the client has to recognize the error by its CODE")
	assert.NotContains(t, body, "internal/core/openapi",
		"the package path of the clashing types MUST NOT LEAK to the client")
	assert.NotContains(t, body, "belongs to the core",
		"the raw text of the build error MUST NOT LEAK to the client")
}

// TestTheDocumentResponseCarriesTheJSONHeader verifies the response's content type.
//
// The body is written without going through the core's writer (the document is
// already encoded); without an assertion exercising that the header is written
// too, that path could silently answer with no Content-Type.
func TestTheDocumentResponseCarriesTheJSONHeader(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	openapi.New("test", "v1").Handler(buildRouter(t))(
		rec, httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody))

	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	assert.Equal(t, openapi.Version, doc["openapi"])
}
