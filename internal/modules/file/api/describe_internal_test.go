package api

import (
	"encoding/json"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The test is in the INTERNAL package because the body being described
// ([uploadDTO]) is unexported. The only way to exercise it from the outside
// would be to export the type; widening the module's surface for the sake of
// exercising the document would break the very thing being exercised.

// docPaths produces the output of Describe against the REAL route tree and
// returns it as read back from JSON.
//
// The router has to be real too: if the description and the route's path drift
// apart, let the error show up HERE and not on somebody looking at
// /openapi.json in production.
func docPaths(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	raw, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"every described endpoint has to match a route; a record that does not match never enters the document")

	encoded, err := json.Marshal(raw)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))

	paths, ok := decoded["paths"].(map[string]any)
	require.True(t, ok, "the document has to have paths")

	return paths
}

// operation returns the given method of the given path.
func operation(t *testing.T, paths map[string]any, path, method string) map[string]any {
	t.Helper()

	entry, ok := paths[path].(map[string]any)
	require.True(t, ok, "%q has to be in the document", path)

	op, ok := entry[method].(map[string]any)
	require.True(t, ok, "%s has to be described for %q", method, path)

	return op
}

// TestTheUploadEndpointDescribesMULTIPART is the most critical claim about the
// schema.
//
// Had the body been written as "application/json", the generated client would
// try to send the file in a JSON body and EVERY request would be rejected — and
// on top of that the developer reading the schema would think the fault was in
// their own code. A wrong schema is worse than a missing schema here.
func TestTheUploadEndpointDescribesMULTIPART(t *testing.T) {
	t.Parallel()

	post := operation(t, docPaths(t), pathAdminUploads, "post")

	body, ok := post["requestBody"].(map[string]any)
	require.True(t, ok, "the upload endpoint has to describe a request body")

	content, ok := body["content"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, content, contentMultipart)
	assert.NotContains(t, content, "application/json",
		"this endpoint READS NO JSON; writing JSON in the schema would be an outright lie")

	media, ok := content[contentMultipart].(map[string]any)
	require.True(t, ok)

	fields, ok := media["schema"].(map[string]any)
	require.True(t, ok)

	properties, ok := fields[schemaProperties].(map[string]any)
	require.True(t, ok)
	require.Contains(t, properties, fieldFile, "the field the handler reads has to be described")

	field, ok := properties[fieldFile].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, formatBinary, field[schemaFormat],
		"binary content is described with format: binary; otherwise the generator sends text")
}

// TestTheUploadEndpointDescribes201AndItsBody pins the shape of the success
// response.
func TestTheUploadEndpointDescribes201AndItsBody(t *testing.T) {
	t.Parallel()

	post := operation(t, docPaths(t), pathAdminUploads, "post")

	responses, ok := post["responses"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, responses, "201")

	response, ok := responses["201"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, response, "content", "a 201 returns a body and its body has to be described")
}

// TestTheDeleteEndpointDescribes204AsBodiless pins that the 204 has no body.
//
// Had a content schema been written, the client generator would promise a body
// to read and the generated method would try to decode an empty response.
func TestTheDeleteEndpointDescribes204AsBodiless(t *testing.T) {
	t.Parallel()

	del := operation(t, docPaths(t), pathAdminUpload, "delete")

	responses, ok := del["responses"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, responses, "204")

	response, ok := responses["204"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, response, "content", "a 204 has no body")
	assert.NotEmpty(t, response[schemaDescription])
}

// TestTheListEndpointDescribesEveryParameterItReads verifies that the set of
// parameters in the document is the same as the set the handler REALLY reads.
//
// When the two sides drift apart two separate silent failures are born: a
// parameter that is not described never appears in the client generator, and a
// parameter that is described but not read is a promise that does not work.
func TestTheListEndpointDescribesEveryParameterItReads(t *testing.T) {
	t.Parallel()

	get := operation(t, docPaths(t), pathAdminUploads, "get")

	raw, ok := get["parameters"].([]any)
	require.True(t, ok, "the endpoint has to describe its query parameters")

	names := make([]string, 0, len(raw))
	for _, p := range raw {
		param, castOK := p.(map[string]any)
		require.True(t, castOK)

		name, nameOK := param["name"].(string)
		require.True(t, nameOK, "the parameter name has to be a string: %#v", param)
		names = append(names, name)
	}

	assert.ElementsMatch(t, []string{queryLimit, queryOffset}, names,
		"the parameters in the document have to match the ones the handler reads exactly")
}

// TestTheServingEndpointIsNotINTheDocument pins that the scope limit is
// deliberate.
//
// The core takes only the /admin/v1 and /store/v1 prefixes into the document;
// /files is not an API call but the target of an <img> tag. Testing the
// omission prevents it from being added one day because it was "forgotten" —
// had it been added, the client generator would produce a method that is called
// without an identity and that method would inherit the security default of the
// schema and so be described wrongly.
func TestTheServingEndpointIsNotINTheDocument(t *testing.T) {
	t.Parallel()

	paths := docPaths(t)

	for path := range paths {
		assert.NotContains(t, path, "/files/",
			"the serving endpoint must not enter the document; the core already takes only the API prefixes")
	}
}

// TestDescribeDoesNotDescribeTheSTORAGEKEY verifies that the schema does not
// promise a field that is not published.
//
// The key and the address are SEPARATE things: today the address derives from
// the key, but in an object store the address is signed and has nothing to do
// with the key. A schema publishing both would quietly start lying on that day.
func TestDescribeDoesNotDescribeTheSTORAGEKEY(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	schema := doc.SchemaOf(uploadDTO{})
	require.NotEmpty(t, schema)

	encoded, err := json.Marshal(doc.Schemas())
	require.NoError(t, err)

	text := string(encoded)
	assert.NotContains(t, text, `"storage_key"`, "the storage key is not published")
	assert.Contains(t, text, `"url"`, "the field the client needs is the address")
	assert.Contains(t, text, `"content_type"`)
	assert.Contains(t, text, `"size"`)
}
