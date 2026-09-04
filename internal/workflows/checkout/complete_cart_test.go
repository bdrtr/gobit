package checkout

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// indexOf returns the position of a call in the record; -1 when it is absent.
func indexOf(calls []string, call string) int {
	return slices.Index(calls, call)
}

// TestRecoveryDefinitionCarriesTheRecordedSteps pins the step names recovery
// relies on, and their ORDER.
//
// Recovery rests on the engine comparing the step NAME in the record with the
// one in the definition (see workflow.Recoverer): if the name does not match, a
// half-finished saga can NEVER be recovered and all the operator is left with
// is "manual intervention". So this sequence is not a convenience, it is the
// precondition of recovery.
//
// The steps are built in a single place (sagaSteps) but the order is still
// nailed down here: an edit that changes the order makes the half-finished
// records of RUNNING installations unrecoverable — the code compiles, the tests
// stay green, and the bill only arrives in production, at exactly the worst
// moment.
func TestRecoveryDefinitionCarriesTheRecordedSteps(t *testing.T) {
	h := newHarness(t)

	plan, err := json.Marshal(checkoutPlan{CartID: testCartID, CurrencyCode: testCurrency})
	require.NoError(t, err)

	wf, err := h.wf.RecoveryWorkflow(plan)
	require.NoError(t, err)

	assert.Equal(t, WorkflowName, wf.Name)

	names := make([]string, 0, len(wf.Steps))
	for _, step := range wf.Steps {
		names = append(names, step.Name())
	}
	assert.Equal(t, []string{
		StepReserveInventory,
		StepCreateOrder,
		StepAuthorizePayment,
		StepCapturePayment,
		StepClearCart,
	}, names, "the recovery definition must carry the name and the ORDER of the recorded steps")
}

// TestRecoveryDefinitionRejectsAnUndecodablePlan pins that recovery never even
// starts when the input of the record cannot be read.
func TestRecoveryDefinitionRejectsAnUndecodablePlan(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.RecoveryWorkflow(json.RawMessage(`{broken`))

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeInvalidInput), "error: %v", err)
}

// TestRecoveryDefinitionRejectsAnEmptyPlan pins that decoding the JSON is NOT
// ENOUGH.
//
// `{}` decodes too, and a chain built from a plan that has no cart identifier
// calls its compensations without one: a recovery that says "I left nothing
// behind" while releasing no reservation at all shows the operator half-done
// work as if it had been CLEANED UP.
func TestRecoveryDefinitionRejectsAnEmptyPlan(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.RecoveryWorkflow(json.RawMessage(`{}`))

	require.Error(t, err)
	assert.True(t, hasCode(err, CodeInvalidInput), "error: %v", err)
}

// TestRecoveryDefinitionRestoresThePlan verifies that the fields of the plan
// come back from the record: the compensations read the cart identifier FROM
// THERE.
func TestRecoveryDefinitionRestoresThePlan(t *testing.T) {
	h := newHarness(t)

	plan, err := json.Marshal(checkoutPlan{
		CartID:       testCartID,
		CurrencyCode: testCurrency,
		Amount:       testAmount,
		Lines:        []planLine{{LineItemID: "li_1", VariantID: "var_1", Quantity: 2}},
	})
	require.NoError(t, err)

	wf, err := h.wf.RecoveryWorkflow(plan)
	require.NoError(t, err)

	reserve, ok := wf.Steps[0].(*reserveInventoryStep)
	require.True(t, ok, "the first step must be the inventory step")
	assert.Equal(t, testCartID, reserve.plan.CartID, "the cart identifier must come back from the record")
	assert.Equal(t, testAmount, reserve.plan.Amount)
	require.Len(t, reserve.plan.Lines, 1)
	assert.Equal(t, "li_1", reserve.plan.Lines[0].LineItemID)
	assert.Empty(t, reserve.plan.PaymentData,
		"payment data is NOT WRITTEN to the record; it must not be in the restored plan either")
}

// TestHappyPathRunsTheFiveStepsInOrder verifies that every step runs and that
// the result reports the order, the capture and the confirmed inventory.
func TestHappyPathRunsTheFiveStepsInOrder(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, testCartID, out.CartID)
	assert.Equal(t, testOrderID, out.OrderID)
	assert.Equal(t, testCollectionID, out.PaymentCollectionID)
	assert.Equal(t, testSessionID, out.PaymentSessionID)
	assert.Equal(t, testPaymentID, out.PaymentID)
	assert.Equal(t, testAmount, out.Amount)
	assert.Equal(t, testCurrency, out.CurrencyCode)
	assert.Equal(t, []string{"res_" + testLineA, "res_" + testLineB}, out.ReservationIDs)
	assert.True(t, out.CartCompleted)
	assert.True(t, out.ReservationsConfirmed)
	assert.Empty(t, out.Warnings)

	assert.Equal(t, []string{
		"totals:calculate",
		"cart:snapshot",
		"catalog:graph",
		"link:list_many:" + LinkVariantInventory,
		"inventory:reserve:" + testLineA,
		"inventory:reserve:" + testLineB,
		"order:place",
		"payment:collection",
		// The order is bound to the collection BEFORE the session is opened:
		// nothing has been held on the card yet, so a failure here costs only a
		// reservation.
		"link:create:order_payment",
		"payment:session",
		"payment:authorize",
		"payment:capture",
		// The first read VERIFIES the capture; the second MEASURES it for the
		// order summary. They are separate calls on purpose (see
		// clearCartStep.recordPaymentTotals).
		"payment:read_collection",
		"payment:read_collection",
		"order:summary",
		"cart:complete",
		"inventory:confirm:res_" + testLineA,
		"inventory:confirm:res_" + testLineB,
	}, h.rec.snapshot())
}

// TestOrderSnapshotIsBuiltFromTotalsAndCatalog verifies that the body sent to
// the order is assembled correctly from the cart, the totals and the catalog.
//
// The schema has to be exactly the one the order module expects and the
// compiler cannot see that (ADR 0006); this is why the fields are checked one
// by one.
func TestOrderSnapshotIsBuiltFromTotalsAndCatalog(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)
	require.Len(t, h.orders.placed, 1)

	placed := h.orders.placed[0]
	assert.Equal(t, testCartID, placed.CartID)
	assert.Equal(t, testRegionID, placed.RegionID)
	assert.Equal(t, testCustomerID, placed.CustomerID)
	assert.Equal(t, "customer@example.com", placed.Email)
	assert.Equal(t, testCurrency, placed.CurrencyCode)
	assert.NotEmpty(t, placed.IdempotencyKey, "the order idempotency key MUST BE FILLED IN")
	assert.Equal(t, int64(2500), placed.Subtotal)
	assert.Equal(t, int64(500), placed.TaxTotal)
	assert.Equal(t, testAmount, placed.Total)

	require.Len(t, placed.Items, 2)
	assert.Equal(t, orderSnapshotItem{
		VariantID: testVariantA, Title: testTitleA, Quantity: 2,
		UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400,
	}, placed.Items[0])
	assert.Equal(t, orderSnapshotItem{
		VariantID: testVariantB, Title: testTitleB, Quantity: 1,
		UnitPrice: 500, Subtotal: 500, TaxTotal: 100, Total: 600,
	}, placed.Items[1])

	// The capture is made with an EXPLICIT amount; zero would have meant "all of
	// what is held".
	assert.Equal(t, []int64{testAmount}, h.payments.captureAmounts)
}

