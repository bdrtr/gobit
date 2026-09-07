package repository

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the database half of the DISCLOSURE answer (ADR 0029, ADR 0033):
// what this module holds about a person, read back for that person. The rule it
// serves — which columns may appear, what the answer says when the bound is
// hit, and why the two handles are resolved together — is in
// service/disclosure.go; the statements and the argument for each SELECT list
// are in queries/disclosure.sql.
//
// # Why there is no transaction here, when the erasure has one
//
// The erasure's three calls MUST be one unit: it locks the carts it is about to
// rewrite, and the first write destroys the handle the second would have used
// to find its rows. Nothing in this file writes and nothing here depends on a
// row still being what it was — the three child reads work from cart
// identifiers the first read returned, so a cart that changed in between is
// reported as it stands rather than making a later statement wrong.
//
// What the absence costs is that a dossier is a set of statements taken close
// together rather than one snapshot: a row rewritten by a concurrent erasure
// can appear in its older or its newer form, and a cart opened while the read
// runs is not in it. What it buys is that an admin read over an unbounded
// number of rows never holds a snapshot open on the pool, which under
// REPEATABLE READ would hold back vacuum for as long as the request takes. The
// one figure that would have been genuinely wrong across two snapshots — how
// many carts matched, against how many were listed — is not read separately:
// the counting happens inside the same statement as the listing
// (count(*) OVER ()), so the truncation notice cannot disagree with the rows it
// describes.

// CartsForDisclosure returns the person's carts, newest first, at most limit of
// them, together with HOW MANY matched in total.
//
// The second return value is the whole matching count, not the length of the
// slice, and it is the number a truncated answer has to be able to state. A
// caller that ignores it will produce a dossier that is silently short, which is
// worse than a large one: the person cannot tell an omission from an absence.
//
// An empty identifier means "do not match on this one" and reaches the query as
// SQL NULL, exactly as it does in the erasure. Both empty matches nothing, and
// the service refuses that case before it gets here.
//
// A non-positive limit is refused rather than passed on. PostgreSQL would take
// LIMIT 0 and return no rows next to a non-zero total, and the answer built
// from that pair would say the person has nothing while the count in the same
// row said otherwise — a bug that reads as a clean "we hold nothing about you".
func (r *Repository) CartsForDisclosure(
	ctx context.Context, customerID, email string, limit int64,
) ([]models.PersonalCart, int64, error) {
	if limit <= 0 {
		return nil, 0, errors.Internal(codeDisclosureBound,
			"a disclosure was asked for with a bound of %d carts; a non-positive bound would "+
				"answer that this person has no cart while the database still holds them", limit)
	}

	rows, err := r.queries(ctx).ListCartsForDisclosure(ctx, cartdb.ListCartsForDisclosureParams{
		CustomerID: nullString(customerID),
		Email:      nullString(email),
		MaxCarts:   limit,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the person's carts could not be read for disclosure")
	}

	out := make([]models.PersonalCart, 0, len(rows))
	for i := range rows {
		metadata, err := toJSONMap(rows[i].Metadata)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, models.PersonalCart{
			ID:         rows[i].ID,
			CustomerID: stringValue(rows[i].CustomerID),
			Email:      stringValue(rows[i].Email),
			Metadata:   metadata,
			CreatedAt:  toTime(rows[i].CreatedAt),
		})
	}

	// Every row carries the same window count; with no rows there is nothing to
	// have been left out, so the total is the length of the empty slice.
	var total int64
	if len(rows) > 0 {
		total = rows[0].TotalCarts
	}

	return out, total, nil
}

// CartAddressesForDisclosure returns every address of the given carts.
//
// It takes the identifiers rather than the subject for the reason the erasure's
// pair takes them: a guest cart whose e-mail has already been erased can no
// longer be found by e-mail, and a second resolution would report the header
// without its address. The soft-deleted rows are included, because the question
// is what the database still holds.
func (r *Repository) CartAddressesForDisclosure(
	ctx context.Context, cartIDs []string,
) ([]models.PersonalAddress, error) {
	if len(cartIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListCartAddressesForDisclosure(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart addresses could not be read for disclosure")
	}

	out := make([]models.PersonalAddress, 0, len(rows))
	for i := range rows {
		metadata, err := toJSONMap(rows[i].Metadata)
		if err != nil {
			return nil, err
		}
		out = append(out, models.PersonalAddress{
			ID:              rows[i].ID,
			CartID:          rows[i].CartID,
			SourceAddressID: stringValue(rows[i].SourceAddressID),
			FirstName:       stringValue(rows[i].FirstName),
			LastName:        stringValue(rows[i].LastName),
			Company:         stringValue(rows[i].Company),
			Address1:        stringValue(rows[i].Address1),
			Address2:        stringValue(rows[i].Address2),
			City:            stringValue(rows[i].City),
			Province:        stringValue(rows[i].Province),
			PostalCode:      stringValue(rows[i].PostalCode),
			CountryCode:     stringValue(rows[i].CountryCode),
			Phone:           stringValue(rows[i].Phone),
			Metadata:        metadata,
		})
	}

	return out, nil
}

// CartLineItemNotesForDisclosure returns the lines of the given carts that
// carry a note.
//
// A line whose metadata is the empty object never comes back; the statement
// filters it, and the reasoning is in queries/disclosure.sql. What matters at
// this level is that an empty result is not an error and not a sign of a
// missing line: most baskets carry no note at all.
func (r *Repository) CartLineItemNotesForDisclosure(
	ctx context.Context, cartIDs []string,
) ([]models.PersonalNote, error) {
	if len(cartIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListCartLineItemNotesForDisclosure(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart line notes could not be read for disclosure")
	}

	out := make([]models.PersonalNote, 0, len(rows))
	for i := range rows {
		data, err := toJSONMap(rows[i].Metadata)
		if err != nil {
			return nil, err
		}
		out = append(out, models.PersonalNote{ID: rows[i].ID, CartID: rows[i].CartID, Data: data})
	}

	return out, nil
}

// CartShippingNotesForDisclosure returns the shipping methods of the given
// carts that carry provider data.
//
// It is [Repository.CartLineItemNotesForDisclosure] over the other free-form
// column this module declares; the two are separate methods rather than one
// with a table parameter because the tables are different tables and a
// parameter naming one of them in Go would put a schema decision in a caller
// that has no way to check it.
func (r *Repository) CartShippingNotesForDisclosure(
	ctx context.Context, cartIDs []string,
) ([]models.PersonalNote, error) {
	if len(cartIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListCartShippingNotesForDisclosure(ctx, cartIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the cart shipping notes could not be read for disclosure")
	}

	out := make([]models.PersonalNote, 0, len(rows))
	for i := range rows {
		data, err := toJSONMap(rows[i].Data)
		if err != nil {
			return nil, err
		}
		out = append(out, models.PersonalNote{ID: rows[i].ID, CartID: rows[i].CartID, Data: data})
	}

	return out, nil
}
