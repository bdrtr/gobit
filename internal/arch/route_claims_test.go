package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file enforces ONE invariant: EVERY ROUTE ADDRESS THE PROSE NAMES IS ONE
// THE TREE BINDS.
//
// The audits in doc_references_test.go all resolve a NAME — a symbol, a path, a
// test, an ADR. None of them resolves a CLAIM. That gap is docs/gaps.md's D29: the
// documents were once read against the code by hand, 91 statements came back false,
// and the sweep left no standing check behind. The classes the sweep found are
// written down in its own commit message, and the second of them is "moved paths
// and names". A route address is that class in its most decidable form: it is
// written the same way everywhere ("POST /store/v1/carts"), the tree either binds
// it or does not, and no reading of the sentence around it changes the answer.
//
// The address is also the claim that ROTS FASTEST. ADR 0044 moved four storefront
// catalog routes on 2026-09-08, and the sentences naming the old addresses stayed
// where they were. Written against the tree the day after, this audit found three
// live defects in 393 addresses: a measurement naming a moved endpoint, a module's
// godoc naming an endpoint a rename had taken away, and a plugin's godoc dropping
// the trailing slash its own sibling file warns about.
//
// # Why this is not the "verify every claim" gate
//
// It is one class of claim, chosen because a machine can settle it alone:
//
//   - the POPULATION comes from the Go source, by walking the registrations (see
//     [scanBoundRoutes]). It does not come from the documents, so a document
//     naming a route that does not exist cannot remove itself from the audit;
//   - the population is CROSS-CHECKED against a second, independent source — the
//     OpenAPI descriptions the modules write (see
//     [TestTheRouteCollectorIsNotBlind]). Two integration gates keep that set
//     equal to the router of a running server, so a collector that goes blind on
//     a directory or on a nesting form is caught by descriptions it can no longer
//     find. It reaches 278 of the 330 routes and NOT the other 52, which carry no
//     description at all — the admin panel is the largest single block, 18 of
//     them — so the same control
//     puts a floor under each resolution mechanism as well;
//   - the CLAIM is anchored on an HTTP method word followed by a path. The shape
//     does not arise in prose by accident, and it carries the method, so the
//     audit checks the address AND the verb rather than the string alone.
//
// # What this audit does NOT guarantee
//
//   - It does not say the endpoint DOES what the sentence around it says. "POST
//     /store/v1/carts creates an order" resolves and stays silent.
//   - It does not see an address written without its method ("the `/admin/v1`
//     surface", "under `/store/v1/carts`"). A bare path has no verb to check and
//     the prefixes are written far more often than the endpoints.
//   - It does not see a query string: everything from the first "?" on is cut,
//     so a document naming a parameter that no handler reads is not caught here.
//     internal/arch/query_params_test.go audits the parameters.
//   - THE DATED RECORDS ARE OUT OF SCOPE: CHANGELOG.md and the ADR records up to
//     [routeFrozenADR] (see [routeDatedRecord]). This is the audit's largest
//     blind spot, it is a rule rather than a convenience, and the section below
//     says what it costs.
//   - A statement the repository has RETRACTED is out of scope, and the marker is
//     the repository's own: a "~~struck-through~~" span. The correction beside it
//     ("**Corrected 2026-09-07: all four of those names are gone.**") is the
//     statement that stays in scope.
//
// # The dated records are out of scope: the rule, the reason, and the price
//
// THE RULE. CHANGELOG.md and the ADR records up to [routeFrozenADR] are not read.
// A dated record states what was true ON ITS DATE, so holding it to today's tree
// makes a defect out of every historical sentence, and the working agreement
// leaves no repair for one: CLAUDE.md keeps ADRs 0001–0051 as "the historical
// record", allows them a two-line Summary as "the only edit they take", and from
// 0052 on requires a decision to be amended "by adding, not by editing". The
// practice matches — when ADR 0052 undid a layer ADR 0003 had decided, 0003
// gained a Status line and kept its body, in its own words "the record of the
// decision as it was taken".
//
// WHAT STAYS IN SCOPE is everything else, which is most of the prose:
// docs/adr/README.md — the index, rewritten whenever a record is added, so it
// speaks about today — every file under docs/measurements, docs/gaps.md,
// docs/known-limits.md and the other guides, both READMEs, and every Go comment in
// the tree, tests included. Measured on the day this was written: 68 addresses in
// 16 documents and 325 in Go comments are audited; 98 addresses in the records are
// not. [TestTheRouteClaimScannerIsNotBlind] holds a floor under each of those
// numbers, because an exclusion with no floor under it can grow until the audit is
// reading nothing.
//
// THE PRICE IS 17 ADDRESSES, in 10 file-and-claim pairs, that the tree does not
// bind and this audit will not report. Eight are in the changelog: seven name a
// catalog address ADR 0044 moved — ONE of them the entry announcing the move,
// the rest feature entries, two query-string examples and two lines of a probe
// transcript — and the eighth is a parameter spelled the way it was before the
// tree was translated. Nine are in four records. Three of those four read as
// history plainly: ADR 0044's before-state table, ADR 0044's account of what the
// old smoke scenario asked for, ADR 0015's transcript measured on a real server.
// ADR 0050's does NOT: it says in the PRESENT TENSE that an embedder can re-serve
// a storefront catalog address, and that address moved the next morning. That
// sentence is what this rule costs. It is written here rather than left to be
// found, and the repair, when the owner wants one, is an amending record — not an
// edit to 0050 and not an exemption entry.
//
// THE EXCLUSION STOPS at [routeFrozenADR]. A record written after this gate was
// installed is audited like any other document: nothing about a frozen record
// applies to a sentence being written today against a tree the gate can already
// read.

