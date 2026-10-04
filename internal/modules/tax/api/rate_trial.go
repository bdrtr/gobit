package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// pathAdminRateTrial is the tax rate trial's address (ADR 0387).
const pathAdminRateTrial = "/admin/v1/tax-rates/{id}/trial"

// orderReadScope is the order module's read privilege, which the trial asks for
// beside this module's own: its report is orders, their identifiers and their
// amounts. The string is the order module's; this module cannot import it.
const orderReadScope = "order:read"

// The trial's query parameters.
const (
	paramTrialFrom     = "from"
	paramTrialTo       = "to"
	paramTrialRateBps  = "rate_bps"
	paramTrialRule     = "rule"
	paramTrialDropRule = "drop_rule"
)

// codeRateTrialAnswerInvalid reports a flow answer this endpoint cannot read.
const codeRateTrialAnswerInvalid = "tax_trial_answer_invalid"

// WithTrial binds the flow the rate trial runs on and returns the API; the
// module hands over a wrapper that resolves it on first use.
func (a *API) WithTrial(trial service.RateTrialFlow) *API {
	a.trial = trial

	return a
}

// rateChangeDTO is the change a trial tried, as the report echoes it.
type rateChangeDTO struct {
	RateBps   *int32       `json:"rate_bps,omitempty"`
	AddRules  []ruleKeyDTO `json:"add_rules,omitempty"`
	DropRules []string     `json:"drop_rules,omitempty"`
}

// ruleKeyDTO is a rule the trial added.
type ruleKeyDTO struct {
	Reference   string `json:"reference"`
	ReferenceID string `json:"reference_id"`
}

// taxRateTrialReportDTO is the trial's published answer. The flow's JSON is
// decoded into it with unknown fields refused, so a field the flow adds or
// renames fails here instead of reaching a client unannounced.
type taxRateTrialReportDTO struct {
	TaxRateID string        `json:"tax_rate_id"`
	Change    rateChangeDTO `json:"change"`
	From      time.Time     `json:"from"`
	To        time.Time     `json:"to"`
	// Assumptions are what the trial set aside, each a word: today's rates,
	// tax classes, price inclusion, catalog and region countries, and
	// product_types_unread when the products' types could not be read.
	Assumptions []string `json:"assumptions"`
	// OrdersRead is every order placed in the period; OrdersCanceled were left
	// out, OrdersRegionRate were taxed at their region's flat rate and
	// OrdersOtherCountry are outside the rate's country.
	OrdersRead         int `json:"orders_read"`
	OrdersCanceled     int `json:"orders_canceled"`
	OrdersRegionRate   int `json:"orders_region_rate"`
	OrdersOtherCountry int `json:"orders_other_country"`
	// Currencies are the sums per currency.
	Currencies []taxRateTrialCurrencyDTO `json:"currencies"`
	// Orders are the orders the change moves, the largest change either way
	// first, at most one hundred.
	Orders []taxRateTrialOrderDTO `json:"orders"`
}

// taxRateTrialCurrencyDTO sums the taxed lines of one currency, in minor units.
type taxRateTrialCurrencyDTO struct {
	CurrencyCode string `json:"currency_code"`
	OrdersPriced int    `json:"orders_priced"`
	LinesPriced  int    `json:"lines_priced"`
	// LinesReached are the lines the rate takes part in, before or after the
	// change; LinesChanged are the ones whose tax the change moves.
	LinesReached int `json:"lines_reached"`
	LinesChanged int `json:"lines_changed"`
	// Charged is the tax the lines were sold with; Baseline is today's tables'
	// and Trial the amended table's. The change's effect is Trial minus
	// Baseline.
	Charged  int64 `json:"charged"`
	Baseline int64 `json:"baseline"`
	Trial    int64 `json:"trial"`
}

// taxRateTrialOrderDTO is one order the change moves.
type taxRateTrialOrderDTO struct {
	OrderID      string    `json:"order_id"`
	DisplayID    int64     `json:"display_id"`
	CurrencyCode string    `json:"currency_code"`
	PlacedAt     time.Time `json:"placed_at"`
	Charged      int64     `json:"charged"`
	Baseline     int64     `json:"baseline"`
	Trial        int64     `json:"trial"`
}

// trialRate answers what a tax rate at another value, or with rules added or
// dropped, would have charged on the orders of a period
// (GET /admin/v1/tax-rates/{id}/trial).
func (a *API) trialRate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	from, err := rateTrialMoment(r, paramTrialFrom)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	to, err := rateTrialMoment(r, paramTrialTo)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	change, err := rateChangeOf(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	raw, err := a.svc.TryRate(ctx, a.trial, pathParam(r, "id"), from, to, change)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var report taxRateTrialReportDTO
	if err := dec.Decode(&report); err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInternal, codeRateTrialAnswerInvalid,
			"the trial's report could not be read"))
		return
	}
	writeItem(w, r, http.StatusOK, report)
}

