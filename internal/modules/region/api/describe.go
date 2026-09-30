package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names used in parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated
// here is not cost but SILENCE: a type name spelled "strig" compiles, the
// document is built, and the mistake surfaces only when a client that reads
// the schema generates the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// referenceDataNote is the description that says the currency and country
// endpoints have NO write surface.
//
// The distinction is written into the schema because the path list alone does
// not show it: a client developer sees GET /admin/v1/currencies, thinks "so
// there is a POST too", and expects an endpoint that does not exist. Keeping
// the note in one place is also deliberate — written three times on three
// endpoints, one would be updated and the others would go stale.
const referenceDataNote = "ARE REFERENCE DATA and READ-ONLY: they are " +
	"seeded by a migration (a copy of ISO 4217 / ISO 3166-1) and cannot be " +
	"created, changed or deleted over HTTP. This module's write surface is " +
	"on the REGION only; the one thing about a country that can change is " +
	"which region it belongs to, and that is managed through the region's " +
	"subresource (POST/DELETE /admin/v1/regions/{id}/countries)."

// Describe writes region's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies described are this package's UNEXPORTED DTOs
// (createRegionRequest, regionDTO …) and the schema is derived from them by
// reflection. Exporting the types to describe them would widen the module's
// surface only to produce a document: an exported type is a contract, and it
// would become buildable from outside. The query parameters belong here too,
// because the code that REALLY reads them ([pageParams], [optionalParam]) is
// in this package; a description kept in another package would drift from it
// in silence. The module's [openapi.Describer] implementation therefore
// delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from TYPES.
// Binding the method to [API] would say the document DEPENDS on a built
// service; yet the document can and must be built even when Register has
// never run.
//
// # The write surface is on the region only
//
// The currency and country endpoints are READS; the reason is in the package
// doc, and it is also written into the schema through [referenceDataNote] so
// the client can see it.
//
// # Known limit: the "required" set of request bodies is WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), which is the right answer for RESPONSE bodies. In
// a request body, "required" means a field the client MUST SEND, and the type
// cannot know that: because [createRegionRequest] carries no omitempty,
// automatic_taxes and tax_rate_bps, whose defaults are acceptable, look
// required too. Field NAMES and TYPES are right, so the schema invents no
// wrong field; it only asks for too much. The right fix is in the CORE (a
// separate "required" policy for request bodies); sprinkling omitempty on the
// tags would move the obligation from the service's validation to a json tag,
// and the two would drift in silence.
func Describe(d *openapi.Doc) {
	describeRegions(d)
	describeRegionCountries(d)
	describeReferenceData(d)
	describeStorefront(d)
}

