package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeGroupWriter puts customers into groups and writes groups, recording
// each group written.
type fakeGroupWriter struct {
	fakeMemberships
	created  []string
	createEr error
}

func (f *fakeGroupWriter) CreateGroup(_ context.Context, name string, rank int32) (string, error) {
	f.created = append(f.created, fmt.Sprintf("%s|%d", name, rank))
	return "custgrp_new", f.createEr
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
