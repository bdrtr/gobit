package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// fixedNow is the instant the fake stamps a moderation with, so an assertion
// about "the moment was stamped" does not have to reason about the clock.
var fixedNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

// seedReview stores a review in the given status without going through the
// rules, so a test can start from a state the storefront cannot produce.
func seedReview(repo *fakeRepo, id, productID string, status models.Status) models.Review {
	review := models.Review{
		ID:         id,
		ProductID:  productID,
		Rating:     5,
		Body:       "it arrived quickly",
		AuthorName: "A customer",
		Status:     status,
		CreatedAt:  fixedNow,
	}
	if status.Moderated() {
		review.ModeratedAt = fixedNow
	}

	return repo.seed(review)
}

// TestTheTransitionTableIsExactlyFourEdges pins the whole table down at once.
//
// It is one table-driven test rather than four, because what matters is the
// SHAPE: which of the nine (from, to) pairs are open and which are shut. A test
// per edge would let somebody add a fifth edge without any test noticing, since
// no existing test would be about the pair that was opened.
func TestTheTransitionTableIsExactlyFourEdges(t *testing.T) {
	t.Parallel()

	statuses := []models.Status{
		models.StatusSubmitted, models.StatusApproved, models.StatusRejected,
	}

	// The open edges, written out. Everything not here has to be shut.
	open := map[models.Status]map[models.Status]bool{
		models.StatusSubmitted: {models.StatusApproved: true, models.StatusRejected: true},
		models.StatusApproved:  {models.StatusRejected: true},
		models.StatusRejected:  {models.StatusApproved: true},
	}

	edges := 0

	for _, from := range statuses {
		for _, to := range statuses {
			expected := open[from][to]
			if expected {
				edges++
			}

			assert.Equalf(t, expected, from.CanMoveTo(to),
				"the edge %q -> %q is %v in the table and %v in CanMoveTo",
				from, to, expected, from.CanMoveTo(to))
		}
	}

	assert.Equal(t, 4, edges, "the table has to have exactly four open edges")
}

// TestAnApprovedReviewCanBeTakenBackDown is the edge that is easy to leave out.
//
// Without it there is no way to unpublish a review an operator approved and
// then found defamatory, and the only remaining exit is psql.
func TestAnApprovedReviewCanBeTakenBackDown(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusApproved)

	moved, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To:   models.StatusRejected,
		Note: "it names a person",
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusRejected, moved.Status)
	assert.Equal(t, "it names a person", moved.ModerationNote)
}

// TestARejectedReviewCanBeApprovedAfterAll covers the repair of a moderator's
// mistake.
//
// The author cannot write their review again — this module has no identity for
// them and no way to reach them — so refusing this edge would make the mistake
// permanent.
func TestARejectedReviewCanBeApprovedAfterAll(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusRejected)

	moved, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusApproved,
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusApproved, moved.Status)
}

// TestAReviewCannotBeApprovedTwice keeps the moment stamp honest.
//
// A second approval would restamp moderated_at, and the row would then say a
// human looked at it at a time no human did.
func TestAReviewCannotBeApprovedTwice(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusApproved)

	_, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusApproved,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTransition, errors.CodeOf(err))
}

// TestAReviewCannotBeMovedBackToSubmitted verifies that moderation cannot be
// undone.
//
// The message is its own rather than the transition table's, because the
// caller's mistake is not "that edge is shut" but "there is no such thing as
// un-moderating" — and the schema agrees: reviews_moderation_mirror ties the
// moment stamp to the status in BOTH directions, so a row returned to submitted
// would need its stamp cleared and the record of the decision lost with it.
func TestAReviewCannotBeMovedBackToSubmitted(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusApproved)

	_, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusSubmitted,
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "cannot be moved back")
}

// TestRejectingWithoutANoteIsRefused pins down the asymmetry between the two
// decisions.
func TestRejectingWithoutANoteIsRefused(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	_, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To:   models.StatusRejected,
		Note: "   ",
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "note")
}

// TestApprovingWithoutANoteIsAllowed is the other half of the same decision.
//
// Demanding a sentence for an approval would produce a column full of the word
// "ok", and a required field nobody means is worse than an empty one.
func TestApprovingWithoutANoteIsAllowed(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	moved, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusApproved,
	})
	require.NoError(t, err)
	assert.Equal(t, models.StatusApproved, moved.Status)
	assert.Empty(t, moved.ModerationNote)
}

// TestTheModerationMomentIsStamped verifies that the decision is dated.
func TestTheModerationMomentIsStamped(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seeded := seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)
	require.True(t, seeded.ModeratedAt.IsZero(),
		"precondition: a submitted review carries no moment")

	moved, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To: models.StatusApproved,
	})
	require.NoError(t, err)
	assert.False(t, moved.ModeratedAt.IsZero(), "the decision has to be dated")
}

// TestAnOverlongNoteIsRefused keeps an operator's sentence a sentence.
func TestAnOverlongNoteIsRefused(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	_, err := svc.Moderate(context.Background(), "rev_1", service.ModerateInput{
		To:   models.StatusRejected,
		Note: strings.Repeat("x", service.MaxNoteLen+1),
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAnUnknownStatusFilterIsRefusedRatherThanAnsweredEmpty is about the one
// wrong answer a moderation queue must never give.
//
// An empty page for "?status=submited" reads as "there is nothing waiting", and
// an operator who believes it stops looking.
func TestAnUnknownStatusFilterIsRefusedRatherThanAnsweredEmpty(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)

	typo := "submited"
	_, err := svc.ListReviews(context.Background(), models.Filter{Status: &typo})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheAdminListingSeesEveryStatus is the counterpart of the storefront
// tests: an operator has to be able to see exactly what a shopper cannot.
func TestTheAdminListingSeesEveryStatus(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusSubmitted)
	seedReview(repo, "rev_2", "prod_1", models.StatusApproved)
	seedReview(repo, "rev_3", "prod_1", models.StatusRejected)

	page, err := svc.ListReviews(context.Background(), models.Filter{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 3)
	assert.Equal(t, int64(3), page.Count)
}
