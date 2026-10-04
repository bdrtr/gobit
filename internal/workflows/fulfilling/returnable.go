package fulfilling

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// returnDetail is the part of the order module's return read this flow uses.
//
// It is repeated here rather than imported (ADR 0006); the producing side
// documents it on order/service.Service.ReturnDetailJSON.
type returnDetail struct {
	OrderID string `json:"order_id"`
	// AwaitsGoods is the order module's own answer to "may goods still arrive for
	// this return", so this flow compares no status word (the D59 class).
	AwaitsGoods bool               `json:"awaits_goods"`
	Lines       []returnDetailLine `json:"lines"`
}

// returnDetailLine is one line a return brings back.
type returnDetailLine struct {
	OrderLineItemID string `json:"order_line_item_id"`
	Quantity        int64  `json:"quantity"`
}

// ReturnLines answers whether order orderID's return returnID still awaits its
// goods and, per order line it names, how many units it brings back (ADR 0384).
//
// A return of another order awaits nothing on this one: the parcel names the order
// in its reference, and a parcel for one order bringing back another order's
// return would be bounded by goods it never sold. A return the order module could
// not read keeps the kind it was reported with, so an unknown one stays a
// not-found rather than reading as "awaits nothing".
func (w *Workflows) ReturnLines(
	ctx context.Context, orderID, returnID string,
) (awaited bool, lines map[string]int64, err error) {
	if w.orders == nil {
		return false, nil, errors.Internal(CodeDispatchableUnknown,
			"the fulfilling flow is not wired, so what a return brings back cannot be read")
	}

	raw, err := w.orders.ReturnDetailJSON(ctx, returnID)
	if err != nil {
		return false, nil, errors.Wrap(err, errors.KindOf(err), CodeDispatchableUnknown,
			"return %s could not be read", returnID)
	}

	var detail returnDetail
	if err = json.Unmarshal(raw, &detail); err != nil {
		return false, nil, errors.Internal(CodeDispatchableUnknown,
			"return %s could not be decoded: %v", returnID, err)
	}

	if detail.OrderID != orderID || !detail.AwaitsGoods {
		return false, nil, nil
	}

	lines = make(map[string]int64, len(detail.Lines))
	for _, line := range detail.Lines {
		lines[line.OrderLineItemID] += line.Quantity
	}

	return true, lines, nil
}
