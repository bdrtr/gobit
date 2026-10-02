package b2b

import (
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/core/identity"
)

// storefrontIdentity binds the two b2b storefront routes, a customer's
// company and their employee record, to the embedder's customer identity
// (ADR 0370).
//
// What they hand back is the customer's employer and spending limit, the
// shape ADR 0043 closed on the address book. The binding hands the handler NO
// identity when nothing is bound, and the handler decides: since ADR 0125 it
// refuses, unless the installation trusts an unverified claim
// (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM) and serves the path's claim
// unchecked. The warning says which of the two this installation chose (D219).
func storefrontIdentity(c *container.Container, log *slog.Logger, trustUnverified bool) *identity.Binding {
	return identity.New(c, log, ModuleName, codeSetupFailed, absentIdentity(trustUnverified))
}

// absentIdentity is the warning of what the b2b storefront does without an
// identity, as the installation answers ADR 0125.
func absentIdentity(trustUnverified bool) string {
	if trustUnverified {
		return "no customer identity is bound and " +
			"STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM is set; the b2b storefront routes take the customer in " +
			"the path at its word, so a caller who knows an identifier reads that person's company and " +
			"spending limit. Bind one to close it"
	}

	return "no customer identity is bound; the b2b storefront routes refuse " +
		"every request (ADR 0125). Bind one to serve them"
}
