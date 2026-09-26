package fulfilling_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// orderFacts is what the order module answers for a delivery quote, as a
// consumer writes it.
var orderFacts = map[string]any{
	"region_id": "reg_tr", "currency_code": "TRY", "country_code": "TR",
	"subtotal": 45000, "item_count": 3,
}

// quoted is an option as the fulfillment module prices it.
func quoted(id, name string, amount int64, currency string) map[string]any {
	return map[string]any{"id": id, "name": name, "amount": amount, "currency_code": currency}
}

// TestADeliveryIsChangedAtTheQuotedPrice prices the option on the order's own
// facts, admin-only options included, and hands the order module the quote
// rather than anything the caller said (ADR 0199).
func TestADeliveryIsChangedAtTheQuotedPrice(t *testing.T) {
	t.Parallel()

	orders := &fakeOrders{facts: orderFacts}
	ful := &fakeFulfillments{options: []map[string]any{
		quoted("sopt_standard", "Standard", 2500, "TRY"),
		quoted("sopt_pickup", "Pickup", 900, "TRY"),
	}}
	flow := newFlow(t, orders, ful, newFakeLinks())

	_, err := flow.ChangeDelivery(context.Background(), "order_1", "oship_1", " sopt_pickup ")
	require.NoError(t, err)

	assert.JSONEq(t, `{"region_id":"reg_tr","currency_code":"TRY","country_code":"TR",
		"subtotal":45000,"item_count":3,"total_weight":0,"include_admin_only":true}`,
		string(ful.quotedWith), "the quote is asked on the order's facts, for the operator")
	require.Equal(t, 1, orders.changed)
	assert.JSONEq(t, `{"shipping_method_id":"oship_1","shipping_option_id":"sopt_pickup",
		"name":"Pickup","amount":900}`, string(orders.changedWith))
}

// TestAnOptionTheOrderCannotGoOnIsRefused refuses an option the listing did not
// quote for the order, a return option and one priced in another currency.
func TestAnOptionTheOrderCannotGoOnIsRefused(t *testing.T) {
	t.Parallel()

	returning := quoted("sopt_back", "Back", 0, "TRY")
	returning["is_return"] = true
	for name, options := range map[string][]map[string]any{
		"not listed":     {quoted("sopt_standard", "Standard", 2500, "TRY")},
		"a return":       {returning},
		"other currency": {quoted("sopt_back", "Abroad", 100, "EUR")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			orders := &fakeOrders{facts: orderFacts}
			flow := newFlow(t, orders, &fakeFulfillments{options: options}, newFakeLinks())

			_, err := flow.ChangeDelivery(context.Background(), "order_1", "", "sopt_back")

			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "%v", err)
			assert.Equal(t, fulfilling.CodeOptionUnavailable, coreerrors.CodeOf(err))
			assert.Zero(t, orders.changed)
		})
	}
}

// TestAParcelOnItsWayStopsTheDeliveryChange refuses while a parcel was handed
// to its carrier on the old service, and lets a canceled or returned one
// through.
func TestAParcelOnItsWayStopsTheDeliveryChange(t *testing.T) {
	t.Parallel()

	for status, allowed := range map[string]bool{
		"pending": false, "shipped": false, "delivered": false,
		"canceled": true, "returned": true,
	} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			links := newFakeLinks()
			links.bound["order_1"] = []string{"ful_1"}
			orders := &fakeOrders{facts: orderFacts}
			ful := &fakeFulfillments{status: status, links: links,
				options: []map[string]any{quoted("sopt_pickup", "Pickup", 0, "TRY")}}
			flow := newFlow(t, orders, ful, links)

			_, err := flow.ChangeDelivery(context.Background(), "order_1", "", "sopt_pickup")

			if allowed {
				require.NoError(t, err)
				assert.Equal(t, 1, orders.changed)

				return
			}
			require.Error(t, err)
			assert.Equal(t, fulfilling.CodeParcelUnderway, coreerrors.CodeOf(err))
			assert.Zero(t, orders.changed)
			assert.Nil(t, ful.quotedWith, "a refused change was still quoted")
		})
	}
}

// TestAFailedQuoteChangesNothing keeps the fulfillment module's fault its own.
func TestAFailedQuoteChangesNothing(t *testing.T) {
	t.Parallel()

	orders := &fakeOrders{facts: orderFacts}
	ful := &fakeFulfillments{quoteErr: coreerrors.Unavailable("x", "the carrier is down")}
	flow := newFlow(t, orders, ful, newFakeLinks())

	_, err := flow.ChangeDelivery(context.Background(), "order_1", "", "sopt_pickup")

	require.Error(t, err)
	assert.Equal(t, fulfilling.CodeQuoteFailed, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "%v", err)
	assert.Zero(t, orders.changed)
}

// TestADeliveryChangeNeedsAnOrderAndAnOption refuses before asking anyone.
func TestADeliveryChangeNeedsAnOrderAndAnOption(t *testing.T) {
	t.Parallel()

	orders := &fakeOrders{facts: orderFacts, err: errors.New("asked")}
	ful := &fakeFulfillments{}
	flow := newFlow(t, orders, ful, newFakeLinks())

	for _, ids := range [][2]string{{"", "sopt_pickup"}, {"order_1", " "}} {
		_, err := flow.ChangeDelivery(context.Background(), ids[0], "", ids[1])
		require.Error(t, err)
		assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	}
	assert.Nil(t, ful.quotedWith)
	assert.Zero(t, orders.changed)
}
