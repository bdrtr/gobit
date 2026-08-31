//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri servisin KARARLARINI sahte bir depoyla kanıtlar. Buradaki
// testler kararların dayandığı ZEMİNİ kanıtlar: idempotency'nin sahte bir
// haritada değil GERÇEK bir benzersiz indekste durduğunu, eşzamanlı iki
// işleyicinin tek bildirim ürettiğini, kaydın alıcı adresini hiçbir sütunda
// TAŞIMADIĞINI ve abonenin gerçek bir olay veri yolu üzerinden tetiklenip
// e-postayı olaydan değil kayıttan okuduğunu.
package notification_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/repository"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

const postgresImage = "postgres:16-alpine"

// Test verisinde kullanılan sabitler. Referans BAŞKA bir modüle (siparişe)
// aittir; bu modül varlığını doğrulamaz (Prensip 2.2).
const (
	testEposta   = "musteri@example.com"
	testSablon   = service.TemplateOrderPlaced
	testProvider = "test"
)

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
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
	// Eşzamanlılık testi onlarca goroutine'i aynı anda koşturur; havuz
	// varsayılandan geniş açılır.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN,
		notification.New(notification.Options{}).Migrations(), notification.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// sahteSaglayici gönderilenleri sayan bir bildirim sağlayıcısıdır.
type sahteSaglayici struct {
	mu         sync.Mutex
	gonderilen []coreprovider.Notification
	err        error
}

func (p *sahteSaglayici) ID() string { return testProvider }

func (p *sahteSaglayici) Send(_ context.Context, n coreprovider.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.gonderilen = append(p.gonderilen, n)

	return p.err
}

func (p *sahteSaglayici) sayi() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.gonderilen)
}

func (p *sahteSaglayici) son() coreprovider.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.gonderilen) == 0 {
		return coreprovider.Notification{}
	}

	return p.gonderilen[len(p.gonderilen)-1]
}

// sahteSiparis "order.interop" yüzeyinin yerine geçer.
//
// Gövde DİZE olarak verilir: order modülü import EDİLEMEZ (Prensip 2.4) ve iki
// tarafın paylaştığı tek şey JSON şemasıdır. Şemanın gerçekten uyuştuğu,
// order modülünün kendi entegrasyon testinde de sınanır — bu testlerin
// ikisi birlikte, derleyicinin kuramadığı bağı kurar.
type sahteSiparis struct {
	mu      sync.Mutex
	govde   string
	cagri   int
	istenen string
}

func (s *sahteSiparis) OrderContactJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cagri++
	s.istenen = orderID

	return json.RawMessage(s.govde), nil
}

// siparisGovdesi verilen kimlik ve adresle bir iletişim yanıtı üretir.
func siparisGovdesi(orderID, email string) string {
	return fmt.Sprintf(`{"order_id":%q,"display_id":"1042","email":%q,`+
		`"currency_code":"TRY","total":"6100","item_count":"2"}`, orderID, email)
}

// yeniServis GERÇEK depo ve verilen sağlayıcı üzerinde çalışan bir servis
// kurar.
func yeniServis(t *testing.T, prov coreprovider.NotificationProvider, siparis service.OrderContactReader) *service.Service {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store:      repository.New(testPool.Pool()),
		Providers:  registry,
		ProviderID: prov.ID(),
		Contacts:   siparis,
	})
	require.NoError(t, err)

	return svc
}

// benzersizReferans testler arası çakışmayı önleyen bir sipariş kimliği üretir.
//
// Testler AYNI tabloyu paylaşır ve idempotency anahtarı (şablon, referans)
// çiftidir; sabit bir referans, ikinci testin ilk testin kaydına takılması
// demek olurdu.
func benzersizReferans(t *testing.T) string {
	t.Helper()

	return "order_" + models.NewDeliveryID(time.Now())
}

// TestGunlukAyniSablonVeReferansiIkinciKezGONDERMEZ idempotency'nin GERÇEK
// benzersiz indekse dayandığını doğrular.
func TestGunlukAyniSablonVeReferansiIkinciKezGONDERMEZ(t *testing.T) {
	prov := &sahteSaglayici{}
	svc := yeniServis(t, prov, &sahteSiparis{})
	ctx := context.Background()
	referans := benzersizReferans(t)

	girdi := service.NotifyInput{
		Template:  testSablon,
		Channel:   coreprovider.ChannelEmail,
		Reference: referans,
		To:        testEposta,
		Data:      map[string]string{"order_id": referans},
	}

	require.NoError(t, svc.Notify(ctx, girdi))
	require.NoError(t, svc.Notify(ctx, girdi), "ikinci çağrı hata değil, sessiz atlama olmalı")

	assert.Equal(t, 1, prov.sayi(), "sağlayıcıya YALNIZCA bir kez gidilmeli")

	kayitlar, toplam, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &referans})
	require.NoError(t, err)
	assert.Equal(t, int64(1), toplam)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliverySent, kayitlar[0].Status)
}

