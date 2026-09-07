package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// minorUnitNote is the unit warning added to every description that carries an
// amount.
//
// The schema says "integer" and the type alone does NOT say the unit: a client
// developer who sees they cannot send 100.50 may try rounding it or dropping the
// kurus. Money never passes through a floating point value anywhere in this
// framework, so the right answer is 10050, and this note is the only thing that
// says so.
const minorUnitNote = " Amounts are MINOR UNIT integers (kurus/cent): send 10050 for 100.50 TRY; " +
	"a decimal value such as 100.50 is invalid."

// describeOptionRecords records the four endpoints carrying the option RECORD.
//
// # They were undescribed until 2026-09-07, and not by neglect
//
// [optionDTO] wanted the component name "Option", which the product module's
// models.Option already held. Two types wanting one name does not degrade one
// endpoint: the document FAILS to build and /openapi.json returns 500 for every
// module, so leaving these four bodiless was the cheaper mistake. The note this
// package carried said the fix belonged in the core, and it did — ADR 0036
// prefixes a component name with the module being described, so this is now
// "FulfillmentOption" while the product module keeps "ProductOption". No type
// here was renamed.
//
// DELETE on a single option was described all along, because it answers 204 and
// a body-less response never touches the colliding type.
func describeOptionRecords(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminOptions, openapi.Operation{
		Summary: "Creates a shipping option.",
		Description: "An option is the CHOICE a shopper sees at checkout, and it is not on " +
			"its own what decides whether they see it — that is the option's rules and the " +
			"eligibility endpoint that reads them." +
			"\n\n" +
			"\"price_type\" decides what \"amount\" means. On a \"flat\" option the amount " +
			"is the price. On a \"calculated\" option the provider is asked at checkout and " +
			"the amount here is not used, which is why it may be left at zero without the " +
			"option being free." + minorUnitNote +
			"\n\n" +
			"\"admin_only\" keeps the option off the storefront while leaving it usable from " +
			"the admin surface — a courier an operator can pick for a phone order but a " +
			"shopper cannot choose. \"is_return\" marks an option that ships the OTHER WAY.",
		RequestBody: d.RequestBody(createOptionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created option", d.Item(optionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminOptions, openapi.Operation{
		Summary: "Lists shipping options.",
		Description: "The four filters below are exactly the ones the handler reads. A " +
			"parameter written into the schema that the server ignores is worse than a " +
			"missing one: the generated client puts an argument on the method, the caller " +
			"fills it in, and the filtering silently does not happen." +
			"\n\n" +
			"This listing does NOT apply the eligibility rules — it is the catalog of " +
			"options, not the set a given cart may use. For that, see the eligibility " +
			"endpoint; asking this one and filtering in the client would reimplement the " +
			"rules on the wrong side of the wire." + minorUnitNote,
		Parameters: []openapi.Parameter{
			queryParameter("region_id", typeString,
				"Limits the listing to the options of one region."),
			queryParameter("shipping_profile_id", typeString,
				"Limits the listing to the options of one shipping profile."),
			queryParameter("provider_id", typeString,
				"Limits the listing to one fulfillment provider."),
			queryParameter("price_type", typeString,
				"\"flat\" or \"calculated\"."),
			queryParameter("limit", typeInteger,
				"Page size; the service's default applies when it is not given."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of shipping options", d.List(optionDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminOption, openapi.Operation{
		Summary: "Reads one shipping option.",
		Description: "The option as the catalog holds it." + minorUnitNote +
			"\n\n" +
			"Its RULES are a separate listing under this path, because a rule is a child " +
			"record with its own endpoints rather than a field of the option.",
		Responses: map[string]any{
			"200": openapi.Response("The shipping option", d.Item(optionDTO{})),
		},
	})

	d.Describe(http.MethodPatch, pathAdminOption, openapi.Operation{
		Summary: "Updates the given fields of a shipping option.",
		Description: "Every field is a POINTER and the distinction is the point: a field " +
			"left out is not touched, and a field sent as null or empty is set to that. An " +
			"update body that could not tell the two apart would erase what the caller did " +
			"not mention." +
			"\n\n" +
			"The provider and the shipping profile are NOT here. Both decide which orders " +
			"an option can even belong to, and moving an option between them after carts " +
			"have chosen it would change what those carts agreed to; creating a new option " +
			"and retiring the old one says the same thing without rewriting history." +
			minorUnitNote,
		RequestBody: d.RequestBody(updateOptionRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated option", d.Item(optionDTO{})),
		},
	})
}
