//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// An exchange that names its return prices what it sends, on the real schema
// (ADR 0432). The order sells two lines whose totals do not divide by their
// quantities: one unit of the shirt is worth floor(3361/3) = 1120.

const (
	shirtVariant  = "variant_SHIRT_INT"
	bookVariant   = "variant_BOOK_INT"
	jacketVariant = "variant_JACKET_INT"
)

// scriptedQuote answers every quote with its response, or by default with one
// line of two jackets at 900, taxed 1% through a two-rate stack: 1800 + 18 =
// 1818.
type scriptedQuote struct {
	asked    atomic.Int32
	response string
}

func (q *scriptedQuote) QuoteExchangeLinesJSON(context.Context, json.RawMessage) (json.RawMessage, error) {
	q.asked.Add(1)
	if q.response != "" {
		return json.RawMessage(q.response), nil
	}

	return json.RawMessage(`{"currency_code":"TRY","prices_include_tax":false,"lines":[{
		"unit_price":900,"subtotal":1800,"discount_total":0,"tax_total":18,"tax_rate_bps":50,"total":1818,
		"tax_components":[
			{"rate_id":"txr_base","rate_bps":50,"compound":false,"taxable_amount":1800,"tax_amount":9},
			{"rate_id":"txr_top","rate_bps":50,"compound":true,"taxable_amount":1809,"tax_amount":9}]}]}`), nil
}

// pricedService is a service on the shared pool with the scripted quote bound.
func pricedService(t *testing.T, store service.Store) *service.Service {
	t.Helper()

	return quotedService(t, store, &scriptedQuote{})
}

// quotedService is a service on the store with the given quote bound.
func quotedService(t *testing.T, store service.Store, quote service.ExchangeQuote) *service.Service {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(ctx)
	})
	svc, err := service.New(service.Options{Repo: store, Events: bus, Quotes: quote})
	require.NoError(t, err)

	return svc
}

// twoLines is the two-line order: the shirt's 3 × 1 000 less a discount of 199
// with 560 tax at 20% is 3 361, the book's 2 × 700 with 14 tax at 1% is 1 414.
func twoLines() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID: testRegionID, CustomerID: testCustomerID, Email: "buyer@example.com",
		CurrencyCode: testCurrency, CartID: fmt.Sprintf("cart_priced_%d", time.Now().UnixNano()),
		Subtotal: 4400, DiscountTotal: 199, TaxTotal: 574, ShippingTotal: 500, Total: 5275,
		Items: []service.CreateOrderItemInput{
			{
				VariantID: shirtVariant, Title: "Shirt", Quantity: 3, UnitPrice: 1000, Subtotal: 3000,
				DiscountTotal: 199, TaxRateBps: 2000, TaxTotal: 560, Total: 3361,
			},
			{
				VariantID: bookVariant, Title: "Book", Quantity: 2, UnitPrice: 700, Subtotal: 1400,
				TaxRateBps: 100, TaxTotal: 14, Total: 1414,
			},
		},
	}
}

// soldTwoLines places the two-line order and answers it with the shirt's and
// the book's line ids.
func soldTwoLines(ctx context.Context, t *testing.T, svc *service.Service) (order models.Order, shirt, book string) {
	t.Helper()

	return sold(ctx, t, svc, twoLines())
}

// sold places the given order and answers it with the shirt's and the book's
// line ids.
func sold(
	ctx context.Context, t *testing.T, svc *service.Service, in service.CreateOrderInput,
) (order models.Order, shirt, book string) {
	t.Helper()

	order, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	for i := range detail.Items {
		item := &detail.Items[i]
		switch item.VariantID {
		case shirtVariant:
			shirt = item.ID
		case bookVariant:
			book = item.ID
		}
	}

	return order, shirt, book
}

// shirtTakenBack opens a return of one shirt and an exchange that takes it back.
func shirtTakenBack(
	ctx context.Context, t *testing.T, svc *service.Service,
) (order models.Order, shirt, book string, ret models.Return, exchange models.Exchange) {
	t.Helper()

	order, shirt, book = soldTwoLines(ctx, t, svc)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 1}},
	})
	require.NoError(t, err)
	exchange, err = svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	return order, shirt, book, ret, exchange
}

// sends is a replacement of the exchange sending the given lines.
func sends(exchangeID string, lines ...service.ReplacementLineInput) service.CreateReplacementInput {
	return service.CreateReplacementInput{
		ExchangeID: exchangeID, ShippingOptionID: "so_standard", LocationID: "sloc_main", Lines: lines,
	}
}

