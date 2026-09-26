package service

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The refusals of a shipping address correction (ADR 0195).
const (
	// CodeAddressNotCorrectable refuses a correction on an order that is not
	// pending, or whose personal data was erased.
	CodeAddressNotCorrectable = "order_address_not_correctable"
	// CodeAddressMissing refuses a correction on an order that recorded no
	// shipping address: there is nothing to correct, and no country it was
	// taxed and quoted in.
	CodeAddressMissing = "order_address_missing"
	// CodeAddressCountryChanged refuses a correction into another country.
	CodeAddressCountryChanged = "order_address_country_changed"
)

// CorrectShippingAddress replaces the order's current shipping address and
// returns the address that is current afterwards (ADR 0195).
//
// The row the order was placed with is not edited. It is closed, and the
// corrected address is written as a new row, in one transaction under the
// order's lock, so a correction and a cancellation or a second correction run
// one after the other.
//
// What is refused:
//
//   - An order that is not pending: a completed order's goods went where they
//     went, and a canceled one ships nothing.
//   - An order whose personal data was erased: writing an address back would
//     restore what the person asked to have removed.
//   - An order with no shipping address: there is nothing to correct, and the
//     country its tax and shipping price rest on was never recorded.
//   - Another country. The tax and the shipping price were computed on the
//     country, so keeping it keeps both true; a different country is a
//     different sale. An empty country in the correction means the current one.
//
// A correction identical to the current address writes nothing and returns it,
// so a repeated request is not a second correction.
//
// This module does not know whether a parcel is on its way; the caller that
// can read the parcels asks that first.
func (s *Service) CorrectShippingAddress(
	ctx context.Context, orderID string, corrected models.OrderAddress,
) (models.OrderAddress, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderAddress{}, err
	}

	var current models.OrderAddress
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if order.Status != models.OrderPending {
			return errors.Conflict(CodeAddressNotCorrectable,
				"order %s is %s; only a pending order's address can be corrected", orderID, order.Status)
		}
		if order.PersonalDataErasedAt != nil {
			return errors.Conflict(CodeAddressNotCorrectable,
				"order %s's personal data was erased; an address is not written back", orderID)
		}

		addresses, err := s.store.OrderAddressesByOrderIDs(ctx, []string{orderID})
		if err != nil {
			return err
		}
		shipping, _ := splitAddresses(addresses[orderID])
		if shipping == nil {
			return errors.Conflict(CodeAddressMissing,
				"order %s recorded no shipping address, so there is none to correct", orderID)
		}

		country := strings.ToUpper(strings.TrimSpace(corrected.CountryCode))
		if country == "" {
			country = shipping.CountryCode
		}
		if country != shipping.CountryCode {
			return errors.Conflict(CodeAddressCountryChanged,
				"order %s ships to %s; a correction keeps the country, and %s is another sale",
				orderID, shipping.CountryCode, country)
		}
		corrected.CountryCode = country

		if sameAddress(*shipping, corrected) {
			current = *shipping
			return nil
		}

		if _, err := s.store.SupersedeOrderAddress(ctx, orderID, models.AddressShipping); err != nil {
			return err
		}
		corrected.ID = models.NewOrderAddressID()
		corrected.OrderID = orderID
		corrected.Type = models.AddressShipping
		corrected.SourceAddressID = ""
		written, err := s.store.CreateOrderAddress(ctx, corrected)
		if err != nil {
			return err
		}
		current = written

		return nil
	})
	if err != nil {
		return models.OrderAddress{}, err
	}

	return current, nil
}

// sameAddress reports whether the correction says what the current address
// already says, field by field, the metadata compared as data.
func sameAddress(current, corrected models.OrderAddress) bool {
	if current.FirstName != corrected.FirstName || current.LastName != corrected.LastName ||
		current.Company != corrected.Company || current.Address1 != corrected.Address1 ||
		current.Address2 != corrected.Address2 || current.City != corrected.City ||
		current.Province != corrected.Province || current.PostalCode != corrected.PostalCode ||
		current.CountryCode != corrected.CountryCode || current.Phone != corrected.Phone {
		return false
	}

	return sameMetadata(current.Metadata, corrected.Metadata)
}

// sameMetadata compares two free-form maps by their JSON, so an absent map and
// an empty one are the same and a number decoded two ways is one number.
func sameMetadata(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)

	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}
