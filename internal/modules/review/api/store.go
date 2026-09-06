package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// The customer side WRITES, and reads only what a person has approved.
//
// There is no storefront endpoint that reads a single review by id, and its
// absence is the design rather than an omission: a shopper holding the id
// returned by their own submission would otherwise be able to read a review
// that is still waiting — theirs today, and with a guessed id somebody else's
// tomorrow. The listing carries the guarantee in its SQL; a by-id read would
// have to carry it in a condition somebody could later forget.

// storeSubmitRequest is the body of a customer's review.
//
// It names the product in the PATH and never in the body, so there is one place
// a review's subject can come from. It carries no email address and no order
// id; the reasons are on [service.SubmitInput], and both are decisions this
// module was blocked on rather than fields nobody got round to.
type storeSubmitRequest struct {
	// Rating is the star count, 1 to 5.
	Rating int16 `json:"rating"`
	// Title is the headline; it may be omitted.
	Title string `json:"title"`
	// Body is the review text.
	Body string `json:"body"`
	// AuthorName is the byline to publish the review under.
	AuthorName string `json:"author_name"`
}

// storeSubmit takes a review (POST /store/v1/products/{product_id}/reviews).
//
// # Authorization
//
// There is none, and that is declared rather than hidden. The storefront's only
// principal is the publishable key (ADR 0008 leaves customer identity to the
// embedding application), so this module cannot know who is writing, cannot
// establish that they bought the product, and does not pretend to.
//
// What makes the write acceptable is the same argument the order module writes
// down for its return request, and decision A15 in docs/gaps.md turns it into a
// question anybody can answer per feature: does a human stand between the write
// and its effect? Here one does. The review is stored in
// [models.StatusSubmitted], the storefront listing cannot see it, and the only
// endpoint that publishes it is admin-only and scoped.
//
// # What the response says, and why the id in it is safe to return
//
// The stored review comes back, including its id. That id opens no storefront
// read — there is no by-id store endpoint — so it is an acknowledgement rather
// than a handle, which is what a client needs in order to report "we have it,
// it will appear once it is checked".
func (h *Handler) storeSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body storeSubmitRequest
	if err := decode(r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	submitted, err := h.svc.Submit(ctx, service.SubmitInput{
		ProductID:  productID(r),
		Rating:     body.Rating,
		Title:      body.Title,
		Body:       body.Body,
		AuthorName: body.AuthorName,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusCreated, toStoreReviewDTO(submitted))
}

// storeList pages a product's APPROVED reviews
// (GET /store/v1/products/{product_id}/reviews).
//
// The status is not a parameter of this endpoint and there is no way to make it
// one: the service method it calls reaches SQL that carries the literal. A
// status query parameter — even one that only accepted "approved" — would put
// the module's whole guarantee behind a validation somebody could widen.
func (h *Handler) storeList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, after, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	page, err := h.svc.ListApproved(ctx, productID(r), models.Filter{
		Limit:  limit,
		Offset: offset,
		After:  after,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	data := make([]storeReviewDTO, 0, len(page.Items))
	for i := range page.Items {
		data = append(data, toStoreReviewDTO(page.Items[i]))
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
	})
}

// storeSummary returns the count and the average
// (GET /store/v1/products/{product_id}/review-summary).
//
// It is a SEPARATE endpoint from the listing rather than a field on it, because
// a product page shows the two numbers everywhere — under the title, in a
// listing card, in a filter — and only opens the reviews themselves when
// somebody scrolls. Folding the aggregate into the listing would make every
// page that wants "4.3 (127)" fetch twenty review bodies to get it.
//
// A product with no approved review answers 200 with a count of zero, not 404:
// this module does not know whether the product exists, and saying "not found"
// would be telling a client something about the catalog it is in no position to
// say.
func (h *Handler) storeSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	summary, err := h.svc.Summarize(ctx, productID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusOK, toSummaryDTO(summary))
}
