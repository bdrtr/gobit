//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are kept apart under the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS with a fake store. The tests
// here prove the GROUND those decisions stand on: that the migration can be
// rolled back WHILE DATA IS PRESENT, that the constraints are really enforced,
// that the provider's state lives outside the process, and that the
// concurrency claim holds at the database level. The claim "two concurrent
// Authorize calls produce a single authorization" in particular can be tested
// only here, with real goroutines on real row locks.
package payment_test

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/core/link"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
	"github.com/bdrtr/gobit/internal/testdb"
)

const postgresImage = "postgres:16-alpine"

// moduleTables are the tables the module owns; the migration tests use this
// list.
var moduleTables = []string{
	"payment_collections", "payment_sessions", "payments", "refunds",
	"payment_manual_sessions",
	// Store credit's two tables (ADR 0152): the ledger is the module's, the
	// sessions are the provider's.
	"payment_store_credit_entries", "payment_store_credit_sessions",
	// The loyalty points ledger (ADR 0164) and the sessions of the provider that
	// spends the points (ADR 0165): the same split as for credit, the ledger is
	// the module's, the sessions are the provider's.
	"payment_loyalty_entries", "payment_loyalty_sessions",
	// The gift cards, their ledger and the gift-card provider's sessions
	// (ADR 0208).
	"payment_gift_cards", "payment_gift_card_entries", "payment_gift_card_sessions",
}

// Constants used in the test data. The reference belongs to ANOTHER module (a
// cart or an order); this module does not verify that it exists (Principle
// 2.2).
const (
	testReference = "cart_TEST"
	testCurrency  = "TRY"
	testAmount    = int64(50_000)
)

var (
	// testPool is the pool all tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test on
// it. It is a separate function because os.Exit skips the defers.
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
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency tests run dozens of goroutines at once; because every
	// transaction holds a connection, the pool is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, payment.New().Migrations(), payment.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	// The outbox is a CORE schema and the module writes to it inside its own
	// transaction (ADR 0121), so the harness has to apply it too — the way the
	// composition root applies the core schemas before the module schemas.
	//
	// This line exists because the payment module's migrations must NOT TOUCH
	// event_outbox: core/eventbus/outbox owns the table, and two owners cannot
	// advance the same table from separate versions.
	if err := db.Migrate(ctx, testDSN, outbox.Migrations(), outbox.MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the outbox migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// newService sets up a service running on the real repository and the REAL
// manual provider.
func newService(t *testing.T) (*service.Service, *manual.Provider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	prov := manual.New(repo, nil)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	return svc, prov
}

// countingProvider wraps the real provider and COUNTS THE CALLS.
//
// The claim "a single authorization is produced" can be tested DEFINITELY only
// this way: because the manual provider is idempotent within itself, the work a
// second call did cannot be told apart by looking at the money amount — both
// calls write the same result. What has to be measured is not the amount but
// HOW MANY TIMES THE PROVIDER WAS CALLED: without the row lock, several
// goroutines see the session as "pending" and all of them go to the provider.
type countingProvider struct {
	inner *manual.Provider

	mu        sync.Mutex
	authorize int
	capture   int
	cancel    int
}

// That the decorator satisfies the core contract is verified at compile time.
var _ coreprovider.PaymentProvider = (*countingProvider)(nil)

// ID returns the wrapped provider's id; sessions are opened under the same name.
func (s *countingProvider) ID() string { return s.inner.ID() }

// CreateSession forwards the call as it is.
func (s *countingProvider) CreateSession(
	ctx context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return s.inner.CreateSession(ctx, in)
}

// Authorize counts the call and forwards it.
func (s *countingProvider) Authorize(ctx context.Context, sessionID string) (coreprovider.AuthResult, error) {
	s.mu.Lock()
	s.authorize++
	s.mu.Unlock()
	return s.inner.Authorize(ctx, sessionID)
}

// Capture counts the call and forwards it.
func (s *countingProvider) Capture(ctx context.Context, sessionID string, amount int64) error {
	s.mu.Lock()
	s.capture++
	s.mu.Unlock()
	return s.inner.Capture(ctx, sessionID, amount)
}

// Refund forwards the call as it is.
func (s *countingProvider) Refund(ctx context.Context, sessionID string, amount int64) error {
	return s.inner.Refund(ctx, sessionID, amount)
}

// Cancel counts the call and forwards it.
func (s *countingProvider) Cancel(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	s.cancel++
	s.mu.Unlock()
	return s.inner.Cancel(ctx, sessionID)
}

// callCounts returns the numbers of calls made to the provider.
func (s *countingProvider) callCounts() (authorize, capture, cancel int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authorize, s.capture, s.cancel
}

// newCountingService sets up a service on a provider that counts the calls.
func newCountingService(t *testing.T) (*service.Service, *countingProvider) {
	t.Helper()

	repo := repository.New(testPool.Pool())
	counting := &countingProvider{inner: manual.New(repo, nil)}
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(counting))

	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)
	return svc, counting
}

// newCollection opens a payment collection for a test.
func newCollection(ctx context.Context, t *testing.T, svc *service.Service) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    testReference,
		Amount:       testAmount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	return col
}

// isolatedDatabase creates a database of the test's own (internal/testdb),
// applies the module's and the outbox's migrations to it, and returns its
// address and a pool on it.
//
// A test that drops the schema has to run here: in the shared database it
// would rewind the rows of every test before it and depend on which ones had
// run (D135).
func isolatedDatabase(ctx context.Context, t *testing.T, prefix string) (string, *db.Pool) {
	t.Helper()

	dsn := testdb.New(t, testDSN, prefix)

	require.NoError(t, db.Migrate(ctx, dsn, payment.New().Migrations(), payment.ModuleName))
	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))
	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return dsn, pool
}

// serviceOnPool is newService on another database's pool.
func serviceOnPool(t *testing.T, pool *db.Pool) *service.Service {
	t.Helper()

	repo := repository.New(pool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	svc, err := service.New(service.Options{Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil)})
	require.NoError(t, err)

	return svc
}

// tableExistsIn reports whether the table exists in the pool's database.
func tableExistsIn(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists))
	return exists
}

// TestTheMigrationRollsBackWithDataPresent verifies that the migration can be
// applied and rolled back on a POPULATED schema.
//
// The gate in internal/arch runs up -> down -> up only on an EMPTY database
// and cannot catch rollback errors that depend on data — in Phase 5 a bug got
// through exactly that gap. The test here first writes the FULL graph of
// collection, session, capture, refund and provider session; a down file that
// gets the foreign key order wrong fails only this way.
func TestTheMigrationRollsBackWithDataPresent(t *testing.T) {
	ctx := context.Background()
	src := payment.New().Migrations()
	// The test drops and re-creates the module's schema, so it runs in a
	// database of its own (D135). In the shared one it rewound every row the
	// tests before it wrote, and 000006's down refuses a point ledger holding a
	// spend row: it passed only while it ran before every test that spends
	// points, which file order happened to arrange.
	dsn, pool := isolatedDatabase(ctx, t, "payment_migration")
	svc := serviceOnPool(t, pool)
	tableExists := func(ctx context.Context, t *testing.T, table string) bool {
		t.Helper()
		return tableExistsIn(ctx, t, pool, table)
	}

	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "migration-key",
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	_, err = svc.RefundPayment(ctx, pay.ID, 1_000, "migration test")
	require.NoError(t, err)

	for _, table := range moduleTables {
		require.True(t, tableExists(ctx, t, table), "%s should exist at the start", table)
	}

	require.NoError(t, db.MigrateDown(ctx, dsn, src, payment.ModuleName, 0),
		"down failed — this means the module can NEVER BE MIGRATED again")
	for _, table := range moduleTables {
		assert.False(t, tableExists(ctx, t, table), "%s should not remain after the rollback", table)
	}

	require.NoError(t, db.Migrate(ctx, dsn, src, payment.ModuleName))
	for _, table := range moduleTables {
		assert.True(t, tableExists(ctx, t, table), "%s should be applied again", table)
	}

	version, dirty, err := db.Version(ctx, dsn, payment.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "there should be no half-finished migration")
	assert.Equal(t, highestVersion(t, src), version,
		"re-applying should run ALL migrations, not just the latest one")
}

// highestVersion returns the largest version number in the embedded migration
// set.
//
// The number is NOT HARD-CODED: hard-coded, the test breaks every time a
// migration is added to the module, and what breaks it is not a bug but the
// test's own outdated expectation. Read from the set, what is tested becomes
// the right thing too — "after the rollback ALL of them were applied again" —
// rather than just "the number is one".
func highestVersion(t *testing.T, src fs.FS) uint {
	t.Helper()

	entries, err := fs.ReadDir(src, ".")
	require.NoError(t, err)

	var highest uint
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}

		digits := name[:strings.IndexByte(name, '_')]
		n, convErr := strconv.ParseUint(digits, 10, 32)
		require.NoError(t, convErr, "%s does not start with a version number", name)

		if uint(n) > highest {
			highest = uint(n)
		}
	}

	require.Positive(t, highest, "the embedded migration set looks empty")
	return highest
}

