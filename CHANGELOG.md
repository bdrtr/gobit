# Değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/) ölçütlerine,
sürümleme [Semantic Versioning](https://semver.org/lang/tr/) kurallarına
uyar.

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir ve bir uç, daha doğru bir tasarım uğruna taşınabilir.
Sabitlenme `1.0.0` ile olur.

## [Yayımlanmamış]

### Eklendi

- **B2B modülü: şirket, çalışan ve harcama limiti.** Alıcının bir birey değil,
  dönem başına harcama yetkisi sınırlı bir ÇALIŞAN olduğu kurulum. Modül başka
  hiçbir modülü import etmez; çalışan → müşteri bağı yalnızca `core/link`'tedir
  ve `b2b_company_employee` tablosunda `customer_id` sütunu **yoktur** (aynı
  ilişkiyi iki yerde tutmak, ayrışabilecekleri bir yer açardı).

  Kural iki modüle bölünmüştür: **limit** `b2b`'nin, **harcama** (verilmiş
  siparişlerin toplamı) `order`'ın verisidir. İkisi birbirini import edemediği
  için sözleşme JSON'dur, `order` kendi dar arayüzünü (`service.SpendingPolicy`)
  kendi paketinde tanımlar ve somut tipi container'dan `b2b.interop` adıyla
  çözer. Bunun kabul edilen bedeli, **derleyicinin bu sözleşmeyi
  denetlememesidir**: bir alan adı ayrışsaydı iki paketin birim testleri de
  yeşil kalır, üretimde limit sessizce kalkardı — sözleşmenin iki ucu bu yüzden
  gerçek container üzerinden e2e'de birleştirilir.

  Kontrol `order.CreateOrder` içinde, siparişin yazıldığı **işlemin içinde** ve
  müşteri kilidi altında yapılır. `complete_cart` saga'sında `create_order`,
  `authorize_payment`'tan **önce** koştuğu için reddedilen alışverişte para hiç
  yetkilendirilmez; kontrol ile yazma aynı işlemde olduğu için iki eşzamanlı
  sipariş limiti birlikte aşamaz. Kural saga yerine servise konmuştur çünkü bu
  modülde sipariş yaratan tek yol odur — saga'ya konsaydı ileride eklenecek
  ikinci bir çağıran onu sessizce atlardı.

  Limit `nil` ise sınırsız, `0` ise gerçek bir sıfır limittir. Pencere
  takvimdendir (aylık/yıllık, UTC). Şirketin para birimi sepetinkinden farklıysa
  sipariş reddedilir; çevirmek bir kur kaynağı gerektirirdi ve o karar bu
  modülün değildir. Modül kayıtlı değilken davranış b2b hiç yokmuş gibidir.

- **GraphQL'in beş yeni sertleştirme sınırı artık ortam değişkeniyle
  ayarlanıyor.** `GRAPHQL_MAX_FIELD_REPETITION`, `GRAPHQL_MAX_RESPONSE_BYTES`,
  `GRAPHQL_MAX_INTROSPECTION_ROOTS`, `GRAPHQL_MAX_INTROSPECTION_DEPTH` ve
  `GRAPHQL_MAX_SELECTIONS`. Kapılar eklendiğinde yalnızca `graph.Options`
  üzerinden ayarlanabiliyordu; yani operatörün ayarlayamadığı kapılar tam da
  **en yüksek ciddiyetli ikisiydi** (bayt çoğaltması ve iç gözlem seli) ve
  meşru bir ihtiyaç doğduğunda tek çare kodu çatallamaktı.

  `internal/arch`'taki simetri testi bu boşluğu göremiyordu çünkü üç sınırı elle
  karşılaştırıyordu; test artık `graph.Options`'ı **yansımayla gezer** ve
  `Max*` ile başlayan her alanın çekirdekte bir karşılığı olmasını zorlar.
  Mutasyonla doğrulandı: eşlemeden bir girdi çıkarıldığında test düşüyor.

