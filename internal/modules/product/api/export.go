package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/bdrtr/gobit/core/openapi"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// pathAdminProductExport is where the catalog leaves as CSV (ADR 0204).
const pathAdminProductExport = "/admin/v1/products/export"

// scopePricingRead is the pricing module's read scope, spelled as its API
// spells it. The export carries prices, so it takes both reads: an operator
// allowed the catalog and not the prices must not get the prices this way.
const scopePricingRead = "pricing:read"

// exportPageDeadline is how long each page of an export may take to write.
// The server's write timeout covers one ordinary response; an export moves it
// forward page by page instead.
const exportPageDeadline = 30 * time.Second

// adminExportProducts GET /admin/v1/products/export
//
// It answers with a CSV file, one row per variant (ADR 0204). The rows are
// streamed page by page, so the headers go out with the first page. An error
// before that is an ordinary error response; one after it drops the
// connection, because a file that stopped in the middle would otherwise read
// as a whole catalog.
func (h *Handler) adminExportProducts(w http.ResponseWriter, r *http.Request) {
	var opts service.ExportOptions
	if raw := stringParam(r, "status"); raw != nil {
		status := models.Status(*raw)
		if !status.Valid() {
			corehttp.WriteError(r.Context(), w, coreerrors.Invalid(codeBadParam,
				"%q is not a product status", *raw))

			return
		}
		opts.Status = &status
	}

	started := false
	controller := http.NewResponseController(w)
	afterPage := func() error {
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		err := controller.SetWriteDeadline(time.Now().Add(exportPageDeadline))
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}

		return nil
	}

	if err := h.svc.ExportProducts(r.Context(), csvResponse(w, &started), opts, afterPage); err != nil {
		if !started {
			corehttp.WriteError(r.Context(), w, err)

			return
		}
		panic(http.ErrAbortHandler)
	}
}

// writerFunc is a function that is an io.Writer.
type writerFunc func(p []byte) (int, error)

// Write calls the function.
func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// csvResponse is the response the export writes to: it sends the headers with
// the first bytes and records that it did, so an error after them is not
// answered with a body the client would read as part of the file.
func csvResponse(w http.ResponseWriter, started *bool) writerFunc {
	return func(p []byte) (int, error) {
		if !*started {
			header := w.Header()
			header.Set("Content-Type", "text/csv; charset=utf-8")
			header.Set("Content-Disposition", `attachment; filename="products.csv"`)
			header.Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			*started = true
		}

		return w.Write(p)
	}
}

// describeAdminExport describes the export (ADR 0204).
//
// The answer is a file and not the JSON envelope, so the response is written
// out here rather than with openapi.Response, which describes JSON alone.
func describeAdminExport(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminProductExport, openapi.Operation{
		Summary: "Exports the catalog as CSV, one row per variant.",
		Description: "The file is streamed page by page, newest product first, and a product " +
			"created while it runs is not in it. A product with no variant is one row with the " +
			"variant columns empty. Tag and category ids are joined with \"|\"; metadata and a " +
			"variant's options (option title to value) are JSON. A text cell that starts with " +
			"=, +, - or @ is prefixed with an apostrophe so a spreadsheet does not run it.\n\n" +
			"After the fixed columns comes one variant_price_<currency> column per currency a " +
			"region sells in, holding the variant's base price at one unit in minor units — " +
			"the price the catalog's price filter compares (ADR 0041) — and empty when there " +
			"is none. It takes pricing:read as well as product:read, because it carries prices. " +
			"An error before the first row is an ordinary error response; one after it drops " +
			"the connection, so a partial file cannot pass for a whole one.",
		Parameters: []openapi.Parameter{
			queryParameter("status", typeString,
				"Exports only the products in this status: draft | published | archived."),
		},
		Responses: map[string]any{
			"200": map[string]any{
				schemaDescription: "The catalog as CSV",
				"content": map[string]any{
					"text/csv": map[string]any{"schema": map[string]any{schemaType: typeString}},
				},
			},
		},
	})
}
