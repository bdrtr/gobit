package service_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// A withdrawal from a requested exchange prices the line items left again from
// each line's first unit (ADR 0432), so a line's live items always add up to
// the share of the units they send, whatever was written and withdrawn before.

// lineSale is the two-line order with line A of the given quantity, total and
// tax, its sticker 1 000 a unit and its discount whatever makes the total.
func lineSale(quantity, total, tax int64) service.CreateOrderInput {
	in := pricedOrderInput()
	subtotal := quantity * 1000
	discount := subtotal + tax - total
	in.Items[0].Quantity, in.Items[0].UnitPrice, in.Items[0].Subtotal = quantity, 1000, subtotal
	in.Items[0].DiscountTotal, in.Items[0].TaxTotal, in.Items[0].Total = discount, tax, total
	in.Subtotal, in.DiscountTotal = subtotal+1400, discount
	in.TaxTotal, in.Total = tax+14, total+1414+500

	return in
}

// share is ⌊amount × part / whole⌋, the arithmetic the tests check against.
func share(amount, part, whole int64) int64 { return amount * part / whole }

// sendOne writes a replacement of the exchange sending units of line A.
func sendOne(t *testing.T, e pricedEnv, exchangeID, lineA string, units int64) service.ReplacementRecord {
	t.Helper()

	record, err := e.svc.CreateReplacement(context.Background(), sendingFor(exchangeID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: units}))
	require.NoError(t, err)

	return record
}

// differenceNow reads the exchange's difference.
func differenceNow(t *testing.T, e pricedEnv, exchangeID string) int64 {
	t.Helper()

	read, err := e.svc.GetExchange(context.Background(), exchangeID)
	require.NoError(t, err)

	return read.DifferenceDue
}

// TestWithdrawingAndSendingAgainKeepsAnEvenSwapAtNothing is the review's probe:
// a line of 10 005 over ten units, whose units are worth 1 000 and 1 001 in
// turn, four of them back, single units sent and withdrawn in an order that
// left the figure at +2 when an item kept the units it was written for.
func TestWithdrawingAndSendingAgainKeepsAnEvenSwapAtNothing(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := saleOf(t, e.env, lineSale(10, 10_005, 5), 4)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-4002), exchange.DifferenceDue)

	x1 := sendOne(t, e, exchange.ID, lineA, 1)
	sendOne(t, e, exchange.ID, lineA, 1)
	_, err = e.svc.CancelReplacement(ctx, x1.ID)
	require.NoError(t, err)
	sendOne(t, e, exchange.ID, lineA, 1)
	x2 := sendOne(t, e, exchange.ID, lineA, 1)
	sendOne(t, e, exchange.ID, lineA, 1)
	_, err = e.svc.CancelReplacement(ctx, x2.ID)
	require.NoError(t, err)
	sendOne(t, e, exchange.ID, lineA, 1)

	assert.Zero(t, differenceNow(t, e, exchange.ID), "four units for four back owe nothing")
	live, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	var totals []int64
	for i := len(live) - 1; i >= 0; i-- {
		if live[i].Status == models.ReplacementCanceled {
			continue
		}
		record, err := e.svc.GetReplacement(ctx, live[i].ID)
		require.NoError(t, err)
		totals = append(totals, record.Items[0].Price.Total)
	}
	assert.Equal(t, []int64{1000, 1001, 1000, 1001}, totals, "the live units are the line's first four, in write order")
}

// TestAnyWritesAndWithdrawalsKeepALinesItemsAtItsShare runs seeded sequences
// of sends and withdrawals on lines whose shares do not divide evenly, and
// after every step reads the live items back: their totals and taxes add up to
// the share of the units they send, and the difference is that share less the
// return's worth.
func TestAnyWritesAndWithdrawalsKeepALinesItemsAtItsShare(t *testing.T) {
	for _, line := range []struct{ quantity, total, tax int64 }{
		{10, 10_005, 5},
		{3, 3_362, 560},
		{7, 7_913, 913},
		{12, 12_011, 2_003},
	} {
		for seed := uint64(1); seed <= 6; seed++ {
			t.Run(fmt.Sprintf("%d over %d, seed %d", line.total, line.quantity, seed), func(t *testing.T) {
				ctx := context.Background()
				e := newPricedEnv(t, jacketQuote)
				back := line.quantity / 2
				order, lineA, _, ret := saleOf(t, e.env, lineSale(line.quantity, line.total, line.tax), back)
				exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
				require.NoError(t, err)
				worth := share(line.total, back, line.quantity)
				random := rand.New(rand.NewPCG(seed, seed*7919))
				var live []service.ReplacementRecord
				units := int64(0)

				for step := 0; step < 12; step++ {
					if len(live) > 0 && (units == line.quantity || random.IntN(3) == 0) {
						k := random.IntN(len(live))
						_, err := e.svc.CancelReplacement(ctx, live[k].ID)
						require.NoError(t, err)
						units -= live[k].Items[0].Quantity
						live = append(live[:k], live[k+1:]...)
					} else {
						q := 1 + random.Int64N(min(2, line.quantity-units))
						live = append(live, sendOne(t, e, exchange.ID, lineA, q))
						units += q
					}

					var total, tax int64
					for i := range live {
						read, err := e.svc.GetReplacement(ctx, live[i].ID)
						require.NoError(t, err)
						total += read.Items[0].Price.Total
						tax += read.Items[0].Price.TaxTotal
					}
					require.Equal(t, share(line.total, units, line.quantity), total, "step %d: %d units live", step, units)
					require.Equal(t, share(line.tax, units, line.quantity), tax, "step %d", step)
					require.Equal(t, share(line.total, units, line.quantity)-worth,
						differenceNow(t, e, exchange.ID), "step %d", step)
				}
			})
		}
	}
}

