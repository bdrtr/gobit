package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	pathpkg "path"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file holds ADR 0390's gate: a contract in core/provider arrives with the
// module or core function that calls it. ADR 0153 measured the shape it refuses
// — an interface with no caller, plus its published-name lines, left the whole
// suite green.

// providerImport is the package that declares the provider contracts.
const providerImport = modulePath + "/core/provider"

// contractCallerFloor is the fewest contracts with a method of their own to
// report through that the reader may find; fewer means the contract reader went
// blind, not that the contracts went away. It counts only the contracts that
// declare such a method, so a reader that matches the wrong kind of type —
// structs, which declare none — fails here, and so does one that reads only
// part of the package.
const contractCallerFloor = 6

// notACaller are the trees whose calls do not count, by repo-relative prefix,
// each with the reason.
var notACaller = map[string]string{
	"core/provider/":     "declares the contracts",
	"core/providertest/": "calls every method to check an implementation, on nobody's behalf",
	"core/plugin/":       "relays a plugin's registration to the core; a call here would be a forwarder",
	"plugins/": "implements contracts; a plugin imports no module (Principle 2.4) and is not " +
		"the core's side",
}

// notAReport are the methods whose call reports nothing: ID names a provider,
// Close releases one.
var notAReport = []string{"ID", "Close"}

// providerContract is one exported interface of core/provider as the gate sees
// it.
type providerContract struct {
	// methods goes from each of the interface's OWN method names, [notAReport]
	// aside, to the exported names of core/provider types its signature spells.
	// A contract that declares none has here the methods of the core/provider
	// contracts it embeds.
	methods map[string][]string
	// composite is set when methods came from the embedded contracts.
	composite bool
}

// TestEveryProviderContractHasACaller is ADR 0390's gate.
//
// It fails for an exported interface in core/provider whose own methods, ID
// and Close aside, no function outside the trees in [notACaller] calls while
// naming the contract or a core/provider type of the called method's
// signature. The unit is the function — a FuncDecl, signature included, or a
// function literal at package level — so a file that calls one contract cannot
// lend that call to another contract it merely stores.
//
// # What it does not see
//
// It reads syntax, not types. A caller written to pass — a function that names
// the contract and calls a method of the same name on something else — is
// admitted, and so is a core subscriber that feeds the contract what the bus
// already carries; whether that caller's fact belonged on the bus is the
// review's judgement, as ADR 0390 says. A contract's methods are the ones it
// declares; one that declares none has those of the core/provider contracts it
// embeds, and one that still has none fails as a slot with nothing to call.
func TestEveryProviderContractHasACaller(t *testing.T) {
	t.Parallel()

	found := readProviderCallers(scanProductionSource(t))
	require.GreaterOrEqual(t, len(found.reporting), contractCallerFloor,
		"the contract reader found %d exported interfaces with a method in core/provider "+
			"(%v); the package holds more than that, so the reader has gone BLIND rather "+
			"than the contracts having gone away", len(found.reporting), found.reporting)

	if len(found.called) == 0 {
		t.Fatalf("not one of the %d contracts in core/provider has a caller; the caller "+
			"reader has gone BLIND — the payment, fulfillment, notification and file modules, "+
			"core/errorreport and the review job call them today", len(found.contracts))
	}

	for _, name := range slices.Sorted(maps.Keys(found.contracts)) {
		if _, ok := found.called[name]; ok {
			continue
		}
		if len(found.contracts[name].methods) == 0 {
			t.Errorf("core/provider.%s has nothing a caller could call (ADR 0390): it declares no "+
				"method but ID and Close, and embeds no contract that does. A slot that reports "+
				"nothing is the inert shape ADR 0153 measured.", name)

			continue
		}
		t.Errorf("core/provider.%s has no caller on the core's side of the plugin boundary "+
			"(ADR 0390): no function outside %v calls one of its methods %v while naming the "+
			"contract or a type of that method's signature.\n"+
			"A contract arrives with the module or core function that calls it; a caller that "+
			"builds the input elsewhere names the contract or the input type in the calling "+
			"function. A registration that only stores the provider, or a call to ID or "+
			"Close, is not a caller.",
			name, slices.Sorted(maps.Keys(notACaller)),
			slices.Sorted(maps.Keys(found.contracts[name].methods)))
	}
}

