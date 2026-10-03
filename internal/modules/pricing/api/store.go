package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// storeGetPriceSet returns a price set with its prices
// (GET /store/v1/price-sets/{id}).
//
// It is the ONLY pricing endpoint on the store side. The price that reaches the
// customer normally comes from product's store listing, through the Query layer
// (ADR 0004); this endpoint is for clients that want to read the prices of a
// container whose id they already know directly.
//
// There is NO write surface on the store side: changing a price is an
// administration job.
//
// The body carries ONLY displayable prices and does NOT INCLUDE rule
// conditions; its counterpart on the admin surface
// (GET /admin/v1/price-sets/{id}) shows both.
func (a *API) storeGetPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r, "id")

	set, err := a.svc.GetPriceSet(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// The customer surface uses the FILTERED path: draft or expired campaign
	// prices and prices bound to a rule do not go out (see ListStorePrices).
	prices, err := a.svc.ListStorePrices(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStorePriceSetDTO(set, prices))
}
