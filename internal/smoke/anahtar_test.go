//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya README'nin KURULUM TUZAĞINI çiviler: publishable anahtar kanalsız da
// üretilir (201) ama mağaza yüzeyinde her zaman 401 alır; kanal sonradan
// bağlandığında AYNI anahtar çalışmaya başlar.
//
// # Neden bir smoke senaryosu
//
// Tuzak, belgeyi izleyen geliştiricinin gerçekten düştüğü yerdir: anahtar üretme
// isteği başarılı görünür, arıza ancak İKİNCİ istekte ve başka bir yüzeyde
// ortaya çıkar. Kararı veren üç parça da ayrı yerlerde durur — kapıyı çekirdeğin
// middleware'i tutar, kanal listesini auth modülünün servisi çözer, ikisini
// bileşim kökü birbirine bağlar. Hiçbir modül testi bu üçünü birden göremez.
//
// # Neden bugüne kadar hiçbir test tutmuyordu
//
// Depodaki hiçbir test auth_no_sales_channel kodunu beklemiyordu; kanalsız
// anahtar yolu ne birim, ne entegrasyon, ne uçtan uca zeminde koşuyordu.
// internal/smoke'un kendi yardımcıları (istemci_test.go) anahtarı HER ZAMAN bir
// kanala bağlı üretir, yani var olan senaryolar tuzağın yanından geçiyordu.
//
// # Teşhis kodu yanıtta DEĞİL logda
//
// İstemcinin gördüğü kod "unauthenticated"tır ve bu bilinçlidir: kimlik
// kapısında 401'in ayrıntısı çağırana verilmez. Sebebi ("hangi anahtar, neden")
// yalnızca sunucunun logundadır ve senaryo tam olarak bu ayrımı sabitler —
// belgede yazan da budur.

