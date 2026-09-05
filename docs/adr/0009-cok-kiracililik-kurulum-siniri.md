# ADR 0009 — Çok kiracılılık: sınır kurulumdur, satır değil

- **Durum:** Kabul edildi
- **Tarih:** 2026-09-01
- **Faz:** 10 sonrası (v0.4.0 sertleştirme turu)

## Bağlam

Plan belgesi çok kiracılılığı iki yerde kapsam dışı bırakıyor
(`go-commerce-framework-plan.md:35` ve `:396`) ama **neden** bıraktığını
söylemiyor. Kavram depoda hiç geçmiyor: 72 tablonun hiçbirinde kiracı sütunu,
hiçbir imzada kiracı parametresi, hiçbir ad alanında kiracı segmenti yok.

> **Bu ADR'deki sayımlar KARAR TARİHİNE aittir** (2026-09-01) ve o gün ölçülmüş
> hâlleriyle bırakılır; kararın dayandığı büyüklüğü gösterirler, bugünkü şemayı
> değil. Sayı büyüdükçe kararın gerekçesi ZAYIFLAMAZ, güçlenir. Şemanın bugünkü
> tablo sayısı `README.md`'nin "Tek örnek mi, birden çok mu?" başlığındadır ve
> orası bugünü anlatır. İki sayı ayrıştığında geçerli olan, sorulan soruya göre
> değişir: "karar neye dayandı" için buradaki, "bugün ne var" için README'deki.

Gerekçesiz bir kapsam dışı bırakma bir karar değildir; her turda yeniden
tartışılır ve bu arada kapılar sessizce kapanır. Bu ADR o boşluğu kapatıyor.

Karara zemin olsun diye üç bağımsız çalışma yapıldı: kiracı başına veritabanı
(A), paylaşılan şemada satır düzeyi ayrım (B) ve mevcut mekanizmaların sızıntı
avı. Avın sayımı işin büyüklüğünü veriyor: 72 tabloda **0** tanesi "bu satır
kime ait" sorusunu yanıtlayabiliyor; 55 sqlc dosyasında **403** adlandırılmış
sorgu; container'a kayıtlı **13** `query.Provider`; **19** üretim çağrı yerinde
`db.Pool.Pool()`; koruma yığını **2** yol önekini sarıyor.

Ama kararı belirleyen şey bu hacim değil, üç yapısal olgu oldu.

