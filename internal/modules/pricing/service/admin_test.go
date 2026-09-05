package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// adminFixture wires the surface to a repository whose ReplacePrices input is
// captured.
//
// The assertion target is what gets WRITTEN, not what a fake store keeps: the
// claim this surface makes is about the payload handed to a destructive
// replace, and a store that quietly normalised it would hide exactly the bug
// being guarded against.
type adminFixture struct {
	surface *AdminSurface
	repo    *stubRepo
	written []models.Price
}

func newAdminFixture(t *testing.T, existing []models.Price) *adminFixture {
	t.Helper()

	fx := &adminFixture{repo: newStubRepo()}
	fx.repo.getPriceSetFn = func(_ context.Context, id string) (models.PriceSet, error) {
		return models.PriceSet{ID: id}, nil
	}
	fx.repo.listPricesFn = func(context.Context, string) ([]models.Price, error) {
		return existing, nil
	}
	fx.repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		fx.written = prices
		return prices, nil
	}
	fx.surface = NewAdminSurface(newTestService(fx.repo))

	return fx
}

// listedPrice builds a stored price.
func listedPrice(currency string, amount int64, listID *string, rules []models.PriceRule) models.Price {
	return models.Price{
		ID:           "price_" + currency,
		PriceSetID:   "pset_1",
		CurrencyCode: currency,
		Amount:       amount,
		MinQuantity:  models.MinQuantity,
		PriceListID:  listID,
		Rules:        rules,
	}
}

// TestSetBasePriceAmountKeepsEveryOtherPrice is the reason this surface exists.
//
// The module's only price writer is a DESTRUCTIVE replace, and the panel reads
// only the listable prices — a campaign price and a rule-bearing price are
// filtered out before the operator ever sees them. An edit form built on the
// plain writer would delete both, and nothing in the response would say so.
//
// The assertion is therefore not "the amount changed" but "everything else was
// written back", which is the half a naive implementation gets wrong.
func TestSetBasePriceAmountKeepsEveryOtherPrice(t *testing.T) {
	t.Parallel()

	listID := "plist_autumn"
	fx := newAdminFixture(t, []models.Price{
		listedPrice("TRY", 19990, nil, nil),
		listedPrice("USD", 999, nil, nil),
		listedPrice("TRY", 14990, &listID, nil),
		listedPrice("TRY", 9990, nil, []models.PriceRule{
			{Attribute: "customer_group", Operator: models.OpEq, Values: []string{"vip"}},
		}),
	})

	require.NoError(t, fx.surface.SetBasePriceAmount(context.Background(), "pset_1", "TRY", 24990))

	require.Len(t, fx.written, 4,
		"no price may be dropped; the writer underneath REPLACES the whole set")

	var base, campaign, ruled, usd int64
	for _, price := range fx.written {
		switch {
		case price.CurrencyCode == "USD":
			usd = price.Amount
		case price.PriceListID != nil:
			campaign = price.Amount
		case len(price.Rules) > 0:
			ruled = price.Amount
		default:
			base = price.Amount
		}
	}

	assert.Equal(t, int64(24990), base, "the base price must carry the new amount")
	assert.Equal(t, int64(14990), campaign, "a price on a list must be written back untouched")
	assert.Equal(t, int64(9990), ruled, "a rule-bearing price must be written back untouched")
	assert.Equal(t, int64(999), usd, "another currency must be written back untouched")
}

// TestSetBasePriceAmountPreservesRuleContent proves the rules themselves round
// trip, not just the rows carrying them.
//
// The write path rebuilds every price from an input struct. A value dropped in
// that conversion would delete the condition while keeping the price, which is
// worse than deleting both: a segment price would silently become a price for
// everyone.
func TestSetBasePriceAmountPreservesRuleContent(t *testing.T) {
	t.Parallel()

	fx := newAdminFixture(t, []models.Price{
		listedPrice("TRY", 19990, nil, nil),
		listedPrice("TRY", 9990, nil, []models.PriceRule{
			{Attribute: "customer_group", Operator: models.OpIn, Values: []string{"vip", "staff"}},
		}),
	})

	require.NoError(t, fx.surface.SetBasePriceAmount(context.Background(), "pset_1", "TRY", 24990))

	var rules []models.PriceRule
	for _, price := range fx.written {
		if len(price.Rules) > 0 {
			rules = price.Rules
		}
	}
	require.Len(t, rules, 1, "the rule must survive the rewrite")
	assert.Equal(t, "customer_group", rules[0].Attribute)
	assert.Equal(t, models.OpIn, rules[0].Operator)
	assert.ElementsMatch(t, []string{"vip", "staff"}, rules[0].Values,
		"a dropped value would turn a segment price into a price for everyone")
}

