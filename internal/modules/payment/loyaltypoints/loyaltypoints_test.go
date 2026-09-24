package loyaltypoints_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The loyalty-points provider (ADR 0165).
//
// # What every test here is about
//
// The LEDGER, not the status field, for the store-credit tests' reason: a
// provider that moved the status correctly and wrote the wrong rows would let a
// customer spend points twice or lose points they still have, and neither shows
// up in a session's state. The state machine itself is balancetender's and is
// proven there; what this file pins is what THIS tender adds — the kinds it
// writes, the reference it writes them under, and the sign the schema will hold
// it to.

// The fixture's customer, currency and amounts.
const (
	testCustomer = "cus_01LOYALTYPOINTS"
	testCurrency = "TRY"
	testAmount   = 5_000
	// testCollection is the collection the module opens the session for. It is
	// what a spend row must NEVER reference.
	testCollection = "paycol_1"
)

// newProvider builds the provider on an empty ledger.
func newProvider() (*loyaltypoints.Provider, *memStore) {
	store := newMemStore()

	return loyaltypoints.New(store, slog.New(slog.DiscardHandler)), store
}

// earn puts points into the ledger the way the service's earn path does: an earn
// row referencing the collection the money moved on.
func earn(t *testing.T, store *memStore, points int64) {
	t.Helper()

	_, err := store.AppendLoyaltyEntry(context.Background(), models.LoyaltyEntry{
		ID:           models.NewLoyaltyEntryID(),
		CustomerID:   testCustomer,
		CurrencyCode: testCurrency,
		Points:       points,
		Kind:         models.LoyaltyEarn,
		Reference:    "paycol_earned_on",
	})
	require.NoError(t, err)
}

// openSession opens a session for the fixture's customer.
func openSession(t *testing.T, provider *loyaltypoints.Provider, key string, amount int64) coreprovider.Session {
	t.Helper()

	session, err := provider.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         amount,
		CurrencyCode:   testCurrency,
		Reference:      testCollection,
		IdempotencyKey: key,
		CustomerID:     testCustomer,
	})
	require.NoError(t, err)

	return session
}

// TestTheIdentityIsTheLedgersOwn pins the one string two parties rely on: the
// registry keys sessions on it, and the earn path excludes captures made under
// it. A provider answering to a different name than the earn path excludes would
// let a points-paid capture earn points.
func TestTheIdentityIsTheLedgersOwn(t *testing.T) {
	t.Parallel()

	provider, _ := newProvider()

	assert.Equal(t, models.LoyaltyTenderID, provider.ID())
	assert.Equal(t, "loyalty_points", provider.ID())
}

// TestAuthorizeHoldsThePointsUnderTheSessionsName is the arithmetic and the
// reference at once.
//
// The hold is NEGATIVE, the kind is the ledger's own, and the row references the
// PROVIDER'S session — not the collection the module handed in. A hold written
// under the collection id would be summed as points that collection had already
// earned, and the next capture would earn them back.
func TestAuthorizeHoldsThePointsUnderTheSessionsName(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 12_000)
	session := openSession(t, provider, "key-1", testAmount)

	result, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(testAmount), result.AuthorizedAmount)

	assert.Equal(t, []string{"earn", "hold"}, store.kinds(),
		"authorizing writes exactly one row beside the fixture's earn, and it is a hold")
	assert.Equal(t, int64(7_000), store.balance(testCustomer, testCurrency),
		"the held points stop being spendable at AUTHORIZATION time, not at capture")
	assert.Equal(t, []string{"paycol_earned_on", session.ID}, store.references(),
		"a spend row references the provider's own session and never the collection")
	assert.NotEqual(t, testCollection, session.ID)

	require.NoError(t, provider.Capture(context.Background(), session.ID, 0))

	assert.Equal(t, []string{"earn", "hold"}, store.kinds(),
		"capturing writes NOTHING to the ledger: the hold already took it")
}

// TestCancelAndRefundWriteTheLedgersOwnKinds pins the adapter's vocabulary: the
// machine says release and refund, the ledger says the same words, and the
// schema pairs each with a positive sign. A kind the schema does not know would
// be refused by the database and is refused by the fake for the same reason.
func TestCancelAndRefundWriteTheLedgersOwnKinds(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 12_000)

	canceled := openSession(t, provider, "key-2", testAmount)
	_, err := provider.Authorize(context.Background(), canceled.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Cancel(context.Background(), canceled.ID))

	refunded := openSession(t, provider, "key-3", testAmount)
	_, err = provider.Authorize(context.Background(), refunded.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Capture(context.Background(), refunded.ID, 0))
	require.NoError(t, provider.Refund(context.Background(), refunded.ID, 2_000))

	assert.Equal(t, []string{"earn", "hold", "release", "hold", "refund"}, store.kinds())
	assert.Equal(t, int64(9_000), store.balance(testCustomer, testCurrency),
		"12_000 - 5_000 held + 5_000 released - 5_000 spent + 2_000 back")
	assert.Equal(t, []string{"paycol_earned_on", canceled.ID, canceled.ID, refunded.ID, refunded.ID},
		store.references())
}

// TestAnInsufficientBalanceIsADeclineAndNotAnError is the ordinary refusal, and
// the one a points tender will produce most: at a storefront that takes one
// tender for the whole order, most customers do not hold enough.
func TestAnInsufficientBalanceIsADeclineAndNotAnError(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 1_000)
	session := openSession(t, provider, "key-4", testAmount)

	result, err := provider.Authorize(context.Background(), session.ID)

	require.NoError(t, err, "a declined payment is not a failure of the call")
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.Zero(t, result.AuthorizedAmount)
	assert.Contains(t, result.DeclineReason, loyaltypoints.CodeInsufficient,
		"the reason carries THIS tender's code, so an operator can tell which balance was short")

	assert.Equal(t, []string{"earn"}, store.kinds(),
		"a declined authorization writes NO ledger row of its own")
}

