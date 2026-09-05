//go:build integration

package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves MULTI-WAREHOUSE order completion with the real modules, a
// real Postgres and the real saga engine.
//
// # Why checkout's unit tests are not enough
//
// The checkout package's unit tests exercise location selection with FAKE
// surfaces: "did the candidates pass through as inventory produced them", "was
// the choice made by fulfillment". Those are the right questions, but what they
// assert is the SHAPE of the calls. What is asserted here is the OUTCOME: were
// the two lines' reservations REALLY opened in two separate warehouses in the
// inventory module's ledger. A fake can catch an implementation that says
// "reserve everything from the same warehouse" through the candidate list; but
// only the real inventory module can say which warehouse the quantity was
// deducted from in the ledger.
//
// # Warehouses and items are set up PER TEST
//
// Package tests run SEQUENTIALLY and share a single database; because a stock
// level is written per (item, location) pair, every test setting up its own item
// is already the rule (see [newStockedVariant]). This file goes one step further
// and sets up its WAREHOUSES too: the scenarios look at the CONTENTS of the
// warehouses and, had they worked on the shared [stockLocationID], the candidate
// list would have become dependent not on other tests' items but on other tests'
// warehouse setup.
//
// # Why the expected amounts are written out by hand
//
// The same reasoning as in the package comment holds: every scenario's subtotal,
// tax and grand total are CONSTANTS computed on paper INSIDE the test. Tax is
// computed per line and rounded down.

// The HAND-computed amounts of the multi-warehouse happy path scenario.
//
// The region is taxed at 20% (2000 basis points), no shipping method is chosen,
// and tax is computed PER LINE:
//
//	line A: 7_500 × 2 = 15_000 subtotal, 15_000 × 20% = 3_000 tax
//	line B: 11_000 × 3 = 33_000 subtotal, 33_000 × 20% = 6_600 tax
//	cart:   15_000 + 33_000 = 48_000 subtotal
//	        3_000 + 6_600 = 9_600 tax
//	        48_000 - 0 + 9_600 + 0 = 57_600 grand total
const (
	multiWarehousePriceA    int64 = 7_500
	multiWarehouseQuantityA int64 = 2
	multiWarehousePriceB    int64 = 11_000
	multiWarehouseQuantityB int64 = 3
	multiWarehouseSubtotal  int64 = 48_000
	multiWarehouseTax       int64 = 9_600
	multiWarehouseTotal     int64 = 57_600
	// multiWarehouseStockA and multiWarehouseStockB are the PHYSICAL quantities
	// in the items' SINGLE warehouses; both are more than the cart asks for, so
	// the place where the scenario stops is NOT insufficient stock.
	multiWarehouseStockA int64 = 6
	multiWarehouseStockB int64 = 9
	// multiWarehouseRemainingA and multiWarehouseRemainingB are the PHYSICAL
	// quantities expected after capture: 6 - 2 and 9 - 3.
	multiWarehouseRemainingA int64 = 4
	multiWarehouseRemainingB int64 = 6
)

