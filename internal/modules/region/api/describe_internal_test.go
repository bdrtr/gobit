package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// The test is in the INTERNAL package because the bodies described
// ([createRegionRequest], [regionDTO] …) are unexported. The only way to test
// them from outside would be to export the types; widening the module's surface
// for the sake of testing the document would break the thing under test.

// document builds Describe's output against the REAL route tree and returns it
// as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not be enough: the
// operations are Go structs there, and the behavior under examination is
// exactly whether the fields are written to JSON. The router has to be real too
// — if a description and the route's path drift apart, the failure should show
// HERE, not to somebody looking at /openapi.json in production.
func document(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	// The namespace the composition root applies to this module (ADR 0036).
	// Without it the test would build a document whose component names differ
	// from the shipped one by exactly the prefix that IS the published
	// contract. The name is a literal because an api package cannot import
	// the module package that holds the constant — that import goes the other
	// way. What keeps the literal honest is the audit in internal/app, which
	// reads the REAL registry.
	doc.ForModule("region", func() { Describe(doc) })

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; an unmatched entry never enters the document")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	var ok bool

	components, ok = decoded["components"].(map[string]any)["schemas"].(map[string]any)
	require.True(t, ok)

	paths, ok = decoded["paths"].(map[string]any)
	require.True(t, ok)

	return paths, components
}

// operation returns one path+method operation from the document.
func operation(t *testing.T, paths map[string]any, method, path string) map[string]any {
	t.Helper()

	pathOperations, ok := paths[path].(map[string]any)
	require.True(t, ok, "%s has to be in the document", path)

	op, ok := pathOperations[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "%s %s has to be in the document", method, path)

	return op
}

// resolveSchema resolves a "$ref" reference to the component in the document.
func resolveSchema(t *testing.T, components, schema map[string]any) map[string]any {
	t.Helper()

	ref, isRef := schema["$ref"].(string)
	if !isRef {
		return schema
	}

	target, ok := components[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	require.True(t, ok, "the %q component has to be registered", ref)

	return target
}

// bodySchema extracts the JSON schema from a response or request body
// definition.
func bodySchema(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	content, ok := definition["content"].(map[string]any)
	require.True(t, ok, "a body definition has to have content: %#v", definition)

	jsonBody, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body has to be application/json")

	schema, ok := jsonBody["schema"].(map[string]any)
	require.True(t, ok, "the body has to have a schema")

	return schema
}

// fields returns the keys of a schema's "properties".
func fields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return keys(properties)
}

// requiredFields returns a schema's "required" list.
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

// keys returns the keys of a map.
func keys[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// jsonKeys encodes a value with encoding/json and returns its keys.
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

	return keys(decoded)
}

// zeroValue returns the zero value of the given example's type.
//
// The keys written to JSON at the zero value are exactly "the ones always
// written", that is, the schema's "required" set. It is derived from the type
// instead of writing the example by hand a second time: when a field was
// forgotten between the two examples, the test would fail for the wrong reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of one described endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see admin.go, store.go).
	status string
	// request is an example carrying ALL the fields of the request body; nil
	// when the endpoint takes no body.
	request any
	// response is an example carrying all the fields of the RECORD in the
	// successful response; nil when the response has no body (204).
	response any
	// list says whether the response comes back in the list envelope.
	list bool
	// queries are the query parameters the handler REALLY reads.
	queries []string
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// pagingQueries are the parameters every paged endpoint reads.
var pagingQueries = []string{"limit", "offset"}

// describedEndpoints are the expectations of the described endpoints.
//
// No DTO carries omitempty, so the zero-valued examples write every field;
// even so, the example is derived FROM THE TYPE, not from a hand-written list
// of fields.
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: pathAdminRegions, status: "201",
			request: createRegionRequest{}, response: regionDTO{},
		},
		{
			method: http.MethodGet, path: pathAdminRegions, status: "200",
			response: regionDTO{}, list: true, queries: pagingQueries,
		},
		{
			method: http.MethodGet, path: pathAdminRegion, status: "200",
			response: regionDTO{},
		},
		{
			method: http.MethodPut, path: pathAdminRegion, status: "200",
			request: updateRegionRequest{}, response: regionDTO{},
		},
		{
			method: http.MethodDelete, path: pathAdminRegion, status: "204",
		},
		{
			method: http.MethodPost, path: pathAdminRegionCountries, status: "201",
			request: addCountryRequest{}, response: countryDTO{},
		},
		{
			method: http.MethodGet, path: pathAdminRegionCountries, status: "200",
			response: countryDTO{}, list: true, queries: pagingQueries,
		},
		{
			method: http.MethodDelete, path: pathAdminRegionCountry, status: "204",
		},
		{
			method: http.MethodGet, path: pathAdminCountries, status: "200",
			response: countryDTO{}, list: true,
			queries: []string{"limit", "offset", "region_id"},
		},
		{
			method: http.MethodGet, path: pathAdminCurrencies, status: "200",
			response: currencyDTO{}, list: true, queries: pagingQueries,
		},
		{
			method: http.MethodGet, path: pathAdminCurrency, status: "200",
			response: currencyDTO{},
		},
		{
			method: http.MethodGet, path: pathStoreRegions, status: "200",
			response: storeRegionDTO{}, list: true, queries: pagingQueries,
		},
		{
			method: http.MethodGet, path: pathStoreRegion, status: "200",
			response: storeRegionDTO{},
		},
	}
}

