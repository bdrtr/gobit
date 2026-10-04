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

// taxTrialChange is the change every tax trial test tries.
var taxTrialChange = json.RawMessage(`{"rate_bps":1000}`)

// newTaxTrialHarness scripts the order entities and returns a harness whose
// tax surface compares at 20% against 10%.
func newTaxTrialHarness(t *testing.T, lines, orders []query.Record) *harness {
	t.Helper()

	taxes := newStubTaxes()
	taxes.trialBps = 1000
	h := newHarnessWith(t, nil, taxes)
	h.catalog.entities = map[string][]query.Record{
		trialLineEntity:  lines,
		trialOrderEntity: orders,
	}

	return h
}

// soldLine is a trial line record carrying the tax it was sold with and the
// order's gift card flag.
func soldLine(id, orderID, variantID string, quantity, unitPrice, discount, tax int64, giftCard bool) query.Record {
	record := trialLineRecord(id, orderID, variantID, quantity, unitPrice, discount)
	record["tax_total"] = tax
	record["is_giftcard"] = giftCard

	return record
}

// productRecord is a product as the catalog offers its facts.
func productRecord(id, typeID string, giftCard bool) query.Record {
	return query.Record{
		query.IDField: id, attrIsGiftcard: giftCard, attrDiscountable: true, attrTypeID: typeID,
	}
}

// TestATaxTrialSendsWhatTheCartSent is the half of ADR 0387 that is the sale's:
// a line is sent at its unit price times its quantity less its discount — not
// the subtotal the order kept — with the product and type the catalog gives
// its variant, in the country of the order's region, and a line is left out
// on the order's own gift card flag rather than today's product's.
func TestATaxTrialSendsWhatTheCartSent(t *testing.T) {
	net := soldLine("li_1", "order_a", testVariantA, 2, 1000, 300, 340, false)
	net["subtotal"] = int64(1500)
	h := newTaxTrialHarness(t,
		[]query.Record{
			net,
			// Flagged on the order, not by today's product: left out.
			soldLine("li_2", "order_a", testVariantA, 1, 500, 0, 0, true),
			// Flagged by today's product, not on the order: sent.
			soldLine("li_3", "order_a", testVariantB, 1, 500, 0, 100, false),
		},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 2500, 300)},
	)
	h.catalog.entities[EntityProduct] = []query.Record{
		productRecord(testProductA, "ptyp_shirt", false),
		productRecord(testProductB, "", true),
	}

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)

	require.Len(t, h.taxes.compared, 1)
	assert.Equal(t, []string{`taxrate_1 {"rate_bps":1000}`}, h.taxes.changes)
	assert.Equal(t, []taxCompareEntry{{
		Reference: "order_a", CountryCode: "TR",
		Items: []taxRequestItem{
			{ID: "li_1", ProductID: testProductA, ProductTypeID: "ptyp_shirt", Amount: 2000 - 300},
			{ID: "li_3", ProductID: testProductB, Amount: 500},
		},
	}}, h.taxes.compared[0].Entries)
	assert.JSONEq(t, string(taxTrialChange), string(report.Change), "the change tried is echoed")
	assert.Equal(t, "taxrate_1", report.TaxRateID)
}

