package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// SwitchPriceListStatus moves the list from the status the caller read to
// another, under the list's lock and only while it is still in the first, and
// reports whether it did (ADR 0328). Every other field is written back as it
// stands, and the move is recorded in the list's history as an edit is (ADR
// 0167), since the status is what the ladder reads of a list.
func (r *Repo) SwitchPriceListStatus(
	ctx context.Context, id string, from, to models.PriceListStatus, clock func() time.Time,
) (models.PriceList, bool, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, false, err
	}

	var row pricingdb.PriceList
	switched := false
	err := r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.GetPriceListForUpdate(ctx, id); err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		current, err := q.GetPriceList(ctx, id)
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		if current.Status != string(from) {
			row = current
			return nil
		}
		now := clock()
		row, err = q.UpdatePriceList(ctx, pricingdb.UpdatePriceListParams{
			ID: id, Title: current.Title, Description: current.Description, Type: current.Type,
			Status: string(to), StartsAt: current.StartsAt, EndsAt: current.EndsAt,
			Metadata: current.Metadata, UpdatedAt: fromTime(now),
		})
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}
		switched = true

		return recordListHistory(ctx, q, row, false, now)
	})
	if err != nil {
		return models.PriceList{}, false, err
	}
	list, err := toPriceList(row)

	return list, switched, err
}
