package order

import (
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// storefrontIdentity binds the storefront's list of a customer's own orders
// (ADR 0367) to the embedder's customer identity (ADR 0370).
//
// An absent identity is a REFUSAL, as it is for the address book (ADR 0043) and
// not as it is for the cart (ADR 0125): a list of one person's orders has no
// correct anonymous reader, as one person's balance has none. The comparison is
// corehttp.ProvenCustomer, shared with every storefront that asks.
func storefrontIdentity(c *container.Container, log *slog.Logger) *identity.Binding {
	return identity.New(c, log, ModuleName, codeSetupFailed,
		"no customer identity is bound; the storefront's own orders route will refuse")
}
