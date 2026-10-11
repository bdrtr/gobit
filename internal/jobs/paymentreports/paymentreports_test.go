package paymentreports

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// fakeCompleter answers with a scripted pass and records the moment.
type fakeCompleter struct {
	pass checkoutwf.ReportPass
	err  error
	now  time.Time
}

func (f *fakeCompleter) CompleteReportedPayments(_ context.Context, now time.Time) (checkoutwf.ReportPass, error) {
	f.now = now

	return f.pass, f.err
}

// TestARunSweepsAtItsMoment: the run hands the checkout the moment it runs at.
func TestARunSweepsAtItsMoment(t *testing.T) {
	t.Parallel()

	fake := &fakeCompleter{}
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
	require.NoError(t, run(context.Background(), fake, slog.New(slog.DiscardHandler), now))

	assert.Equal(t, now, fake.now)
}

// TestARunReportsEveryCountEvenWhenItFails: what was settled before a failure
// was settled, and the operator's line says so (ADR 0438).
func TestARunReportsEveryCountEvenWhenItFails(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(context.Background())
	fake := &fakeCompleter{
		pass: checkoutwf.ReportPass{Discovered: 1, Completed: 2, Refunded: 3, Closed: 4, Left: 5, Retrying: 6, GaveUp: 7},
		err:  coreerrors.Unavailable("payment_down", "the payment module did not answer"),
	}

	err := Definition(fake, nil).Run(ctx)

	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "the cause's kind is kept")
	assert.Equal(t, "completed 2, refunded 3, closed 4, left 5, retrying 6, gave up 7; found 1 unreported",
		jobreport.Detail(ctx))
}

// TestTheDefinitionFitsItsInterval: a run cannot outlast the gap to the next,
// and the first retry of a failed completion is never later than one run.
func TestTheDefinitionFitsItsInterval(t *testing.T) {
	t.Parallel()

	definition := Definition(&fakeCompleter{}, nil)

	assert.Equal(t, Name, definition.Name)
	assert.Less(t, definition.MaxRun, definition.Every)
	assert.LessOrEqual(t, definition.Every, checkoutwf.ReportRetryBase)
}

// TestMoneyThatNeedsAPersonIsSaidOutLoud: a pass that refunded, gave up or
// left a payment warns in the job's own lines, and a pass that only completed
// says nothing there.
func TestMoneyThatNeedsAPersonIsSaidOutLoud(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		pass   checkoutwf.ReportPass
		phrase string
	}{
		"refunded":       {checkoutwf.ReportPass{Refunded: 1}, "refunded=1"},
		"gave up":        {checkoutwf.ReportPass{GaveUp: 1}, "gave_up=1"},
		"left":           {checkoutwf.ReportPass{Left: 1}, "left=1"},
		"completed only": {checkoutwf.ReportPass{Completed: 3}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var lines strings.Builder
			log := slog.New(slog.NewTextHandler(&lines, nil))
			now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

			require.NoError(t, run(context.Background(), &fakeCompleter{pass: tc.pass}, log, now))

			if tc.phrase == "" {
				assert.Empty(t, lines.String())

				return
			}
			assert.Contains(t, lines.String(), "level=WARN")
			assert.Contains(t, lines.String(), tc.phrase)
		})
	}
}
