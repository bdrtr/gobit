package offlineexpiry

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// fakeOverdue pages through a fixed, oldest-first list the way the real read
// does: after the key, at most the limit.
type fakeOverdue struct {
	sessions []models.PaymentSession
	err      error
	reads    int
}

func (f *fakeOverdue) ListOverdueOffline(
	_ context.Context, _ time.Time, after paymentsvc.OverdueKey, limit int32,
) ([]models.PaymentSession, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	out := []models.PaymentSession{}
	for i := range f.sessions {
		ses := &f.sessions[i]
		if ses.CreatedAt.Before(after.OpenedAt) ||
			(ses.CreatedAt.Equal(after.OpenedAt) && ses.ID <= after.SessionID) {
			continue
		}
		if len(out) == int(limit) {
			break
		}
		out = append(out, *ses)
	}

	return out, nil
}

// fakeLinks binds collections to orders.
type fakeLinks map[string][]string

func (f fakeLinks) ListManyByTo(_ context.Context, name string, toIDs []string) (map[string][]string, error) {
	if name != linkOrderPayment {
		return nil, fmt.Errorf("unexpected link %q", name)
	}
	out := map[string][]string{}
	for _, id := range toIDs {
		if orders, ok := f[id]; ok {
			out[id] = orders
		}
	}

	return out, nil
}

// fakeOrders records the cancels and answers each order as scripted.
type fakeOrders struct {
	canceled []string
	reasons  []string
	answer   map[string]error
}

func (f *fakeOrders) CancelPlacedOrder(_ context.Context, orderID, reason string) error {
	f.canceled = append(f.canceled, orderID)
	f.reasons = append(f.reasons, reason)

	return f.answer[orderID]
}

// overdueSessions are n sessions, one per collection col_<i>, opened a minute
// apart, oldest first.
func overdueSessions(n int) []models.PaymentSession {
	opened := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := make([]models.PaymentSession, 0, n)
	for i := range n {
		out = append(out, models.PaymentSession{
			ID:                  fmt.Sprintf("payses_%03d", i),
			PaymentCollectionID: fmt.Sprintf("col_%03d", i),
			ProviderID:          "bank_transfer",
			Status:              models.SessionAuthorized,
			CreatedAt:           opened.Add(time.Duration(i) * time.Minute),
		})
	}

	return out
}

// TestARunCancelsTheOrderOfEveryOverdueSession: each overdue session's order
// is canceled under the job's reason; a collection no order is bound to is
// nothing to do.
func TestARunCancelsTheOrderOfEveryOverdueSession(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	payments := &fakeOverdue{sessions: overdueSessions(3)}
	orders := &fakeOrders{}
	bindings := fakeLinks{"col_000": {"order_a"}, "col_001": {"order_b"}}

	require.NoError(t, Definition(payments, bindings, orders, nil).Run(ctx))

	assert.Equal(t, []string{"order_a", "order_b"}, orders.canceled)
	assert.Equal(t, []string{Reason, Reason}, orders.reasons)
	assert.Equal(t, "2 orders whose offline payment did not arrive are canceled; 0 could not be",
		jobreport.Detail(ctx))
}

// TestARefusedOrderDoesNotHoldThePage: the first page's hundred orders are
// all refused — completed, or paid after the listing — and the run reads on
// and cancels the fifty after them.
func TestARefusedOrderDoesNotHoldThePage(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	payments := &fakeOverdue{sessions: overdueSessions(page + 50)}
	bindings := fakeLinks{}
	orders := &fakeOrders{answer: map[string]error{}}
	for i := range page + 50 {
		order := fmt.Sprintf("order_%03d", i)
		bindings[fmt.Sprintf("col_%03d", i)] = []string{order}
		if i < page {
			orders.answer[order] = coreerrors.Conflict("order_not_pending", "a completed order cannot be canceled")
		}
	}

	require.NoError(t, Definition(payments, bindings, orders, nil).Run(ctx))

	assert.Len(t, orders.canceled, page+50, "every overdue order was tried")
	assert.Equal(t, "50 orders whose offline payment did not arrive are canceled; 100 could not be",
		jobreport.Detail(ctx))
}

// TestAnOrderIsCanceledOncePerRun: two overdue sessions of one order — two
// collections bound to it — cancel it once.
func TestAnOrderIsCanceledOncePerRun(t *testing.T) {
	t.Parallel()

	payments := &fakeOverdue{sessions: overdueSessions(2)}
	orders := &fakeOrders{}
	bindings := fakeLinks{"col_000": {"order_a"}, "col_001": {"order_a"}}

	require.NoError(t, Definition(payments, bindings, orders, nil).Run(context.Background()))

	assert.Equal(t, []string{"order_a"}, orders.canceled)
}

// TestAFailureEndsTheRunAndSaysWhatItDid: a cancel that fails for a reason
// other than a refusal ends the run, and the line still counts what was
// canceled before it.
func TestAFailureEndsTheRunAndSaysWhatItDid(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	payments := &fakeOverdue{sessions: overdueSessions(3)}
	orders := &fakeOrders{answer: map[string]error{
		"order_b": coreerrors.Unavailable("db_down", "the database did not answer"),
	}}
	bindings := fakeLinks{"col_000": {"order_a"}, "col_001": {"order_b"}, "col_002": {"order_c"}}

	err := Definition(payments, bindings, orders, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
	assert.Equal(t, []string{"order_a", "order_b"}, orders.canceled)
	assert.True(t, strings.HasPrefix(jobreport.Detail(ctx), "1 orders"), jobreport.Detail(ctx))
}

// TestAReadFailureIsTheRunsFailure: nothing is canceled from a listing that
// could not be read.
func TestAReadFailureIsTheRunsFailure(t *testing.T) {
	t.Parallel()

	payments := &fakeOverdue{err: coreerrors.Unavailable("db_down", "the database did not answer")}
	orders := &fakeOrders{}

	err := Definition(payments, fakeLinks{}, orders, nil).Run(context.Background())

	require.Error(t, err)
	assert.Equal(t, codeExpiryFailed, coreerrors.CodeOf(err))
	assert.Empty(t, orders.canceled)
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakeOverdue{}, fakeLinks{}, &fakeOrders{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
}
