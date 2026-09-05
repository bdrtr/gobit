package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// secondVariantID is the variant the multi-line order sells alongside
// [testVariantID]; the two make the variant filter provable.
const secondVariantID = "variant_SECOND"

// multiLineInput produces a consistent order of THREE lines.
//
// Two of them sell the SAME variant. That is not decoration: an order may carry
// a variant twice (migration 000001 states why there is no uniqueness rule on
// (order_id, variant_id)), and a variant filter that returned one line per
// order would undercount every sale of that shape.
//
// The numbers add up the way the service demands: line subtotal = unit price x
// quantity, the order subtotal is the sum of the line subtotals (3000 + 1000 +
// 1000 = 5000) and the order total is 5000 + 1000 tax + 2500 shipping = 8500.
func multiLineInput() service.CreateOrderInput {
	in := validInput()
	in.Subtotal = 5000
	in.TaxTotal = 1000
	in.Total = 8500
	in.Items = []service.CreateOrderItemInput{
		{
			VariantID: testVariantID, Title: "Red T-Shirt",
			Quantity: 3, UnitPrice: 1000, Subtotal: 3000,
			TaxRateBps: 2000, TaxTotal: 600, Total: 3600,
		},
		{
			VariantID: secondVariantID, Title: "Blue Cap",
			Quantity: 2, UnitPrice: 500, Subtotal: 1000,
			TaxRateBps: 2000, TaxTotal: 200, Total: 1200,
		},
		{
			VariantID: testVariantID, Title: "Red T-Shirt",
			Quantity: 1, UnitPrice: 1000, Subtotal: 1000,
			TaxRateBps: 2000, TaxTotal: 200, Total: 1200,
		},
	}

	return in
}

// lineIDs reads the identifiers out of a record set, in the order they came.
func lineIDs(t *testing.T, records []query.Record) []string {
	t.Helper()

	out := make([]string, 0, len(records))
	for _, record := range records {
		id, ok := record[service.FieldID].(string)
		require.True(t, ok, "every record has to carry a text id: %v", record)
		out = append(out, id)
	}

	return out
}

// TestLineItemQueryProviderEntityName validates that the provider matches the
// name it is registered under.
//
// Query looks a provider up as "<entity>.query" and verifies that Entity()
// agrees (ADR 0004); the two drifting apart would be a NotFound at run time,
// long after the module came up cleanly.
func TestLineItemQueryProviderEntityName(t *testing.T) {
	e := newEnv(t)

	assert.Equal(t, service.LineItemEntity, service.NewLineItemQueryProvider(e.svc).Entity())
	assert.Equal(t, "order_line_item", service.LineItemEntity)
	assert.Equal(t, "order_line_item.query", service.LineItemProviderName)
}

// TestLineItemQueryProviderProducesTheRequestedFields validates that exactly
// the requested fields are produced, with the values of the line.
func TestLineItemQueryProviderProducesTheRequestedFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Fields: []string{
			service.FieldID, service.FieldLineItemOrderID, service.FieldLineItemVariantID,
			service.FieldLineItemQuantity, service.FieldLineItemTotal,
		},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	assert.Equal(t, query.Record{
		service.FieldID:                detail.Items[0].ID,
		service.FieldLineItemOrderID:   order.ID,
		service.FieldLineItemVariantID: testVariantID,
		service.FieldLineItemQuantity:  int64(3),
		service.FieldLineItemTotal:     int64(3600),
	}, records[0])
}

// TestLineItemQueryProviderARequestWithoutFieldsReturnsAllFields validates the
// default field set, and that METADATA is not part of it.
//
// The metadata is the caller's free-form bag: the module writes it and never
// reads it, so it has no shape it could promise. Its absence is asserted here
// because adding it would be a one-line change that nothing else would catch,
// and the first report reading a key out of it turns that key into a contract
// nobody agreed to.
func TestLineItemQueryProviderARequestWithoutFieldsReturnsAllFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	_, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)

	for _, field := range []string{
		service.FieldID, service.FieldLineItemOrderID, service.FieldLineItemVariantID,
		service.FieldLineItemTitle, service.FieldLineItemQuantity, service.FieldLineItemUnitPrice,
		service.FieldLineItemSubtotal, service.FieldLineItemDiscountTotal,
		service.FieldLineItemTaxTotal, service.FieldLineItemTaxRateBps,
		service.FieldLineItemTotal, service.FieldLineItemCreatedAt,
	} {
		assert.Contains(t, records[0], field)
	}
	assert.NotContains(t, records[0], "metadata",
		"the free-form bag is deliberately not offered; see lineItemFieldGetters")
	assert.Len(t, records[0], 12, "the offered set is exactly the fields listed above")
}

