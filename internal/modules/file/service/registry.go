package service

import (
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// ProviderRegistry keeps the file providers by their identifiers.
//
// The module puts its own default provider
// ([github.com/bdrtr/gobit/internal/modules/file/local.Provider]) in here during
// Register and hands the registry to the container under the name
// "file.providers". The plugin system (coreplugin.Host.RegisterFileProvider)
// resolves the registry and adds its own provider WITHOUT TOUCHING the core and
// this module; the contract is the FileProvider interface in
// core/provider.
//
// It is safe for concurrent use: the registration is done at startup, the
// reading on every upload and on every serve request.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]coreprovider.FileProvider
}

// NewProviderRegistry produces an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]coreprovider.FileProvider)}
}

// Register records the provider under its own identifier.
//
// A second registration with the same identifier returns errors.Conflict and
// the existing provider is PRESERVED. Overwriting it silently would, in an
// installation where two plugins use the same identifier, leave which provider
// runs up to the load order — and here the price of that is concrete: the
// identifier is WRITTEN into the records and the only thing able to read a file
// is the provider that wrote it. The day the order changes, the files written
// yesterday cannot be read today.
func (r *ProviderRegistry) Register(p coreprovider.FileProvider) error {
	if p == nil {
		return errors.Invalid(CodeInvalidInput, "the provider cannot be nil")
	}

	id := strings.TrimSpace(p.ID())
	if id == "" {
		return errors.Invalid(CodeInvalidInput, "the provider id cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.providers[id]; exists {
		return errors.Conflict(CodeProviderExists,
			"a file provider with the id %q is already registered", id)
	}
	r.providers[id] = p

	return nil
}

// Get returns the provider by its identifier; errors.NotFound if it is not
// registered.
//
// The error message writes the SOUGHT identifier and the REGISTERED identifiers
// together: a provider being forgotten to be registered (or FILE_PROVIDER being
// written wrong) is a setup fault that surfaces at run time and it has to be
// diagnosable (see ADR 0002).
func (r *ProviderRegistry) Get(id string) (coreprovider.FileProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the provider id cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"the %q file provider is not registered; the registered ones are: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}

	return p, nil
}

// IDs returns the registered provider identifiers, in order.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.sortedIDs()
}

// sortedIDs returns the registered identifiers in order; the caller must be
// holding the lock.
//
// The order is stable: had the error messages been produced by ranging over the
// map, they would come out in a different order on every call, which would make
// diagnosis and testing harder.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)

	return out
}
