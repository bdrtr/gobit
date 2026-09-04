//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri sahte bir depo ile servisin KARARLARINI kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: migration'ın geri alınabildiğini
// ve modülün merkezî kurallarının gerçekten VERİTABANINDA durduğunu —
// "kayıtlı e-posta tekildir ama misafirinki değildir", "varsayılan adresi
// müşteri başına tektir", "bir adrese yalnızca SAHİBİ erişebilir" ve "yumuşak
// silinen kayıt hiçbir okumada görünmez".
//
// Bu iddiaların hiçbiri sahte depoyla sınanamaz. İlk ikisi kısmi benzersiz
// indekslerin kendisinde, son ikisi ise sorguların WHERE koşulunda durur; sahte
// depo dördünü de Go'da TAKLİT eder ve taklidin gerçeğe uyduğunu yalnızca bu
// dosya gösterir. Sahte depoya bakan bir birim testi, koşul SQL'den düştüğünde
// bile yeşil kalırdı.
package customer_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

const postgresImage = "postgres:16-alpine"

// modulTablolari modülün sahip olduğu tablolardır; migration testleri bu
// listeyi kullanır.
var modulTablolari = []string{
	"customer", "customer_group", "customer_group_customer", "customer_address",
}

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
	// epostaSayaci testler arasında benzersiz e-posta üretir.
	epostaSayaci atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// Eşzamanlılık testi onlarca goroutine'i aynı anda koşturur; her işlem bir
	// bağlantı tuttuğu için havuz varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN, customer.New(nil).Migrations(), customer.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// yeniServis gerçek depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) *service.Service {
	t.Helper()

	return service.New(repository.New(testPool.Pool()), service.Options{})
}

// yeniEposta testler arasında çakışmayan bir e-posta üretir.
func yeniEposta(t *testing.T) string {
	t.Helper()

	return fmt.Sprintf("t%d@example.com", epostaSayaci.Add(1))
}

// yeniHesap kayıtlı bir müşteri açar.
func yeniHesap(ctx context.Context, t *testing.T, svc *service.Service) models.Customer {
	t.Helper()

	c, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: yeniEposta(t)})
	require.NoError(t, err)
	return c
}

// gecerliAdres testlerde kullanılan geçerli bir adresin girdisidir.
func gecerliAdres() service.AddressInput {
	return service.AddressInput{
		FirstName:   "Ali",
		LastName:    "Veli",
		Address1:    "Atatürk Cad. 1",
		City:        "İstanbul",
		CountryCode: "tr",
		PostalCode:  "34000",
	}
}

// nowUTC kimlik üretiminde kullanılan geçerli anı döner.
func nowUTC() time.Time { return time.Now().UTC() }

