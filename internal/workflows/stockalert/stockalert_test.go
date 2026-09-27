package stockalert_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// mark is one marked wishlist item in the fake customer module.
type mark struct {
	customer, variant string
	channels          []string
	armedAt           *time.Time
}

// fakeCustomers pages marks in key order, arms and clears them as the module
// does.
type fakeCustomers struct {
	marks  []*mark
	emails map[string]string
	clock  time.Time
	pages  int
}

func (f *fakeCustomers) StockAlertsJSON(
	_ context.Context, afterCustomer, afterVariant string, limit int,
) (json.RawMessage, error) {
	f.pages++
	slices.SortFunc(f.marks, func(a, b *mark) int {
		if c := strings.Compare(a.customer, b.customer); c != 0 {
			return c
		}
		return strings.Compare(a.variant, b.variant)
	})
	type row struct {
		CustomerID      string     `json:"customer_id"`
		VariantID       string     `json:"variant_id"`
		SalesChannelIDs []string   `json:"sales_channel_ids"`
		ArmedAt         *time.Time `json:"armed_at"`
	}
	out := []row{}
	for _, m := range f.marks {
		if m.customer < afterCustomer || (m.customer == afterCustomer && m.variant <= afterVariant) {
			continue
		}
		out = append(out, row{m.customer, m.variant, m.channels, m.armedAt})
		if len(out) == limit {
			break
		}
	}

	return json.Marshal(out)
}

func (f *fakeCustomers) find(customer, variant string) *mark {
	for _, m := range f.marks {
		if m.customer == customer && m.variant == variant {
			return m
		}
	}
	return nil
}

func (f *fakeCustomers) ArmStockAlert(_ context.Context, customer, variant string) (bool, error) {
	m := f.find(customer, variant)
	if m == nil || m.armedAt != nil {
		return false, nil
	}
	at := f.clock
	m.armedAt = &at

	return true, nil
}

func (f *fakeCustomers) ClearStockAlert(_ context.Context, customer, variant string, armedAt time.Time) (bool, error) {
	m := f.find(customer, variant)
	if m == nil || m.armedAt == nil || !m.armedAt.Equal(armedAt) {
		return false, nil
	}
	f.marks = slices.DeleteFunc(f.marks, func(x *mark) bool { return x == m })

	return true, nil
}

func (f *fakeCustomers) CustomerEmail(_ context.Context, customer string) (string, error) {
	email, ok := f.emails[customer]
	if !ok {
		return "", errors.NotFound("customer_not_found", "no customer %s", customer)
	}
	return email, nil
}

// fakeCatalog answers stock per variant, and a variant in a channel set it does
// not name answers false.
type fakeCatalog struct {
	inStock  map[string]bool
	channels map[string][]string
	asked    [][]string
	err      error
}

func (f *fakeCatalog) VariantsInStock(_ context.Context, variantIDs, channels []string) (map[string]bool, error) {
	f.asked = append(f.asked, channels)
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]bool{}
	for _, id := range variantIDs {
		if want, scoped := f.channels[id]; scoped && !slices.Equal(want, channels) {
			continue
		}
		out[id] = f.inStock[id]
	}
	return out, nil
}

// sent is one mail the fake notifier was handed.
type sent struct {
	reference, to string
	data          map[string]string
}

// fakeNotifier sends a (template, reference) pair once, as the module does.
type fakeNotifier struct {
	sent []sent
	err  error
}

func (f *fakeNotifier) Send(_ context.Context, template, _, reference, to string, data map[string]string) error {
	if template != stockalert.TemplateBackInStock {
		return fmt.Errorf("unexpected template %q", template)
	}
	if f.err != nil {
		return f.err
	}
	for _, s := range f.sent {
		if s.reference == reference {
			return nil
		}
	}
	f.sent = append(f.sent, sent{reference: reference, to: to, data: data})
	return nil
}

// fakeReader answers the catalog names the mail carries.
type fakeReader struct{}

func (fakeReader) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	ids, _ := spec.Filters["ids"].([]string)
	switch spec.Entity {
	case stockalert.EntityVariant:
		return []query.Record{{query.IDField: ids[0], "title": "Blue", "product_id": "prod_" + ids[0]}}, nil
	case stockalert.EntityProduct:
		return []query.Record{{query.IDField: ids[0], "title": "A mug", "handle": "a-mug"}}, nil
	}
	return nil, errors.Invalid("unknown_entity", "no entity %s", spec.Entity)
}

// harness is one customer who marked a variant in one channel.
type harness struct {
	customers *fakeCustomers
	catalog   *fakeCatalog
	notifier  *fakeNotifier
	flow      *stockalert.Workflow
}

func newHarness() *harness {
	h := &harness{
		customers: &fakeCustomers{
			marks:  []*mark{{customer: "cus_1", variant: "var_mug", channels: []string{"sc_1"}}},
			emails: map[string]string{"cus_1": "shopper@example.com"},
			clock:  time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		},
		catalog:  &fakeCatalog{inStock: map[string]bool{}},
		notifier: &fakeNotifier{},
	}
	h.flow = stockalert.New(h.customers, h.catalog, h.notifier, fakeReader{}, slog.New(slog.DiscardHandler))

	return h
}

