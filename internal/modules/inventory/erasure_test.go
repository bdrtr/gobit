// These tests need NO database and carry no build tag. A declaration is a
// property of the code rather than of the data — the same sentence on an empty
// installation and a full one — which is why [personaldata.Declarer] takes no
// context and returns no error, and why the audit of it has to be runnable by
// somebody who has the repository and nothing else.
package inventory_test

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/inventory"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns are the columns that hold nothing about a person, table by
// table, as the schema stands AFTER every migration has run.
//
// EVERY table the module creates has an entry, including inventory_levels which
// the declaration does not touch at all. That is the load-bearing half of
// [TestTheDeclarationCoversEveryPersonalColumn]: everything not named here has
// to appear in the declaration, so a column added tomorrow fails this test until
// somebody decides which side of the line it is on, and a whole new table fails
// it until somebody has read the table. A rule that guessed instead would wave a
// "collected_by" column through on the grounds that it looked technical.
//
// The reasons, per table:
//
//   - The identifiers are exempt everywhere. A synthetic key names a row, not a
//     person; declaring one would drag every foreign id in the deployment in
//     behind it. inventory_reservations.line_item_id is the one worth arguing:
//     it is the module's ONLY tie to a shopper, it points at a cart line that
//     lives in another module, and once the columns beside that line are
//     anonymized it resolves to nobody.
//   - inventory_levels is exempt WHOLE. It is two ids and two counts — how many
//     units of a thing sit at a place — and it carries no text at all, so there
//     is nowhere for a person to be in it.
//   - inventory_items.sku is a goods code. It is required and unique among
//     living items, which is to say gobit uses it to tell one product from
//     another, not one person from another; title and description, the two free
//     text fields beside it, ARE declared.
//   - requires_shipping, quantity, status and a reservation's purpose are a
//     boolean, a count and CHECK-constrained enums: a sentence about somebody
//     cannot land in any of them. inventory_movements.reason is the same shape as status — seven
//     values, held to them by a CHECK — and delta and stocked_after are counts.
//   - inventory_movements is exempt WHOLE, and it is the table where that is
//     worth arguing rather than asserting. It is the ledger that explains the
//     physical count (ADR 0068), and it deliberately carries NO ACTOR: an
//     operator's movement is recorded with its caller in audit_log, not here,
//     and the other movements come from a flow with nobody behind them. Its
//     only free field would have been an actor or a note, and it has neither —
//     a row is ids (its item, its location, its reservation, and the order,
//     cancellation, order line or supplier receipt it moved for), an enum and
//     two numbers. The
//     reference and the line were added by ALTER statements this audit's own
//     scanner never matched, and went unjudged until D190.
//   - inventory_backorders is exempt WHOLE, for the ledger's reason. A claim
//     (ADR 0392) is ids — its item, the order and order line it is owed to, the
//     warehouses it may be filled at, the reservation and warehouse that filled
//     it — two counts, a CHECK-constrained status and a queue position. Like
//     the reservation's line, the order's ids resolve to nobody once the order
//     module's own columns are anonymized.
//   - inventory_supplier_receipts is exempt but for its reference (ADR 0399).
//     A receipt is ids — its item and its warehouse — two counts, a moment
//     the units are expected, a CHECK-constrained status and the moments it
//     was received or canceled. Its reference is free text the shop types and
//     is declared Open in the module's PersonalData.
//   - The timestamps describe the record's state and are true of the row whether
//     or not anybody is behind it.
var notPersonalColumns = map[string][]string{
	"stock_locations": {
		"id", "created_at", "updated_at", "closed_at",
	},
	"inventory_items": {
		"id", "sku", "requires_shipping", "created_at", "updated_at", "deleted_at",
	},
	"inventory_levels": {
		"id", "inventory_item_id", "location_id", "stocked_quantity",
		"reserved_quantity", "created_at", "updated_at", "deleted_at",
	},
	"inventory_reservations": {
		"id", "inventory_item_id", "location_id", "quantity", "line_item_id",
		"status", "created_at", "updated_at", "purpose",
	},
	"inventory_movements": {
		"id", "inventory_item_id", "location_id", "reservation_id", "reason",
		"delta", "stocked_after", "created_at", "reference", "line_item_id",
	},
	"inventory_backorders": {
		"id", "inventory_item_id", "order_id", "order_line_item_id", "quantity",
		"withdrawn_quantity", "location_ids", "status", "reservation_id",
		"filled_location_id", "seq", "created_at", "updated_at",
	},
	"inventory_supplier_receipts": {
		"id", "inventory_item_id", "location_id", "quantity", "expected_at", "status",
		"received_quantity", "received_at", "canceled_at", "created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryPersonalColumn proves the declaration EXHAUSTIVE
// against the schema it describes, in both directions.
//
// A declaration is the only answer an embedder has to "where in your database is
// this person", and it is what the embedder publishes as its privacy notice. A
// missing row therefore does not make the list shorter, it makes it FALSE, and
// the column it forgot is one no audit will ever visit. The opposite direction
// matters as much: a holding naming a column that is not in the schema sends an
// auditor looking for data that does not exist.
func TestTheDeclarationCoversEveryPersonalColumn(t *testing.T) {
	t.Parallel()

	module := inventory.New()
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// TestTheDeclarationLeavesTheHolderToTheCoordinator pins an EMPTY field.
//
// The coordinator overwrites Holder with the name the module is registered
// under, so a name hard-coded here would be dead the moment it disagreed with
// the registry — and it would disagree silently, because both halves look
// entirely normal on their own. The empty value is the assertion that this
// module is not the one that decides what it is called.
func TestTheDeclarationLeavesTheHolderToTheCoordinator(t *testing.T) {
	t.Parallel()

	assert.Empty(t, inventory.New().PersonalData().Holder,
		"the holder comes from the registry; setting it here creates a second "+
			"name that can drift from the one the report prints")
}

// TestEveryHoldingCanBeReadOffOneReport checks the shape of the strings rather
// than their content.
//
// One report puts these sentences side by side with the other modules', which is
// what makes the shape a contract and not a style preference (ADR 0033): a Why
// that starts with a capital or wraps onto a second line reads as a different
// document inside the same list. The Kind matters for the same reason — it is
// what tells the reader whether gobit knows what is in the column or the
// embedder does, and a holding without one says neither.
func TestEveryHoldingCanBeReadOffOneReport(t *testing.T) {
	t.Parallel()

	for _, holding := range inventory.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column

		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about who wrote the column", key)

		require.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
		assert.Equal(t, strings.TrimSpace(holding.Why), holding.Why,
			"%s: the Why is padded and lands in a list of other modules' sentences", key)
		assert.NotContains(t, holding.Why, "\n",
			"%s: the Why is one clause, not a paragraph", key)
		first, _ := utf8.DecodeRuneInString(holding.Why)
		assert.True(t, unicode.IsLower(first),
			"%s: the Why starts with a capital; these clauses sit side by side in one "+
				"report and one of them shouting is how a reader tells them apart", key)
	}
}

// TestTheDeclarationDoesNotNeedRegister holds the property [personaldata.Declarer]
// states in its own words.
//
// A declaration is a property of the code, so an audit reads it with no database
// and a module whose Register failed must still be able to say what it holds.
// The module built here has no pool, no service and no routes.
func TestTheDeclarationDoesNotNeedRegister(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, inventory.New().PersonalData().Holdings)
}

// TestTheSchemaScannerFollowedEveryMigration is the audit auditing itself.
//
// The scanner (internal/schemaaudit) replays every migration, and a scanner
// that stopped at the CREATE TABLE blocks would be describing the schema of
// 000001: 000002 DROPS inventory_reservations.deleted_at, and 000003 replaces
// stock_locations.deleted_at with closed_at (ADR 0055). The scanner this audit
// had before matched an ALTER TABLE only when the table's name and the action
// shared a line, and so never judged three columns added by statements written
// across lines (D190). This module has already
// paid for exactly that mistake once: the audit that was supposed to catch the
// never-written column matched writes by bare column name and could not see it
// for as long as another table wrote a column of the same name (the reasoning
// is in the head of 000002). So the facts the scanner has to get right are
// asserted directly rather than left to be implied by the tests above passing.
//
// Each drop is paired with a column that SURVIVES it, because "the schema has
// this column" and "the scanner reads this table at all" fail the same way
// otherwise: a table it stopped reading contains nothing, and every NotContains
// about it passes.
func TestTheSchemaScannerFollowedEveryMigration(t *testing.T) {
	t.Parallel()

	schema := schemaaudit.Schema(t, inventory.New().Migrations())

	assert.False(t, schema["inventory_reservations"]["deleted_at"],
		"000002 drops this column; a scanner that still sees it is auditing a "+
			"schema the module does not ship")
	assert.False(t, schema["stock_locations"]["deleted_at"],
		"000003 drops this column; a scanner that still sees it is auditing a "+
			"schema the module does not ship")
	assert.True(t, schema["stock_locations"]["closed_at"],
		"000003 adds the column that replaces it; without it the drop above reads "+
			"as a table the scanner stopped following")
	assert.True(t, schema["inventory_items"]["deleted_at"],
		"the drops applied to the wrong table, or to all of them")
	assert.True(t, schema["inventory_reservations"]["description"],
		"the reservation columns stopped being read at all, which would make every "+
			"assertion about them vacuously true")
}
