package service

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// LocalProviderID is the id of the local calculation provider that ships in the
// box.
//
// This provider is used when NO provider_id in the region chain is filled in.
// An empty value does NOT mean "unconfigured" but "my parent's provider — and
// if there is none, local" (resolution order: [Service.providerFor]). Making the
// field mandatory would require single-provider installations to write the
// same string into every region.
//
// A province whose country is bound to an external authority writes this id
// EXPLICITLY if it wants to use the local calculation: the "local" intent is
// expressed by its NAME, not by an empty string. The distinction is required —
// the empty string describes inheritance, this id describes local, and had the
// two shared the same value, one of them could not be expressed.
const LocalProviderID = "local"

// TaxProvider is the contract a tax calculation provider offers this module.
//
// # Why it is not in the core
//
// Plan Section 6 says "TaxProvider", but core/provider defines only
// PaymentProvider and FulfillmentProvider, and this module cannot touch the
// core. The contract therefore lives HERE. The decision is temporary: when a
// second real provider (such as Avalara/TaxJar) is written, the interface has
// to move to core/provider/tax.go and the types here have to become aliases.
// The signatures are written to make that move cheap, in the same pattern as
// the two providers in the core.
//
// # Freedom from side effects
//
// Calculate HAS NO SIDE EFFECTS and can be called again: since the cart total
// is recomputed on every change, it is called many times with the same input
// and has to give the same result. If a provider carries this call to an
// external service, it has to take on caching itself; this module does not
// cache the call.
//
// # Who does the arithmetic
//
// The provider returns both the RATE applied and the AMOUNT it computed. The
// local provider computes the amount with [TaxOf], so the rounding direction is
// the module's guarantee. An external provider may return its own amount —
// most external services treat the amount, not the rate, as authoritative —
// but the result is VALIDATED: the amount has to be within [0, base] and the
// rate within [0, 100%] (see [Service.CalculateTax]). The validation keeps a
// provider failure from silently corrupting the cart total.
type TaxProvider interface {
	// ID is the provider's unique id; it is the value matched against the
	// provider_id in the region record.
	ID() string

	// Calculate computes the tax for the given region chain and line items.
	Calculate(ctx context.Context, in ProviderInput) (ProviderResult, error)
}

// ProviderInput is the input of a tax calculation that goes to the provider.
type ProviderInput struct {
	// RegionIDs is the resolved region chain: from the most SPECIFIC to the
	// general (province, then country). The local provider reads its rates by
	// these ids; external providers may ignore the field.
	RegionIDs []string
	// CountryCode is the ISO 3166-1 alpha-2 code (UPPER case).
	CountryCode string
	// ProvinceCode is the state/province code; empty if not given.
	ProvinceCode string
	// Items are the line items to be taxed.
	Items []TaxableItem
	// Shipping is the shipping line; if it is not to be taxed,
	// [ShippingInput.Taxable] is false.
	Shipping ShippingInput
	// PricesIncludeTax says that the incoming amounts ALREADY INCLUDE the tax.
	//
	// Retail prices are written this way in Turkey and in most of Europe: the
	// customer sees 199.00 and pays 199.00. When false, the amounts are NET and
	// the tax is added on top; this is the behavior from when the field did not
	// exist, and a provider that never reads the flag falls back to it.
	//
	// The flag HAS TO be here: the extraction requires knowing the rate, and
	// the side that selects the rate is the provider. Extracting on the service
	// side and sending the provider a net was MEASURED and is WRONG — putting
	// the extracted net through the normal tax produces one cent too much on
	// one amount in every six at 20% KDV ([TaxIncludedIn]).
	PricesIncludeTax bool
}

// ProviderResult is the provider's calculation.
//
// The order of the line items does not have to be the SAME as the input's; the
// matching is done through [ProviderItemTax.ID]. An id not in the input, or a
// missing line item, is a contract violation and the calculation is refused.
type ProviderResult struct {
	// Items is the tax computed per line item.
	Items []ProviderItemTax
	// Shipping is the tax of the shipping line; zero if it was not taxed.
	Shipping ProviderItemTax
}

