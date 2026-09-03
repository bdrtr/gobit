// Package openapi produces an OpenAPI 3.1 schema from the running router.
//
// # Why from the router
//
// A hand-written schema inevitably drifts from the code: a route is deleted and
// stays in the schema; a path changes and the schema keeps the old one. The
// paths produced here are read from chi's REAL route tree, so they are always
// what the server is serving at that moment.
//
// # The body schemas are derived too
//
// Just as the path and the method are read from the router, the request and
// response BODIES are derived from the Go TYPES through reflection (see
// [Doc.SchemaOf]). The reason is the same: a hand-written field list falls
// behind the day a field is added to the DTO and nobody notices.
//
// The runtime CANNOT KNOW which type a handler reads and which it writes; the
// module makes that connection. A module describes its own endpoints through the
// [Describer] interface, binds them to a route with [Doc.Describe] and produces
// the body schema FROM THE TYPE with [Doc.Item], [Doc.List] and
// [Doc.RequestBody].
//
// # What it does not cover
//
// An endpoint that was not described carries only its path, method, security and
// the shared error responses; it has no body. Writing the boundary down is
// deliberate: saying "we produce OpenAPI" and serving a bodyless schema would
// have a client developer trust the schema and send the wrong field names.
// Knowing it is incomplete beats believing it is not.
package openapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Version is the OpenAPI version of the produced document.
const Version = "3.1.0"

// codeDocumentUnavailable reports that the document could not be produced.
//
// It is the ONLY detail that goes to the client: the text of a build error
// carries the PACKAGE PATHS of the clashing types (see [Doc.reportClash]) and
// this endpoint is unauthenticated. The whole reason goes to the log (see
// corehttp.WriteError).
const codeDocumentUnavailable = "openapi_document_unavailable"

// adminPrefix is the path prefix of the admin API.
const adminPrefix = "/admin/v1"

// storePrefix is the path prefix of the store API.
const storePrefix = "/store/v1"

// bearerScheme is the name of the admin endpoints' security scheme.
const bearerScheme = "bearerAuth"

// publishableScheme is the name of the store endpoints' security scheme.
const publishableScheme = "publishableKey"

// loginPath is the admin endpoint that needs NO authentication.
//
// It is the only way to get a token; were it protected nobody could sign in. It
// has to look unprotected in the schema too, or client generators produce a
// method that cannot be called without a token.
//
// Being unprotected does NOT mean PRODUCING NO 401: a wrong email/password is
// still a 401 (see [defaultResponses]).
const loginPath = adminPrefix + "/auth/login"

// Operation is the description of a single path+method operation.
type Operation struct {
	// Summary is the one-line summary of the operation.
	Summary string `json:"summary,omitempty"`
	// Description is the detailed description of the operation.
	Description string `json:"description,omitempty"`
	// OperationID is the id client generators use as the method name.
	OperationID string `json:"operationId,omitempty"`
	// Tags are the tags grouping the operation (usually the module name).
	Tags []string `json:"tags,omitempty"`
	// Parameters are the path and query parameters.
	Parameters []Parameter `json:"parameters,omitempty"`
	// RequestBody is the schema of the request body; it may be nil.
	RequestBody map[string]any `json:"requestBody,omitempty"`
	// Responses maps a status code to a response definition.
	Responses map[string]any `json:"responses"`
	// Security is this operation's security requirement.
	//
	// omitempty is DELIBERATELY absent. In OpenAPI an empty array ("security: []")
	// means "this endpoint is explicitly unprotected"; omitempty would never write
	// the empty array to JSON and an operation without the field counts as
	// "unspecified" and INHERITS the root-level default security. At the login
	// endpoint the meaning intended is exactly the opposite: the endpoint that
	// hands out the token cannot demand one. Not writing the record would silently
	// show the login endpoint as protected the day a root default was added.
	//
	// Left nil it would write "security": null into the JSON; [Doc.operation]
	// therefore fills the field for every operation and [security] never returns nil.
	Security []map[string][]string `json:"security"`
}

// Parameter is a path or a query parameter.
type Parameter struct {
	// Name is the parameter's name.
	Name string `json:"name"`
	// In is where the parameter sits: "path" | "query" | "header".
	In string `json:"in"`
	// Required is whether the parameter is required.
	Required bool `json:"required"`
	// Schema is the parameter's type schema.
	Schema map[string]any `json:"schema"`
	// Description is the parameter's description.
	Description string `json:"description,omitempty"`
}

