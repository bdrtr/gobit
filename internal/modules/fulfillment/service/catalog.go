package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// Bu dosya kargo KATALOĞUNUN (profil, seçenek, kural) yönetim akışlarıdır.
// Uygunluk hesabı eligibility.go'da, gönderiler fulfillment.go'dadır.

// CreateProfileInput yeni bir kargo profilinin girdisidir.
type CreateProfileInput struct {
	// Name profilin görünen adıdır; zorunludur ve yaşayan kayıtlar arasında
	// tektir.
	Name string
	// Type profilin türüdür; boş verilirse "default" uygulanır.
	Type string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateShippingProfile yeni bir kargo profili oluşturur.
//
// Aynı adla ikinci bir profil errors.Conflict döner: profil adı yöneticinin
// kuralı tanıdığı tek işarettir ve iki aynı adlı profil, hangisinin
// düzenlendiğini belirsiz bırakırdı.
func (s *Service) CreateShippingProfile(
	ctx context.Context,
	in CreateProfileInput,
) (models.ShippingProfile, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("profil adı", name); err != nil {
		return models.ShippingProfile{}, err
	}
	profileType, err := normalizeProfileType(in.Type)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	return s.store.CreateShippingProfile(ctx, models.ShippingProfile{
		ID:       models.NewShippingProfileID(),
		Name:     name,
		Type:     profileType,
		Metadata: in.Metadata,
	})
}

// GetShippingProfile profili kimliğiyle döner; yoksa errors.NotFound.
func (s *Service) GetShippingProfile(ctx context.Context, id string) (models.ShippingProfile, error) {
	if err := requireID(id, models.ShippingProfileIDPrefix, "kargo profili kimliği"); err != nil {
		return models.ShippingProfile{}, err
	}
	return s.store.GetShippingProfile(ctx, id)
}

// ListProfilesInput profil listelemesinin girdisidir.
type ListProfilesInput struct {
	// Type verilirse yalnızca o türdeki profiller döner.
	Type *string
	// Page sayfalama parametreleridir.
	Page Page
}

// ListShippingProfiles profilleri sayfalayarak döner.
// İkinci dönüş değeri süzgece uyan TÜM kayıtların sayısıdır.
func (s *Service) ListShippingProfiles(
	ctx context.Context,
	in ListProfilesInput,
) ([]models.ShippingProfile, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.Type != nil {
		if _, typeErr := normalizeProfileType(*in.Type); typeErr != nil {
			return nil, 0, typeErr
		}
	}

	return s.store.ListShippingProfiles(ctx, models.ProfileFilter{
		Type:   in.Type,
		Limit:  page.Limit,
		Offset: page.Offset,
	})
}

// UpdateProfileInput profil güncellemesinin girdisidir.
//
// İşaretçi alanlar "verilmedi" ile "boş verildi" ayrımını korur: nil bir Name
// alanın DEĞİŞMEYECEĞİ, boş dizeye işaret eden bir Name ise geçersiz bir ad
// verildiği anlamına gelir ve reddedilir.
type UpdateProfileInput struct {
	// Name verilirse profilin adı değiştirilir.
	Name *string
	// Type verilirse profilin türü değiştirilir.
	Type *string
	// Metadata verilirse üstveri YERİNE KONUR (birleştirilmez).
	Metadata map[string]any
}

// UpdateShippingProfile profilin verilen alanlarını günceller.
func (s *Service) UpdateShippingProfile(
	ctx context.Context,
	id string,
	in UpdateProfileInput,
) (models.ShippingProfile, error) {
	if err := requireID(id, models.ShippingProfileIDPrefix, "kargo profili kimliği"); err != nil {
		return models.ShippingProfile{}, err
	}

	current, err := s.store.GetShippingProfile(ctx, id)
	if err != nil {
		return models.ShippingProfile{}, err
	}

	next := current
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if nameErr := requireText("profil adı", name); nameErr != nil {
			return models.ShippingProfile{}, nameErr
		}
		next.Name = name
	}
	if in.Type != nil {
		profileType, typeErr := normalizeProfileType(*in.Type)
		if typeErr != nil {
			return models.ShippingProfile{}, typeErr
		}
		next.Type = profileType
	}
	if in.Metadata != nil {
		next.Metadata = in.Metadata
	}

	return s.store.UpdateShippingProfile(ctx, next)
}

