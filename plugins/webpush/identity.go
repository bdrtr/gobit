package webpush

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A device is bound to the customer the request proves (ADR 0440).
//
// The binding decides who receives an order's confirmation on a lock screen,
// for as long as the device stays subscribed. Until ADR 0440 the subscription's
// customer_id was stored as the storefront wrote it, on the reasoning that
// gobit could verify no customer; since ADR 0043 an installation binds the
// identity that does, and a stranger holding the publishable key and a
// customer's id subscribed a device of their own and received that customer's
// confirmations (D293). So a subscription naming a customer is put to the
// identity through corehttp.ProvenCustomer, the comparison every storefront
// surface naming a customer shares, and the device is bound to what it proves.

// identityBinding resolves the installation's customer identity on first use.
//
// It is the lazy lookup internal/core/identity carries for the modules in the
// box, written here because a plugin may not import internal/: the identity
// is provided by a module the embedder adds after every plugin, so resolving
// it while this module registers would find nothing in a correct installation.
// The comparison is not copied; it is corehttp's.
type identityBinding struct {
	c   *container.Container
	log *slog.Logger

	once sync.Once
	svc  corehttp.Identity
	err  error
}

// identity returns the bound customer identity, nil when none is bound, or
// the wiring fault.
//
// A binding that was never built — a module whose Register did not run —
// answers like an installation that bound nothing, and a subscription naming a
// customer is refused there as it is.
func (b *identityBinding) identity(ctx context.Context) (corehttp.Identity, error) {
	if b == nil || b.c == nil {
		return nil, nil
	}

	b.once.Do(func() {
		svc, err := container.Resolve[corehttp.Identity](b.c, corehttp.IdentityName)
		switch {
		case err == nil:
			b.svc = svc
		case coreerrors.IsNotFound(err):
			b.log.WarnContext(ctx, "no customer identity is bound; a push subscription naming a "+
				"customer is refused, and a device subscribes bound to nobody",
				"service", corehttp.IdentityName)
		default:
			b.err = coreerrors.Wrap(err, coreerrors.KindInternal, codeSetupFailed,
				"the %s module could not resolve the customer identity (%q)", ModuleName, corehttp.IdentityName)
		}
	})

	return b.svc, b.err
}

// boundCustomer returns the customer a subscription binds its device to:
// nobody when the body names nobody, and the customer the installation's
// identity proves when it names one.
//
// Every other answer is corehttp.ProvenCustomer's refusal: no identity bound,
// 401 identity_not_bound; no proof, the identity's own refusal or 401
// identity_refused; another customer proven, 403 identity_mismatch; an
// identity that cannot answer, its own class. STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM
// is not read: it serves the cart's and b2b's claim (ADR 0125), and believing
// this one is the fault D293 records.
func (m *webpushModule) boundCustomer(r *http.Request, claimed string) (string, error) {
	claimed = strings.TrimSpace(claimed)
	if claimed == "" {
		return "", nil
	}

	identity, err := m.identity.identity(r.Context())
	if err != nil {
		return "", err
	}

	return corehttp.ProvenCustomer(identity, r, claimed)
}
