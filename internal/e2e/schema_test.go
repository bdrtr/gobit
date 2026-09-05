//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// This file audits what the /openapi.json endpoint serves on the RUNNING server.
//
// # Why unit tests are not enough
//
// Both the core's schema generator and the modules' descriptions are tested in
// their own packages; while both are green it is still possible for the
// document to be served BODYLESS, because the place that runs the description
// hook is the setup itself. If the hook is not wired, no unit test breaks: the
// modules describe themselves, the generator runs, and the server still
// publishes an empty schema that says "this endpoint exists, it wants
// credentials, it may fail like so". The tests here close exactly that gap —
// the document is generated from the router that requests really pass through
// and from the modules as they are really set up.
//
// # Which set is audited
//
// [TestSchemaDescribesEndpointsWithTheirBodies] audits ONLY the DESCRIBED
// endpoints, and it does not write the set out by hand, it reads it from the
// modules with [describedEndpoints]. The distinction is deliberate: today only
// the /store/v1 surface is described, and an undescribed endpoint is a VALID
// model — it appears in the document with its path, its method and its
// security, only without a body. Saying "every endpoint must have a body" would
// produce a test that is red today and that nobody could fix; as the
// description widens, the audited set grows by itself.
//
// Root keys, $ref integrity, the security of the login endpoint and raw ID
// leakage, on the other hand, are audited over the WHOLE DOCUMENT; those are
// independent of the description.

// refPrefix is the path prefix of references made to component schemas.
const refPrefix = "#/components/schemas/"

// schemaRefKey is JSON Schema's reference key.
const schemaRefKey = "$ref"

// schemaPath is the endpoint that serves the OpenAPI document.
//
// The endpoint is BOUND with this constant (see the e2e ground setup); the test
// keeping its own copy would have meant taking a 404 when the path changed and
// then reporting it as "the schema could not be generated".
const schemaPath = "/openapi.json"

// shapeIsKnown reports whether a schema tells the client ANYTHING.
//
// Saying "it must carry properties" would be WRONG: a list record may perfectly
// well be a scalar. GET /admin/v1/payment-providers returns provider IDS and
// its items are plain strings; {"type":"string"} is a complete and correct
// description, not a deficient one. Had the rule been "it must carry fields",
// the test would reject the correct schema, and fixing it would mean inventing
// a non-existent object in the schema.
//
// What is truly unacceptable is the EMPTY schema ({}): that means "I do not
// know the shape" and produces 'any' in a client generator.
func shapeIsKnown(schema map[string]any) bool {
	if len(schema) == 0 {
		return false
	}

	for _, key := range []string{"properties", "type", "$ref", "items", "anyOf", "oneOf", "allOf", "enum"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}

	return false
}

// bodyBearingMethods are the methods ALLOWED to carry a body.
//
// The name means "may carry", not "must carry": a method OUTSIDE this set
// having a body is a mistake (the server does not read it), while a method
// inside it having no body is perfectly ordinary (see /auth/logout).
//
// DELETE is deliberately OUTSIDE: it is a write method too, but it picks its
// resource from its path and reads no body.
var bodyBearingMethods = map[string]struct{}{
	http.MethodPost:  {},
	http.MethodPut:   {},
	http.MethodPatch: {},
}

// recordIDPattern matches a prefixed record ID (e.g. "cart_01H…").
//
// The pattern is the ID GENERATOR's format: a lowercase prefix + 26 characters
// from the Crockford Base32 alphabet (see the modules' models/ids.go files).
// Instead of listing prefixes one by one, the format itself is searched for; a
// new module's new prefix cannot be left waiting to be added to a list.
var recordIDPattern = regexp.MustCompile(`[a-z][a-z0-9]*_[0-9A-HJKMNP-TV-Z]{26}`)

