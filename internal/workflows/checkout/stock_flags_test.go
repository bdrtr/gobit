package checkout

// This file holds the tests of the STOCK PAIR: manage_inventory and
// allow_backorder, the two catalog flags ADR 0048 gave a reader in this saga.
//
// Every test here is written so that it FAILS if the flag stops being read.
// That is the whole point of the record: before it, both columns were written
// on every variant and nothing in the repository branched on either value, so
// each of them could have been flipped in the database without a single test
// going red.

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

// linkOnly scripts the inventory link so that ONLY the named variants are
// linked to an inventory item.
//
// It is the shape a merchant who unticks "manage inventory" actually leaves
// behind: an uncounted variant is not linked to anything, because there is
// nothing to count.
func linkOnly(h *harness, links map[string][]string) {
	h.links.listManyFn = func(_ context.Context, name string, _ []string) (map[string][]string, error) {
		if name != LinkVariantInventory {
			return nil, errUnexpected("ListMany: " + name)
		}
		return links, nil
	}
}

// TestAnUncountedVariantIsOrderedWithoutAReservation is the live disagreement
// ADR 0048 was written to close.
//
// A merchant unticks "manage inventory" on a variant and links it to no
// inventory item. ADR 0040 says the storefront badges that variant IN STOCK;
// before the flag was read here the checkout refused the whole order with
// CodeVariantNotStocked. The badge and the till now answer the same question.
func TestAnUncountedVariantIsOrderedWithoutAReservation(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: false},
		testVariantB: {title: testTitleB, manageInventory: true},
	})
	linkOnly(h, map[string][]string{testVariantB: {testItemB}})

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err, "an uncounted variant with no inventory link must not stop the order")

	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
		"a variant the merchant does not count takes NO reservation")
	assert.Equal(t, 1, h.rec.count("inventory:reserve:"+testLineB),
		"the counted line is still reserved")
	assert.Equal(t, []string{"res_" + testLineB}, out.ReservationIDs,
		"the order carries the trace of the reservations that were really taken")
	assert.True(t, out.ReservationsConfirmed)
	assert.Equal(t, 0, h.rec.count("inventory:confirm:res_"+testLineA),
		"there is nothing to confirm for a line that reserved nothing")
	require.Len(t, h.orders.placed, 1)
	assert.Len(t, h.orders.placed[0].Items, 2,
		"BOTH lines enter the order; only the reservation is skipped")
}

// TestAnUncountedVariantIsNotReservedEvenWhenItIsStillLinked verifies that the
// FLAG decides and not the link.
//
// A variant that was counted yesterday and is not counted today keeps its old
// inventory link — nothing deletes it. Reserving against that link would set
// stock aside for a variant whose merchant asked for it not to be counted, and
// the shop would then be short of goods it never promised.
func TestAnUncountedVariantIsNotReservedEvenWhenItIsStillLinked(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: false},
		testVariantB: {title: testTitleB, manageInventory: true},
	})

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
		"a stale link is not consent to set stock aside")
	assert.Equal(t, []string{"res_" + testLineB}, out.ReservationIDs)
}

// TestACountedVariantWithoutAnInventoryItemIsStillRefused holds the OTHER half
// of the branch.
//
// The refusal was not deleted, it was narrowed. A variant the merchant counts,
// does not permit backorder for, and links to nothing still cannot be ordered:
// its stock can be reserved nowhere, and letting it through would sell goods
// that were never set aside for a merchant who asked for neither.
func TestACountedVariantWithoutAnInventoryItemIsStillRefused(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true},
		testVariantB: {title: testTitleB, manageInventory: true},
	})
	linkOnly(h, map[string][]string{testVariantB: {testItemB}})

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineB),
		"the refusal happens in the preparation, before any stock is touched")
}

