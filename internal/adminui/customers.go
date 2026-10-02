package adminui

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// EntityCustomer is the customer module's entity name in the read layer.
//
// It is a STRING and not an import, for the reason [EntityOrder] is: the panel
// knows no module (ADR 0011).
const EntityCustomer = "customer"

// The customer fields the panel reads.
const (
	fieldFirstName  = "first_name"
	fieldLastName   = "last_name"
	fieldPhone      = "phone"
	fieldHasAccount = "has_account"
	fieldCreatedAt  = "created_at"
)

// paramCustomerEmail is the customer list's search: an e-mail, matched
// exactly as the customer module stores it (ADR 0302).
const paramCustomerEmail = "email"

// customersLabel is what the section is called on screen.
const customersLabel = "Customers"

// customersPerPage is the page size of the customer list.
//
// It matches the other lists', and matching is the point: three screens in one
// panel that paged differently would make an operator learn three habits.
const customersPerPage = 25

// customerRow is one line of the customer table.
type customerRow struct {
	ID    string
	Email string
	// Name is the two name fields joined, or empty when neither is set. It is
	// built HERE rather than in the template so the two screens cannot start
	// joining them differently.
	Name string
	// HasAccount separates a registered customer from a guest. A shop's guest
	// records outnumber its accounts, and telling them apart is the first thing
	// an operator does on this screen.
	HasAccount bool
	Phone      string
	CreatedAt  time.Time
}

// listCustomers renders the customer list.
//
// It reads through the cross-module read layer, like every other panel screen
// and for the same reason (ADR 0011).
func (u *UI) listCustomers(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))
	// The search finds the records holding one e-mail, the account and the
	// guest records alike (ADR 0302).
	email := strings.TrimSpace(r.URL.Query().Get(paramCustomerEmail))
	var filters map[string]any
	if email != "" {
		filters = map[string]any{filterCustomerEmail: email}
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomer,
		Fields: []string{
			fieldID, fieldEmail, fieldFirstName, fieldLastName,
			fieldHasAccount, fieldCreatedAt,
		},
		Filters: filters,
		Limit:   customersPerPage + 1,
		Offset:  (page - 1) * customersPerPage,
	})
	refused := ""
	switch {
	case err != nil && email != "" && errors.IsInvalid(err):
		// The provider refuses an address it cannot normalize; that is the
		// operator's typing, not the screen's bug.
		refused = "That is not an e-mail address."
		records = nil
	case err != nil:
		u.catalogFailure(w, r, err, "The customer list could not be read.")

		return
	}

	hasNext := len(records) > customersPerPage
	if hasNext {
		records = records[:customersPerPage]
	}

	rows := make([]customerRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, customerRowOf(rec))
	}

	data := map[string]any{
		titleKey:    customersLabel,
		"Customers": rows,
		emailKey:    email,
		refusedKey:  refused,
	}
	addPaging(data, page, hasNext, CustomersPath)

	u.templates.render(w, r, http.StatusOK, "customers.gohtml", data)
}

// fieldCustomerAddresses is the customer provider's list of a customer's
// addresses (ADR 0308), a field and not a join, as a cart's lines are (ADR
// 0290).
const fieldCustomerAddresses = "addresses"

// customerAddress is one of a customer's addresses as the page prints it.
type customerAddress struct {
	ID                              string
	Lines                           []string
	DefaultShipping, DefaultBilling bool
	// Read and Form are the address's printed fields as drawn and as its
	// form offers them, what was typed when its correction was refused (ADR
	// 0342).
	Read, Form printedAddress
	Refused    bool
}

// showCustomer renders one customer with their addresses (ADR 0308) and
// groups (ADR 0322).
func (u *UI) showCustomer(w http.ResponseWriter, r *http.Request) {
	u.renderCustomer(w, r, http.StatusOK, chi.URLParam(r, "id"), "")
}

// renderCustomer reads the customer and writes their page, with a refused
// write's reason. An operator who may write and not read the customers is
// told the reason alone (ADR 0260).
func (u *UI) renderCustomer(w http.ResponseWriter, r *http.Request, code int, id, refused string) {
	u.renderCustomerTyped(w, r, code, id, refused, nil)
}

