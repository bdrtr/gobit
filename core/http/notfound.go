package http

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// CodeRouteNotFound is the code a path no route takes is answered with, on
// every surface (ADR 0458).
const CodeRouteNotFound = "route_not_found"

// RouteNotFound answers a path no route takes with a 404 in the error envelope
// every other error is written in (ADR 0458).
//
// chi's own answer is "404 page not found" as text/plain. A route an
// installation leaves unbound, contrib/identity-session's registration without
// a mailer or web-push's subscription without a key, is such a path, and a
// client that reads every error answer as JSON met a body it could not parse.
//
// [NewRouter] sets it as the root's not-found handler, and chi hands it to
// every router mounted under the root that has none of its own. An embedder
// that sets the root's afterwards replaces it.
func RouteNotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(r.Context(), w, coreerrors.NotFound(CodeRouteNotFound,
		"no route answers %s %s", r.Method, r.URL.Path))
}
