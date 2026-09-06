package app

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/internal/workflows/erasing"
)

// The erasure surface's scopes.
//
// They follow the dictionary every other admin endpoint uses —
// "<resource>:<verb>", with "admin" as the covering scope — and "erasure" is
// the resource even though no module is called that. The alternative was to
// hang the endpoints off customer:write, and it was refused for the same reason
// the route is not under /admin/v1/customers: erasing a person is not an
// operation on a customer record, and an operator trusted to edit shoppers is
// not thereby trusted to reach into the invoice module's retention decision.
const (
	// ScopeErasureRead may read what personal data the installation holds.
	ScopeErasureRead = "erasure:read"
	// ScopeErasureWrite may run an erasure.
	ScopeErasureWrite = "erasure:write"
)

// The erasure endpoints.
const (
	// erasurePath runs a sweep for one person.
	erasurePath = "/admin/v1/erasure"
	// personalDataPath lists what every holder says it keeps.
	personalDataPath = "/admin/v1/erasure/personal-data"
)

// erasureRequest is the body of the sweep endpoint.
//
// Both fields are optional and at least one is required, which is the contract
// core/erasure.Subject documents: an invoice can only be found by the address
// printed on it, and a guest's order carries an e-mail with no customer id.
type erasureRequest struct {
	// CustomerID is the customer module's identifier for the person.
	CustomerID string `json:"customer_id"`
	// Email is the address the person gave.
	Email string `json:"email"`
}

// erasureResultDTO is one holder's answer as it goes over the wire.
type erasureResultDTO struct {
	// Holder names who answered — a module name, or the package path of a store
	// that is not a module.
	Holder string `json:"holder"`
	// Outcome is "deleted", "anonymized" or "retained".
	Outcome string `json:"outcome"`
	// Rows is how many rows the holder touched.
	Rows int `json:"rows"`
	// Kept names what may still hold the person, as "table.column".
	Kept []string `json:"kept,omitempty"`
	// Why explains what Kept lists.
	Why string `json:"why,omitempty"`
}

// erasureReportDTO is the whole answer.
type erasureReportDTO struct {
	// Subject echoes who the request was about, so the report is readable on
	// its own once it has been filed away.
	Subject erasureRequest `json:"subject"`
	// Results is one entry per holder that was asked.
	Results []erasureResultDTO `json:"results"`
	// At is when the sweep ran, in UTC.
	At string `json:"at"`
}

// holdingDTO is one declared place a holder keeps personal data.
type holdingDTO struct {
	// Table is the table.
	Table string `json:"table"`
	// Column is the column.
	Column string `json:"column"`
	// Kind is "named" when gobit itself writes a person there and "open" when
	// the content is the embedder's.
	Kind string `json:"kind"`
	// Why says what it holds about the person.
	Why string `json:"why"`
}

// declarationDTO is one holder's declaration.
type declarationDTO struct {
	// Holder names the module or store.
	Holder string `json:"holder"`
	// Holdings is every place it keeps personal data; an empty list is a real
	// answer and means "none".
	Holdings []holdingDTO `json:"holdings"`
}

// registerErasure builds the erasure coordinator and binds its two endpoints.
//
// It is bound HERE, at the composition root, and not in a module's api package.
// The reason is not tidiness: no module owns this sweep. Binding it on the
// customer module would make that module publish the invoice module's retention
// decision, and it would put the endpoint under /admin/v1/customers/{id}, which
// asserts something measurably false — the subject of an erasure need not have
// a customer record at all. An invoice carries the buyer's address and nothing
// that points back at a customer, and an order placed by a guest carries an
// e-mail with a NULL customer_id.
//
// The precedent for a cross-cutting surface bound from the root is
// [registerPanel], which mounts a tree that is neither core nor a module, and
// the /openapi.json route, which is bound on the router directly for the same
// kind of reason.
func registerErasure(c *container.Container, router chi.Router, mods []module.Module) (*erasing.Coordinator, error) {
	co, err := erasing.FromContainer(c, mods)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the erasure coordinator could not be set up")
	}

	read := router.With(corehttp.RequireScope(ScopeErasureRead))
	write := router.With(corehttp.RequireScope(ScopeErasureWrite))

	read.Get(personalDataPath, personalDataHandler(co))
	write.Post(erasurePath, eraseHandler(co))

	return co, nil
}

