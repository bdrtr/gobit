package api

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// attributesParameter describes the repeated attribute filter (ADR 0219).
//
// It is built by [queryParameter] and then made a list, as variant_id is, so the
// query-parameter audit finds it by name.
func attributesParameter() openapi.Parameter {
	parameter := queryParameter("attribute", "array", fmt.Sprintf(
		"Restricts the products by typed attributes; repeat the parameter for each, "+
			"at most %d. Each is \"<handle>:<value>\": option handles separated by commas "+
			"for a select attribute (material:cotton,wool keeps a product holding any of "+
			"them), true or false for a boolean one (waterproof:true), and <min>..<max> for "+
			"a number, each bound inclusive and either left out (width:..120). A product is "+
			"kept when it holds a matching value of EVERY attribute named. The handles come "+
			"from GET /store/v1/product-attributes; a handle, an option or a value the "+
			"catalog does not have is refused with 422 rather than answered with an empty "+
			"page.", models.MaxAttributeFilters))
	parameter.Schema["items"] = map[string]any{schemaType: typeString}
	parameter.Schema["maxItems"] = models.MaxAttributeFilters

	return parameter
}

// describeAttributes records the attribute surface (ADR 0219).
func describeAttributes(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/product-attributes", openapi.Operation{
		Summary: "Lists the attributes a shopper can filter on, with their options.",
		Description: fmt.Sprintf("Every store-wide attribute in the operator's order, at most %d, "+
			"so the list is not paged. A select attribute lists its options; their handles "+
			"are what the listing's attribute filter takes. Like the tags it is not "+
			"channel-scoped.", models.MaxAttributes),
		Responses: map[string]any{
			"200": openapi.Response("Every attribute", d.List(models.Attribute{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreFacets, openapi.Operation{
		Summary: "Counts the products holding each value of every attribute.",
		Description: "It takes the listing's catalog filters, attribute filters included, " +
			"and answers, among the published products they keep in this channel, how many " +
			"hold each option of a select attribute, each value of a boolean one, and a " +
			"number of a number one with the smallest and the largest. An attribute the " +
			"request filters on is counted WITHOUT its own filter, so a shopper who chose " +
			"cotton still sees how many wool would give; every other attribute is counted " +
			"within every filter. in_stock and the price are answered after the catalog is " +
			"enriched and are refused here.",
		Tags: []string{tagProducts},
		Parameters: []openapi.Parameter{
			queryParameter("collection_id", typeString, "The listing's collection filter."),
			queryParameter("category_id", typeString, "The listing's category filter."),
			queryParameter("tag_id", typeString, "The listing's tag filter."),
			queryParameter("option_value", typeString, "The listing's option value filter."),
			queryParameter("q", typeString, "The listing's text search."),
			variantIDsParameter(),
			attributesParameter(),
			queryParameter("in_stock", typeBoolean, "Refused: availability is not a catalog column."),
			queryParameter("currency_code", typeString, "Refused with the price bounds."),
			queryParameter("min_price", typeInteger, "Refused: the price is not a catalog column."),
			queryParameter("max_price", typeInteger, "Refused: the price is not a catalog column."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Every attribute with its counts", d.List(service.Facet{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-attributes", openapi.Operation{
		Summary: "Defines a store-wide attribute a product takes a value of.",
		Description: fmt.Sprintf("The kind is number, boolean or select; a select attribute "+
			"lists its options, each with a handle the storefront filters by, and the other "+
			"kinds take none. A handle left out is derived from the title or the value. The "+
			"handle and the kind do not change later, since every value and every filter is "+
			"written against them. At most %d attributes stand at once, and a select holds "+
			"at most %d options.", models.MaxAttributes, models.MaxAttributeOptions),
		RequestBody: d.RequestBody(attributeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The defined attribute", d.Item(models.Attribute{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-attributes", openapi.Operation{
		Summary: "Lists the store-wide attributes with their options.",
		Responses: map[string]any{
			"200": openapi.Response("Every attribute", d.List(models.Attribute{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/product-attributes/{id}", openapi.Operation{
		Summary:     "Changes an attribute's title or order.",
		RequestBody: d.RequestBody(attributePatchRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The attribute as it is now", d.Item(models.Attribute{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-attributes/{id}", openapi.Operation{
		Summary: "Removes an attribute.",
		Description: "The products holding it no longer carry it, and a storefront filter " +
			"naming it is refused.",
		Responses: map[string]any{
			"200": openapi.Response("The removed attribute's id", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-attributes/{id}/options", openapi.Operation{
		Summary:     "Adds an option to a select attribute.",
		RequestBody: d.RequestBody(attributeOptionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The added option", d.Item(models.AttributeOption{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-attribute-options/{id}", openapi.Operation{
		Summary: "Removes an option; the products that chose it no longer carry it.",
		Responses: map[string]any{
			"200": openapi.Response("The removed option's id", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/products/{id}/attributes", openapi.Operation{
		Parameters: []openapi.Parameter{ifMatchParameter()},
		Summary:    "Replaces a product's attribute values.",
		Description: "Each value names its attribute by handle and matches its kind: the " +
			"option handles of a select attribute, one number, or one boolean. The body " +
			"replaces every value the product had; an empty list clears them. An attribute " +
			"named twice, an option the attribute does not have, a value of the wrong kind " +
			"and a number that is not finite are refused, and nothing is written.",
		RequestBody: d.RequestBody(productAttributesRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The product's values", d.List(models.ProductAttributeValue{})),
		},
	})
}
