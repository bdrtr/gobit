package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
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
// # FIVE ENDPOINTS LEFT UNDESCRIBED: the component name "LineItem" COLLIDES
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
// All five appear in the document with their path, method and security; only
// their bodies are missing. The fix is to RENAME one of the types, and that
// decision cannot be made from inside this module: the component name is the
// CLASS NAME that client generators PRODUCE, that is, a published contract, and
// changing it once a client has been generated is breaking. Taking a decision
// that concerns two modules at once from inside a single module would break the
// other module's client without warning.
//
// # There is NO other undescribed endpoint
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
