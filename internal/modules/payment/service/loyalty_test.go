package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// The loyalty point ledger's tests (ADR 0164).
//
// They all have one subject: what writes a row is not an INCREMENT but a TARGET
// computed from the collection's own cumulative amounts. Each scenario below
// holds one half of that sentence — the same event arriving twice, a refund
// turning it negative, a guest writing nothing, and which way the rounding goes.

// The fixture this file builds on.
//
// The values repeat the neighboring tests' constants rather than borrowing
// them: those names are Turkish, and the language ledger may only shrink, so a
// new file that reads them would be adding to a debt this repository is paying
// off (ADR 0012).
const (
	// loyaltyCustomer is the customer who earns the points.
	loyaltyCustomer = "cus_LOYAL"
	// loyaltyProvider is the fake provider the captures go through.
	loyaltyProvider = "fake"
	// loyaltyReference is the caller's own record the collection collects for.
	loyaltyReference = "cart_LOYALTY"
	// loyaltyCurrency is the currency every scenario here works in.
	loyaltyCurrency = "TRY"
	// loyaltyAmount is the collection amount a scenario opens with.
	loyaltyAmount = int64(10_000)
)

// earningService builds a service that earns at the given rate.
func earningService(t *testing.T, basisPoints int64) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(loyaltyProvider)))

	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(),
		LoyaltyEarnBasisPoints: basisPoints,
	})
	require.NoError(t, err)

	return svc, store
}

// captureFor captures an amount for a named customer and returns the collection.
func captureFor(
	t *testing.T, svc *service.Service, customer string, collectionAmount, captured int64,
) models.PaymentCollection {
	t.Helper()

	ctx := context.Background()
	col, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    loyaltyReference,
		Amount:       collectionAmount,
		CurrencyCode: loyaltyCurrency,
		CustomerID:   customer,
	})
	require.NoError(t, err)

	ses := openSession(t, svc, col.ID, "key-loyalty")
	_, err = svc.AuthorizePayment(ctx, ses.ID)
	require.NoError(t, err)
	_, err = svc.CapturePayment(ctx, ses.ID, captured)
	require.NoError(t, err)

	return col
}

// openSession opens a payment session on the collection.
func openSession(
	t *testing.T, svc *service.Service, collectionID, key string,
) models.PaymentSession {
	t.Helper()

	ses, err := svc.CreateSession(context.Background(), collectionID, loyaltyProvider,
		service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)

	return ses
}

// pointRows returns the customer's ledger rows.
func pointRows(t *testing.T, store *fakeStore, customer string) []models.LoyaltyEntry {
	t.Helper()

	rows, _, err := store.ListLoyaltyEntries(context.Background(), customer, loyaltyCurrency, 100, 0)
	require.NoError(t, err)

	return rows
}

// TestACaptureWritesTheCustomerPoints holds the happy path.
func TestACaptureWritesTheCustomerPoints(t *testing.T) {
	svc, store := earningService(t, 100)

	col := captureFor(t, svc, loyaltyCustomer, loyaltyAmount, loyaltyAmount)

	rows := pointRows(t, store, loyaltyCustomer)
	require.Len(t, rows, 1, "one capture writes one row")
	assert.Equal(t, int64(100), rows[0].Points,
		"10,000 minor units at one percent is 100 points")
	assert.Equal(t, models.LoyaltyEarn, rows[0].Kind)
	assert.Equal(t, col.ID, rows[0].Reference,
		"the row names the collection it was earned against; the target is recomputed from it")
	assert.Equal(t, loyaltyCurrency, rows[0].CurrencyCode)
	assert.Equal(t, models.LoyaltyEntryIDPrefix, rows[0].ID[:len(models.LoyaltyEntryIDPrefix)])

	balance, err := svc.LoyaltyBalance(context.Background(), loyaltyCustomer, loyaltyCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(100), balance, "the balance is the sum of the rows")
}