// TestEmptyLocationReservesLinesFromDifferentWarehouses proves that a
// multi-warehouse cart turns into an order and that its lines are reserved from
// DIFFERENT warehouses.
//
// The setup makes the single-location assumption impossible: item A's stock is
// only in the first warehouse, item B's stock only in the second. An
// implementation that tries to reserve from a single warehouse MUST blow up on
// one of the lines, so even on its own the "an order was created" assertion says
// something — but it is not enough and the test does not stop there: every
// reservation is read from the REAL inventory module and its location is
// verified. A test that only looked at the order being created would also let
// through an implementation that quietly gathers the reservations into a single
// warehouse.
//
// [checkoutwf.CompleteCartInput.LocationID] is deliberately left EMPTY: that is
// the only way to tell the workflow "you pick the warehouse", and only then does
// the division of labour come into play — the fact of "which warehouses hold
// enough stock" is given by the inventory module, the decision of "which one do
// we ship from" by the fulfillment module.
func TestEmptyLocationReservesLinesFromDifferentWarehouses(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	warehouseA := newWarehouse(ctx, t, "E2E Multi Warehouse A")
	warehouseB := newWarehouse(ctx, t, "E2E Multi Warehouse B")

	variantA, itemA := variantAcrossWarehouses(ctx, t, "E2E Multi Warehouse Product A",
		map[string]int64{taxedCurrency: multiWarehousePriceA},
		map[string]int64{warehouseA: multiWarehouseStockA})
	variantB, itemB := variantAcrossWarehouses(ctx, t, "E2E Multi Warehouse Product B",
		map[string]int64{taxedCurrency: multiWarehousePriceB},
		map[string]int64{warehouseB: multiWarehouseStockB})

	cartID, totals := twoLineCart(ctx, t, customerID, variantA, multiWarehouseQuantityA, variantB, multiWarehouseQuantityB)
	assertTotals(t, totals, expectedTotal{
		subtotal: multiWarehouseSubtotal,
		discount: 0,
		tax:      multiWarehouseTax,
		shipping: 0,
		total:    multiWarehouseTotal,
	}, "after the multi-warehouse cart was prepared")

	// --- the workflow itself ---

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID,
		// The location is DELIBERATELY empty; the whole scenario rests on that emptiness.
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     multiWarehouseTotal,
	})
	require.NoError(t, err,
		"an order must be placeable without a location being declared; an error shows "+
			"that the workflow still insists on a location being declared")
	require.NotEmpty(t, result.OrderID, "the result must carry the created order's ID")
	require.True(t, result.CartCompleted, "the cart must be stamped completed")
	require.True(t, result.ReservationsConfirmed, "the reservations must be confirmed")
	require.Empty(t, result.Warnings,
		"there must be NO warning on the happy path; a warning reports that a module "+
			"blew up after the pivot and that manual repair is needed")

	// --- 1) was the order REALLY created ---

	order, err := orderSvc.GetOrder(ctx, result.OrderID)
	require.NoError(t, err, "the created order must be readable from the order module")
	require.Equal(t, ordermodels.OrderPending, order.Status,
		"the order must come out of this workflow as 'pending'")
	require.Equal(t, multiWarehouseTotal, order.Total,
		"the order's grand total must be the SAME as the cart's")
	require.Len(t, order.Items, 2,
		"the cart's TWO lines must carry over to the order as two lines; the lines "+
			"being reserved from different warehouses does NOT CHANGE the shape of the "+
			"order — a warehouse is a shipping detail, not an invoicing one")

	// --- 2) are the reservations in DIFFERENT warehouses ---
	//
	// This is the real assertion. The reservations are read not from a field the
	// workflow returns but from the REAL inventory module: what is under test is
	// not what the workflow says, but which warehouse the quantity was deducted
	// from in the inventory ledger.
	require.Len(t, result.ReservationIDs, 2,
		"one reservation must be taken for each cart line")
	reservations := readReservations(ctx, t, result.ReservationIDs)

	resA, foundA := reservations[itemA]
	require.True(t, foundA, "item A's reservation must be found")
	resB, foundB := reservations[itemB]
	require.True(t, foundB, "item B's reservation must be found")

	require.Equal(t, warehouseA, resA.LocationID,
		"line A must be reserved from the warehouse that HOLDS its stock; another "+
			"warehouse means the workflow invented a location without ever asking for "+
			"candidates")
	require.Equal(t, warehouseB, resB.LocationID,
		"line B must be reserved from its own warehouse too")
	require.NotEqual(t, resA.LocationID, resB.LocationID,
		"THE TWO LINES MUST NOT BE RESERVED FROM THE SAME WAREHOUSE. This is why this "+
			"file exists: once the single-location assumption is lifted, an order's lines "+
			"must be able to come out of different warehouses. Equality shows that the "+
			"workflow still picks a single warehouse and applies it to every line")

	require.Equal(t, multiWarehouseQuantityA, resA.Quantity, "reservation A must be for the line's quantity")
	require.Equal(t, multiWarehouseQuantityB, resB.Quantity, "reservation B must be for the line's quantity")
	require.Equal(t, inventorymodels.ReservationConfirmed, resA.Status,
		"reservation A must be 'confirmed': it means the sold goods have been deducted from physical stock")
	require.Equal(t, inventorymodels.ReservationConfirmed, resB.Status,
		"reservation B must be 'confirmed' too")

	// --- 3) is the inventory ledger correct PER WAREHOUSE ---
	//
	// The sellable total is not enough on its own: because the two items are
	// counted separately, an "everything was deducted from one warehouse" fault
	// would not show up in the total. That is why the levels are read per
	// warehouse.
	levelA := warehouseLevel(ctx, t, itemA, warehouseA)
	require.Equal(t, multiWarehouseRemainingA, levelA.StockedQuantity,
		"item A's PHYSICAL quantity must go down in its own warehouse (%d - %d)",
		multiWarehouseStockA, multiWarehouseQuantityA)
	require.Equal(t, int64(0), levelA.ReservedQuantity,
		"after confirmation item A's reserved quantity must be zeroed")

	levelB := warehouseLevel(ctx, t, itemB, warehouseB)
	require.Equal(t, multiWarehouseRemainingB, levelB.StockedQuantity,
		"item B's PHYSICAL quantity must go down in its own warehouse (%d - %d)",
		multiWarehouseStockB, multiWarehouseQuantityB)
	require.Equal(t, int64(0), levelB.ReservedQuantity,
		"after confirmation item B's reserved quantity must be zeroed")

	// No level must ever come into existence for the items in ANOTHER warehouse:
	// a reservation cannot create a level that does not exist, and had it done so
	// stock would have been conjured out of nothing.
	require.Len(t, stockLevels(ctx, t, itemA), 1,
		"item A must stay levelled only in its own warehouse")
	require.Len(t, stockLevels(ctx, t, itemB), 1,
		"item B must stay levelled only in its own warehouse")
}

// The constants of the compensation scenario.
//
// The first line's stock is in a SINGLE warehouse and is sufficient. The second
// line's stock is spread over TWO warehouses but neither is enough on its own:
// 1 + 1 = 2 < 3. The setup is deliberate and picks the case that is easiest to
// miss in a multi-warehouse installation — the total stock LOOKS sufficient, but
// because a reservation is made from a single warehouse the order cannot be
// fulfilled. An implementation that looks at the total would have opened an
// order here.
//
// The amounts are not the subject of this scenario: the workflow computes them
// and then stops at the first step, on the second line.
const (
	compensationPrice1             int64 = 9_000
	compensationQuantity1          int64 = 2
	compensationStock1             int64 = 5
	compensationPrice2             int64 = 4_000
	compensationQuantity2          int64 = 3
	compensationStockPerWarehouse2 int64 = 1
)

