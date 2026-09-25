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
	"github.com/bdrtr/gobit/core/query"
)

// Error codes of the promotion trial.
const (
	// CodeTrialInvalid reports a trial asked for with an unusable period or
	// promotion.
	CodeTrialInvalid = "cart_workflow_trial_invalid"
	// CodeTrialTooWide reports a period holding more orders than one trial
	// prices.
	CodeTrialTooWide = "cart_workflow_trial_too_wide"
	// CodeTrialReadInvalid reports a record of the order entities that does
	// not carry what the trial reads — a contract drift, not the caller's
	// fault.
	CodeTrialReadInvalid = "cart_workflow_trial_read_invalid"
	// CodeTrialResultInvalid reports a promotion answer that does not match
	// the purchases it was asked about.
	CodeTrialResultInvalid = "cart_workflow_trial_result_invalid"
)

// The trial's bounds.
const (
	// MaxTrialPeriod is the widest period one trial reads.
	MaxTrialPeriod = 93 * 24 * time.Hour
	// MaxTrialOrders is the most orders one trial prices. It is the promotion
	// module's own bound on a trial (service.MaxTrialEntries), repeated here
	// because this package cannot import it; the flow refuses before asking,
	// so the promotion module's refusal is never what a caller sees.
	MaxTrialOrders = 5000
	// MaxTrialListedOrders is how many of the discounted orders the report
	// names. The counts and the sums cover every order; the list is the
	// largest discounts, for an operator to open and look at.
	MaxTrialListedOrders = 100
)

// The order entities the trial reads through the Query layer. Their names and
// fields are the order module's contract (ADR 0004); a drift fails the read
// with the provider's own refusal.
const (
	trialLineEntity  = "order_line_item"
	trialOrderEntity = "order"
	// trialPageSize is the order module's page ceiling; asking for more is
	// clamped silently, so the walk has to know the real size to stop.
	trialPageSize = 100
	// trialOrderCanceled is the order status the trial leaves out: a canceled
	// order sold nothing, and pricing a discount on it would inflate the
	// report.
	trialOrderCanceled = "canceled"
)

// trialFlowAssumptions are what THIS side of the trial sets aside, published
// beside the promotion module's own.
//
// A purchase is rebuilt from its order, and three facts of the moment of sale
// are not kept anywhere: the product's categories, tags and collection, the
// customer's groups, and the cart's own metadata. The first two are read as they
// are TODAY; the third is absent, so a rule on a `cart.` attribute matches no
// order.
var trialFlowAssumptions = []string{"todays_catalog", "todays_customer_groups", "no_cart_metadata"}

// trialRequest is the consumer-side copy of the promotion module's trial
// request schema; the producing side documents it.
type trialRequest struct {
	Entries []trialRequestEntry `json:"entries"`
}

// trialRequestEntry is one purchase in the trial request.
type trialRequestEntry struct {
	Reference string          `json:"reference"`
	Request   discountRequest `json:"request"`
}

// trialResponse is the consumer-side copy of the promotion module's trial
// response schema.
type trialResponse struct {
	Assumptions []string             `json:"assumptions"`
	Entries     []trialResponseEntry `json:"entries"`
}

// trialResponseEntry is one purchase in the trial response.
type trialResponseEntry struct {
	Reference          string         `json:"reference"`
	AlreadyApplied     bool           `json:"already_applied"`
	Skipped            string         `json:"skipped"`
	Items              []discountLine `json:"items"`
	ItemsDiscountTotal int64          `json:"items_discount_total"`
}

// TrialReport is what a promotion would have done to the orders of a period.
type TrialReport struct {
	// PromotionID is the promotion under trial.
	PromotionID string `json:"promotion_id"`
	// From and To are the period, half open: orders placed at From or later
	// and before To.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Assumptions are what the trial set aside, the promotion module's and
	// this flow's.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period.
	OrdersRead int `json:"orders_read"`
	// OrdersCanceled are the ones left out because they were canceled.
	OrdersCanceled int `json:"orders_canceled"`
	// OrdersAlreadyDiscounted are the ones the promotion itself was redeemed
	// on. Their discount is already in them and is not priced again.
	OrdersAlreadyDiscounted int `json:"orders_already_discounted"`
	// Skipped counts the priced orders the promotion would not have applied
	// to, by the engine's reason.
	Skipped map[string]int `json:"skipped"`
	// Currencies are the sums, one entry per currency, in code order.
	Currencies []TrialCurrency `json:"currencies"`
	// Orders are the discounted orders with the largest trial discount first,
	// at most [MaxTrialListedOrders].
	Orders []TrialOrder `json:"orders"`
}

