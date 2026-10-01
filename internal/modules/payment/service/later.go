package service

import "slices"

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
