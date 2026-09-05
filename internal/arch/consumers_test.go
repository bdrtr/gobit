package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// This file enforces ONE invariant on three surfaces: EVERY PRODUCED CAPABILITY
// HAS A CONSUMER.
//
// One class of the repository's most expensive bugs is not the violation of a
// rule but its ABSENCE: a capability is written, wired up, documented — and
// nobody uses it. The whole of phase 8/9 had been written without being
// mounted; b2b had not been registered in the composition root; RequireScope
// was dead code; "order.placed" was published without a subscriber for a long
// time. None of these give an error: the code compiles, the tests pass, the
// feature behaves as if it were not there.
//
// The three surfaces are audited separately and are SEPARATE tests. Had they
// been gathered into a single test, nothing after the first t.Fatal would run
// and a mutation of one would mask the finding of another.
//
// The shared method: the source is WALKED with go/parser, no list is KEPT.
// Because the names live in constants and the modules cannot import one another
// (ADR 0001), the VALUE of the constant is resolved across packages; when a
// consumer repeats a name by hand (order → "b2b.interop", searchpg →
// "product.interop") the tie is only visible at the value level.

// maxResolutionDepth is the largest number of steps to follow while resolving a
// constant value.
//
// Resolution is recursive (constant → constant → parameter → caller) and a
// cyclic declaration or two functions calling each other could produce an
// infinite descent. The deepest chain today is four steps; eight leaves room
// for chains that will grow without the rule changing.
const maxResolutionDepth = 8

// constDefinition holds the value expression of a constant and the file it is
// defined in.
//
// The file is kept as well because the qualified names inside the value (e.g.
// query.ProviderSuffix) can only be resolved with the import table of the
// DEFINITION's file.
type constDefinition struct {
	expr ast.Expr
	file *sourceFile
}

// sourceFile is a single parsed production file.
type sourceFile struct {
	// path is the path relative to the repository root; this is what appears in
	// error messages.
	path string
	// importPath is the full import path of the file's package.
	importPath string
	tree       *ast.File
	// imports maps the LOCAL package name in the file to the import path.
	imports map[string]string
}

// callSite holds a function call and the context it sits inside.
type callSite struct {
	file *sourceFile
	// fn is the function containing the call; it is nil for package level
	// declarations.
	fn   *ast.FuncDecl
	call *ast.CallExpr
}

// literalSite holds a composite literal and its context.
type literalSite struct {
	file  *sourceFile
	fn    *ast.FuncDecl
	value *ast.CompositeLit
}

// sourceTree is the scanned state of the repository's production source.
//
// It is the single input of all three tests: both the declarations and the
// consumptions are read from here, none of them comes from a hand-written list.
type sourceTree struct {
	fset  *token.FileSet
	files []*sourceFile
	// constants goes from the import path to the constant name, and from there
	// to the definition.
	constants map[string]map[string]constDefinition
	// packageName goes from the import path to the package's DECLARED name;
	// this is the local name of an import without an alias and it can differ
	// from the directory name.
	packageName map[string]string
	// calls goes from the UNQUALIFIED name of the called function to the call
	// sites.
	calls map[string][]callSite
	// literals are all the composite literals that were scanned.
	literals []literalSite
}

// scanProductionSource parses the production source and builds the indexes.
func scanProductionSource(t *testing.T) *sourceTree {
	t.Helper()

	tree := &sourceTree{
		fset:        token.NewFileSet(),
		constants:   map[string]map[string]constDefinition{},
		packageName: map[string]string{},
		calls:       map[string][]callSite{},
	}

	for _, root := range productionTrees {
		absolute := filepath.Join(repoRoot, root)
		if _, err := os.Stat(absolute); err != nil {
			t.Fatalf("the %q root was not found: %v", root, err)
		}
		for _, path := range treeFiles(t, root) {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(tree.fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("%s could not be parsed: %v", path, err)
			}
			relative, err := filepath.Rel(repoRoot, path)
			if err != nil {
				t.Fatalf("%s could not be turned into a relative path: %v", path, err)
			}
			relative = filepath.ToSlash(relative)
			file := &sourceFile{
				path:       relative,
				importPath: modulePath + "/" + filepath.ToSlash(filepath.Dir(relative)),
				tree:       parsed,
				imports:    map[string]string{},
			}
			tree.files = append(tree.files, file)
			tree.packageName[file.importPath] = parsed.Name.Name
		}
	}

	for _, file := range tree.files {
		tree.collectImports(file)
		tree.collectConstants(file)
	}
	for _, file := range tree.files {
		tree.scanDeclarations(file)
	}

	return tree
}

