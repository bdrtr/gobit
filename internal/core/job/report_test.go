package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/job"
)

// detailedError is an error carrying the operator note the runner has always
// read off a failure.
//
// It is the shape internal/jobs/outboxrelay's dead-letter error has, restated
// here so these tests cover the CONTRACT rather than that job's spelling of it.
type detailedError struct{ detail string }

func (e detailedError) Error() string     { return "the work could not be done" }
func (e detailedError) JobDetail() string { return e.detail }

// runOnce runs one job through a real runner and returns the recorded outcome.
func runOnce(t *testing.T, work job.Func) job.Outcome {
	t.Helper()

	store := newFakeStore()
	registry := job.NewRegistry()
	require.NoError(t, registry.Add(job.Definition{
		Name: "reporting", Every: time.Hour, MaxRun: time.Minute, Run: work,
	}))

	runner, err := job.New(job.Options{Registry: registry, Store: store})
	require.NoError(t, err)

	runUntil(t, runner, func() bool { return len(store.outcomes()) == 1 })

	outcomes := store.outcomes()
	require.Len(t, outcomes, 1)

	return outcomes[0]
}

// TestASuccessfulRunCanLeaveADetail is the whole point of the channel.
//
// Before [job.Report] the runner filled Outcome.Detail only from an error, so a
// run that SUCCEEDED had nowhere to put a number — measured, not assumed: the
// same fixture with the call removed records an empty string. Two pieces of
// work hit that wall from opposite sides (the outbox relay had to FAIL to
// mention its dead letters; a plugin's payment watch could only log), and this
// is the assertion that says the wall is gone.
func TestASuccessfulRunCanLeaveADetail(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		job.Report(ctx, "published 12, failed 0")

		return nil
	})

	require.NoError(t, outcome.Err)
	assert.Equal(t, "published 12, failed 0", outcome.Detail,
		"a run that succeeded must be able to say something; if this is empty the "+
			"only way a job can report a number is to fail, which is where this started")
}

// TestAFailingRunReportsExactlyWhatItAlwaysDid pins the behavior the new
// channel must not have disturbed.
//
// The error's JobDetail WINS over anything reported during the run. Without
// that precedence a job that reported progress and then failed would show the
// progress line where the reason used to be — in the one column an operator
// reads during an incident.
func TestAFailingRunReportsExactlyWhatItAlwaysDid(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		job.Report(ctx, "published 12, failed 0")

		return detailedError{detail: "17 dead-lettered; oldest order.placed"}
	})

	require.Error(t, outcome.Err)
	assert.Equal(t, "17 dead-lettered; oldest order.placed", outcome.Detail,
		"the error's own note must override a line reported mid-run, or every job "+
			"that starts reporting quietly rewrites what its failures say")
}

// TestAFailingRunWithNoNoteStillReportsNothing covers the other half of "did
// not change".
//
// A job that never calls Report and fails with a plain error recorded an empty
// detail before this change, and has to keep doing so — otherwise the listing
// grows content nobody wrote.
func TestAFailingRunWithNoNoteStillReportsNothing(t *testing.T) {
	outcome := runOnce(t, func(context.Context) error {
		return errors.New("the job could not do its work")
	})

	require.Error(t, outcome.Err)
	assert.Empty(t, outcome.Detail)
}

// TestAReportedLineSurvivesAFailureThatCarriesNone is the one behavior that IS
// new for a failing run, and it is deliberate.
//
// A pass cut off by its deadline, or one whose second half broke, keeps
// whatever it last said. "examined 30 of 50" beside the error beats the blank
// cell it used to leave, and it is only reachable for a job that calls Report —
// so nothing that failed before this change reports differently.
func TestAReportedLineSurvivesAFailureThatCarriesNone(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		job.Report(ctx, "examined 30 of 50")

		return errors.New("the provider stopped answering")
	})

	require.Error(t, outcome.Err)
	assert.Equal(t, "examined 30 of 50", outcome.Detail)
}

// TestTheLastReportedLineWins states the rule a job that reports as it goes
// depends on.
//
// Appending instead would grow without bound and break the tabwriter row it
// lands in; keeping the FIRST would freeze the line at "starting", which is the
// least useful moment of any run.
func TestTheLastReportedLineWins(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		job.Report(ctx, "examined 10")
		job.Report(ctx, "examined 20")
		job.Report(ctx, "examined 30")

		return nil
	})

	require.NoError(t, outcome.Err)
	assert.Equal(t, "examined 30", outcome.Detail)
}

// TestReportingOutsideARunIsASilentNoOp is the price of the hidden channel,
// paid rather than denied.
//
// A job's own unit test calls its run function directly, and [job.Runner.RunNow]
// records no outcome at all. Neither has a reporter, and a panic in either
// would turn "I forgot the channel is contextual" into a dead process.
func TestReportingOutsideARunIsASilentNoOp(t *testing.T) {
	assert.NotPanics(t, func() { job.Report(context.Background(), "nobody is listening") })
	assert.NotPanics(t, func() { job.Report(t.Context(), "still nobody") })
}

// TestAReporterStartsEmptyAndIsNotShared keeps last night's number from
// standing as tonight's.
//
// The runner takes a FRESH reporter per run. A shared one would leave a job
// that reported nothing this pass showing whatever it said last pass, which is
// worse than a blank cell: it is a wrong number that looks current.
func TestAReporterStartsEmptyAndIsNotShared(t *testing.T) {
	ctx, first := job.WithReporter(t.Context())
	assert.Empty(t, first.Detail(), "a fresh reporter carries nothing")

	job.Report(ctx, "examined 4")
	assert.Equal(t, "examined 4", first.Detail())

	_, second := job.WithReporter(ctx)
	assert.Empty(t, second.Detail(),
		"a reporter attached over an existing one must not inherit its line")
}

// TestTheInnermostReporterIsTheOneThatCollects proves the nesting is not
// merely tidy.
//
// The runner attaches its reporter to the run's own context, which is derived
// from one a test or a caller may already have decorated. If an outer reporter
// won, the line would be collected by whoever is NOT recording the outcome.
func TestTheInnermostReporterIsTheOneThatCollects(t *testing.T) {
	outer, outerReporter := job.WithReporter(t.Context())
	inner, innerReporter := job.WithReporter(outer)

	job.Report(inner, "the run's own line")

	assert.Equal(t, "the run's own line", innerReporter.Detail())
	assert.Empty(t, outerReporter.Detail())
}
