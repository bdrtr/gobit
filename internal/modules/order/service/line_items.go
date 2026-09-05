package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ListLineItemsInput is the criterion set of a listing of lines ACROSS orders.
//
// It is not [ListOrdersInput] with another name: that one lists orders and the
// lines only come with [Service.GetOrder], one order at a time. This one exists
// because the question "which variants sold in this period" cannot be asked of
// the order at all — the variant is on the line, and the line had no listing of
// its own until now (gaps.md B14).
//
// The pointers mean "criterion not given"; the empty string and the zero time
// are legitimate values elsewhere and are not used as sentinels.
type ListLineItemsInput struct {
	// OrderID, when given, returns only that order's lines.
	OrderID *string
	// VariantID, when given, returns only the lines of that product variant.
	VariantID *string
	// PlacedFrom is the INCLUSIVE lower bound of the order's placed_at, and
	// PlacedTo the EXCLUSIVE upper bound.
	//
	// The bound is the ORDER's moment of sale, not the line's created_at; see
	// [models.OrderLineItemFilter] for why the two are different questions.
	PlacedFrom *time.Time
	PlacedTo   *time.Time
	// Page is the pagination window.
	Page Page
}

// ListLineItems lists the lines of orders, filtered and paged.
//
// The rows come back newest sale first (the order's placed_at descending), and
// the line's own identifier breaks a tie so that a page boundary does not move
// between two calls over the same data.
//
// # Why there is no total count
//
// [Service.ListOrders] returns one because its API surface renders a pagination
// envelope. This listing has no such surface: it is read through the Query
// layer, whose List returns records and nothing else. A count would be a second
// query per call that no caller can read.
//
// # Why a reversed date range is an error rather than an empty page
//
// PlacedFrom after PlacedTo selects nothing. Returning an empty list would be
// technically correct and practically a trap: the caller reads "nothing sold in
// this period" from what is almost always two arguments in the wrong order. An
// analytics answer that says zero is indistinguishable from a real zero, so the
// mistake is refused instead of served.
func (s *Service) ListLineItems(
	ctx context.Context, in ListLineItemsInput,
) ([]models.OrderLineItem, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, err
	}
	// The keyset cursor is REFUSED rather than ignored. Page carries one
	// because the order listing pages that way, but this listing orders by a
	// column the line does not hold (the order's placed_at), so the cursor
	// could not name a position in it. Silently dropping it would hand the
	// caller the FIRST page every time, which reads as "the data stopped
	// changing" rather than as an error.
	if !in.Page.After.IsZero() {
		return nil, errors.Invalid(CodeInvalidInput,
			"the line listing does not offer cursor pagination; use limit/offset")
	}

	filter := models.OrderLineItemFilter{Limit: page.Limit, Offset: page.Offset}
	if in.OrderID != nil {
		if err := requireID("order_id", *in.OrderID); err != nil {
			return nil, err
		}
		filter.OrderID = in.OrderID
	}
	if in.VariantID != nil {
		if err := requireID("variant_id", *in.VariantID); err != nil {
			return nil, err
		}
		filter.VariantID = in.VariantID
	}
	if in.PlacedFrom != nil && in.PlacedTo != nil && !in.PlacedFrom.Before(*in.PlacedTo) {
		return nil, errors.Invalid(CodeInvalidInput,
			"the start of the date range has to be before its end: %s is not before %s",
			in.PlacedFrom.UTC().Format(time.RFC3339), in.PlacedTo.UTC().Format(time.RFC3339))
	}
	filter.PlacedFrom = in.PlacedFrom
	filter.PlacedTo = in.PlacedTo

	return s.store.ListLineItemsFiltered(ctx, filter)
}

// ListLineItemsByIDs returns the lines of the given identifiers in a SINGLE
// query. No record is returned for an identifier that is not found; that is not
// an error.
func (s *Service) ListLineItemsByIDs(ctx context.Context, ids []string) ([]models.OrderLineItem, error) {
	if len(ids) == 0 {
		return []models.OrderLineItem{}, nil
	}
	return s.store.LineItemsByIDs(ctx, ids)
}
