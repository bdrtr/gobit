package reviewsuggest_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/jobs/reviewsuggest"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// fakeReviews records what the job asked for and what it stored.
type fakeReviews struct {
	waiting  []models.Review
	readErr  error
	stored   []service.SuggestInput
	storeErr error
}

func (f *fakeReviews) AwaitingSuggestion(_ context.Context, limit int64) ([]models.Review, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	if int64(len(f.waiting)) > limit {
		return f.waiting[:limit], nil
	}

	return f.waiting, nil
}

func (f *fakeReviews) Suggest(
	_ context.Context, _ string, in service.SuggestInput,
) (models.Review, error) {
	if f.storeErr != nil {
		return models.Review{}, f.storeErr
	}

	f.stored = append(f.stored, in)

	return models.Review{}, nil
}

// fakeModel answers with whatever it was given, and records what it was asked.
type fakeModel struct {
	answer provider.Classification
	err    error
	asked  []provider.ClassifyInput
}

func (f *fakeModel) ID() string { return "fake" }

func (f *fakeModel) Classify(
	_ context.Context, in provider.ClassifyInput,
) (provider.Classification, error) {
	f.asked = append(f.asked, in)
	if f.err != nil {
		return provider.Classification{}, f.err
	}

	return f.answer, nil
}

// waiting builds a review the job would pick up.
func waiting(id string) models.Review {
	return models.Review{
		ID: id, ProductID: "prod_1", Rating: 2,
		Title: "not what I ordered", Body: "the box was empty",
		AuthorName: "A customer", Status: models.StatusSubmitted,
	}
}

// answer is a classification the module would accept.
func answer() provider.Classification {
	return provider.Classification{
		Label:  models.StatusRejected.String(),
		Reason: "the text advertises another shop",
		Model:  "a-model-3",
	}
}

// run executes one pass and returns the line it reported and the run's error.
//
// The two come back together because every test here asserts on both: the
// detail an operator reads, and the outcome they scan for first.
func run(t *testing.T, r *fakeReviews, m *fakeModel) (detail string, failure error) {
	t.Helper()

	ctx := jobreport.WithReporter(context.Background())
	failure = reviewsuggest.Definition(r, m, nil).Run(ctx)

	return jobreport.Detail(ctx), failure
}

// TestTheAuthorsNameIsNotSent is the privacy assertion, and it is first because
// it is the one that cannot be undone.
//
// The review text goes to a third party the moment this plugin is installed.
// The author's byline is the only thing the module stores about the person who
// wrote it, it says nothing about whether the text is spam, and once it has
// been in somebody else's request log it cannot be taken back.
func TestTheAuthorsNameIsNotSent(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{waiting: []models.Review{waiting("rev_1")}}
	model := &fakeModel{answer: answer()}

	_, _ = run(t, reviews, model)

	require.Len(t, model.asked, 1)
	assert.NotContains(t, model.asked[0].Text, "A customer",
		"the author's byline was sent to the model; it is the one thing this module "+
			"stores about that person and it says nothing about the text")
	assert.Contains(t, model.asked[0].Text, "the box was empty")
}

// TestTheLabelSetIsClosedToTheTwoDecisions pins what the model may answer.
//
// The label goes into a column with a CHECK that knows two words, and offering
// a third would move the refusal from this package to the database — where the
// message names a constraint instead of the mistake.
func TestTheLabelSetIsClosedToTheTwoDecisions(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{waiting: []models.Review{waiting("rev_1")}}
	model := &fakeModel{answer: answer()}

	_, _ = run(t, reviews, model)

	require.Len(t, model.asked, 1)
	assert.ElementsMatch(t,
		[]string{models.StatusApproved.String(), models.StatusRejected.String()},
		model.asked[0].Labels)
	assert.NotContains(t, model.asked[0].Labels, models.StatusSubmitted.String())
}

// TestOneFailureDoesNotStopThePass is the batch's whole shape.
//
// A single review the model cannot answer about is not a reason to leave the
// rest unasked, and the counts are what make the difference visible afterwards.
func TestOneFailureDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{waiting: []models.Review{waiting("rev_1"), waiting("rev_2")}}
	failing := &fakeModel{err: errors.New("the model is unreachable")}

	detail, err := run(t, reviews, failing)
	require.Error(t, err, "a pass in which nothing succeeded must FAIL rather than report a "+
		"green run over an empty column")
	assert.Contains(t, detail, "2 failed")
	assert.Len(t, failing.asked, 2, "the second review was never attempted")
}

// TestAReviewThatMovedUnderUsIsNotAFailure covers the race with an operator,
// which is the normal outcome of a job that runs beside a person.
func TestAReviewThatMovedUnderUsIsNotAFailure(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{
		waiting:  []models.Review{waiting("rev_1")},
		storeErr: coreerrors.Conflict("review_conflict", "an operator got there first"),
	}

	detail, err := run(t, reviews, &fakeModel{answer: answer()})
	require.NoError(t, err,
		"an operator deciding first turned into a failed run; racing a person is the "+
			"expected case, not a fault")
	assert.Contains(t, detail, "1 moved under us")
}

// TestAModuleRefusalIsReportedAsTheProvidersProblem separates the two ways a
// store can fail.
//
// A conflict is an operator; anything else is the module refusing what the
// model said, which is the provider answering badly and has to be visible as
// that rather than as a busy queue.
func TestAModuleRefusalIsReportedAsTheProvidersProblem(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{
		waiting:  []models.Review{waiting("rev_1")},
		storeErr: coreerrors.Invalid("review_invalid_input", "a proposal requires a note"),
	}

	detail, err := run(t, reviews, &fakeModel{answer: answer()})
	require.Error(t, err)
	assert.Contains(t, detail, "1 refused")
	assert.NotContains(t, detail, "1 moved under us")
}

// TestAnEmptyQueueIsAQuietSuccessThatStillSpeaks is the difference between
// "nothing was waiting" and "this has not run".
func TestAnEmptyQueueIsAQuietSuccessThatStillSpeaks(t *testing.T) {
	t.Parallel()

	detail, err := run(t, &fakeReviews{}, &fakeModel{answer: answer()})
	require.NoError(t, err)
	assert.Contains(t, detail, "0 waiting",
		"an empty pass said nothing at all, so an operator cannot tell it from one that "+
			"never ran")
}

// TestWhatTheModelSaidIsStoredWhole checks the three fields survive the trip.
//
// The model NAME comes from the answer rather than from the configuration,
// because those two drift: an installation naming a family gets whichever
// version the service is serving that week.
func TestWhatTheModelSaidIsStoredWhole(t *testing.T) {
	t.Parallel()

	reviews := &fakeReviews{waiting: []models.Review{waiting("rev_1")}}

	detail, err := run(t, reviews, &fakeModel{answer: answer()})
	require.NoError(t, err)
	require.Len(t, reviews.stored, 1)

	assert.Equal(t, models.StatusRejected, reviews.stored[0].Status)
	assert.Equal(t, "the text advertises another shop", reviews.stored[0].Note)
	assert.Equal(t, "a-model-3", reviews.stored[0].Model)
	assert.Contains(t, detail, "1 proposed")
}

// TestAReadFailureFailsTheRunWithoutAskingAnything keeps a broken read from
// looking like an empty queue.
func TestAReadFailureFailsTheRunWithoutAskingAnything(t *testing.T) {
	t.Parallel()

	model := &fakeModel{answer: answer()}
	_, err := run(t, &fakeReviews{readErr: errors.New("the database is gone")}, model)

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "could not be read"))
	assert.Empty(t, model.asked, "the model was asked despite the read having failed")
}