// describeDocument builds the OpenAPI document and runs it through the modules that
// can describe themselves.
//
// It is the twin of the identically named function in cmd/server, and the
// repetition is deliberate: [openapi.Describer] is an OPTIONAL interface, the
// type assertion is made at the composition root, and the core does not know
// the modules (Principle 2.4). e2e's composition root is TestMain; running the
// hook here as well is the only way to build the SAME setup as in production.
// The module list is repeated for exactly the same reason (see the registry Add
// calls in TestMain).
func describeDocument(title, version string, modules []module.Module) *openapi.Doc {
	doc := openapi.New(title, version)

	for _, mod := range modules {
		describer, canDescribe := mod.(openapi.Describer)
		if !canDescribe {
			continue
		}

		describer.Describe(doc)
	}

	return doc
}

// describedEndpoints returns every "METHOD /path" record the modules describe.
//
// # Why it is generated against an empty router
//
// [openapi.Doc] does not expose the description map; the only way to read the
// set is the [openapi.Doc.UnmatchedDescriptions] function, and that returns the
// ones matching no route. Generated against an EMPTY router no description
// matches, so the returned list is ALL of the described ones.
//
// The alternative — writing the set out by hand in this file — would blind the
// test: a hand-written list silently leaves a newly described endpoint out of
// scope, and an endpoint whose body is missing would hide behind a green test.
func describedEndpoints(t *testing.T) []string {
	t.Helper()

	probe := describeDocument("probe", "e2e", testModules)

	_, err := probe.Build(chi.NewRouter())
	require.NoError(t, err, "the probe document must build")

	endpoints := probe.UnmatchedDescriptions()
	require.NotEmpty(t, endpoints,
		"precondition: at least one endpoint must be described; if none is, the body assertions in this file audit NOTHING")

	return endpoints
}

// schemaDocument calls the /openapi.json endpoint; it returns the raw body and
// the decoded document.
//
// The raw body is returned as well, and the reason is technical: telling "the
// field was never written" from "null was written", and searching for a raw
// record ID INSIDE the document, can only be done over the text.
func schemaDocument(t *testing.T) (raw []byte, doc map[string]any) {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, schemaPath, http.NoBody)
	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code,
		"the schema endpoint must return 200; body: %s", recorder.Body.String())

	raw = recorder.Body.Bytes()

	require.NoError(t, json.Unmarshal(raw, &doc),
		"the schema could not be decoded; body: %s", string(raw))

	return raw, doc
}

// objectField reads an object field from a map; it stops the test if absent.
func objectField(t *testing.T, source map[string]any, field, where string) map[string]any {
	t.Helper()

	value, found := source[field]
	require.True(t, found, "%s must have a %q field; present: %v", where, field, anahtarlar(source))

	object, ok := value.(map[string]any)
	require.True(t, ok, "the %q in %s must be an object, found %T", field, where, value)

	return object
}

// findOperation returns a path+method operation from the document.
func findOperation(t *testing.T, doc map[string]any, method, pattern string) map[string]any {
	t.Helper()

	paths := objectField(t, doc, "paths", "document")
	path := objectField(t, paths, pattern, "paths")

	return objectField(t, path, strings.ToLower(method), pattern)
}

// jsonSchema extracts the application/json schema out of a RESPONSE definition.
//
// For request bodies [requestSchema] is used: not all of them are JSON.
func jsonSchema(t *testing.T, definition map[string]any, where string) map[string]any {
	t.Helper()

	content := objectField(t, definition, "content", where)
	media := objectField(t, content, "application/json", where+".content")

	return objectField(t, media, "schema", where+".content.application/json")
}

// requestSchema reads a request body's schema INDEPENDENTLY of the media type.
//
// The reason it stands apart from [jsonSchema] is concrete: not every request
// body IS JSON. The body of POST /admin/v1/uploads is multipart/form-data, and
// it has to be — wrapping file bytes in JSON would punish every upload with
// base64 growth and would make streaming parsing impossible. A test that forced
// "application/json" here would count that endpoint's CORRECT description as an
// error; and the fix would be to force the schema to lie.
//
// The media type set is expected to be a SINGLETON: an endpoint that describes
// more than one type is letting the client choose which shape goes with which
// type, and there is no such endpoint in this repository. The day one appears,
// it is right that it shows up here and is handled deliberately.
//
// On the RESPONSE side this flexibility does NOT exist and must not
// ([jsonSchema] continues to be used there): the response envelope is the
// core's single format, and had a non-JSON response body been described, every
// assertion that unwraps the envelope would silently become meaningless.
func requestSchema(t *testing.T, definition map[string]any, where string) map[string]any {
	t.Helper()

	content := objectField(t, definition, "content", where)
	mediaTypes := anahtarlar(content)
	require.Len(t, mediaTypes, 1,
		"%s must describe a single media type; more than one leaves the choice to the client (got: %v)",
		where, mediaTypes)

	media := objectField(t, content, mediaTypes[0], where+".content")

	return objectField(t, media, "schema", where+".content."+mediaTypes[0])
}

