package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// authorizedSession sets up an authorized session for the capture tests.
func authorizedSession(t *testing.T, svc *service.Service) (models.PaymentCollection, models.PaymentSession) {
	t.Helper()

	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	updated, err := svc.AuthorizePayment(context.Background(), ses.ID)
	require.NoError(t, err)
	return col, updated
}

// TestCaptureCreatesAPaymentAndMakesTheCollectionCaptured verifies the happy
// path.
func TestCaptureCreatesAPaymentAndMakesTheCollectionCaptured(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)

	payment, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.NoError(t, err)
	assert.Equal(t, testAmount, payment.Amount, "a zero amount takes the whole authorized amount")
	assert.Equal(t, ses.ID, payment.PaymentSessionID)
	assert.Equal(t, col.ID, payment.PaymentCollectionID)
	assert.Equal(t, testCurrency, payment.CurrencyCode)
	assert.False(t, payment.CapturedAt.IsZero(), "the moment of capture must be stamped")
	assert.Equal(t, models.PaymentIDPrefix, payment.ID[:len(models.PaymentIDPrefix)])

	updatedSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCaptured, updatedSession.Status)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured, updatedCol.Status)
	assert.Equal(t, testAmount, updatedCol.CapturedAmount)
}

// TestASecondCaptureReturnsTheSamePaymentAndDoesNotReachTheProvider verifies the
// idempotent branch.
//
// At most ONE capture comes out of a session. A second call producing a new
// record would make it look as if the customer had been charged twice.
func TestASecondCaptureReturnsTheSamePaymentAndDoesNotReachTheProvider(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)
	first, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	second, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.NoError(t, err, "a second capture must NOT fail")
	assert.Equal(t, first.ID, second.ID, "the same capture must come back")
	_, _, capture, _, _ := prov.calls()
	assert.Equal(t, 1, capture, "the provider must be called ONLY once")

	payments, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, payments, 1, "there must be a single capture record")

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, updatedCol.CapturedAmount, "the captured amount must not DOUBLE")
}

// TestCaptureRepeatedWithTheSameAmountDoesNotFail verifies that a repeat is safe
// with an explicit amount too.
func TestCaptureRepeatedWithTheSameAmountDoesNotFail(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	first, err := svc.CapturePayment(ctx, ses.ID, testAmount)
	require.NoError(t, err)

	second, err := svc.CapturePayment(ctx, ses.ID, testAmount)

	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
}

// TestCaptureWithADifferentAmountConflicts verifies that a captured session
// cannot be taken again with a DIFFERENT amount; that is not a repeat but a new
// request.
func TestCaptureWithADifferentAmountConflicts(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	_, err := svc.CapturePayment(ctx, ses.ID, testAmount)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, testAmount-1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestCaptureCannotExceedTheAuthorizedAmount verifies that taking money that is
// not there is prevented.
func TestCaptureCannotExceedTheAuthorizedAmount(t *testing.T) {
	svc, _, prov := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	ses := openPaymentSession(t, svc, col.ID, "key-1")
	prov.scenario(coreprovider.SessionAuthorized, testAmount/2, "")
	_, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, testAmount)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
}

// TestAPartialCaptureDoesNotMakeTheCollectionCaptured verifies that an
// underpayment does not look like a FULL payment.
//
// A partial capture CLOSES the session (no second capture comes out of a
// session) but does not make the collection paid: had 1 unit taken from a
// collection of 50,000 counted as "captured", a saga that reads the payment's
// completion from the status would confirm an unpaid order.
func TestAPartialCaptureDoesNotMakeTheCollectionCaptured(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)

	payment, err := svc.CapturePayment(ctx, ses.ID, 1)

	require.NoError(t, err)
	assert.Equal(t, int64(1), payment.Amount)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyCaptured, updatedCol.Status)
	assert.Equal(t, int64(1), updatedCol.CapturedAmount)
}

// TestAPartialCaptureReleasesTheUncapturedHold verifies that the
// hold that was not captured is NOT LEFT HANGING on the collection.
//
// After the capture the session is "captured" and CANNOT be canceled; if the
// difference is not released here, there is no way left to release it, and the
// collection answers "how much is held on the customer" with too much forever.
func TestAPartialCaptureReleasesTheUncapturedHold(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)

	_, err := svc.CapturePayment(ctx, ses.ID, 1)
	require.NoError(t, err)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, updatedCol.AuthorizedAmount,
		"the captured session's hold must NOT REMAIN on the collection")

	updatedSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), updatedSession.AuthorizedAmount,
		"the session's authorized amount must come down to the amount actually taken")

	// The cancel path is closed; that is why the release MUST happen at the
	// moment of capture.
	require.Error(t, svc.CancelPayment(ctx, ses.ID))
}

