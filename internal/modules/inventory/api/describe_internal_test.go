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
// ([createItemRequest], [inventoryLevelDTO] …) are unexported. The only way to
// test them from outside would be to export the types; widening the module's
// surface for the sake of testing the document would break the thing under
// test.

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
	doc.ForModule("inventory", func() { Describe(doc) })

	r := chi.NewRouter()
	NewHandler(nil, nil).Routes(r)

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
	// the same as the code the handler writes (see api.go).
	status string
	// request is an example carrying ALL the fields of the request body; nil
	// when the endpoint takes no body.
	request any
	// response is an example carrying all the fields of the RECORD in the
	// successful response; nil when the response has no body (204).
	response any
	// list says that the response comes back in the LIST envelope. Telling a
	// single record from a list apart is essential: the envelope's shape
	// differs, and the client generator produces different return types for
	// the two.
	list bool
	// cursorPage reports that the response comes back in the KEYSET envelope —
	// "data" and an optional "next_cursor" — rather than in the list envelope.
	//
	// It is a third shape and not a variant of list, because the two say
	// opposite things to a client: a list envelope promises a total and an
	// offset, and a keyset page has neither and offers a position instead. A
	// generator handed the wrong one produces a paging loop that cannot finish.
	cursorPage bool
	// queries are the query parameters the handler REALLY reads.
	queries []string
}

// key returns the operation's "METHOD path" identity.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// describedEndpoints are the expectations of the described endpoints.
//
// The examples are FILLED: every field carrying omitempty gets a non-zero
// value, because the comparison has the form "the schema's properties set =
// the encoded key set", and an empty example would not write the omitempty
// fields at all.
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodPost, path: pathStockLocations, status: "201",
			request: createStockLocationRequest{}, response: filledLocation(),
		},
		{
			method: http.MethodGet, path: pathStockLocations, status: "200",
			response: filledLocation(), list: true,
			queries: []string{"limit", "offset", "include_closed"},
		},
		{
			method: http.MethodGet, path: pathStockLocation, status: "200",
			response: filledLocation(),
		},
		{
			// It is 200 and NOT 204: the endpoint returns the NEW state of the
			// record, which is exactly what the caller needs to see (closed_at).
			method: http.MethodPost, path: pathStockLocationClose, status: "200",
			response: filledLocation(),
		},
		{
			// Which channels the warehouse ships for. The response is NOT the
			// LIST envelope: the data is a single record CARRYING an array —
			// there is nothing to page, because the set is bounded by one
			// warehouse's channels.
			method: http.MethodGet, path: pathLocationChannels, status: "200",
			response: salesChannelsResponse{SalesChannelIDs: []string{"sc_1"}},
		},
		{
			method: http.MethodPost, path: pathLocationChannels, status: "200",
			request:  salesChannelBindingRequest{},
			response: salesChannelsResponse{SalesChannelIDs: []string{"sc_1"}},
		},
		{
			method: http.MethodDelete, path: pathLocationChannel, status: "200",
			response: salesChannelsResponse{SalesChannelIDs: []string{"sc_1"}},
		},
		{
			method: http.MethodPost, path: pathItems, status: "201",
			request: createItemRequest{}, response: filledItem(),
		},
		{
			method: http.MethodGet, path: pathItems, status: "200",
			response: filledItem(), list: true,
			queries: []string{"limit", "offset", "sku", "requires_shipping"},
		},
		{
			method: http.MethodGet, path: pathItem, status: "200",
			response: filledItem(),
		},
		{
			method: http.MethodDelete, path: pathItem, status: "204",
		},
		{
			method: http.MethodGet, path: pathItemLevels, status: "200",
			response: inventoryLevelDTO{}, list: true,
		},
		{
			// It is 200, NOT 201: the level row already exists at the
			// intersection of the item and the location; the endpoint creates
			// no new resource.
			method: http.MethodPost, path: pathItemLevels, status: "200",
			request: setLevelRequest{}, response: inventoryLevelDTO{},
		},
		{
			method: http.MethodPost, path: pathItemLevelAdjust, status: "200",
			request: adjustLevelRequest{}, response: inventoryLevelDTO{},
		},
		{
			// The ledger's listing is the module's only KEYSET page, so its
			// envelope carries a cursor instead of a count and an offset
			// (ADR 0068).
			method: http.MethodGet, path: pathItemMovements, status: "200",
			response: filledMovement(), cursorPage: true,
			queries: []string{"limit", "location_id", "after"},
		},
		{
			// The queue of orders waiting for the item's units (ADR 0392).
			method: http.MethodGet, path: pathItemBackorders, status: "200",
			response: filledBackorder(), list: true,
			queries: []string{"limit", "offset", "status"},
		},
	}
}

