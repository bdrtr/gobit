package arch_test

import (
	"go/ast"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: A HANDLER DESCRIBES EVERY QUERY PARAMETER
// IT READS.
//
// The opposite direction was already guarded, per module, by tests shaped like
// TestStoreListDescribesOnlyParametersItReads, and their godoc states the fault
// they prevent: describing a parameter nothing reads promises the client a
// feature that does not work. This is the other half, and it was measured to be
// OPEN on 2026-09-06 — a branch reading "undocumented_switch" was planted in the
// storefront product listing and the whole repository stayed green.
//
// What it costs is not a broken promise but an UNADVERTISED one: a working
// switch that is absent from the document, from the generated client and from
// review. Nobody can audit a parameter they cannot see.
//
// # Why the per-module test could not catch it
//
// Neither of the two sides it compares is the handler. It reads the generated
// document and compares it against a list written by hand in the test, so a
// parameter missing from BOTH agrees with itself and passes. This audit takes
// its two sides from two different constructs — the reads from the handler's
// source, the descriptions from the code that builds the document — which is
// what makes the comparison say something about the world.
//
// # Scope, and it is derived rather than listed
//
// A package is in scope when it CONSTRUCTS an openapi.Parameter, that is, when
// it publishes a description at all. That rule selects exactly the module api
// packages and it needs no list to maintain.
//
// The known limit is the other side of the same rule: internal/adminui and the
// plugin HTTP surfaces read query parameters and describe nothing, so
// "described" is undefined for them and they are out of scope. A plugin that
// grows a document walks into scope by itself.

// queryParamScan is what one package's walk produced.
type queryParamScan struct {
	// readers goes from a function name to the index of the parameter it
	// forwards to URL.Query().Get.
	readers map[string]int
	// read are the query parameter names the package reads.
	read map[string]readSite
	// described are the QUERY parameter names the package puts in the document.
	//
	// Path parameters are deliberately not in it. Counting them would let a
	// read of "id" be excused by a path declaration of the same name, and it
	// would make every "{id}" route look like an unread description in the
	// other direction.
	described map[string]bool
	// parameterBuilders are the package's functions returning openapi.Parameter.
	parameterBuilders map[string]bool
}

// readSite remembers where a read was seen, so the failure can point at it.
type readSite struct {
	file string
	line int
}

// isQueryCall reports whether an expression is a "<something>.URL.Query()"
// call.
//
// The shape is matched STRUCTURALLY rather than by the receiver's name: the
// request is called "r" everywhere today, and a rule that said so would go
// blind the day somebody named it "req".
func isQueryCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}

	query, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || query.Sel.Name != "Query" {
		return false
	}

	url, ok := query.X.(*ast.SelectorExpr)

	return ok && url.Sel.Name == "URL"
}

// queryVariables collects the local variables a function assigns the query to.
//
// A FOURTH spelling, and the one that hid two real descriptions from the
// reverse direction until it was added: "query := r.URL.Query()" followed by
// "query.Get(name)". The set is collected PER FUNCTION rather than per file on
// purpose — a name that means the query in one function may mean something else
// in another, and over-matching here would not merely add noise, it would let a
// described-but-unread parameter look read.
func queryVariables(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || !isQueryCall(assign.Rhs[0]) {
			return true
		}
		for _, target := range assign.Lhs {
			if ident, ok := target.(*ast.Ident); ok {
				out[ident.Name] = true
			}
		}

		return true
	})

	return out
}

// isQueryReceiver reports whether an expression names the query: either the
// inline call or one of the local variables holding it.
func isQueryReceiver(vars map[string]bool, expr ast.Expr) bool {
	if isQueryCall(expr) {
		return true
	}
	ident, ok := expr.(*ast.Ident)

	return ok && vars[ident.Name]
}

