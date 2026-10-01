package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeGroupWriter puts customers into groups and writes and revises groups,
// recording each group written and revised.
type fakeGroupWriter struct {
	fakeMemberships
	created  []string
	createEr error
	revised  []string
	reviseEr error
}

func (f *fakeGroupWriter) CreateGroup(_ context.Context, name string, rank int32) (string, error) {
	f.created = append(f.created, fmt.Sprintf("%s|%d", name, rank))
	return "custgrp_new", f.createEr
}

func (f *fakeGroupWriter) ReviseGroup(_ context.Context, id, readName string, readRank int32, name string, rank int32) error {
	f.revised = append(f.revised, fmt.Sprintf("%s|%s|%d|%s|%d", id, readName, readRank, name, rank))
	return f.reviseEr
}

// groupListCatalog holds two groups.
func groupListCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{EntityCustomerGroup: {
		{fieldID: "custgrp_vip", fieldName: "VIP", fieldRank: int32(-1), fieldCreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)},
		{fieldID: "custgrp_trade", fieldName: "Trade", fieldRank: int32(3), fieldCreatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}}}
}

// TestTheCustomerGroupsScreenListsTheGroups is ADR 0323: each group with its
// rank and the day it was written, read a page and one more at a time
// through the group entity; the form is offered to a writer whose surface can
// write a group.
func TestTheCustomerGroupsScreenListsTheGroups(t *testing.T) {
	t.Parallel()

	catalog := groupListCatalog()
	panel := membershipPanel(t, catalog, &fakeGroupWriter{})

	rec := campaignsRequest(panel, http.MethodGet, CustomerGroupListPath+"?page=2", nil, scopeCustomerRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{"VIP", "custgrp_vip", `<td class="num">-1</td>`, "2026-09-01", "Trade", `<td class="num">3</td>`} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "New group", "a reader writes nothing")
	require.NotEmpty(t, catalog.specs)
	spec := catalog.specs[len(catalog.specs)-1]
	assert.Equal(t, EntityCustomerGroup, spec.Entity)
	assert.Equal(t, customerGroupsPerPage+1, spec.Limit, "a page and one more, to know there is a next")
	assert.Equal(t, customerGroupsPerPage, spec.Offset, "the second page")
	assert.NotContains(t, body, "page=3", "two groups fill no second page")

	rec = campaignsRequest(panel, http.MethodGet, CustomerGroupListPath, nil, scopeCustomerRead, scopeCustomerWrite)
	assert.Contains(t, rec.Body.String(), "New group")
	rec = campaignsRequest(membershipPanel(t, groupListCatalog(), &fakeMemberships{}), http.MethodGet,
		CustomerGroupListPath, nil, scopeCustomerRead, scopeCustomerWrite)
	assert.NotContains(t, rec.Body.String(), "New group", "a surface that cannot write a group offers no form")
	rec = campaignsRequest(membershipPanel(t, groupListCatalog(), &fakeMemberships{}), http.MethodPost,
		CustomerGroupListPath, url.Values{formGroupName: {"X"}}, scopeCustomerWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestTheGroupFormWritesWhatWasTyped: the name trimmed and the rank read as a
// whole number, zero when blank; the list it lands on names the group; a rank
// that is not a number and the module's refusal come back with what was
// typed, to a writer who cannot read as the reason alone.
func TestTheGroupFormWritesWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeGroupWriter{}
	panel := membershipPanel(t, groupListCatalog(), writer)
	scopes := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodPost, CustomerGroupListPath,
		url.Values{formGroupName: {" Wholesale "}, formGroupRank: {" -2 "}}, scopes...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CustomerGroupListPath+"?created=Wholesale", rec.Header().Get("Location"))
	assert.Equal(t, []string{"Wholesale|-2"}, writer.created)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeCustomerRead)
	assert.Contains(t, landed.Body.String(), "Group Wholesale was written.")

	campaignsRequest(panel, http.MethodPost, CustomerGroupListPath, url.Values{formGroupName: {"Staff"}}, scopes...)
	assert.Equal(t, "Staff|0", writer.created[1], "a blank rank is zero")

	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath,
		url.Values{formGroupName: {"Gold"}, formGroupRank: {"first"}}, scopes...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "A rank is a whole number; the smaller ranks first.")
	assert.Contains(t, rec.Body.String(), `value="Gold"`, "what was typed comes back")
	assert.Len(t, writer.created, 2, "a rank the panel cannot read is not sent")

	writer.createEr = errors.Conflict("customer_group_name_taken", `a customer group named "Gold" already exists`)
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath, url.Values{formGroupName: {"Gold"}}, scopes...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "a customer group named &#34;Gold&#34; already exists")
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath, url.Values{formGroupName: {"Gold"}}, scopeCustomerWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "custgrp_vip", "a writer who cannot read is shown none of the list")

	writer.createEr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath, url.Values{formGroupName: {"Gold"}}, scopes...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// groupRowForm is the revise form of the group's row on a page.
