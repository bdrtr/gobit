package cart

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
// It is asked only when a storefront body NAMES a customer — cart creation and
// the guest-to-registered handover. A guest cart never reaches it, which is
// what lets the check exist at all on a surface whose default path is a shopper
// with no account (ADR 0057).
//
// # Why lazily
//
// It is the shape this module already uses four times over for its flows: the
// identity comes from a module the EMBEDDER adds and the composition root adds
// those last, so resolving during Register would fail for a perfectly correct
// installation and make registration order part of the contract. The resolved
// name belongs to the CORE, so no module owns a slot the embedder has to fill.
//
// # Why an absent binding is NOT a refusal here
//
// This is where the cart parts company with the address book. ADR 0043 closed
// the address book by refusing when nothing is bound, and it could: those
// routes hand back a person's name, e-mail and street address, and there is no
// correct anonymous use of them. The cart's default path is a shopper with no
// account, and a body naming a customer is how a working installation that
// never bound a verifier registers a cart today. Refusing there would take a
// shipped surface away from an embedder doing nothing wrong, so this binding
// answers "nobody is bound" with a NIL identity and the handler serves the
// request unchecked. What ADR 0057 narrows is the MISMATCH, and that breaks
// only a caller who was lying.
//
// The residue is stated rather than hidden: with no verifier bound, a caller
// who knows an identifier can still open a cart as that customer. Binding one
// is what closes it, and the WARN below is where an operator is told so.
//
// The comparison is not here. It is corehttp.ProvenCustomer, shared with the
// customer and b2b storefronts, because an authorization rule copied per module
// is a rule that keeps answering after one copy drifts.
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
// The three returns are three different sentences, and the middle one is the
// reason this is not itself a corehttp.Identity: that contract answers with an
// identifier or with an error, and "this installation bound no verifier" is
// neither. Squeezing it into the error return is what made the first draft of
// ADR 0057 a breaking change.
func (b *identityBinding) identity(ctx context.Context) (corehttp.Identity, error) {
	b.once.Do(func() { b.resolve(ctx) })

	return b.svc, b.err
}

// resolve looks the identity up in the container and remembers the outcome.
//
// The three branches are three different sentences: bound; nothing bound, which
// is a deployment decision and leaves the claim unchecked behind a WARN naming
// the slot that would check it; and bound under the wrong type, which is a
// WIRING error and answers Internal rather than inheriting the container's
// KindInvalid — telling a client its request was invalid when no client could
// have written it differently is the fault the flow wrappers in this module
// already refuse to make.
//
// The decision is made ONCE and remembered, like its siblings: re-resolving on
// every request would reproduce the same answer at the cost of a container
// lookup per cart opened.
func (b *identityBinding) resolve(ctx context.Context) {
	svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
	switch {
	case err == nil:
		b.svc = svc
		b.log.InfoContext(ctx, "cart storefront identity bound",
			slog.String("service", corehttp.IdentityName))
	case errors.IsNotFound(err):
		b.log.WarnContext(ctx,
			"no customer identity is bound; a cart body naming a customer is taken at its "+
				"word, so a caller who knows an identifier can open a cart as that customer. "+
				"Bind one to close it",
			slog.String("service", corehttp.IdentityName))
	default:
		b.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the customer identity (%q)",
			ModuleName, corehttp.IdentityName)
	}
}
