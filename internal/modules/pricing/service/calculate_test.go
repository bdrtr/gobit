package service

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// This file proves EVERY BRANCH of CalculatePrice's selection rule.
//
// The tests' shared design principle: in the scenario where one criterion is
// proven, ALL THE OTHER criteria favor the loser. That way, when a criterion is
// removed from the code, the test inevitably fails — this is how "a test that
// passes for the wrong reason" is prevented.

// TestSelectPrefersOverrideOverSale proves that list precedence is the first
// criterion: override beats sale.
//
// The loser (sale) is CHEAPER and has a NARROWER range; that is, every later
// criterion favors it. The winner can win only through list precedence.
func TestSelectPrefersOverrideOverSale(t *testing.T) {
	sale := withList(basePrice("price_a", "TRY", 1000, 5, ptr(int32(6))), "plist_sale",
		activeList("plist_sale", models.PriceListSale))
	override := withList(basePrice("price_b", "TRY", 9000, 1, nil), "plist_ovr",
		activeList("plist_ovr", models.PriceListOverride))

	got, ok := selectPrice([]models.PriceCandidate{sale, override}, "TRY", 5, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "the override list has to beat sale")
	assert.Equal(t, models.PriceListOverride, got.PriceListType)
}

// TestSelectPrefersListOverBase proves that a list price beats the base price.
// The base price is cheaper; only the precedence criterion can decide.
func TestSelectPrefersListOverBase(t *testing.T) {
	base := basePrice("price_a", "TRY", 1000, 1, nil)
	sale := withList(basePrice("price_b", "TRY", 9000, 1, nil), "plist_sale",
		activeList("plist_sale", models.PriceListSale))

	got, ok := selectPrice([]models.PriceCandidate{base, sale}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "the campaign list has to beat the base price")
}

// TestSelectPrefersMoreSpecificRules proves that the number of rules is the
// second criterion.
//
// Both candidates are in the SAME list (equal precedence). The candidate with
// more rules is MORE EXPENSIVE and has a WIDER range; the span and amount
// criteria favor the one with fewer rules.
func TestSelectPrefersMoreSpecificRules(t *testing.T) {
	attrs := map[string]string{"region_id": "reg_1", "customer_group_id": "vip"}

	single := withRules(basePrice("price_a", "TRY", 1000, 5, ptr(int32(6))),
		rule("region_id", models.OpEq, "reg_1"))
	double := withRules(basePrice("price_b", "TRY", 9000, 1, nil),
		rule("region_id", models.OpEq, "reg_1"),
		rule("customer_group_id", models.OpEq, "vip"))

	got, ok := selectPrice([]models.PriceCandidate{single, double}, "TRY", 5, attrs, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "the price that satisfies more rules is more specific")
	assert.Equal(t, 2, got.MatchedRules)
}

// TestSelectPrefersNarrowerQuantityRange proves that the width of the range is
// the third criterion.
//
// The number of rules and the list precedence are equal; the candidate with the
// narrower range is MORE EXPENSIVE, that is, the amount criterion favors the
// wide one.
func TestSelectPrefersNarrowerQuantityRange(t *testing.T) {
	wide := basePrice("price_a", "TRY", 1000, 1, nil)
	narrow := basePrice("price_b", "TRY", 9000, 10, ptr(int32(20)))

	got, ok := selectPrice([]models.PriceCandidate{wide, narrow}, "TRY", 10, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "the narrow quantity range has to make the wholesale tier win")
}

// TestSelectPrefersLowerAmount proves that the amount criterion comes fourth.
// Every other criterion is equal.
func TestSelectPrefersLowerAmount(t *testing.T) {
	expensive := basePrice("price_a", "TRY", 9000, 1, ptr(int32(10)))
	cheap := basePrice("price_b", "TRY", 1000, 1, ptr(int32(10)))

	got, ok := selectPrice([]models.PriceCandidate{expensive, cheap}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "between equivalent candidates the decision has to favor the customer")
}

