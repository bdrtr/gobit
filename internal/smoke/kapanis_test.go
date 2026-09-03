//go:build smoke

package smoke

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kapanisSuresi senaryonun sürece tanıdığı SHUTDOWN_TIMEOUT değeridir.
//
// Varsayılandan (15s) kısadır ve bilinçlidir: iddia "makul sürede kapandı"
// değil, "kendi ilan ettiği süreden ÖNCE kapandı"dır. Kısa bir sınır, düzgün
// kapanış yolunun gerçekten işlediğini gösterir; asılan bir süreç ise testi
// daha çabuk düşürür.
const kapanisSuresi = 10 * time.Second

// kapanisPayi sürecin bitmesi için sınırın ÜSTÜNE tanınan gözlem payıdır.
//
// Sınır kadar beklemek yetmezdi: tam sınırda biten bir süreçle hiç bitmeyen
// bir süreç aynı görünür ve test "bitmedi" diyerek gerçek arızayı ("geç
// bitti") gizlerdi. Pay, ikisini ayırt edilebilir kılar.
const kapanisPayi = 10 * time.Second

// TestSigtermDuzgunKapanisYapar E senaryosudur: SIGTERM alan süreç kendi
// SHUTDOWN_TIMEOUT'undan önce ve ÇIKIŞ KODU SIFIR ile kapanır.
//
// # Neden gerçek sinyal
//
// Kapanış yolunun tamamı süreç seviyesindedir: signal.NotifyContext bağlamı
// iptal eder, HTTP sunucusu açık istekleri bekler, container servisleri ters
// sırada kapanır ve izleme dışa aktarıcıları bekleyen span'ları gönderir.
// Hiçbiri httptest ile sürülen bir router'da çalışmaz; internal/e2e bu yüzden
// kapanış hakkında hiçbir şey söyleyemez.
//
// # Neden çıkış kodu
//
// Sıfırdan farklı bir kod, orkestratör için BAŞARISIZ bir kapanıştır: docker
// stop ve Kubernetes bunu konteyner hatası sayar, yeniden başlatma sayacını
// artırır ve düzgün kapanan bir sürüm bile "crash-loop" gibi görünür.
//
// # Neden süre
//
// SIGKILL'e düşen bir süreç açık istekleri ortada bırakır. Kubernetes
// terminationGracePeriodSeconds dolduğunda SIGKILL gönderir; uygulamanın kendi
// sınırından önce kapanması, o noktaya hiç gelinmediğinin kanıtıdır.
func TestSigtermDuzgunKapanisYapar(t *testing.T) {
	ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
	ayar["SHUTDOWN_TIMEOUT"] = kapanisSuresi.String()

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	// Kapanıştan önce sürecin gerçekten hizmet verdiği doğrulanır: hiç istek
	// almamış bir süreci kapatmak, "açık bağlantılar bekleniyor" yolunun hiç
	// çalışmadığı kolay bir durumdur.
	kod, govde := s.iste(http.MethodGet, "/health", "")
	require.Equal(t, http.StatusOK, kod, "kapanıştan önce süreç sağlıklı olmalı; gövde: %s", govde)

	baslangic := time.Now()
	s.sigterm()

	cikisKodu, bitti := s.cikisiBekle(kapanisSuresi + kapanisPayi)
	gecen := time.Since(baslangic)

	require.True(t, bitti,
		"süreç SIGTERM'den sonra %s içinde kapanmadı; SIGKILL gerekiyor demektir\n%s",
		kapanisSuresi+kapanisPayi, s.gunluk())

	assert.Equal(t, 0, cikisKodu,
		"düzgün kapanış sıfır çıkış kodu vermeli; orkestratör aksini hata sayar\n%s", s.gunluk())
	assert.Less(t, gecen, kapanisSuresi,
		"kapanış kendi ilan ettiği süreden (%s) önce bitmeli, %s sürdü\n%s",
		kapanisSuresi, gecen, s.gunluk())

	// Log, kapanışın SİNYALLE geldiğini söylemeli: sıfır kodla ve hızla biten
	// bir süreç, sinyali hiç görmeden main'den düşmüş de olabilirdi.
	assert.True(t, s.gunlukIceriyorMu("the shutdown signal arrived"),
		"süreç SIGTERM'i görmeli\n%s", s.gunluk())
	assert.True(t, s.gunlukIceriyorMu("the HTTP server is closed"),
		"kapanış düzgün yoldan tamamlanmalı\n%s", s.gunluk())
}
