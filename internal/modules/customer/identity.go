package customer

import (
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// storefrontIdentity binds the storefront's profile, address book and
// wishlist routes to the embedder's customer identity (ADR 0043, ADR 0370).
//
// The binding resolves on first use, which keeps true what [Module.Register]
// says: customer resolves no other module's service while it registers, so
// registration order is no part of its contract.
//
// An absent identity is a REFUSAL. The spending policy answers "there is no
// limit" when b2b is not installed, because that is a complete and correct
// answer for a B2C shop; "no identity is bound" does not mean every caller is
// who they say they are, it means nobody looked, and the only honest answer is
// a refusal (ADR 0043, ADR 0007).
func storefrontIdentity(c *container.Container, log *slog.Logger) *identity.Binding {
	return identity.New(c, log, ModuleName, codeSetupFailed,
		"no customer identity is bound; the storefront profile and address routes will refuse")
}