// TestSelectIsDeterministicOnFullTie proves that on a full tie the id decides,
// and that the result is independent of the ORDER IN WHICH the candidates
// arrive.
func TestSelectIsDeterministicOnFullTie(t *testing.T) {
	first := basePrice("price_a", "TRY", 1000, 1, ptr(int32(10)))
	second := basePrice("price_b", "TRY", 1000, 1, ptr(int32(10)))

	forward, ok := selectPrice([]models.PriceCandidate{first, second}, "TRY", 1, nil, testNow)
	require.True(t, ok)
	backward, ok := selectPrice([]models.PriceCandidate{second, first}, "TRY", 1, nil, testNow)
	require.True(t, ok)

	assert.Equal(t, "price_a", forward.PriceID)
	assert.Equal(t, forward.PriceID, backward.PriceID, "the result has to be independent of the candidate order")
}

// TestSelectFiltersByCurrency proves the currency elimination.
func TestSelectFiltersByCurrency(t *testing.T) {
	usd := basePrice("price_usd", "USD", 100, 1, nil)
	try := basePrice("price_try", "TRY", 5000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{usd, try}, "TRY", 1, nil, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_try", got.PriceID, "a cheaper price in another currency cannot be selected")

	_, ok = selectPrice([]models.PriceCandidate{usd}, "EUR", 1, nil, testNow)
	assert.False(t, ok, "if no currency matches at all, no candidate may remain")
}

// TestSelectFiltersByQuantityRange proves the quantity range elimination at its
// edge values; the bounds are INCLUSIVE.
func TestSelectFiltersByQuantityRange(t *testing.T) {
	tiered := basePrice("price_tier", "TRY", 1000, 10, ptr(int32(20)))
	candidates := []models.PriceCandidate{tiered}

	for _, tc := range []struct {
		name     string
		quantity int32
		want     bool
	}{
		{"below the lower bound", 9, false},
		{"at the lower bound", 10, true},
		{"inside the range", 15, true},
		{"at the upper bound", 20, true},
		{"above the upper bound", 21, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := selectPrice(candidates, "TRY", tc.quantity, nil, testNow)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectSkipsUnusablePriceLists proves that only lists whose status is
// active can offer a price.
func TestSelectSkipsUnusablePriceLists(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status models.PriceListStatus
		want   bool
	}{
		{"a draft list offers no price", models.PriceListDraft, false},
		{"an expired list offers no price", models.PriceListExpired, false},
		{"a published list offers a price", models.PriceListActive, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &models.PriceListInfo{ID: "plist_1", Type: models.PriceListSale, Status: tc.status}
			candidate := withList(basePrice("price_a", "TRY", 1000, 1, nil), "plist_1", info)

			_, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 1, nil, testNow)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectHonoursPriceListWindow proves the edges of the date window. The
// edges are INCLUSIVE: the list is valid at the exact start and the exact end.
func TestSelectHonoursPriceListWindow(t *testing.T) {
	starts := testNow.Add(-time.Hour)
	ends := testNow.Add(time.Hour)

	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before the start", starts.Add(-time.Second), false},
		{"exactly at the start", starts, true},
		{"inside the window", testNow, true},
		{"exactly at the end", ends, true},
		{"after the end", ends.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &models.PriceListInfo{
				ID:       "plist_1",
				Type:     models.PriceListSale,
				Status:   models.PriceListActive,
				StartsAt: &starts,
				EndsAt:   &ends,
			}
			candidate := withList(basePrice("price_a", "TRY", 1000, 1, nil), "plist_1", info)

			_, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 1, nil, tc.at)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectSkipsPriceWithDeletedList proves that a price whose list was deleted
// (the list id is set but there is no metadata) is eliminated.
func TestSelectSkipsPriceWithDeletedList(t *testing.T) {
	orphan := withList(basePrice("price_orphan", "TRY", 100, 1, nil), "plist_gone", nil)
	base := basePrice("price_base", "TRY", 5000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{orphan, base}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_base", got.PriceID, "a price whose list was deleted must not be counted")
}

// TestSelectRequiresAllRulesToMatch proves that the rules are combined with AND.
func TestSelectRequiresAllRulesToMatch(t *testing.T) {
	candidate := withRules(basePrice("price_a", "TRY", 100, 1, nil),
		rule("region_id", models.OpEq, "reg_1"),
		rule("customer_group_id", models.OpEq, "vip"))
	candidates := []models.PriceCandidate{candidate}

	_, ok := selectPrice(candidates, "TRY", 1,
		map[string]string{"region_id": "reg_1", "customer_group_id": "vip"}, testNow)
	assert.True(t, ok, "when every rule is satisfied the price has to be valid")

	_, ok = selectPrice(candidates, "TRY", 1,
		map[string]string{"region_id": "reg_1", "customer_group_id": "normal"}, testNow)
	assert.False(t, ok, "if even a single rule is not satisfied, the price has to be eliminated")
}

// TestSelectComputesTotal proves that the result's total field is amount ×
// quantity.
func TestSelectComputesTotal(t *testing.T) {
	candidate := basePrice("price_a", "TRY", 1250, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 4, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, int64(1250), got.Amount)
	assert.Equal(t, int32(4), got.Quantity)
	assert.Equal(t, int64(5000), got.Total)
}

// TestSelectReturnsNoCandidate states that no selection can be made from an
// empty set of candidates.
func TestSelectReturnsNoCandidate(t *testing.T) {
	_, ok := selectPrice(nil, "TRY", 1, nil, testNow)
	assert.False(t, ok)
}

// TestQuantitySpanUnbounded proves that a range without an upper bound counts
// as the maximum width; the "the narrow one wins" rule rests on this.
func TestQuantitySpanUnbounded(t *testing.T) {
	assert.Equal(t, int64(math.MaxInt64), quantitySpan(models.Price{MinQuantity: 1}))
	assert.Equal(t, int64(9), quantitySpan(models.Price{MinQuantity: 1, MaxQuantity: ptr(int32(10))}))
}

// TestMatchRuleOperators proves both the matching and the non-matching branch
// of every operator.
func TestMatchRuleOperators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rule  models.PriceRule
		attrs map[string]string
		want  bool
	}{
		{"eq matches", rule("k", models.OpEq, "a"), map[string]string{"k": "a"}, true},
		{"eq does not match", rule("k", models.OpEq, "a"), map[string]string{"k": "b"}, false},
		{"ne matches", rule("k", models.OpNe, "a"), map[string]string{"k": "b"}, true},
		{"ne does not match", rule("k", models.OpNe, "a"), map[string]string{"k": "a"}, false},
		{"in matches", rule("k", models.OpIn, "a", "b"), map[string]string{"k": "b"}, true},
		{"in does not match", rule("k", models.OpIn, "a", "b"), map[string]string{"k": "c"}, false},
		{"nin matches", rule("k", models.OpNin, "a", "b"), map[string]string{"k": "c"}, true},
		{"nin does not match", rule("k", models.OpNin, "a", "b"), map[string]string{"k": "a"}, false},
		{"gt matches", rule("k", models.OpGt, "10"), map[string]string{"k": "11"}, true},
		{"gt does not match at the bound", rule("k", models.OpGt, "10"), map[string]string{"k": "10"}, false},
		{"gte matches at the bound", rule("k", models.OpGte, "10"), map[string]string{"k": "10"}, true},
		{"gte does not match", rule("k", models.OpGte, "10"), map[string]string{"k": "9"}, false},
		{"lt matches", rule("k", models.OpLt, "10"), map[string]string{"k": "9"}, true},
		{"lt does not match at the bound", rule("k", models.OpLt, "10"), map[string]string{"k": "10"}, false},
		{"lte matches at the bound", rule("k", models.OpLte, "10"), map[string]string{"k": "10"}, true},
		{"lte does not match", rule("k", models.OpLte, "10"), map[string]string{"k": "11"}, false},

		{"a field absent from the context does not match", rule("k", models.OpEq, "a"), map[string]string{"x": "a"}, false},
		{"a negative operator does not match an absent field either",
			rule("k", models.OpNe, "a"), map[string]string{"x": "a"}, false},
		{"a numeric operator does not match a text context",
			rule("k", models.OpGt, "10"), map[string]string{"k": "abc"}, false},
		{"an unrecognized operator does not match",
			rule("k", models.RuleOperator("regex"), "a"), map[string]string{"k": "a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, matchRule(tc.rule, tc.attrs))
		})
	}
}

// TestMatchRulesEmptyIsUnconditional proves that a price without rules is
// unconditional.
func TestMatchRulesEmptyIsUnconditional(t *testing.T) {
	assert.True(t, matchRules(nil, nil))
	assert.True(t, matchRules([]models.PriceRule{}, map[string]string{"k": "v"}))
}

// TestCalculatePriceUsesServiceClock proves that, when At is not given, the
// service's clock is used: a list that falls outside its window is
// eliminated.
func TestCalculatePriceUsesServiceClock(t *testing.T) {
	ended := testNow.Add(-time.Minute)
	info := &models.PriceListInfo{
		ID:     "plist_1",
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
		EndsAt: &ended,
	}

	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{
			withList(basePrice("price_sale", "TRY", 100, 1, nil), "plist_1", info),
			basePrice("price_base", "TRY", 500, 1, nil),
		}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Equal(t, "price_base", got.PriceID, "an expired campaign must not be selected")
}

// TestCalculatePriceNormalizesCurrency proves that a lower-case currency is
// raised to upper case.
func TestCalculatePriceNormalizesCurrency(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 100, 1, nil)}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: " try "})

	require.NoError(t, err)
	assert.Equal(t, "TRY", got.CurrencyCode)
}

// TestCalculatePriceDefaultsQuantityToOne proves that, when no quantity is
// given, 1 is assumed.
func TestCalculatePriceDefaultsQuantityToOne(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 700, 1, nil)}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Equal(t, int32(1), got.Quantity)
	assert.Equal(t, int64(700), got.Total)
}

