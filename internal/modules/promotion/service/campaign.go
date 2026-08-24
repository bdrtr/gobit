package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CampaignInput bir kampanyanın yazma girdisidir.
type CampaignInput struct {
	// Name kampanyanın görünen adıdır; boş olamaz.
	Name string
	// CampaignIdentifier operatörün verdiği benzersiz iş kimliğidir.
	CampaignIdentifier string
	// Description isteğe bağlı açıklamadır.
	Description string
	// StartsAt geçerlilik penceresinin başıdır; nil ise alt sınır yoktur.
	StartsAt *time.Time
	// EndsAt geçerlilik penceresinin sonudur; nil ise üst sınır yoktur.
	EndsAt *time.Time
	// BudgetType bütçenin ölçü birimidir; boş verilirse "none" kabul edilir.
	BudgetType models.CampaignBudgetType
	// BudgetLimit bütçenin üst sınırıdır; nil ise sınır yoktur.
	BudgetLimit *int64
	// BudgetCurrencyCode "spend" bütçesinin para birimidir; diğer türlerde
	// verilmemelidir.
	BudgetCurrencyCode string
}

// CreateCampaign yeni bir kampanya oluşturur.
//
// İş kimliği (CampaignIdentifier) BENZERSİZDİR; aynı kimlik ikinci kez
// alınamaz ve deneme errors.Conflict döner. Benzersizliği veritabanı kısmi
// indeksi zorlar, servis değil: iki eşzamanlı istek arasında yalnızca
// veritabanı hakem olabilir.
//
// Bütçe SAYACI sıfırdan başlar ve bu yoldan yazılamaz.
func (s *Service) CreateCampaign(ctx context.Context, in CampaignInput) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}

	now := s.clock()
	campaign, err := buildCampaign(models.NewCampaignID(now), in, now)
	if err != nil {
		return models.Campaign{}, err
	}
	return s.repo.CreateCampaign(ctx, campaign, now)
}

// GetCampaign kimliğe göre kampanyayı döner; yoksa errors.NotFound.
func (s *Service) GetCampaign(ctx context.Context, id string) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := requireID(id, models.CampaignIDPrefix, "kampanya kimliği"); err != nil {
		return models.Campaign{}, err
	}
	return s.repo.GetCampaign(ctx, id)
}

// GetCampaignByIdentifier iş kimliğine göre kampanyayı döner; yoksa
// errors.NotFound.
func (s *Service) GetCampaignByIdentifier(ctx context.Context, identifier string) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("kampanya iş kimliği", identifier, 1, MaxIdentifierLen); err != nil {
		return models.Campaign{}, err
	}
	return s.repo.GetCampaignByIdentifier(ctx, identifier)
}

// ListCampaigns sayfalanmış kampanya listesini döner.
func (s *Service) ListCampaigns(ctx context.Context, limit, offset int32) (Page[models.Campaign], error) {
	if err := s.ready(); err != nil {
		return Page[models.Campaign]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Campaign]{}, err
	}

	items, total, err := s.repo.ListCampaigns(ctx, limit, offset)
	if err != nil {
		return Page[models.Campaign]{}, err
	}
	return Page[models.Campaign]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateCampaign kampanyanın tanımını YERİNE KOYAR; bütçe sayacı değişmez.
//
// Kısmi güncelleme değildir (gerekçe için bkz. [Service.UpdatePromotion]).
//
// # Bütçenin BİRİMİ sayaç sıfır değilken değiştirilemez
//
// Sayaç (budget_used) sıfırdan farklıyken bütçe TÜRÜNÜ ya da PARA BİRİMİNİ
// değiştirmek errors.Conflict döner. Sebep, sayacın kendisinin eski birimde
// kalmasıdır: "usage" bütçesinde biriken 30 ADET, tür "spend" yapıldığında 30
// KURUŞ olarak okunur ve bütçe sessizce anlamını yitirir. Kural veritabanında,
// tek bir koşullu UPDATE ile zorlanır (bkz. repository.UpdateCampaign) — bu
// yüzden eşzamanlı bir kullanımla yarışamaz.
//
// Tarih penceresi, ad, açıklama ve bütçe SINIRI sayaçtan bağımsız olarak her
// zaman güncellenebilir: hiçbiri sayacın birimini değiştirmez.
func (s *Service) UpdateCampaign(ctx context.Context, id string, in CampaignInput) (models.Campaign, error) {
	if err := s.ready(); err != nil {
		return models.Campaign{}, err
	}
	if err := requireID(id, models.CampaignIDPrefix, "kampanya kimliği"); err != nil {
		return models.Campaign{}, err
	}

	now := s.clock()
	campaign, err := buildCampaign(id, in, now)
	if err != nil {
		return models.Campaign{}, err
	}
	return s.repo.UpdateCampaign(ctx, campaign, now)
}

