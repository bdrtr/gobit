package arch_test

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
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

// TestChannelDerivationMeansTheSameOnBothSurfaces characterizes the three states
// the derivation separates, and checks both surfaces still answer with them.
//
// # What it used to be, and why it is kept
//
// ~~The two surfaces live in two separate packages and CANNOT import each other,
// so the derivation is written twice and this test forms the link the compiler
// cannot.~~ Since 2026-09-08 the derivation lives ONCE, in
// [corehttp.SalesChannelIDs], and both functions below delegate to it — so the
// equality this asserts is now true by construction and cannot fail on its own.
//
// It is kept for the half that is not structural: the TABLE. The three states
// and the boundaries between them are the rule, and a table naming all six
// identity shapes is the only place they are written as behavior rather than as
// prose. What guards the collapse is [TestTheChannelDerivationIsNotCopied],
// which refuses a fourth copy anywhere in the tree.
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

// channelScopeTrees are the trees both channel gates walk: everything that can
// see an identity except core/http, which is where both acts are allowed to
// live.
var channelScopeTrees = []string{
	modulesDir, "plugins", "internal/workflows", "internal/app", "internal/adminui",
}

// channelDerivationExemption is a function that reads the principal's channels
// WITHOUT deriving a scope from them.
type channelDerivationExemption struct {
	// file is the path relative to the repository root.
	file string
	// function is the name of the function (or method) doing the read.
	function string
	// why is the justification; an exemption without one is the rule quietly
	// eroding.
	why string
}

// channelDerivationExemptions are the functions allowed to touch
// Principal.SalesChannelIDs outside core/http.
//
// The list is not a list of covered call sites but of GRANTED EXCEPTIONS: the
// scan walks the whole tree, and any new function that reads the field after
// asking for the principal has to be either a delegation or written here.
var channelDerivationExemptions = []channelDerivationExemption{
	{
		file:     "internal/modules/auth/api/admin.go",
		function: "adminWhoami",
		why: "it ECHOES the principal to its owner rather than deriving a scope from it: " +
			"the field is copied into the response verbatim, so the nil / empty-set " +
			"distinction is passed through rather than collapsed, and no catalog is " +
			"filtered by the result. A caller asking who it is should be told what its " +
			"key actually holds, including that it holds nothing.",
	},
}

// TestTheChannelDerivationIsNotCopied refuses a fourth copy of the derivation.
//
// # Why this replaces a comparison
//
// Until 2026-09-08 the derivation was written three times — product's GraphQL
// layer, the cart workflow and the search plugin — because the three cannot
// import one another, and [TestChannelDerivationMeansTheSameOnBothSurfaces]
// held two of them together by comparing their ANSWERS. That is the weaker
// guarantee, and it had the weakness such tests always have: it could only see
// the copies somebody had told it about. The third copy, the plugin's, was
// never in it.
//
// The derivation now lives once, in corehttp.SalesChannelIDs, and the two
// in-tree functions delegate to it. A comparison of two delegations cannot
// fail, so what guards the collapse has to be structural: nothing outside
// core/http may ask for the principal and read its channels, because doing both
// in one body IS the derivation.
//
// # What it cannot see
//
// A copy that reads the field from a Principal it was handed rather than one it
// asked the context for. That shape does not exist in the tree and would be an
// odd way to write it, but it is the hole, and it is written down rather than
// left for somebody to find.
func TestTheChannelDerivationIsNotCopied(t *testing.T) {
	t.Parallel()

	used := make([]bool, len(channelDerivationExemptions))
	scanned := 0

	for _, tree := range channelScopeTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			scanned += checkChannelDerivation(t, file, used)
		}
	}

	require.Positive(t, scanned,
		"no principal read was found at all; the scan may no longer be checking "+
			"anything (PrincipalFromContext or the field name may have been renamed)")

	for i, exemption := range channelDerivationExemptions {
		assert.True(t, used[i],
			"an unused exemption: %q in %s no longer reads the principal's channels.\n"+
				"Its justification (%q) is not defending anything: either the read was "+
				"removed and the exemption has to go with it, or it moved and the "+
				"exemption no longer sees it.",
			exemption.function, exemption.file, exemption.why)
	}
}

