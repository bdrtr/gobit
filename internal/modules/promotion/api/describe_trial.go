package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// describeTrial describes the promotion trial (ADR 0176).
//
// It became a file of its own while the rest of this package's descriptions
// were still on the language ledger (ADR 0012), which has been empty since
// 2026-10-03 (D228).
func describeTrial(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathTrial, openapi.Operation{
		Summary: "What the promotion would have done to the orders of a period.",
		Description: "WRITES NOTHING. Every order placed in the period and not canceled is " +
			"rebuilt as the purchase it was — its lines at the prices charged, its region " +
			"and its customer — and the promotion is priced against it by the same engine a " +
			"cart meets, AS IF IT HAD BEEN PUBLISHED: active, automatic, and free of its " +
			"usage limit and its campaign. Its method, its currency and every one of its " +
			"rules still decide. \n\n" +
			"THE FIGURE IS WHAT IT WOULD HAVE ADDED. The promotion is priced alone and each " +
			"order's actual discount is already on its lines; discounts add up and are capped " +
			"at the line, so the report counts only what the line had left to discount. An " +
			"order the promotion was itself redeemed on is counted apart and not priced " +
			"again. \n\n" +
			"WHAT THE ORDERS DID NOT KEEP is read as it is today — the products' categories, " +
			"tags and collection, and the customer's groups — and the sales channel the cart " +
			"was opened in is not kept, so a rule on `sales_channel_id` matches no order. A " +
			"cart's metadata reaches no rule, live or tried. " +
			"\"assumptions\" lists all of it. \n\n" +
			"The period is at most 93 days, holds at most 5000 orders and has to be in the " +
			"past; each is refused with a 422 rather than answered in part. It needs the " +
			"order module's read privilege beside this module's, because the report is orders.",
		Parameters: []openapi.Parameter{
			trialMomentParameter("from", "The period's start: orders placed at this moment or later."),
			trialMomentParameter("to", "The period's end: orders placed before this moment. Not in the future."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The trial's report", d.Item(trialReportDTO{})),
		},
	})
}

// trialMomentParameter describes one end of the trial's period: a required RFC
// 3339 moment in the query string.
func trialMomentParameter(name, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    true,
		Schema:      map[string]any{"type": "string", "format": "date-time"},
		Description: description,
	}
}
