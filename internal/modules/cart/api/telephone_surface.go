package api

import (
	"context"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// TelephoneSurface is the cart module's panel surface: an operator opens a
// cart, adds priced lines (ADR 0290), writes the shipping address, chooses the
// shipping method and completes the cart with an offline method (ADR 0291).
// Only primitives cross it, as across every surface the panel resolves (ADR
// 0001).
//
// Each method is the admin API's own act through the handler the API runs, so
// the panel and the API refuse alike: the region comes from the country, the
// price from the catalog of the channel the operator names ([scopeToChannel]),
// an address write reprices the cart, and the completion takes only a method
// whose money comes later.
type TelephoneSurface struct {
	h *Handler
}

// NewTelephoneSurface builds the surface over the handler the API runs.
func NewTelephoneSurface(h *Handler) *TelephoneSurface {
	return &TelephoneSurface{h: h}
}

// The keys [TelephoneSurface.SetShippingAddress] reads, the address body's
// own names.
const (
	AddressFirstName   = "first_name"
	AddressLastName    = "last_name"
	AddressCompany     = "company"
	AddressLine1       = "address_1"
	AddressLine2       = "address_2"
	AddressCity        = "city"
	AddressProvince    = "province"
	AddressPostalCode  = "postal_code"
	AddressCountryCode = "country_code"
	AddressPhone       = "phone"
)

// OpenCart opens a cart for the country's region, for a customer or a guest
// with an e-mail, and returns its id.
func (s *TelephoneSurface) OpenCart(ctx context.Context, countryCode, customerID, email string) (string, error) {
	flow, err := s.h.opening()
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
	flow, err := s.h.pricing()
	if err != nil {
		return "", err
	}

	return flow.AddPricedLineItem(scoped, cartID, variantID, quantity, nil, nil, nil)
}

// SetShippingAddress writes the cart's shipping address from the Address*
// keys and reprices the cart, as the address endpoint does; a key it does not
// name is left out.
func (s *TelephoneSurface) SetShippingAddress(ctx context.Context, cartID string, address map[string]string) error {
	body := addressRequest{
		FirstName:   address[AddressFirstName],
		LastName:    address[AddressLastName],
		Company:     address[AddressCompany],
		Address1:    address[AddressLine1],
		Address2:    address[AddressLine2],
		City:        address[AddressCity],
		Province:    address[AddressProvince],
		PostalCode:  address[AddressPostalCode],
		CountryCode: address[AddressCountryCode],
		Phone:       address[AddressPhone],
	}

	return s.h.repriced(ctx, cartID, func() error {
		_, err := s.h.svc.SetShippingAddress(ctx, cartID, body.toInput())
		return err
	})
}

// ShippingOptions lists the shipping options the cart can take, each its id,
// name and amount in the cart's currency (ADR 0292).
func (s *TelephoneSurface) ShippingOptions(
	ctx context.Context, cartID string,
) (ids, names []string, amounts []int64, err error) {
	flow, err := s.h.shipping()
	if err != nil {
		return nil, nil, nil, err
	}
	options, err := shippingOptionsOf(ctx, flow, cartID)
	if err != nil {
		return nil, nil, nil, err
	}

	for _, option := range options {
		ids = append(ids, option.ID)
		names = append(names, option.Name)
		amounts = append(amounts, option.Amount)
	}

	return ids, names, amounts, nil
}

// AddShippingMethod prices the shipping option for the cart and adds it,
// returning the method's id.
func (s *TelephoneSurface) AddShippingMethod(ctx context.Context, cartID, shippingOptionID string) (string, error) {
	flow, err := s.h.shipping()
	if err != nil {
		return "", err
	}

	return flow.AddQuotedShippingMethod(ctx, cartID, shippingOptionID, nil)
}

// Complete completes the cart in the named channel with an offline method,
// against the total the operator read to the caller, and returns the order and
// what it owes. A total that moved since is refused, and so is a method whose
// money moves at the checkout (ADR 0286).
func (s *TelephoneSurface) Complete(
	ctx context.Context, cartID, salesChannelID, paymentProviderID string, expectedTotal int64,
) (orderID string, outstanding int64, err error) {
	scoped, err := scopeToChannel(ctx, salesChannelID)
	if err != nil {
		return "", 0, err
	}

	result, err := s.h.complete(scoped, completeCartFlowRequest{
		CartID:            cartID,
		PaymentProviderID: paymentProviderID,
		SalesChannelIDs:   corehttp.SalesChannelIDs(scoped),
		ExpectedTotal:     expectedTotal,
		OfflineOnly:       true,
	})
	if err != nil {
		return "", 0, err
	}

	return result.OrderID, result.Outstanding, nil
}
