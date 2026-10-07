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

// An exchange that names its return prices what it sends and derives its
// difference (ADR 0432).
//
// The order the tests stand on sells two lines whose figures do not divide by
// their quantities, so a unit's share is rounded and a mutant that rounds
// another way, or shares another figure, moves a number:
//
//	line A: 3 x 1000, a discount of 199, 560 tax at 20%  -> total 3361
//	line B: 2 x 700, no discount, 14 tax at 1%           -> total 1414
//
// One unit of A is worth floor(3361 / 3) = 1120, of which floor(560 / 3) = 186
// is tax; two units of A are worth 2240.

const (
	pricedVariantA = "variant_SHIRT"
	pricedVariantB = "variant_BOOK"
	quotedVariant  = "variant_JACKET"
	pricedChannel  = "sc_STORE"
)

// pricedOrderInput is the two-line order above, sold in a channel.
func pricedOrderInput() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID: testRegionID, CustomerID: testCustomerID, Email: "buyer@example.com",
		CurrencyCode: "TRY", CartID: "cart_PRICED", SalesChannelID: pricedChannel,
		Subtotal: 4400, DiscountTotal: 199, TaxTotal: 574, ShippingTotal: 500, Total: 5275,
		Items: []service.CreateOrderItemInput{
			{
				VariantID: pricedVariantA, Title: "Shirt", Quantity: 3, UnitPrice: 1000, Subtotal: 3000,
				DiscountTotal: 199, TaxRateBps: 2000, TaxTotal: 560, Total: 3361,
			},
			{
				VariantID: pricedVariantB, Title: "Book", Quantity: 2, UnitPrice: 700, Subtotal: 1400,
				TaxRateBps: 100, TaxTotal: 14, Total: 1414,
			},
		},
	}
}

// fakeQuote is the cart flow's quote as a test scripts it: it records the
// request and answers the response it was given.
type fakeQuote struct {
	asked    []json.RawMessage
	response string
	err      error
}

func (f *fakeQuote) QuoteExchangeLinesJSON(_ context.Context, request json.RawMessage) (json.RawMessage, error) {
	f.asked = append(f.asked, request)
	if f.err != nil {
		return nil, f.err
	}

	return json.RawMessage(f.response), nil
}

// jacketQuote answers two jackets at 900 each, taxed 1%: 1800 + 18 = 1818.
const jacketQuote = `{"currency_code":"TRY","prices_include_tax":false,"tax_source":"tax","lines":[
	{"line_item_id":"line-0","unit_price":900,"subtotal":1800,"discount_total":0,"tax_total":18,
	 "tax_rate_bps":100,"total":1818}]}`

// pricedEnv is an env whose service has a quote bound.
type pricedEnv struct {
	env
	quote *fakeQuote
}

func newPricedEnv(t *testing.T, response string) pricedEnv {
	t.Helper()

	e := newEnv(t)
	quote := &fakeQuote{response: response}
	svc, err := service.New(service.Options{Repo: e.store, Events: e.bus, Quotes: quote})
	require.NoError(t, err)
	e.svc = svc

	return pricedEnv{env: e, quote: quote}
}

// pricedSale places the two-line order and opens a return of the given units of
// line A; it answers the order, line A's and line B's ids, and the return.
func pricedSale(t *testing.T, e env, unitsBack int64) (order models.Order, lineA, lineB string, ret models.Return) {
	t.Helper()

	return saleOf(t, e, pricedOrderInput(), unitsBack)
}

// saleOf places the given two-line order and opens a return of the given units
// of its first line.
func saleOf(
	t *testing.T, e env, in service.CreateOrderInput, unitsBack int64,
) (order models.Order, lineA, lineB string, ret models.Return) {
	t.Helper()

	ctx := context.Background()
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 2)
	for i := range detail.Items {
		item := &detail.Items[i]
		switch item.VariantID {
		case pricedVariantA:
			lineA = item.ID
		case pricedVariantB:
			lineB = item.ID
		}
	}
	ret, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: lineA, Quantity: unitsBack}},
	})
	require.NoError(t, err)

	return order, lineA, lineB, ret
}