// TestEszamanliIkiGonderimTekBildirimUretir yarışı GERÇEK satırlar üzerinde
// sınar.
//
// Sahte depo bunu haritayla ve tek kilitle sağlar; burada kazananı PostgreSQL
// seçer. "Önce oku, yoksa yaz" yazılsaydı bu test iki bildirim görürdü —
// müşteri iki e-posta alırdı.
func TestEszamanliIkiGonderimTekBildirimUretir(t *testing.T) {
	prov := &sahteSaglayici{}
	svc := yeniServis(t, prov, &sahteSiparis{})
	ctx := context.Background()
	referans := benzersizReferans(t)

	const esZamanli = 8
	var wg sync.WaitGroup
	hatalar := make(chan error, esZamanli)

	for range esZamanli {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hatalar <- svc.Notify(ctx, service.NotifyInput{
				Template:  testSablon,
				Channel:   coreprovider.ChannelEmail,
				Reference: referans,
				To:        testEposta,
			})
		}()
	}
	wg.Wait()
	close(hatalar)

	for err := range hatalar {
		require.NoError(t, err, "eşzamanlı çağrıların hiçbiri hata dönmemeli")
	}

	assert.Equal(t, 1, prov.sayi(), "eşzamanlı %d çağrıdan yalnızca biri gönderim yapmalı", esZamanli)

	_, toplam, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &referans})
	require.NoError(t, err)
	assert.Equal(t, int64(1), toplam, "günlükte tek kayıt olmalı")
}

// TestSaglayiciHatasiGunlugeBASARISIZYazar hatanın hem kayda yazıldığını hem
// çağırana döndüğünü doğrular.
func TestSaglayiciHatasiGunlugeBASARISIZYazar(t *testing.T) {
	prov := &sahteSaglayici{err: coreerrors.Unavailable("smtp_down", "sağlayıcıya ulaşılamadı")}
	svc := yeniServis(t, prov, &sahteSiparis{})
	ctx := context.Background()
	referans := benzersizReferans(t)

	err := svc.Notify(ctx, service.NotifyInput{
		Template:  testSablon,
		Channel:   coreprovider.ChannelEmail,
		Reference: referans,
		To:        testEposta,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeSendFailed, coreerrors.CodeOf(err))

	kayitlar, _, listErr := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &referans})
	require.NoError(t, listErr)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliveryFailed, kayitlar[0].Status)
	assert.Contains(t, kayitlar[0].Error, "sağlayıcıya ulaşılamadı")

	// Durum süzgeci gerçek sorguda da çalışmalı; "başarısız bildirimleri
	// göster", günlüğün ikinci en sık sorusudur.
	durum := models.DeliveryFailed.String()
	basarisizlar, _, listErr := svc.ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &referans, Status: &durum})
	require.NoError(t, listErr)
	assert.Len(t, basarisizlar, 1)
}

