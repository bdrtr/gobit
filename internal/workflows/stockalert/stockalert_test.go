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
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	"github.com/bdrtr/gobit/internal/workflows/stockalert"
)

// regionGone is a region the fake quote answers does not exist.
const regionGone = "reg_gone"

// mark is one marked wishlist item in the fake customer module: a stock mark,
// a price mark, or both.
type mark struct {
	customer, variant string
	stock             bool
	channels          []string
	armedAt           *time.Time

	priceMarked   *time.Time
	region        string
	priceChannels []string
	currency      string
	amount        *int64
}

// fakeCustomers pages marks in key order, arms and clears them as the module
// does.
type fakeCustomers struct {
	marks  []*mark
	emails map[string]string
	clock  time.Time
	pages  int
}

func (f *fakeCustomers) WishlistAlertsJSON(
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
		CustomerID           string     `json:"customer_id"`
		VariantID            string     `json:"variant_id"`
		StockAlert           bool       `json:"stock_alert"`
		SalesChannelIDs      []string   `json:"sales_channel_ids"`
		ArmedAt              *time.Time `json:"armed_at"`
		PriceAlert           bool       `json:"price_alert"`
		PriceMarkedAt        *time.Time `json:"price_marked_at"`
		PriceRegionID        string     `json:"price_region_id"`
		PriceSalesChannelIDs []string   `json:"price_sales_channel_ids"`
		PriceCurrencyCode    string     `json:"price_currency_code"`
		PriceAmount          *int64     `json:"price_amount"`
	}
	out := []row{}
	for _, m := range f.marks {
		if m.customer < afterCustomer || (m.customer == afterCustomer && m.variant <= afterVariant) {
			continue
		}
		out = append(out, row{m.customer, m.variant, m.stock, m.channels, m.armedAt,
			m.priceMarked != nil, m.priceMarked, m.region, m.priceChannels, m.currency, m.amount})
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
	if m == nil || !m.stock || m.armedAt != nil {
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
	m.stock, m.channels, m.armedAt = false, nil, nil
	f.drop(m)

	return true, nil
}

func (f *fakeCustomers) RecordPriceBaseline(
	_ context.Context, customer, variant string, markedAt time.Time, currency string, amount int64,
) (bool, error) {
	m := f.find(customer, variant)
	if m == nil || m.priceMarked == nil || !m.priceMarked.Equal(markedAt) || m.amount != nil {
		return false, nil
	}
	m.currency, m.amount = currency, &amount

	return true, nil
}

func (f *fakeCustomers) ClearPriceAlert(_ context.Context, customer, variant string, markedAt time.Time) (bool, error) {
	m := f.find(customer, variant)
	if m == nil || m.priceMarked == nil || !m.priceMarked.Equal(markedAt) {
		return false, nil
	}
	m.priceMarked, m.region, m.priceChannels, m.currency, m.amount = nil, "", nil, "", nil
	f.drop(m)

	return true, nil
}

// drop takes a mark with neither kind left off the page, as the query does.
func (f *fakeCustomers) drop(m *mark) {
	if !m.stock && m.priceMarked == nil {
		f.marks = slices.DeleteFunc(f.marks, func(x *mark) bool { return x == m })
	}
}

// fakePrices quotes per variant in one currency, and records what it was asked.
type fakePrices struct {
	currency string
	prices   map[string]int64
	asked    []string
	err      error
}

func (f *fakePrices) QuoteUnitPrices(
	_ context.Context, regionID, customerID string, variantIDs []string,
) (currency string, prices map[string]int64, err error) {
	f.asked = append(f.asked, regionID+"/"+customerID)
	if f.err != nil {
		return "", nil, f.err
	}
	if regionID == regionGone {
		return "", nil, errors.NotFound(cartwf.CodeQuoteRegionUnknown, "region %s does not exist", regionID)
	}
	out := map[string]int64{}
	for _, id := range variantIDs {
		if price, ok := f.prices[id]; ok {
			out[id] = price
		}
	}
	return f.currency, out, nil
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
	if template != stockalert.TemplateBackInStock && template != stockalert.TemplatePriceDrop {
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
	prices    *fakePrices
	notifier  *fakeNotifier
	flow      *stockalert.Workflow
}

func newHarness() *harness {
	h := &harness{
		customers: &fakeCustomers{
			marks:  []*mark{{customer: "cus_1", variant: "var_mug", stock: true, channels: []string{"sc_1"}}},
			emails: map[string]string{"cus_1": "shopper@example.com"},
			clock:  time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		},
		catalog:  &fakeCatalog{inStock: map[string]bool{}},
		prices:   &fakePrices{currency: "TRY", prices: map[string]int64{}},
		notifier: &fakeNotifier{},
	}
	h.flow = stockalert.New(h.customers, h.catalog, h.prices, h.notifier, fakeReader{},
		slog.New(slog.DiscardHandler))

	return h
}

// TestAVariantThatComesBackIsMailedOnce is ADR 0215: out of stock arms the
// mark, back in stock mails the customer and clears it, and nothing more comes.
func TestAVariantThatComesBackIsMailedOnce(t *testing.T) {
	t.Parallel()

	h := newHarness()
	ctx := context.Background()

	armed, _, mailed, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 0}, []int{armed, mailed}, "out of stock arms the mark")

	h.catalog.inStock["var_mug"] = true
	armed, _, mailed, err = h.flow.Pass(ctx)
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

	_, _, mailed, err = h.flow.Pass(ctx)
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

	armed, _, mailed, err := h.flow.Pass(context.Background())

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

	_, _, mailed, err := h.flow.Pass(context.Background())

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

	_, _, _, err := h.flow.Pass(context.Background())

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
	h.customers.marks = append(h.customers.marks,
		&mark{customer: "cus_2", variant: "var_cap", stock: true, channels: []string{"sc_1"}})
	h.catalog.inStock["var_mug"] = true
	h.notifier.err = errors.Unavailable("mail_down", "the mail gateway did not answer")

	armed, _, _, err := h.flow.Pass(context.Background())

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
			&mark{customer: fmt.Sprintf("cus_%03d", i+2), variant: "var_x", stock: true, channels: []string{"sc_1"}})
	}

	armed, _, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 101, armed)
	assert.Equal(t, 2, h.customers.pages)
}

