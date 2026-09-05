// Package service is the business logic of the fulfillment module.
//
// The module's responsibility in one sentence: to know how far a PHYSICAL order
// has come — which shipping option costs how much, has the fulfillment been
// opened, has it set out, has it been delivered.
//
// # State machine
//
// The fulfillment's transition table lives on [models.FulfillmentStatus], in the
// CancelAction, ShipAction and DeliverAction methods, as pure functions; this
// package only turns the result into a typed error. Every illegal transition
// returns errors.Conflict (e.g. canceling a delivered fulfillment). A
// transition into a status the fulfillment is already in, however, is NOT an
// error but a silent no-op; that is where idempotency comes from.
//
// # A price does not come from two sources
//
// The fee of a shipping option is either the fixed amount on the row
// ([models.PriceFlat]) or the provider's Quote ([models.PriceCalculated]) —
// never both. A calculated option's Amount field must be zero and that is
// enforced at the schema level too; a price with two sources would leave it to
// the reader to decide which one applies.
//
// # The provider call is INSIDE the transaction
//
// Creating and canceling a fulfillment call the provider UNDER the row lock (or
// the unique index lock). The cost is plain: a slow provider holds the row's
// lock for that whole time. What is bought in return is the "exactly one
// fulfillment" guarantee — had the lock been released before the provider call,
// the second of two concurrent calls arriving with the same idempotency key
// would read the first one's row before the provider identifier was written and
// would return a HALF fulfillment. The same decision was made in the payment
// module for the same reason.
//
// Because the manual provider shares the same store, its call JOINS this
// transaction (see repository.Repository.WithTx: a nested call does not open a
// new transaction). This keeps the imitated provider's ledger atomic with the
// module's records; with a real network provider there is no such guarantee, and
// saga compensation exists for exactly that.
//
// # Module isolation
//
// This module knows no other module (Principle 2.1/2.4, ADR 0001).
// [models.Fulfillment.Reference] is an order identifier,
// [models.ShippingOption.RegionID] is a region identifier and
// [models.FulfillmentItem.LineItemID] is an order line identifier; all three are
// stored as free text, no foreign key is given (Principle 2.2) and their
// existence is not validated here — the validation is the job of the workflow
// that knows those modules.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// EntityName is the entity name the module offers to the Query layer. The
// provider is registered with the container under the name "<EntityName>.query"
// (ADR 0004).
const EntityName = "shipping_option"

// Error codes. Clients may branch on these; the messages can change, the codes
// do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "fulfillment_invalid_input"
	// CodeInvalidTransition reports that an illegal transition was attempted in
	// the state machine (e.g. canceling a delivered fulfillment).
	CodeInvalidTransition = "fulfillment_invalid_transition"
	// CodeProviderNotFound reports that the requested provider is not
	// registered.
	CodeProviderNotFound = "fulfillment_provider_not_found"
	// CodeProviderExists reports that a second provider was about to be
	// registered under the same identifier.
	CodeProviderExists = "fulfillment_provider_already_registered"
	// CodeIdempotencyMismatch reports that the same key was used for ANOTHER
	// fulfillment.
	CodeIdempotencyMismatch = "fulfillment_idempotency_key_mismatch"
	// CodeProfileInUse reports that a profile still holding options was about to
	// be deleted.
	CodeProfileInUse = "fulfillment_shipping_profile_in_use"
	// CodeProviderContract reports that the provider returned a response outside
	// the contract; it does not occur in normal operation.
	CodeProviderContract = "fulfillment_provider_contract_violation"
	// CodeNoShippingLocation reports that no location is left for the
	// fulfillment to leave from (see [Service.RankLocations]).
	CodeNoShippingLocation = "fulfillment_no_shipping_location"
	// CodeNoServiceableLocation reports that none of the candidate warehouses
	// serves the destination region (see [Service.RankLocations]).
	//
	// It is a SEPARATE code from [CodeNoShippingLocation] because the work the
	// operator has to do is separate too: there, there is no stock; here, the
	// region coverage of the warehouses is set up wrong. The kind is Conflict in
	// both cases.
	CodeNoServiceableLocation = "fulfillment_no_serviceable_location"
	// CodeNotReady reports that the service was built with a missing dependency.
	CodeNotReady = "fulfillment_service_not_ready"
)

// Pagination bounds (plan Section 8: limit/offset).
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that may be requested in one call.
	MaxLimit int64 = 100
)

// maxTextLen is the upper bound for free-text fields. The bound prevents a
// single request from writing unbounded text into the database.
const maxTextLen = 512

// maxItemsPerFulfillment is the number of items that may be put into a single
// fulfillment. The bound prevents a single request from writing unbounded rows.
const maxItemsPerFulfillment = 500

