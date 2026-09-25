package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// pathTrial is the promotion trial's address (ADR 0176).
const pathTrial = "/admin/v1/promotions/{id}/trial"

// orderReadScope is the order module's read privilege, which the trial asks for
// beside this module's own.
//
// The trial's report is ORDERS: their identifiers, their amounts and what they were
// discounted. A privilege over promotions is not a privilege over orders, and an
// identity allowed to read campaigns but not sales would read sales through here.
// The string is the order module's ("order:read"); this module cannot import it,
// and the admin panel repeats it for the same reason.
const orderReadScope = "order:read"

// Error codes of the trial endpoint.
const (
	// codeTrialInvalidPeriod reports a from or to that is missing, malformed or
	// in the future.
	codeTrialInvalidPeriod = "promotion_trial_invalid_period"
	// codeTrialUnavailable reports that the flow the trial runs on is not bound.
	codeTrialUnavailable = "promotion_trial_unavailable"
	// codeTrialAnswerInvalid reports a flow answer this endpoint cannot read.
	codeTrialAnswerInvalid = "promotion_trial_answer_invalid"
)

// PromotionTrial is the surface of the flow that prices a promotion against past
// orders (ADR 0176).
//
// # Why this module asks a flow
//
// The question is about a promotion and belongs on its admin surface, but the
// answer needs two things this module cannot do: read orders, and rebuild a
// purchase the way the cart flow builds a cart for the engine. Both belong to
// internal/workflows/cart, which this module cannot import (ADR 0006); the
// interface is declared on this side with stdlib types, and the flow's surface
// satisfies it structurally.
type PromotionTrial interface {
	// TrialPromotionJSON prices the promotion against the orders placed in
	// [from, to) and returns the report as JSON; it writes nothing.
	TrialPromotionJSON(ctx context.Context, promotionID string, from, to time.Time) (json.RawMessage, error)
}

// WithTrial binds the flow the trial endpoint runs on and returns the API.
//
// It is a separate step from [New] because the flow is resolved from the
// container on first use (the flows are built after the modules), so the module
// hands over a lazy wrapper; an API with no flow bound refuses the trial with a
// server error rather than answering with an empty report.
func (a *API) WithTrial(trial PromotionTrial) *API {
	a.trial = trial

	return a
}

// trialReportDTO is the trial's published answer.
//
// It is this module's own contract. The flow's JSON is decoded into it with
// unknown fields refused, so a field the flow adds or renames fails here instead
// of reaching a client unannounced.
type trialReportDTO struct {
	// PromotionID is the promotion under trial.
	PromotionID string `json:"promotion_id"`
	// From and To are the period: orders placed at From or later and before To.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Assumptions are what the trial set aside, each a word: the promotion was
	// priced as active, automatic, without a usage limit or a campaign, against
	// today's catalog and today's customer groups, and without the cart's own
	// metadata.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period.
	OrdersRead int `json:"orders_read"`
	// OrdersCanceled were left out because they were canceled.
	OrdersCanceled int `json:"orders_canceled"`
	// OrdersAlreadyDiscounted are the orders the promotion itself was redeemed
	// on; they are not priced again.
	OrdersAlreadyDiscounted int `json:"orders_already_discounted"`
	// Skipped counts the priced orders the promotion would not have applied to,
	// by the reason the engine gives.
	Skipped map[string]int `json:"skipped"`
	// Currencies are the sums per currency.
	Currencies []trialCurrencyDTO `json:"currencies"`
	// Orders are the discounted orders, largest trial discount first, at most
	// one hundred.
	Orders []trialOrderDTO `json:"orders"`
}

// trialCurrencyDTO sums the priced orders of one currency.
type trialCurrencyDTO struct {
	CurrencyCode string `json:"currency_code"`
	// OrdersPriced are the orders the promotion was priced against.
	OrdersPriced int `json:"orders_priced"`
	// OrdersDiscounted are those it would have taken something off.
	OrdersDiscounted int `json:"orders_discounted"`
	// Subtotal is the priced orders' goods before any discount (minor units).
	Subtotal int64 `json:"subtotal"`
	// DiscountTotal is what the priced orders were actually discounted.
	DiscountTotal int64 `json:"discount_total"`
	// TrialDiscountTotal is what the promotion would have ADDED to that.
	TrialDiscountTotal int64 `json:"trial_discount_total"`
}

// trialOrderDTO is one order the promotion would have discounted.
type trialOrderDTO struct {
	OrderID       string    `json:"order_id"`
	DisplayID     int64     `json:"display_id"`
	CurrencyCode  string    `json:"currency_code"`
	PlacedAt      time.Time `json:"placed_at"`
	Subtotal      int64     `json:"subtotal"`
	DiscountTotal int64     `json:"discount_total"`
	TrialDiscount int64     `json:"trial_discount"`
}

// trialPromotion answers what a promotion would have done to the orders of a
// period (GET /admin/v1/promotions/{id}/trial).
//
// Both ends are required and are RFC 3339 moments. The period has to be in the
// past: an order can only be placed at the moment it is placed, so a period that
// reaches into the future would be answered with a report that changes while it
// is read.
func (a *API) trialPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := trialMoment(r, "from")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	to, err := trialMoment(r, "to")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if to.After(time.Now()) {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeTrialInvalidPeriod,
			"a trial reads orders already placed; \"to\" is in the future: %s", to.Format(time.RFC3339)))
		return
	}
	if a.trial == nil {
		corehttp.WriteError(ctx, w, coreerrors.Internal(codeTrialUnavailable,
			"the promotion trial flow is not bound; no order can be read"))
		return
	}

	raw, err := a.trial.TrialPromotionJSON(ctx, pathID(r, "id"), from, to)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var report trialReportDTO
	if err := dec.Decode(&report); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeTrialAnswerInvalid,
			"the trial's report could not be read"))
		return
	}
	writeItem(w, r, http.StatusOK, report)
}

// trialMoment reads a required RFC 3339 query parameter.
func trialMoment(r *http.Request, name string) (time.Time, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return time.Time{}, coreerrors.Invalid(codeTrialInvalidPeriod,
			"the %q query parameter is required: an RFC 3339 moment", name)
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, codeTrialInvalidPeriod,
			"the %q query parameter has to be an RFC 3339 moment with a zone, %q given", name, value)
	}

	return at, nil
}