// queryAccessIn reports the parameter NAME expression an access to the query
// string uses, or nil when the node is not such an access, with the enclosing
// function's query variables in scope.
//
// It has to know FOUR spellings, and they were counted rather than guessed:
// across the module api packages the repository writes Get(name) 56 times,
// the index form twice and Has(name) once, and the fulfillment eligibility read
// assigns the query to a local variable first. Reading only the first — which
// is what this did at first — made the audit report parameters that ARE read as
// unread, in two modules.
func queryAccessIn(vars map[string]bool, node ast.Node) ast.Expr {
	switch value := node.(type) {
	case *ast.IndexExpr:
		// The index form: the query map is subscripted by the name.
		if isQueryReceiver(vars, value.X) {
			return value.Index
		}
	case *ast.CallExpr:
		// The method forms: Get and Has both take the name as their argument.
		method, ok := value.Fun.(*ast.SelectorExpr)
		if !ok || len(value.Args) != 1 {
			return nil
		}
		if method.Sel.Name != "Get" && method.Sel.Name != "Has" {
			return nil
		}
		if isQueryReceiver(vars, method.X) {
			return value.Args[0]
		}
	}

	return nil
}

// stringLiteral unquotes a string literal expression, or returns "" for
// anything else.
func stringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind.String() != "STRING" {
		return ""
	}

	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}

	return value
}

// ownParameterIndex reports which of the function's own parameters an expression
// is, or -1.
func ownParameterIndex(fn *ast.FuncDecl, expr ast.Expr) int {
	ident, ok := expr.(*ast.Ident)
	if !ok || fn == nil || fn.Type.Params == nil {
		return -1
	}

	index := 0
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			if name.Name == ident.Name {
				return index
			}
			index++
		}
		if len(field.Names) == 0 {
			index++
		}
	}

	return -1
}

// returnsOpenAPIParameter reports whether the function's single result is an
// openapi.Parameter, resolving the package's LOCAL import name rather than
// assuming it is spelled "openapi".
func returnsOpenAPIParameter(file *sourceFile, fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}

	selector, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Parameter" {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	return strings.HasSuffix(file.imports[pkg.Name], "/openapi")
}

// isOpenAPIParameterType reports whether a type expression names
// openapi.Parameter, resolving the file's LOCAL import name for the package.
func isOpenAPIParameterType(file *sourceFile, expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Parameter" {
		return false
	}

	pkg, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}

	return strings.HasSuffix(file.imports[pkg.Name], "/openapi")
}

// buildsQueryParameter reports whether a Parameter-returning function produces
// a QUERY parameter.
//
// It is read from the literal the function returns rather than from its name:
// every module writes its own copy of this helper and they are not obliged to
// keep calling it queryParameter, but all of them have to write In: "query" for
// the document to be right.
func buildsQueryParameter(file *sourceFile, fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || !isOpenAPIParameterType(file, lit.Type) {
			return true
		}
		if fieldString(lit, "In") == "query" {
			found = true
		}

		return true
	})

	return found
}

// fieldString reads a string-literal field out of a composite literal.
func fieldString(lit *ast.CompositeLit, field string) string {
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}

		return stringLiteral(pair.Value)
	}

	return ""
}

// parameterNamesIn collects the Name values a composite literal describes.
//
// It has to handle TWO spellings, and the second one is why this function
// exists rather than a type check at the call site: a parameter written on its
// own is "openapi.Parameter{Name: …}", but a parameter written inside a slice
// has its type ELIDED — "[]openapi.Parameter{{Name: …}}" — so the inner literal
// carries no type at all. Reading only the first spelling made this audit
// report a parameter that IS described as undescribed, which its blindness test
// caught before the audit was believed.
func parameterNamesIn(file *sourceFile, lit *ast.CompositeLit, into map[string]bool) {
	if array, ok := lit.Type.(*ast.ArrayType); ok && isOpenAPIParameterType(file, array.Elt) {
		for _, element := range lit.Elts {
			inner, ok := element.(*ast.CompositeLit)
			if !ok {
				continue
			}
			nameField(inner, into)
		}

		return
	}

	if isOpenAPIParameterType(file, lit.Type) {
		nameField(lit, into)
	}
}

// nameField adds the literal's Name to the set, but only when the parameter
// sits in the QUERY. See [queryParamScan.described] for why a path parameter is
// not counted.
func nameField(lit *ast.CompositeLit, into map[string]bool) {
	if fieldString(lit, "In") != "query" {
		return
	}
	if name := fieldString(lit, "Name"); name != "" {
		into[name] = true
	}
}