// checkChannelDerivation checks one file and returns how many principal reads
// it found.
func checkChannelDerivation(t *testing.T, file string, used []bool) int {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	require.NoError(t, err, "%s could not be parsed", file)

	rel := strings.TrimPrefix(file, repoRoot+"/")
	found := 0

	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !asksForThePrincipal(fn) || !readsTheChannelField(fn) {
			continue
		}

		found++
		if markChannelExemption(rel, fn.Name.Name, used) {
			continue
		}

		assert.Fail(t, "the sales channel derivation is written a second time",
			"%s.%s asks the context for the principal AND reads its SalesChannelIDs.\n"+
				"That pair IS the derivation, and it lives once, in "+
				"corehttp.SalesChannelIDs. A copy compiles and agrees with the original "+
				"on the day it is written; what it does not do is stay agreeing, and the "+
				"disagreement is only visible under an identity that holds no channel — "+
				"the one case where the two answers differ and the wrong one hands over "+
				"every channel's catalog.",
			rel, fn.Name.Name)
	}

	return found
}

// asksForThePrincipal reports whether the function calls PrincipalFromContext.
func asksForThePrincipal(fn *ast.FuncDecl) bool {
	asks := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok &&
			selector.Sel.Name == "PrincipalFromContext" {
			asks = true
		}

		return true
	})

	return asks
}

// readsTheChannelField reports whether the function READS a SalesChannelIDs
// field off something.
//
// An assignment TARGET is not a read, and the difference is the rule rather than
// a softening of it. Deriving a scope means turning an identity's channels into
// a filter, which cannot be done without reading them. Writing the field is the
// opposite act — asserting a scope somebody named — and it is guarded by
// [TestOnlyAGrantedSurfaceAssertsAChannel] instead, because a list of who may
// DECIDE a scope is a different list from a list of who may READ one.
//
// A body that does both still trips the derivation gate: the read is still
// there, and it is still the copy.
func readsTheChannelField(fn *ast.FuncDecl) bool {
	written := assignedChannelFields(fn)

	reads := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SalesChannelIDs" || written[selector] {
			return true
		}

		reads = true

		return true
	})

	return reads
}

// assignedChannelFields collects the channel selectors the function WRITES.
//
// The set is keyed by node rather than by name on purpose: in
// `p.SalesChannelIDs = append(p.SalesChannelIDs, id)` the two selectors are
// different nodes, so the right-hand one is still counted as the read it is.
func assignedChannelFields(fn *ast.FuncDecl) map[*ast.SelectorExpr]bool {
	written := map[*ast.SelectorExpr]bool{}

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}

		for _, target := range assign.Lhs {
			if selector, ok := target.(*ast.SelectorExpr); ok &&
				selector.Sel.Name == "SalesChannelIDs" {
				written[selector] = true
			}
		}

		return true
	})

	return written
}

// channelAssertionGrant is a function allowed to put an identity into the
// request with a channel scope IT chose.
type channelAssertionGrant struct {
	// file is the path relative to the repository root.
	file string
	// function is the name of the function (or method) making the claim.
	function string
	// why is the justification; a grant without one is the rule quietly eroding.
	why string
}

// channelAssertionGrants are the functions allowed to name an identity's
// channels outside core/http.
var channelAssertionGrants = []channelAssertionGrant{
	{
		file:     "internal/modules/cart/api/admin_write.go",
		function: "channelScoped",
		why: "an administrator taking an order over the telephone DECLARES which " +
			"shopfront the sale belongs to, which a multi-channel shop has to be able " +
			"to say (ADR 0146). The claim is written into the principal so that the " +
			"cart's scope rule runs unchanged rather than being skipped, the rest of " +
			"the identity is carried over untouched so the audit row still names the " +
			"administrator, and a request that makes no claim is refused with 422 " +
			"rather than left with the empty scope that answers 404 for every product " +
			"the shop has assigned to a channel.",
	},
}

