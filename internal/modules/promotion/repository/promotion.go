package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotion writes a new promotion.
//
// The usage COUNTER (usage_count) is not read from the input; it always starts
// from zero.
func (r *Repo) CreatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}
	metadata, err := encodeMetadata(p.Metadata)
	if err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.InsertPromotion(ctx, promotiondb.InsertPromotionParams{
		ID:          p.ID,
		Code:        p.Code,
		IsAutomatic: p.IsAutomatic,
		Type:        string(p.Type),
		CampaignID:  copyString(p.CampaignID),
		Status:      string(p.Status),
		UsageLimit:  copyInt64(p.UsageLimit),
		Metadata:    metadata,
		CreatedAt:   fromTime(now),
	})
	if err != nil {
		return models.Promotion{}, wrapDB(err, "the promotion could not be created: %s", p.Code)
	}
	return toPromotion(row), nil
}

// GetPromotion returns the promotion by id; if there is none, errors.NotFound.
func (r *Repo) GetPromotion(ctx context.Context, id string) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.GetPromotion(ctx, id)
	if err != nil {
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", id)
	}
	return toPromotion(row), nil
}

// GetPromotionByCode returns the promotion by coupon code; if there is none,
// errors.NotFound.
//
// The code is stored in UPPER case; the caller must already have normalized
// it.
func (r *Repo) GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.GetPromotionByCode(ctx, code)
	if err != nil {
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", code)
	}
	return toPromotion(row), nil
}

// ListPromotions returns the paginated promotion list and the TOTAL count.
//
// status and campaignID are optional filters; nil means "do not filter", the
// empty string does not. The difference is meaningful: an empty status string
// matches no record, while nil means the filter is not applied at all.
func (r *Repo) ListPromotions(
	ctx context.Context,
	status, campaignID *string,
	limit, offset int32,
) ([]models.Promotion, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListPromotions(ctx, promotiondb.ListPromotionsParams{
		Status:     copyString(status),
		CampaignID: copyString(campaignID),
		RowLimit:   int64(limit),
		RowOffset:  int64(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the promotions could not be listed")
	}
	total, err := r.q.CountPromotions(ctx, promotiondb.CountPromotionsParams{
		Status:     copyString(status),
		CampaignID: copyString(campaignID),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "the promotion count could not be read")
	}

	out := make([]models.Promotion, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotion(rows[i]))
	}
	return out, total, nil
}

// GetPromotionsByIDs returns the promotions of the given ids in ONE round trip.
//
// An id that is not found returns NO record; that is not an error (ADR 0004).
func (r *Repo) GetPromotionsByIDs(ctx context.Context, ids []string) ([]models.Promotion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Promotion{}, nil
	}

	rows, err := r.q.GetPromotionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the promotions could not be read")
	}

	out := make([]models.Promotion, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotion(rows[i]))
	}
	return out, nil
}

// UpdatePromotion updates the promotion's definition; if there is none,
// errors.NotFound.
//
// The usage counter does NOT CHANGE on this path (see the reasoning in
// queries/promotion.sql).
func (r *Repo) UpdatePromotion(ctx context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}
	metadata, err := encodeMetadata(p.Metadata)
	if err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.UpdatePromotion(ctx, promotiondb.UpdatePromotionParams{
		ID:          p.ID,
		Code:        p.Code,
		IsAutomatic: p.IsAutomatic,
		Type:        string(p.Type),
		CampaignID:  copyString(p.CampaignID),
		Status:      string(p.Status),
		UsageLimit:  copyInt64(p.UsageLimit),
		Metadata:    metadata,
		UpdatedAt:   fromTime(now),
	})
	if err != nil {
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", p.ID)
	}
	return toPromotion(row), nil
}

// DeletePromotion deletes the promotion with a soft delete; if there is none,
// errors.NotFound.
//
// The application method and the rules are NOT DELETED, and they do NOT ENTER
// the computation either: the computation's candidate query
// (ListApplicablePromotions) looks at the promotion's deleted_at, and so does
// the coupon path (GetPromotionByCode). A deleted promotion's method or rule
// cannot enter any cart computation (measured, 2026-09-06).
//
// Saying "everything is read through the promotion" would be WRONG, and for a
// while it said exactly that: [Repo.GetApplicationMethod] and
// [Repo.GetPromotionRule] look only at the deleted_at of their OWN rows and do
// not JOIN the promotion. Both return a row of a deleted promotion. Today this
// has no consequence — neither has an HTTP path, and GetApplicationMethod's
// only caller (service.storeCandidate) already resolves the promotion from the
// code first and never gets there if it is deleted — but having no
// consequence does not mean it is not there.
func (r *Repo) DeletePromotion(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePromotion(ctx, promotiondb.SoftDeletePromotionParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", id)
	}
	return nil
}

// ListCandidates returns the candidates that can enter the computation in ONE
// ROUND (with four queries): the active automatic promotions and the
// promotions that carry the given codes.
//
// The four queries are FIXED and independent of the number of candidates:
// promotions, application methods, rules and campaigns are read in bulk. There
// is no query per candidate (N+1) — because a cart computation runs on every
// round, N+1 here directly means latency.
//
// codes may be nil or empty; in that case only the automatic promotions are
// returned. The codes are expected to be normalized to UPPER case.
func (r *Repo) ListCandidates(ctx context.Context, codes []string) ([]models.PromotionCandidate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if codes == nil {
		// pgx encodes an empty slice and nil the same way; it is still turned
		// into an empty slice so that a nil array argument is never sent.
		codes = []string{}
	}

	rows, err := r.q.ListApplicablePromotions(ctx, codes)
	if err != nil {
		return nil, wrapDB(err, "the applicable promotions could not be read")
	}

	return r.candidatesOf(ctx, rows)
}

