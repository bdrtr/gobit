package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// CompanyInput bir şirketin yazma girdisidir.
type CompanyInput struct {
	// Name şirketin ticari unvanıdır; zorunludur.
	Name string
	// Email şirketin iletişim adresidir; zorunludur, küçük harfe normalize
	// edilerek saklanır ve BENZERSİZ DEĞİLDİR.
	Email string
	// Phone şirketin telefonudur; boş bırakılabilir.
	Phone string
	// Address fatura adresinin sokak satırıdır; boş bırakılabilir.
	Address string
	// City şehirdir; boş bırakılabilir.
	City string
	// PostalCode posta kodudur; boş bırakılabilir.
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur; boş bırakılabilir.
	CountryCode string
	// CurrencyCode ISO 4217 para birimi kodudur; ZORUNLUDUR. Harcama
	// limitleri bu para biriminde ifade edilir.
	CurrencyCode string
	// SpendingLimitResetPeriod çalışan limitlerinin sıfırlanma aralığıdır;
	// boş bırakılırsa [models.ResetNever] uygulanır.
	SpendingLimitResetPeriod string
}

// CreateCompany yeni bir şirket oluşturur.
//
// E-posta benzersizliği ARANMAZ: bu modülde e-postayla bir kimlik kurulmaz ve
// aynı holdingin iki tüzel kişisi aynı muhasebe adresini paylaşabilir
// (gerekçe migration'daki tablo belgesindedir).
func (s *Service) CreateCompany(ctx context.Context, in CompanyInput) (models.Company, error) {
	company, err := s.validateCompanyInput(in)
	if err != nil {
		return models.Company{}, err
	}

	now := s.clock()
	company.ID = models.NewCompanyID(now)
	company.CreatedAt = now

	created, err := s.repo.CreateCompany(ctx, company)
	if err != nil {
		return models.Company{}, err
	}

	s.log.InfoContext(ctx, "şirket oluşturuldu",
		slog.String("company_id", created.ID),
		slog.String("currency_code", created.CurrencyCode),
	)
	return created, nil
}

