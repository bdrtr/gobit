package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// describeOrderDetail records the five endpoints that answer with the order and
// its lines.
//
// # They were undescribed until 2026-09-07, and not by neglect
//
// [orderDetailDTO] carries its lines with [lineItemDTO], and the cart module's
// api package has a type of the same name that IS described. Two types wanting
// one component name does not degrade one endpoint: the document FAILS to build
// and /openapi.json returns 500 for cart's endpoints too. Leaving these five
// bodiless was the cheaper mistake, and the note this package carried said the
// fix belonged in the core — a name that two modules must agree on is not a
// decision either of them can take alone.
//
// It did, and ADR 0036 made it: the component name carries the module being
// described, so this is "OrderLineItem" and the cart's is "CartLineItem".
// Nothing in this package was renamed.
//
// # Why one function for five endpoints
//
// Because they answer the SAME record. Three of them are status transitions
// that end by re-reading the order (see [Handler.writeCurrentOrder]) precisely
// so that all five envelopes have one shape; describing them apart would invite
// the shapes to drift the way the handlers deliberately do not.
func describeOrderDetail(d *openapi.Doc) {
	const detailNote = "The record is the order together with its LINES and its payment " +
		"summary. The summary's \"outstanding\" is what is still owed — paid minus " +
		"refunded against the total — and it is the field a shop reads to decide whether " +
		"the order is settled. " + amountNote

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}", openapi.Operation{
		Summary: "Reads one order with its lines.",
		Description: detailNote +
			"\n\n" +
			"Each line carries \"tax_rate_bps\", the rate the line's tax was computed at in " +
			"BASIS POINTS (2000 is 20%). It is published because it CANNOT be recomputed: " +
			"the tax is rounded down per line, so the amount alone maps back to a range of " +
			"rates, and anything printing an invoice needs the rate the customer was " +
			"actually charged under.\n\n" +
			"A line taxed by a STACK of rates also carries \"tax_components\", one entry " +
			"per rate in stack order with the base first, and their \"tax_amount\" values " +
			"add up to the line's \"tax_total\". The field is ABSENT when a single rate " +
			"applied, and \"tax_rate_bps\" then says it all; on a stacked line that field " +
			"carries the stack's BASE rate, which is a rate really applied on an amount " +
			"really recorded, not the whole story.\n\n" +
			"A line also carries \"price_origin\": the price row it was charged and, for a " +
			"list price, the list and its type (\"sale\" or \"override\"). It is ABSENT " +
			"when unknown — every line sold before the order kept it (ADR 0168).\n\n" +
			"The record carries \"shipping_address\" and \"billing_address\", the two " +
			"addresses the cart carried into the order, each ABSENT when the order " +
			"recorded none. After an erasure they hold what the erasure keeps: the country " +
			"and the free metadata (ADR 0193).",
		Responses: map[string]any{
			"200": openapi.Response("The order with its lines", d.Item(adminOrderDetailDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/orders/{id}", openapi.Operation{
		Summary: "Reads one order from the storefront.",
		Description: detailNote +
			"\n\n" +
			"It is the record the admin surface returns WITHOUT the two addresses. The " +
			"storefront reaches it with the publishable key, which identifies the shop " +
			"rather than the shopper, so the order id in the path is the only thing naming " +
			"whose order this is — treat it as a capability and keep it out of anywhere it " +
			"can be guessed or shared. That is also why the addresses are not in it " +
			"(ADR 0193).",
		Responses: map[string]any{
			"200": openapi.Response("The order with its lines", d.Item(orderDetailDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/cancel", openapi.Operation{
		Summary: "Cancels the order.",
		Description: "The call is IDEMPOTENT: canceling an already canceled order is not " +
			"an error, it answers 200 with the state the order is already in. A caller " +
			"retrying after a timeout must not be told it failed for a state its own first " +
			"request produced." +
			"\n\n" +
			"A COMPLETED order is a different matter and comes back as 409: completion is " +
			"the end of the line, and unwinding it is a refund and a return rather than a " +
			"cancellation." +
			"\n\n" +
			"The body is OPTIONAL and carries only a reason, kept on the order for whoever " +
			"reads it later. " + detailNote,
		RequestBody: d.RequestBody(cancelOrderRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The order, now canceled", d.Item(adminOrderDetailDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/complete", openapi.Operation{
		Summary: "Completes the order.",
		Description: "Completion is what says the shop is finished with the order. It takes " +
			"no body: there is nothing to choose, and an endpoint that accepted one would " +
			"invite somebody to pass the completion moment or the amounts from the client. " +
			detailNote,
		Responses: map[string]any{
			"200": openapi.Response("The order, now complete", d.Item(adminOrderDetailDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/orders/{id}/shipping-address", openapi.Operation{
		Summary: "Corrects where the order ships, while nothing is on its way.",
		Description: "The body is the WHOLE corrected address. The address the order was " +
			"placed with is not edited: it is closed and the correction written beside it, " +
			"so the person's file lists both and the timeline dates the correction with an " +
			"\"order.shipping_address_corrected\" entry that names no address (ADR 0195). " +
			"A body identical to the current address writes nothing.\n\n" +
			"The country cannot change: the tax and the shipping price were computed on it. " +
			"An empty \"country_code\" means the current one. A field the schema does not " +
			"name is refused. " + detailNote,
		RequestBody: d.RequestBody(orderAddressDTO{}),
		Responses: map[string]any{
			"200": openapi.Response("The order with its corrected address",
				d.Item(adminOrderDetailDTO{})),
			"404": openapi.ErrorResponse("No such order."),
			"409": openapi.ErrorResponse(
				"The correction is refused: \"fulfilling_parcel_underway\" while a parcel is " +
					"pending, shipped or delivered (its carrier has the old address; a canceled " +
					"or returned parcel does not stand in the way), " +
					"\"order_address_not_correctable\" for an order that is not pending or " +
					"whose personal data was erased, \"order_address_missing\" for an order " +
					"that recorded no shipping address, and \"order_address_country_changed\"."),
			"422": openapi.ErrorResponse("The body is empty, unreadable, or names a field the " +
				"address does not have."),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/archive", openapi.Operation{
		Summary: "Archives a completed order.",
		Description: "Archiving takes a finished order out of the working list without " +
			"deleting anything; the record, its lines and its invoice stay exactly as they " +
			"were. Only a COMPLETED order can be archived — archiving one still in flight " +
			"would hide work that is not done. It takes no body for the same reason " +
			"completion does not. " + detailNote,
		Responses: map[string]any{
			"200": openapi.Response("The order, now archived", d.Item(adminOrderDetailDTO{})),
		},
	})
}
