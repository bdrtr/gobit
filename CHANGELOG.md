# Değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/) ölçütlerine,
sürümleme [Semantic Versioning](https://semver.org/lang/tr/) kurallarına
uyar.

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir ve bir uç, daha doğru bir tasarım uğruna taşınabilir.
Sabitlenme `1.0.0` ile olur.

## [Yayımlanmamış]

### Eklendi

- **İKİNCİ bir hata raporlayıcı yazıldı ve ADR 0014'ün sınavı böylece koşuldu**
  (`error-otlp`). ADR "sözleşmenin doğru ŞEKİLDE mi olduğunu yoksa yalnızca
  Sentry'nin istediği şekil mi olduğunu ancak ikinci bir uygulama gösterir"
  diyordu; ikinci uygulama, modeli Sentry'den en uzak olanı seçti.
  OpenTelemetry log modelinde "issue" yok, gruplama anahtarı yok, tekilleştirme
  yok: bir kayıt zaman, önem derecesi, gövde ve özniteliklerdir.

  **`ErrorEvent` sınavdan geçti: hiçbir alan eklenmedi.** Sentry'de bir ALAN
  olan parmak izi burada bir GELENEK oldu — hata kodu `exception.type`
  özniteliğine gidiyor, ki bir toplayıcının hata görünümü onu gruplar. Yani
  kod, tipe göre gruplayan bir toplayıcı için YETERLİ ve yığın izine gerek yok;
  ADR'nin açık bıraktığı soru buydu.

  Ortaya çıkan iki bulgu ADR'ye yazıldı ve ikisi de yeni birer tetik:
  (1) iki raporlayıcının PAYLAŞTIĞI şey gövde değil YAŞAM DÖNGÜSÜ — sınırlı
  kuyruk, tek gönderici, tekrar yok, kapanışta boşaltma; aynı doksan satır iki
  kez elle yazıldı, üçüncüde çekirdeğe taşınmalı. (2) OTLP bir önem derecesi
  SAYISI istiyor ve eklenti onu sabit dolduruyor; bu bugün dürüst, çünkü
  raporlamanın tabanı ERROR. Taban oynarsa sabit yalan olur ve alan olayın
  kendisine girmeli.

  Eklenti yeni bağımlılık getirmiyor: OTLP/HTTP JSON gövdesi elle yazılıyor,
  Sentry zarfı için verilen kararın aynısı.

- **`gobit recover <execution-id> -confirm <execution-id>`: yarım kalmış bir
  saga artık ELLE telafi edilebiliyor.** v0.8.0 kesintiye uğrayan ödemeyi
  GÖRÜNÜR (`gobit stuck`) ve GERİ ALINABİLİR (kayıtlardan telafi) yapmıştı, ama
  geri almayı yalnızca aynı anahtarla dönen bir çağıran tetikleyebiliyordu. Bu,
  yeniden deneyen müşteriyi kapsar ve başkasını değil: terk edilmiş sepetin
  dönen çağıranı yoktur, kayıt sonsuza dek `running` kalır ve ayırdığı stoğu
  BIRAKACAK KİMSE olmaz. Operatörün elinde üzerinde işlem yapamadığı bir liste
  vardı.

  Motor tarafında yeni yetenek `workflow.Recoverer`: kimlikle adreslenen bir
  yürütmenin telafi zincirini koşturur ve Invoke'u ASLA çağırmaz. İsteğe bağlı
  arayüz (tip doğrulaması), `Executor`'a eklenen bir metot değil — sahte
  uygulamaları kırmamak için.

  **Beş reddin hepsi parayla ilgili** ve komut hiçbirini ezemez: kira
  bildirilmemişse (kirasız, koşan saga ile terk edilmiş kayıt ayırt edilemez),
  kayıt `running` değilse, kira DOLMAMIŞSA, talep kaybedilmişse ve verilen tanım
  kaydın workflow adını taşımıyorsa. Kaydı olmayan engelleyici adımdaki sınır da
  aynen duruyor: tahsilatın gerçekleşip gerçekleşmediği kayıtlardan
  yanıtlanamaz, operatör de sağlayıcıya sormadan yanıtlayamaz.

  Komutun kapısı `migrate down` ile aynı: `-confirm` kimliği tekrar etmeden
  hiçbir şey çalışmaz. Kimlik `gobit stuck` çıktısından KOPYALANAN bir değerdir
  ve bir satır yukarısı başka bir müşterinin saga'sıdır.

  Telafi zincirinin tanımı kaydın kendi GİRDİSİNDEN kuruluyor
  (`checkout.Workflows.RecoveryWorkflow`): adımların kurulduğu plan süreçle
  birlikte gitmiştir ama motorun kaydında JSON olarak durur. Ödeme verisi kayda
  yazılmadığı için geri de gelmiyor — telafi onu kullanmıyor.

  Bileşim kökü tek kopya kaldı: komut, sunucunun kurduğu kablolamanın AYNISINI
  `openApplication` üzerinden kullanıyor. İkinci bir kopya, bir modülün birine
  eklenip ötekine eklenmediği gün ayrışırdı. Eklentilerin sıraya alınmış
  kayıtları (`plugin.Registry.Start`) bilinçli olarak UYGULANMIYOR: bir saniye
  sonra çıkacak süreçte olay tüketicisi başlatmak, mesajı alıp ölen bir
  tüketiciden daha kötüsünü üretir.

  Zamanlanmış süpürücü hâlâ yok ve gerekçesi
  [ADR 0017](docs/adr/0017-recovering-abandoned-sagas-from-the-record.md)'de.

### Değiştirildi

- **`internal/core/workflow` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı).
  Beş turda motorun kendisi, `pgstore` ve ikisinin TÜM test dosyaları çevrildi;
  defter 715 dosyadan **708**'e indi.

  Test dosyalarında çeviri üretim dosyalarından farklı bir iştir: yorumlar kadar
  TEST VERİSİ de dile bağlıdır. Adım adları, workflow adları, idempotency
  anahtarları, yürütme kimlikleri, hata kodları ve paylaşılan durum anahtarları
  hep dize sabitidir; hepsi çevrildi ve doğrulamayı derleyici değil testlerin
  kendisi yaptı (iddialar aynı dizelerden türüyor).

  **Test ADLARININ çevrilmesi ek bir bağ getiriyor:** depo tüm test adlarını
  indeksliyor (`TestBelgelerdekiAtiflarCozuluyor`) ve markdown'da ters tırnak
  içinde anılan bir ad çözülmezse arch suite'i düşer. Bu turda iki atıf
  güncellendi — `workflow_test.go`'nun pgstore testine yaptığı godoc atfı ve
  ADR 0017'nin tekellik ölçümünü anan satırı.

  **Üç şeritli dedektör işini gösterdi:** diyakritikler temizlendikten sonra
  suite yeşildi ama dil testi hâlâ düşüyordu; "yok", "eski", "guncel", "talep"
  gibi Türkçe harf taşımayan sözcükler kelime şeridinden yakalandı.

  Davranış değişmedi; üretim koduna dokunulmadı.

- **`internal/core` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı). Workflow
  turunun ardından gelen dört turda `core/http`'nin kalan dosyaları,
  `redisguard`, `core/query`, `core/openapi` ve `core/config` çevrildi; defter
  708 dosyadan **680**'e indi ve defterde artık `internal/core/` ile başlayan
  TEK BİR satır yok. Kalan borç `internal/modules/*`, `internal/e2e`,
  `internal/arch`'ın kendi testleri ve ADR 0001-0011'de.

  Çeviri, tanımlayıcıları da taşıdığı için paket sınırını iki yerde aştı:
  `MemoryIdempotencyStore.Butce()` erişimcisi `Budget()` oldu (bileşim kökü onu
  sınıyor, `cmd/server/setup_test.go` da güncellendi) ve `config`'in hata
  metinleri İngilizceye geçince `internal/smoke/graphql_test.go`'nun iddia
  dizesi yenilendi. İkinci dosya defterde ve Türkçe KALIYOR — çevrilen yalnızca
  aradığı metin.

  **Çeviri turu üç gerçek sapma buldu**, üçü de yalnızca metni taşırken:

  - `openapi` paketinde bir godoc tanımından KOPMUŞTU. `alan` tipinin godoc'u
    zaten İngilizce yazılmıştı ("field is what schema generation needs…"), yani
    `TestGodocBicimi`'nin "godoc, bağlandığı tanımın ADIYLA başlar" kuralını tip
    Türkçe adını taşıdığı sürece sağlayamıyordu. Tip `field` oldu.
  - `middleware_test.go`'nun "ascii dışı" vakası `kimlik-ışık` değeriyle
    kuruluydu. Düz çeviri değeri saf ASCII'ye çevirip testi SESSİZCE anlamsız
    kılıyordu: reddedilmesi beklenen değer artık geçerli bir istek kimliği.
    Vaka ASCII olmayan bir değerle yeniden kuruldu.
  - `config.go`'da Türkçe cümlenin sırası `fmt.Errorf`'un argüman sırasını
    belirliyordu; İngilizcesi ters sıraya gidince `go vet` "%d has arg
    c.AppEnv of wrong type string" dedi.

  Davranış değişmedi; ölçüm tablolarındaki sayılar ve birimler korundu, yalnızca
  ondalık ayracı virgülden noktaya çevrildi (298,9 ms → 298.9 ms) — İngilizce
  metinde virgül binlik ayracı okunur ve ölçüm üç basamak kayardı.

## [0.8.0] — 2026-09-04

### Kırıcı değişiklikler

`0.x` boyunca minor sürümde meşrudur (bkz. dosyanın başı). Üçü de HTTP
yüzeyini değiştirmiyor; ikisi çerçeveyi GÖMEN kurulumları, biri kendi saga
adımını YAZANLARI ilgilendiriyor.

- **`cart/service.Store` portunun `SetLineItemTotals` imzası değişti.** Eski
  imza satır başına çağrılıyordu ve güncellenen satırı döndürüyordu; yenisi bir
  hesap turunun TÜM satır tutarlarını tek çağrıda alıyor ve yalnızca hata
  döndürüyor:

  ```go
  // eski
  SetLineItemTotals(ctx, cartID, lineID string, totals models.LineTotals) (models.LineItem, error)
  // yeni
  SetLineItemTotals(ctx, cartID string, lines []models.LineItemTotals) error
  ```

  Bu portu kendisi uygulayan bir kurulum derlenmez. Sebep bir ölçümdür ve
  aşağıda "Değiştirildi" altında yazılı: satır başına UPDATE, sepetin kilidini
  satır sayısıyla orantılı süre tutuyordu.

- **Motorun, `pgstore`'un, `core/link`'in ve `core/eventbus`'ın hata MESAJLARI
  İngilizce.** Hata KODLARI ve durum sabitlerinin DEĞERLERİ değişmedi — onlar
  makine sözleşmesidir ve dokunulmadı. Etkilenen tek sınıf, mesaj METNİNE
  bağlanmış iddialardır: bu turda deponun kendi testlerinde tam olarak böyle üç
  bağ kırıldı ve ancak koşulunca görüldü, çünkü kod ile mesaj arasında
  derleyici bağı yoktur. Müşteriye mesajı OLDUĞU GİBİ gösteren bir vitrin de
  etkilenir (ADR 0012'nin cırcırı; aynı sınıf v0.6.0'da başlamıştı).

- **`workflow.Step` sözleşmesi büyüdü: `Compensate` EŞZAMANLI çağrılabilir.**
  Bugüne dek "iki kez çağrılabilir" deniyordu ve bu SIRAYLA demekti. Kurtarma
  yolu bir Compensate'i aynı ANDA çağırabilen ilk yoldur. Bu depoda dağıtılan
  iki depo (`NewMemoryStore`, `pgstore`) kurtarmayı tekelli yaptığı için pratikte
  kapı kapalıdır; BAŞKA bir `workflow.Store` uygulayan kurulumda açıktır ve
  telafisini oku-değiştir-yaz olarak yazan bir adım stoğu birden çok kez
  bırakır. Geri alma KİMLİKLE yapılmalıdır.

### Eklendi

- **`gobit stuck`: yarım kalmış saga'lar artık LİSTELENEBİLİYOR.** v0.7.0
  kesintiye uğrayan bir ödemeyi sessiz olmaktan çıkarmıştı (kirası dolan
  yürütme kapanıyor, iş yapılmışsa `compensation_failed` yazılıyor ve ERROR
  loglanıyor) ama o kaydı GÖRECEK hiçbir yüzey yoktu; operatör psql açıyordu.
  Komut YALNIZCA OKUR: hiçbir rezervasyon bırakılmaz, hiçbir yürütme kapanmaz,
  hiçbir anahtar serbest bırakılmaz — hâlâ koşan bir saga'nın stoğunu bırakmak
  onu ikinci kez ayırtır.

  Komut İKİ sınıf listeliyor ve ikincisi ölçülerek bulundu. Durum sorgusu
  (`compensation_failed`) yalnızca motorun KAPATTIĞI kayıtları görür; oysa
  süreç saga'nın ortasında ölür ve müşteri bir daha dönmezse kayıt sonsuza dek
  `running` kalır, stoğu tutar ve hiçbir log satırında geçmez. Ölçüm: elle
  müdahale bekleyen iki yürütmeden yalnızca biri durum sorgusuyla bulunuyordu.
  İkinci sınıf bu yüzden "kirası dolmuş VE hâlâ tutulan adımı olan" olarak
  tanımlı — yalnızca yaşlı olan bir kayıt hiçbir şey tutmuyorsa motor onu kendi
  onarır ve listelemek operatörün sayfasını gereksiz satırla doldururdu.

  Kararın kendisi [ADR 0016](docs/adr/0016-operator-read-surface-for-half-done-sagas.md)'da.

  Bayatlığın kesim ANI artık sorgunun İÇİNDE hesaplanıyor ve satırlarla birlikte
  geri dönüyor: satırları SEÇEN an ile başlıkta YAZAN an aynı ifadeden geliyor.
  İki ayrı değer bırakmak, testte görünmeyen bir sapma sınıfıydı — testte
  çağıran ile veritabanı aynı makinede olduğu için çağıranın saatiyle süzüp
  veritabanının saatini yazan bir sürüm bütün suite'i geçiyordu (mutasyonla
  ölçüldü).

- **`gobit migrate status` ve `gobit migrate down <owner>`: migration'ların
  operatöre açık bir yüzeyi oldu.** `.down.sql` dosyaları vardı, geri
  alınabilirlikleri testliydi, ama onları çağıracak bir şey yoktu; geri alma
  elle yapılıyordu. `cmd/server` argüman bile okumuyordu — `--help` bile
  sunucuyu başlatıyordu.

  Sunucu HÂLÂ argümansız çalıştırmayla başlıyor ve başka hiçbir yolla
  başlamıyor; ileri migration açılışta otomatik kalıyor ve bilinçli olarak bir
  `migrate up` YOK, çünkü ayrı bir komut "şemayı güncellemeyi unuttum"
  sınıfını geri getirirdi.

  Geri alma GERİ ALINAMAZ bir iştir, o yüzden kapısı var: `-confirm <owner>`
  ile sahip adı ikinci kez yazılmadan hiçbir şey çalışmaz, varsayılan adım
  sayısı 1'dir ve KİRLİ bir defter (yarıda kalmış bir önceki koşu) onayla bile
  reddedilir — kirli durumu geri almak, hangi yarının uygulandığı bilinmeyen
  bir şemayı bir adım daha bozmaktır.

  Kaynak listesi İKİNCİ bir liste değil: modüller kendi migration'larını nasıl
  kaydediyorsa komut da onları oradan topluyor, yani sunucunun uyguladığı küme
  ile komutun gördüğü küme ayrışamaz.

  **Bilinen ve ölçülmüş bir tehlike godoc'a yazıldı:** golang-migrate advisory
  kilidi `context.Background()` ile alıyor, yani beklemeyi ne son teslim tarihi
  ne Ctrl-C keser. Ölçüldü: bağlamı 5 saniyede dolan bir `Version()` çağrısı,
  kilidi başkası tutarken 15 saniye sonra hâlâ dönmemişti. Bu STATUS yolunda da
  geçerli (sürüm okumak eksik sürüm tablosunu yaratır, o da kilidi alır), yani
  bir dağıtımın ileri migration'ı sürerken çalıştırılan `migrate status`
  sessizce bekleyebilir.

