package app

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
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

// TestASuccessfulRunsDetailIsPrinted is the far end of the channel a
// successful run now has.
//
// The DETAIL column existed from the start and the runner could only fill it
// from an ERROR, so every row an operator ever saw with something in this cell
// was a row that had failed. A relay keeping up, a reconciliation that examined
// twelve sessions and a job that has never run all rendered the same blank —
// which is why the outbox relay had to fail in order to be seen.
func TestASuccessfulRunsDetailIsPrinted(t *testing.T) {
	out := render(t, []job.Definition{hourly("outbox-relay")}, map[string]job.Run{
		"outbox-relay": {
			Name:      "outbox-relay",
			Due:       listingFixture.Add(-30 * time.Minute),
			StartedAt: listingFixture.Add(-30 * time.Minute),
			EndedAt:   listingFixture.Add(-29 * time.Minute),
			Detail:    "published 12, failed 0",
		},
	})

	assert.Contains(t, out, "ok", "the run succeeded and the outcome column has to say so")
	assert.Contains(t, out, "published 12, failed 0",
		"a successful run's detail has to reach the listing; if it does not, the only "+
			"way a job can report a number is still to fail")
	assert.NotContains(t, out, "FAILED")
}

// TestTheDetailColumnIsBlankWhenNothingWasReported keeps the cell honest for a
// job that says nothing.
//
// Not every job has a number worth a column, and inventing a placeholder would
// make the listing's most-read row noisier for no fact.
func TestTheDetailColumnIsBlankWhenNothingWasReported(t *testing.T) {
	out := render(t, []job.Definition{hourly("saga-watch")}, map[string]job.Run{
		"saga-watch": {
			Name:      "saga-watch",
			Due:       listingFixture.Add(-30 * time.Minute),
			StartedAt: listingFixture.Add(-30 * time.Minute),
			EndedAt:   listingFixture.Add(-29 * time.Minute),
		},
	})

	require.Len(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), 2)
	assert.Equal(t, "ok", strings.TrimSpace(
		strings.TrimPrefix(strings.Split(out, "\n")[1], "saga-watch  1h0m0s  "+
			listingFixture.Add(-29*time.Minute).Format(time.RFC3339)+"  ")))
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

// jobPlugin is a plugin whose whole purpose is to declare the jobs it is given.
type jobPlugin struct {
	name string
	jobs []coreplugin.Job
}

// Name returns the plugin's name.
func (p jobPlugin) Name() string { return p.name }

// Setup declares the jobs through the host.
func (p jobPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	for _, j := range p.jobs {
		h.RegisterJob(j)
	}

	return nil
}

// installed runs the plugins' Setup and returns the host they declared against.
//
// The real [coreplugin.Registry] is used rather than a hand-built host: the
// plugin name that ends up on every job is set by Install, and a host filled in
// by hand would prove the admission works while leaving the label — the only
// thing that tells an operator WHICH plugin to fix — untested.
func installed(t *testing.T, plugins ...coreplugin.Plugin) *coreplugin.Host {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	registry := coreplugin.NewRegistry(log)
	for _, p := range plugins {
		registry.Add(p)
	}

	host := coreplugin.NewHost(c, nil, nil, log, nil)
	require.NoError(t, registry.Install(t.Context(), host))

	return host
}

// TestAPluginJobReachesTheRegistry is the end of the path B13 opened: declared
// in a plugin's Setup, admitted by the composition root, and present in the
// registry the runner and `gobit jobs` both read.
func TestAPluginJobReachesTheRegistry(t *testing.T) {
	t.Parallel()

	ran := false
	host := installed(t, jobPlugin{name: "paytr", jobs: []coreplugin.Job{{
		Name:   "paytr-pending-watch",
		Every:  time.Hour,
		MaxRun: time.Minute,
		Run:    func(context.Context) error { ran = true; return nil },
	}}})

	registry := job.NewRegistry()
	require.NoError(t, addPluginJobs(registry, host))

	definition, err := registry.Get("paytr-pending-watch")
	require.NoError(t, err)
	assert.Equal(t, time.Hour, definition.Every)
	assert.Equal(t, time.Minute, definition.MaxRun)

	require.NotNil(t, definition.Run, "the work must survive the translation, not just its schedule")
	require.NoError(t, definition.Run(t.Context()))
	assert.True(t, ran, "the registry has to hold the plugin's OWN function")
}