// tabloVar tablonun veritabanında olup olmadığını bildirir.
func tabloVar(ctx context.Context, t *testing.T, table string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		`SELECT EXISTS (
             SELECT 1 FROM pg_class c
             JOIN pg_namespace n ON n.oid = c.relnamespace
             WHERE c.relname = $1 AND c.relkind = 'r' AND n.nspname = current_schema()
         )`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// TestMigrationGeriAlinabilir migration'ın uygulanıp geri alınabildiğini
// doğrular (plan Bölüm 8: up/down çiftleri, geri alınabilir).
func TestMigrationGeriAlinabilir(t *testing.T) {
	ctx := context.Background()
	src := customer.New(nil).Migrations()

	for _, table := range modulTablolari {
		require.True(t, tabloVar(ctx, t, table), "%s başlangıçta var olmalı", table)
	}

	require.NoError(t, db.MigrateDown(ctx, testDSN, src, customer.ModuleName, 0))
	for _, table := range modulTablolari {
		assert.False(t, tabloVar(ctx, t, table), "%s geri alma sonrası kalmamalı", table)
	}

	require.NoError(t, db.Migrate(ctx, testDSN, src, customer.ModuleName))
	for _, table := range modulTablolari {
		assert.True(t, tabloVar(ctx, t, table), "%s yeniden uygulanmalı", table)
	}

	version, dirty, err := db.Version(ctx, testDSN, customer.ModuleName)
	require.NoError(t, err)
	assert.False(t, dirty, "yarıda kalmış migration olmamalı")
	assert.Equal(t, uint(1), version)
}

// TestCrossModuleForeignKeyYok modülün tablolarındaki TÜM foreign key'lerin
// yine modülün kendi tablolarına gittiğini doğrular (Prensip 2.2).
func TestCrossModuleForeignKeyYok(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT c.conname, src.relname, tgt.relname
         FROM pg_constraint c
         JOIN pg_class src ON src.oid = c.conrelid
         JOIN pg_class tgt ON tgt.oid = c.confrelid
         WHERE c.contype = 'f' AND src.relname = ANY($1)`, modulTablolari)
	require.NoError(t, err)
	defer rows.Close()

	sahipli := make(map[string]struct{}, len(modulTablolari))
	for _, table := range modulTablolari {
		sahipli[table] = struct{}{}
	}

	var sayi int
	for rows.Next() {
		var name, src, tgt string
		require.NoError(t, rows.Scan(&name, &src, &tgt))
		assert.Contains(t, sahipli, tgt,
			"%s kısıtı modül dışına referans veriyor (%s -> %s)", name, src, tgt)
		sayi++
	}
	require.NoError(t, rows.Err())
	assert.Positive(t, sayi, "modül içi foreign key'ler kullanılmalı")
}

// TestKayitliEpostaVeritabaninda tekilliğin gerçekten kısmi benzersiz indekste
// olduğunu doğrular.
//
// Servis bu kuralı KENDİ kontrol etmez; eğer indeks yoksa ya da WHERE koşulu
// yanlışsa test burada düşer.
func TestKayitliEpostaVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	eposta := yeniEposta(t)
	_, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.NoError(t, err)

	_, err = svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeEmailTaken, errors.CodeOf(err))
}

// TestAyniEpostaylaCokMisafirKabulEdilir Faz 5 DoD'sinin misafir senaryosunu
// GERÇEK indeks üzerinde doğrular.
//
// Kısmi indeksin WHERE has_account koşulu düşerse (yani indeks tüm satırları
// kapsarsa) bu test düşer; kuralı koruyan kapı budur.
func TestAyniEpostaylaCokMisafirKabulEdilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	eposta := yeniEposta(t)
	var kimlikler []string
	for range 3 {
		misafir, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: eposta})
		require.NoError(t, err, "aynı e-postayla misafir kaydı reddedilmemeli")
		kimlikler = append(kimlikler, misafir.ID)
	}
	assert.Len(t, kimlikler, 3)

	// Aynı e-postayla BİR hesap da açılabilir; misafir kayıtları onu engellemez.
	hesap, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.NoError(t, err, "misafir kayıtları hesap açılmasını engellememeli")

	bulunan, err := svc.GetCustomerByEmail(ctx, eposta)
	require.NoError(t, err)
	assert.Equal(t, hesap.ID, bulunan.ID, "e-postaya göre arama HESABI bulmalı")

	// Listeleme misafirleri de görür; toplam dört kayıt vardır.
	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &eposta})
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.Count)
}

// TestMisafirdenHesabaGecis dönüşümü ve çakışmasını gerçek veritabanında
// doğrular.
func TestMisafirdenHesabaGecis(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	t.Run("basarili", func(t *testing.T) {
		misafir, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: yeniEposta(t)})
		require.NoError(t, err)

		require.NoError(t, svc.ConvertGuestToAccount(ctx, misafir.ID))

		okunan, err := svc.GetCustomer(ctx, misafir.ID)
		require.NoError(t, err)
		assert.True(t, okunan.HasAccount)

		// Artık e-postaya göre bulunabilir.
		bulunan, err := svc.GetCustomerByEmail(ctx, okunan.Email)
		require.NoError(t, err)
		assert.Equal(t, misafir.ID, bulunan.ID)
	})

	t.Run("cakisma", func(t *testing.T) {
		eposta := yeniEposta(t)
		_, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
		require.NoError(t, err)
		misafir, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: eposta})
		require.NoError(t, err)

		err = svc.ConvertGuestToAccount(ctx, misafir.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))

		okunan, getErr := svc.GetCustomer(ctx, misafir.ID)
		require.NoError(t, getErr)
		assert.False(t, okunan.HasAccount, "çakışan dönüşüm işlemi GERİ ALINMALI")
	})

	t.Run("zaten hesap", func(t *testing.T) {
		hesap := yeniHesap(ctx, t, svc)

		err := svc.ConvertGuestToAccount(ctx, hesap.ID)
		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))
		assert.Equal(t, repository.CodeAlreadyAccount, errors.CodeOf(err))
	})
}

// TestEszamanliMisafirDonusumu aynı e-postalı iki misafirin aynı anda hesaba
// çevrilmesinde tam olarak BİRİNİN kazandığını doğrular.
//
// Ön denetim tek başına yetmezdi: iki işlem de "e-posta boş" görüp ikisi de
// yazabilirdi. Sınırı koyan kısmi benzersiz indekstir ve bu test onu gerçek
// eşzamanlılıkla yoklar.
func TestEszamanliMisafirDonusumu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	eposta := yeniEposta(t)
	const yarisci = 8

	kimlikler := make([]string, 0, yarisci)
	for range yarisci {
		misafir, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: eposta})
		require.NoError(t, err)
		kimlikler = append(kimlikler, misafir.ID)
	}

	var (
		wg       sync.WaitGroup
		basarili atomic.Int64
		cakisma  atomic.Int64
	)
	for _, id := range kimlikler {
		wg.Add(1)
		go func(customerID string) {
			defer wg.Done()
			switch err := svc.ConvertGuestToAccount(ctx, customerID); {
			case err == nil:
				basarili.Add(1)
			case errors.IsConflict(err):
				cakisma.Add(1)
			default:
				t.Errorf("beklenmeyen hata: %v", err)
			}
		}(id)
	}
	wg.Wait()

	assert.Equal(t, int64(1), basarili.Load(), "tam olarak bir dönüşüm kazanmalı")
	assert.Equal(t, int64(yarisci-1), cakisma.Load(), "kalanlar çakışma almalı")

	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &eposta})
	require.NoError(t, err)
	var hesapSayisi int
	for _, c := range page.Items {
		if c.HasAccount {
			hesapSayisi++
		}
	}
	assert.Equal(t, 1, hesapSayisi, "aynı e-postayla tek hesap kalmalı")
}

// TestSilinenHesabinEpostasiYenidenKullanilabilir yumuşak silmenin indeks
// kapsamından çıkardığını doğrular.
//
// Kısmi indeksin deleted_at IS NULL koşulu düşerse silinmiş bir hesabın
// e-postası sonsuza dek işgal edilmiş kalırdı.
func TestSilinenHesabinEpostasiYenidenKullanilabilir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	eposta := yeniEposta(t)
	ilk, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, ilk.ID))

	ikinci, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.NoError(t, err, "silinmiş hesabın e-postası yeniden kullanılabilmeli")
	assert.NotEqual(t, ilk.ID, ikinci.ID)
}

// TestSilinenMusteriHicbirOkumadaGorunmez yumuşak silmenin GERÇEK sorgularda
// süzüldüğünü doğrular.
//
// Kural (plan Bölüm 8) "silme SOFT'tur, okumalar deleted_at IS NULL süzer"
// biçimindedir ve tek dayanağı SQL'in WHERE koşuludur: satır tabloda KALIR,
// görünmezliği sağlayan yalnızca o süzgeçtir. Birim testi bunu kanıtlayamaz —
// sahte depo süzgeci kendisi uygular ve yalnızca kendi kuralını doğrular.
// Süzgeç düşerse silinmiş müşteriler yönetim listelemesinde, e-postayla
// aramada ve Query sağlayıcısının çıktısında (yani cart/order
// genişletmelerinde) geri gelirdi.
//
// Test müşteri tablosunu okuyan BEŞ sorgunun her birine ayrı ayrı dokunur:
// GetCustomer, GetAccountByEmail, ListCustomers/CountCustomers,
// ListCustomersByIDs ve GetCustomerForUpdate.
func TestSilinenMusteriHicbirOkumadaGorunmez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	saglayici := service.NewQueryProvider(svc)

	eposta := yeniEposta(t)
	musteri, err := svc.CreateCustomer(ctx, service.CustomerInput{Email: eposta})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, musteri.ID))

	// Yumuşak silme satırı SİLMEZ; testin geri kalanı ancak satır dururken
	// anlamlıdır.
	var kalanSatir int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer WHERE id = $1`, musteri.ID).Scan(&kalanSatir))
	require.Equal(t, 1, kalanSatir, "yumuşak silme satırı tabloda bırakmalı")

	// GetCustomer.
	_, err = svc.GetCustomer(ctx, musteri.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"silinen customer idyle okunmamalı")

	// GetAccountByEmail.
	_, err = svc.GetCustomerByEmail(ctx, eposta)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"silinen hesap e-postayla bulunmamalı")

	// ListCustomers + CountCustomers.
	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{Email: &eposta})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "silinen müşteri sayımda görünmemeli")
	assert.Empty(t, page.Items, "silinen müşteri listede görünmemeli")

	// ListCustomersByIDs (Query sağlayıcısının toplu okuma yolu).
	kayitlar, err := saglayici.FetchByIDs(ctx, []string{musteri.ID}, nil)
	require.NoError(t, err)
	assert.Empty(t, kayitlar, "silinen müşteri Query sağlayıcısında görünmemeli")

	// GetCustomerForUpdate: adresi yazma yolu müşteriyi kilitleyerek okur.
	_, err = svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"silinen müşteriye adresi yazılamamalı")

	// Yazma yolları da aynı süzgeci taşır.
	yeniAd := "Ayşe"
	_, err = svc.UpdateCustomer(ctx, musteri.ID, service.UpdateCustomerInput{FirstName: &yeniAd})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"silinen müşteri güncellenememeli")

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteCustomer(ctx, musteri.ID)),
		"silinen müşteri ikinci kez silinememeli")
}