- **GraphQL hata politikası artık hatanın TİPİNE değil KAYNAĞINA bakıyor.**
  Presenter "bu bir `*errors.Error` mi" diye soruyor, olmayanı istemciye olduğu
  gibi veriyordu; oysa çekirdeğin kuralı tam tersidir. Ölçüldü: vitrin servisi
  sınıflandırılmamış bir hata döndürdüğünde yanıt (durum 200)
  `pq: SSL connection error host=db.internal user=gobit password=s3cr3t …;
  SELECT * FROM product_products WHERE id=$1` metnini aynen taşıyordu ve o hata
  **hiçbir yere de yazılmıyordu** — aynı hata REST ucunda 500, `internal_error`
  ve genel mesajla dönüp gerçek metni logluyordu. Artık resolver'ın altından
  gelen her şey, tipli olsun olmasın, `WriteError`'a verilir (maskeleme ve
  loglama kuralı ikinci kez yazılmaz); ayrıştırma, doğrulama ve sınır kapıları
  ise olduğu gibi döner ve sunucu hatası olarak **loglanmaz** — istemcinin
  yazım yanlışı logu doldurabilen bir boru olurdu.

  Aynı ayrımın iki yan sonucu:

  - **`GRAPHQL_INTROSPECTION=false` artık şemayı gerçekten gizliyor.**
    Anahtar `__schema`'yı kapatıyordu ama doğrulayıcı adları perakende
    dağıtmaya devam ediyordu: `prodcts` → `Did you mean "products" or
    "product"?`, `Prodct` → `Did you mean "Product"?`, `limitt` →
    `Did you mean "limit"?`. Doğrulayıcı bütün hataları tek yanıtta topladığı
    için bir istekte onlarca ad denenebiliyor, hız sınırı da bunu bir istek
    sayıyordu. Anahtar artık `SetDisableSuggestion`'ı da kurar ve gqlgen'in
    ulaşamadığı kuralların öneri cümlesi yanıtta kesilir. İç gözlem sorgusu da
    çalıştırılmadan reddedilir (`INTROSPECTION_DISABLED`); `__typename` bir
    kök değildir ve çalışmaya devam eder.
  - **Bozuk JSON gövdesi artık yansıtılmıyor.** gqlgen'in POST taşıması
    çözemediği gövdeyi hata mesajına EKLİYOR (`… body:…`), yani 64 KiB'a kadar
    saldırgan denetimindeki metin yanıta ve yanıtı kaydeden ara katmanların
    loglarına giriyordu. Taşımanın hataları kodsuz geldiği için tanınır ve
    metinleri bizim sabitlerimizle değiştirilir: `REQUEST_DECODE_FAILED` ve —
    boyutunu bildirmeyen istemcinin gövdesi kesildiğinde —
    `REQUEST_BODY_TOO_LARGE`, artık sınırı sayıyla söyleyerek.

