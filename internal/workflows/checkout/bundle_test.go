package checkout

// This file holds ADR 0235's checkout: a bundle line reserves its components.
//
// The cart's line A sells two gift boxes, each a towel and two soaps; line B
// is an ordinary counted line. Every test here fails if the bundle's
// composition stops being read, stops being multiplied by the line's quantity,
// or stops being traced per component in the record.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

const (
	testTowel     = "var_towel"
	testSoap      = "var_soap"
	testTowelItem = "inv_towel"
	testSoapItem  = "inv_soap"
)

// boxCatalog is line A's variant as a gift box of a towel and two soaps, with
// the two parts counted as given.
func boxCatalog(soap variantScript) map[string]variantScript {
	soap.title = "Soap"
	return map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true, components: []bundlePart{
			{VariantID: testTowel, Quantity: 1},
			{VariantID: testSoap, Quantity: 2},
		}},
		testVariantB: {title: testTitleB, manageInventory: true},
		testTowel:    {title: "Towel", manageInventory: true},
		testSoap:     soap,
	}
}

// reservePerItem makes every reservation id name its line AND its item, since
// a bundle line takes more than one.
func reservePerItem(h *harness) {
	h.inventory.reserveFn = func(_ context.Context, itemID, _ string, _ int64, lineItemID string) (string, error) {
		return "res_" + lineItemID + "_" + itemID, nil
	}
}

// TestABundleLineReservesEachComponent is the decision's till: two boxes
// reserve two towels and four soaps, confirm both, and the order line keeps
// what a box was made of.
func TestABundleLineReservesEachComponent(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, boxCatalog(variantScript{manageInventory: true}))
	linkOnly(h, map[string][]string{
		testVariantB: {testItemB}, testTowel: {testTowelItem}, testSoap: {testSoapItem},
	})
	reservePerItem(h)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.ElementsMatch(t, []reservedCall{
		{LineItemID: testLineA, ItemID: testTowelItem, LocationID: testLocationID, Quantity: 2},
		{LineItemID: testLineA, ItemID: testSoapItem, LocationID: testLocationID, Quantity: 4},
		{LineItemID: testLineB, ItemID: testItemB, LocationID: testLocationID, Quantity: 1},
	}, h.inventory.reserved, "a box reserves its parts, the line's quantity times each part's units")
	assert.ElementsMatch(t, []string{
		"res_" + testLineA + "_" + testTowelItem, "res_" + testLineA + "_" + testSoapItem,
		"res_" + testLineB + "_" + testItemB,
	}, out.ReservationIDs)
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineA+"_"+testSoapItem),
		"a component's reservation is confirmed as a line's is")

	require.Len(t, h.orders.placed, 1)
	var box orderSnapshotItem
	for _, item := range h.orders.placed[0].Items {
		if item.VariantID == testVariantA {
			box = item
		}
	}
	assert.Equal(t, []orderSnapshotComponent{
		{VariantID: testTowel, Quantity: 1}, {VariantID: testSoap, Quantity: 2},
	}, box.Components, "the order keeps what one box was made of")
}

// TestABundlesComponentsAreReadInARoundOfTheirOwn holds the round trips: the
// parts the cart does not hold are read in one more variant call, and every
// product in the one product call.
func TestABundlesComponentsAreReadInARoundOfTheirOwn(t *testing.T) {
	h := newHarness(t)
	scripts := boxCatalog(variantScript{manageInventory: true})
	var specs []query.GraphSpec
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		specs = append(specs, spec)
		return catalogAnswer(scripts, spec), nil
	}
	linkOnly(h, map[string][]string{
		testVariantB: {testItemB}, testTowel: {testTowelItem}, testSoap: {testSoapItem},
	})
	reservePerItem(h)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, specs, 3, "the cart's variants, the parts, and the products")
	assert.ElementsMatch(t, []string{testVariantA, testVariantB}, specs[0].Filters[FilterIDs])
	assert.Equal(t, []string{testTowel, testSoap}, specs[1].Filters[FilterIDs])
	assert.Equal(t, EntityProduct, specs[2].Entity)
}

