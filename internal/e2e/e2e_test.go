//go:build integration

// Package e2e planın Faz 5 DoD'sini GERÇEK modüllerle uçtan uca doğrular.
//
// DoD tek cümleyle: "Sepet oluştur -> ürün ekle -> adet güncelle -> ara toplam /
// indirim / vergi / genel toplam DOĞRU hesaplanıyor; MİSAFİR ve KAYITLI MÜŞTERİ
// senaryoları test edilmiş."
//
// # Neden internal/workflows altında değil
//
// ADR 0006, internal/workflows altındaki HİÇBİR paketin internal/modules'ü
// import etmesine izin vermez ve internal/arch'taki
// TestWorkflowlarModulleriImportEtmez bunu dosya sisteminde denetler — test
// dosyaları dahil. Bu paketin işi ise tam tersidir: gerçek modülleri kurmak,
// gerçek migration'ları uygulamak ve akışları o zeminin üstünde koşturmak.
// İkisi aynı ağaçta yaşayamaz, bu yüzden paket internal/e2e altındadır ve
// ADR 0006'nın kapsamı dışındadır.
//
// # Kurulum
//
// Testler tek bir PostgreSQL konteyneri paylaşır (testcontainers) ve kurulum
// cmd/server/main.go'daki sırayı ÖRNEK ALIR: çekirdek servisler container'a
// adla kaydedilir (core.db, core.link, core.query, core.eventbus), çekirdek
// migration'ları uygulanır, modüller [module.Registry] ile ayağa kaldırılır ve
// sepet akışları container'dan ADLA çözülen yüzeylerle kurulur. Kurulumun
// gerçek olması testin bütün değeridir: sahte bir bağımlılıkla geçen bir
// hesap, üretimde aynı hesabı yapacağını kanıtlamaz.
//
// # Beklenen tutarlar neden elle yazılıyor
//
// Her senaryodaki ara toplam, vergi ve genel toplam testin İÇİNDE elle
// hesaplanmış SABİTLERDİR. Üretim kodunun formülünü testte tekrar etmek
// (örneğin vergiyi yine "taban × oran / 10000" ile hesaplamak) aynı hatayı iki
// yerde birden yapmak olurdu ve test kör kalırdı.
//
// # Para
//
// Tüm tutarlar TAM SAYI minor unit'tir (plan Bölüm 8); testte de float yoktur.
package e2e

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customermod "github.com/bdrtr/gobit/internal/modules/customer"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	inventorymod "github.com/bdrtr/gobit/internal/modules/inventory"
	pricingmod "github.com/bdrtr/gobit/internal/modules/pricing"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmod "github.com/bdrtr/gobit/internal/modules/product"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	regionmod "github.com/bdrtr/gobit/internal/modules/region"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// postgresImage testlerin paylaştığı veritabanı imajıdır; modül entegrasyon
// testleriyle AYNI sürüm kullanılır ki şema davranışı iki yerde ayrışmasın.
const postgresImage = "postgres:16-alpine"

// Çekirdek servislerin container'daki adları.
//
// Adlar cmd/server/main.go'dakilerin AYNISIDIR ve tekrarlanmaları bilinçlidir:
// sepet akışları bağımlılıklarını derleme zamanında değil, tam olarak bu
// dizelerle çözer (ADR 0006). Buradaki bir yazım hatası üretimdeki bir yazım
// hatasıyla aynı sonucu verir ve test onu görmelidir.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
)

// Vergisi otomatik uygulanan bölgenin fikstür sabitleri.
//
// Ülke ve para birimi kodları region modülünün TOHUM verisinden gelir
// (000002_region_seed); test yalnızca bölgeyi kurar ve ülkeyi ona bağlar.
const (
	// vergiliUlke vergili bölgeye bağlanan ülkedir (ISO 3166-1 alpha-2).
	vergiliUlke = "TR"
	// vergiliParaBirimi vergili bölgenin para birimidir (ISO 4217).
	vergiliParaBirimi = "TRY"
	// vergiOraniBps vergili bölgenin baz puan oranıdır: 2000 = %20.
	vergiOraniBps int32 = 2000
)

