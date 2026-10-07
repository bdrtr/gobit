package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReplacementOfParcel answers which replacement a parcel carries, or "" when it
// carries none (ADR 0239).
//
// Every parcel is asked, a sale's too, so "none" is an answer rather than a
// NotFound: the flow that asks hears about every canceled parcel and acts on
// the few that carried a replacement.
func (s *Service) ReplacementOfParcel(ctx context.Context, fulfillmentID string) (string, error) {
	if err := requireID("fulfillment_id", fulfillmentID); err != nil {
		return "", err
	}

	carried, err := s.store.ReplacementsByFulfillment(ctx, fulfillmentID)
	if err != nil {
		return "", err
	}
	switch len(carried) {
	case 0:
		return "", nil
	case 1:
		return carried[0].ID, nil
	default:
		return "", errors.Internal(CodeInconsistentState,
			"parcel %s carries %d replacements, %s and %s; its key names one",
			fulfillmentID, len(carried), carried[0].ID, carried[1].ID)
	}
}

// RecallReplacement sends a replacement whose parcel was canceled back to
// 'requested' (ADR 0239).
//
// # It is the RECORD half, as the dispatch's mark is
//
// The units the parcel was to carry are the inventory module's, and the returns
// flow puts them back before it calls this. What this says is that they did not
// leave: the parcel and the moment are cleared, the promises the lines and parts
// held are forgotten, and the recall is counted, so the next dispatch opens a
// parcel under a key of its own.
//
// # The source is reopened when nothing else settled it
//
// A claim or an exchange is completed by the goods leaving. When they did not,
// the source goes back to where it stood, unless another replacement of it was
// dispatched and settles it still. An exchange whose difference was collected
// goes back to 'funded'.
//
// # A parcel that no longer carries it is answered, not refused
//
// The flow hears a canceled parcel once per delivery of the event, and a late
// one may arrive after the replacement left again in another parcel. Only a
// replacement dispatched in THIS parcel is recalled; anything else is returned
// as it stands.
func (s *Service) RecallReplacement(
	ctx context.Context, replacementID, fulfillmentID string,
) (models.Replacement, error) {
	if err := requireID("replacement_id", replacementID); err != nil {
		return models.Replacement{}, err
	}
	if err := requireID("fulfillment_id", fulfillmentID); err != nil {
		return models.Replacement{}, err
	}

	var out models.Replacement
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockReplacement(ctx, replacementID)
		if err != nil {
			return err
		}
		if current.Status != models.ReplacementDispatched || current.FulfillmentID != fulfillmentID {
			out = current

			return nil
		}

		out, err = s.store.RecallReplacement(ctx, replacementID)
		if err != nil {
			return err
		}
		if err := s.store.ClearReplacementReservations(ctx, replacementID); err != nil {
			return err
		}

		return s.reopenSource(ctx, out)
	})
	if err != nil {
		return models.Replacement{}, err
	}

	return out, nil
}

// reopenSource takes the recalled replacement's claim or exchange back to where
// it stood before the goods left, unless another of its replacements was
// dispatched.
func (s *Service) reopenSource(ctx context.Context, recalled models.Replacement) error {
	var siblings []models.Replacement
	var err error
	if recalled.Source() == models.SourceExchange {
		siblings, err = s.store.ListReplacementsByExchange(ctx, recalled.ExchangeID)
	} else {
		siblings, err = s.store.ListReplacementsByClaim(ctx, recalled.ClaimID)
	}
	if err != nil {
		return err
	}
	for i := range siblings {
		if siblings[i].ID != recalled.ID && siblings[i].Status == models.ReplacementDispatched {
			return nil
		}
	}

	if recalled.Source() == models.SourceExchange {
		exchange, err := s.store.LockExchange(ctx, recalled.ExchangeID)
		if err != nil {
			return err
		}
		if exchange.Status != models.ExchangeCompleted {
			return nil
		}
		reopened, err := s.store.ReopenExchange(ctx, recalled.ExchangeID)
		if err != nil || !reopened.Priced() || reopened.Status != models.ExchangeRequested {
			return err
		}
		// Requested again, an exchange that names its return derives its figure
		// again: a replacement withdrawn while it was settled left it as it
		// stood (ADR 0432).
		if err := s.repriceExchangeLines(ctx, reopened); err != nil {
			return err
		}
		_, err = s.deriveExchangeDifference(ctx, reopened)

		return err
	}

	claim, err := s.store.LockClaim(ctx, recalled.ClaimID)
	if err != nil {
		return err
	}
	if claim.Status == models.ClaimCompleted {
		_, err = s.store.ReopenClaim(ctx, recalled.ClaimID)
	}

	return err
}
