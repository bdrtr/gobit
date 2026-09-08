package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/jobreport"
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
// Before [jobreport.Report] the runner filled Outcome.Detail only from an error,
// so a
// run that SUCCEEDED had nowhere to put a number — measured, not assumed: the
// same fixture with the call removed records an empty string. Two pieces of
// work hit that wall from opposite sides (the outbox relay had to FAIL to
// mention its dead letters; a plugin's payment watch could only log), and this
// is the assertion that says the wall is gone.
func TestASuccessfulRunCanLeaveADetail(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		jobreport.Report(ctx, "published 12, failed 0")

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
		jobreport.Report(ctx, "published 12, failed 0")

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
// A job that never reports and fails with a plain error recorded an empty
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
// cell it used to leave, and it is only reachable for a job that reports —
// so nothing that failed before this change reports differently.
func TestAReportedLineSurvivesAFailureThatCarriesNone(t *testing.T) {
	outcome := runOnce(t, func(ctx context.Context) error {
		jobreport.Report(ctx, "examined 30 of 50")

		return errors.New("the provider stopped answering")
	})

	require.Error(t, outcome.Err)
	assert.Equal(t, "examined 30 of 50", outcome.Detail)
}
