package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names parameter schemas use.
//
// The core's own constants are unexported, and the reason they are repeated
// here is not cost but SILENCE: a type name spelled "strig" compiles, the
// document is built, and it surfaces only when a client generated from the
// schema types the parameter wrong.
const (
	schemaType         = "type"
	schemaFormat       = "format"
	typeString         = "string"
	typeInteger        = "integer"
	formatDateTimeName = "date-time"
)

// Describe writes EVERY endpoint of pricing into the OpenAPI document.
//
// # Why in this package
//
// The bodies described are this package's UNEXPORTED DTOs (priceSetDTO,
// priceListRequest …) and the schema is derived from them by reflection.
// Exporting the types just to describe them would widen the module's surface
// for the sake of a document: an exported type is a contract, and it could be
// built from outside. The query parameters are what the handlers REALLY read,
// and that reading lives in this package's api.go ([pageParams],
// [calculateQuery], [timelineQuery]); a description kept in another package
// would drift from it in silence. The module's [openapi.Describer] therefore
// delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from TYPES.
// Binding it to [API] would say the document depends on a built service, while
// the document can and must be built before Routes has ever run.
//
// # Why the admin surface is described too
//
// All of pricing's endpoints but one live under /admin/v1, and it is the only
// way to WRITE a price. Describing only the storefront would leave the write
// methods of a generated client without bodies or returns: a client that
// cannot SET a price. An undescribed endpoint is a valid model and a useless
// stub; none is left here.
//
// # Known limit: the "required" set of request bodies is WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), which is the right answer for RESPONSE bodies. In a
// request body "required" means a field the client MUST SEND, which a type
// cannot know: this package's request DTOs carry no omitempty, so every field
// looks required — PUT /admin/v1/price-lists/{id}, for instance, asks for the
// description and status that may be left empty. Field NAMES and TYPES are
// right, so the schema invents no field; it only asks for too much. The right
// fix is in the CORE (a separate "required" policy for request bodies);
// sprinkling omitempty on tags would move the obligation from the service's
// validation to a json tag, and the two would drift in silence.
func Describe(d *openapi.Doc) {
	describePriceSets(d)
	describePrices(d)
	describePriceLists(d)
	describePriceRules(d)
	describeStore(d)
	describePriceHistory(d)
}

