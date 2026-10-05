//go:build integration

package inventory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestACountOverAMovedLevelIsRefusedOnTheRealSchema is ADR 0280 over the real
// schema: a form drawn at ten is refused once a unit has left, the level keeps
// the movement, and a count drawn at what is there is written.
func TestACountOverAMovedLevelIsRefusedOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), nil)
	admin := service.NewAdminSurface(svc)

	item, err := svc.CreateInventoryItem(ctx, service.CreateInventoryItemInput{
		SKU: "SKU-" + models.NewInventoryItemID(), Title: t.Name(),
	})
	require.NoError(t, err)
	location, err := svc.CreateStockLocation(ctx, service.CreateStockLocationInput{
		Name: "Count " + t.Name(), CountryCode: "TR",
	})
	require.NoError(t, err)
	_, err = svc.SetInventoryLevel(ctx, item.ID, location.ID, 10)
	require.NoError(t, err)

	// The page is drawn at ten; a unit leaves before the count is saved.
	_, err = svc.AdjustInventory(ctx, item.ID, location.ID, -1)
	require.NoError(t, err)

	_, err = admin.SetStockLevel(ctx, item.ID, location.ID, 10, 12)
	require.Error(t, err)
	assert.Equal(t, service.CodeStockMoved, errors.CodeOf(err))

	assert.Equal(t, int64(9), physicalCount(ctx, t, svc, item.ID, location.ID), "the unit that left is not written back")

	stocked, err := admin.SetStockLevel(ctx, item.ID, location.ID, 9, 12)
	require.NoError(t, err)
	assert.Equal(t, int64(12), stocked)
	assert.Equal(t, int64(12), physicalCount(ctx, t, svc, item.ID, location.ID))
}

// physicalCount is the physical count of an item at a location.
func physicalCount(ctx context.Context, t *testing.T, svc *service.Service, itemID, locationID string) int64 {
	t.Helper()

	levels, err := svc.ListInventoryLevels(ctx, itemID)
	require.NoError(t, err)
	for i := range levels {
		if levels[i].LocationID == locationID {
			return levels[i].StockedQuantity
		}
	}
	require.Fail(t, "no level at the location")

	return 0
}