// Doc is the produced OpenAPI document.
type Doc struct {
	// enrichment maps the "METHOD PATH" key to the operation detail.
	enrichment map[string]Operation
	// title is the API title.
	title string
	// version is the API version.
	version string
	// schemas are the component schemas derived from the Go types.
	schemas map[string]any
	// schemaOwners holds which Go type every component name came from; it is the
	// only record that catches a name clash.
	schemaOwners map[string]reflect.Type
	// schemaClashes is the report of DIFFERENT types wanting the same component name.
	schemaClashes []string
	// describeVersion is the version the description records are at.
	//
	// [Doc.Describe] and the component registration ([Doc.structSchemaOrRef])
	// increment it; it goes into the VALIDITY key of [Doc.Handler]'s cache. The
	// description API is for setup and is SINGLE THREADED (modules call Describe at
	// the composition root, before the server starts listening); the production
	// side is concurrent and only READS this field under [Doc.mu].
	describeVersion uint64
	// mu guards the document BUILD, the seen field and the cache.
	//
	// The build is under the lock from start to end because, while it looks like a
	// read, it MUTATES: [Doc.operation] writes the shared error responses into the
	// described operation's Responses map. Two concurrent requests to
	// /openapi.json are ordinary, and two unlocked builds would write into the
	// same map at the same time — in Go that is an unrecoverable runtime error.
	mu sync.Mutex
	// seen are the route keys found in the last build; UnmatchedDescriptions
	// reads it.
	seen map[string]struct{}
	// cache is the ENCODED form of the last produced document (see [Doc.Handler]).
	cache *cacheEntry
}

// documentIdentity reduces the INPUTS the document was built from to a single
// comparable value.
//
// The cache's validity rests on this value rather than on an assumption ("the
// tree is frozen now"); if one of the inputs changes the document is rebuilt.
type documentIdentity struct {
	// routeHash is the hash of the "METHOD PATH" pairs in the tree.
	routeHash uint64
	// routeCount is the number of routes in the tree.
	//
	// Because the hash is combined ORDER-INDEPENDENTLY (XOR) it could on its own
	// reduce two different sets to the same value; the count closes that in practice.
	routeCount int
	// describeVersion is [Doc.describeVersion] as it stood when the document was read.
	describeVersion uint64
}

// cacheEntry holds the produced document and the input it came from.
type cacheEntry struct {
	// identity is the identity of the inputs the body was produced from.
	identity documentIdentity
	// body is the encoded document; it is nil when the build failed.
	body []byte
	// err is the error kept when the build failed.
	//
	// The error is CACHED too: the same input produces the same error, and
	// rebuilding on every request would make a broken document MORE EXPENSIVE than
	// a sound one.
	err error
}

// New builds an empty document.
func New(title, version string) *Doc {
	return &Doc{
		enrichment:   make(map[string]Operation),
		title:        title,
		version:      version,
		schemas:      make(map[string]any),
		schemaOwners: make(map[string]reflect.Type),
	}
}

// Describer is the OPTIONAL interface of modules that can describe their own
// endpoints.
//
// # Why it was not added to module.Module
//
// Putting the method on the module contract was a change that broke EVERY module
// at once and gave nothing for the price: an undescribed module is a VALID
// model — it appears in the document with its path, method and security, only
// without a body. A required method would have produced nothing but a crop of
// empty Describe implementations.
//
// # Who calls it
//
// The composition root (cmd/server) calls it through a type assertion over the
// module list. The core cannot: [Doc] does not know the modules (Principle 2.4)
// and the only place that sees the module list is the setup.
type Describer interface {
	// Describe writes the module's endpoints into the document (with [Doc.Describe]).
	Describe(d *Doc)
}

// Describe records the operation details of a route.
//
// method and pattern have to be given as chi defines them (for example "GET",
// "/store/v1/products/{id}"). A record that matches nothing is NOT ignored
// silently — [Doc.Build] reports it through [Doc.UnmatchedDescriptions];
// otherwise the description of a route whose path changed would vanish quietly.
//
// A record also INVALIDATES a document already built (see [Doc.Handler]): an
// endpoint described after setup would otherwise not appear in the cached one.
func (d *Doc) Describe(method, pattern string, op Operation) {
	d.enrichment[key(method, pattern)] = op
	d.describeVersion++
}