// TestKanalsizPublishableAnahtarVitrindeReddedilir README'nin publishable
// anahtar paragrafını gerçek süreçte yürür.
//
// # Hangi mutasyonları yakalar
//
//   - kanalsız anahtarı üretim anında reddetmek (üretim adımı 201 beklerken
//     düşer): belge o gün değişmelidir, çünkü README "boş bir listeyle de
//     anahtar üretilir" diyor,
//   - kanalsız anahtarı mağaza yüzeyinde KABUL etmek (401 beklenen adım düşer):
//     boş kanal listesi aşağı akışta "süzgeç yok" diye okunursa vitrin bütün
//     kataloğu açar — kapının kapalı düşme kararı budur,
//   - sonradan bağlama ucunu kaldırmak ya da gövde alanının adını değiştirmek
//     (bağlama adımı 200 beklerken düşer),
//   - düz anahtarı okuma uçlarında da dönmek (maskeleme adımı düşer).
func TestKanalsizPublishableAnahtarVitrindeReddedilir(t *testing.T) {
	ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
	ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
	ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	jeton := jetonAl(t, s, tohumEposta, tohumParola)
	anahtarID, anahtar := kanalsizAnahtarUret(t, s, jeton)

	t.Run("düz anahtar yalnızca üretim yanıtında döner", func(t *testing.T) {
		kod, govde := s.yonetimIste(http.MethodGet, "/admin/v1/api-keys/"+anahtarID, jeton, nil)
		require.Equal(t, http.StatusOK, kod, "anahtar okunamadı; gövde: %s", govde)

		gorunum := zarfVerisi[struct {
			Redacted string `json:"redacted"`
		}](t, govde)
		assert.NotEmpty(t, gorunum.Redacted,
			"okuma ucu maskelenmiş gösterimi taşımalı; gövde: %s", govde)
		assert.NotContains(t, govde, anahtar,
			"düz anahtar YALNIZCA üretim yanıtında dönmeli; okuma ucunda görünmesi, "+
				"belgenin 'kaybederseniz tek yol iptal etmektir' cümlesini yalanlar")
	})

	t.Run("kanalsız anahtar mağaza yüzeyinde 401 alır", func(t *testing.T) {
		kod, govde := s.vitrinIste(http.MethodGet, "/store/v1/products", anahtar, nil)
		require.Equal(t, http.StatusUnauthorized, kod,
			"kanalsız publishable anahtar mağaza yüzeyine girememeli. 200, boş kanal "+
				"listesinin 'süzgeç yok' diye okunduğunu ve kataloğun tümüyle açıldığını "+
				"gösterir. gövde: %s", govde)
		assert.Equal(t, corehttp.CodeUnauthenticated, hataKodu(t, govde),
			"kimlik kapısı tek kod döner; ayrıntı çağırana verilmez. gövde: %s", govde)

		// Teşhis kodu YANITTA değil logda aranır; belgenin söylediği yer burasıdır.
		s.gunlukBekle(authservice.CodeNoSalesChannel, gunlukSuresi)
	})

	t.Run("kanal sonradan bağlanır ve AYNI anahtar çalışır", func(t *testing.T) {
		kanalID := satisKanaliAc(t, s, jeton, "Sonradan Bağlanan Kanal")

		kod, govde := s.yonetimIste(http.MethodPost,
			"/admin/v1/api-keys/"+anahtarID+"/sales-channels", jeton,
			map[string]any{"sales_channel_id": kanalID})
		require.Equal(t, http.StatusOK, kod,
			"anahtar sonradan kanala bağlanamadı. Durum kodu nedeni söyler ve üçü de "+
				"ÖLÇÜLDÜ: 404 yolun hiç mount edilmediğini, 405 yolun yalnızca başka bir "+
				"metotla mount edildiğini (aynı yolda GET durduğu için yazma ucunun "+
				"düşmesi 404 değil 405 verir), 422 ise gövde alanının adının değiştiğini "+
				"(tekil sales_channel_id). gövde: %s", govde)

		// Anahtar YENİDEN ÜRETİLMEZ: iddia, elde duran aynı düz metnin artık
		// geçmesidir. Yeni bir anahtar almak, bağlama ucunun hiçbir işe
		// yaramadığı bir dünyada da bu adımı yeşil bırakırdı.
		kod, govde = s.vitrinIste(http.MethodGet, "/store/v1/products", anahtar, nil)
		assert.Equal(t, http.StatusOK, kod,
			"kanal bağlandıktan sonra aynı anahtar mağaza yüzeyine girebilmeli; "+
				"gövde: %s", govde)
	})
}

// kanalsizAnahtarUret hiçbir satış kanalına bağlı OLMAYAN bir publishable
// anahtar üretir; kimliğini ve düz metnini döner.
//
// Gövde sales_channel_ids alanını BOŞ dizi olarak taşır, hiç taşımamak yerine:
// README'nin örneği alanı gönderir ve tuzak tam olarak "alanı gönderdim ama
// içini doldurmadım" durumudur. Alanı hiç göndermemek aynı sonuca varır ama
// belgede yazan hâli bu değildir.
func kanalsizAnahtarUret(t *testing.T, s *surec, jeton string) (anahtarID, anahtar string) {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/api-keys", jeton, map[string]any{
		"type":              string(authmodels.APIKeyPublishable),
		"title":             "smoke kanalsız anahtar",
		"sales_channel_ids": []string{},
	})
	require.Equal(t, http.StatusCreated, kod,
		"kanalsız publishable anahtar ÜRETİLEBİLMELİ; üretim anında reddedilseydi "+
			"README'nin 'boş bir listeyle de anahtar üretilir' cümlesi yanlış olurdu. "+
			"gövde: %s", govde)

	uretilen := zarfVerisi[struct {
		APIKey struct {
			ID string `json:"id"`
		} `json:"api_key"`
		Key string `json:"key"`
	}](t, govde)
	require.NotEmpty(t, uretilen.APIKey.ID, "yanıt anahtarın kimliğini taşımalı; gövde: %s", govde)
	require.NotEmpty(t, uretilen.Key, "yanıt düz anahtarı taşımalı; gövde: %s", govde)

	return uretilen.APIKey.ID, uretilen.Key
}