// collectImports builds the file's local package name → import path table.
//
// The local name of an import that has no alias is the name the target package
// DECLARES; the last element of the directory name is only a guess and there
// are places in this repository where it does not hold. That is why the
// genuinely parsed package name is consulted first and the guess is used only
// for packages outside the repository (stdlib, third party) — their constants
// do not get resolved anyway.
func (a *sourceTree) collectImports(file *sourceFile) {
	for _, imp := range file.tree.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		local := ""
		switch {
		case imp.Name != nil:
			local = imp.Name.Name
		case a.packageName[path] != "":
			local = a.packageName[path]
		default:
			parts := strings.Split(path, "/")
			local = parts[len(parts)-1]
		}
		if local == "" || local == "_" || local == "." {
			continue
		}
		file.imports[local] = path
	}
}

// collectConstants indexes the package level constants in the file.
//
// Only constants that have a VALUE are taken: the ones produced with iota and
// the type declarations have no counterpart in these tests, and the container
// names as well as the event and link names are all strings.
func (a *sourceTree) collectConstants(file *sourceFile) {
	for _, decl := range file.tree.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != len(value.Names) {
				continue
			}
			for i, name := range value.Names {
				if a.constants[file.importPath] == nil {
					a.constants[file.importPath] = map[string]constDefinition{}
				}
				a.constants[file.importPath][name.Name] = constDefinition{expr: value.Values[i], file: file}
			}
		}
	}
}

// scanDeclarations indexes the calls and the composite literals in the file.
func (a *sourceTree) scanDeclarations(file *sourceFile) {
	for _, decl := range file.tree.Decls {
		fn, _ := decl.(*ast.FuncDecl)
		ast.Inspect(decl, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				if name := callName(node.Fun); name != "" {
					a.calls[name] = append(a.calls[name], callSite{file: file, fn: fn, call: node})
				}
			case *ast.CompositeLit:
				a.literals = append(a.literals, literalSite{file: file, fn: fn, value: node})
			}
			return true
		})
	}
}

// callName returns the UNQUALIFIED name of the called function.
//
// Generic calls (container.Resolve[T](...)) produce a wrapping IndexExpr; if it
// were not peeled off the name "Resolve" would never be seen.
func callName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.IndexExpr:
		return callName(x.X)
	case *ast.IndexListExpr:
		return callName(x.X)
	case *ast.ParenExpr:
		return callName(x.X)
	}
	return ""
}

// location writes a position as "file:line" relative to the repository root.
func (a *sourceTree) location(file *sourceFile, pos token.Pos) string {
	return fmt.Sprintf("%s:%d", file.path, a.fset.Position(pos).Line)
}

// stringValues resolves the STRING values an expression can take.
//
// It can return more than one value because a name may not be bound to a single
// value: if the event name is carried as a function PARAMETER (product's
// publishProductEvent is like this) the value comes from the callers and three
// different event names pass through the same publish line. That is why the
// resolution goes not to a single constant but to the SET OF POSSIBLE VALUES.
//
// The supported forms are the ones GENUINELY used in this repository: a string
// literal, a concatenation (ModuleName + ".interop"), a constant in the same
// package, a qualified constant in another package, a function parameter and a
// range variable ranging over a slice literal. An expression that cannot be
// resolved is not skipped silently; the calling side SEES the empty set and
// gives an error according to its own test.
func (a *sourceTree) stringValues(file *sourceFile, fn *ast.FuncDecl, expr ast.Expr, depth int) []string {
	if expr == nil || depth > maxResolutionDepth {
		return nil
	}

	switch x := expr.(type) {
	case *ast.ParenExpr:
		return a.stringValues(file, fn, x.X, depth+1)

	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return nil
		}
		value, err := strconv.Unquote(x.Value)
		if err != nil {
			return nil
		}
		return []string{value}

	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return nil
		}
		var out []string
		for _, left := range a.stringValues(file, fn, x.X, depth+1) {
			for _, right := range a.stringValues(file, fn, x.Y, depth+1) {
				out = append(out, left+right)
			}
		}
		return out

	case *ast.Ident:
		return a.identValues(file, fn, x, depth)

	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return nil
		}
		path, ok := file.imports[pkg.Name]
		if !ok {
			return nil
		}
		definition, ok := a.constants[path][x.Sel.Name]
		if !ok {
			return nil
		}
		return a.stringValues(definition.file, nil, definition.expr, depth+1)
	}

	return nil
}

