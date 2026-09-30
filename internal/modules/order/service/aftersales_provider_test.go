package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// afterSalesProviders are the four after-sales entities by their container
// names (ADR 0270).
func afterSalesProviders(svc *service.Service) map[string]query.Provider {
	return map[string]query.Provider{
		service.ReturnProviderName:      service.NewReturnQueryProvider(svc),
		service.ClaimProviderName:       service.NewClaimQueryProvider(svc),
		service.ExchangeProviderName:    service.NewExchangeQueryProvider(svc),
		service.ReplacementProviderName: service.NewReplacementQueryProvider(svc),
	}
}

// afterSalesOrder is an order holding one record of each kind, and another
// order holding a return of its own, which a read of the first must not see.
type afterSalesOrder struct {
	order                                   models.Order
	lineID                                  string
	returnID, claimID, exchangeID, replacID string
}

func newAfterSalesOrder(t *testing.T, e env) afterSalesOrder {
	t.Helper()

	ctx := context.Background()
	claim, lineID := claimToReplace(t, e)
	order, err := e.svc.GetOrder(ctx, claim.OrderID)
	require.NoError(t, err)
	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: claim.OrderID, RefundAmount: 1200, Reason: "the size did not fit", Note: "boxed",
		Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2, RefundAmount: 1200}},
	})
	require.NoError(t, err)
	replacement, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: claim.OrderID, DifferenceDue: -500})
	require.NoError(t, err)

	other, otherLine := returnedOrder(t, e)
	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: other.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: otherLine, Quantity: 1}},
	})
	require.NoError(t, err)

	return afterSalesOrder{
		order: order.Order, lineID: lineID,
		returnID: ret.ID, claimID: claim.ID, exchangeID: exchange.ID, replacID: replacement.ID,
	}
}

// readOrder lists one entity's records of the order.
func readOrder(t *testing.T, provider query.Provider, orderID string, fields ...string) []query.Record {
	t.Helper()

	records, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FieldAfterSalesOrderID: orderID}, Fields: fields,
	})
	require.NoError(t, err)

	return records
}

// TestAnOrdersAfterSalesRecordsAreReadPerOrder is ADR 0270: each entity
// answers the order's own records with what the panel prints, and nothing of
// another order's.
func TestAnOrdersAfterSalesRecordsAreReadPerOrder(t *testing.T) {
	e := newEnv(t)
	fixture := newAfterSalesOrder(t, e)
	providers := afterSalesProviders(e.svc)

	returns := readOrder(t, providers[service.ReturnProviderName], fixture.order.ID)
	require.Len(t, returns, 1, "the other order's return is not this order's")
	assert.Equal(t, fixture.returnID, returns[0][service.FieldID])
	assert.Equal(t, fixture.order.ID, returns[0][service.FieldAfterSalesOrderID])
	assert.Equal(t, models.ReturnRequested.String(), returns[0][service.FieldAfterSalesStatus])
	assert.Equal(t, int64(1200), returns[0][service.FieldReturnRefundAmount])
	assert.Equal(t, "the size did not fit", returns[0][service.FieldReturnReason])
	assert.Equal(t, "boxed", returns[0][service.FieldAfterSalesNote])
	assert.Nil(t, returns[0][service.FieldReturnReceivedAt], "not received yet")
	assert.Equal(t, []map[string]any{{"line_item_id": fixture.lineID, "quantity": int64(2), "refund_amount": int64(1200)}},
		returns[0][service.FieldAfterSalesItems])

	claims := readOrder(t, providers[service.ClaimProviderName], fixture.order.ID)
	require.Len(t, claims, 1)
	assert.Equal(t, fixture.claimID, claims[0][service.FieldID])
	assert.Equal(t, models.ClaimReplace.String(), claims[0][service.FieldClaimType])
	assert.Nil(t, claims[0][service.FieldClaimCompletedAt])

	exchanges := readOrder(t, providers[service.ExchangeProviderName], fixture.order.ID)
	require.Len(t, exchanges, 1)
	assert.Equal(t, int64(-500), exchanges[0][service.FieldExchangeDifferenceDue], "the sign says who pays")
	assert.Empty(t, exchanges[0][service.FieldExchangePaymentCollectionID])

	replacements := readOrder(t, providers[service.ReplacementProviderName], fixture.order.ID)
	require.Len(t, replacements, 1, "reached through the claim")
	assert.Equal(t, fixture.replacID, replacements[0][service.FieldID])
	assert.Equal(t, fixture.claimID, replacements[0][service.FieldReplacementClaimID])
	assert.Empty(t, replacements[0][service.FieldReplacementExchangeID])
	assert.Nil(t, replacements[0][service.FieldReplacementDispatchedAt], "not sent yet")
	assert.Equal(t, []map[string]any{{"line_item_id": fixture.lineID, "variant_id": "", "quantity": int64(1)}},
		replacements[0][service.FieldAfterSalesItems])
}

