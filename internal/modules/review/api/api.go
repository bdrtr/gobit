// Package api is the review module's HTTP surface.
//
// # Two audiences, and the line between them is the module
//
// The storefront may WRITE a review and may READ the approved ones. It cannot
// read a review that is waiting, cannot read one that was refused, and cannot
// move anything. Everything that ACTS — the moderation queue and the decision
// itself — is under /admin/v1 and behind a scope.
//
// That shape is not a preference, it is what decision A15 in docs/gaps.md asks
// for. The storefront write is accepted from a party this framework cannot
// identify, and the argument for accepting it is the one the order module's
// return request already makes: the write moves nothing, and a person has to
// act before it has any effect.
//
// # What the storefront prefix does and does not give this endpoint
//
// Measured rather than assumed. A route under /store/v1 gets, from
// [github.com/bdrtr/gobit/core/http.APIGuards]: the publishable-key identity
// check, the idempotency ring, and ONE rate limit — a single
// [github.com/bdrtr/gobit/core/http.RateLimit] scoped to the whole prefix.
// There is no per-route quota anywhere in this repository, so a review
// submission draws on the same bucket as every product read, and the key is the
// request's connection address: X-Forwarded-For is only read when
// TRUSTED_PROXY_HOPS is set, which it is not by default, so behind any proxy or
// CDN the whole storefront shares ONE bucket. With RATE_LIMIT_PER_MINUTE at or
// below zero the limiter is not attached at all.
//
// So the honest statement is that this endpoint has a global ceiling and no
// protection of its own. What keeps a flood of submissions off the shop's
// product pages is not the quota — it is that nothing is published until an
// operator approves it, which is the same sentence the rest of this module
// rests on.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// codeInvalidRequest is returned when a body or a parameter cannot be read.
const codeInvalidRequest = "review_invalid_request"

// The scopes that open this module's ADMIN endpoints.
//
// No scope is put on the store endpoints: the identity of /store/v1 is the
// publishable key and that key by definition carries no scope, so a scope there
// would be a condition no storefront client could ever satisfy.
const (
	// ScopeRead opens the moderation queue.
	ScopeRead = "review:read"
	// ScopeWrite opens the decision.
	//
	// Approving and rejecting share one scope rather than splitting into
	// "review:approve" and "review:reject". The split would describe a division
	// of labor no shop has: an operator trusted to publish a stranger's words
	// on the shop's own product page is trusted to decline them, and the
	// reverse is not a role either. A scope nobody would grant separately only
	// makes the grant table longer.
	ScopeWrite = "review:write"
)

// The endpoint paths.
const (
	// pathStoreProductReviews is the storefront write and the storefront read.
	//
	// It is nested under the product because the product is the SUBJECT and the
	// storefront always arrives holding one. A flat /store/v1/reviews would
	// have to take the product from the body on the way in and from a query
	// parameter on the way out, which is the same identifier in two shapes.
	pathStoreProductReviews = "/store/v1/products/{product_id}/reviews"
	// pathStoreProductReviewSummary is the count and the average.
	pathStoreProductReviewSummary = "/store/v1/products/{product_id}/review-summary"
	// pathAdminReviews is the moderation queue.
	pathAdminReviews = "/admin/v1/reviews"
	// pathAdminReview is the single-review read.
	pathAdminReview = "/admin/v1/reviews/{id}"
	// pathAdminReviewStatus is the decision.
	pathAdminReviewStatus = "/admin/v1/reviews/{id}/status"
)

// maxBodyBytes bounds the request body.
//
// It is one order of magnitude above [service.MaxBodyLen] counted as 4-byte
// runes, so a review at the length limit fits with its envelope and anything
// materially larger is refused before it is decoded rather than after.
const maxBodyBytes = 64 << 10

// Reviews is the surface the handler needs from the service.
//
// It is declared HERE, on the consumer's side, so the handler depends on the
// six methods it calls rather than on the whole service. The storefront's two
// reads are separate methods from the admin listing, which is what keeps "the
// storefront sees only approved reviews" checkable at this boundary too.
type Reviews interface {
	Submit(ctx context.Context, in service.SubmitInput) (models.Review, error)
	ListApproved(
		ctx context.Context, productID string, filter models.Filter,
	) (service.Page, error)
	Summarize(ctx context.Context, productID string) (models.Summary, error)
	GetReview(ctx context.Context, id string) (models.Review, error)
	ListReviews(ctx context.Context, filter models.Filter) (service.Page, error)
	Moderate(ctx context.Context, id string, in service.ModerateInput) (models.Review, error)
}

// Handler serves the review endpoints.
type Handler struct {
	svc Reviews
}

// New builds the handler.
func New(svc Reviews) *Handler { return &Handler{svc: svc} }

// Routes mounts the module's endpoints on the router.
func (h *Handler) Routes(r chi.Router) {
	// --- Store API (customer) ---
	r.Post(pathStoreProductReviews, h.storeSubmit)
	r.Get(pathStoreProductReviews, h.storeList)
	r.Get(pathStoreProductReviewSummary, h.storeSummary)

	// --- Admin API (operator) ---
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminReviews, h.adminList)
	r.With(corehttp.RequireScope(ScopeRead)).Get(pathAdminReview, h.adminGet)
	r.With(corehttp.RequireScope(ScopeWrite)).Post(pathAdminReviewStatus, h.adminModerate)
}

