package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// TestStatusTransitionTable exercises the ENTIRE state machine in a single
// table.
//
// The table is the exact counterpart of the transition tables in the godocs: if
// a branch changes, both the documentation and the test have to be updated at
// the same time.
func TestStatusTransitionTable(t *testing.T) {
	t.Parallel()

	statuses := []struct {
		status   models.FulfillmentStatus
		cancel   models.Action
		ship     models.Action
		deliver  models.Action
		returned models.Action
		valid    bool
	}{
		{
			models.StatusPending,
			models.ActionProceed, models.ActionProceed, models.ActionProceed, models.ActionConflict,
			true,
		},
		{
			models.StatusShipped,
			models.ActionProceed, models.ActionNoop, models.ActionProceed, models.ActionProceed,
			true,
		},
		{
			models.StatusDelivered,
			models.ActionConflict, models.ActionRecord, models.ActionNoop, models.ActionConflict,
			true,
		},
		{
			models.StatusReturned,
			models.ActionConflict, models.ActionRecord, models.ActionConflict, models.ActionNoop,
			true,
		},
		{
			models.StatusCanceled,
			models.ActionNoop, models.ActionConflict, models.ActionConflict, models.ActionConflict,
			true,
		},
		{
			models.FulfillmentStatus("unknown"),
			models.ActionConflict, models.ActionConflict, models.ActionConflict, models.ActionConflict,
			false,
		},
	}

	for _, row := range statuses {
		t.Run(row.status.String(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, row.cancel, row.status.CancelAction(), "cancel branch")
			assert.Equal(t, row.ship, row.status.ShipAction(), "ship branch")
			assert.Equal(t, row.deliver, row.status.DeliverAction(), "deliver branch")
			assert.Equal(t, row.returned, row.status.ReturnAction(), "return branch")
			assert.Equal(t, row.valid, row.status.Valid(), "validity")
		})
	}
}

// TestAnOutOfOrderReportIsNeverAConflict pins the property the whole
// [models.ActionRecord] branch exists for, at the level of the table.
//
// It is stated as a rule rather than as four more rows in the table above,
// because the rule is what a carrier plugin depends on: a report about a stage
// the shipment has ALREADY PASSED must never come back as a conflict. A
// conflict on this path is not a caught mistake, it is a webhook the carrier
// will retry until it gives up, against an endpoint that will never accept it.
//
// The canceled status is deliberately outside the rule and is asserted as the
// exception it is: WE recalled that parcel, so a carrier reporting that it
// collected it afterwards contradicts our own record rather than merely
// arriving late, and an operator has to see it.
func TestAnOutOfOrderReportIsNeverAConflict(t *testing.T) {
	t.Parallel()

	pastShipment := []models.FulfillmentStatus{models.StatusDelivered, models.StatusReturned}
	for _, status := range pastShipment {
		assert.NotEqual(t, models.ActionConflict, status.ShipAction(),
			"a collection reported on a %q shipment is a late message, not a contradiction", status)
	}

	assert.Equal(t, models.ActionProceed, models.StatusPending.DeliverAction(),
		"a delivery whose collection event has not arrived yet must still be recorded")

	assert.Equal(t, models.ActionConflict, models.StatusCanceled.ShipAction(),
		"a collection reported after WE canceled the parcel contradicts our own record")
}

// TestDeliveredCannotBeCanceled pins down, at the level of the state machine,
// the decision Phase 7 asks about explicitly.
//
// Delivery is a physical fact that cannot be undone; the remedy is not a
// cancellation but a return. A fulfillment in transit, on the other hand, can be
// recalled and its cancellation is OPEN.
func TestDeliveredCannotBeCanceled(t *testing.T) {
	t.Parallel()

	assert.Equal(t, models.ActionConflict, models.StatusDelivered.CancelAction(),
		"a delivered fulfillment cannot be canceled")
	assert.Equal(t, models.ActionProceed, models.StatusShipped.CancelAction(),
		"a fulfillment in transit can be recalled")
	assert.Equal(t, models.ActionNoop, models.StatusCanceled.CancelAction(),
		"idempotency comes from the noop branch")
}

// TestActionZeroValueIsConflict proves that an undefined status is not
// accidentally read as "go ahead".
func TestActionZeroValueIsConflict(t *testing.T) {
	t.Parallel()

	var zero models.Action
	assert.Equal(t, models.ActionConflict, zero)
	assert.Equal(t, "conflict", zero.String())
	assert.Equal(t, "conflict", models.Action(200).String(), "an undefined value must also print conflict")
}

