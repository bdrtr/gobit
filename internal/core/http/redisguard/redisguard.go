// Package redisguard hız sınırlayıcının ve idempotency deposunun paylaşılan
// (Redis tabanlı) uygulamalarını sağlar.
//
// internal/core/http paketindeki bellek içi karşılıkları (MemoryLimiter,
// MemoryIdempotencyStore) tek örnekli kurulumlar ve testler içindir. Yatay
// ölçeklenen bir dağıtımda ikisi de bozulur, ama AYNI ŞEKİLDE değil:
//
//   - Hız sınırı örnek sayısıyla ÇARPILIR: her örnek kendi sayacını tuttuğu
//     için 3 örnekli bir kurulumda "dakikada 60" pratikte 180 olur. Bu bir
//     HIZ sorunudur; sınır gevşer ama hiçbir istek yanlış işlenmez.
//   - Idempotency koruması örnekler arasında HİÇ çalışmaz: aynı anahtarla
//     farklı örneklere düşen iki istek iki kez işlenir, yani iki sipariş, iki
//     tahsilat. Bu bir hız değil DOĞRULUK sorunudur ve tam da idempotency'nin
//     var olma sebebini ortadan kaldırır.
//
// İkisinin de çaresi paylaşılan bir sayaç/depodur; bu paket onu Redis üzerine
// kurar. Sözleşmeler değişmez: [Limiter] corehttp.RateLimiter'ı,
// [IdempotencyStore] corehttp.IdempotencyStore'u birebir karşılar ve
// middleware'ler hangi uygulamanın takılı olduğunu bilmez.
//
// # Arıza davranışı
//
// Redis erişilemezse iki tip ZIT davranır ve bu bilinçlidir. Sınırlayıcı hata
// döner, middleware isteği GEÇİRİR (fail-open): sınır ürünün doğruluğu için
// değil kötüye kullanıma karşıdır, Redis'in düşmesi tüm trafiği kesmemelidir.
// Depo da hata döner ama middleware onu istemciye YAZAR (fail-closed): kaydı
// yazamıyorken isteği geçirmek, tekrarın ikinci kez işlenmesi demektir —
// korumanın kapalı olduğu anda geçmek, korumayı hiç takmamakla aynıdır.
// Bu asimetri corehttp tarafında zaten kodlanmıştır; buradaki uygulamaların
// tek görevi hataları TİPLİ döndürmektir.
//
// # Anahtar ad alanı
//
// Anahtar biçimi, kuruculara verilen ad alanı önekiyle başlar:
//
//	<önek>:rl:<sınır anahtarı>          — hız sınırı sayacı
//	<önek>:idem:<idempotency anahtarı>  — idempotency kaydı
//
// Önek KURUCU PARAMETRESİDİR, sabit değil. İki gerekçesi var ve ikincisi
// birincisinden çok daha ağır basar:
//
//   - Aynı Redis'i paylaşan başka verilerle (cache, kuyruk, oturum) karışmayı
//     önler; operasyon "<önek>:idem:*" ile tarayabilir.
//   - AYNI Redis'i paylaşan iki gobit KURULUMUNU (staging ile production, ya
//     da aynı kümedeki iki mağaza) birbirinden ayırır. Sabit önekle bu iki
//     kurulum birbirinin hız sınırı kotasını harcar — bu bir hız sorunudur —
//     ve birbirinin idempotency kaydını OKUR: bir kurulumun yanıtı ötekinin
//     istemcisine gider. İkincisi doğruluk sorunudur; ayrı DB/örnek zorunlu
//     tutulsaydı çare altyapıya havale edilmiş olurdu, oysa Redis Cluster
//     numaralı DB'leri desteklemez ve ayrı örnek para/operasyon maliyetidir.
//
// Önek [dogrulaOnek] ile denetlenir; ayırıcı içeren ya da boş bir önek sessizce
// kabul EDİLMEZ.
package redisguard

