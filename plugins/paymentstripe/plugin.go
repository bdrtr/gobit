// Package paymentstripe is an example plugin that adds a Stripe payment
// provider to gobit.
//
// # This is a SKELETON
//
// Registration, configuration and the life cycle work IN FULL; the calls to
// Stripe's HTTP API are NOT MADE. Every method that would move money returns
// an explicit "not implemented" error.
//
// That is deliberate. A capture method that returned a fake "success" would,
// if the skeleton reached production by accident, show orders as paid — that
// is, ship goods without ever taking a payment. A loud error is always cheaper
// than a silent lie.
//
// # What the plugin shows
//
// This package imports NO commerce module. It takes the provider contract from
// the core's [coreprovider] package and its point of registration from
// [coreplugin.Host]. So the payment module's code does not know this plugin
// exists, and adding a plugin does NOT CHANGE the core: one line is added to
// the installation's setup.
//
// # Usage
//
//	plugins.Add(paymentstripe.New())
//
// and STRIPE_API_KEY has to be set in the environment.
package paymentstripe

import (
	"context"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// Name is the plugin's name in the registry.
const Name = "payment-stripe"

// ProviderID is the provider's identity.
//
// This value is WRITTEN to the database with the payment sessions; changing it
// makes the old records unresolvable. It has to stay the same across releases.
const ProviderID = "stripe"

// apiKeySetting is the setting NAME of the Stripe secret key — not the key
// itself. The value is read only from the environment and written nowhere.
const apiKeySetting = "STRIPE_API_KEY" //nolint:gosec // G101: an environment variable name, not an embedded credential

// livePrefix is the prefix of live (non-test) Stripe keys.
const livePrefix = "sk_live_"

// Error codes.
const (
	codeMissingKey     = "stripe_api_key_missing"
	codeNotImplemented = "stripe_not_implemented"
)

// Plugin is the Stripe plugin.
type Plugin struct{}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the configuration and registers the provider.
//
// Without STRIPE_API_KEY the setup returns an ERROR. Skipping silently would
// mean a shop believed to "have Stripe" taking no payments, and that being seen
// only at the first customer's attempt; a configuration error has to blow up at
// startup.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	key, ok := h.Setting(apiKeySetting)
	if !ok {
		return coreerrors.Invalid(codeMissingKey,
			"the %s plugin cannot be set up without the %s setting", Name, apiKeySetting)
	}

	// Not the key ITSELF, only whether it is live, is logged.
	h.Logger().Info("registering the stripe provider",
		"provider_id", ProviderID,
		"live_key", strings.HasPrefix(key, livePrefix))

	h.RegisterPaymentProvider(&stripeProvider{apiKey: key})

	return nil
}

// stripeProvider is Stripe's [coreprovider.PaymentProvider] implementation.
type stripeProvider struct {
	// apiKey is the Stripe secret key. It is NEVER logged and never put in an
	// error message; leaked, it hands over the shop's whole payment history and
	// the power to refund.
	apiKey string
}

// ID returns the provider's identity.
func (s *stripeProvider) ID() string { return ProviderID }

// CreateSession will open a PaymentIntent at Stripe.
//
// Skeleton: the real call is not made.
func (s *stripeProvider) CreateSession(
	_ context.Context, _ coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return coreprovider.Session{}, s.notImplemented("CreateSession")
}

// Authorize will place a hold on the amount at Stripe.
//
// Skeleton: the real call is not made.
func (s *stripeProvider) Authorize(
	_ context.Context, _ string,
) (coreprovider.AuthResult, error) {
	return coreprovider.AuthResult{}, s.notImplemented("Authorize")
}

// Capture will collect an amount on hold.
//
// Skeleton: the real call is not made.
func (s *stripeProvider) Capture(_ context.Context, _ string, _ int64) error {
	return s.notImplemented("Capture")
}

// Refund will give back a collected amount.
//
// Skeleton: the real call is not made.
func (s *stripeProvider) Refund(_ context.Context, _ string, _ int64) error {
	return s.notImplemented("Refund")
}

// Cancel will cancel a session that was authorized and not captured.
//
// Skeleton: the real call is not made. This is the saga's compensation; a real
// implementation has to be IDEMPOTENT, answering success, NOT an error, for a
// session already canceled. Otherwise a compensation that is retried fails
// forever.
func (s *stripeProvider) Cancel(_ context.Context, _ string) error {
	return s.notImplemented("Cancel")
}

// notImplemented builds the error for a method the skeleton does not
// implement.
//
// [coreerrors.KindUnavailable] is chosen: this is not a client error (4xx) but
// a capability missing on the server's side, and it is reported as 503.
func (s *stripeProvider) notImplemented(method string) error {
	return coreerrors.Unavailable(codeNotImplemented,
		"the %s provider's %s method is not implemented in this skeleton", ProviderID, method)
}
