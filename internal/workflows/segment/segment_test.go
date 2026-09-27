package segment_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/segment"
)

// now is the pass's moment in every test.
var now = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

// person is one customer in the fake customer module.
type person struct {
	id         string
	hasAccount bool
	createdAt  time.Time
	country    string
}

// seg is one segment in the fake customer module.
type seg struct {
	groupID string
	setAt   time.Time
	rule    string
}

// page is one ApplySegmentPage call.
type page struct {
	groupID, after, last string
	members              []string
}

// fakeCustomers imitates the customer module's segment surface: the page write
// takes the given members in and every other member in the range out.
type fakeCustomers struct {
	segments []seg
	people   []person
	members  map[string]map[string]bool
	pages    []page
	finished []string
	// ruleAt is the rule each group has now, when it differs from the one paged.
	ruleAt   map[string]time.Time
	applyErr map[string]error
}

func (f *fakeCustomers) SegmentsJSON(context.Context) (json.RawMessage, error) {
	parts := make([]string, 0, len(f.segments))
	for _, s := range f.segments {
		parts = append(parts, fmt.Sprintf(`{"group_id":%q,"set_at":%q,"rule":%s}`,
			s.groupID, s.setAt.Format(time.RFC3339Nano), s.rule))
	}
	return json.RawMessage("[" + strings.Join(parts, ",") + "]"), nil
}

func (f *fakeCustomers) SegmentFactsJSON(_ context.Context, after string, limit int) (json.RawMessage, error) {
	type row struct {
		CustomerID  string    `json:"customer_id"`
		HasAccount  bool      `json:"has_account"`
		CreatedAt   time.Time `json:"created_at"`
		CountryCode string    `json:"country_code,omitempty"`
	}
	slices.SortFunc(f.people, func(a, b person) int { return strings.Compare(a.id, b.id) })
	out := []row{}
	for _, p := range f.people {
		if p.id > after && len(out) < limit {
			out = append(out, row{p.id, p.hasAccount, p.createdAt, p.country})
		}
	}
	return json.Marshal(out)
}

func (f *fakeCustomers) ApplySegmentPage(
	_ context.Context, groupID string, setAt time.Time, after, last string, members []string,
) (added, removed int, applied bool, err error) {
	f.pages = append(f.pages, page{groupID, after, last, members})
	if err := f.applyErr[groupID]; err != nil {
		return 0, 0, false, err
	}
	if at, replaced := f.ruleAt[groupID]; replaced && !at.Equal(setAt) {
		return 0, 0, false, nil
	}
	if f.members[groupID] == nil {
		f.members[groupID] = map[string]bool{}
	}
	for id := range f.members[groupID] {
		if id > after && (last == "" || id <= last) && !slices.Contains(members, id) {
			delete(f.members[groupID], id)
			removed++
		}
	}
	for _, id := range members {
		if !f.members[groupID][id] {
			f.members[groupID][id] = true
			added++
		}
	}
	return added, removed, true, nil
}

func (f *fakeCustomers) FinishSegment(_ context.Context, groupID string, _, evaluatedAt time.Time) (bool, error) {
	if !evaluatedAt.Equal(now) {
		return false, fmt.Errorf("finished at %v, not the pass's moment", evaluatedAt)
	}
	f.finished = append(f.finished, groupID)
	return true, nil
}

// order is one order in the fake order module.
type order struct {
	customer, currency string
	total              int64
	placedAt           time.Time
}

// fakeOrders totals the orders placed since the given moment per customer and
// currency, and records what it was asked.
type fakeOrders struct {
	orders []order
	asked  []*time.Time
	err    error
}

func (f *fakeOrders) CustomerOrderTotalsJSON(
	_ context.Context, customerIDs []string, since *time.Time,
) (json.RawMessage, error) {
	f.asked = append(f.asked, since)
	if f.err != nil {
		return nil, f.err
	}
	type row struct {
		CustomerID   string `json:"customer_id"`
		CurrencyCode string `json:"currency_code"`
		Orders       int64  `json:"orders"`
		NetSpend     int64  `json:"net_spend"`
	}
	sums := map[[2]string]*row{}
	var keys [][2]string
	for _, o := range f.orders {
		if !slices.Contains(customerIDs, o.customer) || (since != nil && o.placedAt.Before(*since)) {
			continue
		}
		key := [2]string{o.customer, o.currency}
		if sums[key] == nil {
			sums[key] = &row{CustomerID: o.customer, CurrencyCode: o.currency}
			keys = append(keys, key)
		}
		sums[key].Orders++
		sums[key].NetSpend += o.total
	}
	out := make([]row, 0, len(keys))
	for _, key := range keys {
		out = append(out, *sums[key])
	}
	return json.Marshal(out)
}

