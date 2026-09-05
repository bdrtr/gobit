// Package havale is an OUT-OF-TREE plugin written only against the packages
// gobit publishes. Its whole purpose is to fail to compile if the published
// surface is missing something an extension author needs.
package havale

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreeventbus "github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// Plugin registers a bank-transfer payment provider and one status route.
type Plugin struct{}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name identifies the plugin to the host.
func (p *Plugin) Name() string { return "havale" }

// Setup registers the provider, a route and an event subscription.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	account, ok := h.Setting("account")
	if !ok {
		return coreerrors.Invalid("havale_account_missing",
			"the havale plugin needs an account setting")
	}
	h.Logger().Info("havale is set up", "account", account)
	h.RegisterPaymentProvider(&transfer{account: account})
	h.AddRoutes(func(r chi.Router) {
		r.Get("/store/v1/havale/account", func(w http.ResponseWriter, r *http.Request) {
			corehttp.WriteJSON(r.Context(), w, http.StatusOK, map[string]string{"account": account})
		})
	})
	// "order.placed" is a name gobit really publishes. The example said
	// "order.created" until a measurement caught it, and the mistake is worth
	// recording rather than quietly fixing: a subscription to a name nobody
	// emits compiles, starts, and stays silent forever — the plugin looks wired
	// and receives nothing. Nothing in the repository refuses it, so the only
	// defence is that the name written here is one a publisher really uses.
	h.Subscribe("order.placed", func(ctx context.Context, e coreeventbus.Event) error {
		h.Logger().InfoContext(ctx, "an order was created", "event", e.Name)

		return nil
	})

	return nil
}

// transfer is the payment provider itself.
type transfer struct{ account string }

// ID is the durable identity the configuration selects.
func (t *transfer) ID() string { return "havale" }

// CreateSession opens a transfer the customer will make by hand.
func (t *transfer) CreateSession(
	_ context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	data, err := json.Marshal(map[string]string{"account": t.account})
	if err != nil {
		return coreprovider.Session{}, coreerrors.Internal("havale_encode_failed",
			"the account could not be encoded")
	}

	return coreprovider.Session{
		ID:           in.IdempotencyKey,
		Status:       coreprovider.SessionPending,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
		Data:         data,
	}, nil
}

// Authorize holds nothing: a transfer is confirmed by an operator.
func (t *transfer) Authorize(_ context.Context, _ string) (coreprovider.AuthResult, error) {
	return coreprovider.AuthResult{Status: coreprovider.SessionPending}, nil
}

// Capture is a no-op; the money arrives out of band.
func (t *transfer) Capture(_ context.Context, _ string, _ int64) error { return nil }

// Refund is made by hand at the bank.
func (t *transfer) Refund(_ context.Context, _ string, _ int64) error { return nil }

// Cancel is idempotent, as the saga requires.
func (t *transfer) Cancel(_ context.Context, _ string) error { return nil }

// compile-time proof that the two published contracts are satisfiable.
var (
	_ coreplugin.Plugin            = (*Plugin)(nil)
	_ coreprovider.PaymentProvider = (*transfer)(nil)
)
