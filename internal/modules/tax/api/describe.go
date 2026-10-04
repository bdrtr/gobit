package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names that parameter schemas use.
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

// Describe writes tax's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies described are this package's UNEXPORTED DTOs (createTaxRateRequest,
// taxRateDTO …) and the schema is derived from them by reflection. Exporting
// the types in order to describe them would widen the module's surface only
// to produce a document: an exported type is a contract, and it would become
// constructible from outside. The query parameters stay here for the same
// reason — the code that knows which parameter is REALLY read is in admin.go;
// had the description moved to another package, the two would drift apart in
// silence. The module's [openapi.Describer] implementation therefore
// delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from TYPES.
// Binding the method to [API] would say the document DOES depend on a built
// service; yet the document can and must be built even when [API.Routes] has
// never been called.
//
// # Why only /admin/v1
//
// The module has NO storefront surface (see the package doc): tax is not
// exposed to the customer directly; it travels through the cart's computed
// tax line. Inventing an endpoint that is not in the doc would make the client
// generator write a method that can never be called.
//
// # Known limit: the "required" set of request bodies is WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]), which is the right answer for RESPONSE bodies. In a
// request body "required" means a field the client MUST SEND, which a type
// cannot know: this package's create DTOs carry no omitempty, so every field
// looks required — POST /admin/v1/tax-regions, for instance, also asks for the
// province_code and parent_id that are left empty when a country root is
// created. Field NAMES and TYPES are right, so the schema invents no field; it
// only asks for too much. The update body ([updateTaxRateRequest]) is outside
// this limit: its fields are pointers and, by accepting null in the schema,
// they describe the "a field not given does not change" behavior correctly.
// The right fix is in the CORE (a separate "required" policy for request
// bodies); sprinkling omitempty on the tags would move the obligation from the
// service's validation to a json tag, and the two would drift apart in
// silence.
func Describe(d *openapi.Doc) {
	describeRegions(d)
	describeRates(d)
	describeRateTrial(d)
	describeRules(d)
	describeClasses(d)
}