// harness is the flow over the two fakes.
type harness struct {
	customers *fakeCustomers
	orders    *fakeOrders
	flow      *segment.Workflow
}

func newHarness(segments []seg, people []person, orders []order) *harness {
	h := &harness{
		customers: &fakeCustomers{segments: segments, people: people, members: map[string]map[string]bool{}},
		orders:    &fakeOrders{orders: orders},
	}
	h.flow = segment.New(h.customers, h.orders, func() time.Time { return now }, nil)

	return h
}

// members lists a group's members, sorted.
func (h *harness) members(groupID string) []string {
	var out []string
	for id := range h.customers.members[groupID] {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

var setAt = now.Add(-time.Hour)

// TestAPassWritesEachSegmentsMembers is ADR 0217: each rule is evaluated over
// every customer, the members are written and the segment is marked evaluated.
func TestAPassWritesEachSegmentsMembers(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_accounts", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
		{"custgrp_spenders", setAt, `{"currency_code":"TRY","window_days":30,"conditions":[
			{"attribute":"net_spend","operator":"gte","value":1000},
			{"attribute":"country_code","operator":"in","values":["TR","DE"]}]}`},
	}, []person{
		{"cus_1", true, now.AddDate(-1, 0, 0), "TR"},
		{"cus_2", false, now.AddDate(-1, 0, 0), "TR"},
		{"cus_3", true, now.AddDate(-1, 0, 0), "FR"},
		{"cus_4", false, now.AddDate(-1, 0, 0), ""},
	}, []order{
		{"cus_1", "TRY", 600, now.AddDate(0, 0, -1)},
		{"cus_1", "TRY", 500, now.AddDate(0, 0, -29)},
		{"cus_2", "TRY", 5_000, now.AddDate(0, 0, -31)},
		{"cus_3", "TRY", 5_000, now.AddDate(0, 0, -1)},
		{"cus_4", "TRY", 5_000, now.AddDate(0, 0, -1)},
	})

	report, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"cus_1", "cus_3"}, h.members("custgrp_accounts"))
	assert.Equal(t, []string{"cus_1"}, h.members("custgrp_spenders"),
		"cus_2 spent before the window, cus_3 is in another country, cus_4 in none")
	assert.Equal(t, segment.Report{Segments: 2, Customers: 4, Added: 3}, report)
	assert.Equal(t, []string{"custgrp_accounts", "custgrp_spenders"}, h.customers.finished)
}

// TestAPassPagesEveryCustomer: the pages cover the ids end to end, the last one
// reaching to the end of the ids.
func TestAPassPagesEveryCustomer(t *testing.T) {
	t.Parallel()

	var people []person
	for i := range 1_001 {
		people = append(people, person{fmt.Sprintf("cus_%04d", i), i%2 == 0, now, ""})
	}
	h := newHarness([]seg{
		{"custgrp_accounts", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
	}, people, nil)

	report, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1_001, report.Customers)
	assert.Len(t, h.members("custgrp_accounts"), 501)
	require.Len(t, h.customers.pages, 3)
	assert.Equal(t, [][2]string{{"", "cus_0499"}, {"cus_0499", "cus_0999"}, {"cus_0999", ""}},
		[][2]string{
			{h.customers.pages[0].after, h.customers.pages[0].last},
			{h.customers.pages[1].after, h.customers.pages[1].last},
			{h.customers.pages[2].after, h.customers.pages[2].last},
		})
	assert.Empty(t, h.orders.asked, "a rule that reads no order asks for none")
}

