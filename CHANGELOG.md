# Değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/) ölçütlerine,
sürümleme [Semantic Versioning](https://semver.org/lang/tr/) kurallarına
uyar.

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir ve bir uç, daha doğru bir tasarım uğruna taşınabilir.
Sabitlenme `1.0.0` ile olur.

## [Yayımlanmamış]

### Kırıcı değişiklikler

`0.x` boyunca minor sürümlerde kırıcı değişiklik olabilir. Aşağıdaki
**mağaza API'sini** kullanan istemcileri doğrudan etkiler.

- **`POST /store/v1/carts` gövdesinden `region_id` KALDIRILDI; yerine
  `country_code` ZORUNLU oldu.** Alanı gönderen istek artık `422` alır (gövde
  tanınmayan alanı reddeder). Sepetin bölgesini ve para birimini sunucu,
  müşterinin ÜLKESİNDEN türetir.

  Kaldırmanın iki sebebi vardır ve ikisi de aynı ölçüttendir ("gövdeye konan
  şey müşterinin belirleyebildiği şeydir"):

  1. `region_id` müşterinin ifade etmek istediği şey **değildir**. Müşteri bir
     ülke seçer (ya da tarayıcısı söyler); bölge, o ülkenin sunucudaki
     karşılığıdır ve eşlemeyi operatör kurar. İstemciye bir iç varlık kimliği
     yazdırmak, `unit_price`/`currency_code` ile kapatılan "sunucunun verisini
     istemciden almak" sınıfının daha yumuşak bir biçimidir; bölge sepetin
     **vergi oranını** seçtiği için sonucu da kozmetik değildir.
  2. Türetmeyi zaten yapan bir akış vardı — `internal/workflows/cart`'ın
     `create_cart`'ı ülke kodundan hem bölgeyi hem para birimini çözer — ve
     vitrin ucu onu **atlıyordu**. Aynı işlem için iki sözleşme, işletmecinin
     gördüğü yol da ham olan.

  Sessizce yok saymak yine seçilmedi: istemci gönderdiğini sanır, sunucu başka
  bir bölgede sepet açardı — ve o sepet başka bir vergi oranıyla, başka bir
  fiyat listesinden fiyatlanırdı.

  Yeni hata yüzeyi: bölgesi olmayan geçerli bir ülke `404`
  (`country_has_no_region` / `country_not_found`), biçimi bozuk ya da boş bir
  kod `422`'dir. Ayrım korunur çünkü ikisi farklı düzeltmeler ister: birinde
  müşteri başka bir ülke seçer, diğerinde istemci gövdesini düzeltir.

- **Bağlama, satır uçlarındaki kalıbın AYNISIDIR ve yeni bir mekanizma
  getirmez.** `cart` kendi paketinde üçüncü bir dar arayüz tanımlar
  (`api.CartOpening`), somut akışı container'dan `workflows.cart.interop`
  adıyla **tembel** çözer ve çözülemezse **kapalı** arızalanır: `500`, sepet
  yazılmaz. Bunun bir sonucu olarak `cart` modülünün başka bir modülü adla
  çözdüğü tek yer de kapandı — `api.RegionCurrencyReader` ve `region.service`
  bağı **kaldırıldı**, çünkü para birimini artık akış türetiyor. Modülün
  `LinePricingName` sabiti `CartFlowsName` oldu: aynı kayıt bugün iki dar
  arayüzü besliyor ve sabitin adı akışın adı olmalıydı.

- `workflows/cart`'ın `Carts` dar arayüzü yine büyüdü: `OpenCart` artık sepet
  metadata'sını da taşır. Kendi uygulamasını yazan gömülü kodu etkiler. Aynı
  yüzeye `OpenCartForCountry` eklendi — `Interop` bir süre bilinçli olarak
  sepet açmayı yayımlamıyordu, çünkü tüketicisi yoktu; artık var.

### Değişti

- `POST /store/v1/carts` gövdesindeki `metadata` **kaldı** ve akışa olduğu gibi
  taşınıyor. Karar satır metadata'sında verilenin aynısıdır: alan gerçekten
  istemcinin bilgisidir (kampanya kaynağı, vitrin oturumu), hiçbir hesaba
  girmez ve türetilecek bir karşılığı yoktur. Düşürülseydi, sepeti açan tek yol
  artık akış olduğu için istemcinin gönderdiği alan sessizce kaybolurdu.

### Güvenlik

- **Satış kanalı kapsamı artık YAZMA yolunda da uygulanıyor: başka bir kanalın
  varyantı sepete EKLENEMİYOR.** Kural (`ataması olmayan ürün her kanalda
  görünür, ataması olan yalnızca atandığı kanallarda`) v0.4.0'a kadar yalnızca
  OKUMA yüzeyinde uygulanıyordu — liste, sayaç, tekil uç ve toplu okuma tek bir
  SQL şablonundan geçiyordu. `POST /store/v1/carts/{id}/line-items` ise varyantı
  YALNIZCA kimlikle okuyordu.

  Sonucu, kuralın kendisini anlamsız kılıyordu: B kanalının publishable
  anahtarıyla gelen bir istemci, yalnızca A kanalında satılan bir varyantın
  kimliğini gövdeye yazarak satırı ekliyor ve alışverişi tamamlayabiliyordu.
  Vitrinde gizlenen ürün sepette satılabiliyordu, yani süzgeç bir yetkilendirme
  değil bir görüntüleme tercihiydi. Gerçek yığında ölçüldü: düzeltme öncesi
  yabancı kanalın varyantı `201`, sonrasında `404` alıyor
  (`internal/e2e/kanal_sepeti_test.go`).

  Kural İKİNCİ KEZ YAZILMADI. Akış varyantı yine Query katmanından okur;
  eklenen tek şey, okumaya isteğin DOĞRULANMIŞ kimliğinden gelen kanalların
  süzgeç olarak konmasıdır. Süzgeci uygulayan taraf product modülüdür ve
  vitrinin kullandığı SQL şablonunun ta kendisiyle uygular
  (`repository/saleschannel.go`); yeni sorgu yalnızca şablonu varyantın
  `product_id`'siyle örnekler.

  Kanallar İSTEMCİDEN ALINMAZ, `corehttp.Principal`'dan gelir — okuma
  yüzeyindeki kararın aynısı. Üç durum da okuma yüzeyiyle BİREBİR aynı ayrılır:
  kimlik yok → süzgeç uygulanmaz, kanalsız kimlik → BOŞ KÜME (yalnızca atamasız
  ürünler), kanallı kimlik → o kanallar. İki türetmenin aynı anlamı taşıdığını
  bir arch testi çiviler (`TestKanalTuretmesiIkiYuzeydeAyniAnlamda`).

  Kapsam dışı varyant, hiç var olmayan varyantla **aynı** hatayı döner
  (`404 cart_workflow_variant_unknown`): farklı bir sınıf, başka bir kanalda
  satılan ürünün varlığını ele verir ve gizlemenin kendisini delerdi.

  **Kapsam GİRİŞTE uygulanır.** Satır adedi güncelleme ve sepet tamamlama
  yolları kapsamı yeniden sormaz: sepete varyant sokabilen tek yol satır
  eklemedir ve sepete GİRMİŞ bir satırın, ürünü sonradan başka bir kanala
  taşıyan bir yönetici düzenlemesiyle ödenemez hâle gelmemesi verilmiş bir
  karardır. Sınır `workflows/cart/saleschannel.go`'da ve README'de yazılıdır;
  bir arch testi (`TestVaryantOkumalariKanalKararindanGecer`) her yeni varyant
  okumasını ya kararı vermeye ya da gerekçesini yazmaya zorlar.

  **Kimden ne isteniyor:** kanal ataması hiç kullanmayan kurulumlar
  etkilenmez (atamasız ürün her kanalda satılabilir kalır). Kanal ataması
  KULLANAN kurulumlarda, bugüne kadar açığa dayanarak yabancı kanalın ürününü
  sepete ekleyen bir istemci artık `404` alır; doğru düzeltme, vitrinde
  gösterilen katalogla sepete eklenen ürünü aynı anahtardan geçirmektir.

- **B2B harcama limitinin uygulanma KOŞULU belgelendi: limit, müşterisini
  BEYAN EDEN alışverişe uygulanır.** Davranış **değişmedi**; değişen şey, bu
  deponun v0.4.0'a kadar limiti koşulsuz uygulanan bir kural gibi anlatmasıydı.

  Kural `order.CreateOrder` içinde `CustomerID` üzerinden çalışır ve o kimlik
  zincire vitrin sepetinin gövdesinden girer. Mağaza yüzeyinin tek kimliği
  publishable anahtardır ve o bir satış kanalını temsil eder, bir müşteriyi
  değil (`corehttp.Principal` müşteri kimliği taşımaz) — yani `customer_id`
  hiçbir kanıt istemeyen bir iddiadır. Gerçek ikilide, tek bir publishable
  anahtarla ölçüldü (limit `50_000`, sepet toplamı `76_800`): gövdede
  `customer_id` varken tamamlama `409 order_spending_limit_exceeded`, aynı sepet
  alan olmadan `200` alıyor. Başkasının kimliğiyle tamamlanan alışveriş o
  müşterinin penceresinden düşüyor, yani adı bilinen bir çalışanın harcama hakkı
  **yakılabiliyor**. Beyanı zorunlu kılmak da kapatmıyor:
  `POST /store/v1/customers` publishable anahtarla kuralsız, taze bir misafir
  kaydı açıyor.

  Kimlik doğrulama **inşa edilmedi** ve bu bilinçlidir: doğrulama çerçevenin
  değil gömen uygulamanın işi olarak karara bağlandı
  ([ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md) — reddedilen
  seçenekler ve gömen uygulamaya düşen işin listesi orada). Sınır README'nin
  B2B bölümüne, `order` modülünün godoc'una, `service.SpendingPolicy` ile
  `CreateOrderInput.CustomerID` alanlarına yazıldı ve `order`'da iki testle
  sabitlendi (`TestMisafirSiparisindeHarcamaKuraliHicSorulmaz`,
  `TestHarcamaKuraliBeyanEdilenMusteriyeUygulanir`). İki test bir yeteneği
  değil bir kararı korur: kimlik doğrulama geldiğinde düşmeleri **beklenir**.

  B2B kurulumu olan gömen uygulamaların yapması gereken: vitrin yüzeyini bir
  müşteri oturumuyla korumak ve `customer_id`'yi gövdeden değil oturumdan
  okumak. O katman olmadan limit, yalnızca dürüst istemcinin hatasını yakalar.

## [0.4.0] — 2026-09-01

### Kırıcı değişiklikler

`0.x` boyunca minor sürümlerde kırıcı değişiklik olabilir. Aşağıdakiler
**mağaza API'sini** kullanan istemcileri doğrudan etkiler.

- **`POST /store/v1/carts/{id}/line-items` gövdesinden `unit_price` ve `title`
  KALDIRILDI.** İkisini gönderen istek artık `422` alır (gövde tanınmayan alanı
  reddeder). Sessizce yok saymak seçilmedi: istemci gönderdiğini sanır, sunucu
  başka bir fiyat yazardı. Fiyat `pricing`'den, başlık katalogdan gelir; gerekçe
  aşağıda ("Fiyat yetkisi istemciden alındı").
- **`POST /store/v1/carts` gövdesinden `currency_code` KALDIRILDI.** Alanı
  gönderen istek artık `422` alır (gövde tanınmayan alanı reddeder). Sepetin
  para birimini sunucu, sepetin BÖLGESİNDEN türetir. Sessizce yok saymak yine
  seçilmedi: istemci gönderdiğini sanır, sunucu başka bir para birimi yazar ve
  satır beklenenden başka bir fiyat listesinden fiyatlanırdı. Gerekçe aşağıda
  ("Para birimi yetkisi istemciden alındı").

- **`POST /store/v1/carts` bilinmeyen bir `region_id` ile artık `404` döner.**
  Eskiden bölgenin varlığı hiç denetlenmiyordu ve uydurma bir kimlikle sepet
  açılabiliyordu; para birimi bölgeden okunduğu için o kapı da kapandı. Boş ya
  da biçimsiz `region_id` yine `422`'dir — ama hata artık `region` modülünün
  kodunu taşır (`region_invalid_input`), `cart`'ınkini değil.

- **`PATCH /store/v1/carts/{id}/line-items/{line_item_id}` sıfır adette artık
  `422` değil `204` döner** ve satırı kaldırır. Sıfır adet eskiden geçersizdi,
  yani hiçbir istemci ona bağımlı olamaz; her sepet arayüzünde adet seçiciyi
  sıfıra indirmek "bunu kaldır" demektir ve niyeti akış çevirir.
- `workflows/cart`'ın `Carts` dar arayüzü büyüdü: `AddCartLineItem` artık satır
  metadata'sını da taşır. Kendi uygulamasını yazan gömülü kodu etkiler.

### Eklendi

- **Sepet akışları üretim ikilisine BAĞLANDI; `POST
  /store/v1/carts/{id}/complete` eklendi.** `internal/workflows/cart` ve
  `internal/workflows/checkout` hiçbir kurulumda çağrılmıyordu: `cmd/server`
  yalnızca saga MOTORUNU (`core.workflow`) kaydediyor, akışların kendisini
  üretim kodunda kuran tek satır bulunmuyordu. Tek çağıran `internal/e2e`
  testleriydi — yani çalışan ikilide sepeti siparişe çeviren yol YOKTU: ödeme,
  kargo, checkout promosyonu, `order.placed` bildirimi ve b2b harcama limiti
  erişilemezdi. README ise `complete_cart`'ı sunulan bir yetenek gibi
  anlatıyordu.

  Kablolama, `order` → `b2b` harcama kuralındaki kalıbın aynısıdır: akışların
  HTTP sahibi MODÜLDÜR. `cart` kendi paketinde iki dar arayüz tanımlar
  (`api.LinePricing`, `api.CartCompletion`), somut akışı container'dan
  `workflows.cart.interop` / `workflows.checkout.interop` adıyla çözer ve
  `cmd/server` yalnızca akışları kurup kaydeder — bileşim köküne handler kodu
  girmez, URL'ler sepetin altında kalır.

  Kayıt sırası **dairesel**dir ve daire iki yerden kırılır: akış tüm modüllerin
  yüzeylerini çözdüğü için ancak `Bootstrap`'tan sonra kurulabilir, modülün
  handler'ı ise `Register` sırasında kurulur. Modül tarafındaki çözüm bu yüzden
  TEMBELDİR (ilk istekte, sonucu saklanarak) — `order`'ın `spendingPolicy`
  sarmalayıcısıyla birebir aynı mekanizma; yenisi icat edilmedi.

  Bağlanan uçlar: satır ekleme ve adet güncelleme artık `add_line_item` /
  `update_line_item` akışlarından geçer (yani satır her değişiklikte YENİDEN
  FİYATLANIR ve sepetin toplamı bayat kalmaz), `POST .../complete` ise
  `complete_cart` saga'sını çalıştırır.

  Tamamlama gövdesindeki her alan bir yetki sorusu olarak ayrı ayrı
  kararlaştırıldı: `payment_provider_id` ve `payment_data` istemciden gelir
  (müşterinin seçimi), `expected_total` **zorunludur** (opsiyonel olsaydı alanı
  unutan her istemci "gördüğün tutarla çekilen tutar aynı mı" korumasını
  sessizce kapatırdı; ayrışma `409` üretir ve hesap saga'nın ilk adımından önce
  yenilendiği için HİÇBİR yan etki uygulanmaz), `email` gövdede YOKTUR (sepetin
  adresi zaten sepettedir; ikinci bir kanal, siparişi sepette görünenden başka
  bir adrese bağlardı) ve `location_id` de YOKTUR (hangi depodan çıkılacağı
  kargo kararıdır; müşteriye depo seçtirmek stok topolojisini sızdırırdı).
  Yanıt siparişin kimliğini ve tahsil edilen tutarı taşır; ödeme oturumu/
  koleksiyon/rezervasyon kimlikleri ve operatöre ait uyarılar yayımlanmaz.

- **Fiyat yetkisi istemciden alındı.** `POST
  /store/v1/carts/{id}/line-items` gövdesi `unit_price` alıyor ve cart servisi
  onu OLDUĞU GİBİ yazıyordu; yalnızca aralığı denetleniyor (`checkAmount`),
  doğruluğu denetlenmiyordu. Alanın godoc'u "nihai fiyatı `calculate_totals`
  workflow'u yazar" diyordu — ama o workflow hiç kurulmuyordu, yani istemcinin
  gönderdiği fiyat NİHAİ fiyattı. Vitrinin kimliği publishable anahtardır ve
  tarayıcıda durur: bu, herkesin erişebildiği bir "kendi fiyatını yaz" ucuydu.
  Sonuca gitmiyordu çünkü checkout ucu da yoktu — ama ikisi aynı kökten ve
  checkout bağlandığı anda "her şeyi 1 kuruşa al" açılırdı. `title` de aynı
  sınıftaydı: satırın adı kataloğun verisidir ve sepette, siparişte, faturada
  ve kargo listesinde görünen odur.

  Fiyat artık `pricing` modülünden, başlık Query katmanından gelir. Yönetim
  tarafında karşılığı açılmadı ve gerekmiyor: `cart`'ın `/admin/v1` yüzeyi
  tanımı gereği YALNIZCA OKUMADIR (sepeti değiştiren tek taraf müşteridir),
  yani "yönetici fiyat girebilsin" için değiştirilecek bir uç yoktur.

  **Fiyat yolu KAPALI arızalanır**: fiyatlandırma akışı çözülemezse satır HİÇ
  EKLENMEZ — ne istemcinin fiyatıyla, ne sıfırla. Bu, b2b harcama kuralının
  bilinçli TERSİDİR: b2b kayıtlı değilse "limit yok" doğru cevaptır, ama
  fiyatlandırıcı yoksa "fiyat yok" satırı yazmak sessizce bedava mal satmaktır.
  Gerekçe `linePricing` godoc'una yazıldı.

  Yeni testler: vitrinin fiyat/başlık kabul etmediği ve reddedilen isteğin
  sepete satır YAZMADIĞI (birim + e2e), fiyatlandırıcı yokken satırın
  eklenmediği, tamamlama ucunun gerçekten sipariş ürettiği ve onaylanmayan
  toplamda hiçbir yan etki bırakmadığı (e2e, gerçek modüller ve gerçek
  Postgres üzerinde, tümüyle HTTP'den).

- **Para birimi yetkisi istemciden alındı.** `POST /store/v1/carts` gövdesi
  `currency_code` alıyor ve cart servisi onu OLDUĞU GİBİ yazıyordu; yalnızca
  kodun BİÇİMİ doğrulanıyor, bölgeninkiyle karşılaştırılmıyordu. Yani ayrışma
  reddedilmiyordu: TRY bölgesinde açılan bir sepete `EUR` yazan istemci, o
  sepeti gerçekten EUR olarak alıyordu.

  Sınıf `unit_price` ile AYNIDIR ve patlama yarıçapı yalnızca daha küçüktür:
  istemci tutar uyduramıyordu ama HANGİ FİYAT LİSTESİNİN uygulanacağını
  seçebiliyordu. Para birimi sepetin bir etiketi değil, fiyatın SEÇİCİSİDİR —
  satır akışı birim fiyatı varyantın fiyat kümesinden "sepetin para biriminde"
  okur. Küçük yarıçap kusuru küçültür, meşrulaştırmaz.

  Para birimi bölgenin verisidir: `region` şemasında bölge başına TEK bir
  sütundur (`region.currency_code`, `currency` tablosuna FK). Bir bölgenin iki
  para birimi olamayacağı için sepetin para birimi bir seçim değil bir
  TÜRETMEDİR ve artık handler onu bölgeden okur. Kalıp fiyattakinin aynısıdır:
  `cart`, `region`'ı import etmez; kendi paketinde dar bir arayüz tanımlar
  (`api.RegionCurrencyReader`) ve somut servisi container'dan `region.service`
  adıyla çözer (ADR 0001/0006).

  **Bu yol da KAPALI arızalanır**: bölge yüzeyi çözülemezse sepet HİÇ AÇILMAZ.
  Bir varsayılana düşmek — mağazanın ilk para birimi ya da istemcinin dediği —
  tam olarak kapatılan kapıyı geri açardı. Gerekçe sepet açma ucunun godoc'una
  yazıldı (`internal/modules/cart/api/store.go`).

  **Yönetim yüzeyinde aynı alan MEŞRUDUR ve kaldırılmadı.** `POST
  /admin/v1/regions` gövdesindeki `currency_code` bölgeyi TANIMLAR: operatör
  orada bir kopya değil ASLI yazar ve kopyalanacak bir kaynak yoktur. Ölçüt
  "alan gövdede mi" değil, "bu değer çağıranın kendi verisi mi" sorusudur.
  `cart`'ın kendi `/admin/v1` yüzeyinde soru hiç doğmaz: orası yalnızca okur ve
  sepet açan bir yönetim ucu yoktur.

  Yeni testler: alanın reddedildiği ve reddedilen isteğin sepet YAZMADIĞI
  (birim + e2e), para biriminin gerçekten bölgeden geldiği, bölge yüzeyi
  yokken sepetin açılmadığı, bilinmeyen bölgenin sepet açtırmadığı ve
  container adının sözleşme olduğu (birim). Asıl kanıt e2e'dedir: farklı para
  birimli İKİ bölgede aynı varyant, sepet başına FARKLI birim fiyat alır —
  para biriminin fiyatı seçtiği ancak bu iddiada görünür.


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

İşletmecinin kurulumunu **sessizce** bozan ayarlar kapatıldı. Ortak yanları,
hiçbirinin hata üretmemesi ve hepsinin ancak üretimde — sınır aşıldığında, ilk
giriş denemesinde ya da görseller kaybolduğunda — görünmesiydi:

- **Olay veri yolu, aynı Redis'i paylaşan iki kurulum arasında AYRILMIYORDU.**
  `cmd/server` veri yolunu sıfır değerli bir `eventbus.RedisConfig` ile
  kuruyordu: stream öneki de consumer group da paketin varsayılanına düşüyor,
  yani `REDIS_KEY_PREFIX` olay tarafına HİÇ ulaşmıyordu. Koruma anahtarları
  ayrılıyor, olaylar ayrılmıyordu.

  Grubun paylaşılması ikisinin de kötüsüdür: consumer group'un tanımı gereği
  bir mesajı gruptaki tüketicilerden yalnızca **biri** alır, yani üretimin
  `order.placed` olayı staging tarafından tüketilip yutulabilirdi — sipariş
  konur, onay bildirimi hiçbir yere gitmez ve hiçbir yerde hata görünmez.

  Ad alanı artık önekten türetiliyor (`<önek>:events:<olay>` ve grup `<önek>`)
  ve türetme tek bir yerde, `eventbus.RedisConfig.WithNamespace` içinde
  yaşıyor; paketin varsayılanları da o türetmeden okunuyor
  (`DefaultStreamPrefix = DefaultGroup + ":events"`), yani varsayılan kurulum
  ile ayrılmış kurulum yarım ayrışamıyor. Varsayılan önekle sonuç **bugünküyle
  birebir aynıdır**: yükseltilen bir kurulumun stream'i ve grubu yerinde kalır.

  Tüketici adı ters yönde çalışır — kurulumları değil, aynı gruptaki
  **süreçleri** ayırır — ve bu yüzden ad alanına bağlanmadı. Bunun yerine
  `EVENT_BUS_CONSUMER` eklendi: `RedisConfig.Consumer`'ın godoc'u kalıcı bir
  kimliğin (StatefulSet pod adı) "açıkça verilmesi" gerektiğini söylüyordu ama
  onu verecek bir ortam değişkeni YOKTU. Aynı adı iki örneğe vermek sessizce
  çift işlemeye yol açar (ikisi de açılışta o adın bekleyen listesini okur) ve
  tek süreç bunu göremez; bu yüzden çözülen ad açılışta **loglanır**.

- **`TRUSTED_PROXY_HOPS=0`, ters proxy arkasında hız sınırını mağaza geneline
  düşürüyordu ve bunu sessizce yapıyordu.** Değer sıfırken `X-Forwarded-For`
  hiç okunmaz ve anahtar `RemoteAddr`'a düşer; ters proxy / ingress / CDN
  arkasında o adres HER İSTEKTE proxy'nindir, yani `RATE_LIMIT_PER_MINUTE`
  "müşteri başına 600" değil "tüm mağaza için dakikada 600" olur ve tek bir
  müşteri vitrini kilitleyebilir.

  **Varsayılan değişmedi ve açılış durmuyor**, çünkü iki yanlışın bedeli aynı
  sınıfta değil: fazla verilen bir değer istemcinin uydurduğu adresi gerçek
  saydırır ve saldırgan her istekte taze bir kova alarak sınırı TAMAMEN atlar —
  bir güvenlik açığı; eksik değer ise korumayı yalnızca gevşetir. Sıfır, doğrudan
  internete bakan bir kurulumda DOĞRU cevaptır ve yapılandırma hangisinin
  geçerli olduğunu bilemez. Eklenen şey deponun kendi kalıbı: hız sınırı açıkken
  sıfır atlamayla çıkan paylaşılan bir kurulum artık açılışta **uyarı** üretir
  (`GUARD_BACKEND=memory` ve `FILE_ROOT` uyarılarıyla aynı kapı). "Riskli"
  tanımı config'te (`Config.RateLimitKeyIsPerClient`), uyarıyı yazan taraf
  `cmd/server`'dadır.

- **`EVENT_BUS=inmemory` paylaşılan ortamda yalnızca INFO logluyordu**, oysa
  eşdeğeri `GUARD_BACKEND=memory` WARN üretiyordu — aynı ödün birinde görünüp
  ötekinde görünmüyordu. Bellek içi veri yolu ÇALIŞIR ama kalıcı değildir:
  teslim asenkrondur ve süreç çökerse ya da kapanış `SHUTDOWN_TIMEOUT` içinde
  bitmezse teslim edilmemiş olaylar iz bırakmadan kaybolur. Artık paylaşılan
  ortamda WARN, yerel geliştirmede INFO.

- **`RATE_LIMIT_PER_MINUTE <= 0` iken sınırlayıcının hiç kurulmadığı tek
  satırla bile bildirilmiyordu.** Kapatmak meşru bir seçimdir (ADR 0007'de
  sıfır "kapat" demektir) ama giriş ucunu da kotasız bırakır ve kimsenin
  bilmediği bir "kapalı", kazayla yazılmış bir sıfırdan ayırt edilemez. Artık
  paylaşılan ortamda WARN, yerel geliştirmede INFO.

- **Taze veritabanı + boş `ADMIN_BOOTSTRAP_*` ikilisi sessizce açılıyordu.**
  İkisini birden boş bırakmak `config.Validate`'ten geçiyordu ve haklı olarak:
  KURULMUŞ bir sistem için meşru bir seçimdir ve "kurulmuş mu" sorusunu
  doğrulama göremez. Ama veritabanı da boşsa sonuç yönetilemez bir kurulumdur —
  hiç kullanıcı yoktur, yönetim yüzeyi giriş ucu dışında tamamen korumalıdır ve
  ilk kullanıcıyı HTTP'den yaratmanın yolu yoktur; mağaza yüzeyi de kapalıdır,
  çünkü publishable anahtarı da yönetim ucu üretir. Sunucu yine de açılıyor,
  `/health` ve `/ready` yeşil dönüyor ve arıza ilk giriş denemesine kadar
  görünmüyordu.

  Tohum adımı artık kullanıcı sayısını HER HÂLDE okuyor ve sıfır kullanıcı +
  tohumsuz yapılandırma paylaşılan ortamlarda **açılışı durduruyor**
  (`admin_bootstrap_required`), yerel geliştirmede uyarı üretiyor. Burada
  belirsizlik yok — `FILE_ROOT` uyarıyla yetiniyor çünkü yapılandırmanın yanlış
  olduğu kesin değil, sıfır kullanıcılı bir kurulumun yönetilemez olduğu ise
  kesin. Ayrım `JWT_SECRET`'inkiyle aynıdır ve ".env olmadan `make up &&
  make run` çalışır" sözü korunur.

- **`Config.LocalFileRootIsDurable` (o gün adı LocalFileRootIsPortable'dı)
  yalnızca `filepath.IsAbs`'e bakıyordu.**
  `FILE_ROOT=/tmp/gobit-uploads` mutlaktır, "göreli yol vermeyin" öğüdünü geçer
  ve uyarı susardı — oysa `/tmp` (ve `/var/tmp`, `/dev/shm`, `TMPDIR`) işletim
  sistemi tarafından temizlenir, üstelik çoğu dağıtımda tmpfs oldukları için
  yeniden başlatmayı bile beklemez. Yani `Config.FileRoot` godoc'unun varsayılan
  için REDDETTİĞİ sessiz veri kaybı, tek satırlık bir ayarla geri geliyordu.
  Ölçüt artık "çalışma dizininden bağımsız mı" değil "süreç yeniden başladığında
  yerinde kalır mı"; ad da davranışa çekildi: `LocalFileRootIsDurable`.

- **`validateFile` godoc'u mutlak yol şartını UYGULUYORMUŞ gibi yazıyordu**,
  oysa doğrulama durdurmuyor, `cmd/server` yalnızca WARN logluyor. Belge
  davranışa çekildi: kalıcılık bir doğrulama değil uyarıdır ve gerekçesi
  `LocalFileRootIsDurable` godoc'undadır.

- **`cmd/server`'daki b2b kaydının yorumu kendisiyle çelişiyordu**: "bu satır
  silindiğinde ... saf B2C kurulum, KODU DEĞİŞTİRMEDEN elde edilir" diyordu,
  oysa satırı silmek kod değişikliğidir. Cümle düzeltildi ve b2b'yi kapatan bir
  ortam değişkeninin neden **eklenmediği** yazıldı: yanlışlıkla `false` verilen
  bir anahtar harcama limitini hiçbir hata üretmeden kaldırırdı — yani bu
  bölümün kapattığı sessiz arıza sınıfının yenisi olurdu. Kod yolu ise yarım
  kalamıyor; `TestHerModulBilesimKokundeKayitli` satırı silen kişiden kararı
  gerekçesiyle yazmasını istiyor. Modülü B2C kurulumda bırakmanın bedeli de
  küçük ve görünür: iki boş tablo ve hiç tetiklenmeyen bir kural.

Sepet akışlarının bağlanmasını izleyen bağımsız doğrulamanın çıkardığı bulgular:

- **Saga adım hatası, alt hatanın KODUNU kaybediyordu.** `core/workflow`
  patlayan adımı sararken hatanın SINIFINI (`Kind`) alt hatadan devralıyor ama
  KODUNU kendi sabitiyle (`workflow_step_failed`) eziyordu. Taşıma katmanı
  gövdeye tek bir makine okunur alan yazar (`error.code`), yani her saga hatası
  istemci için TEK bir değere düzleşiyordu. Somut bedeli B2B harcama limitiydi:
  limiti aşan alışveriş `409` alıyor, gövdede `spending_limit` HİÇ geçmiyordu ve
  vitrin "limitiniz yetmedi" ile "geçici çakışma, tekrar deneyin"i ayırt
  edemiyordu — oysa `409` tam olarak tekrarın çözmediği sınıftır. Kod artık
  korunur (`stepFailureCode`); kodsuz bir adım hatası motorun kendi sabitini
  alır. YALNIZCA kod taşınır: mesaj ve `Details` zincirde kalır ve
  `KindInternal` hatalarında yine maskelenir. Değişikliğin SINIRI da testle
  çizildi — telafi patladığında dıştaki kod `workflow_compensation_failed`
  olarak KALIR, çünkü orada okunması gereken şey adımın neden düştüğü değil,
  sistemin tutarsız kaldığıdır.

- **KAPALI arıza yanlış status sınıfı döndürüyordu (`404`).** Satır
  fiyatlandırma / sepet tamamlama akışı çözülemediğinde `cart` modülü
  container'ın hata sınıfını olduğu gibi geçiriyordu: kayıtsız ad
  `KindNotFound` → `404`, yanlış tipte kayıt `KindInvalid` → `422`. Para
  açısından davranış doğruydu (satır YAZILMIYOR), sınıf yanlıştı: `404`
  istemciye "böyle bir uç yok" der, `5xx` uyarı zinciri hiç çalmaz ve ara
  katmanlar yanıtı önbelleğe alıp arızayı kurulum düzeldikten sonra da
  sürdürebilir. Sarmalama artık `KindInternal`'dır; operatöre ne söylediği
  korunur, istemciye giden metin çekirdeğin maskeleme kuralından geçer ve
  geriye yalnızca `cart_module_setup_failed` kodu kalır. Aynı sınıf hatası
  `order` modülünün harcama kuralı sarmalayıcısında da düzeltildi.

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

### Bilinen sınırlar

Bu turda ARAŞTIRILDI, karar verildi ve BİLEREK açık bırakıldı. Kayda geçmemiş
bir açık, kimsenin kapatmadığı açıktır.

- **`POST /store/v1/carts` hâlâ `region_id` alıyor.** `currency_code` bu
  gövdeden KALDIRILDI (yukarı bakın); bölge kimliği kaldı ve aynı sınıftadır —
  bölge vergi ORANINI seçer. Patlama yarıçapı iki adım küçüldü: bölgenin
  gerçekten var olduğu artık doğrulanıyor (para birimi ondan okunuyor) ve
  seçimin fiyat listesi üzerindeki etkisi kalktı. Doğru kapatma yeri yine
  handler değil: türetmeyi zaten yapan bir akış var — `create_cart` ülke
  kodundan hem bölgeyi hem para birimini çözüyor. Gövdenin `country_code`'a
  inmesi ve ucun o akışa devredilmesi gerekir; akışın modüller arası yüzeyine
  bugün bilinçli olarak bulunmayan bir metot eklemeyi ve mağaza sözleşmesini
  bir kez daha kırmayı gerektirdiği için bu tura alınmadı. Gerekçe
  `api.createCartRequest` godoc'unda.

- **Vitrin sepetlerinde SAHİPLİK denetimi yok — model bu, ve artık YAZILI.**
  `/store/v1/carts/{id}` altındaki uçlar isteği yapanın sepetin sahibi
  olduğunu doğrulamaz. Bu bir "yetenek URL" modelidir: sepet kimliği 48 bit
  zaman damgası + 80 bit kriptografik rastgelelikten üretilir, tahmin edilemez
  ve onu bilmek erişim hakkını taşır. Zorunluluktan da doğar — mağaza
  yüzeyinin tek kimliği publishable anahtardır ve o bir SIR değildir; ortada
  müşteri oturumu yoktur. Aynı beyan `order` modülünde zaten yazılıydı; `cart`
  için hiçbir yerde yazmıyordu ve şimdi paket belgesinde duruyor, modelin
  kuralıyla birlikte (vitrin tarafında LİSTE ucu YOKTUR; bir liste ucu tek bir
  kimliği bilmeyi tüm sepetleri okumaya çevirirdi).

  Modelin KAPSAMADIĞI şey ayrıca adlandırıldı: yetenek URL'i "elimdeki kimliğe
  erişebilirim" der, "ben şu müşteriyim" DEMEZ. Oysa gövdelerdeki
  `customer_id` kanıtsız bir sahiplik iddiasıdır ve sepetin müşterisi b2b
  harcama limitinin hangi şirket penceresinden düşüleceğini belirler — yani
  iddia başkasının penceresini tüketebilir. Servis yalnızca tek bir sınırı
  korur (müşterisi olan sepet başkasına devredilemez). Tek doğru kapatma
  müşteri oturumudur (Faz 8) ve bu turda uydurma bir yetki mekanizması İNŞA
  EDİLMEDİ.

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

[Yayımlanmamış]: https://github.com/bdrtr/gobit/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/bdrtr/gobit/releases/tag/v0.4.0
[0.3.0]: https://github.com/bdrtr/gobit/releases/tag/v0.3.0
[0.2.0]: https://github.com/bdrtr/gobit/releases/tag/v0.2.0
[0.1.0]: https://github.com/bdrtr/gobit/releases/tag/v0.1.0
