//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
//
// # What only a real database can show here
//
// Three things, and none of them has a fake that would be worth trusting.
//
// The module's guarantee is that a storefront read cannot see an unapproved
// review, and the guarantee lives in SQL: the listing and the summary carry
// status = 'approved' as a LITERAL. A fake can be written to agree with that
// and would then be proving its own code. Here the filter is the one production
// runs.
//
// The CHECK constraints are the second half of the same guarantee, and they are
// there for the writes this module does NOT make — a migration, a hand-written
// UPDATE, a repair script. Only PostgreSQL enforces them, so only PostgreSQL
// can be asked whether they bite.
//
// And the moderation race is decided by a conditional UPDATE. A fake
// serializing on a mutex cannot show that two operators deciding at the same
// instant leave exactly one winner for the reason the design gives.
package review_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/review"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/repository"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// postgresImage is the database the tests run against.
const postgresImage = "postgres:16-alpine"

// testPool is the pool every test shares.
var testPool *db.Pool

// testDSN is the connection string the migration calls use.
var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up one Postgres container and runs every test against
// it. It is a separate function because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)

	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()

	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)

		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The race test runs a handful of goroutines at once and each holds a
	// connection for the length of its statement.
	cfg.MaxConns = 12

	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}

	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, review.New(review.Options{}).Migrations(),
		review.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)

		return 1
	}

	return m.Run()
}

