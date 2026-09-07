package app

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// The personal-data surface's scopes.
//
// They follow the dictionary every other admin endpoint uses —
// "<resource>:<verb>", with "admin" as the covering scope — and "personal-data"
// is the resource even though no module is called that. Hanging them off
// customer:write was refused for the reason the routes are not under
// /admin/v1/customers: none of this is an operation on a customer record, and
// an operator trusted to edit shoppers is not thereby trusted to reach into the
// invoice module's retention decision.
//
// # Why three scopes and not two
//
// The three verbs are three different powers and collapsing any pair would hand
// out one of them by accident. The DECLARATION is a map of the schema and
// contains nobody's data; a DISCLOSURE is one named person's file, assembled
// from every holder at once, and is the most concentrated personal data this
// system can produce; an ERASURE destroys. Reading the map is not permission to
// read a person, and reading a person is not permission to destroy them.
const (
	// ScopePersonalDataRead may read the declaration — what the installation
	// holds, as a map of tables and columns, about nobody in particular.
	ScopePersonalDataRead = "personal-data:read"
	// ScopePersonalDataDisclose may assemble one person's dossier.
	ScopePersonalDataDisclose = "personal-data:disclose"
	// ScopePersonalDataErase may run an erasure.
	ScopePersonalDataErase = "personal-data:erase"
)

// The personal-data endpoints.
//
// The declaration sits at the ROOT of the space and the two acts hang below it,
// which is the other way round from the first version of this surface: that one
// put the declaration at /admin/v1/erasure/personal-data, nesting a map of the
// schema underneath a destructive verb it has nothing to do with. The
// declaration is the subject; erasing and disclosing are things done with it.
const (
	// personalDataPath lists what every holder says it keeps.
	personalDataPath = "/admin/v1/personal-data"
	// erasurePath runs a sweep for one person.
	erasurePath = "/admin/v1/personal-data/erasure"
	// disclosurePath assembles one person's dossier.
	disclosurePath = "/admin/v1/personal-data/disclosure"
)

// erasureRequest is the body of the sweep endpoint.
//
// Both fields are optional and at least one is required, which is the contract
// core/personaldata.Subject documents: an invoice can only be found by the address
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

// declarationsDTO is the whole declaration, and it is NOT a page.
//
// The list envelope every other admin listing uses requires count, offset and
// limit; this answer has none of those because it is not paginated and could not
// meaningfully be. It is a fixed, complete map of the installation — the same on
// an empty database — so it comes back whole, and describing it with the paged
// envelope would have documented three fields the endpoint never writes.
type declarationsDTO struct {
	// Data is every holder's declaration.
	Data []declarationDTO `json:"data"`
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
func registerErasure(c *container.Container, router chi.Router, mods []module.Module) (*datasubject.Coordinator, error) {
	co, err := datasubject.FromContainer(c, mods)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"the erasure coordinator could not be set up")
	}

	router.With(corehttp.RequireScope(ScopePersonalDataRead)).
		Get(personalDataPath, personalDataHandler(co))
	router.With(corehttp.RequireScope(ScopePersonalDataErase)).
		Post(erasurePath, eraseHandler(co))
	router.With(corehttp.RequireScope(ScopePersonalDataDisclose)).
		Post(disclosurePath, discloseHandler(co))

	return co, nil
}

// fieldDTO is one personal value on one disclosed record.
type fieldDTO struct {
	// Column is the column it came from.
	Column string `json:"column"`
	// Kind is "named" when gobit wrote the person there itself and "open" when
	// the content is the embedder's — which is what tells a reader which values
	// the framework can vouch for.
	Kind string `json:"kind"`
	// Value is what is stored.
	Value any `json:"value"`
}

// recordDTO is one row a holder found about the subject.
type recordDTO struct {
	// Table is the table the row lives in.
	Table string `json:"table"`
	// ID identifies the row when the holder can name it.
	ID string `json:"id,omitempty"`
	// Fields are the personal values on the row.
	Fields []fieldDTO `json:"fields"`
}

// disclosureDTO is one holder's answer.
type disclosureDTO struct {
	// Holder names who answered.
	Holder string `json:"holder"`
	// State is "disclosed", "nothing" or "unresolvable".
	State string `json:"state"`
	// Records are what was found.
	Records []recordDTO `json:"records,omitempty"`
	// Why explains a state that is not "disclosed".
	Why string `json:"why,omitempty"`
}

// dossierDTO is the whole answer to a disclosure request.
type dossierDTO struct {
	// Subject echoes who the request was about.
	Subject erasureRequest `json:"subject"`
	// Parts is one entry per holder that was asked.
	Parts []disclosureDTO `json:"parts"`
	// At is when the request ran, in UTC.
	At string `json:"at"`
	// Incomplete names every holder that could not answer, and is ABSENT when
	// the dossier is whole.
	//
	// It is a field rather than a status code because of what is being weighed.
	// A failed erasure must not be reported as a success, so that handler
	// refuses the whole thing; a failed disclosure still contains the parts that
	// worked, and those are the person's own data — throwing them away helps
	// nobody and invites an operator to retry until they get a green light they
	// will then trust. So the answer comes back with the hole NAMED in it, and
	// whoever forwards a dossier carrying this field forwards a document that
	// says out loud what is missing from it.
	Incomplete []string `json:"incomplete,omitempty"`
}