// TestACountedComponentWithoutAnItemRefusesTheBundle is ADR 0048's refusal
// asked of a part: a soap the shop counts and links to nothing cannot be
// reserved, so neither can the box.
func TestACountedComponentWithoutAnItemRefusesTheBundle(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, boxCatalog(variantScript{manageInventory: true}))
	linkOnly(h, map[string][]string{testVariantB: {testItemB}, testTowel: {testTowelItem}})

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, h.inventory.reserved, "the refusal is made before any stock is touched")
}

// TestAnUncountedComponentIsNotReserved is ADR 0048's skip asked of a part:
// the towel is reserved, the soap nobody counts is not, and the record names
// the soap so a recovery can tell the skip from a lost trail.
func TestAnUncountedComponentIsNotReserved(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, boxCatalog(variantScript{manageInventory: false}))
	linkOnly(h, map[string][]string{testVariantB: {testItemB}, testTowel: {testTowelItem}})
	reservePerItem(h)

	plan, err := h.wf.prepare(context.Background(), h.input())
	require.NoError(t, err)
	step := &reserveInventoryStep{w: h.wf, plan: plan}
	sc := &workflow.StepContext{Shared: map[string]any{}}

	raw, err := step.Invoke(context.Background(), sc)
	require.NoError(t, err)
	out, ok := raw.(reserveOutput)
	require.True(t, ok)

	assert.Equal(t, []componentRef{{LineItemID: testLineA, VariantID: testSoap}}, out.UnreservedComponents)
	assert.Empty(t, out.Unreserved, "the box's line is traced per part, not as a whole")
	assert.Contains(t, out.Reservations, reservationRef{
		LineItemID: testLineA, ReservationID: "res_" + testLineA + "_" + testTowelItem,
		LocationID: testLocationID, VariantID: testTowel,
	})

	encoded, err := json.Marshal(out)
	require.NoError(t, err)
	require.NoError(t, step.Restore(&workflow.StepContext{Shared: map[string]any{}}, encoded),
		"the record the step writes is the one its recovery accepts")
}

// TestAComponentThatCannotBeReservedUnwindsTheLine is the half-finished line:
// the towel is reserved, the soaps are not there, and the towel goes back.
func TestAComponentThatCannotBeReservedUnwindsTheLine(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, boxCatalog(variantScript{manageInventory: true}))
	linkOnly(h, map[string][]string{
		testVariantB: {testItemB}, testTowel: {testTowelItem}, testSoap: {testSoapItem},
	})
	h.inventory.reserveFn = func(_ context.Context, itemID, _ string, _ int64, lineItemID string) (string, error) {
		if itemID == testSoapItem {
			return "", errors.Conflict(CodeReservationFailed, "no soap")
		}
		return "res_" + lineItemID + "_" + itemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA+"_"+testTowelItem),
		"the towel reserved for a box that cannot be made goes back")
}

