//go:build integration

package cart_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestTheLinesFieldOnTheRealQuery is ADR 0290's read against a real
// PostgreSQL: one read for two carts, each cart's living lines in the order
// they were written, a removed line left out and an add-on naming its line.
// The unit tests run a fake written to match the query.
func TestTheLinesFieldOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newServiceWithBus(t)
	first, second := newCart(ctx, t, svc), newCart(ctx, t, svc)

	add := func(cartID, variantID, title string, addOns ...service.AddOnInput) string {
		t.Helper()

		item, err := svc.AddLineItem(ctx, cartID, service.AddLineItemInput{
			VariantID: variantID, Title: title, Quantity: 1, UnitPrice: 1_000, AddOns: addOns,
		})
		require.NoError(t, err)
		return item.ID
	}
	ring := add(first.ID, "variant_lines_ring", "Ring",
		service.AddOnInput{VariantID: "variant_lines_wrap", Title: "Wrap", UnitPrice: 500})
	gone := add(first.ID, "variant_lines_gone", "Gone")
	add(first.ID, "variant_lines_last", "Last")
	add(second.ID, "variant_lines_other", "Other")
	require.NoError(t, svc.RemoveLineItem(ctx, first.ID, gone))

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx,
		[]string{first.ID, second.ID}, []string{query.IDField, service.FieldLines})
	require.NoError(t, err)
	require.Len(t, records, 2)

	titles := map[string][]string{}
	parents := map[string]string{}
	for _, record := range records {
		lines, ok := record[service.FieldLines].([]map[string]any)
		require.True(t, ok)
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		for _, line := range lines {
			title, _ := line[service.LineTitle].(string)
			titles[id] = append(titles[id], title)
			parents[title], _ = line[service.LineParentID].(string)
		}
	}

	assert.Equal(t, []string{"Ring", "Wrap", "Last"}, titles[first.ID],
		"in the order they were written, the removed line left out")
	assert.Equal(t, []string{"Other"}, titles[second.ID])
	assert.Equal(t, ring, parents["Wrap"], "the add-on names its line")
	assert.Empty(t, parents["Ring"])
}
