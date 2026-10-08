package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// ceilingCollection opens a collection of one capture of the given amount and
// answers it with the capture.
func ceilingCollection(t *testing.T, svc *service.Service, key string, amount int64) (collectionID, paymentID string) {
	t.Helper()

	ctx := t.Context()
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: refundReference, Amount: amount, CurrencyCode: refundCurrency,
	})
	require.NoError(t, err)
	ses, err := svc.CreateSession(ctx, col.ID, refundProviderID, service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	payment, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	return col.ID, payment.ID
}

// refundFor refunds a collection for a cause held to a ceiling.
func refundFor(
	t *testing.T, svc *service.Service, collectionID string, amount, ceiling int64, reference string,
) (int64, error) {
	t.Helper()

	refunds, err := svc.RefundCollection(t.Context(), collectionID, amount, ceiling, "returned", reference)
	var total int64
	for i := range refunds {
		total += refunds[i].Amount
	}

	return total, err
}

// refundedOf reads what the collection gave back.
func refundedOf(t *testing.T, svc *service.Service, collectionID string) int64 {
	t.Helper()

	col, err := svc.GetPaymentCollection(t.Context(), collectionID)
	require.NoError(t, err)

	return col.RefundedAmount
}

// TestZeroIsWhatTheCauseHasLeft is ADR 0433: a cause held to 12 000 that gave
// back 5 000 refunds 7 000 when asked for zero, not the 26 000 its 31 000
// collection still holds.
func TestZeroIsWhatTheCauseHasLeft(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "zero-left", 31_000)

	_, err := refundFor(t, svc, collectionID, 5_000, 12_000, "ret_1")
	require.NoError(t, err)
	given, err := refundFor(t, svc, collectionID, 0, 12_000, "ret_1")
	require.NoError(t, err)

	assert.Equal(t, int64(7_000), given, "zero is what is left under the ceiling")
	assert.Equal(t, int64(12_000), refundedOf(t, svc, collectionID))
}

// TestACauseReachesItsCeilingExactly lets the last part through.
func TestACauseReachesItsCeilingExactly(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "ceiling-exact", 31_000)

	_, err := refundFor(t, svc, collectionID, 7_000, 12_000, "ret_1")
	require.NoError(t, err)
	given, err := refundFor(t, svc, collectionID, 5_000, 12_000, "ret_1")
	require.NoError(t, err, "reaching the ceiling exactly passes")

	assert.Equal(t, int64(5_000), given)
	assert.Equal(t, int64(12_000), refundedOf(t, svc, collectionID))
}

// TestOnePastTheCeilingIsRefusedBeforeMoneyMoves refuses the refund whole,
// with no row and no provider call, and says what the cause gave back.
func TestOnePastTheCeilingIsRefusedBeforeMoneyMoves(t *testing.T) {
	svc, prov := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "ceiling-past", 31_000)
	_, err := refundFor(t, svc, collectionID, 7_000, 12_000, "ret_1")
	require.NoError(t, err)
	calls := prov.refundCalls

	given, err := refundFor(t, svc, collectionID, 5_001, 12_000, "ret_1")

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeRefundExceedsCause, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "ret_1")
	assert.Zero(t, given)
	assert.Equal(t, calls, prov.refundCalls, "no money moved")
	assert.Equal(t, int64(7_000), refundedOf(t, svc, collectionID))

	_, err = refundFor(t, svc, collectionID, 5_000, 12_000, "ret_1")
	require.NoError(t, err)
	_, err = refundFor(t, svc, collectionID, 0, 12_000, "ret_1")
	require.Error(t, err, "zero on a spent cause refunds nothing")
	assert.Equal(t, service.CodeRefundExceedsCause, errors.CodeOf(err))
	assert.Equal(t, int64(12_000), refundedOf(t, svc, collectionID))
}

// TestAnotherCausesRefundsDoNotCount sums by the cause, not the collection: a
// claim that took 20 000 out of 31 000 leaves the return its 12 000 ceiling,
// and zero gives back the 11 000 the collection still holds.
func TestAnotherCausesRefundsDoNotCount(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "other-cause", 31_000)
	_, err := refundFor(t, svc, collectionID, 20_000, 20_000, "clm_2")
	require.NoError(t, err)

	given, err := refundFor(t, svc, collectionID, 0, 12_000, "ret_1")
	require.NoError(t, err)

	assert.Equal(t, int64(11_000), given, "the collection's rest, under the return's ceiling")
	assert.Equal(t, int64(31_000), refundedOf(t, svc, collectionID))
}

