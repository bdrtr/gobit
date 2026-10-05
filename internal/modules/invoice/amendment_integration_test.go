//go:build integration

package invoice_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// This file holds ADR 0406 on Postgres: what a sale's rows have left is summed
// over its LIVE amendments, the sale is locked while an amendment reads it, an
// act has one live document, and the schema refuses an amendment that
// contradicts itself.

// amendedSale issues a sale of two rows: a row taxed 5% + 8% compound on 2000,
// and two units at 1000 taxed 20%.
func amendedSale(t *testing.T, svc *service.Service, prefix string) models.Invoice {
	t.Helper()

	in := issueFor(prefix)
	in.Lines = []service.LineInput{
		{
			Description: "Stacked", Quantity: 1, UnitPrice: 2000, Subtotal: 2000,
			TaxRateBps: 500, TaxTotal: 268, Total: 2268,
			TaxComponents: []service.LineTaxInput{
				{RateID: "txr_base", RateBps: 500, TaxableAmount: 2000, TaxAmount: 100},
				{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168},
			},
		},
		in.Lines[0],
	}
	in.Subtotal, in.TaxTotal, in.Total = 4000, 668, 4668

	sale, err := svc.Issue(context.Background(), in)
	require.NoError(t, err)
	require.Len(t, sale.Lines, 2)

	return sale
}

// amendRow builds a document moving total, tax included, on one sale row as
// one unit; a refund unless reason raises a price.
func amendRow(
	prefix string, sale models.Invoice, row int, total, tax int64, reason models.AmendmentReason,
	components ...service.LineTaxInput,
) service.IssueInput {
	in := issueFor(prefix)
	in.Kind = models.KindRefund
	if reason == models.ReasonPriceRaised {
		in.Kind = models.KindSale
	}
	in.Buyer = models.Party{}
	in.Amends, in.AmendmentReason = sale.ID, reason
	in.Lines = []service.LineInput{{
		Description: "Amended", Quantity: 1, UnitPrice: total - tax, Subtotal: total - tax,
		TaxRateBps: sale.Lines[row].TaxRateBps, TaxTotal: tax, Total: total,
		TaxComponents: components, AmendsLineID: sale.Lines[row].ID,
	}}
	in.Subtotal, in.TaxTotal, in.Total = total-tax, tax, total

	return in
}

// TestARowGivesBackNoMoreThanItCarried: per row, in amount, in tax and under
// each rate; what is left fits exactly, a canceled refund gave nothing back,
// and a charge on the row is given back too.
func TestARowGivesBackNoMoreThanItCarried(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ARC")
	refund := func(row int, total, tax int64, components ...service.LineTaxInput) error {
		_, err := svc.Issue(ctx, amendRow("ARC", sale, row, total, tax, models.ReasonReturned, components...))

		return err
	}
	exceeds := func(err error, why string) {
		t.Helper()
		require.Error(t, err, why)
		assert.Equal(t, service.CodeAmendmentExceedsSale, coreerrors.CodeOf(err), why)
	}

	require.NoError(t, refund(1, 1200, 200))
	exceeds(refund(1, 1201, 200), "the row has 1200 left, while the document has 3468")
	exceeds(refund(1, 1200, 201), "the row has 200 tax left")

	last, err := svc.Issue(ctx, amendRow("ARC", sale, 1, 1200, 200, models.ReasonReturned))
	require.NoError(t, err, "what is left fits exactly")
	exceeds(refund(1, 1, 0), "the row has nothing left")

	_, err = svc.MoveStatus(ctx, last.ID, service.MoveInput{To: models.StatusCanceled, Reason: "typed twice"})
	require.NoError(t, err)
	require.NoError(t, refund(1, 1200, 200), "a canceled refund gave nothing back")

	_, err = svc.Issue(ctx, amendRow("ARC", sale, 1, 120, 20, models.ReasonPriceRaised))
	require.NoError(t, err)
	require.NoError(t, refund(1, 120, 20), "a charge on the row is the row's to give back")

	exceeds(refund(0, 1134, 134,
		service.LineTaxInput{RateBps: 500, TaxableAmount: 1000, TaxAmount: 101},
		service.LineTaxInput{RateBps: 800, Compound: true, TaxableAmount: 1000, TaxAmount: 33},
	), "a rate cannot give back more than it charged while the row's tax fits")
	require.NoError(t, refund(0, 2268, 268,
		service.LineTaxInput{RateBps: 500, TaxableAmount: 2000, TaxAmount: 100},
		service.LineTaxInput{RateBps: 800, Compound: true, TaxableAmount: 2100, TaxAmount: 168},
	), "the whole stacked row gives back exactly what it charged")
}

