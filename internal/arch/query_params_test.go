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
	// described are the parameter names the package puts in the document.
	described map[string]bool
	// parameterBuilders are the package's functions returning openapi.Parameter.
	parameterBuilders map[string]bool
}

// readSite remembers where a read was seen, so the failure can point at it.
type readSite struct {
	file string
	line int
}

// queryGetArgument reports the argument of an "…URL.Query().Get(x)" call, or
// nil when the expression is not one.
//
// The shape is matched STRUCTURALLY rather than by the receiver's name: the
// request is called "r" everywhere today, and a rule that said so would go
// blind the day somebody named it "req".
func queryGetArgument(call *ast.CallExpr) ast.Expr {
	get, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || get.Sel.Name != "Get" || len(call.Args) != 1 {
		return nil
	}

	queryCall, ok := get.X.(*ast.CallExpr)
	if !ok {
		return nil
	}

	query, ok := queryCall.Fun.(*ast.SelectorExpr)
	if !ok || query.Sel.Name != "Query" {
		return nil
	}

	url, ok := query.X.(*ast.SelectorExpr)
	if !ok || url.Sel.Name != "URL" {
		return nil
	}

	return call.Args[0]
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

// nameField adds the literal's Name field to the set.
func nameField(lit *ast.CompositeLit, into map[string]bool) {
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok || key.Name != "Name" {
			continue
		}
		if name := stringLiteral(pair.Value); name != "" {
			into[name] = true
		}
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
				if returnsOpenAPIParameter(file, fn) {
					scan.parameterBuilders[fn.Name.Name] = true
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					argument := queryGetArgument(call)
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
			ast.Inspect(file.tree, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					parameterNamesIn(file, value, scan.described)
				case *ast.CallExpr:
					if argument := queryGetArgument(value); argument != nil {
						if name := stringLiteral(argument); name != "" {
							scan.read[name] = position(tree, file, value)
						}

						return true
					}
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

	// The described side finds both spellings: the helper call and the
	// composite literal.
	assert.Contains(t, scan.described, "with_count", "a queryParameter(...) call must be seen")
	assert.Contains(t, scan.described, "id", "an openapi.Parameter{Name: ...} literal must be seen")

	// The scope rule excludes a package that describes nothing.
	for importPath := range scans {
		assert.NotContains(t, importPath, "/internal/adminui",
			"the panel publishes no document; describing is undefined for it")
	}
}