// TestLineItemQueryProviderRejectsAnUndefinedField validates that a field that
// is not offered is refused rather than answered with a zero value (ADR 0004).
//
// A consumer cannot tell a zero it asked for from a zero meaning "no such
// field", which is why the refusal has to happen on BOTH paths — the listing
// and the expansion.
func TestLineItemQueryProviderRejectsAnUndefinedField(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	for _, field := range []string{"metadata", "updated_at", "placed_at", "customer_id"} {
		_, err := p.List(ctx, query.ListOptions{Fields: []string{field}})
		require.Error(t, err, "the field %q is not offered", field)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

		_, err = p.FetchByIDs(ctx, []string{"oli_1"}, []string{field})
		require.Error(t, err, "the field %q is not offered on the expansion path either", field)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestLineItemQueryProviderFilters covers the order and variant filters and the
// refusal of everything else.
func TestLineItemQueryProviderFilters(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	first, err := e.svc.CreateOrder(ctx, multiLineInput())
	require.NoError(t, err)
	_, err = e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldLineItemOrderID: first.ID},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 3, "the order filter has to select that order's lines only")

	// Two lines of the first order and one of the second sell this variant.
	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldLineItemVariantID: testVariantID},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 3, "a variant sold twice on one order has to be counted twice")

	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{
			service.FieldLineItemOrderID:   first.ID,
			service.FieldLineItemVariantID: secondVariantID,
		},
		Fields: []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1, "the filters have to apply together, not one of them")

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldLineItemVariantID: 42},
	})
	require.Error(t, err, "a filter with the wrong type has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{"title": "Red T-Shirt"},
	})
	require.Error(t, err, "an unoffered filter has to be rejected even when it names a real field")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemQueryProviderDateRangeSelectsOnTheOrdersPlacedAt is the test of
// the capability the entity was built for: "which variants sold in a period".
//
// The bound is the ORDER's placed_at, and the interval is half-open [from, to).
// Both halves are asserted on the boundary itself, because that is where the
// two mistakes live: an exclusive lower bound loses the sale placed exactly at
// the start of the period, and an inclusive upper bound counts the sale at
// midnight in two consecutive months at once.
func TestLineItemQueryProviderDateRangeSelectsOnTheOrdersPlacedAt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	older, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	newer, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.True(t, older.PlacedAt.Before(newer.PlacedAt),
		"the fake store has to stamp the two orders apart for this test to mean anything")

	records, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FilterPlacedFrom: newer.PlacedAt},
		Fields:  []string{service.FieldLineItemOrderID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "the lower bound is INCLUSIVE: the order placed at that instant counts")
	assert.Equal(t, newer.ID, records[0][service.FieldLineItemOrderID])

	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FilterPlacedTo: newer.PlacedAt},
		Fields:  []string{service.FieldLineItemOrderID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "the upper bound is EXCLUSIVE: the order placed at that instant is out")
	assert.Equal(t, older.ID, records[0][service.FieldLineItemOrderID])

	// The same range, spelled the way a query definition that arrived as JSON
	// spells it. A caller in the process holds a time.Time; one coming through
	// the API can only carry text, and both doors have to lead to one answer.
	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{
			service.FilterPlacedFrom: older.PlacedAt.Format(time.RFC3339Nano),
			service.FilterPlacedTo:   newer.PlacedAt.Format(time.RFC3339Nano),
		},
		Fields: []string{service.FieldLineItemOrderID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, older.ID, records[0][service.FieldLineItemOrderID])

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FilterPlacedFrom: "01/09/2026"},
	})
	require.Error(t, err, "a date that is not RFC 3339 has to be rejected, not read as a zero time")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FilterPlacedTo: 1757000000},
	})
	require.Error(t, err, "a unix stamp is not accepted; the unit would be a guess")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemQueryProviderRejectsAReversedDateRange validates that a range