// racingRepo makes two amendments read what a sale's rows have left at the
// same moment when nothing stops them: the first to read waits for the second,
// for long enough that a read no lock orders is always a shared one. Under the
// sale's lock the second cannot arrive, and the first goes on alone.
type racingRepo struct {
	*repository.Repository
	mu       sync.Mutex
	waiting  chan struct{}
	released bool
}

// AmendedRows waits at the barrier and then reads.
func (r *racingRepo) AmendedRows(ctx context.Context, saleID string) (map[string]models.AmendedRow, error) {
	r.mu.Lock()
	if r.waiting == nil {
		wait := make(chan struct{})
		r.waiting = wait
		r.mu.Unlock()
		select {
		case <-wait:
		case <-time.After(2 * time.Second):
		}
	} else {
		if !r.released {
			r.released = true
			close(r.waiting)
		}
		r.mu.Unlock()
	}

	return r.Repository.AmendedRows(ctx, saleID)
}

// TestTwoActsRaceForOneRow: two refunds that fit alone and not together; the
// sale's lock makes the second read the first one's document.
func TestTwoActsRaceForOneRow(t *testing.T) {
	ctx := context.Background()
	sale := amendedSale(t, newService(t), "ARR")
	svc := service.New(&racingRepo{Repository: repository.New(testPool.Pool())}, service.Options{})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = svc.Issue(ctx, amendRow("ARR", sale, 1, 1500, 250, models.ReasonReturned))
		})
	}
	wg.Wait()

	var won, refused int
	for _, err := range errs {
		switch {
		case err == nil:
			won++
		case coreerrors.CodeOf(err) == service.CodeAmendmentExceedsSale:
			refused++
		default:
			t.Fatalf("an unexpected failure: %v", err)
		}
	}
	assert.Equal(t, 1, won, "exactly one refund of the row stands")
	assert.Equal(t, 1, refused, "the other read the first one's and did not fit")
}

// TestASecondDocumentForOneActSpendsNoNumber: two presses for one act write
// one document and take one number.
func TestASecondDocumentForOneActSpendsNoNumber(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ASK")
	act := amendRow("ASK", sale, 1, 1200, 200, models.ReasonPriceLowered)
	act.AmendmentKey = "credit_line:ocl_once"

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range errs {
		wg.Go(func() {
			<-start
			_, errs[i] = svc.Issue(ctx, act)
		})
	}
	close(start)
	wg.Wait()

	var written int
	for _, err := range errs {
		if err == nil {
			written++

			continue
		}
		assert.Equal(t, service.CodeAmendmentExists, coreerrors.CodeOf(err), "got %v", err)
	}
	assert.Equal(t, 1, written)

	amends := sale.ID
	documents, err := svc.ListInvoices(ctx, models.Filter{Amends: &amends})
	require.NoError(t, err)
	assert.Equal(t, int64(1), documents.Count)
	series, err := svc.ListSeries(ctx)
	require.NoError(t, err)
	for _, s := range series {
		if s.Prefix == "ASK" {
			assert.Equal(t, int64(2), s.LastNumber, "the sale and its one amendment; no number was spent")
		}
	}
}