// TestAnUnlinkedVariantThatPermitsBackorderIsOrdered closes the SECOND half of
// the badge-versus-till disagreement.
//
// ADR 0040's in-stock definition is three OR'd clauses and the checkout has to
// read the same ones. Clause one (manage_inventory false) reaches the link
// check above; clause two is this test. A variant the merchant counts, permits
// backorder for and has not linked is badged in stock by clause two, and until
// this test the checkout refused the whole order for it with
// CodeVariantNotStocked — the badge and the till disagreeing in the direction
// that takes a shopper to a checkout that will not serve them.
//
// The inventory module is deliberately NOT asked about the unlinked line: there
// is no item identifier to ask with, and the answer ("no warehouse can cover
// it") is already known. The location is left to per-line selection so that an
// empty item id reaching LocationsWithStock would surface as an unscripted
// call rather than as silence.
func TestAnUnlinkedVariantThatPermitsBackorderIsOrdered(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true, allowBackorder: true},
		testVariantB: {title: testTitleB, manageInventory: true},
	})
	linkOnly(h, map[string][]string{testVariantB: {testItemB}})
	h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
		if itemID != testItemB {
			return nil, errUnexpected("LocationsWithStock: " + itemID)
		}
		return []string{testLocationEast}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID

	in := h.input()
	in.LocationID = ""

	out, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err,
		"a variant ADR 0040 badges in stock under clause two must not be refused at the till")

	assert.Equal(t, 0, h.rec.count("inventory:locations:"),
		"the module is not asked about an EMPTY inventory item")
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
		"there is nothing to reserve for a line nothing counts")
	assert.Equal(t, []string{"res_" + testLineB}, out.ReservationIDs,
		"the linked line is reserved as usual")
	require.Len(t, h.orders.placed, 1)
	assert.Len(t, h.orders.placed[0].Items, 2,
		"BOTH lines enter the order; only the reservation is skipped")
}

// TestAnUncoverableLineIsRefusedUnlessBackorderIsPermitted is the reader of the
// second flag.
//
// The two cases differ in ONE bit of catalog data and in nothing else, which is
// what makes the test discriminating: with the flag off the order is refused
// exactly as it was before ADR 0048, with the flag on the same cart becomes an
// order that carries no reservation for that line.
func TestAnUncoverableLineIsRefusedUnlessBackorderIsPermitted(t *testing.T) {
	tests := map[string]struct {
		allowBackorder bool
		wantOrder      bool
	}{
		"the flag is off: the order is refused":    {allowBackorder: false},
		"the flag is on: the order is not refused": {allowBackorder: true, wantOrder: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			scriptCatalog(h, map[string]variantScript{
				testVariantA: {title: testTitleA, manageInventory: true, allowBackorder: tc.allowBackorder},
				testVariantB: {title: testTitleB, manageInventory: true},
			})
			// No warehouse can cover line A; line B is coverable, so a passing
			// order proves the line was let through rather than the whole cart
			// being waved past the stock check.
			h.inventory.locationsFn = func(_ context.Context, itemID string, _ int64) ([]string, error) {
				if itemID == testItemA {
					return []string{}, nil
				}
				return []string{testLocationEast}, nil
			}
			h.fulfillment.rankFn = rankByGreatestID

			in := h.input()
			in.LocationID = ""

			out, err := h.wf.CompleteCart(context.Background(), in)
			if !tc.wantOrder {
				require.Error(t, err)
				assert.True(t, errors.IsConflict(err), "an uncoverable line is a conflict: %v", err)
				assert.Equal(t, 0, h.rec.count("order:place"))
				return
			}

			require.NoError(t, err, "a backorder-permitting line must not refuse the order")
			assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
				"the line that no warehouse can cover leaves no reservation")
			assert.Equal(t, []string{"res_" + testLineB}, out.ReservationIDs)
			require.Len(t, h.orders.placed, 1)
			assert.Len(t, h.orders.placed[0].Items, 2,
				"the backordered line is on the order; it is only unreserved")
		})
	}
}

// TestBackorderStillReservesWhereverStockExists verifies that the flag lifts a
// REFUSAL and does not switch reservation off.
//
// Without this test "allow_backorder means do not reserve" would pass every
// other test in this file, and every shop that ticked the box would stop
// setting aside stock it actually has.
func TestBackorderStillReservesWhereverStockExists(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true, allowBackorder: true},
		testVariantB: {title: testTitleB, manageInventory: true, allowBackorder: true},
	})

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, []string{"res_" + testLineA, "res_" + testLineB}, out.ReservationIDs,
		"stock that exists is reserved even for a line that permits backorder")
}

