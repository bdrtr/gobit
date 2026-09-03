package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// richVariant is the RICH type the shadowing field carries.
type richVariant struct {
	ID    string `json:"id"`
	Price int64  `json:"price"`
}

// baseRecord is the base type that gets embedded.
//
// It is UNEXPORTED and that is deliberate: encoding/json still walks the
// embedded form of an unexported type and writes the exported fields inside it.
// The schema has to as well; without that the client never sees the product id.
type baseRecord struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Variants   []string  `json:"variants"`
	Skipped    string    `json:"-"`
	unexported string    // an unexported field; it must NOT enter the schema
	Created    time.Time `json:"created_at"`
}

// shadowingRecord SHADOWS one field of the embedded type.
//
// The shape is internal/modules/product/service.StoreProduct itself: the
// embedded record's Variants field is shadowed by an enriched slice. The tie to
// the real type is in the test in internal/arch; the core tests CANNOT import
// the modules (Principle 2.4), so the shape is repeated here.
type shadowingRecord struct {
	baseRecord
	Variants []richVariant `json:"variants"`
}

// leftSide and rightSide are two embedded types wanting the same name at the SAME depth.
type leftSide struct {
	Shared string
}

// rightSide carries the same field as leftSide; neither is tagged.
type rightSide struct {
	Shared string
}

// ambiguousRecord inherits the same name from two embedded types.
//
// The candidate fields are equal in depth and in taggedness; encoding/json writes
// such a field NOT AT ALL (there is no winner) and the schema must not either.
type ambiguousRecord struct {
	leftSide
	rightSide
	Single string `json:"single"`
}

// taggedSide and untaggedSide want the SAME JSON name at the SAME depth, but
// only one of them is tagged.
//
// The tag differing from the Go field name is deliberate: the clash arises
// through the field's JSON NAME, not through its Go name.
type taggedSide struct {
	Tagged string `json:"Shared"`
}

// untaggedSide carries the same JSON name as taggedSide, untagged.
type untaggedSide struct {
	Shared string
}

// partlyAmbiguousRecord carries a single TAGGED candidate at equal depth.
//
// In encoding/json a tagged candidate resolves the ambiguity and the field is written.
type partlyAmbiguousRecord struct {
	taggedSide
	untaggedSide
}

// allTypes gathers the type family the reflection layer has to recognize.
type allTypes struct {
	Text       string          `json:"text"`
	Long       int64           `json:"long"`
	Short      int32           `json:"short"`
	Decimal    float64         `json:"decimal"`
	Flag       bool            `json:"flag"`
	Slice      []string        `json:"slice"`
	Map        map[string]any  `json:"map"`
	Time       time.Time       `json:"time"`
	Raw        json.RawMessage `json:"raw"`
	Pointer    *string         `json:"pointer"`
	TimePtr    *time.Time      `json:"time_ptr"`
	Optional   *int64          `json:"optional,omitempty"`
	Bytes      []byte          `json:"bytes"`
	Nested     richVariant     `json:"nested"`
	Skipped    string          `json:"-"`
	unexported string          // an unexported field; it must NOT enter the schema
}

// node is a self-referencing type; the schema generator must not fall into an
// endless loop here.
type node struct {
	ID       string `json:"id"`
	Children []node `json:"children"`
	Parent   *node  `json:"parent"`
}

// ClashingRecord carries the SAME name as openapi.ClashingRecord in the internal
// test file; the two are in separate packages.
type ClashingRecord struct {
	OtherField int `json:"other_field"`
}

// Error carries the same name as the core's shared error component.
type Error struct {
	Code string `json:"code"`
}

// filledAllTypes returns an instance with every field FILLED.
//
// The fields being filled matters: a field carrying omitempty is not written to
// JSON at all while empty, and the key-set comparison would never see it.
func filledAllTypes() allTypes {
	text := "value"
	number := int64(7)
	an := time.Unix(0, 0).UTC()

	return allTypes{
		Text:       "a",
		Long:       1,
		Short:      2,
		Decimal:    3.5,
		Flag:       true,
		Slice:      []string{"x"},
		Map:        map[string]any{"k": "v"},
		Time:       time.Unix(0, 0).UTC(),
		Raw:        json.RawMessage(`{"free":true}`),
		Pointer:    &text,
		TimePtr:    &an,
		Optional:   &number,
		Bytes:      []byte{1, 2, 3},
		Nested:     richVariant{ID: "v1", Price: 100},
		Skipped:    "must not be written",
		unexported: "must not be written",
	}
}

// document returns an empty document for schema generation.
func document() *openapi.Doc {
	return openapi.New("test", "v1")
}

