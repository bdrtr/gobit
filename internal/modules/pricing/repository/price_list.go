package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceList creates a new price list.
func (r *Repo) CreatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	meta, err := fromJSONMap(list.Metadata)
	if err != nil {
		return models.PriceList{}, err
	}

	var row pricingdb.PriceList
	err = r.inTx(ctx, func(q *pricingdb.Queries) error {
		var err error
		row, err = q.InsertPriceList(ctx, pricingdb.InsertPriceListParams{
			ID:          list.ID,
			Title:       list.Title,
			Description: list.Description,
			Type:        string(list.Type),
			Status:      string(list.Status),
			StartsAt:    fromTimePtr(list.StartsAt),
			EndsAt:      fromTimePtr(list.EndsAt),
			Metadata:    meta,
			CreatedAt:   fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the price list could not be created")
		}

		return recordListHistory(ctx, q, row, false, now)
	})
	if err != nil {
		return models.PriceList{}, err
	}
	return toPriceList(row)
}

// GetPriceList returns the list by id; errors.NotFound when there is none.
func (r *Repo) GetPriceList(ctx context.Context, id string) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	row, err := r.q.GetPriceList(ctx, id)
	if err != nil {
		return models.PriceList{}, notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
	}
	return toPriceList(row)
}

// ListPriceLists returns the paged set of lists and the TOTAL record count.
func (r *Repo) ListPriceLists(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListPriceLists(ctx, pricingdb.ListPriceListsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "the price lists could not be read")
	}

	total, err := r.q.CountPriceLists(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the price list count could not be read")
	}

	lists := make([]models.PriceList, 0, len(rows))
	for i := range rows {
		list, convErr := toPriceList(rows[i])
		if convErr != nil {
			return nil, 0, convErr
		}
		lists = append(lists, list)
	}
	return lists, total, nil
}

// UpdatePriceList writes every updatable field of the list; errors.NotFound
// when there is none.
//
// The list's row is locked first and the clock read after the lock (ADR 0242):
// the update and its snapshot are stamped with the moment of the write, so an
// update that waited for another is recorded after it.
func (r *Repo) UpdatePriceList(ctx context.Context, list models.PriceList, clock func() time.Time) (models.PriceList, error) {
	if err := r.ready(); err != nil {
		return models.PriceList{}, err
	}

	meta, err := fromJSONMap(list.Metadata)
	if err != nil {
		return models.PriceList{}, err
	}

	var row pricingdb.PriceList
	err = r.inTx(ctx, func(q *pricingdb.Queries) error {
		if _, err := q.GetPriceListForUpdate(ctx, list.ID); err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", list.ID)
		}
		now := clock()

		var err error
		row, err = q.UpdatePriceList(ctx, pricingdb.UpdatePriceListParams{
			ID:          list.ID,
			Title:       list.Title,
			Description: list.Description,
			Type:        string(list.Type),
			Status:      string(list.Status),
			StartsAt:    fromTimePtr(list.StartsAt),
			EndsAt:      fromTimePtr(list.EndsAt),
			Metadata:    meta,
			UpdatedAt:   fromTime(now),
		})
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", list.ID)
		}

		// Status and window are what the ladder reads of a list; a sale that
		// opens is this snapshot's next reader (ADR 0167).
		return recordListHistory(ctx, q, row, false, now)
	})
	if err != nil {
		return models.PriceList{}, err
	}
	return toPriceList(row)
}

// DeletePriceList deletes the list with a soft delete; errors.NotFound when
// there is none.
//
// The prices bound to the list are NOT DELETED but are left out of the
// calculation: the LEFT JOIN in the candidate query does not see the deleted
// list, and the service does not count a price whose list has gone missing.
// Keeping the prices is deliberate — if a list is deleted by mistake, restoring
// it is a one-line operation.
func (r *Repo) DeletePriceList(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *pricingdb.Queries) error {
		list, err := q.GetPriceList(ctx, id)
		if err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}

		if _, err := q.SoftDeletePriceList(ctx, pricingdb.SoftDeletePriceListParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodePriceListNotFound, "price list not found: %s", id)
		}

		// Its prices stay, and stop competing; the snapshot says why (ADR 0167).
		return recordListHistory(ctx, q, list, true, now)
	})
}

// toPriceList converts the generated row into the domain model.
func toPriceList(row pricingdb.PriceList) (models.PriceList, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.PriceList{}, err
	}

	return models.PriceList{
		ID:          row.ID,
		Title:       row.Title,
		Description: row.Description,
		Type:        models.PriceListType(row.Type),
		Status:      models.PriceListStatus(row.Status),
		StartsAt:    toTimePtr(row.StartsAt),
		EndsAt:      toTimePtr(row.EndsAt),
		Metadata:    meta,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
	}, nil
}