// eraseHandler runs one sweep and answers with the report.
//
// A PARTIAL sweep is an error and is reported as one: when a holder fails, the
// coordinator returns both the report and an error, and this handler lets the
// error decide the status code. Answering 200 with a report that silently omits
// the holder that failed is the one outcome that must not happen, because the
// controller would repeat it to the data subject as though it were complete.
func eraseHandler(co *erasing.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req erasureRequest
		if err := decodeJSONBody(r, &req); err != nil {
			corehttp.WriteError(ctx, w, err)
			return
		}

		report, err := co.Erase(ctx, erasure.Subject{CustomerID: req.CustomerID, Email: req.Email})
		if err != nil {
			corehttp.WriteError(ctx, w, err)
			return
		}

		corehttp.WriteJSON(ctx, w, http.StatusOK, reportDTO(report))
	}
}

// personalDataHandler answers with what every holder declares.
//
// It is a READ and it takes no subject: the declaration is a property of the
// code rather than of anybody's data, so the answer is the same on an empty
// installation. That is what makes it useful — an embedder can publish its
// privacy notice from it before a single order exists.
func personalDataHandler(co *erasing.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		declared := co.PersonalData()
		out := make([]declarationDTO, 0, len(declared))

		for _, d := range declared {
			holdings := make([]holdingDTO, 0, len(d.Holdings))
			for _, h := range d.Holdings {
				holdings = append(holdings, holdingDTO{
					Table:  h.Table,
					Column: h.Column,
					Kind:   string(h.Kind),
					Why:    h.Why,
				})
			}

			out = append(out, declarationDTO{Holder: d.Holder, Holdings: holdings})
		}

		corehttp.WriteJSON(ctx, w, http.StatusOK, map[string]any{"data": out})
	}
}

// reportDTO turns the coordinator's report into the wire shape.
func reportDTO(report erasure.Report) erasureReportDTO {
	results := make([]erasureResultDTO, 0, len(report.Results))
	for _, res := range report.Results {
		results = append(results, erasureResultDTO{
			Holder:  res.Holder,
			Outcome: string(res.Outcome),
			Rows:    res.Rows,
			Kept:    res.Kept,
			Why:     res.Why,
		})
	}

	return erasureReportDTO{
		Subject: erasureRequest{CustomerID: report.Subject.CustomerID, Email: report.Subject.Email},
		Results: results,
		At:      report.At.Format(timeFormatRFC3339),
	}
}

// timeFormatRFC3339 is the instant format the rest of the API uses.
const timeFormatRFC3339 = "2006-01-02T15:04:05Z07:00"

// erasureBodyLimit bounds the request body.
//
// The body carries at most two short strings, so the limit is small on purpose:
// this endpoint is reachable by anyone holding an admin token, and a limit that
// matches the shape of the data is one fewer thing to reason about.
const erasureBodyLimit = 4 << 10

// decodeJSONBody reads the request body into v.
//
// An EMPTY body is accepted and leaves v at its zero value, so the refusal a
// caller sees for a request that names nobody comes from the coordinator with
// its explanation, rather than from a decoder saying "unexpected end of JSON".
func decodeJSONBody(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, erasureBodyLimit))
	if err != nil {
		return errors.Invalid("erasure_body_unreadable", "the request body could not be read: %v", err)
	}

	if len(body) == 0 {
		return nil
	}

	if err := json.Unmarshal(body, v); err != nil {
		return errors.Invalid("erasure_body_invalid", "the request body is not valid JSON: %v", err)
	}

	return nil
}
