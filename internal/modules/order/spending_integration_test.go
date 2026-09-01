//go:build integration

// Bu dosyadaki testler harcama limitinin ZEMİNİNİ kanıtlar: toplamı hesaplayan
// SQL'in gerçekten pencereyi süzdüğünü, iptal edilen siparişi saymadığını,
// iadeyi düştüğünü ve — asıl önemlisi — danışma kilidinin eşzamanlı iki
// siparişin limiti BİRLİKTE aşmasını gerçekten engellediğini.
//
// Birim testleri (service/spending_test.go) servisin KARARLARINI sahte bir depo
// ile kanıtlar; sahte, toplamın kurallarını taklit eder. Taklidin gerçekle
// örtüştüğü ve kilidin gerçekten serileştirdiği ancak burada, gerçek bir
// PostgreSQL üzerinde ve gerçek goroutine'lerle görülebilir.
package order_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// sabitKural her çağrıda aynı harcama kuralını dönen sağlayıcıdır.
//
// Gerçek sağlayıcı b2b modülüdür ve bu paket onu import EDEMEZ (Prensip 2.4);
// burada taklit edilen tek şey sınırın JSON ŞEMASIDIR. Şemanın iki tarafta
// aynı olduğu bu testlerle kanıtlanamaz — b2b tarafındaki karşılığı için bkz.
// internal/modules/b2b/service/interop_test.go.
type sabitKural struct {
	payload json.RawMessage
}

// SpendingLimitJSON kurulmuş kuralı döner.
func (k sabitKural) SpendingLimitJSON(context.Context, string) (json.RawMessage, error) {
	return k.payload, nil
}

// limitKurali verilen limit ve pencereyle bir kural gövdesi üretir.
func limitKurali(t *testing.T, limit int64, window *time.Time) json.RawMessage {
	t.Helper()

	govde := map[string]any{
		"limited":        true,
		"spending_limit": limit,
		"currency_code":  testCurrency,
		"window_start":   "",
	}
	if window != nil {
		govde["window_start"] = window.UTC().Format(time.RFC3339)
	}
	payload, err := json.Marshal(govde)
	require.NoError(t, err)
	return payload
}

// limitliServis harcama kuralı bağlı, gerçek depo üzerinde çalışan bir servis
// kurar.
func limitliServis(t *testing.T, kural json.RawMessage) *service.Service {
	t.Helper()

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		kapatmaCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(kapatmaCtx); err != nil {
			t.Logf("olay veri yolu kapatılamadı: %v", err)
		}
	})

	svc, err := service.New(service.Options{
		Repo:     repository.New(testPool.Pool()),
		Links:    testLinks,
		Events:   bus,
		Spending: sabitKural{payload: kural},
	})
	require.NoError(t, err)
	return svc
}

// gecmisSiparisYaz depoya DOĞRUDAN bir sipariş yazar ve placed_at'ini
// çağıranın verdiği ana sabitler.
//
// Servis üzerinden yazmak mümkün değildir: placed_at'i veritabanı now() ile
// damgalar ve pencerenin DIŞINDA kalan bir sipariş başka türlü kurulamaz.
// Sınanan kural yine servisin kuralıdır; burada kurulan yalnızca GEÇMİŞTİR.
func gecmisSiparisYaz(ctx context.Context, t *testing.T, customerID string, total int64, placedAt time.Time) string {
	t.Helper()

	id := models.NewOrderID()
	_, err := testPool.Pool().Exec(ctx, `
        INSERT INTO orders (
            id, status, region_id, customer_id, currency_code,
            subtotal, discount_total, tax_total, shipping_total, total, placed_at
        ) VALUES ($1, 'pending', $2, $3, $4, $5, 0, 0, 0, $5, $6)`,
		id, testRegionID, customerID, testCurrency, total, placedAt.UTC())
	require.NoError(t, err)
	return id
}

// limitliGirdi verilen müşteri için 6100 tutarında bir sipariş girdisi üretir.
func limitliGirdi(customerID string) service.CreateOrderInput {
	girdi := gecerliGirdi()
	girdi.CustomerID = customerID
	return girdi
}