// DeleteShippingProfile profili yumuşak siler.
//
// Seçeneği DURAN bir profil silinemez (errors.Conflict): silme sessizce
// geçseydi, o profile bağlı ürünlerin kargo kuralı ortadan kalkar ve müşteri
// hiçbir seçenek göremezdi. Yönetici önce seçenekleri kaldırmalıdır.
//
// Kontrol ve silme tek işlemde ve profil satırı KİLİTLİYKEN yapılır. Tek
// işlem tek başına yetmezdi: yumuşak silme anahtar olmayan bir sütunu
// güncellediği için yalnızca FOR NO KEY UPDATE alır ve o kilit, araya giren bir
// seçenek INSERT'ünün foreign key için aldığı FOR KEY SHARE ile ÇAKIŞMAZ —
// READ COMMITTED'de iki işlem birbirini beklemeden tamamlanır ve geriye
// silinmiş bir profile bağlı CANLI bir seçenek kalırdı (gerçek Postgres'te
// üretildi). [Store.LockShippingProfile]'ın FOR UPDATE kilidi, seçenek
// oluşturmanın aldığı paylaşımlı kilitle çakışarak iki yolu serileştirir
// (bkz. [Service.CreateShippingOption]).
func (s *Service) DeleteShippingProfile(ctx context.Context, id string) error {
	if err := requireID(id, models.ShippingProfileIDPrefix, "kargo profili kimliği"); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.store.LockShippingProfile(ctx, id); err != nil {
			return err
		}
		count, err := s.store.CountAliveOptionsByProfile(ctx, id)
		if err != nil {
			return err
		}
		if count > 0 {
			return errors.Conflict(CodeProfileInUse,
				"kargo profiline bağlı %d seçenek var; önce onlar kaldırılmalı (%s)", count, id)
		}
		return s.store.SoftDeleteShippingProfile(ctx, id)
	})
}

// CreateOptionInput yeni bir kargo seçeneğinin girdisidir.
type CreateOptionInput struct {
	// Name seçeneğin görünen adıdır; zorunludur.
	Name string
	// ProviderID seçeneği yürütecek sağlayıcıdır; zorunludur ve KAYITLI
	// olmalıdır.
	ProviderID string
	// ShippingProfileID seçeneğin bağlanacağı profildir; zorunludur.
	ShippingProfileID string
	// PriceType ücretin nereden geldiğini söyler; boş verilirse "flat".
	PriceType string
	// Amount yalnızca "flat" seçeneklerde anlamlıdır (minor unit).
	Amount int64
	// CurrencyCode ISO 4217 kodudur; zorunludur.
	CurrencyCode string
	// RegionID seçeneğin geçerli olduğu bölgedir; boş ise her bölge.
	RegionID string
	// IsReturn seçeneğin iade gönderisi için olduğunu bildirir.
	IsReturn bool
	// AdminOnly seçeneğin mağaza yüzeyine çıkmayacağını bildirir.
	AdminOnly bool
	// Data sağlayıcıya iletilecek yapılandırmadır.
	Data map[string]any
	// Metadata mağazanın serbest ek verisidir.
	Metadata map[string]any
}

// CreateShippingOption yeni bir kargo seçeneği oluşturur.
//
// Sağlayıcının KAYITLI olması şarttır: kaydedilmemiş bir sağlayıcıya bağlı
// seçenek, ancak müşteriye gösterileceği ya da gönderi açılacağı anda patlardı
// ve hata, kurulumdan çok sonra ortaya çıkardı.
//
// Profilin varlığı da burada doğrulanır. Aynı doğrulamayı foreign key de
// yapar ama sürücü hatasından üretilen mesaj hangi profilin arandığını
// söylemez.
//
// Profil okuması PAYLAŞIMLI KİLİT altında ve INSERT ile AYNI İŞLEMDE yapılır.
// Kilitsiz okunsaydı, aynı anda çalışan bir [Service.DeleteShippingProfile]
// profili "boş" görüp silebilir ve seçenek, silinmiş bir profile bağlı olarak
// yazılırdı. Kilidin paylaşımlı olması bilinçlidir: aynı profile paralel
// seçenek eklemeleri birbirini BEKLEMEZ, yalnızca silme yolu beklerdi.
func (s *Service) CreateShippingOption(
	ctx context.Context,
	in CreateOptionInput,
) (models.ShippingOption, error) {
	option, err := s.validateOptionInput(in)
	if err != nil {
		return models.ShippingOption{}, err
	}

	var out models.ShippingOption
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, lockErr := s.store.LockShippingProfileShared(ctx, option.ShippingProfileID); lockErr != nil {
			return lockErr
		}
		created, createErr := s.store.CreateShippingOption(ctx, option)
		if createErr != nil {
			return createErr
		}
		out = created
		return nil
	})
	if err != nil {
		return models.ShippingOption{}, err
	}
	return out, nil
}

