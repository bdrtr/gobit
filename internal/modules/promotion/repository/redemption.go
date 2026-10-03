package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// Redeem redeems the promotion for a reference and increments the counters.
//
// ONLY ID, PromotionID, Reference, Amount and CurrencyCode are read from req;
// CampaignID, BudgetDelta and the timestamps are IGNORED because they are
// derived from the campaign read under the lock. Had a budget share sent by the
// caller been accepted, whether the ledger and the counter diverge would have
// been in the client's hands.
//
// The second return value reports whether the record was created IN THIS CALL;
// if it is false, the redemption already existed and no counter changed.
//
// # Why one transaction and two locks
//
// The steps run in a single transaction and ALWAYS in the same lock order
// (first the promotion, then the campaign):
//
//  1. The promotion row is locked with FOR UPDATE. This makes all concurrent
//     redemptions of the same promotion SERIAL; a "read first, then write"
//     race cannot form.
//  2. If there is a VALID redemption for the same reference, that record is
//     returned and the counters are NOT TOUCHED — that is the idempotency.
//     Because the lookup happens under the lock, only one of two concurrent
//     calls creates the record.
//  3. The promotion's STATUS is checked; if it is not live, errors.Conflict is
//     returned.
//  4. If there is a campaign, its row is locked; the date window must cover
//     the moment of redemption, and if the budget is measured in a currency,
//     the redemption's currency MUST match it (otherwise two currencies would
//     have been added up on the same counter).
//  5. The counters are incremented with a CONDITIONAL UPDATE: if the limit
//     would be exceeded, the row is not updated and the transaction is rolled
//     back with errors.Conflict.
//  6. The ledger row is written; the counter and the ledger are either written
//     together or not at all.
//
// A fixed lock order is mandatory: when two promotions tied to the same
// campaign are redeemed concurrently, both want the same campaign row, and the
// opposite order means a deadlock.
//
// # The eligibility checks come AFTER THE IDEMPOTENCY
//
// The status and window checks come after step 2 ON PURPOSE. Had the order
// been reversed, the saga step of a promotion stopped after its redemption was
// written would have returned an error when it ran again; yet that redemption
// is already in the ledger, and a repeat means only reading the existing
// record. The compensation (see [Repo.Release]) does no eligibility check for
// the same reason: the redemption of a stopped promotion must be releasable
// too.
func (r *Repo) Redeem(ctx context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error) {
	var (
		out     models.Redemption
		created bool
	)

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		promoRow, err := q.LockPromotion(ctx, req.PromotionID)
		if err != nil {
			return notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", req.PromotionID)
		}
		promo := toPromotion(promoRow)

		existing, err := q.GetActiveRedemption(ctx, promotiondb.GetActiveRedemptionParams{
			PromotionID: req.PromotionID,
			Reference:   req.Reference,
		})
		switch {
		case err == nil:
			out, created = toRedemption(existing), false
			return nil
		case !errors.Is(err, pgx.ErrNoRows):
			return wrapDB(err, "the redemption record could not be read: %s/%s", req.PromotionID, req.Reference)
		}

		if promo.Status != models.PromotionActive {
			return errors.Conflict(CodePromotionNotActive,
				"the promotion is not live: %s (status: %s)", req.PromotionID, promo.Status)
		}

		delta, campaignID, err := lockBudget(ctx, q, promo, req, now)
		if err != nil {
			return err
		}

		if _, err := q.IncrementPromotionUsage(ctx, promotiondb.IncrementPromotionUsageParams{
			ID:  req.PromotionID,
			Now: fromTime(now),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.Conflict(CodeUsageLimitReached,
					"the promotion's uses have run out: %s", req.PromotionID)
			}
			return wrapDB(err, "the usage counter could not be incremented: %s", req.PromotionID)
		}

		if delta > 0 && campaignID != nil {
			if _, err := q.IncrementCampaignBudget(ctx, promotiondb.IncrementCampaignBudgetParams{
				ID:    *campaignID,
				Delta: delta,
				Now:   fromTime(now),
			}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errors.Conflict(CodeBudgetExceeded,
						"the campaign budget does not suffice: %s (requested: %d)", *campaignID, delta)
				}
				return wrapDB(err, "the campaign budget could not be incremented: %s", *campaignID)
			}
		}

		row, err := q.InsertRedemption(ctx, promotiondb.InsertRedemptionParams{
			ID:           req.ID,
			PromotionID:  req.PromotionID,
			CampaignID:   campaignID,
			Reference:    req.Reference,
			Amount:       req.Amount,
			CurrencyCode: req.CurrencyCode,
			BudgetDelta:  delta,
			CreatedAt:    fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "the redemption record could not be written: %s/%s", req.PromotionID, req.Reference)
		}

		out, created = toRedemption(row), true
		return nil
	})
	if err != nil {
		return models.Redemption{}, false, err
	}
	return out, created, nil
}

