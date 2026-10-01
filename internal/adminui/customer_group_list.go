package adminui

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The customer groups screen (ADR 0323): the shop's groups with their rank,
// read through the customer module's group entity, the form that writes one
// through the module's panel surface, and each row's form that renames and
// re-ranks its group from what the row was drawn with (ADR 0329).

const (
	// CustomerGroupListPath lists the customer groups and takes the form
	// that writes one.
	CustomerGroupListPath = URLPrefix + "/customer-groups"
	// CustomerGroupRevisePath takes a row's form that renames and re-ranks
	// its group (ADR 0329).
	CustomerGroupRevisePath = CustomerGroupListPath + "/{id}"
)

// customerGroupsLabel is what the section is called on screen.
const customerGroupsLabel = "Customer groups"

// customerGroupsPerPage is the list's page size, the other lists'.
const customerGroupsPerPage = 25

// fieldRank is a customer group's rank, the smaller ranking first (ADR 0049).
const fieldRank = "rank"

// The group form's fields, and the name and rank a row's form carries as
// they were drawn (ADR 0329).
const (
	formGroupName = "name"
	formGroupRank = "rank"
	formReadName  = "read_name"
	formReadRank  = "read_rank"
)

// GroupCreator is the narrow surface a customer group is written through.
type GroupCreator interface {
	// CreateGroup writes a customer group and returns its id.
	CreateGroup(ctx context.Context, name string, rank int32) (string, error)
}

// GroupReviser is the narrow surface a customer group is renamed and
// re-ranked through (ADR 0329).
type GroupReviser interface {
	// ReviseGroup writes the group's name and rank, and refuses when they are
	// no longer the ones read.
	ReviseGroup(ctx context.Context, id, readName string, readRank int32, name string, rank int32) error
}

// customerGroupRow is one group as the list prints it, with what its form
// offers: the group as drawn, or what was typed in the form a refusal came
// back to.
type customerGroupRow struct {
	ID        string
	Name      string
	Rank      int64
	CreatedAt time.Time
	FormName  string
	FormRank  string
	Refused   bool
}

// canCreateGroups reports whether the operator may write a group here.
func (u *UI) canCreateGroups(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(GroupCreator)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// canReviseGroups reports whether the operator may rename and re-rank a
// group here.
func (u *UI) canReviseGroups(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.memberships.(GroupReviser)

	return ok && principal.HasScope(scopeCustomerWrite)
}

// listCustomerGroups renders the groups.
func (u *UI) listCustomerGroups(w http.ResponseWriter, r *http.Request) {
	u.renderCustomerGroups(w, r, http.StatusOK, "", url.Values{}, "")
}

// reviseCustomerGroup writes the name and the rank a row's form was sent
// with, from the ones the row was drawn with, and returns to the list's page,
// which names the group; a refusal, a group another operator revised first
// included, comes back on the page with what was typed in the row (ADR 0329).
func (u *UI) reviseCustomerGroup(w http.ResponseWriter, r *http.Request) {
	reviser, ok := u.memberships.(GroupReviser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot revise a group in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	name := strings.TrimSpace(r.PostFormValue(formGroupName))
	readRank, readErr := parseRank(r.PostFormValue(formReadRank))
	rank, err := parseRank(r.PostFormValue(formGroupRank))
	switch {
	case readErr != nil:
		err = errors.Invalid("admin_ui_read_rank", "The rank the row was drawn with could not be read; draw the list again.")
	case err != nil:
		err = errors.Invalid(CodeAmountInvalid, "A rank is a whole number; the smaller ranks first.")
	default:
		err = reviser.ReviseGroup(r.Context(), id, r.PostFormValue(formReadName), readRank, name, rank)
	}
	switch {
	case err == nil:
		landing := url.Values{paramCreated: {name}}
		if page := pageNumber(r.URL.Query().Get("page")); page > 1 {
			landing.Set("page", strconv.Itoa(page))
		}
		corehttp.WriteRedirect(r.Context(), w, CustomerGroupListPath+"?"+landing.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderCustomerGroups(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, id)
	default:
		u.unexpectedFailure(w, r, err, "The customer group could not be revised")
	}
}

// createCustomerGroup writes the group the form describes and returns to the
// list, which names it; a refusal comes back on the list with what was typed
// (ADR 0323).
func (u *UI) createCustomerGroup(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.memberships.(GroupCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Customers unavailable",
			"The customer module's panel surface cannot write a group in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	name := strings.TrimSpace(r.PostFormValue(formGroupName))
	rank, err := parseRank(r.PostFormValue(formGroupRank))
	if err != nil {
		err = errors.Invalid(CodeAmountInvalid, "A rank is a whole number; the smaller ranks first.")
	} else {
		_, err = creator.CreateGroup(r.Context(), name, rank)
	}
	switch {
	case err == nil:
		created := url.Values{paramCreated: {name}}
		corehttp.WriteRedirect(r.Context(), w, CustomerGroupListPath+"?"+created.Encode())
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderCustomerGroups(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm, "")
	default:
		u.unexpectedFailure(w, r, err, "The customer group could not be written")
	}
}

// parseRank reads a rank, zero when it is left empty.
func parseRank(text string) (int32, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	rank, err := strconv.ParseInt(text, 10, 32)
	if err != nil {
		return 0, err
	}

	return int32(rank), nil
}

// renderCustomerGroups lists the groups, newest first, with a refused write's
// reason and what was typed: in the new group's form, or in the row of the
// group revised when one was (ADR 0329). An operator who may write and not
// read the customers is told the reason alone (ADR 0260).
func (u *UI) renderCustomerGroups(
	w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values, revised string,
) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeCustomerRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}

	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * customerGroupsPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityCustomerGroup,
		Fields: []string{fieldID, fieldName, fieldRank, fieldCreatedAt},
		// One more than the page, so whether there is a next one comes out of
		// this read.
		Limit:  customerGroupsPerPage + 1,
		Offset: offset,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The customer groups could not be read.")
		return
	}
	more := len(records) > customerGroupsPerPage
	if more {
		records = records[:customerGroupsPerPage]
	}
	rows := make([]customerGroupRow, 0, len(records))
	for _, rec := range records {
		row := customerGroupRow{
			ID: recordString(rec, fieldID), Name: recordString(rec, fieldName),
			Rank: recordInt(rec, fieldRank), CreatedAt: recordTime(rec, fieldCreatedAt),
		}
		row.FormName, row.FormRank = row.Name, strconv.FormatInt(row.Rank, 10)
		if revised != "" && row.ID == revised {
			row.FormName, row.FormRank, row.Refused = typed.Get(formGroupName), typed.Get(formGroupRank), true
		}
		rows = append(rows, row)
	}
	if revised != "" {
		// What was typed is the row's, not the new group's.
		typed = url.Values{}
	}

	data := map[string]any{
		titleKey:     customerGroupsLabel,
		"Groups":     rows,
		createdKey:   r.URL.Query().Get(paramCreated),
		canCreateKey: u.canCreateGroups(r),
		"CanRevise":  u.canReviseGroups(r),
		refusedKey:   refused,
		typedKey:     typed,
	}
	addPaging(data, page, more, CustomerGroupListPath)

	u.templates.render(w, r, code, "customer_groups.gohtml", data)
}
