// Package mcp serves this installation's admin reads to a model client.
//
// # What it is
//
// A read-only Model Context Protocol server. It answers three methods —
// initialize, tools/list and tools/call — and its tool list is DERIVED from the
// OpenAPI document the installation itself serves, so a tool exists exactly when
// an endpoint does. No list is kept here and none can go stale.
//
// # What it is not
//
// It writes nothing. The tools are the GET operations under the admin prefix and
// a call is an in-process GET; there is no method here that reaches a POST, and
// a credential carrying the superior privilege is refused before the server
// opens (see [Options.Validate]).
//
// # The transport
//
// Newline-delimited JSON on a reader and a writer, which is what a stdio MCP
// client speaks. encoding/json already reads one value and skips the whitespace
// between values, so the framing is the decoder itself.
package mcp

import "encoding/json"

// The JSON-RPC 2.0 fields this server speaks.
const (
	// jsonRPCVersion is the only version accepted; a request naming another is
	// refused rather than guessed at.
	jsonRPCVersion = "2.0"
	// protocolVersion is the MCP revision this server implements.
	protocolVersion = "2024-11-05"
)

// The JSON-RPC error codes used here; they are the specification's own.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInternalError  = -32603
)

// request is one JSON-RPC call.
//
// ID is a RawMessage rather than a typed value because the specification allows
// a string, a number or null, and a server that retyped it would answer a
// different id than it was asked under — which a client matches on.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the request expects no answer.
//
// A JSON-RPC notification carries no id, and answering one is a protocol error
// rather than a courtesy: the client has no id to match the answer to.
func (r request) isNotification() bool { return len(r.ID) == 0 }

// response is one JSON-RPC answer.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the error member of an answer.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// answer builds a successful response.
func answer(id json.RawMessage, result any) response {
	return response{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

// failure builds an error response.
//
// The message is the one the client shows its user, so it says what to do rather
// than what happened: a model client's operator sees this text and nothing else.
func failure(id json.RawMessage, code int, message string) response {
	return response{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: message}}
}