// resolvedSchema follows a schema carrying a $ref down to its component
// definition.
//
// Fields cannot be counted without following the chain: the generator produces
// a ref for every named struct ([openapi.Doc.SchemaOf]), and looking at the ref
// and saying "it has no properties" would mean mistaking a full schema for an
// empty one — that is, the test itself would produce the very fault it is
// supposed to catch.
func resolvedSchema(t *testing.T, doc, schema map[string]any) map[string]any {
	t.Helper()

	schemas := schemaComponents(t, doc)

	// The step limit is a safety net: a ref chain that returns to itself must
	// be stopped here instead of spinning the test forever.
	for step := 0; step < 32; step++ {
		raw, hasRef := schema[schemaRefKey]
		if !hasRef {
			return schema
		}

		path, isString := raw.(string)
		require.True(t, isString, "$ref must be a string, found %T", raw)

		name := strings.TrimPrefix(path, refPrefix)
		require.NotEqual(t, path, name, "$ref may only start with %s: %s", refPrefix, path)

		schema = objectField(t, schemas, name, "components/schemas")
	}

	t.Fatalf("the $ref chain did not resolve in 32 steps")

	return nil
}

// schemaComponents returns the components/schemas map.
func schemaComponents(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()

	return objectField(t, objectField(t, doc, "components", "document"), "schemas", "components")
}

// recordSchema extracts the RECORD schema out of an envelope schema.
//
// The envelope may be either singular ({"data": {…}}) or a list ({"data": […]});
// in both cases the thing really being described is what is INSIDE data.
// Looking at the envelope and saying "it has two fields" would mean counting a
// bodyless schema as a full one.
func recordSchema(t *testing.T, doc, envelope map[string]any) map[string]any {
	t.Helper()

	properties := objectField(t, resolvedSchema(t, doc, envelope), "properties", "envelope")
	data := resolvedSchema(t, doc, objectField(t, properties, "data", "envelope.properties"))

	item, isList := data["items"]
	if !isList {
		return data
	}

	itemSchema, ok := item.(map[string]any)
	require.True(t, ok, "data.items must be a schema, found %T", item)

	return resolvedSchema(t, doc, itemSchema)
}

// recordFields returns the field names and the required ones in an operation's
// response record.
func recordFields(t *testing.T, doc map[string]any, method, pattern, code string) (fields, required []string) {
	t.Helper()

	operation := findOperation(t, doc, method, pattern)
	responses := objectField(t, operation, "responses", method+" "+pattern)
	definition := objectField(t, responses, code, method+" "+pattern+" responses")

	record := recordSchema(t, doc, jsonSchema(t, definition, method+" "+pattern+" "+code))

	return anahtarlar(objectField(t, record, "properties", "record schema")), stringSlice(t, record["required"])
}

