package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// This file is the access to the location selection POLICY: which location
// serves which region and in which order it is preferred.
//
// Two tables together carry a single concept (shipping_locations and
// shipping_location_regions) and they leave this package as a SINGLE model: a
// surface on which the caller saw two tables would also hand over to it the
// responsibility of managing the region links separately.
//
// Deletion here IS NOT SOFT; the reasoning is at the top of the migration. That
// is why no query has a deleted_at filter.

// UpsertShippingLocation writes, or overwrites, the location's PRIORITY.
//
// It DOES NOT TOUCH the region links: those are written by
// [Repository.ReplaceShippingLocationRegions]. The two together count as a
// single write and the caller must call them in the SAME transaction (see the
// service layer); calling them separately means that a read in between sees the
// location with its new priority but with its old regions.
func (r *Repository) UpsertShippingLocation(
	ctx context.Context,
	locationID string,
	priority int64,
) (models.ShippingLocation, error) {
	row, err := r.queries(ctx).UpsertShippingLocation(ctx, fulfillmentdb.UpsertShippingLocationParams{
		LocationID: locationID,
		Priority:   priority,
	})
	if err != nil {
		return models.ShippingLocation{}, classify(err, codeQueryFailed, "could not write location policy")
	}
	return toShippingLocation(row), nil
}

// ReplaceShippingLocationRegions writes the location's region links WHOLESALE.
//
// An empty slice is valid input and means "serve all regions"; the links are
// deleted and nothing is written in their place.
//
// It must only be called inside [Repository.WithTx]: it consists of two
// statements (delete, write) and when it is called without a transaction a read
// in between sees the location WITH NO REGIONS — that is, it finds it open to
// ALL regions at a moment when its scope was believed to have narrowed.
func (r *Repository) ReplaceShippingLocationRegions(
	ctx context.Context,
	locationID string,
	regionIDs []string,
) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"region links cannot be written outside a transaction: %s", locationID)
	}

	q := r.queries(ctx)
	if err := q.DeleteShippingLocationRegions(ctx, locationID); err != nil {
		return classify(err, codeQueryFailed, "could not delete location region links")
	}
	if len(regionIDs) == 0 {
		return nil
	}

	err := q.InsertShippingLocationRegions(ctx, fulfillmentdb.InsertShippingLocationRegionsParams{
		LocationID: locationID,
		RegionIds:  regionIDs,
	})
	if err != nil {
		return classify(err, codeQueryFailed, "could not write location region links")
	}
	return nil
}

// GetShippingLocation returns the location's policy with its regions; NotFound
// if there is none.
//
// The read is a SINGLE statement. Two separate SELECTs (first the row, then the
// links) would produce a torn record: two reads made outside a transaction come
// from two different snapshots and a write landing between them would show the
// location's NEW priority side by side with its OLD regions. The write path
// closes this tear with a transaction; the read path closes it with a single
// statement.
func (r *Repository) GetShippingLocation(
	ctx context.Context,
	locationID string,
) (models.ShippingLocation, error) {
	row, err := r.queries(ctx).GetShippingLocation(ctx, locationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.ShippingLocation{}, locationNotFound(locationID)
		}
		return models.ShippingLocation{}, classify(err, codeQueryFailed, "could not read location policy")
	}
	return models.ShippingLocation{
		LocationID: row.LocationID,
		Priority:   row.Priority,
		RegionIDs:  row.RegionIds,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}, nil
}

// ListShippingLocations paginates the policies together with their links; the
// second value is the count of ALL rows.
//
// The links are collected INSIDE the page query: no second query per location
// (N+1) is made and the tearing door of the single read stays closed here too.
func (r *Repository) ListShippingLocations(
	ctx context.Context,
	filter models.LocationFilter,
) ([]models.ShippingLocation, int64, error) {
	q := r.queries(ctx)

	rows, err := q.ListShippingLocations(ctx, fulfillmentdb.ListShippingLocationsParams{
		RowLimit:  filter.Limit,
		RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not list location policies")
	}

	total, err := q.CountShippingLocations(ctx)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "could not count location policies")
	}

	out := make([]models.ShippingLocation, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.ShippingLocation{
			LocationID: row.LocationID,
			Priority:   row.Priority,
			RegionIDs:  row.RegionIds,
			CreatedAt:  toTime(row.CreatedAt),
			UpdatedAt:  toTime(row.UpdatedAt),
		})
	}
	return out, total, nil
}

// DeleteShippingLocation deletes the policy PERMANENTLY; the region links fall
// with it. NotFound if there is no such record.
//
// The number of deleted rows is checked because DELETE returns without an error
// for a row that does not exist as well: without the check, a deletion made with
// a wrong identifier would look successful.
func (r *Repository) DeleteShippingLocation(ctx context.Context, locationID string) error {
	affected, err := r.queries(ctx).DeleteShippingLocation(ctx, locationID)
	if err != nil {
		return classify(err, codeQueryFailed, "could not delete location policy")
	}
	if affected == 0 {
		return locationNotFound(locationID)
	}
	return nil
}

// LocationPolicies returns, in a SINGLE query, the facts about the candidate
// locations that affect the decision at selection time.
//
// The returned slice contains ONLY the candidates that HAVE a policy and it can
// be SHORTER than the candidate list. A missing candidate is not an error: a
// location without a policy counts as the default and the caller makes that
// distinction.
//
// The target region IS NOT A PARAMETER: the matching is done not by the query
// but by the pure function in the service layer. Had the region been given to
// SQL, the rule would have moved into the database and become untestable without
// a real Postgres; moreover the regions the eliminated candidates are bound to
// would not come back, which means the error message could not write its reason.
//
// If the candidate list is empty no query is made at all.
func (r *Repository) LocationPolicies(
	ctx context.Context,
	locationIDs []string,
) ([]models.LocationPolicy, error) {
	if len(locationIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ShippingLocationPolicies(ctx, locationIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read location policies")
	}

	out := make([]models.LocationPolicy, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.LocationPolicy{
			LocationID: row.LocationID,
			Priority:   row.Priority,
			RegionIDs:  row.RegionIds,
		})
	}
	return out, nil
}

// toShippingLocation converts the row returned by the priority write into the
// model.
//
// The region field stays EMPTY and this DOES NOT mean "it serves all regions" —
// the record returned by this path is incomplete. The caller should not use it
// directly: the only caller of the priority write is the service, which WRITES
// the links right afterwards within the same transaction and READS the result
// back with GetShippingLocation.
func toShippingLocation(row fulfillmentdb.ShippingLocation) models.ShippingLocation {
	return models.ShippingLocation{
		LocationID: row.LocationID,
		Priority:   row.Priority,
		CreatedAt:  toTime(row.CreatedAt),
		UpdatedAt:  toTime(row.UpdatedAt),
	}
}
