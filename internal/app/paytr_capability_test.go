package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/plugins/paymentpaytr"
)

// capturedProviders stands where the payment module's provider registry would
// and keeps what the plugins register.
type capturedProviders struct {
	registered []coreprovider.PaymentProvider
}

// Register keeps the provider.
func (c *capturedProviders) Register(p coreprovider.PaymentProvider) error {
	c.registered = append(c.registered, p)

	return nil
}

// TestPayTRSaysItsCustomerAuthorizes pins the internal capability the payment
// module asserts for on the provider the PayTR plugin really registers
// (ADR 0438).
//
// The capability is found by a type assertion against an interface the plugin
// cannot import, so a renamed or re-typed method would break nothing at
// compile time: the checkout would open PayTR's session inside the completion
// again, and every PayTR order would fail as it did before the decision. The
// composition root is the one package that can import both, and it asks
// through the module's own function rather than a copy of the interface.
func TestPayTRSaysItsCustomerAuthorizes(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	providers := &capturedProviders{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, providers))
	require.NoError(t, c.Provide(coreplugin.CallbacksName,
		corehttp.NewCallbackRegistry(corehttp.CallbackOptions{Logger: log})))

	host := coreplugin.NewHost(c, module.NewRegistry(log, nil), nil, log, map[string]string{
		"PAYTR_MERCHANT_ID":   "123456",
		"PAYTR_MERCHANT_KEY":  "KEY",
		"PAYTR_MERCHANT_SALT": "SALT",
		"PAYTR_SUCCESS_URL":   "https://shop.example.test/done",
		"PAYTR_FAILURE_URL":   "https://shop.example.test/failed",
	})
	plugins := coreplugin.NewRegistry(log)
	plugins.Add(paymentpaytr.New())
	require.NoError(t, plugins.Install(t.Context(), host))
	require.NoError(t, plugins.Start(t.Context(), host))

	require.Len(t, providers.registered, 1, "the PayTR plugin registers one payment provider")
	assert.Equal(t, paymentpaytr.ProviderID, providers.registered[0].ID())
	assert.True(t, paymentsvc.ProviderCustomerAuthorizes(providers.registered[0]),
		"the payment module no longer finds PayTR's CustomerAuthorizes")
}
