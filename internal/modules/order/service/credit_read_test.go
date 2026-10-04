package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestACreditSentTwiceCreditsOnce is ADR 0388: a credit naming the credited
// total read goes through while that holds, and the same one sent again, the
// total having moved, is refused and writes nothing; one read after the first
// goes through, and one naming no total is judged by the ceiling alone.
func TestACreditSentTwiceCreditsOnce(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	none := int64(0)

	in := service.CreateCreditLineInput{Amount: 100, Reason: "goodwill", ReadCredited: &none}
	_, err = e.svc.CreateCreditLine(ctx, order.ID, in)
	require.NoError(t, err)
	_, err = e.svc.CreateCreditLine(ctx, order.ID, in)
	require.Error(t, err)
	assert.Equal(t, service.CodeCreditMoved, errors.CodeOf(err), "the same form sent twice: %v", err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Contains(t, err.Error(), "has 100 credited now, not 0")
	lines, err := e.svc.ListCreditLines(ctx, order.ID)
	require.NoError(t, err)
	assert.Len(t, lines, 1, "the refused credit wrote nothing")

	read := int64(100)
	in.ReadCredited = &read
	_, err = e.svc.CreateCreditLine(ctx, order.ID, in)
	require.NoError(t, err, "read after the first, it goes through")
	in.ReadCredited = nil
	_, err = e.svc.CreateCreditLine(ctx, order.ID, in)
	require.NoError(t, err, "no total read, the ceiling alone judges")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(300), detail.CreditedTotal)
}

// TestACreditReadAboveTheTotalIsRefused compares the total read for equality:
// a form drawn with more credited than the order has is as stale as one drawn
// with less.
func TestACreditReadAboveTheTotalIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	more := int64(50)
	_, err = e.svc.CreateCreditLine(ctx, order.ID, service.CreateCreditLineInput{
		Amount: 100, Reason: "goodwill", ReadCredited: &more,
	})
	assert.Equal(t, service.CodeCreditMoved, errors.CodeOf(err), "%v", err)
}
