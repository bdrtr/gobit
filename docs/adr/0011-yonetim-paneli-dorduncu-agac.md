# ADR 0011 — Yönetim paneli: dördüncü ağaç, kendi kimliği, çekirdeğin yazıcısı

- **Durum:** Kabul edildi
- **Tarih:** 2026-09-03
- **Faz:** 10 sonrası (yönetim paneli turu)
- **Değiştirildi:** 2026-09-03 — Karar 6'nın ertelediği yazma sorusu
  [ADR 0013](0013-panel-write-surface.md) ile kapatıldı. Dördüncü ağaç, kimlik
  ve yazıcı kararları değişmedi.

## Bağlam

Plan belgesi yönetim paneli UI'ını iki yerde kapsam dışı bırakıyor ve **neden**
bıraktığını söylemiyor. Panel yazılmaya başlandığında ortaya çıkan şey, bir
arayüz tasarımı sorunu değil: çerçevenin üç yazılı kararıyla doğrudan temas eden
bir **yerleşim ve kimlik** sorunu.

Karar öncesi depo şu hâldeydi ve hepsi ölçüldü:

- Yönetim kimliği **yalnızca** `Authorization: Bearer` başlığından çözülüyor.
  Üretim kodunda çerez okuyan ya da yazan tek satır yok; CSRF, CORS ve güvenlik
  başlığı middleware'i de yok.
- Koruma yığını yol **önekiyle** kapsamlanıyor ve eşleşme segment sınırında.
  `/admin/v1` dışındaki bir ağaç kimliğe, hız sınırına ve idempotency halkasına
  **hiç** girmiyor.
- Modül dışı paketlerde yanıt gövdesini doğrudan yazmak yapısal ihlal;
  `w.Write`, `w.WriteHeader` ve `http.Redirect` yakalanıyor.
- `html/template`, gömülü statik varlık ve şablon emsali depoda yok.

Bunun bir sonucu doğrudan şudur: **tarayıcı bir sayfaya giderken `Authorization`
başlığı gönderemez.** Sunucu tarafında üretilen HTML tercih edildiği anda,
kimliğin tarayıcıdan nasıl geleceği bir tasarım sorusu olmaktan çıkıp bir
çerçeve sorusuna dönüşüyor.

## Karar

### 1. Panel dördüncü bir ağaçta yaşar: `internal/adminui`

Ne çekirdek ne modül; `internal/workflows`'un kardeşi.

Modül içine konsaydı üç duvara birden çarpardı ve üçü de ölçüldü: başka hiçbir
modülü import edemez (bu denetimde gerekçeli muafiyet kapısı **yok**), api
paketinde şablonu yazıcıya veremez, ve şablonu alan üzerinden çalıştıran doğal
Go yazımı muafiyet listesine **yazılamaz bile** — çağrının adı çözülemediği için.

Çekirdek altına konamaz: çekirdek modülleri tanımaz. Bileşim kökü altına
konamaz: orası yalnızca kablolamadır.

**Bu ağacın bedeli, ADR 0006'nın `internal/workflows` için yazdığı bedelin
aynısıdır** ve aynı şekilde ödenir — bkz. Karar 5.

### 2. Panel `/admin/ui` altında yaşar ve koruma yığınına AÇIKÇA eklenir

`/admin/v1/ui/...` üç yeri aynı anda kırardı: adres çubuğundan gelen her sayfa
`401` alırdı; HTML uçları OpenAPI belgesine ve üretilen istemcilere sızardı;
router ağacını gezen yetki testi her sayfadan `403` beklerdi.

`/admin/ui` bu üçünü de çözüyor ama **varsayılan olarak açık** geliyor — kimlik
yok, kota yok. Bu, ADR 0007'nin kimlik satırının tam tersidir ve kabul edilmez.
Bu yüzden panelin kendi koruma halkası bileşim kökünde, koruma yığınının
döndürdüğü dilime **eklenir**; hız sınırı için önek `OpenPrefixes`'e girer —
dosya sunumu ve OpenAPI ile aynı sınıf.

Halka bileşim kökünde takılır, panelin `Routes` metodunda değil: router, route
kaydından sonra halka eklenmesini panikle reddediyor.

### 3. Kimlik: HttpOnly çerez, YALNIZCA panel ağacında

Giriş, jetonu `HttpOnly · Secure · SameSite=Strict` bir çereze yazar ve çerezin
yolu panel ağacıyla sınırlanır. **`/admin/v1` bu çerezi kabul etmez ve
etmeyecek.**

