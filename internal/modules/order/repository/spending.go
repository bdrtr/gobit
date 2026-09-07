package repository

import (
	"context"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// advisoryLockSQL takes the transaction-lifetime advisory lock per customer.
//
// The lock is NOT GENERATED through sqlc and is run directly with the
// transaction handle: the query touches none of this module's tables, it is a
// concurrency primitive. It carries no schema information for sqlc either.
const advisoryLockSQL = `SELECT pg_advisory_xact_lock($1)`

// spendingLockClass is the CLASS number of the advisory lock and is written
// into the UPPER 32 bits of the key.
//
// The key space of pg_advisory_xact_lock is a single one ACROSS THE WHOLE
// DATABASE: code that locks the same number for another purpose would hold
// this lock up without being aware of it. The class number in the upper bits
// makes it impossible for another advisory lock added later (with another
// class) to collide with this lock.
const spendingLockClass int64 = 1

// LockCustomerSpending locks the customer's spend total UNTIL THE END OF THE
// TRANSACTION.
//
// # Why not a row lock
//
// What is protected is not a ROW but a TOTAL: "the sum of this customer's
// orders within the window". The total has no single row to lock, and
// SELECT ... FOR UPDATE locks the rows that exist, not the one NOT YET
// WRITTEN. Two concurrent orders would race for exactly that reason: both read
// the total, both see it below the limit, both write (the classic write skew).
// The advisory lock closes that gap — the lock is bound not to rows but to the
// CUSTOMER IDENTIFIER and is held until the transaction commits, which means
// the waiting second transaction reads the total together with the row the
// first one wrote.
//
// The SERIALIZABLE isolation level would solve this class of race as well, but
// its price is that conflicting transactions fail with a serialization error
// and the caller takes the retry on itself; the lock waits and then carries on,
// and puts the responsibility for retrying on no existing flow.
//
// # The key
//
// The upper 32 bits of the key are the class ([spendingLockClass]), the lower
// 32 bits are the FNV-1a digest of the customer identifier. It is possible for
// two different identifiers to fall on the same digest; the consequence is ONLY
// an unnecessary wait, not a wrong result — the lock is a correctness gate, not
// an identity.
//
// It can only be called inside [Repository.WithTx]: a pg_advisory_xact_lock
// without a transaction is released immediately and protects nothing.
func (r *Repository) LockCustomerSpending(ctx context.Context, customerID string) error {
	if err := requireTx(ctx, "LockCustomerSpending"); err != nil {
		return err
	}
	tx, _ := txFromContext(ctx)
	if _, err := tx.Exec(ctx, advisoryLockSQL, spendingLockKey(customerID)); err != nil {
		return classify(err, codeQueryFailed, "could not take the customer's spending lock")
	}
	return nil
}

// spendingLockKey converts the customer id into an advisory lock key.
//
// The digest is FNV-1a: it does not need to be cryptographic, it only needs to
// produce the same number for the same identifier. Widening from uint32 to
// int64 is lossless, so the key is the same across processes and versions.
func spendingLockKey(customerID string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(customerID))
	return spendingLockClass<<32 | int64(h.Sum32())
}

// SumCustomerSpend returns the customer's spend within the window.
//
// What is summed (cancellations excluded, refunds deducted, currency fixed)
// and the bounds of the window are documented in queries/spending.sql. If
// windowStart is nil, the customer's WHOLE history is summed.
func (r *Repository) SumCustomerSpend(
	ctx context.Context,
	customerID, currencyCode string,
	windowStart *time.Time,
) (int64, error) {
	var window pgtype.Timestamptz
	if windowStart != nil {
		window = pgtype.Timestamptz{Time: windowStart.UTC(), Valid: true}
	}

	spent, err := r.queries(ctx).SumCustomerSpend(ctx, orderdb.SumCustomerSpendParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
		WindowStart:  window,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not read the customer's spend")
	}
	return spent, nil
}
