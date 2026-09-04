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
// ([createCartRequest], [cartDTO] …) are unexported. The only way to exercise
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
	Describe(doc)

	r := chi.NewRouter()
	New(nil, Flows{}).Routes(r)

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

// endpointExpectation is the contract of a single described storefront endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be the
	// same as the code the handler writes (see store.go).
	status string
	// request is a sample carrying ALL the fields of the request body; if it is
	// nil the endpoint takes no body.
	request any
	// response is a sample carrying all the fields of the RECORD in the
	// successful response; if it is nil the response has no body (204).
	response any
}

// key returns the operation's "METHOD path" identity.
func (u endpointExpectation) key() string { return u.method + " " + u.path }

// storeEndpoints are the expectations of the described storefront endpoints.
//
// The samples are FILLED IN: every field carrying omitempty gets a value
// different from the zero one, because the comparison has the shape "the
// schema's properties set = the encoded key set" and an empty sample would never
// write the omitempty fields.
func storeEndpoints() []endpointExpectation {
	now := time.Now().UTC()
	address := addressDTO{}

	return []endpointExpectation{
		{
			method: http.MethodPost, path: "/store/v1/carts", status: "201",
			request: createCartRequest{}, response: filledCart(now),
		},
		{
			method: http.MethodGet, path: "/store/v1/carts/{id}", status: "200",
			response: cartDetailDTO{
				cartDTO:         filledCart(now),
				ShippingAddress: &address,
				BillingAddress:  &address,
			},
		},
		{
			method: http.MethodPost, path: "/store/v1/carts/{id}", status: "200",
			request: updateCartRequest{}, response: filledCart(now),
		},
		{
			method: http.MethodDelete, path: "/store/v1/carts/{id}", status: "204",
		},
		{
			method: http.MethodPost, path: "/store/v1/carts/{id}/line-items", status: "201",
			request: addLineItemRequest{}, response: filledLineItem(),
		},
		{
			method: http.MethodPatch, path: "/store/v1/carts/{id}/line-items/{line_item_id}",
			status:  "200",
			request: updateLineItemRequest{}, response: filledLineItem(),
		},
		{
			method: http.MethodDelete, path: "/store/v1/carts/{id}/line-items/{line_item_id}",
			status: "204",
		},
		{
			method: http.MethodPut, path: "/store/v1/carts/{id}/shipping-address", status: "200",
			request: addressRequest{}, response: filledAddress(),
		},
		{
			method: http.MethodPut, path: "/store/v1/carts/{id}/billing-address", status: "200",
			request: addressRequest{}, response: filledAddress(),
		},
		{
			method: http.MethodPost, path: "/store/v1/carts/{id}/shipping-methods", status: "201",
			request: addShippingMethodRequest{}, response: filledShippingMethod(),
		},
		{
			method: http.MethodPost, path: "/store/v1/carts/{id}/complete", status: "200",
			request: completeCartRequest{}, response: completeCartDTO{},
		},
		{
			method: http.MethodDelete,
			path:   "/store/v1/carts/{id}/shipping-methods/{shipping_method_id}",
			status: "204",
		},
	}
}

// filledCart produces a cart record whose omitempty fields are written too.
func filledCart(now time.Time) cartDTO {
	return cartDTO{
		CustomerID:  "cus_1",
		Email:       "a@b.c",
		Metadata:    map[string]any{"k": "v"},
		CompletedAt: &now,
	}
}

// filledLineItem produces a line item record whose omitempty fields are written
// too.
func filledLineItem() lineItemDTO {
	return lineItemDTO{Metadata: map[string]any{"k": "v"}}
}

// filledAddress produces an address record whose omitempty fields are written
// too.
func filledAddress() addressDTO {
	return addressDTO{
		SourceAddressID: "addr_1",
		FirstName:       "A",
		LastName:        "B",
		Company:         "C",
		Address1:        "D",
		Address2:        "E",
		City:            "F",
		Province:        "G",
		PostalCode:      "H",
		CountryCode:     "TR",
		Phone:           "I",
		Metadata:        map[string]any{"k": "v"},
	}
}