// TestSilinenMisafirHesabaCevrilemez yumuşak silme süzgecinin misafir dönüşüm
// yolunda da durduğunu doğrular.
//
// Dönüşüm müşteriyi kilitleyerek okur ve yükseltme sorgusu da aynı süzgeci
// taşır; süzgeç düşerse silinmiş bir misafir hesaba çevrilebilir ve silinmiş
// bir satır e-posta benzersizliğini işgal ederdi.
func TestSilinenMisafirHesabaCevrilemez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	misafir, err := svc.RegisterGuest(ctx, service.CustomerInput{Email: yeniEposta(t)})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, misafir.ID))

	err = svc.ConvertGuestToAccount(ctx, misafir.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"silinen misafir hesaba çevrilememeli")
}

// TestEpostaCheckKisitiBuyukHarfiEngeller normalizasyonun veritabanında da
// zorlandığını doğrular.
//
// Servis atlansa bile (örn. ileride yazılacak bir toplu içe aktarma) büyük
// harfli bir e-posta tabloya giremez; girseydi kısmi benzersiz indeks aynı
// hesabı iki kez kabul ederdi.
func TestEpostaCheckKisitiBuyukHarfiEngeller(t *testing.T) {
	ctx := context.Background()

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO customer (id, email, has_account) VALUES ($1, $2, TRUE)`,
		models.NewCustomerID(nowUTC()), "BUYUK@EXAMPLE.COM")
	require.Error(t, err, "büyük harfli e-posta CHECK kısıtına takılmalı")
	assert.Contains(t, err.Error(), "customer_email_check")
}

// TestVarsayilanAdresKisitiVeritabaninda müşteri başına tek varsayılan kuralının
// UYGULAMADA DEĞİL veritabanında olduğunu doğrular.
//
// Test servisi bilinçli olarak ATLAR ve iki satırı doğrudan SQL ile işaretler:
// kural yalnızca uygulamada olsaydı bu geçerdi ve iki varsayılan kargo adresi
// tabloda yan yana dururdu.
func TestVarsayilanAdresKisitiVeritabaninda(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteri := yeniHesap(ctx, t, svc)

	ilk, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	ikinci, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_shipping = TRUE WHERE id = $1`, ilk.ID)
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_shipping = TRUE WHERE id = $1`, ikinci.ID)
	require.Error(t, err, "ikinci varsayılan kargo adresi veritabanınca reddedilmeli")
	assert.Contains(t, err.Error(), "customer_address_default_shipping_uniq")

	// Fatura tarafı da aynı şekilde korunur.
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer_address SET is_default_billing = TRUE WHERE id IN ($1, $2)`, ilk.ID, ikinci.ID)
	require.Error(t, err, "ikinci varsayılan fatura adresi veritabanınca reddedilmeli")
	assert.Contains(t, err.Error(), "customer_address_default_billing_uniq")
}

