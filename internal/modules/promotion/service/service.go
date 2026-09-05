// Package service holds the business logic of the promotion module.
//
// # Cross-module surface (ADR 0001)
//
// promotion IMPORTS no module and READS data from no module; there is therefore no
// consumer-side interface in this package. The reverse direction exists: the cart
// flow (internal/workflows/cart) and the order completion saga need promotion. So
// that side can define a narrow interface in its own package, promotion's surface is
// split IN TWO:
//
//   - The rich in-module surface — it uses the [models] types
//     ([Service.CreatePromotion], [Service.ComputeDiscounts] …). These methods are
//     called only by promotion's own API layer, its query provider and its interop
//     surface.
//   - The cross-module surface — it uses ONLY primitive and stdlib types; it lives in
//     the interop.go file and is registered in the container under the name
//     "promotion.interop".
//
// The split is mandatory: structural conformance in Go demands signature EQUALITY.
// Because the consuming module cannot import promotion, it cannot name a type such as
// [models.Promotion] in its signature; the moment it names one it becomes a different
// type in its own package and the concrete service does not satisfy the interface.
//
// # Money and rates
//
// Amounts are INTEGER minor units and the currency is a separate field (plan
// Section 8). Rates are BASIS POINTS (2000 = 20%). The service uses float nowhere;
// the rounding direction is documented next to [models.BasisPointDenominator].
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "promotion_invalid_input"
	// CodeBuyGetNotActivatable reports that the buyget promotion cannot be activated
	// in this phase (see [models.PromotionBuyGet]).
	CodeBuyGetNotActivatable = "promotion_buyget_not_activatable"
	// CodePromotionNotUsable reports that the promotion is not in a state that can be
	// offered TO THE CUSTOMER; the store surface presents this as "not there".
	CodePromotionNotUsable = "promotion_not_usable"
	// CodeUnconfigured reports that the service has not been set up.
	CodeUnconfigured = "promotion_service_unconfigured"
)

// Paging bounds. If no limit is given the default is applied, if an excessively large
// one is given the maximum value is applied; a client cannot scan the database in a
// single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int32 = 50
	// MaxLimit is the maximum number of records a single request can return.
	MaxLimit int32 = 100
)

// Page is a paginated list result.
//
// Limit and Offset are not the raw values of the request but the APPLIED values; the
// API envelope writes these fields as they are, so the client learns about a clamped
// limit.
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
// The interface is defined on the CONSUMING side (here); the concrete implementation
// is in the internal/modules/promotion/repository package. This is the in-module
// counterpart of the pattern of ADR 0001, and it lets the service be tested without a
// database.
type Repository interface {
	CreateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error)
	GetCampaign(ctx context.Context, id string) (models.Campaign, error)
	GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error)
	ListCampaigns(ctx context.Context, limit, offset int32) ([]models.Campaign, int64, error)
	GetCampaignsByIDs(ctx context.Context, ids []string) ([]models.Campaign, error)
	UpdateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error)
	DeleteCampaign(ctx context.Context, id string, now time.Time) error

	CreatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error)
	GetPromotion(ctx context.Context, id string) (models.Promotion, error)
	GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error)
	ListPromotions(ctx context.Context, status, campaignID *string, limit, offset int32) ([]models.Promotion, int64, error)
	GetPromotionsByIDs(ctx context.Context, ids []string) ([]models.Promotion, error)
	UpdatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error)
	DeletePromotion(ctx context.Context, id string, now time.Time) error
	ListCandidates(ctx context.Context, codes []string) ([]models.PromotionCandidate, error)

	SetApplicationMethod(ctx context.Context, m models.ApplicationMethod, now time.Time) (models.ApplicationMethod, error)
	GetApplicationMethod(ctx context.Context, promotionID string) (models.ApplicationMethod, error)
	DeleteApplicationMethod(ctx context.Context, promotionID string, now time.Time) error

	CreatePromotionRule(ctx context.Context, rule models.PromotionRule, now time.Time) (models.PromotionRule, error)
	GetPromotionRule(ctx context.Context, id string) (models.PromotionRule, error)
	ListPromotionRules(ctx context.Context, promotionID string) ([]models.PromotionRule, error)
	DeletePromotionRule(ctx context.Context, id string, now time.Time) error

	Redeem(ctx context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error)
	Release(ctx context.Context, promotionID, reference string, now time.Time) (models.Redemption, bool, error)
	GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error)
	ListRedemptions(ctx context.Context, promotionID string, limit, offset int32) ([]models.Redemption, int64, error)
}

// Options are the setup settings of the service.
type Options struct {
	// Logger is the structured log target; if nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; if nil time.Now is used. Tests fill this in with a
	// fixed clock to make the time-dependent branches (the campaign window)
	// deterministic.
	Now func() time.Time
}

// Service is the public service of the promotion module. It is safe for concurrent
// use.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New produces a service running on the given repository.
//
// If repo is nil this is reported as a typed error not at setup time but on the first
// call; the setup path produces no panic.
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

// ready verifies that the repository has been set up.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "the promotion service has not been set up")
	}
	return nil
}

// clock returns the current moment in UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// PromotionInput is the write input of a promotion.
type PromotionInput struct {
	// Code is the coupon code; upper/lower case is free, it is normalized to
	// UPPERCASE for storage.
	Code string
	// IsAutomatic is whether the promotion will be applied without a code being
	// entered.
	IsAutomatic bool
	// Type is the mechanic of the promotion; if left empty "standard" is assumed.
	Type models.PromotionType
	// CampaignID binds the promotion to a campaign; if nil it has no campaign.
	CampaignID *string
	// Status is the publication status; if left empty "draft" is assumed.
	//
	// The default being draft is deliberate: an incompletely filled request must not
	// produce a discount that goes live by accident.
	Status models.PromotionStatus
	// UsageLimit is the usage bound; if nil it is unbounded.
	UsageLimit *int64
	// Metadata is the operator's free note; it does not enter business rules.
	Metadata map[string]string
}

// CreatePromotion creates a new promotion.
//
// The code is UNIQUE; the same code cannot be taken a second time and the attempt
// returns errors.Conflict (uniqueness is enforced by a partial database index, not by
// the service — between two concurrent requests only the database can be the
// referee).
func (s *Service) CreatePromotion(ctx context.Context, in PromotionInput) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}

	now := s.clock()
	promo, err := buildPromotion(models.NewPromotionID(now), in, now)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.CreatePromotion(ctx, promo, now)
}

// GetPromotion returns the promotion by identifier; if there is none,
// errors.NotFound.
func (s *Service) GetPromotion(ctx context.Context, id string) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Promotion{}, err
	}
	return s.repo.GetPromotion(ctx, id)
}

// GetPromotionByCode returns the promotion by coupon code; if there is none,
// errors.NotFound.
//
// It is meant for the ADMIN surface and applies NO filter: draft and inactive
// promotions are returned too. For the surface that goes to the customer,
// [Service.LookupStoreCoupon] must be used.
func (s *Service) GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	normalized, err := normalizeCode(code)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.GetPromotionByCode(ctx, normalized)
}

// ListPromotionsInput holds the optional filters of the promotion listing.
type ListPromotionsInput struct {
	// Status returns only the promotions in this status; if nil no filtering is done.
	Status *models.PromotionStatus
	// CampaignID returns only the promotions bound to this campaign; if nil no
	// filtering is done.
	CampaignID *string
	// Limit is the page size; if 0 then [DefaultLimit] is applied.
	Limit int32
	// Offset is the number of records to skip.
	Offset int32
}