// resolve resolves "$ref" references to the component in the document.
//
// [openapi.Doc.SchemaOf] returns a reference for named structs; what the test
// looks at is the reference's TARGET.
func resolve(t *testing.T, d *openapi.Doc, schema map[string]any) map[string]any {
	t.Helper()

	ref, isRef := schema["$ref"].(string)
	if !isRef {
		return schema
	}

	name := strings.TrimPrefix(ref, "#/components/schemas/")
	target, exists := d.Schemas()[name]
	require.True(t, exists, "the component %q has to be registered", name)

	m, ok := target.(map[string]any)
	require.True(t, ok, "the component %q has to be an object", name)

	return m
}

// fieldNames returns the schema's "properties" key set.
func fieldNames(t *testing.T, d *openapi.Doc, schema map[string]any) []string {
	t.Helper()

	properties, ok := resolve(t, d, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties: %#v", schema)

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}

	return names
}

// property returns the schema of a single field.
func property(t *testing.T, d *openapi.Doc, schema map[string]any, name string) map[string]any {
	t.Helper()

	properties, ok := resolve(t, d, schema)["properties"].(map[string]any)
	require.True(t, ok, "the schema has to have properties")
	require.Contains(t, properties, name)

	m, ok := properties[name].(map[string]any)
	require.True(t, ok, "the schema of the %q field has to be an object", name)

	return m
}

// requiredOf returns the schema's "required" list.
func requiredOf(t *testing.T, d *openapi.Doc, schema map[string]any) []string {
	t.Helper()

	raw, exists := resolve(t, d, schema)["required"]
	if !exists {
		return nil
	}

	list, ok := raw.([]string)
	require.True(t, ok, "required has to be a string slice")

	return list
}

// jsonKeys encodes the value with encoding/json and returns its keys.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))

	names := make([]string, 0, len(decoded))
	for name := range decoded {
		names = append(names, name)
	}

	return names
}

// TestSchemaFieldsMatchJSONEXACTLY is the reflection layer's STRONGEST exam.
//
// A sample value is encoded with encoding/json and the key set in the JSON is
// compared with the key set of the produced schema's "properties". One assertion;
// renaming through a tag, skipping with "-", unexported fields and SHADOWING all
// land here when they go wrong.
//
// The samples are given FILLED: a field carrying omitempty is not written to
// JSON while empty, and an empty sample would hide a field left over in the schema.
func TestSchemaFieldsMatchJSONEXACTLY(t *testing.T) {
	t.Parallel()

	examples := map[string]any{
		"a shadowing record": shadowingRecord{
			baseRecord: baseRecord{
				ID:         "p1",
				Title:      "T-shirt",
				Variants:   []string{"shadowed"},
				Skipped:    "must not be written",
				unexported: "must not be written",
				Created:    time.Unix(0, 0).UTC(),
			},
			Variants: []richVariant{{ID: "v1", Price: 100}},
		},
		"an ambiguous embedded field":    ambiguousRecord{Single: "t"},
		"a tagged candidate resolves it": partlyAmbiguousRecord{},
		"every type":                     filledAllTypes(),
		"a self-reference":               node{ID: "root", Children: []node{{ID: "leaf"}}},
	}

	for name, example := range examples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := document()

			assert.ElementsMatch(t, jsonKeys(t, example), fieldNames(t, d, d.SchemaOf(example)),
				"the schema's fields have to match the keys encoding/json writes")
		})
	}
}

// TestTheShadowedFieldCarriesTheRichType verifies that shadowing is right not
// only at the "the field appears once" level but at the TYPE level.
//
// The key-set comparison is not enough here: the shadowed field and the
// shadowing one both carry the name "variants", so picking the wrong one does
// not break the key set. The wrong pick means the client takes the variants for
// an array of strings.
func TestTheShadowedFieldCarriesTheRichType(t *testing.T) {
	t.Parallel()

	d := document()
	schema := d.SchemaOf(shadowingRecord{})

	variants := property(t, d, schema, "variants")
	assert.Equal(t, "array", variants["type"])

	item, ok := variants["items"].(map[string]any)
	require.True(t, ok)

	assert.ElementsMatch(t, []string{"id", "price"}, fieldNames(t, d, item),
		"the item type of the shadowing field has to be the rich variant, not the embedded string")
}

