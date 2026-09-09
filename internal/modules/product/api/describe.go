package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The JSON Schema names that appear in the parameter schemas.
//
// The core's counterparts are unexported and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is generated, and it only surfaces once the client reading the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
	typeObject  = "object"
	typeArray   = "array"
)

// Describe writes product's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The query parameters are the ones the handler REALLY reads, and that reading
// lives in this package's store.go and [paging]. Had the description stood in
// another package, the parameter list would drift away from the reading and the
// two would silently diverge. That is why the module's [openapi.Describer]
// implementation delegates here.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes FROM THE TYPES.
// Binding the method to [Handler] would say the document DEPENDS on a service
// having been built; yet the document can be generated even when Register has
// never run.
//
// # Both surfaces are described
//
// The storefront endpoints are what a store client needs, the /admin/v1
// endpoints what the admin panel and the catalog integrations need.
//
// # The deletion endpoints return 200, NOT 204
//
// Every DELETE on the admin side writes a BODY (the [deleted] record, inside
// the single envelope; see admin.go). Writing "204, no body" out of habit would
// be a concrete lie here: a client generator would produce a method that never
// reads the body, and the "deleted" field that reports the deletion really
// happened would never reach the client.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathStoreProducts, openapi.Operation{
		Summary: "Lists the published products with their price and stock information.",
		// The tag is written by hand because the core derives it from the FIRST
		// path segment after the prefix, which is now "sales-channels". Left
		// derived, the three catalog reads would be grouped under a tag named
		// after their scoping segment and would land in the same generated
		// client class as auth's /admin/v1/sales-channels endpoints, which are a
		// different module's surface entirely.
		Tags: []string{"products"},
		// The QUERY parameters are the ones the handler READS, not the ones we
		// might wish for: the list below is exactly what
		// [Handler.storeListProducts] takes off the query string, and nothing
		// else is promised.
		//
		// "sales_channel_id" is DELIBERATELY ABSENT FROM THE QUERY STRING and
		// must not be added there. It is a PATH parameter and nothing else (see
		// [storeChannelScope]); the handler never looks at r.URL.Query() for it,
		// so describing it as a query parameter would promise an input that is
		// silently ignored and hint that the channel scope can be set twice.
		Parameters: []openapi.Parameter{
			salesChannelPathParameter(),
			queryParameter("collection_id", typeString,
				"Restricts the products to a single collection."),
			queryParameter("category_id", typeString,
				"Restricts the products to a single category. The id comes from "+
					"GET /store/v1/categories — a storefront has the word a shopper clicked, "+
					"not an id. A product that belongs to SEVERAL categories is returned "+
					"once, and the count counts products rather than memberships."),
			queryParameter("tag_id", typeString,
				"Restricts the products to a single tag. The id comes from "+
					"GET /store/v1/tags. A product carrying SEVERAL tags is returned once."),
			queryParameter("option_value", typeString,
				"Restricts the products to the ones OFFERING this option value (\"red\"). "+
					"The value comes from GET /store/v1/sales-channels/{sales_channel_id}"+
					"/option-values, the one vocabulary that returns text rather than ids: an "+
					"option belongs to exactly one product, so an option-value id would name a "+
					"single product's single value. "+
					"The match is on the FOLDED form and the fold is Go's, never the database's: "+
					"it trims, lower-cases and replaces each Latin letter carrying a mark with "+
					"its ASCII base, and KEEPS every rune it has no ASCII base for. So two "+
					"spellings of one value meet, two different values never merge, and a "+
					"Cyrillic or CJK catalog gains case-insensitive matching instead of losing "+
					"its text -- at the price that an unaccented approximation of such a value "+
					"finds nothing, which is a miss rather than a wrong answer. "+
					"The option TITLE is not part of it: this asks \"offers the value red on any "+
					"axis\"."),
			queryParameter("in_stock", typeBoolean,
				"Restricts the products by availability. A VARIANT is in stock when it is not "+
					"counted (manage_inventory false), OR it may be sold past zero "+
					"(allow_backorder), OR its available quantity is above zero; a PRODUCT is in "+
					"stock when at least one of its variants is, and a product with no variants "+
					"is not. A variant that is counted and linked to NO inventory item is NOT in "+
					"stock. "+
					"The same answer is written on every product body as \"in_stock\" whether or "+
					"not this parameter is given. "+
					"true keeps what can be bought and false keeps what cannot; leaving the "+
					"parameter out filters nothing. "+
					"The answer is over the sellable total across ALL locations and is not "+
					"regional. "+
					"It cannot be a database predicate, so the listing SCANS the catalog for it "+
					"and the page can come back SHORT with a cursor -- keep paging until "+
					"\"next_cursor\" is absent, which is the listing's end-of-catalog signal in "+
					"every case. \"offset\" is refused beside it and so is with_count=true."),
			queryParameter("currency_code", typeString,
				"The ISO 4217 currency the price bounds are given in, e.g. \"TRY\". Required "+
					"when min_price or max_price is given, and refused on its "+
					"own: prices in different currencies are not comparable and this listing "+
					"never converts them, because a rate is a fact about a moment this "+
					"repository does not store."),
			queryParameter("min_price", typeInteger,
				"Inclusive lower bound of the BASE price, in MINOR units (cents). "+
					"\"Base\" is the price that belongs to no price list: there is no default "+
					"price list in this schema and no column that could mark one, so the shop's "+
					"ordinary price is the ABSENCE of a list. The amount compared is the one for "+
					"quantity ONE, with no customer-group context -- a wholesale tier starting "+
					"at fifty units and a group price are both invisible to this filter. "+
					"A product matches when ANY of its variants' base prices does; a variant "+
					"priced ONLY on a list has no base price and never matches. "+
					"A product on SALE shows the sale price and filters by its base price, so a "+
					"shopper asking for \"under 100\" may not see something currently selling "+
					"for 90 -- that is the cost of a filter that does not move on its own. "+
					"Like in_stock it is answered after the rows are read, with the "+
					"same short-page, no-offset and no-count consequences."),
			queryParameter("max_price", typeInteger,
				"Inclusive upper bound of the BASE price, in MINOR units. See min_price "+
					"for what \"base price\" means; a lower bound greater than the upper one is "+
					"REFUSED rather than answered with the empty set it describes."),
			queryParameter("q", typeString,
				"Free-text search over the TITLE only, case-insensitively and anywhere in it. "+
					"The handle is a SEPARATE and EXACT filter, not part of this one — a "+
					"client searching for a handle fragment finds nothing. "+
					"The match has a leading wildcard and therefore uses no index; on a large "+
					"catalog it is a full scan (ADR 0015 measures it and names pg_trgm as the "+
					"standing remedy)."),
			queryParameter("sort", typeString,
				"Listing order: \"newest\" (default, and the order this listing had before "+
					"the parameter existed) or \"oldest\". Any other value is REFUSED rather "+
					"than falling back, because a client that asked for an order it did not "+
					"get cannot tell that from a catalog which happens to look that way. "+
					"Both orders ride the SAME index — it is declared (created_at DESC, id "+
					"DESC) and a b-tree is readable in either direction — so neither is the "+
					"expensive one. "+
					"A cursor belongs to ONE order: an \"after\" minted under \"newest\" is "+
					"refused when sent with sort=oldest, because it names a position in a key "+
					"space read the other way and would otherwise serve the wrong page. "+
					"Price and availability are not offered: they are not columns of the "+
					"catalog at all."),
			queryParameter("limit", typeInteger,
				"Page size; if not given the service's default applies."),
			queryParameter("offset", typeInteger,
				"Number of records to skip. It is REFUSED together with in_stock or a "+
					"price bound: an offset counts rows the DATABASE returns, while those two "+
					"filters remove rows after the fact, so \"skip 40\" would skip forty catalog "+
					"rows rather than forty matches and the second page would begin somewhere "+
					"the first had already shown. Page those filters with \"after\" instead, "+
					"which names a position rather than a count."),
			queryParameter("after", typeString,
				"Opaque cursor from a previous page's \"next_cursor\". Cheaper than \"offset\" "+
					"for deep pages: offset makes the database walk and DISCARD every row it "+
					"skips, so its cost grows with depth, while a cursor becomes an index "+
					"condition and stays flat. Measured on a 52,000-product catalog, the last "+
					"page costs 34.71 ms by offset and 0.08 ms by cursor. "+
					"\"after\" and \"offset\" name two different positions and are REFUSED "+
					"together. When the response carries no \"next_cursor\" the listing is "+
					"exhausted."),
			// It is essential that the client sees from the document that the
			// counter can be turned off and that its default is ON: both so as
			// not to leave a silent default, and so that it knows in advance
			// that the "count" field drops out of the body when it is turned
			// off. The measurement is written here too — a description that
			// does not say what the parameter buys leaves a parameter nobody
			// will use.
			queryParameter("with_count", typeBoolean,
				"Should the total counter be computed? Default: true (today's behavior). "+
					"If false is given the count query is not run at all and the response "+
					"envelope carries NO \"count\" field — it does not return 0, it does not "+
					"return null, the field is ABSENT. "+
					"Independently of the page size, the counter walks the whole set the "+
					"sales channel filter is applied to and its cost grows WITH THE SIZE OF "+
					"THE CATALOG: on a 52,004-product catalog, 64.07 ms of the 67.00 ms "+
					"measured for the list service is the counter; the same call without the "+
					"counter takes 0.65 ms (the endpoint's remaining enrichment legs are "+
					"independent of the counter and are not skipped by this parameter). "+
					"The total number is generally needed once on the first page; on the "+
					"following pages the same number is computed again. "+
					"It is REFUSED, rather than silently dropped, when it is asked for "+
					"explicitly beside in_stock or a price bound: those two are answered "+
					"after the rows are read, so counting their matches would mean reading and "+
					"enriching the WHOLE catalog. Send with_count=false, or leave the "+
					"parameter out.",
			),
		},
		Responses: map[string]any{
			// The envelope SCHEMA has to say the counter can drop as well:
			// "count" is NOT a required field here (see
			// [openapi.Doc.ListOptionalCount]). Had d.List been used, the
			// document would declare a field required that does not exist in a
			// with_count=false response.
			"200": openapi.Response("Storefront products",
				d.ListOptionalCount(service.StoreProduct{}, openapi.WithCursor())),
			"403": channelRefusedResponse(),
		},
	})

	d.Describe(http.MethodGet, pathStoreProduct, openapi.Operation{
		Summary: "Returns a single storefront product by id or by handle.",
		Tags:    []string{"products"},
		// Both path parameters are derived from the pattern by the core as well;
		// the only reason they are written BY HAND here is their description.
		// The second is named "id" but the value may also be a handle, and only
		// the handler knows this; the deriver cannot tell it by looking at the
		// pattern.
		Parameters: []openapi.Parameter{
			salesChannelPathParameter(),
			{
				Name:        "id",
				In:          "path",
				Required:    true,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Product id (prod_…) or the handle in the storefront address.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("Storefront product", d.Item(service.StoreProduct{})),
			"403": channelRefusedResponse(),
		},
	})

	describeStorefrontVocabulary(d)
	describeStorefrontGraphQL(d)
	describeAdminProducts(d)
	describeAdminVariants(d)
	describeAdminOptions(d)
	describeAdminLinks(d)
	describeAdminSalesChannels(d)
	describeAdminTaxonomy(d)
}

