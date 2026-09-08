package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file holds ADR 0044's structural claim: a storefront read that names a
// SALES CHANNEL IN ITS PATH resolves its scope through the one narrowing helper
// and never off the router itself.
//
// # Why the claim needs a gate and not a code review
//
// The narrowing is an AUTHORIZATION. The path segment is a value the client
// types, and taken alone it would let any holder of a publishable key read any
// channel's catalog by editing a URL — the query-string mistake with a
// different spelling, and one this repository has already refused twice in
// writing. What makes the segment safe is that it is INTERSECTED with the set
// the key carries, so it can only ever pick among channels the caller already
// had.
//
// A handler that reads the segment itself compiles, returns the right shape,
// and passes every test written about the channel it was tested with. The
// failure is only visible under a caller that names a channel it does not hold,
// which is exactly the caller nobody writes a fixture for. That is why this is
// a default-deny scan and not a checklist of the routes anybody remembered.
//
// # Why it was not written when the decision was made
//
// ADR 0044 moved three of four routes on 2026-09-08 and said in its own
// consequences that the audit was owed and not built. The fourth route stayed
// at its old address for a week for no reason anybody defended. Both are the
// same shape: a rule that lives in prose gets applied to the routes somebody
// was looking at.

// channelScopedRoute is one registered storefront route whose path names a
// sales channel.
type channelScopedRoute struct {
	// verb is the HTTP method the route is registered under.
	verb string
	// path is the resolved chi pattern.
	path string
	// handler is the name of the method the route dispatches to.
	handler string
	// pkg is the directory the registration was read out of, relative to the
	// repository root.
	pkg string
}

// where names a route in a failure message.
func (r channelScopedRoute) where() string {
	return r.verb + " " + r.path + " (" + r.pkg + ", " + r.handler + ")"
}

// channelScopeHelper is the one function allowed to turn the path segment into
// a scope.
const channelScopeHelper = "SalesChannelScope"

// channelSegment is the segment a channel-scoped route carries, built from the
// published constant rather than typed again.
var channelSegment = "{" + corehttp.SalesChannelIDParam + "}"

// namesAChannel reports whether a storefront path is channel-scoped, WITHOUT
// asking how the placeholder is spelled.
//
// The spelling is what [TestTheChannelSegmentIsSpelledOnce] checks, so it may
// not also be what decides who gets checked. The first version of this scan
// selected on the exact "{sales_channel_id}" segment, and a mutation found the
// consequence immediately: renaming the placeholder to "{channel_id}" did not
// FAIL the spelling gate, it removed the route from the gate's population and
// left it green on three routes out of four. A population derived from the
// property under audit cannot report a violation of it — it can only shrink.
//
// So the question is asked twice over, from two directions that a rename cannot
// satisfy at once: the collection segment ADR 0044 put in the URL, and any
// placeholder that calls itself a channel.
func namesAChannel(path string) bool {
	if strings.Contains(path, "/sales-channels/") {
		return true
	}

	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") &&
			strings.Contains(part, "channel") {
			return true
		}
	}

	return false
}

// TestEveryChannelScopedRouteNarrowsThroughTheOneHelper is the coverage half:
// every storefront route whose path names a channel reaches
// [corehttp.SalesChannelScope].
//
// The reach is allowed to be one hop — a handler may call a package-local
// wrapper that calls the helper — because both surfaces in the tree do exactly
// that and the wrapper is where each keeps its own account of the decision.
// What is refused is a handler that arrives at the value some other way.
func TestEveryChannelScopedRouteNarrowsThroughTheOneHelper(t *testing.T) {
	t.Parallel()

	routes, callers := channelRouteScan(t)
	require.NotEmpty(t, routes,
		"no channel-scoped storefront route was found at all; the scan has gone BLIND "+
			"(the segment spelling or the route registration shape may have changed)")

	for _, route := range routes {
		assert.True(t, reachesChannelScope(route, callers),
			"%s names a sales channel in its path but never reaches corehttp.%s.\n"+
				"The segment is a value the CLIENT supplies: read on its own it lets any "+
				"publishable key read any channel's catalog by editing a URL. It is safe "+
				"only intersected with the set the key carries, and that intersect is "+
				"written once (ADR 0044).",
			route.where(), channelScopeHelper)
	}
}