// TestVarsayilanAdresServisYoluyla servisin eski işareti temizleyerek kısıtı
// sağladığını doğrular.
func TestVarsayilanAdresServisYoluyla(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteri := yeniHesap(ctx, t, svc)

	ilk, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	ikinci, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.SetDefaultShippingAddress(ctx, musteri.ID, ilk.ID)
	require.NoError(t, err)
	_, err = svc.SetDefaultShippingAddress(ctx, musteri.ID, ikinci.ID)
	require.NoError(t, err, "yeni varsayılan eskisini temizleyerek yazılmalı")

	assert.Equal(t, 1, varsayilanSayisi(ctx, t, musteri.ID, "is_default_shipping"))

	eskisi, err := svc.GetAddress(ctx, musteri.ID, ilk.ID)
	require.NoError(t, err)
	assert.False(t, eskisi.IsDefaultShipping)
}

// TestEszamanliVarsayilanAdres aynı müşteriye gelen eşzamanlı atamaların
// kilitlenmeden ve tek varsayılan bırakarak tamamlandığını doğrular.
//
// Kilit sırası (önce müşteri satırı, sonra adresler) sabit olmasaydı işlemler
// birbirini ters sırada bekler ve veritabanı bir kısmını deadlock ile
// öldürürdü; iddia ancak gerçek goroutine'lerle sınanabilir.
func TestEszamanliVarsayilanAdres(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteri := yeniHesap(ctx, t, svc)

	const yarisci = 8
	adresler := make([]string, 0, yarisci)
	for range yarisci {
		a, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
		require.NoError(t, err)
		adresler = append(adresler, a.ID)
	}

	var wg sync.WaitGroup
	for _, id := range adresler {
		wg.Add(1)
		go func(addressID string) {
			defer wg.Done()
			if _, err := svc.SetDefaultShippingAddress(ctx, musteri.ID, addressID); err != nil {
				t.Errorf("eşzamanlı varsayılan atama hata verdi: %v", err)
			}
		}(id)
	}
	wg.Wait()

	assert.Equal(t, 1, varsayilanSayisi(ctx, t, musteri.ID, "is_default_shipping"),
		"eşzamanlı atamalardan sonra tek varsayılan kalmalı")
}

