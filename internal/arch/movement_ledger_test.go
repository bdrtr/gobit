package arch_test

import (
	"go/ast"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ONE rule: EVERY CHANGE TO THE PHYSICAL STOCK COUNT IS
// EXPLAINED BY A MOVEMENT, WRITTEN IN THE SAME TRANSACTION.
//
// It is ADR 0068's load-bearing half. The decision keeps
// inventory_levels.stocked_quantity AUTHORITATIVE and lets inventory_movements
// EXPLAIN it, rather than deriving the count by summing the ledger — an
// aggregate on the availability read path, and no answer at all for the stock
// that existed before the table. That choice leaves exactly one way for the two
// to disagree: one of them being written without the other.
//
// The transaction half is held inside the module — the repository's
// AppendMovement refuses to run outside one, exactly as the Lock methods do,
// and a unit test drives that refusal. This file holds the OTHER half, which no
// test inside the package can hold honestly: that no GO CALLER in the tree
// reaches the quantity-writing store methods except the one function that also
// writes the ledger.
//
// What that leaves outside, said rather than implied: raw SQL against the
// column, a migration, and anything reaching the database without going through
// the store at all. This gate reads a call graph, so it sees callers and not
// statements — [TestModuleSQLNamesOnlyItsOwnTables] is what keeps another
// component's SQL off the table, and neither of them can speak for a hand-run
// UPDATE.

// stockLedgerChokePoints maps a store method that must not be called freely to
// the ONE function allowed to call it.
//
// The map is the rule, and each entry earns its place:
//
//   - CreateInventoryLevel and UpdateInventoryLevelQuantities are the only two
//     statements in the repository that write stocked_quantity. Everything else
//     in the module reaches the column through one of them.
//   - AppendMovement is here for the mirror reason. The choke point is what
//     refuses a movement with no delta and a delta with no reason; a caller
//     reaching past it could write a row saying nothing moved, or a sale naming
//     no reservation, and the ledger would stop being readable in exactly the
//     way this decision exists to prevent.
var stockLedgerChokePoints = map[string]string{
	"CreateInventoryLevel":           "openLevel",
	"UpdateInventoryLevelQuantities": "writeQuantities",
	"AppendMovement":                 "recordMovement",
}

// TestEveryPhysicalStockWriteGoesThroughTheLedger refuses a second way to write
// the stock count.
//
// # The defect it is written for
//
// A flow is added — a transfer between warehouses, a write-off, a supplier
// receipt — and it does what the six flows before it did: lock the level, work
// out the new number, call the store. Everything compiles, the stock is right,
// and the ledger silently stops explaining the column. Nothing goes red,
// because a missing row is not an error anywhere; it is a gap that shows up
// months later as a discrepancy nobody can trace.
//
// That is not hypothetical here. Before this decision, five of the six flows in
// the module called UpdateInventoryLevelQuantities directly, which is exactly
// the shape the sixth would have copied.
//
// # Why a call graph and not a directory scan
//
// The population is EVERY call in the production tree, from the same scan the
// other audits in this package use ([scanProductionSource]), and not the
// inventory service's own files. A rule that only looked inside one package
// would be checking that the package obeys itself: the interesting failure is a
// caller appearing somewhere new, and a directory-scoped audit is blind to
// precisely that.
//
// # Why the same-name delegation is skipped
//
// The repository declares a method per store call and forwards it to the
// generated query of the same name, so "AppendMovement calls AppendMovement" is
// the implementation rather than a consumer. Skipping it is not a hole: those
// functions are where the SQL lives, and a ledger row cannot be written from
// them without the caller that decided to write it.
//
// # The blindness floor
//
// Two counts have to hold, and both fail LOUDLY rather than passing quietly. At
// least one real consumer has to be found for every entry — a scan that finds
// none is reading the wrong tree and would approve any violation — and every
// choke point named above has to exist as a function in the source, so renaming
// one turns this audit red instead of switching it off.
func TestEveryPhysicalStockWriteGoesThroughTheLedger(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)

	declared := map[string]bool{}
	for _, file := range tree.files {
		for _, decl := range file.tree.Decls {
			if fn := funcDeclName(decl); fn != "" {
				declared[fn] = true
			}
		}
	}

	for _, called := range sortedKeys(stockLedgerChokePoints) {
		chokePoint := stockLedgerChokePoints[called]

		require.True(t, declared[chokePoint],
			"%q is named as the only caller allowed to reach %s, and no function of that "+
				"name exists in the production source.\n"+
				"Either it was renamed — then rename it here too — or it is gone, and this "+
				"audit is guarding a door that is no longer in the wall (ADR 0068).",
			chokePoint, called)

		consumers := 0

		for _, site := range tree.calls[called] {
			if site.fn == nil || site.fn.Name.Name == called {
				// The repository's own method forwarding to the generated query
				// of the same name; the implementation, not a consumer.
				continue
			}

			consumers++

			assert.Equal(t, chokePoint, site.fn.Name.Name,
				"%s calls %s from %s, and the only function allowed to is %s.\n"+
					"Every change to the physical stock count is explained by a row in "+
					"inventory_movements, written in the SAME transaction (ADR 0068). A "+
					"second way into the column is a change the ledger cannot explain, and "+
					"nothing else in this repository will report it: a missing row is not "+
					"an error, it is a discrepancy somebody meets months later.\n"+
					"Route the write through %s and give it a reason, or — if the count "+
					"genuinely does not change — pass the empty reason, which is what the "+
					"reservation flows do.",
				tree.location(site.file, site.call.Pos()), called, site.fn.Name.Name,
				chokePoint, chokePoint)
		}

		require.Positive(t, consumers,
			"no call to %s was found anywhere in the production source, so this audit "+
				"read nothing and approved everything.\n"+
				"The store method was renamed or removed, or the scan no longer reaches "+
				"the inventory module — either way the rule is not being enforced.", called)
	}
}