// TestANegativeBalanceDeclines is the state ADR 0165 declares: a refund can
// reverse points a customer already spent, and the tender then declines against
// the hole until an earn fills it.
func TestANegativeBalanceDeclines(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 5_000)

	spent := openSession(t, provider, "key-5", 5_000)
	_, err := provider.Authorize(context.Background(), spent.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Capture(context.Background(), spent.ID, 0))

	// The order the points were earned on is refunded: the earn path writes the
	// reverse without asking whether the points are still there.
	_, err = store.AppendLoyaltyEntry(context.Background(), models.LoyaltyEntry{
		ID: models.NewLoyaltyEntryID(), CustomerID: testCustomer, CurrencyCode: testCurrency,
		Points: -5_000, Kind: models.LoyaltyReverse, Reference: "paycol_earned_on",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(-5_000), store.balance(testCustomer, testCurrency))

	next := openSession(t, provider, "key-6", 1)
	result, err := provider.Authorize(context.Background(), next.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status,
		"one point is asked and the balance is below zero: declined, not an error")

	// An earn fills the hole first; only what is left over is spendable.
	earn(t, store, 5_001)
	after := openSession(t, provider, "key-7", 2)
	result, err = provider.Authorize(context.Background(), after.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionFailed, result.Status, "5_001 - 5_000 = 1 point, two asked")

	last := openSession(t, provider, "key-8", 1)
	result, err = provider.Authorize(context.Background(), last.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
}

// TestASessionWithNoCustomerIsAConflict is the decision's security half seen
// through the error's KIND.
//
// Points are one person's. A session that named nobody would have to take the
// owner from somewhere, and every "somewhere" available is data the client sent.
// It is a CONFLICT and not a server error, because the chooser at the storefront
// is a shopper who read a list that offered this tender for a cart naming nobody
// (ADR 0165).
func TestASessionWithNoCustomerIsAConflict(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()

	_, err := provider.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      testCollection,
		IdempotencyKey: "key-9",
	})

	require.Error(t, err)
	assert.Equal(t, loyaltypoints.CodeNoCustomer, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsConflict(err), "kind: %v", err)
	assert.Empty(t, store.kinds(), "nothing may be written for a session with no owner")
}

// TestTheLedgerIsLockedBeforeItIsRead is the concurrency contract, readable here
// as an order and proven as a queue against a real server in the module's
// integration tests.
func TestTheLedgerIsLockedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 12_000)
	session := openSession(t, provider, "key-10", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"session", "balance lock", "balance read"}, store.sequence,
		"the session is locked first and the balance second, and the balance is read "+
			"only under its lock: the two locks swapped let two transactions hold each "+
			"other's next lock, and a read before the lock decides on money another "+
			"authorization is spending")
}

// TestInspectSessionAnswersFromTheProvidersOwnTable is what the reconcile job
// asks, and the answer it gets is the provider's own row, not the module's.
//
// It is read BEFORE the capture as well as after. A capture writes the taken
// amount into both the authorized and the captured column, so a fixture that
// inspected only a captured session could not tell the two fields apart, and an
// inspection that reported one as the other would pass.
func TestInspectSessionAnswersFromTheProvidersOwnTable(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 12_000)
	session := openSession(t, provider, "key-11", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)

	inspection, err := provider.InspectSession(context.Background(), session.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionInspection{
		Status:           coreprovider.SessionAuthorized,
		AuthorizedAmount: testAmount,
	}, inspection, "an authorized session holds the amount and has captured nothing")

	require.NoError(t, provider.Capture(context.Background(), session.ID, 2_000))
	require.NoError(t, provider.Refund(context.Background(), session.ID, 500))

	inspection, err = provider.InspectSession(context.Background(), session.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionInspection{
		Status:           coreprovider.SessionCaptured,
		AuthorizedAmount: 2_000,
		CapturedAmount:   2_000,
		RefundedAmount:   500,
	}, inspection)

	_, err = provider.InspectSession(context.Background(), "lpses_NOBODY")
	require.Error(t, err, "a session the provider has never heard of is NotFound, not a zero inspection")
	assert.True(t, coreerrors.IsNotFound(err))
}

// TestPointsAreSpentOnlyInTheCurrencyTheyWereEarnedIn pins the half of "a point
// is worth one minor unit of the currency it was earned in" that says WHICH
// currency.
//
// A balance is per customer AND per currency, and the tender reads the one the
// session is in. Points earned on lira orders are no balance at all in a euro
// checkout: the session declines, and the lira points are untouched. A tender
// that read the customer's points without the currency would spend lira points
// as euro cents — a number that happens to fit, at a worth nobody decided.
func TestPointsAreSpentOnlyInTheCurrencyTheyWereEarnedIn(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	earn(t, store, 12_000)

	session, err := provider.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   "EUR",
		Reference:      testCollection,
		IdempotencyKey: "key-eur",
		CustomerID:     testCustomer,
	})
	require.NoError(t, err)

	result, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err, "a short balance is a decline, not an error")
	assert.Equal(t, coreprovider.SessionFailed, result.Status,
		"12 000 lira points are no euro balance at all")
	assert.Contains(t, result.DeclineReason, loyaltypoints.CodeInsufficient)

	balance, err := store.LoyaltyBalance(context.Background(), testCustomer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(12_000), balance, "the lira points are not touched by a euro decline")
	assert.Equal(t, []string{string(models.LoyaltyEarn)}, store.kinds(),
		"a declined session writes nothing")
}