// TestATaxTrialSumsChargedBaselineAndTrial: per currency and per order, what
// was charged is the tax the sent lines were sold with — here at 18%, a rate
// since changed — beside the two taxes the comparison answers; a line the
// rate is reached on and whose tax it leaves as it was is reached and not
// changed.
func TestATaxTrialSumsChargedBaselineAndTrial(t *testing.T) {
	h := newTaxTrialHarness(t,
		[]query.Record{
			soldLine("li_1", "order_a", testVariantA, 2, 1000, 0, 360, false),
			soldLine("li_2", "order_a", testVariantB, 1, 500, 0, 77, true),
			soldLine("li_3", "order_a", testVariantA, 1, 1500, 0, 270, false),
			soldLine("li_4", "order_b", testVariantA, 1, 1000, 0, 180, false),
			// A free line: the rate is reached on it, and neither value takes
			// anything from it.
			soldLine("li_5", "order_b", testVariantA, 1, 0, 0, 0, false),
		},
		[]query.Record{
			trialOrderRecord("order_a", "pending", "", 4000, 0),
			trialOrderRecord("order_b", "completed", "", 1000, 0),
		},
	)
	h.taxes.reached = map[string]bool{"li_5": true}

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)

	require.Len(t, report.Currencies, 1)
	assert.Equal(t, TaxRateTrialCurrency{
		CurrencyCode: testCurrency, OrdersPriced: 2, LinesPriced: 4, LinesReached: 4, LinesChanged: 3,
		Charged: 360 + 270 + 180, Baseline: 400 + 300 + 200, Trial: 200 + 150 + 100,
	}, report.Currencies[0], "the gift card line's tax is not what the change is held against")
	require.Len(t, report.Orders, 2)
	assert.Equal(t, TaxRateTrialOrder{
		OrderID: "order_a", DisplayID: int64(len("order_a")), CurrencyCode: testCurrency,
		PlacedAt: trialFrom.Add(time.Hour), Charged: 630, Baseline: 700, Trial: 350,
	}, report.Orders[0])
	assert.Equal(t, []string{"todays_rates", "todays_catalog", "todays_region_countries"}, report.Assumptions)
}

// TestATaxTrialLeavesOutWhatTheCartDidNotTax: a canceled order, an order whose
// region a cart taxes at the region's rate, and an order outside the rate's
// country are counted apart and not summed.
func TestATaxTrialLeavesOutWhatTheCartDidNotTax(t *testing.T) {
	multi := trialOrderRecord("order_multi", "pending", "", 1000, 0)
	multi["region_id"] = "reg_eu"
	abroad := trialOrderRecord("order_de", "pending", "", 1000, 0)
	abroad["region_id"] = "reg_de"
	h := newTaxTrialHarness(t,
		[]query.Record{
			soldLine("li_c", "order_canceled", testVariantA, 1, 1000, 0, 200, false),
			soldLine("li_m", "order_multi", testVariantA, 1, 1000, 0, 200, false),
			soldLine("li_d", "order_de", testVariantA, 1, 1000, 0, 190, false),
			soldLine("li_t", "order_tr", testVariantA, 1, 1000, 0, 200, false),
		},
		[]query.Record{
			trialOrderRecord("order_canceled", trialOrderCanceled, "", 1000, 0),
			multi, abroad,
			trialOrderRecord("order_tr", "pending", "", 1000, 0),
		},
	)
	h.catalog.countries["reg_eu"] = []string{"DE", "FR"}
	h.catalog.countries["reg_de"] = []string{"DE"}

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)

	assert.Equal(t, 4, report.OrdersRead)
	assert.Equal(t, 1, report.OrdersCanceled)
	assert.Equal(t, 1, report.OrdersRegionRate)
	assert.Equal(t, 1, report.OrdersOtherCountry)
	require.Len(t, report.Currencies, 1)
	assert.Equal(t, 1, report.Currencies[0].OrdersPriced)
	assert.Equal(t, int64(200), report.Currencies[0].Charged)
	require.Len(t, h.taxes.compared, 1)
	require.Len(t, h.taxes.compared[0].Entries, 2, "the region-rate order is not sent")
	assert.Equal(t, "DE", h.taxes.compared[0].Entries[0].CountryCode)
}

// TestATaxTrialReadsEachRegionOnce: the orders of one region ask the region's
// country once.
func TestATaxTrialReadsEachRegionOnce(t *testing.T) {
	var lines, orders []query.Record
	for i := range 3 {
		id := fmt.Sprintf("order_%d", i)
		lines = append(lines, soldLine("li_"+id, id, testVariantA, 1, 1000, 0, 200, false))
		orders = append(orders, trialOrderRecord(id, "pending", "", 1000, 0))
	}
	h := newTaxTrialHarness(t, lines, orders)

	_, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)

	reads := 0
	for _, spec := range h.catalog.specs {
		if spec.Entity == EntityRegion {
			reads++
		}
	}
	assert.Equal(t, 1, reads)
}

