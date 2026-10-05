package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestCreatePriceSetValidatesBeforeWriting proves that, when an invalid price
// is given, the container is NEVER CREATED.
//
// This is the behavior that prevents the orphan records a "create first,
// validate later" order leaves behind; if the order is reversed, the test
// fails.
func TestCreatePriceSetValidatesBeforeWriting(t *testing.T) {
	repo := newStubRepo()
	repo.createPriceSetFn = func(
		_ context.Context, id string, _ []models.Price, now time.Time,
	) (models.PriceSet, error) {
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	_, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "XX", Amount: 200},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["CreatePriceSet"], "no container may be created while there is an invalid price")
}

// TestCreatePriceSetReportsFailingIndex proves that the error reports which
// price was rejected.
func TestCreatePriceSetReportsFailingIndex(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: -1},
	})

	require.Error(t, err)
	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 1, typed.Details["index"])
}

// TestCreatePriceSetWithoutPricesWritesEmptySet proves that, when no price is
// given, the container is written with an empty set of prices.
func TestCreatePriceSetWithoutPricesWritesEmptySet(t *testing.T) {
	repo := newStubRepo()
	var gotPrices []models.Price
	repo.createPriceSetFn = func(
		_ context.Context, id string, prices []models.Price, now time.Time,
	) (models.PriceSet, error) {
		gotPrices = prices
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	set, err := newTestService(repo).CreatePriceSet(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(set.ID, models.PriceSetIDPrefix))
	assert.Equal(t, 1, repo.calls["CreatePriceSet"])
	assert.Empty(t, gotPrices)
}

// TestCreatePriceSetWritesSetAndPricesInOneCall proves that the container and
// its prices reach the repository in ONE call.
//
// Had a second write round (ReplacePrices) been opened, that round would be a
// SEPARATE transaction, and when the database rejected a price, the container
// would already have been committed — that is, a container without prices,
// bound to nothing, would have been left behind. stubRepo's ReplacePrices is
// not scripted; if it is called it returns an error, and the test fails for
// that reason too.
func TestCreatePriceSetWritesSetAndPricesInOneCall(t *testing.T) {
	repo := newStubRepo()
	var gotPrices []models.Price
	repo.createPriceSetFn = func(
		_ context.Context, id string, prices []models.Price, now time.Time,
	) (models.PriceSet, error) {
		gotPrices = prices
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	set, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: 200},
	})

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(set.ID, models.PriceSetIDPrefix))
	require.Len(t, gotPrices, 2, "the prices have to go to the call that creates the container")
	assert.Equal(t, "TRY", gotPrices[0].CurrencyCode)
	assert.Equal(t, "USD", gotPrices[1].CurrencyCode)
	assert.Zero(t, repo.calls["ReplacePrices"], "the container and its prices must not be written in SEPARATE rounds")
}

// TestSetPricesNormalizesInput proves that the input reaches the repository
// normalized: the currency upper case, the minimum quantity defaulting to 1,
// the ids prefixed.
func TestSetPricesNormalizesInput(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", []PriceInput{{
		CurrencyCode: "try",
		Amount:       1999,
		Rules:        []RuleInput{{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}}},
	}})

	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.Equal(t, "TRY", written[0].CurrencyCode)
	assert.Equal(t, int32(1), written[0].MinQuantity)
	assert.Nil(t, written[0].MaxQuantity)
	assert.True(t, strings.HasPrefix(written[0].ID, models.PriceIDPrefix))

	require.Len(t, written[0].Rules, 1)
	assert.True(t, strings.HasPrefix(written[0].Rules[0].ID, models.PriceRuleIDPrefix))
	assert.Equal(t, written[0].ID, written[0].Rules[0].PriceID,
		"the rule has to be bound to the price it belongs to")
}

// TestSetPricesRejectsInvalidBeforeWriting proves that on invalid input the
// repository is NEVER reached; this is the application-side half of the
// atomicity.
func TestSetPricesRejectsInvalidBeforeWriting(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "TRY", Amount: models.MaxAmount + 1},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["ReplacePrices"], "invalid input must not reach the repository")
}

