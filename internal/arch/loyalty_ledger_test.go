package arch_test

import (
	"go/ast"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ONE rule for the payment module's two ledgers: EVERY ROW A
// CUSTOMER'S BALANCE IS MADE OF ENTERED THROUGH A NAMED DOOR.
//
// It is ADR 0164's load-bearing half, widened by ADR 0165, and it is
// [stockLedgerChokePoints]'s instrument pointed at two ledgers. A point ledger
// row is either the DIFFERENCE between what a payment collection should have
// earned and what it has already been written — computed inside the
// collection's own lock by writeCollectionTotals, the one function every
// change to a collection's captured or refunded total goes through — or one
// step of a spending session's state machine, written by the tender that spends
// points. The credit ledger has the same two doors: the service's issue and
// the tender that spends credit.
//
// A third writer breaks all of it at once. A flow that appended its own earn
// row would be adding to a number the rest of the module treats as derived: the
// next capture would compute a target, find more points written than it
// expected, and append a NEGATIVE row to correct a balance nobody corrupted.
// Nothing goes red when that happens — an extra row is not an error anywhere —
// and what an operator eventually sees is a history that contradicts itself.
//
// # The second door is DERIVED, not listed
//
// The tender is admitted by its PACKAGE, and the package is found by the one
// mechanically checkable identity a provider has: the string its ID() method
// returns. Naming the tender's four verbs instead would be an exemption list,
// and a wrong one — the manual and PayTR providers have methods of those names
// too. A tender that is renamed or removed turns this audit red rather than
// opening the door to nobody.
//
// What this leaves outside, said rather than implied: raw SQL against the
// table, a migration, and anything reaching the database without going through
// the store. This gate reads a call graph, so it sees callers and not statements
// or KINDS: the schema's sign check bounds the sign and the earn target's query
// sums only the earning kinds, and neither is this gate's business.
// [TestModuleSQLNamesOnlyItsOwnTables] is what keeps another component's SQL
// off the tables.

// ledgerDoor is the way (or ways) into one function of a ledger's write chain.
type ledgerDoor struct {
	// onlyFrom is the ONE function allowed to call it.
	onlyFrom string
	// orTheTender is the identity of the provider whose package is ALSO allowed
	// to call it; empty for a link with a single door.
	orTheTender string
}

// paymentLedgerChokePoints maps a function that must not be called freely to
// its door(s), for both ledgers.
//
// Three entries for the point ledger and not one, because the payment module's
// repository does NOT name its methods after its queries the way inventory's
// does. The chain runs generated query -> repository method -> service earn
// path -> the totals writer, and an entry that named only its far end would
// leave the near ones open: a new repository method calling InsertLoyaltyEntry
// reaches the table without ever touching the name the gate was watching.
//
// The credit ledger's chain is two entries: its service door (IssueCredit) is
// the operator's act and has callers of its own, so the chain ends there.
var paymentLedgerChokePoints = map[string]ledgerDoor{
	"InsertLoyaltyEntry":     {onlyFrom: "AppendLoyaltyEntry"},
	"AppendLoyaltyEntry":     {onlyFrom: "earnLoyaltyPoints", orTheTender: "loyalty_points"},
	"earnLoyaltyPoints":      {onlyFrom: "writeCollectionTotals"},
	"InsertStoreCreditEntry": {onlyFrom: "AppendStoreCreditEntry"},
	"AppendStoreCreditEntry": {onlyFrom: "IssueCredit", orTheTender: "store_credit"},
}

// TestEveryPaymentLedgerWriteEntersThroughANamedDoor refuses a third way to
// write either ledger.
//
// # Why a call graph and not a directory scan
//
// [TestEveryPhysicalStockWriteGoesThroughTheLedger]'s reason: the population is
// every call in the production tree, because the interesting failure is a caller
// appearing somewhere new, and an audit scoped to the payment module would be
// checking that the module obeys itself.
//
// # The blindness floor
//
// Failing loudly rather than passing quietly: every door has to exist as a
// function in the source, so a rename turns this audit red instead of
// switching it off; every tender named has to be found as exactly one package;
// and every door has to be SEEN OPENING — the function it names calling the
// entry, and the tender it names calling it from its package. A scan that finds
// no call approves everything, and a door nobody walks through is an inert
// mechanism: the writer it names has moved to a path this audit does not see,
// or stopped writing and the door should go. Counting consumers of the entry
// was not enough once an entry had two doors, because the tender's calls
// satisfied the count while the named function had stopped calling at all.
func TestEveryPaymentLedgerWriteEntersThroughANamedDoor(t *testing.T) {
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

	called := make([]string, 0, len(paymentLedgerChokePoints))
	for name := range paymentLedgerChokePoints {
		called = append(called, name)
	}
	sort.Strings(called)

	for _, name := range called {
		door := paymentLedgerChokePoints[name]

		require.True(t, declared[door.onlyFrom],
			"%q is named as the function allowed to reach %s, and no function of that "+
				"name exists in the production source.\n"+
				"Either it was renamed — then rename it here too — or it is gone, and this "+
				"audit is guarding a door that is no longer in the wall (ADR 0164, 0165).",
			door.onlyFrom, name)

		tenderPkg := ""
		if door.orTheTender != "" {
			tenderPkg = tenderPackage(t, tree, door.orTheTender)
		}

		fromDoor, fromTender := 0, 0

		for _, site := range tree.calls[name] {
			if site.fn == nil || site.fn.Name.Name == name {
				// A method forwarding to the thing of the same name; the
				// implementation, not a consumer.
				continue
			}

			if tenderPkg != "" && site.file.importPath == tenderPkg {
				fromTender++

				continue
			}
			if site.fn.Name.Name == door.onlyFrom {
				fromDoor++
			}

			assert.Equal(t, door.onlyFrom, site.fn.Name.Name,
				"%s calls %s from %s, and the only function allowed to is %s%s.\n"+
					"A ledger row is either the DIFFERENCE between what a payment collection "+
					"should have earned and what it has already been written, computed "+
					"inside that collection's lock by writeCollectionTotals (ADR 0164), or a "+
					"step of a spending session written by the tender that spends the "+
					"balance (ADR 0165). A third writer adds to a number the rest of the "+
					"module derives: the next capture finds more points than its target and "+
					"appends a negative row to correct a balance nobody corrupted.\n"+
					"Nothing else in this repository will report it — an extra row is not "+
					"an error — so route the write through a door, or make the case for a "+
					"third one in a record that amends ADR 0165.",
				tree.location(site.file, site.call.Pos()), name, site.fn.Name.Name,
				door.onlyFrom, tenderDoorSentence(door.orTheTender, tenderPkg))
		}

		require.Positive(t, fromDoor,
			"%s is the door to %s and no call from it was found in the production source.\n"+
				"Either the scan no longer reaches the payment module, and this audit read "+
				"nothing and approved everything, or the door stopped opening: the writer it "+
				"names reaches the ledger some other way, which this audit would then not see, "+
				"or it no longer writes and the door should go.", door.onlyFrom, name)

		if tenderPkg != "" {
			require.Positive(t, fromTender,
				"the door to %s admits the %q tender (%s) and that package never calls it.\n"+
					"A door held open for a writer that does not write is an inert mechanism: "+
					"either the tender reaches the ledger some other way, which this audit would "+
					"then not see, or it no longer spends this ledger and the door should go.",
				name, door.orTheTender, tenderPkg)
		}
	}
}

// tenderPackage returns the import path of the ONE package whose ID() method
// returns the given provider identity.
//
// The identity is resolved through the scanner's constant table, so a tender
// whose ID is a constant — or a constant of another package, as the points
// tender's is — is found by what it returns rather than by how it spells it.
func tenderPackage(t *testing.T, tree *sourceTree, id string) string {
	t.Helper()

	var packages []string

	for _, file := range tree.files {
		for _, decl := range file.tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "ID" || !returnsOneString(fn) {
				continue
			}

			for _, returned := range returnedExpressions(fn) {
				for _, value := range tree.stringValues(file, fn, returned, 0) {
					if value == id && !containsString(packages, file.importPath) {
						packages = append(packages, file.importPath)
					}
				}
			}
		}
	}

	require.Len(t, packages, 1,
		"the %q tender was looked for as the package whose ID() returns it, and %d were "+
			"found: %v.\n"+
			"Zero means the tender is gone or renamed and the door that admits it would "+
			"be held open for nobody; two means the identity is declared twice, which the "+
			"provider registry would refuse at boot. Either way the door cannot be derived.",
		id, len(packages), packages)

	return packages[0]
}

