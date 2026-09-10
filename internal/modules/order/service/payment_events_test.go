package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// scriptedCatalog answers Graph with records the test wrote.
//
// It records the spec it was asked, because WHAT is asked is half of what these
// tests are about: the collection has to be the root and the order has to be
// reached backwards over the link. That the backward direction actually works
// is not provable here — a fake link service already passed once while every
// reverse expansion failed against the real one (see core/query's regression
// test) — and it is proved in internal/e2e/payment_events_test.go instead.
type scriptedCatalog struct {
	records []query.Record
	err     error
	asked   query.GraphSpec
}

// Graph hands back the scripted answer and keeps the question.
func (c *scriptedCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	c.asked = spec
	if c.err != nil {
		return nil, c.err
	}
	return c.records, nil
}

// moneyEvent produces the event the payment module publishes.
func moneyEvent(name, collectionID string) eventbus.Event {
	return eventbus.Event{
		ID:   name + ":row_1",
		Name: name,
		Data: map[string]any{"payment_collection_id": collectionID},
	}
}

// withCatalog builds a second service on the SAME fake store, this time with a
// query catalog attached.
//
// The catalog is optional on the service (an installation without the Query
// layer runs without it), so the shared newEnv leaves it out and the tests that
// need one attach it here. Sharing the store is what makes the assertion
// possible: the order is written through one handle and read back through the
// other.
func (e env) withCatalog(t *testing.T, catalog service.Catalog) *service.Service {
	t.Helper()

	svc, err := service.New(service.Options{Repo: e.store, Events: e.bus, Catalog: catalog})
	require.NoError(t, err)

	return svc
}

// placeOrder writes the standard order and returns it.
func placeOrder(t *testing.T, e env) models.Order {
	t.Helper()

	order, err := e.svc.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)

	return order
}

// TestAnEventForAnUnboundCollectionIsSilent proves that a collection no order is
// bound to costs nothing.
//
// It is not an exotic case. A cart that never became an order has a collection,
// and so does an exchange's difference (ADR 0120). Returning an error for those
// would put the bus into a retry loop over an event that will NEVER become
// actionable, and the retries would be spent on the shop's ordinary traffic.
func TestAnEventForAnUnboundCollectionIsSilent(t *testing.T) {
	e := newEnv(t)
	catalog := &scriptedCatalog{records: []query.Record{{
		"id":              "paycol_unbound",
		"captured_amount": int64(500),
		"refunded_amount": int64(0),
		// The expansion is present and EMPTY: the collection was found, the
		// link had nothing on the other side. This is the shape that must be
		// told apart from "the collection does not exist".
		"order": []query.Record{},
	}}}
	svc := e.withCatalog(t, catalog)

	err := svc.HandleMoneyMoved(context.Background(),
		moneyEvent(service.TopicPaymentCaptured, "paycol_unbound"))

	require.NoError(t, err,
		"an unbound collection is an ordinary thing; an error here would be retried forever")
	assert.Equal(t, service.EntityPaymentCollection, catalog.asked.Entity,
		"the COLLECTION has to be the root: the handler starts from one and has no order "+
			"to filter on")
	assert.Equal(t, "paycol_unbound", catalog.asked.Filters[query.IDField],
		"it must be selected by its own identifier; the collection's `reference` carries "+
			"the CART's id and would find carts and miss orders")
	require.Len(t, catalog.asked.Expand, 1)
	assert.Equal(t, service.LinkOrderPayment, catalog.asked.Expand[0].Link)
}

// TestAnEventWithoutACollectionIsRejected proves the one shape that IS an error.
//
// A malformed event is not a fact about the shop, it is a fault in whatever
// published it, and swallowing it would make the topic look healthy while every
// delivery did nothing.
func TestAnEventWithoutACollectionIsRejected(t *testing.T) {
	e := newEnv(t)
	svc := e.withCatalog(t, &scriptedCatalog{})

	err := svc.HandleMoneyMoved(context.Background(),
		eventbus.Event{ID: "x", Name: service.TopicPaymentRefunded, Data: map[string]any{}})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeMoneyEventUnusable, errors.CodeOf(err))
}

// TestAKnownCollectionReportsItsCumulativeTotals proves the handler reports what
// the COLLECTION holds rather than anything the event carried.
//
// The event names no amount at all, so there is nothing here for the handler to
// copy: the two figures can only have come from the record it read.
func TestAKnownCollectionReportsItsCumulativeTotals(t *testing.T) {
	e := newEnv(t)
	order := placeOrder(t, e)

	catalog := &scriptedCatalog{records: []query.Record{{
		"id":              "paycol_bound",
		"captured_amount": int64(6100),
		"refunded_amount": int64(1100),
		"order":           []query.Record{{"id": order.ID}},
	}}}
	svc := e.withCatalog(t, catalog)

	require.NoError(t, svc.HandleMoneyMoved(context.Background(),
		moneyEvent(service.TopicPaymentRefunded, "paycol_bound")))

	summary, err := svc.GetOrderSummary(context.Background(), order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(6100), summary.PaidTotal)
	assert.Equal(t, int64(1100), summary.RefundedTotal,
		"the figures must be the COLLECTION's; the event carried none")
}
