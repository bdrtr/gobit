package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// testShippingOptionID is HOW a replacement is sent.
const testShippingOptionID = "so_standard"

// claimToReplace places an order and opens a claim on it that will be settled
// with goods; the returned line carries a quantity of three.
func claimToReplace(t *testing.T, e env) (claim models.Claim, lineID string) {
	t.Helper()

	order, lineID := returnedOrder(t, e)
	claim, err := e.svc.CreateClaim(context.Background(), service.CreateClaimInput{
		OrderID: order.ID, Type: models.ClaimReplace,
	})
	require.NoError(t, err)

	return claim, lineID
}

// replacementOf is a request for one line.
func replacementOf(claimID, lineID string, quantity int64) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ClaimID:          claimID,
		ShippingOptionID: testShippingOptionID,
		LocationID:       testLocationID,
		Lines: []service.ReplacementLineInput{
			{OrderLineItemID: lineID, Quantity: quantity},
		},
	}
}

// TestAClaimCanSayWhatItWillSend is the sentence the schema could not say.
//
// A claim of type "replace" carried the promise and nothing carried its
// content: not which line, not how many, not from where, not by which carrier.
func TestAClaimCanSayWhatItWillSend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	record, err := e.svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ClaimID:          claim.ID,
		ShippingOptionID: testShippingOptionID,
		LocationID:       testLocationID,
		Note:             "the second one arrived broken too",
		Lines: []service.ReplacementLineInput{
			{OrderLineItemID: lineID, Quantity: 2},
		},
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(record.ID, models.ReplacementIDPrefix))
	assert.Equal(t, models.ReplacementRequested, record.Status,
		"a replacement has to be born as a REQUEST: nothing has been sent")
	assert.Equal(t, claim.ID, record.ClaimID)
	assert.Equal(t, testShippingOptionID, record.ShippingOptionID)
	assert.Equal(t, testLocationID, record.LocationID)
	require.Len(t, record.Items, 1)
	assert.Equal(t, lineID, record.Items[0].OrderLineItemID)
	assert.Equal(t, int64(2), record.Items[0].Quantity)
	assert.Equal(t, record.ID, record.Items[0].ReplacementID)

	readBack, err := e.svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	assert.Equal(t, record.ID, readBack.ID)
	require.Len(t, readBack.Items, 1, "reading one back has to bring its lines")

	listed, err := e.svc.ListReplacementsOfClaim(ctx, claim.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, record.ID, listed[0].ID)
}

// TestAReplacementIsRefusedOnAClaimSettledWithMoney holds the pairing of the
// claim's type with its settlement.
//
// A refund claim's answer is its amount. Promising goods on it would leave two
// settlements on one claim and the customer receiving both.
func TestAReplacementIsRefusedOnAClaimSettledWithMoney(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	claim, err := e.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID: order.ID, Type: models.ClaimRefund, RefundAmount: 1200,
	})
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeClaimNotReplaceable, errors.CodeOf(err))
}

// TestAReplacementIsRefusedOnAClaimThatIsSettled holds the other half: a claim
// that has had its answer takes no additions.
func TestAReplacementIsRefusedOnAClaimThatIsSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	_, err := e.svc.CompleteClaim(ctx, claim.ID)
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
}

// TestAReplacementIsRefusedOnACanceledOrder validates that the order is read
// LIVE, not just at the moment the claim was opened.
func TestAReplacementIsRefusedOnACanceledOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	require.NoError(t, e.svc.CancelOrder(ctx, claim.OrderID, "the customer gave up"))

	_, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
}

// TestRecordingAReplacementTakesTheOrderLock validates that the ceiling is
// counted RACE-FREE.
//
// Summing without the lock would be true only at that moment: between the sum
// and the write a second request could promise the same units, and both would
// be within the ceiling separately while their total is not.
func TestRecordingAReplacementTakesTheOrderLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	// Only the locks taken from HERE on count: opening the claim locked the
	// same order, so a test reading the whole list would be green while this
	// call locked nothing at all.
	before := len(e.store.lockedOrders)

	_, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	assert.Contains(t, e.store.lockedOrders[before:], claim.OrderID,
		"the replacement record has to take the lock of the order it is sending against")
}

// TestMoreCannotBeSentThanWasBought is the rule that spans rows: neither
// request is too large on its own, their sum is.
func TestMoreCannotBeSentThanWasBought(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	_, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 2))
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 2))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementQuantityExceeded, errors.CodeOf(err))
}

