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

// The test is in the INTERNAL package because the bodies it describes
// ([createTaxRateRequest], [taxRateDTO] …) are unexported. The only way to test
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
	doc.ForModule("tax", func() { Describe(doc) })

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
	// the code the handler writes (see admin.go).
	status string
	// request is an example carrying EVERY field of the request body; nil when
	// the endpoint takes no body.
	request any
	// response is an example carrying every field of the RECORD in the
	// successful response; nil when the response has no body (204).
	response any
	// list says whether the response comes in the list envelope.
	list bool
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// endpoints are the expectations of the described endpoints.
//
// The response examples are FULL: the metadata field, which carries omitempty,
// gets a non-zero value, because the comparison is "the schema's properties =
// the encoded keys" and an empty example would not write that field at all.
func endpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: pathAdminRegions, status: "201",
			request: createTaxRegionRequest{}, response: fullRegion(),
		},
		{
			method: http.MethodGet, path: pathAdminRegions, status: "200",
			response: fullRegion(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminRegion, status: "200",
			response: fullRegion(),
		},
		{method: http.MethodDelete, path: pathAdminRegion, status: "204"},
		{
			method: http.MethodGet, path: pathAdminRegionRates, status: "200",
			response: fullRate(), list: true,
		},

		{
			method: http.MethodPost, path: pathAdminClasses, status: "201",
			request: createTaxClassRequest{}, response: fullClass(),
		},
		{
			method: http.MethodGet, path: pathAdminClasses, status: "200",
			response: fullClass(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminClass, status: "200",
			response: fullClass(),
		},
		{method: http.MethodDelete, path: pathAdminClass, status: "204"},
		{
			method: http.MethodGet, path: pathAdminClassProducts, status: "200",
			response: fullMembership(), list: true,
		},
		{
			method: http.MethodPost, path: pathAdminClassProducts, status: "201",
			request: taxClassMemberRequest{}, response: fullMembership(),
		},
		{method: http.MethodDelete, path: pathAdminClassProduct, status: "204"},

		{
			method: http.MethodPost, path: pathAdminRates, status: "201",
			request: createTaxRateRequest{}, response: fullRate(),
		},
		{
			method: http.MethodGet, path: pathAdminRates, status: "200",
			response: fullRate(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminRate, status: "200",
			response: fullRate(),
		},
		{
			method: http.MethodPut, path: pathAdminRate, status: "200",
			request: updateTaxRateRequest{}, response: fullRate(),
		},
		{method: http.MethodDelete, path: pathAdminRate, status: "204"},
		{
			method: http.MethodGet, path: pathAdminRateTrial, status: "200",
			response: fullRateTrialReport(),
		},

		{
			method: http.MethodPost, path: pathAdminRateRules, status: "201",
			request: createTaxRateRuleRequest{}, response: taxRateRuleDTO{},
		},
		{
			method: http.MethodGet, path: pathAdminRateRules, status: "200",
			response: taxRateRuleDTO{}, list: true,
		},
		{method: http.MethodDelete, path: pathAdminRateRule, status: "204"},
	}
}

// fullRegion produces a tax region record with its omitempty fields written
// too.
func fullRegion() taxRegionDTO {
	return taxRegionDTO{Metadata: map[string]any{"k": "v"}}
}

// fullRateTrialReport produces a rate trial report with its omitempty fields
// written too.
func fullRateTrialReport() taxRateTrialReportDTO {
	bps := int32(800)
	return taxRateTrialReportDTO{Change: rateChangeDTO{
		RateBps: &bps, AddRules: []ruleKeyDTO{{}}, DropRules: []string{"taxrule_1"},
	}}
}

// fullRate produces a tax rate record with its omitempty fields written too.
func fullRate() taxRateDTO {
	return taxRateDTO{Metadata: map[string]any{"k": "v"}}
}

// TestEveryEndpointDescribesItsBodies checks that every endpoint says what it
// TAKES and what it RETURNS.
//
// This is the finding's exact counterpart: a schema without bodies tells the
// client "this endpoint exists and can fail like so" without saying what to
// send; a client generator then produces a method where everything is 'any' and
// the return type is 'void'.
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
			assert.NotEmpty(t, op["summary"], "the endpoint has to be described in one line")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has a requestBody, one that does not has none")

			if endpoint.request != nil {
				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fields(t, components, schema),
					"the request body's fields have to match the DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok, "the code the handler REALLY writes has to be documented: %s", endpoint.status)

			if endpoint.response == nil {
				assert.NotContains(t, definition, "content",
					"a 204 has no body; the schema must not promise one")

				return
			}

			record := envelopeRecord(t, components, bodySchema(t, definition), endpoint.list)

			assert.ElementsMatch(t, jsonKeys(t, endpoint.response),
				fields(t, components, record),
				"the response record's fields have to match the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
				required(t, components, record),
				"required has to be the keys encoding/json ALWAYS writes")
		})
	}
}

