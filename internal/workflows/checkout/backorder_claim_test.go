package checkout

// This file holds ADR 0392 at the till: the last step records a line the stock
// step let through without stock as a claim on the order line at its position,
// naming the warehouses the order may ship from, and settles it against the
// write-offs it reads after the claim.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// backorderHarness is a cart whose line A permits backorder and no warehouse
// can cover, and whose line B is reserved at the east warehouse; both
// warehouses are open, and fulfillment ranks the greatest id first.
func backorderHarness(t *testing.T, backordered string) (*harness, CompleteCartInput) {
	t.Helper()

	h := newHarness(t)
	scripts := map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true},
		testVariantB: {title: testTitleB, manageInventory: true},
	}
	script := scripts[backordered]
	script.allowBackorder = true
	scripts[backordered] = script
	scriptCatalog(h, scripts)

	item := map[string]string{testVariantA: testItemA, testVariantB: testItemB}[backordered]
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == item {
			return []string{}, nil
		}
		return []string{testLocationEast}, nil
	}
	h.inventory.openFn = func() ([]string, error) { return []string{testLocationEast, testLocationWest}, nil }
	h.fulfillment.rankFn = rankByGreatestID

	in := h.input()
	in.LocationID = ""

	return h, in
}

// linesAnswer is the order module's answer for the default cart: line A of two
// at position 0, line B of one at position 1.
func linesAnswer(canceledA int64) json.RawMessage {
	raw, _ := json.Marshal([]orderLineAnswer{
		{LineItemID: orderLineID(0), Bought: 2, Canceled: canceledA, VariantID: testVariantA},
		{LineItemID: orderLineID(1), Bought: 1, VariantID: testVariantB},
	})
	return raw
}

// TestABackorderedLineIsClaimedAtItsPosition: the claim names the order line at
// the cart line's position, with its units, item and ranked warehouses.
func TestABackorderedLineIsClaimedAtItsPosition(t *testing.T) {
	for name, tc := range map[string]struct {
		variant, item, orderLine string
		quantity                 int64
	}{
		"the first line":  {testVariantA, testItemA, orderLineID(0), 2},
		"the second line": {testVariantB, testItemB, orderLineID(1), 1},
	} {
		t.Run(name, func(t *testing.T) {
			h, in := backorderHarness(t, tc.variant)

			out, err := h.wf.CompleteCart(context.Background(), in)
			require.NoError(t, err)

			assert.Empty(t, out.Warnings)
			assert.Equal(t, []claimCall{{
				ItemID: tc.item, OrderID: testOrderID, OrderLineItemID: tc.orderLine, Quantity: tc.quantity,
				LocationIDs: []string{testLocationWest, testLocationEast},
			}}, h.inventory.claims)
			assert.Equal(t, []settleCall{{OrderLineItemID: tc.orderLine, Bought: tc.quantity}}, h.inventory.settles,
				"nothing was written off, so nothing is withdrawn")
		})
	}
}

// TestALineNoWarehouseHoldsIsNotClaimedWhenItIsNotCounted: a line the merchant
// does not count, though still linked to an item, and a backorder line linked
// to no item are owed nothing a warehouse can hold.
func TestALineNoWarehouseHoldsIsNotClaimedWhenItIsNotCounted(t *testing.T) {
	for name, tc := range map[string]struct {
		scripts map[string]variantScript
		links   map[string][]string
	}{
		"an uncounted line still linked": {
			scripts: map[string]variantScript{
				testVariantA: {title: testTitleA, allowBackorder: true},
				testVariantB: {title: testTitleB, manageInventory: true},
			},
			links: map[string][]string{testVariantA: {testItemA}, testVariantB: {testItemB}},
		},
		"a line linked to no item": {
			scripts: map[string]variantScript{
				testVariantA: {title: testTitleA, manageInventory: true, allowBackorder: true},
				testVariantB: {title: testTitleB, manageInventory: true},
			},
			links: map[string][]string{testVariantB: {testItemB}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			scriptCatalog(h, tc.scripts)
			linkOnly(h, tc.links)

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.NoError(t, err)

			assert.Empty(t, h.inventory.claims)
			assert.Zero(t, h.orders.linesReads, "no claim, no question to the order")
		})
	}
}

