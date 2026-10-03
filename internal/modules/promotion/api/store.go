package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// storeGetPromotion validates a coupon code
// (GET /store/v1/promotions/{code}).
//
// It is the ONLY promotion endpoint on the store side and it ONLY reads; using
// a coupon (incrementing the counter) is the job of the admin and order flows.
//
// # What it RETURNS
//
// The coupon's code and the discount's type/target/value. This much is
// required: a customer cannot apply a code to their cart without seeing what
// the code they typed does.
//
// # What it does NOT return
//
// The promotion's STATUS, the usage counter, the campaign id and budget, the
// metadata and the RULE CONDITIONS. A rule's right-hand side (e.g. a customer
// group's id or a segment list) is business information.
//
// # Why no reason is given
//
// If the code does not exist, if the promotion is draft/inactive, if its
// campaign's window is closed, if its budget is exhausted or if its usage
// allowance has run out, the SAME 404 is returned. Were a distinction made,
// someone guessing codes could read an unpublished campaign calendar off an
// answer like "this code exists but its campaign has not started yet".
//
// Whether the coupon actually produces a discount in THIS CART is a separate
// question, and its answer is the cart total itself: rule conditions can only
// be evaluated with the cart's context, and evaluating them here would give
// away the EXISTENCE of the condition.
func (a *API) storeGetPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	coupon, err := a.svc.LookupStoreCoupon(ctx, pathID(r, "code"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreCouponDTO(coupon))
}
