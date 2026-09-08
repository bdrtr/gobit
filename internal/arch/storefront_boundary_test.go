package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file holds ADR 0051 mechanically, which is the only way that decision is
// held at all.
//
// # What the decision says, and why prose could not carry it
//
// A storefront may accept content from a party it cannot identify only when the
// write is CONFINED or INERT. The review module is the worked example: a review
// is born `submitted`, the only way out is an admin endpoint, and the storefront
// therefore must not be able to name a status — not on the way in, where it would
// publish its own review, and not on the way out, where it would read somebody
// else's unapproved one.
//
// ADR 0051's decisive finding is that expressing that in the TYPE is not the same
// as CHECKING it: the guarantee drifted inside this very module within one
// release, and a godoc in the review models still describes a handler behavior
// the handler does not have. So the property is checked here, against the routes
// the router actually registers.
//
// # Why the population is resolved from PATHS and not from names
//
// The obvious scan is for handlers called store-something. Measured on
// 2026-09-08, that misses the payment module, whose two storefront handlers are
// createStoreSession and cancelStoreSession; and a scan for request types named
// storeSomethingRequest finds exactly two in the whole tree, which are the two
// worked examples the ADR already cites. Either would have produced a gate that
// passes because it is looking in the wrong place. The routes are therefore
// resolved by their PATH, through the constants the registrations use.

// operatorControlledFields are the request-body and query-parameter names a
// storefront must never carry.
//
// One entry, and it is the one the decision is about: a status is the state an
// OPERATOR moves. A storefront that can set it publishes its own content, and a
// storefront that can filter by it reads content nobody approved.
//
// The list is deliberately short. It is not a general list of dangerous names —
// it is the names this decision's discriminator turns on, and an addition to it
// should come with the decision that needs it.
var operatorControlledFields = []string{"status"}

// storefrontBoundaryExemptions names a route that carries one of those fields on
// purpose, with the reason.
//
// EMPTY, and an entry here should be rare enough to argue. A storefront write
// that legitimately names a status would be one where the status is not a
// moderation gate at all, and that is a claim worth writing down rather than
// waving through.
var storefrontBoundaryExemptions = map[string]string{}

// TestNoStorefrontWriteAcceptsAnOperatorControlledField is the WRITE boundary.
func TestNoStorefrontWriteAcceptsAnOperatorControlledField(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)

	require.NotEmpty(t, surface.writes,
		"no storefront write route was resolved anywhere in the module tree, which cannot be "+
			"true while the cart alone registers several; the route scan has gone BLIND and "+
			"this gate would pass whatever a handler accepted")
	require.NotEmpty(t, surface.decoded,
		"no storefront handler was found to decode a request body; the body scan has gone "+
			"BLIND, so every assertion below is being made about nothing")

	for _, route := range surface.writes {
		body, decodes := surface.decoded[route.key()]
		if !decodes {
			// A write that takes no body — a delete, or one whose whole input is
			// in the path. Nothing to check, and not a finding.
			continue
		}

		for _, field := range body.jsonFields {
			if !isOperatorControlled(field) {
				continue
			}
			if _, exempt := storefrontBoundaryExemptions[route.path]; exempt {
				continue
			}

			t.Errorf("%s %s decodes %s, whose %q field the STOREFRONT must not set.\n"+
				"ADR 0051: an unidentified writer's content is accepted only when the write is "+
				"CONFINED or INERT, and a status is the state an operator moves — a storefront "+
				"that can name it publishes its own content.\n"+
				"Remove the field from the request type, or add the route to "+
				"storefrontBoundaryExemptions with the reason this status is not a moderation "+
				"gate.", route.verb, route.path, body.name, field)
		}
	}
}

// TestNoStorefrontReadOffersAnOperatorControlledParameter is the READ boundary.
//
// It is the half the review module argues hardest: a listing that accepted a
// status parameter would let the storefront widen the predicate to rows the
// module publishes only on approval, and the SQL literal that keeps the read
// narrow would then be satisfied by a document nobody meant to publish.
func TestNoStorefrontReadOffersAnOperatorControlledParameter(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)

	require.NotEmpty(t, surface.reads,
		"no storefront read route was resolved; the route scan has gone BLIND")
	require.NotEmpty(t, surface.queried,
		"no storefront handler was found to read a query parameter at all, which cannot be "+
			"true while the catalog listing pages; the parameter scan has gone BLIND")

	for _, route := range surface.reads {
		for _, param := range surface.queried[route.key()] {
			if !isOperatorControlled(param) {
				continue
			}
			if _, exempt := storefrontBoundaryExemptions[route.path]; exempt {
				continue
			}

			t.Errorf("%s %s reads the %q query parameter, which the STOREFRONT must not offer.\n"+
				"ADR 0051: a storefront read may not widen its predicate to rows the module "+
				"publishes only on approval. A handler that takes a status can be pointed at "+
				"content nobody approved, whatever the query underneath filters by.\n"+
				"Remove the parameter, or add the route to storefrontBoundaryExemptions with "+
				"the reason.", route.verb, route.path, param)
		}
	}
}

