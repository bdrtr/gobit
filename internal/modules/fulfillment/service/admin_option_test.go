package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestThePanelWritesAShippingOption is ADR 0334: the surface lists the
// registered providers and the profiles, newest first, to write an option
// from, and writes one with every term the form carries; it is refused a
// provider that is not registered, a price type the module does not know and
// a fee on a calculated option, as the API is.
func TestThePanelWritesAShippingOption(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	surface := service.NewAdminSurface(setup.svc)
	bulky := setup.createProfile(t, "bulky")
	standard := setup.createProfile(t, "standard")

	raw, err := surface.OptionChoicesJSON(ctx)
	require.NoError(t, err)
	var choices struct {
		Providers []string `json:"providers"`
		Profiles  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"profiles"`
		ProfilesMore bool `json:"profiles_more"`
	}
	require.NoError(t, json.Unmarshal(raw, &choices))
	assert.Contains(t, choices.Providers, "fake", "the registered providers")
	// The double writes both profiles at one moment, so their order is the
	// random tails of their ids; the order on a real schema is the
	// integration test's.
	offered := make([]string, 0, len(choices.Profiles))
	for _, profile := range choices.Profiles {
		offered = append(offered, profile.ID+"|"+profile.Name+"|"+profile.Type)
	}
	assert.ElementsMatch(t, []string{bulky + "|bulky|default", standard + "|standard|default"}, offered,
		"every profile while they fit a page")
	assert.False(t, choices.ProfilesMore, "two profiles fill no second page")

	id, err := surface.CreateShippingOption(ctx, " Courier ", "fake", standard, "flat", 2_500, "try", "reg_tr", true, false, nil, nil)
	require.NoError(t, err)
	option, err := setup.svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.OptionTerms{Name: "Courier", Amount: 2_500}, option.Terms(), "offered on the storefront")
	assert.Equal(t, "fake|"+standard+"|flat|TRY|reg_tr|true",
		option.ProviderID+"|"+option.ShippingProfileID+"|"+option.PriceType.String()+"|"+option.CurrencyCode+"|"+
			option.RegionID+"|"+strconv.FormatBool(option.IsReturn), "every term as the form carried it")

	_, err = surface.CreateShippingOption(ctx, "Rate", "nobody", standard, "flat", 0, "TRY", "", false, false, nil, nil)
	assert.True(t, errors.IsNotFound(err), "a provider that is not registered: %v", err)
	_, err = surface.CreateShippingOption(ctx, "Rate", "fake", standard, "auction", 0, "TRY", "", false, false, nil, nil)
	assert.True(t, errors.IsInvalid(err), "a price type the module does not know: %v", err)
	_, err = surface.CreateShippingOption(ctx, "Rate", "fake", standard, "calculated", 500, "TRY", "", false, false, nil, nil)
	assert.True(t, errors.IsInvalid(err), "a fee on a calculated option: %v", err)
}

// TestThePanelWritesAnOptionsDeliveryDaysAsAPair is ADR 0421 on the panel's
// surface: two figures write the option's days, none writes none, one alone is
// refused, and a figure past what an int32 carries is refused rather than
// wrapped; a revision reads and writes them as a pair too.
func TestThePanelWritesAnOptionsDeliveryDaysAsAPair(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	surface := service.NewAdminSurface(setup.svc)
	profile := setup.createProfile(t, "standard")
	figure := func(v int64) *int64 { return &v }

	id, err := surface.CreateShippingOption(ctx, "Courier", "fake", profile, "flat", 900, "TRY", "", false, false,
		figure(3), figure(5))
	require.NoError(t, err)
	option, err := setup.svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, &models.DeliveryDays{Min: 3, Max: 5}, option.DeliveryDays)

	bare, err := surface.CreateShippingOption(ctx, "Bare", "fake", profile, "flat", 0, "TRY", "", false, false, nil, nil)
	require.NoError(t, err)
	none, err := setup.svc.GetShippingOption(ctx, bare)
	require.NoError(t, err)
	assert.Nil(t, none.DeliveryDays, "no figure, no days")

	for label, pair := range map[string][2]*int64{
		"a least alone": {figure(3), nil},
		"a most alone":  {nil, figure(5)},
		// Each wraps to a pair an option may carry, 1 to 5 and 1 to 1, so only
		// the bound refuses it.
		"a most past an int32":     {figure(1), figure(1<<32 + 5)},
		"a least below an int32":   {figure(-(1 << 32) + 1), figure(1)},
		"the least above the most": {figure(5), figure(3)},
	} {
		_, err := surface.CreateShippingOption(ctx, "Odd", "fake", profile, "flat", 0, "TRY", "", false, false,
			pair[0], pair[1])
		require.Error(t, err, label)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}

	require.NoError(t, surface.ReviseShippingOption(ctx, id, "Courier", 900, false, figure(3), figure(5),
		"Courier", 900, false, figure(1), figure(2)))
	revised, err := setup.svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, &models.DeliveryDays{Min: 1, Max: 2}, revised.DeliveryDays)
	err = surface.ReviseShippingOption(ctx, id, "Courier", 900, false, figure(3), figure(5),
		"Courier", 900, false, nil, nil)
	assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err), "read with the days it no longer has: %v", err)
	err = surface.ReviseShippingOption(ctx, id, "Courier", 900, false, figure(1), nil,
		"Courier", 900, false, nil, nil)
	assert.True(t, errors.IsInvalid(err), "a read pair with one figure: %v", err)
}