// TestSetPricesAcceptsEmptySlice proves that an empty slice means "remove every
// price".
func TestSetPricesAcceptsEmptySlice(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", nil)

	require.NoError(t, err)
	assert.Empty(t, written)
	assert.Equal(t, 1, repo.calls["ReplacePrices"])
}

// TestSetPricesRejectsWrongIDPrefix proves that an id of the wrong kind comes
// back with a validation error (not "not found").
func TestSetPricesRejectsWrongIDPrefix(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).SetPrices(context.Background(), "variant_1", nil)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, repo.calls)
}

// TestListPricesChecksSetExists proves that the price listing of a missing
// container returns NotFound, not an empty slice.
func TestListPricesChecksSetExists(t *testing.T) {
	repo := newStubRepo()
	repo.getPriceSetFn = func(context.Context, string) (models.PriceSet, error) {
		return models.PriceSet{}, errors.NotFound("price_set_not_found", "missing")
	}

	_, err := newTestService(repo).ListPrices(context.Background(), "pset_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListPrices"], "with no container, no price query may be opened")
}

// TestListPriceSetsReportsAppliedPaging proves that the limit/offset written to
// the envelope are the APPLIED values.
func TestListPriceSetsReportsAppliedPaging(t *testing.T) {
	var gotLimit, gotOffset int32
	repo := newStubRepo()
	repo.listPriceSetsFn = func(_ context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
		gotLimit, gotOffset = limit, offset
		return []models.PriceSet{{ID: "pset_1"}}, 42, nil
	}

	page, err := newTestService(repo).ListPriceSets(context.Background(), MaxLimit+50, 10)

	require.NoError(t, err)
	assert.Equal(t, MaxLimit, gotLimit, "the clipped limit has to reach the repository")
	assert.Equal(t, int32(10), gotOffset)
	assert.Equal(t, MaxLimit, page.Limit, "the envelope has to report the applied limit")
	assert.Equal(t, int32(10), page.Offset)
	assert.Equal(t, int64(42), page.Count)
	assert.Len(t, page.Items, 1)
}

// TestCreatePriceListDefaultsToDraft proves that, when no status is given, the
// list is NOT PUBLISHED.
func TestCreatePriceListDefaultsToDraft(t *testing.T) {
	var written models.PriceList
	repo := newStubRepo()
	repo.createPriceListFn = func(_ context.Context, list models.PriceList, _ time.Time) (models.PriceList, error) {
		written = list
		return list, nil
	}

	_, err := newTestService(repo).CreatePriceList(context.Background(), PriceListInput{
		Title: "Summer campaign",
		Type:  models.PriceListSale,
	})

	require.NoError(t, err)
	assert.Equal(t, models.PriceListDraft, written.Status)
	assert.True(t, strings.HasPrefix(written.ID, models.PriceListIDPrefix))
}

// TestCreatePriceListValidation proves every branch of the price list
// validation.
func TestCreatePriceListValidation(t *testing.T) {
	early := testNow
	late := testNow.Add(time.Hour)

	for _, tc := range []struct {
		name string
		in   PriceListInput
		ok   bool
	}{
		{"valid", PriceListInput{Title: "K", Type: models.PriceListSale}, true},
		{"window in order", PriceListInput{
			Title: "K", Type: models.PriceListOverride, StartsAt: &early, EndsAt: &late}, true},
		{"blank title", PriceListInput{Title: "   ", Type: models.PriceListSale}, false},
		{"undefined type", PriceListInput{Title: "K", Type: models.PriceListType("bogus")}, false},
		{"empty type", PriceListInput{Title: "K"}, false},
		{"undefined status", PriceListInput{
			Title: "K", Type: models.PriceListSale, Status: models.PriceListStatus("bogus")}, false},
		{"window reversed", PriceListInput{
			Title: "K", Type: models.PriceListSale, StartsAt: &late, EndsAt: &early}, false},
		{"window ends equal", PriceListInput{
			Title: "K", Type: models.PriceListSale, StartsAt: &early, EndsAt: &early}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			repo.createPriceListFn = func(_ context.Context, list models.PriceList, _ time.Time) (models.PriceList, error) {
				return list, nil
			}

			_, err := newTestService(repo).CreatePriceList(context.Background(), tc.in)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["CreatePriceList"])
		})
	}
}

