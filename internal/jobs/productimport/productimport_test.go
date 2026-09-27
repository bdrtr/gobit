package productimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeImporter answers with a scripted count and records the deadline.
type fakeImporter struct {
	applied int
	err     error
	until   time.Time
}

func (f *fakeImporter) ApplyImports(_ context.Context, until time.Time) (int, error) {
	f.until = until

	return f.applied, f.err
}

// TestARunStopsShortOfItsBound gives the service a deadline inside MaxRun, so
// the row in hand is recorded before the runner cuts the run off.
func TestARunStopsShortOfItsBound(t *testing.T) {
	t.Parallel()

	fake := &fakeImporter{applied: 3}
	started := time.Now()
	require.NoError(t, Definition(fake, nil).Run(context.Background()))

	assert.True(t, fake.until.After(started))
	assert.True(t, fake.until.Before(started.Add(MaxRun)), "the budget has to end before the run's bound")
}

// TestAFailedRunIsAnError the runner records.
func TestAFailedRunIsAnError(t *testing.T) {
	t.Parallel()

	err := Definition(&fakeImporter{err: errors.New("database down")}, nil).Run(context.Background())

	require.Error(t, err)
}
