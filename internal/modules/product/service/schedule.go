package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Error codes of the scheduled publication (ADR 0177).
const (
	// CodeNotADraft reports a schedule asked for on a product that is not a
	// draft: a published product is already live and an archived one has to be
	// brought back to draft first.
	CodeNotADraft = "product_not_a_draft"
	// CodeScheduleNotAhead reports a moment that is not in the future. Publishing
	// now is a status change, and a moment in the past would publish at the next
	// pass while reading like a date nobody meant.
	CodeScheduleNotAhead = "product_schedule_not_ahead"
)

// SchedulePublication sets the moment a DRAFT is to be published (ADR 0177).
//
// The product stays a draft until then, and every reader that asks "is it
// visible" keeps answering with its status: the storefront, the search index and
// a webhook see nothing new until the scheduled publisher changes the status, at
// which point they see exactly what a publication by hand produces.
//
// A schedule on a product that already has one replaces it.
func (s *Service) SchedulePublication(ctx context.Context, id string, at time.Time) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}
	if !at.After(s.now()) {
		return models.Product{}, errors.Invalid(CodeScheduleNotAhead,
			"a publication moment has to be in the future, %s given; to publish now, set the status",
			at.UTC().Format(time.RFC3339))
	}

	product, err := s.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	if product.Status != models.StatusDraft {
		return models.Product{}, notADraft(product)
	}

	if _, err := s.repo.ScheduleProductPublication(ctx, id, at); err != nil {
		// The product was a draft a moment ago and matched no row now: its
		// status changed in between.
		if errors.IsNotFound(err) {
			return models.Product{}, errors.Conflict(CodeNotADraft,
				"the product stopped being a draft while it was being scheduled: %s", id)
		}

		return models.Product{}, err
	}

	return s.afterScheduleChange(ctx, id)
}

// CancelPublication takes the schedule off a product; the product stays what it
// is. A product with no schedule is answered as it is.
func (s *Service) CancelPublication(ctx context.Context, id string) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}
	if _, err := s.repo.CancelProductPublication(ctx, id); err != nil {
		return models.Product{}, err
	}

	return s.afterScheduleChange(ctx, id)
}

// PublishDue publishes the drafts whose moment has come, at most limit of them,
// and returns their ids (ADR 0177).
//
// Each one gets the product.updated event a publication by hand gets, carrying
// the new status, so the search index takes it in and a webhook tells whoever
// listens — nothing downstream needs to know a schedule exists.
func (s *Service) PublishDue(ctx context.Context, limit int64) ([]string, error) {
	ids, err := s.repo.PublishDueProducts(ctx, s.now(), limit)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		s.publishProductEvent(ctx, EventProductUpdated, id, models.StatusPublished)
	}

	return ids, nil
}

// afterScheduleChange reads the product back the way the other admin writes
// answer, and announces the change.
//
// A schedule is one of the product's own fields, so it gets product.updated. A
// subscriber deciding by status sees a draft and does nothing; one that wants to
// know about the schedule reads the record.
func (s *Service) afterScheduleChange(ctx context.Context, id string) (models.Product, error) {
	product, err := s.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	s.publishProductEvent(ctx, EventProductUpdated, product.ID, product.Status)

	return product, nil
}

// notADraft is the refusal for a schedule on a product that is not a draft.
func notADraft(product models.Product) error {
	return errors.Conflict(CodeNotADraft,
		"only a draft can be scheduled; product %s is %s", product.ID, product.Status)
}
