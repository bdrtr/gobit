//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// TestSogukAcilisBosVeritabaniylaCalisirDurumaGelir A senaryosudur: boş bir
// veritabanına açılan süreç, hiçbir elle adım olmadan KULLANILABİLİR olur.
//
// # Neden gerçek süreç
//
// internal/e2e aynı uçlara httptest ile gider ve hepsini yeşil bulur, ama
// router'ı KENDİ kurar: migration'ları elle uygular, tohum adımını hiç
// çalıştırmaz ve config'i hiç yüklemez. Yani "depoyu klonlayıp çalıştıran biri
// giriş yapabiliyor mu" sorusunu cevaplayamaz — bu test tam olarak onu sorar
// ve README'nin anlattığı akışı adım adım yürür.
func TestSogukAcilisBosVeritabaniylaCalisirDurumaGelir(t *testing.T) {
	ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
	ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
	ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

	s := sunucuBaslat(t, ayar)
	s.hazirBekle(acilisSuresi)

	t.Run("sağlık uçları", func(t *testing.T) {
		kod, govde := s.iste(http.MethodGet, "/health", "")
		assert.Equal(t, http.StatusOK, kod, "/health 200 dönmeli; gövde: %s", govde)

		kod, govde = s.iste(http.MethodGet, "/ready", "")
		require.Equal(t, http.StatusOK, kod, "/ready 200 dönmeli; gövde: %s", govde)

		// /ready'nin postgres kontrolünü İÇERMESİ ayrıca doğrulanır: kontrol
		// listesi boşsa uç her zaman 200 döner ve orkestratör, veritabanı
		// düşmüşken bile trafiği bu örneğe göndermeye devam ederdi.
		var hazir struct {
			Status string `json:"status"`
			Checks map[string]struct {
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			} `json:"checks"`
		}
		require.NoError(t, json.Unmarshal([]byte(govde), &hazir),
			"/ready yanıtı çözülemedi; gövde: %s", govde)

		assert.Equal(t, "ok", hazir.Status, "hazırlık durumu ok olmalı")
		require.Contains(t, hazir.Checks, "postgres",
			"/ready postgres kontrolünü içermeli; gövde: %s", govde)
		assert.Equal(t, "ok", hazir.Checks["postgres"].Status,
			"postgres kontrolü ok olmalı; gövde: %s", govde)
	})

	t.Run("README akışı: giriş, jeton, korumalı uç", func(t *testing.T) {
		jeton := jetonAl(t, s, tohumEposta, tohumParola)

		kod, govde := s.iste(http.MethodGet, "/admin/v1/auth/me", jeton)
		assert.Equal(t, http.StatusOK, kod,
			"tohumlanan yönetici korumalı uca erişebilmeli; gövde: %s", govde)
	})

	t.Run("kimliksiz yönetim isteği 401", func(t *testing.T) {
		kod, govde := s.iste(http.MethodGet, "/admin/v1/users", "")
		assert.Equal(t, http.StatusUnauthorized, kod,
			"kimliksiz yönetim isteği reddedilmeli; gövde: %s", govde)
	})

	// Tanımsız yönetim yolu da 401 dönmeli, 404 DEĞİL: 404, "bu uç yok"
	// bilgisini kimliksiz çağırana verir ve yönetim yüzeyinin uç haritası
	// böyle bir yanıt farkıyla dışarı sızar. Koruma yolun VARLIĞINDAN önce
	// çalışmalıdır.
	t.Run("tanımsız yönetim yolu 401 döner, 404 değil", func(t *testing.T) {
		kod, govde := s.iste(http.MethodGet, "/admin/v1/boyle-bir-uc-yok", "")
		assert.Equal(t, http.StatusUnauthorized, kod,
			"tanımsız yönetim yolu uç haritasını sızdırmamalı; gövde: %s", govde)
	})
}

// jetonAl giriş ucundan bir oturum jetonu alır.
//
// Gövde elle kurulur, auth modülünün DTO'su import EDİLMEZ: bu test ağdan
// geçen SÖZLEŞMEYİ sınar ve Go tipini paylaşmak, alan adı değişse bile testin
// yeşil kalmasına yol açardı. Yol ise sabitten okunur (authapi.LoginPath);
// orada sızma riski yok, tersine yolun elle yazılması iki yerde ayrışırdı.
func jetonAl(t *testing.T, s *surec, eposta, parola string) string {
	t.Helper()

	govde, err := json.Marshal(map[string]string{"email": eposta, "password": parola})
	require.NoError(t, err, "giriş gövdesi kodlanamadı")

	istek, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.adres+authapi.LoginPath, bytes.NewReader(govde))
	require.NoError(t, err, "giriş isteği kurulamadı")
	istek.Header.Set("Content-Type", "application/json")

	kod, yanit := s.gonder(istek)
	require.Equal(t, http.StatusOK, kod, "giriş 200 dönmeli; gövde: %s", yanit)

	var zarf struct {
		Data struct {
			Token     string `json:"token"`
			TokenType string `json:"token_type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(yanit), &zarf),
		"giriş yanıtı çözülemedi; gövde: %s", yanit)
	require.NotEmpty(t, zarf.Data.Token, "giriş jeton dönmeli; gövde: %s", yanit)
	assert.Equal(t, "Bearer", zarf.Data.TokenType,
		"istemci hangi şemayı kullanacağını yanıttan öğrenmeli")

	return zarf.Data.Token
}
