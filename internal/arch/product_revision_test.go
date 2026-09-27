package arch_test

import (
	"go/ast"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A product's revisions are only as complete as its least careful writer
// (ADR 0221).
//
// A revision is the product's admin view after a write, and the view is
// assembled in the service; a write that reached a table of the view outside a
// revising transaction would leave the next revision naming its change as if
// the next writer had made it, and the version would count one write short.

// productQueriesDir holds the module's SQL, from which the population is read.
const productQueriesDir = "internal/modules/product/queries"

// productRepositoryPkg and productServicePkg are where the writes are called
// from.
const (
	productRepositoryPkg = "github.com/bdrtr/gobit/internal/modules/product/repository"
	productServicePkg    = "github.com/bdrtr/gobit/internal/modules/product/service"
)

// productViewTables are the tables a revision's snapshot is read from.
var productViewTables = []string{
	"product", "product_variant", "product_option", "product_option_value", "product_variant_option_value",
	"product_image", "product_tag_map", "product_category_map", "product_attribute_value",
}

// productRevisionFrames are the service functions whose function literal a
// write may sit in: revise, and applyDue for the schedule's statements.
var productRevisionFrames = []string{"revise", "applyDue"}

// productRevisionRecorder is the call a transaction that records its own
// revision makes, as creating a product does.
const productRevisionRecorder = "recordRevision"

// productWritesThatAreNotRevisions are the service functions that write a
// table of the view and are not a revision of one product, each with why.
var productWritesThatAreNotRevisions = map[string]string{
	"DeleteProduct": "a deleted product has no view left to revise",
	"DeleteCollection": "it clears the collection of every product in it; each product's next " +
		"revision shows it",
	"DeleteProductType":  "it clears the type of every product of it, as DeleteCollection does",
	"RefoldOptionValues": "value_folded is a matching form the view does not carry",
	"SetSchedule":        "a schedule is not in the view (ADR 0177)",
	"ClearSchedule":      "a schedule is not in the view (ADR 0177)",
}

// productWritingQueryFloor is the smallest population that could be the real
// one; a run that finds fewer has a blind parser.
const productWritingQueryFloor = 25

// TestEveryWriteToAProductsViewIsARevision holds ADR 0221 in the source.
//
// The population is every sqlc query writing a table of the view, read from the
// SQL; its repository callers, one level of repository helper through; and every
// place in the service that calls one of those on a store. Each such call sits
// in a function literal handed to revise or applyDue, or in one that records
// its own revision, or in a function named above with its reason — or in a
// service helper every caller of which does.
func TestEveryWriteToAProductsViewIsARevision(t *testing.T) {
	t.Parallel()

	var writing []string
	for name, statement := range sqlcQueries(t, productQueriesDir) {
		match := writingStatement.FindStringSubmatch(statement)
		if len(match) > 1 && slices.Contains(productViewTables, strings.ToLower(match[1])) {
			writing = append(writing, name)
		}
	}
	sort.Strings(writing)
	require.GreaterOrEqualf(t, len(writing), productWritingQueryFloor,
		"only %d writing queries were found in %s; the parser has gone blind: %v",
		len(writing), productQueriesDir, writing)

	tree := scanProductionSource(t)
	storeMethods := map[string]bool{}
	for _, query := range writing {
		sites := tree.sitesIn(query, productRepositoryPkg)
		require.NotEmptyf(t, sites, "%s writes the view and nothing in the repository calls it", query)
		for _, site := range sites {
			name := site.fn.Name.Name
			if ast.IsExported(name) {
				storeMethods[name] = true
				continue
			}
			for _, caller := range tree.sitesIn(name, productRepositoryPkg) {
				storeMethods[caller.fn.Name.Name] = true
			}
		}
	}

	exempted := map[string]bool{}
	for _, method := range slices.Sorted(mapKeys(storeMethods)) {
		for _, site := range tree.sitesIn(method, productServicePkg) {
			if !isStoreCall(site.call) {
				continue
			}
			if name := site.fn.Name.Name; productWritesThatAreNotRevisions[name] != "" {
				exempted[name] = true
				continue
			}
			if revised(site.fn, site.call) {
				continue
			}
			helper := site.fn.Name.Name
			callers := tree.sitesIn(helper, productServicePkg)
			if ast.IsExported(helper) || len(callers) == 0 {
				assert.Failf(t, "a write outside a revision",
					"%s calls %s outside a revising transaction (ADR 0221): put the write in the "+
						"function handed to revise, or name the function in "+
						"productWritesThatAreNotRevisions with why it is not a revision",
					tree.location(site.file, site.call.Pos()), method)
				continue
			}
			for _, caller := range callers {
				assert.Truef(t, revised(caller.fn, caller.call),
					"%s reaches %s through %s outside a revising transaction (ADR 0221)",
					tree.location(caller.file, caller.call.Pos()), method, helper)
			}
		}
	}
	for name := range productWritesThatAreNotRevisions {
		assert.Truef(t, exempted[name],
			"%s is excused from revising and writes no table of the view any more; take it off the list",
			name)
	}
}

// TestTheProductRevisionsAreAppendOnlyInSQL refuses a statement that rewrites
// a revision: the floor is that something inserts one.
func TestTheProductRevisionsAreAppendOnlyInSQL(t *testing.T) {
	t.Parallel()

	inserted := false
	for name, statement := range sqlcQueries(t, productQueriesDir) {
		match := writingStatement.FindStringSubmatch(statement)
		if len(match) < 2 || !strings.EqualFold(match[1], "product_revision") {
			continue
		}
		keyword := strings.ToUpper(strings.Fields(strings.TrimSpace(statement))[0])
		assert.Equalf(t, "INSERT", keyword,
			"%s is a %s against product_revision; a revision is appended and never rewritten (ADR 0221)",
			name, keyword)
		inserted = true
	}
	require.True(t, inserted, "no query inserts into product_revision; the scan read nothing")
}

// sitesIn returns the calls of a name made from inside a function of a package.
func (a *sourceTree) sitesIn(name, importPath string) []callSite {
	var out []callSite
	for _, site := range a.calls[name] {
		if site.fn != nil && site.file.importPath == importPath {
			out = append(out, site)
		}
	}
	return out
}

// isStoreCall reports whether a call is made on a store: tx.X, store.X or
// s.repo.X, and not on the service itself.
func isStoreCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch x := selector.X.(type) {
	case *ast.Ident:
		return x.Name == "tx" || x.Name == "store"
	case *ast.SelectorExpr:
		return x.Sel.Name == "repo"
	}
	return false
}

