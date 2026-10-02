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
	"github.com/bdrtr/gobit/core/query"
)

// A customer's address corrected on their page (ADR 0342): the printed
// fields, written through the customer module's panel surface from the ones
// the page was drawn with.

// CustomerAddressPath takes the form that corrects one of the customer's
// addresses.
const CustomerAddressPath = CustomerPath + "/addresses/{address}"

// formAddressID names the address a refused form was sent for, so the page
// is drawn again with what was typed in that address's form.
const formAddressID = "address_id"

// addressKeys are the printed fields' keys, the customer provider's address
// keys, each also a form field and, prefixed with "read_", the field the form
// carries it as drawn in.
var addressKeys = []string{
	"first_name", "last_name", "company", "address_1", "address_2", "city", "province", "country_code", "postal_code",
	"phone",
}

// AddressReviser is the narrow surface a customer's address is corrected
// through (ADR 0342).
type AddressReviser interface {
	// ReviseCustomerAddress corrects the address from the printed fields
	// read, both as JSON with the provider's address keys, and refuses when
	// they are no longer the ones read.
	ReviseCustomerAddress(ctx context.Context, customerID, addressID string, read, next json.RawMessage) error
}

// printedAddress are an address's printed fields by key.
type printedAddress map[string]string

// printedAddressOf reads an address entry's printed fields.
func printedAddressOf(entry query.Record) printedAddress {
	fields := printedAddress{}
	for _, key := range addressKeys {
		fields[key] = stringValue(entry[key])
	}

	return fields
}

// canReviseAddresses reports whether the operator may correct an address
// here.
func (u *UI) canReviseAddresses(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(AddressReviser)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// reviseAddress corrects the address in the path from the printed fields the
// page was drawn with and returns to the page, which says so; a refusal, an
// address corrected since included, comes back on the page with what was
// typed (ADR 0342).
func (u *UI) reviseAddress(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.memberships.(AddressReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot correct an address in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	customerID, addressID := chi.URLParam(r, "id"), chi.URLParam(r, "address")
	read, next := printedAddress{}, printedAddress{}
	for _, key := range addressKeys {
		read[key] = r.PostFormValue("read_" + key)
		next[key] = strings.TrimSpace(r.PostFormValue(key))
	}
	readJSON, err := json.Marshal(read)
	var nextJSON []byte
	if err == nil {
		nextJSON, err = json.Marshal(next)
	}
	if err == nil {
		err = reviser.ReviseCustomerAddress(r.Context(), customerID, addressID, readJSON, nextJSON)
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CustomersPath+"/"+customerID+"?"+url.Values{paramWritten: {"address"}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		typed := r.PostForm
		typed.Set(formAddressID, addressID)
		u.renderCustomerTyped(w, r, http.StatusUnprocessableEntity, customerID, messageFor(err), typed)
	default:
		u.unexpectedFailure(w, r, err, "The address could not be corrected")
	}
}
