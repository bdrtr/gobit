package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The test is in the INTERNAL package because the body being described
// ([deliveryDTO]) is unexported. The only way to test from outside would be to
// export the type; widening the module's surface for the sake of testing the
// document would break the very thing that is being tested.

// documentPaths produces Describe's output against the REAL route tree and
// returns the paths of the document as read back from JSON.
//
// The router has to be real as well: if the description and the route's path
// drift apart, let the failure show up HERE, not on somebody looking at
// /openapi.json in production.
func documentPaths(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint must match a route; a record that does not match never enters the document")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	paths, ok := decoded["paths"].(map[string]any)
	require.True(t, ok, "the document must have paths")

	return paths
}

// operation returns the GET operation of the given path.
func operation(t *testing.T, paths map[string]any, path string) map[string]any {
	t.Helper()

	entry, ok := paths[path].(map[string]any)
	require.True(t, ok, "%q must be in the document", path)

	get, ok := entry["get"].(map[string]any)
	require.True(t, ok, "GET must be described for %q", path)

	return get
}

// TestDescribeDescribesTheDeliveryLogEndpoint verifies that the endpoint shows
// up in the document.
//
// An endpoint that is not described does NOT show up in the client generator at
// all: because the schema is produced from the router the path and the method
// are still written, but an operation without a body tells the client "the
// shape of the response is unknown".
func TestDescribeDescribesTheDeliveryLogEndpoint(t *testing.T) {
	get := operation(t, documentPaths(t), pathAdminDeliveries)

	assert.NotEmpty(t, get["summary"])
	responses, ok := get["responses"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, responses, "200")
}

// TestDescribeDescribesEveryParameterThatIsRead verifies that the set of
// parameters in the document is the same as the set the handler REALLY reads.
//
// When the two sides drift apart two separate silent failures are born: a
// filter that is not described does not show up in the client generator at all
// (nobody can use it), while a parameter that is described but not read is a
// promise that does not work — the client sends it and gets an unfiltered list.
func TestDescribeDescribesEveryParameterThatIsRead(t *testing.T) {
	get := operation(t, documentPaths(t), pathAdminDeliveries)

	raw, ok := get["parameters"].([]any)
	require.True(t, ok, "the endpoint must describe its query parameters")

	names := make([]string, 0, len(raw))
	for _, p := range raw {
		param, castOK := p.(map[string]any)
		require.True(t, castOK)

		name, nameOK := param["name"].(string)
		require.True(t, nameOK, "the parameter name must be a string: %#v", param)
		names = append(names, name)
	}

	assert.ElementsMatch(t,
		[]string{queryReference, queryStatus, queryLimit, queryOffset}, names,
		"the parameters in the document must be exactly the ones the handler reads")
}

// TestDescribeDoesNotDescribeARecipientAddressInTheBody verifies that the
// schema does not promise a field that is not in the record.
func TestDescribeDoesNotDescribeARecipientAddressInTheBody(t *testing.T) {
	doc := openapi.New("test", "v1")
	schema := doc.SchemaOf(deliveryDTO{})

	encoded, err := json.Marshal(doc.Schemas())
	require.NoError(t, err)
	require.NotEmpty(t, schema)

	text := string(encoded)
	assert.NotContains(t, text, `"to"`, "the schema must not promise a recipient address")
	assert.Contains(t, text, `"reference"`, "the field that binds the record to the order must be described")
}

// TestTheRouteMethodIsREADONLY verifies that the module opens no write
// endpoint.
//
// A "send a notification" endpoint would make the same job doable over a second
// path and would make the idempotency key selectable from outside.
func TestTheRouteMethodIsREADONLY(t *testing.T) {
	r := chi.NewRouter()
	New(nil).Routes(r)

	methods := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		methods[method+" "+route] = true

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{http.MethodGet + " " + pathAdminDeliveries: true}, methods)
}
