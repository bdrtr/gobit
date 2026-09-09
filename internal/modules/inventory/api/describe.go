package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names the parameter schemas use.
//
// The core's own are unexported, and they are repeated here for SILENCE rather
// than cost: a type name written "strig" compiles, produces a document, and
// surfaces only when the client reading the schema generates the parameter with
// the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
)

// Describe writes EVERY one of inventory's endpoints into the OpenAPI document.
//
// # Why it lives in this package
//
// The bodies it describes are this package's UNEXPORTED types
// (createItemRequest, inventoryLevelDTO …) and the schema is derived from them
// by reflection. Exporting them so they could be described would widen the
// module's surface for the sake of a document: an exported type is a contract
// and would become constructible from outside. The query parameters are the
// ones the handler ACTUALLY reads ([parsePage], [Handler.listItems]); had the
// description lived in another package it would drift from the read and the two
// would part silently. The module's [openapi.Describer] therefore delegates
// here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the TYPES.
// Binding it to [Handler] would say the document DEPENDS on the service being
// built, when the document can and should be produced without Routes ever
// having run.
//
// # There is only /admin/v1
//
// The module has NO storefront endpoint (see the package doc): a shopper sees
// stock through the product listing, by way of the Query layer's provider. The
// 12 endpoints here are therefore the WHOLE module, and must not be read as
// "the storefront is undescribed".
//
// # The path constants are SHARED with the routes
//
// The described paths are given as constants such as [pathItems], never as
// hand-written strings. Written by hand, a path change would silently strand
// the description; the core reports that through
// [openapi.Doc.UnmatchedDescriptions], but a report can go unread — a constant
// never produces the fault at all.
//
// # A known limit: the "required" set of a request body is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), which is the right answer for a RESPONSE body. In a
// request body "required" means the field a client MUST SEND, and the type
// cannot know that: this package's request types carry no omitempty, so every
// field looks required — POST /admin/v1/stock-locations asks for address_2 and
// province, both of which may be left empty. The field NAMES and TYPES are
// right, so the schema invents no field; it only asks for too much. The proper
// fix is in the CORE (a separate "required" policy for request bodies);
// sprinkling omitempty over the tags would move the requirement out of the
// service's validation and into a json tag, and the two would part silently.
func Describe(d *openapi.Doc) {
	describeLocations(d)
	describeItems(d)
	describeLevels(d)
	describeMovements(d)
}