// TestNoCrossModuleForeignKeys verifies that ALL foreign keys on the module's
// tables lead to the module's own tables again (Principle 2.2).
//
// payment_collections.reference in particular is a cart or order id and CANNOT
// be a foreign key; this test shows that the rule really holds in the schema.
func TestNoCrossModuleForeignKeys(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT c.conname, src.relname, tgt.relname
         FROM pg_constraint c
         JOIN pg_class src ON src.oid = c.conrelid
         JOIN pg_class tgt ON tgt.oid = c.confrelid
         WHERE c.contype = 'f' AND src.relname = ANY($1)`, moduleTables)
	require.NoError(t, err)
	defer rows.Close()

	owned := make(map[string]struct{}, len(moduleTables))
	for _, table := range moduleTables {
		owned[table] = struct{}{}
	}

	var count int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, owned, tgt,
			"the %s constraint references outside the module (%s -> %s)", name, src, tgt)
		count++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, count, "in-module foreign keys should be in use")
}

// TestTheEndToEndPaymentFlow runs the full flow Phase 6 asks for with the REAL
// provider: CreateSession -> Authorize -> Capture -> Refund.
//
// At every step both the module's record and the PROVIDER's ledger are
// checked; a bug in which the two diverge is visible only by looking at both
// sides at once.
func TestTheEndToEndPaymentFlow(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := newCollection(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "e2e-" + col.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, models.SessionPending, ses.Status)

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAwaiting, freshCol.Status)

	authorized, err := svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, authorized.Status)
	assert.Equal(t, testAmount, authorized.AuthorizedAmount)

	providerSession, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized, providerSession.Status,
		"the provider's ledger should show the session authorized too")

	freshCol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionAuthorized, freshCol.Status)
	assert.Equal(t, testAmount, freshCol.AuthorizedAmount)

	pay, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)
	assert.Equal(t, testAmount, pay.Amount)
	assert.Equal(t, testCurrency, pay.CurrencyCode)

	providerSession, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, providerSession.CapturedAmount)

	freshCol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured, freshCol.Status)

	refund, err := svc.RefundPayment(ctx, pay.ID, testAmount/2, "partial refund")
	require.NoError(t, err)
	assert.Equal(t, testAmount/2, refund.Amount)

	freshCol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyRefunded, freshCol.Status)

	_, err = svc.RefundPayment(ctx, pay.ID, 0, "remaining refund")
	require.NoError(t, err)

	freshCol, err = svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded, freshCol.Status)
	assert.Equal(t, testAmount, freshCol.RefundedAmount)

	providerSession, err = prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, providerSession.RefundedAmount,
		"the refund should be reflected in the provider's ledger too")
}

// TestTwoConcurrentAuthorizesProduceOneAuthorization tests the concurrency claim
// on real row locks.
//
// Two goroutines try to authorize the same session at the same time. The lock
// on the collection row serializes them; the second call sees the state the
// first one wrote and falls through to a no-op. Without the lock both would
// read "pending", both would go to the provider and the collection's held
// amount would be DOUBLE — the amount assertion below catches exactly that.
func TestTwoConcurrentAuthorizesProduceOneAuthorization(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-auth-" + col.ID,
	})
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		errs      []error
		succeeded int
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, authErr := svc.AuthorizePayment(ctx, ses.ID)

			mu.Lock()
			defer mu.Unlock()
			if authErr != nil {
				errs = append(errs, authErr)
				return
			}
			succeeded++
		}()
	}
	wg.Wait()

	assert.Empty(t, errs, "every call should succeed (one authorizes, the rest are no-ops)")
	assert.Equal(t, goroutines, succeeded)

	authorizeCalls, _, _ := counting.callCounts()
	assert.Equal(t, 1, authorizeCalls,
		"the PROVIDER should be called only once; the remaining calls should fall through to a no-op")

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, freshCol.AuthorizedAmount,
		"the held amount should equal ONE authorization, not a multiple of it")
	assert.Equal(t, models.CollectionAuthorized, freshCol.Status)
}

// TestTwoConcurrentCreateSessionsProduceOneSession verifies that the idempotency
// key holds under concurrent calls too.
//
// A call that slipped in between the two steps of "read first, write if
// absent" would open a second session if the collection lock were not there;
// the unique index is the last line of defense, but it is here that the lock
// is first seen working.
func TestTwoConcurrentCreateSessionsProduceOneSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)
	key := "concurrent-create-" + col.ID

	const goroutines = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		ids  = map[string]int{}
		errs []error
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			ses, createErr := svc.CreateSession(ctx, col.ID, manual.ID,
				service.CreateSessionInput{IdempotencyKey: key})

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

	assert.Empty(t, errs, "concurrent calls with the same key should not return an error")
	assert.Len(t, ids, 1, "every call should return the SAME session")

	sessions, err := svc.ListPaymentSessions(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "the database should hold a single session row")
}

// TestCancelIdempotencyOnTheRealDatabase verifies that the saga compensation
// is idempotent on real rows too.
//
// That the second call returns no error is not enough: it is also proven that
// the collection's held amount is not touched a SECOND TIME. Had it been
// touched, the amount would drop below zero and the CHECK constraint would blow
// up the transaction — that is, it would not be a silent bug but one that
// locks the compensation up completely in production.
func TestCancelIdempotencyOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "cancel-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "the second compensation should NOT return an error")
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "the third compensation should not return an error either")

	freshSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, freshSession.Status)

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, freshCol.AuthorizedAmount)
	assert.Equal(t, models.CollectionCanceled, freshCol.Status)

	providerSession, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, providerSession.Status)
	assert.Zero(t, providerSession.AuthorizedAmount)
}

// TestTwoConcurrentCancelsProduceOneCompensation verifies that the compensation is
// applied only once under a race too.
func TestTwoConcurrentCancelsProduceOneCompensation(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-cancel-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if cancelErr := svc.CancelPayment(ctx, ses.ID); cancelErr != nil {
				mu.Lock()
				errs = append(errs, cancelErr)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, errs, "concurrent compensations should not return an error")

	_, _, cancelCalls := counting.callCounts()
	assert.Equal(t, 1, cancelCalls, "the PROVIDER should be called only once")

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, freshCol.AuthorizedAmount, "the hold should be released only ONCE")
}

// TestADeclinedFlowIsOpenToCompensation verifies end to end that the compensation runs
// when the saga's payment step blows up.
//
// Phase 6's DoD requires this. The decline is INJECTED with the behavior key
// written into the session's Data field, and because the key is stored with
// the session, the authorization behaves the same way in a SEPARATE request
// too.
func TestADeclinedFlowIsOpenToCompensation(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := newCollection(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "declined-" + col.ID,
		Data: map[string]any{
			manual.DataKeyOutcome:       manual.OutcomeDecline,
			manual.DataKeyDeclineReason: "test decline",
		},
	})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err, "the payment step MUST blow up so that the saga moves on to compensation")
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeAuthorizationDeclined, errors.CodeOf(err))

	declined, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionFailed, declined.Status, "the decline should be written PERMANENTLY")
	assert.Equal(t, "test decline", declined.DeclineReason)

	// Compensation: undoing the step that opened the session.
	require.NoError(t, svc.CancelPayment(ctx, ses.ID))
	require.NoError(t, svc.CancelPayment(ctx, ses.ID), "the compensation should be runnable again")

	closed, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, closed.Status)
	assert.Equal(t, "test decline", closed.DeclineReason, "the decline reason should be kept")

	providerSession, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled, providerSession.Status)
}

// TestAnInjectedProviderErrorRollsTheOperationBack verifies that nothing is written
// when the provider cannot be reached.
//
// The difference between a decline and an error shows here: an error has to be
// RETRYABLE, so the session must stay "pending" and the same request must be
// repeatable.
func TestAnInjectedProviderErrorRollsTheOperationBack(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "provider-error-" + col.ID,
		Data:           map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "error: %v", err)

	freshSession, err := svc.GetPaymentSession(ctx, ses.ID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionPending, freshSession.Status, "the status should not change")

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, freshCol.AuthorizedAmount)
	assert.Equal(t, models.CollectionAwaiting, freshCol.Status)
}

// TestTheProviderStateLivesOutsideTheProcess verifies that the manual provider's
// state is kept in the database, NOT IN MEMORY.
//
// Setting up a new provider instance imitates the process restarting: a ledger
// kept in memory would have been reset at this point and the session would say
// "not found". The e2e flows and the Phase 9 load test must be able to find a
// session that was opened before the process restarted; the saga compensation
// has to work in exactly that scenario too.
func TestTheProviderStateLivesOutsideTheProcess(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "restart-" + col.ID,
	})
	require.NoError(t, err)

	// "The process restarted": a completely NEW provider and service instance.
	restartedSvc, restartedProv := newService(t)

	providerSession, err := restartedProv.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err, "the provider session should still be found after the restart")
	assert.Equal(t, models.SessionPending, providerSession.Status)

	authorized, err := restartedSvc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err, "authorization should work after the restart")
	assert.Equal(t, models.SessionAuthorized, authorized.Status)

	require.NoError(t, restartedSvc.CancelPayment(ctx, ses.ID),
		"the compensation should work after the restart too")
}

// TestTheSameKeyOpensOneSessionInTheProviderLedgerToo verifies that the
// provider's own idempotency constraint is really enforced.
//
// Even if the module's record were deleted, the provider must not open a
// second session with the same key; the constraint is the last line of defense
// and is tested by going to the provider directly.
func TestTheSameKeyOpensOneSessionInTheProviderLedgerToo(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := newCollection(ctx, t, svc)
	key := "provider-idem-" + col.ID

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: key,
	})
	require.NoError(t, err)

	var count int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM payment_manual_sessions WHERE idempotency_key = $1`,
		key).Scan(&count))
	assert.Equal(t, int64(1), count)

	providerSession, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, key, providerSession.IdempotencyKey)
	assert.Equal(t, col.ID, providerSession.Reference,
		"the provider should keep the collection id for reconciliation")
}