// TestATaxTrialNamesTheLargestChangesFirst: of the moved orders the hundred
// largest changes are named, the largest first and the later of two equal
// ones before the earlier, while the sums cover all of them.
func TestATaxTrialNamesTheLargestChangesFirst(t *testing.T) {
	var lines, orders []query.Record
	for i := range MaxTrialListedOrders {
		id := fmt.Sprintf("order_%03d", i)
		lines = append(lines, soldLine("li_"+id, id, testVariantA, int64(i+1), 1000, 0, 200*int64(i+1), false))
		orders = append(orders, trialOrderRecord(id, "pending", "", 1000*int64(i+1), 0))
	}
	tie := trialOrderRecord("order_tie", "pending", "", 1000, 0)
	tie["placed_at"] = trialFrom.Add(2 * time.Hour)
	orders = append(orders, tie)
	lines = append(lines, soldLine("li_tie", "order_tie", testVariantA, 1, 1000, 0, 200, false))
	h := newTaxTrialHarness(t, lines, orders)

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)

	assert.Equal(t, MaxTrialListedOrders+1, report.Currencies[0].OrdersPriced)
	require.Len(t, report.Orders, MaxTrialListedOrders)
	assert.Equal(t, fmt.Sprintf("order_%03d", MaxTrialListedOrders-1), report.Orders[0].OrderID, "the largest change first")
	assert.Equal(t, "order_001", report.Orders[MaxTrialListedOrders-2].OrderID)
	assert.Equal(t, "order_tie", report.Orders[MaxTrialListedOrders-1].OrderID,
		"of two equal changes the later order first, and the earlier one past the bound")
}

