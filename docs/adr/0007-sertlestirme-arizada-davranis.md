# ADR 0007 — Sertleştirme bileşenleri arızalandığında ne olur

- **Durum:** Kabul edildi
- **Tarih:** 2026-08-24
- **Faz:** 9

## Bağlam

Faz 9 üç koruma bileşeni getiriyor: hız sınırlama, idempotency ve (Faz 8'den)
kimlik doğrulama. Üçü de "istek geçsin mi geçmesin mi" sorusuna cevap veren
middleware'ler. Üçü de yapılandırılmamış ya da arızalı olabilir:

- `RateLimit` bir `RateLimiter` alır; Redis tabanlı bir uygulama erişilemez
  olabilir, ya da hiç yapılandırılmamış (nil) olabilir.
- `Idempotency` bir `IdempotencyStore` alır; aynı şekilde.
- `RequireAdmin` bir `Authenticator` alır; auth modülü kayıtlı olmayabilir.

Kolay olan, üçü için de tek bir kural koymaktı: "bileşen yoksa geç" ya da
"bileşen yoksa reddet". İkisi de yanlış.

## Karar

**Her bileşen kendi başarısızlık modeline göre davranır. Tek tip kural yok.**

| Bileşen | Yapılandırılmamış (nil) | Çalışma anında arıza |
|---|---|---|
| `RequireAdmin` / `RequireStore` | **Her isteği reddet** (401) | Reddet |
| `RateLimit` | **No-op** (geçir) | **Geçir** (fail-open) + uyarı logu |
| `Idempotency` | **No-op** (geçir) | Ayırmada: reddet. Kayıtta: anahtarı serbest bırak |

Gerekçe, "bu bileşen olmadan ne bozulur" sorusunun her satırda farklı
cevaplanmasıdır:

**Kimlik doğrulama açık kalırsa sistem sessizce SAVUNMASIZ olur.** Kimsenin
fark etmediği bir açık, ancak istismar edildiğinde görünür. Bu yüzden
`auth == nil` bir yapılandırma hatasıdır ve gürültülü biçimde başarısız olmalı:
korumasız bir admin yüzeyi asla sessizce açık kalmamalı.

**Hız sınırlayıcı kapalı kalırsa sistem yalnızca KORUMASIZ olur, yanlış
olmaz.** Hız sınırı ürünün doğruluğu için değil, kötüye kullanıma karşı vardır.
Redis düştüğünde tüm trafiği reddetmek, hız sınırlayıcıyı tam bir kesinti
kaynağına çevirirdi: koruduğu servisi kendisi çökertirdi. Arıza penceresinde
sınırın uygulanmaması kabul edilen bedeldir ve `WARN` seviyesinde loglanır.

**Idempotency deposu kapalıysa tekrar denemeler ÇİFT İŞLEM üretir — bu bir
doğruluk sorunudur.** Yine de `store == nil` no-op'tur, çünkü depo yokken
"idempotency zorunlu" demek, anahtar göndermeyen tüm mevcut istemcileri bir
gecede kırardı.

Depo VARSA, çalışma anı hatasının sonucu isteğin hangi ANINDA oluştuğuna
bağlıdır ve bu ayrım kaçınılmazdır:

- **Ayırma (`Begin`) sırasında hata**: handler henüz çalışmadı, hiçbir yan etki
  yok. Hata istemciye iletilir ve istek reddedilir. "Kaydedemedim ama yine de
  işledim" demek, sessizce ikinci bir tahsilat riskini kabul etmek olurdu.
- **Kayıt (`Complete`) sırasında hata**: handler çalıştı, yanıt istemciye
  ÇOKTAN yazıldı. Artık status kodu değiştirilemez. Yapılabilecek tek doğru
  şey ayırmayı serbest bırakmaktır; aksi hâlde anahtar sonsuza dek "işlemde"
  kalır ve istemci ne yanıt alabilir ne tekrar deneyebilir. Serbest bırakmanın
  bedeli tekrarın yeniden işlenme ihtimalidir — kalıcı kilitten iyidir.

Aynı gerekçeyle, tampon sınırını aşan bir yanıt da kaydedilmez: eksik bir
gövdeyi kaydedip sonra çalmak, istemciye KESİK ve bozuk bir yanıt vermek
olurdu. Bozuk yanıt, tekrarın yeniden işlenmesinden çok daha kötüdür.

## Sonuçlar

**Olumlu.** Her bileşenin nil davranışı, o bileşenin ne için var olduğunun
doğrudan yansıması. Kodda bu asimetri godoc'ta açıkça karşılıklı referansla
belgeleniyor (`RateLimit`, `RequireAdmin`'e atıf yapıyor) ki okuyan kişi
tutarsızlık sanmasın.

**Olumsuz.** Üç bileşen benzer imzalara sahip ama farklı davranıyor; bunu
bilmeyen biri `RateLimit(nil, nil)` yazıp korunduğunu sanabilir. Karşı önlem:
her davranış için ayrı bir test var ve testlerin adları davranışı söylüyor
(`TestRateLimitNilSinirlayiciNoOptur`, `TestIdempotencyNilDepoNoOptur`).

**Bellek içi uygulamalar tek örnekliktir.** `MemoryLimiter` yatay ölçeklendiğinde
gerçek sınır örnek sayısıyla çarpılır — bu bir *hız* sorunudur, tolere edilebilir.
`MemoryIdempotencyStore` yatay ölçeklendiğinde aynı anahtarla farklı örneklere
düşen iki istek İKİ KEZ işlenir — bu bir *doğruluk* sorunudur, tolere edilemez.
Yani çok örnekli dağıtımda paylaşılan idempotency deposu ZORUNLUDUR, paylaşılan
hız sınırlayıcı ise isteğe bağlıdır. İkisi de `internal/core/http` içindeki
arayüzler üzerinden değiştirilebilir.

## Reddedilen seçenekler

**Her şey için fail-closed.** Redis'in kısa bir kesintisi tüm mağazayı
kapatırdı. Koruma bileşeninin kendisi en büyük kesinti kaynağı olurdu.

**Her şey için fail-open.** Auth arızasında istekleri geçirmek, kimlik
doğrulamayı tamamen anlamsız kılardı: saldırgan yalnızca auth'u yormayı
başarmakla admin olurdu.

**Idempotency deposu arızasında geçirmek.** Cazip, çünkü "en azından istek
işlenir". Ama idempotency'nin tek varlık sebebi tekrarları yakalamak; depo
yazamıyorken işlemi tamamlamak, tam da korumaya çalıştığı çift tahsilatı
üretmenin en olası yolu.
