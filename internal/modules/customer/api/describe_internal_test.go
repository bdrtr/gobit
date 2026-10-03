package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// The test is in the INTERNAL package because the bodies being described
// ([customerRequest], [customerDTO] …) are unexported. The only way to exercise
// them from the outside would be to export the types; widening the module's
// surface for the sake of exercising the document would break the very thing
// being exercised.

// buildDoc produces Describe's output against the REAL route tree and returns it
// as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not have been enough:
// the operations are Go structs there and the behavior under examination is
// exactly whether the fields get written into JSON. The router has to be real
// too — the moment the description's path drifts from the route's, let the
// failure show up HERE, not in somebody looking at /openapi.json in production.
func buildDoc(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	// The namespace the composition root applies to this module (ADR 0036).
	// Without it the test would build a document whose component names differ
	// from the shipped one by exactly the prefix that IS the published
	// contract. The name is a literal because an api package cannot import
	// the module package that holds the constant — that import goes the other
	// way. What keeps the literal honest is the audit in internal/app, which
	// reads the REAL registry.
	doc.ForModule("customer", func() { Describe(doc) })

	r := chi.NewRouter()
	// Both arguments nil: the document is derived from TYPES and neither the
	// service nor the identity is consulted to build it. A handler with a
	// bound identity would describe the same paths (see storeCustomerID,
	// which runs per request), and passing one here would suggest otherwise.
	New(nil, nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; an unmatched record never enters the document")

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
	require.True(t, ok, "%s has to be in the document", path)

	op, ok := pathOperations[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "%s %s has to be in the document", method, path)

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
	require.True(t, ok, "the %q component has to be registered", ref)

	return target
}

// bodySchema extracts the JSON schema out of a response or request body
// definition.
func bodySchema(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	content, ok := definition["content"].(map[string]any)
	require.True(t, ok, "the body definition has to have content: %#v", definition)

	jsonContent, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body has to be application/json")

	schema, ok := jsonContent["schema"].(map[string]any)
	require.True(t, ok, "the body has to have a schema")

	return schema
}

// fields returns the "properties" keys of the schema.
func fields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return mapKeys(properties)
}

