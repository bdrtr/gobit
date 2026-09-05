package app

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/job"
)

// listingFixture is the moment every case below is rendered at.
var listingFixture = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

// hourly is a definition standing in for either real job.
func hourly(name string) job.Definition {
	return job.Definition{Name: name, Every: time.Hour, MaxRun: time.Minute}
}

// render prints one listing.
func render(t *testing.T, defs []job.Definition, history map[string]job.Run) string {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, printJobs(&buf, defs, history, listingFixture))

	return buf.String()
}

// TestAJobThatHasNeverRunDoesNotReadAsFine is the one row an operator must not
// skim past.
//
// It is not necessarily broken — the process may have started minutes ago — but
// a blank cell would be read as "nothing to report", and for the reconciliation
// job that is exactly backwards: nothing has been compared at all.
func TestAJobThatHasNeverRunDoesNotReadAsFine(t *testing.T) {
	out := render(t, []job.Definition{hourly("payment-reconcile")}, nil)

	assert.Contains(t, out, "payment-reconcile")
	assert.Contains(t, out, "never")
}

// TestAFailedRunKeepsItsReason keeps the outcome column from saying only that
// something happened.
func TestAFailedRunKeepsItsReason(t *testing.T) {
	out := render(t, []job.Definition{hourly("payment-reconcile")}, map[string]job.Run{
		"payment-reconcile": {
			Name:      "payment-reconcile",
			Due:       listingFixture.Add(-30 * time.Minute),
			StartedAt: listingFixture.Add(-30 * time.Minute),
			EndedAt:   listingFixture.Add(-29 * time.Minute),
			Failure:   "the payment ledgers could not be compared",
		},
	})

	assert.Contains(t, out, "FAILED: the payment ledgers could not be compared")
	assert.NotContains(t, out, "OVERDUE")
}

// TestAnUnfinishedRunIsNotCalledRunning holds the listing's own admission.
//
// A row with no end is what a live run and a dead process both leave behind.
// The lock is what tells them apart, and the listing does not have it, so it
// says so rather than guessing.
func TestAnUnfinishedRunIsNotCalledRunning(t *testing.T) {
	out := render(t, []job.Definition{hourly("saga-watch")}, map[string]job.Run{
		"saga-watch": {
			Name:      "saga-watch",
			Due:       listingFixture.Add(-10 * time.Minute),
			StartedAt: listingFixture.Add(-10 * time.Minute),
		},
	})

	assert.Contains(t, out, "unfinished (running now, or the process died)")
}

// TestOverdueNeedsTwoMissedIntervals is what tells an operator that scheduled
// work has STOPPED, and it is the reason the threshold is not one interval.
//
// A job becomes due before it runs, by definition. Flagging that as overdue
// would put the word on every healthy row and make the column mean nothing.
func TestOverdueNeedsTwoMissedIntervals(t *testing.T) {
	defs := []job.Definition{hourly("payment-reconcile")}

	justDue := render(t, defs, map[string]job.Run{
		"payment-reconcile": {
			Name:      "payment-reconcile",
			Due:       listingFixture.Add(-90 * time.Minute),
			StartedAt: listingFixture.Add(-90 * time.Minute),
			EndedAt:   listingFixture.Add(-89 * time.Minute),
		},
	})
	assert.NotContains(t, justDue, "OVERDUE")

	stopped := render(t, defs, map[string]job.Run{
		"payment-reconcile": {
			Name:      "payment-reconcile",
			Due:       listingFixture.Add(-5 * time.Hour),
			StartedAt: listingFixture.Add(-5 * time.Hour),
			EndedAt:   listingFixture.Add(-5 * time.Hour).Add(time.Minute),
		},
	})
	assert.Contains(t, stopped, "OVERDUE")
}

// TestTheListingCarriesEveryRegisteredJob keeps a job from being rendered away.
//
// The listing is how an operator learns a job exists at all; a definition that
// reaches the registry but not the page is invisible in the one place built to
// show it.
func TestTheListingCarriesEveryRegisteredJob(t *testing.T) {
	out := render(t, []job.Definition{hourly("saga-watch"), hourly("payment-reconcile")}, nil)

	assert.Contains(t, out, "saga-watch")
	assert.Contains(t, out, "payment-reconcile")
	assert.Contains(t, out, "JOB")
}
