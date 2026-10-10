package openapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/openapi"
)

// served decodes what the document handler answers.
func served(t *testing.T, h http.HandlerFunc) map[string]any {
	t.Helper()

	code, body := request(t, h)
	require.Equal(t, http.StatusOK, code, "body: %s", body)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &decoded))

	return decoded
}

// TestAWebhookIsWrittenUnderWebhooks is ADR 0435's half in the core.
//
// A request gobit SENDS has no route, so it reaches the document only through
// [openapi.Doc.DescribeWebhook], and three things are held here: the section
// is at the document's root under "webhooks" and only once something was
// described there, the operation keeps none of a route's defaults (a webhook's
// answers are the receiver's, and it carries its own proof) while the gaps
// that would write null or an empty Responses are filled and the x-internal
// mark is added, and describing one invalidates a document already served, as
// describing a route does.
func TestAWebhookIsWrittenUnderWebhooks(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	h := doc.Handler(buildRouter(t))

	assert.NotContains(t, served(t, h), "webhooks",
		"a document nobody described a webhook in carries no section; an empty one would "+
			"read as a promise of requests that never come")

	doc.DescribeWebhook("order.placed", openapi.Operation{Summary: "Delivers order.placed."})

	schema := served(t, h)
	require.Contains(t, schema, "webhooks",
		"a webhook described after the document was served is missing from it; either the "+
			"section is not written or the cache did not notice the description")
	webhooks := asMap(t, schema["webhooks"], "webhooks")
	post := asMap(t, asMap(t, webhooks["order.placed"], "order.placed")["post"], "order.placed.post")

	assert.Equal(t, "Delivers order.placed.", post["summary"])
	assert.Equal(t, "webhook_order_placed", post["operationId"],
		"a webhook's operationId is derived from its name, apart from every path's")
	assert.Equal(t, []any{}, post["security"],
		"a webhook names none of the document's schemes, and says so with an empty list")
	assert.Equal(t, map[string]any{
		"default": map[string]any{"description": "Any answer of the receiver's; the webhook's description names none."},
	}, post["responses"],
		"a webhook described with no answers takes none of a route's shared error answers, "+
			"which are gobit's, and is not written with an empty Responses, which OpenAPI 3.1 refuses")
	assert.Equal(t, true, post["x-internal"],
		"a webhook is not a call a client makes; without the mark openapi-generator gives "+
			"every client a method that POSTs the topic's name to this server")
	assert.NotContains(t, asMap(t, schema["paths"], "paths"), "order.placed",
		"a webhook is not a path")
}

// TestAWebhookBodyKeepsTheWrittenForm is the body the way the plugin describes
// it — [openapi.Doc.SchemaOf] of a type gobit writes — for a type that the
// document ALSO publishes in its request form. A path's body would be renamed
// to that form, which requires nothing (ADR 0363); a webhook's is the body
// gobit writes, so its reference keeps naming the written form, where a field
// written every time is required.
func TestAWebhookBodyKeepsTheWrittenForm(t *testing.T) {
	t.Parallel()

	d := document()
	describeLines(d, false)
	d.DescribeWebhook("line.added", openapi.Operation{
		RequestBody: openapi.Response("The line gobit sends", d.SchemaOf(cartLine{})),
	})

	schema := buildSchema(t, d, buildRouter(t))
	post := asMap(t, asMap(t, asMap(t, schema["webhooks"], "webhooks")["line.added"], "line.added")["post"], "post")

	assert.Equal(t, "#/components/schemas/CartLine", bodySchemaOf(t, post)["$ref"],
		"a webhook body gobit writes was renamed to the request form, which tells a receiver "+
			"that a field gobit sends every time may be missing")
	require.Contains(t, asMap(t, schema["components"], "components")["schemas"], "CartLineInput",
		"precondition: the type is also published in its request form, or the rename has nothing to rename to")
}

// TestTwoModulesDescribingOneWebhookStopTheDocument is ADR 0036's rule for a
// name two owners want, applied to a webhook: the document is not built, and
// the report names the webhook and both modules, rather than one module's
// description standing for requests the other sends. One module describing
// its own webhook again replaces it.
func TestTwoModulesDescribingOneWebhookStopTheDocument(t *testing.T) {
	t.Parallel()

	same := openapi.New("test", "v1")
	same.ForModule("webhookout", func() {
		same.DescribeWebhook("order.placed", openapi.Operation{Summary: "first"})
		same.DescribeWebhook("order.placed", openapi.Operation{Summary: "second"})
	})
	schema := buildSchema(t, same, buildRouter(t))
	post := asMap(t, asMap(t, asMap(t, schema["webhooks"], "webhooks")["order.placed"], "order.placed")["post"], "post")
	assert.Equal(t, "second", post["summary"], "a module describing its own webhook again replaces it")

	clashing := openapi.New("test", "v1")
	clashing.ForModule("webhookout", func() {
		clashing.DescribeWebhook("order.placed", openapi.Operation{Summary: "webhook-out's"})
	})
	clashing.ForModule("othersender", func() {
		clashing.DescribeWebhook("order.placed", openapi.Operation{Summary: "another plugin's"})
	})

	_, err := clashing.Build(buildRouter(t))
	require.Error(t, err, "two modules described one webhook and the document was built anyway")
	assert.Contains(t, err.Error(), "order.placed")
	assert.Contains(t, err.Error(), "webhookout")
	assert.Contains(t, err.Error(), "othersender")
}