// TestTheReserveRecordCountsReservationUnits is Restore's identity with a
// bundle line: one trace per component, and a component's lost trail is
// refused like a line's.
func TestTheReserveRecordCountsReservationUnits(t *testing.T) {
	box := planLine{LineItemID: testLineA, VariantID: testVariantA, Quantity: 2, Components: []planComponent{
		{VariantID: testTowel, Quantity: 1, InventoryItemID: testTowelItem},
		{VariantID: testSoap, Quantity: 2, Unmanaged: true},
	}}
	towel := `{"line_item_id":"` + testLineA + `","reservation_id":"res_t","location_id":"loc","variant_id":"` + testTowel + `"}`

	tests := map[string]struct {
		output    string
		wantError bool
	}{
		"every part traced": {
			output: `{"reservations":[` + towel + `],"unreserved_components":[{"line_item_id":"` +
				testLineA + `","variant_id":"` + testSoap + `"}]}`,
		},
		"the skipped part not named": {
			output: `{"reservations":[` + towel + `]}`, wantError: true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			step := &reserveInventoryStep{plan: &checkoutPlan{CartID: testCartID, Lines: []planLine{box}}}
			err := step.Restore(&workflow.StepContext{Shared: map[string]any{}}, json.RawMessage(tc.output))
			if tc.wantError {
				require.Error(t, err)
				assert.Equal(t, CodeSharedStateInvalid, errors.CodeOf(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestABundleInsideABundleIsRefused holds the second round to its promise: a
// part that reads as a bundle is a catalog ADR 0234 refuses to write, and the
// checkout does not expand it.
func TestABundleInsideABundleIsRefused(t *testing.T) {
	h := newHarness(t)
	scripts := boxCatalog(variantScript{manageInventory: true})
	towel := scripts[testTowel]
	towel.components = []bundlePart{{VariantID: testVariantB, Quantity: 1}}
	scripts[testTowel] = towel
	scriptCatalog(h, scripts)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Empty(t, h.inventory.reserved)
}

// TestACompositionThatDoesNotReadIsRefused keeps a malformed composition from
// becoming a decision, as a malformed flag is kept.
func TestACompositionThatDoesNotReadIsRefused(t *testing.T) {
	for name, composition := range map[string]any{
		"missing":          nil,
		"no units":         []query.Record{{FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: int64(0)}},
		"a fraction":       []any{map[string]any{FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: 1.5}},
		"a part twice":     []query.Record{{FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: int64(1)}, {FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: int64(1)}},
		"past the ceiling": []query.Record{{FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: MaxComponentQuantity + 1}},
	} {
		t.Run(name, func(t *testing.T) {
			record := query.Record{
				query.IDField: testVariantA, FieldTitle: testTitleA, FieldManageInventory: true,
				FieldAllowBackorder: false, FieldProductID: productOf(testVariantA),
			}
			if composition != nil {
				record[FieldBundleComponents] = composition
			}
			_, _, err := readVariantFacts(record)
			require.Error(t, err)
			assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
		})
	}

	_, fact, err := readVariantFacts(query.Record{
		query.IDField: testVariantA, FieldTitle: testTitleA, FieldManageInventory: true,
		FieldAllowBackorder: false, FieldProductID: productOf(testVariantA),
		FieldBundleComponents: []any{map[string]any{
			FieldBundleComponentVariantID: testTowel, FieldBundleComponentQuantity: float64(3),
		}},
	})
	require.NoError(t, err, "a composition that crossed JSON still reads")
	assert.Equal(t, []bundlePart{{VariantID: testTowel, Quantity: 3}}, fact.Components)
}

// TestAPlanWhoseComponentLostItsItemIsRefused is the pairing guard asked of a
// part: a bundle line carries no item of its own, and each counted part that
// refuses backorder has to carry one, or the reserve step would skip it.
func TestAPlanWhoseComponentLostItsItemIsRefused(t *testing.T) {
	plan := &checkoutPlan{
		CartID: testCartID, CurrencyCode: testCurrency, Amount: 1000, Subtotal: 1000,
		Lines: []planLine{{
			LineItemID: testLineA, VariantID: testVariantA, Quantity: 1,
			UnitPrice: 1000, Subtotal: 1000, Total: 1000,
			Components: []planComponent{
				{VariantID: testTowel, Quantity: 1, InventoryItemID: testTowelItem},
				{VariantID: testSoap, Quantity: 2},
			},
		}},
	}

	err := plan.validate()
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))

	plan.Lines[0].Components[1].Unmanaged = true
	require.NoError(t, plan.validate(), "a part nobody counts carries no item, and the box's line none")

	plan.Lines[0].Components[1].Quantity = MaxComponentQuantity + 1
	require.Error(t, plan.validate(), "a part held past the product module's bound is not one it wrote")
}
