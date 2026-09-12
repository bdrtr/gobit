package storecredit_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// The store-credit provider (ADR 0152).
//
// # What every test here is about
//
// The LEDGER, not the status field. A provider that moved the status correctly
// and wrote the wrong rows would let a customer spend money twice or lose money
// they still have, and neither shows up in a session's state — which is why most
// assertions below read the entries the provider wrote and the balance they sum
// to.

// The fixture's customer, currency and amounts.
const (
	testCustomer = "cus_01STORECREDIT"
	testCurrency = "TRY"
	testAmount   = 5_000
)

// newProvider builds the provider on an empty ledger.
func newProvider() (*storecredit.Provider, *memStore) {
	store := newMemStore()

	return storecredit.New(store, slog.New(slog.DiscardHandler)), store
}

// issue puts credit into the ledger the way the service does.
func issue(t *testing.T, store *memStore, amount int64) {
	t.Helper()

	_, err := store.AppendStoreCreditEntry(context.Background(), models.StoreCreditEntry{
		ID:           models.NewStoreCreditEntryID(),
		CustomerID:   testCustomer,
		CurrencyCode: testCurrency,
		Amount:       amount,
		Kind:         models.StoreCreditIssue,
		Reason:       "fixture",
	})
	require.NoError(t, err)
}

// openSession opens a session for the fixture's customer.
func openSession(t *testing.T, provider *storecredit.Provider, key string, amount int64) coreprovider.Session {
	t.Helper()

	session, err := provider.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         amount,
		CurrencyCode:   testCurrency,
		Reference:      "paycol_1",
		IdempotencyKey: key,
		CustomerID:     testCustomer,
	})
	require.NoError(t, err)

	return session
}

// TestAuthorizeHoldsTheMoneyAndCaptureDoesNotTakeItAgain is the arithmetic the
// whole design rests on.
//
// The hold is what makes the balance safe to read, and a capture that wrote a
// second negative row would take the money twice — a mistake no status field
// would show.
func TestAuthorizeHoldsTheMoneyAndCaptureDoesNotTakeItAgain(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-1", testAmount)

	result, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Equal(t, int64(testAmount), result.AuthorizedAmount)

	assert.Equal(t, []string{"issue", "hold"}, store.kinds(),
		"authorizing writes exactly one row beside the fixture's issue, and it is the hold")
	assert.Equal(t, int64(7_000), store.balance(testCustomer, testCurrency),
		"the held money stops being spendable at AUTHORIZATION time, not at capture")

	require.NoError(t, provider.Capture(context.Background(), session.ID, 0))

	assert.Equal(t, []string{"issue", "hold"}, store.kinds(),
		"capturing writes NOTHING to the ledger: the hold already took it")
	assert.Equal(t, int64(7_000), store.balance(testCustomer, testCurrency),
		"the balance after a capture is what it was after the hold")
}

// TestAnInsufficientBalanceIsADeclineAndNotAnError is the ordinary refusal.
//
// Not having enough credit is an outcome of paying, like a card being declined.
// Returning an error would make the saga treat it as a fault and compensate a
// checkout that simply needs another tender.
func TestAnInsufficientBalanceIsADeclineAndNotAnError(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 1_000)
	session := openSession(t, provider, "key-2", testAmount)

	result, err := provider.Authorize(context.Background(), session.ID)

	require.NoError(t, err, "a declined payment is not a failure of the call")
	assert.Equal(t, coreprovider.SessionFailed, result.Status)
	assert.Zero(t, result.AuthorizedAmount)
	assert.Contains(t, result.DeclineReason, "balance does not cover",
		"the reason is for an operator and has to say what was missing")

	assert.Equal(t, []string{"issue"}, store.kinds(),
		"a declined authorization writes NO ledger row of its own")
	assert.Equal(t, int64(1_000), store.balance(testCustomer, testCurrency))
}

// TestASecondAuthorizeHoldsNothingMore is the contract's idempotency, and it is
// the one that would cost a customer money.
//
// Two holds for one session would take the amount twice out of a balance nobody
// spent twice.
func TestASecondAuthorizeHoldsNothingMore(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-3", testAmount)

	first, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	second, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)

	assert.Equal(t, first.Status, second.Status)
	assert.Equal(t, first.AuthorizedAmount, second.AuthorizedAmount)
	assert.Equal(t, []string{"issue", "hold"}, store.kinds(),
		"the second call holds nothing more")
	assert.Equal(t, int64(7_000), store.balance(testCustomer, testCurrency))
}