// TestBackorderDoesNotForgiveAnythingButAConflict verifies that only the class
// meaning "no warehouse can cover this" is forgiven.
//
// A read that cannot reach the database answers the same way at every
// warehouse. Letting the flag swallow it would turn an outage into a shop that
// quietly takes orders it has set no stock aside for — and says nothing.
func TestBackorderDoesNotForgiveAnythingButAConflict(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: true, allowBackorder: true},
		testVariantB: {title: testTitleB, manageInventory: true, allowBackorder: true},
	})
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, _ string) (string, error) {
		return "", errors.Unavailable("inventory_unavailable", "the inventory database is unreachable")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"a transient fault stays a transient fault: %v", err)
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestTheStockFlagsRideOnTheTitleQuery verifies the cost ADR 0048 measured:
// the pair costs ZERO extra catalog round trips.
//
// The saga already reads every variant of the cart in one batch for the title,
// and the variant record already publishes both flags. If the flags ever move
// to a query of their own, every checkout starts paying a second catalog call
// and this test says so.
func TestTheStockFlagsRideOnTheTitleQuery(t *testing.T) {
	h := newHarness(t)

	var specs []query.GraphSpec
	h.catalog.graphFn = func(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
		specs = append(specs, spec)
		return catalogRecords(defaultVariants()), nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, specs, 1, "the whole checkout reads the catalog ONCE")
	assert.Equal(t, EntityVariant, specs[0].Entity)
	assert.Equal(t,
		[]string{query.IDField, FieldTitle, FieldManageInventory, FieldAllowBackorder},
		specs[0].Fields,
		"the two flags are two more names in the field list the title already pays for")
}