// TestCalculatePriceDistinguishesMissingSetFromMissingPrice proves that the two
// NotFound cases come back with DIFFERENT codes: no container vs an empty
// container.
func TestCalculatePriceDistinguishesMissingSetFromMissingPrice(t *testing.T) {
	t.Run("no container", func(t *testing.T) {
		repo := newStubRepo()
		repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
			return nil, nil
		}
		repo.getPriceSetFn = func(context.Context, string) (models.PriceSet, error) {
			return models.PriceSet{}, errors.NotFound("price_set_not_found", "missing")
		}

		_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
			CalculateParams{CurrencyCode: "TRY"})

		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, "price_set_not_found", errors.CodeOf(err))
	})

	t.Run("the container exists but has no price", func(t *testing.T) {
		repo := newStubRepo()
		repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
			return nil, nil
		}
		repo.getPriceSetFn = func(_ context.Context, id string) (models.PriceSet, error) {
			return models.PriceSet{ID: id}, nil
		}

		_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
			CalculateParams{CurrencyCode: "TRY"})

		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeNotCalculable, errors.CodeOf(err))
	})
}

// TestCalculatePriceSkipsExistenceCheckWhenPricesExist proves that the happy
// path makes a SINGLE round trip; the second query is opened only on an empty
// result.
func TestCalculatePriceSkipsExistenceCheckWhenPricesExist(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 100, 1, nil)}, nil
	}

	_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Zero(t, repo.calls["GetPriceSet"], "while there is a price, the container's existence must not be asked separately")
	assert.Equal(t, 1, repo.calls["ListPriceCandidates"])
}

