package arch_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// This file enforces that SALES CHANNEL SCOPE stays the same on both surfaces.
//
// The scope rule ("a product without an assignment is visible in every channel, one
// with an assignment only in the channels it is assigned to") is the product
// module's data and lives there in a single SQL template. The danger is not in the
// rule itself but in the paths where it is NOT APPLIED: the rule was at one time
// only written and never read, and then applied on the READ surface but not on the
// WRITE path. Both are the same class — "decided in one place, not applied in
// another" — and both lived while the tests were green.
//
// The three invariants here close three separate faces of that class: the MEANING
// of the derivation, the contract NAME, and that the read goes THROUGH THE SCOPE
// DECISION.

// TestChannelDerivationMeansTheSameOnBothSurfaces verifies that the read and the
// write surface derive the same three states from an identity.
//
// The two surfaces live in two separate packages and CANNOT import each other: the
// product module cannot see the workflows, and the workflows cannot see the modules
// (ADR 0006). That is why the derivation is written twice, and this test forms the
// link the compiler cannot.
//
// A drift would be SILENT and visible only under a particular identity shape: were
// the write path to start returning nil in the "identity without channels" case,
// say, the owner of that key could see no product at all in the storefront while
// being able to add EVERY product to the cart — and no endpoint would give an error.
func TestChannelDerivationMeansTheSameOnBothSurfaces(t *testing.T) {
	t.Parallel()

	withPrincipal := func(channels []string) context.Context {
		return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			ID:              "apk_test",
			Kind:            "api_key",
			SalesChannelIDs: channels,
		})
	}

	cases := map[string]context.Context{
		"no identity":                          context.Background(),
		"an identity without channels (nil)":   withPrincipal(nil),
		"an identity without channels (empty)": withPrincipal([]string{}),
		"a single-channel identity":            withPrincipal([]string{"sc_a"}),
		"a multi-channel identity":             withPrincipal([]string{"sc_a", "sc_b"}),
		"an identity with an empty channel":    withPrincipal([]string{""}),
	}

	for name, ctx := range cases {
		read := graph.SalesChannelIDsFromContext(ctx)
		write := cartwf.SalesChannelIDsFromContext(ctx)

		assert.Equal(t, read, write,
			"%s: the read and write surfaces have to derive the SAME channel set from the identity", name)
		// The equality assertion already separates nil from an empty slice
		// (reflect.DeepEqual), but the distinction is written out explicitly because it
		// is THE VERY CENTER of this rule: nil means "no filtering", an empty slice means
		// "an identity with no channels", and treating the two alike opens the whole
		// catalog to a key without channels.
		assert.Equal(t, read == nil, write == nil,
			"%s: the nil versus EMPTY SET distinction has to be the same on both surfaces", name)
	}
}

// TestTheChannelContractNamesAgree verifies the question the workflow asks product
// FROM ITS NAMES.
//
// The workflow cannot import product (ADR 0006) and repeats the entity name and the
// filter key it sends to the Query layer AS STRINGS. If they drift, the provider is
// either never found or does not recognize the filter; both fail with errors, that
// is, the fault is NOT silent — but it is seen in production, in a customer's cart.
// This test moves it into the team.
func TestTheChannelContractNamesAgree(t *testing.T) {
	t.Parallel()

	assert.Equal(t, productsvc.FilterSalesChannelIDs, cartwf.FilterSalesChannelIDs,
		"the channel filter key the cart workflow sends has to be the same as the key "+
			"the variant provider recognizes")
	assert.Equal(t, productsvc.EntityVariant, cartwf.EntityVariant,
		"the entity name the workflow asks for has to be the same as the name product "+
			"offers; if they drift, Query never finds the provider")
}

// variantReadExemption is a variant read that DELIBERATELY does not make a channel decision.
type variantReadExemption struct {
	// file is the path relative to the repository root.
	file string
	// function is the name of the function (or method) doing the read.
	function string
	// why is the justification of the decision; an exemption without a justification is
	// the rule quietly eroding.
	why string
}

// variantReadExemptions are the variant reads that do NOT LOOK FOR a channel decision.
//
// The list is not a list of covered paths but of GRANTED EXCEPTIONS: the scan walks
// the whole tree, and every new variant read not written here has to make the
// decision. An unused exemption is an error too (see the end of the test), that is,
// the list shrinks by itself once an exception goes away.
var variantReadExemptions = []variantReadExemption{
	{
		file:     "internal/workflows/checkout/plan.go",
		function: "variantTitles",
		why: "the scope is applied AT THE ENTRANCE: the only way a variant can get into " +
			"a cart is adding a line, and that path is covered. This read copies the name " +
			"of a line that has ALREADY entered the cart onto an order line; filtering " +
			"again would mean an administrator edit moving a product to another channel " +
			"making the customer's full cart unpayable, and would contradict the product " +
			"module's written decision (see productsvc.productProvider.List).",
	},
	{
		file:     "internal/modules/product/service/store.go",
		function: "enrichVariants",
		why: "the scope is applied at a HIGHER step: this read enriches the variants of " +
			"the storefront list's already filtered products with their price/stock " +
			"links. Filtering a second time would mean applying the same rule twice in " +
			"the same request; a product outside the scope never reaches here.",
	},
}

// channelDecidingCalls are the function names showing that a variant read makes
// the scope decision.
//
// The name is looked at, not the type: the scan is a parser, not a compiler, and a
// cross-package type resolution would bind the test to go/types and to the whole
// build graph. A false positive from the name is harmless too — the only thing that
// really makes the decision is putting the filter into the query, and the behavior
// tests prove that.
var channelDecidingCalls = map[string]bool{
	"SalesChannelIDsFromContext": true,
	"salesChannelFilter":         true,
}

