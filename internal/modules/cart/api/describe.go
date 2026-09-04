package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The JSON Schema names appearing in the parameter schemas.
//
// The core's counterparts are unexported and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the document
// is produced, and it only surfaces when the client reading the schema produces
// the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
)

// Describe records cart's endpoints into the OpenAPI document.
//
// # Why it is in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (createCartRequest, cartDTO …) and the schema is derived from them by
// reflection. Exporting the types in order to be able to describe them would
// mean widening the module's surface merely for the sake of producing a
// document: an exported type is a contract and would become constructible from
// the outside. That is why the module's [openapi.Describer] implementation
// delegates here.
//
// # Why a package-level function
//
// The description looks at no run-time state — the schema comes from the TYPES.
// Attaching the method to [Handler] would say the document DEPENDED on the
// service having been set up; whereas the document can be produced, and has to
// be producible, even when Register has never run.
//
// # Both surfaces are described
//
// The storefront endpoints are the need of a store client (storefront, SDK), the
// /admin/v1 endpoints that of the admin panel. The admin surface is READ ONLY
// (see admin.go); that is why its description carries no request body at all —
// what is described is not a write endpoint without a body but a read endpoint
// that HAS no body.
//
// # A known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]) and that is the correct answer for RESPONSE bodies.
// On a request body, however, "required" means the field the client HAS TO SEND
// and the type cannot know that: because this package's request DTOs carry no
// omitempty, they all look mandatory — for example POST /store/v1/carts asks for
// the customer_id and the email that are left empty on a guest cart too. The
// field NAMES and TYPES are correct, that is, the schema does not invent a wrong
// field; it merely asks for too much. Writing the limit down is deliberate:
// knowing that it is missing is better than believing it is not. The correct fix
// is IN THE CORE (a separate "required" policy for request bodies); sprinkling
// omitempty over the tags would move the requirement from the service's
// validation to the json tag and the two would drift apart silently.
//
// # Only the admin list reads a query parameter
//
// None of the storefront cart endpoints look at the query string (see store.go)
// and their schemas announce no parameter either; the ONLY endpoint that reads
// the query string is GET /admin/v1/carts (see admin.go and [parsePage]).
// Writing a parameter that is not read into the schema would mean promising the
// client a feature that DOES NOT WORK: the client generator puts an argument on
// the method, the caller fills it in and the server silently ignores it.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts", openapi.Operation{
		Summary:     "Opens a new cart; the server derives the region and the currency from the country.",
		RequestBody: d.RequestBody(createCartRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created cart", d.Item(cartDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/carts/{id}", openapi.Operation{
		Summary: "Returns the cart with its line items, addresses and shipping methods.",
		Responses: map[string]any{
			"200": openapi.Response("The cart and its children", d.Item(cartDetailDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/carts/{id}", openapi.Operation{
		Summary:     "Updates the cart's email and customer.",
		RequestBody: d.RequestBody(updateCartRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated cart", d.Item(cartDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}", openapi.Operation{
		Summary: "Deletes the cart.",
		Responses: map[string]any{
			"204": emptyResponse("The cart was deleted"),
		},
	})

	d.Describe(http.MethodPost, "/store/v1/carts/{id}/complete", openapi.Operation{
		Summary:     "Turns the cart into an order: stock is reserved, the payment is captured, the cart is closed.",
		RequestBody: d.RequestBody(completeCartRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The resulting order and the captured amount", d.Item(completeCartDTO{})),
		},
	})

	describeLineItems(d)
	describeAddresses(d)
	describeShipping(d)
	describeAdmin(d)
}

// describeLineItems describes the cart line item endpoints.
func describeLineItems(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts/{id}/line-items", openapi.Operation{
		Summary:     "Adds a line item to the cart; the server decides the unit price and the title.",
		RequestBody: d.RequestBody(addLineItemRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The added line item", d.Item(lineItemDTO{})),
		},
	})

	// A quantity update produces TWO successful outcomes: a 200 with its record
	// when the line item stays, and a bodyless 204 when it is removed with a
	// quantity of ZERO. Not writing the second one down would make the client
	// generator produce a single return type that expects a body, and that client
	// would try to decode the empty body of the 204.
	d.Describe(http.MethodPatch, "/store/v1/carts/{id}/line-items/{line_item_id}", openapi.Operation{
		Summary:     "Updates the cart line item's quantity; a quantity of zero removes the line item.",
		RequestBody: d.RequestBody(updateLineItemRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated line item", d.Item(lineItemDTO{})),
			"204": emptyResponse("A quantity of zero was given, the line item was removed"),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}/line-items/{line_item_id}", openapi.Operation{
		Summary: "Removes the cart line item.",
		Responses: map[string]any{
			"204": emptyResponse("The line item was removed"),
		},
	})
}

// describeAddresses describes the cart address endpoints.
//
// The two endpoints carry the SAME body ([addressRequest]) and the SAME record
// ([addressDTO]); the only thing separating them is the address's type and that
// shows up in the response's "type" field.
func describeAddresses(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/store/v1/carts/{id}/shipping-address", openapi.Operation{
		Summary:     "Writes the cart's shipping address.",
		RequestBody: d.RequestBody(addressRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The written shipping address", d.Item(addressDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/store/v1/carts/{id}/billing-address", openapi.Operation{
		Summary:     "Writes the cart's billing address.",
		RequestBody: d.RequestBody(addressRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The written billing address", d.Item(addressDTO{})),
		},
	})
}

// describeShipping describes the shipping method endpoints.
func describeShipping(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/carts/{id}/shipping-methods", openapi.Operation{
		Summary:     "Adds a shipping method to the cart.",
		RequestBody: d.RequestBody(addShippingMethodRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The added shipping method", d.Item(shippingMethodDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/store/v1/carts/{id}/shipping-methods/{shipping_method_id}",
		openapi.Operation{
			Summary: "Removes the shipping method from the cart.",
			Responses: map[string]any{
				"204": emptyResponse("The shipping method was removed"),
			},
		})
}

// describeAdmin describes the /admin/v1 cart endpoints.
//
// The surface is READ ONLY (see admin.go), therefore no endpoint has a
// requestBody. It is written here so that this is not taken for a "missing
// description": the only party that changes the cart is the customer and there
// is NO write endpoint on the admin side.
func describeAdmin(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/carts", openapi.Operation{
		Summary: "Pages the carts by customer, region and completion state.",
		// The parameters are the ONES [Handler.adminListCarts] READS; adding
		// another one would mean promising the client a filter that does not work.
		Parameters: []openapi.Parameter{
			queryParameter("customer_id", typeString,
				"Limits the carts to a single customer."),
			queryParameter("region_id", typeString,
				"Limits the carts to a single region."),
			queryParameter("completed", typeBoolean,
				"true returns only completed carts, false only open ones."),
			queryParameter("limit", typeInteger,
				"Page size; if it is not given the service's default is applied."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
		},
		Responses: map[string]any{
			// The record is NOT [cartDetailDTO] but [cartDTO]: the list endpoint
			// does NOT LOAD the line items, the addresses and the shipping methods
			// (that would open it up to N+1). Writing the detail schema would mean
			// promising the client fields that are never filled in.
			"200": openapi.Response("A page of carts", d.List(cartDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/carts/{id}", openapi.Operation{
		Summary: "Returns a single cart with its line items, addresses and shipping methods.",
		Responses: map[string]any{
			"200": openapi.Response("The cart and its children", d.Item(cartDetailDTO{})),
		},
	})
}

// queryParameter defines a parameter read from the query string.
//
// None of them is mandatory: when one is not given the handler carries on with
// the default (see [parsePage] and [Handler.adminListCarts]).
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}

// emptyResponse produces a BODYLESS response definition.
//
// [openapi.Response] always writes a body schema; a 204, on the other hand, HAS
// no body (see store.go, the calls that pass nil to corehttp.WriteJSON). Writing
// an empty schema would mean saying "something is returned but its shape is
// unknown" and the client generator would produce a method that expects a body
// to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
