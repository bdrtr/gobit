package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// A refund names its cause (ADR 0187).

// twiceCapturedCollection opens a collection and captures it in two halves, one
// session each, so a refund of the whole is spread over two captures.
func twiceCapturedCollection(t *testing.T, svc *service.Service) string {
	t.Helper()

	ctx := t.Context()
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: refundReference, Amount: refundAmount, CurrencyCode: refundCurrency,
	})
	require.NoError(t, err)
	for _, key := range []string{"first-half", "second-half"} {
		ses, err := svc.CreateSession(ctx, col.ID, refundProviderID,
			service.CreateSessionInput{Amount: refundAmount / 2, IdempotencyKey: key})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
		_, err = svc.CapturePayment(ctx, ses.ID, 0)
		require.NoError(t, err)
	}

	return col.ID
}

// TestEveryRefundOfASplitCarriesTheCause: a refund spread over two captures
// writes two rows, and a cause written on one of them only would leave the
// other's money unattributed in the order's books.
func TestEveryRefundOfASplitCarriesTheCause(t *testing.T) {
	svc, _ := newRefundService(t)
	collectionID := twiceCapturedCollection(t, svc)

	refunds, err := svc.RefundCollection(t.Context(), collectionID, 0, "returned", "ret_42")

	require.NoError(t, err)
	require.Len(t, refunds, 2, "the fixture has to spread the refund, or it proves one row")
	for _, refund := range refunds {
		assert.Equal(t, "ret_42", refund.Reference, refund.ID)
	}
}

// TestTheMovementsCarryTheCause is what the order module reads: the reference
// crosses the query graph on each refund movement, and a capture has none.
func TestTheMovementsCarryTheCause(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "movements-cause")

	_, err := svc.RefundCollection(ctx, collectionID, 1_000, "damaged", "claim_7")
	require.NoError(t, err)

	movements, err := svc.ListPaymentMovementsByIDs(ctx, []string{collectionID})
	require.NoError(t, err)
	references := map[string]string{}
	for _, movement := range movements {
		references[movement.Kind] = movement.Reference
	}
	assert.Equal(t, map[string]string{"capture": "", "refund": "claim_7"}, references)
}

// TestAnOperatorsRefundNamesNothing: the refund an operator makes from the
// payment surface has no cause the order recorded, and it says so rather than
// guessing one.
func TestAnOperatorsRefundNamesNothing(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "operator-refund")
	payments, err := svc.ListPayments(ctx, collectionID)
	require.NoError(t, err)
	require.Len(t, payments, 1)

	refund, err := svc.RefundPayment(ctx, payments[0].ID, 1_000, "goodwill")

	require.NoError(t, err)
	assert.Empty(t, refund.Reference)
}

// TestAReferenceIsAnIDNotProse refuses surrounding space before any money moves:
// the order reads the reference back as a key, and " ret_1" names nothing.
func TestAReferenceIsAnIDNotProse(t *testing.T) {
	svc, _ := newRefundService(t)
	ctx := t.Context()
	collectionID := capturedCollection(t, svc, "padded-reference")

	_, err := svc.RefundCollection(ctx, collectionID, 1_000, "", " ret_1")

	assert.True(t, errors.IsInvalid(err), "%v", err)
	current, err := svc.GetPaymentCollection(ctx, collectionID)
	require.NoError(t, err)
	assert.Zero(t, current.RefundedAmount, "a refused reference moves no money")
}
