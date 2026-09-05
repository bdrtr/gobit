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
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The test is in the INTERNAL package because the BODIES of the admin endpoints
// are unexported ([createProductRequest], [deleted], [productSalesChannels] …).
// The only way to exercise them from the outside would be to export the types;
// widening the module's surface for the sake of exercising the document would
// break the very thing being exercised. The test of the storefront endpoints
// works with exported types, so it lives in a SEPARATE file and in the api_test
// package.

// adminDoc produces Describe's output against the REAL route tree and returns it
// as read back from JSON.
//
// Looking at [openapi.Doc.Build]'s output directly would not have been enough:
// the operations are Go structs there and the behavior under examination is
// exactly whether the fields get written into JSON. The router has to be real
// too — the moment the description's path drifts from the route's, let the test
// fail, not somebody looking at /openapi.json in production.
func adminDoc(t *testing.T) (paths, components map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil, graph.Options{}).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err,
		"the document could not be produced; two types asking for the same component name gives this error too")
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

// adminOperation returns a single path+method operation from the document.
func adminOperation(t *testing.T, paths map[string]any, method, path string) map[string]any {
	t.Helper()

	pathOperations, ok := paths[path].(map[string]any)
	require.True(t, ok, "%s has to be in the document", path)

	op, ok := pathOperations[strings.ToLower(method)].(map[string]any)
	require.True(t, ok, "%s %s has to be in the document", method, path)

	return op
}

