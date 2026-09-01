package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// sabitSaat testlerin belirlenimci zaman kaynağıdır. Ayın ORTASI seçilmiştir:
// aylık pencerenin başlangıcı böyle "şimdi" ile karışmaz.
var sabitSaat = time.Date(2026, time.March, 17, 9, 30, 0, 0, time.UTC)

// yeniServis sahte depo ve sahte bağ servisiyle bir servis kurar.
func yeniServis(t *testing.T) (*Service, *memRepo, *memLinker) {
	t.Helper()

	repo := newMemRepo()
	links := newMemLinker()
	svc, err := New(Options{
		Repo:  repo,
		Links: links,
		Now:   func() time.Time { return sabitSaat },
	})
	require.NoError(t, err)
	return svc, repo, links
}

// gecerliSirket testlerin varsayılan şirket girdisidir.
func gecerliSirket() CompanyInput {
	return CompanyInput{
		Name:                     "Acme Sanayi A.Ş.",
		Email:                    "Muhasebe@Acme.example",
		CurrencyCode:             "try",
		SpendingLimitResetPeriod: string(models.ResetMonthly),
	}
}

// yeniSirket testler için bir şirket oluşturur.
func yeniSirket(t *testing.T, svc *Service) models.Company {
	t.Helper()

	company, err := svc.CreateCompany(t.Context(), gecerliSirket())
	require.NoError(t, err)
	return company
}

// TestServisBagimlilikOlmadanKurulamaz eksik bağımlılığın KURULUMDA
// yakalandığını doğrular.
//
// Çalışma zamanına ertelenseydi modül açılır, çalışan kayıtları yazılır ve
// hiçbiri bir müşteriye bağlanmazdı; eksiklik ancak vitrinde görünürdü.
func TestServisBagimlilikOlmadanKurulamaz(t *testing.T) {
	_, err := New(Options{Links: newMemLinker()})
	assert.True(t, errors.HasKind(err, errors.KindInternal), "depo olmadan kurulmamalı")

	_, err = New(Options{Repo: newMemRepo()})
	assert.True(t, errors.HasKind(err, errors.KindInternal), "link servisi olmadan kurulmamalı")
}

// TestSirketOlusturmaGirdiyiDogrular servis katmanındaki doğrulamanın
// veritabanına gitmeden çalıştığını gösterir.
func TestSirketOlusturmaGirdiyiDogrular(t *testing.T) {
	durumlar := map[string]func(in *CompanyInput){
		"ad boş":                 func(in *CompanyInput) { in.Name = "   " },
		"e-posta boş":            func(in *CompanyInput) { in.Email = "" },
		"e-posta biçimsiz":       func(in *CompanyInput) { in.Email = "muhasebe@acme" },
		"para birimi boş":        func(in *CompanyInput) { in.CurrencyCode = "" },
		"para birimi kısa":       func(in *CompanyInput) { in.CurrencyCode = "TR" },
		"para birimi harf değil": func(in *CompanyInput) { in.CurrencyCode = "TR1" },
		"ülke kodu geçersiz":     func(in *CompanyInput) { in.CountryCode = "TUR" },
		"periyot tanımsız":       func(in *CompanyInput) { in.SpendingLimitResetPeriod = "weekly" },
	}

	for ad, boz := range durumlar {
		t.Run(ad, func(t *testing.T) {
			svc, repo, _ := yeniServis(t)

			in := gecerliSirket()
			boz(&in)

			_, err := svc.CreateCompany(t.Context(), in)
			assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
			assert.Zero(t, repo.calls["CreateCompany"], "geçersiz girdi depoya hiç gitmemeli")
		})
	}
}

// TestSirketOlusturmaNormalizeEder e-postanın küçük, kodların BÜYÜK harfe
// çevrildiğini ve periyodun varsayılanını doğrular.
//
// Normalizasyon SAKLAMADA yapılır: süzgeç sütundaki değerle karşılaştırır ve
// iki farklı yazım tabloya girerse süzgeç ikisini farklı sanardı.
func TestSirketOlusturmaNormalizeEder(t *testing.T) {
	svc, _, _ := yeniServis(t)

	in := gecerliSirket()
	in.CountryCode = "tr"
	in.SpendingLimitResetPeriod = ""

	company, err := svc.CreateCompany(t.Context(), in)
	require.NoError(t, err)

	assert.Equal(t, "muhasebe@acme.example", company.Email)
	assert.Equal(t, "TRY", company.CurrencyCode)
	assert.Equal(t, "TR", company.CountryCode)
	assert.Equal(t, models.ResetNever, company.SpendingLimitResetPeriod,
		"periyot verilmezse en kısıtlayıcı seçenek uygulanmalı")
	assert.Equal(t, sabitSaat, company.CreatedAt)
}

