//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Bu dosya tek bir DEĞİŞMEZ kurar ve onu TÜM yönetim yüzeyinde denetler:
//
//	Yetkisiz bir kimlik hiçbir /admin/v1 ucunda iş yapamaz.
//
// # Neden route'lar tek tek yazılmıyor
//
// Elle yazılmış bir uç listesi, listeye eklenmesi unutulan ilk uçta kör kalır
// — ve unutulacak uç, tam da yeni yazılmış olandır. Test bunun yerine router
// ağacını GEZER: bugün kayıtlı olan her yönetim ucu otomatik kapsama girer,
// yarın eklenecek olan da.
//
// # Neden 403 bekleniyor
//
// Kimlik GEÇERLİDİR (kullanıcı var, parolası doğru, jetonu imzalı); eksik olan
// yetkidir. 401 dönmesi "kim olduğunu söyle" demek olurdu ve istemci aynı
// jetonla sonsuza dek tekrar denerdi. Ayrım corehttp.RequireScope'un
// sözleşmesidir.
//
// # Yetkisiz kullanıcı bir kaza değil, bir SÖZLEŞMEDİR
//
// service.CreateUserInput.Scopes godoc'u şunu yazar: "BOŞ ama nil olmayan
// dilim ... yetkisiz bir kullanıcı üretir — giriş yapabilir ama hiçbir
// korumalı uca erişemez." Bu test o cümlenin denetimidir.

// yetkisizMuafYollar yetki İSTEMEYEN yönetim uçlarıdır.
//
// İkisi de bilinçlidir ve listenin bu kadar kısa kalması testin asıl
// iddiasıdır: giriş ucu kimliği daha yeni kuracaktır, kimlik ucu ise kurulmuş
// kimliğin kendisini geri okur. Yetkisiz bir çağıranın kim olduğunu bile
// öğrenememesi, hiçbir şeyi korumadan hata ayıklamayı imkânsızlaştırırdı.
var yetkisizMuafYollar = map[string]struct{}{
	authapi.LoginPath:       {},
	"/admin/v1/auth/me":     {},
	"/admin/v1/auth/logout": {},
}

// yolParametreRe chi route desenindeki {param} ve {param:regex} parçalarını
// yakalar.
var yolParametreRe = regexp.MustCompile(`\{[^}]*\}`)

// yonetimRotasi gezilen bir yönetim ucudur.
type yonetimRotasi struct {
	metot string
	desen string
	yol   string
}

// yonetimRotalari router ağacındaki tüm /admin/v1 uçlarını döner.
func yonetimRotalari(t *testing.T) []yonetimRotasi {
	t.Helper()

	var rotalar []yonetimRotasi

	err := chi.Walk(testRouter, func(
		metot, desen string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		// chi, Mount edilmiş alt ağaçlarda desenin sonuna "/" ekler; tam yolla
		// kaydedilen route'larda bu olmaz ama normalize etmek, ileride bir
		// modül Mount'a geçerse testin sessizce yanlış yolu denemesini önler.
		desen = strings.TrimSuffix(desen, "/*")
		if desen != "/" {
			desen = strings.TrimSuffix(desen, "/")
		}

		if !strings.HasPrefix(desen, "/admin/v1") {
			return nil
		}
		if _, muaf := yetkisizMuafYollar[desen]; muaf {
			return nil
		}

		rotalar = append(rotalar, yonetimRotasi{
			metot: metot,
			desen: desen,
			// Yol parametreleri sahte bir değerle doldurulur: yetki denetimi
			// handler'dan ÖNCE çalıştığı için kaydın var olması gerekmez.
			// Var olmayan bir kimlikle 404 gelmesi, testin yakalamak istediği
			// hatayı (403 yerine 2xx) gizlemez; 403 beklendiği için 404 de
			// başarısızlık sayılır ve bu doğrudur — yetki, varlık
			// denetiminden ÖNCE gelmelidir.
			yol: yolParametreRe.ReplaceAllString(desen, "yetki_testi_sahte_kimlik"),
		})

		return nil
	})
	require.NoError(t, err, "router ağacı gezilemedi")

	return rotalar
}

