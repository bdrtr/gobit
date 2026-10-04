package order

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/api"
)

// recordingFulfilling opens a parcel as scripted and records what it was
// asked; the rest of the flow is never reached.
type recordingFulfilling struct {
	api.Fulfilling
	orderID string
	request json.RawMessage
	already bool
}

func (f *recordingFulfilling) OpenForOrder(
	_ context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	f.orderID, f.request = orderID, request
	return "ful_1", f.already, nil
}

// changingFulfilling records the delivery changes, corrections and quotes
// the surface hands the flow, each parameter in its own field (ADR 0388).
type changingFulfilling struct {
	api.Fulfilling
	calls []string
}

func (f *changingFulfilling) ChangeDelivery(
	_ context.Context, orderID, shippingMethodID, shippingOptionID, collectionID string, quotedAmount *int64,
) (json.RawMessage, error) {
	quoted := "nil"
	if quotedAmount != nil {
		quoted = fmt.Sprint(*quotedAmount)
	}
	f.calls = append(f.calls, "change order="+orderID+" method="+shippingMethodID+
		" option="+shippingOptionID+" collection="+collectionID+" quoted="+quoted)

	return json.RawMessage(`null`), nil
}

func (f *changingFulfilling) CorrectShippingAddress(
	_ context.Context, orderID string, address json.RawMessage, readAddressID string,
) (json.RawMessage, error) {
	f.calls = append(f.calls, "correct order="+orderID+" address="+string(address)+" read="+readAddressID)

	return address, nil
}

func (f *changingFulfilling) DeliveryQuoteJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	f.calls = append(f.calls, "quote order="+orderID)

	return json.RawMessage(`[]`), nil
}

// TestThePanelChangesAndCorrectsThroughTheFlowTheAPICalls is ADR 0388: the
// surface hands the fulfilling flow the order, the delivery, the option, the
// collection and the price shown, each to its own parameter, and the row an
// address correction was drawn from beside its address; a surface without the
// flow is unavailable.
func TestThePanelChangesAndCorrectsThroughTheFlowTheAPICalls(t *testing.T) {
	t.Parallel()

	flow := &changingFulfilling{}
	surface := &AfterSalesSurface{fulfilling: flow}
	ctx := context.Background()

	_, err := surface.ChangeDelivery(ctx, "o1", "d1", "so1", "pc1", 700)
	require.NoError(t, err)
	require.NoError(t, surface.CorrectShippingAddress(ctx, "o1", json.RawMessage(`{"address_1":"a"}`), "oadr1"))
	_, err = surface.DeliveryQuoteJSON(ctx, "o1")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"change order=o1 method=d1 option=so1 collection=pc1 quoted=700",
		`correct order=o1 address={"address_1":"a"} read=oadr1`,
		"quote order=o1",
	}, flow.calls)

	bare := &AfterSalesSurface{}
	_, err = bare.ChangeDelivery(ctx, "o1", "d1", "so1", "", 700)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "%v", err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(bare.CorrectShippingAddress(ctx, "o1", nil, "oadr1")))
	_, err = bare.DeliveryQuoteJSON(ctx, "o1")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "%v", err)
	_, err = bare.CreditLinesJSON(ctx, "o1")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "%v", err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(bare.CreditOrder(ctx, "o1", 0, 1, "r", "")))
}

// TestThePanelOpensAParcelThroughTheFlowTheAPICalls is ADR 0324: named no
// delivery, the surface hands the fulfilling flow the order and the panel's
// key, and nothing else, so the flow ships on the delivery the order was sold;
// it reports a key that had already opened the parcel; a surface without the
// flow is unavailable. A named delivery is ADR 0332's, on the real schema.
func TestThePanelOpensAParcelThroughTheFlowTheAPICalls(t *testing.T) {
	t.Parallel()

	flow := &recordingFulfilling{}
	surface := &AfterSalesSurface{fulfilling: flow}

	parcel, already, err := surface.OpenParcel(context.Background(), "order_1", "", "panel-k")
	require.NoError(t, err)
	assert.Equal(t, "ful_1", parcel)
	assert.False(t, already)
	assert.Equal(t, "order_1", flow.orderID)
	assert.JSONEq(t, `{"idempotency_key":"panel-k"}`, string(flow.request),
		"the key alone; no option, so the flow takes the one the order was sold")

	flow.already = true
	_, already, err = surface.OpenParcel(context.Background(), "order_1", "", "panel-k")
	require.NoError(t, err)
	assert.True(t, already, "a key that had already opened the parcel says so")

	_, _, err = (&AfterSalesSurface{}).OpenParcel(context.Background(), "order_1", "", "panel-k")
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}

