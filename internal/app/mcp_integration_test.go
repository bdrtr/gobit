//go:build integration

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/mcp"
)

// The tool list has to be THIS installation's, and that cannot be shown against
// a hand-written document.
//
// internal/mcp's own tests pin the derivation — which operations become tools,
// what they are called, what they accept — against a document written there. What
// they cannot say is that the document the server derives from is the one the
// installation serves. That needs a real assembly: the modules registered, the
// routes bound, the schema generated from the router tree.

// TestTheToolListIsThisInstallationsOwnSchema is the end-to-end claim.
func TestTheToolListIsThisInstallationsOwnSchema(t *testing.T) {
	ctx := context.Background()

	cfg, err := config.Load()
	require.NoError(t, err)

	log := slog.New(slog.DiscardHandler)

	// publishesOnly, like the verb: a test that consumed would take messages
	// from anything else running against the same bus (ADR 0160).
	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{}, publishesOnly)
	require.NoError(t, err, "the installation the mcp server needs could not be opened")
	defer closeApp()

	handler, err := assemble(ctx, cfg, log, app, Options{})
	require.NoError(t, err)

	out := &strings.Builder{}
	server, err := mcp.New(ctx, mcp.Options{
		Handler: handler,
		Key:     "sk_not_a_real_key",
		In:      strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"),
		Out:     out,
		Log:     log,
	})
	require.NoError(t, err,
		"the server could not derive a tool list from this installation's schema")
	require.NoError(t, server.Serve(ctx))

	var answer struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.String()), &answer))

	tools := answer.Result.Tools
	require.GreaterOrEqualf(t, len(tools), 80,
		"the installation's schema produced %d tools and it carries well over a hundred "+
			"admin reads. A smaller number means the derivation stopped seeing the document, "+
			"and an empty-ish tool list is a server that connects and answers nothing",
		len(tools))

	byName := map[string]bool{}
	for _, tool := range tools {
		byName[tool.Name] = true

		assert.NotEmptyf(t, tool.Description, "the %q tool carries no description", tool.Name)
		assert.Equalf(t, "object", tool.InputSchema["type"],
			"the %q tool's input schema is not an object; a client cannot fill it", tool.Name)
	}

	// Two endpoints this installation certainly has. The names are PATH-derived,
	// and measuring taught that: not one operation in this document carries an
	// operationId, so every tool is named from its address. That makes a tool's
	// name move when its endpoint moves — which is the honest failure, because a
	// tool that survived an endpoint's move would answer about something else.
	for _, expected := range []string{"get_admin_v1_orders", "get_admin_v1_products_id"} {
		assert.Containsf(t, byName, expected,
			"the tool list has no %q. It is derived from the served schema, so a missing "+
				"tool means the endpoint moved or the derivation stopped reading the "+
				"document", expected)
	}

	// A name is how a model addresses a tool, so two tools sharing one is a call
	// that reaches whichever the lookup finds first. The derivation replaces the
	// path's separators and strips its braces, which is exactly the kind of
	// flattening that can make two addresses meet.
	assert.Lenf(t, byName, len(tools),
		"two tools share a name: %d tools produced %d distinct names. A model calling "+
			"the shared name reaches whichever the lookup finds first",
		len(tools), len(byName))
}