// lockBudget locks the campaign, verifies that it is usable at the moment of
// redemption and computes how much of the budget will be consumed.
//
// For a promotion without a campaign it returns zero and nil. If the campaign
// id is filled but the row cannot be found, the campaign was DELETED during the
// transaction and a conflict is returned: writing to a deleted campaign's
// budget would have left the ledger without an owner.
func lockBudget(
	ctx context.Context,
	q *promotiondb.Queries,
	promo models.Promotion,
	req models.Redemption,
	now time.Time,
) (delta int64, campaignID *string, err error) {
	if promo.CampaignID == nil {
		return 0, nil, nil
	}

	campRow, err := q.LockCampaign(ctx, *promo.CampaignID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, campaignGone(*promo.CampaignID)
		}
		return 0, nil, wrapDB(err, "the campaign could not be locked: %s", *promo.CampaignID)
	}
	campaign := toCampaign(campRow)

	// The budget of a campaign whose window is closed cannot be spent. The check
	// is made AFTER the lock so that the window and the counter are a record of
	// the same moment; this is already where the budget limit is refereed, and
	// leaving the window to some other place would have left a promotion that is
	// skipped in the computation but accepted at redemption.
	if !campaign.WindowContains(now) {
		return 0, nil, errors.Conflict(CodeCampaignWindowClosed,
			"the campaign's date window does not cover the moment of redemption: %s", campaign.ID)
	}

	// A budget measured in a currency can be consumed only in its OWN currency.
	// Otherwise 100 TRY and 100 USD would be added up on the same counter and the
	// budget would lose its meaning.
	if campaign.BudgetType == models.BudgetSpend && campaign.BudgetCurrencyCode != req.CurrencyCode {
		return 0, nil, errors.Conflict(CodeBudgetCurrencyMismatch,
			"the campaign budget is in %s; the redemption came in %s (campaign: %s)",
			campaign.BudgetCurrencyCode, req.CurrencyCode, campaign.ID)
	}

	id := campaign.ID
	return campaign.BudgetDeltaFor(req.Amount), &id, nil
}

