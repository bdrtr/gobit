//go:build integration

package payment_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestACreditNamesItsOrderOnTheRealSchema is ADR 0274 over the real schema:
// an issue keeps the order it compensates, the history narrowed to the order
// reads its credits alone, and the schema refuses an order on a row that is
// not an issue.
func TestACreditNamesItsOrderOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := reconService(t)
	customer := fmt.Sprintf("cus_order_credit_%d", time.Now().UnixNano())

	named, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, Amount: 2_000, Reason: "a late delivery",
		OrderID: "order_compensated",
	})
	require.NoError(t, err)
	assert.Equal(t, "order_compensated", named.OrderID)
	_, err = svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, Amount: 500, Reason: "goodwill",
	})
	require.NoError(t, err)

	forOrder, count, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID: customer, CurrencyCode: reconCurrency, OrderID: "order_compensated",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
	require.Len(t, forOrder, 1)
	assert.Equal(t, named.ID, forOrder[0].ID)
	assert.Equal(t, "order_compensated", forOrder[0].OrderID)

	all, count, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{CustomerID: customer, CurrencyCode: reconCurrency})
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	assert.Len(t, all, 2)

	// The raw write is the witness: the service never writes an order on a
	// hold, so only the schema can be asked whether it would take one.
	_, err = testPool.Pool().Exec(ctx, `
        INSERT INTO payment_store_credit_entries (id, customer_id, currency_code, amount, kind, reference, reason, order_id)
        VALUES ($1, $2, $3, -100, 'hold', 'ses_1', '', 'order_compensated')`,
		fmt.Sprintf("scredit_hold_%d", time.Now().UnixNano()), customer, reconCurrency)
	require.Error(t, err, "a hold is money moving within the balance and names no order")
	assert.Contains(t, err.Error(), "payment_store_credit_entries_order_on_issue")
}

// TestTheOrderOfACreditRollsBack: 000015's down forgets the orders and keeps
// every credit.
func TestTheOrderOfACreditRollsBack(t *testing.T) {
	ctx := context.Background()
	dsn, pool := isolatedDatabase(ctx, t, "payment_credit_order_rollback")
	svc := serviceOnPool(t, pool)

	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_rollback", CurrencyCode: reconCurrency, Amount: 100, Reason: "kept", OrderID: "order_1",
	})
	require.NoError(t, err)

	rollBackTo(ctx, t, dsn, 15)
	require.NoError(t, db.MigrateDown(ctx, dsn, payment.New().Migrations(), payment.ModuleName, 1))

	var credits int
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_store_credit_entries WHERE customer_id = 'cus_rollback'`).Scan(&credits))
	assert.Equal(t, 1, credits, "the credit stays; only the order it named is forgotten")
}
