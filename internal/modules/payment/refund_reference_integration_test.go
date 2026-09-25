//go:build integration

package payment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestARefundKeepsItsCauseInTheRealSchema is ADR 0187 on the real rows: a
// refund spread over two captures writes the cause on both refund rows, in the
// transactions that write them, and the movements the order module reads carry
// it back.
func TestARefundKeepsItsCauseInTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	collection, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: 10_000, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	for _, key := range []string{"cause-first", "cause-second"} {
		session, err := svc.CreateSession(ctx, collection.ID, manual.ID, service.CreateSessionInput{
			Amount: 5_000, IdempotencyKey: key + "-" + collection.ID,
		})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(ctx, session.ID)
		require.NoError(t, err)
		_, err = svc.CapturePayment(ctx, session.ID, 0)
		require.NoError(t, err)
	}

	refunds, err := svc.RefundCollection(ctx, collection.ID, 0, "returned", "ret_cause")
	require.NoError(t, err)
	require.Len(t, refunds, 2)

	movements, err := svc.ListPaymentMovementsByIDs(ctx, []string{collection.ID})
	require.NoError(t, err)
	causes := map[string]int{}
	for _, movement := range movements {
		causes[movement.Kind+":"+movement.Reference]++
	}
	assert.Equal(t, map[string]int{"capture:": 2, "refund:ret_cause": 2}, causes,
		"both refund rows carry the cause and neither capture has one")
}