Bu, kararın omurgasıdır. Yönetim API'sinin bugünkü CSRF bağışıklığı bir
savunmadan gelmiyor; jetonun, tarayıcının kendiliğinden eklemediği bir başlıkta
durmasından geliyor. Çerezi `/admin/v1`'e açmak o bağışıklığı tek satırda yok
eder ve yönetim uçlarının **tamamını** yeni bir saldırı yüzeyine sokardı.

Ve bu, **çekirdeğe hiç dokunmadan** yapılabiliyor: kimliği context'e koyan
yardımcı dışa açık ve yetki denetimi kimliği yalnızca context'ten okuyor. Panelin
halkası çerezi okur, **aynı** kimlik doğrulayıcıya "Bearer" şemasıyla sorar ve
sonucu context'e koyar. Başlık okuyan kod da yönetim koruması da değişmez.

CSRF savunması `SameSite=Strict` **artı** durum değiştiren metotlarda `Origin`
başlığı denetimidir; ikincisi gerekli çünkü `SameSite` alt alan adı
senaryolarını kapatmaz. İkisi de panelin kendi halkasında yaşar.

### 4. HTML çekirdeğin yazıcısından geçer

`WriteHTML`, `WriteRedirect` ve `WriteAsset` çekirdeğin yanıt yazıcılarının
yanına eklenir. O dosya "çekirdek yazıcı tanımı" olarak taramadan zaten muaf ve
muafiyetin geçerliliği yalnızca mevcut iki yazıcının orada tanımlı olmasına
bakıyor — üçüncü bir yazıcı hiçbir denetimi bozmaz.

Gövde **önce belleğe** üretilir, sonra başlık ve durum yazılır. Doğrudan
yazıcıya akıtan bir tasarımda şablonun ortasında doğan bir hata, `200` durum
kodlu **yarım** bir sayfa bırakır ve panik yakalayıcı başlık yazıldıktan sonra
hiçbir şey yapamaz; arıza istemcide sessizleşir.

Ölçülmüş bir yan sonuç: 2xx zorunluluğu yalnızca JSON yazıcısına ait. Yani HTML
yazıcısı `401` ve `403` taşıyabilir — **kimliksiz istek giriş sayfasını doğru
durum koduyla alır**, yönlendirmeye gerek kalmaz. Yönlendirme yine de gerektiği
yerde (giriş sonrası) `Location` başlığı ve `303` ile yapılır; `http.Redirect`
yasaktır.

### 5. Ağacın kablolama boşluğu AÇILDIĞI ANDA kapatılır

Kayıt denetimleri kapsamlarını modül ağacına indiriyor. Panel ağacında "kayıtlı
ama hiçbir zeminde kurulmamış" bir yetenek arch testlerini **yeşil** bırakırdı —
ölçüldü. Bu, bu deponun en pahalı hata sınıfı: kaydı olmayan yetenek.

Uydurmaya gerek yok. Aynı boşluk `internal/workflows` için zaten kapatılmış ve
kalıbı hazır: ağaç adı, kurulum işareti olarak konvansiyonel bir yapıcı adı ve
denetimin körleşmesini engelleyen bir bayatlık kapısı. Panel ağacı aynı kalıbı
alır.

Aynı şekilde, gövde yazma taramasının modül dışı kolu şablon yazımını
**görmüyor** — çünkü çağrının alıcısı bir import adı değil. Bu bir izin değil,
taramanın ölçme biçiminin negatifidir; panelin tamamı o kör noktada yaşayacağı
için tarama şablon yazımını görecek biçimde genişletilir.

### 6. Panel, çerçevenin OKUMA yollarını kullanır

Katalog ekranları veriyi Query katmanından alır (ADR 0004), container'dan adla
çözülen dar bir arayüzle (ADR 0006). Hiçbir modül import edilmez; sepet akışı
aynı kalıbın kanıtlanmış örneğidir.

**Yazma bu ADR'nin kapsamında DEĞİLDİR** ve bilinçli olarak ertelenmiştir:
modüllerin yönetim tarafına açılmış hiçbir dar yüzeyi yok — fiyat modülünün
böyle bir yüzeyi hiç yok — yani yazma, üç modüle yeni sözleşmeler açmayı
gerektirir ve her sözleşme derleyicisiz bir taahhüttür. Karar, gerçek ekranlar
elde varken verilecek.