// validateOptionInput seçenek girdisini doğrular ve kaydedilecek modeli üretir.
//
// Ayrı bir fonksiyondur çünkü doğrulama SAF'tır: veritabanına dokunmaz ve her
// dalı tek tek sınanabilir.
func (s *Service) validateOptionInput(in CreateOptionInput) (models.ShippingOption, error) {
	name := strings.TrimSpace(in.Name)
	if err := requireText("seçenek adı", name); err != nil {
		return models.ShippingOption{}, err
	}
	providerID := strings.TrimSpace(in.ProviderID)
	if err := requireText("sağlayıcı kimliği", providerID); err != nil {
		return models.ShippingOption{}, err
	}
	if !s.providers.Has(providerID) {
		return models.ShippingOption{}, errors.NotFound(CodeProviderNotFound,
			"%q kargo sağlayıcısı kayıtlı değil; kayıtlı olanlar: %s",
			providerID, strings.Join(s.providers.IDs(), ", "))
	}
	if err := requireID(in.ShippingProfileID, models.ShippingProfileIDPrefix,
		"kargo profili kimliği"); err != nil {
		return models.ShippingOption{}, err
	}
	priceType, err := normalizePriceType(in.PriceType)
	if err != nil {
		return models.ShippingOption{}, err
	}
	amount, err := amountFor(priceType, in.Amount)
	if err != nil {
		return models.ShippingOption{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.ShippingOption{}, err
	}
	regionID := strings.TrimSpace(in.RegionID)
	if err := checkTextLen("bölge kimliği", regionID); err != nil {
		return models.ShippingOption{}, err
	}

	return models.ShippingOption{
		ID:                models.NewShippingOptionID(),
		Name:              name,
		ProviderID:        providerID,
		ShippingProfileID: strings.TrimSpace(in.ShippingProfileID),
		PriceType:         priceType,
		Amount:            amount,
		CurrencyCode:      currency,
		RegionID:          regionID,
		IsReturn:          in.IsReturn,
		AdminOnly:         in.AdminOnly,
		Data:              in.Data,
		Metadata:          in.Metadata,
	}, nil
}

// amountFor fiyat türüne göre saklanacak tutarı doğrular.
//
// "calculated" seçenekte tutar SIFIR olmak zorundadır; sıfırdan farklı bir
// değer sessizce sıfırlanmaz, REDDEDİLİR. Sessiz sıfırlama, yöneticinin
// girdiği ücretin hiç uygulanmaması ve bunu ancak fatura ile görmesi demek
// olurdu.
func amountFor(priceType models.PriceType, amount int64) (int64, error) {
	if priceType == models.PriceCalculated {
		if amount != 0 {
			return 0, errors.Invalid(CodeInvalidInput,
				"hesaplanan kargo seçeneğinin tutarı sıfır olmalı; ücret sağlayıcıdan gelir (verilen: %d)",
				amount)
		}
		return 0, nil
	}
	if err := requireAmount("kargo tutarı", amount); err != nil {
		return 0, err
	}
	return amount, nil
}

// GetShippingOption seçeneği KURALLARIYLA birlikte döner; yoksa
// errors.NotFound.
func (s *Service) GetShippingOption(ctx context.Context, id string) (models.ShippingOption, error) {
	if err := requireID(id, models.ShippingOptionIDPrefix, "kargo seçeneği kimliği"); err != nil {
		return models.ShippingOption{}, err
	}

	option, err := s.store.GetShippingOption(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}
	rules, err := s.store.ListShippingOptionRules(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}
	option.Rules = rules
	return option, nil
}

// ListOptionsAdminInput yönetim listelemesinin girdisidir.
type ListOptionsAdminInput struct {
	// RegionID verilirse yalnızca o bölgeye ait seçenekler döner.
	RegionID *string
	// ProfileID verilirse yalnızca o profile bağlı seçenekler döner.
	ProfileID *string
	// ProviderID verilirse yalnızca o sağlayıcının seçenekleri döner.
	ProviderID *string
	// PriceType verilirse yalnızca o fiyat türündeki seçenekler döner.
	PriceType *string
	// Page sayfalama parametreleridir.
	Page Page
}

// ListShippingOptions seçenekleri sayfalayarak döner.
//
// Kurallar DOLDURULMAZ: liste yüzeyinde her seçeneğin kurallarını taşımak,
// sayfa başına ikinci bir sorgu ve büyüyen bir yanıt demektir. Kurallar
// [Service.GetShippingOption] ile okunur.
func (s *Service) ListShippingOptions(
	ctx context.Context,
	in ListOptionsAdminInput,
) ([]models.ShippingOption, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}
	if in.PriceType != nil {
		if _, typeErr := normalizePriceType(*in.PriceType); typeErr != nil {
			return nil, 0, typeErr
		}
	}

	return s.store.ListShippingOptions(ctx, models.OptionFilter{
		RegionID:   in.RegionID,
		ProfileID:  in.ProfileID,
		ProviderID: in.ProviderID,
		PriceType:  in.PriceType,
		Limit:      page.Limit,
		Offset:     page.Offset,
	})
}