// TestASettledExchangeIsNotPricedAgainUntilItReopens withdraws a replacement
// from a settled exchange, where nothing moves, and then cancels the parcel of
// the one it sent: the exchange is requested again, and its items and its
// figure are derived again.
func TestASettledExchangeIsNotPricedAgainUntilItReopens(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := saleOf(t, e.env, lineSale(10, 10_005, 5), 2)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	x := sendOne(t, e, exchange.ID, lineA, 1)
	y := sendOne(t, e, exchange.ID, lineA, 1)
	require.Equal(t, int64(1001), y.Items[0].Price.Total, "the second unit")
	require.Zero(t, differenceNow(t, e, exchange.ID))
	require.NoError(t, e.svc.RecordReplacementReservation(ctx, y.ID, y.Items[0].ID, pricedVariantA, "invres_y"))
	_, err = e.svc.MarkReplacementDispatched(ctx, y.ID, "ful_y")
	require.NoError(t, err)
	_, err = e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)

	_, err = e.svc.CancelReplacement(ctx, x.ID)
	require.NoError(t, err)
	read, err := e.svc.GetReplacement(ctx, y.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1001), read.Items[0].Price.Total, "a settled exchange's figures stay as they stood")
	assert.Zero(t, differenceNow(t, e, exchange.ID))

	_, err = e.svc.RecallReplacement(ctx, y.ID, "ful_y")
	require.NoError(t, err)

	reopened, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	require.Equal(t, models.ExchangeRequested, reopened.Status)
	read, err = e.svc.GetReplacement(ctx, y.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), read.Items[0].Price.Total, "requested again, the one unit sent is the first")
	assert.Equal(t, int64(1000-2001), reopened.DifferenceDue, "what it sends less the two units back")
}

// TestUnitsWorthUnderAMinorUnitAreNotSentAboveTheirTotal refuses a piece of a
// line worth half a minor unit a unit whose share of the tax comes out above
// its share of the total, which no row may carry.
func TestUnitsWorthUnderAMinorUnitAreNotSentAboveTheirTotal(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	in := pricedOrderInput()
	in.Items[0].Quantity, in.Items[0].UnitPrice, in.Items[0].Subtotal = 100, 1, 100
	in.Items[0].DiscountTotal, in.Items[0].TaxTotal, in.Items[0].Total = 58, 8, 50
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.Total = 1500, 58, 22, 1964
	order, lineA, _, ret := saleOf(t, e.env, in, 13)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	sendOne(t, e, exchange.ID, lineA, 12)

	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))

	require.Error(t, err, "floor(50x13/100) - 6 = 0 and floor(8x13/100) - 0 = 1")
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeReplacementTaxAboveTotal, errors.CodeOf(err))
}

// TestAWithdrawalThatWouldPriceAPieceAboveItsTotalIsRefused prices the pieces
// after a withdrawal again and refuses the withdrawal when one of them would
// come out with more tax than total: on a line of 50 over a hundred units with
// 8 of tax, a single unit at the thirteenth place is 0 with 1 of tax.
func TestAWithdrawalThatWouldPriceAPieceAboveItsTotalIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	in := pricedOrderInput()
	in.Items[0].Quantity, in.Items[0].UnitPrice, in.Items[0].Subtotal = 100, 1, 100
	in.Items[0].DiscountTotal, in.Items[0].TaxTotal, in.Items[0].Total = 58, 8, 50
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.Total = 1500, 58, 22, 1964
	order, lineA, _, ret := saleOf(t, e.env, in, 13)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	first := sendOne(t, e, exchange.ID, lineA, 1)
	sendOne(t, e, exchange.ID, lineA, 12)
	sendOne(t, e, exchange.ID, lineA, 1)
	before := differenceNow(t, e, exchange.ID)

	_, err = e.svc.CancelReplacement(ctx, first.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeReplacementTaxAboveTotal, errors.CodeOf(err))
	read, err := e.svc.GetReplacement(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReplacementRequested, read.Status, "nothing was withdrawn")
	assert.Equal(t, before, differenceNow(t, e, exchange.ID))
}