// TestAZeroRateWritesNothing holds the default.
//
// A points program nobody asked for is a promise to customers the shop did not
// make, and an installation must not start making it by upgrading.
func TestAZeroRateWritesNothing(t *testing.T) {
	svc, store := earningService(t, 0)

	captureFor(t, svc, loyaltyCustomer, loyaltyAmount, loyaltyAmount)

	assert.Empty(t, pointRows(t, store, loyaltyCustomer),
		"at a zero rate the ledger exists and nothing is written into it")
}

// TestAGuestCaptureWritesNothing refuses the empty key.
//
// Most collections name nobody, and a row keyed on an empty customer would put
// every guest in the shop into ONE account.
func TestAGuestCaptureWritesNothing(t *testing.T) {
	svc, store := earningService(t, 100)

	captureFor(t, svc, "", loyaltyAmount, loyaltyAmount)

	rows, _, err := store.ListLoyaltyEntries(context.Background(), "", loyaltyCurrency, 100, 0)
	require.NoError(t, err)
	assert.Empty(t, rows, "a capture with no customer writes no row in any ledger")
}

// TestUnchangedTotalsWriteNoSecondRow holds the load-bearing half of the record.
//
// Because a row is a DIFFERENCE, a second write that does not move the totals
// adds nothing. That is what makes the write safe when the same money moment is
// written twice, and it is why no uniqueness index is needed to say so.
func TestUnchangedTotalsWriteNoSecondRow(t *testing.T) {
	svc, store := earningService(t, 100)
	ctx := context.Background()

	// The capture is PARTIAL so that the collection still has room to open a
	// session and the totals writer can run again.
	col := captureFor(t, svc, loyaltyCustomer, loyaltyAmount, 6_000)
	require.Len(t, pointRows(t, store, loyaltyCustomer), 1)

	// A second session is opened and canceled: writeCollectionTotals runs twice
	// more, and the captured and refunded amounts stay where they were.
	ses := openSession(t, svc, col.ID, "key-loyalty-2")
	require.NoError(t, svc.CancelPayment(ctx, ses.ID))

	assert.Len(t, pointRows(t, store, loyaltyCustomer), 1,
		"if the totals did not move the difference is zero, and zero writes no row")
}

// TestClosingTheProgramDoesNotTakeBackWhatItGave holds the zero-rate return.
//
// The target is computed from the rate, so at a rate of zero the target is zero
// and the difference for somebody who has already earned is the NEGATIVE of
// everything they hold: the next time their money moved, one reverse row would
// take the lot. Closing a program is not confiscating what it gave, which is
// why the early return is a rule rather than a short circuit.
func TestClosingTheProgramDoesNotTakeBackWhatItGave(t *testing.T) {
	ctx := context.Background()

	// ONE fake store, two services: the installation has come back up with the
	// rate turned off and the ledger still standing.
	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(loyaltyProvider)))

	earning, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(),
		LoyaltyEarnBasisPoints: 100,
	})
	require.NoError(t, err)

	col := captureFor(t, earning, loyaltyCustomer, loyaltyAmount, 6_000)
	require.Len(t, pointRows(t, store, loyaltyCustomer), 1)

	closed, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(),
	})
	require.NoError(t, err)

	// The money moves again: a session is opened for the rest and canceled.
	ses := openSession(t, closed, col.ID, "key-closed")
	require.NoError(t, closed.CancelPayment(ctx, ses.ID))

	balance, err := closed.LoyaltyBalance(ctx, loyaltyCustomer, loyaltyCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(60), balance,
		"points already earned are not taken back because the rate was turned off")
	assert.Len(t, pointRows(t, store, loyaltyCustomer), 1,
		"a closed program writes no row at all, negative ones included")
}

// TestARefundTakesThePointsBack holds the negative row.
func TestARefundTakesThePointsBack(t *testing.T) {
	svc, store := earningService(t, 100)
	ctx := context.Background()

	col := captureFor(t, svc, loyaltyCustomer, loyaltyAmount, loyaltyAmount)
	captures, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)
	require.Len(t, captures, 1)

	_, err = svc.RefundPayment(ctx, captures[0].ID, 2_500, "partial refund")
	require.NoError(t, err)

	rows := pointRows(t, store, loyaltyCustomer)
	require.Len(t, rows, 2, "a refund writes a NEW row rather than correcting the old one")
	assert.Equal(t, int64(-25), rows[0].Points,
		"the target for the remaining 7,500 is 75, and 75 less the 100 written is -25")
	assert.Equal(t, models.LoyaltyReverse, rows[0].Kind)

	balance, err := svc.LoyaltyBalance(ctx, loyaltyCustomer, loyaltyCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(75), balance)
}

