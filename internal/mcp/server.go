package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// The addresses this server reaches inside its own process.
const (
	// schemaPath is where the installation serves its own OpenAPI document.
	schemaPath = "/openapi.json"
	// authHeader carries the server's credential on every in-process request.
	authHeader = "Authorization"
	// contentText is the one content type this server's results carry: the
	// endpoint's own body, handed over as it is.
	contentText = "text"
)

// Options is what a server needs.
type Options struct {
	// Handler is the assembled router — the same one the server would serve.
	// Every tool call goes through it, so every guard the admin surface has
	// applies: identity, scope, quota and the audit log.
	Handler http.Handler
	// Key is the secret key the tool calls present. It is the server's own
	// credential and not an operator's: a model client is a caller, and a caller
	// in this system holds a key.
	Key string
	// In and Out are the client's stream.
	In  io.Reader
	Out io.Writer
	// Log receives what the client never sees. It must not be the client's
	// stream: a log line written to Out would be read as a JSON-RPC frame and
	// take the session down.
	Log *slog.Logger
}

// Validate refuses a server that cannot answer, or that could answer too much.
func (o Options) Validate() error {
	if o.Handler == nil {
		return fmt.Errorf("the mcp server has no handler to ask; nothing would answer a tool call")
	}
	if strings.TrimSpace(o.Key) == "" {
		return fmt.Errorf("the mcp server has no credential; every tool call would be refused " +
			"401 and the model would report the shop as broken")
	}
	if o.In == nil || o.Out == nil {
		return fmt.Errorf("the mcp server has no stream")
	}
	if o.Log == nil {
		return fmt.Errorf("the mcp server has no logger; its diagnostics would go to the " +
			"client's stream and be read as protocol frames")
	}

	return nil
}

// Server answers a model client.
type Server struct {
	opts  Options
	tools []tool
}

// New builds the server and derives its tool list.
//
// The list is derived ONCE, at startup, from the document this process serves.
// Deriving it per request would mean a tool list that changed under a client
// mid-session, and the document cannot change while the process lives: it is
// built from the router, which is assembled once.
func New(ctx context.Context, opts Options) (*Server, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	document, err := fetchDocument(ctx, opts)
	if err != nil {
		return nil, err
	}

	tools, err := toolsFrom(document)
	if err != nil {
		return nil, err
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("the served schema holds no admin read; a server with no tool " +
			"is a client that connects and finds nothing")
	}

	opts.Log.Info("mcp: the tool list was derived from the served schema", "tools", len(tools))

	return &Server{opts: opts, tools: tools}, nil
}

// errSessionOver ends the loop without ending the process.
//
// The three ways a session ends — the context being canceled, the stream
// closing, and a frame that is not JSON — are not failures of this server, and
// returning them as errors would make an ordinary disconnect look like a crash
// in the exit code. They are carried as one sentinel so the loop has a single way
// to stop and [Server.Serve] has a single place that decides what stopping means.
var errSessionOver = errors.New("the mcp session is over")

// Serve reads frames until the stream ends or the context is canceled.
func (s *Server) Serve(ctx context.Context) error {
	if err := s.loop(ctx); err != nil && !errors.Is(err, errSessionOver) {
		return err
	}

	return nil
}

// loop reads and answers frames until one of the three endings.
func (s *Server) loop(ctx context.Context) error {
	decoder := json.NewDecoder(bufio.NewReader(s.opts.In))
	encoder := json.NewEncoder(s.opts.Out)

	for {
		// A canceled context is a clean stop: the operator pressed a key, or the
		// client closed.
		if ctx.Err() != nil {
			return errSessionOver
		}

		var req request
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return errSessionOver
			}
			// A frame that is not JSON ends the session: the decoder cannot find
			// where the broken value stops, so every frame after it would be read
			// at the wrong offset.
			_ = encoder.Encode(failure(nil, codeParseError, "the frame could not be read as JSON"))

			return errSessionOver
		}

		if req.JSONRPC != jsonRPCVersion {
			if err := encoder.Encode(failure(req.ID, codeInvalidRequest,
				"this server speaks JSON-RPC "+jsonRPCVersion)); err != nil {
				return err
			}

			continue
		}

		result := s.dispatch(ctx, req)
		if req.isNotification() {
			// A notification is answered with silence; the client has no id to
			// match a response to.
			continue
		}
		if err := encoder.Encode(result); err != nil {
			return err
		}
	}
}