// TestAdresYasamDongusu adresin oluşturma, güncelleme, listeleme ve yumuşak
// silmeyi uçtan uca doğrular.
func TestAdresYasamDongusu(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteri := yeniHesap(ctx, t, svc)

	adresi, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)
	assert.Equal(t, "TR", adresi.CountryCode)
	assert.False(t, adresi.CreatedAt.IsZero(), "created_at veritabanından gelmeli")
	assert.Equal(t, "UTC", adresi.CreatedAt.Location().String(), "zaman UTC olmalı")

	yeniSehir := "Ankara"
	guncel, err := svc.UpdateAddress(ctx, musteri.ID, adresi.ID,
		service.UpdateAddressInput{City: &yeniSehir})
	require.NoError(t, err)
	assert.Equal(t, "Ankara", guncel.City)
	assert.Equal(t, adresi.Address1, guncel.Address1, "verilmeyen alan korunmalı")

	require.NoError(t, svc.DeleteAddress(ctx, musteri.ID, adresi.ID))

	_, err = svc.GetAddress(ctx, musteri.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	kalanlar, err := svc.ListAddresses(ctx, musteri.ID)
	require.NoError(t, err)
	assert.Empty(t, kalanlar, "yumuşak silinen adresi listede görünmemeli")
}

// TestBaskaMusterininAdresineErisilemez adresin sahipliğinin GERÇEK sorguda
// zorlandığını doğrular.
//
// Sahiplik denetimi, adrese dokunan her sorgunun WHERE koşulundaki
// customer_id eşitliğidir (bkz. queries/customer_address.sql). Koşul düşerse
// adresin kimliğini bilen HERKES başkasının adresini okuyabilir, güncelleyebilir,
// silebilir ve varsayılan yapabilir; store uçları Faz 8'e kadar korumasız
// olduğu ve customer id yol parametresinden geldiği için bu koşul şu an
// TEK bariyerdir (bkz. api paket belgesi).
//
// Birim testi bu iddiayı kanıtlayamaz: sahte depo sahipliği kendisi süzer ve
// yalnızca kendi kuralını doğrular. Hata sınıfı da tek başına yetmez — yanlış
// bir sorgu hata döndürmeden satırı DEĞİŞTİRMİŞ olabilirdi; bu yüzden adresin
// ham hâli servisi atlayarak ayrıca okunur.
func TestBaskaMusterininAdresineErisilemez(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	sahibi := yeniHesap(ctx, t, svc)
	yabanci := yeniHesap(ctx, t, svc)

	adresi, err := svc.CreateAddress(ctx, sahibi.ID, gecerliAdres())
	require.NoError(t, err)

	_, err = svc.GetAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi okunamamalı")

	baskaSehir := "Ankara"
	_, err = svc.UpdateAddress(ctx, yabanci.ID, adresi.ID,
		service.UpdateAddressInput{City: &baskaSehir})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi güncellenememeli")

	_, err = svc.SetDefaultShippingAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi varsayılan kargo adresi yapılamamalı")

	_, err = svc.SetDefaultBillingAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi varsayılan fatura adresi yapılamamalı")

	err = svc.DeleteAddress(ctx, yabanci.ID, adresi.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"başkasının adresi silinememeli")

	// Asıl kanıt: satır tabloda OLDUĞU GİBİ duruyor.
	durum := adresDurumu(ctx, t, adresi.ID)
	assert.Equal(t, gecerliAdres().City, durum.sehir, "yabancının güncellemesi yazılmamalı")
	assert.False(t, durum.varsayilanKargo, "yabancı varsayılan kargo işareti koyamamalı")
	assert.False(t, durum.varsayilanFatura, "yabancı varsayılan fatura işareti koyamamalı")
	assert.False(t, durum.silinmis, "yabancı adresi silememeli")

	// Sahibi kendi adresine ERİŞEBİLMELİ; aksi hâlde yukarıdaki NotFound'lar
	// sahiplikten değil, tümden kırık bir sorgudan geliyor olurdu.
	kendi, err := svc.GetAddress(ctx, sahibi.ID, adresi.ID)
	require.NoError(t, err, "sahibi kendi adresini okuyabilmeli")
	assert.Equal(t, adresi.ID, kendi.ID)
}