- **GraphQL sertleştirmesinde ölçülen dört boşluk kapatıldı: yanıt baytı, iç
  gözlem, sorgu önbelleği ve fragment açılımı.** Dördü de gerçek handler
  üzerinde ölçüldü, tahmin edilmedi.

  1. **Yanıt boyutu hiçbir kapıdan geçmiyordu.** Karmaşıklık modeli alan
     *sayısını* fiyatlıyor, baytı değil:
     `products(limit:100){ items { a0:description … a488:description } }`
     belgesinin maliyeti tam **50.000**, yani tavana oturuyor ve geçiyordu —
     8,5 KiB'lık istek **204,9 MiB** yanıt üretiyordu (24.620 kat) ve hız
     sınırlayıcı bunu *bir* istek sayıyordu. İki kapı eklendi: aynı alanın aynı
     nesne altında kaç kez seçilebileceği (`MaxFieldRepetition`, varsayılan 20;
     kardeş kapsamlı sayım, takma adlar yok sayılır) ve **gerçekleşen** yanıt
     baytı (`MaxResponseBytes`, varsayılan 4 MiB). İkincisi tahmine değil
     ölçüme bakar. Sınıra çarpıldığında **yarım JSON gönderilmez**: hiçbir bayt
     gitmemişken aşan gövde atılıp tam bir hata zarfı yazılır, bir kısmı
     gitmişse bağlantı `http.ErrAbortHandler` ile bırakılır.
  2. **İç gözlem her iki kapının da dışındaydı.** Derinlik sayımı
     `__schema`/`__type` köklerini atlıyor, gqlgen'in karmaşıklık yürüyüşü de
     `__Schema` tipli alanı atlıyordu; yani ölçülen derinlik 0, karmaşıklık 0
     ve operatörün elinde ayar yoktu. Ölçüldü: 302 takma adlı `__schema`
     belgesi 5,00 MiB dönüyordu ve `Options{MaxDepth: 1, MaxComplexity: 1}` ile
     bile 200 alıyordu — aynı ayarla `products { count }` reddedilirken. Artık
     iç gözlem *sayılıyor*: kök sayısı (`MaxIntrospectionRoots`, varsayılan 2)
     ve alt ağacın kendi derinlik tavanı (`MaxIntrospectionDepth`, varsayılan
     15) ayrı ayrı sınırlı. Ayrı tavan, veri sınırının 13'ün üstüne
     çıkarılmasını gerektiren eski gerekçeyi de ortadan kaldırdı.
  3. **Sorgu önbelleği reddedilen belgeleri saklıyordu.** gqlgen belgeyi
     doğrulamadan hemen sonra önbelleğe ekler, sınır eklentileri ise ondan
     sonra koşar; yani servise hiç ulaşmayan belge de yer tutuyordu. Ölçüldü:
     65 KB'lık 100 reddedilmiş belge, `runtime.GC` sonrası **171,8 MiB** kalıcı
     yığın (6,5 MB'lık yüklemenin 26 katı) — üstelik vitrinin gerçek belgeleri
     önbellekten atılıyordu. Önbellek artık girdi *sayısıyla* değil **bayt** ile
     sınırlı (girdi başına 8 KiB) ve bir belge ancak **tüm kapılardan geçtikten
     sonra** saklanıyor. Ayrıca `SetParserTokenLimit` kuruldu (8.192; daha önce
     hiç çağrılmıyordu, yani sınırsızdı): jeton sınırı ayrıştırmanın *içinde*
     çalıştığı için en ucuz kapıdır ve gövde sınırına sığan 302/448 takma adlı
     iç gözlem belgelerini belge sonuna kadar ayrıştırmadan reddeder.

  4. **Fragment açılımı üsseldi ve ağacı gezen her hesap orada asılıyordu.**
     `fragment f(k) on Product { ...f(k-1) ...f(k-1) }` zinciri geçerlidir,
     döngü içermez (doğrulamanın reddettiği tek şey odur) ve 26 seviyede
     **1.127 bayttır** — ama açılımı 2²⁶ seçimdir. Ölçüldü: bu belge ucu on
     saniyede bitiremiyordu. Tuzak tek bir yürüyüşte değildi; derinlik sayımı,
     yeni alan tekrarı sayımı ve gqlgen'in kendi karmaşıklık yürüyüşü, üçü de
     fragment tanımına belleksiz iniyor. Bu yüzden düzeltme bir yürüyüşü değil
     ağacın büyüklüğünü bağlar: `MaxSelections` (varsayılan 10.000, jeton
     sınırının hemen üstü) diğer bütün kapılardan önce koşar ve bütçe bittiği
     anda gezinmeyi yarıda keser — sınırı uygularken tam da sınırın engellediği
     işi yapmamak için.

  Kalibrasyon tablosuna **bayt sütunu** geldi (README ve `limits.go`): eski
  tablo yalnızca alan sayımını ölçüyordu, yani tam da kaçırdığı boyutu hiç
  sormuyordu. Tablodaki karmaşıklık sayıları artık `graph/limits_test.go`
  içinde ölçümle sabitleniyor (ürün sayfası satırı kök sorgu maliyetini
  saymadığı için 1,4 bin yazıyordu; ölçüldüğünde 2.368 çıktı). Yeni sınırlar
  `graph.Options`/`product.Options` üzerinden yapılandırılır.

- **GraphQL ucunun sertleştirilmesi: derinlik, karmaşıklık, gövde ve iç
  gözlem.** Bu uçta bir isteğin maliyetini sorguyu **yazan** belirler; hız
  sınırlayıcı ise takma adlarla yüzlerce kök sorgu taşıyan belgeyi de bir
  istek sayar. Üç kapı eklendi ve her biri ötekinin göremediği belgeyi
  yakalar (kalanları yukarıdaki maddede): derinlik (`GRAPHQL_MAX_DEPTH`, varsayılan 10), karmaşıklık
  (`GRAPHQL_MAX_COMPLEXITY`, varsayılan 50.000) ve 64 KiB'lık gövde sınırı —
  ilk ikisi ancak belge ayrıştırıldıktan sonra ölçülebildiği için ayrıştırma
  maliyetini yalnızca sonuncusu bağlar. Karmaşıklık modeli **liste
  alanlarının maliyetini eleman sayısıyla çarpar** (sabit maliyet, tam da
  pahalı olan sorguyu ucuz gösterirdi) ve kök sorgulara ayrıca bir veritabanı
  gidiş-dönüşü fiyatı yazar. Sınırlar **yükseltilebilir, kaldırılamaz**:
  sıfır/negatif değer geçersizdir ve açılışı durdurur. İç gözlem
  yapılandırılabilir oldu (`GRAPHQL_INTROSPECTION`) ve varsayılanı **açık**
  bırakıldı: şema bu deponun içinde duran bir dosyadır, kapatmak saldırgandan
  bir şey saklamaz ama kod üreteçlerini körleştirir. `product.New` artık
  `product.Options` alır (kırıcı: modül config'i tanımadığı için sınırlar
  kompozisyon kökünden geçirilir).

- **GraphQL vitrin okuma yüzeyi (`POST /store/v1/graphql`).** Katalog ikinci
  bir yüzeyden okunur: `products` ve `product` sorguları, `StoreProduct`'ın
  bugün döndüğü alanlarla. Şema (`internal/modules/product/graph/schema.graphqls`)
  elle yazılan sözleşmedir, Go tarafı ondan üretilir (gqlgen, `make gen`).
  Resolver'lar **vitrin servisini** çağırır — depoya inilmez, yeni SQL
  yazılmaz: satış kanalı görünürlük kuralının ikinci bir uygulaması, ayrıştığı
  gün kataloğu sızdırırdı. Kanal kimlikleri `Principal`'dan okunur ve şemada
  argümanı **yoktur**; uç `/store/v1` altında olduğu için publishable anahtar
  ve hız sınırı yığından otomatik gelir. Yazma yüzeyi yok, GET yok
  (yalnızca POST; yanıt kanala göre değiştiğinden GET'in önbellek getirisi
  yoktur, bedeli vardır). Fiyat ve stok, sahibi başka modüller olduğu için
  JSON skalarıdır; hata gövdesi çekirdeğin `WriteError`'ından geçirilir, yani
  maskeleme kuralı ve hata kodları REST ile aynıdır.

- **`FileProvider` ve `file` modülü — plan Bölüm 5.6 tamamlandı.** Dört
  sağlayıcı soyutlamasının sonuncusu. `POST /admin/v1/uploads` (multipart) →
  dönen adres → `GET /files/{anahtar}`. Üretilen URL mevcut ürün görseli
  akışına doğrudan takılır, yani `product` modülüne dokunmadan gerçek bir
  tüketici yolu oluşur.
  Bu, depoda istemciden **rastgele bayt** kabul edilen ilk yerdir; güvenlik
  kuralları yapısal: depo anahtarı ÜRETİLİR (istemcinin dosya adı hiçbir yol
  ifadesine girmez, yol geçişi imkânsız), içerik tipi istemciye sorulmaz
  içerikten tespit edilir, izin listesi (yasak listesi değil) ve SVG dışarıda,
  boyut sınırı hem gövdede hem dosyada zorlanır, sunumda `Content-Type`
  saklanan tipten yazılır ve `nosniff` her yanıtta bulunur.

### Düzeltildi

Düşmanca bir güvenlik incelemesinin çıkardığı altı bulgu:

- **Idempotency middleware yükleme akışını öldürüyordu.** `Idempotency-Key`
  taşıyan bir multipart isteğin TÜM gövdesi parmak izi için belleğe alınıyor,
  akışın anlamı yok oluyor ve middleware'in 1 MiB tamponu yükleme ucunun kendi
  sınırından ÖNCE devreye giriyordu — istemci, ayarladığı sınırın altında
  "gövde çok büyük" alıyordu. Akışlı gövdeler artık kaydedilmez.
- **`/files` koruma yığınının dışındaydı**: kimliksiz VE kotasız, üstelik her
  istek bir veritabanı okuması. Kimliksiz olmak korumasız olmak değildir;
  `GuardOptions.OpenPrefixes` eklendi. Sağlık uçları bilinçli olarak dışarıda.
- **Çok aralıklı `Range` ile ~11x yanıt büyütmesi**: `ServeContent` aralıkların
  toplam baytını sınırlar, SAYISINI değil. Tek aralık korunur, çoklu olanda
  başlık silinir.
- **`Cache-Control: immutable` yanlıştı**: anahtar tekrar kullanılmaz ama
  içerik SİLİNEBİLİR; paylaşılan bir önbellek silinen dosyayı bir yıl daha
  sunardı. Süre bir saate indirildi, `immutable` kaldırıldı.
- **`FILE_ALLOWED_TYPES` tarayıcıda çalışan tipleri kabul ediyordu.**
  `text/html` yazan bir kurulumda zincir çalışır ve depolanmış XSS olur;
  `nosniff` bunu DURDURMAZ, çünkü yanıt gerçekten o tiptir.
- Geçici yükleme dosyasının temizliği defer edilmemişti (panikte sızıntı).

- **`NotificationProvider` ve `notification` modülü.** Plan Bölüm 5.6 DÖRT
  sağlayıcı soyutlaması sayıyor (payment, fulfillment, notification, file);
  kodda yalnızca ikisi vardı. Bu iş üçüncüsünü kapatır ve aynı anda ikinci bir
  boşluğu da: `order.placed` yayımlanıyordu ama **tek abonesi yoktu** — arama
  eklentisi ürün olaylarını dinliyor, sipariş olaylarını değil. Bildirim, o
  olayın ilk gerçek tüketicisi.
  Varsayılan sağlayıcı `log`'dur ve **gerçekten göndermediğini söyler**: WARN
  seviyesinde "bildirim GÖNDERİLMEDİ" yazar, alıcıyı loglamaz ve şablon
  verisinin değerlerini değil yalnızca anahtarlarını basar. Sessiz bir "gitti"
  yalanı, sipariş onayının müşteriye ulaştığını sanmak demek olurdu.
  Bilinmeyen bir `NOTIFICATION_PROVIDER` adı açılışı durdurur.
  Teslim günlüğü **alıcı adresini saklamaz**: e-posta zaten sipariş kaydında
  duruyor ve ikinci bir kopya, silinmesi gereken yerlerin sayısını artırırdı.
  `(şablon, referans)` benzersizdir — aynı sipariş için iki kez bildirim
  gitmez.
  Abone e-postayı **olaydan değil kayıttan** okur (olay yükü kalıcı akışa PII
  koymaz); bunun için `order.interop` dar bir okuma yüzeyi açtı
  (`OrderContactJSON`). Uçtan uca test tam olarak bu ayrımı çiviler.

- **Smoke testleri: gerçek süreç, gerçek migration, gerçek sinyal.**
  Birim + entegrasyon testleri (~%76 kapsam) ve lint TEMİZ geçerken uygulama
  elle çalıştırıldığında dört arıza çıkmıştı; dördü de `main.go`'nun
  kablolamasında, açılıştaki migration'larda, config yüklemesinde ve sinyal
  işlemede saklanıyordu. `internal/e2e` bunları göremez: `httptest` ile
  router'ı sürer, yani gerçek bir açılış DEĞİLDİR.
  `internal/smoke` sunucu ikilisini derleyip **süreç olarak** çalıştırır ve
  o hata sınıfını CI'a bağlar: soğuk açılış + README akışı, üç örneğin aynı
  boş veritabanına eşzamanlı açılışı (tohum yarışının regresyonu), beş yanlış
  yapılandırmanın açılışta anlaşılır mesajla durması, OTLP adresinin iki
  biçiminin de kabul edilmesi ve `METRIC_EXPORT_INTERVAL` ad çakışmasının geri
  gelmemesi, SIGTERM sonrası çıkış kodu 0 ile düzgün kapanış.
  İki regresyon **mutasyonla** doğrulandı: tohum düzeltmesi geri alındığında
  eşzamanlı açılış testi, ad çakışması geri getirildiğinde izleme testi düşüyor.
  `make smoke` ile çalışır; CI'da AYRI bir iş — "entegrasyon düştü" ile
  "uygulama açılmıyor" aynı satırda görünmemeli.

### Kaldırıldı

- **`cart_customer`, `cart_region`, `order_customer`, `order_region` link
  tanımları.** Dördü de her sepette/siparişte YAZILIYOR, hiç GEZİLMİYORDU.
  Bu **kayıp bir özellik değildir**: bu bağların taşıdığı her okumayı zaten
  sütunlar yapıyor. `carts.region_id` / `carts.customer_id` ve
  `orders.region_id` / `orders.customer_id` hem kaynaktır hem indekslidir;
  müşteri ve bölge süzgeçleri (`ListCarts`, `ListOrders`,
  `order/queries/orders.sql`) tam olarak o sütunlardan çalışır. Link tablosu
  aynı ilişkinin ikinci bir kopyasıydı; satır yazıyor, kardinalite kısıtının
  bedelini ödüyor ve karşılığında hiçbir davranış üretmiyordu.

  Bu, bilinçli bir tasarım kararının geri alınmasıdır. Bağlar "Query katmanına
  açılan ayna" olsun diye bildirilmişti ve `ManyToMany` kardinalitesi tam da o
  ayna uğruna seçilmişti (tekillik zaten sütunda garantiliydi). Aynaya bakan
  bir okuyucu hiç çıkmadı: ne bir `query.Expansion`, ne bir modül API'si.
  Bulan şey `internal/arch/tuketici_test.go`'daki `TestLinkTanimlariGeziliyor`
  değişmezidir — "üretilen her yeteneğin bir tüketicisi vardır" kuralının
  link yüzeyi. Aynı sınıfın önceki vakası ürün ↔ satış kanalı arızasıydı.

  Silme telafisi de gitti ve **kod bundan sadeleşerek çıktı**: bağ sepet
  satırıyla aynı işlemde olmadığı için `CreateCart` bağ kurulamayınca sepeti
  geri alıyor, `UpdateCart` müşteri devrini geri alıyor, `CreateOrder` ise
  siparişi yazmadan ÖNCE bağlanıp yazma düşünce bağı temizliyordu. Şimdi her
  ikisi de tek yazma işlemidir; telafi yolu, telafinin telafisi ve
  "hayalet sipariş" penceresi diye bir şey kalmadı. `cart` ve `order`
  modüllerinin `core.link` bağımlılığı da tamamen kalktı (`service.Linker`,
  `Options.Links`, `Definitions()`).

  **Veritabanı: migration YAZILMADI, bu bilinçlidir.** Link şeması
  migration'ın değil, açılıştaki bildirimin ürünüdür ve sahibi `core/link`'tir
  (ADR 0005); bir modülün migration'ının başka bir alt sistemin tablosunu
  düşürmesi, `b2b`'nin down migration'ında da bilinçle yapılmayan şeydir.
  Somut sonuçlar:

  - `link_cart_customer`, `link_cart_region`, `link_order_customer` ve
    `link_order_region` tabloları var olan kurulumlarda **yetim kalır**.
    Zararsızdırlar: hiçbir kod yolu onlara dokunmaz ve işaret ettikleri
    kimlikler bir daha üretilmez. Temizlik OPERASYONEL bir karardır ve elle
    yapılır (`DROP TABLE IF EXISTS link_cart_customer, link_cart_region,
    link_order_customer, link_order_region;`). Otomatikleştirilmemesinin
    sebebi ADR 0005'te yazılıdır: tabloyu koda bakarak düşürmek, bir dağıtım
    hatası yüzünden geçici olarak kaybolan bir tanımın tüm bağları silmesi
    demek olurdu — ve **silinen satır geri gelmez**.
  - `link_definitions` tablosunda bu dört ada ait satırlar kalır. Açılışta
    **hiçbir çakışma üretmezler**: `LinkService.Define` yalnızca kendi
    bildirdiği adın satırını okuyup karşılaştırır (upsert + `RETURNING`,
    bkz. `internal/core/link/service.go`), defteri koda karşı taramaz.
    Koddan gelmeyen bir satır hiç okunmaz. Tek koşullu sonuç şudur: ileride
    aynı ADLA fakat farklı uçlarla bir link bildirilirse açılış
    `errors.Conflict` ile durur — ki bu, defterin görevini yapmasıdır, bir
    arıza değil.

## [0.3.0] — 2026-08-31

API artık kendini anlatıyor: şemadan çalışan bir istemci üretilebiliyor.

**Kırıcı değişiklik YOKTUR.** `internal/core/openapi` paketinin dışa açık
API'si yalnızca büyüdü (metot eklendi, hiçbiri kaldırılmadı) ve kaldırılan
`List` bileşeni v0.2.0'da zaten yayımlanmıyordu — eklenmesi ve kaldırılması
aynı yayımlanmamış pencerede oldu, yani kimsenin ürettiği bir istemciye
girmedi.

### Eklendi

- **Tüm API yüzeyi anlatıldı (196 uç).** Şema artık her ucun ne aldığını ve ne
  döndüğünü söylüyor. Ölçüldü: `openapi-generator v7.10.0` şemayı **sıfır
  bulguyla** doğruluyor ve 237 modelli bir TypeScript istemcisi üretiyor.
  `POST /admin/v1/users` örneği farkı özetler —
  öncesi `postAdminV1Users(): Promise<void>` (gövdesiz, dönüşsüz, kullanılamaz),
  sonrası `postAdminV1Users(req: PostAdminV1UsersRequest): Promise<…201Response>`.
  `make openapi-client DIL=…` ile istemci üretilebilir; depoda SDK
  VENDORLANMAZ, çünkü şema router'dan üretildiğine göre ikinci bir artefaktı
  sürümlemek ve senkron tutmak gereksiz bir yük olurdu.
- **OpenAPI şeması artık gövdeleri anlatıyor.** Şema sözdizimsel olarak
  geçerliydi ama anlamsal olarak BOŞTU: `Doc.Describe` hiçbir yerde
  çağrılmıyordu ve her işlem yalnızca `operationId`, `tags`, `security` ve
  GENEL hata yanıtları (401/422/429/500) taşıyordu. `POST /store/v1/carts`
  için ne `requestBody` ne de bir 2xx yanıtı vardı — bir istemci üreteci
  bundan her şeyi `any` olan, dönüş tipi `void` metotlar üretirdi.
  Gövde şemaları artık Go tiplerinden **yansımayla türetiliyor**: elle yazılan
  bir alan listesi, DTO'ya alan eklendiği gün eksik kalır ve kimse fark etmez.
  Türetme `encoding/json`'un davranışını taklit eder (etiket, `omitempty`,
  dışa kapalı alanlar, gömülü struct düzleştirmesi ve **gölgelenme**).
  Modüller opsiyonel `openapi.Describer` arayüzüyle kendi uçlarını anlatır;
  `module.Module` sözleşmesi değişmedi. Bugün `cart` ve `product`'ın vitrin
  uçları anlatılıyor.

