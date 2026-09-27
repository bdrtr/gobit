package service

import (
	"context"

	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// paymentChecker is a provider that can say, before any session exists, that a
// payment will be refused.
//
// The module's own tenders answer it through their shared state machine, by
// resolving whose balance the payment would spend: nobody for a guest spending
// credit, no card for a code nobody was given (ADR 0208). A card provider does
// not: whether it will authorize is known only once it is asked, and asking is
// the saga's job. The interface stays in this package rather than in
// core/provider because nothing outside the repository implements it yet, and
// a published method is a promise kept forever (ADR 0026).
type paymentChecker interface {
	// CheckPayment refuses a payment the provider can never make.
	CheckPayment(ctx context.Context, in coreprovider.CreateSessionInput) error
}

// CheckTender refuses a payment that can never be made, before anything is
// opened for it (ADR 0175).
//
// Some refusals do not depend on the amount, the balance or the moment, so the
// checkout can learn them ahead of the order: the provider is not registered;
// it spends a person's balance and the cart names nobody; or it spends a gift
// card and the code opens none, or opens one in another currency. Anything
// else — a declined card, a balance too small — is still the provider's to say
// at the payment step, because only then is the answer true.
func (s *Service) CheckTender(
	ctx context.Context, providerID, customerID, currencyCode string, data map[string]any,
) error {
	provider, err := s.providers.Get(providerID)
	if err != nil {
		return err
	}
	if checker, ok := provider.(paymentChecker); ok {
		return checker.CheckPayment(ctx, coreprovider.CreateSessionInput{
			CurrencyCode: currencyCode, CustomerID: customerID, Data: data,
		})
	}

	return nil
}
