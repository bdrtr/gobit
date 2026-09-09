package checkout

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The channels and warehouses of the narrowing tests.
const (
	// testChannelWeb is the storefront the order is placed on.
	testChannelWeb = "sc_web"
	// testChannelPOS is a second storefront, bound to another warehouse.
	testChannelPOS = "sc_pos"
)

// channelBoundHarness scripts a two-warehouse shop where the web channel ships
// only from the EAST warehouse.
//
// Both warehouses hold the stock of both items, so nothing but the binding can
// decide where the order is reserved from — a narrowing that failed would show
// up as a reservation in the west, not as an error.
func channelBoundHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationEast, testLocationWest}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID
	h.links.served = map[string][]string{
		testChannelWeb: {testLocationEast},
		testChannelPOS: {testLocationWest},
	}

	return h
}

// TestAnOrderIsReservedOnlyFromTheWarehousesItsChannelIsServedBy is the rule the
// binding exists for.
//
// The fulfillment module's fake ranks by the GREATEST id, so with both
// warehouses on the table it would pick the west one. The order is placed on
// the web channel, which ships from the east: a checkout that did not narrow
// would reserve in the west and look perfectly healthy doing it.
func TestAnOrderIsReservedOnlyFromTheWarehousesItsChannelIsServedBy(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	require.NotEmpty(t, h.inventory.reserved)
	for _, call := range h.inventory.reserved {
		assert.Equal(t, testLocationEast, call.LocationID,
			"the web channel ships from the east warehouse and from nowhere else")
	}

	require.NotEmpty(t, h.fulfillment.offered)
	for _, candidates := range h.fulfillment.offered {
		assert.Equal(t, []string{testLocationEast}, candidates,
			"the narrowing happens BEFORE the ranking: the fulfillment module is asked to "+
				"order the warehouses the channel may use, not all of them")
	}
}

// TestAnUnboundChannelNarrowsNothing is the reading that keeps every existing
// installation working.
//
// A channel with no warehouse bound is one nobody has configured, not one that
// ships from nowhere. Read the other way, the day this field arrived would have
// refused every order in every shop.
func TestAnUnboundChannelNarrowsNothing(t *testing.T) {
	h := channelBoundHarness(t)
	h.links.served = map[string][]string{}

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	require.NotEmpty(t, h.fulfillment.offered)
	assert.Equal(t, []string{testLocationEast, testLocationWest}, h.fulfillment.offered[0],
		"with nothing bound the candidates reach the ranking exactly as the inventory "+
			"module gave them")
}

// TestAnOrderWithNoChannelNarrowsNothing keeps the administrative path as it
// was.
//
// An operator placing an order names no channel, and the link service must not
// even be asked: a question about an empty set is a query for nothing.
func TestAnOrderWithNoChannelNarrowsNothing(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = ""

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, []string{testLocationEast, testLocationWest}, h.fulfillment.offered[0])
	assert.Zero(t, h.rec.count("link:list_many_by_to:"+LinkLocationSalesChannel),
		"an order that names no channel asks the link service nothing")
}

// TestStockInAWarehouseTheChannelDoesNotUseIsNotSold is the refusal that makes
// the binding worth something.
//
// The units exist and the order is still refused, so the message has to say
// WHICH of the two it is: a code of its own sends the operator to the binding
// rather than to the purchase order.
func TestStockInAWarehouseTheChannelDoesNotUseIsNotSold(t *testing.T) {
	h := channelBoundHarness(t)
	h.inventory.locationsFn = func(_ context.Context, _ string, _ int64) ([]string, error) {
		return []string{testLocationWest}, nil
	}

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, CodeChannelHasNoStock, coreerrors.CodeOf(err),
		"'out of stock' and 'the stock is in a warehouse this channel may not use' send an "+
			"operator to two different places")
	assert.Empty(t, h.inventory.reserved)
}

// TestADeclaredWarehouseOutsideTheChannelIsRefused holds the instruction and
// the binding against each other.
//
// A declared location is an INSTRUCTION rather than a preference, which is why
// no module is asked when one is given. An instruction that contradicts the
// merchant's own binding is refused: obeying would make the binding a
// suggestion, and silently choosing another warehouse would mean the flow
// deciding where an administrative order ships from.
func TestADeclaredWarehouseOutsideTheChannelIsRefused(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = testLocationWest
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, CodeLocationOutsideChannel, coreerrors.CodeOf(err))
	assert.Empty(t, h.inventory.reserved, "nothing may be reserved before the refusal")
}

// TestADeclaredWarehouseInsideTheChannelIsObeyed is the other half: the
// instruction stands when it agrees with the binding.
func TestADeclaredWarehouseInsideTheChannelIsObeyed(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = testLocationEast
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	require.NotEmpty(t, h.inventory.reserved)
	for _, call := range h.inventory.reserved {
		assert.Equal(t, testLocationEast, call.LocationID)
	}
	assert.Empty(t, h.fulfillment.offered,
		"a declared location asks no module, and that has not changed")
}

// TestSeveralChannelsAreTheUNIONOfTheirWarehouses answers the identity that
// holds more than one channel.
//
// The set is a union rather than an intersection: each channel is a storefront
// the order may have come from, and a warehouse serving any of them can serve
// the order. An intersection would refuse orders from a key holding two
// storefronts that share no warehouse — which is the ordinary shape of two
// storefronts.
func TestSeveralChannelsAreTheUNIONOfTheirWarehouses(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb, testChannelPOS}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	require.NotEmpty(t, h.fulfillment.offered)
	assert.ElementsMatch(t, []string{testLocationEast, testLocationWest},
		h.fulfillment.offered[0])
}

// TestTheChannelBindingIsReadONCEPerOrder keeps a set that cannot change inside
// one step from being asked per line.
func TestTheChannelBindingIsReadONCEPerOrder(t *testing.T) {
	h := channelBoundHarness(t)

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, 1, h.rec.count("link:list_many_by_to:"+LinkLocationSalesChannel),
		"the cart has two lines and the binding is one question")
}

// TestAnUnreadableBindingStopsTheOrder keeps a failed read from being taken as
// "no restriction".
//
// The set is what narrows the reservation. Treating a failure as an empty set
// would place the order from a warehouse the channel may not serve — exactly
// what the binding exists to prevent — and nothing would say the rule had been
// skipped.
func TestAnUnreadableBindingStopsTheOrder(t *testing.T) {
	h := channelBoundHarness(t)
	h.links.listManyByToFn = func(
		_ context.Context, _ string, _ []string,
	) (map[string][]string, error) {
		return nil, errors.New("the link service is down")
	}

	in := h.input()
	in.LocationID = ""
	in.SalesChannelIDs = []string{testChannelWeb}

	_, err := h.wf.CompleteCart(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, CodeChannelLocationsUnreadable, coreerrors.CodeOf(err))
	assert.Empty(t, h.inventory.reserved)
}
