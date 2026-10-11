//go:build integration

// The pending sessions of a provider whose customer pays on its own page,
// proven against a real PostgreSQL (ADR 0438).
//
// A customer who pays on the provider's page and closes the browser leaves the
// money taken, the session pending here and no order. The second page of the
// reconciliation reads those sessions, newest first, and these tests hold the
// query, its order, its index and the report it feeds.
package payment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// pageProviderID is the fake provider whose customer pays on its page.
const pageProviderID = "paying_page"

// payingPage is a provider whose customer pays on its own page and which
// reports what was paid, as PayTR does: a session is pending until the
// customer pays, and its own ledger says captured afterwards.
type payingPage struct {
	mu   sync.Mutex
	paid map[string]bool
}

var (
	_ coreprovider.SessionInspector = (*payingPage)(nil)
)

func newPayingPage() *payingPage { return &payingPage{paid: map[string]bool{}} }

func (p *payingPage) ID() string { return pageProviderID }

// CustomerAuthorizes says the customer pays on the provider's page.
func (p *payingPage) CustomerAuthorizes() bool { return true }

// pay records that the customer paid on the page.
func (p *payingPage) pay(externalID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paid[externalID] = true
}

func (p *payingPage) CreateSession(
	_ context.Context, in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return coreprovider.Session{
		ID: "page_" + in.IdempotencyKey, Status: coreprovider.SessionPending,
		Amount: in.Amount, CurrencyCode: in.CurrencyCode,
	}, nil
}

func (p *payingPage) Authorize(_ context.Context, sessionID string) (coreprovider.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.paid[sessionID] {
		return coreprovider.AuthResult{Status: coreprovider.SessionPending},
			fmt.Errorf("the customer has not paid %s", sessionID)
	}

	return coreprovider.AuthResult{Status: coreprovider.SessionAuthorized}, nil
}

func (p *payingPage) Capture(context.Context, string, int64) error { return nil }
func (p *payingPage) Refund(context.Context, string, int64) error  { return nil }
func (p *payingPage) Cancel(context.Context, string) error         { return nil }

func (p *payingPage) InspectSession(_ context.Context, sessionID string) (coreprovider.SessionInspection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.paid[sessionID] {
		return coreprovider.SessionInspection{Status: coreprovider.SessionPending}, nil
	}

	return coreprovider.SessionInspection{
		Status: coreprovider.SessionCaptured, AuthorizedAmount: reconAmount, CapturedAmount: reconAmount,
	}, nil
}

// pageService wires the module over the shared pool with the paying page and
// the manual provider.
func pageService(t *testing.T, page *payingPage) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(page))

	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)

	return svc
}

// pendingSession opens a session at a provider and ages it.
func pendingSession(
	ctx context.Context, t *testing.T, svc *service.Service, providerID, key string, age time.Duration,
) models.PaymentSession {
	t.Helper()

	col := reconCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, providerID, service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)
	require.Equal(t, models.SessionPending, ses.Status)
	backdateSession(ctx, t, ses.ID, age)

	return ses
}

