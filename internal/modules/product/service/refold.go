package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// refoldPageSize is how many option values one page of the convergence reads.
const refoldPageSize = 500

// RefoldReport is what the convergence tells the operator.
type RefoldReport struct {
	// Examined is how many non-ASCII option values were read.
	Examined int
	// Rewritten is how many carried a matching form Go would not have written.
	Rewritten int
	// Collisions are the values that could NOT be converged because another
	// value in the same option already holds the folded form.
	//
	// They are returned rather than counted because each one needs a HUMAN: the
	// two rows are the same value typed twice, and only the merchant knows which
	// spelling to keep.
	Collisions []Collision
}

// Collision is one option value the convergence could not write.
type Collision struct {
	// OptionID is the option the two spellings share.
	OptionID string
	// Value is the text the merchant typed for the row that could not be moved.
	Value string
	// Folded is the matching form it would have taken, which another value in the
	// same option already holds.
	Folded string
}

// RefoldOptionValues brings every non-ASCII option value's matching form up to
// what [models.FoldOptionValue] produces.
//
// # Why this exists
//
// Migration 000003 added value_folded and had to backfill it for values already
// in the catalog. A migration is SQL, and SQL folds with lower(), which is the
// CLUSTER's fold — on a --locale=C database it folds ASCII and nothing else, so
// exactly the values ADR 0039 is about are the ones the backfill gets wrong.
// Leaving that to an operator's command was the mistake made once already with
// the invoice module's buyer address, and corrected there for the same reason:
// a silent data defect is not a thing to leave to whoever reads a migration
// header.
//
// # Why a collision is an ANSWER and not an error
//
// The convergence can be refused by the database, and the case is not exotic. On
// a C-locale cluster the SQL backfill leaves the dotted and dotless spellings of
// one Turkish word at DIFFERENT folded forms, so migration 000003's unique index
// accepts both rows; the Go fold brings them to the same form, and the update
// then violates that index. Measured before this was written.
//
// The pass therefore does not stop. It converges what it can, collects what it
// cannot, and hands back both — because the rows it cannot move are a question
// for the merchant (which spelling did you mean?) and not a fault the process
// can fix by trying again.
func (s *Service) RefoldOptionValues(ctx context.Context) (RefoldReport, error) {
	var (
		report RefoldReport
		after  string
	)

	for {
		page, err := s.repo.ListNonAsciiOptionValuesForRefold(ctx, after, refoldPageSize)
		if err != nil {
			// What was already converged stays converged, so the report goes
			// back with the error rather than being discarded.
			return report, err
		}
		if len(page) == 0 {
			return report, nil
		}

		for _, handle := range page {
			report.Examined++
			after = handle.ID

			folded := models.FoldOptionValue(handle.Value)
			if folded == handle.Folded {
				continue
			}

			switch err := s.repo.SetOptionValueFolded(ctx, handle.ID, folded); {
			case err == nil:
				report.Rewritten++
			case errors.KindOf(err) == errors.KindConflict:
				report.Collisions = append(report.Collisions, Collision{
					OptionID: handle.OptionID,
					Value:    handle.Value,
					Folded:   folded,
				})
			default:
				return report, err
			}
		}
	}
}
