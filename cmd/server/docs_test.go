package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
)

// silentModule is a module that does NOT describe itself.
//
// Because [openapi.Describer] is optional this is a valid model too; it enters
// the document with nothing but its path and its security.
type silentModule struct{ name string }

func (m silentModule) Name() string                                         { return m.name }
func (m silentModule) Register(context.Context, *container.Container) error { return nil }
func (m silentModule) Migrations() fs.FS                                    { return nil }
func (m silentModule) Routes(chi.Router)                                    {}

// describingModule is a module that describes its own endpoint.
type describingModule struct {
	silentModule
	path string
}

// cartBody is the shape of the describing module's request and response body.
type cartBody struct {
	ID      string `json:"id"`
	Address string `json:"address,omitempty"`
}

// Describe writes the module's endpoint into the document.
func (m describingModule) Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, m.path, openapi.Operation{
		Summary:     "Creates a cart",
		RequestBody: d.RequestBody(cartBody{}),
		Responses: map[string]any{
			"201": openapi.Response("The created cart", d.Item(cartBody{})),
		},
	})
}

// Routes binds the module's endpoint to the router.
func (m describingModule) Routes(r chi.Router) {
	r.Post(m.path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
}

// documentJSON builds the document and returns it as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not be enough: the
// operations are Go structs there, and the behavior under examination is
// exactly whether the fields are WRITTEN to JSON.
func documentJSON(t *testing.T, doc *openapi.Doc, r chi.Routes) map[string]any {
	t.Helper()

	document, err := doc.Build(r)
	require.NoError(t, err)

	raw, err := json.Marshal(document)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return decoded
}

// documentRouter builds a router carrying the described endpoint.
func documentRouter(t *testing.T, modules ...module.Module) chi.Router {
	t.Helper()

	r := chi.NewRouter()
	for _, mod := range modules {
		mod.Routes(r)
	}

	return r
}

// TestDescribeAPICallsTheOptionalInterface proves the type assertion works.
//
// Adding a mandatory method to the contract would break EVERY module; the price
// of an optional interface is that who describes what is not visible at compile
// time. This test pays that price: if the hook came loose nothing would break
// at compile time, the document would simply empty out in silence.
func TestDescribeAPICallsTheOptionalInterface(t *testing.T) {
	t.Parallel()

	describing := describingModule{silentModule: silentModule{name: "cart"}, path: "/store/v1/carts"}
	modules := []module.Module{silentModule{name: "region"}, describing}

	doc := describeAPI("test", "v1", modules)

	document := documentJSON(t, doc, documentRouter(t, modules...))
	assert.Empty(t, doc.UnmatchedDescriptions(), "the description must match a route")

	paths, ok := document["paths"].(map[string]any)
	require.True(t, ok)

	path, ok := paths["/store/v1/carts"].(map[string]any)
	require.True(t, ok, "the described endpoint must be in the document")

	operation, ok := path["post"].(map[string]any)
	require.True(t, ok)

	// Exactly what the finding was about: the endpoint now SAYS what it takes
	// and what it returns. Without both, a client generator produces a method
	// where everything is 'any' and the return type is 'void'.
	require.Contains(t, operation, "requestBody", "the request body must be documented")

	responses, ok := operation["responses"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, responses, "201", "the successful response must be documented")
}

// TestDescribeAPISkipsAModuleThatDoesNotDescribe proves a module without
// Describer does not break the document.
func TestDescribeAPISkipsAModuleThatDoesNotDescribe(t *testing.T) {
	t.Parallel()

	doc := describeAPI("test", "v1", []module.Module{silentModule{name: "region"}})

	_, err := doc.Build(chi.NewRouter())
	require.NoError(t, err, "an undescribed module is a valid model")
}

// TestCheckSchemaWarnsAboutAnUnmatchedDescription proves the description of a
// route whose path has changed does not vanish in silence.
//
// Startup does NOT stop (ADR 0007): a schema is documentation, not the
// product's correctness. But without the warning, the description of a deleted
// endpoint would disappear without anyone seeing it.
func TestCheckSchemaWarnsAboutAnUnmatchedDescription(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.Describe(http.MethodGet, "/store/v1/deleted", openapi.Operation{Summary: "an old endpoint"})

	checkSchema(t.Context(), doc, chi.NewRouter(), discardLogger())

	assert.Equal(t, []string{"GET /store/v1/deleted"}, doc.UnmatchedDescriptions())
}