// TestTheIndexHoldsAnActToOneLiveDocument is the index under the service's
// check: a second live document for an act, written past the service, is
// refused and named as such.
func TestTheIndexHoldsAnActToOneLiveDocument(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "AIX")
	act := amendRow("AIX", sale, 1, 120, 20, models.ReasonPriceLowered)
	act.AmendmentKey = "credit_line:ocl_index"
	first, err := svc.Issue(ctx, act)
	require.NoError(t, err)

	repo := repository.New(testPool.Pool())
	second := first
	second.ID = models.NewInvoiceID()
	second.Number = first.Number + "X"
	second.Lines = nil
	err = repo.WithTx(ctx, func(ctx context.Context) error {
		_, err := repo.CreateInvoice(ctx, second)

		return err
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExists, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsConflict(err), "got %v", err)
}

// TestARejectedAmendmentFreesItsAct: a rejected document took no effect, so
// its act is documented again.
func TestARejectedAmendmentFreesItsAct(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ARJ")
	act := amendRow("ARJ", sale, 1, 120, 20, models.ReasonPriceLowered)
	act.AmendmentKey = "credit_line:ocl_rejected"

	first, err := svc.Issue(ctx, act)
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, first.ID, service.MoveInput{To: models.StatusSent})
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, first.ID, service.MoveInput{To: models.StatusRejected, Reason: "wrong address"})
	require.NoError(t, err)

	again, err := svc.Issue(ctx, act)
	require.NoError(t, err, "the rejected document frees its act")
	assert.NotEqual(t, first.Number, again.Number, "the rejected one's number stays spent")
}

// TestASaleWithLiveAmendmentsIsNotCanceledOnPostgres: the sale is canceled once
// the documents amending it are not live.
func TestASaleWithLiveAmendmentsIsNotCanceledOnPostgres(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ACN")
	refund, err := svc.Issue(ctx, amendRow("ACN", sale, 1, 120, 20, models.ReasonReturned))
	require.NoError(t, err)

	_, err = svc.MoveStatus(ctx, sale.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.Error(t, err)
	assert.Equal(t, service.CodeHasLiveAmendments, coreerrors.CodeOf(err))

	_, err = svc.MoveStatus(ctx, refund.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err)
	moved, err := svc.MoveStatus(ctx, sale.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err, "a canceled amendment does not hold its sale")
	assert.Equal(t, models.StatusCanceled, moved.Status)

	_, err = svc.Issue(ctx, amendRow("ACN", sale, 1, 120, 20, models.ReasonReturned))
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendsVoid, coreerrors.CodeOf(err))
}

