package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// AddCountryToRegion bir ülkeyi bölgeye ekler.
//
// Ülke başka bir bölgeye aitse errors.Conflict döner: bir ülke EN FAZLA bir
// bölgeye ait olabilir. Kural veritabanı tarafında satır kilidiyle korunur
// (bkz. repository.AssignCountry), yani eşzamanlı iki istekte de tutar.
//
// Ülke zaten AYNI bölgedeyse çağrı başarılıdır ve mevcut kayıt döner;
// tekrarlanan bir yönetim isteği hata üretmez.
func (s *Service) AddCountryToRegion(ctx context.Context, regionID, countryCode string) (models.Country, error) {
	if err := s.ready(); err != nil {
		return models.Country{}, err
	}
	if err := requireRegionID(regionID); err != nil {
		return models.Country{}, err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return models.Country{}, err
	}

	country, err := s.repo.AssignCountry(ctx, regionID, code, s.clock())
	if err != nil {
		return models.Country{}, err
	}

	s.log.DebugContext(ctx, "ülke bölgeye eklendi",
		slog.String("region_id", regionID),
		slog.String("country_code", code),
	)
	return country, nil
}

// RemoveCountryFromRegion bir ülkeyi bölgeden çıkarır.
//
// Ülke o bölgeye ait değilse errors.NotFound döner; silme isteğinin hedefi
// "bölgedeki ülke" kaydıdır ve o kayıt yoktur.
func (s *Service) RemoveCountryFromRegion(ctx context.Context, regionID, countryCode string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireRegionID(regionID); err != nil {
		return err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return err
	}

	if err := s.repo.UnassignCountry(ctx, regionID, code, s.clock()); err != nil {
		return err
	}

	s.log.DebugContext(ctx, "ülke bölgeden çıkarıldı",
		slog.String("region_id", regionID),
		slog.String("country_code", code),
	)
	return nil
}

// ListCountriesInput ülke listeleme girdisidir.
type ListCountriesInput struct {
	// RegionID dolu ise yalnızca o bölgenin ülkeleri döner; nil ise tümü.
	RegionID *string
	// Limit sayfa boyudur; 0 verilirse [DefaultLimit] uygulanır.
	Limit int32
	// Offset atlanacak kayıt sayısıdır.
	Offset int32
}

// ListCountries sayfalanmış ülke listesini döner.
//
// Ülke listesi REFERANS VERİDİR ve tohum ile yüklenir; burada yalnızca okuma
// vardır. Bölge süzgeci verilirse kimliği önce doğrulanır: doğrulanmasaydı
// yanlış türde bir kimlik boş bir liste döndürür ve istemci bölgenin ülkesi
// olmadığını sanırdı.
func (s *Service) ListCountries(ctx context.Context, in ListCountriesInput) (Page[models.Country], error) {
	if err := s.ready(); err != nil {
		return Page[models.Country]{}, err
	}
	if in.RegionID != nil {
		if err := requireRegionID(*in.RegionID); err != nil {
			return Page[models.Country]{}, err
		}
	}
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Country]{}, err
	}

	countries, total, err := s.repo.ListCountries(ctx, in.RegionID, limit, offset)
	if err != nil {
		return Page[models.Country]{}, err
	}
	return Page[models.Country]{Items: countries, Count: total, Limit: limit, Offset: offset}, nil
}

// ResolveRegionForCountry ülke kodundan bölgeyi çözer.
//
// Sepet oluşturulurken kullanılan yoldur: sepetin para birimi ve vergi bölgesi
// müşterinin ülkesinden bulunur. Mutlu yol TEK sorgudur.
//
// Bulunamama hâlinde üç ayrı durum vardır ve üçü de errors.NotFound döner ama
// KODLARI farklıdır; çağıran hangi düzeltmenin gerektiğini kodundan bilir:
//
//   - Ülke tanımsız (repository.CodeCountryNotFound) — istemci geçerli bir ISO
//     kodu göndermemiştir.
//   - Ülke hiçbir bölgeye bağlı değil ([CodeCountryUnassigned]) — operatör o
//     ülkeye satış açmamıştır.
//   - Ülke bağlı ama bölgesi yok ([CodeCountryRegionMissing]) — veri
//     tutarsızlığıdır; normalde oluşmaz, çünkü bölge silinirken ülkeleri
//     serbest bırakılır.
//
// Ayrım YALNIZCA hata yolunda ikinci bir sorguyla yapılır; mutlu yol tek
// sorgu kalır.
func (s *Service) ResolveRegionForCountry(ctx context.Context, countryCode string) (models.Region, error) {
	if err := s.ready(); err != nil {
		return models.Region{}, err
	}
	code, err := NormalizeCountryCode(countryCode)
	if err != nil {
		return models.Region{}, err
	}

	region, err := s.repo.GetRegionByCountry(ctx, code)
	if err == nil {
		return region, nil
	}
	if !errors.IsNotFound(err) {
		return models.Region{}, err
	}

	country, lookupErr := s.repo.GetCountry(ctx, code)
	if lookupErr != nil {
		// Ülkenin kendisi de yoksa ilk hata değil BU hata anlamlıdır:
		// "ülke bulunamadı" istemcinin düzeltebileceği tek bilgidir.
		return models.Region{}, lookupErr
	}
	if country.RegionID != nil {
		return models.Region{}, errors.NotFound(CodeCountryRegionMissing,
			"%s ülkesi %s bölgesine bağlı ama bölge bulunamadı", code, *country.RegionID)
	}
	return models.Region{}, errors.NotFound(CodeCountryUnassigned,
		"%s ülkesi hiçbir bölgeye bağlı değil", code)
}
