//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// costLine is one order line of the margin fixtures: its quantity, unit price,
// discount and tax, whether it sold gift cards, and its unit cost, nil for
// none. The subtotal is derived as the order module checks it.
type costLine struct {
	quantity, unitPrice, discount, tax int64
	giftcard                           bool
	cost                               *int64
}

// costOf takes a cost's address inline.
func costOf(v int64) *int64 { return &v }

// placeCosted places an order of the given lines through the service and
// returns its id; the order's amounts are the lines' sums.
func placeCosted(t *testing.T, svc *service.Service, pricesIncludeTax bool, lines ...costLine) string {
	t.Helper()
	in := service.CreateOrderInput{
		RegionID: testRegionID, CustomerID: testCustomerID, Email: "customer@example.com",
		CurrencyCode: testCurrency, PricesIncludeTax: pricesIncludeTax,
	}
	for i, l := range lines {
		subtotal := l.unitPrice * l.quantity
		if pricesIncludeTax {
			subtotal -= l.tax
		}
		total := subtotal - l.discount + l.tax
		in.Items = append(in.Items, service.CreateOrderItemInput{
			VariantID: "variant_cost_" + string(rune('a'+i)), Title: "Line",
			Quantity: l.quantity, UnitPrice: l.unitPrice, Subtotal: subtotal,
			DiscountTotal: l.discount, TaxTotal: l.tax, Total: total,
			IsGiftcard: l.giftcard, UnitCost: l.cost,
		})
		in.Subtotal += subtotal
		in.DiscountTotal += l.discount
		in.TaxTotal += l.tax
		in.Total += total
	}
	order, err := svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)
	return order.ID
}

// TestAnOrderLineKeepsItsUnitCost is ADR 0401 on the real schema: a cost, a
// cost of zero and none round-trip as written, and the column refuses a cost
// outside a unit amount's range, naming its constraint.
func TestAnOrderLineKeepsItsUnitCost(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	id := placeCosted(t, svc, false,
		costLine{quantity: 1, unitPrice: 1000, cost: costOf(400)},
		costLine{quantity: 1, unitPrice: 200, cost: costOf(0)},
		costLine{quantity: 1, unitPrice: 100},
	)
	detail, err := svc.GetOrder(ctx, id)
	require.NoError(t, err)
	require.Len(t, detail.Items, 3)
	byVariant := map[string]*int64{}
	for _, line := range detail.Items {
		byVariant[line.VariantID] = line.UnitCost
	}
	require.NotNil(t, byVariant["variant_cost_a"])
	assert.Equal(t, int64(400), *byVariant["variant_cost_a"])
	require.NotNil(t, byVariant["variant_cost_b"], "a cost of zero is not NULL")
	assert.Equal(t, int64(0), *byVariant["variant_cost_b"])
	assert.Nil(t, byVariant["variant_cost_c"], "no cost is NULL, not zero")

	insert := `INSERT INTO order_line_items (id, order_id, variant_id, title, quantity, unit_cost)
		VALUES ($1, $2, 'variant_raw', 'Raw', 1, $3)`
	_, err = testPool.Pool().Exec(ctx, insert, "oli_cost_bound_"+id, id, models.MaxAmount)
	require.NoError(t, err, "the bound is a cost")
	for name, cost := range map[string]int64{"negative": -1, "past the bound": models.MaxAmount + 1} {
		_, err := testPool.Pool().Exec(ctx, insert, "oli_cost_"+id+"_"+name, id, cost)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, name)
		assert.Equal(t, "order_line_items_unit_cost_range", pgErr.ConstraintName, name)
	}
}

// TestThePlacedMarginIsReadFromTheLinesAsSold holds queries/margin.sql, the one
// definition of the placed margin (ADR 0401), with every order of the fixture
// read in one call.
func TestThePlacedMarginIsReadFromTheLinesAsSold(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	// Discounts, quantities above one and two different costs: sales are the
	// subtotals less the discounts, the cost each unit cost times its quantity.
	discounted := placeCosted(t, svc, false,
		costLine{quantity: 3, unitPrice: 1000, discount: 300, tax: 540, cost: costOf(400)},
		costLine{quantity: 2, unitPrice: 500, discount: 100, tax: 180, cost: costOf(150)},
	)
	// Prices that include their tax: the sales are the net subtotal, not the
	// sticker times the quantity and not the line's total.
	inclusive := placeCosted(t, svc, true,
		costLine{quantity: 2, unitPrice: 1200, tax: 400, cost: costOf(700)},
	)
	// A gift card line has no cost and is no goods: the margin is the other
	// line's.
	withCard := placeCosted(t, svc, false,
		costLine{quantity: 1, unitPrice: 1000, cost: costOf(600)},
		costLine{quantity: 1, unitPrice: 5000, giftcard: true},
	)
	uncosted := placeCosted(t, svc, false,
		costLine{quantity: 1, unitPrice: 1000, cost: costOf(300)},
		costLine{quantity: 1, unitPrice: 500},
	)
	free := placeCosted(t, svc, false, costLine{quantity: 2, unitPrice: 100, cost: costOf(0)})
	cardsOnly := placeCosted(t, svc, false, costLine{quantity: 1, unitPrice: 5000, giftcard: true})
	loss := placeCosted(t, svc, false, costLine{quantity: 1, unitPrice: 100, cost: costOf(250)})
	// A line of a million units at the bound costs 10^18, an order total's
	// bound itself, which is a cost. Two cost 2 x 10^18, past the bound but
	// inside an int64; ten cost 10^19, past both. Past the bound the order
	// states no cost rather than failing.
	atBound := costLine{quantity: models.MaxQuantity, unitPrice: 0, cost: costOf(models.MaxAmount)}
	bound := placeCosted(t, svc, false, atBound)
	pastBound := placeCosted(t, svc, false, atBound, atBound)
	var huge []costLine
	for range 10 {
		huge = append(huge, atBound)
	}
	overflowing := placeCosted(t, svc, false, huge...)

	margins, err := svc.PlacedMargins(ctx, []string{
		discounted, inclusive, withCard, uncosted, free, cardsOnly, loss, bound, pastBound, overflowing,
	})
	require.NoError(t, err)

	want := map[string]struct {
		sales, lines int64
		cost, margin *int64
	}{
		discounted:  {sales: 3600, cost: costOf(1500), margin: costOf(2100)},
		inclusive:   {sales: 2000, cost: costOf(1400), margin: costOf(600)},
		withCard:    {sales: 1000, cost: costOf(600), margin: costOf(400)},
		uncosted:    {sales: 1500, lines: 1},
		free:        {sales: 200, cost: costOf(0), margin: costOf(200)},
		loss:        {sales: 100, cost: costOf(250), margin: costOf(-150)},
		bound:       {sales: 0, cost: costOf(models.MaxTotal), margin: costOf(-models.MaxTotal)},
		pastBound:   {sales: 0},
		overflowing: {sales: 0},
	}
	for id, w := range want {
		got, ok := margins[id]
		require.True(t, ok, "order %s has a margin entry", id)
		assert.Equal(t, w.sales, got.Sales, "sales of %s", id)
		assert.Equal(t, w.lines, got.LinesWithoutCost, "uncosted lines of %s", id)
		assert.Equal(t, w.cost, got.Cost, "cost of %s", id)
		assert.Equal(t, w.margin, got.Margin, "margin of %s", id)
	}
	assert.NotContains(t, margins, cardsOnly, "an order of gift cards alone has no margin")
	assert.Len(t, margins, len(want))
}