// validateCompanyInput girdiyi doğrular ve saklanacak modele çevirir.
//
// Kimlik ve zaman alanları BURADA doldurulmaz: doğrulama saf kalır ve zaman
// kaynağına dokunmadığı için testte belirlenimcidir.
func (s *Service) validateCompanyInput(in CompanyInput) (models.Company, error) {
	if err := requireText("şirket adı", in.Name); err != nil {
		return models.Company{}, err
	}
	name := strings.TrimSpace(in.Name)
	if err := checkLen("şirket adı", name, models.MaxNameLen); err != nil {
		return models.Company{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Company{}, err
	}
	if err := checkLen("telefon", in.Phone, models.MaxPhoneLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("adres", in.Address, models.MaxAddressLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("şehir", in.City, models.MaxAddressLen); err != nil {
		return models.Company{}, err
	}
	if err := checkLen("posta kodu", in.PostalCode, models.MaxPostalCodeLen); err != nil {
		return models.Company{}, err
	}

	country, err := normalizeCountryCode(in.CountryCode)
	if err != nil {
		return models.Company{}, err
	}
	currency, err := normalizeCurrencyCode(in.CurrencyCode)
	if err != nil {
		return models.Company{}, err
	}
	period, err := normalizeResetPeriod(in.SpendingLimitResetPeriod)
	if err != nil {
		return models.Company{}, err
	}

	return models.Company{
		Name:                     name,
		Email:                    email,
		Phone:                    strings.TrimSpace(in.Phone),
		Address:                  strings.TrimSpace(in.Address),
		City:                     strings.TrimSpace(in.City),
		PostalCode:               strings.TrimSpace(in.PostalCode),
		CountryCode:              country,
		CurrencyCode:             currency,
		SpendingLimitResetPeriod: period,
	}, nil
}

// GetCompany kimliğe göre şirket döner; yoksa errors.NotFound.
func (s *Service) GetCompany(ctx context.Context, id string) (models.Company, error) {
	if err := requireID(id, models.CompanyIDPrefix, "şirket kimliği"); err != nil {
		return models.Company{}, err
	}
	return s.repo.GetCompany(ctx, id)
}

// ListCompaniesInput şirket listelemesinin girdisidir.
type ListCompaniesInput struct {
	// Email verilirse yalnızca bu e-postaya sahip şirketler döner; sonuç
	// BİRDEN ÇOK kayıt içerebilir.
	Email *string
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListCompanies şirketleri süzerek ve sayfalayarak listeler.
func (s *Service) ListCompanies(ctx context.Context, in ListCompaniesInput) (Page[models.Company], error) {
	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Company]{}, err
	}

	filter := models.CompanyFilter{}
	if in.Email != nil {
		// Süzgeç değeri de SAKLAMA biçimine çevrilir: sütunda küçük harfli
		// değer durur ve normalize edilmemiş bir süzgeç hiçbir satır bulmazdı.
		email := models.NormalizeEmail(*in.Email)
		filter.Email = &email
	}

	items, total, err := s.repo.ListCompanies(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.Company]{}, err
	}
	return Page[models.Company]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateCompanyInput bir şirketin kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir; adres alanlarında
// boş dize gerçek bir temizlemedir.
type UpdateCompanyInput struct {
	// Name yeni unvandır; verilirse boş olamaz.
	Name *string
	// Email yeni e-postadır; verilirse doğrulanır ve normalize edilir.
	Email *string
	// Phone yeni telefondur.
	Phone *string
	// Address adresin yeni sokak satırıdır.
	Address *string
	// City yeni şehirdir.
	City *string
	// PostalCode yeni posta kodudur.
	PostalCode *string
	// CountryCode yeni ülke kodudur; boş dize adresi temizler.
	CountryCode *string
	// CurrencyCode yeni para birimi kodudur; verilirse boş olamaz.
	CurrencyCode *string
	// SpendingLimitResetPeriod yeni sıfırlama aralığıdır.
	//
	// Değişmesi GEÇMİŞE dönük çalışır: yeni pencere, geçerli takvim ayının ya
	// da yılının başından itibaren sayılır (bkz. [models.SpendingResetPeriod]).
	// Bu bilinçlidir — alternatifi, değişiklik anını her çalışan için ayrıca
	// saklamak ve sıfırlamayı takvimden koparmaktı.
	SpendingLimitResetPeriod *string
}

// UpdateCompany şirketin verilen alanlarını günceller; yoksa errors.NotFound.
//
// Verilen alanlar oluşturmadakiyle AYNI doğrulamadan geçer: kısmi güncelleme
// bir alanı atlayabilir ama var olan bir zorunluluğu kaldıramaz.
func (s *Service) UpdateCompany(
	ctx context.Context,
	id string,
	in UpdateCompanyInput,
) (models.Company, error) {
	if err := requireID(id, models.CompanyIDPrefix, "şirket kimliği"); err != nil {
		return models.Company{}, err
	}

	patch, err := s.validateCompanyPatch(in)
	if err != nil {
		return models.Company{}, err
	}
	return s.repo.UpdateCompany(ctx, id, patch, s.clock())
}

// validateCompanyPatch kısmi güncelleme girdisini doğrulanmış bir yamaya
// çevirir.
func (s *Service) validateCompanyPatch(in UpdateCompanyInput) (models.CompanyPatch, error) {
	var patch models.CompanyPatch

	if in.Name != nil {
		if err := requireText("şirket adı", *in.Name); err != nil {
			return models.CompanyPatch{}, err
		}
		name := strings.TrimSpace(*in.Name)
		if err := checkLen("şirket adı", name, models.MaxNameLen); err != nil {
			return models.CompanyPatch{}, err
		}
		patch.Name = &name
	}
	if in.Email != nil {
		email, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.Email = &email
	}

	metinler := []struct {
		hedef  **string
		kaynak *string
		etiket string
		sinir  int
	}{
		{&patch.Phone, in.Phone, "telefon", models.MaxPhoneLen},
		{&patch.Address, in.Address, "adres", models.MaxAddressLen},
		{&patch.City, in.City, "şehir", models.MaxAddressLen},
		{&patch.PostalCode, in.PostalCode, "posta kodu", models.MaxPostalCodeLen},
	}
	for _, m := range metinler {
		if m.kaynak == nil {
			continue
		}
		value := strings.TrimSpace(*m.kaynak)
		if err := checkLen(m.etiket, value, m.sinir); err != nil {
			return models.CompanyPatch{}, err
		}
		*m.hedef = &value
	}

	if in.CountryCode != nil {
		country, err := normalizeCountryCode(*in.CountryCode)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.CountryCode = &country
	}
	if in.CurrencyCode != nil {
		currency, err := normalizeCurrencyCode(*in.CurrencyCode)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.CurrencyCode = &currency
	}
	if in.SpendingLimitResetPeriod != nil {
		// Boş dize burada "never" DEĞİLDİR: güncellemede boş bir değer, alanı
		// sessizce en kısıtlayıcı seçeneğe çekerdi ve istemci bunu istememiş
		// olabilir. Oluşturmadaki varsayılan ile güncellemedeki reddin farkı
		// budur.
		if err := requireText("harcama limiti sıfırlama periyodu", *in.SpendingLimitResetPeriod); err != nil {
			return models.CompanyPatch{}, err
		}
		period, err := normalizeResetPeriod(*in.SpendingLimitResetPeriod)
		if err != nil {
			return models.CompanyPatch{}, err
		}
		patch.SpendingLimitResetPeriod = &period
	}

	return patch, nil
}

// DeleteCompany şirketi ve ÇALIŞANLARINI yumuşak siler; yoksa errors.NotFound.
//
// # Karar: çalışanlar şirketle birlikte silinir
//
// Alternatif, çalışan kayıtlarını yerinde bırakmaktı ve o kayıtlar vitrinde
// SAHİPSİZ kalırdı: "kendi şirketim" sorusu artık okunamayan bir şirkete
// çözülür, müşteri ise hâlâ bir harcama limiti taşıyan bir kayıt görürdü —
// arkasında ödeme yapacak bir tüzel kişi olmadan. Bu yüzden değişmez şudur:
// canlı bir çalışan kaydı DAİMA canlı bir şirkete aittir.
//
// Müşteri bağları da kaldırılır ve bu, silmenin en kritik adımıdır: bağ tekil
// olduğu için sarkan bir satır, o müşterinin bir daha HİÇBİR şirkete çalışan
// olarak eklenememesi demektir. Bağların kaldırılması veritabanı işleminin
// DIŞINDADIR (link ayrı bir alt sistemdir); başarısız olursa hata dönülmez,
// uyarı loglanır — silme çoktan gerçekleşmiştir ve çağırana hata dönmek
// "şirket silinmedi" izlenimi verirdi.
func (s *Service) DeleteCompany(ctx context.Context, id string) error {
	if err := requireID(id, models.CompanyIDPrefix, "şirket kimliği"); err != nil {
		return err
	}

	employeeIDs, err := s.repo.DeleteCompany(ctx, id, s.clock())
	if err != nil {
		return err
	}
	s.unlinkCustomers(ctx, employeeIDs)

	s.log.InfoContext(ctx, "şirket silindi",
		slog.String("company_id", id),
		slog.Int("silinen_calisan", len(employeeIDs)),
	)
	return nil
}
