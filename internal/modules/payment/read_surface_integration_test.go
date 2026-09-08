//go:build integration

// This file covers the two things the module's money records are READ through,
// and one thing that is only ever written under a race.
//
//   - The provider's idempotency COLLISION. The unit tests in
//     internal/modules/payment/manual prove the provider's branch with an
//     in-memory store, but the branch only exists because a real Postgres
//     turns ON CONFLICT DO NOTHING into "no row came back" rather than into a
//     unique-violation error. That translation is a property of the database
//     and of the generated query, not of the provider, and only a real
//     database can witness it.
//   - The operator's reconciliation view: the refunds of a capture and the
//     paginated collection listing. Their filters, their total order and
//     their count all live in SQL; every one of them can be wrong while every
//     Go test still passes.
//
// The tests share the container and the schema set up by TestMain in
// payment_integration_test.go. The database is NOT reset between tests, so
// every listing assertion here filters on a reference unique to its own test;
// a listing test that trusted the whole table would break the moment another
// test wrote a row.
package payment_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// --- the provider's idempotency collision ------------------------------------

// TestASecondProviderCallOnTheSameKeyReadsBackTheFirstSession proves the
// collision branch of the manual provider's ledger on a real database.
//
// The provider is called DIRECTLY here, not through the service. That is not a
// shortcut, it is the only way in: the service holds the collection lock and
// answers a repeated idempotency key out of its own payment_sessions row
// (service.CreateSession), so no flow driven from the service ever reaches the
// provider a second time with the same key. The branch that stops a double
// charge is therefore unreachable from every existing integration test, and
// has never been executed against Postgres.
//
// What only the database can witness is the shape of the collision.
// InsertManualSessionIfAbsent carries ON CONFLICT DO NOTHING and RETURNING; on
// a conflict Postgres returns NO ROW, which pgx reports as pgx.ErrNoRows, and
// the repository translates exactly that into "not inserted, and not an
// error". If Postgres instead reported a duplicate key violation — which is
// what the same statement does without the ON CONFLICT clause — the second
// call would come back as an error and every retried checkout would fail after
// the customer had already been charged once. The re-read then has to find the
// FIRST session: a re-read that missed it would leave the caller with a
// NotFound for a session that plainly exists.
func TestASecondProviderCallOnTheSameKeyReadsBackTheFirstSession(t *testing.T) {
	ctx := context.Background()
	_, prov := newService(t)
	key := "provider-collision-" + models.NewPaymentCollectionID()

	in := coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      testReference,
		IdempotencyKey: key,
	}

	first, err := prov.CreateSession(ctx, in)
	require.NoError(t, err)

	second, err := prov.CreateSession(ctx, in)
	require.NoError(t, err,
		"a repeated call on the same key must not be an error; a retried checkout "+
			"depends on it")

	assert.Equal(t, first.ID, second.ID,
		"the second call opened a NEW provider session; the same customer would be "+
			"charged twice")
	assert.Equal(t, first.Amount, second.Amount)
	assert.Equal(t, first.CurrencyCode, second.CurrencyCode)

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM payment_manual_sessions WHERE idempotency_key = $1`,
		key).Scan(&rows))
	assert.Equal(t, int64(1), rows, "the provider's ledger must hold ONE row for the key")

	// The row that came back the second time is the one on disk, not a value
	// invented in memory: the ledger is asked again, by identifier this time.
	stored, err := prov.GetSession(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, key, stored.IdempotencyKey)
	assert.Equal(t, models.SessionPending, stored.Status)
}

// TestASecondProviderCallWithADifferentAmountIsRefusedOnTheStoredRow proves
// that the mismatch check reads the amount BACK OUT OF THE DATABASE.
//
// The comparison the provider makes on a collision is against the row it just
// re-read. Nothing in the flow keeps the first call's amount in memory — the
// process that made it may be long gone — so a re-read that returned the wrong
// row, or a stale one, would let a second call quietly pass with a different
// amount. That is the failure idempotency exists to prevent: the caller
// believes it sent 50000 and the ledger holds something else.
func TestASecondProviderCallWithADifferentAmountIsRefusedOnTheStoredRow(t *testing.T) {
	ctx := context.Background()
	_, prov := newService(t)
	key := "provider-mismatch-" + models.NewPaymentCollectionID()

	_, err := prov.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      testReference,
		IdempotencyKey: key,
	})
	require.NoError(t, err)

	_, err = prov.CreateSession(ctx, coreprovider.CreateSessionInput{
		Amount:         testAmount + 1,
		CurrencyCode:   testCurrency,
		Reference:      testReference,
		IdempotencyKey: key,
	})
	require.Error(t, err,
		"the same key used with a different amount must be refused; accepting it "+
			"silently means the amount the caller sent was never applied")
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err),
		"the refusal has to be reportable by code; the message may change, the code may not")
	assert.True(t, errors.HasKind(err, errors.KindConflict),
		"a reused key with a different amount is a conflict, not a server fault")
}

// TestConcurrentProviderCallsOnOneKeyOpenOneSession proves the collision holds
// when the two calls are genuinely simultaneous.
//
// The sequential test above cannot fail the way production does. Two checkout
// requests arriving together both find the key free, both try to insert, and
// "read first, write if absent" would open two provider sessions — two holds
// on the customer's card for one basket. There is no lock in this path at all:
// the single INSERT ... ON CONFLICT DO NOTHING statement is the whole defence,
// and its correctness is a property of Postgres's unique index. The loser of
// the race must then see the winner's COMMITTED row on its re-read; if it read
// too early it would get NotFound for a session that exists.
func TestConcurrentProviderCallsOnOneKeyOpenOneSession(t *testing.T) {
	ctx := context.Background()
	_, prov := newService(t)
	key := "provider-race-" + models.NewPaymentCollectionID()

	const goroutines = 8
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		ids    = map[string]int{}
		errs   []error
		params = coreprovider.CreateSessionInput{
			Amount:         testAmount,
			CurrencyCode:   testCurrency,
			Reference:      testReference,
			IdempotencyKey: key,
		}
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ses, createErr := prov.CreateSession(ctx, params)

			mu.Lock()
			defer mu.Unlock()
			if createErr != nil {
				errs = append(errs, createErr)
				return
			}
			ids[ses.ID]++
		}()
	}
	wg.Wait()

	assert.Empty(t, errs,
		"a lost race is not an error; the loser re-reads the winner's session")
	assert.Len(t, ids, 1, "every caller must come back with the SAME provider session")

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM payment_manual_sessions WHERE idempotency_key = $1`,
		key).Scan(&rows))
	assert.Equal(t, int64(1), rows,
		"a second row means a second hold on the customer's card for one basket")
}

