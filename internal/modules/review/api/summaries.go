package api

import (
	"fmt"
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// storeSummaries returns the count and the average of each named product
// (GET /store/v1/review-summaries?product_id=…&product_id=…, ADR 0449).
//
// It is [Handler.storeSummary] for a listing: a page of cards reads its
// ratings in one request and one statement rather than one of each per card.
// The products answer in the order named, a product with no approved review
// counted zero, for storeSummary's reason.
func (h *Handler) storeSummaries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	summaries, err := h.svc.SummarizeProducts(ctx, r.URL.Query()["product_id"])
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	data := make([]summaryDTO, 0, len(summaries))
	for i := range summaries {
		data = append(data, toSummaryDTO(summaries[i]))
	}
	writeItem(w, r, http.StatusOK, data)
}

// describeSummaries records the listing's summary read.
func describeSummaries(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathStoreReviewSummaries, openapi.Operation{
		Summary: "Returns the count and average rating of each of a listing's products.",
		Description: "The review-summary read for a page of product cards: one request and one " +
			"statement for every product named, in the order named, each answered as " +
			"GET /store/v1/products/{product_id}/review-summary answers it — the approved " +
			"reviews only, the average in hundredths, a product with none counted zero. A " +
			"product named twice is answered once. " +
			fmt.Sprintf("Between 1 and %d products are named; more, or an empty id, is ", service.MaxSummaryProducts) +
			"refused with 400.",
		Parameters: []openapi.Parameter{productIDsParameter()},
		Responses: map[string]any{
			"200": openapi.Response("The aggregates, in the order named", d.Item([]summaryDTO{})),
		},
	})
}

// productIDsParameter is the repeated product_id the summaries read takes. It
// is built by [queryParameter], with the name, for the query-parameter audit.
func productIDsParameter() openapi.Parameter {
	parameter := queryParameter("product_id", "array",
		"A product's ID, repeated for each product: product_id=a&product_id=b.")
	parameter.Required = true
	parameter.Schema["items"] = map[string]any{schemaType: typeString}

	return parameter
}
