//go:build integration

package fulfillment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAnOptionsDeliveryDaysOnTheRealSchema is ADR 0421 against a real
// PostgreSQL: the days are stored, read back, replaced, revised from the ones
// read (an option without days compared as NULL IS NOT DISTINCT FROM NULL),
// cleared, and listed for a cart; the schema refuses a pair the service would,
// a half pair and a range past a year, each by its constraint.
func TestAnOptionsDeliveryDaysOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	profile := newProfile(ctx, t, svc)
	surface := service.NewAdminSurface(svc)

	option, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name: "Courier", ProviderID: manual.ID, ShippingProfileID: profile.ID, Amount: 900,
		CurrencyCode: testCurrency, RegionID: testRegion, DeliveryDays: &models.DeliveryDays{Min: 3, Max: 5},
	})
	require.NoError(t, err)
	stored, err := svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	assert.Equal(t, &models.DeliveryDays{Min: 3, Max: 5}, stored.DeliveryDays, "stored as written")

	quoted, err := svc.ListShippingOptionsFor(ctx, service.ListOptionsInput{
		RegionID: testRegion, CurrencyCode: testCurrency, ShippingProfileIDs: []string{profile.ID},
	})
	require.NoError(t, err)
	listed := false
	for _, q := range quoted {
		if q.Option.ID == option.ID {
			listed = true
			assert.Equal(t, &models.DeliveryDays{Min: 3, Max: 5}, q.Option.DeliveryDays, "the eligibility read carries them")
		}
	}
	assert.True(t, listed, "the option is listed for its region")

	updated, err := svc.UpdateShippingOption(ctx, option.ID, service.UpdateOptionInput{
		DeliveryDays: &models.DeliveryDays{Min: 1, Max: 2},
	})
	require.NoError(t, err)
	assert.Equal(t, &models.DeliveryDays{Min: 1, Max: 2}, updated.DeliveryDays)

	figure := func(v int64) *int64 { return &v }
	err = surface.ReviseShippingOption(ctx, option.ID, "Courier", 900, false, figure(3), figure(5),
		"Courier", 900, false, nil, nil)
	assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err), "read with days it no longer has: %v", err)
	require.NoError(t, surface.ReviseShippingOption(ctx, option.ID, "Courier", 900, false, figure(1), figure(2),
		"Courier", 900, false, nil, nil), "read with the days it has, written with none")
	require.NoError(t, surface.ReviseShippingOption(ctx, option.ID, "Courier", 900, false, nil, nil,
		"Courier", 900, false, figure(4), figure(6)), "read with none, as NULL is not distinct from NULL")
	revised, err := svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	assert.Equal(t, &models.DeliveryDays{Min: 4, Max: 6}, revised.DeliveryDays)

	cleared, err := svc.UpdateShippingOption(ctx, option.ID, service.UpdateOptionInput{ClearDeliveryDays: true})
	require.NoError(t, err)
	assert.Nil(t, cleared.DeliveryDays)

	for constraint, sql := range map[string]string{
		"shipping_options_delivery_days_paired": `UPDATE shipping_options SET delivery_min_days = 2 WHERE id = $1`,
		"shipping_options_delivery_days_range": `UPDATE shipping_options SET delivery_min_days = 5, ` +
			`delivery_max_days = 3 WHERE id = $1`,
	} {
		_, err := testPool.Pool().Exec(ctx, sql, option.ID)
		require.Error(t, err, constraint)
		assert.Contains(t, err.Error(), `check constraint "`+constraint+`"`)
	}
	for label, pair := range map[string][2]int{
		"a negative least": {-1, 2}, "a most past a year": {1, 366},
	} {
		_, err := testPool.Pool().Exec(ctx,
			`UPDATE shipping_options SET delivery_min_days = $2, delivery_max_days = $3 WHERE id = $1`,
			option.ID, pair[0], pair[1])
		require.Error(t, err, label)
		assert.Contains(t, err.Error(), `check constraint "shipping_options_delivery_days_range"`, label)
	}
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE shipping_options SET delivery_min_days = 0, delivery_max_days = 365 WHERE id = $1`, option.ID)
	require.NoError(t, err, "the widest range is a carrier's year")
}
