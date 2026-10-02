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

// TestThePanelRevisesAShippingOptionFromWhatItRead is ADR 0333 against a
// real PostgreSQL: the name, the fee and the storefront visibility are
// written from the terms read, the provider, the profile, the price type and
// the region kept; a stale name, fee or visibility writes nothing; a fee on a
// calculated option is refused by the schema in the operator's words, and
// none is not; a deleted option is not found.
func TestThePanelRevisesAShippingOptionFromWhatItRead(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	surface := service.NewAdminSurface(svc)
	profile := newProfile(ctx, t, svc)
	option := newOption(ctx, t, svc, profile.ID, 2_500)

	require.NoError(t, surface.ReviseShippingOption(ctx, option.ID, option.Name, 2_500, false, " Economy ", 1_750, true))
	stored, err := svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OptionTerms{Name: "Economy", Amount: 1_750, AdminOnly: true}, stored.Terms())
	assert.Equal(t, manual.ID+"|"+profile.ID+"|flat|"+testRegion,
		stored.ProviderID+"|"+stored.ShippingProfileID+"|"+stored.PriceType.String()+"|"+stored.RegionID,
		"the provider, the profile, the price type and the region are kept")

	for label, read := range map[string]models.OptionTerms{
		"a name read before the revision":       {Name: option.Name, Amount: 1_750, AdminOnly: true},
		"a fee read before the revision":        {Name: "Economy", Amount: 2_500, AdminOnly: true},
		"a visibility read before the revision": {Name: "Economy", Amount: 1_750},
	} {
		err = surface.ReviseShippingOption(ctx, option.ID, read.Name, read.Amount, read.AdminOnly, "Express", 3_000, false)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err), "%s: %v", label, err)
		assert.Contains(t, err.Error(), `it is "Economy" now`, label)
	}
	stored, err = svc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	assert.Equal(t, "Economy", stored.Name, "a stale read writes nothing")

	calculated, err := svc.CreateShippingOption(ctx, service.CreateOptionInput{
		Name: "calculated-" + models.NewShippingOptionID(), ProviderID: manual.ID,
		ShippingProfileID: profile.ID, PriceType: "calculated", CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	err = surface.ReviseShippingOption(ctx, calculated.ID, calculated.Name, 0, false, calculated.Name, 500, false)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a fee on a calculated option: %v", err)
	assert.Contains(t, err.Error(), "the fee comes from the provider")
	require.NoError(t, surface.ReviseShippingOption(ctx, calculated.ID, calculated.Name, 0, false, "Carrier rate", 0, true),
		"a calculated option is renamed and hidden with no fee of its own")
	err = surface.ReviseShippingOption(ctx, calculated.ID, calculated.Name, 0, false, calculated.Name, 500, false)
	require.Error(t, err)
	assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err),
		"a stale read is told the option moved, whatever else it sends: %v", err)

	require.NoError(t, svc.DeleteShippingOption(ctx, option.ID))
	err = surface.ReviseShippingOption(ctx, option.ID, "Economy", 1_750, true, "Gone", 0, false)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted option: %v", err)
}
