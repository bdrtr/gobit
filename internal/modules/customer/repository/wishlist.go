package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// SaveToWishlist puts a variant on the customer's wishlist and returns the item.
//
// The customer row is locked first, as every write to a customer's rows locks
// it (see [Repo.CreateAddress]), and the count and the insert are made under
// that lock, so two saves at once cannot both pass the cap. A variant already
// on the list comes back as it is, with the moment it was first saved, and is
// not counted against the cap again.
func (r *Repo) SaveToWishlist(
	ctx context.Context,
	customerID, variantID string,
	limit int64,
	now time.Time,
) (models.WishlistItem, error) {
	var out models.WishlistItem

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		var err error
		out, err = saveUnderLock(ctx, q, customerID, variantID, limit, now)

		return err
	})
	if err != nil {
		return models.WishlistItem{}, err
	}
	return out, nil
}

// saveUnderLock locks the customer and returns the item, saving it first when
// it is not on the list and the cap leaves room.
func saveUnderLock(
	ctx context.Context, q *customerdb.Queries, customerID, variantID string, limit int64, now time.Time,
) (models.WishlistItem, error) {
	if _, err := q.GetCustomerForUpdate(ctx, customerID); err != nil {
		return models.WishlistItem{}, notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", customerID)
	}

	existing, err := q.GetWishlistItem(ctx, customerdb.GetWishlistItemParams{
		CustomerID: customerID,
		VariantID:  variantID,
	})
	if err == nil {
		return toWishlistItem(existing), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return models.WishlistItem{}, wrapDB(err, "wishlist item could not be read: %s", customerID)
	}

	count, err := q.CountWishlistItems(ctx, customerID)
	if err != nil {
		return models.WishlistItem{}, wrapDB(err, "wishlist could not be counted: %s", customerID)
	}
	if count >= limit {
		return models.WishlistItem{}, errors.Conflict(models.CodeWishlistFull,
			"the wishlist holds %d variants, which is as many as it may", count)
	}

	row, err := q.InsertWishlistItem(ctx, customerdb.InsertWishlistItemParams{
		CustomerID: customerID,
		VariantID:  variantID,
		CreatedAt:  fromTime(now),
	})
	if err != nil {
		return models.WishlistItem{}, wrapDB(err, "wishlist item could not be saved: %s", customerID)
	}

	return toWishlistItem(row), nil
}

// MarkStockAlert puts the variant on the list if it is not there, under the
// same lock and cap as [Repo.SaveToWishlist], and marks it (ADR 0215).
func (r *Repo) MarkStockAlert(
	ctx context.Context, customerID, variantID string, channels []string, limit int64, now time.Time,
) (models.WishlistItem, error) {
	var out models.WishlistItem

	err := r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := saveUnderLock(ctx, q, customerID, variantID, limit, now); err != nil {
			return err
		}
		row, err := q.MarkStockAlert(ctx, customerdb.MarkStockAlertParams{
			Channels: channels, CustomerID: customerID, VariantID: variantID,
		})
		if err != nil {
			return wrapDB(err, "the stock alert could not be set: %s", customerID)
		}
		out = toWishlistItem(row)

		return nil
	})
	if err != nil {
		return models.WishlistItem{}, err
	}
	return out, nil
}

// UnmarkStockAlert clears the mark and leaves the item on the list; an item
// that is not there, or not marked, is left as it is.
func (r *Repo) UnmarkStockAlert(ctx context.Context, customerID, variantID string) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", customerID)
		}
		if _, err := q.UnmarkStockAlert(ctx, customerdb.UnmarkStockAlertParams{
			CustomerID: customerID, VariantID: variantID,
		}); err != nil {
			return wrapDB(err, "the stock alert could not be cleared: %s", customerID)
		}

		return nil
	})
}