// TestCalculatePriceRejectsBadInput proves that the input validation runs
// BEFORE anything REACHES the database.
func TestCalculatePriceRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setID  string
		params CalculateParams
	}{
		{"wrong id prefix", "variant_1", CalculateParams{CurrencyCode: "TRY"}},
		{"empty id", "", CalculateParams{CurrencyCode: "TRY"}},
		{"missing currency", "pset_1", CalculateParams{}},
		{"four-letter currency", "pset_1", CalculateParams{CurrencyCode: "TRYX"}},
		{"currency not letters", "pset_1", CalculateParams{CurrencyCode: "T1L"}},
		{"negative quantity", "pset_1", CalculateParams{CurrencyCode: "TRY", Quantity: -1}},
		{"quantity above the bound", "pset_1",
			CalculateParams{CurrencyCode: "TRY", Quantity: models.MaxQuantity + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()

			_, err := newTestService(repo).CalculatePrice(context.Background(), tc.setID, tc.params)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Empty(t, repo.calls, "invalid input must never reach the repository")
		})
	}
}

// TestCalculateAmountMatchesCalculatePrice proves that the narrow cross-module
// surface uses the same selection rule.
func TestCalculateAmountMatchesCalculatePrice(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{
			basePrice("price_wide", "TRY", 1000, 1, nil),
			basePrice("price_tier", "TRY", 800, 10, ptr(int32(20))),
		}, nil
	}
	svc := newTestService(repo)

	amount, err := svc.CalculateAmount(context.Background(), "pset_1", "TRY", 10, nil)
	require.NoError(t, err)

	full, err := svc.CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY", Quantity: 10})
	require.NoError(t, err)

	assert.Equal(t, full.Amount, amount)
	assert.Equal(t, int64(800), amount)
}