// proofFunction is the one comparison every storefront surface naming a
// customer has to reach.
//
// It is a NAME rather than a package-qualified symbol; [calleeName] says why.
// If it is ever renamed this gate fails everywhere at once, which is the loud
// half of the failure — the silent half would be a gate that found nothing and
// passed, and the blindness floor below is what refuses that.
const proofFunction = "ProvenCustomer"

// customerResourcePath is the storefront path whose OWN resource is a customer.
//
// The other two spellings of the claim are structural — a "{customer_id}" path
// parameter, or a "customer_id" field in the decoded body — and this one is
// not: the address book's routes read the customer out of a segment called
// "id", because the customer IS the resource there. A scan that only looked for
// the structural spellings would miss the eight routes ADR 0043 closed, and a
// gate blind to the worked example is a gate that cannot notice it regressing.
const customerResourcePath = "/store/v1/customers/{id}"

// TestNoStorefrontSurfaceActsOnACustomerItCannotProve holds ADR 0057 tree-wide.
//
// # What the property is, exactly
//
// A storefront route that names a customer must REACH corehttp.ProvenCustomer.
// Not "must refuse" and not even "must compare": the two surfaces ADR 0057
// added consult the comparison only when there is something to consult it with
// — a non-empty claim on the cart, a bound identity on both — and both of those
// conditions are decisions argued in the record rather than properties held
// here.
//
// What IS held here is the one thing a copy would break: that no storefront
// handler naming a customer decides the claim with an answer of its own. Three
// copies of an authorization rule are three chances for one to answer
// differently while still answering, and that is the failure this record was
// written for. [TestNoAPIPackageDefinesItsOwnProof] is the other half — reaching
// a LOCAL function of the same name would satisfy the walk below, because
// [calleeName] drops the package qualifier on purpose.
//
// # Where the population comes from
//
// From the routes and the request types, both of which are independent of the
// property: a handler that stopped proving anything would still register its
// route and still decode its body, so a violation makes this gate FAIL rather
// than shrinking the set it looks at. That is the rule the plugin-ownership
// audit next door learned the hard way — a population derived from the property
// lets a subject leave the audit by becoming the thing it looks for.
//
// # What it does NOT cover
//
// Plugins. The scan walks internal/modules/*/api, and plugins/webpush mounts a
// storefront POST whose body carries a customer_id — the same class, a
// different tree, and a defect ADR 0051 records for reasons of its own (it is a
// standing authority, which proving the claim would not fix). Widening the walk
// to plugins/ would fail this gate on a defect it cannot close, so the row
// stays where it is decided.
func TestNoStorefrontSurfaceActsOnACustomerItCannotProve(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)
	proving := functionsReachingTheProof(surface.calls)

	require.NotEmpty(t, proving,
		"no function in any module api package reaches %s. Either the comparison was "+
			"renamed, or the call scan has gone BLIND — and blind, this gate passes "+
			"whatever a handler believes about the customer a request names",
		proofFunction)

	var named []storefrontRoute
	for _, route := range slices.Concat(surface.reads, surface.writes) {
		if routeNamesACustomer(route, surface.decoded[route.key()]) {
			named = append(named, route)
		}
	}

	require.NotEmpty(t, named,
		"no storefront route naming a customer was resolved, which cannot be true while "+
			"the customer module alone registers eight and the cart's creation body "+
			"carries a customer_id. The route scan has gone BLIND")

	for _, route := range named {
		if slices.Contains(proving, route.key()) {
			continue
		}

		t.Errorf("%s %s names a customer and never reaches %s.\n"+
			"ADR 0057: a storefront surface that names a customer has to put the claim "+
			"to the installation's bound identity, through the ONE comparison. The "+
			"identifier is not a secret — it travels in cart and order response bodies "+
			"— so a route that decides the claim by itself answers for anybody who has "+
			"seen one.\n"+
			"Reach corehttp.ProvenCustomer from the handler (directly or through this "+
			"package's own helper), or, if this route genuinely names nobody, stop it "+
			"carrying a customer identifier.",
			route.verb, route.path, proofFunction)
	}
}