// identValues resolves the possible string values of an unqualified name.
//
// The order matters: the local context (parameter, range variable) is tried
// BEFORE the package level constant, because a shadowing local name wins.
func (a *sourceTree) identValues(file *sourceFile, fn *ast.FuncDecl, id *ast.Ident, depth int) []string {
	if fn != nil {
		if index, ok := parameterIndex(fn, id.Name); ok {
			return a.callArguments(fn, index, depth)
		}
		if values := a.rangeValues(file, fn, id.Name, depth); len(values) > 0 {
			return values
		}
	}
	if definition, ok := a.constants[file.importPath][id.Name]; ok {
		return a.stringValues(definition.file, nil, definition.expr, depth+1)
	}
	return nil
}

// parameterIndex returns which STRING parameter of the function the name is.
//
// The receiver does not count: it is not in the argument list of the method
// call either.
//
// Only string parameters are followed. This is NOT a speed measure but a
// correctness measure: descending into the caller chain of a non-string
// parameter (container, logger, ctx) never produces a name, but it grows the
// resolution exponentially by walking every caller and makes the test take
// minutes.
func parameterIndex(fn *ast.FuncDecl, name string) (int, bool) {
	if fn.Type == nil || fn.Type.Params == nil {
		return 0, false
	}
	index := 0
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			index++
			continue
		}
		typ, isString := field.Type.(*ast.Ident)
		isString = isString && typ.Name == "string"
		for _, fieldName := range field.Names {
			if fieldName.Name == name {
				return index, isString
			}
			index++
		}
	}
	return 0, false
}

// callArguments resolves the given argument at the function's callers.
//
// The match is made by function NAME; without type information the receiver
// type cannot be known. A wrong match does not shift the test to the WRONG
// SIDE: the argument of another function with the same name resolves to a
// string that is not in the set of names being looked for and enters no
// assertion.
func (a *sourceTree) callArguments(fn *ast.FuncDecl, index, depth int) []string {
	var out []string
	for _, site := range a.calls[fn.Name.Name] {
		if site.fn == fn || len(site.call.Args) <= index {
			continue
		}
		out = append(out, a.stringValues(site.file, site.fn, site.call.Args[index], depth+1)...)
	}
	return out
}

// rangeValues resolves the values of a variable ranging over a slice literal.
//
// In this repository the cleanup and compensation paths may, instead of writing
// the names out one by one, range like
// "for _, name := range []string{LinkVariantPriceSet, LinkVariantInventory}".
// Had this form not been resolved those calls would be UNRECOGNIZED and a link
// would count as "being read" even though it is only read in order to be
// deleted.
func (a *sourceTree) rangeValues(file *sourceFile, fn *ast.FuncDecl, name string, depth int) []string {
	var out []string
	ast.Inspect(fn, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		id, ok := loop.Value.(*ast.Ident)
		if !ok || id.Name != name {
			return true
		}
		slice, ok := loop.X.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, element := range slice.Elts {
			out = append(out, a.stringValues(file, fn, element, depth+1)...)
		}
		return true
	})
	return out
}

// qualifiedType tells whether a type expression is the given type in the given
// package.
//
// The comparison is made against the IMPORT PATH, not the local alias: the same
// type can be written as "link.LinkDefinition" in one file and
// "corelink.LinkDefinition" in another.
func qualifiedType(file *sourceFile, expr ast.Expr, importPath, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return file.imports[pkg.Name] == importPath
}

// fieldExpr returns the expression of the named field in the composite literal.
func fieldExpr(value *ast.CompositeLit, field string) ast.Expr {
	for _, element := range value.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != field {
			continue
		}
		return kv.Value
	}
	return nil
}

// ---------------------------------------------------------------------------
// SURFACE 1 — interop registrations
// ---------------------------------------------------------------------------