// TestPaymentFailureRollsBackOrderAndInventory is the DoD test of Phase 6.
//
// When the payment step blows up the order MUST BE CANCELED, the inventory
// reservation MUST BE RELEASED and the compensations must run in REVERSE ORDER.
func TestPaymentFailureRollsBackOrderAndInventory(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "the card was declined")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a decline is a conflict, not a server fault")

	calls := h.rec.snapshot()

	// FORWARD direction: inventory first, then the order, then the payment.
	assert.Less(t, indexOf(calls, "inventory:reserve:"+testLineA), indexOf(calls, "order:place"))
	assert.Less(t, indexOf(calls, "order:place"), indexOf(calls, "payment:authorize"))

	// The compensation REALLY ran.
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))

	// REVERSE order: the order cancellation comes BEFORE the inventory release.
	assert.Less(t, indexOf(calls, "order:cancel"), indexOf(calls, "inventory:release:res_"+testLineA),
		"compensation must run in reverse order: create_order is undone BEFORE reserve_inventory")

	// The payment session was closed too; the capture was never attempted.
	assert.Equal(t, 1, h.rec.count("payment:cancel"))
	assert.Equal(t, 0, h.rec.count("payment:capture"))
	assert.Equal(t, 0, h.rec.count("cart:complete"))
}

// TestInsufficientStockCreatesNoOrder verifies that when the reservation of the
// first line blows up no side effect has been applied: there is nothing to
// compensate.
func TestInsufficientStockCreatesNoOrder(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(context.Context, string, string, int64, string) (string, error) {
		return "", errors.Conflict("inventory_insufficient_stock", "insufficient stock")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, "inventory_insufficient_stock", errors.CodeOf(err),
		"the inventory module's code MUST REACH the client; the step code is only a "+
			"fallback for a codeless error — %v", err)
	assert.False(t, hasCode(err, CodeReservationFailed),
		"the step code must NOT be anywhere in the chain; finding it would mean the "+
			"wrapping overwrote the code and then carried the underlying error")

	assert.Equal(t, 0, h.rec.count("order:place"))
	assert.Equal(t, 0, h.rec.count("payment:collection"))
	assert.Empty(t, h.orders.canceled)
	for _, call := range h.rec.snapshot() {
		assert.NotContains(t, call, "inventory:release", "if no reservation was taken there is nothing to release")
	}
}

// TestInsufficientStockOnTheSecondLineReleasesTheFirst verifies that a step
// which stops halfway does its OWN cleanup.
//
// The engine does NOT compensate a step that blows up on a single attempt; the
// cleanup debt belongs to the step.
func TestInsufficientStockOnTheSecondLineReleasesTheFirst(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", errors.Conflict("inventory_insufficient_stock", "insufficient stock")
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"the reservation of the first line must be released by the step's own cleanup")
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestOldBehaviorIsKeptWhenTheLocationIsGiven verifies that a location declared
// by the caller is an INSTRUCTION: no selection is made, no module is asked and
// ALL the lines of the cart are reserved from that warehouse.
//
// This is the test of backward compatibility. The field became optional, but
// when it arrives filled in the behavior must not change in the slightest;
// otherwise the decision of a single-warehouse installation, or of an
// administrative order that has to ship from one specific warehouse, would be
// silently overridden.
func TestOldBehaviorIsKeptWhenTheLocationIsGiven(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)
	assert.Equal(t, testOrderID, out.OrderID)

	require.Len(t, h.inventory.reserved, 2)
	assert.Equal(t, testLocationID, h.inventory.reserved[0].LocationID)
	assert.Equal(t, testLocationID, h.inventory.reserved[1].LocationID)

	// The fakes are not scripted, so the flow would already have blown up had
	// they been called; the trail is checked anyway, because "returned no error"
	// and "was never called" are not the same thing.
	for _, call := range h.rec.snapshot() {
		assert.NotContains(t, call, "inventory:locations")
		assert.NotContains(t, call, "fulfillment:rank_locations")
	}
}

// TestLocationIsChosenPerLineWhenLeftEmpty verifies that the single-location
// assumption is gone.
//
// Three claims are checked together: the candidates come from the INVENTORY
// module, the ranking is built by the FULFILLMENT module, and the lines of one
// order may be reserved from DIFFERENT warehouses. The fake's policy is the
// inverse of the real module's (see [rankByGreatestID]) and the chosen
// candidate is neither first nor last in the list: a checkout that picks from
// the list itself fails here.
func TestLocationIsChosenPerLineWhenLeftEmpty(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == testItemA {
			return []string{testLocationEast, testLocationWest, testLocationNorth}, nil
		}
		return []string{testLocationEast}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	in := h.input()
	in.LocationID = ""

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err, "a request without a location is valid from now on")
	assert.Equal(t, testOrderID, out.OrderID)

	require.Len(t, h.inventory.reserved, 2)
	assert.Equal(t, testLocationWest, h.inventory.reserved[0].LocationID,
		"the fulfillment module chooses the location; checkout cannot pick from the candidates itself")
	assert.Equal(t, testLocationEast, h.inventory.reserved[1].LocationID)
	assert.NotEqual(t, h.inventory.reserved[0].LocationID, h.inventory.reserved[1].LocationID,
		"the lines of one order must be reservable from different warehouses")

	// The candidates pass through exactly AS THEY CAME from the inventory
	// module: were checkout to filter or sort them, it would in effect be
	// deciding the preference order itself.
	assert.Equal(t, [][]string{
		{testLocationEast, testLocationWest, testLocationNorth},
		{testLocationEast},
	}, h.fulfillment.offered)

	// The input of the policy comes FROM THE PLAN. Had the region not been
	// passed, the real fulfillment module would drop the request but the fake
	// does not; this is why the claim is written here, explicitly.
	assert.Equal(t, []string{testRegionID, testRegionID}, h.fulfillment.offeredRegions,
		"every selection call must carry the region of the order")

	// Order: for every line the FACT is asked first, then the DECISION is taken,
	// then the reservation is made.
	var inventoryAndFulfillment []string
	for _, call := range h.rec.snapshot() {
		if strings.HasPrefix(call, "inventory:") || strings.HasPrefix(call, "fulfillment:") {
			inventoryAndFulfillment = append(inventoryAndFulfillment, call)
		}
	}
	assert.Equal(t, []string{
		"inventory:locations:" + testItemA,
		"fulfillment:rank_locations",
		"inventory:reserve:" + testLineA,
		"inventory:locations:" + testItemB,
		"fulfillment:rank_locations",
		"inventory:reserve:" + testLineB,
		"inventory:confirm:res_" + testLineA,
		"inventory:confirm:res_" + testLineB,
	}, inventoryAndFulfillment)
}

// TestReservationOfTheFirstLineIsReleasedWhenTheSecondHasNoWarehouse proves the
// compensation of a multi-warehouse reservation.
//
// The situation is hard to reach in a single-warehouse flow and easy in a
// multi-warehouse one: the first line is reserved from a warehouse, the second
// line is found in no warehouse at all. Because the engine does not compensate a
// step that blows up on a single attempt, the debt belongs to the step and the
// reservation of the first line MUST BE RELEASED. The reporting invents no new
// class either: whatever is returned when stock is insufficient
// (errors.Conflict) is what is returned here.
func TestReservationOfTheFirstLineIsReleasedWhenTheSecondHasNoWarehouse(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == testItemA {
			return []string{testLocationEast}, nil
		}
		return []string{}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err),
		"when no warehouse has enough stock the result is 'the order cannot be placed': %v", err)
	assert.True(t, hasCode(err, CodeReservationFailed),
		"when there is NO candidate this package produces the result; there is no underlying code to preserve — %v", err)
	assert.Contains(t, err.Error(), testItemB,
		"the message must say which item could not be placed")

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"the reservation of the first line must be released by the step's OWN cleanup")
	assert.Equal(t, 1, h.rec.count("fulfillment:rank_locations"),
		"with no candidate the selection is NOT asked for: what is missing is stock, not a warehouse to ship from")
	assert.Equal(t, 0, h.rec.count("order:place"))
	assert.Empty(t, h.orders.canceled)
}