// routeSurface is the set of routes the tree binds, plus the descriptions that
// cross-check it.
type routeSurface struct {
	// routes is keyed "METHOD /pattern".
	routes map[string]boundRoute
	// patterns holds every bound pattern regardless of method, so a claim whose
	// verb is the only thing wrong can be told apart from an address that does
	// not exist at all.
	patterns map[string][]string
	// described is keyed the same way as routes and holds the OpenAPI
	// descriptions, which are the independent cross-check rather than part of
	// the population.
	described map[string]string
	// files counts the production Go files that were parsed.
	files int
}

// boundRoute is what is known about one registration beyond its address, which the
// map key already carries.
type boundRoute struct {
	file string
	// nested says the registration sits inside a chi Route() subtree, so its
	// pattern was assembled from a prefix and a child pattern and appears
	// NOWHERE in the source as one string.
	nested bool
	// joined says the pattern was produced by a "+", in the registration or
	// inside a constant it read. It exists for the blindness control: the
	// concatenation branch is what resolves the admin panel's paths, and losing
	// it takes seventeen routes away in one block while the totals still look
	// healthy.
	joined bool
	// origin says which registration form produced it, and it exists for the
	// blindness control: a collector that quietly lost one form still returns
	// hundreds of routes.
	origin routeOrigin
}

// routeOrigin is the registration form a route was read from.
type routeOrigin string

const (
	// originVerb is r.Get("/x", h) and its eight siblings.
	originVerb routeOrigin = "verb"
	// originMethod is r.Method(http.MethodPost, path, h), which the product
	// module uses to bind the GraphQL endpoint from another package's constant.
	originMethod routeOrigin = "method"
	// originHandler is Handle/HandleFunc, which binds every method at once. The
	// observability mux and net/http/pprof arrive this way.
	originHandler routeOrigin = "handler"
	// originCallback is a core/http.CallbackRoute literal. A provider's inbound
	// endpoint is not registered by the plugin at all — the registry mounts it —
	// so a collector that only reads router calls cannot see it.
	originCallback routeOrigin = "callback"
)

// anyMethod is the method recorded for Handle and HandleFunc, which answer every
// verb. A claim resolves against a pattern bound this way whatever its verb.
const anyMethod = "*"

// routeVerbs maps chi's verb methods to the HTTP method they bind.
var routeVerbs = map[string]string{
	"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH",
	"Delete": "DELETE", "Head": "HEAD", "Options": "OPTIONS",
	"Connect": "CONNECT", "Trace": "TRACE",
}

// httpMethodConstants maps net/http's method constants to their value, so a
// registration written as r.Method(http.MethodPost, …) carries a verb.
var httpMethodConstants = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT",
	"MethodPatch": "PATCH", "MethodDelete": "DELETE", "MethodHead": "HEAD",
	"MethodOptions": "OPTIONS", "MethodConnect": "CONNECT", "MethodTrace": "TRACE",
}

// routeFile is a parsed production Go file with the import table its qualified
// constant references are resolved through.
type routeFile struct {
	path    string
	dir     string
	tree    *ast.File
	imports map[string]string
}

// scanBoundRoutes reads every route the production tree binds.
//
// # Why the source and not a router
//
// The real router needs a database, a container and every module; the arch lane has
// none of them, and internal/app's own unit test says the same thing about the
// composition root's routes. What is available here is the SOURCE, and the source
// is enough because the registration forms are few and each one is read out:
//
//   - the nine verb methods, on any receiver, including the ones reached through
//     a With(...) chain;
//   - Route(prefix, func(r chi.Router){…}), which is the form that makes a
//     pattern appear nowhere in the tree as one string. Two plugins register
//     thirteen routes this way;
//   - Group(func(r chi.Router){…}), which nests without a prefix;
//   - Method and MethodFunc, whose verb is an argument;
//   - Handle and HandleFunc, on chi and on a net/http mux alike;
//   - core/http.CallbackRoute literals, which the callback registry mounts.
//
// # Constants are resolved to a FIXPOINT
//
// A path is often a constant, and a constant is often built out of another one —
// the panel's seventeen paths are all "URLPrefix + …" and the GraphQL endpoint is
// another package's exported constant. Measured when this was written: resolving
// literals alone collected 197 of the 328 routes bound that day, so the constant
// table is rebuilt until it stops growing.
//
// # What the collector cannot see, said plainly
//
// A path that is not a literal and not a constant chain — one assembled at run time
// from a variable — is invisible here. The tree has none today: every registration
// whose first argument failed to resolve was measured, and all of them are calls
// named Get or Delete on something that is not a router (a container lookup, a link
// registry, a map). The day one appears in a DESCRIBED module, the cross-check in
// [TestTheRouteCollectorIsNotBlind] is what notices, because there a bound route is
// also a described one. Where nothing describes the route — the admin panel, the
// observability mux, 52 routes in all — nothing notices, and the audit's answer for
// an address it cannot see is a false accusation against true prose rather than a
// silent forgiveness.
func scanBoundRoutes(t *testing.T) *routeSurface {
	t.Helper()

	files := routeSourceFiles(t)
	constants := routeConstants(files)

	surface := &routeSurface{
		routes:    map[string]boundRoute{},
		patterns:  map[string][]string{},
		described: map[string]string{},
		files:     len(files),
	}

	for _, file := range files {
		collectRegistrations(file, constants, surface)
		collectCallbackRoutes(file, constants, surface)
		collectDescribedRoutes(file, constants, surface)
	}

	require.NotEmpty(t, surface.routes, "no route was collected at all; the walk went blind")

	return surface
}

