package service

import "context"

// CustomerBoughtAnyOf reports whether the customer has an order that was not
// canceled with a line of one of the variants (ADR 0372).
//
// A review asks it of the customer a storefront request PROVED, so its
// "verified purchase" rests on the customer's own orders and never on an
// identifier a request body carries. No variants is no purchase, answered
// without a read.
func (s *Service) CustomerBoughtAnyOf(ctx context.Context, customerID string, variantIDs []string) (bool, error) {
	if err := requireID("customer_id", customerID); err != nil {
		return false, err
	}
	if len(variantIDs) == 0 {
		return false, nil
	}

	return s.store.CustomerBoughtAnyOf(ctx, customerID, variantIDs)
}
