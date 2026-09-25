package cart

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// trialFrom and trialTo are the period every trial test asks about.
var (
	trialFrom = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	trialTo   = time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
)

// trialLineRecord is a sold line as the order module offers it.
func trialLineRecord(id, orderID, variantID string, quantity, unitPrice, discount int64) query.Record {
	return query.Record{
		"id": id, "order_id": orderID, "variant_id": variantID, "quantity": quantity,
		"unit_price": unitPrice, "subtotal": unitPrice * quantity, "discount_total": discount,
	}
}

// trialOrderRecord is an order as the order module offers it.
func trialOrderRecord(id, status, customerID string, subtotal, discount int64) query.Record {
	return query.Record{
		"id": id, "display_id": int64(len(id)), "status": status, "region_id": testRegionID,
		"customer_id": customerID, "cart_id": "cart_of_" + id, "currency_code": testCurrency,
		"placed_at": trialFrom.Add(time.Hour), "subtotal": subtotal, "discount_total": discount,
	}
}

// newTrialHarness scripts the order entities and returns a harness whose
// promotion surface answers with perLine.
func newTrialHarness(t *testing.T, lines, orders []query.Record, perLine map[string]int64) *harness {
	t.Helper()

	h := newHarnessWith(t, &stubDiscounts{perLine: perLine}, nil)
	h.catalog.entities = map[string][]query.Record{
		trialLineEntity:  lines,
		trialOrderEntity: orders,
	}

	return h
}

// TestATrialAddsWhatThePromotionWouldHaveAddedAndNoMore verifies the report's
// arithmetic (ADR 0176).
//
// The promotion is priced alone and the order's actual discount is already on
// its lines. The engine's discounts add up and are capped at the line, so what
// the promotion ADDS is min(actual + trial, subtotal) - actual — on a line whose
// actual discount nearly covers it, the promotion adds only what is left.
func TestATrialAddsWhatThePromotionWouldHaveAddedAndNoMore(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{
			// Line 1: 1000, already discounted 900; the promotion would take 200.
			trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 900),
			// Line 2: 500, not discounted; the promotion would take 100.
			trialLineRecord("li_2", "order_a", testVariantB, 2, 250, 0),
		},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1500, 900)},
		map[string]int64{"li_1": 200, "li_2": 100},
	)

	report, err := h.wf.TrialPromotion(context.Background(), "promo_1", trialFrom, trialTo)
	require.NoError(t, err)

	require.Len(t, report.Currencies, 1)
	sum := report.Currencies[0]
	assert.Equal(t, 1, sum.OrdersPriced)
	assert.Equal(t, 1, sum.OrdersDiscounted)
	assert.Equal(t, int64(1500), sum.Subtotal)
	assert.Equal(t, int64(900), sum.DiscountTotal)
	assert.Equal(t, int64(200), sum.TrialDiscountTotal,
		"line 1 has 100 left to discount and line 2 takes the whole 100: 200, not the 300 priced alone")
	require.Len(t, report.Orders, 1)
	assert.Equal(t, int64(200), report.Orders[0].TrialDiscount)
}

// TestATrialLeavesOutWhatItMustNotPrice verifies the three kinds of order the
// sums do not include: a canceled one, one the promotion was already redeemed
// on, and one the promotion would not apply to — each counted where the report
// says.
func TestATrialLeavesOutWhatItMustNotPrice(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{
			trialLineRecord("li_c", "order_canceled", testVariantA, 1, 1000, 0),
			trialLineRecord("li_r", "order_redeemed", testVariantA, 1, 1000, 100),
			trialLineRecord("li_s", "order_skipped", testVariantA, 1, 1000, 0),
			trialLineRecord("li_d", "order_discounted", testVariantA, 1, 1000, 0),
		},
		[]query.Record{
			trialOrderRecord("order_canceled", trialOrderCanceled, "", 1000, 0),
			trialOrderRecord("order_redeemed", "completed", "", 1000, 100),
			trialOrderRecord("order_skipped", "pending", "", 1000, 0),
			trialOrderRecord("order_discounted", "pending", "", 1000, 0),
		},
		nil,
	)
	h.discounts.trialFn = func(_ string, req trialRequest) (trialResponse, error) {
		resp := trialResponse{Assumptions: []string{"active"}}
		for _, entry := range req.Entries {
			answer := trialResponseEntry{Reference: entry.Reference, Items: []discountLine{}}
			switch entry.Reference {
			case "cart_of_order_redeemed":
				answer.AlreadyApplied = true
			case "cart_of_order_skipped":
				answer.Skipped = "rules_not_matched"
				answer.Items = append(answer.Items, discountLine{ID: "li_s"})
			default:
				answer.Items = append(answer.Items, discountLine{ID: entry.Request.Items[0].ID, Amount: 50})
			}
			resp.Entries = append(resp.Entries, answer)
		}
		return resp, nil
	}

	report, err := h.wf.TrialPromotion(context.Background(), "promo_1", trialFrom, trialTo)
	require.NoError(t, err)

	require.Len(t, h.discounts.trials, 1)
	assert.Len(t, h.discounts.trials[0].Entries, 3, "a canceled order is not even asked about")
	assert.Equal(t, 4, report.OrdersRead)
	assert.Equal(t, 1, report.OrdersCanceled)
	assert.Equal(t, 1, report.OrdersAlreadyDiscounted)
	assert.Equal(t, map[string]int{"rules_not_matched": 1}, report.Skipped)

	require.Len(t, report.Currencies, 1)
	assert.Equal(t, 2, report.Currencies[0].OrdersPriced, "the skipped order was priced, the redeemed one was not")
	assert.Equal(t, 1, report.Currencies[0].OrdersDiscounted)
	assert.Equal(t, int64(50), report.Currencies[0].TrialDiscountTotal)
	assert.Contains(t, report.Assumptions, "todays_catalog", "the flow's own assumptions are published")
	assert.Contains(t, report.Assumptions, "active", "the promotion module's assumptions are published")
}

