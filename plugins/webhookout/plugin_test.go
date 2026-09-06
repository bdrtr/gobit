package webhookout_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/webhookout"
)

// This file tests the plugin from OUTSIDE, through the surface the core sees:
// what it registers, under which names, and what it does not need in order to
// install.
//
// A plugin may import no module (internal/arch's TestPluginsDoNotImportModules)
// and the ban covers test files, so there is no real order module here. Nothing
// needs one: what is asserted is the registration, and the core sees exactly
// this much of it too.

// recordingBus is an event bus that records what was subscribed to.
type recordingBus struct {
	mu         sync.Mutex
	subscribed []string
}

var _ eventbus.EventBus = (*recordingBus)(nil)

// Publish does nothing; this plugin publishes nothing.
func (b *recordingBus) Publish(context.Context, eventbus.Event) error { return nil }

// Subscribe records the topic.
func (b *recordingBus) Subscribe(eventName string, _ eventbus.Handler) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.subscribed = append(b.subscribed, eventName)

	return nil
}

// Shutdown does nothing.
func (b *recordingBus) Shutdown(context.Context) error { return nil }

// setUp installs and starts the plugin over a fresh container.
func setUp(t *testing.T) (*module.Registry, *coreplugin.Host, *recordingBus, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	modules := module.NewRegistry(log, nil)
	bus := &recordingBus{}

	registry := coreplugin.NewRegistry(log)
	registry.Add(webhookout.New())

	host := coreplugin.NewHost(container.New(log), modules, bus, log, nil)
	if err := registry.Install(t.Context(), host); err != nil {
		return modules, host, bus, err
	}

	return modules, host, bus, registry.Start(t.Context(), host)
}

// TestThePluginInstallsWithNoSetting proves the plugin needs no environment.
//
// It is worth an assertion because every other plugin in the tree refuses to
// start without one, and the difference is a decision rather than an omission:
// a webhook's URL, topics and secret belong to a RECEIVER, and a receiver
// configured in the environment could not be added without a deploy. The host
// here is built with a nil settings map, which is what an installation that
// sets nothing looks like.
func TestThePluginInstallsWithNoSetting(t *testing.T) {
	t.Parallel()

	_, _, _, err := setUp(t)
	require.NoError(t, err,
		"the plugin must install with no setting at all; it reads none")
}

// TestThePluginBringsItsModule checks the module reaches the registry.
func TestThePluginBringsItsModule(t *testing.T) {
	t.Parallel()

	modules, _, _, err := setUp(t)
	require.NoError(t, err)

	var names []string
	for _, m := range modules.Modules() {
		names = append(names, m.Name())
	}

	assert.Contains(t, names, webhookout.ModuleName,
		"the module the plugin brings has to go through the registry: without it there is "+
			"no migration, no table and no admin surface")
}

// TestEveryForwardedTopicIsActuallySubscribedTo is the half the source scan
// cannot see.
//
// TestTheForwardedTopicsAreEveryPublishedTopic compares a LIST against the
// repository. This compares the list against what the plugin actually did with
// it: a topic added to ForwardedTopics without the matching Subscribe call
// would pass that gate, be accepted by validateTopics, be registered by an
// integrator, and never be delivered — a receiver that is set up correctly and
// hears nothing.
func TestEveryForwardedTopicIsActuallySubscribedTo(t *testing.T) {
	t.Parallel()

	_, _, bus, err := setUp(t)
	require.NoError(t, err)

	for _, topic := range webhookout.ForwardedTopics {
		assert.Contains(t, bus.subscribed, topic,
			"%q is on ForwardedTopics and the plugin never subscribed to it.\n"+
				"validateTopics will accept a receiver for it, the receiver will look "+
				"correctly registered, and it will be delivered NOTHING.", topic)
	}

	assert.Len(t, bus.subscribed, len(webhookout.ForwardedTopics),
		"the plugin subscribed to something that is not on ForwardedTopics: %v.\n"+
			"A subscription outside the list is delivered to receivers that could not "+
			"have asked for it, because validateTopics refuses the name.", bus.subscribed)
}

// TestThePluginRegistersItsDeliveryJob is the assertion that the sender exists.
//
// Without the job the plugin is a queue with no drain: the subscriber writes
// delivery rows, every one of them stays pending forever, and nothing anywhere
// reports it — `gobit jobs` would list the jobs there are and show nothing
// missing, which an operator reads as "that pass had nothing to do".
func TestThePluginRegistersItsDeliveryJob(t *testing.T) {
	t.Parallel()

	_, host, _, err := setUp(t)
	require.NoError(t, err)

	jobs := host.Jobs()
	require.Len(t, jobs, 1, "the plugin registers exactly one job")

	job := jobs[0]
	assert.Equal(t, webhookout.Name, job.PluginName())
	assert.True(t, strings.HasPrefix(job.Name, webhookout.ModuleName),
		"the job name shares one namespace with the core's jobs and every other "+
			"plugin's; it has to carry this plugin's own prefix, got %q", job.Name)
	assert.NotNil(t, job.Run, "a job with no body is refused at boot, loudly, but it is "+
		"cheaper to find it here")
	assert.Positive(t, job.Every, "an interval that is not positive is refused at boot")
	assert.LessOrEqual(t, job.MaxRun, job.Every,
		"a run that can outlast its own interval is due again before it finished and "+
			"can never catch up; the scheduler refuses it at boot")
}