// routeSourceFiles parses the production Go files of the whole tree.
//
// Test files are left out on purpose: a test binds made-up paths on a throwaway
// router ("/x", "/boom"), and letting those into the population would forgive a
// document naming an address only a test ever had. The examples ARE included even
// though they live in separate Go modules — examples/starter/loyalty binds a
// storefront route that the documents name, and a module outside this one is
// exactly the case ADR 0035 published the vocabulary for.
func routeSourceFiles(t *testing.T) []*routeFile {
	t.Helper()

	var files []*routeFile
	fset := token.NewFileSet()

	err := filepath.WalkDir(repoRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(current, ".go") || strings.HasSuffix(current, "_test.go") {
			return nil
		}

		tree, err := parser.ParseFile(fset, current, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", current, err)
		}

		relative, err := filepath.Rel(repoRoot, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)

		file := &routeFile{
			path:    relative,
			dir:     filepath.ToSlash(filepath.Dir(relative)),
			tree:    tree,
			imports: map[string]string{},
		}
		for _, imported := range tree.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				continue
			}
			name := path[strings.LastIndex(path, "/")+1:]
			if imported.Name != nil {
				name = imported.Name.Name
			}
			file.imports[name] = path
		}
		files = append(files, file)

		return nil
	})
	require.NoError(t, err, "the production Go files could not be scanned")
	require.NotEmpty(t, files, "no production Go file was found")

	return files
}

// routeConstant is a resolved string constant, together with the one thing about
// its definition the blindness control needs.
type routeConstant struct {
	value string
	// joined says a "+" took part in producing the value, at any depth. The
	// admin panel's seventeen paths are all "URLPrefix + …", so this is what
	// notices when the concatenation branch of [resolveRouteParts] stops
	// resolving: those routes then leave the collection in a block, and the
	// totals alone are too coarse to see it.
	joined bool
}

// routeTable is the constant table, keyed by import path and then by name.
type routeTable map[string]map[string]routeConstant

// routeConstants builds the table of string constants, keyed by import path and
// name, to a fixpoint.
func routeConstants(files []*routeFile) routeTable {
	table := routeTable{}
	for _, file := range files {
		if table[routeImportPath(file.dir)] == nil {
			table[routeImportPath(file.dir)] = map[string]routeConstant{}
		}
	}

	for range len(files) {
		grew := false
		for _, file := range files {
			known := table[routeImportPath(file.dir)]
			ast.Inspect(file.tree, func(node ast.Node) bool {
				spec, ok := node.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for i, name := range spec.Names {
					if i >= len(spec.Values) {
						continue
					}
					if _, done := known[name.Name]; done {
						continue
					}
					if resolved, ok := resolveRouteParts(spec.Values[i], file, table); ok {
						known[name.Name] = resolved
						grew = true
					}
				}

				return true
			})
		}
		if !grew {
			break
		}
	}

	return table
}

// routeImportPath turns a repository-relative directory into an import path.
func routeImportPath(dir string) string {
	if dir == "." {
		return modulePath
	}

	return modulePath + "/" + dir
}

// resolveRouteParts evaluates a string expression — a literal, a constant of this
// package, a constant of an imported package, or a sum of those — and reports how
// it was built.
func resolveRouteParts(expr ast.Expr, file *routeFile, table routeTable) (routeConstant, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if value := stringLiteral(typed); value != "" {
			return routeConstant{value: value}, true
		}
	case *ast.Ident:
		resolved, ok := table[routeImportPath(file.dir)][typed.Name]

		return resolved, ok
	case *ast.SelectorExpr:
		qualifier, ok := typed.X.(*ast.Ident)
		if !ok {
			return routeConstant{}, false
		}
		path, ok := file.imports[qualifier.Name]
		if !ok {
			return routeConstant{}, false
		}
		resolved, ok := table[path][typed.Sel.Name]

		return resolved, ok
	case *ast.BinaryExpr:
		if typed.Op != token.ADD {
			return routeConstant{}, false
		}
		left, leftOK := resolveRouteParts(typed.X, file, table)
		right, rightOK := resolveRouteParts(typed.Y, file, table)

		return routeConstant{value: left.value + right.value, joined: true}, leftOK && rightOK
	}

	return routeConstant{}, false
}

// resolveRouteString is [resolveRouteParts] for the callers that only need the
// value.
func resolveRouteString(expr ast.Expr, file *routeFile, table routeTable) (string, bool) {
	resolved, ok := resolveRouteParts(expr, file, table)

	return resolved.value, ok
}

// resolveRouteMethod evaluates the verb argument of Method and of a callback route.
func resolveRouteMethod(expr ast.Expr, file *routeFile, table routeTable) (string, bool) {
	if selector, ok := expr.(*ast.SelectorExpr); ok {
		if method, ok := httpMethodConstants[selector.Sel.Name]; ok {
			return method, true
		}
	}
	if value, ok := resolveRouteString(expr, file, table); ok {
		return strings.ToUpper(value), true
	}

	return "", false
}

