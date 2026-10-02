package adminui

import (
	"context"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Moving a default and removing an address (ADR 0360): each address's row on
// a customer's page makes it the default shipping or billing address, taking
// the flag from the one that held it, or removes it, through the customer
// module's panel surface.

// CustomerAddressDefaultPath makes the address a default, and
// CustomerAddressRemovePath removes it.
const (
	CustomerAddressDefaultPath = CustomerAddressPath + "/default"
	CustomerAddressRemovePath  = CustomerAddressPath + "/remove"
)

// formDefaultKind names the default an address is made: "shipping" or
// "billing", the customer module's names.
const formDefaultKind = "kind"

// The written markers the customer's page says after an address's act.
const (
	writtenDefault = "default"
	writtenRemoved = "removed"
)

// AddressActs is the narrow surface an address's default is moved and an
// address removed through.
type AddressActs interface {
	// MakeAddressDefault makes the address the default the kind names.
	MakeAddressDefault(ctx context.Context, customerID, addressID, kind string) error
	// RemoveCustomerAddress removes the address.
	RemoveCustomerAddress(ctx context.Context, customerID, addressID string) error
}

// canActOnAddresses reports whether the operator may move a default or
// remove an address here.
func (u *UI) canActOnAddresses(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(AddressActs)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// makeAddressDefault makes the address in the path the default the form
// names (ADR 0360).
func (u *UI) makeAddressDefault(w http.ResponseWriter, r *http.Request) {
	u.actOnAddress(w, r, writtenDefault, func(ctx context.Context, acts AddressActs, customerID, addressID string) error {
		return acts.MakeAddressDefault(ctx, customerID, addressID, r.PostFormValue(formDefaultKind))
	})
}

// removeAddress removes the address in the path (ADR 0360).
func (u *UI) removeAddress(w http.ResponseWriter, r *http.Request) {
	u.actOnAddress(w, r, writtenRemoved, func(ctx context.Context, acts AddressActs, customerID, addressID string) error {
		return acts.RemoveCustomerAddress(ctx, customerID, addressID)
	})
}

// actOnAddress carries out an address's act and returns to the customer's
// page, which says what was done; a refusal comes back on the page.
func (u *UI) actOnAddress(
	w http.ResponseWriter, r *http.Request, written string,
	act func(ctx context.Context, acts AddressActs, customerID, addressID string) error,
) {
	acts, ok := u.memberships.(AddressActs)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot act on an address in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	customerID := chi.URLParam(r, "id")
	err := act(r.Context(), acts, customerID, chi.URLParam(r, "address"))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CustomersPath+"/"+customerID+"?"+url.Values{paramWritten: {written}}.Encode())
	case errors.IsInvalid(err) || errors.IsNotFound(err) || errors.IsConflict(err):
		u.renderCustomerTyped(w, r, http.StatusUnprocessableEntity, customerID, messageFor(err), nil)
	default:
		u.unexpectedFailure(w, r, err, "The address could not be changed")
	}
}
