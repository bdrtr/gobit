package service

import (
	"context"
)

// ownerChecker is a provider that can say, before any session exists, that a
// payment naming nobody will be refused.
//
// The two tenders that spend a person's balance answer it through their shared
// state machine. A card provider does not: whether it will authorize is known
// only once it is asked, and asking is the saga's job. The interface stays in
// this package rather than in core/provider because nothing outside the
// repository implements it yet, and a published method is a promise kept
// forever (ADR 0026).
type ownerChecker interface {
	// CheckOwner refuses a payment for which customerID names nobody.
	CheckOwner(customerID string) error
}

// CheckTender refuses a payment that can never be made, before anything is
// opened for it (ADR 0175).
//
// Two refusals do not depend on the amount, the balance or the moment, so the
// checkout can learn them ahead of the order: the provider is not registered, or
// it spends a person's balance and the cart names nobody. Anything else — a
// declined card, a balance too small — is still the provider's to say at the
// payment step, because only then is the answer true.
func (s *Service) CheckTender(_ context.Context, providerID, customerID string) error {
	provider, err := s.providers.Get(providerID)
	if err != nil {
		return err
	}
	if checker, ok := provider.(ownerChecker); ok {
		return checker.CheckOwner(customerID)
	}

	return nil
}
