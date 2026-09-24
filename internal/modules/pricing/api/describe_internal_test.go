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

	"github.com/bdrtr/gobit/core/openapi"
)

// The test is in the INTERNAL package because the bodies it describes
// ([priceListRequest], [priceSetDTO] …) are unexported. The only way to test
// them from outside would be to export the types; widening the module's surface
// for the sake of testing the document would break the thing under test.

// document builds Describe's output against the REAL route tree and returns it
// as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not be enough: the
// operations are Go structs there, and the behavior under test is exactly which
// fields are written to JSON. The router has to be real too — if a description
// and a route's path drift apart, the failure shows HERE, not to somebody
// reading /openapi.json in production.
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
	doc.ForModule("pricing", func() { Describe(doc) })

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; an unmatched one never enters the document")

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

// resolve follows a "$ref" to the component in the document.
func resolve(t *testing.T, components, schema map[string]any) map[string]any {
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
	require.True(t, ok, "a body definition has content: %#v", definition)

	jsonBody, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body has to be application/json")

	schema, ok := jsonBody["schema"].(map[string]any)
	require.True(t, ok, "the body has to have a schema")

	return schema
}

// fields returns the keys of a schema's "properties".
func fields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolve(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return keys(properties)
}

// required returns a schema's "required" list.
func required(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	raw, _ := resolve(t, components, schema)["required"].([]any)

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
// REALLY on the wire, and the only thing that knows is encoding/json itself.
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
// written", which is the schema's "required" set. It is derived from the type
// rather than written a second time by hand: when a field was forgotten between
// two examples the test would fail for the wrong reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of one described endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the code the handler writes (see admin.go, store.go, history.go).
	status string
	// request is an example carrying EVERY field of the request body; nil when
	// the endpoint takes no body.
	request any
	// response is an example carrying every field of the RECORD in the
	// successful response; nil when the response has no body (204).
	response any
	// list says the response comes in the LIST envelope. Telling the two apart
	// matters: the envelope's shape differs, and a client generator produces
	// different return types for them.
	list bool
	// query are the query parameters the handler REALLY reads.
	query []string
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// endpoints are the expectations of the described endpoints.
//
// The examples are FULL: every omitempty field gets a non-zero value, because
// the comparison is "the schema's properties = the encoded keys" and an empty
// example would not write the omitempty fields at all.
func endpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: "/admin/v1/price-sets", status: "201",
			request: createPriceSetRequest{}, response: fullPriceSet(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-sets", status: "200",
			response: fullPriceSet(), list: true,
			query: []string{"limit", "offset"},
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-sets/{id}", status: "200",
			response: fullPriceSet(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/price-sets/{id}", status: "204",
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-sets/{id}/prices", status: "200",
			response: priceDTO{}, list: true,
		},
		{
			// 200, NOT 201: the endpoint creates no resource, it replaces the
			// set and returns what it wrote in the LIST envelope (see
			// [API.setPrices]).
			method: http.MethodPost, path: "/admin/v1/price-sets/{id}/prices", status: "200",
			request: setPricesRequest{}, response: priceDTO{}, list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-sets/{id}/calculate", status: "200",
			response: calculatedPriceDTO{},
			query:    []string{paramCurrencyCode, paramQuantity, paramAt},
		},
		{
			method: http.MethodPost, path: "/admin/v1/price-lists", status: "201",
			request: priceListRequest{}, response: fullPriceList(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-lists", status: "200",
			response: fullPriceList(), list: true,
			query: []string{"limit", "offset"},
		},
		{
			method: http.MethodGet, path: "/admin/v1/price-lists/{id}", status: "200",
			response: fullPriceList(),
		},
		{
			method: http.MethodPut, path: "/admin/v1/price-lists/{id}", status: "200",
			request: priceListRequest{}, response: fullPriceList(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/price-lists/{id}", status: "204",
		},
		{
			method: http.MethodGet, path: "/admin/v1/prices/{price_id}/rules", status: "200",
			response: priceRuleDTO{}, list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/prices/{price_id}/rules", status: "201",
			request: ruleRequest{}, response: priceRuleDTO{},
		},
		{
			method: http.MethodDelete, path: "/admin/v1/price-rules/{id}", status: "204",
		},
		{
			method: http.MethodGet, path: "/store/v1/price-sets/{id}", status: "200",
			response: fullStorePriceSet(),
		},
		{
			method: http.MethodGet, path: pathAdminPriceHistory, status: "200",
			response: priceTimelineDTO{Stretches: []appliedPriceDTO{{}}},
			query:    []string{paramCurrencyCode, paramFrom, paramTo},
		},
	}
}

// fullPriceList is a price list whose omitempty field is written too.
//
// "metadata" is the only omitempty field; left empty, the test could not see it
// fall out of the schema.
func fullPriceList() priceListDTO {
	return priceListDTO{Metadata: map[string]any{"campaign": "spring"}}
}

// fullPriceSet is a price set whose omitempty field is written too.
//
// "prices" is the only omitempty field; left empty, the test could not see it
// fall out of the schema.
func fullPriceSet() priceSetDTO {
	return priceSetDTO{Prices: []priceDTO{{}}}
}

// fullStorePriceSet is a storefront price set whose every omitempty field is
// written, so the comparison sees the whole shape (ADR 0167).
func fullStorePriceSet() storePriceSetDTO {
	listType, lowest, since := "sale", int64(1), time.Unix(1, 0)

	return storePriceSetDTO{Prices: []storePriceDTO{{
		PriceListType: &listType, ReducedSince: &since, LowestPriorAmount: &lowest,
	}}}
}

// TestEveryEndpointDescribesItsBodies checks that every endpoint says what it
// TAKES and what it RETURNS.
//
// This is the finding's exact counterpart: a schema without bodies tells a
// client "this endpoint exists and may fail like this" and not what to send;
// a client generator produces a method whose everything is 'any' and whose
// return is 'void' — a client that cannot SET a price.
//
// The field sets are compared with the DTO's encoding/json output rather than
// with a hand-written list: a hand-written list falls short the day a field is
// added to the DTO, and the test would not see it.
func TestEveryEndpointDescribesItsBodies(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, endpoint := range endpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)
			assert.NotEmpty(t, op["summary"], "an operation without a summary is a nameless client method")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has a requestBody, one that does not has none")

			if endpoint.request != nil {
				assert.Equal(t, true, requestDefinition["required"],
					"a write endpoint's body is required")

				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fields(t, components, schema),
					"the request body's fields have to match the DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok, "the code the handler REALLY writes has to be documented: %s",
				endpoint.status)

			if endpoint.response == nil {
				assert.NotContains(t, definition, "content",
					"a 204 has no body; the schema must not promise one")

				return
			}

			checkRecordSchema(t, components, bodySchema(t, definition), endpoint)
		})
	}
}