// differenceOf reads an exchange's difference straight from the table.
func differenceOf(ctx context.Context, t *testing.T, exchangeID string) int64 {
	t.Helper()

	var due int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT difference_due FROM order_exchanges WHERE id = $1`, exchangeID).Scan(&due))

	return due
}

// TestAPricedExchangeDerivesItsDifferenceOnTheSchema is the record end to end:
// the exchange owes the shirt back, a shirt sent for it nets to nothing, two
// jackets add their quote, a withdrawn shirt comes out again, and the prices
// written are the prices read.
func TestAPricedExchangeDerivesItsDifferenceOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	_, shirt, _, ret, exchange := shirtTakenBack(ctx, t, svc)

	assert.Equal(t, ret.ID, exchange.ReturnID)
	assert.Equal(t, int64(-1120), differenceOf(ctx, t, exchange.ID), "it owes the shirt that comes back")

	sentShirt, err := svc.CreateReplacement(ctx, sends(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)
	assert.Zero(t, differenceOf(ctx, t, exchange.ID), "a shirt for a shirt owes nothing")

	jackets, err := svc.CreateReplacement(ctx, sends(exchange.ID,
		service.ReplacementLineInput{VariantID: jacketVariant, Quantity: 2}))
	require.NoError(t, err)
	assert.Equal(t, int64(1818), differenceOf(ctx, t, exchange.ID))

	read, err := svc.GetReplacement(ctx, jackets.ID)
	require.NoError(t, err)
	require.Len(t, read.Items, 1)
	require.NotNil(t, read.Items[0].Price, "the price is written with the item")
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 900, Total: 1818, TaxTotal: 18, TaxRateBps: 50, PricedBy: models.PricedByQuote,
		TaxComponents: []models.ReplacementItemTax{
			{RateID: "txr_base", RateBps: 50, TaxableAmount: 1800, TaxAmount: 9},
			{RateID: "txr_top", RateBps: 50, Compound: true, TaxableAmount: 1809, TaxAmount: 9},
		},
	}, *read.Items[0].Price)
	shirtRead, err := svc.GetReplacement(ctx, sentShirt.ID)
	require.NoError(t, err)
	require.NotNil(t, shirtRead.Items[0].Price)
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1000, Total: 1120, TaxTotal: 186, TaxRateBps: 2000, PricedBy: models.PricedByLine,
	}, *shirtRead.Items[0].Price)

	_, err = svc.CancelReplacement(ctx, sentShirt.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1818-1120), differenceOf(ctx, t, exchange.ID), "the withdrawn shirt comes out")

	raw, err := svc.ReturnDetailJSON(ctx, ret.ID)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"settled_by_exchange":"`+exchange.ID+`"`)

	_, err = svc.FundExchange(ctx, exchange.ID, "paycol_priced_stale_"+exchange.ID, 1818)
	require.Error(t, err)
	assert.Equal(t, service.CodeExchangeDifferenceMoved, errors.CodeOf(err))

	_, err = svc.FundExchange(ctx, exchange.ID, "paycol_priced_"+exchange.ID, 698)
	require.NoError(t, err)
	_, err = svc.CancelReplacement(ctx, jackets.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeExchangeDifferenceHeld, errors.CodeOf(err))
	assert.Equal(t, int64(698), differenceOf(ctx, t, exchange.ID))

	// The exit's order half: the money went back, the exchange is withdrawn,
	// and the jackets go after it with the figure as it stood.
	_, err = svc.WithdrawFundedExchange(ctx, exchange.ID)
	require.NoError(t, err)
	withdrawn, err := svc.CancelReplacement(ctx, jackets.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReplacementCanceled, withdrawn.Status)
	assert.Equal(t, int64(698), differenceOf(ctx, t, exchange.ID))
}

