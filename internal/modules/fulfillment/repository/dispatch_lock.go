package repository

import (
	"context"
	"hash/fnv"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// dispatchLockSQL takes the transaction-lifetime advisory lock per reference.
//
// It is not generated through sqlc: it touches none of this module's tables, it
// is a concurrency primitive, the shape the order module's spending lock has.
const dispatchLockSQL = `SELECT pg_advisory_xact_lock($1)`

// dispatchTryLockSQL takes the same lock if it is free and answers whether it
// did, without waiting (ADR 0420).
const dispatchTryLockSQL = `SELECT pg_try_advisory_xact_lock($1)`

// dispatchLockClass is the class of the dispatch lock, the upper 32 bits of its
// key; internal/arch holds every class in the tree apart.
const dispatchLockClass int64 = 8

// LockReferenceDispatch serializes what writes the units one reference's
// outgoing parcels hold until the transaction ends: its opens (ADR 0409, gap
// D265) and its joins of another order's parcel (ADR 0428).
//
// What is protected is a total, the units the items the reference owns hold,
// and the row that would break it is one not written yet, so no row lock covers
// it. Two opens under different keys, or an open and a join, each read what the
// order owed before either wrote; under this lock the second one reads the
// first one's units before it writes its own.
//
// Two references falling on the same digest only wait for each other. It must be
// called inside [Repository.WithTx]: outside a transaction the lock is released
// at once and protects nothing.
func (r *Repository) LockReferenceDispatch(ctx context.Context, reference string) error {
	if err := requireTx(ctx, "LockReferenceDispatch"); err != nil {
		return err
	}
	tx, _ := txFromContext(ctx)
	if _, err := tx.Exec(ctx, dispatchLockSQL, dispatchLockKey(reference)); err != nil {
		return classify(err, codeQueryFailed, "could not take the reference's dispatch lock")
	}

	return nil
}

// TryLockReferenceDispatch takes the reference's dispatch lock until the
// transaction ends if no other transaction holds it, and answers false at once
// if one does (ADR 0420).
//
// A reader that must not hold a pooled connection while an open's carrier call
// runs asks this one: a bus consumer counting what the order's parcels hold.
// Like [Repository.LockReferenceDispatch] it must be called inside
// [Repository.WithTx].
func (r *Repository) TryLockReferenceDispatch(ctx context.Context, reference string) (bool, error) {
	if err := requireTx(ctx, "TryLockReferenceDispatch"); err != nil {
		return false, err
	}
	tx, _ := txFromContext(ctx)
	var taken bool
	if err := tx.QueryRow(ctx, dispatchTryLockSQL, dispatchLockKey(reference)).Scan(&taken); err != nil {
		return false, classify(err, codeQueryFailed, "could not try the reference's dispatch lock")
	}

	return taken, nil
}

// dispatchLockKey is the reference's key: the class above, the FNV-1a digest of
// the reference below, the same number in every process.
func dispatchLockKey(reference string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(reference))

	return dispatchLockClass<<32 | int64(h.Sum32())
}

// HeldQuantitiesForReference sums, per order line, the units the outgoing
// parcels hold for the reference, found by the reference this module stores on
// each item (ADR 0409, ADR 0428): those of its live parcels and those of its
// parcels that came back undelivered, apart (ADR 0423).
func (r *Repository) HeldQuantitiesForReference(
	ctx context.Context, reference string,
) (map[string]models.HeldUnits, error) {
	rows, err := r.queries(ctx).HeldQuantitiesForReference(ctx, reference)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not sum the units the reference's parcels hold")
	}

	out := make(map[string]models.HeldUnits, len(rows))
	for i := range rows {
		out[rows[i].LineItemID] = models.HeldUnits{Live: rows[i].Live, Back: rows[i].Back}
	}

	return out, nil
}