- **Vitrin listesinin toplam SAYACI artık isteğe bağlı**
  (`GET /store/v1/products?with_count=false`; GraphQL'de `count` alanını
  seçmemek yeter). Varsayılan DEĞİŞMEDİ: parametresiz istek bugünkü baytların
  aynısını alıyor.

  Sayaç ucuzlatılamadığı için isteğe bağlı yapıldı ve bu bir ölçüm sonucudur:
  kanal süzgeci ürün başına bir alt sorgu çalıştırıyor (`SubPlan`, `loops=52004`)
  ve sorgunun kendisi zaten indeks üstünde — `EXPLAIN` çıktısında `Heap
  Fetches: 0`. Yani gezilecek küme küçültülemiyor, yalnızca gezilmemesi
  sağlanabiliyor. Ölçüldü (52.004 ürün, LIMIT 20, ortanca): liste servisi
  sayarak **67,00 ms**, saymadan **0,65 ms**; sayacın kendisi 64,07 ms.

  Sayaç atlandığında zarfta `count` alanı **BULUNMAZ** — `0` dönmez, `null`
  dönmez. `0` yalan söylerdi ("sonuç yok"), `null` ise GraphQL şemasında
  `Int!`'i gevşetmek demekti; alanın yokluğu ise iki yüzeyde de aynı şeyi
  söylüyor: sayılmadı.

  Bir de bedava düzelen bir kusur: GraphQL'de `count` alanını hiç seçmeyen bir
  sorgu da sayaç SQL'ini çalıştırıyordu. Artık seçim kümesine bakılıyor
  (`@skip`/`@include` dâhil).

  README'nin "Bilinen sınırlar"ındaki 79 ms bayattı; bu turda yeniden ölçüldü
  ve satır güncellendi. Planın "tutarlı zarf" cümlesi de sayacın düşebildiğini
  söyleyecek şekilde düzeltildi — alan adları ve tipleri değişmiyor, yalnızca
  hesaplanmayan sayaç zarfta yer almıyor.

### Değiştirildi

- **Terk edilmiş bir saga'nın telafisi artık KAYITLARDAN çalıştırılıyor**
  ([ADR 0017](docs/adr/0017-recovering-abandoned-sagas-from-the-record.md)).
  Süreç saga'nın ortasında öldüğünde telafi hiç çalışmıyordu: ayrılan stok,
  açılan sipariş ve ödeme oturumu ortada kalıyordu ve README'nin yazdığı gibi
  "otomatik kurtarma YOKTU". Engel `StepContext.Shared`'ın kalıcı olmamasıydı —
  telafi "hangi rezervasyonu iptal edeceğim" cevabını oradan okuyor.

  Ölçüldü: o cevap kaybolmuş DEĞİL. Adımların Invoke çıktıları kalıcı ve telafi
  kaydı onları silmiyor (`StepRecord.Output` godoc'u bunu zaten bir karar olarak
  yazıyor). Eksik olan tek şey JSON'u tipli değere geri çevirmekti ve onu
  yalnızca adımın kendisi bilir: yeni `workflow.Recoverable` arayüzü bunu
  yapıyor. Uygulamayan adımı olan zincir bugünkü davranışı alıyor, yani arayüz
  yetenek ekliyor, sözleşme kırmıyor. Kurtarma tamamlanınca kayıt `failed` olup
  anahtarını BIRAKIYOR — müşteri aynı sepeti yeniden ödeyebiliyor.

  **Kurtarma bir noktada bilerek DURUYOR ve orası ödeme.** Motor adım kaydını
  Invoke döndükten SONRA yazıyor, yani tahsilatın içinde ölen süreç hiçbir iz
  bırakmıyor; kurtarma onu "çalışmamış" sayarsa kartı çekilmiş müşterinin stoğu
  bırakılır, anahtarı serbest kalır ve müşteri İKİNCİ KEZ tahsil edilir. Böyle
  bir adım `workflow.RecoveryBlocker` ile işaretleniyor ve kaydı yokken
  kendisinden öncekilerin de kurtarılmasını engelliyor. `complete_cart` için
  sonuç: çökmenin dört noktasından üçü kurtarılıyor, tahsilat noktası elle
  müdahalede kalıyor.

  Kurtarma tetiklenmiyor, denk geliniyor: aynı anahtarla dönen bir çağıran onu
  bulur. Zamanlanmış süpürücü bilinçli olarak eklenmedi — kurtarma yan etkisi
  olan bir iş çalıştırır.

- **Workflow motorunun KENDİSİ İngilizceye çevrildi**: `workflow.go` — paket
  yorumu (saga sözleşmesi, telafi kuralı, idempotency anahtarı, kalıcılık
  politikası), `Step`/`Recoverable`/`RecoveryBlocker` arayüzleri, `Executor` ve
  motorun tüm iç yordamları. Defter 716 dosyadan 715'e indi; paketin ÜRETİM
  kodunda Türkçe kalmadı, kalan beş dosyanın hepsi testtir.

  İki TEST BAĞI kırıldı ve kırıldığı yerde düzeltildi — ikisi de mesaj METNİNE
  bağlıydı, yani derleyici görmedi, ancak koşunca çıktı: `workflow_test.go`
  motorun kendi cümlesinin kaybolmadığını `"b" adımı` diye arıyordu ve
  `pgstore_integration_test.go` kurtarma reddini DÖRT yerde `ELLE MÜDAHALE`
  diye arıyordu. Bu, çeviri turlarının tekrar eden bulgusu: mesaj metnine
  bağlanmış iddialar, çeviriyi ancak koşarak fark ettiren tek bağ.

  Çevirinin kendi tehlike sınıfı da vardı ve üç yerde denk gelindi: Türkçe
  cümlenin öğe sırası İngilizcede DEĞİŞİYOR, dolayısıyla `%q ... %d ... %q`
  operandları da yeniden sıralanmak zorunda ("%q workflow'unun %q adımı (%d)
  başarısız oldu" → "the %q step (%d) of the %q workflow failed"). Aynı tipteki
  iki operandı takas etmek derleyici için görünmezdir; `go vet` de aynı tipte
  olduklarından susar. Üçü de çeviriyle birlikte elle sıralandı ve suite
  koşuldu.

  Davranış değişmedi: hata KODLARI (`workflow_step_failed`,
  `workflow_recovery_failed`, …) ve durum sabitlerinin değerleri aynı — onlar
  makine sözleşmesi. Değişen yalnızca insan okuyan metin.

- **Workflow motorunun sözleşme dosyaları İngilizceye çevrildi**: `store.go`
  (Store arayüzü, durum sabitleri, kayıt tipleri), `options.go` (RunOption'lar
  ve yeniden deneme politikası), `memory.go` ve `parallel.go`. Defter 720
  dosyadan 716'ya indi.

  Çeviri BAYAT BİR CÜMLE ortaya çıkardı ve düzeltildi: `ParallelStep`'in tip
  godoc'u "Compensate tüm dalları TERS SIRADA ve SIRAYLA çağırır" diyordu, oysa
  uygulama dal telafilerini EŞZAMANLI koşuyor ve bunun gerekçesi aynı dosyanın
  başka bir godoc'unda yazılı (sıralı yürütme, yavaş bir dalın ortak bütçeyi
  tüketmesi yüzünden sonraki dalları ölü bağlamla çağırıyordu). İki cümle
  birbiriyle çelişiyordu; İngilizce metin uygulamanın yaptığını yazıyor. Aynı
  godoc'ta İKİNCİ bir kopya daha vardı ("iç geri alma sırayla ve ters dal
  sırasında yürür") — iç geri alma da aynı eşzamanlı yola gidiyor; o da
  düzeltildi.

