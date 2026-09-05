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

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// envelopeDataField is the name of the record-carrying field of the response
// envelope (plan Section 8).
//
// The reason for keeping it as a constant is not the repetition itself but the
// fact that a typo is SILENT: a key written as "dta" compiles and the test
// would fail for the wrong reason.
const envelopeDataField = "data"

// The test is in the INTERNAL package because the bodies being described
// ([createReturnRequest], [returnDTO] …) are unexported. The only way to test
// from outside would be to export the types; widening the module's surface for
// the sake of testing the document would break the very thing being tested.

// document produces Describe's output against the REAL route tree and returns
// it as read back from JSON.
//
// Looking directly at [openapi.Doc.Build]'s output would not have been enough:
// there the operations are Go structs and the behavior under examination is
// exactly whether the fields are written to JSON or not. The router has to be
// real too — if the description and the route's path drift apart, let the fault
// show up HERE, not in someone looking at /openapi.json in production.
func document(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil, nil, nil, nil).Routes(r)

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

	jsonBody, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body has to be application/json")

	schema, ok := jsonBody["schema"].(map[string]any)
	require.True(t, ok, "the body has to have a schema")

	return schema
}

// fieldNames returns the keys of the schema's "properties".
func fieldNames(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	props, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

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
// out a second time by hand, it is derived from the type: had a field been
// forgotten between the two samples, the test would fail for the wrong reason.
func zeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// endpointExpectation is the contract of a single described endpoint.
type endpointExpectation struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see admin.go).
	status string
	// request is a sample carrying ALL the fields of the request body; when it
	// is nil the endpoint takes no body.
	request any
	// response is a sample carrying all the fields of the RECORD in the
	// successful response.
	response any
	// list states that the response comes back with the LIST envelope; its
	// difference from the single envelope is the paging fields, and mixing the
	// two up means a wrong return type in the client generator.
	list bool
}

// key returns the "METHOD path" identity of the operation.
func (e endpointExpectation) key() string { return e.method + " " + e.path }

