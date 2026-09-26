package fulfilling_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// joinFixture is a parent with one parcel, and the flow over it.
func joinFixture(t *testing.T, status string) (*fulfilling.Workflows, *fakeOrders, *fakeLinks) {
	t.Helper()

	links := newFakeLinks()
	links.bound["order_parent"] = []string{"ful_parent"}
	orders := &fakeOrders{parent: "order_parent"}
	flow := newFlow(t, orders, &fakeFulfillments{status: status, links: links}, links)

	return flow, orders, links
}

// TestAnAdditionJoinsItsParentsWaitingParcel binds the addition beside the
// parent, and a second call binds nothing new (ADR 0197).
func TestAnAdditionJoinsItsParentsWaitingParcel(t *testing.T) {
	t.Parallel()

	flow, _, links := joinFixture(t, "pending")

	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent"))
	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent"))

	assert.Equal(t, []string{"ful_parent"}, links.bound["order_addition"],
		"the addition is bound to the parent's parcel, once")
	assert.Equal(t, []string{"ful_parent"}, links.bound["order_parent"], "the parent keeps it")
}

// TestAnAdditionJoinsNoOtherParcel refuses a parcel that is not the parent's,
// or not waiting any more, and binds nothing.
func TestAnAdditionJoinsNoOtherParcel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, status, parcel, code string
	}{
		{"another order's parcel", "pending", "ful_other", fulfilling.CodeParcelNotParents},
		{"a shipped parcel", "shipped", "ful_parent", fulfilling.CodeParcelNotWaiting},
		{"a canceled parcel", "canceled", "ful_parent", fulfilling.CodeParcelNotWaiting},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow, _, links := joinFixture(t, tc.status)

			err := flow.ShipInParcel(context.Background(), "order_addition", tc.parcel)

			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "%v", err)
			assert.Equal(t, tc.code, coreerrors.CodeOf(err))
			assert.Empty(t, links.bound["order_addition"], "a refused join binds nothing")
		})
	}
}

// TestTheOrderModulesRefusalStopsTheJoin passes the order module's answer
// through as it is, before any parcel is read.
func TestTheOrderModulesRefusalStopsTheJoin(t *testing.T) {
	t.Parallel()

	flow, orders, links := joinFixture(t, "pending")
	orders.parentErr = coreerrors.Conflict("order_ships_elsewhere", "another address")

	err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent")

	require.Error(t, err)
	assert.Equal(t, "order_ships_elsewhere", coreerrors.CodeOf(err))
	assert.Empty(t, links.bound["order_addition"])
}