// TrialCurrency sums the priced orders of one currency.
type TrialCurrency struct {
	CurrencyCode string `json:"currency_code"`
	// OrdersPriced are the orders the promotion was priced against.
	OrdersPriced int `json:"orders_priced"`
	// OrdersDiscounted are the ones it would have taken something off.
	OrdersDiscounted int `json:"orders_discounted"`
	// Subtotal is the priced orders' goods before any discount.
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is what the priced orders were actually discounted.
	DiscountTotal int64 `json:"discount_total"`
	// TrialDiscountTotal is what the promotion would have ADDED to that.
	TrialDiscountTotal int64 `json:"trial_discount_total"`
}

// TrialOrder is one order the promotion would have discounted.
type TrialOrder struct {
	OrderID       string    `json:"order_id"`
	DisplayID     int64     `json:"display_id"`
	CurrencyCode  string    `json:"currency_code"`
	PlacedAt      time.Time `json:"placed_at"`
	Subtotal      int64     `json:"subtotal"`
	DiscountTotal int64     `json:"discount_total"`
	TrialDiscount int64     `json:"trial_discount"`
}

// trialLine is one sold line as the trial reads it.
type trialLine struct {
	id            string
	variantID     string
	quantity      int64
	unitPrice     int64
	subtotal      int64
	discountTotal int64
}

// trialOrder is one order with its lines, in the order the lines were read.
type trialOrder struct {
	id            string
	displayID     int64
	status        string
	regionID      string
	customerID    string
	cartID        string
	currencyCode  string
	placedAt      time.Time
	subtotal      int64
	discountTotal int64
	lines         []trialLine
}

