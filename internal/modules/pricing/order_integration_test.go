//go:build integration

package pricing_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// TestAPriceSetKeepsTheOrderOfItsPricesAndRules is D175: a set's prices and a
// price's rules are written in one transaction with one stamp, and the random
// tails of their ids ordered every read of them. Twelve prices and six rules
// named in reverse come back as they were given on the admin read, the rule
// listing, the storefront's read, the read layer's price set and the history's
// snapshot, and again after a replace that gives them in the opposite order.
func TestAPriceSetKeepsTheOrderOfItsPricesAndRules(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	svc := service.New(repo, service.Options{})

	inputs := make([]service.PriceInput, 0, 12)
	for i := range 12 {
		quantity := int32(12 - i)
		inputs = append(inputs, service.PriceInput{
			CurrencyCode: "TRY", Amount: int64(1_000 * quantity), MinQuantity: quantity,
		})
	}
	for j := range 6 {
		inputs[0].Rules = append(inputs[0].Rules, service.RuleInput{
			Attribute: fmt.Sprintf("attr_%02d", 5-j), Operator: models.OpEq, Values: []string{"x"},
		})
	}
	quantitiesOf := func(given []service.PriceInput) []int32 {
		out := make([]int32, 0, len(given))
		for i := range given {
			out = append(out, given[i].MinQuantity)
		}
		return out
	}
	attributesOf := func(given []service.RuleInput) []string {
		out := make([]string, 0, len(given))
		for _, rule := range given {
			out = append(out, rule.Attribute)
		}
		return out
	}
	quantities := func(prices []models.Price) []int32 {
		out := make([]int32, 0, len(prices))
		for i := range prices {
			out = append(out, prices[i].MinQuantity)
		}
		return out
	}
	attributes := func(rules []models.PriceRule) []string {
		out := make([]string, 0, len(rules))
		for i := range rules {
			out = append(out, rules[i].Attribute)
		}
		return out
	}

	set, err := svc.CreatePriceSet(ctx, inputs)
	require.NoError(t, err)

	// check reads every surface and holds it to the order the inputs gave.
	check := func(stage string, given []service.PriceInput) {
		t.Helper()

		ruled := slices.IndexFunc(given, func(in service.PriceInput) bool { return len(in.Rules) > 0 })
		require.GreaterOrEqual(t, ruled, 0)
		wantPrices := quantitiesOf(given)
		wantRules := attributesOf(given[ruled].Rules)

		prices, err := svc.ListPrices(ctx, set.ID)
		require.NoError(t, err)
		assert.Equal(t, wantPrices, quantities(prices), "%s: the admin read", stage)
		require.Len(t, prices, len(given))
		assert.Equal(t, wantRules, attributes(prices[ruled].Rules), "%s: a price's rules", stage)

		rules, err := svc.ListPriceRules(ctx, prices[ruled].ID)
		require.NoError(t, err)
		assert.Equal(t, wantRules, attributes(rules), "%s: the rule listing", stage)

		// The storefront leaves a price with rules out; the rest keep their order.
		store, err := svc.ListStorePrices(ctx, set.ID)
		require.NoError(t, err)
		storeQuantities := make([]int32, 0, len(store))
		for i := range store {
			storeQuantities = append(storeQuantities, store[i].Price.MinQuantity)
		}
		wantStore := slices.Delete(slices.Clone(wantPrices), ruled, ruled+1)
		assert.Equal(t, wantStore, storeQuantities, "%s: the storefront's read", stage)

		// The read layer's price set carries the same prices through the batched
		// read another module's listing uses.
		records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{set.ID}, nil)
		require.NoError(t, err)
		require.Len(t, records, 1)
		offered, ok := records[0]["prices"].([]map[string]any)
		require.True(t, ok, "%s: the set record carries its prices", stage)
		offeredQuantities := make([]int32, 0, len(offered))
		for _, price := range offered {
			quantity, ok := price["min_quantity"].(int32)
			require.True(t, ok, "%s: a price's min_quantity is an int32: %v", stage, price)
			offeredQuantities = append(offeredQuantities, quantity)
		}
		assert.Equal(t, wantStore, offeredQuantities, "%s: the read layer's prices", stage)

		snapshots := setSnapshots(ctx, t, repo, set.ID)
		require.NotEmpty(t, snapshots)
		latest := snapshots[len(snapshots)-1]
		for _, snapshot := range snapshots {
			if snapshot.RecordedAt.After(latest.RecordedAt) {
				latest = snapshot
			}
		}
		assert.Equal(t, wantPrices, quantities(latest.Prices), "%s: the history's snapshot", stage)
		require.Len(t, latest.Prices, len(given))
		assert.Equal(t, wantRules, attributes(latest.Prices[ruled].Rules), "%s: a snapshot's rules", stage)
	}

	check("as created", inputs)

	replaced := slices.Clone(inputs)
	slices.Reverse(replaced)
	replaced[len(replaced)-1].Rules = slices.Clone(replaced[len(replaced)-1].Rules)
	slices.Reverse(replaced[len(replaced)-1].Rules)
	_, err = svc.SetPrices(ctx, set.ID, replaced)
	require.NoError(t, err)

	check("after a replace", replaced)
}