// renderCustomerTyped is [UI.renderCustomer] with what was typed in the
// contact form a refusal came back to, the drawn name and phone always the
// customer's as they are now (ADR 0337).
func (u *UI) renderCustomerTyped(
	w http.ResponseWriter, r *http.Request, code int, id, refused string, typed url.Values,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeCustomerRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No customer was named.")

		return
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomer,
		Fields: []string{
			fieldID, fieldEmail, fieldFirstName, fieldLastName, fieldPhone,
			fieldHasAccount, fieldCreatedAt, fieldCustomerAddresses, fieldCustomerGroupIDs,
		},
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The customer could not be read.")

		return
	}

	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such customer.")

		return
	}

	var addresses []customerAddress
	refusedAddress := typed.Get(formAddressID)
	for _, entry := range recordList(records[0][fieldCustomerAddresses]) {
		address := customerAddress{
			ID:              recordString(entry, fieldID),
			Lines:           addressLines(map[string]any(entry)),
			DefaultShipping: recordBool(entry, "is_default_shipping"),
			DefaultBilling:  recordBool(entry, "is_default_billing"),
			Read:            printedAddressOf(entry),
		}
		address.Form = address.Read
		if refusedAddress != "" && address.ID == refusedAddress {
			address.Form, address.Refused = printedAddress{}, true
			for _, key := range addressKeys {
				address.Form[key] = typed.Get(key)
			}
		}
		addresses = append(addresses, address)
	}

	memberOf := recordStrings(records[0], fieldCustomerGroupIDs)
	groups, groupsUnread := u.customerGroupsOf(r.Context(), memberOf)
	data := map[string]any{
		titleKey:           customerRowOf(records[0]).display(),
		"Customer":         customerRowOf(records[0]),
		"Addresses":        addresses,
		"CustomersPath":    CustomersPath,
		"Groups":           groups,
		"GroupsUnread":     groupsUnread,
		refusedKey:         refused,
		writtenKey:         r.URL.Query().Get(paramWritten),
		"CanEditAddresses": u.canReviseAddresses(r),
		// The customer's newest orders, for an operator who may read the
		// orders (ADR 0358).
		"CustomerOrders": u.ordersOfCustomer(r, recordString(records[0], fieldID)),
		ordersPathKey:    OrdersPath,
		// The new address's form, with what was typed when it was refused
		// (ADR 0359).
		"CanAddAddress": u.canAddAddresses(r),
		"NewAddress":    printedAddress{},
	}
	if refusedAddress == newAddressMarker {
		added := printedAddress{}
		for _, key := range addressKeys {
			added[key] = typed.Get(key)
		}
		data["NewAddress"], data["NewAddressRefused"] = added, true
		data["NewDefaultShipping"] = typed.Get(formDefaultShipping) != ""
		data["NewDefaultBilling"] = typed.Get(formDefaultBilling) != ""
	}
	if u.canReviseContact(r) {
		drawn := contactOf(records[0])
		form := drawn
		if typed != nil && refusedAddress == "" {
			form = customerContact{
				FirstName: typed.Get(formFirstName), LastName: typed.Get(formLastName), Phone: typed.Get(formPhone),
			}
		}
		data["ContactRead"], data["ContactForm"] = drawn, form
		data["ContactRefused"] = typed != nil && refusedAddress == ""
	}
	// The groups to join are offered only to an operator who may write them:
	// a reader is not read the whole list for a form they cannot send.
	if u.canEditMemberships(r) {
		data["CanEditGroups"] = true
		offered := u.groupList(r.Context())
		joinable := offered.Options[:0:0]
		for _, option := range offered.Options {
			if !slices.Contains(memberOf, option.ID) {
				joinable = append(joinable, option)
			}
		}
		offered.Options = joinable
		data["GroupChoices"] = offered
	}

	u.templates.render(w, r, code, "customer.gohtml", data)
}

// customerRowOf turns a customer record into a row.
//
// The list and the detail page share it so the two screens cannot start reading
// the same customer differently.
func customerRowOf(rec query.Record) customerRow {
	return customerRow{
		ID:         recordString(rec, fieldID),
		Email:      recordString(rec, fieldEmail),
		Name:       joinName(recordString(rec, fieldFirstName), recordString(rec, fieldLastName)),
		HasAccount: recordBool(rec, fieldHasAccount),
		Phone:      recordString(rec, fieldPhone),
		CreatedAt:  recordTime(rec, fieldCreatedAt),
	}
}

// display is what the customer is called on a page title.
//
// A customer may have no name and no e-mail — a guest record created from a
// checkout that carried neither — so the identifier is the last resort. A blank
// title would leave the operator on a page they cannot tell from another.
func (c customerRow) display() string {
	switch {
	case c.Name != "":
		return c.Name
	case c.Email != "":
		return c.Email
	default:
		return c.ID
	}
}

// joinName joins the two name fields, tolerating either being empty.
func joinName(first, last string) string {
	return strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
}

// recordBool reads a boolean field, or false when it is absent or not one.
func recordBool(rec query.Record, field string) bool {
	value, _ := rec[field].(bool)

	return value
}
