package repository

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreatePromotion yeni bir promosyon yazar.
//
// Kullanım SAYACI (usage_count) girdiden okunmaz, daima sıfırdan başlar.
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
		return models.Promotion{}, wrapDB(err, "promosyon oluşturulamadı: %s", p.Code)
	}
	return toPromotion(row), nil
}

// GetPromotion kimliğe göre promosyonu döner; yoksa errors.NotFound.
func (r *Repo) GetPromotion(ctx context.Context, id string) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.GetPromotion(ctx, id)
	if err != nil {
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", id)
	}
	return toPromotion(row), nil
}

// GetPromotionByCode kupon koduna göre promosyonu döner; yoksa errors.NotFound.
//
// Kod BÜYÜK harf saklanır; çağıran normalleştirmeyi yapmış olmalıdır.
func (r *Repo) GetPromotionByCode(ctx context.Context, code string) (models.Promotion, error) {
	if err := r.ready(); err != nil {
		return models.Promotion{}, err
	}

	row, err := r.q.GetPromotionByCode(ctx, code)
	if err != nil {
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", code)
	}
	return toPromotion(row), nil
}

// ListPromotions sayfalanmış promosyon listesini ve TOPLAM sayıyı döner.
//
// status ve campaignID isteğe bağlı süzgeçlerdir; nil "süzme" demektir, boş
// dize değil. Ayrım anlamlıdır: boş bir durum dizesi hiçbir kayda uymazken
// nil, süzgecin hiç uygulanmaması demektir.
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
		return nil, 0, wrapDB(err, "promosyonlar listelenemedi")
	}
	total, err := r.q.CountPromotions(ctx, promotiondb.CountPromotionsParams{
		Status:     copyString(status),
		CampaignID: copyString(campaignID),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "promosyon sayısı alınamadı")
	}

	out := make([]models.Promotion, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotion(rows[i]))
	}
	return out, total, nil
}

// GetPromotionsByIDs verilen kimliklerin promosyonlarını TEK turda döner.
//
// Bulunamayan kimlik için kayıt DÖNMEZ; bu bir hata değildir (ADR 0004).
func (r *Repo) GetPromotionsByIDs(ctx context.Context, ids []string) ([]models.Promotion, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Promotion{}, nil
	}

	rows, err := r.q.GetPromotionsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "promosyonlar alınamadı")
	}

	out := make([]models.Promotion, 0, len(rows))
	for i := range rows {
		out = append(out, toPromotion(rows[i]))
	}
	return out, nil
}

// UpdatePromotion promosyonun tanımını günceller; yoksa errors.NotFound.
//
// Kullanım sayacı bu yoldan DEĞİŞMEZ (bkz. queries/promotion.sql'deki gerekçe).
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
		return models.Promotion{}, notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", p.ID)
	}
	return toPromotion(row), nil
}

// DeletePromotion promosyonu soft delete ile siler; yoksa errors.NotFound.
//
// Uygulama yöntemi ve kurallar silinmez ama okunmaz hâle gelir: hepsi
// promosyon üzerinden okunur ve promosyon canlı değildir.
func (r *Repo) DeletePromotion(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeletePromotion(ctx, promotiondb.SoftDeletePromotionParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodePromotionNotFound, "promosyon bulunamadı: %s", id)
	}
	return nil
}

// ListCandidates hesaplamaya girebilecek adayları TEK TURDA (dört sorguyla)
// döner: aktif otomatik promosyonlar ve verilen kodlara sahip promosyonlar.
//
// Dört sorgu SABİTTİR ve aday sayısından bağımsızdır: promosyonlar, uygulama
// yöntemleri, kurallar ve kampanyalar toplu okunur. Aday başına sorgu (N+1)
// yapılmaz — bir sepet hesabı her turda çalıştığı için N+1 burada doğrudan
// gecikme demektir.
//
// codes nil ya da boş olabilir; o durumda yalnızca otomatik promosyonlar döner.
// Kodlar BÜYÜK harfe normalleştirilmiş olarak beklenir.
func (r *Repo) ListCandidates(ctx context.Context, codes []string) ([]models.PromotionCandidate, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if codes == nil {
		// pgx boş dilim ile nil'i aynı biçimde kodlar; yine de nil bir dizi
		// argümanı göndermemek için boş dilime çevrilir.
		codes = []string{}
	}

	rows, err := r.q.ListApplicablePromotions(ctx, codes)
	if err != nil {
		return nil, wrapDB(err, "uygulanabilir promosyonlar alınamadı")
	}
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
		return nil, wrapDB(err, "uygulama yöntemleri alınamadı")
	}
	methods := make(map[string]models.ApplicationMethod, len(methodRows))
	for i := range methodRows {
		methods[methodRows[i].PromotionID] = toApplicationMethod(methodRows[i])
	}

	ruleRows, err := r.q.ListPromotionRulesByPromotions(ctx, promotionIDs)
	if err != nil {
		return nil, wrapDB(err, "promosyon kuralları alınamadı")
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

// toPromotion üretilen satırı domain modeline çevirir.
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

// copyString bir dize işaretçisini KOPYALAYARAK döner; gerekçe copyInt64'teki
// ile aynıdır.
func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