### Değişti

- **Kullanılmayan `List` bileşeni yayımlanmıyor.** Gerçek üreteç onu
  "kullanılmayan model" diye bildirdi; üretilen her istemcide ölü bir sınıftı.
  Anlatılmamış liste uçlarına varsayılan olarak bağlamak cazipti ama yanlış
  olurdu: bir ucun gerçekten liste döndüğü doğrulanmadan şemaya yazılamaz.
- **Şema bileşen adları normalleştirildi** (`cartDTO` → `Cart`). Bileşen adı
  bir iç ayrıntı değil yayımlanan sözleşmedir; istemci üreteçleri ondan sınıf
  adı üretir. Normalleştirilmeseydi aynı belgede `StoreProduct` (dışa açık) ile
  `cartDTO` (dışa kapalı) yan yana durur, üretilen istemcide iki farklı
  adlandırma düzeni olurdu.

## [0.2.0] — 2026-08-31

Yol haritası bittikten sonra bulunanlar. Ortak bir örüntü var: bu sürümdeki
işlerin çoğu yeni özellik değil, **kurulmuş ama tüketicisi olmayan**
yeteneklere tüketici yazmaktır — satış kanalı doğrulanıyor ama okunmuyordu,
event bus hazırdı ama tek olay vardı, `Host.AddModule` hiç kullanılmamıştı.