// TestACollectionRefundNamesItsCauseAndItsCeiling: a refund with no cause, or
// no ceiling, is no refund the returns flow makes.
func TestACollectionRefundNamesItsCauseAndItsCeiling(t *testing.T) {
	svc, prov := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "no-cause", 31_000)

	_, err := refundFor(t, svc, collectionID, 1_000, 12_000, "")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)

	_, err = refundFor(t, svc, collectionID, 1_000, 0, "ret_1")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)

	assert.Zero(t, prov.refundCalls)
	assert.Zero(t, refundedOf(t, svc, collectionID))
}

// TestAnOperatorsRefundHasNoCeiling keeps the payment route as it was: it
// names no cause, and a cause's ceiling does not hold it.
func TestAnOperatorsRefundHasNoCeiling(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID, paymentID := ceilingCollection(t, svc, "operator", 31_000)
	_, err := refundFor(t, svc, collectionID, 12_000, 12_000, "ret_1")
	require.NoError(t, err)

	refund, err := svc.RefundPayment(t.Context(), paymentID, 0, "goodwill")
	require.NoError(t, err)

	assert.Equal(t, int64(19_000), refund.Amount, "the rest of the capture")
	assert.Empty(t, refund.Reference)
	assert.Equal(t, int64(31_000), refundedOf(t, svc, collectionID))
}

// TestASpentCauseIsRefusedForItsCeilingBeforeTheCollectionIsAsked: a cause
// that gave back its whole ceiling out of a collection it emptied is refused
// as spent, at zero too, not as a collection with nothing left; the answer a
// return is given is the return's own (ADR 0433).
func TestASpentCauseIsRefusedForItsCeilingBeforeTheCollectionIsAsked(t *testing.T) {
	svc, prov := newRefundService(t)
	collectionID, _ := ceilingCollection(t, svc, "spent-and-empty", 12_000)
	_, err := refundFor(t, svc, collectionID, 0, 12_000, "ret_1")
	require.NoError(t, err)
	calls := prov.refundCalls

	for _, amount := range []int64{0, 1} {
		_, err = refundFor(t, svc, collectionID, amount, 12_000, "ret_1")

		require.Error(t, err)
		assert.Equal(t, service.CodeRefundExceedsCause, errors.CodeOf(err), "asked %d: %v", amount, err)
	}
	assert.Equal(t, calls, prov.refundCalls, "no money moved")
}

// interleavingStore runs a step of the test between two parts of a split
// refund: at the second preview read of a capture, which each part makes
// before its transaction, once.
type interleavingStore struct {
	*fakeStore

	reads   int
	between func()
}

// GetPayment runs the step at the second read and reads the capture.
func (s *interleavingStore) GetPayment(ctx context.Context, id string) (models.Payment, error) {
	s.reads++
	if s.reads == 2 && s.between != nil {
		step := s.between
		s.between = nil
		step()
	}

	return s.fakeStore.GetPayment(ctx, id)
}

// TestASplitRefundsLaterPartIsHeldUnderTheLock: a refund of a cause spread
// over two captures checks each part against the ceiling under the lock, so a
// second refund of the same cause that lands between its parts holds the
// first one to what is left (ADR 0433). A return held to 12 000 is refunded
// 12 000 out of two captures of 10 000; another 2 000 of it lands after the
// first part; the second part is refused, and 12 000 is given back, not
// 14 000.
func TestASplitRefundsLaterPartIsHeldUnderTheLock(t *testing.T) {
	store := &interleavingStore{fakeStore: newFakeStore()}
	prov := newFakeProvider(refundProviderID)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))
	svc, err := service.New(service.Options{Store: store, Providers: registry, Events: newFakeBus()})
	require.NoError(t, err)

	ctx := t.Context()
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: refundReference, Amount: 20_000, CurrencyCode: refundCurrency,
	})
	require.NoError(t, err)
	for _, key := range []string{"split-first", "split-second"} {
		ses, err := svc.CreateSession(ctx, col.ID, refundProviderID,
			service.CreateSessionInput{Amount: 10_000, IdempotencyKey: key})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
		_, err = svc.CapturePayment(ctx, ses.ID, 0)
		require.NoError(t, err)
	}

	var between error
	store.between = func() {
		_, between = svc.RefundCollection(ctx, col.ID, 2_000, 12_000, "the second refund", "ret_1")
	}
	made, err := svc.RefundCollection(ctx, col.ID, 12_000, 12_000, "the first refund", "ret_1")

	require.Nil(t, store.between, "the second refund ran between the parts")
	require.NoError(t, between, "the refund between the parts fits what was left")
	require.Error(t, err, "the second part would take the cause past its ceiling")
	var refused *errors.Error
	assert.True(t, errors.As(err, &refused))
	assert.Contains(t, err.Error(), service.CodeRefundExceedsCause, "under the made-only-in-part report")
	require.Len(t, made, 1, "the first part stands")
	assert.Equal(t, int64(10_000), made[0].Amount)
	assert.Equal(t, int64(12_000), refundedOf(t, svc, col.ID), "12 000 given back for a 12 000 ceiling")
}