// filledBackorder produces a claim whose omitempty fields are written too: a
// filled claim carries every key.
func filledBackorder() backorderDTO {
	return backorderDTO{ReservationID: "invres_1", FilledLocationID: "sloc_1", LocationIDs: []string{}}
}

// filledMovement produces a movement record whose omitempty fields are written
// too.
//
// ReservationID is the omitempty field and it is FILLED here: the comparison is
// "the schema's properties = the encoded key set", and a sale is the row shape
// that carries every key.
func filledMovement() movementDTO {
	return movementDTO{ReservationID: "invres_1"}
}

// filledLocation produces a location record whose omitempty fields are written
// too.
//
// ClosedAt is left unset, and has to be: the field carries NO omitempty, so it
// is written even while nil. Filling it in would test a populated record rather
// than the claim that the field appears in every response.
func filledLocation() stockLocationDTO {
	return stockLocationDTO{
		Address1:    "A",
		Address2:    "B",
		City:        "C",
		Province:    "D",
		PostalCode:  "E",
		CountryCode: "TR",
	}
}

// filledItem produces an inventory item whose omitempty fields are written too.
func filledItem() inventoryItemDTO {
	return inventoryItemDTO{Title: "A", Description: "B"}
}

// TestDescribedEndpointsDescribeTheirBodies verifies that every endpoint says
// what it TAKES and what it RETURNS.
//
// This is the exact counterpart of the finding: a schema without bodies tells
// the client "this endpoint exists and can fail like this" and does not say
// what to send; the client generator then produces a method whose everything
// is 'any' and whose return type is 'void' — that is, stock CANNOT BE WRITTEN
// with that client.
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
			assert.NotEmpty(t, op["summary"], "an operation without a summary becomes a nameless method in the client")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have requestBody, one that does not must not")

			if endpoint.request != nil {
				assert.Equal(t, true, requestDefinition["required"],
					"a write endpoint's body has to be required")

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

			assertRecordSchema(t, components, bodySchema(t, definition), endpoint)
		})
	}
}

// assertRecordSchema compares the envelope and the record inside it with the
// expectation.
func assertRecordSchema(t *testing.T, components, envelope map[string]any, endpoint endpointExpectation) {
	t.Helper()

	expectedEnvelope := []string{"data"}
	switch {
	case endpoint.list:
		expectedEnvelope = []string{"data", "count", "offset", "limit"}
	case endpoint.cursorPage:
		expectedEnvelope = []string{"data", "next_cursor"}
	}

	assert.ElementsMatch(t, expectedEnvelope, fields(t, components, envelope),
		"the envelope's shape is fixed in plan Section 8")

	record := envelopeRecord(t, components, envelope, endpoint.list || endpoint.cursorPage)
	assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fields(t, components, record),
		"the response record's fields have to match the DTO")
	assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
		requiredFields(t, components, record),
		"required has to be the same as the keys encoding/json ALWAYS writes")
}

// envelopeRecord returns the RECORD schema in the envelope's "data" field.
//
// In the list envelope data is an array and what is really described is the
// ITEM schema; looking at the array and counting its fields would be taking a
// filled record for an empty one.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

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

// TestEndpointsDescribeOnlyTheParametersTheyRead verifies that the query
// parameters are the same as the ones the handler REALLY reads.
//
// Putting a parameter that is not read into the schema promises the client a
// feature that DOES NOT WORK: the generator puts an argument on the method, the
// caller fills it in, and the server silently ignores it. The opposite
// direction matters just as much — if the "sku" filter the item listing reads
// is not described, the client can NEVER send it.
func TestEndpointsDescribeOnlyTheParametersTheyRead(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, endpoint := range describedEndpoints() {
		op := operation(t, paths, endpoint.method, endpoint.path)
		assert.ElementsMatch(t, endpoint.queries, parameterNames(t, op, "query"),
			"%s's query parameters have to be the same as the ones the handler reads", endpoint.key())
	}
}

// TestTheItemFilterIsDescribedAsABoolean verifies that requires_shipping
// appears in the schema as a boolean.
//
// Described as a string, it would make the client generator produce an
// argument that free text can be sent through; yet [Handler.listItems] reads it
// with strconv.ParseBool and rejects a value it cannot parse with a 422.
func TestTheItemFilterIsDescribedAsABoolean(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)
	op := operation(t, paths, http.MethodGet, pathItems)

	params, ok := op["parameters"].([]any)
	require.True(t, ok)

	var found bool

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["name"] != "requires_shipping" {
			continue
		}

		found = true

		schema, ok := p["schema"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, typeBoolean, schema[schemaType])
	}

	require.True(t, found, "the requires_shipping filter has to be described")
}

// parameterNames returns the names of the operation's parameters at the given
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
