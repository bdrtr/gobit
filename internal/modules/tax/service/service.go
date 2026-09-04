// Package service holds the tax module's business logic.
//
// # The work the module takes over
//
// In Phase 5 tax stood TEMPORARILY in the region module: the region row carried
// a single tax_rate (basis points) and an automatic_taxes flag, and the cart
// flow read them. region's godoc had marked this explicitly as "the tax module
// will take it over in Phase 7". This module performs that takeover: it is the
// SOLE write authority for tax region, rate and rule data (Principle 2.3).
//
// tax DOES NOT IMPORT region and does not see its table (ADR 0001). The only
// common ground is the ISO 3166-1 country code: the cart flow comes here with
// the country code it holds and this module resolves the region out of its own
// table.
//
// # The in-module and the cross-module surface
//
// The surface is split IN TWO (the very same pattern as in region):
//
//   - The rich in-module surface — it uses the [models] types
//     ([Service.CreateTaxRegion], [Service.CalculateTax] …). Only tax's own API
//     layer and query provider call these.
//   - The cross-module surface — it uses ONLY primitive and stdlib types
//     (see interop.go: [Interop.CalculateTaxJSON], [Interop.RateForCountry]).
//
// The separation is mandatory: structural conformance in Go demands signature
// EQUALITY. Because the consuming module cannot import tax, it cannot name a
// type such as [models.TaxRegion] in its signature; the moment it names one it
// becomes a different type in its own package and the concrete service does not
// satisfy the interface.
//
// # The provider abstraction is NOT IN THE CORE, it is HERE
//
// Plan Section 6 says "TaxProvider", but there is NO tax provider in
// internal/core/provider (there are only Payment and Fulfillment) and this
// module may not touch the core. That is why the contract is defined in this
// package ([TaxProvider], see taxprovider.go) and the out-of-the-box
// implementation is local calculation ([LocalProvider]).
//
// THE DECISION IS EXPLICITLY TEMPORARY: once the contract matures (once a
// second real provider is written) it must be moved to
// internal/core/provider/tax.go. During the move the types in this package can
// be turned into aliases of the ones in the core; the signatures were written
// with that in mind, in the same shape as the core's PaymentProvider and
// FulfillmentProvider (ID() string + a single work method, input/output
// structs).
//
// # Money and the rate
//
// A tax rate is a BASIS POINT and an INTEGER (2000 = 20%). Amounts are minor
// unit integers. Floating point is used nowhere in this package (plan
// Section 8). The rounding direction and where it is done are documented in the
// [Service.CalculateTax] godoc.
package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "tax_invalid_input"
	// CodeUnconfigured reports that the service was not set up.
	CodeUnconfigured = "tax_service_unconfigured"
	// CodeRegionNotFound reports that the requested tax region was not found.
	CodeRegionNotFound = "tax_region_not_found"
	// CodeParentInvalid reports that a province region's root is invalid.
	CodeParentInvalid = "tax_parent_invalid"
	// CodeRootExists reports that the country already has a root tax region.
	CodeRootExists = "tax_region_root_exists"
	// CodeDefaultExists reports that the region already has a default rate.
	CodeDefaultExists = "tax_default_rate_exists"
	// CodeRateOutOfRange reports that a rate is outside the contract.
	CodeRateOutOfRange = "tax_rate_out_of_range"
	// CodeAmountOverflow reports that an amount exceeded the permitted range.
	CodeAmountOverflow = "tax_amount_overflow"
	// CodeProviderExists reports that a provider with the same id is already
	// registered.
	CodeProviderExists = "tax_provider_exists"
	// CodeProviderNotFound reports that the requested provider is not
	// registered.
	CodeProviderNotFound = "tax_provider_not_found"
	// CodeProviderMisconfigured reports that the region points at a provider
	// that is NOT registered; it is a setup error.
	CodeProviderMisconfigured = "tax_provider_misconfigured"
	// CodeProviderInvalidResult reports that the provider returned a result
	// outside the contract.
	CodeProviderInvalidResult = "tax_provider_invalid_result"
)

// Paging bounds. When no limit is given the default applies, when an
// excessively large one is given the maximum applies; a client cannot scan the
// database with a single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int32 = 50
	// MaxLimit is the maximum number of records a single request can return.
	MaxLimit int32 = 200
)

