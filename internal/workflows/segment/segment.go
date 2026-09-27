// Package segment writes the members of the customer groups a rule decides
// (ADR 0217).
//
// It is a workflow because a rule reads two modules (ADR 0006): the customer
// module holds the segments, the customers' own records and the memberships,
// and the order module knows what each customer ordered. Neither imports the
// other, and this package imports neither.
//
// # A pass
//
// A pass reads the segments, then pages the live customers in id order. For
// each page it asks the order module once per window the rules read, evaluates
// every rule, and hands each segment its members among that page's ids: the
// customer module puts them in and takes every other member in the same id
// range out. A short page reaches to the end of the ids, so a member whose
// record is gone leaves too. When the last page is written, each segment is
// marked evaluated.
//
// # A rule replaced during a pass
//
// A page is written under the group's lock and only while the rule is still
// the one the pass read. Once it is not, the pass writes nothing more for that
// segment, and the next pass evaluates the new rule from the start.
package segment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
)

// The container names this flow resolves, spelled by hand because it imports
// no module; internal/arch pins the surfaces behind them.
const (
	ServiceCustomers = "customer.service"
	ServiceOrders    = "order.interop"
)

// InteropName is the name the flow's own surface is provided under, where the
// customer module's preview endpoint finds it.
const InteropName = "workflows.segment.interop"

// The attributes and operators of a rule, spelled again from the customer
// module's vocabulary; internal/arch holds the two copies to each other.
const (
	AttrHasAccount     = "has_account"
	AttrAccountAgeDays = "account_age_days"
	AttrCountryCode    = "country_code"
	AttrOrderCount     = "order_count"
	AttrNetSpend       = "net_spend"

	OpEq  = "eq"
	OpNe  = "ne"
	OpGt  = "gt"
	OpGte = "gte"
	OpLt  = "lt"
	OpLte = "lte"
	OpIn  = "in"
	OpNin = "nin"
)

// Operators is the vocabulary this flow evaluates: each attribute and the
// operators it takes.
var Operators = map[string][]string{
	AttrHasAccount:     {OpEq},
	AttrAccountAgeDays: {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte},
	AttrCountryCode:    {OpEq, OpNe, OpIn, OpNin},
	AttrOrderCount:     {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte},
	AttrNetSpend:       {OpEq, OpNe, OpGt, OpGte, OpLt, OpLte},
}

// PageSize is how many customers one read takes, and how many the order module
// is asked to total at once; internal/arch holds it under both modules' caps.
const PageSize = 500

// CodePassIncomplete reports a pass that could not write every segment.
const CodePassIncomplete = "segment_pass_incomplete"

// CodeNotReady reports a flow that could not be wired or a record it could not
// read.
const CodeNotReady = "segment_not_ready"

// CodeRuleUnreadable reports a rule the flow's vocabulary does not know.
const CodeRuleUnreadable = "segment_rule_unreadable"

// Customers is the customer module as the flow needs it.
type Customers interface {
	SegmentsJSON(ctx context.Context) (json.RawMessage, error)
	SegmentFactsJSON(ctx context.Context, afterCustomerID string, limit int) (json.RawMessage, error)
	ApplySegmentPage(
		ctx context.Context, groupID string, setAt time.Time, afterCustomerID, lastCustomerID string, members []string,
	) (added, removed int, applied bool, err error)
	FinishSegment(ctx context.Context, groupID string, setAt, evaluatedAt time.Time) (bool, error)
}

// Orders is the order module as the flow needs it.
type Orders interface {
	CustomerOrderTotalsJSON(ctx context.Context, customerIDs []string, since *time.Time) (json.RawMessage, error)
}

// Workflow evaluates the segments.
type Workflow struct {
	customers Customers
	orders    Orders
	clock     func() time.Time
	log       *slog.Logger
}