// TestGunlukAliciAdresiSUTUNUTASIMAZ kaydın kişisel veri taşımadığını ŞEMA
// düzeyinde doğrular.
//
// Kod düzeyinde bir iddia yetmezdi: bugün yazmayan bir kod, sütun var olduğu
// sürece yarın yazabilir. Sütunun hiç olmaması, KVKK/GDPR silme talebinde
// temizlenecek yerlerin sayısını sabit tutar.
func TestGunlukAliciAdresiSUTUNUTASIMAZ(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = 'notification_deliveries'`)
	require.NoError(t, err)
	defer rows.Close()

	var sutunlar []string
	for rows.Next() {
		var ad string
		require.NoError(t, rows.Scan(&ad))
		sutunlar = append(sutunlar, ad)
	}
	require.NoError(t, rows.Err())

	assert.ElementsMatch(t, []string{
		"id", "template", "channel", "reference", "provider_id",
		"status", "error", "created_at", "updated_at",
	}, sutunlar, "günlükte alıcı adresi tutacak bir sütun BULUNMAMALI")
}

// TestAbonelikOlaydanDegilKayittanOkur bildirim yolunu UÇTAN UCA kurar.
//
// Gerçek olan her şey gerçektir: gerçek veritabanı, gerçek olay veri yolu,
// modülün kendi Register'ı ve container'dan ADLA çözülen sipariş yüzeyi. Test
// üç iddiayı birden sabitler:
//
//  1. Modül "order.placed" olayına GERÇEKTEN abone olur (Register'da).
//  2. Olay yükü E-POSTA TAŞIMAZ; abone adresi "order.interop" üzerinden
//     kayıttan okur.
//  3. Eklentiden gelen bir sağlayıcı, modül Register EDİLDİKTEN SONRA
//     kaydedilse bile kullanılır — çünkü çözüm gönderim anında yapılır.
//     coreplugin.Registry'nin iki fazlı yapısı tam olarak buna dayanır.
func TestAbonelikOlaydanDegilKayittanOkur(t *testing.T) {
	ctx := context.Background()
	referans := benzersizReferans(t)

	bus := eventbus.NewInMemory(nil)
	siparis := &sahteSiparis{govde: siparisGovdesi(referans, testEposta)}

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide(service.OrderInteropName, siparis))

	mod := notification.New(notification.Options{ProviderID: testProvider})
	require.NoError(t, mod.Register(ctx, c))

	// Sağlayıcı Register'dan SONRA eklenir; eklenti sistemi de tam olarak
	// bunu yapar (kayıtlar modüller ayağa kalktıktan sonra uygulanır).
	prov := &sahteSaglayici{}
	require.NoError(t, mod.Providers().Register(prov))

	// Olayın yükü, order modülünün yayımladığıyla AYNI şekildedir ve e-posta
	// İÇERMEZ; testin dayanağı budur.
	require.NoError(t, bus.Publish(ctx, eventbus.Event{
		Name: service.EventOrderPlaced,
		Data: map[string]any{
			"order_id":      referans,
			"display_id":    "1042",
			"status":        "pending",
			"currency_code": "TRY",
			"total":         "6100",
			"item_count":    "2",
		},
	}))

	// Shutdown çalışan işleyicilerin bitmesini bekler; yoklama döngüsü yerine
	// veri yolunun kendi sözleşmesi kullanılır.
	require.NoError(t, bus.Shutdown(ctx))

	require.Equal(t, 1, prov.sayi(), "abone tetiklenmeli ve sağlayıcıya gitmeli")
	gonderilen := prov.son()
	assert.Equal(t, testEposta, gonderilen.To, "adres KAYITTAN okunmalı")
	assert.Equal(t, coreprovider.ChannelEmail, gonderilen.Channel)
	assert.Equal(t, testSablon, gonderilen.Template)
	assert.Equal(t, referans, gonderilen.Data["order_id"])
	assert.Equal(t, "6100", gonderilen.Data["total"], "tutar dize olarak taşınmalı")

	require.Equal(t, 1, siparis.cagri, "sipariş kaydı okunmalı")
	assert.Equal(t, referans, siparis.istenen)

	kayitlar, _, err := mod.Service().ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &referans})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, models.DeliverySent, kayitlar[0].Status)
	assert.Equal(t, testProvider, kayitlar[0].ProviderID)
}

// TestModulVarsayilanSaglayiciylaKaydolur kutudan çıkan kurulumun eksiksiz
// olduğunu doğrular: hiçbir eklenti yokken de bir sağlayıcı vardır ve seçili
// kimlik onu bulur.
func TestModulVarsayilanSaglayiciylaKaydolur(t *testing.T) {
	ctx := context.Background()

	bus := eventbus.NewInMemory(nil)
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))

	mod := notification.New(notification.Options{})
	require.NoError(t, mod.Register(ctx, c))
	t.Cleanup(func() { require.NoError(t, bus.Shutdown(ctx)) })

	assert.Equal(t, []string{logonly.ID}, mod.Providers().IDs())
	assert.Equal(t, notification.DefaultProviderID, mod.Service().ProviderID())

	kayit, err := container.Resolve[*service.ProviderRegistry](c, notification.ProvidersName)
	require.NoError(t, err, "sağlayıcı kaydı container'da %q adıyla bulunmalı",
		notification.ProvidersName)
	assert.Same(t, mod.Providers(), kayit)
}