// providerCallers is what the gate reads from a tree.
type providerCallers struct {
	// contracts are the exported interfaces of core/provider, Provider aside.
	contracts map[string]providerContract
	// reporting names, sorted, the contracts with a method of their own.
	reporting []string
	// called goes from a contract to the first file that calls it.
	called map[string]string
}

// readProviderCallers reads the contracts of core/provider and their callers.
func readProviderCallers(tree *sourceTree) providerCallers {
	found := providerCallers{contracts: readProviderContracts(tree), called: map[string]string{}}
	for name, contract := range found.contracts {
		if len(contract.methods) > 0 && !contract.composite {
			found.reporting = append(found.reporting, name)
		}
	}
	slices.Sort(found.reporting)

	for _, file := range tree.files {
		if excludedCaller(file.path) {
			continue
		}
		aliases := providerAliases(file)
		if len(aliases) == 0 {
			continue
		}
		for _, unit := range callerUnits(file.tree) {
			named, calls := readCallerUnit(unit, aliases)
			for name, contract := range found.contracts {
				if _, seen := found.called[name]; seen {
					continue
				}
				if contract.calledIn(name, named, calls) {
					found.called[name] = file.path
				}
			}
		}
	}

	return found
}

// readProviderContracts returns the exported interfaces of core/provider by
// name, Provider aside, each with its own methods.
func readProviderContracts(tree *sourceTree) map[string]providerContract {
	declared := map[string]bool{}
	var specs []*ast.TypeSpec
	for _, file := range tree.files {
		if file.importPath != providerImport {
			continue
		}
		for _, decl := range file.tree.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || !typeSpec.Name.IsExported() {
					continue
				}
				declared[typeSpec.Name.Name] = true
				specs = append(specs, typeSpec)
			}
		}
	}

	contracts := map[string]providerContract{}
	embeds := map[string][]string{}
	for _, spec := range specs {
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok || spec.Name.Name == "Provider" {
			continue
		}
		contract := providerContract{methods: map[string][]string{}}
		for _, field := range iface.Methods.List {
			fn, ok := field.Type.(*ast.FuncType)
			if !ok {
				if embedded, ok := field.Type.(*ast.Ident); ok {
					embeds[spec.Name.Name] = append(embeds[spec.Name.Name], embedded.Name)
				}

				continue
			}
			var spelled []string
			ast.Inspect(fn, func(node ast.Node) bool {
				if ident, ok := node.(*ast.Ident); ok && declared[ident.Name] {
					spelled = append(spelled, ident.Name)
				}

				return true
			})
			for _, name := range field.Names {
				if !slices.Contains(notAReport, name.Name) {
					contract.methods[name.Name] = spelled
				}
			}
		}
		contracts[spec.Name.Name] = contract
	}

	inherited := map[string]map[string][]string{}
	for name, contract := range contracts {
		if len(contract.methods) == 0 {
			inherited[name] = embeddedMethods(contracts, embeds, name, map[string]bool{})
		}
	}
	for name, methods := range inherited {
		contracts[name] = providerContract{methods: methods, composite: true}
	}

	return contracts
}

// embeddedMethods returns the methods a contract that declares none has through
// the contracts it embeds, each of which counts its own methods, or those it
// embeds in turn when it declares none.
func embeddedMethods(
	contracts map[string]providerContract, embeds map[string][]string, name string, seen map[string]bool,
) map[string][]string {
	methods := map[string][]string{}
	seen[name] = true
	for _, embedded := range embeds[name] {
		contract, ok := contracts[embedded]
		if !ok || seen[embedded] {
			continue
		}
		own := contract.methods
		if len(own) == 0 {
			own = embeddedMethods(contracts, embeds, embedded, seen)
		}
		maps.Copy(methods, own)
	}

	return methods
}

// calledIn reports whether a unit that names the core/provider types in named
// and calls the selectors in calls is a caller of the contract.
func (c providerContract) calledIn(name string, named, calls map[string]bool) bool {
	for method, spelled := range c.methods {
		if !calls[method] {
			continue
		}
		if named[name] || slices.ContainsFunc(spelled, func(s string) bool { return named[s] }) {
			return true
		}
	}

	return false
}