// TestFulfillmentFailingToRankLocationsIsReportedLikeInsufficientStock verifies
// that the fulfillment module eliminating the candidates and being unable to
// choose any of them is met in the SAME branch.
//
// The class is errors.Conflict again: what is missing is not something the
// request can fix, it is the state of the world.
func TestFulfillmentFailingToRankLocationsIsReportedLikeInsufficientStock(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == testItemA {
			return []string{testLocationEast}, nil
		}
		return []string{testLocationNorth}, nil
	}
	h.fulfillment.rankFn = func(_ context.Context, _ string, candidates []string) ([]string, error) {
		if slices.Contains(candidates, testLocationNorth) {
			return nil, errors.Conflict("fulfillment_no_serviceable_location",
				"no warehouse serves the target region")
		}
		return rankByGreatestID(context.Background(), "", candidates)
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)
	// errors.CodeOf reads the OUTERMOST code of the chain and that is the field
	// the transport layer writes into the body. Checking with hasCode would not
	// have been enough: it walks the chain and would find the underlying error —
	// and stay green — even in a wrapping that overwrote the code.
	assert.Equal(t, "fulfillment_no_serviceable_location", errors.CodeOf(err),
		"the fulfillment module's code MUST REACH the client: elimination is not an "+
			"inventory problem and its fix lies elsewhere — %v", err)
	assert.Contains(t, err.Error(), "unselected",
		"a call that blows up before a location is chosen cannot invent a warehouse name in the message")

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineB))
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestAnEmptyLocationRankingIsNotAccepted verifies that the fulfillment module
// returning an EMPTY ranking without an error does not count as success.
//
// Had it been accepted, the line would fail without a single warehouse being
// tried and without a reason being written down: the loop ends on its first turn
// and no accumulated error is left behind either.
func TestAnEmptyLocationRankingIsNotAccepted(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == testItemA {
			return []string{testLocationEast}, nil
		}
		return []string{testLocationNorth}, nil
	}
	h.fulfillment.rankFn = func(_ context.Context, _ string, candidates []string) ([]string, error) {
		if slices.Contains(candidates, testLocationNorth) {
			return nil, nil
		}
		return rankByGreatestID(context.Background(), "", candidates)
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"a provider that violates the contract is not a situation the caller can fix")
	assert.True(t, hasCode(err, CodeReservationFailed), "error: %v", err)

	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineB),
		"reservation is NOT ATTEMPTED with an empty ranking")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
}