// Vergisi otomatik uygulanMAYAN bölgenin fikstür sabitleri.
//
// Oran bilinçli olarak SIFIR DEĞİLDİR: bölge sıfır oran taşıdığı için değil,
// otomatik vergiyi kapattığı için verginin sıfır çıkması gerekir. Sıfır oranla
// kurulsaydı test iki durumu ayırt edemezdi.
const (
	// vergisizUlke vergisiz bölgeye bağlanan ülkedir.
	vergisizUlke = "DE"
	// vergisizParaBirimi vergisiz bölgenin para birimidir.
	vergisizParaBirimi = "EUR"
	// vergisizOranBps vergisiz bölgenin taşıdığı ama UYGULANMAMASI gereken
	// orandır: 1900 = %19.
	vergisizOranBps int32 = 1900
)

// Testlerin paylaştığı zemin. TestMain doldurur, testler yalnızca okur.
var (
	// testPool tüm modüllerin paylaştığı bağlantı havuzudur.
	testPool *db.Pool
	// testDSN migration çağrılarının kullandığı bağlantı adresidir.
	testDSN string
	// kap modüllerin ve akışların çözüldüğü DI kabıdır.
	kap *container.Container
	// baglar çekirdeğin Module Links servisidir; testler bağların GERÇEKTEN
	// kurulduğunu buradan okuyarak doğrular.
	baglar link.LinkService
)

// Modül servisleri; hepsi container'dan ADLA çözülür, elle kurulmaz.
var (
	urunSvc    *productsvc.Service
	fiyatSvc   *pricingsvc.Service
	bolgeSvc   *regionsvc.Service
	musteriSvc *customersvc.Service
	sepetSvc   *cartsvc.Service
)

// akislar sepet akışlarının ÜRETİM kablolamasıyla kurulmuş örneğidir
// (cartwf.FromContainer). Testte hiçbir köprü ya da sahte yoktur.
var akislar *cartwf.Workflows

// Fikstür bölgelerinin kimlikleri.
var (
	vergiliBolgeID  string
	vergisizBolgeID string
)

// TestMain tek bir Postgres konteyneri kaldırır, modülleri ayağa kaldırır ve
// tüm testleri o zeminin üstünde koşturur.
func TestMain(m *testing.M) {
	os.Exit(postgresIleCalistir(m))
}

