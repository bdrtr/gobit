package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
)

// storeOrderAccessNote is what the three storefront order routes say about who
// may call them (ADR 0439).
const storeOrderAccessNote = "The storefront's publishable key names the shop rather than the " +
	"shopper, and the order id is not a secret: gobit sends it to every webhook receiver of " +
	"an order topic. So the route answers a request that presents one of the order's access " +
	"tokens in the \"" + corehttp.OrderTokenHeader + "\" header, or that the installation's " +
	"customer identity proves to be the order's customer. A storefront receives a token as " +
	"\"order_token\" in the answer of POST /store/v1/carts/{id}/complete, and each order mail " +
	"carries another as \"order_token\" in its template data; a guest's order is opened by a " +
	"token alone, and an order placed before tokens existed by its customer's proof alone. " +
	"Send the token in the header and never in a URL: the request log writes the path. Every " +
	"answer carries Cache-Control: no-store and varies on the token's header. An Idempotency-Key " +
	"on the return request is the caller's own: the record is replayed only to a request " +
	"presenting the same token or proving the same customer."

// storeOrderRefusal is the 404 the three storefront order routes answer.
const storeOrderRefusal = "No such order, or the request neither presents one of its access " +
	"tokens nor proves its customer (\"order_not_found\"). The two answers are the same on " +
	"purpose, so an order id says nothing about the order it names. An identity that could " +
	"not reach a verdict answers its own class, 500 or 503, for a real order and an unknown id " +
	"alike."

// orderTokenParameter is the header the three storefront order routes read
// the order's access token from.
func orderTokenParameter() openapi.Parameter {
	return openapi.Parameter{
		Name: corehttp.OrderTokenHeader, In: "header",
		Schema: map[string]any{schemaType: typeString},
		Description: "One of the order's access tokens, as the completion answered it or an " +
			"order mail carried it. Optional for a request the customer identity proves to be " +
			"the order's customer.",
	}
}

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
			"\"shipping_methods\" are the deliveries the order was sold, the option, its " +
			"name and what the checkout charged, adding up to \"shipping_total\"; an " +
			"empty array for an order placed before they were kept (ADR 0198). Each lists " +
			"in \"changes\" the options it was put on since, oldest first, and the last one " +
			"is the delivery the order is on (ADR 0199).\n\n" +
			"The record carries \"shipping_address\" and \"billing_address\", the two " +
			"addresses the cart carried into the order, each ABSENT when the order " +
			"recorded none. After an erasure they hold what the erasure keeps: the country " +
			"and the free metadata (ADR 0193).\n\n" + lineCostNote + placedMarginNote,
		Responses: map[string]any{
			"200": openapi.Response("The order with its lines", d.Item(adminOrderDetailDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/orders/{id}", openapi.Operation{
		Summary: "Reads one order from the storefront.",
		Description: detailNote +
			"\n\n" +
			"It is the record the admin surface returns WITHOUT the two addresses, the " +
			"cart and customer ids, the lines' costs and the placed margin (ADR 0193, " +
			"ADR 0439). " + storeOrderAccessNote,
		Parameters: []openapi.Parameter{orderTokenParameter()},
		Responses: map[string]any{
			"200": openapi.Response("The order with its lines", d.Item(storeOrderDetailDTO{})),
			"404": openapi.ErrorResponse(storeOrderRefusal),
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
			"cancellation. So is an order with money collected on it." +
			"\n\n" +
			"The cancel WRITES OFF every unit not yet returned or written off, as line " +
			"cancellations under its reason, so the stock the checkout deducted comes back " +
			"to the shelf; what a live parcel holds stays out until the parcel is canceled " +
			"(ADR 0285)." +
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
			"The completion publishes \"order.completed\" with the order and the moment, " +
			"written in the same transaction; the notification module mails the order's " +
			"address unless the provider says it holds no such template (ADR 0386). " + detailNote,
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

	d.Describe(http.MethodPut, "/admin/v1/orders/{id}/shipping-methods/{shippingMethodId}", openapi.Operation{
		Summary: "Changes which service a delivery goes on, while nothing is on its way.",
		Description: "The option is priced by the fulfillment module on the order's own facts " +
			"— its region, currency, the country of its shipping address, the goods after " +
			"discount and every unit sold but a gift card's — and has to be among the options " +
			"it lists for them, " +
			"admin-only ones included. The method keeps what the order was sold; the change is " +
			"added to its \"changes\", and the last change is the delivery the order is on. The " +
			"timeline dates it with an \"order.delivery_changed\" entry (ADR 0199).\n\n" +
			"A change that costs less writes the difference off as a credit line with the " +
			"reason \"delivery_change\", which the order journal books against shipping. A " +
			"change that costs the same writes no credit. Putting the delivery on the option it " +
			"is already on writes nothing.\n\n" +
			"A change that costs more is refused with \"order_delivery_costs_more\", whose " +
			"details say the option, its amount, the difference and the currency. Collect the " +
			"difference on a payment collection opened with the order's id as its reference, " +
			"for exactly the difference, and captured; then name it in " +
			"\"payment_collection_id\". The change records the collection, and the order " +
			"journal books the difference as receivable against shipping (ADR 0200). " + detailNote,
		RequestBody: d.RequestBody(changeDeliveryRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The order with its changed delivery",
				d.Item(adminOrderDetailDTO{})),
			"404": openapi.ErrorResponse("No such order, or no such shipping method on it."),
			"409": openapi.ErrorResponse(
				"The change is refused: \"fulfilling_parcel_underway\" while a parcel is " +
					"pending, shipped or delivered, \"fulfilling_option_unavailable\" for an " +
					"option not listed for the order, a return option or one priced in another " +
					"currency, \"order_delivery_not_changeable\" for an order that is not " +
					"pending, \"order_delivery_costs_more\" for a dearer change naming no " +
					"collection, \"fulfilling_collection_not_the_orders\" for a collection " +
					"opened for another record or in another currency, " +
					"\"fulfilling_collection_not_settled\" for one not holding all it was " +
					"opened for, \"order_delivery_payment_mismatch\" for one not holding the " +
					"difference, \"order_delivery_takes_no_payment\" for a collection named on a " +
					"change that costs no more, and \"order_payment_collection_taken\" for one " +
					"that already paid for a change or an exchange."),
			"422": openapi.ErrorResponse("The body is empty, unreadable, names an unknown " +
				"field, or names no option."),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/archive", openapi.Operation{
		Summary: "Archives a completed order.",
		Description: "Archiving takes a finished order out of the working list without " +
			"deleting anything; the record, its lines and its invoice stay exactly as they " +
			"were. Only a COMPLETED order can be archived — archiving one still in flight " +
			"would hide work that is not done. It takes no body for the same reason " +
			"completion does not. Archiving publishes no event. " + detailNote,
		Responses: map[string]any{
			"200": openapi.Response("The order, now archived", d.Item(adminOrderDetailDTO{})),
		},
	})
}