// describePriceSets describes the price set endpoints.
func describePriceSets(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/price-sets", openapi.Operation{
		Summary:     "Creates a price set and writes the prices in the body.",
		RequestBody: d.RequestBody(createPriceSetRequest{}),
		Responses: map[string]any{
			// 201 is the code the handler REALLY writes (see
			// [API.createPriceSet]); the response carries the set together with
			// the prices just written.
			"201": openapi.Response("The price set created", d.Item(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets", openapi.Operation{
		Summary: "Lists price sets a page at a time.",
		// [API.listPriceSets] reads ONLY these two from the query string
		// ([pageParams]); writing another would promise the client a filter that
		// does not work.
		Parameters: pageParameters(),
		Responses: map[string]any{
			// The list response carries NO prices (see [toPriceSetSummaryDTO])
			// but the schema comes from the same type: "prices" is omitempty, so
			// it is not required and a client sees it as optional.
			"200": openapi.Response("A page of price sets", d.List(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}", openapi.Operation{
		Summary: "Returns one price set with its prices.",
		Responses: map[string]any{
			"200": openapi.Response("The price set and its prices", d.Item(priceSetDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-sets/{id}", openapi.Operation{
		Summary: "Deletes a price set and its prices.",
		Responses: map[string]any{
			"204": emptyResponse("The price set was deleted"),
		},
	})
}

// describePrices describes the endpoints that read and write a set's prices.
func describePrices(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}/prices", openapi.Operation{
		Summary: "Returns every price of a set with its rules.",
		// The endpoint is NOT paged ([API.listPrices] reads no query string) but
		// its envelope is still the list envelope: the shape a client sees does
		// not change from endpoint to endpoint (see [writeItems]).
		Responses: map[string]any{
			"200": openapi.Response("The set's prices", d.List(priceDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/price-sets/{id}/prices", openapi.Operation{
		Summary:     "Replaces a set's prices as a whole.",
		RequestBody: d.RequestBody(setPricesRequest{}),
		Responses: map[string]any{
			// 200, NOT 201: the endpoint creates no resource, it replaces the
			// existing set's prices and returns what it wrote in the LIST
			// envelope ([API.setPrices] → [writeItems]). Writing 201 would
			// generate a client method that falls into a "created" branch.
			"200": openapi.Response("The set's new prices", d.List(priceDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-sets/{id}/calculate", openapi.Operation{
		Summary: "Picks the set's valid price in the given context and computes the amount.",
		// The rule context travels in PREFIXED parameters, and that cannot be
		// WRITTEN as a parameter in the schema: OpenAPI names a parameter, not
		// a prefix. An invented "attr_*" entry would generate a client argument
		// carrying exactly that name, which does not work. The honest answer is
		// to describe the three named parameters and say the prefix in the
		// description: a client developer reads it, and the generator produces
		// no lie.
		Description: "The rule context is given in query parameters prefixed `" + paramAttrPrefix +
			"` (for example `" + paramAttrPrefix + "region_id=reg_1`); the prefix is stripped and " +
			"the rest is the field name the rule looks at. An unknown parameter (unprefixed " +
			"and not reserved) is an error.",
		Parameters: []openapi.Parameter{
			queryParameter(paramCurrencyCode, typeString,
				"The currency asked for (ISO 4217); the service's default when absent."),
			queryParameter(paramQuantity, typeInteger,
				"The quantity to price; the service's default when absent."),
			timeParameter(paramAt,
				"The moment to price at (RFC 3339); now when absent. A \"+\" in the "+
					"time zone offset has to be percent-encoded."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The price picked and the amount computed",
				d.Item(calculatedPriceDTO{})),
		},
	})
}

// describePriceLists describes the price list endpoints.
//
// Create and update carry the SAME body ([priceListRequest]); only the method
// tells them apart. PUT is NOT a partial update: a field missing from the body
// is reset (see [API.updatePriceList]).
func describePriceLists(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/price-lists", openapi.Operation{
		Summary:     "Creates a price list.",
		RequestBody: d.RequestBody(priceListRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The price list created", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-lists", openapi.Operation{
		Summary:    "Lists price lists a page at a time.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of price lists", d.List(priceListDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary: "Returns one price list.",
		Responses: map[string]any{
			"200": openapi.Response("The price list", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary:     "Writes EVERY field of a price list; a field missing from the body is reset.",
		RequestBody: d.RequestBody(priceListRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The price list updated", d.Item(priceListDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-lists/{id}", openapi.Operation{
		Summary: "Deletes a price list.",
		Responses: map[string]any{
			"204": emptyResponse("The price list was deleted"),
		},
	})
}

// describePriceRules describes the price rule endpoints.
func describePriceRules(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/prices/{price_id}/rules", openapi.Operation{
		Summary: "Returns a price's validity rules.",
		Responses: map[string]any{
			"200": openapi.Response("The price's rules", d.List(priceRuleDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/prices/{price_id}/rules", openapi.Operation{
		Summary: "Adds a validity rule to a price.",
		// The body is ONE rule, not a list: the handler wraps it in a
		// one-element slice (see [API.createPriceRule]). Describing a list would
		// let a client's second rule disappear in silence.
		RequestBody: d.RequestBody(ruleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The rule added", d.Item(priceRuleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/price-rules/{id}", openapi.Operation{
		Summary: "Deletes a price rule.",
		Responses: map[string]any{
			"204": emptyResponse("The rule was deleted"),
		},
	})
}

// describeStore describes the module's ONE storefront endpoint.
//
// Its body shared the admin one until ADR 0167, because the fields were the same
// and only the prices differed. They are no longer the same: see
// [storePriceSetDTO].
func describeStore(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/price-sets/{id}", openapi.Operation{
		Summary: "Returns a price set with the prices a storefront may show.",
		Description: "Only the prices a storefront may show: no draft or ended list, no price " +
			"that carries a rule. A price from a list names the list's type. The sale price a " +
			"shopper is charged now carries `reduced_since` — when the reduction began — and " +
			"`lowest_prior_amount`, the lowest price that applied in the thirty days before " +
			"it, which is the price a shop announcing a reduction shows. Each is ABSENT when " +
			"the price history cannot state it: a reduction that began before the history " +
			"did, or thirty days the history does not reach. Amounts are in minor units.",
		Responses: map[string]any{
			"200": openapi.Response("The price set and its storefront prices",
				d.Item(storePriceSetDTO{})),
		},
	})
}

// pageParameters are the query parameters [pageParams] reads.
//
// Neither is REQUIRED: when absent the service applies its own default.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger, "The page size; the service's default when absent."),
		queryParameter("offset", typeInteger, "How many records to skip."),
	}
}

// queryParameter describes a parameter read from the query string.
func queryParameter(name, kind, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: kind},
		Description: description,
	}
}

// timeParameter describes a query parameter that carries an RFC 3339 stamp.
//
// The format is written OUT in the schema: a plain "string" would let a client
// generator make the field free text and the caller invent its own format,
// while [timeParam] refuses anything that is not RFC 3339.
func timeParameter(name, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typeString, schemaFormat: formatDateTimeName},
		Description: description,
	}
}

// emptyResponse builds a response definition with NO body.
//
// [openapi.Response] always writes a body schema, and a 204 has NO body (see
// admin.go, the calls that hand corehttp.WriteJSON nil). An empty schema would
// say "something comes back and its shape is unknown", and a client generator
// would produce a method waiting for a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