// TestAnEvenSwapSentInPiecesOwesNothingOnTheSchema prices each one-unit piece
// of a line as the units after those the exchange's live replacements already
// send, read from the table: a shirt whose total, 3 362, leaves a remainder of
// two over its units nets to nothing when its two returned units go one at a
// time (ADR 0432).
func TestAnEvenSwapSentInPiecesOwesNothingOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	in := twoLines()
	in.DiscountTotal, in.Total = 198, 5276
	in.Items[0].DiscountTotal, in.Items[0].Total = 198, 3362
	order, shirt, _ := sold(ctx, t, svc, in)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 2}},
	})
	require.NoError(t, err)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-2241), exchange.DifferenceDue)

	first, err := svc.CreateReplacement(ctx, sends(exchange.ID, service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)
	second, err := svc.CreateReplacement(ctx, sends(exchange.ID, service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)

	assert.Equal(t, int64(1120), first.Items[0].Price.Total)
	assert.Equal(t, int64(1121), second.Items[0].Price.Total)
	assert.Zero(t, differenceOf(ctx, t, exchange.ID))
}

// TestWithdrawalsPriceAnExchangesLineAgainOnTheSchema is the follow-up
// review's probe on the real tables: a shirt of 10 005 over ten units, whose
// units are worth 1 000 and 1 001 in turn, four back, single units sent and
// withdrawn. Each withdrawal prices the live pieces again from the line's first
// unit in the order they were written, so the four live units end as the
// line's first four and the swap owes nothing (ADR 0432).
func TestWithdrawalsPriceAnExchangesLineAgainOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	in := twoLines()
	in.Items[0].Quantity, in.Items[0].Subtotal, in.Items[0].DiscountTotal = 10, 10_000, 0
	in.Items[0].TaxTotal, in.Items[0].Total = 5, 10_005
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.Total = 11_400, 0, 19, 11_919
	order, shirt, _ := sold(ctx, t, svc, in)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 4}},
	})
	require.NoError(t, err)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-4002), exchange.DifferenceDue)
	one := func() service.ReplacementRecord {
		record, err := svc.CreateReplacement(ctx, sends(exchange.ID, service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
		require.NoError(t, err)
		return record
	}

	x1 := one()
	y1 := one()
	_, err = svc.CancelReplacement(ctx, x1.ID)
	require.NoError(t, err)
	y2 := one()
	assert.Equal(t, int64(1001), y2.Items[0].Price.Total,
		"the second unit, after the one live piece; counting the withdrawn one too would make it the third")
	x2 := one()
	y3 := one()
	_, err = svc.CancelReplacement(ctx, x2.ID)
	require.NoError(t, err)
	y4 := one()

	assert.Zero(t, differenceOf(ctx, t, exchange.ID), "four units for four back owe nothing")
	var totals []int64
	for _, record := range []service.ReplacementRecord{y1, y2, y3, y4} {
		read, err := svc.GetReplacement(ctx, record.ID)
		require.NoError(t, err)
		totals = append(totals, read.Items[0].Price.Total)
	}
	assert.Equal(t, []int64{1000, 1001, 1000, 1001}, totals, "the line's first four units, in the order written")

	// Two pieces left of four: the first written takes the first unit, 1 000,
	// and the second the next, 1 001. Priced in another order they would swap.
	_, err = svc.CancelReplacement(ctx, y3.ID)
	require.NoError(t, err)
	_, err = svc.CancelReplacement(ctx, y4.ID)
	require.NoError(t, err)
	first, err := svc.GetReplacement(ctx, y1.ID)
	require.NoError(t, err)
	second, err := svc.GetReplacement(ctx, y2.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), first.Items[0].Price.Total)
	assert.Equal(t, int64(1001), second.Items[0].Price.Total)
	assert.Equal(t, int64(2001-4002), differenceOf(ctx, t, exchange.ID))
}

