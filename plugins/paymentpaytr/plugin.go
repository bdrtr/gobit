// Package paymentpaytr takes payments through PayTR.
//
// # It is a plugin that brings a MODULE, not only a provider
//
// PayTR is hosted checkout: the customer pays inside PayTR's own iframe and
// PayTR reports the outcome by posting back to us, once, at a moment of its
// choosing. That single fact decides the plugin's shape.
//
// The provider contract asks `Authorize(ctx, sessionID)` — "is the money held?"
// — and expects an answer from the session id alone. A gateway that can be
// DRIVEN answers by making an API call. PayTR cannot be driven, so the answer
// is whatever the callback said, and it has to survive a restart between the
// callback arriving and the question being asked. That is durable state, so the
// plugin owns a table and a route as well as the provider slot.
//
// It is the same finding ADR 0018 recorded for web push, arriving from the
// other direction: a provider slot alone cannot express a party that reports
// back rather than being asked.
//
// # What it does NOT do
//
// The callback records the outcome, reports a payment to the core through
// [coreplugin.Host.PaymentReports], and acknowledges. It does NOT complete the
// cart: the checkout does, from that report, because a customer who pays and
// then closes the browser never brings the storefront back to complete it
// (ADR 0438). Having a payment plugin reach into the order flow would put the
// flow in two places; the plugin says what PayTR said, and the checkout
// decides.
//
// A completion the report could not make is tried again by the checkout's
// sweep, and one it can never make — the cart changed or was completed with
// another payment — is refunded. A payment still held with no order is
// reported by the payment module's reconciliation, and is completed through
// the storefront's own POST /store/v1/carts/{id}/complete naming the session,
// or canceled with POST /admin/v1/payment-sessions/{id}/cancel, which here is
// the refund. [store.pending] lists the other class: payments PayTR never
// reported on at all.
//
// # The call order a storefront makes
//
// The customer pays inside PayTR's iframe, so the payment is opened BEFORE the
// cart is completed (ADR 0438):
//
//  1. POST /store/v1/carts/{id}/payment-sessions with payment_provider_id
//     "paytr", payment_data carrying the customer's email and user_ip, and the
//     expected_total; the answer's data.iframe_token opens the iframe.
//  2. The customer pays; PayTR posts the outcome to [CallbackPath].
//  3. On PAYTR_SUCCESS_URL, POST /store/v1/carts/{id}/complete naming the
//     answer's payment_session_id. Until PayTR's callback has arrived it is
//     refused with 409 checkout_workflow_payment_pending and Retry-After and
//     changes nothing, so it is sent again; once the callback has completed
//     the cart, it answers with that order. A shopper who never reaches the
//     page has the order all the same: the callback completed the cart.
//
// A completion naming paytr without a session is refused with 409
// checkout_workflow_payment_session_required, before anything is opened.
//
// # Installments
//
// PayTR's callback signs the customer's total, which carries the installment
// difference when the customer pays in installments. The session is authorized
// and captured for its own amount, the order's; the difference is the cost of
// the installments, not the shop's money, and it is kept on the payment row and
// the session's data (see [shopsPart]).
//
// # No new dependency
//
// The three signatures are HMAC-SHA256 over concatenated fields; net/http and
// crypto/hmac cover the whole integration. The same decision ADR 0014 made for
// the error reporters.
//
// # Usage
//
//	PLUGINS=payment-paytr
//	PAYTR_MERCHANT_ID=...
//	PAYTR_MERCHANT_KEY=...
//	PAYTR_MERCHANT_SALT=...
//	PAYTR_SUCCESS_URL=https://shop.example.test/checkout/done
//	PAYTR_FAILURE_URL=https://shop.example.test/checkout/failed
package paymentpaytr

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the registry; the PLUGINS list recognizes it.
const Name = "payment-paytr"

// ModuleName is the module's name, and it differs from the plugin's on purpose:
// a module name becomes the prefix of its migration ledger table, which the
// core validates against a strict identifier pattern that refuses a hyphen.
const ModuleName = "paytr"

// The setting names. The key and the salt are never written anywhere.
const (
	settingMerchantID = "PAYTR_MERCHANT_ID"
	settingKey        = "PAYTR_MERCHANT_KEY"
	settingSalt       = "PAYTR_MERCHANT_SALT"
	settingSuccessURL = "PAYTR_SUCCESS_URL"
	settingFailureURL = "PAYTR_FAILURE_URL"
	settingTestMode   = "PAYTR_TEST_MODE"
	settingBaseURL    = "PAYTR_BASE_URL"
)