// TestThePendingListingSelectsOnlyTheUnreportedSet proves each half of the
// predicate against the database.
func TestThePendingListingSelectsOnlyTheUnreportedSet(t *testing.T) {
	ctx := context.Background()
	page := newPayingPage()
	svc := pageService(t, page)
	repo := repository.New(testPool.Pool())

	wanted := pendingSession(ctx, t, svc, pageProviderID, "page-wanted-"+t.Name(), 2*time.Hour)
	fresh := pendingSession(ctx, t, svc, pageProviderID, "page-fresh-"+t.Name(), time.Minute)
	driven := pendingSession(ctx, t, svc, manual.ID, "page-driven-"+t.Name(), 2*time.Hour)
	closed := pendingSession(ctx, t, svc, pageProviderID, "page-closed-"+t.Name(), 2*time.Hour)
	require.NoError(t, svc.CancelPayment(ctx, closed.ID))
	backdateSession(ctx, t, closed.ID, 2*time.Hour)
	declined := pendingSession(ctx, t, svc, pageProviderID, "page-declined-"+t.Name(), 2*time.Hour)
	_, err := testPool.Pool().Exec(ctx, `UPDATE payment_sessions SET status = 'failed' WHERE id = $1`, declined.ID)
	require.NoError(t, err)
	backdateSession(ctx, t, declined.ID, 2*time.Hour)
	old := pendingSession(ctx, t, svc, pageProviderID, "page-old-"+t.Name(), 30*time.Hour)
	authorized := pendingSession(ctx, t, svc, pageProviderID, "page-authorized-"+t.Name(), time.Minute)
	page.pay(authorized.ExternalID)
	_, err = svc.AuthorizePayment(ctx, authorized.ID)
	require.NoError(t, err)
	backdateSession(ctx, t, authorized.ID, 2*time.Hour)

	settled := time.Now().UTC().Add(-15 * time.Minute)
	rows, err := repo.ListPendingSessionsForReconciliation(ctx,
		settled, settled.Add(-24*time.Hour), []string{pageProviderID}, 1_000)
	require.NoError(t, err)
	ids := map[string]bool{}
	for i := range rows {
		ids[rows[i].ID] = true
	}

	assert.True(t, ids[wanted.ID], "a settled pending session of the paying page is the set")
	assert.False(t, ids[fresh.ID], "a completion may still be on its way")
	assert.False(t, ids[driven.ID], "a driven provider's pending session holds no money")
	assert.True(t, ids[closed.ID], "a canceled session is read: a customer may have paid on its page after the cancel")
	assert.False(t, ids[declined.ID], "a declined session holds no money")
	assert.False(t, ids[authorized.ID], "an authorized session is the other page's")
	assert.False(t, ids[old.ID], "a session settled before the window has left the page")

	none, err := repo.ListPendingSessionsForReconciliation(ctx, time.Now().UTC(), time.Time{}, nil, 1_000)
	require.NoError(t, err)
	assert.Empty(t, none, "with no paying-page provider no pending session holds money")
}

// TestThePendingListingIsNewestFirst pins the order the page reads, because a
// window larger than a page is read from its new end.
func TestThePendingListingIsNewestFirst(t *testing.T) {
	ctx := context.Background()
	svc := pageService(t, newPayingPage())
	repo := repository.New(testPool.Pool())

	old := pendingSession(ctx, t, svc, pageProviderID, "page-old-"+t.Name(), 6*time.Hour)
	mid := pendingSession(ctx, t, svc, pageProviderID, "page-mid-"+t.Name(), 4*time.Hour)
	recent := pendingSession(ctx, t, svc, pageProviderID, "page-recent-"+t.Name(), 2*time.Hour)

	rows, err := repo.ListPendingSessionsForReconciliation(ctx,
		time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-24*time.Hour), []string{pageProviderID}, 1_000)
	require.NoError(t, err)
	positions := map[string]int{}
	for i := range rows {
		positions[rows[i].ID] = i
	}
	require.Contains(t, positions, old.ID)
	require.Contains(t, positions, mid.ID)
	require.Contains(t, positions, recent.ID)
	assert.Less(t, positions[recent.ID], positions[mid.ID])
	assert.Less(t, positions[mid.ID], positions[old.ID])

	page, err := repo.ListPendingSessionsForReconciliation(ctx,
		time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-24*time.Hour), []string{pageProviderID}, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, recent.ID, page[0].ID, "a limit takes from the new end")
}

// TestAPaidPageWithNoOrderIsReported is the default path's dominant failure:
// the customer paid on the page and no completion came, so the pass reports
// the pending session the provider says is captured, and an authorization and
// a capture take it out of the report.
func TestAPaidPageWithNoOrderIsReported(t *testing.T) {
	ctx := context.Background()
	page := newPayingPage()
	svc := pageService(t, page)

	paid := pendingSession(ctx, t, svc, pageProviderID, "page-paid-"+t.Name(), time.Minute)
	page.pay(paid.ExternalID)
	backdateSession(ctx, t, paid.ID, 2*time.Hour)

	report, err := svc.Reconcile(ctx, 15*time.Minute, service.MaxReconcileLimit)
	require.NoError(t, err)
	var reported *service.Divergence
	for i := range report.Divergences {
		if report.Divergences[i].SessionID == paid.ID {
			reported = &report.Divergences[i]
		}
	}
	require.NotNil(t, reported, "the payment with no order behind it is reported: %+v", report)
	assert.Equal(t, models.SessionPending, reported.LocalStatus)
	assert.Equal(t, int64(0), reported.LocalAuthorized)
	assert.Equal(t, coreprovider.SessionCaptured, reported.ProviderStatus)
	assert.Equal(t, reconAmount, reported.ProviderCaptured)
	assert.Equal(t, paid.ExternalID, reported.ExternalID)

	_, err = svc.AuthorizePayment(ctx, paid.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, paid.ID, 0)
	require.NoError(t, err)
	backdateSession(ctx, t, paid.ID, 2*time.Hour)

	report, err = svc.Reconcile(ctx, 15*time.Minute, service.MaxReconcileLimit)
	require.NoError(t, err)
	for _, divergence := range report.Divergences {
		assert.NotEqual(t, paid.ID, divergence.SessionID, "a captured payment is no longer reported")
	}
}

