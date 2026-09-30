package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// expiredIssue writes an issue row whose moment has already come, the way the
// real table holds one issued earlier.
func expiredIssue(ctx context.Context, t *testing.T, store *fakeStore, customer string, amount int64) {
	t.Helper()

	past := time.Now().Add(-time.Hour)
	_, err := store.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
		ID: models.NewStoreCreditEntryID(), CustomerID: customer, CurrencyCode: "TRY",
		Amount: amount, Kind: models.StoreCreditIssue, Reason: "goodwill", ExpiresAt: &past,
	})
	require.NoError(t, err)
}

// TestExpiredCreditStopsPayingAndIsTakenBack is ADR 0258 in the service: the
// balance stops counting expired credit at once, the expiry writes one
// negative row under the balance's lock, and a second run writes nothing.
func TestExpiredCreditStopsPayingAndIsTakenBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, store := giftCardService(t)

	expiredIssue(ctx, t, store, "cus_expiry", 1_000)
	future := time.Now().Add(24 * time.Hour)
	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_expiry", CurrencyCode: "TRY", Amount: 500, Reason: "loyal", ExpiresAt: &future,
	})
	require.NoError(t, err)

	balance, err := svc.StoreCreditBalance(ctx, "cus_expiry", "TRY")
	require.NoError(t, err)
	assert.Equal(t, int64(500), balance, "expired credit stops paying before the job runs")

	written, err := svc.ExpireStoreCredit(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, written)
	entries, _, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID: "cus_expiry", CurrencyCode: "TRY", Page: service.Page{Limit: 10},
	})
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	assert.Equal(t, models.StoreCreditExpire, entries[0].Kind)
	assert.Equal(t, int64(-1_000), entries[0].Amount)
	assert.Equal(t, service.ReasonCreditExpired, entries[0].Reason)
	assert.Contains(t, store.creditLocks, "cus_expiry\x00TRY", "the expiry takes the lock the tenders take")

	written, err = svc.ExpireStoreCredit(ctx, 10)
	require.NoError(t, err)
	assert.Zero(t, written, "a settled balance is written nothing")
	balance, err = svc.StoreCreditBalance(ctx, "cus_expiry", "TRY")
	require.NoError(t, err)
	assert.Equal(t, int64(500), balance)
}

// TestCreditIsIssuedWithAMomentInTheFuture refuses a moment that has passed.
func TestCreditIsIssuedWithAMomentInTheFuture(t *testing.T) {
	t.Parallel()
	svc, _ := giftCardService(t)

	past := time.Now().Add(-time.Minute)
	_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 100, Reason: "late", ExpiresAt: &past,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)

	_, err = svc.ExpireStoreCredit(context.Background(), 0)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a pass of no balances is refused: %v", err)
}
