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

// listTrialAssumptions are what a price list trial sets aside (ADR 0220),
// published beside its figures.
//
// A line is priced as it would be today: with today's link from its variant to
// a price set, today's prices of every other list and of the base, and the
// customer's groups and company as they are now. The list itself is offered as
// if active with no window. A promotion is not recomputed: the figures are the
// unit prices before any discount. The sales channel the cart was opened in is
// a fact of the sale an order does not keep, so a price ruled on it matches no
// order (ADR 0397). The cart's metadata is not set aside: it chooses no cart's
// price either (ADR 0403).
var listTrialAssumptions = []string{
	"todays_prices", "todays_price_set_links", "todays_customer_groups", "list_active_without_window",
	"before_discounts", "no_sales_channel",
}

// PriceListTrialReport is what a price list would have done to the prices of
// the orders of a period.
type PriceListTrialReport struct {
	PriceListID string    `json:"price_list_id"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	// Assumptions are what the trial set aside.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period; OrdersCanceled are the ones
	// left out.
	OrdersRead     int `json:"orders_read"`
	OrdersCanceled int `json:"orders_canceled"`
	// LinesUnpriced are the lines no price could be found for today, with the
	// list or without it: a variant with no price set or with two, or no price
	// in the order's currency.
	LinesUnpriced int `json:"lines_unpriced"`
	// Currencies are the sums, one entry per currency, in code order.
	Currencies []PriceListTrialCurrency `json:"currencies"`
	// Orders are the orders whose goods the list changes, the largest change
	// either way first, at most [MaxTrialListedOrders].
	Orders []PriceListTrialOrder `json:"orders"`
}

// PriceListTrialCurrency sums the priced lines of one currency.
type PriceListTrialCurrency struct {
	CurrencyCode string `json:"currency_code"`
	OrdersPriced int    `json:"orders_priced"`
	LinesPriced  int    `json:"lines_priced"`
	// LinesChanged are the lines the list gives another unit price.
	LinesChanged int `json:"lines_changed"`
	// Charged is what the priced lines were sold at, before discounts.
	Charged int64 `json:"charged"`
	// Baseline is what they would cost today without the list, and Trial with
	// it; the list's effect is Trial minus Baseline.
	Baseline int64 `json:"baseline"`
	Trial    int64 `json:"trial"`
}

// PriceListTrialOrder is one order whose goods the list changes.
type PriceListTrialOrder struct {
	OrderID      string    `json:"order_id"`
	DisplayID    int64     `json:"display_id"`
	CurrencyCode string    `json:"currency_code"`
	PlacedAt     time.Time `json:"placed_at"`
	Baseline     int64     `json:"baseline"`
	Trial        int64     `json:"trial"`
}

// compareRequest and compareResponse are the consumer-side copies of the
// pricing module's comparison schema; the producing side documents it.
type compareRequest struct {
	Entries []compareEntry `json:"entries"`
}

type compareEntry struct {
	Reference    string             `json:"reference"`
	CurrencyCode string             `json:"currency_code"`
	Attributes   map[string]string  `json:"attributes"`
	Items        []priceRequestItem `json:"items"`
}

type compareResponse struct {
	Entries []struct {
		Reference string `json:"reference"`
		Items     []struct {
			Baseline priceResponseItem `json:"baseline"`
			Trial    priceResponseItem `json:"trial"`
		} `json:"items"`
	} `json:"entries"`
}

// TrialPriceList prices the goods of the orders placed in [from, to) with the
// price list as if it had been active and without it, as the ladder would
// today, and writes nothing (ADR 0220).
//
// # What is compared
//
// Each order that was not canceled is priced line by line, at the line's
// quantity, in the order's currency, with the price context a cart of that
// customer in that region carries, less the sales channel an order does not keep
// (ADR 0397, ADR 0403).
// The list's effect is the difference between the two prices; what the line was
// sold at is reported beside them, since today's ladder is not the one of the
// sale.
func (w *Workflows) TrialPriceList(
	ctx context.Context, listID string, from, to time.Time,
) (PriceListTrialReport, error) {
	if strings.TrimSpace(listID) == "" {
		return PriceListTrialReport{}, errors.Invalid(CodeTrialInvalid, "the price list to try is required")
	}
	if !from.Before(to) {
		return PriceListTrialReport{}, errors.Invalid(CodeTrialInvalid,
			"the period has to end after it starts: from %s, to %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	if to.Sub(from) > MaxTrialPeriod {
		return PriceListTrialReport{}, errors.Invalid(CodeTrialInvalid,
			"a trial covers at most %d days; narrow the period", int(MaxTrialPeriod/(24*time.Hour)))
	}

	orders, err := w.trialOrders(ctx, from, to)
	if err != nil {
		return PriceListTrialReport{}, err
	}
	report := PriceListTrialReport{
		PriceListID: listID, From: from.UTC(), To: to.UTC(), Assumptions: slices.Clone(listTrialAssumptions),
		OrdersRead: len(orders), Currencies: []PriceListTrialCurrency{}, Orders: []PriceListTrialOrder{},
	}
	var sold []trialOrder
	for i := range orders {
		if orders[i].status == trialOrderCanceled {
			report.OrdersCanceled++

			continue
		}
		sold = append(sold, orders[i])
	}

	sets, err := w.lineSets(ctx, sold)
	if err != nil {
		return PriceListTrialReport{}, err
	}
	request, err := w.compareRequestFor(ctx, sold, sets)
	if err != nil {
		return PriceListTrialReport{}, err
	}
	answer, err := w.compareAnswer(ctx, listID, request)
	if err != nil {
		return PriceListTrialReport{}, err
	}

	sums := map[string]*PriceListTrialCurrency{}
	for i := range sold {
		order, entry := &sold[i], &answer.Entries[i]
		sum := sums[order.currencyCode]
		if sum == nil {
			sum = &PriceListTrialCurrency{CurrencyCode: order.currencyCode}
			sums[order.currencyCode] = sum
		}
		var baseline, trial int64
		priced, next := false, 0
		for _, line := range order.lines {
			if _, ok := sets[line.variantID]; !ok {
				report.LinesUnpriced++

				continue
			}
			item := entry.Items[next]
			next++
			if !item.Baseline.Priced || !item.Trial.Priced {
				report.LinesUnpriced++

				continue
			}
			priced = true
			sum.LinesPriced++
			sum.Charged += line.unitPrice * line.quantity
			sum.Baseline += item.Baseline.Amount * line.quantity
			sum.Trial += item.Trial.Amount * line.quantity
			baseline += item.Baseline.Amount * line.quantity
			trial += item.Trial.Amount * line.quantity
			if item.Baseline.Amount != item.Trial.Amount {
				sum.LinesChanged++
			}
		}
		if !priced {
			continue
		}
		sum.OrdersPriced++
		if baseline != trial {
			report.Orders = append(report.Orders, PriceListTrialOrder{
				OrderID: order.id, DisplayID: order.displayID, CurrencyCode: order.currencyCode,
				PlacedAt: order.placedAt.UTC(), Baseline: baseline, Trial: trial,
			})
		}
	}

	for _, code := range slices.Sorted(maps.Keys(sums)) {
		report.Currencies = append(report.Currencies, *sums[code])
	}
	slices.SortStableFunc(report.Orders, func(a, b PriceListTrialOrder) int {
		return cmp.Or(cmp.Compare(absolute(b.Trial-b.Baseline), absolute(a.Trial-a.Baseline)), b.PlacedAt.Compare(a.PlacedAt))
	})
	if len(report.Orders) > MaxTrialListedOrders {
		report.Orders = report.Orders[:MaxTrialListedOrders]
	}

	return report, nil
}

// lineSets reads today's price set of every variant the orders sold, in one
// read; a variant with none or with two is left out and its lines unpriced.
func (w *Workflows) lineSets(ctx context.Context, orders []trialOrder) (map[string]string, error) {
	var variantIDs []string
	seen := map[string]bool{}
	for i := range orders {
		for _, line := range orders[i].lines {
			if !seen[line.variantID] {
				seen[line.variantID] = true
				variantIDs = append(variantIDs, line.variantID)
			}
		}
	}
	out := map[string]string{}
	if len(variantIDs) == 0 {
		return out, nil
	}
	linked, err := w.links.ListMany(ctx, LinkVariantPriceSet, variantIDs)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkReadFailed,
			"could not read the %q link (%d variants)", LinkVariantPriceSet, len(variantIDs))
	}
	for variantID, setIDs := range linked {
		if len(setIDs) == 1 {
			out[variantID] = setIDs[0]
		}
	}
	return out, nil
}

// compareRequestFor builds one comparison entry per order, with the price
// context a cart of its customer in its region carries, less the sales channel
// an order does not keep. The context is read once per customer, region and
// currency.
func (w *Workflows) compareRequestFor(
	ctx context.Context, orders []trialOrder, sets map[string]string,
) (compareRequest, error) {
	contexts := map[[3]string]map[string]string{}
	request := compareRequest{Entries: make([]compareEntry, 0, len(orders))}
	for i := range orders {
		order := &orders[i]
		key := [3]string{order.customerID, order.regionID, order.currencyCode}
		attributes, ok := contexts[key]
		if !ok {
			var contextErr error
			attributes, _, contextErr = w.priceContext(ctx, priceSubject{RegionID: order.regionID, CustomerID: order.customerID})
			if contextErr != nil {
				w.log.WarnContext(ctx, "the customer's groups or company could not be read; trying without them",
					"error", contextErr, "customer_id", order.customerID)
			}
			contexts[key] = attributes
		}
		entry := compareEntry{Reference: order.id, CurrencyCode: order.currencyCode, Attributes: attributes}
		for _, line := range order.lines {
			setID, ok := sets[line.variantID]
			if !ok {
				continue
			}
			quantity, err := quantity32(line.quantity)
			if err != nil {
				return compareRequest{}, errors.Wrap(err, errors.KindInternal, CodeTrialReadInvalid,
					"order %s holds a line of a quantity no cart holds", order.id)
			}
			entry.Items = append(entry.Items, priceRequestItem{PriceSetID: setID, Quantity: quantity})
		}
		request.Entries = append(request.Entries, entry)
	}
	return request, nil
}

// compareAnswer asks the pricing module and checks that the answer is about
// the orders and their lines.
func (w *Workflows) compareAnswer(ctx context.Context, listID string, request compareRequest) (compareResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return compareResponse{}, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the comparison request could not be encoded")
	}
	raw, err := w.prices.CompareListJSON(ctx, listID, payload)
	if err != nil {
		return compareResponse{}, err
	}
	var answer compareResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return compareResponse{}, errors.Wrap(err, errors.KindInternal, CodePriceResponseInvalid,
			"the comparison could not be decoded")
	}
	if len(answer.Entries) != len(request.Entries) {
		return compareResponse{}, errors.Internal(CodePriceResponseInvalid,
			"for %d orders the comparison returned %d", len(request.Entries), len(answer.Entries))
	}
	for i := range answer.Entries {
		if answer.Entries[i].Reference != request.Entries[i].Reference ||
			len(answer.Entries[i].Items) != len(request.Entries[i].Items) {
			return compareResponse{}, errors.Internal(CodePriceResponseInvalid,
				"the comparison's answer for order %s does not match its lines", request.Entries[i].Reference)
		}
	}
	return answer, nil
}

// absolute is a change's size either way.
func absolute(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
