package openapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// cartLine is read from a request body and written back in a response.
type cartLine struct {
	VariantID string `json:"variant_id"`
	Quantity  int    `json:"quantity"`
	Note      string `json:"note,omitempty"`
}

// addLinesRequest reaches cartLine through a field, a slice and a pointer.
type addLinesRequest struct {
	Line  cartLine   `json:"line"`
	Lines []cartLine `json:"lines"`
	Gift  *cartLine  `json:"gift"`
	Code  string     `json:"code"`
}

// cartLineInput takes the name cartLine's request form is published by.
type cartLineInput struct {
	Other string `json:"other"`
}

// loginBody is a request body whose component a module edits after deriving
// it, as the auth module marks its password field.
type loginBody struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// describeLines describes one operation that reads addLinesRequest and
// writes cartLine, deriving the response first when responseFirst says so.
func describeLines(d *openapi.Doc, responseFirst bool) {
	var body, response map[string]any
	if responseFirst {
		response = d.Item(cartLine{})
		body = d.RequestBody(addLinesRequest{})
	} else {
		body = d.RequestBody(addLinesRequest{})
		response = d.Item(cartLine{})
	}

	d.Describe("GET", "/store/v1/products", openapi.Operation{
		RequestBody: body,
		Responses:   map[string]any{"200": openapi.Response("The line", response)},
	})
	d.Describe("POST", loginPath, openapi.Operation{RequestBody: d.RequestBody(cartLine{})})
}

// componentOf returns a component of a built document.
func componentOf(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()

	components := asMap(t, asMap(t, schema["components"], "components")["schemas"], "schemas")
	require.Contains(t, components, name)

	return asMap(t, components[name], name)
}

// bodySchemaOf returns the schema of an operation's JSON request body.
func bodySchemaOf(t *testing.T, operation map[string]any) map[string]any {
	t.Helper()

	content := asMap(t, asMap(t, operation["requestBody"], "requestBody")["content"], "content")

	return asMap(t, asMap(t, content["application/json"], "application/json")["schema"], "schema")
}

// TestARequestBodyRequiresNoField is ADR 0363: a request body's component,
// and every component it reaches, lists no required field, though their
// fields carry no omitempty; a type only read keeps its own name.
func TestARequestBodyRequiresNoField(t *testing.T) {
	t.Parallel()

	d := document()
	d.Describe("GET", "/store/v1/products", openapi.Operation{RequestBody: d.RequestBody(addLinesRequest{})})
	schema := buildSchema(t, d, buildRouter(t))

	for _, name := range []string{"AddLinesRequest", "CartLine"} {
		component := componentOf(t, schema, name)
		assert.NotContains(t, component, "required", name)
		assert.NotEmpty(t, component["properties"], name)
	}
	assert.NotContains(t, asMap(t, schema["components"], "components")["schemas"], "CartLineInput",
		"a type only read is published once, by its name")
}

// TestATypeReadAndWrittenIsPublishedInBothForms is ADR 0363: a type a request
// body reads and a response writes is published as written under its name
// and as read under its name with "Input", every reference from the body
// pointing at the latter, whichever form was derived first.
func TestATypeReadAndWrittenIsPublishedInBothForms(t *testing.T) {
	t.Parallel()

	var documents []string

	for _, responseFirst := range []bool{false, true} {
		d := document()
		describeLines(d, responseFirst)
		schema := buildSchema(t, d, buildRouter(t))

		assert.Equal(t, []any{"quantity", "variant_id"}, componentOf(t, schema, "CartLine")["required"],
			"the written form requires what is always written")
		assert.NotContains(t, componentOf(t, schema, "CartLineInput"), "required")

		input := "#/components/schemas/CartLineInput"
		properties := asMap(t, componentOf(t, schema, "AddLinesRequest")["properties"], "properties")
		assert.Equal(t, input, asMap(t, properties["line"], "line")["$ref"])
		assert.Equal(t, input, asMap(t, asMap(t, properties["lines"], "lines")["items"], "items")["$ref"])
		gift, ok := asMap(t, properties["gift"], "gift")["anyOf"].([]any)
		require.True(t, ok, "a pointer to a component is an anyOf")
		assert.Equal(t, input, asMap(t, gift[0], "gift")["$ref"])

		operation := operationOf(t, schema, "/store/v1/products", "get")
		assert.Equal(t, "#/components/schemas/AddLinesRequest", bodySchemaOf(t, operation)["$ref"])
		assert.Equal(t, input, bodySchemaOf(t, operationOf(t, schema, loginPath, "post"))["$ref"],
			"a body that is the type itself")

		response := asMap(t, responsesOf(t, operation)["200"], "200")
		data := asMap(t, asMap(t, asMap(t, asMap(t, asMap(t, response["content"], "content")["application/json"],
			"json")["schema"], "schema")["properties"], "properties")["data"], "data")
		assert.Equal(t, "#/components/schemas/CartLine", data["$ref"], "the response reads the written form")

		encoded, err := json.Marshal(schema)
		require.NoError(t, err)
		documents = append(documents, string(encoded))
	}

	assert.Equal(t, documents[0], documents[1], "the order the forms were derived in changes nothing")
}

// TestATakenInputNameStopsTheDocument is ADR 0363: a type already holding the
// name a request form would be published by is a clash, which stops the
// document as two types wanting one name do.
func TestATakenInputNameStopsTheDocument(t *testing.T) {
	t.Parallel()

	d := document()
	describeLines(d, false)
	d.SchemaOf(cartLineInput{})

	_, err := d.Build(buildRouter(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CartLineInput")
}

// TestARequestFormEditedThroughSchemasIsTheOnePublished is ADR 0363: the
// component Schemas returns for a request body's reference is the one the
// document publishes, so a module marking a field in it marks the document.
func TestARequestFormEditedThroughSchemasIsTheOnePublished(t *testing.T) {
	t.Parallel()

	d := document()
	d.Describe("GET", "/store/v1/products", openapi.Operation{RequestBody: d.RequestBody(loginBody{})})

	component := resolve(t, d, map[string]any{"$ref": "#/components/schemas/LoginBody"})
	asMap(t, asMap(t, component["properties"], "properties")["password"], "password")["format"] = "password"

	properties := asMap(t, componentOf(t, buildSchema(t, d, buildRouter(t)), "LoginBody")["properties"], "properties")
	assert.Equal(t, "password", asMap(t, properties["password"], "password")["format"])
}

// TestTwoTypesWantingOneNameAcrossTheFormsStopTheDocument is ADR 0363: a name
// belongs to one type across both forms, so a type read and another type
// written under the same name stop the document as two written ones do.
func TestTwoTypesWantingOneNameAcrossTheFormsStopTheDocument(t *testing.T) {
	t.Parallel()

	for _, readFirst := range []bool{true, false} {
		d := document()
		if readFirst {
			d.RequestBody(openapi.ClashingRecord{})
			d.SchemaOf(ClashingRecord{})
		} else {
			d.SchemaOf(ClashingRecord{})
			d.RequestBody(openapi.ClashingRecord{})
		}

		_, err := d.Build(buildRouter(t))
		require.Error(t, err, "read first: %v", readFirst)
		assert.Contains(t, err.Error(), "ClashingRecord")
	}
}
