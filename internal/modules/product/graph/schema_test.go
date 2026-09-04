package graph_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// compiledSchema returns the compiled schema.
//
// The schema is read from the generated code, NOT FROM THE FILE: that is what
// the server really speaks. Reading the file would make a schema change whose
// generation was forgotten look like it passes.
func compiledSchema(t *testing.T) *ast.Schema {
	t.Helper()

	return graph.NewExecutableSchema(graph.Config{}).Schema()
}

// typeDef returns the definition of a type in the schema.
func typeDef(t *testing.T, name string) *ast.Definition {
	t.Helper()

	definition, ok := compiledSchema(t).Types[name]
	require.True(t, ok, "the type %q must be in the schema", name)

	return definition
}

// sameName applies gqlgen's field matching rule: underscores are dropped and
// the comparison is case insensitive ("collectionId" <-> "CollectionID").
func sameName(a, b string) bool {
	return strings.EqualFold(strings.ReplaceAll(a, "_", ""), strings.ReplaceAll(b, "_", ""))
}

// goField looks for the field in the Go type that corresponds to the schema
// field.
func goField(gt reflect.Type, schemaField string) (reflect.StructField, bool) {
	return gt.FieldByNameFunc(func(name string) bool { return sameName(name, schemaField) })
}

// binding ties a schema type to its Go type and to the fields of that type that
// were DELIBERATELY not put into the schema.
type binding struct {
	schemaType string
	goType     reflect.Type
	// leftOut holds the Go fields deliberately kept out of the schema. If the
	// field name is not here either the test fails; that is, a DECISION is
	// mandatory for every field added to the service — the distinction between
	// "we forgot to add it" and "we decided not to add it" can only be
	// preserved that way.
	leftOut map[string]string
}

// bindings is the mapping between the schema types and the module's types.
//
// The mapping is the same as the "models" block in gqlgen.yml and IS REPEATED
// HERE. The repetition leaves no silent divergence: if the line in the
// configuration is deleted, gqlgen generates its OWN model for that type, the
// resolver signatures change and the package does not compile at all — that is,
// the divergence comes out before this test even gets a chance to run.
func bindings() []binding {
	// Timestamps and deletion information are MEANINGLESS in the storefront: a
	// deleted record is never returned, and when the taxonomy was written does
	// not concern the customer. createdAt/updatedAt are kept on the product and
	// the variant because the client refreshes its cache by them.
	taxonomyLeftOut := map[string]string{
		"CreatedAt": "the storefront client does not use the taxonomy's creation time",
		"UpdatedAt": "the storefront client does not use the taxonomy's update time",
		"DeletedAt": "a deleted record is never returned from the storefront anyway",
	}

	return []binding{
		{
			schemaType: "Product",
			goType:     reflect.TypeOf(service.StoreProduct{}),
			leftOut: map[string]string{
				"Status":    "the storefront returns only published products; the field would always be \"published\"",
				"DeletedAt": "a deleted product is never returned from the storefront anyway",
			},
		},
		{
			schemaType: "Variant",
			goType:     reflect.TypeOf(service.StoreVariant{}),
			leftOut: map[string]string{
				"DeletedAt": "a deleted variant is never returned from the storefront anyway",
			},
		},
		{schemaType: "Option", goType: reflect.TypeOf(models.Option{}), leftOut: taxonomyLeftOut},
		{schemaType: "OptionValue", goType: reflect.TypeOf(models.OptionValue{}), leftOut: taxonomyLeftOut},
		{schemaType: "Image", goType: reflect.TypeOf(models.Image{}), leftOut: taxonomyLeftOut},
		{schemaType: "Tag", goType: reflect.TypeOf(models.Tag{}), leftOut: taxonomyLeftOut},
		{schemaType: "Category", goType: reflect.TypeOf(models.Category{}), leftOut: taxonomyLeftOut},
	}
}

// TestSchemaFieldsExistOnTheServiceType verifies that every field in the schema
// has a counterpart on the service type.
//
// Could a field the service does not return be put into the schema, the client
// would be promised a feature that will never be filled. The generated code
// already catches this violation at COMPILE time (an unbindable field asks for
// a resolver); the test also closes off writing that resolver by hand and
// inventing the field.
func TestSchemaFieldsExistOnTheServiceType(t *testing.T) {
	t.Parallel()

	for _, b := range bindings() {
		t.Run(b.schemaType, func(t *testing.T) {
			t.Parallel()

			for _, field := range typeDef(t, b.schemaType).Fields {
				if strings.HasPrefix(field.Name, "__") {
					continue
				}

				_, ok := goField(b.goType, field.Name)
				assert.True(t, ok, "the field %s.%s has no counterpart on the type %s",
					b.schemaType, field.Name, b.goType)
			}
		})
	}
}