// describeRegions describes the region's admin endpoints.
func describeRegions(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRegions, openapi.Operation{
		Summary:     "Creates a new region.",
		RequestBody: d.RequestBody(createRegionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created region", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegions, openapi.Operation{
		Summary:    "Lists the regions, paged.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of regions", d.List(regionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegion, openapi.Operation{
		Summary: "Returns a single region by its ID.",
		Responses: map[string]any{
			"200": openapi.Response("The region", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminRegion, openapi.Operation{
		Summary: "Updates the given fields of the region.",
		Description: "The method is PUT, but the semantics are PARTIAL: a field " +
			"absent from the body does not change. Were a full body required, a " +
			"client that forgot to send tax_rate_bps would silently zero the rate.",
		RequestBody: d.RequestBody(updateRegionRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated region", d.Item(regionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegion, openapi.Operation{
		Summary: "Deletes the region.",
		Responses: map[string]any{
			"204": emptyResponse("Region deleted"),
		},
	})
}

// describeRegionCountries describes the endpoints of the region-country link.
//
// They do not write the country ITSELF, only which region it belongs to; they
// do not contradict the reference data being unwritable (see
// [referenceDataNote]).
func describeRegionCountries(d *openapi.Doc) {
	// It returns 201 because what is created is the LINK, and the response
	// body is the country record carrying the link's new state (see
	// [API.addCountry]). Writing 200 would produce the wrong branch in a
	// client generator.
	d.Describe(http.MethodPost, pathAdminRegionCountries, openapi.Operation{
		Summary: "Adds a country to the region.",
		Description: "Returns 409 if the country belongs to another region; a " +
			"country can be linked to only one region at a time.",
		RequestBody: d.RequestBody(addCountryRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The country linked to the region", d.Item(countryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegionCountries, openapi.Operation{
		Summary:     "Lists the countries linked to the region, paged.",
		Description: "Country records " + referenceDataNote,
		Parameters:  pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("The region's countries", d.List(countryDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegionCountry, openapi.Operation{
		Summary: "Removes the country from the region.",
		Description: "The country record is NOT DELETED; only the region link " +
			"is removed and region_id becomes null.",
		Responses: map[string]any{
			"204": emptyResponse("Country removed from the region"),
		},
	})
}

// describeReferenceData describes the currency and country READ endpoints.
func describeReferenceData(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminCountries, openapi.Operation{
		Summary:     "Lists the countries, paged.",
		Description: "Country records " + referenceDataNote,
		// "region_id" is REALLY read ([API.listCountries], [optionalParam]),
		// and an empty string is DISTINCT from "not given": an empty ID is the
		// client's mistake and the service rejects it; it does not silently
		// turn into "no filter".
		Parameters: append(pageParameters(),
			queryParameter("region_id", typeString,
				"Limits the countries to a single region. If it is given but "+
					"left EMPTY, the filter is not dropped; the request is "+
					"rejected with 422.")),
		Responses: map[string]any{
			"200": openapi.Response("A page of countries", d.List(countryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCurrencies, openapi.Operation{
		Summary:     "Lists the currencies, paged.",
		Description: "Currency records " + referenceDataNote,
		Parameters:  pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of currencies", d.List(currencyDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminCurrency, openapi.Operation{
		Summary:     "Returns a single currency by its ISO code.",
		Description: "Currency records " + referenceDataNote,
		// The path parameter is also derived from the pattern; the only reason
		// it is written BY HAND is its description. Only the handler knows
		// that the code is ISO 4217 and is normalized to upper case.
		Parameters: []openapi.Parameter{{
			Name:        "code",
			In:          "path",
			Required:    true,
			Schema:      map[string]any{schemaType: typeString},
			Description: "ISO 4217 currency code (e.g. TRY).",
		}},
		Responses: map[string]any{
			"200": openapi.Response("The currency", d.Item(currencyDTO{})),
		},
	})
}

// describeStorefront describes the region's storefront endpoints.
//
// The storefront body is DIFFERENT from the admin body, and showing them as
// two separate components is deliberate: the record sent to the customer
// carries the currency's symbol and decimal digits but not the tax rate. Were
// a single component used, the client would believe it could read the
// tax_rate_bps field, which the storefront never returns.
func describeStorefront(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathStoreRegions, openapi.Operation{
		Summary: "Lists the regions the storefront can choose from, with their currency and countries.",
		Description: "Amounts are minor-unit INTEGERS; the client learns the " +
			"divisor (10^decimal_digits) from the currency in the same " +
			"response. A client that assumes a fixed 100 shows yen amounts a " +
			"hundred times too small.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Storefront regions", d.List(storeRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreRegion, openapi.Operation{
		Summary: "Returns a single storefront region by its ID.",
		Responses: map[string]any{
			"200": openapi.Response("The storefront region", d.Item(storeRegionDTO{})),
		},
	})
}

// pageParameters returns the limit and offset query parameters.
//
// Both carry the SAME meaning on every paged endpoint ([pageParams]); keeping
// them in one place prevents one description from being updated while the
// others go stale. The slice is built ANEW on every call: a caller may append
// extra parameters to it (see [describeReferenceData]), and appending to a
// shared slice could change the other endpoints' parameter lists as well.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"Page size; if it is not given, the service's default applies."),
		queryParameter("offset", typeInteger, "Number of records to skip."),
	}
}

// queryParameter declares a parameter read from the query string.
//
// None of them is REQUIRED: when one is not given, the handler does not apply
// the filter or proceeds with the service's default (see [pageParams],
// [optionalParam]).
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse builds a response definition with NO body.
//
// [openapi.Response] always writes a body schema, while a 204 HAS no body (see
// the calls that hand corehttp.WriteJSON a nil). Writing an empty schema would
// say "something comes back but its shape is unknown", and the client
// generator would produce a method that expects a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
