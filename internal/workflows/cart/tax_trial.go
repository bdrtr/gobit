package cart

import (
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// taxTrialFlowAssumptions are what THIS side of a tax rate trial sets aside
// (ADR 0387), published beside the tax module's own: a line's product and
// type are read from today's catalog, and the country from the region's
// countries today.
var taxTrialFlowAssumptions = []string{"todays_catalog", "todays_region_countries"}

// taxTrialTypesUnread is the assumption a trial adds when the products' types
// could not be read: every line went without one, so a rule on a type moved
// nothing, and the report must not present that zero as a measurement.
const taxTrialTypesUnread = "product_types_unread"

// TaxRateTrialReport is what a tax rate, amended, would have charged on the
// orders of a period.
type TaxRateTrialReport struct {
	TaxRateID string `json:"tax_rate_id"`
	// Change is the change tried, as the tax module sent it.
	Change json.RawMessage `json:"change"`
	From   time.Time       `json:"from"`
	To     time.Time       `json:"to"`
	// Assumptions are what the trial set aside, the tax module's and this
	// flow's.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period; OrdersCanceled are the
	// ones left out because they were canceled.
	OrdersRead     int `json:"orders_read"`
	OrdersCanceled int `json:"orders_canceled"`
	// OrdersRegionRate are the orders whose region does not resolve to one
	// country today, which a cart taxes at the region's flat rate.
	OrdersRegionRate int `json:"orders_region_rate"`
	// OrdersOtherCountry are the orders outside the rate's country.
	OrdersOtherCountry int `json:"orders_other_country"`
	// Currencies are the sums, one entry per currency, in code order.
	Currencies []TaxRateTrialCurrency `json:"currencies"`
	// Orders are the orders the change moves, the largest change either way
	// first, at most [MaxTrialListedOrders].
	Orders []TaxRateTrialOrder `json:"orders"`
}

// TaxRateTrialCurrency sums the taxed lines of one currency.
type TaxRateTrialCurrency struct {
	CurrencyCode string `json:"currency_code"`
	OrdersPriced int    `json:"orders_priced"`
	LinesPriced  int    `json:"lines_priced"`
	// LinesReached are the lines the rate takes part in, before or after the
	// change; LinesChanged are the ones whose tax the change moves.
	LinesReached int `json:"lines_reached"`
	LinesChanged int `json:"lines_changed"`
	// Charged is the tax the lines were sold with, Baseline what today's
	// tables take and Trial what the amended table takes; the change's effect
	// is Trial minus Baseline.
	Charged  int64 `json:"charged"`
	Baseline int64 `json:"baseline"`
	Trial    int64 `json:"trial"`
}

// TaxRateTrialOrder is one order the change moves.
type TaxRateTrialOrder struct {
	OrderID      string    `json:"order_id"`
	DisplayID    int64     `json:"display_id"`
	CurrencyCode string    `json:"currency_code"`
	PlacedAt     time.Time `json:"placed_at"`
	Charged      int64     `json:"charged"`
	Baseline     int64     `json:"baseline"`
	Trial        int64     `json:"trial"`
}

// taxCompareRequest and taxCompareResponse are the consumer-side copies of the
// tax module's comparison schema; the producing side documents it.
type taxCompareRequest struct {
	Entries []taxCompareEntry `json:"entries"`
}

type taxCompareEntry struct {
	Reference   string           `json:"reference"`
	CountryCode string           `json:"country_code"`
	Items       []taxRequestItem `json:"items"`
}

type taxCompareResponse struct {
	Assumptions []string `json:"assumptions"`
	Entries     []struct {
		Reference string `json:"reference"`
		Outside   bool   `json:"outside"`
		Items     []struct {
			ID       string         `json:"id"`
			Reached  bool           `json:"reached"`
			Baseline taxCompareLine `json:"baseline"`
			Trial    taxCompareLine `json:"trial"`
		} `json:"items"`
	} `json:"entries"`
}

type taxCompareLine struct {
	RateID    string `json:"rate_id"`
	TaxAmount int64  `json:"tax_amount"`
}

// TrialTaxRate taxes the lines of the orders placed in [from, to) twice, with
// today's tables and with a tax rate amended by change, and writes nothing
// (ADR 0387).
//
// # What is sent
//
// Which lines were sent, and at what amount, is the sale's: each line of an
// order that was not canceled, at its unit price times its quantity less its
// discount, a line the order kept as a gift card left out (ADR 0211). How
// they are taxed is today's: the country the order's region resolves to, no
// province, the product and type the catalog gives the variant, and the tax
// module's tables. An order whose region does not resolve to one country was
// taxed at the region's rate and is counted apart.
func (w *Workflows) TrialTaxRate(
	ctx context.Context, rateID string, from, to time.Time, change json.RawMessage,
) (TaxRateTrialReport, error) {
	if strings.TrimSpace(rateID) == "" {
		return TaxRateTrialReport{}, errors.Invalid(CodeTrialInvalid, "the tax rate to try is required")
	}
	if !from.Before(to) {
		return TaxRateTrialReport{}, errors.Invalid(CodeTrialInvalid,
			"the period has to end after it starts: from %s, to %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	if to.Sub(from) > MaxTrialPeriod {
		return TaxRateTrialReport{}, errors.Invalid(CodeTrialInvalid,
			"a trial covers at most %d days; narrow the period", int(MaxTrialPeriod/(24*time.Hour)))
	}
	// Refused before the walk: a trial that cannot ask the tax module must not
	// read a period of orders first.
	if w.taxes == nil {
		return TaxRateTrialReport{}, errors.Internal(CodeTrialInvalid,
			"the tax module is not wired, so no tax rate can be tried")
	}

	orders, err := w.trialOrders(ctx, from, to)
	if err != nil {
		return TaxRateTrialReport{}, err
	}
	report := TaxRateTrialReport{
		TaxRateID: rateID, Change: change, From: from.UTC(), To: to.UTC(), OrdersRead: len(orders),
		Currencies: []TaxRateTrialCurrency{}, Orders: []TaxRateTrialOrder{},
	}

	countries := map[string]string{}
	taxed := make([]trialOrder, 0, len(orders))
	for i := range orders {
		if orders[i].status == trialOrderCanceled {
			report.OrdersCanceled++

			continue
		}
		country, known := countries[orders[i].regionID]
		if !known {
			code, reason, err := w.countryForRegion(ctx, orders[i].regionID)
			if err != nil {
				return TaxRateTrialReport{}, err
			}
			if reason == "" {
				country = code
			}
			countries[orders[i].regionID] = country
		}
		if country == "" {
			report.OrdersRegionRate++

			continue
		}
		taxed = append(taxed, orders[i])
	}

	request, sent, typesRead, err := w.taxCompareRequestFor(ctx, taxed, countries)
	if err != nil {
		return TaxRateTrialReport{}, err
	}
	answer, err := w.taxCompareAnswer(ctx, rateID, change, request, sent)
	if err != nil {
		return TaxRateTrialReport{}, err
	}
	report.Assumptions = append(slices.Clone(answer.Assumptions), taxTrialFlowAssumptions...)
	if !typesRead {
		report.Assumptions = append(report.Assumptions, taxTrialTypesUnread)
	}

	sums := map[string]*TaxRateTrialCurrency{}
	for k := range sent {
		order, entry := &sent[k], &answer.Entries[k]
		if entry.Outside {
			report.OrdersOtherCountry++

			continue
		}
		sum := sums[order.currencyCode]
		if sum == nil {
			sum = &TaxRateTrialCurrency{CurrencyCode: order.currencyCode}
			sums[order.currencyCode] = sum
		}
		sum.OrdersPriced++
		var charged, baseline, trial int64
		for j, line := range order.lines {
			item := &entry.Items[j]
			sum.LinesPriced++
			charged += line.taxTotal
			baseline += item.Baseline.TaxAmount
			trial += item.Trial.TaxAmount
			if item.Reached {
				sum.LinesReached++
			}
			if item.Baseline.TaxAmount != item.Trial.TaxAmount {
				sum.LinesChanged++
			}
		}
		sum.Charged += charged
		sum.Baseline += baseline
		sum.Trial += trial
		if baseline != trial {
			report.Orders = append(report.Orders, TaxRateTrialOrder{
				OrderID: order.id, DisplayID: order.displayID, CurrencyCode: order.currencyCode,
				PlacedAt: order.placedAt.UTC(), Charged: charged, Baseline: baseline, Trial: trial,
			})
		}
	}

	for _, code := range slices.Sorted(maps.Keys(sums)) {
		report.Currencies = append(report.Currencies, *sums[code])
	}
	slices.SortStableFunc(report.Orders, func(a, b TaxRateTrialOrder) int {
		return cmp.Or(cmp.Compare(absolute(b.Trial-b.Baseline), absolute(a.Trial-a.Baseline)), b.PlacedAt.Compare(a.PlacedAt))
	})
	if len(report.Orders) > MaxTrialListedOrders {
		report.Orders = report.Orders[:MaxTrialListedOrders]
	}

	return report, nil
}

// taxCompareRequestFor builds one comparison entry per order with a line to
// send, and returns beside it each entry's order holding only the lines sent.
//
// The products are read in one catalog read and their types in a second; a
// failure of the first is fatal, since a line's product is what a rule on a
// product matches, while a failure of the second is reported as an
// assumption, as a cart prices a line whose facts could not be read.
func (w *Workflows) taxCompareRequestFor(
	ctx context.Context, orders []trialOrder, countries map[string]string,
) (taxCompareRequest, []trialOrder, bool, error) {
	var variantIDs []string
	seen := map[string]bool{}
	for i := range orders {
		for _, line := range orders[i].lines {
			if !line.giftCard && !seen[line.variantID] {
				seen[line.variantID] = true
				variantIDs = append(variantIDs, line.variantID)
			}
		}
	}
	productIDs, err := w.productIDsFor(ctx, variantIDs)
	if err != nil {
		return taxCompareRequest{}, nil, false, err
	}
	types, typesRead := map[string]string{}, true
	var facts map[string]productFacts
	if len(productIDs) > 0 {
		var factsErr error
		facts, factsErr = w.productFactsFor(ctx, uniqueProductIDs(productIDs))
		if factsErr != nil {
			w.log.WarnContext(ctx, "the products' types could not be read; trying without them",
				"error", factsErr, "products", len(productIDs))
			typesRead = false
		}
		for variantID, productID := range productIDs {
			types[variantID] = facts[productID].TypeID
		}
	}

	request := taxCompareRequest{Entries: make([]taxCompareEntry, 0, len(orders))}
	sent := make([]trialOrder, 0, len(orders))
	for i := range orders {
		order := orders[i]
		order.lines = nil
		entry := taxCompareEntry{Reference: order.id, CountryCode: countries[order.regionID]}
		for _, line := range orders[i].lines {
			// The order's own flag, as the line was sold: the cart left a gift
			// card out by the product's flag at the moment of sale, which is
			// what the order kept, and today's product may say otherwise.
			if line.giftCard {
				continue
			}
			amount := line.subtotal - line.discountTotal
			if amount < 0 {
				return taxCompareRequest{}, nil, false, errors.Internal(CodeTrialReadInvalid,
					"line %s of order %s is discounted past its amount", line.id, order.id)
			}
			entry.Items = append(entry.Items, taxRequestItem{
				ID: line.id, ProductID: productIDs[line.variantID], ProductTypeID: types[line.variantID], Amount: amount,
			})
			order.lines = append(order.lines, line)
		}
		if len(entry.Items) == 0 {
			continue
		}
		request.Entries = append(request.Entries, entry)
		sent = append(sent, order)
	}

	return request, sent, typesRead, nil
}

// taxCompareAnswer asks the tax module and checks that the answer is about the
// orders and their lines. A refusal of the tax module passes through as it
// is: a change it refuses is the caller's, not this flow's.
func (w *Workflows) taxCompareAnswer(
	ctx context.Context, rateID string, change json.RawMessage, request taxCompareRequest, sent []trialOrder,
) (taxCompareResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return taxCompareResponse{}, errors.Wrap(err, errors.KindInternal, CodeTaxInvalid,
			"the comparison request could not be encoded")
	}
	raw, err := w.taxes.CompareRateJSON(ctx, rateID, change, payload)
	if err != nil {
		return taxCompareResponse{}, err
	}
	var answer taxCompareResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return taxCompareResponse{}, errors.Wrap(err, errors.KindInternal, CodeTaxInvalid,
			"the comparison could not be decoded")
	}
	if len(answer.Entries) != len(request.Entries) {
		return taxCompareResponse{}, errors.Internal(CodeTaxInvalid,
			"for %d orders the comparison returned %d", len(request.Entries), len(answer.Entries))
	}
	for k := range answer.Entries {
		entry := &answer.Entries[k]
		if entry.Reference != request.Entries[k].Reference {
			return taxCompareResponse{}, errors.Internal(CodeTaxInvalid,
				"the comparison's answer %d is about %q and %q was asked", k, entry.Reference, request.Entries[k].Reference)
		}
		if entry.Outside {
			if len(entry.Items) != 0 {
				return taxCompareResponse{}, errors.Internal(CodeTaxInvalid,
					"the comparison taxed order %s and called it outside the rate's country", entry.Reference)
			}

			continue
		}
		if len(entry.Items) != len(sent[k].lines) {
			return taxCompareResponse{}, errors.Internal(CodeTaxInvalid,
				"the comparison taxed %d lines of order %s, which sent %d", len(entry.Items), entry.Reference, len(sent[k].lines))
		}
		for j := range entry.Items {
			if entry.Items[j].ID != sent[k].lines[j].id {
				return taxCompareResponse{}, errors.Internal(CodeTaxInvalid,
					"the comparison's line %d of order %s is %q, expected %q",
					j, entry.Reference, entry.Items[j].ID, sent[k].lines[j].id)
			}
		}
	}

	return answer, nil
}