// DeleteCampaign kampanyayı soft delete ile siler.
//
// Kampanyanın promosyonları SİLİNMEZ; kapsız kaldıkları için hesapta elenirler
// (bkz. [Service.ComputeDiscounts] eleme kuralı). Bu, bir kampanyayı
// promosyonlarını kaybetmeden durdurmanın yoludur.
func (s *Service) DeleteCampaign(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CampaignIDPrefix, "kampanya kimliği"); err != nil {
		return err
	}
	return s.repo.DeleteCampaign(ctx, id, s.clock())
}

// buildCampaign girdiyi doğrular ve yazılacak domain modeline çevirir.
func buildCampaign(id string, in CampaignInput, now time.Time) (models.Campaign, error) {
	if err := validateText("kampanya adı", in.Name, 1, MaxNameLen); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("kampanya iş kimliği", in.CampaignIdentifier, 1, MaxIdentifierLen); err != nil {
		return models.Campaign{}, err
	}
	if err := validateText("kampanya açıklaması", in.Description, 0, MaxDescriptionLen); err != nil {
		return models.Campaign{}, err
	}
	if in.StartsAt != nil && in.EndsAt != nil && !in.StartsAt.Before(*in.EndsAt) {
		return models.Campaign{}, errors.Invalid(CodeInvalidInput,
			"kampanya başlangıcı bitişinden önce olmalı (başlangıç: %s, bitiş: %s)",
			in.StartsAt.UTC().Format(time.RFC3339), in.EndsAt.UTC().Format(time.RFC3339))
	}

	budgetType, limit, currency, err := normalizeBudget(in)
	if err != nil {
		return models.Campaign{}, err
	}

	return models.Campaign{
		ID:                 id,
		Name:               in.Name,
		CampaignIdentifier: in.CampaignIdentifier,
		Description:        in.Description,
		StartsAt:           copyTime(in.StartsAt),
		EndsAt:             copyTime(in.EndsAt),
		BudgetType:         budgetType,
		BudgetLimit:        limit,
		BudgetCurrencyCode: currency,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

// normalizeBudget bütçe alanlarını doğrular ve tutarlı hâle getirir.
//
// Üç kural zorlanır ve üçü de migration'daki CHECK kısıtlarıyla eşleşir:
//
//   - "spend" bütçesi para birimi İSTER; diğer türler para birimi TAŞIMAZ.
//   - Bütçesiz ("none") kampanyada sınır OLAMAZ; sınır varsa tür seçilmelidir.
//   - Sınır negatif olamaz ve [models.MaxAmount]'u aşamaz.
//
// Doğrulamanın burada da olması bilinçlidir: veritabanı kısıtı son savunmadır
// ama hatası kullanıcıya "kısıt ihlali" olarak gider; buradaki hata NEYİN
// yanlış olduğunu söyler.
func normalizeBudget(in CampaignInput) (
	budgetType models.CampaignBudgetType,
	limit *int64,
	currencyCode string,
	err error,
) {
	budgetType = in.BudgetType
	if budgetType == "" {
		budgetType = models.BudgetNone
	}
	if !budgetType.Valid() {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"kampanya bütçe türü tanımsız: %q", string(in.BudgetType))
	}

	if budgetType == models.BudgetNone {
		if in.BudgetLimit != nil {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"bütçesiz kampanyada bütçe sınırı verilemez; önce bütçe türü seçilmeli")
		}
		if in.BudgetCurrencyCode != "" {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"bütçesiz kampanyada bütçe para birimi verilemez")
		}
		return budgetType, nil, "", nil
	}

	if in.BudgetLimit == nil {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"%q bütçesi bir sınır ister; sınırsız bütçe için tür %q olmalı",
			string(budgetType), string(models.BudgetNone))
	}
	if *in.BudgetLimit < 0 {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"bütçe sınırı negatif olamaz, %d verildi", *in.BudgetLimit)
	}
	if *in.BudgetLimit > models.MaxAmount {
		return "", nil, "", errors.Invalid(CodeInvalidInput,
			"bütçe sınırı en fazla %d olabilir, %d verildi", models.MaxAmount, *in.BudgetLimit)
	}

	if budgetType == models.BudgetUsage {
		if in.BudgetCurrencyCode != "" {
			return "", nil, "", errors.Invalid(CodeInvalidInput,
				"adet ölçülü bütçede para birimi verilemez, %q verildi", in.BudgetCurrencyCode)
		}
		return budgetType, copyInt64(in.BudgetLimit), "", nil
	}

	currency, err := normalizeCurrency(in.BudgetCurrencyCode)
	if err != nil {
		return "", nil, "", err
	}
	return budgetType, copyInt64(in.BudgetLimit), currency, nil
}

// copyTime bir zaman işaretçisini UTC'ye çevirip KOPYALAYARAK döner.
func copyTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	out := t.UTC()
	return &out
}