**2026-09-03'te karar verildi:** Erteleme aynı gün kapandı.
[ADR 0013](0013-panel-write-surface.md) yazmayı, her modülün kendi yayımladığı
**ilkel tipli** dar bir yüzeye bağladı ve o yüzeyi interop'tan AYRI bir adla
kaydetti — bugün üç tane: `product.admin`, `pricing.admin`, `inventory.admin`.
Yazma deponun değil SERVİSİN üzerinden geçer, yani handle tekliği denetlenir ve
`product.updated` yayımlanır. Panel bu adları **isteğe bağlı** çözer: ürün
modülü olmayan bir kurulum yine panel alır, düzenleme formu sebebini söyleyen
bir `503` döner. Karar 6'nın okuma yolu ana hatlarıyla durdu — katalog
ekranları hâlâ Query katmanının `Graph` çağrısını container'dan adla çözülen
dar arayüzden kullanıyor — ama DEĞİŞMEDEN kalmadı: ADR 0013 başlığında bu
kararı değiştirdiğini yazar ve yönetim yüzeyine dar bir OKUMA hakkı da tanır,
yalnızca modüller arası okuma katmanının izleyici kitlesi o veri için yanlış
olduğunda (konum kırılımı rezerve miktarları ve iç depo adlarını taşır, o
katmanın tüketicileri arasında ise vitrin vardır). Tur kazandırmak için
yapılan bir okuma bu hakkın dışındadır.

## Sonuçlar

**Olumlu**

- Çerçevenin kimlik yüzeyi **değişmiyor**; çerez panelin kendi ağacında kalıyor
  ve yönetim API'sinin CSRF bağışıklığı korunuyor.
- HTML yazmanın tek ve adlandırılabilir bir kapısı oluyor; kör noktadan
  yararlanmak yerine denetim genişletiliyor.
- Panel tek ikiliyle dağıtılıyor: ayrı bir araç zinciri, ayrı bir dağıtım ve
  CORS yüzeyi yok.
- Dördüncü ağacın kablolama boşluğu, açıldığı turda kapanıyor.

**Olumsuz — kabul edilen bedeller**

- **Depo dördüncü bir ağaç kazanıyor.** ADR 0006'nın `internal/workflows` için
  ödediği bedelin aynısı: kurallar ağaç adına göre yazıldığı için her yeni ağaç,
  her kuralın kapsamını yeniden sorgulatıyor.
- **Çerçeve HTML'den haberdar oluyor.** Çekirdek yanıt yazıcıları artık bir
  tarayıcı kavramı taşıyor. Bedel küçük ve tek dosyada, ama "başsız çerçeve"
  ifadesinin kenarını aşındırıyor.
- **Yeni bir saldırı yüzeyi açılıyor.** Bir HTML paneli yayımlamak XSS ve
  çerçeveleme yüzeylerini ilk kez açıyor; depoda bugüne kadar hiç güvenlik
  başlığı yoktu. Savunma panel önekine takılıyor, API davranışı değişmiyor.
- **Panel varsayılan olarak derleniyor ve yayımlanıyor.** Kapatma yolu bir ortam
  değişkeni DEĞİL, bileşim kökünden bir satır silmektir — ADR 0007 ve ADR
  0009'da reddedilen bayrak sınıfının aynısı burada da reddedildi: yanlışlıkla
  `false` verilen bir anahtar, paneli hiçbir hata üretmeden açık bırakabilirdi.
- **Oturum 12 saat sürüyor ve yenileme yok.** Panel süre bitimini önceden
  gösterir; uzun bir düzenleme oturumunun ortasında jeton ölebilir.
- **Çıkış TOPTANDIR.** Panelden çıkmak kullanıcının tüm oturumlarını düşürür;
  arayüz bunu gizlemez, düğme bunu söyler.

## Reddedilen seçenekler

**Paneli modül ağacına koymak.** Kazancı gerçekti: kayıt ve zemin denetimleri
bedava gelirdi. Reddedildi çünkü bedeli ölçülmüş üç duvar — modül import yasağı,
api paketinde yazma yasağı ve muafiyet listesine yazılamayan çağrı biçimi —
paneli fiilen imkânsız kılıyor. Kazanç Karar 5 ile elle geri alınıyor; bedel
geri alınamıyor.

**Kimliği `localStorage`'da tutup her isteğe JavaScript ile takmak.** Çerçeveye
hiç dokunmazdı. Reddedildi çünkü sunucu tarafında üretilen HTML kararıyla
çelişir: adres çubuğuna yazılan bir gezinme ve F5 kimliksizdir, yani ilk boyama
JavaScript'e bağımlı hâle gelir. Ayrıca yönetim jetonunu XSS'e açar ve depoda
hiç içerik güvenlik politikası olmadığı için bu gerçek bir bedeldir.

