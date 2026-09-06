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
// ([createProfileRequest], [fulfillmentDTO] …) are unexported. The only way to
// test from the outside would be to export the types; widening the module's
// surface for the sake of testing the documentation would break the very thing
// being tested.

// document produces Describe's output against the REAL route tree and returns
// it as read back from JSON.
//
// Looking directly at [openapi.Doc.Build]'s output would not be enough: there
// the operations are Go structs and the behavior under examination is exactly
// whether the fields are written into JSON. The router has to be real as well —
// if a description and a route's path drift apart, let the failure show up
// HERE, not in someone looking at /openapi.json in production.
func document(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint must match a route; an unmatched record never enters the document")

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

// operation returns a single path+method operation from the document.
func operation(t *testing.T, paths map[string]any, method, path string) map[string]any {
	t.Helper()

	pathOperations, ok := paths[path].(map[string]any)
	require.True(t, ok, "%s must be in the document", path)

	op, ok := pathOperations[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "%s %s must be in the document", method, path)

	return op
}

// resolveSchema resolves "$ref" references to the component in the document.
func resolveSchema(t *testing.T, components, schema map[string]any) map[string]any {
	t.Helper()

	ref, isRef := schema["$ref"].(string)
	if !isRef {
		return schema
	}

	target, ok := components[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	require.True(t, ok, "the %q component must be registered", ref)

	return target
}

// bodySchema extracts the JSON schema from a response or request body
// definition.
func bodySchema(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	content, ok := definition["content"].(map[string]any)
	require.True(t, ok, "the body definition must have content: %#v", definition)

	jsonContent, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body must be application/json")

	schema, ok := jsonContent["schema"].(map[string]any)
	require.True(t, ok, "the body must have a schema")

	return schema
}

// fieldNames returns the keys of the schema's "properties".
func fieldNames(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	props, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema must have properties: %#v", schema)

	return mapKeys(props)
}

// requiredNames returns the schema's "required" list.
func requiredNames(t *testing.T, components, schema map[string]any) []string {
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

// mapKeys returns the keys of a map.
func mapKeys[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// jsonKeys encodes the value with encoding/json and returns its keys.
//
// This is the other end of the comparison: the schema has to describe what
// REALLY goes over the wire, and the only thing that knows that is
// encoding/json itself.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return mapKeys(decoded)
}

// zeroValue returns the zero value of the given sample's type.
//
// The keys written to JSON at the zero value are exactly "the ones always
// written", that is, the schema's "required" set. Instead of writing the sample
// out a second time by hand it is derived from the type: if a field were
// forgotten between the two samples, the test would fail for the wrong reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of a single described endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see handlers.go).
	status string
	// request is a sample carrying ALL the fields of the request body; nil if
	// the endpoint takes no body.
	request any
	// response is a sample carrying all the fields of the RECORD in the
	// successful response; nil if the response has no body (204).
	response any
	// list reports whether the response comes back with the list envelope.
	list bool
}

// key returns the "METHOD path" identity of the operation.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// describedEndpoints are the expectations of the described endpoints.
//
// The samples are FULL: every field carrying omitempty gets a non-zero value,
// because the comparison has the form "the schema's properties set = the
// encoded key set" and an empty sample would never write the omitempty fields.
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodGet, path: pathAdminProviders, status: "200",
			// The record is not a DTO but a plain string (see
			// [Handler.listProviders]).
			response: "", list: true,
		},

		{
			method: http.MethodPost, path: pathAdminProfiles, status: "201",
			request: createProfileRequest{}, response: filledProfile(),
		},
		{
			method: http.MethodGet, path: pathAdminProfiles, status: "200",
			response: filledProfile(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminProfile, status: "200",
			response: filledProfile(),
		},
		{
			method: http.MethodPatch, path: pathAdminProfile, status: "200",
			request: updateProfileRequest{}, response: filledProfile(),
		},
		{method: http.MethodDelete, path: pathAdminProfile, status: "204"},

		{
			method: http.MethodGet, path: pathAdminLocations, status: "200",
			response: filledPolicy(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminLocation, status: "200",
			response: filledPolicy(),
		},
		{
			method: http.MethodPut, path: pathAdminLocation, status: "200",
			request: setLocationRequest{}, response: filledPolicy(),
		},
		{method: http.MethodDelete, path: pathAdminLocation, status: "204"},

		{method: http.MethodDelete, path: pathAdminOption, status: "204"},
		{
			method: http.MethodPost, path: pathAdminOptionRules, status: "201",
			request: createRuleRequest{}, response: ruleDTO{},
		},
		{
			method: http.MethodGet, path: pathAdminOptionRules, status: "200",
			response: ruleDTO{}, list: true,
		},
		{method: http.MethodDelete, path: pathAdminOptionRule, status: "204"},

		{
			method: http.MethodGet, path: pathAdminEligible, status: "200",
			response: quotedOptionDTO{}, list: true,
		},
		{
			method: http.MethodGet, path: pathStoreOptions, status: "200",
			response: storeOptionDTO{}, list: true,
		},

		{
			method: http.MethodPost, path: pathAdminFulfillments, status: "201",
			request: createFulfillmentRequest{}, response: filledFulfillment(),
		},
		{
			method: http.MethodGet, path: pathAdminFulfillments, status: "200",
			response: filledFulfillment(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminFulfillment, status: "200",
			response: filledFulfillment(),
		},
		{
			method: http.MethodPost, path: pathAdminCancel, status: "200",
			response: filledFulfillment(),
		},
		{
			method: http.MethodPost, path: pathAdminShip, status: "200",
			request: shipRequest{}, response: filledFulfillment(),
		},
		{
			method: http.MethodPost, path: pathAdminDeliver, status: "200",
			response: filledFulfillment(),
		},
		{
			method: http.MethodPost, path: pathAdminReturned, status: "200",
			response: filledFulfillment(),
		},
	}
}

// undescribedOptionEndpoints are the endpoints left undescribed because they
// carry [optionDTO].
//
// The rationale is in the [Describe] godoc: a component name collision would
// bring down document generation entirely.
func undescribedOptionEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{method: http.MethodPost, path: pathAdminOptions},
		{method: http.MethodGet, path: pathAdminOptions},
		{method: http.MethodGet, path: pathAdminOption},
		{method: http.MethodPatch, path: pathAdminOption},
	}
}