// sendingFor is a replacement of the exchange that sends the given lines.
func sendingFor(exchangeID string, lines ...service.ReplacementLineInput) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ExchangeID: exchangeID, ShippingOptionID: testShippingOptionID, LocationID: testLocationID, Lines: lines,
	}
}

// TestAnExchangeThatNamesItsReturnOwesWhatComesBack opens the exchange at minus
// the worth of the units its return takes back, shared from the line's total.
func TestAnExchangeThatNamesItsReturnOwesWhatComesBack(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	assert.Equal(t, ret.ID, exchange.ReturnID)
	assert.True(t, exchange.Priced())
	assert.Equal(t, int64(-1120), exchange.DifferenceDue,
		"one unit of a 3-unit line totalling 3361 is worth floor(3361/3)")
	assert.Equal(t, []string{order.ID}, e.store.lockedOrders[len(e.store.lockedOrders)-1:],
		"the return is named under the order's lock")
	assert.Contains(t, e.store.lockedReturns, ret.ID, "the return is named under its own lock")
}

// TestAnExchangeThatNamesItsReturnTakesNoTypedDifference refuses a figure typed
// beside the return.
func TestAnExchangeThatNamesItsReturnTakesNoTypedDifference(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)

	_, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: order.ID, ReturnID: ret.ID, DifferenceDue: 500,
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, service.CodeExchangeDifferenceDerived, errors.CodeOf(err))
}

// TestAReturnIsNamedOnlyWhileItIsRequested refuses a received and a withdrawn
// return: a received one may have been refunded already.
func TestAReturnIsNamedOnlyWhileItIsRequested(t *testing.T) {
	ctx := context.Background()

	t.Run("received", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, _, _, ret := pricedSale(t, e.env, 1)
		_, err := e.svc.ReceiveReturn(ctx, ret.ID, testLocationID)
		require.NoError(t, err)

		_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, service.CodeExchangeReturnNotOpen, errors.CodeOf(err))
	})

	t.Run("withdrawn", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, _, _, ret := pricedSale(t, e.env, 1)
		_, err := e.svc.CancelReturn(ctx, ret.ID)
		require.NoError(t, err)

		_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})

		require.Error(t, err)
		assert.Equal(t, service.CodeExchangeReturnNotOpen, errors.CodeOf(err))
	})
}

// TestAReturnIsTakenBackByOneLiveExchange refuses a second exchange naming it
// and lets a withdrawn one's return go.
func TestAReturnIsTakenBackByOneLiveExchange(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)

	first, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeExchangeReturnNamed, errors.CodeOf(err))

	_, err = e.svc.CancelExchange(ctx, first.ID)
	require.NoError(t, err)
	second, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err, "a withdrawn exchange lets its return go")
	assert.Equal(t, ret.ID, second.ReturnID)
}

// TestAnExchangeNamesAReturnOfItsOwnOrder refuses another order's return.
func TestAnExchangeNamesAReturnOfItsOwnOrder(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	_, _, _, ret := pricedSale(t, e.env, 1)
	other, err := e.svc.CreateOrder(ctx, pricedOrderInput())
	require.NoError(t, err)

	_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: other.ID, ReturnID: ret.ID})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, service.CodeExchangeReturnOtherOrder, errors.CodeOf(err))
}

// TestAReturnThatNamesNoLineCannotBeTakenBack refuses a return whose goods
// cannot be valued.
func TestAReturnThatNamesNoLineCannotBeTakenBack(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, err := e.svc.CreateOrder(ctx, pricedOrderInput())
	require.NoError(t, err)
	empty, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
	require.NoError(t, err)

	_, err = e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: empty.ID})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, service.CodeExchangeReturnEmpty, errors.CodeOf(err))
}

