package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// refoldPageSize is how many documents one page of the re-fold pass reads.
//
// The pass is maintenance run by a human at a moment they chose, so throughput
// is not what this number is protecting; what it protects is the pool, which the
// same installation may be serving requests from while this runs. A few hundred
// rows is a page small enough that no single read holds a connection long.
const refoldPageSize = 500

// RefoldReport is what the re-fold pass tells the operator.
type RefoldReport struct {
	// Examined is how many documents carrying a buyer address were read.
	Examined int
	// Rewritten is how many of them had a handle Go would not have produced.
	//
	// It is reported SEPARATELY from Examined because the difference is the
	// answer to the question the operator is actually asking: zero rewritten
	// out of ten thousand examined means this installation never had the defect,
	// which is a real result and not a no-op.
	Rewritten int
}

// RefoldBuyerEmails rewrites every buyer-address handle that Go would fold
// differently from the way it currently stands.
//
// # Why this exists
//
// Migration 000003 gave this module a buyer_email_folded column so that an
// erasure resolves a person by a handle GO folded rather than one the cluster
// folded. It also had to backfill that column for documents already in the
// table, and a migration is SQL — so the backfill was written with lower() and
// btrim(), which is precisely the pair that cannot be trusted here: lower() is
// the cluster's fold and does nothing to non-ASCII letters on a --locale=C
// database, and btrim() strips spaces where Go's strings.TrimSpace also strips
// tabs and newlines. The rows the original defect was about are therefore the
// rows the backfill gets wrong.
//
// This pass is the correction, and it runs the SAME function every other holder
// of an address folds with.
//
// # Why it is idempotent and why it decides per row
//
// It compares before it writes. Running it twice rewrites nothing the second
// time, and running it on an installation that never had the defect rewrites
// nothing at all — which matters because an operator cannot tell in advance
// whether their invoices contain a non-ASCII address, and a command that has to
// be reasoned about before it is safe to run will not be run.
//
// # What it does NOT touch
//
// buyer_email — what the document prints — is read and never written. An
// invoice is a snapshot and ADR 0024 makes it immutable; this corrects the
// handle beside it, not the document.
func (s *Service) RefoldBuyerEmails(ctx context.Context) (RefoldReport, error) {
	var (
		report RefoldReport
		after  string
	)

	for {
		page, err := s.repo.ListBuyerEmailsForRefold(ctx, after, refoldPageSize)
		if err != nil {
			// The report so far is returned WITH the error rather than
			// discarded: the pass writes as it goes, so the rows it already
			// corrected stay corrected, and an operator who is told only "it
			// failed" would have no idea whether to expect a half-done table.
			return report, err
		}
		if len(page) == 0 {
			return report, nil
		}

		for _, handle := range page {
			report.Examined++
			after = handle.ID

			folded := models.NormalizeEmail(handle.BuyerEmail)
			if folded == handle.Folded {
				continue
			}

			if err := s.repo.SetBuyerEmailFolded(ctx, handle.ID, folded); err != nil {
				return report, err
			}

			report.Rewritten++
		}
	}
}
