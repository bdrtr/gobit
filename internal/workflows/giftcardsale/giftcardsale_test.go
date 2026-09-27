package giftcardsale_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/workflows/giftcardsale"
)

// fakePayments answers the collections' amounts and issues each sale's card
// once, handing out its code only the first time.
type fakePayments struct {
	amount     int64
	captured   map[string]int64
	issued     map[string]string
	references []string
	amounts    []int64
	lookups    [][]string
	// failFor makes the issue of that order's cards fail.
	failFor string
}

//nolint:gocritic // The result count comes from the [giftcardsale.Payments] signature.
func (f *fakePayments) Collection(_ context.Context, collectionID string) (string, int64, int64, int64, int64, error) {
	return "captured", f.amount, f.amount, f.captured[collectionID], 0, nil
}

func (f *fakePayments) IssueSoldGiftCard(
	_ context.Context, reference, orderID, _ string, amount int64,
) (cardID, code string, err error) {
	if orderID == f.failFor {
		return "", "", errors.Unavailable("payment_down", "the payment module did not answer")
	}
	f.references, f.amounts = append(f.references, reference), append(f.amounts, amount)
	if card, ok := f.issued[reference]; ok {
		return card, "", nil
	}
	card := fmt.Sprintf("gcard_%d", len(f.issued)+1)
	f.issued[reference] = card

	return card, "CODE-" + card, nil
}

func (f *fakePayments) SoldGiftCardReferences(_ context.Context, references []string) ([]string, error) {
	f.lookups = append(f.lookups, references)
	var found []string
	for _, reference := range references {
		if _, ok := f.issued[reference]; ok {
			found = append(found, reference)
		}
	}

	return found, nil
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

// fakeReader holds orders and their lines. It filters and pages the lines as
// the order module's provider does, newest order first, and answers the fields
// asked for and no others, so a flow that does not ask for a field does not
// get it.
type fakeReader struct {
	lines  []query.Record
	placed map[string]time.Time
	status map[string]string
}

func (f *fakeReader) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	switch spec.Entity {
	case giftcardsale.EntityOrder:
		id, _ := spec.Filters[query.IDField].(string)
		if _, ok := f.placed[id]; !ok {
			return nil, nil
		}
		return []query.Record{{query.IDField: id, giftcardsale.FieldStatus: f.status[id]}}, nil
	case giftcardsale.EntityLineItem:
	default:
		return nil, errors.Invalid("unknown_entity", "no entity %s", spec.Entity)
	}

	var matched []query.Record
	for _, line := range f.lines {
		orderID, _ := line[giftcardsale.FieldOrderID].(string)
		if want, ok := spec.Filters[giftcardsale.FieldOrderID]; ok && want != orderID {
			continue
		}
		if want, ok := spec.Filters[giftcardsale.FieldIsGiftcard]; ok && want != line[giftcardsale.FieldIsGiftcard] {
			continue
		}
		if since, ok := spec.Filters[giftcardsale.FilterPlacedFrom].(time.Time); ok && f.placed[orderID].Before(since) {
			continue
		}
		matched = append(matched, line)
	}
	slices.SortStableFunc(matched, func(a, b query.Record) int {
		orderA, _ := a[giftcardsale.FieldOrderID].(string)
		orderB, _ := b[giftcardsale.FieldOrderID].(string)

		return f.placed[orderB].Compare(f.placed[orderA])
	})
	if spec.Offset >= len(matched) {
		return nil, nil
	}
	var out []query.Record
	for _, line := range matched[spec.Offset:min(spec.Offset+spec.Limit, len(matched))] {
		record := query.Record{}
		for _, field := range spec.Fields {
			record[field] = line[field]
		}
		out = append(out, record)
	}

	return out, nil
}

// line is one order line as the read layer answers it.
func line(orderID, id string, quantity int32, unitPrice int64, giftcard bool) query.Record {
	return query.Record{query.IDField: id, giftcardsale.FieldOrderID: orderID, "quantity": quantity,
		"unit_price": unitPrice, giftcardsale.FieldIsGiftcard: giftcard}
}

