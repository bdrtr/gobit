//go:build integration

package payment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestMoneyMovedCarriesTheStoredMomentOnTheRealDatabase is ADR 0437's rule over
// the real schema (D278): payment.captured's occurred_at is the capture row's
// captured_at and each payment.refunded's the refund row's created_at,
// compared in Go at full precision.
func TestMoneyMovedCarriesTheStoredMomentOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "stored-moment-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	first, err := svc.RefundPayment(ctx, pay.ID, testAmount/2, "partial refund")
	require.NoError(t, err)
	second, err := svc.RefundPayment(ctx, pay.ID, 0, "remaining refund")
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx, `
        SELECT e.id, e.data->>'occurred_at', COALESCE(p.captured_at, r.created_at)
        FROM event_outbox e
        LEFT JOIN payments p ON e.name = $2 AND e.id = $2 || ':' || p.id
        LEFT JOIN refunds r ON e.name = $3 AND e.id = $3 || ':' || r.id
        WHERE e.data->>'payment_collection_id' = $1`,
		col.ID, service.EventPaymentCaptured, service.EventPaymentRefunded)
	require.NoError(t, err)
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var text *string
		var column *time.Time
		require.NoError(t, rows.Scan(&id, &text, &column))
		ids = append(ids, id)
		if !assert.NotNil(t, text, "%s carries no moment", id) || !assert.NotNil(t, column, "%s has no row to compare", id) {
			continue
		}
		moment, parseErr := time.Parse(time.RFC3339Nano, *text)
		require.NoError(t, parseErr)
		assert.True(t, moment.Equal(*column), "%s carries %s and its row holds %s", id, moment, *column)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{
		service.EventPaymentCaptured + ":" + pay.ID,
		service.EventPaymentRefunded + ":" + first.ID,
		service.EventPaymentRefunded + ":" + second.ID,
	}, ids)
}