// TestATrialResolvesEachCustomersSegmentOnce verifies that the rule context is
// read once per region and customer, not once per order.
func TestATrialResolvesEachCustomersSegmentOnce(t *testing.T) {
	var lines, orders []query.Record
	for i := range 3 {
		id := fmt.Sprintf("order_%d", i)
		lines = append(lines, trialLineRecord("li_"+id, id, testVariantA, 1, 1000, 0))
		orders = append(orders, trialOrderRecord(id, "pending", testCustomerID, 1000, 0))
	}
	h := newTrialHarness(t, lines, orders, map[string]int64{})
	h.customers.groups = map[string][]string{testCustomerID: {"vip"}}
	reads := 0
	h.customers.groupsHook = func() { reads++ }

	_, err := h.wf.TrialPromotion(context.Background(), "promo_1", trialFrom, trialTo)
	require.NoError(t, err)

	assert.Equal(t, 1, reads, "three orders of one customer read the customer's groups once")
	for _, entry := range h.discounts.trials[0].Entries {
		assert.Equal(t, "vip", entry.Request.Context[attrCustomerGroupID],
			"every order of the customer carries the segment the cart would have")
	}
}

// TestATrialRefusesAPeriodItCannotPrice verifies the period's bounds.
func TestATrialRefusesAPeriodItCannotPrice(t *testing.T) {
	h := newTrialHarness(t, nil, nil, nil)

	for name, period := range map[string][2]time.Time{
		"backwards":      {trialTo, trialFrom},
		"empty":          {trialFrom, trialFrom},
		"wider than 93d": {trialFrom, trialFrom.Add(MaxTrialPeriod + time.Hour)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h.wf.TrialPromotion(context.Background(), "promo_1", period[0], period[1])
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Equal(t, CodeTrialInvalid, errors.CodeOf(err))
		})
	}
}

// TestATrialRefusesMoreOrdersThanItPrices verifies that a period holding more
// than MaxTrialOrders orders is refused rather than cut: a report over the first
// N orders would read as a report over the period.
func TestATrialRefusesMoreOrdersThanItPrices(t *testing.T) {
	lines := make([]query.Record, 0, MaxTrialOrders+1)
	for i := range MaxTrialOrders + 1 {
		id := fmt.Sprintf("order_%05d", i)
		lines = append(lines, trialLineRecord("li_"+id, id, testVariantA, 1, 1000, 0))
	}
	h := newTrialHarness(t, lines, nil, nil)

	_, err := h.wf.TrialPromotion(context.Background(), "promo_1", trialFrom, trialTo)

	require.Error(t, err)
	assert.Equal(t, CodeTrialTooWide, errors.CodeOf(err))
	assert.Empty(t, h.discounts.trials, "the promotion module was asked although the period was refused")
}

// TestATrialRefusesAnAnswerAboutOtherPurchases verifies that an answer that does
// not line up with the question is an error rather than a report.
func TestATrialRefusesAnAnswerAboutOtherPurchases(t *testing.T) {
	h := newTrialHarness(t,
		[]query.Record{trialLineRecord("li_1", "order_a", testVariantA, 1, 1000, 0)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
		nil,
	)
	h.discounts.trialFn = func(string, trialRequest) (trialResponse, error) {
		return trialResponse{Entries: []trialResponseEntry{{Reference: "cart_other", Items: []discountLine{}}}}, nil
	}

	_, err := h.wf.TrialPromotion(context.Background(), "promo_1", trialFrom, trialTo)

	require.Error(t, err)
	assert.Equal(t, CodeTrialResultInvalid, errors.CodeOf(err))
}
