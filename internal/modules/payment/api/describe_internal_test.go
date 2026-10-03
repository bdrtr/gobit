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

// envelopeDataField is the name of the record-carrying field of the response
// envelope (plan Section 8).
//
// The reason for keeping it as a constant is not the repetition itself but the
// fact that a typo is SILENT: a key written as "dta" compiles and the test
// would fail for the wrong reason.
const envelopeDataField = "data"

// The test is in the INTERNAL package because the bodies being described
// ([createSessionRequest], [sessionDTO] …) are unexported. The only way to test
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
	// The namespace the composition root applies to this module (ADR 0036).
	// Without it the test would build a document whose component names differ
	// from the shipped one by exactly the prefix that IS the published
	// contract. The name is a literal because an api package cannot import
	// the module package that holds the constant — that import goes the other
	// way. What keeps the literal honest is the audit in internal/app, which
	// reads the REAL registry.
	doc.ForModule("payment", func() { Describe(doc) })

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

// properties returns the schema's "properties" map.
func properties(t *testing.T, components, schema map[string]any) map[string]any {
	t.Helper()

	m, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return m
}

// fieldNames returns the keys of the schema's "properties".
func fieldNames(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	return mapKeys(properties(t, components, schema))
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
	// the same as the code the handler writes (see handlers.go).
	status string
	// request is a sample carrying ALL the fields of the request body; when it
	// is nil the endpoint takes no body.
	request any
	// bodyOptional states that the body may not be sent at all.
	bodyOptional bool
	// response is a sample carrying all the fields of the RECORD in the
	// successful response; when it is nil the response has no body (204) or
	// its record is a primitive value.
	response any
	// list states that the response comes back in the LIST envelope.
	list bool
	// primitiveItem is the JSON Schema type name when the list item is a
	// primitive value.
	primitiveItem string
}

// key returns the "METHOD path" identity of the operation.
func (u endpointExpectation) key() string { return u.method + " " + u.path }

// bodiless reports whether the endpoint's response has no body.
func (u endpointExpectation) bodiless() bool { return u.response == nil && u.primitiveItem == "" }