// TestAnItemNamingALineIsPricedAtWhatTheLineCharged sends the line's own units
// at their share of its figures, so an even swap owes nothing.
func TestAnItemNamingALineIsPricedAtWhatTheLineCharged(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 2)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-2240), exchange.DifferenceDue)

	record, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 2}))
	require.NoError(t, err)

	require.Len(t, record.Items, 1)
	require.NotNil(t, record.Items[0].Price)
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1000, Total: 2240, TaxTotal: 373, TaxRateBps: 2000, PricedBy: models.PricedByLine,
	}, *record.Items[0].Price, "two units of the line: floor(3361x2/3) and floor(560x2/3)")
	assert.Empty(t, e.quote.asked, "a line is not quoted")

	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Zero(t, read.DifferenceDue, "an even swap owes nothing")
}

// TestAnItemNamingAVariantIsPricedByTheQuote asks the quote in the order's
// region, channel and customer and adds what it answers to the difference.
func TestAnItemNamingAVariantIsPricedByTheQuote(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	record, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)

	require.Len(t, e.quote.asked, 1)
	assert.JSONEq(t, `{"region_id":"`+testRegionID+`","sales_channel_id":"`+pricedChannel+
		`","customer_id":"`+testCustomerID+`","lines":[{"variant_id":"`+quotedVariant+`","quantity":2}]}`,
		string(e.quote.asked[0]))
	require.NotNil(t, record.Items[0].Price)
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 900, Total: 1818, TaxTotal: 18, TaxRateBps: 100, PricedBy: models.PricedByQuote,
	}, *record.Items[0].Price)

	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1818-1120), read.DifferenceDue, "what it sends less what comes back")
}

// TestAnOperatorPriceIsQuotedForItsTax passes the operator's unit price to the
// quote and records the item as priced by the operator.
func TestAnOperatorPriceIsQuotedForItsTax(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, `{"currency_code":"TRY","prices_include_tax":false,"lines":[
		{"unit_price":850,"subtotal":1700,"discount_total":0,"tax_total":17,"tax_rate_bps":100,"total":1717}]}`)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	price := int64(850)

	record, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2, UnitPrice: &price}))
	require.NoError(t, err)

	require.Len(t, e.quote.asked, 1)
	assert.Contains(t, string(e.quote.asked[0]), `"unit_price":850`)
	require.NotNil(t, record.Items[0].Price)
	assert.Equal(t, models.PricedByOperator, record.Items[0].Price.PricedBy)
	assert.Equal(t, int64(17), record.Items[0].Price.TaxTotal, "an operator's price is still taxed")
}

// TestAUnitPriceIsTakenOnlyWhereSomethingIsPriced refuses it on a line, on a
// claim and on an exchange that names no return.
func TestAUnitPriceIsTakenOnlyWhereSomethingIsPriced(t *testing.T) {
	ctx := context.Background()
	price := int64(900)

	t.Run("on a line of a priced exchange", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		order, lineA, _, ret := pricedSale(t, e.env, 1)
		exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
		require.NoError(t, err)

		_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
			service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1, UnitPrice: &price}))

		require.Error(t, err)
		assert.Equal(t, service.CodeReplacementPriceRefused, errors.CodeOf(err))
	})

	t.Run("on an exchange that names no return", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		typed, _ := exchangeToSend(t, e.env, 300)

		_, err := e.svc.CreateReplacement(ctx, sendingFor(typed.ID,
			service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 1, UnitPrice: &price}))

		require.Error(t, err)
		assert.Equal(t, service.CodeReplacementPriceRefused, errors.CodeOf(err))
		assert.Empty(t, e.quote.asked)
	})

	t.Run("on a claim", func(t *testing.T) {
		e := newPricedEnv(t, jacketQuote)
		claim, lineID := claimToReplace(t, e.env)
		in := replacementOf(claim.ID, lineID, 1)
		in.Lines[0].UnitPrice = &price

		_, err := e.svc.CreateReplacement(ctx, in)

		require.Error(t, err)
		assert.Equal(t, service.CodeReplacementPriceRefused, errors.CodeOf(err))
	})
}

