package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names that appear in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and it only surfaces when the client that reads the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// amountNote is the warning added to the description of endpoints that carry an
// amount in their body.
//
// The type already comes out of the schema as "integer", but the TYPE on its
// own does not state the REASON: a client developer may see that 100.50 TL
// cannot be sent as an integer and try rounding the kurus away. Having the unit
// written down is the only thing that says 10050 has to be sent.
//
// The note sits at the OPERATION level, not at the FIELD level: the schema is
// derived from the Go type and Go fields carry no description. A per-field
// description would have meant adding a new mechanism to the core (a
// description read from a tag) and that is not this module's job.
const amountNote = "Amounts are MINOR UNIT integers (kurus/cent): " +
	"for 100.50 TL you send 10050; a fractional value such as 100.50 is invalid."

// Describe records order's ADMIN endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (createReturnRequest, returnDTO …) and the schema is derived from them by
// reflection. Exporting the types so that they could be described would mean
// widening the module's surface merely for the sake of producing a document: an
// exported type is a contract and it would become constructible from outside.
// The query parameters too are the ones the handler REALLY reads, and that
// reading lives in this package's admin.go together with [parsePage]; had the
// description lived in another package, the two would have drifted apart
// silently. This is why the module's [openapi.Describer] implementation
// delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the TYPES.
// Attaching the method to [Handler] would have said that the document DEPENDS
// on the service having been constructed; yet the document can be produced, and
// has to be producible, even when Register has never run.
//
// # ~~FIVE ENDPOINTS LEFT UNDESCRIBED~~ — the collision was fixed in the core
//
// The name of the schema component is derived from the Go type name (the first
// letter is capitalized, the "DTO" suffix is dropped), that is, [lineItemDTO]
// asks for the name "LineItem". The cart module's api package also has a type
// with the same name and it is described there too. When two DIFFERENT types
// ask for the same name, [openapi.Doc.Build] returns an error and the document
// cannot be produced AT ALL: /openapi.json becomes a 500 and not only this
// module's endpoints but cart's endpoints drop out of the document as well.
// That is, ignoring the collision means losing not a single endpoint but the
// whole document.
//
// [orderDetailDTO] carries its line items with [lineItemDTO]; this is why the
// five endpoints that return it were LEFT undescribed:
//
//   - GET  /store/v1/orders/{id}
//   - GET  /admin/v1/orders/{id}
//   - POST /admin/v1/orders/{id}/cancel
//   - POST /admin/v1/orders/{id}/complete
//   - POST /admin/v1/orders/{id}/archive
//
// ~~All five appear in the document with their path, method and security; only
// their bodies are missing.~~ **2026-09-07: all five are described, in
// [describeOrderDetail].** The paragraph above stays because its diagnosis was
// right and its refusal was right: the fix IS a rename of a published class
// name, and it IS a decision that concerns two modules at once, so taking it
// from inside this one would have broken the cart module's client without
// warning.
//
// What the paragraph got wrong was only the shape of the answer. ADR 0036 did
// not rename either type; it gave the component name a NAMESPACE — the name of
// the module being described — so this package's type is "OrderLineItem" and
// cart's is "CartLineItem". Both were renamed, both deliberately, in one place,
// with one decision.
//
// # ~~There is NO other undescribed endpoint~~ — there were five more
//
// **Corrected 2026-09-07.** The sentence was true when it was written and stopped
// being true when the after-sales routes arrived; nothing in the repository
// could contradict it until [openapi.Doc.UndescribedRoutes] existed (ADR 0035),
// and the first measurement found these:
//
//   - GET  /admin/v1/orders/{id}/payment
//   - POST /admin/v1/orders/{id}/returns/{returnId}/receive
//   - POST /admin/v1/orders/{id}/returns/{returnId}/refund
//   - POST /admin/v1/orders/{id}/claims/{claimId}/settle
//   - POST /store/v1/orders/{id}/returns
//
// None of them is blocked by the collision above — they answer with return,
// refund and claim payloads, not with [orderDetailDTO] — so unlike the five
// named there, these were simply never written. They are described below.
//
// The lesson worth keeping is the FORM of the claim rather than the omission: a
// sentence that says "there is no other X" is a claim about the whole tree made
// from inside one file, and it goes stale the moment somebody adds an X
// somewhere the sentence's author is not looking. It survived because it was
// unfalsifiable here, not because it was checked.
//
// The order CREATION endpoint is absent from the document as well, because it
// has no route at all; the rationale is in the package documentation (an
// endpoint that takes the amounts from the client meant an order with a total
// of zero could be written).
//
// # Known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]) and that is the right answer for RESPONSE bodies. In
// a request body, however, "required" means the field the client MUST SEND, and
// the type cannot know that: because this package's request DTOs carry no
// omitempty, all of them look mandatory — "note", for example, which is
// optional when opening a return record, is asked for as well. The field NAMES
// and TYPES are correct, that is, the schema does not invent a field that does
// not exist; it merely asks for too much. Writing the limit down is deliberate:
// knowing that something is missing is better than not suspecting that it is.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders", openapi.Operation{
		Summary: "Lists orders with filtering and paging.",
		// The parameters are the ones the handler READS, not the ones we could
		// wish for: [Handler.adminListOrders] reads exactly these five. Line
		// items are NOT LOADED in the list, which is why there is no parameter
		// such as "expand" here either — had there been one, it would have
		// promised a feature the server ignores.
		Parameters: []openapi.Parameter{
			queryParameter("customer_id", typeString,
				"Limits the orders to a single customer."),
			queryParameter("region_id", typeString,
				"Limits the orders to a single region."),
			queryParameter("status", typeString,
				"Status filter: pending, completed, archived or canceled."),
			queryParameter("limit", typeInteger,
				"Page size; when it is not given the service's default applies."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
			queryParameter("after", typeString,
				"Opaque cursor from a previous page's \"next_cursor\". Cheaper than \"offset\" "+
					"for deep pages: offset makes the database walk and DISCARD every row it "+
					"skips, so its cost grows with depth, while a cursor becomes an index "+
					"condition and stays flat. "+
					"\"after\" and \"offset\" name two different positions and are REFUSED "+
					"together. When the response carries no \"next_cursor\" the listing is "+
					"exhausted."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Page of orders",
				d.List(orderDTO{}, openapi.WithCursor())),
		},
	})

	describeReturns(d)
	describeExchanges(d)
	describeClaims(d)
	describeInvoicing(d)
	describeFulfilling(d)
	describeTimeline(d)
}

