package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tool list is DERIVED, so what these check is the derivation: which
// operations become tools, what a tool is called, and what it accepts.
//
// The document is written out here rather than taken from the installation on
// purpose. The end-to-end claim — that the list comes from the document this
// process serves — is asserted in internal/app against the real router; what
// belongs here is the shape of the rule, including the shapes a real document
// does not happen to contain today.

// sampleDocument is an OpenAPI document with the cases that matter.
func sampleDocument() map[string]any {
	return map[string]any{
		"paths": map[string]any{
			"/admin/v1/orders": map[string]any{
				"get": map[string]any{
					"operationId": "listOrders",
					"summary":     "Pages the orders.",
					"parameters": []any{
						map[string]any{
							"name": "limit", "in": "query", "required": false,
							"schema": map[string]any{"type": "integer"},
						},
					},
				},
				// A write on the same path must not become a tool.
				"post": map[string]any{"operationId": "createOrder", "summary": "Opens one."},
			},
			"/admin/v1/orders/{id}": map[string]any{
				"get": map[string]any{
					"summary": "Reads one order.",
					"parameters": []any{
						map[string]any{
							"name": "id", "in": "path", "required": true,
							"schema": map[string]any{"type": "string"},
						},
					},
				},
			},
			// The store surface is a shopper's and is not the operator's tool.
			"/store/v1/products": map[string]any{
				"get": map[string]any{"operationId": "listProducts", "summary": "The catalog."},
			},
			// The panel serves HTML.
			"/admin/ui/products": map[string]any{"get": map[string]any{"summary": "A page."}},
		},
	}
}

// TestOnlyTheAdminReadsBecomeTools is the population.
func TestOnlyTheAdminReadsBecomeTools(t *testing.T) {
	t.Parallel()

	tools, err := toolsFrom(sampleDocument())
	require.NoError(t, err)

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}

	assert.Equal(t, []string{"get_orders_id", "listOrders"}, names,
		"the tool list is not the admin READS.\nA write that became a tool would make "+
			"\"read-only\" a property of the caller's restraint; a store path would need a "+
			"publishable key this server does not hold; a panel path answers HTML.")
}

// TestAToolIsNamedByItsOperationID pins the name a client calls.
//
// The operationId is what a generated client would call the endpoint, and two
// names for one endpoint is one too many. Where the document carries none the
// name is derived from the path, which moves when the endpoint moves — a tool
// that vanished is a clearer failure than one that answers about something else.
func TestAToolIsNamedByItsOperationID(t *testing.T) {
	t.Parallel()

	tools, err := toolsFrom(sampleDocument())
	require.NoError(t, err)

	byName := map[string]tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	assert.Contains(t, byName, "listOrders", "the document's own operationId must be the name")
	assert.Contains(t, byName, "get_orders_id",
		"an operation with no operationId must still get a stable name derived from its path")
}

// TestAToolsInputSchemaIsItsParameters is what a model fills in.
func TestAToolsInputSchemaIsItsParameters(t *testing.T) {
	t.Parallel()

	tools, err := toolsFrom(sampleDocument())
	require.NoError(t, err)

	byName := map[string]tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}

	item := byName["get_orders_id"]
	properties, ok := item.InputSchema["properties"].(map[string]any)
	require.True(t, ok, "the schema carries no properties; a model would send nothing")
	assert.Contains(t, properties, "id")
	assert.Equal(t, []string{"id"}, item.InputSchema["required"],
		"a path parameter is required, and a model that omits it would be answered about "+
			"the collection instead of the item")

	list := byName["listOrders"]
	listProperties, _ := list.InputSchema["properties"].(map[string]any)
	assert.Contains(t, listProperties, "limit")
	assert.NotContains(t, list.InputSchema, "required",
		"a query parameter that is not required must not be demanded")
}

// TestTheAddressRefusesAMissingPathParameter is the sharpest refusal.
//
// A path parameter left empty does not produce a failed request: it produces a
// DIFFERENT request. "/admin/v1/orders/" reaches the collection, which answers
// confidently about every order — and a model reporting that as the answer to
// "what is order X" is worse than a model told it asked wrongly.
func TestTheAddressRefusesAMissingPathParameter(t *testing.T) {
	t.Parallel()

	tools, err := toolsFrom(sampleDocument())
	require.NoError(t, err)

	var item tool
	for _, tool := range tools {
		if tool.Name == "get_orders_id" {
			item = tool
		}
	}
	require.NotEmpty(t, item.Name)

	_, err = item.requestPath(map[string]any{})
	assert.Error(t, err, "the address was built with no id")

	_, err = item.requestPath(map[string]any{"id": ""})
	assert.Error(t, err, "an empty id names the collection, not the item")

	path, err := item.requestPath(map[string]any{"id": "ord_1"})
	require.NoError(t, err)
	assert.Equal(t, "/admin/v1/orders/ord_1", path)
}

// TestAQueryParameterReachesTheAddress covers the other half.
func TestAQueryParameterReachesTheAddress(t *testing.T) {
	t.Parallel()

	tools, err := toolsFrom(sampleDocument())
	require.NoError(t, err)

	var list tool
	for _, tool := range tools {
		if tool.Name == "listOrders" {
			list = tool
		}
	}
	require.NotEmpty(t, list.Name)

	path, err := list.requestPath(map[string]any{"limit": 10})
	require.NoError(t, err)
	assert.Equal(t, "/admin/v1/orders?limit=10", path)

	bare, err := list.requestPath(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "/admin/v1/orders", bare,
		"an absent optional parameter must not appear in the address at all")
}
