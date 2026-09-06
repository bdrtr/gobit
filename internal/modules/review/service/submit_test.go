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

// validSubmission is a review that ought to be accepted.
func validSubmission() service.SubmitInput {
	return service.SubmitInput{
		ProductID:  "prod_1",
		Rating:     5,
		Title:      "exactly what I wanted",
		Body:       "it arrived in two days and the size was right",
		AuthorName: "A customer",
	}
}

// TestASubmittedReviewIsBornUnapproved is the module's central claim, checked
// at the earliest point it can be.
//
// Everything else in this package rests on this: if a submission could arrive
// in any other status, the storefront filter would be guarding nothing.
func TestASubmittedReviewIsBornUnapproved(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	stored, err := svc.Submit(context.Background(), validSubmission())
	require.NoError(t, err)
	assert.Equal(t, models.StatusSubmitted, stored.Status)
	assert.True(t, stored.ModeratedAt.IsZero(), "nobody has decided yet")
}

// TestASubmittedReviewIsInvisibleToTheStorefront is the same claim from the
// reading side, and it is the one the whole design exists for.
func TestASubmittedReviewIsInvisibleToTheStorefront(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	stored, err := svc.Submit(context.Background(), validSubmission())
	require.NoError(t, err)

	page, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a review nobody approved must not be listed")
	assert.Equal(t, int64(0), page.Count, "and it must not be counted either")

	summary, err := svc.Summarize(context.Background(), "prod_1")
	require.NoError(t, err)
	assert.Equal(t, int64(0), summary.Count,
		"an unapproved review must not move the product's rating")
	assert.Equal(t, int64(0), summary.AverageHundredths)

	// And it really was stored — the absence above is the filter, not a write
	// that quietly failed.
	seen, err := svc.GetReview(context.Background(), stored.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusSubmitted, seen.Status)
}

// TestAnApprovedReviewBecomesVisible closes the loop: what a person approved is
// what a shopper reads.
func TestAnApprovedReviewBecomesVisible(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	stored, err := svc.Submit(context.Background(), validSubmission())
	require.NoError(t, err)

	_, err = svc.Moderate(context.Background(), stored.ID,
		service.ModerateInput{To: models.StatusApproved})
	require.NoError(t, err)

	page, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, stored.ID, page.Items[0].ID)
}

// TestARejectedReviewDisappearsAgain proves the exit exists end to end.
func TestARejectedReviewDisappearsAgain(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	stored, err := svc.Submit(context.Background(), validSubmission())
	require.NoError(t, err)

	_, err = svc.Moderate(context.Background(), stored.ID,
		service.ModerateInput{To: models.StatusApproved})
	require.NoError(t, err)

	_, err = svc.Moderate(context.Background(), stored.ID, service.ModerateInput{
		To:   models.StatusRejected,
		Note: "it turned out to be about a different shop",
	})
	require.NoError(t, err)

	page, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a review taken back down must leave the storefront")
}

// TestOnlyTheProductsOwnReviewsAreListed keeps one product's page from carrying
// another's words.
func TestOnlyTheProductsOwnReviewsAreListed(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	seedReview(repo, "rev_1", "prod_1", models.StatusApproved)
	seedReview(repo, "rev_2", "prod_2", models.StatusApproved)

	page, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "rev_1", page.Items[0].ID)
	assert.Equal(t, "prod_1", repo.approvedFor)
}

