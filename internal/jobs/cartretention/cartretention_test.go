package cartretention

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakeCarts holds a number of abandoned carts and deletes them a batch at a
// time, as the real delete does.
type fakeCarts struct {
	expire  bool
	waiting int64
	err     error
	calls   []int64
	nows    []time.Time
}

func (f *fakeCarts) AbandonedCartsExpire() bool { return f.expire }

func (f *fakeCarts) DeleteAbandonedCarts(_ context.Context, now time.Time, limit int64) (int64, error) {
	f.calls = append(f.calls, limit)
	f.nows = append(f.nows, now)
	if f.err != nil {
		return 0, f.err
	}
	n := min(limit, f.waiting)
	f.waiting -= n

	return n, nil
}

// TestARunDeletesEveryBatch: a run goes on until a batch comes back short,
// so a backlog larger than one batch is deleted in one run, and it says how
// many.
func TestARunDeletesEveryBatch(t *testing.T) {
	ctx := jobreport.WithReporter(context.Background())
	svc := &fakeCarts{expire: true, waiting: 2*batch + 7}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	require.NoError(t, run(ctx, svc, now))

	assert.Equal(t, []int64{batch, batch, batch}, svc.calls)
	assert.Zero(t, svc.waiting)
	for _, at := range svc.nows {
		assert.Equal(t, now, at, "one run deletes against one moment")
	}
	assert.Equal(t, "1007 abandoned carts are deleted", jobreport.Detail(ctx))
}

// TestARunWithoutAPeriodDeletesNothing: with no period set nothing is asked,
// and the line says every cart is kept rather than that none was found.
func TestARunWithoutAPeriodDeletesNothing(t *testing.T) {
	ctx := jobreport.WithReporter(context.Background())
	svc := &fakeCarts{waiting: 3}

	require.NoError(t, run(ctx, svc, time.Now()))

	assert.Empty(t, svc.calls)
	assert.Equal(t, "no cart retention period is set; every cart is kept", jobreport.Detail(ctx))
}

// TestAFailedDeleteEndsTheRun: the failure is the job's, and what was deleted
// before it is still reported.
func TestAFailedDeleteEndsTheRun(t *testing.T) {
	ctx := jobreport.WithReporter(context.Background())
	svc := &fakeCarts{expire: true, err: coreerrors.Unavailable("db_down", "the database did not answer")}

	err := run(ctx, svc, time.Now())

	require.Error(t, err)
	assert.Equal(t, codeRetentionFailed, coreerrors.CodeOf(err))
	assert.Equal(t, "0 abandoned carts are deleted", jobreport.Detail(ctx))
}
