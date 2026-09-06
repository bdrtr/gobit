package paymentpaytr

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
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
