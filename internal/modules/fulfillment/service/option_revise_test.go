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

// ReviseShippingOption mirrors the query: the terms are written only from the
// ones the caller read, on a live option, and a fee on a calculated one is
// refused as the schema refuses it.
func (f *fakeStore) ReviseShippingOption(
	_ context.Context, id string, read, next models.OptionTerms,
) (models.ShippingOption, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	current, ok := f.options[id]
	if !ok || current.DeletedAt != nil || !sameTerms(current.Terms(), read) {
		return models.ShippingOption{}, false, nil
	}
	if current.PriceType != models.PriceFlat && next.Amount != 0 {
		return models.ShippingOption{}, false, errors.Invalid("fulfillment_constraint_violation",
			"the amount of a calculated shipping option must be zero; the fee comes from the provider")
	}
	current.Name, current.Amount, current.AdminOnly = next.Name, next.Amount, next.AdminOnly
	current.DeliveryDays = next.DeliveryDays
	current.UpdatedAt = testNow
	f.options[id] = current
	return current, true, nil
}

// sameTerms compares two terms as the query does: the days by value, IS NOT
// DISTINCT FROM, and not by the address their pointer holds.
func sameTerms(a, b models.OptionTerms) bool {
	sameDays := (a.DeliveryDays == nil) == (b.DeliveryDays == nil) &&
		(a.DeliveryDays == nil || *a.DeliveryDays == *b.DeliveryDays)
	return sameDays && a.Name == b.Name && a.Amount == b.Amount && a.AdminOnly == b.AdminOnly
}

// TestAShippingOptionIsRevisedFromWhatWasRead is ADR 0333: the name trimmed,
// the fee and the storefront visibility are written from the terms read, the
// provider, the profile, the price type and the region kept; an option
// revised since is refused by what it is now; a fee on a calculated option is
// refused as on a new one, and none is not; an empty name and a negative fee
// are refused; an unknown option is not found.
func TestAShippingOptionIsRevisedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	flatID := setup.createOption(t, service.CreateOptionInput{
		Name: "Standard", ShippingProfileID: profileID, Amount: 2_000, RegionID: "reg_tr",
	})
	calculatedID := setup.createOption(t, service.CreateOptionInput{
		Name: "Carrier rate", ShippingProfileID: profileID, PriceType: "calculated",
	})
	read := models.OptionTerms{Name: "Standard", Amount: 2_000}

	revised, err := setup.svc.ReviseShippingOption(ctx, flatID, read,
		models.OptionTerms{Name: " Economy ", Amount: 1_500, AdminOnly: true})
	require.NoError(t, err)
	assert.Equal(t, models.OptionTerms{Name: "Economy", Amount: 1_500, AdminOnly: true}, revised.Terms())
	assert.Equal(t, "fake|"+profileID+"|flat|reg_tr",
		revised.ProviderID+"|"+revised.ShippingProfileID+"|"+revised.PriceType.String()+"|"+revised.RegionID,
		"the provider, the profile, the price type and the region are kept")

	_, err = setup.svc.ReviseShippingOption(ctx, flatID, read, models.OptionTerms{Name: "Express", Amount: 3_000})
	require.Error(t, err)
	assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err), "read as Standard, it is Economy now: %v", err)
	assert.Contains(t, err.Error(), `it is "Economy" now`)

	calculated := models.OptionTerms{Name: "Carrier rate"}
	_, err = setup.svc.ReviseShippingOption(ctx, calculatedID, calculated, models.OptionTerms{Name: "Carrier rate", Amount: 500})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a fee on a calculated option: %v", err)
	assert.Contains(t, err.Error(), "the fee comes from the provider")
	hidden, err := setup.svc.ReviseShippingOption(ctx, calculatedID, calculated,
		models.OptionTerms{Name: "Carrier rate", AdminOnly: true})
	require.NoError(t, err, "a calculated option is renamed and hidden with no fee of its own")
	assert.True(t, hidden.AdminOnly)

	for label, next := range map[string]models.OptionTerms{
		"an empty name":  {Name: "  ", Amount: 100},
		"a negative fee": {Name: "X", Amount: -1},
	} {
		_, err = setup.svc.ReviseShippingOption(ctx, flatID, revised.Terms(), next)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	stored, err := setup.svc.GetShippingOption(ctx, flatID)
	require.NoError(t, err)
	assert.Equal(t, revised.Terms(), stored.Terms(), "a refused input writes nothing")

	_, err = setup.svc.ReviseShippingOption(ctx, "sopt_missing", read, models.OptionTerms{Name: "X"})
	assert.True(t, errors.IsNotFound(err), "an unknown option: %v", err)
	_, err = setup.svc.ReviseShippingOption(ctx, "so_missing", read, models.OptionTerms{Name: "X"})
	assert.True(t, errors.IsInvalid(err), "an id that is not an option's: %v", err)
}