// describedEndpoints holds the expectations of the described endpoints.
//
// The samples are FULL: every field carrying omitempty takes a value different
// from zero, because the comparison is of the form "the schema's properties set
// = the encoded key set" and an empty sample would not write the omitempty
// fields at all.
//
// The five endpoints returning [orderDetailDTO] are NOT here and their absence
// is deliberate: the rationale is in [Describe]'s documentation (the "LineItem"
// component name collides with cart).
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodGet, path: "/admin/v1/orders", status: "200",
			response: filledOrder(), list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/returns", status: "200",
			response: filledReturn(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/orders/{id}/returns", status: "201",
			request: createReturnRequest{}, response: filledReturn(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/returns/{returnId}",
			status: "200", response: filledReturn(),
		},
		{
			method: http.MethodPost,
			path:   "/admin/v1/orders/{id}/returns/{returnId}/cancel",
			// No request, for the reason the exchange's cancel gives below:
			// there is nothing to choose.
			status: "200", response: filledReturn(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/exchanges", status: "200",
			response: filledExchange(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/orders/{id}/exchanges", status: "201",
			request: createExchangeRequest{}, response: filledExchange(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/exchanges/{exchangeId}",
			status: "200", response: filledExchange(),
		},
		{
			method: http.MethodPost,
			path:   "/admin/v1/orders/{id}/exchanges/{exchangeId}/cancel",
			// No request: the transition takes no body. There is nothing to
			// choose — an exchange is either withdrawn or not — and a body
			// would invite a client to send one and be surprised it is ignored.
			status: "200", response: filledExchange(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/claims", status: "200",
			response: filledClaim(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/orders/{id}/claims", status: "201",
			request: createClaimRequest{}, response: filledClaim(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/claims/{claimId}",
			status: "200", response: filledClaim(),
		},
		{
			method: http.MethodPost,
			path:   "/admin/v1/orders/{id}/claims/{claimId}/cancel",
			status: "200", response: filledClaim(),
		},
		{
			// The issue endpoint answers 201 when it created the document and
			// 200 when the order already had one. The 201 is the one this table
			// checks; that both are described is what the document says.
			method: http.MethodPost, path: "/admin/v1/orders/{id}/invoice", status: "201",
			request: invoicingIssueRequest{}, response: invoiceIssuedDTO{
				InvoiceID: "inv_1", Number: "GBT2026000000001", AlreadyIssued: false,
			},
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/invoice", status: "200",
			response: orderInvoiceDTO{
				InvoiceID: "inv_1", Number: "GBT2026000000001", Status: "issued",
			},
		},
		{
			// Like the issue endpoint, the open endpoint answers 201 when it
			// opened a parcel and 200 when the idempotency key had already
			// opened one. The 201 is the one this table checks.
			method: http.MethodPost, path: "/admin/v1/orders/{id}/fulfillments", status: "201",
			request: openShipmentRequest{}, response: shipmentOpenedDTO{
				FulfillmentID: "ful_1", AlreadyOpen: false,
			},
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/fulfillments", status: "200",
			response: orderShipmentDTO{FulfillmentID: "ful_1", Status: "pending"},
		},
		{
			method: http.MethodGet, path: "/admin/v1/orders/{id}/timeline", status: "200",
			// Every field is filled in: the omitempty ones drop out of the
			// marshaled sample when they are zero, and a sample missing a field
			// the schema declares fails this comparison for a reason that has
			// nothing to do with the endpoint.
			response: timelineEntryDTO{
				At: &describeSampleTime, Kind: "payment.captured", RefID: "paycol_1",
				Clock: "application", Detail: "captured", Amount: 1000, Currency: "TRY",
			},
		},
	}
}

// undescribedEndpoints are the endpoints whose bodies are left undescribed
// because of the collision.
var undescribedEndpoints = []string{
	http.MethodGet + " /store/v1/orders/{id}",
	http.MethodGet + " /admin/v1/orders/{id}",
	http.MethodPost + " /admin/v1/orders/{id}/cancel",
	http.MethodPost + " /admin/v1/orders/{id}/complete",
	http.MethodPost + " /admin/v1/orders/{id}/archive",
}

// filledOrder produces an order record whose omitempty fields are written too.
func filledOrder() orderDTO {
	now := time.Now().UTC()

	return orderDTO{
		CustomerID:   "cus_1",
		Email:        "a@b.c",
		CartID:       "cart_1",
		Metadata:     map[string]any{"k": "v"},
		CompletedAt:  &now,
		CanceledAt:   &now,
		ArchivedAt:   &now,
		CancelReason: "the customer changed their mind",
	}
}

// filledReturn produces a return record whose omitempty fields are written too.
func filledReturn() returnDTO {
	now := time.Now().UTC()

	return returnDTO{
		Reason:     "damaged",
		Note:       "the carrier delivered it",
		Metadata:   map[string]any{"k": "v"},
		ReceivedAt: &now,
		CanceledAt: &now,
	}
}

// filledExchange produces an exchange record whose omitempty fields are written
// too.
func filledExchange() exchangeDTO {
	now := time.Now().UTC()

	return exchangeDTO{
		Note:       "size exchange",
		Metadata:   map[string]any{"k": "v"},
		CanceledAt: &now,
	}
}

// filledClaim produces a claim record whose omitempty fields are written too.
func filledClaim() claimDTO {
	now := time.Now().UTC()

	return claimDTO{
		Reason:      "missing item",
		Note:        "the box was open",
		Metadata:    map[string]any{"k": "v"},
		CompletedAt: &now,
		CanceledAt:  &now,
	}
}

// TestDescribedEndpointsDescribeTheirBodies verifies that every endpoint states
// what it TAKES and what it RETURNS.
//
// This is the exact counterpart of the finding: a bodiless schema tells the
// client "this endpoint exists and it can fail like this", not what to send;
// and the client generator produces a method in which everything is 'any' and
// the return type is 'void'.
//
// The field sets are compared against the DTO's encoding/json output, not
// against a hand-written list: a hand-written list falls short on the day a
// field is added to the DTO and the test would not see it.
func TestDescribedEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, endpoint := range describedEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)
			assert.NotEmpty(t, op["summary"], "every described endpoint has to carry a summary")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have a requestBody, one that does not must not")

			if endpoint.request != nil {
				schema := bodySchema(t, requestDefinition)
				assert.ElementsMatch(t, jsonKeys(t, endpoint.request),
					fieldNames(t, components, schema),
					"the fields of the request body have to match the DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok, "the code the handler REALLY writes has to be documented: %s", endpoint.status)

			// A listing that takes a cursor has to give one back, and one that
			// does not must not document a field it never writes. The answer is
			// read off the operation's own parameters so the two halves stay one
			// decision instead of two tables that can drift apart.
			cursored := slices.Contains(parameterNames(t, op, "query"), "after")

			record := envelopeRecord(t, components, bodySchema(t, definition), endpoint.list, cursored)
			assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fieldNames(t, components, record),
				"the fields of the response record have to match the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
				requiredNames(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// envelopeRecord returns the RECORD schema the response envelope carries.
//
// The envelope itself is tested too: mixing up the single and the list envelope
// means a wrong return type in the client generator — a caller expecting the
// paging fields gets a single record, or the other way around.
func envelopeRecord(
	t *testing.T, components, envelope map[string]any, list, cursored bool,
) map[string]any {
	t.Helper()

	expected := []string{envelopeDataField}
	if list {
		expected = []string{envelopeDataField, "count", "offset", "limit"}
	}
	if cursored {
		expected = append(expected, "next_cursor")
	}

	assert.ElementsMatch(t, expected, fieldNames(t, components, envelope), "response envelope")

	props, ok := resolveSchema(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	record, ok := props[envelopeDataField].(map[string]any)
	require.True(t, ok)

	if !list {
		return record
	}

	assert.Equal(t, "array", record["type"], "the data field of the list envelope has to be an array")

	item, ok := record["items"].(map[string]any)
	require.True(t, ok, "the array has to have an item schema")

	return item
}

// TestEveryDescribedEndpointIsInTheTable verifies that the set of described
// endpoints is the SAME as the table.
//
// It covers both directions. When a new endpoint is added and not described,
// the test fails: without the warning the fault would be SILENT — the endpoint
// appears in the document with its path and security, only its body is missing.
// It also fails when an endpoint that never entered the table is described; a
// described but untested endpoint is a contract that is only believed to be
// correct.
func TestEveryDescribedEndpointIsInTheTable(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	var found []string

	for path, operations := range paths {
		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a method map")

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
		"an endpoint that is not in the table means an untested endpoint")
}

// TestLineItemEndpointsStayInDocumentWithoutBodies verifies that the five
// undescribed endpoints DO NOT DROP OUT of the document, that they only stay
// without a body.
//
// The gap itself is tested because the gap is DELIBERATE: [orderDetailDTO]
// carries its line items with [lineItemDTO] and the "LineItem" component name
// that type would ask for is already registered in the cart module; describing
// it would have made the WHOLE document unproducible (see [Describe]). The
// endpoints still appear with their path, method and security — the client
// knows that they exist, it only does not know their shape.
//
// If the collision is one day resolved by renaming one of the types, this test
// fails; what has to be done then is to move the endpoints into the
// [describedEndpoints] table and REMOVE this test.
func TestLineItemEndpointsStayInDocumentWithoutBodies(t *testing.T) {
	t.Parallel()

	paths, _ := document(t)

	for _, entry := range undescribedEndpoints {
		method, path, _ := strings.Cut(entry, " ")

		op := operation(t, paths, method, path)
		assert.Nil(t, op["summary"], "%s has to still be undescribed", entry)
		assert.Nil(t, op["requestBody"], "%s has to stay without a body", entry)

		responses, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		for code := range responses {
			assert.NotEqual(t, "2", code[:1],
				"the successful response for %s has to stay undescribed", entry)
		}
	}
}

// parameterNames returns the names of the operation's parameters in the given
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

		if parameter["in"] == location {
			name, ok := parameter["name"].(string)
			require.True(t, ok, "a parameter has to have a name")
			names = append(names, name)
		}
	}

	return names
}

// describeSampleTime is a fixed moment for the document's samples.
var describeSampleTime = time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