// Build walks the router and produces the OpenAPI document.
//
// The build is under the lock from start to end; the reason is on [Doc.mu].
func (d *Doc) Build(r chi.Routes) (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.build(r)
}

// build produces the document and ASSUMES the [Doc.mu] lock is HELD.
func (d *Doc) build(r chi.Routes) (map[string]any, error) {
	paths := map[string]any{}
	seen := map[string]struct{}{}

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		path := normalizePath(route)
		if !included(path) {
			return nil
		}

		seen[key(method, path)] = struct{}{}

		operation := d.operation(method, path)

		existing, _ := paths[path].(map[string]any)
		if existing == nil {
			existing = map[string]any{}
			paths[path] = existing
		}

		existing[strings.ToLower(method)] = operation

		return nil
	})
	if err != nil {
		return nil, err
	}

	d.seen = seen

	// The clash check comes AFTER the walk: unmatched descriptions
	// ([Doc.UnmatchedDescriptions]) are a failure independent of a clash, and seen
	// has to be filled anyway so the operator can see both at once.
	if len(d.schemaClashes) > 0 {
		return nil, errors.Invalid(codeSchemaNameConflict,
			"an OpenAPI component name clashed: %s", strings.Join(d.schemaClashes, "; "))
	}

	return map[string]any{
		"openapi": Version,
		"info": map[string]any{
			"title":   d.title,
			"version": d.version,
		},
		"paths":      paths,
		"components": d.components(),
	}, nil
}

// UnmatchedDescriptions returns the descriptions that matched no route during
// [Doc.Build].
//
// A non-empty result means a route's path changed or was deleted while its
// description stayed. Staying silent would lead to an endpoint that is not in the
// document being described, or an existing one not being described.
func (d *Doc) UnmatchedDescriptions() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var missing []string

	for k := range d.enrichment {
		if _, ok := d.seen[k]; !ok {
			missing = append(missing, k)
		}
	}

	sort.Strings(missing)

	return missing
}

// operation builds the OpenAPI operation for a single route.
func (d *Doc) operation(method, path string) Operation {
	op := d.enrichment[key(method, path)]

	if op.OperationID == "" {
		op.OperationID = operationID(method, path)
	}

	if len(op.Tags) == 0 {
		if tag := tagFrom(path); tag != "" {
			op.Tags = []string{tag}
		}
	}

	// The path parameters are derived from the pattern; hand-written ones are kept.
	op.Parameters = mergeParameters(op.Parameters, pathParameters(path))

	if op.Responses == nil {
		op.Responses = map[string]any{}
	}

	for code, description := range defaultResponses(path) {
		if _, var_ := op.Responses[code]; !var_ {
			op.Responses[code] = description
		}
	}

	// Only nil is filled in: a hand-given EMPTY slice means "this endpoint is
	// explicitly unprotected" and overwriting it would invert the meaning.
	if op.Security == nil {
		op.Security = security(path)
	}

	return op
}

// key produces the key of the enrichment map.
func key(method, pattern string) string {
	return strings.ToUpper(method) + " " + pattern
}

// normalizePath turns the route string chi returns into an OpenAPI path.
//
// chi can leave "/*" remnants behind on nested Mounts; those are invalid in
// OpenAPI.
func normalizePath(route string) string {
	path := strings.ReplaceAll(route, "/*/", "/")
	path = strings.TrimSuffix(path, "/*")

	if path == "" {
		return "/"
	}

	return path
}

// included reports whether the path goes into the document.
//
// Only the versioned API surface is documented; /health and /ready are
// operational endpoints and client generators are not meant to produce methods
// for them.
func included(path string) bool {
	return strings.HasPrefix(path, adminPrefix) || strings.HasPrefix(path, storePrefix)
}

// tagFrom extracts the module tag from the path (e.g. "/store/v1/products" → "products").
func tagFrom(path string) string {
	remaining := strings.TrimPrefix(strings.TrimPrefix(path, adminPrefix), storePrefix)
	parts := strings.Split(strings.Trim(remaining, "/"), "/")

	if len(parts) == 0 || parts[0] == "" {
		return ""
	}

	return parts[0]
}