// TestYetkisizKimlikHicbirYonetimUcundaIsYapamaz Faz 8'in RBAC ayağını TÜM
// modüller üzerinde denetler.
//
// Bir modül yetki zorlamasını eklemeyi unutursa bu test o modülün her
// ucunda kırmızı yanar; unutmanın sessiz kalacağı bir yol yoktur.
func TestYetkisizKimlikHicbirYonetimUcundaIsYapamaz(t *testing.T) {
	jeton := yetkisizYoneticiJetonu(t)
	rotalar := yonetimRotalari(t)

	// Alt sınır, testin kendisinin bozulmasına karşıdır: router boş dönerse
	// (örn. gezme mantığı kırılırsa) test hiçbir şey denemeden yeşil kalırdı.
	require.Greater(t, len(rotalar), 50,
		"yönetim yüzeyi beklenenden küçük göründü; gezme mantığı bozulmuş olabilir")

	for _, rota := range rotalar {
		t.Run(rota.metot+" "+rota.desen, func(t *testing.T) {
			istek := httptest.NewRequest(rota.metot, rota.yol, http.NoBody)
			istek.Header.Set("Authorization", "Bearer "+jeton)
			istek.Header.Set("Content-Type", "application/json")

			kayit := httptest.NewRecorder()
			testRouter.ServeHTTP(kayit, istek)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kimlik bu uçta 403 almalı; gövde: %s", kayit.Body.String())
		})
	}
}

// TestYetkisizKullaniciGirisYapabilir yetkisizliğin kimliği DEĞİL yetkiyi
// kaldırdığını doğrular.
//
// İkisi ayrılmasaydı yetkisiz bir kullanıcı hiç giriş yapamaz, dolayısıyla
// kendisine yetki verilmesini de isteyemezdi.
func TestYetkisizKullaniciGirisYapabilir(t *testing.T) {
	jeton := yetkisizYoneticiJetonu(t)

	kimlik := kimlikOku(t, "Bearer "+jeton)
	assert.Equal(t, authsvc.PrincipalKindUser, kimlik.Kind)
	assert.Empty(t, kimlik.Scopes, "yetkisiz kullanıcının yetki listesi boş olmalı")
}

// yetkisizYoneticiJetonu yetkisi BOŞ bir kullanıcı üretip jetonunu döner.
//
// Kullanıcı bir kez üretilir ve testler paylaşır; her testte yenisini
// üretmek, bcrypt maliyetini yüzlerce alt-test boyunca tekrarlardı.
func yetkisizYoneticiJetonu(t *testing.T) string {
	t.Helper()

	yetkisizBirKez.Do(func() {
		_, err := authSvc.CreateUser(t.Context(), authsvc.CreateUserInput{
			Email:     yetkisizEposta,
			FirstName: "Yetkisiz",
			LastName:  "Kullanıcı",
			// nil DEĞİL, boş dilim: "yetki alanını unuttum" ile "yetkisiz
			// olsun" arasındaki farkı servis bilinçli olarak korur.
			Scopes: []string{},
		}, yetkisizParola)
		yetkisizKurulumHatasi = err
	})
	require.NoError(t, yetkisizKurulumHatasi, "yetkisiz kullanıcı kurulamadı")

	return jetonAl(t, yetkisizEposta, yetkisizParola)
}

// Yetkisiz fikstür kullanıcısının sabitleri ve bir kez kurulum durumu.
var (
	// yetkisizEposta yetkisi boş olan fikstür kullanıcısının e-postasıdır.
	yetkisizEposta = "yetkisiz@gobit.test"
	// yetkisizParola o kullanıcının parolasıdır.
	yetkisizParola = "yetkisiz-parola-42"
	// yetkisizBirKez kullanıcının yalnızca bir kez üretilmesini sağlar.
	yetkisizBirKez sync.Once
	// yetkisizKurulumHatasi kurulum hatasını testlere taşır.
	yetkisizKurulumHatasi error
)