// TestNoAPIPackageDefinesItsOwnProof closes the loophole [calleeName] leaves.
//
// The walk above matches a BARE callee name, deliberately: an api package that
// aliased the core import differently would otherwise fall out of it, and
// falling out means passing. The cost is that a function named ProvenCustomer
// defined inside an api package would satisfy it — which is precisely the
// second, silently diverging copy of an authorization rule ADR 0057 exists to
// prevent, wearing the name of the thing it replaced.
//
// So the two run together: one says every route reaches something with that
// name, this one says nothing with that name is declared where a route could
// reach it instead.
func TestNoAPIPackageDefinesItsOwnProof(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)

	require.NotEmpty(t, surface.declared,
		"no function was found declared in any module api package, which cannot be true "+
			"while every handler is one; the declaration scan has gone BLIND")

	for _, declared := range surface.declared {
		module, name, _ := strings.Cut(declared, ".")
		if name != proofFunction {
			continue
		}

		t.Errorf("the %s module's api package declares its own %s.\n"+
			"ADR 0057: the comparison is corehttp.ProvenCustomer's and is shared, because "+
			"a copy of an authorization rule keeps answering after it drifts. A local one "+
			"under the same name also satisfies "+
			"TestNoStorefrontSurfaceActsOnACustomerItCannotProve, so the copy would be "+
			"invisible there.\n"+
			"Call the core's function, or give this one a name that says what it does "+
			"instead.", module, proofFunction)
	}
}

// routeNamesACustomer reports whether a route identifies a customer whose claim
// a caller could make about somebody else.
func routeNamesACustomer(route storefrontRoute, body requestBody) bool {
	return strings.Contains(route.path, "{customer_id}") ||
		strings.HasPrefix(route.path, customerResourcePath) ||
		slices.Contains(body.jsonFields, "customer_id")
}

// functionsReachingTheProof returns every function that reaches the comparison,
// directly or through its own package's helpers.
//
// It is a fixpoint rather than one hop because both modules that took the
// contract factored the call into a helper, and a gate that demanded the call
// inline would have failed the correct arrangement and rewarded a copied one.
func functionsReachingTheProof(calls map[string][]string) []string {
	reaching := map[string]bool{}
	for caller, callees := range calls {
		if slices.Contains(callees, proofFunction) {
			reaching[caller] = true
		}
	}

	for grew := true; grew; {
		grew = false
		for caller, callees := range calls {
			if reaching[caller] {
				continue
			}
			module, _, _ := strings.Cut(caller, ".")
			for _, callee := range callees {
				if reaching[module+"."+callee] {
					reaching[caller] = true
					grew = true

					break
				}
			}
		}
	}

	out := make([]string, 0, len(reaching))
	for caller := range reaching {
		out = append(out, caller)
	}
	slices.Sort(out)

	return out
}

// isOperatorControlled reports whether a field or parameter name is one the
// storefront must not carry.
func isOperatorControlled(name string) bool {
	lowered := strings.ToLower(name)

	return slices.Contains(operatorControlledFields, lowered) ||
		strings.HasSuffix(lowered, "_status")
}

// storefrontRoute is one registered /store/v1 route.
//
// The module is the directory under internal/modules the registration was read
// out of, and it is carried because the SCHEMA half of ADR 0051 needs it: a
// route says nothing about a table on its own, and the module is the first hop
// of the join from one to the other (see storefront_schema_test.go). It is
// read from the FILE rather than guessed from the path, because a storefront
// path names a resource and not an owner — the review module's write is
// registered under /store/v1/products.
type storefrontRoute struct {
	verb    string
	path    string
	handler string
	module  string
}

// key is how a route is looked up in the per-handler maps of [storefrontScan]
// and in the route-to-table map next door.
//
// It is MODULE-QUALIFIED, and the bare handler name is not enough for the reason
// Go itself gives: a handler is a method of one api package, so two modules may
// each register a storeCreate and neither is shadowing the other. Keyed by the
// bare name, one route's decoded body or resolved table set would silently
// overwrite the other's, and the gate would then pass on the wrong module's
// evidence while the losing route went unaudited. All twenty-two storefront
// write handler names happen to be distinct today, measured 2026-09-08; that is
// a coincidence of the tree rather than a property of it.
func (r storefrontRoute) key() string { return r.module + "." + r.handler }