// TestTheDatabaseConstraintsAreTheLastDefense verifies that the schema protects the
// money even when the service is bypassed.
//
// The constraints are not a copy of the service layer; they make sure that an
// intervention made with DIRECT SQL cannot write a negative amount either,
// cannot set an undefined status, and cannot refund money that does not exist.
func TestTheDatabaseConstraintsAreTheLastDefense(t *testing.T) {
	ctx := context.Background()

	tests := map[string]string{
		"negative amount": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                          VALUES ('paycol_neg', 'cart_x', -1, 'TRY')`,
		"zero amount": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                        VALUES ('paycol_zero', 'cart_x', 0, 'TRY')`,
		"invalid currency": `INSERT INTO payment_collections (id, reference, amount, currency_code)
                                 VALUES ('paycol_cur', 'cart_x', 100, 'try')`,
		"unknown status": `INSERT INTO payment_collections (id, reference, amount, currency_code, status)
                             VALUES ('paycol_st', 'cart_x', 100, 'TRY', 'paid')`,
		"refund above the capture": `INSERT INTO payment_collections
                                   (id, reference, amount, currency_code, captured_amount, refunded_amount)
                                   VALUES ('paycol_ref', 'cart_x', 100, 'TRY', 10, 20)`,
		// The collection is the CEILING of the money to be collected: a hold or a
		// capture above it means taking more than the order from the customer. The
		// service already refuses this; the constraint stops an intervention made
		// with direct SQL too.
		"hold above the collection": `INSERT INTO payment_collections
                                      (id, reference, amount, currency_code, authorized_amount)
                                      VALUES ('paycol_auth', 'cart_x', 100, 'TRY', 101)`,
		"capture above the collection": `INSERT INTO payment_collections
                                         (id, reference, amount, currency_code, captured_amount)
                                         VALUES ('paycol_cap', 'cart_x', 100, 'TRY', 101)`,
	}

	for name, stmt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, stmt)
			require.Error(t, err, "the constraint should be enforced")
		})
	}
}

