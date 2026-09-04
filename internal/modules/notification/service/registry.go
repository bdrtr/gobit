package service

import (
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// ProviderRegistry holds the notification providers under their identifiers.
//
// The module puts its own default provider
// ([github.com/bdrtr/gobit/internal/modules/notification/logonly.Provider])
// in here during Register and hands the registry to the container under the
// name "notification.providers". The plugin system
// (coreplugin.Host.RegisterNotificationProvider) resolves that registry and
// adds its own provider WITHOUT TOUCHING the core or this module; the contract
// is the NotificationProvider interface in internal/core/provider.
//
// It is safe for concurrent use: registration happens at startup, reading on
// every notification.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.NotificationProvider
}

// NewProviderRegistry produces an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.NotificationProvider)}
}

// Register records the provider under its own identifier.
//
// A second registration with the same identifier returns errors.Conflict and
// the existing provider is KEPT. Overwriting it silently would, in a setup
// where two plugins use the same identifier, leave which provider runs up to
// the load order — and the price of that here is an order confirmation that is
// believed to have gone to the customer never being sent at all, or being sent
// from the wrong account.
func (r *ProviderRegistry) Register(p coreprovider.NotificationProvider) error {
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
			"a notification provider with the identifier %q is already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider by its identifier; errors.NotFound when it is not
// registered.
//
// The error message writes the identifier that was LOOKED FOR and the
// REGISTERED identifiers together: a provider being forgotten during
// registration (or NOTIFICATION_PROVIDER being misspelled) is a setup fault
// that surfaces at run time and it has to be diagnosable (see ADR 0002).
func (r *ProviderRegistry) Get(id string) (coreprovider.NotificationProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the provider identifier cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"the notification provider %q is not registered; the registered ones are: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}
	return p, nil
}

// IDs returns the registered provider identifiers in sorted order.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedIDs()
}

// sortedIDs returns the registered identifiers sorted; the caller has to be
// holding the lock.
//
// The order is STABLE: had the error messages been produced by ranging over the
// map, they would come out in a different order on every call and that would
// make diagnosis and testing harder.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
