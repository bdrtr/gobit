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

// TestProfileDefaultType proves that "default" is applied when no type is
// given.
func TestProfileDefaultType(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profile, err := setup.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: "default",
	})
	require.NoError(t, err)
	assert.Equal(t, models.ProfileDefault, profile.Type)
}

// TestProfileNameIsUnique proves that a second profile with the same name is
// rejected.
//
// Two profiles with the same name would leave it ambiguous which rule the
// administrator is editing.
func TestProfileNameIsUnique(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.createProfile(t, "default")

	_, err := setup.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: "default",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
}

// TestAProfileWithAnOptionCannotBeDeleted proves that the deletion does not
// silently wipe out the shipping rule of the products.
func TestAProfileWithAnOptionCannotBeDeleted(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	err := setup.svc.DeleteShippingProfile(context.Background(), profileID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeProfileInUse, errors.CodeOf(err))

	require.NoError(t, setup.svc.DeleteShippingOption(context.Background(), optionID))
	require.NoError(t, setup.svc.DeleteShippingProfile(context.Background(), profileID),
		"once the option is removed the profile has to be deletable")

	_, err = setup.svc.GetShippingProfile(context.Background(), profileID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted profile has to become unreadable: %v", err)
}

// TestTheProfilePathsLockTheRow proves that the check-then-write race is closed
// WITH A LOCK.
//
// Regression: both paths were reading the profile row without a lock. The
// deletion path saw the profile as "empty" and soft-deleted it, while an
// interleaving option creation completed without waiting; in Postgres the FOR NO
// KEY UPDATE a soft delete takes DOES NOT CONFLICT with the FOR KEY SHARE the
// INSERT takes for its foreign key. The result: a LIVE option bound to a deleted
// profile (reproduced on real Postgres).
//
// The fake store hands out the lock-taking methods only inside a transaction; if
// the lock is not taken, or if the flow steps outside the transaction, the claim
// fails.
func TestTheProfilePathsLockTheRow(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	setup.store.resetLocks()
	option, err := setup.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
		CurrencyCode:      "TRY",
		ProviderID:        "fake",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"profile-shared"}, setup.store.lockOrder(),
		"option creation has to read the profile with a SHARED lock")

	require.NoError(t, setup.svc.DeleteShippingOption(context.Background(), option.ID))

	setup.store.resetLocks()
	require.NoError(t, setup.svc.DeleteShippingProfile(context.Background(), profileID))
	assert.Equal(t, []string{"profile"}, setup.store.lockOrder(),
		"profile deletion has to take the row with a WRITE lock BEFORE the count")
}

// TestAnOptionCannotBeOpenedWithAnUnregisteredProvider proves that the setup
// error is seen WHILE THE OPTION IS BEING CREATED.
//
// Had it not been seen, the error would only blow up at the moment the option is
// about to be shown to the customer or a fulfillment is about to be opened.
func TestAnOptionCannotBeOpenedWithAnUnregisteredProvider(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	_, err := setup.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Unknown shipping",
		ProviderID:        "no-such-provider",
		ShippingProfileID: profileID,
		CurrencyCode:      "TRY",
		Amount:            1_000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)
	assert.Equal(t, service.CodeProviderNotFound, errors.CodeOf(err))
}

// TestACalculatedOptionTakesNoAmount proves that a price with two sources is
// rejected.
//
// Had it been zeroed out silently, the fee the administrator entered would never
// be applied and they would only find out through the invoice.
func TestACalculatedOptionTakesNoAmount(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	_, err := setup.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Calculated shipping",
		ProviderID:        "fake",
		ShippingProfileID: profileID,
		CurrencyCode:      "TRY",
		PriceType:         "calculated",
		Amount:            1_000,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
}

// TestOptionInputIsValidated proves that invalid inputs are rejected.
func TestOptionInputIsValidated(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	cases := []struct {
		name  string
		input service.CreateOptionInput
	}{
		{"no name", service.CreateOptionInput{
			ProviderID: "fake", ShippingProfileID: profileID, CurrencyCode: "TRY",
		}},
		{"no profile", service.CreateOptionInput{
			Name: "Shipping", ProviderID: "fake", CurrencyCode: "TRY",
		}},
		{"wrong profile prefix", service.CreateOptionInput{
			Name: "Shipping", ProviderID: "fake", ShippingProfileID: "sopt_XYZ", CurrencyCode: "TRY",
		}},
		{"malformed currency", service.CreateOptionInput{
			Name: "Shipping", ProviderID: "fake", ShippingProfileID: profileID, CurrencyCode: "TR",
		}},
		{"negative amount", service.CreateOptionInput{
			Name: "Shipping", ProviderID: "fake", ShippingProfileID: profileID,
			CurrencyCode: "TRY", Amount: -1,
		}},
		{"unrecognized price type", service.CreateOptionInput{
			Name: "Shipping", ProviderID: "fake", ShippingProfileID: profileID,
			CurrencyCode: "TRY", PriceType: "dynamic",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := setup.svc.CreateShippingOption(context.Background(), tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err) || errors.IsNotFound(err),
				"the error has to be a client error: %v", err)
		})
	}
}