// requiredFields returns the "required" list of the schema.
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
// This is the other end of the comparison: the schema has to describe what is
// REALLY on the wire, and the only thing that knows that is encoding/json
// itself.
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
// written", that is, the schema's "required" set. It is derived from the type
// rather than writing the sample out a second time by hand: had a field been
// forgotten between the two samples, the test would fail for the wrong reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of a single described endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see admin.go, store.go).
	status string
	// request is a sample carrying ALL the fields of the request body; if it is
	// nil the endpoint takes no body.
	request any
	// response is a sample carrying all the fields of the RECORD in the
	// successful response; if it is nil the response has no body (204).
	response any
	// list reports that the response comes back in the list envelope.
	list bool
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// describedEndpoints are the expectations of the described endpoints.
//
// The samples are FILLED IN: every field carrying omitempty gets a value
// different from the zero one, because the comparison has the shape "the
// schema's properties set = the encoded key set", and an empty sample would not
// write the omitempty fields at all.
func describedEndpoints() []endpointExpectation {
	endpoints := []endpointExpectation{
		{
			method: http.MethodPost, path: "/admin/v1/customers", status: "201",
			request: customerRequest{}, response: filledCustomer(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/customers", status: "200",
			response: filledCustomer(), list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/customers/{id}", status: "200",
			response: filledCustomer(),
		},
		{
			method: http.MethodPut, path: "/admin/v1/customers/{id}", status: "200",
			request: updateCustomerRequest{}, response: filledCustomer(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/customers/{id}", status: "204",
		},
		{
			method: http.MethodPost, path: "/admin/v1/customers/{id}/convert-to-account",
			status: "200", response: filledCustomer(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/customers/{id}/groups", status: "200",
			response: filledGroup(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/customer-groups", status: "201",
			request: groupRequest{}, response: filledGroup(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/customer-groups", status: "200",
			response: filledGroup(), list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/customer-groups/{id}", status: "200",
			response: filledGroup(),
		},
		{
			method: http.MethodPut, path: "/admin/v1/customer-groups/{id}", status: "200",
			request: updateGroupRequest{}, response: filledGroup(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/customer-groups/{id}", status: "204",
		},
		{
			method: http.MethodPost, path: "/admin/v1/customer-groups/{id}/customers",
			status: "204", request: groupMemberRequest{},
		},
		{
			method: http.MethodDelete,
			path:   "/admin/v1/customer-groups/{id}/customers/{customer_id}",
			status: "204",
		},
		{
			method: http.MethodPut, path: "/admin/v1/customer-groups/{id}/segment", status: "200",
			request: *fullSegmentRule(), response: filledGroup(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/customer-groups/{id}/segment", status: "200",
			response: filledGroup(),
		},
		{
			method: http.MethodPost, path: "/admin/v1/customer-segments/preview", status: "200",
			request: *fullSegmentRule(), response: segmentPreviewDTO{},
		},
		{
			method: http.MethodPost, path: "/store/v1/customers", status: "201",
			request: customerRequest{}, response: filledCustomer(),
		},
		{
			method: http.MethodGet, path: "/store/v1/customers/{id}", status: "200",
			response: filledCustomer(),
		},
		{
			method: http.MethodPut, path: "/store/v1/customers/{id}", status: "200",
			request: storeUpdateCustomerRequest{}, response: filledCustomer(),
		},
	}

	// Twelve address endpoints, the SAME shape for both surfaces. Deriving
	// them rather than writing twelve rows by hand is deliberate: the two
	// surfaces' contract really is the same, and two hand-written blocks would
	// open a place where one could drift from the other.
	for _, prefix := range []string{"/admin/v1", "/store/v1"} {
		collection := prefix + "/customers/{id}/addresses"
		single := collection + "/{address_id}"

		endpoints = append(endpoints,
			endpointExpectation{
				method: http.MethodGet, path: collection, status: "200",
				response: filledAddress(), list: true,
			},
			endpointExpectation{
				method: http.MethodPost, path: collection, status: "201",
				request: addressRequest{}, response: filledAddress(),
			},
			endpointExpectation{
				method: http.MethodPut, path: single, status: "200",
				request: updateAddressRequest{}, response: filledAddress(),
			},
			endpointExpectation{method: http.MethodDelete, path: single, status: "204"},
			endpointExpectation{
				method: http.MethodPost, path: single + "/default-shipping", status: "200",
				response: filledAddress(),
			},
			endpointExpectation{
				method: http.MethodPost, path: single + "/default-billing", status: "200",
				response: filledAddress(),
			},
		)
	}

	// The wishlist (ADR 0190): the operator reads it, the storefront also
	// saves and removes. The PUT has no body; the path names the variant.
	endpoints = append(endpoints,
		endpointExpectation{
			method: http.MethodGet, path: "/admin/v1/customers/{id}/wishlist", status: "200",
			response: fullWishlistItem(), list: true,
		},
		endpointExpectation{
			method: http.MethodGet, path: "/store/v1/customers/{id}/wishlist", status: "200",
			response: fullWishlistItem(), list: true,
		},
		endpointExpectation{
			method: http.MethodPut, path: "/store/v1/customers/{id}/wishlist/{variant_id}", status: "200",
			response: fullWishlistItem(),
		},
		endpointExpectation{
			method: http.MethodDelete, path: "/store/v1/customers/{id}/wishlist/{variant_id}", status: "204",
		},
		// The stock alert (ADR 0215).
		endpointExpectation{
			method: http.MethodPut, path: "/store/v1/customers/{id}/wishlist/{variant_id}/stock-alert", status: "200",
			response: fullWishlistItem(),
		},
		endpointExpectation{
			method: http.MethodDelete, path: "/store/v1/customers/{id}/wishlist/{variant_id}/stock-alert", status: "204",
		},
		// The price alert (ADR 0216).
		endpointExpectation{
			method: http.MethodPut, path: "/store/v1/customers/{id}/wishlist/{variant_id}/price-alert", status: "200",
			request: priceAlertRequest{}, response: fullWishlistItem(),
		},
		endpointExpectation{
			method: http.MethodDelete, path: "/store/v1/customers/{id}/wishlist/{variant_id}/price-alert", status: "204",
		},
	)

	return endpoints
}

// filledAddress is the address sample that enters the comparison.
//
// The zero value IS ENOUGH, and this is where it parts from its neighbor
// [filledCustomer]: [addressDTO] carries omitempty on none of its fields, so
// every field is always written. Since the comparison has the shape "the
// schema's field set = the encoded key set", an empty sample of a type carrying
// omitempty would break the test for a reason that has nothing to do with the
// endpoint itself; here it cannot. The function exists all the same, so that
// the place to fill in is obvious the day omitempty is added to the type.
func filledAddress() addressDTO {
	return addressDTO{}
}

// undescribedEndpoints ~~are the endpoints left UNDESCRIBED because of a
// component name collision~~ is now EMPTY.
//
// **2026-09-07: the list was paid off.** The reason was real — [addressDTO] and
// [addressRequest] want the same component name as the identically named types
// in cart/api, and the document could not have been built at all — but the
// place for the fix was known correctly too: the core. ADR 0036 put the
// described module's name in front of the component name, these types became
// "CustomerAddress", cart's became "CartAddress", and not one type in this
// package was renamed.
//
// The function STAYED and returns nothing. Deleting it would have meant the
// same class of gap being reinvented the next time; an empty list instead keeps
// the "known missing" branch of [TestEveryEndpointIsDescribedOrKnownMissing]
// alive, so that if an endpoint that really cannot be described ever turns up,
// the place to write it is obvious. The test's stale-row check is in place too:
// an endpoint put on the list that does get described breaks the test.
func undescribedEndpoints() []string {
	return nil
}

// filledCustomer produces a customer record whose omitempty fields are written
// too.
func filledCustomer() customerDTO {
	return customerDTO{Metadata: map[string]any{"k": "v"}}
}

// filledGroup produces a group record whose omitempty fields are written too.
func filledGroup() customerGroupDTO {
	evaluated := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

	return customerGroupDTO{
		Metadata: map[string]any{"k": "v"}, Segment: fullSegmentRule(), SegmentEvaluatedAt: &evaluated,
	}
}

// fullSegmentRule is a rule with every field that can be left out written.
func fullSegmentRule() *segmentRuleDTO {
	return &segmentRuleDTO{CurrencyCode: "TRY", WindowDays: 30, Conditions: []segmentConditionDTO{
		{Attribute: "net_spend", Operator: "gte", Value: json.RawMessage(`1000`)},
		{Attribute: "country_code", Operator: "in", Values: []string{"TR"}},
	}}
}

// TestEndpointsDescribeTheirBodies verifies that every described endpoint says
// what it TAKES and what it RETURNS.
//
// That is the exact counterpart of the finding: a bodyless schema tells the
// client "this endpoint exists and can fail like so", it does not say what to
// send; and the client generator produces a method whose every parameter is
// 'any' and whose return type is 'void'.
//
// The field sets are compared against the DTO's encoding/json output, not
// against a hand-written list: a hand-written list falls short the day a field is
// added to the DTO and the test would not see it.
func TestEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := buildDoc(t)

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

			// A list that takes a cursor HAS TO return one; one that does not
			// must not document a field it never writes. The answer is read from
			// the endpoint's own parameters so that the two halves stay a single
			// decision.
			takesCursor := slices.Contains(parameterNames(t, op, "query"), "after")

			record := envelopeRecord(t, components, bodySchema(t, definition), endpoint.list, takesCursor)
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
func envelopeRecord(
	t *testing.T, components, envelope map[string]any, list, takesCursor bool,
) map[string]any {
	t.Helper()

	expectedFields := []string{"data"}
	if list {
		expectedFields = []string{"data", "count", "offset", "limit"}
	}
	if takesCursor {
		expectedFields = append(expectedFields, "next_cursor")
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

// TestEveryEndpointIsDescribedOrKnownMissing verifies that every endpoint in
// the document is either described or among the known missing ones.
//
// When a new endpoint is added and not described, this test fails. Without the
// warning the fault would be SILENT: the endpoint appears in the document with
// its path and its security, only without a body — that is, the schema says "it
// exists but what it takes is unknown" and nobody notices.
func TestEveryEndpointIsDescribedOrKnownMissing(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)

	described := map[string]struct{}{}
	for _, endpoint := range describedEndpoints() {
		described[endpoint.key()] = struct{}{}
	}

	missing := map[string]struct{}{}
	for _, name := range undescribedEndpoints() {
		missing[name] = struct{}{}
	}

	var found []string

	for path, operations := range paths {
		byMethod, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a map of methods")

		for method, raw := range byMethod {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			name := strings.ToUpper(method) + " " + path
			found = append(found, name)

			if _, knownMissing := missing[name]; knownMissing {
				assert.Empty(t, op["summary"],
					"%s is marked as known missing but is described; "+
						"once described it has to be taken off the undescribedEndpoints list", name)

				continue
			}

			assert.Contains(t, described, name, "%s has to be described", name)
			assert.NotEmpty(t, op["summary"], "%s has to carry a summary", name)
		}
	}

	expected := append(mapKeys(described), mapKeys(missing)...)
	assert.ElementsMatch(t, expected, found,
		"an endpoint missing from the table has not been tested")
}

// TestDescribedEndpointsPromiseNoUnreadParameter verifies that the schema
// announces no query parameter that is not read.
//
// The only two endpoints that read the query string are the customer and the
// group lists (see [Handler.adminListCustomers], [Handler.adminListGroups]).
// Writing a parameter on any other endpoint meant the client generator putting
// an argument on the method, and the caller filling it in while the server
// silently ignores it.
func TestDescribedEndpointsPromiseNoUnreadParameter(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)

	expected := map[string][]string{
		"GET /admin/v1/customers":       {"email", "has_account", "group_id", "limit", "offset", "after"},
		"GET /admin/v1/customer-groups": {"limit", "offset"},
	}

	for _, endpoint := range describedEndpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)

		queries := parameterNames(t, op, "query")

		assert.ElementsMatch(t, expected[endpoint.key()], queries,
			"%s has to announce only the parameters the handler REALLY reads",
			endpoint.key())
	}
}

// parameterNames returns the names of the operation's parameters at the given
// location.
func parameterNames(t *testing.T, op map[string]any, location string) []string {
	t.Helper()

	raw, ok := op["parameters"].([]any)
	if !ok {
		return nil
	}

	var names []string

	for _, entry := range raw {
		parameter, ok := entry.(map[string]any)
		require.True(t, ok, "a parameter has to be an object")

		if parameter["in"] != location {
			continue
		}

		name, ok := parameter["name"].(string)
		require.True(t, ok, "a parameter has to have a name")
		names = append(names, name)
	}

	return names
}

// fullWishlistItem is a wishlist item with every field written, the price
// alert's included (ADR 0216).
func fullWishlistItem() wishlistItemDTO {
	amount := int64(1_000)

	return wishlistItemDTO{
		PriceAlert: true, PriceAlertRegionID: "reg_1", PriceAlertCurrencyCode: "TRY", PriceAlertAmount: &amount,
	}
}