// --- the refunds of a capture ------------------------------------------------

// TestEveryPartialRefundStaysInTheCaptureListing proves that a refund which
// was made can be read back.
//
// This listing is the operator's only per-capture reconciliation view. The
// capture row carries a single refunded_amount total; the listing carries the
// individual refunds behind it. A refund that was made but does not appear
// here is indistinguishable from one that was never made — and the two lead to
// opposite actions, because the second one gets refunded again.
//
// The claim is a SQL one and nothing else can hold it: the payment_id filter
// and the ordering live in the query text, and a listing that silently
// returned the refunds of another capture, or none at all, would still be a
// valid Go slice.
func TestEveryPartialRefundStaysInTheCaptureListing(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	pay := capturedPayment(ctx, t, svc, "refund-listing")

	first, err := svc.RefundPayment(ctx, pay.ID, 10_000, "damaged item")
	require.NoError(t, err)
	second, err := svc.RefundPayment(ctx, pay.ID, 15_000, "goodwill")
	require.NoError(t, err)

	// A refund on a DIFFERENT capture must not leak into this listing; without
	// a second capture the payment_id filter could be missing entirely and
	// every assertion below would still pass.
	other := capturedPayment(ctx, t, svc, "refund-listing-other")
	_, err = svc.RefundPayment(ctx, other.ID, 5_000, "someone else's refund")
	require.NoError(t, err)

	refunds, err := svc.ListRefunds(ctx, pay.ID)
	require.NoError(t, err)
	require.Len(t, refunds, 2, "both refunds that were made must be in the listing")

	ids := []string{refunds[0].ID, refunds[1].ID}
	assert.Contains(t, ids, first.ID)
	assert.Contains(t, ids, second.ID)

	var total int64
	for i := range refunds {
		assert.Equal(t, pay.ID, refunds[i].PaymentID,
			"the listing returned a refund belonging to another capture")
		total += refunds[i].Amount
	}
	assert.Equal(t, int64(25_000), total,
		"the listed amounts must add up to what was actually refunded")

	// The listed rows and the capture's own total are two separate writes; if
	// they disagree, the reconciliation view is lying about one of them.
	stored, err := svc.GetPayment(ctx, pay.ID)
	require.NoError(t, err)
	assert.Equal(t, total, stored.RefundedAmount,
		"the capture's refunded_amount must equal the sum of its refund rows")

	reasons := []string{refunds[0].Reason, refunds[1].Reason}
	assert.Contains(t, reasons, "damaged item")
	assert.Contains(t, reasons, "goodwill")
}

