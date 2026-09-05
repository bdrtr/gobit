package service

import (
	"strconv"
	"testing"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// benchCartSize is the number of lines the benchmark cart carries.
//
// Twenty is a large real basket rather than a synthetic one. The point of the
// figure is to sit where the cost per line still reads clearly while staying
// somewhere a shop actually goes.
const benchCartSize = 20

// benchmarkInput builds a cart of benchCartSize lines and one shipping method.
func benchmarkInput() ComputeInput {
	items := make([]ComputeItem, 0, benchCartSize)
	for i := range benchCartSize {
		items = append(items, item(
			"line_"+strconv.Itoa(i),
			int64(1_000+i*137),
			int64(1+i%3),
			map[string]string{"product_id": "prod_" + strconv.Itoa(i%7)},
		))
	}

	return ComputeInput{
		CurrencyCode:    "TRY",
		Context:         map[string]string{"region_id": "reg_1", "customer_group_id": "vip"},
		Items:           items,
		ShippingMethods: []ComputeShippingMethod{{ID: "sm_1", Amount: 4_990}},
		At:              testNow,
	}
}

// benchmarkCandidates builds the promotions the benchmark cart is priced against.
//
// The mix is deliberate: percentage and fixed, across and each, items and
// shipping. Each shape walks the computation differently, and a benchmark
// carrying only one of them would measure a quarter of the function.
func benchmarkCandidates() []models.PromotionCandidate {
	shapes := []struct {
		id     string
		method *models.ApplicationMethod
	}{
		{"promo_pct_across", percentageMethod("promo_pct_across", 1_000, models.TargetItems, models.AllocationAcross)},
		{"promo_pct_each", percentageMethod("promo_pct_each", 500, models.TargetItems, models.AllocationEach)},
		{"promo_fixed_across", fixedMethod("promo_fixed_across", 2_500, models.TargetItems, models.AllocationAcross)},
		{"promo_shipping", percentageMethod("promo_shipping", 5_000, models.TargetShippingMethods, models.AllocationAcross)},
	}

	candidates := make([]models.PromotionCandidate, 0, len(shapes))
	for _, shape := range shapes {
		candidates = append(candidates, models.PromotionCandidate{
			Promotion: models.Promotion{
				ID:          shape.id,
				IsAutomatic: true,
				Type:        models.PromotionStandard,
				Status:      models.PromotionActive,
			},
			Method: shape.method,
		})
	}

	return candidates
}

// BenchmarkComputeDiscounts measures the promotion arithmetic that runs on
// every cart read.
//
// The repository measured the database side hard — EXPLAIN inside integration
// tests, 52,000-row fixtures, millisecond figures in godocs — and measured the
// Go side not at all. This is the hottest pure-CPU path there is: it runs on
// every totals calculation, it allocates per line and per promotion, and an
// allocation regression in it would surface as latency long before anything
// pointed at the cause.
func BenchmarkComputeDiscounts(b *testing.B) {
	in := benchmarkInput()
	candidates := benchmarkCandidates()

	// A benchmark measuring the REJECTION path would report a fine number and
	// mean nothing. This proves discounts are actually being computed before
	// the timer starts.
	if got := computeDiscounts(candidates, in); got.DiscountTotal <= 0 {
		b.Fatalf("the fixture produced no discount, so the benchmark would measure the rejection path: %+v", got)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = computeDiscounts(candidates, in)
	}
}

// BenchmarkAllocateAcross measures the remainder distribution alone.
//
// It is separated from the computation above because it is the part with an
// exact arithmetic obligation — every kurus has to land somewhere — and the
// obvious way to make that cheaper is to make it wrong. A figure of its own
// means a change here can be judged on its own.
func BenchmarkAllocateAcross(b *testing.B) {
	lines := make([]allocLine, 0, benchCartSize)
	for i := range benchCartSize {
		lines = append(lines, allocLine{ID: "line_" + strconv.Itoa(i), Amount: int64(1_000 + i*137)})
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = allocateAcross(9_999, lines)
	}
}
