package cart

import (
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// storefrontIdentity binds the cart's storefront to the embedder's customer
// identity (ADR 0370). It is asked only when a storefront body NAMES a
// customer — cart creation and the guest-to-registered handover; a guest cart
// never reaches it.
//
// # Why an absent identity is not refused here
//
// This is where the cart parts company with the address book. ADR 0043 closed
// the address book by refusing when nothing is bound, and it could: there is no
// correct anonymous use of a person's address. The cart's default path is a
// shopper with no account, so the binding hands the handler NO identity and the
// handler decides what that means for a body naming a customer: since ADR 0125
// it refuses it, unless the installation trusts an unverified claim
// (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM) and serves it unchecked. The
// warning says which of the two this installation chose (D219).
func storefrontIdentity(c *container.Container, log *slog.Logger, trustUnverified bool) *identity.Binding {
	return identity.New(c, log, ModuleName, codeSetupFailed, absentIdentity(trustUnverified))
}

// absentIdentity is the warning of what the cart's storefront does without an
// identity, as the installation answers ADR 0125.
func absentIdentity(trustUnverified bool) string {
	if trustUnverified {
		return "no customer identity is bound and " +
			"STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM is set; a cart body naming a customer is taken at its " +
			"word, so a caller who knows an identifier can open a cart as that customer. Bind one to close it"
	}

	return "no customer identity is bound; a cart body naming a customer is " +
		"refused and only guest carts are opened (ADR 0125). Bind one to open carts for customers"
}