// TestAnUncountedLineIsNotClaimedEvenWithAnItem runs the last step over a
// record whose uncounted line carries an item and went out unreserved: the flag,
// not the item, decides that nothing is owed.
func TestAnUncountedLineIsNotClaimedEvenWithAnItem(t *testing.T) {
	h := newHarness(t)
	h.orders.linesFn = func(int) (json.RawMessage, error) { return linesAnswer(0), nil }
	plan := &checkoutPlan{
		CartID: testCartID, RegionID: testRegionID, LocationID: testLocationID,
		Lines: []planLine{
			{LineItemID: testLineA, VariantID: testVariantA, InventoryItemID: testItemA, Quantity: 2, Unmanaged: true},
			{LineItemID: testLineB, VariantID: testVariantB, InventoryItemID: testItemB, Quantity: 1},
		},
	}
	sc := &workflow.StepContext{Shared: map[string]any{
		sharedOrderID: testOrderID, sharedCollectionID: "pcol_1", sharedSessionID: "ps_1", sharedPaymentID: "pay_1",
		sharedUnreserved: unreservedRef{Lines: []string{testLineA}},
	}}

	_, err := (&clearCartStep{w: h.wf, plan: plan}).Invoke(context.Background(), sc)
	require.NoError(t, err)

	assert.Empty(t, h.inventory.claims)
}

// TestARecoveryNarrowsTheWarehousesToTheChannel runs the last step alone, as a
// recovery does after Restore: the stock step's channel set is not in memory,
// and the claim is still ranked over the warehouses the channel is served by.
func TestARecoveryNarrowsTheWarehousesToTheChannel(t *testing.T) {
	h := newHarness(t)
	h.inventory.openFn = func() ([]string, error) { return []string{testLocationEast, testLocationWest}, nil }
	h.fulfillment.rankFn = rankByGreatestID
	h.links.served = map[string][]string{"sc_web": {testLocationEast}}
	h.orders.linesFn = func(int) (json.RawMessage, error) { return linesAnswer(0), nil }

	plan := &checkoutPlan{
		CartID: testCartID, RegionID: testRegionID, SalesChannelIDs: []string{"sc_web"},
		Lines: []planLine{
			{LineItemID: testLineA, VariantID: testVariantA, InventoryItemID: testItemA, Quantity: 2, AllowBackorder: true},
			{LineItemID: testLineB, VariantID: testVariantB, InventoryItemID: testItemB, Quantity: 1},
		},
	}
	sc := &workflow.StepContext{Shared: map[string]any{
		sharedOrderID: testOrderID, sharedCollectionID: "pcol_1", sharedSessionID: "ps_1", sharedPaymentID: "pay_1",
		sharedReservations: []reservationRef{{LineItemID: testLineB, ReservationID: "res_b", LocationID: testLocationEast}},
	}}
	reserve := &reserveInventoryStep{w: h.wf, plan: plan}
	output, err := json.Marshal(reserveOutput{
		Reservations: []reservationRef{{LineItemID: testLineB, ReservationID: "res_b", LocationID: testLocationEast}},
		Unreserved:   []string{testLineA},
	})
	require.NoError(t, err)
	require.NoError(t, reserve.Restore(sc, output))

	_, err = (&clearCartStep{w: h.wf, plan: plan}).Invoke(context.Background(), sc)
	require.NoError(t, err)

	assert.Equal(t, [][]string{{testLocationEast}}, h.fulfillment.offered, "only the channel's warehouse is ranked")
	require.Len(t, h.inventory.claims, 1)
	assert.Equal(t, []string{testLocationEast}, h.inventory.claims[0].LocationIDs)
}

