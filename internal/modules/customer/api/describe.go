package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names used in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and the mistake surfaces only when a client reading the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
)

// Describe records customer's endpoints into the OpenAPI document.
//
// # Why it is in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (customerRequest, customerDTO …), and the schema is derived from them by
// reflection. Exporting the types so that they can be described would widen
// the module's surface merely to produce a document: an exported type is a
// contract and would become constructible from outside. The query parameters
// belong here too, because the code that ACTUALLY reads them ([pageParams],
// [boolParam], [stringParam]) is in this package; were the description in
// another package, the two would drift apart silently. That is why the
// module's [openapi.Describer] implementation delegates here.
//
// # Why a package-level function
//
// The description looks at no run-time state — the schema comes from the
// TYPES. Attaching the method to [Handler] would say the document DEPENDS on
// the service having been set up; yet the document can be produced, and has to
// be, even when Register has never run.
//
// # The address endpoints are described in describe_address.go
//
// They went undescribed until ADR 0036: this package's [addressDTO] and
// [addressRequest] asked for the same component names as the same-named types
// in cart/api, and a clash stops the whole document from building. ADR 0036
// prefixes every component name with its module, and describe_address.go
// describes the twelve.
//
// # A known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), and that is the correct answer for RESPONSE bodies.
// On a request body, however, "required" means a field the client HAS TO SEND,
// and the type cannot know that: because this package's request DTOs carry no
// omitempty, they all look mandatory — for example POST /store/v1/customers
// also asks for phone and metadata, which may be left empty. The field NAMES
// and TYPES are correct, so the schema does not invent a wrong field; it merely
// asks for too much. The correct fix is IN THE CORE (a separate "required"
// policy for request bodies); sprinkling omitempty over the tags would move the
// requirement from the service's validation to the json tag, and the two would
// drift apart silently.
func Describe(d *openapi.Doc) {
	describeCustomers(d)
	describeGroups(d)
	describeStorefront(d)

	// The twelve address endpoints; see describe_address.go for why they are in
	// a file of their own and why they were undescribed until ADR 0036.
	describeAddresses(d)
	describeWishlist(d)
	describeSegments(d)
}