// TestAMarkWithNoChannelIsAskedWithNone: nil is not the empty set.
func TestAMarkWithNoChannelIsAskedWithNone(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.customers.marks = append(h.customers.marks, &mark{customer: "cus_2", variant: "var_cap", stock: true},
		&mark{customer: "cus_3", variant: "var_hat", stock: true, channels: []string{}})

	_, _, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	require.Len(t, h.catalog.asked, 3, "three channel sets, three questions")
	assert.Contains(t, h.catalog.asked, []string(nil))
	assert.Contains(t, h.catalog.asked, []string{})
}

// priceHarness is one customer who marked a variant's price in one region and
// channel, shown there at 5,000.
func priceHarness() (*harness, *mark) {
	h := newHarness()
	marked := h.customers.clock.Add(-time.Hour)
	m := &mark{customer: "cus_1", variant: "var_mug", priceMarked: &marked, region: "reg_tr",
		priceChannels: []string{"sc_1"}}
	h.customers.marks = []*mark{m}
	h.catalog.inStock["var_mug"] = false
	h.prices.prices["var_mug"] = 5_000

	return h, m
}

// TestAPriceThatDropsIsMailedOnce is ADR 0216: the first pass records the
// price at the mark, a pass at the same price does nothing, and a lower price
// mails the customer the two prices once and clears the mark.
func TestAPriceThatDropsIsMailedOnce(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	ctx := context.Background()

	armed, recorded, mailed, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{0, 1, 0}, []int{armed, recorded, mailed})
	assert.Equal(t, [][]string{{"sc_1"}}, h.catalog.asked,
		"a price mark is shown in its own channels and asks nothing of the stock")
	require.NotNil(t, m.amount)
	assert.Equal(t, int64(5_000), *m.amount)
	assert.Equal(t, "TRY", m.currency)
	assert.Equal(t, []string{"reg_tr/cus_1"}, h.prices.asked, "the customer's price in the mark's region")

	_, _, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Zero(t, mailed, "the same price is not a drop")

	h.prices.prices["var_mug"] = 4_200
	_, _, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, mailed)
	require.Len(t, h.notifier.sent, 1)
	assert.Equal(t, "shopper@example.com", h.notifier.sent[0].to)
	assert.Equal(t, "5000", h.notifier.sent[0].data[stockalert.DataPreviousAmount])
	assert.Equal(t, "4200", h.notifier.sent[0].data[stockalert.DataAmount])
	assert.Equal(t, "TRY", h.notifier.sent[0].data[stockalert.DataCurrencyCode])
	assert.Empty(t, h.customers.marks, "the mail clears the mark")

	_, _, mailed, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	assert.Zero(t, mailed)
}

// TestAPriceThatRisesIsNotADrop: the price at the mark stays the one it
// was.
func TestAPriceThatRisesIsNotADrop(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	amount := int64(5_000)
	m.currency, m.amount = "TRY", &amount
	h.prices.prices["var_mug"] = 6_000

	_, recorded, mailed, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []int{0, 0}, []int{recorded, mailed})
	assert.Equal(t, int64(5_000), *m.amount)
}

// TestAPriceInAnotherCurrencyIsNotCompared: a region whose currency changed
// asks a price the mark's cannot be set against.
func TestAPriceInAnotherCurrencyIsNotCompared(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	amount := int64(5_000)
	m.currency, m.amount = "EUR", &amount
	h.prices.prices["var_mug"] = 100

	_, _, mailed, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Zero(t, mailed)
}