// dispatch answers one request.
func (s *Server) dispatch(ctx context.Context, req request) response {
	switch req.Method {
	case "initialize":
		return answer(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			// Only tools. This server has no resources, no prompts and no
			// sampling: declaring a capability it does not implement is how a
			// client learns to distrust the whole handshake.
			"capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo":   map[string]any{"name": "gobit", "version": "read-only"},
		})
	case "ping":
		return answer(req.ID, map[string]any{})
	case "tools/list":
		return answer(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.call(ctx, req)
	default:
		return failure(req.ID, codeMethodNotFound,
			"this server answers initialize, ping, tools/list and tools/call")
	}
}

// callParams is the body of a tools/call.
type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// call runs one tool.
func (s *Server) call(ctx context.Context, req request) response {
	var params callParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return failure(req.ID, codeInvalidRequest, "the call's parameters could not be read")
	}

	found, ok := s.lookup(params.Name)
	if !ok {
		return failure(req.ID, codeMethodNotFound, "there is no tool called "+params.Name)
	}

	path, err := found.requestPath(params.Arguments)
	if err != nil {
		// A bad argument is the MODEL's mistake and it is reported as a tool
		// result rather than a protocol error, which is what lets the client show
		// it to the model and let it try again.
		return answer(req.ID, toolFailure(err.Error()))
	}

	status, body, err := s.get(ctx, path)
	if err != nil {
		s.opts.Log.Error("mcp: a tool call could not be made", "tool", params.Name, "error", err)

		return failure(req.ID, codeInternalError, "the tool call could not be made")
	}

	if status >= http.StatusBadRequest {
		// The body is the API's own error envelope, which names the code and the
		// request id. It is handed back as it is: a model that sees
		// "auth_forbidden" can say which privilege is missing, and a summary
		// written here would lose that.
		return answer(req.ID, toolFailure(body))
	}

	return answer(req.ID, map[string]any{
		"content": []map[string]any{{"type": contentText, contentText: body}},
	})
}

// lookup finds a tool by name.
func (s *Server) lookup(name string) (tool, bool) {
	for _, t := range s.tools {
		if t.Name == name {
			return t, true
		}
	}

	return tool{}, false
}

// toolFailure is a result the model can read and act on.
func toolFailure(text string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": contentText, contentText: text}},
	}
}

// get makes one in-process GET and returns the status and the body.
func (s *Server) get(ctx context.Context, path string) (status int, body string, err error) {
	req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if requestErr != nil {
		return 0, "", requestErr
	}
	req.Header.Set(authHeader, "Bearer "+s.opts.Key)

	capture := newCapture()
	s.opts.Handler.ServeHTTP(capture, req)

	return capture.status, capture.body.String(), nil
}

// fetchDocument reads the schema this installation serves.
//
// Through the handler rather than by building a second document: what the tool
// list must describe is what the endpoints ARE, and the document the process
// serves is the one thing that cannot disagree with them. The schema endpoint
// asks for no identity, so the credential is not needed here — it is sent anyway,
// because a request this server makes should look the same whatever it asks for.
func fetchDocument(ctx context.Context, opts Options) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, schemaPath, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set(authHeader, "Bearer "+opts.Key)

	capture := newCapture()
	opts.Handler.ServeHTTP(capture, req)

	if capture.status != http.StatusOK {
		return nil, fmt.Errorf("the installation's schema endpoint answered %d; the tool list "+
			"cannot be derived", capture.status)
	}

	var document map[string]any
	if err := json.Unmarshal(capture.body.Bytes(), &document); err != nil {
		return nil, fmt.Errorf("the served schema could not be read: %w", err)
	}

	return document, nil
}

// capture takes a handler's answer into memory.
//
// httptest.ResponseRecorder is NOT used: that package belongs to the test binary
// and importing it here would carry test helpers into the server binary — a rule
// the GraphQL handler's own capture writer states and internal/arch now enforces
// (D106). The surface needed is three methods.
type capture struct {
	headers http.Header
	status  int
	body    bytes.Buffer
}

// newCapture builds a capture with the status a handler that writes none leaves.
func newCapture() *capture {
	return &capture{headers: http.Header{}, status: http.StatusOK}
}

// Header returns the headers the handler writes into.
func (c *capture) Header() http.Header { return c.headers }

// WriteHeader keeps the status.
func (c *capture) WriteHeader(status int) { c.status = status }

// Write takes the body into memory.
func (c *capture) Write(p []byte) (int, error) { return c.body.Write(p) }
