package api

import (
	"net/http"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// The price history's admin surface (ADR 0167).
//
// One question: what did this set charge, in this currency, over this window —
// and which price and which list did each stretch come from. It is the support
// screen's answer to "the customer says it was cheaper last week", and the
// evidence behind a reduction the storefront announces.

// pathAdminPriceHistory is the endpoint's address.
const pathAdminPriceHistory = "/admin/v1/price-sets/{id}/price-history"

// The endpoint's query parameters besides currency_code.
const (
	paramFrom = "from"
	paramTo   = "to"
)

// priceTimelineDTO is the body of a price timeline.
type priceTimelineDTO struct {
	// PriceSetID and CurrencyCode say which prices.
	PriceSetID   string `json:"price_set_id"`
	CurrencyCode string `json:"currency_code"`
	// From and To are the window asked about.
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// RecordedSince is the first moment the history holds for this set; null
	// when it holds nothing.
	RecordedSince *time.Time `json:"recorded_since"`
	// Covered reports whether the history holds the whole window.
	Covered bool `json:"covered"`
	// Stretches are the prices that applied, in time order.
	Stretches []appliedPriceDTO `json:"stretches"`
	// Lowest is the lowest stretch of the window; null when nothing applied.
	Lowest *appliedPriceDTO `json:"lowest"`
}

// appliedPriceDTO is one stretch during which one price applied.
type appliedPriceDTO struct {
	// From and To bound the stretch, [From, To).
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Priced reports whether any price applied; false is a set that offered
	// nothing in this currency, which is not a price of zero.
	Priced bool `json:"priced"`
	// Amount is the price in minor units; null when nothing applied.
	Amount *int64 `json:"amount"`
	// PriceID is the price that won the ladder; null when nothing applied.
	PriceID *string `json:"price_id"`
	// PriceListID and PriceListType name its list; null for a base price.
	PriceListID   *string `json:"price_list_id"`
	PriceListType *string `json:"price_list_type"`
}

// priceHistory answers GET /admin/v1/price-sets/{id}/price-history.
func (a *API) priceHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	query, err := timelineQuery(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	timeline, err := a.svc.PriceTimeline(ctx, pathID(r, "id"), query)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceTimelineDTO(timeline))
}

// timelineQuery reads the endpoint's query string. An unknown parameter is
// refused, as the calculation endpoint refuses one: a misspelled "form" would
// otherwise answer a different window than the one asked for, and look right.
func timelineQuery(r *http.Request) (service.TimelineQuery, error) {
	for name, values := range r.URL.Query() {
		switch name {
		case paramCurrencyCode, paramFrom, paramTo:
		default:
			return service.TimelineQuery{}, coreerrors.Invalid(codeInvalidBody,
				"%q is not a parameter of the price history", name)
		}
		if len(values) > 1 {
			return service.TimelineQuery{}, coreerrors.Invalid(codeInvalidBody,
				"%q was given more than once", name)
		}
	}

	q := service.TimelineQuery{CurrencyCode: r.URL.Query().Get(paramCurrencyCode)}
	for _, bound := range []struct {
		name string
		dst  **time.Time
	}{{paramFrom, &q.From}, {paramTo, &q.To}} {
		value, err := timeParam(r, bound.name)
		if err != nil {
			return service.TimelineQuery{}, err
		}
		if !value.IsZero() {
			*bound.dst = &value
		}
	}

	return q, nil
}

// toPriceTimelineDTO converts a timeline into its body.
func toPriceTimelineDTO(timeline models.PriceTimeline) priceTimelineDTO {
	out := priceTimelineDTO{
		PriceSetID:    timeline.PriceSetID,
		CurrencyCode:  timeline.CurrencyCode,
		From:          timeline.From,
		To:            timeline.To,
		RecordedSince: timeline.RecordedSince,
		Covered:       timeline.Covers(timeline.From),
		Stretches:     make([]appliedPriceDTO, 0, len(timeline.Stretches)),
	}
	for _, stretch := range timeline.Stretches {
		out.Stretches = append(out.Stretches, toAppliedPriceDTO(stretch))
	}
	if lowest, ok := timeline.Lowest(timeline.From, timeline.To); ok {
		dto := toAppliedPriceDTO(lowest)
		out.Lowest = &dto
	}

	return out
}

// toAppliedPriceDTO converts one stretch.
func toAppliedPriceDTO(stretch models.AppliedPrice) appliedPriceDTO {
	out := appliedPriceDTO{From: stretch.From, Priced: stretch.Priced}
	if stretch.To != nil {
		out.To = *stretch.To
	}
	if stretch.Priced {
		amount, priceID := stretch.Amount, stretch.PriceID
		out.Amount, out.PriceID = &amount, &priceID
		out.PriceListID = stretch.PriceListID
		out.PriceListType = listTypeOrNil(stretch.PriceListType)
	}

	return out
}

// describePriceHistory describes the endpoint.
func describePriceHistory(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminPriceHistory, openapi.Operation{
		Summary: "Reads what a price set charged over a window, and where each price came from.",
		Description: "The price a shopper who names nothing was charged — quantity one, no rule " +
			"context, the price a storefront shows — as stretches of time, each naming the price " +
			"that won and the list it came from. A stretch changes where a write changed the set " +
			"or one of its lists, and where a list's window opened or closed with no write at " +
			"all. The window defaults to the thirty days before now, the reference a reduction " +
			"announced now looks back over. Nothing before `recorded_since` is known — a replaced " +
			"price was deleted until ADR 0167 — so a window that reaches further back is answered " +
			"from `recorded_since` and says `covered: false`. Amounts are in minor units.",
		Parameters: []openapi.Parameter{
			requiredQueryParameter(paramCurrencyCode, typeString, "The currency, ISO 4217."),
			timeParameter(paramFrom,
				"The window's start, RFC 3339; thirty days before `to` when absent."),
			timeParameter(paramTo, "The window's end, exclusive, RFC 3339; now when absent."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The prices that applied", d.Item(priceTimelineDTO{})),
		},
	})
}

// requiredQueryParameter is [queryParameter] for a parameter the handler
// refuses to answer without.
func requiredQueryParameter(name, kind, description string) openapi.Parameter {
	parameter := queryParameter(name, kind, description)
	parameter.Required = true

	return parameter
}