// The FAMILIES of the container registration names.
//
// Not every name a module leaves in the container carries the same contract,
// and this test audits only the ones ending in [interopFamily]. The reasons one
// by one:
//
//   - [interopFamily]: the PRIMITIVE surface between the modules (ADR
//     0001/0006). Its only purpose is for ANOTHER module or workflow to resolve
//     it; it uses only primitive and stdlib types, precisely because it cannot
//     be imported. An interop without a consumer is a language nobody speaks:
//     the cost of the registration is paid, nothing is received in return.
//   - [serviceFamily]: the module's OWN service. It is the extension surface
//     opened to the plugins and to embedded use; the core does not resolve it,
//     nor is it expected to. Having NO out-of-the-box consumer is legitimate
//     and that is why it is out of scope — otherwise the test would force every
//     module to write an artificial consumer that uses its own service.
//   - [queryFamily]: the provider of the Query layer. Its consumer is in the
//     core and it computes the name AT RUNTIME (core/query appends ".query" to
//     the entity name at the far end of the link and looks it up in the
//     container). It cannot be followed statically; the tie of this family is
//     pinned separately by [TestTheSalesChannelEntityNameAgrees].
//   - [providerFamily]: the registration point of the plugin providers. Its tie
//     is already asserted by [TestTheProviderRegistryNamesAgree].
//   - [coreFamily]: the core infrastructure (database, link, query, saga, event
//     bus). It is not module production.
//   - [reportingFamily]: a SINGLE slot the core owns — the error reporter (ADR
//     0014). It differs from the others in two ways: it is NOT module
//     production (a plugin fills it) and it is SINGULAR (the registration point
//     is not a registry but the name itself; the container already rejects a
//     duplicate name). Its consumer is in the core but it is CONDITIONAL — if
//     the installation has not defined a collector the name is never registered
//     and cmd/server asks with Has first — which is why it is outside the scope
//     of the "does it have a consumer" audit.
//   - [adminFamily]: the module's ADMIN WRITE surface (ADR 0013). Its only
//     consumer is the admin panel and that consumer is CONDITIONAL: the panel
//     resolves this name only if it is registered, because it has to be able to
//     open on an installation where the module is not installed either. That is
//     why it is OUTSIDE THE SCOPE of the "does it have a consumer" audit — had
//     it been taken into scope it would have turned a conditional resolution
//     into a mandatory tie. The family's real constraint is a different one:
//     WHO may mention this name is limited by
//     [TestAdminSurfaceHasOneAudience], and whether the panel's name holds
//     against the module's constant by [TestThePanelCatalogNamesAgree].
const (
	interopFamily   = ".interop"
	serviceFamily   = ".service"
	queryFamily     = ".query"
	providerFamily  = ".providers"
	adminFamily     = ".admin"
	reportingFamily = "error.reporter"
	coreFamily      = "core."
)

// resolveCallFragment is the name fragment of the functions that resolve from
// the container.
//
// Resolution is not always done directly with container.Resolve: the workflows
// use the resolve/resolveOptional wrappers in order to wrap the error while
// preserving its class, and the plugin host uses resolveSink. Matching on the
// name fragment catches all of these wrappers with a single rule and does not
// require the test to be updated when a new wrapper is added.
const resolveCallFragment = "resolve"

// TestTheInteropSurfacesHaveAConsumer verifies that every cross-module surface
// left in the container is resolved FROM SOMEWHERE ELSE.
//
// An interop that is registered and never resolved is a dead contract and it
// does not say that it is dead: the module opens, logs its name, sets up its
// surface. Phase 8/9 not being mounted and b2b not being registered in the
// composition root went unnoticed in exactly this way.
//
// Two distinctions are deliberate:
//
//   - A surface resolved IN TESTS DOES NOT COUNT as consumed. The tests are
//     outside this scan ([scanProductionSource] skips the _test.go files). If
//     the only caller of a surface is its own integration test, then that
//     surface adds nothing to the product; the test only proves that the
//     surface works, not that it is needed.
//   - A surface resolved from INSIDE ITS OWN module DOES NOT COUNT as consumed.
//     The reason interop exists is cross-module access; for access from its own
//     package the concrete type is already there.
func TestTheInteropSurfacesHaveAConsumer(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	provided := tree.providedNames(t)

	consuming := map[string][]string{}
	for name, sites := range tree.calls {
		if !strings.Contains(strings.ToLower(name), resolveCallFragment) {
			continue
		}
		for _, site := range sites {
			for _, arg := range site.call.Args {
				for _, value := range tree.stringValues(site.file, site.fn, arg, 0) {
					consuming[value] = append(consuming[value], site.file.path)
				}
			}
		}
	}

	count := 0
	for _, name := range slices.Sorted(maps.Keys(provided)) {
		if !strings.HasSuffix(name, interopFamily) {
			continue
		}
		count++

		declaringFile := provided[name]
		ownerPrefix := owningModulePrefix(declaringFile)
		var fromOutside []string
		for _, path := range consuming[name] {
			if ownerPrefix != "" && strings.HasPrefix(path, ownerPrefix) {
				continue
			}
			fromOutside = append(fromOutside, path)
		}

		if len(fromOutside) == 0 {
			t.Errorf("%s: the %q surface is registered in the container but NO PRODUCTION FILE resolves it.\n"+
				"The only purpose of the primitive cross-module surface (ADR 0001/0006) is for another module or "+
				"workflow to resolve it; an interop without a consumer is a dead contract.\n"+
				"Either add the wiring that consumes the surface or remove the registration. "+
				"(Being resolved only in tests DOES NOT COUNT AS CONSUMPTION.)",
				declaringFile.path, name)
		}
	}

	if count == 0 {
		t.Fatal("no interop registration was found; the scan must be missing the surface " +
			"(if the registration form has changed this test must change too)")
	}
}

