package paymentpaytr

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/jobreport"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// TestThePendingWatchIsDeclaredWithAScheduleTheSchedulerAccepts proves the
// definition this plugin hands the host can actually be admitted.
//
// The scheduler refuses a MaxRun longer than the interval, and it refuses it at
// BOOT: the process does not start, so every test in the suite fails at once
// and none of them says why. The rule itself is proved once, against the
// scheduler, in internal/app/jobs_test.go; what is checked here is that this
// plugin's numbers satisfy it, because they are the numbers that would take a
// real installation down.
func TestThePendingWatchIsDeclaredWithAScheduleTheSchedulerAccepts(t *testing.T) {
	t.Parallel()

	declared := pendingWatch(newModule(config{}, nil))

	assert.Equal(t, JobName, declared.Name)
	assert.Positive(t, declared.Every)
	assert.Positive(t, declared.MaxRun)
	assert.LessOrEqual(t, declared.MaxRun, declared.Every,
		"a run that outlasts its own interval is due again before it finished; the "+
			"scheduler refuses the definition and the process does not start")
	require.NotNil(t, declared.Run, "a job with no work is refused at boot, not skipped")
}

// TestTheWatchFailsRatherThanReportingNothingWhenTheStoreIsMissing keeps a
// silent success out of `gobit jobs`.
//
// Register is what builds the store. A pass that ran before it — or after a
// Register that never got as far as the pool — has looked at no rows at all,
// and returning nil would put an "ok" in the listing for a watch that examined
// nothing. That is precisely the reading the listing exists to prevent.
func TestTheWatchFailsRatherThanReportingNothingWhenTheStoreIsMissing(t *testing.T) {
	t.Parallel()

	m := newModule(config{}, nil)
	require.Nil(t, m.store, "the store is built in Register, not in the constructor")

	err := m.watchPending(t.Context())

	require.Error(t, err)
	assert.Contains(t, err.Error(), ModuleName)
}

// TestSetupDeclaresTheWatchToTheHost is this capability's consumer check.
//
// [coreplugin.Host.RegisterJob] was built for this plugin, and a registration
// surface nobody calls is the defect class this repository names as its most
// expensive. Nothing else in the build would notice the call disappearing: the
// plugin would compile, install, take payments, and simply never report a
// pending pile again — which looks exactly like a pile that is empty.
func TestSetupDeclaresTheWatchToTheHost(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	registry := coreplugin.NewRegistry(log)
	registry.Add(New())

	host := coreplugin.NewHost(c, nil, nil, log, map[string]string{
		settingMerchantID: "123456",
		settingKey:        "TESTKEY",
		settingSalt:       "TESTSALT",
		settingSuccessURL: "https://shop.example.test/done",
		settingFailureURL: "https://shop.example.test/failed",
	})
	require.NoError(t, registry.Install(t.Context(), host))

	jobs := host.Jobs()
	require.Len(t, jobs, 1, "the plugin has to DECLARE its watch, not merely be able to")
	assert.Equal(t, JobName, jobs[0].Name)
	assert.Equal(t, Name, jobs[0].PluginName())
}

// fakePendingLister is the watch's one dependency, without a database.
type fakePendingLister struct {
	rows []payment
	err  error
}

func (f fakePendingLister) pending(context.Context, time.Duration, int) ([]payment, error) {
	return f.rows, f.err
}

// pendingRows builds n rows the pass only ever counts.
func pendingRows(n int) []payment {
	rows := make([]payment, n)
	for i := range rows {
		rows[i] = payment{Status: statusPending}
	}

	return rows
}

// TestThePassLeavesItsCountWhereAnOperatorLooksFirst is this plugin's half of
// ADR 0069, and the reason the reporter is an exported type.
//
// The watch SUCCEEDS when it finds stuck payments — deliberately, because the
// pile has no off switch and a permanently FAILED row gets skimmed past. Until
// the channel was published, succeeding meant the DETAIL column of `gobit jobs`
// stayed blank whether three payments were stuck or none were, and the only
// record was a log line somebody had to already be reading. This is a PLUGIN's
// own unit test installing a reporter and reading the line back, which is the
// property the core's jobs had and a plugin's did not.
func TestThePassLeavesItsCountWhereAnOperatorLooksFirst(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	require.NoError(t, runPendingWatch(ctx,
		fakePendingLister{rows: pendingRows(3)}, slog.New(slog.DiscardHandler)))

	assert.Equal(t, "3 payment(s) pending for over "+pendingGrace.String(), jobreport.Detail(ctx),
		"a plugin's job has to be able to say something while SUCCEEDING; if this is "+
			"empty the only channel left is the failure this watch refuses to raise")
}

// TestTheQuietPassSaysSoRatherThanSayingNothing keeps the number legible.
//
// A blank cell is what a job that has never run looks like. "0 pending" every
// hour is what makes the hour it becomes 3 mean something, and it is the same
// choice internal/jobs/sagawatch and internal/jobs/paymentrecon made.
func TestTheQuietPassSaysSoRatherThanSayingNothing(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	require.NoError(t, runPendingWatch(ctx,
		fakePendingLister{}, slog.New(slog.DiscardHandler)))

	assert.Equal(t, "0 pending", jobreport.Detail(ctx))
}

// TestAFilledCapIsReportedAsFilled is the one that stops a number from lying.
//
// [pendingWatchLimit] caps the query, so a hundred rows means "a hundred OR
// MORE". A cell reading a bare "100" is the number an operator would use to
// decide the incident is small, and the log has carried "truncated" since the
// day this job was written — the listing had no way to.
func TestAFilledCapIsReportedAsFilled(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	require.NoError(t, runPendingWatch(ctx,
		fakePendingLister{rows: pendingRows(pendingWatchLimit)}, slog.New(slog.DiscardHandler)))

	assert.Contains(t, jobreport.Detail(ctx), "so there may be more")
}

// TestAPassThatCouldNotReadReportsNothingAtAll keeps a reassuring count off the
// run that examined nothing.
//
// The read failed, so the pass knows nothing about how many payments are
// pending. Reporting "0 pending" here would put the calmest line in the table
// beside the run that looked at no rows at all.
func TestAPassThatCouldNotReadReportsNothingAtAll(t *testing.T) {
	t.Parallel()

	ctx := jobreport.WithReporter(t.Context())

	err := runPendingWatch(ctx,
		fakePendingLister{err: errors.New("the connection went away")},
		slog.New(slog.DiscardHandler))

	require.Error(t, err)
	assert.Empty(t, jobreport.Detail(ctx))
}
