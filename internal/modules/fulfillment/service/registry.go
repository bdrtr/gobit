package service

import (
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// ProviderRegistry holds the shipping providers by their identifiers.
//
// The module puts its own default provider (manual.Provider) in here during
// Register and hands the registry to the container under the name
// "fulfillment.providers". The Phase 9 plugin system can, WITHOUT TOUCHING the
// core or this module, resolve the registry from the container and add its own
// provider; the contract is the FulfillmentProvider interface in
// internal/core/provider.
//
// It is safe for concurrent use: registration happens at startup, reading on
// every request.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.FulfillmentProvider
}

// NewProviderRegistry produces an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.FulfillmentProvider)}
}

// Register records the provider under its own identifier.
//
// A second registration with the same identifier returns errors.Conflict and
// the existing provider is KEPT. Overwriting it silently would leave it to the
// load order which provider runs in a setup where two plugins use the same
// identifier — and in shipping the price of that is the parcel being handed to
// an unexpected carrier.
func (r *ProviderRegistry) Register(p coreprovider.FulfillmentProvider) error {
	if p == nil {
		return errors.Invalid(CodeInvalidInput, "the provider cannot be nil")
	}
	id := strings.TrimSpace(p.ID())
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "the provider identifier cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[id]; exists {
		return errors.Conflict(CodeProviderExists,
			"a shipping provider with the identifier %q is already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider by its identifier; errors.NotFound if it is not
// registered.
//
// The error message writes the identifier LOOKED FOR and the REGISTERED
// identifiers together: forgetting to register a provider is a setup error that
// only surfaces at runtime, and it has to be diagnosable (see ADR 0002).
func (r *ProviderRegistry) Get(id string) (coreprovider.FulfillmentProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the provider identifier cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"the shipping provider %q is not registered; the registered ones are: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}
	return p, nil
}

// Has reports whether the provider is registered.
//
// It is used while creating an option: an option bound to an unregistered
// provider would blow up the moment it is shown to the customer or a
// fulfillment is opened; seeing the error on the admin surface, while the
// option is being created, is preferable.
func (r *ProviderRegistry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[strings.TrimSpace(id)]
	return ok
}

// IDs returns the registered provider identifiers in order.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedIDs()
}

// sortedIDs returns the registered identifiers in order; the caller must be
// holding the lock.
//
// The order is FIXED: had error messages and API responses been produced by
// ranging over the map, they would come out in a different order on every call,
// making diagnosis and testing harder.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
