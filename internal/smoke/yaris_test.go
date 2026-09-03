//go:build smoke

package smoke

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/db"
)

// esZamanliOrnekSayisi aynı boş veritabanına aynı anda açılan örnek sayısıdır.
//
// Üçtür çünkü arıza tam olarak böyle görüldü: Kubernetes'te replicas:3 ile ilk
// dağıtımda İKİ pod crash-loop'a girdi. İki örnek de yarışı gösterirdi ama üç,
// "kaybeden HER örnek devam ediyor mu" sorusunu da cevaplar — düzeltme yalnızca
// ikinci örneği kurtarıyor olsaydı ikili bir test bunu göremezdi.
const esZamanliOrnekSayisi = 3

// TestEsZamanliAcilisTekYoneticiYaratir B senaryosudur: üç örnek AYNI boş
// veritabanına aynı anda açılır, üçü de sağlıklı olur ve veritabanında TEK
// yönetici bulunur.
//
// # Yakalanan arıza
//
// Tohum adımı "hiç kullanıcı yoksa yarat" diye çalışır. Üç örnek aynı anda
// açıldığında ÜÇÜ DE "hiç kullanıcı yok" görür, üçü de yaratmayı dener ve
// e-posta benzersizliği ikisini reddeder. Çakışmayı hata sayan bir kurulumda
// iki örnek "admin_bootstrap_failed" ile ölür; istenen son durum ("bir yönetici
// var") sağlanmış olduğu hâlde dağıtım bozuk görünür.
//
// # Neden birim testi yetmezdi
//
// cmd/server'daki tohum mantığının sahte bir servisle yazılmış birim testi
// vardır ve çakışma dalını da kapsar. Ama o test, çakışmanın GERÇEKTEN
// üretildiğini kanıtlamaz: gerçek yarışı üreten şey, üç ayrı SÜRECİN aynı
// PostgreSQL benzersizlik kısıtına aynı anda çarpmasıdır. Bu test yarışın
// kendisini kurar; düzeltme geri alınırsa (cmd/server/setup.go içindeki
// errors.IsConflict dalı) iki örnek açılamaz ve test düşer.
func TestEsZamanliAcilisTekYoneticiYaratir(t *testing.T) {
	dsn := senaryoVeritabani(t)

	surecler := make([]*surec, 0, esZamanliOrnekSayisi)
	for i := range esZamanliOrnekSayisi {
		ayar := temelAyarlar(dsn, bosPort(t))
		ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
		ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

		// Çok örnekli dağıtımın GERÇEK yapılandırması: bellek içi koruma
		// arka ucu örnekler arasında çalışmaz (bkz. config.GuardBackend
		// godoc'u), yani üç örnekli bir kurulumda kimse onu kullanmaz. Senaryo
		// üretimde görülen kabloları sürmelidir, kolay olanları değil.
		ayar["GUARD_BACKEND"] = "redis"
		ayar["REDIS_URL"] = redisURL
		ayar["REDIS_KEY_PREFIX"] = "smoke-yaris-" + strconv.Itoa(i)

		surecler = append(surecler, sunucuBaslat(t, ayar))
	}

	// Süreçler SIRAYLA başlatılır ama yarış yine de gerçektir: exec.Start
	// milisaniyeler sürer, tohum adımı ise açılışın SONUNDA — çekirdek ve on
	// üç modülün migration'ından sonra, yani saniyeler sonra — çalışır. Üçü de
	// o ana kadar birbirine yetişmiş olur.
	for i, s := range surecler {
		s.hazirBekle(acilisSuresi)

		kod, govde := s.iste(http.MethodGet, "/health", "")
		assert.Equal(t, http.StatusOK, kod,
			"%d. örnek sağlıklı olmalı; gövde: %s", i, govde)
	}

	assert.Equal(t, int64(1), yoneticiSayisi(t, dsn),
		"eşzamanlı açılışta TEK yönetici olmalı: yarışı kaybeden örnekler "+
			"tohumu atlamalı, ikinci bir yönetici yaratmamalı")

	// Jeton üç örnekte de geçerli olmalı: kaybeden örneklerin de tohumlanmış
	// yöneticiyle çalışabildiğinin kanıtı budur. "Sağlıklı ama giriş
	// yapılamayan" bir örnek, /health'e bakan bir testin göremeyeceği bir
	// arızadır.
	jeton := jetonAl(t, surecler[0], tohumEposta, tohumParola)
	for i, s := range surecler {
		kod, govde := s.iste(http.MethodGet, "/admin/v1/auth/me", jeton)
		assert.Equal(t, http.StatusOK, kod,
			"%d. örnek tohumlanan yöneticinin jetonunu kabul etmeli; gövde: %s", i, govde)
	}
}

// yoneticiSayisi senaryo veritabanındaki silinmemiş yönetici sayısını döner.
//
// Sorgu auth modülünün tablosuna DOĞRUDAN gider; modülün servisini kurmak,
// testin sınadığı şeye (üç sürecin veritabanında bıraktığı son durum) hiçbir
// şey eklemeden kurulum maliyeti getirirdi.
func yoneticiSayisi(t *testing.T, dsn string) int64 {
	t.Helper()

	havuz, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err, "senaryo veritabanına bağlanılamadı")
	defer havuz.Close()

	var sayi int64
	require.NoError(t,
		havuz.Pool().QueryRow(t.Context(),
			"SELECT count(*) FROM auth_user WHERE deleted_at IS NULL").Scan(&sayi),
		"yönetici sayısı okunamadı")

	return sayi
}
