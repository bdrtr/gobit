package service_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAnAdminOnlyOptionDoesNotReachTheStorefront exercises the storefront
// requirement of Phase 7.
//
// An admin_only option must NEVER appear on the storefront; had the filter only
// been applied while the response was being produced, the row would be read and
// could leak by accident in a later refactor.
func TestAnAdminOnlyOptionDoesNotReachTheStorefront(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	openID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	hiddenID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Hand delivery",
		ShippingProfileID: profileID,
		Amount:            0,
		AdminOnly:         true,
	})

	storefront, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, storefront, 1, "the storefront has to see only the open option")
	assert.Equal(t, openID, storefront[0].Option.ID)

	admin, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode:     "TRY",
		IncludeAdminOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, admin, 2, "the admin surface has to see both options")

	ids := []string{admin[0].Option.ID, admin[1].Option.ID}
	assert.Contains(t, ids, hiddenID, "the admin_only option has to be visible in the admin surface")
}

// TestAFlatOptionDoesNotGoToTheProvider proves that a fixed-fee option is NEVER
// asked of the provider.
//
// Had it gone, a pointless network call would be made every time the cart is
// updated, and at a moment when the provider is unreachable the fixed-fee option
// would drop as well.
func TestAFlatOptionDoesNotGoToTheProvider(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, int64(2_000), options[0].Amount, "the fixed fee has to come from the row")
	assert.Equal(t, "TRY", options[0].CurrencyCode)

	quote, _, _ := setup.provider.callCounts()
	assert.Zero(t, quote, "the provider must not be called for a fixed-fee option")
}

// TestACalculatedOptionTakesItsPriceFromTheProvider proves that the fee of a
// calculated option comes from the provider and that the option's configuration
// IS PASSED THROUGH to Quote.
//
// Had the configuration not been passed, the provider could not know the
// per-kilogram fee and would quote the same price for every shipment.
func TestACalculatedOptionTakesItsPriceFromTheProvider(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.provider.quoteAmount = 7_350
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Calculated shipping",
		ShippingProfileID: profileID,
		PriceType:         "calculated",
		Data:              map[string]any{"manual_per_kilogram_amount": 500},
	})

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		CountryCode:  "tr",
		ItemCount:    3,
		TotalWeight:  1_200,
	})
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, int64(7_350), options[0].Amount, "the fee has to come from the provider")
	assert.NotEmpty(t, options[0].ProviderData, "the provider's raw data has to be carried")

	input := setup.provider.lastQuoteInput()
	assert.Equal(t, "TRY", input.CurrencyCode)
	assert.Equal(t, "TR", input.CountryCode, "the country code has to be uppercased")
	assert.Equal(t, int64(3), input.ItemCount)
	assert.Equal(t, int64(1_200), input.TotalWeight)
	assert.Equal(t, 500, input.Data["manual_per_kilogram_amount"],
		"the option's configuration has to be passed to the provider")
}

// TestAProviderErrorDropsOnlyThatOption proves that when the provider of a
// calculated option blows up, the FIXED-fee options stay standing.
//
// The claim is that a single carrier being unreachable must not shut down the
// payment step (see the ListShippingOptionsFor godoc).
func TestAProviderErrorDropsOnlyThatOption(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.provider.quoteErr = errors.Unavailable("test_provider_down", "the carrier is unreachable")
	profileID := setup.createProfile(t, "default")

	flatID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Calculated shipping",
		ShippingProfileID: profileID,
		PriceType:         "calculated",
	})

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err, "a single provider failure must not drop the whole request")
	require.Len(t, options, 1)
	assert.Equal(t, flatID, options[0].Option.ID)
}

// TestAnOptionDropsWhenTheProviderReturnsAnotherCurrency proves that the
// contract violation is caught.
//
// Had it not been caught, a shipping fee denominated in dollars would be
// silently added to a lira cart and the difference would only be seen in
// accounting.
func TestAnOptionDropsWhenTheProviderReturnsAnotherCurrency(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.provider.quoteCurrency = "USD"
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Calculated shipping",
		ShippingProfileID: profileID,
		PriceType:         "calculated",
	})

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, options, "an option quoted in a different currency must not be listed")
}

