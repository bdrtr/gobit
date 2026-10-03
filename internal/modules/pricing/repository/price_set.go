package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceSet creates a price set with the given id and writes its prices in
// the SAME TRANSACTION.
//
// The container and its prices HAVE TO be in one transaction: with two separate
// transactions, when the database refuses the price write (e.g. a price bound
// to a price list that does not exist), the container would already be
// committed, and even though the caller got an error, a container without
// prices, bound to nothing, would be left behind.
//
// prices may be empty; in that case only the container is written.
func (r *Repo) CreatePriceSet(
	ctx context.Context,
	id string,
	prices []models.Price,
	now time.Time,
) (models.PriceSet, error) {
	var set models.PriceSet

	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		row, err := q.InsertPriceSet(ctx, pricingdb.InsertPriceSetParams{
			ID:        id,
			CreatedAt: fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the price set could not be created")
		}
		set = toPriceSet(row)

		// Because the container is created in this transaction, nobody else can
		// reach it yet; the row lock the replacement takes is not needed here.
		if _, err = insertPrices(ctx, q, id, prices, now); err != nil {
			return err
		}

		// The set's first snapshot: its history starts with it (ADR 0167).
		return recordSetHistory(ctx, q, id, now)
	})
	if err != nil {
		return models.PriceSet{}, err
	}
	return set, nil
}

// GetPriceSet returns the price set with the given id; errors.NotFound if
// there is none.
func (r *Repo) GetPriceSet(ctx context.Context, id string) (models.PriceSet, error) {
	if err := r.ready(); err != nil {
		return models.PriceSet{}, err
	}

	row, err := r.q.GetPriceSet(ctx, id)
	if err != nil {
		return models.PriceSet{}, notFoundOr(err, CodePriceSetNotFound, "price set not found: %s", id)
	}
	return toPriceSet(row), nil
}

// ListPriceSets returns a paged list of price sets and the TOTAL number of
// records.
//
// The total is independent of the page length; the "count" field in the API
// envelope lets the client know how many pages there are.
func (r *Repo) ListPriceSets(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListPriceSets(ctx, pricingdb.ListPriceSetsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "the price set list could not be read")
	}

	total, err := r.q.CountPriceSets(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the price set count could not be read")
	}

	sets := make([]models.PriceSet, 0, len(rows))
	for _, row := range rows {
		sets = append(sets, toPriceSet(row))
	}
	return sets, total, nil
}

// GetPriceSetsByIDs returns the price sets matching the given ids in ONE query.
// No record is returned for an id that is not found; that is not an error (the
// Query layer's FetchByIDs contract, ADR 0004).
func (r *Repo) GetPriceSetsByIDs(ctx context.Context, ids []string) ([]models.PriceSet, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.PriceSet{}, nil
	}

	rows, err := r.q.GetPriceSetsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the price sets could not be read")
	}

	sets := make([]models.PriceSet, 0, len(rows))
	for _, row := range rows {
		sets = append(sets, toPriceSet(row))
	}
	return sets, nil
}

// DeletePriceSet soft-deletes the price set; errors.NotFound if there is none.
//
// The prices are stamped in the same transaction: deleting the container and
// leaving its prices live meant that a deleted container's prices showed up in
// listings. This stamp is indispensable, because NOTHING ELSE on the
// calculation path checks that the container is live — ListPriceCandidates
// never JOINs price_set, and the service reads the container only when zero
// candidates come back.
//
// After ADR 0047 this is the ONLY writer of price.deleted_at: the replacement no
// longer stamps, it deletes (see [Repo.ReplacePrices]). That is why the column
// has a single meaning — a stamped price row can only have come from a deleted
// container — and that comes not from the schema but from the number of
// callers.
func (r *Repo) DeletePriceSet(ctx context.Context, id string, now time.Time) error {
	return r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.SoftDeletePriceSet(ctx, pricingdb.SoftDeletePriceSetParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodePriceSetNotFound, "price set not found: %s", id)
		}

		if err := q.SoftDeletePricesBySet(ctx, pricingdb.SoftDeletePricesBySetParams{
			PriceSetID: id,
			DeletedAt:  fromTime(now),
		}); err != nil {
			return wrapDB(err, "the price set's prices could not be deleted: %s", id)
		}

		// A deleted set offers no price from now on, and its history says so
		// with an empty snapshot (ADR 0167).
		return recordSetHistory(ctx, q, id, now)
	})
}

// toPriceSet turns the generated row into the domain model.
func toPriceSet(row pricingdb.PriceSet) models.PriceSet {
	return models.PriceSet{
		ID:        row.ID,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}