// TestAnOrderThatDoesNotFollowTheCartRecordsNoClaim: an answer shorter than the
// cart, or one whose line bought another quantity, is not guessed at.
func TestAnOrderThatDoesNotFollowTheCartRecordsNoClaim(t *testing.T) {
	short, _ := json.Marshal([]orderLineAnswer{{LineItemID: orderLineID(0), Bought: 2, VariantID: testVariantA}})
	other, _ := json.Marshal([]orderLineAnswer{
		{LineItemID: orderLineID(0), Bought: 3, VariantID: testVariantA},
		{LineItemID: orderLineID(1), Bought: 1, VariantID: testVariantB},
	})

	for name, answer := range map[string]json.RawMessage{"a short answer": short, "another quantity": other} {
		t.Run(name, func(t *testing.T) {
			h, in := backorderHarness(t, testVariantA)
			h.orders.linesFn = func(int) (json.RawMessage, error) { return answer, nil }

			out, err := h.wf.CompleteCart(context.Background(), in)
			require.NoError(t, err)

			assert.Empty(t, h.inventory.claims)
			assert.Contains(t, out.Warnings, "the order's lines do not follow the cart's; no backorder was recorded")
		})
	}
}

// TestADeclaredLocationIsTheClaimsOnlyWarehouse: an order naming its warehouse
// claims there, and fulfillment is not asked.
func TestADeclaredLocationIsTheClaimsOnlyWarehouse(t *testing.T) {
	h, _ := backorderHarness(t, testVariantA)
	h.inventory.reserveFn = func(_ context.Context, itemID, _ string, _ int64, lineItemID string) (string, error) {
		if itemID == testItemA {
			return "", errors.Conflict(CodeReservationFailed, "no stock")
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.inventory.claims, 1)
	assert.Equal(t, []string{testLocationID}, h.inventory.claims[0].LocationIDs)
	assert.Zero(t, h.rec.count("fulfillment:rank_locations"))
	assert.Zero(t, h.rec.count("inventory:open_locations"))
}

// TestAClaimThatCannotBeRankedNamesNoWarehouse: the order stands, the claim is
// recorded naming none, and the answer says why.
func TestAClaimThatCannotBeRankedNamesNoWarehouse(t *testing.T) {
	for name, script := range map[string]func(h *harness){
		"the warehouses cannot be read": func(h *harness) {
			h.inventory.openFn = func() ([]string, error) { return nil, errors.New("unreachable") }
		},
		"fulfillment ranks none": func(h *harness) {
			h.fulfillment.rankFn = func(_ context.Context, _ string, candidates []string) ([]string, error) {
				if len(candidates) > 1 {
					return nil, errors.Conflict("fulfillment_no_location_serves_region", "none serves the region")
				}
				return candidates, nil
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, in := backorderHarness(t, testVariantA)
			script(h)

			out, err := h.wf.CompleteCart(context.Background(), in)
			require.NoError(t, err)

			require.Len(t, h.inventory.claims, 1)
			assert.Empty(t, h.inventory.claims[0].LocationIDs)
			require.Len(t, out.Warnings, 1)
			assert.Contains(t, out.Warnings[0], "could not be ranked")
		})
	}
}

// TestAClaimThatFailsIsAWarningAndTheConfirmsRun: a claim the module refuses is
// in the answer, its line is not settled, and the reservations are confirmed.
func TestAClaimThatFailsIsAWarningAndTheConfirmsRun(t *testing.T) {
	h, in := backorderHarness(t, testVariantA)
	h.inventory.claimFn = func(string, string, int64, []string) error { return errors.New("the database is gone") }

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Empty(t, h.inventory.settles)
	require.Len(t, out.Warnings, 1)
	assert.Contains(t, out.Warnings[0], "could not be recorded")
	assert.True(t, out.ReservationsConfirmed)
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineB))
}

