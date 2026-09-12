package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server is driven the way a client drives it: frames in, frames out.
//
// The handler is a fake, and what it records is what the claim is about — that a
// tool call becomes a real request, at the address the tool named, carrying the
// server's credential.

// recordingHandler answers the schema and records every other request.
type recordingHandler struct {
	document string
	requests []*http.Request
	status   int
	body     string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == schemaPath {
		_, _ = w.Write([]byte(h.document))

		return
	}

	h.requests = append(h.requests, r)
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
	_, _ = w.Write([]byte(h.body))
}

// testDocument is the smallest served schema with one admin read.
const testDocument = `{"paths":{"/admin/v1/orders/{id}":{"get":{"operationId":"getOrder",
"summary":"Reads one order.","parameters":[{"name":"id","in":"path","required":true,
"schema":{"type":"string"}}]}}}}`

// session runs the given frames through a server and returns the answers.
func session(t *testing.T, handler *recordingHandler, frames ...string) []response {
	t.Helper()

	out := &strings.Builder{}
	server, err := New(context.Background(), Options{
		Handler: handler,
		Key:     "sk_test",
		In:      strings.NewReader(strings.Join(frames, "\n") + "\n"),
		Out:     out,
		Log:     slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)
	require.NoError(t, server.Serve(context.Background()))

	var answers []response
	decoder := json.NewDecoder(strings.NewReader(out.String()))
	for {
		var answer response
		if err := decoder.Decode(&answer); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("an answer could not be read: %v", err)
		}
		answers = append(answers, answer)
	}

	return answers
}

// TestTheHandshakeDeclaresOnlyWhatTheServerHas pins initialize.
//
// A capability declared and not implemented is how a client learns to distrust
// the whole handshake: it would ask for resources or prompts and be answered
// "method not found" by a server that had just said it had them.
func TestTheHandshakeDeclaresOnlyWhatTheServerHas(t *testing.T) {
	t.Parallel()

	answers := session(t, &recordingHandler{document: testDocument},
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)

	require.Len(t, answers, 1)
	result, ok := answers[0].Result.(map[string]any)
	require.True(t, ok, "initialize answered no result")

	assert.Equal(t, protocolVersion, result["protocolVersion"])
	capabilities, _ := result["capabilities"].(map[string]any)
	assert.Equal(t, []string{"tools"}, keysOf(capabilities),
		"the server declared a capability it does not implement")
}

// TestTheToolListIsTheDocumentsAdminReads ties the list to the schema.
func TestTheToolListIsTheDocumentsAdminReads(t *testing.T) {
	t.Parallel()

	answers := session(t, &recordingHandler{document: testDocument},
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)

	require.Len(t, answers, 1)
	result, _ := answers[0].Result.(map[string]any)
	tools, _ := result["tools"].([]any)
	require.Len(t, tools, 1, "the list is not the document's one admin read")

	first, _ := tools[0].(map[string]any)
	assert.Equal(t, "getOrder", first["name"])
	assert.Contains(t, first["description"], "Reads one order")
}

// TestAToolCallBecomesARealRequest is the whole point of the server.
//
// Not a description of a request — a request, through the handler this process
// assembled, so every ring the admin surface has applies to it. What is asserted
// is the address the tool named and the credential it carried: a call that
// reached the right path with no key would be refused by the installation, and a
// call with the key at the wrong path would answer confidently about something
// else.
func TestAToolCallBecomesARealRequest(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{document: testDocument, body: `{"data":{"id":"ord_1"}}`}
	answers := session(t, handler,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call",`+
			`"params":{"name":"getOrder","arguments":{"id":"ord_1"}}}`)

	require.Len(t, handler.requests, 1, "the tool call did not reach the handler")
	assert.Equal(t, "/admin/v1/orders/ord_1", handler.requests[0].URL.Path)
	assert.Equal(t, "Bearer sk_test", handler.requests[0].Header.Get(authHeader),
		"the call carried no credential; the installation would refuse it")

	require.Len(t, answers, 1)
	result, _ := answers[0].Result.(map[string]any)
	content, _ := result["content"].([]any)
	require.Len(t, content, 1)
	first, _ := content[0].(map[string]any)
	assert.Contains(t, first["text"], "ord_1", "the answer did not carry the endpoint's body")
}

// TestARefusedCallIsHandedBackAsTheAPISaidIt keeps the diagnosis.
//
// The body is the API's own error envelope, which names the code and the request
// id. A summary written by this server would lose both, and a model that sees
// the code can say which privilege is missing.
func TestARefusedCallIsHandedBackAsTheAPISaidIt(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{
		document: testDocument,
		status:   http.StatusForbidden,
		body:     `{"error":{"code":"auth_forbidden","message":"this operation requires order:read"}}`,
	}
	answers := session(t, handler,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call",`+
			`"params":{"name":"getOrder","arguments":{"id":"ord_1"}}}`)

	require.Len(t, answers, 1)
	assert.Nil(t, answers[0].Error,
		"a refusal by the API is a TOOL result, not a protocol error: the model has to be "+
			"able to read it and say what is missing")

	result, _ := answers[0].Result.(map[string]any)
	assert.Equal(t, true, result["isError"])
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	assert.Contains(t, first["text"], "auth_forbidden")
	assert.Contains(t, first["text"], "order:read")
}

// TestAnUnknownToolAndAnUnknownMethodAreToldApart keeps the two refusals
// distinct.
func TestAnUnknownToolAndAnUnknownMethodAreToldApart(t *testing.T) {
	t.Parallel()

	answers := session(t, &recordingHandler{document: testDocument},
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"nope"}}`)

	require.Len(t, answers, 2)
	require.NotNil(t, answers[0].Error)
	assert.Equal(t, codeMethodNotFound, answers[0].Error.Code)
	require.NotNil(t, answers[1].Error)
	assert.Contains(t, answers[1].Error.Message, "nope")
}

// TestANotificationIsAnsweredWithSilence is the protocol's own rule.
//
// A notification carries no id, so a client has nothing to match an answer to;
// a server that answered one would put an unmatched frame on the stream.
func TestANotificationIsAnsweredWithSilence(t *testing.T) {
	t.Parallel()

	answers := session(t, &recordingHandler{document: testDocument},
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`)

	require.Len(t, answers, 1, "the notification was answered")
	assert.Equal(t, json.RawMessage("1"), answers[0].ID)
}

// TestAServerWithNoCredentialDoesNotOpen is the refusal at startup.
func TestAServerWithNoCredentialDoesNotOpen(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Options{
		Handler: &recordingHandler{document: testDocument},
		In:      strings.NewReader(""),
		Out:     &strings.Builder{},
		Log:     slog.New(slog.DiscardHandler),
	})

	assert.Error(t, err, "a server with no credential opened; every tool call would be 401")
}

// TestAServerWhoseSchemaHoldsNoAdminReadDoesNotOpen refuses an empty list.
//
// A client that connects and finds nothing cannot tell a misconfigured server
// from an installation with no endpoints.
func TestAServerWhoseSchemaHoldsNoAdminReadDoesNotOpen(t *testing.T) {
	t.Parallel()

	_, err := New(context.Background(), Options{
		Handler: &recordingHandler{document: `{"paths":{"/store/v1/products":{"get":{}}}}`},
		Key:     "sk_test",
		In:      strings.NewReader(""),
		Out:     &strings.Builder{},
		Log:     slog.New(slog.DiscardHandler),
	})

	assert.Error(t, err)
}

// keysOf returns a map's keys.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}

	return out
}