// describeRegions describes the tax region endpoints.
func describeRegions(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRegions, openapi.Operation{
		Summary: "Creates a new tax region.",
		Description: "When province_code is left empty a COUNTRY ROOT is created; when it " +
			"is given, parent_id is required too.",
		RequestBody: d.RequestBody(createTaxRegionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The tax region created", d.Item(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegions, openapi.Operation{
		Summary: "Lists the tax regions, paged.",
		Parameters: append(pageParameters(),
			queryParameter("country_code", typeString, false,
				"Limits the list to a single country; when absent, every region is returned.")),
		Responses: map[string]any{
			"200": openapi.Response("The tax regions", d.List(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegion, openapi.Operation{
		Summary: "Returns a tax region by its ID.",
		Responses: map[string]any{
			"200": openapi.Response("The tax region", d.Item(taxRegionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRegion, openapi.Operation{
		Summary: "Soft-deletes a tax region TOGETHER WITH its tree.",
		Description: "The delete also covers the child regions, their rates and the " +
			"rules of those rates. The response HAS NO body: returning a listing of " +
			"the deleted tree would build, on every call, a list the client does " +
			"not need.",
		Responses: map[string]any{
			"204": emptyResponse("The tax region and its tree were deleted"),
		},
	})

	d.Describe(http.MethodGet, pathAdminRegionRates, openapi.Operation{
		Summary: "Lists the rates of a tax region.",
		// There are NO paging parameters: the list is written with [writeAll]
		// and the handler never reads the query string. Writing them would
		// promise the client paging that does not work.
		Responses: map[string]any{
			"200": openapi.Response("The region's tax rates", d.List(taxRateDTO{})),
		},
	})
}

// describeRates describes the tax rate endpoints.
func describeRates(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRates, openapi.Operation{
		Summary: "Creates a new tax rate.",
		Description: "The rate's region is carried in the BODY (tax_region_id); a rate " +
			"is WRITTEN only through this endpoint, and the endpoint under the region " +
			"is read-only." +
			"\n\n" +
			"When stacks_on_id is given, the rate sits ON TOP OF another rate in the " +
			"same region: the base rate is applied to the line first, then this rate, " +
			"and when compound is true this rate's base also includes the tax of the " +
			"rates beneath it. A rate on top is never SELECTED, so it cannot be the " +
			"default and cannot carry rules; the BASE sets the stack's scope. A stack " +
			"cannot be built in a region whose prices are written tax INCLUSIVE " +
			"(ADR 0086), and the stack's rates together cannot exceed the line.",
		RequestBody: d.RequestBody(createTaxRateRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The tax rate created", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRates, openapi.Operation{
		Summary: "Lists the tax rates of a region.",
		// "tax_region_id" is REQUIRED and the handler returns 422 when it is
		// missing; limit/offset, on the other hand, are NEVER READ (the list is
		// written with [writeAll]). Writing them would promise the client
		// paging that does not work.
		Parameters: []openapi.Parameter{
			queryParameter("tax_region_id", typeString, true,
				"The tax region whose rates are read; required."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The tax rates", d.List(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRate, openapi.Operation{
		Summary: "Returns a tax rate by its ID.",
		Responses: map[string]any{
			"200": openapi.Response("The tax rate", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminRate, openapi.Operation{
		Summary: "Updates the given fields of a tax rate.",
		Description: "The method is PUT but the semantics are PARTIAL: a field not given " +
			"in the body does not change. To REMOVE the code, send an empty string in " +
			"the code field.",
		RequestBody: d.RequestBody(updateTaxRateRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The tax rate updated", d.Item(taxRateDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRate, openapi.Operation{
		Summary: "Soft-deletes a tax rate.",
		Responses: map[string]any{
			"204": emptyResponse("The tax rate was deleted"),
		},
	})
}

// describeRules describes the tax rate rule endpoints.
func describeRules(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminRateRules, openapi.Operation{
		Summary: "Adds a rule to a tax rate.",
		Description: "The rate ID is taken from the PATH; carried a second time in the " +
			"body, the path and the body could contradict each other. reference_id " +
			"is ANOTHER module's ID and this module does not verify that it exists.",
		RequestBody: d.RequestBody(createTaxRateRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The rule added", d.Item(taxRateRuleDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminRateRules, openapi.Operation{
		Summary: "Lists the rules of a tax rate.",
		Responses: map[string]any{
			"200": openapi.Response("The rate's rules", d.List(taxRateRuleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminRateRule, openapi.Operation{
		Summary: "Soft-deletes a tax rate rule.",
		Responses: map[string]any{
			"204": emptyResponse("The rule was deleted"),
		},
	})
}

// pageParameters builds the limit/offset query parameters.
//
// It is used only on endpoints that call [pageParams]; in this module the only
// such endpoint is the region listing. Rate and rule lists are not paged.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger, false,
			"The page size; when absent, the service's default applies."),
		queryParameter("offset", typeInteger, false, "How many records to skip."),
	}
}

// queryParameter declares a parameter read from the query string.
//
// The required flag is a parameter ON PURPOSE: this module has a single
// required query parameter (tax_region_id of GET /admin/v1/tax-rates), and
// showing it as optional would mean the client generator producing a method
// it takes to be callable but that always returns 422.
func queryParameter(name, typ string, required bool, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    required,
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse builds a response definition with NO body.
//
// [openapi.Response] always writes a body schema, and a 204 HAS NO body (see
// admin.go, the calls that hand corehttp.WriteJSON nil). Writing an empty
// schema would say "something comes back but its shape is unknown", and the
// client generator would produce a method that expects a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}

// describeClasses describes the tax class endpoints.
func describeClasses(d *openapi.Doc) {
	const meaning = "A tax class is a set of products the merchant taxes THE SAME WAY " +
		"(books, food, electronics). A rule is written to a class and applies to every " +
		"product put in the class; a rule written to a product BEATS one written to the " +
		"class, and the class beats the product type."

	d.Describe(http.MethodPost, pathAdminClasses, openapi.Operation{
		Summary:     "Creates a new tax class.",
		Description: meaning + " The name is required and unique among LIVE classes.",
		RequestBody: d.RequestBody(createTaxClassRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The class created", d.Item(taxClassDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminClasses, openapi.Operation{
		Summary: "Lists the tax classes in name order.",
		Description: "Not paged: a shop's tax classes number in the tens — they are the " +
			"merchant's own vocabulary, not its catalog.",
		Responses: map[string]any{
			"200": openapi.Response("The tax classes", d.List(taxClassDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminClass, openapi.Operation{
		Summary: "Returns a tax class by its ID.",
		Responses: map[string]any{
			"200": openapi.Response("The tax class", d.Item(taxClassDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminClass, openapi.Operation{
		Summary: "Soft-deletes a tax class that holds no products.",
		Description: "A class that holds products is REFUSED with 409: had it been deleted, " +
			"those products would keep matching a rule whose class nobody could name.",
		Responses: map[string]any{
			"204": emptyResponse("The class was deleted"),
		},
	})

	d.Describe(http.MethodGet, pathAdminClassProducts, openapi.Operation{
		Summary: "Lists the products in a class.",
		Responses: map[string]any{
			"200": openapi.Response("The class's products", d.List(taxClassMemberDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminClassProducts, openapi.Operation{
		Summary: "Puts a product in a class.",
		Description: "When the product is in ANOTHER class it is MOVED, not refused: " +
			"reclassifying is an ordinary operation, and refusing would force the operator " +
			"to delete the old membership first, leaving a window in which the product is " +
			"in no class. product_id is ANOTHER module's ID and this module does not " +
			"verify that it exists.",
		RequestBody: d.RequestBody(taxClassMemberRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The membership", d.Item(taxClassMemberDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminClassProduct, openapi.Operation{
		Summary: "Removes a product from its class.",
		Description: "The class in the path is NOT COMPARED with the product's class: since " +
			"a product is in at most one class, the two statements cannot have different " +
			"outcomes.",
		Responses: map[string]any{
			"204": emptyResponse("The product was removed from the class"),
		},
	})
}