// TestAnAfterSalesRecordWithoutLinesReadsAnEmptyList: a return that named no
// lines answers an empty list, not nil, so a reader ranges over it alike.
func TestAnAfterSalesRecordWithoutLinesReadsAnEmptyList(t *testing.T) {
	e := newEnv(t)
	order, _ := returnedOrder(t, e)
	_, err := e.svc.CreateReturn(context.Background(), service.CreateReturnInput{OrderID: order.ID})
	require.NoError(t, err)

	returns := readOrder(t, service.NewReturnQueryProvider(e.svc), order.ID, service.FieldAfterSalesItems)

	require.Len(t, returns, 1)
	assert.Equal(t, []map[string]any{}, returns[0][service.FieldAfterSalesItems])
}

// TestAnAfterSalesEntityIsReadPerOrder refuses what the entities do not
// answer: a read across orders, an empty order, another filter, a field the
// entity does not offer.
func TestAnAfterSalesEntityIsReadPerOrder(t *testing.T) {
	e := newEnv(t)

	for name, provider := range afterSalesProviders(e.svc) {
		for refusal, opts := range map[string]query.ListOptions{
			"no order":      {},
			"empty order":   {Filters: map[string]any{service.FieldAfterSalesOrderID: " "}},
			"another":       {Filters: map[string]any{service.FieldAfterSalesOrderID: "order_1", service.FieldAfterSalesStatus: "requested"}},
			"unknown field": {Filters: map[string]any{service.FieldAfterSalesOrderID: "order_1"}, Fields: []string{"metadata"}},
			"id and order":  {Filters: map[string]any{service.FieldID: "x", service.FieldAfterSalesOrderID: "order_1"}},
		} {
			_, err := provider.List(context.Background(), opts)
			require.Errorf(t, err, "%s: %s", name, refusal)
			assert.Truef(t, errors.IsInvalid(err), "%s: %s: %v", name, refusal, err)
		}
	}

	for _, name := range []string{service.ClaimProviderName, service.ExchangeProviderName} {
		_, err := afterSalesProviders(e.svc)[name].List(context.Background(), query.ListOptions{
			Filters: map[string]any{service.FieldAfterSalesOrderID: "order_1"},
			Fields:  []string{service.FieldAfterSalesItems},
		})
		assert.Truef(t, errors.IsInvalid(err), "%s has no lines: %v", name, err)
	}
}

// TestAnAfterSalesRecordIsReadByIdentity: the id filter and the batch read
// answer the same records, and an unknown identifier is left out.
func TestAnAfterSalesRecordIsReadByIdentity(t *testing.T) {
	e := newEnv(t)
	fixture := newAfterSalesOrder(t, e)

	for name, id := range map[string]string{
		service.ReturnProviderName:      fixture.returnID,
		service.ClaimProviderName:       fixture.claimID,
		service.ExchangeProviderName:    fixture.exchangeID,
		service.ReplacementProviderName: fixture.replacID,
	} {
		provider := afterSalesProviders(e.svc)[name]
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{service.FieldID: []any{id, "missing"}}, Fields: []string{service.FieldID},
		})
		require.NoError(t, err, name)
		assert.Equal(t, []query.Record{{service.FieldID: id}}, records, name)

		none, err := provider.FetchByIDs(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.Empty(t, none)
	}
}

// TestTheAfterSalesEntitiesAreNamedForTheirProviders: the read layer looks a
// provider up as "<entity>.query" and checks Entity() against it (ADR 0004).
func TestTheAfterSalesEntitiesAreNamedForTheirProviders(t *testing.T) {
	e := newEnv(t)

	for name, provider := range afterSalesProviders(e.svc) {
		assert.Equal(t, strings.TrimSuffix(name, query.ProviderSuffix), provider.Entity())
	}
}
