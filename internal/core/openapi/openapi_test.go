package openapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// loginPath is the full path of the login endpoint (the same as LoginPath in the
// auth module).
//
// The value is written by hand here because the core tests cannot import the
// modules either (Principle 2.4); the match is exercised indirectly, by the
// produced schema recognizing the login endpoint.
const loginPath = "/admin/v1/auth/login"

// buildRouter builds a router carrying the endpoints to be documented.
func buildRouter(t *testing.T) chi.Router {
	t.Helper()

	noop := func(http.ResponseWriter, *http.Request) {}

	r := chi.NewRouter()
	r.Post(loginPath, noop)
	r.Get("/admin/v1/auth/me", noop)
	r.Get("/store/v1/products", noop)

	return r
}

// buildSchema produces the document and returns it read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not do: the behavior
// under examination is exactly whether the fields are WRITTEN to JSON, and a
// test looking at the struct never sees omitempty.
func buildSchema(t *testing.T, d *openapi.Doc, r chi.Router) map[string]any {
	t.Helper()

	doc, err := d.Build(r)
	require.NoError(t, err)

	raw, err := json.Marshal(doc)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return decoded
}

// asMap returns a schema node as a map.
func asMap(t *testing.T, value any, name string) map[string]any {
	t.Helper()

	m, ok := value.(map[string]any)
	require.True(t, ok, "%s has to be an object, got: %T", name, value)

	return m
}

// operationOf pulls a single path+method operation out of the schema.
func operationOf(t *testing.T, schema map[string]any, path, method string) map[string]any {
	t.Helper()

	paths := asMap(t, schema["paths"], "paths")
	require.Contains(t, paths, path)

	pathNode := asMap(t, paths[path], path)
	require.Contains(t, pathNode, method)

	return asMap(t, pathNode[method], path+" "+method)
}

// responsesOf returns the operation's response set.
func responsesOf(t *testing.T, operation map[string]any) map[string]any {
	t.Helper()

	return asMap(t, operation["responses"], "responses")
}

// TestTheLoginEndpointsUnprotectedMarkIsWrittenToTheSchema verifies that the
// empty "security" array REALLY is written to JSON.
//
// With omitempty an empty array is never written, and an operation without the
// field counts as "unspecified" in OpenAPI and inherits the root-level security.
// That is, the schema would say the endpoint handing out the token demands one,
// and client generators would produce a login method that can never be called.
func TestTheLoginEndpointsUnprotectedMarkIsWrittenToTheSchema(t *testing.T) {
	t.Parallel()

	schema := buildSchema(t, openapi.New("test", "v1"), buildRouter(t))
	operation := operationOf(t, schema, loginPath, "post")

	security, written := operation["security"]
	require.True(t, written,
		"the \"security\" field has to be written; without it the endpoint is taken for protected")
	assert.Equal(t, []any{}, security, "an empty array = this endpoint is explicitly unprotected")
}

// TestProtectedEndpointsKeepTheirSecurity verifies that the schema of the
// protected endpoints did not change.
func TestProtectedEndpointsKeepTheirSecurity(t *testing.T) {
	t.Parallel()

	schema := buildSchema(t, openapi.New("test", "v1"), buildRouter(t))

	assert.Equal(t,
		[]any{map[string]any{"bearerAuth": []any{}}},
		operationOf(t, schema, "/admin/v1/auth/me", "get")["security"])
	assert.Equal(t,
		[]any{map[string]any{"publishableKey": []any{}}},
		operationOf(t, schema, "/store/v1/products", "get")["security"])
}

// TestTheLoginEndpointDocumentsIts401 verifies that the login endpoint writes
// its 401 into the schema.
//
// The endpoint is unprotected but its job is to verify credentials: a wrong
// password returns a 401. Undocumented, a client generator produces a method
// that never handles a login failure and a wrong password looks like an
// unexpected fault.
func TestTheLoginEndpointDocumentsIts401(t *testing.T) {
	t.Parallel()

	schema := buildSchema(t, openapi.New("test", "v1"), buildRouter(t))
	responses := responsesOf(t, operationOf(t, schema, loginPath, "post"))

	require.Contains(t, responses, "401", "the login endpoint returns a 401 on a wrong password")
	assert.Contains(t,
		asMap(t, responses["401"], "401")["description"], "password",
		"at login a 401 means \"the credentials are wrong\", not \"the token is missing\"")

	// A 403 is only meaningful at endpoints with an authorization step; at login
	// there is no identity yet.
	assert.NotContains(t, responses, "403")
}

// TestProtectedAdminEndpointsDocument403 verifies that the authorization
// response stays on the protected endpoints.
func TestProtectedAdminEndpointsDocument403(t *testing.T) {
	t.Parallel()

	schema := buildSchema(t, openapi.New("test", "v1"), buildRouter(t))
	responses := responsesOf(t, operationOf(t, schema, "/admin/v1/auth/me", "get"))

	assert.Contains(t, responses, "401")
	assert.Contains(t, responses, "403")
}

// TestAHandGivenEmptySecurityIsKept verifies that an endpoint marked "explicitly
// unprotected" with [openapi.Doc.Describe] is not overwritten.
//
// Were it overwritten, the only endpoint known to be unprotected would be the
// login one and the webhook endpoints plugins bring would be described wrongly
// in the schema.
func TestAHandGivenEmptySecurityIsKept(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.Describe(http.MethodGet, "/store/v1/products", openapi.Operation{
		Security: []map[string][]string{},
	})

	schema := buildSchema(t, doc, buildRouter(t))

	assert.Equal(t, []any{}, operationOf(t, schema, "/store/v1/products", "get")["security"])
}