// scanQueryParams collects, per package, what is read and what is described.
//
// It runs in two passes over each package, and the order is forced: a literal
// handed to a reader function ("stringParam(r, \"tag_id\")") can only be
// recognized once that function has been identified as a reader, and the
// function may be declared in another file of the same package.
func scanQueryParams(t *testing.T, tree *sourceTree) map[string]*queryParamScan {
	t.Helper()

	byPackage := map[string][]*sourceFile{}
	for _, file := range tree.files {
		byPackage[file.importPath] = append(byPackage[file.importPath], file)
	}

	out := map[string]*queryParamScan{}
	for importPath, files := range byPackage {
		scan := &queryParamScan{
			readers:           map[string]int{},
			read:              map[string]readSite{},
			described:         map[string]bool{},
			parameterBuilders: map[string]bool{},
		}

		// Pass one: which functions forward a parameter of their own to
		// URL.Query().Get, and which build an openapi.Parameter.
		for _, file := range files {
			for _, decl := range file.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				if returnsOpenAPIParameter(file, fn) && buildsQueryParameter(file, fn) {
					scan.parameterBuilders[fn.Name.Name] = true
				}
				vars := queryVariables(fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					argument := queryAccessIn(vars, node)
					if argument == nil {
						return true
					}
					if index := ownParameterIndex(fn, argument); index >= 0 {
						scan.readers[fn.Name.Name] = index
					}

					return true
				})
			}
		}

		// Pass two: the names themselves.
		for _, file := range files {
			for _, decl := range file.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}

				vars := queryVariables(fn)
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					if argument := queryAccessIn(vars, node); argument != nil {
						if name := stringLiteral(argument); name != "" {
							scan.read[name] = position(tree, file, node)
						}

						return true
					}

					switch value := node.(type) {
					case *ast.CompositeLit:
						parameterNamesIn(file, value, scan.described)
					case *ast.CallExpr:
						callee, ok := value.Fun.(*ast.Ident)
						if !ok {
							return true
						}
						if scan.parameterBuilders[callee.Name] && len(value.Args) > 0 {
							if name := stringLiteral(value.Args[0]); name != "" {
								scan.described[name] = true
							}

							return true
						}
						if index, isReader := scan.readers[callee.Name]; isReader && index < len(value.Args) {
							if name := stringLiteral(value.Args[index]); name != "" {
								scan.read[name] = position(tree, file, value)
							}
						}
					}

					return true
				})
			}
		}

		if len(scan.described) == 0 {
			// The package publishes no document, so "described" is undefined
			// for it. See the scope note at the head of this file.
			continue
		}
		out[importPath] = scan
	}

	return out
}

// position turns a node into the file:line a failure should point at.
func position(tree *sourceTree, file *sourceFile, node ast.Node) readSite {
	return readSite{file: file.path, line: tree.fset.Position(node.Pos()).Line}
}

// queryParamExemption is the justification of a query parameter that is read
// and deliberately not described.
type queryParamExemption struct {
	importPath string
	name       string
	reason     string
}

// queryParamExemptions are the reads this audit deliberately allows.
//
// Both entries are the same finding and the same decision. The audit found them
// on its first run: the shipping-option listing filters by a provider and by a
// price type and the document mentions neither. The module did not forget them
// — it leaves the WHOLE of the shipping-option CRUD undescribed, argued in its
// describe.go godoc, because optionDTO asks the document for the component
// name "Option" and the product module's models.Option asks for the same one; a
// collision brings down the whole of /openapi.json, and renaming either type
// breaks a published client.
//
// What is worth writing down is that the decision's REASON does not reach this
// far. The collision is about the response SCHEMA, and a query parameter never
// touches optionDTO — the core even supplies the shared error responses when an
// operation names none, so the filters could be described without naming a 2xx
// at all. Describing them was tried and refused by the module's own
// TestOptionEndpointsAreDeliberatelyUndescribed, which forbids even a summary:
// that test is STRICTER than the reason its file gives. Narrowing it is the
// module's decision to make, not this audit's, so the reads are exempted here
// and the tension is recorded rather than quietly resolved.
var queryParamExemptions = []queryParamExemption{
	{
		importPath: "github.com/bdrtr/gobit/internal/modules/fulfillment/api",
		name:       "provider_id",
		reason: "The shipping-option CRUD is undescribed by a recorded decision (a " +
			"component-name collision with the product module). The decision's reason " +
			"covers the body, not the filters; narrowing it belongs to that module.",
	},
	{
		importPath: "github.com/bdrtr/gobit/internal/modules/fulfillment/api",
		name:       "price_type",
		reason:     "As provider_id, on the same endpoint and the same decision.",
	},
}

