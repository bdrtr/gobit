package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// Adding an address to a customer (ADR 0359): their page adds one with its
// printed fields, as their default shipping or billing address when ticked,
// through the customer module's panel surface.

// CustomerAddressesPath takes the form that adds an address.
const CustomerAddressesPath = CustomerPath + "/addresses"

// The new address's form's fields beside the printed ones.
const (
	formDefaultShipping = "default_shipping"
	formDefaultBilling  = "default_billing"
)

// newAddressMarker names the new address's form as the one a refusal was
// sent for, so the page draws it again with what was typed and neither an
// address's row nor the contact form.
const newAddressMarker = "new"

// AddressAdder is the narrow surface an address is added through.
type AddressAdder interface {
	// AddCustomerAddress adds an address from its printed fields, as JSON
	// with the provider's address keys, and returns its id.
	AddCustomerAddress(ctx context.Context, customerID string, address json.RawMessage, defaultShipping, defaultBilling bool) (string, error)
}

// canAddAddresses reports whether the operator may add an address here.
func (u *UI) canAddAddresses(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(AddressAdder)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// addAddress adds the address typed to the customer in the path and returns
// to their page, which says so; a refusal comes back on the page with what
// was typed in the form (ADR 0359).
func (u *UI) addAddress(w http.ResponseWriter, r *http.Request) {
	adder, ok := u.memberships.(AddressAdder)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot add an address in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	customerID := chi.URLParam(r, "id")
	fields := printedAddress{}
	for _, key := range addressKeys {
		fields[key] = strings.TrimSpace(r.PostFormValue(key))
	}
	body, err := json.Marshal(fields)
	if err == nil {
		_, err = adder.AddCustomerAddress(r.Context(), customerID, body,
			r.PostFormValue(formDefaultShipping) != "", r.PostFormValue(formDefaultBilling) != "")
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CustomersPath+"/"+customerID+"?"+url.Values{paramWritten: {"address"}}.Encode())
	case errors.IsInvalid(err) || errors.IsNotFound(err):
		typed := r.PostForm
		typed.Set(formAddressID, newAddressMarker)
		u.renderCustomerTyped(w, r, http.StatusUnprocessableEntity, customerID, messageFor(err), typed)
	default:
		u.unexpectedFailure(w, r, err, "The address could not be added")
	}
}