// ListCandidatesForDiagnosis returns the candidates WITHOUT THE STATUS FILTER.
//
// The admin side's "why was it not applied" answer is produced from this read:
// the most common reason a code does nothing is that the promotion has NOT
// BEEN PUBLISHED, and because the filtered read never returns it, that answer
// could not be given.
//
// The skip rule does not live in THIS read; it lives in service.skipReasonOf.
// The same reasoning is written in service.storeCandidate's godoc: if the
// status check is done in Go, the "what applies" rule stays in ONE place and is
// not split between the query's WHERE and Go — if it were split, a change that
// removed the filter would fail no test.
//
// The HOT PATH does not use this: the cart total is recomputed on every change,
// and the status filter is what keeps that read limited to applicable
// promotions.
func (r *Repo) ListCandidatesForDiagnosis(
	ctx context.Context, codes []string,
) ([]models.PromotionCandidate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if codes == nil {
		codes = []string{}
	}

	rows, err := r.q.ListCandidatesForDiagnosis(ctx, codes)
	if err != nil {
		return nil, wrapDB(err, "the candidate promotions could not be read")
	}

	return r.candidatesOf(ctx, rows)
}

// candidatesOf turns promotion rows into candidates together with their
// methods, rules and campaigns.
//
// Both reads come down here, and their NOT diverging is REQUIRED: all four of
// the sub-reads are the same, and writing them twice would have meant a field
// added to one going missing from the other — the two candidate lists feed the
// same computation, and the only difference between them has to be the members
// that are SKIPPED.
func (r *Repo) candidatesOf(
	ctx context.Context, rows []promotiondb.Promotion,
) ([]models.PromotionCandidate, error) {
	if len(rows) == 0 {
		return []models.PromotionCandidate{}, nil
	}

	promotionIDs := make([]string, 0, len(rows))
	campaignIDs := make([]string, 0, len(rows))
	for i := range rows {
		promotionIDs = append(promotionIDs, rows[i].ID)
		if rows[i].CampaignID != nil {
			campaignIDs = append(campaignIDs, *rows[i].CampaignID)
		}
	}

	methodRows, err := r.q.GetApplicationMethodsByPromotions(ctx, promotionIDs)
	if err != nil {
		return nil, wrapDB(err, "the application methods could not be read")
	}
	methods := make(map[string]models.ApplicationMethod, len(methodRows))
	for i := range methodRows {
		methods[methodRows[i].PromotionID] = toApplicationMethod(methodRows[i])
	}

	ruleRows, err := r.q.ListPromotionRulesByPromotions(ctx, promotionIDs)
	if err != nil {
		return nil, wrapDB(err, "the promotion rules could not be read")
	}
	rules := make(map[string][]models.PromotionRule, len(promotionIDs))
	for i := range ruleRows {
		rule := toPromotionRule(ruleRows[i])
		rules[rule.PromotionID] = append(rules[rule.PromotionID], rule)
	}

	campaigns := map[string]models.Campaign{}
	if len(campaignIDs) > 0 {
		found, campErr := r.GetCampaignsByIDs(ctx, campaignIDs)
		if campErr != nil {
			return nil, campErr
		}
		for i := range found {
			campaigns[found[i].ID] = found[i]
		}
	}

	out := make([]models.PromotionCandidate, 0, len(rows))
	for i := range rows {
		promo := toPromotion(rows[i])
		candidate := models.PromotionCandidate{
			Promotion: promo,
			Rules:     rules[promo.ID],
		}
		if method, ok := methods[promo.ID]; ok {
			candidate.Method = &method
		}
		if promo.CampaignID != nil {
			if campaign, ok := campaigns[*promo.CampaignID]; ok {
				candidate.Campaign = &campaign
			}
		}
		out = append(out, candidate)
	}
	return out, nil
}

// requireLivePromotion verifies under a SHARED lock that the promotion is
// LIVE; if there is none (or it has been deleted), errors.NotFound is returned.
//
// Every path that writes rows UNDER a promotion must call this, and the call
// must be in the SAME transaction as the write: the lock is released when the
// transaction ends, so a lock without a transaction protects nothing.
//
// The rejected alternative was doing the check IN THE SERVICE — and for a
// while it was done that way. In that shape the existence check and the write
// are two SEPARATE autocommit statements; a soft delete that gets in between
// makes the read stale and the write still lands. A foreign key cannot catch
// this, because a soft delete leaves the row in place and the FK looks at the
// row's EXISTENCE, not at its deleted_at (measured, 2026-09-06; see
// TestAddingARuleDoesNotWriteUnderADeletedPromotion in
// promotion_integration_test.go).
func requireLivePromotion(ctx context.Context, q *promotiondb.Queries, promotionID string) error {
	if _, err := q.LockPromotionShared(ctx, promotionID); err != nil {
		return notFoundOr(err, CodePromotionNotFound, "promotion not found: %s", promotionID)
	}
	return nil
}

// toPromotion turns the generated row into the domain model.
func toPromotion(row promotiondb.Promotion) models.Promotion {
	return models.Promotion{
		ID:          row.ID,
		Code:        row.Code,
		IsAutomatic: row.IsAutomatic,
		Type:        models.PromotionType(row.Type),
		CampaignID:  copyString(row.CampaignID),
		Status:      models.PromotionStatus(row.Status),
		UsageLimit:  copyInt64(row.UsageLimit),
		UsageCount:  row.UsageCount,
		Metadata:    decodeMetadata(row.Metadata),
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}
}

// copyString returns a string pointer by COPYING it; the reasoning is the same
// as copyInt64's.
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