// TestMatchRuleWithoutValuesDoesNotMatch proves that a rule without values is
// counted as not matching, WITHOUT PANICKING.
//
// The service validation rejects an empty value list, but the CHECK constraint
// in the database is not a sufficient gate on its own (see migration 000002),
// and a maintenance script running SQL directly can produce such a row. If the
// rule's value cannot be read, the right behavior has the same reason as for an
// unrecognized operator: the rule must not silently switch itself off and OPEN
// the price to everyone. That is why the "nin" and "ne" branches are proven
// separately — without the bounds check, both would count a valueless rule as
// SATISFIED.
func TestMatchRuleWithoutValuesDoesNotMatch(t *testing.T) {
	for _, op := range []models.RuleOperator{
		models.OpEq, models.OpNe, models.OpIn, models.OpNin,
		models.OpGt, models.OpGte, models.OpLt, models.OpLte,
	} {
		t.Run(string(op), func(t *testing.T) {
			empty := models.PriceRule{Attribute: "k", Operator: op, Values: []string{}}
			assert.False(t, matchRule(empty, map[string]string{"k": "10"}),
				"a valueless rule must not match")

			nilValues := models.PriceRule{Attribute: "k", Operator: op}
			assert.False(t, matchRule(nilValues, map[string]string{"k": "10"}),
				"a rule with nil values must not match")
		})
	}
}

// TestSelectSkipsPriceWithValuelessRule proves that a price with a valueless
// rule never enters the selection and does not bring the calculation down.
//
// The candidate with the valueless rule is CHEAPER and MORE SPECIFIC; were it
// not eliminated, it would win.
func TestSelectSkipsPriceWithValuelessRule(t *testing.T) {
	broken := withRules(basePrice("price_a", "TRY", 1, 1, nil),
		models.PriceRule{Attribute: "region_id", Operator: models.OpEq})
	base := basePrice("price_b", "TRY", 10000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{broken, base}, "TRY", 1,
		map[string]string{"region_id": "reg_1"}, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "a price with a valueless rule has to be eliminated")
}

// batchItem is a single item of the batch price request in these tests.
type batchItem struct {
	setID    string
	quantity int32
}

// batchFixture returns price containers that test EVERY dimension of the
// selection rule: a plain base price, a quantity tier, a campaign list that
// beats the base, a rule that fits only a single region, a container whose only
// price is in another currency, and an empty container.
//
// The equivalence test below is worth only as much as this fixture: if a
// container here cannot tell two selection rules apart, the test would pass
// for both.
func batchFixture() map[string][]models.PriceCandidate {
	return map[string][]models.PriceCandidate{
		"pset_base": {basePrice("price_base", "TRY", 1000, 1, nil)},
		"pset_tier": {
			basePrice("price_wide", "TRY", 1000, 1, nil),
			basePrice("price_tier", "TRY", 800, 10, ptr(int32(20))),
		},
		"pset_sale": {
			basePrice("price_plain", "TRY", 5000, 1, nil),
			withList(basePrice("price_sale", "TRY", 9000, 1, nil), "plist_sale",
				activeList("plist_sale", models.PriceListSale)),
		},
		"pset_rule": {
			basePrice("price_any", "TRY", 7000, 1, nil),
			withRules(basePrice("price_region", "TRY", 6000, 1, nil),
				models.PriceRule{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}}),
		},
		"pset_other_currency": {basePrice("price_usd", "USD", 100, 1, nil)},
		"pset_empty":          {},
	}
}

// batchRepo answers both candidate queries from the SAME fixture; that is what
// makes the two paths comparable.
func batchRepo(fixture map[string][]models.PriceCandidate) *stubRepo {
	repo := newStubRepo()
	repo.listCandidatesFn = func(_ context.Context, id string) ([]models.PriceCandidate, error) {
		return fixture[id], nil
	}
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		out := make(map[string][]models.PriceCandidate, len(ids))
		for _, id := range ids {
			out[id] = fixture[id]
		}
		return out, nil
	}
	repo.getPriceSetFn = func(_ context.Context, id string) (models.PriceSet, error) {
		if _, ok := fixture[id]; !ok {
			return models.PriceSet{}, errors.NotFound(CodeInvalidInput, "price set missing: %s", id)
		}
		return models.PriceSet{ID: id}, nil
	}
	return repo
}

