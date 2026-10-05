package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// sentItems runs the checkout and returns the order snapshot's lines by
// variant, as raw JSON objects.
func sentItems(t *testing.T, h *harness) map[string]map[string]json.RawMessage {
	t.Helper()
	var sent json.RawMessage
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		sent = snapshot
		return testOrderID, nil
	}
	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	var body struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(sent, &body))
	out := map[string]map[string]json.RawMessage{}
	for _, item := range body.Items {
		var variant string
		require.NoError(t, json.Unmarshal(item["variant_id"], &variant))
		out[variant] = item
	}
	return out
}

// TestAnOrderLineCarriesTheCostInTheOrdersCurrency is ADR 0401: of a variant's
// costs the line keeps the one in the cart's currency, on the plan and in the
// order snapshot.
func TestAnOrderLineCarriesTheCostInTheOrdersCurrency(t *testing.T) {
	h := newHarness(t)
	scripts := defaultVariants()
	a := scripts[testVariantA]
	a.costs = map[string]int64{"EUR": 5, testCurrency: 400, "USD": 7}
	scripts[testVariantA] = a
	scriptCatalog(h, scripts)

	plan, err := h.wf.prepare(context.Background(), h.input())
	require.NoError(t, err)
	for _, line := range plan.Lines {
		if line.VariantID == testVariantA {
			require.NotNil(t, line.UnitCost)
			assert.Equal(t, int64(400), *line.UnitCost, "the cart's currency, not the first or the last entry")
		}
	}

	items := sentItems(t, h)
	assert.JSONEq(t, `400`, string(items[testVariantA]["unit_cost"]))
}

// TestACostOfZeroIsACost: a cost of zero reaches the order as zero, and a
// variant with no cost in the cart's currency sends no cost at all, even when it
// has one in another.
func TestACostOfZeroIsACost(t *testing.T) {
	h := newHarness(t)
	scripts := defaultVariants()
	a, b := scripts[testVariantA], scripts[testVariantB]
	a.costs = map[string]int64{testCurrency: 0}
	b.costs = map[string]int64{"EUR": 9}
	scripts[testVariantA], scripts[testVariantB] = a, b
	scriptCatalog(h, scripts)

	items := sentItems(t, h)
	require.Contains(t, items[testVariantA], "unit_cost", "a cost of zero is written")
	assert.JSONEq(t, `0`, string(items[testVariantA]["unit_cost"]))
	assert.NotContains(t, items[testVariantB], "unit_cost", "no cost in the order's currency is no cost")
}

// TestACostListThatDoesNotReadIsRefused: the list is read as strictly as the
// title. Each shape below refuses the checkout before any step runs; the one
// that crossed a JSON boundary still reads.
func TestACostListThatDoesNotReadIsRefused(t *testing.T) {
	entry := func(code string, amount any) map[string]any {
		return map[string]any{FieldUnitCostCurrencyCode: code, FieldUnitCostAmount: amount}
	}
	for name, costs := range map[string]any{
		"missing":                nil,
		"not a list":             "TRY:400",
		"an entry not a record":  []any{"TRY"},
		"a lower-case currency":  []any{entry("try", int64(1))},
		"a four-letter currency": []any{entry("TRYY", int64(1))},
		"no amount":              []any{map[string]any{FieldUnitCostCurrencyCode: testCurrency}},
		"a negative amount":      []any{entry(testCurrency, int64(-1))},
		"past the bound":         []any{entry(testCurrency, MaxAmount+1)},
		"a fraction":             []any{entry(testCurrency, 1.5)},
		"a currency twice":       []any{entry(testCurrency, int64(1)), entry("EUR", int64(2)), entry(testCurrency, int64(3))},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
				records := catalogAnswer(defaultVariants(), spec)
				if spec.Entity == EntityVariant {
					for _, record := range records {
						if record[query.IDField] != testVariantA {
							continue
						}
						delete(record, FieldUnitCosts)
						if costs != nil {
							record[FieldUnitCosts] = costs
						}
					}
				}
				return records, nil
			}

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.Error(t, err)
			assert.Equal(t, errors.KindInternal, errors.KindOf(err), "%v", err)
			assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
			assert.Zero(t, h.rec.count("order:place"), "no order is opened")
			assert.Zero(t, h.rec.count("inventory:reserve:"+testLineA), "nothing is reserved")
		})
	}

	costs, ok := readUnitCosts([]any{entry(testCurrency, float64(400)), entry("EUR", float64(0))})
	require.True(t, ok, "a list that crossed JSON still reads")
	assert.Equal(t, map[string]int64{testCurrency: 400, "EUR": 0}, costs)
	bound, ok := readUnitCosts([]query.Record{{FieldUnitCostCurrencyCode: testCurrency, FieldUnitCostAmount: MaxAmount}})
	require.True(t, ok, "the bound is a cost")
	assert.Equal(t, map[string]int64{testCurrency: MaxAmount}, bound)
	none, ok := readUnitCosts([]query.Record{})
	require.True(t, ok)
	assert.NotNil(t, none, "an empty list reads as no cost")
	assert.Empty(t, none)
}

// TestOnlyTheCartsOwnVariantsAreAskedTheirCost: the first round names the
// costs and the round reading a bundle's components does not; a component's
// cost is not its bundle line's.
func TestOnlyTheCartsOwnVariantsAreAskedTheirCost(t *testing.T) {
	h := newHarness(t)
	scripts := boxCatalog(variantScript{manageInventory: true, costs: map[string]int64{testCurrency: 30}})
	box := scripts[testVariantA]
	box.costs = map[string]int64{testCurrency: 250}
	scripts[testVariantA] = box
	linkOnly(h, map[string][]string{
		testVariantB: {testItemB}, testTowel: {testTowelItem}, testSoap: {testSoapItem},
	})
	var variantSpecs []query.GraphSpec
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		if spec.Entity == EntityVariant {
			variantSpecs = append(variantSpecs, spec)
		}
		return catalogAnswer(scripts, spec), nil
	}

	plan, err := h.wf.prepare(context.Background(), h.input())
	require.NoError(t, err)
	require.Len(t, variantSpecs, 2, "the cart's variants, then the box's parts")
	assert.Contains(t, variantSpecs[0].Fields, FieldUnitCosts)
	assert.NotContains(t, variantSpecs[1].Fields, FieldUnitCosts, "the costs are asked once, in the first round")
	for _, line := range plan.Lines {
		if line.VariantID == testVariantA {
			require.NotNil(t, line.UnitCost)
			assert.Equal(t, int64(250), *line.UnitCost, "a bundle line costs its own entry")
		}
	}
}

// TestAPlanSavedBeforeTheCostReadsNone: a plan written before the field existed
// decodes it nil, and a line with no cost writes no key.
func TestAPlanSavedBeforeTheCostReadsNone(t *testing.T) {
	var line planLine
	require.NoError(t, json.Unmarshal([]byte(`{"line_item_id":"cali_1","variant_id":"var_1","unit_price":100}`), &line))
	assert.Nil(t, line.UnitCost)

	raw, err := json.Marshal(line)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "unit_cost")
}