// anahtarlar returns a map's keys in sorted order.
func anahtarlar[T any](m map[string]T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// stringSlice converts a string array coming from JSON into a Go slice.
//
// If the field is absent it returns an empty slice: "required was not written"
// and "required is empty" mean the same thing in JSON Schema.
func stringSlice(t *testing.T, value any) []string {
	t.Helper()

	if value == nil {
		return nil
	}

	raw, ok := value.([]any)
	require.True(t, ok, "a string array was expected, found %T", value)

	out := make([]string, 0, len(raw))

	for _, item := range raw {
		text, isString := item.(string)
		require.True(t, isString, "an array item must be a string, found %T", item)

		out = append(out, text)
	}

	return out
}

// mustBeSubset verifies that every item of subset is found inside superset.
func mustBeSubset(t *testing.T, subset, superset []string, message string) {
	t.Helper()

	for _, item := range subset {
		assert.Contains(t, superset, item, "%s (missing: %q; set: %v)", message, item, superset)
	}
}

// collectRefs gathers every $ref value appearing in the document.
func collectRefs(node any, total *[]string) {
	switch value := node.(type) {
	case map[string]any:
		for field, child := range value {
			if field == schemaRefKey {
				if path, isString := child.(string); isString {
					*total = append(*total, path)
				}

				continue
			}

			collectRefs(child, total)
		}
	case []any:
		for _, item := range value {
			collectRefs(item, total)
		}
	}
}

// createCart opens a REAL cart through the storefront endpoint; it returns its
// ID and its body.
func createCart(t *testing.T) (cartID string, body []byte) {
	t.Helper()

	requestBody, err := json.Marshal(map[string]string{"country_code": taxedCountry})
	require.NoError(t, err, "the cart body could not be encoded")

	request := httptest.NewRequest(http.MethodPost, "/store/v1/carts", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code,
		"the cart must be created; body: %s", recorder.Body.String())

	body = recorder.Body.Bytes()

	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "the cart response could not be decoded; body: %s", string(body))
	require.NotEmpty(t, envelope.Data.ID, "a cart ID must be returned")

	return envelope.Data.ID, body
}

// responseFields returns the field names inside a response's data envelope.
func responseFields(t *testing.T, body []byte) []string {
	t.Helper()

	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "the response could not be decoded; body: %s", string(body))
	require.NotEmpty(t, envelope.Data, "the response must carry a data envelope; body: %s", string(body))

	return anahtarlar(envelope.Data)
}

// TestSchemaDescribesEndpointsWithTheirBodies verifies that every described
// endpoint says what it takes and what it returns.
//
// The audited set is the DESCRIBED endpoints ([describedEndpoints]); for
// undescribed endpoints no body assertion is MADE (the reason is at the top of
// the file).
//
// The assertion has three layers, and all three are a client generator's needs:
//
//   - at least one 2xx response: without it the generator writes a method whose
//     return type is "void";
//   - the 2xx body carries FIELDS: it looks inside the envelope, because
//     {data: {}} is an object too and a test that looked at the envelope would
//     mistake an empty schema for a full one;
//   - a requestBody on write endpoints: without it the generator writes a method
//     that takes "any" for everything and the caller ends up guessing field
//     names.
//
// 204 is the sole exception and it MUST NOT have a body: an empty content would
// mean "something is returned but its shape is unknown", whereas what is meant
// is "nothing is returned".
func TestSchemaDescribesEndpointsWithTheirBodies(t *testing.T) {
	_, doc := schemaDocument(t)

	described := describedEndpoints(t)
	t.Logf("audited set — %d described endpoints: %s", len(described), strings.Join(described, ", "))

	for _, endpoint := range described {
		method, pattern, split := strings.Cut(endpoint, " ")
		require.True(t, split, "the description key could not be parsed: %q", endpoint)

		t.Run(endpoint, func(t *testing.T) {
			operation := findOperation(t, doc, method, pattern)

			assert.NotEmpty(t, operation["summary"],
				"a described endpoint must carry a summary; an operation without one becomes a nameless method in the client")

			responses := objectField(t, operation, "responses", endpoint)

			var successes []string

			for _, code := range anahtarlar(responses) {
				if strings.HasPrefix(code, "2") {
					successes = append(successes, code)
				}
			}

			require.NotEmpty(t, successes,
				"a described endpoint must carry at least one 2xx response; only error responses present: %v", anahtarlar(responses))

			for _, code := range successes {
				definition := objectField(t, responses, code, endpoint+" responses")
				assert.NotEmpty(t, definition["description"], "the %s response must carry a description", code)

				if code == "204" {
					assert.NotContains(t, definition, "content",
						"204 IS BODYLESS; an empty content would mean 'the shape is unknown'")

					continue
				}

				record := recordSchema(t, doc, jsonSchema(t, definition, endpoint+" "+code))
				assert.True(t, shapeIsKnown(record),
					"the SHAPE of the %s response record must be known; an empty schema leaves the client guessing (got: %v)",
					code, anahtarlar(record))
			}

			if _, isWrite := bodyBearingMethods[method]; !isWrite {
				assert.NotContains(t, operation, "requestBody",
					"%s reads no body; writing a body into the schema would promise a field that is never read", method)

				return
			}

			// A write method does not PROVE that a body is read: /auth/logout
			// takes its identity from the token, /api-keys/{id}/revoke takes its
			// resource from the path, and both are POSTs. Had a body been made
			// MANDATORY, the test would demand documentation for a field the
			// server never reads — that is, it would demand that the schema lie.
			// Which endpoint reads a body is tabulated in the module's OWN test;
			// the assertion here is only "if it is written, it is written right".
			rawBody, hasBody := operation["requestBody"]
			if !hasBody {
				return
			}

			body, ok := rawBody.(map[string]any)
			require.True(t, ok, "the %s requestBody must be an object", endpoint)

			// "required" is NOT tested here. Whether the body is mandatory is the
			// endpoint's own decision: POST .../payment-sessions/{id}/capture
			// called without a body captures the WHOLE blocked amount, and that
			// is the most common call. Imposing "required" would have meant the
			// client generator forcing the caller to build an empty object only
			// because the schema says so. Which endpoint reads the body as
			// mandatory is tabulated in the module's OWN test; the assertion here
			// is that the SHAPE of the body is known.
			assert.Contains(t, body, "content", "the %s body must carry content", endpoint)

			request := resolvedSchema(t, doc, requestSchema(t, body, endpoint+" requestBody"))
			assert.NotEmpty(t, objectField(t, request, "properties", endpoint+" request body"),
				"the request body must carry fields; a fieldless body leaves the client guessing")
		})
	}
}

