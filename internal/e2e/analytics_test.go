//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/plugins/analytics"
)

// This file is the only place where the chain of ADR 0153 exists in one piece:
// the CART module publishes, the real bus carries, and the ANALYTICS plugin
// writes a row an operator can read over HTTP.
//
// Neither end can prove it alone, and that is not a convenience argument. The
// cart module's own tests assert what it hands to a fake bus; the plugin's assert
// what it does with an event a test constructed. Between the two sits every way a
// topic name, a payload key or a moment format can disagree — and the plugin may
// not import the cart module (internal/arch TestPluginsDoNotImportModules), so no
// compiler will ever put the two names side by side.

// funnelResponse is the answer of GET /admin/v1/analytics/funnel.
type funnelResponse struct {
	Data []struct {
		Day            string `json:"day"`
		RegionID       string `json:"region_id"`
		CartsCreated   int64  `json:"carts_created"`
		CartsCompleted int64  `json:"carts_completed"`
		OrdersPlaced   int64  `json:"orders_placed"`
	} `json:"data"`
	From string `json:"from"`
	To   string `json:"to"`
}

// readFunnel asks the admin endpoint for today's counts in the taxed region.
func readFunnel(t *testing.T) funnelResponse {
	t.Helper()

	today := time.Now().UTC().Format(time.DateOnly)
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)

	recorder, err := adminRequestWithBody(http.MethodGet,
		analytics.FunnelPath+"?from="+today+"&to="+tomorrow, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var out funnelResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &out))

	return out
}

// regionCounts picks the taxed region's row out of the answer.
func regionCounts(t *testing.T, answer funnelResponse) (created, completed, placed int64) {
	t.Helper()

	for i := range answer.Data {
		if answer.Data[i].RegionID == taxedRegionID {
			return answer.Data[i].CartsCreated, answer.Data[i].CartsCompleted,
				answer.Data[i].OrdersPlaced
		}
	}

	return 0, 0, 0
}

// TestTheFunnelSeesACartOpenAndBecomeAnOrder is the slice's whole claim, end to
// end.
//
// The counts are read as DELTAS around the act rather than as absolutes: this
// harness is shared, every other scenario opens carts of its own, and an
// assertion on a total would fail depending on which tests ran first. What the
// deltas say is exactly the decision — one cart opened is one row, one cart
// completed is another, and the order that came out of it is a third.
func TestTheFunnelSeesACartOpenAndBecomeAnOrder(t *testing.T) {
	ctx := t.Context()

	createdBefore, completedBefore, placedBefore := regionCounts(t, readFunnel(t))

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Funnel Product",
		map[string]int64{taxedCurrency: 30_000}, 5)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, 1)

	afterOpen := readFunnel(t)
	createdAfterOpen, _, _ := regionCounts(t, afterOpen)
	assert.Equal(t, createdBefore+1, createdAfterOpen,
		"opening a cart must reach the funnel: the cart module publishes, the bus "+
			"carries, the plugin writes — and if any of the three names disagree this "+
			"is the only assertion in the tree that notices")

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     36_000,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.OrderID)

	afterOrder := readFunnel(t)
	createdAfter, completedAfter, placedAfter := regionCounts(t, afterOrder)

	assert.Equal(t, createdBefore+1, createdAfter,
		"completing a cart must not open another one")
	assert.Equal(t, completedBefore+1, completedAfter,
		"the completion is its own moment and its own row")
	assert.Equal(t, placedBefore+1, placedAfter,
		"the order module's event lands in the same table, read with ITS own moment "+
			"field — the two payloads spell the moment differently and a handler reading "+
			"the wrong word would leave this number at zero forever")

	assert.Equal(t, time.Now().UTC().Format(time.DateOnly), afterOrder.From,
		"the window the request asked for is echoed back, so a caller can see which "+
			"one the defaults produced")
}

// TestAnAbandonedCartIsAnOpeningWithNoCompletion is the gap the funnel exists to
// show.
//
// A shop's question is never "how many carts" but "how many did not finish", and
// the answer is the difference between two numbers that must therefore move
// INDEPENDENTLY. A single counter, or a completion derived from the creation,
// could not express it.
func TestAnAbandonedCartIsAnOpeningWithNoCompletion(t *testing.T) {
	ctx := t.Context()

	createdBefore, completedBefore, _ := regionCounts(t, readFunnel(t))

	customerID, _ := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Abandoned Product",
		map[string]int64{taxedCurrency: 30_000}, 5)
	_, _ = prepareCart(ctx, t, customerID, variantID, 1)

	createdAfter, completedAfter, _ := regionCounts(t, readFunnel(t))

	assert.Equal(t, createdBefore+1, createdAfter, "the cart was opened")
	assert.Equal(t, completedBefore, completedAfter,
		"and never completed; the two numbers are not the same number")
}

// TestTheFunnelRefusesAnImpossibleWindow keeps the endpoint's own boundary.
func TestTheFunnelRefusesAnImpossibleWindow(t *testing.T) {
	recorder, err := adminRequestWithBody(http.MethodGet,
		analytics.FunnelPath+"?from=2026-09-13&to=2026-09-12", nil)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code,
		"a window that ends before it starts is a request for nothing, and saying so "+
			"is cheaper than answering an empty list that reads as 'no carts'")
}