// requestBody is a struct a handler decodes, and the JSON names of its fields.
type requestBody struct {
	name       string
	jsonFields []string
}

// storefrontScan is everything the gates here need, read once.
//
// decoded, queried and calls are keyed by [storefrontRoute.key] —
// "<module>.<handler>" — and never by the bare handler name; the method says
// why.
type storefrontScan struct {
	writes  []storefrontRoute
	reads   []storefrontRoute
	decoded map[string]requestBody
	queried map[string][]string
	// calls is the name of every function each function in an api package
	// calls, module-qualified on the CALLER. It is what lets the identity gate
	// follow a handler through its own package's helper — the address book
	// reaches the comparison through storeCustomerID, the cart through
	// provenCustomer, and requiring the call to be inline in the handler would
	// have failed the two modules that factored it correctly.
	calls map[string][]string
	// declared is every function and method DECLARED in an api package,
	// module-qualified. It is what lets a LOCAL redefinition of the proof be
	// seen: calls cannot tell one apart from the core's, because calleeName
	// drops the package qualifier.
	declared []string
}

// storefrontSurface parses the module api packages and resolves the storefront
// routes, the bodies their handlers decode and the parameters they read.
//
// It parses rather than greps for the reason the other audits here do: a path
// written in a comment is not a registration, and a struct named in prose is not
// a body.
func storefrontSurface(t *testing.T) storefrontScan {
	t.Helper()

	fset := token.NewFileSet()
	files := []*ast.File{}
	owner := map[*ast.File]string{}

	root := filepath.Join(repoRoot, modulesDir)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "api" {
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		files = append(files, parsed)
		owner[parsed] = filepath.Base(filepath.Dir(filepath.Dir(path)))

		return nil
	})
	require.NoError(t, err, "the module api packages could not be walked")
	require.NotEmpty(t, files, "no api package was parsed; the scan has gone BLIND")

	paths := storefrontPathConstants(files)
	structs := structJSONFields(files, owner)
	scan := storefrontScan{
		decoded: map[string]requestBody{},
		queried: map[string][]string{},
		calls:   map[string][]string{},
	}

	for _, file := range files {
		module := owner[file]
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				collectRoute(typed, module, paths, &scan)
			case *ast.FuncDecl:
				collectHandlerReads(typed, module, structs, &scan)
			}

			return true
		})
	}

	return scan
}

// collectRoute records a router registration whose path is a storefront one.
func collectRoute(call *ast.CallExpr, module string, paths map[string]string, scan *storefrontScan) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) < 2 {
		return
	}

	verb := selector.Sel.Name
	if !slices.Contains([]string{"Get", "Post", "Put", "Patch", "Delete"}, verb) {
		return
	}

	path, named := routePath(call.Args[0], paths)
	if !named || !strings.HasPrefix(path, "/store/v1") {
		return
	}

	handler, ok := call.Args[1].(*ast.SelectorExpr)
	if !ok {
		return
	}

	route := storefrontRoute{
		verb:    strings.ToUpper(verb),
		path:    path,
		handler: handler.Sel.Name,
		module:  module,
	}
	if verb == "Get" {
		scan.reads = append(scan.reads, route)

		return
	}

	scan.writes = append(scan.writes, route)
}

// routePath resolves a route argument to its literal path.
func routePath(arg ast.Expr, paths map[string]string) (string, bool) {
	switch typed := arg.(type) {
	case *ast.Ident:
		path, known := paths[typed.Name]

		return path, known
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(typed.Value)

		return unquoted, err == nil
	}

	return "", false
}

// storefrontPathConstants indexes the constants whose value is a storefront path.
func storefrontPathConstants(files []*ast.File) map[string]string {
	out := map[string]string{}

	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}

			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}

				literal, isLit := spec.Values[i].(*ast.BasicLit)
				if !isLit || literal.Kind != token.STRING {
					continue
				}

				value, err := strconv.Unquote(literal.Value)
				if err == nil && strings.HasPrefix(value, "/store/v1") {
					out[name.Name] = value
				}
			}

			return true
		})
	}

	return out
}

