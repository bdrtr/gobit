package b2b

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// identityBinding resolves the embedder's customer identity ON FIRST USE and
// hands it to the handler, or hands the handler NOTHING when the installation
// bound none.
//
// The two storefront routes name a customer in their path and, until ADR 0057,
// believed it whatever else was true. What they hand back is the customer's
// employer and their spending limit, so a caller holding somebody's identifier
// read that person's company and allowance — the same shape ADR 0043 closed on
// the address book, in the copy that record named in its own consequences.
//
// # Why lazily
//
// The identity comes from a module the EMBEDDER adds, and the composition root
// adds those after everything in the box. Resolving during Register would fail
// for a perfectly correct installation and make registration order part of the
// contract. It is the shape order/module.go uses for the spending policy and
// cart for its flows, and the resolved name belongs to the CORE, so no module
// owns a slot the embedder has to fill.
//
// # Why an absent binding is NOT a refusal
//
// Because these two routes are shipped and working. An installation that bound
// no verifier reads a company and a limit through them today, and ADR 0057
// narrows what is WRONG rather than withdrawing what works: a path claim the
// bound identity contradicts is refused, and a path claim nobody can check is
// served exactly as before. The residue is real and named where an operator
// meets it — the WARN below — rather than paid for by an embedder who upgraded.
//
// What is NOT here is the comparison. That is corehttp.ProvenCustomer, shared
// with the address book and the cart, because an authorization rule copied per
// module is a rule that keeps answering after one copy drifts (ADR 0057).
type identityBinding struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  corehttp.Identity
	err  error
}

// identity returns the bound customer identity, a NIL one when the installation
// bound none, or an error when the binding itself is broken.
//
// The middle answer is why this is not itself a corehttp.Identity: that
// contract answers with an identifier or with an error, and "this installation
// bound no verifier" is neither.
func (b *identityBinding) identity(ctx context.Context) (corehttp.Identity, error) {
	b.once.Do(func() { b.resolve(ctx) })

	return b.svc, b.err
}

// resolve looks the identity up in the container and remembers the outcome.
//
// The three branches are three different sentences: bound; nothing bound (a
// deployment decision, so the routes go on answering and an operator is warned
// what that costs); and bound under the wrong type (a WIRING error, so Internal
// — the container reports a type mismatch as KindInvalid and inheriting that
// would tell a client its request was invalid when no client could have written
// it differently).
//
// The decision is made ONCE and remembered: re-resolving per request would
// reproduce the same answer at the cost of a container lookup on every read.
func (b *identityBinding) resolve(ctx context.Context) {
	svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
	switch {
	case err == nil:
		b.svc = svc
		b.log.InfoContext(ctx, "b2b storefront identity bound",
			slog.String("service", corehttp.IdentityName))
	case errors.IsNotFound(err):
		b.log.WarnContext(ctx,
			"no customer identity is bound; the b2b storefront routes take the customer in "+
				"the path at its word, so a caller who knows an identifier reads that "+
				"person's company and spending limit. Bind one to close it",
			slog.String("service", corehttp.IdentityName))
	default:
		b.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the customer identity (%q)",
			ModuleName, corehttp.IdentityName)
	}
}