// TestAnAmbiguousEmbeddedFieldDoesNotEnterTheSchema verifies that a field
// clashing at equal depth and equal taggedness DROPS.
func TestAnAmbiguousEmbeddedFieldDoesNotEnterTheSchema(t *testing.T) {
	t.Parallel()

	d := document()

	assert.ElementsMatch(t, []string{"single"}, fieldNames(t, d, d.SchemaOf(ambiguousRecord{})),
		"encoding/json does not write an ambiguous field; the schema must not either")

	assert.ElementsMatch(t, []string{"Shared"}, fieldNames(t, d, d.SchemaOf(partlyAmbiguousRecord{})),
		"a single tagged candidate at equal depth resolves the ambiguity")
}

// TestUnexportedAndSkippedFieldsDoNotEnterTheSchema verifies that both ways of
// hiding drop out of the schema.
func TestUnexportedAndSkippedFieldsDoNotEnterTheSchema(t *testing.T) {
	t.Parallel()

	d := document()
	names := fieldNames(t, d, d.SchemaOf(filledAllTypes()))

	assert.NotContains(t, names, "Skipped", `a json:"-" field must not be in the schema`)
	assert.NotContains(t, names, "unexported", "an unexported field must not be in the schema")
}

// TestRequiredFieldsAreTheOnesWithoutOmitempty verifies that the "required" set
// is the keys encoding/json ALWAYS writes.
func TestRequiredFieldsAreTheOnesWithoutOmitempty(t *testing.T) {
	t.Parallel()

	d := document()
	schema := d.SchemaOf(allTypes{})

	required := requiredOf(t, d, schema)
	assert.Contains(t, required, "text")
	assert.NotContains(t, required, "optional", "a field carrying omitempty is not required")

	// At the zero value the omitempty fields are not written to JSON at all; the
	// remaining key set is exactly "the ones always written".
	assert.ElementsMatch(t, jsonKeys(t, allTypes{}), required,
		"required has to match the JSON keys of the zero value")
}

// TestBasicTypeMappings verifies the JSON Schema counterparts of the Go types.
func TestBasicTypeMappings(t *testing.T) {
	t.Parallel()

	d := document()
	schema := d.SchemaOf(filledAllTypes())

	expected := map[string]string{
		"text":    "string",
		"long":    "integer",
		"short":   "integer",
		"decimal": "number",
		"flag":    "boolean",
		"slice":   "array",
		"map":     "object",
		"bytes":   "string", // encoding/json writes a byte slice as a base64 STRING
	}

	for name, typ := range expected {
		assert.Equal(t, typ, property(t, d, schema, name)["type"], "the type of the %q field", name)
	}

	assert.Equal(t, "int64", property(t, d, schema, "long")["format"])
	assert.Equal(t, "int32", property(t, d, schema, "short")["format"])
	assert.Equal(t, map[string]any{"type": "string"},
		property(t, d, schema, "slice")["items"], "the slice element's type has to be carried")
}

// TestTimeIsADateTimeString verifies that time.Time is a string, not an object.
//
// time.Time's fields are unexported; read naively through reflection it would
// come out as an EMPTY object in the schema and a client would try to send an
// object into a date field.
func TestTimeIsADateTimeString(t *testing.T) {
	t.Parallel()

	d := document()

	assert.Equal(t, map[string]any{"type": "string", "format": "date-time"},
		property(t, d, d.SchemaOf(filledAllTypes()), "time"))
}

// TestATimePointerCarriesBothTheFormatAndNull verifies that *time.Time carries
// its format as well as null.
//
// The trap is real: time.Time's MarshalJSON has a VALUE receiver, so *time.Time
// carries it too. A naive check saying "it has its own encoder, I do not know its
// shape" drops fields like deleted_at — present in EVERY model — into a free
// schema and the client never recognizes the date field.
func TestATimePointerCarriesBothTheFormatAndNull(t *testing.T) {
	t.Parallel()

	d := document()

	assert.Equal(t,
		map[string]any{"type": []any{"string", "null"}, "format": "date-time"},
		property(t, d, d.SchemaOf(filledAllTypes()), "time_ptr"))
}

// TestRawJSONIsAFreeSchema verifies that json.RawMessage's shape is UNKNOWN.
func TestRawJSONIsAFreeSchema(t *testing.T) {
	t.Parallel()

	d := document()

	assert.Empty(t, property(t, d, d.SchemaOf(filledAllTypes()), "raw"),
		"the shape of raw JSON is unknown by definition; it has to be a free schema")
}

// TestPointersAreNullable verifies that pointer fields accept null.
func TestPointersAreNullable(t *testing.T) {
	t.Parallel()

	d := document()
	schema := d.SchemaOf(filledAllTypes())

	assert.Equal(t, []any{"string", "null"}, property(t, d, schema, "pointer")["type"])
	assert.Equal(t, "array", property(t, d, schema, "slice")["type"],
		"a slice being nil is Go's zero value rather than the author's choice; it is not nullable")
}

