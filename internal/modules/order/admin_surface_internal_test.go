package order

import (
	"context"
	"encoding/json"
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

// TestThePanelOpensAParcelThroughTheFlowTheAPICalls is ADR 0324: the surface
// hands the fulfilling flow the order and the panel's key, and nothing else,
// so the flow ships on the delivery the order was sold; it reports a key that
// had already opened the parcel; a surface without the flow is unavailable.
func TestThePanelOpensAParcelThroughTheFlowTheAPICalls(t *testing.T) {
	t.Parallel()

	flow := &recordingFulfilling{}
	surface := &AfterSalesSurface{fulfilling: flow}

	parcel, already, err := surface.OpenParcel(context.Background(), "order_1", "panel-k")
	require.NoError(t, err)
	assert.Equal(t, "ful_1", parcel)
	assert.False(t, already)
	assert.Equal(t, "order_1", flow.orderID)
	assert.JSONEq(t, `{"idempotency_key":"panel-k"}`, string(flow.request),
		"the key alone; no option, so the flow takes the one the order was sold")

	flow.already = true
	_, already, err = surface.OpenParcel(context.Background(), "order_1", "panel-k")
	require.NoError(t, err)
	assert.True(t, already, "a key that had already opened the parcel says so")

	_, _, err = (&AfterSalesSurface{}).OpenParcel(context.Background(), "order_1", "panel-k")
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
