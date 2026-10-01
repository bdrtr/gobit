package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreateCampaign writes a new campaign.
//
// The budget COUNTER (budget_used) is not read from the input and always starts
// at zero: the counter is a ledger value that only the usage flow writes.
func (r *Repo) CreateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.InsertCampaign(ctx, promotiondb.InsertCampaignParams{
		ID:                 c.ID,
		Name:               c.Name,
		CampaignIdentifier: c.CampaignIdentifier,
		Description:        c.Description,
		StartsAt:           fromTimePtr(c.StartsAt),
		EndsAt:             fromTimePtr(c.EndsAt),
		BudgetType:         string(c.BudgetType),
		BudgetLimit:        copyInt64(c.BudgetLimit),
		BudgetCurrencyCode: nilIfEmpty(c.BudgetCurrencyCode),
		CreatedAt:          fromTime(now),
	})
	if err != nil {
		// The operator chose the identifier; the constraint's name is not
		// theirs (ADR 0319).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlstateUniqueViolation {
			return models.Campaign{}, errors.Conflict(CodeDuplicate,
				"a campaign with the identifier %s exists", c.CampaignIdentifier)
		}
		return models.Campaign{}, wrapDB(err, "the campaign %s could not be created", c.CampaignIdentifier)
	}
	return toCampaign(row), nil
}

// GetCampaign returns the campaign by id; errors.NotFound if there is none.
func (r *Repo) GetCampaign(ctx context.Context, id string) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.GetCampaign(ctx, id)
	if err != nil {
		return models.Campaign{}, notFoundOr(err, CodeCampaignNotFound, "campaign not found: %s", id)
	}
	return toCampaign(row), nil
}

// GetCampaignByIdentifier returns the campaign by business identifier;
// errors.NotFound if there is none.
func (r *Repo) GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.GetCampaignByIdentifier(ctx, identifier)
	if err != nil {
		return models.Campaign{}, notFoundOr(err, CodeCampaignNotFound,
			"campaign not found: %s", identifier)
	}
	return toCampaign(row), nil
}

// ListCampaigns returns a paged list of campaigns and the TOTAL count.
func (r *Repo) ListCampaigns(ctx context.Context, limit, offset int32) ([]models.Campaign, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCampaigns(ctx, promotiondb.ListCampaignsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "the campaigns could not be listed")
	}
	total, err := r.q.CountCampaigns(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "the campaign count could not be read")
	}

	out := make([]models.Campaign, 0, len(rows))
	for i := range rows {
		out = append(out, toCampaign(rows[i]))
	}
	return out, total, nil
}

// GetCampaignsByIDs returns the campaigns of the given ids in a SINGLE round
// trip.
//
// An id that is not found returns NO record; this is not an error.
func (r *Repo) GetCampaignsByIDs(ctx context.Context, ids []string) ([]models.Campaign, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Campaign{}, nil
	}

	rows, err := r.q.GetCampaignsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "the campaigns could not be read")
	}

	out := make([]models.Campaign, 0, len(rows))
	for i := range rows {
		out = append(out, toCampaign(rows[i]))
	}
	return out, nil
}

// UpdateCampaign updates the campaign's definition; errors.NotFound if there is
// none.
//
// The budget counter DOES NOT CHANGE through this path, and while the counter
// is not zero the budget's UNIT (its type and currency) is frozen; the
// reasoning for both is in queries/campaign.sql. An attempt to change a frozen
// unit returns errors.Conflict (code: [CodeBudgetUnitLocked]).
func (r *Repo) UpdateCampaign(ctx context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.UpdateCampaign(ctx, promotiondb.UpdateCampaignParams{
		ID:                 c.ID,
		Name:               c.Name,
		CampaignIdentifier: c.CampaignIdentifier,
		Description:        c.Description,
		StartsAt:           fromTimePtr(c.StartsAt),
		EndsAt:             fromTimePtr(c.EndsAt),
		BudgetType:         string(c.BudgetType),
		BudgetLimit:        copyInt64(c.BudgetLimit),
		BudgetCurrencyCode: nilIfEmpty(c.BudgetCurrencyCode),
		UpdatedAt:          fromTime(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Campaign{}, r.campaignUpdateRejected(ctx, c)
		}
		return models.Campaign{}, wrapDB(err, "the campaign %s could not be updated", c.ID)
	}
	return toCampaign(row), nil
}

// campaignUpdateRejected tells apart why the update returned no row at all.
//
// There are two reasons, and the client has to see them SEPARATELY: the
// campaign does not exist (errors.NotFound), or the budget's unit was to be
// changed while the budget counter is not zero (errors.Conflict). A single
// "not found" answer would make the operator think an existing campaign had
// been deleted.
//
// To tell them apart the record is read AGAIN; the read is outside the update,
// but the outcome of a race affects only the error MESSAGE, not the write — the
// write decision has already been made by a single conditional UPDATE.
func (r *Repo) campaignUpdateRejected(ctx context.Context, c models.Campaign) error {
	current, err := r.GetCampaign(ctx, c.ID)
	if err != nil {
		return err
	}
	return errors.Conflict(CodeBudgetUnitLocked,
		"the campaign's budget counter is %d; the budget type or currency cannot be "+
			"changed until the counter is reset (current: %s/%s, requested: %s/%s)",
		current.BudgetUsed,
		current.BudgetType, currencyLabel(current.BudgetCurrencyCode),
		c.BudgetType, currencyLabel(c.BudgetCurrencyCode))
}

// currencyLabel shows an empty currency with a readable mark.
//
// An empty string would be invisible in the error message, and a text of the
// form "usage/ → spend/TRY" would not tell the operator what changed.
func currencyLabel(code string) string {
	if code == "" {
		return "-"
	}
	return code
}

// DeleteCampaign deletes the campaign with a soft delete; errors.NotFound if
// there is none.
//
// The campaign's promotions are NOT DELETED, they are only left without their
// campaign (the schema's ON DELETE SET NULL acts only on a hard delete; on a
// soft delete the link column stays filled, but the read queries cannot find
// the campaign live and the promotion is skipped in the computation — see the
// skip rule in the service layer).
func (r *Repo) DeleteCampaign(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCampaign(ctx, promotiondb.SoftDeleteCampaignParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeCampaignNotFound, "campaign not found: %s", id)
	}
	return nil
}

// toCampaign converts the generated row into the domain model.
func toCampaign(row promotiondb.Campaign) models.Campaign {
	return models.Campaign{
		ID:                 row.ID,
		Name:               row.Name,
		CampaignIdentifier: row.CampaignIdentifier,
		Description:        row.Description,
		StartsAt:           toTimePtr(row.StartsAt),
		EndsAt:             toTimePtr(row.EndsAt),
		BudgetType:         models.CampaignBudgetType(row.BudgetType),
		BudgetLimit:        copyInt64(row.BudgetLimit),
		BudgetUsed:         row.BudgetUsed,
		BudgetCurrencyCode: deref(row.BudgetCurrencyCode),
		CreatedAt:          toTime(row.CreatedAt),
		UpdatedAt:          toTime(row.UpdatedAt),
		DeletedAt:          toTimePtr(row.DeletedAt),
	}
}

// campaignGone is the error that reports a campaign vanishing at the moment of
// use.
//
// A campaign whose lock cannot be taken has been deleted after the transaction
// started; it is classified as a conflict because the request was valid and
// can be retried.
func campaignGone(id string) error {
	return errors.Conflict(CodeCampaignNotFound,
		"the campaign vanished during use: %s", id)
}