// operationID derives an operationId from the method and the path.
func operationID(method, path string) string {
	clean := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(path)

	return strings.ToLower(method) + clean
}

// pathParameters turns the {name} placeholders in the pattern into parameters.
func pathParameters(path string) []Parameter {
	var params []Parameter

	for _, part := range strings.Split(path, "/") {
		if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			continue
		}

		ad := strings.Trim(part, "{}")
		params = append(params, Parameter{
			Name:     ad,
			In:       "path",
			Required: true,
			Schema:   map[string]any{schemaType: typeString},
		})
	}

	return params
}

// mergeParameters merges the hand-written parameters with the derived ones.
//
// The hand-written one WINS: a generator cannot produce details like a
// description or an example.
func mergeParameters(byHand, derived []Parameter) []Parameter {
	known := make(map[string]struct{}, len(byHand))
	for _, p := range byHand {
		known[p.In+":"+p.Name] = struct{}{}
	}

	result := append([]Parameter(nil), byHand...)

	for _, p := range derived {
		if _, ok := known[p.In+":"+p.Name]; !ok {
			result = append(result, p)
		}
	}

	return result
}

// security returns the security requirement for a path.
//
// It NEVER returns nil: because [Operation.Security] carries no omitempty, a nil
// slice writes "security": null into the schema and that is invalid. An empty
// slice is both valid and meaningful — "this endpoint is explicitly unprotected".
func security(path string) []map[string][]string {
	switch {
	case path == loginPath:
		// An empty slice and nil are DIFFERENT: an empty slice means "this endpoint
		// is explicitly unprotected" and OVERRIDES the root-level security; nil
		// means "unspecified" and a reader assumes it inherits the root default.
		return []map[string][]string{}
	case strings.HasPrefix(path, adminPrefix):
		return []map[string][]string{{bearerScheme: {}}}
	case strings.HasPrefix(path, storePrefix):
		return []map[string][]string{{publishableScheme: {}}}
	default:
		// Only admin/store paths enter the document ([included]); for a path
		// landing here the right answer is "unprotected" too, not "unspecified".
		return []map[string][]string{}
	}
}

// The frequently repeated key and type names of JSON Schema.
//
// They are constants not because of the repetition itself but because a typo is
// SILENT: a map key written "propertes" compiles, the schema is produced, and it
// only comes out when a client reading the schema cannot find the field.
const (
	schemaType                 = "type"
	schemaProperties           = "properties"
	schemaRequired             = "required"
	schemaDescription          = "description"
	schemaItems                = "items"
	schemaAdditionalProperties = "additionalProperties"
	schemaFormat               = "format"
	schemaRef                  = "$ref"
	schemaAny                  = "anyOf"
	typeObject                 = "object"
	typeString                 = "string"
	typeInteger                = "integer"
	typeNumber                 = "number"
	typeArray                  = "array"
	typeBoolean                = "boolean"
	typeNull                   = "null"
	formatDateTime             = "date-time"
	formatByte                 = "byte"
	formatInt32                = "int32"
	formatInt64                = "int64"
	formatFloat                = "float"
	formatDouble               = "double"
)

// The names of the core's own shared components.
//
// They share the SAME namespace as the derived schemas; that is why the names
// are protected through [reservedSchemaNames].
const (
	schemaNameError = "Error"
	schemaNameList  = "List"
)

// defaultResponses returns the shared error responses every endpoint can produce.
//
// The login endpoint is NOT EXEMPTED from the 401. The endpoint is unprotected
// but its job is exactly to verify credentials, and it returns a 401 on a wrong
// email/password (the auth service produces errors.Unauthorized). Confusing
// "unprotected" with "produces no 401" would have a client generator produce a
func defaultResponses(path string) map[string]any {
	responses := map[string]any{
		"401": errorResponse("Authentication is missing or invalid"),
		"422": errorResponse("The input did not pass validation"),
		"429": errorResponse("The request limit was exceeded"),
		"500": errorResponse("An unexpected server error"),
	}

	if path == loginPath {
		// At the login endpoint a 401 is not "the token is missing" but "the
		// credentials are wrong". Failed attempts are not told apart, so the
		// description does not point at a single cause either (see auth adminLogin).
		responses["401"] = errorResponse("The email or the password is wrong")

		// A 403 is only meaningful at endpoints that HAVE an authorization step;
		// at login there is no identity yet, so there can be no insufficient right.
		return responses
	}

	if strings.HasPrefix(path, adminPrefix) {
		responses["403"] = errorResponse("Authenticated but the rights are not enough")
	}

	return responses
}