// structJSONFields indexes every struct type by "<module>.<name>", with the JSON
// names of its fields.
//
// The module qualifier is not decoration. A request type is unexported and lives
// in ONE api package, so its name is unique where Go looks it up and reused
// freely across the tree: measured on 2026-09-08, nine struct names occur in two
// module api packages each, addressRequest among them — the cart's carries
// source_address_id and the customer's carries is_default_shipping, and they are
// different types. An index keyed by the bare name would resolve one handler's
// body to the other module's struct on whichever file the walk parsed last, and
// the consequence lands next door: storefront_schema_test.go reads this to
// decide whether a party column arrived in a request BODY, so a shadowed struct
// would empty that evidence and the CONFINED refusal would stop firing with
// nothing noticing. [TestTheBodyScanIsKeyedByModule] holds the qualifier.
func structJSONFields(files []*ast.File, owner map[*ast.File]string) map[string][]string {
	out := map[string][]string{}

	for _, file := range files {
		module := owner[file]
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok {
				return true
			}

			structType, isStruct := spec.Type.(*ast.StructType)
			if !isStruct {
				return true
			}

			names := []string{}
			for _, field := range structType.Fields.List {
				name := jsonFieldName(field)
				if name != "" {
					names = append(names, name)
				}
			}
			out[module+"."+spec.Name.Name] = names

			return true
		})
	}

	return out
}

// jsonFieldName returns the JSON name a field is decoded under, falling back to
// the Go name when there is no tag.
func jsonFieldName(field *ast.Field) string {
	if field.Tag != nil {
		unquoted, err := strconv.Unquote(field.Tag.Value)
		if err == nil {
			name, _, _ := strings.Cut(reflect.StructTag(unquoted).Get("json"), ",")
			if name != "" {
				return name
			}
		}
	}

	if len(field.Names) == 0 {
		return ""
	}

	return field.Names[0].Name
}

// collectHandlerReads records, for one handler, the body it decodes and the
// query parameters it reads.
func collectHandlerReads(
	fn *ast.FuncDecl, module string, structs map[string][]string, scan *storefrontScan,
) {
	if fn.Body == nil {
		return
	}

	name := module + "." + fn.Name.Name
	scan.declared = append(scan.declared, name)

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.DeclStmt:
			// `var body storeSubmitRequest` — the decode idiom in this tree.
			generic, ok := typed.Decl.(*ast.GenDecl)
			if !ok || generic.Tok != token.VAR {
				return true
			}

			for _, spec := range generic.Specs {
				value, isValue := spec.(*ast.ValueSpec)
				if !isValue || value.Type == nil {
					continue
				}

				ident, isIdent := value.Type.(*ast.Ident)
				if !isIdent {
					continue
				}

				if fields, known := structs[module+"."+ident.Name]; known {
					scan.decoded[name] = requestBody{name: ident.Name, jsonFields: fields}
				}
			}
		case *ast.CallExpr:
			if param, ok := queryParameterName(typed); ok {
				scan.queried[name] = append(scan.queried[name], param)
			}
			if callee, ok := calleeName(typed); ok {
				scan.calls[name] = append(scan.calls[name], callee)
			}
		}

		return true
	})
}

// calleeName returns the name of the function a call names.
//
// Selector and plain identifier are both reduced to the BARE name, which is
// what the identity gate's fixpoint looks up: corehttp.ProvenCustomer,
// h.storeCustomerID and pathParam all arrive as one word. The package qualifier
// is dropped deliberately — an api package that aliased the core import
// differently would otherwise fall out of the walk, and falling out means
// passing.
func calleeName(call *ast.CallExpr) (string, bool) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name, true
	case *ast.Ident:
		return fn.Name, true
	}

	return "", false
}

// queryParameterName returns the parameter a `Query().Get("x")` call reads.
func queryParameterName(call *ast.CallExpr) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Get" || len(call.Args) != 1 {
		return "", false
	}

	inner, isCall := selector.X.(*ast.CallExpr)
	if !isCall {
		return "", false
	}

	innerSelector, isSelector := inner.Fun.(*ast.SelectorExpr)
	if !isSelector || innerSelector.Sel.Name != "Query" {
		return "", false
	}

	literal, isLit := call.Args[0].(*ast.BasicLit)
	if !isLit || literal.Kind != token.STRING {
		return "", false
	}

	value, err := strconv.Unquote(literal.Value)

	return value, err == nil
}