// TestABundleOwesOneClaimPerItem: a box whose two parts are one item owes the
// sum of both, as one claim.
func TestABundleOwesOneClaimPerItem(t *testing.T) {
	h := newHarness(t)
	towel := variantScript{manageInventory: true, allowBackorder: true}
	catalog := boxCatalog(towel)
	part := catalog[testTowel]
	part.allowBackorder = true
	catalog[testTowel] = part
	scriptCatalog(h, catalog)
	linkOnly(h, map[string][]string{testVariantB: {testItemB}, testTowel: {"inv_cotton"}, testSoap: {"inv_cotton"}})
	h.inventory.reserveFn = func(_ context.Context, itemID, _ string, _ int64, lineItemID string) (string, error) {
		if itemID == "inv_cotton" {
			return "", errors.Conflict(CodeReservationFailed, "no stock")
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.inventory.claims, 1)
	assert.Equal(t, "inv_cotton", h.inventory.claims[0].ItemID)
	assert.Equal(t, int64(6), h.inventory.claims[0].Quantity, "two boxes of a towel and two soaps")
	assert.Equal(t, []settleCall{{OrderLineItemID: orderLineID(0), Bought: 2}}, h.inventory.settles)
}

// TestAClaimSeesAWriteOffMadeBeforeIt: the write-offs are read again after the
// claim, so one made while the checkout finished withdraws what it wrote off.
func TestAClaimSeesAWriteOffMadeBeforeIt(t *testing.T) {
	h, in := backorderHarness(t, testVariantA)
	h.orders.linesFn = func(read int) (json.RawMessage, error) {
		if read == 1 {
			return linesAnswer(0), nil
		}
		return linesAnswer(1), nil
	}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, []settleCall{{OrderLineItemID: orderLineID(0), Bought: 2, Window: 1}}, h.inventory.settles)
	calls := h.rec.snapshot()
	assert.Less(t, slices.Index(calls, "inventory:claim:"+orderLineID(0)), slices.Index(calls, "inventory:settle:"+orderLineID(0)))
}

// TestClaimsComeBeforeTheConfirm: whenever a sale of the order exists, its
// claims do too.
func TestClaimsComeBeforeTheConfirm(t *testing.T) {
	h, in := backorderHarness(t, testVariantA)

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	calls := h.rec.snapshot()
	claimed := slices.Index(calls, "inventory:claim:"+orderLineID(0))
	confirmed := slices.Index(calls, "inventory:confirm:res_"+testLineB)
	require.NotEqual(t, -1, claimed)
	require.NotEqual(t, -1, confirmed)
	assert.Less(t, claimed, confirmed)
}

// TestAClaimIsSettledAgainstTheOrdersLiveParcels: the order exists from the
// pivot, so a parcel may hold the backordered line before the last step runs —
// or before a recovery runs it again. Two bought, both in a live parcel, one
// written off: nothing will stay behind, so nothing is withdrawn.
func TestAClaimIsSettledAgainstTheOrdersLiveParcels(t *testing.T) {
	for name, tc := range map[string]struct {
		held   int64
		window int64
	}{
		"a parcel holds both units":            {held: 2, window: 0},
		"a parcel holds one unit":              {held: 1, window: 1},
		"parcels hold more than the line sold": {held: 3, window: 0},
	} {
		t.Run(name, func(t *testing.T) {
			h, in := backorderHarness(t, testVariantA)
			h.orders.linesFn = func(int) (json.RawMessage, error) { return linesAnswer(1), nil }
			h.links.listManyFn = func(ctx context.Context, name string, ids []string) (map[string][]string, error) {
				if name == LinkOrderFulfillment {
					return map[string][]string{testOrderID: {"ful_1"}}, nil
				}
				return defaultLinks(ctx, name, ids)
			}
			var asked []string
			h.fulfillment.committedFn = func(ids []string) (map[string]int64, error) {
				asked = ids
				return map[string]int64{orderLineID(0): tc.held}, nil
			}

			out, err := h.wf.CompleteCart(context.Background(), in)
			require.NoError(t, err)

			assert.Empty(t, out.Warnings)
			assert.Equal(t, []string{"ful_1"}, asked, "the order's own parcels")
			assert.Equal(t, []settleCall{{OrderLineItemID: orderLineID(0), Bought: 2, Window: tc.window}},
				h.inventory.settles)
		})
	}
}