// funcDeclName returns the name of a function declaration, or the empty string
// for a declaration that is not one.
func funcDeclName(decl ast.Decl) string {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}

	return fn.Name.Name
}

// TestTheStockLedgerIsAppendOnlyInSQL refuses a statement that rewrites history.
//
// A movement records something that HAPPENED. An UPDATE or a DELETE against the
// table would make the ledger a set of rows somebody can correct, and a
// correctable ledger explains nothing — the reason inventory_reservations
// carries no deleted_at, one table over, is the same reason.
//
// It reads the module's query files as TEXT, which is all it needs to: the only
// way to reach the table from Go is through a named query in that directory,
// and a statement that is not there cannot be called. The floor is the same
// shape as above — the scan has to find the table at all.
func TestTheStockLedgerIsAppendOnlyInSQL(t *testing.T) {
	t.Parallel()

	const table = "inventory_movements"

	queries := readSQL(t, filepath.Join(repoRoot, modulesDir, "inventory", "queries"), ".sql")
	require.Contains(t, queries, table,
		"no query names %s, so this audit read nothing.\n"+
			"The queries moved, or the ledger is no longer reachable from Go — and a "+
			"scan that finds no statement approves every one of them.", table)

	// The check is a boolean rather than assert.NotContains, so a failure prints
	// the SENTENCE and not the module's whole query directory: a gate whose
	// message is twenty kilobytes of SQL is one whose reader scrolls past the
	// reason it fired.
	lowered := strings.ToLower(queries)
	for _, forbidden := range []string{"update " + table, "delete from " + table} {
		assert.False(t, strings.Contains(lowered, forbidden),
			"a query says %q.\n"+
				"The movement ledger is APPEND-ONLY (ADR 0068): a row records a change "+
				"that happened, and a row that can be rewritten or removed explains "+
				"nothing. A correction to the stock is a NEW movement, which is what the "+
				"adjustment reason is for.", forbidden)
	}
}