// describeInvoicing describes the two endpoints that reach the invoicing flow.
//
// They are described HERE, with the order's other endpoints, because that is
// where they are mounted: the client asking "invoice this order" is holding an
// order id, and the document it gets back is the invoice module's shape.
func describeInvoicing(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/invoice", openapi.Operation{
		Summary: "Issues the invoice for the order, or returns the one it has.",
		Description: "Issuing is a DECISION and nothing does it automatically: when a shop " +
			"invoices — on payment, on dispatch, monthly for a corporate buyer — is a policy " +
			"the framework does not make. " +
			"A number is spent for good once it is taken, so a second call does NOT issue a " +
			"second document: it returns the one the order already has, with " +
			"\"already_issued\": true and a 200 instead of a 201. " +
			"The two parties come from THIS BODY and the lines come from the order. The " +
			"seller's legal details are the shop's own configuration and the buyer's tax " +
			"number is not in this framework's customer model, so neither can be guessed; " +
			"an empty buyer e-mail is filled in from the order. " +
			"Carriage reaches the document as a LINE, because that is how it is printed.",
		RequestBody: d.RequestBody(invoicingIssueRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The document the order already had",
				d.Item(invoiceIssuedDTO{})),
			"201": openapi.Response("The issued document", d.Item(invoiceIssuedDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/invoice", openapi.Operation{
		Summary: "Returns the identity of the invoice bound to the order.",
		Description: "404 when the order has no invoice. It answers with the document's id, " +
			"number and status rather than with the document: this endpoint answers a question " +
			"about an ORDER, and the document itself is served by /admin/v1/invoices/{id}, " +
			"where its shape lives.",
		Responses: map[string]any{
			"200": openapi.Response("The document's identity", d.Item(orderInvoiceDTO{})),
		},
	})
}

