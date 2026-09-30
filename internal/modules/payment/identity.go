package payment

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// identityBinding resolves the embedder's customer identity on first use, for
// the storefront balance reads (ADR 0253).
//
// It is lazy for the reason the customer module's is: the identity comes from
// a module the embedder adds, which the composition root adds after everything
// in the box, so resolving it during Register would make registration order
// part of the contract.
//
// An absent binding is a REFUSAL, as it is for the address book (ADR 0043) and
// not as it is for the cart (ADR 0057): a balance is one person's money, and
// there is no correct anonymous reader of it. The comparison itself is
// corehttp.ProvenCustomer, shared with every storefront that asks.
type identityBinding struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  corehttp.Identity
	err  error
}

var _ corehttp.Identity = (*identityBinding)(nil)

// CustomerID returns the customer identifier the request proves.
func (b *identityBinding) CustomerID(r *http.Request) (string, error) {
	b.once.Do(func() { b.resolve(r.Context()) })
	if b.err != nil {
		return "", b.err
	}

	return b.svc.CustomerID(r)
}

// resolve looks the identity up and remembers the outcome: bound; nothing
// bound, which refuses as Unauthorized because a shopper's client can act on
// it by signing in; or bound under the wrong type, a wiring error answered as
// Internal rather than as the container's KindInvalid.
func (b *identityBinding) resolve(ctx context.Context) {
	svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
	switch {
	case err == nil:
		b.svc = svc
	case errors.IsNotFound(err):
		b.err = errors.Unauthorized(corehttp.CodeIdentityNotBound,
			"this installation has bound no customer identity under %q, so a request "+
				"naming a customer cannot be served", corehttp.IdentityName)
		b.log.WarnContext(ctx,
			"no customer identity is bound; the storefront balance routes will refuse",
			slog.String("service", corehttp.IdentityName))
	default:
		b.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the customer identity (%q)",
			ModuleName, corehttp.IdentityName)
	}
}