**Birincisi: kapsam kuralının provası bugün YARIM duruyor.** Depodaki tek kapsam
mekanizması satış kanalı süzgecidir ve README onu bir *yetkilendirme* olarak
anlatıyor ("Kanal sorgu dizesinden alınmaz, kimlikten gelir — alınsaydı süzgeç
bir yetkilendirme olmaktan çıkardı"). Süzgeç okuma yüzeyinde titizce uygulanmış:
liste, sayaç, tekil ve toplu görünürlük hepsi tek bir SQL şablonundan geçiyor
(`internal/modules/product/repository/saleschannel.go`). Yazma yolunda ise yok:

Vitrin sepet ucu `variant_id`'yi istemci gövdesinden alıyordu ve akış onu Query
ile **küresel** çözüyordu — yalnızca `id` filtresiyle. Yani B kanalının
publishable anahtarıyla gelen istemci, katalogda göremediği bir A-kanalı
varyantını sepete ekleyip satın alabiliyordu. Bu, deponun daha önce bir kez
ödediği arızanın (kanal bağı yazılıyor ama okunmuyordu) **yazma tarafıydı**.
Yarım uygulanmış bir kapsam kuralının üzerine ondan daha büyük ikinci bir kapsam
kuralı koymak, tam olarak bu deponun en pahalı hata sınıfını ölçekleyerek
tekrarlamak olurdu.

> **Bu gerekçe ARTIK GEÇERLİ DEĞİL ve kaydı öyle bırakılmıyor.** Açık, bu ADR'nin
> kendisi tarafından tetiklendi ve aynı gün kapatıldı: akış varyantı artık
> isteğin doğrulanmış kimliğinden gelen kanallarla kapsayarak okuyor, görünürlük
> yüklemi tek yerde (`salesChannelVisibleTemplate`) kalıyor ve kapsam dışı
> varyant, hiç var olmayan varyantla aynı hatayı dönüyor. Gerçek süreçte
> ölçüldü (404 / sepet boş), mutasyonla açığın üretilebilirliği kanıtlandı
> (süzgeç kaldırılınca 201) ve `internal/arch` altına bir proxy değişmez kondu.
>
> Karar bu gerekçe olmadan da **ayakta duruyor**: aşağıdaki iki sebep taşıyıcı
> olanlardır ve ikisi de dokunulmadan kaldı. Gerekçe silinmiyor çünkü bir ADR'nin
> değeri vardığı sonuç kadar, o sonuca hangi olguyla varıldığının kaydıdır;
> okuyanın bugünkü kodda karşılığı olmayan bir kanıt bloğuna güvenmesi ise ayrı
> bir arıza olurdu.

**İkincisi: iki tasarım da VERİYİ yalıtıyor, ikisi de YAPILANDIRMAYI
yalıtmıyor.** Ödeme sağlayıcı defteri kimliği yalnızca `id` ile tutar ve aynı
`id` ikinci kez kaydedilemez (`internal/modules/payment/service/registry.go`);
eklentiler `PLUGINS` ortam değişkeninden seçilip açılışta bir kez kurulur
(`core/plugin`); Stripe gizli anahtarı, SMTP göndereni, `FILE_ROOT`,
`JWT_SECRET` ve tüm kotalar süreç başına tek bir `Config`'ten gelir. Kendi
ödeme hesabı, kendi gönderen adresi ve kendi kotası olamayan bir kiracı, kiracı
değil bir bölmedir. Bu bileşen A'da da B'de de **tasarlanmamıştır** ve izolasyon
seçiminin kendisinden büyüktür.

**Üçüncüsü: yüzey kiracıya göre değişemez, yalnızca veri değişebilir.**
Route'lar tek router'a bir kez bağlanır ve chi aynı deseni ikinci kez mount
etmede panikler (`core/http.Scoped` godoc'u); OpenAPI belgesinin
önbelleği tek yuvalıdır. Bu bir arıza değil, kabul edilmesi gereken bir sınırdır
ve hangi seçenek seçilirse seçilsin geçerlidir.

## Karar

**gobit v1'de çok kiracılılık İNŞA EDİLMEZ. Sınırın ifadesi şudur:**

> Her gobit kurulumu **tek kiracılıdır**. İzolasyon uygulama katmanında değil,
> **dağıtım katmanındadır**: bir kiracı = bir kurulum = bir veritabanı = bir
> süreç. Çerçeve kiracılar arası bir sınır **tanımaz**, dolayısıyla onu
> **uygulamaz** ve uyguladığını **iddia etmez**.

Bu, "sonraki sürüme bırakıldı"nın gerekçelendirilmiş hâlidir, ertelemesi değil.
İki cümleyle: bir kiracı sınırı ancak yarım uygulanmışsa tehlikelidir, ve bu
depo bugün daha küçük bir kapsam kuralını bile yarım uygulamış durumdadır. Önce
o kapatılır; kiracı sınırı ondan sonra tartışılır.

Kararın üç bağlayıcı sonucu vardır:

1. **Belge gerçeği söyler.** Plan belgesinin iki satırı ve README artık kapsam
   dışı bırakmanın **gerekçesini** taşır ve bu ADR'ye bağlanır. "Sonraki
   sürümlere bırakılır" cümlesi tek başına hiçbir yerde kalmaz.
2. **A ile B arasındaki seçim ERTELENMEZ, TETİKLENİR.** Aşağıdaki "Kararın
   yeniden açılması" bölümü, kararı yeniden açacak veriyi ve o veri geldiğinde
   hangisinin seçileceğini belirleyen soruyu adıyla yazar. Karar bugün
   verilemiyor çünkü elimizde o veri yok — beklenen kiracı sayısı ve kiracı
   başına sağlayıcı kimliği gereksinimi bilinmiyor.
3. **Bugün üç şey yapılır.** Hiçbiri kiracılık işi değildir; üçü de kararın
   yönünden bağımsız olarak bugünkü depoyu düzeltir ve hiçbiri geri alınacak bir
   yatırım yaratmaz. Kiracı adına bir kavram, bir alan ya da bir ayar
   **ayrılmaz**: tüketicisi olmayan bir yetenek, bu deponun adını koyduğu ikinci
   hata sınıfıdır.

   - **Kanal kuralının yazma tarafı.** Ya vitrin sepet yolu kanala süzülür, ya
     da açık README'nin "Aynı ölçütün henüz uygulanmadığı yer" bölümüne ölçülmüş
     hâliyle yazılır. Kayda geçmemiş bir açık, kimsenin kapatmadığı açıktır.
   - **`eventbus.Handler` godoc'u davranışıyla uzlaştırılır.** Godoc "verilen
     ctx, Publish'i çağıran isteğin ctx'inden türetilir" diyor; bu yalnızca
     in-memory backend için doğrudur (`eventbus.inMemoryBus.Publish` çağıranın
     `ctx`'inden türetir, `eventbus.redisBus.dispatch` veri yolunun kök
     ctx'inden). Varsayılan `EVENT_BUS=inmemory` olduğu için ctx'te bir şey
     taşıyan her tasarım testlerde yeşil geçip üretimde kırılır. Sözün
     düzeltilmesi bir paragraftır ve kiracılıktan
     bağımsız olarak bugün yanlıştır.
   - **Hız sınırının kimliğe göre anahtarlanması ya bağlanır ya silinir.**
     Üretimde hiçbir tüketicisi olmayan bir `core/http.KeyFunc`
     uygulaması duruyordu (yalnızca kendi testi çağırıyordu) ve godoc'u "kimliği
     doğrulanmış çağrıyı kimliğine göre anahtarlar" diyordu; oysa koruma
     yığınında hız sınırı kimlikten **önce** koşar
     (`core/http.APIGuards`), yani bağlansaydı her zaman IP'ye
     düşerdi. Aynı yerde hem tüketicisiz bir yetenek hem de godoc'u
     davranışından ayrışan bir fonksiyon duruyordu.

     > **Kapatıldı: fonksiyon silindi.** Hız sınırı bugün yalnızca IP anahtarlar
     > (`core/http.ClientIPKey`, üretimde
     > `core/http.TrustedProxyIPKey`); kimliğe göre anahtarlama
     > yeteneği depoda **yoktur**. Kimlik başına kota istendiği gün yeniden
     > yazılır ve o gün sıranın kendisi de (hız sınırı kimlikten önce koşuyor)
     > birlikte çözülmelidir — yoksa yeni yazılan da aynı sebeple IP'ye düşer.

## Sonuçlar

**Olumlu.** Çerçeve, vermediği bir garantiyi vermiş gibi görünmüyor. "Kiracı
süzgeci var" sanılan bir dönem hiç başlamıyor — ki bu dönemin bedeli, satış
kanalı provasında ölçüldüğü gibi, insanların güvendiği ama çalışmayan bir
sınırdır ve kiracı ölçeğinde bedeli başka bir müşterinin verisidir.

**Olumlu.** "Bir kiracı = bir kurulum" bir kaçamak değil, çerçeveler için
meşru ve yaygın bir konumdur. Tek binary, tek DSN, açılışta otomatik migration
ve tek yönetici tohumu — deponun bütün açılış sekansı zaten bu modeli anlatıyor.
Karar, kodun hâlihazırda söylediği şeyi belgeye yazıyor.

**Olumlu.** Seçim ertelenmiş olsa da **kapılar sayılı hâle geldi**: aşağıdaki
"Kapanmakta olan kapılar" listesi, bugün ücretsiz olan ama yarın pahalıya
kapanacak kararları adıyla taşıyor. Liste tahmin değil, ölçüm.

**Olumsuz.** Aynı ürünü birden çok müşteriye tek kurulumdan satmak isteyen
operatör gobit ile bunu yapamaz; N müşteri N kurulum, N veritabanı ve N süreç
demektir. Bu, küçük kiracıların çok olduğu bir ürün için gerçek bir maliyettir
ve pazarlanabilir gücü düşürür. Kabul edildi: yanlış bir izolasyon iddiası,
hiç iddia etmemekten pahalıdır.

**Olumsuz.** Karar ertelendiği için A→B ya da B→A geçiş maliyeti de ertelenmiş
oluyor; gün geldiğinde ödenecek. Karşı önlem, aşağıdaki kapı listesinin bu ADR
ile birlikte yaşamasıdır.

## Kapanmakta olan kapılar

Bunlar bugün bir iş emri **değildir**; kiracılık gündeme geldiğinde ne kadar
pahalıya mal olacağını belirleyen ölçümlerdir ve gözden kaçmasınlar diye
yazılıyorlar.

- **`orders.display_id` `GENERATED ALWAYS AS IDENTITY` ile tek diziden gelir.**
  Paylaşılan şema (B) seçilirse kiracı başına sipariş numaralandırması ancak
  sütunun tümden değiştirilmesiyle olur; sütuna kiracı öneki eklemek yetmez.
  Kiracı başına veritabanı (A) bunu bedavaya çözer.
- **`ScopeAdmin` bir jokerdir** (`HasScope`: `s == scope || s == ScopeAdmin`) ve
  `POST /admin/v1/users` gövdeden keyfi `Scopes` kabul edip verilmezse `"admin"`
  uyguluyor. Kiracılık geldiği gün "platform yetkisi" bir **scope** olamaz —
  her kiracı yöneticisi kendine basardı. `Principal.Kind`'a üçüncü bir değer
  olmalıdır ve bu, dağıtılmış scope dizeleri çoğaldıkça pahalılaşan bir karardır.
- **`workflow_executions_idempotency_key_uniq` `(workflow, idempotency_key)`
  üzerindedir** ve aynı anahtarla ikinci çağrı adımları koşturmadan
  `prev.Output`'u döner. Bugün tek kiracıda bu, belgelenmiş idempotency
  modelidir; kiracı eklendiği gün indeks `(tenant, workflow, key)` olmazsa mutlu
  yolda çapraz kiracı sipariş çıktısı teslim eder.
- **`db.Pool.Pool()` 19 üretim çağrı yerinde açıkta** ve 14 repository yapıcısı
  `*pgxpool.Pool` alıyor. `product` zaten bir arayüz aldığı için desen depoda
  kanıtlı; ama yapıcı imzaları README'nin pazarladığı gömülü kullanımın parçası,
  yani daraltmak dışa açık bir kırılmadır ve tetiksiz yapılmaz.
- **`internal/core/workflow/pgstore/migrations` modül dışında bir çekirdek
  şemadır** ve `TestCrossModuleForeignKeyYok` yalnızca `internal/modules/*`
  altını geziyor. Tablo başına bir değişmez yazılacağı gün gezintinin
  `migrations` dizinlerini **keşfetmesi** gerekir, listelemesi değil — yoksa
  çekirdek şemalar sessizce kapsam dışında kalır.

## Reddedilen seçenekler

**(A) Kiracı başına veritabanı.** Zorlama gücü en yüksek seçenek ve tek
gerekçesi teorik değil ölçülmüş: 14 repository havuzu yalnızca `xxxdb.New(pool)`
ve `pool.Begin/BeginTx` için kullanıyor, yani tek bir `db.Conn` arayüzü 19 çağrı
yerinin tamamında yerine geçer ve **403 sqlc sorgusunun hiçbiri değişmez**.
Kural ikinci bir yere yazılmadığı için birinci hata sınıfı ortadan kalkar; 43
benzersizlik kısıtı, `display_id` dizisi, otomatik promosyon taraması ve
kiracılar arası link satırı **imkânsız** hâle gelir; kiracı silmek bir liste
değil `DROP DATABASE` olur. Reddedilmesinin sebebi zayıflığı değil bedeli:
kimlik bilgileri kontrol düzlemine taşınmak zorundadır (bir anahtarın hangi
kiracıya ait olduğu, o kiracının havuzu açılmadan **önce** bilinmelidir), yani
`auth` normal bir modül olmaktan çıkar ve "kiracıyı sil = veritabanını düşür"
artık doğru olmaz; migration açılış yolundan çıkar ve "binary'yi çalıştır, şema
hazır" özelliği kalıcı olarak kaybolur; kiracı sayısı sürecin bağlantı bütçesine
çivilenir (yüzler, on binler değil). En pahalısı ise tek yönlü kapıdır: hiçbir
tabloda kiracı sütunu olmadığı için sonradan paylaşılan şemaya geçmek 64 tabloya
sütun eklemek ve zorlama katmanlarının tamamını yeniden yazmak demektir.

**(B) Paylaşılan şemada satır düzeyi ayrım, uygulama katmanında zorlanır.**
Açılış sekansı değişmez, kiracı açmak bir satır eklemektir, on binlerce küçük
kiracıya ölçeklenir ve hız sınırı sıralaması ile `ScopeAdmin` jokeri gibi A'nın
sessiz geçtiği iki gerçek sorunu adıyla çözer. Reddedilmesinin sebebi, satın
aldığı garantinin cinsidir: zorlama, `.sql` dosyalarını ve Go kaynağındaki SQL
sabitlerini tarayan **sözdizimsel bir denetleyicinin** doğruluğuna bağlıdır.
Çalışma zamanı kapısı (kiracısız ifade koşmaz) "bir kiracı var" der, denetim
"yüklem var" der; ikisi birlikte bile "yüklemdeki değer doğru kiracıdır"
demez. Ayrıca 403 sorgunun tamamı yeniden yazılır, her modülün her entegrasyon
fikstürü kiracı kurmak zorunda kalır ve mevcut kurulumların göçü zorunludur.
Erteleyerek reddediliyor, kalıcı olarak değil.

**(C) Melez: B'nin sütunu bugün, A'nın yerleştirmesi yarın.** Teknik olarak en
savunulabilir uzun vadeli biçim ve tek yönlü kapı argümanı bunu destekliyor:
**B'den A'ya geçiş yalnızca bir veri taşımasıdır ve sütunları korur; A'dan B'ye
geçiş sütunları yoktan var etmektir.** En güçlü uç noktası da buradadır —
kiracı sütunu + `FORCE ROW LEVEL SECURITY` + her ifadeyi işlem içine alıp
`SET LOCAL` ile kiracıyı kurmak, B'nin bıraktığı "denetleyicimiz bir biçimi
kaçırabilir" boşluğunu tamamen kapatır, çünkü reddeden taraf motorun kendisi
olur. Bugün reddedilmesinin sebebi maliyetin **en yüksek** olması (B'nin tüm
bedeli + 403 okumanın tamamının işleme alınması) ve satın aldığı şeyin hâlâ
yukarıdaki üç "hiçbiri çözmüyor" kaleminden hiçbirini kapatmaması. Karar
yeniden açıldığında **başlangıç adayı budur**, A ya da B değil.

**Kiracıyı `X-Tenant-ID` başlığından ya da gövdeden okumak.** ADR 0008'in harfi
harfine tekrarı ve o karar ölçümüyle birlikte verildi: `customer_id` "bir olgu
değil, hiçbir kanıt istemeyen bir sahiplik iddiasıdır" ve gövdeden geldiği için
başkasının harcama hakkını yakmaya yetti. Kiracı başlığı aynı iddianın çok daha
büyük patlama yarıçaplı hâlidir. Hangi seçenek seçilirse seçilsin kiracı yalnızca
kimlik bilgisinin **satırından** okunur; JWT claim'inden bile değil, çünkü elde
duran bir jeton askıya alınmış ya da taşınmış bir kiracıyı taşıyabilir ve
kullanıcı satırı zaten okunuyor.

**Ortam değişkeniyle açılıp kapanan bir "çok kiracılı mod".** ADR 0007'nin
gerekçesi: yanlışlıkla `false` verilen bir anahtar korumayı hiçbir hata
üretmeden kaldırır. Burada ayrıca yanlış tarafa da düşerdi — "kapalı" bir
kurulum, kiracı sütunu dolu bir veritabanını süzgeçsiz okurdu.

**Bugünden `Principal`'a bir `TenantID` alanı ya da bir `tenant` paketi
eklemek.** Reddedildi ve bu ADR'nin en somut "yapmayın" maddesidir. Alanı
okuyacak hiçbir yer olmadan eklemek, deponun ikinci hata sınıfını (tüketicisi
olmayan yetenek) bilerek üretmek olurdu; üstelik alan bir kez orada durduğunda
"kiracı desteği var" cümlesi kendiliğinden kurulur. Kavramın adını şimdiden
ayırmanın hiçbir teknik faydası yok: depoda `tenant` ile çakışan bir ad yok,
yani ayrılacak bir şey de yok.

## Kararın yeniden açılması

Bu karar bir kapanış değil, tetiklenmiş bir bekleyiştir. Yeniden açan üç soru
var ve ikisinin cevabı bugün elimizde **yok**; karar tam olarak bu yüzden
verilemiyor:

1. **Süreç başına beklenen kiracı sayısı nedir?** Yüzler ise A; on binler ise
   B ya da C. Bu bir ayar değil, çataldır: A'nın kiracı başına havuzu, canlı
   kiracı sayısını Postgres'in bağlantı tavanına bağlar.
2. **Kiracının kendi sağlayıcı kimliği olacak mı?** (kendi Stripe hesabı, kendi
   gönderen adresi, kendi kotası.) Cevap "evet" ise, izolasyon seçiminden
   **önce** kiracı başına yapılandırma bileşeni tasarlanmalıdır; o bileşen A'da
   da B'de de yoktur ve ikisinden de büyüktür. Cevap "hayır" ise istenen şey
   muhtemelen çok kiracılılık değil, çok mağazalılıktır — ve onun cevabı bugün
   depoda zaten var olan satış kanalıdır, önce yazma tarafı kapatılarak.
3. **Kiracı başına geri yükleme ya da veri yerleşimi (residency) isteniyor
   mu?** İsteniyorsa A'nın kozu belirleyicidir: `pg_dump` kiracı başınadır ve
   bir kiracıyı başka sunucuya taşımak kontrol düzleminde tek satırdır.

Karar yeniden açıldığında gözden geçirilecekler: yukarıdaki "Kapanmakta olan
kapılar" listesinin her maddesi (o güne kadar hangileri kapandı, bedeli ne
oldu), README'nin "Aynı ölçütün henüz uygulanmadığı yer" bölümü (kanal kuralının
yazma tarafı kapandı mı) ve ADR 0004 ile 0005 — `query.Provider.FetchByIDs`
imzasında süzgeç parametresi yoktur ve link tabloları çalışma anında kurulur;
ikisi de kiracı sınırının **geçirilemediği** yerlerdir ve o gün ya değişmeli ya
da kiracı sınırı onların altından geçmelidir.
