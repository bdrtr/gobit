package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// RevisePriceList writes the list's title, description and window under its
// lock, only while they are still the ones the caller read, and reports
// whether it did (ADR 0330). The type, the status and the metadata are
// written back as they stand, and the revision is recorded in the list's
// history as an edit is (ADR 0167), since the window is what the ladder reads
// of a list.
func (r *Repo) RevisePriceList(
	ctx context.Context, id string, read, next models.PriceListTerms, clock func() time.Time,
) (models.PriceList, bool, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, false, err
	}

	var row pricingdb.PriceList
	revised := false
	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.GetPriceListForUpdate(ctx, id); err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		current, err := q.GetPriceList(ctx, id)
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		row = current
		list, err := toPriceList(current)
		if err != nil {
			return err
		}
		if !list.Terms().Same(read) {
			return nil
		}
		now := clock()
		row, err = q.UpdatePriceList(ctx, pricingdb.UpdatePriceListParams{
			ID: id, Title: next.Title, Description: next.Description, Type: current.Type,
			Status: current.Status, StartsAt: fromTimePtr(next.StartsAt), EndsAt: fromTimePtr(next.EndsAt),
			Metadata: current.Metadata, UpdatedAt: fromTime(now),
		})
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		revised = true

		return recordListHistory(ctx, q, row, false, now)
	})
	if err != nil {
		return models.PriceList{}, false, err
	}
	list, err := toPriceList(row)

	return list, revised, err
}
