package repository

import (
	"context"
	"hash/fnv"
)

// ledgerLockSQL takes a transaction-lifetime advisory lock on one balance.
//
// It is run directly on the transaction handle rather than generated through
// sqlc, for the order module's reason: it touches none of this module's tables,
// it is a concurrency primitive and carries no schema.
const ledgerLockSQL = `SELECT pg_advisory_xact_lock($1)`

// The CLASS numbers of the two balance locks, written into the UPPER 32 bits of
// every key they take.
//
// The key space of pg_advisory_xact_lock is one across the whole database, and
// the classes the tree has taken are listed on internal/core/job's LockClass;
// TestAdvisoryLockClassesAreUnique holds them apart. The two ledgers take two
// classes rather than one because they are two balances: a customer spending
// credit and points in one collection has nothing to serialize between them.
const (
	storeCreditBalanceLockClass int64 = 4
	loyaltyBalanceLockClass     int64 = 5
)

// LockStoreCreditBalance locks one customer's credit balance in one currency
// until the transaction ends.
//
// Every write that reads the balance and acts on it calls this FIRST: without
// it two concurrent authorizations both see enough money and both write a hold,
// which is the customer spending the same money twice. What the lock is, and why
// it is not a row lock, is on [Repository.lockBalance].
func (r *Repository) LockStoreCreditBalance(
	ctx context.Context, customerID, currencyCode string,
) error {
	return r.lockBalance(ctx, "LockStoreCreditBalance", storeCreditBalanceLockClass,
		customerID, currencyCode)
}

// LockLoyaltyBalance locks one customer's point balance in one currency until
// the transaction ends, for [Repository.LockStoreCreditBalance]'s reason.
func (r *Repository) LockLoyaltyBalance(
	ctx context.Context, customerID, currencyCode string,
) error {
	return r.lockBalance(ctx, "LockLoyaltyBalance", loyaltyBalanceLockClass,
		customerID, currencyCode)
}

// lockBalance takes the advisory lock of one balance.
//
// # Why not the rows
//
// What a balance tender protects is a SUM, and a sum has no row to lock.
// SELECT ... FOR UPDATE takes the rows that exist, so it took nothing from a
// customer who had none, and ADR 0152 wrote that down as "not a hole" because
// such a customer's balance is zero. It is zero at the LOCK. The sum is the
// next statement and, under READ COMMITTED, a fresh snapshot: an earn or an
// issue committed between the two is a row nobody locked, a second
// authorization locks that row without waiting, both read the same balance and
// both hold it (D118). The lock is bound to the customer and the currency
// instead, which is the order module's spending lock and its argument.
//
// # The key
//
// The upper 32 bits are the ledger's class, the lower 32 the FNV-1a digest of
// the customer and the currency with a zero byte between them, so that no pair
// of ids runs into another by concatenation. Two different balances can land on
// the same digest; the consequence is an unnecessary wait, never two spends of
// one balance let through together.
//
// It refuses to run outside a transaction: a pg_advisory_xact_lock without one
// is released when the statement ends and protects nothing.
func (r *Repository) lockBalance(
	ctx context.Context, op string, class int64, customerID, currencyCode string,
) error {
	if err := requireTx(ctx, op); err != nil {
		return err
	}
	tx, _ := txFromContext(ctx)
	if _, err := tx.Exec(ctx, ledgerLockSQL, balanceLockKey(class, customerID, currencyCode)); err != nil {
		return classify(err, codeQueryFailed, "the balance lock could not be taken")
	}

	return nil
}

// balanceLockKey converts a balance's identity into an advisory lock key.
func balanceLockKey(class int64, customerID, currencyCode string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(customerID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(currencyCode))

	return class<<32 | int64(h.Sum32())
}
