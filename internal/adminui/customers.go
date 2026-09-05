package adminui

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomer,
		Fields: []string{
			fieldID, fieldEmail, fieldFirstName, fieldLastName,
			fieldHasAccount, fieldCreatedAt,
		},
		Limit:  customersPerPage + 1,
		Offset: (page - 1) * customersPerPage,
	})
	if err != nil {
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
	}
	addPaging(data, page, hasNext, CustomersPath)

	u.templates.render(w, r, http.StatusOK, "customers.gohtml", data)
}

// showCustomer renders one customer.
//
// The ADDRESSES are absent, and that is a property of the read layer rather
// than a shortcut: an address is the customer module's own record, and the read
// layer joins across LINKS, not within a module. Showing them would need the
// panel to hold the customer module's service — the coupling the fourth tree
// exists to avoid.
func (u *UI) showCustomer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No customer was named.")

		return
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomer,
		Fields: []string{
			fieldID, fieldEmail, fieldFirstName, fieldLastName, fieldPhone,
			fieldHasAccount, fieldCreatedAt,
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

	u.templates.render(w, r, http.StatusOK, "customer.gohtml", map[string]any{
		titleKey:        customerRowOf(records[0]).display(),
		"Customer":      customerRowOf(records[0]),
		"CustomersPath": CustomersPath,
	})
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
