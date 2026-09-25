package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Error codes of a product's schedule (ADR 0177, ADR 0179).
const (
	// CodeNotADraft reports a publication moment asked for on a product that is
	// not a draft: a published product is already live and an archived one has
	// to be brought back to draft first.
	CodeNotADraft = "product_not_a_draft"
	// CodeAlreadyArchived reports a moment to leave asked for on a product that
	// has already left.
	CodeAlreadyArchived = "product_already_archived"
	// CodeScheduleNotAhead reports a moment that is not in the future. Changing
	// the status now is a status change, and a moment in the past would act at
	// the next pass while reading like a date nobody meant.
	CodeScheduleNotAhead = "product_schedule_not_ahead"
	// CodeScheduleOutOfOrder reports a product scheduled to leave before it
	// arrives.
	CodeScheduleOutOfOrder = "product_schedule_out_of_order"
	// CodeScheduleEmpty reports a schedule with no moment in it; taking a
	// schedule off is its own call.
	CodeScheduleEmpty = "product_schedule_empty"
)

// Schedule is a product's two moments; either may be nil.
type Schedule struct {
	// PublishAt is when a draft goes live.
	PublishAt *time.Time
	// ArchiveAt is when a draft or published product is archived.
	ArchiveAt *time.Time
}

// SetSchedule REPLACES a product's schedule (ADR 0177, ADR 0179): a moment left
// nil is taken off.
//
// The product keeps its status until a moment comes, and every reader that asks
// "is it visible" keeps answering with the status: the storefront, the search
// index and a webhook see nothing new until the scheduler changes it, at which
// point they see exactly what the same change by hand produces.
//
// The rules: a publication moment belongs to a draft, a moment to leave to a
// draft or a published product, every moment is in the future, and a product
// scheduled for both leaves after it arrives.
func (s *Service) SetSchedule(ctx context.Context, id string, schedule Schedule) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}
	if schedule.PublishAt == nil && schedule.ArchiveAt == nil {
		return models.Product{}, errors.Invalid(CodeScheduleEmpty,
			"a schedule needs a moment to publish, a moment to archive, or both; to take one off, clear it")
	}
	now := s.now()
	for _, at := range []*time.Time{schedule.PublishAt, schedule.ArchiveAt} {
		if at != nil && !at.After(now) {
			return models.Product{}, errors.Invalid(CodeScheduleNotAhead,
				"a scheduled moment has to be in the future, %s given; to act now, set the status",
				at.UTC().Format(time.RFC3339))
		}
	}
	if schedule.PublishAt != nil && schedule.ArchiveAt != nil && !schedule.ArchiveAt.After(*schedule.PublishAt) {
		return models.Product{}, errors.Invalid(CodeScheduleOutOfOrder,
			"the product would be archived at %s, before it is published at %s",
			schedule.ArchiveAt.UTC().Format(time.RFC3339), schedule.PublishAt.UTC().Format(time.RFC3339))
	}

	product, err := s.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	if err := scheduleFits(product, schedule); err != nil {
		return models.Product{}, err
	}

	if _, err := s.repo.SetProductSchedule(ctx, id, schedule.PublishAt, schedule.ArchiveAt); err != nil {
		// The product's status changed between the read and the write, and the
		// constraints refused the moment it no longer fits.
		if errors.IsInvalid(err) {
			return models.Product{}, errors.Conflict(CodeNotADraft,
				"the product changed while it was being scheduled: %s", id)
		}

		return models.Product{}, err
	}

	return s.afterScheduleChange(ctx, id)
}

// scheduleFits says whether the product's status can carry the moments.
func scheduleFits(product models.Product, schedule Schedule) error {
	if schedule.PublishAt != nil && product.Status != models.StatusDraft {
		return errors.Conflict(CodeNotADraft,
			"only a draft can be scheduled to be published; product %s is %s", product.ID, product.Status)
	}
	if schedule.ArchiveAt != nil && product.Status == models.StatusArchived {
		return errors.Conflict(CodeAlreadyArchived,
			"product %s is already archived; there is nothing to schedule it out of", product.ID)
	}

	return nil
}

// ClearSchedule takes the whole schedule off a product; the product stays what
// it is. A product with none is answered as it is.
func (s *Service) ClearSchedule(ctx context.Context, id string) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}
	if _, err := s.repo.ClearProductSchedule(ctx, id); err != nil {
		return models.Product{}, err
	}

	return s.afterScheduleChange(ctx, id)
}

// ApplyDueSchedules publishes the drafts whose moment has come and archives the
// products whose moment to leave has come, at most limit of each, and returns
// their ids (ADR 0177, ADR 0179).
//
// The publications go first. A draft scheduled to arrive and leave, both moments
// passed while the scheduler was down, is published and then archived, and gets
// the event of each — which is what happened to it, in order.
//
// Each change gets the product.updated event the same change by hand gets,
// carrying the new status, so the search index and the webhooks follow without
// knowing a schedule exists.
func (s *Service) ApplyDueSchedules(ctx context.Context, limit int64) (published, archived []string, err error) {
	now := s.now()

	published, err = s.repo.PublishDueProducts(ctx, now, limit)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range published {
		s.publishProductEvent(ctx, EventProductUpdated, id, models.StatusPublished)
	}

	archived, err = s.repo.ArchiveDueProducts(ctx, now, limit)
	if err != nil {
		return published, nil, err
	}
	for _, id := range archived {
		s.publishProductEvent(ctx, EventProductUpdated, id, models.StatusArchived)
	}

	return published, archived, nil
}

// afterScheduleChange reads the product back the way the other admin writes
// answer, and announces the change.
//
// A schedule is one of the product's own fields, so it gets product.updated. A
// subscriber deciding by status sees nothing new and does nothing; one that
// wants to know about the schedule reads the record.
func (s *Service) afterScheduleChange(ctx context.Context, id string) (models.Product, error) {
	product, err := s.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	s.publishProductEvent(ctx, EventProductUpdated, product.ID, product.Status)

	return product, nil
}