// Release releases a redemption and rolls the counters back.
//
// The second return value reports whether anything was rolled back IN THIS
// CALL.
//
// # IT IS IDEMPOTENT
//
// Because it is called as a saga compensation it has to be safe to run again
// (plan Section 5.5). There are two defenses:
//
//   - The row lock: the valid redemption is locked with FOR UPDATE, so only one
//     of two concurrent Releases can release the row.
//   - The conditional UPDATE: the marking writes only while released_at IS
//     NULL.
//
// If there is NO redemption at all (or it was already released), the call
// returns NO error: the compensation must also be able to run after a step
// that blew up before writing. If the promotion ITSELF does not exist,
// errors.NotFound is returned — that is a setup error the compensation must not
// silently swallow.
//
// If the campaign has been deleted in the meantime, the budget decrement is
// SKIPPED and the release still completes: a campaign being deleted is not a
// good enough reason to fail the compensation.
func (r *Repo) Release(
	ctx context.Context,
	promotionID, reference string,
	now time.Time,
) (models.Redemption, bool, error) {
	var (
		out      models.Redemption
		released bool
	)

	err := r.inTx(ctx, func(q *promotiondb.Queries) error {
		if _, err := q.LockPromotion(ctx, promotionID); err != nil {
			return notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", promotionID)
		}

		locked, err := q.LockActiveRedemption(ctx, promotiondb.LockActiveRedemptionParams{
			PromotionID: promotionID,
			Reference:   reference,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// There is nothing to roll back; it means the compensation already
				// ran or no redemption was ever written.
				return nil
			}
			return wrapDB(err, "the redemption record could not be locked: %s/%s", promotionID, reference)
		}
		redemption := toRedemption(locked)

		if redemption.CampaignID != nil && redemption.BudgetDelta > 0 {
			if _, err := q.DecrementCampaignBudget(ctx, promotiondb.DecrementCampaignBudgetParams{
				ID:    *redemption.CampaignID,
				Delta: redemption.BudgetDelta,
				Now:   fromTime(now),
			}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return wrapDB(err, "the campaign budget could not be decremented: %s", *redemption.CampaignID)
			}
		}

		if _, err := q.DecrementPromotionUsage(ctx, promotiondb.DecrementPromotionUsageParams{
			ID:  promotionID,
			Now: fromTime(now),
		}); err != nil {
			return wrapDB(err, "the usage counter could not be decremented: %s", promotionID)
		}

		row, err := q.MarkRedemptionReleased(ctx, promotiondb.MarkRedemptionReleasedParams{
			ID:         redemption.ID,
			ReleasedAt: fromTime(now),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				// It means the record was released while we held the lock —
				// under the row lock this CANNOT happen. Landing here shows that
				// the lock did not work, and the situation is not silently
				// swallowed; but the class is Conflict (not Internal), because
				// the request was valid and can be retried. In this branch the
				// counters are rolled back together with the transaction.
				return errors.Conflict(CodeRedemptionRaced,
					"another call got in while the redemption record was being released: %s", redemption.ID)
			}
			return wrapDB(err, "the redemption record could not be released: %s", redemption.ID)
		}

		out, released = toRedemption(row), true
		return nil
	})
	if err != nil {
		return models.Redemption{}, false, err
	}
	return out, released, nil
}

// GetRedemption returns a reference's VALID redemption; if there is none,
// errors.NotFound.
func (r *Repo) GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error) {
	if err := r.ready(); err != nil {
		return models.Redemption{}, err
	}

	row, err := r.q.GetActiveRedemption(ctx, promotiondb.GetActiveRedemptionParams{
		PromotionID: promotionID,
		Reference:   reference,
	})
	if err != nil {
		return models.Redemption{}, notFoundOr(err, CodePromotionNotFound,
			"redemption record not found: %s/%s", promotionID, reference)
	}
	return toRedemption(row), nil
}

// ListRedemptions returns a promotion's redemption ledger, paginated.
//
// Released records are returned TOO: the ledger is a history, and the trace of
// a redemption that was rolled back must not be erased.
func (r *Repo) ListRedemptions(
	ctx context.Context,
	promotionID string,
	limit, offset int32,
) ([]models.Redemption, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListRedemptions(ctx, promotiondb.ListRedemptionsParams{
		PromotionID: promotionID,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the redemption ledger could not be read: %s", promotionID)
	}
	total, err := r.q.CountRedemptions(ctx, promotionID)
	if err != nil {
		return nil, 0, wrapDB(err, "the redemption count could not be read: %s", promotionID)
	}

	out := make([]models.Redemption, 0, len(rows))
	for i := range rows {
		out = append(out, toRedemption(rows[i]))
	}
	return out, total, nil
}

// toRedemption turns the generated row into the domain model.
func toRedemption(row promotiondb.PromotionRedemption) models.Redemption {
	return models.Redemption{
		ID:           row.ID,
		PromotionID:  row.PromotionID,
		CampaignID:   copyString(row.CampaignID),
		Reference:    row.Reference,
		Amount:       row.Amount,
		CurrencyCode: row.CurrencyCode,
		BudgetDelta:  row.BudgetDelta,
		CreatedAt:    toTime(row.CreatedAt),
		UpdatedAt:    toTime(row.UpdatedAt),
		ReleasedAt:   toTimePtr(row.ReleasedAt),
	}
}