- **`internal/core/workflow/pgstore`'un ÜRETİM dosyaları İngilizceye çevrildi**
  (ADR 0012'nin cırcırı): `pgstore.go`, `convert.go`, `sql.go`, `ids.go`,
  `migrations.go` ve iki migration SQL'i. Defter 727 dosyadan 720'ye indi;
  pakette Türkçe kalan üç dosya da test dosyalarıdır.

  Davranış değişmedi ama bir TEST BAĞI kırıldı ve kırıldığı yerde düzeltildi:
  hata eşlemesini sınayan tablo, birincil anahtar ihlalinin mesajında
  "kimlikli" kelimesini arıyordu. Bu, deponun kendi mesaj METNİNE bağlanmış tek
  iddiaydı; kod ve mesaj arasında derleyici bağı olmadığı için ancak koşunca
  görülür.

  Migration dosyalarının yalnızca YORUMLARI değişti; DDL'e dokunulmadı ve
  golang-migrate dosyaları sürüm numarasına göre uyguladığı için uygulanmış bir
  veritabanı etkilenmez.

- **`internal/core/link` ve `internal/core/eventbus` İngilizceye çevrildi**
  (ADR 0012'nin cırcırı). Türkçe defterinden 15 satır DÜŞTÜ: 742 dosyadan
  727'ye; yol defteri 38'de kaldı (iki pakette hiç yol kaydı yoktu). İki pakette
  Türkçe harf sayısı sıfır.

  Çeviri davranışı değiştirmedi ve bu iddia yapısal olarak sınandı: yorumları
  atıp dizeleri ve tanıtıcıları normalleştiren bir AST karşılaştırması altı
  üretim dosyasının beşini BİREBİR aynı gösteriyor. Altıncısında iki biçim
  dizesinin operand sırası değişti — Türkçe cümlenin öğe sırası İngilizcede
  başka — ve o sıra hiçbir kapının göremediği bir yerdi: aynı tipte üç operand
  arasında `go vet` bir şey görmez. Sıra artık bir entegrasyon testiyle çivili
  ve testin fikstürü de ölçüldü: adı çakışan bir GÖRÜNÜM ile DDL bir adım önce
  düşüyor, MATERYALLEŞTİRİLMİŞ görünümle ise "başarıyla" tamamlanıp denetime
  ulaşıyor — yani sessiz şekil budur.

  Hata KODLARI değişmedi (beş üretim dosyasında birebir aynı) ve hata
  ayrıntılarının ANAHTARLARI da artık testli: `stored` anahtarının hem varlığı
  hem DEĞERİ sabitlendi — yalnızca varlığını sınayan bir iddia, saklanan tanım
  yerine geleni yazan bir hatayı geçiriyordu ve operatör iki tanımı aynı
  görürdü.

- **Sepet satır tutarları TEK deyimle yazılıyor; sepetin kilidi satır sayısıyla
  orantılı süre boyunca tutulmuyor.** Hesap turu satır başına bir UPDATE
  koşuyordu ve bunu sepetin `FOR UPDATE` kilidi altında yapıyordu; kilit o
  sepete yazan her akışı sıraya dizdiği için süre doğrudan sepetin yazma
  kapasitesiydi. Ölçüldü (100 satırlık sepet, kilidin alınmasından son yazmanın
  dönmesine kadar, p50): satır başına UPDATE **8,0 ms**, tek deyim **0,55 ms**;
  10 satırda 0,28 ms, yani satır sayısıyla neredeyse hiç uzamıyor.

  Ölçüm dürüst okunmalı ve godoc'lar bunu artık söylüyor: test harness'ının
  konteyneri `fsync=off` koşuyor, dolayısıyla bu sayılar YAZMA EVRESİDİR,
  ardından gelen commit'in WAL flush'ı değildir. Flush da aynı kilidin altında
  ve bu değişiklik ona dokunmuyor — kalıcı bir kümede ölçüldü, satır sayısından
  bağımsız 6,2 ms. Yani operatörün göreceği kilit süresi ~14,2 ms'den ~6,8 ms'ye
  iner: **~2 kat**, yazma evresinin kendi içindeki 14 kat değil.

  Boru hattı (pgx batch / sqlc `:batchexec`) bilerek REDDEDİLDİ ve gerekçe bir
  sayı: aynı 100 UPDATE tek boru hattında 3,0 ms sürüyor, yani kazancın yalnızca
  üçte ikisi. Kalan fark deyim başına ayrıştırma/planlama maliyetidir ve onu
  ancak deyim sayısını 1'e indirmek siler.

  Tutar–satır eşleşmesi API şekliyle korunuyor: kimlik tutarlarıyla AYNI değerde
  taşınıyor (`LineItemTotals`), yani çağıran iki ayrı dilimi farklı sıralarda
  veremez. Eksik yazılan tur sessiz geçmiyor — eşleşmeyen kimlik (silinmiş satır,
  başka sepetin satırı) turu düşürüyor ve hata çağıranın sırasındaki İLK
  yazılamayan satırı adlandırıyor.

### Düzeltildi

- **Motor, hiçbir adım koşmadan BAŞARI dönebiliyordu.** Yürütme açmayı en fazla
  iki tur denerken ikinci turda da "terk edilmiş, yeniden dene" cevabı gelirse
  döngü bitiyor ve o noktada `replay`'in dönüş değeri `(nil, nil)` oluyordu —
  bu değer olduğu gibi çağırana veriliyordu. Ölçüldü: `out=<nil> err=<nil>
  invokes=0`. Çağıran nil hatayı "sipariş verildi" diye okur; sepet akışında
  bunun anlamı, hiçbir siparişin açılmadığı bir başarı yanıtıdır — bir saga
  motorunun söyleyebileceği en kötü yalan.

  Döngünün ardındaki hata artık gerçekten dönüyor ve sınıfı `KindUnavailable`
  (503), yeni kodu `workflow_execution_contended`: sistem bozuk değil, anahtar
  çekişmede ve hiçbir adım koşmadığı için çağıran AYNI anahtarla
  tekrarlayabilir. Üretimde bu duruma art arda iki terk edilmiş yürütmeyle ya da
  gerçek saga süresinden kısa bildirilen bir `WithLease` ile varılır.

- **Kendi kendine çözülen bir yarış 500 dönüyordu.** `Create` "anahtar dolu"
  dedikten sonra okuma "böyle bir yürütme yok" diyorsa, iki çağrı ARASINDA
  anahtar bırakılmıştır — telafi edilen bir yürütme anahtarını bırakır ve terk
  edilmiş bir kaydı kapatan her çağıran bunu yapar. Motor bu okumayı
  `workflow_store_failed` diye sarıyordu, yani müşteri kendi kendine çözülen bir
  yarış yüzünden 500 alıyordu (ölçüldü: aynı terk edilmiş kayda dört eşzamanlı
  çağıran vardığında biri tam olarak bu hatayı aldı). Artık yeniden AÇMAYI
  deniyor; anahtar zaten serbesttir.

  İki arıza da aynı avda, v0.7.0 sonrası eklenen kurtarma yolunun eşzamanlılık
  ölçümüyle bulundu; ikisi de mutasyonla kanıtlandı (eski davranış geri
  konduğunda testler tek tek düşüyor).

- **Kurtarma TEKELLİ oldu: terk edilmiş bir kaydı artık tek bir süreç telafi
  ediyor.** Terk edilmiş kayıt kimsenin sahipliğinde olmadığı için aynı anahtarla
  dönen her çağıran onu buluyordu ve HEPSİ telafi zincirini koşuyordu — dört
  eşzamanlı çağıranla ölçüldü, zincir dört kez koştu. Motor artık kurtarmadan
  ÖNCE kaydı talep ediyor: tek bir koşullu UPDATE, yalnızca kayıt hâlâ `running`
  iken ve `updated_at` "bu terk edilmiş" kararının dayandığı değerken tutuyor.
  Kazanan `updated_at`'i damgalıyor; bu hem ötekileri eliyor hem de kirayı
  kurtarma sürdükçe tazeliyor. Ölçüm: aynı dört çağıran, TEK telafi (talep
  kaldırıldığında dörde dönüyor).

  Talep, adım kayıtları OKUNDUKTAN sonra ve ilk yazmadan önce alınıyor. Okuma
  yan etkisizdir; kazanılan talep ise `updated_at`'i damgalar, yani kirayı
  uzatır. Talep önce gelseydi, adımları okuyamayan — yani hiçbir şey yapmayan —
  bir çağıran kaydın kirasını sessizce ileri atmış olurdu ve gerçekten yarım
  kalmış bir saga hem bir sonraki çağırandan hem de `gobit stuck`'tan tam bir
  kira süresi boyunca saklanırdı.

  Talebi KAYBEDEN çağırana "hâlâ sürüyor" denmiyor, döngüye bir tur daha
  gönderiliyor: kazanan anahtarı her an bırakabilir ve ikinci tur iki sonu da
  doğru yanıtlar — anahtar serbestse yeni yürütme açılır, kazanan hâlâ
  çalışıyorsa bulunan kayıt TAZEDİR, yani "hâlâ sürüyor" o zaman doğrudur.

  Yetenek İSTEĞE BAĞLI bir arayüzdür (`workflow.ClaimingStore`), `Store`'a
  eklenen bir metot değil: port metodu, bu deponun dışında yazılmış her Store
  uygulamasını kırardı. Bedeli, `Store`'u GÖMEN bir sarmalayıcının yeteneği
  sessizce gizlemesidir (gömülü arayüz yalnızca kendi metotlarını taşır) —
  gerekçe ve sınır [ADR 0017](docs/adr/0017-recovering-abandoned-sagas-from-the-record.md)'de.

- **Telafinin EŞZAMANLI çağrılabildiği yazıya geçti** (davranış değişmedi).
  Terk edilmiş kayıt kimsenin sahipliğinde olmadığı için aynı anahtarla varan
  her çağıran onu kurtarır; dört eşzamanlı çağıranla ölçüldü, zincir DÖRT kez
  koştu. `Step` sözleşmesi bugüne dek yalnızca "iki kez çağrılabilir" diyordu ve
  bu SIRAYLA anlamına geliyordu. Deponun kendi adımlarında bedel yinelenen iş ve
  yinelenen sağlayıcı çağrısıdır (her telafi KİMLİKLE geri alır), ama telafisini
  oku-değiştir-yaz olarak yazan bir eklenti adımı stoğu birden çok kez bırakır.
  Sözleşme artık bunu açıkça yasaklıyor — ve aynı yayımlanmamış turda kapı da
  kapandı: kurtarma tekelli oldu (yukarıdaki maddeye bakın). Yasak yine de
  duruyor, çünkü tekelliği kuran yetenek isteğe bağlıdır ve bir adım altındaki
  Store'un onu sunup sunmadığını GÖREMEZ.

## [0.7.0] — 2026-09-03

### Kırıcı değişiklikler

Üçü de `0.x` boyunca minor sürümde meşrudur (bkz. dosyanın başı) ve üçü de
**yükseltirken bakılacak** şeylerdir.

- **`/ready` artık her bağımlılık için 503 DÖNMÜYOR.** Redis erişilemezken uç
  `200` ve gövdede `"status": "degraded"` döner; yalnızca Postgres gibi
  KESEN bir bağımlılık `503` üretir. 503'e alarm kuran bir kurulum Redis
  kesintisini artık o yoldan GÖRMEZ — sinyal gövdedeki `degraded` alanı ve
  düşen her yoklama için yazılan WARN satırıdır. Değişikliğin sebebi ve ölçümü
  aşağıda; kararın kendisi
  [ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md)'de.
- **Bir sepet en fazla 100 farklı satır taşır.** Tavana ulaşmış bir sepete YENİ
  satır açmak isteyen istek `400` ve `cart_workflow_line_limit_reached` alır.
  Var olan satırın adedini artırmak muaftır; tavandan ÖNCE açılmış daha büyük
  sepetler hesaplanabilir ve ödenebilir kalır, yalnızca yeni satır alamaz.
- **Arama sonuçlarının SIRASI değişti.** Sonuç KÜMESİ aynı; çok kelimeli bir
  sorguda artık alan ağırlığı (başlık > anahtar > açıklama) kelime yakınlığını
  yeniyor. Sıralamaya bağlı ekran görüntüsü testi olan istemciler etkilenir.

Çerçeveyi gömen (Go) kurulumlar için üç imza değişti:

- `NewMemoryIdempotencyStore` artık bayt bütçesini de alıyor (`ttl, butce`).
- `RouterOptions.ReadinessChecks` alanının tipi `GatingChecks` oldu ve yanına
  `DegradedChecks` geldi. Adlandırılmamış bir harita değişmezi hâlâ atanabilir;
  adlandırılmış `map[string]HealthCheck` tipinde bir DEĞİŞKEN geçen çağıran
  derlenmez — ve bu bilinçlidir, iki sınıfın karışmaması buna dayanıyor.
- `cart/api.Carts` arayüzünden `AddLineItem` kaldırıldı. Servis metodunun
  kendisi duruyor; satır ekleme akıştan geçer.

### Eklendi

- **Bellek içi idempotency deposu SINIRSIZ büyüyordu; bayt bütçesi geldi**
  (`IDEMPOTENCY_MAX_MEMORY_BYTES`, varsayılan 64 MiB). Depo her mutasyon isteği
  için yanıt gövdesiyle birlikte bir kayıt tutuyor, kaydı açan anahtarı İSTEMCİ
  seçiyor ve tek sınır 24 saatlik TTL'di. Ölçüldü (runtime.MemStats, GC
  sonrası): 1 KiB gövdeli 10.000 kayıt 15,51 MiB, 64 KiB gövdeli 10.000 kayıt
  630,69 MiB, 1 MiB gövdeli 1.000 kayıt 999,58 MiB tutuyordu; 50.000 kayıt
  yazılıp saat 23 saat ilerletildiğinde düşen kayıt sayısı SIFIRDI — TTL
  büyümeyi hiçbir yerde durdurmuyordu. `GUARD_BACKEND` varsayılanı `memory` ve
  `Validate` üretimde `redis` şart koşmuyor, yani sıradan bir üretim dağıtımı bu
  depoyu çalıştırıyor.

  Bütçe dolunca en ESKİ kayıt düşüyor. Reddetmek daha kötüydü: anahtarı istemci
  seçtiği için uydurma anahtarlarla gelen tek bir istemci mağazanın tüm mutasyon
  trafiğini kapatabilirdi — bellek arızası, tetiklemesi bedava bir erişim
  arızasına dönerdi. Düşürmenin bedeli, o anahtarla gelen tekrarın yeniden
  işlenmesidir ve bu TTL'in zaten ödediği bedelin aynısıdır; tahliye o silmeyi
  ERKENE alır, en eski kayıt da korumasından geriye en az kalmış olandır.
  Sessiz değil: ilk tahliye her zaman, sonrası dakikada bir WARN loglanıyor,
  bütçe her açılışta yazılıyor, README ve `docs/mimari.md` sınırı adıyla anıyor.

  Kayıtlar artık haritanın yanında süreye göre sıralı bir listede duruyor.
  Eski süre-dolumu TÜM haritayı tarıyordu ve tarama sürecin TEK idempotency
  kilidini tutarken koşuyordu: 1.000.000 kayıtta 50,3 ms, 100.000 kayıtta
  2,13 ms. Artık yalnızca süresi dolan ÖN EK dolaşılıyor: aynı iki harita
  boyunda 188 ns ve 164 ns. Bu, taramayı dakikada bire kısan sapmayı da
  gereksiz kıldı — o kısıntı, süresi dolmuş bir kaydın bir dakikaya kadar
  OYNATILMAYA devam etmesi demekti, yani TTL'in söylediğinden uzun bir koruma.
  Yanıt kopyası ve muhasebe kilidin DIŞINA çıkarıldı: 1 MiB gövdeli eşzamanlı
  oynatma 50,1-52,7 µs'ten 34,5-40,8 µs'e indi.

  **Kabul edilen en küçük bütçe 1 MiB'tan 2 MiB'a çıkarıldı.** Tabanın gerekçesi
  "tek bir azami boy yanıt sığmalı"ydı ama 1 MiB'ta sığmıyordu ve bu ölçüldü:
  1 MiB bütçeye yazılan 1 MiB'lık yanıt anında düşüyor, çünkü kaydın bedeli
  gövdenin yanında anahtarı, parmak izini ve yapısal maliyeti de taşıyor. Yani
  taban, tam olarak yasakladığı sessiz-işlevsiz yapılandırmayı KABUL ediyordu.
  Sabit eşitliği sınayan test, davranışı sınayan bir testle değiştirildi.

- **PostgreSQL havuzunun sınırları ayarlanabilir oldu** (`DB_MAX_CONNS`,
  varsayılan 10; `DB_MIN_CONNS`, varsayılan 2). Sayı sabit yazılıydı ve hiçbir
  ortam değişkeni onu değiştiremiyordu; oysa havuz TEK BİR isteğin değil TÜM
  SÜRECİN veritabanı eşzamanlılık tavanıdır — HTTP istekleri, workflow motoru ve
  olay tüketicisi aynı havuzdan çeker.

  Tavanın gözden kaçan tarafı GraphQL'de: gqlgen kök alanlarını eşzamanlı çözer
  ve sayıyı sınırlamaz, yani `GRAPHQL_MAX_FIELD_REPETITION=20` ile tek bir meşru
  vitrin belgesi 40 eşzamanlı okuma açabilir. Ölçüldü (52.000 ürün, gerçek
  vitrin sorguları, 40 eşzamanlı kök alanı): 10 bağlantıda 813 alımın 771'i
  bekliyor, ortalama bekleme 65,3 ms.

  Varsayılan yine de 10 KALDI ve sebebi ölçüm: veritabanı uygulamayla aynı
  kutudayken darboğaz havuz değil sunucunun CPU'su, büyütmek gecikmeyi geri
  getirmiyor (p50 306 ms → 368 ms). Veritabanı ağın ötesindeyse kazandırıyor ve
  kazanç kök alanın gidiş dönüş sayısına bağlı: liste yolunda 5 ms'lik atlamada
  1,3 kat (459 → 348 ms), 20 ms'de 1,8 kat (638 → 351 ms); üç gidiş dönüşlük
  tekil ürün alanında 3,8 kat (69,2 → 18,0 ms). Yani eksik olan sayı değil
  DÜĞMEYDİ — varsayılanı yükseltmek her kurulumun küme bağlantı bütçesini
  çarpardı, kazanç ise yalnızca gecikmeye bağlı topolojilere düşer.

  Sınırların gerçekten havuza ULAŞTIĞI testli ve iki uçtan da çivili: havuz 1
  bağlantıyla açılıp cevap veriyor (paylaşılan bir kümeye çok örnekle bağlanan
  kurulumun godoc'ta önerilen çaresi buydu ve o güne kadar yalnızca bir yapı
  iddiasıydı), 250'lik bir tavan da değiştirilmeden geçiyor. İkisi de sessiz
  mutasyonlara karşı: `max(cfg.MaxConns, 4)` biçiminde bir taban ya da 64'lük
  bir tavan, 4 ile yazılmış bir testle uyuşup açılış logunun yazdığından farklı
  bir havuz çalıştırırdı.

- **`DATABASE_URL` içindeki `pool_*` parametreleri artık açılışta UYARILIYOR.**
  pgxpool onları okuyor, uygulama ise havuz alanlarını yapılandırmadan ezdiği
  için `?pool_max_conns=40` hiçbir şey yapmıyordu — sessizce. Havuz sabit
  yazılıyken zararsızdı; `DB_MAX_CONNS` var olduğu andan itibaren operatörün
  aynı sayıyı yazabileceği iki makul yer var ve biri hiçbir işe yaramıyor.
  Reddetmek değil uyarmak doğru: parametre ne kadar zamandır yok sayılıyorsa o
  kadar zamandır açılan bir süreci durdurmak, önlediği sürprizden büyük bir
  bedeldir.

- **Sepete satır sayısı TAVANI: 100** (`cart.MaxLineItems`). Tavana dayanmış
  bir sepete YENİ satır açmak isteyen istek `409` değil `400` ile ve
  `cart_workflow_line_limit_reached` koduyla reddedilir; mesaj hem tavanı hem
  sepetteki satır sayısını yazar. Kırpma YOK.

  Sebebi ölçülmüştür: satır ekleyen her istek sepetin tüm satırlarının tutarını
  yeniden YAZAR (cart modülünün `SetTotals`'ı satır başına bir UPDATE, sepetin
  kilidi altında), yani 100 satırlık bir sepeti kurmak 5.050 satır yazımı,
  1.000 satırlık bir sepet 500.500 yazım eder. Tavansız bir sepet, tek bir
  istemcinin veritabanını meşgul edebileceği süreyi sınırsız bırakıyordu.

  Tavan yalnızca satır AÇAN yolda uygulanır: sepette zaten duran bir varyantı
  yeniden eklemek adedi artırır ve tavana takılmaz — takılsaydı dolu bir sepetin
  sahibi kendi satırının adedini bile artıramazdı. Hesap turu, adet güncellemesi
  ve sipariş yolu tavanı hiç sormaz, çünkü tavan konmadan önce açılmış ve bugün
  100'ün üstünde satır taşıyan bir sepet hesaplanabilir ve tamamlanabilir
  kalmalıdır. Tavan bir KAPIDIR, kesin bir üst sınır değil: karşılaştırma sepet
  kilidinin dışındaki anlık görüntüye bakar, eşzamanlı iki ekleme birkaç satır
  aşabilir.

  Tavanın dayandığı "tek kapı" iddiası uğruna `cart/api.Carts` arayüzünden
  `AddLineItem` KALDIRILDI (kırıcı; servis metodunun kendisi duruyor ve akış
  onu çağırıyor). Metodun hiçbir çağıranı yoktu ama arayüzde durması, ona
  bağlanacak bir handler'ın hem sunucu tarafı fiyatlandırmayı hem tavanı
  sessizce atlamasına açık kapı bırakıyordu — aynı gerekçeyle `CreateCart` da
  o arayüzde yok.

- **`pricing.interop` toplu fiyat yüzeyi yayımlıyor**
  (`CalculateAmountsJSON`, kalem tavanı `MaxCalculateItems` = 1000). İstek
  sırasını korur, kalem başına "fiyatlandı" BAYRAĞI döner (hata değil) ve
  fiyatı olmayan kalem yüzünden isteğin tamamını düşürmez. Tavan aşılırsa istek
  bütün olarak reddedilir; kırpmak, çağıranın sepetinin bir kısmını fiyatsız
  bırakıp sonucu "başarılı" göstermek olurdu. Kalem sayısı 280 ile 300 arasında
  planın indeksten tam taramaya döndüğü ölçüldü ve sabitin godoc'una yazıldı —
  1000'e kadar maliyet doğrusal değildir.

- **`SHUTDOWN_TIMEOUT` saga bütçesinden kısaysa açılışta UYARI.** Varsayılanlar
  15 saniye ve 2 dakika, yani sıradan bir deploy uçuştaki bir ödemeyi ortasından
  kesebilir. İkisi de yanlış değil — 15 saniye makul bir deploy bütçesi
  (Kubernetes'in varsayılan grace period'u 30 saniye), 2 dakika üç modül ve bir
  ödeme sağlayıcısı geçen bir zincir için makul bir tavan. Yanlış olan, bir
  kurulumun hangisini seçtiğini BİLMEMEK.


- **PostgreSQL'in bir SEÇENEK değil TEMEL olduğu yazıya geçti**
  ([ADR 0015](docs/adr/0015-postgresql-cluster-contract.md)). gobit
  PostgreSQL'i desteklemiyor, onun ÜZERİNE yazılmış — ve bu bağımlılık bugüne
  kadar hiçbir yerde sözleşme olarak durmuyordu. Bu turda tam da bu yüzden bir
  blocker çıktı: küme `--locale=C` ile kuruluyordu ve on dört ADR'nin hiçbiri
  locale'den bahsetmiyordu.

  ADR bağımlılığın nerede yaşadığını SAYIYOR — dizi parametreleri (`= ANY`)
  üzerine kurulu N+1'siz okuma katmanı, iş kuralının kendisi olan kısmi tekil
  indeksler (`UNIQUE (handle) WHERE deleted_at IS NULL`), `jsonb`,
  `timestamptz` (235 sütun), advisory lock'lar, ve `core/link`'in HER AÇILIŞTA
  koştuğu DDL — sonra kümenin sağlaması gerekenleri bir tabloya bağlıyor:
  sürüm, encoding, CTYPE, uzantılar (bugün SIFIR), yetkiler, `search_path`.

  Sözleşme bir PROB ile uygulanıyor, çünkü gerçek dağıtımda compose dosyasını
  düzeltmek yetmez: RDS/Cloud SQL/Neon'da `initdb` argümanını siz seçmezsiniz.
  Prob AD değil DAVRANIŞ sınıyor ve **bugün tek bir kontrolü var** — bu bir
  eksiklik değil karar: tablodaki öteki satırların hepsi GÜRÜLTÜLÜ düşüyor
  (insert reddedilir, `link.Define` patlar, sorgu "relation does not exist"
  der), yalnızca harf katlaması doğru, boş ve sessiz bir cevap dönerek düşüyor.

  İkinci bir veritabanı desteklenmeyecek ve gerekçesi ideolojik değil:
  listenin ilk üç maddesi taşınabilir değil, üstelik ikinci lehçe deponun
  "her kural TEK yerde tanımlı" disiplinini her değişmez için bozar.

### Değiştirildi

- **Redis kesintisi TÜM kopyaları aynı anda trafikten çıkarıyordu.** `/ready`
  bugüne kadar tek sınıf yoklama tanıyordu: biri düşünce 503. `GUARD_BACKEND`
  çok örnekli her kurulumda `redis` olduğu için Redis o kümeye giriyordu ve bir
  failover sırasında bütün pod'lar aynı saniyede NotReady oluyordu — Kubernetes
  Service'i boşaltıyor, trafiğin kaydırılabileceği sağlıklı kopya kalmıyor,
  kısmi bir bozulma tam bir kesintiye dönüyordu. Bu, ADR 0007'nin koruma
  katmanları için REDDETTİĞİ "her şey için fail-closed" seçeneğinin bir kat
  yukarısıdır; ADR o bölümle genişletildi.

  Yoklamalar artık İKİ SINIF: `ReadinessChecks` düşerse 503 ve örnek trafikten
  çıkar (Postgres), `DegradedChecks` düşerse gövdede bildirilir ama kod 200
  kalır (Redis). Gövdedeki `status` üç ayrı değer alır — `ok`, `degraded`,
  `unavailable` — çünkü eskiden 503 de "degraded" diyordu ve iki durum bir
  logdan ayırt edilemiyordu.

  Redis'in derecelendiren tarafa konması ÖLÇÜLDÜ (`GUARD_BACKEND=redis`, Redis
  kapalı): vitrin katalog okuması 200, `Idempotency-Key` taşımayan yazma 200,
  taşıyan yazma istek başına yeniden denenebilir bir 503
  (`idempotency_store_unavailable`). Hiçbir istek yanlış işlenmiyor —
  korunamayan tek sınıf reddedilen tek sınıf. Kapı yapmak, 200 dönen istekleri
  de birlikte götürürdü.

  İki sınıfın Go tipi de AYRIDIR (`GatingChecks`, `DegradingChecks`): bir
  bağımlılığı taraf değiştirmek tek kelimelik, incelemede masum görünen bir
  düzenlemedir ve her testi geçer. Adlandırılmamış bir `map[string]HealthCheck`
  ikisine birden atanabildiği için bileşim kökünde o tipin kullanılmadığı da
  ayrıca sınanıyor (`TestReadinessMapsUseTheNamedTypes`) — mutasyonla
  doğrulandı: adlandırılmamış harita kullanan sürüm Redis'i kapı tarafına geri
  koyuyor ve depodaki hiçbir test düşmüyordu.

  Derecelendiren yoklamaların bütçesi ayrı ve KISA (varsayılan 250 ms,
  `READINESS_DEGRADED_TIMEOUT`): erişilemez bir Redis'e atılan tek Ping 1,7
  saniye sürüyor (istemci beş kez deniyor) ve kubelet'in varsayılan probe zaman
  aşımı 1 saniye — bütçesiz bir "bozulma" yoklaması, probu düşürerek aynı
  kesintiyi arka kapıdan geri getirirdi. Bütçe aşımı gövdede bütçeyi adıyla
  yazar; ama bütçenin bir bedeli var ve godoc'a yazıldı: kök sebebi yok ediyor,
  "connection refused" ile DNS hatası aynı cümleye iniyor.

  Düşen her derecelendiren yoklama WARN logluyor ve satır örneğin HİZMET
  VERMEYE DEVAM ETTİĞİNİ söylüyor: kod 200 kaldığı için orkestratörde hiçbir
  olay üretmez, yani o satır bozulmanın tek alarm kanalıdır. Aynı ad iki sınıfa
  birden yazılırsa kapı tarafı kazanır ve bu da açılışta bir kez uyarı olarak
  bildirilir.

