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

// MaxAwaitingSuggestion bounds one read of the reviews awaiting a proposal.
//
// It is a bound on the READ and not on the work: what the caller does with the
// page is the caller's appetite. The bound exists because the caller is a
// scheduled process, and a scheduled process that asks for "all of them" on a
// shop with a year of unmoderated reviews asks for a query nobody sized.
const MaxAwaitingSuggestion = 100

// AwaitingSuggestion returns the oldest reviews waiting for a human that no
// model has been asked about.
//
// It is a READ and it is the only one this module opens for a machine. The
// pairing with [Service.Suggest] is deliberate and narrow: together they let a
// caller find the reviews a proposal would help with and write one, and they do
// not let it do anything else — there is no method here that moves a review.
func (s *Service) AwaitingSuggestion(ctx context.Context, limit int64) ([]models.Review, error) {
	if limit <= 0 {
		limit = MaxAwaitingSuggestion
	}
	if limit > MaxAwaitingSuggestion {
		// Clamped rather than refused. The caller asking for more than the
		// bound is asking for a bigger batch, not for something forbidden, and
		// an error here would stop a scheduled run over a number somebody
		// raised without knowing there was a ceiling.
		limit = MaxAwaitingSuggestion
	}

	return s.repo.AwaitingSuggestion(ctx, limit)
}

// checkSuggestionFilter refuses a listing narrowed by something no proposal can
// say.
//
// It is the same rule the status filter follows and for the same reason: an
// empty page for a value that could never match reads as "the model has flagged
// nothing", and an operator acting on that would leave the flagged reviews
// sitting where they are.
func checkSuggestionFilter(filter models.Filter) error {
	if filter.Suggested != nil && filter.Unsuggested {
		// Contradictory rather than merely empty. A review either carries a
		// proposal or does not; asking for both would always answer zero, and
		// zero is the answer this module refuses to give by accident.
		return errors.Invalid(CodeInvalidInput,
			"a listing cannot ask for reviews proposed %q AND for reviews with no "+
				"proposal at once; the two describe disjoint sets", *filter.Suggested)
	}
	if filter.Suggested == nil {
		return nil
	}

	proposed := models.Status(*filter.Suggested)
	if proposed != models.StatusApproved && proposed != models.StatusRejected {
		// StatusSubmitted lands here with the unknown values, and that is not
		// an oversight: the column's CHECK admits two words, so a listing
		// narrowed to "submitted" could never return a row. Answering it with
		// an empty page would say the model has proposed nothing when what
		// happened is that nothing could ever have been proposed.
		return errors.Invalid(CodeInvalidInput,
			"a proposal is %q or %q, so a listing cannot be narrowed to %q",
			models.StatusApproved, models.StatusRejected, proposed)
	}

	return nil
}

// SuggestionAgreement reports how often each model's proposals matched the
// decision an operator then made.
//
// # It is the corpus, read back
//
// ADR 0072 claims no accuracy for the model and says why: measuring one needs a
// set of reviews an operator has already decided about, which gobit cannot
// invent. ADR 0073 decided that a proposal is NOT cleared when the decision is
// made, so that set accumulates in the table on its own. This is the read that
// turns it into something a shop can act on.
//
// # It answers about the SHOP and not about the model
//
// A low number means this model is not helping these operators — which is the
// conclusion worth having and the only one the data supports. Nothing here says
// the model was wrong: the operator decided, and a disagreement is a record of
// two opinions rather than of a mistake.
func (s *Service) SuggestionAgreement(ctx context.Context) ([]models.Agreement, error) {
	return s.repo.SuggestionAgreement(ctx)
}
