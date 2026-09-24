package arch_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pricing module's history is only as true as its least careful writer
// (ADR 0167).
//
// A price at a past moment is rebuilt from the snapshots the module took after
// every write. A write that changed a price, a rule, a set or a list and took no
// snapshot would leave the history wrong from that moment on — and the
// reduction a storefront announces, the lowest price of the thirty days before
// it, would be computed from a catalog that never existed. Nothing at run time
// would say so: the answer would be a number, just the wrong one.

// pricingQueriesDir holds the module's SQL, from which the population is read.
const pricingQueriesDir = "internal/modules/pricing/queries"

// pricingRepositoryPkg is where every writing query is called from.
const pricingRepositoryPkg = "github.com/bdrtr/gobit/internal/modules/pricing/repository"

// pricingLadderTables are the tables whose rows the ladder reads.
var pricingLadderTables = []string{"price", "price_rule", "price_set", "price_list"}

// pricingHistoryTables are the snapshots.
var pricingHistoryTables = []string{"price_set_history", "price_list_history"}

// pricingHistoryRecorders are the functions that take a snapshot.
var pricingHistoryRecorders = []string{"recordSetHistory", "recordListHistory", "recordSetOfPrice"}

// pricingWritingQueryFloor is the smallest population that could be the real
// one: the module writes its four tables through at least this many queries.
// A run that finds fewer has a blind parser rather than a smaller module.
const pricingWritingQueryFloor = 10

// sqlcQueryHeader matches a named sqlc query and captures the name.
var sqlcQueryHeader = regexp.MustCompile(`(?m)^-- name: (\w+) :\w+$`)

// writingStatement matches the first statement of a query that writes, and
// captures the table.
var writingStatement = regexp.MustCompile(`(?is)^\s*(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+([a-z_]+)`)

// sqlcQueries reads every named query of a directory: name to statement.
func sqlcQueries(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
	require.NoError(t, err)

	out := map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot, dir, entry.Name()))
		require.NoError(t, err)

		text := string(raw)
		headers := sqlcQueryHeader.FindAllStringSubmatchIndex(text, -1)
		for i, header := range headers {
			end := len(text)
			if i+1 < len(headers) {
				end = headers[i+1][0]
			}
			out[text[header[2]:header[3]]] = stripSQLComments(text[header[1]:end])
		}
	}

	return out
}

// TestEveryPriceWriteLeavesAHistorySnapshot holds the pairing in the source.
//
// # The population is read from the SQL
//
// Every sqlc query in the module whose statement writes one of the four tables
// the ladder reads, found by parsing the query files rather than listed: a list
// is a record of what somebody remembered, and the writer added tomorrow is the
// one that would be missing from it.
//
// # What counts as recording
//
// A query's caller in the repository takes a snapshot itself, or is a helper
// every caller of which does — insertPrices writes prices for two doors, and
// both record. One level of helper and no more: a deeper chain would be a
// structure this audit could not see through, and it fails rather than guesses.
//
// What it cannot see is a snapshot taken OUTSIDE the transaction of the write.
// The recorders take the transaction's own query handle, which is what keeps
// them inside it; the integration test proves each write on a real server.
func TestEveryPriceWriteLeavesAHistorySnapshot(t *testing.T) {
	t.Parallel()

	var writing []string
	for name, statement := range sqlcQueries(t, pricingQueriesDir) {
		match := writingStatement.FindStringSubmatch(statement)
		if len(match) > 1 && slices.Contains(pricingLadderTables, strings.ToLower(match[1])) {
			writing = append(writing, name)
		}
	}
	sort.Strings(writing)
	require.GreaterOrEqualf(t, len(writing), pricingWritingQueryFloor,
		"only %d writing queries were found in %s; the parser has gone blind: %v",
		len(writing), pricingQueriesDir, writing)

	tree := scanProductionSource(t)
	recordsSnapshot := func(fn *ast.FuncDecl) bool {
		found := false
		ast.Inspect(fn, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && slices.Contains(pricingHistoryRecorders, ident.Name) {
				found = true
			}

			return !found
		})

		return found
	}
	repositoryCallers := func(name string) []callSite {
		var sites []callSite
		for _, site := range tree.calls[name] {
			if site.fn != nil && site.file.importPath == pricingRepositoryPkg {
				sites = append(sites, site)
			}
		}

		return sites
	}

	for _, query := range writing {
		sites := repositoryCallers(query)
		require.NotEmptyf(t, sites,
			"%s writes a table the price ladder reads and nothing in the repository calls it; "+
				"either it is dead or the scan no longer reaches the repository", query)

		for _, site := range sites {
			door := site.fn
			if recordsSnapshot(door) {
				continue
			}

			callers := repositoryCallers(door.Name.Name)
			assert.NotEmptyf(t, callers,
				"%s calls %s and takes no snapshot of what it changed, and it is not a helper "+
					"of a function that does (ADR 0167).\n"+
					"Call recordSetHistory or recordListHistory in the same transaction, after "+
					"the write: the price history is rebuilt from those snapshots, and a write "+
					"without one makes every reduction announced after it wrong.",
				tree.location(site.file, site.call.Pos()), query)
			for _, caller := range callers {
				assert.Truef(t, recordsSnapshot(caller.fn),
					"%s reaches %s through %s and takes no snapshot of what it changed (ADR 0167)",
					tree.location(caller.file, caller.call.Pos()), query, door.Name.Name)
			}
		}
	}
}

// TestThePriceHistoryIsAppendOnlyInSQL refuses a statement that rewrites a
// snapshot.
//
// A snapshot is a fact about a moment that has passed; an UPDATE would make the
// price a shop charged last month something somebody can correct, and a DELETE
// would make the reference of a reduction something somebody can remove. The
// population is every query file of the module, and the floor is that both
// tables are written by something — a scan that finds neither approves
// everything.
func TestThePriceHistoryIsAppendOnlyInSQL(t *testing.T) {
	t.Parallel()

	inserted := map[string]bool{}
	for name, statement := range sqlcQueries(t, pricingQueriesDir) {
		match := writingStatement.FindStringSubmatch(statement)
		if match == nil {
			continue
		}
		table := strings.ToLower(match[1])
		if !slices.Contains(pricingHistoryTables, table) {
			continue
		}

		keyword := strings.ToUpper(strings.Fields(strings.TrimSpace(statement))[0])
		assert.Equalf(t, "INSERT", keyword,
			"%s is a %s against %s; the price history is appended to and never rewritten "+
				"(ADR 0167)", name, keyword, table)
		inserted[table] = true
	}

	for _, table := range pricingHistoryTables {
		require.Truef(t, inserted[table],
			"no query inserts into %s; the scan read nothing, or the history stopped being written",
			table)
	}
}
