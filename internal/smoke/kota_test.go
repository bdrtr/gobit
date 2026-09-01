//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bu dosya, hız sınırının HANGİ yolları kapsadığını gerçek süreçte sabitler.
//
// # Neden bir smoke senaryosu
//
// Kapsam bileşim kökünde tek bir satırla verilir (korumaYigini'nin
// GuardOptions.OpenPrefixes alanı) ve o satırdaki bir eksiklik hiçbir şeyi
// düşürmez: uç çalışmaya devam eder, yalnızca kotasız çalışır. Yani arıza tam
// olarak bu deponun tekrar eden sınıfındandır — kural bir yerde tanımlı,
// uygulandığı yer görünmüyor. Kapsamı ancak kotayı GERÇEKTEN doldurup ne
// olduğuna bakan bir koşum kanıtlar.
//
// # Neden iki yön birden sınanıyor
//
// Yalnızca "kotalı mı" sorusu sorulsaydı, her yolu kapsayan bir yığın da testi
// geçerdi — oysa sağlık uçlarının kotaya takılması ORKESTRATÖRÜN sağlıklı bir
// örneği trafikten çekmesi demektir ve bu, kotasızlıktan daha pahalı bir
// arızadır (bkz. corehttp.GuardOptions.OpenPrefixes godoc'u). Bu yüzden test
// hem kapsananı hem kapsanmayanı iddia eder.

// kotaSiniri senaryonun dakika başına izin verdiği istek sayısıdır.
//
// Küçüktür ki sınır birkaç istekte dolsun: amaç sınırlayıcının ALGORİTMASINI
// ölçmek değil (o birim testlerinin işi), kapsamın doğru olduğunu görmek.
const kotaSiniri = 3

// TestKotaKapsamiGercekSurecte hız sınırının kimliksiz uçlardaki kapsamını
// doğrular: /openapi.json kotaya TABİDİR, /health ve /ready DEĞİLDİR.
//
// # /openapi.json neden kotalı
//
// İstemcisi bir kod üreteci ya da IDE'dir ve başlık göndermez, yani uç
// kimliksizdir. Ama bedava değildir: her istek route ağacını gezip önbelleğin
// hâlâ geçerli olduğunu doğrular ve ağaç değiştiğinde tüm modüllerin DTO'ları
// yansımayla yeniden şemaya çevrilir. Kimlik ve kota AYRI kararlardır.
//
// # /health ve /ready neden kotasız
//
// Onları çağıran istemci orkestratördür ve cevabın gecikmesi "bu örnek
// hasta" diye okunur. Kotaya takılan bir sağlık ucu, sağlıklı bir örneği
// trafikten çektirir — yani sınırın kendisi arızayı ÜRETİR.
func TestKotaKapsamiGercekSurecte(t *testing.T) {
	dsn := senaryoVeritabani(t)

	ayar := temelAyarlar(dsn, bosPort(t))
	ayar["RATE_LIMIT_PER_MINUTE"] = "3"

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	t.Run("/openapi.json kotaya tabidir", func(t *testing.T) {
		// İlk istek belgeyi kesinlikle üretebilmeli: 200 gelmezse sınanan şey
		// kota değil, belgenin kendisi olurdu.
		kod, govde := s.iste(http.MethodGet, openAPISmokeYolu, "")
		require.Equal(t, http.StatusOK, kod,
			"belge ucu ilk istekte çalışmalı; gövde: %s", govde)

		assert.True(t, kotayaTakilir(t, s, openAPISmokeYolu),
			"/openapi.json hız sınırına TAKILMALI. Takılmıyorsa yol "+
				"korumaYigini'ndeki OpenPrefixes listesinde değildir ve uç, kimlik "+
				"doğrulama maliyeti bile ödemeden atılabilen bir yük hâline gelir")
	})

	t.Run("sağlık uçları kotasızdır", func(t *testing.T) {
		for _, yol := range []string{"/health", "/ready"} {
			assert.False(t, kotayaTakilir(t, s, yol),
				"%s hız sınırına TAKILMAMALI: onu çağıran orkestratördür ve 429, "+
					"sağlıklı bir örneğin trafikten çekilmesi demektir", yol)
		}
	})
}

// openAPISmokeYolu belge ucunun yoludur.
//
// Yol elle yazılır çünkü sabiti main paketindedir ve main paketi import
// EDİLEMEZ. Ayrışırsa test ilk istekte 404 alır ve teşhis mesajı bunu söyler —
// yani sessizce yanlış bir şeyi sınamaya başlamaz.
const openAPISmokeYolu = "/openapi.json"

// kotayaTakilir verilen yola sınırın üstünde istek atıp 429 görülüp
// görülmediğini bildirir.
//
// Sınırın KAÇ istekte dolduğu iddia EDİLMEZ: kova zaman tabanlıdır ve
// senaryonun kendi ısınma istekleri de sayılır. İddia yalnızca "bu yol sayılıyor
// mu" sorusudur ve o soru tek bir 429 ile kesin olarak yanıtlanır.
func kotayaTakilir(t *testing.T, s *surec, yol string) bool {
	t.Helper()

	for range kotaSiniri * 3 {
		if kod, _ := s.iste(http.MethodGet, yol, ""); kod == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}
