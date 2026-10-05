package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// pathListTrial is the price list trial's address (ADR 0220).
const pathListTrial = "/admin/v1/price-lists/{id}/trial"

// orderReadScope is the order module's read privilege, which the trial asks for
// beside this module's own: its report is orders, their identifiers and their
// amounts. The string is the order module's; this module cannot import it.
const orderReadScope = "order:read"

// codeListTrialAnswerInvalid reports a flow answer this endpoint cannot read;
// the trial's other codes are the service's, which the panel's surface
// answers with too.
const codeListTrialAnswerInvalid = "pricing_trial_answer_invalid"

// PriceListTrial is the surface of the flow that prices a price list against
// past orders (ADR 0220).
//
// The question is about a price list and belongs here, but the answer needs to
// read orders and to build the rule context a cart carries; both belong to
// internal/workflows/cart, which this module cannot import (ADR 0006).
type PriceListTrial interface {
	// TrialPriceListJSON prices the goods of the orders placed in [from, to)
	// with the list as if active and without it; it writes nothing.
	TrialPriceListJSON(ctx context.Context, listID string, from, to time.Time) (json.RawMessage, error)
}

// WithTrial binds the flow the trial endpoint runs on and returns the API; the
// module hands over a wrapper that resolves it on first use.
func (a *API) WithTrial(trial PriceListTrial) *API {
	a.trial = trial

	return a
}

// listTrialReportDTO is the trial's published answer. The flow's JSON is
// decoded into it with unknown fields refused, so a field the flow adds or
// renames fails here instead of reaching a client unannounced.
type listTrialReportDTO struct {
	PriceListID string    `json:"price_list_id"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	// Assumptions are what the trial set aside, each a word: today's prices,
	// price set links and customer groups, the list active with no window, and
	// the prices before any discount.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period; OrdersCanceled were left
	// out.
	OrdersRead     int `json:"orders_read"`
	OrdersCanceled int `json:"orders_canceled"`
	// LinesUnpriced are the lines no price could be found for today.
	LinesUnpriced int `json:"lines_unpriced"`
	// Currencies are the sums per currency.
	Currencies []listTrialCurrencyDTO `json:"currencies"`
	// Orders are the orders whose goods the list changes, the largest change
	// either way first, at most one hundred.
	Orders []listTrialOrderDTO `json:"orders"`
}

// listTrialCurrencyDTO sums the priced lines of one currency, in minor units.
type listTrialCurrencyDTO struct {
	CurrencyCode string `json:"currency_code"`
	OrdersPriced int    `json:"orders_priced"`
	LinesPriced  int    `json:"lines_priced"`
	// LinesChanged are the lines the list gives another unit price.
	LinesChanged int `json:"lines_changed"`
	// Charged is what the priced lines were sold at, before discounts.
	Charged int64 `json:"charged"`
	// Baseline is what they would cost today without the list, and Trial with it.
	Baseline int64 `json:"baseline"`
	Trial    int64 `json:"trial"`
}

// listTrialOrderDTO is one order whose goods the list changes.
type listTrialOrderDTO struct {
	OrderID      string    `json:"order_id"`
	DisplayID    int64     `json:"display_id"`
	CurrencyCode string    `json:"currency_code"`
	PlacedAt     time.Time `json:"placed_at"`
	Baseline     int64     `json:"baseline"`
	Trial        int64     `json:"trial"`
}

// trialPriceList answers what a price list would do to the prices of the orders
// of a period (GET /admin/v1/price-lists/{id}/trial).
//
// Both ends are required RFC 3339 moments, and the period has to be in the past.
func (a *API) trialPriceList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := listTrialMoment(r, paramFrom)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	to, err := listTrialMoment(r, paramTo)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if to.After(time.Now()) {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(service.CodeListTrialInvalidPeriod,
			"a trial reads orders already placed; \"to\" is in the future: %s", to.Format(time.RFC3339)))
		return
	}
	if a.trial == nil {
		corehttp.WriteError(ctx, w, coreerrors.Internal(service.CodeListTrialUnavailable,
			"the price list trial flow is not bound; no order can be read"))
		return
	}

	raw, err := a.trial.TrialPriceListJSON(ctx, pathID(r, "id"), from, to)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var report listTrialReportDTO
	if err := dec.Decode(&report); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeListTrialAnswerInvalid,
			"the trial's report could not be read"))
		return
	}
	writeItem(w, r, http.StatusOK, report)
}

// listTrialMoment reads a required RFC 3339 query parameter.
func listTrialMoment(r *http.Request, name string) (time.Time, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return time.Time{}, coreerrors.Invalid(service.CodeListTrialInvalidPeriod,
			"the %q query parameter is required: an RFC 3339 moment", name)
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, service.CodeListTrialInvalidPeriod,
			"the %q query parameter has to be an RFC 3339 moment with a zone, %q given", name, value)
	}
	return at, nil
}

// describeListTrial records the price list trial (ADR 0220).
func describeListTrial(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathListTrial, openapi.Operation{
		Summary: "Reads what a price list would do to the prices of the orders of a period.",
		Description: "Every order placed in the period and not canceled is priced line by line, at " +
			"the line's quantity, in the order's currency and with the rule context a cart of its " +
			"customer in its region carries: once as if the list did not exist, and once as if it " +
			"were active with no window, whatever its status and dates. Both prices are today's " +
			"ladder, so the list's effect is `trial` minus `baseline`; what the lines were sold " +
			"at is `charged`, before discounts. Nothing is written. The period is at most 93 days " +
			"and 5000 orders and has to be in the past. It needs `order:read` beside " +
			"`pricing:read`, because its answer is orders. Amounts are in minor units.",
		Parameters: []openapi.Parameter{
			requiredTimeParameter(paramFrom, "The period's start, RFC 3339: orders placed at it or later."),
			requiredTimeParameter(paramTo, "The period's end, exclusive, RFC 3339; not in the future."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The list's effect on the period's orders", d.Item(listTrialReportDTO{})),
		},
	})
}

// requiredTimeParameter is [timeParameter] for a moment the handler refuses to
// answer without.
func requiredTimeParameter(name, description string) openapi.Parameter {
	parameter := timeParameter(name, description)
	parameter.Required = true

	return parameter
}
