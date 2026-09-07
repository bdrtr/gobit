package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// TestTheConvergenceFixesWhatTheSqlBackfillGotWrong is the pass's reason to
// exist.
//
// Migration 000003 backfilled value_folded with lower(btrim()), and on a
// --locale=C cluster that leaves a non-ASCII letter alone. The stored matching
// form is then not what models.FoldOptionValue produces, and the filter this
// decision exists for misses the value. The letters below are \u escapes so this
// file carries no Turkish letter of its own (ADR 0012): U+0131 is the dotless i.
func TestTheConvergenceFixesWhatTheSqlBackfillGotWrong(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	seedOptionValue(t, store, "v1", "o1", "K\u0131rm\u0131z\u0131", "k\u0131rm\u0131z\u0131")
	seedOptionValue(t, store, "v2", "o1", "Mavi", "mavi")
	// A non-ASCII value that is ALREADY right — it was written by Go rather than
	// by the SQL backfill. It is in scope and must NOT be rewritten, which is what
	// makes the pass idempotent and what lets it run on every boot.
	seedOptionValue(t, store, "v3", "o2", "Ye\u015fil", "yesil")

	svc := newService(t, store, nil, nil)

	report, err := svc.RefoldOptionValues(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 2, report.Examined,
		"the two non-ASCII values are in scope; a pure-ASCII value folds the same on every cluster")
	assert.Equal(t, 1, report.Rewritten,
		"only the value the SQL backfill got wrong is written; a row already carrying the Go "+
			"fold must be left alone, or every boot would rewrite the whole catalog")
	assert.Empty(t, report.Collisions)
	assert.Equal(t, "kirmizi", store.foldedOf("v1"),
		"the stored matching form must become what models.FoldOptionValue produces")
}

// TestTheConvergenceReportsACollisionInsteadOfFailing is the case that decides
// the pass's shape.
//
// On a C-locale cluster the SQL backfill leaves the dotted and dotless spellings
// of one word at DIFFERENT folded forms, so 000003's unique index accepts both
// rows. The Go fold brings them together and the update is refused. That is not
// a fault to retry: the two rows are one value typed twice, and only the merchant
// knows which spelling to keep — so the pass converges what it can, collects what
// it cannot, and keeps going.
func TestTheConvergenceReportsACollisionInsteadOfFailing(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	// Already folded the way Go would: this row holds the form the other wants.
	seedOptionValue(t, store, "v1", "o1", "Kirmizi", "kirmizi")
	// The SQL backfill left this one apart, and converging it collides.
	seedOptionValue(t, store, "v2", "o1", "K\u0131rm\u0131z\u0131", "k\u0131rm\u0131z\u0131")
	// A value in ANOTHER option that must still be converged afterwards, which is
	// what proves the pass did not stop at the collision.
	seedOptionValue(t, store, "v3", "o2", "Y\u0131ld\u0131z", "y\u0131ld\u0131z")

	svc := newService(t, store, nil, nil)

	report, err := svc.RefoldOptionValues(context.Background())
	require.NoError(t, err, "a collision is an answer, not an error")

	require.Len(t, report.Collisions, 1)
	assert.Equal(t, "o1", report.Collisions[0].OptionID)
	assert.Equal(t, "kirmizi", report.Collisions[0].Folded,
		"the report has to name the form that was already taken, or nobody can find the pair")

	assert.Equal(t, 1, report.Rewritten, "the value in the other option is still converged")
	assert.Equal(t, "yildiz", store.foldedOf("v3"))
	assert.Equal(t, "k\u0131rm\u0131z\u0131", store.foldedOf("v2"),
		"the row that collided is LEFT as it was; guessing which spelling to drop is the "+
			"merchant's decision and not this pass's")
}

// seedOptionValue puts a value and its stored matching form into the fake.
func seedOptionValue(t *testing.T, store *memStore, id, optionID, value, folded string) {
	t.Helper()

	_, err := store.CreateOptionValue(context.Background(),
		models.OptionValue{ID: id, OptionID: optionID, Value: value})
	require.NoError(t, err)
	store.setFolded(id, folded)
}
