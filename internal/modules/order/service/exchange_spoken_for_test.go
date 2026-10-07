package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// An exchange that names its return takes a line's units back through the
// return and sends the same line's units again through its replacement: both
// name the same goods, so the exchange speaks for each of the line's units
// once, the more of the two (ADR 0432, amending ADR 0423's count).

// spokenFor reads what the order's returns and replacements speak for on a line.
func spokenFor(t *testing.T, e env, lineID string) int64 {
	t.Helper()

	spoken, err := e.svc.SpokenForUnits(context.Background(), []string{lineID})
	require.NoError(t, err)

	return spoken[lineID]
}

// TestAnExchangeSpeaksForAUnitItTakesBackAndSendsAgainOnce is the follow-up
// review's F2 on ADR 0423: three units sold, one taken back by the exchange's
// return and one of the same line sent again by its replacement. The
// exchange speaks for one unit, so two are owed again; the sum said two and
// left one.
func TestAnExchangeSpeaksForAUnitItTakesBackAndSendsAgainOnce(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(1), spokenFor(t, e.env, lineA), "the return asks one unit back")

	sendOne(t, e, exchange.ID, lineA, 1)

	assert.Equal(t, int64(1), spokenFor(t, e.env, lineA), "the same unit, taken back and sent again")
}

// TestAnExchangeSpeaksForTheMoreOfWhatItTakesBackAndWhatItSends counts an
// exchange that sends more of the line than its return takes back, and one
// that sends less.
func TestAnExchangeSpeaksForTheMoreOfWhatItTakesBackAndWhatItSends(t *testing.T) {
	ctx := context.Background()

	t.Run("sends more", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, ret := pricedSale(t, e.env, 1)
		exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
		require.NoError(t, err)

		sendOne(t, e, exchange.ID, lineA, 2)

		assert.Equal(t, int64(2), spokenFor(t, e.env, lineA))
	})

	t.Run("takes back more", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, ret := pricedSale(t, e.env, 2)
		exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
		require.NoError(t, err)

		sendOne(t, e, exchange.ID, lineA, 1)

		assert.Equal(t, int64(2), spokenFor(t, e.env, lineA))
	})

	t.Run("withdrawn, its return and its replacement count apart again", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, ret := pricedSale(t, e.env, 1)
		exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
		require.NoError(t, err)
		sent := sendOne(t, e, exchange.ID, lineA, 1)
		_, err = e.svc.CancelReplacement(ctx, sent.ID)
		require.NoError(t, err)
		_, err = e.svc.CancelExchange(ctx, exchange.ID)
		require.NoError(t, err)

		assert.Equal(t, int64(1), spokenFor(t, e.env, lineA), "the return alone, as any return")
	})
}

// TestAReturnAndAReplacementNoExchangeTiesStillAddUp keeps the sum where
// nothing says the two name the same goods: a return beside an exchange that
// names no return, and a return beside a claim's replacement.
func TestAReturnAndAReplacementNoExchangeTiesStillAddUp(t *testing.T) {
	ctx := context.Background()

	t.Run("an exchange that names no return", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, _ := pricedSale(t, e.env, 1)
		typed, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, DifferenceDue: 0})
		require.NoError(t, err)

		sendOne(t, e, typed.ID, lineA, 1)

		assert.Equal(t, int64(2), spokenFor(t, e.env, lineA), "one asked back and one sent again")
	})

	t.Run("a claim's replacement", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, _ := pricedSale(t, e.env, 1)
		claim, err := e.svc.CreateClaim(ctx, service.CreateClaimInput{OrderID: order.ID, Type: "replace"})
		require.NoError(t, err)

		_, err = e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineA, 1))
		require.NoError(t, err)

		assert.Equal(t, int64(2), spokenFor(t, e.env, lineA))
	})
}
