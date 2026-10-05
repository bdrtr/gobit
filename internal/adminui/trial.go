package adminui

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The trial screens (ADR 0395): a promotion, a price list or a tax rate tried
// on the orders of a past period, through the surface of the module that owns
// it, which answers with the report its admin endpoint answers with (ADR 0176,
// ADR 0220, ADR 0387). Each screen asks for its module's read privilege on the
// route and for the order module's in the handler, because the report is
// orders; nothing is written.

// The trial screens' paths.
const (
	// PromotionTrialPath tries a promotion on past orders.
	PromotionTrialPath = PromotionsPath + "/{id}/trial"
	// PriceListTrialPath tries a price list on past orders.
	PriceListTrialPath = PriceListsPath + "/{id}/trial"
	// TaxRateTrialPath tries a tax rate at another value, or with a rule
	// added, on past orders.
	TaxRateTrialPath = TaxesPath + "/rates/{id}/trial"
)

// PromotionTrial is the narrow surface a promotion is tried through.
type PromotionTrial interface {
	// TrialPromotionJSON prices the promotion against the orders placed in
	// [from, to) and answers the promotion trial's report.
	TrialPromotionJSON(ctx context.Context, id string, from, to time.Time) (json.RawMessage, error)
}

// PriceListTrial is the narrow surface a price list is tried through.
type PriceListTrial interface {
	// TrialPriceListJSON prices the list against the orders placed in
	// [from, to) and answers the price list trial's report.
	TrialPriceListJSON(ctx context.Context, id string, from, to time.Time) (json.RawMessage, error)
}

// TaxRateTrial is the narrow surface a tax rate is tried through.
type TaxRateTrial interface {
	// TrialTaxRateJSON taxes the lines of the orders placed in [from, to) with
	// today's tables and with the change, given as JSON, and answers the tax
	// rate trial's report.
	TrialTaxRateJSON(ctx context.Context, id string, from, to time.Time, change json.RawMessage) (json.RawMessage, error)
}

// The trial form's fields: the period's first and last day, and a tax rate's
// value as a percent, the reference and id of a rule to add, and whether only
// the value is offered.
const (
	paramTrialFrom          = "from"
	paramTrialTo            = "to"
	paramTrialRate          = "rate"
	paramTrialRuleReference = "rule_reference"
	paramTrialRuleID        = "rule_id"
	paramTrialValueOnly     = "value_only"
)

// trialRuleReferences are the references a tried rule can name, as the tax
// module spells them; a rule on shipping is not offered, as the cart taxes no
// shipping.
var trialRuleReferences = []string{"product", "product_type", "tax_class"}

// codeTrialPeriod refuses a trial period the screen cannot read or the flows
// would not run.
const codeTrialPeriod = "admin_ui_trial_period"

// TrialMaxDays is the most days a trial period takes, the last one included:
// the cart flows' MaxTrialPeriod in days, which internal/arch holds equal.
const TrialMaxDays = 93

// canTryKey says whether a screen offers its trial.
const canTryKey = "CanTry"

// trialPeriod is a trial's period as the form carries it and as the surface
// is asked it: the first day from midnight, the last day whole, up to now.
type trialPeriod struct {
	FromDay, ToDay string
	from, to       time.Time
	// asked says both days were sent; a form drawn without them runs nothing.
	asked bool
}

// trialPeriodOf reads the period from the address in the server's zone, as
// the sales report reads its own: the last day is included, so the
// end is the next midnight, and a period reaching into today ends now, since
// a trial reads orders already placed.
//
// The bound is counted in days, as the form offers them. The flows bound the
// period in absolute time, and a period of [TrialMaxDays] days across a change
// of the zone's clock back is an hour longer than that; its start then moves
// by that hour, the one the flows would refuse.
func trialPeriodOf(query url.Values, now time.Time) (trialPeriod, error) {
	today := startOfDay(now)
	period := trialPeriod{
		FromDay: today.AddDate(0, 0, -(salesWindowDays - 1)).Format(dayLayout),
		ToDay:   today.Format(dayLayout),
	}
	fromRaw, toRaw := strings.TrimSpace(query.Get(paramTrialFrom)), strings.TrimSpace(query.Get(paramTrialTo))
	if fromRaw == "" && toRaw == "" {
		return period, nil
	}
	period.FromDay, period.ToDay = fromRaw, toRaw

	from, fromErr := parseDay(fromRaw, now.Location())
	to, toErr := parseDay(toRaw, now.Location())
	if fromErr != nil || toErr != nil {
		return period, errors.Invalid(codeTrialPeriod,
			"The period could not be read: write its first and last day as %s.", dayLayout)
	}
	if days := calendarDays(from, to) + 1; days > TrialMaxDays {
		return period, errors.Invalid(codeTrialPeriod,
			"A trial covers at most %d days, the last one included; this period has %d. Narrow it.",
			TrialMaxDays, days)
	}
	period.from, period.to, period.asked = from, to.AddDate(0, 0, 1), true
	if period.to.After(now) {
		period.to = now
	}
	if widest := TrialMaxDays * 24 * time.Hour; period.to.Sub(period.from) > widest {
		period.from = period.to.Add(-widest)
	}

	return period, nil
}