// checkRecordSchema compares the envelope and the record inside it with the
// expectation.
func checkRecordSchema(t *testing.T, components, envelope map[string]any, endpoint endpointExpectation) {
	t.Helper()

	expectedEnvelope := []string{"data"}
	if endpoint.list {
		expectedEnvelope = []string{"data", "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, expectedEnvelope, fields(t, components, envelope),
		"the envelope's shape is fixed by plan Section 8")

	record := envelopeRecord(t, components, envelope, endpoint.list)
	assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fields(t, components, record),
		"the response record's fields have to match the DTO")
	assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
		required(t, components, record),
		"required has to be the keys encoding/json ALWAYS writes")
}

// envelopeRecord returns the RECORD schema in the envelope's "data" field.
//
// In the list envelope data is an array and what is described is the ITEM
// schema; counting fields on the array would take a full record for an empty
// one.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	properties, ok := resolve(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	data, ok := properties["data"].(map[string]any)
	require.True(t, ok)

	if !list {
		return data
	}

	item, ok := data["items"].(map[string]any)
	require.True(t, ok, "the list envelope has an item schema")

	return item
}

// TestEveryEndpointIsDescribed checks that no endpoint is left undescribed.
//
// The test fails when an endpoint is added and not described. Without it the
// fault would be SILENT: the endpoint shows in the document with its path and
// its security, and without a body — the schema says "it exists, and what it
// takes is unknown", and nobody notices.
func TestEveryEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	var found []string

	for path, operations := range paths {
		byMethod, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry is a map of methods")

		for method, raw := range byMethod {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	expected := make([]string, 0, len(endpoints()))
	for _, endpoint := range endpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"an endpoint missing from the table is an endpoint nothing tests")
}