// TestLineWithNoWarehouseReleasesPreviousReservation proves that a line with no
// warehouse holding enough stock drops the order AND that the previous line's
// reservation is released.
//
// This is the path that breaks most easily with multiple warehouses. In a
// single-warehouse installation the first line would already have blown up and
// there would have been nothing to compensate; with multiple warehouses the
// reserving walks line by line and can stop halfway. A reservation that is not
// released hangs silently here: unsold goods look reserved, and because no order
// consumes them the fault is only noticed at stocktaking.
//
// What is under test is not "an error was returned" but WHAT THE ERROR LEFT
// BEHIND: no order must have been opened and the first item's sellable quantity
// must return to its OLD value.
func TestLineWithNoWarehouseReleasesPreviousReservation(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	warehouseA := newWarehouse(ctx, t, "E2E Compensation Warehouse A")
	warehouseB := newWarehouse(ctx, t, "E2E Compensation Warehouse B")

	variant1, item1 := variantAcrossWarehouses(ctx, t, "E2E Compensation Product 1",
		map[string]int64{taxedCurrency: compensationPrice1},
		map[string]int64{warehouseA: compensationStock1})
	// The second item's stock is spread over TWO warehouses and neither is enough
	// on its own. Not levelling the item at all would also have emptied the list
	// but would have been a weaker setup: "it has no stock" and "it has stock but
	// no warehouse has enough" are different cases, and only the second one is
	// what breaks.
	variant2, item2 := variantAcrossWarehouses(ctx, t, "E2E Compensation Product 2",
		map[string]int64{taxedCurrency: compensationPrice2},
		map[string]int64{warehouseA: compensationStockPerWarehouse2, warehouseB: compensationStockPerWarehouse2})

	cartID, _ := twoLineCart(ctx, t, customerID, variant1, compensationQuantity1, variant2, compensationQuantity2)

	// The line order is this test's PRECONDITION and is therefore asserted: "the
	// previous line's reservation was released" only proves something when the
	// fulfillable line is processed FIRST. If the order is reversed the workflow
	// stops on the first line, takes no reservation at all, and the rest of the
	// test would quietly turn into an empty assertion.
	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.Len(t, cart.Items, 2, "the cart must have two lines")
	require.Equal(t, variant1, cart.Items[0].VariantID,
		"the fulfillable line must come FIRST in the cart; cart lines are read in "+
			"(created_at, id) order and the plan inherits that order")

	previousSellable1 := sellableQuantity(ctx, t, item1)
	require.Equal(t, compensationStock1, previousSellable1,
		"the fixture must have set the first item up with the expected quantity")
	previousSellable2 := sellableQuantity(ctx, t, item2)
	require.Equal(t, 2*compensationStockPerWarehouse2, previousSellable2,
		"the second item's TOTAL sellable quantity is less than the cart asks for but "+
			"is not zero; that is what the setup means")

	// --- the workflow MUST BLOW UP ---

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
	})
	require.Error(t, err,
		"an order must NOT BE PLACEABLE while a line cannot be fulfilled by any "+
			"warehouse; if it were, the store would collect money for goods it cannot deliver")
	require.Equal(t, checkoutwf.CompleteCartResult{}, result,
		"a workflow that returns an error must NOT LEAK a half-built result")

	require.True(t, errors.IsConflict(err),
		"the result must be an errors.Conflict: the input is valid, the state of the world "+
			"is unfavourable, and the client can lower the quantity and try AGAIN. A new "+
			"error class would break the branch the client writes today for insufficient "+
			"stock. Returned error: %v", err)
	require.ErrorContains(t, err, checkoutwf.StepReserveInventory,
		"the error must NAME THE STEP THAT BLEW UP; the step name is written to the "+
			"execution record too")
	require.ErrorContains(t, err, checkoutwf.CodeReservationFailed,
		"when there is NO candidate the outcome is drawn by the cart workflow and the "+
			"code is its own; there is no sub-code to preserve because no module was asked")
	// The fragment below stays Turkish ON PURPOSE. It is produced by
	// internal/workflows/checkout/steps.go, which is still a Turkish file in the
	// ledger; translating the literal here would break the assertion instead of
	// translating anything. It flips when that file is translated.
	require.ErrorContains(t, err, "no location can reserve",
		"the message must NAME the reason. Without this assertion the test would stay "+
			"green in a fault where the candidate list was FULL but the policy eliminated "+
			"all of it — both return a Conflict and both blow up at the same step, but "+
			"their fixes are in different places")
	require.ErrorContains(t, err, item2,
		"the message must write down WHICH item could not be fulfilled; naming the "+
			"second item is at the same time proof that the workflow got past the FIRST "+
			"line — had it stopped on the first line the message would carry that one's ID")

	// --- 1) NO order must have been created ---
	//
	// create_order comes AFTER reserve_inventory; because the stock step blew up
	// it must not have run at all. An order record, even a canceled one, would
	// mean that an order that was never attempted exists.
	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err, "the customer's orders must be readable")

	orders, totalCount := listed.Items, listed.Count
	require.Empty(t, orders, "NO order must have been created")
	require.Zero(t, totalCount, "the counter must be zero too")

	// --- 2) WAS THE PREVIOUS LINE'S RESERVATION RELEASED ---
	//
	// This is the core of the test. The first line had been reserved
	// successfully; once the second line cannot be fulfilled by any warehouse the
	// step has to do its own cleanup. If it does not, 2 units of goods stay
	// reserved forever.
	require.Equal(t, previousSellable1, sellableQuantity(ctx, t, item1),
		"the first item's sellable quantity must return to its OLD value (%d). If it does "+
			"not, the reserved stock hangs and unsold goods become unsellable", previousSellable1)

	level1 := warehouseLevel(ctx, t, item1, warehouseA)
	require.Equal(t, compensationStock1, level1.StockedQuantity,
		"the PHYSICAL quantity must not change at all: releasing erases an unconfirmed "+
			"promise, deducting from stock happens only on confirmation")
	require.Equal(t, int64(0), level1.ReservedQuantity,
		"the first item's reserved quantity must return to ZERO; staying different from "+
			"zero means the promise is still standing — that is, that the compensation "+
			"did not run")

	// --- 3) the second item must NOT have been touched at all ---
	//
	// Had a partial reservation (the 1 unit on hand) been attempted while the
	// candidate list was empty, stock would have been locked temporarily even
	// though the whole cart could not be fulfilled.
	require.Equal(t, previousSellable2, sellableQuantity(ctx, t, item2),
		"the second item's sellable quantity must not change at all; a reservation "+
			"either happens COMPLETELY or does not happen")
	for _, warehouse := range []string{warehouseA, warehouseB} {
		require.Equal(t, int64(0), warehouseLevel(ctx, t, item2, warehouse).ReservedQuantity,
			"no reserved quantity must come into existence for the second item in warehouse %s", warehouse)
	}

	// --- 4) the cart must stay open ---
	cart, err = cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.False(t, cart.Completed(),
		"the cart must NOT be stamped completed; the customer must be able to lower the "+
			"quantity and try again")
}