// TestAFullRefundEmptiesTheTarget holds the whole way back down.
func TestAFullRefundEmptiesTheTarget(t *testing.T) {
	svc, store := earningService(t, 100)
	ctx := context.Background()

	col := captureFor(t, svc, loyaltyCustomer, loyaltyAmount, loyaltyAmount)
	captures, err := svc.ListPayments(ctx, col.ID)
	require.NoError(t, err)

	_, err = svc.RefundPayment(ctx, captures[0].ID, 0, "all of it")
	require.NoError(t, err)

	balance, err := svc.LoyaltyBalance(ctx, loyaltyCustomer, loyaltyCurrency)
	require.NoError(t, err)
	assert.Zero(t, balance, "if the money went back entirely the points go with it")

	rows := pointRows(t, store, loyaltyCustomer)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(-100), rows[0].Points)
}

// TestThePointsRoundDown fixes the direction of the rounding.
//
// It has to be the SAME in both directions: the target comes from one formula
// and the ledger holds the difference, so an earn that rounded one way and a
// reverse that rounded the other would leave a one-point residue behind on every
// refund — in a table that is append-only.
func TestThePointsRoundDown(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		captured int64
		expected int64
	}{
		{name: "under one unit earns nothing", captured: 99, expected: 0},
		{name: "exactly one unit earns one point", captured: 100, expected: 1},
		{name: "the remainder is dropped", captured: 999, expected: 9},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			svc, store := earningService(t, 100)

			captureFor(t, svc, loyaltyCustomer, scenario.captured, scenario.captured)

			balance, err := svc.LoyaltyBalance(context.Background(), loyaltyCustomer, loyaltyCurrency)
			require.NoError(t, err)
			assert.Equal(t, scenario.expected, balance)

			if scenario.expected == 0 {
				assert.Empty(t, pointRows(t, store, loyaltyCustomer),
					"a difference of zero points writes no row; the table refuses zero anyway")
			}
		})
	}
}

// TestAnInvalidRateRefusesTheService holds the ceiling and the floor.
//
// An embedder who never passes through the configuration has to get the same
// answer: a library does not trust its caller.
func TestAnInvalidRateRefusesTheService(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(loyaltyProvider)))

	for _, rate := range []int64{-1, service.MaxLoyaltyEarnBasisPoints + 1} {
		_, err := service.New(service.Options{
			Store: newFakeStore(), Providers: registry, Events: newFakeBus(),
			LoyaltyEarnBasisPoints: rate,
		})

		require.Error(t, err, "the rate %d must not be accepted", rate)
		assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
	}
}

// TestTheBalanceCannotBeAskedWithoutAnOwner holds the read side's validation.
func TestTheBalanceCannotBeAskedWithoutAnOwner(t *testing.T) {
	svc, _ := earningService(t, 100)
	ctx := context.Background()

	_, err := svc.LoyaltyBalance(ctx, "  ", loyaltyCurrency)
	require.Error(t, err)
	assert.Equal(t, service.CodeLoyaltyInvalidInput, errors.CodeOf(err))

	_, _, err = svc.ListLoyalty(ctx, service.ListLoyaltyInput{CurrencyCode: loyaltyCurrency})
	require.Error(t, err)
	assert.Equal(t, service.CodeLoyaltyInvalidInput, errors.CodeOf(err))
}

// TestACustomerWithNoPointsReadsZero holds absence as a number.
func TestACustomerWithNoPointsReadsZero(t *testing.T) {
	svc, _ := earningService(t, 100)

	balance, err := svc.LoyaltyBalance(context.Background(), "cus_NONE", loyaltyCurrency)

	require.NoError(t, err)
	assert.Zero(t, balance,
		"somebody who never earned and somebody whose points were all reversed hold the same number")
}