// TestListPriceRulesChecksPriceExists proves that the rules of a missing price
// return NotFound, not an empty slice.
func TestListPriceRulesChecksPriceExists(t *testing.T) {
	repo := newStubRepo()
	repo.getPriceFn = func(context.Context, string) (models.Price, error) {
		return models.Price{}, errors.NotFound("price_not_found", "missing")
	}

	_, err := newTestService(repo).ListPriceRules(context.Background(), "price_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListPriceRules"])
}

// TestServiceRejectsWrongIDPrefixes proves that every endpoint expects its own
// id prefix.
func TestServiceRejectsWrongIDPrefixes(t *testing.T) {
	ctx := context.Background()
	repo := newStubRepo()
	svc := newTestService(repo)

	for name, call := range map[string]func() error{
		"GetPriceSet":     func() error { _, err := svc.GetPriceSet(ctx, "price_1"); return err },
		"DeletePriceSet":  func() error { return svc.DeletePriceSet(ctx, "plist_1") },
		"GetPriceList":    func() error { _, err := svc.GetPriceList(ctx, "pset_1"); return err },
		"DeletePriceList": func() error { return svc.DeletePriceList(ctx, "pset_1") },
		"GetPriceRule":    func() error { _, err := svc.GetPriceRule(ctx, "price_1"); return err },
		"DeletePriceRule": func() error { return svc.DeletePriceRule(ctx, "price_1") },
		"CreatePriceRule": func() error {
			_, err := svc.CreatePriceRule(ctx, "pset_1",
				RuleInput{Attribute: "k", Operator: models.OpEq, Values: []string{"v"}})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
	assert.Empty(t, repo.calls, "prefix errors must never reach the repository")
}

// TestUnconfiguredServiceFailsTyped proves that a service without a repository
// returns a typed error, not a panic.
func TestUnconfiguredServiceFailsTyped(t *testing.T) {
	svc := New(nil, Options{})

	_, err := svc.GetPriceSet(context.Background(), "pset_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}

// TestSetBasePricesIsDeterministic proves that the cross-module surface writes
// the currencies IN ORDER; map iteration order is random.
func TestSetBasePricesIsDeterministic(t *testing.T) {
	var seen []string
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		seen = nil
		for i := range prices {
			seen = append(seen, prices[i].CurrencyCode)
		}
		return prices, nil
	}
	svc := newTestService(repo)

	amounts := map[string]int64{"usd": 500, "try": 19900, "eur": 450, "gbp": 400}
	for range 5 {
		require.NoError(t, svc.SetBasePrices(context.Background(), "pset_1", amounts))
		assert.Equal(t, []string{"EUR", "GBP", "TRY", "USD"}, seen)
	}
}

// TestSetBasePricesWritesBasePrices proves that the prices written have no list
// and no rules; that is the definition of "base".
func TestSetBasePricesWritesBasePrices(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	err := newTestService(repo).SetBasePrices(context.Background(), "pset_1",
		map[string]int64{"TRY": 19900})

	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.Nil(t, written[0].PriceListID, "a base price must not be bound to a list")
	assert.Empty(t, written[0].Rules, "a base price has to be unconditional")
	assert.Equal(t, int64(19900), written[0].Amount)
}

// TestCreateEmptyPriceSetReturnsID proves that the cross-module surface returns
// only an id.
func TestCreateEmptyPriceSetReturnsID(t *testing.T) {
	repo := newStubRepo()
	var gotPrices []models.Price
	repo.createPriceSetFn = func(
		_ context.Context, id string, prices []models.Price, now time.Time,
	) (models.PriceSet, error) {
		gotPrices = prices
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	id, err := newTestService(repo).CreateEmptyPriceSet(context.Background())

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, models.PriceSetIDPrefix))
	assert.Empty(t, gotPrices, "a container without prices has to be created")
}

// TestCreatePriceSetReportsFailingRuleIndex proves that a rule-level error
// carries BOTH the price's AND the rule's position.
//
// Had the two levels used the same detail key, the outer price index would
// OVERWRITE the inner rule index, and in the case "prices[1].rules[2] is
// invalid" the client would see only index=1 and look for the error in the
// price itself. That is why the two indexes are set up with DIFFERENT values;
// if one were written in place of the other, the test would fail.
func TestCreatePriceSetReportsFailingRuleIndex(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "TRY", Amount: 200, Rules: []RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
			{Attribute: "customer_group_id", Operator: models.OpIn, Values: []string{"vip"}},
			{Attribute: "customer_age", Operator: models.OpGt, Values: []string{"eighteen"}},
		}},
	})

	require.Error(t, err)
	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 1, typed.Details[detailIndex], "which price")
	assert.Equal(t, 2, typed.Details[detailRuleIndex], "which rule of that price")
	assert.Zero(t, repo.calls["CreatePriceSet"], "no container may be created while there is an invalid rule")
}

