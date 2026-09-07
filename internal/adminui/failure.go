package adminui

// How the panel answers a failure the operator cannot act on. Both writers here
// are used by nearly every screen — the catalog, the orders, the customers, the
// inventory, the sales report and the edit forms — so they belong to the panel
// rather than to the screen they were first written for. The pair is kept
// together because the choice between them is the point: one reports a request
// that could not be completed, the other a read layer that did not answer or
// was asked for something it does not offer.

import (
	"net/http"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// unexpectedFailure renders the panel's own error page for a failure the
// operator cannot act on, and logs the real cause.
//
// # Why not corehttp.WriteError
//
// That writer produces the framework's JSON envelope, which is right for an API
// endpoint and wrong here: the panel's client is a BROWSER that navigated to
// this path, and answering it with JSON makes the failure unreadable to the one
// person who could do something about it. The split is the same one
// error.gohtml describes — the envelope stands beside the panel's page, it is
// not replaced by it.
//
// The underlying message is NOT shown. The framework treats a non-Internal
// message as client-safe because a service author wrote it, but that promise was
// made about API clients; a panel page is read by an operator who cannot tell a
// leaked connection string from a diagnosis. The real error goes to the log.
func (u *UI) unexpectedFailure(w http.ResponseWriter, r *http.Request, err error, title string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not complete the request",
		"error", err, "path", r.URL.Path)

	u.errorPage(w, r, corehttp.StatusFor(err), title,
		"The request could not be completed. The reason is in the server log.")
}

// catalogFailure turns a read-layer failure into a page the operator can act
// on.
//
// The underlying error is NOT shown: it can carry a provider name, a query or a
// database address. What is shown is what the operator can do about it, and the
// real error goes to the log through the core's error path — the same split
// [corehttp.WriteError] makes for the API.
func (u *UI) catalogFailure(w http.ResponseWriter, r *http.Request, err error, message string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not read the catalog",
		"error", err, "path", r.URL.Path)

	status := http.StatusServiceUnavailable
	hint := message + " The read layer did not answer; a module may not be registered."
	if errors.IsInvalid(err) {
		// An invalid spec is OUR bug, not an outage: the field, filter or link
		// name this package spells no longer matches the provider. Saying
		// "temporarily unavailable" would send the operator to look at the
		// database while the fix is in this file.
		status = http.StatusInternalServerError
		hint = message + " The screen asked the read layer for something it does not offer."
	}

	u.errorPage(w, r, status, "Catalog unavailable", hint)
}