// describeLocations describes the stock location endpoints.
func describeLocations(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStockLocations, openapi.Operation{
		Summary:     "Creates a new stock location.",
		RequestBody: d.RequestBody(createStockLocationRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created location", d.Item(stockLocationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStockLocations, openapi.Operation{
		Summary: "Lists the stock locations, paged.",
		// The parameters are the ones [Handler.listStockLocations] READS: the
		// pagination pair ([parsePage]) and the closed-location switch.
		// include_closed is a BOOLEAN and appears as one in the schema — the
		// handler reads it with the standard library's boolean parser.
		Parameters: append(pagingParameters(),
			queryParameter("include_closed", typeBoolean,
				"With true the closed locations come back too; the default is the open ones only."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of locations", d.List(stockLocationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStockLocation, openapi.Operation{
		Summary: "Returns a single stock location, a closed one included.",
		Responses: map[string]any{
			"200": openapi.Response("The stock location", d.Item(stockLocationDTO{})),
		},
	})

	// The close is a BODILESS POST and answers 200, not 204: it returns the NEW
	// state of the record, which is exactly what the caller needs to see
	// (closed_at). It is not described as a DELETE, because a close is not a
	// deletion (ADR 0055).
	d.Describe(http.MethodPost, pathStockLocationClose, openapi.Operation{
		Summary: "Closes the stock location; refused while it holds stock or an active reservation.",
		Responses: map[string]any{
			"200": openapi.Response("The closed location", d.Item(stockLocationDTO{})),
		},
	})

	describeLocationSalesChannels(d)
}

// describeLocationSalesChannels describes the warehouse-to-channel binding.
func describeLocationSalesChannels(d *openapi.Doc) {
	// What the binding MEANS is written here rather than left to the reader,
	// because the empty case is the one a merchant gets wrong: a channel bound
	// to no warehouse is not restricted at all.
	const meaning = "A channel with at least one warehouse bound is served ONLY by those " +
		"warehouses: an order placed on it reserves stock from them and from nowhere else. " +
		"A channel bound to no warehouse is not a channel that ships from nowhere — it is " +
		"one nobody has configured, and it is served by every warehouse."

	d.Describe(http.MethodGet, pathLocationChannels, openapi.Operation{
		Summary:     "Lists the sales channels this warehouse ships for.",
		Description: meaning,
		Responses: map[string]any{
			"200": openapi.Response("The channels bound to the warehouse",
				d.Item(salesChannelsResponse{})),
		},
	})

	d.Describe(http.MethodPost, pathLocationChannels, openapi.Operation{
		Summary: "Binds the warehouse to a sales channel it ships for.",
		Description: meaning + " Binding a pair twice is one binding. The CHANNEL is not " +
			"verified: it belongs to another module and this one validates no foreign " +
			"reference, so a mistyped id is recorded and shows up as a channel nothing serves.",
		RequestBody: d.RequestBody(salesChannelBindingRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The channels bound after the write",
				d.Item(salesChannelsResponse{})),
		},
	})

	d.Describe(http.MethodDelete, pathLocationChannel, openapi.Operation{
		Summary: "Removes the binding between the warehouse and the channel.",
		Description: "Removing a binding that is not there succeeds: that is the state the " +
			"caller asked for. The answer is the remaining list either way.",
		Responses: map[string]any{
			"200": openapi.Response("The channels bound after the removal",
				d.Item(salesChannelsResponse{})),
		},
	})
}

// describeItems describes the inventory item endpoints.
func describeItems(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathItems, openapi.Operation{
		Summary:     "Creates a new inventory item.",
		RequestBody: d.RequestBody(createItemRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created inventory item", d.Item(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathItems, openapi.Operation{
		Summary: "Lists the inventory items, filtered and paged.",
		// The parameters are the ones [Handler.listItems] READS: the pagination
		// pair and two filters. requires_shipping is a BOOLEAN and appears as
		// one in the schema — described as a string, the client generator would
		// produce an argument taking free text instead of "true", and the
		// server would count a value it could not parse as an error.
		Parameters: append(pagingParameters(),
			queryParameter("sku", typeString,
				"Narrows the items to a single SKU."),
			queryParameter("requires_shipping", typeBoolean,
				"true returns only the items that need shipping, false only those that do not."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of inventory items", d.List(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathItem, openapi.Operation{
		Summary: "Returns a single inventory item.",
		Responses: map[string]any{
			"200": openapi.Response("The inventory item", d.Item(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathItem, openapi.Operation{
		Summary: "Deletes the inventory item.",
		Responses: map[string]any{
			"204": emptyResponse("The inventory item was deleted"),
		},
	})
}

// describeLevels describes the stock level endpoints.
//
// Both are WRITES and both answer 200, NOT 201: the level row already exists at
// the intersection of the item and the location (or the service opens it
// silently), so the endpoints do not CREATE a resource, they change a quantity
// that is already there (see [Handler.setLevel], [Handler.adjustLevel]).
// Writing 201 would produce a client method landing on the "created" branch.
func describeLevels(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathItemLevels, openapi.Operation{
		Summary: "Returns the item's stock levels across every location.",
		// The endpoint is NOT paged ([Handler.listLevels] never reads the query
		// string), and its envelope is still the list envelope: the shape a
		// client sees does not change from endpoint to endpoint.
		Responses: map[string]any{
			"200": openapi.Response("The item's stock levels", d.List(inventoryLevelDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemLevels, openapi.Operation{
		Summary:     "Writes the PHYSICAL quantity at a location, absolutely.",
		RequestBody: d.RequestBody(setLevelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The written stock level", d.Item(inventoryLevelDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemLevelAdjust, openapi.Operation{
		Summary:     "Changes the physical quantity at a location by a delta.",
		RequestBody: d.RequestBody(adjustLevelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated stock level", d.Item(inventoryLevelDTO{})),
		},
	})
}

// pagingParameters returns the query parameters [parsePage] reads.
//
// Neither is required: absent, the default page applies (see [parsePage],
// service.DefaultLimit).
//
// It builds a NEW slice on every call. Sharing one package-level value would
// let a caller that appends a filter onto it ([describeItems]) silently change
// the other caller's list too.
func pagingParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"Page size; absent, the default page size applies."),
		queryParameter("offset", typeInteger, "How many records to skip."),
	}
}

// queryParameter declares a parameter read from the query string.
func queryParameter(name, kind, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: kind},
		Description: description,
	}
}

// emptyResponse declares a response with NO body.
//
// [openapi.Response] always writes a body schema, and a 204 HAS no body (see
// [Handler.deleteItem], the call handing corehttp.WriteJSON a nil). Writing an
// empty schema would say "something comes back and its shape is unknown", and
// the client generator would produce a method expecting a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
