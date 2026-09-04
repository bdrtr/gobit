// Package service holds the region module's business logic.
//
// # Cross-module surface (ADR 0001)
//
// region imports NO module and READS data from no module; that is why there is
// no consumer-side interface in this package. The reverse direction exists:
// cart (Phase 5), order (Phase 6) and tax (Phase 7) need region. So that that
// side can define a narrow interface in its own package, region's surface is
// split IN TWO:
//
//   - The rich in-module surface — it uses the [models] types
//     ([Service.CreateRegion], [Service.ResolveRegionForCountry] …). Only
//     region's own API layer and its query provider call these methods.
//   - The cross-module surface — it uses ONLY primitive and stdlib types
//     (see interop.go: [Service.RegionCurrency], [Service.RegionTax],
//     [Service.RegionIDForCountry], [Service.CurrencyDecimalDigits]).
//
// The split is compulsory: in Go, structural conformance demands signature
// EQUALITY. Since the consuming module cannot import region, it cannot name a
// type such as [models.Region] in its signature; the moment it names one, it
// becomes a different type in its own package and the concrete service does not
// satisfy the interface.
//
// # Money
//
// This module carries no AMOUNT; it carries the currency DEFINITION. Cart
// amounts are minor unit integers (plan Section 8) and the presentation factor
// of that integer comes from [models.Currency.DecimalDigits]. The tax rate is
// an integer too (basis points); the service uses a float nowhere.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "region_invalid_input"
	// CodeRegionNotFound reports that the requested region was not found.
	CodeRegionNotFound = "region_not_found"
	// CodeCountryUnassigned reports that the country is attached to no region.
	CodeCountryUnassigned = "country_has_no_region"
	// CodeCountryRegionMissing reports that the region the country is attached
	// to was not found (it arises if the region was deleted and the country was
	// not released).
	CodeCountryRegionMissing = "country_region_missing"
	// CodeDecorateFailed reports that the storefront view could not be built;
	// it is an indicator of internal inconsistency and does not arise in the
	// normal flow.
	CodeDecorateFailed = "region_decorate_failed"
)

// Paging bounds. If no limit is given the default is applied, if an excessively
// large one is given the maximum value is applied; a client cannot scan the
// database in a single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int32 = 50
	// MaxLimit is the maximum number of records that can come back in a single
	// request.
	//
	// It is deliberately generous for the country list: there are 249 countries
	// in ISO 3166 and an admin screen is expected to be able to take the whole
	// of it in two or three pages.
	MaxLimit int32 = 250
)

// Page is a paginated list result.
//
// Limit and Offset are not the request's raw values but the APPLIED values; the
// API envelope writes these fields as they are, so the client learns about a
// limit that was clamped.
type Page[T any] struct {
	// Items are the records on the current page.
	Items []T
	// Count is the TOTAL number of records matching the filter (not the page size).
	Count int64
	// Limit is the applied page size.
	Limit int32
	// Offset is the applied number of skipped records.
	Offset int32
}

// Repository is the data access surface the service needs.
//
// The interface is defined on the CONSUMING side (here); the concrete
// implementation is in the internal/modules/region/repository package. This is
// the IN-module counterpart of ADR 0001's pattern and it makes it possible to
// test the service without a database.
type Repository interface {
	CreateRegion(ctx context.Context, region models.Region, now time.Time) (models.Region, error)
	GetRegion(ctx context.Context, id string) (models.Region, error)
	ListRegions(ctx context.Context, limit, offset int32) ([]models.Region, int64, error)
	GetRegionsByIDs(ctx context.Context, ids []string) ([]models.Region, error)
	UpdateRegion(ctx context.Context, id string, patch models.RegionPatch, now time.Time) (models.Region, error)
	DeleteRegion(ctx context.Context, id string, now time.Time) error
	GetRegionByCountry(ctx context.Context, countryCode string) (models.Region, error)

	AssignCountry(ctx context.Context, regionID, countryCode string, now time.Time) (models.Country, error)
	UnassignCountry(ctx context.Context, regionID, countryCode string, now time.Time) error
	GetCountry(ctx context.Context, code string) (models.Country, error)
	ListCountries(ctx context.Context, regionID *string, limit, offset int32) ([]models.Country, int64, error)
	ListCountriesByRegions(ctx context.Context, regionIDs []string) (map[string][]models.Country, error)

	GetCurrency(ctx context.Context, code string) (models.Currency, error)
	ListCurrencies(ctx context.Context, limit, offset int32) ([]models.Currency, int64, error)
	GetCurrenciesByCodes(ctx context.Context, codes []string) ([]models.Currency, error)
}

// Options are the service's setup settings.
type Options struct {
	// Logger is the structured log target; if nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; if nil time.Now is used. Tests fill this in with
	// a fixed clock and so make the time-dependent fields deterministic.
	Now func() time.Time
}

// Service is the region module's public service. It is safe for concurrent use.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New produces a service that works over the given repository.
//
// If repo is nil this is reported as a typed error not at setup but on the
// first call; the setup path produces no panic.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, log: log, now: now}
}

// ready verifies that the repository is configured.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable("region_service_unconfigured", "the region service is not configured")
	}
	return nil
}