// TestServiceFieldsAreInTheSchemaOrDeliberatelyLeftOut verifies that every
// field the service returns is either in the schema or deliberately left out.
//
// This test's REAL work will be done tomorrow: when a field is added to the
// service the test fails and whoever adds it is forced to answer the question
// "should it enter the storefront too". Otherwise the second read surface falls
// behind the first one over time and nobody notices.
func TestServiceFieldsAreInTheSchemaOrDeliberatelyLeftOut(t *testing.T) {
	t.Parallel()

	for _, b := range bindings() {
		t.Run(b.schemaType, func(t *testing.T) {
			t.Parallel()

			definition := typeDef(t, b.schemaType)

			for _, field := range reflect.VisibleFields(b.goType) {
				// The embedded struct itself is not a data field; its fields
				// already appear flattened in the list.
				if field.Anonymous || !field.IsExported() {
					continue
				}

				inSchema := false

				for _, schemaField := range definition.Fields {
					if sameName(schemaField.Name, field.Name) {
						inSchema = true

						break
					}
				}

				if inSchema {
					continue
				}

				reason, deliberate := b.leftOut[field.Name]
				assert.True(t, deliberate,
					"%s.%s is not in the schema. If it is to enter the storefront it must be added "+
						"to the schema, otherwise to the test's 'leftOut' list with its rationale",
					b.goType, field.Name)
				assert.NotEmpty(t, reason)
			}
		})
	}
}

// TestProductsArgumentsMatchWhatTheServiceReads verifies that the query
// arguments overlap ONE TO ONE with service.StoreListOptions.
//
// Putting an argument the service does not read into the schema is promising
// the client a feature that does not work: the generator puts the field into
// the query, the caller fills it in, the server silently ignores it.
//
// The value of a storefront option can come from THREE places and each is a
// separate decision: from a query argument, from the request's identity, or
// from the SELECTION SET. The third is specific to GraphQL — whether the client
// selects a field is an input too — and that is why looking for an argument
// would be wrong; putting a "should I count" argument into the schema would be
// asking the same question the "count" field itself asks, a second time.
func TestProductsArgumentsMatchWhatTheServiceReads(t *testing.T) {
	t.Parallel()

	// The name mapping is written BY HAND because one of them does not overlap:
	// the free-text search is "q" in the schema (kept the same as its name in
	// REST), while the service field is Search.
	mapping := map[string]string{
		"CollectionID": "collectionId",
		"Search":       "q",
		"Limit":        "limit",
		"Offset":       "offset",
	}

	// The fields the client CANNOT GIVE: their values come from the request's
	// identity.
	fromIdentity := map[string]bool{"SalesChannelIDs": true}

	// The fields whose value comes from the SELECTION SET: their counterpart is
	// not an argument but a FIELD on ProductList. If the field is not selected,
	// the work is not done either.
	fromSelection := map[string]string{"SkipCount": "count"}

	productList := compiledSchema(t).Types["ProductList"]
	require.NotNil(t, productList, "the ProductList type must be in the schema")

	var expected []string

	for _, field := range reflect.VisibleFields(reflect.TypeOf(service.StoreListOptions{})) {
		if fromIdentity[field.Name] {
			continue
		}

		if fieldName, selectionBound := fromSelection[field.Name]; selectionBound {
			require.NotNil(t, productList.Fields.ForName(fieldName),
				"StoreListOptions.%s ties its decision to the selection of the ProductList.%s "+
					"field (see graph.isSelected); that field is not in the schema",
				field.Name, fieldName)

			continue
		}

		name, ok := mapping[field.Name]
		require.True(t, ok,
			"StoreListOptions.%s is defined neither as a schema argument, nor as coming from the "+
				"identity, nor as coming from the selection set; if a new option was added, one of "+
				"the three must be decided on", field.Name)

		expected = append(expected, name)
	}

	var found []string

	for _, arg := range compiledSchema(t).Query.Fields.ForName("products").Arguments {
		found = append(found, arg.Name)
	}

	assert.ElementsMatch(t, expected, found,
		"the products arguments must be the same as the options the service reads")
}