// TestAnExchangeOnATaxInclusiveOrderIsPricedOnTheSchema prices a line and a
// variant of an order whose prices include their tax, and reads the prices
// back from the table (ADR 0246, ADR 0432).
func TestAnExchangeOnATaxInclusiveOrderIsPricedOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := quotedService(t, repository.New(testPool.Pool()), &scriptedQuote{response: `{"currency_code":"TRY",
		"prices_include_tax":true,"lines":[{"unit_price":1210,"subtotal":2017,"discount_total":0,"tax_total":403,
		"tax_rate_bps":2000,"total":2420}]}`})
	in := twoLines()
	in.PricesIncludeTax = true
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.Total = 4386, 1, 614, 5499
	in.Items[0].UnitPrice, in.Items[0].Subtotal, in.Items[0].DiscountTotal = 1200, 3000, 1
	in.Items[0].TaxTotal, in.Items[0].Total = 600, 3599
	in.Items[1].Subtotal, in.Items[1].Total = 1386, 1400
	order, shirt, _ := sold(ctx, t, svc, in)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 1}},
	})
	require.NoError(t, err)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	require.Equal(t, int64(-1199), exchange.DifferenceDue)

	record, err := svc.CreateReplacement(ctx, sends(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1},
		service.ReplacementLineInput{VariantID: jacketVariant, Quantity: 2}))
	require.NoError(t, err)

	read, err := svc.GetReplacement(ctx, record.ID)
	require.NoError(t, err)
	require.Len(t, read.Items, 2)
	prices := map[string]models.ReplacementPrice{}
	for i := range read.Items {
		prices[read.Items[i].Names()] = *read.Items[i].Price
	}
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1200, Total: 1199, TaxTotal: 200, TaxRateBps: 2000, PricedBy: models.PricedByLine,
	}, prices[shirt])
	assert.Equal(t, models.ReplacementPrice{
		UnitPrice: 1210, Total: 2420, TaxTotal: 403, TaxRateBps: 2000, PricedBy: models.PricedByQuote,
	}, prices[jacketVariant])
	assert.Equal(t, int64(2420), differenceOf(ctx, t, exchange.ID))
}

// TestAWithdrawnExchangeLetsItsReturnGoOnTheSchema reads the live exchange of a
// return on the real schema: a withdrawn one names it no more, so the return
// refunds on its own again and another exchange may take it back.
func TestAWithdrawnExchangeLetsItsReturnGoOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	order, _, _, ret, exchange := shirtTakenBack(ctx, t, svc)

	_, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.Error(t, err)
	assert.Equal(t, service.CodeExchangeReturnNamed, errors.CodeOf(err))
	_, err = svc.CancelReturn(ctx, ret.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeReturnNamedByExchange, errors.CodeOf(err))

	_, err = svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	raw, err := svc.ReturnDetailJSON(ctx, ret.ID)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "settled_by_exchange")
	again, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err, "a withdrawn exchange lets its return go")
	assert.Equal(t, int64(-1120), again.DifferenceDue)
}

