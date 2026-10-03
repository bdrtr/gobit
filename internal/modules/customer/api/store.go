package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// The endpoints in this file manage the customer's OWN profile and addresses.
//
// Every handler below that names a customer resolves it through
// [Handler.storeCustomerID], which refuses the request unless the bound
// identity proves the customer the path claims (ADR 0043). The call is the
// FIRST thing each handler does — before the body is decoded — so a refusal
// cannot be mistaken for a malformed body, and an unidentified caller never
// reaches the service with a parsed address in hand.

// storeRegisterGuest opens a guest customer record (POST /store/v1/customers).
//
// An earlier guest record or a registered account under the same e-mail
// address is NOT an obstacle: a guest record is not an identity but the contact
// details of a one-off purchase (see models.Customer for the reasoning). Opening
// an account will come in Phase 8, together with the auth module; until then a
// guest is turned into an account with
// POST /admin/v1/customers/{id}/convert-to-account.
func (h *Handler) storeRegisterGuest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req customerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	guest, err := h.svc.RegisterGuest(ctx, toCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCustomerDTO(guest))
}

// storeGetCustomer returns the customer's own profile
// (GET /store/v1/customers/{id}).
func (h *Handler) storeGetCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	customer, err := h.svc.GetCustomer(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// storeUpdateCustomer updates the shopper's own profile
// (PUT /store/v1/customers/{id}), every field but the e-mail address, which
// the storefront cannot prove a shopper owns (ADR 0376).
func (h *Handler) storeUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	var req storeUpdateCustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateCustomer(ctx, customerID, service.UpdateCustomerInput{
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     req.Phone,
		Metadata:  req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(updated))
}

// The six handlers below share a five-line preamble and it is written out six
// times rather than factored into a helper that takes the body as a function
// value. That is not an oversight: a writer handed to a function VALUE cannot
// be followed by the audit that keeps every error response going through the
// core's writer (see internal/arch, TestErrorResponsesAreWrittenInOnePlace),
// and an unscannable error path is worth more than five saved lines. What
// guards against one of the six drifting is not this comment but the route walk
// in identity_test.go, which drives every registered storefront route naming a
// customer and refuses to be told how many there are.

// storeListAddresses returns the customer's addresses
// (GET /store/v1/customers/{id}/addresses).
func (h *Handler) storeListAddresses(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.listAddresses(w, r, customerID)
}

// storeCreateAddress adds a new address for the customer
// (POST /store/v1/customers/{id}/addresses).
func (h *Handler) storeCreateAddress(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.createAddress(w, r, customerID)
}

// storeUpdateAddress updates the customer's address
// (PUT /store/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) storeUpdateAddress(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.updateAddress(w, r, customerID)
}

// storeDeleteAddress soft-deletes the customer's address
// (DELETE /store/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) storeDeleteAddress(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.deleteAddress(w, r, customerID)
}

// storeSetDefaultShipping makes the address the default shipping address
// (POST /store/v1/customers/{id}/addresses/{address_id}/default-shipping).
func (h *Handler) storeSetDefaultShipping(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.setDefaultShipping(w, r, customerID)
}

// storeSetDefaultBilling makes the address the default billing address
// (POST /store/v1/customers/{id}/addresses/{address_id}/default-billing).
func (h *Handler) storeSetDefaultBilling(w http.ResponseWriter, r *http.Request) {
	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	h.setDefaultBilling(w, r, customerID)
}

// --- address bodies shared by the two namespaces ----------------------------
//
// The address endpoints do the SAME work on the admin and the store side; the
// only difference between them is where the customer id comes from. The bodies
// therefore live in one place: two copies would mean a validation bug fixed in
// only one of them.

// listAddresses writes the given customer's addresses.
func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	addresses, err := h.svc.ListAddresses(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(addresses, toAddressDTO))
}

// createAddress adds a new address for the given customer.
func (h *Handler) createAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	var req addressRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	address, err := h.svc.CreateAddress(ctx, customerID, service.AddressInput{
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		Company:           req.Company,
		Address1:          req.Address1,
		Address2:          req.Address2,
		City:              req.City,
		Province:          req.Province,
		CountryCode:       req.CountryCode,
		PostalCode:        req.PostalCode,
		Phone:             req.Phone,
		IsDefaultShipping: req.IsDefaultShipping,
		IsDefaultBilling:  req.IsDefaultBilling,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toAddressDTO(address))
}

// updateAddress updates the given customer's address.
func (h *Handler) updateAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	var req updateAddressRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	address, err := h.svc.UpdateAddress(ctx, customerID, pathParam(r, paramAddressID),
		service.UpdateAddressInput{
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			Company:     req.Company,
			Address1:    req.Address1,
			Address2:    req.Address2,
			City:        req.City,
			Province:    req.Province,
			CountryCode: req.CountryCode,
			PostalCode:  req.PostalCode,
			Phone:       req.Phone,
		})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}

// deleteAddress soft-deletes the given customer's address.
func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	if err := h.svc.DeleteAddress(ctx, customerID, pathParam(r, paramAddressID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// setDefaultShipping makes the given customer's address the default shipping
// address.
//
// The flag lives NOT in the update body but on an endpoint of its own: the flag
// concerns the customer's other addresses too (the previous one is cleared) and
// cannot be expressed as a single-row update (see updateAddressRequest).
func (h *Handler) setDefaultShipping(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	address, err := h.svc.SetDefaultShippingAddress(ctx, customerID, pathParam(r, paramAddressID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}

// setDefaultBilling makes the given customer's address the default billing
// address.
func (h *Handler) setDefaultBilling(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	address, err := h.svc.SetDefaultBillingAddress(ctx, customerID, pathParam(r, paramAddressID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}