// discloseHandler assembles one person's dossier.
//
// It answers 200 even when a holder failed, which is the deliberate opposite of
// [eraseHandler] and the reasoning is on [dossierDTO.Incomplete]. What it must
// never do is answer 200 with a dossier that looks whole, and the Incomplete
// field is what keeps that from happening.
func discloseHandler(co *datasubject.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req erasureRequest
		if err := decodeJSONBody(r, &req); err != nil {
			corehttp.WriteError(ctx, w, err)

			return
		}

		dossier, err := co.Disclose(ctx, personaldata.Subject{CustomerID: req.CustomerID, Email: req.Email})
		if err != nil && len(dossier.Parts) == 0 {
			// Nothing came back at all — a refused subject, or every holder
			// down. There is no half to preserve, so the error decides.
			corehttp.WriteError(ctx, w, err)

			return
		}

		out := dossierDTO{
			Subject: erasureRequest{CustomerID: dossier.Subject.CustomerID, Email: dossier.Subject.Email},
			Parts:   make([]disclosureDTO, 0, len(dossier.Parts)),
			At:      dossier.At.Format(timeFormatRFC3339),
		}

		for _, part := range dossier.Parts {
			out.Parts = append(out.Parts, disclosureFromDomain(part))
		}

		if err != nil {
			out.Incomplete = []string{err.Error()}
		}

		corehttp.WriteJSON(ctx, w, http.StatusOK, out)
	}
}

// disclosureFromDomain turns one holder's answer into the wire shape.
func disclosureFromDomain(part personaldata.Disclosure) disclosureDTO {
	records := make([]recordDTO, 0, len(part.Records))

	for _, rec := range part.Records {
		fields := make([]fieldDTO, 0, len(rec.Fields))
		for _, f := range rec.Fields {
			fields = append(fields, fieldDTO{Column: f.Column, Kind: string(f.Kind), Value: f.Value})
		}

		records = append(records, recordDTO{Table: rec.Table, ID: rec.ID, Fields: fields})
	}

	return disclosureDTO{
		Holder:  part.Holder,
		State:   string(part.State),
		Records: records,
		Why:     part.Why,
	}
}

// eraseHandler runs one sweep and answers with the report.
//
// A PARTIAL sweep is an error and is reported as one: when a holder fails, the
// coordinator returns both the report and an error, and this handler lets the
// error decide the status code. Answering 200 with a report that silently omits
// the holder that failed is the one outcome that must not happen, because the
// controller would repeat it to the data subject as though it were complete.
func eraseHandler(co *datasubject.Coordinator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var req erasureRequest
		if err := decodeJSONBody(r, &req); err != nil {
			corehttp.WriteError(ctx, w, err)
			return
		}

		report, err := co.Erase(ctx, personaldata.Subject{CustomerID: req.CustomerID, Email: req.Email})
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
func personalDataHandler(co *datasubject.Coordinator) http.HandlerFunc {
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

		corehttp.WriteJSON(ctx, w, http.StatusOK, declarationsDTO{Data: out})
	}
}

// reportDTO turns the coordinator's report into the wire shape.
func reportDTO(report personaldata.Report) erasureReportDTO {
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

// describePersonalData writes the three personal-data endpoints into the
// OpenAPI document.
//
// # Why it is called by hand
//
// [describeAPI] builds the document by walking the module registry and asking
// every module that implements openapi.Describer. These three routes belong to
// no module — that is the whole argument for binding them at the composition
// root — so nothing would ever ask about them, and they would enter the
// document as bare paths with no body, no response and no scope. An endpoint
// that hands back the most concentrated personal data in the system is the last
// one that should be undocumented.
func describePersonalData(d *openapi.Doc) {
	d.Describe(http.MethodGet, personalDataPath, openapi.Operation{
		Summary: "Lists what personal data this installation holds.",
		Description: "The DECLARATION: every holder's tables and columns, with what each one keeps " +
			"about a person and whether gobit wrote it there itself (kind \"named\") or the content " +
			"is the embedder's (kind \"open\"). It is about NOBODY in particular — the answer is the " +
			"same on an empty installation, which is what makes it usable for a privacy notice " +
			"before the first order. Scope: " + ScopePersonalDataRead + ".",
		Responses: map[string]any{
			"200": openapi.Response("What every holder declares", d.Item(declarationsDTO{})),
		},
	})

	d.Describe(http.MethodPost, disclosurePath, openapi.Operation{
		Summary: "Assembles one person's dossier.",
		Description: "Asks every holder what it keeps about the subject and returns it as one " +
			"document. A holder answers \"disclosed\", \"nothing\" (it searched and this person is " +
			"not there) or \"unresolvable\" (it keeps personal data and cannot tell whose) — the " +
			"three are kept apart because an empty list would let the last hide inside the second. " +
			"A holder that could not answer at all is named in \"incomplete\", and a dossier " +
			"carrying that field is NOT whole. The result is material a controller reviews before " +
			"sending, not a document to forward unread: it contains free-form values gobit never " +
			"inspects. The subject travels in the BODY and never in the path, because the audit log " +
			"records request paths and declares them personal data. Scope: " +
			ScopePersonalDataDisclose + ".",
		RequestBody: d.RequestBody(erasureRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The person's dossier", d.Item(dossierDTO{})),
		},
	})

	d.Describe(http.MethodPost, erasurePath, openapi.Operation{
		Summary: "Erases one person across every holder.",
		Description: "Each holder answers \"deleted\", \"anonymized\" or \"retained\", and a " +
			"retained answer says WHAT was kept and WHY so a controller can repeat it. gobit never " +
			"rewrites a free-form column, so an anonymized answer NAMES the open columns it left. " +
			"Unlike the disclosure, a partial sweep is an ERROR: an erasure reported as complete " +
			"when it is not would be a false statement made to the person who asked. Scope: " +
			ScopePersonalDataErase + ".",
		RequestBody: d.RequestBody(erasureRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("What each holder did", d.Item(erasureReportDTO{})),
		},
	})
}
