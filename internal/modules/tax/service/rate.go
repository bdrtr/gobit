package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// CreateTaxRateInput yeni bir vergi oranının yazma girdisidir.
type CreateTaxRateInput struct {
	// TaxRegionID oranın ekleneceği bölgedir; zorunludur.
	TaxRegionID string
	// Name oranın görünen adıdır (örn. "KDV"); zorunludur.
	Name string
	// Code dış sistemlerle mutabakat kodudur; boş bırakılabilir.
	Code string
	// RateBps orandır (baz puan; 2000 = %20).
	RateBps int32
	// IsDefault bölgenin varsayılan oranı olup olmadığıdır.
	IsDefault bool
	// Metadata serbest üstveridir.
	Metadata map[string]any
}

// CreateTaxRate bir bölgeye vergi oranı ekler.
//
// # İkinci varsayılan oran
//
// Reddedilir (errors.Conflict, kod [CodeDefaultExists]). Servis bunu önce
// okuyarak denetler; son savunma veritabanındaki kısmi benzersiz indekstir
// (tax_rate_default_uniq). İki eşzamanlı istek "önce oku, sonra yaz"
// denetimini birlikte geçebilir ve o andan sonra hangi oranın uygulandığı satır
// sırasına kalırdı.
//
// # Bölge yoksa
//
// errors.NotFound döner ve hiçbir satır yazılmaz. Denetim burada okunabilir bir
// hatayla, veritabanında ise foreign key ile iki kez yapılır; ikincisi yalnızca
// doğrudan SQL ile yapılan müdahaleyi kapsar.
//
// # Denetim ile yazma AYNI işlemdedir
//
// Bölge denetimi ile oranın yazılması tek bir işlemde koşar ve bölge satırı
// PAYLAŞIMLI kilitle okunur (Repository.LockTaxRegion). Bu çerçeve eklenmeden
// önceki durum ÖLÇÜLDÜ: iki çağrı ayrı ayrı otomatik commit'lenen ifadelerdi ve
// aralarındaki boşluğa giren bir [Service.DeleteTaxRegion] denetimden sonra
// tamamlanıyor, oran yine de yazılıyordu. Foreign key bunu YAKALAMAZ — silme
// YUMUŞAKTIR, bölge satırı yerinde durur — ve geriye silinmiş bir bölgeye bağlı
// CANLI bir oran kalıyordu. O oran hiçbir hesaba girmez ama defterde durur;
// repository.DeleteTaxRegion'ın işlemi tam olarak bu satırın oluşmaması için
// vardır ve servis tarafındaki boşluk onu atlıyordu.
//
// Varsayılan oran denetimi ([Service.assertNoDefaultRate]) de işlemin
// İÇİNDEDİR ama tekilliği o SAĞLAMAZ: paylaşımlı kilit iki eşzamanlı oran
// eklemeyi birbirinden ayırmaz (ayırması da istenmez) ve iki istek denetimi
// birlikte geçebilir. Son savunma yine kısmi benzersiz indekstir; denetimin
// buradaki işi, yarışın kaybedeni değil, sıradan çağıran için okunabilir bir
// hata üretmektir.
func (s *Service) CreateTaxRate(ctx context.Context, in CreateTaxRateInput) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(in.TaxRegionID, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return models.TaxRate{}, err
	}

	name, err := normalizeName(in.Name)
	if err != nil {
		return models.TaxRate{}, err
	}
	code, err := normalizeCode(in.Code)
	if err != nil {
		return models.TaxRate{}, err
	}
	if err := validateRateBps(in.RateBps); err != nil {
		return models.TaxRate{}, err
	}

	var created models.TaxRate
	txErr := s.repo.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.repo.LockTaxRegion(ctx, in.TaxRegionID); err != nil {
			return err
		}
		if in.IsDefault {
			if err := s.assertNoDefaultRate(ctx, in.TaxRegionID); err != nil {
				return err
			}
		}

		now := s.clock()
		rate := models.TaxRate{
			ID:          models.NewTaxRateID(now),
			TaxRegionID: in.TaxRegionID,
			Name:        name,
			RateBps:     in.RateBps,
			IsDefault:   in.IsDefault,
			Metadata:    in.Metadata,
		}
		if code != "" {
			rate.Code = &code
		}

		var err error
		created, err = s.repo.CreateTaxRate(ctx, rate, now)
		return err
	})
	if txErr != nil {
		return models.TaxRate{}, txErr
	}
	return created, nil
}

// assertNoDefaultRate bölgenin henüz varsayılan oranı olmadığını doğrular.
func (s *Service) assertNoDefaultRate(ctx context.Context, regionID string) error {
	existing, err := s.repo.ListTaxRates(ctx, regionID)
	if err != nil {
		return err
	}
	for i := range existing {
		if existing[i].IsDefault {
			return errors.Conflict(CodeDefaultExists,
				"%s bölgesinin varsayılan oranı zaten var: %s", regionID, existing[i].ID)
		}
	}
	return nil
}