// The HAND-computed amounts of the declared-location scenario.
//
//	15_000 × 2 = 30_000 subtotal
//	30_000 × 20% = 6_000 tax
//	30_000 - 0 + 6_000 + 0 = 36_000 grand total
const (
	declaredPrice             int64 = 15_000
	declaredQuantity          int64 = 2
	declaredSubtotal          int64 = 30_000
	declaredTax               int64 = 6_000
	declaredTotal             int64 = 36_000
	declaredStockPerWarehouse int64 = 4
	// declaredRemaining is the physical quantity in the declared warehouse after
	// capture: 4 - 2.
	declaredRemaining int64 = 2
)

// TestDeclaredLocationSkipsSelection proves that the old behaviour of a call
// that DECLARES a location is preserved exactly.
//
// The backward-compatibility test is set up so that it means something in a
// multi-warehouse installation: the item's stock is sufficient in BOTH
// warehouses, so had ordering been done there would genuinely have been a
// preference. A priority that the policy would certainly put first is written
// onto the warehouse OUTSIDE the declared one; that way the reservation being
// opened in the declared warehouse becomes proof that the workflow never asked
// the fulfillment module.
//
// The discriminating power is built with the POLICY, NOT with ID order. This
// test used to say "declare the one with the larger ID" and its claim depended
// on the tie-breaking rule; if that rule ever changed the test would not fail,
// it would quietly lose its DISCRIMINATING POWER.
//
// The setup is chosen to cut both ways and that is deliberate: the declared
// warehouse is the one the policy WOULD ELIMINATE (it is bound to another
// region), and the other one is the one the policy would put first. That way,
// had the workflow asked the fulfillment module, it would have FAILED along two
// different paths — asked with a single candidate, elimination would have
// produced an empty set and dropped the order; asked with two candidates, the
// other warehouse would have been chosen. A one-way setup (only "write a
// priority onto the other one") catches the second but would miss the first: the
// instruction path already reduces the candidates to a single element and
// ordering a single candidate would return it again.
//
// A declared location is not a preference but an INSTRUCTION: an admin order
// that is to leave a specific warehouse, or a single-warehouse installation,
// wants the selection never to be made at all.
func TestDeclaredLocationSkipsSelection(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	warehouseOne := newWarehouse(ctx, t, "E2E Declared Warehouse 1")
	warehouseTwo := newWarehouse(ctx, t, "E2E Declared Warehouse 2")

	// The declared warehouse is warehouseOne and it is INVALID according to the
	// policy: it serves only another region. warehouseTwo serves the cart's
	// region and has priority. Had it been asked, the outcome would have been
	// either the order dropping or warehouseTwo; both fail this test.
	declaredWarehouse, policyWouldPick := warehouseOne, warehouseTwo
	warehousePolicy(ctx, t, declaredWarehouse, 0, "reg_a_completely_different_region")
	warehousePolicy(ctx, t, policyWouldPick, -1, taxedRegionID)

	variantID, stockItemID := variantAcrossWarehouses(ctx, t, "E2E Declared Location Product",
		map[string]int64{taxedCurrency: declaredPrice},
		map[string]int64{
			declaredWarehouse: declaredStockPerWarehouse,
			policyWouldPick:   declaredStockPerWarehouse,
		})

	cartID, totals := prepareCart(ctx, t, customerID, variantID, declaredQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: declaredSubtotal,
		discount: 0,
		tax:      declaredTax,
		shipping: 0,
		total:    declaredTotal,
	}, "after the declared-location cart was prepared")

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        declaredWarehouse,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     declaredTotal,
	})
	require.NoError(t, err,
		"a call that declares a location must work as it used to; multi-warehouse "+
			"support must NOT BREAK the old path")
	require.True(t, result.ReservationsConfirmed, "the reservations must be confirmed")
	require.Len(t, result.ReservationIDs, 1, "a single reservation must be taken for a single line")

	reservation, err := inventorySvc.GetReservation(ctx, result.ReservationIDs[0])
	require.NoError(t, err, "the reservation must be readable from the inventory module")
	require.Equal(t, declaredWarehouse, reservation.LocationID,
		"the reservation must be opened in the DECLARED warehouse — even though it is "+
			"INVALID according to the policy. The other warehouse (%s) carries enough "+
			"stock, serves the cart's region and has priority; a warehouse other than the "+
			"declared one means the workflow treated the caller's instruction as a "+
			"'candidate' and quietly changed it", policyWouldPick)

	declaredLevel := warehouseLevel(ctx, t, stockItemID, declaredWarehouse)
	require.Equal(t, declaredRemaining, declaredLevel.StockedQuantity,
		"the declared warehouse's PHYSICAL quantity must go down (%d - %d)",
		declaredStockPerWarehouse, declaredQuantity)
	require.Equal(t, int64(0), declaredLevel.ReservedQuantity,
		"after confirmation the reserved quantity must be zeroed")

	otherLevel := warehouseLevel(ctx, t, stockItemID, policyWouldPick)
	require.Equal(t, declaredStockPerWarehouse, otherLevel.StockedQuantity,
		"the other warehouse's stock must NOT be touched at all")
	require.Equal(t, int64(0), otherLevel.ReservedQuantity,
		"no reserved quantity must come into existence in the other warehouse; it "+
			"coming into existence would have meant the quantity was split across two "+
			"warehouses")
}