**Başlık okuyan çekirdek koduna çerez geri düşüşü eklemek.** En az kod isteyen
yoldu. Reddedildi çünkü mağaza korumasını da etkiler ve yönetim API'sinin
tamamını CSRF'e açar; bugün onu koruyan tek şey jetonun otomatik gönderilmeyen
bir başlıkta olmasıdır.

**Kör noktadan yararlanmak** — şablonu doğrudan yazıcıya vermek. Bugün denetimi
geçiyor. Reddedildi çünkü bu bir izin değil, taramanın ölçme biçiminin
negatifidir; sınır kapatıldığı gün panelin tamamı ihlale düşerdi. Depo bu hata
sınıfını (denetleyicisi olmayan yüzey) daha önce kendi eliyle üretti ve kapattı.

**Her sayfa için gerekçeli muafiyet yazmak.** Muafiyetler çağrı bazındadır ve
kullanılmayan bir muafiyet testi düşürür; her yeni sayfa listeyi büyütürdü.
Ölçeklenmez ve muafiyet listesi, kuralın kendisinden uzun olurdu.

**Paneli ayrı bir uygulama olarak yazıp çerçeveye CORS eklemek.** Panelin
çerçeveden ayrı sürümlenmesini sağlardı. Reddedildi çünkü CORS bir güvenlik
yüzeyidir (origin listesi, kimlik bilgisi taşıma, ön uçuş) ve çerçevenin
sertleştirme kararlarına, karşılığında hiçbir şey almadan yeni bir madde
eklerdi; jeton da tarayıcıda saklanmak zorunda kalırdı.

**Panelin kendi HTTP API'sine çağrı yapması.** Panelin, bir API istemcisinin
yapamayacağı hiçbir şeyi yapamayacağını garanti ederdi ve bu gerçek bir kazanç.
Okuma dilimi için reddedildi: Query katmanı aynı ekranı tek turda kuruyor, oysa
HTTP yolu her satır için ek çağrı ve ikinci bir serileştirme demek. ~~Karar
**yazma dilimi için yeniden açıktır** ve orada kazancı daha ağır basabilir.~~
**2026-09-03'te düzeltildi:** Yazma dilimi için de reddedildi.
[ADR 0013](0013-panel-write-surface.md) aynı seçeneği üç bedelle kapattı: panel
kendine bir yönetim jetonu üretip taşımak zorunda kalırdı — çerezin yol
kapsamının tam olarak kaçındığı tehlike —, her düzenleme iki serileştirme
öderdi, ve kendi bağlantı havuzu üzerinden kendini çağıran süreç doygunlukta
yavaşlamak yerine kilitlenirdi.

## Kararın yeniden açılması

Üç veri bu kararı yeniden açar:

1. **Panelin bir ekranı, çerçevenin API'sinin sunmadığı bir işi yapabildiği ilk
   an.** O an panel bir referans tüketici olmaktan çıkıp ayrıcalıklı ikinci bir
   yol olur ve Karar 6 yeniden düşünülmelidir.
2. **Yazma diliminin gerçek maliyeti ölçüldüğünde.** Üç modüle yeni dar yüzey
   açmak, panelin kendi API'sine HTTP çağrısı yapmasından pahalı çıkarsa
   reddedilen o seçenek geri gelir. **2026-09-03:** bu tetik, istediği
   ölçüm hiç yapılmadan düştü. [ADR 0013](0013-panel-write-surface.md)
   loopback'i maliyet karşılaştırmasıyla değil YAPISAL üç bedelle kapattı —
   panelin kendine bir yönetim jetonu üretmesi, her düzenlemenin iki
   serileştirme ödemesi, ve sürecin kendi bağlantı havuzu üzerinden kendini
   çağırırken doygunlukta yavaşlamak yerine kilitlenmesi. Bundan sonraki
   tetikleri o ADR yazıyor.
3. **Panelin ayrı sürümlenmesi istendiğinde.** O gün CORS kararı yeniden
   tartılır; bugün reddedilme sebebi karşılığının olmamasıdır, imkânsızlığı
   değil.

## İlgili

- [ADR 0001](0001-modul-arasi-iletisim.md) — dar arayüz + adla çözüm.
- [ADR 0004](0004-query-veri-erisimi.md) — panelin okuma yolu.
- [ADR 0006](0006-workflow-modul-erisimi.md) — dördüncü ağacın emsali; bu ADR
  onun kurduğu kalıbı ikinci kez uyguluyor.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — arızada davranış; panelin
  koruma halkası ve bayrak reddi oradan besleniyor.
- [ADR 0013](0013-panel-write-surface.md) — Karar 6'nın ertelediği yazma
  sorusunun cevabı; bu ADR'yi değiştirir.
