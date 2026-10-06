package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// currencyCodes returns n distinct three-letter codes: AAA, AAB, ...
func currencyCodes(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, string([]byte{'A' + byte(i/676%26), 'A' + byte(i/26%26), 'A' + byte(i%26)}))
	}
	return out
}

// costsOf is one cost per code, each worth its position.
func costsOf(codes []string) []models.VariantCost {
	out := make([]models.VariantCost, 0, len(codes))
	for i, code := range codes {
		out = append(out, models.VariantCost{CurrencyCode: code, Amount: int64(i)})
	}
	return out
}

// TestAVariantsCostsReadBackAsWritten is ADR 0401's admin surface on the fake
// store: a variant with no cost reads an empty list, a write is read back
// normalized and in currency order, and an empty write clears it.
func TestAVariantsCostsReadBackAsWritten(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	variant := seedProduct(t, fx.svc, "kettle", "Kettle").Variants[0].ID

	none, err := fx.svc.VariantCosts(ctx, variant)
	require.NoError(t, err)
	assert.NotNil(t, none, "no cost is an empty list, not a missing one")
	assert.Empty(t, none)

	written, err := fx.svc.SetVariantCosts(ctx, variant, []models.VariantCost{
		{CurrencyCode: " try ", Amount: 400}, {CurrencyCode: "EUR", Amount: 0},
		{CurrencyCode: "usd", Amount: service.MaxCostAmount},
	})
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{
		{CurrencyCode: "EUR", Amount: 0}, {CurrencyCode: "TRY", Amount: 400},
		{CurrencyCode: "USD", Amount: service.MaxCostAmount},
	}, written, "a code is trimmed and upper-cased, zero and the bound are costs")

	cleared, err := fx.svc.SetVariantCosts(ctx, variant, []models.VariantCost{})
	require.NoError(t, err)
	assert.Empty(t, cleared)
	assert.Equal(t, 2, fx.store.callCount("ReplaceVariantCosts"))
	assert.Equal(t, 2, fx.store.callCount("LockLiveVariantsForBundle"), "every write holds the variant's row")
}

// TestACostListIsCheckedBeforeAnythingIsWritten is the validation of
// SetVariantCosts: each refusal is 422 product_invalid_input, the list beside
// it is not written, and the variant's costs stay as they were.
func TestACostListIsCheckedBeforeAnythingIsWritten(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	variant := seedProduct(t, fx.svc, "kettle", "Kettle").Variants[0].ID
	kept := []models.VariantCost{{CurrencyCode: "TRY", Amount: 400}}
	_, err := fx.svc.SetVariantCosts(ctx, variant, kept)
	require.NoError(t, err)

	fifty, err := fx.svc.SetVariantCosts(ctx, variant, costsOf(currencyCodes(service.MaxVariantCosts)))
	require.NoError(t, err, "the bound itself is a list")
	assert.Len(t, fifty, service.MaxVariantCosts)
	_, err = fx.svc.SetVariantCosts(ctx, variant, kept)
	require.NoError(t, err)

	for name, costs := range map[string][]models.VariantCost{
		"a currency twice":               {{CurrencyCode: "TRY", Amount: 1}, {CurrencyCode: "EUR", Amount: 2}, {CurrencyCode: "TRY", Amount: 3}},
		"a currency twice once folded":   {{CurrencyCode: "try", Amount: 1}, {CurrencyCode: "TRY", Amount: 3}},
		"a negative amount":              {{CurrencyCode: "EUR", Amount: -1}},
		"an amount past the bound":       {{CurrencyCode: "EUR", Amount: service.MaxCostAmount + 1}},
		"a code of two letters":          {{CurrencyCode: "TR", Amount: 1}},
		"a code of four letters":         {{CurrencyCode: "TRYY", Amount: 1}},
		"a code with a digit":            {{CurrencyCode: "T1Y", Amount: 1}},
		"no code":                        {{CurrencyCode: "", Amount: 1}},
		"one currency past the list cap": costsOf(currencyCodes(service.MaxVariantCosts + 1)),
	} {
		before := fx.store.callCount("ReplaceVariantCosts")
		_, err := fx.svc.SetVariantCosts(ctx, variant, costs)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
		assert.Equal(t, "product_invalid_input", errors.CodeOf(err), name)
		assert.Equal(t, before, fx.store.callCount("ReplaceVariantCosts"), "%s: nothing is written", name)

		stored, err := fx.svc.VariantCosts(ctx, variant)
		require.NoError(t, err)
		assert.Equal(t, kept, stored, "%s: the costs stay as they were", name)
	}
}