// calendarDays is how many days the second date falls after the first, by the
// calendar and not by elapsed hours, which a change of the clock bends.
func calendarDays(from, to time.Time) int {
	day := func(at time.Time) time.Time {
		year, month, date := at.Date()
		return time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
	}

	return int(day(to).Sub(day(from)) / (24 * time.Hour))
}

// taxTrialForm is what a tax rate's trial form carries: the value as a
// percent and a rule to add, which a default rate is not offered.
type taxTrialForm struct {
	Rate, RuleReference, RuleID string
	ValueOnly                   bool
	References                  []string
}

// taxRateChange is the change the tax module's surface takes; the json tags
// spell its contract (ADR 0387).
type taxRateChange struct {
	RateBps  *int64       `json:"rate_bps,omitempty"`
	AddRules []taxRuleKey `json:"add_rules,omitempty"`
}

// taxRuleKey is a rule to add.
type taxRuleKey struct {
	Reference   string `json:"reference"`
	ReferenceID string `json:"reference_id"`
}

// change reads the form into the change the surface takes, the value typed as
// a percent; an empty value or rule id is left out, and the tax module refuses
// a change that holds nothing.
func (f taxTrialForm) change() (json.RawMessage, error) {
	var change taxRateChange
	if f.Rate != "" {
		bps, err := parseAmount(f.Rate, 2, false)
		if err != nil {
			return nil, err
		}
		change.RateBps = &bps
	}
	if f.RuleID != "" && !f.ValueOnly {
		change.AddRules = []taxRuleKey{{Reference: f.RuleReference, ReferenceID: f.RuleID}}
	}

	return json.Marshal(change)
}

// promotionTrialReport is the promotion trial's report as the cart flows
// write it (ADR 0176); the json tags spell that contract.
type promotionTrialReport struct {
	PromotionID             string         `json:"promotion_id"`
	From                    time.Time      `json:"from"`
	To                      time.Time      `json:"to"`
	Assumptions             []string       `json:"assumptions"`
	OrdersRead              int            `json:"orders_read"`
	OrdersCanceled          int            `json:"orders_canceled"`
	OrdersAlreadyDiscounted int            `json:"orders_already_discounted"`
	Skipped                 map[string]int `json:"skipped"`
	Currencies              []struct {
		CurrencyCode       string `json:"currency_code"`
		OrdersPriced       int    `json:"orders_priced"`
		OrdersDiscounted   int    `json:"orders_discounted"`
		Subtotal           int64  `json:"subtotal"`
		DiscountTotal      int64  `json:"discount_total"`
		TrialDiscountTotal int64  `json:"trial_discount_total"`
	} `json:"currencies"`
	Orders []struct {
		trialOrderHead
		Subtotal      int64 `json:"subtotal"`
		DiscountTotal int64 `json:"discount_total"`
		TrialDiscount int64 `json:"trial_discount"`
	} `json:"orders"`
}

