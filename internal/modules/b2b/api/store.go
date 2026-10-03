package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// The endpoints in this file return the customer's OWN company and OWN
// employee record. Both rest on the same service call (MembershipOfCustomer):
// if the customer is not an employee of a company, both answer 404.
//
// There is NO endpoint called with a company id; the reasoning is in the
// package documentation.
//
// Both resolve the customer through [Handler.storeCustomerID], which refuses
// the request when the installation's bound identity CONTRADICTS the customer
// the path claims (ADR 0057; with none bound it refuses nothing). The call is
// the FIRST thing each handler does, so a refused caller never reaches the
// service — and never learns from a 404 whether the identifier it guessed
// belongs to anybody.

// storeGetCompany returns the customer's own company
// (GET /store/v1/b2b/customers/{customer_id}/company).
func (h *Handler) storeGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	membership, err := h.svc.MembershipOfCustomer(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCompanyDTO(membership.Company))
}

// storeGetEmployee returns the customer's own employee record
// (GET /store/v1/b2b/customers/{customer_id}/employee).
//
// The response carries the spending limit, the interval at which the limit
// resets and the start of the current window; it does NOT carry the REMAINING
// allowance (see storeEmployeeDTO).
func (h *Handler) storeGetEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	membership, err := h.svc.MembershipOfCustomer(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreEmployeeDTO(membership))
}
