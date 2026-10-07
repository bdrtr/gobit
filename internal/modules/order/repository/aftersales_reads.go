package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// The reads the after-sales entities of the read layer make (ADR 0270): the
// records by identifier, the replacements of an order a page at a time, and
// the lines of a page of returns or replacements in one query each.

// ReturnsByIDs returns the given return records, newest first; an identifier
// with no record is left out.
func (r *Repository) ReturnsByIDs(ctx context.Context, ids []string) ([]models.Return, error) {
	rows, err := r.queries(ctx).ListOrderReturnsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the return records")
	}

	return toReturns(rows)
}

// ClaimsByIDs returns the given claim records, newest first; an identifier
// with no record is left out.
func (r *Repository) ClaimsByIDs(ctx context.Context, ids []string) ([]models.Claim, error) {
	rows, err := r.queries(ctx).ListOrderClaimsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the claim records")
	}

	return toClaims(rows)
}

// ExchangesByIDs returns the given exchange records, newest first; an
// identifier with no record is left out.
func (r *Repository) ExchangesByIDs(ctx context.Context, ids []string) ([]models.Exchange, error) {
	rows, err := r.queries(ctx).ListOrderExchangesByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the exchange records")
	}

	return toExchanges(rows)
}

// ReplacementsByIDs returns the given replacements, newest first; an
// identifier with no record is left out.
func (r *Repository) ReplacementsByIDs(ctx context.Context, ids []string) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the replacements")
	}

	return toReplacements(rows), nil
}

// PageReplacementsOfOrder pages the replacements the order's claims and
// exchanges promised, newest first.
func (r *Repository) PageReplacementsOfOrder(
	ctx context.Context, filter models.ChildFilter,
) ([]models.Replacement, error) {
	rows, err := r.queries(ctx).ListOrderReplacementsOfOrder(ctx, orderdb.ListOrderReplacementsOfOrderParams{
		OrderID: filter.OrderID, RowLimit: filter.Limit, RowOffset: filter.Offset,
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"could not list the replacements of order %s", filter.OrderID)
	}

	return toReplacements(rows), nil
}

// ReturnItemsOf returns the lines of the given returns by return, each
// return's in the order they were written.
func (r *Repository) ReturnItemsOf(
	ctx context.Context, returnIDs []string,
) (map[string][]models.ReturnItem, error) {
	rows, err := r.queries(ctx).ListOrderReturnItemsOfReturns(ctx, returnIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list the return lines")
	}

	out := make(map[string][]models.ReturnItem, len(returnIDs))
	for i := range rows {
		item := toReturnItem(rows[i])
		out[item.ReturnID] = append(out[item.ReturnID], item)
	}

	return out, nil
}

// ReplacementItemsOf returns the lines of the given replacements by
// replacement, each replacement's in the order they were written. The lines'
// parts are not read.
func (r *Repository) ReplacementItemsOf(
	ctx context.Context, replacementIDs []string,
) (map[string][]models.ReplacementItem, error) {
	rows, err := r.queries(ctx).ListOrderReplacementItemsOfReplacements(ctx, replacementIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not list the replacement lines")
	}

	out := make(map[string][]models.ReplacementItem, len(replacementIDs))
	for i := range rows {
		item, err := toReplacementItem(rows[i])
		if err != nil {
			return nil, err
		}
		out[item.ReplacementID] = append(out[item.ReplacementID], item)
	}

	return out, nil
}
