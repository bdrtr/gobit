package app

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/audit"
	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	corepage "github.com/bdrtr/gobit/internal/core/page"
)

// ScopeAuditRead may read the audit log.
//
// # Why it is its own scope and not admin-only
//
// The audit log is the record of what everyone else did, and reading it is a
// different power from doing any of it. An operator trusted to refund an order
// is not thereby trusted to read the trail of every colleague's actions, and the
// covering "admin" scope still reaches it — this scope exists so that reaching
// it can be granted WITHOUT granting everything else.
//
// It follows the dictionary the rest of the admin surface uses,
// "<resource>:<verb>". There is deliberately no audit:write: rows are written by
// the framework and by nothing else, and a scope that names a power nobody has
// is a name somebody will one day try to use.
const ScopeAuditRead = "audit:read"

// auditLogPath is the audit log's listing.
//
// It is a top-level admin resource rather than something under an existing one,
// because it is about no module: a row records a request to any admin endpoint,
// and hanging the listing off one of them would say the log belonged there.
const auditLogPath = "/admin/v1/audit-log"

// The query parameters the handler reads. There are no others; a parameter the
// schema names and the server ignores is worse than a missing one.
const (
	paramActorID = "actor_id"
	paramPath    = "path"
	paramLimit   = "limit"
	paramAfter   = "after"
)

// The JSON Schema names the parameters below are built from.
//
// They are constants because the linter counts the literals and is right to: a
// name written as "strig" compiles, produces a document, and only surfaces when
// a generated client sends the parameter with the wrong type.
const (
	inQuery     = "query"
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// auditLogListing names this listing inside an opaque cursor.
//
// The name is part of the encoded value, so a cursor from another listing is
// refused rather than silently applied to this one — which would answer a
// question the caller did not ask, from a position that means nothing here.
const auditLogListing = "audit_log"

// auditRecordDTO is one audit row as it goes over the wire.
//
// It is a separate type from [audit.Record] for the reason every other DTO in
// this repository is: the published Go struct and the published JSON body are
// two contracts, and letting one be the other means a Go field rename becoming a
// breaking API change nobody meant to make.
type auditRecordDTO struct {
	// ID is the row's identifier and the first half of its paging position.
	ID string `json:"id"`
	// ActorID and ActorKind are who made the request.
	ActorID   string `json:"actor_id"`
	ActorKind string `json:"actor_kind"`
	// Method and Path are what they called.
	Method string `json:"method"`
	Path   string `json:"path"`
	// Status is what came back.
	Status int `json:"status"`
	// RequestID ties the row to the log lines of the same request.
	RequestID string `json:"request_id"`
	// CreatedAt is when it happened, in UTC.
	CreatedAt time.Time `json:"created_at"`
}

// auditPageDTO is a page of audit rows.
//
// It carries "next_cursor" and NOT "count", "offset" or "limit", and the absence
// is the honest part: paging here is keyset, so there is no offset to report,
// and a total would mean counting the whole log on every request to answer a
// question nobody asked. An empty "next_cursor" means the listing is exhausted.
type auditPageDTO struct {
	// Data is the page, newest first.
	Data []auditRecordDTO `json:"data"`
	// NextCursor is the opaque position the next page starts below; it is absent
	// on the last page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// auditStore resolves a reader over the core pool, or nil.
//
// It returns nil rather than an error on purpose, and the caller binds no route
// in that case. An installation with no database pool has no audit table either,
// and failing startup over a READER that nobody has asked for would turn a
// missing convenience into an outage — the same reasoning the writer already
// applies one step earlier ([corehttp.GuardOptions.Audit] is optional).
func auditStore(c *container.Container, log *slog.Logger) *audit.Store {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		log.Warn("the audit log reader was not bound; the database pool could not be resolved",
			"service", svcDB, "error", err)

		return nil
	}

	return audit.NewStore(pool.Pool())
}

// registerAuditLog binds the audit log's reader.
//
// It is bound at the composition root for the same reason the personal-data
// endpoints are: the store is core, not a module, and no module owns the record
// of what every module's endpoints were asked to do.
//
// The store may be nil — an installation can run without an audit log at all
// (GuardOptions.Audit is optional) — and then the route is not bound. An
// endpoint that exists and answers "there is no log" would be worse: a client
// cannot tell that from "the log is empty", and the two are opposite facts
// during an incident.
func registerAuditLog(router chi.Router, store *audit.Store) {
	if store == nil {
		return
	}

	router.With(corehttp.RequireScope(ScopeAuditRead)).
		Get(auditLogPath, auditLogHandler(store))
}

// auditLogHandler reads a page of the audit log.
func auditLogHandler(store *audit.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		filter, err := auditFilter(r)
		if err != nil {
			corehttp.WriteError(ctx, w, err)

			return
		}

		records, err := store.List(ctx, filter)
		if err != nil {
			corehttp.WriteError(ctx, w, err)

			return
		}

		corehttp.WriteJSON(ctx, w, http.StatusOK, auditPage(records, filter.Limit))
	}
}

