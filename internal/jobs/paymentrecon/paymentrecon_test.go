package paymentrecon_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/jobs/paymentrecon"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// fakeReconciler stands in for the payment service.
type fakeReconciler struct {
	report paymentsvc.ReconciliationReport
	err    error

	calls        int
	gotWindow    time.Duration
	gotLimit     int
	gotCancelled bool
}

// Reconcile records what it was asked and returns the scripted report.
func (f *fakeReconciler) Reconcile(
	ctx context.Context, unchangedFor time.Duration, limit int,
) (paymentsvc.ReconciliationReport, error) {
	f.calls++
	f.gotWindow = unchangedFor
	f.gotLimit = limit
	f.gotCancelled = ctx.Err() != nil

	return f.report, f.err
}

// runJob runs one pass and returns everything it logged.
func runJob(t *testing.T, r *fakeReconciler) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	def := paymentrecon.Definition(r, log)
	err := def.Run(context.Background())

	return buf.String(), err
}

// TestDefinitionAsksWithTheSettlingWindowAndTheLimit pins the two numbers the
// job owns.
//
// They are the job's, not the service's: the service refuses a window of zero
// but does not choose one, because how long a capture legitimately takes is a
// property of this installation's schedule rather than of the comparison.
func TestDefinitionAsksWithTheSettlingWindowAndTheLimit(t *testing.T) {
	r := &fakeReconciler{}
	_, err := runJob(t, r)
	require.NoError(t, err)

	assert.Equal(t, 1, r.calls)
	assert.Positive(t, r.gotWindow)
	assert.Positive(t, r.gotLimit)
	assert.False(t, r.gotCancelled)

	def := paymentrecon.Definition(r, nil)
	assert.Equal(t, paymentrecon.Name, def.Name)
	assert.Equal(t, paymentrecon.Every, def.Every)
	assert.Equal(t, paymentrecon.MaxRun, def.MaxRun)
}

