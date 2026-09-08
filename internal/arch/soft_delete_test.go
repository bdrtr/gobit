package arch_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// softDeleteColumn is the column name this gate refuses.
const softDeleteColumn = "deleted_at"

// modulesWithoutSoftDelete are the modules ADR 0054 put outside soft delete.
//
// It is NOT every module. Most of them soft-delete and are right to: a product,
// a price, a customer group and a shipping option are things a merchant edits,
// and hiding one is how it stops being offered without breaking the rows that
// point at it. These two are different in kind — an order and a payment are
// RECORDS OF SOMETHING THAT HAPPENED — and that is the whole distinction the
// list carries.
var modulesWithoutSoftDelete = []string{"order", "payment"}

// TestTheOrderAndPaymentModulesDeclareNoSoftDelete keeps ADR 0054's decision.
//
// # What the ten columns cost while they stood
//
// Nothing ever wrote them. Fifty-six of the two modules' eighty-three
// statements carried "deleted_at IS NULL" — a predicate that had never once
// been false — and twenty-three indexes were partial on a column with one
// value. That much is waste and would not need a gate.
//
// What needs one is the other half. FOUR of the twenty-three were UNIQUE and
// written as "unique among LIVING rows": orders_idempotency_key_uniq,
// order_return_items_line_uniq, payment_sessions_provider_idempotency_uniq and
// payments_session_uniq. While the column existed, one hand-written UPDATE
// stamping a row let the SAME key open a second live one — measured on
// PostgreSQL 16.14 (docs/measurements/0054-soft-delete-in-order-and-payment.md).
// On the payment tables that key is what stands between a retried saga step and
// a SECOND CHARGE.
//
// # Why this is not the column audit
//
// [TestEveryColumnIsWrittenBySomething] refuses a column NOTHING WRITES, so it
// would catch the columns coming back empty and would go green the moment
// somebody also wrote them — which is to say, exactly when the decision is
// being reversed rather than merely repeated. This gate is about the decision:
// order and payment do not soft-delete, and reopening that means deleting this
// test and writing the ADR that supersedes 0054.
//
// # Where the population comes from
//
// From the module directory and the migration REPLAY, never from the property.
// A table that gained the column would still be in the population — it is the
// migrations that put it there — so a violation makes this test fail instead of
// quietly leaving the audit. The three floors below are what stop the walk from
// approving by reading nothing: the modules must be found by name, each must
// leave tables behind, and the total column count must be positive.
func TestTheOrderAndPaymentModulesDeclareNoSoftDelete(t *testing.T) {
	t.Parallel()

	present := moduleNames(t)
	require.NotEmpty(t, present, "no module was found at all; the walk has gone BLIND")

	scanned := 0
	for _, module := range modulesWithoutSoftDelete {
		require.True(t, slices.Contains(present, module),
			"the %q module is not in %s any more.\n"+
				"This gate names its subjects, so a module that moved or was renamed takes "+
				"its own audit with it and leaves nothing failing. Follow the module or "+
				"retire the entry deliberately.", module, modulesDir)

		migrations := readSQL(t,
			filepath.Join(repoRoot, modulesDir, module, migrationsDirName), upMigrationSuffix)
		require.NotEmpty(t, migrations,
			"the %q module's migrations read as empty; the replay would report no column "+
				"and approve every one of them", module)

		replay := newSchemaReplay()
		replay.apply(migrations)
		require.NotEmpty(t, replay.tables,
			"the %q module's migrations left no table behind; the replay no longer reads "+
				"this schema and cannot see the column it refuses", module)

		for _, table := range sortedTableNames(replay.tables) {
			columns := replay.tables[table]
			scanned += len(columns)

			_, found := columns[softDeleteColumn]
			assert.False(t, found,
				"%s.%s declares %s, and the %s module does not soft-delete (ADR 0054).\n"+
					"A record of something that happened is not hidden: an order retires by "+
					"STATUS with a dated transition for each of its four states, and a money "+
					"record is kept — the retreat from one is a refund ROW or a canceled "+
					"status. Bringing the column back also widens two uniqueness rules into "+
					"\"unique among LIVING rows\", which is one hand-written UPDATE away from "+
					"letting a retried step take a second charge.\n"+
					"If the decision is genuinely being reversed, delete this gate in the "+
					"same change as the ADR that supersedes 0054 — do not exempt a table.",
				module, table, softDeleteColumn, module)
		}
	}

	require.Positive(t, scanned,
		"no column was scanned; the audit has gone blind and would approve the columns it "+
			"exists to refuse")
}