// describeCustomers describes the customer admin endpoints.
func describeCustomers(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/customers", openapi.Operation{
		Summary: "Creates a registered customer account.",
		Description: "The admin endpoint always opens an ACCOUNT; a guest record is " +
			"part of the storefront flow (POST /store/v1/customers).",
		RequestBody: d.RequestBody(customerRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created customer", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customers", openapi.Operation{
		Summary: "Lists customers, filtered and paginated.",
		// The parameters are the ones the handler READS, not the ones we might
		// want: [Handler.adminListCustomers] reads exactly these six.
		Parameters: []openapi.Parameter{
			queryParameter("email", typeString,
				"Filters by email; also returns GUEST records, "+
					"so more than one row may come back."),
			queryParameter("has_account", typeBoolean,
				"true returns only registered accounts, false only guests."),
			queryParameter("group_id", typeString, "Limits the customers to a single group."),
			queryParameter("limit", typeInteger,
				"The page size; when omitted, the service's default applies."),
			queryParameter("offset", typeInteger, "The number of records to skip."),
			queryParameter("after", typeString,
				"The previous page's \"next_cursor\" value. Cheaper than \"offset\" "+
					"for deep pages: offset makes the database walk and DISCARD every row "+
					"it skips, so its cost grows with depth, while a cursor becomes an index "+
					"condition and stays flat. \"after\" and \"offset\" name two different "+
					"positions and are REFUSED together. When the response carries no "+
					"\"next_cursor\" the listing is exhausted."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of customers",
				d.List(customerDTO{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Returns a single customer by ID.",
		Responses: map[string]any{
			"200": openapi.Response("The customer", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Updates the given fields of a customer.",
		Description: "The semantics are PARTIAL: a field absent from the body does " +
			"not change, and an empty string that is given is a real clear.",
		RequestBody: d.RequestBody(updateCustomerRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated customer", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Soft-deletes the customer and its addresses.",
		Responses: map[string]any{
			"204": emptyResponse("The customer was deleted"),
		},
	})

	// The endpoint READS no body (see [Handler.adminConvertGuest]); writing a
	// requestBody would mean the client generator putting an argument on the
	// method to be filled in and the server silently ignoring it.
	d.Describe(http.MethodPost, "/admin/v1/customers/{id}/convert-to-account", openapi.Operation{
		Summary: "Converts a guest record into a registered account.",
		Description: "The response is the record AFTER CONVERSION; the client does " +
			"not need a second request to see the has_account field.",
		Responses: map[string]any{
			"200": openapi.Response("The customer converted into an account",
				d.Item(customerDTO{})),
		},
	})

	// It is an unpaginated list and READS no query string; the envelope is
	// still the list envelope (see [writeItems]), so the envelope shape the
	// client sees does not change from one endpoint to another.
	d.Describe(http.MethodGet, "/admin/v1/customers/{id}/groups", openapi.Operation{
		Summary: "Returns the groups the customer is a member of.",
		Responses: map[string]any{
			"200": openapi.Response("The customer's groups", d.List(customerGroupDTO{})),
		},
	})
}

// describeGroups describes the customer group endpoints.
func describeGroups(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/customer-groups", openapi.Operation{
		Summary:     "Creates a new customer group.",
		RequestBody: d.RequestBody(groupRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created group", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customer-groups", openapi.Operation{
		Summary: "Lists customer groups, paginated.",
		Parameters: []openapi.Parameter{
			queryParameter("limit", typeInteger,
				"The page size; when omitted, the service's default applies."),
			queryParameter("offset", typeInteger, "The number of records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of groups", d.List(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary: "Returns a single customer group by ID.",
		Responses: map[string]any{
			"200": openapi.Response("The group", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary:     "Updates the given fields of a group.",
		RequestBody: d.RequestBody(updateGroupRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated group", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary: "Soft-deletes the customer group.",
		Responses: map[string]any{
			"204": emptyResponse("The group was deleted"),
		},
	})

	// It TAKES a body but RETURNS none: a membership is a link, not a record,
	// and the handler writes 204. Using Item/List would lead the client to
	// expect a body to read.
	d.Describe(http.MethodPost, "/admin/v1/customer-groups/{id}/customers", openapi.Operation{
		Summary: "Adds a customer to the group.",
		Description: "The operation is idempotent; it also returns 204 for a " +
			"customer who is already a member.",
		RequestBody: d.RequestBody(groupMemberRequest{}),
		Responses: map[string]any{
			"204": emptyResponse("The customer was added to the group"),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customer-groups/{id}/customers/{customer_id}",
		openapi.Operation{
			Summary: "Removes a customer from the group.",
			Responses: map[string]any{
				"204": emptyResponse("The customer was removed from the group"),
			},
		})
}

// describeStorefront describes the storefront endpoints for the customer's own
// profile. The address endpoints are in describe_address.go.
func describeStorefront(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/customers", openapi.Operation{
		Summary: "Opens a guest customer record.",
		Description: "There can be more than one guest record with the same email: " +
			"a guest record is not an identity but the contact details of a " +
			"one-off purchase.",
		RequestBody: d.RequestBody(customerRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created guest", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/customers/{id}", openapi.Operation{
		Summary: "Returns the customer's own profile.",
		Description: "The customer named in the path has to be the customer the " +
			"request proves; see the refusal codes below.",
		Responses: answers(storefrontIdentityRefusals(), "200",
			openapi.Response("The customer profile", d.Item(customerDTO{}))),
	})

	d.Describe(http.MethodPut, "/store/v1/customers/{id}", openapi.Operation{
		Summary: "Updates the customer's own profile.",
		Description: "The customer named in the path has to be the customer the " +
			"request proves; see the refusal codes below.\n\n" +
			"The e-mail address is not changed here: it is the address the account " +
			"signs in under and receives its mail at, and nothing here proves the " +
			"shopper owns a new one. A body carrying email is refused as a field this " +
			"endpoint does not know; an operator changes the address (ADR 0376).",
		RequestBody: d.RequestBody(storeUpdateCustomerRequest{}),
		Responses: answers(storefrontIdentityRefusals(), "200",
			openapi.Response("The updated profile", d.Item(customerDTO{}))),
	})
}

// queryParameter defines a parameter read from the query string.
//
// None of them is REQUIRED: when they are omitted, the handler applies no
// filter or continues with the service's default (see [pageParams],
// [boolParam], [stringParam]).
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse builds a response definition WITHOUT a body.
//
// [openapi.Response] always writes a body schema, whereas a 204 HAS no body
// (see the calls that pass nil to corehttp.WriteJSON). Writing an empty schema
// would say "something comes back but its shape is unknown", and the client
// generator would produce a method that expects a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