// auditFilter reads the listing's parameters off the request.
func auditFilter(r *http.Request) (audit.Filter, error) {
	query := r.URL.Query()

	filter := audit.Filter{
		ActorID: query.Get(paramActorID),
		Path:    query.Get(paramPath),
		Limit:   audit.DefaultLimit,
	}

	if raw := query.Get(paramLimit); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return audit.Filter{}, errors.Wrap(err, errors.KindInvalid, audit.CodeReadFailed,
				"the %s parameter has to be an integer (%q given)", paramLimit, raw)
		}

		filter.Limit = limit
	}

	// The cursor is decoded HERE and not in the store: encoding a position into
	// an opaque string is an HTTP concern, and core/audit may not import the
	// package that does it — a published package may not depend on an internal
	// one. The store's business is a keyset position; what a caller wraps it in
	// is the caller's.
	cursor, err := corepage.Decode(auditLogListing, query.Get(paramAfter))
	if err != nil {
		return audit.Filter{}, err
	}
	if !cursor.IsZero() {
		filter.AfterAt, filter.AfterID = cursor.Time, cursor.ID
	}

	return filter, nil
}

// auditPage renders the records and the position of the next page.
//
// A page SHORTER than the limit is the last one and carries no cursor. That is
// the cheapest correct answer: the alternative is asking for one row more than
// was wanted and throwing it away, which costs a row read on every page to save
// a caller one empty request at the end.
func auditPage(records []audit.Record, limit int) auditPageDTO {
	page := auditPageDTO{Data: make([]auditRecordDTO, 0, len(records))}

	// The index form, not "for _, rec": a Record is wide enough that copying one
	// per row is measurable on a full page, and nothing here needs a copy.
	for i := range records {
		rec := &records[i]
		page.Data = append(page.Data, auditRecordDTO{
			ID:        rec.ID,
			ActorID:   rec.ActorID,
			ActorKind: rec.ActorKind,
			Method:    rec.Method,
			Path:      rec.Path,
			Status:    rec.Status,
			RequestID: rec.RequestID,
			CreatedAt: rec.CreatedAt.UTC(),
		})
	}

	if len(records) == limit && limit > 0 {
		last := records[len(records)-1]
		page.NextCursor = corepage.Encode(auditLogListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return page
}

// describeAuditLog records the audit log's endpoint in the OpenAPI document.
//
// It is called from the composition root because describeAPI walks the module
// registry and this route belongs to no module — the same reason
// describePersonalData is called there.
func describeAuditLog(d *openapi.Doc) {
	d.Describe(http.MethodGet, auditLogPath, openapi.Operation{
		Summary: "Reads the audit log, newest first.",
		Description: "A row records one admin request that CHANGED something: who called " +
			"it, what they called, and what came back. It does not record the change " +
			"itself — the WHAT is read from the record, which carries its own updated_at " +
			"— because a diff would mean every module producing a before-and-after on " +
			"every write, and a bare \"a product was updated\" would be cheaper and worth " +
			"nothing." +
			"\n\n" +
			"Storefront requests are NOT recorded: what authenticates one is a " +
			"publishable key, which names a SALES CHANNEL and not a person, so a row " +
			"there would say \"somebody\" and mean nothing. The customer identity of " +
			"ADR 0043 does not change that: it is resolved by the one module that needs " +
			"it, at the moment it needs it, and never reaches this stack. Reads are " +
			"not recorded either, with ONE exception — this endpoint. Who read the record " +
			"of who did what is the question an incident asks, so a call to this path " +
			"appears in its own listing." +
			"\n\n" +
			"Paging is KEYSET, not offset. Send back the \"next_cursor\" of the previous " +
			"page as \"after\"; when the response carries no cursor the listing is " +
			"exhausted. Offset was refused because an append-only log read newest-first " +
			"is the shape it is worst at: a row written between two requests shifts every " +
			"later page by one, so a reader following an incident silently misses a row " +
			"or sees it twice." +
			"\n\n" +
			"The two filters are the two INDEXES the table carries, and they are the two " +
			"questions it was built for: what did this person do, and what happened to " +
			"this endpoint. \"path\" matches EXACTLY — a prefix filter would need an index " +
			"that does not exist and would invite \"/admin/v1/\" as an argument, which is " +
			"the whole table with extra steps." +
			"\n\n" +
			"Requires the " + ScopeAuditRead + " scope.",
		Parameters: []openapi.Parameter{
			{
				Name: paramActorID, In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Limits the listing to one caller.",
			},
			{
				Name: paramPath, In: inQuery,
				Schema:      map[string]any{schemaType: typeString},
				Description: "Limits the listing to one endpoint path, matched exactly.",
			},
			{
				Name: paramLimit, In: inQuery,
				Schema: map[string]any{
					schemaType: typeInteger, "minimum": 1, "maximum": audit.MaxLimit,
				},
				Description: "Page size; the default is " +
					strconv.Itoa(audit.DefaultLimit) + ". A value above the maximum is " +
					"REFUSED rather than clamped, so a caller never silently receives " +
					"fewer rows than it asked for.",
			},
			{
				Name: paramAfter, In: inQuery,
				Schema: map[string]any{schemaType: typeString},
				Description: "The previous page's \"next_cursor\", sent back exactly as it " +
					"was given. A cursor from another listing is refused rather than " +
					"applied to this one.",
			},
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of audit rows, newest first", d.SchemaOf(auditPageDTO{})),
		},
	})
}