// TestADeletedVariantHasNoCosts: an unknown or deleted variant is 404 on both
// the read and the write, and the write finds it gone under its lock.
func TestADeletedVariantHasNoCosts(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	product := seedProduct(t, fx.svc, "kettle", "Kettle")
	variant := product.Variants[0].ID
	require.NoError(t, fx.svc.DeleteVariant(ctx, variant))

	for name, err := range map[string]error{
		"read a deleted variant":  second(fx.svc.VariantCosts(ctx, variant)),
		"write a deleted variant": second(fx.svc.SetVariantCosts(ctx, variant, []models.VariantCost{{CurrencyCode: "TRY", Amount: 1}})),
		"read an unknown variant": second(fx.svc.VariantCosts(ctx, "variant_missing")),
		"write an unknown variant": second(fx.svc.SetVariantCosts(ctx, "variant_missing",
			[]models.VariantCost{{CurrencyCode: "TRY", Amount: 1}})),
	} {
		require.Error(t, err, name)
		assert.True(t, errors.IsNotFound(err), "%s: %v", name, err)
		assert.Equal(t, "product_not_found", errors.CodeOf(err), name)
	}
	assert.Zero(t, fx.store.callCount("ReplaceVariantCosts"), "nothing is written for a variant that is gone")
}

// second is the error of a two-valued call.
func second[T any](_ T, err error) error { return err }

// TestTheVariantRecordCarriesItsCostsOnlyWhenNamed is the field the checkout
// reads (ADR 0401): named, it carries every currency's cost through both
// provider reads, an empty list for a variant with none, in one batch read; not
// named, it is not read; and a reader naming no fields does not receive it,
// for a record taken whole may be published.
func TestTheVariantRecordCarriesItsCostsOnlyWhenNamed(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	costed := seedProduct(t, fx.svc, "kettle", "Kettle").Variants[0].ID
	plain := seedProduct(t, fx.svc, "mug", "Mug").Variants[0].ID
	_, err := fx.svc.SetVariantCosts(ctx, costed, []models.VariantCost{
		{CurrencyCode: "TRY", Amount: 400}, {CurrencyCode: "EUR", Amount: 5},
	})
	require.NoError(t, err)

	want := []query.Record{
		{service.FieldUnitCostCurrencyCode: "EUR", service.FieldUnitCostAmount: int64(5)},
		{service.FieldUnitCostCurrencyCode: "TRY", service.FieldUnitCostAmount: int64(400)},
	}
	provider := fx.variantProvider()
	fields := []string{query.IDField, service.FieldUnitCosts}
	ids := []string{costed, plain}

	reads := fx.store.callCount("ListVariantCosts")
	fetched, err := provider.FetchByIDs(ctx, ids, fields)
	require.NoError(t, err)
	assert.Equal(t, reads+1, fx.store.callCount("ListVariantCosts"), "one batch read for the page")
	listed, err := provider.List(ctx, query.ListOptions{Fields: fields, Filters: map[string]any{"ids": ids}})
	require.NoError(t, err)
	for name, records := range map[string][]query.Record{"FetchByIDs": fetched, "List": listed} {
		require.Len(t, records, 2, name)
		for _, record := range records {
			if record[query.IDField] == costed {
				assert.Equal(t, want, record[service.FieldUnitCosts], name)
			} else {
				assert.Equal(t, []query.Record{}, record[service.FieldUnitCosts], "%s: none is an empty list", name)
			}
		}
	}

	reads = fx.store.callCount("ListVariantCosts")
	unnamed, err := provider.FetchByIDs(ctx, []string{costed}, []string{query.IDField, "title"})
	require.NoError(t, err)
	assert.NotContains(t, unnamed[0], service.FieldUnitCosts, "a field not asked for is not carried")
	whole, err := provider.FetchByIDs(ctx, []string{costed}, nil)
	require.NoError(t, err)
	assert.Contains(t, whole[0], "title", "the default field set is the record")
	assert.NotContains(t, whole[0], service.FieldUnitCosts, "a cost is never in the default field set")
	assert.Equal(t, reads, fx.store.callCount("ListVariantCosts"), "a cost not named is not read")
}