// TestAPluginJobIsJudgedByTheSameScheduleRule is the assertion that matters
// most in production.
//
// A MaxRun longer than the interval is refused at boot — a smoke test once
// caught it by every test in the suite failing at once, because the binary
// would not start. The rule lives in exactly one place and this proves a plugin
// goes through THAT one, by comparing the error code with the one an in-repo
// definition with the identical fault produces.
func TestAPluginJobIsJudgedByTheSameScheduleRule(t *testing.T) {
	t.Parallel()

	host := installed(t, jobPlugin{name: "greedy", jobs: []coreplugin.Job{{
		Name:   "greedy-pass",
		Every:  time.Minute,
		MaxRun: time.Hour,
		Run:    func(context.Context) error { return nil },
	}}})

	registry := job.NewRegistry()
	err := addPluginJobs(registry, host)
	require.Error(t, err, "a job that can never catch up must not reach the runner")

	inRepo := job.NewRegistry().Add(job.Definition{
		Name:   "greedy-pass",
		Every:  time.Minute,
		MaxRun: time.Hour,
		Run:    func(context.Context) error { return nil },
	})
	require.Error(t, inRepo)
	assert.Equal(t, coreerrors.CodeOf(inRepo), coreerrors.CodeOf(err),
		"a plugin job and an in-repo job must be refused by the SAME rule, not by two")

	assert.Contains(t, err.Error(), "greedy", "the error has to name the plugin to fix")
	assert.Zero(t, registry.Len(), "a refused job must not be half-admitted")
}

// TestAPluginJobWithNoWorkIsRefusedRatherThanSkipped closes the silent path.
//
// Skipping it would produce the defect the listing exists to prevent: `gobit
// jobs` prints a page with nothing missing from it, and the absence of the
// plugin's line reads as "that pass had nothing to do" rather than "it was
// never admitted".
func TestAPluginJobWithNoWorkIsRefusedRatherThanSkipped(t *testing.T) {
	t.Parallel()

	host := installed(t, jobPlugin{name: "hollow", jobs: []coreplugin.Job{{
		Name:   "hollow-pass",
		Every:  time.Hour,
		MaxRun: time.Minute,
	}}})

	registry := job.NewRegistry()
	err := addPluginJobs(registry, host)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hollow")
	assert.Zero(t, registry.Len())
}

// TestAPluginCannotShadowACoreJob proves the namespace is shared and the clash
// is refused.
//
// Two jobs under one name share an advisory lock and a history row, so one of
// them silently never runs — and which one would depend on registration order.
// The plugins are admitted LAST so that the one blamed is the one an operator
// can actually change.
func TestAPluginCannotShadowACoreJob(t *testing.T) {
	t.Parallel()

	host := installed(t, jobPlugin{name: "impostor", jobs: []coreplugin.Job{{
		Name:   "saga-watch",
		Every:  time.Hour,
		MaxRun: time.Minute,
		Run:    func(context.Context) error { return nil },
	}}})

	// The core's job goes in first, exactly as registerJobs orders them.
	core := hourly("saga-watch")
	core.Run = func(context.Context) error { return nil }

	registry := job.NewRegistry()
	require.NoError(t, registry.Add(core))

	err := addPluginJobs(registry, host)
	require.Error(t, err)
	assert.Equal(t, job.CodeDuplicate, coreerrors.CodeOf(err),
		"the clash must stay a duplicate-name error rather than being flattened into "+
			"an invalid definition; the two send the reader to different places")
	assert.Contains(t, err.Error(), "impostor")
	assert.Equal(t, 1, registry.Len(), "the core's job must be the one still standing")
}

// TestEveryJobDefinitionFieldReachesAPluginJob is the price of not publishing
// the scheduler.
//
// [coreplugin.Job] repeats the four fields a plugin needs instead of importing
// internal/core/job, which a published package may not do. The repetition's
// failure mode is silent: the scheduler grows a field, the translation in
// [addPluginJobs] does not, and every plugin job quietly runs with that field's
// zero value while the in-repo jobs use it. This fails the day that happens.
func TestEveryJobDefinitionFieldReachesAPluginJob(t *testing.T) {
	t.Parallel()

	definition := reflect.TypeFor[job.Definition]()
	published := reflect.TypeFor[coreplugin.Job]()

	carried := 0
	for i := range definition.NumField() {
		field := definition.Field(i)
		if !field.IsExported() {
			continue
		}

		mirrored, found := published.FieldByName(field.Name)
		require.True(t, found,
			"job.Definition has an exported field %q that coreplugin.Job does not carry.\n"+
				"A plugin cannot set it, so every plugin job runs with its zero value while "+
				"the in-repo jobs use it — and nothing else in the build would say so.",
			field.Name)
		require.True(t, mirrored.Type.ConvertibleTo(field.Type),
			"coreplugin.Job.%s is %s and job.Definition.%s is %s; addPluginJobs cannot "+
				"carry one into the other", field.Name, mirrored.Type, field.Name, field.Type)
		carried++
	}

	require.Positive(t, carried,
		"no exported field was compared, so this audit proved nothing; job.Definition "+
			"emptying out would leave it green having checked nothing")
}

