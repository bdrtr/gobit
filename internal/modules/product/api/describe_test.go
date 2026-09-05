package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// storefrontDoc produces Describe's output against the REAL route tree and
// returns it as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not have been enough:
// the operations are Go structs there and the behavior under examination is
// exactly whether the fields get written into JSON. The router has to be real
// too — the moment the description's path drifts from the route's, let the test
// fail, not somebody looking at /openapi.json in production.
func storefrontDoc(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	api.Describe(doc)

	r := chi.NewRouter()
	api.New(nil, graph.Options{}).Routes(r)

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

// storefrontOperation returns a single path+method operation from the document.
func storefrontOperation(t *testing.T, paths map[string]any, method, path string) map[string]any {
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

// responseSchema extracts the JSON schema out of a response definition.
func responseSchema(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	content, ok := definition["content"].(map[string]any)
	require.True(t, ok, "a response definition has to have content: %#v", definition)

	json_, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the response has to be application/json")

	schema, ok := json_["schema"].(map[string]any)
	require.True(t, ok, "the response has to have a schema")

	return schema
}

// property returns the schema of a single field of the schema.
func property(t *testing.T, components, schema map[string]any, name string) map[string]any {
	t.Helper()

	properties, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	field, ok := properties[name].(map[string]any)
	require.True(t, ok, "the %q field has to be in the schema", name)

	return field
}

// storefrontFields returns the "properties" keys of the schema.
func storefrontFields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return storefrontKeys(properties)
}

// storefrontRequired returns the "required" list of the schema.
func storefrontRequired(t *testing.T, components, schema map[string]any) []string {
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

// storefrontKeys returns the keys of a map.
func storefrontKeys[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// storefrontJSONKeys encodes the value with encoding/json and returns its keys.
//
// This is the other end of the comparison: the schema has to describe what is
// REALLY on the wire, and the only thing that knows that is encoding/json
// itself.
func storefrontJSONKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return storefrontKeys(decoded)
}

// filledStoreProduct produces a storefront product whose omitempty fields are
// written too.
//
// The Variants field of the embedded models.Product is DELIBERATELY left empty:
// it is the shadowed field and encoding/json never writes it. Had it been
// filled, the test could not have seen the shadowing break.
func filledStoreProduct() service.StoreProduct {
	text := "x"
	number := int32(1)
	now := time.Now().UTC()

	return service.StoreProduct{
		Product: models.Product{
			Subtitle:      &text,
			Description:   &text,
			Thumbnail:     &text,
			Weight:        &number,
			Length:        &number,
			Height:        &number,
			Width:         &number,
			Material:      &text,
			OriginCountry: &text,
			CollectionID:  &text,
			Metadata:      map[string]any{"k": "v"},
			DeletedAt:     &now,
			Options:       []models.Option{{}},
			Images:        []models.Image{{}},
			Tags:          []models.Tag{{}},
			Categories:    []models.Category{{}},
		},
		Variants: []service.StoreVariant{filledStoreVariant()},
	}
}

// filledStoreVariant produces a storefront variant whose omitempty fields are
// written too.
func filledStoreVariant() service.StoreVariant {
	text := "x"
	number := int32(1)
	now := time.Now().UTC()

	return service.StoreVariant{
		Variant: models.Variant{
			SKU:          &text,
			Barcode:      &text,
			EAN:          &text,
			UPC:          &text,
			Weight:       &number,
			Metadata:     map[string]any{"k": "v"},
			DeletedAt:    &now,
			OptionValues: []models.OptionValue{{}},
		},
		PriceSet:      query.Record{"id": "pset_1"},
		InventoryItem: query.Record{"id": "iitem_1"},
	}
}

// TestStoreListDescribesItsBody verifies that the listing endpoint says what it
// returns.
//
// The field set is compared against the DTO's encoding/json output, not against
// a hand-written list: a hand-written list falls short the day a field is added
// to the type, and the test would not see it.
func TestStoreListDescribesItsBody(t *testing.T) {
	t.Parallel()

	paths, components := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody", "a GET endpoint takes no body")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	// The listing returns a 200 (see writeList); writing 201 would produce the
	// wrong branch in a client generator.
	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok, "the code the handler REALLY writes has to be documented")

	envelope := responseSchema(t, definition)
	// "next_cursor" is here because this listing accepts "after"; the two are
	// one decision, and a listing that took a cursor without giving one back
	// would leave a client unable to reach page two.
	assert.ElementsMatch(t, []string{"data", "count", "offset", "limit", "next_cursor"},
		storefrontFields(t, components, envelope), "the list envelope is the shape from plan Section 8")

	item, ok := property(t, components, envelope, "data")["items"].(map[string]any)
	require.True(t, ok, "the list envelope has to have an item schema")

	assertProductSchema(t, components, item)
}

// TestStoreItemEndpointDescribesItsBody verifies that the single endpoint says
// what it returns.
func TestStoreItemEndpointDescribesItsBody(t *testing.T) {
	t.Parallel()

	paths, components := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products/{id}")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody", "a GET endpoint takes no body")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok, "the code the handler REALLY writes has to be documented")

	envelope := responseSchema(t, definition)
	assert.ElementsMatch(t, []string{"data"}, storefrontFields(t, components, envelope),
		"single responses come back with the {\"data\": …} envelope")

	assertProductSchema(t, components, property(t, components, envelope, "data"))
}