// clock returns the current moment as UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// CreateRegionInput is the write input of a new region.
type CreateRegionInput struct {
	// Name is the region's display name; it is required.
	Name string
	// CurrencyCode is the ISO 4217 code; upper/lower case is free, it is stored
	// normalized to UPPER case. It is required.
	CurrencyCode string
	// AutomaticTaxes states whether the tax is applied automatically.
	AutomaticTaxes bool
	// TaxRate is the region's FALLBACK tax rate (basis points; 2000 = 20%).
	//
	// In Phase 7 the tax module took over the tax calculation; this field
	// remained as the FALLBACK path of the cart flow (see [Service.RegionTax]).
	TaxRate int32
}

// CreateRegion creates a new region.
//
// If the currency code is formally invalid it returns errors.Invalid and the
// database is not visited at all. A formally valid but UNDEFINED code is
// rejected with errors.Invalid as well; that check is in the foreign key in the
// database (see repository.CreateRegion).
func (s *Service) CreateRegion(ctx context.Context, in CreateRegionInput) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}

	name, err := normalizeName(in.Name)
	if err != nil {
		return models.Region{}, err
	}
	currency, err := NormalizeCurrencyCode(in.CurrencyCode)
	if err != nil {
		return models.Region{}, err
	}
	if err := validateTaxRate(in.TaxRate); err != nil {
		return models.Region{}, err
	}

	now := s.clock()
	return s.repo.CreateRegion(ctx, models.Region{
		ID:             models.NewRegionID(now),
		Name:           name,
		CurrencyCode:   currency,
		AutomaticTaxes: in.AutomaticTaxes,
		TaxRate:        in.TaxRate,
	}, now)
}

// GetRegion returns the region by id; errors.NotFound if there is none.
func (s *Service) GetRegion(ctx context.Context, id string) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	if err := requireRegionID(id); err != nil {
		return models.Region{}, err
	}
	return s.repo.GetRegion(ctx, id)
}

// ListRegions returns the paginated region list.
func (s *Service) ListRegions(ctx context.Context, limit, offset int32) (Page[models.Region], error) {
	if err := s.ready(); err != nil {
		return Page[models.Region]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Region]{}, err
	}

	regions, total, err := s.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return Page[models.Region]{}, err
	}
	return Page[models.Region]{Items: regions, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateRegionInput is the PARTIAL update input of a region.
//
// A nil field means "do not touch". Had the whole body been demanded, a client
// that forgets to send tax_rate in its body would silently zero the rate.
type UpdateRegionInput struct {
	// Name is the new name; if nil the name does not change.
	Name *string
	// CurrencyCode is the new currency code; if nil the currency does not change.
	CurrencyCode *string
	// AutomaticTaxes states whether the tax is applied automatically; if nil it
	// does not change.
	AutomaticTaxes *bool
	// TaxRate is the new tax rate (basis points); if nil the rate does not change.
	TaxRate *int32
}

// UpdateRegion updates the given fields of the region.
//
// If no field at all is given it returns errors.Invalid: an empty patch is the
// most likely indication that the client misspelled the name of the field it
// thought it was sending, and returning success silently would hide that
// mistake.
func (s *Service) UpdateRegion(ctx context.Context, id string, in UpdateRegionInput) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	if err := requireRegionID(id); err != nil {
		return models.Region{}, err
	}

	patch, err := buildRegionPatch(in)
	if err != nil {
		return models.Region{}, err
	}
	if patch.Empty() {
		return models.Region{}, errors.Invalid(CodeInvalidInput,
			"no field was given to update")
	}

	return s.repo.UpdateRegion(ctx, id, patch, s.clock())
}

// buildRegionPatch validates the update input and turns it into a patch.
//
// The validation is applied only to the FILLED fields: the current value of a
// field that is not touched must not drop the update even if it violates a rule
// that is not in force today.
func buildRegionPatch(in UpdateRegionInput) (models.RegionPatch, error) {
	var patch models.RegionPatch

	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return models.RegionPatch{}, err
		}
		patch.Name = &name
	}
	if in.CurrencyCode != nil {
		currency, err := NormalizeCurrencyCode(*in.CurrencyCode)
		if err != nil {
			return models.RegionPatch{}, err
		}
		patch.CurrencyCode = &currency
	}
	if in.AutomaticTaxes != nil {
		automatic := *in.AutomaticTaxes
		patch.AutomaticTaxes = &automatic
	}
	if in.TaxRate != nil {
		if err := validateTaxRate(*in.TaxRate); err != nil {
			return models.RegionPatch{}, err
		}
		rate := *in.TaxRate
		patch.TaxRate = &rate
	}
	return patch, nil
}

// DeleteRegion deletes the region with a soft delete and releases its countries.
func (s *Service) DeleteRegion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireRegionID(id); err != nil {
		return err
	}

	if err := s.repo.DeleteRegion(ctx, id, s.clock()); err != nil {
		return err
	}

	// The deletion leaves the currency of every cart that falls into that
	// region unresolved and releases its countries; it has to be traceable.
	s.log.InfoContext(ctx, "region deleted", slog.String("region_id", id))
	return nil
}