// fakeLinks binds collections and orders both ways.
type fakeLinks struct{ orders, collections map[string][]string }

func (f fakeLinks) ListMany(_ context.Context, _ string, fromIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, id := range fromIDs {
		out[id] = f.collections[id]
	}

	return out, nil
}

func (f fakeLinks) ListManyByTo(context.Context, string, []string) (map[string][]string, error) {
	return f.orders, nil
}

// harness is a paid order of two lines, the first selling two 5,000 cards.
type harness struct {
	payments *fakePayments
	notifier *fakeNotifier
	reader   *fakeReader
	links    fakeLinks
	flow     *giftcardsale.Workflow
}

// week is the harness's clock: the order was placed an hour after it.
var week = time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)

func newHarness() *harness {
	h := &harness{
		payments: &fakePayments{amount: 12_000, captured: map[string]int64{"paycol_1": 12_000},
			issued: map[string]string{}},
		notifier: &fakeNotifier{},
		reader: &fakeReader{
			lines: []query.Record{
				line("order_1", "line_card", 2, 5_000, true),
				line("order_1", "line_mug", 1, 2_000, false),
			},
			placed: map[string]time.Time{"order_1": week.Add(time.Hour)},
			status: map[string]string{"order_1": "completed"},
		},
		links: fakeLinks{
			orders:      map[string][]string{"paycol_1": {"order_1"}},
			collections: map[string][]string{"order_1": {"paycol_1"}},
		},
	}
	h.rewire()

	return h
}

// rewire builds the flow again over the harness's fakes.
func (h *harness) rewire() {
	h.flow = giftcardsale.New(h.payments, fakeOrders{}, h.notifier, h.reader, h.links, slog.New(slog.DiscardHandler))
}

// addPaidOrder adds an order of one gift card line, placed at the moment and
// paid through its own collection.
func (h *harness) addPaidOrder(orderID string, at time.Time) {
	h.reader.lines = append(h.reader.lines, line(orderID, "line_"+orderID, 1, 1_000, true))
	h.reader.placed[orderID], h.reader.status[orderID] = at, "completed"
	h.links.collections[orderID] = []string{"paycol_" + orderID}
	h.payments.captured["paycol_"+orderID] = h.payments.amount
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
	h.payments.captured["paycol_1"] = 7_000

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))

	assert.Empty(t, h.payments.references)
}

// TestACollectionNoOrderWasPlacedThroughIsLeftAlone: a delivery change or an
// exchange's difference is not an order's checkout.
func TestACollectionNoOrderWasPlacedThroughIsLeftAlone(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.links.orders = map[string][]string{}
	h.rewire()

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
	h.reader.lines = nil
	for i := range 100 {
		h.reader.lines = append(h.reader.lines, line("order_1", fmt.Sprintf("line_%d", i), 1, 100, false))
	}
	h.reader.lines = append(h.reader.lines, line("order_1", "line_card", 1, 5_000, true))

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

// TestALineWhoseFlagCannotBeReadStopsTheFlow: a line that cannot say whether it
// sold a card is not guessed to be a mug.
func TestALineWhoseFlagCannotBeReadStopsTheFlow(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.reader.lines[1][giftcardsale.FieldIsGiftcard] = "false"

	err := h.flow.HandleCaptured(context.Background(), captured())

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
	assert.Empty(t, h.payments.references)
}

// TestASweepIssuesWhatNoDeliveryDid is ADR 0212: an order whose capture
// reached no handler gets its cards and their mails from the sweep.
func TestASweepIssuesWhatNoDeliveryDid(t *testing.T) {
	t.Parallel()

	h := newHarness()

	orders, issued, waiting, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 0}, []int{orders, issued, waiting})
	assert.Equal(t, []string{"line_card:1", "line_card:2"}, h.payments.references)
	require.Len(t, h.notifier.sent, 2)
	assert.Equal(t, "gcard_1", h.notifier.sent[0].reference)
}

