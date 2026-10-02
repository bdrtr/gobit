// Package identity resolves the embedder's customer identity for the modules
// whose storefront routes name a customer (ADR 0370).
//
// # Why lazily
//
// The identity comes from a module the EMBEDDER adds, and the composition root
// adds those after everything in the box (see internal/app). Resolving it while
// a module registers would fail for a perfectly correct installation and make
// registration order part of the contract; by the time a storefront request
// arrives, every module has long been registered. The resolved name belongs to
// the CORE (corehttp.IdentityName), so the naming runs module to core and no
// module owns a slot the embedder has to fill (ADR 0043).
//
// # Why one binding
//
// The comparison every storefront route makes is corehttp.ProvenCustomer's,
// shared since ADR 0057 because a rule copied per module keeps answering after
// one copy drifts. The lookup in front of it was copied five times, and D219 is
// what that cost.
//
// # What a module still decides
//
// Whether an absent identity refuses is which door the module's handler holds.
// [Binding.CustomerID] refuses — the address book, a balance, a list of orders
// have no correct anonymous reader (ADR 0043). [Binding.Identity] hands the
// handler no identity and lets it decide, as the cart and b2b do under ADR
// 0125. What the module says for itself is the sentence its operator is warned
// with, because only the module knows what its routes do without an identity.
package identity

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Binding resolves the customer identity on first use, once, and classifies
// what it finds.
type Binding struct {
	c         *container.Container
	log       *slog.Logger
	module    string
	setupCode string
	warning   string

	once sync.Once
	svc  corehttp.Identity
	err  error
}

// A Binding is the identity a refusing module's handler holds; a signature that
// drifted would otherwise be caught only where a module is wired.
var _ corehttp.Identity = (*Binding)(nil)

// New binds the module named module to the container's customer identity.
// setupCode is the module's own code for a wiring fault, so the error a client
// sees names the module that could not be set up; warning is the sentence its
// operator is warned with, once, on the first request, when nothing is bound.
func New(c *container.Container, log *slog.Logger, module, setupCode, warning string) *Binding {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Binding{c: c, log: log, module: module, setupCode: setupCode, warning: warning}
}

// Identity returns the bound customer identity, nil and no error when none is
// bound, or the wiring fault.
//
// The middle answer is why a module that decides for itself holds this method
// rather than the Binding: corehttp.Identity answers with an identifier or an
// error, and "this installation bound no verifier" is neither.
func (b *Binding) Identity(ctx context.Context) (corehttp.Identity, error) {
	b.once.Do(func() { b.resolve(ctx) })

	return b.svc, b.err
}

// CustomerID returns the customer identifier the request proves, and refuses
// as Unauthorized when nothing is bound: a caller that asks for a proof is
// never handed an empty one, and the refusal is the half a shopper's browser
// can act on by signing in.
func (b *Binding) CustomerID(r *http.Request) (string, error) {
	svc, err := b.Identity(r.Context())
	switch {
	case err != nil:
		return "", err
	case svc == nil:
		return "", notBound()
	}

	return svc.CustomerID(r)
}

// resolve looks the identity up in the container and remembers the outcome.
//
// The three branches are three different sentences and none of them may be
// folded into another:
//
//   - resolved — the embedder bound one, and every storefront request now goes
//     through it.
//   - not registered — nothing was bound, a deployment decision rather than a
//     fault in the running server, so nothing is remembered as an error; the
//     operator is warned in the module's own words.
//   - registered under the wrong type — a WIRING error, turned into
//     KindInternal: the container reports a type mismatch as KindInvalid, and
//     inheriting that would answer a 422 for a request no client could have
//     written differently.
//
// The decision is made ONCE: re-resolving on every request would reproduce
// the same answer at the cost of a container lookup per request.
func (b *Binding) resolve(ctx context.Context) {
	svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
	switch {
	case err == nil:
		b.svc = svc
		b.log.InfoContext(ctx, "customer identity bound",
			slog.String("module", b.module), slog.String("service", corehttp.IdentityName))
	case errors.IsNotFound(err):
		b.log.WarnContext(ctx, b.warning,
			slog.String("module", b.module), slog.String("service", corehttp.IdentityName))
	default:
		b.err = errors.Wrap(err, errors.KindInternal, b.setupCode,
			"the %s module could not resolve the customer identity (%q)", b.module, corehttp.IdentityName)
	}
}

// notBound is the refusal of a request naming a customer in an installation
// that bound no identity.
func notBound() error {
	return errors.Unauthorized(corehttp.CodeIdentityNotBound,
		"this installation has bound no customer identity under %q, so a request "+
			"naming a customer cannot be served", corehttp.IdentityName)
}