// TestSchemaDescriptionsMatchRealRoutes verifies that no description falls
// through.
//
// A description that does not match is SILENT: it never appears in the
// document, the endpoint stays bodyless, and the schema is still served as
// valid JSON. The proof can only be produced here — besides the modules, the
// set also contains the routes the plugins bring, and the full tree is only
// wired up on a running server.
func TestSchemaDescriptionsMatchRealRoutes(t *testing.T) {
	// The document is regenerated on every request; the match record is filled
	// during that generation too. That is why the read is triggered by making a
	// request FIRST.
	schemaDocument(t)

	assert.Empty(t, testDoc.UnmatchedDescriptions(),
		"every described endpoint must be found in the router tree; an unmatched record means a route whose path has changed or that has been deleted")
}

// reservedNames are the names of the shared components the core PUBLISHES.
//
// It is kept only to answer the question "are there derived schemas at all".
// "List" is NOT here: the untyped list envelope used to be published, but no
// endpoint referred to it and a real client generator reported it as an "unused
// model" — a dead class in every generated client. The name is still RESERVED
// in the core (a module's DTO named "List" would produce a generic name that
// means nothing in the published contract), it is only not published.
var reservedNames = []string{"Error"}

// TestSchemaCarriesRootKeysAndSharedComponents verifies the document's skeleton.
//
// If the skeleton is missing, the schema becomes "syntactically valid but
// unreadable": no generator can parse a document without version information,
// and if components is missing every $ref breaks.
func TestSchemaCarriesRootKeysAndSharedComponents(t *testing.T) {
	_, doc := schemaDocument(t)

	assert.Equal(t, openapi.Version, doc["openapi"], "the document must declare the OpenAPI version")

	info := objectField(t, doc, "info", "document")
	assert.NotEmpty(t, info["title"], "info.title must be filled in")
	assert.NotEmpty(t, info["version"], "info.version must be filled in")

	assert.NotEmpty(t, objectField(t, doc, "paths", "document"), "paths must be filled in")

	schemas := schemaComponents(t, doc)

	// The shared error envelope: every endpoint refers to it for 401/422/429/500.
	errSchema := objectField(t, schemas, "Error", "components/schemas")
	innerError := objectField(t, objectField(t, errSchema, "properties", "Error"), "error", "Error.properties")
	assert.ElementsMatch(t, []string{"code", "message"}, stringSlice(t, innerError["required"]),
		"a code and a message are ALWAYS present in the error body")

	errorFields := objectField(t, innerError, "properties", "Error.error")
	mustBeSubset(t, []string{"code", "message", "request_id", "details"}, anahtarlar(errorFields),
		"the fields of the error envelope must be described in full")

	// The untyped list envelope is NOT PUBLISHED (see [reservedNames]); the
	// shape of the list envelope is written INLINE at every endpoint together
	// with the record type, and that shape is audited by
	// TestSchemaDescribesEndpointsWithTheirBodies.
	assert.NotContains(t, schemas, "List",
		"an unused generic component must not be published; it becomes a dead class in every client")

	security := objectField(t, objectField(t, doc, "components", "document"), "securitySchemes", "components")
	mustBeSubset(t, []string{"bearerAuth", "publishableKey"}, anahtarlar(security),
		"the security scheme of both surfaces must be defined")

	// Derived schemas must stand BESIDE the shared ones: if only Error and List
	// are there, the body description has not run at all, and the $ref test in
	// this file would audit nothing either.
	assert.Greater(t, len(schemas), len(reservedNames),
		"there must also be component schemas derived from the bodies; present: %v", anahtarlar(schemas))
}