// Page is a paginated list result.
//
// Limit and Offset are not the request's raw values but the APPLIED ones; the
// API envelope writes these fields as they are, so that the client learns of a
// clamped limit.
type Page[T any] struct {
	// Items are the records on the current page.
	Items []T
	// Count is the TOTAL number of records matching the filter (not the page
	// size).
	Count int64
	// Limit is the applied page size.
	Limit int32
	// Offset is the applied skip count.
	Offset int32
}

// Repository is the data access surface the service needs.
//
// The interface is defined on the CONSUMING side (here); the concrete
// implementation is in the internal/modules/tax/repository package. This is the
// IN-MODULE counterpart of ADR 0001's pattern and it makes the service testable
// without a database.
type Repository interface {
	CreateTaxRegion(ctx context.Context, region models.TaxRegion, now time.Time) (models.TaxRegion, error)
	GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error)
	GetTaxRegionsByIDs(ctx context.Context, ids []string) ([]models.TaxRegion, error)
	ListTaxRegions(ctx context.Context, countryCode string, limit, offset int32) ([]models.TaxRegion, int64, error)
	ResolveTaxRegions(ctx context.Context, countryCode, provinceCode string) ([]models.TaxRegion, error)
	DeleteTaxRegion(ctx context.Context, id string, now time.Time) error

	CreateTaxRate(ctx context.Context, rate models.TaxRate, now time.Time) (models.TaxRate, error)
	GetTaxRate(ctx context.Context, id string) (models.TaxRate, error)
	ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error)
	ListTaxRatesByRegions(ctx context.Context, regionIDs []string) ([]models.TaxRate, error)
	UpdateTaxRate(ctx context.Context, id string, patch models.TaxRatePatch, now time.Time) (models.TaxRate, error)
	DeleteTaxRate(ctx context.Context, id string, now time.Time) error

	CreateTaxRateRule(ctx context.Context, rule models.TaxRateRule, now time.Time) (models.TaxRateRule, error)
	GetTaxRateRule(ctx context.Context, id string) (models.TaxRateRule, error)
	ListTaxRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error)
	ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error)
	DeleteTaxRateRule(ctx context.Context, id string, now time.Time) error
}

// Options are the service's setup settings.
type Options struct {
	// Logger is the structured log target; when nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; when nil time.Now is used. Tests fill this in
	// with a fixed clock to make time-dependent fields deterministic.
	Now func() time.Time
	// Providers is the registry of tax providers; when nil a registry
	// containing only local calculation is set up.
	Providers *ProviderRegistry
}

// Service is the tax module's public service. It is safe for concurrent use.
type Service struct {
	repo      Repository
	providers *ProviderRegistry
	log       *slog.Logger
	now       func() time.Time
}

// New produces a service that works on the given repository.
//
// When repo is nil this is reported as a typed error not at setup but on the
// first call; the setup path produces no panic.
//
// When no provider registry is given, the local calculation provider
// ([LocalProvider]) is set up on its own and the repository becomes its rate
// source. Having no registry at all would mean every region whose tax is
// configured blowing up on its first calculation.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	providers := opts.Providers
	if providers == nil {
		providers = NewProviderRegistry()
		if repo != nil {
			// The error can be ignored: in a freshly set up, empty registry no
			// second provider with the same id can be found, so a collision is
			// impossible.
			_ = providers.Register(NewLocalProvider(repo))
		}
	}

	return &Service{repo: repo, providers: providers, log: log, now: now}
}

// ready verifies that the repository is set up.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "the tax service is not configured")
	}
	return nil
}

// clock returns the current instant in UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// Providers returns the service's provider registry.
//
// The module registers this in the container separately, so that Phase 9's
// plugin system can add its own tax provider without touching the core or this
// module.
func (s *Service) Providers() *ProviderRegistry {
	if s == nil {
		return nil
	}
	return s.providers
}

