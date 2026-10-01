package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderNamesTheOperatorWhoPlacedIt follows the operator from the
// checkout's wire name to the read layer's field and filter (ADR 0298). The
// shoppers' orders outnumber the operator's, so a filter read the wrong way
// round would not list the same orders.
func TestAnOrderNamesTheOperatorWhoPlacedIt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &body))
	body["placed_by"] = "usr_operator"
	body["idempotency_key"] = "wf_PLACED_BY"
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	byOperator, err := interop.PlaceOrderJSON(ctx, raw)
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, byOperator)
	require.NoError(t, err)
	assert.Equal(t, "usr_operator", detail.PlacedBy)

	var byShoppers []string
	for range 2 {
		order, err := e.svc.CreateOrder(ctx, validInput())
		require.NoError(t, err)
		assert.Empty(t, order.PlacedBy)
		byShoppers = append(byShoppers, order.ID)
	}

	provider := service.NewQueryProvider(e.svc)
	for _, tc := range []struct {
		flag bool
		want []string
	}{
		{flag: true, want: []string{byOperator}},
		{flag: false, want: byShoppers},
	} {
		records, err := provider.List(ctx, query.ListOptions{
			Fields:  []string{query.IDField, service.FieldPlacedBy},
			Filters: map[string]any{service.FilterPlacedByOperator: tc.flag},
		})
		require.NoError(t, err)
		var listed []string
		for _, record := range records {
			id, _ := record[query.IDField].(string)
			listed = append(listed, id)
			if tc.flag {
				assert.Equal(t, "usr_operator", record[service.FieldPlacedBy])
			} else {
				assert.Empty(t, record[service.FieldPlacedBy])
			}
		}
		assert.ElementsMatch(t, tc.want, listed, "placed_by_operator=%v", tc.flag)
	}

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{service.FilterPlacedByOperator: "yes"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestABlankOperatorPlacesNoOrder refuses an operator that names nobody rather
// than storing it as a shopper's order or an operator's.
func TestABlankOperatorPlacesNoOrder(t *testing.T) {
	e := newEnv(t)

	for _, placedBy := range []string{" ", "usr_operator "} {
		in := validInput()
		in.PlacedBy = placedBy
		_, err := e.svc.CreateOrder(context.Background(), in)
		require.Error(t, err, "%q", placedBy)
		assert.True(t, errors.IsInvalid(err), "%q", placedBy)
	}
}