// providedNames returns every name left in the container with Provide.
//
// If a name cannot be resolved an error is given: a registration that cannot be
// resolved is a registration that is not audited, and skipping it silently
// would narrow the scope of this test invisibly.
//
// The name FAMILY is audited here as well. A new registration with an
// unrecognized suffix fails the test: when a new family appears somebody has to
// answer the question "who is its consumer?" and write the decision into the
// constant block above. Staying out of scope silently is the very bug this file
// tries to prevent.
func (a *sourceTree) providedNames(t *testing.T) map[string]*sourceFile {
	t.Helper()

	provided := map[string]*sourceFile{}
	for _, site := range a.calls["Provide"] {
		if len(site.call.Args) == 0 {
			continue
		}
		names := a.stringValues(site.file, site.fn, site.call.Args[0], 0)
		if len(names) == 0 {
			t.Errorf("%s: the REGISTRATION NAME of the Provide call could not be resolved statically.\n"+
				"These tests collect the names by walking the source; a name that cannot be resolved cannot be audited. "+
				"Bind the name to a string constant.", a.location(site.file, site.call.Pos()))
			continue
		}
		for _, name := range names {
			if !knownFamily(name) {
				t.Errorf("%s: the %q registration belongs to an UNRECOGNIZED name family.\n"+
					"For every family the question \"who is its consumer and where is it looked for\" has been answered "+
					"(see the interopFamily/serviceFamily/... constants). Add the new family there with "+
					"its reason; staying out of scope must be a DELIBERATE decision.",
					a.location(site.file, site.call.Pos()), name)
				continue
			}
			if _, exists := provided[name]; !exists {
				provided[name] = site.file
			}
		}
	}
	return provided
}

