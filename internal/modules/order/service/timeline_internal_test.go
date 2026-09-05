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
