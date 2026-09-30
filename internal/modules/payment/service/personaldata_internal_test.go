package service

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// columnsOf lists the columns one accessor table can read, sorted.
func columnsOf[T any](values accessors[T]) []string {
	return slices.Sorted(maps.Keys(values))
}

// TestTheDisclosureCanReadEveryDeclaredColumn holds the hand-written accessors
// to the declaration in both directions (ADR 0277): a declared column nobody
// reads leaves the dossier short, and a reader for an undeclared column would
// reach past the declaration. The table below is written out rather than
// derived, because deriving it is what is under test.
func TestTheDisclosureCanReadEveryDeclaredColumn(t *testing.T) {
	t.Parallel()

	readable := map[string][]string{
		tableCollections:        columnsOf(collectionValues),
		tableSessions:           columnsOf(sessionValues),
		tableRefunds:            columnsOf(refundValues),
		tableManualSessions:     columnsOf(manualSessionValues),
		tableGiftCardSessions:   columnsOf(giftCardSessionValues),
		tableStoreCreditEntries: columnsOf(storeCreditEntryValues),
		tableStoreCreditSession: columnsOf(tenderSessionValues),
		tableLoyaltyEntries:     columnsOf(loyaltyEntryValues),
		tableLoyaltySessions:    columnsOf(tenderSessionValues),
	}

	declared := map[string][]string{}
	for _, holding := range personalColumns {
		declared[holding.Table] = append(declared[holding.Table], holding.Column)
	}
	require.NotEmpty(t, declared)
	for table, columns := range declared {
		slices.Sort(columns)
		assert.Equal(t, columns, readable[table], "the declared and the readable columns of %s differ", table)
	}
	for table := range readable {
		assert.Contains(t, declared, table, "the disclosure reads %s and the declaration does not name it", table)
	}
}

// TestAnUnreadColumnStopsTheWholeDossier holds the collector's error to the
// answer: a declared column with no accessor on a later table refuses the
// dossier rather than returning the records built before it.
//
// It is not parallel because it swaps a package accessor table; parallel tests
// resume only after the sequential ones have finished.
func TestAnUnreadColumnStopsTheWholeDossier(t *testing.T) {
	crippled := maps.Clone(loyaltyEntryValues)
	delete(crippled, "points")
	saved := loyaltyEntryValues
	loyaltyEntryValues = crippled
	t.Cleanup(func() { loyaltyEntryValues = saved })

	records, err := disclosureRecords(&dossierRows{
		collections:    []models.PaymentCollection{{ID: "paycol_1", CustomerID: "cus_1"}},
		loyaltyEntries: []models.LoyaltyEntry{{ID: "lpoint_1", CustomerID: "cus_1", Points: 5}},
	})

	require.Error(t, err)
	assert.Equal(t, CodeDisclosureColumnUnread, errors.CodeOf(err))
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Nil(t, records, "a dossier short of a column is refused whole")
}
