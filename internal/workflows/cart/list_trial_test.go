package cart

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// TestAListTrialComparesTodaysPriceWithAndWithoutTheList is ADR 0220: each line
// is priced at its quantity with the list and without it, the list's effect is
// the difference, and what the line was sold at is reported beside it.
func TestAListTrialComparesTodaysPriceWithAndWithoutTheList(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{
			trialLineRecord("li_1", "order_a", testVariantA, 1, 900, 0),
			trialLineRecord("li_2", "order_a", testVariantB, 2, 250, 0),
			trialLineRecord("li_3", "order_b", testVariantB, 1, 250, 0),
		},
		[]query.Record{
			trialOrderRecord("order_a", "pending", testCustomerID, 1400, 0),
			trialOrderRecord("order_b", "completed", "", 250, 0),
		},
		nil,
	)
	h.prices.trialAmounts = map[string]int64{testPriceSetA: 800}

	report, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)

	require.NoError(t, err)
	assert.Equal(t, "plist_1", report.PriceListID)
	assert.Equal(t, []string{"plist_1"}, h.prices.comparedList)
	require.Len(t, report.Currencies, 1)
	assert.Equal(t, PriceListTrialCurrency{
		CurrencyCode: testCurrency, OrdersPriced: 2, LinesPriced: 3, LinesChanged: 1,
		Charged: 900 + 500 + 250, Baseline: 1000 + 500 + 250, Trial: 800 + 500 + 250,
	}, report.Currencies[0])
	require.Len(t, report.Orders, 1, "the order the list does not change is not listed")
	assert.Equal(t, "order_a", report.Orders[0].OrderID)
	assert.Equal(t, []int64{1500, 1300}, []int64{report.Orders[0].Baseline, report.Orders[0].Trial})
	assert.Contains(t, report.Assumptions, "list_active_without_window")

	entry := h.prices.compared[0].Entries[0]
	assert.Equal(t, testRegionID, entry.Attributes[attrRegionID])
	assert.Equal(t, testCustomerID, entry.Attributes[AttrCustomerID], "the customer's own context, as a cart's")
	assert.Equal(t, []priceRequestItem{{PriceSetID: testPriceSetA, Quantity: 1}, {PriceSetID: testPriceSetB, Quantity: 2}},
		entry.Items, "each line at its own quantity")
}

// TestAListTrialLeavesOutWhatItCannotPrice: a canceled order, a variant with no
// price set today and a set with no price are counted apart.
func TestAListTrialLeavesOutWhatItCannotPrice(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{
			trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 0),
			trialLineRecord("li_2", "order_a", "variant_unlinked", 1, 500, 0),
			trialLineRecord("li_3", "order_c", testVariantA, 1, 1000, 0),
			trialLineRecord("li_4", "order_a", "variant_two_sets", 1, 500, 0),
		},
		[]query.Record{
			trialOrderRecord("order_a", "pending", "", 1500, 0),
			trialOrderRecord("order_c", trialOrderCanceled, "", 1000, 0),
		},
		nil,
	)
	delete(h.prices.amounts, testPriceSetA)
	h.links.links["variant_two_sets"] = []string{testPriceSetB, testPriceSetA}

	report, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)

	require.NoError(t, err)
	assert.Equal(t, 2, report.OrdersRead)
	assert.Equal(t, 1, report.OrdersCanceled)
	assert.Equal(t, 3, report.LinesUnpriced, "the unlinked variant, the one with two sets, and the set with no price")
	require.Len(t, report.Currencies, 1)
	assert.Zero(t, report.Currencies[0].OrdersPriced, "an order none of whose lines were priced is not priced")
	assert.Empty(t, report.Orders)
}

// TestAListTrialReadsEachCustomersContextOnce: three orders of one customer
// read the customer's groups once, and every order carries the head group.
func TestAListTrialReadsEachCustomersContextOnce(t *testing.T) {
	var lines, orders []query.Record
	for i := range 3 {
		id := fmt.Sprintf("order_%d", i)
		lines = append(lines, trialLineRecord("li_"+id, id, testVariantA, 1, 1000, 0))
		orders = append(orders, trialOrderRecord(id, "pending", testCustomerID, 1000, 0))
	}
	h := newTrialHarness(t, lines, orders, nil)
	h.customers.groups = map[string][]string{testCustomerID: {"vip"}}
	reads := 0
	h.customers.groupsHook = func() { reads++ }

	_, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)

	require.NoError(t, err)
	assert.Equal(t, 1, reads)
	for _, entry := range h.prices.compared[0].Entries {
		assert.Equal(t, "vip", entry.Attributes[attrCustomerGroupID])
	}
}