// TestThePartialCaptureStatusIsDefinedInTheSchema verifies that the new derived status is
// in the status CHECK list.
//
// The status column is guarded by an allow-list: a value not written into the
// list blows up the transaction the moment the service derives it, and the
// error would show only in a production flow that makes a PARTIAL capture.
func TestThePartialCaptureStatusIsDefinedInTheSchema(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO payment_collections (id, reference, amount, currency_code, status, captured_amount)
         VALUES ('paycol_partial', 'cart_x', 100, 'TRY', $1, 1)`,
		models.CollectionPartiallyCaptured.String())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `DELETE FROM payment_collections WHERE id = 'paycol_partial'`)
	require.NoError(t, err)
}

// TestTheModuleRegistersItsNamesInTheContainer verifies that the surfaces the module
// publishes can be resolved from the container BY NAME.
//
// This is the runtime counterpart of ADR 0001/0004/0006: consumers reach this
// module only by name, WITHOUT importing it. A misspelled name or a forgotten
// registration shows up not at compile time but only here.
func TestTheModuleRegistersItsNamesInTheContainer(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	// The link service is provided too, and that is not optional: the module now
	// declares the "order_payment" definition at startup (ADR 0005), so it cannot
	// register without it. The product module carries the same requirement.
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))
	// The event bus is mandatory too (ADR 0121): the module publishes money
	// movements, and a lost money event has no compensation. The refusal itself
	// is verified in a separate test.
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	mod := payment.New()
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, payment.ServiceName)
	require.NoError(t, err)
	assert.NotNil(t, svc)

	iop, err := container.Resolve[*service.Interop](c, payment.InteropName)
	require.NoError(t, err)
	assert.NotNil(t, iop)

	registry, err := container.Resolve[*service.ProviderRegistry](c, payment.ProvidersName)
	require.NoError(t, err)
	assert.Equal(t, []string{giftcard.ID}, registry.IDs(),
		"the gift card is registered in every installation (ADR 0208), and the manual provider "+
			"only where it is asked for (ADR 0283)")

	provider, err := container.Resolve[query.Provider](c, payment.ProviderName)
	require.NoError(t, err)
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestTheModuleDoesNotRegisterWithoutABus verifies that the event bus is MANDATORY.
//
// Were it optional, an installation without a bus would look healthy and say
// nothing: captures work, refunds work, and the order's record never learns of
// them. A lost money event has no compensation — that is why the error is
// raised at startup, not at the first money movement.
func TestTheModuleDoesNotRegisterWithoutABus(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))

	err := payment.New().Register(ctx, c)

	require.Error(t, err, "an installation without a bus should stop AT STARTUP")
	assert.Contains(t, err.Error(), "core.eventbus",
		"the error should name the missing service BY NAME; that is what the operator has to fix")
}

// TestTheInteropFlowEndToEndOnTheRealDatabase verifies that the PRIMITIVE
// surface the saga will use works on a real database.
//
// The surface's SIGNATURE is now checked at compile time (ADR 0136): the pin
// file in internal/arch assigns this type to the interface its consumer
// declares, so a missing method breaks the build.
//
// That is not what this test proves, and it never was: it shows that the
// primitive surface runs with REAL dependencies, that is, that the SQL, the
// transaction and the returned values are right. A signature can be checked,
// behavior cannot.
func TestTheInteropFlowEndToEndOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSession(ctx, colID, manual.ID, "interop-"+colID)
	require.NoError(t, err)

	status, held, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), status)
	assert.Equal(t, testAmount, held, "the surface should carry the held AMOUNT too")

	payID, err := iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	colStatus, colAmount, _, colCaptured, _, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionCaptured.String(), colStatus)
	assert.Equal(t, testAmount, colAmount)
	assert.Equal(t, testAmount, colCaptured, "the saga should be able to verify from the number that the payment is FULL")

	refundID, err := iop.Refund(ctx, payID, 0, "interop refund")
	require.NoError(t, err)
	assert.NotEmpty(t, refundID)

	colStatus, _, _, _, colRefunded, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionRefunded.String(), colStatus)
	assert.Equal(t, testAmount, colRefunded)
}

// TestTheInteropShortPaymentOnTheRealDatabase verifies on a real database and the
// REAL provider that the saga can see from the primitive surface that the
// payment is SHORT.
//
// Phase 6's payment bypass was exactly here: when the provider authorized
// partially the status still looked "authorized", on a partial capture the
// collection still looked "captured", and there was no number for the saga to
// look at.
func TestTheInteropShortPaymentOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)
	sesID, err := iop.OpenSessionWithData(ctx, colID, manual.ID, "interop-partial-"+colID,
		[]byte(`{"manual_authorized_amount":1}`))
	require.NoError(t, err)

	status, held, err := iop.Authorize(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionAuthorized.String(), status)
	assert.Equal(t, int64(1), held, "the provider held only 1 unit")

	_, err = iop.Capture(ctx, sesID, 0)
	require.NoError(t, err)

	colStatus, colAmount, colHeld, colCaptured, _, err := iop.Collection(ctx, colID)
	require.NoError(t, err)
	assert.Equal(t, models.CollectionPartiallyCaptured.String(), colStatus)
	assert.Equal(t, testAmount, colAmount)
	assert.Zero(t, colHeld, "the hold that was not captured should not be left hanging")
	assert.Equal(t, int64(1), colCaptured)
	assert.Less(t, colCaptured, colAmount, "with this comparison the saga should not confirm the order")
}

// TestTwoFullSessionsCannotOpenOnOneCollectionOnTheRealDatabase verifies that
// the gate to DOUBLE CAPTURE is closed with real queries too.
//
// This was the finding's scenario: two sessions for the FULL amount, opened
// while neither was authorized, captured twice the collection once both were
// authorized and captured. The remaining amount's calculation has to count the
// open sessions, and that can be proven only with the real aggregate query.
func TestTwoFullSessionsCannotOpenOnOneCollectionOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)

	_, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "double-1-" + col.ID,
	})
	require.NoError(t, err)

	_, err = svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "double-2-" + col.ID,
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeCollectionClosed, errors.CodeOf(err))
}

// TestAPartialCaptureOnTheRealDatabase verifies that a partial capture is
// written to both ledgers the same way.
//
// If the hold that was not captured is not released, the session cannot be
// canceled again because it is "captured", and the amount stays hanging
// forever; a divergence between the provider's ledger and the module's record
// is also visible only by looking at both sides at once.
func TestAPartialCaptureOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, prov := newService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "partial-capture-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	_, err = svc.CapturePayment(ctx, ses.ID, 1)
	require.NoError(t, err)

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Zero(t, freshCol.AuthorizedAmount, "the hold that was not captured should NOT REMAIN on the collection")
	assert.Equal(t, int64(1), freshCol.CapturedAmount)
	assert.Equal(t, models.CollectionPartiallyCaptured, freshCol.Status)

	providerSession, err := prov.GetSession(ctx, ses.ExternalID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), providerSession.AuthorizedAmount,
		"the provider's ledger should release the remaining hold too")
	assert.Equal(t, int64(1), providerSession.CapturedAmount)

	require.Error(t, svc.CancelPayment(ctx, ses.ID),
		"a captured session cannot be canceled; the release has to happen at capture time")
}

// TestTheInteropCanCompensateADeclinedSession verifies that the saga can compensate
// the payment step that blew up through the primitive surface.
func TestTheInteropCanCompensateADeclinedSession(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	iop := service.NewInterop(svc)

	colID, err := iop.CreateCollection(ctx, testReference, "", testCurrency, testAmount)
	require.NoError(t, err)

	sesID, err := iop.OpenSessionWithData(ctx, colID, manual.ID, "interop-decline-"+colID,
		[]byte(`{"manual_outcome":"decline","manual_decline_reason":"saga test"}`))
	require.NoError(t, err)

	_, _, err = iop.Authorize(ctx, sesID)
	require.Error(t, err, "the payment step should blow up")

	require.NoError(t, iop.Cancel(ctx, sesID))
	require.NoError(t, iop.Cancel(ctx, sesID))

	status, err := iop.SessionStatus(ctx, sesID)
	require.NoError(t, err)
	assert.Equal(t, models.SessionCanceled.String(), status)
}

// TestTheQueryProviderOnTheRealDatabase verifies that the read surface opened
// to the Query layer works on real rows (ADR 0004).
func TestTheQueryProviderOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)
	p := service.NewQueryProvider(svc)

	records, err := p.FetchByIDs(ctx, []string{col.ID, "paycol_MISSING"},
		[]string{service.FieldID, service.FieldReference, service.FieldAmount, service.FieldStatus})

	require.NoError(t, err)
	require.Len(t, records, 1, "NO record is returned for an id that is not found")
	assert.Equal(t, col.ID, records[0][service.FieldID])
	assert.Equal(t, testReference, records[0][service.FieldReference])
	assert.Equal(t, testAmount, records[0][service.FieldAmount])
	assert.Equal(t, models.CollectionNotPaid.String(), records[0][service.FieldStatus])
}

// TestConcurrentSeparateSessionsDoNotLoseTheCollectionAmount verifies that the
// collection row lock prevents a LOST UPDATE.
//
// Two SEPARATE sessions on the same collection, half and half, are authorized
// at the same time. The right result is their SUM. Without the collection lock
// both flows would read the held amount as zero, each would write its own
// amount, and the last writer would OVERWRITE the other — the collection would
// look half paid and the capture step would take too little money.
//
// This claim is different from the "single authorization" claim and cannot be
// tested in the same test: there the same session races, here DIFFERENT
// sessions do.
func TestConcurrentSeparateSessionsDoNotLoseTheCollectionAmount(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	col := newCollection(ctx, t, svc)

	half := testAmount / 2
	first, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		Amount:         half,
		IdempotencyKey: "split-1-" + col.ID,
	})
	require.NoError(t, err)
	second, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		Amount:         half,
		IdempotencyKey: "split-2-" + col.ID,
	})
	require.NoError(t, err)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	wg.Add(2)
	for _, sessionID := range []string{first.ID, second.ID} {
		go func() {
			defer wg.Done()
			if _, authErr := svc.AuthorizePayment(ctx, sessionID); authErr != nil {
				mu.Lock()
				errs = append(errs, authErr)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Empty(t, errs)

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, freshCol.AuthorizedAmount,
		"the held amounts of the two sessions should ADD UP; one should not overwrite the other")
	assert.Equal(t, models.CollectionAuthorized, freshCol.Status)
}

// TestTwoConcurrentCapturesProduceOneCapture verifies under a race that only ONE
// capture comes out of a session.
//
// Without the session lock both flows would see the session as "authorized",
// both would try to write a capture row and would hit the unique index: one of
// them gets errors.Conflict. The "there should be no error at all" assertion
// below catches exactly that — the lock makes the second flow fall through to a
// no-op rather than the constraint blowing up.
func TestTwoConcurrentCapturesProduceOneCapture(t *testing.T) {
	ctx := context.Background()
	svc, counting := newCountingService(t)
	col := newCollection(ctx, t, svc)
	ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
		IdempotencyKey: "concurrent-capture-" + col.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)

	const goroutines = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
		ids  = map[string]int{}
	)
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			pay, capErr := svc.CapturePayment(ctx, ses.ID, 0)

			mu.Lock()
			defer mu.Unlock()
			if capErr != nil {
				errs = append(errs, capErr)
				return
			}
			ids[pay.ID]++
		}()
	}
	wg.Wait()

	assert.Empty(t, errs, "concurrent captures should not return an error")
	assert.Len(t, ids, 1, "every call should return the SAME capture")

	_, captureCalls, _ := counting.callCounts()
	assert.Equal(t, 1, captureCalls, "the PROVIDER should be called only once")

	payments, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)
	assert.Len(t, payments, 1, "the database should hold a single capture row")

	freshCol, err := svc.GetPaymentCollection(ctx, col.ID)
	require.NoError(t, err)
	assert.Equal(t, testAmount, freshCol.CapturedAmount,
		"the captured amount should equal ONE capture, not a multiple of it")
}

// --- store credit ------------------------------------------------------------

// TestTheManualProviderIsRegisteredWhereItIsAskedFor is the companion of
// [TestTheModuleRegistersItsNamesInTheContainer] for the manual provider: the default
// leaves it out, and the setting is what puts it in (ADR 0283).
func TestTheManualProviderIsRegisteredWhereItIsAskedFor(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	require.NoError(t, payment.New(payment.Options{ManualProvider: true}).Register(ctx, c))

	registry, err := container.Resolve[*service.ProviderRegistry](c, payment.ProvidersName)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{giftcard.ID, manual.ID}, registry.IDs())
}

// TestTheModuleRegistersTheBalanceProvidersWhileTheSettingIsOn proves the providers are
// registered while the setting is ON.
//
// It is the companion of [TestTheModuleRegistersItsNamesInTheContainer], which pins that
// a default installation registers only the gift card; that claim alone would
// hold if the providers were never registered at all. Together the two say the
// refusal is the SETTING's decision — and that setting is a security decision:
// in an installation that trusts the customer claim without proof, a
// person-bound tender would mean anybody who types someone's name spends their
// balance (ADR 0152). The setting is ONE and opens both tenders (ADR 0165):
// there is no installation where credit is registered and points are not.
func TestTheModuleRegistersTheBalanceProvidersWhileTheSettingIsOn(t *testing.T) {
	ctx := context.Background()
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", link.New(testPool, slog.New(slog.DiscardHandler))))
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	mod := payment.New(payment.Options{PersonBoundTenders: true})
	require.NoError(t, mod.Register(ctx, c))

	registry, err := container.Resolve[*service.ProviderRegistry](c, payment.ProvidersName)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{giftcard.ID, storecredit.ID, loyaltypoints.ID}, registry.IDs(),
		"with the setting on, store credit and loyalty points are tenders too, beside the gift card")
}

// lockWaiters counts the requests WAITING on a lock the given backend holds.
//
// Narrowing the waiters to a known blocker is what makes the count mean
// anything: "somebody in this database waits on a lock" is also true of another
// test's session, and then the assertion would hold before the request under
// test had run a single statement — green, and measuring nothing.
//
// It returns its error instead of asserting it, because it is polled: the
// condition of require.Eventually runs on a goroutine of its own, where a
// failed require is a runtime.Goexit of the wrong goroutine rather than a
// failed test.
func lockWaiters(ctx context.Context, blockerPID int32) (int64, error) {
	var waiters int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM pg_stat_activity
         WHERE datname = current_database()
           AND wait_event_type = 'Lock'
           AND $1 = ANY(pg_blocking_pids(pid))`, blockerPID).Scan(&waiters)

	return waiters, err
}