// TestMusteriSilinincaAdresleriDeSilinir yumuşak silmenin adresleri de
// kapsadığını doğrular.
//
// Foreign key'in ON DELETE CASCADE'i yalnızca GERÇEK silmede çalışır; yumuşak
// silme bir UPDATE olduğu için adresleri kendiliğinden götürmez.
func TestMusteriSilinincaAdresleriDeSilinir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	musteri := yeniHesap(ctx, t, svc)

	_, err := svc.CreateAddress(ctx, musteri.ID, gecerliAdres())
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, musteri.ID))

	var canli int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_address WHERE customer_id = $1 AND deleted_at IS NULL`,
		musteri.ID).Scan(&canli))
	assert.Zero(t, canli, "silinen müşterinin canlı adresi kalmamalı")
}

// TestGrupUyeligi grup yaşam döngüsünü ve üyeliğin idempotansını doğrular.
func TestGrupUyeligi(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	musteri := yeniHesap(ctx, t, svc)
	grup, err := svc.CreateGroup(ctx, service.GroupInput{
		Name:     "VIP-" + models.NewCustomerGroupID(nowUTC()),
		Metadata: map[string]any{"indirim": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, "10", grup.Metadata["indirim"], "metadata jsonb'den geri gelmeli")

	require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID))
	require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID), "ikinci ekleme hata vermemeli")

	gruplar, err := svc.ListGroupsOf(ctx, musteri.ID)
	require.NoError(t, err)
	require.Len(t, gruplar, 1, "üyelik çoklanmamalı")

	// Aynı adla ikinci grup açılamaz.
	_, err = svc.CreateGroup(ctx, service.GroupInput{Name: grup.Name})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	require.NoError(t, svc.RemoveFromGroup(ctx, musteri.ID, grup.ID))
	err = svc.RemoveFromGroup(ctx, musteri.ID, grup.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"olmayan üyeliğin kaldırılması NotFound olmalı")
}

// TestGrupGuncellemeVeSilme grup adının düzeltilebildiğini ve yumuşak silmenin
// GERÇEK veritabanında görünmezlik ürettiğini doğrular.
//
// Üyelik satırları silmede BIRAKILIR; silinmiş grubu gizleyen tek şey grup
// okuyan her sorgunun deleted_at IS NULL süzgecidir. Müşteri listesinin
// group_id süzgeci de bu yüzden üyelik satırına değil, üyeliğin bağlandığı
// CANLI gruba bakar — yalnızca üyeliğe baksaydı silinmiş bir grubun üyeleri
// listelenmeye devam ederdi.
func TestGrupGuncellemeVeSilme(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	saglayici := service.NewQueryProvider(svc)

	uye := yeniHesap(ctx, t, svc)
	ad := "Segment-" + models.NewCustomerGroupID(nowUTC())
	grup, err := svc.CreateGroup(ctx, service.GroupInput{Name: ad})
	require.NoError(t, err)
	require.NoError(t, svc.AddToGroup(ctx, uye.ID, grup.ID))

	// Ad düzeltilebilir; kısmi benzersiz indeks yeni adı kabul eder.
	duzeltilmis := ad + "-duzeltilmis"
	guncel, err := svc.UpdateGroup(ctx, grup.ID, service.UpdateGroupInput{
		Name:     &duzeltilmis,
		Metadata: map[string]any{"indirim": "10"},
	})
	require.NoError(t, err)
	assert.Equal(t, duzeltilmis, guncel.Name)
	assert.Equal(t, "10", guncel.Metadata["indirim"], "metadata jsonb'den geri gelmeli")
	assert.False(t, guncel.UpdatedAt.Before(guncel.CreatedAt), "updated_at ilerlemeli")

	// Başka bir canlı grubun adı alınamaz; kural indekstedir.
	digeri, err := svc.CreateGroup(ctx, service.GroupInput{
		Name: "Segment-" + models.NewCustomerGroupID(nowUTC()),
	})
	require.NoError(t, err)
	_, err = svc.UpdateGroup(ctx, digeri.ID, service.UpdateGroupInput{Name: &duzeltilmis})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeGroupNameTaken, errors.CodeOf(err))

	require.NoError(t, svc.DeleteGroup(ctx, grup.ID))

	// Üyelik satırı yerinde DURUYOR; görünmezliği sağlayan tek şey süzgeç.
	var uyelikSatiri int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_group_customer WHERE customer_group_id = $1`,
		grup.ID).Scan(&uyelikSatiri))
	require.Equal(t, 1, uyelikSatiri, "grup silmesi üyelik satırını bırakmalı")

	_, err = svc.GetGroup(ctx, grup.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "silinen grup okunmamalı")

	gruplar, err := svc.ListGroupsOf(ctx, uye.ID)
	require.NoError(t, err)
	assert.Empty(t, gruplar, "silinen grup müşterinin gruplarında görünmemeli")

	kayitlar, err := saglayici.FetchByIDs(ctx, []string{uye.ID}, nil)
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Empty(t, kayitlar[0]["group_ids"], "silinen grup fiyat bağlamına taşınmamalı")

	page, err := svc.ListCustomers(ctx, service.ListCustomersInput{GroupID: &grup.ID})
	require.NoError(t, err)
	assert.Zero(t, page.Count, "silinen grubun üyeleri süzgeçle listelenmemeli")
	assert.Empty(t, page.Items)

	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.AddToGroup(ctx, uye.ID, grup.ID)),
		"silinen gruba üye eklenememeli")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(svc.DeleteGroup(ctx, grup.ID)),
		"silinen grup ikinci kez silinememeli")

	_, err = svc.UpdateGroup(ctx, grup.ID, service.UpdateGroupInput{Name: &ad})
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "silinen grup güncellenememeli")

	// Ad indeksin kapsamından çıktığı için serbest kalır.
	_, err = svc.CreateGroup(ctx, service.GroupInput{Name: duzeltilmis})
	require.NoError(t, err, "silinen grubun adı yeniden kullanılabilmeli")
}

