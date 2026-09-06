# ADR 0008 — Müşteri kimliği: çerçevenin sınırı, gömen uygulamanın sorumluluğu

- **Durum:** Kabul edildi
- **Tarih:** 2026-09-01
- **Faz:** 10 sonrası (v0.4.0 sertleştirme turu)

## Bağlam

Depo, B2B harcama limitini **uygulanan bir kural** olarak anlatıyor: README'nin
"Kontrol nerede ve neden orada" bölümü, `order` modülünün godoc'u ve
`internal/e2e/b2b_test.go` hep aynı cümleyi kuruyor — limiti aşan alışveriş
siparişe dönüşmez, parası çekilmez, stok hareketsiz kalır. Bunların **hepsi
doğru**. Eksik olan, kuralın hangi **koşulda** uygulandığıydı.

Kural `order.CreateOrder` içinde, `CreateOrderInput.CustomerID` üzerinden
çalışır. O kimlik zincirin başına vitrin sepetinin gövdesinden girer:

```
POST /store/v1/carts  {"country_code":"TR","customer_id":"cus_…"}
  -> cart -> complete_cart saga -> order.CreateOrder(CustomerID: "cus_…")
     -> b2b.interop.SpendingLimitJSON("cus_…")
```

Mağaza yüzeyinin tek kimliği **publishable API anahtarıdır** ve o bir satış
kanalını temsil eder, bir müşteriyi değil: `corehttp.Principal` alanları
`ID`, `Kind`, `Scopes` ve `SalesChannelIDs`'tir — müşteri kimliği **yoktur**.
Yani `customer_id` bir olgu değil, hiçbir kanıt istemeyen bir **sahiplik
iddiasıdır**; `cart/api/store.go` bunu kendi godoc'unda zaten yazıyordu.

Gerçek ikili üzerinde, tek bir publishable anahtarla ölçüldü. Aynı sepet, aynı
istemci, tek fark gövdedeki alan (limit `50_000`, sepet toplamı `76_800`):

| İstek gövdesi | Sonuç |
|---|---|
| `{"country_code":"TR","customer_id":"cus_…"}` | `409 order_spending_limit_exceeded` |
| `{"country_code":"TR"}` | `200`, sipariş açılır (`customer_id: ""`) |

Aynı ölçümün ikinci yarısı: **yabancı bir istemci** başkasının `customer_id`'si
ile alışverişi tamamladığında sipariş o müşterinin adına yazıldı ve harcama
onun penceresinden düştü — sonraki adımda **çalışanın kendi alışverişi**
`409` aldı. Yani iddia yalnızca kaçış değil, adı bilinen bir çalışanın harcama
hakkını **yakma** yoludur ve publishable anahtar bir sır olmadığı için bunu
tarayıcıdaki herkes yapabilir.

Üçüncü bir biçim daha var ve seçenekleri o eliyor: `POST /store/v1/customers`
publishable anahtarla **yeni bir misafir kaydı açar**. Yani "kimlik beyan
etmek zorunludur" demek bile yetmez; kaçan taraf bir istek daha atıp hiçbir
şirkete bağlı olmayan taze bir kimlik üretir.

Dördüncüsü, atfın **sepet açılışına bağlı olmamasıdır**: misafir olarak açılan
bir sepet `POST /store/v1/carts/{id}` ile başkasının `customer_id`'sine
devredilebilir ve sipariş o kimliğe yazılır (ölçüldü: devir `200`, sipariş
kurbanın adına). Bu, "açılışta kimlik iste" biçimindeki her kapıyı da eler:
atıf sepetin ÖMRÜ boyunca beyana dayanır, tek bir anına değil.

## Karar

**Müşteri kimliğinin doğrulanması çerçevenin değil, gömen uygulamanın işidir.
gobit bu turda kimlik doğrulama İNŞA ETMEZ; sınırı çizer, belgeler ve testle
sabitler.**

Sınırın tam ifadesi şudur:

> Harcama limiti, **müşterisini beyan eden** alışverişlere uygulanır. Beyanın
> doğruluğunu gobit doğrulamaz. Beyan etmeyen alışverişe hiçbir limit
> uygulanmaz.

Bunun üç sonucu vardır ve üçü de kayıt altındadır:

1. **Belge gerçeğe çekildi.** README'nin B2B bölümü artık kuralın koşulunu
   ölçülmüş hâliyle yazıyor; `order` modülünün godoc'u, `SpendingPolicy`
   arayüzü ve `CreateOrderInput.CustomerID` aynı sınırı kendi yerlerinde
   tekrarlıyor. Kuralın "her alışverişe uygulandığı" cümlesi hiçbir yerde
   kalmadı.
