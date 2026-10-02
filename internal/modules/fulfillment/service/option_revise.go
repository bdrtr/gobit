package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// CodeOptionRevised refuses a shipping option's revision when another writer
// revised the option since the caller read it (ADR 0333).
const CodeOptionRevised = "fulfillment_shipping_option_revised"

// ReviseShippingOption renames the option, sets its fee and says whether the
// storefront offers it, writing them only while they are the ones the caller
// read, and refuses with [CodeOptionRevised] when another writer changed any
// of them since (ADR 0333). The name is checked as a new option's is and the
// fee as a flat option's; a calculated option keeps a fee of zero, its fee
// being the provider's, which the schema holds. The provider, the profile,
// the price type and the region are kept.
func (s *Service) ReviseShippingOption(
	ctx context.Context, id string, read, next models.OptionTerms,
) (models.ShippingOption, error) {
	if err := requireID(id, models.ShippingOptionIDPrefix, "the shipping option identifier"); err != nil {
		return models.ShippingOption{}, err
	}
	next.Name = strings.TrimSpace(next.Name)
	if err := requireText("the option name", next.Name); err != nil {
		return models.ShippingOption{}, err
	}
	if err := requireAmount("the shipping fee", next.Amount); err != nil {
		return models.ShippingOption{}, err
	}

	option, revised, err := s.store.ReviseShippingOption(ctx, id, read, next)
	if err != nil || revised {
		return option, err
	}

	// Nothing was written: the option is gone, or it moved since it was read.
	current, err := s.store.GetShippingOption(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}

	return models.ShippingOption{}, errors.Conflict(CodeOptionRevised,
		"shipping option %s was revised since it was read: it is %q now; draw the list again", id, current.Name)
}