// collectRegistrations walks one file's router calls, carrying the prefix that
// Route() opens.
func collectRegistrations(file *routeFile, table routeTable, surface *routeSurface) {
	var walk func(node ast.Node, prefix routeConstant)

	record := func(method string, prefix, pattern routeConstant, origin routeOrigin) {
		surface.add(method, prefix.value+pattern.value, file.path,
			prefix.value != "", prefix.joined || pattern.joined, origin)
	}

	walk = func(node ast.Node, prefix routeConstant) {
		ast.Inspect(node, func(current ast.Node) bool {
			call, ok := current.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			name := selector.Sel.Name

			switch {
			case name == "Route" && len(call.Args) == 2:
				child, ok := call.Args[1].(*ast.FuncLit)
				if !ok {
					return true
				}
				opened, ok := resolveRouteParts(call.Args[0], file, table)
				if !ok {
					return true
				}
				walk(child.Body, routeConstant{
					value:  prefix.value + opened.value,
					joined: prefix.joined || opened.joined,
				})

				return false
			case name == "Group" && len(call.Args) == 1:
				child, ok := call.Args[0].(*ast.FuncLit)
				if !ok {
					return true
				}
				walk(child.Body, prefix)

				return false
			case routeVerbs[name] != "" && len(call.Args) >= 1:
				if pattern, ok := resolveRouteParts(call.Args[0], file, table); ok {
					record(routeVerbs[name], prefix, pattern, originVerb)
				}
			case (name == "Method" || name == "MethodFunc") && len(call.Args) >= 2:
				method, methodOK := resolveRouteMethod(call.Args[0], file, table)
				pattern, patternOK := resolveRouteParts(call.Args[1], file, table)
				if methodOK && patternOK {
					record(method, prefix, pattern, originMethod)
				}
			case (name == "Handle" || name == "HandleFunc") && len(call.Args) >= 1:
				if pattern, ok := resolveRouteParts(call.Args[0], file, table); ok {
					record(anyMethod, prefix, pattern, originHandler)
				}
			}

			return true
		})
	}

	walk(file.tree, routeConstant{})
}

// collectCallbackRoutes reads the provider callbacks, which no router call binds.
//
// A plugin returns a [corehttp.CallbackRoute] and the registry mounts it; the path
// never appears next to a verb method. The registry's own default is POST when the
// literal names no method, and this reads it the same way — otherwise the one
// callback in the tree would be collected under the wrong verb.
func collectCallbackRoutes(file *routeFile, table routeTable, surface *routeSurface) {
	ast.Inspect(file.tree, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if routeTypeName(literal.Type) != "CallbackRoute" {
			return true
		}

		method, pattern := "", routeConstant{}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Method":
				if value, ok := resolveRouteMethod(pair.Value, file, table); ok {
					method = value
				}
			case "Path":
				if resolved, ok := resolveRouteParts(pair.Value, file, table); ok {
					pattern = resolved
				}
			}
		}
		if pattern.value == "" {
			return true
		}
		if method == "" {
			method = "POST"
		}
		surface.add(method, pattern.value, file.path, false, pattern.joined, originCallback)

		return true
	})
}

// collectDescribedRoutes reads the OpenAPI descriptions, which are the INDEPENDENT
// cross-check and not part of the population.
func collectDescribedRoutes(file *routeFile, table routeTable, surface *routeSurface) {
	ast.Inspect(file.tree, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Describe" || len(call.Args) < 2 {
			return true
		}
		method, methodOK := resolveRouteMethod(call.Args[0], file, table)
		pattern, patternOK := resolveRouteString(call.Args[1], file, table)
		if !methodOK || !patternOK || !strings.HasPrefix(pattern, "/") {
			return true
		}
		surface.described[method+" "+pattern] = file.path

		return true
	})
}

// routeTypeName is the bare type name of a composite literal's type expression.
func routeTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typed.Sel.Name
	}

	return ""
}

// add records one bound route. Anything that is not a rooted path is dropped: the
// verb names are read off the CALL and a call named Get on something that is not a
// router is common (a container lookup, a link registry, a map).
func (s *routeSurface) add(method, pattern, file string, nested, joined bool, origin routeOrigin) {
	if !strings.HasPrefix(pattern, "/") {
		return
	}
	key := method + " " + pattern
	if _, seen := s.routes[key]; seen {
		return
	}
	s.routes[key] = boundRoute{file: file, nested: nested, joined: joined, origin: origin}
	s.patterns[pattern] = append(s.patterns[pattern], method)
}

// binds says whether the tree answers this method at this pattern.
func (s *routeSurface) binds(method, pattern string) bool {
	if _, ok := s.routes[method+" "+pattern]; ok {
		return true
	}
	_, ok := s.routes[anyMethod+" "+pattern]

	return ok
}

// routeAddress is an HTTP method followed by a rooted path.
//
// The method word is the anchor. A bare path is written far more often (a prefix,
// a directory, a URL in a link) and carries no verb to check; the two together are
// a sentence saying "this endpoint exists", and that is the claim being audited.
var routeAddress = regexp.MustCompile(`\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (/[A-Za-z0-9_{},.*:@/-]*)`)

// routeClaim is one route address written in prose.
type routeClaim struct {
	file    string
	line    int
	method  string
	pattern string
	// markdown says the claim came from a document rather than a Go comment.
	markdown bool
}

func (c routeClaim) key() string { return c.method + " " + c.pattern }

func (c routeClaim) where() string { return fmt.Sprintf("%s:%d", c.file, c.line) }

// routeADRRecord captures the NUMBER of an ADR record file. docs/adr/README.md is
// deliberately not matched: it is the index, not a record.
//
// The path is matched anywhere rather than only at the root, so that a nested
// checkout — the worktrees this repository keeps under .claude — cannot bring the
// records back into scope through a longer path and make this audit report defects
// the tree at the root does not have.
var routeADRRecord = regexp.MustCompile(`(^|/)docs/adr/(\d{4})-`)

