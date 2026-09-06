package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The JSON Schema names used in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// documentation is generated, and it only surfaces when a client reading the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	schemaItems = "items"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeArray   = "array"
)

// Describe records the fulfillment endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (createProfileRequest, fulfillmentDTO …) and the schema is derived from them
// by reflection. Exporting the types just to be able to describe them would
// mean widening the module's surface merely to generate documentation: an
// exported type is a contract and would become constructible from the outside.
// The query parameters live here for the same reason — the code that knows
// which parameter is REALLY read is inside api.go and handlers.go; had the
// description been moved to another package, the two would silently drift
// apart. That is why the module's [openapi.Describer] implementation delegates
// here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the TYPES.
// Binding the method to [Handler] would say that the documentation DEPENDS on a
// service having been constructed; yet the documentation can and must be
// generated even when [Handler.Routes] has never been called.
//
// # The endpoints NOT described: shipping option CRUD
//
// POST /admin/v1/shipping-options, GET /admin/v1/shipping-options, GET and
// PATCH /admin/v1/shipping-options/{id} are NOT described with their bodies.
// The reason is not in this module but IN THE DOCUMENT'S NAMESPACE: the
// component name is derived from the Go type name (the first letter is
// capitalized, the "DTO" suffix is dropped) and [optionDTO] asks for the name
// "Option" — the same name the product module's models.Option asks for. When
// two DIFFERENT types ask for the same component name, document generation
// returns an error, so describing these four endpoints would bring down the
// WHOLE of /openapi.json.
//
// Renaming the type is NOT a decision this package can take on its own: the
// component name is the published contract and, on whichever side the fix is
// made, it breaks the other module's generated clients. The endpoints were
// therefore left undescribed — they appear in the document with their path,
// method and security, only without their bodies.
//
// DELETE /admin/v1/shipping-options/{id} is described though: it returns 204
// and, having no body, never touches [optionDTO].
//
// # Known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]) and that is the right answer for RESPONSE bodies. In
// a request body, however, "required" means a field the client MUST SEND, and
// the type cannot know that: because this package's request DTOs carry no
// omitempty, all of them look required — for example POST
// /admin/v1/shipping-profiles also asks for metadata, which may be left empty.
// The field NAMES and TYPES are correct, so the schema does not invent a wrong
// field; it merely asks for too much. The right fix is IN THE CORE (a separate
// "required" policy for request bodies); sprinkling omitempty over the tags
// would move the obligation from the service's validation to the json tag, and
// the two would silently drift apart.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminProviders, openapi.Operation{
		Summary: "Lists the identifiers of the registered shipping providers.",
		Responses: map[string]any{
			// The record is not a DTO but a plain string (see
			// [Handler.listProviders]); the envelope is still the list
			// envelope.
			"200": openapi.Response("Provider identifiers", d.List("")),
		},
	})

	describeProfiles(d)
	describeLocationPolicies(d)
	describeOptions(d)
	describeEligibility(d)
	describeFulfillments(d)
}

