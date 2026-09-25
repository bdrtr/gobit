package service

import (
	"slices"
	"strings"
	"sync"

	coreprovider "github.com/bdrtr/gobit/core/provider"

	"github.com/bdrtr/gobit/core/errors"
)

// ProviderRegistry holds the payment providers by their identities.
//
// The module puts its own default provider
// ([github.com/bdrtr/gobit/internal/modules/payment/manual.Provider]) here
// during Register and hands the registry to the container under the name
// "payment.providers". A plugin adds its own provider by resolving the registry
// from the container, WITHOUT TOUCHING the core or this module; the contract is
// the PaymentProvider interface in core/provider, and plugins/paymentpaytr
// satisfies it.
//
// It is safe for concurrent use: registration happens at startup, reads on
// every request.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.PaymentProvider
}

// NewProviderRegistry builds an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.PaymentProvider)}
}

// Register records the provider under its own identity.
//
// A second registration under the same identity returns errors.Conflict and the
// existing provider is KEPT. Overwriting silently would leave which provider runs
// to the load order in an installation where two plugins use the same identity —
// and in payments the price of that is money going to an institution nobody
// expected.
func (r *ProviderRegistry) Register(p coreprovider.PaymentProvider) error {
	if p == nil {
		return errors.Invalid(CodeInvalidInput, "the provider cannot be nil")
	}
	id := strings.TrimSpace(p.ID())
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "the provider identity cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[id]; exists {
		return errors.Conflict(CodeProviderExists,
			"a payment provider with the identity %q is already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider by its identity; errors.NotFound when it is not
// registered.
//
// The message names the identity ASKED FOR together with the REGISTERED ones: a
// provider somebody forgot to register is a setup failure that surfaces at run
// time, and it has to be diagnosable (see ADR 0002). The registered identities
// are no secret — the storefront lists them.
func (r *ProviderRegistry) Get(id string) (coreprovider.PaymentProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the provider identity cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"the payment provider %q is not registered; the registered ones are: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}
	return p, nil
}

// IDs returns the registered provider identities in sorted order.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedIDs()
}

// sortedIDs returns the registered identities sorted; the caller must hold the
// lock.
//
// The order is fixed: produced by ranging over the map, error messages and API
// answers would come out in a different order on every call, which makes both
// diagnosis and testing harder.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
