//go:build smoke

package smoke

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// TestYanlisYapilandirmaAcilistaDurur C senaryosudur: kusurlu her yapılandırma
// AÇILIŞTA, sıfırdan farklı bir çıkış koduyla ve ANLAŞILIR bir mesajla durur.
//
// # Neden çıkış kodu ve mesaj birlikte
//
// Yalnızca çıkış kodu sınansaydı, "bir yerde patladı" bilgisiyle yetinilirdi
// ve yanlış sebeple patlayan bir açılış da testi geçerdi. Yalnızca mesaj
// sınansaydı, mesajı yazıp yine de açılmaya devam eden bir kurulum yakalanmazdı
// — üstelik en tehlikeli hâl budur: operatör uyarıyı loglarda görür, sistem
// çalışıyor sanır ve eksik yapılandırmayla üretime çıkar.
//
// # Neden açılış, ilk istek değil
//
// Yedi arızanın hepsi sessizce ERTELENEBİLİRDİ: bilinmeyen eklenti yok
// sayılabilir, eksik anahtar ilk ödemede patlayabilir, paylaşılan koruma
// sessizce bellek içine düşebilir, bilinmeyen bir sağlayıcı adı ilk bildirim
// ya da ilk yüklemeye kadar beklenebilir. Hepsinde bedel aynı: arıza,
// yapılandırmayı yazan kişinin elinden çıktıktan GÜNLER sonra, çoğu zaman bir
// müşteri isteğinin ortasında görünür.
//
// # Sağlayıcı senaryoları neden burada
//
// Son iki senaryo (bildirim ve dosya sağlayıcısı) yapılandırmanın SIRA
// bakımından en geç doğrulanan kısmıdır: ikisi de modüller ayağa kalktıktan,
// akışlar kurulduktan ve eklentiler başlatıldıktan SONRA denetlenir. Yani
// süreç bunlarda ölüyorsa, o noktaya kadarki tüm kurulum adımları dinleyici
// AÇILMADAN ÖNCE ve SENKRON koşmuş demektir. Bir kurulum adımının sessizce
// arka plana (goroutine'e) alınması, kurulum hatasını "açılış durdu"dan "ilk
// istekte 500"e çevirir ve bu senaryolar tam olarak o kaymayı yakalar.
//
// # Neyi kapsamaz
//
// Akış kurulumunun (cmd/server, registerWorkflows) KENDİSİNİ başarısız kılan bir
// yapılandırma bugün YAZILAMIYOR: akışlar yalnızca modüllerin koşulsuz
// kaydettiği adları çözer (cart/pricing/region/customer/order/payment/... ve
// çekirdek servisleri) ve o modüllerin kaydını hiçbir ortam değişkeni
// kapatmaz. Yani "akış kurulamadı" hâli ancak KOD değişikliğiyle üretilebilir;
// buradaki en yakın güvence, akış kurulumundan SONRA koşan yukarıdaki iki
// denetimdir.
func TestYanlisYapilandirmaAcilistaDurur(t *testing.T) {
	// Erişilemez Redis için KAPALI bir port kullanılır: adres biçimsel olarak
	// geçerlidir, yani hata "URL çözümlenemedi" değil gerçekten
	// "bağlanılamadı" olur. Sınanmak istenen ayrım tam olarak budur.
	kapaliPort := bosPort(t)

	durumlar := map[string]struct {
		// duzenle temel ayarları senaryonun kusuruyla değiştirir.
		duzenle func(ayarlar)
		// anahtarlar stderr'de GEÇMESİ gereken metinlerdir: biri arızanın
		// makine kodu, diğeri operatörün düzelteceği ayarın adıdır.
		anahtarlar []string
	}{
		"bilinmeyen eklenti adı": {
			duzenle: func(a ayarlar) {
				a["PLUGINS"] = "boyle-bir-eklenti-yok"
			},
			anahtarlar: []string{"plugin_unknown", "boyle-bir-eklenti-yok"},
		},
		"eklenti var ama ayarı yok": {
			duzenle: func(a ayarlar) {
				// STRIPE_API_KEY BİLİNÇLİ olarak verilmez; süreç ortamı
				// sıfırdan kurulduğu için (bkz. ortam) kabuktan sızamaz da.
				a["PLUGINS"] = paymentstripe.Name
			},
			anahtarlar: []string{"STRIPE_API_KEY", paymentstripe.Name},
		},
		"paylaşılan koruma arka ucu erişilemez": {
			duzenle: func(a ayarlar) {
				a["GUARD_BACKEND"] = "redis"
				a["REDIS_URL"] = "redis://127.0.0.1:" + strconv.Itoa(kapaliPort) + "/0"
			},
			anahtarlar: []string{"redis_unreachable"},
		},
		"yarım tohum yapılandırması": {
			duzenle: func(a ayarlar) {
				// Parola BİLİNÇLİ olarak eksik: operatörün iki değişkenden
				// birini yazıp diğerini unuttuğu hâl.
				a["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
			},
			anahtarlar: []string{"ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_PASSWORD"},
		},
		"paylaşılan ortamda imza sırrı yok": {
			duzenle: func(a ayarlar) {
				a["APP_ENV"] = "staging"
				delete(a, "JWT_SECRET")
			},
			anahtarlar: []string{"JWT_SECRET", "staging"},
		},
		"bilinmeyen bildirim sağlayıcısı": {
			duzenle: func(a ayarlar) {
				// Ad, hiçbir eklentinin ve kutudan çıkan hiçbir sağlayıcının
				// karşılamayacağı biçimde seçilir: sınanan şey "yanlış ad"
				// değil, yanlış adın AÇILIŞTA yakalanmasıdır.
				a["NOTIFICATION_PROVIDER"] = "boyle-bir-saglayici-yok"
			},
			anahtarlar: []string{"notification_provider_unknown", "NOTIFICATION_PROVIDER"},
		},
		"bilinmeyen dosya sağlayıcısı": {
			duzenle: func(a ayarlar) {
				a["FILE_PROVIDER"] = "boyle-bir-saglayici-yok"
			},
			anahtarlar: []string{"file_provider_unknown", "FILE_PROVIDER"},
		},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			// Her alt test KENDİ veritabanını alır: kusurların bir kısmı
			// migration'lardan SONRA patlar ve paylaşılan bir veritabanı,
			// yarım kalmış bir açılışın izini sonraki senaryoya taşırdı.
			ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
			durum.duzenle(ayar)

			kod, hata := acilistaDurmali(t, ayar, acilisSuresi)

			assert.NotZero(t, kod,
				"yanlış yapılandırma sıfırdan farklı çıkış kodu vermeli; stderr:\n%s", hata)

			for _, anahtar := range durum.anahtarlar {
				assert.Contains(t, hata, anahtar,
					"stderr operatöre neyi düzelteceğini söylemeli; stderr:\n%s", hata)
			}
		})
	}
}