// TestAFullCaptureClosesTheHold verifies that the captured amount does
// not also count as authorized on the collection.
//
// The same money showing as both "authorized" and "captured" means twice the
// money in reconciliation.
func TestAFullCaptureClosesTheHold(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)

	_, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, updatedCol.AuthorizedAmount)
	assert.Equal(t, testAmount, updatedCol.CapturedAmount)
	assert.Equal(t, models.CollectionCaptured, updatedCol.Status)
}

// TestCaptureInvalidTransitions verifies the state machine's conflict branches.
func TestCaptureInvalidTransitions(t *testing.T) {
	tests := map[string]func(t *testing.T, svc *service.Service, sessionID string){
		"pending": func(_ *testing.T, _ *service.Service, _ string) {},
		"canceled": func(t *testing.T, svc *service.Service, sessionID string) {
			require.NoError(t, svc.CancelPayment(context.Background(), sessionID))
		},
	}

	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			svc, _, _ := newTestService(t)
			col := openCollection(t, svc, testAmount)
			ses := openPaymentSession(t, svc, col.ID, "key-1")
			prepare(t, svc, ses.ID)

			_, err := svc.CapturePayment(context.Background(), ses.ID, 0)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
			assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
		})
	}
}

// TestCaptureNegativeAmountIsInvalid tests the amount validation.
func TestCaptureNegativeAmountIsInvalid(t *testing.T) {
	svc, _, _ := newTestService(t)
	_, ses := authorizedSession(t, svc)

	_, err := svc.CapturePayment(context.Background(), ses.ID, -1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestCaptureWritesNothingOnAWriteError verifies that when the capture record
// cannot be written, the session and the collection DO NOT CHANGE.
//
// Leaving the session "captured" while failing to write the capture record would
// mean a payment whose money was taken but which has no record; a difference
// impossible to find in reconciliation.
func TestCaptureWritesNothingOnAWriteError(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)
	store.failCreatePayment = errors.Internal("fake_write_failed", "could not be written")

	_, err := svc.CapturePayment(ctx, ses.ID, 0)

	require.Error(t, err)
	updatedSession, getErr := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, getErr)
	assert.Equal(t, models.SessionAuthorized, updatedSession.Status, "the transaction must be rolled back")

	updatedCol, getErr := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, getErr)
	assert.Zero(t, updatedCol.CapturedAmount)
	assert.Equal(t, models.CollectionAuthorized, updatedCol.Status)
}

// TestRefundPartialMakesTheCollectionPartiallyRefunded verifies the partial
// refund flow.
func TestRefundPartialMakesTheCollectionPartiallyRefunded(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	refund, err := svc.RefundPayment(ctx, payment.ID, testAmount/4, "customer request")

	require.NoError(t, err)
	assert.Equal(t, testAmount/4, refund.Amount)
	assert.Equal(t, "customer request", refund.Reason)
	assert.Equal(t, models.RefundIDPrefix, refund.ID[:len(models.RefundIDPrefix)])

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyRefunded, updatedCol.Status)
	assert.Equal(t, testAmount/4, updatedCol.RefundedAmount)
}

// TestRefundFullMakesTheCollectionRefunded verifies the full refund flow.
//
// It also tests that a zero amount refunds the REMAINDER: after two partial
// refunds, a call with zero must close the remainder.
func TestRefundFullMakesTheCollectionRefunded(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, testAmount/4, "")
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 0, "remainder")
	require.NoError(t, err)

	updatedCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded, updatedCol.Status)
	assert.Equal(t, testAmount, updatedCol.RefundedAmount)

	refunds, err := svc.ListRefunds(ctx, payment.ID)
	require.NoError(t, err)
	assert.Len(t, refunds, 2, "every refund produces a SEPARATE record")
}

// TestRefundCannotExceedTheRemainder verifies that a request to refund money
// that is not there is rejected.
func TestRefundCannotExceedTheRemainder(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, testAmount+1, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeInvalidTransition, errors.CodeOf(err))
}

// TestASecondRefundAfterAFullRefundConflicts verifies that a conflict comes back
// when nothing is left to refund.
//
// This method is DELIBERATELY NOT idempotent: a refund called twice is, in the
// real world, money paid back twice, and silently swallowing it would misinform
// an operator who does not notice that the second request was not applied.
func TestASecondRefundAfterAFullRefundConflicts(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 0, "")
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, 0, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeNothingToRefund, errors.CodeOf(err))
}