// GetTaxRate kimliğe göre oranı döner; yoksa errors.NotFound.
func (s *Service) GetTaxRate(ctx context.Context, id string) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "vergi oranı kimliği"); err != nil {
		return models.TaxRate{}, err
	}
	return s.repo.GetTaxRate(ctx, id)
}

// ListTaxRates bir bölgenin oranlarını döner; varsayılan oran BAŞTADIR.
//
// Sayfalama YOKTUR ve bilinçlidir: bir bölgedeki oran sayısı yönetilebilir bir
// listedir (standart, indirimli, muaf …) ve tamamı tek yanıtta görünmelidir.
// Sayfalama, yöneticinin ikinci sayfadaki bir oranı gözden kaçırması demekti.
func (s *Service) ListTaxRates(ctx context.Context, regionID string) ([]models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(regionID, models.TaxRegionIDPrefix, "tax region id"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTaxRegion(ctx, regionID); err != nil {
		return nil, err
	}
	return s.repo.ListTaxRates(ctx, regionID)
}

// UpdateTaxRateInput bir oranın KISMİ güncelleme girdisidir.
//
// nil alan "dokunma" demektir. Tam gövde istenseydi, gövdesinde rate_bps
// göndermeyi unutan bir istemci oranı sessizce sıfırlardı.
type UpdateTaxRateInput struct {
	// Name yeni addır; nil ise ad değişmez.
	Name *string
	// Code yeni mutabakat kodudur; nil ise kod değişmez. Kodu KALDIRMAK için
	// boş dizeye işaret eden bir işaretçi verilir.
	Code *string
	// RateBps yeni orandır (baz puan); nil ise oran değişmez.
	RateBps *int32
	// IsDefault varsayılanlık bayrağıdır; nil ise değişmez.
	IsDefault *bool
	// Metadata yeni üstveridir; nil ise üstveri değişmez.
	Metadata map[string]any
}

// UpdateTaxRate oranın verilen alanlarını günceller.
//
// Hiçbir alan verilmezse errors.Invalid döner: boş bir yama, istemcinin
// gönderdiğini sandığı alanın adını yanlış yazdığının en olası göstergesidir
// ve sessizce başarılı dönmek o hatayı gizlerdi.
//
// Bir oranı VARSAYILAN yapmak iki ek koşula bağlıdır ve ikisi de depo
// katmanında, satır KİLİDİ ALTINDA denetlenir: bölgede başka bir varsayılan
// oran olmamalı (kısmi benzersiz indeks) ve oranın hiç kuralı olmamalıdır.
// Denetimlerin kilit altında olması şarttır — araya giren bir kural ekleme,
// aksi hâlde kurallı bir oranı varsayılan yapabilirdi.
func (s *Service) UpdateTaxRate(ctx context.Context, id string, in UpdateTaxRateInput) (models.TaxRate, error) {
	if err := s.ready(); err != nil {
		return models.TaxRate{}, err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "vergi oranı kimliği"); err != nil {
		return models.TaxRate{}, err
	}

	patch, err := buildRatePatch(in)
	if err != nil {
		return models.TaxRate{}, err
	}
	if patch.Empty() {
		return models.TaxRate{}, errors.Invalid(CodeInvalidInput, "no field was given to update")
	}
	return s.repo.UpdateTaxRate(ctx, id, patch, s.clock())
}

// buildRatePatch güncelleme girdisini doğrular ve yamaya çevirir.
//
// Doğrulama yalnızca DOLU alanlara uygulanır: dokunulmayan bir alanın mevcut
// değeri, bugün geçerli olmayan bir kuralı ihlal etse bile güncellemeyi
// düşürmemelidir.
func buildRatePatch(in UpdateTaxRateInput) (models.TaxRatePatch, error) {
	var patch models.TaxRatePatch

	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return models.TaxRatePatch{}, err
		}
		patch.Name = &name
	}
	if in.Code != nil {
		code, err := normalizeCode(*in.Code)
		if err != nil {
			return models.TaxRatePatch{}, err
		}
		patch.Code = &code
	}
	if in.RateBps != nil {
		if err := validateRateBps(*in.RateBps); err != nil {
			return models.TaxRatePatch{}, err
		}
		rate := *in.RateBps
		patch.RateBps = &rate
	}
	if in.IsDefault != nil {
		isDefault := *in.IsDefault
		patch.IsDefault = &isDefault
	}
	patch.Metadata = in.Metadata
	return patch, nil
}

// DeleteTaxRate oranı ve kurallarını yumuşak siler; yoksa errors.NotFound.
func (s *Service) DeleteTaxRate(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRateIDPrefix, "vergi oranı kimliği"); err != nil {
		return err
	}
	return s.repo.DeleteTaxRate(ctx, id, s.clock())
}
