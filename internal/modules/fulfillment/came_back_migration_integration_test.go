//go:build integration

package fulfillment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/fulfillment"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

// TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole is fulfillment migration
// 000008 (ADR 0423): a parcel already returned when the migration runs is
// marked held whole and counted as holding every unit, as it was before the
// rule; one that comes back after it is not, and holds its units only as far as
// a return or a replacement speaks for them. The down migration drops the mark.
func TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole(t *testing.T) {
	ctx := context.Background()
	src := fulfillment.New().Migrations()
	// The rollback runs in a database of its own, for D141's reason.
	dsn := testdb.New(t, testDSN, "fulfillment_held_whole")
	require.NoError(t, db.Migrate(ctx, dsn, src, fulfillment.ModuleName))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	svc, _ := newServiceOn(t, pool)
	option := newOption(ctx, t, svc, newProfile(ctx, t, svc).ID, 2_500)
	reference := "order_held_whole"
	const line = "line_held_whole"
	cameBack := func(key string, units int64) models.Fulfillment {
		ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference: reference, ShippingOptionID: option.ID, IdempotencyKey: reference + key,
			Items: []service.FulfillmentItemInput{{LineItemID: line, Quantity: units}},
		})
		require.NoError(t, err)
		_, err = svc.MarkShipped(ctx, ful.ID, "TRK"+key, "")
		require.NoError(t, err)
		_, err = svc.MarkReturned(ctx, ful.ID)
		require.NoError(t, err)

		return ful
	}
	heldWhole := func(id string) (bool, error) {
		var whole bool
		err := pool.Pool().QueryRow(ctx, `SELECT held_whole FROM fulfillments WHERE id = $1`, id).Scan(&whole)

		return whole, err
	}

	before := cameBack("-before", 2)

	require.NoError(t, db.MigrateDown(ctx, dsn, src, fulfillment.ModuleName, 1))
	_, err = heldWhole(before.ID)
	require.Error(t, err, "the down migration drops the mark")
	assert.Contains(t, err.Error(), "held_whole")

	require.NoError(t, db.Migrate(ctx, dsn, src, fulfillment.ModuleName))
	after := cameBack("-after", 3)

	whole, err := heldWhole(before.ID)
	require.NoError(t, err)
	assert.True(t, whole, "a parcel returned when the migration ran is marked held whole")
	whole, err = heldWhole(after.ID)
	require.NoError(t, err)
	assert.False(t, whole, "a parcel that comes back afterwards carries the default")

	held, err := svc.HeldForReferenceLocked(ctx, reference, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{line: 2}, held,
		"the parcel from before holds both its units; the later one holds none, nothing speaking for them")
	stored, err := svc.GetFulfillment(ctx, before.ID)
	require.NoError(t, err)
	assert.True(t, stored.HeldWhole, "the model carries the mark")
}