// ListStockAlerts pages the marked items of live customers after the given
// key, in key order.
func (r *Repo) ListStockAlerts(
	ctx context.Context, afterCustomerID, afterVariantID string, limit int32,
) ([]models.WishlistItem, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	rows, err := r.q.ListStockAlerts(ctx, customerdb.ListStockAlertsParams{
		AfterCustomerID: afterCustomerID, AfterVariantID: afterVariantID, RowLimit: limit,
	})
	if err != nil {
		return nil, wrapDB(err, "the stock alerts could not be read")
	}
	return toWishlistItems(rows), nil
}

// ArmStockAlert records that a marked variant was seen out of stock, and says
// whether it did.
func (r *Repo) ArmStockAlert(ctx context.Context, customerID, variantID string) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	n, err := r.q.ArmStockAlert(ctx, customerdb.ArmStockAlertParams{CustomerID: customerID, VariantID: variantID})
	if err != nil {
		return false, wrapDB(err, "the stock alert could not be armed: %s", customerID)
	}
	return n > 0, nil
}

// ClearStockAlert clears a mark whose mail went, only if it is still armed at
// the given moment, and says whether it did.
func (r *Repo) ClearStockAlert(ctx context.Context, customerID, variantID string, armedAt time.Time) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}
	n, err := r.q.ClearStockAlert(ctx, customerdb.ClearStockAlertParams{
		CustomerID: customerID, VariantID: variantID, StockAlertArmedAt: fromTime(armedAt),
	})
	if err != nil {
		return false, wrapDB(err, "the stock alert could not be cleared: %s", customerID)
	}
	return n > 0, nil
}

// ListWishlist returns the customer's wishlist, most recently saved first.
func (r *Repo) ListWishlist(ctx context.Context, customerID string) ([]models.WishlistItem, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListWishlistItems(ctx, customerID)
	if err != nil {
		return nil, wrapDB(err, "wishlist could not be read: %s", customerID)
	}
	return toWishlistItems(rows), nil
}

// RemoveFromWishlist takes a variant off the customer's wishlist.
//
// A variant that is not on the list is not an error: the list is left as the
// caller asked for it. The customer row is locked as a save locks it, so a
// removal and a save of the same customer are ordered.
func (r *Repo) RemoveFromWishlist(ctx context.Context, customerID, variantID string) error {
	return r.inTx(ctx, func(q *customerdb.Queries) error {
		if _, err := q.GetCustomerForUpdate(ctx, customerID); err != nil {
			return notFoundOr(err, CodeCustomerNotFound, "customer not found: %s", customerID)
		}

		if _, err := q.DeleteWishlistItem(ctx, customerdb.DeleteWishlistItemParams{
			CustomerID: customerID,
			VariantID:  variantID,
		}); err != nil {
			return wrapDB(err, "wishlist item could not be removed: %s", customerID)
		}
		return nil
	})
}

// WishlistForDisclosure reads every wishlist item saved on the given customers.
//
// It is [Repo.AddressesForDisclosure]'s twin, for the same reasons: one query
// for any number of customers, and no query for none.
func (r *Repo) WishlistForDisclosure(
	ctx context.Context,
	customerIDs []string,
) ([]models.WishlistItem, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	if len(customerIDs) == 0 {
		return []models.WishlistItem{}, nil
	}

	rows, err := r.q.ListWishlistForDisclosure(ctx, customerIDs)
	if err != nil {
		return nil, wrapDB(err, "wishlist could not be read for disclosure")
	}
	return toWishlistItems(rows), nil
}

// toWishlistItem converts a generated row into the domain model.
func toWishlistItem(row customerdb.CustomerWishlistItem) models.WishlistItem {
	return models.WishlistItem{
		CustomerID: row.CustomerID,
		VariantID:  row.VariantID,
		CreatedAt:  toTime(row.CreatedAt),

		StockAlert:         row.StockAlert,
		StockAlertChannels: row.StockAlertChannels,
		StockAlertArmedAt:  toTimePtr(row.StockAlertArmedAt),
	}
}

// toWishlistItems converts generated rows into domain models.
func toWishlistItems(rows []customerdb.CustomerWishlistItem) []models.WishlistItem {
	out := make([]models.WishlistItem, 0, len(rows))
	for i := range rows {
		out = append(out, toWishlistItem(rows[i]))
	}
	return out
}