// TestASweepAfterADeliveryTouchesNothing: an order whose cards were all made
// is found complete by one read and not issued again.
func TestASweepAfterADeliveryTouchesNothing(t *testing.T) {
	t.Parallel()

	h := newHarness()
	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))
	issuedByDelivery := len(h.payments.references)

	orders, issued, waiting, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{0, 0, 0}, []int{orders, issued, waiting})
	assert.Len(t, h.payments.references, issuedByDelivery, "no card was asked for again")
	assert.Len(t, h.notifier.sent, 2, "no code was mailed again")
	assert.Equal(t, [][]string{{"line_card:1", "line_card:2"}}, h.payments.lookups)
}

// TestASweepFinishesAnOrderHalfIssued: a delivery that stopped after the first
// card leaves the second to the sweep, which mails only the second.
func TestASweepFinishesAnOrderHalfIssued(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.payments.issued["line_card:1"] = "gcard_earlier"

	orders, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{1, 1}, []int{orders, issued})
	require.Len(t, h.notifier.sent, 1)
	assert.NotEqual(t, "gcard_earlier", h.notifier.sent[0].reference)
}

// TestASweepLeavesAnUnpaidOrderWaiting: an order whose collection is not
// captured in full issues nothing and is counted as waiting.
func TestASweepLeavesAnUnpaidOrderWaiting(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.payments.captured["paycol_1"] = 7_000

	orders, issued, waiting, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{0, 0, 1}, []int{orders, issued, waiting})
	assert.Empty(t, h.payments.references)
}

// TestNeitherWayIssuesForACanceledOrder: a capture delivered late and a sweep
// both find the order canceled and issue nothing.
func TestNeitherWayIssuesForACanceledOrder(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.reader.status["order_1"] = giftcardsale.StatusCanceled

	require.NoError(t, h.flow.HandleCaptured(context.Background(), captured()))
	orders, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{0, 0}, []int{orders, issued})
	assert.Empty(t, h.payments.references)
	assert.Empty(t, h.notifier.sent)
}

// TestASweepReadsOnlyItsWindow: an order placed before the window is not
// looked at, whatever it is missing.
func TestASweepReadsOnlyItsWindow(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.addPaidOrder("order_old", week.Add(-time.Minute))

	_, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, 2, issued)
	assert.NotContains(t, h.payments.references, "line_order_old:1")
}

// TestASweepReadsEveryPageOfItsWindow: the window's hundred-and-first gift
// card line is swept too.
func TestASweepReadsEveryPageOfItsWindow(t *testing.T) {
	t.Parallel()

	h := newHarness()
	for i := range 100 {
		h.addPaidOrder(fmt.Sprintf("order_%03d", i+2), week.Add(time.Duration(i+2)*time.Hour))
	}

	orders, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, []int{101, 102}, []int{orders, issued})
}

// TestASweepAsksAboutItsSalesInBatches: an order of six hundred cards is looked
// up in two reads rather than one of unbounded size.
func TestASweepAsksAboutItsSalesInBatches(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.reader.lines = []query.Record{line("order_1", "line_card", 600, 10, true)}

	_, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.NoError(t, err)
	assert.Equal(t, 600, issued)
	require.Len(t, h.payments.lookups, 2)
	assert.Len(t, h.payments.lookups[0], 500)
	assert.Len(t, h.payments.lookups[1], 100)
}

// TestAnOrderThatFailsDoesNotStopTheSweep: the failure is reported, and the
// other orders are issued.
func TestAnOrderThatFailsDoesNotStopTheSweep(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.addPaidOrder("order_2", week.Add(2*time.Hour))
	h.payments.failFor = "order_2"

	orders, issued, _, err := h.flow.Sweep(context.Background(), week)

	require.Error(t, err)
	assert.Equal(t, giftcardsale.CodeSweepIncomplete, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the cause's kind is kept")
	assert.Equal(t, []int{1, 2}, []int{orders, issued}, "order_1 was issued all the same")
}