// TestVariantReadsGoThroughTheChannelDecision verifies that every function
// reading a variant makes a VISIBLE decision about sales channel scope.
//
// # Why it walks the structure
//
// A hand-kept list of "covered paths" applies the rule for TODAY only: a write path
// added tomorrow is not in the list and stays quietly unscoped — and every bug in
// this repository was of exactly that class. Instead, this test walks the
// internal/workflows and internal/modules trees, finds every `variant` read going to
// Query and asks: does the function doing this read call the helper that makes the
// scope decision?
//
// # What it PROVES and what it does not
//
// The invariant is a PROXY and that must not be hidden: it enforces that the
// decision IS MADE, not that it is made CORRECTLY. A function that calls the helper
// without putting the filter into the query passes through here. The correctness of
// the decision is the job of the behavior tests and exists at three layers at once:
// the workflow's unit tests (the value put into the query), product's integration
// tests (the real SQL) and the end-to-end test (two publishable keys, the real guard
// stack).
//
// A stronger invariant — "a variant read not carrying the channel filter MUST NOT
// COMPILE" — would only be possible if the Query filters were a typed structure
// rather than a map[string]any; and that runs into the rule that the core does not
// know the modules (Principle 2.4), because the filter names are the modules'
// contract. This is therefore the strongest structural check that can be written.
func TestVariantReadsGoThroughTheChannelDecision(t *testing.T) {
	t.Parallel()

	used := make([]bool, len(variantReadExemptions))
	scanned := 0

	for _, root := range []string{"internal/workflows", modulesDir} {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, root)) {
			scanned += checkVariantReads(t, file, used)
		}
	}

	require.Positive(t, scanned,
		"no variant read was found at all; the scan may no longer be checking anything "+
			"(the GraphSpec field name or the entity constant may have changed)")

	for i, exemption := range variantReadExemptions {
		assert.True(t, used[i],
			"an unused exemption: %q in %s no longer reads variants.\n"+
				"Its justification (%q) is not defending anything: either the read was removed "+
				"and the exemption has to be deleted too, or it moved and the exemption no "+
				"longer sees it.",
			exemption.function, exemption.file, exemption.why)
	}
}

// checkVariantReads checks the variant reads in one file and returns the number of
// reads found.
func checkVariantReads(t *testing.T, file string, used []bool) int {
	t.Helper()

	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("%s could not be parsed: %v", file, err)
	}

	path := filepath.ToSlash(repoPath(file))
	bulunan := 0

	for _, decl := range tree.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !readsVariants(fn) {
			continue
		}
		bulunan++

		if exemption := markExemption(path, fn.Name.Name, used); exemption {
			continue
		}
		if makesChannelDecision(fn) {
			continue
		}

		t.Errorf("%s:%d: %q reads variants but makes no visible decision about sales "+
			"karar vermiyor.\n"+
			"The read has to carry the channels coming from the request's identity as a "+
			"filter (see workflows/cart/saleschannel.go), or why it does not has to be "+
			"written into variantReadExemptions WITH ITS JUSTIFICATION. An unscoped "+
			"variant read means another storefront's product passing through this path.",
			path, fset.Position(fn.Pos()).Line, fn.Name.Name)
	}

	return bulunan
}

// readsVariants says whether a query.GraphSpec going to the `variant` entity is
// built in the function's body.
func readsVariants(fn *ast.FuncDecl) bool {
	bulundu := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if bulundu {
			return false
		}
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isGraphSpec(lit.Type) {
			return true
		}
		if isVariantEntity(lit) {
			bulundu = true
		}

		return true
	})

	return bulundu
}

// isGraphSpec says whether a composite literal's type is query.GraphSpec.
func isGraphSpec(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "GraphSpec"
}

// isVariantEntity says whether the GraphSpec's Entity field points at variants.
//
// Both the constant name ("EntityVariant") and the plain string ("variant") are
// accepted: skipping the constant and writing the string by hand would be the
// easiest and most innocent-looking way to escape the check.
func isVariantEntity(lit *ast.CompositeLit) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		keyIdent, ok := kv.Key.(*ast.Ident)
		if !ok || keyIdent.Name != "Entity" {
			continue
		}

		switch value := kv.Value.(type) {
		case *ast.Ident:
			return value.Name == "EntityVariant"
		case *ast.SelectorExpr:
			return value.Sel.Name == "EntityVariant"
		case *ast.BasicLit:
			return value.Value == `"`+productsvc.EntityVariant+`"`
		}
	}

	return false
}

// makesChannelDecision says whether the function calls a helper that makes the
// scope decision.
func makesChannelDecision(fn *ast.FuncDecl) bool {
	bulundu := false

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if bulundu {
			return false
		}
		cagri, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch hedef := cagri.Fun.(type) {
		case *ast.Ident:
			bulundu = channelDecidingCalls[hedef.Name]
		case *ast.SelectorExpr:
			bulundu = channelDecidingCalls[hedef.Sel.Name]
		}

		return true
	})

	return bulundu
}

// markExemption says whether the read is exempt and marks the exemption as USED.
func markExemption(file, function string, used []bool) bool {
	for i, exemption := range variantReadExemptions {
		if exemption.file == file && exemption.function == function {
			used[i] = true
			return true
		}
	}

	return false
}