// singleConnectionRepository is a repository on a pool of exactly ONE
// connection, and the backend that connection runs on.
//
// It is how a competing transaction goes through the repository's own code and
// still has a backend the test knows before the transaction begins. The earlier
// competitors took the lock with a copy of its SQL, and a copy is what would
// have gone on proving the old lock's shape after the lock itself had changed.
func singleConnectionRepository(
	ctx context.Context, t *testing.T,
) (repo *repository.Repository, backendPID int32) {
	t.Helper()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns, cfg.MinConns = 1, 1
	pool, err := db.New(ctx, cfg, nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, pool.Pool().QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID))

	return repository.New(pool.Pool()), backendPID
}

// balanceTenderService builds a real-repository service with both balance
// tenders and the manual provider registered, earning at the ceiling rate, on a
// pool whose connections start with the given default isolation level — empty
// being the server's own.
//
// A pool at another level is built with pgxpool directly, because core/db
// refuses to build one (ADR 0166). That refusal is the installation's guard;
// this case proves the payment repository's own, which names READ COMMITTED on
// every transaction it begins and so holds on a pool nobody guarded (D119).
func balanceTenderService(t *testing.T, defaultIsolation string) *service.Service {
	t.Helper()

	pool := testPool.Pool()
	if defaultIsolation != "" {
		dsn := testDSN + "&default_transaction_isolation=" + url.QueryEscape(defaultIsolation)
		own, err := pgxpool.New(context.Background(), dsn)
		require.NoError(t, err)
		t.Cleanup(own.Close)

		var level string
		require.NoError(t, own.QueryRow(context.Background(),
			`SHOW default_transaction_isolation`).Scan(&level))
		require.Equal(t, defaultIsolation, level,
			"the pool's connections have to start at the level under test, or the case "+
				"proves the server's default again")
		pool = own
	}

	repo := repository.New(pool)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(storecredit.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	return svc
}

// balanceTender is what the lock test needs to know about one tender.
type balanceTender struct {
	name       string
	providerID string
	// fund puts testAmount on the balance the only way the module has: an
	// operator's issue for credit, a capture's earn for points.
	fund func(ctx context.Context, t *testing.T, svc *service.Service, customer string)
	// balance reads it.
	balance func(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64
	// lock and spend are the competitor's two steps, through the repository.
	lock  func(ctx context.Context, repo *repository.Repository, customer string) error
	spend func(ctx context.Context, repo *repository.Repository, customer string) error
}

// balanceTenders are the two tenders the shared machine runs.
func balanceTenders() []balanceTender {
	return []balanceTender{
		{
			name:       "store credit",
			providerID: storecredit.ID,
			fund: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) {
				t.Helper()
				_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
					CustomerID: customer, CurrencyCode: testCurrency, Amount: testAmount,
					Reason: "lock test",
				})
				require.NoError(t, err)
			},
			balance: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64 {
				t.Helper()
				balance, err := svc.StoreCreditBalance(ctx, customer, testCurrency)
				require.NoError(t, err)

				return balance
			},
			lock: func(ctx context.Context, repo *repository.Repository, customer string) error {
				return repo.LockStoreCreditBalance(ctx, customer, testCurrency)
			},
			spend: func(ctx context.Context, repo *repository.Repository, customer string) error {
				_, err := repo.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
					ID: models.NewStoreCreditEntryID(), CustomerID: customer,
					CurrencyCode: testCurrency, Amount: -testAmount,
					Kind: models.StoreCreditHold, Reference: "the competitor",
				})

				return err
			},
		},
		{
			name:       "loyalty points",
			providerID: loyaltypoints.ID,
			fund: func(ctx context.Context, t *testing.T, svc *service.Service, customer string) {
				t.Helper()
				earnPoints(ctx, t, svc, customer)
			},
			balance: pointBalance,
			lock: func(ctx context.Context, repo *repository.Repository, customer string) error {
				return repo.LockLoyaltyBalance(ctx, customer, testCurrency)
			},
			spend: func(ctx context.Context, repo *repository.Repository, customer string) error {
				_, err := repo.AppendLoyaltyEntry(ctx, models.LoyaltyEntry{
					ID: models.NewLoyaltyEntryID(), CustomerID: customer,
					CurrencyCode: testCurrency, Points: -testAmount,
					Kind: models.LoyaltyHold, Reference: "the competitor",
				})

				return err
			},
		},
	}
}

// TestAnAuthorizationWaitsOnTheBalanceLock proves the correctness argument of
// both balance tenders against a real server: the balance is read and acted on,
// so two authorizations of one customer must not both see the same money.
//
// The interleaving is FORCED, not hoped for. A competitor takes the balance
// lock through the repository's own function; that the authorization under test
// WAITS on it is seen through pg_blocking_pids; the competitor spends the whole
// balance and commits; only then does the authorization go on, and it has to
// read the fresh balance and decline. The balance covers ONE spend, so a lock
// that lets the two through together ends below zero.
//
// Three shapes, and each one is a way the lock was, or could be, not a lock:
//
//   - the customer had rows when the competitor locked: the shape ADR 0152 and
//     the first draft of ADR 0165 proved, and the only one a row lock passes;
//   - the customer had NO rows until the competitor held the lock, and the
//     money arrived while it did: a row lock took nothing from a customer with
//     none, the authorization locked the new row without waiting, and both
//     spent it (D118);
//   - the pool's connections default to REPEATABLE READ: the sum after the wait
//     then reads the snapshot taken before it, and the lock serializes two
//     authorizations that both decide on the balance before either hold (D119).
func TestAnAuthorizationWaitsOnTheBalanceLock(t *testing.T) {
	shapes := []struct {
		name                string
		fundedBeforeTheLock bool
		defaultIsolation    string
	}{
		{name: "the customer had rows", fundedBeforeTheLock: true},
		{name: "the customer had no rows until the lock was held"},
		{
			name:                "the server defaults to repeatable read",
			fundedBeforeTheLock: true,
			defaultIsolation:    "repeatable read",
		},
	}

	for _, tender := range balanceTenders() {
		for _, shape := range shapes {
			t.Run(tender.name+"/"+shape.name, func(t *testing.T) {
				ctx := context.Background()
				svc := balanceTenderService(t, shape.defaultIsolation)

				customer := "cus_" + models.NewPaymentCollectionID()
				if shape.fundedBeforeTheLock {
					tender.fund(ctx, t, svc, customer)
				}

				col := customerCollection(ctx, t, svc, customer, testAmount)
				ses, err := svc.CreateSession(ctx, col.ID, tender.providerID,
					service.CreateSessionInput{IdempotencyKey: tender.providerID + "-lock-" + col.ID})
				require.NoError(t, err)

				// --- the competitor: takes the balance and holds it ---

				rival, rivalPID := singleConnectionRepository(ctx, t)
				locked, spend := make(chan struct{}), make(chan struct{})
				rivalDone := make(chan error, 1)
				go func() {
					rivalDone <- rival.WithTx(ctx, func(ctx context.Context) error {
						if err := tender.lock(ctx, rival, customer); err != nil {
							return err
						}
						close(locked)
						<-spend

						return tender.spend(ctx, rival, customer)
					})
				}()
				select {
				case <-locked:
				case err := <-rivalDone:
					t.Fatalf("the competitor could not take the balance lock: %v", err)
				}

				if !shape.fundedBeforeTheLock {
					// The money a row lock could not see: committed while the
					// competitor holds the balance of a customer who had no row
					// for it to take.
					tender.fund(ctx, t, svc, customer)
				}
				require.Equal(t, testAmount, tender.balance(ctx, t, svc, customer),
					"the balance covers exactly ONE spend; covering both would let the "+
						"test tell nothing apart")

				// --- the authorization under test: it has to WAIT ---

				done := make(chan error, 1)
				go func() { _, authErr := svc.AuthorizePayment(ctx, ses.ID); done <- authErr }()

				var lastPollErr atomic.Value
				waited := assert.Eventually(t, func() bool {
					waiters, err := lockWaiters(ctx, rivalPID)
					if err != nil {
						lastPollErr.Store(err.Error())

						return false
					}

					return waiters > 0
				}, 10*time.Second, 10*time.Millisecond)

				// The competitor spends the balance and commits, whatever the poll
				// saw, so that nothing below waits on a transaction left open.
				close(spend)
				require.NoError(t, <-rivalDone)

				if !waited {
					pollErr, _ := lastPollErr.Load().(string)
					t.Fatalf("the authorization never waited on the competitor's balance lock, "+
						"so it decided on a balance somebody else was spending (last poll "+
						"error: %q)", pollErr)
				}

				// --- and it has to read the FRESH balance and decline ---

				select {
				case authErr := <-done:
					require.Error(t, authErr,
						"the authorization must NOT pass after the competitor spent the "+
							"balance: if it did, it read the balance from before the spend")
					assert.True(t, errors.IsConflict(authErr),
						"an insufficient balance is a DECLINE, not a server error: %v", authErr)
				case <-time.After(10 * time.Second):
					t.Fatal("the authorization did not finish after the competitor committed")
				}

				assert.Zero(t, tender.balance(ctx, t, svc, customer),
					"the balance has to stay at zero; below it the customer spent money "+
						"they did not hold twice over")
			})
		}
	}
}

