package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// PriceListInput is the write input of a price list.
type PriceListInput struct {
	// Title is the list's display name; it is required.
	Title string
	// Description is the optional description.
	Description string
	// Type is the list's type (sale | override); it is required.
	Type models.PriceListType
	// Status is the list's status; if left empty it is taken as draft.
	//
	// The default being draft is deliberate: a status left out by mistake must
	// not PUBLISH the campaign unintentionally.
	Status models.PriceListStatus
	// StartsAt is the start of the validity window; if nil there is no lower
	// bound.
	StartsAt *time.Time
	// EndsAt is the end of the validity window; if nil there is no upper bound.
	EndsAt *time.Time
	// Metadata is the caller's free-form data; this module never reads it.
	//
	// It is REPLACED on an update rather than merged, like every other field of
	// this input: the write is a whole record, and a merge would leave no way to
	// remove a key.
	Metadata map[string]any
}

// CreatePriceList creates a new price list.
func (s *Service) CreatePriceList(ctx context.Context, in PriceListInput) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}

	list, err := buildPriceList(in)
	if err != nil {
		return models.PriceList{}, err
	}

	now := s.clock()
	list.ID = models.NewPriceListID(now)
	return s.repo.CreatePriceList(ctx, list, now)
}

// GetPriceList returns the list by id; errors.NotFound if there is none.
func (s *Service) GetPriceList(ctx context.Context, id string) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "price list id"); err != nil {
		return models.PriceList{}, err
	}
	return s.repo.GetPriceList(ctx, id)
}

// ListPriceLists returns the paged set of price lists.
func (s *Service) ListPriceLists(ctx context.Context, limit, offset int32) (Page[models.PriceList], error) {
	if err := s.ready(); err != nil {
		return Page[models.PriceList]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.PriceList]{}, err
	}

	lists, total, err := s.repo.ListPriceLists(ctx, limit, offset)
	if err != nil {
		return Page[models.PriceList]{}, err
	}
	return Page[models.PriceList]{Items: lists, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdatePriceList writes every updatable field of the list.
//
// It is NOT a partial update: fields that are not given are reset. This is
// deliberate — in a partial update there would be no way to tell "leave this
// end of the date window alone" from "remove it".
func (s *Service) UpdatePriceList(ctx context.Context, id string, in PriceListInput) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "price list id"); err != nil {
		return models.PriceList{}, err
	}

	list, err := buildPriceList(in)
	if err != nil {
		return models.PriceList{}, err
	}

	list.ID = id
	return s.repo.UpdatePriceList(ctx, list, s.clock)
}

// DeletePriceList deletes the list with a soft delete.
//
// The prices bound to the list are not deleted but are left out of the
// calculation; for the reasoning see repository.Repo.DeletePriceList.
func (s *Service) DeletePriceList(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceListIDPrefix, "price list id"); err != nil {
		return err
	}
	return s.repo.DeletePriceList(ctx, id, s.clock())
}

// CreatePriceRule adds a rule to an existing price.
func (s *Service) CreatePriceRule(ctx context.Context, priceID string, in RuleInput) (models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return models.PriceRule{}, err
	}
	if err := requireID(priceID, models.PriceIDPrefix, "price id"); err != nil {
		return models.PriceRule{}, err
	}

	now := s.clock()
	rule, err := buildRule(priceID, in, now)
	if err != nil {
		return models.PriceRule{}, err
	}
	return s.repo.CreatePriceRule(ctx, rule, now)
}

// ListPriceRules returns the rules of a price.
func (s *Service) ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceID, models.PriceIDPrefix, "price id"); err != nil {
		return nil, err
	}
	// The price's existence is verified: if the rules of a price that does not
	// exist came back as an empty slice, the client would think "it has no
	// rule" instead of a 404.
	if _, err := s.repo.GetPrice(ctx, priceID); err != nil {
		return nil, err
	}
	return s.repo.ListPriceRules(ctx, priceID)
}

// GetPriceRule returns the rule by id; errors.NotFound if there is none.
func (s *Service) GetPriceRule(ctx context.Context, id string) (models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return models.PriceRule{}, err
	}
	if err := requireID(id, models.PriceRuleIDPrefix, "price rule id"); err != nil {
		return models.PriceRule{}, err
	}
	return s.repo.GetPriceRule(ctx, id)
}

// DeletePriceRule deletes the rule with a soft delete.
func (s *Service) DeletePriceRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceRuleIDPrefix, "price rule id"); err != nil {
		return err
	}
	return s.repo.DeletePriceRule(ctx, id, s.clock())
}

// buildPriceList validates the input and converts it into the domain model.
func buildPriceList(in PriceListInput) (models.PriceList, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput, "the price list title cannot be empty")
	}
	if !in.Type.Valid() {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"the price list type is undefined: %q (expected: %s, %s)",
			string(in.Type), models.PriceListSale, models.PriceListOverride)
	}

	status := in.Status
	if status == "" {
		status = models.PriceListDraft
	}
	if !status.Valid() {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"the price list status is undefined: %q (expected: %s, %s, %s)",
			string(in.Status), models.PriceListDraft, models.PriceListActive, models.PriceListExpired)
	}

	starts, ends := normalizeWindow(in.StartsAt, in.EndsAt)
	if starts != nil && ends != nil && !starts.Before(*ends) {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"the price list start (%s) has to be before its end (%s)",
			starts.Format(time.RFC3339), ends.Format(time.RFC3339))
	}

	return models.PriceList{
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		Type:        in.Type,
		Status:      status,
		Metadata:    in.Metadata,
		StartsAt:    starts,
		EndsAt:      ends,
	}, nil
}

// normalizeWindow converts the window's ends to UTC and COPIES them; the
// caller's pointers are not shared.
func normalizeWindow(starts, ends *time.Time) (utcStart, utcEnd *time.Time) {
	var outStart, outEnd *time.Time
	if starts != nil {
		utc := starts.UTC()
		outStart = &utc
	}
	if ends != nil {
		utc := ends.UTC()
		outEnd = &utc
	}
	return outStart, outEnd
}
