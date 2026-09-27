package giftcardsale_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/workflows/giftcardsale"
)

// fakePayments answers the collection's amounts and issues each sale's card
// once, handing out its code only the first time.
type fakePayments struct {
	amount, captured int64
	issued           map[string]string
	references       []string
	amounts          []int64
}

//nolint:gocritic // The result count comes from the [giftcardsale.Payments] signature.
func (f *fakePayments) Collection(context.Context, string) (string, int64, int64, int64, int64, error) {
	return "captured", f.amount, f.amount, f.captured, 0, nil
}

func (f *fakePayments) IssueSoldGiftCard(
	_ context.Context, reference, _, _ string, amount int64,
) (cardID, code string, err error) {
	f.references, f.amounts = append(f.references, reference), append(f.amounts, amount)
	if card, ok := f.issued[reference]; ok {
		return card, "", nil
	}
	card := fmt.Sprintf("gcard_%d", len(f.issued)+1)
	f.issued[reference] = card

	return card, "CODE-" + card, nil
}

// fakeOrders answers the order's contact.
type fakeOrders struct{}

func (fakeOrders) OrderContactJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	return json.RawMessage(`{"order_id":"` + orderID + `","email":"buyer@example.com","currency_code":"TRY"}`), nil
}

// sent is one notification the fake notifier was handed.
type sent struct {
	template, reference, to string
	data                    map[string]string
}

// fakeNotifier records what it is asked to send.
type fakeNotifier struct {
	sent []sent
	err  error
}

func (f *fakeNotifier) Send(_ context.Context, template, _, reference, to string, data map[string]string) error {
	f.sent = append(f.sent, sent{template: template, reference: reference, to: to, data: data})

	return f.err
}

// fakeCatalog holds an order's lines and the catalog behind them.
type fakeCatalog struct {
	lines     []query.Record
	products  map[string]string
	giftcards map[string]bool
}

func (f *fakeCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	switch spec.Entity {
	case giftcardsale.EntityLineItem:
		end := min(spec.Offset+spec.Limit, len(f.lines))
		if spec.Offset >= len(f.lines) {
			return nil, nil
		}
		return f.lines[spec.Offset:end], nil
	case giftcardsale.EntityVariant:
		var out []query.Record
		ids, _ := spec.Filters["ids"].([]string)
		for _, id := range ids {
			out = append(out, query.Record{query.IDField: id, "product_id": f.products[id]})
		}
		return out, nil
	case giftcardsale.EntityProduct:
		var out []query.Record
		ids, _ := spec.Filters["ids"].([]string)
		for _, id := range ids {
			out = append(out, query.Record{query.IDField: id, giftcardsale.FieldIsGiftcard: f.giftcards[id]})
		}
		return out, nil
	}

	return nil, errors.Invalid("unknown_entity", "no entity %s", spec.Entity)
}

// fakeLinks binds the collection to its order.
type fakeLinks struct{ orders map[string][]string }

func (f fakeLinks) ListManyByTo(context.Context, string, []string) (map[string][]string, error) {
	return f.orders, nil
}

// harness is a paid order of two lines, the first selling two 5,000 cards.
type harness struct {
	payments *fakePayments
	notifier *fakeNotifier
	catalog  *fakeCatalog
	flow     *giftcardsale.Workflow
}

func newHarness() *harness {
	h := &harness{
		payments: &fakePayments{amount: 12_000, captured: 12_000, issued: map[string]string{}},
		notifier: &fakeNotifier{},
		catalog: &fakeCatalog{
			lines: []query.Record{
				{query.IDField: "line_card", "variant_id": "var_card", "quantity": int32(2), "unit_price": int64(5_000)},
				{query.IDField: "line_mug", "variant_id": "var_mug", "quantity": int32(1), "unit_price": int64(2_000)},
			},
			products:  map[string]string{"var_card": "prod_card", "var_mug": "prod_mug"},
			giftcards: map[string]bool{"prod_card": true},
		},
	}
	h.flow = giftcardsale.New(h.payments, fakeOrders{}, h.notifier, h.catalog,
		fakeLinks{orders: map[string][]string{"paycol_1": {"order_1"}}}, slog.New(slog.DiscardHandler))

	return h
}

