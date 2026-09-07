//go:build integration

package app

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/core/job/jobpg"
	"github.com/bdrtr/gobit/internal/jobs/outboxrelay"
	"github.com/bdrtr/gobit/internal/jobs/paymentrecon"
	"github.com/bdrtr/gobit/internal/jobs/sagawatch"
)

// jobsEnv points the binary at a database of its own and gives it the smallest
// configuration that boots.
//
// The runner is built from the SERVER's configuration ([config.Load]) — the
// shutdown window it compares its jobs against is read from there — so the test
// speaks to it the same way an operator's environment would.
func jobsEnv(t *testing.T, dsn string) {
	t.Helper()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "jobs-integration-test-secret-32-bytes-long")
	t.Setenv("LOG_LEVEL", "warn")
}

// scheduled is everything one started runner left behind.
type scheduled struct {
	// logs is what the runner and the composition root wrote at startup.
	logs string
	// delivered are the events that reached the bus during the pass.
	delivered []eventbus.Event
	// history is the job ledger as the database holds it after the pass.
	history map[string]job.Run
}

// relayedPass boots the whole installation, writes the given events into the
// outbox and starts the job runner exactly as [serve] starts it.
//
// It returns when every registered job has a FINISHED run in the ledger, which
// is the only signal from the outside that a pass happened at all: the runner
// starts a goroutine and reports nothing to its caller.
//
// The application is closed before returning, so a test may afterwards run
// `gobit jobs` — which opens an installation of its own — against the same
// database without two composition roots holding it at once.
func relayedPass(t *testing.T, dsn string, events ...eventbus.Event) scheduled {
	t.Helper()

	ctx := context.Background()
	jobsEnv(t, dsn)

	cfg, err := config.Load()
	require.NoError(t, err)

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{})
	require.NoError(t, err, "the installation the runner needs could not be opened")
	defer closeApp()

	pool, err := container.Resolve[*db.Pool](app.container, svcDB)
	require.NoError(t, err)

	// The subscription is bound BEFORE the runner starts: what is being proved
	// is that the relay hands the event to the bus, and a handler attached
	// afterwards could miss the very delivery the test is waiting for.
	delivered := make(chan eventbus.Event, len(events)+1)
	bus, err := container.Resolve[eventbus.EventBus](app.container, svcEventBus)
	require.NoError(t, err)

	for _, event := range events {
		require.NoError(t, bus.Subscribe(event.Name, func(_ context.Context, e eventbus.Event) error {
			delivered <- e

			return nil
		}))
		// Written the way a module writes it — inside the outbox's own
		// statement — and then abandoned. Nobody publishes it; that is the
		// whole point.
		require.NoError(t, outbox.Write(ctx, pool.Pool(), event))
	}

	_, stopJobs, err := startJobs(ctx, app.container, app.host, cfg, log)
	require.NoError(t, err, "the composition root could not start the jobs it declares")

	store := jobpg.New(pool)
	names := []string{sagawatch.Name, paymentrecon.Name, outboxrelay.Name}

	// The wait is a loop rather than require.Eventually because the failure
	// message has to carry the ledger AS IT ENDED UP: Eventually formats its
	// message before it starts waiting, so the one report worth having — which
	// jobs did run — would always print an empty map.
	history := waitForPass(ctx, t, store, names)

	stopJobs()

	collected := make([]eventbus.Event, 0, len(events))
	for range events {
		select {
		case event := <-delivered:
			collected = append(collected, event)
		case <-time.After(10 * time.Second):
			t.Fatal("the outbox row was marked but the event never reached a subscriber")
		}
	}

	return scheduled{logs: logs.String(), delivered: collected, history: history}
}

// waitForPass returns the job ledger once every named job has a finished run.
//
// A pass is not observable from the outside in any other way: [job.Runner.Start]
// launches a goroutine and returns, so the only thing that says the scheduler
// did anything is the row it wrote.
func waitForPass(
	ctx context.Context, t *testing.T, store *jobpg.Store, names []string,
) map[string]job.Run {
	t.Helper()

	deadline := time.Now().Add(60 * time.Second)
	for {
		history, err := store.Last(ctx, names)
		require.NoError(t, err)

		finished := true
		for _, name := range names {
			if history[name].EndedAt.IsZero() {
				finished = false
			}
		}
		if finished {
			return history
		}

		require.False(t, time.Now().After(deadline),
			"the started runner did not finish a pass over every job of %v; the ledger "+
				"holds %v", names, history)
		time.Sleep(100 * time.Millisecond)
	}
}