// that cannot match is an ERROR rather than an empty page.
//
// An empty answer would read as "nothing sold in this period", which is exactly
// what a report would print — and a real zero looks the same. The swapped
// arguments have to be refused where they are still visible as arguments.
func TestLineItemQueryProviderRejectsAReversedDateRange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	_, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	start := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{
			service.FilterPlacedFrom: start,
			service.FilterPlacedTo:   start.Add(-24 * time.Hour),
		},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	// The empty interval [t, t) is refused too: it selects nothing by
	// construction, and a caller asking for it meant a day, not an instant.
	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{
			service.FilterPlacedFrom: start,
			service.FilterPlacedTo:   start,
		},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemQueryProviderFetchByIDsReadsSeveralLinesInOneCall is the proof
// that the expansion path is a BATCH.
//
// The record set alone cannot tell a batch from a per-id loop — both return the
// same lines — so the store's call count is asserted as well. That count is the
// whole point: an expansion over a hundred orders has to cost one query, which
// is the N+1 the read layer exists to make impossible.
func TestLineItemQueryProviderFetchByIDsReadsSeveralLinesInOneCall(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, multiLineInput())
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 3)

	ids := []string{detail.Items[0].ID, detail.Items[1].ID, detail.Items[2].ID, "oli_MISSING"}
	records, err := p.FetchByIDs(ctx, ids, []string{service.FieldID, service.FieldLineItemVariantID})
	require.NoError(t, err)

	require.Len(t, records, 3, "an identifier that is not found returns no record and is not an error")
	assert.ElementsMatch(t,
		[]string{detail.Items[0].ID, detail.Items[1].ID, detail.Items[2].ID},
		lineIDs(t, records))
	assert.Equal(t, 1, e.store.lineItemBatchReads,
		"three lines have to cost ONE store read, not one per identifier")

	empty, err := p.FetchByIDs(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.Equal(t, 1, e.store.lineItemBatchReads,
		"an empty id set must not reach the store at all")
}

// TestLineItemQueryProviderSelectsLinesByID covers the identity filter on the
// root path.
//
// FetchByIDs is the EXPANSION path; a caller holding line identifiers and
// running a root query needs the filter, and the order provider offers exactly
// this — an entity that did not would be an inconsistency found by getting a
// 422. Combining it with another filter is refused rather than intersected:
// "these ids, but only the ones from last month" is a question with two
// answers, and quietly picking one of them is worse than saying no.
func TestLineItemQueryProviderSelectsLinesByID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, multiLineInput())
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldID},
		Filters: map[string]any{service.FieldID: []any{detail.Items[0].ID, detail.Items[2].ID}},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{detail.Items[0].ID, detail.Items[2].ID}, lineIDs(t, records))

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{
			service.FieldID:              detail.Items[0].ID,
			service.FieldLineItemOrderID: order.ID,
		},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemQueryProviderHidesTheLinesOfADeletedOrder validates that the
// order's liveness reaches the lines.
//
// The listing joins orders and checks their deleted_at, and so does the batch
// read; the two answering differently would make the same line exist or not
// depending on which side of a query it was reached from. There is no surface
// that deletes an order today, which is precisely why the condition needs a
// test: nothing else would notice if it were dropped.
func TestLineItemQueryProviderHidesTheLinesOfADeletedOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, multiLineInput())
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 3)

	e.store.softDeleteOrder(order.ID)

	records, err := p.List(ctx, query.ListOptions{Fields: []string{service.FieldID}})
	require.NoError(t, err)
	assert.Empty(t, records, "the lines of a deleted order are not sales")

	records, err = p.FetchByIDs(ctx, []string{detail.Items[0].ID}, []string{service.FieldID})
	require.NoError(t, err)
	assert.Empty(t, records, "the expansion has to hide what the listing hides")
}

