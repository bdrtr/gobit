//go:build integration

// The reconciliation listing, proven against a real PostgreSQL.
//
// The unit tests prove what the service DECIDES once it has a suspect set. The
// set itself is a SQL predicate, and a predicate is exactly the kind of claim a
// fake store cannot falsify: the fake was written to match the query, so the
// two agree by construction. What is proven here is the query — which rows it
// returns, in which order — and that the index built for it is the one the
// planner actually chooses.
package payment_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// backdateSession moves a session's updated_at into the past.
//
// It is raw SQL because the state cannot be reached any other way: every
// service path that writes a session sets updated_at to now, which is precisely
// the column the settling window reads.
func backdateSession(ctx context.Context, t *testing.T, sessionID string, age time.Duration) {
	t.Helper()

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE payment_sessions SET updated_at = now() - $2::interval WHERE id = $1`,
		sessionID, fmt.Sprintf("%d seconds", int(age.Seconds())))
	require.NoError(t, err)
}

// The fixture's own constants and helpers.
//
// They duplicate what the module's older fixture file already provides, and the
// duplication is deliberate: this file is new, so it is English (ADR 0012), and
// every call across to the older fixtures would have pulled a Turkish
// identifier into it. The duplication ends when that file is translated.
const (
	// reconReference belongs to ANOTHER module (a cart); this module stores it
	// as free text and does not validate it (Principle 2.2).
	reconReference = "cart_RECON"
	reconCurrency  = "TRY"
	reconAmount    = int64(50_000)
)

// reconService wires the module over the shared test pool.
func reconService(t *testing.T) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))

	svc, err := service.New(service.Options{Store: repo, Providers: registry})
	require.NoError(t, err)

	return svc
}

// reconCollection opens a payment collection for the fixture.
func reconCollection(
	ctx context.Context, t *testing.T, svc *service.Service,
) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    reconReference,
		Amount:       reconAmount,
		CurrencyCode: reconCurrency,
	})
	require.NoError(t, err)

	return col
}

// authorizedSession opens a session, authorizes it, and ages it.
func authorizedSession(
	ctx context.Context, t *testing.T, svc *service.Service, key string, age time.Duration,
) models.PaymentSession {
	t.Helper()

	col := reconCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: key,
	})
	require.NoError(t, err)

	authorized, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	require.Equal(t, models.SessionAuthorized, authorized.Status)

	backdateSession(ctx, t, authorized.ID, age)

	return authorized
}

// TestReconciliationListingSelectsOnlyTheSuspectSet proves each half of the
// predicate against the database rather than against the fake that mirrors it.
func TestReconciliationListingSelectsOnlyTheSuspectSet(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)
	repo := repository.New(testPool.Pool())

	wanted := authorizedSession(ctx, t, svc, "recon-suspect-"+t.Name(), 2*time.Hour)

	// Captured: the two ledgers already agree by construction, so asking the
	// provider about it every hour would be pure noise.
	captured := authorizedSession(ctx, t, svc, "recon-captured-"+t.Name(), 2*time.Hour)
	_, err := svc.CapturePayment(ctx, captured.ID, 0)
	require.NoError(t, err)
	backdateSession(ctx, t, captured.ID, 2*time.Hour)

	// Inside the settling window: a capture in flight sits in exactly this
	// state, and reporting it would make every ordinary payment a finding.
	fresh := authorizedSession(ctx, t, svc, "recon-fresh-"+t.Name(), time.Minute)

	// Soft deleted.
	deleted := authorizedSession(ctx, t, svc, "recon-deleted-"+t.Name(), 2*time.Hour)
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE payment_sessions SET deleted_at = now() WHERE id = $1`, deleted.ID)
	require.NoError(t, err)

	rows, err := repo.ListSessionsForReconciliation(ctx, time.Now().UTC().Add(-15*time.Minute), 100)
	require.NoError(t, err)

	ids := make(map[string]bool, len(rows))
	for i := range rows {
		ids[rows[i].ID] = true
	}

	assert.True(t, ids[wanted.ID], "an aged authorized session is the whole suspect set")
	assert.False(t, ids[captured.ID], "a captured session cannot silently disagree")
	assert.False(t, ids[fresh.ID], "a capture in flight is not a divergence")
	assert.False(t, ids[deleted.ID], "a soft-deleted session is not live money")

	// The row round-trips with the fields the report is built from.
	for i := range rows {
		if rows[i].ID != wanted.ID {
			continue
		}
		assert.Equal(t, wanted.ExternalID, rows[i].ExternalID)
		assert.Equal(t, wanted.ProviderID, rows[i].ProviderID)
		assert.Equal(t, wanted.AuthorizedAmount, rows[i].AuthorizedAmount)
		assert.Equal(t, wanted.PaymentCollectionID, rows[i].PaymentCollectionID)
	}
}

