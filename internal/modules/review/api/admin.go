package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// moderateRequest is the body of an operator's decision.
type moderateRequest struct {
	// Status is where the review is to go: approved or rejected.
	Status string `json:"status"`
	// Note is why; it is required for a rejection.
	Note string `json:"note"`
}

// adminList pages the reviews (GET /admin/v1/reviews).
//
// The MODERATION QUEUE is this endpoint with "?status=submitted", and it is not
// a second path. Two endpoints would be two listings to keep in step — the same
// paging, the same filters, the same DTO — for a difference of one word, and
// the day a filter was added to one of them an operator would find it on the
// queue and not on the archive, or the other way round.
//
// An unknown status is REFUSED rather than answered with an empty page: an
// empty page for a misspelled status reads as "there is nothing waiting", and
// that is the one answer a moderation queue must never give wrongly.
func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, after, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	page, err := h.svc.ListReviews(ctx, models.Filter{
		Status:    stringParam(r, "status"),
		ProductID: stringParam(r, "product_id"),
		Limit:     limit,
		Offset:    offset,
		After:     after,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	data := make([]adminReviewDTO, 0, len(page.Items))
	for i := range page.Items {
		data = append(data, toAdminReviewDTO(page.Items[i]))
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
	})
}

// adminGet returns one review whatever its status
// (GET /admin/v1/reviews/{id}).
//
// It exists for the case the queue cannot serve: a review already decided,
// reached from a link somebody kept — a support ticket, an email from the
// author asking why their words are not on the page. Finding it through the
// listing would mean paging to it.
func (h *Handler) adminGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	review, err := h.svc.GetReview(ctx, reviewID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusOK, toAdminReviewDTO(review))
}

// adminModerate decides about a review (POST /admin/v1/reviews/{id}/status).
//
// # Why one endpoint and not /approve and /reject
//
// Two verb endpoints would carry the SAME body — both decisions take a note —
// and would split one transition table across two handlers. The table is what
// this endpoint enforces, and it has four edges rather than two: an approved
// review may be rejected, which is the only way to take a published review back
// down, and a rejected one may be approved, because a rejection is a person's
// judgement and the author has no way to submit their words a second time.
// Naming the two obvious moves and leaving the two repairs to an unnamed third
// path is how the repairs end up being done in psql.
//
// # It is a POST to a sub-path rather than a PATCH on the review
//
// The review itself is not editable: a shop that could edit the text would be
// publishing words under a customer's byline that the customer did not write.
// What changes is where it stands, and a PATCH would suggest otherwise.
func (h *Handler) adminModerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body moderateRequest
	if err := decode(r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	moved, err := h.svc.Moderate(ctx, reviewID(r), service.ModerateInput{
		To:   models.Status(body.Status),
		Note: body.Note,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusOK, toAdminReviewDTO(moved))
}
