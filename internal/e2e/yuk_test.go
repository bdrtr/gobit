//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Bu dosya planın Faz 9 DoD'sindeki "temel yük testi geçiyor" maddesini
// karşılar.
//
// # Ne ölçüyor, ne ölçmüyor
//
// ÖLÇTÜĞÜ: eşzamanlı istek altında sistemin DOĞRU kalması — hiçbir istek
// düşmemeli, hiçbiri 5xx dönmemeli, koruma yığını yarış üretmemeli ve
// gecikme makul bir tavanın altında kalmalı.
//
// ÖLÇMEDİĞİ: mutlak performans. Ölçüm süreç içinde (httptest) yapılır, ağ ve
// çekirdek yığını yoktur; çıkan sayılar bir kapasite planı DEĞİLDİR. Eşik de
// bu yüzden cömerttir: amaç kilitlenmeyi, havuz tükenmesini ve N+1
// patlamasını yakalamaktır, milisaniye kovalamak değil.
//
// Parametreler ortamdan ayarlanabilir; varsayılanlar CI'ı yavaşlatmayacak
// kadar küçüktür:
//
//	GOBIT_LOAD_REQUESTS=5000 GOBIT_LOAD_CONCURRENCY=32 make test-integration

// Yük testinin varsayılan parametreleri.
const (
	// varsayilanIstek toplam istek sayısıdır.
	varsayilanIstek = 1000
	// varsayilanEszaman eşzamanlı işçi sayısıdır.
	varsayilanEszaman = 16
	// p99Tavani kabul edilen en yüksek 99. yüzdelik gecikmedir.
	//
	// Süreç içi bir çağrı için son derece cömerttir; bu bilinçlidir. Dar bir
	// eşik, yavaş bir CI makinesinde gerçek bir gerileme olmadan kırmızı
	// yanar ve testin güvenilirliğini yok ederdi.
	p99Tavani = 2 * time.Second
)

// TestTemelYukAltindaDogruKalir eşzamanlı yük altında hiçbir isteğin
// düşmediğini doğrular.
//
// Yol MAĞAZA yüzeyidir ve publishable anahtarla çağrılır: koruma yığınının
// tamamı (hız sınırı -> kimlik -> idempotency) her istekte çalışır, yani yük
// yalnızca handler'ı değil sertleştirme katmanını da sınar. Yığındaki
// paylaşılan durum (jeton kovaları, idempotency haritası) yarış üretseydi
// -race ile burada görünürdü.
func TestTemelYukAltindaDogruKalir(t *testing.T) {
	istekSayisi := ortamSayisi(t, "GOBIT_LOAD_REQUESTS", varsayilanIstek)
	eszaman := ortamSayisi(t, "GOBIT_LOAD_CONCURRENCY", varsayilanEszaman)
	require.Positive(t, eszaman, "eşzamanlılık pozitif olmalı")

	var (
		mu        sync.Mutex
		sureler   = make([]time.Duration, 0, istekSayisi)
		basarisiz atomic.Int64
		sunucu5xx atomic.Int64
	)

	is := make(chan int, istekSayisi)
	for i := range istekSayisi {
		is <- i
	}
	close(is)

	basla := time.Now()

	var wg sync.WaitGroup

	for range eszaman {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range is {
				istek := httptest.NewRequest(http.MethodGet, "/store/v1/products?limit=10", http.NoBody)
				istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)

				kayit := httptest.NewRecorder()

				istekBasi := time.Now()
				testRouter.ServeHTTP(kayit, istek)
				gecen := time.Since(istekBasi)

				switch {
				case kayit.Code >= http.StatusInternalServerError:
					sunucu5xx.Add(1)
				case kayit.Code != http.StatusOK:
					basarisiz.Add(1)
				}

				mu.Lock()
				sureler = append(sureler, gecen)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	toplamSure := time.Since(basla)
	require.Len(t, sureler, istekSayisi, "her istek tamamlanmalı")

	slices.Sort(sureler)
	p50 := yuzdelik(sureler, 50)
	p99 := yuzdelik(sureler, 99)

	t.Logf("yük: %d istek / %d eşzamanlı, süre %s, %.0f istek/sn, p50 %s, p99 %s",
		istekSayisi, eszaman, toplamSure.Round(time.Millisecond),
		float64(istekSayisi)/toplamSure.Seconds(),
		p50.Round(time.Microsecond), p99.Round(time.Microsecond))

	assert.Zero(t, sunucu5xx.Load(), "yük altında sunucu hatası olmamalı")
	assert.Zero(t, basarisiz.Load(), "yük altında hiçbir istek reddedilmemeli")
	assert.Less(t, p99, p99Tavani, "p99 gecikme tavanın altında kalmalı")
}

// yuzdelik SIRALI süre diliminden verilen yüzdeliği döner.
//
// En yakın sıra (nearest-rank) yöntemi kullanılır: küçük örneklemlerde
// enterpolasyon, gerçekte ölçülmemiş bir değeri raporlardı.
func yuzdelik(sirali []time.Duration, yuzde int) time.Duration {
	if len(sirali) == 0 {
		return 0
	}

	sira := (yuzde*len(sirali) + 99) / 100
	if sira < 1 {
		sira = 1
	}

	if sira > len(sirali) {
		sira = len(sirali)
	}

	return sirali[sira-1]
}

// ortamSayisi bir ortam değişkenini pozitif tam sayı olarak okur.
//
// Geçersiz değer sessizce varsayılana DÜŞMEZ: "GOBIT_LOAD_REQUESTS=çok" yazan
// birine testin varsayılanla koştuğunu söylememek, ölçtüğünü sandığı şeyi
// ölçmediğini gizlerdi.
func ortamSayisi(t *testing.T, ad string, varsayilan int) int {
	t.Helper()

	ham := os.Getenv(ad)
	if ham == "" {
		return varsayilan
	}

	deger, err := strconv.Atoi(ham)
	require.NoError(t, err, "%s sayı olmalı, %q verildi", ad, ham)
	require.Positive(t, deger, "%s pozitif olmalı", ad)

	return deger
}
