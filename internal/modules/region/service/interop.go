package service

import (
	"context"
)

// This file is region's CROSS-MODULE surface (ADR 0001, ADR 0006).
//
// The signatures here use ONLY primitive and stdlib types. The reason is Go's
// structural conformance rule: since the consuming module cannot import region,
// it cannot name a type such as models.Region in its signature; the moment it
// names one, that becomes ANOTHER type defined in its own package and the
// concrete service does not satisfy the consumer's interface. A signature
// written with primitive types, on the other hand, can be repeated verbatim in
// the consumer's own package and is resolved from the container under the name
// "region.service".
//
// The rich in-module surface (with the models types) is in service.go,
// country.go and currency.go; only region's own API layer and its query
// provider call it.
//
// The surface is DELIBERATELY narrow. There are three things the cart needs
// from region — finding the region from the country, learning the region's
// currency, learning the region's tax — and the fourth (the decimal digits of a
// code) is for presentation. Every method added here raises the cost of moving
// region out into a separate service.

// RegionIDForCountry returns the region ID from the country code.
//
// It is the first step of the cart creation flow: the customer's country is
// known, the region the cart will be attached to is found. If no region is
// found it returns errors.NotFound and its code says which situation is in
// force (see [Service.ResolveRegionForCountry]).
//
// Its counterpart on the consumer side (cart will define this in Phase 5):
//
//	type RegionResolver interface {
//	    RegionIDForCountry(ctx context.Context, countryCode string) (string, error)
//	}
func (s *Service) RegionIDForCountry(ctx context.Context, countryCode string) (string, error) {
	region, err := s.ResolveRegionForCountry(ctx, countryCode)
	if err != nil {
		return "", err
	}
	return region.ID, nil
}

// RegionCurrency returns the region's currency code and its DECIMAL DIGIT
// count.
//
// The two come back together because the caller needs both at the same time and
// two separate calls would mean two round trips: the code says which currency
// the cart will be held in; the digit count says with which factor the minor
// unit integer will be shown (see models.Currency.MinorUnitFactor). A
// presentation layer that does not know the digit count assumes a fixed 100 and
// shows yen amounts a hundred times too small.
//
// If the region does not exist it returns errors.NotFound. If the region exists
// but the currency is not in the reference table it returns errors.NotFound as
// well; because of the foreign key that situation normally cannot arise.
//
// Its counterpart on the consumer side:
//
//	type RegionCurrencyReader interface {
//	    RegionCurrency(ctx context.Context, regionID string) (string, int32, error)
//	}
func (s *Service) RegionCurrency(ctx context.Context, regionID string) (code string, decimalDigits int32, err error) {
	region, err := s.GetRegion(ctx, regionID)
	if err != nil {
		return "", 0, err
	}

	currency, err := s.repo.GetCurrency(ctx, region.CurrencyCode)
	if err != nil {
		return "", 0, err
	}
	return currency.Code, currency.DecimalDigits, nil
}

// RegionTax returns the region's FALLBACK tax rate (basis points) and whether
// the tax will be applied automatically.
//
// In Phase 7 the tax module TOOK OVER the tax calculation, but this method was
// NOT REMOVED: the cart flow goes on using it as the FALLBACK path
// (internal/workflows/cart, the section on where the tax comes FROM). It is
// called in two situations: when the tax module is not registered at all, and
// when a COUNTRY cannot be resolved from the cart's region — the tax module
// cannot compute without a country.
//
// So this surface is PERMANENT. If it is removed, an installation that runs
// without the tax module (e.g. a small single-region shop) silently zeroes the
// tax. Since the compiler does not check the contract (ADR 0006), the decision
// to remove it can only be made by reading internal/workflows/cart.
//
// The rate is an integer and it is in basis points (2000 = 20%): a float rate,
// multiplied by an amount, would produce silent rounding at the cent level
// (plan Section 8). The caller has to compute the tax in the form
// "amount * rate / 10000", with integer arithmetic.
//
// Its counterpart on the consumer side:
//
//	type RegionTaxReader interface {
//	    RegionTax(ctx context.Context, regionID string) (int32, bool, error)
//	}
func (s *Service) RegionTax(ctx context.Context, regionID string) (rateBps int32, automatic bool, err error) {
	region, err := s.GetRegion(ctx, regionID)
	if err != nil {
		return 0, false, err
	}
	return region.TaxRate, region.AutomaticTaxes, nil
}

// CurrencyDecimalDigits returns the decimal digit count of a currency code.
//
// It is for callers that hold only the currency code and not a region (e.g. the
// currency of an order record). If you are going through the region,
// [Service.RegionCurrency] gives both in a single call.
//
// Its counterpart on the consumer side:
//
//	type CurrencyReader interface {
//	    CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error)
//	}
func (s *Service) CurrencyDecimalDigits(ctx context.Context, currencyCode string) (int32, error) {
	currency, err := s.GetCurrency(ctx, currencyCode)
	if err != nil {
		return 0, err
	}
	return currency.DecimalDigits, nil
}
