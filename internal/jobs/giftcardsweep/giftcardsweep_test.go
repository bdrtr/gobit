package giftcardsweep

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakeSweeper answers with scripted counts and records the window's start.
type fakeSweeper struct {
	orders, issued, waiting int
	err                     error
	since                   time.Time
}

func (f *fakeSweeper) Sweep(_ context.Context, since time.Time) (orders, issued, waiting int, err error) {
	f.since = since

	return f.orders, f.issued, f.waiting, f.err
}

// TestARunLooksBackOneWindow: the orders placed in the last week are swept.
func TestARunLooksBackOneWindow(t *testing.T) {
	t.Parallel()

	fake := &fakeSweeper{}
	now := time.Date(2026, time.September, 27, 12, 0, 0, 0, time.UTC)
	require.NoError(t, run(context.Background(), fake, slog.New(slog.DiscardHandler), now))

	assert.Equal(t, now.Add(-7*24*time.Hour), fake.since)
}

// TestARunReportsWhatItIssuedEvenWhenItFails: the cards made before a failure
// exist, and the operator's line says so.
func TestARunReportsWhatItIssuedEvenWhenItFails(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeSweeper{orders: 1, issued: 2, waiting: 3,
		err: coreerrors.Unavailable("payment_down", "the payment module did not answer")}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "the cause's kind is kept")
	assert.Equal(t, "issued 2 gift cards on 1 orders; 3 orders wait for their capture", jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakeSweeper{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
}