// TestAClaimWhoseParcelsCannotBeReadIsNotSettled: without the parcels the
// withdrawal could take units a parcel will ship, so the line is not settled
// and the answer says so.
func TestAClaimWhoseParcelsCannotBeReadIsNotSettled(t *testing.T) {
	h, in := backorderHarness(t, testVariantA)
	h.links.listManyFn = func(ctx context.Context, name string, ids []string) (map[string][]string, error) {
		if name == LinkOrderFulfillment {
			return map[string][]string{testOrderID: {"ful_1"}}, nil
		}
		return defaultLinks(ctx, name, ids)
	}
	h.fulfillment.committedFn = func([]string) (map[string]int64, error) { return nil, errors.New("unreachable") }

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	require.Len(t, h.inventory.claims, 1)
	assert.Empty(t, h.inventory.settles)
	require.Len(t, out.Warnings, 1)
	assert.Contains(t, out.Warnings[0], "parcels could not be read")
}

// TestAnAddOnAheadOfABackorderedLineIsNotItsOrderLine: the order keeps a root
// line's add-ons after the roots (ADR 0229), so the cart [P1, an add-on of P1,
// P2] is the order [P1, P2, add-on]. Only the position the cart line has in the
// ORDER names P2's line: an add-on selling P2's variant and quantity would take
// P2's claim, and one selling another would make the order look unlike the cart.
func TestAnAddOnAheadOfABackorderedLineIsNotItsOrderLine(t *testing.T) {
	for name, addOn := range map[string]struct {
		variant  string
		quantity int64
	}{
		"the add-on sells P2's variant": {testVariantB, 1},
		"the add-on sells another":      {testVariantA, 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.orders.linesFn = func(int) (json.RawMessage, error) {
				raw, _ := json.Marshal([]orderLineAnswer{
					{LineItemID: "oli_p1", Bought: 1, VariantID: testVariantA},
					{LineItemID: "oli_p2", Bought: 1, VariantID: testVariantB},
					{LineItemID: "oli_addon", Bought: addOn.quantity, VariantID: addOn.variant},
				})
				return raw, nil
			}
			plan := &checkoutPlan{
				CartID: testCartID, RegionID: testRegionID, LocationID: testLocationID,
				Lines: []planLine{
					{LineItemID: "li_p1", VariantID: testVariantA, InventoryItemID: testItemA, Quantity: 1},
					{
						LineItemID: "li_addon", ParentLineItemID: "li_p1", VariantID: addOn.variant,
						InventoryItemID: testItemB, Quantity: addOn.quantity,
					},
					{
						LineItemID: "li_p2", VariantID: testVariantB, InventoryItemID: testItemB, Quantity: 1,
						AllowBackorder: true,
					},
				},
			}
			sc := &workflow.StepContext{Shared: map[string]any{
				sharedOrderID: testOrderID, sharedCollectionID: "pcol_1", sharedSessionID: "ps_1",
				sharedPaymentID:  "pay_1",
				sharedUnreserved: unreservedRef{Lines: []string{"li_p2"}},
			}}

			_, err := (&clearCartStep{w: h.wf, plan: plan}).Invoke(context.Background(), sc)
			require.NoError(t, err)

			require.Len(t, h.inventory.claims, 1)
			assert.Equal(t, "oli_p2", h.inventory.claims[0].OrderLineItemID)
		})
	}
}