// TestPencereDisindakiHarcamaGercektenSayilmaz dönemin SQL'de uygulandığını
// doğrular.
//
// Pencereden bir gün önce verilmiş 50000'lik sipariş, limiti 10000 olan
// çalışanı engellememelidir; pencerenin İÇİNDEKİ 5000 ise 6100'lük yeni
// siparişle birlikte limiti aşırır. İki iddia aynı testte durur çünkü ikisi
// aynı sorgunun iki dalıdır: yalnızca birini sınamak, süzgecin hiç
// uygulanmadığı hâli de geçirirdi.
func TestPencereDisindakiHarcamaGercektenSayilmaz(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_PENCERE"
	pencere := time.Now().UTC().Truncate(time.Hour)

	gecmisSiparisYaz(ctx, t, musteri, 50_000, pencere.Add(-24*time.Hour))
	svc := limitliServis(t, limitKurali(t, 10_000, &pencere))

	// Pencere dışındaki 50000 sayılmadığı için sipariş GEÇER.
	_, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.NoError(t, err, "önceki dönemin harcaması bu dönemin bütçesini yakmamalı")

	// Şimdi pencere İÇİNDE 6100 harcanmış oldu; ikinci sipariş limiti aşırır.
	_, err = svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestPencereYokkaTumGecmisSayilir "never" periyodunun SQL karşılığını
// doğrular.
//
// window_start boş geldiğinde süzgeç hiç uygulanmaz ve yıllar önceki bir
// sipariş de toplama girer.
func TestPencereYokkaTumGecmisSayilir(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_PENCERESIZ"

	gecmisSiparisYaz(ctx, t, musteri, 5000, time.Date(2019, time.March, 3, 0, 0, 0, 0, time.UTC))
	svc := limitliServis(t, limitKurali(t, 10_000, nil))

	_, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.Error(t, err)
	assert.Equal(t, service.CodeSpendingLimitExceeded, errors.CodeOf(err))
}

// TestIptalEdilenSiparisHarcamayaGirmez iptalin bütçeyi GERÇEKTEN geri
// verdiğini doğrular.
//
// Sipariş servis üzerinden iptal edilir (status = 'canceled'), yani sınanan şey
// sorgunun durum süzgecidir. İptal edilmiş bir sipariş bütçeyi tutsaydı, ödemesi
// reddedilen her deneme çalışanın dönem hakkını kalıcı olarak yakardı — saga
// başarısız bir denemeden sonra siparişi tam olarak böyle iptal eder.
func TestIptalEdilenSiparisHarcamayaGirmez(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_IPTAL"
	pencere := time.Now().UTC().Add(-time.Hour)
	svc := limitliServis(t, limitKurali(t, 10_000, &pencere))

	ilk, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.NoError(t, err)

	// İptalden ÖNCE ikinci sipariş limiti aşar.
	_, err = svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.Error(t, err)

	require.NoError(t, svc.CancelOrder(ctx, ilk.ID, "ödeme reddedildi"))

	_, err = svc.CreateOrder(ctx, limitliGirdi(musteri))
	assert.NoError(t, err, "iptal edilen sipariş bütçeyi bırakmalı")
}

// TestIadeEdilenTutarGercektenDusulur iadenin bütçeye geri döndüğünü
// doğrular.
//
// Sorgunun LEFT JOIN'i yalnızca burada, gerçek bir order_summaries satırıyla
// sınanabilir: özeti olmayan siparişte iade sıfır sayılmalı, olan siparişte ise
// düşülmelidir.
func TestIadeEdilenTutarGercektenDusulur(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_IADE"
	pencere := time.Now().UTC().Add(-time.Hour)
	svc := limitliServis(t, limitKurali(t, 10_000, &pencere))

	ilk, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
	require.NoError(t, err)

	_, err = svc.SetOrderSummaryTotals(ctx, ilk.ID, service.SummaryTotalsInput{
		PaidTotal:     6100,
		RefundedTotal: 6100,
	})
	require.NoError(t, err)

	_, err = svc.CreateOrder(ctx, limitliGirdi(musteri))
	assert.NoError(t, err, "tamamı iade edilmiş sipariş bütçeyi tutmamalı")
}

// TestEszamanliSiparislerLimitiBirlikteAsamaz kontrol ile yazma arasındaki
// YARIŞIN kapandığını doğrular.
//
// Limit tam olarak BİR siparişe yeter (6100 <= 10000 < 12200). Sekiz goroutine
// aynı anda sipariş açmaya çalışır; kilit olmasaydı hepsi toplamı 0 okur,
// hepsi limitin altında görür ve hepsi yazılırdı — klasik write skew. Kilit
// altında yalnızca BİRİ geçmeli, geri kalanı limit aşımıyla düşmelidir.
//
// Bu iddia sahte bir depoyla kurulamaz: serileştirmeyi yapan şey PostgreSQL'in
// pg_advisory_xact_lock'ıdır ve ancak gerçek işlemlerle görülür.
func TestEszamanliSiparislerLimitiBirlikteAsamaz(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_YARIS"
	pencere := time.Now().UTC().Add(-time.Hour)
	svc := limitliServis(t, limitKurali(t, 10_000, &pencere))

	const deneme = 8
	var basarili, asim atomic.Int64
	var wg sync.WaitGroup
	baslat := make(chan struct{})

	wg.Add(deneme)
	for range deneme {
		go func() {
			defer wg.Done()
			<-baslat

			_, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
			switch {
			case err == nil:
				basarili.Add(1)
			case errors.CodeOf(err) == service.CodeSpendingLimitExceeded:
				asim.Add(1)
			default:
				t.Errorf("beklenmeyen hata: %v", err)
			}
		}()
	}
	close(baslat)
	wg.Wait()

	assert.Equal(t, int64(1), basarili.Load(), "limit yalnızca BİR siparişe yetmeli")
	assert.Equal(t, int64(deneme-1), asim.Load(), "geri kalanı limit aşımıyla düşmeli")

	// Veritabanındaki gerçek toplam da limiti aşmamalı.
	var toplam int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT COALESCE(SUM(total), 0) FROM orders
         WHERE customer_id = $1 AND deleted_at IS NULL AND status <> 'canceled'`,
		musteri).Scan(&toplam))
	assert.LessOrEqual(t, toplam, int64(10_000))
}

// TestHarcamaKilidiFarkliMusterileriBEKLETMEZ kilidin müşteri BAŞINA
// alındığını doğrular.
//
// Kilit tüm siparişleri serileştirseydi kural doğru uygulanırdı ama sipariş
// açma yolu tek şeritli olurdu. İki farklı müşterinin eşzamanlı siparişleri bu
// yüzden ikisi de geçmelidir.
func TestHarcamaKilidiFarkliMusterileriBEKLETMEZ(t *testing.T) {
	ctx := context.Background()
	pencere := time.Now().UTC().Add(-time.Hour)
	svc := limitliServis(t, limitKurali(t, 10_000, &pencere))

	var wg sync.WaitGroup
	hatalar := make([]error, 2)
	musteriler := []string{"cus_AYRI_A", "cus_AYRI_B"}

	wg.Add(len(musteriler))
	for i, musteri := range musteriler {
		go func() {
			defer wg.Done()
			_, hatalar[i] = svc.CreateOrder(ctx, limitliGirdi(musteri))
		}()
	}
	wg.Wait()

	assert.NoError(t, hatalar[0])
	assert.NoError(t, hatalar[1])
}

// TestLimitsizMusteriKilitAlmaz kuralı olmayan siparişte ek maliyet
// olmadığını doğrular.
//
// "limited": false gövdesinde ne kilit alınır ne toplam okunur; kanıtı,
// pencereyi hiç aşmayacak kadar büyük bir geçmişin bile siparişi
// engellememesidir.
func TestLimitsizMusteriKilitAlmaz(t *testing.T) {
	ctx := context.Background()
	musteri := "cus_LIMITSIZ"

	gecmisSiparisYaz(ctx, t, musteri, 1_000_000, time.Now().UTC())
	svc := limitliServis(t, json.RawMessage(`{"limited":false}`))

	_, err := svc.CreateOrder(ctx, limitliGirdi(musteri))
	assert.NoError(t, err)
}