// filledProfile produces a profile record whose omitempty fields are written
// too.
func filledProfile() profileDTO {
	return profileDTO{Metadata: map[string]any{"k": "v"}}
}

// filledPolicy produces a location shipping policy sample.
//
// locationDTO has NO omitempty field — "region_ids" is written even when it is
// empty and that is the rule itself (empty = all regions) — so the sample can
// stay empty.
func filledPolicy() locationDTO {
	return locationDTO{RegionIDs: []string{"reg_tr"}}
}

// filledFulfillment produces a fulfillment record whose omitempty fields are
// written too.
func filledFulfillment() fulfillmentDTO {
	now := time.Now().UTC()

	return fulfillmentDTO{
		TrackingNumber: "TN1",
		TrackingURL:    "https://example/TN1",
		ShippedAt:      &now,
		DeliveredAt:    &now,
		CanceledAt:     &now,
		ReturnedAt:     &now,
		Data:           json.RawMessage(`{"k":"v"}`),
		Metadata:       map[string]any{"k": "v"},
		Items:          []fulfillmentItemDTO{{}},
	}
}

// TestDescribedEndpointsDocumentTheirBodies verifies that every endpoint says
// what it TAKES and what it RETURNS.
//
// That is the exact counterpart of the finding: a schema without a body tells
// the client "this endpoint exists and can fail like so" but not what to send;
// and the client generator produces a method in which everything is 'any' and
// the return type is 'void'.
//
// The field sets are compared against the DTO's encoding/json output, not
// against a hand-written list: a hand-written list falls short the day a field
// is added to the DTO, and the test would not see it.
func TestDescribedEndpointsDocumentTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, endpoint := range describedEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)
			assert.NotEmpty(t, op["summary"], "the endpoint must be described in one line")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint taking a body must have requestBody, one that does not must not")

			if endpoint.request != nil {
				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fieldNames(t, components, schema),
					"the request body's fields must match the DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok, "the code the handler REALLY writes must be documented: %s", endpoint.status)

			if endpoint.response == nil {
				assert.NotContains(t, definition, "content",
					"a 204 has no body; the schema must not promise one")

				return
			}

			assertRecordSchema(t, components, bodySchema(t, definition), endpoint)
		})
	}
}

// assertRecordSchema verifies the envelope and the record inside it against the
// DTO.
func assertRecordSchema(t *testing.T, components, envelope map[string]any, endpoint endpointExpectation) {
	t.Helper()

	record := envelopeRecord(t, components, envelope, endpoint.list)

	// The record of the provider list is NOT a struct but a plain string; a
	// field comparison is meaningless there and the only true thing the schema
	// can say is the type itself.
	if reflect.TypeOf(endpoint.response).Kind() != reflect.Struct {
		assert.Equal(t, "string", record["type"],
			"the item type of a string list must be a string in the schema too")

		return
	}

	assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fieldNames(t, components, record),
		"the fields of the response record must match the DTO")
	assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
		requiredNames(t, components, record),
		"required must be the same as the keys encoding/json ALWAYS writes")
}