// TestEndpointsDescribeOnlyTheParametersTheyRead checks that the query
// parameters are the ones the handler REALLY reads.
//
// Putting an unread parameter in the schema promises the client a feature that
// does NOT work: the generator adds an argument, the caller fills it, the
// server ignores it in silence. The other direction matters as much — if the
// currency the calculation endpoint reads were not described, a client could
// NEVER send it.
func TestEndpointsDescribeOnlyTheParametersTheyRead(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, endpoint := range endpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)
		assert.ElementsMatch(t, endpoint.query, parameterNames(t, op, "query"),
			"%s: the query parameters have to be the ones the handler reads", endpoint.key())
	}
}

// TestTheCalculationDescribesTheRuleContextInItsDescription checks that the
// "attr_" prefix is described in the description and NOT as a parameter.
//
// OpenAPI names a parameter, not a prefix: an invented "attr_*" entry would
// generate a client argument carrying exactly that name, which the server
// refuses (see [calculateQuery]; an unknown parameter is an error).
func TestTheCalculationDescribesTheRuleContextInItsDescription(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/price-sets/{id}/calculate")

	for _, name := range parameterNames(t, op, "query") {
		assert.NotContains(t, name, paramAttrPrefix,
			"a prefixed context cannot be written as a parameter name")
	}

	description, ok := op["description"].(string)
	require.True(t, ok, "the rule context has to be described in the description")
	assert.Contains(t, description, paramAttrPrefix)
}

// parameterNames returns the operation's parameter names in the given place.
func parameterNames(t *testing.T, op map[string]any, in string) []string {
	t.Helper()

	params, _ := op["parameters"].([]any)

	names := make([]string, 0, len(params))

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["in"] != in {
			continue
		}

		name, ok := p["name"].(string)
		require.True(t, ok)

		names = append(names, name)
	}

	return names
}

// TestTimeParametersDescribeTheirFormat checks that every parameter carrying a
// moment says RFC 3339 in the schema.
//
// A plain "string" would let a client generator make the field free text,
// while [timeParam] refuses every other format and the failure would come at
// run time, in the client's hands. The price history's two bounds read the
// same way the calculation's moment does, so all three are held to it.
func TestTimeParametersDescribeTheirFormat(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, tc := range []struct {
		path, name string
	}{
		{"/admin/v1/price-sets/{id}/calculate", paramAt},
		{pathAdminPriceHistory, paramFrom},
		{pathAdminPriceHistory, paramTo},
	} {
		op := operation(t, paths, http.MethodGet, tc.path)

		params, ok := op["parameters"].([]any)
		require.True(t, ok)

		var found bool

		for _, raw := range params {
			p, ok := raw.(map[string]any)
			require.True(t, ok)

			if p["name"] != tc.name {
				continue
			}

			found = true

			schema, ok := p["schema"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, typeString, schema[schemaType], "%s %s", tc.path, tc.name)
			assert.Equal(t, formatDateTimeName, schema[schemaFormat], "%s %s", tc.path, tc.name)
		}

		require.True(t, found, "%s: the %q parameter has to be described", tc.path, tc.name)
	}
}

// TestTheSchemaDescribesTimeFieldsAsDates checks that the response record's
// timestamps appear in the schema as date-times.
//
// The concrete gain: a client generator produces a correctly formatted field as
// a Date, not as a plain string — so a price list's validity window can be
// compared as a date on the client.
func TestTheSchemaDescribesTimeFieldsAsDates(t *testing.T) {
	t.Parallel()

	paths, components := document(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/price-lists/{id}")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok)

	record := envelopeRecord(t, components, bodySchema(t, definition), false)

	properties, ok := resolve(t, components, record)["properties"].(map[string]any)
	require.True(t, ok)

	created, ok := properties["created_at"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, formatDateTimeName, created[schemaFormat])

	// A window field that may be left empty has to be both a date and null; a
	// single type would make a list with no end undecodable on the client.
	starts, ok := properties["starts_at"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []any{typeString, "null"}, starts[schemaType])
}
