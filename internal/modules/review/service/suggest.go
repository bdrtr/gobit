package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
)

// MaxSuggestionModelLen is the longest model identifier, in runes.
//
// It is generous rather than tight because the string is a NAME chosen by
// somebody else — a provider's model identifier, carrying a version and a date
// — and a bound that guessed at its shape would refuse the next one. What the
// bound is for is the row: an unbounded string arriving from outside would let
// a misconfigured client write a document into a column an operator reads.
const MaxSuggestionModelLen = 200

// SuggestInput is a model's proposal about a review.
type SuggestInput struct {
	// Status is what is proposed: [models.StatusApproved] or
	// [models.StatusRejected].
	Status models.Status
	// Note is the model's reason, and it is REQUIRED for both.
	//
	// This is where a proposal differs from a moderation, which requires a note
	// only for a rejection. A human approval explains itself, because a person
	// took responsibility for it. A machine approval does not: the operator is
	// being asked to take that responsibility on the strength of the reason,
	// and a proposal with no reason gives them nothing to weigh — so the only
	// sound thing they could do with it is ignore it, which makes the whole
	// column cost without benefit.
	Note string
	// Model is which model said it, as the provider names it.
	Model string
}

// Suggest records a model's proposal about a review that is still waiting.
//
// # It moves nothing
//
// The one guarantee this package exists for — a review is invisible until an
// operator approves it — is untouched here, and deliberately so in three
// places: this method sets neither status nor the moderation moment, the
// statement behind it names neither column, and the table refuses a row where
// one moved without the other. What a proposal changes is how long an operator
// spends reading the queue.
//
// # Why a proposal about a decided review is refused rather than ignored
//
// A job producing proposals races the operator working the queue, so the review
// moving out from under it is NORMAL rather than exceptional. It is still an
// error and not a silent success: a caller told "recorded" about a proposal
// that was dropped would report a run in which every review was handled, and
// the reviews it skipped would be exactly the ones somebody was already working
// on. The conflict is the caller's signal to move to the next one.
func (s *Service) Suggest(
	ctx context.Context, id string, in SuggestInput,
) (models.Review, error) {
	if strings.TrimSpace(id) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput, "the review id is required")
	}
	if in.Status != models.StatusApproved && in.Status != models.StatusRejected {
		// Both the unknown status and StatusSubmitted land here, with one
		// message, because the caller's mistake is the same in both cases: a
		// proposal says which way the decision should go, and neither value
		// says that. Proposing 'submitted' proposes that the row stay as it is,
		// which the row already says.
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"a proposal must be %q or %q, and %q is neither",
			models.StatusApproved, models.StatusRejected, in.Status)
	}
	if strings.TrimSpace(in.Note) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"a proposal requires a note saying why, for an approval as well as a rejection")
	}
	if trimmedLen(in.Note) > MaxNoteLen {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"the proposal note can be at most %d characters", MaxNoteLen)
	}
	if strings.TrimSpace(in.Model) == "" {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"a proposal requires the name of the model that made it; "+
				"an anonymous proposal cannot be retired when a shop stops trusting a model")
	}
	if trimmedLen(in.Model) > MaxSuggestionModelLen {
		return models.Review{}, errors.Invalid(CodeInvalidInput,
			"the model name can be at most %d characters", MaxSuggestionModelLen)
	}

	suggested, err := s.repo.Suggest(ctx, id, models.Suggestion{
		Status: in.Status,
		Note:   strings.TrimSpace(in.Note),
		Model:  strings.TrimSpace(in.Model),
	})
	if err != nil {
		return models.Review{}, err
	}

	s.log.InfoContext(ctx, "a proposal was recorded about a review",
		"review_id", suggested.ID, "product_id", suggested.ProductID,
		"suggested", in.Status, "model", strings.TrimSpace(in.Model))

	return suggested, nil
}
