//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderLineKeepsItsProductTitle is ADR 0365 on the real schema: the
// product's title written with a line is read back from the order and from
// the line entity, its surrounding spaces trimmed, and an order placed without
// one still places, its lines naming no product.
func TestAnOrderLineKeepsItsProductTitle(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.Items[0].ProductTitle = "  Kenya AA "
	created, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.Items)
	assert.Equal(t, "Kenya AA", detail.Items[0].ProductTitle)

	records, err := service.NewLineItemQueryProvider(svc).List(ctx, query.ListOptions{
		Fields:  []string{service.FieldID, service.FieldLineItemProductTitle},
		Filters: map[string]any{service.FieldLineItemOrderID: created.ID},
	})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	assert.Equal(t, "Kenya AA", records[0][service.FieldLineItemProductTitle])

	plain, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	read, err := svc.GetOrder(ctx, plain.ID)
	require.NoError(t, err)
	assert.Empty(t, read.Items[0].ProductTitle, "an order placed without the title names no product")
}
