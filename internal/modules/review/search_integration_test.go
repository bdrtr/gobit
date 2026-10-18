//go:build integration

package review_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// This file proves the admin review list's q (ADR 0456) against a real
// database. The Turkish letters are \u escapes so that the file stays English
// (ADR 0012): U+00C7 and U+00E7 are the capital and small c with cedilla,
// U+0130 the dotted capital I, U+0131 the dotless i.

func reviewIDs(items []models.Review) []string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}

	return ids
}

func TestTheReviewListFindsItsTitleTextAndByline(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)
	token := fmt.Sprintf("rv%d", time.Now().UnixNano())

	stored, err := svc.Submit(ctx, service.SubmitInput{
		ProductID:  product,
		Rating:     5,
		Title:      "CR\u0130SP F\u0131n\u0131sh " + token,
		Body:       "Sh\u0131pped FAST, the b\u00f6x was \u00e7lean.",
		AuthorName: "\u0130lker K\u0131l\u0131\u00e7",
	})
	require.NoError(t, err)
	other := submit(t, svc, product, 3)

	cases := map[string]struct {
		q    string
		want []string
	}{
		"title folded":         {q: "crisp finish " + token, want: []string{stored.ID}},
		"text by its starts":   {q: "SHIP fa box clean " + token, want: []string{stored.ID}},
		"byline":               {q: "ilker kilic " + token, want: []string{stored.ID}},
		"a word not there":     {q: token + " broken", want: nil},
		"the other review":     {q: "arrived quick", want: []string{other.ID}},
		"signs are no words":   {q: "@@ --", want: nil},
		"capital dotted i":     {q: "\u0130LKER " + token, want: []string{stored.ID}},
		"small dotless i kept": {q: "k\u0131l\u0131\u00e7 " + token, want: []string{stored.ID}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			page, err := svc.ListReviews(ctx, models.Filter{
				ProductID: &product, Search: &tc.q, Limit: 100,
			})
			require.NoError(t, err)
			assert.ElementsMatch(t, tc.want, reviewIDs(page.Items))
			assert.EqualValues(t, len(tc.want), page.Count)
		})
	}

	submitted := string(models.StatusSubmitted)
	approved := string(models.StatusApproved)
	q := token
	waiting, err := svc.ListReviews(ctx, models.Filter{Status: &submitted, Search: &q})
	require.NoError(t, err)
	assert.Equal(t, []string{stored.ID}, reviewIDs(waiting.Items))
	none, err := svc.ListReviews(ctx, models.Filter{Status: &approved, Search: &q})
	require.NoError(t, err)
	assert.Zero(t, none.Count, "the status narrows the hits")

	long := strings.Repeat("x", 201)
	_, err = svc.ListReviews(ctx, models.Filter{Search: &long})
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err), "%v", err)
}

func TestASearchedReviewListPagesAndCountsTheHits(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)
	token := fmt.Sprintf("rv%d", time.Now().UnixNano())

	var want []string
	for i := range 3 {
		stored, err := svc.Submit(ctx, service.SubmitInput{
			ProductID: product, Rating: 4, Body: fmt.Sprintf("%s number %d", token, i), AuthorName: "A",
		})
		require.NoError(t, err)
		want = append(want, stored.ID)
	}

	first, err := svc.ListReviews(ctx, models.Filter{Search: &token, Limit: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 3, first.Count)
	require.Len(t, first.Items, 2)
	require.NotEmpty(t, first.NextCursor)

	after, err := decodeCursor(first.NextCursor)
	require.NoError(t, err)
	second, err := svc.ListReviews(ctx, models.Filter{Search: &token, Limit: 2, After: after})
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.ElementsMatch(t, want, append(reviewIDs(first.Items), reviewIDs(second.Items)...))
}

// TestTheReviewSearchIsReadThroughItsIndex fails when the statement's
// expression and the index's drift apart. The table is filled inside a
// transaction that is rolled back, because on a handful of rows any plan is
// the cheapest.
func TestTheReviewSearchIsReadThroughItsIndex(t *testing.T) {
	ctx := context.Background()

	tx, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `INSERT INTO reviews (id, product_id, rating, title, body, author_name, status)
		SELECT 'rev_plan' || g, 'prod_plan', 4, 'Title ' || g, 'Body ' || g, 'Author', 'submitted'
		FROM generate_series(1, 3000) AS g`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE reviews`)
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `EXPLAIN SELECT count(*) FROM reviews r
		WHERE review_search_document(NULL, r.title, r.body, r.author_name)
		      @@ review_search_query('shipped')`)
	require.NoError(t, err)

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line + "\n")
	}
	rows.Close()
	require.NoError(t, rows.Err())
	assert.Contains(t, plan.String(), "reviews_search_idx", plan.String())
}