// recordingInvoicing answers the order's document as scripted and records
// each issue request.
type recordingInvoicing struct {
	api.Invoicing
	found    bool
	readErr  error
	requests []string
	already  bool
}

func (f *recordingInvoicing) InvoiceOfOrder(
	_ context.Context, _ string,
) (invoiceID, number, status string, err error) {
	if f.readErr != nil {
		return "", "", "", f.readErr
	}
	if !f.found {
		return "", "", "", errors.NotFound("invoicing_invalid_input", "order order_1 has no invoice")
	}
	return "inv_1", "GBT2026000000007", "issued", nil
}

func (f *recordingInvoicing) IssueForOrder(
	_ context.Context, _ string, request json.RawMessage,
) (invoiceID, number string, alreadyIssued bool, err error) {
	f.requests = append(f.requests, string(request))
	return "inv_1", "GBT2026000000007", f.already, nil
}

// TestThePanelInvoicesAnOrderThroughTheFlowTheAPICalls is ADR 0335: the
// surface names the order's document, and an order with none is told so
// rather than refused; it issues on the series named with the buyer as
// given, an empty one when none is, so the flow takes what is left out from
// the order; a surface without the flow is unavailable.
func TestThePanelInvoicesAnOrderThroughTheFlowTheAPICalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	flow := &recordingInvoicing{}
	surface := &AfterSalesSurface{invoicing: flow}

	_, _, _, found, err := surface.InvoiceOfOrder(ctx, "order_1")
	require.NoError(t, err, "an order with no document is not an error")
	assert.False(t, found)
	flow.found = true
	id, number, status, found, err := surface.InvoiceOfOrder(ctx, "order_1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "inv_1|GBT2026000000007|issued", id+"|"+number+"|"+status)
	flow.readErr = errors.Unavailable("db_down", "no answer")
	_, _, _, _, err = surface.InvoiceOfOrder(ctx, "order_1")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "a failure is not a missing document")

	flow.already = true
	_, number, already, err := surface.IssueInvoice(ctx, "order_1", "GBT",
		json.RawMessage(`{"name":"Ada","tax_number":"1234567890"}`))
	require.NoError(t, err)
	assert.True(t, already)
	assert.Equal(t, "GBT2026000000007", number)
	_, _, _, err = surface.IssueInvoice(ctx, "order_1", "IAD", nil)
	require.NoError(t, err)
	require.Len(t, flow.requests, 2)
	assert.JSONEq(t, `{"series_prefix":"GBT","buyer":{"name":"Ada","tax_number":"1234567890"}}`,
		flow.requests[0], "the buyer as given; the flow takes what is left out from the order")
	assert.JSONEq(t, `{"series_prefix":"IAD","buyer":{}}`, flow.requests[1], "no buyer is an empty one")

	_, _, _, _, err = (&AfterSalesSurface{}).InvoiceOfOrder(ctx, "order_1")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	_, _, _, err = (&AfterSalesSurface{}).IssueInvoice(ctx, "order_1", "GBT", nil)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}

// TestASurfaceWithoutTheServiceCancelsNothing is ADR 0339's guard: a surface
// that was never built is unavailable rather than a nil dereference.
func TestASurfaceWithoutTheServiceCancelsNothing(t *testing.T) {
	t.Parallel()

	err := (&AfterSalesSurface{}).CancelOrder(context.Background(), "order_1", "")
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