// revised reports whether a call sits in a function literal handed to one of
// the revision frames, or in one that records its own revision.
func revised(fn *ast.FuncDecl, target *ast.CallExpr) bool {
	found := false
	ast.Inspect(fn, func(node ast.Node) bool {
		lit, ok := node.(*ast.FuncLit)
		if !ok || found || target.Pos() < lit.Pos() || target.End() > lit.End() {
			return !found
		}
		if handedToFrame(fn, lit) || callsName(lit, productRevisionRecorder) {
			found = true
		}
		return !found
	})
	return found
}

// handedToFrame reports whether a function literal is an argument of a call to
// a revision frame.
func handedToFrame(fn *ast.FuncDecl, lit *ast.FuncLit) bool {
	handed := false
	ast.Inspect(fn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !slices.Contains(productRevisionFrames, callName(call.Fun)) {
			return !handed
		}
		for _, arg := range call.Args {
			if arg == lit {
				handed = true
			}
		}
		return !handed
	})
	return handed
}

// callsName reports whether a node calls a function of the given name.
func callsName(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && callName(call.Fun) == name {
			found = true
		}
		return !found
	})
	return found
}

// mapKeys is the keys of a set.
func mapKeys(set map[string]bool) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range set {
			if !yield(key) {
				return
			}
		}
	}
}