// returnedExpressions collects the expressions of every return statement in the
// function body.
func returnedExpressions(fn *ast.FuncDecl) []ast.Expr {
	var out []ast.Expr

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ret, ok := n.(*ast.ReturnStmt); ok {
			out = append(out, ret.Results...)
		}

		return true
	})

	return out
}

// tenderDoorSentence names the second door in a failure message, when there is
// one.
func tenderDoorSentence(id, pkg string) string {
	if id == "" {
		return ""
	}

	return " — or the " + id + " tender's package, " + pkg
}

// containsString reports whether the slice holds the value.
func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}

	return false
}

// paymentLedgerTables are the payment module's append-only ledgers.
//
// Both are a balance held as the sum of its rows, and both say so in their own
// migrations. The list is what makes the audit below cover the tables rather
// than one file: the rule that made it necessary — a correction is a new row —
// is the module's own since its migration 000003.
var paymentLedgerTables = []string{"payment_store_credit_entries", "payment_loyalty_entries"}

// TestThePaymentLedgersAreAppendOnlyInSQL refuses a statement that rewrites a
// customer's money or points.
//
// A ledger row records something that HAPPENED. An UPDATE or a DELETE would make
// it a set of rows somebody can correct, and a correctable ledger explains
// nothing — which is the whole argument for holding a balance as a sum rather
// than as a column (ADR 0152, ADR 0164).
//
// It reads the module's query files as TEXT, which is all it needs to: the only
// way to reach either table from Go is through a named query in that directory,
// and a statement that is not there cannot be called.
//
// # Why it lives here and not beside the ledger
//
// Because the rule's subject is the DIRECTORY and the copy it replaces had a
// FILE for a subject. The store credit ledger's version read one path by name,
// in a test behind the integration build tag, so the second ledger's query file
// would have been invisible to it on the day it was added — which is this day.
// Its floor was missing too: a renamed file made the assertions read an empty
// string and pass. Both are the same defect this repository keeps recording, a
// gate whose audited population is narrower than the sentence it states.
func TestThePaymentLedgersAreAppendOnlyInSQL(t *testing.T) {
	t.Parallel()

	queries := readSQL(t, filepath.Join(repoRoot, modulesDir, "payment", "queries"), ".sql")
	lowered := strings.ToLower(queries)

	for _, table := range paymentLedgerTables {
		// require.True over require.Contains for the same reason the check below
		// is a boolean: Contains prints the HAYSTACK, and the haystack here is
		// the payment module's entire query directory.
		require.True(t, strings.Contains(lowered, table),
			"no query names %s, so this audit read nothing.\n"+
				"The queries moved, or the ledger is no longer reachable from Go — and a "+
				"scan that finds no statement approves every one of them.", table)

		// A boolean rather than assert.NotContains, so a failure prints the
		// SENTENCE and not the module's whole query directory.
		for _, forbidden := range []string{"update " + table, "delete from " + table} {
			assert.False(t, strings.Contains(lowered, forbidden),
				"a query says %q.\n"+
					"The payment module's ledgers are APPEND-ONLY: a balance is the sum of "+
					"its rows, and a row that can be rewritten or removed explains nothing. "+
					"A correction is a NEW ROW — which for the points is what a reverse is, "+
					"and for the credit what an issue is.", forbidden)
		}
	}
}