// defaultBaseURL is PayTR's production address.
const defaultBaseURL = "https://www.paytr.com"

// Error codes.
const (
	codeMissingSetting = "paytr_setting_missing"
	codeInvalidSetting = "paytr_setting_invalid"
	codeSetupFailed    = "paytr_module_setup_failed"
)

// config is the plugin's validated configuration.
type config struct {
	MerchantID   string
	MerchantKey  string
	MerchantSalt string
	SuccessURL   string
	FailureURL   string
	// TestMode is "1" or "0" and goes into the signature, so it is kept as the
	// string PayTR expects rather than as a bool that has to be rendered twice.
	TestMode string
	BaseURL  string
}

// Plugin is the PayTR payment plugin.
type Plugin struct {
	mod *paytrModule
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the configuration and registers the module and the provider.
//
// Every fault stops startup. An installation that believes it takes payments
// and does not is the worst failure a commerce framework has: it is discovered
// by a customer, at the checkout, with a cart they have already filled.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	cfg, err := readConfig(h)
	if err != nil {
		return err
	}

	log := h.Logger()

	// The merchant id is logged; the key and the salt never are. Between them
	// they let anyone sign a callback saying a payment succeeded, which is the
	// one message this system trusts about money.
	log.Info("registering the paytr payment provider",
		"module", ModuleName,
		"provider_id", ProviderID,
		"merchant_id", cfg.MerchantID,
		"test_mode", cfg.TestMode == "1",
		"base_url", cfg.BaseURL,
	)

	if cfg.TestMode == "1" {
		// WARN rather than INFO: a production installation left in test mode
		// takes no real money and looks completely healthy while doing it.
		log.Warn("PayTR is in TEST MODE; no real money will be taken",
			"setting", settingTestMode)
	}

	p.mod = newModule(cfg, log.With("module", ModuleName))
	p.mod.reports = h.PaymentReports()
	h.AddModule(p.mod)

	// The provider and the module share one store, and the module builds it in
	// Register — so the provider is handed the module rather than a store that
	// does not exist yet.
	h.RegisterPaymentProvider(p.mod.provider())

	// The callback is registered rather than bound. The ring refuses a route
	// with no verifier at startup and binds the path itself, so this plugin
	// cannot end up with the endpoint it had before ADR 0028: reachable by
	// anyone, with no quota, no body limit and no replay window.
	h.RegisterCallback(p.mod.callbackRoute())

	// The stuck-payment report used to happen once, inside Register, and the
	// class it names accumulates in a process that has been up for a week
	// rather than at the moment it boots. This is that report on a schedule; it
	// reads and logs and never touches a payment (job.go).
	h.RegisterJob(pendingWatch(p.mod))

	return nil
}

// readConfig reads and validates every setting.
func readConfig(h *coreplugin.Host) (config, error) {
	cfg := config{}

	for name, into := range map[string]*string{
		settingMerchantID: &cfg.MerchantID,
		settingKey:        &cfg.MerchantKey,
		settingSalt:       &cfg.MerchantSalt,
		settingSuccessURL: &cfg.SuccessURL,
		settingFailureURL: &cfg.FailureURL,
	} {
		v, ok := h.Setting(name)
		if !ok {
			return config{}, coreerrors.Invalid(codeMissingSetting,
				"the %s plugin cannot be set up without the %s setting", Name, name)
		}
		*into = v
	}

	for name, value := range map[string]string{
		settingSuccessURL: cfg.SuccessURL,
		settingFailureURL: cfg.FailureURL,
	} {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return config{}, coreerrors.Invalid(codeInvalidSetting,
				"%s has to be an absolute http(s) URL; %q was given", name, value)
		}
	}

	testMode, err := readTestMode(h)
	if err != nil {
		return config{}, err
	}
	cfg.TestMode = testMode

	base, ok := h.Setting(settingBaseURL)
	if !ok {
		base = defaultBaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(base, "/")

	return cfg, nil
}

// readTestMode reads the test-mode switch.
//
// It accepts only the exact words, for the reason every other switch in this
// repository does: a permissive parser makes it easy to be in test mode without
// meaning to, and test mode takes no real money while looking entirely healthy.
func readTestMode(h *coreplugin.Host) (string, error) {
	raw, ok := h.Setting(settingTestMode)
	if !ok {
		return "0", nil
	}
	switch raw {
	case "true":
		return "1", nil
	case "false":
		return "0", nil
	default:
		return "", coreerrors.Invalid(codeInvalidSetting,
			"%s only accepts %q or %q; %q was given", settingTestMode, "true", "false", raw)
	}
}

// slogDiscard is used when a caller gives no logger.
func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }
