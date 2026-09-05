// Package service holds the business logic of the pricing module.
//
// # The cross-module surface (ADR 0001)
//
// pricing IMPORTS no module and READS data from no module; there is therefore no
// consumer-side interface in this package. The reverse direction does exist:
// product and (in Phase 5) cart need pricing. So that side can define a narrow
// interface in its own package, pricing's surface is SPLIT IN TWO:
//
//   - The rich in-module surface — it uses the [models] types
//     ([Service.CreatePriceSet], [Service.SetPrices], [Service.CalculatePrice] …).
//     Only pricing's own API layer and query provider call these methods.
//   - The cross-module surface — it uses ONLY primitive and stdlib types
//     ([Service.CreateEmptyPriceSet], [Service.SetBasePrices],
//     [Service.CalculateAmount]).
//
// The split is mandatory: structural conformance in Go demands signature
// EQUALITY. Because the consuming module cannot import pricing, it cannot name a
// type such as [models.PriceSet] in its signature; the moment it names one it
// becomes a different type in its own package and the concrete service does not
// satisfy the interface. Signatures written with primitive types, on the other
// hand, can be repeated verbatim in the consumer's own package:
//
//	// in the product module, WITHOUT importing pricing:
//	type PriceSetCreator interface {
//	    CreateEmptyPriceSet(ctx context.Context) (string, error)
//	}
//	creator, err := container.Resolve[PriceSetCreator](c, "pricing.service")
//
// # Money
//
// Amounts are INTEGER minor units and the currency is a separate field (plan
// Section 8). The service uses float nowhere and does no rounding.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "pricing_invalid_input"
	// CodeNotCalculable reports that no valid price was found in the given context.
	CodeNotCalculable = "price_not_calculable"
	// CodePriceSetNotFound reports that the requested price set was not found.
	CodePriceSetNotFound = "price_set_not_found"
)

// Paging limits. If no limit is given the default is applied, if an excessively
// large one is given the maximum is applied; a client cannot scan the database
// with a single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int32 = 50
	// MaxLimit is the maximum number of records a single request may return.
	MaxLimit int32 = 100
)

// Page is a paged list result.
//
// Limit and Offset are not the request's raw values but the APPLIED ones; the
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
// implementation is in the internal/modules/pricing/repository package. This is
// the IN-MODULE counterpart of ADR 0001's pattern and it lets the service be
// tested without a database.
type Repository interface {
	CreatePriceSet(ctx context.Context, id string, prices []models.Price, now time.Time) (models.PriceSet, error)
	GetPriceSet(ctx context.Context, id string) (models.PriceSet, error)
	ListPriceSets(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error)
	GetPriceSetsByIDs(ctx context.Context, ids []string) ([]models.PriceSet, error)
	DeletePriceSet(ctx context.Context, id string, now time.Time) error

	ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error)
	ListPriceCandidatesBySets(ctx context.Context, priceSetIDs []string) (map[string][]models.PriceCandidate, error)
	ListPriceCandidates(ctx context.Context, priceSetID string) ([]models.PriceCandidate, error)
	ReplacePrices(ctx context.Context, priceSetID string, prices []models.Price, now time.Time) ([]models.Price, error)
	GetPrice(ctx context.Context, id string) (models.Price, error)

	CreatePriceRule(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error)
	GetPriceRule(ctx context.Context, id string) (models.PriceRule, error)
	ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error)
	DeletePriceRule(ctx context.Context, id string, now time.Time) error

	CreatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	GetPriceList(ctx context.Context, id string) (models.PriceList, error)
	ListPriceLists(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error)
	UpdatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	DeletePriceList(ctx context.Context, id string, now time.Time) error
}

// Options are the service's setup settings.
type Options struct {
	// Logger is the structured log target; if nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; if nil time.Now is used. Tests fill this in with a
	// fixed clock to make the time-dependent branches (the price list window)
	// deterministic.
	Now func() time.Time
}

// Service is the public service of the pricing module. It is safe for concurrent
// use.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New produces a service working on the given repository.
//
// If repo is nil, this is reported as a typed error on the first call rather
// than at setup; the setup path produces no panic.
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
		return errors.Unavailable("pricing_service_unconfigured", "the pricing service is not configured")
	}
	return nil
}

// clock returns the current moment in UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// PriceInput is the write input of a single price.
type PriceInput struct {
	// CurrencyCode is the ISO 4217 code; the case is free, it is normalized to
	// UPPERCASE and stored that way.
	CurrencyCode string
	// Amount is the amount in minor units.
	Amount int64
	// MinQuantity is the lower quantity bound; if 0 is given it is taken as 1.
	MinQuantity int32
	// MaxQuantity is the upper quantity bound; if nil it is unbounded.
	MaxQuantity *int32
	// PriceListID binds the price to a campaign/segment list; if nil it is a base price.
	PriceListID *string
	// Rules are the price's validity conditions.
	Rules []RuleInput
}

// RuleInput is the write input of a single price rule.
type RuleInput struct {
	// Attribute is the name of the field to look at in the calculation context.
	Attribute string
	// Operator is the comparison operator.
	Operator models.RuleOperator
	// Values is the right-hand side of the comparison; it must hold at least one
	// element.
	Values []string
}

// CreatePriceSet creates a new price set and writes the given prices.
//
// prices may be left empty: a variant can first be created without prices and
// have its prices written later. If one of the prices is invalid NONE of them is
// written and the price set is not created either — this holds both when service
// validation eliminates the input and when the database rejects it (e.g. a price
// bound to a price list that does not exist): the container and its prices are
// written in a SINGLE transaction.
func (s *Service) CreatePriceSet(ctx context.Context, prices []PriceInput) (models.PriceSet, error) {
	if err := s.ready(); err != nil {
		return models.PriceSet{}, err
	}

	now := s.clock()
	// Validation is done BEFORE THE WRITE; for an invalid price the database is
	// not visited at all.
	toWrite, err := s.buildPrices("", prices, now)
	if err != nil {
		return models.PriceSet{}, err
	}

	return s.repo.CreatePriceSet(ctx, models.NewPriceSetID(now), toWrite, now)
}

// GetPriceSet returns the price set by id; errors.NotFound if there is none.
func (s *Service) GetPriceSet(ctx context.Context, id string) (models.PriceSet, error) {
	if err := s.ready(); err != nil {
		return models.PriceSet{}, err
	}
	if err := requireID(id, models.PriceSetIDPrefix, "price set id"); err != nil {
		return models.PriceSet{}, err
	}
	return s.repo.GetPriceSet(ctx, id)
}

// ListPriceSets returns the paged price set list.
func (s *Service) ListPriceSets(ctx context.Context, limit, offset int32) (Page[models.PriceSet], error) {
	if err := s.ready(); err != nil {
		return Page[models.PriceSet]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.PriceSet]{}, err
	}

	sets, total, err := s.repo.ListPriceSets(ctx, limit, offset)
	if err != nil {
		return Page[models.PriceSet]{}, err
	}
	return Page[models.PriceSet]{Items: sets, Count: total, Limit: limit, Offset: offset}, nil
}

// DeletePriceSet deletes the price set and its prices with a soft delete.
func (s *Service) DeletePriceSet(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceSetIDPrefix, "price set id"); err != nil {
		return err
	}
	return s.repo.DeletePriceSet(ctx, id, s.clock())
}

// SetPrices changes a price set's prices IN BULK.
//
// The operation is a replace, not an append: prices that were not given are
// deleted. The write is atomic — if one of the inputs is rejected by the
// database none of them is written and the container keeps its old prices.
//
// An empty slice is a valid request and removes all of the container's prices.
func (s *Service) SetPrices(ctx context.Context, priceSetID string, prices []PriceInput) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return nil, err
	}

	now := s.clock()
	toWrite, err := s.buildPrices(priceSetID, prices, now)
	if err != nil {
		return nil, err
	}

	written, err := s.repo.ReplacePrices(ctx, priceSetID, toWrite, now)
	if err != nil {
		return nil, err
	}

	// A replace is a destructive operation: how many prices were deleted and
	// how many were written is the only way to trace a wrong bulk call.
	// Amounts are not logged; the id and the count suffice (plan Section 8).
	s.log.DebugContext(ctx, "price set prices replaced",
		slog.String("price_set_id", priceSetID),
		slog.Int("price_count", len(written)),
	)
	return written, nil
}

// ListStorePrices returns the container's prices that may be shown TO THE
// CUSTOMER.
//
// What differs from [Service.ListPrices] is the filter: only prices belonging to
// published and unexpired lists, or base prices, are returned; rule-bound prices
// are never returned. Showing a rule-bound price to the customer is wrong in two
// ways — the price may not be valid for that customer, and the rule itself (e.g.
// the id of a customer group) is business information.
//
// The Rules field of the returned prices is EMPTIED: since the selection has
// already been made here, there is no need for the conditions to leave.
//
// The admin surface keeps using ListPrices; the operator MUST SEE draft
// campaigns and rule conditions.
func (s *Service) ListStorePrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
		return nil, err
	}

	candidates, err := s.repo.ListPriceCandidates(ctx, priceSetID)
	if err != nil {
		return nil, err
	}

	// The SAME filter as the Query provider's is used; the two customer surfaces
	// drifting apart would mean a price that leaks in one is not visible in the
	// other.
	prices := listablePrices(candidates, s.clock())
	for i := range prices {
		prices[i].Rules = nil
	}
	return prices, nil
}

// ListPrices returns a price set's prices together with their rules.
//
// It is for the ADMIN surface and applies NO filter: draft campaign prices and
// rule-bound prices are returned too. For the surface that goes to the customer
// [Service.ListStorePrices] must be used.
func (s *Service) ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceSetID, models.PriceSetIDPrefix, "price set id"); err != nil {
		return nil, err
	}
	// The container's existence is verified: if the prices of a container that
	// does not exist came back as an empty slice, the client would think "it has
	// no price" instead of a 404.
	if _, err := s.repo.GetPriceSet(ctx, priceSetID); err != nil {
		return nil, err
	}
	return s.repo.ListPrices(ctx, priceSetID)
}

// buildPrices validates the inputs and converts them into the domain models to
// be written.
//
// priceSetID may be given empty (when the container has not been created yet);
// the id is assigned by the repository at write time.
//
// The error carries which price was rejected under the [detailIndex] key; for
// rule-level errors [detailRuleIndex] is filled in as well and the two levels do
// NOT OVERWRITE each other.
func (s *Service) buildPrices(priceSetID string, inputs []PriceInput, now time.Time) ([]models.Price, error) {
	out := make([]models.Price, 0, len(inputs))
	for i, in := range inputs {
		price, err := buildPrice(priceSetID, in, now)
		if err != nil {
			return nil, withIndex(err, detailIndex, i)
		}
		out = append(out, price)
	}
	return out, nil
}

// buildPrice validates a single input and converts it into the model.
func buildPrice(priceSetID string, in PriceInput, now time.Time) (models.Price, error) {
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Price{}, err
	}
	if err := validateAmount(in.Amount); err != nil {
		return models.Price{}, err
	}
	minQty, maxQty, err := normalizeQuantityRange(in.MinQuantity, in.MaxQuantity)
	if err != nil {
		return models.Price{}, err
	}
	if err := validatePriceListRef(in.PriceListID); err != nil {
		return models.Price{}, err
	}

	priceID := models.NewPriceID(now)
	rules := make([]models.PriceRule, 0, len(in.Rules))
	for i, ruleIn := range in.Rules {
		rule, err := buildRule(priceID, ruleIn, now)
		if err != nil {
			return models.Price{}, withIndex(err, detailRuleIndex, i)
		}
		rules = append(rules, rule)
	}

	return models.Price{
		ID:           priceID,
		PriceSetID:   priceSetID,
		PriceListID:  in.PriceListID,
		CurrencyCode: currency,
		Amount:       in.Amount,
		MinQuantity:  minQty,
		MaxQuantity:  maxQty,
		Rules:        rules,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// buildRule validates a single rule input and converts it into the model.
func buildRule(priceID string, in RuleInput, now time.Time) (models.PriceRule, error) {
	if err := validateRule(in); err != nil {
		return models.PriceRule{}, err
	}
	return models.PriceRule{
		ID:        models.NewPriceRuleID(now),
		PriceID:   priceID,
		Attribute: in.Attribute,
		Operator:  in.Operator,
		Values:    append([]string(nil), in.Values...),
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}