// routeFrozenADR is the highest ADR number that existed when this gate was
// installed. Records up to it are the historical record and are not audited;
// 0059 onward IS audited, and stays audited until someone raises this line on
// purpose and says why.
//
// Raising it is the one honest way to forgive a later record, and it is deliberately
// noisy: it names a number in a diff. The other two ways to answer a failure — write
// the amending record, or take the sentence out — do not touch this line at all.
const routeFrozenADR = 58

// routeDatedRecord says whether a document is a DATED RECORD, which this audit does
// not read.
//
// Two file classes are records and nothing else is: CHANGELOG.md, and the ADR
// records up to [routeFrozenADR]. The rule, what stays in scope, and the price it
// charges are at the head of this file, measured.
func routeDatedRecord(doc string) bool {
	if doc == "CHANGELOG.md" || strings.HasSuffix(doc, "/CHANGELOG.md") {
		return true
	}
	match := routeADRRecord.FindStringSubmatch(doc)
	if match == nil {
		return false
	}
	number, err := strconv.Atoi(match[2])

	return err == nil && number <= routeFrozenADR
}

// routeProse strips the spans the repository writes to RETRACT a statement.
//
// The marker is "~~struck through~~" and it is not a style: it is how this
// repository corrects a record it will not rewrite: ADR 0033 carries one over the
// erasure endpoints it named, ADR 0044 over the route that had not moved yet, and
// columns_test.go over a predicate. A struck span is not a claim about today; the
// correction written beside it is, and that stays in scope.
//
// Backticked spans are skipped when looking for the marker, because Postgres spells
// its case-insensitive regex operator `~~*` and one measurement document quotes it —
// and this sentence has to write it in backticks for the same reason, which is the
// defect the group control in [collectCommentRouteClaims] found in this very file on
// the day it was added. The span's TEXT is kept: a route address inside backticks is
// a claim like any other.
type routeProse struct{ struck bool }

// live returns the line with its struck spans blanked out.
func (p *routeProse) live(line string) string {
	runes := []rune(line)
	fenced := strings.HasPrefix(strings.TrimSpace(line), "```")
	quoted := false

	var out strings.Builder
	for i := 0; i < len(runes); i++ {
		switch {
		case !fenced && runes[i] == '`':
			quoted = !quoted
		case !quoted && runes[i] == '~' && i+1 < len(runes) && runes[i+1] == '~':
			p.struck = !p.struck
			i++

			continue
		}
		if p.struck {
			out.WriteRune(' ')

			continue
		}
		out.WriteRune(runes[i])
	}

	return out.String()
}

// routeClaimsIn extracts the addresses of one text, already stripped of its struck
// spans. The query string is cut: what this audits is the address, not its
// parameters.
func routeClaimsIn(text, file string, line int, markdown bool) []routeClaim {
	var claims []routeClaim
	for _, match := range routeAddress.FindAllStringSubmatch(text, -1) {
		pattern := strings.TrimRight(match[2], ".,;:)\"'`*]")
		if cut := strings.IndexByte(pattern, '?'); cut >= 0 {
			pattern = pattern[:cut]
		}
		if pattern == "/" || pattern == "" {
			continue
		}
		for _, expanded := range expandRouteShorthand(pattern) {
			claims = append(claims, routeClaim{
				file:     file,
				line:     line,
				method:   match[1],
				pattern:  expanded,
				markdown: markdown,
			})
		}
	}

	return claims
}

// expandRouteShorthand expands the "{a,b,c}" shorthand a document uses to name
// several endpoints in one line ("GET /store/v1/{collections,categories,tags}").
//
// A brace WITHOUT a comma is a chi path parameter and is left alone. The
// distinction is the comma and nothing else, which is why it is decidable.
func expandRouteShorthand(pattern string) []string {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		return []string{pattern}
	}
	width := strings.IndexByte(pattern[open:], '}')
	if width < 0 {
		return []string{pattern}
	}
	inside := pattern[open+1 : open+width]
	head, tail := pattern[:open], pattern[open+width+1:]

	if !strings.Contains(inside, ",") {
		expanded := expandRouteShorthand(tail)
		out := make([]string, 0, len(expanded))
		for _, rest := range expanded {
			out = append(out, pattern[:open+width+1]+rest)
		}

		return out
	}

	var out []string
	for _, alternative := range strings.Split(inside, ",") {
		out = append(out, expandRouteShorthand(head+strings.TrimSpace(alternative)+tail)...)
	}

	return out
}

// collectRouteClaims reads every route address written in the documents and in the
// Go comments.
//
// Go comments are read from EVERY .go file, tests included. A test's godoc names
// the endpoint it drives as often as a module's does, and a test naming an address
// that no longer exists is the same defect — measured, the tree carries 325 of these
// against 68 in the documents that are in scope.
func collectRouteClaims(t *testing.T) []routeClaim {
	t.Helper()

	claims := collectMarkdownRouteClaims(t)
	claims = append(claims, collectCommentRouteClaims(t)...)

	return claims
}

// collectMarkdownRouteClaims reads the addresses in the documents.
func collectMarkdownRouteClaims(t *testing.T) []routeClaim {
	t.Helper()

	var claims []routeClaim
	for _, doc := range markdownDocs(t) {
		if routeDatedRecord(doc.path) {
			continue
		}

		prose := &routeProse{}
		for i, line := range doc.lines {
			claims = append(claims, routeClaimsIn(prose.live(line), doc.path, i+1, true)...)
		}
		assert.False(t, prose.struck,
			"%s ends inside a struck-through span: every address after the opening "+
				"\"~~\" was silently dropped from this audit. Close the span, or the "+
				"scanner is blind to the rest of the file", doc.path)
	}

	return claims
}