// ListShippingOptionsByIDs verilen kimliklerin seçeneklerini TEK sorguda
// döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (s *Service) ListShippingOptionsByIDs(
	ctx context.Context,
	ids []string,
) ([]models.ShippingOption, error) {
	return s.store.ShippingOptionsByIDs(ctx, ids)
}

// UpdateOptionInput seçenek güncellemesinin girdisidir.
//
// ProviderID ve ShippingProfileID BURADA YOKTUR ve bu bilinçlidir: ikisi de
// seçeneğin kimliğine bağlı kararlardır ve değişmeleri, o seçenekle açılmış
// gönderilerin hangi sağlayıcıda olduğunu geçmişe dönük yanıltırdı. Değişmesi
// gerekiyorsa yeni bir seçenek açılır.
type UpdateOptionInput struct {
	// Name verilirse seçeneğin adı değiştirilir.
	Name *string
	// PriceType verilirse fiyat türü değiştirilir.
	PriceType *string
	// Amount verilirse tutar değiştirilir (minor unit).
	Amount *int64
	// RegionID verilirse bölge değiştirilir.
	RegionID *string
	// IsReturn verilirse iade işareti değiştirilir.
	IsReturn *bool
	// AdminOnly verilirse mağaza görünürlüğü değiştirilir.
	AdminOnly *bool
	// Data verilirse sağlayıcı yapılandırması YERİNE KONUR.
	Data map[string]any
	// Metadata verilirse üstveri YERİNE KONUR.
	Metadata map[string]any
}

// UpdateShippingOption seçeneğin verilen alanlarını günceller.
//
// Fiyat türü ile tutar BİRLİKTE doğrulanır: yalnızca türü "calculated"a
// çeviren bir istek, satırda duran eski sabit tutarı da sıfırlamalıdır; aksi
// hâlde şemadaki kısıt patlar ve istemci sebebini anlamayacağı bir hata alır.
func (s *Service) UpdateShippingOption(
	ctx context.Context,
	id string,
	in UpdateOptionInput,
) (models.ShippingOption, error) {
	if err := requireID(id, models.ShippingOptionIDPrefix, "kargo seçeneği kimliği"); err != nil {
		return models.ShippingOption{}, err
	}

	current, err := s.store.GetShippingOption(ctx, id)
	if err != nil {
		return models.ShippingOption{}, err
	}

	next := current
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if nameErr := requireText("seçenek adı", name); nameErr != nil {
			return models.ShippingOption{}, nameErr
		}
		next.Name = name
	}
	if in.PriceType != nil {
		priceType, typeErr := normalizePriceType(*in.PriceType)
		if typeErr != nil {
			return models.ShippingOption{}, typeErr
		}
		next.PriceType = priceType
	}
	if in.Amount != nil {
		next.Amount = *in.Amount
	} else if next.PriceType == models.PriceCalculated {
		// Tür "calculated"a çevrildi ama tutar verilmedi: satırdaki eski sabit
		// tutar artık anlamsızdır ve sıfırlanır. Bu sessiz bir kayıp değildir,
		// türün TANIMIDIR — hesaplanan seçenekte ücret sağlayıcıdan gelir.
		next.Amount = 0
	}
	amount, err := amountFor(next.PriceType, next.Amount)
	if err != nil {
		return models.ShippingOption{}, err
	}
	next.Amount = amount

	if in.RegionID != nil {
		regionID := strings.TrimSpace(*in.RegionID)
		if regionErr := checkTextLen("bölge kimliği", regionID); regionErr != nil {
			return models.ShippingOption{}, regionErr
		}
		next.RegionID = regionID
	}
	if in.IsReturn != nil {
		next.IsReturn = *in.IsReturn
	}
	if in.AdminOnly != nil {
		next.AdminOnly = *in.AdminOnly
	}
	if in.Data != nil {
		next.Data = in.Data
	}
	if in.Metadata != nil {
		next.Metadata = in.Metadata
	}

	return s.store.UpdateShippingOption(ctx, next)
}