// TestAMemberWhoNoLongerMatchesLeaves: the members are written, not added to.
func TestAMemberWhoNoLongerMatchesLeaves(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_accounts", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
	}, []person{{"cus_1", false, now, ""}, {"cus_2", true, now, ""}}, nil)
	h.customers.members["custgrp_accounts"] = map[string]bool{"cus_1": true, "cus_gone": true}

	report, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"cus_2"}, h.members("custgrp_accounts"), "a record that is gone leaves too")
	assert.Equal(t, 2, report.Removed)
}

// TestARuleReplacedDuringAPassStopsItsWrites: once a page is refused for a new
// rule, the pass writes nothing more for it and does not mark it evaluated.
func TestARuleReplacedDuringAPassStopsItsWrites(t *testing.T) {
	t.Parallel()

	var people []person
	for i := range 600 {
		people = append(people, person{fmt.Sprintf("cus_%04d", i), true, now, ""})
	}
	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
		{"custgrp_b", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
	}, people, nil)
	h.customers.ruleAt = map[string]time.Time{"custgrp_a": now}

	report, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, report.Replaced)
	assert.Equal(t, 1, report.Segments)
	assert.Equal(t, []string{"custgrp_b"}, h.customers.finished)
	written := 0
	for _, p := range h.customers.pages {
		if p.groupID == "custgrp_a" {
			written++
		}
	}
	assert.Equal(t, 1, written, "the refused page is the last one asked")
}

// TestAnUnreadableRuleDoesNotStopTheOthers: a word the flow does not know makes
// the pass incomplete, and the other segments are written.
func TestAnUnreadableRuleDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"conditions":[{"attribute":"lifetime_value","operator":"gt","value":1}]}`},
		{"custgrp_b", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
	}, []person{{"cus_1", true, now, ""}}, nil)

	_, err := h.flow.Pass(context.Background())

	require.Error(t, err)
	assert.Equal(t, segment.CodePassIncomplete, errors.CodeOf(err))
	assert.Equal(t, []string{"custgrp_b"}, h.customers.finished)
	assert.Empty(t, h.members("custgrp_a"))
}

// TestAFailedWriteStopsOnlyItsSegment: a page one segment could not write is
// reported, and the others go on.
func TestAFailedWriteStopsOnlyItsSegment(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
		{"custgrp_b", setAt, `{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`},
	}, []person{{"cus_1", true, now, ""}}, nil)
	h.customers.applyErr = map[string]error{"custgrp_a": errors.Unavailable("db_down", "the database is away")}

	_, err := h.flow.Pass(context.Background())

	require.Error(t, err)
	assert.Equal(t, segment.CodePassIncomplete, errors.CodeOf(err))
	assert.Equal(t, []string{"custgrp_b"}, h.customers.finished)
}

// TestAFailedOrderReadStopsThePass: no segment is written past a page whose
// orders could not be read, and none is marked evaluated.
func TestAFailedOrderReadStopsThePass(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"conditions":[{"attribute":"order_count","operator":"gte","value":1}]}`},
	}, []person{{"cus_1", true, now, ""}}, nil)
	h.orders.err = errors.Unavailable("db_down", "the database is away")

	_, err := h.flow.Pass(context.Background())

	require.Error(t, err)
	assert.Empty(t, h.customers.pages)
	assert.Empty(t, h.customers.finished)
}