// priceListTrialReport is the price list trial's report as the cart flows
// write it (ADR 0220).
type priceListTrialReport struct {
	PriceListID    string    `json:"price_list_id"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Assumptions    []string  `json:"assumptions"`
	OrdersRead     int       `json:"orders_read"`
	OrdersCanceled int       `json:"orders_canceled"`
	LinesUnpriced  int       `json:"lines_unpriced"`
	Currencies     []struct {
		CurrencyCode string `json:"currency_code"`
		OrdersPriced int    `json:"orders_priced"`
		LinesPriced  int    `json:"lines_priced"`
		LinesChanged int    `json:"lines_changed"`
		Charged      int64  `json:"charged"`
		Baseline     int64  `json:"baseline"`
		Trial        int64  `json:"trial"`
	} `json:"currencies"`
	Orders []struct {
		trialOrderHead
		Baseline int64 `json:"baseline"`
		Trial    int64 `json:"trial"`
	} `json:"orders"`
}

// taxRateTrialReport is the tax rate trial's report as the cart flows write it
// (ADR 0387).
type taxRateTrialReport struct {
	TaxRateID string `json:"tax_rate_id"`
	Change    struct {
		RateBps   *int64       `json:"rate_bps"`
		AddRules  []taxRuleKey `json:"add_rules"`
		DropRules []string     `json:"drop_rules"`
	} `json:"change"`
	From               time.Time `json:"from"`
	To                 time.Time `json:"to"`
	Assumptions        []string  `json:"assumptions"`
	OrdersRead         int       `json:"orders_read"`
	OrdersCanceled     int       `json:"orders_canceled"`
	OrdersRegionRate   int       `json:"orders_region_rate"`
	OrdersOtherCountry int       `json:"orders_other_country"`
	Currencies         []struct {
		CurrencyCode string `json:"currency_code"`
		OrdersPriced int    `json:"orders_priced"`
		LinesPriced  int    `json:"lines_priced"`
		LinesReached int    `json:"lines_reached"`
		LinesChanged int    `json:"lines_changed"`
		Charged      int64  `json:"charged"`
		Baseline     int64  `json:"baseline"`
		Trial        int64  `json:"trial"`
	} `json:"currencies"`
	Orders []struct {
		trialOrderHead
		Charged  int64 `json:"charged"`
		Baseline int64 `json:"baseline"`
		Trial    int64 `json:"trial"`
	} `json:"orders"`
}

// trialOrderHead is what every report says of an order it names.
type trialOrderHead struct {
	OrderID      string    `json:"order_id"`
	DisplayID    int64     `json:"display_id"`
	CurrencyCode string    `json:"currency_code"`
	PlacedAt     time.Time `json:"placed_at"`
}

// The words every report's counts and columns share.
const (
	trialOrdersRead = "orders read"
	trialCanceled   = "canceled, left out"
	trialCharged    = "Charged"
	trialEffect     = "Effect"
)

// trialCount is one count a report gives, with what it counts.
type trialCount struct {
	Label string
	Count int
}

// trialRow is one currency's sums or one order's figures as the screen draws
// them: the cells under the report's columns, amounts in the currency's
// decimals.
type trialRow struct {
	// OrderID and DisplayID name an order's row, empty on a currency's.
	OrderID   string
	DisplayID int64
	PlacedAt  string
	Cells     []string
}

// trialReport is a report as the screen draws it, whichever module wrote it.
type trialReport struct {
	Counts        []trialCount
	CurrencyHeads []string
	Currencies    []trialRow
	OrderHeads    []string
	Orders        []trialRow
	Assumptions   []string
	// Change is what a tax rate trial tried, as its report echoes it.
	Change string
}

// trialScreen is one trial screen's subject and how it is asked.
type trialScreen struct {
	// Subject names what is tried, for the title.
	Subject string
	// Back is the screen the trial is reached from.
	Back string
	// available is false where the module is not installed; a registered
	// surface without the trial fails the panel's wiring instead.
	available bool
	// run asks the surface.
	run func(ctx context.Context, from, to time.Time) (json.RawMessage, error)
	// draw reads the report into the screen's.
	draw func(raw json.RawMessage, scales map[string]int) (trialReport, error)
	// tax is the tax rate form, nil on the other screens.
	tax *taxTrialForm
}

// trialPromotion tries a promotion on past orders (ADR 0395).
func (u *UI) trialPromotion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u.showTrial(w, r, trialScreen{
		Subject: "promotion " + id, Back: PromotionsPath + "/" + id, available: u.promotionTrials != nil,
		run: func(ctx context.Context, from, to time.Time) (json.RawMessage, error) {
			return u.promotionTrials.TrialPromotionJSON(ctx, id, from, to)
		},
		draw: drawPromotionTrial,
	})
}

// trialPriceList tries a price list on past orders (ADR 0395).
func (u *UI) trialPriceList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	u.showTrial(w, r, trialScreen{
		Subject: "price list " + id, Back: PriceListsPath, available: u.priceListTrials != nil,
		run: func(ctx context.Context, from, to time.Time) (json.RawMessage, error) {
			return u.priceListTrials.TrialPriceListJSON(ctx, id, from, to)
		},
		draw: drawPriceListTrial,
	})
}

// trialTaxRate tries a tax rate at another value, or with a rule added, on
// past orders (ADR 0395).
func (u *UI) trialTaxRate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	query := r.URL.Query()
	form := &taxTrialForm{
		Rate:          strings.TrimSpace(query.Get(paramTrialRate)),
		RuleReference: strings.TrimSpace(query.Get(paramTrialRuleReference)),
		RuleID:        strings.TrimSpace(query.Get(paramTrialRuleID)),
		ValueOnly:     query.Get(paramTrialValueOnly) != "",
		References:    trialRuleReferences,
	}
	u.showTrial(w, r, trialScreen{
		Subject: "tax rate " + id, Back: TaxesPath, available: u.taxTrials != nil, tax: form,
		run: func(ctx context.Context, from, to time.Time) (json.RawMessage, error) {
			change, err := form.change()
			if err != nil {
				return nil, err
			}

			return u.taxTrials.TrialTaxRateJSON(ctx, id, from, to, change)
		},
		draw: drawTaxRateTrial,
	})
}

// showTrial draws a trial screen: the period form, and once both days are
// sent, the report or the module's refusal.
func (u *UI) showTrial(w http.ResponseWriter, r *http.Request, screen trialScreen) {
	if !screen.available {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Trial unavailable",
			"The module's panel surface cannot try this on past orders in this installation.")
		return
	}
	if !canTry(r) {
		u.errorPage(w, r, http.StatusForbidden, "Not permitted",
			"A trial's report is orders, which needs the "+scopeOrderRead+" privilege beside "+
				"this screen's; this account does not carry it. An administrator can grant it.")
		return
	}

	data := map[string]any{
		titleKey:      "Try " + screen.Subject + " on past orders",
		pathKey:       r.URL.Path,
		"Back":        screen.Back,
		ordersPathKey: OrdersPath,
		"Tax":         screen.tax,
	}
	period, err := trialPeriodOf(r.URL.Query(), time.Now())
	data["Period"] = period
	if err != nil {
		data[refusedKey] = messageFor(err)
		u.templates.render(w, r, http.StatusUnprocessableEntity, "trial.gohtml", data)
		return
	}
	if !period.asked {
		u.templates.render(w, r, http.StatusOK, "trial.gohtml", data)
		return
	}

	raw, err := screen.run(r.Context(), period.from, period.to)
	switch {
	case err == nil:
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		data[refusedKey] = messageFor(err)
		u.templates.render(w, r, corehttp.StatusFor(err), "trial.gohtml", data)
		return
	default:
		u.unexpectedFailure(w, r, err, "The trial could not be run")
		return
	}

	report, err := screen.draw(raw, u.currencyScales(r.Context()))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The trial's report could not be read")
		return
	}
	data["Report"] = report
	u.templates.render(w, r, http.StatusOK, "trial.gohtml", data)
}

// canTry reports whether the operator may read a trial's report: its orders
// ask for the order module's read privilege (ADR 0395).
func canTry(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	return principal.HasScope(scopeOrderRead)
}

// decodeTrial reads a report strictly: a field the module adds or renames
// fails here rather than drawing a zero (the D50 class).
func decodeTrial(raw json.RawMessage, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return errors.Wrap(err, errors.KindInternal, "admin_ui_trial_report", "the trial's report could not be read")
	}

	return nil
}

// drawPromotionTrial draws the promotion trial's report.
func drawPromotionTrial(raw json.RawMessage, scales map[string]int) (trialReport, error) {
	var report promotionTrialReport
	if err := decodeTrial(raw, &report); err != nil {
		return trialReport{}, err
	}
	out := trialReport{
		Counts: []trialCount{
			{trialOrdersRead, report.OrdersRead}, {trialCanceled, report.OrdersCanceled},
			{"already discounted by it", report.OrdersAlreadyDiscounted},
		},
		CurrencyHeads: []string{"Orders priced", "Orders it discounts", "Goods", "Discounted", "It adds"},
		OrderHeads:    []string{"Goods", "Discounted", "It adds"},
		Assumptions:   report.Assumptions,
	}
	for _, reason := range slices.Sorted(maps.Keys(report.Skipped)) {
		out.Counts = append(out.Counts, trialCount{"not applied: " + reason, report.Skipped[reason]})
	}
	for _, c := range report.Currencies {
		out.Currencies = append(out.Currencies, trialRow{Cells: []string{
			c.CurrencyCode, strconv.Itoa(c.OrdersPriced), strconv.Itoa(c.OrdersDiscounted),
			minorText(c.Subtotal, c.CurrencyCode, scales), minorText(c.DiscountTotal, c.CurrencyCode, scales),
			minorText(c.TrialDiscountTotal, c.CurrencyCode, scales),
		}})
	}
	for _, o := range report.Orders {
		out.Orders = append(out.Orders, trialOrderRow(o.trialOrderHead, scales,
			o.Subtotal, o.DiscountTotal, o.TrialDiscount))
	}

	return out, nil
}

// drawPriceListTrial draws the price list trial's report.
func drawPriceListTrial(raw json.RawMessage, scales map[string]int) (trialReport, error) {
	var report priceListTrialReport
	if err := decodeTrial(raw, &report); err != nil {
		return trialReport{}, err
	}
	out := trialReport{
		Counts: []trialCount{
			{trialOrdersRead, report.OrdersRead}, {trialCanceled, report.OrdersCanceled},
			{"lines with no price", report.LinesUnpriced},
		},
		CurrencyHeads: []string{"Orders priced", "Lines priced", "Lines changed", trialCharged, "Without it", "With it", trialEffect},
		OrderHeads:    []string{"Without it", "With it", trialEffect},
		Assumptions:   report.Assumptions,
	}
	for _, c := range report.Currencies {
		out.Currencies = append(out.Currencies, trialRow{Cells: []string{
			c.CurrencyCode, strconv.Itoa(c.OrdersPriced), strconv.Itoa(c.LinesPriced), strconv.Itoa(c.LinesChanged),
			minorText(c.Charged, c.CurrencyCode, scales), minorText(c.Baseline, c.CurrencyCode, scales),
			minorText(c.Trial, c.CurrencyCode, scales), minorText(c.Trial-c.Baseline, c.CurrencyCode, scales),
		}})
	}
	for _, o := range report.Orders {
		out.Orders = append(out.Orders, trialOrderRow(o.trialOrderHead, scales, o.Baseline, o.Trial, o.Trial-o.Baseline))
	}

	return out, nil
}

// drawTaxRateTrial draws the tax rate trial's report.
func drawTaxRateTrial(raw json.RawMessage, scales map[string]int) (trialReport, error) {
	var report taxRateTrialReport
	if err := decodeTrial(raw, &report); err != nil {
		return trialReport{}, err
	}
	out := trialReport{
		Counts: []trialCount{
			{trialOrdersRead, report.OrdersRead}, {trialCanceled, report.OrdersCanceled},
			{"taxed at their region's rate, left out", report.OrdersRegionRate},
			{"in another country, left out", report.OrdersOtherCountry},
		},
		CurrencyHeads: []string{
			"Orders taxed", "Lines taxed", "Lines it reaches", "Lines changed", trialCharged, "Today", "Tried", trialEffect,
		},
		OrderHeads:  []string{trialCharged, "Today", "Tried", trialEffect},
		Assumptions: report.Assumptions,
	}
	var tried []string
	if bps := report.Change.RateBps; bps != nil {
		tried = append(tried, "at "+percentText(*bps)+"%")
	}
	for _, rule := range report.Change.AddRules {
		tried = append(tried, "with a rule on "+rule.Reference+" "+rule.ReferenceID)
	}
	for _, id := range report.Change.DropRules {
		tried = append(tried, "without rule "+id)
	}
	out.Change = strings.Join(tried, ", ")
	for _, c := range report.Currencies {
		out.Currencies = append(out.Currencies, trialRow{Cells: []string{
			c.CurrencyCode, strconv.Itoa(c.OrdersPriced), strconv.Itoa(c.LinesPriced),
			strconv.Itoa(c.LinesReached), strconv.Itoa(c.LinesChanged),
			minorText(c.Charged, c.CurrencyCode, scales), minorText(c.Baseline, c.CurrencyCode, scales),
			minorText(c.Trial, c.CurrencyCode, scales), minorText(c.Trial-c.Baseline, c.CurrencyCode, scales),
		}})
	}
	for _, o := range report.Orders {
		out.Orders = append(out.Orders, trialOrderRow(o.trialOrderHead, scales,
			o.Charged, o.Baseline, o.Trial, o.Trial-o.Baseline))
	}

	return out, nil
}

// trialOrderRow draws one order's row: its amounts in its currency's decimals.
func trialOrderRow(head trialOrderHead, scales map[string]int, amounts ...int64) trialRow {
	row := trialRow{OrderID: head.OrderID, DisplayID: head.DisplayID, PlacedAt: head.PlacedAt.UTC().Format("2006-01-02 15:04")}
	for _, amount := range amounts {
		row.Cells = append(row.Cells, minorText(amount, head.CurrencyCode, scales))
	}

	return row
}
