package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the access to the cart_addresses table. An address is written per
// cart and per type, and it is written by UPSERT rather than by insert/update, so
// its queries do not resemble the cart's. The cart itself is in repository.go.

// --- addresses ---------------------------------------------------------------

// UpsertCartAddress writes the cart's address of the given type; it overwrites an
// existing one.
//
// The ID is used only for a NEW row; while an existing record is updated its ID
// is KEPT. The ID staying stable means that a reference given to the address (a
// log record, an order copy) stays valid after a correction as well.
func (r *Repository) UpsertCartAddress(ctx context.Context, addr models.CartAddress) (models.CartAddress, error) {
	meta, err := fromJSONMap(addr.Metadata)
	if err != nil {
		return models.CartAddress{}, err
	}

	row, err := r.queries(ctx).UpsertCartAddress(ctx, cartdb.UpsertCartAddressParams{
		ID:              addr.ID,
		CartID:          addr.CartID,
		AddressType:     addr.Type.String(),
		SourceAddressID: nullString(addr.SourceAddressID),
		FirstName:       nullString(addr.FirstName),
		LastName:        nullString(addr.LastName),
		Company:         nullString(addr.Company),
		Address1:        nullString(addr.Address1),
		Address2:        nullString(addr.Address2),
		City:            nullString(addr.City),
		Province:        nullString(addr.Province),
		PostalCode:      nullString(addr.PostalCode),
		CountryCode:     nullString(addr.CountryCode),
		Phone:           nullString(addr.Phone),
		Metadata:        meta,
	})
	if err != nil {
		return models.CartAddress{}, classify(err, codeQueryFailed, "the cart address could not be written")
	}
	return toCartAddress(row)
}

// ListCartAddresses returns the cart's addresses (in type order).
func (r *Repository) ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error) {
	rows, err := r.queries(ctx).ListCartAddresses(ctx, cartID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart addresses could not be listed")
	}

	out := make([]models.CartAddress, 0, len(rows))
	for i := range rows {
		addr, convErr := toCartAddress(rows[i])
		if convErr != nil {
			return nil, convErr
		}
		out = append(out, addr)
	}
	return out, nil
}

// SoftDeleteCartAddressesByCart soft-deletes all of the cart's addresses.
func (r *Repository) SoftDeleteCartAddressesByCart(ctx context.Context, cartID string) error {
	if err := r.queries(ctx).SoftDeleteCartAddressesByCart(ctx, cartID); err != nil {
		return classify(err, codeQueryFailed, "the cart addresses could not be deleted")
	}
	return nil
}