// TestSetBasePriceAmountAddsAMissingCurrency proves a set with no base price in
// that currency gains one.
//
// Without it the form could not give a price to a set that only had campaign
// prices: the operator would type into an empty box and saving would do
// nothing.
func TestSetBasePriceAmountAddsAMissingCurrency(t *testing.T) {
	t.Parallel()

	fx := newAdminFixture(t, []models.Price{listedPrice("TRY", 19990, nil, nil)})

	require.NoError(t, fx.surface.SetBasePriceAmount(context.Background(), "pset_1", "usd", 999))

	require.Len(t, fx.written, 2)

	found := false
	for _, price := range fx.written {
		if price.CurrencyCode == "USD" {
			found = true
			assert.Equal(t, int64(999), price.Amount)
			assert.Nil(t, price.PriceListID, "an added price must be a BASE price")
			assert.Empty(t, price.Rules, "an added price must carry no rules")
		}
	}
	assert.True(t, found, "a lower-case currency code must be normalized, not rejected")
}

// TestANewPriceCarriesTheNormalizedCurrency proves the code is cleaned before
// it becomes a stored row.
//
// Only the ADDING path can show this. On the replacing path the match is
// case-insensitive, so a padded or lower-case code still finds the base price
// and the stored row keeps whatever case it already had. When there is nothing
// to match, the code the caller typed is the code that gets WRITTEN — and
// "try " would become a currency that no lookup ever finds again.
func TestANewPriceCarriesTheNormalizedCurrency(t *testing.T) {
	t.Parallel()

	fx := newAdminFixture(t, nil)

	require.NoError(t, fx.surface.SetBasePriceAmount(context.Background(), "pset_1", " try ", 24990))

	require.Len(t, fx.written, 1)
	assert.Equal(t, "TRY", fx.written[0].CurrencyCode)
	assert.Equal(t, int64(24990), fx.written[0].Amount)
}

// TestAPaddedCurrencyStillFindsTheBasePrice proves the code is cleaned BEFORE
// it is matched, not only before it is stored.
//
// The match is case-insensitive but not space-insensitive. Feeding " try "
// through unpadded would fail to recognize the TRY base price, fall into the
// adding branch, and leave the set with TWO base prices in the same currency —
// a state the module has no way to resolve and the form no way to show.
func TestAPaddedCurrencyStillFindsTheBasePrice(t *testing.T) {
	t.Parallel()

	fx := newAdminFixture(t, []models.Price{listedPrice("TRY", 19990, nil, nil)})

	require.NoError(t, fx.surface.SetBasePriceAmount(context.Background(), "pset_1", " try ", 24990))

	require.Len(t, fx.written, 1, "the set must not gain a second base price in one currency")
	assert.Equal(t, int64(24990), fx.written[0].Amount)
}

// TestSetBasePriceAmountRejectsAnEmptyCurrency proves the surface refuses input
// the set would otherwise absorb.
func TestSetBasePriceAmountRejectsAnEmptyCurrency(t *testing.T) {
	t.Parallel()

	fx := newAdminFixture(t, nil)

	err := fx.surface.SetBasePriceAmount(context.Background(), "pset_1", "  ", 100)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, fx.written, "nothing may be written when the input is refused")
}

// TestPricingAdminSurfaceIsNilSafe proves a surface with no service answers
// instead of panicking.
func TestPricingAdminSurfaceIsNilSafe(t *testing.T) {
	t.Parallel()

	var surface *AdminSurface

	err := surface.SetBasePriceAmount(context.Background(), "pset_1", "TRY", 100)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}
