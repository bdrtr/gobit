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

// TestTheQueueCanBeNarrowedToWhatTheModelSaid is the operator's own question,
// and it is why the proposal is stored rather than shown one review at a time.
func TestTheQueueCanBeNarrowedToWhatTheModelSaid(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	for _, id := range []string{"rev_1", "rev_2", "rev_3"} {
		seedReview(repo, id, "prod_1", models.StatusSubmitted)
	}

	_, err := svc.Suggest(context.Background(), "rev_1", validSuggestion())
	require.NoError(t, err)

	approving := validSuggestion()
	approving.Status = models.StatusApproved
	approving.Note = "it describes the product and names nobody"
	_, err = svc.Suggest(context.Background(), "rev_2", approving)
	require.NoError(t, err)

	rejected := models.StatusRejected.String()
	page, err := svc.ListReviews(context.Background(), models.Filter{Suggested: &rejected})
	require.NoError(t, err)
	assert.Equal(t, int64(1), page.Count)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "rev_1", page.Items[0].ID)

	waiting, err := svc.ListReviews(context.Background(), models.Filter{Unsuggested: true})
	require.NoError(t, err)
	assert.Equal(t, int64(1), waiting.Count,
		"the reviews with no proposal came back wrong; that is the listing an operator "+
			"uses to see whether the job is keeping up")
	require.Len(t, waiting.Items, 1)
	assert.Equal(t, "rev_3", waiting.Items[0].ID)
}

// TestAListingNarrowedToSomethingNoProposalCanSayIsRefused holds the same rule
// the status filter follows.
//
// An empty page for a value that could never match reads as "the model has
// flagged nothing", and an operator acting on that leaves the flagged reviews
// where they are.
func TestAListingNarrowedToSomethingNoProposalCanSayIsRefused(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	for name, value := range map[string]string{
		"a status no proposal can carry": models.StatusSubmitted.String(),
		"a misspelling":                  "rejcted",
		"the API's reserved word":        "none",
		"the empty string":               "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			narrowed := value
			_, err := svc.ListReviews(context.Background(), models.Filter{Suggested: &narrowed})
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestAskingForBothHalvesAtOnceIsRefused covers the contradiction the API
// cannot produce and a direct caller can.
//
// A review either carries a proposal or does not, so the pair would always
// answer zero — and zero is the answer this module refuses to give by accident.
func TestAskingForBothHalvesAtOnceIsRefused(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	rejected := models.StatusRejected.String()
	_, err := svc.ListReviews(context.Background(), models.Filter{
		Suggested: &rejected, Unsuggested: true,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheAgreementCountsTheDecisionsAndNothingElse is the read that makes the
// whole feature accountable.
//
// ADR 0072 claims no accuracy and names what measuring one would need; ADR 0073
// lets that set accumulate. This is where a shop reads it back, and what it must
// count is exactly the reviews a PERSON decided about that carry a proposal —
// not the queue, which nobody has judged yet.
func TestTheAgreementCountsTheDecisionsAndNothingElse(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	for _, id := range []string{"rev_1", "rev_2", "rev_3", "rev_4"} {
		seedReview(repo, id, "prod_1", models.StatusSubmitted)
	}

	// Two the model would reject, one it would approve, one it never saw.
	reject := validSuggestion()
	approve := service.SuggestInput{
		Status: models.StatusApproved,
		Note:   "it describes the product and names nobody",
		Model:  "a-model-3",
	}
	for _, id := range []string{"rev_1", "rev_2"} {
		_, err := svc.Suggest(context.Background(), id, reject)
		require.NoError(t, err)
	}
	_, err := svc.Suggest(context.Background(), "rev_3", approve)
	require.NoError(t, err)

	// The operator agrees about rev_1, disagrees about rev_2, agrees about
	// rev_3 — and rev_4 is decided with no proposal at all.
	for id, to := range map[string]models.Status{
		"rev_1": models.StatusRejected,
		"rev_2": models.StatusApproved,
		"rev_3": models.StatusApproved,
		"rev_4": models.StatusApproved,
	} {
		_, err := svc.Moderate(context.Background(), id, service.ModerateInput{
			To: to, Note: "decided by a person",
		})
		require.NoError(t, err)
	}

	rows, err := svc.SuggestionAgreement(context.Background())
	require.NoError(t, err)
	require.Len(t, rows, 1, "the models were not grouped into one row")

	assert.Equal(t, "a-model-3", rows[0].Model)
	assert.Equal(t, int64(3), rows[0].Decided,
		"the review nobody proposed about was counted; the denominator must be the "+
			"reviews the model actually had an opinion on")
	assert.Equal(t, int64(2), rows[0].Agreed)
}

// TestAnInstallationThatRanNoModelGetsAnEmptyReport keeps "nothing to report"
// apart from a failure.
func TestAnInstallationThatRanNoModelGetsAnEmptyReport(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	rows, err := svc.SuggestionAgreement(context.Background())
	require.NoError(t, err, "an installation with no model got an ERROR, which a client "+
		"cannot tell from a broken query")
	assert.Empty(t, rows)
}

// TestAWaitingReviewIsNotInTheAgreement is the boundary the report rests on.
//
// A proposal about a review nobody has judged is not evidence about anything;
// counting it would put the model's own opinion in the denominator of its score.
func TestAWaitingReviewIsNotInTheAgreement(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	_, err := svc.Suggest(context.Background(), "rev_1", validSuggestion())
	require.NoError(t, err)

	rows, err := svc.SuggestionAgreement(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rows, "a review still waiting for a person was counted as evidence")
}
