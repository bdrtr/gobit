//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrdersReplacementsAreFoundThroughItsClaimsAndExchanges is the real
// query behind the timeline's replacement entries (ADR 0170).
//
// A replacement row names its claim or its exchange and not its order, so the
// query reaches the order through both. A query that followed only one of them
// would lose half the replacements, and one that forgot the order would hand
// the timeline another order's goods; the fake store answers both correctly by
// construction, which is why this is asked of the database.
func TestAnOrdersReplacementsAreFoundThroughItsClaimsAndExchanges(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	store := repository.New(testPool.Pool())

	claim, lineID := replaceableClaim(ctx, t, svc)
	byClaim, err := svc.CreateReplacement(ctx, requestOf(claim.ID, lineID, 1))
	require.NoError(t, err)

	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: claim.OrderID})
	require.NoError(t, err)
	byExchange, err := svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ExchangeID:       exchange.ID,
		ShippingOptionID: "so_integration",
		LocationID:       "sloc_integration",
		Lines:            []service.ReplacementLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)

	other, otherLine := replaceableClaim(ctx, t, svc)
	_, err = svc.CreateReplacement(ctx, requestOf(other.ID, otherLine, 1))
	require.NoError(t, err)

	found, err := store.ListReplacementsByOrder(ctx, claim.OrderID, 100)
	require.NoError(t, err)

	ids := make([]string, 0, len(found))
	for i := range found {
		ids = append(ids, found[i].ID)
	}
	assert.Equal(t, []string{byClaim.ID, byExchange.ID}, ids,
		"both sources, this order only, oldest first")

	limited, err := store.ListReplacementsByOrder(ctx, claim.OrderID, 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, byClaim.ID, limited[0].ID)
}

// TestTheOrderReadCarriesTheMomentOfItsErasure is the stamp the timeline dates
// the erasure by (ADR 0170), read through the repository rather than the raw
// column: the timeline's own tests hand it a model, so only this read proves the
// column reaches one.
func TestTheOrderReadCarriesTheMomentOfItsErasure(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	const (
		customerID = "cus_TIMELINE_ERASED"
		email      = "timeline.erased@example.com"
	)
	ord := placeOrderFor(t, svc, customerID, email)
	settleOrder(t, svc, ord)

	before, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Nil(t, before.PersonalDataErasedAt, "nothing was erased yet")

	_, err = svc.Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)

	after, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	stamp := erasureStamp(t, ord.ID)
	require.NotNil(t, stamp)
	require.NotNil(t, after.PersonalDataErasedAt)
	assert.True(t, stamp.Equal(*after.PersonalDataErasedAt))
}