// TestReservationTrailCarriesTheChosenLocation verifies that the trail written
// into the execution record carries the warehouse chosen for each line.
//
// The record is the only source of information for an operator intervening by
// hand; when lines can be reserved from different warehouses, the answer to
// "which warehouse" must be there. The step is called DIRECTLY because what is
// being asked is not the result of the saga but the output the step writes into
// the execution record.
func TestReservationTrailCarriesTheChosenLocation(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID == testItemA {
			return []string{testLocationEast, testLocationWest, testLocationNorth}, nil
		}
		return []string{testLocationEast}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	in := h.input()
	in.LocationID = ""

	plan, err := h.wf.prepare(context.Background(), in)
	require.NoError(t, err)

	step := &reserveInventoryStep{w: h.wf, plan: plan}
	sc := &workflow.StepContext{Shared: map[string]any{}}

	raw, err := step.Invoke(context.Background(), sc)
	require.NoError(t, err)

	out, ok := raw.(reserveOutput)
	require.True(t, ok, "the output of the step must be reserveOutput: %T", raw)
	assert.Equal(t, []reservationRef{
		{LineItemID: testLineA, ReservationID: "res_" + testLineA, LocationID: testLocationWest},
		{LineItemID: testLineB, ReservationID: "res_" + testLineB, LocationID: testLocationEast},
	}, out.Reservations)

	payload, err := json.Marshal(out)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"location_id":"`+testLocationWest+`"`,
		"the chosen warehouse MUST BE WRITTEN into the record")
}

// TestOrderFailureReleasesTheReservations verifies that a failure of the order
// step releases the inventory and that the payment is never reached.
func TestOrderFailureReleasesTheReservations(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", errors.Internal("order_store_unavailable", "the order could not be written")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
	assert.Empty(t, h.orders.canceled, "if no order was opened there is nothing to cancel")
	assert.Equal(t, 0, h.rec.count("payment:collection"))
}

// TestPartialAuthorizationFailsTheStep verifies the FULL PAYMENT RULE.
//
// When the provider holds LESS than was asked for, the status is still
// "authorized"; a saga that only looks at the status would confirm an unpaid
// order.
func TestPartialAuthorizationFailsTheStep(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "authorized", testAmount - 1, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentUnderauthorized), "error: %v", err)
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 0, h.rec.count("payment:capture"), "capture is NOT ATTEMPTED with a short hold")
	assert.Equal(t, 1, h.rec.count("payment:cancel"), "a partial hold must be released")
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
}

// TestUncompensatedWorkIsReportedWhenTheCaptureFallsShort exercises the case
// where the verification blows up AFTER the capture.
//
// The money has been taken: the order is NOT canceled, the inventory is NOT
// released and the execution ends not as "rolled back" but with an error that
// asks for manual intervention.
func TestUncompensatedWorkIsReportedWhenTheCaptureFallsShort(t *testing.T) {
	h := newHarness(t)
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, 0, testAmount - 1, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"a captured amount is a dangling side effect")

	assert.Empty(t, h.orders.canceled, "a paid order is not canceled")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA), "the inventory of a paid order is not released")
	assert.Equal(t, 0, h.rec.count("payment:cancel"), "the capture has already closed the hold")
}

// TestRemainingCompensationsStillRunWhenOneFails verifies that an error from one
// Compensate does NOT STOP the chain.
func TestRemainingCompensationsStillRunWhenOneFails(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "the card was declined")
	}
	h.orders.cancelFn = func(context.Context, string, string) error {
		return errors.Internal("order_store_unavailable", "the order could not be canceled")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"if the compensation could not be completed the class is raised to Internal")

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"the inventory must be released even if the order cancellation blows up")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
}

// TestASecondCallWithTheSameKeyDoesNotRerunTheSteps verifies that the
// idempotency key deduplicates the execution.
//
// Because the fake cart does NOT RECORD the completion, the preparation passes a
// second time as well; in a real installation the cart would already be
// completed and the flow would stop earlier (see
// [TestACompletedCartIsRejected]). The only thing checked here is that the steps
// are NOT RUN again by the engine.
func TestASecondCallWithTheSameKeyDoesNotRerunTheSteps(t *testing.T) {
	h := newHarness(t)

	first, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	second, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, first, second, "the second call returns the OUTPUT of the first execution")

	assert.Equal(t, 1, h.rec.count("inventory:reserve:"+testLineA))
	assert.Equal(t, 1, h.rec.count("order:place"))
	assert.Equal(t, 1, h.rec.count("payment:collection"))
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Equal(t, 1, h.rec.count("cart:complete"))
}

// TestClearCartFailureDoesNotFailTheOrder verifies that the step after the pivot
// DOES NOT RETURN an error and reports the fault as a warning instead.
func TestClearCartFailureDoesNotFailTheOrder(t *testing.T) {
	h := newHarness(t)
	h.carts.markCompletedFn = func(context.Context, string) error {
		return errors.Conflict("cart_totals_stale", "the totals are not up to date")
	}
	h.inventory.confirmFn = func(_ context.Context, reservationID string) error {
		if reservationID == "res_"+testLineB {
			return errors.Internal("inventory_unavailable", "could not be confirmed")
		}
		return nil
	}

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err, "a paid order is not dropped over a cart stamp")

	assert.Equal(t, testOrderID, out.OrderID)
	assert.False(t, out.CartCompleted)
	assert.False(t, out.ReservationsConfirmed)
	assert.Len(t, out.Warnings, 2)

	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
}

// TestACompletedCartIsRejected verifies that a completed cart is rejected
// without any side effect being applied.
func TestACompletedCartIsRejected(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Revision: testRevision, Completed: true,
			Items: []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}},
		})
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestACartChangedAfterTheTotalsIsRejected verifies that the totals and the
// snapshot have to belong to the SAME shape of the cart.
func TestACartChangedAfterTheTotalsIsRejected(t *testing.T) {
	h := newHarness(t)
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.Revision = testRevision - 1
		return totals, err
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCartChanged, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestAChangedApprovedTotalIsRejected verifies that a divergence between the
// amount the customer approved and the calculated amount does not stay silent.
func TestAChangedApprovedTotalIsRejected(t *testing.T) {
	h := newHarness(t)

	in := h.input()
	in.ExpectedTotal = testAmount - 100

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, CodeTotalMismatch, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestAVariantWithoutAnInventoryItemIsRejected verifies that a variant not
// linked to an inventory item is NOT SILENTLY SKIPPED.
func TestAVariantWithoutAnInventoryItemIsRejected(t *testing.T) {
	h := newHarness(t)
	h.links.listManyFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{testVariantA: {testItemA}}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestMultipleInventoryItemsAreRejected verifies that a link which ought to be
// singular turning up plural is not left to the accident of ordering.
func TestMultipleInventoryItemsAreRejected(t *testing.T) {
	h := newHarness(t)
	h.links.listManyFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{
			testVariantA: {testItemA, "inv_x"},
			testVariantB: {testItemB},
		}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantInventoryAmbiguous, errors.CodeOf(err))
}

// TestAVariantMissingFromTheCatalogIsRejected verifies that an order line
// without a title is not written.
func TestAVariantMissingFromTheCatalogIsRejected(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(context.Context, query.GraphSpec) ([]query.Record, error) {
		return []query.Record{{query.IDField: testVariantA, FieldTitle: testTitleA}}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err))
}

// TestACatalogOutageIsNotTreatedAsAMissingVariant verifies that an
// infrastructure error is not reported as a business state.
func TestACatalogOutageIsNotTreatedAsAMissingVariant(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(context.Context, query.GraphSpec) ([]query.Record, error) {
		return nil, errors.Unavailable("query_unavailable", "the read layer is unreachable")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCatalogReadFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "a transient outage does not count as permanent")
}

// TestBrokenTotalsAreCaughtBeforeAnySideEffect verifies that the arithmetic of
// the totals is NOT ACCEPTED unquestioned just because it comes from the cart
// module.
func TestBrokenTotalsAreCaughtBeforeAnySideEffect(t *testing.T) {
	tests := map[string]func(*cartwf.Totals){
		"line subtotal is not unit price x quantity": func(totals *cartwf.Totals) {
			totals.Lines[0].Subtotal = 1999
		},
		"line total identity is broken": func(totals *cartwf.Totals) {
			totals.Lines[0].Total = 2399
		},
		"cart subtotal is not the sum of the lines": func(totals *cartwf.Totals) {
			totals.Subtotal = 2400
		},
		"cart total identity is broken": func(totals *cartwf.Totals) {
			totals.Total = 2999
		},
		// The three cases below close the gap where the discount and the tax at
		// CART LEVEL do not go through the range check. In all three the total
		// identity HOLDS — the totals are internally consistent — but the
		// numbers are meaningless.
		"cart discount is negative": func(totals *cartwf.Totals) {
			// A negative discount INFLATES the amount to be captured: the
			// customer would be charged more than the sum of the lines.
			totals.DiscountTotal = -500
			totals.Total = 3500
		},
		"cart tax is negative": func(totals *cartwf.Totals) {
			// A negative tax does the opposite: the customer would be charged an
			// amount below the sum of the lines.
			totals.TaxTotal = -2000
			totals.Total = 500
		},
		"cart tax and discount overflow int64": func(totals *cartwf.Totals) {
			// The two extreme values cancel each other out and the identity
			// "holds" in raw int64 arithmetic; left unchecked, the order would be
			// opened with MaxInt64 worth of tax.
			totals.TaxTotal = math.MaxInt64
			totals.DiscountTotal = math.MaxInt64 - 500
			totals.Total = testAmount
		},
	}

	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
				totals, err := defaultTotals(ctx, cartID)
				corrupt(&totals)
				return totals, err
			}

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.Error(t, err)
			assert.Equal(t, CodeAmountInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
				"the price of broken totals must not be inventory reserved and then released")
		})
	}
}

// TestAMissingTotalsLineIsRejected verifies that the totals are required to
// cover ALL the lines of the cart.
func TestAMissingTotalsLineIsRejected(t *testing.T) {
	h := newHarness(t)
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.Lines = totals.Lines[:1]
		totals.Subtotal = 2000
		totals.TaxTotal = 400
		totals.Total = 2400
		return totals, err
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeTotalsInvalid, errors.CodeOf(err))
}

// TestInputValidation verifies that the required fields are checked without any
// side effect being applied.
func TestInputValidation(t *testing.T) {
	tests := map[string]func(*CompleteCartInput){
		"cart_id empty":             func(in *CompleteCartInput) { in.CartID = "" },
		"payment_provider_id empty": func(in *CompleteCartInput) { in.PaymentProviderID = "" },
		"cart_id with whitespace":   func(in *CompleteCartInput) { in.CartID = " cart_1" },
		// The location is OPTIONAL but it is checked WHEN IT IS GIVEN: empty
		// input now means "you choose", so it was taken out of here, while an
		// identifier with whitespace must still be rejected (see
		// TestLocationIsChosenPerLineWhenLeftEmpty).
		"location_id with whitespace": func(in *CompleteCartInput) { in.LocationID = " sloc_1" },
		"expected_total negative":     func(in *CompleteCartInput) { in.ExpectedTotal = -1 },
	}

	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := h.input()
			corrupt(&in)

			_, err := h.wf.CompleteCart(context.Background(), in)
			require.Error(t, err)
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
			assert.Empty(t, h.rec.snapshot(), "invalid input touches no module")
		})
	}
}

// TestAnOverlongCartIDIsRejected verifies that the budget of the idempotency key
// is enforced in the input validation.
func TestAnOverlongCartIDIsRejected(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.CartID = strings.Repeat("c", MaxCartIDLen+1)

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "cart_id")
}

// TestPaymentDataReachesTheProvider verifies that the free-form data destined
// for the provider is carried into the session.
//
// The integration test's ability to blow up the payment step depends on this:
// the behavior of the manual provider is read from the session data.
func TestPaymentDataReachesTheProvider(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.PaymentData = json.RawMessage(`{"manual_outcome":"authorize"}`)

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{`{"manual_outcome":"authorize"}`}, h.payments.sessionData)
}

// TestPaymentDataIsNotWrittenToTheExecutionRecord verifies that sensitive data
// does not land in the durable ledger (plan Section 8).
func TestPaymentDataIsNotWrittenToTheExecutionRecord(t *testing.T) {
	plan := &checkoutPlan{
		CartID:      testCartID,
		PaymentData: json.RawMessage(`{"card_token":"tok_secret"}`),
	}

	payload, err := json.Marshal(plan)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "tok_secret")
	assert.NotContains(t, string(payload), "card_token")
}

// TestCaptureVerificationIsAnchoredToTheLocalAmount proves that the post-capture
// verification is anchored not to the amount the payment module reports ITSELF
// but to the amount the saga knows LOCALLY.
//
// Regression: the verification used to be "captured < amount" and both values
// came from the same Collection call. The question therefore shrank to "is the
// collection internally consistent"; when the collection said "0 was to be
// collected, 0 was collected", an order of 3000 units was written as successful
// with a ZERO capture. The authorization rule (authorized < plan.Amount) was
// already anchored to the local amount; this is its twin.
func TestCaptureVerificationIsAnchoredToTheLocalAmount(t *testing.T) {
	h := newHarness(t)
	// The collection is internally CONSISTENT but unrelated to the amount the
	// saga expects.
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", 0, 0, 0, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err,
		"the flow cannot count as SUCCESSFUL while the collection reports a zero capture")
	assert.True(t, hasCode(err, CodePaymentUndercaptured),
		"the error must report the short capture: %v", err)

	// The capture had been performed: this is a DANGLING side effect, it is not
	// closed by compensation.
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Equal(t, 0, h.rec.count("cart:complete"),
		"the cart must not be marked completed while the verification fails")
}

// TestACaptureFailureRollsEverythingBackInReverseOrder verifies that when the
// capture step ITSELF blows up the whole compensation chain runs in reverse
// order.
//
// It was a coverage gap: no test blew up the capture, so
// authorizePaymentStep.Compensate never ran at all. The "payment:cancel" trail
// the tests counted came from the hold release inside Invoke — that is, the
// COMPENSATION path that releases the hold on the customer's card had zero
// coverage.
//
// The PRECONDITION of the rollback is that the collection reports no capture at
// all: the capture call returning an error does not by itself mean "the money
// did not move", and the saga no longer rolls back without evidence (see
// [TestAnAmbiguousCaptureDoesNotRollBackAPaidOrder]). This is why the scene is
// set up explicitly — the provider was never reached at all, and there is no
// movement in the collection.
func TestACaptureFailureRollsEverythingBackInReverseOrder(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_down", "the provider is unreachable")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "authorized", testAmount, testAmount, 0, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	calls := h.rec.snapshot()

	// The rollback rests on EVIDENCE: the chain does not move without the
	// collection being read.
	assert.Equal(t, 1, h.rec.count("payment:read_collection"),
		"after a capture error the collection MUST BE QUERIED")

	// The authorization had SUCCEEDED; the hold must be released by the
	// compensation.
	assert.Equal(t, 1, h.rec.count("payment:authorize"))
	assert.Equal(t, 1, h.rec.count("payment:cancel"),
		"an authorized hold must be released; otherwise it stays dangling on the customer's card")

	// The order and the inventory were rolled back too.
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))

	// REVERSE order: payment -> order -> inventory.
	assert.Less(t, indexOf(calls, "payment:cancel"), indexOf(calls, "order:cancel"),
		"compensation in reverse order: authorize_payment is undone BEFORE create_order")
	assert.Less(t, indexOf(calls, "order:cancel"), indexOf(calls, "inventory:release:res_"+testLineA),
		"compensation in reverse order: create_order is undone BEFORE reserve_inventory")

	assert.Equal(t, 0, h.rec.count("cart:complete"))
}

// TestAnAmbiguousCaptureDoesNotRollBackAPaidOrder locks down the most expensive
// failure of the saga: the provider takes the money, the response is lost and
// Capture returns an error.
//
// Regression: the pivot guard was tied to the CAPTURE IDENTIFIER and because
// that identifier is never written on this path, the guard was switched off. The
// measured result was exactly the case the package comment calls "must never
// happen" — the call trail was
// "payment:capture -> payment:cancel -> order:cancel -> inventory:release x2",
// meaning the customer lost both the money and the order, and the goods were
// released as well. The error path is now INVESTIGATED: when the collection sees
// a capture, the saga stays on the forward side and manual reconciliation is
// requested.
func TestAnAmbiguousCaptureDoesNotRollBackAPaidOrder(t *testing.T) {
	h := newHarness(t)
	// The provider TOOK the money, then the response timed out.
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_timeout", "the provider response timed out")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", testAmount, testAmount, testAmount, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"a capture whose outcome is unknown is a dangling side effect")
	assert.True(t, hasCode(err, CodeCaptureAmbiguous), "error: %v", err)

	// The investigation REALLY happened.
	assert.Equal(t, 1, h.rec.count("payment:read_collection"),
		"after a capture error the collection MUST BE QUERIED")

	// The paid order stands: no compensation runs.
	assert.Empty(t, h.orders.canceled, "a paid order is not canceled")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"the inventory of a paid order is not released; otherwise the same goods are sold a second time")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestNoRollbackHappensWhenTheCollectionCannotBeRead verifies that the saga does
// NOT roll back when no evidence can be found.
//
// The most typical cause of the ambiguity is that the payment provider cannot be
// reached; in that case the collection read blows up too. Rolling back without
// evidence risks destroying a paid order, and in doubt this flow picks the CHEAP
// mistake: a pending order and reserved inventory are visible, an unrefunded
// capture is not.
func TestNoRollbackHappensWhenTheCollectionCannotBeRead(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_timeout", "the provider response timed out")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "", 0, 0, 0, 0, errors.Unavailable("psp_down", "the collection could not be read")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated))
	assert.True(t, hasCode(err, CodeCaptureAmbiguous), "error: %v", err)

	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestCompensatingAnAmbiguousCaptureDoesNotClaimRollback verifies that the
// compensation of a capture whose outcome is unknown DOES NOT RETURN nil.
//
// The compensation is called directly: the engine does not compensate a step
// that blows up on a single attempt, so this path is unreachable through the
// flow. The contract itself is checked anyway — returning nil would be a lie
// that records the execution as "the work was done and ROLLED BACK", and the day
// the step is made retryable that lie would quietly reach production.
func TestCompensatingAnAmbiguousCaptureDoesNotClaimRollback(t *testing.T) {
	h := newHarness(t)
	step := &capturePaymentStep{w: h.wf, plan: &checkoutPlan{
		CartID: testCartID, Amount: testAmount, CurrencyCode: testCurrency,
	}}

	t.Run("capture not attempted: no-op", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{}}
		require.NoError(t, step.Compensate(context.Background(), sc))
	})

	t.Run("capture attempted, outcome unknown", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{sharedCaptureAttempted: true}}
		err := step.Compensate(context.Background(), sc)
		require.Error(t, err)
		assert.True(t, hasCode(err, CodeCaptureAmbiguous), "error: %v", err)
		assert.True(t, errors.IsConflict(err), "a permanent state is not retried")
	})

	t.Run("capture performed", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{
			sharedCaptureAttempted: true,
			sharedPaymentID:        testPaymentID,
		}}
		err := step.Compensate(context.Background(), sc)
		require.Error(t, err)
		assert.True(t, hasCode(err, CodeCaptureIrreversible), "error: %v", err)
	})
}

// TestAStepFailingAfterCaptureKeepsThePivot locks down the pivot decision: once
// the capture has SUCCEEDED, nothing is rolled back if a later step fails.
//
// It was a coverage gap: when the body of capturePaymentStep.Compensate was
// replaced with "return nil" not a single test failed, because no test blew up a
// step AFTER a SUCCESSFUL capture. The scene is set up by making the cart module
// panic — the engine turns the panic into a step error and the compensation
// chain starts; what is really under test is that the chain STOPS at the pivot.
func TestAStepFailingAfterCaptureKeepsThePivot(t *testing.T) {
	h := newHarness(t)
	h.carts.markCompletedFn = func(context.Context, string) error {
		panic("the cart module crashed")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err),
		"because the capture compensation returns an error the execution must be compensation_failed")
	assert.True(t, hasCode(err, CodeCaptureIrreversible),
		"the compensation MUST REPORT that the money taken could not be given back: %v", err)

	// The capture had happened: the paid order stands.
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Empty(t, h.orders.canceled, "a paid order is not canceled")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"the inventory of a paid order is not released")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"the capture has already closed the hold")
}

// TestACancellationAfterCaptureDoesNotLockTheCart verifies that the saga is
// DETACHED from the caller's cancellation.
//
// Regression: the saga ran with the caller's context and, because the engine
// checks the context BEFORE every step, a client that dropped during the capture
// skipped clear_cart entirely. The measured result: cart:complete=0,
// inventory:confirm=0, execution compensation_failed — meaning the money was
// taken, the order stayed "pending", the cart stayed locked and the inventory
// stayed "active"; and because the idempotency key was burned as well, the same
// cart could never be attempted again.
func TestACancellationAfterCaptureDoesNotLockTheCart(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The client drops right in the middle of the capture.
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		cancel()
		return testPaymentID, nil
	}

	out, err := h.wf.CompleteCart(ctx, h.input())
	require.NoError(t, err,
		"once the capture is done the flow cannot stop halfway because the client went away")

	assert.True(t, out.CartCompleted, "the cart must be stamped; otherwise it stays locked forever")
	assert.True(t, out.ReservationsConfirmed)
	assert.Empty(t, out.Warnings)

	assert.Equal(t, 1, h.rec.count("cart:complete"))
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineB))
	assert.Empty(t, h.orders.canceled)
}

// TestTheSagaContextIsDetachedFromTheCaller verifies that [sagaContext] DOES NOT
// CARRY the cancellation and sets up its own budget.
func TestTheSagaContextIsDetachedFromTheCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sctx, stop := sagaContext(ctx)
	defer stop()

	require.NoError(t, sctx.Err(), "the caller's cancellation must not make the saga stillborn")

	deadline, ok := sctx.Deadline()
	require.True(t, ok, "the detached context MUST HAVE its own time budget")
	assert.WithinDuration(t, time.Now().Add(SagaTimeout), deadline, time.Minute)
}

// TestTheCleanupContextIsUnaffectedByCancellation verifies that [cleanupContext]
// produces a live context even out of a canceled one.
//
// One of the moments cleanup is needed most is exactly when the context dies:
// trying to release a half-finished reservation with a dead context means every
// attempt fails instantly.
func TestTheCleanupContextIsUnaffectedByCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cctx, stop := cleanupContext(ctx)
	defer stop()

	require.NoError(t, cctx.Err(), "the cleanup context must not be stillborn")

	deadline, ok := cctx.Deadline()
	require.True(t, ok, "the cleanup MUST HAVE its own time budget")
	assert.WithinDuration(t, time.Now().Add(CompensationTimeout), deadline, time.Minute)
}

// TestAnEmptyReservationIDIsNotTreatedAsSuccess verifies that the inventory
// module returning an EMPTY identifier without an error IS NOT COUNTED as
// success.
//
// An empty identifier does not mean "no reservation was made"; it means "one was
// made but there is no trail of it". If it is silently accepted, neither this
// step nor the compensation can release it.
func TestAnEmptyReservationIDIsNotTreatedAsSuccess(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", nil
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "error: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"a reservation without a trail is a dangling side effect")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	// The reservation that DOES have a trail is released all the same.
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("order:place"), "no order is opened with an identifier-less reservation")
}

// TestAnEmptyOrderIDIsNotTreatedAsSuccess verifies that the order module
// returning an EMPTY identifier without an error does not produce an ORPHAN
// order.
//
// Regression: the empty identifier was written into the shared map and the
// compensation, thinking "no order was ever opened", did a no-op; the measured
// result was order:place=1 while order:cancel=0 — an open order was left behind
// in the order module.
func TestAnEmptyOrderIDIsNotTreatedAsSuccess(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "error: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"an order without a trail is a dangling side effect; the execution cannot say 'rolled back'")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	assert.Equal(t, 0, h.rec.count("payment:collection"), "no payment is opened with an identifier-less order")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA), "the inventory is released all the same")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
}

// TestAnEmptyPaymentIDDoesNotDisableThePivot verifies that the payment module
// returning an EMPTY capture identifier without an error DOES NOT BRING DOWN the
// pivot guard.
//
// Regression: the guard was tied to the emptiness of a string. On an empty
// identifier skipAfterCapture returned false and, with the capture already done,
// order:cancel and two inventory:release calls ran.
func TestAnEmptyPaymentIDDoesNotDisableThePivot(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "error: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated))
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	assert.Empty(t, h.orders.canceled, "the order is not canceled once the money has been taken")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestEmptyPaymentIdentifiersAreStoppedAtAuthorization verifies that empty
// collection and session identifiers are rejected at the CHEAPEST point.
//
// At this point there is no amount held on the customer's card; the only price
// is a reservation that gets rolled back.
func TestEmptyPaymentIdentifiersAreStoppedAtAuthorization(t *testing.T) {
	tests := map[string]func(*harness){
		"collection identifier empty": func(h *harness) {
			h.payments.createCollectionFn = func(context.Context, string, string, int64) (string, error) {
				return "", nil
			}
		},
		"session identifier empty": func(h *harness) {
			h.payments.openSessionFn = func(context.Context, string, string, string, json.RawMessage) (string, error) {
				return "", nil
			}
		},
	}

	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			corrupt(h)

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.Error(t, err)

			assert.True(t, hasCode(err, CodeEmptyIdentifier), "error: %v", err)
			assert.False(t, errors.Is(err, workflow.ErrUncompensated),
				"before the authorization there is no side effect left dangling")

			assert.Equal(t, 0, h.rec.count("payment:authorize"))
			assert.Equal(t, 0, h.rec.count("payment:capture"))
			assert.Equal(t, []string{testOrderID}, h.orders.canceled)
			assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
			assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
		})
	}
}

// TestALeakedReservationIsReportedWhenInventoryCompensationFails locks down
// together the error REPORTING path of the inventory compensation and
// releaseAll's promise that "the chain does NOT STOP at the first error".
//
// It was two coverage gaps: (1) Compensate swallowing the error and returning
// nil did not fail a single test — yet returning nil means the engine writes the
// execution as "the work was done and ROLLED BACK" while in reality reserved
// inventory stays dangling; (2) when the "continue" in releaseAll was replaced
// with "break", the second reservation quietly dangling went unnoticed.
//
// That the remaining list is PRUNED is visible here too: when the compensation
// is retried, only the reservation that could not be released is attempted
// (res_li_a three times, res_li_b once).
func TestALeakedReservationIsReportedWhenInventoryCompensationFails(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "the card was declined")
	}
	h.inventory.releaseFn = func(_ context.Context, reservationID string) error {
		if reservationID == "res_"+testLineA {
			return errors.Internal("inventory_unavailable", "the reservation could not be released")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, hasCode(err, CodeReservationLeaked),
		"a dangling reservation MUST BE REPORTED: %v", err)

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB),
		"one reservation failing to be released is no reason for the other one to dangle")
	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("inventory:release:res_"+testLineA),
		"the compensation is retried and on every attempt ONLY the remaining reservation is attempted")
}

// TestInlineCleanupIsRetriedWithTheCompensationPolicy verifies that a step's OWN
// cleanup shows the same persistence as the engine's compensation.
//
// A transient outage must not produce manual intervention merely depending on
// which path caught it: the in-step cleanup had a single attempt while the
// engine's compensation was retried three times.
func TestInlineCleanupIsRetriedWithTheCompensationPolicy(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", errors.Conflict("inventory_insufficient_stock", "insufficient stock")
		}
		return "res_" + lineItemID, nil
	}

	var releases int
	h.inventory.releaseFn = func(context.Context, string) error {
		releases++
		if releases < compensationRetry().MaxAttempts {
			return errors.Unavailable("inventory_unavailable", "the inventory service is temporarily unreachable")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("inventory:release:res_"+testLineA),
		"on a transient outage the cleanup must insist")
	assert.False(t, errors.Is(err, workflow.ErrUncompensated),
		"if the cleanup eventually succeeded there is NO dangling work")
	assert.True(t, hasCode(err, "inventory_insufficient_stock"), "error: %v", err)
}

// TestACompletedCartStopsInPrepareOnTheSecondCall locks down what the
// idempotency godoc amounts to in a REAL installation.
//
// The engine's "return the output of a completed execution" path is unreachable
// in this flow: the preparation runs BEFORE the engine's check and a successful
// execution stamps the cart as completed. The answer to the second call is
// therefore not "the same result" but CodeCartCompleted — and what matters is
// that a second order IS NOT BORN.
func TestACompletedCartStopsInPrepareOnTheSecondCall(t *testing.T) {
	h := newHarness(t)

	var completed bool
	h.carts.markCompletedFn = func(context.Context, string) error {
		completed = true
		return nil
	}
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		if !completed {
			return defaultSnapshot(ctx, cartID)
		}
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CustomerID: testCustomerID,
			CurrencyCode: testCurrency, Revision: testRevision, Completed: true,
			Items: []SnapshotItem{
				{ID: testLineA, VariantID: testVariantA, Quantity: 2},
				{ID: testLineB, VariantID: testVariantB, Quantity: 1},
			},
		})
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	_, err = h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err, "a completed cart cannot be ordered a second time")
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 1, h.rec.count("order:place"), "a second order IS NOT BORN from the same cart")
	assert.Equal(t, 1, h.rec.count("payment:capture"))
}

// TestReleasingTheHoldIsRetriedWithTheCompensationPolicy verifies that releasing
// the hold of a half-finished authorization also shows the same persistence as
// the engine's compensation.
//
// Leaving the hold on the customer's card dangling because of a transient outage
// is an outcome that would not have happened had the same outage been caught in
// the compensation chain.
func TestReleasingTheHoldIsRetriedWithTheCompensationPolicy(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "the card was declined")
	}

	var cancels int
	h.payments.cancelFn = func(context.Context, string) error {
		cancels++
		if cancels < compensationRetry().MaxAttempts {
			return errors.Unavailable("psp_down", "the provider is temporarily unreachable")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("payment:cancel"),
		"on a transient outage releasing the hold must be insisted on")
	assert.False(t, errors.Is(err, workflow.ErrUncompensated),
		"if the hold was eventually released there is NO dangling work")
	assert.True(t, errors.IsConflict(err), "if the cleanup succeeds the error stays a CARD DECLINE")
}

// TestThePivotGuardHoldsWhenTheCapturePanics locks down that the capture flag is
// set BEFORE the Capture call.
//
// Regression: had the flag been set AFTER the call, a panic raised during the
// call (a fault in the provider adapter) would have run the compensation chain
// without ever engaging the pivot guard — an order that MIGHT HAVE BEEN PAID
// would be canceled and the inventory released. Because the engine turns a panic
// into a step error, this scenario is genuinely reachable.
//
// With the flag set before the call, EVERY fault after that point (error, panic,
// timeout) counts as "the money may have gone" and NO rollback is done.
func TestThePivotGuardHoldsWhenTheCapturePanics(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		panic("the provider adapter crashed")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	// The pivot must hold: the money MAY have gone, no rollback can be done.
	assert.Equal(t, 0, h.rec.count("order:cancel"),
		"the order cannot be canceled once the capture was attempted; the customer would lose both the money and the order")
	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"the inventory cannot be released once the capture was attempted; the goods of a standing order would be sold a second time")
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"the hold may already have been closed by the capture")
}

// TestTheStepFailsWhenTheCollectionAmountDivergesFromThePlan verifies that a
// payment collection having been opened with an amount DIFFERENT from the one
// the saga opened is caught.
//
// The capture verification is anchored to the local amount (plan.Amount); the
// collection's own amount is a separate consistency claim. A divergence means the
// payment collection was opened with some amount other than the expected one, and
// it must not be passed over silently.
func TestTheStepFailsWhenTheCollectionAmountDivergesFromThePlan(t *testing.T) {
	h := newHarness(t)
	// The capture MEETS the plan but the amount of the collection is something
	// else entirely.
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", testAmount * 2, testAmount, testAmount, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err, "if the collection amount diverges from the plan the step must fail")
	assert.True(t, hasCode(err, CodePaymentUndercaptured), "error: %v", err)
	assert.Equal(t, 0, h.rec.count("cart:complete"))
}

// TestTheNextCandidateIsTriedWhenAWarehouseIsExhausted verifies that the RACE
// between the candidate list and the reservation does not drop the order.
//
// The candidates are read without a lock, the reservation is made under a lock:
// in the window between the two the chosen warehouse may have run out. An
// implementation that settled for a single candidate would drop the WHOLE order
// — and that while another warehouse still had enough stock.
//
// The scenario is not theoretical: the ranking is deterministic, meaning every
// concurrently arriving order tries the SAME warehouse and they all collide on
// the same line. A deterministic ranking does not reduce the contention, it
// concentrates it.
func TestTheNextCandidateIsTriedWhenAWarehouseIsExhausted(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast, testLocationWest}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	// rankByGreatestID puts west first; that warehouse ran out just in between.
	h.inventory.reserveFn = func(
		_ context.Context, _, locationID string, _ int64, lineItemID string,
	) (string, error) {
		if locationID == testLocationWest {
			return "", errors.Conflict("inventory_insufficient_stock",
				"not enough stock in warehouse %s", locationID)
		}
		return "res_" + lineItemID, nil
	}

	in := h.input()
	in.LocationID = ""

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err, "the order must not be dropped while another warehouse has stock: %v", err)
	assert.Equal(t, testOrderID, out.OrderID)

	require.Len(t, h.inventory.reserved, 4, "for every line the exhausted warehouse + the valid warehouse must be tried")
	assert.Equal(t, testLocationWest, h.inventory.reserved[0].LocationID)
	assert.Equal(t, testLocationEast, h.inventory.reserved[1].LocationID,
		"after the exhausted warehouse the next candidate must be taken")

	// The fulfillment module is asked ONCE PER LINE: falling back means moving to
	// the next entry in the ranking already obtained. Asking again after every
	// exhausted candidate would produce the same answer (the ranking is
	// deterministic) but would read the policy records anew each time, and every
	// read would lengthen the race window between the lock-free reading of the
	// candidates and the locked reservation.
	assert.Equal(t, [][]string{
		{testLocationEast, testLocationWest},
		{testLocationEast, testLocationWest},
	}, h.fulfillment.offered)
	assert.Equal(t, 2, h.rec.count("fulfillment:rank_locations"),
		"two lines, two calls: the ranking is per line, not per candidate")
}

// TestFallbackHappensONLYOnAConflict separates the errors where insisting is
// right from those where it is wrong.
//
// errors.Conflict means "there is not enough stock in this warehouse" and the
// answer may be different in another warehouse. An unreachable database, on the
// other hand, gives the same answer in EVERY warehouse; insisting there would
// hide the outage and multiply the latency by the number of candidates.
func TestFallbackHappensONLYOnAConflict(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast, testLocationWest, testLocationNorth}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID
	h.inventory.reserveFn = func(
		_ context.Context, _, _ string, _ int64, _ string,
	) (string, error) {
		return "", errors.Unavailable("db_down", "the database could not be reached")
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
		"the class must be preserved, not turned into a conflict: %v", err)

	assert.Len(t, h.inventory.reserved, 1,
		"on an error that is not a conflict the next candidates must NOT be tried")
}

// TestTheOrderFailsWhenEveryCandidateIsExhausted verifies that the fallback is
// not unbounded and that the behavior does not change once it is exhausted.
func TestTheOrderFailsWhenEveryCandidateIsExhausted(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast, testLocationWest}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID
	h.inventory.reserveFn = func(
		_ context.Context, _, locationID string, _ int64, _ string,
	) (string, error) {
		return "", errors.Conflict("inventory_insufficient_stock",
			"not enough stock in warehouse %s", locationID)
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)
	assert.True(t, hasCode(err, "inventory_insufficient_stock"), "error: %v", err)

	assert.Len(t, h.inventory.reserved, 2, "every candidate must be tried once, no more")
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestNoFallbackWhenTheDeclaredLocationIsExhausted verifies that the instruction
// is not a preference.
//
// If the caller declared a location, moving to another warehouse would be
// silently changing their decision — which warehouse the goods ship from is
// something the caller knows and has made other decisions on (shipping contract,
// customs).
func TestNoFallbackWhenTheDeclaredLocationIsExhausted(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(
		_ context.Context, _, locationID string, _ int64, _ string,
	) (string, error) {
		return "", errors.Conflict("inventory_insufficient_stock",
			"not enough stock in warehouse %s", locationID)
	}

	// LocationID is FILLED IN inside h.input(); the candidate list must never be
	// asked for.
	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)

	assert.Len(t, h.inventory.reserved, 1, "a declared location is a single attempt")
	assert.Equal(t, 0, h.rec.count("fulfillment:rank_locations"),
		"if a location was declared the fulfillment module is never asked")
}

// TestALocationRankingOutsideTheCandidatesIsNotAccepted verifies that the
// fulfillment module cannot step OUTSIDE the candidate set.
//
// Without the check, a reservation would be attempted against a warehouse that
// was never in the list and the error would blow up one module away from its
// cause — in the inventory module's "no such location" answer. The class is
// Internal: a provider that violates the contract is not a situation the caller
// can fix.
func TestALocationRankingOutsideTheCandidatesIsNotAccepted(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast}, nil
	}
	h.fulfillment.rankFn = func(_ context.Context, _ string, _ []string) ([]string, error) {
		return []string{"sloc_not_a_candidate"}, nil
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"a provider that violates the contract is not a situation the caller can fix")
	assert.Equal(t, CodeReservationFailed, errors.CodeOf(err),
		"the error is produced by this package itself; there is no underlying code to preserve — %v", err)

	assert.Zero(t, h.rec.count("inventory:reserve:"+testLineA),
		"reservation is NOT ATTEMPTED against a location that is not a candidate")
}

// TestADuplicateLocationRankingIsNotAccepted verifies that the fulfillment
// module cannot rank the same candidate twice.
//
// A duplicated candidate means going to an exhausted warehouse a SECOND TIME:
// one round of the fallback is wasted and one more call is made to the inventory
// module that will give the same answer. Had it been silently accepted the fault
// would be invisible and only felt as slowness.
func TestADuplicateLocationRankingIsNotAccepted(t *testing.T) {
	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast, testLocationWest}, nil
	}
	h.fulfillment.rankFn = func(_ context.Context, _ string, _ []string) ([]string, error) {
		return []string{testLocationEast, testLocationEast}, nil
	}

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err), "error: %v", err)
	assert.Equal(t, CodeReservationFailed, errors.CodeOf(err), "error: %v", err)

	assert.Zero(t, h.rec.count("inventory:reserve:"+testLineA),
		"a duplicated ranking is NEVER tried; the check runs BEFORE the reservation")
}

// TestTheOrderRecordsWhatWasCollected is the reason the summary write exists.
//
// Until this landed, `SetOrderSummaryTotals` had a service method, a repository
// method and a generated query — and no production caller. Every real order
// therefore reported paid_total: 0 and outstanding: <the whole total> on both
// the admin and the storefront read, so an operator could not tell a paid order
// from an unpaid one.
func TestTheOrderRecordsWhatWasCollected(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.True(t, out.PaymentTotalsRecorded)
	require.Len(t, h.orders.summaries, 1)
	assert.Equal(t, testOrderID, h.orders.summaries[0].orderID)
	assert.Equal(t, testAmount, h.orders.summaries[0].paidTotal)
	assert.Zero(t, h.orders.summaries[0].refundedTotal)
}

// TestTheRecordedTotalsAreThePAYMENTMODULEs proves the numbers are measured
// rather than assumed.
//
// The saga knows what it ASKED to capture. What it writes has to be what the
// collection actually holds: a provider may capture less than was asked, and a
// refund may already stand against the same collection.
func TestTheRecordedTotalsAreThePAYMENTMODULEs(t *testing.T) {
	h := newHarness(t)
	h.payments.collectionFn = func(
		_ context.Context, _ string,
	) (string, int64, int64, int64, int64, error) {
		// Captured MORE than the plan (an over-collection is a real fact) and a
		// refund already recorded against it.
		return "captured", testAmount, testAmount, testAmount + 500, 200, nil
	}

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.True(t, out.PaymentTotalsRecorded)
	require.Len(t, h.orders.summaries, 1)
	assert.Equal(t, testAmount+500, h.orders.summaries[0].paidTotal,
		"the paid total has to be the collection's, not the plan's")
	assert.Equal(t, int64(200), h.orders.summaries[0].refundedTotal,
		"a refund standing against the collection has to be carried too")
}

// TestAFailedSummaryWriteDoesNotROLLBACKAPaidOrder is the discipline the step
// runs under, and it is the whole reason this write lives after the pivot.
//
// The money has moved and the order has been placed. Returning an error here
// would write the execution failed and show the customer a failure for a flow
// that succeeded. The fault is reported instead: an ERROR line, a warning in
// the result, and the flag left false.
func TestAFailedSummaryWriteDoesNotROLLBACKAPaidOrder(t *testing.T) {
	h := newHarness(t)
	h.orders.summaryFn = func(_ context.Context, _ string, _, _ int64) error {
		return errors.New("the order module is unreachable")
	}

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err, "a bookkeeping failure must not fail a paid order")

	assert.False(t, out.PaymentTotalsRecorded)
	assert.NotEmpty(t, out.Warnings)
	assert.Contains(t, strings.Join(out.Warnings, " "), "could not be recorded on the order")

	// The order still stands: nothing was canceled.
	assert.Empty(t, h.orders.canceled)
	// And the rest of the finalization still ran.
	assert.True(t, out.CartCompleted)
}

// TestAnUnreadableCollectionIsAlsoOnlyAWarning covers the other half of the
// same path: the failure can come from the read rather than from the write.
func TestAnUnreadableCollectionIsAlsoOnlyAWarning(t *testing.T) {
	h := newHarness(t)
	reads := 0
	h.payments.collectionFn = func(
		_ context.Context, _ string,
	) (string, int64, int64, int64, int64, error) {
		reads++
		if reads == 1 {
			// The capture step's verification still has to pass, otherwise the
			// saga fails before it ever reaches the summary.
			return "captured", testAmount, testAmount, testAmount, 0, nil
		}

		return "", 0, 0, 0, 0, errors.New("the payment module is unreachable")
	}

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.False(t, out.PaymentTotalsRecorded)
	assert.NotEmpty(t, out.Warnings)
	assert.Empty(t, h.orders.summaries, "nothing may be written from an unread collection")
	assert.Empty(t, h.orders.canceled)
}

// TestTheOrderIsBoundToItsPaymentCollection is the path that did not exist.
//
// The collection's Reference carries the CART id, so until this binding was
// written there was no way from an order to the money collected for it: an
// operator asking "what was paid on this order" had to already know which cart
// it came from. Two godocs named the "order_payment" link and nothing declared
// or wrote it.
func TestTheOrderIsBoundToItsPaymentCollection(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.links.created, 1)
	assert.Equal(t, LinkOrderPayment, h.links.created[0].name)
	assert.Equal(t, out.OrderID, h.links.created[0].fromID,
		"the link's left side is the ORDER")
	assert.Equal(t, out.PaymentCollectionID, h.links.created[0].toID,
		"the link's right side is the COLLECTION")
}

// TestTheBindingIsWrittenBEFORETheAuthorization pins where the failure is
// cheap.
//
// Nothing has been held on the customer's card at that point, so a binding that
// cannot be written costs only a reservation that gets rolled back. Writing it
// after the authorization would mean choosing between a held card and an
// unreachable payment.
func TestTheBindingIsWrittenBEFORETheAuthorization(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	calls := h.rec.snapshot()
	linkAt := slices.Index(calls, "link:create:"+LinkOrderPayment)
	authAt := slices.Index(calls, "payment:authorize")
	require.NotEqual(t, -1, linkAt)
	require.NotEqual(t, -1, authAt)
	assert.Less(t, linkAt, authAt)
}

// TestAnUnwritableBindingStopsTheSaga is the fail-closed half.
//
// Carrying on without the link would produce exactly the state the binding
// exists to end — a paid order with no way back to its payment — and it would
// do so silently.
func TestAnUnwritableBindingStopsTheSaga(t *testing.T) {
	h := newHarness(t)
	h.links.createFn = func(_ context.Context, _, _, _ string) error {
		return errors.New("the link table is unreachable")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())

	require.Error(t, err)
	// The card was never touched: the failure is before the authorization.
	assert.Equal(t, 0, h.rec.count("payment:authorize"))
	// And the reservation was rolled back.
	assert.Positive(t, h.rec.count("inventory:release:res_"+testLineA))
}

// TestARolledBackSagaKeepsTheBinding holds the compensation decision.
//
// A rolled-back saga cancels the order and leaves the collection standing —
// "a collection holds no money, it is only a ledger line". The link says which
// order that line belonged to, and removing it would erase the trace of an
// attempt that really happened.
func TestARolledBackSagaKeepsTheBinding(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(_ context.Context, _ string) (string, int64, error) {
		return "", 0, errors.New("the provider declined")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	require.Len(t, h.links.created, 1, "the binding was written before the authorization")
	assert.Equal(t, 0, h.rec.count("link:delete:"+LinkOrderPayment),
		"the compensation must not erase the trace of an attempt that happened")
}