// TestEverySchemaRefResolves verifies that all references arrive at a
// definition.
//
// An unresolved ref blows the client generator up AT RUNTIME and is not noticed
// by looking at the document with the naked eye: the JSON is valid, the
// endpoint is visible, only the type of one field points at a component that
// does not exist. A name collision emptying the schema (two modules with a DTO
// of the same name) is visible from here too.
func TestEverySchemaRefResolves(t *testing.T) {
	_, doc := schemaDocument(t)

	schemas := schemaComponents(t, doc)

	var refs []string

	collectRefs(doc, &refs)
	require.NotEmpty(t, refs,
		"precondition: the document must contain at least one $ref; with none, this test audits nothing")

	seen := map[string]struct{}{}

	for _, path := range refs {
		if _, repeat := seen[path]; repeat {
			continue
		}

		seen[path] = struct{}{}

		name := strings.TrimPrefix(path, refPrefix)
		require.NotEqual(t, path, name,
			"every reference in the document must point at the component schemas; %q points somewhere else", path)

		assert.Contains(t, schemas, name, "unresolved reference: %s", path)
	}

	t.Logf("resolved references: %d unique, %d component schemas", len(seen), len(schemas))
}

// TestLoginEndpointIsExplicitlyUnsecuredInSchema verifies that the endpoint
// handing out the token does not ask for a token.
//
// The distinction is subtle and its cost is large: had the field NOT BEEN
// WRITTEN AT ALL, the operation would count as "unspecified" and would INHERIT
// the root-level default security; the client generator would then produce a
// method that asks for a token in order to log in, that is, one that can never
// be called. An empty array, on the other hand, means "this endpoint is
// explicitly unsecured" and OVERRIDES the default.
//
// For the empty array to mean anything, a full one has to be seen as well; that
// is why the test compares one endpoint from each of the two surfaces.
func TestLoginEndpointIsExplicitlyUnsecuredInSchema(t *testing.T) {
	_, doc := schemaDocument(t)

	login := findOperation(t, doc, http.MethodPost, authapi.LoginPath)

	value, written := login["security"]
	require.True(t, written,
		"the security field on the login endpoint must be WRITTEN; an unwritten field inherits the root default")
	require.NotNil(t, value, "security cannot be null; null comes down to the same thing as 'unspecified'")

	list, ok := value.([]any)
	require.True(t, ok, "security must be an array, found %T", value)
	assert.Empty(t, list, "an empty array means 'this endpoint is explicitly unsecured'")

	// That does not mean login PRODUCES no 401: a wrong e-mail/password is still
	// a 401 and the client must handle that branch.
	responses := objectField(t, login, "responses", "login endpoint")
	assert.Contains(t, responses, "401", "an unsecured endpoint must report credential errors too")

	admin := findOperation(t, doc, http.MethodGet, "/admin/v1/users")
	assert.Equal(t, []any{map[string]any{"bearerAuth": []any{}}}, admin["security"],
		"the admin endpoint must ask for a session token")

	store := findOperation(t, doc, http.MethodGet, "/store/v1/products")
	assert.Equal(t, []any{map[string]any{"publishableKey": []any{}}}, store["security"],
		"the storefront endpoint must ask for a publishable key")
}

