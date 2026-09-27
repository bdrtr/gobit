package api

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// describeSegments records the segment endpoints (ADR 0217): the operator gives
// a group a rule, takes it away, and counts what a rule would take in.
func describeSegments(d *openapi.Doc) {
	const (
		segmentPath = "/admin/v1/customer-groups/{id}/segment"
		previewPath = "/admin/v1/customer-segments/preview"
	)

	rule := fmt.Sprintf("A rule holds 1 to %d conditions, all of which a member satisfies. The "+
		"attributes are has_account (eq, with true or false); account_age_days, whole days since "+
		"the customer record was created; country_code, the country of the default shipping "+
		"address (eq and ne with a code, in and nin with up to %d codes in values; a customer "+
		"without one matches no condition on it); order_count, the orders placed in the window in "+
		"any currency; and net_spend, those orders' totals less their refunds in currency_code, in "+
		"minor units. The numeric ones take eq, ne, gt, gte, lt and lte with a whole number from 0. "+
		"Canceled orders do not count and no currency is converted. window_days (0 to %d, 0 for the "+
		"whole history) is read only by the two order attributes, and currency_code only by "+
		"net_spend; either one given without its attribute is refused.",
		models.MaxSegmentConditions, models.MaxSegmentValues, models.MaxSegmentWindowDays)
	invalid := openapi.ErrorResponse(fmt.Sprintf("The rule is outside the vocabulary: code %q.",
		models.CodeSegmentInvalid))

	d.Describe(http.MethodPut, segmentPath, openapi.Operation{
		Summary: "Makes a group a segment whose members a rule decides.",
		Description: rule + "\n\n" +
			"The customer-segments job writes the members every hour: every live customer who " +
			"satisfies the rule is in, everyone else is out, and segment_evaluated_at says when it " +
			"last finished. Until the next pass the group keeps the members it has. A new rule for a " +
			"segment replaces the old one and waits for its own pass. While a group is a segment, " +
			"adding or removing a member by hand is refused.",
		RequestBody: d.RequestBody(segmentRuleDTO{}),
		Responses: map[string]any{
			"200": openapi.Response("The group with its rule", d.Item(customerGroupDTO{})),
			"409": openapi.ErrorResponse(fmt.Sprintf(
				"%d groups are segments already and this one is not among them: code %q.",
				models.MaxSegments, models.CodeSegmentLimit)),
			"422": invalid,
		},
	})

	d.Describe(http.MethodDelete, segmentPath, openapi.Operation{
		Summary: "Hands a segment back to the operator.",
		Description: "The rule goes and the members the job wrote stay; adding and removing them " +
			"by hand is taken again.",
		Responses: map[string]any{
			"200": openapi.Response("The group without a rule", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodPost, previewPath, openapi.Operation{
		Summary:     "Counts the customers a rule would take in.",
		Description: rule + "\n\nNothing is written. Every live customer is read, so the answer takes as long as a pass does for one segment.",
		RequestBody: d.RequestBody(segmentRuleDTO{}),
		Responses: map[string]any{
			"200": openapi.Response("How many customers satisfy the rule now", d.Item(segmentPreviewDTO{})),
			"422": invalid,
		},
	})
}
