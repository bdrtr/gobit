package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
)

// Bu dosya çıkış ucunun HTTP yüzeyini sınar.
//
// Oturumun gerçekten düştüğü servis katmanında kanıtlanır
// (service/cikis_test.go); burada kanıtlanan şey, handler'ın kararı VERMEDİĞİ
// ama kararın verilebilmesi için gerekeni servise GEÇİRDİĞİdir: kimliğin
// kendisi ve TÜRÜ.

// cikisRouter verilen türde doğrulanmış bir kimlikle router kurar.
func cikisRouter(t *testing.T, kind string) (chi.Router, *sahteAuth) {
	t.Helper()

	svc := &sahteAuth{}
	r := chi.NewRouter()
	r.Use(kimlikVerTur(kind))
	api.New(svc).Routes(r)

	return r, svc
}

// TestCikisCagiranKimliginiServiseGecirir çıkışın KİMİN oturumunu kapattığını
// çekirdekten aldığını kanıtlar.
//
// Kimlik istemcinin gövdesinden okunsaydı, uç başkasının oturumunu kapatmanın
// yolu olurdu: yetkisi hiç olmayan bir kullanıcı, yöneticinin kimliğini yazıp
// onu dışarı atardı.
func TestCikisCagiranKimliginiServiseGecirir(t *testing.T) {
	r, svc := cikisRouter(t, kimlikTuruKullanici)

	kayit := istek(t, r, http.MethodPost, cikisYolu, "")

	require.Equal(t, http.StatusOK, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, kimlikTestID, svc.sonCikisKimligi,
		"servise geçen kimlik, doğrulanmış çağıranın kimliği olmalı")
	assert.Equal(t, kimlikTuruKullanici, svc.sonCikisTuru,
		"kimliğin TÜRÜ de geçmeli; servis api anahtarını ancak böyle ayırt eder")
}

// TestCikisYanitiToptanIptaliBildirir yanıt gövdesinin sözleşmeyi taşıdığını
// kanıtlar.
//
// Uç gövdesiz 204 dönseydi, "bu cihazdan çıktım" sanan istemci diğer
// cihazlarının da düştüğünü yanıttan öğrenemezdi; iptal anı da yalnızca
// gövdede taşınır ve istemcinin elindeki jetonu deneme-yanılmadan elemesini
// sağlar.
func TestCikisYanitiToptanIptaliBildirir(t *testing.T) {
	r, _ := cikisRouter(t, kimlikTuruKullanici)

	kayit := istek(t, r, http.MethodPost, cikisYolu, "")
	require.Equal(t, http.StatusOK, kayit.Code, "gövde: %s", kayit.Body.String())

	var zarf struct {
		Data struct {
			AllSessions bool      `json:"all_sessions"`
			RevokedAt   time.Time `json:"revoked_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf), "gövde: %s", kayit.Body.String())

	assert.True(t, zarf.Data.AllSessions, "iptalin TOPTAN olduğu yanıttan okunabilmeli")
	assert.Equal(t, cikisAni, zarf.Data.RevokedAt.UTC(),
		"iptal anı servisten geldiği gibi taşınmalı")
}

// TestApiAnahtarininCikisReddiStatusuServistenGelir handler'ın status kodu
// SEÇMEDİĞİNİ kanıtlar.
//
// Anahtarın çıkış yapamayacağı kararı servisin kararıdır ve tipli hatayla
// döner; handler onu corehttp.WriteError'a verir. Handler kendi status kodunu
// yazsaydı, servisin sınıflandırması değiştiğinde uç sessizce yanlış kod
// dönmeye devam ederdi.
func TestApiAnahtarininCikisReddiStatusuServistenGelir(t *testing.T) {
	r, svc := cikisRouter(t, kimlikTuruAnahtar)
	svc.cikisHatasi = coreerrors.Invalid("auth_no_session",
		"api anahtarının kapatılabilecek bir oturumu yok")

	kayit := istek(t, r, http.MethodPost, cikisYolu, "")

	assert.Equal(t, http.StatusUnprocessableEntity, kayit.Code,
		"tipli hata status koduna eşlenmeli; gövde: %s", kayit.Body.String())
	assert.Equal(t, "auth_no_session", hataKodu(t, kayit))
	assert.Equal(t, kimlikTuruAnahtar, svc.sonCikisTuru,
		"reddin verilebilmesi için türün servise ulaşmış olması gerekir")
}

// TestKimliksizCikis401Doner kimliği olmayan bir isteğin çıkış yapamadığını
// kanıtlar.
//
// Uç yetki istemez ama KİMLİK ister: kimliksiz bir istek kimin oturumunu
// kapatacağını söyleyemez. Üretimde bunu corehttp.RequireAdmin keser; burada
// handler'ın kendi kapısı sınanır, çünkü o middleware bir gün bu yol için
// yanlışlıkla muaf tutulabilir.
func TestKimliksizCikis401Doner(t *testing.T) {
	r, svc := kimliksizRouter(t)

	kayit := istek(t, r, http.MethodPost, cikisYolu, "")

	assert.Equal(t, http.StatusUnauthorized, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, corehttp.CodeUnauthenticated, hataKodu(t, kayit))
	assert.Zero(t, svc.cagriSayisi, "kimliksiz istek servise HİÇ ulaşmamalı")
}