// assertProductSchema verifies that the product schema overlaps with the
// storefront type.
func assertProductSchema(t *testing.T, components, schema map[string]any) {
	t.Helper()

	assert.ElementsMatch(t, storefrontJSONKeys(t, filledStoreProduct()),
		storefrontFields(t, components, schema), "the product fields have to overlap with the storefront type")

	// The keys written at the zero value are exactly "the ones always written".
	assert.ElementsMatch(t, storefrontJSONKeys(t, service.StoreProduct{}),
		storefrontRequired(t, components, schema),
		"required has to be the same as the keys encoding/json ALWAYS writes")
}

// TestStoreVariantsDescribeEnrichedType verifies that the shadowed field appears
// in the schema with the RIGHT type.
//
// service.StoreProduct SHADOWS the Variants field of the embedded models.Product
// and encoding/json writes only the shadowing one. Had the schema described the
// shadowed models.Variant, a client generator would produce the variants with a
// type that HAS NO price and stock information, that is, the storefront client
// could never see the price — and, since the key set is not disturbed
// ("variants" exists in both types), this would happen silently. The only thing
// that tells them apart is the ITEM TYPE, and the test compares exactly that.
func TestStoreVariantsDescribeEnrichedType(t *testing.T) {
	t.Parallel()

	paths, components := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products/{id}")

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok)

	product := property(t, components, responseSchema(t, definition), "data")

	variants := property(t, components, product, "variants")
	assert.Equal(t, "array", variants["type"])

	item, ok := variants["items"].(map[string]any)
	require.True(t, ok)

	fields := storefrontFields(t, components, item)
	assert.ElementsMatch(t, storefrontJSONKeys(t, filledStoreVariant()), fields,
		"the variant schema has to come from the enriched type")
	assert.Contains(t, fields, "price_set", "the shadowed models.Variant carries no price")
	assert.Contains(t, fields, "inventory_item")
	// The fields of the embedded models.Variant have to be FLATTENED as well;
	// only the additions showing up would mean the base variant information had
	// been lost.
	assert.Contains(t, fields, "sku")
}

