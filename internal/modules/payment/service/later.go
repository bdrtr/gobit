package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// laterCapturer is a provider whose money arrives after the order is placed:
// an offline method, whose capture is an operator recording that it came
// (ADR 0284).
//
// It stays in this package rather than in core/provider for
// [paymentChecker]'s reason: nothing outside the repository implements it yet,
// and a published method is a promise kept forever (ADR 0026).
type laterCapturer interface {
	// CapturesLater reports that the provider's sessions are captured after
	// the checkout, by the admin's session capture.
	CapturesLater() bool
}

// CapturesLater reports whether a provider's money arrives after the order is
// placed, so the checkout places the order owing that part rather than
// capturing it (ADR 0284). A provider that is not registered is refused, as
// [Service.CheckTender] refuses it.
func (s *Service) CapturesLater(providerID string) (bool, error) {
	provider, err := s.providers.Get(providerID)
	if err != nil {
		return false, err
	}

	return capturesLater(provider), nil
}

// capturesLater reports whether a provider says its money comes later.
func capturesLater(provider any) bool {
	later, ok := provider.(laterCapturer)

	return ok && later.CapturesLater()
}

// laterCapturing lists the registered providers whose money comes later, in
// the registry's order.
func (s *Service) laterCapturing() []string {
	var out []string
	for _, id := range s.providers.IDs() {
		provider, err := s.providers.Get(id)
		if err == nil && capturesLater(provider) {
			out = append(out, id)
		}
	}

	return slices.Clip(out)
}

// CodeSessionCapturesNow reports a session recorded as received whose
// provider's money moves at the checkout (ADR 0287).
const CodeSessionCapturesNow = "payment_session_captures_now"

// ListAwaitingSessions returns, per collection, the sessions whose money the
// shop records when it arrives: authorized, not captured, and of a provider
// whose money comes later (ADR 0287).
func (s *Service) ListAwaitingSessions(
	ctx context.Context, collectionIDs []string,
) (map[string][]models.PaymentSession, error) {
	later := s.laterCapturing()
	out := map[string][]models.PaymentSession{}
	if len(collectionIDs) == 0 || len(later) == 0 {
		return out, nil
	}

	sessions, err := s.store.SessionsOfCollections(ctx, collectionIDs)
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].Status == models.SessionAuthorized && slices.Contains(later, sessions[i].ProviderID) {
			out[sessions[i].PaymentCollectionID] = append(out[sessions[i].PaymentCollectionID], sessions[i])
		}
	}

	return out, nil
}

// RecordReceived records that the money an offline method promised arrived:
// it captures the session whole (ADR 0287). A second call returns the capture
// the first made.
//
// A session whose provider moves its money at the checkout is refused: its
// money moves through the provider, and an operator's word is not a capture of
// it.
func (s *Service) RecordReceived(ctx context.Context, sessionID string) (models.Payment, error) {
	ses, err := s.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return models.Payment{}, err
	}
	provider, err := s.providers.Get(ses.ProviderID)
	if err != nil {
		return models.Payment{}, err
	}
	if !capturesLater(provider) {
		return models.Payment{}, errors.Conflict(CodeSessionCapturesNow,
			"session %s is paid through %q at the checkout; only an offline method's money is "+
				"recorded as received", sessionID, ses.ProviderID)
	}

	return s.CapturePayment(ctx, sessionID, 0)
}