// New builds the flow; a nil clock is time.Now and a nil log discards.
func New(customers Customers, orders Orders, clock func() time.Time, log *slog.Logger) *Workflow {
	if clock == nil {
		clock = time.Now
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Workflow{customers: customers, orders: orders, clock: clock, log: log}
}

// Report is what one pass did.
type Report struct {
	// Segments is how many segments the pass wrote to the end.
	Segments int
	// Replaced is how many it stopped writing because their rule was replaced.
	Replaced int
	// Customers is how many customers it read.
	Customers int
	// Added and Removed are the memberships it wrote.
	Added, Removed int
}

// segment is one segment as the customer module pages it.
type segment struct {
	GroupID string    `json:"group_id"`
	SetAt   time.Time `json:"set_at"`
	Rule    rule      `json:"rule"`
}

// rule and condition are a segment's rule as the customer module stores it.
type rule struct {
	CurrencyCode string      `json:"currency_code"`
	WindowDays   int32       `json:"window_days"`
	Conditions   []condition `json:"conditions"`
}

type condition struct {
	Attribute string          `json:"attribute"`
	Operator  string          `json:"operator"`
	Value     json.RawMessage `json:"value"`
	Values    []string        `json:"values"`
}

// customer is one customer as the customer module pages them.
type customer struct {
	CustomerID  string    `json:"customer_id"`
	HasAccount  bool      `json:"has_account"`
	CreatedAt   time.Time `json:"created_at"`
	CountryCode string    `json:"country_code"`
}

// orderTotal is one customer's orders in one currency.
type orderTotal struct {
	CustomerID   string `json:"customer_id"`
	CurrencyCode string `json:"currency_code"`
	Orders       int64  `json:"orders"`
	NetSpend     int64  `json:"net_spend"`
}

// history is a customer's orders in one window: the count in every currency,
// and the net spend per currency.
type history struct {
	orders int64
	spend  map[string]int64
}

// compiled is a rule whose values are read into their types.
type compiled struct {
	currency    string
	windowDays  int32
	readsOrders bool
	conditions  []test
}

// test is one condition with its value in its type.
type test struct {
	attribute, operator string
	number              int64
	flag                bool
	text                string
	texts               []string
}

// compile reads a rule's values into their types; a word the flow does not
// know makes the rule unreadable rather than guessed at.
func compile(r rule) (compiled, error) {
	out := compiled{currency: r.CurrencyCode, windowDays: r.WindowDays}
	if len(r.Conditions) == 0 {
		return compiled{}, errors.Internal(CodeRuleUnreadable, "a rule with no condition")
	}
	for _, c := range r.Conditions {
		if !slices.Contains(Operators[c.Attribute], c.Operator) {
			return compiled{}, errors.Internal(CodeRuleUnreadable, "%s %s is no condition this flow evaluates",
				c.Attribute, c.Operator)
		}
		t := test{attribute: c.Attribute, operator: c.Operator, texts: c.Values}
		var err error
		switch {
		case c.Operator == OpIn || c.Operator == OpNin:
		case c.Attribute == AttrHasAccount:
			err = json.Unmarshal(c.Value, &t.flag)
		case c.Attribute == AttrCountryCode:
			err = json.Unmarshal(c.Value, &t.text)
		default:
			t.number, err = strconv.ParseInt(string(c.Value), 10, 64)
		}
		if err != nil {
			return compiled{}, errors.Wrap(err, errors.KindInternal, CodeRuleUnreadable,
				"the value of %s %s could not be read", c.Attribute, c.Operator)
		}
		out.readsOrders = out.readsOrders || c.Attribute == AttrOrderCount || c.Attribute == AttrNetSpend
		out.conditions = append(out.conditions, t)
	}

	return out, nil
}

// matches reports whether a customer satisfies every condition.
func (c *compiled) matches(who *customer, orders history, now time.Time) bool {
	for i := range c.conditions {
		t := &c.conditions[i]
		switch t.attribute {
		case AttrHasAccount:
			if who.HasAccount != t.flag {
				return false
			}
		case AttrAccountAgeDays:
			if !compare(int64(now.Sub(who.CreatedAt)/(24*time.Hour)), t.operator, t.number) {
				return false
			}
		case AttrCountryCode:
			if !place(who.CountryCode, t) {
				return false
			}
		case AttrOrderCount:
			if !compare(orders.orders, t.operator, t.number) {
				return false
			}
		case AttrNetSpend:
			if !compare(orders.spend[c.currency], t.operator, t.number) {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// compare applies a numeric operator.
func compare(have int64, operator string, want int64) bool {
	switch operator {
	case OpEq:
		return have == want
	case OpNe:
		return have != want
	case OpGt:
		return have > want
	case OpGte:
		return have >= want
	case OpLt:
		return have < want
	case OpLte:
		return have <= want
	}

	return false
}

// place applies a country operator; a customer with no country matches none.
func place(country string, t *test) bool {
	if country == "" {
		return false
	}
	switch t.operator {
	case OpEq:
		return country == t.text
	case OpNe:
		return country != t.text
	case OpIn:
		return slices.Contains(t.texts, country)
	case OpNin:
		return !slices.Contains(t.texts, country)
	}

	return false
}

// since is the start of a window of days before now; zero is no window.
func since(now time.Time, days int32) *time.Time {
	if days == 0 {
		return nil
	}
	start := now.Add(-time.Duration(days) * 24 * time.Hour)

	return &start
}

// histories reads the order history of a page's customers in every window the
// rules read, keyed by window.
func (w *Workflow) histories(
	ctx context.Context, rules []*compiled, customers []customer, now time.Time,
) (map[int32]map[string]history, error) {
	out := map[int32]map[string]history{}
	ids := make([]string, 0, len(customers))
	for i := range customers {
		ids = append(ids, customers[i].CustomerID)
	}
	for _, r := range rules {
		if !r.readsOrders || len(ids) == 0 {
			continue
		}
		if _, read := out[r.windowDays]; read {
			continue
		}
		raw, err := w.orders.CustomerOrderTotalsJSON(ctx, ids, since(now, r.windowDays))
		if err != nil {
			return nil, err
		}
		var totals []orderTotal
		if err := json.Unmarshal(raw, &totals); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeNotReady, "the order totals could not be read")
		}
		byCustomer := map[string]history{}
		for _, total := range totals {
			h := byCustomer[total.CustomerID]
			if h.spend == nil {
				h.spend = map[string]int64{}
			}
			h.orders += total.Orders
			h.spend[total.CurrencyCode] += total.NetSpend
			byCustomer[total.CustomerID] = h
		}
		out[r.windowDays] = byCustomer
	}

	return out, nil
}

// customersAfter reads one page of customers.
func (w *Workflow) customersAfter(ctx context.Context, after string) ([]customer, error) {
	raw, err := w.customers.SegmentFactsJSON(ctx, after, PageSize)
	if err != nil {
		return nil, err
	}
	var out []customer
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeNotReady, "the customers could not be read")
	}

	return out, nil
}

// walk pages every live customer once and hands each page, with its order
// histories and the id range it covers, to visit; the last page's range
// reaches to the end of the ids.
func (w *Workflow) walk(
	ctx context.Context, rules []*compiled, now time.Time,
	visit func(customers []customer, histories map[int32]map[string]history, after, last string) error,
) (read int, err error) {
	after := ""
	for {
		customers, err := w.customersAfter(ctx, after)
		if err != nil {
			return read, err
		}
		histories, err := w.histories(ctx, rules, customers, now)
		if err != nil {
			return read, err
		}
		read += len(customers)
		last := ""
		if len(customers) == PageSize {
			last = customers[len(customers)-1].CustomerID
		}
		if err := visit(customers, histories, after, last); err != nil {
			return read, err
		}
		if last == "" {
			return read, nil
		}
		after = last
	}
}

// Pass evaluates every segment and writes its members.
//
// A segment whose rule cannot be read or whose write fails is reported and
// does not stop the others; a page of customers or orders that cannot be read
// stops the pass, since no segment can be written past it. What was written
// before a failure stays, and the next pass writes every segment again.
func (w *Workflow) Pass(ctx context.Context) (Report, error) {
	var report Report
	raw, err := w.customers.SegmentsJSON(ctx)
	if err != nil {
		return report, err
	}
	var segments []segment
	if err := json.Unmarshal(raw, &segments); err != nil {
		return report, errors.Wrap(err, errors.KindInternal, CodeNotReady, "the segments could not be read")
	}
	if len(segments) == 0 {
		return report, nil
	}

	now := w.clock()
	var failures []error
	live := make([]*segment, 0, len(segments))
	rules := make([]*compiled, 0, len(segments))
	for i := range segments {
		c, err := compile(segments[i].Rule)
		if err != nil {
			w.log.ErrorContext(ctx, "a segment rule could not be read", "group_id", segments[i].GroupID, "error", err)
			failures = append(failures, err)

			continue
		}
		live = append(live, &segments[i])
		rules = append(rules, &c)
	}
	if len(live) == 0 {
		return report, errors.Wrap(failures[0], errors.KindOf(failures[0]), CodePassIncomplete,
			"%d segments could not be written", len(failures))
	}
	stopped := make([]bool, len(live))

	read, walkErr := w.walk(ctx, rules, now, func(
		customers []customer, histories map[int32]map[string]history, after, last string,
	) error {
		for i, s := range live {
			if stopped[i] {
				continue
			}
			var members []string
			for j := range customers {
				if rules[i].matches(&customers[j], histories[rules[i].windowDays][customers[j].CustomerID], now) {
					members = append(members, customers[j].CustomerID)
				}
			}
			added, removed, applied, err := w.customers.ApplySegmentPage(ctx, s.GroupID, s.SetAt, after, last, members)
			switch {
			case err != nil:
				w.log.ErrorContext(ctx, "a segment page could not be written", "group_id", s.GroupID, "error", err)
				failures = append(failures, err)
				stopped[i] = true
			case !applied:
				report.Replaced++
				stopped[i] = true
			default:
				report.Added += added
				report.Removed += removed
			}
		}

		return nil
	})
	report.Customers = read
	if walkErr != nil {
		return report, walkErr
	}
	for i, s := range live {
		if stopped[i] {
			continue
		}
		if _, err := w.customers.FinishSegment(ctx, s.GroupID, s.SetAt, now); err != nil {
			failures = append(failures, err)

			continue
		}
		report.Segments++
	}
	if len(failures) > 0 {
		return report, errors.Wrap(failures[0], errors.KindOf(failures[0]), CodePassIncomplete,
			"%d segments could not be written", len(failures))
	}

	return report, nil
}

// Preview counts the live customers a rule would take in, and how many were
// read, without writing anything. The rule is one the customer module has
// normalized.
func (w *Workflow) Preview(ctx context.Context, ruleJSON json.RawMessage) (members, customers int, err error) {
	var r rule
	if err := json.Unmarshal(ruleJSON, &r); err != nil {
		return 0, 0, errors.Wrap(err, errors.KindInvalid, CodeRuleUnreadable, "the rule could not be read")
	}
	c, err := compile(r)
	if err != nil {
		return 0, 0, err
	}
	now := w.clock()
	read, err := w.walk(ctx, []*compiled{&c}, now, func(
		page []customer, histories map[int32]map[string]history, _, _ string,
	) error {
		for i := range page {
			if c.matches(&page[i], histories[c.windowDays][page[i].CustomerID], now) {
				members++
			}
		}

		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	return members, read, nil
}

// Interop is the flow's surface for the customer module's preview endpoint.
type Interop struct {
	flow *Workflow
}

// NewInterop wraps the flow.
func NewInterop(flow *Workflow) *Interop { return &Interop{flow: flow} }

// PreviewSegmentJSON is [Workflow.Preview] as JSON:
//
//	{"members": 12, "customers": 480}
func (i *Interop) PreviewSegmentJSON(ctx context.Context, rule json.RawMessage) (json.RawMessage, error) {
	members, customers, err := i.flow.Preview(ctx, rule)
	if err != nil {
		return nil, err
	}

	return json.Marshal(map[string]int{"members": members, "customers": customers})
}

// FromContainer wires the flow from a built container.
func FromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady, "the segment flow cannot be wired without a container")
	}
	customers, err := resolve[Customers](c, ServiceCustomers)
	if err != nil {
		return nil, err
	}
	orders, err := resolve[Orders](c, ServiceOrders)
	if err != nil {
		return nil, err
	}

	return New(customers, orders, nil, log), nil
}

// resolve reads one service and says which name failed.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T

		return zero, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the segment flow needs %q and it could not be resolved", name)
	}

	return value, nil
}

// String describes a report for the job's line.
func (r Report) String() string {
	return fmt.Sprintf("wrote %d segments over %d customers: %d members added, %d removed; %d left for a new rule",
		r.Segments, r.Customers, r.Added, r.Removed, r.Replaced)
}

// PassReport is [Workflow.Pass] with its report written as the job's line; the
// line is written for a failed pass too, since what it wrote stays.
func (w *Workflow) PassReport(ctx context.Context) (string, error) {
	report, err := w.Pass(ctx)

	return report.String(), err
}