// TestThePendingListingUsesItsIndex holds the claim the migration makes, as
// TestReconciliationListingUsesItsIndex holds 000002's.
func TestThePendingListingUsesItsIndex(t *testing.T) {
	ctx := context.Background()
	svc := pageService(t, newPayingPage())
	for i := range 20 {
		pendingSession(ctx, t, svc, pageProviderID, fmt.Sprintf("page-bulk-%d-%s", i, t.Name()),
			time.Duration(i+1)*time.Hour)
	}

	bulk := reconCollection(ctx, t, svc)
	prefix := fmt.Sprintf("payses_pagebulk%d_", time.Now().UnixNano())
	_, err := testPool.Pool().Exec(ctx, `
		INSERT INTO payment_sessions (id, payment_collection_id, provider_id, external_id,
			status, amount, authorized_amount, currency_code, idempotency_key, updated_at)
		SELECT $1 || g, $2, $3, $1 || g, 'captured', $4, $4, $5, $1 || g,
			now() - make_interval(hours => g)
		FROM generate_series(1, 20000) AS g`,
		prefix, bulk.ID, pageProviderID, reconAmount, reconCurrency)
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx, `ANALYZE payment_sessions`)
	require.NoError(t, err)

	// The statement is the query's own text (queries/payment_sessions.sql).
	settled := time.Now().UTC().Add(-15 * time.Minute)
	rows, err := testPool.Pool().Query(ctx,
		`EXPLAIN SELECT * FROM payment_sessions
		 WHERE status = 'pending' AND updated_at < $1 AND updated_at >= $2
		   AND provider_id = ANY($3::text[])
		 ORDER BY updated_at DESC LIMIT $4`,
		settled, settled.Add(-24*time.Hour), []string{pageProviderID}, int32(1001))
	require.NoError(t, err)
	defer rows.Close()

	plan := ""
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, plan, "payment_sessions_pending_reconcile_idx",
		"the pending reconciliation index is not being used; the plan was:\n%s", plan)
	assert.NotContains(t, plan, "Seq Scan on payment_sessions",
		"the listing fell back to a sequential scan; the plan was:\n%s", plan)
}