// postgresIleCalistir konteyneri kaldırıp kurulumu yapar ve çıkış kodunu döner.
//
// os.Exit defer'ları atladığı için ayrı bir fonksiyondadır: konteyner ve havuz
// ancak burada güvenle kapatılabilir.
func postgresIleCalistir(m *testing.M) int {
	// Modüller açılışta slog.Default() kullanır; testin çıktısı hesap
	// iddialarıyla kalsın diye loglar atılır.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_e2e"),
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

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := zeminiKur(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "zemin kurulamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// zeminiKur container'ı, modülleri, akışları ve bölge fikstürlerini hazırlar.
//
// Sıra cmd/server/main.go ile AYNIDIR ve aynı olması şarttır: modüller
// Register sırasında core.db, core.link ve core.query'yi çözer, dolayısıyla o
// üçü Bootstrap'tan ÖNCE kayıtlı olmalıdır. Sıra değişirse üretimde de
// patlayacak bir kurulum burada patlar — istenen budur.
func zeminiKur(ctx context.Context) error {
	kap = container.New(nil)

	if err := kap.Provide(svcDB, testPool); err != nil {
		return err
	}

	// Çekirdek migration'ları modül migration'larından ÖNCE uygulanır; sepet
	// akışları workflow motorunu kullanmasa da kurulum sırası üretimdekiyle
	// aynı kalmalıdır.
	if err := db.Migrate(ctx, testDSN, pgstore.Migrations(), pgstore.MigrationOwner); err != nil {
		return err
	}

	baglar = link.New(testPool, nil)
	if err := kap.Provide(svcLink, baglar); err != nil {
		return err
	}
	if err := kap.Provide(svcQuery, query.New(baglar, kap, nil)); err != nil {
		return err
	}
	if err := kap.Provide(svcEventBus, eventbus.NewInMemory(nil)); err != nil {
		return err
	}

	kayit := module.NewRegistry(nil, func(ctx context.Context, src fs.FS, owner string) error {
		return db.Migrate(ctx, testDSN, src, owner)
	})
	// Modül kümesi cmd/server/main.go'dakinin aynısıdır. inventory sepet
	// akışlarında kullanılmaz ama listede DURUR: kurulumun tamamı sınanmalıdır
	// ve bir modülü test için ayıklamak, üretimde ancak açılışta görülecek bir
	// çakışmayı testten gizlerdi.
	kayit.Add(productmod.New())
	kayit.Add(pricingmod.New(nil))
	kayit.Add(inventorymod.New())
	kayit.Add(regionmod.New(nil))
	kayit.Add(customermod.New(nil))
	kayit.Add(cartmod.New())
	if err := kayit.Bootstrap(ctx, kap, chi.NewRouter()); err != nil {
		return err
	}

	if err := modulServisleriniCoz(); err != nil {
		return err
	}

	var kurulumErr error
	if akislar, kurulumErr = sepetAkislariniKur(); kurulumErr != nil {
		return fmt.Errorf("sepet akışları kurulamadı: %w", kurulumErr)
	}

	return bolgeFiksturleriniKur(ctx)
}

// modulServisleriniCoz fikstürlerin kullanacağı modül servislerini container'dan
// ADLA çözer.
//
// Servisler modül nesnelerinden (örn. cartmod.Module.Service()) DEĞİL,
// container'dan alınır: testin kullandığı servis, akışların kullandığı servisin
// TA KENDİSİ olmalıdır. İki ayrı örnek olsaydı test kendi yazdığını okur ve
// akışın gerçekten aynı sepete dokunduğunu kanıtlayamazdı.
func modulServisleriniCoz() error {
	var err error
	if urunSvc, err = container.Resolve[*productsvc.Service](kap, productmod.ServiceName); err != nil {
		return err
	}
	if fiyatSvc, err = container.Resolve[*pricingsvc.Service](kap, pricingmod.ServiceName); err != nil {
		return err
	}
	if bolgeSvc, err = container.Resolve[*regionsvc.Service](kap, regionmod.ServiceName); err != nil {
		return err
	}
	if musteriSvc, err = container.Resolve[*customersvc.Service](kap, customermod.ServiceName); err != nil {
		return err
	}
	sepetSvc, err = container.Resolve[*cartsvc.Service](kap, cartmod.ServiceName)
	return err
}

// sepetAkislariniKur sepet akışlarını ÜRETİM kablolamasıyla kurar.
//
// [cartwf.FromContainer] altı yüzeyi de container'dan adla çözer; cart tarafı
// "cart.interop" adıyla kayıtlı ilkel yüzeydir (ADR 0006). Testte hiçbir köprü
// ya da sahte yoktur: burada bir uyumsuzluk çıkarsa üretimde de çıkar.
func sepetAkislariniKur() (*cartwf.Workflows, error) {
	return cartwf.FromContainer(kap)
}

// bolgeFiksturleriniKur senaryoların paylaştığı iki bölgeyi hazırlar.
//
// Bölgeler TestMain'de bir kez kurulur çünkü bir ülke aynı anda tek bir bölgeye
// bağlanabilir; test başına yeniden kurmak ikinci çağrıda çakışırdı.
func bolgeFiksturleriniKur(ctx context.Context) error {
	vergili, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Vergili Bölge",
		CurrencyCode:   vergiliParaBirimi,
		AutomaticTaxes: true,
		TaxRate:        vergiOraniBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, vergili.ID, vergiliUlke); err != nil {
		return err
	}
	vergiliBolgeID = vergili.ID

	vergisiz, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Vergisiz Bölge",
		CurrencyCode:   vergisizParaBirimi,
		AutomaticTaxes: false,
		TaxRate:        vergisizOranBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, vergisiz.ID, vergisizUlke); err != nil {
		return err
	}
	vergisizBolgeID = vergisiz.ID

	return nil
}
