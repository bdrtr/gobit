package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/logger"
	"github.com/bdrtr/gobit/internal/mcp"
)

// mcpCommand is the verb that answers a model client.
const mcpCommand = "mcp"

// The error codes of the mcp command.
const (
	codeMCPFailed  = "cli_mcp_failed"
	codeMCPNoKey   = "cli_mcp_no_key"
	codeMCPTooWide = "cli_mcp_key_too_wide"
)

// mcpKeyEnv names the credential the tool calls present. It is spelled here for
// the REFUSALS, which have to tell an operator which variable to set; the value
// itself is read through the configuration like every other setting.
const mcpKeyEnv = "MCP_API_KEY"

// runMCP serves the model client on stdin and stdout.
//
// # Why a verb and not an endpoint
//
// An HTTP transport would put every JSON-RPC frame through the admin surface's
// rings: each one audited, each one against the installation's rate limit, and
// the endpoint itself in the OpenAPI document — where the server would list
// ITSELF as a tool. A stdio server is what the protocol's own authorization
// section expects anyway: it takes its credential from the environment and needs
// no OAuth metadata.
//
// # Why the whole application is opened
//
// Because a tool call is a real request. It goes through the router this process
// assembles — the same one `serve` would serve — so identity, scope, quota and
// the audit log all apply, and a tool cannot reach what an admin client could
// not. Nothing here is re-implemented, which is the reason the tool list is
// derived from the document this process serves rather than written down.
//
// The assembly publishes only (ADR 0160): it must not take a message the running
// server is owed.
func runMCP(args []string, out io.Writer, opts Options) error {
	if len(args) > 0 {
		return errors.Invalid(codeMCPFailed,
			"%s takes no arguments; it reads JSON-RPC on stdin", mcpCommand)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	key := strings.TrimSpace(cfg.MCPAPIKey)
	if key == "" {
		return errors.Invalid(codeMCPNoKey,
			"%s needs a secret key in %s; every tool call would be refused and the model "+
				"would report the installation as broken", mcpCommand, mcpKeyEnv)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The logs go to STDERR. Stdout is the client's stream and a log line written
	// there would be read as a JSON-RPC frame, which ends the session.
	log := logger.New(logger.Options{
		Level:     cfg.SlogLevel(),
		Format:    cfg.LogFormat,
		AddSource: !cfg.IsProduction(),
	})

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), opts, publishesOnly)
	if err != nil {
		return err
	}
	defer closeApp()

	handler, err := assemble(ctx, cfg, log, app, opts)
	if err != nil {
		return err
	}

	if err := refuseAWideKey(ctx, handler, key); err != nil {
		return err
	}

	server, err := mcp.New(ctx, mcp.Options{
		Handler: handler,
		Key:     key,
		In:      os.Stdin,
		Out:     out,
		Log:     log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeMCPFailed,
			"the mcp server could not be opened")
	}

	return server.Serve(ctx)
}

// refuseAWideKey stops a read-only server holding a credential that can write.
//
// The key's privileges are the ONLY thing that makes this server read-only at
// the surface: the tool list is built from GET operations, and a key carrying the
// superior scope would let any future method — or a mistake in the derivation —
// reach a write. So the server asks the installation what the key carries, and
// refuses to open when the answer is "everything".
//
// It asks rather than assumes: a key's scopes live in its row, and the endpoint
// that reports them is the same one any admin client would use.
func refuseAWideKey(ctx context.Context, handler http.Handler, key string) error {
	scopes, err := keyScopes(ctx, handler, key)
	if err != nil {
		return err
	}

	for _, scope := range scopes {
		if scope == corehttp.ScopeAdmin {
			return errors.Invalid(codeMCPTooWide,
				"the key in %s carries the %q scope, which covers every privilege there is. "+
					"A read-only server must not hold a credential that can write; mint a key "+
					"with the read scopes the questions need", mcpKeyEnv, corehttp.ScopeAdmin)
		}
	}

	return nil
}

// keyScopes asks the installation what the credential carries.
//
// Through the same in-process router a tool call uses, and through the same
// endpoint an admin client would ask: the answer is the identity the guard ring
// resolved, so a key that does not authenticate at all is reported here rather
// than at the model's first question.
func keyScopes(ctx context.Context, handler http.Handler, key string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, identityPath, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)

	capture := &identityCapture{headers: http.Header{}, status: http.StatusOK}
	handler.ServeHTTP(capture, req)

	if capture.status != http.StatusOK {
		return nil, errors.Unauthorized(codeMCPNoKey,
			"the key in %s was refused by this installation (%d); the model would see every "+
				"question fail", mcpKeyEnv, capture.status)
	}

	var envelope struct {
		Data struct {
			Scopes []string `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(capture.body.Bytes(), &envelope); err != nil {
		return nil, errors.Internal(codeMCPFailed, "the identity answer could not be read")
	}

	return envelope.Data.Scopes, nil
}

// identityPath is where a caller reads back its own identity.
const identityPath = "/admin/v1/auth/me"

// identityCapture takes the identity answer into memory; see the mcp package's
// own capture for why httptest is not used (D106).
type identityCapture struct {
	headers http.Header
	status  int
	body    bytes.Buffer
}

// Header returns the headers the handler writes into.
func (c *identityCapture) Header() http.Header { return c.headers }

// WriteHeader keeps the status.
func (c *identityCapture) WriteHeader(status int) { c.status = status }

// Write takes the body into memory.
func (c *identityCapture) Write(p []byte) (int, error) { return c.body.Write(p) }