### Kırıcı değişiklikler

`0.x` boyunca minor sürümlerde kırıcı değişiklik olabilir. Bu sürümdekiler
yalnızca **modülleri gömen** kodu etkiler; HTTP API'sini kullanan istemciler
etkilenmez.

- `product` modülü `Register` sırasında `core.eventbus` servisini ZORUNLU
  kılar; yoksa açılış durur. Sessizce atlamak, katalog çalışırken indeksin
  sessizce eskimesi demekti.
- `product/repository.Store` arayüzü büyüdü (`ProductVisibleInSalesChannels`,
  `VisibleProductIDs`); kendi uygulamasını yazan kod
  bunları eklemelidir.
- `product/service.GetStoreProduct` artık satış kanalı kimliklerini de alır.
- `workflows/checkout`'un `Inventory` ve `Fulfillment` dar arayüzleri büyüdü
  (`LocationsWithStock`, `SelectLocation`).

### Eklendi

- **Alan olayları ve gerçek bir eklenti: arama.** `order.placed` depodaki TEK
  olaydı — event bus tamamen kurulu (bellek içi + Redis Streams, consumer
  group, XACK), plan Bölüm 5.4'te çekirdek sözleşme, `Host.Subscribe`
  eklentiler için hazır, ama abone olunacak neredeyse hiçbir şey yoktu.
  `product` artık `product.created` / `product.updated` / `product.deleted`
  yayımlıyor (sipariş olaylarının doktrini: dar yük, tüm değerler dize, kalıcı
  akışa kişisel veri yok).
  `plugins/searchpg` bunları tüketen ilk **gerçek** eklenti: kendi modülünü,
  kendi tablosunu ve migration'ını getiriyor (`Host.AddModule` bugüne kadar hiç
  kullanılmamıştı), PostgreSQL tam metin araması yapıyor ve
  `GET /store/v1/search` ile `POST /admin/v1/search/reindex` uçlarını açıyor.
  Dış servis bilinçli olarak yok: eklenti sınırı sayesinde ileride
  Meilisearch/OpenSearch'e geçmek başka hiçbir yeri değiştirmez.
  **Arama, kanal süzmesinin bypass'ı değildir** — eklenti yalnızca kimlik
  indeksler, kayıtları `product.interop` getirir ve görünürlük kuralı tek
  yerde kalır.