// TrialPromotion prices a promotion against the orders placed in [from, to) as if
// it had been published then, and writes nothing (ADR 0176).
//
// # What is priced
//
// Each order that was not canceled is rebuilt as the purchase it was — its lines
// at the prices charged, its region and its customer — and handed to the promotion
// module in the shape [Workflows.discountRequestFor] gives a cart, so a rule sees
// an order exactly as it would have seen the cart. What the moment of sale did not
// keep is read as it is today, and the report says so ([trialFlowAssumptions]).
//
// # What the report adds up
//
// The promotion is priced ALONE, and what it would have ADDED to each order is
// derived from the discount the order actually got. The engine's discounts add up
// rather than compound and are capped at the line's amount, so on a line the new
// total is min(actual + trial, subtotal) whatever the order of application; the
// promotion adds that minus the actual. No other promotion is recomputed, and the
// answer does not depend on today's state of them.
func (w *Workflows) TrialPromotion(
	ctx context.Context, promotionID string, from, to time.Time,
) (TrialReport, error) {
	if strings.TrimSpace(promotionID) == "" {
		return TrialReport{}, errors.Invalid(CodeTrialInvalid, "the promotion to try is required")
	}
	if !from.Before(to) {
		return TrialReport{}, errors.Invalid(CodeTrialInvalid,
			"the period has to end after it starts: from %s, to %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	if to.Sub(from) > MaxTrialPeriod {
		return TrialReport{}, errors.Invalid(CodeTrialInvalid,
			"a trial covers at most %d days; narrow the period", int(MaxTrialPeriod/(24*time.Hour)))
	}

	orders, err := w.trialOrders(ctx, from, to)
	if err != nil {
		return TrialReport{}, err
	}

	report := TrialReport{
		PromotionID: promotionID,
		From:        from.UTC(),
		To:          to.UTC(),
		OrdersRead:  len(orders),
		Skipped:     map[string]int{},
		Currencies:  []TrialCurrency{},
		Orders:      []TrialOrder{},
	}

	priced := make([]trialOrder, 0, len(orders))
	for i := range orders {
		if orders[i].status == trialOrderCanceled {
			report.OrdersCanceled++
			continue
		}
		priced = append(priced, orders[i])
	}

	answer, err := w.trialAnswer(ctx, promotionID, priced)
	if err != nil {
		return TrialReport{}, err
	}
	report.Assumptions = append(slices.Clone(answer.Assumptions), trialFlowAssumptions...)

	sums := map[string]*TrialCurrency{}
	for i := range priced {
		order, entry := priced[i], answer.Entries[i]
		if entry.AlreadyApplied {
			report.OrdersAlreadyDiscounted++
			continue
		}

		sum := sums[order.currencyCode]
		if sum == nil {
			sum = &TrialCurrency{CurrencyCode: order.currencyCode}
			sums[order.currencyCode] = sum
		}
		sum.OrdersPriced++
		sum.Subtotal += order.subtotal
		sum.DiscountTotal += order.discountTotal

		if entry.Skipped != "" {
			report.Skipped[entry.Skipped]++
			continue
		}

		added := addedDiscount(order.lines, entry.Items)
		if added == 0 {
			continue
		}
		sum.OrdersDiscounted++
		sum.TrialDiscountTotal += added
		report.Orders = append(report.Orders, TrialOrder{
			OrderID:       order.id,
			DisplayID:     order.displayID,
			CurrencyCode:  order.currencyCode,
			PlacedAt:      order.placedAt.UTC(),
			Subtotal:      order.subtotal,
			DiscountTotal: order.discountTotal,
			TrialDiscount: added,
		})
	}

	for _, code := range slices.Sorted(maps.Keys(sums)) {
		report.Currencies = append(report.Currencies, *sums[code])
	}
	slices.SortStableFunc(report.Orders, func(a, b TrialOrder) int {
		return cmp.Or(cmp.Compare(b.TrialDiscount, a.TrialDiscount), b.PlacedAt.Compare(a.PlacedAt))
	})
	if len(report.Orders) > MaxTrialListedOrders {
		report.Orders = report.Orders[:MaxTrialListedOrders]
	}

	return report, nil
}

// addedDiscount is what the promotion adds to an order: per line, the capped new
// total minus the discount the line actually got.
func addedDiscount(lines []trialLine, trial []discountLine) int64 {
	var added int64
	for i := range lines {
		combined := min(lines[i].discountTotal+trial[i].Amount, lines[i].subtotal)
		if combined > lines[i].discountTotal {
			added += combined - lines[i].discountTotal
		}
	}

	return added
}

// trialAnswer asks the promotion module to price the promotion against the
// orders, and checks that the answer is about them.
func (w *Workflows) trialAnswer(
	ctx context.Context, promotionID string, orders []trialOrder,
) (trialResponse, error) {
	if w.discounts == nil {
		return trialResponse{}, errors.Internal(CodeTrialInvalid,
			"the promotion module is not wired, so no promotion can be tried")
	}

	variants := Snapshot{}
	for i := range orders {
		for _, line := range orders[i].lines {
			variants.Items = append(variants.Items, SnapshotItem{VariantID: line.variantID})
		}
	}
	// One read of the catalog for every order. A failure is not fatal, for the
	// reason [Workflows.lineProductFacts] gives about a cart: the purchase is
	// priced without the facts and a rule that needs one does not match.
	facts, factsErr := w.lineProductFacts(ctx, variants)
	if factsErr != nil {
		w.log.WarnContext(ctx, "the products' facts could not be read; trying without them",
			"error", factsErr, "promotion_id", promotionID)
	}

	type ruleContext struct {
		attributes map[string]string
		lists      map[string][]string
	}
	contexts := map[string]ruleContext{}

	request := trialRequest{Entries: make([]trialRequestEntry, 0, len(orders))}
	for i := range orders {
		order := orders[i]
		snap := Snapshot{
			ID:           order.id,
			RegionID:     order.regionID,
			CustomerID:   order.customerID,
			CurrencyCode: order.currencyCode,
			Items:        make([]SnapshotItem, 0, len(order.lines)),
		}
		lines := make([]LineTotals, 0, len(order.lines))
		for _, line := range order.lines {
			snap.Items = append(snap.Items, SnapshotItem{ID: line.id, VariantID: line.variantID, Quantity: line.quantity})
			lines = append(lines, LineTotals{LineItemID: line.id, UnitPrice: line.unitPrice, Subtotal: line.subtotal})
		}

		// The context depends on the region and the customer alone for a
		// purchase with no cart metadata, so each pair is resolved once.
		key := order.regionID + "\x00" + order.customerID
		resolved, seen := contexts[key]
		if !seen {
			attributes, lists, contextErr := w.ruleContext(ctx, snap)
			if contextErr != nil {
				w.log.WarnContext(ctx, "the customer's groups or company could not be read; trying without them",
					"error", contextErr, "customer_id", order.customerID)
			}
			resolved = ruleContext{attributes: attributes, lists: lists}
			contexts[key] = resolved
		}

		reference := order.cartID
		if reference == "" {
			reference = order.id
		}
		request.Entries = append(request.Entries, trialRequestEntry{
			Reference: reference,
			Request:   discountRequestWith(snap, lines, facts, resolved.attributes, resolved.lists),
		})
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return trialResponse{}, errors.Wrap(err, errors.KindInternal, CodeTrialInvalid,
			"the trial request could not be encoded")
	}
	raw, err := w.discounts.TrialDiscountsJSON(ctx, promotionID, payload)
	if err != nil {
		return trialResponse{}, err
	}

	var answer trialResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return trialResponse{}, errors.Wrap(err, errors.KindInternal, CodeTrialResultInvalid,
			"the trial result could not be decoded")
	}
	if err := checkTrialAnswer(request, answer, orders); err != nil {
		return trialResponse{}, err
	}

	return answer, nil
}