// callBatch encodes the request, calls the batch surface and decodes the
// response.
func callBatch(t *testing.T, svc *Service, currency string, attrs map[string]string, items []batchItem) calculateAmountsResponse {
	t.Helper()

	req := calculateAmountsRequest{CurrencyCode: currency, Attributes: attrs}
	for _, item := range items {
		req.Items = append(req.Items, calculateAmountsItem{PriceSetID: item.setID, Quantity: item.quantity})
	}
	payload, err := json.Marshal(req)
	require.NoError(t, err)

	raw, err := svc.CalculateAmountsJSON(context.Background(), payload)
	require.NoError(t, err)

	var resp calculateAmountsResponse
	require.NoError(t, json.Unmarshal(raw, &resp))
	return resp
}

// TestCalculateAmountsJSONMatchesCalculateAmount proves that the batch surface
// selects the SAME amount the per-item surface selects.
//
// The cart calculation's move to a batch read rests on this claim: a batch read
// that selected a different price would charge the customer a different amount,
// and no check downstream could see it — the totals are internally consistent
// in both cases.
func TestCalculateAmountsJSONMatchesCalculateAmount(t *testing.T) {
	fixture := batchFixture()
	svc := newTestService(batchRepo(fixture))
	attrs := map[string]string{"region_id": "reg_1"}
	items := []batchItem{
		{"pset_base", 1},
		{"pset_tier", 10},
		{"pset_tier", 1},
		{"pset_sale", 3},
		{"pset_rule", 2},
		{"pset_other_currency", 1},
		{"pset_empty", 1},
		{"pset_missing", 1},
	}

	resp := callBatch(t, svc, "TRY", attrs, items)

	require.Len(t, resp.Items, len(items))
	var priced int
	for i, item := range items {
		amount, err := svc.CalculateAmount(context.Background(), item.setID, "TRY", item.quantity, attrs)
		if err != nil {
			require.True(t, errors.IsNotFound(err), "%s: %v", item.setID, err)
			assert.False(t, resp.Items[i].Priced,
				"%s shows as unpriced on the single path and priced on the batch path", item.setID)
			assert.Zero(t, resp.Items[i].Amount, "an unpriced item's amount is zero")
			continue
		}
		priced++
		assert.True(t, resp.Items[i].Priced, "%s is priced on the single path", item.setID)
		assert.Equal(t, amount, resp.Items[i].Amount, "%s (quantity %d)", item.setID, item.quantity)
	}
	require.Equal(t, 5, priced, "the fixture's priced items have to really be priced")

	// The tier and the campaign list have to have REALLY changed the response;
	// otherwise the equivalence above would hold for a fixture that proves
	// nothing as well.
	assert.Equal(t, int64(800), resp.Items[1].Amount, "the quantity tier has to be selected")
	assert.Equal(t, int64(1000), resp.Items[2].Amount, "outside the tier the wide price has to be selected")
	assert.Equal(t, int64(9000), resp.Items[3].Amount, "the campaign list has to beat the base price")
	assert.Equal(t, int64(6000), resp.Items[4].Amount, "the price whose region rule matches has to be selected")
}

// TestCalculateAmountsJSONWithoutAttributesMatchesCalculateAmount proves that
// the two paths give the same answer when there is NO rule context as well: the
// ruled price has to be eliminated on both.
func TestCalculateAmountsJSONWithoutAttributesMatchesCalculateAmount(t *testing.T) {
	fixture := batchFixture()
	svc := newTestService(batchRepo(fixture))

	resp := callBatch(t, svc, "TRY", nil, []batchItem{{"pset_rule", 2}})

	amount, err := svc.CalculateAmount(context.Background(), "pset_rule", "TRY", 2, nil)
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, amount, resp.Items[0].Amount)
	assert.Equal(t, int64(7000), resp.Items[0].Amount, "in a request without a context the ruled price has to be eliminated")
}

