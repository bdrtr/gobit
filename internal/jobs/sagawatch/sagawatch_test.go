package sagawatch_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/internal/jobs/sagawatch"
)

// fakeReader stands in for the stuck-saga listing.
type fakeReader struct {
	page pgstore.StuckPage
	err  error

	gotFilter pgstore.StuckFilter
	calls     int
}

// Stuck records the filter and returns the scripted page.
func (f *fakeReader) Stuck(_ context.Context, filter pgstore.StuckFilter) (pgstore.StuckPage, error) {
	f.calls++
	f.gotFilter = filter

	return f.page, f.err
}

// runJob runs one pass and returns everything it logged.
func runJob(t *testing.T, r *fakeReader) (string, error) {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	err := sagawatch.Definition(r, log).Run(context.Background())

	return buf.String(), err
}

// TestDefinitionAsksWithAStaleWindowAndALimit pins the two numbers the job owns.
//
// The window has to be LONGER than the workflow's lease: set it shorter and the
// listing names sagas that are still running, and an operator who released
// their stock by hand would double-free inventory the saga is about to
// compensate.
func TestDefinitionAsksWithAStaleWindowAndALimit(t *testing.T) {
	r := &fakeReader{}
	_, err := runJob(t, r)
	require.NoError(t, err)

	assert.Equal(t, 1, r.calls)
	assert.Positive(t, r.gotFilter.StaleAfter)
	assert.Positive(t, r.gotFilter.Limit)

	def := sagawatch.Definition(r, nil)
	assert.Equal(t, sagawatch.Name, def.Name)
	assert.Equal(t, sagawatch.Every, def.Every)
	assert.Equal(t, sagawatch.MaxRun, def.MaxRun)
}

// TestAQuietPassStaysQuiet keeps the hourly line out of the log an operator
// reads.
func TestAQuietPassStaysQuiet(t *testing.T) {
	out, err := runJob(t, &fakeReader{})
	require.NoError(t, err)

	assert.Contains(t, out, "level=DEBUG")
	assert.NotContains(t, out, "level=ERROR")
}

// TestAnAbandonedSagaIsReportedWithItsExecutionIDs is the reason the job runs.
//
// The ids are the operator's next command; without them the line is an alarm
// rather than a report.
func TestAnAbandonedSagaIsReportedWithItsExecutionIDs(t *testing.T) {
	r := &fakeReader{page: pgstore.StuckPage{
		Executions: []*workflow.Execution{
			{ID: "exec_held", Status: workflow.StatusRunning},
		},
	}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, "exec_held")
	assert.Contains(t, out, "gobit recover")
}

// TestCompensationFailedIsLeftToTheEngine holds the job's narrowing.
//
// That status is the engine's own statement that a human is needed, written and
// logged at ERROR in process at the moment it happened. Re-reporting it every
// hour would train an operator to ignore the line that carries the class
// nothing else reports.
func TestCompensationFailedIsLeftToTheEngine(t *testing.T) {
	r := &fakeReader{page: pgstore.StuckPage{
		Executions: []*workflow.Execution{
			{ID: "exec_already_shouted", Status: workflow.StatusCompensationFailed},
		},
	}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.NotContains(t, out, "level=ERROR")
	assert.NotContains(t, out, "exec_already_shouted")
	assert.Contains(t, out, "level=DEBUG")
}

// TestOnlyTheAbandonedOnesAreCounted keeps a mixed page from inflating the
// number an operator acts on.
func TestOnlyTheAbandonedOnesAreCounted(t *testing.T) {
	r := &fakeReader{page: pgstore.StuckPage{
		Executions: []*workflow.Execution{
			{ID: "exec_held", Status: workflow.StatusRunning},
			{ID: "exec_already_shouted", Status: workflow.StatusCompensationFailed},
			nil,
		},
	}}

	out, err := runJob(t, r)
	require.NoError(t, err)

	assert.Contains(t, out, "abandoned=1")
	assert.Contains(t, out, "exec_held")
	assert.NotContains(t, out, "exec_already_shouted")
}

// TestAFailedReadIsAFailedRun keeps a pass that never happened out of the
// listing as a success.
func TestAFailedReadIsAFailedRun(t *testing.T) {
	_, err := runJob(t, &fakeReader{err: errors.New("the pool is closed")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not be read")
}

// TestDefinitionToleratesANilLogger keeps a caller that passes none from
// panicking on the first pass.
func TestDefinitionToleratesANilLogger(t *testing.T) {
	r := &fakeReader{page: pgstore.StuckPage{
		Executions: []*workflow.Execution{{ID: "exec_held", Status: workflow.StatusRunning}},
	}}

	require.NotPanics(t, func() {
		require.NoError(t, sagawatch.Definition(r, nil).Run(context.Background()))
	})
}