// textScalars holds the schema scalars that bind to *string on the Go side.
//
// ID is a scalar separate from String but its carrier is the same, and the
// empty string means the same thing for both; a test that looked only at String
// would silently skip half of the filters (collectionId).
var textScalars = map[string]bool{"String": true, "ID": true}

// TestEmptyTextArgumentBuildsNoFilter verifies that every text argument given
// as empty reaches the service as nil.
//
// [TestProductsArgumentsMatchWhatTheServiceReads] says that the arguments
// overlap with the service's options; this test pins what that overlap MEANS AT
// AN EMPTY VALUE. The rule already existed in the module — stringParam in REST
// and singleSelector on the single-item GraphQL endpoint count the empty string
// as "not given" — and the only place it was not applied was the list road. The
// cost is SILENT in both directions: `collectionId: ""` filters by an empty
// identity and returns nothing, while `q: ""` adds an ILIKE scan that matches
// every row and never touches the result.
//
// The test walks the SCHEMA rather than individual arguments; its real work
// will be done tomorrow: a new text filter added to products is inside this
// claim too, and an addition that forgets to normalize fails here.
func TestEmptyTextArgumentBuildsNoFilter(t *testing.T) {
	t.Parallel()

	// Schema argument -> [service.StoreListOptions] field. The mapping is
	// written BY HAND; its rationale is the same as the mapping table inside
	// [TestProductsArgumentsMatchWhatTheServiceReads] (the names do not overlap
	// one to one).
	fields := map[string]string{
		"collectionId": "CollectionID",
		"q":            "Search",
	}

	for _, arg := range compiledSchema(t).Query.Fields.ForName("products").Arguments {
		if !textScalars[arg.Type.NamedType] {
			continue
		}

		t.Run(arg.Name, func(t *testing.T) {
			t.Parallel()

			name, known := fields[arg.Name]
			require.True(t, known,
				"the StoreListOptions counterpart of the text argument %q is unknown; if a new "+
					"filter was added it must be added to the mapping too", arg.Name)

			svc := &fakeStorefront{}

			// A value made up only of whitespace is given: it exercises both
			// the "empty" and the "empty after trimming" case in one case.
			response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
				fmt.Sprintf(`{ products(%s: "   ") { count } }`, arg.Name))

			require.Empty(t, response.Errors)

			value := reflect.ValueOf(svc.lastList(t)).FieldByName(name)
			require.Equal(t, reflect.Pointer, value.Kind(),
				"%s must be a pointer; the only thing carrying the 'not given' distinction is nil",
				name)
			assert.True(t, value.IsNil(),
				"an empty %q argument must build no filter; nil must reach the service", arg.Name)
		})
	}
}

// TestSchemaHasNoSalesChannelArgument verifies that the channel CANNOT BE ASKED
// FOR from anywhere.
//
// This is not a convenience but a SECURITY claim: the moment the channel turns
// into an argument, the filter stops being an authorization and turns into a
// display preference, and a client arriving with any publishable key it happened
// to hold reads another storefront's catalog. The claim looks not at individual
// queries but at the WHOLE SCHEMA: a query added tomorrow is inside this rule
// too.
func TestSchemaHasNoSalesChannelArgument(t *testing.T) {
	t.Parallel()

	for name, definition := range compiledSchema(t).Types {
		if strings.HasPrefix(name, "__") {
			continue
		}

		for _, field := range definition.Fields {
			for _, arg := range field.Arguments {
				assert.NotContains(t, strings.ToLower(arg.Name), "channel",
					"%s.%s(%s): the sales channel is read from the IDENTITY, not from the request",
					name, field.Name, arg.Name)
			}
		}
	}
}

// TestSchemaHasNoWriteSurface verifies that the surface stays limited to
// READING.
//
// The absence of Mutation is not an omission but a decision that was made (see
// schema.graphqls); if a decision has no test, one day it gets "completed" as
// if it were missing.
func TestSchemaHasNoWriteSurface(t *testing.T) {
	t.Parallel()

	s := compiledSchema(t)

	assert.Nil(t, s.Mutation, "the storefront GraphQL surface is read only")
	assert.Nil(t, s.Subscription, "there is no subscription surface")
}
