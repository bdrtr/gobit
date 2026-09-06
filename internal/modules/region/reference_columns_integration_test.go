//go:build integration

// The tests in this file need a real PostgreSQL instance and share the
// container, the pool and the schema that region_integration_test.go's TestMain
// sets up. To run them: make test-integration
//
// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the Turkish files it sits next to are on
// internal/arch/testdata/turkish_ledger.txt and stay as they are. Nothing here
// calls a helper from those files, so no identifier crosses the boundary.
package region_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheReferenceTablesHaveNoSoftDeleteColumn pins the removal made by
// migration 000003.
//
// country and currency are reference data: their rows are written by the seed
// in 000002 and the only write the module offers moves a country BETWEEN
// regions. Both columns stood unwritten from the first migration while every
// read carried "deleted_at IS NULL" — a predicate that had never once been
// false (docs/gaps.md D18).
//
// The region table is checked too, in the opposite direction: it KEEPS its
// column, DeleteRegion writes it, and a sweep that took all three would have
// removed a soft delete the module really uses.
func TestTheReferenceTablesHaveNoSoftDeleteColumn(t *testing.T) {
	ctx := context.Background()

	for table, want := range map[string]bool{
		"country":  false,
		"currency": false,
		"region":   true,
	} {
		var exists bool
		require.NoError(t, testPool.Pool().QueryRow(ctx,
			`SELECT EXISTS (
                 SELECT 1 FROM information_schema.columns
                 WHERE table_name = $1 AND column_name = 'deleted_at')`,
			table).Scan(&exists))
		assert.Equal(t, want, exists,
			"%s.deleted_at: the reference tables must not carry one and region must", table)
	}
}

// TestDroppingTheColumnDidNotTakeTheCountryIndexWithIt is the danger this
// migration had to handle, and it was measured before the migration was
// written.
//
// PostgreSQL drops any index whose PREDICATE names a dropped column — silently,
// with no notice and no error. country_region_id_idx was partial on deleted_at,
// so the DROP COLUMN removed it and 000003 has to put it back. On a probe table
// carrying a UNIQUE partial index the same drop removed the uniqueness and the
// duplicate key that had been impossible one statement earlier was accepted,
// with the schema looking untouched.
//
// The assertion is on the index's existence and its definition, not on a query
// plan: this test makes no claim about speed.
func TestDroppingTheColumnDidNotTakeTheCountryIndexWithIt(t *testing.T) {
	ctx := context.Background()

	var definition string
	err := testPool.Pool().QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
         WHERE tablename = 'country' AND indexname = 'country_region_id_idx'`,
	).Scan(&definition)
	require.NoError(t, err,
		"country_region_id_idx is gone: DROP COLUMN takes every index whose "+
			"predicate names the column, and the migration has to put it back")
	assert.NotContains(t, definition, "deleted_at",
		"the index still filters on a column that no longer exists")
}

// TestTheSeedStillCountsAfterTheColumnWentAway is the check the other two
// cannot make.
//
// A migration that dropped a column could also have taken rows with it — a
// mistaken DELETE, a failed constraint, a cascade nobody expected. The seed is
// the only reference data this module has, so its two counts being intact is
// what says the schema change moved the SHAPE and not the CONTENT.
func TestTheSeedStillCountsAfterTheColumnWentAway(t *testing.T) {
	ctx := context.Background()

	var countries, currencies int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM country`).Scan(&countries))
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM currency`).Scan(&currencies))

	assert.Positive(t, countries, "the country seed is empty after migration 000003")
	assert.Positive(t, currencies, "the currency seed is empty after migration 000003")
}
