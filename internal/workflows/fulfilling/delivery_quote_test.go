package fulfilling_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// TestTheQuoteListsWhatAChangeAccepts is ADR 0388: the quote is asked on the
// order's own facts with the admin-only options, lists neither a return
// option nor one priced in another currency, and every option it lists is
// one a change takes at the price listed.
func TestTheQuoteListsWhatAChangeAccepts(t *testing.T) {
	t.Parallel()

	returning := quoted("sopt_back", "Back", 0, "TRY")
	returning["is_return"] = true
	orders := &fakeOrders{facts: orderFacts}
	ful := &fakeFulfillments{options: []map[string]any{
		quoted("sopt_standard", "Standard", 2500, "TRY"),
		returning,
		quoted("sopt_abroad", "Abroad", 100, "EUR"),
		quoted("sopt_pickup", "Pickup", 900, "TRY"),
	}}
	flow := newFlow(t, orders, ful, newFakeLinks())

	raw, err := fulfilling.NewInterop(flow).DeliveryQuoteJSON(context.Background(), "order_1")
	require.NoError(t, err)
	assert.JSONEq(t, `[{"id":"sopt_standard","name":"Standard","amount":2500},
		{"id":"sopt_pickup","name":"Pickup","amount":900}]`, string(raw))
	assert.JSONEq(t, `{"region_id":"reg_tr","currency_code":"TRY","country_code":"TR",
		"subtotal":45000,"item_count":3,"total_weight":0,"include_admin_only":true}`,
		string(ful.quotedWith), "the quote is asked on the order's facts, for the operator")

	var listed []fulfilling.DeliveryQuote
	require.NoError(t, json.Unmarshal(raw, &listed))
	for _, option := range listed {
		amount := option.Amount
		_, err := flow.ChangeDelivery(context.Background(), "order_1", "oship_1", option.ID, "", &amount)
		require.NoError(t, err, "%s is listed, so a change takes it at its price", option.ID)
	}
	assert.Equal(t, len(listed), orders.changed)
}

// TestAQuoteThatMovedRefusesTheChange is ADR 0388: a change naming the price
// it was shown is refused when the option is quoted at another, and writes
// nothing; one naming none takes the quote, as the API's does.
func TestAQuoteThatMovedRefusesTheChange(t *testing.T) {
	t.Parallel()

	orders := &fakeOrders{facts: orderFacts}
	ful := &fakeFulfillments{options: []map[string]any{quoted("sopt_pickup", "Pickup", 900, "TRY")}}
	flow := newFlow(t, orders, ful, newFakeLinks())

	for _, shown := range []int64{1000, 800} {
		_, err := flow.ChangeDelivery(context.Background(), "order_1", "oship_1", "sopt_pickup", "", &shown)
		require.Error(t, err)
		assert.True(t, coreerrors.IsConflict(err), "%v", err)
		assert.Equal(t, fulfilling.CodeQuoteMoved, coreerrors.CodeOf(err), "%v", err)
		assert.Contains(t, err.Error(), "Pickup costs 900 now")
	}
	assert.Zero(t, orders.changed, "a moved quote writes nothing")

	shown := int64(1000)
	_, err := fulfilling.NewInterop(flow).ChangeDelivery(context.Background(), "order_1", "oship_1", "sopt_pickup", "", &shown)
	assert.Equal(t, fulfilling.CodeQuoteMoved, coreerrors.CodeOf(err), "the surface hands the price on: %v", err)

	_, err = flow.ChangeDelivery(context.Background(), "order_1", "oship_1", "sopt_pickup", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, orders.changed)
	assert.JSONEq(t, `{"shipping_method_id":"oship_1","shipping_option_id":"sopt_pickup",
		"name":"Pickup","amount":900,"payment_collection_id":"","paid":0}`, string(orders.changedWith))
}

// TestAFailedQuoteListsNothing keeps the fulfillment module's fault its own,
// and refuses an order that names nothing before asking anyone.
func TestAFailedQuoteListsNothing(t *testing.T) {
	t.Parallel()

	ful := &fakeFulfillments{quoteErr: coreerrors.Unavailable("x", "the carrier is down")}
	flow := newFlow(t, &fakeOrders{facts: orderFacts}, ful, newFakeLinks())

	_, err := flow.QuoteDelivery(context.Background(), "order_1")
	require.Error(t, err)
	assert.Equal(t, fulfilling.CodeQuoteFailed, coreerrors.CodeOf(err))

	_, err = flow.QuoteDelivery(context.Background(), " ")
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
}