// TestAMarkInARegionThatIsGoneIsNotAFailure: a request may name any region, and
// an operator may delete one; the mark waits for nothing, and the pass prices
// the other marks and does not report itself incomplete.
func TestAMarkInARegionThatIsGoneIsNotAFailure(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	marked := *m.priceMarked
	gone := &mark{customer: "cus_2", variant: "var_mug", priceMarked: &marked, region: regionGone}
	h.customers.marks = append(h.customers.marks, gone)

	_, recorded, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, recorded, "the mark in a region that exists is priced")
	assert.Nil(t, gone.amount)
	assert.Contains(t, h.prices.asked, regionGone+"/cus_2")
}

// TestAQuoteThatFailsIsAFailure: a price the flow could not ask is not a
// region that is gone.
func TestAQuoteThatFailsIsAFailure(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	h.prices.err = errors.Unavailable("pricing_down", "the pricing module is away")

	_, recorded, _, err := h.flow.Pass(context.Background())

	require.Error(t, err)
	assert.Equal(t, stockalert.CodePassIncomplete, errors.CodeOf(err))
	assert.Zero(t, recorded)
	assert.Nil(t, m.amount)
}

// TestThePriceIsJudgedInThePriceMarksChannels: an item marked for both keeps
// two channel sets, and its price is asked where the price mark was set.
func TestThePriceIsJudgedInThePriceMarksChannels(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	m.stock, m.channels = true, []string{"sc_stock"}
	h.catalog.channels = map[string][]string{"var_mug": {"sc_1"}}

	_, recorded, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 1, recorded, "shown in the price mark's channels")
	assert.ElementsMatch(t, [][]string{{"sc_stock"}, {"sc_1"}}, h.catalog.asked)
}

// TestAMarkIsPricedForItsOwnCustomerRegionAndChannels: marks are asked
// together only when they share the customer, the region and the channels, since
// the price is the customer's in the region and the variant is shown in the
// channels.
func TestAMarkIsPricedForItsOwnCustomerRegionAndChannels(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	marked := *m.priceMarked
	h.customers.marks = append(h.customers.marks,
		&mark{customer: "cus_2", variant: "var_mug", priceMarked: &marked, region: "reg_tr", priceChannels: []string{"sc_1"}},
		&mark{customer: "cus_1", variant: "var_cup", priceMarked: &marked, region: "reg_de", priceChannels: []string{"sc_1"}},
		&mark{customer: "cus_1", variant: "var_pan", priceMarked: &marked, region: "reg_tr", priceChannels: []string{"sc_2"}},
	)
	h.prices.prices["var_cup"], h.prices.prices["var_pan"] = 700, 300

	_, recorded, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 4, recorded)
	assert.ElementsMatch(t, []string{"reg_tr/cus_1", "reg_tr/cus_2", "reg_de/cus_1", "reg_tr/cus_1"}, h.prices.asked)
	assert.ElementsMatch(t, [][]string{{"sc_1"}, {"sc_1"}, {"sc_1"}, {"sc_2"}}, h.catalog.asked)
}

// TestAPriceMarkedAgainAfterItsMailIsMailedAgain: a new mark is a new mail,
// which the notification module sends only under a reference it has not seen.
func TestAPriceMarkedAgainAfterItsMailIsMailedAgain(t *testing.T) {
	t.Parallel()

	h, _ := priceHarness()
	ctx := context.Background()
	_, _, _, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	h.prices.prices["var_mug"] = 4_200
	_, _, mailed, err := h.flow.Pass(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, mailed)

	again := h.customers.clock
	h.customers.marks = []*mark{{customer: "cus_1", variant: "var_mug", priceMarked: &again, region: "reg_tr",
		priceChannels: []string{"sc_1"}}}
	_, _, _, err = h.flow.Pass(ctx)
	require.NoError(t, err)
	h.prices.prices["var_mug"] = 4_000
	_, _, mailed, err = h.flow.Pass(ctx)

	require.NoError(t, err)
	assert.Equal(t, 1, mailed)
	require.Len(t, h.notifier.sent, 2, "the second mark's mail went")
	assert.NotEqual(t, h.notifier.sent[0].reference, h.notifier.sent[1].reference)
	assert.Equal(t, "4200", h.notifier.sent[1].data[stockalert.DataPreviousAmount])
}

// TestAVariantTheStorefrontDoesNotShowIsNotPriced: a product unpublished or
// out of the mark's channels records nothing and mails nothing.
func TestAVariantTheStorefrontDoesNotShowIsNotPriced(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	h.catalog.channels = map[string][]string{"var_mug": {"sc_other"}}

	_, recorded, _, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Zero(t, recorded)
	assert.Nil(t, m.amount)
}

// TestAPriceMarkAndAStockMarkOnOneItemAreTwoAnswers: the stock arms and the
// price records in one pass, each on its own.
func TestAPriceMarkAndAStockMarkOnOneItemAreTwoAnswers(t *testing.T) {
	t.Parallel()

	h, m := priceHarness()
	m.stock, m.channels = true, []string{"sc_1"}

	armed, recorded, mailed, err := h.flow.Pass(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []int{1, 1, 0}, []int{armed, recorded, mailed})
}