// TestACleanPassStaysQuiet keeps the hourly line out of the log an operator
// reads.
//
// A line that never changes is a line nobody reads, which is how the one that
// differs gets missed.
func TestACleanPassStaysQuiet(t *testing.T) {
	r := &fakeReconciler{report: paymentsvc.ReconciliationReport{Examined: 12, Agreed: 12}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.Contains(t, out, "level=DEBUG")
	assert.NotContains(t, out, "level=ERROR")
	assert.NotContains(t, out, "level=WARN")
}

// TestADivergenceIsReportedWithTheOperatorsNextStep is the reason the job runs.
//
// The external id has to be ON the line: it is the value pasted into the
// provider's own dashboard, and it is the difference between a report and an
// alarm.
func TestADivergenceIsReportedWithTheOperatorsNextStep(t *testing.T) {
	r := &fakeReconciler{report: paymentsvc.ReconciliationReport{
		Examined: 3,
		Agreed:   2,
		Divergences: []paymentsvc.Divergence{{
			SessionID:        "payses_lost",
			CollectionID:     "paycol_lost",
			ProviderID:       "paytr",
			ExternalID:       "PAYTR-99887",
			LocalStatus:      "authorized",
			LocalAuthorized:  10_000,
			ProviderStatus:   "captured",
			ProviderCaptured: 10_000,
			CurrencyCode:     "TRY",
		}},
	}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, "PAYTR-99887")
	assert.Contains(t, out, "payses_lost")
	assert.Contains(t, out, "provider_captured=10000")
	assert.Contains(t, out, "local_status=authorized")
}

// TestEveryDivergenceGetsItsOwnLine keeps a summary count from standing in for
// the rows an operator has to work through.
func TestEveryDivergenceGetsItsOwnLine(t *testing.T) {
	r := &fakeReconciler{report: paymentsvc.ReconciliationReport{
		Examined: 2,
		Divergences: []paymentsvc.Divergence{
			{SessionID: "payses_one", ExternalID: "EXT-1"},
			{SessionID: "payses_two", ExternalID: "EXT-2"},
		},
	}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.Equal(t, 2, strings.Count(out, "a payment session disagrees with its provider"))
	assert.Contains(t, out, "EXT-1")
	assert.Contains(t, out, "EXT-2")
}

// TestWhatCouldNotBeAskedIsSaidOutLoud is the whole point of counting those
// cases apart from agreement.
//
// A pass that could not reach three providers is not clean, and a job that
// stayed silent about it would let an unverified ledger look like a verified
// one.
func TestWhatCouldNotBeAskedIsSaidOutLoud(t *testing.T) {
	cases := map[string]struct {
		report paymentsvc.ReconciliationReport
		level  string
		phrase string
	}{
		"an unreachable provider": {
			report: paymentsvc.ReconciliationReport{Examined: 4, Agreed: 3, Unreachable: 1},
			level:  "level=WARN",
			phrase: "unreachable=1",
		},
		"a provider with no inspector": {
			report: paymentsvc.ReconciliationReport{Examined: 4, Agreed: 3, Unaskable: 1},
			level:  "level=INFO",
			phrase: "unaskable=1",
		},
		"a session the provider disowns": {
			report: paymentsvc.ReconciliationReport{Examined: 4, Agreed: 3, Unknown: 1},
			level:  "level=ERROR",
			phrase: "unknown=1",
		},
		"a filled limit": {
			report: paymentsvc.ReconciliationReport{Examined: 50, Agreed: 49, Unreachable: 1, Truncated: true},
			level:  "level=WARN",
			phrase: "the reconciliation pass filled its limit",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runJob(t, &fakeReconciler{report: tc.report})
			require.NoError(t, err)

			assert.Contains(t, out, tc.level)
			assert.Contains(t, out, tc.phrase)
		})
	}
}

// TestAFailedComparisonIsAFailedRun keeps a pass that never happened out of the
// listing as a success.
//
// `gobit jobs` reads the last run's outcome; a swallowed error would make the
// row say the ledgers were compared when nothing was.
func TestAFailedComparisonIsAFailedRun(t *testing.T) {
	r := &fakeReconciler{err: errors.New("the pool is closed")}

	_, err := runJob(t, r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be compared")
}

// TestDefinitionToleratesANilLogger keeps a caller that passes none from
// panicking on the first pass.
func TestDefinitionToleratesANilLogger(t *testing.T) {
	r := &fakeReconciler{report: paymentsvc.ReconciliationReport{
		Divergences: []paymentsvc.Divergence{{SessionID: "payses_lost"}},
	}}

	require.NotPanics(t, func() {
		require.NoError(t, paymentrecon.Definition(r, nil).Run(context.Background()))
	})
}

// reportedBy runs one pass with a reporter attached and returns the line it
// left for `gobit jobs`.
func reportedBy(t *testing.T, r *fakeReconciler) (string, error) {
	t.Helper()

	ctx, reporter := job.WithReporter(context.Background())
	err := paymentrecon.Definition(r, slog.New(slog.DiscardHandler)).Run(ctx)

	return reporter.Detail(), err
}

// TestADivergenceReachesTheListingAndNotOnlyTheLog is the hole this conversion
// closes, and it is the expensive one.
//
// This job never fails: it reports and does not repair. Before a successful run
// could leave a detail, that meant its findings reached the LOG and nothing
// else — `gobit jobs` printed "ok" with an empty cell for a pass that found
// money captured with no local record, exactly as it did for a pass that found
// nothing. An operator had to already suspect the problem to go looking for it.
func TestADivergenceReachesTheListingAndNotOnlyTheLog(t *testing.T) {
	r := &fakeReconciler{report: paymentsvc.ReconciliationReport{
		Examined:    4,
		Agreed:      3,
		Divergences: []paymentsvc.Divergence{{SessionID: "session_1"}},
	}}

	detail, err := reportedBy(t, r)
	require.NoError(t, err, "a divergence is a finding for a human, not a failed run")

	assert.Equal(t, "examined 4, agreed 3, DIVERGENT 1", detail)
	assert.NotContains(t, detail, "session_1",
		"the session ids belong in the log; the listing cell is one column of a table")
}

// TestACleanPassStillSaysWhatItExamined keeps the quiet hour readable.
//
// A number that appears only when something is wrong is a number nobody has
// learned to read. "examined 4, agreed 4" every hour is what makes "DIVERGENT
// 1" legible the hour it appears.
func TestACleanPassStillSaysWhatItExamined(t *testing.T) {
	detail, err := reportedBy(t, &fakeReconciler{
		report: paymentsvc.ReconciliationReport{Examined: 4, Agreed: 4},
	})
	require.NoError(t, err)

	assert.Equal(t, "examined 4, agreed 4", detail)
}

// TestAnEmptySuspectSetIsNotABlankCell separates two facts the listing must not
// merge.
//
// A blank DETAIL is what a job that has never run looks like, and for this job
// that reading is exactly backwards: "nothing was suspect" and "nothing has
// been compared at all" are opposite conclusions about the same money.
func TestAnEmptySuspectSetIsNotABlankCell(t *testing.T) {
	detail, err := reportedBy(t, &fakeReconciler{})
	require.NoError(t, err)

	assert.Equal(t, "nothing to compare", detail)
}

// TestWhatCouldNotBeAskedIsInTheListingToo keeps an unanswered question from
// reading as an answer.
//
// The three "could not ask" counts are the reason this job exists in the shape
// it does — a pass that could not reach a provider proves nothing about it —
// and folding them out of the listing would make the cell claim more than the
// pass established.
func TestWhatCouldNotBeAskedIsInTheListingToo(t *testing.T) {
	detail, err := reportedBy(t, &fakeReconciler{report: paymentsvc.ReconciliationReport{
		Examined: 9, Agreed: 3, Unknown: 2, Unreachable: 3, Unaskable: 1, Truncated: true,
	}})
	require.NoError(t, err)

	assert.Contains(t, detail, "unknown 2")
	assert.Contains(t, detail, "unreachable 3")
	assert.Contains(t, detail, "unaskable 1")
	assert.Contains(t, detail, "limit of 50 was filled")
	assert.NotContains(t, detail, "\n", "Outcome.Detail is one line of a table")
}

// TestAZeroFindingIsOmittedRatherThanPrinted keeps the cell scannable.
//
// Four zeroes are four things to read past before reaching the one that is not
// zero, in a column read during an incident.
func TestAZeroFindingIsOmittedRatherThanPrinted(t *testing.T) {
	detail, err := reportedBy(t, &fakeReconciler{
		report: paymentsvc.ReconciliationReport{Examined: 2, Agreed: 2},
	})
	require.NoError(t, err)

	assert.NotContains(t, detail, "unknown")
	assert.NotContains(t, detail, "unreachable")
	assert.NotContains(t, detail, "unaskable")
	assert.NotContains(t, detail, "DIVERGENT")
}