// TestCalculateAmountsJSONReadsCandidatesOnce proves the batch surface's reason
// to exist: a single repository read, INDEPENDENT of the number of items.
//
// The per-item path opens two queries for every item (candidates + rules); this
// path opens two in total. Measured against gobit_load: 100 containers one by
// one 9.9 ms, in a batch 0.33 ms (see the method's godoc).
func TestCalculateAmountsJSONReadsCandidatesOnce(t *testing.T) {
	fixture := batchFixture()
	repo := batchRepo(fixture)
	var asked []string
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		asked = append([]string(nil), ids...)
		out := make(map[string][]models.PriceCandidate, len(ids))
		for _, id := range ids {
			out[id] = fixture[id]
		}
		return out, nil
	}
	svc := newTestService(repo)

	resp := callBatch(t, svc, "TRY", nil, []batchItem{
		{"pset_base", 1}, {"pset_tier", 10}, {"pset_base", 5}, {"pset_tier", 1},
	})

	require.Len(t, resp.Items, 4)
	assert.Equal(t, 1, repo.calls["ListPriceCandidatesBySets"], "a single read, not one per item")
	assert.Zero(t, repo.calls["ListPriceCandidates"], "the single candidate query must not be used")
	assert.Equal(t, []string{"pset_base", "pset_tier"}, asked, "the same container must not be asked for twice")

	// Deduplicating the read must not deduplicate the RESPONSE: the same
	// container asked for at two different quantities can fall into two
	// different tiers.
	assert.Equal(t, int64(1000), resp.Items[0].Amount)
	assert.Equal(t, int64(800), resp.Items[1].Amount)
	assert.Equal(t, int64(1000), resp.Items[2].Amount)
	assert.Equal(t, int64(1000), resp.Items[3].Amount)
}

// TestCalculateAmountsJSONReadsClockOnce proves that every item of a request is
// evaluated against the SAME moment.
//
// A campaign that ends in the middle of the request must not price two lines of
// the same cart from two different worlds; reading the clock per item would
// allow exactly that.
func TestCalculateAmountsJSONReadsClockOnce(t *testing.T) {
	fixture := batchFixture()
	var reads int
	svc := New(batchRepo(fixture), Options{Now: func() time.Time {
		reads++
		return testNow
	}})

	req := calculateAmountsRequest{CurrencyCode: "TRY", Items: []calculateAmountsItem{
		{PriceSetID: "pset_base", Quantity: 1},
		{PriceSetID: "pset_sale", Quantity: 1},
		{PriceSetID: "pset_tier", Quantity: 10},
	}}
	payload, err := json.Marshal(req)
	require.NoError(t, err)

	_, err = svc.CalculateAmountsJSON(context.Background(), payload)
	require.NoError(t, err)

	assert.Equal(t, 1, reads, "the clock is read per request, not per item")
}

// TestCalculateAmountsJSONPreservesOrder proves the response lines up with the
// request BY POSITION.
//
// The consumer matches the answers to the cart's lines by index; an answer out
// of order would write the neighboring variant's price onto every line, and no
// other check here would catch it.
func TestCalculateAmountsJSONPreservesOrder(t *testing.T) {
	fixture := batchFixture()
	svc := newTestService(batchRepo(fixture))

	resp := callBatch(t, svc, "TRY", nil, []batchItem{
		{"pset_sale", 1}, {"pset_empty", 1}, {"pset_base", 1}, {"pset_tier", 10},
	})

	// Each priced item also names the row the ladder picked and its list, and
	// an unpriced one names nothing (ADR 0168).
	require.Len(t, resp.Items, 4)
	assert.Equal(t, []calculatedAmount{
		{Amount: 9000, Priced: true, PriceID: "price_sale", PriceListID: ptr("plist_sale"), PriceListType: "sale"},
		{Amount: 0, Priced: false},
		{Amount: 1000, Priced: true, PriceID: "price_base"},
		{Amount: 800, Priced: true, PriceID: "price_tier"},
	}, resp.Items)
}

// TestCalculateAmountsJSONNormalizesCurrencyAndQuantity proves that the batch
// surface corrects the input THE SAME WAY as the per-item surface: the currency
// is turned to upper case, and a quantity of 0 counts as 1.
func TestCalculateAmountsJSONNormalizesCurrencyAndQuantity(t *testing.T) {
	fixture := batchFixture()
	svc := newTestService(batchRepo(fixture))

	resp := callBatch(t, svc, "try", nil, []batchItem{{"pset_tier", 0}})

	amount, err := svc.CalculateAmount(context.Background(), "pset_tier", "try", 0, nil)
	require.NoError(t, err)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, amount, resp.Items[0].Amount)
	assert.Equal(t, int64(1000), resp.Items[0].Amount, "a quantity of 0 counts as 1 and stays outside the tier")
}