import (
	"strings"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Anahtar bölümleri; ad alanı önekiyle birleşerek tam anahtarı kurar.
const (
	// ayirici anahtar bölümlerini ayıran karakterdir.
	//
	// Redis'te bir dil kuralı değil GELENEKTİR: redis-cli ve çoğu izleme aracı
	// anahtarları bu karakterden bölerek ağaç gibi gösterir, bellek raporlarını
	// da bu ağaca göre gruplar.
	ayirici = ":"
	// hizSinirBolumu hız sınırı sayaçlarının bölüm adıdır.
	hizSinirBolumu = "rl"
	// idempotencyBolumu idempotency kayıtlarının bölüm adıdır.
	idempotencyBolumu = "idem"
)

// dogrulaOnek ad alanı önekinin biçimini denetler.
//
// Kabul edilen: en az bir karakter, ve yalnızca ASCII harf, rakam, '-', '_',
// '.'. Bu liste "güvenli olduğu düşünülen" karakterler değil, reddedilenlerin
// her birinin SOMUT bir arızası olduğu için bu kadar dardır:
//
//   - Boş önek anahtarı ":rl:istemci" yapar. Ad alanı yok demektir; oysa
//     çağıran önek PARAMETRESİ vererek tam da ad alanı istediğini söylemiştir.
//     Boşu varsayılana çevirmek daha da kötü olurdu: eksik yapılandırmayla
//     açılan iki kurulum yine aynı ad alanına düşer, yani düzeltmeye
//     çalıştığımız arıza sessizce geri gelir.
//   - Ayırıcı (':') gerçek bir ÇAKIŞMA açar. "a" öneki "a:idem:<K>" yazar;
//     "a:idem:x" öneki "a:idem:x:idem:<K2>" yazar. İstemcinin uydurduğu
//     K = "x:idem:<K2>" ikisini AYNI anahtara düşürür — yani bir kurulumun
//     istemcisi, öteki kurulumun kaydını kasten okuyabilir hâle gelir.
//   - Glob imleri ('*', '?', '[') paket godoc'undaki "<önek>:idem:*" ile
//     tarama yordamını bozar: operatörün deseni ya fazlasını siler ya
//     hiçbirini bulmaz. İkisi de üretimde yanlış anahtarla karar vermektir.
//   - Boşluk ve kontrol karakterleri GÖRÜNMEZDİR. Ortam dosyasından sızan tek
//     bir sondaki boşluk, kurulumu kimsenin fark etmeyeceği biçimde BAŞKA bir
//     ad alanına taşır: tüm sayaçlar sıfırlanır ve işlemdeki idempotency
//     kayıtları bir anda yok sayılır.
//
// ASCII DIŞI harfler de reddedilir. Redis anahtarları ikili güvenlidir, yani
// teknik bir engel yok; ama önek insanların redis-cli çıktısında okuyup
// eşleştirdiği bir dizedir ve görsel olarak ayırt edilemeyen karakterler
// (örn. Kiril 'а' ile Latin 'a') iki ayrı ad alanını AYNI gösterirdi.
func dogrulaOnek(keyPrefix string) error {
	if keyPrefix == "" {
		return coreerrors.Invalid(CodeInvalidConfig, "anahtar ad alanı öneki boş olamaz")
	}

	if strings.ContainsFunc(keyPrefix, func(r rune) bool { return !gecerliOnekKarakteri(r) }) {
		return coreerrors.Invalid(CodeInvalidConfig,
			"anahtar ad alanı öneki %q geçersiz karakter içeriyor; "+
				"yalnızca ASCII harf, rakam, '-', '_' ve '.' kabul edilir", keyPrefix)
	}

	return nil
}

// gecerliOnekKarakteri karakterin ad alanı önekinde kullanılabildiğini bildirir.
func gecerliOnekKarakteri(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.'
	}
}

// Bu paketin ürettiği makine okunur hata kodları.
//
// corehttp.CodeRateLimited'dan AYRIDIRLAR: o kod "sınırı aştın" der ve
// istemcinin doğru tepkisi beklemektir; buradakiler "sınırı ölçemedim" der ve
// istemcinin yapabileceği bir şey yoktur.
const (
	// CodeRateLimiterFailed hız sınırı sayacının güncellenemediğini bildirir.
	CodeRateLimiterFailed = "rate_limiter_unavailable"
	// CodeIdempotencyStoreFailed idempotency deposuna erişilemediğini bildirir.
	CodeIdempotencyStoreFailed = "idempotency_store_unavailable"
	// CodeInvalidConfig kurucuya geçersiz ayar verildiğini bildirir.
	CodeInvalidConfig = "redisguard_invalid_config"
)