// describeFulfilling describes the two endpoints that reach the fulfilling flow.
//
// They are described HERE, with the order's other endpoints, because that is
// where they are mounted: the operator asking "ship this order" is holding an
// order id, and the fulfillment module's own create endpoint takes a reference
// it never validates.
func describeFulfilling(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/fulfillments", openapi.Operation{
		Summary: "Opens a shipment for the order and binds the two.",
		Description: "Shipping is a DECISION and nothing does it automatically: when a shop " +
			"ships — on payment, after picking, in one parcel or in three — is a policy the " +
			"framework does not make. " +
			"The idempotency key is REQUIRED. A second call with the same key does NOT open a " +
			"second parcel: it returns the one already open, with \"already_open\": true and a " +
			"200 instead of a 201. Without a key a retried request opens a second parcel and " +
			"the shop finds out at the carrier. " +
			"An unknown order id is REFUSED rather than opening a parcel bound to nothing — " +
			"the fulfillment module never validates the reference it is handed, so this is the " +
			"only place that can refuse. " +
			"The order may have SEVERAL shipments; the binding is one to many.",
		RequestBody: d.RequestBody(openShipmentRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The shipment that was already open",
				d.Item(shipmentOpenedDTO{})),
			"201": openapi.Response("The opened shipment", d.Item(shipmentOpenedDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/fulfillments", openapi.Operation{
		Summary: "Lists the shipments bound to the order.",
		Description: "This is the \"where is the parcel\" read. It answers with identities and " +
			"statuses rather than with the shipments: a client that wants a parcel's detail " +
			"reads it from /admin/v1/fulfillments/{id}, where its shape lives. " +
			"A status that could NOT be read comes back empty rather than failing the whole " +
			"request — the binding is a fact either way, and an order with three parcels one " +
			"of whose statuses is unreadable still has three parcels.",
		Responses: map[string]any{
			"200": openapi.Response("The order's shipments", d.Item(orderShipmentDTO{})),
		},
	})
}

// orderShipmentDTO is one shipment as the order's endpoint reports it.
//
// It exists so the OpenAPI document can describe the response; the body itself
// is the flow's JSON, passed through without being re-encoded.
type orderShipmentDTO struct {
	// FulfillmentID is the shipment's identifier.
	FulfillmentID string `json:"fulfillment_id"`
	// Status is empty when the fulfillment module could not be asked.
	Status string `json:"status"`
}

