package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// ReviseShippingOption writes the option's name, fee, storefront visibility
// and delivery days while they are still the ones the caller read, and reports
// whether it did (ADR 0333, ADR 0421). An option that moved since, or that is not
// there, is left as it is, and the caller tells which; a fee on a calculated
// option is refused by the schema, in the words a new option's is.
func (r *Repository) ReviseShippingOption(
	ctx context.Context, id string, read, next models.OptionTerms,
) (models.ShippingOption, bool, error) {
	minDays, maxDays := fromDeliveryDays(next.DeliveryDays)
	readMin, readMax := fromDeliveryDays(read.DeliveryDays)
	row, err := r.queries(ctx).ReviseShippingOption(ctx, fulfillmentdb.ReviseShippingOptionParams{
		ID: id, Name: next.Name, Amount: next.Amount, AdminOnly: next.AdminOnly,
		DeliveryMinDays: minDays, DeliveryMaxDays: maxDays,
		ReadName: read.Name, ReadAmount: read.Amount, ReadAdminOnly: read.AdminOnly,
		ReadDeliveryMinDays: readMin, ReadDeliveryMaxDays: readMax,
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return models.ShippingOption{}, false, nil
	case err != nil:
		return models.ShippingOption{}, false, classify(err, codeQueryFailed, "could not revise shipping option")
	}
	option, err := toOption(row)

	return option, err == nil, err
}