// TestRefundsAreListedNewestFirst proves the listing has a deterministic,
// newest-first order.
//
// Order is not decoration here. The operator reading a capture asks "what
// happened last"; an oldest-first listing puts the answer at the bottom of a
// page they may never scroll to, and a listing with NO defined order can put
// it anywhere and put it somewhere else on the next call. Only the database
// decides this: the ORDER BY lives in the query text, and Go never re-sorts
// what comes back.
func TestRefundsAreListedNewestFirst(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	pay := capturedPayment(ctx, t, svc, "refund-order")

	const refunds = 4
	made := make([]string, 0, refunds)
	for range refunds {
		ref, err := svc.RefundPayment(ctx, pay.ID, 5_000, "partial")
		require.NoError(t, err)
		made = append(made, ref.ID)
	}

	listed, err := svc.ListRefunds(ctx, pay.ID)
	require.NoError(t, err)
	require.Len(t, listed, refunds)

	got := make([]string, 0, refunds)
	for i := range listed {
		got = append(got, listed[i].ID)
	}
	slices.Reverse(made)
	assert.Equal(t, made, got, "the newest refund has to come first")

	for i := 1; i < len(listed); i++ {
		assert.False(t, listed[i].CreatedAt.After(listed[i-1].CreatedAt),
			"refund %d is newer than the one listed before it", i)
	}
}

// --- the paginated collection listing ----------------------------------------

// TestTheCollectionCountIsOfTheFilterNotOfThePage proves the pagination
// envelope's count survives a page with no rows on it.
//
// The count and the rows come from two separate statements on purpose, and the
// reason is exactly this case: on a page past the end no row comes back at
// all. A count derived from the returned rows — or from a window function
// evaluated per returned row — reads as 0 there, and the operator's list view
// concludes there is nothing to page back to and offers no way home.
//
// Only a database shows this. A count computed in Go from the slice it just
// built is correct on every page except the ones nobody writes a test for.
func TestTheCollectionCountIsOfTheFilterNotOfThePage(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	reference := "cart_LIST_" + models.NewPaymentCollectionID()

	const created = 3
	for range created {
		newCollectionFor(ctx, t, svc, reference)
	}

	page, total, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 1},
	})
	require.NoError(t, err)
	assert.Len(t, page, 1, "the page size has to be respected")
	assert.Equal(t, int64(created), total,
		"the count is of the filter, not of the page that was returned")

	beyond, total, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 10, Offset: created + 5},
	})
	require.NoError(t, err)
	assert.Empty(t, beyond, "there is nothing past the end")
	assert.Equal(t, int64(created), total,
		"a page past the end still has to report how many rows the filter matches; "+
			"otherwise the list view cannot offer a way back")
}

// TestTheCollectionCountAppliesTheSameFiltersAsTheListing proves the two
// statements are kept in step.
//
// The filter predicates are written out twice, once in the listing and once in
// the count, and nothing but this test forces them to agree. When they drift,
// the failure is not an error: the list shows the right rows and reports a
// total taken from a wider set, so the operator pages forward into pages that
// are empty and concludes records are missing.
func TestTheCollectionCountAppliesTheSameFiltersAsTheListing(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	reference := "cart_FILTER_" + models.NewPaymentCollectionID()

	const created = 3
	for range created {
		newCollectionFor(ctx, t, svc, reference)
	}

	notPaid := models.CollectionNotPaid.String()
	rows, total, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Status:    &notPaid,
		Page:      service.Page{Limit: 10},
	})
	require.NoError(t, err)
	assert.Len(t, rows, created)
	assert.Equal(t, int64(created), total)

	// Nothing under this reference has been captured, so BOTH statements have
	// to come back empty. A count that ignored the status filter would report
	// three rows for a page that shows none.
	captured := models.CollectionCaptured.String()
	rows, total, err = svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Status:    &captured,
		Page:      service.Page{Limit: 10},
	})
	require.NoError(t, err)
	assert.Empty(t, rows, "no collection under this reference is captured")
	assert.Zero(t, total, "the count has to apply the status filter as well")

	// The reference filter is proved the same way: another reference exists
	// with its own rows, and neither the page nor the count may include them.
	otherReference := "cart_FILTER_" + models.NewPaymentCollectionID()
	newCollectionFor(ctx, t, svc, otherReference)

	rows, total, err = svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 100},
	})
	require.NoError(t, err)
	assert.Len(t, rows, created, "another reference's collections leaked into the page")
	assert.Equal(t, int64(created), total,
		"another reference's collections leaked into the count")
}