// CreateTaxRegionInput is the write input for a new tax region.
type CreateTaxRegionInput struct {
	// CountryCode is the ISO 3166-1 alpha-2 code; case is free, it is stored
	// normalized to UPPER case. It is required.
	CountryCode string
	// ProvinceCode is the province/state code. Left empty, a COUNTRY ROOT is
	// created; given a value, [CreateTaxRegionInput.ParentID] is required too.
	ProvinceCode string
	// ParentID is the country root the province region will be attached to. It
	// must be left empty while creating a country root.
	ParentID string
	// ProviderID is the tax provider's id and it must be REGISTERED.
	//
	// It may be left empty: on a province region it means "inherit the
	// country's provider", on a root region it means "local calculation" (see
	// [Service.providerFor]). It is stored with leading/trailing whitespace
	// trimmed.
	ProviderID string
	// Metadata is free-form metadata.
	Metadata map[string]any
}

// CreateTaxRegion creates a new tax region.
//
// # Two shapes
//
// The input defines either a COUNTRY ROOT (province code and parent empty) or a
// PROVINCE (both filled). Half shapes are rejected: a province without a parent
// is never found — the resolution path always starts from the country — while a
// root carrying a province code applies a rate to a single province instead of
// to the whole country.
//
// # A second root for a country
//
// Rejected (errors.Conflict, code [CodeRootExists]). The service checks this by
// READING first, but the last line of defense is the partial unique index in
// the database: two concurrent requests can pass the "read first, then write"
// check together, and from that moment on which rate got applied would be left
// to row order.
//
// # The parent check
//
// The parent must exist, must be a ROOT and must belong to the SAME country.
// All three are checked here with a readable error; the composite foreign key
// in the database (parent_id, country_code) gives the same guarantee a second
// time.
//
// # The provider is validated BEFORE THE WRITE
//
// When [CreateTaxRegionInput.ProviderID] is given a value it is checked to be
// registered, and when it is not errors.Invalid is returned
// ([CodeProviderNotFound]); the rationale is in the
// [Service.normalizeProviderID] godoc. An empty value is free and means
// inheritance (see [Service.providerFor]).
func (s *Service) CreateTaxRegion(ctx context.Context, in CreateTaxRegionInput) (models.TaxRegion, error) {
	if err := s.ready(); err != nil {
		return models.TaxRegion{}, err
	}

	country, err := NormalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.TaxRegion{}, err
	}
	province, err := NormalizeProvinceCode(in.ProvinceCode)
	if err != nil {
		return models.TaxRegion{}, err
	}
	providerID, err := s.normalizeProviderID(in.ProviderID)
	if err != nil {
		return models.TaxRegion{}, err
	}

	region := models.TaxRegion{
		CountryCode: country,
		ProviderID:  providerID,
		Metadata:    in.Metadata,
	}

	switch {
	case province == "" && in.ParentID == "":
		if err := s.assertNoRoot(ctx, country); err != nil {
			return models.TaxRegion{}, err
		}
	case province != "" && in.ParentID != "":
		parent, err := s.parentForProvince(ctx, in.ParentID, country)
		if err != nil {
			return models.TaxRegion{}, err
		}
		region.ParentID = &parent.ID
		region.ProvinceCode = &province
	case province == "":
		return models.TaxRegion{}, errors.Invalid(CodeInvalidInput,
			"when a parent is given the province code is required too; a country root is created without a parent")
	default:
		return models.TaxRegion{}, errors.Invalid(CodeInvalidInput,
			"the parent (country root) id is required for a province region")
	}

	// The clock is read ONCE: the id's timestamp diverging from created_at
	// means a list ordered by id not matching creation order.
	now := s.clock()
	region.ID = models.NewTaxRegionID(now)
	return s.repo.CreateTaxRegion(ctx, region, now)
}

// assertNoRoot verifies that the country does not have a root region yet.
func (s *Service) assertNoRoot(ctx context.Context, country string) error {
	existing, err := s.repo.ResolveTaxRegions(ctx, country, "")
	if err != nil {
		return err
	}
	for i := range existing {
		if existing[i].IsRoot() {
			return errors.Conflict(CodeRootExists,
				"the root tax region of country %s already exists: %s", country, existing[i].ID)
		}
	}
	return nil
}