// describeStorefrontVocabulary documents the three endpoints that turn a word
// into an id.
//
// They are documented together because they exist for one reason: the catalog
// listing filters by id, and a storefront has the word a shopper clicked. A
// filter whose value cannot be obtained is a filter nobody can call.
func describeStorefrontVocabulary(d *openapi.Doc) {
	paging := []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"Page size; if not given the service's default applies."),
		queryParameter("offset", typeInteger, "Number of records to skip."),
	}

	d.Describe(http.MethodGet, "/store/v1/collections", openapi.Operation{
		Summary:    "Lists the collections, for the \"collection_id\" filter of the product listing.",
		Parameters: paging,
		Responses: map[string]any{
			"200": openapi.Response("Collections", d.List(models.Collection{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/categories", openapi.Operation{
		Summary: "Lists the categories a shopper may see.",
		// What is NOT listed is worth stating in the document: a client that
		// cannot see a category it knows exists needs to be able to find out
		// why without reading this repository.
		Parameters: append([]openapi.Parameter{
			queryParameter("parent_id", typeString,
				"Lists only the children of that category. A navigation menu asks for one "+
					"level at a time; without it the whole tree comes back flat."),
		}, paging...),
		Responses: map[string]any{
			"200": openapi.Response(
				"Categories that are active and not internal. A category the merchant has "+
					"switched off (\"is_active\": false) or marked as operator-only "+
					"(\"is_internal\": true) is NOT listed here, and neither is it counted.",
				d.List(models.Category{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/tags", openapi.Operation{
		Summary:    "Lists the product tags.",
		Parameters: paging,
		Responses: map[string]any{
			"200": openapi.Response("Tags", d.List(models.Tag{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreOptionValues, openapi.Operation{
		Summary: "Lists the option vocabulary of the visible catalog.",
		// Written by hand for the reason the product listing's is; left derived
		// the tag would be "sales-channels".
		Tags: []string{"option-values"},
		Description: "The DISTINCT (option title, value) pairs the catalog offers — " +
			"\"Color: red\", \"Size: M\". " +
			"It is the one vocabulary endpoint that returns TEXT and no id, and the " +
			"reason is structural: an option belongs to exactly ONE product, so an " +
			"option-value id names a single product's single value and filtering a " +
			"catalog by it would return at most one product. " +
			"Two products that both offer \"Color: red\" are two rows in the database " +
			"and ONE entry here. " +
			"The vocabulary is scoped exactly as the product listing is — published " +
			"products, and the sales channel named in the path — because every entry " +
			"exists BECAUSE some product carries it, and an unscoped vocabulary would " +
			"name what the listing hides. That is also why this is the one vocabulary " +
			"endpoint under the channel segment: collections, categories and tags do " +
			"not vary by channel and keep their unscoped addresses.",
		Parameters: append([]openapi.Parameter{salesChannelPathParameter()}, paging...),
		Responses: map[string]any{
			"200": openapi.Response("Option values", d.List(models.OptionValuePair{})),
			"403": channelRefusedResponse(),
		},
	})
}

// graphqlRequest is the GraphQL request body.
//
// This type DOES NOT decode the body; gqlgen does (see graph.NewHandler). It
// exists only for the document and the reason is concrete: an undescribed POST
// endpoint appears in the document with no body, and a client generator
// produces a method with no parameters — that is, one that cannot be called.
// The fields are fixed by GraphQL's HTTP transport, so they carry no risk of
// silently drifting from gqlgen's internal type.
type graphqlRequest struct {
	// Query is the GraphQL document to be executed.
	Query string `json:"query"`
	// OperationName picks the operation to run when the document carries more
	// than one.
	OperationName string `json:"operationName,omitempty"`
	// Variables are the query's variables.
	Variables map[string]any `json:"variables,omitempty"`
}

// describeStorefrontGraphQL describes the GraphQL storefront endpoint.
//
// # WHY the endpoint is described in OpenAPI
//
// The contract is not here but in the module's GraphQL schema
// (graph/schema.graphqls) and OpenAPI cannot describe it: a single path, a
// single body and the fields the CLIENT CHOOSES. Still, the endpoint exists in
// the router and the document is generated from the router (see
// [openapi.Doc.Build]); had it not been described it would appear in the
// document as a POST line with no summary and no body. The description does two
// things: it gives the shape of the body and it says where the real contract
// is.
//
// # The response envelope is written, its INSIDE is not
//
// "data" and "errors" are the fixed parts of the GraphQL contract and they are
// written. The INSIDE of "data", on the other hand, changes shape with the
// query; typing it would mean writing a separate schema for every query, or
// lying.
//
// # The idempotency exemption IS WRITTEN
//
// The endpoint is the only POST under /store/v1 that is EXEMPT from the guard
// stack's idempotency ring (see corehttp.GuardOptions.IdempotencyExempt). This
// cannot be hidden from a client that assumes every POST endpoint behaves the
// same: a client that sends an Idempotency-Key will never see an
// Idempotency-Replayed header here and, if it does not read this in the
// document, it will either think the record worked or think it is a missing
// implementation and report it.
func describeStorefrontGraphQL(d *openapi.Doc) {
	d.Describe(http.MethodPost, graph.Path, openapi.Operation{
		Summary: "Reads the storefront catalog with GraphQL.",
		Description: "The contract is the module's GraphQL schema; for the field list see the " +
			"schema (graph/schema.graphqls) or an introspection query against the endpoint. " +
			"The surface is READ-ONLY: there are products and product queries, no mutation. " +
			"The sales channel filter comes from the request's publishable key; there is NO " +
			"query argument for it. " +
			"Only POST is accepted (GET returns 405). " +
			"By the GraphQL contract the response of a request that can be resolved is 200 " +
			"even if there are field errors, and the errors come back in the \"errors\" array " +
			"in the body; their codes come from the SAME dictionary as the ones on the REST " +
			"surface (extensions.code). " +
			"The DEPTH and the estimated COST of the document are limited (configured per " +
			"installation): a document that exceeds a limit is not executed and comes back " +
			"inside a 200 with the code DEPTH_LIMIT_EXCEEDED / COMPLEXITY_LIMIT_EXCEEDED. " +
			"The cost is multiplied by the number of records asked for in list fields — that " +
			"is, the query gets more expensive as the limit grows. If the request body " +
			"exceeds 64 KiB the document is not parsed at all and the response is not the " +
			"GraphQL envelope but the core's error envelope (422). " +
			"Unlike the other POSTs on the surface, the endpoint TAKES NO IDEMPOTENCY " +
			"RECORD: the Idempotency-Key header is accepted but ignored and the response " +
			"never carries Idempotency-Replayed. The reason is twofold — the surface is " +
			"read-only, so there is no side effect for a record to protect; and because " +
			"GraphQL says 200 to every request it resolves, a record would also store a " +
			"transient server error and would keep giving the old response back for the " +
			"whole TTL even after the fault was fixed.",
		RequestBody: d.RequestBody(graphqlRequest{}),
		Responses: map[string]any{
			// The envelope carries the same name as REST's ("data") but its
			// INSIDE is not typed: the client's query decides its shape.
			// "errors", on the other hand, is part of the GraphQL contract and
			// comes inside the 200 response — this is the single point where
			// this endpoint differs from the others, which is why it is
			// written.
			"200": openapi.Response(
				"GraphQL response: \"data\" carrying the queried fields and/or \"errors\".",
				map[string]any{
					schemaType: typeObject,
					"properties": map[string]any{
						"data": map[string]any{
							schemaType:    typeObject,
							"description": "The queried fields; the query decides their shape.",
						},
						"errors": map[string]any{
							schemaType:    typeArray,
							"description": "The error list; the codes come from the same dictionary as REST (extensions.code).",
							"items":       map[string]any{schemaType: typeObject},
						},
					},
				}),
		},
	})
}

// describeAdminProducts describes the /admin/v1 product endpoints.
//
// The response record is [models.Product], NOT the storefront's
// [service.StoreProduct]: the admin service does no price and stock enrichment
// (see the writeItem calls). Writing the storefront type would be promising the
// client "price_set" and "inventory_item" fields that never get filled.
func describeAdminProducts(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products", openapi.Operation{
		Summary:     "Creates a new product with its options, variants and images.",
		RequestBody: d.RequestBody(createProductRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created product", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products", openapi.Operation{
		Summary: "Lists the whole catalog, drafts included, with filters.",
		// The parameters are the ones [Handler.adminListProducts] READS. There
		// are two differences from the storefront listing and both are specific
		// to the admin side: "status" makes draft/archived records visible (the
		// storefront returns only the published ones), and "expand" adds the
		// variants and options to the response.
		Parameters: []openapi.Parameter{
			queryParameter("collection_id", typeString,
				"Restricts the products to a single collection."),
			queryParameter("handle", typeString,
				"Restricts the products to a single handle."),
			queryParameter("q", typeString,
				"Free-text search over the TITLE only, case-insensitively and anywhere in it. "+
					"The handle is a SEPARATE and EXACT filter, not part of this one — a "+
					"client searching for a handle fragment finds nothing. "+
					"The match has a leading wildcard and therefore uses no index; on a large "+
					"catalog it is a full scan (ADR 0015 measures it and names pg_trgm as the "+
					"standing remedy)."),
			queryParameter("status", typeString,
				"Publication status filter: draft | published | archived."),
			queryParameter("expand", typeBoolean,
				"If true the variant, option, image and taxonomy records are returned too."),
			queryParameter("limit", typeInteger,
				"Page size; if not given the service's default applies."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
			queryParameter("after", typeString,
				"Opaque cursor from a previous page's \"next_cursor\". Cheaper than \"offset\" "+
					"for deep pages: offset makes the database walk and DISCARD every row it "+
					"skips, so its cost grows with depth, while a cursor becomes an index "+
					"condition and stays flat. Measured on a 52,000-product catalog, the last "+
					"page costs 34.71 ms by offset and 0.08 ms by cursor. "+
					"\"after\" and \"offset\" name two different positions and are REFUSED "+
					"together. When the response carries no \"next_cursor\" the listing is "+
					"exhausted."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of products",
				d.List(models.Product{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}", openapi.Operation{
		Summary: "Returns a single product by its id.",
		Responses: map[string]any{
			"200": openapi.Response("Product", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/products/{id}", openapi.Operation{
		Summary:     "Updates only the given fields of the product.",
		RequestBody: d.RequestBody(updateProductRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated product", d.Item(models.Product{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/products/{id}", openapi.Operation{
		Summary: "Deletes the product.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})
}

// describeAdminVariants describes the /admin/v1 variant endpoints.
func describeAdminVariants(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/variants", openapi.Operation{
		Summary:     "Adds a new variant to the product.",
		RequestBody: d.RequestBody(createVariantRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created variant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/variants", openapi.Operation{
		Summary: "Lists the product's variants with their option values.",
		// The handler ALWAYS loads the option values (WithOptionValues:true),
		// that is, it reads no key such as "expand"; only the pagination is
		// read.
		Parameters: pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of variants", d.List(models.Variant{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/variants/{id}", openapi.Operation{
		Summary: "Returns a single variant by its id.",
		Responses: map[string]any{
			"200": openapi.Response("Variant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/variants/{id}", openapi.Operation{
		Summary:     "Updates only the given fields of the variant.",
		RequestBody: d.RequestBody(updateVariantRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated variant", d.Item(models.Variant{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}", openapi.Operation{
		Summary: "Deletes the variant.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})
}

// describeAdminOptions describes the /admin/v1 option and option value
// endpoints.
func describeAdminOptions(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/options", openapi.Operation{
		Summary:     "Adds a new option axis to the product.",
		RequestBody: d.RequestBody(createOptionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created option", d.Item(models.Option{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/options", openapi.Operation{
		Summary: "Lists the product's option axes with their values.",
		// There is DELIBERATELY no pagination parameter:
		// [Handler.adminListOptions] never looks at the query string and writes
		// the whole result as if it were a single page. Announcing "limit"
		// would produce an argument the client sends and the server ignores — a
		// product's option count fits in the palm of a hand anyway.
		Responses: map[string]any{
			"200": openapi.Response("The option list", d.List(models.Option{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-options/{id}/values", openapi.Operation{
		Summary:     "Adds a new value to the option.",
		RequestBody: d.RequestBody(optionValueRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The added option value", d.Item(models.OptionValue{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-options/{id}", openapi.Operation{
		Summary: "Deletes the option together with its values.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-option-values/{id}", openapi.Operation{
		Summary: "Deletes a single option value.",
		Description: "A value carried by a living variant is refused with 409; change " +
			"those variants first. The id in the path is the VALUE's own id, not the " +
			"option's.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})
}

// describeAdminLinks describes the variant's price/stock link endpoints.
//
// # A known limit: [linkRequest] carries two fields, each endpoint reads ONE
//
// The body schema is derived from the REAL DTO and that DTO carries both: the
// price endpoint reads only "price_set_id", the stock endpoint only
// "inventory_item_id" (see [Handler.adminSetPriceSet],
// [Handler.adminSetInventoryItem]). That is, the schema shows a field that can
// be sent but WILL BE IGNORED. The limit was written down deliberately: the
// right place for the fix is not the schema but splitting the DTO per endpoint,
// and that is the api layer's decision, not the description's. The field NAMES
// and TYPES are correct; the schema shows no made-up field.
func describeAdminLinks(d *openapi.Doc) {
	d.Describe(http.MethodPut, "/admin/v1/variants/{id}/price-set", openapi.Operation{
		Summary:     "Links the variant to a price set.",
		RequestBody: d.RequestBody(linkRequest{}),
		Responses: map[string]any{
			// The response is not the link itself but the variant's CURRENT set
			// of links; the client sees the result without a second GET (see
			// writeVariantLinks).
			"200": openapi.Response("The variant's current links", d.Item(service.VariantLinks{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}/price-set", openapi.Operation{
		Summary: "Removes the variant's price set link.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/variants/{id}/inventory-item", openapi.Operation{
		Summary:     "Links the variant to an inventory item.",
		RequestBody: d.RequestBody(linkRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The variant's current links", d.Item(service.VariantLinks{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/variants/{id}/inventory-item", openapi.Operation{
		Summary: "Removes the variant's inventory item link.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/variants/{id}/links", openapi.Operation{
		Summary: "Returns the variant's price set and inventory item links.",
		Responses: map[string]any{
			"200": openapi.Response("The variant's links", d.Item(service.VariantLinks{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-images/by-upload/{upload_id}", openapi.Operation{
		Summary: "Returns the product images made from an upload.",
		Responses: map[string]any{
			// An upload nothing uses and an id that belongs to no upload give
			// the SAME answer, an empty list: the catalog cannot see the file
			// module's records and does not claim to know whether the upload
			// exists. That is why there is no 404 here.
			"200": openapi.Response("The images using the upload", d.Item(uploadImages{})),
		},
	})
}

// describeAdminSalesChannels describes the product-to-sales-channel link
// endpoints.
//
// All three return the SAME record ([productSalesChannels]): because the link is
// many-to-many, the client's question is "which channels am I in" and the answer
// is given in its current state after every request.
func describeAdminSalesChannels(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/products/{id}/sales-channels", openapi.Operation{
		Summary:     "Links the product to a sales channel.",
		RequestBody: d.RequestBody(linkSalesChannelRequest{}),
		Responses: map[string]any{
			// 200, NOT 201: linking is idempotent and sending the same pair a
			// second time creates no new record (see
			// [Handler.adminAddSalesChannel]).
			"200": openapi.Response("The product's current channel links", d.Item(productSalesChannels{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/products/{id}/sales-channels/{sales_channel_id}",
		openapi.Operation{
			Summary: "Removes one of the product's sales channel links.",
			Responses: map[string]any{
				// NOT a bodiless 204: the endpoint returns the list of the
				// remaining links.
				"200": openapi.Response("The product's current channel links", d.Item(productSalesChannels{})),
			},
		})

	d.Describe(http.MethodGet, "/admin/v1/products/{id}/sales-channels", openapi.Operation{
		Summary: "Returns the sales channels the product is linked to.",
		Responses: map[string]any{
			"200": openapi.Response("The product's channel links", d.Item(productSalesChannels{})),
		},
	})
}

// describeAdminTaxonomy describes the collection, category and tag endpoints.
func describeAdminTaxonomy(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/product-collections", openapi.Operation{
		Summary:     "Creates a new collection.",
		RequestBody: d.RequestBody(createCollectionRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created collection", d.Item(models.Collection{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-collections", openapi.Operation{
		Summary:    "Lists the collections with pagination.",
		Parameters: pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of collections", d.List(models.Collection{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-collections/{id}", openapi.Operation{
		Summary: "Deletes the collection and releases its products.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-categories", openapi.Operation{
		Summary:     "Creates a new category.",
		RequestBody: d.RequestBody(createCategoryRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created category", d.Item(models.Category{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-categories", openapi.Operation{
		Summary: "Lists the categories, filtered by parent category.",
		Parameters: append(pagingParameters(),
			queryParameter("parent_id", typeString,
				"Returns only the CHILD categories of this category.")),
		Responses: map[string]any{
			"200": openapi.Response("A page of categories", d.List(models.Category{})),
		},
	})

	d.Describe(http.MethodPatch, "/admin/v1/product-categories/{id}", openapi.Operation{
		Summary: "Changes a category and can move it in the tree.",
		Description: "A field that is not supplied is preserved. Sending clear_parent makes " +
			"the category a root, and it may not be sent together with parent_id. A move " +
			"that would place the category under itself or under one of its own " +
			"descendants is refused with 422.",
		RequestBody: d.RequestBody(updateCategoryRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated category", d.Item(models.Category{})),
		},
	})

	// The refusal is described in prose and NOT recorded as a separate "409".
	// The reason is the payment module's, and it holds here: the error body is
	// the core's shared envelope, and the way to refer to it (the name of the
	// "Error" component) is an internal detail of the core — repeating it here
	// would create a second record that breaks silently the day the name
	// changes.
	d.Describe(http.MethodDelete, "/admin/v1/product-categories/{id}", openapi.Operation{
		Summary: "Deletes the category.",
		Description: "A category that still has subcategories is refused with 409; " +
			"move or delete the children first.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/product-tags", openapi.Operation{
		Summary:     "Creates a new tag.",
		RequestBody: d.RequestBody(createTagRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created tag", d.Item(models.Tag{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/product-tags", openapi.Operation{
		Summary:    "Lists the tags with pagination.",
		Parameters: pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("A page of tags", d.List(models.Tag{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/product-tags/{id}", openapi.Operation{
		Summary: "Deletes the tag.",
		Description: "The products that carried it are not changed; the tag simply stops " +
			"appearing on them. The value becomes free for a new tag.",
		Responses: map[string]any{
			"200": openapi.Response("Deletion record", d.Item(deleted{})),
		},
	})
}

// pagingParameters returns the two parameters [paging] reads.
//
// A new slice is produced on every call: a shared slice would let a parameter
// the caller appended leak into another endpoint's list.
func pagingParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"Page size; if not given the service's default applies."),
		queryParameter("offset", typeInteger, "Number of records to skip."),
	}
}

// salesChannelPathParameter describes the channel segment of the three
// channel-scoped catalog reads.
//
// The core derives a bare "sales_channel_id, in path, string" from the pattern
// on its own; what it cannot derive is the RULE, and the rule is the one thing a
// client reading this has to know: the segment picks WHICH of the key's channels
// to read, it does not decide WHETHER the key may read one. A client that
// mistook it for a free choice would write a storefront that walks other
// merchants' channel ids and be surprised by a 403 with no explanation in the
// document.
//
// The threat model of the id being in a URL at all is written here too, because
// this is the parameter that puts it there.
func salesChannelPathParameter() openapi.Parameter {
	return openapi.Parameter{
		Name:     paramSalesChannelID,
		In:       "path",
		Required: true,
		Schema:   map[string]any{schemaType: typeString},
		Description: "The sales channel this catalog read is scoped to (sc_...). " +
			"It NARROWS and never broadens: the channel named here must be one the " +
			"request's publishable key is bound to, and a channel the key does not " +
			"hold is refused with a 403 rather than served. A key bound to several " +
			"channels reads them ONE AT A TIME, one request each; there is no union " +
			"response. " +
			"The channel is in the URL rather than in a header so that a shared cache " +
			"has a key it can see with nothing configured: two different keys " +
			"authorized for the same channel receive byte-identical bodies. " +
			"That also makes the id public — it reaches browser history, referrers " +
			"and access logs — which is harmless while the ORIGIN authorizes, and is " +
			"not harmless in a deployment that lets an edge answer without the origin.",
	}
}

// channelRefusedResponse describes the 403 the channel-scoped catalog reads can
// produce.
//
// The core adds a 403 to the admin surface and not to the storefront, and the
// reason it gives is sound: a 403 is only meaningful where there is an
// authorization step, and until ADR 0044 the storefront had none — the
// publishable key carries no scope. These three routes now do have one, so the
// code is described HERE, by the module that knows the rule, rather than by
// widening the core's default to every store route that still cannot produce it.
func channelRefusedResponse() map[string]any {
	return openapi.ErrorResponse(
		"The publishable key is not bound to the sales channel named in the path. " +
			"The answer does not depend on whether that channel exists: the key's own " +
			"channel set is the only thing consulted, so this code discloses nothing " +
			"about another merchant's channels.")
}

// queryParameter defines a parameter that is read from the query string.
//
// None of them is REQUIRED: when it is not given the handler carries on with
// the default (see [stringParam], [intParam], [boolParam]).
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}