// TestCalculateAmountsJSONRejectsBadRequest proves that a malformed request is
// rejected AS A WHOLE and never reaches the database.
//
// Rejecting instead of skipping matters: a silently dropped item leaves the
// caller a response SHORTER than its request, and the alignment the caller
// relies on is lost.
func TestCalculateAmountsJSONRejectsBadRequest(t *testing.T) {
	oversized := make([]calculateAmountsItem, MaxCalculateItems+1)
	for i := range oversized {
		oversized[i] = calculateAmountsItem{PriceSetID: "pset_base", Quantity: 1}
	}

	tests := map[string]struct {
		request calculateAmountsRequest
		message string
	}{
		"empty currency": {
			request: calculateAmountsRequest{Items: []calculateAmountsItem{{PriceSetID: "pset_base", Quantity: 1}}},
		},
		"empty container id": {
			request: calculateAmountsRequest{CurrencyCode: "TRY", Items: []calculateAmountsItem{{Quantity: 1}}},
			message: "item 0 of the batch price request",
		},
		"container id with a wrong prefix": {
			request: calculateAmountsRequest{CurrencyCode: "TRY", Items: []calculateAmountsItem{
				{PriceSetID: "pset_base", Quantity: 1},
				{PriceSetID: "price_1", Quantity: 1},
			}},
			message: "item 1 of the batch price request",
		},
		"negative quantity": {
			request: calculateAmountsRequest{CurrencyCode: "TRY", Items: []calculateAmountsItem{
				{PriceSetID: "pset_base", Quantity: -1},
			}},
			message: "item 0 of the batch price request",
		},
		"item count exceeds the ceiling": {
			request: calculateAmountsRequest{CurrencyCode: "TRY", Items: oversized},
			message: strconv.Itoa(MaxCalculateItems),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			repo := batchRepo(batchFixture())
			payload, err := json.Marshal(tc.request)
			require.NoError(t, err)

			_, err = newTestService(repo).CalculateAmountsJSON(context.Background(), payload)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			if tc.message != "" {
				assert.Contains(t, err.Error(), tc.message, "which item was rejected has to be written")
			}
			assert.Empty(t, repo.calls, "an invalid request must never reach the repository")
		})
	}
}

// TestCalculateAmountsJSONRejectsUnreadableBody proves that an empty or
// malformed body produces a TYPED error, not an empty response.
func TestCalculateAmountsJSONRejectsUnreadableBody(t *testing.T) {
	for name, body := range map[string]json.RawMessage{
		"empty body":     nil,
		"malformed body": json.RawMessage(`{"items":`),
		"array body":     json.RawMessage(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			repo := batchRepo(batchFixture())

			_, err := newTestService(repo).CalculateAmountsJSON(context.Background(), body)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Empty(t, repo.calls)
		})
	}
}

// TestCalculateAmountsJSONAcceptsEmptyItemList proves that a request without
// items is an EMPTY response, not an error: a cart with no lines is a valid
// cart.
func TestCalculateAmountsJSONAcceptsEmptyItemList(t *testing.T) {
	repo := batchRepo(batchFixture())
	svc := newTestService(repo)

	payload, err := json.Marshal(calculateAmountsRequest{CurrencyCode: "TRY"})
	require.NoError(t, err)

	raw, err := svc.CalculateAmountsJSON(context.Background(), payload)
	require.NoError(t, err)

	var resp calculateAmountsResponse
	require.NoError(t, json.Unmarshal(raw, &resp))
	assert.Empty(t, resp.Items)
}

// TestCalculateAmountsJSONUnconfiguredServiceFailsTyped proves that, on a
// service without a repository, the batch surface returns a typed error like
// the others.
func TestCalculateAmountsJSONUnconfiguredServiceFailsTyped(t *testing.T) {
	payload, err := json.Marshal(calculateAmountsRequest{
		CurrencyCode: "TRY",
		Items:        []calculateAmountsItem{{PriceSetID: "pset_base", Quantity: 1}},
	})
	require.NoError(t, err)

	_, err = New(nil, Options{}).CalculateAmountsJSON(context.Background(), payload)

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
