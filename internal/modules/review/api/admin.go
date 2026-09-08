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

	suggested, unsuggested := suggestionFilter(r)

	page, err := h.svc.ListReviews(ctx, models.Filter{
		Status:      stringParam(r, "status"),
		ProductID:   stringParam(r, "product_id"),
		Suggested:   suggested,
		Unsuggested: unsuggested,
		Limit:       limit,
		Offset:      offset,
		After:       after,
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

// suggestionNone is the value of ?suggested= that asks for the reviews NO model
// has been asked about.
//
// It is a reserved word rather than a second parameter because a client asking
// about the proposal asks ONE question — what does the model say about this
// review — and "nothing yet" is one of its answers. Two parameters would let a
// client send both and mean nothing.
//
// The word is safe because it is not a status and cannot become one: the
// column's CHECK admits "approved" and "rejected", the module's own transition
// table adds "submitted", and "none" is none of the three. It lives HERE and
// nowhere else — the service and the SQL below it take a value and a flag, so
// the word never reaches them.
const suggestionNone = "none"

// suggestionFilter reads ?suggested= and turns it into the pair the service
// takes.
//
// An unrecognized value is passed THROUGH rather than dropped, so the service
// refuses it with a message naming the two words a proposal can carry. Dropping
// it here would answer a misspelled filter with the unfiltered queue — a page
// full of reviews the model has said nothing about, under a heading that says
// it is showing what the model flagged.
func suggestionFilter(r *http.Request) (suggested *string, unsuggested bool) {
	value := stringParam(r, "suggested")
	if value == nil {
		return nil, false
	}
	if *value == suggestionNone {
		return nil, true
	}

	return value, false
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