// newWarehouse creates a stock location and returns its ID.
//
// The warehouse is set up PER TEST rather than in TestMain (unlike
// [stockLocationID]): this file's scenarios look at the fact of "how much stock
// is in which warehouse", and a shared warehouse would make the candidate list
// dependent on other tests' setup. A counter is appended to the name so that the
// warehouses stay distinguishable in the records even if the same scenario runs
// twice.
//
// The country code is the same as the taxed region's but DOES NOT ENTER THE
// DECISION: the shipping policy looks not at the warehouse's country but at the
// region LINKS in its own schema, and those links are written separately (see
// [warehousePolicy]). The fixture merely stays realistic.
func newWarehouse(ctx context.Context, t *testing.T, name string) string {
	t.Helper()

	seq := fixtureCounter.Add(1)
	location, err := inventorySvc.CreateStockLocation(ctx, inventorysvc.CreateStockLocationInput{
		Name:        fmt.Sprintf("%s #%d", name, seq),
		CountryCode: taxedCountry,
	})
	require.NoError(t, err, "the fixture warehouse could not be created")
	return location.ID
}

// variantAcrossWarehouses sets up a variant with a price, spreads its stock over
// the GIVEN warehouses and returns the IDs of the variant and of the inventory
// item.
//
// Its only difference from [newStockedVariant] is where the stock is written:
// that one uses the single shared warehouse, this one takes a quantity per
// warehouse. Being a separate fixture is deliberate — making the shared fixture
// multi-warehouse would have meant rewriting dozens of scenarios that have no
// interest in warehouses at all.
//
// The warehouses are walked in ID order: ranging over the map would change the
// order in which the levels are written from run to run and a fault would become
// impossible to debug.
func variantAcrossWarehouses(
	ctx context.Context,
	t *testing.T,
	title string,
	prices map[string]int64,
	stocks map[string]int64,
) (variantID, stockItemID string) {
	t.Helper()

	require.NotEmpty(t, stocks,
		"a multi-warehouse fixture must ask for at least one warehouse; with no "+
			"warehouse given the item is never levelled and the scenario would start "+
			"testing the 'it has no stock' case")

	variantID = newVariant(ctx, t, title, prices)

	seq := fixtureCounter.Add(1)
	item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   fmt.Sprintf("E2E-WAREHOUSE-SKU-%d", seq),
		Title: title,
	})
	require.NoError(t, err, "the fixture inventory item could not be created")

	require.NoError(t, productSvc.SetVariantInventoryItem(ctx, variantID, item.ID),
		"the variant could not be linked to the inventory item; without the link the "+
			"workflow treats the variant as stockless")

	for _, locationID := range slices.Sorted(maps.Keys(stocks)) {
		level, err := inventorySvc.SetInventoryLevel(ctx, item.ID, locationID, stocks[locationID])
		require.NoError(t, err, "the fixture stock level could not be written in warehouse %s", locationID)
		require.Equal(t, stocks[locationID], level.Available(),
			"in a new level the sellable quantity must equal the physical quantity; if it "+
				"does not, the fixture is already carrying reserved stock at the outset")
	}

	return variantID, item.ID
}

