//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrdersAfterSalesAreReadThroughTheReadLayer is ADR 0270 over the real
// schema: the module registers the four entities, and the read layer answers
// one order's return with its lines and where it arrived, its claim, its
// exchange and the replacement the claim promised, newest first, and nothing
// of another order's.
func TestAnOrdersAfterSalesAreReadThroughTheReadLayer(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(shutdownCtx)
	})
	reads := query.New(link.New(testPool, nil), c, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide("core.query", reads))
	require.NoError(t, order.New().Register(ctx, c))
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID
	first, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: placed.ID, RefundAmount: 100, Reason: "too small",
		Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1, RefundAmount: 100}},
	})
	require.NoError(t, err)
	_, err = svc.ReceiveReturn(ctx, first.ID, "sloc_returns")
	require.NoError(t, err)
	second, err := svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: placed.ID})
	require.NoError(t, err)
	claim, err := svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: placed.ID, Type: models.ClaimReplace})
	require.NoError(t, err)
	replacement, err := svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ClaimID: claim.ID, ShippingOptionID: "so_standard", LocationID: "sloc_main",
		Lines: []service.ReplacementLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: placed.ID, DifferenceDue: 250})
	require.NoError(t, err)
	sent, err := svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ExchangeID: exchange.ID, ShippingOptionID: "so_standard", LocationID: "sloc_main",
		Lines: []service.ReplacementLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)

	other, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: other.ID})
	require.NoError(t, err)

	read := func(entity string, fields ...string) []query.Record {
		records, err := reads.Graph(ctx, query.GraphSpec{
			Entity: entity, Fields: fields, Filters: map[string]any{service.FieldAfterSalesOrderID: placed.ID},
		})
		require.NoError(t, err, entity)

		return records
	}

	returns := read(service.ReturnEntity, service.FieldID, service.FieldAfterSalesStatus,
		service.FieldReturnReceivedAt, service.FieldReturnReceivedLocationID, service.FieldAfterSalesItems)
	require.Len(t, returns, 2, "the other order's return is not this order's")
	assert.Equal(t, []any{second.ID, first.ID}, []any{returns[0][service.FieldID], returns[1][service.FieldID]},
		"newest first")
	assert.Equal(t, models.ReturnReceived.String(), returns[1][service.FieldAfterSalesStatus])
	assert.NotNil(t, returns[1][service.FieldReturnReceivedAt])
	assert.Equal(t, "sloc_returns", returns[1][service.FieldReturnReceivedLocationID])
	assert.Equal(t, []map[string]any{{"line_item_id": lineID, "quantity": int64(1), "refund_amount": int64(100)}},
		returns[1][service.FieldAfterSalesItems])
	assert.Equal(t, []map[string]any{}, returns[0][service.FieldAfterSalesItems])

	claims := read(service.ClaimEntity, service.FieldID, service.FieldClaimType)
	assert.Equal(t, []query.Record{{service.FieldID: claim.ID, service.FieldClaimType: "replace"}}, claims)

	exchanges := read(service.ExchangeEntity, service.FieldID, service.FieldExchangeDifferenceDue)
	assert.Equal(t, []query.Record{{service.FieldID: exchange.ID, service.FieldExchangeDifferenceDue: int64(250)}},
		exchanges)

	replacements := read(service.ReplacementEntity, service.FieldID, service.FieldReplacementClaimID,
		service.FieldReplacementExchangeID, service.FieldReplacementLocationID, service.FieldAfterSalesItems)
	require.Len(t, replacements, 2, "one through the claim, one through the exchange")
	assert.Equal(t, sent.ID, replacements[0][service.FieldID], "newest first")
	assert.Equal(t, exchange.ID, replacements[0][service.FieldReplacementExchangeID])
	assert.Equal(t, replacement.ID, replacements[1][service.FieldID])
	assert.Equal(t, claim.ID, replacements[1][service.FieldReplacementClaimID])
	assert.Equal(t, "sloc_main", replacements[1][service.FieldReplacementLocationID])
	assert.Equal(t, []map[string]any{{"line_item_id": lineID, "variant_id": "", "quantity": int64(1)}},
		replacements[1][service.FieldAfterSalesItems])

	// Each entity's batch read answers its own record and leaves out an
	// identifier with none.
	for entity, id := range map[string]string{
		service.ReturnEntity: first.ID, service.ClaimEntity: claim.ID,
		service.ExchangeEntity: exchange.ID, service.ReplacementEntity: replacement.ID,
	} {
		byID, err := reads.Graph(ctx, query.GraphSpec{
			Entity: entity, Fields: []string{service.FieldID},
			Filters: map[string]any{service.FieldID: []string{id, "missing"}},
		})
		require.NoError(t, err, entity)
		assert.Equal(t, []query.Record{{service.FieldID: id}}, byID, entity)
	}
}
