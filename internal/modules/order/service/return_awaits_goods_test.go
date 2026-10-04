package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAReturnAwaitsGoodsOnlyWhileItCanBeReceived is the order module's answer
// to "may goods still arrive for this return", which bounds a parcel bringing
// it back (ADR 0384). It is answered from the receive rule, so no consumer
// compares a status word.
func TestAReturnAwaitsGoodsOnlyWhileItCanBeReceived(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		after func(t *testing.T, e env, returnID string)
		want  bool
	}{
		{name: "requested", after: func(*testing.T, env, string) {}, want: true},
		{name: "received", after: func(t *testing.T, e env, returnID string) {
			t.Helper()
			_, err := e.svc.ReceiveReturn(context.Background(), returnID, testLocationID)
			require.NoError(t, err)
		}, want: false},
		{name: "canceled", after: func(t *testing.T, e env, returnID string) {
			t.Helper()
			_, err := e.svc.CancelReturn(context.Background(), returnID)
			require.NoError(t, err)
		}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			e := newEnv(t)
			order, lineID := returnedOrder(t, e)
			ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
				OrderID: order.ID,
				Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
			})
			require.NoError(t, err)
			tc.after(t, e, ret.ID)

			raw, err := e.svc.ReturnDetailJSON(ctx, ret.ID)
			require.NoError(t, err)

			var detail struct {
				AwaitsGoods *bool `json:"awaits_goods"`
			}
			require.NoError(t, json.Unmarshal(raw, &detail))
			require.NotNil(t, detail.AwaitsGoods, "the field is on the wire; body: %s", raw)
			assert.Equal(t, tc.want, *detail.AwaitsGoods, "body: %s", raw)
		})
	}
}
