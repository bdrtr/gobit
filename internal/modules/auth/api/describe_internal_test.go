package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The test is in the INTERNAL package because the bodies being described
// ([loginRequest], [userDTO] …) are unexported. The only way to test from the
// outside would have been to export the types; widening the module's surface
// for the sake of testing the documentation would have broken the very thing
// under test. The package's OTHER tests (scopes, logout) are in the api_test
// package because they test the exported surface; the two can stand side by
// side.

// document produces Describe's output against the REAL route tree and returns
// it as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not have been enough:
// the operations are Go structs there and the behavior under examination is
// exactly whether the fields get written into JSON. The router has to be real
// too — if the description and the route's path drift apart, let the fault show
// up HERE and not in somebody looking at /openapi.json in production.
func document(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; an unmatched record never enters the document")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	componentSchemas, ok := decoded["components"].(map[string]any)
	require.True(t, ok)

	components, ok = componentSchemas["schemas"].(map[string]any)
	require.True(t, ok)

	paths, ok = decoded["paths"].(map[string]any)
	require.True(t, ok)

	return paths, components
}

// operation returns a single path+method operation from the document.
func operation(t *testing.T, paths map[string]any, method, path string) map[string]any {
	t.Helper()

	pathOperations, ok := paths[path].(map[string]any)
	require.True(t, ok, "%s has to be in the document", path)

	op, ok := pathOperations[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "%s %s has to be in the document", method, path)

	return op
}

// resolveSchema resolves "$ref" references to the component in the document.
func resolveSchema(t *testing.T, components, schema map[string]any) map[string]any {
	t.Helper()

	ref, referenced := schema[schemaRef].(string)
	if !referenced {
		return schema
	}

	target, ok := components[strings.TrimPrefix(ref, refPrefix)].(map[string]any)
	require.True(t, ok, "the %q component has to be registered", ref)

	return target
}

// bodySchemaOf extracts the JSON schema from a response or request body
// definition.
func bodySchemaOf(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	schema := subMap(definition, bodyContent, bodyMediaType, bodySchema)
	require.NotNil(t, schema,
		"the body definition has to carry an application/json schema: %#v", definition)

	return schema
}

// fields returns the schema's "properties" keys.
func fields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolveSchema(t, components, schema)[schemaProperties].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return keysOf(properties)
}

// requiredFields returns the schema's "required" list.
func requiredFields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	raw, _ := resolveSchema(t, components, schema)["required"].([]any)

	names := make([]string, 0, len(raw))

	for _, name := range raw {
		text, ok := name.(string)
		require.True(t, ok)

		names = append(names, text)
	}

	return names
}

// keysOf returns a map's keys.
func keysOf[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// jsonKeys encodes the value with encoding/json and returns its keys.
//
// This is the other end of the comparison: the schema has to describe what is
// REALLY on the wire, and the only thing that knows that is encoding/json
// itself.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return keysOf(decoded)
}

// zeroValue returns the zero value of the given sample's type.
//
// The keys written to JSON at the zero value are exactly "the ones always
// written", that is, the schema's "required" set. Rather than hand-writing the
// sample a second time it is derived from the type: had a field been forgotten
// between the two samples, the test would have fallen over for the wrong
// reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of a single described admin endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see admin.go).
	status string
	// request is a sample carrying ALL the fields of the request body; if nil
	// the endpoint READS NO body.
	request any
	// response is a sample carrying all the fields of the RECORD in the
	// successful response; if nil the response has no body (204).
	response any
	// list reports that the response comes back with the list envelope.
	list bool
	// query are the query parameters the handler REALLY reads.
	query []string
}

// key returns the operation's "METHOD path" identifier.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// pageQuery are the shared parameters of the paged list endpoints.
var pageQuery = []string{"limit", "offset"}

