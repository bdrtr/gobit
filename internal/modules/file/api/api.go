// Package api is the HTTP surface of the file module.
//
// There are two separate surfaces and the PROTECTION of the two is
// deliberately different: the admin endpoints (/admin/v1/uploads) are
// protected, the file SERVING endpoint (/files/…) is not.
//
// # Why the serving endpoint is NOT UNDER /admin/v1 or /store/v1
//
// The protection stack in this repository (corehttp.APIGuards) covers exactly
// two prefixes: /admin/v1 asks for an identity, /store/v1 asks for a
// publishable key. The address of an uploaded image is plugged straight into
// the product image flow, and the thing that calls that address is an
// <img src="…"> tag on a storefront page. A browser CANNOT ADD A CUSTOM HEADER
// to an image request: neither Authorization nor x-publishable-api-key. So had
// the serving endpoint been put under a protected prefix, every uploaded image
// would return 401 on the storefront — the upload path would technically work
// and would be of no use at all for showing the product.
//
// That is why a separate and UNPROTECTED prefix is used. The limits of the
// trade-off are drawn explicitly:
//
//   - The only thing served is UPLOADED files. The key in the address is asked
//     of the upload ledger first; for a key that has no record the storage is
//     NOT TOUCHED AT ALL (see service.Service.OpenByKey). The endpoint is not a
//     "read a file" endpoint, it is a "serve this record" endpoint.
//   - The key is UNGUESSABLE: it carries 80 bits of cryptographic randomness.
//     So being unprotected does not mean "anybody can list every file" —
//     whoever knows the address can read it, and that is exactly what is
//     published on the storefront anyway. For documents that have to stay
//     secret the right answer is not to protect this endpoint, it is never to
//     put them here at all.
//   - The endpoint is READ ONLY; the write path is in a single place and it is
//     protected.
//
// The accepted price has to be written down as well: /files is OUTSIDE the rate
// limit (the stack covers only the two API prefixes). What is obtained in
// return is that a static file can be served like a static file; the
// alternative would have been to make every image request pay the cost of
// authentication.
//
// # Scopes
//
// The endpoints under /admin/v1 ask for a scope SEPARATELY from the identity:
//
//   - [ScopeRead] ("file:read") — listing.
//   - [ScopeWrite] ("file:write") — uploading and deleting.
//
// corehttp.ScopeAdmin ("admin") is the SUPER SCOPE and satisfies both.
//
// The handlers DO NOT CHOOSE the status code: the service returns a typed
// error and corehttp.WriteError turns it into a status code (plan Section 2.7).
package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// Route paths. Module routes are registered with their FULL PATH; a prefix such
// as "/admin/v1" is NOT MOUNTED, because the first module that mounts one owns
// that whole subtree and would collide with the other modules using the same
// prefix.
const (
	// pathAdminUploads is the upload and listing endpoint.
	pathAdminUploads = "/admin/v1/uploads"
	// pathAdminUpload is the delete endpoint of a single upload.
	pathAdminUpload = "/admin/v1/uploads/{id}"
	// pathFile is the file serving endpoint; the UNPROTECTED prefix is
	// deliberate (see the package documentation).
	pathFile = "/files/{key}"
)

// Path parameters.
const (
	paramID  = "id"
	paramKey = "key"
)

// Query parameters.
const (
	queryLimit  = "limit"
	queryOffset = "offset"
)

// fieldFile is the field name where the file is expected in the multipart body.
const fieldFile = "file"

// codeInvalidRequest is the error code returned when the request body or a
// parameter could not be parsed.
//
// A SINGLE code is enough and that is deliberate: every decision this layer can
// make falls into the same class ("the request is not in the expected shape").
// The content type, the size and the allow list decisions ARE NOT MADE IN THIS
// LAYER; their codes live in the service and that is where the client is really
// going to branch.
const codeInvalidRequest = "file_invalid_request"

// headerContentTypeOptions is the header that TURNS OFF the browser's content
// type guessing.
const headerContentTypeOptions = "X-Content-Type-Options"

// nosniff is the only valid value of the [headerContentTypeOptions] header.
const nosniff = "nosniff"