// envelopeRecord returns the RECORD schema the envelope carries.
//
// The single envelope holds the record directly under "data"; the list envelope
// makes it the item of an array. The envelope's own fields are checked as well:
// the format is fixed in plan Section 8 and breaking it means the client cannot
// parse the response at all.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	if list {
		assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
			fieldNames(t, components, envelope), "the list envelope is the format in plan Section 8")
	} else {
		assert.ElementsMatch(t, []string{"data"}, fieldNames(t, components, envelope),
			"single responses come back with the {\"data\": …} envelope")
	}

	props, ok := resolveSchema(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	data, ok := props["data"].(map[string]any)
	require.True(t, ok)

	if !list {
		return data
	}

	item, ok := data["items"].(map[string]any)
	require.True(t, ok, "the list envelope must have an item schema")

	return item
}

// TestShipRequestBodyIsNotRequired verifies that the body of the ship
// notification is not marked REQUIRED.
//
// The handler accepts an empty body (see [decodeOptionalBody]): some carriers
// provide the tracking number later. Marking it required would mean the client
// generator never produces a call without a body.
func TestShipRequestBodyIsNotRequired(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodPost, pathAdminShip)

	body, ok := op["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, body["required"])
}

// TestOptionEndpointsAreDeliberatelyUndescribed verifies that the colliding
// endpoints stay WITHOUT A BODY.
//
// The test fixes a "missing" state and that is deliberate: [optionDTO] asks for
// the "Option" component name, the product module's models.Option asks for the
// same name, and two types asking for the same name brings down document
// generation ENTIRELY. That is why the endpoints were left undescribed. If
// someone adds a body one day, /openapi.json starts returning 500, and this
// test puts the reason for that change here in writing.
func TestOptionEndpointsAreDeliberatelyUndescribed(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, endpoint := range undescribedOptionEndpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)

		assert.NotContains(t, op, "summary", "%s must be left undescribed", endpoint.key())
		assert.NotContains(t, op, "requestBody", "%s must stay without a body", endpoint.key())

		responses, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		for code := range responses {
			assert.NotEqual(t, "2", code[:1],
				"%s must not promise a success response: %s", endpoint.key(), code)
		}
	}
}

// TestEveryDescribedEndpointIsInTheTable verifies that every described endpoint
// has a counterpart in the table.
//
// When a new endpoint is added and not described, or is described and not
// written into the table, this test fails. Without the warning the fault would
// be SILENT: the endpoint appears in the document with its path and security,
// only without a body — that is, the schema says "it exists but what it takes
// is unknown" and nobody notices.
func TestEveryDescribedEndpointIsInTheTable(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	var found []string

	for path, operations := range paths {
		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry must be a method map")

		for method, raw := range operationMap {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			if op["summary"] == nil {
				continue
			}

			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	expected := make([]string, 0, len(describedEndpoints()))
	for _, endpoint := range describedEndpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"an endpoint that is not in the table has not been tested")
}

// TestEligibilityParametersDoNotExposeTrustDecisions verifies that the schema
// does not announce the trust decisions as query parameters.
//
// "include_admin_only" and "trusted_facts" are FIXED in the handler; writing
// them into the schema would imply to a client coming from the storefront that
// a single parameter could open the admin-only options.
func TestEligibilityParametersDoNotExposeTrustDecisions(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	expected := []string{
		"region_id", "currency_code", "country_code", "shipping_profile_id",
		"subtotal", "item_count", "total_weight", "is_return",
	}

	for _, path := range []string{pathStoreOptions, pathAdminEligible} {
		names := parameterNames(t, operation(t, paths, http.MethodGet, path), "query")

		assert.ElementsMatch(t, expected, names,
			"the parameters of %s must be the same as the ones parseEligibilityQuery reads", path)
		assert.NotContains(t, names, "include_admin_only")
		assert.NotContains(t, names, "trusted_facts")
	}
}

// parameterNames returns the names of the operation's parameters in the given
// place.
func parameterNames(t *testing.T, op map[string]any, where string) []string {
	t.Helper()

	params, _ := op["parameters"].([]any)

	names := make([]string, 0, len(params))

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["in"] != where {
			continue
		}

		name, ok := p["name"].(string)
		require.True(t, ok)

		names = append(names, name)
	}

	return names
}