// TestTheSummaryAveragesOnlyApprovedRatings is the aggregate's half of the same
// guarantee: a review nobody approved must not move the number a shop prints.
func TestTheSummaryAveragesOnlyApprovedRatings(t *testing.T) {
	t.Parallel()

	svc, repo := newService()

	approved := []int16{5, 4}
	for i, rating := range approved {
		review := models.Review{
			ID: "rev_ok_" + string(rune('a'+i)), ProductID: "prod_1", Rating: rating,
			Body: "b", AuthorName: "n", Status: models.StatusApproved, ModeratedAt: fixedNow,
		}
		repo.seed(review)
	}
	// A one-star review nobody has looked at yet. If it counted, the average
	// would be 333 rather than 450.
	repo.seed(models.Review{
		ID: "rev_pending", ProductID: "prod_1", Rating: 1,
		Body: "b", AuthorName: "n", Status: models.StatusSubmitted,
	})

	summary, err := svc.Summarize(context.Background(), "prod_1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), summary.Count)
	assert.Equal(t, int64(450), summary.AverageHundredths)
}

// TestAProductWithNoReviewsAnswersZeroRatherThanNotFound pins down the choice
// not to speak about the catalog.
//
// This module does not know whether a product exists, so a 404 here would be a
// statement it is in no position to make.
func TestAProductWithNoReviewsAnswersZeroRatherThanNotFound(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	summary, err := svc.Summarize(context.Background(), "prod_nothing")
	require.NoError(t, err)
	assert.Equal(t, int64(0), summary.Count)
	assert.Equal(t, "prod_nothing", summary.ProductID)
}

// TestASubmissionIsRefusedWhenItCouldNotBecomeAReadableReview walks the
// validation as a table.
func TestASubmissionIsRefusedWhenItCouldNotBecomeAReadableReview(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*service.SubmitInput)
		reason string
	}{
		{"no product", func(in *service.SubmitInput) { in.ProductID = " " }, "product id"},
		{"rating below the floor", func(in *service.SubmitInput) { in.Rating = 0 }, "rating"},
		{"rating above the ceiling", func(in *service.SubmitInput) { in.Rating = 6 }, "rating"},
		{"no byline", func(in *service.SubmitInput) { in.AuthorName = "  " }, "name"},
		{
			"byline too long",
			func(in *service.SubmitInput) {
				in.AuthorName = strings.Repeat("a", service.MaxAuthorNameLen+1)
			},
			"name",
		},
		{
			"title too long",
			func(in *service.SubmitInput) {
				in.Title = strings.Repeat("t", service.MaxTitleLen+1)
			},
			"title",
		},
		{"empty body", func(in *service.SubmitInput) { in.Body = " \n\t " }, "empty"},
		{
			"body too long",
			func(in *service.SubmitInput) { in.Body = strings.Repeat("b", service.MaxBodyLen+1) },
			"review",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, repo := newService()
			in := validSubmission()
			tc.mutate(&in)

			_, err := svc.Submit(context.Background(), in)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Contains(t, err.Error(), tc.reason)
			assert.Empty(t, repo.order, "a refused submission must write nothing")
		})
	}
}

// TestTheBodyIsStoredTrimmed keeps a card of whitespace out of the queue.
func TestTheBodyIsStoredTrimmed(t *testing.T) {
	t.Parallel()

	svc, _ := newService()
	in := validSubmission()
	in.Body = "  it is fine  "
	in.AuthorName = "  A customer  "

	stored, err := svc.Submit(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, "it is fine", stored.Body)
	assert.Equal(t, "A customer", stored.AuthorName)
}

// TestTheBoundsAreCountedInRunesAndNotBytes keeps a review written in an
// accented language from being half the length of an English one.
//
// The sample rune is written as a \u escape and is not a Turkish letter, which
// is what a shop here would actually receive. Measured rather than guessed: the
// first version repeated a literal Turkish letter and TestNoTurkishOutsideLedger
// failed on this line, because the language gate reads SOURCE and cannot tell a
// letter in a test fixture from a file written in Turkish. Any multi-byte rune
// proves the same property, so the one that keeps this file ASCII is the right
// one.
func TestTheBoundsAreCountedInRunesAndNotBytes(t *testing.T) {
	t.Parallel()

	svc, _ := newService()
	in := validSubmission()
	// Every one of these characters is two bytes in UTF-8. At the rune limit
	// the review is accepted; a byte-counting check would refuse it at half.
	in.Body = strings.Repeat("\u00e9", service.MaxBodyLen)

	stored, err := svc.Submit(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, service.MaxBodyLen, len([]rune(stored.Body)))
}

// TestTheIdentifierCarriesTheModulesPrefix keeps the id readable without a
// table lookup, which is what every other module's generator promises too.
func TestTheIdentifierCarriesTheModulesPrefix(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	stored, err := svc.Submit(context.Background(), validSubmission())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(stored.ID, models.ReviewIDPrefix),
		"the id has to start with %q: %s", models.ReviewIDPrefix, stored.ID)
	assert.Len(t, strings.TrimPrefix(stored.ID, models.ReviewIDPrefix), models.IDBodyLen)
}

// TestTheListingRefusesAPageSizeItCannotServe covers the two paging bounds on
// the surface a stranger can reach.
func TestTheListingRefusesAPageSizeItCannotServe(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	_, err := svc.ListApproved(context.Background(), "prod_1",
		models.Filter{Limit: service.MaxLimit + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = svc.ListApproved(context.Background(), "prod_1", models.Filter{Offset: -1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheListingMintsACursorOnlyWhenThereIsANextPage keeps a client from
// walking forever.
func TestTheListingMintsACursorOnlyWhenThereIsANextPage(t *testing.T) {
	t.Parallel()

	svc, repo := newService()
	for i := range 3 {
		repo.seed(models.Review{
			ID: "rev_" + string(rune('a'+i)), ProductID: "prod_1", Rating: 4,
			Body: "b", AuthorName: "n", Status: models.StatusApproved,
			ModeratedAt: fixedNow, CreatedAt: fixedNow,
		})
	}

	first, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, first.Items, 2)
	assert.NotEmpty(t, first.NextCursor, "there is a third review, so there is a next page")

	whole, err := svc.ListApproved(context.Background(), "prod_1", models.Filter{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, whole.Items, 3)
	assert.Empty(t, whole.NextCursor, "the last page names no next position")
}