// TestSchemaContainsNoRawRecordIDs verifies that the document publishes route
// PATTERNS, not live data.
//
// The test first opens a REAL cart: since the schema is generated from the
// running server, had it mixed with live data the new record's ID would have
// fallen into the document. An ID leaking into the schema breaks two things at
// once — the document publishes personal data, and the client generator writes
// a method pinned to the path of a single record.
func TestSchemaContainsNoRawRecordIDs(t *testing.T) {
	cartID, _ := createCart(t)

	raw, doc := schemaDocument(t)
	text := string(raw)

	assert.Contains(t, objectField(t, doc, "paths", "document"), "/store/v1/carts/{id}",
		"the schema must publish the path PATTERN")
	assert.NotContains(t, text, cartID, "the ID of the cart just opened must not be in the schema")

	// Fixture IDs and API keys fall in the same scope: the latter are not merely
	// an "ID" but a SECRET, and the schema is a public document.
	for name, value := range map[string]string{
		"admin id":         adminID,
		"sales channel id": testChannelID,
		"region id":        taxedRegionID,
		"secret key":       secretKey,
		"publishable key":  publishableKey,
	} {
		require.NotEmpty(t, value, "precondition: the %s must be filled in", name)
		assert.NotContains(t, text, value, "the %s must not be in the schema", name)
	}

	assert.Empty(t, recordIDPattern.FindAllString(text, -1),
		"there must be no string in the schema shaped like a prefixed record ID")
}

// TestSchemaMatchesRealResponses verifies that the described schema overlaps
// with the body the server ACTUALLY writes.
//
// A full schema is not enough; it must be CORRECT as well. The schema is
// derived from the types while the response goes through the handler: between
// the two stands a converter (the code that maps to a DTO), and a field lost in
// there shows up in no unit test. The assertion runs in two directions:
//
//   - every field the server writes EXISTS in the schema — otherwise the
//     document describes it incompletely;
//   - every field the schema calls "required" EXISTS in the response — otherwise
//     the document gives a guarantee that does not exist and the client finds
//     that field empty when it reads it.
//
// The reverse direction (a field in the schema but not in the response) is NOT
// COUNTED as an error: a field carrying omitempty is not written at its zero
// value and does not appear as required in the schema either.
func TestSchemaMatchesRealResponses(t *testing.T) {
	_, doc := schemaDocument(t)

	cartID, createBody := createCart(t)

	t.Run("POST /store/v1/carts", func(t *testing.T) {
		fields, required := recordFields(t, doc, http.MethodPost, "/store/v1/carts", "201")
		actual := responseFields(t, createBody)

		mustBeSubset(t, actual, fields, "every field the server writes must be in the schema")
		mustBeSubset(t, required, actual, "every field the schema calls required must be in the response")
	})

	// The cart detail flattens an EMBEDDED DTO; that the field set comes out on
	// the wire in that same flattened form can only be seen here.
	t.Run("GET /store/v1/carts/{id}", func(t *testing.T) {
		recorder := magazaIstegi(t, "/store/v1/carts/"+cartID, publishableKey)
		require.Equal(t, http.StatusOK, recorder.Code, "the cart must be readable; body: %s", recorder.Body.String())

		fields, required := recordFields(t, doc, http.MethodGet, "/store/v1/carts/{id}", "200")
		actual := responseFields(t, recorder.Body.Bytes())

		mustBeSubset(t, actual, fields, "every field the server writes must be in the schema")
		mustBeSubset(t, required, actual, "every field the schema calls required must be in the response")
	})
}
