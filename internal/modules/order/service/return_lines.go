package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReturnLine is one line of a return, joined to what the order line says.
//
// The variant is what makes it useful: the return record points at an ORDER
// LINE, and putting stock back needs the product variant that line was sold
// for. The join is done here rather than by the caller because both halves are
// this module's own data.
type ReturnLine struct {
	// OrderLineItemID is the order line coming back.
	OrderLineItemID string
	// VariantID is the variant that line was sold for.
	VariantID string
	// Quantity is how many units are coming back.
	Quantity int64
}

// ReturnDetail is a return with everything a flow needs to act on it.
type ReturnDetail struct {
	// ReturnID and OrderID locate the return.
	ReturnID string
	OrderID  string
	// Status is the return's current status.
	Status string
	// ReceivedLocationID is where the goods arrived; empty until they do.
	ReceivedLocationID string
	// Lines are the lines coming back, with their variants.
	Lines []ReturnLine
}

// ReturnDetailJSON returns a return with its lines and their variants.
//
// # Why the surface is JSON
//
// The consumer is a flow that cannot import this module (ADR 0006), so the
// answer crosses as a document rather than as this package's types. The schema
// is [ReturnDetail]'s field tags.
//
// # Why the variant is joined HERE
//
// A return line points at an order line; restocking needs the VARIANT. Both
// are this module's data, so joining them elsewhere would mean a caller reading
// two of this module's surfaces and pairing them itself — and getting the
// pairing wrong would restock the wrong product.
func (s *Service) ReturnDetailJSON(ctx context.Context, returnID string) (json.RawMessage, error) {
	if err := requireID("return_id", returnID); err != nil {
		return nil, err
	}

	ret, err := s.store.GetReturn(ctx, returnID)
	if err != nil {
		return nil, err
	}

	items, err := s.store.ListReturnItems(ctx, returnID)
	if err != nil {
		return nil, err
	}

	lines, err := s.store.ListLineItems(ctx, ret.OrderID)
	if err != nil {
		return nil, err
	}

	variantOf := make(map[string]string, len(lines))
	for i := range lines {
		variantOf[lines[i].ID] = lines[i].VariantID
	}

	detail := returnDetailJSON{
		ReturnID:           ret.ID,
		OrderID:            ret.OrderID,
		Status:             ret.Status.String(),
		ReceivedLocationID: ret.ReceivedLocationID,
		Lines:              make([]returnLineJSON, 0, len(items)),
	}
	for i := range items {
		variantID, known := variantOf[items[i].OrderLineItemID]
		if !known {
			// The line was soft deleted after the return was opened. Reporting
			// it without a variant would let a caller restock nothing and
			// believe it restocked something, so it is an error rather than a
			// gap in the list.
			return nil, errors.Internal(CodeInconsistentState,
				"return %s names line %s, which is not on order %s",
				returnID, items[i].OrderLineItemID, ret.OrderID)
		}
		detail.Lines = append(detail.Lines, returnLineJSON{
			OrderLineItemID: items[i].OrderLineItemID,
			VariantID:       variantID,
			Quantity:        items[i].Quantity,
		})
	}

	return json.Marshal(detail)
}

// returnDetailJSON is the wire form of [ReturnDetail].
type returnDetailJSON struct {
	ReturnID           string           `json:"return_id"`
	OrderID            string           `json:"order_id"`
	Status             string           `json:"status"`
	ReceivedLocationID string           `json:"received_location_id"`
	Lines              []returnLineJSON `json:"lines"`
}

// returnLineJSON is the wire form of [ReturnLine].
type returnLineJSON struct {
	OrderLineItemID string `json:"order_line_item_id"`
	VariantID       string `json:"variant_id"`
	Quantity        int64  `json:"quantity"`
}

// ReturnStatusOf reports a return's current status.
//
// It exists next to [Service.ReturnDetailJSON] because a caller that only has
// to know whether the goods already arrived should not have to read and decode
// every line to find out.
func (s *Service) ReturnStatusOf(ctx context.Context, returnID string) (models.ReturnStatus, error) {
	ret, err := s.store.GetReturn(ctx, returnID)
	if err != nil {
		return "", err
	}

	return ret.Status, nil
}
