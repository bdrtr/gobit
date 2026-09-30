//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestStoreCreditExpiresOnTheRealSchema is ADR 0258 on the real schema. An
// expired grant beside an unexpired one, with a hold standing:
//   - the balance stops counting the expired credit at once;
//   - the expiry takes back only what is not held;
//   - the hold's release is taken back by the next run;
//   - a refund after the expiry survives it;
//   - the journal books each expiry against the grant's cost.
func TestStoreCreditExpiresOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)
	repo := repository.New(testPool.Pool())
	customer := fmt.Sprintf("cus_expiry_%d", time.Now().UnixNano())
	started := time.Now().Add(-time.Minute)

	future := time.Now().Add(24 * time.Hour)
	expiring, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, Amount: 1_000, Reason: "goodwill", ExpiresAt: &future,
	})
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE payment_store_credit_entries
         SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day'
         WHERE id = $1`, expiring.ID)
	require.NoError(t, err)
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, Amount: 500, Reason: "lasting",
	})
	require.NoError(t, err)

	write := func(kind models.StoreCreditKind, amount int64) {
		t.Helper()
		_, err := repo.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
			ID: models.NewStoreCreditEntryID(), CustomerID: customer, CurrencyCode: reconCurrency,
			Amount: amount, Kind: kind, Reference: "scrses_expiry",
		})
		require.NoError(t, err)
	}
	balance := func() int64 {
		t.Helper()
		b, err := svc.StoreCreditBalance(ctx, customer, reconCurrency)
		require.NoError(t, err)
		return b
	}
	sum := func() int64 {
		t.Helper()
		var s int64
		require.NoError(t, testPool.Pool().QueryRow(ctx,
			`SELECT COALESCE(SUM(amount), 0) FROM payment_store_credit_entries
             WHERE customer_id = $1 AND currency_code = $2`, customer, reconCurrency).Scan(&s))
		return s
	}
	// agrees holds the list of balances due to the rule the expiry writes by:
	// the rule is written once in Go, and the list's SQL carries its own copy
	// to select with.
	ref := models.StoreCreditBalanceRef{CustomerID: customer, CurrencyCode: reconCurrency}
	agrees := func(step string) {
		t.Helper()
		figures, err := repo.StoreCreditExpiryFigures(ctx, ref, time.Now())
		require.NoError(t, err)
		due, err := repo.StoreCreditExpiryDue(ctx, time.Now(), service.MaxLimit)
		require.NoError(t, err)
		assert.Equal(t, figures.Due() > 0, slices.Contains(due, ref),
			"%s: the balance is listed as due exactly when the rule has something to take", step)
	}
	expire := func() {
		t.Helper()
		for {
			written, err := svc.ExpireStoreCredit(ctx, service.MaxLimit)
			require.NoError(t, err)
			if written < int(service.MaxLimit) {
				return
			}
		}
	}

	write(models.StoreCreditHold, -600)
	assert.Equal(t, int64(500), balance(), "the expired credit stops paying before the job runs")
	assert.Equal(t, int64(900), sum())
	agrees("a hold standing")

	expire()
	assert.Equal(t, int64(500), sum(), "the expiry took the 400 the hold did not")
	assert.Equal(t, int64(500), balance())
	agrees("after the first expiry")

	write(models.StoreCreditRelease, 600)
	assert.Equal(t, int64(500), balance(), "money released after the expiry is not spendable")
	agrees("the hold released")
	expire()
	assert.Equal(t, int64(500), sum(), "the next run took the released 600")

	write(models.StoreCreditRefund, 200)
	expire()
	assert.Equal(t, int64(700), sum(), "a refund after the expiry survives it")
	assert.Equal(t, int64(700), balance())
	agrees("a refund after the expiry")

	var expired []int64
	rows, err := testPool.Pool().Query(ctx,
		`SELECT amount FROM payment_store_credit_entries
         WHERE customer_id = $1 AND kind = 'expire' ORDER BY created_at, id`, customer)
	require.NoError(t, err)
	for rows.Next() {
		var amount int64
		require.NoError(t, rows.Scan(&amount))
		expired = append(expired, amount)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []int64{-400, -600}, expired)

	journal, err := svc.Journal(ctx, service.JournalQuery{From: started, To: time.Now().Add(time.Minute)})
	require.NoError(t, err)
	var booked int64
	for _, entry := range journal.Entries {
		if entry.Kind != models.JournalStoreCreditExpire {
			continue
		}
		var debit, credit int64
		for _, line := range entry.Lines {
			if line.Account == models.AccountStoreCredit && line.CustomerID == customer {
				debit += line.Debit
				booked += line.Debit
			}
			if line.Account == models.AccountStoreCreditGranted {
				credit += line.Credit
			}
		}
		if debit > 0 {
			assert.Equal(t, debit, credit, "an expiry takes the grant's cost back as the debt goes")
		}
	}
	assert.Equal(t, int64(1_000), booked, "the journal books what the expiries took back")
}

// TestARefundOfExpiringCreditNeverExpires: credit spent and then repaid into
// the account comes back as a refund, and a refund never expires, even when
// the credit it was spent from expires before the job runs (ADR 0258).
func TestARefundOfExpiringCreditNeverExpires(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)
	repo := repository.New(testPool.Pool())
	customer := fmt.Sprintf("cus_refunded_%d", time.Now().UnixNano())

	future := time.Now().Add(24 * time.Hour)
	expiring, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, Amount: 1_000, Reason: "goodwill", ExpiresAt: &future,
	})
	require.NoError(t, err)
	for _, row := range []struct {
		kind   models.StoreCreditKind
		amount int64
	}{{models.StoreCreditHold, -1_000}, {models.StoreCreditRefund, 1_000}} {
		_, err := repo.AppendStoreCreditEntry(ctx, models.StoreCreditEntry{
			ID: models.NewStoreCreditEntryID(), CustomerID: customer, CurrencyCode: reconCurrency,
			Amount: row.amount, Kind: row.kind, Reference: "scrses_refunded",
		})
		require.NoError(t, err)
	}
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE payment_store_credit_entries
         SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day'
         WHERE id = $1`, expiring.ID)
	require.NoError(t, err)

	balance, err := svc.StoreCreditBalance(ctx, customer, reconCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), balance, "the refunded money is spendable after its credit expired")

	ref := models.StoreCreditBalanceRef{CustomerID: customer, CurrencyCode: reconCurrency}
	due, err := repo.StoreCreditExpiryDue(ctx, time.Now(), service.MaxLimit)
	require.NoError(t, err)
	assert.NotContains(t, due, ref, "nothing is due from a balance holding only refunded money")
	_, err = svc.ExpireStoreCredit(ctx, service.MaxLimit)
	require.NoError(t, err)
	balance, err = svc.StoreCreditBalance(ctx, customer, reconCurrency)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), balance, "the expiry took none of it")
}

// TestAnExpiringCreditHoldsBackTheRollback: 000013's down stops while an issue
// names a moment or an expire row exists. It rolls the schema back, so it runs
// in a database of its own.
func TestAnExpiringCreditHoldsBackTheRollback(t *testing.T) {
	ctx := context.Background()
	dsn, pool := isolatedDatabase(ctx, t, "payment_credit_expiry_rollback")
	svc := serviceOnPool(t, pool)

	future := time.Now().Add(time.Hour)
	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_rollback", CurrencyCode: reconCurrency, Amount: 100, Reason: "kept", ExpiresAt: &future,
	})
	require.NoError(t, err)

	err = db.MigrateDown(ctx, dsn, payment.New().Migrations(), payment.ModuleName, 1)
	assertRefusedBy(t, err, "payment_store_credit_entries_none_expiring_on_rollback")
}
