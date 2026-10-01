package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// RevisePrices rewrites a price set from the prices it holds under the set's
// lock (D193).
//
// A write that changes part of a set — one currency's base price — has to read
// the rest to write it back, since the set is replaced whole. Read before the
// lock, two such writes to one set each wrote back the other's currency as it
// was before, and one of the two changes was lost with both reporting success.
// Here the lock is taken first, the prices are read in the same transaction,
// and revise decides from them what the set becomes; when it reports no write,
// nothing is written and the current prices are returned.
func (r *Repo) RevisePrices(
	ctx context.Context,
	priceSetID string,
	revise func(current []models.Price) (next []models.Price, write bool, err error),
	clock func() time.Time,
) ([]models.Price, error) {
	var out []models.Price

	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.GetPriceSetForUpdate(ctx, priceSetID); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set not found: %s", priceSetID)
		}

		rows, err := q.ListPricesBySet(ctx, priceSetID)
		if err != nil {
			return wrapDB(err, "the prices of %s could not be read", priceSetID)
		}
		current := make([]models.Price, 0, len(rows))
		for i := range rows {
			current = append(current, toPrice(rows[i]))
		}
		if err := attachRulesWith(ctx, q, current); err != nil {
			return err
		}

		next, write, err := revise(current)
		if err != nil {
			return err
		}
		if !write {
			out = current
			return nil
		}

		out, err = replaceLocked(ctx, q, priceSetID, next, clock())

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// replaceLocked replaces the set's prices with prices, for a caller that holds
// the set's lock in the transaction q belongs to. The replaced prices are
// deleted, as ADR 0047 decided; what they were is kept in the snapshot before
// this one, and what replaced them in this one (ADR 0167).
func replaceLocked(
	ctx context.Context, q *pricingdb.Queries, priceSetID string, prices []models.Price, now time.Time,
) ([]models.Price, error) {
	if err := q.DeletePricesBySet(ctx, priceSetID); err != nil {
		return nil, wrapDB(err, "the old prices of %s could not be deleted", priceSetID)
	}

	written, err := insertPrices(ctx, q, priceSetID, prices, now)
	if err != nil {
		return nil, err
	}

	if err := recordSetHistory(ctx, q, priceSetID, now); err != nil {
		return nil, err
	}

	return written, nil
}