// TestPriceTypeValidation pins down the defined price types.
func TestPriceTypeValidation(t *testing.T) {
	t.Parallel()

	assert.True(t, models.PriceFlat.Valid())
	assert.True(t, models.PriceCalculated.Valid())
	assert.False(t, models.PriceType("dynamic").Valid())
}

// TestProfileTypeValidation pins down the defined profile types.
func TestProfileTypeValidation(t *testing.T) {
	t.Parallel()

	assert.True(t, models.ProfileDefault.Valid())
	assert.True(t, models.ProfileGiftCard.Valid())
	assert.True(t, models.ProfileCustom.Valid())
	assert.False(t, models.ProfileType("digital").Valid())
}

// TestRuleOperator pins down the classification of the operators.
//
// Recognizing the numeric operators separately is essential: if money fields
// such as the subtotal were compared as strings, "9" > "50000" would come out.
func TestRuleOperator(t *testing.T) {
	t.Parallel()

	numeric := []models.RuleOperator{models.OpGt, models.OpGte, models.OpLt, models.OpLte}
	for _, op := range numeric {
		assert.True(t, op.Valid(), "%s must be defined", op)
		assert.True(t, op.Numeric(), "%s must be numeric", op)
		assert.False(t, op.MultiValue(), "%s must take a single value", op)
	}

	text := []models.RuleOperator{models.OpEq, models.OpNe}
	for _, op := range text {
		assert.True(t, op.Valid())
		assert.False(t, op.Numeric())
		assert.False(t, op.MultiValue())
	}

	multiValued := []models.RuleOperator{models.OpIn, models.OpNin}
	for _, op := range multiValued {
		assert.True(t, op.Valid())
		assert.False(t, op.Numeric())
		assert.True(t, op.MultiValue(), "%s must take more than one value", op)
	}

	assert.False(t, models.RuleOperator("like").Valid())
}

// TestIDPrefixes pins down the prefix convention of plan Section 8.
func TestIDPrefixes(t *testing.T) {
	t.Parallel()

	generated := map[string]string{
		models.FulfillmentIDPrefix:        models.NewFulfillmentID(),
		models.ShippingOptionIDPrefix:     models.NewShippingOptionID(),
		models.ShippingProfileIDPrefix:    models.NewShippingProfileID(),
		models.ShippingOptionRuleIDPrefix: models.NewShippingOptionRuleID(),
		models.FulfillmentItemIDPrefix:    models.NewFulfillmentItemID(),
		models.ManualShipmentIDPrefix:     models.NewManualShipmentID(),
	}

	for prefix, id := range generated {
		assert.True(t, strings.HasPrefix(id, prefix), "%q must start with the prefix %q", id, prefix)
		assert.Len(t, id, len(prefix)+26, "the body must be 26 characters")
	}
}

// TestIDsAreUniqueAndTimeOrdered proves that identifiers do not collide and
// remain sortable by time.
//
// The ordering claim matters: list queries are paginated with "created_at DESC,
// id DESC", and a random identifier would leave the order of records within the
// same millisecond undefined.
func TestIDsAreUniqueAndTimeOrdered(t *testing.T) {
	t.Parallel()

	const count = 500
	seen := make(map[string]struct{}, count)
	previous := make([]string, 0, count)

	for range count {
		id := models.NewFulfillmentID()
		_, collision := seen[id]
		require.False(t, collision, "identifier collided: %s", id)
		seen[id] = struct{}{}
		previous = append(previous, id)
	}

	// Identifiers produced within the same millisecond diverge in the random
	// part; the ordering claim only holds once time has moved on.
	time.Sleep(2 * time.Millisecond)
	next := models.NewFulfillmentID()
	assert.Less(t, previous[0], next, "an identifier produced later must be lexicographically greater")
}

// TestAmountBounds pins down that the money bounds hold the documented values.
//
// The lower bound being ZERO is deliberate: free shipping is a real business
// decision.
func TestAmountBounds(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(0), models.MinAmount, "free shipping must be valid")
	assert.Equal(t, int64(1_000_000_000_000), models.MaxAmount)
	assert.Equal(t, int64(1), models.MinQuantity)
}