// twoLineCart opens a cart for a registered customer, adds TWO lines and returns
// the cart's ID together with its computed totals.
//
// [prepareCart] adds a single line; multi-warehouse scenarios want at least two,
// because what is under test is that the lines can be reserved from DIFFERENT
// warehouses. The lines are added in the given order and the order is not a
// detail: cart lines are read in (created_at, id) order, the plan inherits that
// order, and therefore in the compensation scenario the "previous line" is the
// first line added.
//
// The cart is opened for a REGISTERED customer; the reason is the same as in
// [prepareCart]: the scenarios read orders by customer and guest orders would
// see each other's.
func twoLineCart(
	ctx context.Context,
	t *testing.T,
	customerID, firstVariantID string,
	firstQuantity int64,
	secondVariantID string,
	secondQuantity int64,
) (cartID string, totals cartwf.Totals) {
	t.Helper()

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		CustomerID:  customerID,
	})
	require.NoError(t, err, "the fixture cart could not be opened")

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: firstVariantID,
		Quantity:  firstQuantity,
	})
	require.NoError(t, err, "the first line could not be added to the fixture cart")

	second, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: secondVariantID,
		Quantity:  secondQuantity,
	})
	require.NoError(t, err, "the second line could not be added to the fixture cart")

	return cart.CartID, second.Totals
}

// stockLevels returns the item's levels in ALL locations.
//
// [stockLevel] cannot be used here: it requires the item to be levelled in a
// SINGLE location, and this file's items are deliberately spread over more than
// one warehouse.
func stockLevels(ctx context.Context, t *testing.T, stockItemID string) []inventorymodels.InventoryLevel {
	t.Helper()

	levels, err := inventorySvc.ListInventoryLevels(ctx, stockItemID)
	require.NoError(t, err, "the stock levels could not be read")
	return levels
}

// warehouseLevel returns the item's level in a SPECIFIC warehouse.
//
// The level is found by searching, the slice's order is not trusted: the order
// belongs to the inventory module's query and a change in it would lead the test
// to quietly verify the quantities of the wrong warehouse. If the level cannot
// be found the test FAILS — "there is no level in that warehouse" and "the
// quantity in that warehouse is zero" are different things, and assuming the
// second would mean counting a warehouse the reservation never touched as
// verified.
func warehouseLevel(
	ctx context.Context,
	t *testing.T,
	stockItemID, locationID string,
) inventorymodels.InventoryLevel {
	t.Helper()

	for _, level := range stockLevels(ctx, t, stockItemID) {
		if level.LocationID == locationID {
			return level
		}
	}

	require.FailNow(t, "stock level not found",
		"item %s must be levelled in warehouse %s", stockItemID, locationID)
	return inventorymodels.InventoryLevel{}
}

// readReservations reads the reservation IDs returned by the workflow from the
// REAL inventory module and maps them BY INVENTORY ITEM.
//
// The mapping is done by ID, not by the slice's order: the order comes from the
// workflow's line order and an assertion bound to it would have verified the
// wrong line the day that order changed.
//
// The reading is done from the inventory module, NOT from a field the workflow
// returns: the claim under test is that the reservation was opened in the chosen
// warehouse in the inventory module's LEDGER. Looking at the workflow's own
// output would only verify what the workflow says.
func readReservations(
	ctx context.Context,
	t *testing.T,
	ids []string,
) map[string]inventorymodels.Reservation {
	t.Helper()

	byItem := make(map[string]inventorymodels.Reservation, len(ids))
	for _, id := range ids {
		reservation, err := inventorySvc.GetReservation(ctx, id)
		require.NoError(t, err, "reservation %s could not be read from the inventory module", id)
		_, duplicate := byItem[reservation.InventoryItemID]
		require.False(t, duplicate,
			"TWO reservations were taken for the same inventory item (%s); every cart "+
				"line must take a single reservation", reservation.InventoryItemID)
		byItem[reservation.InventoryItemID] = reservation
	}
	return byItem
}

// warehousePolicy writes a shipping policy onto a warehouse.
//
// If the region list is given EMPTY the warehouse serves all regions; the only
// thing written is the priority. The fixture calls the fulfillment module's REAL
// service — writing the policy straight into the table would skip the service's
// validation and its transaction boundary and would set up a state that cannot
// arise in production.
func warehousePolicy(ctx context.Context, t *testing.T, warehouseID string, priority int64, regions ...string) {
	t.Helper()

	_, err := shippingSvc.SetShippingLocation(ctx, fulfillmentsvc.SetShippingLocationInput{
		LocationID: warehouseID,
		Priority:   priority,
		RegionIDs:  regions,
	})
	require.NoError(t, err, "the warehouse shipping policy could not be written: %s", warehouseID)
}

