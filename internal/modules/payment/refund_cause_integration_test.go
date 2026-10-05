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

// TestCausedRefundsOfNamesOnlyThoseCauses is ADR 0406's read on the real
// rows: the refunds of the causes asked for, whenever they were made, and
// none of another cause's or of an operator's.
func TestCausedRefundsOfNamesOnlyThoseCauses(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	collection, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: 10_000, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	session, err := svc.CreateSession(ctx, collection.ID, manual.ID, service.CreateSessionInput{
		Amount: 10_000, IdempotencyKey: "cause-of-" + collection.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, session.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, session.ID, 0)
	require.NoError(t, err)

	asked, err := svc.RefundCollection(ctx, collection.ID, 1_000, "returned", "ret_asked_"+collection.ID)
	require.NoError(t, err)
	again, err := svc.RefundCollection(ctx, collection.ID, 500, "returned", "ret_asked_"+collection.ID)
	require.NoError(t, err)
	_, err = svc.RefundCollection(ctx, collection.ID, 700, "claimed", "clm_other_"+collection.ID)
	require.NoError(t, err)
	_, err = svc.RefundCollection(ctx, collection.ID, 300, "goodwill", "")
	require.NoError(t, err)

	refunds, err := svc.CausedRefundsOf(ctx, []string{"ret_asked_" + collection.ID})
	require.NoError(t, err)
	require.Len(t, refunds, 2, "both refunds of the cause asked for, and nothing else")
	assert.Equal(t, asked[0].ID, refunds[0].ID)
	assert.Equal(t, again[0].ID, refunds[1].ID, "oldest first")
	assert.Equal(t, int64(1_000), refunds[0].Amount)
	assert.Equal(t, collection.ID, refunds[0].CollectionID)
	assert.Equal(t, testCurrency, refunds[0].CurrencyCode)
}