// TestAFlagThatIsNotABoolIsRefused verifies that a broken record does not
// become a decision.
//
// The Query layer refuses a field the provider does not publish, so a renamed
// column cannot arrive as a missing key; what can arrive is a value of the
// wrong type. Reading that as false would decide "do not count this variant"
// out of a type error and would sell goods nobody reserved.
func TestAFlagThatIsNotABoolIsRefused(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(context.Context, query.GraphSpec) ([]query.Record, error) {
		return []query.Record{{
			query.IDField:        testVariantA,
			FieldTitle:           testTitleA,
			FieldManageInventory: "true",
			FieldAllowBackorder:  false,
		}}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestAPlanRecordWithoutTheFlagDecodesAsCounted proves the direction the plan
// field was INVERTED for.
//
// A plan written before ADR 0048 carries no "unmanaged" key at all. Go fills
// the absent field with its zero value, and the field is spelled so that the
// zero value means COUNTED — the behavior every one of those records ran under.
// Had the plan carried manage_inventory as the catalog spells it, the same old
// record would decode as "do not count this line" and the recovered saga would
// hand over goods it had set no stock aside for.
func TestAPlanRecordWithoutTheFlagDecodesAsCounted(t *testing.T) {
	record := []byte(`{
		"cart_id": "cart_1",
		"lines": [{"line_item_id": "li_a", "variant_id": "var_a", "inventory_item_id": "inv_a", "quantity": 1}]
	}`)

	var plan checkoutPlan
	require.NoError(t, json.Unmarshal(record, &plan))
	require.Len(t, plan.Lines, 1)

	assert.False(t, plan.Lines[0].Unmanaged, "an old record must decode as a COUNTED line")
	assert.False(t, plan.Lines[0].AllowBackorder, "an old record must decode as a REFUSAL")

	step := &reserveInventoryStep{plan: &plan}
	sc := &workflow.StepContext{Shared: map[string]any{}}
	assert.Error(t, step.Restore(sc, json.RawMessage(`{"reservations":[]}`)),
		"the line the old plan reserved is exactly the one an empty record is corrupt for")
}

// TestTheReserveRecordMustAccountForEveryLine verifies the guard
// [reserveInventoryStep.Restore] holds.
//
// Compensation reads the reservation identifiers out of the record and has no
// other source, so a record that lost them means the trail is gone. Since ADR
// 0048 an empty reservation list is also an ordinary outcome — every line
// uncounted, or every line permitting backorder and covered by no warehouse —
// and treating that as corruption would make a legitimate order unrecoverable.
//
// What tells the two apart is the record and NOT the plan, and the last two
// cases are why. A line that permits backorder is reserved wherever stock
// exists, so a plan made of such lines is equally consistent with "reserved
// nothing, correctly" and "reserved two and lost both"; the plan cannot answer
// which happened, and a guard that asked it let the lost trail through with
// compensation reporting "done" having released nothing. The step therefore
// NAMES every line it deliberately left unreserved, and a whole record covers
// each line of the plan exactly once.
func TestTheReserveRecordMustAccountForEveryLine(t *testing.T) {
	reservedA := `{"line_item_id":"` + testLineA + `","reservation_id":"res_a","location_id":"loc"}`

	tests := map[string]struct {
		lines     []planLine
		output    string
		wantError bool
	}{
		"a counted line whose reservation is missing": {
			lines:     []planLine{{LineItemID: testLineA, InventoryItemID: testItemA}},
			output:    `{"reservations":[]}`,
			wantError: true,
		},
		"a record written before the accounting existed": {
			lines:  []planLine{{LineItemID: testLineA, InventoryItemID: testItemA}},
			output: `{"reservations":[` + reservedA + `]}`,
		},
		"every line uncounted and named": {
			lines:  []planLine{{LineItemID: testLineA, Unmanaged: true}},
			output: `{"reservations":[],"unreserved":["` + testLineA + `"]}`,
		},
		"a backorder line named as unreserved": {
			lines:  []planLine{{LineItemID: testLineA, InventoryItemID: testItemA, AllowBackorder: true}},
			output: `{"reservations":[],"unreserved":["` + testLineA + `"]}`,
		},
		"one counted line among uncounted ones": {
			lines: []planLine{
				{LineItemID: testLineA, Unmanaged: true},
				{LineItemID: testLineB, InventoryItemID: testItemB},
			},
			output:    `{"reservations":[],"unreserved":["` + testLineA + `"]}`,
			wantError: true,
		},
		"backorder lines that were reserved and lost their trail": {
			lines: []planLine{
				{LineItemID: testLineA, InventoryItemID: testItemA, AllowBackorder: true},
				{LineItemID: testLineB, InventoryItemID: testItemB, AllowBackorder: true},
			},
			output:    `{"reservations":[]}`,
			wantError: true,
		},
		"a backorder cart that half reserved and says so": {
			lines: []planLine{
				{LineItemID: testLineA, InventoryItemID: testItemA, AllowBackorder: true},
				{LineItemID: testLineB, InventoryItemID: testItemB, AllowBackorder: true},
			},
			output: `{"reservations":[` + reservedA + `],"unreserved":["` + testLineB + `"]}`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			step := &reserveInventoryStep{plan: &checkoutPlan{CartID: testCartID, Lines: tc.lines}}
			sc := &workflow.StepContext{Shared: map[string]any{}}

			err := step.Restore(sc, json.RawMessage(tc.output))
			if tc.wantError {
				require.Error(t, err)
				assert.Equal(t, CodeSharedStateInvalid, errors.CodeOf(err))
				return
			}
			require.NoError(t, err, "an order that legitimately reserved nothing must stay recoverable")
		})
	}
}

// TestTheReserveStepNamesEveryLineItLeavesUnreserved is the other side of that
// guard: the record it measures has to be written in the first place.
//
// Without this the accounting would be a rule the reader enforces and the
// writer never satisfies — every backordered order would become unrecoverable
// on the day it crashed, which is the one day the record matters.
func TestTheReserveStepNamesEveryLineItLeavesUnreserved(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, map[string]variantScript{
		testVariantA: {title: testTitleA, manageInventory: false},
		testVariantB: {title: testTitleB, manageInventory: true},
	})

	plan, err := h.wf.prepare(context.Background(), h.input())
	require.NoError(t, err)

	step := &reserveInventoryStep{w: h.wf, plan: plan}
	sc := &workflow.StepContext{Shared: map[string]any{}}

	raw, err := step.Invoke(context.Background(), sc)
	require.NoError(t, err)

	out, ok := raw.(reserveOutput)
	require.True(t, ok, "the output of the step must be reserveOutput: %T", raw)
	assert.Equal(t, []string{testLineA}, out.Unreserved,
		"the uncounted line is named in the record rather than left to be inferred")
	require.Len(t, out.Reservations, 1)
	assert.Len(t, plan.Lines, len(out.Reservations)+len(out.Unreserved),
		"the record accounts for every line of the plan")
}

// TestAPlanWhoseCountedLineLostItsItemIsRefused guards the pairing of the two
// reads.
//
// The flag comes from the catalog and the inventory item from the link layer,
// and the reserve step branches on both. If they ever disagree the step would
// SKIP a counted line: goods leaving the shop with no stock set aside and no
// error anywhere. The plan is validated before the saga starts, so the refusal
// costs nothing.
func TestAPlanWhoseCountedLineLostItsItemIsRefused(t *testing.T) {
	plan := &checkoutPlan{
		CartID:       testCartID,
		CurrencyCode: testCurrency,
		Amount:       1000,
		Subtotal:     1000,
		Lines: []planLine{{
			LineItemID: testLineA,
			VariantID:  testVariantA,
			Quantity:   1,
			UnitPrice:  1000,
			Subtotal:   1000,
			Total:      1000,
		}},
	}

	err := plan.validate()
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))

	plan.Lines[0].Unmanaged = true
	assert.NoError(t, plan.validate(),
		"the same line is valid the moment it says it is not counted")

	plan.Lines[0].Unmanaged = false
	plan.Lines[0].AllowBackorder = true
	assert.NoError(t, plan.validate(),
		"and valid the moment it says an uncoverable line does not refuse the order")
}
