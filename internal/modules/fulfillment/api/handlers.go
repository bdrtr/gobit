package api

import (
	"net/http"
)

// listProviders returns the identifiers of the registered shipping providers.
//
// It is bound ONLY to the admin surface: which carriers the store works with is
// the store's operational information and is not shown to the customer (this is
// the difference from the payment providers — there the customer has to know
// which payment method to choose).
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeList(ctx, w, h.svc.ProviderIDs(ctx))
}
