// Package service inventory modülünün iş mantığıdır.
//
// Modülün sorumluluğu tek cümleyle: bir kalemin hangi lokasyonda kaç adet
// olduğunu ve bunun ne kadarının söz verilmiş (rezerve) olduğunu bilmek.
// Satılabilir adet (available) SAKLANMAZ, stocked - reserved olarak türetilir.
//
// # Eşzamanlılık
//
// Stok adedini değiştiren her akış tek bir veritabanı işlemi içinde koşar ve
// okumasını satır kilidi (SELECT ... FOR UPDATE) altında yapar. Bu, "önce oku
// sonra yaz" yarışını yapısal olarak imkânsız kılar: iki eşzamanlı rezervasyon
// aynı seviye satırını kilitlemek zorundadır, ikincisi birincinin işlemi
// bitene kadar bekler ve READ COMMITTED altında satırın GÜNCEL sürümünü okur.
// Son bir adet için yarışan iki çağrıdan tam olarak biri kazanır, diğeri
// errors.Conflict alır. Uygulama katmanında yapılan bir kontrol bunu
// sağlayamazdı; sınır veritabanındadır.
//
// # Modül izolasyonu
//
// Bu modül başka hiçbir modülü tanımaz. Bir stok kaleminin hangi ürün
// varyantına ait olduğu bilgisi burada YOKTUR; bağ product modülünün bildirdiği
// "product_variant_inventory" link'i ile kurulur (Prensip 2.2, ADR 0001).
// Rezervasyonun taşıdığı line_item_id de cart modülüne ait bir kimliktir ve
// foreign key DEĞİLDİR.
package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// EntityName modülün Query katmanına sunduğu entity adıdır. Sağlayıcı
// container'a "<EntityName>.query" adıyla kaydedilir (ADR 0004).
const EntityName = "inventory_item"

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "inventory_invalid_input"
	// CodeInsufficientStock istenen adedin satılabilir stoktan fazla olduğunu
	// bildirir. Rezervasyon yarışını kaybeden çağrı da bunu alır.
	CodeInsufficientStock = "inventory_insufficient_stock"
	// CodeReservationNotActive sonlanmış bir rezervasyon üzerinde geçersiz bir
	// geçiş denendiğini bildirir.
	CodeReservationNotActive = "inventory_reservation_not_active"
	// CodeItemHasReservations aktif rezervasyonu olan bir kalemin silinmek
	// istendiğini bildirir.
	CodeItemHasReservations = "inventory_item_has_reservations"
	// CodeInconsistentState rezerve adedi ile rezervasyon kayıtlarının
	// birbirini tutmadığını bildirir; normal işleyişte oluşmaz.
	CodeInconsistentState = "inventory_inconsistent_state"
)

// Sayfalama sınırları (plan Bölüm 8: limit/offset).
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// maxTextLen serbest metin alanları için üst sınırdır. Sınır, tek bir isteğin
// veritabanına sınırsız büyüklükte metin yazmasını engeller.
const maxTextLen = 512

// Service inventory modülünün dışa açık servisidir.
// Eşzamanlı kullanıma güvenlidir.
type Service struct {
	store Store
	log   *slog.Logger
}

// New verilen depo üzerinde çalışan bir servis üretir.
// log nil verilirse loglar atılır.
func New(store Store, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{store: store, log: log}
}

// Page liste isteklerinin sayfalama parametreleridir.
type Page struct {
	// Limit döndürülecek azami satır sayısıdır; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// normalize sayfalama parametrelerini doğrular ve varsayılanları uygular.
func (p Page) normalize() (Page, error) {
	if p.Limit < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "limit negatif olamaz: %d", p.Limit)
	}
	if p.Offset < 0 {
		return Page{}, errors.Invalid(CodeInvalidInput, "offset negatif olamaz: %d", p.Offset)
	}
	if p.Limit > MaxLimit {
		return Page{}, errors.Invalid(CodeInvalidInput,
			"limit en fazla %d olabilir: %d", MaxLimit, p.Limit)
	}
	if p.Limit == 0 {
		p.Limit = DefaultLimit
	}
	return p, nil
}

// CreateStockLocationInput yeni bir stok lokasyonunun alanlarıdır.
type CreateStockLocationInput struct {
	// Name lokasyonun görünen adıdır; zorunludur.
	Name string
	// Address1, Address2, City, Province, PostalCode konum bilgileridir.
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur; verilirse iki harf olmalıdır.
	CountryCode string
}

// CreateStockLocation yeni bir stok lokasyonu oluşturur.
func (s *Service) CreateStockLocation(ctx context.Context, in CreateStockLocationInput) (models.StockLocation, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("name", name); err != nil {
		return models.StockLocation{}, err
	}
	country := strings.ToUpper(strings.TrimSpace(in.CountryCode))
	if country != "" && len(country) != 2 {
		return models.StockLocation{}, errors.Invalid(CodeInvalidInput,
			"country_code iki harfli ISO 3166-1 alpha-2 kodu olmalı: %q", in.CountryCode)
	}
	// Sıra bilinçli olarak sabittir: map üzerinde dönmek, birden çok alan
	// birden uzun olduğunda hangi hatanın döneceğini rastgele bırakırdı.
	for _, field := range []struct{ label, value string }{
		{"address_1", in.Address1},
		{"address_2", in.Address2},
		{"city", in.City},
		{"province", in.Province},
		{"postal_code", in.PostalCode},
	} {
		if err := checkTextLen(field.label, field.value); err != nil {
			return models.StockLocation{}, err
		}
	}

	return s.store.CreateStockLocation(ctx, models.StockLocation{
		ID:          models.NewStockLocationID(),
		Name:        name,
		Address1:    strings.TrimSpace(in.Address1),
		Address2:    strings.TrimSpace(in.Address2),
		City:        strings.TrimSpace(in.City),
		Province:    strings.TrimSpace(in.Province),
		PostalCode:  strings.TrimSpace(in.PostalCode),
		CountryCode: country,
	})
}