// exemptQueryParam reports whether the read is justified, and marks it used.
func exemptQueryParam(used []bool, importPath, name string) bool {
	for i, exemption := range queryParamExemptions {
		if exemption.importPath == importPath && exemption.name == name {
			used[i] = true

			return true
		}
	}

	return false
}

// TestEveryQueryParameterAHandlerReadsIsDescribed verifies that a package which
// publishes an OpenAPI description describes every query parameter it reads.
//
// The failure it prevents was reproduced before it was written: a handler
// reading a name that appears in no description is a working switch nothing
// announces, and on 2026-09-06 the whole repository was green over one.
func TestEveryQueryParameterAHandlerReadsIsDescribed(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	scans := scanQueryParams(t, tree)

	require.NotEmpty(t, scans,
		"no package that publishes an openapi.Parameter was found; the SCOPE rule must "+
			"have gone blind, and a scope that selects nothing reports nothing")

	used := make([]bool, len(queryParamExemptions))

	reads := 0
	for importPath, scan := range scans {
		reads += len(scan.read)

		names := make([]string, 0, len(scan.read))
		for name := range scan.read {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			if scan.described[name] || exemptQueryParam(used, importPath, name) {
				continue
			}
			where := scan.read[name]
			t.Errorf("%s:%d: %s reads the query parameter %q and describes it nowhere.\n"+
				"A parameter that is read but not described is a working switch the "+
				"document does not mention: the generated client cannot offer it, a "+
				"reviewer cannot see it, and nobody can audit what they cannot read. "+
				"Describe it, or stop reading it.",
				where.file, where.line, importPath, name)
		}
	}

	for i, exemption := range queryParamExemptions {
		assert.True(t, used[i],
			"exemption STALE: %s no longer reads %q without describing it (either the "+
				"read was removed or the parameter is described now).\nJustification: %s\n"+
				"An exemption that stays behind silently forgives the next undescribed "+
				"parameter.",
			exemption.importPath, exemption.name, exemption.reason)
	}

	require.Positive(t, reads,
		"NOT ONE query parameter read was found in any described package; the reader "+
			"detection must have gone BLIND.\nIt recognizes a call shaped "+
			"\"<something>.URL.Query().Get(x)\" and, when x is one of the enclosing "+
			"function's own parameters, treats that function as a reader whose call "+
			"sites carry the literal.")
}

// TestEveryDescribedQueryParameterIsRead is the other half, and it derives what
// was being kept by hand.
//
// Describing a parameter nothing reads promises the client a feature that does
// not work: the generator puts an argument on the method, the caller fills it
// in, the server ignores it. Several modules guard that today with a test that
// compares the generated document against a list written INSIDE the test, so
// the handler is never consulted and the two hand-maintained sides can drift
// together. This one derives both sides from source, so the comparison says
// something about the code rather than about the list.
//
// It does NOT retire those tests and must not be read as doing so: each of them
// also says things this one cannot, per ENDPOINT rather than per package, and
// at least one of them carries a security statement — that the sales channel is
// taken from the request identity and must never be announced as a query
// parameter.
//
// The comparison is per PACKAGE and not per endpoint, and the limit is worth
// stating: a parameter described on one route and read on another passes here.
// Catching that needs the route table, and the route table is a third source
// this audit deliberately does not depend on yet.
func TestEveryDescribedQueryParameterIsRead(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	scans := scanQueryParams(t, tree)

	require.NotEmpty(t, scans, "the scope rule must have gone blind")

	described := 0
	for importPath, scan := range scans {
		described += len(scan.described)

		names := make([]string, 0, len(scan.described))
		for name := range scan.described {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			if _, read := scan.read[name]; read {
				continue
			}
			t.Errorf("%s describes the query parameter %q and reads it nowhere.\n"+
				"A described parameter nothing reads is a promise the server does not "+
				"keep: the generated client offers it, the caller sends it and it is "+
				"silently ignored. Read it, or stop describing it.",
				importPath, name)
		}
	}

	require.Positive(t, described,
		"NOT ONE described query parameter was found; the description side must have "+
			"gone BLIND.\nIt recognizes a call to a package-local function that returns "+
			"an openapi.Parameter carrying In: \"query\", and an openapi.Parameter "+
			"literal spelled out with the same field.")
}

