package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// --- customers ----------------------------------------------------------------

// adminCreateCustomer creates a REGISTERED customer account
// (POST /admin/v1/customers).
//
// The admin endpoint always opens an ACCOUNT; guest registration is part of the
// storefront flow and is done with POST /store/v1/customers. Had the
// distinction been left to a flag in the body, an admin request would silently
// have fallen outside the uniqueness rule.
func (h *Handler) adminCreateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req customerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateCustomer(ctx, toCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCustomerDTO(created))
}

// adminListCustomers lists customers, filtered and paged
// (GET /admin/v1/customers).
//
// Filters: email, has_account, group_id. The "email" filter returns GUESTS
// TOO; since several guest records can share one e-mail address, the result
// can contain more than one row (see models.Customer).
func (h *Handler) adminListCustomers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	hasAccount, err := boolParam(r, "has_account")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	after, err := afterParam(r, service.CustomerListing, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListCustomers(ctx, service.ListCustomersInput{
		Email:      stringParam(r, "email"),
		HasAccount: hasAccount,
		GroupID:    stringParam(r, "group_id"),
		Limit:      limit,
		Offset:     offset,
		After:      after,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCustomerDTO)
}

// adminGetCustomer returns a single customer (GET /admin/v1/customers/{id}).
func (h *Handler) adminGetCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, err := h.svc.GetCustomer(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// adminUpdateCustomer updates the given fields of the customer
// (PUT /admin/v1/customers/{id}).
func (h *Handler) adminUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateCustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateCustomer(ctx, pathParam(r, paramID), toUpdateCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(updated))
}

// adminDeleteCustomer soft-deletes the customer and its addresses
// (DELETE /admin/v1/customers/{id}).
func (h *Handler) adminDeleteCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCustomer(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminConvertGuest turns a guest record into a registered account
// (POST /admin/v1/customers/{id}/convert-to-account).
//
// It returns 409 if the e-mail address already belongs to a registered account
// or the record already is an account; the classification comes from the
// service, the handler does not choose a status.
func (h *Handler) adminConvertGuest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathParam(r, paramID)

	if err := h.svc.ConvertGuestToAccount(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// The record's CURRENT state after the conversion is returned: the client
	// does not need a second request to see the has_account field.
	customer, err := h.svc.GetCustomer(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// --- groups -------------------------------------------------------------------

// adminCreateGroup creates a new customer group
// (POST /admin/v1/customer-groups).
func (h *Handler) adminCreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req groupRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	group, err := h.svc.CreateGroup(ctx, service.GroupInput{Name: req.Name, Rank: req.Rank, Metadata: req.Metadata})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toGroupDTO(group))
}

// adminListGroups lists the groups, paged
// (GET /admin/v1/customer-groups).
func (h *Handler) adminListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListGroups(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toGroupDTO)
}

// adminGetGroup returns a single group (GET /admin/v1/customer-groups/{id}).
func (h *Handler) adminGetGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	group, err := h.svc.GetGroup(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminUpdateGroup updates the given fields of the group
// (PUT /admin/v1/customer-groups/{id}).
//
// It returns 409 if another live group has the same name; the classification
// comes from the service, the handler does not choose a status.
func (h *Handler) adminUpdateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateGroupRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	group, err := h.svc.UpdateGroup(ctx, pathParam(r, paramID),
		service.UpdateGroupInput{Name: req.Name, Rank: req.Rank, Metadata: req.Metadata})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminDeleteGroup soft-deletes the group
// (DELETE /admin/v1/customer-groups/{id}).
//
// The memberships are not removed, but the deleted group shows up in no read;
// its name also becomes free to use again (see service.Service.DeleteGroup).
func (h *Handler) adminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteGroup(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminAddToGroup adds the customer to the group
// (POST /admin/v1/customer-groups/{id}/customers).
//
// The operation is idempotent; it returns 204 for a customer who already is a
// member too.
func (h *Handler) adminAddToGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req groupMemberRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.AddToGroup(ctx, req.CustomerID, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminRemoveFromGroup removes the customer from the group
// (DELETE /admin/v1/customer-groups/{id}/customers/{customer_id}).
func (h *Handler) adminRemoveFromGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := h.svc.RemoveFromGroup(ctx, pathParam(r, paramCustomerID), pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminListGroupsOfCustomer returns the customer's groups
// (GET /admin/v1/customers/{id}/groups).
func (h *Handler) adminListGroupsOfCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	groups, err := h.svc.ListGroupsOf(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(groups, toGroupDTO))
}

// --- addresses ----------------------------------------------------------------

// adminListAddresses returns the customer's addresses
// (GET /admin/v1/customers/{id}/addresses).
func (h *Handler) adminListAddresses(w http.ResponseWriter, r *http.Request) {
	h.listAddresses(w, r, pathParam(r, paramID))
}

// adminCreateAddress adds a new address for the customer
// (POST /admin/v1/customers/{id}/addresses).
func (h *Handler) adminCreateAddress(w http.ResponseWriter, r *http.Request) {
	h.createAddress(w, r, pathParam(r, paramID))
}

// adminUpdateAddress updates the address
// (PUT /admin/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) adminUpdateAddress(w http.ResponseWriter, r *http.Request) {
	h.updateAddress(w, r, pathParam(r, paramID))
}

// adminDeleteAddress soft-deletes the address
// (DELETE /admin/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) adminDeleteAddress(w http.ResponseWriter, r *http.Request) {
	h.deleteAddress(w, r, pathParam(r, paramID))
}

// adminSetDefaultShipping makes the address the default shipping address
// (POST /admin/v1/customers/{id}/addresses/{address_id}/default-shipping).
//
// The endpoint is the admin-side COUNTERPART of the storefront's. Without it an
// operator would have to re-create an existing address to make it the default
// — the update body carries no flag, and re-creating the address would change
// its identifier.
func (h *Handler) adminSetDefaultShipping(w http.ResponseWriter, r *http.Request) {
	h.setDefaultShipping(w, r, pathParam(r, paramID))
}

// adminSetDefaultBilling makes the address the default billing address
// (POST /admin/v1/customers/{id}/addresses/{address_id}/default-billing).
func (h *Handler) adminSetDefaultBilling(w http.ResponseWriter, r *http.Request) {
	h.setDefaultBilling(w, r, pathParam(r, paramID))
}

// toCustomerInput turns the request body into the service input.
func toCustomerInput(req customerRequest) service.CustomerInput {
	return service.CustomerInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     req.Phone,
		Metadata:  req.Metadata,
	}
}

// toUpdateCustomerInput turns the update body into the service input.
func toUpdateCustomerInput(req updateCustomerRequest) service.UpdateCustomerInput {
	return service.UpdateCustomerInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     req.Phone,
		Metadata:  req.Metadata,
	}
}
