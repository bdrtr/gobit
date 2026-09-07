package searchpg

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names the parameters and the envelope below are built from.
//
// They are constants because the linter counts the literals and is right to: a
// name written as "intger" compiles, produces a document, and only surfaces when
// a generated client sends the value with the wrong type.
const (
	schemaType   = "type"
	typeString   = "string"
	typeInteger  = "integer"
	typeObject   = "object"
	typeArray    = "array"
	inQuery      = "query"
	fieldMinimum = "minimum"
	fieldMaximum = "maximum"
)

// Describe records the plugin's two endpoints in the OpenAPI document.
//
// # A plugin describes its endpoints the same way a module does
//
// The module a plugin brings enters the same registry as the ones in the box, so
// the composition root's type assertion finds this method with no special case.
// Nothing ever stopped this plugin from having one; until 2026-09-07 its two
// endpoints simply had no description and nothing in the repository could say so
// (ADR 0035). They were the last two lines on the ledger that audit reads.
//
// # The search result's item is an OPEN object, and that is not laziness
//
// The records come back as raw JSON from the catalog's "product.interop" surface
// and are written out unchanged: what the search endpoint returns is EXACTLY the
// body the storefront's product endpoint returns. This package cannot say so in
// a schema, because it cannot import product (ADR 0001) and therefore cannot
// name the type — and defining the record's shape here would produce a second
// copy of the storefront representation, which is the very thing catalog.go
// refuses to do.
//
// So the item is declared as an object with no fixed properties and the
// description says where the real shape is written. That is a weaker schema than
// the rest of the document, and it is the honest one: a made-up field list here
// would start lying the day product gains a field.
func (m *searchModule) Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, SearchPath, openapi.Operation{
		Summary: "Full-text search over the published catalog.",
		Description: "The flow is two steps and the split matters: the index gives the " +
			"matching product ids IN RELEVANCE ORDER, and the records are read from the " +
			"catalog in that same order. Each record is byte for byte the body " +
			"GET /store/v1/products/{id} returns — this endpoint does not reshape it, " +
			"which is why its schema below is an open object rather than a second copy " +
			"of the product's." +
			"\n\n" +
			"Publication status and the sales-channel filter are applied BY THE CATALOG " +
			"while the records are read, not by the index. A stale row left in the index " +
			"therefore cannot leak a product from another channel: an id that does not " +
			"pass the filter never enters the response." +
			"\n\n" +
			"\"count\" is the number of records IN THIS RESPONSE, not the total number of " +
			"matches. The channel filter runs during the read, so a true total could only " +
			"be known after every match had been read and filtered, and reporting the raw " +
			"match count would be a number that counts products the caller can never see. " +
			"The visible consequence: a page can come back SHORTER than the limit while " +
			"more matches exist, so page until an empty page arrives." +
			"\n\n" +
			"The query supports what websearch_to_tsquery supports: quoting for an exact " +
			"phrase, OR, and - to exclude. A query made only of exclusions works and " +
			"returns results in id order — there is no positive signal to rank by. " +
			"Nothing a user can type produces a 500." +
			"\n\n" +
			"There is no stemming: a search for \"pen\" does not find a product that says " +
			"\"pens\". The dictionary is 'simple' because this framework does not know its " +
			"installation's language, and stemming in the wrong one is worse than none.",
		Parameters: []openapi.Parameter{
			{
				Name: paramQuery, In: inQuery, Required: true,
				Schema: map[string]any{schemaType: typeString, "maxLength": maxQueryBytes},
				Description: "The search text. It is REQUIRED and may not be empty: an empty " +
					"query does not mean \"return everything\" — the way to list the catalog " +
					"is GET /store/v1/products.",
			},
			{
				Name: paramLimit, In: inQuery,
				Schema: map[string]any{schemaType: typeInteger, fieldMinimum: 1, fieldMaximum: maxLimit},
				Description: "Page size; the default applies when it is not given. A value " +
					"above the maximum is REFUSED rather than clamped, so a caller never " +
					"silently receives fewer records than it asked for.",
			},
			{
				Name:        paramOffset,
				In:          "query",
				Schema:      map[string]any{schemaType: typeInteger, fieldMinimum: 0},
				Description: "Number of records to skip.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The matching products, most relevant first", searchEnvelopeSchema()),
		},
	})

	d.Describe(http.MethodPost, ReindexPath, openapi.Operation{
		Summary: "Rebuilds the whole search index from the catalog.",
		Description: "This is the repair path, and it is the only one. The index is kept " +
			"fresh by catalog events, but events carry only what happened AFTER startup: " +
			"when the plugin is fitted to an existing shop, when the bus was unreachable " +
			"for a while, or when an event was missed, this call is what brings the index " +
			"back to the truth." +
			"\n\n" +
			"It runs SYNCHRONOUSLY and answers with the counts when the work is done. A " +
			"202 would tell the caller only that it started, and with nowhere to see the " +
			"outcome a failed round would disappear silently. On a large catalog the " +
			"request is long; it is an operations tool and is not on a customer's path." +
			"\n\n" +
			"The order is WRITE first, SWEEP after. A threshold is taken from the database " +
			"clock, every write refreshes a row's stamp, and only when the round finishes " +
			"are the rows older than the threshold deleted — which is how products that " +
			"were unpublished or deleted leave the index without any event. A round that " +
			"stops halfway does NOT sweep, so an interrupted call leaves the index " +
			"un-updated rather than corrupted." +
			"\n\n" +
			"\"pages\" is how many catalog pages were read and is there as a cost figure. " +
			"Requires the " + ScopeWrite + " scope.",
		Responses: map[string]any{
			"200": openapi.Response("What the round wrote, swept and read", d.Item(reindexResult{})),
		},
	})
}

// searchEnvelopeSchema is the list envelope with an OPEN item.
//
// It is built by hand rather than through [openapi.Doc.List] for one reason: the
// item cannot be derived from a Go type. The records are catalog JSON this
// package deliberately never parses, and the type that describes them lives in a
// module a plugin may not import. The envelope's own four fields are written out
// so that the shape of the page — count, offset, limit — is still a contract.
func searchEnvelopeSchema() map[string]any {
	return map[string]any{
		schemaType: typeObject,
		"properties": map[string]any{
			"data": map[string]any{
				schemaType: typeArray,
				"items": map[string]any{
					schemaType: typeObject,
					"description": "A storefront product record, identical to the body of " +
						"GET /store/v1/products/{id}. Its fields are the product module's " +
						"contract and are documented there; this plugin passes them through " +
						"untouched and does not restate them.",
				},
			},
			"count":  map[string]any{schemaType: typeInteger},
			"offset": map[string]any{schemaType: typeInteger},
			"limit":  map[string]any{schemaType: typeInteger},
		},
		"required": []any{"data", "count", "offset", "limit"},
	}
}
