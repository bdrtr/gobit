package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// An exchange that names its return is one act after the sale, documented as
// a return and a sale (ADR 0432): the units its return takes back, and the
// items its live replacements send at the prices recorded when they were
// written, documentable once every one of those has left.

// entityCatalog answers each entity its own records and keeps the questions.
type entityCatalog struct {
	records map[string][]query.Record
	asked   []query.GraphSpec
}

// Graph hands back the entity's records.
func (c *entityCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	c.asked = append(c.asked, spec)
	return c.records[spec.Entity], nil
}

// exchangeActOf finds the order's exchange act, failing when it has none.
func exchangeActOf(t *testing.T, svc *service.Service, orderID, exchangeID string) (service.AfterSaleAct, bool) {
	t.Helper()

	acts, err := svc.AfterSaleActs(context.Background(), orderID)
	require.NoError(t, err)
	for i := range acts {
		if acts[i].Kind == models.JournalExchange && acts[i].ID == exchangeID {
			return acts[i], true
		}
	}

	return service.AfterSaleAct{}, false
}

// dispatched sends a replacement's goods, each line held first.
func dispatched(t *testing.T, e env, record service.ReplacementRecord) {
	t.Helper()

	ctx := context.Background()
	for i := range record.Items {
		variant := record.Items[i].VariantID
		if variant == "" {
			variant = pricedVariantA
		}
		require.NoError(t, e.svc.RecordReplacementReservation(ctx, record.ID, record.Items[i].ID, variant,
			"invres_"+record.Items[i].ID))
	}
	_, err := e.svc.MarkReplacementDispatched(ctx, record.ID, "ful_"+record.ID)
	require.NoError(t, err)
}

// TestAnExchangeThatNamesItsReturnIsOneAct lists the exchange with the unit its
// return takes back and, in the order they were written, a unit of line A at
// its share and two jackets at their quote; it is documentable only once both
// replacements have left and the shirt has come back.
func TestAnExchangeThatNamesItsReturnIsOneAct(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	act, listed := exchangeActOf(t, e.svc, order.ID, exchange.ID)
	require.True(t, listed, "an exchange that names its return is an act from the moment it is opened")
	assert.False(t, act.Documentable, "nothing has left")
	assert.Equal(t, []service.ReturnedUnits{{LineID: lineA, Quantity: 1}}, act.Returned)
	assert.Empty(t, act.Sent)
	assert.Equal(t, exchange.CreatedAt.UTC(), act.OccurredAt)

	shirt, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	jackets, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)

	act, _ = exchangeActOf(t, e.svc, order.ID, exchange.ID)
	require.Len(t, act.Sent, 2)
	assert.Equal(t, service.SentItem{
		LineID: lineA, Title: "Shirt", Quantity: 1,
		Price: models.ReplacementPrice{UnitPrice: 1000, Total: 1120, TaxTotal: 186, TaxRateBps: 2000, PricedBy: models.PricedByLine},
	}, act.Sent[0], "the line's first unit at its share")
	assert.Equal(t, service.SentItem{
		VariantID: quotedVariant, Quantity: 2,
		Price: models.ReplacementPrice{UnitPrice: 900, Total: 1818, TaxTotal: 18, TaxRateBps: 100, PricedBy: models.PricedByQuote},
	}, act.Sent[1], "the jackets at their quote, unnamed in a list")
	assert.Equal(t, int64(1120+1818), act.Amount, "what it sends")
	assert.False(t, act.Documentable)

	dispatched(t, e.env, shirt)
	act, _ = exchangeActOf(t, e.svc, order.ID, exchange.ID)
	assert.False(t, act.Documentable, "the jackets are still on the shelf")
	dispatched(t, e.env, jackets)
	_, err = e.svc.ReceiveReturn(ctx, ret.ID, testLocationID)
	require.NoError(t, err)
	act, _ = exchangeActOf(t, e.svc, order.ID, exchange.ID)
	assert.True(t, act.Documentable, "the shirt came back and every replacement has left")

	read, err := e.svc.AfterSaleAct(ctx, order.ID, models.JournalExchange, exchange.ID)
	require.NoError(t, err)
	assert.Equal(t, act, read, "without a catalog the variant is left unnamed")
}

// TestAWithdrawnReplacementSendsNothingOnTheAct: a replacement withdrawn while
// the exchange is requested leaves the act, and the act is documentable once
// what is left has gone and the return has come back.
func TestAWithdrawnReplacementSendsNothingOnTheAct(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	shirt, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	jackets, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	_, err = e.svc.CancelReplacement(ctx, jackets.ID)
	require.NoError(t, err)
	dispatched(t, e.env, shirt)
	_, err = e.svc.ReceiveReturn(ctx, ret.ID, testLocationID)
	require.NoError(t, err)

	act, _ := exchangeActOf(t, e.svc, order.ID, exchange.ID)
	require.Len(t, act.Sent, 1)
	assert.Equal(t, lineA, act.Sent[0].LineID)
	assert.Equal(t, int64(1120), act.Amount)
	assert.True(t, act.Documentable, "the one live replacement has left")
}