// checkTrialAnswer refuses an answer that is not about the purchases asked
// about: one entry per purchase in the same order, one line per line, and no
// line discounted beyond its amount.
func checkTrialAnswer(request trialRequest, answer trialResponse, orders []trialOrder) error {
	if len(answer.Entries) != len(request.Entries) {
		return errors.Internal(CodeTrialResultInvalid,
			"the trial answered %d purchases and %d were asked about", len(answer.Entries), len(request.Entries))
	}
	for i := range answer.Entries {
		entry := answer.Entries[i]
		if entry.Reference != request.Entries[i].Reference {
			return errors.Internal(CodeTrialResultInvalid,
				"the trial's answer %d is about %q and %q was asked", i, entry.Reference, request.Entries[i].Reference)
		}
		if entry.AlreadyApplied {
			continue
		}
		lines := orders[i].lines
		if len(entry.Items) != len(lines) {
			return errors.Internal(CodeTrialResultInvalid,
				"the trial priced %d lines of order %s, which has %d", len(entry.Items), orders[i].id, len(lines))
		}
		for j := range entry.Items {
			if entry.Items[j].ID != lines[j].id {
				return errors.Internal(CodeTrialResultInvalid,
					"the trial's line %d of order %s is %q, expected %q", j, orders[i].id, entry.Items[j].ID, lines[j].id)
			}
			if entry.Items[j].Amount < 0 || entry.Items[j].Amount > lines[j].subtotal {
				return errors.Internal(CodeTrialResultInvalid,
					"the trial discounted line %s by %d, outside [0, %d]", lines[j].id, entry.Items[j].Amount, lines[j].subtotal)
			}
		}
	}

	return nil
}

// trialOrders reads the orders placed in [from, to) with their lines, newest
// first.
//
// The lines are read by the period and walked page by page; the orders they belong
// to are then read by identifier, in batches. A period holding more than
// [MaxTrialOrders] orders is refused before the orders are read.
func (w *Workflows) trialOrders(ctx context.Context, from, to time.Time) ([]trialOrder, error) {
	var sequence []string
	byID := map[string]*trialOrder{}

	for offset := 0; ; offset += trialPageSize {
		records, err := w.catalog.Graph(ctx, query.GraphSpec{
			Entity: trialLineEntity,
			Fields: []string{"id", "order_id", "variant_id", "quantity", "unit_price", "subtotal", "discount_total"},
			Filters: map[string]any{
				"placed_from": from,
				"placed_to":   to,
			},
			Limit:  trialPageSize,
			Offset: offset,
		})
		if err != nil {
			return nil, err
		}

		for _, record := range records {
			line, orderID, err := trialLineOf(record)
			if err != nil {
				return nil, err
			}
			order, seen := byID[orderID]
			if !seen {
				if len(sequence) == MaxTrialOrders {
					return nil, errors.Invalid(CodeTrialTooWide,
						"the period holds more than %d orders; narrow it", MaxTrialOrders)
				}
				order = &trialOrder{id: orderID}
				byID[orderID] = order
				sequence = append(sequence, orderID)
			}
			order.lines = append(order.lines, line)
		}
		if len(records) < trialPageSize {
			break
		}
	}

	for start := 0; start < len(sequence); start += trialPageSize {
		batch := sequence[start:min(start+trialPageSize, len(sequence))]
		records, err := w.catalog.Graph(ctx, query.GraphSpec{
			Entity: trialOrderEntity,
			Fields: []string{
				"id", "display_id", "status", "region_id", "customer_id", "cart_id",
				"currency_code", "placed_at", "subtotal", "discount_total",
			},
			Filters: map[string]any{"id": batch},
			Limit:   len(batch),
		})
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if err := fillTrialOrder(record, byID); err != nil {
				return nil, err
			}
		}
	}

	out := make([]trialOrder, 0, len(sequence))
	for _, id := range sequence {
		order := byID[id]
		if order.currencyCode == "" {
			return nil, errors.Internal(CodeTrialReadInvalid,
				"order %s has lines in the period and its record was not read", id)
		}
		// The lines arrive newest sale first and by id descending, across page
		// boundaries. They are sorted by id so a purchase is asked about in one
		// fixed order, whatever the walk did.
		slices.SortFunc(order.lines, func(a, b trialLine) int { return strings.Compare(a.id, b.id) })
		out = append(out, *order)
	}

	return out, nil
}

