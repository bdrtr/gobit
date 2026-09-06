package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/cart/repository/cartdb"
)

// This file is the database half of the erasure answer (ADR 0029). The rule it
// serves — why the cart is anonymized rather than deleted, and what the answer
// says it kept — is in service/erasure.go; the statements are in
// queries/erasure.sql, with their arguments.
//
// Nothing here decides anything. The read carries the ids up, the two writes
// carry the decision down, and both writes take an ARRAY so that a shopper with
// a year of carts costs one round trip rather than one per cart — the same
// reason CartsByIDs exists.
//
// The transaction lives here and not in the service, and that is not only
// TestLayerPurity's rule about a service package importing pgx: the three calls
// have to be one atomic unit, because the ids the read locks are the ids the
// two writes work from, and a cart that changed in between would be anonymized
// on the strength of a state that no longer held.

// CartsForErasure returns the identifiers of the person's carts and LOCKS them.
//
// It may only be called inside [Repository.WithTx]. The query takes FOR UPDATE
// on the cart rows, and a FOR UPDATE lock outside a transaction is released the
// instant it is taken: it would silently protect nothing, and what it protects
// here is the gap between reading which carts are the person's and rewriting
// them.
//
// An empty identifier means "do not match on this one" and reaches the query as
// SQL NULL — the shape [nullString] gives every optional filter in this
// package. Both empty matches nothing, and the service refuses that case before
// it gets here.
func (r *Repository) CartsForErasure(ctx context.Context, customerID, email string) ([]string, error) {
	if err := requireTx(ctx, "CartsForErasure"); err != nil {
		return nil, err
	}

	ids, err := r.queries(ctx).ListCartsForErasure(ctx, cartdb.ListCartsForErasureParams{
		CustomerID: nullString(customerID),
		Email:      nullString(email),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the person's carts could not be read for erasure")
	}

	return ids, nil
}

// AnonymizeCartContacts nulls the e-mail of the given carts and returns the
// number of rows written.
//
// The returned number is the rows the statement WROTE, not the rows whose
// values changed: the statement is unconditional over the identifiers, so a
// repeated sweep writes the same rows again and reports the same figure. That
// is deliberate — the figure goes into the report a controller compares with
// the previous one, and a count that shrank on the second run would look like
// data that had gone missing.
func (r *Repository) AnonymizeCartContacts(ctx context.Context, cartIDs []string) (int64, error) {
	if len(cartIDs) == 0 {
		return 0, nil
	}

	written, err := r.queries(ctx).AnonymizeCartContacts(ctx, cartIDs)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the cart contacts could not be anonymized")
	}

	return written, nil
}

// AnonymizeCartAddresses nulls the personal columns of the given carts'
// addresses and returns the number of rows written.
//
// A cart with no address writes no row and that is not an error: a cart that
// was abandoned before the address step has none, and an erasure that insisted
// on finding one would fail on the most ordinary cart there is.
func (r *Repository) AnonymizeCartAddresses(ctx context.Context, cartIDs []string) (int64, error) {
	if len(cartIDs) == 0 {
		return 0, nil
	}

	written, err := r.queries(ctx).AnonymizeCartAddresses(ctx, cartIDs)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the cart addresses could not be anonymized")
	}

	return written, nil
}
