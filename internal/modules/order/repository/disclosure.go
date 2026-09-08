package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// This file is the database half of the disclosure answer (ADR 0034). The rule
// it serves — which columns of what it reads may be shown, and how they are
// said — is in service/disclosure.go; the statements are in
// queries/disclosure.sql, with their arguments.
//
// # Why it is a separate file from erasure.go and not five more methods there
//
// The two halves resolve the same person and then part company completely.
// erasure.go's reads take FOR UPDATE and may only be called inside a
// transaction, because a status read that a write is about to depend on has to
// be locked; nothing here locks anything, nothing here writes, and every method
// below is safe to call on the bare pool. Putting the two next to each other
// would put a `requireTx` guard beside a method that must NOT need one, and the
// first person to copy the neighboring method would inherit a lock the read
// has no use for.
//
// Nothing here decides anything either. The rows come up whole, the service
// picks the DECLARED columns out of them, and which ones those are is derived
// from the module's own declaration rather than from this file — see
// service.PersonalDataHoldings.
//
// The four child reads take an ARRAY of order ids for the reason
// OrderAddressesByOrderIDs does: a person with two hundred orders is two hundred
// round trips otherwise, and the person with the most orders is the one whose
// dossier costs the most to build.

// OrdersForDisclosure returns every order that carries the person's customer id
// or e-mail address.
//
// It takes NO lock and opens no transaction of its own, which is the difference
// from [Repository.OrdersForErasure] that matters most: this read answers a
// question a person asked and must not make the shop's checkout wait behind it.
// A caller that needs the six reads of a dossier to agree with each other wraps
// them in [Repository.WithReadTx], which gives one snapshot and still takes
// nothing.
//
// An empty identifier means "do not match on this one" and reaches the query as
// SQL NULL — the shape [nullString] gives every optional filter in this package.
// Both empty matches nothing, and the service refuses that case before it gets
// here: disclosing "everyone" is not a subject-access request.
func (r *Repository) OrdersForDisclosure(
	ctx context.Context, customerID, email string,
) ([]models.Order, error) {
	rows, err := r.queries(ctx).ListOrdersForDisclosure(ctx, orderdb.ListOrdersForDisclosureParams{
		CustomerID: nullString(customerID),
		Email:      nullString(email),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the person's orders for disclosure")
	}

	return toOrders(rows)
}

// LineItemsForDisclosure reads the lines of the given orders.
//
// The line's only declared holding is its metadata, which is where a shop
// records the engraving or the gift message the customer typed; the title and
// the price describe the sale and are not disclosed.
func (r *Repository) LineItemsForDisclosure(
	ctx context.Context, orderIDs []string,
) ([]models.OrderLineItem, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListOrderLineItemsForDisclosure(ctx, orderIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the order lines for disclosure")
	}

	return toLineItems(rows)
}

// ReturnsForDisclosure reads the return records of the given orders.
func (r *Repository) ReturnsForDisclosure(
	ctx context.Context, orderIDs []string,
) ([]models.Return, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListOrderReturnsForDisclosure(ctx, orderIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the returns for disclosure")
	}

	return toReturns(rows)
}

// ExchangesForDisclosure reads the exchange records of the given orders.
func (r *Repository) ExchangesForDisclosure(
	ctx context.Context, orderIDs []string,
) ([]models.Exchange, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListOrderExchangesForDisclosure(ctx, orderIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the exchanges for disclosure")
	}

	return toExchanges(rows)
}

// ClaimsForDisclosure reads the damage and shortage records of the given
// orders.
func (r *Repository) ClaimsForDisclosure(
	ctx context.Context, orderIDs []string,
) ([]models.Claim, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}

	rows, err := r.queries(ctx).ListOrderClaimsForDisclosure(ctx, orderIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the claims for disclosure")
	}

	return toClaims(rows)
}