// TestTheSchemaRefusesAnAmendmentThatContradictsItself writes past the
// service and asserts the server's own report of each constraint.
func TestTheSchemaRefusesAnAmendmentThatContradictsItself(t *testing.T) {
	ctx := context.Background()
	sale := amendedSale(t, newService(t), "ASC")

	insert := func(id, kind string, amends, reason, key *string) error {
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO invoices (id, number, series_id, kind, status, currency_code,
			   seller_name, buyer_name, buyer_email_folded, subtotal, total, issued_at,
			   amends_invoice_id, amendment_reason, amendment_key)
			 VALUES ($1, $1, $2, $3, 'issued', 'TRY', 'Seller', 'Buyer', '', 0, 0, now(), $4, $5, $6)`,
			id, sale.SeriesID, kind, amends, reason, key)

		return err
	}
	text := func(v string) *string { return &v }
	self := "inv_check_self"

	for name, err := range map[string]error{
		"invoices_amendment_reason_named":  insert("inv_check_named", "refund", &sale.ID, nil, nil),
		"invoices_amendment_reason_known":  insert("inv_check_known", "refund", &sale.ID, text("discounted"), nil),
		"invoices_amendment_reason_fits":   insert("inv_check_fits", "refund", &sale.ID, text("price_raised"), nil),
		"invoices_amendment_key_amends":    insert("inv_check_key", "sale", nil, nil, text("credit_line:x")),
		"invoices_amendment_key_not_blank": insert("inv_check_blank", "refund", &sale.ID, text("returned"), text("  ")),
		"invoices_amendment_not_itself":    insert(self, "refund", &self, text("returned"), nil),
	} {
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `check constraint "`+name+`"`)
	}

	require.NoError(t, insert("inv_check_fine", "refund", &sale.ID, text("returned"), text("return_refunded:re_1")),
		"a refund that names its sale and why is the schema's")
}

// amendableRead is the part of the interop's amendable answer this file reads.
type amendableRead struct {
	Rows []struct {
		LineID     string `json:"line_id"`
		LeftTotal  int64  `json:"left_total"`
		LeftTax    int64  `json:"left_tax"`
		Components []struct {
			LeftTax int64 `json:"left_tax"`
		} `json:"components"`
	} `json:"rows"`
	ChargeRows []struct {
		LineID       string `json:"line_id"`
		InvoiceID    string `json:"invoice_id"`
		AmendsLineID string `json:"amends_line_id"`
		LeftTotal    int64  `json:"left_total"`
	} `json:"charge_rows"`
	Amendments []struct {
		AmendmentKey string `json:"amendment_key"`
	} `json:"amendments"`
}

// TestTheAmendableReadSaysWhatEachRowHasLeft reads the figures every split is
// weighted by on the real schema: each row less what live refunds gave back
// and plus what live charges added, under each rate, a canceled refund
// counting for nothing, and the rows a charge added listed apart.
func TestTheAmendableReadSaysWhatEachRowHasLeft(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ALT")
	issue := func(in service.IssueInput) models.Invoice {
		t.Helper()
		doc, err := svc.Issue(ctx, in)
		require.NoError(t, err)

		return doc
	}

	issue(amendRow("ALT", sale, 1, 1200, 200, models.ReasonReturned))
	issue(amendRow("ALT", sale, 1, 120, 20, models.ReasonPriceRaised))
	issue(amendRow("ALT", sale, 0, 1100, 100, models.ReasonReturned,
		service.LineTaxInput{RateBps: 500, TaxableAmount: 1000, TaxAmount: 100},
		service.LineTaxInput{RateBps: 800, Compound: true, TaxableAmount: 1000, TaxAmount: 0}))
	dead := issue(amendRow("ALT", sale, 1, 100, 0, models.ReasonPriceLowered))
	_, err := svc.MoveStatus(ctx, dead.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	require.NoError(t, err)
	added := amendRow("ALT", sale, 1, 1500, 0, models.ReasonPriceRaised)
	added.Lines[0].AmendsLineID, added.Lines[0].TaxRateBps = "", 0
	charge := issue(added)

	raw, err := service.NewInterop(svc).AmendableJSON(ctx, sale.ID)
	require.NoError(t, err)
	var read amendableRead
	require.NoError(t, json.Unmarshal(raw, &read))
	require.Len(t, read.Rows, 2)
	assert.Equal(t, int64(2268-1100), read.Rows[0].LeftTotal)
	assert.Equal(t, int64(268-100), read.Rows[0].LeftTax)
	require.Len(t, read.Rows[0].Components, 2)
	assert.Equal(t, int64(0), read.Rows[0].Components[0].LeftTax, "the base rate gave back all it charged")
	assert.Equal(t, int64(168), read.Rows[0].Components[1].LeftTax)
	assert.Equal(t, int64(2400-1200+120), read.Rows[1].LeftTotal, "the canceled refund gave nothing back")
	assert.Equal(t, int64(400-200+20), read.Rows[1].LeftTax)
	require.Len(t, read.ChargeRows, 2, "the two live charges' rows")
	assert.Equal(t, sale.Lines[1].ID, read.ChargeRows[0].AmendsLineID)
	assert.Equal(t, charge.ID, read.ChargeRows[1].InvoiceID)
	assert.Empty(t, read.ChargeRows[1].AmendsLineID, "a row the sale did not have")
	assert.Equal(t, int64(1500), read.ChargeRows[1].LeftTotal)
	assert.Len(t, read.Amendments, 5)
}

// TestARateRemembersWhatItGaveBackOnPostgres: the per-rate sums read the
// live documents before this one.
func TestARateRemembersWhatItGaveBackOnPostgres(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ARB")
	refund := func(total, tax, base, top int64) error {
		_, err := svc.Issue(ctx, amendRow("ARB", sale, 0, total, tax, models.ReasonReturned,
			service.LineTaxInput{RateBps: 500, TaxableAmount: 1000, TaxAmount: base},
			service.LineTaxInput{RateBps: 800, Compound: true, TaxableAmount: 1000, TaxAmount: top}))

		return err
	}

	require.NoError(t, refund(1100, 100, 100, 0))
	err := refund(1134, 134, 1, 133)
	require.Error(t, err, "the base rate has nothing left")
	assert.Equal(t, service.CodeAmendmentExceedsSale, coreerrors.CodeOf(err))
	require.NoError(t, refund(1134, 134, 0, 134))
}

// TestACanceledChargeIsRefusedWhileARefundReliesOnIt holds ADR 0406's charge
// guard on Postgres: the sale is locked, the charge's sums are taken out, and
// a refund that gave back on a row the charge added holds it too.
func TestACanceledChargeIsRefusedWhileARefundReliesOnIt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	sale := amendedSale(t, svc, "ACG")
	cancel := service.MoveInput{To: models.StatusCanceled, Reason: "issued twice"}

	charge, err := svc.Issue(ctx, amendRow("ACG", sale, 1, 120, 20, models.ReasonPriceRaised))
	require.NoError(t, err)
	relying, err := svc.Issue(ctx, amendRow("ACG", sale, 1, 2520, 420, models.ReasonReturned))
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, charge.ID, cancel)
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExceedsSale, coreerrors.CodeOf(err))
	_, err = svc.MoveStatus(ctx, relying.ID, cancel)
	require.NoError(t, err)
	_, err = svc.MoveStatus(ctx, charge.ID, cancel)
	require.NoError(t, err)

	added := amendRow("ACG", sale, 1, 1500, 0, models.ReasonPriceRaised)
	added.Lines[0].AmendsLineID, added.Lines[0].TaxRateBps = "", 0
	addedCharge, err := svc.Issue(ctx, added)
	require.NoError(t, err)
	back := amendRow("ACG", sale, 1, 1500, 0, models.ReasonPriceLowered)
	back.Lines[0].AmendsLineID, back.Lines[0].TaxRateBps = addedCharge.Lines[0].ID, 0
	_, err = svc.Issue(ctx, back)
	require.NoError(t, err, "the row the charge added gives the change back")
	_, err = svc.MoveStatus(ctx, addedCharge.ID, cancel)
	require.Error(t, err)
	assert.Equal(t, service.CodeAmendmentExceedsSale, coreerrors.CodeOf(err))
}

// TestAChargesCancelAndARefundRelyingOnItRace: a refund that fits only with a
// charge and the charge's cancellation, read at the same moment; the sale's
// lock, taken by both, lets exactly one of them stand.
func TestAChargesCancelAndARefundRelyingOnItRace(t *testing.T) {
	ctx := context.Background()
	plain := newService(t)
	sale := amendedSale(t, plain, "ACR")
	charge, err := plain.Issue(ctx, amendRow("ACR", sale, 1, 120, 20, models.ReasonPriceRaised))
	require.NoError(t, err)
	svc := service.New(&racingRepo{Repository: repository.New(testPool.Pool())}, service.Options{})

	var wg sync.WaitGroup
	var canceled, refunded error
	wg.Go(func() {
		_, canceled = svc.MoveStatus(ctx, charge.ID, service.MoveInput{To: models.StatusCanceled, Reason: "void"})
	})
	wg.Go(func() {
		_, refunded = svc.Issue(ctx, amendRow("ACR", sale, 1, 2520, 420, models.ReasonReturned))
	})
	wg.Wait()

	assert.True(t, (canceled == nil) != (refunded == nil),
		"exactly one stands: the cancel %v, the refund %v", canceled, refunded)
	for _, err := range []error{canceled, refunded} {
		if err != nil {
			assert.Equal(t, service.CodeAmendmentExceedsSale, coreerrors.CodeOf(err))
		}
	}
}