// collectCommentRouteClaims reads the addresses in the Go comments.
func collectCommentRouteClaims(t *testing.T) []routeClaim {
	t.Helper()

	fset := token.NewFileSet()

	var claims []routeClaim
	err := filepath.WalkDir(repoRoot, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if slices.Contains(skippedDirs, entry.Name()) {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(current, ".go") {
			return nil
		}

		tree, err := parser.ParseFile(fset, current, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("%s: %w", current, err)
		}
		relative, err := filepath.Rel(repoRoot, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)

		for _, group := range tree.Comments {
			prose := &routeProse{}
			for _, comment := range group.List {
				line := fset.Position(comment.Pos()).Line
				for offset, text := range strings.Split(comment.Text, "\n") {
					claims = append(claims,
						routeClaimsIn(prose.live(text), relative, line+offset, false)...)
				}
			}
			assert.False(t, prose.struck,
				"the comment at %s:%d ends inside a struck-through span: every address "+
					"after the opening \"~~\" was silently dropped from this audit. Close "+
					"the span, or put the \"~~\" in backticks if it is being quoted rather "+
					"than used, because the scanner is blind to the rest of the comment",
				relative, fset.Position(group.Pos()).Line)
		}

		return nil
	})
	require.NoError(t, err, "the Go comments could not be scanned")

	return claims
}

// routeClaimExemption is an address named on purpose although the tree does not
// bind it.
type routeClaimExemption struct {
	file   string
	claim  string
	reason string
}

// routeClaimExemptions are the addresses a text names deliberately.
//
// The price of an exemption is measured exactly, the same way
// [pathReferenceExemptions] pays it: the day the route DOES exist, or the day the
// sentence goes away, the audit fails and asks for the entry to be removed. A
// forgiveness that outlives its reason is what D16 records.
var routeClaimExemptions = []routeClaimExemption{
	{
		file:  "internal/modules/cart/api/store.go",
		claim: "POST /store/v1/orders",
		reason: "A REJECTED alternative, written as one: the godoc argues why completing " +
			"a cart is not a POST to the order collection, and naming the address it " +
			"is not is the argument. It is the shape [isADROptionSection] forgives in " +
			"an ADR, in the one place that rule cannot reach — a Go comment has no " +
			"sections.",
	},
}

// TestTheRouteAddressesInTheProseExist verifies that every route a document or a
// comment names is one the tree really binds, with that verb.
//
// # What a failure means, and what it does not
//
// It means the sentence is wrong about today's tree. There are three honest
// endings, and adding an exemption is the last of them:
//
//  1. the address moved and the sentence should follow it;
//  2. the document is a living one that recorded a fact which has since changed —
//     a measurement, a limits list — and the repository's way of saying so is to
//     strike the old statement through and write the correction beside it, which
//     is what docs/measurements/b2-remainder.md already does with its own
//     findings;
//  3. the address is named on purpose — a rejected alternative, a probe transcript
//     — and then it goes into [routeClaimExemptions] WITH ITS REASON.
//
// It is never "add it to the exemptions because a record may not be edited". The
// records that may not be edited are not in scope at all (see [routeDatedRecord]),
// and for a record written after [routeFrozenADR] — which IS in scope — the honest
// endings are the amending record CLAUDE.md asks for, or the sentence going away.
func TestTheRouteAddressesInTheProseExist(t *testing.T) {
	t.Parallel()

	surface := scanBoundRoutes(t)
	claims := collectRouteClaims(t)

	require.NotEmpty(t, claims, "no route address was found in the prose at all")

	used := map[string]bool{}
	for _, claim := range claims {
		if surface.binds(claim.method, claim.pattern) {
			continue
		}
		exemption := findRouteClaimExemption(claim)
		if exemption != nil {
			used[claim.file+"\x00"+claim.key()] = true

			continue
		}

		assert.Fail(t, "a route address that the tree does not bind",
			"%s names %q.\n%s\n"+
				"Either the address moved and this sentence has to follow it, or the "+
				"sentence recorded something that has since changed and the change belongs "+
				"beside it with the old statement struck through, or the address is named "+
				"deliberately and belongs in routeClaimExemptions with its reason.",
			claim.where(), claim.key(), routeClaimHint(surface, claim))
	}

	for _, exemption := range routeClaimExemptions {
		assert.True(t, used[exemption.file+"\x00"+exemption.claim],
			"the exemption for %q in %s is not needed any more: the route now exists, or "+
				"the sentence naming it is gone. Take the entry out — a forgiveness that "+
				"outlives its reason forgives the next defect silently.",
			exemption.claim, exemption.file)
	}
}