// knownFamily tells whether the name belongs to one of the recognized
// registration families.
func knownFamily(name string) bool {
	if strings.HasPrefix(name, coreFamily) {
		return true
	}
	if name == reportingFamily {
		return true
	}
	for _, suffix := range []string{interopFamily, serviceFamily, queryFamily, providerFamily, adminFamily} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// owningModulePrefix returns the repository path prefix of the module the file
// belongs to.
//
// For files outside a module (cmd, workflows, plugins, core) it returns empty:
// by definition every resolution of theirs is "from outside".
func owningModulePrefix(file *sourceFile) string {
	prefix := modulesDir + "/"
	if !strings.HasPrefix(file.path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(file.path, prefix)
	module, _, found := strings.Cut(rest, "/")
	if !found {
		return ""
	}
	return prefix + module + "/"
}

// ---------------------------------------------------------------------------
// SURFACE 2 — event topics
// ---------------------------------------------------------------------------

// subscriberlessPublications are the events whose subscriber is NOT LOOKED FOR
// because they are open to the outside.
//
// A topic having no subscriber inside the repository can be DELIBERATE: gobit
// is a framework and the application doing the installation may bind its own
// handler. That decision is written here, with the topic name and its REASON;
// as long as it is not written, a topic without a subscriber is a bug.
//
// The map is EMPTY today and this is not a gap but a finding: all four of the
// four published topics have a subscriber inside the repository (plugins/searchpg
// listens to the three catalog events, the notification module to
// "order.placed"). The price of an exemption is high — an exempt topic means
// going back to the state where "order.placed" did nothing for months — which
// is why anyone adding a line here is expected to answer the question "who
// listens, in which installation" in the reason.
var subscriberlessPublications = map[string]string{}

// TestTheEventTopicsHaveASubscriber verifies that every published event name
// has a subscriber.
//
// "order.placed" was published without a subscriber for a long time: when an
// order was placed the event was produced, written to the bus and nothing
// happened. Nobody saw an error; the existence of the event gave the impression
// that some work was being done. Until the notification module was written the
// feature DID NOT EXIST.
//
// The publish side is WALKED: the Name field of the eventbus.Event value is
// resolved, and if the name comes from a function parameter the callers are
// descended into (product's three catalog events pass through a single publish
// line). A publish that cannot be resolved gives an error; skipping it silently
// would leave a topic unaudited.
//
// A name that cannot be resolved on the subscription side, however, is skipped
// SILENTLY and this asymmetry is deliberate: the core's plugin host is an
// intermediate layer that forwards the subscription and carries the name as a
// parameter. A missed subscription can only produce a FALSE ALARM (not seeing
// an existing subscriber), it cannot produce a silent pass.
func TestTheEventTopicsHaveASubscriber(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	const eventbusPath = modulePath + "/core/eventbus"

	published := map[string]string{}
	for _, site := range tree.calls["Publish"] {
		if len(site.call.Args) < 2 {
			continue
		}
		value := tree.eventLiteral(site, site.call.Args[1], eventbusPath)
		if value == nil {
			continue
		}
		nameExpr := fieldExpr(value, "Name")
		names := tree.stringValues(site.file, site.fn, nameExpr, 0)
		if len(names) == 0 {
			t.Errorf("%s: the NAME of the published event could not be resolved statically.\n"+
				"The subscriber of a topic whose name cannot be resolved cannot be looked for either; bind the event "+
				"name to a string constant (see service.EventOrderPlaced).",
				tree.location(site.file, site.call.Pos()))
			continue
		}
		for _, name := range names {
			published[name] = tree.location(site.file, site.call.Pos())
		}
	}

	subscribed := map[string][]string{}
	for _, site := range tree.calls["Subscribe"] {
		if len(site.call.Args) == 0 {
			continue
		}
		for _, name := range tree.stringValues(site.file, site.fn, site.call.Args[0], 0) {
			subscribed[name] = append(subscribed[name], site.file.path)
		}
	}

	if len(published) == 0 {
		t.Fatal("no event publish was found; the scan must be missing the publish surface " +
			"(if the use of eventbus.Event has changed this test must change too)")
	}

	for _, name := range slices.Sorted(maps.Keys(published)) {
		if reason, exempt := subscriberlessPublications[name]; exempt {
			if len(subscribed[name]) > 0 {
				t.Errorf("the %q topic was counted as subscriberless but it HAS a subscriber (%s).\n"+
					"The reason for the exemption no longer holds (%q); delete it from subscriberlessPublications — "+
					"a dead exemption covers up the next real violation.",
					name, strings.Join(subscribed[name], ", "), reason)
			}
			continue
		}
		if len(subscribed[name]) == 0 {
			t.Errorf("%s: the %q event is published but NO PRODUCTION FILE subscribes to it.\n"+
				"A topic without a subscriber is work that is believed to be done but is not: the publish returns "+
				"successfully, nobody sees an error, the feature is absent (\"order.placed\" was like this for months).\n"+
				"Either add the subscriber or, if you are keeping the publish DELIBERATELY for outside observers, "+
				"write it with its reason into the subscriberlessPublications map.", published[name], name)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(subscriberlessPublications)) {
		if _, exists := published[name]; !exists {
			t.Errorf("the %q topic is exempt in subscriberlessPublications but is NO LONGER PUBLISHED.\n"+
				"Exemptions go unmaintained too; delete the entry.", name)
		}
	}
}

// eventLiteral finds the eventbus.Event composite literal given to the Publish
// call.
//
// Two forms are supported because both exist in the repository: the value can
// be written inside the call (product) or first built into a local variable and
// then passed (order). A Publish call whose Event is not this package's — the
// method of another library with the same name — returns nil and does not enter
// the audit.
func (a *sourceTree) eventLiteral(site callSite, arg ast.Expr, eventbusPath string) *ast.CompositeLit {
	if value, ok := arg.(*ast.CompositeLit); ok {
		if qualifiedType(site.file, value.Type, eventbusPath, "Event") {
			return value
		}
		return nil
	}

	id, ok := arg.(*ast.Ident)
	if !ok || site.fn == nil {
		return nil
	}

	var found *ast.CompositeLit
	ast.Inspect(site.fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		target, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || target.Name != id.Name {
			return true
		}
		value, ok := assign.Rhs[0].(*ast.CompositeLit)
		if !ok || !qualifiedType(site.file, value.Type, eventbusPath, "Event") {
			return true
		}
		found = value
		return false
	})
	return found
}

// ---------------------------------------------------------------------------
// SURFACE 3 — link definitions
// ---------------------------------------------------------------------------

// linkReadMethods are the methods of the link service that TRAVERSE a link.
//
// The distinction is the core of this test: creating a link (Create) and
// removing it (Delete) are the WRITE path, they show the relation to nobody.
// The only observable consequence of a link existing is somebody READING it.
var linkReadMethods = []string{"List", "ListMany", "ListManyByTo"}

// The labels naming the WRITE side of the link usages and the Query expansion.
//
// [expansionMethod] is not a method name: in the Query layer a link is
// traversed not with a call but with a query.Expansion value, and that too is a
// read path. Its standing in the same list makes the question "what is a read"
// be answered in a single place.
const (
	linkDeleteMethod = "Delete"
	linkWriteMethod  = "Create"
	expansionMethod  = "Expansion"
)

// linkUsage is a single usage site of a link name.
type linkUsage struct {
	file   *sourceFile
	fn     *ast.FuncDecl
	method string
	pos    token.Pos
}

// TestTheLinkDefinitionsAreTraversed verifies that every declared link name is
// used on a READ path.
//
// This is the test of the sales channel failure. The product ↔ sales channel
// link was being written, assigned from the admin API, sitting in the database
// — and no read was traversing it. The storefront was showing products that
// were not assigned to a channel as well; the feature was counted as "done",
// the behavior never changed. Writing the link is not proof that the link
// WORKS.
//
// # A cleanup read DOES NOT COUNT as consumption
//
// The delete compensation first reads its own links and then deletes them
// (today's example: clearVariantLink in the product service). This read is
// there to destroy the link and shows the relation to nobody — it is part of
// the write path. Had it counted the rule would empty out: every module that
// deletes its link would count as "reading" it and the test would never find
// anything. That is why a read is accepted as cleanup if the deletion of the
// same link exists IN THE SAME FUNCTION.
//
// # There is NO exemption mechanism
//
// Unlike with the event topics, no deliberate gap is accepted here. If a link
// really is declared for the outside, the right answer is not an exemption but
// adding a read path that traverses that link (a Query expansion or the
// module's own API): a link that is not read writes the data, pays the cost and
// produces no behavior in return.
func TestTheLinkDefinitionsAreTraversed(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)
	const linkPath = modulePath + "/core/link"
	const queryPath = modulePath + "/core/query"

	declared := map[string]string{}
	for _, literal := range tree.literals {
		for _, definition := range qualifiedLiterals(literal, linkPath, "LinkDefinition") {
			// A literal with no fields is not a DECLARATION but a zero value:
			// the core's query engine writes "link.LinkDefinition{}" while
			// returning an error and that one has no name to resolve.
			if len(definition.Elts) == 0 {
				continue
			}
			nameExpr := fieldExpr(definition, "Name")
			names := tree.stringValues(literal.file, literal.fn, nameExpr, 0)
			if len(names) == 0 {
				t.Errorf("%s: the NAME of the link definition could not be resolved statically.\n"+
					"Whether a link whose name cannot be resolved is traversed cannot be audited either; "+
					"bind the name to a string constant (see service.LinkProductSalesChannel).",
					tree.location(literal.file, definition.Pos()))
				continue
			}
			for _, name := range names {
				declared[name] = tree.location(literal.file, definition.Pos())
			}
		}
	}

	if len(declared) == 0 {
		t.Fatal("no link definition was found; the scan must be missing the declaration surface " +
			"(if the use of link.LinkDefinition has changed this test must change too)")
	}

	usages := tree.linkUsages(declared, queryPath)

	for _, name := range slices.Sorted(maps.Keys(declared)) {
		deleters := map[*ast.FuncDecl]bool{}
		for _, usage := range usages[name] {
			if usage.method == linkDeleteMethod && usage.fn != nil {
				deleters[usage.fn] = true
			}
		}

		var reads, writes []string
		for _, usage := range usages[name] {
			if usage.method == linkWriteMethod {
				writes = append(writes, tree.location(usage.file, usage.pos))
				continue
			}
			if !slices.Contains(linkReadMethods, usage.method) && usage.method != expansionMethod {
				continue
			}
			if usage.fn != nil && deleters[usage.fn] {
				continue // cleanup: it reads in order to delete the link
			}
			reads = append(reads, tree.location(usage.file, usage.pos))
		}

		if len(reads) == 0 {
			t.Errorf("%s: the %q link is declared and written (%s) but is NEVER TRAVERSED.\n"+
				"The only observable consequence of a link is its being read; a link that is not read writes the data, "+
				"pays its cost and produces no behavior — the sales channel failure was exactly "+
				"this.\n"+
				"The read path: one of the link service's %s methods or query.Expansion. "+
				"(The delete compensation reading its own link DOES NOT COUNT AS CONSUMPTION.)",
				declared[name], name, writeSummary(writes), strings.Join(linkReadMethods, "/"))
		}
	}
}

// qualifiedLiterals returns the literals of the given type inside the composite
// literal.
//
// Both forms are met: the singular "link.LinkDefinition{...}" and the
// "[]link.LinkDefinition{ {...}, {...} }" the modules use — in the second the
// type of the element literals IS NOT WRITTEN and is known only from the slice
// type. The same duality holds for query.Expansion as well; the shared helper
// prevents the slice form of either one from being overlooked.
func qualifiedLiterals(literal literalSite, importPath, typeName string) []*ast.CompositeLit {
	if qualifiedType(literal.file, literal.value.Type, importPath, typeName) {
		return []*ast.CompositeLit{literal.value}
	}

	array, ok := literal.value.Type.(*ast.ArrayType)
	if !ok || !qualifiedType(literal.file, array.Elt, importPath, typeName) {
		return nil
	}

	var out []*ast.CompositeLit
	for _, element := range literal.value.Elts {
		if value, ok := element.(*ast.CompositeLit); ok {
			out = append(out, value)
		}
	}
	return out
}

// writeSummary summarizes the places the link is written for the error message.
//
// If there is no write site at all the meaning of the finding CHANGES: a link
// that is neither read nor written is a forgotten declaration; a link that is
// written but not read is dead data whose price is paid on every request. The
// message must not confuse the two.
func writeSummary(writes []string) string {
	if len(writes) == 0 {
		return "not written at all either"
	}
	return "write: " + strings.Join(writes, ", ")
}

// linkUsages collects all the usage sites of the declared link names.
//
// The scan goes not from the CALL but from the VALUE: the method name narrows
// the candidate list, but what makes the record is the argument resolving to a
// declared link name. This way unrelated methods with the same name (such as
// repository.Delete) are eliminated by themselves, and the core's own generic
// traverser (core/query, which takes the name at runtime) shows no link as
// "read".
func (a *sourceTree) linkUsages(declared map[string]string, queryPath string) map[string][]linkUsage {
	out := map[string][]linkUsage{}

	methods := append(slices.Clone(linkReadMethods), linkDeleteMethod, linkWriteMethod)
	for _, method := range methods {
		for _, site := range a.calls[method] {
			if len(site.call.Args) < 2 {
				continue
			}
			for _, value := range a.stringValues(site.file, site.fn, site.call.Args[1], 0) {
				if _, isDeclared := declared[value]; !isDeclared {
					continue
				}
				out[value] = append(out[value], linkUsage{
					file: site.file, fn: site.fn, method: method, pos: site.call.Pos(),
				})
			}
		}
	}

	for _, literal := range a.literals {
		for _, expansion := range qualifiedLiterals(literal, queryPath, "Expansion") {
			for _, value := range a.stringValues(literal.file, literal.fn, fieldExpr(expansion, "Link"), 0) {
				if _, isDeclared := declared[value]; !isDeclared {
					continue
				}
				out[value] = append(out[value], linkUsage{
					file: literal.file, fn: literal.fn, method: expansionMethod, pos: expansion.Pos(),
				})
			}
		}
	}

	return out
}