// rateTrialMoment reads a required RFC 3339 query parameter.
func rateTrialMoment(r *http.Request, name string) (time.Time, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return time.Time{}, coreerrors.Invalid(service.CodeTrialInvalidPeriod,
			"the %q query parameter is required: an RFC 3339 moment", name)
	}
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, coreerrors.Wrap(err, coreerrors.KindInvalid, service.CodeTrialInvalidPeriod,
			"the %q query parameter has to be an RFC 3339 moment with a zone, %q given", name, value)
	}
	return at, nil
}

// rateChangeOf reads the change from the query string: rate_bps once, rule
// and drop_rule repeated. A rule is "reference:reference_id", split at the
// first colon, so an id may carry colons of its own.
func rateChangeOf(r *http.Request) (service.RateChange, error) {
	query := r.URL.Query()
	var change service.RateChange

	if values := query[paramTrialRateBps]; len(values) > 0 {
		if len(values) > 1 {
			return service.RateChange{}, coreerrors.Invalid(service.CodeTrialInvalidChange,
				"the %q query parameter is given once", paramTrialRateBps)
		}
		value, err := strconv.ParseInt(values[0], 10, 32)
		if err != nil {
			return service.RateChange{}, coreerrors.Invalid(service.CodeTrialInvalidChange,
				"the %q query parameter has to be an integer of basis points, %q was given", paramTrialRateBps, values[0])
		}
		bps := int32(value)
		change.RateBps = &bps
	}
	for _, value := range query[paramTrialRule] {
		reference, id, found := strings.Cut(value, ":")
		if !found {
			return service.RateChange{}, coreerrors.Invalid(service.CodeTrialInvalidChange,
				"a %q is reference:reference_id, %q was given", paramTrialRule, value)
		}
		change.AddRules = append(change.AddRules, service.RuleKey{Reference: reference, ReferenceID: id})
	}
	change.DropRules = append(change.DropRules, query[paramTrialDropRule]...)

	return change, nil
}

// describeRateTrial records the tax rate trial (ADR 0387).
func describeRateTrial(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminRateTrial, openapi.Operation{
		Summary: "Reads what a tax rate at another value, or with rules added or dropped, " +
			"would have charged on the orders of a period.",
		Description: "Every line of the period's uncanceled orders in the rate's country is taxed " +
			"twice through the local rate table, from the amount the cart sent for it (unit price " +
			"times quantity less its discount, gift card lines left out as the order kept them): " +
			"once with today's tables and once with the change. The change's effect is `trial` " +
			"minus `baseline`; the tax the lines were sold with is `charged`. Every other rate and " +
			"rule, the tax classes and whether prices include their tax are today's. Nothing is " +
			"written. A change a write would refuse, a province's rate and a rate under an " +
			"external provider are refused before any order is read. The period is at most 93 " +
			"days and 5000 orders and has to be in the past, and its orders at most 100000 lines, " +
			"which is known only once they are read. It needs `order:read` beside " +
			"`tax:read`, because its answer is orders. Amounts are in minor units.",
		Parameters: []openapi.Parameter{
			momentParameter(paramTrialFrom, "The period's start, RFC 3339: orders placed at it or later."),
			momentParameter(paramTrialTo, "The period's end, exclusive, RFC 3339; not in the future."),
			queryParameter(paramTrialRateBps, typeInteger, false,
				"The rate to try, in basis points (2000 = 20%)."),
			repeatedParameter(paramTrialRule,
				"A rule to add, as reference:reference_id with the reference product, "+
					"tax_class or product_type; repeated for several. Not on a default or stacked rate."),
			repeatedParameter(paramTrialDropRule, "The id of a rule of this rate to drop; repeated for several."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The change's effect on the period's orders", d.Item(taxRateTrialReportDTO{})),
			"404": openapi.ErrorResponse("No such tax rate."),
			"422": openapi.ErrorResponse(
				"\"tax_trial_invalid_period\" for a missing or malformed from or to, or a future to, " +
					"\"tax_trial_invalid_change\" for a malformed change, \"tax_invalid_input\" for a " +
					"rate outside 0 to 10000 basis points or a period holding more than 100000 lines, " +
					"\"cart_workflow_trial_invalid\" for a period that ends before it starts or is " +
					"longer than 93 days, and \"cart_workflow_trial_too_wide\" for one holding more " +
					"than 5000 orders."),
			"409": openapi.ErrorResponse(
				"Refused before any order is read: \"tax_trial_change_refused\" for a rule change on a " +
					"default or stacked rate, \"tax_stack_exceeds_base\" for a value that makes a stack " +
					"take more than its line, \"tax_trial_rate_unreached\" for a province's rate, and " +
					"\"tax_trial_provider_external\" for a rate under an external provider."),
		},
	})
}

// momentParameter is a required RFC 3339 query parameter.
func momentParameter(name, description string) openapi.Parameter {
	parameter := queryParameter(name, typeString, true, description)
	parameter.Schema["format"] = "date-time"

	return parameter
}

// repeatedParameter is an optional query parameter given once per value.
func repeatedParameter(name, description string) openapi.Parameter {
	parameter := queryParameter(name, "array", false, description)
	parameter.Schema["items"] = map[string]any{schemaType: typeString}

	return parameter
}