// captured is the capture event of the harness's collection.
func captured() eventbus.Event {
	return eventbus.Event{Name: giftcardsale.TopicPaymentCaptured,
		Data: map[string]any{giftcardsale.FieldCollectionID: "paycol_1"}}
}

// TestAPaidOrderIssuesItsCardsAndMailsTheirCodes is ADR 0210: one card per
// unit of a gift card line, worth the line's unit price, each mailed once to
// the order's address.
func TestAPaidOrderIssuesItsCardsAndMailsTheirCodes(t *testing.T) {
	t.Parallel()

	h := newHarness()

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Equal(t, []string{"line_card:1", "line_card:2"}, h.payments.references, "the mug is not a gift card")
	assert.Equal(t, []int64{5_000, 5_000}, h.payments.amounts)
	require.Len(t, h.notifier.sent, 2)
	for i, message := range h.notifier.sent {
		card := fmt.Sprintf("gcard_%d", i+1)
		assert.Equal(t, giftcardsale.TemplateIssued, message.template)
		assert.Equal(t, card, message.reference, "one mail per card")
		assert.Equal(t, "buyer@example.com", message.to)
		assert.Equal(t, map[string]string{
			giftcardsale.DataCode: "CODE-" + card, giftcardsale.DataAmount: "5000",
			giftcardsale.DataCurrencyCode: "TRY", giftcardsale.DataOrderID: "order_1",
		}, message.data)
	}
}

// TestASecondDeliveryMailsNothing: the bus delivers at least once, and the
// cards the first delivery made are found without their codes.
func TestASecondDeliveryMailsNothing(t *testing.T) {
	t.Parallel()

	h := newHarness()
	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Len(t, h.payments.issued, 2, "no card was made twice")
	assert.Len(t, h.notifier.sent, 2, "no code was mailed twice")
}

// TestAPartlyCapturedCollectionWaits: a split payment captures twice, and the
// capture that completes it is the one that issues.
func TestAPartlyCapturedCollectionWaits(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.payments.captured = 7_000

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Empty(t, h.payments.references)
}

// TestACollectionNoOrderWasPlacedThroughIsLeftAlone: a delivery change or an
// exchange's difference is not an order's checkout.
func TestACollectionNoOrderWasPlacedThroughIsLeftAlone(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.flow = giftcardsale.New(h.payments, fakeOrders{}, h.notifier, h.catalog,
		fakeLinks{orders: map[string][]string{}}, slog.New(slog.DiscardHandler))

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Empty(t, h.payments.references)
}

// TestAMailThatFailsIsReported: the error goes back to the bus, which logs
// it; the card stands and the operator gives it a new code.
func TestAMailThatFailsIsReported(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.notifier.err = errors.Unavailable("mail_down", "the mail gateway did not answer")

	err := h.flow.HandleCaptured(context.Background(), captured())

	require.Error(t, err)
	assert.Len(t, h.payments.issued, 1, "the first card stands")
}

// TestALongOrderIsReadToTheEnd: a gift card on the order's hundred-and-first
// line is issued too.
func TestALongOrderIsReadToTheEnd(t *testing.T) {
	t.Parallel()

	h := newHarness()
	var lines []query.Record
	for i := range 100 {
		lines = append(lines, query.Record{query.IDField: fmt.Sprintf("line_%d", i), "variant_id": "var_mug",
			"quantity": int32(1), "unit_price": int64(100)})
	}
	lines = append(lines, query.Record{query.IDField: "line_card", "variant_id": "var_card",
		"quantity": int32(1), "unit_price": int64(5_000)})
	h.catalog.lines = lines

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Equal(t, []string{"line_card:1"}, h.payments.references)
}

// TestAnEventWithoutACollectionIsIgnored rather than retried for ever.
func TestAnEventWithoutACollectionIsIgnored(t *testing.T) {
	t.Parallel()

	h := newHarness()

	require.NoError(t, h.flow.HandleCaptured(context.Background(),
		eventbus.Event{Name: giftcardsale.TopicPaymentCaptured, Data: map[string]any{}}))

	assert.Empty(t, h.payments.references)
}