// Store is the persistence surface the service needs.
//
// The interface is defined on the CONSUMING side, that is, here (the pattern of
// ADR 0001). The service does NOT import the repository package; the concrete
// store satisfies these signatures structurally and the wiring is done in
// module.go. This is what lets unit tests be written without a real database,
// against a fake store a few lines long.
//
// # The manual provider's ledger IS NOT HERE
//
// The concrete store also reaches the fulfillment_manual_shipments table, but
// those methods have DELIBERATELY not been taken into this interface: the
// provider's internal state is not the module's data and the service must not
// be given the means to touch it. The boundary is not a comment, it is the type
// system.
//
// # Transaction boundary
//
// [Store.WithTx] runs the given function in a single database transaction and
// carries the transaction on the context the function receives. Every call
// inside the transaction must therefore be made with THE CONTEXT GIVEN TO THE
// FUNCTION; if the outer ctx is used, that call falls outside the transaction
// and atomicity is silently lost.
//
// [Store.LockFulfillment], [Store.LockShippingProfile] and
// [Store.LockShippingProfileShared] lock the row until the end of the
// transaction and may only be called inside [Store.WithTx].
type Store interface {
	// WithTx runs fn in a single transaction; if fn returns an error the
	// transaction is rolled back.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateShippingProfile records a new shipping profile.
	CreateShippingProfile(ctx context.Context, profile models.ShippingProfile) (models.ShippingProfile, error)
	// GetShippingProfile returns the profile by its identifier; NotFound if
	// absent.
	GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// LockShippingProfile reads the profile with a WRITE lock held until the end
	// of the transaction.
	LockShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error)
	// LockShippingProfileShared reads the profile with a SHARED lock held until
	// the end of the transaction.
	LockShippingProfileShared(ctx context.Context, id string) (models.ShippingProfile, error)
	// ListShippingProfiles filters and paginates the profiles; the second value
	// is the count of ALL rows matching the filter.
	ListShippingProfiles(ctx context.Context, filter models.ProfileFilter) ([]models.ShippingProfile, int64, error)
	// UpdateShippingProfile writes the profile's fields with ABSOLUTE values.
	UpdateShippingProfile(ctx context.Context, profile models.ShippingProfile) (models.ShippingProfile, error)
	// SoftDeleteShippingProfile soft-deletes the profile.
	SoftDeleteShippingProfile(ctx context.Context, id string) error
	// CountAliveOptionsByProfile counts the living options bound to the profile.
	CountAliveOptionsByProfile(ctx context.Context, profileID string) (int64, error)

	// CreateShippingOption records a new shipping option.
	CreateShippingOption(ctx context.Context, option models.ShippingOption) (models.ShippingOption, error)
	// GetShippingOption returns the option by its identifier; NotFound if
	// absent.
	GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error)
	// ListShippingOptions filters and paginates the options.
	ListShippingOptions(ctx context.Context, filter models.OptionFilter) ([]models.ShippingOption, int64, error)
	// ShippingOptionsByIDs fetches the identifier set in a SINGLE query (no
	// N+1).
	ShippingOptionsByIDs(ctx context.Context, ids []string) ([]models.ShippingOption, error)
	// ListEligibleShippingOptions returns the CANDIDATE options of a cart
	// context together with THEIR RULES.
	ListEligibleShippingOptions(ctx context.Context, filter models.EligibilityFilter) ([]models.ShippingOption, error)
	// UpdateShippingOption writes the option's fields with ABSOLUTE values.
	UpdateShippingOption(ctx context.Context, option models.ShippingOption) (models.ShippingOption, error)
	// SoftDeleteShippingOption soft-deletes the option.
	SoftDeleteShippingOption(ctx context.Context, id string) error

	// CreateShippingOptionRule records a new rule.
	CreateShippingOptionRule(ctx context.Context, rule models.ShippingOptionRule) (models.ShippingOptionRule, error)
	// GetShippingOptionRule returns the rule by its identifier; NotFound if
	// absent.
	GetShippingOptionRule(ctx context.Context, id string) (models.ShippingOptionRule, error)
	// ListShippingOptionRules returns an option's rules.
	ListShippingOptionRules(ctx context.Context, optionID string) ([]models.ShippingOptionRule, error)
	// SoftDeleteShippingOptionRule soft-deletes the rule.
	SoftDeleteShippingOptionRule(ctx context.Context, id string) error

	// InsertFulfillmentIfAbsent writes the fulfillment only if the idempotency
	// key has not been used yet. The second return value is whether the row was
	// written; a conflict IS NOT AN ERROR.
	InsertFulfillmentIfAbsent(ctx context.Context, ful models.Fulfillment) (models.Fulfillment, bool, error)
	// GetFulfillment returns the fulfillment by its identifier; NotFound if
	// absent.
	GetFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// FulfillmentByIdempotencyKey returns the fulfillment created with the same
	// key; NotFound if absent.
	FulfillmentByIdempotencyKey(ctx context.Context, key string) (models.Fulfillment, error)
	// LockFulfillment locks the fulfillment and returns its current form.
	LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error)
	// ListFulfillments filters and paginates the fulfillments.
	ListFulfillments(ctx context.Context, filter models.FulfillmentFilter) ([]models.Fulfillment, int64, error)
	// UpdateFulfillmentProviderResult writes the provider's response to the row.
	UpdateFulfillmentProviderResult(
		ctx context.Context,
		id, externalID string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
		data []byte,
		shippedAt, deliveredAt, canceledAt *time.Time,
	) (models.Fulfillment, error)
	// UpdateFulfillmentStatus writes the status, the tracking information and
	// the stamps with ABSOLUTE values.
	UpdateFulfillmentStatus(
		ctx context.Context,
		id string,
		status models.FulfillmentStatus,
		trackingNumber, trackingURL string,
		shippedAt, deliveredAt, canceledAt *time.Time,
	) (models.Fulfillment, error)

	// CreateFulfillmentItem adds an item to the fulfillment.
	CreateFulfillmentItem(ctx context.Context, item models.FulfillmentItem) (models.FulfillmentItem, error)
	// UpsertShippingLocation writes, or overwrites, the warehouse's shipping
	// PRIORITY; it does not touch the region links.
	UpsertShippingLocation(ctx context.Context, locationID string, priority int64) (models.ShippingLocation, error)
	// ReplaceShippingLocationRegions writes the warehouse's region links
	// WHOLESALE and may only be called inside [Store.WithTx]: it consists of two
	// statements, and a read in between would see the warehouse as region-less,
	// that is, open to all regions.
	ReplaceShippingLocationRegions(ctx context.Context, locationID string, regionIDs []string) error
	// GetShippingLocation returns the policy with its regions; NotFound if there
	// is no record.
	GetShippingLocation(ctx context.Context, locationID string) (models.ShippingLocation, error)
	// ListShippingLocations paginates the policies in priority order; the second
	// value is the count of ALL rows.
	ListShippingLocations(ctx context.Context, filter models.LocationFilter) ([]models.ShippingLocation, int64, error)
	// DeleteShippingLocation deletes the policy PERMANENTLY; NotFound if there
	// is no record. There is no soft delete; the rationale is at the top of the
	// migration.
	DeleteShippingLocation(ctx context.Context, locationID string) error
	// LocationPolicies returns the selection-time facts of the candidate
	// warehouses in a SINGLE query. The returned slice contains only the
	// candidates THAT HAVE A RECORD and may be shorter than the candidate list.
	//
	// The destination region IS NOT A PARAMETER: the matching is done not in the
	// database but in a pure function — so the rule can be exercised without a
	// real Postgres, and the regions the eliminated candidates are bound to can
	// be written into the error message.
	LocationPolicies(ctx context.Context, locationIDs []string) ([]models.LocationPolicy, error)

	// ListFulfillmentItems returns a fulfillment's items.
	ListFulfillmentItems(ctx context.Context, fulfillmentID string) ([]models.FulfillmentItem, error)
	// FulfillmentItemsByFulfillments returns the items for MULTIPLE fulfillments
	// in a SINGLE query (no N+1).
	FulfillmentItemsByFulfillments(ctx context.Context, fulfillmentIDs []string) ([]models.FulfillmentItem, error)
}

