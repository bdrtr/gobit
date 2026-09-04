//go:build integration

// These tests need a real PostgreSQL (and therefore Docker); they sit behind
// the `integration` tag so `make test` stays fast.
//
// The migration rollback is here for the reason ADR 0018 recorded: the
// architecture gates walk internal/modules/ only, so a plugin's up/down pair is
// certified by nothing. The rest is here because the callback's rules — a
// replayed notification changes nothing, a mismatched signature is refused, a
// failed write is NOT acknowledged — are rules about a row, and a fake store
// would only prove that the fake behaves as written.
package paymentpaytr

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

const postgresImage = "postgres:16-alpine"

var (
	testPool *db.Pool
	testDSN  string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_paytr"),
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

	// The schema is applied the same way production applies it.
	if err = db.Migrate(ctx, testDSN, migrationsRoot, ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the paytr schema could not be applied: %v\n", err)

		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// testConfig is the configuration every test signs with.
var testConfig = config{
	MerchantID:   "123456",
	MerchantKey:  "TESTKEY",
	MerchantSalt: "TESTSALT",
	SuccessURL:   "https://shop.example.test/done",
	FailureURL:   "https://shop.example.test/failed",
	TestMode:     "1",
}

// freshModule empties the table and returns a module over it.
func freshModule(t *testing.T) *paytrModule {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `TRUNCATE paytr_payment`)
	require.NoError(t, err)

	m := newModule(testConfig, slog.New(slog.DiscardHandler))
	m.store = newStore(testPool.Pool())
	m.prov.store = m.store

	return m
}

// callback posts a correctly signed notification and returns the answer.
func callback(t *testing.T, m *paytrModule, oid, status, amount string) *httptest.ResponseRecorder {
	t.Helper()

	return rawCallback(t, m, oid, status, amount,
		callbackSignature(callbackInput{MerchantOID: oid, Status: status, TotalAmount: amount},
			testConfig.MerchantKey, testConfig.MerchantSalt))
}

// rawCallback posts a notification with a caller-supplied signature.
func rawCallback(
	t *testing.T, m *paytrModule, oid, status, amount, hash string,
) *httptest.ResponseRecorder {
	t.Helper()

	form := url.Values{}
	form.Set("merchant_oid", oid)
	form.Set("status", status)
	form.Set("total_amount", amount)
	form.Set("hash", hash)

	req := httptest.NewRequest(http.MethodPost, CallbackPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	m.handleCallback(rec, req)

	return rec
}

// --- the migration ----------------------------------------------------------

// TestTheMigrationIsReallyReversible is the gate no architecture test provides.
func TestTheMigrationIsReallyReversible(t *testing.T) {
	ctx := t.Context()
	dsn := freshDatabase(t)

	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))

	version, dirty, err := db.Version(ctx, dsn, ModuleName)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(1), version)

	require.NoError(t, db.MigrateDown(ctx, dsn, migrationsRoot, ModuleName, 1))

	var exists bool
	require.NoError(t, poolFor(t, dsn).Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables WHERE table_name = 'paytr_payment'
		)`).Scan(&exists))
	assert.False(t, exists, "the table has to be gone after the rollback")

	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName),
		"the schema has to apply again; a down that leaves an index behind fails here")
}

// --- the callback -----------------------------------------------------------

// TestAValidCallbackIsAnsweredWithExactlyOK pins PayTR's protocol.
//
// PayTR reads the BODY, not the status code. Anything other than "OK" means
// "not acknowledged" and PayTR retries — so an answer that is almost right
// produces an endless retry loop while every payment looks fine from inside.
func TestAValidCallbackIsAnsweredWithExactlyOK(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

	rec := callback(t, m, "ORDER1", "success", "10000")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String(),
		"the body has to be exactly OK; PayTR retries anything else")

	stored, err := m.store.get(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, statusSuccess, stored.Status)
	assert.Equal(t, int64(10000), stored.PaidAmount)
	assert.NotNil(t, stored.CallbackAt)
}

// TestAForgedCallbackIsRefused is the security proof.
//
// This is the message that tells the system a payment succeeded, so it is the
// one worth forging. Without signature verification anyone who knows an order
// id can mark it paid.
func TestAForgedCallbackIsRefused(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

	rec := rawCallback(t, m, "ORDER1", "success", "10000", "not-the-signature")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.NotEqual(t, "OK", rec.Body.String())

	stored, err := m.store.get(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, statusPending, stored.Status,
		"a forged callback must not move the payment an inch")
}

// TestACallbackSignedWithTheAppendedSaltIsRefused proves the asymmetry is
// enforced and not merely documented.
//
// PayTR's other requests append the salt; the callback puts it inside the body.
// A verifier written from the general rule accepts nothing genuine — and one
// written the other way round would accept a signature an attacker could forge
// from the public formula if the salt ever leaked in a different position.
func TestACallbackSignedWithTheAppendedSaltIsRefused(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

	appended := signWithAppendedSalt("ORDER1success10000",
		testConfig.MerchantKey, testConfig.MerchantSalt)

	rec := rawCallback(t, m, "ORDER1", "success", "10000", appended)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"the callback signature is NOT the appended-salt form")
}

// TestARepeatedCallbackChangesNothing proves the replay guard.
//
// PayTR retries a callback it believes was not acknowledged, so the same
// notification arrives more than once as a matter of course. A later one must
// not be able to overturn an earlier outcome.
func TestARepeatedCallbackChangesNothing(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

	// The same payment, now reported as failed. A genuine PayTR retry carries
	// the same outcome; this is the hostile version of the same shape.
	rec := callback(t, m, "ORDER1", "failed", "10000")

	assert.Equal(t, "OK", rec.Body.String(), "a repeat is acknowledged rather than retried forever")

	stored, err := m.store.get(t.Context(), "ORDER1")
	require.NoError(t, err)
	assert.Equal(t, statusSuccess, stored.Status,
		"a later callback must NOT overturn an outcome already recorded")
}

// TestACallbackForAnUnknownOrderIsAcknowledged proves an unknown id does not
// cause an endless retry.
//
// Retrying would not make the order id exist. PayTR would call forever and the
// log would fill with the same line.
func TestACallbackForAnUnknownOrderIsAcknowledged(t *testing.T) {
	m := freshModule(t)

	rec := callback(t, m, "NEVEROPENED", "success", "10000")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OK", rec.Body.String())
}

// --- what the provider answers ---------------------------------------------

// TestAuthorizeSaysNotYetBeforeTheCallback proves a pending payment is
// Unavailable rather than a failure.
//
// The distinction is the customer: one who is still typing their card number is
// exactly this state, and reporting it as a failed payment would cancel their
// checkout out from under them.
func TestAuthorizeSaysNotYetBeforeTheCallback(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))

	result, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.Error(t, err)
	assert.Equal(t, coreprovider.SessionPending, result.Status)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"a payment PayTR has not reported on is 'ask again', not 'refused': %v", err)
}

// TestAuthorizeReadsWhatTheCallbackRecorded is the whole point of the table.
//
// The provider is asked, given only a session id, whether the money is held. It
// can only answer because the callback wrote it down — and it has to be able to
// answer after a restart, which is why the answer is not in memory.
func TestAuthorizeReadsWhatTheCallbackRecorded(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

	// A NEW provider over the same table: this is what a restarted process has.
	restarted := &provider{cfg: testConfig, store: newStore(testPool.Pool()), client: &http.Client{}}

	result, err := restarted.Authorize(t.Context(), "ORDER1")

	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(10000), result.AuthorizedAmount)
}

// TestAFailedPaymentIsRefusedRatherThanRetried proves a reported failure is
// Invalid, not Unavailable.
//
// The difference decides whether the saga retries: a declined card does not
// become approved by asking again.
func TestAFailedPaymentIsRefusedRatherThanRetried(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "failed", "0").Body.String())

	_, err := m.prov.Authorize(t.Context(), "ORDER1")

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err),
		"a declined payment must not be retried by the saga: %v", err)
}

// TestCaptureRefusesMoreThanWasPaid proves the capture verifies rather than
// rubber-stamps.
//
// PayTR has no separate capture — the money is taken when the payment succeeds
// — so it would be easy to return success unconditionally. A capture larger
// than what was collected would then be recorded by the core as collected, and
// the shortfall would surface as an accounting difference weeks later.
func TestCaptureRefusesMoreThanWasPaid(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

	require.NoError(t, m.prov.Capture(t.Context(), "ORDER1", 10000))
	require.NoError(t, m.prov.Capture(t.Context(), "ORDER1", 0), "zero means the whole amount")

	err := m.prov.Capture(t.Context(), "ORDER1", 10001)

	require.Error(t, err)
	assert.Equal(t, codeAmountMismatch, coreerrors.CodeOf(err))
}

// TestCancelIsIdempotentAndNeverFailsAnAbandonedCheckout proves the
// compensation's working condition.
//
// A compensation that fails retries forever. A customer who simply closed the
// tab leaves a pending payment with nothing to undo, and the saga has to be
// able to finish unwinding.
func TestCancelIsIdempotentAndNeverFailsAnAbandonedCheckout(t *testing.T) {
	m := freshModule(t)

	t.Run("a session that was never opened", func(t *testing.T) {
		assert.NoError(t, m.prov.Cancel(t.Context(), "NEVEROPENED"))
	})

	t.Run("a payment nobody completed", func(t *testing.T) {
		require.NoError(t, m.store.open(t.Context(), payment{
			MerchantOID: "ORDER2", Amount: 10000, CurrencyCode: "TRY"}))

		assert.NoError(t, m.prov.Cancel(t.Context(), "ORDER2"),
			"there is nothing to undo, so the compensation succeeded")
		assert.NoError(t, m.prov.Cancel(t.Context(), "ORDER2"), "and again")
	})

	t.Run("a payment PayTR declined", func(t *testing.T) {
		require.NoError(t, m.store.open(t.Context(), payment{
			MerchantOID: "ORDER3", Amount: 10000, CurrencyCode: "TRY"}))
		require.Equal(t, "OK", callback(t, m, "ORDER3", "failed", "0").Body.String())

		assert.NoError(t, m.prov.Cancel(t.Context(), "ORDER3"))
	})
}

// TestARefundCannotExceedWhatIsLeft proves the ledger the plugin keeps.
//
// PayTR offers no "how much has been refunded" query, so this column is the
// only thing standing between a retried compensation and sending the money back
// twice.
func TestARefundCannotExceedWhatIsLeft(t *testing.T) {
	m := freshModule(t)
	require.NoError(t, m.store.open(t.Context(), payment{
		MerchantOID: "ORDER1", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "ORDER1", "success", "10000").Body.String())

	require.NoError(t, m.store.addRefund(t.Context(), "ORDER1", 6000))

	err := m.prov.Refund(t.Context(), "ORDER1", 5000)

	require.Error(t, err)
	assert.Equal(t, codeAmountMismatch, coreerrors.CodeOf(err),
		"only 4000 is left; a larger refund must be refused before PayTR is called")
}

// TestPendingListsWhatPayTRNeverReportedOn proves the operator's view of the
// gap this plugin cannot close by itself.
func TestPendingListsWhatPayTRNeverReportedOn(t *testing.T) {
	m := freshModule(t)
	ctx := t.Context()

	require.NoError(t, m.store.open(ctx, payment{
		MerchantOID: "OLD", Amount: 10000, CurrencyCode: "TRY"}))
	require.NoError(t, m.store.open(ctx, payment{
		MerchantOID: "NEW", Amount: 10000, CurrencyCode: "TRY"}))
	require.NoError(t, m.store.open(ctx, payment{
		MerchantOID: "DONE", Amount: 10000, CurrencyCode: "TRY"}))
	require.Equal(t, "OK", callback(t, m, "DONE", "success", "10000").Body.String())

	// Age one of them past the grace period.
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE paytr_payment SET created_at = now() - interval '2 hours' WHERE merchant_oid = 'OLD'`)
	require.NoError(t, err)

	stuck, err := m.store.pending(ctx, pendingGrace, 100)

	require.NoError(t, err)
	require.Len(t, stuck, 1, "only the aged, still-pending payment is stuck")
	assert.Equal(t, "OLD", stuck[0].MerchantOID)
}

// --- helpers ----------------------------------------------------------------

// freshDatabase creates an empty database for a migration test.
func freshDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("paytr_migration_%d", time.Now().UnixNano())
	_, err := testPool.Pool().Exec(t.Context(), `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := testPool.Pool().Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("the temporary database %s could not be dropped: %v", name, err)
		}
	})

	u, err := url.Parse(testDSN)
	require.NoError(t, err)
	u.Path = "/" + name

	return u.String()
}

// poolFor opens a pool against one of the temporary databases.
func poolFor(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