// ProviderItemTax is the tax of a single line item computed by the provider.
type ProviderItemTax struct {
	// ID is the line item's id on the caller's side.
	ID string
	// RateID is the id of the rate applied; it may be empty with external
	// providers.
	RateID string
	// RateBps is the rate applied (basis points).
	RateBps int32
	// TaxAmount is the computed tax (minor unit).
	TaxAmount int64
	// TaxableAmount is the BASE the tax was computed on (minor unit).
	//
	// While [ProviderInput.PricesIncludeTax] is false this field is NOT READ:
	// the base is the amount sent to the provider itself and is recorded as
	// such. Not reading the field lets a provider that never sees the flag
	// keep working as it does today.
	//
	// While the flag is true the field is REQUIRED and validated: adding the
	// base and the tax has to give the gross that was sent. The whole of
	// inclusive pricing is in this equality — the customer pays what they saw
	// on the label.
	TaxableAmount int64
	// Components is the per-rate breakdown when the line was taxed by a STACK;
	// it is empty when one rate applied, which is what [RateBps] then says.
	//
	// A provider that does not stack leaves it empty and stays correct. When it
	// is filled it is VALIDATED: at least two entries, and their taxes add up
	// to [TaxAmount] exactly — see [validateComponents].
	Components []TaxComponent
}

// ProviderRegistry holds the tax providers by their ids.
//
// The module puts its own default provider ([LocalProvider]) here during
// Register and hands the registry to the container under the name
// "tax.providers". The Phase 9 plugin system can resolve the registry from the
// container and add its own provider WITHOUT TOUCHING the core or this module.
//
// It is safe for concurrent use: registration happens at startup, reading on
// every request.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]TaxProvider
}

// NewProviderRegistry builds an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{providers: make(map[string]TaxProvider)}
}

// Register registers the provider under its own id.
//
// A second registration under the same id returns errors.Conflict and the
// existing provider is KEPT. Silently overwriting would leave which provider
// runs, in an installation where two plugins use the same id, to load order —
// in tax the cost of that is invoices issued at the wrong rate.
func (r *ProviderRegistry) Register(p TaxProvider) error {
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
			"a tax provider with the id %q is already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns the provider by its id; errors.NotFound if it is not registered.
//
// An empty id means [LocalProviderID]. Inheritance along the region chain is
// resolved BEFORE this call ([Service.providerFor]); an empty id arrives here
// only if the whole chain is empty.
//
// The error message writes the id SOUGHT and the REGISTERED ids together; a
// provider somebody forgot to register is a setup error that surfaces at run
// time, and it has to be diagnosable (see ADR 0002).
func (r *ProviderRegistry) Get(id string) (TaxProvider, error) {
	wanted := strings.TrimSpace(id)
	if wanted == "" {
		wanted = LocalProviderID
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[wanted]
	if !ok {
		return nil, errors.NotFound(CodeProviderNotFound,
			"the tax provider %q is not registered; the registered ones are: %s",
			wanted, strings.Join(r.sortedIDs(), ", "))
	}
	return p, nil
}

// IDs returns the registered provider ids in order.
func (r *ProviderRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sortedIDs()
}

// sortedIDs returns the registered ids in order; the caller has to hold the
// lock.
//
// The order is fixed: had the error messages been produced by ranging over the
// map, they would come out in a different order on every call, making
// diagnosis and testing harder.
func (r *ProviderRegistry) sortedIDs() []string {
	out := make([]string, 0, len(r.providers))
	for id := range r.providers {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// RateSource is the local provider's rate source.
//
// The interface is defined on the CONSUMING side (the in-module counterpart of
// ADR 0001); the concrete implementation is the repository package. This is
// the boundary that lets the local provider be tested without a database.
type RateSource interface {
	// ListTaxRatesByRegions returns the rates in the region chain in a SINGLE
	// round trip.
	ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error)
	// ListTaxRateRulesByRates returns the rules of the given rates in a SINGLE
	// round trip.
	ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error)
}