// TestQuerySaglayicisiGruplarlaDoner sağlayıcının müşteriyi grup kimlikleriyle
// ve TEK turda döndürdüğünü doğrular.
//
// pricing'in kural bağlamı bu alanı isteyecektir; grup kimlikleri ayrı bir
// turda gelseydi her müşteri için ikinci bir sorgu gerekirdi (ADR 0004).
func TestQuerySaglayicisiGruplarlaDoner(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)
	saglayici := service.NewQueryProvider(svc)

	assert.Equal(t, "customer", saglayici.Entity())
	assert.Equal(t, customer.ProviderName, saglayici.Entity()+query.ProviderSuffix)

	grup, err := svc.CreateGroup(ctx, service.GroupInput{
		Name: "Segment-" + models.NewCustomerGroupID(nowUTC()),
	})
	require.NoError(t, err)

	uye := yeniHesap(ctx, t, svc)
	require.NoError(t, svc.AddToGroup(ctx, uye.ID, grup.ID))
	uyesiz := yeniHesap(ctx, t, svc)

	kayitlar, err := saglayici.FetchByIDs(ctx, []string{uye.ID, uyesiz.ID}, nil)
	require.NoError(t, err)
	require.Len(t, kayitlar, 2)

	byID := map[string]query.Record{}
	for _, kayit := range kayitlar {
		id, ok := kayit[query.IDField].(string)
		require.True(t, ok)
		byID[id] = kayit
	}

	assert.Equal(t, []string{grup.ID}, byID[uye.ID]["group_ids"])
	assert.Empty(t, byID[uyesiz.ID]["group_ids"])
	assert.Equal(t, uye.Email, byID[uye.ID]["email"])
	assert.Equal(t, true, byID[uye.ID]["has_account"])

	// Bulunamayan kimlik hata değildir; kayıt dönmez.
	kayitlar, err = saglayici.FetchByIDs(ctx, []string{models.NewCustomerID(nowUTC())}, nil)
	require.NoError(t, err)
	assert.Empty(t, kayitlar)
}

