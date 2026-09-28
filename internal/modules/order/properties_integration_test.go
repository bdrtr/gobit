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

// TestAnOrderLineKeepsItsPropertiesOnTheRealSchema is ADR 0223 in the order
// module: a line's words and note are written, read back through the service
// and offered by the read layer, and a line without words reads none.
func TestAnOrderLineKeepsItsPropertiesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	in := validInput()
	in.Items[0].Properties = map[string]string{"Engraving": "For Anna", "Font": "Serif"}
	in.Items[0].Metadata = map[string]any{"gift_wrap": true}

	created, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	detail, err := svc.GetOrder(ctx, created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.Items)
	assert.Equal(t, map[string]string{"Engraving": "For Anna", "Font": "Serif"}, detail.Items[0].Properties)
	assert.Equal(t, map[string]any{"gift_wrap": true}, detail.Items[0].Metadata)

	records, err := service.NewLineItemQueryProvider(svc).List(ctx, query.ListOptions{
		Fields:  []string{service.FieldID, service.FieldLineItemProperties},
		Filters: map[string]any{service.FieldLineItemOrderID: created.ID},
	})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	assert.Equal(t, map[string]string{"Engraving": "For Anna", "Font": "Serif"}, records[0][service.FieldLineItemProperties])

	plain := validInput()
	other, err := svc.CreateOrder(ctx, plain)
	require.NoError(t, err)
	read, err := svc.GetOrder(ctx, other.ID)
	require.NoError(t, err)
	assert.Nil(t, read.Items[0].Properties)
}
