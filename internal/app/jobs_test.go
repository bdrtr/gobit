package app

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
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