// TestStoreListDescribesOnlyParametersItReads verifies that the query
// parameters are the same as the ones the handler REALLY reads.
//
// Putting a parameter that is not read into the schema is promising the client a
// feature that DOES NOT WORK: the generator puts an argument on the method, the
// caller fills it in, the server silently ignores it.
//
// The absence of "sales_channel_id" is also a SECURITY statement: the channel
// comes from the request's publishable key, not from the query string. Writing
// it into the schema would hint to a client arriving with any key it happened to
// hold that it could ask for another channel's catalog.
func TestStoreListDescribesOnlyParametersItReads(t *testing.T) {
	t.Parallel()

	paths, _ := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products")

	names := parameterNames(t, op, "query")
	assert.ElementsMatch(t, []string{"collection_id", "q", "limit", "offset", "after", "with_count"}, names,
		"the parameters have to be the same as the ones storeListProducts reads")
	assert.NotContains(t, names, "sales_channel_id",
		"the channel comes from the identity; it must not be announced as a query parameter")
}

// TestStoreListDocumentsCountParameterDefault verifies that the DEFAULT of
// "with_count" can be read in the document.
//
// Not writing down the default of a cost switch is the exact violation of this
// repository's "silent default" ban: a client that sees the parameter in the
// document does not know what happens when it does not send it and can be wrong
// in both directions — it may think the counter is free, or think it never
// arrives.
//
// The CONSEQUENCE of turning it off has to be written down too: in the response
// envelope the field does not fall to 0, it does not become null, it IS NOT
// THERE AT ALL. A client that does not read this reads the "count" field
// directly and does arithmetic with undefined in JavaScript.
func TestStoreListDocumentsCountParameterDefault(t *testing.T) {
	t.Parallel()

	paths, _ := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products")

	// The case distinction is dropped: the text writes words in capitals for
	// emphasis and the claim is not about the emphasis but about the
	// INFORMATION.
	description := strings.ToLower(parameterDescription(t, op, "with_count"))

	assert.Contains(t, description, "true", "which value the default is has to be written down")
	assert.Contains(t, description, "absent",
		"that the field drops out of the body when it is turned off has to be written down")
	assert.Contains(t, description, "ms",
		"what the parameter buys has to be written down WITH A MEASUREMENT; a description "+
			"with no numbers leaves the reader unable to decide")
}

// TestStoreListCountIsNotRequired verifies that the response schema DOES NOT
// declare "count" required.
//
// The envelope schema is shared by every module and the counter is required
// there; the storefront listing is the only exception, because it is the only
// one that can be turned off. Had the schema said required, the document would
// promise a field that does not exist in a "with_count=false" response and a
// generated client would fall over while reading it. The schema of the other
// endpoints is exercised together with it in this test: only that way can it be
// seen that the relaxation belongs ONLY to this endpoint.
func TestStoreListCountIsNotRequired(t *testing.T) {
	t.Parallel()

	paths, components := storefrontDoc(t)

	storefront := listEnvelopeSchema(t, components, storefrontOperation(t, paths, http.MethodGet, "/store/v1/products"))
	assert.NotContains(t, requiredFields(t, storefront), "count",
		"the counter can drop in the storefront listing; it must not be declared required")
	assert.Contains(t, objectKeys(storefront["properties"]), "count",
		"the field MUST NOT BE REMOVED from the schema; being droppable is not the same as being gone")

	admin := listEnvelopeSchema(t, components, storefrontOperation(t, paths, http.MethodGet, "/admin/v1/products"))
	assert.Contains(t, requiredFields(t, admin), "count",
		"the admin listing always counts; its schema must not be relaxed")
}

// listEnvelopeSchema returns the list envelope schema in an operation's 200
// response.
func listEnvelopeSchema(t *testing.T, components, op map[string]any) map[string]any {
	t.Helper()

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok)

	return resolveSchema(t, components, responseSchema(t, definition))
}

// requiredFields returns the "required" list of the schema.
func requiredFields(t *testing.T, schema map[string]any) []string {
	t.Helper()

	raw, ok := schema["required"].([]any)
	require.True(t, ok, "the schema has to have required: %#v", schema)

	names := make([]string, 0, len(raw))

	for _, v := range raw {
		s, ok := v.(string)
		require.True(t, ok)

		names = append(names, s)
	}

	return names
}