// The gate that reads that the ledger is a record that is only APPENDED TO was
// MOVED from here: TestThePaymentLedgersAreAppendOnlyInSQL in internal/arch
// (ADR 0164).
//
// The version here had a FILE as its subject — a single path read by its name —
// and could not have seen the module's second ledger on the day it was added.
// The moved version's subject is the DIRECTORY; it looks for both tables by
// name, turns red instead of silently reading an empty string when the file
// moves, and runs in the fast lane rather than behind the integration tag.

// --- loyalty points ledger (ADR 0164) ----------------------------------------

// earningService builds a real-repository service that earns at the given rate.
func earningService(t *testing.T, basisPoints int64) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: basisPoints,
	})
	require.NoError(t, err)

	return svc
}

// TestACaptureWritesARealPointRow proves the ledger against the real schema.
//
// The unit tests prove the ARITHMETIC against a fake store. What only this test
// can see is that the row the arithmetic produces satisfies the table: the sign
// matches the kind, the currency matches the format, the reference is not blank
// and the points are not zero. A row that the service is happy with and the
// schema refuses would fail a capture whose money has already moved.
func TestACaptureWritesARealPointRow(t *testing.T) {
	ctx := context.Background()
	svc := earningService(t, 100)
	customer := "cus_" + models.NewPaymentCollectionID()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: testAmount,
		CurrencyCode: testCurrency, CustomerID: customer,
	})
	require.NoError(t, err)

	ses, err := svc.CreateSession(ctx, col.ID, manual.ID,
		service.CreateSessionInput{IdempotencyKey: "loyalty-" + col.ID})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	capture, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount*100/10_000, balance,
		"the balance is the target the captured money implies")

	rows, total, err := svc.ListLoyalty(ctx, service.ListLoyaltyInput{
		CustomerID: customer, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, models.LoyaltyEarn, rows[0].Kind)
	assert.Equal(t, col.ID, rows[0].Reference)
	assert.False(t, rows[0].CreatedAt.IsZero(), "the moment is stamped by the schema's default")

	_, err = svc.RefundPayment(ctx, capture.ID, testAmount/2, "half back")
	require.NoError(t, err)

	balance, err = svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount/2*100/10_000, balance,
		"half the money back is half the points back")
}

// TestTheLedgerRefusesASignThatContradictsItsKind proves the constraint pair.
//
// The sign is decided by the kind and the SCHEMA holds the pairing, which is the
// only reason a refund that was written positive — the mistake that hands a
// customer points for taking their money back — cannot enter the table. No Go
// code checks it, and none should: a check in the service is a check one caller
// can go around.
//
// The three spending kinds (ADR 0165) are in the table too, each with the sign
// its meaning forbids: a hold that ADDED points would let a checkout pay the
// customer for buying, and a release or a refund that took points away would
// charge them twice for a session that gave up or a payment they got back.
// Migration 000006 re-creates the pair under the same names; this is what says
// the re-created pair still binds.
func TestTheLedgerRefusesASignThatContradictsItsKind(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	customer := "cus_" + models.NewPaymentCollectionID()

	for _, bad := range []struct {
		name  string
		entry models.LoyaltyEntry
	}{
		{
			name: "a reverse written positive",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyReverse, Reference: "paycol_x",
			},
		},
		{
			name: "an earn written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "a hold written positive",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyHold, Reference: "lpses_x",
			},
		},
		{
			name: "a release written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyRelease, Reference: "lpses_x",
			},
		},
		{
			name: "a refund written negative",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: -10, Kind: models.LoyaltyRefund, Reference: "lpses_x",
			},
		},
		{
			name: "a kind outside the vocabulary",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyKind("bonus"), Reference: "paycol_x",
			},
		},
		{
			name: "no points at all",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 0, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "a row belonging to no collection",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyEarn, Reference: "   ",
			},
		},
		{
			name: "a currency that is not a code",
			entry: models.LoyaltyEntry{
				CustomerID: customer, CurrencyCode: "try",
				Points: 10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
		{
			name: "points belonging to nobody",
			entry: models.LoyaltyEntry{
				CustomerID: "  ", CurrencyCode: testCurrency,
				Points: 10, Kind: models.LoyaltyEarn, Reference: "paycol_x",
			},
		},
	} {
		t.Run(bad.name, func(t *testing.T) {
			entry := bad.entry
			entry.ID = models.NewLoyaltyEntryID()

			_, err := repo.AppendLoyaltyEntry(ctx, entry)

			require.Error(t, err, "the schema has to refuse this row")
		})
	}
}

// TestTwoConcurrentCapturesEarnTheTargetOnce proves the serialization the
// decision rests on, with real row locks.
//
// Each capture computes a TARGET and appends the difference between it and what
// the collection has already been written. That read and that write are inside
// the collection's own lock, so two captures on the same collection queue up and
// the second sees the first one's row. Without the lock both would read nothing
// written, both would append their own whole target, and the customer would hold
// twice the points the money earned.
//
// A unit test cannot see this: the fake store has no transactions and no rows to
// lock, so it would be asserting about a mechanism it does not have.
func TestTwoConcurrentCapturesEarnTheTargetOnce(t *testing.T) {
	ctx := context.Background()
	svc := earningService(t, 100)
	customer := "cus_" + models.NewPaymentCollectionID()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: testReference, Amount: testAmount,
		CurrencyCode: testCurrency, CustomerID: customer,
	})
	require.NoError(t, err)

	// TWO sessions, each holding half, so both captures are real and both move
	// the collection's captured total.
	half := testAmount / 2
	sessions := make([]models.PaymentSession, 0, 2)
	for i := range 2 {
		ses, err := svc.CreateSession(ctx, col.ID, manual.ID, service.CreateSessionInput{
			IdempotencyKey: fmt.Sprintf("loyalty-race-%s-%d", col.ID, i),
			Amount:         half,
		})
		require.NoError(t, err)
		_, err = svc.AuthorizePayment(ctx, ses.ID)
		require.NoError(t, err)
		sessions = append(sessions, ses)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(sessions))
	start := make(chan struct{})
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.CapturePayment(ctx, sessions[i].ID, 0)
		}()
	}
	close(start)
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i], "both captures have to succeed")
	}

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, testAmount*100/10_000, balance,
		"the points are the target the whole captured amount implies, whatever order "+
			"the two captures ran in; a balance of twice this would mean each capture "+
			"read a ledger the other had not written to yet")
}

// --- the loyalty-points tender (ADR 0165) -------------------------------------

// pointsService builds a real-repository service with BOTH the manual provider
// and the loyalty-points tender registered, earning at the ceiling rate.
//
// The ceiling — one point per minor unit — is chosen so that ONE manual capture
// of testAmount leaves the customer holding exactly testAmount points, which is
// what a spend of testAmount needs and not a point more. Points are only ever
// EARNED: the ledger has no issue endpoint, so every balance below starts as a
// capture through the manual provider. The repository is shared between the
// service and the tender, as it is for store credit: the hold and the session
// state are written in one transaction, or not at all.
func pointsService(t *testing.T) *service.Service {
	t.Helper()

	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	return svc
}