// describeReturns describes the return record endpoints.
func describeReturns(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/returns", openapi.Operation{
		Summary:    "Lists the order's return records with paging.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Page of return records", d.List(returnDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/returns", openapi.Operation{
		Summary:     "Opens a return record on the order.",
		Description: amountNote,
		RequestBody: d.RequestBody(createReturnRequest{}),
		Responses: map[string]any{
			// The handler writes 201 (see admin.go); a new record is born.
			"201": openapi.Response("The opened return record", d.Item(returnDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/returns/{returnId}", openapi.Operation{
		Summary: "Returns the return record by its id.",
		Responses: map[string]any{
			"200": openapi.Response("Return record", d.Item(returnDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/returns/{returnId}/cancel",
		openapi.Operation{
			Summary: "Withdraws the return request.",
			// The RELEASE is written out because it is the reason a client
			// would call this rather than ignoring the record: nothing else in
			// the API gives a line's returnable quantity back, and a client
			// that does not know it will report "more was asked back than was
			// bought" to a shop whose only open request was a mistake.
			Description: "Withdrawing RELEASES the units the request was holding: those " +
				"lines can be asked back again, which nothing else in this API can undo. " +
				"It is refused with 409 on a RECEIVED return — the goods are physically in " +
				"the warehouse and the record is the only thing that says where they came " +
				"from, so withdrawing would not un-receive them. A second call on an " +
				"already withdrawn record succeeds and keeps the first moment.",
			Responses: map[string]any{
				"200": openapi.Response("The withdrawn return record", d.Item(returnDTO{})),
			},
		})
}

// describeExchanges describes the exchange record endpoints.
func describeExchanges(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/exchanges", openapi.Operation{
		Summary:    "Lists the order's exchange records with paging.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Page of exchange records", d.List(exchangeDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/exchanges", openapi.Operation{
		Summary: "Opens an exchange record on the order.",
		// The SIGN of the difference is meaningful and cannot be read from the
		// type; if it is not written down, a client may present a difference
		// that is paid to the customer as if it were collected from them.
		Description: amountNote + " When difference_due is positive the difference " +
			"is collected from the customer, when it is negative it is paid to the customer.",
		RequestBody: d.RequestBody(createExchangeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened exchange record", d.Item(exchangeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/exchanges/{exchangeId}", openapi.Operation{
		Summary: "Returns the exchange record by its id.",
		Responses: map[string]any{
			"200": openapi.Response("Exchange record", d.Item(exchangeDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/exchanges/{exchangeId}/cancel",
		openapi.Operation{
			Summary: "Withdraws the exchange request.",
			// The two statuses and the ABSENT third are written out here for
			// the same reason the claim's type values are: the schema shows
			// "status" as a bare string, and a client that assumed the usual
			// requested/completed/canceled triple would wait for a completion
			// that cannot arrive.
			Description: "The only transition an exchange has. Its status is " +
				"\"requested\" or \"canceled\" and there is no completed state: " +
				"completing an exchange would need goods shipped out against an " +
				"existing order and, on a positive difference, money collected " +
				"against it, and this framework can do neither. A second call on " +
				"an already withdrawn record succeeds and keeps the first moment.",
			Responses: map[string]any{
				"200": openapi.Response("The withdrawn exchange record", d.Item(exchangeDTO{})),
			},
		})
}

// describeClaims describes the damage/shortage claim record endpoints.
func describeClaims(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/claims", openapi.Operation{
		Summary:    "Lists the order's claim records with paging.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Page of claim records", d.List(claimDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/claims", openapi.Operation{
		Summary: "Opens a damage/shortage claim record on the order.",
		// In the schema the type is only a "string"; the two accepted values
		// are enforced in the handler (see [Handler.adminCreateClaim]) and had
		// they not been written here, the client developer would have found the
		// valid value by trial and error.
		Description: amountNote + " type is mandatory and has to be \"refund\" or " +
			"\"replace\"; refund_amount is meaningful only for \"refund\".",
		RequestBody: d.RequestBody(createClaimRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened claim record", d.Item(claimDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/claims/{claimId}", openapi.Operation{
		Summary: "Returns the claim record by its id.",
		Responses: map[string]any{
			"200": openapi.Response("Claim record", d.Item(claimDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/claims/{claimId}/cancel",
		openapi.Operation{
			Summary: "Withdraws the claim.",
			// The pairing with settle is stated because the two are the claim's
			// ONLY exits and they are not interchangeable: a client that reads
			// only the settle endpoint would conclude that a claim opened by
			// mistake has to be settled with money to get it off the list.
			Description: "The claim's other exit, beside settling. Use it for a claim opened " +
				"against the wrong order, opened twice, or one the customer withdrew: " +
				"nothing is refunded and nothing is shipped, the record is simply closed. " +
				"It is refused with 409 on a COMPLETED claim — that one was met with money " +
				"or goods, and un-meeting it is a new record rather than a status change. " +
				"A second call on an already withdrawn claim succeeds and keeps the first " +
				"moment.",
			Responses: map[string]any{
				"200": openapi.Response("The withdrawn claim record", d.Item(claimDTO{})),
			},
		})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/claims/{claimId}/replacements",
		openapi.Operation{
			Summary: "Records what a claim of type \"replace\" will send.",
			// What it does NOT do is the part a client has to read, because the
			// endpoint's name suggests otherwise: this writes a record and
			// moves nothing.
			Description: "It records the promise and SENDS NOTHING: no stock moves and no " +
				"shipment is opened. Until this record exists nothing in the schema could " +
				"say what a claim settled with goods was going to send, which is why " +
				"settling such a claim is still refused. At least one line is required, " +
				"and more of a line cannot be promised than was bought on it (409).",
			RequestBody: d.RequestBody(createReplacementRequest{}),
			Responses: map[string]any{
				"201": openapi.Response("The recorded replacement", d.Item(replacementDTO{})),
			},
		})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/claims/{claimId}/replacements",
		openapi.Operation{
			Summary: "Lists the claim's replacements, newest first.",
			// The absence of paging is described rather than left to be
			// discovered: a client that expected a cursor would look for one.
			Description: "The list is not paged: a replacement belongs to one claim and the " +
				"count is bounded by the lines of a single order.",
			Responses: map[string]any{
				"200": openapi.Response("The claim's replacements", d.Item([]replacementDTO{})),
			},
		})

	d.Describe(http.MethodGet,
		"/admin/v1/orders/{id}/claims/{claimId}/replacements/{replacementId}",
		openapi.Operation{
			Summary: "Returns one replacement with its lines.",
			Responses: map[string]any{
				"200": openapi.Response("The replacement", d.Item(replacementDTO{})),
			},
		})

	d.Describe(http.MethodPost,
		"/admin/v1/orders/{id}/claims/{claimId}/replacements/{replacementId}/cancel",
		openapi.Operation{
			Summary: "Withdraws a replacement that has not been acted on.",
			Description: "A second call on an already withdrawn replacement succeeds and " +
				"keeps the first moment. While a replacement is open the claim it belongs " +
				"to cannot be withdrawn (409): the promise would otherwise outlive the " +
				"record that made it.",
			Responses: map[string]any{
				"200": openapi.Response("The withdrawn replacement", d.Item(replacementDTO{})),
			},
		})

	d.Describe(http.MethodPost,
		"/admin/v1/orders/{id}/claims/{claimId}/replacements/{replacementId}/dispatch",
		openapi.Operation{
			Summary: "Sends what the claim promised, and settles the claim by sending it.",
			// The three movements are named because each one is visible
			// somewhere else: the units leave the inventory module's count, the
			// parcel appears in the fulfillment module, and the claim closes
			// here. A caller that knew only "it dispatched" could not tell
			// which of the three to look at when something is missing.
			Description: "The units are set aside at the replacement's location, a parcel is " +
				"opened on the order with the replacement's shipping option, the units come " +
				"OUT of the physical count as a movement of their own reason, and the claim " +
				"is marked settled. It is refused with 409 when the record was withdrawn, " +
				"when a line's variant has no inventory item, and when there is not enough " +
				"stock to send. \n\n" +
				"Repeating it sends nothing a second time: already_sent then reports that " +
				"the goods had gone, and fulfillment_id names the parcel they left in.",
			Responses: map[string]any{
				"200": openapi.Response("What was sent", d.Item(dispatchReplacementResponse{})),
			},
		})
}

// pageParameters returns the two parameters [parsePage] reads.
//
// The after-sales lists have NO OTHER filter: the handlers take only the order
// id from the path (see admin.go). Writing a "status" filter here would have
// meant making the client generator put in an argument for a field the server
// would ignore.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"Page size; when it is not given the service's default applies."),
		queryParameter("offset", typeInteger, "Number of records to skip."),
	}
}

// queryParameter defines a parameter that is read from the query string.
//
// None of them IS mandatory: when it is not given, the handler either does not
// apply the filter at all or continues with the default (see [parsePage],
// [parseInt64Param]).
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}

// describeCreditLines documents the write-off.
func describeCreditLines(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/credit-lines", openapi.Operation{
		Summary: "Writes off part of what the order owes.",
		Description: "THE ORDER'S TOTAL DOES NOT MOVE. That figure is the cart's snapshot — what " +
			"was sold and at what price — and it is pinned to the order's own lines by a " +
			"constraint. A goodwill gesture, a price match or a compensation agreed after " +
			"the sale changes what the customer has left to pay, not what they bought. " +
			"\n\n" +
			"What changes is the summary's \"outstanding\", which is where a client should " +
			"look for the effect; the summary also carries \"credited_total\". " +
			"\n\n" +
			"A credit may not take the credited total past the order's total: writing off " +
			"more than the sale was ever worth is a data-entry error rather than a " +
			"concession. A credit granted AFTER the customer paid is legitimate and makes " +
			"the outstanding amount negative, which is this module's word for \"the shop " +
			"owes the customer\" and is what a refund then settles. " +
			"\n\n" +
			"The amount has to be POSITIVE. A negative credit is a CHARGE — a different act " +
			"with a different authorization — and it does not belong on this endpoint. The " +
			"reason is REQUIRED: a credit with no reason is a number nobody can answer a " +
			"question about six months later.",
		RequestBody: d.RequestBody(createCreditLineRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The credit that was written", d.Item(creditLineDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/credit-lines", openapi.Operation{
		Summary: "Lists the order's credit lines, oldest first.",
		Description: "The record is an ARRAY inside the plain envelope: the credits belong to " +
			"one order and there is no page to ask for.",
		Responses: map[string]any{
			"200": openapi.Response("The order's credit lines", d.Item([]creditLineDTO{})),
		},
	})
}

// describeTimeline documents the support desk's view.
func describeTimeline(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/timeline", openapi.Operation{
		Summary: "Everything that happened to the order, newest first.",
		Description: "It is COMPOSED from records that already exist — the order's own " +
			"stamps, its returns, claims and exchanges, the payment collection's capture " +
			"and refund moments, and every parcel's five moments — rather than read from a " +
			"log. Nothing is duplicated, so nothing can drift from the record it mirrors. " +
			"\n\n" +
			"THE MOMENTS DO NOT SHARE ONE CLOCK, and each entry says which one stamped it. " +
			"\"database\" means the moment came from the database's now(); \"application\" " +
			"means it came from whichever process wrote the row. The capture and a parcel's " +
			"shipped/delivered/canceled/returned moments are on the application clock; everything " +
			"else is on the database's. On one machine they agree. Across machines they can " +
			"disagree by more than the gap between two events, and then two lines look out " +
			"of order — the \"clock\" field is what explains it. " +
			"\n\n" +
			"An entry whose \"at\" is NULL is a fact that really happened and whose moment " +
			"was never recorded: an exchange that was completed or canceled, whose columns " +
			"exist and which nothing writes. Those come LAST, and they are reported rather " +
			"than dropped — a timeline shorter than the truth hides the gap instead of " +
			"showing it.",
		Responses: map[string]any{
			// The record is an ARRAY inside the plain envelope: the timeline is
			// bounded by its order and has no page to ask for, so it carries no
			// paging fields. Written as a single record it was a lie a client
			// generator turns into a parse failure (D44).
			"200": openapi.Response("The order's timeline", d.Item([]timelineEntryDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/orders/{id}/timeline", openapi.Operation{
		Summary: "What happened to the order, as the customer may see it.",
		Description: "It is the same composition the admin timeline returns, narrowed to the " +
			"moments about the ORDER and the GOODS: placed, completed, canceled, every " +
			"parcel's five moments, and the returns, claims and exchanges. " +
			"\n\n" +
			"THE MONEY MOMENTS ARE NOT HERE, and neither is \"order.archived\". A capture " +
			"or a refund recorded on this side is the merchant's ledger view — a partial " +
			"capture is a fact about a hold, and the figure a customer will reconcile " +
			"against is the one their bank shows on the day it lands. Archiving is the " +
			"merchant filing the order away; nothing happened to the goods or the money. " +
			"The response has NO amount field at all, so a moment carrying one could not " +
			"be published here even by mistake. " +
			"\n\n" +
			"Everything the admin timeline says about clocks holds here too: the moments do " +
			"not share one axis and each entry says which clock stamped it. An entry whose " +
			"\"at\" is NULL really happened and its moment was never recorded; those come " +
			"LAST. " +
			"\n\n" +
			"KNOWING THE ORDER ID IS THE CAPABILITY. This route names an order rather than " +
			"a customer, so the embedding application is what decides whether the caller may " +
			"see it (ADR 0008), exactly as on the order read.",
		Responses: map[string]any{
			"200": openapi.Response("The order's timeline", d.Item([]storeTimelineEntryDTO{})),
		},
	})

	describeCreditLines(d)
	describeAfterSales(d)
	describeOrderDetail(d)
}

// describeAfterSales records the return, refund and claim endpoints.
//
// # Why they are in a function of their own
//
// Not for length. These five are the ones the "there is no other undescribed
// endpoint" sentence above did not know about, and keeping them together keeps
// the correction and its subject in one place. None of them is blocked by the
// component-name collision that keeps the order DETAIL endpoints bodiless: they
// answer with receipt, refund and claim payloads, and never touch
// [orderDetailDTO] or the [lineItemDTO] inside it.
func describeAfterSales(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/orders/{id}/payment", openapi.Operation{
		Summary: "The order's payment position in one record.",
		Description: "Answers what a shop asks when it looks at an order: how much was " +
			"agreed, how much is authorized, how much has actually moved and how much has " +
			"gone back. " + amountNote +
			"\n\n" +
			"The two moments are NULLABLE and the null is meaningful: \"first_captured_at\" " +
			"is empty until money moves for the first time and \"last_refunded_at\" until a " +
			"refund goes out. A zero time would read as the first of January in year one to " +
			"whoever is drawing a timeline, which is why the field is a null rather than a " +
			"zero. " +
			"\n\n" +
			"The figures come from the payment module's collection for this order; this " +
			"endpoint READS them and owns none of them.",
		Responses: map[string]any{
			"200": openapi.Response("The order's payment position", d.Item(orderPaymentDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/returns/{returnId}/receive", openapi.Operation{
		Summary: "Records that returned goods arrived and puts their stock back.",
		Description: "The body names the stock LOCATION the goods arrived at, and it is " +
			"required: a receipt without a location would put the units back into a " +
			"warehouse nobody chose. " +
			"\n\n" +
			"A 200 CARRYING WARNINGS is a real outcome and not a contradiction. The goods " +
			"arrived — that is a physical fact — and the record says so, while something " +
			"about the stock still needs a human: a line whose variant no longer exists, a " +
			"location that refused the units. Refusing the receipt instead would deny the " +
			"fact and leave the operator with no record to work from. Read " +
			"\"restocked_lines\" and \"restocked_units\" for what DID happen and " +
			"\"warnings\" for what did not; an empty warnings list is the ordinary case " +
			"and the field is then absent.",
		RequestBody: d.RequestBody(receiveReturnRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("What the receipt restocked, and what needs a human",
				d.Item(receiveReturnResponse{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/returns/{returnId}/refund", openapi.Operation{
		Summary: "Sends money back for a received return.",
		Description: "It is a SEPARATE call from receiving on purpose: receiving is a " +
			"physical fact, refunding is a decision the shop makes after looking at what " +
			"arrived. One endpoint doing both would refund goods nobody has inspected. " +
			"\n\n" +
			"\"amount\" ZERO is not a no-op — it means everything the payment collection " +
			"has left, which is what \"give the customer their money back\" means when " +
			"nobody named a figure. Send an explicit amount for a partial refund. " +
			amountNote +
			"\n\n" +
			"\"reason\" is free text kept on the refund record and is optional. " +
			"\"summary_recorded\" says whether the return's own summary was updated; a " +
			"false there with a non-zero \"refunded_amount\" means the money went back and " +
			"the bookkeeping did not, which is exactly what the warnings are for.",
		RequestBody: d.RequestBody(refundReturnRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("How much went back, and what needs a human",
				d.Item(refundReturnResponse{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/orders/{id}/claims/{claimId}/settle", openapi.Operation{
		Summary: "Settles a claim by refunding it.",
		Description: "The body and the answer are the refund endpoint's, because settling a " +
			"claim IS a refund: zero means whatever the collection has left, and the same " +
			"two figures come back. " + amountNote +
			"\n\n" +
			"A claim to be settled with a REPLACEMENT comes back as a CONFLICT, and the " +
			"message says why: shipping goods against an existing order is not something " +
			"this framework can do. Stamping such a claim complete would record a settlement " +
			"that never reached the customer, which is worse than refusing it.",
		RequestBody: d.RequestBody(refundReturnRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("How much was refunded against the claim",
				d.Item(refundReturnResponse{})),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/orders/{id}/returns", openapi.Operation{
		Summary: "The customer asks to send order lines back.",
		Description: "This is a REQUEST and not a return: it opens a record for the shop to " +
			"act on, and nothing moves until an operator receives the goods. " +
			"\n\n" +
			"At least one line is required. The body names order line items and quantities " +
			"and CANNOT name a refund figure — the created record's refund amount is left at " +
			"zero for the shop to fill in. A body that could say what the return is worth " +
			"would let a customer decide their own refund, which is the same defect as a " +
			"cart that names its own shipping price. " +
			"\n\n" +
			"\"reason\" is optional free text.",
		RequestBody: d.RequestBody(storeReturnRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened return request", d.Item(returnDTO{})),
		},
	})
}