// TestEachWindowIsReadOncePerPage: rules that share a window share the order
// read; another window is another read, and zero reads the whole history.
func TestEachWindowIsReadOncePerPage(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"window_days":30,"conditions":[{"attribute":"order_count","operator":"gte","value":1}]}`},
		{"custgrp_b", setAt, `{"window_days":30,"conditions":[{"attribute":"order_count","operator":"gte","value":2}]}`},
		{"custgrp_c", setAt, `{"conditions":[{"attribute":"order_count","operator":"gte","value":2}]}`},
	}, []person{{"cus_1", true, now, ""}}, []order{
		{"cus_1", "TRY", 100, now.AddDate(0, 0, -1)},
		{"cus_1", "EUR", 100, now.AddDate(0, 0, -40)},
	})

	_, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	require.Len(t, h.orders.asked, 2)
	require.NotNil(t, h.orders.asked[0])
	assert.Equal(t, now.AddDate(0, 0, -30), *h.orders.asked[0])
	assert.Nil(t, h.orders.asked[1])
	assert.Equal(t, []string{"cus_1"}, h.members("custgrp_a"))
	assert.Empty(t, h.members("custgrp_b"), "one order in the window")
	assert.Equal(t, []string{"cus_1"}, h.members("custgrp_c"), "two in the whole history, in any currency")
}

// TestNetSpendIsInTheRulesCurrency: a spend in another currency is not
// converted and does not count.
func TestNetSpendIsInTheRulesCurrency(t *testing.T) {
	t.Parallel()

	h := newHarness([]seg{
		{"custgrp_a", setAt, `{"currency_code":"TRY","conditions":[{"attribute":"net_spend","operator":"gte","value":1000}]}`},
	}, []person{{"cus_1", true, now, ""}, {"cus_2", true, now, ""}}, []order{
		{"cus_1", "EUR", 5_000, now},
		{"cus_2", "TRY", 400, now},
		{"cus_2", "TRY", 600, now},
	})

	_, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{"cus_2"}, h.members("custgrp_a"))
}

// TestTheRecordConditions: whole days of age, and a customer without a country
// matches no condition on it, not even ne or nin.
func TestTheRecordConditions(t *testing.T) {
	t.Parallel()

	people := []person{
		{"cus_day", true, now.Add(-24 * time.Hour), "TR"},
		{"cus_almost", true, now.Add(-24*time.Hour + time.Second), "DE"},
		{"cus_nowhere", true, now.AddDate(-1, 0, 0), ""},
	}
	for rule, want := range map[string][]string{
		`{"attribute":"account_age_days","operator":"gte","value":1}`:   {"cus_day", "cus_nowhere"},
		`{"attribute":"account_age_days","operator":"lt","value":1}`:    {"cus_almost"},
		`{"attribute":"account_age_days","operator":"eq","value":1}`:    {"cus_day"},
		`{"attribute":"account_age_days","operator":"ne","value":1}`:    {"cus_almost", "cus_nowhere"},
		`{"attribute":"account_age_days","operator":"gt","value":1}`:    {"cus_nowhere"},
		`{"attribute":"account_age_days","operator":"lte","value":1}`:   {"cus_almost", "cus_day"},
		`{"attribute":"country_code","operator":"eq","value":"TR"}`:     {"cus_day"},
		`{"attribute":"country_code","operator":"ne","value":"TR"}`:     {"cus_almost"},
		`{"attribute":"country_code","operator":"in","values":["DE"]}`:  {"cus_almost"},
		`{"attribute":"country_code","operator":"nin","values":["DE"]}`: {"cus_day"},
		`{"attribute":"has_account","operator":"eq","value":false}`:     nil,
	} {
		h := newHarness([]seg{{"custgrp_a", setAt, `{"conditions":[` + rule + `]}`}}, people, nil)

		_, err := h.flow.Pass(context.Background())

		require.NoError(t, err, rule)
		assert.Equal(t, want, h.members("custgrp_a"), rule)
	}
}

// TestAPreviewCountsAndWritesNothing: the preview reads every customer once and
// writes no page.
func TestAPreviewCountsAndWritesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(nil, []person{{"cus_1", true, now, ""}, {"cus_2", false, now, ""}}, []order{
		{"cus_1", "TRY", 100, now},
	})

	members, customers, err := h.flow.Preview(context.Background(),
		json.RawMessage(`{"conditions":[{"attribute":"order_count","operator":"gte","value":1}]}`))

	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, []int{members, customers})
	assert.Empty(t, h.customers.pages)

	raw, err := segment.NewInterop(h.flow).PreviewSegmentJSON(context.Background(),
		json.RawMessage(`{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"members":1,"customers":2}`, string(raw))

	_, _, err = h.flow.Preview(context.Background(), json.RawMessage(`{"conditions":[]}`))
	assert.Equal(t, segment.CodeRuleUnreadable, errors.CodeOf(err))
}

// TestNoSegmentReadsNoCustomer: a pass with nothing to write pages nothing.
func TestNoSegmentReadsNoCustomer(t *testing.T) {
	t.Parallel()

	h := newHarness(nil, []person{{"cus_1", true, now, ""}}, nil)

	report, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, segment.Report{}, report)
	assert.Empty(t, h.customers.pages)
}
