package api

import (
	"net/http"
	"strconv"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The addresses of a product's revisions (ADR 0221).
const (
	pathProductRevisions       = "/admin/v1/products/{id}/revisions"
	pathProductRevision        = "/admin/v1/products/{id}/revisions/{version}"
	pathProductRevisionRestore = "/admin/v1/products/{id}/revisions/{version}/restore"
)

// revisionSummary is a revision as a listing answers it: without its snapshot.
type revisionSummary struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	// Version numbers the product's revisions from 1.
	Version    int64     `json:"version"`
	RecordedAt time.Time `json:"recorded_at"`
	// Changed names the top-level fields that differ from the revision before.
	Changed []string `json:"changed"`
	// RequestID is the request that made the revision; absent for a job's.
	RequestID *string `json:"request_id,omitempty"`
}

// toRevisionSummary leaves the snapshot out.
func toRevisionSummary(rev models.Revision) revisionSummary {
	return revisionSummary{
		ID: rev.ID, ProductID: rev.ProductID, Version: rev.Version, RecordedAt: rev.RecordedAt,
		Changed: rev.Changed, RequestID: rev.RequestID,
	}
}

// restoreResponse is a restored product and what the revision named that no
// longer stands.
type restoreResponse struct {
	Product adminProduct `json:"product"`
	// Dropped names what the restore left out because it was removed since,
	// each as kind:id with the kind collection, type, tag, category, attribute
	// or option.
	Dropped []string `json:"dropped"`
}

// adminListRevisions lists a product's revisions, newest first and without
// their snapshots (GET /admin/v1/products/{id}/revisions).
func (h *Handler) adminListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListRevisions(r.Context(), id, limit, offset)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	page := service.ListResult[revisionSummary]{
		Items: make([]revisionSummary, 0, len(result.Items)), Count: result.Count,
		Offset: result.Offset, Limit: result.Limit,
	}
	for i := range result.Items {
		page.Items = append(page.Items, toRevisionSummary(result.Items[i]))
	}
	writeList(w, r, page)
}

// adminGetRevision returns one revision with its snapshot
// (GET /admin/v1/products/{id}/revisions/{version}).
func (h *Handler) adminGetRevision(w http.ResponseWriter, r *http.Request) {
	id, version, err := revisionAddress(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	rev, err := h.svc.GetRevision(r.Context(), id, version)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, rev)
}

// adminRestoreRevision writes a revision's content back to the product
// (POST /admin/v1/products/{id}/revisions/{version}/restore).
func (h *Handler) adminRestoreRevision(w http.ResponseWriter, r *http.Request) {
	id, version, err := revisionAddress(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.RestoreRevision(r.Context(), id, version)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	dropped := result.Dropped
	if dropped == nil {
		dropped = []string{}
	}
	writeItem(w, r, http.StatusOK, restoreResponse{Product: toAdminProduct(result.Product), Dropped: dropped})
}

// revisionAddress reads the product and the version a revision is addressed by.
func revisionAddress(r *http.Request) (id string, version int64, err error) {
	id, err = pathParam(r, "id")
	if err != nil {
		return "", 0, err
	}
	raw, err := pathParam(r, "version")
	if err != nil {
		return "", 0, err
	}
	version, err = strconv.ParseInt(raw, 10, 64)
	if err != nil || version < 1 {
		return "", 0, coreerrors.Invalid(codeBadParam, "the version path parameter is a whole number from 1, %q given", raw)
	}
	return id, version, nil
}

// versionPathParameter describes the {version} segment.
func versionPathParameter() openapi.Parameter {
	return openapi.Parameter{
		Name: "version", In: inPath, Required: true,
		Schema:      map[string]any{schemaType: typeInteger, "minimum": 1},
		Description: "The revision's version: 1 is the product's first.",
	}
}

// describeAdminRevisions describes a product's revisions (ADR 0221).
func describeAdminRevisions(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathProductRevisions, openapi.Operation{
		Summary: "Lists a product's revisions, newest first, without their snapshots.",
		Description: "A revision is the product's admin view after a write that changed it: its own " +
			"fields, variants, options, images, tags, categories and attribute values, without " +
			"timestamps. Every write to a product through its own routes, and the schedule's " +
			"publication and archiving, records one; a write that changes nothing records none. " +
			"`changed` names the top-level fields that differ from the revision before, and " +
			"`request_id` the request that made it, the key the audit log names its caller under. " +
			"A product written before revisions began gets its first revision, what it was, " +
			"from its first write since.",
		Parameters: pagingParameters(),
		Responses: map[string]any{
			"200": openapi.Response("The product's revisions", d.List(revisionSummary{})),
		},
	})

	d.Describe(http.MethodGet, pathProductRevision, openapi.Operation{
		Summary:    "Returns one revision of a product with its snapshot.",
		Parameters: []openapi.Parameter{versionPathParameter()},
		Responses: map[string]any{
			"200": openapi.Response("The revision", d.Item(models.Revision{})),
		},
	})

	d.Describe(http.MethodPost, pathProductRevisionRestore, openapi.Operation{
		Summary: "Writes a revision's content back to the product, as a new revision.",
		Description: "The product's own fields come back as the revision had them, a field it did " +
			"not have cleared, with its collection, type, tags, categories and attribute values. " +
			"The status, the schedule, the variants, the options and the images are not " +
			"restored: a status and a schedule have their own writes, and the others are held by " +
			"id elsewhere. What the revision names that was removed since is left out and listed " +
			"in `dropped`; a handle another product has taken since is refused with 409. The " +
			"restore gets the product.updated event an edit gets.",
		Parameters: []openapi.Parameter{versionPathParameter()},
		Responses: map[string]any{
			"200": openapi.Response("The restored product", d.Item(restoreResponse{})),
		},
	})
}