- **Çoklu depo: stok satır başına, doğru depodan ayrılır.** `complete_cart`
  saga'sındaki "TEK LOKASYON VARSAYIMI" kaldırıldı — kod bu değişikliği
  "Faz 7'de" diye vaat ediyordu, Faz 7 bitmiş ve varsayım durmuştu.
  `CompleteCartInput.LocationID` artık **opsiyoneldir**: dolu ise eski davranış
  aynen korunur, boş ise lokasyon satır başına seçilir ve bir siparişin
  satırları farklı depolardan ayrılabilir.
  İş bölümü bilinçlidir — "hangi depolarda yeterli stok var" bir **stok
  olgusudur** (`inventory.interop.LocationsWithStock`), "hangisinden
  gönderelim" bir **kargo kararıdır** (`fulfillment.interop.SelectLocation`).
  Seçilen depo ayırma anında tükenmişse sıradaki adaya geçilir; bu yalnızca
  çakışmada olur, diğer hata sınıflarında ısrar edilmez.
- **`product↔sales_channel` bağı ve vitrin katalog süzmesi.** Planın "önemli
  linkler" listesindeki son eksik bağ kuruldu: publishable anahtarın bağlı
  olduğu kanal artık kataloğu gerçekten belirliyor. Önceden anahtar
  doğrulanıyor ve `Principal.SalesChannelIDs` doluyordu ama hiçbir modül
  okumuyordu — her anahtar aynı kataloğu görüyordu.
  Süzgeç veritabanında uygulanır (`EXISTS`/`NOT EXISTS`), böylece sayfalama ve
  toplam sayaç süzülmüş küme üzerinde çalışır. Kanal kimlikten okunur, sorgu
  dizesinden ASLA.
  Yeni uçlar: `POST`/`DELETE`/`GET /admin/v1/products/{id}/sales-channels`.