func groupRowForm(t *testing.T, body, id string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+CustomerGroupListPath+"/"+id+"?page=")
	require.True(t, found, "%s offers its form", id)
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestAGroupRowRevisesItsGroup is ADR 0329: each row offers a writer the
// form that renames and re-ranks its group, carrying the name and the rank it
// was drawn with; the surface is asked to revise the group from those, and
// the list's page names it; a rank the panel cannot read is not sent, and a
// refusal, a group another operator revised first included, comes back in
// the row with what was typed, drawn from the group as it is now.
func TestAGroupRowRevisesItsGroup(t *testing.T) {
	t.Parallel()

	writer := &fakeGroupWriter{}
	panel := membershipPanel(t, groupListCatalog(), writer)
	scopes := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodGet, CustomerGroupListPath+"?page=2", nil, scopes...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := groupRowForm(t, rec.Body.String(), "custgrp_vip")
	assert.True(t, strings.HasPrefix(form, `2">`), "the form returns to the page it was drawn on")
	for _, want := range []string{
		`name="read_name" value="VIP"`, `name="read_rank" value="-1"`,
		`name="name" value="VIP"`, `name="rank" value="-1"`,
	} {
		assert.Contains(t, form, want)
	}
	assert.Contains(t, groupRowForm(t, rec.Body.String(), "custgrp_trade"), `name="read_rank" value="3"`)
	assert.NotContains(t, rec.Body.String(), "<details open>", "no row is open until a refusal opens it")
	rec = campaignsRequest(panel, http.MethodGet, CustomerGroupListPath, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "Revise", "a reader revises nothing")
	rec = campaignsRequest(membershipPanel(t, groupListCatalog(), &fakeMemberships{}), http.MethodGet,
		CustomerGroupListPath, nil, scopes...)
	assert.NotContains(t, rec.Body.String(), "Revise", "a surface that cannot revise a group offers no form")
	rec = campaignsRequest(membershipPanel(t, groupListCatalog(), &fakeMemberships{}), http.MethodPost,
		CustomerGroupListPath+"/custgrp_vip", url.Values{formGroupName: {"X"}}, scopeCustomerWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	sent := url.Values{
		formReadName: {"VIP"}, formReadRank: {"-1"}, formGroupName: {" Gold "}, formGroupRank: {" -4 "},
	}
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_vip?page=2", sent, scopes...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CustomerGroupListPath+"?created=Gold&page=2", rec.Header().Get("Location"))
	assert.Equal(t, []string{"custgrp_vip|VIP|-1|Gold|-4"}, writer.revised)
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_trade?page=1", url.Values{
		formReadName: {"Trade"}, formReadRank: {"3"}, formGroupName: {"Retail"}, formGroupRank: {"3"},
	}, scopes...)
	assert.Equal(t, CustomerGroupListPath+"?created=Retail", rec.Header().Get("Location"), "the first page is the list")
	assert.Equal(t, "custgrp_trade|Trade|3|Retail|3", writer.revised[1], "the group in the path is revised")

	for reason, form := range map[string]url.Values{
		"A rank is a whole number":                          {formReadName: {"VIP"}, formReadRank: {"-1"}, formGroupName: {"Gold"}, formGroupRank: {"first"}},
		"The rank the row was drawn with could not be read": {formReadName: {"VIP"}, formReadRank: {"x"}, formGroupName: {"Gold"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_vip", form, scopes...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, writer.revised, 2, "a rank the panel cannot read is not sent")

	writer.reviseEr = errors.Conflict("customer_group_moved",
		`customer group custgrp_vip is "VIP" at rank -1 now, not "Staff" at rank 0; draw the list again`)
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_vip", url.Values{
		formReadName: {"Staff"}, formReadRank: {"0"}, formGroupName: {"Gold"}, formGroupRank: {"7"},
	}, scopes...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	form = groupRowForm(t, body, "custgrp_vip")
	for _, want := range []string{
		`name="read_name" value="VIP"`, `name="read_rank" value="-1"`, `name="name" value="Gold"`, `name="rank" value="7"`,
	} {
		assert.Contains(t, form, want, "the row carries the group as it is now and what was typed")
	}
	assert.Contains(t, body, "<details open>", "the refused row is open")
	assert.Contains(t, groupRowForm(t, body, "custgrp_trade"), `name="name" value="Trade"`, "another row is as drawn")
	newGroup, _, _ := strings.Cut(body, "<table>")
	assert.NotContains(t, newGroup, "Gold", "what was typed is the row's, not the new group's")

	writer.reviseEr = errors.NotFound("customer_group_not_found", "customer group custgrp_vip not found")
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_vip", sent, scopes...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "customer group custgrp_vip not found")
	writer.reviseEr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, CustomerGroupListPath+"/custgrp_vip", sent, scopes...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
