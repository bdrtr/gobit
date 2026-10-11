package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// customerAuthorizer is a provider whose customer authorizes the payment on the
// provider's own page, which the provider then reports on in its own time:
// PayTR's iframe, which takes the money and posts the outcome back (ADR 0438).
//
// Such a provider cannot be driven. Its session is opened BEFORE the cart is
// completed, so the storefront holds what the customer pays with, and the
// completion asks the provider what it was told rather than telling it to
// authorize. It stays in this package rather than in core/provider for
// [laterCapturer]'s reason: nothing outside the repository implements it yet,
// and a published method is a promise kept forever (ADR 0026).
//
// What the rest of the module and the checkout take for granted of such a
// provider, which PayTR keeps and a second one has to: it is a
// [coreprovider.SessionInspector], since nothing else says whether the
// customer paid; the money it took shows as the inspection's CapturedAmount,
// which is what the report sweep and reconciliation look for; and the cancel
// of a session its customer paid on gives that money back, which is why a
// report whose session was canceled settles refunded.
type customerAuthorizer interface {
	// CustomerAuthorizes reports that the provider's customer authorizes the
	// payment on the provider's page, before the checkout completes the cart.
	CustomerAuthorizes() bool
}

// ProviderCustomerAuthorizes reports whether a provider says its customer
// authorizes the payment on its own page.
//
// It is exported for one reader: a plugin cannot import this package, so the
// pin that the PayTR plugin still answers the question lives at the
// composition root, and it asks through this function rather than through a
// copy of the interface that could drift from the one asked here.
func ProviderCustomerAuthorizes(provider any) bool {
	authorizer, ok := provider.(customerAuthorizer)

	return ok && authorizer.CustomerAuthorizes()
}

// CustomerAuthorizes reports whether a provider's customer authorizes the
// payment on the provider's own page, so the checkout refuses to open its
// session inside the completion (ADR 0438). A provider that is not registered
// is refused, as [Service.CheckTender] refuses it.
func (s *Service) CustomerAuthorizes(providerID string) (bool, error) {
	provider, err := s.providers.Get(providerID)
	if err != nil {
		return false, err
	}

	return ProviderCustomerAuthorizes(provider), nil
}

// customerAuthorizing lists the registered providers whose customer authorizes
// on the provider's page, in the registry's order.
func (s *Service) customerAuthorizing() []string {
	var out []string
	for _, id := range s.providers.IDs() {
		provider, err := s.providers.Get(id)
		if err == nil && ProviderCustomerAuthorizes(provider) {
			out = append(out, id)
		}
	}

	return slices.Clip(out)
}

// CheckoutSession returns a session and its collection, for a checkout handed
// the session to complete a cart with (ADR 0438).
//
// The two are read one after the other and without a lock. What the checkout
// decides from them — whose cart the collection was opened for, which provider,
// for how much — is written once and never updated, and the one fact that does
// move, the session's status, is decided again under the lock by the
// authorization the completion makes.
func (s *Service) CheckoutSession(
	ctx context.Context, sessionID string,
) (models.PaymentSession, models.PaymentCollection, error) {
	ses, err := s.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return models.PaymentSession{}, models.PaymentCollection{}, err
	}
	col, err := s.GetPaymentCollection(ctx, ses.PaymentCollectionID)
	if err != nil {
		return models.PaymentSession{}, models.PaymentCollection{}, err
	}

	return ses, col, nil
}

// CodeSessionNotReportable reports a session the checkout asked about whose
// provider does not report on its own sessions.
const CodeSessionNotReportable = "payment_session_not_reportable"

// ReportedStatus returns what a customer-authorizing provider says about one of
// its sessions in its own ledger: pending while the customer has not paid,
// captured once they have, failed when the payment was refused (ADR 0438).
//
// It is a READ, as [coreprovider.SessionInspector] promises, and it moves
// nothing: the checkout asks it so that a completion made before the provider
// reported reserves no stock and opens no order. A provider whose customer
// does not authorize, or that cannot be inspected, is refused, because its
// session's status is the one this module holds and asking the provider would
// answer a different question.
func (s *Service) ReportedStatus(ctx context.Context, sessionID string) (coreprovider.SessionStatus, error) {
	ses, err := s.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	provider, err := s.providers.Get(ses.ProviderID)
	if err != nil {
		return "", err
	}
	inspector, inspectable := provider.(coreprovider.SessionInspector)
	if !ProviderCustomerAuthorizes(provider) || !inspectable {
		return "", errors.Invalid(CodeSessionNotReportable,
			"session %s is paid through %q, which does not report on its own sessions; "+
				"its status is the one this module holds", sessionID, ses.ProviderID)
	}

	inspection, err := inspector.InspectSession(ctx, ses.ExternalID)
	if err != nil {
		return "", err
	}

	return inspection.Status, nil
}