// TestFreeShippingRule exercises the example rule from Phase 7 of the plan:
// "free shipping if the total is >= 50000".
//
// There are two options and both are on the same profile; the only thing that
// separates them is the rule. While the subtotal is below the threshold the free
// option IS NOT OFFERED; above it, it is offered and, because it is cheaper, it
// moves TO THE HEAD OF THE LIST.
func TestFreeShippingRule(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	freeID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Free shipping",
		ShippingProfileID: profileID,
		Amount:            0,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), freeID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	// The context is marked TRUSTED: rule-bound options are listed only for
	// callers that produce the cart facts on the server side
	// (see TestARuleBoundOptionIsNotListedInAnUntrustedContext).
	below, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     49_999,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, below, 1, "below the threshold free shipping must not be offered")
	assert.Equal(t, int64(2_000), below[0].Amount)

	atThreshold, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     50_000,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, atThreshold, 2, "at the threshold free shipping has to be offered too")
	assert.Equal(t, freeID, atThreshold[0].Option.ID, "the cheaper one has to be at the head of the list")
	assert.Equal(t, int64(0), atThreshold[0].Amount)
	assert.Equal(t, int64(2_000), atThreshold[1].Amount)

	// A subtotal with MORE DIGITS than the threshold: had the comparison been
	// lexical, "100000" < "50000" would come out and a cart far above the
	// threshold WOULD LOSE free shipping. This branch proves that the comparison
	// really is numeric.
	wellAbove, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     100_000,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, wellAbove, 2, "far above the threshold free shipping has to be offered as well")
	assert.Equal(t, freeID, wellAbove[0].Option.ID)
}

// TestARuleLookingAtAFieldAbsentFromTheContextDoesNotMatch proves that when the
// field a rule looks at is absent from the context the option IS ELIMINATED —
// even on a negative operator.
//
// Otherwise a request with an empty context would satisfy every negative rule and
// open the restricted options to everyone.
func TestARuleLookingAtAFieldAbsentFromTheContextDoesNotMatch(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "VIP shipping",
		ShippingProfileID: profileID,
		Amount:            1_000,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{
			Attribute: "customer_group_id",
			Operator:  "ne",
			Values:    []string{"blocked"},
		})
	require.NoError(t, err)

	withoutContext, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, withoutContext, "a context that does not carry the field MUST NOT satisfy the negative rule")

	withContext, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"customer_group_id": "vip"},
	})
	require.NoError(t, err)
	require.Len(t, withContext, 1, "when the field is given the rule has to be evaluated")
	assert.Equal(t, optionID, withContext[0].Option.ID)
}

// TestTheCallerCannotOverwriteACartFact proves that the free-form fields the
// caller sends cannot be written over the cart's FACTS.
//
// Had they been able to, a single "subtotal" value coming from the storefront
// would dodge the free-shipping rule.
func TestTheCallerCannotOverwriteACartFact(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Free shipping",
		ShippingProfileID: profileID,
		Amount:            0,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), optionID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	// TrustedFacts=true: what is under test is that even in a TRUSTED context the
	// caller's free-form field cannot overwrite the cart's fact. Without the flag
	// the option would not be listed anyway and the claim would be vacuous.
	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     100,
		Attributes:   map[string]string{service.AttrSubtotal: "999999"},
		TrustedFacts: true,
	})
	require.NoError(t, err)
	assert.Empty(t, options, "the subtotal the caller gave must not overwrite the cart's real subtotal")
}

// TestARuleBoundOptionIsNotListedInAnUntrustedContext proves that the storefront
// surface is not a RULE ORACLE.
//
// Regression: the cart facts (subtotal, item count, weight) arrived at the
// storefront end straight from a query parameter, and this module cannot verify
// them. A customer sending "subtotal=50000" with an empty cart was seeing the free
// shipping option that was CLOSED to them, and its price. Now, in an untrusted
// context, an option with a rule bound to those facts does not enter the list
// even if the fact matches.
func TestARuleBoundOptionIsNotListedInAnUntrustedContext(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field string
		value string
		input service.ListOptionsInput
	}{
		{"subtotal", service.AttrSubtotal, "50000", service.ListOptionsInput{
			CurrencyCode: "TRY", Subtotal: 50_000,
		}},
		{"item count", service.AttrItemCount, "3", service.ListOptionsInput{
			CurrencyCode: "TRY", ItemCount: 3,
		}},
		{"total weight", service.AttrTotalWeight, "1000", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: 1_000,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newSetup(t)
			profileID := setup.createProfile(t, "default")
			openID := setup.createOption(t, service.CreateOptionInput{
				Name:              "Standard shipping",
				ShippingProfileID: profileID,
				Amount:            2_000,
			})
			restrictedID := setup.createOption(t, service.CreateOptionInput{
				Name:              "Rule-bound shipping",
				ShippingProfileID: profileID,
				Amount:            0,
			})
			_, err := setup.svc.CreateShippingOptionRule(context.Background(), restrictedID,
				service.CreateRuleInput{
					Attribute: tc.field,
					Operator:  "gte",
					Values:    []string{tc.value},
				})
			require.NoError(t, err)

			untrusted, err := setup.svc.ListShippingOptionsFor(context.Background(), tc.input)
			require.NoError(t, err)
			require.Len(t, untrusted, 1,
				"an option bound to a fabricable fact must not be listed in an untrusted context")
			assert.Equal(t, openID, untrusted[0].Option.ID)

			// When the same context is marked TRUSTED (the cart flow) the option
			// becomes visible; the filter does not disable the rule, it only
			// applies it according to the surface.
			trusted := tc.input
			trusted.TrustedFacts = true
			listed, err := setup.svc.ListShippingOptionsFor(context.Background(), trusted)
			require.NoError(t, err)
			require.Len(t, listed, 2, "in a trusted context the rule-bound option has to be listed")
			assert.Equal(t, restrictedID, listed[0].Option.ID)
		})
	}
}