// TestCalisanEklemeMusteriBaginiKurar bağın kurulduğunu ve kaydın müşteri
// kimliğiyle döndüğünü doğrular.
//
// Kimliğin dönmesi önemlidir: sütunu olmadığı için değer yalnızca link'ten
// gelebilir ve boş dönmesi, bağın hiç kurulmadığının işareti olurdu.
func TestCalisanEklemeMusteriBaginiKurar(t *testing.T) {
	svc, _, links := yeniServis(t)
	company := yeniSirket(t, svc)

	limit := int64(150000)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:      company.ID,
		CustomerID:     "cust_01",
		SpendingLimit:  &limit,
		IsCompanyAdmin: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "cust_01", employee.CustomerID)
	assert.Equal(t, company.ID, employee.CompanyID)
	require.NotNil(t, employee.SpendingLimit)
	assert.Equal(t, limit, *employee.SpendingLimit)
	assert.True(t, links.bags[LinkEmployeeCustomer][employee.ID]["cust_01"], "bağ kurulmalı")
}

// TestCalisanEklemeKimlikOneklerinizDenetler yanlış tipteki bir kimliğin
// veritabanına hiç gitmeden yakalandığını doğrular.
func TestCalisanEklemeKimlikOneklerinizDenetler(t *testing.T) {
	svc, repo, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID,
		// Şirket kimliği müşteri kimliği yerine verilmiş.
		CustomerID: company.ID,
	})
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
	assert.Zero(t, repo.calls["CreateEmployee"])
}

// TestCalisanEklemeOlmayanSirketeBulunamadiDoner eksik kaynağın 422 değil 404
// sınıfında bildirildiğini doğrular.
func TestCalisanEklemeOlmayanSirketeBulunamadiDoner(t *testing.T) {
	svc, repo, _ := yeniServis(t)

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:  "comp_YOK",
		CustomerID: "cust_01",
	})
	assert.True(t, errors.IsNotFound(err), "beklenen sınıf NotFound, gelen: %v", err)
	assert.Zero(t, repo.calls["CreateEmployee"], "şirket yoksa çalışan hiç yazılmamalı")
}

// TestCalisanEklemeBagKurulamazsaGeriAlinir telafinin çalıştığını doğrular.
//
// Geri alınmasaydı, müşterisi olmayan bir çalışan kaydı ayakta kalırdı: kayıt
// bir harcama limiti taşır ama vitrinde hiç kimseye çözülmez.
func TestCalisanEklemeBagKurulamazsaGeriAlinir(t *testing.T) {
	svc, repo, links := yeniServis(t)
	company := yeniSirket(t, svc)
	links.failCreate = true

	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID:  company.ID,
		CustomerID: "cust_01",
	})
	require.Error(t, err)

	require.Len(t, repo.employees, 1, "kayıt yazılmış olmalı")
	for _, e := range repo.employees {
		assert.NotNil(t, e.DeletedAt, "bağı kurulamayan çalışan geri alınmalı")
	}
}

// TestAyniMusteriIkinciSirketeEklenemez kardinalitenin sonucunu doğrular.
//
// Kural link tablosundadır; burada sınanan, servisin o ihlali ÇAKIŞMA olarak
// taşımasıdır — Internal'a çevrilseydi istemci düzeltilebilir bir durumu sunucu
// hatası sanardı.
func TestAyniMusteriIkinciSirketeEklenemez(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ilk := yeniSirket(t, svc)

	ikinci, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Beta Ltd.", Email: "beta@ornek.test", CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: ilk.ID, CustomerID: "cust_01"})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: ikinci.ID, CustomerID: "cust_01"})
	assert.True(t, errors.IsConflict(err), "beklenen sınıf Conflict, gelen: %v", err)
}

