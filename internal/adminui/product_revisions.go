package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// A product's history (ADR 0316): the revisions the product module records
// for every write that changed its admin view (ADR 0221), and the restore of
// one, read and written through the product module's panel surface.

// ProductRevisionsPath lists a product's revisions, and
// ProductRevisionRestorePath restores one.
const (
	ProductRevisionsPath       = ProductPath + "/revisions"
	ProductRevisionRestorePath = ProductRevisionsPath + "/{revision}/restore"
)

// revisionsPerPage is the history's page size, the other lists'.
const revisionsPerPage = 25

// The restore's outcome travels to the history page in its address, so a
// reload shows it again rather than restoring twice.
const (
	paramRestored = "restored"
	paramDropped  = "dropped"
)

// RevisionReader is the narrow surface a product's history is read through.
type RevisionReader interface {
	// RevisionsJSON lists the product's revisions newest first, a page at a
	// time, with their total.
	RevisionsJSON(ctx context.Context, productID string, limit, offset int32) (json.RawMessage, int64, error)
}

// RevisionRestorer is the narrow surface a revision is restored through.
type RevisionRestorer interface {
	// RestoreRevision writes the revision back as a new one, refused when the
	// product moved past the version read, and names what it left out.
	RestoreRevision(ctx context.Context, productID string, revision, version int64) ([]string, error)
}

// revisionRow is one revision as the surface sends it; the json tags are the
// contract with that surface, exercised end to end.
type revisionRow struct {
	Version    int64     `json:"version"`
	RecordedAt time.Time `json:"recorded_at"`
	Changed    []string  `json:"changed"`
	RequestID  *string   `json:"request_id"`
}

// showRevisions renders the product's history.
func (u *UI) showRevisions(w http.ResponseWriter, r *http.Request) {
	u.renderRevisions(w, r, http.StatusOK, "")
}

// restoreRevision restores the revision in the path at the version the page
// was read at, and returns to the history saying what was left out; a
// refusal, a product written since included, is drawn on the history.
func (u *UI) restoreRevision(w http.ResponseWriter, r *http.Request) {
	restorer, ok := u.products.(RevisionRestorer)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "History unavailable",
			"The product module's admin surface cannot restore a revision in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	revision, err := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
	if err != nil {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such revision.")
		return
	}
	version, err := strconv.ParseInt(r.PostFormValue(fieldVersion), 10, 64)
	if err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request",
			"The form does not say which version of the product it was opened at; open the history again.")
		return
	}

	dropped, err := restorer.RestoreRevision(r.Context(), id, revision, version)
	switch {
	case err == nil:
		outcome := url.Values{paramRestored: {strconv.FormatInt(revision, 10)}, paramDropped: dropped}
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+id+"/revisions?"+outcome.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err) || errors.IsPreconditionFailed(err):
		u.renderRevisions(w, r, http.StatusUnprocessableEntity, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The revision could not be restored")
	}
}

// renderRevisions reads the product and its revisions and writes the history,
// with a refused restore's reason. An operator who may restore and not read
// is told the reason alone (ADR 0260).
func (u *UI) renderRevisions(w http.ResponseWriter, r *http.Request, code int, refused string) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeProductRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	reader, ok := u.products.(RevisionReader)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "History unavailable",
			"The product module's admin surface cannot read a product's revisions in this installation.")
		return
	}

	id := chi.URLParam(r, "id")
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{fieldID, fieldTitle, fieldVersion},
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return
	}
	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such product.")
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * revisionsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	raw, total, err := reader.RevisionsJSON(r.Context(), id, revisionsPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The revisions could not be read")
		return
	}
	var rows []revisionRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The revisions could not be read")
		return
	}

	_, canRestore := u.products.(RevisionRestorer)
	data := map[string]any{
		titleKey:        "History of " + recordString(records[0], fieldTitle),
		"ProductID":     id,
		productsPathKey: ProductsPath,
		"Version":       recordInt(records[0], fieldVersion),
		"Revisions":     rows,
		totalKey:        total,
		"CanRestore":    canRestore && principal.HasScope(scopeProductWrite),
		refusedKey:      refused,
		"Restored":      r.URL.Query().Get(paramRestored),
		"Dropped":       r.URL.Query()[paramDropped],
	}
	addPaging(data, page, int64(page*revisionsPerPage) < total, ProductsPath+"/"+id+"/revisions")

	u.templates.render(w, r, code, "product_revisions.gohtml", data)
}