// TestOnlyAnExchangeThatNamesItsReturnIsAnAct: an exchange written without a
// return prices nothing and is no act, and a withdrawn one is listed as
// withdrawn and documented no more.
func TestOnlyAnExchangeThatNamesItsReturnIsAnAct(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, _, _, ret := pricedSale(t, e.env, 1)
	typed, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, DifferenceDue: 500})
	require.NoError(t, err)
	priced, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)

	_, listed := exchangeActOf(t, e.svc, order.ID, typed.ID)
	assert.False(t, listed, "an exchange that names no return is on no document")
	_, listed = exchangeActOf(t, e.svc, order.ID, priced.ID)
	assert.True(t, listed)

	_, err = e.svc.CancelExchange(ctx, priced.ID)
	require.NoError(t, err)
	withdrawn, listed := exchangeActOf(t, e.svc, order.ID, priced.ID)
	require.True(t, listed, "a withdrawn exchange is listed, so documents issued before can be seen")
	assert.True(t, withdrawn.Withdrawn)
	assert.False(t, withdrawn.Documentable, "a withdrawn exchange is documented no more")

	raw, err := service.NewInterop(e.svc).AfterSaleActJSON(ctx, order.ID, "exchange", priced.ID)
	require.NoError(t, err)
	var wire struct {
		Withdrawn    bool `json:"withdrawn"`
		Documentable bool `json:"documentable"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.True(t, wire.Withdrawn, "the flow is told")
}

// TestAnExchangeReadAloneNamesTheVariantsItSends: the act read on its own asks
// the catalog for the variant's title and its product's, and the list does
// not ask; the interop surface carries the items with their figures.
func TestAnExchangeReadAloneNamesTheVariantsItSends(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	catalog := &entityCatalog{records: map[string][]query.Record{
		service.CatalogEntityVariant: {{
			query.IDField: quotedVariant, service.CatalogFieldTitle: "L", service.CatalogFieldProductID: "prod_JACKET",
		}},
		service.CatalogEntityProduct: {{query.IDField: "prod_JACKET", service.CatalogFieldTitle: "Jacket"}},
	}}
	svc, err := service.New(service.Options{Repo: e.store, Events: e.bus, Quotes: e.quote, Catalog: catalog})
	require.NoError(t, err)
	e.svc = svc
	order, _, _, ret := pricedSale(t, e.env, 1)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	_, err = svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{VariantID: quotedVariant, Quantity: 2}))
	require.NoError(t, err)
	catalog.asked = nil

	listed, _ := exchangeActOf(t, svc, order.ID, exchange.ID)
	assert.Empty(t, listed.Sent[0].Title)
	assert.Empty(t, catalog.asked, "a list names no variant")

	read, err := svc.AfterSaleAct(ctx, order.ID, models.JournalExchange, exchange.ID)
	require.NoError(t, err)
	require.Len(t, read.Sent, 1)
	assert.Equal(t, "L", read.Sent[0].Title)
	assert.Equal(t, "Jacket", read.Sent[0].ProductTitle)
	require.Len(t, catalog.asked, 2)
	assert.Equal(t, map[string]any{service.CatalogFilterIDs: []string{quotedVariant}}, catalog.asked[0].Filters)
	assert.Equal(t, service.CatalogEntityProduct, catalog.asked[1].Entity)
	assert.Equal(t, map[string]any{service.CatalogFilterIDs: []string{"prod_JACKET"}}, catalog.asked[1].Filters)

	raw, err := service.NewInterop(svc).AfterSaleActJSON(ctx, order.ID, "exchange", exchange.ID)
	require.NoError(t, err)
	var wire struct {
		Kind     string `json:"kind"`
		Returned []struct {
			Quantity int64 `json:"quantity"`
		} `json:"returned"`
		Sent []map[string]any `json:"sent"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "exchange", wire.Kind)
	require.Len(t, wire.Returned, 1)
	require.Len(t, wire.Sent, 1)
	assert.Equal(t, map[string]any{
		"variant_id": quotedVariant, "title": "L", "product_title": "Jacket", "quantity": float64(2),
		"unit_price": float64(900), "total": float64(1818), "tax_total": float64(18), "tax_rate_bps": float64(100),
		"tax_components": nil,
	}, wire.Sent[0])
}

// TestAnExchangeIsDocumentableOnceItsReturnHasComeBack: every replacement sent
// is not enough while the return it names is still requested; the act turns
// documentable when the goods have come back too.
func TestAnExchangeIsDocumentableOnceItsReturnHasComeBack(t *testing.T) {
	ctx := context.Background()
	e := newPricedEnv(t, jacketQuote)
	order, lineA, _, ret := pricedSale(t, e.env, 1)
	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
	require.NoError(t, err)
	shirt, err := e.svc.CreateReplacement(ctx, sendingFor(exchange.ID,
		service.ReplacementLineInput{OrderLineItemID: lineA, Quantity: 1}))
	require.NoError(t, err)
	dispatched(t, e.env, shirt)

	act, _ := exchangeActOf(t, e.svc, order.ID, exchange.ID)
	assert.False(t, act.Documentable, "the shirt it takes back has not come back")

	_, err = e.svc.ReceiveReturn(ctx, ret.ID, testLocationID)
	require.NoError(t, err)
	act, _ = exchangeActOf(t, e.svc, order.ID, exchange.ID)
	assert.True(t, act.Documentable, "goods have moved both ways")

	// No route withdraws an exchange whose goods have all moved; written so on
	// the store, it is still documented no more.
	e.store.mu.Lock()
	withdrawn := e.store.exchanges[exchange.ID]
	withdrawn.Status = models.ExchangeCanceled
	e.store.exchanges[exchange.ID] = withdrawn
	e.store.mu.Unlock()
	act, _ = exchangeActOf(t, e.svc, order.ID, exchange.ID)
	assert.True(t, act.Withdrawn)
	assert.False(t, act.Documentable, "a withdrawn exchange is documented no more")
}