// TestTheCollectionListingIsNewestFirst proves the listing's total order.
//
// Pagination without a total order is not pagination: rows that tie on the
// sort key can come back on page one and again on page two, or on neither, and
// the operator sees a list that changes under them while nothing was written.
// The order is decided entirely by the query's ORDER BY — created_at first and
// the identifier as the tie-break — and Go never re-sorts the result.
func TestTheCollectionListingIsNewestFirst(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	reference := "cart_ORDER_" + models.NewPaymentCollectionID()

	const created = 4
	made := make([]string, 0, created)
	for range created {
		made = append(made, newCollectionFor(ctx, t, svc, reference).ID)
	}

	rows, _, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 10},
	})
	require.NoError(t, err)
	require.Len(t, rows, created)

	got := make([]string, 0, created)
	for i := range rows {
		got = append(got, rows[i].ID)
	}
	slices.Reverse(made)
	assert.Equal(t, made, got, "the newest collection has to come first")

	// The two pages of a split listing must not overlap: paging is only safe
	// while the order is total.
	first, _, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 2},
	})
	require.NoError(t, err)
	next, _, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 2, Offset: 2},
	})
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.Len(t, next, 2)
	assert.Equal(t, got[:2], []string{first[0].ID, first[1].ID})
	assert.Equal(t, got[2:], []string{next[0].ID, next[1].ID},
		"the second page repeated or skipped a row")
}

// TestTheCollectionFilterReachesTheCountAsWellAsTheRows proves that the two
// statements behind one page apply the SAME filter.
//
// The listing and the count are separate SQL, and the predicate is written
// twice. The count is the half that is easy to forget: the filtered-out
// collection vanishes from the page, the total keeps counting it, and the list
// view shows "3 records" above two rows for good. An operator reading that
// concludes a record was lost.
//
// The odd row is put out of the filter by its STATUS, written straight into the
// table. Until ADR 0054 this test used deleted_at, which no longer exists —
// a collection is not hidden, it reaches a terminal status. The status is
// normally derived and rewritten by the service; setting it by hand is how the
// read gets exercised without a flow that would also move the amounts.
func TestTheCollectionFilterReachesTheCountAsWellAsTheRows(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	reference := "cart_FILTERED_" + models.NewPaymentCollectionID()

	const created = 3
	var canceled string
	for range created {
		canceled = newCollectionFor(ctx, t, svc, reference).ID
	}

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE payment_collections SET status = 'canceled' WHERE id = $1`, canceled)
	require.NoError(t, err)

	notPaid := models.CollectionNotPaid.String()
	rows, total, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Status:    &notPaid,
		Page:      service.Page{Limit: 10},
	})
	require.NoError(t, err)
	assert.Len(t, rows, created-1, "the canceled collection must not be served")
	assert.Equal(t, int64(created-1), total,
		"the count must not keep counting a collection the page no longer shows")

	for i := range rows {
		assert.NotEqual(t, canceled, rows[i].ID)
	}
}

// --- helpers -----------------------------------------------------------------

// newCollectionFor opens a payment collection under the given reference.
//
// It exists next to yeniKoleksiyon because the listing tests need a reference
// of their OWN: the database is shared by every test in this package, and a
// listing filtered on the package-wide reference would count rows written by
// tests that have nothing to do with it.
func newCollectionFor(
	ctx context.Context, t *testing.T, svc *service.Service, reference string,
) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    reference,
		Amount:       testAmount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return col
}

// capturedPayment drives a collection all the way to a capture and returns it.
//
// The refund tests all need the same three steps in front of them (open a
// session, authorize, capture); repeating them inline would bury the one claim
// each test actually makes under the same twelve lines.
func capturedPayment(
	ctx context.Context, t *testing.T, svc *service.Service, keyPrefix string,
) models.Payment {
	t.Helper()

	col := newCollectionFor(ctx, t, svc, "cart_"+keyPrefix)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: keyPrefix + "-" + col.ID,
	})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	return pay
}