// TestTheQueryParameterScannerIsNotBlind pins down what the scanner sees.
//
// The scanner is the whole audit: everything after it is a set difference. A
// pattern that quietly stopped matching would leave a test that walks the entire
// repository, finds nothing and reports success.
func TestTheQueryParameterScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	scans := scanQueryParams(t, tree)

	product := ""
	for importPath := range scans {
		if strings.HasSuffix(importPath, "/modules/product/api") {
			product = importPath
		}
	}
	require.NotEmpty(t, product, "the product api package must be in scope; it describes parameters")

	scan := scans[product]

	// A literal read directly: sortParam reads "sort" with no indirection.
	assert.Contains(t, scan.read, "sort", "a literal handed straight to Query().Get must be seen")
	// A literal read THROUGH a helper: stringParam(r, "tag_id") forwards its
	// own parameter, so the name lives at the CALL SITE and not in the reader.
	assert.Contains(t, scan.read, "tag_id", "a literal handed to a reader function must be seen")
	// A literal read through the paging helper, which is two calls deep.
	assert.Contains(t, scan.read, "limit", "a literal inside a helper that itself calls a reader must be seen")

	// A PATH parameter must NOT be seen: pathParam reads chi.URLParam, not the
	// query, and counting it would make every "{id}" route a false positive.
	assert.NotContains(t, scan.read, "id", "a path parameter must not be counted as a query read")

	// The described side finds a helper call...
	assert.Contains(t, scan.described, "with_count", "a queryParameter(...) call must be seen")
	// ...and REFUSES a path parameter, even though it is written as an
	// openapi.Parameter literal with a Name. Counting it would excuse a query
	// read of "id" and would make every "{id}" route look unread.
	assert.NotContains(t, scan.described, "id", "a path parameter must not enter the query set")

	// The composite-literal spelling IS seen when the parameter sits in the
	// query; fulfillment writes one that way because it is repeatable.
	fulfillment := ""
	for importPath := range scans {
		if strings.HasSuffix(importPath, "/modules/fulfillment/api") {
			fulfillment = importPath
		}
	}
	require.NotEmpty(t, fulfillment, "the fulfillment api package must be in scope")

	// The two spellings that are NOT the common one, each pinned where it is
	// actually written. Both were missing when this audit was first run and
	// both made it report a parameter that IS read as unread.
	//
	//   query := r.URL.Query() … query.Get("currency_code")   — a variable
	//   values, ok := r.URL.Query()[name]                     — an index
	assert.Contains(t, scans[fulfillment].read, "currency_code",
		"a read through a VARIABLE holding r.URL.Query() must be seen")

	review := ""
	for importPath := range scans {
		if strings.HasSuffix(importPath, "/modules/review/api") {
			review = importPath
		}
	}
	require.NotEmpty(t, review, "the review api package must be in scope")
	assert.Contains(t, scans[review].read, "status",
		"a read through the INDEX form r.URL.Query()[name] must be seen")

	assert.Contains(t, scans[fulfillment].described, "shipping_profile_id",
		"an openapi.Parameter{In: \"query\"} literal must be seen; the ELIDED form inside "+
			"a []openapi.Parameter slice is the one this scanner missed at first")

	// The scope rule excludes a package that describes nothing.
	for importPath := range scans {
		assert.NotContains(t, importPath, "/internal/adminui",
			"the panel publishes no document; describing is undefined for it")
	}
}
