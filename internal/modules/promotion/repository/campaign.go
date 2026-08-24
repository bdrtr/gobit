package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// CreateCampaign yeni bir kampanya yazar.
//
// Bütçe SAYACI (budget_used) girdiden okunmaz, daima sıfırdan başlar: sayaç
// yalnızca kullanım akışının yazdığı bir defter değeridir.
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
		return models.Campaign{}, wrapDB(err, "kampanya oluşturulamadı: %s", c.CampaignIdentifier)
	}
	return toCampaign(row), nil
}

// GetCampaign kimliğe göre kampanyayı döner; yoksa errors.NotFound.
func (r *Repo) GetCampaign(ctx context.Context, id string) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.GetCampaign(ctx, id)
	if err != nil {
		return models.Campaign{}, notFoundOr(err, CodeCampaignNotFound, "kampanya bulunamadı: %s", id)
	}
	return toCampaign(row), nil
}

// GetCampaignByIdentifier iş kimliğine göre kampanyayı döner; yoksa
// errors.NotFound.
func (r *Repo) GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error) {
	if err := r.ready(); err != nil {
		return models.Campaign{}, err
	}

	row, err := r.q.GetCampaignByIdentifier(ctx, identifier)
	if err != nil {
		return models.Campaign{}, notFoundOr(err, CodeCampaignNotFound,
			"kampanya bulunamadı: %s", identifier)
	}
	return toCampaign(row), nil
}

// ListCampaigns sayfalanmış kampanya listesini ve TOPLAM sayıyı döner.
func (r *Repo) ListCampaigns(ctx context.Context, limit, offset int32) ([]models.Campaign, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListCampaigns(ctx, promotiondb.ListCampaignsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, wrapDB(err, "kampanyalar listelenemedi")
	}
	total, err := r.q.CountCampaigns(ctx)
	if err != nil {
		return nil, 0, wrapDB(err, "kampanya sayısı alınamadı")
	}

	out := make([]models.Campaign, 0, len(rows))
	for i := range rows {
		out = append(out, toCampaign(rows[i]))
	}
	return out, total, nil
}

// GetCampaignsByIDs verilen kimliklerin kampanyalarını TEK turda döner.
//
// Bulunamayan kimlik için kayıt DÖNMEZ; bu bir hata değildir.
func (r *Repo) GetCampaignsByIDs(ctx context.Context, ids []string) ([]models.Campaign, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.Campaign{}, nil
	}

	rows, err := r.q.GetCampaignsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "kampanyalar alınamadı")
	}

	out := make([]models.Campaign, 0, len(rows))
	for i := range rows {
		out = append(out, toCampaign(rows[i]))
	}
	return out, nil
}

// UpdateCampaign kampanyanın tanımını günceller; yoksa errors.NotFound.
//
// Bütçe sayacı bu yoldan DEĞİŞMEZ ve sayaç sıfır değilken bütçenin BİRİMİ
// (türü, para birimi) dondurulur; ikisinin de gerekçesi
// queries/campaign.sql'dedir. Dondurulmuş birimi değiştirme denemesi
// errors.Conflict döner (kod: [CodeBudgetUnitLocked]).
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
		return models.Campaign{}, wrapDB(err, "kampanya güncellenemedi: %s", c.ID)
	}
	return toCampaign(row), nil
}

// campaignUpdateRejected güncellemenin neden hiç satır döndürmediğini ayırt eder.
//
// İki sebep vardır ve istemciye AYRI görünmeleri gerekir: kampanya yoktur
// (errors.NotFound) ya da bütçe sayacı sıfır değilken bütçenin birimi
// değiştirilmek istenmiştir (errors.Conflict). Tek bir "bulunamadı" cevabı,
// operatöre var olan bir kampanyanın silindiğini düşündürürdü.
//
// Ayrımı yapmak için kayıt YENİDEN okunur; okuma güncellemenin dışındadır ama
// yarışın sonucu yalnızca hata MESAJINI etkiler, yazmayı değil — yazma kararını
// tek bir koşullu UPDATE zaten vermiştir.
func (r *Repo) campaignUpdateRejected(ctx context.Context, c models.Campaign) error {
	current, err := r.GetCampaign(ctx, c.ID)
	if err != nil {
		return err
	}
	return errors.Conflict(CodeBudgetUnitLocked,
		"kampanyanın bütçe sayacı %d; sayaç sıfırlanmadan bütçe türü ya da para birimi "+
			"değiştirilemez (mevcut: %s/%s, istenen: %s/%s)",
		current.BudgetUsed,
		current.BudgetType, currencyLabel(current.BudgetCurrencyCode),
		c.BudgetType, currencyLabel(c.BudgetCurrencyCode))
}

// currencyLabel boş para birimini okunur bir işaretle gösterir.
//
// Boş dize hata mesajında görünmez olurdu ve "usage/ → spend/TRY" biçimindeki
// bir metin, operatöre neyin değiştiğini söylemezdi.
func currencyLabel(code string) string {
	if code == "" {
		return "-"
	}
	return code
}

// DeleteCampaign kampanyayı soft delete ile siler; yoksa errors.NotFound.
//
// Kampanyanın promosyonları SİLİNMEZ, yalnızca kapsız kalır (şemadaki
// ON DELETE SET NULL yalnızca sert silmede işler; soft delete'te bağ sütunu
// dolu kalır ama okuma sorguları kampanyayı canlı bulamaz ve promosyon
// hesapta elenir — bkz. servis katmanındaki eleme kuralı).
func (r *Repo) DeleteCampaign(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if _, err := r.q.SoftDeleteCampaign(ctx, promotiondb.SoftDeleteCampaignParams{
		ID:        id,
		DeletedAt: fromTime(now),
	}); err != nil {
		return notFoundOr(err, CodeCampaignNotFound, "kampanya bulunamadı: %s", id)
	}
	return nil
}

// toCampaign üretilen satırı domain modeline çevirir.
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

// campaignGone kampanyanın kullanım anında kaybolduğunu bildiren hatadır.
//
// Kilit alınamayan bir kampanya, işlem başladıktan sonra silinmiş demektir;
// çakışma olarak sınıflandırılır çünkü istek geçerliydi ve yeniden denenebilir.
func campaignGone(id string) error {
	return errors.Conflict(CodeCampaignNotFound,
		"kampanya kullanım sırasında kayboldu: %s", id)
}
