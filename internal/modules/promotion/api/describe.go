package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// The JSON Schema names used in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated
// here is not cost but SILENCE: a type name written as "strig" compiles, the
// document is generated, and the mistake surfaces only when a client that
// reads the schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// Describe records promotion's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies being described are this package's UNEXPORTED DTOs
// (campaignRequest, promotionDTO …) and the schema is derived from them by
// reflection. Exporting the types just to be able to describe them would mean
// widening the module's surface merely to generate a document: an exported
// type is a contract and would become constructible from the outside. The
// query parameters live here for the same reason — the code that knows which
// parameter is REALLY read is inside api.go and admin.go; had the description
// been moved to another package, the two would silently drift apart. That is
// why the module's [openapi.Describer] implementation delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the
// TYPES. Binding the method to [API] would say that the document DEPENDS on a
// service having been constructed; yet the document can and must be generated
// even when [API.Routes] has never been called.
//
// # The admin body and the customer body are described SEPARATELY
//
// GET /store/v1/promotions/{code} does NOT return the admin body
// ([promotionDTO]) but a narrow coupon ([storeCouponDTO]). Describing the two
// with one component would mean the schema promises the customer a status,
// counters and a campaign budget; none of these is sent from that endpoint.
//
// # Known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]) and that is the right answer for RESPONSE bodies.
// In a request body, however, "required" means a field the client MUST SEND,
// and the type cannot know that: because this package's request DTOs carry no
// omitempty, all of them look required — for example POST
// /admin/v1/campaigns also asks for budget_currency_code, which is left empty
// on a campaign without a budget. The field NAMES and TYPES are correct, so
// the schema does not invent a wrong field; it merely asks for too much. The
// right fix is IN THE CORE (a separate "required" policy for request bodies);
// sprinkling omitempty over the tags would move the obligation from the
// service's validation to the json tag, and the two would silently drift
// apart.
func Describe(d *openapi.Doc) {
	describeCampaigns(d)
	describePromotions(d)
	describeMethodsAndRules(d)
	describeRedemptions(d)
	describeTrial(d)

	d.Describe(http.MethodPost, "/admin/v1/promotions/compute", openapi.Operation{
		Summary: "Computes the discounts for the given cart context and says what " +
			"was NOT APPLIED.",
		Description: "HAS NO SIDE EFFECTS: no counter and no budget changes, so " +
			"the response is 200. The amounts are EXACTLY the amounts the cart flow " +
			"receives; a request tried on the admin screen gives the same result in " +
			"the cart. \n\n" +
			"\"skipped\" returns the promotions that were EVALUATED and eliminated, " +
			"with their reason, and it is the ONLY field in which this body departs " +
			"from the interop schema: the cart flow would not know what to do with a " +
			"reason, and because the storefront reads the cart's totals, a reason " +
			"passed through there would end up reaching the customer whose code was " +
			"rejected (ADR 0110). " +
			"The population is every automatic promotion plus the promotions of the " +
			"codes SENT — including the ones not yet published, since \"you have not " +
			"activated it\" is the most common reason a code does nothing. A coupon " +
			"nobody typed is not in the population: the answer does not grow with the " +
			"size of the catalog.",
		RequestBody: d.RequestBody(computeRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Discount computation", d.Item(computeResultDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/promotions/{code}", openapi.Operation{
		Summary: "Validates a coupon code and returns the discount's type, target " +
			"and value.",
		Description: "The customer body is NARROW: the promotion's status, usage " +
			"counter, campaign and rule conditions are NOT HERE. If the code is " +
			"invalid, the reason is not given either — a draft, an inactive, an " +
			"expired, a budget-exhausted and a nonexistent code all return the same " +
			"404.",
		Responses: map[string]any{
			"200": openapi.Response("Coupon", d.Item(storeCouponDTO{})),
		},
	})
}

// describeCampaigns describes the campaign endpoints.
func describeCampaigns(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/campaigns", openapi.Operation{
		Summary:     "Creates a new campaign.",
		RequestBody: d.RequestBody(campaignRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Created campaign", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/campaigns", openapi.Operation{
		Summary:    "Lists the campaigns page by page.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Campaigns", d.List(campaignDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Returns the campaign by its identifier.",
		Responses: map[string]any{
			"200": openapi.Response("Campaign", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Replaces the campaign's definition.",
		// The body is the SAME type as on create: a replace takes the full body,
		// and two separate types would describe two different shapes of the same
		// record.
		RequestBody: d.RequestBody(campaignRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Updated campaign", d.Item(campaignDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/campaigns/{id}", openapi.Operation{
		Summary: "Soft deletes the campaign.",
		Responses: map[string]any{
			"204": emptyResponse("Campaign deleted"),
		},
	})
}

// describePromotions describes the promotion endpoints.
func describePromotions(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/promotions", openapi.Operation{
		Summary:     "Creates a new promotion.",
		RequestBody: d.RequestBody(promotionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Created promotion", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/promotions", openapi.Operation{
		Summary: "Lists the promotions page by page.",
		Parameters: append(pageParameters(),
			queryParameter("status", typeString,
				"Limits the list to a single publication status (draft | active | inactive)."),
			queryParameter("campaign_id", typeString,
				"Limits the list to the promotions of a single campaign.")),
		Responses: map[string]any{
			"200": openapi.Response("Promotions", d.List(promotionDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary: "Returns the promotion by its identifier.",
		Responses: map[string]any{
			"200": openapi.Response("Promotion", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary:     "Replaces the promotion's definition.",
		RequestBody: d.RequestBody(promotionRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Updated promotion", d.Item(promotionDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/promotions/{id}", openapi.Operation{
		Summary: "Soft deletes the promotion.",
		Responses: map[string]any{
			"204": emptyResponse("Promotion deleted"),
		},
	})
}

// describeMethodsAndRules describes the application method and rule
// endpoints.
func describeMethodsAndRules(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/admin/v1/promotions/{id}/application-method",
		openapi.Operation{
			Summary: "Writes the promotion's application method.",
			Description: "It is a replace: if the promotion already has a method, " +
				"it is overwritten, so the response is 200, not 201.",
			RequestBody: d.RequestBody(applicationMethodRequest{}),
			Responses: map[string]any{
				"200": openapi.Response("Written application method",
					d.Item(applicationMethodDTO{})),
			},
		})

	d.Describe(http.MethodDelete, "/admin/v1/promotions/{id}/application-method",
		openapi.Operation{
			Summary: "Soft deletes the promotion's application method.",
			Responses: map[string]any{
				"204": emptyResponse("Application method deleted"),
			},
		})

	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}/rules", openapi.Operation{
		Summary: "Lists the promotion's rules.",
		// There are NO paging parameters: the list is written with [writeItems]
		// and the handler never reads the query string. Describing them would
		// promise the client a paging that does not work.
		Responses: map[string]any{
			"200": openapi.Response("The promotion's rules", d.List(promotionRuleDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/rules", openapi.Operation{
		Summary:     "Adds a rule to the promotion.",
		RequestBody: d.RequestBody(promotionRuleRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Added rule", d.Item(promotionRuleDTO{})),
			"422": openapi.ErrorResponse("The body is invalid. A rule whose attribute begins with `" +
				models.ReservedAttributePrefix + "` is refused with `" + service.CodeRuleAttributeReserved +
				"`: a cart's metadata reaches no rule."),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/promotion-rules/{id}", openapi.Operation{
		Summary: "Soft deletes the promotion rule.",
		Responses: map[string]any{
			"204": emptyResponse("Rule deleted"),
		},
	})
}

// describeRedemptions describes the redemption ledger endpoints.
func describeRedemptions(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/admin/v1/promotions/{id}/redemptions", openapi.Operation{
		Summary:    "Lists the promotion's redemption ledger page by page.",
		Parameters: pageParameters(),
		Responses: map[string]any{
			"200": openapi.Response("Redemption records", d.List(redemptionDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/redeem", openapi.Operation{
		Summary: "Redeems the promotion for a business record reference.",
		Description: "IDEMPOTENT: a second request with the same reference does not " +
			"increment the counter and returns the existing record. The response is " +
			"therefore 200, not 201 — the request does not always create a NEW " +
			"record.",
		RequestBody: d.RequestBody(redeemRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Redemption record", d.Item(redemptionDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/promotions/{id}/release", openapi.Operation{
		Summary: "Releases a redemption and takes the counters back.",
		Description: "IDEMPOTENT: a second call does not fail and the counters are " +
			"not decremented a second time. The \"released\" field in the response " +
			"reports whether anything was taken back IN THIS REQUEST; false is NOT a " +
			"failure.",
		RequestBody: d.RequestBody(releaseRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Release result", d.Item(releaseResultDTO{})),
		},
	})
}

// pageParameters produces the limit/offset query parameters.
//
// It is used only on the endpoints that call [pageParams]; lists that are not
// paged (the ones written with [writeItems]) never read the query string.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"The page size; if not given, the service's default is applied."),
		queryParameter("offset", typeInteger, "The number of records to skip."),
	}
}

// queryParameter defines a parameter read from the query string.
//
// None of them is required: when they are not given the handler continues
// with the default (see [intParam], [stringParam]).
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse produces a response definition with NO BODY.
//
// [openapi.Response] always writes a body schema; a 204 however HAS no body
// (see admin.go, the calls that pass nil to corehttp.WriteJSON). Writing an
// empty schema would mean "something is returned but its shape is unknown",
// and the client generator would produce a method expecting a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
