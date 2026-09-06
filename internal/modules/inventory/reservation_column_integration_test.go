//go:build integration

// The tests in this file need a real PostgreSQL instance and share the
// container, the pool and the schema that inventory_integration_test.go's
// TestMain sets up. To run them: make test-integration
//
// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the Turkish files it sits next to are on
// internal/arch/testdata/turkish_ledger.txt and stay as they are. Nothing here
// calls a helper from those files, so no identifier crosses the boundary.
package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheReservationTableHasNoSoftDeleteColumn pins the removal made by
// migration 000002.
//
// The column existed from the first migration and NOTHING ever wrote it, while
// every read of the table carried "deleted_at IS NULL" — a predicate that had
// never once been false (docs/gaps.md D18). It contradicted the rule the table
// enforces with its status machine: a reservation is released, not deleted, and
// the checkout saga's compensation is idempotent only because a released
// reservation stays readable.
//
// The test asks the DATABASE rather than reading the migration text, because
// what matters is the schema the module ends up with after every migration has
// run.
func TestTheReservationTableHasNoSoftDeleteColumn(t *testing.T) {
	ctx := context.Background()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM information_schema.columns
             WHERE table_name = 'inventory_reservations' AND column_name = 'deleted_at')`,
	).Scan(&exists)
	require.NoError(t, err)
	assert.False(t, exists,
		"inventory_reservations.deleted_at is back; a column nothing writes is a "+
			"predicate every read pays for and a second way of saying what status says")
}

// TestDroppingTheColumnDidNotTakeTheReservationIndexesWithIt is the real danger
// of this migration, and it was measured before the migration was written.
//
// PostgreSQL drops any index whose PREDICATE names a dropped column — silently,
// with no notice and no error. Both of this table's indexes were partial on
// deleted_at, so the DROP COLUMN removed both; 000002 recreates them without
// the predicate. A probe on a real PostgreSQL 16 showed the failure mode in its
// worst form: a UNIQUE partial index disappeared with the column and the
// duplicate key that had been impossible one statement earlier was accepted.
//
// The assertion is on the index's EXISTENCE and on its predicate, not on a
// query plan. This test makes no claim about speed; it says only that the
// structures 000001 declared are the structures the schema still has.
func TestDroppingTheColumnDidNotTakeTheReservationIndexesWithIt(t *testing.T) {
	ctx := context.Background()

	for _, index := range []string{
		"inventory_reservations_item_idx",
		"inventory_reservations_line_item_idx",
	} {
		var definition string
		err := testPool.Pool().QueryRow(ctx,
			`SELECT indexdef FROM pg_indexes
             WHERE tablename = 'inventory_reservations' AND indexname = $1`,
			index).Scan(&definition)
		require.NoError(t, err,
			"%s is gone: DROP COLUMN takes every index whose predicate names the "+
				"column, and the migration has to put it back", index)
		assert.NotContains(t, definition, "deleted_at",
			"%s still filters on a column that no longer exists", index)
	}
}