2. **Sınır testle sabitlendi.** `internal/modules/order/service/spending_test.go`
   içindeki `TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule` ve
   `TestTheSpendingRuleIsAppliedToTheDeclaredCustomer`, sınırın bugünkü yerini
   davranış olarak tutar. İkisi de bir yeteneği değil bir **kararı** korur:
   kimlik doğrulayan bir katman eklendiğinde düşmeleri beklenir ve o gün
   düşmeleri, kararın gerçekten verildiğinin işaretidir.
3. **Gömen uygulamaya düşen iş adlandırıldı.** Vitrin yüzeyini bir müşteri
   oturumuyla koruyan taraf odur: `customer_id` gövdeden değil oturumdan
   gelmeli, uyuşmazlıkta `errors.Forbidden` dönmelidir. Kodda değişecek yer
   dardır ve işaretlidir — `cart/api/store.go`'daki sepet açma, `b2b/api`'nin
   `storeCustomerID` yardımcısı ve `order`'ın `spendingRuleFor` girişi.

## Sonuçlar

**Olumlu.** Deponun en pahalı arıza sınıfı, "kod doğru ama belge yanlış
söylüyor"du; bu ADR onu kapatıyor. B2B kurulumu yapan operatör, limitin neyi
garanti ettiğini kurulumdan **önce** okur: limit, muhasebe disiplinini
kimliğin doğrulandığı bir vitrinde uygular; kimliğin doğrulanmadığı bir
vitrinde ise yalnızca dürüst istemcinin hatasını yakalar.

**Olumlu.** Sınır bir yere **yazıldığı** için ölçülebilir hâle geldi: bugün
`order`'da iki test, README'de bir tablo satırı. Yarın kimlik doğrulama
geldiğinde değişmesi gereken yerlerin listesi tahmin değil, referans.

**Olumsuz.** Çerçeve, kendi başına çalıştırıldığında B2B harcama limitini
**garanti etmiyor** ve bu, özelliğin pazarlanabilir gücünü düşürüyor. Kabul
edildi: yanlış bir garanti vermek, hiç garanti vermemekten pahalıdır — güvenilen
bir limit, güvenilmeyen bir limitten daha tehlikelidir.

**Olumsuz.** Sınır iki katmana dağılmış durumda: kimliği **kabul eden** yer
`cart`'ın vitrin ucu, kuralı **uygulayan** yer `order`. Gömen uygulamanın
ikisini birden okuması gerekir. Karşı önlem, iki yerin de birbirine ve bu
ADR'ye atıf yapmasıdır.

## Reddedilen seçenekler

**Asgari bir kapı: "limitli bir müşterinin sepeti misafir olarak
tamamlanamasın."** Uygulanamaz, çünkü `order` misafir sepetinin limitli bir
çalışana ait olduğunu **bilemez**: elinde boş bir `customer_id`'den başka bir
şey yoktur. Bağı kurabilecek tek alan sepetin e-postasıdır ve o da doğrulanmamış,
istemcinin serbestçe seçtiği (ve `POST /store/v1/carts/{id}` ile
değiştirebildiği) bir alandır. Üstelik `order`'ı bir e-posta → müşteri
çözücüsüne dönüştürmek, müşteri **sayımı** için yeni bir kapı açardı: sipariş
ucunun cevabı "bu e-posta kayıtlı mı" sorusunu yanıtlar hâle gelirdi. Kısacası
kapı, kaçışı kapatmadan yeni bir açık üretirdi.

**`customer_id` beyanını ZORUNLU kılmak.** İlk bakışta misafir kaçışını
kapatıyor. Kapatmıyor: `POST /store/v1/customers` publishable anahtarla yeni
bir misafir kaydı açar, o kayıt hiçbir şirkete bağlı değildir ve limiti de
yoktur. Bedeli ise gerçek: misafir alışverişi vitrinin **varsayılan yoludur** ve
bu değişiklik onu her kurulumda kırardı — kapatmadığı bir açık uğruna.

**Beyanın kanıtını istemek (imzalı müşteri belirteci).** Doğru çözüm bu, ama
bu turun işi değil: belirteci **kim üretir** (auth mu, gömen uygulama mı),
ömrü ne kadardır, misafirden kayıtlı müşteriye geçişte sepet nasıl devrolur,
`corehttp.Principal` müşteri kimliğini taşımaya başladığında admin yüzeyindeki
yetki modeli nasıl etkilenir — hepsi ayrı kararlardır. Yarım bir kimlik
katmanı, olmayan bir kimlik katmanından daha tehlikelidir: doğruladığını sanan
bir sunucu, doğrulamadığını bilen bir sunucudan daha kötü kararlar verir.

**Ortam değişkeniyle kapatılabilir bir "katı mod".** ADR 0007'nin gerekçesiyle
aynı sebeple reddedildi: yanlışlıkla `false` verilen bir anahtar, korumayı
hiçbir hata üretmeden kaldırır. Burada ayrıca yanlış tarafa da düşerdi —
"katı mod açık" diyen bir kurulum, gerçekte doğrulanmamış bir beyana
güvenmeye devam ederdi.