// TestAListTrialRefusesWhatItCannotAnswer: the list is required, the period
// has to end after it starts and be at most the trial's bound, a line's
// quantity has to be one a cart holds, and a pricing failure is not answered
// with a partial report.
func TestAListTrialRefusesWhatItCannotAnswer(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 0)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
		nil,
	)
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"no list": func() error { _, err := h.wf.TrialPriceList(ctx, " ", trialFrom, trialTo); return err },
		"empty": func() error {
			_, err := h.wf.TrialPriceList(ctx, "plist_1", trialFrom, trialFrom)
			return err
		},
		"backwards": func() error {
			_, err := h.wf.TrialPriceList(ctx, "plist_1", trialTo, trialFrom)
			return err
		},
		"too long": func() error {
			_, err := h.wf.TrialPriceList(ctx, "plist_1", trialFrom, trialFrom.Add(MaxTrialPeriod+time.Hour))
			return err
		},
	} {
		err := call()
		require.Error(t, err, name)
		assert.Equal(t, CodeTrialInvalid, errors.CodeOf(err), name)
	}

	h.catalog.entities[trialLineEntity] = []query.Record{trialLineRecord("li_1", "order_a", testVariantA, MaxQuantity+1, 1000, 0)}
	_, err := h.wf.TrialPriceList(ctx, "plist_1", trialFrom, trialTo)
	assert.Equal(t, CodeTrialReadInvalid, errors.CodeOf(err), "a quantity no cart holds is not cut to fit")
	h.catalog.entities[trialLineEntity] = []query.Record{trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 0)}

	h.prices.batchErr = errors.Unavailable("pricing_down", "the pricing module is away")
	_, err = h.wf.TrialPriceList(ctx, "plist_1", trialFrom, trialTo)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}

// TestAListTrialNamesTheLargestChangesFirst: of the changed orders the hundred
// largest changes are named, the largest first and the later of two equal ones
// before the earlier, while the sums cover all of them.
func TestAListTrialNamesTheLargestChangesFirst(t *testing.T) {
	var lines, orders []query.Record
	for i := range MaxTrialListedOrders {
		id := fmt.Sprintf("order_%03d", i)
		lines = append(lines, trialLineRecord("li_"+id, id, testVariantA, int64(i+1), 1000, 0))
		orders = append(orders, trialOrderRecord(id, "pending", "", 1000*int64(i+1), 0))
	}
	tie := trialOrderRecord("order_tie", "pending", "", 1000, 0)
	tie["placed_at"] = trialFrom.Add(2 * time.Hour)
	orders = append(orders, tie)
	lines = append(lines, trialLineRecord("li_tie", "order_tie", testVariantA, 1, 1000, 0))
	h := newTrialHarness(t, lines, orders, nil)
	h.prices.trialAmounts = map[string]int64{testPriceSetA: 800}

	report, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)

	require.NoError(t, err)
	assert.Equal(t, MaxTrialListedOrders+1, report.Currencies[0].OrdersPriced)
	require.Len(t, report.Orders, MaxTrialListedOrders)
	assert.Equal(t, fmt.Sprintf("order_%03d", MaxTrialListedOrders-1), report.Orders[0].OrderID, "the largest change first")
	assert.Equal(t, "order_001", report.Orders[MaxTrialListedOrders-2].OrderID)
	assert.Equal(t, "order_tie", report.Orders[MaxTrialListedOrders-1].OrderID,
		"of two equal changes the later order first, and the earlier one past the bound")
}

// TestAListTrialReadsOnlyAnAnswerAboutItsOrders: an answer about other orders
// or other lines is an error, and a line the list prices but the base does not
// is unpriced, since there is no baseline to take the list's effect from.
func TestAListTrialReadsOnlyAnAnswerAboutItsOrders(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 0)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
		nil,
	)
	priced := `{"amount":900,"priced":true}`
	for name, raw := range map[string]string{
		"no entry":      `{"entries":[]}`,
		"another order": `{"entries":[{"reference":"order_z","items":[{"baseline":` + priced + `,"trial":` + priced + `}]}]}`,
		"no line":       `{"entries":[{"reference":"order_a","items":[]}]}`,
		"not an object": `[1]`,
	} {
		h.prices.compareRaw = json.RawMessage(raw)
		_, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)
		require.Error(t, err, name)
		assert.Equal(t, CodePriceResponseInvalid, errors.CodeOf(err), name)
	}

	h.prices.compareRaw = json.RawMessage(`{"entries":[{"reference":"order_a","items":[` +
		`{"baseline":{"priced":false},"trial":` + priced + `}]}]}`)
	report, err := h.wf.TrialPriceList(context.Background(), "plist_1", trialFrom, trialTo)
	require.NoError(t, err)
	assert.Equal(t, 1, report.LinesUnpriced)
	assert.Empty(t, report.Orders)
}