// TestAnExchangeThatNamesNoReturnPricesNothing keeps the typed exchange as it
// was: its items carry no price, nothing is quoted, its figure stays.
func TestAnExchangeThatNamesNoReturnPricesNothing(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	typed, lineID := exchangeToSend(t, e.env, 300)

	record, err := e.svc.CreateReplacement(ctx, sendingFor(typed.ID,
		service.ReplacementLineInput{OrderLineItemID: lineID, Quantity: 1},
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 1}))
	require.NoError(t, err)

	for _, item := range record.Items {
		assert.Nil(t, item.Price, "%s", item.Names())
	}
	assert.Empty(t, e.quote.asked)
	read, err := e.svc.GetExchange(ctx, typed.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(300), read.DifferenceDue)
}

// TestAQuoteInTermsTheSaleCannotCarryIsRefused refuses a region whose currency
// or tax convention left the order's.
func TestAQuoteInTermsTheSaleCannotCarryIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, response := range map[string]string{
		"another currency": `{"currency_code":"EUR","prices_include_tax":false,"lines":[
			{"unit_price":900,"subtotal":1800,"tax_total":18,"tax_rate_bps":100,"total":1818}]}`,
		"tax now included": `{"currency_code":"TRY","prices_include_tax":true,"lines":[
			{"unit_price":909,"subtotal":1800,"tax_total":18,"tax_rate_bps":100,"total":1818}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newPricedEnv(t, response)
			order, _, _, ret := pricedSale(t, e.env, 1)
			exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
			require.NoError(t, err)

			_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
				service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))

			require.Error(t, err)
			assert.True(t, errors.IsConflict(err))
			assert.Equal(t, service.CodeExchangeRegionMoved, errors.CodeOf(err))
			replacements, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
			require.NoError(t, err)
			assert.Empty(t, replacements, "nothing was written")
		})
	}
}

// TestAQuoteOutsideItsContractIsRefused checks each identity a quoted line
// holds before it is recorded.
func TestAQuoteOutsideItsContractIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, line := range map[string]string{
		"total is not subtotal plus tax": `{"unit_price":900,"subtotal":1800,"tax_total":18,"tax_rate_bps":100,"total":1819}`,
		"subtotal is not its units":      `{"unit_price":900,"subtotal":1700,"tax_total":17,"tax_rate_bps":100,"total":1717}`,
		"a discount":                     `{"unit_price":900,"subtotal":1800,"discount_total":1,"tax_total":18,"tax_rate_bps":100,"total":1818}`,
		"a rate past 100%":               `{"unit_price":900,"subtotal":1800,"tax_total":18,"tax_rate_bps":10001,"total":1818}`,
		"a negative tax":                 `{"unit_price":900,"subtotal":1800,"tax_total":-1,"tax_rate_bps":100,"total":1799}`,
		"components not adding up": `{"unit_price":900,"subtotal":1800,"tax_total":18,"tax_rate_bps":100,"total":1818,
			"tax_components":[{"rate_bps":100,"taxable_amount":1800,"tax_amount":10}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newPricedEnv(t, `{"currency_code":"TRY","prices_include_tax":false,"lines":[`+line+`]}`)
			order, _, _, ret := pricedSale(t, e.env, 1)
			exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
			require.NoError(t, err)

			_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
				service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))

			require.Error(t, err)
			assert.Equal(t, service.CodeExchangeQuoteInvalid, errors.CodeOf(err))
		})
	}
}

// TestAVariantIsNotSentUnpricedWithoutAQuote refuses it when no quote is bound.
func TestAVariantIsNotSentUnpricedWithoutAQuote(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, _, _, ret := pricedSale(t, e, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 1}))

	require.Error(t, err)
	assert.Equal(t, service.CodeExchangeQuoteUnavailable, errors.CodeOf(err))
}

// TestWithdrawingAReplacementDerivesTheDifferenceAgain takes what it sent out
// of the difference.
func TestWithdrawingAReplacementDerivesTheDifferenceAgain(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	shirt, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1120+1818-1120), read.DifferenceDue)

	_, err = e.svc.CancelReplacement(ctx, shirt.ID)
	require.NoError(t, err)

	read, err = e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1818-1120), read.DifferenceDue, "the withdrawn shirt no longer counts")
	assert.Contains(t, e.store.lockedExchanges, exchange.ID)
}

