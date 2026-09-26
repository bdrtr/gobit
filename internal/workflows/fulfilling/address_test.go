package fulfilling_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// correction is the body the operator sends, as the order module reads it.
var correction = json.RawMessage(`{"address_1":"12 Right St","country_code":"TR"}`)

// TestAParcelOnItsWayStopsTheCorrection refuses while any parcel is pending,
// shipped or delivered — its carrier has the old address — and lets a canceled
// or returned one through (ADR 0195).
func TestAParcelOnItsWayStopsTheCorrection(t *testing.T) {
	t.Parallel()

	for status, allowed := range map[string]bool{
		"pending": false, "shipped": false, "delivered": false,
		"canceled": true, "returned": true,
	} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			links := newFakeLinks()
			links.bound["order_1"] = []string{"ful_1"}
			orders := &fakeOrders{}
			flow := newFlow(t, orders, &fakeFulfillments{status: status, links: links}, links)

			current, err := flow.CorrectShippingAddress(context.Background(), "order_1", correction)

			if allowed {
				require.NoError(t, err)
				assert.Equal(t, 1, orders.corrected)
				assert.JSONEq(t, string(correction), string(orders.correctedWith),
					"the body reaches the order module as the operator sent it")
				assert.JSONEq(t, string(correction), string(current))

				return
			}
			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "%v", err)
			assert.Equal(t, fulfilling.CodeParcelUnderway, coreerrors.CodeOf(err))
			assert.Zero(t, orders.corrected, "the order was written while a parcel was on its way")
		})
	}
}

// TestAnOrderWithNoParcelIsCorrected asks the order module straight away.
func TestAnOrderWithNoParcelIsCorrected(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	orders := &fakeOrders{}
	flow := newFlow(t, orders, &fakeFulfillments{links: links}, links)

	_, err := flow.CorrectShippingAddress(context.Background(), "order_1", correction)
	require.NoError(t, err)
	assert.Equal(t, 1, orders.corrected)
}

// TestAnUnreadableParcelStopsTheCorrection fails closed: a status nobody could
// read may be a parcel on its way.
func TestAnUnreadableParcelStopsTheCorrection(t *testing.T) {
	t.Parallel()

	links := newFakeLinks()
	links.bound["order_1"] = []string{"ful_1"}
	orders := &fakeOrders{}
	flow := newFlow(t, orders, &fakeFulfillments{statusErr: errors.New("unreachable"), links: links}, links)

	_, err := flow.CorrectShippingAddress(context.Background(), "order_1", correction)

	require.Error(t, err)
	assert.Zero(t, orders.corrected)
}