// customerCollection opens a collection of the given amount that NAMES the
// customer, so the tender has an owner and the earn path a recipient.
func customerCollection(
	ctx context.Context, t *testing.T, svc *service.Service, customer string, amount int64,
) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    testReference + "-points",
		CustomerID:   customer,
		Amount:       amount,
		CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	return col
}

// payThrough opens a session at the given provider for the given amount — zero
// being the collection's remainder — authorizes it and captures it. It returns
// the module's session, whose ExternalID is the provider's own, and the capture.
func payThrough(
	ctx context.Context, t *testing.T, svc *service.Service,
	col models.PaymentCollection, providerID string, amount int64,
) (models.PaymentSession, models.Payment) {
	t.Helper()

	ses, err := svc.CreateSession(ctx, col.ID, providerID, service.CreateSessionInput{
		IdempotencyKey: fmt.Sprintf("%s-%s-%d", providerID, col.ID, amount),
		Amount:         amount,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	capture, err := svc.CapturePayment(ctx, ses.ID, 0)
	require.NoError(t, err)

	return ses, capture
}

// earnPoints gives the customer testAmount points the only way there is: a
// manual capture of testAmount at the ceiling rate. It returns the collection
// that earned them and the capture, so a test can refund it.
func earnPoints(
	ctx context.Context, t *testing.T, svc *service.Service, customer string,
) (models.PaymentCollection, models.Payment) {
	t.Helper()

	col := customerCollection(ctx, t, svc, customer, testAmount)
	_, capture := payThrough(ctx, t, svc, col, manual.ID, 0)

	return col, capture
}

// pointBalance reads the customer's points in the test currency.
func pointBalance(ctx context.Context, t *testing.T, svc *service.Service, customer string) int64 {
	t.Helper()

	balance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)

	return balance
}

// pointRows reads the customer's whole point history in the test currency.
func pointRows(ctx context.Context, t *testing.T, svc *service.Service, customer string) []models.LoyaltyEntry {
	t.Helper()

	rows, total, err := svc.ListLoyalty(ctx, service.ListLoyaltyInput{
		CustomerID: customer, CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	require.Len(t, rows, int(total), "the whole history fits in one page")

	return rows
}

// rowsReferencing keeps the rows whose reference is the given identifier.
func rowsReferencing(rows []models.LoyaltyEntry, reference string) []models.LoyaltyEntry {
	var out []models.LoyaltyEntry
	for _, row := range rows {
		if row.Reference == reference {
			out = append(out, row)
		}
	}

	return out
}

// rowsOfKind keeps the rows of the given kind.
func rowsOfKind(rows []models.LoyaltyEntry, kind models.LoyaltyKind) []models.LoyaltyEntry {
	var out []models.LoyaltyEntry
	for _, row := range rows {
		if row.Kind == kind {
			out = append(out, row)
		}
	}

	return out
}

// TestACapturePaidWithPointsEarnsNothing proves the exclusion the earn rule
// gained in ADR 0165, against the real query that joins captures to sessions.
//
// At the ceiling rate a capture paid with points would earn itself back: the
// customer spends testAmount points, the capture earns testAmount points, and
// one point buys unbounded goods. The earn target is therefore the net of the
// collection's captures whose session is at ANOTHER provider, and this test
// reads it two ways: an order paid entirely with points earns nothing at all,
// and an order split between points and the manual provider earns on the
// manual half only. The split is the module's, not the storefront's — the
// storefront pays an order with one tender — and the module has to get it
// right for the admin surface that can split.
func TestACapturePaidWithPointsEarnsNothing(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()
	earning, _ := earnPoints(ctx, t, svc, customer)

	// --- an order paid entirely with points ---

	paid := customerCollection(ctx, t, svc, customer, testAmount)
	ses, _ := payThrough(ctx, t, svc, paid, loyaltypoints.ID, 0)

	assert.Zero(t, pointBalance(ctx, t, svc, customer),
		"the points were spent and the spend earned none back")

	rows := pointRows(ctx, t, svc, customer)
	assert.Empty(t, rowsReferencing(rows, paid.ID),
		"no row may reference the collection paid with points: nothing was earned on it")
	require.Len(t, rows, 2, "one earn and one hold, and nothing else")

	holds := rowsReferencing(rows, ses.ExternalID)
	require.Len(t, holds, 1, "the hold references the provider's OWN session")
	assert.Equal(t, models.LoyaltyHold, holds[0].Kind)
	assert.Equal(t, -testAmount, holds[0].Points)

	earns := rowsReferencing(rows, earning.ID)
	require.Len(t, earns, 1)
	assert.Equal(t, models.LoyaltyEarn, earns[0].Kind)

	// --- an order split between points and the manual provider ---

	earnPoints(ctx, t, svc, customer)
	require.Equal(t, testAmount, pointBalance(ctx, t, svc, customer))

	half := testAmount / 2
	split := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, split, loyaltypoints.ID, half)
	payThrough(ctx, t, svc, split, manual.ID, 0)

	var earnedOnSplit int64
	for _, row := range rowsReferencing(pointRows(ctx, t, svc, customer), split.ID) {
		assert.Contains(t, []models.LoyaltyKind{models.LoyaltyEarn, models.LoyaltyReverse}, row.Kind,
			"a row referencing a collection is an earn or a reverse, never a spend")
		earnedOnSplit += row.Points
	}
	manualHalfEarns := half * service.MaxLoyaltyEarnBasisPoints / 10_000
	assert.Equal(t, manualHalfEarns, earnedOnSplit,
		"only the half that came through the manual provider earns; the half that came "+
			"out of the point ledger is a liability being extinguished, not revenue")
	assert.Equal(t, testAmount-half+manualHalfEarns, pointBalance(ctx, t, svc, customer))
}

// TestCreditThatIsSpentStillEarns is the other half of the exclusion, against
// the real query: only the POINTS tender is left out of the earn base.
//
// Store credit is money the shop owes at face value — a refund that stayed in
// the shop — and an order paid with it is revenue the day the credit is spent;
// points are the programme's own currency and earning on them would let a point
// earn itself back. An exclusion that grew to "every balance tender" would
// answer both with nothing, and no other test would notice, because every other
// earn in this file is a manual capture.
func TestCreditThatIsSpentStillEarns(t *testing.T) {
	ctx := context.Background()
	svc := balanceTenderService(t, "")

	customer := "cus_" + models.NewPaymentCollectionID()
	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: testCurrency, Amount: testAmount,
		Reason: "a late delivery",
	})
	require.NoError(t, err)

	col := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, col, storecredit.ID, 0)

	credit, err := svc.StoreCreditBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	require.Zero(t, credit, "the order spent the whole credit")

	earned := rowsReferencing(pointRows(ctx, t, svc, customer), col.ID)
	require.Len(t, earned, 1, "the credit-paid capture earned, once")
	assert.Equal(t, models.LoyaltyEarn, earned[0].Kind)
	assert.Equal(t, testAmount*service.MaxLoyaltyEarnBasisPoints/10_000, earned[0].Points,
		"credit earns like the money it stands for")
}

// TestARefundOfAPointsPaidOrderGivesThePointsBack proves the refund half of the
// state machine on the real ledger.
//
// A points tender holds no card and no account, so a refund has one destination:
// a positive row in the customer's own balance, at FACE VALUE — a point is worth
// one minor unit in both directions. The row references the provider's own
// session and not the collection, because the earn target is recomputed per
// collection from the rows that reference it, and a refund row carrying the
// collection id would read as points the collection had been written. And no
// reverse appears: the collection earned nothing, so there is nothing to
// reverse.
func TestARefundOfAPointsPaidOrderGivesThePointsBack(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()
	earnPoints(ctx, t, svc, customer)

	paid := customerCollection(ctx, t, svc, customer, testAmount)
	ses, capture := payThrough(ctx, t, svc, paid, loyaltypoints.ID, 0)
	require.Zero(t, pointBalance(ctx, t, svc, customer))

	_, err := svc.RefundPayment(ctx, capture.ID, testAmount/2, "half back")
	require.NoError(t, err)

	assert.Equal(t, testAmount/2, pointBalance(ctx, t, svc, customer),
		"half the money back is half the points back, at face value and not at the earn rate")

	rows := pointRows(ctx, t, svc, customer)
	require.Len(t, rows, 3, "an earn, a hold and a refund")

	refunds := rowsOfKind(rows, models.LoyaltyRefund)
	require.Len(t, refunds, 1, "the row a refund writes is a REFUND, not a release")
	assert.Equal(t, testAmount/2, refunds[0].Points)
	assert.Equal(t, ses.ExternalID, refunds[0].Reference,
		"the refund references the PROVIDER's session, the one the hold references")
	assert.True(t, strings.HasPrefix(refunds[0].Reference, models.LoyaltySessionIDPrefix),
		"the reference is the tender's own identifier: %s", refunds[0].Reference)
	assert.NotEqual(t, paid.ID, refunds[0].Reference,
		"a spend row never references a collection")

	assert.Empty(t, rowsOfKind(rows, models.LoyaltyReverse),
		"nothing was earned on this collection, so nothing is reversed")
	assert.Empty(t, rowsReferencing(rows, paid.ID))
}

