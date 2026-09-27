package api

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/bdrtr/gobit/core/openapi"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The import's paths (ADR 0205).
const (
	pathAdminProductImports = "/admin/v1/products/imports"
	pathAdminProductImport  = "/admin/v1/products/imports/{id}"
)

// importDTO is an import as the admin API answers it.
type importDTO struct {
	ID          string           `json:"id"`
	Status      string           `json:"status"`
	RowsTotal   int              `json:"rows_total"`
	RowsDone    int              `json:"rows_done"`
	RowsCreated int              `json:"rows_created"`
	RowsUpdated int              `json:"rows_updated"`
	RowsFailed  int              `json:"rows_failed"`
	Errors      []importErrorDTO `json:"errors"`
	CreatedAt   time.Time        `json:"created_at"`
	StartedAt   *time.Time       `json:"started_at,omitempty"`
	FinishedAt  *time.Time       `json:"finished_at,omitempty"`
}

// importErrorDTO is one refused row.
type importErrorDTO struct {
	Row     int    `json:"row"`
	Message string `json:"message"`
}

// toImportDTO converts an import.
func toImportDTO(in models.Import) importDTO {
	out := importDTO{
		ID: in.ID, Status: string(in.Status), RowsTotal: in.RowsTotal, RowsDone: in.RowsDone,
		RowsCreated: in.RowsCreated, RowsUpdated: in.RowsUpdated, RowsFailed: in.RowsFailed,
		Errors: make([]importErrorDTO, 0, len(in.Errors)), CreatedAt: in.CreatedAt,
		StartedAt: in.StartedAt, FinishedAt: in.FinishedAt,
	}
	for _, e := range in.Errors {
		out.Errors = append(out.Errors, importErrorDTO{Row: e.Row, Message: e.Message})
	}

	return out
}

// adminCreateImport POST /admin/v1/products/imports
//
// The body is the CSV file itself, as the export writes it (ADR 0205). It is
// checked whole here and applied by a job, one row at a time; the answer is
// the import to ask about.
func (h *Handler) adminCreateImport(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != mediaCSV {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(codeBadParam,
			"an import's body is a CSV file sent as text/csv"))

		return
	}
	file, err := io.ReadAll(http.MaxBytesReader(w, r.Body, service.MaxImportBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			corehttp.WriteError(r.Context(), w, coreerrors.Invalid(codeBadParam,
				"an import's file is at most %d bytes", service.MaxImportBytes))

			return
		}
		corehttp.WriteError(r.Context(), w, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadParam,
			"the file could not be read"))

		return
	}

	// A file with price columns writes prices too, and an operator allowed the
	// catalog and not the prices must not change them this way (ADR 0207).
	if service.ImportNamesPrices(file) {
		principal, _ := corehttp.PrincipalFromContext(r.Context())
		if !principal.HasScope(scopePricingWrite) {
			corehttp.WriteError(r.Context(), w, coreerrors.Forbidden(corehttp.CodeForbidden,
				"a file with price columns also requires the %q privilege", scopePricingWrite))

			return
		}
	}

	created, err := h.svc.CreateImport(r.Context(), file)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusAccepted, toImportDTO(created))
}

// adminGetImport GET /admin/v1/products/imports/{id}
func (h *Handler) adminGetImport(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	found, err := h.svc.GetImport(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)

		return
	}
	writeItem(w, r, http.StatusOK, toImportDTO(found))
}

// describeAdminImport describes the import's two routes (ADR 0205).
func describeAdminImport(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminProductImports, openapi.Operation{
		Summary: "Takes a CSV file of the catalog for a job to apply.",
		Description: "The body is the file, sent as text/csv, in the columns GET " +
			"/admin/v1/products/export writes; any subset of them works, if it names " +
			"product_id or product_handle. It is refused whole, with 422, when it is not UTF-8 " +
			"or not CSV, when a row is of another width, when a column is unknown or repeated, " +
			"or when it is larger than 32 MiB. Otherwise it is kept, answered with 202, and a " +
			"job applies it a row at a time within the next minutes.\n\n" +
			"A row finds its product by product_id, or else by product_handle, and creates it " +
			"when the handle is new; it finds its variant by variant_id, or else by variant_sku, " +
			"or else by variant_options, and creates it when none matches. An empty cell leaves " +
			"the value as it is. A row that cannot be applied is refused alone and named in the " +
			"import's errors by its line. A row a stopped job applied already runs again and " +
			"finds what it made.\n\n" +
			"A variant_price_<currency> cell sets the variant's base price at one unit in that " +
			"currency, in minor units, through the pricing module, and gives the variant a price " +
			"set when it has none; a quantity tier and a list price keep their amounts. A file " +
			"with price columns also requires pricing:write, and is answered 403 without it and " +
			"422 in an installation without the pricing module (ADR 0207).",
		RequestBody: map[string]any{
			"required": true,
			"content": map[string]any{
				mediaCSV: map[string]any{"schema": map[string]any{schemaType: typeString}},
			},
		},
		Responses: map[string]any{
			"202": openapi.Response("The import, to be applied", d.Item(importDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminProductImport, openapi.Operation{
		Summary:     "Reads where an import stands.",
		Description: "The counts move as the job applies rows; errors keeps the first thousand refused rows.",
		Responses: map[string]any{
			"200": openapi.Response("The import", d.Item(importDTO{})),
		},
	})
}
