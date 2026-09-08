package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// validSuggestion is a proposal with nothing wrong with it, so a test about one
// rule does not have to restate the other three.
func validSuggestion() service.SuggestInput {
	return service.SuggestInput{
		Status: models.StatusRejected,
		Note:   "the text is an advertisement for another shop",
		Model:  "a-model-3",
	}
}

// TestAProposalMovesTheReviewNowhere is the test this whole feature exists
// under.
//
// The module's one guarantee is that a review is invisible until a HUMAN
// approves it, and a column written by a model is the obvious way to break that
// without noticing. So the assertion is not about the proposal at all: it is
// that the two columns a human writes are exactly where they were.
func TestAProposalMovesTheReviewNowhere(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	got, err := svc.Suggest(context.Background(), "rev_1", service.SuggestInput{
		Status: models.StatusApproved,
		Note:   "it describes the product and names nobody",
		Model:  "a-model-3",
	})
	require.NoError(t, err)

	assert.Equal(t, models.StatusSubmitted, got.Status,
		"a proposal moved the review; the human in between is the whole module")
	assert.True(t, got.ModeratedAt.IsZero(),
		"a proposal stamped the moderation moment, so the row now says a person decided when none did")

	require.NotNil(t, got.Suggestion)
	assert.Equal(t, models.StatusApproved, got.Suggestion.Status)
	assert.Equal(t, "a-model-3", got.Suggestion.Model)
}

// TestAProposalIsRefusedAboutAReviewSomebodyHasDecided covers the race a
// scheduled job runs into every time an operator is working the queue.
//
// It is refused rather than dropped: a caller told "recorded" would report a
// run in which every review was handled, and the ones it skipped would be
// exactly the ones somebody was already looking at.
func TestAProposalIsRefusedAboutAReviewSomebodyHasDecided(t *testing.T) {
	t.Parallel()

	for _, status := range []models.Status{models.StatusApproved, models.StatusRejected} {
		t.Run(status.String(), func(t *testing.T) {
			t.Parallel()

			svc, repo := newService()
			seedReview(repo, "rev_1", "prod_1", status)

			_, err := svc.Suggest(context.Background(), "rev_1", validSuggestion())
			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err),
				"a proposal arriving after a decision is a conflict, not a missing record")
		})
	}
}

// TestAProposalNeedsAReasonForAnApprovalToo is the rule that differs from
// moderation on purpose.
//
// [service.Service.Moderate] requires a note only for a rejection, because a
// person's approval explains itself. A machine's does not: the operator is
// being asked to take responsibility on the strength of the reason.
func TestAProposalNeedsAReasonForAnApprovalToo(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	in := validSuggestion()
	in.Status = models.StatusApproved
	in.Note = "   "

	_, err := svc.Suggest(context.Background(), "rev_1", in)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	moderated, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusApproved,
	})
	require.NoError(t, err,
		"a HUMAN approval still takes no note; the asymmetry is the point and this pins both halves")
	assert.Equal(t, models.StatusApproved, moderated.Status)
}

// TestAProposalIsRefusedWithoutTheThingsThatMakeItWeighable walks the input
// rules in one table.
//
// Each row is a way of arriving with a proposal an operator could not weigh —
// no direction, no reason, or no name to attribute it to — and the shape is
// what matters: all of them are refused as INVALID, so a caller cannot tell one
// from another by the kind and start branching on it.
func TestAProposalIsRefusedWithoutTheThingsThatMakeItWeighable(t *testing.T) {
	t.Parallel()

	cases := map[string]func(in *service.SuggestInput){
		"no direction at all":     func(in *service.SuggestInput) { in.Status = "" },
		"an unknown direction":    func(in *service.SuggestInput) { in.Status = "maybe" },
		"proposing it stay put":   func(in *service.SuggestInput) { in.Status = models.StatusSubmitted },
		"no reason":               func(in *service.SuggestInput) { in.Note = "" },
		"a reason of only spaces": func(in *service.SuggestInput) { in.Note = "\t \n" },
		"a reason past the bound": func(in *service.SuggestInput) {
			in.Note = strings.Repeat("x", service.MaxNoteLen+1)
		},
		"no model": func(in *service.SuggestInput) { in.Model = "" },
		"a model name past the bound": func(in *service.SuggestInput) {
			in.Model = strings.Repeat("m", service.MaxSuggestionModelLen+1)
		},
	}

	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, repo := newService()
			seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

			in := validSuggestion()
			break_(&in)

			_, err := svc.Suggest(context.Background(), "rev_1", in)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestTheEmptyIDIsRefusedBeforeTheDatabase keeps the blank id from reaching a
// statement that would answer "no such review" for it.
func TestTheEmptyIDIsRefusedBeforeTheDatabase(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	_, err := svc.Suggest(context.Background(), "  ", validSuggestion())
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestALaterProposalReplacesTheEarlierOne records the decision rather than
// discovering it: a proposal is not a record of anything that happened, so the
// older sentence has no reader once a newer one exists.
func TestALaterProposalReplacesTheEarlierOne(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	first := validSuggestion()
	_, err := svc.Suggest(context.Background(), "rev_1", first)
	require.NoError(t, err)

	second := service.SuggestInput{
		Status: models.StatusApproved,
		Note:   "on a second reading the shop it names is this one",
		Model:  "a-model-4",
	}

	got, err := svc.Suggest(context.Background(), "rev_1", second)
	require.NoError(t, err)
	require.NotNil(t, got.Suggestion)
	assert.Equal(t, models.StatusApproved, got.Suggestion.Status)
	assert.Equal(t, "a-model-4", got.Suggestion.Model)
	assert.NotEqual(t, first.Note, got.Suggestion.Note)
}