// costPtr takes a cost's address inline.
func costPtr(v int64) *int64 { return &v }

// TestOneCurrencysCostIsWrittenFromWhatWasRead is the panel's cost write
// (ADR 0412): one currency's cost is set from none, changed from its value and
// cleared, and another currency's cost stays through all three; a write drawn
// from a cost that moved since, or from none when one is held, is refused with
// product_variant_cost_moved and writes nothing; the currency and the amount
// are held to the list's rules.
func TestOneCurrencysCostIsWrittenFromWhatWasRead(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	variant := seedProduct(t, fx.svc, "kettle", "Kettle").Variants[0].ID
	_, err := fx.svc.SetVariantCosts(ctx, variant, []models.VariantCost{{CurrencyCode: "USD", Amount: 7}})
	require.NoError(t, err)
	usd := models.VariantCost{CurrencyCode: "USD", Amount: 7}

	require.NoError(t, fx.svc.SetVariantCost(ctx, variant, " try ", nil, costPtr(400)))
	stored, err := fx.svc.VariantCosts(ctx, variant)
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 400}, usd}, stored,
		"a cost in a new currency is added beside the others")

	require.NoError(t, fx.svc.SetVariantCost(ctx, variant, "TRY", costPtr(400), costPtr(450)))
	stored, err = fx.svc.VariantCosts(ctx, variant)
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{{CurrencyCode: "TRY", Amount: 450}, usd}, stored, "the cost read is changed")

	for name, read := range map[string]*int64{"a cost that moved": costPtr(400), "none while one is held": nil} {
		before := fx.store.callCount("ReplaceVariantCosts")
		err := fx.svc.SetVariantCost(ctx, variant, "TRY", read, costPtr(500))
		require.Error(t, err, name)
		assert.True(t, errors.IsConflict(err), "%s: %v", name, err)
		assert.Equal(t, service.CodeVariantCostMoved, errors.CodeOf(err), name)
		assert.Equal(t, before, fx.store.callCount("ReplaceVariantCosts"), "%s: nothing is written", name)
	}
	err = fx.svc.SetVariantCost(ctx, variant, "EUR", costPtr(1), costPtr(2))
	require.Error(t, err, "a cost read where none is held has moved too")
	assert.Equal(t, service.CodeVariantCostMoved, errors.CodeOf(err))

	require.NoError(t, fx.svc.SetVariantCost(ctx, variant, "TRY", costPtr(450), nil))
	stored, err = fx.svc.VariantCosts(ctx, variant)
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{usd}, stored, "clearing drops the named currency alone")

	for name, write := range map[string]func() error{
		"a code of two letters":    func() error { return fx.svc.SetVariantCost(ctx, variant, "TR", nil, costPtr(1)) },
		"a negative amount":        func() error { return fx.svc.SetVariantCost(ctx, variant, "EUR", nil, costPtr(-1)) },
		"an amount past the bound": func() error { return fx.svc.SetVariantCost(ctx, variant, "EUR", nil, costPtr(service.MaxCostAmount+1)) },
	} {
		err := write()
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	for name, id := range map[string]string{"unknown": "variant_missing", "blank": " "} {
		err := fx.svc.SetVariantCost(ctx, id, "TRY", nil, costPtr(1))
		require.Error(t, err, name)
		assert.False(t, errors.IsConflict(err), "%s: %v", name, err)
	}
	err = fx.svc.SetVariantCost(ctx, "variant_missing", "TRY", nil, costPtr(1))
	assert.True(t, errors.IsNotFound(err), "%v", err)
	stored, err = fx.svc.VariantCosts(ctx, variant)
	require.NoError(t, err)
	assert.Equal(t, []models.VariantCost{usd}, stored, "no refused write moved a cost")
}