// TestSirketSilmeCalisanlariVeBaglariTemizler şirket silmenin kararını
// doğrular: sarkan çalışan kaydı KALMAZ ve müşteri bağı serbest kalır.
//
// Bağın serbest kalması testin asıl iddiasıdır: kalsaydı müşteri, kapanmış bir
// şirket yüzünden bir daha hiçbir şirkete çalışan olarak eklenemezdi.
func TestSirketSilmeCalisanlariVeBaglariTemizler(t *testing.T) {
	svc, repo, links := yeniServis(t)
	company := yeniSirket(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_01",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCompany(t.Context(), company.ID))

	assert.NotNil(t, repo.employees[employee.ID].DeletedAt, "çalışan da silinmeli")
	assert.Empty(t, links.bags[LinkEmployeeCustomer][employee.ID], "müşteri bağı kaldırılmalı")

	// Müşteri artık başka bir şirkete eklenebilmeli.
	yeni, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Gamma Ltd.", Email: "gamma@ornek.test", CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: yeni.ID, CustomerID: "cust_01"})
	assert.NoError(t, err, "bağı serbest kalan müşteri yeniden işe alınabilmeli")
}

// TestCalisanSilmeBagiKaldirir tek bir çalışanın silinmesinde de bağın
// temizlendiğini doğrular. İşten çıkan birinin başka bir şirkette işe başlaması
// olağan durumdur.
func TestCalisanSilmeBagiKaldirir(t *testing.T) {
	svc, _, links := yeniServis(t)
	company := yeniSirket(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_01",
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteEmployee(t.Context(), employee.ID))
	assert.Empty(t, links.bags[LinkEmployeeCustomer][employee.ID])
}

// TestUyelikYalnizcaKendiSirketiniDoner modülün vitrin değişmezini sabitler:
// bir müşteri BAŞKASININ şirketini okuyamaz.
//
// Yüzeyde şirket kimliği alan bir uç olmadığı için (bkz. api paketi) tek
// giriş noktası budur; burada tuttuğu sürece vitrinde de tutar.
func TestUyelikYalnizcaKendiSirketiniDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)

	acme := yeniSirket(t, svc)
	beta, err := svc.CreateCompany(t.Context(), CompanyInput{
		Name: "Beta Ltd.", Email: "beta@ornek.test", CurrencyCode: "EUR",
		SpendingLimitResetPeriod: string(models.ResetYearly),
	})
	require.NoError(t, err)

	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: acme.ID, CustomerID: "cust_A"})
	require.NoError(t, err)
	_, err = svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: beta.ID, CustomerID: "cust_B"})
	require.NoError(t, err)

	uyelikA, err := svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	assert.Equal(t, acme.ID, uyelikA.Company.ID)

	uyelikB, err := svc.MembershipOfCustomer(t.Context(), "cust_B")
	require.NoError(t, err)
	assert.Equal(t, beta.ID, uyelikB.Company.ID,
		"her müşteri YALNIZCA kendi şirketini görmeli")

	_, err = svc.MembershipOfCustomer(t.Context(), "cust_YABANCI")
	assert.True(t, errors.IsNotFound(err),
		"hiçbir şirkete bağlı olmayan müşteri 404 almalı, gelen: %v", err)
}

// TestUyelikPencereBaslangiciniHesaplar harcama penceresinin şirketin
// periyodundan türetildiğini doğrular.
//
// Pencerenin kendisi bu turda uygulanmaz; sonraki adımın okuyacağı değer tam
// olarak budur ve TAKVİME göredir (kaydın açılış tarihine göre değil).
func TestUyelikPencereBaslangiciniHesaplar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc) // aylık
	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	uyelik, err := svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	require.NotNil(t, uyelik.SpendingWindowStart)
	assert.Equal(t, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), *uyelik.SpendingWindowStart)

	// Periyot "never" olduğunda pencere yoktur.
	hicbirZaman := string(models.ResetNever)
	_, err = svc.UpdateCompany(t.Context(), company.ID,
		UpdateCompanyInput{SpendingLimitResetPeriod: &hicbirZaman})
	require.NoError(t, err)

	uyelik, err = svc.MembershipOfCustomer(t.Context(), "cust_A")
	require.NoError(t, err)
	assert.Nil(t, uyelik.SpendingWindowStart)
}