// objectKeys returns the keys of a JSON object.
func objectKeys(v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// parameterDescription returns the description of the operation's given query
// parameter.
func parameterDescription(t *testing.T, op map[string]any, name string) string {
	t.Helper()

	params, ok := op["parameters"].([]any)
	require.True(t, ok)

	for _, raw := range params {
		p, ok := raw.(map[string]any)
		require.True(t, ok)

		if p["name"] != name {
			continue
		}

		description, ok := p["description"].(string)
		require.True(t, ok, "the %q parameter has to have a description", name)

		return description
	}

	require.Failf(t, "no such parameter", "the %q parameter was not found in the document", name)

	return ""
}

// TestStoreItemEndpointDescribesPathParameter verifies that it is written down
// that the path parameter accepts a handle too.
//
// The deriver cannot say this by looking at the pattern: the name "{id}" only
// implies an id, whereas storefront addresses carry a handle.
func TestStoreItemEndpointDescribesPathParameter(t *testing.T) {
	t.Parallel()

	paths, _ := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodGet, "/store/v1/products/{id}")

	assert.Empty(t, parameterNames(t, op, "query"), "the single endpoint does not read the query string")
	assert.Equal(t, []string{"id"}, parameterNames(t, op, "path"))

	params, ok := op["parameters"].([]any)
	require.True(t, ok)
	require.Len(t, params, 1)

	p, ok := params[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, p["description"], "handle")
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

// TestEveryStoreEndpointIsDescribed verifies that no storefront endpoint has
// been left undescribed.
//
// When a new endpoint is added and not described, this test fails. Otherwise the
// failure would be SILENT: the endpoint appears in the document with its path
// and its security, only what it returns is unknown.
func TestEveryStoreEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := storefrontDoc(t)

	var found []string

	for path, operations := range paths {
		if !strings.HasPrefix(path, "/store/v1") {
			continue
		}

		operationMap, ok := operations.(map[string]any)
		require.True(t, ok, "a path entry has to be a method map")

		for method, raw := range operationMap {
			op, ok := raw.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s has to be described", method, path)
			found = append(found, strings.ToUpper(method)+" "+path)
		}
	}

	assert.ElementsMatch(t, []string{
		"GET /store/v1/products",
		"GET /store/v1/products/{id}",
		// The GraphQL endpoint is part of the storefront too and it is
		// described; OpenAPI cannot describe its SCHEMA but it does describe its
		// path, its body and where the contract is (see
		// api.describeStorefrontGraphQL).
		"POST /store/v1/graphql",
	}, found)
}

// TestGraphQLEndpointDescribesItsBodies verifies that the GraphQL endpoint
// describes its request and response envelopes.
//
// The claim comes from two places. The first is the client generator: a POST
// whose body is not described turns into a method that cannot be called. The
// second is the end-to-end schema test (internal/e2e): the shape of the 2xx body
// of EVERY described endpoint has to be known, and that test only runs in a
// Docker-backed run — breaking here shows the problem without waiting for a
// container.
//
// The INSIDE of "data" is deliberately not described: the client's query decides
// its shape.
func TestGraphQLEndpointDescribesItsBodies(t *testing.T) {
	t.Parallel()

	paths, components := storefrontDoc(t)
	op := storefrontOperation(t, paths, http.MethodPost, "/store/v1/graphql")

	assert.NotEmpty(t, op["summary"])

	body, ok := op["requestBody"].(map[string]any)
	require.True(t, ok, "the GraphQL endpoint reads a body and has to say so")

	content, ok := body["content"].(map[string]any)
	require.True(t, ok)

	json_, ok := content["application/json"].(map[string]any)
	require.True(t, ok)

	request, ok := json_["schema"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"query", "operationName", "variables"},
		storefrontFields(t, components, request))

	responses, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	definition, ok := responses["200"].(map[string]any)
	require.True(t, ok, "GraphQL returns a 200 to a resolved request, field errors included")

	envelope := responseSchema(t, definition)
	assert.ElementsMatch(t, []string{"data", "errors"},
		storefrontFields(t, components, envelope), "the GraphQL response envelope is these two fields")
}
