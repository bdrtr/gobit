package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The timeline is tested from INSIDE the package here, and only for the part
// that is a pure function of the order row.
//
// [Service.Timeline] itself cannot be reached from the external test package:
// it needs a wired query catalog for the money and the shipments, and the
// module's fake store has none. [orderEntries] needs nothing — it turns one row
// into the moments that row carries — and it is exactly the piece D5 changed.
//
// That gap is worth naming rather than leaving: the composed timeline built on
// 2026-09-05 has no test of any kind, and the bug this file's first test catches
// is one a test of the composition would have caught the day it was written.

// archivedOrder produces an order detail carrying the four moments an order can
// hold, at four DISTINCT instants so a mixed-up assignment is visible.
func archivedOrder() models.OrderDetail {
	placed := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	completed := placed.Add(48 * time.Hour)
	archived := completed.Add(72 * time.Hour)

	return models.OrderDetail{Order: models.Order{
		ID:          "order_1",
		Status:      models.OrderArchived,
		PlacedAt:    placed,
		CompletedAt: &completed,
		ArchivedAt:  &archived,
	}}
}

// kindsOf collects the entry kinds in the order they were produced.
func kindsOf(entries []TimelineEntry) []string {
	out := make([]string, 0, len(entries))
	for i := range entries {
		out = append(out, entries[i].Kind)
	}

	return out
}

// findEntry returns the single entry of a kind; it fails when there is not
// exactly one, because "the first of several" would hide a duplicate.
func findEntry(t *testing.T, entries []TimelineEntry, kind string) TimelineEntry {
	t.Helper()

	var found []TimelineEntry
	for i := range entries {
		if entries[i].Kind == kind {
			found = append(found, entries[i])
		}
	}

	require.Len(t, found, 1, "expected exactly one %q entry in %v", kind, kindsOf(entries))

	return found[0]
}

// TestTheTimelineDatesTheArchiving is the regression the composed timeline
// could not have failed on before, because it had no test at all.
//
// The defect was doubled. The order had no archived_at to report (gaps.md D5),
// AND the timeline's own documentation claimed an archived order came back with
// a nil At — while [orderEntries] emitted placed, completed and canceled and no
// archived entry of any kind. So the fact was missing from the row and missing
// from the composition, and the comment said it was handled.
func TestTheTimelineDatesTheArchiving(t *testing.T) {
	t.Parallel()

	detail := archivedOrder()

	entries := orderEntries(detail)

	archived := findEntry(t, entries, KindOrderArchived)
	require.NotNil(t, archived.At)
	assert.Equal(t, *detail.ArchivedAt, *archived.At)
	assert.Equal(t, detail.ID, archived.RefID)
	assert.Equal(t, ClockDatabase, archived.Clock,
		"archived_at is written by the query's now(), not by the process")
}

// TestTheArchivingIsNotTheCompletion holds the two moments apart.
//
// One column could not have carried both, and a timeline that reported the
// completion twice under two names would be worse than the missing entry: it
// would look right.
func TestTheArchivingIsNotTheCompletion(t *testing.T) {
	t.Parallel()

	detail := archivedOrder()

	entries := orderEntries(detail)

	completed := findEntry(t, entries, KindOrderCompleted)
	archived := findEntry(t, entries, KindOrderArchived)

	require.NotNil(t, completed.At)
	require.NotNil(t, archived.At)
	assert.True(t, archived.At.After(*completed.At),
		"the order left the lists after it closed, and the entries have to say so")
}

// TestAnOrderArchivedBeforeTheColumnExistedInventsNoMoment is the honest half of
// migration 000007.
//
// Rows archived before archived_at existed carry the status and no moment. The
// database allows exactly that — the constraint holds a stamp to the status and
// not the reverse — and the timeline must not paper over it. An entry with a
// made-up instant would place the archiving somewhere on the axis, and every
// reader would take that position for a record.
func TestAnOrderArchivedBeforeTheColumnExistedInventsNoMoment(t *testing.T) {
	t.Parallel()

	detail := archivedOrder()
	detail.ArchivedAt = nil

	entries := orderEntries(detail)

	assert.NotContains(t, kindsOf(entries), KindOrderArchived,
		"an undated archiving produces no entry rather than an invented one")
	placed := findEntry(t, entries, KindOrderPlaced)
	assert.Equal(t, models.OrderArchived.String(), placed.Detail,
		"the fact is still visible in the status; only its moment is missing")
}

// TestEveryOrderEntryCarriesAMomentAndAClock closes the shape of what this
// function may emit.
//
// The order's own row is the one source on the timeline where every fact is a
// column: if a moment is there it has an instant and the database wrote it.
// A nil At coming out of HERE would mean a status was read as an event, which
// is the shape the exchange's removed "unfinished" branch had.
func TestEveryOrderEntryCarriesAMomentAndAClock(t *testing.T) {
	t.Parallel()

	canceled := time.Date(2026, 9, 4, 9, 0, 0, 0, time.UTC)
	detail := archivedOrder()
	detail.CanceledAt = &canceled

	entries := orderEntries(detail)

	require.Len(t, entries, 4, "placed, completed, canceled, archived")
	for i := range entries {
		assert.NotNil(t, entries[i].At, "%s has no moment", entries[i].Kind)
		assert.Equal(t, ClockDatabase, entries[i].Clock, "%s has no clock", entries[i].Kind)
	}
}