// DeleteShippingOption seçeneği yumuşak siler.
//
// Silme YUMUŞAKTIR ve bu şart: seçeneğe bağlı gönderiler ON DELETE RESTRICT
// ile korunuyor, yani fiziksel silme geçmişi olan bir seçeneği hiç
// kaldıramazdı. Yumuşak silme seçeneği kataloğun dışına çıkarır, geçmiş
// gönderiler ise seçeneği okumaya devam eder.
func (s *Service) DeleteShippingOption(ctx context.Context, id string) error {
	if err := requireID(id, models.ShippingOptionIDPrefix, "kargo seçeneği kimliği"); err != nil {
		return err
	}
	return s.store.SoftDeleteShippingOption(ctx, id)
}

// CreateRuleInput yeni bir kargo seçeneği kuralının girdisidir.
type CreateRuleInput struct {
	// Attribute uygunluk bağlamında bakılacak alan adıdır; zorunludur.
	Attribute string
	// Operator karşılaştırma işlecidir; zorunludur.
	Operator string
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içermelidir.
	Values []string
}

// CreateShippingOptionRule bir seçeneğe kural ekler.
//
// Seçeneğin varlığı burada doğrulanır: foreign key de aynı şeyi yapar ama
// sürücü hatasından üretilen mesaj hangi seçeneğin arandığını söylemez.
func (s *Service) CreateShippingOptionRule(
	ctx context.Context,
	optionID string,
	in CreateRuleInput,
) (models.ShippingOptionRule, error) {
	if err := requireID(optionID, models.ShippingOptionIDPrefix, "kargo seçeneği kimliği"); err != nil {
		return models.ShippingOptionRule{}, err
	}
	operator, values, err := validateRuleInput(in.Attribute, in.Operator, in.Values)
	if err != nil {
		return models.ShippingOptionRule{}, err
	}

	if _, err := s.store.GetShippingOption(ctx, optionID); err != nil {
		return models.ShippingOptionRule{}, err
	}

	return s.store.CreateShippingOptionRule(ctx, models.ShippingOptionRule{
		ID:               models.NewShippingOptionRuleID(),
		ShippingOptionID: optionID,
		Attribute:        strings.TrimSpace(in.Attribute),
		Operator:         operator,
		Values:           values,
	})
}

// ListShippingOptionRules bir seçeneğin kurallarını döner.
func (s *Service) ListShippingOptionRules(
	ctx context.Context,
	optionID string,
) ([]models.ShippingOptionRule, error) {
	if err := requireID(optionID, models.ShippingOptionIDPrefix, "kargo seçeneği kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.store.GetShippingOption(ctx, optionID); err != nil {
		return nil, err
	}
	return s.store.ListShippingOptionRules(ctx, optionID)
}

// DeleteShippingOptionRule kuralı yumuşak siler.
func (s *Service) DeleteShippingOptionRule(ctx context.Context, ruleID string) error {
	if err := requireID(ruleID, models.ShippingOptionRuleIDPrefix,
		"kargo seçeneği kuralı kimliği"); err != nil {
		return err
	}
	return s.store.SoftDeleteShippingOptionRule(ctx, ruleID)
}