// filledShippingMethod produces a shipping method whose omitempty fields are
// written too.
func filledShippingMethod() shippingMethodDTO {
	return shippingMethodDTO{ShippingOptionID: "so_1", Data: map[string]any{"k": "v"}}
}

// TestStoreEndpointsDescribeTheirBodies verifies that every storefront endpoint
// says what it TAKES and what it RETURNS.
//
// That is the exact counterpart of the finding: a bodyless schema tells the
// client "this endpoint exists and can fail like so", it does not say what to
// send; and the client generator produces a method where everything is 'any' and
// the return type is 'void'.
//
// The field sets are compared against the DTO's encoding/json output, not
// against a hand-written list: a hand-written list falls short the day a field is
// added to the DTO and the test would not see it.
func TestStoreEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := buildDoc(t)

	for _, endpoint := range storeEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have a requestBody, one that does not must not")

			if endpoint.request != nil {
				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fields(t, components, schema),
					"the request body's fields have to overlap with the DTO")
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

			envelope := bodySchema(t, definition)
			assert.ElementsMatch(t, []string{"data"}, fields(t, components, envelope),
				"single responses are returned in a {\"data\": …} envelope")

			record := envelopeRecord(t, components, envelope)
			assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fields(t, components, record),
				"the response record's fields have to overlap with the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
				requiredFields(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// envelopeRecord returns the schema of the single envelope's "data" field.
func envelopeRecord(t *testing.T, components, envelope map[string]any) map[string]any {
	t.Helper()

	properties, ok := resolveSchema(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	record, ok := properties["data"].(map[string]any)
	require.True(t, ok)

	return record
}

// TestEveryStoreEndpointIsDescribed verifies that no storefront endpoint has been
// left undescribed.
//
// When a new endpoint is added and not described, this test fails. Without the
// warning the failure would be SILENT: the endpoint shows up in the document
// with its path and its security, it just has no body — that is, the schema says
// "it exists but what it takes is unknown" and nobody notices.
func TestEveryStoreEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)

	var found []string

	for path, operations := range paths {
		if !strings.HasPrefix(path, "/store/v1") {
			continue
		}

		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "the path entry has to be a method map")

		for method, raw := range operationMap {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	expected := make([]string, 0, len(storeEndpoints()))
	for _, endpoint := range storeEndpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"a storefront endpoint that is not in the table means it is not exercised")
}

// TestAdminListDescribesItsBody verifies that /admin/v1/carts says what it
// returns.
//
// The record type is the real point here: the endpoint does NOT LOAD the line
// items, the addresses and the shipping methods (see [Handler.adminListCarts]).
// Had [cartDetailDTO] been described, the schema would promise fields that are
// never filled in, and the client would see an empty "items" array in every
// response and believe the line items really were not there.
func TestAdminListDescribesItsBody(t *testing.T) {
	t.Parallel()

	paths, components := buildDoc(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/carts")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody",
		"cart's admin surface only reads; none of its endpoints takes a body")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	// The list returns 200 (see adminListCarts); writing another code would
	// produce wrong branching in the client generator.
	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok, "the code the handler REALLY writes has to be documented")

	envelope := bodySchema(t, definition)
	assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
		fields(t, components, envelope), "the list envelope is the shape in plan Section 8")

	item := listItemSchema(t, components, envelope)
	recordFields := fields(t, components, item)

	assert.ElementsMatch(t, jsonKeys(t, filledCart(time.Now().UTC())), recordFields,
		"the list record has to overlap with cartDTO")
	assert.NotContains(t, recordFields, "items",
		"the list endpoint does not load the line items; the detail schema must not be promised")
	assert.ElementsMatch(t, jsonKeys(t, cartDTO{}),
		requiredFields(t, components, item),
		"required has to be the same as the keys encoding/json ALWAYS writes")
}