// TestAnUntrustedContextDoesNotDropARulelessOption proves that the filter IS NOT
// too broad.
//
// Ruleless (unconditional) options and rules bound to fields that cannot be
// fabricated have to be listed in an untrusted context as well; otherwise the fix
// would empty out the storefront entirely.
func TestAnUntrustedContextDoesNotDropARulelessOption(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	rulelessID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	regionalID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Regional shipping",
		ShippingProfileID: profileID,
		Amount:            1_000,
	})
	_, err := setup.svc.CreateShippingOptionRule(context.Background(), regionalID,
		service.CreateRuleInput{
			Attribute: service.AttrCountryCode,
			Operator:  "eq",
			Values:    []string{"TR"},
		})
	require.NoError(t, err)

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		CountryCode:  "TR",
	})
	require.NoError(t, err)
	require.Len(t, options, 2, "ruleless options and ones bound to a scope field must not drop")
	assert.Equal(t, regionalID, options[0].Option.ID)
	assert.Equal(t, rulelessID, options[1].Option.ID)
}

// TestProfileFilter proves that the options of profiles the cart's products are
// not bound to are not offered; if no profile is given, no filter is applied.
func TestProfileFilter(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	defaultID := setup.createProfile(t, "default")
	heavyID := setup.createProfile(t, "heavy-load")

	standardID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: defaultID,
		Amount:            2_000,
	})
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Heavy freight shipping",
		ShippingProfileID: heavyID,
		Amount:            20_000,
	})

	filtered, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode:       "TRY",
		ShippingProfileIDs: []string{defaultID},
	})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, standardID, filtered[0].Option.ID)

	unfiltered, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Len(t, unfiltered, 2, "if no profile is given no filter has to be applied")
}

// TestRegionFilter proves that an option with an empty region is offered in
// EVERY region, while one that has a region is offered only in its own.
func TestRegionFilter(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	generalID := setup.createOption(t, service.CreateOptionInput{
		Name:              "General shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	regionalID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Regional shipping",
		ShippingProfileID: profileID,
		Amount:            1_000,
		RegionID:          "reg_tr",
	})

	trRegion, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		RegionID:     "reg_tr",
	})
	require.NoError(t, err)
	require.Len(t, trRegion, 2)
	assert.Equal(t, regionalID, trRegion[0].Option.ID, "the cheaper one has to be first")

	deRegion, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		RegionID:     "reg_de",
	})
	require.NoError(t, err)
	require.Len(t, deRegion, 1, "in another region only the region-less option has to be offered")
	assert.Equal(t, generalID, deRegion[0].Option.ID)
}

// TestReturnOptionsAreListedSeparately proves that return options are not
// offered in the normal flow.
func TestReturnOptionsAreListedSeparately(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")

	saleID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	returnID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Return shipping",
		ShippingProfileID: profileID,
		Amount:            0,
		IsReturn:          true,
	})

	sale, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, sale, 1)
	assert.Equal(t, saleID, sale[0].Option.ID)

	returns, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		IsReturn:     true,
	})
	require.NoError(t, err)
	require.Len(t, returns, 1)
	assert.Equal(t, returnID, returns[0].Option.ID)
}

// TestCurrencyFilter proves that an option priced in another currency is not
// offered.
//
// Had it been offered, the amounts of two currencies would be summed in the same
// cart.
func TestCurrencyFilter(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Dollar shipping",
		ShippingProfileID: profileID,
		Amount:            500,
		CurrencyCode:      "USD",
	})

	options, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, options)
}

