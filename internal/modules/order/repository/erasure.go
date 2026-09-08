package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository/orderdb"
)

// This file is the database half of the erasure answer (ADR 0029). The rule it
// serves — which order can be forgotten and which cannot — is in
// models/erasure.go and service/erasure.go; the statements are in
// queries/erasure.sql, with their arguments.
//
// Nothing here decides anything. The candidate read carries the facts up, the
// two writes carry the decision down, and both writes take an ARRAY so that a
// person with many orders costs one round trip rather than one per order — the
// same reason OrdersByIDs and OrderAddressesByOrderIDs exist.

// OrdersForErasure returns the person's orders with the facts that decide
// whether each one is settled, and LOCKS them.
//
// It may only be called inside [Repository.WithTx]. The query takes FOR UPDATE
// on the order rows, and a FOR UPDATE lock outside a transaction is released
// the instant it is taken: it would silently protect nothing, and what it is
// protecting here is the gap between reading a status and erasing on the
// strength of it.
//
// An empty identifier means "do not match on this one" and reaches the query as
// SQL NULL — the shape [nullString] gives every optional filter in this
// package. Both empty matches nothing, and the service refuses that case before
// it gets here.
func (r *Repository) OrdersForErasure(
	ctx context.Context, customerID, email string,
) ([]models.OrderErasureCandidate, error) {
	if err := requireTx(ctx, "OrdersForErasure"); err != nil {
		return nil, err
	}

	rows, err := r.queries(ctx).ListOrdersForErasure(ctx, orderdb.ListOrdersForErasureParams{
		CustomerID: nullString(customerID),
		Email:      nullString(email),
	})
	if err != nil {
		return nil, classify(err, codeQueryFailed, "could not read the person's orders for erasure")
	}

	out := make([]models.OrderErasureCandidate, 0, len(rows))
	// By index: the row struct carries eleven fields and copying it per turn
	// would move them for nothing.
	for i := range rows {
		out = append(out, models.OrderErasureCandidate{
			OrderID:           rows[i].ID,
			DisplayID:         rows[i].DisplayID,
			Status:            models.OrderStatus(rows[i].Status),
			CurrencyCode:      rows[i].CurrencyCode,
			ErasedAt:          toTimePtr(rows[i].PersonalDataErasedAt),
			Total:             rows[i].Total,
			PaidTotal:         rows[i].PaidTotal,
			RefundedTotal:     rows[i].RefundedTotal,
			ReturnRequested:   rows[i].ReturnRequested,
			ExchangeRequested: rows[i].ExchangeRequested,
			ClaimRequested:    rows[i].ClaimRequested,
		})
	}

	return out, nil
}

// AnonymizeOrderContacts nulls the e-mail of the given orders and stamps them.
//
// The returned number is the rows the statement WROTE, not the rows whose
// values changed: the statement is unconditional over the identifiers, so a
// repeated sweep writes the same rows again and reports the same figure. That
// is deliberate — the figure goes into the report a controller compares with
// the previous one, and a count that shrank on the second run would look like
// data that had gone missing.
func (r *Repository) AnonymizeOrderContacts(ctx context.Context, orderIDs []string) (int64, error) {
	if len(orderIDs) == 0 {
		return 0, nil
	}

	written, err := r.queries(ctx).AnonymizeOrderContacts(ctx, orderIDs)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not anonymize the order contacts")
	}

	return written, nil
}

// AnonymizeOrderAddresses nulls the personal columns of the given orders'
// addresses and returns the number of rows written.
//
// An order with no address writes no row and that is not an error: a shop
// selling a download records none, and an erasure that insisted on finding one
// would fail on a perfectly ordinary sale.
func (r *Repository) AnonymizeOrderAddresses(ctx context.Context, orderIDs []string) (int64, error) {
	if len(orderIDs) == 0 {
		return 0, nil
	}

	written, err := r.queries(ctx).AnonymizeOrderAddresses(ctx, orderIDs)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "could not anonymize the order addresses")
	}

	return written, nil
}