// TestRefundCalledTwiceRefundsTwice proves explicitly that a refund is NOT
// idempotent.
//
// This is the documented form of the behavior, and the test exists on purpose:
// someone changing it "so that refunds are idempotent too" would reduce two real
// refunds of 10 units to a single refund, and the customer would get too little
// money back.
func TestRefundCalledTwiceRefundsTwice(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, 1_000, "")
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, payment.ID, 1_000, "")
	require.NoError(t, err)

	updated, err := svc.GetPayment(ctx, payment.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2_000), updated.RefundedAmount)
}

// TestRefundOfAnUnknownPaymentIsNotFound verifies that a capture that does not
// exist cannot be refunded.
func TestRefundOfAnUnknownPaymentIsNotFound(t *testing.T) {
	svc, _, _ := newTestService(t)

	_, err := svc.RefundPayment(context.Background(), "pay_MISSING", 0, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
}

// TestRefundNegativeAmountIsInvalid tests the amount validation.
func TestRefundNegativeAmountIsInvalid(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, payment.ID, -1, "")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestRefundLockOrder verifies the refund flow's lock order.
//
// The order must be collection -> session -> capture; an implementation that
// took the capture lock first would deadlock with another flow touching the same
// collection.
func TestRefundLockOrder(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	store.locks = nil

	_, err = svc.RefundPayment(ctx, payment.ID, 100, "")
	require.NoError(t, err)

	assert.Equal(t, []string{"collection", "session", "payment"}, store.lockOrder())
}

// TestCaptureLockOrder verifies the capture flow's lock order.
func TestCaptureLockOrder(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	store.locks = nil

	_, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	assert.Equal(t, []string{"collection", "session"}, store.lockOrder())
}

// TestTheCaptureEventIsWrittenInTheTransactionAndPublishedAfter verifies that
// the announcement of a money movement cannot be lost.
//
// There are two writes and both are needed: the outbox row is the GUARANTEE, the
// direct publish is the SPEED. The row commits in the same transaction as the
// money movement, so even if the process dies between the commit and the
// publish, the event is not lost; the relay sends it.
func TestTheCaptureEventIsWrittenInTheTransactionAndPublishedAfter(t *testing.T) {
	svc, store, bus := newTestServiceWithBus(t)
	ctx := context.Background()
	col, ses := authorizedSession(t, svc)

	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	require.Len(t, store.outbox, 1, "the event must be written to the outbox inside the transaction")
	assert.Equal(t, service.EventPaymentCaptured, store.outbox[0].Name)
	assert.Equal(t, col.ID, store.outbox[0].Data[service.EventFieldCollectionID],
		"the event names the COLLECTION")
	assert.NotContains(t, store.outbox[0].Data, "amount",
		"the event carries NO AMOUNT: a refund is not idempotent, so an amount in the payload would be an INCREMENT")
	assert.Equal(t, service.EventPaymentCaptured+":"+payment.ID, store.outbox[0].ID,
		"the id derives from the CAPTURE row, not from the collection")

	// And after the commit it is published directly as well. That both carry the
	// SAME id is what makes the outbox row and this publish ONE event rather than
	// TWO: a subscriber idempotent on the event id — which the bus's contract
	// already requires — cannot tell the two deliveries apart.
	published := bus.events()
	require.Len(t, published, 1, "after the commit the fast path must publish too")
	assert.Equal(t, store.outbox[0].ID, published[0].ID,
		"the two deliveries are ONE event; a different id would mean a money movement processed twice")
	assert.Equal(t, store.outbox[0].Data, published[0].Data,
		"the same id carrying two different bodies would mean the subscriber behaving "+
			"differently depending on which one it saw")
}

// TestTwoRefundsYieldTwoDistinctEventIDs verifies the rule that stops the
// outbox from silently swallowing an event.
//
// The outbox row is written with ON CONFLICT (id) DO NOTHING, so the same id
// does not FAIL, it silently DROPS. An id keyed on the collection would take
// the second refund for a repeat of the first and destroy it; because the id
// derives from the refund ROW, two real refunds are two real events.
func TestTwoRefundsYieldTwoDistinctEventIDs(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	_, ses := authorizedSession(t, svc)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	first, err := svc.RefundPayment(ctx, payment.ID, testAmount/4, "")
	require.NoError(t, err)
	second, err := svc.RefundPayment(ctx, payment.ID, testAmount/4, "")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	var refundEvents []string
	for _, row := range store.outbox {
		if row.Name == service.EventPaymentRefunded {
			refundEvents = append(refundEvents, row.ID)
		}
	}

	require.Len(t, refundEvents, 2, "two real refunds are two events")
	assert.NotEqual(t, refundEvents[0], refundEvents[1],
		"the same id would be silently dropped in the outbox")
}
