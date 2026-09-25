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
// ([campaignRequest], [promotionDTO] …) are unexported. The only way to test
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
	doc.ForModule("promotion", func() { Describe(doc) })

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
	// the same as the code the handler writes (see admin.go and store.go).
	status string
	// request is an example carrying ALL the fields of the request body; nil
	// when the endpoint takes no body.
	request any
	// response is an example carrying all the fields of the RECORD in the
	// successful response; nil when the response has no body (204).
	response any
	// list says whether the response comes back in the list envelope.
	list bool
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// describedEndpoints are the expectations of the described endpoints.
//
// The examples are ZERO values, and that is safe: none of this package's DTOs
// carries omitempty, so the zero value writes every field too. If omitempty is
// added one day, the comparison fails on a missing field, and the need to fill
// the example in shows at exactly that point.
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: "/admin/v1/campaigns", status: "201",
			request: campaignRequest{}, response: campaignDTO{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/campaigns", status: "200",
			response: campaignDTO{}, list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/campaigns/{id}", status: "200",
			response: campaignDTO{},
		},
		{
			method: http.MethodPut, path: "/admin/v1/campaigns/{id}", status: "200",
			request: campaignRequest{}, response: campaignDTO{},
		},
		{method: http.MethodDelete, path: "/admin/v1/campaigns/{id}", status: "204"},

		{
			method: http.MethodPost, path: "/admin/v1/promotions", status: "201",
			request: promotionRequest{}, response: promotionDTO{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/promotions", status: "200",
			response: promotionDTO{}, list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/promotions/{id}", status: "200",
			response: promotionDTO{},
		},
		{
			method: http.MethodPut, path: "/admin/v1/promotions/{id}", status: "200",
			request: promotionRequest{}, response: promotionDTO{},
		},
		{method: http.MethodDelete, path: "/admin/v1/promotions/{id}", status: "204"},

		{
			method: http.MethodPut, path: "/admin/v1/promotions/{id}/application-method",
			status:  "200",
			request: applicationMethodRequest{}, response: applicationMethodDTO{},
		},
		{
			method: http.MethodDelete, path: "/admin/v1/promotions/{id}/application-method",
			status: "204",
		},

		{
			method: http.MethodGet, path: "/admin/v1/promotions/{id}/rules", status: "200",
			response: promotionRuleDTO{}, list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/promotions/{id}/rules", status: "201",
			request: promotionRuleRequest{}, response: promotionRuleDTO{},
		},
		{method: http.MethodDelete, path: "/admin/v1/promotion-rules/{id}", status: "204"},

		{
			method: http.MethodGet, path: "/admin/v1/promotions/{id}/redemptions", status: "200",
			response: redemptionDTO{}, list: true,
		},
		{
			// 200: redeeming is IDEMPOTENT and a second request does NOT
			// CREATE a new record.
			method: http.MethodPost, path: "/admin/v1/promotions/{id}/redeem", status: "200",
			request: redeemRequest{}, response: redemptionDTO{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/promotions/{id}/release", status: "200",
			request: releaseRequest{}, response: releaseResultDTO{},
		},

		{
			method: http.MethodPost, path: "/admin/v1/promotions/compute", status: "200",
			request: computeRequest{}, response: computeResultDTO{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/promotions/{id}/trial", status: "200",
			response: trialReportDTO{},
		},

		{
			method: http.MethodGet, path: "/store/v1/promotions/{code}", status: "200",
			response: storeCouponDTO{},
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
			assert.NotEmpty(t, op["summary"], "the endpoint has to be described in one line")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have requestBody, one that does not must not")

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
				requiredFields(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// envelopeRecord returns the RECORD schema the envelope carries.
//
// The single envelope holds the record directly under "data"; the list envelope
// makes it the item of an array. The envelope's own fields are checked as well:
// the format is fixed in plan Section 8, and breaking it means the client
// cannot decode the response at all.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	if list {
		assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
			fields(t, components, envelope), "the list envelope is the format in plan Section 8")
	} else {
		assert.ElementsMatch(t, []string{"data"}, fields(t, components, envelope),
			"single responses come back in the {\"data\": …} envelope")
	}

	properties, ok := resolveSchema(t, components, envelope)["properties"].(map[string]any)
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

// TestCustomerEndpointDoesNotLeakTheAdminBody verifies that the storefront
// coupon endpoint describes the NARROW body.
//
// Describing both bodies with one component would mean the schema promises the
// customer the promotion's status, its usage counter and its campaign; none of
// them goes out from that endpoint, and the client generator would produce
// fields that always stay empty.
func TestCustomerEndpointDoesNotLeakTheAdminBody(t *testing.T) {
	t.Parallel()

	paths, components := document(t)
	op := operation(t, paths, http.MethodGet, "/store/v1/promotions/{code}")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok)

	record := envelopeRecord(t, components, bodySchema(t, definition), false)
	names := fields(t, components, record)

	assert.ElementsMatch(t, jsonKeys(t, storeCouponDTO{}), names)

	for _, forbidden := range []string{"status", "usage_count", "usage_limit", "campaign_id", "metadata"} {
		assert.NotContains(t, names, forbidden,
			"%q does not go to the customer; the schema must not promise it", forbidden)
	}
}

// TestUnpaginatedListPromisesNoQueryParameter verifies that the rule list
// announces no parameter it does not read.
//
// GET /admin/v1/promotions/{id}/rules reads NO query string at all (it is
// written with [writeItems]). Writing limit/offset into the schema meant the
// client generator putting an argument on the method, and the caller filling it
// in while the server silently ignores it.
func TestUnpaginatedListPromisesNoQueryParameter(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/promotions/{id}/rules")

	assert.Empty(t, parameterNames(t, op, "query"),
		"the rule list is not paginated; no pagination parameter may be announced")
}

// TestPromotionListDescribesTheParametersItReads verifies that the query
// parameters are the same as the ones the handler REALLY reads.
func TestPromotionListDescribesTheParametersItReads(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/promotions")

	assert.ElementsMatch(t, []string{"limit", "offset", "status", "campaign_id"},
		parameterNames(t, op, "query"),
		"the parameters have to be the same as the ones listPromotions reads")
}

// parameterNames returns the names of an operation's parameters in the given
// location.
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
