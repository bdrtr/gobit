package repository

import (
	"context"
	"hash/fnv"
)

// dispatchLockSQL takes the transaction-lifetime advisory lock per reference.
//
// It is not generated through sqlc: it touches none of this module's tables, it
// is a concurrency primitive, the shape the order module's spending lock has.
const dispatchLockSQL = `SELECT pg_advisory_xact_lock($1)`

// dispatchLockClass is the class of the dispatch lock, the upper 32 bits of its
// key; internal/arch holds every class in the tree apart.
const dispatchLockClass int64 = 8

// LockReferenceDispatch serializes the outgoing parcels opened for one reference
// until the transaction ends (ADR 0409, gap D265).
//
// What is protected is a total, the units the reference's live parcels hold, and
// the parcel that would break it is a row not written yet, so no row lock covers
// it. Two opens under different keys each read what the order owed before
// either wrote; under this lock the second one reads the first one's units
// before it writes its own.
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

// dispatchLockKey is the reference's key: the class above, the FNV-1a digest of
// the reference below, the same number in every process.
func dispatchLockKey(reference string) int64 {
	h := fnv.New32a()
	// hash.Hash.Write never returns an error (a documented contract).
	_, _ = h.Write([]byte(reference))

	return dispatchLockClass<<32 | int64(h.Sum32())
}

// CommittedQuantitiesForReference sums, per order line, the units the LIVE
// outgoing parcels opened for the reference hold, found by the reference this
// module stores (ADR 0409).
func (r *Repository) CommittedQuantitiesForReference(
	ctx context.Context, reference string,
) (map[string]int64, error) {
	rows, err := r.queries(ctx).CommittedQuantitiesForReference(ctx, reference)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not sum the units the reference's parcels hold")
	}

	out := make(map[string]int64, len(rows))
	for i := range rows {
		out[rows[i].LineItemID] = rows[i].Quantity
	}

	return out, nil
}
