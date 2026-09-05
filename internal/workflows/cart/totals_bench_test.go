package cart

import (
	"strconv"
	"testing"
)

// benchCartSize is the number of lines the benchmark cart carries.
//
// Twenty is a large real basket rather than a synthetic one: big enough that
// the per-line cost reads clearly, small enough to still be somewhere a shop
// actually goes.
const benchCartSize = 20

// benchmarkLines builds the per-line totals a priced cart arrives with.
func benchmarkLines() []LineTotals {
	lines := make([]LineTotals, 0, benchCartSize)
	for i := range benchCartSize {
		unit := int64(1_000 + i*137)
		quantity := int64(1 + i%3)
		subtotal := unit * quantity
		lines = append(lines, LineTotals{
			LineItemID:    "line_" + strconv.Itoa(i),
			UnitPrice:     unit,
			Subtotal:      subtotal,
			DiscountTotal: subtotal / 10,
		})
	}

	return lines
}

// benchmarkSnapshot builds the cart the benchmark lines belong to.
func benchmarkSnapshot() Snapshot {
	items := make([]SnapshotItem, 0, benchCartSize)
	for i := range benchCartSize {
		items = append(items, SnapshotItem{
			ID:        "line_" + strconv.Itoa(i),
			VariantID: "var_" + strconv.Itoa(i),
			Quantity:  int64(1 + i%3),
		})
	}

	return snapshotOf(1, items, nil)
}

// BenchmarkAssembleTotals measures the summation every cart read ends with.
//
// It carries the invariant the whole flow is judged on — Total = Subtotal -
// DiscountTotal + ShippingTotal + TaxTotal, line by line and again at the cart
// level — and it runs on every totals calculation, every checkout step and
// every storefront cart render.
func BenchmarkAssembleTotals(b *testing.B) {
	snap := benchmarkSnapshot()
	lines := benchmarkLines()

	// A benchmark measuring an error return would report a fine number and mean
	// nothing.
	if _, err := assembleTotals(snap, lines, 4_990, "region"); err != nil {
		b.Fatalf("the fixture does not assemble, so the benchmark would measure the error path: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, _ = assembleTotals(snap, lines, 4_990, "region")
	}
}

// BenchmarkApplyTaxResponse measures the per-line rate arithmetic.
//
// This is where the rounding rules live — down per line, remainder not carried
// — so it is the part of the flow where making it faster is most likely to make
// it wrong. A figure of its own means such a change can be judged on its own.
func BenchmarkApplyTaxResponse(b *testing.B) {
	snap := benchmarkSnapshot()
	source := benchmarkLines()

	resp := taxResponse{RegionFound: true, RegionID: "txreg_1", ProviderID: "system"}
	for i := range source {
		taxable := source[i].Subtotal - source[i].DiscountTotal
		tax := taxable * 2_000 / 10_000
		resp.Items = append(resp.Items, taxResponseLine{
			ID:            source[i].LineItemID,
			RateID:        "rate_1",
			RateBps:       2_000,
			TaxableAmount: taxable,
			TaxAmount:     tax,
		})
		resp.TaxTotal += tax
	}

	// applyTaxResponse WRITES into the lines, so each iteration needs its own
	// copy; the copy is inside the timed loop because skipping it would measure
	// a function applying tax on top of tax.
	lines := make([]LineTotals, len(source))
	copy(lines, source)
	if applyTaxResponse(snap, lines, resp) != nil {
		b.Fatal("the fixture does not apply, so the benchmark would measure the error path")
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		copy(lines, source)
		_ = applyTaxResponse(snap, lines, resp)
	}
}
