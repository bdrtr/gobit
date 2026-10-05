package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// actsFixture is one order with every kind of act after its sale: a credit, a
// cheaper and a dearer delivery, a funded exchange, and a return's, a claim's
// and an exchange's refund, each a minute after the one before.
func actsFixture(t *testing.T) (svc *service.Service, orderID string) {
	t.Helper()

	store := newFakeStore()
	at := func(minute int) time.Time { return time.Date(2026, 10, 1, 9, minute, 0, 0, time.UTC) }
	refunds := scriptedRefunds{body: fmt.Sprintf(`[
		{"id":"ref_return","reference":"ret_1","amount":900,"currency_code":"TRY","refunded_at":%q},
		{"id":"ref_claim","reference":"clm_1","amount":400,"currency_code":"TRY","refunded_at":%q},
		{"id":"ref_exchange","reference":"exc_1","amount":200,"currency_code":"TRY","refunded_at":%q},
		{"id":"ref_other","reference":"ret_elsewhere","amount":100,"currency_code":"TRY","refunded_at":%q}
	]`, at(6).Format(time.RFC3339), at(7).Format(time.RFC3339), at(8).Format(time.RFC3339),
		at(9).Format(time.RFC3339))}
	svc, err := service.New(service.Options{Repo: store, Events: newFakeBus(t), Refunds: refunds})
	require.NoError(t, err)

	order, err := svc.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)

	store.credits["ocl_credit"] = models.OrderCreditLine{ID: "ocl_credit", OrderID: order.ID, Amount: 300, CreatedAt: at(1)}
	store.credits["ocl_cheaper"] = models.OrderCreditLine{ID: "ocl_cheaper", OrderID: order.ID, Amount: 500, CreatedAt: at(2)}
	store.deliveryChanges[order.ID] = []models.DeliveryChange{
		{ID: "odc_cheaper", OrderID: order.ID, Difference: -500, CreditLineID: "ocl_cheaper", CreatedAt: at(2)},
		{ID: "odc_dearer", OrderID: order.ID, Difference: 700, PaymentCollectionID: "pay_col_x", CreatedAt: at(3)},
		{ID: "odc_same", OrderID: order.ID, Difference: 0, CreatedAt: at(4)},
	}
	funded := at(5)
	store.afterSaleCauses = map[string][]models.AfterSaleCause{order.ID: {
		{ID: "clm_1", Kind: "claim"},
		{ID: "exc_1", Kind: "exchange", FundedAt: &funded, DifferenceDue: 250},
		{ID: "exc_open", Kind: "exchange", DifferenceDue: 80},
		{ID: "ret_1", Kind: "return"},
	}}
	detail, err := svc.GetOrder(context.Background(), order.ID)
	require.NoError(t, err)
	line := detail.Items[0].ID
	store.retItems["retitem_1"] = models.ReturnItem{ID: "retitem_1", ReturnID: "ret_1", OrderLineItemID: line, Quantity: 2}

	return svc, order.ID
}

// TestAnOrdersActsAfterTheSaleAreItsJournalsEntries lists every kind once,
// keyed as the journal keys it, in time order, an exchange's two left out of
// what a document can carry and a return carrying the units it took back.
func TestAnOrdersActsAfterTheSaleAreItsJournalsEntries(t *testing.T) {
	t.Parallel()

	svc, orderID := actsFixture(t)
	acts, err := svc.AfterSaleActs(context.Background(), orderID)
	require.NoError(t, err)

	type seen struct {
		kind         models.JournalKind
		id           string
		amount       int64
		documentable bool
	}
	got := make([]seen, 0, len(acts))
	for _, act := range acts {
		got = append(got, seen{act.Kind, act.ID, act.Amount, act.Documentable})
	}
	assert.Equal(t, []seen{
		{models.JournalCreditLine, "ocl_credit", 300, true},
		{models.JournalDeliveryChanged, "odc_cheaper", 500, true},
		{models.JournalDeliveryUpgraded, "odc_dearer", 700, true},
		{models.JournalExchangeFunded, "exc_1", 250, false},
		{models.JournalReturnRefunded, "ref_return", 900, true},
		{models.JournalClaimRefunded, "ref_claim", 400, true},
		{models.JournalExchangeRefunded, "ref_exchange", 200, false},
	}, got)

	returned := acts[4]
	require.Len(t, returned.Returned, 1)
	assert.Equal(t, int64(2), returned.Returned[0].Quantity)
	for _, act := range acts {
		if act.Kind != models.JournalReturnRefunded {
			assert.Empty(t, act.Returned, "%s %s took nothing back", act.Kind, act.ID)
		}
	}
}