// routeClaimHint says what is wrong with an address, when the tree can tell.
//
// The three cases are worth separating: a verb that does not answer at a bound
// pattern is a different repair from an address that does not exist, and a
// parameter spelled differently from the way chi binds it is a third — the pattern
// answers, but the name a client generator reads from the document is not the name
// the server publishes.
func routeClaimHint(surface *routeSurface, claim routeClaim) string {
	if methods, ok := surface.patterns[claim.pattern]; ok {
		sort.Strings(methods)

		return fmt.Sprintf("The pattern IS bound, but only for %s.", strings.Join(methods, ", "))
	}

	for _, near := range []string{claim.pattern + "/", strings.TrimSuffix(claim.pattern, "/")} {
		if _, ok := surface.patterns[near]; ok && near != claim.pattern {
			return fmt.Sprintf(
				"The tree binds %q, which differs only in the trailing slash. chi reports "+
					"the pattern it was registered with, and a route opened with Route() "+
					"and a child pattern of \"/\" carries it.", near)
		}
	}

	shape := routeParameterShape(claim.pattern)
	for pattern := range surface.patterns {
		if routeParameterShape(pattern) == shape {
			return fmt.Sprintf(
				"The tree binds %q, which differs only in the name of a path parameter. "+
					"chi's name is the one the OpenAPI document publishes, so the document "+
					"and the server have to spell it the same way.", pattern)
		}
	}

	return "No pattern of that shape is bound anywhere in the tree.\n" +
		"What the collector CAN see, so that this accusation can be weighed: literal " +
		"patterns, patterns built from constants and from sums of them, and the ones " +
		"assembled inside a Route() subtree, which appear nowhere in the source as one " +
		"string. The single form it cannot see is a pattern computed at RUN TIME from a " +
		"variable; the tree binds none today and TestTheRouteCollectorIsNotBlind holds a " +
		"floor under each of the other forms."
}

// routeParameterShape blanks the parameter names out of a pattern.
var routeParameter = regexp.MustCompile(`\{[^}]*\}`)

func routeParameterShape(pattern string) string {
	return routeParameter.ReplaceAllString(pattern, "{}")
}

// findRouteClaimExemption returns the exemption covering a claim, or nil.
func findRouteClaimExemption(claim routeClaim) *routeClaimExemption {
	for i, exemption := range routeClaimExemptions {
		if exemption.file == claim.file && exemption.claim == claim.key() {
			return &routeClaimExemptions[i]
		}
	}

	return nil
}

// TestTheRouteCollectorIsNotBlind pins down what [scanBoundRoutes] can see.
//
// A doc audit built on a blind collector is worse than no audit: it reports
// coverage over the routes it cannot see and then calls a true sentence false. The
// control has two halves.
//
// # The independent half
//
// Every OpenAPI description in the tree has to be a route the collector found. The
// description set is written by hand, in another file, by another method, and two
// integration gates keep it equal to a RUNNING server's router: internal/e2e's
// TestEveryRealRouteIsDescribed fails on a route with no description and its ledger
// may only shrink, and Doc.UnmatchedDescriptions reports the other direction.
// So this comparison is not the collector checking itself.
//
// # The shape half
//
// Every registration form has to be represented, because a collector that lost one
// of them still returns hundreds of routes and looks healthy. The assertions name
// the FORM, not the address, so moving a route does not falsify the control.
func TestTheRouteCollectorIsNotBlind(t *testing.T) {
	t.Parallel()

	surface := scanBoundRoutes(t)

	assert.GreaterOrEqual(t, len(surface.routes), 250,
		"the collector found %d routes; the tree bound 328 when this control was "+
			"written and a drop of that size means a walk stopped somewhere",
		len(surface.routes))
	assert.GreaterOrEqual(t, surface.files, 550,
		"only %d production Go files were parsed; there were 697 when this control was "+
			"written and the walk is no longer reaching the tree",
		surface.files)

	assert.GreaterOrEqual(t, len(surface.described), 200,
		"only %d OpenAPI descriptions were found, so the independent cross-check "+
			"below is comparing against almost nothing", len(surface.described))

	for described, file := range surface.described {
		_, found := surface.routes[described]
		assert.True(t, found,
			"%s describes %q and the collector did not find that route.\n"+
				"The description set is kept equal to a running server's router by "+
				"internal/e2e's TestEveryRealRouteIsDescribed, so this is the collector "+
				"going blind, not the description being wrong.", file, described)
	}

	forms := map[routeOrigin]int{}
	nested, joined, plugins, published, composition, outside := 0, 0, 0, 0, 0, 0
	for _, route := range surface.routes {
		forms[route.origin]++
		if route.nested {
			nested++
		}
		if route.joined {
			joined++
		}
		switch {
		case strings.HasPrefix(route.file, "plugins/"):
			plugins++
		case strings.HasPrefix(route.file, "core/"):
			published++
		case strings.HasPrefix(route.file, "internal/app/"):
			composition++
		case strings.HasPrefix(route.file, "examples/"):
			outside++
		}
	}

	assert.Positive(t, forms[originVerb], "no route was read from a verb method")
	assert.Positive(t, forms[originMethod],
		"no route was read from Method/MethodFunc; the GraphQL endpoint is bound that "+
			"way, from another package's constant")
	assert.Positive(t, forms[originHandler],
		"no route was read from Handle/HandleFunc; /metrics and the pprof endpoints "+
			"are bound that way")
	assert.Positive(t, forms[originCallback],
		"no route was read from a CallbackRoute literal; a provider's inbound endpoint "+
			"is mounted by the registry and no router call names it")

	assert.GreaterOrEqual(t, nested, 10,
		"only %d routes were assembled from a Route() prefix; thirteen are registered "+
			"that way today and their patterns appear NOWHERE in the tree as one "+
			"string, which is the blindness this collector was written to avoid",
		nested)
	assert.GreaterOrEqual(t, joined, 12,
		"only %d routes had their pattern JOINED with a \"+\"; seventeen do today and "+
			"they are the admin panel's, every one. They leave IN A BLOCK the moment the "+
			"concatenation branch of resolveRouteParts stops resolving, and the "+
			"description cross-check above does not notice: fifty-two bound routes carry "+
			"no OpenAPI description and the panel is the largest block, 18 of them",
		joined)
	assert.Positive(t, plugins, "no plugin route was collected")
	assert.Positive(t, published, "no route was collected from the published core")
	assert.Positive(t, composition, "no route bound at the composition root was collected")
	assert.Positive(t, outside,
		"no route was collected from examples/, which is a SEPARATE Go module; a "+
			"collector that only walks this module cannot see an embedder's")
}

