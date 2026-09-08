package customer

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// identityBinding resolves the embedder's customer identity ON FIRST USE.
//
// # Why lazily, in a module that was written to resolve nothing
//
// [Module.Register] says, and means, that customer resolves no other module's
// service — that is the one thing that would create an ordering dependency, and
// the module deliberately has none. The identity comes from a module the
// EMBEDDER adds, which the composition root adds after everything in the box
// (see internal/app), so resolving it during Register would fail for a
// perfectly correct installation and make registration order part of the
// contract. Deferring to the first request keeps the note in Register true: by
// the time a storefront request arrives, every module has long been registered.
//
// It is the shape order/module.go already uses for the spending policy and the
// invoicing flow, and cart for its own flows. What is new here is only that the
// resolved name belongs to the CORE (corehttp.IdentityName) rather than to
// another module, so the direction of naming runs module to core and no module
// owns a slot the embedder has to fill.
//
// # Why an absent binding is an error and not an empty answer
//
// The spending policy answers "there is no limit" when b2b is not installed,
// because that is a complete and correct answer for a B2C shop. This wrapper
// cannot do the same. "No identity is bound" does not mean every caller is who
// they say they are; it means nobody looked, and the only honest thing to
// return is a refusal (ADR 0043, ADR 0007). The decision is made ONCE and
// stored, like its siblings: re-resolving on every request would do nothing but
// reproduce the same answer at the cost of a container lookup per address read.
type identityBinding struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  corehttp.Identity
	err  error
}

// That the wrapper satisfies the contract the handler expects is pinned down at
// compile time. The handler holds the interface, so a signature that drifted
// would otherwise be caught only where the module is wired.
var _ corehttp.Identity = (*identityBinding)(nil)

// CustomerID returns the customer identifier the request proves.
func (b *identityBinding) CustomerID(r *http.Request) (string, error) {
	b.once.Do(func() { b.resolve(r.Context()) })
	if b.err != nil {
		return "", b.err
	}

	return b.svc.CustomerID(r)
}

// resolve looks the identity up in the container and remembers the outcome.
//
// The three branches are three different sentences and none of them may be
// folded into another:
//
//   - resolved — the embedder bound one, and every storefront request now goes
//     through it.
//   - not registered — nothing was bound. The refusal is Unauthorized rather
//     than Internal because it is a deployment decision (or an oversight), not
//     a fault in the running server, and because the client-visible half of it
//     is exactly the half a shopper's browser can act on: sign in.
//   - registered under the wrong type — a WIRING error. It is turned into
//     KindInternal rather than passed through: the container reports a type
//     mismatch as KindInvalid, and inheriting that would tell the caller "your
//     request is invalid" with a 422 for a request that no client could have
//     written differently. The same rationale is written in order's spending
//     policy wrapper.
func (b *identityBinding) resolve(ctx context.Context) {
	svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
	switch {
	case err == nil:
		b.svc = svc
		b.log.InfoContext(ctx, "customer identity bound",
			slog.String("service", corehttp.IdentityName))
	case errors.IsNotFound(err):
		b.err = errors.Unauthorized(corehttp.CodeIdentityNotBound,
			"this installation has bound no customer identity under %q, so a request "+
				"naming a customer cannot be served", corehttp.IdentityName)
		b.log.WarnContext(ctx,
			"no customer identity is bound; the storefront profile and address routes will refuse",
			slog.String("service", corehttp.IdentityName))
	default:
		b.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the customer identity (%q)",
			ModuleName, corehttp.IdentityName)
	}
}