// Options are the service's construction dependencies.
type Options struct {
	// Store is the persistence surface; it is required.
	Store Store
	// Providers are the registered shipping providers; it is required.
	Providers *ProviderRegistry
	// Logger, if nil, causes the logs to be discarded.
	Logger *slog.Logger
	// Clock produces "now"; if nil, time.Now is used.
	//
	// Being injectable is for the tests: that a fulfillment's dispatch moment is
	// really written can be exercised exactly with a fixed clock.
	Clock func() time.Time
}

// Service is the fulfillment module's outward-facing service.
// It is safe for concurrent use.
type Service struct {
	store     Store
	providers *ProviderRegistry
	log       *slog.Logger
	clock     func() time.Time
}

// New produces a service with the given dependencies.
//
// A missing dependency is a construction error and is returned EXPLICITLY: a
// service built with a nil store would panic on the first request, and the
// error would surface long after construction.
func New(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.Internal(CodeNotReady,
			"the fulfillment service cannot be built without a store")
	}
	if opts.Providers == nil {
		return nil, errors.Internal(CodeNotReady,
			"the fulfillment service cannot be built without a provider registry")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: opts.Store, providers: opts.Providers, log: log, clock: clock}, nil
}

// ProviderIDs returns the identifiers of the registered shipping providers, in
// order.
//
// While opening an option, the admin surface learns from here which providers
// are installed; the provider object itself does NOT LEAK out — the only thing
// exposed is the identifier.
//
// ctx is unused; the reason it stands in the signature is that the provider
// registry may one day be fed from outside the PROCESS rather than merely from
// outside the module. Plugins already feed registries in-process, which needed
// no context; a remote registry would. Every service method in the project
// takes a context and the signature must not have to change on that day.
func (s *Service) ProviderIDs(_ context.Context) []string { return s.providers.IDs() }

// now returns a UTC moment from the service's clock.
func (s *Service) now() time.Time { return s.clock().UTC() }

// Page holds the pagination parameters of list requests.
type Page struct {
	// Limit is the maximum number of rows to return; if 0, [DefaultLimit] is
	// applied.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}

// normalize validates the pagination parameters and applies the defaults.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the limit cannot be negative: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "the offset cannot be negative: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"the limit can be at most %d: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}
