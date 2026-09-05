package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/repository/taxdb"
)

// CreateTaxRateRule bir orana kural ekler.
//
// Oran KİLİT ALTINDA okunur ve varsayılan olup olmadığı orada denetlenir:
// denetim kilitsiz yapılsaydı, araya giren bir güncelleme oranı varsayılan
// yapabilir ve kural yine de yazılırdı. Varsayılan oranın kuralı olmaz.
func (r *Repo) CreateTaxRateRule(
	ctx context.Context,
	rule models.TaxRateRule,
	now time.Time,
) (models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return models.TaxRateRule{}, err
	}

	var out models.TaxRateRule
	err := r.inTx(ctx, func(q *taxdb.Queries) error {
		rate, err := q.GetTaxRateForUpdate(ctx, rule.TaxRateID)
		if err != nil {
			return notFoundOr(err, CodeTaxRateNotFound,
				"vergi oranı bulunamadı: %s", rule.TaxRateID)
		}
		if rate.IsDefault {
			return errors.Conflict(CodeConstraintViolation,
				"%s bölgenin VARSAYILAN oranıdır ve kuralı olamaz; "+
					"kurallı bir oran için ayrı bir oran tanımlayın", rule.TaxRateID)
		}

		row, err := q.InsertTaxRateRule(ctx, taxdb.InsertTaxRateRuleParams{
			ID:          rule.ID,
			TaxRateID:   rule.TaxRateID,
			Reference:   rule.Reference.String(),
			ReferenceID: rule.ReferenceID,
			CreatedAt:   fromTime(now),
		})
		if err != nil {
			return wrapDB(err, "vergi kuralı eklenemedi: %s/%s",
				rule.Reference, rule.ReferenceID)
		}

		out = toTaxRateRule(row)
		return nil
	})
	if err != nil {
		return models.TaxRateRule{}, err
	}
	return out, nil
}

// GetTaxRateRule kimliğe göre kuralı döner; yoksa errors.NotFound.
func (r *Repo) GetTaxRateRule(ctx context.Context, id string) (models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return models.TaxRateRule{}, err
	}

	row, err := r.q.GetTaxRateRule(ctx, id)
	if err != nil {
		return models.TaxRateRule{}, notFoundOr(err, CodeTaxRateRuleNotFound,
			"vergi kuralı bulunamadı: %s", id)
	}
	return toTaxRateRule(row), nil
}

// ListTaxRateRules bir oranın canlı kurallarını döner.
func (r *Repo) ListTaxRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListTaxRateRulesByRate(ctx, rateID)
	if err != nil {
		return nil, wrapDB(err, "vergi kuralları alınamadı: %s", rateID)
	}
	return toTaxRateRules(rows), nil
}

// ListTaxRateRulesByRates birden çok oranın kurallarını TEK sorguda döner
// (hesap yolunun okuma biçimi; N+1 yoktur).
func (r *Repo) ListTaxRateRulesByRates(ctx context.Context, rateIDs []string) ([]models.TaxRateRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(rateIDs) == 0 {
		return []models.TaxRateRule{}, nil
	}

	rows, err := r.q.ListTaxRateRulesByRates(ctx, rateIDs)
	if err != nil {
		return nil, wrapDB(err, "vergi kuralları alınamadı")
	}
	return toTaxRateRules(rows), nil
}

// DeleteTaxRateRule kuralı yumuşak siler; yoksa errors.NotFound.
func (r *Repo) DeleteTaxRateRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteTaxRateRule(ctx, taxdb.SoftDeleteTaxRateRuleParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeTaxRateRuleNotFound, "vergi kuralı bulunamadı: %s", id)
	}
	return nil
}

// toTaxRateRule üretilen satırı domain modeline çevirir.
func toTaxRateRule(row taxdb.TaxRateRule) models.TaxRateRule {
	return models.TaxRateRule{
		ID:          row.ID,
		TaxRateID:   row.TaxRateID,
		Reference:   models.RuleReference(row.Reference),
		ReferenceID: row.ReferenceID,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}
}

// toTaxRateRules satır dilimini domain modellerine çevirir.
func toTaxRateRules(rows []taxdb.TaxRateRule) []models.TaxRateRule {
	out := make([]models.TaxRateRule, 0, len(rows))
	for i := range rows {
		out = append(out, toTaxRateRule(rows[i]))
	}
	return out
}