// TestABalanceBelowZeroIsAState proves the sentence ADR 0165 wrote about the
// hole: a balance can fall below zero, the refund that makes it fall is never
// refused, the tender declines against it and the next earn fills it first.
//
// The points a capture earned may already be spent when that capture is
// refunded. The reverse is written all the same — a refund is money going back
// and the ledger records what happened — so the balance goes negative. That is a
// STATE, not a fault: nothing repairs it, the tender simply reads a balance that
// covers nothing, and the next capture's earn is where the hole closes.
//
// No mutation is needed for the refund half: the assertion that it is not
// refused is a require.NoError on the real path, and Service.RefundPayment reads
// no balance anywhere between its lock and its ledger write.
func TestABalanceBelowZeroIsAState(t *testing.T) {
	ctx := context.Background()
	svc := pointsService(t)
	customer := "cus_" + models.NewPaymentCollectionID()

	earning, earningCapture := earnPoints(ctx, t, svc, customer)
	spent := customerCollection(ctx, t, svc, customer, testAmount)
	payThrough(ctx, t, svc, spent, loyaltypoints.ID, 0)
	require.Zero(t, pointBalance(ctx, t, svc, customer), "everything earned is spent")

	// --- the capture that earned is refunded in full ---

	_, err := svc.RefundPayment(ctx, earningCapture.ID, 0, "everything back")
	require.NoError(t, err,
		"a refund is never refused because the points it reverses were spent already")

	assert.Equal(t, -testAmount, pointBalance(ctx, t, svc, customer),
		"the reverse takes back what was earned, and what was earned is gone: the hole")

	reverses := rowsOfKind(pointRows(ctx, t, svc, customer), models.LoyaltyReverse)
	require.Len(t, reverses, 1)
	assert.Equal(t, -testAmount, reverses[0].Points)
	assert.Equal(t, earning.ID, reverses[0].Reference,
		"the reverse references the collection whose earn it undoes")

	// --- the tender declines against the hole ---

	tiny := customerCollection(ctx, t, svc, customer, 1)
	tinySes, err := svc.CreateSession(ctx, tiny.ID, loyaltypoints.ID,
		service.CreateSessionInput{IdempotencyKey: "points-hole-" + tiny.ID})
	require.NoError(t, err)

	_, err = svc.AuthorizePayment(ctx, tinySes.ID)
	require.Error(t, err, "one minor unit is more than a negative balance covers")
	assert.True(t, errors.IsConflict(err),
		"a balance that does not cover the payment is a DECLINE, not a server error: %v", err)
	assert.Equal(t, -testAmount, pointBalance(ctx, t, svc, customer),
		"a declined authorization writes no hold")

	// --- the next earn fills the hole first ---

	filling := customerCollection(ctx, t, svc, customer, testAmount+1)
	payThrough(ctx, t, svc, filling, manual.ID, 0)

	assert.Equal(t, int64(1), pointBalance(ctx, t, svc, customer),
		"the earn lands on the negative balance: what is left to spend is one point")
}

// TestBothTendersAnswerReconciliation proves that the hourly reconciliation can
// ASK both balance tenders about real sessions.
//
// Service.reconcileOne reaches a provider only through the contract and asks it
// with a TYPE ASSERTION to [coreprovider.SessionInspector]; a provider that does
// not satisfy the interface is counted as unaskable, and nothing goes red. Store
// credit was that provider until ADR 0165, the same shape as the manual provider
// that did answer. The compile-time pins in the two tender packages say the TYPE
// satisfies the interface; what this test adds is that the answer is RIGHT: an
// authorized session reports authorized with the amount held and nothing
// captured, and a session the tender never opened is disowned with NotFound
// rather than answered with zeros — which the reconciler counts as unknown, its
// one finding about an installation.
func TestBothTendersAnswerReconciliation(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(manual.New(repo, nil)))
	require.NoError(t, registry.Register(storecredit.New(repo, nil)))
	require.NoError(t, registry.Register(loyaltypoints.New(repo, nil)))

	svc, err := service.New(service.Options{
		Store: repo, Providers: registry, Events: eventbus.NewInMemory(nil),
		LoyaltyEarnBasisPoints: service.MaxLoyaltyEarnBasisPoints,
	})
	require.NoError(t, err)

	customer := "cus_" + models.NewPaymentCollectionID()
	// Both balances are funded the way each is: credit is issued, points are
	// earned.
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: testCurrency,
		Amount: testAmount, Reason: "reconciliation",
	})
	require.NoError(t, err)
	earnPoints(ctx, t, svc, customer)

	for _, tender := range []struct {
		id      string
		unknown string
	}{
		{id: storecredit.ID, unknown: models.StoreCreditSessionIDPrefix + "NOBODYOPENEDTHIS"},
		{id: loyaltypoints.ID, unknown: models.LoyaltySessionIDPrefix + "NOBODYOPENEDTHIS"},
	} {
		t.Run(tender.id, func(t *testing.T) {
			prov, err := registry.Get(tender.id)
			require.NoError(t, err)

			// The same assertion the reconciler makes, on the same static type.
			inspector, ok := prov.(coreprovider.SessionInspector)
			require.True(t, ok,
				"the reconciler would count every %s session as unaskable", tender.id)

			col := customerCollection(ctx, t, svc, customer, testAmount)
			ses, err := svc.CreateSession(ctx, col.ID, tender.id,
				service.CreateSessionInput{IdempotencyKey: "reconcile-" + col.ID})
			require.NoError(t, err)
			_, err = svc.AuthorizePayment(ctx, ses.ID)
			require.NoError(t, err)

			inspection, err := inspector.InspectSession(ctx, ses.ExternalID)
			require.NoError(t, err)
			assert.Equal(t, coreprovider.SessionAuthorized, inspection.Status)
			assert.Equal(t, testAmount, inspection.AuthorizedAmount,
				"the tender reports the amount it holds")
			assert.Zero(t, inspection.CapturedAmount)
			assert.Zero(t, inspection.RefundedAmount)

			_, err = inspector.InspectSession(ctx, tender.unknown)
			assert.True(t, errors.IsNotFound(err),
				"a session the tender never opened is disowned, not answered with zeros: %v", err)
		})
	}
}

// TestTheEarnTargetSumsOnlyWhatACollectionEarned pins the SUBJECT of the query
// the earn target is computed from.
//
// LoyaltyPointsForReference sums the two earning kinds and nothing else. Every
// spend row the tender writes references its own session, so the filter changes
// no sum the system produces today — which is exactly why it needs a witness of
// its own: a mutation that dropped it would leave every other test green. The
// row planted here is one nothing in the tree writes, a hold carrying a
// collection id, and it is planted straight into the repository because the
// point is what the QUERY does with such a row, not how it got there. Without
// the filter the target would read the hold as points already written and the
// next capture would earn them back (ADR 0165).
func TestTheEarnTargetSumsOnlyWhatACollectionEarned(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	customer := "cus_" + models.NewPaymentCollectionID()
	collection := models.NewPaymentCollectionID()

	for _, entry := range []models.LoyaltyEntry{
		{Points: 300, Kind: models.LoyaltyEarn},
		{Points: -100, Kind: models.LoyaltyReverse},
		// The three spend rows deliberately do NOT net to zero: a fixture whose
		// spend rows canceled out would read the same with and without the
		// filter, and the mutation would survive it.
		{Points: -50, Kind: models.LoyaltyHold},
		{Points: 20, Kind: models.LoyaltyRelease},
		{Points: 10, Kind: models.LoyaltyRefund},
	} {
		entry.ID = models.NewLoyaltyEntryID()
		entry.CustomerID = customer
		entry.CurrencyCode = testCurrency
		entry.Reference = collection
		_, err := repo.AppendLoyaltyEntry(ctx, entry)
		require.NoError(t, err)
	}

	earned, err := repo.LoyaltyPointsForReference(ctx, collection)
	require.NoError(t, err)
	assert.Equal(t, int64(200), earned,
		"300 earned - 100 reversed; the hold, the release and the refund are not what the collection EARNED")

	balance, err := repo.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(180), balance,
		"the balance, unlike the target, is every row: 300 - 100 - 50 + 20 + 10")
}