// TestMetadataYuvarlanir metadata'nın jsonb'ye yazılıp geri okunduğunu
// doğrular.
func TestMetadataYuvarlanir(t *testing.T) {
	ctx := context.Background()
	svc := yeniServis(t)

	musteri, err := svc.CreateCustomer(ctx, service.CustomerInput{
		Email:    yeniEposta(t),
		Metadata: map[string]any{"kaynak": "web", "puan": float64(12)},
	})
	require.NoError(t, err)

	okunan, err := svc.GetCustomer(ctx, musteri.ID)
	require.NoError(t, err)
	assert.Equal(t, "web", okunan.Metadata["kaynak"])
	assert.InDelta(t, 12, okunan.Metadata["puan"], 0.0001)

	// Metadata verilmeyen güncelleme sütuna DOKUNMAZ.
	yeniAd := "Ayşe"
	guncel, err := svc.UpdateCustomer(ctx, musteri.ID, service.UpdateCustomerInput{FirstName: &yeniAd})
	require.NoError(t, err)
	assert.Equal(t, "Ayşe", guncel.FirstName)
	assert.Equal(t, "web", guncel.Metadata["kaynak"], "verilmeyen metadata korunmalı")
}

// adresSatiri adresin tabloda duran ham hâlidir.
type adresSatiri struct {
	// sehir city sütunudur.
	sehir string
	// varsayilanKargo is_default_shipping sütunudur.
	varsayilanKargo bool
	// varsayilanFatura is_default_billing sütunudur.
	varsayilanFatura bool
	// silinmis deleted_at sütununun dolu olup olmadığıdır.
	silinmis bool
}

// adresDurumu adresi SERVİSİ ATLAYARAK doğrudan tablodan okur.
//
// Servisin döndürdüğü hata sınıfı tek başına yetmez: sahipliği süzmeyen bir
// sorgu hiç hata vermeden satırı değiştirmiş olabilir. Ham okuma o farkı
// görünür kılar.
func adresDurumu(ctx context.Context, t *testing.T, addressID string) adresSatiri {
	t.Helper()

	var row adresSatiri
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT city, is_default_shipping, is_default_billing, deleted_at IS NOT NULL
         FROM customer_address WHERE id = $1`, addressID).
		Scan(&row.sehir, &row.varsayilanKargo, &row.varsayilanFatura, &row.silinmis))
	return row
}

// varsayilanSayisi müşterinin verilen sütunda kaç işaretli canlı adresi
// olduğunu döner.
func varsayilanSayisi(ctx context.Context, t *testing.T, customerID, sutun string) int {
	t.Helper()

	// Sütun adı SQL'de parametrelenemez; bu yüzden yalnızca testin kendi
	// sabitleri kabul edilir.
	var sorgu string
	switch sutun {
	case "is_default_shipping":
		sorgu = `SELECT count(*) FROM customer_address
                 WHERE customer_id = $1 AND deleted_at IS NULL AND is_default_shipping`
	case "is_default_billing":
		sorgu = `SELECT count(*) FROM customer_address
                 WHERE customer_id = $1 AND deleted_at IS NULL AND is_default_billing`
	default:
		t.Fatalf("bilinmeyen sütun: %s", sutun)
	}

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx, sorgu, customerID).Scan(&n))
	return n
}