// ListPromotions returns the paginated promotion list.
func (s *Service) ListPromotions(ctx context.Context, in ListPromotionsInput) (Page[models.Promotion], error) {
	if err := s.ready(); err != nil {
		return Page[models.Promotion]{}, err
	}
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Promotion]{}, err
	}

	var status *string
	if in.Status != nil {
		if !in.Status.Valid() {
			return Page[models.Promotion]{}, errors.Invalid(CodeInvalidInput,
				"promotion status is undefined: %q", string(*in.Status))
		}
		value := string(*in.Status)
		status = &value
	}
	if in.CampaignID != nil {
		if err := requireID(*in.CampaignID, models.CampaignIDPrefix, "campaign id"); err != nil {
			return Page[models.Promotion]{}, err
		}
	}

	items, total, err := s.repo.ListPromotions(ctx, status, in.CampaignID, limit, offset)
	if err != nil {
		return Page[models.Promotion]{}, err
	}
	return Page[models.Promotion]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdatePromotion REPLACES the definition of the promotion.
//
// It is not a partial update: the fields that are not given are reset. The reason is
// that a partial update leaves the distinction between "the field was not sent" and
// "the field is meant to be emptied" up to the client; removing a promotion's
// campaign can also be a request and must not be silently ignored.
//
// The usage counter DOES NOT CHANGE through this path.
func (s *Service) UpdatePromotion(ctx context.Context, id string, in PromotionInput) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Promotion{}, err
	}

	now := s.clock()
	promo, err := buildPromotion(id, in, now)
	if err != nil {
		return models.Promotion{}, err
	}
	return s.repo.UpdatePromotion(ctx, promo, now)
}

// DeletePromotion deletes the promotion with a soft delete.
func (s *Service) DeletePromotion(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
		return err
	}
	return s.repo.DeletePromotion(ctx, id, s.clock())
}

// buildPromotion validates the input and turns it into the domain model to be
// written.
func buildPromotion(id string, in PromotionInput, now time.Time) (models.Promotion, error) {
	code, err := normalizeCode(in.Code)
	if err != nil {
		return models.Promotion{}, err
	}

	promoType := in.Type
	if promoType == "" {
		promoType = models.PromotionStandard
	}
	if !promoType.Valid() {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"promotion type is undefined: %q", string(in.Type))
	}

	status := in.Status
	if status == "" {
		status = models.PromotionDraft
	}
	if !status.Valid() {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"promotion status is undefined: %q", string(in.Status))
	}

	// The buyget mechanic DOES NOT EXIST in this phase and, so as not to leave the gap
	// silent, the type is closed structurally (see [models.PromotionBuyGet]). It can
	// be prepared as draft or as inactive; it goes live only when the mechanic
	// arrives.
	if promoType == models.PromotionBuyGet && status == models.PromotionActive {
		return models.Promotion{}, errors.Invalid(CodeBuyGetNotActivatable,
			"the buyget promotion cannot be activated in this release; the mechanic is not implemented yet (code: %s)", code)
	}

	if in.CampaignID != nil {
		if err := requireID(*in.CampaignID, models.CampaignIDPrefix, "campaign id"); err != nil {
			return models.Promotion{}, err
		}
	}
	if err := validateUsageLimit(in.UsageLimit); err != nil {
		return models.Promotion{}, err
	}
	metadata, err := normalizeMetadata(in.Metadata)
	if err != nil {
		return models.Promotion{}, err
	}

	return models.Promotion{
		ID:          id,
		Code:        code,
		IsAutomatic: in.IsAutomatic,
		Type:        promoType,
		CampaignID:  copyString(in.CampaignID),
		Status:      status,
		UsageLimit:  copyInt64(in.UsageLimit),
		Metadata:    metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// copyString returns a string pointer BY COPYING it.
//
// The copy is essential: had the caller's pointer been placed directly into the
// model, a caller that modified the request object afterwards would have modified the
// written record as well.
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// copyInt64 returns an integer pointer BY COPYING it; the rationale is the same as in
// copyString.
func copyInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