// newService builds a service over the real repository.
func newService(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// productID returns an identifier unique to the calling test, so the tests can
// share one table without seeing each other's rows.
func productID(t *testing.T) string {
	t.Helper()

	return "prod_" + strings.ReplaceAll(t.Name(), "/", "_")
}

// submit stores one review through the real path.
func submit(t *testing.T, svc *service.Service, product string, rating int16) models.Review {
	t.Helper()

	stored, err := svc.Submit(context.Background(), service.SubmitInput{
		ProductID:  product,
		Rating:     rating,
		Body:       "it arrived quickly and the size was right",
		AuthorName: "A customer",
	})
	require.NoError(t, err)

	return stored
}

// decodeCursor reads a cursor the listing minted.
//
// The listing NAME is the service's own constant rather than a literal: a
// cursor is refused by the listing it does not belong to, so a test that typed
// the name would still pass on the day the name changed and would then be
// proving that the refusal works.
func decodeCursor(raw string) (corepage.Cursor, error) {
	return corepage.Decode(service.ReviewListing, raw)
}

// TestAnUnapprovedReviewIsInvisibleThroughTheRealQueries is the module's claim
// against the SQL that production runs.
func TestAnUnapprovedReviewIsInvisibleThroughTheRealQueries(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	stored := submit(t, svc, product, 5)

	page, err := svc.ListApproved(ctx, product, models.Filter{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "an unapproved review must not be listed")
	assert.Equal(t, int64(0), page.Count, "and must not be counted")

	summary, err := svc.Summarize(ctx, product)
	require.NoError(t, err)
	assert.Equal(t, int64(0), summary.Count)
	assert.Equal(t, int64(0), summary.AverageHundredths,
		"an unapproved review must not move the printed rating")

	// The row is really there; the absence above is the filter and not a write
	// that silently failed.
	seen, err := svc.GetReview(ctx, stored.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusSubmitted, seen.Status)
	assert.True(t, seen.ModeratedAt.IsZero(), "nothing has decided yet")
}

// TestApprovalPublishesAndRejectionUnpublishes walks the whole life of a review
// against the real queries.
func TestApprovalPublishesAndRejectionUnpublishes(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	stored := submit(t, svc, product, 4)

	approved, err := svc.Moderate(ctx, stored.ID, service.ModerateInput{
		To: models.StatusApproved,
	})
	require.NoError(t, err)
	assert.False(t, approved.ModeratedAt.IsZero(),
		"the database has to stamp the moment of the decision")

	page, err := svc.ListApproved(ctx, product, models.Filter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, stored.ID, page.Items[0].ID)

	summary, err := svc.Summarize(ctx, product)
	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.Count)
	assert.Equal(t, int64(400), summary.AverageHundredths)

	_, err = svc.Moderate(ctx, stored.ID, service.ModerateInput{
		To:   models.StatusRejected,
		Note: "it turned out to be about a different shop",
	})
	require.NoError(t, err)

	page, err = svc.ListApproved(ctx, product, models.Filter{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a review taken back down has to leave the storefront")
}

// TestTheSummaryRoundsTheWayTheDocumentSays checks the one number a shop prints.
//
// The ratings 5, 4 and 4 average to 4.333…, and the endpoint promises
// hundredths — so 433, and not 4 with the fraction lost on the way.
func TestTheSummaryRoundsTheWayTheDocumentSays(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	for _, rating := range []int16{5, 4, 4} {
		stored := submit(t, svc, product, rating)
		_, err := svc.Moderate(ctx, stored.ID, service.ModerateInput{To: models.StatusApproved})
		require.NoError(t, err)
	}

	summary, err := svc.Summarize(ctx, product)
	require.NoError(t, err)
	assert.Equal(t, int64(3), summary.Count)
	assert.Equal(t, int64(433), summary.AverageHundredths)
}

// TestTheRatingBoundIsEnforcedByTheDatabase asks PostgreSQL, not the service.
//
// The service refuses a rating outside 1..5 too, and that is checked in the
// unit tests. This is about the writes the service never makes: a migration, a
// repair script, a hand-written UPDATE. A rating of 9 in one row moves the
// average a shop prints, and no read would report anything wrong.
func TestTheRatingBoundIsEnforcedByTheDatabase(t *testing.T) {
	ctx := context.Background()

	for _, rating := range []int{0, 6, 9, -1} {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO reviews (id, product_id, rating, body, author_name, status)
			 VALUES ($1, $2, $3, 'b', 'n', 'submitted')`,
			models.NewReviewID(), productID(t), rating)
		require.Error(t, err, "the column must refuse a rating of %d", rating)
		assert.Contains(t, err.Error(), "reviews_rating_check")
	}
}

// TestTheStatusCheckRefusesAWordTheModuleDoesNotKnow keeps the transition table
// and the column in agreement.
//
// A status the Go code does not know would come back through the reader as a
// [models.Status] nothing matches: not approved, so invisible on the storefront,
// and not submitted either, so absent from the moderation queue — a row nobody
// can see and nobody can act on.
func TestTheStatusCheckRefusesAWordTheModuleDoesNotKnow(t *testing.T) {
	ctx := context.Background()

	// The moment stamp is supplied so the MIRROR constraint is satisfied and
	// the status check is the one that has to fire. Measured on the first run
	// of this test: with the stamp left out, PostgreSQL refused the row on
	// reviews_moderation_mirror instead — 'pending' is not 'submitted', so the
	// mirror demands a moment. Both constraints bite, but a test that accepted
	// either would go green on the day the status check was dropped.
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO reviews (id, product_id, rating, body, author_name, status, moderated_at)
		 VALUES ($1, $2, 5, 'b', 'n', 'pending', now())`,
		models.NewReviewID(), productID(t))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviews_status_check")
}

// TestTheModerationMirrorHoldsInBothDirections is the constraint the fulfillment
// module's returned_at taught this schema.
//
// A moment without a decision and a decision without a moment are both rows
// that lie, and the mirror is expressible here for the same reason it was there:
// nothing moves a review back to submitted, so the two halves really are one
// statement.
func TestTheModerationMirrorHoldsInBothDirections(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name   string
		status string
		moment string
	}{
		{"a submitted review carrying a decision moment", "submitted", "now()"},
		{"an approved review carrying none", "approved", "NULL"},
		{"a rejected review carrying none", "rejected", "NULL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, fmt.Sprintf(
				`INSERT INTO reviews (id, product_id, rating, body, author_name, status, moderated_at)
				 VALUES ($1, $2, 5, 'b', 'n', $3, %s)`, tc.moment),
				models.NewReviewID(), productID(t), tc.status)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "reviews_moderation_mirror")
		})
	}
}