// The HAND-computed amounts of the policy scenarios.
//
//	9_000 × 2 = 18_000 subtotal
//	18_000 × 20% = 3_600 tax
//	18_000 - 0 + 3_600 + 0 = 21_600 grand total
const (
	policyPrice             int64 = 9_000
	policyQuantity          int64 = 2
	policySubtotal          int64 = 18_000
	policyTax               int64 = 3_600
	policyTotal             int64 = 21_600
	policyStockPerWarehouse int64 = 5
	// policyRemaining is the physical quantity in the chosen warehouse after
	// capture: 5 - 2.
	policyRemaining int64 = 3
)

// TestPolicySelectsPrioritizedWarehouse proves that the PRIORITY written by the
// operator enters the decision in the real stack.
//
// The setup is chosen so that the outcome cannot be explained by any other rule:
// both warehouses carry enough stock (so the stock fact makes both of them
// candidates), both serve all regions (so elimination drops neither) and the
// warehouse given the priority is the one the tie-breaking rule (smaller ID
// first) WOULD NOT SELECT. Only one explanation is left: the ordering was
// determined by the priority.
//
// The ID comparison is made during the run. The assumption "the second one
// created has the larger ID" would not fail the test the day the ID generator
// changes, it would quietly make it meaningless — so a MEASUREMENT is used
// rather than an assumption.
func TestPolicySelectsPrioritizedWarehouse(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	warehouseOne := newWarehouse(ctx, t, "E2E Policy Priority 1")
	warehouseTwo := newWarehouse(ctx, t, "E2E Policy Priority 2")

	smallerID, largerID := warehouseOne, warehouseTwo
	if largerID < smallerID {
		smallerID, largerID = largerID, smallerID
	}

	// The priority is written onto the one with the LARGER ID: the tie-breaking
	// rule would put it LAST, the policy pulls it first.
	warehousePolicy(ctx, t, largerID, -1)

	variantID, stockItemID := variantAcrossWarehouses(ctx, t, "E2E Policy Priority Product",
		map[string]int64{taxedCurrency: policyPrice},
		map[string]int64{
			smallerID: policyStockPerWarehouse,
			largerID:  policyStockPerWarehouse,
		})

	cartID, totals := prepareCart(ctx, t, customerID, variantID, policyQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: policySubtotal,
		discount: 0,
		tax:      policyTax,
		shipping: 0,
		total:    policyTotal,
	}, "after the policy cart was prepared")

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     policyTotal,
	})
	require.NoError(t, err, "an order must be placeable while both warehouses are sufficient: %v", err)
	require.Len(t, result.ReservationIDs, 1, "a single reservation must be taken for a single line")

	reservation, err := inventorySvc.GetReservation(ctx, result.ReservationIDs[0])
	require.NoError(t, err, "the reservation must be readable from the inventory module")
	require.Equal(t, largerID, reservation.LocationID,
		"the reservation must be opened in the PRIORITIZED warehouse. The one with the "+
			"smaller ID (%s) also carries enough stock and the tie-breaking rule would "+
			"have selected it; the outcome being that one means the priority written by "+
			"the operator never entered the decision at all",
		smallerID)

	selectedLevel := warehouseLevel(ctx, t, stockItemID, largerID)
	require.Equal(t, policyRemaining, selectedLevel.StockedQuantity,
		"the prioritized warehouse's PHYSICAL quantity must go down (%d - %d)",
		policyStockPerWarehouse, policyQuantity)

	otherLevel := warehouseLevel(ctx, t, stockItemID, smallerID)
	require.Equal(t, policyStockPerWarehouse, otherLevel.StockedQuantity,
		"the other warehouse's stock must NOT be touched at all")
}

// TestPolicyEliminatesOutOfScopeWarehouse proves that the region link is a
// CONSTRAINT, that is, that it DROPS a warehouse from the candidate list.
//
// The difference matters and the test's setup measures it: had the eliminated
// warehouse merely been "pushed to the end", the order would still have come out
// of the other one, so the outcome would have been the same. That is why this
// test does not exercise elimination on its own; its sibling
// [TestNoWarehouseServingTheRegionDropsTheOrder] shows that an eliminated set
// LEAVES NO place to fall back to, and together the two establish the
// "constraint" claim.
//
// The eliminated warehouse is the one that has been put FIRST by priority: had
// elimination not run BEFORE ordering, the outcome would have been that
// warehouse.
func TestPolicyEliminatesOutOfScopeWarehouse(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	outOfScope := newWarehouse(ctx, t, "E2E Policy Out Of Scope")
	inScope := newWarehouse(ctx, t, "E2E Policy In Scope")

	// The out-of-scope warehouse is both PRIORITIZED and bound to another region.
	// Had elimination not run, the priority would have put it first.
	warehousePolicy(ctx, t, outOfScope, -5, "reg_another_region")
	warehousePolicy(ctx, t, inScope, 0, taxedRegionID)

	variantID, stockItemID := variantAcrossWarehouses(ctx, t, "E2E Policy Scope Product",
		map[string]int64{taxedCurrency: policyPrice},
		map[string]int64{
			outOfScope: policyStockPerWarehouse,
			inScope:    policyStockPerWarehouse,
		})

	cartID, _ := prepareCart(ctx, t, customerID, variantID, policyQuantity)

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     policyTotal,
	})
	require.NoError(t, err, "an order must be placeable while the in-scope warehouse is sufficient: %v", err)
	require.Len(t, result.ReservationIDs, 1, "a single reservation must be taken for a single line")

	reservation, err := inventorySvc.GetReservation(ctx, result.ReservationIDs[0])
	require.NoError(t, err, "the reservation must be readable from the inventory module")
	require.Equal(t, inScope, reservation.LocationID,
		"the reservation must be opened in the warehouse that SERVES the cart's region. "+
			"The other warehouse (%s) both carries enough stock and has the higher "+
			"priority; the outcome being that one means elimination ran after ordering or "+
			"never ran at all",
		outOfScope)

	outOfScopeLevel := warehouseLevel(ctx, t, stockItemID, outOfScope)
	require.Equal(t, policyStockPerWarehouse, outOfScopeLevel.StockedQuantity,
		"the out-of-scope warehouse's stock must NOT be touched at all")
	require.Equal(t, int64(0), outOfScopeLevel.ReservedQuantity,
		"no reserved quantity must come into existence in the out-of-scope warehouse")
}

