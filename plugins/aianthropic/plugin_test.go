package aianthropic_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/plugins/aianthropic"
)

// setup runs the plugin against a fresh container with the given settings.
func setup(t *testing.T, settings map[string]string) (*container.Container, error) {
	t.Helper()

	c := container.New(slog.New(slog.DiscardHandler))
	host := coreplugin.NewHost(c, nil, nil, slog.New(slog.DiscardHandler), settings)

	return c, aianthropic.New().Setup(context.Background(), host)
}

// TestTheSlotIsFilledWithSomethingThatSatisfiesTheContract is the tie between
// the plugin and the core.
//
// The container holds an `any`; what the composition root then does is a type
// assertion, and an assertion that fails at boot is the whole feature silently
// missing. This is the only place the two ends are checked against each other.
func TestTheSlotIsFilledWithSomethingThatSatisfiesTheContract(t *testing.T) {
	t.Parallel()

	c, err := setup(t, map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-test",
		"ANTHROPIC_MODEL":   "a-model-3",
	})
	require.NoError(t, err)

	classifier, err := container.Resolve[provider.Classifier](c, coreplugin.AIProviderName)
	require.NoError(t, err,
		"the plugin registered something that is not a %T, so the job that resolves it "+
			"would fail at boot", (*provider.Classifier)(nil))
	assert.Equal(t, aianthropic.ProviderID, classifier.ID())
}

// TestASettingThatIsMissingStopsTheBoot pins the refusal rather than a no-op.
//
// An installation that believes it has a model and does not is worse off than
// one that knows it has none: the reviews pile up with an empty column and
// nothing says why.
func TestASettingThatIsMissingStopsTheBoot(t *testing.T) {
	t.Parallel()

	for name, settings := range map[string]map[string]string{
		"no key at all":  {"ANTHROPIC_MODEL": "a-model-3"},
		"an empty key":   {"ANTHROPIC_API_KEY": "   ", "ANTHROPIC_MODEL": "a-model-3"},
		"no model":       {"ANTHROPIC_API_KEY": "sk-ant-test"},
		"an empty model": {"ANTHROPIC_API_KEY": "sk-ant-test", "ANTHROPIC_MODEL": " "},
		"nothing":        {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c, err := setup(t, settings)
			require.Error(t, err)
			assert.False(t, c.Has(coreplugin.AIProviderName),
				"the slot was filled by a plugin that refused to start")
		})
	}
}

// TestTheModelIsNotDefaulted is a decision worth a test of its own.
//
// A model name is a price and a capability. Defaulting it would mean an
// installation moving to a different model — and a different bill — on an
// upgrade of gobit rather than on a decision of theirs.
func TestTheModelIsNotDefaulted(t *testing.T) {
	t.Parallel()

	_, err := setup(t, map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_MODEL")
}