// TestAWithdrawnReplacementFreesWhatItPromised validates that the ceiling
// counts LIVE promises.
//
// A canceled request sends nothing, so counting it would make a line
// unreplaceable after a mistake was corrected.
func TestAWithdrawnReplacementFreesWhatItPromised(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	first, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 3))
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 3))
	require.Error(t, err, "while the first promise stands the line is spoken for")

	_, err = e.svc.CancelReplacement(ctx, first.ID)
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 3))
	assert.NoError(t, err, "a withdrawn promise sends nothing and holds nothing")
}

// TestGoodsComingBackDoNotCountAgainstGoodsGoingOut pins the decision that the
// two ceilings are separate.
//
// Returning a broken unit and being sent a working one is the ordinary shape of
// a replacement; counting the return against the replacement would refuse
// exactly the case the record exists for.
func TestGoodsComingBackDoNotCountAgainstGoodsGoingOut(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: claim.OrderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 3}},
	})
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 3))
	assert.NoError(t, err)
}

// TestAReplacedLineHasToBeOnTheOrder keeps a replacement inside its own order.
func TestAReplacedLineHasToBeOnTheOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, _ := claimToReplace(t, e)

	_, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, "oli_somebody_elses", 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementLineUnknown, errors.CodeOf(err))
}

// TestAReplacementHasToSayWhatToSend collects the shape rules of the request.
func TestAReplacementHasToSayWhatToSend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	cases := map[string]service.CreateReplacementInput{
		"no lines": {
			ClaimID:          claim.ID,
			ShippingOptionID: testShippingOptionID,
			LocationID:       testLocationID,
		},
		"no carrier": func() service.CreateReplacementInput {
			in := replacementOf(claim.ID, lineID, 1)
			in.ShippingOptionID = ""

			return in
		}(),
		"no warehouse": func() service.CreateReplacementInput {
			in := replacementOf(claim.ID, lineID, 1)
			in.LocationID = ""

			return in
		}(),
		"no claim": func() service.CreateReplacementInput {
			in := replacementOf(claim.ID, lineID, 1)
			in.ClaimID = ""

			return in
		}(),
		"zero quantity": replacementOf(claim.ID, lineID, 0),
		"the same line twice": {
			ClaimID:          claim.ID,
			ShippingOptionID: testShippingOptionID,
			LocationID:       testLocationID,
			Lines: []service.ReplacementLineInput{
				{OrderLineItemID: lineID, Quantity: 1},
				{OrderLineItemID: lineID, Quantity: 1},
			},
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CreateReplacement(ctx, in)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	listed, err := e.svc.ListReplacementsOfClaim(ctx, claim.ID)
	require.NoError(t, err)
	assert.Empty(t, listed, "a refused request must not leave a record behind")
}

// TestWithdrawingAReplacementTwiceKeepsTheFirstMoment holds the idempotence of
// the withdrawal.
func TestWithdrawingAReplacementTwiceKeepsTheFirstMoment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	record, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	first, err := e.svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err)
	require.Equal(t, models.ReplacementCanceled, first.Status)
	require.NotNil(t, first.CanceledAt)

	second, err := e.svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err, "the repetition is the same request and its answer has not changed")
	require.NotNil(t, second.CanceledAt)
	assert.Equal(t, *first.CanceledAt, *second.CanceledAt,
		"the second withdrawal must not move the moment of the first")
}

// TestAClaimWithAnOpenPromiseCannotBeWithdrawn keeps the promise and the record
// that made it together.
//
// Withdrawing the claim first would leave a replacement pointing at a claim
// that says the matter is closed.
func TestAClaimWithAnOpenPromiseCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)

	record, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	_, err = e.svc.CancelClaim(ctx, claim.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementNotOpen, errors.CodeOf(err))
	assert.Contains(t, err.Error(), record.ID,
		"the refusal has to name the replacement to withdraw first")

	_, err = e.svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err)

	withdrawn, err := e.svc.CancelClaim(ctx, claim.ID)
	require.NoError(t, err, "with the promise withdrawn the claim closes")
	assert.Equal(t, models.ClaimCanceled, withdrawn.Status)
}

// TestReadingAReplacementValidatesItsIdentifier covers the two read paths.
func TestReadingAReplacementValidatesItsIdentifier(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.GetReplacement(ctx, "")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = e.svc.GetReplacement(ctx, "orepl_MISSING")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = e.svc.ListReplacementsOfClaim(ctx, "")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = e.svc.CancelReplacement(ctx, "orepl_MISSING")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}