// TestATaxTrialReadsOnlyAnAnswerAboutItsOrders: an answer about other orders,
// other lines, or a line of an order it calls outside the country is an error.
func TestATaxTrialReadsOnlyAnAnswerAboutItsOrders(t *testing.T) {
	h := newTaxTrialHarness(t,
		[]query.Record{soldLine("li_1", "order_a", testVariantA, 1, 1000, 0, 200, false)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
	)
	taxed := `{"rate_id":"taxrate_1","tax_amount":200}`
	line := func(id string) string {
		return `{"id":"` + id + `","reached":true,"baseline":` + taxed + `,"trial":` + taxed + `}`
	}
	for name, raw := range map[string]string{
		"no entry":         `{"entries":[]}`,
		"another order":    `{"entries":[{"reference":"order_z","items":[` + line("li_1") + `]}]}`,
		"no line":          `{"entries":[{"reference":"order_a","items":[]}]}`,
		"another line":     `{"entries":[{"reference":"order_a","items":[` + line("li_9") + `]}]}`,
		"outside, but not": `{"entries":[{"reference":"order_a","outside":true,"items":[` + line("li_1") + `]}]}`,
		"not an object":    `[1]`,
	} {
		h.taxes.compareRaw = json.RawMessage(raw)
		_, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
		require.Error(t, err, name)
		assert.Equal(t, CodeTaxInvalid, errors.CodeOf(err), name)
	}
}

// TestATaxTrialRefusesWhatItCannotAnswer: the rate is required, the period
// has to end after it starts and be at most the trial's bound, a flow with no
// tax module refuses before it reads an order, and the tax module's refusal
// passes through as it was given.
func TestATaxTrialRefusesWhatItCannotAnswer(t *testing.T) {
	h := newTaxTrialHarness(t,
		[]query.Record{soldLine("li_1", "order_a", testVariantA, 1, 1000, 0, 200, false)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
	)
	ctx := context.Background()

	for name, call := range map[string]func() error{
		"no rate": func() error {
			_, err := h.wf.TrialTaxRate(ctx, " ", trialFrom, trialTo, taxTrialChange)
			return err
		},
		"empty": func() error {
			_, err := h.wf.TrialTaxRate(ctx, "taxrate_1", trialFrom, trialFrom, taxTrialChange)
			return err
		},
		"too long": func() error {
			_, err := h.wf.TrialTaxRate(ctx, "taxrate_1", trialFrom, trialFrom.Add(MaxTrialPeriod+time.Hour), taxTrialChange)
			return err
		},
	} {
		err := call()
		require.Error(t, err, name)
		assert.Equal(t, CodeTrialInvalid, errors.CodeOf(err), name)
	}

	h.taxes.compareErr = errors.Conflict("tax_trial_change_refused", "a rule on the default")
	_, err := h.wf.TrialTaxRate(ctx, "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.Error(t, err)
	assert.Equal(t, "tax_trial_change_refused", errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindConflict))

	unwired := newHarnessWith(t, nil, nil)
	unwired.catalog.entities = h.catalog.entities
	_, err = unwired.wf.TrialTaxRate(ctx, "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.Error(t, err)
	assert.Equal(t, CodeTrialInvalid, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal))
	assert.Empty(t, unwired.catalog.specs, "no order is read for a trial that cannot be asked")
}

// TestATaxTrialSaysWhenTypesWereNotRead: a failed read of the products' types
// is not fatal, and the report says every line went without one.
func TestATaxTrialSaysWhenTypesWereNotRead(t *testing.T) {
	h := newTaxTrialHarness(t,
		[]query.Record{soldLine("li_1", "order_a", testVariantA, 1, 1000, 0, 200, false)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 0)},
	)

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)
	assert.NotContains(t, report.Assumptions, taxTrialTypesUnread)

	h.catalog.productErr = errors.Unavailable("catalog_down", "the catalog is away")
	report, err = h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)
	assert.Contains(t, report.Assumptions, taxTrialTypesUnread)
	assert.Equal(t, testProductA, h.taxes.compared[1].Entries[0].Items[0].ProductID, "the product is still sent")
}

// TestATaxTrialSendsNoOrderWithoutATaxableLine: an order whose every line it
// kept as a gift card is neither sent nor priced, and a line discounted past
// its amount is a record no cart made, refused before the tax module is asked.
func TestATaxTrialSendsNoOrderWithoutATaxableLine(t *testing.T) {
	h := newTaxTrialHarness(t,
		[]query.Record{
			soldLine("li_g", "order_gift", testVariantA, 1, 1000, 0, 0, true),
			soldLine("li_1", "order_a", testVariantA, 1, 1000, 0, 200, false),
		},
		[]query.Record{
			trialOrderRecord("order_gift", "pending", "", 1000, 0),
			trialOrderRecord("order_a", "pending", "", 1000, 0),
		},
	)

	report, err := h.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.NoError(t, err)
	require.Len(t, h.taxes.compared, 1)
	require.Len(t, h.taxes.compared[0].Entries, 1, "the gift card order is not sent")
	assert.Equal(t, "order_a", h.taxes.compared[0].Entries[0].Reference)
	require.Len(t, report.Currencies, 1)
	assert.Equal(t, 1, report.Currencies[0].OrdersPriced)
	assert.Equal(t, 1, report.Currencies[0].LinesPriced)

	past := newTaxTrialHarness(t,
		[]query.Record{soldLine("li_1", "order_a", testVariantA, 1, 1000, 1001, 0, false)},
		[]query.Record{trialOrderRecord("order_a", "pending", "", 1000, 1001)},
	)
	_, err = past.wf.TrialTaxRate(context.Background(), "taxrate_1", trialFrom, trialTo, taxTrialChange)
	require.Error(t, err)
	assert.Equal(t, CodeTrialReadInvalid, errors.CodeOf(err))
	assert.Empty(t, past.taxes.compared, "the tax module is not asked")
}