// TestAPointerToAComponentIsNullableThroughAnyOf verifies how a pointer to a
// component is made nullable.
//
// A type written next to a "$ref" is evaluated TOGETHER with the $ref in JSON
// Schema 2020-12 and null matches nothing; the right form is anyOf.
func TestAPointerToAComponentIsNullableThroughAnyOf(t *testing.T) {
	t.Parallel()

	d := document()
	parent := property(t, d, d.SchemaOf(node{}), "parent")

	options, ok := parent["anyOf"].([]any)
	require.True(t, ok, "a pointer to a component has to be nullable through anyOf: %#v", parent)
	assert.Contains(t, options, map[string]any{"type": "null"})
}

// TestRecursionDoesNotLoopForever verifies that the schema of a
// self-referencing type CAN BE PRODUCED.
//
// In a loop the test does not finish (it times out); the assertions show the
// cycle was broken with a $ref.
func TestRecursionDoesNotLoopForever(t *testing.T) {
	t.Parallel()

	d := document()
	schema := d.SchemaOf(node{})

	assert.Equal(t, "#/components/schemas/Node", schema["$ref"],
		"a named struct has to be registered as a component and described by a reference")

	children := property(t, d, schema, "children")
	assert.Equal(t, map[string]any{"$ref": "#/components/schemas/Node"}, children["items"],
		"the recursion has to be broken by a reference rather than by a depth limit")
}

// TestDerivedSchemasAreWrittenIntoTheDocument verifies that the components
// really enter the document.
//
// Looking at [openapi.Doc.SchemaOf]'s output alone would not do: with the
// reference target missing from the document the schema would be SYNTACTICALLY
// valid but unresolvable.
func TestDerivedSchemasAreWrittenIntoTheDocument(t *testing.T) {
	t.Parallel()

	d := document()
	d.Describe("GET", "/store/v1/products", openapi.Operation{
		Responses: map[string]any{"200": openapi.Response("Products", d.List(shadowingRecord{}))},
	})

	schema := buildSchema(t, d, buildRouter(t))
	schemas := asMap(t, asMap(t, schema["components"], "components")["schemas"], "schemas")

	assert.Contains(t, schemas, "ShadowingRecord")
	assert.Contains(t, schemas, "RichVariant")
	assert.Contains(t, schemas, "Error", "the core's shared error component has to stay")
}

// TestTheSingleEnvelopeIsProducedFromTheType verifies the single envelope's shape.
func TestTheSingleEnvelopeIsProducedFromTheType(t *testing.T) {
	t.Parallel()

	d := document()
	envelope := d.Item(richVariant{})

	assert.Equal(t, "object", envelope["type"])
	assert.Equal(t, []string{"data"}, envelope["required"])
	assert.ElementsMatch(t, []string{"id", "price"}, fieldNames(t, d, property(t, d, envelope, "data")))
}

// TestTheListEnvelopeIsProducedFromTheType verifies the list envelope's
// pagination fields and item type.
func TestTheListEnvelopeIsProducedFromTheType(t *testing.T) {
	t.Parallel()

	d := document()
	envelope := d.List(richVariant{})

	assert.ElementsMatch(t,
		[]string{"data", "count", "offset", "limit"}, requiredOf(t, d, envelope))
	assert.Equal(t, "integer", property(t, d, envelope, "count")["type"])

	data := property(t, d, envelope, "data")
	assert.Equal(t, "array", data["type"])

	item, ok := data["items"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"id", "price"}, fieldNames(t, d, item))
}

// TestTheListEnvelopesCountCanBeOptional verifies that an envelope declaring the
// counter droppable differs ONLY in the required list.
//
// Both assertions are needed. The field dropping out of "required" makes the
// documentation of the endpoint whose counter can be turned off (the storefront
// list) correct; the field STAYING in "properties" says that being droppable does
// not mean being absent — with the counter asked for it is still an integer and a
// client reading the schema has to produce it. Deleted, the document would define
// no field at all for the client that wants the counter.
//
// Comparing it with [Doc.List]'s output is deliberate: that the loosening belongs
// to this surface alone cannot be seen without putting the two side by side.
func TestTheListEnvelopesCountCanBeOptional(t *testing.T) {
	t.Parallel()

	d := document()

	strict := d.List(richVariant{})
	loose := d.ListOptionalCount(richVariant{})

	assert.Contains(t, requiredOf(t, d, strict), "count",
		"in the default envelope the counter is required")
	assert.NotContains(t, requiredOf(t, d, loose), "count",
		"in the loose envelope the counter MUST NOT be required")

	assert.ElementsMatch(t, []string{"data", "offset", "limit"}, requiredOf(t, d, loose),
		"only the counter may loosen; the other fields stay required")
	assert.Equal(t, "integer", property(t, d, loose, "count")["type"],
		"the field has to STAY in the schema: being droppable is not being absent")

	assert.Equal(t, property(t, d, strict, "data"), property(t, d, loose, "data"),
		"the item schema has to be the same in both envelopes")
}