// The scope vocabulary: the scopes the admin endpoints of file ask for.
//
// The names follow the same pattern in ALL modules ("<module>:read" /
// "<module>:write"); every module inventing its own word would mean that
// whoever hands out the scopes has to memorize a separate vocabulary per
// module.
const (
	// ScopeRead is the scope to READ the upload ledger.
	ScopeRead = "file:read"

	// ScopeWrite is the scope to upload and to delete.
	//
	// Collecting uploading and deleting under the SAME scope is deliberate:
	// both are the life cycle of the same resource, and a role that "can
	// upload but cannot take back what it uploaded" would fill the storage
	// with garbage, because it could not clean up a wrongly uploaded file.
	ScopeWrite = "file:write"
)

// Uploads is the NARROW surface the handler asks of the service.
//
// An interface is used instead of the concrete *service.Service: the HTTP layer
// binds not to the whole service but only to the calls listed here, and the
// handler behavior (multipart parsing, type detection, envelope, serving
// headers) can be exercised without a real database and a real disk.
type Uploads interface {
	// Upload validates the body, has it written to the storage and records it
	// in the ledger.
	Upload(ctx context.Context, in service.UploadInput) (models.Upload, error)
	// ListUploads pages the ledger; the second value is the count of ALL
	// records.
	ListUploads(ctx context.Context, page service.Page) ([]models.Upload, int64, error)
	// DeleteUpload deletes the file and the record; it IS IDEMPOTENT.
	DeleteUpload(ctx context.Context, id string) error
	// OpenByKey opens a file by its storage key so that it can be served.
	OpenByKey(ctx context.Context, key string) (service.OpenedFile, error)
	// MaxUploadBytes is the maximum size of a single upload.
	MaxUploadBytes() int64
}

// Handler holds the HTTP handlers of file.
type Handler struct {
	svc Uploads
}

// New produces a handler working on the given service.
func New(svc Uploads) *Handler { return &Handler{svc: svc} }

// Routes binds the routes of file to the router.
//
// The admin endpoints have two layers of protection and both are needed: the
// IDENTITY (corehttp.RequireAdmin, on the side that builds the router) and the
// SCOPE (here). The serving endpoint has NEITHER and the rationale is in the
// package documentation.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	write.Post(pathAdminUploads, h.createUpload)
	read.Get(pathAdminUploads, h.listUploads)
	write.Delete(pathAdminUpload, h.deleteUpload)

	r.Get(pathFile, h.serveFile)
}

// listUploads is the GET /admin/v1/uploads handler.
func (h *Handler) listUploads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	records, total, err := h.svc.ListUploads(ctx, page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writePage(w, r, records, total, page)
}

// deleteUpload is the DELETE /admin/v1/uploads/{id} handler.
//
// An identifier that does not exist also returns 204: the service is idempotent
// and the rationale is written down there (in short: a delete is a claim about
// an end state, and a retried cleanup flow must not get an error on its second
// round).
func (h *Handler) deleteUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteUpload(ctx, chi.URLParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// itemEnvelope is the envelope of single-item responses (plan Section 8).
type itemEnvelope struct {
	// Data is the body of the single record.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data are the records on the current page.
	Data any `json:"data"`
	// Count is the TOTAL number of records.
	Count int64 `json:"count"`
	// Offset is the applied number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the applied page size.
	Limit int64 `json:"limit"`
}

// writeItem writes a single-item response with its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writePage writes the records with the list envelope.
//
// The Limit in the envelope is NOT the raw value of the request but the value
// the service APPLIED: when no limit is given the service applies its default,
// and the envelope reporting it is what lets the client compute the next page
// correctly.
func writePage(w http.ResponseWriter, r *http.Request, records []models.Upload, total int64, page service.Page) {
	limit := page.Limit
	if limit == 0 {
		limit = service.DefaultLimit
	}

	items := make([]uploadDTO, 0, len(records))
	for i := range records {
		items = append(items, toUploadDTO(records[i]))
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  total,
		Offset: page.Offset,
		Limit:  limit,
	})
}

// pageParams reads the pagination parameters from the query string.
func pageParams(r *http.Request) (service.Page, error) {
	limit, err := intParam(r, queryLimit)
	if err != nil {
		return service.Page{}, err
	}

	offset, err := intParam(r, queryOffset)
	if err != nil {
		return service.Page{}, err
	}

	return service.Page{Limit: limit, Offset: offset}, nil
}

// intParam reads a single numeric query parameter; when it is absent it returns
// zero.
//
// A value that CANNOT BE CONVERTED TO A NUMBER returns an error; silently
// falling back to zero would make the client receive the first page instead of
// the page it asked for.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidRequest,
			"the %q parameter has to be an integer, %q was given", name, raw)
	}

	return value, nil
}
