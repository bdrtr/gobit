//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya planın Faz 8 DoD'sini kanıtlar:
//
//	"Yetkisiz istek 401; admin login -> token ile korumalı endpoint'e erişim;
//	 publishable key olmadan store API erişimi reddediliyor."
//
// # Neden HTTP'den geçiyor
//
// Faz 8'in iddiası bir SERVİS iddiası değil, bir TAŞIMA iddiasıdır: koruma
// middleware'i doğru yollara, doğru sırayla ve doğru muafiyetle bağlanmış
// olmalıdır. Servisi doğrudan çağıran bir test, middleware hiç takılmamış
// olsa da yeşil kalırdı — Faz 8 öncesi durum tam olarak buydu.
//
// Router, üretimdekiyle AYNI koruma yığınıyla kurulur (bkz. e2e_test.go,
// corehttp.APIGuards); testin kanıtladığı koruma, üretimde çalışanın ta
// kendisidir.

// yonetimIstegi verilen Authorization başlığıyla bir yönetim isteği yapar.
//
// Başlık boşsa hiç eklenmez: "başlık yok" ile "boş başlık" farklı durumlardır
// ve 401 iddiası ilkini hedefler.
func yonetimIstegi(t *testing.T, method, yol, yetki string) *httptest.ResponseRecorder {
	t.Helper()

	istek := httptest.NewRequest(method, yol, http.NoBody)
	if yetki != "" {
		istek.Header.Set("Authorization", yetki)
	}

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// magazaIstegi verilen publishable anahtarla bir mağaza isteği yapar.
func magazaIstegi(t *testing.T, yol, anahtar string) *httptest.ResponseRecorder {
	t.Helper()

	istek := httptest.NewRequest(http.MethodGet, yol, http.NoBody)
	if anahtar != "" {
		istek.Header.Set(corehttp.PublishableKeyHeader, anahtar)
	}

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// girisYap giriş ucunu çağırır ve yanıt kaydını döner.
func girisYap(t *testing.T, eposta, parola string) *httptest.ResponseRecorder {
	t.Helper()

	govde, err := json.Marshal(map[string]string{"email": eposta, "password": parola})
	require.NoError(t, err, "giriş gövdesi kodlanamadı")

	istek := httptest.NewRequest(http.MethodPost, authapi.LoginPath, bytes.NewReader(govde))
	istek.Header.Set("Content-Type", "application/json")

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// jetonAl başarılı bir girişten oturum jetonunu çıkarır.
func jetonAl(t *testing.T, eposta, parola string) string {
	t.Helper()

	kayit := girisYap(t, eposta, parola)
	require.Equal(t, http.StatusOK, kayit.Code,
		"giriş 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf struct {
		Data struct {
			Token     string `json:"token"`
			TokenType string `json:"token_type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"giriş yanıtı çözülemedi; gövde: %s", kayit.Body.String())
	require.NotEmpty(t, zarf.Data.Token, "giriş jeton dönmeli")
	require.Equal(t, "Bearer", zarf.Data.TokenType,
		"istemci hangi şemayı kullanacağını yanıttan öğrenmeli")

	return zarf.Data.Token
}

// kimlikOku /admin/v1/auth/me ucundan doğrulanmış kimliği okur.
func kimlikOku(t *testing.T, yetki string) principalGorunumu {
	t.Helper()

	kayit := yonetimIstegi(t, http.MethodGet, "/admin/v1/auth/me", yetki)
	require.Equal(t, http.StatusOK, kayit.Code,
		"kimlik ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf struct {
		Data principalGorunumu `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"kimlik yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf.Data
}

// principalGorunumu /admin/v1/auth/me yanıtının test tarafındaki karşılığıdır.
type principalGorunumu struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Scopes          []string `json:"scopes"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// TestYetkisizYonetimIstegiReddedilir kimliksiz her yönetim isteğinin 401
// döndüğünü doğrular.
//
// Tablo yalnızca okuma uçlarıyla sınırlı DEĞİLDİR: asıl risk yazma
// uçlarındadır. Koruma bağlanmamış olsaydı DELETE /admin/v1/products/{id}
// başlıksız çalışır ve katalog silinebilirdi.
func TestYetkisizYonetimIstegiReddedilir(t *testing.T) {
	testler := map[string]struct {
		method string
		yol    string
	}{
		"kimlik ucu":            {http.MethodGet, "/admin/v1/auth/me"},
		"kullanıcı listesi":     {http.MethodGet, "/admin/v1/users"},
		"anahtar oluşturma":     {http.MethodPost, "/admin/v1/api-keys"},
		"ürün silme":            {http.MethodDelete, "/admin/v1/products/prod_yok"},
		"sipariş listesi":       {http.MethodGet, "/admin/v1/orders"},
		"satış kanalı listesi":  {http.MethodGet, "/admin/v1/sales-channels"},
		"tanımsız yönetim yolu": {http.MethodGet, "/admin/v1/boyle-bir-uc-yok"},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			kayit := yonetimIstegi(t, tt.method, tt.yol, "")

			assert.Equal(t, http.StatusUnauthorized, kayit.Code,
				"kimliksiz yönetim isteği 401 dönmeli; gövde: %s", kayit.Body.String())
			assert.Equal(t, "Bearer", kayit.Header().Get("WWW-Authenticate"),
				"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")
		})
	}
}

// TestTanimsizYonetimYoluDaKorunur korumanın route eşleşmesinden ÖNCE
// çalıştığını doğrular.
//
// Ayrı bir testtir çünkü iddiası farklıdır: tanımsız bir yol 404 dönseydi,
// saldırgan yalnızca status koduna bakarak hangi yönetim uçlarının var
// olduğunu haritalayabilirdi. 401, var olan ve olmayan yolu ayırt ettirmez.
func TestTanimsizYonetimYoluDaKorunur(t *testing.T) {
	varOlan := yonetimIstegi(t, http.MethodGet, "/admin/v1/users", "")
	olmayan := yonetimIstegi(t, http.MethodGet, "/admin/v1/kesinlikle-yok", "")

	assert.Equal(t, varOlan.Code, olmayan.Code,
		"var olan ve olmayan yönetim yolu aynı status dönmeli, aksi hâlde uç haritası sızar")
	assert.Equal(t, http.StatusUnauthorized, olmayan.Code)
}

// TestYoneticiGirisiJetonlaKorumaliUcaErisir Faz 8 DoD'sinin ikinci ayağıdır:
// giriş -> jeton -> korumalı uca erişim.
func TestYoneticiGirisiJetonlaKorumaliUcaErisir(t *testing.T) {
	jeton := jetonAl(t, yoneticiEposta, yoneticiParola)

	kimlik := kimlikOku(t, "Bearer "+jeton)

	assert.Equal(t, yoneticiID, kimlik.ID, "jeton, giriş yapan kullanıcıyı taşımalı")
	assert.Equal(t, authsvc.PrincipalKindUser, kimlik.Kind)
	assert.Contains(t, kimlik.Scopes, corehttp.ScopeAdmin,
		"varsayılan yönetim kullanıcısı tam yetkili olmalı")

	// Jeton yalnızca kimlik ucunda değil, gerçek bir yönetim ucunda da geçerli
	// olmalı: kimlik ucu korumadan bağımsız bir yol izleseydi test kör kalırdı.
	kayit := yonetimIstegi(t, http.MethodGet, "/admin/v1/users", "Bearer "+jeton)
	assert.Equal(t, http.StatusOK, kayit.Code,
		"jetonla kullanıcı listesi okunabilmeli; gövde: %s", kayit.Body.String())
}

// TestGizliAnahtarYonetimYuzeyineErisir insan olmayan çağıranın (entegrasyon)
// jeton almadan çalışabildiğini doğrular.
func TestGizliAnahtarYonetimYuzeyineErisir(t *testing.T) {
	kimlik := kimlikOku(t, "Bearer "+gizliAnahtar)

	assert.Equal(t, authsvc.PrincipalKindAPIKey, kimlik.Kind)
	assert.Contains(t, kimlik.Scopes, corehttp.ScopeAdmin)
	assert.Empty(t, kimlik.SalesChannelIDs,
		"gizli anahtar satış kanalı taşımaz; kanal bağı publishable anahtara aittir")
}

// TestGecersizKimlikBilgileriReddedilir kimliğin gerçekten DOĞRULANDIĞINI
// gösterir: yalnızca "başlık var mı" kontrolü yapan bir stub bu tabloyu
// geçemez.
func TestGecersizKimlikBilgileriReddedilir(t *testing.T) {
	gecerliJeton := jetonAl(t, yoneticiEposta, yoneticiParola)

	testler := map[string]string{
		"boş şema":              "Bearer",
		"yanlış şema":           "Basic " + gizliAnahtar,
		"uydurma jeton":         "Bearer uydurma.jeton.dizesi",
		"imzası bozulmuş jeton": "Bearer " + gecerliJeton + "bozuk",
		"uydurma gizli anahtar": "Bearer sk_" + "0123456789abcdef0123456789abcdef",
		"publishable anahtar":   "Bearer " + publishableAnahtar,
		"şemasız düz kimlik":    gizliAnahtar,
	}

	for ad, yetki := range testler {
		t.Run(ad, func(t *testing.T) {
			kayit := yonetimIstegi(t, http.MethodGet, "/admin/v1/auth/me", yetki)

			assert.Equal(t, http.StatusUnauthorized, kayit.Code,
				"geçersiz kimlik 401 dönmeli; gövde: %s", kayit.Body.String())
		})
	}
}

// TestPublishableAnahtarsizMagazaIstegiReddedilir Faz 8 DoD'sinin üçüncü
// ayağıdır.
func TestPublishableAnahtarsizMagazaIstegiReddedilir(t *testing.T) {
	anahtarsiz := magazaIstegi(t, "/store/v1/products", "")
	assert.Equal(t, http.StatusUnauthorized, anahtarsiz.Code,
		"publishable anahtarsız mağaza isteği reddedilmeli; gövde: %s", anahtarsiz.Body.String())

	anahtarli := magazaIstegi(t, "/store/v1/products", publishableAnahtar)
	assert.Equal(t, http.StatusOK, anahtarli.Code,
		"publishable anahtarla mağaza isteği geçmeli; gövde: %s", anahtarli.Body.String())
}

// TestGizliAnahtarMagazaBasligindaGecmez anahtar türlerinin yer
// değiştiremediğini doğrular.
//
// İddia güvenliktir, kolaylık değil: gizli anahtar tarayıcıda görünen bir
// başlıkta taşınabilseydi, vitrin kodunun içine yönetim yetkisi gömülürdü.
func TestGizliAnahtarMagazaBasligindaGecmez(t *testing.T) {
	kayit := magazaIstegi(t, "/store/v1/products", gizliAnahtar)

	assert.Equal(t, http.StatusUnauthorized, kayit.Code,
		"gizli anahtar mağaza başlığında kabul edilmemeli; gövde: %s", kayit.Body.String())
}

// TestMagazaKimligiSatisKanaliniTasir publishable anahtarın işinin yetki
// değil BAĞLAM olduğunu doğrular: istek bir satış kanalına bağlanır.
func TestMagazaKimligiSatisKanaliniTasir(t *testing.T) {
	kimlik := kimlikOku(t, "Bearer "+gizliAnahtar)
	require.Empty(t, kimlik.SalesChannelIDs)

	// Mağaza kimliği yönetim ucundan okunamaz (publishable anahtar orada
	// geçmez), bu yüzden doğrudan doğrulayıcıya sorulur — koruma
	// middleware'inin context'e koyduğu kimliğin ta kendisi budur.
	dogrulayici, err := testAuthn.AuthenticateStore(context.Background(), publishableAnahtar)
	require.NoError(t, err, "publishable anahtar mağaza kimliği üretmeli")

	assert.Equal(t, authsvc.PrincipalKindAPIKey, dogrulayici.Kind)
	assert.Empty(t, dogrulayici.Scopes,
		"publishable anahtar YETKİ TAŞIMAZ; taşısaydı tarayıcıya konan bir yönetim kimliği olurdu")
	assert.Equal(t, []string{testKanalID}, dogrulayici.SalesChannelIDs)
}

// TestIptalEdilenAnahtarReddedilir iptalin ANINDA etkili olduğunu doğrular.
//
// Anahtarın kaydı silinmez, "revoked" işaretlenir; doğrulama yolu o işareti
// okumazsa iptal edilmiş bir anahtar sonsuza kadar çalışmaya devam ederdi.
func TestIptalEdilenAnahtarReddedilir(t *testing.T) {
	ctx := context.Background()

	kayit, duzMetin, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeySecret,
		Title:     "iptal edilecek anahtar",
		CreatedBy: yoneticiID,
	})
	require.NoError(t, err, "anahtar üretilemedi")

	once := yonetimIstegi(t, http.MethodGet, "/admin/v1/auth/me", "Bearer "+duzMetin)
	require.Equal(t, http.StatusOK, once.Code,
		"yeni anahtar çalışmalı; gövde: %s", once.Body.String())

	_, err = authSvc.RevokeAPIKey(ctx, kayit.ID, yoneticiID)
	require.NoError(t, err, "anahtar iptal edilemedi")

	sonra := yonetimIstegi(t, http.MethodGet, "/admin/v1/auth/me", "Bearer "+duzMetin)
	assert.Equal(t, http.StatusUnauthorized, sonra.Code,
		"iptal edilen anahtar reddedilmeli; gövde: %s", sonra.Body.String())
}

// TestGirisUcuKorumadanMuafKalir korumanın giriş ucunu kapsamadığını doğrular.
//
// Kapsasaydı kimse giriş yapamaz ve sistem kilitlenirdi — muafiyetin
// kaybolması sessiz değil, tam ekran bir arızadır.
func TestGirisUcuKorumadanMuafKalir(t *testing.T) {
	// Doğru parolayla 200: uç kimlik doğrulamadan geçmiş olsaydı buraya hiç
	// varılamazdı.
	basarili := girisYap(t, yoneticiEposta, yoneticiParola)
	assert.Equal(t, http.StatusOK, basarili.Code,
		"giriş ucu kimlik istemeden çalışmalı; gövde: %s", basarili.Body.String())

	// Yanlış parolayla da 401 döner ama bu SERVİSİN kararıdır; middleware
	// engellemiş olsaydı gövde farklı olurdu.
	yanlis := girisYap(t, yoneticiEposta, "yanlis-parola")
	assert.Equal(t, http.StatusUnauthorized, yanlis.Code)
}

// TestGirisHatasiKullaniciSayimiSizdirmaz olmayan e-posta ile yanlış parolanın
// AYNI cevabı ürettiğini doğrular.
//
// Fark olsaydı, saldırgan hangi e-postaların kayıtlı olduğunu tek tek
// öğrenebilirdi; bu, hedefli kimlik avı saldırısının ilk adımıdır.
func TestGirisHatasiKullaniciSayimiSizdirmaz(t *testing.T) {
	yanlisParola := girisYap(t, yoneticiEposta, "kesinlikle-yanlis")
	olmayanKullanici := girisYap(t, "hic-boyle-biri-yok@gobit.test", "kesinlikle-yanlis")

	require.Equal(t, http.StatusUnauthorized, yanlisParola.Code)
	require.Equal(t, http.StatusUnauthorized, olmayanKullanici.Code)

	// request_id her istekte FARKLIDIR ve farklı olmalıdır; karşılaştırma
	// ondan arındırılır. Sızıntı riski taşıyan alanlar kod ve mesajdır.
	assert.Equal(t, hataOzu(t, yanlisParola), hataOzu(t, olmayanKullanici),
		"iki hata gövdesi ayırt edilemez olmalı")
}

// hataOzu bir hata yanıtından kod ve mesajı çıkarır.
func hataOzu(t *testing.T, kayit *httptest.ResponseRecorder) [2]string {
	t.Helper()

	var zarf struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"hata yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return [2]string{zarf.Error.Code, zarf.Error.Message}
}

// TestSaglikUclariKorumasizKalir orkestratörün gördüğü yolun kimlik
// istemediğini doğrular.
//
// Koruma yığını /health'i de kapsasaydı readiness probe'u 401 alır, Kubernetes
// süreci sağlıksız sayıp sonsuz döngüde yeniden başlatırdı.
func TestSaglikUclariKorumasizKalir(t *testing.T) {
	for _, yol := range []string{"/health", "/ready"} {
		t.Run(yol, func(t *testing.T) {
			istek := httptest.NewRequest(http.MethodGet, yol, http.NoBody)
			kayit := httptest.NewRecorder()
			testRouter.ServeHTTP(kayit, istek)

			assert.NotEqual(t, http.StatusUnauthorized, kayit.Code,
				"sağlık ucu kimlik istememeli; gövde: %s", kayit.Body.String())
		})
	}
}
