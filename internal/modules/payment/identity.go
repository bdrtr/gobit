package payment

import (
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// storefrontIdentity binds the storefront balance reads (ADR 0253) to the
// embedder's customer identity (ADR 0370).
//
// An absent identity is a REFUSAL, as it is for the address book (ADR 0043) and
// not as it is for the cart (ADR 0125): a balance is one person's money, and
// there is no correct anonymous reader of it. The comparison itself is
// corehttp.ProvenCustomer, shared with every storefront that asks.
func storefrontIdentity(c *container.Container, log *slog.Logger) *identity.Binding {
	return identity.New(c, log, ModuleName, codeSetupFailed,
		"no customer identity is bound; the storefront balance routes will refuse")
}