// TestCancelGivesTheHeldMoneyBack is the saga's compensation seen from the
// ledger.
func TestCancelGivesTheHeldMoneyBack(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-4", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Cancel(context.Background(), session.ID))

	assert.Equal(t, []string{"issue", "hold", "release"}, store.kinds())
	assert.Equal(t, int64(12_000), store.balance(testCustomer, testCurrency),
		"a canceled checkout leaves the customer exactly what they had")

	// And a second cancel is not a failure and gives nothing back twice.
	require.NoError(t, provider.Cancel(context.Background(), session.ID))
	assert.Equal(t, []string{"issue", "hold", "release"}, store.kinds())
	assert.Equal(t, int64(12_000), store.balance(testCustomer, testCurrency))
}

// TestAPartialCaptureReleasesTheRemainder keeps money from hanging.
//
// The session cannot be canceled after a capture, so an unreleased remainder
// would be money the customer can never spend and nothing would ever report it.
func TestAPartialCaptureReleasesTheRemainder(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-5", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Capture(context.Background(), session.ID, 2_000))

	assert.Equal(t, []string{"issue", "hold", "release"}, store.kinds())
	assert.Equal(t, int64(10_000), store.balance(testCustomer, testCurrency),
		"12_000 - 5_000 held + 3_000 released = 10_000; only the captured 2_000 is gone")
}

// TestARefundGivesTheMoneyBackAsCredit is where a repayment goes.
//
// This provider holds no card and no account, so the only destination a refund
// can have is the ledger it came from.
func TestARefundGivesTheMoneyBackAsCredit(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-6", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)
	require.NoError(t, provider.Capture(context.Background(), session.ID, 0))
	require.NoError(t, provider.Refund(context.Background(), session.ID, 2_000))

	assert.Equal(t, []string{"issue", "hold", "refund"}, store.kinds())
	assert.Equal(t, int64(9_000), store.balance(testCustomer, testCurrency),
		"12_000 - 5_000 spent + 2_000 back")

	// The rest of it, and then nothing more.
	require.NoError(t, provider.Refund(context.Background(), session.ID, 0))
	assert.Equal(t, int64(12_000), store.balance(testCustomer, testCurrency))

	require.NoError(t, provider.Refund(context.Background(), session.ID, 0))
	assert.Equal(t, int64(12_000), store.balance(testCustomer, testCurrency),
		"a repeated refund gives nothing back twice")
}

// TestASessionWithNoCustomerIsRefused is the decision's whole security half.
//
// Store credit is one person's money. A session that named nobody would have to
// take the owner from somewhere, and every "somewhere" available is data the
// client sent.
func TestASessionWithNoCustomerIsRefused(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()

	_, err := provider.CreateSession(context.Background(), coreprovider.CreateSessionInput{
		Amount:         testAmount,
		CurrencyCode:   testCurrency,
		Reference:      "paycol_1",
		IdempotencyKey: "key-7",
	})

	require.Error(t, err)
	assert.Equal(t, storecredit.CodeNoCustomer, coreerrors.CodeOf(err))
	assert.Empty(t, store.kinds(), "nothing may be written for a session with no owner")
}

// TestTheSameKeyOpensNoSecondSession is the contract's idempotency on the open.
func TestTheSameKeyOpensNoSecondSession(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()

	first := openSession(t, provider, "key-8", testAmount)
	second := openSession(t, provider, "key-8", testAmount)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, 1, store.insertCalls)
}

// TestTheLedgerIsLockedBeforeItIsRead is the concurrency contract.
//
// The balance is read and acted on, so two authorizations of one customer's two
// sessions must not both see the same money. In a real database a missing lock
// shows up only under load; here the order is readable.
func TestTheLedgerIsLockedBeforeItIsRead(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-9", testAmount)

	_, err := provider.Authorize(context.Background(), session.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{"session", "ledger"}, store.locks,
		"the session is locked first and the ledger second; the reverse order would "+
			"let two transactions hold each other's next lock")
}

// TestAFailedWriteLeavesTheLedgerUntouched is the transaction boundary.
//
// The hold and the session's status are one act. If the status write failed after
// the hold was appended, the customer would have lost money to a session that
// never authorized.
func TestAFailedWriteLeavesTheLedgerUntouched(t *testing.T) {
	t.Parallel()

	provider, store := newProvider()
	issue(t, store, 12_000)
	session := openSession(t, provider, "key-10", testAmount)

	// The session disappears between the open and the authorize, which is what a
	// failing write looks like from the provider's side.
	store.mu.Lock()
	delete(store.sessions, session.ID)
	store.mu.Unlock()

	_, err := provider.Authorize(context.Background(), session.ID)

	require.Error(t, err)
	assert.Equal(t, []string{"issue"}, store.kinds(), "a failed authorization writes no hold")
	assert.Equal(t, int64(12_000), store.balance(testCustomer, testCurrency))
}