// TestDescribedEndpointsDescribeTheirBodies verifies that every endpoint says
// what it TAKES and what it RETURNS.
//
// This is the exact counterpart of the finding: a schema without bodies tells
// the client "this endpoint exists and can fail like this" and does not say
// what to send; the client generator then produces a method whose everything is
// 'any' and whose return type is 'void'.
//
// The field sets are compared with the DTO's encoding/json output, not with a
// hand-written list: a hand-written list falls short the day a field is added
// to the DTO, and the test would not see it.
func TestDescribedEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, endpoint := range describedEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have requestBody, one that does not must not")

			if endpoint.request != nil {
				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fields(t, components, schema),
					"the request body's fields have to match the DTO")
			}

			definition := successResponse(t, op, endpoint.status)

			if endpoint.response == nil {
				assert.NotContains(t, definition, "content",
					"a 204 has no body; the schema must not promise one")

				return
			}

			record := envelopeRecord(t, components, bodySchema(t, definition), endpoint.list)
			assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fields(t, components, record),
				"the response record's fields have to match the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
				requiredFields(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// successResponse returns the operation's response definition at the EXPECTED
// status code.
func successResponse(t *testing.T, op map[string]any, status string) map[string]any {
	t.Helper()

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses[status].(map[string]any)
	require.True(t, ok, "the code the handler REALLY writes has to be documented: %s", status)

	return definition
}

// envelopeRecord returns the RECORD schema inside the response envelope.
//
// The single and the list envelopes carry the same "data" field, but in a list
// that field is an ARRAY; reading the record directly would take the array
// schema for the record, and the field comparison would silently pass with an
// empty set.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	expectedFields := []string{"data"}
	if list {
		expectedFields = []string{"data", "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, expectedFields, fields(t, components, envelope),
		"the response envelope's shape has to be the same as the envelope in plan Section 8")

	properties, ok := resolveSchema(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	record, ok := properties["data"].(map[string]any)
	require.True(t, ok)

	if !list {
		return record
	}

	item, ok := record["items"].(map[string]any)
	require.True(t, ok, "the list envelope's data field has to be an array")

	return item
}

// TestEveryDescribedEndpointIsInTheTable verifies that no endpoint is left
// undescribed.
//
// When a new endpoint is added and not described, this test fails. Without the
// warning the fault would be SILENT: the endpoint appears in the document with
// its path and its security, only without a body — that is, the schema says "it
// exists but what it takes is unknown" and nobody notices.
func TestEveryDescribedEndpointIsInTheTable(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	var found []string

	for path, operations := range paths {
		byMethod, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a map of methods")

		for method, raw := range byMethod {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	expected := make([]string, 0, len(describedEndpoints()))
	for _, endpoint := range describedEndpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"an endpoint missing from the table has not been tested")
}

// TestEndpointsPromiseNoUnreadParameter verifies that the schema announces no
// query parameter that is not read.
//
// The only parameters read are paging and the "region_id" filter of
// /admin/v1/countries (see [pageParams], [optionalParam]). Writing a parameter
// on any other endpoint meant the client generator putting an argument on the
// method, and the caller filling it in while the server silently ignores it.
func TestEndpointsPromiseNoUnreadParameter(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, endpoint := range describedEndpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)

		var queries []string

		params, _ := op["parameters"].([]any)
		for _, raw := range params {
			p, ok := raw.(map[string]any)
			require.True(t, ok)

			if p["in"] != "query" {
				continue
			}

			name, ok := p["name"].(string)
			require.True(t, ok)

			queries = append(queries, name)
		}

		assert.ElementsMatch(t, endpoint.queries, queries,
			"%s has to announce only the parameters the handler REALLY reads",
			endpoint.key())
	}
}

// TestReferenceDataEndpointsPromiseNoWrite verifies that the currency and
// country endpoints are READS and that the schema SAYS so.
//
// Two separate faults are caught. The first is a write endpoint being added to
// reference data one day: making the ISO table "correctable" over HTTP would,
// for a single currency entered with the wrong number of decimal digits, show
// every amount in that currency at the wrong scale. The second is silence — a
// client developer looking at the path list sees the GET and assumes there is a
// POST too; having the distinction written in the description cuts that
// assumption off.
func TestReferenceDataEndpointsPromiseNoWrite(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	referencePaths := []string{pathAdminCurrencies, pathAdminCurrency, pathAdminCountries}

	for _, path := range referencePaths {
		byMethod, ok := paths[path].(map[string]any)
		require.True(t, ok, "%s has to be in the document", path)

		assert.ElementsMatch(t, []string{"get"}, keys(byMethod),
			"%s is reference data; it has to carry GET only", path)

		description, _ := operation(t, paths, http.MethodGet, path)["description"].(string)
		assert.Contains(t, description, "READ-ONLY",
			"the %s description has to say it is reference data", path)
	}
}
