package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The fixture's own constants and helpers.
//
// They duplicate what the module's older Turkish fixtures provide, and the
// duplication is deliberate: this file is new, so it is English (ADR 0012), and
// calling across for a helper would have pulled a Turkish identifier into it.
const (
	refundProviderID = "manual"
	refundAmount     = int64(10_000)
	refundCurrency   = "TRY"
	refundReference  = "cart_REFUND"
)

// newRefundService wires a service over the in-memory fakes.
func newRefundService(t *testing.T) (*service.Service, *fakeProvider) {
	t.Helper()

	prov := newFakeProvider(refundProviderID)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: newFakeStore(), Providers: registry})
	require.NoError(t, err)

	return svc, prov
}

// capturedCollection opens a collection and drives it to captured.
func capturedCollection(t *testing.T, svc *service.Service, key string) string {
	t.Helper()

	ctx := t.Context()
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    refundReference,
		Amount:       refundAmount,
		CurrencyCode: refundCurrency,
	})
	require.NoError(t, err)

	ses, err := svc.CreateSession(ctx, col.ID, refundProviderID,
		service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	return col.ID
}

// emptyCollection opens a collection nothing was ever captured on.
func emptyCollection(t *testing.T, svc *service.Service) string {
	t.Helper()

	col, err := svc.CreatePaymentCollection(t.Context(), service.CreateCollectionInput{
		Reference:    refundReference,
		Amount:       refundAmount,
		CurrencyCode: refundCurrency,
	})
	require.NoError(t, err)

	return col.ID
}

// TestACollectionCanBeRefundedWithoutNamingACapture is the surface the return
// flow needs.
//
// RefundPayment wants a payment id, and a caller outside this module has no way
// to get one — nothing on the cross-module surface maps a collection to its
// captures. It should not have to either: how a collected amount is split
// across captures is this module's bookkeeping.
func TestACollectionCanBeRefundedWithoutNamingACapture(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "refund-collection")

	refunds, err := svc.RefundCollection(ctx, collectionID, 1_000, "returned")
	require.NoError(t, err)

	require.Len(t, refunds, 1)
	assert.Equal(t, int64(1_000), refunds[0].Amount)

	current, err := svc.GetPaymentCollection(ctx, collectionID)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), current.RefundedAmount)
}

// TestAZeroAmountRefundsEverythingLeft is what "give the customer their money
// back" means when nobody named a figure.
func TestAZeroAmountRefundsEverythingLeft(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "refund-all")

	refunds, err := svc.RefundCollection(ctx, collectionID, 0, "")
	require.NoError(t, err)

	var total int64
	for i := range refunds {
		total += refunds[i].Amount
	}
	assert.Equal(t, refundAmount, total)
}

// TestMoreCannotBeRefundedThanTheCollectionHolds rejects BEFORE any money
// moves.
//
// Building the plan first is what makes that possible: refunding what it could
// and then failing would leave the caller with a partial refund it never asked
// for.
func TestMoreCannotBeRefundedThanTheCollectionHolds(t *testing.T) {
	svc, prov := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "refund-too-much")

	before := prov.refundCalls

	_, err := svc.RefundCollection(ctx, collectionID, refundAmount+1, "")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCollectionNothingToRefund, errors.CodeOf(err))
	assert.Equal(t, before, prov.refundCalls, "no money may move on a rejected plan")
}

// TestACollectionWithNoCaptureHasNothingToRefund names the case apart from a
// too-large request.
func TestACollectionWithNoCaptureHasNothingToRefund(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID := emptyCollection(t, svc)

	_, err := svc.RefundCollection(t.Context(), collectionID, 0, "")

	require.Error(t, err)
	assert.Equal(t, service.CodeCollectionNothingToRefund, errors.CodeOf(err))
}

// TestASecondRefundComesOutOfWhatIsLeft proves the plan reads the remaining
// amount rather than the captured one.
func TestASecondRefundComesOutOfWhatIsLeft(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "refund-twice")

	_, err := svc.RefundCollection(ctx, collectionID, 4_000, "first")
	require.NoError(t, err)

	_, err = svc.RefundCollection(ctx, collectionID, refundAmount-4_000+1, "second")
	require.Error(t, err, "the second refund may not exceed what is left")

	_, err = svc.RefundCollection(ctx, collectionID, refundAmount-4_000, "second")
	require.NoError(t, err)

	current, err := svc.GetPaymentCollection(ctx, collectionID)
	require.NoError(t, err)
	assert.Equal(t, refundAmount, current.RefundedAmount)
}