// TestCurrencyIsUppercased proves that a lower-case code is normalized.
//
// Had it not been converted, "try" and "TRY" would behave like two different
// currencies and the eligibility filter would never find the option.
func TestCurrencyIsUppercased(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	option, err := setup.svc.CreateShippingOption(context.Background(), service.CreateOptionInput{
		Name:              "Standard shipping",
		ProviderID:        "fake",
		ShippingProfileID: profileID,
		CurrencyCode:      " try ",
		Amount:            2_000,
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", option.CurrencyCode)

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "try",
	})
	require.NoError(t, err)
	require.Len(t, options, 1, "a lower-case query has to find the option too")
}

// TestOptionUpdateDoesNotChangeTheProvider proves that the option's provider and
// profile ARE ABSENT from the update surface.
//
// Had they been changeable, which provider the fulfillments opened with that
// option are at would become retroactively misleading.
func TestOptionUpdateDoesNotChangeTheProvider(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	newName := "Economy shipping"
	newAmount := int64(1_500)
	updated, err := setup.svc.UpdateShippingOption(context.Background(), optionID,
		service.UpdateOptionInput{Name: &newName, Amount: &newAmount})
	require.NoError(t, err)
	assert.Equal(t, newName, updated.Name)
	assert.Equal(t, newAmount, updated.Amount)
	assert.Equal(t, "fake", updated.ProviderID)
	assert.Equal(t, profileID, updated.ShippingProfileID)
}

// TestAmountIsZeroedWhenSwitchedToCalculated proves that the old fixed amount
// does not survive the type change.
//
// Had it survived, the schema constraint would blow up and the client would get
// an error whose cause it could not make out.
func TestAmountIsZeroedWhenSwitchedToCalculated(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	calculated := "calculated"
	updated, err := setup.svc.UpdateShippingOption(context.Background(), optionID,
		service.UpdateOptionInput{PriceType: &calculated})
	require.NoError(t, err)
	assert.Equal(t, models.PriceCalculated, updated.PriceType)
	assert.Zero(t, updated.Amount, "on a calculated option the amount on the row has to be zeroed")
}

// TestRuleValidation proves that an operator expecting a single value cannot be
// given two, and that a valueless rule is rejected.
//
// Had it been swallowed silently, the administrator would only notice that the
// condition they believed they had set is not running once the orders flowed
// wrong.
func TestRuleValidation(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	cases := []struct {
		name  string
		input service.CreateRuleInput
	}{
		{"no field", service.CreateRuleInput{Operator: "eq", Values: []string{"a"}}},
		{"unrecognized operator", service.CreateRuleInput{
			Attribute: "region_id", Operator: "like", Values: []string{"a"},
		}},
		{"no value", service.CreateRuleInput{Attribute: "region_id", Operator: "eq"}},
		{"empty value", service.CreateRuleInput{
			Attribute: "region_id", Operator: "eq", Values: []string{"  "},
		}},
		{"two values for a single-value operator", service.CreateRuleInput{
			Attribute: "region_id", Operator: "eq", Values: []string{"a", "b"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID, tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
		})
	}

	multiValue, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{Attribute: "region_id", Operator: "in", Values: []string{"a", "b"}})
	require.NoError(t, err, "the in operator has to take more than one value")
	assert.Len(t, multiValue.Values, 2)
}

// TestDeletingARuleMakesTheOptionUnconditional proves that the soft deletion of a
// rule drops out of the eligibility calculation as well.
func TestDeletingARuleMakesTheOptionUnconditional(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Restricted shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	rule, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{Attribute: "customer_group_id", Operator: "eq", Values: []string{"vip"}})
	require.NoError(t, err)

	before, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Empty(t, before, "the option must not be offered because the rule does not match")

	require.NoError(t, setup.svc.DeleteShippingOptionRule(context.Background(), rule.ID))

	after, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, after, 1, "once the rule is deleted the option has to become unconditional")
}

// TestADeletedOptionIsNotListed proves that the soft deletion affects both the
// catalog and the eligibility list.
func TestADeletedOptionIsNotListed(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	require.NoError(t, setup.svc.DeleteShippingOption(context.Background(), optionID))

	_, err := setup.svc.GetShippingOption(context.Background(), optionID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, options)
}

// TestAnOptionIsReadWithItsRules proves that GetShippingOption attaches the
// rules.
func TestAnOptionIsReadWithItsRules(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{Attribute: "region_id", Operator: "eq", Values: []string{"reg_tr"}})
	require.NoError(t, err)

	option, err := setup.svc.GetShippingOption(context.Background(), optionID)
	require.NoError(t, err)
	require.Len(t, option.Rules, 1)
	assert.Equal(t, "region_id", option.Rules[0].Attribute)
}

// TestPaginationBounds exercises the limit validation.
func TestPaginationBounds(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)

	_, _, err := setup.svc.ListShippingProfiles(context.Background(), service.ListProfilesInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)

	_, _, err = setup.svc.ListShippingOptions(context.Background(), service.ListOptionsAdminInput{
		Page: service.Page{Offset: -1},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
}