// TestNoPluginsIsNotAnError keeps the common installation quiet.
//
// Most installations run no plugin at all, and the migrate subcommand builds a
// host it throws away. Neither is a reason to fail a boot.
func TestNoPluginsIsNotAnError(t *testing.T) {
	t.Parallel()

	registry := job.NewRegistry()
	require.NoError(t, addPluginJobs(registry, nil))
	require.NoError(t, addPluginJobs(registry, installed(t)))
	assert.Zero(t, registry.Len())
}

// bounded is a definition the registry accepts, with the two durations the
// shutdown check compares.
func bounded(name string, every, maxRun time.Duration) job.Definition {
	return job.Definition{
		Name:   name,
		Every:  every,
		MaxRun: maxRun,
		Run:    func(context.Context) error { return nil },
	}
}

// startupWarnings runs the startup check and returns what it wrote.
func startupWarnings(t *testing.T, shutdown time.Duration, defs ...job.Definition) string {
	t.Helper()

	registry := job.NewRegistry()
	for _, d := range defs {
		require.NoError(t, registry.Add(d))
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	warnIfAJobOutlivesShutdown(t.Context(), registry,
		config.Config{ShutdownTimeout: shutdown}, log)

	return buf.String()
}

// TestAJobThatCanOutlastTheShutdownWindowIsNamedBeforeItHappens connects two
// facts an operator would otherwise see separately and never join.
//
// The day it happens they get a shutdown that takes the whole timeout and, in
// `gobit jobs`, a run with a start and no end. Nothing in either sentence points
// at the other, and the natural reading of the second — "the process died
// mid-pass, something is wrong" — is exactly wrong: the pass was cut off on
// purpose because its own budget is longer than the window the process is given
// to leave. The warning is the only place the two numbers appear together, and
// it is printed at STARTUP, minutes or weeks before the shutdown it explains,
// because during the shutdown nobody is reading.
func TestAJobThatCanOutlastTheShutdownWindowIsNamedBeforeItHappens(t *testing.T) {
	t.Parallel()

	out := startupWarnings(t, 15*time.Second,
		bounded("outbox-relay", time.Minute, 45*time.Second))

	assert.Contains(t, out, "outbox-relay",
		"the warning has to name WHICH job, or an installation with several is told "+
			"only that one of them is a problem")
	assert.Contains(t, out, "45s", "the job's own budget is half of the comparison")
	assert.Contains(t, out, "15s", "and the window it does not fit into is the other half")
}

// TestAJobThatFitsInTheShutdownWindowIsNotWarnedAbout keeps the line worth
// reading.
//
// This runs once per boot, in the first screen of a start-up log, next to the
// lines an operator reads when a deploy misbehaves. A warning printed for every
// job would be skimmed past within a week, and the one job that genuinely
// cannot finish would be skimmed past with it.
//
// The boundary is the interesting half: a job whose budget is EXACTLY the
// shutdown window ends as the window ends, so it cannot outlive it. Warning
// there would put a line into the log of every installation that tuned the two
// numbers to match — the most deliberate configuration there is.
func TestAJobThatFitsInTheShutdownWindowIsNotWarnedAbout(t *testing.T) {
	t.Parallel()

	assert.Empty(t, startupWarnings(t, 15*time.Second,
		bounded("saga-watch", time.Hour, 10*time.Second)),
		"a job that finishes well inside the window is not news")

	assert.Empty(t, startupWarnings(t, 15*time.Second,
		bounded("saga-watch", time.Hour, 15*time.Second)),
		"a job whose budget is exactly the shutdown window ends as the window ends; "+
			"there is nothing to warn about, and the warning would land on precisely "+
			"the installations that matched the two numbers on purpose")
}

// TestOnlyTheJobsThatDoNotFitAreNamed stops the warning from becoming a listing.
//
// A registry holds several jobs and typically ONE of them has a long pass. The
// operator's next step is to shorten that job's budget or lengthen the window,
// and both need the name; a warning that swept in the well-behaved jobs would
// send them looking at the wrong three.
func TestOnlyTheJobsThatDoNotFitAreNamed(t *testing.T) {
	t.Parallel()

	out := startupWarnings(t, 30*time.Second,
		bounded("saga-watch", time.Hour, 10*time.Second),
		bounded("outbox-relay", time.Minute, 45*time.Second),
		bounded("payment-reconcile", time.Hour, 20*time.Second))

	assert.Contains(t, out, "outbox-relay")
	assert.NotContains(t, out, "saga-watch",
		"a job that fits must not appear; the operator would go and shorten a budget "+
			"that was never the problem")
	assert.NotContains(t, out, "payment-reconcile")
	require.Len(t, strings.Split(strings.TrimRight(out, "\n"), "\n"), 1,
		"one line per job that does not fit, and nothing else:\n%s", out)
}