// describedEndpoints holds the expectations of the described endpoints.
//
// The samples are FULL: every field carrying omitempty takes a value different
// from zero, because the comparison is of the form "the schema's properties set
// = the encoded key set" and an empty sample would not write the omitempty
// fields at all.
//
// The four endpoints that carry a payment COLLECTION body are here too, at the
// end; the comment above them says why they were once left out and what
// brought them in (ADR 0036).
func describedEndpoints() []endpointExpectation {
	return []endpointExpectation{
		{
			method: http.MethodGet, path: pathAdminProviders, status: "200",
			list: true, primitiveItem: "string",
		},
		{
			method: http.MethodGet, path: pathStoreProviders, status: "200",
			list: true, primitiveItem: "string",
		},
		{
			method: http.MethodGet, path: pathAdminCollectionSess, status: "200",
			response: fullSession(), list: true,
		},
		{
			method: http.MethodPost, path: pathAdminCollectionSess, status: "201",
			request: createSessionRequest{}, response: fullSession(),
		},
		{
			method: http.MethodGet, path: pathAdminSession, status: "200",
			response: fullSession(),
		},
		{
			method: http.MethodPost, path: pathAdminSessionAuthorize, status: "200",
			response: fullSession(),
		},
		{
			method: http.MethodPost, path: pathAdminSessionCapture, status: "201",
			request: amountRequest{}, bodyOptional: true, response: fullPayment(),
		},
		{
			method: http.MethodPost, path: pathAdminSessionCancel, status: "204",
		},
		{
			method: http.MethodGet, path: pathAdminCollectionPays, status: "200",
			response: fullPayment(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminPayment, status: "200",
			response: fullPayment(),
		},
		{
			method: http.MethodGet, path: pathAdminPaymentRefund, status: "200",
			response: fullRefund(), list: true,
		},
		{
			method: http.MethodPost, path: pathAdminPaymentRefund, status: "201",
			request: refundRequest{}, response: fullRefund(),
		},
		{
			method: http.MethodPost, path: pathStoreCollectSess, status: "201",
			request: createStoreSessionRequest{}, response: fullSession(),
		},
		{
			method: http.MethodPost, path: pathStoreSessionCancel, status: "204",
		},
		// The four collection endpoints. Until 2026-09-07 they were not in this
		// table, and there was a reason for that: because of a component name
		// collision they could not be described, and they were kept in a
		// separate list as a "known gap". ADR 0036 put the module name in front
		// of the name, the collision ended, and the list and the test guarding
		// it were removed — as that test's own documentation said they would be.
		{
			method: http.MethodPost, path: pathAdminCollections, status: "201",
			request: createCollectionRequest{}, response: fullCollection(),
		},
		{
			method: http.MethodGet, path: pathAdminCollections, status: "200",
			response: fullCollection(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminCollection, status: "200",
			response: fullCollection(),
		},
		{
			method: http.MethodGet, path: pathStoreCollection, status: "200",
			response: fullCollection(),
		},
		// The three endpoints of store credit (ADR 0152).
		{
			method: http.MethodPost, path: pathAdminStoreCredits, status: "201",
			request: issueCreditRequest{ExpiresAt: fullCreditEntry().ExpiresAt, OrderID: "order_1"}, response: fullCreditEntry(),
		},
		{
			method: http.MethodGet, path: pathAdminStoreCredits, status: "200",
			response: fullCreditEntry(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminStoreCreditBalance, status: "200",
			response: storeCreditBalanceDTO{},
		},
		// The two read endpoints of loyalty points (ADR 0164).
		{
			method: http.MethodGet, path: pathAdminLoyaltyPoints, status: "200",
			response: fullLoyaltyEntry(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminLoyaltyPointsBalance, status: "200",
			response: loyaltyBalanceDTO{},
		},
		// A customer's own balances on the storefront (ADR 0253).
		{
			method: http.MethodGet, path: pathStoreOwnStoreCredit, status: "200",
			response: storeCreditBalanceDTO{},
		},
		{
			method: http.MethodGet, path: pathStoreOwnLoyalty, status: "200",
			response: loyaltyBalanceDTO{},
		},
		// The payment journal (ADR 0186).
		{
			method: http.MethodGet, path: pathAdminPaymentJournal, status: "200",
			response: fullJournal(),
		},
		// Gift cards (ADR 0208).
		{
			method: http.MethodPost, path: pathAdminGiftCards, status: "201",
			request: issueGiftCardRequest{ExpiresAt: fullGiftCard().ExpiresAt}, response: fullGiftCard(),
		},
		{
			method: http.MethodGet, path: pathAdminGiftCards, status: "200",
			response: fullGiftCard(), list: true,
		},
		{
			method: http.MethodGet, path: pathAdminGiftCard, status: "200",
			response: fullGiftCard(),
		},
		{
			method: http.MethodGet, path: pathAdminGiftCardEntries, status: "200",
			response: giftCardEntryDTO{Reference: "gcses_1"}, list: true,
		},
		{
			method: http.MethodPost, path: pathAdminGiftCardCode, status: "200",
			response: fullGiftCard(),
		},
		// Closing a gift card (ADR 0213).
		{
			method: http.MethodPost, path: pathAdminGiftCardDisable, status: "200",
			request: disableGiftCardRequest{}, response: fullGiftCard(),
		},
	}
}

// fullGiftCard is a card with every field written, the code included.
func fullGiftCard() giftCardDTO {
	changed := time.Unix(1_000, 0).UTC()

	return giftCardDTO{
		Code: "ABCD-EFGH-JKMN-PQRS", CodeChangedAt: &changed,
		DisabledAt: &changed, DisableReason: "a test", ExpiresAt: &changed,
	}
}

// fullLoyaltyEntry produces a points ledger entry with every field written.
func fullLoyaltyEntry() loyaltyEntryDTO {
	return loyaltyEntryDTO{
		Reference: "paycol_1",
		CreatedAt: time.Now().UTC(),
	}
}

// fullCreditEntry produces a ledger entry whose omitempty fields are written
// too.
func fullCreditEntry() storeCreditEntryDTO {
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	return storeCreditEntryDTO{
		Reference: "ret_1",
		Reason:    "credit instead of a refund",
		ExpiresAt: &expires,
		OrderID:   "order_1",
		CreatedAt: time.Now().UTC(),
	}
}

// fullCollection produces a collection record whose omitempty fields are
// written too.
func fullCollection() collectionDTO {
	return collectionDTO{Metadata: map[string]any{"k": "v"}}
}

// fullSession produces a payment session whose omitempty fields are written
// too.
func fullSession() sessionDTO {
	return sessionDTO{
		Data:          json.RawMessage(`{"k":"v"}`),
		DeclineReason: "insufficient_funds",
	}
}

// fullPayment produces a capture record.
//
// [paymentDTO] has no omitempty field; the time field is filled in anyway so
// that the sample looks like a real response.
func fullPayment() paymentDTO {
	return paymentDTO{CapturedAt: time.Now().UTC()}
}

// fullRefund produces a refund record whose omitempty fields are written too.
func fullRefund() refundDTO {
	return refundDTO{Reason: "customer refund", Reference: "ret_1"}
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

			assertRequestBody(t, components, op, endpoint)

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok, "the code the handler REALLY writes has to be documented: %s", endpoint.status)

			if endpoint.bodiless() {
				assert.NotContains(t, definition, "content",
					"a 204 has no body; the schema must not promise one")

				return
			}

			record := envelopeRecord(t, components, bodySchema(t, definition), endpoint.list)

			if endpoint.primitiveItem != "" {
				assert.Equal(t, endpoint.primitiveItem, record["type"],
					"the list item has to be of a primitive type; saying object produces the wrong class in the client")

				return
			}

			assert.ElementsMatch(t, jsonKeys(t, endpoint.response), fieldNames(t, components, record),
				"the fields of the response record have to match the DTO")
			assert.ElementsMatch(t, jsonKeys(t, zeroValue(endpoint.response)),
				requiredNames(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// assertRequestBody verifies the endpoint's request body contract.
//
// Whether the body is REQUIRED is tested too: on the capture endpoint sending
// no body is valid and means "the whole". Had the schema said required, the
// client generator would force the caller to build an empty object only because
// the schema says so.
func assertRequestBody(t *testing.T, components, op map[string]any, endpoint endpointExpectation) {
	t.Helper()

	definition, hasBody := op["requestBody"].(map[string]any)
	require.Equal(t, endpoint.request != nil, hasBody,
		"an endpoint that takes a body has to have a requestBody, one that does not must not")

	if endpoint.request == nil {
		return
	}

	assert.Equal(t, !endpoint.bodyOptional, definition["required"],
		"whether the body is required has to match the handler's behavior")

	schema := bodySchema(t, definition)
	assert.ElementsMatch(t, jsonKeys(t, endpoint.request), fieldNames(t, components, schema),
		"the fields of the request body have to match the DTO")
}

// envelopeRecord returns the RECORD schema the response envelope carries.
//
// The envelope itself is tested too: mixing up the single and the list envelope
// means a wrong return type in the client generator — a caller expecting the
// paging fields gets a single record, or the other way around.
func envelopeRecord(t *testing.T, components, envelope map[string]any, list bool) map[string]any {
	t.Helper()

	expected := []string{envelopeDataField}
	if list {
		expected = []string{envelopeDataField, "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, expected, fieldNames(t, components, envelope), "response envelope")

	record, ok := properties(t, components, envelope)[envelopeDataField].(map[string]any)
	require.True(t, ok)

	if !list {
		return record
	}

	assert.Equal(t, "array", record["type"], "the data field of a list envelope has to be an array")

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

// TestAmountFieldsAreMinorUnitIntegers verifies that every field that carries
// money is described as an integer.
//
// The concrete fault is this: had the schema shown the amount as "number", the
// client developer would send 100.50 and the server could not decode the body,
// or — worse — the floating point would be rounded somewhere. Money never
// touches floating point at any stage (plan Section 8), and without
// "format: int64" JavaScript corrupts the value SILENTLY beyond 2^53.
//
// The fields are found by their NAMES, not by a hand-written list: on the day a
// new amount field is added the list would fall short and the test would not
// see it.
func TestAmountFieldsAreMinorUnitIntegers(t *testing.T) {
	t.Parallel()

	_, components := document(t)

	counted := 0

	for name, raw := range components {
		schema, ok := raw.(map[string]any)
		require.True(t, ok, "the %q component has to be an object", name)

		props, present := schema["properties"].(map[string]any)
		if !present {
			continue
		}

		for field, fieldSchema := range props {
			if !isAmountField(field) {
				continue
			}

			m, ok := fieldSchema.(map[string]any)
			require.True(t, ok)

			assert.Equal(t, amountType, amountTypeOf(m), "%s.%s has to be an integer", name, field)
			assert.Equal(t, "int64", m["format"], "%s.%s has to be int64", name, field)

			counted++
		}
	}

	assert.Positive(t, counted, "at least one amount field has to be described")
}

// amountType is the JSON Schema type a field that carries money has to carry.
const amountType = "integer"

// amountTypeOf returns the type of the field schema, peeling off the NULLABLE
// wrapper.
//
// A pointer field ("amount *int64") comes out in the schema as
// ["integer","null"], and that is as deliberate as the wrapper itself: inside
// [createCollectionRequest] an amount that was not sent and an amount of zero
// that was sent are SEPARATE things, and each is rejected with its own message.
// The test's claim is "money does not touch floating point"; a nullable integer
// does not break that claim, "number" or "string" does.
//
// The version that did not peel the wrapper was GREEN until 2026-09-07, because
// the only pointer field carrying an amount was inside a type that never entered
// the document at all, because of a component name collision (ADR 0036). So the
// test was right and the set it measured was incomplete.
func amountTypeOf(schema map[string]any) any {
	switch typ := schema["type"].(type) {
	case []any:
		for _, name := range typ {
			if name != "null" {
				return name
			}
		}

		return typ
	default:
		return typ
	}
}

// TestAmountTypeOfPeelsTheWrapperButLetsNoWrongTypeTHROUGH holds the helper
// itself.
//
// The helper is the ONLY decision point of the test above it: a wrongly
// written version of it — one that never looks inside the wrapper and returns
// "integer", for example — leaves that test green, and an amount field typed
// ["number","null"] gets through unnoticed. I measured this with a mutation:
// such a version COMPILES and no test made a sound.
//
// A test's decision point needs as much proof as the thing it measures.
func TestAmountTypeOfPeelsTheWrapperButLetsNoWrongTypeTHROUGH(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "integer", amountTypeOf(map[string]any{"type": "integer"}),
		"a type without a wrapper has to come back as it is")
	assert.Equal(t, "integer", amountTypeOf(map[string]any{"type": []any{"integer", "null"}}),
		"the nullable wrapper of a pointer field has to be peeled off")
	assert.Equal(t, "number", amountTypeOf(map[string]any{"type": []any{"number", "null"}}),
		"if the INSIDE of the wrapper is wrong the helper must NOT HIDE it; the version "+
			"that hides it compiles and leaves the test above green")
	assert.Equal(t, "string", amountTypeOf(map[string]any{"type": "string"}))
}

// TestAmountCarryingEndpointsStateTheUnit verifies that every endpoint that
// carries an amount states the unit EXPLICITLY.
//
// "integer" on its own does not state the unit: a client developer who sees
// they cannot send 100,50 TL may try sending 100 or 101. The right answer is
// 10050, and the only thing that says so is the description.
//
// Which endpoints have to carry the note is derived FROM THE SCHEMA, not from
// the table: every endpoint with an amount field needs the note, and when a new
// endpoint is added this applies on its own.
func TestAmountCarryingEndpointsStateTheUnit(t *testing.T) {
	t.Parallel()

	paths, components := document(t)

	for _, endpoint := range describedEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := operation(t, paths, endpoint.method, endpoint.path)
			if !endpointCarriesAmount(t, components, op, endpoint) {
				return
			}

			description, _ := op["description"].(string)
			assert.Contains(t, description, "MINOR UNIT",
				"an endpoint that carries an amount has to state its unit")
			assert.Contains(t, description, "kurus/cent",
				"the unit has to be written with the word the client developer knows")
		})
	}
}

// endpointCarriesAmount reports whether the endpoint's request or response
// record has an amount field.
func endpointCarriesAmount(t *testing.T, components, op map[string]any, endpoint endpointExpectation) bool {
	t.Helper()

	if definition, present := op["requestBody"].(map[string]any); present {
		if schemaCarriesAmount(t, components, bodySchema(t, definition)) {
			return true
		}
	}

	if endpoint.bodiless() || endpoint.primitiveItem != "" {
		return false
	}

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses[endpoint.status].(map[string]any)
	require.True(t, ok)

	return schemaCarriesAmount(t, components,
		envelopeRecord(t, components, bodySchema(t, definition), endpoint.list))
}

// schemaCarriesAmount reports whether the schema has an amount field directly.
func schemaCarriesAmount(t *testing.T, components, schema map[string]any) bool {
	t.Helper()

	for field := range properties(t, components, schema) {
		if isAmountField(field) {
			return true
		}
	}

	return false
}

// isAmountField reports whether the field name carries money.
//
// "balance" was added on 2026-09-12, and the reason it was added is the rule
// itself: this test's sentence is "every endpoint that carries an AMOUNT", and a
// balance is an amount too — a minor unit integer. The version that built the
// population from "amount" alone silently left store credit's balance endpoint
// out; that was an instance of the gate being NARROWER than the sentence it
// states, not an exemption.
//
// "points" joined for the same reason on 2026-09-24. ADR 0165 made a point ONE
// MINOR UNIT of the currency it was earned in, so the loyalty endpoints carry
// an amount under a name this function did not know, and their descriptions had
// said "minor unit" in lower case without the word a client developer knows.
func isAmountField(name string) bool {
	return name == "amount" || strings.HasSuffix(name, "_amount") ||
		name == "balance" || strings.HasSuffix(name, "_balance") ||
		name == "points"
}