// adminResolveSchema resolves "$ref" references to the component in the
// document.
func adminResolveSchema(t *testing.T, components, schema map[string]any) map[string]any {
	t.Helper()

	ref, isRef := schema["$ref"].(string)
	if !isRef {
		return schema
	}

	target, ok := components[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	require.True(t, ok, "the %q component has to be registered", ref)

	return target
}

// adminBodySchema extracts the JSON schema out of a request or response
// definition.
func adminBodySchema(t *testing.T, definition map[string]any) map[string]any {
	t.Helper()

	content, ok := definition["content"].(map[string]any)
	require.True(t, ok, "a body definition has to have content: %#v", definition)

	json_, ok := content["application/json"].(map[string]any)
	require.True(t, ok, "the body has to be application/json")

	schema, ok := json_["schema"].(map[string]any)
	require.True(t, ok, "the body has to have a schema")

	return schema
}

// adminFields returns the "properties" keys of the schema.
func adminFields(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	properties, ok := adminResolveSchema(t, components, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return adminKeys(properties)
}

// adminRequired returns the "required" list of the schema.
func adminRequired(t *testing.T, components, schema map[string]any) []string {
	t.Helper()

	raw, _ := adminResolveSchema(t, components, schema)["required"].([]any)

	names := make([]string, 0, len(raw))
	for _, name := range raw {
		text, ok := name.(string)
		require.True(t, ok)

		names = append(names, text)
	}

	return names
}

// adminKeys returns the keys of a map.
func adminKeys[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// adminJSONKeys encodes the value with encoding/json and returns its keys.
//
// This is the other end of the comparison: the schema has to describe what is
// REALLY on the wire, and the only thing that knows that is encoding/json
// itself.
func adminJSONKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return adminKeys(decoded)
}

// adminZeroValue returns the zero value of the given sample's type.
//
// The keys written to JSON at the zero value are exactly "the ones always
// written", that is, the schema's "required" set. It is derived from the type
// rather than writing the sample out a second time by hand: had a field been
// forgotten between the two samples, the test would fail for the wrong reason.
func adminZeroValue(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// adminEndpoint is the contract of a single described /admin/v1 endpoint.
type adminEndpoint struct {
	method string
	path   string
	// status is the REAL status code of the successful response; it has to be
	// the same as the code the handler writes (see admin.go). On the admin side
	// 204 is NEVER used: the deletion endpoints write a [deleted] record with a
	// 200 as well.
	status string
	// request is a sample of the request body's type; if nil the endpoint takes
	// no body.
	request any
	// record is a sample carrying all the fields of the RECORD in the
	// successful response.
	record any
	// list reports whether the response comes back with the list envelope
	// (writeList) or with the single envelope (writeItem); the two are different
	// schemas and mixing them up would produce the wrong type in a client
	// generator.
	list bool
}

// key returns the operation's "METHOD path" identity.
func (u adminEndpoint) key() string { return u.method + " " + u.path }

// adminEndpoints are the expectations of the described admin endpoints.
//
// The record samples are FILLED IN: every field carrying omitempty/omitzero gets
// a value different from the zero one, because the comparison has the shape
// "the schema's properties set = the encoded key set" and an empty sample would
// never write those fields. For the request bodies the zero value is enough: no
// request DTO carries omitempty.
func adminEndpoints() []adminEndpoint {
	return []adminEndpoint{
		{
			method: http.MethodPost, path: "/admin/v1/products", status: "201",
			request: createProductRequest{}, record: filledAdminProduct(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/products", status: "200",
			record: filledAdminProduct(), list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/products/{id}", status: "200",
			record: filledAdminProduct(),
		},
		{
			method: http.MethodPatch, path: "/admin/v1/products/{id}", status: "200",
			request: updateProductRequest{}, record: filledAdminProduct(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/products/{id}", status: "200",
			record: deleted{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/products/{id}/variants", status: "201",
			request: createVariantRequest{}, record: filledAdminVariant(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/products/{id}/variants", status: "200",
			record: filledAdminVariant(), list: true,
		},
		{
			method: http.MethodGet, path: "/admin/v1/variants/{id}", status: "200",
			record: filledAdminVariant(),
		},
		{
			method: http.MethodPatch, path: "/admin/v1/variants/{id}", status: "200",
			request: updateVariantRequest{}, record: filledAdminVariant(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/variants/{id}", status: "200",
			record: deleted{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/products/{id}/options", status: "201",
			request: createOptionRequest{}, record: filledAdminOption(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/products/{id}/options", status: "200",
			record: filledAdminOption(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/product-options/{id}/values", status: "201",
			request: optionValueRequest{}, record: filledOptionValue(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/product-options/{id}", status: "200",
			record: deleted{},
		},
		{
			method: http.MethodPut, path: "/admin/v1/variants/{id}/price-set", status: "200",
			request: linkRequest{}, record: filledVariantLinks(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/variants/{id}/price-set", status: "200",
			record: deleted{},
		},
		{
			method: http.MethodPut, path: "/admin/v1/variants/{id}/inventory-item", status: "200",
			request: linkRequest{}, record: filledVariantLinks(),
		},
		{
			method: http.MethodDelete, path: "/admin/v1/variants/{id}/inventory-item", status: "200",
			record: deleted{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/variants/{id}/links", status: "200",
			record: filledVariantLinks(),
		},
		{
			method: http.MethodPost, path: "/admin/v1/products/{id}/sales-channels", status: "200",
			request: linkSalesChannelRequest{}, record: productSalesChannels{},
		},
		{
			method: http.MethodDelete,
			path:   "/admin/v1/products/{id}/sales-channels/{sales_channel_id}",
			status: "200", record: productSalesChannels{},
		},
		{
			method: http.MethodGet, path: "/admin/v1/products/{id}/sales-channels", status: "200",
			record: productSalesChannels{},
		},
		{
			method: http.MethodPost, path: "/admin/v1/product-collections", status: "201",
			request: createCollectionRequest{}, record: filledCollection(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/product-collections", status: "200",
			record: filledCollection(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/product-categories", status: "201",
			request: createCategoryRequest{}, record: filledCategory(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/product-categories", status: "200",
			record: filledCategory(), list: true,
		},
		{
			method: http.MethodPost, path: "/admin/v1/product-tags", status: "201",
			request: createTagRequest{}, record: filledTag(),
		},
		{
			method: http.MethodGet, path: "/admin/v1/product-tags", status: "200",
			record: filledTag(), list: true,
		},
	}
}

// filledAdminProduct produces an admin product whose omitempty fields are
// written too.
//
// Unlike the storefront product, the related records are NOT SHADOWED: the admin
// response is [models.Product] itself and its "variants" field carries the
// unenriched [models.Variant].
func filledAdminProduct() models.Product {
	text := "x"
	number := int32(1)
	now := time.Now().UTC()

	return models.Product{
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
		Variants:      []models.Variant{{}},
		Options:       []models.Option{{}},
		Images:        []models.Image{{}},
		Tags:          []models.Tag{{}},
		Categories:    []models.Category{{}},
	}
}

// filledAdminVariant produces a variant whose omitempty fields are written too.
func filledAdminVariant() models.Variant {
	text := "x"
	number := int32(1)
	now := time.Now().UTC()

	return models.Variant{
		SKU:          &text,
		Barcode:      &text,
		EAN:          &text,
		UPC:          &text,
		Weight:       &number,
		Metadata:     map[string]any{"k": "v"},
		DeletedAt:    &now,
		OptionValues: []models.OptionValue{{}},
	}
}

// filledAdminOption produces an option whose omitempty fields are written too.
func filledAdminOption() models.Option {
	now := time.Now().UTC()

	return models.Option{DeletedAt: &now, Values: []models.OptionValue{{}}}
}

// filledOptionValue produces an option value whose omitempty and omitzero fields
// are written too.
func filledOptionValue() models.OptionValue {
	now := time.Now().UTC()

	return models.OptionValue{
		OptionTitle: "Size",
		CreatedAt:   now,
		UpdatedAt:   now,
		DeletedAt:   &now,
	}
}

// filledVariantLinks produces a variant link record with both links filled in.
func filledVariantLinks() service.VariantLinks {
	text := "x"

	return service.VariantLinks{PriceSetID: &text, InventoryItemID: &text}
}

// filledCollection produces a collection whose omitempty fields are written too.
func filledCollection() models.Collection {
	now := time.Now().UTC()

	return models.Collection{Metadata: map[string]any{"k": "v"}, DeletedAt: &now}
}

// filledCategory produces a category whose omitempty and omitzero fields are
// written too.
func filledCategory() models.Category {
	text := "x"
	now := time.Now().UTC()

	return models.Category{
		Description: &text,
		ParentID:    &text,
		CreatedAt:   now,
		UpdatedAt:   now,
		DeletedAt:   &now,
	}
}

// filledTag produces a tag whose omitzero fields are written too.
func filledTag() models.Tag {
	now := time.Now().UTC()

	return models.Tag{CreatedAt: now, UpdatedAt: now, DeletedAt: &now}
}

// TestAdminEndpointsDescribeTheirBodies verifies that every admin endpoint says
// what it TAKES and what it RETURNS.
//
// This is the exact counterpart of the finding: a schema with no body tells the
// client "this endpoint exists and can fail like this", it does not say what to
// send; and a client generator produces a method with no body whose return type
// is 'void' — that is, no product CAN BE CREATED with that client.
//
// The field sets are compared against the DTO's encoding/json output, not
// against a hand-written list: a hand-written list falls short the day a field
// is added to the type, and the test would not see it.
func TestAdminEndpointsDescribeTheirBodies(t *testing.T) {
	t.Parallel()

	paths, components := adminDoc(t)

	for _, endpoint := range adminEndpoints() {
		t.Run(endpoint.key(), func(t *testing.T) {
			t.Parallel()

			op := adminOperation(t, paths, endpoint.method, endpoint.path)
			assert.NotEmpty(t, op["summary"], "the endpoint has to be described in one line")

			requestDefinition, hasBody := op["requestBody"].(map[string]any)
			require.Equal(t, endpoint.request != nil, hasBody,
				"an endpoint that takes a body has to have a requestBody, one that does not must not")

			if endpoint.request != nil {
				schema := adminBodySchema(t, requestDefinition)
				assert.ElementsMatch(t, adminJSONKeys(t, endpoint.request),
					adminFields(t, components, schema),
					"the fields of the request body have to overlap with the REAL DTO")
			}

			responses, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			definition, ok := responses[endpoint.status].(map[string]any)
			require.True(t, ok,
				"the code the handler REALLY writes has to be documented: %s", endpoint.status)

			// A listing that takes a cursor has to give one back, and one that
			// does not must not document a field it never writes. Reading the
			// answer off the operation's own parameters ties the two halves
			// together instead of repeating the decision in a second table.
			cursored := slices.Contains(adminParameterNames(t, op, "query"), "after")

			record := adminRecordSchema(t, components, adminBodySchema(t, definition),
				endpoint.list, cursored)

			assert.ElementsMatch(t, adminJSONKeys(t, endpoint.record),
				adminFields(t, components, record),
				"the fields of the response record have to overlap with the REAL type")
			assert.ElementsMatch(t, adminJSONKeys(t, adminZeroValue(endpoint.record)),
				adminRequired(t, components, record),
				"required has to be the same as the keys encoding/json ALWAYS writes")
		})
	}
}

// adminRecordSchema returns the RECORD schema inside the envelope and verifies
// the envelope's shape.
//
// Telling the list envelope apart from the single one is half of the test's job:
// both carry "data", but in the list that field is an ARRAY. Writing the wrong
// one would produce a method in a client generator that takes a single record
// for an array (or an array for a single record).
func adminRecordSchema(
	t *testing.T, components, envelope map[string]any, list, cursored bool,
) map[string]any {
	t.Helper()

	expected := []string{"data"}
	if list {
		expected = []string{"data", "count", "offset", "limit"}
	}
	if cursored {
		expected = append(expected, "next_cursor")
	}

	assert.ElementsMatch(t, expected, adminFields(t, components, envelope),
		"the envelope has to have the shape from plan Section 8")

	properties, ok := adminResolveSchema(t, components, envelope)["properties"].(map[string]any)
	require.True(t, ok)

	data, ok := properties["data"].(map[string]any)
	require.True(t, ok)

	if !list {
		return data
	}

	assert.Equal(t, "array", data["type"], "the data field of the list envelope is an array")

	item, ok := data["items"].(map[string]any)
	require.True(t, ok, "the list envelope has to have an item schema")

	return item
}

// TestEveryAdminEndpointIsDescribed verifies that no admin endpoint has been
// left undescribed.
//
// When a new endpoint is added and not described, this test fails. Otherwise the
// failure would be SILENT: the endpoint appears in the document with its path
// and its security, only what it takes and what it returns is unknown.
func TestEveryAdminEndpointIsDescribed(t *testing.T) {
	t.Parallel()

	paths, _ := adminDoc(t)

	var found []string

	for path, operations := range paths {
		if !strings.HasPrefix(path, "/admin/v1") {
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

	expected := make([]string, 0, len(adminEndpoints()))
	for _, endpoint := range adminEndpoints() {
		expected = append(expected, endpoint.key())
	}

	assert.ElementsMatch(t, expected, found,
		"an admin endpoint that is not in the table means it is not exercised")
}

// TestAdminEndpointsDescribeOnlyParametersTheyRead verifies that the query
// parameters are the same as the ones the handler REALLY reads.
//
// Putting a parameter that is not read into the schema is promising the client a
// feature that DOES NOT WORK: the generator puts an argument on the method, the
// caller fills it in, the server silently ignores it. The reverse direction is
// just as expensive — a parameter that is read but not described is a filter the
// client can never reach.
func TestAdminEndpointsDescribeOnlyParametersTheyRead(t *testing.T) {
	t.Parallel()

	paths, _ := adminDoc(t)

	// The expected sets come from the reading calls in admin.go; every admin
	// endpoint that is not in the list never looks at the query string.
	expected := map[string][]string{
		"GET /admin/v1/products": {
			"collection_id", "handle", "q", "status", "expand", "limit", "offset", "after",
		},
		"GET /admin/v1/products/{id}/variants": {"limit", "offset"},
		"GET /admin/v1/product-collections":    {"limit", "offset"},
		"GET /admin/v1/product-categories":     {"parent_id", "limit", "offset"},
		"GET /admin/v1/product-tags":           {"limit", "offset"},
	}

	for _, endpoint := range adminEndpoints() {
		op := adminOperation(t, paths, endpoint.method, endpoint.path)

		assert.ElementsMatch(t, expected[endpoint.key()], adminParameterNames(t, op, "query"),
			"the query parameters of %s have to be the same as the ones the handler reads", endpoint.key())
	}
}

// adminParameterNames returns the names of the operation's parameters in the
// given location.
func adminParameterNames(t *testing.T, op map[string]any, location string) []string {
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
