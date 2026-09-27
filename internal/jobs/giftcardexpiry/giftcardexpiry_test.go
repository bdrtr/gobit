package giftcardexpiry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
)

// fakeExpirer answers with scripted counts and records the batch asked for.
type fakeExpirer struct {
	closed, held int
	err          error
	limit        int64
}

func (f *fakeExpirer) ExpireGiftCards(_ context.Context, limit int64) (closed, held int, err error) {
	f.limit = limit

	return f.closed, f.held, f.err
}

// TestARunClosesABatchAndSaysSo: the operator's line counts what was closed
// and what a payment still holds.
func TestARunClosesABatchAndSaysSo(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeExpirer{closed: 3, held: 1}

	require.NoError(t, Definition(fake, nil).Run(ctx))

	assert.Equal(t, int64(batch), fake.limit)
	assert.Equal(t, "closed 3 expired gift cards; 1 wait for a payment that holds them", jobreport.Detail(ctx))
}

// TestAFailedRunReportsWhatItClosed: the cards closed before the failure are
// closed.
func TestAFailedRunReportsWhatItClosed(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeExpirer{closed: 2, err: coreerrors.Unavailable("db_down", "the database did not answer")}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable))
	assert.Equal(t, "closed 2 expired gift cards; 0 wait for a payment that holds them", jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakeExpirer{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
}
