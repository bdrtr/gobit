package paymentstripe_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// fakeRegistry stands in for the payment module's provider registry.
type fakeRegistry struct {
	registered []coreprovider.PaymentProvider
}

// Register takes the provider into the list.
func (k *fakeRegistry) Register(p coreprovider.PaymentProvider) error {
	k.registered = append(k.registered, p)

	return nil
}

// install sets the plugin up with the given settings and takes it as far as
// Start.
func install(t *testing.T, settings map[string]string) (*fakeRegistry, error) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	registry := &fakeRegistry{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, registry))

	reg := coreplugin.NewRegistry(log)
	reg.Add(paymentstripe.New())

	h := coreplugin.NewHost(c, nil, nil, log, settings)
	if err := reg.Install(t.Context(), h); err != nil {
		return registry, err
	}

	return registry, reg.Start(t.Context(), h)
}

// TestThePluginRegistersTheProvider proves the plugin plugs in without
// touching the core and that the provider is SELECTABLE by its identity (phase
// 9 definition of done).
func TestThePluginRegistersTheProvider(t *testing.T) {
	t.Parallel()

	registry, err := install(t, map[string]string{"STRIPE_API_KEY": "sk_test_1"})
	require.NoError(t, err)

	require.Len(t, registry.registered, 1)
	assert.Equal(t, paymentstripe.ProviderID, registry.registered[0].ID())
}

// TestASetupWithoutAKeyIsRefused proves a missing configuration blows up AT
// STARTUP.
//
// Skipped silently, a shop believed to "have Stripe" would take no payments,
// and that would be seen only at the first customer's attempt.
func TestASetupWithoutAKeyIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]string{
		"no setting at all": nil,
		"an empty setting":  {"STRIPE_API_KEY": ""},
		"only whitespace":   {"STRIPE_API_KEY": "   "},
	}

	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			registry, err := install(t, settings)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "STRIPE_API_KEY")
			assert.Empty(t, registry.registered, "no provider may be registered with a missing configuration")
		})
	}
}

// TestTheMoneyMovingMethodsReturnNoFakeSuccess proves none of the skeleton's
// methods silently returns "success".
//
// The scenario this test guards: if the skeleton reached production by
// accident, a Capture returning a fake success would show orders as paid and
// the shop would ship goods without ever taking a payment. A loud error is
// cheaper than a silent lie.
func TestTheMoneyMovingMethodsReturnNoFakeSuccess(t *testing.T) {
	t.Parallel()

	registry, err := install(t, map[string]string{"STRIPE_API_KEY": "sk_test_1"})
	require.NoError(t, err)
	require.Len(t, registry.registered, 1)

	p := registry.registered[0]
	ctx := t.Context()

	t.Run("CreateSession", func(t *testing.T) {
		t.Parallel()

		_, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{})
		assert.Error(t, err)
	})

	t.Run("Authorize", func(t *testing.T) {
		t.Parallel()

		_, err := p.Authorize(ctx, "sess_1")
		assert.Error(t, err)
	})

	t.Run("Capture", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Capture(ctx, "sess_1", 1000))
	})

	t.Run("Refund", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Refund(ctx, "sess_1", 1000))
	})

	t.Run("Cancel", func(t *testing.T) {
		t.Parallel()

		assert.Error(t, p.Cancel(ctx, "sess_1"))
	})
}

// TestTheKeyDoesNotLeakIntoAnErrorMessage proves the secret key does not end
// up in an error's text.
func TestTheKeyDoesNotLeakIntoAnErrorMessage(t *testing.T) {
	t.Parallel()

	const secret = "sk_live_VERYSECRET123"

	registry, err := install(t, map[string]string{"STRIPE_API_KEY": secret})
	require.NoError(t, err)
	require.Len(t, registry.registered, 1)

	_, refused := registry.registered[0].Authorize(t.Context(), "sess_1")
	require.Error(t, refused)
	assert.NotContains(t, refused.Error(), secret, "the secret key must not leak into an error message")
}