// deliveryState reads one outbox row after the installation that relayed it has
// been closed.
//
// It goes to the DATABASE rather than to the runner's own report: what has to
// be proved is that the row changed, and a relay that reported a delivery it
// never recorded would satisfy an assertion on its own output.
func deliveryState(t *testing.T, dsn, id string) outboxRow {
	t.Helper()

	pool, err := db.New(context.Background(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	return readOutboxRow(t, pool, id)
}

// TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation is the boot
// failure nothing else in the repository can see.
//
// [registerJobs] does not take its dependencies, it RESOLVES them, and two of
// the three are resolved through an interface declared in this package
// ([paymentReconciler]) against a service registered by another. That
// assertion happens at run time. So the payment service can change the shape of
// Reconcile, or the module can stop registering under its own name, and every
// unit test in the repository still compiles and passes — the jobs tests build
// their own fakes, and internal/arch reads the SOURCE and finds the
// registration line right where it expects it. The binary would fail to start.
//
// This is the test that runs the resolution against a container the composition
// root really filled.
func TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	jobsEnv(t, dsn)

	cfg, err := config.Load()
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)
	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{})
	require.NoError(t, err)
	defer closeApp()

	registry, err := registerJobs(app.container, app.host, log)
	require.NoError(t, err,
		"a job whose dependency cannot be resolved fails the whole boot; this is the "+
			"error an operator would see instead of a running server")

	for _, name := range []string{sagawatch.Name, paymentrecon.Name, outboxrelay.Name} {
		definition, getErr := registry.Get(name)
		require.NoError(t, getErr,
			"%q is missing from the registry the runner and `gobit jobs` both read; "+
				"the listing would then show a page with nothing missing from it", name)
		assert.NotNil(t, definition.Run, "%q was registered with no work to do", name)
	}
}

// TestTheStartedRunnerDeliversAnEventNobodyPublished is the transactional
// outbox's second half, driven from the composition root that owns it.
//
// A module commits an order and writes the event in the same transaction; the
// process then dies before it can publish. The row is the promise, and this
// runner is the only thing that keeps it. Every piece of that path has unit
// tests with fakes — the relay against a fake store, the store against a real
// table — and none of them can see the wiring: the relay is handed a store
// built over THIS pool and a bus resolved from THIS container by
// [registerJobs], and the runner that calls it at all is started by
// [startJobs]. Both were at zero coverage, so an installation whose relay was
// never scheduled would look exactly like one whose subscribers are slow.
func TestTheStartedRunnerDeliversAnEventNobodyPublished(t *testing.T) {
	dsn := migrateDSN(t)

	const id = "evt_outboxrelay_integration_1"
	pass := relayedPass(t, dsn, eventbus.Event{
		ID:   id,
		Name: "order.placed",
		Data: map[string]any{"order_id": "order_1", "to": "shopper@example.com"},
	})

	require.Len(t, pass.delivered, 1)
	assert.Equal(t, id, pass.delivered[0].ID,
		"the event that reached the bus has to be the one the transaction wrote, "+
			"identity and all; a subscriber matching on the id would otherwise "+
			"process a different event than the one that was promised")
	assert.Equal(t, "order_1", pass.delivered[0].Data["order_id"],
		"the payload has to survive the round trip through the table")

	row := deliveryState(t, dsn, id)
	require.True(t, row.exists, "the outbox row the module wrote is gone")
	assert.True(t, row.published,
		"the row is still unpublished, so the next pass would send the event a second "+
			"time; only the database can witness that the delivery was recorded")
	assert.False(t, row.dead, "a delivery that succeeded must not be given up on")
	assert.Equal(t, int64(0), row.attempts,
		"a first-time delivery must not be recorded as a retry")

	relay := pass.history[outboxrelay.Name]
	require.True(t, relay.Succeeded(), "the relay's pass failed: %s", relay.Failure)
	assert.Equal(t, "published 1, failed 0", relay.Detail,
		"the pass's own numbers have to reach the ledger; a relay that reports nothing "+
			"is one an operator can only judge by whether it broke")
}

