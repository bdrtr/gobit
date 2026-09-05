package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// MaxReconcileLimit is the largest suspect set a single pass may take.
//
// The bound is the PROVIDER's, not the database's: the listing is one indexed
// scan whatever its size, while the pass that follows makes a network call per
// row. A caller asking for more than this has not chosen a bigger page, it has
// asked for a pass that cannot finish inside any sane deadline.
const MaxReconcileLimit = 1_000

// Divergence is one session whose two ledgers disagree.
//
// It carries BOTH sides and no verdict. What to do about a difference is a
// decision for a human with an accounting system in front of them; a field
// called something like "shouldCapture" would be this module claiming an
// authority it does not have.
type Divergence struct {
	// SessionID and CollectionID locate the session in this module.
	SessionID    string
	CollectionID string
	// ProviderID is which provider was asked.
	ProviderID string
	// ExternalID is the identifier the provider knows the session by. It is
	// the value an operator pastes into the provider's own dashboard, which is
	// what makes the report actionable rather than merely alarming.
	ExternalID string

	// LocalStatus and LocalAuthorized are what this module believes: an amount
	// on hold, and nothing drawn.
	LocalStatus     models.SessionStatus
	LocalAuthorized int64
	// ProviderStatus and ProviderCaptured are what the provider says.
	ProviderStatus   coreprovider.SessionStatus
	ProviderCaptured int64

	// CurrencyCode is the session's currency, so an amount is never reported
	// without one.
	CurrencyCode string
}

// ReconciliationReport is the outcome of one comparison pass.
//
// It reports counts rather than a verdict for the reason any fan-out does: a
// pass that asked forty providers and could not reach three of them is not
// "clean", and a single boolean would say it was.
type ReconciliationReport struct {
	// Examined is how many sessions were in the suspect set.
	Examined int
	// Agreed is how many the provider confirmed as this module has them.
	Agreed int
	// Divergences are the sessions whose ledgers disagree.
	Divergences []Divergence
	// Unaskable is how many sessions belong to a provider that cannot be
	// inspected at all — unregistered, or registered without the capability.
	//
	// It is counted SEPARATELY and never folded into Agreed: "the two ledgers
	// agree" and "nobody could ask" must not look the same, which is the whole
	// reason [coreprovider.SessionInspector] is an optional interface rather
	// than a method every provider is forced to fake.
	Unaskable int
	// Unreachable is how many providers were asked and could not answer.
	Unreachable int
	// Unknown is how many sessions the provider has never heard of.
	//
	// It is its own count and not an error, because it is a FINDING: this
	// module holds an external identifier that the provider disowns, which is
	// what an authorization opened against the wrong merchant account looks
	// like from here. [coreprovider.SessionInspector] draws exactly this
	// distinction — "the provider has no such session" and "the provider says
	// nothing was taken" are different facts — and folding it into Unreachable
	// would file a standing misconfiguration as a network blip.
	Unknown int
	// Truncated reports that the suspect set filled the limit, so there may be
	// more waiting for the next pass.
	Truncated bool
}

// Clean reports whether the pass found nothing needing a human.
//
// A pass that could not ask is NOT clean. That is the point.
func (r ReconciliationReport) Clean() bool {
	return len(r.Divergences) == 0 &&
		r.Unaskable == 0 && r.Unreachable == 0 && r.Unknown == 0
}