// describeProfiles describes the shipping profile endpoints.
func describeProfiles(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminProfiles, openapi.Operation{
		Summary:     "Creates a new shipping profile.",
		RequestBody: d.RequestBody(createProfileRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created profile", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminProfiles, openapi.Operation{
		Summary: "Lists the shipping profiles page by page.",
		Parameters: append(pageParameters(),
			queryParameter("type", typeString,
				"Restricts the list to a single profile type.")),
		Responses: map[string]any{
			"200": openapi.Response("Shipping profiles", d.List(profileDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminProfile, openapi.Operation{
		Summary: "Returns the shipping profile by its identifier.",
		Responses: map[string]any{
			"200": openapi.Response("The shipping profile", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodPatch, pathAdminProfile, openapi.Operation{
		Summary:     "Updates the given fields of the shipping profile.",
		RequestBody: d.RequestBody(updateProfileRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated profile", d.Item(profileDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminProfile, openapi.Operation{
		Summary: "Soft deletes the shipping profile.",
		Responses: map[string]any{
			"204": emptyResponse("The profile was deleted"),
		},
	})
}

// describeLocationPolicies describes the location selection policy endpoints.
func describeLocationPolicies(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminLocations, openapi.Operation{
		Summary:    "Lists the location shipping policies in priority order.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Location shipping policies", d.List(locationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminLocation, openapi.Operation{
		Summary: "Returns the shipping policy of a location.",
		Responses: map[string]any{
			"200": openapi.Response("The location shipping policy", d.Item(locationDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminLocation, openapi.Operation{
		Summary:     "Writes or overwrites the shipping policy of a location.",
		RequestBody: d.RequestBody(setLocationRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The written policy", d.Item(locationDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminLocation, openapi.Operation{
		Summary: "Deletes the shipping policy of a location and returns the location to the default.",
		Responses: map[string]any{
			"204": emptyResponse("The policy was deleted"),
		},
	})
}

// describeOptions describes those shipping option endpoints that can be
// described.
//
// The four endpoints carrying the option's OWN body are deliberately left out;
// the rationale is in the [Describe] godoc. What remains here are the ones that
// never touch the option record: deletion (which has no body) and the rules
// (which have their own DTO).
func describeOptions(d *openapi.Doc) {
	d.Describe(http.MethodDelete, pathAdminOption, openapi.Operation{
		Summary: "Soft deletes the shipping option.",
		Responses: map[string]any{
			"204": emptyResponse("The option was deleted"),
		},
	})

	d.Describe(http.MethodPost, pathAdminOptionRules, openapi.Operation{
		Summary:     "Adds an eligibility rule to the shipping option.",
		RequestBody: d.RequestBody(createRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The added rule", d.Item(ruleDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminOptionRules, openapi.Operation{
		Summary: "Lists the eligibility rules of the shipping option.",
		// There is NO paging parameter: the list is written with [writeList]
		// and the handler never reads the query string. Writing one would be
		// promising the client a paging that does not work.
		Responses: map[string]any{
			"200": openapi.Response("The rules of the option", d.List(ruleDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminOptionRule, openapi.Operation{
		Summary: "Soft deletes the eligibility rule.",
		Responses: map[string]any{
			"204": emptyResponse("The rule was deleted"),
		},
	})
}

// describeEligibility describes the two eligibility listings.
//
// The two describe the SAME record but with different types ([quotedOptionDTO]
// and [storeOptionDTO]); what the distinction is is written in the description
// of both.
func describeEligibility(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminEligible, openapi.Operation{
		Summary: "Lists the shipping options eligible for the given cart context " +
			"in the admin representation.",
		Description: "The admin representation includes options flagged admin_only TOO " +
			"and carries the provider_id, shipping_profile_id, is_return and " +
			"admin_only fields on every record. The SAME option either does not " +
			"appear at all on the storefront endpoint " +
			"(GET /store/v1/shipping-options) or appears without these fields: the " +
			"two endpoints read the same catalog, their representations differ. " +
			"Cart facts are considered TRUSTED here, so options with a rule " +
			"depending on subtotal, item_count and total_weight are listed as well.",
		Parameters: eligibilityParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Eligible options (admin representation)",
				d.List(quotedOptionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreOptions, openapi.Operation{
		Summary: "Lists the shipping options eligible for the given cart context " +
			"in the storefront representation.",
		Description: "The storefront representation is NARROWER than the admin one and " +
			"the same record looks different on the two endpoints: options flagged " +
			"admin_only are NEVER listed here, and on the ones that are listed the " +
			"provider_id, shipping_profile_id, is_return and admin_only fields are " +
			"not written (see GET /admin/v1/shipping-options/eligible). The cart " +
			"facts (subtotal, item_count, total_weight) are the client's CLAIM: " +
			"options with a rule depending on these three facts are removed from " +
			"the list entirely, and the rate of \"calculated\" options is only a " +
			"PRESENTATION — the real rate is determined at the payment step with " +
			"the cart's real facts.",
		Parameters: eligibilityParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Eligible options (storefront representation)",
				d.List(storeOptionDTO{})),
		},
	})
}

// describeFulfillments describes the fulfillment endpoints.
func describeFulfillments(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminFulfillments, openapi.Operation{
		Summary:     "Opens a new fulfillment at the provider.",
		RequestBody: d.RequestBody(createFulfillmentRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The opened fulfillment", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminFulfillments, openapi.Operation{
		Summary: "Lists the fulfillments page by page.",
		Parameters: append(pageParameters(),
			queryParameter("reference", typeString,
				"Restricts the list to a single business record reference."),
			queryParameter("status", typeString,
				"Restricts the list to a single fulfillment status.")),
		Responses: map[string]any{
			"200": openapi.Response("Fulfillments", d.List(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminFulfillment, openapi.Operation{
		Summary: "Returns the fulfillment together with its items.",
		Responses: map[string]any{
			"200": openapi.Response("The fulfillment", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminCancel, openapi.Operation{
		Summary: "Cancels the fulfillment and returns its current state.",
		Description: "It is IDEMPOTENT: a second call also returns 200 and the same " +
			"record. It TAKES no body; the fulfillment to cancel is chosen from the path.",
		Responses: map[string]any{
			"200": openapi.Response("The canceled fulfillment", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminShip, openapi.Operation{
		Summary: "Records that the carrier collected the fulfillment.",
		Description: "It is IDEMPOTENT. If the shipment has already been delivered or " +
			"returned, the report is ACCEPTED rather than refused — a carrier's events " +
			"arrive out of order — and only the tracking information is written; the " +
			"status is not moved backwards and shipped_at stays empty, because the only " +
			"moment available would be later than the delivery already recorded. A " +
			"tracking number that CONTRADICTS a stored one is still a 409.",
		// The body is OPTIONAL: some carriers provide the tracking number later
		// and the handler does not treat an empty body as an error (see
		// [decodeOptionalBody]). Marking it required would mean the client
		// generator never makes a call without a body possible.
		RequestBody: optionalBody(d, shipRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The shipped fulfillment", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminDeliver, openapi.Operation{
		Summary: "Marks the fulfillment as delivered.",
		Description: "It is IDEMPOTENT. A delivery may be reported on a fulfillment that " +
			"was never marked as collected; shipped_at then stays empty, which says that " +
			"nobody has reported the dispatch rather than that it did not happen.",
		Responses: map[string]any{
			"200": openapi.Response("The delivered fulfillment", d.Item(fulfillmentDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminReturned, openapi.Operation{
		Summary: "Records that the parcel came back to the sender undelivered.",
		Description: "The carriers' \"iade\": the parcel could not be delivered and came " +
			"back under the ORIGINAL waybill. It is IDEMPOTENT and TAKES no body. It is " +
			"not the same thing as a customer sending goods back after receiving them — " +
			"that is a new fulfillment on a shipping option with is_return set. Only a " +
			"shipped fulfillment can come back; anything else is a 409.",
		Responses: map[string]any{
			"200": openapi.Response("The returned fulfillment", d.Item(fulfillmentDTO{})),
		},
	})
}

// eligibilityParameters are the query parameters of the eligibility listing.
//
// The list is what [parseEligibilityQuery] READS and it is the same on both
// surfaces. "include_admin_only" and "trusted_facts" are DELIBERATELY ABSENT:
// both are a TRUST decision and their value is fixed according to which surface
// the handler belongs to. Writing them into the schema would imply to a client
// coming from the storefront that a single parameter could open the admin-only
// options.
//
// The free rule context ([service.ListOptionsInput.Attributes]) is absent as
// well; none of the HTTP endpoints reads it.
func eligibilityParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("region_id", typeString,
			"Restricts the options to the cart's region."),
		queryParameter("currency_code", typeString,
			"The currency the rate will be computed in."),
		queryParameter("country_code", typeString,
			"The delivery country; country-bound rules are evaluated with it."),
		{
			Name: "shipping_profile_id",
			In:   "query",
			// REPEATABLE: a cart may contain products bound to several profiles
			// and all of them are asked at once (see the query["…"] read).
			Schema: map[string]any{
				schemaType:  typeArray,
				schemaItems: map[string]any{schemaType: typeString},
			},
			Description: "The shipping profiles of the products in the cart; may be given more than once.",
		},
		queryParameter("subtotal", typeInteger,
			"The subtotal of the cart (minor unit)."),
		queryParameter("item_count", typeInteger, "The number of items in the cart."),
		queryParameter("total_weight", typeInteger, "The total weight of the cart."),
		queryParameter("is_return", typeBoolean,
			"If true, return options are listed."),
	}
}

// pageParameters produces the limit/offset query parameters.
//
// It is used only on the endpoints that call [parsePage]; lists that are not
// paged (the ones written with [writeList]) never read the query string.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"The page size; if not given, the service's default is applied."),
		queryParameter("offset", typeInteger, "The number of records to skip."),
	}
}

// queryParameter defines a parameter read from the query string.
//
// None of them is required: when they are not given the handler continues with
// the default (see [parseInt64Param], [parseBoolParam]).
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// optionalBody produces a request definition whose body is NOT REQUIRED.
//
// [openapi.Doc.RequestBody] always marks the body required and that is right
// for almost every write endpoint. On the ship notification it is not: the
// handler accepts an empty body and shipping is written without tracking
// information as well. Marking it required would mean the client generator
// never produces a call without a body.
func optionalBody(d *openapi.Doc, v any) map[string]any {
	body := d.RequestBody(v)
	body["required"] = false

	return body
}

// emptyResponse produces a response definition with NO BODY.
//
// [openapi.Response] always writes a body schema; a 204 however HAS no body
// (see the calls that pass nil to corehttp.WriteJSON). Writing an empty schema
// would mean "something is returned but its shape is unknown", and the client
// generator would produce a method expecting a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