// TestThePriceConstraintsAreTheLastDefence writes the price columns past the
// service: they are all set or all empty, the source is a known one that fits
// the item's shape, and the figures are in range.
func TestThePriceConstraintsAreTheLastDefence(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	order, shirt, book := soldTwoLines(ctx, t, svc)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, DifferenceDue: 100})
	require.NoError(t, err)
	record, err := svc.CreateReplacement(ctx, sends(exchange.ID,
		service.ReplacementLineInput{VariantID: "variant_FIRST", Quantity: 1}))
	require.NoError(t, err)

	insert := func(line, variant, price string) error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO order_replacement_items (id, order_replacement_id, order_line_item_id, variant_id,
                 quantity, unit_price, total, tax_total, tax_rate_bps, tax_components, priced_by)
             SELECT $1, $2, NULLIF($3, ''), NULLIF($4, ''), 1, p.unit_price, p.total, p.tax_total,
                    p.tax_rate_bps, p.tax_components, p.priced_by
             FROM (SELECT `+price+`) AS p (unit_price, total, tax_total, tax_rate_bps, tax_components, priced_by)`,
			fmt.Sprintf("oreplitem_raw_%d", time.Now().UnixNano()), record.ID, line, variant)

		return err
	}
	const (
		priced  = `1000::bigint, 1200::bigint, 200::bigint, 2000, NULL::jsonb, 'quote'`
		ofALine = `1000::bigint, 1200::bigint, 200::bigint, 2000, NULL::jsonb, 'line'`
	)

	for name, tc := range map[string]struct {
		line, variant, price, constraint string
	}{
		"a price with no source": {"", "variant_A", `1000::bigint, 1200::bigint, 200::bigint, 2000, NULL::jsonb, NULL::text`,
			"order_replacement_items_priced_together"},
		"a source with no price": {"", "variant_B", `NULL::bigint, NULL::bigint, NULL::bigint, NULL::int, NULL::jsonb, 'quote'`,
			"order_replacement_items_priced_together"},
		"a price with no total": {"", "variant_C", `1000::bigint, NULL::bigint, 200::bigint, 2000, NULL::jsonb, 'quote'`,
			"order_replacement_items_priced_together"},
		"components with no price": {"", "variant_D", `NULL::bigint, NULL::bigint, NULL::bigint, NULL::int, '[]'::jsonb, NULL::text`,
			"order_replacement_items_priced_together"},
		"an unknown source": {"", "variant_E", `1000::bigint, 1200::bigint, 200::bigint, 2000, NULL::jsonb, 'guess'`,
			"order_replacement_items_priced_by_known"},
		"a variant priced as a line": {"", "variant_F", ofALine, "order_replacement_items_priced_by_shape"},
		"a line priced by a quote":   {shirt, "", priced, "order_replacement_items_priced_by_shape"},
		"more tax than total": {"", "variant_G", `1000::bigint, 100::bigint, 200::bigint, 2000, NULL::jsonb, 'quote'`,
			"order_replacement_items_price_range"},
		"a negative unit price": {"", "variant_H", `-1::bigint, 1200::bigint, 200::bigint, 2000, NULL::jsonb, 'quote'`,
			"order_replacement_items_price_range"},
		"a rate past 100%": {"", "variant_I", `1000::bigint, 1200::bigint, 200::bigint, 10001, NULL::jsonb, 'quote'`,
			"order_replacement_items_price_range"},
		"components that are no list": {"", "variant_J", `1000::bigint, 1200::bigint, 200::bigint, 2000, '{}'::jsonb, 'quote'`,
			"order_replacement_items_tax_components_list"},
	} {
		t.Run(name, func(t *testing.T) {
			err := insert(tc.line, tc.variant, tc.price)

			require.Error(t, err)
			assert.Contains(t, err.Error(), `check constraint "`+tc.constraint+`"`)
		})
	}

	require.NoError(t, insert("", "variant_OK", priced), "a priced variant is written")
	require.NoError(t, insert(book, "", ofALine), "a line priced at its own figures is written")
	require.NoError(t, insert("", "variant_FREE",
		`NULL::bigint, NULL::bigint, NULL::bigint, NULL::int, NULL::jsonb, NULL::text`), "an unpriced item is written")
}

// TestAnExchangeNamesOneLiveReturnOfItsOwnOrder holds the return's link past the
// service: a return of another order is refused by the key, and a return a live
// exchange takes back by the index, which a withdrawn exchange leaves free.
func TestAnExchangeNamesOneLiveReturnOfItsOwnOrder(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	order, shirt, _ := soldTwoLines(ctx, t, svc)
	other, _, _ := soldTwoLines(ctx, t, svc)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 1}},
	})
	require.NoError(t, err)

	insert := func(orderID, status string) error {
		canceled := "NULL"
		if status == "canceled" {
			canceled = "now()"
		}
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO order_exchanges (id, order_id, status, order_return_id, canceled_at)
             VALUES ($1, $2, $3, $4, `+canceled+`)`,
			fmt.Sprintf("exch_raw_%d", time.Now().UnixNano()), orderID, status, ret.ID)

		return err
	}

	err = insert(other.ID, "requested")
	require.Error(t, err, "an exchange named another order's return")
	assert.Contains(t, err.Error(), `foreign key constraint "order_exchanges_return_fk"`)

	require.NoError(t, insert(order.ID, "canceled"), "a withdrawn exchange holds no return")
	require.NoError(t, insert(order.ID, "requested"))
	err = insert(order.ID, "requested")
	require.Error(t, err, "two live exchanges took one return back")
	assert.Contains(t, err.Error(), `unique constraint "order_exchanges_return_uniq"`)
}