// excludedCaller reports whether a repo-relative path sits in a [notACaller]
// tree.
func excludedCaller(path string) bool {
	for prefix := range notACaller {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// providerAliases returns the local names under which the file imports
// core/provider.
func providerAliases(file *sourceFile) map[string]bool {
	aliases := map[string]bool{}
	for local, imported := range file.imports {
		if imported == providerImport {
			aliases[local] = true
		}
	}

	return aliases
}

// callerUnits returns the file's functions: every FuncDecl, signature and
// nested literals included, and every function literal at package level.
func callerUnits(file *ast.File) []ast.Node {
	var units []ast.Node
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			units = append(units, fn)

			continue
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if literal, ok := node.(*ast.FuncLit); ok {
				units = append(units, literal)

				return false
			}

			return true
		})
	}

	return units
}

// readCallerUnit returns the core/provider names the unit spells through one
// of the aliases, and the selector names it calls.
func readCallerUnit(unit ast.Node, aliases map[string]bool) (named, calls map[string]bool) {
	named, calls = map[string]bool{}, map[string]bool{}
	ast.Inspect(unit, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if ident, ok := n.X.(*ast.Ident); ok && aliases[ident.Name] {
				named[n.Sel.Name] = true
			}
		case *ast.CallExpr:
			if selector, ok := n.Fun.(*ast.SelectorExpr); ok {
				calls[selector.Sel.Name] = true
			}
		}

		return true
	})

	return named, calls
}

// TestTheProviderCallerGateReadsItsShapes pins the gate's reading on fixtures,
// one shape each, so that no part of the reader is held only by today's tree.
func TestTheProviderCallerGateReadsItsShapes(t *testing.T) {
	t.Parallel()

	const contracts = `package provider

import "context"

type Provider interface{ ID() string }

type AlphaInput struct{}

type Alpha interface {
	Provider
	Alpha(ctx context.Context, in AlphaInput) error
}

type Beta interface {
	Provider
	Beta(ctx context.Context) error
}

type Both interface {
	Alpha
	Beta
}

type Inert interface{ Provider }
`
	found := readProviderCallers(providerFixture(t, map[string]string{
		"core/provider/contracts.go": contracts,
		"internal/modules/one/service/literal.go": `package service

import (
	"context"

	"github.com/bdrtr/gobit/core/provider"
)

var callBeta = func(ctx context.Context, b provider.Beta) error { return b.Beta(ctx) }
`,
		"internal/modules/two/service/foreign.go": `package service

import (
	"context"

	other "example.com/elsewhere"
	"github.com/bdrtr/gobit/core/provider"
)

var _ provider.Inert

func callAlpha(ctx context.Context, a other.Alpha) error { return a.Alpha(ctx, other.AlphaInput{}) }
`,
		"internal/modules/three/service/both.go": `package service

import (
	"context"

	prov "github.com/bdrtr/gobit/core/provider"
)

func callBoth(ctx context.Context, b prov.Both) error { return b.Beta(ctx) }
`,
	}))

	require.Equal(t, []string{"Alpha", "Beta"}, found.reporting,
		"a contract reports through the methods it declares; Both and Inert declare none")
	require.Contains(t, found.called, "Beta",
		"a function literal at package level is a caller unit of its own")
	require.Equal(t, "internal/modules/three/service/both.go", found.called["Both"],
		"a contract that declares no method has those of the contracts it embeds")
	require.NotContains(t, found.called, "Alpha",
		"foreign.go calls Alpha and spells Alpha and AlphaInput, but through another "+
			"package's selector, which names nothing in core/provider")
	require.Empty(t, found.contracts["Inert"].methods,
		"a contract that embeds only Provider has nothing to call")
}

// providerFixture parses the files, by repo-relative path, into a tree the
// gate's reader takes.
func providerFixture(t *testing.T, files map[string]string) *sourceTree {
	t.Helper()

	tree := &sourceTree{fset: token.NewFileSet(), packageName: map[string]string{}}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		parsed, err := parser.ParseFile(tree.fset, path, files[path], parser.SkipObjectResolution)
		require.NoError(t, err, path)
		file := &sourceFile{
			path:       path,
			importPath: modulePath + "/" + pathpkg.Dir(path),
			tree:       parsed,
			imports:    map[string]string{},
		}
		tree.collectImports(file)
		tree.files = append(tree.files, file)
	}

	return tree
}