// TestUyelikSilinmisCalisanaCozulmez temizlenememiş bir bağın silinmiş kaydı
// geri getiremeyeceğini doğrular.
//
// Senaryo gerçektir: link kaldırma veritabanı işleminin dışındadır ve
// başarısız olabilir (bkz. Service.unlinkCustomers). Okuma yolu bu yüzden
// deleted_at IS NULL süzmeye GÜVENİR; süzmeseydi silinmiş bir çalışan
// vitrinde hâlâ harcama yetkisi taşırdı.
func TestUyelikSilinmisCalisanaCozulmez(t *testing.T) {
	svc, _, links := yeniServis(t)
	company := yeniSirket(t, svc)

	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	links.failDelete = true // bağ temizlenemesin
	require.NoError(t, svc.DeleteEmployee(t.Context(), employee.ID))
	require.True(t, links.bags[LinkEmployeeCustomer][employee.ID]["cust_A"],
		"test kurulumu: bağ bilerek sarkık bırakıldı")

	_, err = svc.MembershipOfCustomer(t.Context(), "cust_A")
	assert.True(t, errors.IsNotFound(err),
		"sarkan bağ silinmiş çalışanı geri getirmemeli, gelen: %v", err)
}

// TestHarcamaLimitiKaldirilabilir "dokunma" ile "sınırsız yap" ayrımını
// doğrular.
//
// Ayrım olmasaydı bir kez konmuş limit asla kaldırılamazdı: JSON'da null ile
// alanın hiç gönderilmemesi aynı nil işaretçiye çözülür.
func TestHarcamaLimitiKaldirilabilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	limit := int64(5000)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &limit,
	})
	require.NoError(t, err)

	// Yalnızca yönetici işaretini değiştirmek limite DOKUNMAMALI.
	yonetici := true
	guncel, err := svc.UpdateEmployee(t.Context(), employee.ID,
		UpdateEmployeeInput{IsCompanyAdmin: &yonetici})
	require.NoError(t, err)
	require.NotNil(t, guncel.SpendingLimit, "limit korunmalı")
	assert.Equal(t, limit, *guncel.SpendingLimit)

	guncel, err = svc.UpdateEmployee(t.Context(), employee.ID,
		UpdateEmployeeInput{ClearSpendingLimit: true})
	require.NoError(t, err)
	assert.Nil(t, guncel.SpendingLimit, "limit kaldırılmalı")
	assert.False(t, guncel.HasSpendingLimit())
}

// TestHarcamaLimitiHemVerilipHemKaldirilamaz çelişkili girdinin reddedildiğini
// doğrular. Sessizce birini seçmek, istemcinin hangisinin uygulandığını
// bilmemesi demek olurdu.
func TestHarcamaLimitiHemVerilipHemKaldirilamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	limit := int64(100)
	employee, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A",
	})
	require.NoError(t, err)

	_, err = svc.UpdateEmployee(t.Context(), employee.ID, UpdateEmployeeInput{
		SpendingLimit: &limit, ClearSpendingLimit: true,
	})
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
}

// TestNegatifHarcamaLimitiReddedilir sınırın anlamlı olmasını zorlar: negatif
// bir limit her karşılaştırmayı aşar ve çalışanı sessizce alışverişten men
// ederdi.
func TestNegatifHarcamaLimitiReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	negatif := int64(-1)
	_, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &negatif,
	})
	assert.True(t, errors.IsInvalid(err), "beklenen sınıf Invalid, gelen: %v", err)
}

// TestSifirHarcamaLimitiSinirsizdanFarklidir 0 ile nil'in ayrı anlamlar
// taşıdığını doğrular: biri "hiç harcayamaz", öteki "sınırsız".
func TestSifirHarcamaLimitiSinirsizdanFarklidir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	sifir := int64(0)
	sinirli, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_A", SpendingLimit: &sifir,
	})
	require.NoError(t, err)
	require.NotNil(t, sinirli.SpendingLimit)
	assert.Equal(t, int64(0), *sinirli.SpendingLimit)
	assert.True(t, sinirli.HasSpendingLimit(), "sıfır limit de bir sınırdır")

	sinirsiz, err := svc.CreateEmployee(t.Context(), EmployeeInput{
		CompanyID: company.ID, CustomerID: "cust_B",
	})
	require.NoError(t, err)
	assert.False(t, sinirsiz.HasSpendingLimit())
}