// listEnvelope is the envelope of paginated responses.
type listEnvelope struct {
	// Data holds the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the applied page size.
	Limit int64 `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

// itemEnvelope is the envelope of single responses.
type itemEnvelope struct {
	Data any `json:"data"`
}

// storeReviewDTO is what a SHOPPER sees of a review.
//
// It carries no status, no moderation note and no moderation moment. Every
// review a shopper can reach is approved — that is the listing's guarantee —
// so a status field would print the same word on every row, and the note is an
// operator's sentence about a stranger's text that was never written to be
// published.
type storeReviewDTO struct {
	ID         string    `json:"id"`
	ProductID  string    `json:"product_id"`
	Rating     int16     `json:"rating"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	AuthorName string    `json:"author_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// adminReviewDTO is what an OPERATOR sees.
type adminReviewDTO struct {
	ID         string `json:"id"`
	ProductID  string `json:"product_id"`
	Rating     int16  `json:"rating"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	AuthorName string `json:"author_name"`
	Status     string `json:"status"`
	// ModeratedAt is absent while the review is still waiting, rather than
	// present as the zero instant: a review dated year one reads as a data
	// fault, and "not yet" is what the row actually says.
	ModeratedAt    *time.Time `json:"moderated_at,omitempty"`
	ModerationNote string     `json:"moderation_note"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// summaryDTO is the aggregate a product page shows.
type summaryDTO struct {
	ProductID string `json:"product_id"`
	// Count is how many APPROVED reviews the product has.
	Count int64 `json:"count"`
	// AverageHundredths is the mean rating times 100 — 433 means 4.33 stars.
	// It is an integer for the reason money is one here: a float would make the
	// printed number depend on where it happened to be rounded.
	AverageHundredths int64 `json:"average_hundredths"`
}

// toStoreReviewDTO converts a review for a shopper.
func toStoreReviewDTO(in models.Review) storeReviewDTO {
	return storeReviewDTO{
		ID:         in.ID,
		ProductID:  in.ProductID,
		Rating:     in.Rating,
		Title:      in.Title,
		Body:       in.Body,
		AuthorName: in.AuthorName,
		CreatedAt:  in.CreatedAt,
	}
}

// toAdminReviewDTO converts a review for an operator.
func toAdminReviewDTO(in models.Review) adminReviewDTO {
	out := adminReviewDTO{
		ID:             in.ID,
		ProductID:      in.ProductID,
		Rating:         in.Rating,
		Title:          in.Title,
		Body:           in.Body,
		AuthorName:     in.AuthorName,
		Status:         in.Status.String(),
		ModerationNote: in.ModerationNote,
		CreatedAt:      in.CreatedAt,
		UpdatedAt:      in.UpdatedAt,
	}
	if !in.ModeratedAt.IsZero() {
		moment := in.ModeratedAt
		out.ModeratedAt = &moment
	}

	return out
}

// toSummaryDTO converts an aggregate.
func toSummaryDTO(in models.Summary) summaryDTO {
	return summaryDTO{
		ProductID:         in.ProductID,
		Count:             in.Count,
		AverageHundredths: in.AverageHundredths,
	}
}

// writeItem writes a single record inside its envelope.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// decode reads the request body.
//
// An unknown field is REJECTED: a client that writes "authorName" learns what
// it did instead of watching the field be silently ignored and the review be
// stored with an empty byline.
func decode(r *http.Request, into any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "the request body cannot be empty")
		}

		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be read")
	}

	return nil
}

// intParam reads a numeric query parameter; an absent one is zero.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidRequest,
			"the %q parameter has to be a whole number, %q was given", name, raw)
	}

	return value, nil
}

// stringParam reads an optional string filter; an absent one is nil.
//
// The empty string and "not given" are kept apart: an empty status filter is a
// value the client sent by mistake, and turning it into "no filter" would hide
// the mistake behind a queue that looks full of everything.
func stringParam(r *http.Request, name string) *string {
	values, ok := r.URL.Query()[name]
	if !ok || len(values) == 0 {
		return nil
	}

	return &values[0]
}

// afterParam reads the cursor of the page being asked for.
//
// An offset alongside it is REFUSED: a cursor and an offset each name a
// position, and honoring both would serve the page N rows past the cursor,
// which neither of them asked for.
func afterParam(r *http.Request, offset int64) (corepage.Cursor, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return corepage.Cursor{}, nil
	}
	if offset != 0 {
		return corepage.Cursor{}, coreerrors.Invalid(codeInvalidRequest,
			`"after" and "offset" name two different positions; send one of them`)
	}

	return corepage.Decode(service.ReviewListing, raw)
}

// pageParams reads the three paging inputs every listing here takes.
func pageParams(r *http.Request) (limit, offset int64, after corepage.Cursor, err error) {
	if limit, err = intParam(r, "limit"); err != nil {
		return 0, 0, corepage.Cursor{}, err
	}
	if offset, err = intParam(r, "offset"); err != nil {
		return 0, 0, corepage.Cursor{}, err
	}
	if after, err = afterParam(r, offset); err != nil {
		return 0, 0, corepage.Cursor{}, err
	}

	return limit, offset, after, nil
}

// productID reads the review's subject from the path.
func productID(r *http.Request) string { return chi.URLParam(r, "product_id") }

// reviewID reads the review identifier from the path.
func reviewID(r *http.Request) string { return chi.URLParam(r, "id") }