// TestWithdrawingAReplacementDerivesWhatTheExchangeSendsAfterItsLock is the
// race the exchange's lock exists for (ADR 0432).
//
// Another writer holds the exchange's row and has written a replacement of
// two jackets it has not committed. The withdrawal of the shirt has to wait on
// the exchange's lock and derive the difference from what is there once it
// gets it — the jackets, not the shirt. Deriving before the lock reads the
// jackets as absent, and the write that follows waits on the same row and
// then puts a difference that leaves them out.
func TestWithdrawingAReplacementDerivesWhatTheExchangeSendsAfterItsLock(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	_, shirt, _, _, exchange := shirtTakenBack(ctx, t, svc)
	sentShirt, err := svc.CreateReplacement(ctx, sends(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)
	require.Zero(t, differenceOf(ctx, t, exchange.ID))

	pool, holderPID := singleConnection(ctx, t)
	holder, err := pool.Pool().Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = holder.Rollback(ctx) }()
	_, err = holder.Exec(ctx, `SELECT id FROM order_exchanges WHERE id = $1 FOR UPDATE`, exchange.ID)
	require.NoError(t, err)
	jacketsID := fmt.Sprintf("orepl_raw_%d", time.Now().UnixNano())
	_, err = holder.Exec(ctx,
		`INSERT INTO order_replacements (id, order_exchange_id, status, shipping_option_id, location_id)
         VALUES ($1, $2, 'requested', 'so_standard', 'sloc_main')`, jacketsID, exchange.ID)
	require.NoError(t, err)
	_, err = holder.Exec(ctx,
		`INSERT INTO order_replacement_items (id, order_replacement_id, variant_id, quantity,
             unit_price, total, tax_total, tax_rate_bps, priced_by)
         VALUES ($1, $2, $3, 2, 900, 1818, 18, 100, 'quote')`, jacketsID+"_item", jacketsID, jacketVariant)
	require.NoError(t, err)

	withdrawn := make(chan error, 1)
	go func() {
		_, cancelErr := svc.CancelReplacement(ctx, sentShirt.ID)
		withdrawn <- cancelErr
	}()
	require.Eventually(t, func() bool {
		waiters, waitErr := lockWaiters(ctx, holderPID)
		return waitErr == nil && waiters == 1
	}, 10*time.Second, 20*time.Millisecond, "the withdrawal has to WAIT on the row the other writer holds")

	require.NoError(t, holder.Commit(ctx))
	require.NoError(t, <-withdrawn)

	assert.Equal(t, int64(1818-1120), differenceOf(ctx, t, exchange.ID),
		"the jackets written under the lock count, and the withdrawn shirt does not")
}

// TestARollbackRefusesAnExchangeTakingAReturnBack keeps 000043's down migration
// from dropping the one sentence that stops a return being refunded beside the
// exchange that already counts its goods.
func TestARollbackRefusesAnExchangeTakingAReturnBack(t *testing.T) {
	ctx := context.Background()
	dsn, pool := isolatedDatabase(ctx, t, "order_exchange_return_rollback")
	svc, _ := newServiceWithStore(t, repository.New(pool.Pool()))
	order, shirt, _ := soldTwoLines(ctx, t, svc)
	ret, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: shirt, Quantity: 1}},
	})
	require.NoError(t, err)
	_, err = svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	err = rollBackThrough(ctx, t, dsn, 43)

	require.Error(t, err, "the rollback dropped which return an exchange takes back")
	// The server's report quotes the constraint; the bare name is also in the
	// migration's own text, which the error carries (D148).
	assert.Contains(t, err.Error(), `check constraint "order_exchanges_names_no_live_return"`)
}

// TestAnExchangeSpeaksForTheGoodsItNamesTwiceOnceOnTheSchema reads what a
// line's returns and replacements speak for from the tables: an exchange that
// takes one unit back through its return and sends the same line again speaks
// for the more of the two, while a return beside an exchange that names no
// return still adds to that exchange's replacement (ADR 0432, amending
// ADR 0423's count).
func TestAnExchangeSpeaksForTheGoodsItNamesTwiceOnceOnTheSchema(t *testing.T) {
	ctx := context.Background()
	svc := pricedService(t, repository.New(testPool.Pool()))
	spoken := func(lineID string) int64 {
		units, err := svc.SpokenForUnits(ctx, []string{lineID})
		require.NoError(t, err)
		return units[lineID]
	}

	_, shirt, _, _, exchange := shirtTakenBack(ctx, t, svc)
	require.Equal(t, int64(1), spoken(shirt), "the return asks one unit back")
	_, err := svc.CreateReplacement(ctx, sends(exchange.ID, service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)
	assert.Equal(t, int64(1), spoken(shirt), "the unit taken back and the unit sent again are the exchange's one")
	_, err = svc.CreateReplacement(ctx, sends(exchange.ID, service.ReplacementLineInput{OrderLineItemID: shirt, Quantity: 1}))
	require.NoError(t, err)
	assert.Equal(t, int64(2), spoken(shirt), "two sent for one back: the more of the two")

	order, other, _ := soldTwoLines(ctx, t, svc)
	_, err = svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: other, Quantity: 1}},
	})
	require.NoError(t, err)
	typed, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, DifferenceDue: 0})
	require.NoError(t, err)
	_, err = svc.CreateReplacement(ctx, sends(typed.ID, service.ReplacementLineInput{OrderLineItemID: other, Quantity: 1}))
	require.NoError(t, err)
	assert.Equal(t, int64(2), spoken(other), "nothing ties that return to that exchange, so the two add up")
}
