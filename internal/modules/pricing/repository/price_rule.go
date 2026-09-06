package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// CreatePriceRule bir fiyata kural ekler.
//
// Fiyat HİÇ YOKSA foreign key ihlali oluşur ve errors.Invalid dönülür.
//
// Fiyat SİLİNMİŞSE foreign key SUSAR ve kural yazılır: silme yumuşaktır, satır
// yerinde durur ve FK satırın deleted_at'ine değil VARLIĞINA bakar. Bir süre
// burada "kuralın yetim kalması yapısal olarak imkânsızdır" yazıyordu; ölçüldü
// ve yanlış çıktı (2026-09-06, bkz. pricing_integration_test.go'daki
// TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz).
//
// Kilit EKLENMEDİ ve bu bilinçlidir. Eklenecek kilidin koruyacağı bir karar
// yoktur: bu yol TEK depo çağrısı yapar ve servis öncesinde hiçbir şey okumaz,
// yani "oku → karar ver → yaz" yarışı oluşamaz. Yazılan kuralın sonucu da
// ULAŞILAMAZ bir satırdır — fiyatın kendisi silinmiş olduğu için aday
// sorgusuna girmez ve müşterinin ödediği tutar değişmez. Testin ikinci yarısı
// tam olarak bunu tutar.
func (r *Repo) CreatePriceRule(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return models.PriceRule{}, err
	}

	row, err := r.q.InsertPriceRule(ctx, pricingdb.InsertPriceRuleParams{
		ID:         rule.ID,
		PriceID:    rule.PriceID,
		Attribute:  rule.Attribute,
		Operator:   string(rule.Operator),
		RuleValues: rule.Values,
		CreatedAt:  fromTime(now),
	})
	if err != nil {
		return models.PriceRule{}, wrapDB(err, "fiyat kuralı eklenemedi: %s", rule.PriceID)
	}
	return toPriceRule(row), nil
}

// GetPriceRule kimliğe göre kuralı döner; yoksa errors.NotFound.
func (r *Repo) GetPriceRule(ctx context.Context, id string) (models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return models.PriceRule{}, err
	}

	row, err := r.q.GetPriceRule(ctx, id)
	if err != nil {
		return models.PriceRule{}, notFoundOr(err, CodePriceRuleNotFound, "fiyat kuralı bulunamadı: %s", id)
	}
	return toPriceRule(row), nil
}

// ListPriceRules bir fiyatın canlı kurallarını döner.
func (r *Repo) ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListPriceRulesByPrice(ctx, priceID)
	if err != nil {
		return nil, wrapDB(err, "fiyat kuralları alınamadı: %s", priceID)
	}

	rules := make([]models.PriceRule, 0, len(rows))
	for i := range rows {
		rules = append(rules, toPriceRule(rows[i]))
	}
	return rules, nil
}

// DeletePriceRule kuralı soft delete ile siler; yoksa errors.NotFound.
func (r *Repo) DeletePriceRule(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePriceRule(ctx, pricingdb.SoftDeletePriceRuleParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePriceRuleNotFound, "fiyat kuralı bulunamadı: %s", id)
	}
	return nil
}

// toPriceRule üretilen satırı domain modeline çevirir.
func toPriceRule(row pricingdb.PriceRule) models.PriceRule {
	return models.PriceRule{
		ID:        row.ID,
		PriceID:   row.PriceID,
		Attribute: row.Attribute,
		Operator:  models.RuleOperator(row.Operator),
		Values:    row.RuleValues,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
	}
}
