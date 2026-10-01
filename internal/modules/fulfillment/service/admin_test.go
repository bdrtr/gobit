package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestThePanelMovesAParcel is ADR 0324: the surface ships a parcel with its
// tracking, delivers it, and is refused canceling a delivered one by the
// module's state machine; a second parcel comes back undelivered, and a third
// is canceled before it leaves.
func TestThePanelMovesAParcel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	surface := service.NewAdminSurface(setup.svc)
	optionID := readyOption(t, setup)

	shipped := setup.createFulfillment(t, optionID, "key-1")
	require.NoError(t, surface.ShipParcel(ctx, shipped.ID, "TK-1", "https://carrier.example/TK-1"))
	read, err := setup.svc.GetFulfillment(ctx, shipped.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusShipped, read.Status)
	assert.Equal(t, "TK-1", read.TrackingNumber)
	assert.Equal(t, "https://carrier.example/TK-1", read.TrackingURL)

	require.NoError(t, surface.DeliverParcel(ctx, shipped.ID))
	err = surface.CancelParcel(ctx, shipped.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a delivered parcel is not canceled: %v", err)

	back := setup.createFulfillment(t, optionID, "key-2")
	require.NoError(t, surface.ShipParcel(ctx, back.ID, "", ""))
	require.NoError(t, surface.ReturnParcel(ctx, back.ID))
	read, err = setup.svc.GetFulfillment(ctx, back.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusReturned, read.Status)

	unsent := setup.createFulfillment(t, optionID, "key-3")
	require.NoError(t, surface.CancelParcel(ctx, unsent.ID))
	read, err = setup.svc.GetFulfillment(ctx, unsent.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, read.Status)
}
