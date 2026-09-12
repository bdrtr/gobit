package analytics

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names the parameters below are built from.
//
// They are constants because the linter counts the literals and is right to: a
// name written as "intger" compiles, produces a document, and only surfaces when
// a generated client sends the value with the wrong type.
const (
	schemaType = "type"
	typeString = "string"
	inQuery    = "query"
)

// Describe records the plugin's endpoint in the OpenAPI document.
//
// A plugin describes its endpoints the same way a module does: the module it
// brings enters the same registry as the ones in the box, so the composition
// root's type assertion finds this method with no special case. An endpoint that
// enters the document with a path, a method and no body is a valid OpenAPI model
// and therefore invisible to everything except the audit that asks (ADR 0035).
func (m *funnelModule) Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, FunnelPath, openapi.Operation{
		Summary: "Daily cart and order counts per region.",
		Description: "Three counts per day and region: the carts that were opened, the " +
			"carts that were completed, and the orders that were placed. They come from " +
			"the events the cart and order modules publish (ADR 0153), recorded one row " +
			"per event, so a redelivery cannot inflate them." +
			"\n\n" +
			"The completions and the placements are reported SEPARATELY and neither is a " +
			"subset of the other. The checkout places the order before it completes the " +
			"cart, so a placement with no completion is an order that failed after it was " +
			"opened; and a cart opened on one day and completed on the next is counted on " +
			"two different days, which is what asking about a day means." +
			"\n\n" +
			"The window is closed on the left and OPEN on the right: \"to\" is the first " +
			"day NOT included. That is the only form that tiles — two adjacent windows " +
			"asked for separately add up to the wide one and no day is counted twice. " +
			"Both ends are echoed in the response so a caller can see which window the " +
			"defaults produced." +
			"\n\n" +
			"The response does NOT page. A funnel is read as a whole, and the window is " +
			"bounded instead (at most 366 days)." +
			"\n\n" +
			"The counting starts when the plugin is installed: the funnel says nothing " +
			"about carts opened before that. Requires the " + ScopeRead + " scope.",
		Parameters: []openapi.Parameter{
			{
				Name: paramFrom, In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "The first day included, as 2006-01-02. Defaults to thirty days before \"to\".",
			},
			{
				Name: paramTo, In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "The first day NOT included, as 2006-01-02. Defaults to tomorrow, so today is in the window.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("The daily counts", d.SchemaOf(funnelResponse{})),
		},
	})
}
