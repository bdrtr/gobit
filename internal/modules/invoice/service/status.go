package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// MoveInput is a request to move a document to another status.
type MoveInput struct {
	// To is the status the document is to move to.
	To models.Status
	// Reason is why; it is REQUIRED for a rejection and a cancellation.
	Reason string
	// ProviderID and ExternalID are filled in when a provider took the document
	// for transmission; empty leaves whatever the record already holds.
	ProviderID string
	ExternalID string
}

// MoveStatus moves the document if the move is one it may make.
//
// # Why the current status is read first AND sent to the database
//
// The read is what produces a useful error: "an accepted document cannot be
// sent again" says more than a row count of zero. The write then carries the
// status that was read, so the database decides the race — two operators acting
// at the same moment cannot both win, and the loser is told the document moved
// under them rather than silently overwriting the winner.
func (s *Service) MoveStatus(
	ctx context.Context, id string, in MoveInput,
) (models.Invoice, error) {
	if strings.TrimSpace(id) == "" {
		return models.Invoice{}, errors.Invalid(CodeInvalidInput, "the invoice id is required")
	}
	if !in.To.Valid() {
		return models.Invoice{}, errors.Invalid(CodeInvalidInput,
			"unknown invoice status: %q", in.To)
	}

	// A rejection and a cancellation are the two states a person later has to
	// account for, and "why" is the whole content of that account. The other
	// moves are self-explanatory and are not made to carry a sentence nobody
	// would write honestly.
	if (in.To == models.StatusRejected || in.To == models.StatusCanceled) &&
		strings.TrimSpace(in.Reason) == "" {
		return models.Invoice{}, errors.Invalid(CodeInvalidInput,
			"moving a document to %q requires a reason", in.To)
	}

	current, err := s.repo.GetInvoice(ctx, id)
	if err != nil {
		return models.Invoice{}, err
	}

	if !current.Status.CanMoveTo(in.To) {
		return models.Invoice{}, errors.Conflict(CodeTransition,
			"an invoice in status %q cannot move to %q", current.Status, in.To)
	}

	return s.repo.SetStatus(ctx, id, current.Status, in.To, in.Reason, in.ProviderID, in.ExternalID)
}