## [0.1.0] — 2026-08-31

Planın Faz 0–9 yol haritasının tamamı. Tek binary olarak çalışan, modüller
arası derleme zamanı bağımlılığı OLMAYAN bir headless commerce çekirdeği.

### Eklendi

**Çekirdek**
- Modül sözleşmesi ve yaşam döngüsü (`Register` → migration → `Routes`),
  el yazması DI container ([ADR 0002](docs/adr/0002-di-container-el-yazmasi.md)).
- Module Links — modüller arası ilişki foreign key OLMADAN; kardinalite
  veritabanı kısıtıyla zorlanır
  ([ADR 0005](docs/adr/0005-link-semasi-migration-disinda.md)).
- Query katmanı — cross-module okuma; N+1 yapısal olarak imkânsız
  ([ADR 0004](docs/adr/0004-query-veri-erisimi.md)).
- Saga motoru — ters sırada telafi, retry, idempotency anahtarı, panik
  izolasyonu; yürütme durumu Postgres'te.
- Event bus — bellek içi (geliştirme) ve Redis Streams (üretim).
- Modül başına ayrı migration klasörü ve versiyon tablosu; iptal edilebilir
  migration ([ADR 0003](docs/adr/0003-migration-iptali.md)).

**Commerce modülleri**
- Katalog: `product`, `pricing`, `inventory`.
- Sepet: `cart`, `customer`, `region`.
- Sipariş: `payment`, `order` — `complete_cart` saga'sı.
- Faz 7: `fulfillment`, `promotion`, `tax`.
- Kimlik: `auth` — yönetim kullanıcısı, JWT oturumu, publishable/gizli API
  anahtarı, satış kanalı.

**Güvenlik**
- İki yüzey, iki kimlik: `/admin/v1` Bearer jeton ya da gizli anahtar,
  `/store/v1` publishable anahtar.
- Yetki (scope) TÜM modüllerde uç uç zorlanır (`<modül>:read` /
  `<modül>:write`, `admin` üst yetki); yetki yükseltme ayrıca servis
  katmanında engellenir.
