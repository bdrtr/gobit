package api

import (
	"context"
)

// TelephoneSurface is the cart module's panel surface: an operator opens a
// cart and adds priced lines to it, the first half of a telephone order (ADR
// 0290). Only primitives cross it, as across every surface the panel resolves
// (ADR 0001).
//
// Each method is the admin API's own act through the same flow the API holds,
// so the panel and the API refuse alike: the region comes from the country, the
// price from the catalog, and a line is scoped to the channel the operator
// names ([scopeToChannel]).
type TelephoneSurface struct {
	flows Flows
}

// NewTelephoneSurface builds the surface over the flows the handler holds.
func NewTelephoneSurface(flows Flows) *TelephoneSurface {
	return &TelephoneSurface{flows: flows}
}

// OpenCart opens a cart for the country's region, for a customer or a guest
// with an e-mail, and returns its id.
func (s *TelephoneSurface) OpenCart(ctx context.Context, countryCode, customerID, email string) (string, error) {
	flow, err := s.flows.opening()
	if err != nil {
		return "", err
	}

	return flow.OpenCartForCountry(ctx, countryCode, customerID, email, "", nil)
}

// AddLine adds a variant priced by the catalog of the named sales channel and
// returns the line's id; the quantity of a variant already in the cart is
// raised.
func (s *TelephoneSurface) AddLine(
	ctx context.Context, cartID, salesChannelID, variantID string, quantity int64,
) (string, error) {
	scoped, err := scopeToChannel(ctx, salesChannelID)
	if err != nil {
		return "", err
	}
	flow, err := s.flows.pricing()
	if err != nil {
		return "", err
	}

	return flow.AddPricedLineItem(scoped, cartID, variantID, quantity, nil, nil, nil)
}
