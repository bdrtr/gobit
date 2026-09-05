package service

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// StoreRegion vitrine dönen bölge görünümüdür: bölge, para birimi ve ülkeleri.
//
// Üçü BİRLİKTE taşınır çünkü vitrin üçüne aynı anda ihtiyaç duyar: müşteri
// bölgeyi ülkesinden seçer, tutarları para biriminin sembolüyle ve ondalık
// basamağıyla görür. Ayrı uç noktalar olsaydı tek bir bölge seçim ekranı üç
// istek atardı.
//
// Vergi oranı ve otomatik vergi bayrağı BİLİNÇLİ OLARAK yoktur: ikisi de iş
// yapılandırmasıdır ve müşteriye gitmez; vergi, sepet toplamının içinde
// hesaplanmış olarak görünür.
type StoreRegion struct {
	// Region bölgenin kendisidir.
	Region models.Region
	// Currency bölgenin para birimidir; referans tablosunda bulunamazsa nil.
	//
	// nil dönmesi bilinçlidir: eksik bir para biriminin yerine sıfır değer
	// koymak, ondalık basamağı 0 göstererek tutarları yanlış ölçekte
	// gösterirdi. Foreign key nedeniyle bu durum normalde oluşamaz.
	Currency *models.Currency
	// Countries bölgeye bağlı ülkelerdir; yoksa boş dilim.
	Countries []models.Country
}

// ListStoreRegions vitrin için sayfalanmış bölge listesini para birimi ve
// ülkeleriyle birlikte döner.
//
// Sorgu sayısı bölge sayısından BAĞIMSIZDIR: bölgeler, para birimleri ve
// ülkeler üç toplu okumayla alınır. Bölge başına okuma yapmak N+1 demek olurdu
// ve vitrin listesi tam da en çok kaydın döndüğü yerdir.
func (s *Service) ListStoreRegions(ctx context.Context, limit, offset int32) (Page[StoreRegion], error) {
	if err := s.ready(); err != nil {
		return Page[StoreRegion]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[StoreRegion]{}, err
	}

	regions, total, err := s.repo.ListRegions(ctx, limit, offset)
	if err != nil {
		return Page[StoreRegion]{}, err
	}

	items, err := s.decorate(ctx, regions)
	if err != nil {
		return Page[StoreRegion]{}, err
	}
	return Page[StoreRegion]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// GetStoreRegion vitrin için tek bir bölgeyi para birimi ve ülkeleriyle döner.
func (s *Service) GetStoreRegion(ctx context.Context, id string) (StoreRegion, error) {
	region, err := s.GetRegion(ctx, id)
	if err != nil {
		return StoreRegion{}, err
	}

	items, err := s.decorate(ctx, []models.Region{region})
	if err != nil {
		return StoreRegion{}, err
	}
	if len(items) == 0 {
		// decorate girdi başına tam bir çıktı üretir; buraya düşmek
		// imkânsızdır ama sıfır değer dönmek sessiz bir hata olurdu.
		return StoreRegion{}, errors.Internal(CodeDecorateFailed,
			"bölge vitrin görünümüne çevrilemedi: %s", id)
	}
	return items[0], nil
}

// decorate bölgeleri para birimi ve ülkeleriyle TOPLU olarak zenginleştirir.
//
// İki toplu okuma yapar (para birimleri, ülkeler) ve girdi sırasını KORUR.
func (s *Service) decorate(ctx context.Context, regions []models.Region) ([]StoreRegion, error) {
	items := make([]StoreRegion, 0, len(regions))
	if len(regions) == 0 {
		return items, nil
	}

	codes := make([]string, 0, len(regions))
	ids := make([]string, 0, len(regions))
	for _, region := range regions {
		if !slices.Contains(codes, region.CurrencyCode) {
			codes = append(codes, region.CurrencyCode)
		}
		ids = append(ids, region.ID)
	}

	fetched, err := s.repo.GetCurrenciesByCodes(ctx, codes)
	if err != nil {
		return nil, err
	}
	currencies := make(map[string]models.Currency, len(fetched))
	for _, currency := range fetched {
		currencies[currency.Code] = currency
	}

	countries, err := s.repo.ListCountriesByRegions(ctx, ids)
	if err != nil {
		return nil, err
	}

	for _, region := range regions {
		item := StoreRegion{Region: region, Countries: countries[region.ID]}
		if item.Countries == nil {
			item.Countries = []models.Country{}
		}
		if currency, ok := currencies[region.CurrencyCode]; ok {
			item.Currency = &currency
		}
		items = append(items, item)
	}
	return items, nil
}