- **Sepet kurmanın fiyat okuması KARESEL büyüyordu; doğrusala indi.** Satır
  ekleyen her istek sepetin TÜM satırlarını yeniden fiyatlıyor ve pricing'e
  satır başına iki sorgu açıyordu, yani N satırlık bir sepeti kurmak ~1,5N²
  gidiş-dönüş ediyordu. Ölçüldü (paketin kendi sahteleriyle, çağrılar
  sayılarak):

  | sepet | fiyat çağrısı (eski) | (yeni) | SQL sorgusu (eski) | (yeni) |
  |---|---|---|---|---|
  | 10 satır | 65 | 20 | 130 | 40 |
  | 50 satır | 1 325 | 100 | 2 650 | 200 |
  | 100 satır | 5 150 | 200 | 10 300 | 400 |

  Hesap turu artık pricing'in TOPLU yüzeyini (`service.CalculateAmountsJSON`)
  kullanıyor: kap sayısından bağımsız olarak iki sorgu. Toplu okumanın kendisi
  zaten vardı (`ListPriceCandidatesBySets`) ve hesap yoluna hiç bağlanmamıştı.
  Sorgunun kendisi de gerçek veriyle ölçüldü (54.000 kap): 50 kap için kap
  başına yol 4,93 ms, toplu yol 0,25 ms; 100 kap için 9,88 ms ve 0,33 ms.

  Seçilen TUTAR değişmiyor ve bu iddia testle çivili
  (`TestCalculateAmountsJSONMatchesCalculateAmount`): iki yol pricing'in aynı
  saf seçim fonksiyonunu aynı aday satırlarıyla çalıştırır. Tek fark toplu
  yolun saati BİR kez okumasıdır ve fark toplu yolun lehinedir — tam o sırada
  biten bir kampanya, aynı sepetin iki satırını farklı anlardan fiyatlayamaz.

  Satır AÇILIRKEN sorulan tek fiyat hâlâ tekil metotla soruluyor: ölçüldü, tek
  kapta toplu yolun üstünlüğü YOK (aday sorgusu 66 µs'ye karşı 77 µs) ve tekil
  metot daha kesin bir "kap yok" hatası veriyor.

- **Fiyatı olmayan satırların HEPSİ tek hatada bildiriliyor.** Toplu yanıt
  satırların tamamını birden taşıdığı için ilk fiyatsız satırda dönmek elde
  olan bilgiyi atmak olurdu: iki ölü varyantı olan bir sepetin sahibi ikisini
  de bu istekte öğreniyor, sepetini istek istek onarmıyor. Hata sınıfı ve kodu
  değişmedi (`Invalid`, `cart_workflow_price_unavailable`); tek satır fiyatsızsa
  mesaj da aynen eskisi gibi.

- **Arama sıralaması `ts_rank_cd` yerine `ts_rank` ile yapılıyor ve sıralama
  sorgusu artık sorgu başına bir kez hesaplanıyor.** Vitrinin arama ucu
  eşleşen HER belgeyi puanlamak zorundadır (GIN indeksi `ORDER BY`'ı
  karşılayamaz), dolayısıyla puanlama fonksiyonunun satır başına bedeli
  doğrudan ucun bedelidir. Ölçüldü (52.000 belgelik indeks, ~92 lexeme'lik
  belgeler, LIMIT 20):

  | eşleşme | ts_rank_cd | ts_rank | yalnızca eşleşme |
  |---|---|---|---|
  | 1 002 | 13,7 ms | 1,4 ms | 1,1 ms |
  | 10 400 | 148,0 ms | 23,0 ms | 21,7 ms |
  | 52 000 | 663,0 ms | 24,7 ms | 23,8 ms |

  Fark `ts_rank_cd`'nin belge başına ~12 µs'lik bedelidir ve planlayıcı bunu
  GÖREMEZ: `pg_proc.procost` her iki fonksiyon için de 1'dir. Kataloğun
  tamamında geçen tek bir kelime, varsayılan 600 istek/dakika kotasıyla
  saniyede 6,6 çekirdek yakıyordu.

  Sıralama GÖZLENEBİLİR biçimde değişti: `ts_rank_cd` kelime yakınlığını alan
  ağırlığının ÜSTÜNE koyabiliyordu, `ts_rank` koyamaz. "mavi gomlek"
  sorgusunda iki kelimeyi anahtar alanında (B) yan yana taşıyan ürün, ikisini
  de başlığında (A) taşıyan üründen önce geliyordu; artık başlık kazanıyor.
  İndeksin ağırlıklara ayrılmış olmasının sebebi budur, yani bu düzeltmedir.
  Yakınlık tamamen kaybolmadı — ölçüldü, iki kelime arasındaki boşluk 0'dan
  6'ya çıkarken skor 0,9910'dan 0,7615'e iniyor — yalnızca ağırlığı yenemez
  oldu.

  Sıralama **sorgunun olumlu kısmıyla** yapılıyor (`querytree`): `ts_rank`
  olumsuzlama taşıyan bir sorguda HER belgeye 0 verir, yani `gomlek -mavi`
  yazan alışverişçinin sonuçları alakaya göre değil indekslenme sırasına göre
  gelirdi — üstelik `-` desteği `websearch_to_tsquery`'yi seçmenin gerekçesi
  sayılırken. Yalnızca hariç tutmadan oluşan bir sorgu (`-mavi`) sıralanacak
  olumlu sinyal bırakmaz; o durumda sıra `product_id`'dir ve bu README'nin
  "Bilinen sınırlar" bölümünde yazılıdır.

  Sıralama ifadesi skaler alt sorgudur. pgx altıncı çalıştırmadan sonra genel
  plana geçebilir ve genel planda ifade sabite katlanmaz, satır başına
  yeniden ayrıştırılırdı: 52.000 eşleşmede 46,7 ms'ye karşı 25,4 ms.

- **Vitrinin satış kanalı görünürlük kuralı tek bir korelasyonlu alt sorguya
  indi.** Kural DEĞİŞMEDİ; nasıl yazıldığı değişti. Eski hâli iki bağımsız
  alt sorguydu ("hiç ataması yok VEYA istenen kanalda ataması var") ve
  `saleschannel.go`'nun yorumu aday satır başına bir indeks yoklaması
  yapıldığını iddia ediyordu. İddia yanlıştı: planlayıcı iki bağımsız EXISTS
  gördüğünde ikisini de hash'e çeviriyor, yani ilk satırı dönmeden ÖNCE link
  tablosunun tamamını iki kez tarıyor.

  Ölçüldü — 52.000 ürün, 52.000 kanal ataması, gerçek Postgres, vitrinin
  `GET /store/v1/products?limit=20` ucu:

  | | eski | yeni |
  |---|---|---|
  | liste sorgusu | 26,80 ms | **0,14 ms** |
  | sayaç sorgusu | 73,87 ms | 78,97 ms |
  | istekteki toplam SQL | 100,7 ms | 79,9 ms |

  Maliyet sayfa boyutuyla değil KATALOG boyutuyla büyüyordu, üstelik vitrinin
  en sıcak ucunda: aynı uç 2.000 ürünle 7,5 ms, 52.000 ürünle 113 ms sürüyordu
  ve ikisi de aynı 20 satırı dönüyordu.

  Yeni formülasyondaki `IS TRUE` bir süs DEĞİL: onsuz, kanal dizisi bir NULL
  eleman taşıdığında `bool_or` NULL'ı yutuyor, `COALESCE` onu "hiç ataması yok"
  sanıyor ve atanmış bir ürün yanlış kanalda GÖRÜNÜR oluyor — yani eksik hâli
  açığa düşüyor. Sekiz senaryoda ölçüldü. Ve hiçbir test bunu yakalayamaz,
  çünkü kanal dizisi Go'dan `[]string` gelir ve NULL eleman üretemez; gerekçe
  kodda yazılı.

- **Sayacın maliyeti bir SINIR olarak yazıya geçti** (README, "Bilinen
  sınırlar"). Vitrin listesinin toplam sayacı kanal süzgeciyle birlikte
  katalogun tamamına bakmak zorundadır ve düzeltilebilir bir şey değildir:
  aynı katalogda süzgeçsiz düz sayım 2 ms, kanal süzgeçli sayım 79 ms sürüyor.

### Düzeltildi

- **Ortasında kesilen bir ödeme sepeti SONSUZA DEK kilitliyordu.** Yürütme
  kaydı "running" açılır ve uç duruma geçerek kapanır; süreç o geçişi yazamadan
  ölürse (deploy, OOM, pod tahliyesi) kayıt sonsuza dek running kalır. Ölçüldü:
  üç gün önce çökmüş bir yürütme hâlâ *"hâlâ sürüyor"* diyordu ve o sepet bir
  daha ödenemiyordu.

  Motor artık bir KİRA süresi kabul ediyor (`workflow.WithLease`): çağıran
  akışının meşru olarak ne kadar sürebileceğini bildirir, ve o süreden uzun
  süre running duran bir kayıt hiçbir sürecin tutamayacağı bir kayıttır.
  Yaşlılık tek başına kanıt değildir, kira kanıttır — bu yüzden süre motorca
  tahmin edilmez, çağıranca bildirilir.

  Terk edilmiş bir kaydın ne yapılacağına ADIM KAYITLARINA bakılarak karar
  verilir ve iki dal da testli:

  - **Hiçbir adım iş yapmamışsa** telafi edilecek bir şey yoktur: kayıt
    `failed` olur, anahtarını bırakır, müşteri sepetini ödeyebilir.
  - **İş yapılmışsa** telafi hiç çalışmamıştır ve yarım iş ortadadır: kayıt
    `compensation_failed` olur, anahtarını TUTAR, ERROR loglanır ve çağıran
    "elle müdahale gerekir" der. Sessizce yeniden denemek, ayrılmış stoğun
    ikinci kez ayrılması olurdu.
  - **Adımlar okunamıyorsa** karar VERİLMEZ; kayıt olduğu gibi bırakılır. İki
    yanlışın bedeli eşit değil: geç karar müşteriyi bekletir, erken karar
    koşan bir saga'nın anahtarını bırakıp stoğu ikiye katlar.

  `complete_cart` kirası 10 dakika: teorik üst sınır 2dk + 5×30sn = 4,5 dakika
  ve marj bilinçli olarak iki katından fazla.


- **Başarısız bir ödeme sepeti KALICI olarak bozuyordu.** Kartı reddedilen
  müşteri — gerçek bir vitrinde her on ödemenin birinde olan şey — o sepeti bir
  daha ödeyemiyordu. Ölçüldü:

  ```
  1) manual_outcome=decline  -> payment_authorization_declined   (saga telafi etti)
  2) geçerli ödemeyle tekrar -> 409 workflow_execution_failed
     "...daha önce başarısız oldu ve telafi edildi; yeniden denemek için
      YENİ bir anahtar kullanın"
  ```

  Tavsiyenin HTTP yüzeyinde bir karşılığı da yoktu: anahtar sepet kimliğinden
  TÜRETİLİYOR (`complete_cart:<sepet>`), yani müşterinin yeni anahtar
  verebileceği bir alan yok. Sepet içindekilerle birlikte duruyor ama satın
  alınamıyor; müşteri sepeti sıfırdan kurmak zorunda.

  Kusur anlamdaydı: bu motorda `StatusFailed` "başarısız" değil, **"başarısız
  ve telafi EKSİKSİZ tamamlandı"** demek — yani deneme dünyada iz bırakmadı.
  Anahtar da bir izdir. Artık o duruma geçiş anahtarı BIRAKIYOR (kaydı silmeden;
  başarısız deneme denetim kaydı olarak kalıyor) ve aynı sepet tekrar
  ödenebiliyor.

  Sınır iki yandan çizili ve testli: `completed` anahtarı bırakmaz (yoksa aynı
  sepet iki kez tahsil edilirdi), `compensation_failed` de bırakmaz (yoksa elle
  müdahale bekleyen yarım bir işin üstüne yeni deneme binerdi). Bırakma, durum
  yazımıyla AYNI ifadede yapılıyor: iki ayrı yazım arasında düşen bir süreç
  anahtarı sonsuza dek tutulu bırakır, yani düzeltilen arızayı nadir bir yarış
  olarak geri getirirdi.


- **ARAMA TÜRKÇE'DE SESSİZCE ÇALIŞMIYORDU.** `deploy/docker-compose.yml`
  Postgres'i `--locale=C` ile kuruyordu ve C locale yalnızca ASCII harfleri
  katlar. Sonuç: `"çanta"` arayan müşteri, başlığı `"Çanta"` olan ürünü
  BULAMIYORDU. Hata yok, log yok, metrik yok — arama kutusu boş liste dönüyordu.

  Bu bir eklenti sorunu DEĞİLDİ: vitrinin kendi süzgeci
  (`title ILIKE '%' || $q || '%'`) de aynı ayara bağlı, yani hiçbir eklenti
  kurulmamış bir kurulumda da bozuktu. Gerçek sunucuda ölçüldü:

  ```
  GET /store/v1/products?q=çanta   -> 0 sonuç
  GET /store/v1/products?q=Çanta   -> 1 sonuç
  ```

  Düzeltme `--locale=C.UTF-8`. Aynı imajda üç kurulum ölçüldü:

  | initdb | `ILIKE` | `to_tsvector` |
  |---|---|---|
  | `--locale=C` (eskisi) | ✗ | ✗ |
  | `--locale=C.UTF-8` (yenisi) | ✓ | ✓ |
  | `--locale-provider=icu` | ✓ | **✗** |

  ICU'nun yarım kalması önemli: `ILIKE`'ı düzeltip arama indeksini bozuk
  bırakıyor, yani düzeltilmiş gibi görünen bir kurulum üretiyor. C.UTF-8
  sıralamayı da kaybettirmiyor — karşılaştırma yine bayt sırası, değişen
  yalnızca harf katlaması.

- **Açılışta artık bu sınanıyor** (`internal/core/db/casefold.go`). Havuz
  açıldıktan sonra veritabanına iki soru sorulur — `'Ç' ILIKE 'ç'` ve
  `to_tsvector`/`websearch_to_tsquery` eşleşmesi — ve biri bile başarısızsa
  hangi arama yolunun etkilendiğini ve çözümün ne olduğunu söyleyen bir UYARI
  loglanır. Açılış DURDURULMAZ: tamamen ASCII bir katalog C locale'de sorunsuz
  çalışır ve o kurulumları reddetmek yanlış olurdu.

  Locale ADI okunmuyor, DAVRANIŞ sınanıyor: ad bir vekildir ve beklenmedik ama
  doğru bir locale yanlış raporlanırdı. İki yarı da sınanıyor, çünkü ICU
  kurulumunda ayrışıyorlar — yalnızca `ILIKE`'a bakan bir kontrol o kuruluma
  temiz rapor verirdi. Locale initdb ANINDA sabitlendiği için var olan bir veri
  dizini eski ayarıyla kalır; uyarı bunu ve dump/restore gerektiğini söyler.

### Güvenlik

- **Vitrinde bir alışverişçi başkasının SEPETİNİ alabiliyordu.** Idempotency
  kaydı çağıranın kimliğiyle ad alanına alınıyor; ama `/store/v1`'de çözülen
  kimlik alışverişçinin değil MAĞAZANIN kimliği — publishable anahtar her
  tarayıcıda aynı ve zaten gizli değil. Yani bütün müşteriler TEK kova
  paylaşıyor ve kaydı seçen şey istemcinin seçtiği bir başlık.

  Ölçüldü, çıkarsanmadı: iki bağımsız çağıran, `Idempotency-Key: cart-9`,
  aynı gövde → **ikisi de aynı sepet kimliğini** aldı ve ikincinin yanıtında
  `Idempotency-Replayed: true` vardı. Sepette sahiplik denetimi olmadığı için
  (README, "Bilinen sınırlar") bu, yabancıya birinin sepetini vermek demek:
  içindekiler, e-postası, adresi, ve tamamlama yetkisi.

  Vitrin bunu çoğu uçta atlatıyordu, çünkü parmak izi YOLU da içeriyor ve
  sepet kapsamlı uçların yolunda sepet kimliği var — aynı anahtarı kendi
  sepetinde kullanan ikinci müşteri 409 alıyor. Sızıntı tam olarak yolunda
  hiçbir yetenek TAŞIMAYAN ve yanıtında bir yetenek ÜRETEN tek uçtaydı:
  `POST /store/v1/carts`.

  O uç artık idempotency halkasından MUAF. Bedeli açık: zaman aşımına uğrayan
  bir yaratma isteğini tekrarlayan istemci iki sepet açar, biri terk edilir.
  Para, stok ve müşteriye görünen hiçbir şey etkilenmiyor. Muafiyet TAM YOL
  eşleşmesiyle çalıştığı için `/carts/{id}/complete` korunmaya devam ediyor —
  çift SİPARİŞ üreten uç odur.

  Bu davranışı bir e2e testi TERSİNDEN çiviliyordu ("aynı anahtar tek sepet
  üretir") ve iddiası kendi başına makuldü; yanlış olan, kaydın vitrinde
  çağıranları ayırabildiği varsayımıydı. Test yeni sözleşmeyi ve kapattığı
  sızıntıyı yazacak şekilde yeniden yazıldı. Ayrıca e2e kurulumu artık
  üretimin muafiyet listesini KULLANIYOR: eskiden kendi listesini kurduğu için
  üretimdeki satırı silmek hiçbir testi düşürmüyordu.

## [0.6.0] — 2026-09-03

### Eklendi

- **Hata bildirimi: çekirdekte sözleşme, eklentide Sentry**
  ([ADR 0014](docs/adr/0014-error-reporting.md)). `provider.ErrorReporter`
  çekirdekte, `plugins/errorsentry` içinde uygulaması. Besleme **log**tur: her
  arıza zaten ERROR yazıyor, dolayısıyla `logger.Options.Middleware` ile log
  handler'ını sarmak üç kapıyı (WriteError, Recoverer, doğrudan ErrorContext)
  birden kapatır ve arıza üreten koda hiçbir yükümlülük eklemez.

  Zor kısım Sentry'yi bağlamak değil, **neyin asla gönderilmeyeceğine** karar
  vermekti ve o karar çekirdekte duruyor:

  - Raporlayıcı **hatanın kendisini hiç görmez**; olay yalnızca dize taşır.
    Alamadığı şeyi gönderemez.
  - Öznitelikler **izin listesiyle** geçer, elenen anahtarların adları yine
    taşınır. Varsayılanda hiçbir iş kimliği yok.
  - Serbest metinden yalnızca log mesajı ve `errors.Error.Message` çıkar;
    ikisinin de yazılı güvencesi var. Sarılı zincir süreçte kalır.
  - Gruplama anahtarı hata KODUDUR, yığın izi değil.
  - Kod başına dakikada üç rapor; bastırılan sayı bir sonrakiyle taşınır.
  - Log önce yazılır; panikleyen raporlayıcı süreç ömrü boyunca kapatılır;
    gönderim hatası raporlama eşiğinin ALTINDA loglanır — üstünde loglamak
    toplayıcı kesintisini kendi kendini büyüten bir döngüye çevirirdi.

- **Erişim logunun 5xx satırı artık "zaten raporlandı" diye işaretleniyor.**
  Bu kusuru gerçek bir toplayıcıya karşı koşarken bulduk ve hiçbir birim testi
  gösteremezdi: bir 5xx İKİ kez loglanıyor — biri kodu taşıyan teşhis satırı,
  öteki kod taşımayan erişim özeti — ve ikisi de ERROR. İkisini birden
  bildirmek hacmi ikiye katlıyor, dahası uygulamadaki her sunucu hatasını
  `unclassified` kovasına dolduruyordu; o kovanın, gerçekten sınıflandırılmamış
  bir arıza içinde görünebilsin diye boş kalması gerekir. Üstelik o kovanın
  hız bütçesini de harcıyordu.

- **Panelde fiyat ve stok düzenleme** ([ADR 0013](docs/adr/0013-panel-write-surface.md)
  eki). Varyant sayfası bir varyantın para birimi başına taban fiyatını ve her
  lokasyondaki fiziksel stoğunu düzenletiyor; `pricing.admin` ve
  `inventory.admin` yüzeyleri bunun için eklendi.

  Fiyat yüzeyi bir **kayıpsız oku-değiştir-yaz**. Modülün tek fiyat yazıcısı
  YIKICI: `SetPrices` kümenin fiyatlarını değiştirmiyor, DEĞİŞTİRİYOR — girdide
  olmayan her fiyatı siliyor. Panel ise fiyatları sorgu sağlayıcısından
  okuyor ve o sağlayıcı kural taşıyan ve liste üzerindeki fiyatları
  FİLTRELİYOR. İkisi birleşince, taban fiyatı düzenleyen naif bir form
  kümedeki her kampanya fiyatını sessizce silerdi — operatör onları hiç
  görmediği için de fark edilmezdi. Yüzey bu yüzden TÜM fiyatları okuyup
  yalnızca birini değiştiriyor ve geri kalanını olduğu gibi geri yazıyor.
  Bedeli yazılı: yazma fiyat kimliklerini yeniden üretiyor, ki bu kimlikler
  yalnızca pricing'in kendi `price_rule` satırlarınca anılıyor.

  Stok yüzeyi bir de OKUMA taşıyor, ki diğer ikisi taşımıyor. Sebep tercih
  değil boşluk: sorgu sağlayıcısı kalem başına TEK bir toplam veriyor ve
  toplamla stok düzenlenemez — operatörün hangi deponun ne tuttuğunu bilmesi
  gerek. Kırılım sorgu katmanına eklenmedi, çünkü orada kitle vitrini de
  içeriyor; rezerve adetler ve iç depo adları oraya ait değil.

  Boş lokasyonlar da listeleniyor: yalnızca seviyesi olanları gösteren bir
  form yeni bir depoyu HİÇ stoklayamazdı, çünkü depo ancak stoğu olduğunda
  görünürdü — yani operatörün ulaşmaya çalıştığı durumda. Rezerve adet de
  yazılıyor, çünkü servisin "söz verilmiş stoğun altına inemezsin" reddi
  aksi hâlde keyfî görünürdü.

  Para hesabı baştan sona TAMSAYI. Operatörün yazdığı metin ondalık kısmı
  ÖTELENEREK (ölçeklenerek değil) minor birime çevriliyor — iki haneli bir
  para biriminde "1.5" 150'dir, 15 değil — ve para biriminin hane sayısından
  fazla ondalık YUVARLANMIYOR, reddediliyor: yuvarlamak operatörün yazdığı
  fiyatı sessizce değiştirmek olurdu. Ölçek bilinmiyorsa kutu ham minor
  birim alıyor ve form bunu SÖYLÜYOR; söylemeyen bir kutuya "199.90" yazan
  operatör kastettiğinin yüzde birini kaydederdi.

- **Panel artık YAZIYOR: ürün başlığı, handle ve durumu düzenlenebiliyor**
  ([ADR 0013](docs/adr/0013-panel-write-surface.md)). ADR 0011'in "panel okuma
  yollarını kullanır" kararı bilinçli olarak açıldı ve yerine ne konduğu
  yazıldı.

  Okuma katmanı GENERİKTİ; yazmanın karşılığı yok. Product servisinin metodu
  modülün kendi tiplerini taşıyor (`UpdateProductInput`, `models.Status`) ve
  panel onları adlandıramaz — adlandırdığı an KENDİ paketinde tanımlı BAŞKA bir
  tip olurlar. Bu yüzden modül ilkel-tipli dar bir **yönetim yazma yüzeyi**
  yayımlıyor ve container'a `product.admin` adıyla, interop'tan AYRI
  kaydediliyor.

  Ayrım bir dosyalama tercihi değil: interop'un godoc'u dar kalmaya söz veriyor
  ve kitlesini sayıyor (başka modüller, akışlar, eklentiler). Oraya bir yazma
  metodu eklemek, bir düzenleme formunun yan etkisi olarak HER EKLENTİYE
  katalogu yeniden yazma yetkisi verirdi. `TestAdminSurfaceHasOneAudience` adı
  gerçek kılıyor: `.admin` ile biten bir adı, sahibi modül ve panel dışında
  hiçbir üretim dosyası anamaz.

  Yazma SERVİSTEN geçiyor, depodan değil: handle tekilliği ve
  `product.updated` olayı orada. Sessiz olan yarısı (olay) ayrıca iddia
  ediliyor — gürültülü olan (handle çakışması) yoksa ikisinin de kanıtı
  sayılırdı.

  Yüzey KOŞULLU çözülüyor: product modülü kurulu olmayan bir kurulumda panel
  yine açılıyor ve düzenleme formu sebebini söyleyen bir 503 dönüyor.

- **Panelin beklenmeyen arızada tarayıcıya JSON zarfı yazması düzeltildi.**
  Kusur giriş yolunda ADR 0011'den beri vardı: `corehttp.WriteError` çerçevenin
  JSON zarfını yazıyor, bu bir API istemcisi için doğru ama bu yola TARAYICI
  gelmiş oluyor. Üstelik zarf, Internal olmayan sınıfların mesajını olduğu gibi
  geçiriyor — o söz API istemcileri için verilmişti; panel sayfasını okuyan
  operatör sızmış bir bağlantı dizesini teşhisten ayıramaz. Artık panelin kendi
  hata sayfası dönüyor, gerçek sebep loga gidiyor.

- **Panelin katalog ekranları geldi: ürün listesi ve ürün sayfası.** Ürün
  sayfası varyantları, fiyatlarını ve stoklarını gösteriyor — üçü üç ayrı
  modülden, hiçbiri panel tarafından import edilmeden. Okuma katmanına herkes
  gibi ADLA ulaşılıyor (ADR 0004) ve fiyat ile stok TEK çağrıda genişletme
  olarak geliyor; satır başına sorgu yok.

  Panel bu adları ELLE yazmak zorunda (modülleri import edemez) ve ayrışmaları
  SESSİZDİR: link adı değiştiği gün panel derlenir, 200 döner ve yalnızca fiyat
  sütunu boşalır. `TestPanelKatalogAdlariUyusuyor` bu bağı derleme zamanına
  taşıyor — `TestSaglayiciKayitAdlariUyusuyor` ile aynı gerekçe, aynı yer.
  Süzgeç ve alan adlarının çoğu sahibi modülde dışa açık olmadığı için
  pinlenemiyor; onların koruması okuma katmanının "tanımadığım alan" reddi ve
  bunun panelde 500'e çevrilmesi.

  **Fiyat ASLA tahmin edilmiyor.** Tutar minor unit tam sayısıdır ve okunur
  hâle getirmek para biriminin ondalık basamak sayısını gerektirir; ISO 4217'de
  bu sayı 0 (JPY), 2 (çoğunluk) ve 3 (KWD) olabilir. Ölçek bölge kaydından
  okunur; okunamazsa ham tam sayı gösterilir ve "minor units" diye
  ETİKETLENİR. Sabit 100 varsaymak iki sınıfta yanlış tutarı KENDİNDEN EMİN
  gösterirdi. Aritmetik baştan sona tam sayıda kalır (plan Bölüm 8: float
  ASLA).

  Stoğu olmayan varyant `—` gösteriyor, `0` DEĞİL: sıfır "tükendi" demektir,
  hiç takip edilmemek başka bir olgudur.

- **Yönetim paneli iskeleti: dördüncü ağaç `internal/adminui`**
  ([ADR 0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md)). Panel `/admin/ui`
  altında yaşar, sunucu tarafında HTML üretir (`html/template`, ikiliye gömülü)
  ve modülleri İMPORT ETMEZ — çerçevenin okuma yollarını container'dan adla
  çözer. Bu turda giriş, çıkış ve korumalı bir giriş noktası var; katalog
  ekranları bir sonraki turda.

- **Panelin kimliği bir çerezle taşınır ve çerez YALNIZCA panel ağacında
  geçerlidir.** `Path` panel önekine sabitlenmiştir; `HttpOnly`, `SameSite=Strict`
  ve paylaşılan ortamlarda `Secure`. Bunun sebebi savunma değil KORUMA:
  yönetim API'sinin bugünkü CSRF bağışıklığı, jetonun tarayıcının KENDİLİĞİNDEN
  eklemediği bir başlıkta yaşamasından gelir. Çerez `/admin/v1`'e de gitseydi o
  bağışıklık kaybolur ve her yönetim ucu yeni bir saldırı yüzeyine girerdi.
  CSRF'in ikinci katmanı `Origin` denetimidir (`adminui.UI.CheckOrigin`).

- **`corehttp.WriteHTML`, `corehttp.WriteRedirect` ve `corehttp.WriteAsset`.**
  Panel gövdesini kendi yazmaz: HTML de çekirdeğin yazıcısından geçer, böylece
  hata yolu değişmezi (gövde yalnızca çekirdeğin yazıcılarından yazılır)
  panelde de geçerli kalır. Sayfa önce TAMPONA üretilir; ortada oluşan bir hata
  yarım gövde + 200 yerine 500 döner.

- **Panelin koruma halkası bileşim kökünde takılır** (`adminui.Ring`).
  Middleware router kurulurken takılmak zorundadır, panel ise container'dan
  modül önyüklemesi SIRASINDA doğar; halka bu boşluğu köprüler ve bağlanmadan
  önce gelen isteği REDDEDER — korumasız bir yönetim yüzeyi sessizce açık
  kalmaktansa gürültüyle kapalı kalır (ADR 0007'nin kimlik hattı).

- **Deponun çalışma dili İngilizce oldu ve geçiş bir DEFTERE bağlandı**
  ([ADR 0012](docs/adr/0012-repository-language-and-solid.md)).
  `internal/arch/testdata/turkish_ledger.txt` hâlâ Türkçe içeren her dosyayı,
  `internal/arch/testdata/turkish_paths.txt` ise Türkçe ADI olan her yolu adıyla
  sayar; defterde olmayan bir dosya Türkçe içeremez. Defterler yalnızca
  KÜÇÜLÜR: bir satırı silmek dosyanın gerçekten çevrilmiş olmasını gerektirir.
  Başlangıç borcu 784 dosya + 41 yol.

  Dedektör ÜÇ ŞERİTLİDİR ve bunun sebebi ölçüldü: bütün ağacı harf çevirisine
  sokmak yalnızca diyakritiğe bakan bir kuralı 724 dosyadan 0'a düşürüyor —
  yani tek bir komutla "çeviri bitti" dedirtiyor. İkinci şerit, harf
  çevirisinden SAĞ ÇIKAN Türkçe işlev sözcüklerini yorum ve dize
  değişmezlerinde arar (liste Go standart kütüphanesinin 7711 dosyasına karşı
  ölçüldü, yalnızca sıfır isabet verenler alındı); üçüncüsü Türkçe kökleri
  tanımlayıcıların TAM parçalarında arar.

- Dil dedektöründeki fixture, paket düzeyindeki muafiyet haritasını mutasyona
  uğratıyordu ve aynı haritayı paralel koşan başka bir test okuyordu; `-race`
  altında veri yarışı. Harita artık `scanSource`'a PARAMETRE olarak geçiyor:
  paylaşılan durum kilitlenmedi, kaldırıldı.

- **Smoke testlerinin beklediği log mesajları artık üretime bağlı**
  (`TestSmokeLogAssertionsMatchProduction`). Smoke testi bir üretim log
  satırını METİN olarak bekliyor ve ikisi arasında derleyici bağı YOK: mesajı
  yeniden adlandırmak smoke testini derlenir, vet'lenir ve lint'lenir hâlde
  bırakıyor, üstelik `go test ./...` onu koşmuyor bile — smoke bir build
  etiketinin arkasında. Kırılma push'tan SONRA, CI'ın en yavaş işinde
  görünüyor.

  Bu varsayımsal değil: observability paketindeki `"izleme kuruldu"` mesajını
  çevirmek tam olarak bu çifti kırdı ve bütün yerel kapılar yeşil kaldı.
  Denetim, mesajın üretimde hâlâ YAZILDIĞINI kaynağa bakarak doğruluyor;
  mesajı dışa açık bir sabite taşımak, operatöre giden bir metni paketin API
  yüzeyine koymak olurdu.

- **SOLID'in mekanik olarak ölçülebilen iki boşluğu kapandı**
  (`internal/arch/solid_test.go`). `TestResolvedTypeIsAnInterface` DIP'in
  TÜKETİM yarısını zorluyor: üretimdeki her `container.Resolve[T]` çağrı yeri
  bir ARAYÜZ çözmek zorunda. depguard yalnızca modüller arası import'u yasaklar;
  çağıranın KENDİ modülünden ya da çekirdekten gelen somut bir tipe hiçbir şey
  demiyordu. Ölçüm: 18 arayüz, 5 jenerik yardımcı ve tam bir somut aile —
  `core.db` adıyla 16 kez çözülen `*db.Pool`, gerekçesiyle yazılı.
  `TestLayerPurity` ise modül İÇİNDEKİ katman sınırını zorluyor: `api` pgx'i,
  kendi `repository`'sini ve üretilmiş sqlc kodunu; `service` ise `net/http`,
  chi ve pgx'i import edemez. Ölçüm: 15 modül, 30 dizin, 0 ihlal.

  İki testin de mutasyonla bulunmuş bir kusuru var artık kapalı: taranan dizin
  sayacı, denetlediği kural listesinin KENDİSİNDEN besleniyordu — katman adını
  kuralda değiştirmek sıfır dizin buluyor ve her modül tek bir import
  okunmadan geçiyordu. Sayaç artık DİSKE karşı doğrulanıyor.

- **Dedektörün kendi körlüğüne karşı denetimler.** `TestDetectorIsNotBlind`
  her şeridin ayrı sayacını ve taranan her kökü pozitif tutar; taranacak
  köklerin listesi DİSKE karşı doğrulanır, çünkü listeyi kendi içinden okuyan
  bir sayaç, listeden bir ağaç düştüğünde onunla birlikte susar (mutasyonla
  görüldü). `TestDetectorFindsPlantedTurkish` her şeride bilinen bir örnek
  ekiller, `TestDetectorPassesEnglishSource` ise doğru İngilizceyi yanlışlıkla
  suçlamadığını kanıtlar — `module`, `rollback`, `reason` ve Go'nun `x, ok`
  deyiminden doğan `yok` değişkeni dâhil.

- **SOLID kuralı ölçüme bağlandı.** ADR 0012 beş prensibin bugünkü durumunu
  tabloya döküyor: DIP ve OCP zorlanıyor, ISP modül sınırlarında YAPISAL olarak
  sağlanıyor, SRP yalnızca makro düzeyde, LSP için hiçbir denetim yok. Son ikisi
  için "denetim yoktur" AÇIKÇA yazıldı; boyut linter'ları kapalı kalıyor çünkü
  53 metotlu bir arayüzü eşiğe göre altıya bölmek tasarımı değil sayacı
  memnun eder.

### Değiştirildi

- Kablolama değişmezi (`TestPanelBilesimKokundeKurulu`) ve modül-izolasyonu
  denetimi (`TestPanelModulleriImportEtmez`) dördüncü ağacı da kapsıyor. Önek
  eşlemesi ağacın KÖKÜNÜ de kabul edecek şekilde düzeltildi: eskiden yalnızca
  alt paketleri görüyordu, yani kökte kurulan bir paket denetimin dışında
  kalırdı.
- Gövde yazımı taraması artık `tmpl.Execute(w, …)` biçimindeki şablon
  akıtmalarını da yakalıyor. Tarama alıcının import adına baktığı için şablon
  yazıcısına KÖRDÜ ve panel bu kör noktadan geçebilirdi.
- **Panel çerezinin `/admin/v1`'de KABUL EDİLMEDİĞİ artık bir değişmez**
  (`TestPanelCookieIsNotAcceptedByTheAdminAPI`). ADR 0011'in taşıyıcı iddiası
  buydu ve bugüne kadar hiçbir test onu tutmuyordu: yönetim API'sinin CSRF
  bağışıklığı bir savunmadan değil, jetonun tarayıcının KENDİLİĞİNDEN
  eklemediği bir başlıkta yaşamasından geliyor. İddia GERÇEK koruma yığınında
  sınanıyor, elle kurulmuş bir zincirde değil — çünkü kanıtlanan şey KAPSAMIN
  bir özelliği.

  Test yazılırken dört mutasyon sağ kaldı ve dördü de testteki gerçek
  boşluklardı: çerezle panelin açılması, halka hiç takılı değilken de
  geçiyordu (panel öneki kotalar için zaten açık); köken halkasının TAKILI
  olduğunu hiçbir şey kanıtlamıyordu; giriş yolunun kimlik muafiyetini
  kaldırmak hiçbir testi düşürmüyordu — oysa bedeli "kimse giriş yapamaz"dır
  ve arıza bir hataya bile benzemez, giriş sayfası 401'le geri gelir.

- **`corehttp.SchemeBearer`.** Çekirdek, `Authorization` başlığından okuduğu
  şemayı KÜÇÜK HARFE indirip doğrulayıcıya öyle veriyor; panel ise jetonu
  çerezde taşıdığı için başlıktan hiç geçmiyor ve şemayı elle yazıyordu. İki
  yazım bugün yalnızca auth modülünün büyük/küçük harf duyarsız
  karşılaştırması sayesinde çalışıyordu. Sözleşme artık `Authenticator`
  arayüzünde yazılı ve iki taraf da aynı sabiti kullanıyor.

- `internal/core/http/auth.go` İngilizceye çevrildi. Kimlik doğrulama
  yanıtlarının mesajları değişti (`"authentication is required"`); kodlar
  (`unauthenticated`, `forbidden`) değişmedi.

- **Çekirdeğin sekiz paketi İngilizceye çevrildi** (ADR 0012): `internal/core/errors`,
  `internal/core/container`, `internal/core/module`, `internal/core/provider`,
  `internal/core/logger`, `internal/core/db` (migration testdata'sı dâhil),
  `internal/core/observability`, `internal/core/plugin`, ve `internal/core/http`
  içinde `response.go`, `auth.go`, `router.go`, `server.go`, `middleware.go`,
  ve `internal/core/query`'nin üretim dosyaları.

  Okuma katmanının hata AYRINTI anahtarları da çevrildi
  (`"aranan_ad"` → `"looked_up_name"`, `"alan"` → `"field"`). Bunlar hata
  KODU değildir; kod sözleşmedir ve değişmedi. Ayrıntılar teşhis içindir ve
  deponun dilinde yazılır. Davranış değişmedi. Değişen KULLANICIYA/OPERATÖRE
  giden metinlerdir: container'ın teşhis mesajları (`"missing: Reserve(...)"`,
  `"...have pointer receivers"`), modül kaydı hataları ve log anahtarları
  (`"servis"` → `"service"`, `"tembel"` → `"lazy"`). `Kind.String()` çıktıları
  (`not_found`, `invalid`, …) SÖZLEŞMEDİR ve değişmedi; godoc'a bu açıkça
  yazıldı.

- **Bileşim kökü ve çekirdeğin yanıt yazıcısı İngilizceye çevrildi**
  ([ADR 0012](docs/adr/0012-repository-language-and-solid.md)). `cmd/server`
  içinde `kurulum.go` → `setup.go`, `kurulum_test.go` → `setup_test.go`,
  `belge_test.go` → `docs_test.go`; `internal/core/http` içinde `response.go`
  ve testi. Davranış değişmedi, ama açılış LOG MESAJLARI
  ve kullanıcıya dönen genel iç hata mesajı artık İngilizce
  (`"an unexpected server error occurred"`). Hata KODLARI değişmedi ve
  değişmeyecek: kod makine sözleşmesidir, mesaj insan içindir.

  Yeniden adlandırmalar sırasında kayıt denetimi gerçek bir tuzağı yakaladı:
  eklenti kaydının yerel değişkenine `registry` demek, denetimin alıcıyı ADIYLA
  tanıması yüzünden o satırı modül kaydı gibi gösteriyordu.

  İçerik defteri 784 → 777, yol defteri 41 → 38.

- ADR seçenek bölümü başlıklarını tanıyan liste İKİ DİLLİ oldu
  (`internal/arch/belge_atiflari_test.go`). Yalnızca Türkçe başlık tanıyan
  kural, İngilizce yazılmış bir ADR'nin REDDEDİLMİŞ seçeneklerini bugünkü depo
  hakkında iddia sanar ve var olmayan sembolleri kırık bildirirdi.

## [0.5.0] — 2026-09-02

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

  Yeni hata yüzeyi ÜÇ ayrı `404` taşır ve üçü ayrı durumdur: geçerli ama hiçbir
  bölgeye bağlı olmayan ülke `country_has_no_region`, referans tablosunda hiç
  bulunmayan ülke kodu `country_not_found`, bağlı olduğu bölge silinmiş ülke
  ise `country_region_missing`. Biçimi bozuk ya da boş bir kod `422`'dir.
  Ayrıca sepet açma yolundan `cart_region_unavailable` (500) kodu DÜŞTÜ —
  bölge yüzeyi handler'a artık hiç bağlanmıyor — ve yerine sepet açılıp
  okunamadığında `cart_missing_after_create` geldi; ikisi de operatör kodudur,
  istemci onlara göre dallanmaz. Ayrım korunur çünkü ikisi farklı düzeltmeler ister: birinde
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

- **`fulfillment.interop`'un `SelectLocation` metodu KALDIRILDI; yerine
  `RankLocations` geldi.** Gömülü kodu ve kendi kargo yüzeyini yazan tüketiciyi
  etkiler. VAR OLAN uçların yolları ile istek/yanıt şemaları değişmedi; hata
  kodu için bir alttaki maddeye, yeni yönetim uçları için "Eklendi" bölümüne
  bakın.

  ```go
  // önce
  SelectLocation(ctx context.Context, candidateLocationIDs []string) (string, error)
  // sonra
  RankLocations(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)
  ```

  İki değişiklik var ve ikisinin de ayrı gerekçesi var.

  **Bölge parametresi** yazılı bir taahhüdü kırıyor: eski godoc "politika bu
  metodun İÇİNDE zenginleşir; çağıranın gördüğü imza değişmez" diyordu. Taahhüt
  yanlıştı ve nerede yanlış olduğu somut: eksik olan yalnızca deponun kendisi
  değil, gönderinin NEREYE gittiğiydi ve ikincisi modülün içinde zenginleşmeyle
  elde edilemez. Bölge çağıranın elindedir — sepet akışının planı zaten taşıyor.

  **Sıra dönmesi** bir maliyet kararıdır ve karşılaştırma KARŞI-OLGUSALDIR:
  v0.4.0'ın seçimi saf bir fonksiyondu, veritabanına hiç dokunmuyordu. Politika
  eski yüzeye (tek lokasyon dönen `SelectLocation`) eklenseydi, çağıran tükenen
  her depodan sonra yeniden sormak zorunda kalacaktı — N adaylı bir satır için
  bir sorgu yerine N sorgu; üstelik sıra deterministik olduğu için o N-1 çağrı
  aynı sıralamayı yeniden hesaplayacaktı. Yan kazanç ölçülebilir: sepet akışının aday döngüsünün
  sonlanması artık modülün ne döndüğünden bağımsızdır — eskiden seçilen adayın
  listeden düşürülebilmesine bağlıydı, şimdi sonlu bir dilimin uzunluğuyla
  sınırlıdır.

  Derleyicinin denetlemediği tek dikiş, arayüzün container'dan **adla**
  çözüldüğü yerdir; kanıtı `internal/e2e` altındaki uçtan uca senaryodur.

- **Stok ayırma adımı artık ALT HATANIN KODUNU koruyor.** Vitrin istemcisinin
  gövdede gördüğü `error.code`, tamamlama sırasında stok ayrılamadığında
  değişti:

  | Durum | Önce | Sonra |
  |---|---|---|
  | Hiçbir depoda aday yok | `checkout_workflow_reservation_failed` | değişmedi |
  | Seçilen depolar tükendi | `checkout_workflow_reservation_failed` | `inventory_insufficient_stock` |
  | Hiçbir aday sepetin bölgesine hizmet etmiyor | — | `fulfillment_no_serviceable_location` |

  Durum kodu üçünde de `409` kalır. Değişikliğin sebebi bu turun kendi
  ihtiyacıdır ve bu özelliğin ÖN KOŞULUDUR: taşıma katmanı gövdeye tek bir
  makine okunur alan yazar ve kod ezildiği sürece yanlış kurulmuş bir bölge
  bağı, dolu raflarla "stok ayrılamadı" diye raporlanırdı — operatör bakması
  gereken yeri bulamazdı. Kalıp yeni değil: motor aynı hatayı bir tur önce
  kendi sarmalamasında düzeltmişti ve gerekçesi orada B2B harcama limitiyle
  ölçülmüş hâlde yazılı.

  Koda göre dallanan istemciyi etkiler. `checkout_workflow_reservation_failed`
  artık adım hatasının SARMALAMASINDA yedektir: alt hata kendi kodunu taşıyorsa
  o korunur. Kod kaybolmuş DEĞİLDİR — adımın KENDİ ürettiği hatalarda görünmeye
  devam eder: hiçbir depoda aday bulunmadığında (yukarıdaki tablonun ilk satırı)
  ve kargo modülü sözleşmeyi çiğnediğinde (boş sıra, aday olmayan kimlik,
  yinelenen aday — üçü de `500`).

- **Satış kanalı kapsamı artık YAZMA yolunda da uygulanıyor.** Kanal ataması
  KULLANAN kurulumlarda `POST /store/v1/carts/{id}/line-items`, yabancı kanalın
  varyantı için `201` yerine `404` döner. Ayrıntı ve gerekçe aşağıda, Güvenlik
  başlığında; madde buraya da konuldu çünkü yükseltme öncesi yalnızca bu bölümü
  tarayan entegratör aksi hâlde görmezdi.

### Eklendi

- **Yönetim paneli başladı: yazma kapısı, iskelet ve denetimin kapsamı.**
  Panel `internal/adminui` altında, `internal/workflows`'un kardeşi olarak
  dördüncü bir ağaçta yaşıyor ve sunucu tarafında üretilen HTML'i ikiliye
  gömülü şablonlardan üretiyor. Karar ve reddedilen seçenekler
  [ADR 0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md)'de. Bu turda gelen
  yalnızca iskelettir: oturum, koruma halkası ve katalog ekranları sonraki
  turlarda.

  Çekirdeğe üç yazıcı eklendi — HTML, yönlendirme ve statik varlık. HTML
  yazıcısı gövdeyi **önce belleğe** üretmeyi şart koşuyor: doğrudan yazıcıya
  akıtılan bir şablonda ortada doğan hata, `200` durum kodlu YARIM bir sayfa
  bırakır ve başlık gönderildikten sonra ne panik yakalayıcı ne hata yazıcısı
  bir şey yapabilir. JSON yazıcısının aksine 2xx zorunluluğu YOKTUR ve bu
  bilinçli: kimliksiz bir tarayıcıya giriş sayfasını `401` ile döndürmek, onu
  başka bir yere yollamaktan daha dürüsttür.

  **İki kör nokta, açıldıkları turda kapatıldı** — ikisi de ölçüldü:

  - Kayıt denetimleri kapsamlarını modül ağacına indiriyordu; panel ağacında
    "yazılmış ama hiçbir yere bağlanmamış" bir yetenek arch koşusunu YEŞİL
    bırakırdı. Uydurmaya gerek olmadı: aynı boşluk `internal/workflows` için
    zaten kapatılmıştı ve kalıbı hazırdı. Denetim ayrıca kökte yaşayan
    paketleri de görecek şekilde düzeltildi — önek eşleşmesi yalnızca alt
    paketleri kapsıyordu.
  - "Gövde tek yerden yazılır" değişmezinin modül dışı kolu şablon yazımını
    GÖRMÜYORDU: çağrının alıcısı bir paket adı olmadığı için hedef çözülemiyor
    ve çağrı sessizce geçiyordu. Bu bir izin değil, taramanın ölçme biçiminin
    negatifiydi — kural kalkmıyor, körleşiyordu. Tarama artık şablonun yazıcıya
    akıtılmasını yakalıyor.

  Şablonlar AÇILIŞTA ayrıştırılıyor ve adları iki yönlü çiviliyor: beklenen bir
  ad ayrıştırılmamışsa da, ayrıştırılan bir şablon hiçbir yerde çağrılmıyorsa da
  açılış durur. Şablon adı bir dizedir; yazım hatası derlenir, lint görmez ve
  yalnızca o sayfa açıldığında patlar.

  Beş mutasyonla doğrulandı: panelin kablolaması, modül import yasağı, şablonun
  yazıcıya akıtılması ve şablon adı denetiminin her iki yönü.

- **Depo seçimi artık bir POLİTİKA taşıyor.** Sınır bu turda YAZIYA GEÇTİ ve
  aynı yayımlanmamış pencerede kapandı; hiçbir yayımlanmış sürümün bilinen
  sınırlarında durmadı. Kaydın değeri, kuralın v0.2.0'dan beri sessizce
  "kimliği en küçük aday" olmasıdır. Yazıya geçtiğinde şöyle duruyordu:
  *"Depo seçimi bir POLİTİKA taşımaz … yakınlık, maliyet ve stok dağılımı
  İFADE EDİLEMEZ, çünkü modülün bir lokasyon modeli yoktur."*

  Lokasyon modeli kargo modülünün **kendi** şemasına geldi (iki tablo) ve depo
  kimliği opak, FK'sız bir yabancı kimlik olarak duruyor — `region_id`'nin
  bugüne kadar durduğu gibi. Modül ad ya da adres KOPYALAMIYOR: deponun nerede
  olduğu stok modülünün verisidir ve orada kalıyor.

  Kural üç adımdır — **ele** (bir depoya bağlanmış bölgeler varsa ve hedef
  onların arasında değilse aday düşer), **sırala** (`priority`, küçük olan öne),
  **eşitliği boz** (kimliği küçük olan öne). Yönetim yüzeyi
  `PUT/GET/DELETE /admin/v1/shipping-locations/{location_id}` ve
  `GET /admin/v1/shipping-locations`.

  **Geriye uyumluluk SEÇİLEN DEPO için tamdır ve testlidir:** politika kaydı
  yokken eleme ve sıralama boşa düşer, geriye eşitliği bozan kural kalır ve
  seçilen depo bu turdan öncekiyle aynıdır.

  Kayıtsız kurulumda da değişen iki şey var ve ikisi de burada yazılı olmalı:
  hata KODU değişti (yukarıdaki kırıcı değişikliğe bakın; depo BİLDİREN
  çağrıları da etkiler, çünkü o yol politikaya hiç girmese de aynı sarmalamadan
  geçer) ve seçim artık satır başına BİR SQL SORGUSU yapıyor — eski seçim saf
  bir fonksiyondu ve veritabanına hiç dokunmuyordu, yani bu yolda yeni bir
  arıza ihtimali doğdu.

  Gerçek yığında ölçüldü: `internal/e2e/coklu_depo_test.go`, gerçek Postgres ve
  gerçek modüllerle iki yeterli depo kurar, politikayı yazar ve rezervasyonun
  hangi depoda açıldığını okur. Mutasyonla doğrulandı.

  Politikanın İFADE ETMEDİKLERİ de yazıya geçti — stok dağılımı, maliyet,
  sipariş düzeyinde karar ve (depo, bölge) çifti başına tercih — ve her birinin
  neden edilemediği [ADR 0010](docs/adr/0010-depo-secim-politikasi.md)'da.

  Kabul edilen üç bedel README'nin bilinen sınırlarına GİRDİ ve en ağırı şudur:
  var olmayan bir bölge kimliği bağlamak (ya da bir bölgeyi silip aynı adla
  yeniden açmak — yeni kayıt yeni kimlik alır) o depoyu her sepette eler ve tek
  depolu bir kurulumda mağazayı kapatır; düşen sepet de bir daha tamamlanamaz,
  çünkü tamamlama akışının idempotency anahtarı sepet kimliğinden türer.

  Bedel kaldırılmadı, GÖRÜNÜR yapıldı — ama görünürlüğün sınırı da yazılı
  olmalı: vitrin gövdesine yalnızca KOD ulaşır
  (`fulfillment_no_serviceable_location`); gövdedeki mesaj her üç ayırma
  arızasında da aynıdır, çünkü taşıma katmanı en dıştaki mesajı yazar.
  Adayların gerçekte hangi bölgelere bağlı olduğunu yazan döküm SUNUCU LOGUNDA
  ve `workflow_executions` kaydındadır. Yani kod istemciye, döküm operatöre
  gider.

  Bölge bağının bir **kısıt** olduğu, tercih için `priority` kullanıldığı ayrımı
  da bilinçlidir: "hizmet ettiği bölgeler" taşıyıcının kapsama alanıdır ve
  kapsam dışına göndermek graceful bir geri düşüş değil, imkânsız bir gönderidir.
  Bağı sıralama anahtarına çevirip katı kesiği bir bayrağın arkasına almak
  değerlendirildi ve reddedildi; gerekçe ADR'de.

- **Kurulum tuzağı artık gerçek süreçte çivili:**
  `internal/smoke/anahtar_test.go` içindeki
  `TestKanalsizPublishableAnahtarVitrindeReddedilir`, README'nin publishable
  anahtar paragrafını uçtan uca yürür — kanalsız anahtar üretilir (`201`),
  mağaza yüzeyinde `401` alır, teşhis kodu (`auth_no_sales_channel`) yanıtta
  değil sunucunun LOGUNDA aranır ve kanal sonradan bağlanınca AYNI anahtar
  girer. Bu yol depoda hiçbir zeminde koşmuyordu: hiçbir test o kodu
  beklemiyordu ve `internal/smoke`'un kendi yardımcıları anahtarı her zaman
  bir kanala bağlı üretiyordu. Mutasyonla doğrulandı — kanalsız anahtarı kabul
  eden bir sunucuda senaryo `401` beklerken `200` görüp düşüyor.

- `product` modülünün varyant Query sağlayıcısı yeni bir süzgeç tanıyor:
  `sales_channel_ids`. Yalnızca `id` ya da `ids` ile BİRLİKTE kullanılabilir;
  tek başına verilirse istek `422` alır — kanal süzgeci bir yetkilendirme
  daraltmasıdır, kendi başına bir listeleme ölçütü değil. Sepet akışının kanal
  kapsamını yazma yolunda uygulaması buna dayanır. HTTP yüzeyine açık DEĞİLDİR.

### Değişti

- `POST /store/v1/carts` gövdesindeki `metadata` **kaldı** ve akışa olduğu gibi
  taşınıyor. Karar satır metadata'sında verilenin aynısıdır: alan gerçekten
  istemcinin bilgisidir (kampanya kaynağı, vitrin oturumu), hiçbir hesaba
  girmez ve türetilecek bir karşılığı yoktur. Düşürülseydi, sepeti açan tek yol
  artık akış olduğu için istemcinin gönderdiği alan sessizce kaybolurdu.

### Düzeltildi

- **`.env`, komut satırından verilen ortam değişkenlerini SESSİZCE eziyordu.**
  `Makefile`'ın `.env` yükleyicisi dosyayı çağıranın ortamının ÜSTÜNE
  uyguluyordu; `.env.example`'daki boş `PLUGINS=`,
  `OTEL_EXPORTER_OTLP_ENDPOINT=` ve `ADMIN_BOOTSTRAP_EMAIL=` satırları,
  README'nin `DEĞİŞKEN=… make run` biçimindeki her örneğini etkisiz bırakıyordu
  — hata vermeden. Ölçüldü (aynı Makefile, aynı `.env`, tek fark yükleyici):
  düzeltme öncesi `PLUGINS=search-pg … make` → `PLUGINS=[]`,
  `OTEL_EXPORTER_OTLP_ENDPOINT=[]`; sonrasında ikisi de komut satırındaki
  değeri taşıyor ve `.env` hâlâ okunuyor (`LOG_FORMAT=text` geliyor).
  Öncelik docker compose'unkiyle aynı yöne çevrildi: **ortam > `.env`**.
  Yöntem ayrıştırmaz — çağıranın ortamı `export -p` ile saklanır, `.env`
  kabukla yüklenir, saklanan ortam geri uygulanır.

- **`make openapi-client` çalışma ağacına root'a ait dosyalar yazıyordu** ve
  ardından `make clean` "Permission denied" ile düşüyordu; geliştirici kendi
  deposunu temizlemek için `sudo`'ya muhtaç kalıyordu. Üreteç konteynerine
  `--user` verildi. Mekanizma ölçüldü: `--user` olmadan konteyner `uid 0` ile
  yazıyor ve `rm -rf` çıkış kodu 1 veriyor; `--user` ile dosyaların sahibi
  çağıran oluyor ve aynı `rm -rf` 0 dönüyor.

- README'nin modül izolasyonu güvencesi BAYATTI: "12 modül × 11 yasak"
  yazıyordu, oysa `.golangci.yml` bugün 15 modülün her biri için 14 yasak
  taşıyor (sayıldı: 15 kural, 210 `deny` girdisi, hiçbiri eksik değil). Sayı
  düzeltildi ve listenin elle tutulduğu, ama unutulması hâlinde kuralın
  denetimsiz KALMADIĞI yazıldı — `TestModullerBirbiriniImportEtmez` modül
  ağacını gezer, `.golangci.yml`'den haberi yoktur.

- README, müşteri oturumunu "Faz 8" diye anıyordu; aynı belgenin "Faz durumu"
  tablosunda Faz 8 (Auth · admin user · API key · RBAC) **tamamlanmış**
  görünüyor. Okuyan için çelişkili işaret: yapılmış bir fazın kapsamı olarak
  gösterilen şey aslında hiçbir fazın kapsamında değil. Faz numarası
  kaldırıldı, kapsam açıkça yazıldı.

### Kaldırıldı

- **Hız sınırının dışa açık anahtar yardımcısı KALDIRILDI** —
  `internal/core/http` paketindeki `PrincipalKey`. (Ad burada paketiyle
  nitelenmeden yazılıyor: nitelenmiş bir atıf okuyanı ARAMAYA yollar ve
  `internal/arch` bunu denetler; oysa bu maddenin söylediği şey tam olarak
  aranacak bir şey KALMADIĞIDIR.)

  v0.4.0'da dışa açık bir yardımcıydı ve hız sınırı anahtarını çağıranın
  kimliğinden türetiyordu. Üretimde tüketicisi
  YOKTU ve olamazdı: hız sınırı halkası koruma yığınında kimlik doğrulamadan
  ÖNCE koşar, yani çağrıldığı anda ortada bir kimlik bulunmaz ve fonksiyon her
  istekte aynı yedek anahtarı döndürürdü. Sunduğu şey tutulamayan bir vaatti.

  Gömülü kodu etkiler: kendi `KeyFunc`'ını yazan taraf bu yardımcıyı çağırıyorsa
  artık derlenmez. Karşılığı aynı davranışın kendi paketinde iki satırla
  yazılmasıdır; anahtarın kimliğe göre ayrılması isteniyorsa halkanın kimlik
  doğrulamadan SONRA takılması gerekir ve gerekçe `KeyFunc` godoc'undadır.

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
  bir arch testi çiviler (`TestChannelDerivationMeansTheSameOnBothSurfaces`).

  Kapsam dışı varyant, hiç var olmayan varyantla **aynı** hatayı döner
  (`404 cart_workflow_variant_unknown`): farklı bir sınıf, başka bir kanalda
  satılan ürünün varlığını ele verir ve gizlemenin kendisini delerdi.

  **Kapsam GİRİŞTE uygulanır.** Satır adedi güncelleme ve sepet tamamlama
  yolları kapsamı yeniden sormaz: sepete varyant sokabilen tek yol satır
  eklemedir ve sepete GİRMİŞ bir satırın, ürünü sonradan başka bir kanala
  taşıyan bir yönetici düzenlemesiyle ödenemez hâle gelmemesi verilmiş bir
  karardır. Sınır `workflows/cart/saleschannel.go`'da ve README'de yazılıdır;
  bir arch testi (`TestVariantReadsGoThroughTheChannelDecision`) her yeni varyant
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

  Dördüncü kapı atfın **sonradan** yapılabilmesidir: misafir olarak açılan bir
  sepet `POST /store/v1/carts/{id}` ile başkasının `customer_id`'sine devredilir
  ve sipariş o kimliğe yazılır — yani atıf yalnızca sepet açılışında değil,
  sepetin ÖMRÜ BOYUNCA beyana dayanır (ölçüldü: devir `200`, sipariş kurbanın
  adına). Kapı sayısı üç değil DÖRTTÜR; aynı sayı README'nin B2B bölümünde ve
  ADR 0008'de de dörttür.

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

### Bilinen sınırlar

Bu bölüm bir GEÇMİŞ kaydının parçasıdır ve yalnızca **bu sürümde değişeni**
söyler. v0.1.0 ile v0.4.0'ın "Bilinen sınırlar" bölümleri O SÜRÜMLERDE neyin
bilindiğini anlatır ve geriye dönük düzeltilmezler; kapanan bir sınır, kapandığı
sürümün kaydına yazılır — buraya. Bugün geçerli olan sınırların TAM listesi
[`README.md`](./README.md)'nin "Bilinen sınırlar" bölümündedir: bir sürüm
kaydından bugünü çıkarmak, üç listeyi üst üste koymayı gerektirirdi ve
benimseme kararını veren kişi tam olarak o listeyi okur.

**Kapananlar.**

- v0.4.0'ın "`POST /store/v1/carts` hâlâ `region_id` alıyor" maddesi KAPANDI:
  alan gövdeden kalktı, bölgeyi ve para birimini sunucu `country_code`'dan
  türetiyor (yukarıda, Kırıcı değişiklikler). Kapatma, maddenin kendi işaret
  ettiği yerde yapıldı — handler'da değil, türetmeyi zaten yapan akışta.
- Satış kanalı kuralının YAZMA yolunda uygulanmaması KAPANDI (yukarıda,
  Güvenlik). Bu, hiçbir sürümün "Bilinen sınırlar" bölümünde YAZMIYORDU ve
  kaydın asıl kısmı budur: kural v0.1.0'dan beri bir yetkilendirme diye
  anlatılıyor, yalnızca okuma yüzeyinde uygulanıyordu. Yazılmamış bir sınır,
  kimsenin kapatmadığı sınırdır; bu kez onu görünür kılan şey de bir belge oldu
  ([ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md) açığı, kendi
  gerekçesini kurarken buldu).
- Depo seçiminin POLİTİKASIZ olması KAPANDI (yukarıda, Eklendi): kural artık
  ele → sırala → eşitliği boz üçlüsüdür. Bu sınır da hiçbir YAYIMLANMIŞ sürümün
  "Bilinen sınırlar" bölümünde durmadı — aynı yayımlanmamış pencerede yazıya
  geçti ve kapandı. Kaydın değeri, kuralın v0.2.0'dan (çoklu depo desteğinin
  geldiği sürüm) beri sessizce "kimliği en küçük aday" olmasıdır. Kapatmanın
  kabul edilen bedelleri aşağıdaki açık sınırlara girdi.

**Devam eden.** v0.4.0'ın "vitrin sepetlerinde SAHİPLİK denetimi yok" maddesi
aynen geçerlidir; model değişmedi. Değişen tek şey, modelin kapsamadığı yerin
(`customer_id` iddiası) bu sürümde gerçek ikilide ÖLÇÜLMÜŞ olmasıdır.

**Bu turda araştırıldı, karar verildi ve BİLEREK açık bırakıldı.**

- **Müşteri kimliği doğrulanmıyor; harcama limiti KOŞULLU uygulanıyor.**
  Ölçümler ve gerekçe yukarıda, Güvenlik başlığında; karar
  [ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md)'de. Sınırın doğru
  cümlesi "harcama limiti uygulanmıyor" DEĞİL, "limit yalnızca müşterisini
  BEYAN EDEN alışverişe uygulanır"dır: kimliğin doğrulandığı bir vitrinde kural
  muhasebe disiplinini gerçekten uygular.

- **Satış kanalı kapsamı GİRİŞTE uygulanır; sepete girmiş bir satırın ADEDİ
  sonradan artırılabilir.** Kapsam yalnızca satır eklemede sorulur. Ürün
  sonradan başka bir kanala taşınsa bile satır adedini güncelleyen yol kapsamı
  yeniden sormaz (`Workflows.UpdateLineItem`,
  `internal/workflows/cart/update_line_item.go`); tamamlama akışı da sormaz.
  Sonucu tek cümleyle: vitrininde artık görünmeyen bir üründen, sepetinde zaten
  bir satırı olan istemci DAHA FAZLA satın alabilir. Bu bir gözden kaçma değil,
  verilmiş kararın bedelidir — alternatifi, yöneticinin bir katalog
  düzenlemesiyle müşterinin dolu sepetini ödenemez hâle getirmesiydi. Karar
  gerekçesiyle `internal/workflows/cart/saleschannel.go`'da yazılıdır ve bir
  arch testi her yeni varyant okumasını aynı kararı vermeye zorlar
  (`TestVariantReadsGoThroughTheChannelDecision`).

- **Çok kiracılılık YOKTUR ve bu bir karardır: sınır KURULUMDUR, satır değil.**
  74 tablonun hiçbirinde "bu satır kime ait" sorusunun cevabı yoktur, hiçbir
  sorgu böyle bir süzgeç taşımaz ve çerçeve kiracılar arası bir sınır
  tanımadığı gibi İDDİA DA ETMEZ. İki müşteriye tek kurulumdan hizmet vermek
  desteklenmiyor: bir kiracı = bir kurulum = bir veritabanı = bir süreç. Plan
  belgesi kavramı iki yerde kapsam dışı bırakıyordu ama GEREKÇESİNİ
  yazmıyordu; gerekçesiz bir kapsam dışı bırakma karar değildir, her turda
  yeniden tartışılır. Reddedilen iki tasarım ve kararı yeniden neyin açacağı
  [ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md)'da.

- **Yanlış bir bölge bağı MAĞAZAYI KAPATIR ve düşen sepeti KALICI olarak
  tüketir.** Var olmayan bir bölge kimliği bağlamak — ya da bir bölgeyi silip
  aynı adla yeniden açmak, çünkü yeni kayıt yeni kimlik alır — o depoyu her
  sepette eler; tek depolu bir kurulumda sonucu, katalog dolu olduğu hâlde her
  tamamlamanın reddedilmesidir. Düşen sepet bir daha tamamlanamaz, çünkü
  tamamlama akışının idempotency anahtarı sepet kimliğinden türer ve başarısız
  bir yürütme aynı anahtarla tekrar koşamaz. Bu yakma bu sürümden ÖNCE de
  vardı; değişen, tetikleyicisinin artık bir stok olgusu değil tek bir yönetim
  yazması olabilmesidir. Bedel kaldırılmadı, GÖRÜNÜR yapıldı: arıza kendi hata
  kodunu taşır ve o kod vitrine ulaşır.

- **Bölge bağı bir TERCİH değil KISITTIR ve geri düşme kümesini DARALTIR.** İki
  depoyu ayrı bölgelere bağlayan işletmeci, ilk deponun stoğu yarışta
  tükendiğinde siparişin düşmesini kabul etmiş olur — oysa politika yazılmadan
  önce o sipariş diğerinden çıkardı. "Önce A, tükenirse B" bölge bağıyla değil
  ÖNCELİKLE yazılır. Bağı sıralama anahtarına çevirip katı kesiği bir bayrağın
  arkasına almak değerlendirildi ve reddedildi; gerekçe
  [ADR 0010](docs/adr/0010-depo-secim-politikasi.md)'da.

- **Bir deponun SON bölge bağını silmek onu gizlemez, TÜM bölgelere açar.**
  Kural satış kanalı kapsamınınkiyle aynıdır ve aynı gerekçeden gelir: katı
  alternatif, açıldığı gün politikası olmayan tüm kurulumların siparişini
  durdururdu. Asimetri yazılmalı — satış kanalında bedel GÖRÜNÜRLÜKTÜR, burada
  DÜŞEN SİPARİŞTİR.

- **Akış kurulumunu denetleyen mimari değişmez sözdizimsel bir VEKİLDİR ve
  yanlış negatifi ÖLÇÜLDÜ.** `TestHerAkisBilesimKokundeKurulu`, "yanlış
  yapılandırma açılışı durdurabilir mi" sorusunu "kuruluma giden yol bir `go`
  ifadesinden geçiyor mu" diye sorar. `go` tek satırlık bir dolaylamanın
  arkasına saklandığında denetim GEÇER, oysa özellik sağlanmaz: gerçek süreçte
  ölçüldü — senkron ikili, kurulum hatasında çıkış kodu 1 verirken o biçimdeki
  ikili sağlıklı açılıp arızayı tek bir ERROR satırına indiriyor. Vekil yine de
  tutuluyor çünkü YAKALADIĞI biçimler (çıplak `go`, kapanış, çok halkalı
  zincir) kazara yazılanlardır; kaçırdığı biçim bilerek yazılmayı gerektirir.
  Kapsam `internal/arch/kayit_test.go`'da yazılıdır ve orada "bu değişmez
  açılışın kapalı arızalandığını garanti eder" cümlesi bilinçli olarak
  kurulmuyor.

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

[Yayımlanmamış]: https://github.com/bdrtr/gobit/compare/v0.8.0...HEAD
[0.8.0]: https://github.com/bdrtr/gobit/releases/tag/v0.8.0
[0.7.0]: https://github.com/bdrtr/gobit/releases/tag/v0.7.0
[0.6.0]: https://github.com/bdrtr/gobit/releases/tag/v0.6.0
[0.5.0]: https://github.com/bdrtr/gobit/releases/tag/v0.5.0
[0.4.0]: https://github.com/bdrtr/gobit/releases/tag/v0.4.0
[0.3.0]: https://github.com/bdrtr/gobit/releases/tag/v0.3.0
[0.2.0]: https://github.com/bdrtr/gobit/releases/tag/v0.2.0
[0.1.0]: https://github.com/bdrtr/gobit/releases/tag/v0.1.0