// TestCalisanListesiMusteriKimliklerinizTekSorguylaDoldurur ADR 0004'ün N+1
// yasağını sabitler.
//
// Müşteri kimlikleri link'ten gelir; kayıt başına ayrı bir bağ okuması,
// sayfa büyüdükçe sorgu sayısının da büyümesi demek olurdu.
func TestCalisanListesiMusteriKimliklerinizTekSorguylaDoldurur(t *testing.T) {
	svc, _, links := yeniServis(t)
	company := yeniSirket(t, svc)

	for _, id := range []string{"cust_A", "cust_B", "cust_C"} {
		_, err := svc.CreateEmployee(t.Context(), EmployeeInput{CompanyID: company.ID, CustomerID: id})
		require.NoError(t, err)
	}
	links.calls["ListMany"] = 0

	sayfa, err := svc.ListEmployees(t.Context(), ListEmployeesInput{CompanyID: &company.ID})
	require.NoError(t, err)

	require.Len(t, sayfa.Items, 3)
	assert.Equal(t, int64(3), sayfa.Count)
	assert.Equal(t, 1, links.calls["ListMany"], "kayıt sayısından bağımsız TEK bağ sorgusu olmalı")
	for _, e := range sayfa.Items {
		assert.NotEmpty(t, e.CustomerID, "her kaydın müşteri kimliği dolmalı")
	}
}

// TestListelemeSayfalamaSinirlariniUygular varsayılan ve üst sınırın
// uygulandığını doğrular.
//
// Aşırı limit KIRPILMAZ, reddedilir: sessizce kırpılan bir limit istemciye
// sayfa boyunu yanlış bildirir ve sayfalama döngüsü aynı kayıtları tekrar okur.
func TestListelemeSayfalamaSinirlariniUygular(t *testing.T) {
	svc, _, _ := yeniServis(t)
	yeniSirket(t, svc)

	sayfa, err := svc.ListCompanies(t.Context(), ListCompaniesInput{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, sayfa.Limit, "limit verilmezse varsayılan uygulanmalı")

	_, err = svc.ListCompanies(t.Context(), ListCompaniesInput{Limit: MaxLimit + 1})
	assert.True(t, errors.IsInvalid(err), "üst sınırı aşan limit reddedilmeli")

	_, err = svc.ListEmployees(t.Context(), ListEmployeesInput{Offset: -1})
	assert.True(t, errors.IsInvalid(err), "negatif offset reddedilmeli")
}

// TestSirketSuzgeciNormalizeEdilir süzgeç değerinin de saklama biçimine
// çevrildiğini doğrular; çevrilmeseydi büyük harfli bir e-posta hiçbir kaydı
// bulamazdı.
func TestSirketSuzgeciNormalizeEdilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	yeniSirket(t, svc)

	aranan := "MUHASEBE@acme.EXAMPLE"
	sayfa, err := svc.ListCompanies(t.Context(), ListCompaniesInput{Email: &aranan})
	require.NoError(t, err)
	assert.Len(t, sayfa.Items, 1)
}

// TestSirketGuncellemeZorunluluguKaldiramaz kısmi güncellemenin sınırını
// sabitler: verilmeyen alan değişmez, ama VERİLEN bir alan boşaltılamaz.
func TestSirketGuncellemeZorunluluguKaldiramaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	company := yeniSirket(t, svc)

	bos := ""
	_, err := svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{Name: &bos})
	assert.True(t, errors.IsInvalid(err), "ad boşaltılamaz")

	_, err = svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{CurrencyCode: &bos})
	assert.True(t, errors.IsInvalid(err), "para birimi boşaltılamaz")

	_, err = svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{SpendingLimitResetPeriod: &bos})
	assert.True(t, errors.IsInvalid(err),
		"periyot boş verilirse sessizce 'never'a düşmemeli")

	// Adres alanları ise gerçekten temizlenebilir.
	guncel, err := svc.UpdateCompany(t.Context(), company.ID, UpdateCompanyInput{PostalCode: &bos})
	require.NoError(t, err)
	assert.Empty(t, guncel.PostalCode)
}