// TestLineItemQueryProviderClampsAnUnlimitedRequest validates that the core's
// "0 means unlimited" contract is brought down to the provider's ceiling.
//
// The line table is the module's largest — it grows with every line of every
// order — so an unlimited root query would pull the whole sales history into
// memory. The request is CLAMPED rather than rejected: the caller said "all of
// them" and gets the most it can get.
func TestLineItemQueryProviderClampsAnUnlimitedRequest(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewLineItemQueryProvider(e.svc)

	_, err := e.svc.CreateOrder(ctx, multiLineInput())
	require.NoError(t, err)

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1000} {
		records, err := p.List(ctx, query.ListOptions{Limit: limit, Fields: []string{service.FieldID}})
		require.NoError(t, err, "limit=%d must not be rejected", limit)
		assert.Len(t, records, 3)
	}
}

// TestLineItemListingPagesNewestSaleFirst validates the ordering and the
// pagination window of the service call the provider runs on.
//
// The order is the sale's moment descending, which is what a report asking for
// "the last period" reads top down; the paging has to be able to walk it
// without repeating or skipping a line.
func TestLineItemListingPagesNewestSaleFirst(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	older, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	newer, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	first, err := e.svc.ListLineItems(ctx, service.ListLineItemsInput{Page: service.Page{Limit: 1}})
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.Equal(t, newer.ID, first[0].OrderID, "the newest sale comes first")

	second, err := e.svc.ListLineItems(ctx,
		service.ListLineItemsInput{Page: service.Page{Limit: 1, Offset: 1}})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, older.ID, second[0].OrderID)

	past, err := e.svc.ListLineItems(ctx,
		service.ListLineItemsInput{Page: service.Page{Limit: 10, Offset: 50}})
	require.NoError(t, err)
	assert.Empty(t, past, "a page beyond the end is empty, not an error")
}

// TestLineItemListingRefusesACursor validates that the keyset cursor of the
// order listing is refused here instead of being ignored.
//
// [service.Page] carries one because the order listing pages that way, and this
// listing orders by a column the line does not hold. Dropping the cursor
// silently would serve the FIRST page to every request that sent one — which a
// caller reads as "the data stopped changing" rather than as an error.
func TestLineItemListingRefusesACursor(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.ListLineItems(ctx, service.ListLineItemsInput{
		Page: service.Page{Limit: 10, After: corepage.Cursor{Time: time.Now().UTC(), ID: "oli_1"}},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemListingValidatesItsIdentifiers validates that an identifier
// criterion is rejected the way every other identifier in the module is.
//
// The filter is not trimmed and not tidied: an identifier that differs from the
// stored one by a space selects nothing, and the difference only becomes
// visible after a report has already been read.
func TestLineItemListingValidatesItsIdentifiers(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	blank := " "
	_, err := e.svc.ListLineItems(ctx, service.ListLineItemsInput{OrderID: &blank})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	padded := " " + testVariantID
	_, err = e.svc.ListLineItems(ctx, service.ListLineItemsInput{VariantID: &padded})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestLineItemStoreFilterMatchesTheQuery pins the fake store's filter to the
// one the SQL applies.
//
// The fake is what every unit test above runs on, so a fake that filtered on
// the LINE's created_at instead of the ORDER's placed_at would let all of them
// pass over behavior the database does not have. The two stamps are equal for
// a line written together with its order, which is why the divergence needs a
// line whose row was written LATER than the order it belongs to — the shape an
// exchange produces.
func TestLineItemStoreFilterMatchesTheQuery(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	// A line written after the order, carrying its own later created_at while
	// belonging to the sale that already happened.
	late, err := e.store.CreateLineItem(ctx, models.OrderLineItem{
		ID: "oli_LATE", OrderID: order.ID, VariantID: secondVariantID,
		Title: "Exchanged Cap", Quantity: 1, UnitPrice: 500,
		Subtotal: 500, TaxTotal: 100, Total: 600,
	})
	require.NoError(t, err)
	require.True(t, order.PlacedAt.Before(late.CreatedAt),
		"the added line has to be stamped later than the order for this test to mean anything")

	lines, err := e.svc.ListLineItems(ctx, service.ListLineItemsInput{
		PlacedTo: ptr(late.CreatedAt),
	})
	require.NoError(t, err)
	assert.Len(t, lines, 2,
		"both lines belong to a sale placed before that instant; a filter on the line's own "+
			"created_at would have dropped the later one")
}

// ptr returns a pointer to the value; the filter fields are pointers because
// nil is "criterion not given".
func ptr[T any](v T) *T { return &v }