// TestTheListEnvelopeDoesNotWrapASliceTwice verifies that the same envelope
// comes out whether List is given a slice or a record.
//
// Wrapping twice would produce an "array of arrays" in the document and nobody
// would notice that without reading the schema line by line.
func TestTheListEnvelopeDoesNotWrapASliceTwice(t *testing.T) {
	t.Parallel()

	d := document()

	assert.Equal(t, d.List(richVariant{}), d.List([]richVariant{}))
}

// TestTheRequestBodyIsProducedFromTheType verifies the requestBody definition's shape.
func TestTheRequestBodyIsProducedFromTheType(t *testing.T) {
	t.Parallel()

	d := document()
	body := d.RequestBody(richVariant{})

	assert.Equal(t, true, body["required"])

	content := asMap(t, body["content"], "content")
	typ := asMap(t, content["application/json"], "application/json")

	assert.Equal(t, map[string]any{"$ref": "#/components/schemas/RichVariant"}, typ["schema"])
}

// TestTwoTypesWantingTheSameNameStopTheDocument verifies that a name clash does
// not stay SILENT.
//
// Overwriting silently was the worst outcome: the body of one of two endpoints
// would be described wrongly, and that would only come out when a client sent
// the wrong field.
func TestTwoTypesWantingTheSameNameStopTheDocument(t *testing.T) {
	t.Parallel()

	d := document()
	d.SchemaOf(openapi.ClashingRecord{})
	d.SchemaOf(ClashingRecord{})

	_, err := d.Build(buildRouter(t))
	require.Error(t, err, "two different types cannot take the same component name")
	assert.Contains(t, err.Error(), "ClashingRecord")
}

// TestTheCoreComponentNameIsProtected verifies that the shared "Error" component
// cannot be overwritten by a module type.
func TestTheCoreComponentNameIsProtected(t *testing.T) {
	t.Parallel()

	d := document()
	d.SchemaOf(Error{})

	_, err := d.Build(buildRouter(t))
	require.Error(t, err, "the core's shared component name cannot be overwritten by a derived schema")
	assert.Contains(t, err.Error(), "Error")
}

// TestTheBodyOfADescribedEndpointIsWrittenIntoTheSchema verifies the end-to-end
// flow: the body a module described really has to appear in the produced document.
//
// This test exists because of a finding: the schema was syntactically valid but
// SEMANTICALLY empty — no endpoint had a requestBody or a 2xx response, and a
// client generator would produce methods where everything was 'any'.
func TestTheBodyOfADescribedEndpointIsWrittenIntoTheSchema(t *testing.T) {
	t.Parallel()

	d := document()
	d.Describe("GET", "/store/v1/products", openapi.Operation{
		Summary:     "Lists the storefront products",
		RequestBody: d.RequestBody(richVariant{}),
		Responses: map[string]any{
			"200": openapi.Response("The product list", d.List(shadowingRecord{})),
		},
	})

	operation := operationOf(t, buildSchema(t, d, buildRouter(t)), "/store/v1/products", "get")

	require.Contains(t, operation, "requestBody")
	require.Contains(t, responsesOf(t, operation), "200")
}

// TestAnUndescribedEndpointStaysInTheSchema verifies that the endpoint of a
// module not implementing Describer does NOT DROP out of the document.
//
// [openapi.Describer] is optional; an undescribed endpoint has to go on appearing
// with its path, method and security, only without a body.
func TestAnUndescribedEndpointStaysInTheSchema(t *testing.T) {
	t.Parallel()

	operation := operationOf(t, buildSchema(t, document(), buildRouter(t)), "/store/v1/products", "get")

	assert.NotContains(t, operation, "requestBody")
	assert.Contains(t, responsesOf(t, operation), "401", "the shared error responses have to stay")
}

// TestSchemaOfANilValueIsFree verifies that the schema of a value with no type
// CLAIMS NOTHING.
func TestSchemaOfANilValueIsFree(t *testing.T) {
	t.Parallel()

	assert.Empty(t, document().SchemaOf(nil))
}
