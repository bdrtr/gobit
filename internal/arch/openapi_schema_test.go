package arch_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// resolveSchema resolves "$ref" references to the component in the document.
func resolveSchema(t *testing.T, doc *openapi.Doc, schema map[string]any) map[string]any {
	t.Helper()

	ref, isRef := schema["$ref"].(string)
	if !isRef {
		return schema
	}

	target, exists := doc.Schemas()[strings.TrimPrefix(ref, "#/components/schemas/")]
	require.True(t, exists, "the %q component has to be registered", ref)

	m, ok := target.(map[string]any)
	require.True(t, ok, "the %q component has to be an object", ref)

	return m
}

// schemaProperties returns the schema's "properties" map.
func schemaProperties(t *testing.T, doc *openapi.Doc, schema map[string]any) map[string]any {
	t.Helper()

	properties, ok := resolveSchema(t, doc, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	return properties
}

// keysOf returns the keys of a map.
func keysOf[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	return names
}

// jsonKeySet encodes the value with encoding/json and returns its keys.
func jsonKeySet(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	return keysOf(decoded)
}

// TestTheStorefrontProductSchemaDescribesTheRealType verifies that the
// reflection layer gives the same result as encoding/json on a REAL module type.
//
// The core's own tests CANNOT import modules (Principle 2.4) and work with a
// copy of the shape. The copy is right on the day it is copied; it then goes
// stale silently when the real type changes. This test closes that gap and
// lives in the arch package because that is the only place that is test-only:
// it can import both the core and the modules.
//
// [productsvc.StoreProduct] was chosen deliberately: it carries the embedded
// models.Product and SHADOWS its Variants field. encoding/json does not write
// the shadowed field; the schema must not write it either. Were it to, a client
// generator would produce the variants with the type carrying NO price/stock
// information and the storefront client could never see the price.
func TestTheStorefrontProductSchemaDescribesTheRealType(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	schema := doc.SchemaOf(productsvc.StoreProduct{})
	properties := schemaProperties(t, doc, schema)

	// At the zero value the omitempty fields are never written to JSON; the keys
	// left over are exactly "the ones always written", that is, "required".
	required, ok := resolveSchema(t, doc, schema)["required"].([]string)
	require.True(t, ok, "the schema has to have required")

	// No SEPARATE guard was written against an empty key set, and that is
	// deliberate: even if the type lost its fields, the "the schema has to have
	// required" assertion above fails (measured — the schema of an empty struct
	// produces no required key at all), that is, there is no path quietly
	// matching two empty sets. A second gate would have been put in front of a
	// closed door.
	written := jsonKeySet(t, productsvc.StoreProduct{})
	assert.ElementsMatch(t, written, required,
		"required has to be the same as the keys encoding/json ALWAYS writes")

	for _, name := range written {
		assert.Contains(t, properties, name, "the %q field has to be in the schema", name)
	}
}

// TestTheStorefrontProductVariantsCarryTheShadowingType verifies that the
// shadowing is right at the TYPE level too.
//
// Comparing key sets is not enough here: both the shadowed models.Product field
// and the shadowing StoreProduct field carry the name "variants", that is,
// picking the wrong one does not break the key set. The only thing that tells
// them apart is the item type: only the enriched variant carries price and stock.
func TestTheStorefrontProductVariantsCarryTheShadowingType(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	properties := schemaProperties(t, doc, doc.SchemaOf(productsvc.StoreProduct{}))

	variants, ok := properties["variants"].(map[string]any)
	require.True(t, ok, "the storefront product has to carry variants")
	assert.Equal(t, "array", variants["type"])

	item, ok := variants["items"].(map[string]any)
	require.True(t, ok)

	itemFields := schemaProperties(t, doc, item)
	assert.Contains(t, itemFields, "price_set",
		"the variant schema has to come from the enriched type; the shadowed models.Variant carries no price")
	assert.Contains(t, itemFields, "inventory_item")

	// The fields of the embedded models.Variant have to be FLATTENED too; only
	// the additions showing up would mean the base variant information was lost.
	assert.Contains(t, itemFields, "sku")
}
