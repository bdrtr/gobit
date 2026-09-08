package jobreport_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/jobreport"
)

// TestAReporterStartsEmptyAndIsNotShared keeps last night's number from
// standing as tonight's.
//
// The runner takes a FRESH reporter per run. A shared one would leave a job
// that reported nothing this pass showing whatever it said last pass, which is
// worse than a blank cell: it is a wrong number that looks current.
func TestAReporterStartsEmptyAndIsNotShared(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())
	assert.Empty(t, jobreport.Detail(ctx), "a fresh reporter carries nothing")

	jobreport.Report(ctx, "examined 4")
	assert.Equal(t, "examined 4", jobreport.Detail(ctx))

	nested := jobreport.WithReporter(ctx)
	assert.Empty(t, jobreport.Detail(nested),
		"a reporter attached over an existing one must not inherit its line")
}

// TestTheInnermostReporterIsTheOneThatCollects proves the nesting is not merely
// tidy.
//
// The runner attaches its reporter to the run's own context, which is derived
// from one a test or a caller may already have decorated. If an outer reporter
// won, the line would be collected by whoever is NOT recording the outcome.
func TestTheInnermostReporterIsTheOneThatCollects(t *testing.T) {
	t.Parallel()

	outer := jobreport.WithReporter(t.Context())
	inner := jobreport.WithReporter(outer)

	jobreport.Report(inner, "the run's own line")

	assert.Equal(t, "the run's own line", jobreport.Detail(inner))
	assert.Empty(t, jobreport.Detail(outer))
}

// TestTheLastReportedLineWins states the rule a job that reports as it goes
// depends on.
//
// Appending instead would grow without bound and break the tabwriter row it
// lands in; keeping the FIRST would freeze the line at "starting", which is the
// least useful moment of any run.
func TestTheLastReportedLineWins(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	jobreport.Report(ctx, "examined 10")
	jobreport.Report(ctx, "examined 20")
	jobreport.Report(ctx, "examined 30")

	assert.Equal(t, "examined 30", jobreport.Detail(ctx))
}

// TestReportingOutsideARunIsASilentNoOp is the price of the hidden channel,
// paid rather than denied.
//
// A job's own unit test calls its run function directly, and a hand-run records
// no outcome at all. Neither has a reporter, and a panic in either would turn
// "I forgot the channel is contextual" into a dead process — in a plugin, that
// is a process the author of this package will never see.
func TestReportingOutsideARunIsASilentNoOp(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() { jobreport.Report(context.Background(), "nobody is listening") })
	assert.NotPanics(t, func() { jobreport.Report(t.Context(), "still nobody") })
}

// TestAReportFromAnotherGoroutineIsReadableByTheRunner is why the mutex is
// there.
//
// The godoc says a job may report from a goroutine it started while the runner
// reads the line back on the goroutine that called the job. That is a data race
// unless the type guards it, and a race is not a defect a reader can see by
// eye — this is the case, run under -race, that says the guard is real.
func TestAReportFromAnotherGoroutineIsReadableByTheRunner(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobreport.Report(ctx, "examined 30 of 50")
		}()
	}
	wg.Wait()

	assert.Equal(t, "examined 30 of 50", jobreport.Detail(ctx))
}
