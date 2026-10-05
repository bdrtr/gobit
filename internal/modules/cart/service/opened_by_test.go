package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestACartNamesTheOperatorWhoOpenedIt follows the opener from the opening to
// the read layer's field and filter (ADR 0296).
func TestACartNamesTheOperatorWhoOpenedIt(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	byOperator, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, Email: "caller@example.com",
		OpenedBy: "usr_operator",
	})
	require.NoError(t, err)
	assert.Equal(t, "usr_operator", byOperator.OpenedBy)
	byShopper := newCart(ctx, t, svc)
	assert.Empty(t, byShopper.OpenedBy)

	provider := service.NewQueryProvider(svc)
	for _, tc := range []struct {
		flag bool
		want string
	}{
		{flag: true, want: byOperator.ID},
		{flag: false, want: byShopper.ID},
	} {
		records, err := provider.List(ctx, query.ListOptions{
			Fields:  []string{service.FieldID, service.FieldOpenedBy},
			Filters: map[string]any{service.FilterOpenedByOperator: tc.flag},
		})
		require.NoError(t, err)
		require.Len(t, records, 1, "opened_by_operator=%v", tc.flag)
		assert.Equal(t, tc.want, records[0][service.FieldID])
	}

	records, err := provider.FetchByIDs(ctx, []string{byOperator.ID, byShopper.ID},
		[]string{service.FieldID, service.FieldOpenedBy})
	require.NoError(t, err)
	opener := map[any]any{}
	for _, record := range records {
		opener[record[service.FieldID]] = record[service.FieldOpenedBy]
	}
	assert.Equal(t, map[any]any{byOperator.ID: "usr_operator", byShopper.ID: ""}, opener)

	_, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FilterOpenedByOperator: "yes"},
	})
	require.Error(t, err, "a filter with the wrong type must be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestABlankOperatorIsNotAnOpener refuses an opener that names nobody rather
// than storing it as a shopper's cart or an operator's.
func TestABlankOperatorIsNotAnOpener(t *testing.T) {
	svc, _ := newService(t)

	for _, openedBy := range []string{" ", " usr_operator"} {
		_, err := svc.CreateCart(context.Background(), service.CreateCartInput{
			RegionID: regionID, CurrencyCode: currency, OpenedBy: openedBy,
		})
		require.Error(t, err, "%q", openedBy)
		assert.True(t, errors.IsInvalid(err), "%q", openedBy)
	}
}

// TestTheInteropOpensACartForItsOperator follows the opener through the
// surface the cart flow opens carts with (ADR 0296).
func TestTheInteropOpensACartForItsOperator(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	id, err := service.NewInterop(svc).OpenCart(ctx, regionID, currency, "", "caller@example.com", "",
		"usr_operator", "", nil)
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "usr_operator", detail.OpenedBy)
}
