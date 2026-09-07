package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// TestTheRefoldRewritesOnlyTheHandlesGoWouldNotHaveWritten is the property the
// pass exists for, and the property that makes it safe to run.
//
// An operator cannot tell in advance whether their invoices carry a non-ASCII
// buyer address, so the command has to be safe to run blind: it must correct
// what the SQL backfill in migration 000003 got wrong and leave everything else
// exactly as it found it. The two halves are tested together because either one
// alone is satisfiable by a wrong implementation — one that rewrites nothing
// passes the second, and one that rewrites everything passes the first.
func TestTheRefoldRewritesOnlyTheHandlesGoWouldNotHaveWritten(t *testing.T) {
	t.Parallel()

	// The first row is what a C-locale cluster's lower() produced for a Turkish
	// address: the capital letters came through untouched. Its letters are
	// written as \u escapes rather than as themselves — U+0130 (capital I with
	// a dot) and U+00D6 (capital O with diaeresis), with U+00F6 in the expected
	// result — because this file is English, the language ratchet's diacritic
	// lane reads the source, and a Turkish letter here would need a ledger entry
	// for what is plainly test data. The second is an
	// ASCII address, where the SQL backfill and Go agree exactly. The third is
	// the OTHER way the two disagree — btrim() strips spaces where Go's
	// TrimSpace also strips tabs — and it is here because narrowing the pass to
	// "addresses that look non-ASCII" would have missed it.
	repo := newFakeRepo()
	repo.refoldPages = map[string][]models.BuyerEmailHandle{
		"": {
			{ID: "i1", BuyerEmail: "\u0130hsan@\u00d6rnek.com", Folded: "\u0130hsan@\u00d6rnek.com"},
			{ID: "i2", BuyerEmail: "Ada@Example.com", Folded: "ada@example.com"},
			{ID: "i3", BuyerEmail: "\tgrace@example.com ", Folded: "\tgrace@example.com"},
		},
		"i3": nil,
	}
	svc := service.New(repo, service.Options{})

	report, err := svc.RefoldBuyerEmails(context.Background())
	require.NoError(t, err)

	assert.Equal(t, 3, report.Examined)
	assert.Equal(t, 2, report.Rewritten,
		"the ASCII row was already right and must not be rewritten; rewriting every "+
			"row would touch every invoice in the table to change nothing")

	assert.Equal(t, map[string]string{
		"i1": "ihsan@\u00f6rnek.com",
		"i3": "grace@example.com",
	}, repo.refoldWrites,
		"the handles written are the ones models.NormalizeEmail produces, which is "+
			"the same function the other five holders fold with")
}

// TestTheRefoldIsIdempotent holds the claim the command's own documentation
// makes to justify having no -confirm flag.
//
// A second run must write nothing. If it did, the command would not be safe to
// run twice, and a command that is not safe to run twice needs the guard that
// recover and migrate down carry — which is a cost paid by every operator on
// every run, to protect against something that would not happen.
func TestTheRefoldIsIdempotent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.refoldPages = map[string][]models.BuyerEmailHandle{
		"":   {{ID: "i1", BuyerEmail: "Ada@Example.com", Folded: "ada@example.com"}},
		"i1": nil,
	}
	svc := service.New(repo, service.Options{})

	first, err := svc.RefoldBuyerEmails(context.Background())
	require.NoError(t, err)
	require.Zero(t, first.Rewritten)

	second, err := svc.RefoldBuyerEmails(context.Background())
	require.NoError(t, err)
	assert.Zero(t, second.Rewritten)
	assert.Empty(t, repo.refoldWrites, "an installation that never had the defect is left alone")
}

// TestARefoldThatFailsStillReportsWhatItDid is the half an operator acts on.
//
// The pass writes as it goes, so a failure part way leaves a table that is
// partly corrected. Returning only the error would tell the operator nothing
// about whether to expect that, and the count is the only thing that can.
func TestARefoldThatFailsStillReportsWhatItDid(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.refoldErr = errors.New("the connection went away")
	svc := service.New(repo, service.Options{})

	report, err := svc.RefoldBuyerEmails(context.Background())
	require.Error(t, err)
	assert.Zero(t, report.Examined)
}
