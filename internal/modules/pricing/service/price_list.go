package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// PriceListInput bir fiyat listesinin yazma girdisidir.
type PriceListInput struct {
	// Title listenin görünen adıdır; zorunludur.
	Title string
	// Description isteğe bağlı açıklamadır.
	Description string
	// Type listenin türüdür (sale | override); zorunludur.
	Type models.PriceListType
	// Status listenin durumudur; boş bırakılırsa draft kabul edilir.
	//
	// Varsayılanın draft olması bilinçlidir: yanlışlıkla eksik gönderilen bir
	// durum, kampanyayı istemeden YAYINA almamalıdır.
	Status models.PriceListStatus
	// StartsAt geçerlilik penceresinin başıdır; nil ise alt sınır yoktur.
	StartsAt *time.Time
	// EndsAt geçerlilik penceresinin sonudur; nil ise üst sınır yoktur.
	EndsAt *time.Time
}

// CreatePriceList yeni bir fiyat listesi oluşturur.
func (s *Service) CreatePriceList(ctx context.Context, in PriceListInput) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}

	list, err := buildPriceList(in)
	if err != nil {
		return models.PriceList{}, err
	}

	now := s.clock()
	list.ID = models.NewPriceListID(now)
	return s.repo.CreatePriceList(ctx, list, now)
}

// GetPriceList kimliğe göre listeyi döner; yoksa errors.NotFound.
func (s *Service) GetPriceList(ctx context.Context, id string) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "fiyat listesi kimliği"); err != nil {
		return models.PriceList{}, err
	}
	return s.repo.GetPriceList(ctx, id)
}

// ListPriceLists sayfalanmış fiyat listesi kümesini döner.
func (s *Service) ListPriceLists(ctx context.Context, limit, offset int32) (Page[models.PriceList], error) {
	if err := s.ready(); err != nil {
		return Page[models.PriceList]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.PriceList]{}, err
	}

	lists, total, err := s.repo.ListPriceLists(ctx, limit, offset)
	if err != nil {
		return Page[models.PriceList]{}, err
	}
	return Page[models.PriceList]{Items: lists, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdatePriceList listenin tüm güncellenebilir alanlarını yazar.
//
// Kısmi güncelleme DEĞİLDİR: verilmeyen alanlar sıfırlanır. Bu bilinçlidir —
// tarih penceresinin bir ucunu "değiştirme" ile "kaldır" arasındaki farkı
// kısmi güncellemede ayırt etmek mümkün olmazdı.
func (s *Service) UpdatePriceList(ctx context.Context, id string, in PriceListInput) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "fiyat listesi kimliği"); err != nil {
		return models.PriceList{}, err
	}

	list, err := buildPriceList(in)
	if err != nil {
		return models.PriceList{}, err
	}

	list.ID = id
	return s.repo.UpdatePriceList(ctx, list, s.clock())
}

// DeletePriceList listeyi soft delete ile siler.
//
// Listeye bağlı fiyatlar silinmez ama hesaplamada elenir; gerekçe için bkz.
// repository.Repo.DeletePriceList.
func (s *Service) DeletePriceList(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceListIDPrefix, "fiyat listesi kimliği"); err != nil {
		return err
	}
	return s.repo.DeletePriceList(ctx, id, s.clock())
}

// CreatePriceRule var olan bir fiyata kural ekler.
func (s *Service) CreatePriceRule(ctx context.Context, priceID string, in RuleInput) (models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return models.PriceRule{}, err
	}
	if err := requireID(priceID, models.PriceIDPrefix, "fiyat kimliği"); err != nil {
		return models.PriceRule{}, err
	}

	now := s.clock()
	rule, err := buildRule(priceID, in, now)
	if err != nil {
		return models.PriceRule{}, err
	}
	return s.repo.CreatePriceRule(ctx, rule, now)
}

// ListPriceRules bir fiyatın kurallarını döner.
func (s *Service) ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(priceID, models.PriceIDPrefix, "fiyat kimliği"); err != nil {
		return nil, err
	}
	// Fiyatın varlığı doğrulanır: olmayan bir fiyatın kuralları boş dilim
	// olarak dönerse istemci 404 yerine "kuralı yok" sanırdı.
	if _, err := s.repo.GetPrice(ctx, priceID); err != nil {
		return nil, err
	}
	return s.repo.ListPriceRules(ctx, priceID)
}

// GetPriceRule kimliğe göre kuralı döner; yoksa errors.NotFound.
func (s *Service) GetPriceRule(ctx context.Context, id string) (models.PriceRule, error) {
	if err := s.ready(); err != nil {
		return models.PriceRule{}, err
	}
	if err := requireID(id, models.PriceRuleIDPrefix, "fiyat kuralı kimliği"); err != nil {
		return models.PriceRule{}, err
	}
	return s.repo.GetPriceRule(ctx, id)
}

// DeletePriceRule kuralı soft delete ile siler.
func (s *Service) DeletePriceRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.PriceRuleIDPrefix, "fiyat kuralı kimliği"); err != nil {
		return err
	}
	return s.repo.DeletePriceRule(ctx, id, s.clock())
}

// buildPriceList girdiyi doğrular ve domain modeline çevirir.
func buildPriceList(in PriceListInput) (models.PriceList, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput, "fiyat listesi başlığı boş olamaz")
	}
	if !in.Type.Valid() {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"fiyat listesi türü tanımsız: %q (beklenen: %s, %s)",
			string(in.Type), models.PriceListSale, models.PriceListOverride)
	}

	status := in.Status
	if status == "" {
		status = models.PriceListDraft
	}
	if !status.Valid() {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"fiyat listesi durumu tanımsız: %q (beklenen: %s, %s, %s)",
			string(in.Status), models.PriceListDraft, models.PriceListActive, models.PriceListExpired)
	}

	starts, ends := normalizeWindow(in.StartsAt, in.EndsAt)
	if starts != nil && ends != nil && !starts.Before(*ends) {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"fiyat listesi başlangıcı (%s) bitişinden (%s) önce olmalı",
			starts.Format(time.RFC3339), ends.Format(time.RFC3339))
	}

	return models.PriceList{
		Title:       title,
		Description: strings.TrimSpace(in.Description),
		Type:        in.Type,
		Status:      status,
		StartsAt:    starts,
		EndsAt:      ends,
	}, nil
}

// normalizeWindow pencere uçlarını UTC'ye çevirir ve KOPYALAR;
// çağıranın işaretçileri paylaşılmaz.
func normalizeWindow(starts, ends *time.Time) (utcStart, utcEnd *time.Time) {
	var outStart, outEnd *time.Time
	if starts != nil {
		utc := starts.UTC()
		outStart = &utc
	}
	if ends != nil {
		utc := ends.UTC()
		outEnd = &utc
	}
	return outStart, outEnd
}