// TestTheReportQueueKeepsItsAttemptsOnPostgres holds the report queue's SQL:
// one report per session, the due read, the attempt count, a settle that
// happens once, and the constraints that keep an outcome and its moment
// together (ADR 0438).
func TestTheReportQueueKeepsItsAttemptsOnPostgres(t *testing.T) {
	ctx := context.Background()
	page := newPayingPage()
	svc := pageService(t, page)
	repo := repository.New(testPool.Pool())
	ses := pendingSession(ctx, t, svc, pageProviderID, "page-queue-"+t.Name(), time.Minute)

	openedAt := time.Now().UTC()
	ids, err := svc.RecordSessionReport(ctx, pageProviderID, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, []string{ses.ID}, ids)
	opened, err := repo.RecordSessionReport(ctx, ses.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, opened, "a session is reported once")

	dueNow := func(at time.Time) bool {
		t.Helper()
		due, err := svc.DueSessionReports(ctx, at, service.MaxReconcileLimit)
		require.NoError(t, err)
		for _, report := range due {
			if report.SessionID == ses.ID {
				return true
			}
		}

		return false
	}
	assert.False(t, dueNow(openedAt.Add(time.Second)), "the checkout attempts a new report itself; the sweep waits")
	assert.True(t, dueNow(openedAt.Add(models.ReportFirstRetry+time.Minute)), "and takes it a retry later")

	next := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	require.NoError(t, svc.RecordSessionReportAttempt(ctx, ses.ID, "no stock", next))
	report, err := svc.SessionReport(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Attempts)
	assert.True(t, next.Equal(report.NextAttemptAt), "%s != %s", next, report.NextAttemptAt)
	assert.Equal(t, "no stock", report.LastFailure)
	assert.False(t, dueNow(next.Add(-time.Second)), "a report waits for its next try")
	assert.True(t, dueNow(next), "a report is due at its next try")

	require.NoError(t, svc.SettleSessionReport(ctx, ses.ID, models.ReportCompleted, ""))
	require.NoError(t, svc.SettleSessionReport(ctx, ses.ID, models.ReportRefunded, "late"))
	report, err = svc.SessionReport(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReportCompleted, report.Outcome, "a settled report keeps its outcome")
	require.NotNil(t, report.SettledAt)
	assert.False(t, dueNow(next.Add(time.Hour)), "a settled report is never due")
	require.NoError(t, svc.RecordSessionReportAttempt(ctx, ses.ID, "after", next))
	report, err = svc.SessionReport(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Attempts, "a settled report counts no attempt")

	other := pendingSession(ctx, t, svc, pageProviderID, "page-queue-other-"+t.Name(), time.Minute)
	for name, statement := range map[string]string{
		"an unknown outcome": `INSERT INTO payment_session_reports (session_id, next_attempt_at, outcome, settled_at)
			VALUES ($1, now(), 'bogus', now())`,
		"an outcome with no time": `INSERT INTO payment_session_reports (session_id, next_attempt_at, outcome)
			VALUES ($1, now(), 'completed')`,
		"a time with no outcome": `INSERT INTO payment_session_reports (session_id, next_attempt_at, settled_at)
			VALUES ($1, now(), now())`,
		"negative attempts": `INSERT INTO payment_session_reports (session_id, next_attempt_at, attempts)
			VALUES ($1, now(), -1)`,
		"no first try": `INSERT INTO payment_session_reports (session_id) VALUES ($1)`,
	} {
		_, err := testPool.Pool().Exec(ctx, statement, other.ID)
		require.Error(t, err, name)
		if name == "no first try" {
			assert.Contains(t, err.Error(), `"next_attempt_at"`, "a report is opened with the moment it is first due")

			continue
		}
		assert.Contains(t, err.Error(), "check constraint", name)
	}
}

// TestACompletionSecretIsSpentOnceOnPostgres holds the completion secret's SQL
// (ADR 0438): the digest is kept and the plaintext is not, a secret that is not
// the session's spends nothing, the right one is spent once, a secret is its
// own session's alone, and the digest's width is the table's own rule.
func TestACompletionSecretIsSpentOnceOnPostgres(t *testing.T) {
	ctx := context.Background()
	svc := pageService(t, newPayingPage())
	first := pendingSession(ctx, t, svc, pageProviderID, "page-secret-"+t.Name(), time.Minute)
	second := pendingSession(ctx, t, svc, pageProviderID, "page-secret-marked-"+t.Name(), time.Minute)

	secret, err := svc.OpenSessionAnswer(ctx, first.ID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(secret, models.CompletionSecretPrefix))
	var kept []byte
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT secret_digest FROM payment_session_answers WHERE session_id = $1`, first.ID).Scan(&kept))
	assert.Equal(t, models.CompletionSecretDigest(secret), kept)
	assert.NotContains(t, string(kept), secret, "the plaintext is not kept")

	for _, presented := range []string{"", "cpsec_guess"} {
		claimed, err := svc.ClaimSessionAnswer(ctx, first.ID, presented)
		require.NoError(t, err)
		assert.False(t, claimed, "%q is not the session's secret", presented)
	}
	claimed, err := svc.ClaimSessionAnswer(ctx, first.ID, secret)
	require.NoError(t, err)
	assert.True(t, claimed, "the secret answers once")
	claimed, err = svc.ClaimSessionAnswer(ctx, first.ID, secret)
	require.NoError(t, err)
	assert.False(t, claimed, "and not twice")

	other, err := svc.OpenSessionAnswer(ctx, second.ID)
	require.NoError(t, err)
	claimed, err = svc.ClaimSessionAnswer(ctx, second.ID, secret)
	require.NoError(t, err)
	assert.False(t, claimed, "one session's secret spends no other's")
	claimed, err = svc.ClaimSessionAnswer(ctx, second.ID, other)
	require.NoError(t, err)
	assert.True(t, claimed)

	third := pendingSession(ctx, t, svc, pageProviderID, "page-secret-width-"+t.Name(), time.Minute)
	_, err = testPool.Pool().Exec(ctx,
		`INSERT INTO payment_session_answers (session_id, secret_digest) VALUES ($1, '\x00')`, third.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "payment_session_answers_digest_width"`)
}
