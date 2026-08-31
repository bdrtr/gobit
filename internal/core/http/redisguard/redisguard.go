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
// Tüm anahtarlar "gobit:" önekiyle yazılır ([RateLimitKeyPrefix],
// [IdempotencyKeyPrefix]). Redis'te anahtar uzunluğu sorun değildir; önek
// aynı Redis'i paylaşan başka verilerle (cache, kuyruk, oturum) karışmayı
// önlemek ve operasyonun "gobit:idem:*" ile tarayabilmesi içindir.
package redisguard

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