// TestAPriceRuleCannotNameTheCartsBag holds ADR 0403 at the three writes that
// take a rule a caller wrote: an attribute under `cart.` is the cart's
// metadata, which reaches no price, so a rule naming it is refused before
// anything is written. A name that merely starts with the letters, and a name
// the cart does send, pass.
func TestAPriceRuleCannotNameTheCartsBag(t *testing.T) {
	const reserved = "pricing_rule_attribute_reserved"

	writes := map[string]func(repo *stubRepo, rule RuleInput) error{
		"CreatePriceRule": func(repo *stubRepo, rule RuleInput) error {
			_, err := newTestService(repo).CreatePriceRule(context.Background(), "price_1", rule)
			return err
		},
		"CreatePriceSet": func(repo *stubRepo, rule RuleInput) error {
			_, err := newTestService(repo).CreatePriceSet(context.Background(), cartRulePrices(rule))
			return err
		},
		"ReplacePrices": func(repo *stubRepo, rule RuleInput) error {
			_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", cartRulePrices(rule))
			return err
		},
	}

	for write, call := range writes {
		for _, attribute := range []string{"cart.arm", "cart."} {
			repo := writingRepo()
			err := call(repo, RuleInput{Attribute: attribute, Operator: models.OpEq, Values: []string{"B"}})

			require.Error(t, err, "%s with %q", write, attribute)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "%s with %q", write, attribute)
			assert.Equal(t, reserved, errors.CodeOf(err), "%s with %q", write, attribute)
			assert.Zero(t, repo.calls[write], "%s with %q: nothing may be written", write, attribute)
			if write == "CreatePriceRule" {
				continue
			}
			var typed *errors.Error
			require.True(t, errors.As(err, &typed), write)
			assert.Equal(t, 2, typed.Details[detailIndex], "%s: which price", write)
			assert.Equal(t, 1, typed.Details[detailRuleIndex], "%s: which rule of that price", write)
		}
		for _, attribute := range []string{"cartel", "region_id"} {
			repo := writingRepo()
			err := call(repo, RuleInput{Attribute: attribute, Operator: models.OpEq, Values: []string{"B"}})

			require.NoError(t, err, "%s with %q", write, attribute)
			assert.Equal(t, 1, repo.calls[write], "%s with %q is written", write, attribute)
		}
	}
}

// cartRulePrices is a base price, a quantity tier and a ruled price whose
// SECOND rule is the given one, so the error has an index at both levels to
// report and the two differ.
func cartRulePrices(rule RuleInput) []PriceInput {
	return []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "TRY", Amount: 95, MinQuantity: 10},
		{CurrencyCode: "TRY", Amount: 90, Rules: []RuleInput{
			{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}},
			rule,
		}},
	}
}

// writingRepo accepts the three writes a rule reaches pricing through.
func writingRepo() *stubRepo {
	repo := newStubRepo()
	repo.createPriceSetFn = func(_ context.Context, id string, _ []models.Price, _ time.Time) (models.PriceSet, error) {
		return models.PriceSet{ID: id}, nil
	}
	repo.replacePricesFn = func(_ context.Context, _ string, prices []models.Price, _ time.Time) ([]models.Price, error) {
		return prices, nil
	}
	repo.createPriceRuleFn = func(_ context.Context, rule models.PriceRule, _ time.Time) (models.PriceRule, error) {
		return rule, nil
	}
	return repo
}