// TestNothingOutsideCoreReadsTheChannelSegment is the default-deny half.
//
// The test above says the routes it can SEE go through the helper. This one
// says there is no second way in: outside core/http nothing reads the segment
// off the router at all, so a handler cannot quietly grow its own narrowing —
// nor a correct-looking one that forgets the intersect.
//
// It scans every production tree rather than the api packages, because the
// leak does not care which layer commits it.
func TestNothingOutsideCoreReadsTheChannelSegment(t *testing.T) {
	t.Parallel()

	found := 0
	for _, tree := range []string{modulesDir, "plugins", "internal/workflows", "internal/adminui", "internal/app"} {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			for _, line := range channelParamReads(t, file) {
				found++
				assert.Fail(t, "the sales channel segment is read outside core/http",
					"%s:%d reads the %q path parameter directly.\n"+
						"Only corehttp.%s may: reading the segment without intersecting it "+
						"against the identity's channels turns an authorization into a "+
						"client-supplied display preference, and the failure is invisible "+
						"until a caller names a channel it does not hold.",
					strings.TrimPrefix(file, repoRoot+"/"), line,
					corehttp.SalesChannelIDParam, channelScopeHelper)
			}
		}
	}

	assert.Zero(t, found)
}

// TestTheChannelSegmentIsSpelledOnce checks the routes' segment against the
// published constant.
//
// The paths are whole string literals on purpose — the route audits resolve a
// path from a literal or from a constant whose value is one, and a
// concatenation reads back as unknown, which would drop the route out of their
// population. The cost of that choice is that the spelling is typed again in
// every path, and this is what pays it.
func TestTheChannelSegmentIsSpelledOnce(t *testing.T) {
	t.Parallel()

	routes, _ := channelRouteScan(t)
	require.NotEmpty(t, routes)

	for _, route := range routes {
		assert.Contains(t, route.path, "/sales-channels/"+channelSegment+"/",
			"%s does not carry the channel segment in the published spelling (%s).\n"+
				"A route whose segment is named anything else compiles and serves an "+
				"empty channel id, because corehttp.%s asks the router for %q.",
			route.where(), channelSegment, channelScopeHelper, corehttp.SalesChannelIDParam)
	}
}

// channelRouteScan reads the channel-scoped storefront routes and, for every
// function in the packages that register them, the names it calls.
//
// The walk covers the module api packages AND plugins. Both halves are load
// bearing: three of the four routes are a module's and the fourth is a
// plugin's, and a scan of internal/modules alone would have reported full
// coverage of a rule the plugin was breaking — which is the same blindness
// D30 found in the personal-data audit and D28 in the case-folding one.
func channelRouteScan(t *testing.T) (routes []channelScopedRoute, callers map[string][]string) {
	t.Helper()

	type parsedFile struct {
		pkg  string
		tree *ast.File
	}

	var parsedFiles []parsedFile
	// The constants are indexed per PACKAGE and not per file. A Go package is a
	// directory, and both surfaces keep their path constants in one file and
	// register the routes in another — a per-file index resolves neither, which
	// is what the blindness guard below caught the first time this ran.
	consts := map[string]map[string]string{}

	for _, tree := range []string{modulesDir, "plugins"} {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, file, nil, 0)
			require.NoError(t, err, "%s could not be parsed", file)

			pkg := filepath.ToSlash(strings.TrimPrefix(filepath.Dir(file), repoRoot+"/"))
			parsedFiles = append(parsedFiles, parsedFile{pkg: pkg, tree: parsed})

			if consts[pkg] == nil {
				consts[pkg] = map[string]string{}
			}
			for name, value := range channelFileConstants(parsed) {
				consts[pkg][name] = value
			}
		}
	}

	callers = map[string][]string{}

	for _, file := range parsedFiles {
		ast.Inspect(file.tree, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				if route, ok := channelRoute(typed, consts[file.pkg], file.pkg); ok {
					routes = append(routes, route)
				}
			case *ast.FuncDecl:
				callers[file.pkg+"."+typed.Name.Name] = calledNames(typed)
			}

			return true
		})
	}

	return routes, callers
}

