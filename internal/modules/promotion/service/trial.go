package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// MaxTrialEntries is the most purchases one trial prices.
//
// A trial runs the engine once per purchase, inside one request. The bound keeps a
// wide window from turning into a long computation; a window that holds more is
// refused rather than cut, because a report over the first N purchases would read
// as a report over the period.
const MaxTrialEntries = 5000

// CodeTrialCannotApply reports a promotion that discounts nothing whatever the
// purchase: it has no application method, or its mechanic and its method
// disagree. The trial says so once instead of once per purchase.
const CodeTrialCannotApply = "promotion_trial_cannot_apply"

// TrialAssumptions are what a trial sets aside, in the order they are applied to
// the promotion before it is priced (ADR 0176).
//
// Each is a condition that decides WHETHER a promotion may apply today, not WHAT
// it would take off a purchase: a draft is not active, a coupon was not typed on
// orders placed before it existed, and a campaign's window and budget are about
// when and how often. The report publishes the list, so nobody reads a trial as a
// forecast of those.
var TrialAssumptions = []string{"active", "automatic", "no_usage_limit", "no_campaign"}

// reasonsNoPurchaseChanges are the skip reasons that do not depend on the
// purchase. A promotion refused for one of them discounts nothing on any order.
var reasonsNoPurchaseChanges = []SkipReason{
	SkipMechanicUnknown,
	SkipNoApplicationMethod,
	SkipRewardMismatch,
}

// TrialEntry is one past purchase a trial prices the promotion against.
type TrialEntry struct {
	// Reference is what the checkout redeemed promotions under for this purchase:
	// the cart's identifier. It tells a purchase the promotion already discounted
	// from one it did not.
	Reference string
	// Input is the purchase in the engine's own terms, with no codes.
	Input ComputeInput
}

// TrialOutcome is what the promotion would have done to one purchase.
type TrialOutcome struct {
	// Reference is the entry's reference.
	Reference string
	// AlreadyApplied reports that the promotion was redeemed on this purchase.
	// Nothing is computed for it: its discount is already in the purchase, and
	// pricing it again would count it twice.
	AlreadyApplied bool
	// Skipped is why the promotion did not apply to this purchase, or empty.
	Skipped SkipReason
	// Result is the promotion's discount on the purchase, ALONE — no other
	// promotion is in the computation.
	Result ComputeResult
}

// TrialDiscounts prices ONE promotion against past purchases as if it had been
// published, and writes nothing (ADR 0176).
//
// # What "as if published" means
//
// The promotion is priced through the same elimination and the same arithmetic as
// every cart ([computeDiscounts]); what changes is the promotion, not the rules.
// It is made active and automatic, and its usage limit and campaign are taken off
// ([TrialAssumptions]). Its mechanic, its method, its currency and every one of its
// rules still decide.
//
// # What it does not do
//
// It prices the promotion ALONE. The caller holds each purchase's actual discount
// and can combine the two, because the engine's discounts add up and are capped
// per line rather than compounding — the caller's side of that is in the cart
// flow's trial.
//
// A promotion that could discount no purchase at all — no method, or a mechanic
// its method does not fit — is refused as a whole with [CodeTrialCannotApply].
func (s *Service) TrialDiscounts(
	ctx context.Context, promotionID string, entries []TrialEntry,
) ([]TrialOutcome, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if len(entries) > MaxTrialEntries {
		return nil, errors.Invalid(CodeInvalidInput,
			"a trial prices at most %d purchases, %d given; narrow the period", MaxTrialEntries, len(entries))
	}

	candidate, err := s.trialCandidate(ctx, promotionID)
	if err != nil {
		return nil, err
	}
	redeemed, err := s.liveReferences(ctx, promotionID)
	if err != nil {
		return nil, err
	}

	outcomes := make([]TrialOutcome, 0, len(entries))
	for i := range entries {
		outcome := TrialOutcome{Reference: entries[i].Reference}
		if _, ok := redeemed[entries[i].Reference]; ok && entries[i].Reference != "" {
			outcome.AlreadyApplied = true
			outcomes = append(outcomes, outcome)

			continue
		}

		input := entries[i].Input
		input.Codes = nil
		normalized, err := normalizeComputeInput(input, s.clock())
		if err != nil {
			return nil, err
		}
		outcome.Result = computeDiscounts([]models.PromotionCandidate{candidate}, normalized)
		if len(outcome.Result.Skipped) > 0 {
			outcome.Skipped = outcome.Result.Skipped[0].Reason
		}
		outcomes = append(outcomes, outcome)
	}

	return outcomes, nil
}

// trialCandidate reads the promotion with its method and rules and returns it as
// published.
func (s *Service) trialCandidate(ctx context.Context, promotionID string) (models.PromotionCandidate, error) {
	promotion, err := s.GetPromotion(ctx, promotionID)
	if err != nil {
		return models.PromotionCandidate{}, err
	}

	candidate := models.PromotionCandidate{Promotion: promotion}
	method, err := s.GetApplicationMethod(ctx, promotionID)
	switch {
	case err == nil:
		candidate.Method = &method
	case !errors.IsNotFound(err):
		return models.PromotionCandidate{}, err
	}
	if candidate.Rules, err = s.ListPromotionRules(ctx, promotionID); err != nil {
		return models.PromotionCandidate{}, err
	}

	published := asPublished(candidate)

	// The single elimination answers with the first reason that holds, and the
	// three that do not depend on the purchase come before every one that does.
	// So a probe with no purchase at all tells the two apart.
	probe := ComputeInput{}
	if published.Method != nil {
		probe.CurrencyCode = published.Method.CurrencyCode
	}
	if reason := skipReasonOf(published, probe); slices.Contains(reasonsNoPurchaseChanges, reason) {
		return models.PromotionCandidate{}, errors.Conflict(CodeTrialCannotApply,
			"promotion %s discounts nothing on any purchase: %s", promotionID, reason)
	}

	return published, nil
}

// asPublished returns the candidate as a trial prices it: active, automatic, and
// free of its usage limit and its campaign ([TrialAssumptions]).
func asPublished(candidate models.PromotionCandidate) models.PromotionCandidate {
	candidate.Promotion.Status = models.PromotionActive
	candidate.Promotion.IsAutomatic = true
	candidate.Promotion.UsageLimit = nil
	candidate.Promotion.CampaignID = nil
	candidate.Campaign = nil

	return candidate
}

// liveReferences returns the references the promotion is redeemed on and not
// released.
//
// It reads the redemption ledger once rather than once per purchase. A promotion
// under trial is usually a draft with an empty ledger, and one that ran has as
// many rows as uses — the pages are walked to the end either way.
func (s *Service) liveReferences(ctx context.Context, promotionID string) (map[string]struct{}, error) {
	live := map[string]struct{}{}
	for offset := int32(0); ; offset += MaxLimit {
		page, _, err := s.repo.ListRedemptions(ctx, promotionID, MaxLimit, offset)
		if err != nil {
			return nil, err
		}
		for i := range page {
			if !page[i].Released() {
				live[page[i].Reference] = struct{}{}
			}
		}
		if len(page) < int(MaxLimit) {
			return live, nil
		}
	}
}
