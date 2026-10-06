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

// days takes an option's business days inline.
func days(minDays, maxDays int32) *models.DeliveryDays {
	return &models.DeliveryDays{Min: minDays, Max: maxDays}
}

// TestAnOptionsDeliveryDaysAreHeldToTheirRange is ADR 0421: a new option says
// how many business days its delivery takes, or nothing, and the pair is held
// to 0 <= min <= max <= 365 at both ends of the range.
func TestAnOptionsDeliveryDaysAreHeldToTheirRange(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	for label, tc := range map[string]struct {
		days *models.DeliveryDays
		ok   bool
	}{
		"none":                  {nil, true},
		"a range":               {days(3, 5), true},
		"the same day":          {days(0, 0), true},
		"a year at most":        {days(365, 365), true},
		"the least above most":  {days(5, 3), false},
		"a negative least":      {days(-1, 2), false},
		"a day past a year":     {days(1, 366), false},
		"a negative most alone": {days(0, -1), false},
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()

			option, err := setup.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
				Name: "Courier", ProviderID: "fake", ShippingProfileID: profileID, CurrencyCode: "TRY",
				DeliveryDays: tc.days,
			})
			if !tc.ok {
				require.Error(t, err)
				assert.True(t, errors.IsInvalid(err), "%v", err)
				assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.days, option.DeliveryDays)
			stored, err := setup.svc.GetShippingOption(context.Background(), option.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.days, stored.DeliveryDays, "the days are stored as written")
		})
	}
}

// TestAnUpdateWritesOrClearsAnOptionsDeliveryDays is ADR 0421 on the update: a
// field left out keeps the days, a pair replaces them, a clear takes them off,
// a clear with a pair is refused, and a pair out of range is refused with
// nothing written.
func TestAnUpdateWritesOrClearsAnOptionsDeliveryDays(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	id := setup.createOption(t, service.CreateOptionInput{
		Name: "Courier", ShippingProfileID: profileID, DeliveryDays: days(3, 5),
	})
	name := "Courier service"

	kept, err := setup.svc.UpdateShippingOption(ctx, id, service.UpdateOptionInput{Name: &name})
	require.NoError(t, err)
	assert.Equal(t, days(3, 5), kept.DeliveryDays, "an update naming no days keeps them")

	changed, err := setup.svc.UpdateShippingOption(ctx, id, service.UpdateOptionInput{DeliveryDays: days(1, 2)})
	require.NoError(t, err)
	assert.Equal(t, days(1, 2), changed.DeliveryDays)

	_, err = setup.svc.UpdateShippingOption(ctx, id, service.UpdateOptionInput{
		DeliveryDays: days(2, 4), ClearDeliveryDays: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a pair and a clear together: %v", err)

	_, err = setup.svc.UpdateShippingOption(ctx, id, service.UpdateOptionInput{DeliveryDays: days(4, 2)})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a pair out of range: %v", err)
	stored, err := setup.svc.GetShippingOption(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, days(1, 2), stored.DeliveryDays, "a refused update writes nothing")

	cleared, err := setup.svc.UpdateShippingOption(ctx, id, service.UpdateOptionInput{ClearDeliveryDays: true})
	require.NoError(t, err)
	assert.Nil(t, cleared.DeliveryDays, "a clear takes the days off")
}

// TestARevisionWritesTheDaysItReadAndRefusesOthers is ADR 0421 on the panel's
// revision (ADR 0333): the days are written from the ones read, a revision
// that read other days is refused as one that read another name is, and a pair
// out of range is refused as a new option's is.
func TestARevisionWritesTheDaysItReadAndRefusesOthers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	id := setup.createOption(t, service.CreateOptionInput{
		Name: "Courier", ShippingProfileID: profileID, Amount: 900, DeliveryDays: days(3, 5),
	})
	read := models.OptionTerms{Name: "Courier", Amount: 900, DeliveryDays: days(3, 5)}

	_, err := setup.svc.ReviseShippingOption(ctx, id,
		models.OptionTerms{Name: "Courier", Amount: 900, DeliveryDays: days(2, 5)},
		models.OptionTerms{Name: "Courier", Amount: 900, DeliveryDays: days(1, 1)})
	require.Error(t, err)
	assert.Equal(t, service.CodeOptionRevised, errors.CodeOf(err), "read with other days: %v", err)

	_, err = setup.svc.ReviseShippingOption(ctx, id, read,
		models.OptionTerms{Name: "Courier", Amount: 900, DeliveryDays: days(9, 1)})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a pair out of range: %v", err)

	revised, err := setup.svc.ReviseShippingOption(ctx, id, read,
		models.OptionTerms{Name: "Courier", Amount: 900, DeliveryDays: days(1, 2)})
	require.NoError(t, err)
	assert.Equal(t, days(1, 2), revised.DeliveryDays)

	cleared, err := setup.svc.ReviseShippingOption(ctx, id, revised.Terms(),
		models.OptionTerms{Name: "Courier", Amount: 900})
	require.NoError(t, err)
	assert.Nil(t, cleared.DeliveryDays, "a revision with no days takes them off")
}