// TestReconciliationListingIsOldestFirst pins the ordering, because the limit
// reads it.
//
// A hit limit means the newest sessions go unread until the backlog clears, and
// that consequence is only true — and only reportable — if the order is the one
// documented.
func TestReconciliationListingIsOldestFirst(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)
	repo := repository.New(testPool.Pool())

	old := authorizedSession(ctx, t, svc, "recon-old-"+t.Name(), 6*time.Hour)
	mid := authorizedSession(ctx, t, svc, "recon-mid-"+t.Name(), 4*time.Hour)
	recent := authorizedSession(ctx, t, svc, "recon-recent-"+t.Name(), 2*time.Hour)

	rows, err := repo.ListSessionsForReconciliation(ctx, time.Now().UTC().Add(-time.Hour), 500)
	require.NoError(t, err)

	positions := map[string]int{}
	for i := range rows {
		positions[rows[i].ID] = i
	}
	require.Contains(t, positions, old.ID)
	require.Contains(t, positions, mid.ID)
	require.Contains(t, positions, recent.ID)

	assert.Less(t, positions[old.ID], positions[mid.ID])
	assert.Less(t, positions[mid.ID], positions[recent.ID])

	// A limit takes from the old end, which is what makes truncation a backlog
	// rather than a sample.
	page, err := repo.ListSessionsForReconciliation(ctx, time.Now().UTC().Add(-5*time.Hour), 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, old.ID, page[0].ID)
}

// TestReconciliationListingUsesItsIndex holds the claim the migration makes.
//
// An index that exists and is not chosen is a comment, not an optimisation, and
// this repository has already shipped one godoc claiming an index was used
// where the planner disagreed. The query runs hourly forever, so a sequential
// scan here would grow with every payment the installation ever took.
func TestReconciliationListingUsesItsIndex(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)

	// The planner will not choose an index on an empty table, and it is right
	// not to. Enough rows to make the choice meaningful, all of them terminal
	// except one, which is the shape of a real ledger.
	for i := range 400 {
		col := reconCollection(ctx, t, svc)
		ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
			IdempotencyKey: fmt.Sprintf("recon-bulk-%d-%s", i, t.Name()),
		})
		require.NoError(t, err)

		authorized, err := svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
		if i%50 != 0 {
			_, err = svc.CapturePayment(ctx, authorized.ID, 0)
			require.NoError(t, err)
		}
		backdateSession(ctx, t, authorized.ID, time.Duration(i+1)*time.Hour)
	}

	_, err := testPool.Pool().Exec(ctx, `ANALYZE payment_sessions`)
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx,
		`EXPLAIN SELECT * FROM payment_sessions
		 WHERE status = 'authorized' AND updated_at < $1 AND deleted_at IS NULL
		 ORDER BY updated_at LIMIT $2`,
		time.Now().UTC().Add(-15*time.Minute), int32(50))
	require.NoError(t, err)
	defer rows.Close()

	plan := ""
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, plan, "payment_sessions_reconcile_idx",
		"the reconciliation index is not being used; the plan was:\n%s", plan)
	assert.NotContains(t, plan, "Seq Scan on payment_sessions",
		"the listing fell back to a sequential scan; the plan was:\n%s", plan)
}