// ListStockLocations stok lokasyonlarını sayfalayarak döner.
// İkinci dönüş değeri sayfaya değil, TÜM eşleşen satırlara ait sayıdır.
func (s *Service) ListStockLocations(ctx context.Context, page Page) ([]models.StockLocation, int64, error) {
	page, err := page.normalize()
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListStockLocations(ctx, page.Limit, page.Offset)
}

// GetStockLocation lokasyonu kimliğiyle döner.
func (s *Service) GetStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireText("id", id); err != nil {
		return models.StockLocation{}, err
	}
	return s.store.GetStockLocation(ctx, id)
}

// CreateInventoryItemInput yeni bir stok kaleminin alanlarıdır.
type CreateInventoryItemInput struct {
	// SKU stok takip kodudur; zorunludur ve yaşayan kalemler arasında tektir.
	SKU string
	// Title ve Description isteğe bağlıdır.
	Title       string
	Description string
	// RequiresShipping nil bırakılırsa true varsayılır. İşaretçi olmasının
	// sebebi budur: bool'un sıfır değeri "sevkiyat gerekmiyor" demek olurdu ve
	// alanı hiç göndermeyen bir istemci dijital ürün oluşturmuş sayılırdı.
	RequiresShipping *bool
}

// CreateInventoryItem yeni bir stok kalemi oluşturur.
// Aynı SKU yaşayan bir kalemde varsa errors.Conflict döner.
func (s *Service) CreateInventoryItem(ctx context.Context, in CreateInventoryItemInput) (models.InventoryItem, error) {
	sku := strings.TrimSpace(in.SKU)
	if err := requireText("sku", sku); err != nil {
		return models.InventoryItem{}, err
	}
	if err := checkTextLen("title", in.Title); err != nil {
		return models.InventoryItem{}, err
	}
	if err := checkTextLen("description", in.Description); err != nil {
		return models.InventoryItem{}, err
	}

	requiresShipping := true
	if in.RequiresShipping != nil {
		requiresShipping = *in.RequiresShipping
	}

	return s.store.CreateInventoryItem(ctx, models.InventoryItem{
		ID:               models.NewInventoryItemID(),
		SKU:              sku,
		Title:            strings.TrimSpace(in.Title),
		Description:      strings.TrimSpace(in.Description),
		RequiresShipping: requiresShipping,
	})
}

// GetInventoryItem kalemi kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error) {
	if err := requireText("id", id); err != nil {
		return models.InventoryItem{}, err
	}
	return s.store.GetInventoryItem(ctx, id)
}

// ListInventoryItemsInput kalem listelemesinin girdisidir.
type ListInventoryItemsInput struct {
	// SKU verilirse yalnızca o koda sahip kalem döner.
	SKU *string
	// RequiresShipping verilirse kalemler sevkiyat gerekliliğine göre süzülür.
	RequiresShipping *bool
	// Page sayfalama parametreleridir.
	Page Page
}

// ListInventoryItems kalemleri sayfalayarak döner.
// İkinci dönüş değeri filtreye uyan TÜM satırların sayısıdır.
func (s *Service) ListInventoryItems(ctx context.Context, in ListInventoryItemsInput) ([]models.InventoryItem, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	filter := models.InventoryItemFilter{
		RequiresShipping: in.RequiresShipping,
		Limit:            page.Limit,
		Offset:           page.Offset,
	}
	if in.SKU != nil {
		sku := strings.TrimSpace(*in.SKU)
		if err := requireText("sku", sku); err != nil {
			return nil, 0, err
		}
		filter.SKU = &sku
	}
	return s.store.ListInventoryItems(ctx, filter)
}

// ListInventoryItemsByIDs verilen kimliklerin kalemlerini TEK sorguda döner.
// Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (s *Service) ListInventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error) {
	if len(ids) == 0 {
		return []models.InventoryItem{}, nil
	}
	return s.store.InventoryItemsByIDs(ctx, ids)
}

// DeleteInventoryItem kalemi ve stok seviyelerini yumuşak siler.
//
// Kalemin AKTİF rezervasyonu varsa errors.Conflict döner: silme, söz verilmiş
// stoğu sessizce yok etmek anlamına gelirdi. Kontrol ve silme aynı işlemde ve
// kalem kilidi altında yapılır; araya giren bir rezervasyon kontrolü atlatamaz.
func (s *Service) DeleteInventoryItem(ctx context.Context, id string) error {
	if err := requireText("id", id); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItem(ctx, id); err != nil {
			return err
		}
		active, err := s.store.CountActiveReservations(ctx, id)
		if err != nil {
			return err
		}
		if active > 0 {
			return errors.Conflict(CodeItemHasReservations,
				"kalem silinemez: %d aktif rezervasyonu var (%s)", active, id)
		}
		if err := s.store.SoftDeleteInventoryLevelsByItem(ctx, id); err != nil {
			return err
		}
		return s.store.SoftDeleteInventoryItem(ctx, id)
	})
}

// requireText zorunlu bir metin alanını doğrular.
func requireText(label, value string) error {
	if value == "" {
		return errors.Invalid(CodeInvalidInput, "%s boş olamaz", label)
	}
	return checkTextLen(label, value)
}

// checkTextLen metin alanının uzunluk sınırını doğrular.
func checkTextLen(label, value string) error {
	if len(value) > maxTextLen {
		return errors.Invalid(CodeInvalidInput,
			"%s en fazla %d bayt olabilir: %d", label, maxTextLen, len(value))
	}
	return nil
}