// TestThePassOfEveryScheduledJobIsRecordedWhereTheOperatorLooks is the other
// half of starting a runner: the jobs that only READ still have to leave a
// trace.
//
// saga-watch and payment-reconcile change nothing an operator can observe, so
// their entire visible output is the row this pass writes. A runner that
// started, ticked and skipped them would be indistinguishable from a healthy
// one until somebody asked why no abandoned cart had ever been reported — which
// is the question the reconciliation job was written for in the first place
// (ADR 0019).
func TestThePassOfEveryScheduledJobIsRecordedWhereTheOperatorLooks(t *testing.T) {
	dsn := migrateDSN(t)

	pass := relayedPass(t, dsn, eventbus.Event{
		ID: "evt_outboxrelay_integration_2", Name: "order.placed",
		Data: map[string]any{"order_id": "order_2"},
	})

	for _, name := range []string{sagawatch.Name, paymentrecon.Name} {
		run := pass.history[name]
		require.True(t, run.Succeeded(),
			"%q did not finish a successful pass against a real installation: %s",
			name, run.Failure)
		assert.NotEmpty(t, run.Detail,
			"%q reported nothing at all; a quiet pass and a job that never ran are "+
				"then the same blank cell in `gobit jobs`", name)
	}
}

// TestARelayThatCannotFinishBeforeShutdownIsAnnouncedAtStartup is the warning
// on the REAL numbers, and it fires today.
//
// The relay's budget is 45 seconds and the default shutdown window is 15, so
// every installation that has not tuned either one can be stopped in the middle
// of a pass. The warning is what connects a shutdown that took the full timeout
// to a run with no end recorded, and the unit tests around it are built from
// hand-made definitions and a hand-made config — they would go on passing if
// [startJobs] stopped calling the check, or called it with a config nobody
// loaded.
func TestARelayThatCannotFinishBeforeShutdownIsAnnouncedAtStartup(t *testing.T) {
	dsn := migrateDSN(t)

	pass := relayedPass(t, dsn, eventbus.Event{
		ID: "evt_outboxrelay_integration_3", Name: "order.placed",
		Data: map[string]any{"order_id": "order_3"},
	})

	assert.Contains(t, pass.logs, "a job may still be running when the process is asked to stop",
		"the check did not run at startup against the definitions the root registered")
	assert.Contains(t, pass.logs, outboxrelay.Name,
		"the warning has to name the job whose budget does not fit")
}

// TestTheJobsListingReadsTheLedgerTheRunnerWrote closes the loop the two
// commands share.
//
// `gobit jobs` builds its registry from [registerJobs] — the same function the
// runner uses — and then reads the history the runner wrote. That the two agree
// is the command's entire reason for existing: an operator asking "did it run
// last night?" during an incident is asking about the RUNNER's work, and a
// listing assembled from a thinner container would quietly describe a different
// set of jobs than the one that actually runs.
func TestTheJobsListingReadsTheLedgerTheRunnerWrote(t *testing.T) {
	dsn := migrateDSN(t)

	relayedPass(t, dsn, eventbus.Event{
		ID: "evt_outboxrelay_integration_4", Name: "order.placed",
		Data: map[string]any{"order_id": "order_4"},
	})

	var out bytes.Buffer
	require.NoError(t, Main([]string{jobsCommand}, &out, Options{}))

	listing := out.String()
	for _, name := range []string{sagawatch.Name, paymentrecon.Name, outboxrelay.Name} {
		assert.Contains(t, listing, name, "the listing lost a job the runner runs")
	}
	assert.NotContains(t, listing, "never",
		"every job has just run, so a row saying it never has means the command is "+
			"reading a different ledger than the runner writes")
	assert.Contains(t, listing, "published 1, failed 0",
		"the relay's own report of the pass has to reach the operator's page")
	assert.NotContains(t, listing, "FAILED", "listing:\n%s", listing)
}