// TestAFundedExchangesReplacementIsNotWithdrawn refuses taking goods out from
// under money held for them.
func TestAFundedExchangesReplacementIsNotWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	jacket, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_1", 698)
	require.NoError(t, err)

	_, err = e.svc.CancelReplacement(ctx, jacket.ID)

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeExchangeDifferenceHeld, errors.CodeOf(err))
	read, err := e.svc.GetReplacement(ctx, jacket.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReplacementRequested, read.Status)
}

// TestAFundingCheckedAgainstAnotherFigureIsRefused refuses a collection checked
// against a difference the exchange no longer owes.
func TestAFundingCheckedAgainstAnotherFigureIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)

	_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_1", 1818)

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeExchangeDifferenceMoved, errors.CodeOf(err))
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ExchangeRequested, read.Status)
}

// TestAReturnAnExchangeTakesBackIsNotWithdrawn keeps the units the difference
// counts coming back, and lets the return go once the exchange is withdrawn.
func TestAReturnAnExchangeTakesBackIsNotWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	_, err = e.svc.CancelReturn(ctx, ret.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeReturnNamedByExchange, errors.CodeOf(err))

	_, err = e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)
	withdrawn, err := e.svc.CancelReturn(ctx, ret.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnCanceled, withdrawn.Status)
}