// channelRoute reads a router registration and reports whether its path names a
// sales channel.
func channelRoute(call *ast.CallExpr, consts map[string]string, pkg string) (channelScopedRoute, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) < 2 {
		return channelScopedRoute{}, false
	}

	verb := selector.Sel.Name
	switch verb {
	case "Get", "Post", "Put", "Patch", "Delete":
	default:
		return channelScopedRoute{}, false
	}

	path, known := channelStringValue(call.Args[0], consts)
	if !known || !strings.HasPrefix(path, "/store/v1") || !namesAChannel(path) {
		return channelScopedRoute{}, false
	}

	handler, ok := call.Args[1].(*ast.SelectorExpr)
	if !ok {
		return channelScopedRoute{}, false
	}

	return channelScopedRoute{
		verb:    strings.ToUpper(verb),
		path:    path,
		handler: handler.Sel.Name,
		pkg:     pkg,
	}, true
}

// reachesChannelScope reports whether the route's handler calls the helper, or
// calls something in its own package that does.
func reachesChannelScope(route channelScopedRoute, callers map[string][]string) bool {
	direct := callers[route.pkg+"."+route.handler]
	if slices.Contains(direct, channelScopeHelper) {
		return true
	}

	for _, called := range direct {
		if slices.Contains(callers[route.pkg+"."+called], channelScopeHelper) {
			return true
		}
	}

	return false
}

// channelParamReads returns the lines on which the file reads the channel path
// parameter off the router.
//
// It matches the parameter by VALUE and not by the name of the constant
// holding it: a local constant, the published one and a bare string literal are
// three spellings of the same read, and a scan that only knew one of them would
// be defeated by renaming a constant.
func channelParamReads(t *testing.T, file string) []int {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	require.NoError(t, err, "%s could not be parsed", file)

	consts := channelFileConstants(parsed)
	var lines []int

	ast.Inspect(parsed, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "URLParam" || len(call.Args) < 2 {
			return true
		}
		if value, known := channelStringValue(call.Args[len(call.Args)-1], consts); known &&
			value == corehttp.SalesChannelIDParam {
			lines = append(lines, fset.Position(call.Pos()).Line)
		}

		return true
	})

	return lines
}

// channelStringValue resolves an expression to a string: a literal, a constant
// declared in the same file, or a selector whose name is the published
// parameter constant.
//
// The arch package already has a `literalOrConstant` and it is deliberately not
// reused: that one stops at a same-file constant, which is right for the
// personal-data audit it serves. This scan has to see through a PACKAGE
// QUALIFIER as well, because the two surfaces reach the segment name as
// corehttp.SalesChannelIDParam — and widening the shared helper would change
// the population of an audit that is currently passing, for no benefit to it.
func channelStringValue(expr ast.Expr, consts map[string]string) (string, bool) {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		if typed.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(typed.Value)

		return unquoted, err == nil
	case *ast.Ident:
		value, known := consts[typed.Name]

		return value, known
	case *ast.SelectorExpr:
		// corehttp.SalesChannelIDParam and the like. The package qualifier is
		// not resolved: the audit cares that the VALUE is the channel segment,
		// and the only constant of that name in the tree is the published one.
		if typed.Sel.Name == "SalesChannelIDParam" {
			return corehttp.SalesChannelIDParam, true
		}
	}

	return "", false
}

// channelFileConstants indexes the file's string constants, following one hop of
// aliasing so that `const p = corehttp.SalesChannelIDParam` resolves.
func channelFileConstants(file *ast.File) map[string]string {
	out := map[string]string{}

	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != len(spec.Values) {
			return true
		}
		for i, name := range spec.Names {
			if value, known := channelStringValue(spec.Values[i], out); known {
				out[name.Name] = value
			}
		}

		return true
	})

	return out
}

// calledNames returns the names of the functions and methods the declaration
// calls, unqualified.
func calledNames(fn *ast.FuncDecl) []string {
	var out []string
	if fn.Body == nil {
		return out
	}

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			out = append(out, fun.Name)
		case *ast.SelectorExpr:
			out = append(out, fun.Sel.Name)
		}

		return true
	})

	return out
}
