package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// exchangeToSend opens an order and an exchange on it, and returns the exchange
// with the order's single line.
func exchangeToSend(
	t *testing.T, e env, differenceDue int64,
) (exchange models.Exchange, lineID string) {
	t.Helper()

	ctx := context.Background()
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	exchange, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: order.ID, DifferenceDue: differenceDue,
	})
	require.NoError(t, err)

	return exchange, detail.Items[0].ID
}

// replacementOfExchange is the request that sends goods against an exchange.
func replacementOfExchange(exchangeID, lineID string, quantity int64) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ExchangeID:       exchangeID,
		ShippingOptionID: testShippingOptionID,
		LocationID:       testLocationID,
		Lines: []service.ReplacementLineInput{
			{OrderLineItemID: lineID, Quantity: quantity},
		},
	}
}

// TestAnExchangeCanSayWhatItWillSend is the half the record was missing.
func TestAnExchangeCanSayWhatItWillSend(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 2))
	require.NoError(t, err)

	assert.Equal(t, exchange.ID, record.ExchangeID)
	assert.Empty(t, record.ClaimID, "a replacement settles ONE record")
	assert.Equal(t, models.SourceExchange, record.Source())
	assert.Equal(t, exchange.ID, record.SourceID())
	require.Len(t, record.Items, 1)
	assert.Equal(t, int64(2), record.Items[0].Quantity)
}

// TestAReplacementSettlesExactlyOneRecord refuses both of the malformed shapes.
func TestAReplacementSettlesExactlyOneRecord(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	both := replacementOfExchange(exchange.ID, lineID, 1)
	both.ClaimID = "claim_1"

	neither := replacementOfExchange(exchange.ID, lineID, 1)
	neither.ExchangeID = ""

	for name, in := range map[string]service.CreateReplacementInput{
		"both a claim and an exchange": both,
		"neither":                      neither,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CreateReplacement(ctx, in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestOnlyAnOpenExchangeCanPromiseGoods keeps a settled record from gaining a
// new promise.
func TestOnlyAnOpenExchangeCanPromiseGoods(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	_, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheLineCeilingHoldsForAnExchangeToo keeps the rule that spans rows on both
// sources.
func TestTheLineCeilingHoldsForAnExchangeToo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	_, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 4))

	require.Error(t, err, "the order's line holds three units")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestAnExchangeThatOwesNothingCanBeSettled is the transition that came back.
func TestAnExchangeThatOwesNothingCanBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	settled, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)

	assert.Equal(t, models.ExchangeCompleted, settled.Status)
	require.NotNil(t, settled.CompletedAt, "the status and its moment imply each other")

	again, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err, "a second settlement is a no-op")
	assert.Equal(t, settled.CompletedAt.UTC(), again.CompletedAt.UTC(),
		"and it keeps the FIRST moment")
}

// TestAnExchangeThatOwesMoneyIsNotSettled is the bound, and it is the reason the
// completion came back narrow rather than general.
func TestAnExchangeThatOwesMoneyIsNotSettled(t *testing.T) {
	ctx := context.Background()

	for name, difference := range map[string]int64{
		"the customer owes": 500,
		"the shop owes":     -500,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			exchange, _ := exchangeToSend(t, e, difference)

			_, err := e.svc.CompleteExchange(ctx, exchange.ID)

			require.Error(t, err,
				"money cannot be moved against an existing order, so saying the "+
					"exchange is settled would say a balance was handled")
		})
	}
}

// TestAWithdrawnExchangeCannotBeSettled is the third row of the table.
func TestAWithdrawnExchangeCannotBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	_, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CompleteExchange(ctx, exchange.ID)

	require.Error(t, err, "a withdrawn request has no goods to answer")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestASettledExchangeCannotBeWithdrawn is the mirror refusal.
func TestASettledExchangeCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, _ := exchangeToSend(t, e, 0)

	_, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)

	require.Error(t, err, "an exchange that was met is not un-met by withdrawing the request")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestTheDetailNamesTheExchangeAsItsSource is the wire the dispatch flow reads.
func TestTheDetailNamesTheExchangeAsItsSource(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err)

	var detail struct {
		SourceKind       string `json:"source_kind"`
		SourceID         string `json:"source_id"`
		SourceStatus     string `json:"source_status"`
		SourceSettleable bool   `json:"source_settleable"`
		OrderID          string `json:"order_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))

	assert.Equal(t, "exchange", detail.SourceKind)
	assert.Equal(t, exchange.ID, detail.SourceID)
	assert.Equal(t, "requested", detail.SourceStatus)
	assert.True(t, detail.SourceSettleable, "this one owes nothing")
	assert.Equal(t, exchange.OrderID, detail.OrderID,
		"the order comes from the SOURCE; the replacement does not carry a copy")
}

// TestTheDetailSaysAnOwingExchangeCannotBeSettled is what keeps a dispatch that
// really sent the goods from failing over a correct state.
func TestTheDetailSaysAnOwingExchangeCannotBeSettled(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 750)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
	require.NoError(t, err)

	var detail struct {
		SourceSettleable bool `json:"source_settleable"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))

	assert.False(t, detail.SourceSettleable,
		"the PRODUCER answers it; a flow that re-derived the rule would hold a "+
			"second copy of it")
}

// TestAnExchangesReplacementsAreListedApart keeps the two sources' listings from
// answering each other's question.
func TestAnExchangesReplacementsAreListedApart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	listed, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, record.ID, listed[0].ID)

	claim, claimLineID := claimToReplace(t, e)
	_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, claimLineID, 1))
	require.NoError(t, err)

	listed, err = e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Len(t, listed, 1, "the claim's promise is not the exchange's")
}

// TestAnExchangeWithAnOpenPromiseCannotBeWithdrawn is the claim's guard, on the
// record ADR 0114 gave the same capability to.
//
// The claim has refused this since it could promise goods, and its reason is
// the one that applies here word for word: the promise outlives the record that
// made it. ADR 0114 let a replacement name an EXCHANGE as its source and be
// dispatched against it, and the guard stayed on the claim — so a withdrawn
// exchange still had goods on the way and nothing between the two said so.
//
// Gap D56. The asymmetry is the same shape D52 had: a capability moved to the
// sibling record and the rule written for the first one did not follow.
func TestAnExchangeWithAnOpenPromiseCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReplacementNotOpen, errors.CodeOf(err))
	assert.Contains(t, err.Error(), record.ID,
		"the refusal has to name the replacement to withdraw first")
}

// TestAWithdrawnPromiseFreesTheExchange keeps the guard from becoming a lock.
//
// It is the other half of the claim's shape: withdrawing the replacement first
// leaves the exchange withdrawable, so the record never reaches a state with no
// way out. Without this the guard would trade one stuck record for another.
func TestAWithdrawnPromiseFreesTheExchange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	exchange, lineID := exchangeToSend(t, e, 0)

	record, err := e.svc.CreateReplacement(ctx, replacementOfExchange(exchange.ID, lineID, 1))
	require.NoError(t, err)
	_, err = e.svc.CancelReplacement(ctx, record.ID)
	require.NoError(t, err)

	withdrawn, err := e.svc.CancelExchange(ctx, exchange.ID)

	require.NoError(t, err, "a withdrawn promise no longer holds the exchange open")
	assert.Equal(t, models.ExchangeCanceled, withdrawn.Status)
}
