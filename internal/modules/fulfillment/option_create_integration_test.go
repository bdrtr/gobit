//go:build integration

package fulfillment_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestThePanelWritesAShippingOptionOnTheRealSchema is ADR 0334 against a
// real PostgreSQL: the surface offers the registered provider and a profile
// just written among the newest, and writes an option with every term the
// form carries, the profile's lock taken as the API's write takes it; a
// profile that is not there is refused.
func TestThePanelWritesAShippingOptionOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	surface := service.NewAdminSurface(svc)
	profile := newProfile(ctx, t, svc)

	raw, err := surface.OptionChoicesJSON(ctx)
	require.NoError(t, err)
	var choices struct {
		Providers []string `json:"providers"`
		Profiles  []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(raw, &choices))
	assert.Contains(t, choices.Providers, manual.ID)
	require.NotEmpty(t, choices.Profiles)
	assert.Equal(t, profile.ID+"|"+profile.Name, choices.Profiles[0].ID+"|"+choices.Profiles[0].Name,
		"the profile just written comes first")

	id, err := surface.CreateShippingOption(ctx, "Panel courier", manual.ID, profile.ID, "flat", 1_990,
		testCurrency, testRegion, false, true, nil, nil)
	require.NoError(t, err)
	option, err := svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.OptionTerms{Name: "Panel courier", Amount: 1_990, AdminOnly: true}, option.Terms())
	assert.Equal(t, manual.ID+"|"+profile.ID+"|"+testCurrency+"|"+testRegion,
		option.ProviderID+"|"+option.ShippingProfileID+"|"+option.CurrencyCode+"|"+option.RegionID)

	_, err = surface.CreateShippingOption(ctx, "Orphan", manual.ID, "sprof_missing", "flat", 0, testCurrency, "", false, false, nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a profile that is not there: %v", err)
}
