package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// The JSON Schema names that appear in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and it only surfaces when the client that reads the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// Describe records the invoice endpoints into the OpenAPI document.
//
// It is a package-level function and it lives here for the same reasons the
// other modules' do: the bodies being described are this package's unexported
// DTOs, and the code that knows which query parameter is really read is in
// api.go — a description kept elsewhere would drift from it silently.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminInvoices, openapi.Operation{
		Summary: "Issues a document and gives it its number.",
		Description: "The number is taken from the series of the given prefix and the CURRENT " +
			"year, inside the same transaction that writes the document: within a series the " +
			"numbers run without a gap and without a repeat. The series row for a new year is " +
			"opened automatically — the year rolls over at midnight and the first sale of the " +
			"year must not wait for a human. " +
			"The totals in the body are CHECKED against the lines rather than derived from " +
			"them: a caller that lost a line would otherwise send a document that adds up " +
			"perfectly and is missing a row. " +
			"An issued document is IMMUTABLE; a mistake is corrected with a cancellation and a " +
			"new document.",
		RequestBody: d.RequestBody(issueRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The issued document", d.Item(invoiceDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminInvoices, openapi.Operation{
		Summary: "Lists the documents with paging.",
		Description: "The rows of a document are NOT in the listing: a page of documents with " +
			"all their rows would be an N+1 and a body nobody needs in that shape. They come " +
			"with the single-document endpoint.",
		Parameters: []openapi.Parameter{
			queryParameter("status", typeString,
				"Status filter: "+statusesText()+"."),
			queryParameter("kind", typeString, "Kind filter: sale, refund."),
			queryParameter("limit", typeInteger,
				"Page size; when it is not given the service's default applies."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
			queryParameter("after", typeString,
				"Opaque cursor from a previous page's \"next_cursor\". Cheaper than \"offset\" "+
					"for deep pages: offset makes the database walk and DISCARD every row it "+
					"skips, so its cost grows with depth, while a cursor becomes an index "+
					"condition and stays flat. \"after\" and \"offset\" name two different "+
					"positions and are REFUSED together. When the response carries no "+
					"\"next_cursor\" the listing is exhausted."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of documents",
				d.List(invoiceDTO{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, pathAdminInvoice, openapi.Operation{
		Summary: "Returns one document with its rows.",
		Responses: map[string]any{
			"200": openapi.Response("The document", d.Item(invoiceDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminStatus, openapi.Operation{
		Summary: "Moves the document to another status.",
		Description: "It is a POST to a sub-path rather than a PATCH on the document, because " +
			"the document itself is immutable: what changes is where it STANDS, and a PATCH " +
			"would suggest the amounts could be edited too. " +
			"A rejection and a cancellation require a reason — they are the two states a person " +
			"later has to account for. The move is decided by the database as well as by the " +
			"service, so two operators acting at the same moment cannot both win.",
		RequestBody: d.RequestBody(statusRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The moved document", d.Item(invoiceDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminInvoiceSerie, openapi.Operation{
		Summary: "Lists the number series.",
		Description: "It shows which series are open, which year each belongs to and how far " +
			"each has gone. A series opened by a typo in the configured prefix appears here " +
			"with its counter at 1, which is the only place that mistake is visible.",
		Responses: map[string]any{
			"200": openapi.Response("The series", d.Item([]seriesDTO{})),
		},
	})
}

// statusesText writes the valid statuses into the document text.
//
// The list is not written by hand: the status set lives in the models package
// and a status added there has to enter the document as well. A hand-written
// list would mean the added status silently dropping out of it.
func statusesText() string {
	statuses := []models.Status{
		models.StatusIssued, models.StatusSent, models.StatusAccepted,
		models.StatusRejected, models.StatusCanceled,
	}

	out := ""

	for i, status := range statuses {
		if i > 0 {
			out += ", "
		}

		out += status.String()
	}

	return out
}

// queryParameter defines a parameter read from the query string.
//
// There is no "required" flag because this module has no required query
// parameter: a listing without a filter is meaningful.
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}