// TestTheRouteClaimScannerIsNotBlind pins down what [collectRouteClaims] sees and,
// just as importantly, what it drops.
//
// The population of an audit must not be derived from the property it audits, and
// the exclusions here are the place that could go wrong: each one is a door out of
// the audit, so each one is held to a shape rather than to a judgement.
func TestTheRouteClaimScannerIsNotBlind(t *testing.T) {
	t.Parallel()

	claims := collectRouteClaims(t)

	documents, comments := 0, 0
	audited := map[string]bool{}
	for _, claim := range claims {
		if claim.markdown {
			documents++
			audited[claim.file] = true

			continue
		}
		comments++
	}

	assert.GreaterOrEqual(t, documents, 50,
		"only %d route addresses were found in the documents; there were 68 in scope "+
			"when this control was written", documents)
	assert.GreaterOrEqual(t, comments, 250,
		"only %d route addresses were found in the Go comments; there were 325 when "+
			"this control was written", comments)

	// The floor under the DOCUMENTS THAT REMAIN, which is what the dated-record
	// exclusion could eat. The exclusion is a rule with a reason (see
	// [routeDatedRecord]); a rule with no floor under it is a door that can be
	// widened until the audit reads nothing and still passes.
	assert.GreaterOrEqual(t, len(audited), 12,
		"only %d documents were audited at all; 16 carried an address when this control "+
			"was written. The dated-record exclusion is the thing most likely to have "+
			"eaten the rest — check what routeDatedRecord is now matching", len(audited))

	assert.False(t, routeDatedRecord("docs/adr/README.md"),
		"the ADR INDEX is not a record: it is rewritten whenever a record is added, so "+
			"it describes today's tree and stays in scope")
	assert.False(t, routeDatedRecord("docs/measurements/b2-remainder.md"),
		"a measurement is kept current — b2-remainder.md carries its own corrections — "+
			"and stays in scope")
	assert.False(t, routeDatedRecord(fmt.Sprintf("docs/adr/%04d-a-record-written-later.md", routeFrozenADR+1)),
		"the exclusion reaches past routeFrozenADR and would forgive a record written "+
			"AFTER this gate; nothing about the historical record applies to one of those")

	// The frozen line may only name a record that is ALREADY history. Raising it
	// past the tree pre-forgives records nobody has written, which is the one way
	// this exclusion could be widened without anybody reading a new sentence.
	highest := 0
	for _, doc := range markdownDocs(t) {
		match := routeADRRecord.FindStringSubmatch(doc.path)
		if match == nil {
			continue
		}
		if number, err := strconv.Atoi(match[2]); err == nil && number > highest {
			highest = number
		}
	}
	require.Positive(t, highest,
		"no ADR record was found at all, so the boundary of the dated-record exclusion "+
			"cannot be checked against anything")
	assert.LessOrEqual(t, routeFrozenADR, highest,
		"routeFrozenADR is %d and the highest record in the tree is %d: the exclusion "+
			"reaches past the records that EXIST and would forgive an address in an ADR "+
			"nobody has written yet", routeFrozenADR, highest)
	assert.True(t, routeDatedRecord("CHANGELOG.md"), "the changelog is a dated record")
	assert.True(t, routeDatedRecord(fmt.Sprintf("docs/adr/%04d-a-frozen-record.md", routeFrozenADR)),
		"the records up to routeFrozenADR are the historical record and are excluded")
	assert.True(t, routeDatedRecord(".claude/worktrees/wf_1/CHANGELOG.md"),
		"a nested checkout brought an excluded record back into scope through a longer "+
			"path, which would make this audit report defects the tree does not have")

	files := map[string]bool{}
	for _, claim := range claims {
		files[claim.file] = true
		assert.False(t, routeDatedRecord(claim.file),
			"%s is a dated record, out of scope, and a claim was taken from it anyway",
			claim.file)
	}
	assert.GreaterOrEqual(t, len(files), 90,
		"the addresses came from only %d files; the scan is not reaching the tree",
		len(files))

	for _, group := range []string{"docs/", "internal/modules/", "plugins/", "core/", "internal/e2e/"} {
		found := false
		for file := range files {
			if strings.HasPrefix(file, group) {
				found = true

				break
			}
		}
		assert.True(t, found, "no route address was read from anything under %q", group)
	}

	prose := &routeProse{}
	corrected := routeClaimsIn(prose.live("~~GET /gone~~ **Corrected: GET /here**"), "probe", 1, true)
	require.Len(t, corrected, 1,
		"a struck-through address and the correction beside it were not told apart")
	assert.Equal(t, "GET /here", corrected[0].key(),
		"the struck address was read as the live claim and the correction was dropped")
	assert.False(t, prose.struck, "the strike state did not close on a balanced line")

	quoted := &routeProse{}
	assert.Len(t, routeClaimsIn(quoted.live("the operator writes `title ~~* 'x'` and calls GET /store/v1/carts"),
		"probe", 1, true), 1,
		"a backticked \"~~\" was read as a strike marker, which swallows the rest of "+
			"the document")

	assert.Len(t, expandRouteShorthand("/store/v1/{a,b,c}"), 3,
		"the \"{a,b,c}\" shorthand a document uses to name three endpoints at once was "+
			"not expanded")
	assert.Len(t, expandRouteShorthand("/store/v1/carts/{id}"), 1,
		"a path parameter was mistaken for the shorthand")
}