// adminEndpoints are the expectations of the described admin endpoints.
//
// The samples are FULL: every field carrying omitempty gets a non-zero value,
// because the comparison has the form "the schema's properties set = the set of
// encoded keys" and an empty sample would never write the omitempty fields.
func adminEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: LoginPath, status: "200",
			request: loginRequest{}, response: loginResponse{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/auth/me", status: "200",
			response: fullPrincipal(),
		},
		{
			method: http.MethodPost, path: "/admin/v1/auth/logout", status: "200",
			response: logoutResponse{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/users", status: "201",
			request: createUserRequest{}, response: fullUser(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/users", status: "200",
			response: fullUser(), list: true,
			query: append(pageQuery, "email", "scope"),
		},
		{
			method: http.MethodGet, path: "/admin/v1/users/{id}", status: "200",
			response: fullUser(),
		},
		{
			method: http.MethodPut, path: "/admin/v1/users/{id}", status: "200",
			request: updateUserRequest{}, response: fullUser(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/users/{id}", status: "204",
		},
		{
			method: http.MethodPost, path: "/admin/v1/users/{id}/password", status: "204",
			request: setPasswordRequest{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/api-keys", status: "201",
			request:  createAPIKeyRequest{},
			response: createAPIKeyResponse{APIKey: fullAPIKey(), Key: "sk_plain"},
		},
		{
			method: http.MethodGet, path: "/admin/v1/api-keys", status: "200",
			response: fullAPIKey(), list: true,
			query: append(pageQuery, "type", "revoked"),
		},
		{
			method: http.MethodGet, path: "/admin/v1/api-keys/{id}", status: "200",
			response: fullAPIKey(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/api-keys/{id}", status: "204",
		},
		{
			method: http.MethodPost, path: "/admin/v1/api-keys/{id}/revoke", status: "200",
			response: fullAPIKey(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/api-keys/{id}/sales-channels",
			status: "200", response: fullSalesChannel(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/api-keys/{id}/sales-channels",
			status: "200", request: linkChannelRequest{}, response: fullSalesChannel(), list: true,
		},
		{
			method: http.MethodDelete,
			path:   "/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}",
			status: "204",
		},
		{
			method: http.MethodPost, path: "/admin/v1/sales-channels", status: "201",
			request: salesChannelRequest{}, response: fullSalesChannel(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/sales-channels", status: "200",
			response: fullSalesChannel(), list: true,
			query: append(pageQuery, "name", "is_disabled"),
		},
		{
			method: http.MethodGet, path: "/admin/v1/sales-channels/{id}", status: "200",
			response: fullSalesChannel(),
		},
		{
			method: http.MethodPut, path: "/admin/v1/sales-channels/{id}", status: "200",
			request: updateSalesChannelRequest{}, response: fullSalesChannel(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/sales-channels/{id}", status: "204",
		},
	}
}

// fullPrincipal produces an identity record whose omitempty field is written
// too.
func fullPrincipal() principalResponse {
	return principalResponse{SalesChannelIDs: []string{"sc_1"}}
}

// fullUser produces a user record whose omitempty field is written too.
func fullUser() userDTO {
	return userDTO{Metadata: map[string]any{"k": "v"}}
}

// fullAPIKey produces a key record whose omitempty fields are written too.
func fullAPIKey() apiKeyDTO {
	moment := time.Now().UTC()

	return apiKeyDTO{
		LastUsedAt: &moment,
		RevokedAt:  &moment,
		RevokedBy:  "usr_1",
	}
}

// fullSalesChannel produces a sales channel record whose omitempty field is
// written too.
func fullSalesChannel() salesChannelDTO {
	return salesChannelDTO{Metadata: map[string]any{"k": "v"}}
}

// TestAdminEndpointsDescribeTheirBodies verifies that every admin endpoint says
// what it TAKES and what it RETURNS.
//
// This is exactly what the finding amounts to: a bodyless schema tells the
// client "this endpoint exists and can fail like this" but does not say what to
// send; and the client generator produces a method with no body whose return
// type is 'void' — for POST /admin/v1/users that means no user CAN BE CREATED
// with that client.
//
// The field sets are compared against the DTO's encoding/json output and not
// against a hand-written list: a hand-written list falls short the day a field
// is added to the DTO, and the test would not see it.
func TestAdminEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, ep := range adminEndpoints() {
		t.Run(ep.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, ep.method, ep.path)
			assert.NotEmpty(t, op["summary"],
				"an operation without a summary becomes a nameless method in the client")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, ep.request != nil, hasBody,
				"an endpoint that READS a body has to have a requestBody, one that does not must not")

			if ep.request != nil {
				assert.Equal(t, true, requestDefinition["required"],
					"the body of a write endpoint is mandatory")
				assert.ElementsMatch(t, jsonKeys(t, ep.request),
					fields(t, components, bodySchemaOf(t, requestDefinition)),
					"the fields of the request body have to overlap with the DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[ep.status].(map[string]any)
			require.True(t, ok,
				"the code the handler REALLY writes has to be documented: %s", ep.status)
			assert.NotEmpty(t, definition["description"], "the response has to carry a description")

			if ep.response == nil {
				assert.NotContains(t, definition, bodyContent,
					"a 204 has no body; the schema must not promise one")

				return
			}

			envelope := bodySchemaOf(t, definition)
			if ep.list {
				assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
					fields(t, components, envelope),
					"the list envelope is the shape from plan Section 8")
			} else {
				assert.ElementsMatch(t, []string{"data"}, fields(t, components, envelope),
					"single responses come back with a {\"data\": …} envelope")
			}

			record := envelopeRecord(t, components, envelope, ep.list)
			assert.ElementsMatch(t, jsonKeys(t, ep.response), fields(t, components, record),
				"the fields of the response record have to overlap with the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(ep.response)),
				requiredFields(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// envelopeRecord returns the RECORD schema the envelope carries.
//
// In a list envelope the record is INSIDE "data"; stopping at the envelope
// would have meant counting a list whose body is unknown as described.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	properties, ok := resolveSchema(t, components, envelope)[schemaProperties].(map[string]any)
	require.True(t, ok)

	data, ok := properties["data"].(map[string]any)
	require.True(t, ok)

	if !list {
		return data
	}

	item, ok := data["items"].(map[string]any)
	require.True(t, ok, "the list envelope has to have an item schema")

	return item
}

// undescribedEndpoints are the endpoints deliberately left undescribed.
//
// It is EMPTY and has to stay that way. POST /admin/v1/sales-channels was here
// once: its body asked for the "SalesChannelRequest" component and a type of
// the product module asked for the SAME name; when two different types ask for
// the same name the WHOLE document becomes impossible to produce. The clash was
// resolved by naming the type on the product side after what it really is
// (linkSalesChannelRequest).
//
// The list is still standing because it is a SAFETY NET: if an endpoint is
// deliberately left undescribed in the future, its justification is forced to
// be written down here. An unwritten gap is an unknown gap.
var undescribedEndpoints = []string{}

// TestEveryAdminEndpointIsDescribed verifies that no admin endpoint is left
// undescribed.
//
// When a new endpoint is added and not described, this test falls over. Without
// the warning the fault would be SILENT: the endpoint shows up in the document
// with its path and its security, it just has no body — that is, the schema
// says "it exists but what it takes is unknown" and nobody notices.
func TestEveryAdminEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	var described, undescribed []string

	for path, operations := range paths {
		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a method map")

		for method, raw := range operationMap {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			opKey := strings.ToUpper(method) + " " + path

			if op["summary"] == nil {
				// An undescribed endpoint is a VALID model but it MUST NOT have
				// a body either: an operation with no summary but with a body
				// would mean a half-finished description.
				assert.NotContains(t, op, "requestBody", "%s was not described", opKey)

				undescribed = append(undescribed, opKey)

				continue
			}

			described = append(described, opKey)
		}
	}

	expected := make([]string, 0, len(adminEndpoints()))
	for _, ep := range adminEndpoints() {
		expected = append(expected, ep.key())
	}

	assert.ElementsMatch(t, expected, described,
		"an admin endpoint that is not in the table means an untested one")
	assert.ElementsMatch(t, undescribedEndpoints, undescribed,
		"the set of undescribed endpoints has to be WRITTEN DOWN; a silent gap is a gap nobody knows about")
}

// TestAdminEndpointsDescribeOnlyTheQueryParametersTheyRead verifies that the
// schema announces no parameter that is never read.
//
// Putting a parameter that is not read into the schema is promising the client
// a feature that DOES NOT WORK: the generator puts an argument on the method,
// the caller fills it in, and the server silently ignores it.
func TestAdminEndpointsDescribeOnlyTheQueryParametersTheyRead(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, ep := range adminEndpoints() {
		op := operation(t, paths, ep.method, ep.path)
		assert.ElementsMatch(t, ep.query, parameterNames(t, op, "query"),
			"the parameters of %s have to be the same as the ones the handler reads", ep.key())
	}
}

// parameterNames returns the operation's parameter names at the given location.
func parameterNames(t *testing.T, op map[string]any, location string) []string {
	t.Helper()

	params, _ := op["parameters"].([]any)

	names := make([]string, 0, len(params))

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["in"] != location {
			continue
		}

		name, ok := p["name"].(string)
		require.True(t, ok)

		names = append(names, name)
	}

	return names
}

// TestPasswordFieldsAreDeclaredMasked verifies that the password appears in the
// request schema AS a password.
//
// An unmarked password is an ordinary string that looks just like an email: the
// client generator makes it a plaintext field, the schema viewer prints it in
// the clear, and the tool producing sample requests records the value. The test
// also locks down [passwordBody] writing into the component — if the core one
// day returns a deep copy the mark does not disappear silently, it falls over
// here.
func TestPasswordFieldsAreDeclaredMasked(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	passwordEndpoints := []struct{ method, path string }{
		{http.MethodPost, LoginPath},
		{http.MethodPost, "/admin/v1/users"},
		{http.MethodPost, "/admin/v1/users/{id}/password"},
	}

	for _, ep := range passwordEndpoints {
		op := operation(t, paths, ep.method, ep.path)

		body, ok := op["requestBody"].(map[string]any)
		require.True(t, ok, "%s %s has to take a body", ep.method, ep.path)

		properties, ok := resolveSchema(t, components,
			bodySchemaOf(t, body))[schemaProperties].(map[string]any)
		require.True(t, ok)

		password, ok := properties[fieldPassword].(map[string]any)
		require.True(t, ok, "%s %s has to carry a password field", ep.method, ep.path)

		assert.Equal(t, typeString, password[schemaType], "a password is a string on the wire")
		assert.Equal(t, formatPassword, password[schemaFormat],
			"the password field has to be marked with format: %q", formatPassword)
	}
}

// TestResponsesCarryNoPassword verifies that NO successful response holds a
// password field.
//
// The assertion looks not at the response DTOs as they are today but at the
// WHOLE SCHEMA: every component reachable from the response is scanned. Were a
// response body one day to reuse a request type (or a password field to be
// added to a DTO), the leak would become visible in the document — and a field
// visible in the document is a field the client generator will try to read.
func TestResponsesCarryNoPassword(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, ep := range adminEndpoints() {
		if ep.response == nil {
			continue
		}

		op := operation(t, paths, ep.method, ep.path)

		responses, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		definition, ok := responses[ep.status].(map[string]any)
		require.True(t, ok)

		reached := reachableFields(t, components, bodySchemaOf(t, definition), map[string]struct{}{})
		assert.NotContains(t, reached, fieldPassword,
			"the response of %s must not carry a password", ep.key())
	}
}

// reachableFields collects ALL the property names reachable from the schema.
//
// seen prevents descending into the same component a second time; schemas can
// refer to themselves and a cycle would make the test loop forever.
func reachableFields(t *testing.T, components, schema map[string]any,
	seen map[string]struct{},
) []string {
	t.Helper()

	if ref, referenced := schema[schemaRef].(string); referenced {
		if _, repeat := seen[ref]; repeat {
			return nil
		}

		seen[ref] = struct{}{}
	}

	resolved := resolveSchema(t, components, schema)

	var names []string

	if properties, ok := resolved[schemaProperties].(map[string]any); ok {
		for name, raw := range properties {
			names = append(names, name)

			if child, object := raw.(map[string]any); object {
				names = append(names, reachableFields(t, components, child, seen)...)
			}
		}
	}

	if item, ok := resolved["items"].(map[string]any); ok {
		names = append(names, reachableFields(t, components, item, seen)...)
	}

	return names
}

// TestSchemaSaysThePlaintextKeyIsReturnedOnce verifies that the lifetime of the
// plaintext key is written down in the schema.
//
// The schema describes the EXISTENCE of the field but cannot describe that it
// is one-shot: "key" is an ordinary string and the client believes it can read
// it on every call. The only place for that information is the description;
// without it the client developer would not store the key and would lose the
// value.
func TestSchemaSaysThePlaintextKeyIsReturnedOnce(t *testing.T) {
	t.Parallel()

	paths, components := document(t)
	op := operation(t, paths, http.MethodPost, "/admin/v1/api-keys")

	summary, _ := op["summary"].(string)
	assert.Contains(t, summary, "ONCE", "the summary has to say the key is returned once")

	description, _ := op["description"].(string)
	assert.Contains(t, description, "can never again be read from any endpoint",
		"the description has to say the plaintext key cannot be read back")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["201"].(map[string]any)
	require.True(t, ok)

	responseDescription, _ := definition["description"].(string)
	assert.Contains(t, responseDescription, "never returned again",
		"the response description has to say the plaintext is one-shot")

	// The plaintext is ONLY in the creation response; the record of the read
	// endpoints holds the masked representation.
	record := envelopeRecord(t, components, bodySchemaOf(t, definition), false)
	assert.ElementsMatch(t, []string{"api_key", "key"}, fields(t, components, record))

	readOp := operation(t, paths, http.MethodGet, "/admin/v1/api-keys/{id}")

	readResponses, ok := readOp["responses"].(map[string]any)
	require.True(t, ok)

	readDefinition, ok := readResponses["200"].(map[string]any)
	require.True(t, ok)

	readFields := fields(t, components,
		envelopeRecord(t, components, bodySchemaOf(t, readDefinition), false))
	assert.NotContains(t, readFields, "key", "the read endpoint must not promise a plaintext key")
	assert.Contains(t, readFields, "redacted")
}

// TestLoginEndpointStaysUnprotectedInTheSchema verifies that the endpoint
// handing out the token does not ask for a token.
//
// The distinction is subtle and its cost is large: had the field NOT BEEN
// WRITTEN AT ALL the operation would count as "unspecified" and would INHERIT
// the root-level default security; and the client generator would produce a
// method that asks for a token in order to log in, that is, one that can never
// be called. The core makes the decision ([openapi.Doc] recognizes the login
// path); the claim here is that the description DOES NOT OVERRIDE it.
func TestLoginEndpointStaysUnprotectedInTheSchema(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	login := operation(t, paths, http.MethodPost, LoginPath)

	security, written := login["security"]
	require.True(t, written,
		"the security field of the login endpoint has to be WRITTEN; an unwritten field inherits the root default")
	require.NotNil(t, security, "security cannot be null; null amounts to the same as 'unspecified'")

	list, ok := security.([]any)
	require.True(t, ok, "security has to be an array, %T found", security)
	assert.Empty(t, list, "an empty array means 'this endpoint is explicitly unprotected'")

	// For the empty array to mean anything, a full one has to be seen as well.
	protected := operation(t, paths, http.MethodGet, "/admin/v1/users")
	assert.Equal(t, []any{map[string]any{"bearerAuth": []any{}}}, protected["security"],
		"the other admin endpoints have to ask for a session token")
}