// envelopeRecord returns the RECORD schema the envelope carries.
//
// The single envelope holds the record directly under "data"; the list
// envelope makes it the item of an array. The envelope's own fields are
// checked too: the shape is fixed in plan Section 8, and breaking it means the
// client cannot decode the response at all.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	if list {
		assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
			fields(t, components, envelope), "the list envelope is the shape in plan Section 8")
	} else {
		assert.ElementsMatch(t, []string{"data"}, fields(t, components, envelope),
			"single responses come back in the {\"data\": …} envelope")
	}

	properties, ok := resolve(t, components, envelope)["properties"].(map[string]any)
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

// TestTheRateListDescribesItsRequiredParameter checks that tax_region_id is
// marked REQUIRED.
//
// When it is missing the handler returns 422. Showing it as optional would mean
// a client generator producing a method it believes callable that always
// returns an error.
func TestTheRateListDescribesItsRequiredParameter(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, pathAdminRates)

	params, _ := op["parameters"].([]any)

	var found bool

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["name"] != "tax_region_id" {
			continue
		}

		found = true

		assert.Equal(t, "query", p["in"])
		assert.Equal(t, true, p["required"],
			"the handler returns 422 when tax_region_id is missing; the schema has to show it as required")
	}

	require.True(t, found, "the tax_region_id parameter has to be in the document")

	// The paging parameters are DELIBERATELY absent: this endpoint reads only
	// tax_region_id from the query string and does not page the list.
	assert.ElementsMatch(t, []string{"tax_region_id"}, parameterNames(t, op, "query"))
}

// TestUnpagedListsPromiseNoQueryParameter checks that the rate and rule
// listings do not announce a parameter they never read.
//
// Both are written with [writeAll] and never read the query string. Writing
// limit/offset into the schema would mean the client generator putting an
// argument on the method, the caller filling it, and the server silently
// ignoring it.
func TestUnpagedListsPromiseNoQueryParameter(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, path := range []string{pathAdminRegionRates, pathAdminRateRules} {
		op := operation(t, paths, http.MethodGet, path)
		assert.Empty(t, parameterNames(t, op, "query"),
			"%s is not paged; no paging parameter may be announced", path)
	}
}

// TestTheRegionListDescribesTheParametersItReads checks that the query
// parameters are the same as the ones the handler REALLY reads.
func TestTheRegionListDescribesTheParametersItReads(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, pathAdminRegions)

	assert.ElementsMatch(t, []string{"limit", "offset", "country_code"},
		parameterNames(t, op, "query"),
		"the parameters have to be the same as the ones listRegions reads")
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

// TestEveryEndpointIsDescribed checks that no endpoint is left undescribed.
//
// The test fails when an endpoint is added and not described. Without the
// warning the fault would be SILENT: the endpoint shows in the document with
// its path and its security, only without a body — the schema says "it
// exists, and what it takes is unknown", and nobody notices.
func TestEveryEndpointIsDescribed(t *testing.T) {
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

	expected := make([]string, 0, len(endpoints()))
	for _, endpoint := range endpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"an endpoint missing from the table is an endpoint nothing tests")
}

// fullClass produces a tax class with its omitempty fields written too.
func fullClass() taxClassDTO {
	return taxClassDTO{
		ID:       "taxcls_1",
		Name:     "Books",
		Metadata: map[string]any{"k": "v"},
	}
}

// fullMembership produces a class membership.
func fullMembership() taxClassMemberDTO {
	return taxClassMemberDTO{TaxClassID: "taxcls_1", ProductID: "prod_1"}
}