// TestTheCustomerDoesNotSeeTheMoney is the decision this filter exists for.
//
// A capture's recorded amount is the merchant's ledger view: a partial capture
// is a fact about a hold, and what the customer will reconcile against is what
// their bank shows. A refund is the same story from the other side.
func TestTheCustomerDoesNotSeeTheMoney(t *testing.T) {
	kept := customerVisible([]TimelineEntry{
		{Kind: KindOrderPlaced, RefID: "order_1"},
		{Kind: KindPaymentCaptured, RefID: "paycol_1", Amount: 1000, Currency: "TRY"},
		{Kind: KindPaymentRefunded, RefID: "paycol_1", Amount: 400, Currency: "TRY"},
		{Kind: KindShipmentShipped, RefID: "ful_1"},
	})

	require.Len(t, kept, 2)
	assert.Equal(t, KindOrderPlaced, kept[0].Kind)
	assert.Equal(t, KindShipmentShipped, kept[1].Kind)
}

// TestTheCustomerDoesNotSeeTheFiling keeps an internal housekeeping move off the
// customer's screen.
//
// Archiving takes the order out of the merchant's daily lists. Nothing happened
// to the goods or the money, and a customer told their order was "archived"
// would reasonably read it as something being done to them.
func TestTheCustomerDoesNotSeeTheFiling(t *testing.T) {
	kept := customerVisible([]TimelineEntry{
		{Kind: KindOrderCompleted, RefID: "order_1"},
		{Kind: KindOrderArchived, RefID: "order_1"},
	})

	require.Len(t, kept, 1)
	assert.Equal(t, KindOrderCompleted, kept[0].Kind)
}

// TestTheCustomerSeesEverythingAboutTheGoods pins the other direction: the
// filter is a NARROWING, and a narrowing that drops what the customer is
// actually asking about would be worse than no endpoint.
func TestTheCustomerSeesEverythingAboutTheGoods(t *testing.T) {
	goods := []string{
		KindOrderPlaced, KindOrderCompleted, KindOrderCanceled,
		KindShipmentOpened, KindShipmentShipped, KindShipmentDelivered,
		KindShipmentCanceled, KindShipmentReturned,
		KindReturnOpened, KindReturnReceived, KindReturnCanceled,
		KindClaimOpened, KindClaimCompleted, KindClaimCanceled,
		KindExchangeOpened, KindExchangeCompleted, KindExchangeCanceled,
	}

	entries := make([]TimelineEntry, 0, len(goods))
	for _, kind := range goods {
		entries = append(entries, TimelineEntry{Kind: kind})
	}

	assert.Len(t, customerVisible(entries), len(goods),
		"every moment about the order's lifecycle or its goods has to cross")
}

// TestANewKindIsInvisibleUntilSomebodyDecides states the default the map gives.
//
// A kind added tomorrow does not appear on the storefront until it is put in
// the set. That direction is the safe one: a moment nobody classified showing up
// on a customer's screen is a leak, while one that is missing is a gap somebody
// notices and fixes.
func TestANewKindIsInvisibleUntilSomebodyDecides(t *testing.T) {
	assert.Empty(t, customerVisible([]TimelineEntry{{Kind: "invoice.issued"}}))
}

// TestTheTimelineReportsBothOfAnExchangesEndings is the entry ADR 0114 made
// reachable and no surface reported.
//
// The defect had the same shape as the archiving one above and it is worth
// stating twice, because the repository produced it twice. Migration 000017
// brought the completed status and its column back; this file's own godoc went
// on saying "completion is gone from the record entirely", and the composition
// went on emitting the withdrawal alone. The claim beside it — the same record
// shape, in the same loop — had carried both endings all along, so the tree
// held the correct form and the wrong one at the same time.
//
// It is pinned HERE rather than in the composed timeline for the reason
// [exchangeEntries] states: the composition needs a query catalog and can only
// run end to end, which is where this entry hid.
func TestTheTimelineReportsBothOfAnExchangesEndings(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	completed := opened.Add(24 * time.Hour)

	entries := exchangeEntries([]models.Exchange{{
		ID:          "exch_1",
		Status:      models.ExchangeCompleted,
		CreatedAt:   opened,
		CompletedAt: &completed,
	}})

	settled := findEntry(t, entries, KindExchangeCompleted)
	require.NotNil(t, settled.At)
	assert.Equal(t, completed, *settled.At)
	assert.Equal(t, "exch_1", settled.RefID)
	assert.Equal(t, ClockDatabase, settled.Clock,
		"completed_at is written by the query's now(), not by the process")

	assert.NotContains(t, kindsOf(entries), KindExchangeCanceled,
		"an exchange that was completed was not also withdrawn")
}

// TestAWithdrawnExchangeReportsOnlyItsWithdrawal is the other direction, and it
// is what keeps the entry above from being written unconditionally.
//
// The two moments are mutually exclusive on the row (order_exchanges_completed_
// stamp and order_exchanges_canceled_stamp each hold a status to its moment),
// so a mapping that reported both would describe a record the database cannot
// hold.
func TestAWithdrawnExchangeReportsOnlyItsWithdrawal(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	withdrawn := opened.Add(2 * time.Hour)

	entries := exchangeEntries([]models.Exchange{{
		ID:         "exch_2",
		Status:     models.ExchangeCanceled,
		CreatedAt:  opened,
		CanceledAt: &withdrawn,
	}})

	assert.Equal(t, []string{KindExchangeOpened, KindExchangeCanceled}, kindsOf(entries))
}

// TestAnOpenExchangeReportsOnlyItsOpening keeps the floor under the two tests
// above: a record with neither moment produces neither entry.
func TestAnOpenExchangeReportsOnlyItsOpening(t *testing.T) {
	t.Parallel()

	entries := exchangeEntries([]models.Exchange{{
		ID:        "exch_3",
		Status:    models.ExchangeRequested,
		CreatedAt: time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC),
	}})

	assert.Equal(t, []string{KindExchangeOpened}, kindsOf(entries))
}