// errorResponse produces a response definition referring to the shared error envelope.
func errorResponse(description string) map[string]any {
	return Response(description, refSchema(schemaNameError))
}

// components returns the shared schemas, the derived schemas and the security
// definitions.
func (d *Doc) components() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			bearerScheme: map[string]any{
				schemaType:        "http",
				"scheme":          "bearer",
				"bearerFormat":    "JWT",
				schemaDescription: "The admin session token. It is obtained with /admin/v1/auth/login.",
			},
			publishableScheme: map[string]any{
				schemaType: "apiKey",
				"in":       "header",
				"name":     "x-publishable-api-key",
				schemaDescription: "Binds the store request to a sales channel. " +
					"It is NOT A SECRET; it is expected to be visible in the browser.",
			},
		},
		"schemas": d.schemaComponents(),
	}
}

// schemaComponents merges the core's shared schemas with the derived ones.
//
// The derived ones CANNOT overwrite the shared ones: [reservedSchemaNames]
// catches the clash back in [Doc.SchemaOf] and [Doc.Build] returns an error.
// The shared ones being written last here is a second line of defense — if that
// check is ever skipped, the error envelope's schema still does not break.
func (d *Doc) schemaComponents() map[string]any {
	schemas := make(map[string]any, len(d.schemas)+len(reservedSchemaNames))

	for ad, schema := range d.schemas {
		schemas[ad] = schema
	}

	schemas[schemaNameError] = map[string]any{
		schemaType:     typeObject,
		schemaRequired: []string{"error"},
		schemaProperties: map[string]any{
			"error": map[string]any{
				schemaType:     typeObject,
				schemaRequired: []string{"code", "message"},
				schemaProperties: map[string]any{
					"code":       map[string]any{schemaType: typeString},
					"message":    map[string]any{schemaType: typeString},
					"request_id": map[string]any{schemaType: typeString},
					"details":    map[string]any{schemaType: typeObject},
				},
			},
		},
	}

	// The UNTYPED list envelope is DELIBERATELY not published.
	//
	// It used to be written "for endpoints whose record schema is unknown", but
	// no endpoint referred to it; a real client generator (openapi-generator)
	// reported it as an "unused model" and it stood as a dead class in every
	// generated client.
	//
	// Attaching it by default to undescribed list endpoints was tempting too but
	// would be WRONG: even though the envelope shape is universal in this
	// repository, it cannot be written into the schema without VERIFYING that an
	// endpoint really returns a list. An unverified claim is worse than silence —
	// the client takes it for the truth.
	//
	// The name still STAYS in [reservedSchemaNames]: a module's DTO named "List"
	// would produce a generic name that means nothing in the published contract.

	return schemas
}

