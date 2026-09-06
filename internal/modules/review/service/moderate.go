package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// ModerateInput is an operator's decision about a review.
type ModerateInput struct {
	// To is the status the review is to move to.
	To models.Status
	// Note is why. It is REQUIRED for a rejection and optional for an approval.
	//
	// The asymmetry is deliberate. A rejection is the decision somebody later
	// has to account for — to the author who asks why their words are not
	// there, and to the next operator who sees the row and would otherwise have
	// to guess. An approval explains itself, and demanding a sentence for it
	// would produce a column full of the word "ok".
	Note string
}

// Moderate moves a review if the move is one it may make.
//
// # Why the current status is read first AND sent to the database
//
// The read is what produces a useful error: "an approved review cannot be
// approved again" says more than a row count of zero. The write then carries
// the status that was read, so the database decides the race — two operators
// acting at the same moment cannot both win, and the loser is told the review
// moved under them rather than silently overwriting the winner.
//
// This is the endpoint decision A15 in docs/gaps.md calls the human in between,
// and it is the reason the storefront write is acceptable at all. It is admin
// only and scoped; see the api package.
func (s *Service) Moderate(
	ctx context.Context, id string, in ModerateInput,
) (models.Review, error) {
	if strings.TrimSpace(id) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput, "the review id is required")
	}
	if !in.To.Valid() {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"unknown review status: %q", in.To)
	}
	if in.To == models.StatusSubmitted {
		// Refused with its own message rather than falling through to the
		// transition table, because the table would say "a submitted review
		// cannot move to submitted" and the caller's real mistake is a
		// different one: they are asking to un-moderate, and nothing does that.
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"a review cannot be moved back to %q; moderation is a decision, "+
				"and a decision that can be withdrawn without a trace is not one",
			models.StatusSubmitted)
	}
	if in.To == models.StatusRejected && strings.TrimSpace(in.Note) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"rejecting a review requires a note saying why")
	}
	if trimmedLen(in.Note) > MaxNoteLen {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"the moderation note can be at most %d characters", MaxNoteLen)
	}

	current, err := s.repo.Get(ctx, id)
	if err != nil {
		return models.Review{}, err
	}

	if !current.Status.CanMoveTo(in.To) {
		return models.Review{}, errors.Conflict(CodeTransition,
			"a review in status %q cannot move to %q", current.Status, in.To)
	}

	moved, err := s.repo.Moderate(ctx, id, current.Status, in.To, strings.TrimSpace(in.Note))
	if err != nil {
		return models.Review{}, err
	}

	s.log.InfoContext(ctx, "a review was moderated",
		"review_id", moved.ID, "product_id", moved.ProductID,
		"from", current.Status, "to", moved.Status)

	return moved, nil
}

// GetReview returns one review whatever its status.
//
// It is the admin read. There is no storefront counterpart on purpose: a
// shopper holding a review id must not be able to read a review that is still
// waiting or was refused, and the way to be sure of that is for no such path to
// exist rather than for one to exist with a filter on it.
func (s *Service) GetReview(ctx context.Context, id string) (models.Review, error) {
	if strings.TrimSpace(id) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput, "the review id is required")
	}

	return s.repo.Get(ctx, id)
}

// ListReviews pages the reviews for an operator.
//
// With no status filter it returns everything, which is what an operator
// looking for a review they have already handled wants; the moderation QUEUE is
// this listing filtered to submitted, and that filter comes from the request
// rather than from a second endpoint, because the two differ by one word.
func (s *Service) ListReviews(ctx context.Context, filter models.Filter) (Page, error) {
	if filter.Status != nil && !models.Status(*filter.Status).Valid() {
		// Refused rather than answered with an empty page: an empty page for a
		// misspelled status reads as "there is nothing to moderate", which is
		// the one answer an operator must never be given wrongly.
		return Page{}, errors.Invalid(CodeInvalidInput,
			"unknown review status: %q", *filter.Status)
	}

	fetch, limit, err := boundedFilter(filter)
	if err != nil {
		return Page{}, err
	}

	items, count, err := s.repo.List(ctx, fetch)
	if err != nil {
		return Page{}, err
	}

	return pageOf(items, count, limit, filter.Offset), nil
}