// TestTheReturnDetailNamesTheExchangeThatSettlesIt tells the refund flow the
// return is the exchange's.
func TestTheReturnDetailNamesTheExchangeThatSettlesIt(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)

	raw, err := e.svc.ReturnDetailJSON(ctx, ret.ID)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "settled_by_exchange")

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	raw, err = e.svc.ReturnDetailJSON(ctx, ret.ID)
	require.NoError(t, err)
	var detail struct {
		SettledByExchange string `json:"settled_by_exchange"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail))
	assert.Equal(t, exchange.ID, detail.SettledByExchange)
}

// fundedAtLockStore funds the exchange the moment it is locked, which is what a
// funding that committed between a writer's unlocked read and its lock looks
// like to that writer.
type fundedAtLockStore struct {
	*fakeStore
}

func (s fundedAtLockStore) LockExchange(ctx context.Context, id string) (models.Exchange, error) {
	s.mu.Lock()
	exchange := s.exchanges[id]
	stamp := s.nextStamp()
	exchange.Status, exchange.PaymentCollectionID, exchange.FundedAt = models.ExchangeFunded, "paycol_race", &stamp
	s.exchanges[id] = exchange
	s.mu.Unlock()

	return s.fakeStore.LockExchange(ctx, id)
}

// TestAReplacementIsAddedOnlyToAnExchangeStillOpenUnderItsLock refuses a
// replacement of an exchange funded after the writer found it open: the
// difference it would rewrite holds money now.
func TestAReplacementIsAddedOnlyToAnExchangeStillOpenUnderItsLock(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	racing, err := service.New(service.Options{
		Repo: fundedAtLockStore{fakeStore: e.store}, Events: e.bus, Quotes: e.quote,
	})
	require.NoError(t, err)

	_, err = racing.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
	replacements, err := e.svc.ListReplacementsOfExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Empty(t, replacements, "nothing was written against the funded exchange")
}

// remainderTwoInput is the two-line order with line A's discount one lower, so
// its total, 3 362, leaves a remainder of two over its three units: one unit
// is worth 1 120 and two are worth 2 241, not 2 240.
func remainderTwoInput() service.CreateOrderInput {
	in := pricedOrderInput()
	in.DiscountTotal, in.Total = 198, 5276
	in.Items[0].DiscountTotal, in.Items[0].Total = 199-1, 3362

	return in
}

// TestAnEvenSwapSentInPiecesOwesNothing sends two returned units back one
// replacement at a time: each is priced as the units after those already sent,
// so the pieces add up to the two units' worth (ADR 0432).
func TestAnEvenSwapSentInPiecesOwesNothing(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := saleOf(t, e.env, remainderTwoInput(), 2)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-2241), exchange.DifferenceDue, "floor(3362x2/3)")

	first, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	second, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)

	assert.Equal(t, int64(1120), first.Items[0].Price.Total)
	assert.Equal(t, int64(1121), second.Items[0].Price.Total, "the second unit is floor(3362x2/3) - floor(3362/3)")
	assert.Equal(t, int64(186), first.Items[0].Price.TaxTotal)
	assert.Equal(t, int64(187), second.Items[0].Price.TaxTotal)
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Zero(t, read.DifferenceDue, "two units in two pieces are worth the two units returned")

	// A withdrawn earlier piece prices the later one again from the line's
	// first unit, and the next piece follows it: the swap still owes nothing.
	_, err = e.svc.CancelReplacement(ctx, first.ID)
	require.NoError(t, err)
	repriced, err := e.svc.GetReplacement(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1120), repriced.Items[0].Price.Total, "the one unit still sent is the first unit")
	assert.Equal(t, int64(186), repriced.Items[0].Price.TaxTotal)
	third, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	assert.Equal(t, int64(1121), third.Items[0].Price.Total, "priced after the one unit still sent")
	read, err = e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Zero(t, read.DifferenceDue)
}

// TestTheExitLetsAFundedExchangesReplacementGo runs the funded exchange's exit
// and then withdraws its replacement: the money went back with the exit, so
// nothing holds the replacement, and the figure stays as it stood.
func TestTheExitLetsAFundedExchangesReplacementGo(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	jacket, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_exit", 698)
	require.NoError(t, err)

	_, err = e.svc.CancelReplacement(ctx, jacket.ID)
	require.Error(t, err, "while funded, the collection holds the buyer's money for it")
	assert.Equal(t, service.CodeExchangeDifferenceHeld, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "paycol_exit")

	_, err = e.svc.WithdrawFundedExchange(ctx, exchange.ID)
	require.NoError(t, err)
	withdrawn, err := e.svc.CancelReplacement(ctx, jacket.ID)
	require.NoError(t, err, "the exit sent the money back, and the replacement goes after it")
	assert.Equal(t, models.ReplacementCanceled, withdrawn.Status)
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(698), read.DifferenceDue, "a withdrawn exchange's figure is not derived again")
}

// TestASettledExchangesOtherReplacementIsWithdrawn completes an exchange that
// owes nothing with one of its two replacements and withdraws the other: it
// holds no money, so nothing refuses, and the figure stays as it stood.
func TestASettledExchangesOtherReplacementIsWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 2)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	second, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	settled, err := e.svc.CompleteExchange(ctx, exchange.ID)
	require.NoError(t, err)
	require.Equal(t, models.ExchangeCompleted, settled.Status)
	require.Zero(t, settled.DifferenceDue)

	withdrawn, err := e.svc.CancelReplacement(ctx, second.ID)

	require.NoError(t, err)
	assert.Equal(t, models.ReplacementCanceled, withdrawn.Status)
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ExchangeCompleted, read.Status)
	assert.Zero(t, read.DifferenceDue, "a settled exchange's figure is not derived again")
}

// TestTheDetailSaysWhetherAReplacementCanBeWithdrawn answers, for the flow
// that releases stock before it withdraws, the order module's own rule.
func TestTheDetailSaysWhetherAReplacementCanBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	jacket, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	withdrawable := func() bool {
		raw, err := e.svc.ReplacementDetailJSON(ctx, jacket.ID)
		require.NoError(t, err)
		var detail struct {
			Withdrawable bool `json:"withdrawable"`
		}
		require.NoError(t, json.Unmarshal(raw, &detail))
		return detail.Withdrawable
	}

	assert.True(t, withdrawable(), "requested")
	_, err = e.svc.FundExchange(ctx, exchange.ID, "paycol_detail", 698)
	require.NoError(t, err)
	assert.False(t, withdrawable(), "funded")
	_, err = e.svc.WithdrawFundedExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.True(t, withdrawable(), "withdrawn after its money went back")
}

// inclusiveOrderInput is the two-line order sold in a market whose prices
// include their tax (ADR 0246): a line's unit price is the sticker, its
// subtotal what is left of the stickers once the tax is taken out, and its
// total what the buyer paid. Line A's 3 599 leaves a remainder over its three
// units.
func inclusiveOrderInput() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID: testRegionID, CustomerID: testCustomerID, Email: "buyer@example.com",
		CurrencyCode: "TRY", CartID: "cart_INCLUSIVE", SalesChannelID: pricedChannel, PricesIncludeTax: true,
		Subtotal: 4386, DiscountTotal: 1, TaxTotal: 614, ShippingTotal: 500, Total: 5499,
		Items: []service.CreateOrderItemInput{
			{
				VariantID: pricedVariantA, Title: "Shirt", Quantity: 3, UnitPrice: 1200, Subtotal: 3000,
				DiscountTotal: 1, TaxRateBps: 2000, TaxTotal: 600, Total: 3599,
			},
			{
				VariantID: pricedVariantB, Title: "Book", Quantity: 2, UnitPrice: 700, Subtotal: 1386,
				TaxRateBps: 100, TaxTotal: 14, Total: 1400,
			},
		},
	}
}

// TestAnExchangeOnATaxInclusiveOrderIsPricedInItsConvention prices a line and
// a variant of an order whose prices include their tax: the line's units at
// their share of its total, the variant at a quote whose total is its
// stickers (ADR 0432, ADR 0246).
func TestAnExchangeOnATaxInclusiveOrderIsPricedInItsConvention(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, `{"currency_code":"TRY","prices_include_tax":true,"lines":[
		{"unit_price":1210,"subtotal":2017,"discount_total":0,"tax_total":403,"tax_rate_bps":2000,"total":2420}]}`)
	order, lineA, _, ret := saleOf(t, e.env, inclusiveOrderInput(), 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-1199), exchange.DifferenceDue, "floor(3599/3), tax included")

	record, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1},
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)

	require.Len(t, record.Items, 2)
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1200, Total: 1199, TaxTotal: 200, TaxRateBps: 2000, PricedBy: models.PricedByLine,
	}, *record.Items[0].Price, "the sticker, and the unit's share of the total paid and of its tax")
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1210, Total: 2420, TaxTotal: 403, TaxRateBps: 2000, PricedBy: models.PricedByQuote,
	}, *record.Items[1].Price, "two stickers of 1 210 with their tax in them")
	read, err := e.svc.GetExchange(ctx, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1199+2420-1199), read.DifferenceDue)
}

// TestAnInclusiveQuoteWhoseTotalIsNotItsStickersIsRefused holds the identity a
// tax-inclusive line keeps: what the buyer pays is units times the sticker.
func TestAnInclusiveQuoteWhoseTotalIsNotItsStickersIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, `{"currency_code":"TRY","prices_include_tax":true,"lines":[
		{"unit_price":1210,"subtotal":2018,"discount_total":0,"tax_total":403,"tax_rate_bps":2000,"total":2421}]}`)
	order, _, _, ret := saleOf(t, e.env, inclusiveOrderInput(), 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	_, err = e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))

	require.Error(t, err)
	assert.Equal(t, service.CodeExchangeQuoteInvalid, errors.CodeOf(err))
}

// TestTheDetailSaysASentReplacementCannotBeWithdrawn answers no for goods that
// left, which a return takes back instead.
func TestTheDetailSaysASentReplacementCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	claim, lineID := claimToReplace(t, e)
	record, err := e.svc.CreateReplacement(ctx, replacementOf(claim.ID, lineID, 1))
	require.NoError(t, err)
	withdrawable := func() bool {
		raw, err := e.svc.ReplacementDetailJSON(ctx, record.ID)
		require.NoError(t, err)
		var detail struct {
			Withdrawable bool `json:"withdrawable"`
		}
		require.NoError(t, json.Unmarshal(raw, &detail))
		return detail.Withdrawable
	}
	require.True(t, withdrawable(), "a claim's replacement that has not left")

	require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, record.Items[0].ID, testVariantID, "invres_1"))
	_, err = e.svc.MarkReplacementDispatched(ctx, record.ID, "ful_1")
	require.NoError(t, err)

	assert.False(t, withdrawable(), "goods that left are taken back by a return")
}
