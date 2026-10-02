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

	id, err := surface.CreateShippingOption(ctx, " Courier ", "fake", standard, "flat", 2_500, "try", "reg_tr", true, false)
	require.NoError(t, err)
	option, err := setup.svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.OptionTerms{Name: "Courier", Amount: 2_500}, option.Terms(), "offered on the storefront")
	assert.Equal(t, "fake|"+standard+"|flat|TRY|reg_tr|true",
		option.ProviderID+"|"+option.ShippingProfileID+"|"+option.PriceType.String()+"|"+option.CurrencyCode+"|"+
			option.RegionID+"|"+strconv.FormatBool(option.IsReturn), "every term as the form carried it")

	_, err = surface.CreateShippingOption(ctx, "Rate", "nobody", standard, "flat", 0, "TRY", "", false, false)
	assert.True(t, errors.IsNotFound(err), "a provider that is not registered: %v", err)
	_, err = surface.CreateShippingOption(ctx, "Rate", "fake", standard, "auction", 0, "TRY", "", false, false)
	assert.True(t, errors.IsInvalid(err), "a price type the module does not know: %v", err)
	_, err = surface.CreateShippingOption(ctx, "Rate", "fake", standard, "calculated", 500, "TRY", "", false, false)
	assert.True(t, errors.IsInvalid(err), "a fee on a calculated option: %v", err)
}