// TestAVariantThatComesBackIsMailedOnce is ADR 0215: out of stock arms the
// mark, back in stock mails the customer and clears it, and nothing more comes.
func TestAVariantThatComesBackIsMailedOnce(t *testing.T) {
	t.Parallel()

	h := newHarness()
	ctx := context.Background()

	armed, mailed, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 0}, []int{armed, mailed}, "out of stock arms the mark")

	h.catalog.inStock["var_mug"] = true
	armed, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1}, []int{armed, mailed})
	require.Len(t, h.notifier.sent, 1)
	assert.Equal(t, "shopper@example.com", h.notifier.sent[0].to)
	assert.Equal(t, map[string]string{
		stockalert.DataVariantID: "var_mug", stockalert.DataVariantTitle: "Blue",
		stockalert.DataProductID: "prod_var_mug", stockalert.DataProductTitle: "A mug",
		stockalert.DataHandle: "a-mug",
	}, h.notifier.sent[0].data)
	assert.Empty(t, h.customers.marks, "the mail clears the mark")

	_, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Zero(t, mailed)
	assert.Len(t, h.notifier.sent, 1)
}

// TestAMarkOnAVariantInStockWaitsForItToRunOut: nothing is back that never
// left.
func TestAMarkOnAVariantInStockWaitsForItToRunOut(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.catalog.inStock["var_mug"] = true

	armed, mailed, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []int{0, 0}, []int{armed, mailed})
	assert.Empty(t, h.notifier.sent)
	assert.Nil(t, h.customers.marks[0].armedAt)
}

// TestTheStockIsJudgedInTheMarksChannels: stock in another channel's
// warehouses does not mail.
func TestTheStockIsJudgedInTheMarksChannels(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.catalog.channels = map[string][]string{"var_mug": {"sc_other"}}
	h.catalog.inStock["var_mug"] = true
	at := h.customers.clock
	h.customers.marks[0].armedAt = &at

	_, mailed, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Zero(t, mailed)
	assert.Equal(t, [][]string{{"sc_1"}}, h.catalog.asked)
}

// TestAPassThatStopsAfterTheMailSendsNothingTwice: the reference names the
// arming, so a second pass finds the mail sent and only clears.
func TestAPassThatStopsAfterTheMailSendsNothingTwice(t *testing.T) {
	t.Parallel()

	h := newHarness()
	at := h.customers.clock
	h.customers.marks[0].armedAt = &at
	h.catalog.inStock["var_mug"] = true
	h.notifier.sent = []sent{{reference: fmt.Sprintf("cus_1:var_mug:%d", at.UnixNano())}}

	_, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Len(t, h.notifier.sent, 1, "the same reference is not sent again")
	assert.Empty(t, h.customers.marks)
}

// TestAFailedMailKeepsTheMark: the mark stays for the next pass, and the
// failure is reported without stopping the other marks.
func TestAFailedMailKeepsTheMark(t *testing.T) {
	t.Parallel()

	h := newHarness()
	at := h.customers.clock
	h.customers.marks[0].armedAt = &at
	h.customers.marks = append(h.customers.marks, &mark{customer: "cus_2", variant: "var_cap", channels: []string{"sc_1"}})
	h.catalog.inStock["var_mug"] = true
	h.notifier.err = errors.Unavailable("mail_down", "the mail gateway did not answer")

	armed, _, err := h.flow.Pass(context.Background())

	require.Error(t, err)
	assert.Equal(t, stockalert.CodePassIncomplete, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
	assert.Equal(t, 1, armed, "the other mark was armed all the same")
	assert.Len(t, h.customers.marks, 2, "the failed mark stays")
}

// TestAPassReadsEveryPage: the hundred-and-first mark is handled.
func TestAPassReadsEveryPage(t *testing.T) {
	t.Parallel()

	h := newHarness()
	for i := range 100 {
		h.customers.marks = append(h.customers.marks,
			&mark{customer: fmt.Sprintf("cus_%03d", i+2), variant: "var_x", channels: []string{"sc_1"}})
	}

	armed, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 101, armed)
	assert.Equal(t, 2, h.customers.pages)
}

// TestAMarkWithNoChannelIsAskedWithNone: nil is not the empty set.
func TestAMarkWithNoChannelIsAskedWithNone(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.customers.marks = append(h.customers.marks, &mark{customer: "cus_2", variant: "var_cap"},
		&mark{customer: "cus_3", variant: "var_hat", channels: []string{}})

	_, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	require.Len(t, h.catalog.asked, 3, "three channel sets, three questions")
	assert.Contains(t, h.catalog.asked, []string(nil))
	assert.Contains(t, h.catalog.asked, []string{})
}
