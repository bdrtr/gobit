package arch_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ONE rule: EVERY LOYALTY POINT A CUSTOMER HOLDS WAS WRITTEN BY
// THE FUNCTION THAT MOVED THE MONEY, IN THE SAME TRANSACTION.
//
// It is ADR 0164's load-bearing half, and it is [stockLedgerChokePoints]'s
// instrument pointed at a second ledger. The decision makes a point row the
// DIFFERENCE between what a payment collection should have earned and what it
// has already been written, computed inside the collection's own lock by
// writeCollectionTotals — the one function every change to a collection's
// captured or refunded total goes through. That shape is what makes the write
// safe to repeat and a refund able to take points back.
//
// A second writer breaks all of it at once. A flow that appended its own row
// would be adding to a number the rest of the module treats as derived: the next
// capture would compute a target, find more points written than it expected, and
// append a NEGATIVE row to correct a balance nobody corrupted. Nothing goes red
// when that happens — an extra row is not an error anywhere — and what an
// operator eventually sees is a history that contradicts itself.
//
// What this leaves outside, said rather than implied: raw SQL against the table,
// a migration, and anything reaching the database without going through the
// store. This gate reads a call graph, so it sees callers and not statements;
// [TestModuleSQLNamesOnlyItsOwnTables] is what keeps another component's SQL off
// the table.

// loyaltyLedgerChokePoints maps a function that must not be called freely to the
// ONE function allowed to call it.
//
// Three entries and not one, because the payment module's repository does NOT
// name its methods after its queries the way inventory's does. The chain runs
// generated query -> repository method -> service earn path -> the totals
// writer, and an entry that named only its far end would leave the near ones
// open: a new repository method calling InsertLoyaltyEntry reaches the table
// without ever touching the name the gate was watching.
var loyaltyLedgerChokePoints = map[string]string{
	"InsertLoyaltyEntry": "AppendLoyaltyEntry",
	"AppendLoyaltyEntry": "earnLoyaltyPoints",
	"earnLoyaltyPoints":  "writeCollectionTotals",
}

// TestEveryLoyaltyPointWriteGoesThroughTheCollectionTotals refuses a second way
// to write the point ledger.
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
// The same two counts, failing loudly rather than passing quietly: every choke
// point has to exist as a function in the source, so a rename turns this audit
// red instead of switching it off, and at least one real consumer has to be
// found for every entry, because a scan that finds none approves everything.
func TestEveryLoyaltyPointWriteGoesThroughTheCollectionTotals(t *testing.T) {
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

	for _, called := range sortedKeys(loyaltyLedgerChokePoints) {
		chokePoint := loyaltyLedgerChokePoints[called]

		require.True(t, declared[chokePoint],
			"%q is named as the only caller allowed to reach %s, and no function of that "+
				"name exists in the production source.\n"+
				"Either it was renamed — then rename it here too — or it is gone, and this "+
				"audit is guarding a door that is no longer in the wall (ADR 0164).",
			chokePoint, called)

		consumers := 0

		for _, site := range tree.calls[called] {
			if site.fn == nil || site.fn.Name.Name == called {
				// A method forwarding to the thing of the same name; the
				// implementation, not a consumer.
				continue
			}

			consumers++

			assert.Equal(t, chokePoint, site.fn.Name.Name,
				"%s calls %s from %s, and the only function allowed to is %s.\n"+
					"A loyalty point row is the DIFFERENCE between what a payment "+
					"collection should have earned and what it has already been written, "+
					"and it is computed inside that collection's lock by %s (ADR 0164). A "+
					"second writer adds to a number the rest of the module derives: the "+
					"next capture finds more points than its target and appends a negative "+
					"row to correct a balance nobody corrupted.\n"+
					"Nothing else in this repository will report it — an extra row is not "+
					"an error — so route the write through %s, or make the case for a "+
					"second door in a record that supersedes ADR 0164.",
				tree.location(site.file, site.call.Pos()), called, site.fn.Name.Name,
				chokePoint, chokePoint, chokePoint)
		}

		require.Positive(t, consumers,
			"no call to %s was found anywhere in the production source, so this audit "+
				"read nothing and approved everything.\n"+
				"The function was renamed or removed, or the scan no longer reaches the "+
				"payment module — either way the rule is not being enforced.", called)
	}
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