// Reconcile compares this module's ledger against each provider's own, for the
// sessions where the two can silently disagree.
//
// # The hole this closes
//
// Every money-moving flow calls the provider INSIDE this module's database
// transaction (see the package doc for why that trade is made). If the
// transaction then fails to commit, the rollback leaves the money moved and no
// local trace of it: the session stays authorized here while the provider says
// captured. Downstream, the checkout saga reads the local collection, sees
// nothing captured, and compensates — rolling back an order that was paid for.
//
// internal/workflows/checkout/doc.go names this as the risk it narrows but
// cannot close, and names the only correct closure: ask the provider.
//
// # It REPORTS and does not repair
//
// Nothing here writes. Recording a capture off the back of a comparison would
// mean this module deciding, on its own, that money moved — and the reasoning
// that refuses an unwatched saga sweeper (ADR 0017) applies with more force
// where the subject is money. The repair stays a human's, with both ledgers in
// front of them. What changes is that the human learns there is one.
//
// unchangedFor is the settling window: a session that has been authorized for
// less than this is presumed to be a capture still in flight, not a
// divergence. limit bounds the pass.
func (s *Service) Reconcile(
	ctx context.Context, unchangedFor time.Duration, limit int,
) (ReconciliationReport, error) {
	if unchangedFor <= 0 {
		return ReconciliationReport{}, errors.Invalid(CodeInvalidInput,
			"reconciliation needs a positive settling window; a capture in flight sits in "+
				"exactly the suspect state for as long as the provider takes")
	}
	if limit <= 0 || limit > MaxReconcileLimit {
		return ReconciliationReport{}, errors.Invalid(CodeInvalidInput,
			"the reconciliation limit has to be between 1 and %d", MaxReconcileLimit)
	}

	// ONE MORE than the limit is asked for, and the extra row is never
	// examined. Reporting truncation from "the page came back full" is wrong
	// exactly when the suspect set ends on the limit, and the job turns
	// Truncated into a warning that the newest sessions went unread — a warning
	// that fires every pass on a healthy installation is one an operator learns
	// to skip, which costs the pass the day it is true.
	suspects, err := s.store.ListSessionsForReconciliation(
		ctx, time.Now().UTC().Add(-unchangedFor), int32(limit)+1)
	if err != nil {
		return ReconciliationReport{}, errors.Wrap(err, errors.KindOf(err),
			CodeReconcileFailed, "the sessions to reconcile could not be listed")
	}

	truncated := len(suspects) > limit
	if truncated {
		suspects = suspects[:limit]
	}

	report := ReconciliationReport{
		Examined:  len(suspects),
		Truncated: truncated,
	}
	for i := range suspects {
		s.reconcileOne(ctx, suspects[i], &report)
	}

	return report, nil
}

// reconcileOne asks one provider about one session and files the answer.
func (s *Service) reconcileOne(
	ctx context.Context, ses models.PaymentSession, report *ReconciliationReport,
) {
	prov, err := s.providers.Get(ses.ProviderID)
	if err != nil {
		// A session whose provider is no longer registered cannot be asked,
		// and that is a fact worth counting rather than an error worth failing
		// the pass for: uninstalling a plugin is a deployment decision, and the
		// money it took is still real.
		report.Unaskable++

		return
	}

	inspector, ok := prov.(coreprovider.SessionInspector)
	if !ok {
		report.Unaskable++

		return
	}

	inspection, err := inspector.InspectSession(ctx, ses.ExternalID)
	switch {
	case errors.KindOf(err) == errors.KindNotFound:
		// The provider was reached and disowned the session. That is a finding
		// about this installation, not a fault in the pass.
		s.log.WarnContext(ctx, "the provider does not know a session this module holds",
			"provider", ses.ProviderID, "session", ses.ID, "external_id", ses.ExternalID)
		report.Unknown++

		return
	case err != nil:
		// Counted, not returned. One unreachable provider must not stop the
		// pass from asking the others: the sessions it would have covered are
		// precisely the ones nobody else is looking at.
		s.log.WarnContext(ctx, "the provider could not be asked during reconciliation",
			"provider", ses.ProviderID, "session", ses.ID, "error", err)
		report.Unreachable++

		return
	}

	if inspection.CapturedAmount == 0 {
		// Every session in the suspect set is locally authorized, which means
		// this module believes NOTHING has been drawn. So the comparison is
		// narrow on purpose: the provider reporting nothing captured IS
		// agreement, whatever it calls the status.
		//
		// That includes a provider calling the authorization dead — expired,
		// canceled — while this module still holds it. No money moved, so
		// nothing here is wrong about money; and the next capture on that
		// session FAILS, which the saga already handles correctly by
		// compensating. Reporting it would put every routinely expired
		// authorization into the one listing that must stay worth reading.
		report.Agreed++

		return
	}

	report.Divergences = append(report.Divergences, Divergence{
		SessionID:        ses.ID,
		CollectionID:     ses.PaymentCollectionID,
		ProviderID:       ses.ProviderID,
		ExternalID:       ses.ExternalID,
		LocalStatus:      ses.Status,
		LocalAuthorized:  ses.AuthorizedAmount,
		ProviderStatus:   inspection.Status,
		ProviderCaptured: inspection.CapturedAmount,
		CurrencyCode:     ses.CurrencyCode,
	})
}