// parentForProvince reads and validates the root the province region will be
// attached to.
func (s *Service) parentForProvince(ctx context.Context, parentID, country string) (models.TaxRegion, error) {
	if err := requireID(parentID, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return models.TaxRegion{}, err
	}

	parent, err := s.repo.GetTaxRegion(ctx, parentID)
	if err != nil {
		return models.TaxRegion{}, err
	}
	if !parent.IsRoot() {
		return models.TaxRegion{}, errors.Invalid(CodeParentInvalid,
			"%s is a province region; the tax hierarchy is two levels and no region can be opened under a province",
			parent.ID)
	}
	if parent.CountryCode != country {
		return models.TaxRegion{}, errors.Invalid(CodeParentInvalid,
			"the province region's country (%s) cannot differ from the root's country (%s)",
			country, parent.CountryCode)
	}
	return parent, nil
}

// normalizeProviderID trims the provider id and verifies that it is REGISTERED.
//
// # Why before the write
//
// The price of an unvalidated id is DELAYED and large: a typo comes out not at
// write time but at the FIRST tax calculation in that country, and there it
// becomes [CodeProviderMisconfigured] + KindInternal (500). Because the cart
// total calls that calculation on every round, a single administrator typo
// would close every cart in that country and the error would only be seen while
// a customer was adding a product to the cart. The pattern in the sibling
// module is the same too: payment/service/session.go resolves the provider from
// the registry BEFORE writing the session.
//
// The registry error is turned into errors.Invalid: an id that is not
// registered is an error in the writer's INPUT, not the server's.
//
// # Why it is trimmed and bounded
//
// The registry lookup does its search by trimming the id
// ([ProviderRegistry.Get]); had an untrimmed value been stored, "stored" and
// "applied" would diverge. The length bound is the same as the module's other
// ids (maxIDLen): an unbounded text field is the cheapest way to write
// megabytes of data into a table with a single request.
func (s *Service) normalizeProviderID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", nil
	}
	if len(trimmed) > maxIDLen {
		return "", errors.Invalid(CodeInvalidInput,
			"a tax provider id can be at most %d bytes, %d bytes were given",
			maxIDLen, len(trimmed))
	}
	if s.providers == nil {
		return "", errors.Internal(CodeProviderMisconfigured, "the tax provider registry is not configured")
	}
	if _, err := s.providers.Get(trimmed); err != nil {
		return "", errors.Wrap(err, errors.KindInvalid, CodeProviderNotFound,
			"the %q tax provider is not registered in this setup", trimmed)
	}
	return trimmed, nil
}

// GetTaxRegion returns the region by id; errors.NotFound when there is none.
func (s *Service) GetTaxRegion(ctx context.Context, id string) (models.TaxRegion, error) {
	if err := s.ready(); err != nil {
		return models.TaxRegion{}, err
	}
	if err := requireID(id, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return models.TaxRegion{}, err
	}
	return s.repo.GetTaxRegion(ctx, id)
}

// ListTaxRegions returns the paginated region list.
//
// When countryCode is left empty no filter is applied; when given a value its
// shape is validated and it is normalized to UPPER case. Silently turning a
// malformed code into "no filter" would return a far wider list than the client
// asked for.
func (s *Service) ListTaxRegions(ctx context.Context, countryCode string, limit, offset int32) (Page[models.TaxRegion], error) {
	if err := s.ready(); err != nil {
		return Page[models.TaxRegion]{}, err
	}

	filter := ""
	if countryCode != "" {
		normalized, err := NormalizeCountryCode(countryCode)
		if err != nil {
			return Page[models.TaxRegion]{}, err
		}
		filter = normalized
	}

	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.TaxRegion]{}, err
	}

	regions, total, err := s.repo.ListTaxRegions(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.TaxRegion]{}, err
	}
	return Page[models.TaxRegion]{Items: regions, Count: total, Limit: limit, Offset: offset}, nil
}

// DeleteTaxRegion soft deletes the region, its sub-regions and its rates.
//
// The deletion covers the TREE; the rationale is in the
// repository.DeleteTaxRegion godoc. A deleted region drops the tax of every
// cart in that country to zero, which is why its trace is logged.
func (s *Service) DeleteTaxRegion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return err
	}

	if err := s.repo.DeleteTaxRegion(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "tax region deleted", slog.String("tax_region_id", id))
	return nil
}