// TestTwoOperatorsMakingTheSameDecisionLeaveOneWinner is what the conditional
// UPDATE is for.
//
// Every operator here reads the same submitted review and tries the SAME move.
// The WHERE carries the status each of them believed, so exactly one statement
// matches a row; the rest are told the review moved under them, either by the
// transition check (their read already saw the new status) or by the repository
// (their read was stale and the UPDATE matched nothing). Which of the two fires
// is a matter of timing and both are conflicts.
//
// # What this test measured and does NOT claim
//
// The first version had the operators make DIFFERENT moves — half approving,
// half rejecting — and asserted one winner. It failed with three, and the
// failure was the test's premise rather than the code's: the transition table
// has a CYCLE (approved -> rejected -> approved), which it has on purpose,
// because those two edges are the only way to unpublish a review and the only
// way to repair a rejection. Where there is a cycle, "one winner" is not
// expressible: two operators disagreeing can each land a legal move and neither
// is told the other decided. That is the accepted cost of a reversible decision,
// and the alternative — terminal statuses, as an invoice has — would mean a
// published review with no way down.
func TestTwoOperatorsMakingTheSameDecisionLeaveOneWinner(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	stored := submit(t, svc, product, 5)

	const operators = 6

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
		losers  int
	)

	start := make(chan struct{})

	for range operators {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-start

			_, err := svc.Moderate(ctx, stored.ID,
				service.ModerateInput{To: models.StatusApproved})

			mu.Lock()
			defer mu.Unlock()

			if err == nil {
				winners++

				return
			}

			assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err),
				"a loser has to be told it lost, not given a fault: %v", err)
			losers++
		}()
	}

	close(start)
	wg.Wait()

	assert.Equal(t, 1, winners, "only one approval may land on a submitted review")
	assert.Equal(t, operators-1, losers)
}

// TestTheStorefrontListingPagesInTheIndexsOrder walks a real page boundary.
//
// The cursor is minted from the row the page ended on and handed back; what
// this proves against a real database is that the keyset bound and the index
// order agree — a mismatch between them repeats a row or skips one, and the
// only place that shows is a second page.
func TestTheStorefrontListingPagesInTheIndexsOrder(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	const total = 5

	ids := map[string]bool{}
	for range total {
		stored := submit(t, svc, product, 4)
		_, err := svc.Moderate(ctx, stored.ID, service.ModerateInput{To: models.StatusApproved})
		require.NoError(t, err)
		ids[stored.ID] = true
	}

	seen := map[string]bool{}
	filter := models.Filter{Limit: 2}

	for pages := 0; ; pages++ {
		require.Less(t, pages, total+2, "the walk must terminate")

		page, err := svc.ListApproved(ctx, product, filter)
		require.NoError(t, err)

		for i := range page.Items {
			assert.False(t, seen[page.Items[i].ID], "a row must not come back twice")
			seen[page.Items[i].ID] = true
		}

		assert.Equal(t, int64(total), page.Count)

		if page.NextCursor == "" {
			break
		}

		cursor, err := decodeCursor(page.NextCursor)
		require.NoError(t, err)
		filter.After = cursor
	}

	assert.Equal(t, ids, seen, "the walk has to visit every approved review exactly once")
}

// TestTheAdminQueueSeesWhatTheStorefrontCannot is the counterpart assertion.
func TestTheAdminQueueSeesWhatTheStorefrontCannot(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	product := productID(t)

	waiting := submit(t, svc, product, 3)

	refused := submit(t, svc, product, 1)
	_, err := svc.Moderate(ctx, refused.ID, service.ModerateInput{
		To: models.StatusRejected, Note: "it is abusive",
	})
	require.NoError(t, err)

	submitted := models.StatusSubmitted.String()
	queue, err := svc.ListReviews(ctx, models.Filter{Status: &submitted, ProductID: &product})
	require.NoError(t, err)
	require.Len(t, queue.Items, 1)
	assert.Equal(t, waiting.ID, queue.Items[0].ID)

	all, err := svc.ListReviews(ctx, models.Filter{ProductID: &product})
	require.NoError(t, err)
	assert.Len(t, all.Items, 2, "an operator has to be able to see a decided review too")
}