// TestEligibilityOrderingIsDeterministic proves that options with the same fee
// are ordered BY IDENTIFIER and that the result does not change from call to
// call.
func TestEligibilityOrderingIsDeterministic(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	for _, name := range []string{"A shipping", "B shipping", "C shipping"} {
		setup.createOption(t, service.CreateOptionInput{
			Name:              name,
			ShippingProfileID: profileID,
			Amount:            2_000,
		})
	}

	first, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, first, 3)

	for i := 1; i < len(first); i++ {
		assert.Less(t, first[i-1].Option.ID, first[i].Option.ID,
			"at an equal fee the ordering has to be ascending by identifier")
	}

	second, err := setup.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	for i := range first {
		assert.Equal(t, first[i].Option.ID, second[i].Option.ID, "the order must not change from call to call")
	}
}

// TestEligibilityInputIsValidated proves that an invalid input is rejected with
// errors.Invalid.
func TestEligibilityInputIsValidated(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input service.ListOptionsInput
	}{
		{"no currency", service.ListOptionsInput{}},
		{"malformed currency", service.ListOptionsInput{CurrencyCode: "TR"}},
		{"negative subtotal", service.ListOptionsInput{CurrencyCode: "TRY", Subtotal: -1}},
		{"negative item count", service.ListOptionsInput{CurrencyCode: "TRY", ItemCount: -1}},
		{"negative weight", service.ListOptionsInput{CurrencyCode: "TRY", TotalWeight: -1}},
		// The UPPER bounds: a check that only looks at negativity would let a
		// single query parameter overflow the provider's product.
		{"subtotal exceeds the upper bound", service.ListOptionsInput{
			CurrencyCode: "TRY", Subtotal: models.MaxAmount + 1,
		}},
		{"item count exceeds the upper bound", service.ListOptionsInput{
			CurrencyCode: "TRY", ItemCount: models.MaxItemCount + 1,
		}},
		{"weight exceeds the upper bound", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: models.MaxTotalWeight + 1,
		}},
		{"weight at the top of int64", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: math.MaxInt64,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newSetup(t)
			_, err := setup.svc.ListShippingOptionsFor(context.Background(), tc.input)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
		})
	}
}

// TestAnUnregisteredProvidersOptionDrops proves that the option of a provider
// whose registration went missing afterwards drops out of the list and that the
// request does not drop.
//
// The option is opened with a REGISTERED provider, then the service is rebuilt
// with a new registry that does not know that provider; a setup error looks
// exactly like this.
func TestAnUnregisteredProvidersOptionDrops(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	flatID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
	})
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Missing provider shipping",
		ShippingProfileID: profileID,
		PriceType:         "calculated",
	})

	emptyRegistry := service.NewProviderRegistry()
	svcWithoutProvider, err := service.New(service.Options{Store: setup.store, Providers: emptyRegistry})
	require.NoError(t, err)

	options, err := svcWithoutProvider.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err, "a missing provider must not drop the whole request")
	require.Len(t, options, 1)
	assert.Equal(t, flatID, options[0].Option.ID)
}

// TestABrokenRuleDoesNotOpenTheOptionToEveryone proves that the eligibility
// calculation is resilient to EVERY row it reads from the database.
//
// Service validation does not produce a valueless rule or one with an
// unrecognized operator; but a maintenance script running SQL directly, or a
// partial restore, can leave such a row behind. A condition that cannot be read
// MUST NOT quietly disable the rule and OPEN THE OPTION TO EVERYONE — which is
// why a broken rule DOES NOT MATCH.
//
// The rules are written straight into the fake store; an input passing through
// the service's door could never produce these rows.
func TestABrokenRuleDoesNotOpenTheOptionToEveryone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		broken models.ShippingOptionRule
	}{
		{"valueless rule", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.OpGte, Values: nil,
		}},
		{"unrecognized operator", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.RuleOperator("like"),
			Values: []string{"1"},
		}},
		{"text value for a numeric operator", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.OpGte,
			Values: []string{"fifty thousand"},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := newSetup(t)
			profileID := setup.createProfile(t, "default")
			optionID := setup.createOption(t, service.CreateOptionInput{
				Name:              "Restricted shipping",
				ShippingProfileID: profileID,
				Amount:            2_000,
			})

			rule := tc.broken
			rule.ID = models.NewShippingOptionRuleID()
			rule.ShippingOptionID = optionID
			setup.store.mu.Lock()
			setup.store.rules[rule.ID] = rule
			setup.store.mu.Unlock()

			options, err := setup.svc.ListShippingOptionsFor(context.Background(),
				service.ListOptionsInput{CurrencyCode: "TRY", Subtotal: 999_999, TrustedFacts: true})
			require.NoError(t, err, "a broken rule must not produce a PANIC or an error")
			assert.Empty(t, options, "a condition that cannot be read must not open the option")
		})
	}
}
