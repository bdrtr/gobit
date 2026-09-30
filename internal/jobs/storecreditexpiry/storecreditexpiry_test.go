package storecreditexpiry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakeExpirer answers with a scripted count and records the batch asked for.
type fakeExpirer struct {
	written int
	err     error
	limit   int64
}

func (f *fakeExpirer) ExpireStoreCredit(_ context.Context, limit int64) (int, error) {
	f.limit = limit

	return f.written, f.err
}

// TestARunSettlesABatchAndSaysSo: the operator's line counts the balances a
// run took expired credit back from.
func TestARunSettlesABatchAndSaysSo(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeExpirer{written: 4}

	require.NoError(t, Definition(fake, nil).Run(ctx))

	assert.Equal(t, int64(batch), fake.limit)
	assert.Equal(t, "took back expired credit from 4 balances", jobreport.Detail(ctx))
}

// TestAFailedRunReportsWhatItSettled: the rows written before the failure are
// committed.
func TestAFailedRunReportsWhatItSettled(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeExpirer{written: 2, err: coreerrors.Unavailable("db_down", "the database did not answer")}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
	assert.Equal(t, "took back expired credit from 2 balances", jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakeExpirer{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
}