// TestOneActIsReadByItsKindAndID reads an act as the list has it, and refuses
// an id under another kind.
func TestOneActIsReadByItsKindAndID(t *testing.T) {
	t.Parallel()

	svc, orderID := actsFixture(t)
	act, err := svc.AfterSaleAct(context.Background(), orderID, models.JournalDeliveryChanged, "odc_cheaper")
	require.NoError(t, err)
	assert.Equal(t, int64(500), act.Amount)

	_, err = svc.AfterSaleAct(context.Background(), orderID, models.JournalCreditLine, "ocl_cheaper")
	require.Error(t, err, "a cheaper delivery's credit is the change's act, not a credit's")
	assert.True(t, errors.IsNotFound(err), "got %v", err)
	assert.Equal(t, service.CodeAfterSaleActUnknown, errors.CodeOf(err))
}

// TestAfterSaleActsAreRefusedPastTheirCeiling: an order with more records than
// one read returns is refused rather than cut.
func TestAfterSaleActsAreRefusedPastTheirCeiling(t *testing.T) {
	t.Parallel()

	svc, orderID := actsFixture(t)
	acts, err := svc.AfterSaleActs(context.Background(), orderID)
	require.NoError(t, err)
	require.NotEmpty(t, acts)

	store := newFakeStore()
	many, err := service.New(service.Options{Repo: store, Events: newFakeBus(t)})
	require.NoError(t, err)
	order, err := many.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)
	for i := range service.MaxAfterSaleActs + 1 {
		id := fmt.Sprintf("ocl_%04d", i)
		store.credits[id] = models.OrderCreditLine{ID: id, OrderID: order.ID, Amount: 1}
	}
	_, err = many.AfterSaleActs(context.Background(), order.ID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	causes := make([]models.AfterSaleCause, service.MaxAfterSaleActs+1)
	for i := range causes {
		causes[i] = models.AfterSaleCause{ID: fmt.Sprintf("ret_%04d", i), Kind: "return"}
	}
	store.credits = map[string]models.OrderCreditLine{}
	store.afterSaleCauses = map[string][]models.AfterSaleCause{order.ID: causes}
	_, err = many.AfterSaleActs(context.Background(), order.ID)
	require.Error(t, err, "the causes read past the ceiling")
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestAnActsRefundInAnotherCurrencyIsAFault: a refund naming the order's
// return in another currency names the wrong order.
func TestAnActsRefundInAnotherCurrencyIsAFault(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc, err := service.New(service.Options{Repo: store, Events: newFakeBus(t), Refunds: scriptedRefunds{
		body: `[{"id":"ref_eur","reference":"ret_eur","amount":10,"currency_code":"EUR","refunded_at":"2026-10-01T09:00:00Z"}]`,
	}})
	require.NoError(t, err)
	order, err := svc.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)
	store.afterSaleCauses = map[string][]models.AfterSaleCause{order.ID: {{ID: "ret_eur", Kind: "return"}}}

	_, err = svc.AfterSaleActs(context.Background(), order.ID)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "got %v", err)
}

// TestTheInvoiceSurfaceNamesEachLineAndItsCard: an amendment names the units
// a return took back by the order line, and a card's row takes no price
// lowered (ADR 0406).
func TestTheInvoiceSurfaceNamesEachLineAndItsCard(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	in := validInput()
	in.Items[0].IsGiftcard = true
	in.Items[0].TaxTotal, in.Items[0].Total = 0, 3000
	in.TaxTotal, in.Total = 0, 5500
	placed, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)
	order, err := e.svc.GetOrder(context.Background(), placed.ID)
	require.NoError(t, err)

	raw, err := service.NewInterop(e.svc).OrderInvoiceJSON(context.Background(), order.ID)
	require.NoError(t, err)
	var body struct {
		Items []struct {
			LineID     string `json:"line_id"`
			IsGiftcard *bool  `json:"is_giftcard"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Items, len(order.Items))
	assert.Equal(t, order.Items[0].ID, body.Items[0].LineID)
	require.NotNil(t, body.Items[0].IsGiftcard, "the flag is always sent")
	assert.True(t, *body.Items[0].IsGiftcard, "the line sold a card")
}