// TestNoWarehouseServingTheRegionDropsTheOrder MEASURES the price of a
// misconfigured scope: the order drops even though stock is full.
//
// This is the heaviest price the feature has accepted and the test's job is not
// to hide it. Both warehouses are more than stocked; the only thing missing is
// that NEITHER of them is bound to the cart's region — this is the state that
// arises when the operator writes down a region ID that does not exist, or
// deletes a region and opens it again (a new record gets a new ID).
//
// The test's real claim is that the error is DIAGNOSABLE: the code must be
// different from the insufficient-stock one and the error must write down which
// regions the candidates are ACTUALLY bound to. A dead region ID can only be
// seen that way — with an error that only said "no warehouse serves it" the
// operator could not have noticed that the IDs had diverged.
//
// The LIMIT of the claim must be written down here too: the test reads the error
// OBJECT, not the HTTP body. Only the CODE reaches the storefront client; the
// text carrying the region listing stays in the server log and in the execution
// record.
func TestNoWarehouseServingTheRegionDropsTheOrder(t *testing.T) {
	ctx := t.Context()

	const deadRegion = "reg_deleted_region"

	customerID, email := newCustomer(ctx, t)
	warehouseOne := newWarehouse(ctx, t, "E2E Scopeless Warehouse 1")
	warehouseTwo := newWarehouse(ctx, t, "E2E Scopeless Warehouse 2")

	warehousePolicy(ctx, t, warehouseOne, 0, deadRegion)
	warehousePolicy(ctx, t, warehouseTwo, 0, deadRegion)

	variantID, stockItemID := variantAcrossWarehouses(ctx, t, "E2E Scopeless Product",
		map[string]int64{taxedCurrency: policyPrice},
		map[string]int64{
			warehouseOne: policyStockPerWarehouse,
			warehouseTwo: policyStockPerWarehouse,
		})

	previousSellable := sellableQuantity(ctx, t, stockItemID)
	require.Equal(t, 2*policyStockPerWarehouse, previousSellable,
		"what the setup means is that the stock is FULL; the place where the scenario "+
			"stops is not stock")

	cartID, _ := prepareCart(ctx, t, customerID, variantID, policyQuantity)

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     policyTotal,
	})
	require.Error(t, err,
		"an order must not be placeable if no candidate serves the cart's region")
	require.Equal(t, checkoutwf.CompleteCartResult{}, result,
		"a workflow that returns an error must NOT LEAK a half-built result")

	require.True(t, errors.IsConflict(err),
		"the class must be errors.Conflict: there is nothing to fix in the request and "+
			"the engine's default retry predicate DOES NOT RETRY this class — an "+
			"eliminated candidate set does not change by being tried again. Returned "+
			"error: %v", err)
	require.Equal(t, fulfillmentsvc.CodeNoServiceableLocation, errors.CodeOf(err),
		"the code must be SEPARATE from the insufficient-stock one and must REACH the "+
			"storefront: a step error preserves the sub-error's code and the transport "+
			"layer writes the OUTERMOST code into the body. An assertion that walks the "+
			"chain would not be enough here — it would find the sub-error and stay green "+
			"even under a wrapping that overwrites the code. Returned error: %v", err)
	require.ErrorContains(t, err, deadRegion,
		"the message must write down the region the candidates are ACTUALLY bound to; "+
			"only that way can a dead region ID be diagnosed")
	require.ErrorContains(t, err, taxedRegionID,
		"the message must also write down which region was being looked for; an operator "+
			"who does not see the two IDs side by side cannot notice the divergence")

	// --- what it left behind ---

	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err, "the customer's orders must be readable")

	orders, totalCount := listed.Items, listed.Count
	require.Zero(t, totalCount, "NO order must have been created")
	require.Empty(t, orders, "the order list must be empty")

	require.Equal(t, previousSellable, sellableQuantity(ctx, t, stockItemID),
		"no reservation must be opened in any warehouse; elimination runs BEFORE reserving")

	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.False(t, cart.Completed(), "the cart must NOT be stamped completed")
}
