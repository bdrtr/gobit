package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/invoice/models"
)

// MoveInput is a request to move a document to another status.
type MoveInput struct {
	// From, when given, is the status the caller read the document in; the
	// move is refused with [CodeStatusMoved] when the document is no longer
	// in it (ADR 0344). Empty moves it from whatever status it is in.
	From models.Status
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

	if in.To == models.StatusCanceled {
		return s.cancel(ctx, id, in)
	}

	return s.move(ctx, id, in)
}

// cancel withdraws the document, and refuses to withdraw a sale while a
// document amending it stands (ADR 0406).
//
// The sale is locked while its amendments are counted, as an amendment locks
// it while it checks the sale is live, so a cancellation and an amendment
// arriving together cannot each see the other absent. A rejection is not
// refused: it is a fact the receiving side reports, and it voids the sale for
// every amendment that follows.
//
// A charge, a sale amending another, is withdrawn only while the refunds of
// that sale still fit their rows without it; the sale it amends is locked
// first, as an amendment locks it, so neither can pass the other's check.
func (s *Service) cancel(ctx context.Context, id string, in MoveInput) (models.Invoice, error) {
	var out models.Invoice
	err := s.repo.WithTx(ctx, func(ctx context.Context) error {
		head, err := s.repo.GetInvoice(ctx, id)
		if err != nil {
			return err
		}
		if head.Kind == models.KindSale && head.AmendsInvoiceID != "" {
			if _, err := s.repo.LockInvoice(ctx, head.AmendsInvoiceID); err != nil {
				return err
			}
		}
		locked, err := s.repo.LockInvoice(ctx, id)
		if err != nil {
			return err
		}
		switch {
		case locked.Kind == models.KindSale && locked.AmendsInvoiceID == "":
			live, err := s.repo.CountLiveAmendments(ctx, id)
			if err != nil {
				return err
			}
			if live > 0 {
				return errors.Conflict(CodeHasLiveAmendments,
					"invoice %s is amended by %d live documents; cancel them first", id, live)
			}
		case locked.Kind == models.KindSale && locked.Status.Live():
			if err := s.chargeLeavesRowsCovered(ctx, locked); err != nil {
				return err
			}
		}

		out, err = s.move(ctx, id, in)

		return err
	})

	return out, err
}

// move makes the move once the request is known to be one a document may ask
// for.
func (s *Service) move(ctx context.Context, id string, in MoveInput) (models.Invoice, error) {
	current, err := s.repo.GetInvoice(ctx, id)
	if err != nil {
		return models.Invoice{}, err
	}
	if in.From != "" && current.Status != in.From {
		return models.Invoice{}, errors.Conflict(CodeStatusMoved,
			"invoice %s is %q now, not %q as it was read; draw the page again", id, current.Status, in.From)
	}

	if !current.Status.CanMoveTo(in.To) {
		return models.Invoice{}, errors.Conflict(CodeTransition,
			"an invoice in status %q cannot move to %q", current.Status, in.To)
	}

	return s.repo.SetStatus(ctx, id, current.Status, in.To, in.Reason, in.ProviderID, in.ExternalID)
}
