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

// A customer's contact details on their page (ADR 0337): the name and the
// phone, corrected through the customer module's panel surface from the ones
// the page was drawn with. The e-mail is not among them.

// CustomerContactPath takes the form that corrects a customer's name and
// phone.
const CustomerContactPath = CustomerPath + "/contact"

// The contact form's fields: the name and the phone as typed, and as the
// page was drawn with them.
const (
	formFirstName     = "first_name"
	formLastName      = "last_name"
	formPhone         = "phone"
	formReadFirstName = "read_first_name"
	formReadLastName  = "read_last_name"
	formReadPhone     = "read_phone"
)

// ContactReviser is the narrow surface a customer's name and phone are
// corrected through (ADR 0337).
type ContactReviser interface {
	// ReviseCustomerContact corrects the customer's name and phone, the read
	// and the written ones as JSON with their fields named, and refuses when
	// they are no longer the ones read.
	ReviseCustomerContact(ctx context.Context, id string, read, next json.RawMessage) error
}

// customerContact is a customer's name and phone as the surface takes them;
// the json tags are the contract with that surface, exercised end to end.
type customerContact struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

// canReviseContact reports whether the operator may correct a customer's
// contact details here.
func (u *UI) canReviseContact(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(ContactReviser)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// contactOf is the customer's contact as the record has it.
func contactOf(rec query.Record) customerContact {
	return customerContact{
		FirstName: recordString(rec, fieldFirstName), LastName: recordString(rec, fieldLastName),
		Phone: recordString(rec, fieldPhone),
	}
}

// reviseContact corrects the customer's name and phone from the ones the
// page was drawn with and returns to the page, which says so; a refusal, a
// customer corrected since included, comes back on the page with what was
// typed (ADR 0337).
func (u *UI) reviseContact(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.memberships.(ContactReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot correct a customer in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	read, err := json.Marshal(customerContact{
		FirstName: r.PostFormValue(formReadFirstName), LastName: r.PostFormValue(formReadLastName),
		Phone: r.PostFormValue(formReadPhone),
	})
	var next []byte
	if err == nil {
		next, err = json.Marshal(customerContact{
			FirstName: strings.TrimSpace(r.PostFormValue(formFirstName)),
			LastName:  strings.TrimSpace(r.PostFormValue(formLastName)),
			Phone:     strings.TrimSpace(r.PostFormValue(formPhone)),
		})
	}
	if err == nil {
		err = reviser.ReviseCustomerContact(r.Context(), id, read, next)
	}
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, CustomersPath+"/"+id+"?"+url.Values{paramWritten: {"1"}}.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderCustomerTyped(w, r, http.StatusUnprocessableEntity, id, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The customer could not be corrected")
	}
}