// Handler returns the handler serving the produced schema as JSON.
//
// # Why a cache
//
// Building the document is NOT cheap: the whole router tree is walked, an
// operation object (parameters, shared responses, security) is built for every
// route, the component schemas are copied and the result is encoded to JSON.
// Doing that on every request turns a small GET into the most expensive work in
// the process — and this endpoint can be mounted OUTSIDE the identity and quota
// gates.
//
// # Why not "once at startup"
//
// When the tree FREEZES is not something the core can know: modules bind their
// routes during bootstrap and plugins after that (see Registry.MountRoutes in
// the plugin package), and there is NO guarantee about the order this handler
// was registered in. Building once at startup would SILENTLY drop every route
// bound after the handler was registered — and not doing that is the document's
// whole reason to exist.
//
// So the cache rests not on an assumption but on the document's INPUTS: every
// request derives the tree's identity (a walk only; no operation is built, no
// encoding is done), combines it with the description version and compares it
// with the cached one. With the same input the encoded body is written as it
// is. The remaining cost is walking the tree and it is small next to the build;
// in exchange the cache stays correct in an installation that adds routes while
// running.
func (d *Doc) Handler(r chi.Routes) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, err := d.encodedDocument(r)
		if err != nil {
			// The error is NOT handed to the client RAW: its text carries the
			// PACKAGE PATHS of the clashing types and this endpoint can be called
			// without an identity. Wrapping it in KindInternal applies the core's
			// decision — the body is the shared error envelope, the message is
			// masked and the real reason is logged with the request id.
			corehttp.WriteError(req.Context(), w,
				errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
					"the openapi document could not be produced"))

			return
		}

		// The header and the status code are written through the core's gate: a nil
		// body means "headers and status only" in WriteJSON, so the Content-Type
		// decision is not defined a SECOND time here.
		corehttp.WriteJSON(req.Context(), w, http.StatusOK, nil)

		// The body is written directly because it is ALREADY ENCODED. Handed to the
		// core's writer (as a json.RawMessage) the document would be scanned and
		// copied once more per request; the measured price is large enough to take
		// back most of what the cache saves.
		if _, err := w.Write(body); err != nil {
			// The status code has already been sent (the client may have closed the
			// connection); the only thing left to do is record it — corehttp.WriteJSON
			// does the same.
			corehttp.LoggerFromContext(req.Context()).ErrorContext(req.Context(),
				"the openapi document could not be written",
				"error", err,
				"request_id", corehttp.RequestIDFromContext(req.Context()),
			)
		}
	}
}

// encodedDocument returns the document from the cache, rebuilding it if the inputs changed.
func (d *Doc) encodedDocument(r chi.Routes) ([]byte, error) {
	// The tree is walked OUTSIDE the lock: walking does not touch the document and
	// the lock is kept for the moment the build is really needed.
	identity, err := routeIdentity(r)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	identity.describeVersion = d.describeVersion

	if d.cache != nil && d.cache.identity == identity {
		return d.cache.body, d.cache.err
	}

	body, buildError := d.buildAndEncode(r)
	d.cache = &cacheEntry{identity: identity, body: body, err: buildError}

	return body, buildError
}

// buildAndEncode builds the document and encodes it to JSON; it ASSUMES the lock is HELD.
//
// The body is INDENTED and ends with a newline. Both match the behavior from
// before the cache and staying the same is deliberate: the document is a
// published CONTRACT, it is compared against recorded outputs (make
// openapi-schema), and a speed-up changing it byte for byte would make the
// change indistinguishable from a real schema change. The price of the
// indentation is now paid per build rather than per request.
func (d *Doc) buildAndEncode(r chi.Routes) ([]byte, error) {
	doc, err := d.build(r)
	if err != nil {
		return nil, err
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
			"the openapi document could not be encoded")
	}

	// MarshalIndent writes no newline; a json.Encoder would.
	return append(body, '\n'), nil
}

// routeIdentity reduces the CURRENT contents of the router tree to an identity.
//
// The hash is combined ORDER-INDEPENDENTLY (XOR). chi's walk order is
// deterministic today, but the price of depending on it is silent: were the
// order to change one day, the identity would differ on every request, the
// cache would never hit and nobody would notice because the output is still
// correct.
func routeIdentity(r chi.Routes) (documentIdentity, error) {
	var identity documentIdentity

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		hash := addToHash(addToHash(hashSeed, method), route)

		identity.routeHash ^= hash
		identity.routeCount++

		return nil
	})
	if err != nil {
		return documentIdentity{}, errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
			"the route tree could not be walked")
	}

	return identity, nil
}

// The constants of the FNV-1a 64-bit hash.
//
// The hash is written by hand because hash/fnv's interface ALLOCATES an object
// per route; since the identity is computed on every request, the allocation
// count would be multiplied directly by the route count. No cryptographic
// strength is wanted: the value only answers "did the input change", and the
// price of a collision is one document not being rebuilt — which is why the
// count ([documentIdentity.routeCount]) is compared TOGETHER with the hash.
const (
	hashSeed  uint64 = 14695981039346656037
	hashPrime uint64 = 1099511628211
)

// addToHash mixes a string into the current hash.
func addToHash(hash uint64, s string) uint64 {
	for i := range len(s) {
		hash ^= uint64(s[i])
		hash *= hashPrime
	}

	// A separator: "GET" + "/a" must not give the same hash as "GE" + "T/a".
	hash ^= uint64(' ')
	hash *= hashPrime

	return hash
}