// listItemSchema returns the item schema of the list envelope's "data" array.
func listItemSchema(t *testing.T, components, envelope map[string]any) map[string]any {
	t.Helper()

	data := envelopeRecord(t, components, envelope)
	assert.Equal(t, "array", data["type"], "the list envelope's data field is an array")

	item, ok := data["items"].(map[string]any)
	require.True(t, ok, "the list envelope has to have an item schema")

	return item
}

// TestAdminSingleEndpointDescribesItsBody verifies that /admin/v1/carts/{id} says
// it returns the cart with its children.
func TestAdminSingleEndpointDescribesItsBody(t *testing.T) {
	t.Parallel()

	paths, components := buildDoc(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/carts/{id}")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody", "a read endpoint takes no body")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok, "the code the handler REALLY writes has to be documented")

	envelope := bodySchema(t, definition)
	assert.ElementsMatch(t, []string{"data"}, fields(t, components, envelope),
		"single responses are returned in a {\"data\": …} envelope")

	address := addressDTO{}
	expected := cartDetailDTO{
		cartDTO:         filledCart(time.Now().UTC()),
		ShippingAddress: &address,
		BillingAddress:  &address,
	}

	record := envelopeRecord(t, components, envelope)
	assert.ElementsMatch(t, jsonKeys(t, expected), fields(t, components, record),
		"the response record has to overlap with cartDetailDTO")
}

// TestAdminListDescribesOnlyTheParametersItReads verifies that the query
// parameters are the same as the ones the handler REALLY reads.
//
// This is the ONLY endpoint that filters carts from the query string. More would
// mean promising the client a filter that does not work, fewer would mean hiding
// a filter that is read from the client.
func TestAdminListDescribesOnlyTheParametersItReads(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)
	op := operation(t, paths, http.MethodGet, "/admin/v1/carts")

	assert.ElementsMatch(t,
		[]string{"customer_id", "region_id", "completed", "limit", "offset"},
		parameterNames(t, op, "query"),
		"the parameters have to be the same as the ones adminListCarts reads")

	single := operation(t, paths, http.MethodGet, "/admin/v1/carts/{id}")
	assert.Empty(t, parameterNames(t, single, "query"),
		"the single admin endpoint does not read the query string")
}

// parameterNames returns the names of the operation's parameters in the given
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

// TestEveryAdminEndpointIsDescribed verifies that no admin endpoint has been left
// undescribed.
//
// Cart's admin surface deliberately consists of two endpoints (the only party
// that changes the cart is the customer); when a third endpoint is added to the
// list this test fails and stops it from slipping through undescribed.
func TestEveryAdminEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)

	var found []string

	for path, operations := range paths {
		if !strings.HasPrefix(path, "/admin/v1") {
			continue
		}

		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "the path entry has to be a method map")

		for method, raw := range operationMap {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	assert.ElementsMatch(t,
		[]string{"GET /admin/v1/carts", "GET /admin/v1/carts/{id}"}, found)
}

// TestStoreEndpointsPromiseNoQueryParameter verifies that the schema announces no
// parameter that is not read.
//
// None of the storefront cart handlers read the query string (see store.go).
// Writing a parameter into the schema all the same meant the client generator
// putting an argument on the method and the caller filling it in while the server
// silently ignored it.
func TestStoreEndpointsPromiseNoQueryParameter(t *testing.T) {
	t.Parallel()

	paths, _ := buildDoc(t)

	for _, endpoint := range storeEndpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)

		params, _ := op["parameters"].([]any)
		for _, raw := range params {
			p, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.Equal(t, "path", p["in"],
				"%s has to carry only path parameters; the %q query parameter is not read",
				endpoint.key(), p["name"])
		}
	}
}