- İlk yönetici tohumu (`ADMIN_BOOTSTRAP_*`) — yalnızca hiç kullanıcı yokken
  çalışır, eşzamanlı açılışta yarışı yutar.
- Oturum iptali: parola değişimi ve `POST /admin/v1/auth/logout`; ikisi de
  çağıranın TÜM oturumlarını düşürür.

**Sertleştirme**
- Hız sınırı, idempotency ve kimlik middleware'leri; arıza davranışı bileşene
  göre değişir ([ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md)).
- `GUARD_BACKEND=redis` ile paylaşılan hız sınırı ve idempotency deposu —
  çok örnekli dağıtım için.
- OpenTelemetry trace + metrik; toplayıcı verilmezse izleme gerçekten kapalı.
- Eklenti sistemi (derleme zamanı kaydı) ve `payment-stripe` iskeleti.
- Router ağacından üretilen OpenAPI şeması (`/openapi.json`).

**Doğrulama**
- Mimari değişmezler test ile zorlanır: modül izolasyonu, cross-module FK
  yasağı, eklenti izolasyonu, godoc biçimi, para tam sayılığı.
- Uçtan uca testler modülleri ÜRETİM kablolamasıyla kurar; yetki değişmezi
  router ağacını gezerek her yönetim ucunu denetler.
- Temel yük testi (`make load-test`).

### Düzeltildi

Bu sürüm yayımlanmadan önce, uygulamayı gerçekten çalıştırarak bulunan ve
yalnızca test koşarak görünmeyen üç arıza:

- **Eşzamanlı açılışta tohum yarışı.** Birden çok örnek boş bir veritabanına
  aynı anda açıldığında biri dışındaki hepsi `admin_bootstrap_failed` ile
  ölüyordu. Çakışma artık bir arıza değil yarış olarak ele alınır.
- **`OTEL_EXPORTER_OTLP_ENDPOINT` belirtim biçimini SESSİZCE yutuyordu.**
  `http://host:4317` verildiğinde uygulama "izleme kuruldu" logluyor ve
  hiçbir span göndermiyordu. Artık iki biçim de kabul edilir.
- **`OTEL_METRIC_EXPORT_INTERVAL` adı OpenTelemetry ile çakışıyordu.**
  Belirtim milisaniye tamsayı ister, bu paket Go süresi okur; belirtime uyan
  değer uygulamayı açılışta düşürüyordu. Değişken `METRIC_EXPORT_INTERVAL`
  oldu.

### Düzeltildi

- **`make migrate-up` dokuz faz geriden konuşuyordu.** Operatöre "Faz 1'de
  core/db migration runner'ı devreye girecek" diyordu; Faz 1 dokuz faz önce
  bitmiş ve migration'lar açılışta otomatik uygulanıyordu. Hedefler gerçeğe
  uyduruldu ve geri alma yolunun OLMADIĞI açıkça yazıldı.
- **Kapsam ölçümü 22 puan yanıltıyordu.** CI `-coverpkg` olmadan ölçüyordu,
  yani bir paketi BAŞKA paketin testi kapsadığında sayılmıyordu. Artık iki
  ayrı sayı raporlanıyor: yalnızca birim (~%55) ve birim + entegrasyon (~%76).
- **Arama yolunda N+1.** Görünürlük kimlik başına sorulurken toplu sorguya
  çevrildi; aynı SQL şablonundan üretildiği için kural hâlâ tek.
- **Çoklu depoda yarış.** Aday listesi kilitsiz okunur, ayırma kilitlidir;
  seçilen depo bu arada tükenmişse sıradaki adaya geçilir. Önceden sipariş
  tümden düşerdi — üstelik başka depoda stok dururken.

### Bilinen sınırlar

- Oturum iptali yalnızca toptan; tek cihaz düşürülemez.
- Modüller arası imzalar derleme zamanında denetlenmez
  ([ADR 0001](docs/adr/0001-modul-arasi-iletisim.md)'in kabul edilen bedeli).
- Stokta tek lokasyon varsayımı.
- **Kanal ataması olmayan ürün tüm kanallarda görünür.** Kural bilinçli ve
  geriye uyumludur, ama bir tuzağı vardır: son kanal bağını silmek ürünü
  gizlemez, tüm vitrinlere açar. Gizlemek için `status` kullanılmalıdır.
  Katı alternatif ("ataması olmayan hiçbir kanalda görünmez") bir sonraki
  minor sürüm için düşünülmeli — açıldığı gün mevcut katalogları boşaltır.
- **Migration geri alma yolu yok.** Her modülün `.down.sql` dosyaları vardır
  ve geri alınabilirlikleri testle denetlenir, ama onları çağıracak bir yüzey
  yoktur; geri alma elle yapılır. İleri yön açılışta otomatiktir.
- Yük testi süreç içidir; kapasite planı üretmez.

[Yayımlanmamış]: https://github.com/bdrtr/gobit/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/bdrtr/gobit/releases/tag/v0.3.0
[0.2.0]: https://github.com/bdrtr/gobit/releases/tag/v0.2.0
[0.1.0]: https://github.com/bdrtr/gobit/releases/tag/v0.1.0