// trialLineOf reads one line record.
func trialLineOf(record query.Record) (trialLine, string, error) {
	var (
		line    trialLine
		orderID string
	)
	for _, read := range []func() error{
		func() (e error) { line.id, e = recordText(record, "id"); return },
		func() (e error) { orderID, e = recordText(record, "order_id"); return },
		func() (e error) { line.variantID, e = recordText(record, "variant_id"); return },
		func() (e error) { line.quantity, e = recordAmount(record, "quantity"); return },
		func() (e error) { line.unitPrice, e = recordAmount(record, "unit_price"); return },
		func() (e error) { line.subtotal, e = recordAmount(record, "subtotal"); return },
		func() (e error) { line.discountTotal, e = recordAmount(record, "discount_total"); return },
	} {
		if err := read(); err != nil {
			return trialLine{}, "", err
		}
	}

	return line, orderID, nil
}

// fillTrialOrder reads one order record into the order its lines opened.
func fillTrialOrder(record query.Record, byID map[string]*trialOrder) error {
	id, err := recordText(record, "id")
	if err != nil {
		return err
	}
	order, ok := byID[id]
	if !ok {
		return errors.Internal(CodeTrialReadInvalid, "order %s was read and no line in the period names it", id)
	}

	for _, read := range []func() error{
		func() (e error) { order.displayID, e = recordAmount(record, "display_id"); return },
		func() (e error) { order.status, e = recordText(record, "status"); return },
		func() (e error) { order.regionID, e = recordText(record, "region_id"); return },
		func() (e error) { order.customerID, e = recordOptionalText(record, "customer_id"); return },
		func() (e error) { order.cartID, e = recordOptionalText(record, "cart_id"); return },
		func() (e error) { order.currencyCode, e = recordText(record, "currency_code"); return },
		func() (e error) { order.placedAt, e = recordTime(record, "placed_at"); return },
		func() (e error) { order.subtotal, e = recordAmount(record, "subtotal"); return },
		func() (e error) { order.discountTotal, e = recordAmount(record, "discount_total"); return },
	} {
		if err := read(); err != nil {
			return err
		}
	}

	return nil
}

// recordText reads a field that has to be non-empty text.
func recordText(record query.Record, name string) (string, error) {
	value, err := recordOptionalText(record, name)
	if err == nil && value == "" {
		err = errors.Internal(CodeTrialReadInvalid, "the record's %q is empty", name)
	}

	return value, err
}

// recordOptionalText reads a text field that may be empty.
func recordOptionalText(record query.Record, name string) (string, error) {
	switch value := record[name].(type) {
	case string:
		return value, nil
	case nil:
		return "", nil
	default:
		return "", errors.Internal(CodeTrialReadInvalid, "the record's %q is %T, not text", name, value)
	}
}

// recordAmount reads an integer field.
//
// The Query layer hands a provider's values through as they are, so an int64
// column arrives as an int64; a float would mean the value went through JSON on
// the way and may have lost its minor units, which is refused rather than
// rounded.
func recordAmount(record query.Record, name string) (int64, error) {
	switch value := record[name].(type) {
	case int64:
		return value, nil
	case int32:
		return int64(value), nil
	case int:
		return int64(value), nil
	default:
		return 0, errors.Internal(CodeTrialReadInvalid, "the record's %q is %T, not an integer", name, value)
	}
}

// recordTime reads a moment.
func recordTime(record query.Record, name string) (time.Time, error) {
	if value, ok := record[name].(time.Time); ok {
		return value, nil
	}

	return time.Time{}, errors.Internal(CodeTrialReadInvalid, "the record's %q is %T, not a moment", name, record[name])
}