// TestOnlyAGrantedSurfaceAssertsAChannel refuses a second place that decides
// what channels an identity holds.
//
// # Why this is a separate gate from the derivation
//
// [TestTheChannelDerivationIsNotCopied] guards the READ: turning an identity's
// channels into a catalog filter has to happen in one place, or the copies drift
// apart under the identity that holds none. Until ADR 0146 nothing outside
// core/http wrote the field either, so one gate covering both acts cost nothing.
//
// It stopped being free the day an administrator was given a surface that names
// the channel: the read gate would have refused the write as a fourth copy of a
// derivation it is not. Splitting them keeps both answers exact — the derivation
// still lives once, and the far rarer act of OVERWRITING a proven identity's
// scope is now written down with the reason it is allowed.
//
// # What it cannot see
//
// Minting a principal from a key's record (auth/service/interop.go) writes the
// field but never calls WithPrincipal — the core middleware does that — so this
// scan does not reach it, deliberately. Answering "what does this key hold" is
// the auth module's own job, tested there. What this gate refuses is a SECOND
// answer replacing the first one mid-request.
func TestOnlyAGrantedSurfaceAssertsAChannel(t *testing.T) {
	t.Parallel()

	used := make([]bool, len(channelAssertionGrants))
	scanned := 0

	for _, tree := range channelScopeTrees {
		for _, file := range productionFiles(t, filepath.Join(repoRoot, tree)) {
			scanned += checkChannelAssertion(t, file, used)
		}
	}

	require.Positive(t, scanned,
		"no channel assertion was found at all; the scan may no longer be checking "+
			"anything (WithPrincipal or the field name may have been renamed)")

	for i, grant := range channelAssertionGrants {
		assert.True(t, used[i],
			"an unused grant: %q in %s no longer asserts a channel.\n"+
				"Its justification (%q) is not defending anything: either the claim was "+
				"removed and the grant has to go with it, or it moved and the grant no "+
				"longer sees it.",
			grant.function, grant.file, grant.why)
	}
}

// checkChannelAssertion checks one file and returns how many assertions it
// found.
func checkChannelAssertion(t *testing.T, file string, used []bool) int {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	require.NoError(t, err, "%s could not be parsed", file)

	rel := strings.TrimPrefix(file, repoRoot+"/")
	found := 0

	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !putsThePrincipalBack(fn) || !namesTheChannels(fn) {
			continue
		}

		found++
		if markChannelGrant(rel, fn.Name.Name, used) {
			continue
		}

		assert.Fail(t, "a second surface decides an identity's channels",
			"%s.%s puts a principal into the context AND names its SalesChannelIDs.\n"+
				"That pair is a scope the SERVER did not prove: everything downstream — "+
				"the catalog read, the price list, the stock location — trusts the field "+
				"as if the guard ring had put it there. One such surface exists on "+
				"purpose (ADR 0146) and it is written in channelAssertionGrants with the "+
				"reason; a second one needs its own reason, or the identity a request "+
				"carries stops meaning anything.",
			rel, fn.Name.Name)
	}

	return found
}

// putsThePrincipalBack reports whether the function calls WithPrincipal.
func putsThePrincipalBack(fn *ast.FuncDecl) bool {
	puts := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok &&
			selector.Sel.Name == "WithPrincipal" {
			puts = true
		}

		return true
	})

	return puts
}

// namesTheChannels reports whether the function writes a SalesChannelIDs field,
// either by assigning it or by building a value that carries it.
func namesTheChannels(fn *ast.FuncDecl) bool {
	if len(assignedChannelFields(fn)) > 0 {
		return true
	}

	names := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		pair, ok := node.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "SalesChannelIDs" {
			names = true
		}

		return true
	})

	return names
}

// markChannelGrant marks the grant covering the function, if there is one.
func markChannelGrant(file, function string, used []bool) bool {
	for i, grant := range channelAssertionGrants {
		if grant.file == file && grant.function == function {
			used[i] = true

			return true
		}
	}

	return false
}

// markChannelExemption marks the exemption covering the function, if there is
// one.
func markChannelExemption(file, function string, used []bool) bool {
	for i, exemption := range channelDerivationExemptions {
		if exemption.file == file && exemption.function == function {
			used[i] = true

			return true
		}
	}

	return false
}
