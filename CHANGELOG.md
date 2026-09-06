# Değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/) ölçütlerine,
sürümleme [Semantic Versioning](https://semver.org/lang/tr/) kurallarına
uyar.

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir ve bir uç, daha doğru bir tasarım uğruna taşınabilir.
Sabitlenme `1.0.0` ile olur.

## [Yayımlanmamış]

### Kararlar

- **"Bir kararın arkasında" diyen bir satırın kararı yazılmamıştı; yazıldı ve
  karar metin eşleşmesi çıktı** (A18, B2'nin OPTION VALUE yarısı).

  Satır kendini "bir kararın arkasındaki bir iş" diye tarif ediyordu ama kararın
  NE olduğunu söylemiyordu — A4, A5, A11 ve A12'de düzeltilen kusurun aynısı,
  bir başlık altında. Ölçülünce baştaki soru şu çıktı: **bir alışverişçi
  "Color: red" diye süzdüğünde neyi eşleşme sayacağız?**

  Üç aday, üçü de ağaca karşı fiyatlandırıldı. **(1) Birebir** — hiçbir yeni
  makine istemiyor, ve sıradan istemci farkı hiç görmüyor, çünkü sözlük ucu
  değeri BİREBİR veriyor ve az önce aldığı şeyle süzen istemci daima eşleşiyor;
  bedel elle yazılmış URL'de: "Kirmizi" hiçbir şey bulmuyor ve çağıran bunu boş
  bir kataloğdan ayırt edemiyor. **(2) Büyük/küçük harf duyarsız** — kataloğun
  `q` aramasının zaten kullandığı şekil, ama bir ifade indeksi istiyor ki bu
  depoda **hiç yok**, ve ADR 0015'in canlı tehlikesini miras alıyor: `--locale=C`
  ile kurulmuş bir kümede katlama ASCII dışına işlemiyor ve süzgeç sessizce boş
  dönüyor. **(3) ASCII'ye katlama** — `slugify` ve `turkishASCII`'nin handle'lar
  için zaten yaptığı normalleştirme, yani deponun zaten güvendiği bir kural, ve
  (2)'nin sahip olmadığı özelliğe sahip: küme yerelinden BAĞIMSIZ. Bedeli
  saklanan normalleştirilmiş bir kolon, artı bir itiraf — satıcının ayrı tutmak
  istemiş olabileceği değerleri BİRLEŞTİRİR.

  Hangisi seçilirse indeksi de o seçiyor, o yüzden "öncülük eden indeks yok"
  ayrı bir soru değil.

  **Ve B8'in engeli kaydedildi.** B7 satırı "abonesi olmayan bir konuyu denetim
  reddediyor" engelini yazıyor; B8 aynı engele sahipti ve yazmıyordu.
  `customer.deleted`'ın istediği abone erasure koordinatörü, o da B17, o da A2 ve
  A4'ü bekliyor. Yani olay ile ilk tüketicisi tek paket ya da hiçbiri —
  deponun B7 ve B13'te iki kez ödediği kural.

- **Bir kararın adı, onu bulan ÖZELLİĞİN adı olursa, sonraki tur aynı soruyu
  ikinci kez öder** (A15, B4).

  A15 "vitrin, doğrulayamadığı bir adresi sonradan postalamak için alabilir mi?"
  diye soruyordu ve bekleme listesinin (C1) engeli diye kaydedilmişti. 2026-09-06'da
  ölçüldü: soru bir SINIFI tarif ediyor, bir özelliği değil. Vitrinin tek
  kimliği publishable anahtar ve her vitrin yazması konusunu istemcinin seçtiği
  bir yol parametresinden alıyor — adres defteri ve
  `POST /store/v1/orders/{id}/returns`. Yani müşterinin YAZDIĞI bir yorum (B4)
  aynı şekle sahip, ve bu bugüne kadar hiçbir yerde yazmıyordu.

  **Depo bu sorunun bir örneğini zaten cevaplamış ve gerekçesi order modülünün
  vitrin handler'ında duruyor:** kimliksiz bir yazma orada kabul edilebilir,
  çünkü bir TALEP taleptir — stok da para da kımıldatmaz, bir operatörün onu
  teslim alması gerekir, ve EYLEYEN her uç yönetime ait ve kapsamlıdır. Karara
  ayırt edicisini veren de bu gerekçe, ve özellik başına cevaplanabilir bir
  soru: **yazma ile etkisi arasında bir insan duruyor mu?** Bekleme listesi bunu
  geçemiyor, çünkü bir satırın etkisi giden bir mesaj ve kimse onaylamıyor. Bir
  yorum için cevabı aynı ayırt edici veriyor: ONAYLA yayımlanıyorsa mevcut
  gerekçe onu olduğu gibi kapsar, GÖNDERİMLE yayımlanıyorsa kapsamaz ve cümlenin
  ilk tablodan önce yazılması gerekir.

  Aynı okumada A grubunun tamamı gözden geçirildi: on altı açık satırın on ikisi
  seçenekleriyle birlikte yazılmış ve bir cümleyle cevaplanabilir; dördü (A4,
  A5, A11, A12) bir KONU adlandırıyor, aday cevap adlandırmıyor — yani onları
  cevaplamak önce seçenekleri icat etmeyi gerektiriyor. Seçenekler ölçülmeden
  uydurulmadı; eksiklik olduğu gibi kaydedildi.

- **A15 uygulandı, alıntılanmadı: cevabı taşıyan şey SQL — bir yorum satırı
  değil** (A15, B4).

  Aynı gün yorum modülü yazıldı ve ayırt edici — yazma ile etkisi arasında bir
  insan duruyor mu? — bir cümle olarak değil bir TİP olarak kuruldu. Yorum
  `submitted` doğuyor, vitrinin gördüğü iki sorgu statüyü parametre değil SABİT
  olarak taşıyor, repository ve servis onları statü almayan AYRI metotlar olarak
  yayımlıyor, ve vitrin tarafında tek bir yoruma id ile bakan hiçbir uç yok —
  yani gönderimin döndürdüğü id bir makbuz, onaylanmamış bir satırın kulpu
  değil. Statü parametresi alan ORTAK bir sorgu bilerek reddedildi: tasarımın
  tamamını, bugüne kadar gönderilmiş her şeyi yayımlamaya tek bir atama
  uzaklıkta bırakırdı. İade talebinin gerekçesi düzyazıda duruyor; buradaki
  gerekçe iki SQL sabitinde duruyor, ve fark bugün bu sınavı geçmekle bir
  sonraki refactor'dan sonra da geçmek arasındaki fark.

  **İkinci işlenmiş örneğin öğrettiği, birincisinin öğretemediği şey: ayırt
  edici yalnızca AKIŞI değil ŞEMAYI da kısıtlıyor.** İade talebi yazarı hakkında
  hiçbir şey saklamadığı için bu görünmemişti. Yazar kimliklendirilemediği için
  üç kolon reddedildi ve üçünün gerekçesi ayrı. Sipariş id'si: spam'i daraltır,
  kimseyi doğrulamaz — iade talebinin üzerinde çalıştığı kimlik bilgisinin ta
  kendisi, yani onay sayfasını görmüş herkeste var — ve ondan basılacak bir
  "doğrulanmış alışveriş" rozeti şemanın söylediği yanlış bir cümle olurdu;
  okunacağı anlamı taşıyamayan bir kolon, saklanmasındansa olmaması iyidir.
  E-posta: doğrulaması ve aboneliği bırakması olmayan bir posta listesi — bekleme
  listesini A15'te düşüren tam da bu özellik, ve depo bu sınıfı zaten
  reddediyor, çünkü notification modülü hiçbir alıcı adresi saklamıyor. IP: bu
  depoda bir alışverişçinin saklanan tek ağ tanımlayıcısı olurdu, ve onu
  kullanacak kota zaten bir katman yukarıda duruyor. Geriye tek bir kimlik alanı
  kalıyor, o da yazarın BASILSIN diye yazdığı ad.

  A2 bununla kapanmıyor ve modül kapandığını iddia etmiyor; A2'nin ulaşması
  gereken yüzeyi tek kolona indiriyor. Bekleme listesi de hâlâ düşüyor, artık
  daha keskin bir sebeple: yorumda onay adımı ÖZELLİĞİN kendisi, bekleme
  listesinde ise giden her mesajı tek tek onaylayan bir operatör demek olurdu —
  insanı araya koymak orada özelliği silmek olur.

- **Yazılmış, belgelenmiş, uçtan uca test edilmiş bir eklenti KURULAMIYORDU —
  ve bu sınıf için yazılmış kapı YEŞİLDİ** (C5, D22).

  `plugins/webhookout` derleniyor, birim/entegrasyon/e2e testleri geçiyor ve
  `.env.example`'da kopyalanabilir bir satırla anlatılıyordu. Kompozisyon
  kökünün eklenti kataloğunda YOKTU — kurulumun baktığı tek harita orası — yani
  `PLUGINS=webhook-out` açılışı "unknown plugin" ile durduruyordu. Ölçüm
  `go list -deps ./cmd/server`: ikilinin bağımlılık kapanışı sekiz eklenti
  sayıyor ve bunu saymıyordu, yani paket ikiliye hiç derlenmiyor ve
  migration'ı da migrate yüzeyinin dışında kalıyordu.

  **Kapının neden görmediği, bulgunun kendisinden değerli.**
  `TestThePluginNamesInTheDocsAreReal` iki yönü de kontrol ediyor ve ikisi de
  geçiyordu, çünkü kayıtlı ad kümesini `plugins/` ağacındaki `const Name`
  değerlerini ayrıştırarak türetiyor. Yani "belgelenen ad bir yerde tanımlı mı"
  ve "tanımlı ad belgede anılmış mı" diye soruyor; **operatörün sorduğu soruyu,
  yani ikilinin o adı bilip bilmediğini hiç sormuyor.** Sonuç en keskin hâliyle:
  belge, kopyalandığında kapının kendi godoc'unda önlediğini söylediği açılış
  hatasını üreten bir satır taşıyordu.

  İki düzeltme de yapıldı: katalog satırı, ve `TestEveryPluginIsInstallable` —
  bir tarafı yine eklenti ağacından, ÖTEKİ tarafı kataloğun kendisinden okuyor,
  ve her anahtarı o dosyanın kendi import tablosundan çözüyor ki takma adlı bir
  import var olmayan bir eklenti gibi okunmasın. Kapı, sevk edilmiş kusurun tam
  kendisiyle kanıtlandı: katalog satırı silindiğinde `webhook-out` adını vererek
  düşüyor.

  **Ders eklentileri aşıyor: bir karşılaştırmanın İKİ tarafını da aynı yerden
  türeten kapı, ikisinin birbiriyle uyuştuğunu kanıtlar, dünya hakkında hiçbir
  şey kanıtlamaz.** D16'da kolon denetimi yalnızca `CREATE TABLE`'dan kurduğu
  bir şemayı okuyordu; daha öncesinde sahte deponun sahteyi kanıtladığı
  bulunmuştu. Kalan denetimler bu gözle gözden geçirilmeli.

- **`internal/app`'in migrate-down testleri, kanıtın kendisinde değil ÖN
  KOŞULUNDA düşüyordu.** İki test region'ın "tam iki migration"ı olduğunu elle
  yazmıştı; modül üçüncüsünü kazanınca ikisi de kırıldı. Sayı artık okunuyor:
  biri geri alınacak adım sayısını mevcut sürümden türetiyor, diğeri
  dokunulmayan sahibin sürümünü geri almadan ÖNCE okuyup onunla karşılaştırıyor.
- **Bir handler, hiç belgelemediği bir sorgu parametresini okuyabiliyor ve
  deponun bütün kapıları yeşil kalıyor** (D25).

  Mutasyonla kanıtlandı: vitrin ürün listelemesine `undocumented_switch` okuyan
  bir dal ekildi, modülün bütün suite'i **ve** `internal/arch` geçti. Böyle bir
  parametre, çalışan ama reklamı yapılmayan bir anahtardır — üretilen istemciye,
  belgeye ve incelemeye görünmez.

  Bunu kapatıyor sanılan denetim yalnızca **öteki yönü** kapatıyor.
  Godoc'u önlediği kusuru zaten yazıyor: "okunmayan bir parametreyi şemaya
  koymak, istemciye ÇALIŞMAYAN bir özellik vaat etmektir." Eksik yarı için daha
  kötüsü var: karşılaştırdığı iki taraftan hiçbiri HANDLER değil — üretilen
  belgeyi testin içine elle yazılmış bir listeyle karşılaştırıyor, yani ikisinde
  birden olmayan bir parametre kendisiyle uyuşuyor ve geçiyor.

  **Kapı fiyatlandırıldı ve YAZILMADI, çünkü naif biçimi bu deponun kendi
  ölçütünü geçemiyor.** İlk yaklaşıklık — her `URL.Query().Get("x")` ve her
  `xxxParam(r, "x")` sabitini, aynı paketin `queryParameter("x")` çağrılarıyla
  karşılaştırmak — **on iki modülde** bulgu bildirdi, ve tek tek bakınca
  neredeyse hepsi gürültü çıktı: `id`, `upload_id` ve `sales_channel_id` YOL
  parametresi, `limit` ile `offset` ise regex'in göremediği bir `paging`
  değişkeniyle belgeleniyor. Yanlış pozitiflerini muafiyet listesinde taşımak
  zorunda kalan bir kapı, kapı sayılmaz — bu ölçütü aynı gün başka bir kapı için
  ben koymuştum.

  Gereken yapısal kural yazıldı ama uygulanmadı: bir fonksiyon, KENDİ
  parametresini `URL.Query().Get`'e geçiriyorsa parametre okuyucusudur ve sabit
  onun çağrı yerlerinden toplanır; belgelenen taraf ise `openapi.Parameter`
  üreten çağrılardan türetilir. İki taraf, iki farklı yapı, elle liste yok.
  Bugünkü sızıntının sıfır olduğuna inanılıyor — ekilen anahtar bulunan tek
  örnekti — yani kapı ÖNLEYİCİ, ki aceleye getirilmemesinin sebebi de bu.

  Testin gevşemediği mutasyonla gösterildi — `-steps` yok sayıldığında hâlâ
  düşüyor.

- **gobit bir KÜTÜPHANE olacak, kopyalanan bir şablon değil** (ADR 0025).

  Bugün yapısal olarak şablon modeli ve bu bir tercih değil: 155.054 satırın
  %100'ü `internal/` altında, yani hiçbir dış modül import edemiyor. Tek kullanım
  yolu klonlayıp `cmd/server`'ı düzenlemek.

  **Ölçüm, "gerekirse yeniden yazarız" sorusunu cevapladı: yeniden yazmak
  gerekmiyor.** Kütüphane modelinin ihtiyaç duyduğu en küçük public küme —
  modül sözleşmesi, sağlayıcı sözleşmeleri, olay yüzeyi, tipli hatalar,
  container — **2.961 satır, yani kod tabanının %1,9'u**. Uzatma noktalarının
  hepsi zaten var ve üretim yollarında çalışıyor; iş icat etmek değil, taşımak
  ve adlandırmak. Yeniden yazmak yirmi dört ADR'yi, onları zorlayan arch
  korumalarını ve ölçülerek varılmış her düzeltmeyi atıp aynı yere varmak olurdu.

  Karar verilen: yön. **Karar verilmeyen: hangi paketlerin public olacağı** — ve
  bütün risk orada, çünkü depo kuralı zaten biliyor: bir sözleşmeye giren alan
  bir daha çıkarılamaz. Modül modelleri 7.058 satırla (%4,6) bu hareketin en
  kalıcı kararı; on bir modülde zaten `metadata` map'i var, ki tam da bir
  projenin modeli struct'ı değiştirmeden genişletebilmesi için.

  Yüzeyin doğru olup olmadığının somut sınavı: `ecom-iyzico` bu deponun dışında,
  yayımlanmış sözleşmelere karşı yazılabiliyor mu? Bugün yazılamıyor — `plugins/`
  ağacı `internal` dışında ama içindeki her eklenti `core/plugin`'i
  import ediyor, yani depoda üçüncü tarafın eklenti yazamayacağı bir eklenti
  sistemi var.

### Düzeltildi

- **Dört karar satırı bir KONU adlandırıyordu; ölçülüp SORUYA çevrildi — ve ilk
  taslak yirmi dört yanlış iddia taşıyordu** (A4, A5, A11, A12).

  `gaps.md` bu dördünü kendisi işaretlemişti: her biri konusunu söylüyor, aday
  cevap söylemiyordu, yani "bir toplantıda cevaplanamaz, yalnızca yeniden
  açılabilir". Aynı paragraf uyarısını da yazmıştı: **seçenekleri ölçmeden icat
  etmek, karar kaydına yanlış bir seçenek koymanın yoludur.**

  Uyarı doğru çıktı ve ölçülebilir biçimde. İlk tur dört soruyu da seçenekleriyle
  yazdı; karşıt bir tur onları yeniden ölçtü ve **yirmi dört yanlış iddia**
  buldu — auth modülüne dört tablo atfedilmişti, tek migration'ında beş var; bir
  fiyat kuralı bağlamının tek üretim çağıranı olduğu yazılmıştı, yönetim ucu
  `attr_` önekiyle serbest bir harita kuruyor; faturanın kişiyi altı adlandırılmış
  kolonda tuttuğu söylenmişti, aynı satırda yedincisi duruyor ve serbest biçimli.
  İkinci tur bunları düzeltti ve daha küçüklerini bıraktı.

  **Bu yüzden satırlar bilerek KISA yazıldı.** Bir karar satırının ihtiyacı üçtür:
  soru, birbirinden ayrı adaylar, her adayın bedeli. Sürekli yanlış çıkan şey bu
  üçü değil, yanlarına eklenen envanterdi — panelin kaç alan istediği, hangi
  dosyanın hangi satırı. Onlar onarılmadı, DÜŞÜRÜLDÜ: bir karar satırındaki her
  fazladan ölçüm cümlesi, kaydın ağaç hakkında yanılması için yeni bir fırsattır.

  **İki karar ölçülünce yer değiştirdi, ve bu ölçmenin kendi gerekçesi.**

  A5'in tarif ettiği durum vitrinden ERİŞİLEBİLİR DEĞİL: `customer_group_customer`
  bileşik anahtar taşıdığı için bir müşteri birden çok gruba girebiliyor, ama
  sepet kural motorlarına varyant kimliğinden başka bir şey göndermiyor. Yani
  satır önce "beraberlik nasıl bozulur" değil, "sepet ne göndermeli" sorusudur;
  beraberlik ancak "hepsini gönder" seçilirse doğar, ve o da yalnızca
  fiyatlandırmayı ilgilendirir çünkü merdiven zaten `better` ile kuruluyor.

  A12'de pahalı sanılan yarı ZATEN KURULU: doğrulama salt kriptografik değil, her
  istekte kullanıcıyı ve bir oturum çıpasını okuyup çıkıştan ya da parola
  değişiminden önce üretilmiş jetonu reddediyor. Yani iptal var, kaba taneli de
  olsa; "uzun ömür + iptal" bir yapılandırma işi, "kısa ömür + refresh" ise yeni
  tablo ve yeni uç isteyen pahalı olan. Naif okumanın tam tersi.

  A4'te asıl bulgu şu: faturayı hiçbir şey silemiyor ama koruma bir KISIT değil,
  KODUN YOKLUĞU — modülün sorgularında, deposunda ve beş ucunda tek bir DELETE
  yok, yani bir silme uygulaması bir cümle uzakta. A11'de ise soru sırası yanlış
  çıktı: vitrinin alışverişçinin dili diye bir kavramı hiç yok, o yüzden "çeviri
  nerede durur", "istek dilini nasıl söyler"in ardından gelir.

  Kararların hiçbiri VERİLMEDİ. Bu satırların kuralı cevabın insana ait olması;
  yazılan şey soru, adaylar ve bedeller.

- **Belgelerin tamamı koda karşı ölçüldü: otuz sekiz iddia yanlıştı, ve üçü
  gaps.md'nin KENDİ tablosuyla çelişiyordu.**

  Denetim otuz beş markdown dosyasını altı ayrı şeritten okudu ve her iddiayı
  ağaca karşı ölçtü. Altmış üç bulgu çıktı, **otuz sekizi** karşıt doğrulamadan
  sağ kaldı, yirmi beşi elendi — elenenler arasında sıralama, üslup ve
  "tarihine göre doğru" olan sayımlar var, ve bir tanesi de bu turda benim
  yanlış bildirdiğim bir defektti.

  **En ağır sınıf: dosyanın okuyucuya iki zıt cevap vermesi.** `gaps.md`'nin
  düzyazısı üç şeyin eksik olduğunu söylüyordu, üçü de yapılmıştı ve üçünün de
  YAPILDIĞI aynı dosyanın tablosunda yazıyordu — sepetin adresleri siparişe
  ulaşmıyor (B11 "Built" diyor, `order_addresses` migration'ı duruyor), eklenti
  host'u iş kaydedemez (B13 "Built" diyor, `Host.RegisterJob` duruyor), ürün
  görseli upload kaydına bağlı değil (B15 "built" diyor, `upload_id` kolonu
  duruyor). Üçü de üstü çizilip tarihlendirildi.

  **Bayat sayıların çoğunun tek bir sebebi vardı: review modülü.** Bir modülün
  eklenmesi 16→17 modül, 24→25 şema, 71→72 tablo, 15→17 interop yüzeyi diye
  onlarca cümleyi aynı anda bayatlattı. Sayılar yerinde düzeltildi; tarihiyle
  birlikte yazılmış ve o gün doğru olan iki sayım ise KAYIT olarak bırakıldı ve
  hangi ağacı anlattıkları açıkça söylendi, ki okuyucu iki cevap arasında
  kalmasın.

  `README` iki sert yanlış taşıyordu: teknoloji tablosu `samber/do`'yu DI
  seçimi olarak sunuyordu — o kütüphane ne `go.mod`'da ne `go.sum`'da var,
  container elle yazılmış — ve var olmayan bir tanıtıcıya atıf yapıyordu.

  **Düzeltme turunun KENDİSİ altı yeni yanlış üretti ve denetim şeridi onları
  yakaladı.** Bir dosyanın MinIO yüzünden dışarıda kaldığını "import etmiyor"
  diye yazmak, üç işi tek iş saymak, `core/provider`'a olmayan bir vergi
  sağlayıcısı eklemek, ADR 0025'ten hiç var olmayan bir cümleyi alıntılamak, ve
  ADR 0011'e "bu tetik ölçümle düştü" diye yapılmamış bir ölçüm yazmak. Altısı
  da tek tek yeniden ölçülüp düzeltildi. Ders kayda değer: bir belgeyi
  düzeltmek, düzeltilen iddia kadar dikkat isteyen yeni bir iddia yazmaktır.

  **İki kapı deliği kapatıldı, ikisi de bu turda canlı örnekle bulundu.**

  Birincisi: satır numarası yasağının deseni yalnızca `.go` uzantısını
  arıyordu, yani bir BELGEDEN bir belgeye verilen satır numarası yasağın
  dışındaydı. Depoda tam bir tane vardı ve daha yazıldığı gün çürümüştü — ADR
  0009 plan belgesinin iki satırını gösteriyordu ve ikincisi, ADR commit
  edilmeden önce alakasız bir görev maddesine kaymıştı. Desen genişletildi ve
  yeni yarısı kendi pozitif kontrolünü aldı.

  İkincisi: bir BELGENİN ters tırnak içinde andığı test adı hiçbir denetimden
  geçmiyordu. Mevcut markdown denetimi noktalı adları (`paket.Ad`) arıyor,
  oysa bir test tam da nitelemesiz yazılan tek semboldür. Ölçüldü: 93 benzersiz
  ad, 152 anma, sekizi var olmayan bir testi gösteriyordu — hepsi İngilizce
  çevirinin taşıdığı Türkçe adlar, dört ADR'de, `docs/mimari.md`'de ve
  CHANGELOG'da. Altısı düzeltildi; ikisi muafiyet aldı, çünkü onlar ölü adı
  BİLDİRDİKLERİ KUSUR olarak alıntılıyor ve canlı adla değiştirmek bulgunun
  kendisini yok ederdi.

  Kapı ilk koşusunda yazdığım üçüncü muafiyeti ELEDİ: CHANGELOG o adı
  `-run …` biçiminde, daha büyük bir alıntının içinde yazıyor, yani ad ters
  tırnağı doldurmuyor ve desen orayı hiç görmüyor. Muafiyet kaldırıldı ve
  görülmeyişin sebebi godoc'a yazıldı — kullanılmayan bir muafiyet, kapının
  kapsamını sessizce büyütür.

  Üç mutasyon, hepsi sha256 doğrulamalı geri alma ile: bir belgeye ekilen
  `.md:NN` göstergesi yakalanıyor, ekilen var olmayan test adı yakalanıyor, ve
  muafiyet silinince kapı onun satırında düşüyor.

- **Bir kök eklemek için yapılan temizlik, godoc'ların yirmi beş ölü dosya
  adına atıf yaptığını ortaya çıkardı — ve o sınıfı hiçbir kapı görmüyordu.**

  Başlangıç dar bir borçtu: `vitrin` bir kök değildi, ve ledger'da olmayan
  dosyalarda dört Türkçe tanımlayıcı yaşıyordu. Dördü de çevrildi —
  `vitrinIstegi` → `storefrontRequest`, `anahtarliVitrinIstegi` →
  `keyedStorefrontRequest`, `vitrinKatalogu` → `storefrontCatalog`,
  `vitrinZarfi` → `storefrontEnvelope`; 55 kullanım, yedi e2e dosyası. Adlar
  icat edilmedi: `internal/smoke` paketi kendi `storefrontRequest`'ini zaten
  taşıyordu, yani seçilen ad deponun kendi konvansiyonu. Sonra `vitrin` kök
  listesine eklendi (186'dan 187'ye) ve çevrilmiş bir adı geri alan mutasyon
  tanımlayıcı şeridinde düşüyor.

  **Asıl bulgu bu temizliğin kenarında duruyordu.** `internal/smoke`'un
  `vitrin` geçen tek yeri bir tanımlayıcı değil, paketin kendi senaryolarını
  sayan godoc'undaki `vitrin_test.go` adıydı — ve öyle bir dosya YOK. Bakınca
  aynı godoc'un saydığı sekiz senaryodan YEDİSİNİN adı ölüydü: hepsi bir
  Türkçe→İngilizce yeniden adlandırma turunda değişmiş, godoc olduğu yerde
  kalmıştı. Depo geneli tarandı: dokuz dosyada **yirmi beş** ölü ad. Hepsi
  düzeltildi ve her eşleşme tahminle değil dosyanın KENDİ test adlarıyla
  doğrulandı — örneğin godoc'un "kanalsız publishable anahtar 201 alır ama
  vitrinde 401 yer" cümlesi `keys_test.go`'daki
  `TestPublishableKeyWithoutChannelIsRejectedByStorefront` ile birebir.

  Bu, deponun kendi ADIYLA andığı en inatçı kusur sınıfı — belgenin çürümesi —
  ve yedi kapının yedisi bu yirmi beş ölü atfın üstünde YEŞİLDİ.

  **Neden görülmüyordu, ve kapı neden dar:** mevcut atıf denetimi KÖKLÜ
  yollara demirli (`internal/…` ile başlayan), yani
  `internal/smoke/storefront_test.go`'yu görüyor ama `storefront_test.go`'yu
  görmüyor. Bir paket kendi dosyalarını sayarken kısa biçimi yazar — yani
  denetimin görmediği biçim, tam olarak bir paketin kendini anlattığı biçim.

  Geniş hali ölçüldü ve REDDEDİLDİ: her çıplak `*.go` adını çözümlemek 49
  çözülmeyen yazım buluyor ve neredeyse hiçbiri atıf değil — metot çağrıları
  (`b.Go`, `x.Go`) ve dil denetiminin kendi tarayıcısına verdiği sahte dosya
  adları (`planted.go`, `english.go`). `_test.go` ekiyle daraltınca 87 anmada
  60 benzersiz ad kalıyor ve çözülmeyen TEK bir tane var, o da gerekçesiyle
  muafiyet listesinde. Muafiyet listesi kendi yanlış pozitiflerini taşımak
  zorunda kalan bir kapı, kapı sayılmaz.

  Çözümleme depo geneli, dizin içi DEĞİL — ve bu da ölçüldü: 82 çıplak atfın
  28'i başka bir paketteki test dosyasını anıyor (kompozisyon kökü kendini
  denetleyen testi anıyor, bir workflow kendini süren uçtan uca testi), yani
  dizinle sınırlı bir kural fazladan hiçbir şey yakalamadan 28 doğru atfı
  düşürürdü. Verilen söz bu yüzden mevcut denetimin sözüyle aynı: adın DOĞRU
  dosyaya gittiği değil, ÇÖZÜLDÜĞÜ.

  Kapı üç mutasyonla kanıtlandı, hepsi sha256 doğrulamalı geri alma ile. Bir
  TEST dosyasının yorumuna ekilen ölü ad yakalanıyor — bu en önemlisi, çünkü
  yirmi beş çürüğün hepsi test dosyalarındaydı ve tarayıcının oraya bakmaması
  kapıyı tam da varlık sebebine kör bırakırdı. Bir ÜRETİM dosyasına ekilen ölü
  ad da yakalanıyor. Muafiyet silinince kapı `hatayolu_test.go` üzerinde
  düşüyor, yani muafiyet süs değil taşıyıcı.

- **Yol denetiminin kök listesinde beş dosyalık bir delik vardı, ve deliği
  bulan şey deliğe düşen bir dosya oldu.**

  `TestRepoPathsAreEnglishOutsideLedger` bir yolun Türkçe olup olmadığını
  `turkishStems` listesine bakarak sorar, ve o listenin kendi godoc'u onu bir
  TABAN olarak tarif ediyor, bir çit olarak değil. Taban 2026-09-06'da ölçüldü:
  izlenen bütün yollar parçalarına ayrıldı — 488 benzersiz parça — ve 19'u
  Türkçe çıktı. Bunlardan beşi listede hiçbir karşılığı olmadığı için
  denetimden GÖRÜNMEDEN geçiyordu, ve beşi de yol ledger'ında değildi:
  `docs/adr/0002-di-container-el-yazmasi.md`,
  `docs/adr/0003-migration-iptali.md`, `docs/adr/0004-query-veri-erisimi.md`,
  `docs/adr/0005-link-semasi-migration-disinda.md` ve `docs/mimari.md`.
  Ledger'ın ADR'leri 0001, 0006–0011 diye gidiyordu; eksik aralığın kendisi
  delikti.

  Bu, denetimin kırıldığı bir hata değil: denetim hiçbir şey görmeden geçiyordu,
  ve geçen bir denetimle ayırt edilemiyordu.

  Kök listesi VERİdir, o yüzden toplu yeniden yazılmadı — 18 kök alfabetik
  yerlerine tek tek eklendi ve sonuç küme farkıyla doğrulandı: 168'den 186'ya,
  kayıp sıfır. (Aynı listeden bir kökün toplu bir işlemle sessizce silinmesi bu
  depoda daha önce yaşandı.)

  **Eklenen bir kök beklenmedik bir yerde ısırdı, ve orada bulduğu şey de
  gerçek borçtu.** `veri` kökü, ledger'da OLMAYAN üç e2e dosyasında
  `vitrinVeri` adlı test yardımcısını yakaladı — yol şeridi değil, tanımlayıcı
  şeridi. Yardımcının godoc'u zaten İngilizceydi ve komşuları da öyleydi
  (`openStorefrontCart`, `storefrontCompletionBody`); Türkçe kalan tek şey
  adıydı. `storefrontData` olarak çevrildi.

  Değişmez iki yönde de mutasyonla kanıtlandı. Ledger satırı silinince denetim
  `docs/mimari.md`'yi adıyla VE onu yakalayan kökle bildiriyor; kök silinince
  `TestPathLedgerIsNotStale` aynı satır için "artık Türkçe ad taşımıyor" diyor.
  Yani ne kök ne de ledger satırı süs.

  Kapanmayan borç adıyla yazılıyor: `vitrin` hâlâ bir kök DEĞİL, ve ledger'da
  olmayan dokuz `.go` dosyası onu taşıyor — `vitrinIstegi`,
  `anahtarliVitrinIstegi`, `vitrinKatalogu`, `vitrinZarfi`, `describeVitrin`.
  Bu turda çevrilmediler, çünkü kök eklemek onları çevirmeyi ZORUNLU kılar ve o
  ayrı bir iştir; burada ölçülüp adlandırıldılar.

- **Hiçbir şeyin OKUMADIĞI bayrağın VARSAYILANINI da hiçbir şey tutmuyordu — ve
  `allow_backorder` yalnız değil, dördün biri** (A6, D2).

  Önce ölçüm, çünkü işi yönlendiren o oldu. Depodaki her yayımlanmış boole
  bayrağı bir `go/ast` aracıyla tarandı: A tarafı json etiketi taşıyan her
  `bool`/`*bool` alan ve okuma katmanının kayıt anahtarları; B tarafı aynı adın
  bir KOŞULDA geçmesi (if, for, switch, case, ya da değil/ve/veya işleneni),
  `x != nil` yama şekli hariç — o alanın GÖNDERİLİP gönderilmediğini sınar,
  ne dediğini değil — artı her SQL yükleminde snake_case kolonun aranması.
  Üretilen sqlc paketleri atlandı: satır struct'ları kolonu yayımlamıyor,
  aynalıyor.

  Üç sayı çıktı. **35 yayımlanmış boole bayrağı; on modülde 17 boole kolonu.**
  **Hiç yazılmayan: sıfır** — 17 kolonun her birini adıyla yazan bir INSERT ya
  da bir UPDATE var, yani `TestEveryColumnIsWrittenBySomething` bu popülasyonun
  tamamında işini yapıyor, ve bu dışarıdan kontrol edildi. **Taşınan ama
  hakkında hiçbir karar verilmeyen: dört** — `manage_inventory`,
  `allow_backorder`, `discountable`, `is_giftcard`. Geri kalan her saklanan
  boole'nin sistemin davranışını değiştiren bir okuyucusu var.

  **Asıl bulgu dördün dağınık OLMAMASI.** Deponun okunmayan bütün boole
  kolonlarını tek bir modül yayımlıyor, diğer on altısı hiç yayımlamıyor. Yani
  A6 "bir bayrağa okuyucu lazım" değil, ürün modülünün DTO'sunun tutmadığı dört
  söz veriyor olması, ve bunu bayrak bayrak cevaplamak üçünü geride bırakır.
  İkisi bu modülde okuyucu edinemez bile: vitrin envanter kaydını bilerek
  YORUMLAMADAN geçiriyor (ADR 0004'ün kabul edilmiş bedeli), yani stok çiftini
  okuyabilecek tek yer checkout saga'sı.

  **Bayraklara okuyucu YAZILMADI, çünkü o bir karar (A6) ve hâlâ açık.** Bunun
  yerine, karar beklerken saklanan değerin çürümesine açık iki kapı kapatıldı —
  ve ikisi de önce ölçüldü. **Varsayılanlar tutulmuyordu:** varsayılan bloğu
  olan üç bayrak teker teker ters çevrildi (`manage_inventory` true'dan false'a,
  `allow_backorder` false'tan true'ya, `discountable` true'dan false'a) ve ürün
  paketinin TÜM testleri — birim ve gerçek bir PostgreSQL'e karşı entegrasyon —
  her üçünde de YEŞİL kaldı. Karşılaştırma da ölçüldü: inventory modülünün aynı
  cinsten varsayılanı tutuluyor,
  `TestCreateInventoryItemVarsayilanSevkiyatGerektirir` `requires_shipping` ters
  çevrildiğinde anında düşüyor — yani bu bir ev âdeti değil, bir boşluktu. Dört
  yeni birim testi hem varsayılanları hem de açıkça gönderilen değerin
  KORUNDUĞUNU sabitliyor; bayrakların işaretçi olmasının sebebi zaten
  gönderilmiş bir `false` ile "hiç gönderilmedi"yi ayırmak, ve dolduracağı yerde
  üzerine yazan bir varsayılan bloğu yalnız varsayılan testlerinden geçer.
  **Kısmi bir güncelleme de sıfırlayabilirdi:** ikinci kapı, hiçbir bayrağı
  ADLANDIRMAYAN bir güncellemenin ikisini de olduğu gibi bıraktığını gerçek
  veritabanına karşı kanıtlayan bir entegrasyon testi. Birim testi olamazdı —
  koruma Go'da yazılı değil, varyant sorgusunda kolon başına bir `COALESCE`, ve
  servis paketinin sahte deposu bu iki kolonu hiç modellemiyor.

  **Saklanan gerekçe bu:** hiçbir şeyin okumadığı bir bayrağın ikinci savunma
  hattı YOKTUR. Okunan bir bayrağın varsayılanı kaydığında ona dayanan bir test
  kızarır; taşınan bir bayrağınki hiçbir şeyi kızartmaz, üstelik kolon her
  satırda bir değer biriktirmeye devam eder. Hasar A6'nın cevaplandığı gün
  yüzeye çıkar: yeni yazılan okuyucu, o güne kadar yazılmış her satıra göre
  davranır ve hiçbir migration kasıtlı değerlerle kazara değerleri birbirinden
  ayıramaz. Taşınan bir bayrak için varsayılan, sözleşmenin TAMAMIDIR — çünkü
  bayrağın bu depoda gerçekten üretilen tek parçası odur.

  **Ve bu tarama bilerek bir mimari kapıya dönüştürülmedi.** Go alan adları
  üzerinde naif çalıştırıldığında 15 bayrak bildiriyor ve 11'i yanlış — %73
  yanlış pozitif — çünkü buradaki boole'lerin çoğu saklanan bayrak değil, bir
  yanıt DTO'sunda SONUÇ alanı (`already_issued`, `already_open`, `released`,
  `cart_completed`, `summary_recorded`, `reservations_confirmed`): onları bu
  deponun okumaması gerekiyor, onları İSTEMCİ okuyor. Kapıyı bir boole KOLONUNA
  bağlamak 11'in 10'unu eliyor ve rapor beşe iniyor. On birincisi bağlamadan
  sağ çıkıyor ve hâlâ yanlış, ve öğretici olan o: `automatic_taxes` gerçek bir
  kolon, region DTO'sunda yayımlanıyor, region modülünde onu okuyan hiçbir dal
  yok — ama sepet okuyor. Region onu modül sınırının ötesine, değeri ADSIZ bir
  boole olarak döndüren ilkel bir interop metoduyla veriyor, ve sepetin vergi
  adımı ona bakıp dallanıyor: otomatik değilse vergi satırı yok. Değer akıyor;
  AD sınırı geçmiyor. Bu, ADR 0001'in interop kuralının tam da tasarlandığı gibi
  çalışması — yani ada dayalı bir okuyucu denetimi bu deponun kendi ev üslubuna
  karşı sağlam yapılamaz, ve `automatic_taxes` yüzünden derlemeyi düşüren bir
  kapı birine yayımlanmış bir sözleşmeyi tarayıcıyı memnun etmek için
  genişletmeyi öğretirdi. Bulgu bunun yerine `docs/gaps.md`'ye yazıldı; bir
  testin tutamayacağı bir bulgunun dürüst yeri orası.

- **Kapıların kendisi denetlendi: 89 kapıdan üçü, yazılma sebebi olan kusurun
  tam üzerinde YEŞİLDİ** (D23).

  D22 bir cümleyle kapanmıştı — "kalan denetimler bu gözle gözden geçirilmeli".
  Bu tur onu yaptı: `internal/arch`'taki 89 kapının hepsi teker teker okundu ve
  her birine aynı üç soru soruldu. Karşılaştırmanın iki tarafı ne? Her taraf
  NEREDEN geliyor? Hangi makul ihlal bu kapıyı yeşil bırakır?

  **Önce sonucun büyük yarısı, çünkü geri kalanını inandırıcı kılan odur:
  kapıların ezici çoğunluğu sağlam, ve sebebi şans değil YAPI.** 89'un 21'i
  yalnızca körlük kontrolü ya da pozitif kontrol olarak var — tek işi bir ihlal
  ekip yanındaki okuyucunun hâlâ gördüğünü kanıtlamak olan testler. Kalanların
  neredeyse hepsi, yürüyüşünün o halkası sessizleşirse ne anlama geleceğini
  adıyla söyleyen en az bir sayaç taşıyor; birkaçı üç ile beş arası taşıyor,
  çünkü tek bir toplam hangi halkanın koptuğunu gizlerdi. Zayıf olduğu tahmin
  edilen üç kapı da ölçülüp aklandı.

  **Bulgu 1 — `TestMoneyIsAnInteger` beş üretim ağacından BİRİNİ okuyordu.**
  Godoc'u depo geneline ait bir kural söylüyor (para, minor birimde INTEGER
  saklanır); yürüyüşü ise modül listesiydi. Ölçüm: üretim ağaçlarında para adlı
  **682** struct alanı var, bunların **118'i `internal/modules` dışında**, ve o
  118'in **93'ü `internal/workflows`'ta** — yani toplamların HESAPLANDIĞI yerde:
  checkout, fiyat, indirim, vergi, kargo, ödeme tutarı. Kuralın korumak için
  yazıldığı yerde bildirilen bir float, kuralın DIŞINDAYDI; kapı 118'inin
  hepsinde, dosyayı hiç açmamış bir denetimin yeşil olduğu gibi yeşildi.
  Yürüyüş artık ortak üretim ağacı listesi, ağaç başına dosya sayacıyla — bir
  ağaç sessizleşirse geçmek yerine düşüyor — ve karar aynı anda işaretçi ve
  dilim float'lara genişletildi, çünkü nullable bir para sütunu tam da birinin
  işaretçiye uzanacağı yer. Üç ayrı mutasyonla kanıtlandı; eski kapı,
  checkout'un para zincirine ekilen aynı float'ta yeşil kalıyordu.

  **Bulgu 2 — bir kapı, kendi godoc'unun "asıl sebebim" dediği yönde
  DÜŞEMİYORDU**, ve bu D22'nin dersinin en küçük ölçeği: "aynı yer" ikinci bir
  dosya bile değil, iddianın kendi fonksiyonuydu. Panelin durum listesi, modül
  tarafını testin GÖVDESİNE yazılmış üç elemanlı bir dilimle karşılaştırıyordu.
  Her eleman gerçek bir sabite derleyici tarafından bağlıydı — listeyi modülün
  cevabı gibi gösteren de buydu — ama SAYIM teste aitti. Mutasyonla ölçüldü:
  modülün sabit bloğuna ve `Status.Valid` switch'ine beşinci bir durum eklemek,
  yani gerçek değişikliğin tamamı, kapıyı YEŞİL bıraktı; operatör o durumu asla
  seçemezdi ve hiçbir hata sebebini söylemezdi. Modülün söz dağarcığı artık
  modülün kendi models paketinden OKUNUYOR ve sonra DERLENMİŞ `Status.Valid`
  süzgecinden geçiriliyor: aynı şeye bakan iki farklı alet, çünkü bir kaynak
  taraması kendini denetleyemez ve Go sabitleri çalışma zamanında yansıtılacak
  şekilde var olmaz. Bildirilip switch'e alınmayan bir sabit artık ayrıca
  raporlanıyor. Yeni pozitif kontrol karşılaştırmaya değil OKUYUCUYA ekiyor,
  çünkü eksik olan oydu. Paneldeki bir godoc bu bağlamanın çalıştığını zaten
  iddia ediyordu; o cümle bu düzeltmeye kadar YANLIŞTI, şimdi doğru.

  **Bulgu 3 — üç migration kapısı, deponun 24 migration dizininden 16'sını
  okuyordu.** Geri alma, modüller arası foreign key kuralı ve gerçek
  PostgreSQL'de gidiş-dönüş; üçü de girdisini modül listesinden türetiyordu. Hiç
  açılmayan sekizin dördü eklenti şeması, dördü ÇEKİRDEK: `core/audit/migrations`,
  `core/eventbus/outbox/migrations`, `internal/core/workflow/pgstore/migrations`
  ve `internal/core/job/jobpg/migrations`. Çekirdek dördü açık farkla daha kötü,
  çünkü her açılışta HER modül migration'ından ÖNCE uygulanıyorlar: orada
  başarısız olan bir down, migration defterini bütün açılışın önünde kirli
  bırakır — ki gidiş-dönüş testinin yazılma sebebi kelimesi kelimesine budur.
  Sekizinin sekizi de ölçüldüğünde kurala uygundu; DENETLENMEDEN uygundular, ki
  bu okunmamış bir dosyanın güvenlikle kurduğu ilişkinin aynısı. Ortak bir
  yardımcı artık dizinleri üretim ağaçlarından buluyor, iki tabanla: her modül
  hâlâ bulunmalı, ve en az bir küme `internal/modules` DIŞINDA olmalı ki
  ileride bir daraltma sessizce değil gürültüyle düşsün. Entegrasyon kapısı
  24'ün hepsini gerçek bir kapsayıcıda gidip geliyor.

  **Bu iş sırasında bir hata ÜRETİLDİ ve sevk edilmeden yakalandı**, ve
  kaydedilmesinin sebebi onu ortaya çıkaranın mutasyon koşusu olması: yardımcının
  ilk hâli depo kökünü de özyinelemeli yürüyordu, yani her dizin iki kez
  bulunuyor ve her bulgu iki kez yazılıyordu. Tekilleştirerek değil, kökü tek
  derinlikte okuyarak düzeltildi — tekilleştirme örtüşmeyi kaldırmaz, gizlerdi.

  **Dört kapı daha ölçüldü ve BİLEREK düzeltilmedi**, her biri sebebiyle
  yazıldı; çünkü "baktık ve bıraktık" ile "bakmadık" aynı cümle değil. Kolon
  denetimi aynı 16/24 kapsamında — dışarıdaki on tablonun hiç kolon denetimi yok
  — ama yazma tarafını genişletmeden bildirim tarafını genişletmek o paketlerin
  BÜTÜN kolonlarını ölü diye raporlardı, ve yalancı çoban bir kapının silinme
  sebebidir. Bir modülün bir EKLENTİ tablosunu adlandırması bugün serbest;
  dosyanın başlığı bu izni çekirdek ve link tabloları için savunuyor, eklenti
  tabloları için savunmuyor, ve başlığı savunulmuş bir kararın olduğu yerde
  "sahip" teriminin anlamını sessizce değiştirmek doğru sıra değil. Varsayılan
  mux yasağı, pakette kendi sayacı ve pozitif kontrolü OLMAYAN tek yasak kapısı:
  boş bir yürüyüş geçer. Ve katman saflığının pozitif kontrolü kararın
  KENDİ YENİDEN YAZIMINA ekiyor — yani yasak parçanın eşleşebildiğini
  kanıtlıyor, gerçek yürüyüşün onu gördüğünü değil.

  **Bu kaydın İDDİA ETMEDİĞİ şey.** Değiştirilmeyen 84 kapı, okuyucuları,
  sayaçları ve kontrolleri okunarak yargılandı — her biri kırılarak değil.
  Mutasyonla kanıtlanan beş tane: düzeltilen üçü ve iki kör nokta.
  `TestNoTurkishOutsideLedger` hiç çalıştırılamadı (kullanıcının depo kökündeki
  izlenmeyen dosyası onu düşürüyor; tur boyunca o tek test atlandı), smoke etiketli
  paket de koşulmadı.

- **Kargonun olayları SIRASIZ gelir; sevkiyat durum makinesi hepsini
  reddediyordu — tekrarları ise hoş görüyordu** (B10, D24).

  Tablonun tamamı gözle okunarak değil, tek kullanımlık bir sonda ile
  YAZDIRILARAK ölçüldü, ve cevabın şekli kaydı hak ettiren şey: ikinci bir
  ship, deliver veya cancel noop'a düşüyordu — yani ETKİSİZLİK düşünülmüştü — ama
  pending + deliver ve delivered + ship'in ikisi de çatışmaydı, yani YENİDEN
  SIRALAMA reddediliyordu. Bir webhook akışının yaptığı iki şey bunlar ve
  yalnızca biri düşünülmüş.

  Sonucun canlı bir yarısı var, gizli bir yarısı var. Gizli olan: henüz kargo
  eklentisi yok (C6), yani bugün reddedilen bir webhook yok. Canlı olan: yönetim
  uçları var, ve kargonun portalıyla elle mutabakat yapan bir operatör bir
  teslimi kaydetmeden önce "ship"e basmak zorundaydı — bu da saatten bir sevk anı
  damgalıyor, yani KİMSENİN ÖLÇMEDİĞİ bir sayıyı, tam da eski kuralın korumaya
  çalıştığı sütuna. Eski godoc "adımı atlamak shipped_at'i boş bırakır ve
  mutabakatın sevk anına cevabı olmaz" diyordu: gerçek bir boşluğu adlandırıp
  yanlış çareyi yazmış. Teslimi reddetmek sevk anını üretmiyor; teslimi de atıyor
  ve müşterinin elinde olduğu ispatlı bir koliyi temelli "pending" bırakıyor.

  Düzeltme tabloya DÖRDÜNCÜ bir sonuç ekliyor — sevkiyatın konumunun GERİSİNDE
  kalan bir bildirim, durumu geriye çekmeden kabul ediliyor — ve geri çevrilen
  alternatif, yani yalnızca callback'lerin kullanacağı ikinci ve gevşek bir
  tablo, tablonun yanına yazıldı: aynı statüler hakkında birbiriyle çelişen iki
  tablo, tam da tablonun önlemek için var olduğu "üç servis metoduna dağılmış
  if'ler" şeklidir. Geç gelen bir toplama bildirimi takip numarasını YAZIYOR —
  çoğu kargoda onu taşıyan tek mesaj odur — ve HİÇBİR an damgalamıyor, çünkü
  eldeki tek saat "şimdi" diyor ve sevki kendi teslimatından sonraya tarihlerdi.
  Eksik damga "bize söylenmedi" der; sırasız damga olmamış bir şeyi iddia eder.

  İptal sıkı kalıyor ve tablonun taşıdığı ayırt edici bu: buradaki tek KOMUT
  odur, bildirim değil, dolayısıyla "geç geldi" onun için anlamsızdır — ve BİZ
  geri çağırdıktan sonra toplama bildiren bir kargo, bizi geçmiş olmuyor, kendi
  kaydımızla çelişiyor.

- **Tax'ın şekli dört modülde daha arandı: ikisinde KUSUR çıktı, ikisinde
  çıkmadı — ve çıkmayışı da ölçüldü** (D6, D19, D20).

  D6 kendi hatasını zaten kaydetmişti: iki modül paylaştıkları ŞEKİL üzerinden
  eşleştirilmişti, kusur üzerinden değil. Bu yüzden bu kez sorulan soru şekil
  değil: bir SERVİS metodu okuyup karar veriyor ve sonra AYRI bir autocommit
  deyimiyle yazıyor mu? Dört modülün ikisinde cevap evet, ikisinde hayır. Hayır
  da bir sonuçtur ve burada öyle yazılıyor.

  **`auth`: vardı, ve bıraktığı iz isteğin ömrünü aşıyor.**
  `Service.SetPassword` önce `GetUser` çağırıyordu — tek başına bir deyim —
  sonra `SetPasswordHash`, kendi işlemi. Araya giren bir `DeleteUser`
  kullanıcıyı VE kimliklerini yumuşak siliyor; yazma hiçbir kimlik bulamayıp bir
  tane EKLİYOR: silinmiş bir kullanıcının altında CANLI bir kimlik. Foreign key
  itiraz edemez, ve bu sınıfın tamamının sebebi bu — yumuşak silme bir
  UPDATE'tir, `auth_user` satırı fiziksel olarak yerinde kalır, CASCADE hiç
  çalışmaz.

  **Sonuç bir güvenlik açığı DEĞİL, ve bu varsayılmadı, ölçüldü.** Silinmiş
  yönetici o kimlikle geri giremiyor: `Login` (`GetUserByEmail`) de belirteç
  doğrulaması (`principalFromToken`) da önce CANLI kullanıcıyı okuyor;
  `TestARevivedIdentityCannotLogIn` yetim satırı ham SQL ile imal edip reddi
  kanıtlıyor. Gerçek bedel daha küçük ve KALICI: silinen kullanıcının adresi
  `auth_identity_provider_uniq` içinde sonsuza kadar duruyor, yani o adrese YENİ
  bir yönetici açmak çakışmayla düşüyor — kullanıcı listesi adresi boş
  gösterirken. Onarım yolu da yok: zaten silinmiş bir kullanıcı için
  `DeleteUser` NotFound döner.

  Aynı boşluğun ikinci çeşidi silme yerine `UpdateUser` ile üretildi: INSERT bu
  kez ESKİ adresi `provider_identity` alanına yazdı — tam olarak
  `SyncIdentityProviderIdentity`'nin önlemek için var olduğu ayrışma.

  **Düzeltme okumayı yazmanın İÇİNE taşıdı.** `LockLiveUser` (`FOR SHARE`) artık
  `Repo.SetPasswordHash` işleminin ilk deyimi, ve kimliğin `provider_identity`
  değeri bir parametreden değil O satırdan geliyor: canlılık ile adresin tek bir
  kaynağı var, hangi anı anlattıkları konusunda anlaşmazlığa düşemezler. Servis
  artık tek bir depo çağrısı yapıyor ve `providerIdentity` parametresi imzadan
  kalktı. Tax'ın dışa açılmış `WithTx`'i gerekçesiyle reddedildi: o, SERVİSİN
  kilitli satırdan bir karar çıkarması gerektiğinde doğru cevaptır, burada ise
  gereken yalnızca "canlı mı" ve "adresi ne" — ikisi de yazmaya ait. Kanıt
  mutasyonla: `LockLiveUser`'dan `FOR SHARE` sökülünce yeni test tam da önlemek
  için yazıldığı yetimle düşüyor.

  Yolda ikinci bir şey sabitlendi: başarısız giriş sayacının `FOR UPDATE`'i
  DOĞRUydu ama hiçbir test onu tutmuyordu. Kilit sökülünce eşzamanlı on iki
  artıştan sekizi kalıyor; `TestTheFailedAttemptCounterLosesNoIncrement` artık
  tutuyor.

  **`auth`'ta ölçülüp bilerek KAPATILMAYAN bir yarış da var, ve adı konuluyor.**
  `LinkSalesChannel` kanalı kilitliyor, anahtarı kilitlemiyor: servisin
  `GetAPIKey` okumasıyla bağlama yazması arasına giren bir `DeleteAPIKey`,
  az önce koparttığı anahtar için bir bağlama satırı bırakıyor. Üretildi — ve
  ulaşılamaz: o satıra giden her yol önce canlı anahtarı okuyor. Kapatmak
  `api_key` üzerinde ikinci bir `FOR SHARE` demek, üstelik kanalınkinden ÖNCE
  alınmak zorunda, çünkü `DeleteAPIKey` önce anahtarı sonra bağlama satırlarını
  geziyor ve kanalı önce alan bir akış bekleme çemberini kapatırdı. Kimsenin
  okuyamadığı bir satır için yeni bir kilit sırası kuralı satın alınmadı.

  **`promotion`: iki tane vardı, ve asıl kusur satır değil CÜMLEydi** (D20).
  `AddPromotionRule` ile `SetApplicationMethod`, ikisi de, `GetPromotion` ile
  "promosyon canlı" kararını verip sonra yazıyordu. Yarış tartışılmadı,
  ÜRETİLDİ: rakip işlem gerçek yumuşak-silme deyimini çalıştırıyor ve
  `pg_blocking_pids` ile yazmanın TAM O oturumu beklediği doğrulanıyor. Orijinal
  kodda ikisi de hiç beklemedi ve her biri bir yetim satır bıraktı.

  **Yetimin yaptığı şey ise hiçbir şey, ve bunu ölçmek bu kaydın dürüst yarısı.**
  `ComputeDiscounts` sıfır `DiscountTotal` ve kodu `UnmatchedCodes` içinde
  döndürüyor, `LookupStoreCoupon` `promotion_not_usable` diyor,
  `ListPromotionRules` 404 veriyor — çünkü okuyan her sorgu promosyonun kendi
  `deleted_at` sütununu süzüyor. Hiçbir müşteriden yanlış tutar alınmıyor.
  Yetimi döndüren tek iki okuyucunun ise HTTP rotası yok. Yani düzeltilen şey üç
  godoc cümlesi: "yetim kalması yapısal olarak imkânsızdır" diyorlardı ve bu
  ölçülebilir biçimde yanlıştı — D6'nın tax'a karşı kaydettiği hatanın aynısı.
  Kodun doğru hâle getirilmesi seçildi çünkü modülün zaten sahip olduğu
  makineyle otuz satır tuttu; pahalı olsaydı doğru cevap cümleleri düzeltip
  durmaktı.

  Düzeltme `LockPromotionShared` (`FOR SHARE`) ve `requireLivePromotion`:
  deponun kendi işleminin ilk adımı. Paylaşımlı, çünkü aynı promosyona kural
  ekleyen iki yönetici birbirini beklememeli; yumuşak silme ise `FOR SHARE`'in
  ÇAKIŞTIĞI bir UPDATE kilidi alıyor — ve bu çakışma PostgreSQL'in kilit tablosu
  üzerine akıl yürütülerek değil, ölçülerek doğrulandı. İşlem depoda kaldı:
  metot başına tek yazma var, dolayısıyla tax'tan farklı olarak servise bakan
  hiçbir yüzey değişmedi.

  Üç şey de kanıtlanırken bulundu. İlk düzenek atıldı: rakip olarak
  `SELECT ... FOR UPDATE` kullanıyordu, o da her satır kilidiyle çakışır, yani
  YANLIŞ kilit alınmış olsa bile test yeşil kalırdı. İki sahte depo hatanın
  kendisini modelliyordu — var olmayan bir promosyona kural kabul ediyorlardı,
  ki hiçbir birim testinin bunu yakalayamamış olmasının sebebi budur — ve
  sözleşme öğretilince `TestHataSiniflandirmasiStatusKodunaCevrilir` düştü:
  test 404 bekliyordu, sahte 200 döndürüyordu. Üçüncüsü,
  `DeletePromotion`'ın "çocukları okunamaz hâle gelir" iddiası yarıştan bağımsız
  olarak yanlıştı: `GetApplicationMethod` silinmiş bir promosyonun yöntemini
  döndürüyor, çünkü o sorgu yalnızca yöntemin KENDİ `deleted_at` sütununu
  süzüyor.

  **`pricing`: yoktu, ve bu bir omuz silkme değil kanıtlanmış bir cevap.** Yazan
  her servis metodu tam olarak BİR depo çağrısı yapıyor; çok çağrılı üç metot —
  `ListPrices`, `ListStorePrices`, `ListPriceRules` — salt okunur ve fazladan
  okumaları boş liste yerine 404 dönebilmek için var. Depo zaten bitmiş şekle
  sahipti: `ReplacePrices` işlemine `GetPriceSetForUpdate` ile başlıyor. Hiçbir
  davranış değişmedi. Yine de bir godoc yanlıştı: silinmiş bir fiyatın altına
  kural YAZILABİLİYOR, FK susuyor. Kilit eklenmedi, çünkü servis orada tek çağrı
  yapıyor — korunacak bayat bir karar yok, ve olmayan bir yarış için kilit
  makinesi kurmak yanlış olurdu. Cümle düzeltildi;
  `TestSilinmisFiyataKuralYazilabilirAmaUlasilamaz` iki yarımı birden çiviliyor:
  FK gerçekten susuyor VE ortaya çıkan satıra ulaşılamıyor.

  **`customer`: şekil var, kusur yok — ve farkı ölçüm söyledi.** `AddToGroup`
  iki varlık denetimini kilitsiz yapıyor, yani yarış GERÇEK: READ COMMITTED
  altında kilitsiz bir işlem tek başına hiçbir şeyi korumaz, her deyim taze bir
  anlık görüntü alır. Sonucu ise sıfır: silinmiş bir grubun üyelik satırları
  olağan silme yolunda ZATEN bırakılıyor, silinmiş bir müşterininki de öyle —
  dolayısıyla yarışın ürettiği satır, modülün normal işleyişte ürettiğinden
  ayırt edilemez. Kilit eklenmedi; godoc artık eklenmesi gerekseydi hangisinin
  olacağını (`GetCustomerForUpdate`, çünkü bu modülde müşteri satırı her zaman
  ilk kilitlenir) yazıyor, ki bir sonraki okuyucu aynı ölçümü baştan yapmasın.

- **Sütun denetiminin üç kör noktasından İKİSİ kapandı, DÖRDÜNCÜSÜ kapatırken
  bulundu — ve düzeltme ilk koşumunda dokuz canlı bulgu çıkardı** (D16, D18).

  Bir gün önce bu bölümde şu yazıyordu: *"Düzeltilen şey kapı değil, kapı
  hakkındaki KAYIT: `internal/arch` başka bir bütçe ve elle tutulmadı."* Bugün
  tutuldu.

  **Yazılanlar kümesi artık TABLOYA bağlı.** Bir `INSERT`'ün sütun listesi
  yazdığı tabloya, bir `SET`'in atamaları güncellediği tabloya, `ON CONFLICT DO
  UPDATE SET` ise asıldığı `INSERT`'ün tablosuna ait. **Bildirimler ise artık
  YENİDEN OYNATILIYOR:** migrasyonlar dosya sırasına göre uygulanıyor —
  `CREATE TABLE`, `ALTER TABLE ... ADD COLUMN`, `DROP COLUMN`, `DROP TABLE` —
  yani denetim ilk migrasyonun anlattığı şemayı değil, veritabanının vardığı
  şemayı görüyor. Görünen sütun sayısı 715'ten 718'e çıktı: o güne dek `ALTER`
  ile eklenmiş dört sütun, eksi düşürülen bir tanesi
  (`order_exchanges.completed_at`). Sıra varsayımını koruyan ucuz bir denetim de
  eklendi: migrasyon önekleri aynı genişlikte doldurulmazsa `10_` ile
  `000009_` yan yana geldiğinde `ALTER` dizisi sessizce yanlış sırada uygulanır.

  **Oynatmanın modellemediği iki `ALTER` eylemi ATLANMIYOR, BULGU olarak
  bildiriliyor:** `RENAME COLUMN` ve sütunu bu denetimin kapsamından tümüyle
  çıkaran `ALTER COLUMN ... SET DEFAULT`. Anlamadığı şeyi atlamak, bu kapının
  kendi karşı örneğini belgesinde taşır hâle gelmesinin tam olarak yolu; artık
  yüksek sesle düşüyor ve öğretilmeyi istiyor.

  **İki düzeltme de mutasyonla ve HER İKİ kapı sürümüne karşı kanıtlandı.**
  `ALTER` ile eklenmiş yazılmamış bir sütun, ve adı KARDEŞ tabloda yazılan bir
  sütun: ikisi de eski kapıda yeşil kalıyor, yenisinde adıyla düşüyor. Yalnızca
  yeni kapının düştüğünü göstermek dünkü kapı hakkında hiçbir şey kanıtlamazdı.

  **Kimsenin listesinde olmayan dördüncü kör nokta.** `CREATE TABLE` okuyucusu
  bir sütunu DOKUZ TİPLİK bir izin listesinden tanıyor ve tablo gövdesini SATIRA
  göre bölüyordu. Başka bir tiple bildirilen sütun — `uuid`, `date`, `varchar`,
  `interval` — sonsuza kadar denetim dışı kalırdı, ve çok satırlı bir CHECK
  kısıtı sütun görünümlü yedi satır üretiyordu; bunların yanlış bulguya
  dönüşmemesi yalnızca baş sözcüklerinin tip listesinde olmamasına bağlıydı.
  Artık yedi tablo kısıtı anahtar sözcüğünden oluşan KAPALI bir ret listesi ve
  derinlik gözeten virgül ayırma var: bilinmeyen bir tip artık bir sütun, bir
  kör nokta değil. İki pozitif kontrol hem okumayı hem kararı sabitliyor ve
  ekilen durumların yarısı REDDEDİŞ: `FOR UPDATE` hiçbir şey yazmaz, sütun
  listesi olmayan bir `INSERT` hiçbir ad vermez, yorum içindeki SQL düzyazıdır,
  dizge içindeki SQL veridir.

  **Üçüncü kör nokta açık, ve bu artık bir eksiklik değil bir ÖLÇÜM.**
  `internal/modules` altındaki 445 sqlc sorgusundan 9'unun `internal` ya da
  `core` içinde elle yazılmış hiçbir çağıranı yok — ve dokuzu da `SELECT`. Yani
  metni okuyup çağrı çizgesini okumamak bugün TAM OLARAK SIFIR sütun maskeliyor:
  deponun her `INSERT`'ü ve her `UPDATE`'i çağrılıyor. Ucuz düzeltme — "yazan
  bir deyimin elle yazılmış bir çağıranı olsun" — sınırı bir adım öteye taşıyıp
  duruyor, çünkü çağıranın kendisi ulaşılamaz olabilir; D17 tam olarak o
  biçimdeydi. Dürüst kapatma, rotalardan, işlerden ve akışlardan başlayan bir
  erişilebilirlik çözümlemesi demek — `internal` ile `core`'un 317 bin satırı
  üzerinde — ve `golang.org/x/tools` bugün yalnızca dolaylı bir bağımlılık.
  Ölçüm kapının kendi godoc'unda duruyor, yani kapı artık yaptığından fazlasını
  iddia etmiyor.

  Ölçerken bir de kendi hatası yakalandı ve gönderilen kapıyı o belirledi: ilk
  probun `UPDATE` eşleştiricisi migrasyon düzyazısındaki sözcüğü sayıp
  `orders.archived_at`'ı yanlışlıkla maskelenmiş bildirdi. Bu migrasyonlar
  SQL'den çok tartışma taşıyor ve `UPDATE` sözcüğü yorumlarda deyimlerden daha
  sık geçiyor; kapı bu yüzden paketin var olan SQL sökücüsünü kullanıyor,
  yorumları ve dizgeleri körleştirdikten sonra.

- **Denetim düzeltilir düzeltilmez görünen dokuz sütun: hiçbir şeyin yazmadığı
  dokuz `deleted_at`** (D18).

  Dokuzu da aynı biçimde saklanmıştı: modül tablolarının BİR KISMINI yumuşak
  siliyor, o yazmalar çıplak ad eşlemesi altında bu tablonun sütununu da
  örtüyor, ve kapı hiçbir satırında dolu olmamış bir sütunda yeşil kalıyordu.

  | modül | tablolar |
  | --- | --- |
  | product | product_category, product_collection, product_tag, product_option_value |
  | region | country, currency |
  | inventory | stock_locations, inventory_reservations |
  | fulfillment | fulfillments |

  Maliyetleri saklama alanı değil: bu tabloların her okuması, hiçbir zaman
  yanlış olmamış bir `deleted_at IS NULL` yüklemini taşıyor — sipariş ve ödeme
  için D9'un kaydettiği bulgunun aynısı, tek farkla ve fark tam olarak burada:
  D9'daki on sütun bir KARAR, bu dokuz sütunda ise karar verilmiş hiçbir şey
  yok.

  **Muafiyet girdileri karar değil, KAPATILMAMIŞ İŞİN YER TUTUCUSU — ve kapı
  bunu girdinin kendisinde söylüyor.** Dokuzunun her biri "UNCLOSED FINDING, not
  a decision" sözleriyle başlıyor ve neyin cevaplanmadığını yazıyor: bir
  kategori hiç silinebilmeli mi, bir stok konumunu kapatmak silme mi durum mu,
  `SetReservationStatus` ile serbest bırakılan bir rezervasyonun durumunun zaten
  söylediği şeyi ikinci kez söylemesi gerekiyor mu, ve satırları silinmek yerine
  yeniden yönlendirilen ülke ile para birimi listeleri bu sütunu hiç taşımalı
  mı. Dört modülde hiçbir şey değiştirilmedi: şema değişikliği, soruyu soran
  denetimin değil cevaplayanın işi.

- **Bir sütunun yazılmadığını yakalamak için kurulmuş denetim, godoc'unda ÖRNEK
  olarak verdiği bulguyu hiç yakalamamıştı — üç kör noktası da mutasyonla
  ölçüldü** (gaps.md D16).

  D4 kapatılırken çıktı ve bu turun en pahalı bulgusu bu, çünkü ev kurallarından
  biri doğrudan bu kapıya yaslanıyor: "bir sütun eklemek onu yazmayı zorunlu
  kılar". `TestEveryColumnIsWrittenBySomething` oturumun başında YEŞİLDİ —
  `order_exchanges.completed_at` ile `canceled_at` kanıtlanabilir biçimde hiç
  yazılmıyorken ve muafiyet listesinde de değilken.
  `internal/arch/columns_test.go`'nun kendi godoc'u, kusurun neye benzediğini
  anlattığı paragrafta tam olarak bu iki sütunu örnek gösteriyor. Bunu geçerken
  söylüyordu.

  **Birinci kör nokta: yazılanlar kümesi TABLOYA göre değil, modül genelinde
  çıplak sütun ADINA göre tutuluyor.** `completed_at` yazılmış sayılıyordu çünkü
  `CompleteOrderClaim` onu BAŞKA bir tabloda yazıyor; `canceled_at` sayılıyordu
  çünkü üç ayrı sorgu yazıyor. Zaman damgası adları tasarımı gereği tekrar eder,
  yani aynı körlük tekrar eden ada sahip her modülde var.

  **İkinci kör nokta: bildirim taraması yalnızca `CREATE TABLE` gövdesini
  okuyor.** `ALTER TABLE ... ADD COLUMN` ile eklenen bir sütun — yani ilk
  migrasyondan SONRAKİ her migrasyonun sütun ekleme biçimi — taramaya hiç
  görünmüyor. İki yönlü mutasyonla kanıtlandı: `ALTER` ile eklenen yazılmamış
  bir sütunda kapı yeşil kalıyor, aynı sütun ilk `CREATE TABLE` gövdesine
  taşındığında kapı doğru mesajla düşüyor. Yani ev kuralı ilk şema için geçerli,
  o günden beri eklenen hiçbir şey için değil — aynı turda eklenen
  `orders.archived_at` dâhil.

  **Üçüncü kör nokta: `.sql` METNİ okunuyor, çağrı çizgesi değil.** Hiçbir rotanın
  ya da akışın çağırmadığı bir yazma sorgusu kapıyı yeşile çevirir, oysa hiçbir
  operatör o yazmayı üretemez. D4'ün iptali servis metodu olarak değil ROTAYA
  BAĞLI olarak gönderildi, sebebi tam olarak bu.

  **Kayıtlı üçüncü seçenek MEKANİK OLARAK İMKÂNSIZMIŞ.** D4 için yazılı duran
  "ikisini gerekçesiyle muafiyet listesine kaydet" yolu işlemiyor: muafiyet
  yalnızca yazılanlar kümesi nöbetçisinden SONRA bakılıyor ve testin kapanış
  döngüsü her muafiyet anahtarının gerçekten yazılmamış bulunduğunu doğruluyor —
  giriş "artık yazılmamış DEĞİL" diye düşerdi.

  Düzeltilen şey kapı değil, kapı hakkındaki KAYIT: `internal/arch` başka bir
  bütçe ve elle tutulmadı. Aynı sınıftan ikinci bir örnek de aynı gün ölçüldü ve
  açık bırakıldı (D17): `CancelReturn` ile `CancelClaim` üretimde hiçbir yerden
  çağrılmıyor — ne rota, ne akış, ne interop — yani `order_returns.canceled_at`
  ile `order_claims.canceled_at` üretimde tam olarak takas sütunu kadar yazılmış
  durumda. Kapı onlarda yeşil, çünkü `UPDATE` metni bir `.sql` dosyasında duruyor.

- **Verilmeyen bir ölçüt artık HİÇ YAN TÜMCE YAZMIYOR — ve kategori süzgecinin
  gerçek maliyeti ilk kez ÖLÇÜLDÜ.**

  Düne kadar listelemenin her isteğe bağlı ölçütü `($n IS NULL OR <yan tümce>)`
  olarak yazılıyordu: istek ne taşırsa taşısın tek sabit metin. İyi okunuyordu ve
  tam da önemli olan yerde yanlıştı. Bir ayrık altındaki alt sorgu yarı-birleşime
  YÜKSELTİLEMEZ, yani `product` her planın dış ilişkisi olarak kalıyor ve
  taksonomi haritası ancak tam bir taramaya asılı alt plan olabiliyordu.

  **Dünkü ölçüm bunu söyleyemezdi, çünkü düzenekte tek bir kategori satırı bile
  yoktu** — o yüzden dünkü rakamlar TABAN olarak yazılmıştı. Düzenek artık
  depodan kuruluyor (D13) ve varsayılan biçimi yirmi kategori ile yirmi etiket
  taşıyor, yani rakamlar var. Ve rakamlar dünkü iki cümleyi düşürdü: *"kategori
  taramayı hiç daraltmaz"* YANLIŞ — 52.004 satırın 49.404'ü, kanal alt sorgusu
  hiç koşmadan önce eleniyor — ve *"kategori indeksine erişilemez"* yalnızca
  GENEL plan altında doğru. OR'lu gövde her çağrıda sıfırdan planlandığı için
  planlayıcı sabiti görüyor, ayrığı katlıyor ve indekse hash'lenmiş bir alt planla
  ERİŞİYOR (`loops=1`, 4 buffer). Yani indeks, deyim ASLA önbelleğe
  alınamadığı için erişilebilirdi.

  Ölçüm, yeniden kurulmuş düzenek (52.004 ürün, 52.000 kanal ataması), harf
  katlamasını geçen bir küme, `plan_cache_mode` varsayılan `auto` — yani pgx'in
  aldığı ayar — ve her satır 9-11 ısınmış koşumun medyanı:

  | sorgu | OR'lu gövde | koşullu gövde | oran |
  | --- | --- | --- | --- |
  | ölçüt yok, sayım +kanal | 78,0 ms / 156.743 | 76,3 ms / 156.743 | 1,0 |
  | ölçüt yok, liste +kanal | 0,10 ms / 68 | 0,08 ms / 68 | 1,0 |
  | kategori %5, sayım | 13,2 ms / 1.117 | 8,0 ms / 1.117 | 1,6 |
  | kategori %5, sayım +kanal | 20,2 ms / 8.918 | 10,3 ms / 18.588 | 1,9 |
  | kategori %5, liste | 0,90 ms / 473 | 0,38 ms / 1.272 | 2,4 |
  | kategori %5, liste +kanal | 1,29 ms / 534 | 0,93 ms / 2.458 | 1,4 |
  | 26 ürünlük bitişik kategori, sayım +kanal | 12,5 ms / 812 | 0,16 ms / 186 | 80 |
  | 27 ürünlük yayık kategori, sayım +kanal | 163,5 ms / 156.746 | 0,28 ms / 193 | 586 |
  | 27 ürünlük yayık kategori, liste +kanal | 52,7 ms / 122.033 | 0,28 ms / 193 | 188 |

  **İlk üç satır bu değişikliğin bir takas OLMADIĞINI söylüyor:** vitrinin en sık
  sunduğu istek biçiminde — hiç taksonomi ölçütü yokken — iki gövde aynı planı,
  aynı buffer sayısını ve aynı milisaniyeyi veriyor. Tablonun geri kalanını almak
  için hiçbir şey verilmedi.

  **Yedinci ile sekizinci satır ise bir sayı değil, bir YAZI TURA.** Aynı deyim, aynı veri,
  yalnızca kategori kimliği değişik: 26 ürünü listeleme sırasında BİTİŞİK olan
  kategori 12,5 ms, 27 ürünü YAYIK olan 163,5 ms. İki plan da yasal, ikisi de tek
  bir deyimden çıkıyor ve hangisinin seçileceğine istatistikler karar veriyor —
  yani OR'lu gövdenin maliyeti bütçelenebilir bir rakam değildi. Ters yöne düşen
  planda korelasyonlu kanal alt sorgusu, 26 satır döndürmek için 52.004 kez
  koşuyor.

  **Bu değişikliğin gerçekten riske attığı tek şey ölçüldü, akıl yürütülmedi.**
  OR'lu gövde PostgreSQL'in genel plan tuzağından KORUNMUŞTU — ve tam da yavaş
  olmasının sebebiyle: yedi ölçütün hepsi canlı olabildiği için genel planı
  695.113'e mal oluyor, özel planları 232.000 civarı, `auto` da genel planı asla
  benimsemiyordu (`pg_prepared_statements`: 60 koşumda 0 genel / 60 özel). Ucuzlayan
  bir deyimin genel planı benimsenebilir ve genel plan hangi kategorinin
  sorulduğunu göremez. Sayım gerçekten geçiyor (25 genel / 5 özel) ve hiçbir şey
  kaybetmiyor: %5 kategoride 9,8 ms genel, 10,3 ms özel. Liste denenen dört
  biçimin hiçbirinde geçmiyor — ama bu bir garanti değil bir maliyet
  karşılaştırması, o yüzden godoc bunu saptayan tek sorguyu adıyla yazıyor.

  Ödenen üç bedel de yazıldı, hiçbiri sıfır değil: parametre numaraları artık
  sabit değil (gövde ile argümanları TEK ÇİFT olarak dönüyor, ki listeleme ile
  sayım numaralandırmada ayrışamasın), hazırlanmış deyim yeniden kullanımı ölçüt
  BİLEŞİMİ başına bir metin üretiyor (tavan 128, pgx'in önbelleği 512) ve
  `grep` artık deyimin tamamını hiçbir zaman basmayacak. Tam kayıt:
  `internal/modules/product/repository/saleschannel.go` ve
  `docs/catalog-search-cost.md`.

  **Bunu ödeyen şey ÇARPIKLIK, ve düzenekte çarpıklık yok.** Düzeneğin yirmi
  kategorisinin her biri tam olarak 2.600 ürün tutuyor (kataloğun %5,0'i,
  üreticinin kuralı gereği) ve o biçimde bu arızanın tamamı görünmez. Aynı
  cümlenin öteki yüzü şu: bu kusur, deponun ölçebildiği TEK katalogda
  görünmüyordu. Seçici kategoriler elle, geçici bir veritabanında kuruldu ve
  onunla birlikte gitti — `rig.Spec`'te çarpıklık seçeneği yok (G).

- **Ürün modülünün sekiz kayıtlı performans rakamı yeniden ölçüldü; BEŞİ yanlış
  çıktı** (D15).

  D13'ün ilk faturası bu. Bir rakamı yeniden kurulabilir bir düzeneğe bağlamak
  onu yanlışlanabilir yapar — ve kümenin ilk yeniden koşumu, çoğunluğunun
  kaydığını buldu. Yeniden üretilen üçünün üçü de YAPISAL (plan düğümü, koşum
  sayısı, elenen satır); düşen beşinin beşi de milisaniye.

  **Birebir yeniden üretilenler:** sayımın kayıtlı planı (52.004 satır, 52.004
  alt plan koşumu, Heap Fetches 0, 156.743 buffer'ın 156.013'ü alt sorgunun) ve
  reddedilen `OR IS NULL` imleç sınırının `Rows Removed by Filter: 50001`
  değeri. İkisinde de yalnızca milisaniye makineye bağlı, iddiaya değil.

  **Yönü doğru, iki büyüklüğü de yanlış:** görünürlük kuralının biçimini savunan
  "iki EXISTS 26,8 ms, tek `bool_or` 0,8 ms" çifti. MEKANİZMA birebir yeniden
  üretiliyor — eski biçimin iki alt bağlantısı da ilk satır dönmeden önce
  bağlantı tablosunu baştan sona tarıyor — ama ölçülen 21,4 ms'ye karşı 0,12 ms.
  En olası okuma, iki yarının aynı saatten okunmamış olması: 0,8 ms bir istemci
  gidiş-dönüşünün, 26,8 ms bir `EXPLAIN`'in biçimini taşıyor. **Bir
  karşılaştırmanın iki yarısı da aynı saatten gelmek zorundadır** — 0,8 ms'nin
  aylarca 0,12 ms'lik bir sorgunun yanında durabilmesinin sebebi bu.

  **Düpedüz yanlış çıkan dördü ve yanlarında yeniden üretilen biri**, sayımın
  neden hash birleşimi olarak yeniden yazılmadığını savunan blokta. Tutan: iki
  EXISTS'in 43-54 ms bandı, tavanında ölçüldü. Tutmayanlar: hash biçiminin bandı
  33-45 ms yazıyordu, 42-48 ölçüldü; "süzgeçsizken iki kat hızlı" 1,35 ile 1,6
  kat arası; "sabit ~30 ms taban" ~39 ms; ve seçici süzgeçteki "13,8 ms'ye karşı
  30,0 ms" çifti 20,3'e karşı 39,0. **SAV dördünü de atlatıyor** ve düzeltme
  olmalarının sebebi bu: iddia mutlak rakamlar değil, takasın YÖN DEĞİŞTİRMESİYDİ
  — hash biçimi hiçbir şey süzülmezken hızlı, bir ölçüt seçici olur olmaz yavaş
  — ve hâlâ öyle.

  **Neden denetlenemez olduklarını da yazmak, sınıfın geri gelmemesi için:**
  sekizinin hiçbiri tarih, küme ya da `plan_cache_mode` ayarı taşımıyordu ve
  alındıkları veritabanı yeniden kurulamıyordu (D13). İkisi ayrıca C yerel ayarlı,
  yani `core/db/casefold.go` probunu GEÇEMEYEN bir kümede alınmıştı — orada büyük
  bir Türkçe harf kendi küçüğüyle `ILIKE` eşleşmiyor, yani o rakamlar öyle bir
  harf taşıyan hiçbir kelimeyi bulamayan bir aramayı tarif ediyor. Aynı çıplak
  `ILIKE` sayımı orada 8,7 ms, katlamanın çalıştığı kümede 14,5 ms — 1,66 kat.
  Düzeltmeler kaynağında, eski rakam SİLİNMEDEN üstü çizilerek yapıldı; her
  yenisinin yanında tezgâh ve plan önbelleği ayarı yazıyor.

- **`make load-test` BOŞ bir katalog ölçüyordu** (D14) — ve bu, D11'in bir kat
  altı.

  D11 hedefin testi yeniden KOŞMASINI sağladı; bu, koşan testin ne yaptığı.
  Koşum ortamı bölgeleri, vergi verilerini, bir kimliği ve bir stok lokasyonunu
  yaratıyor ve TEK BİR ÜRÜN yaratmıyor; hedef de yalnızca bu testi seçtiği için
  başka hiçbir dosyanın verisi koşmuyor. Sonuç: vitrin listelemesi boş sayfa
  döndürüyor, sayım hiçbir şey saymıyor ve hedef yeşil bir istek/saniye satırı
  basıyor. Sınıf D11'inkiyle aynı — hiçbir şey görmeyen bir denetim ile geçen
  bir denetim birbirinden ayırt edilemez — ama boşluk VERİDE, yani hiçbir
  seçici kapısının bakamayacağı yerde.

  Test artık ölçtüğü şeyi kendisi kuruyor: düzeneği yeniden kuran üretecin aynısıyla
  200 ürün, kendi mintlediği bir satış kanalına atanmış (yük verisi paketteki
  diğer vitrin senaryolarına sızmasın diye), ve gövdesinde ürün taşımayan bir 200
  artık BAŞARISIZLIK sayılıyor (`internal/e2e/load_test.go`).

  Paylaşım tek yönlü bir kolaylık değil: seed eden SQL başka modüllerin
  tablolarını adlandırıyor ve iki SQL kapısı da yalnızca `internal/modules`
  altını okuyor (D10), yani o SQL'i hiçbir mimari kapı görmüyor. Bir
  migration'ın yeniden adlandırdığı sütun BURADA, adlandıran commit'te düşüyor —
  bir yıl sonra düzeneği kurmayı deneyenin karşısında değil.

- **`make load-test` hiçbir şey ölçmüyordu — ve yeşil görünüyordu.**

  Reçetedeki seçici `-run TestTemelYukAltindaDogruKalir` idi ve o ad depoda
  HİÇBİR YERDE yok: test `TestStaysCorrectUnderBaselineLoad` olarak çevrilmişti,
  Makefile o değişikliğe dahil edilmemişti. Arıza bir yazım hatası değil,
  `go test`'in sözleşmesi: hiçbir şeyle eşleşmeyen bir seçiciye verdiği cevap
  `ok ... [no tests to run]` ve **çıkış kodu 0**. Yani hedef ryuk ve postgres
  konteynerlerini ayağa kaldırıyor, saniyeler harcıyor, "ok" basıyor ve
  ÖLÇMÜYOR. Düzeltmeden önce birebir yeniden üretildi.

  Bu sınıfın kötülüğü sessizliği değil, YANLIŞ GÜVEN vermesi: yük testi
  koşulmadığı için değil, koştuğu SANILDIĞI için işe yaramıyordu. Kapısı da
  aynı turda geldi (bkz. Eklendi).

  Ortam anahtarları ayrıca kontrol edildi ve onlar sağlamdı:
  `GOBIT_LOAD_REQUESTS` ve `GOBIT_LOAD_CONCURRENCY`, `internal/e2e/load_test.go`
  içinde Makefile'ın ihraç ettiği adların aynısı. Çeviri turunda düşen tek şey
  test ADIYDI.

- **`docs/gaps.md`: B2'nin kalan dört filtresi tek bir iş DEĞİLMİŞ — ölçüldü ve
  bölündü.**

  Satır "hâlâ eksik: fiyat, stokta, seçenek değeri, sıralama" diyordu ve dördünü
  aynı cinsten tek bir boşluk gibi listeliyordu. Ölçüm dördü ikiye ayırdı:
  **ikisi inşaat, ikisi karar.**

  - **SIRALAMA** gerçek bir inşaat ve tek düz olanı. Listelemenin `ORDER BY`'ı
    derleme zamanı sabiti (`created_at DESC, id DESC`) ve iki yüzeyin de
    sıralama argümanı yok. Ürün modülünün kendi tablolarından cevaplanabilenler:
    `created_at` (indeksli, zaten kullanımda), `handle` (indeksli), `title`
    (İNDEKSSİZ). Fiyat, stok ve popülerlik sıralamasının yolu YOK — popülerliğin
    indeksi değil TABLOSU yok. **Tuzak imleçte:** keyset imleci yalnızca
    (listeleme adı, zaman, kimlik) taşıyor ve başka bir sıralama altında TEMİZ
    ÇÖZÜLÜR — listeleme adı tutar, zaman ve kimlik geçerlidir, sorgu yanlış
    sıradan sayfalamaya devam eder. Düşmez, makul satırlar döndürür. Sıralama
    ile imleç tek karardır.
  - **SEÇENEK DEĞERİ** bir kararın arkasındaki inşaat. Beş tablonun hepsi ürün
    modülünün içinde, yani ADR 0001 engellemiyor. Ama bir seçenek DEĞERİ
    kimliği, NOT NULL yabancı anahtarlar yüzünden tam olarak bir ürüne çözülüyor
    — kimliğe göre süzmek tanım gereği en fazla bir ürün döndürürdü. Süzgeç
    (seçenek başlığı, değer) METİN çiftinde olmak zorunda ve hiçbir indeks bu
    iki sütunun biriyle başlamıyor. Sözlük ucu da yok: istemcinin ihtiyacı
    DISTINCT metin çiftleri ve bunu bugün hiçbir sorgu, repository metodu ya da
    servis metodu döndürmüyor. Üstelik kategori ve etiketin hiç karşılaşmadığı
    bir soru var — ÇOK DEĞERLİ süzme (bir seçenek içinde VEYA, seçenekler arası
    VE); ikisi de eksen başına tek skalerle geldiği için o soru hiç sorulmadı.
  - **FİYAT bir KARAR, inşaat değil** (artık A16). Bir ürünün fiyatı yoktur:
    fiyat bir `price_set`'e, set bir link üzerinden VARYANTA aittir ve tek set
    birçok satır tutar (para birimi × adet kademesi × fiyat listesi). "Fiyat",
    (para birimi, adet, kural nitelikleri, an) girdisinden beş sıralı eşitlik
    bozucuyla seçilen bir FONKSİYON, bir sütun değil. Vitrin bunu daha da
    belirsiz yapıyor: fiyatlar sayfa `LIMIT`/`OFFSET` ile KESİLDİKTEN SONRA
    ikinci bir gidiş dönüşle çekiliyor ve dönen şey her para biriminde yalnızca
    KOŞULSUZ alt küme — para birimi, bölge, müşteri grubu girdisi yok. Yani
    süzülecek "o tutar" sayfada bile yok. Kataloğa denormalize etmenin
    geçersizleştirme sinyali YOK: pricing modülü hiç olay yayımlamıyor.
  - **STOKTA da bir KARAR** (artık A17). Bir ÜRÜN için "stokta" bu depoda
    hiçbir yerde tanımlı değil; uygunluk iki granülerlikte tanımlı ve ikisi de
    ürünün ALTINDA. Ürün modülü link tablosunu okuyabildiği için SQL'i "envanter
    kalemi var mı"yı cevaplayabilir, "stok var mı"yı cevaplayamaz — adet
    `inventory_levels` içinde, envanter modülünün kendi tablosunda. Bölgeye
    doğru bir cevap ÜÇ MODÜLLÜK soru: `stock_locations` bölge sütunu hiç
    taşımıyor ve bölge→lokasyon haritası `shipping_location_regions`, yani
    FULFILLMENT'ta. Denormalize stok sütunu yok, olaya dayalı yol ise B7 ile
    kapalı.

  **İnşa edilmiş yarı panele ULAŞMAMIŞ, ve bunu yazılı bir yanlış saklıyordu.**
  Panel vitrin listelemesini hiç okumuyor; okuma katmanının `product`
  sağlayıcısını okuyor ve o sağlayıcı `status`, `handle`, `collection_id` ve
  `id`/`ids` kabul ediyor — `category_id` ve `tag_id` DEĞİL. Yani okuma
  katmanının ürün yüzeyi, B2'nin genişlettiği REST ve GraphQL yüzeylerinin
  ARKASINDA kaldı ve görünür sonucu şu: **dükkânın müşterisi kataloğu kategoriye
  göre daraltabiliyor, dükkânın operatörü daraltamıyor.** Bunun imkânsız olduğu
  `internal/adminui/catalog.go` içinde godoc olarak yazılıydı — "vitrin
  listelemesinin kullandığı AYNI Graph çağrısı, o yüzden ekran ayrışamaz". İkisi
  hiçbir zaman aynı çağrı olmadı. Yorum düzeltildi; kod değişmedi.

  Son olarak, açıkça yazıldı: `docs/gaps.md` B2'nin tüketicisi olarak C10'u
  (doğal dil arama) gösteriyor ve **C10 kodda yok**. Yani 2026-09-05'te inşa
  edilen süzgeçlerin gerçek bir adlandırılmış tüketicisi henüz yok — bu deponun
  kuralı da tam olarak bu: tüketicisi olmayan bir yetenek, yapıldığı SANILAN
  iştir.

- **`docs/gaps.md`: okuma önbelleği maddesinin dayanağı ÖLÇÜMLE çürüdü.**

  pgbench ile yük fikstürüne karşı (52.004 ürün, 16 istemci, bu makinede):

  | sorgu | gecikme | verim |
  | --- | --- | --- |
  | vitrin listesi, 20 satır | 0,47 ms | 33.830 /s |
  | handle ile tek ürün | 0,36 ms | 44.976 /s |
  | katalog `count(*)` | 3,03 ms | 5.273 /s |

  Buna karşılık bir vitrin GraphQL isteğinin GO tarafı 374 µs ve 8.421 tahsisat
  — çekirdek başına yaklaşık 2.700 istek/s. **Veritabanı, cevabını biçimleyen
  koddan kabaca on iki kat hızlı.** Okuma önbelleği zaten önde olan tarafı
  rahatlatırdı.

  Pahalı olan tek okuma sayım sorgusu ve o zaten isteğe bağlı yapılmıştı —
  maddenin işaret ettiği şey buydu, ama tartacak verim rakamı olmadan.

  Maddeyi yeniden açacak koşullar yazıldı: listeleme indeks taramasının yirmi
  satırlık olmaktan çıktığı bir katalog, bir isteği çok sorguya çeviren bir
  zenginleştirme ayağı, ya da veritabanının bir ağ atlaması uzakta olduğu bir
  kurulum. Bugünün şekli bunların hiçbiri değil.

  (Bir önbellek gözden kaçmıştı ve eklendi: GraphQL ucu AYRIŞTIRILMIŞ BELGELERİ
  kendi kabul kuralıyla önbelleğe alıyor. Sorguyu önbelleğe alıyor, veriyi
  değil.)

- **`docs/gaps.md`: "yönetici oturumu iptal edilemiyor" maddesi YANLIŞTI.**

  Karar yazılmış ve iptal çalışıyor. `auth/service/session.go` tam olarak
  anlatıyor: bilerek oturum kaydı yok, bunun yerine her kimlik bir OTURUM
  ÇAPASI taşıyor ve çapadan önce üretilen token reddediliyor. `AuthenticateAdmin`
  her doğrulamada çapayı okuyor; çıkış ya da parola değişimi kullanıcının
  token'larını ANINDA, her cihazda düşürüyor — imza anahtarına dokunmadan. Altı
  test kapsıyor.

  Yani yönetici oturumu iptal edilebilir ve "iptal edecek bir şey yok" iddiası
  yanlıştı. Yok olan şey CİHAZ BAŞINA iptal ve o bir gözden kaçma değil, yazılı
  bir ret: token'a `jti` ve her isteğin okuduğu bir kara liste gerektirir, yani
  kimsenin istemediği bir yetenek için durumsuzluktan vazgeçmek.

  Önceki okuma bir oturum TABLOSU aradı, bulamadı ve mekanizma yok sandı;
  mekanizma zaten var olan bir satırdaki zaman damgası.

- **`docs/gaps.md`: misafir sepeti devralınamıyor maddesi YANLIŞTI.**

  Devralma var ve korumalı: `UpdateCart` misafir sepetine `customer_id` yazıyor,
  yanındaki kural ise SAHİPLİ bir sepetin başka müşteriye devrini reddediyor
  (`cart_customer_mismatch`); entegrasyon testi iki yönü de kapsıyor,
  reddedilen devrin hiçbir şey yazmadığı dahil. Önceki okumanın kaçırdığı şey
  yeteneğin adında "claim" ya da "adopt" geçen bir metot değil, güncellemenin
  bir ALANI olması — arama tam da onu aramıştı.

  Gerçekten eksik olan daha dar: misafir sepetini müşterinin ZATEN sahip olduğu
  sepetle BİRLEŞTİRMEK. Devralma müşteriye ikinci bir sepet veriyor, ikisini
  tek sepete katlayan bir şey yok. Bu bir politika sorusu (hangi adetler
  kazanır, hangi sepetin promosyonları yaşar), tesisat boşluğu değil; o yüzden
  tahmin edilmeden bırakıldı.

### Eklendi

- **Vitrinin dördüncü sözlük ucu kuruldu — ve tek metin döndüreni o**
  (B2'nin OPTION VALUE yarısının ön koşulu).

  `GET /store/v1/option-values`, kataloğun sunduğu **ayrık (seçenek başlığı,
  değer)** çiftlerini döndürüyor. Kimlik döndürmemesi yapısal: `product_option`
  bir ürüne, `product_option_value` bir seçeneğe ait — ikisi de NOT NULL — yani
  bir seçenek-değeri kimliği tek bir ürünün tek bir değerini adlandırır ve
  onunla katalog süzmek en çok bir ürün döndürür. Filtrenin ihtiyacı metin çifti.
  Aynı çifti sunan iki ürün veritabanında iki satır, burada **bir** girdi.

  **Kapsam bir incelik değil, güvenlik cümlesi.** Sözlüğün her girdisi bir ürün
  onu taşıdığı için var; kapsanmamış bir sözlük TASLAK bir ürünün değerini ya da
  çağıranın anahtarı olmayan bir kanalın değerini adlandırırdı — yani
  listelemenin söylemeyi reddettiği şeyi söylerdi. O yüzden listelemenin
  uyguladığı iki koşul, değerin astığı ürüne aynen uygulanıyor. Yumuşak silme
  koruması üç düzeyde birden: seçeneği silmek bir cascade değil, ve silinmiş bir
  seçeneğin değeri aksi hâlde sözlükte kalırdı.

  **Sahte depo gerçeğin davranışını taklit ediyor** — ayrıklık, üç düzeyli silme
  koruması ve (başlık, değer) sırası dahil. Aksi hâlde birim testi, veritabanının
  izlemediği bir kuralda yeşil kalırdı; bu deponun bir kez ödediği kusur.

  İki mutasyon: SQL'den `DISTINCT`'i çıkarmak entegrasyon testini düşürüyor,
  `PublicOnly`'nin durum süzgecini kurmaması ise **hem** birim **hem** entegrasyon
  testini düşürüyor.

  Denetimler yine iki kez ısırdı ve ikisi de haklıydı: yeni uç belgelenmeden
  `TestEveryStoreEndpointIsDescribed` geçmedi, ve `OptionValuePair`'i yanlışlıkla
  `Option`'ın godoc'u ile gövdesinin arasına koymuştum — bu turda ikinci kez aynı
  hata.

- **Vitrin kataloğu artık sıralanabiliyor — ve satırın "tek tuzağı" dediği şey,
  bindiği sözleşme tarafından zaten çözülmüştü** (B2'nin SORT yarısı).

  REST'te `sort=newest|oldest`, GraphQL'de bağlı bir `ProductOrder` enum'u.
  Küme KAPALI ve fiyat içinde değil: fiyat bu tablonun kolonu değil, ne anlama
  geldiği de A16'nın sorusu. Değeri okunmayan bir sıra sessizce varsayılana
  düşmüyor, REDDEDİLİYOR — istediği sırayı almayan bir istemci, bunu öyle
  görünen bir katalogdan ayırt edemez.

  **Hiçbir migration gerekmedi.** `product_created_at_idx` zaten
  `(created_at DESC, id DESC)` diye tanımlı ve bir b-tree iki yönde de okunur,
  yani eskiden-yeniye aynı indeksin ters yürütülmesi. Başlık sıralaması bilerek
  yapılmadı: onun için indeks yok, ve ADR 0015'in kaydettiği C-locale kümesinde
  sıra Türkçe için yanlış görünürdü — ölçülmüş bir tehlikeye bedava girilmez.

  **Tuzak buydu ve zaten kapalıydı:** iki sıra AYNI anahtarı ters yönlerde
  yürüdüğü için, birinde üretilmiş bir imleç ötekinin anahtar uzayında da
  geçerli bir konumdur ve sessizce yanlış sayfayı verir. `internal/core/page`
  bunu zaten reddediyor, çünkü imleç ait olduğu listelemenin ADINI taşıyor ve o
  ad dönüşte denetleniyor. Bütün çözüm, sırayı o adın parçası yapmaktı.
  Yeniden-eskiye sırası ÇIPLAK adı koruyor, böylece parametre var olmadan önce
  üretilmiş imleçler hâlâ çözülüyor.

  GraphQL enum'u modülün kendi tipine BAĞLANDI, üretilmiş ikinci bir tipe
  değil — o yüzden değerleri SCREAMING_CASE değil, REST'in yazdığı gibi küçük
  harf. Gerekçe gqlgen.yml'in kendi politikası: ayrı bir model katmanı bir
  dönüştürücü ister, dönüştürücü de ikinci bir tanımdır.

  **Üç mutasyon, ve üçüncüsü katmanların neyi koruduğunu gösteriyor.** Sırayı
  listeleme adından çıkarmak tuzak testini düşürüyor; `oldest`'in yönünü ters
  çevirmek yön-eşleme testini düşürüyor; ve sırayı servisten depoya
  GEÇİRMEMEK bütün SQL testlerini YEŞİL bırakıp yalnızca gerçek veritabanındaki
  testi düşürüyor — entegrasyon yarısı tam olarak bunun için var.

  İki denetim bu turda benim işimi yakaladı ve ikisi de haklıydı: yeni
  parametre belgelenmiş ama handler'ın okuduğu kümeye eklenmemişti, ve
  `keysetSeek` yanlışlıkla `listProductsSQL`'in godoc'u ile gövdesinin arasına
  girmişti. Üçüncüsü belge tarafında yakaladı: paketin adını `internal/`
  öneki olmadan yazmıştım, doğrusu `internal/core/page`. Ayrıca artık yanlış olan bir cümle düzeltildi —
  listelemenin godoc'u "sıra sabittir" diyordu; sabit olan ANAHTAR, yön değil.

- **Yorum modülü: bir müşteri yorum yazabiliyor, ve onaylanana kadar hiçbir
  yerde görünmüyor** (B4). On yedinci modül.

  Vitrin uçları `POST` ve `GET /store/v1/products/{product_id}/reviews` ile
  `GET /store/v1/products/{product_id}/review-summary`; yönetim uçları
  `GET /admin/v1/reviews`, `GET /admin/v1/reviews/{id}` ve
  `POST /admin/v1/reviews/{id}/status`. Altısının da bu değişikliğin İÇİNDE
  tüketicisi var. Görünmezliğin nasıl kurulduğu ve neden bu şekilde kurulduğu
  Kararlar bölümünde; burada ÖLÇÜLENLER var.

  **Ortalama OKUMADA hesaplanıyor, ve bu bir tercih değil bir ölçüm.**
  PostgreSQL 16 üzerinde 20.001 ürüne yayılmış 505.000 yorumluk bir düzenek
  kuruldu ve ürün sayfasının çalıştırdığı toplama ölçüldü. İndekssiz: 33-38 ms,
  tam paralel sıralı tarama, ve maliyet ürünün 19 yorumu da olsa 5.000 yorumu da
  olsa AYNI. Modülün kısmi indeksiyle — onaylı satırlar üzerinde, `rating`
  INCLUDE ile — 19 onaylı yorumda 0,17-0,21 ms, 5.000'de 1,3-2,0 ms, 50.000'de
  9,3 ms. Yirmilik ilk sayfa her boyutta 0,03-0,04 ms, çünkü LIMIT indeks
  taramasını durduruyor. İndeks, 348 MB'lık tabloya karşı 40 MB.

  Geçiş noktası gizlenmedi, yazıldı: maliyet TEK ürünün onaylı yorum sayısıyla
  doğrusal, yani saklanmış bir sayaç ancak tek bir ürüne yüz binlerce yorum
  gelmiş bir dükkâna bir şey satar — ve o milisaniyeleri, yorum yazan her yola
  bir doğruluk borcu yükleyerek satar. Bu, A16'nın fiyatı kataloğa
  denormalize etmeye karşı kaydettiği takasın aynısı, ve eksik parça da aynı:
  geçersiz kılma sinyali. Ortalama YÜZDE BİRLİK tam sayı olarak dönüyor
  (433 = 4,33 yıldız); float olsaydı basılan sayı istemcinin nerede
  yuvarladığına bağlı olurdu, ve yuvarlama kararı onu ifade edebilen tek yere
  ait.

  **Geçiş tablosu dört kenar ve her biri savunuldu:** submitted→approved,
  submitted→rejected, yayından indirmenin tek yolu olarak approved→rejected, ve
  tek onarım olarak rejected→approved — çünkü yazar yeniden gönderemiyor.
  Kendine dönen kenarlar reddedildi ki `moderated_at` bir insanın ne zaman
  baktığı konusunda yalan söylemesin. Şema tarafında üç kısıt gerçek bir
  PostgreSQL'e karşı ısırıyor: yıldızın 1-5 aralığı, statünün üç kelimesi, ve
  `moderated_at` ile statüyü iki yönde birden bağlayan TAM AYNA — fulfillment'ın
  `returned_at`'i ile aynı sebeple ifade edilebiliyor, hiçbir geçiş
  `submitted`'a geri dönmüyor.

  **Reddedilen alternatif kaydedildi: konu ÜRÜN, varyant değil.** Ölçüldü —
  vitrin bir ürünü adresleyebiliyor, varyantı adresleyemiyor: mağaza tarafında
  varyant ucu hiç yok, yani varyant düzeyinde bir yorumun görüneceği bir sayfa
  olmazdı. Kolon çıplak bir metin ve doğrulanmıyor; yabancı anahtar da yok,
  çünkü Prensip 2.2 modül sınırında yasaklıyor ve order modülünün satırı sattığı
  varyantı tam olarak aynı kurala göre saklıyor. Olmayan bir ürünün yorumunu
  vitrinden uzak tutan şey, istenmeyen her yorumu uzak tutan şeyle aynı: onu
  onaylamayan bir operatör.

  **Kapılar iddiayla değil kusurla kanıtlandı.** Üretilen SQL'den onay filtresi
  silindiğinde hem modülün entegrasyon testi hem e2e testi düşüyor; migration'a
  yazarı olmayan bir kolon eklendiğinde `TestEveryColumnIsWrittenBySomething` o
  kolonun adını vererek düşüyor; kompozisyon kökündeki kayıt satırı bir no-op ile
  değiştirildiğinde kayıt kapısı paketi adıyla düşüyor; beşinci bir geçiş kenarı
  açıldığında iki test birden düşüyor. Her mutasyondan önce dosya kopyalandı,
  sonra kopyadan geri yüklendi ve sha256 ile doğrulandı.

  **Yazılmayanlar, gerekçeleriyle.** Okuma katmanı sağlayıcısı, interop yüzeyi ve
  olay: üçü de tüketicisi olmayan sözleşme olurdu — denetim zaten hiçbir üretim
  dosyasının çözmediği bir interop yüzeyini ve hiçbir üretim dosyasının abone
  olmadığı bir konuyu reddediyor — ve ilk okuyucuları C11, yani üçü onunla
  birlikte gelir. `WithTx` benzeri bir işlem makinesi de yok: her yazma tek
  deyim (bir INSERT, bir koşullu UPDATE), yani doğruluğunu kimsenin kontrol
  edemeyeceği bir mekanizma olurdu; dosyada eksiklik gibi durmasın diye
  gerekçesiyle yazıldı.

  **Ve dürüstçe söylenen bir sınır: bu ucun kendine ait bir kotası YOK.**
  Ölçüldü ve hem api paketinin doküman başlığına hem OpenAPI açıklamasına
  yazıldı: `/store/v1` önekinin tamamı TEK bir kota taşıyor, bağlantı adresiyle
  anahtarlanıyor (`TRUSTED_PROXY_HOPS` ayarlanmadıkça X-Forwarded-For hiç
  okunmuyor, varsayılanı 0 — yani herhangi bir vekilin arkasında bütün vitrin
  aynı kovayı paylaşıyor), ve `RATE_LIMIT_PER_MINUTE` sıfır ya da altındaysa
  limitleyici hiç takılmıyor. Bir ürün sayfasını selden koruyan şey limit değil,
  onay adımı.

- **Bir koli GERİ DÖNEBİLİR: `returned` beşinci sevkiyat statüsü, ve tablo artık
  BİLDİRİM ile KOMUT'u ayırıyor** (B10).

  Her Türk kargosu (Yurtiçi, Aras, MNG, PTT) terminal bir "iade" olayı
  bildiriyor: koli teslim edilemedi — alıcı bulunamadı, kabul etmedi ya da adres
  yanlıştı — ve ORİJİNAL irsaliyeyle geri geldi. Bu güne kadar şema dört statü
  kabul ediyordu ve hiçbiri bu olguyu tutamıyordu; bu olayı eşleyecek bir
  eklentinin üç yeri vardı ve üçü de yanlış bir şey yazıyordu. `canceled`
  sevkiyatın olmadığını iddia eder, oysa etiket basıldı ve koli iki yönde de
  yolculuk etti — üstelik modülün kendi anlamıyla, yani SAGA TELAFİSİYLE
  çakışır. `delivered` alıcının koliyi aldığını iddia eder, ki olmayan tam
  olarak budur. Hiçbir şey yazmamak satırı temelli "shipped" bırakır: kendi
  deposunda duran bir koli, yolda diye tarif edilir ve onu oradan
  çıkarabilecek hiçbir olay yoktur.

  **Bu, modülün ZATEN sahip olduğu iade değil, ve fark fiziksel.** Müşterinin
  teslim ALDIKTAN sonra geri göndermesine modülün cevabı duruyor: `is_return`
  işaretli bir kargo seçeneği üzerinde İKİNCİ bir fulfillment — yani bilerek
  satın alınmış ikinci bir irsaliye, ters yöne giden. Buradaki statü BİRİNCİ
  irsaliyenin kötü bitmesiyle ilgili. Birinde iki sevkiyat var, ötekinde bir;
  ikisini birleştirmek ya kimsenin satın almadığı bir sevkiyat icat eder ya da
  gerçek olanı sıkışık bırakır.

  **`returned_at`'in CHECK'i TAM AYNA, ve 000001'in üç damga kısıtı olamazdı.**
  Onlar tek yönlü: statüyü damgasız reddediyorlar, damgayı statüsüz kabul
  ediyorlar. Bu bir gözden kaçma değil, zorunluluk — `shipped_at`, `delivered`
  statüsüne SAĞ ÇIKAR, dolayısıyla ters yön teslim edilmiş her sevkiyat için
  yanlıştır. `returned` TERMİNAL olduğu için farklı: tablodaki hiçbir geçiş onu
  takip etmiyor, yani damga kendi statüsünü aşamaz. Veri migration'ı olmadan
  eklenebilmesinin tek sebebi kolonun YENİ olması; kullanımdaki bir kolona aynı
  kısıt sonradan takılamazdı. Aynı gerekçe order modülünün D4'te yazdığı
  gerekçedir.

  Kolonun aynı değişiklikte yazarı var: `Service.MarkReturned`,
  `POST /admin/v1/fulfillments/{id}/returned` ucuna bağlı — gövdesiz, çünkü uç
  tek bir olguyu bildiriyor ve operatörün onu renklendireceği bir girdi almıyor.
  Statü çeşitlemesi `fulfillment_manual_shipments`'a BİLEREK taşınmadı: o tablo
  taklit edilen dış sistemin defteri ve sağlayıcı sözleşmesi dört statü biliyor,
  `returned` onlardan biri değil. Taklidin CHECK'ini genişletmek, sağlayıcının
  yazamayacağı ve servisin okumayı reddedeceği bir değer üretirdi — yani hiçbir
  yerden erişilemeyen bir durum, bu deponun iki kez temizlediği "canlı görünen
  ölü kod" şekli.

  **Geri alma DÜŞEBİLİR ve bu bir kusur değil, karar.** Herhangi bir satır
  `returned` tutuyorsa daraltılan CHECK reddedilir ve down o deyimde durur. Onları
  önce `canceled` diye yeniden yazmak — böylece geri alma hep başarılı olur —
  reddedildi: bu tablo kargoyla mutabakatın tutulduğu kayıt, ve "biz bu koliyi
  geri çağırdık" cümlesini, kargonun teslim edemeyip iade ettiği bir kolinin
  üstüne, hem de kimsenin yakından izlemediği bir deploy geri alması sırasında
  sessizce yazmak olurdu. Duran bir geri alma kurtarılabilir; satırları yeniden
  yazan bir geri alma kurtarılamaz.

  **Sevkiyat rotasının kalan yarısı ölçüldü ve yapılmadı, ikisi de kaydedildi.**
  `core/provider.QuoteInput` ülke, ağırlık, kalem sayısı ve seçeneğin kendi
  verisini taşıyor, başka hiçbir şey taşımıyor — yani yurt içi kargonun tarifesini
  belirleyen iki sayı, ilçe ve desi, bu sözleşmede ifade edilemiyor; genişletmek
  YAYIMLANMIŞ bir sözleşme kararı (ADR 0026) ve iki şeyin daha kımıldaması
  gerekiyor, çünkü hiçbir adres tablosunda ilçe kolonu yok ve koli ölçüleri
  katalog modülünün olgusu. Ve bir kargonun olayını modüle sokacak bir yüzey de
  yok: modüller arası yazma yüzeyi beş ilkel metot ve hiçbiri sevkiyatı
  kımıldatamıyor. O metot eklentiden ÖNCE eklenmedi, çünkü tüketicisi olmayan
  bir sözleşmeyi hiçbir şey denetleyemez — B13'ün aynı gerekçeyle beklediği gibi.

- **Giden webhook'ların GÖNDERİCİSİ yazıldı — ve bugün KURULAMIYOR; bu
  varsayılmadı, ölçüldü** (C5, D22).

  Altındaki her şey zaten yapılmıştı: outbox olayı, onu vaat eden işlemden sağ
  çıkarıyor; röle başarısız bir yayını tavanlı bir merdivenle yeniden deniyor ve
  vazgeçtiğini ölü mektuba yazıyor (B12); bir eklenti periyodik iş kaydedebiliyor
  (B13). Eksik olan tek şey, yayımlanmış bir olayı gobit'in dışındaki birinin
  alabileceği bir HTTP isteğine çeviren şeydi. `plugins/webhookout` odur.

  **Sağlayıcı yuvası değil, KENDİ MODÜLÜNÜ getiren bir eklenti**, ve gerekçe ADR
  0018'in web push için verdiği cevabın aynısı: bir sağlayıcı iş birimi başına
  SEÇİLİR, oysa giden bir webhook'un ihtiyaç duyduğu şey çerçevenin ZATEN
  TUTUYOR OLMASI gereken durumdur — bir URL, bir gizli anahtar ve bir konu
  kümesi, üçü de herhangi bir olay yola çıkmadan önce bir insan tarafından
  kaydedilmiş. Bildirim sağlayıcısının tek bir adres alanı var ve o bir dizge;
  bir webhook adresi üç değerdir, ikisinin o sözleşmede duracak yeri yoktur.
  Eklenti ayar İSTEMİYOR ve bu da bilinçli: bir alıcının URL'i, konuları ve
  anahtarı ALICIYA aittir, sürece değil — ortam değişkenine yazılan bir alıcı
  deploy olmadan eklenemezdi.

  **Teslim kuyruğu bilerek `event_outbox` DEĞİL, ve reddin gerekçesi şemanın
  kendisi.** O tablo OLAY üzerinde anahtarlı: tek bir deneme sayacı, tek bir
  sonraki deneme anı, tek bir vazgeçme damgası — ve hiç hedef sütunu yok. Üç
  alıcıya borçlu olunan, alıcılardan biri kapalı bir olayın o satırda ifade
  edilebilir hiçbir hâli yok: yayımlandı diye kapatmak başarısızlığı kaybeder,
  beklemede bırakmak 200 dönmüş iki alıcıya aynı olayı yeniden gönderir. Fan-out
  HEDEF BAŞINA bir deneme sayacı ister, o da (alıcı, olay) başına bir satırdır.
  İkinci gerekçe alarmın kendisi: röle işi ölü mektup yığını boş değilken
  koşusunu DÜŞÜRÜYOR ve `gobit jobs` listesine ulaşan kanal budur; webhook
  teslimlerini aynı yığına koymak, üçüncü tarafın kapattığı bir ucu gobit'in
  kendi veri yolunun bir olayı kabul edememesinden ayırt edilemez kılardı — hem
  de operatörün tam bu ikisini ayırmak için okuduğu tek listede.

  **İmza şeması bir alıcının Go okumadan uygulayabileceği biçimde yazıldı.**
  İmzalanan gövde değil: şema sürümü, zaman damgası, teslim kimliği, olay adı,
  deneme sayısı ve ham gövde UZUNLUK ÖNEKLİ olarak birleştiriliyor, üstüne
  HMAC-SHA256. Önek kuralı ADR 0028'in GELEN çağrı halkasının tekrar oynatma
  anahtarını kurarken kullandığı kuralın aynısı, ve sebebi de aynı: onsuz,
  ayıracı içeren bir değer iki farklı mesajı tek bir imzalı dizgeye
  getirebilir — burada bu, bir gövdeyi başka bir olayın imzasının altına taşımak
  demek olurdu. Sayılar da başlıkta GİDEN METİN olarak imzalanıyor, tamsayı
  olarak değil: "03" ile "3" tek bir sayı ama iki ayrı bayt dizisidir, yani
  doğrulamadan önce ayrıştıran taraf imzalanan şeyi çoktan değiştirmiştir.

  **Bir geçiş işlemi TUTMUYOR, kiralıyor.** Röle kendi grubunu
  `FOR UPDATE SKIP LOCKED` ile okuyup yayını o işlemin içinde yapar; orada
  doğrudur, çünkü yayın yerel bir veri yolu çağrısıdır. Burada yanlış olurdu:
  bir deneme, üçüncü tarafa yapılan ve on saniye zaman aşımı olan bir HTTP
  isteğidir, ve bir geçiş boyunca açık tutulan işlem havuz bağlantısıyla
  anlık görüntüyü dakikanın çoğunda tutardı — hem de tam olarak alıcının cevap
  vermeyi bıraktığı, yani geçişin uzadığı anda. İşlem yalnızca istemi kapsıyor,
  `next_attempt_at` ileri itiliyor, istekler elde hiçbir şey tutulmadan
  yapılıyor. Bedeli yazılı: geçiş ortasında ölen bir süreç satırlarını kilitli
  değil KİRALI bırakır ve satırlar kira dolunca kendiliğinden geri gelir.

  **Merdiven rölenin merdiveni DEĞİL ve fark maddenin bütün noktası.** Rölenin
  dört saat üç dakikası YEREL veri yoluna karşı ölçülmüştür ve süreç içi bir
  yayın hatası için fazlasıyla uzundur. Bir webhook ise bir iş günü boyunca
  kapalı kalabilen üçüncü tarafa gider, dört saat ise tek bir gecenin içindedir.
  Buradaki merdiven on üç denemede yirmi altı saat otuz bir dakikayı kapsıyor:
  cuma akşamı bozulan bir alıcıya cumartesi gecesi hâlâ borçluyuz, gerçekten
  ortadan kalkmış bir alıcı ise bir gün ve biraz içinde ölü ilan ediliyor.
  `TestTheRetryWindowOutlastsAReceiverBeingDownForADay` bu pencereyi tutuyor.
  Kod yeniden kullanılamadı ve sebebi tercih değil: rölenin politikası dışa
  açık, ama deneme sayısını gecikmeye çeviren aritmetiği dışa açık DEĞİL, yani
  dışarıdaki bir çağıran politikayı tutabiliyor ve ona hiçbir şey soramıyor —
  yayımlanmış yüzeyi depo içi bir tüketici için genişletmek ise ADR 0026'nın
  reddettiği takas.

  Gövde olayın yükünü olduğu gibi taşıyor ama `customer_id`'yi TAŞIMIYOR, ve
  çıkarma GÖRÜNÜR: alan adı gövdedeki listede duruyor. Gerekçe ADR 0008 —
  `/store/v1/customers/{id}` kimlik doğrulamasızdır, yani bir müşteri kimliği
  tanımlayıcı değil, o müşterinin adı, e-postası ve bütün adresleri için
  hamiline yazılı bir bilettir. Sessiz çıkarma daha kötü olurdu: alıcı, her
  siparişte tetiklenen bir "misafir siparişi" dalı yazardı. Düz http de
  reddediliyor, tek istisna loopback; ana makineyi çözüp özel adresi reddetmek
  ise BİLEREK yapılmadı ve gerekçesi yazıldı — çöz-sonra-bağlan denetimi bir
  denetim değildir, çünkü ad ikinci seferde başka türlü çözülebilir.

  Uçtan uca kanıt gerçek bir sepetle: `TestARealOrderReachesARealWebhookReceiver`
  gerçek checkout akışını, gerçek sipariş modülünü, gerçek veri yolunu,
  eklentinin gerçek PostgreSQL kuyruğunu ve gerçek bir HTTP isteğini birbirine bağlıyor;
  alıcının elinde yönetim ucunun BİR KEZ gösterdiği anahtardan başka bir şey
  yokken imza doğrulanıyor. Eklentinin kendi entegrasyon testi bunu
  kanıtlayamazdı: bir eklenti hiçbir modülü import edemez (ADR 0001), yani orada
  olayı aboneye testin kendisi verir — sipariş modülünün GERÇEKTEN yayımladığı
  olayın, eklentinin GERÇEKTEN aldığı olay olduğu yalnızca burada görülebilir.

  **Ve eksik yarı gizlenmiyor: bu eklenti BUGÜN KURULAMIYOR.** `webhook-out`,
  kompozisyon kökünün eklenti kataloğunda yok; kurulumu belirleyen tek harita o
  olduğu için `PLUGINS=webhook-out` açılışı "bilinmeyen eklenti" ile durduruyor.
  Ölçüm `go list -deps ./cmd/server`: ikilinin bağımlılık kapanışı sekiz eklenti
  sayıyor ve bunu saymıyor — paket ikiliye hiç derlenmiyor, dolayısıyla
  migrasyonu da migrate yüzeyinin dışında kalıyor. Eklentinin kendi paket
  belgesi "kompozisyon kökünün kataloğundaki tek bir satır dışında hiçbir yerde
  adı geçmez" diyor; o satır yok.

  **Bu sınıfı yakalamak için yazılmış kapı ise YEŞİL, ve sebebi bulgunun
  kendisinden daha değerli.** `TestThePluginNamesInTheDocsAreReal`'in ters yönü
  tam olarak "yazılmış ama hiçbir yerde duyurulmamış bir eklenti, tüketicisi
  olmayan bir yetenektir" diye var. Her iki yönü de burada geçiyor, çünkü
  "kayıtlı" adları KATALOGDAN değil, eklentiler ağacındaki `Name` sabitlerini
  ayrıştırarak çıkarıyor. Yani ileri yön "belgedeki bu ad bir yerde tanımlı mı",
  ters yön "tanımlı bu ad belgede geçiyor mu" diye soruyor; ikisi de operatörün
  sorduğu soruyu — ikili bu adı tanıyor mu — sormuyor. Sonuç en keskin hâliyle
  ortada: `.env.example` artık kopyalanabilir bir örnek satır taşıyor ve o satırı
  kopyalamak, kapının kendi godoc'unun önlediğini söylediği açılış hatasını
  üretiyor. İki düzeltme de kaydedildi, yapılmadı: kompozisyon kökünde bir
  harita satırı, ve adlarını kaynak ağacından değil katalogdan alan bir kapı
  (`docs/gaps.md`, C5 ve D22).

- **Hiçbir şeyin hiç yazmadığı dokuz sütun cevaplandı — ve dokuzun TEK bir
  cevabı yokmuş** (D18).

  Sütun denetimi yazılmış kümesini çıplak sütun ADI yerine TABLOYA göre tutmaya
  başladığı gün dokuz canlı bulgu yüzeye çıkmıştı (D16, birinci kör nokta).
  Dokuzu da `deleted_at`, dokuzu da aynı şekilde: modül tablolarının BAZILARINI
  yumuşak siliyor, bu tablosunu silmiyor, ve kardeş tabloların yazması sayesinde
  kapı yeşil kalıyor. O gün hiçbiri karara bağlanmamıştı; muafiyetleri
  "UNCLOSED FINDING, not a decision" diye açılan YER TUTUCULARDI.

  **Sekizi kapandı ve kapanışlar ZIT yönlere gidiyor. Asıl bulgu bu.** Dördünde
  eksik olan YAZMAYDI; dördünde yanlış olan SÜTUNDU. Dokuzunu tek bir parti
  gibi okumak, ya meşru bir silme yolunu yok eden dört şema değişikliği ya da
  hiçbir şeyin ihtiyaç duymadığı dört silme üretirdi.

  **Yazma eksikti: ürün modülü dört silme kazandı**, ve dördünün de yönetim
  ucu var — bağlanmayan bir sorgu, sütun denetimini yeşile döndürüp sütunu tam
  olarak eskisi kadar yazılamaz bırakırdı (D4'ün öğrettiği ders). Koleksiyonun
  silinmesi ürünlerini AYNI İŞLEMDE serbest bırakıyor: ürün koleksiyonunu kendi
  sütunuyla gösterir, o sütunun ON DELETE SET NULL kuralı yumuşak silmeye karşı
  hiç çalışmaz, ve vitrin listesi koleksiyona JOIN yapmadan o kimliğe süzer —
  yani eski bir bağlantıyı izleyen müşteri, satıcının artık göremediği bir
  koleksiyonun ürünlerini görmeye devam ederdi. Kategoride ALT DÜĞÜMÜ olan bir
  düğüm REDDEDİLİYOR: ağaç kökten aşağı yürütüldüğü için yetim kalan çocuklar
  sadece boşta kalmaz, bütün alt ağaç satırları canlıyken her listeden kaybolur.
  İki seçenek gerekçesiyle reddedildi — çocukları büyükanneye bağlamak satıcının
  ağacının şeklini sormadan değiştirir, ebeveynlerini boşaltmak ise alt ağacı
  vitrin menüsünün EN ÜST düzeyine terfi ettirir. Etikette hiçbir koruma yok ve
  fark yazılı: etiket bir yaftadır, katalogda ondan yapılanmış bir şey yoktur ve
  yanlış yazılmış bir yaftayı geri çekmek için ürünleri tek tek etiketten
  çıkarmak istenmez. Seçenek değerinde ise CANLI bir varyantın taşıdığı değer
  reddediliyor: varyant okuması değeri canlılık süzgeciyle birleştirdiğinden
  silme gürültüyle düşmez, varyantı OLDUĞUNDAN AZ seçenekle gösterir — ve
  yalnızca o seçenekte ayrışan iki varyant hem sayfada hem veride birbirinin
  aynısı olur. Seçeneğin kendi silmesi de artık değerlerini aynı işlemde
  damgalıyor; onları canlı bırakmak bu sütunun neden hiç yazılmamış olduğunun ta
  kendisiydi.

  **Sütun yanlıştı: üç migrasyon dört sütunu DÜŞÜRDÜ**, ve üçü de gerekçesini
  ayrı ayrı yazıyor. Sevkiyat ile rezervasyon OLMUŞ BİR ŞEYİN KAYDIDIR ve
  emekliliği bir DURUMDUR: sevkiyat modülünün ilk migrasyonu bu argümanı zaten
  sevkiyat KALEMLERİ için yapmış, tabloya kendisine hiç uygulamamıştı; envanter
  tarafında ise `deleted_at`, durumun söylediğini söyleyen ikinci bir yoldur ve
  ikisi çelişebilir — dahası, bırakmayı silmeye çevirmek "zaten bırakıldı" ile
  "hiç var olmadı" cevaplarını tek cevaba indirir, ki checkout saga'sının
  telafi adımının idempotent olması tam da bu ayrıma dayanır. Ülke ile para
  birimi ise REFERANS VERİDİR: satırlarını tohum migrasyonu yazar, var olan tek yazma
  bir ülkeyi bölgeler ARASINDA taşır, ve sütun bir gün yazılsaydı aktif olarak
  zararlı olurdu — damgalanmış bir ülke sonsuza kadar "bölgesi yok" cevabı verir
  ve o ülkede ödeme, katalogda ya da bölge listesinde bunu açıklayan hiçbir şey
  yokken durur.

  **Sütun düşürmek indeksleri de düşürür ve biri taşıyıcıydı.** PostgreSQL,
  YÜKLEMİ düşürülen sütunu adlandıran her indeksi sessizce ve uyarısız düşürür;
  bu tabloların bütün indeksleri `deleted_at` üzerinde kısmiydi. Migrasyonlar
  bunu yazılmadan ÖNCE gerçek bir PostgreSQL'de ölçtüklerini kaydediyor: kısmi
  UNIQUE indeksli bir deneme tablosunda, bir ifade öncesine kadar imkânsız olan
  yinelenen anahtar, sütun düşürüldükten sonra kabul edilmiş. Sevkiyatta söz
  konusu indeks, yeniden denenen bir saga adımının İKİNCİ BİR KARGO ETİKETİ
  üretmesini durduran idempotentlik korumasıydı — indeksleri yeniden kurmadan
  sütunu düşürmek korumayı kaldırır ve şemayı el değmemiş gösterirdi. Üç test
  bunu migrasyon metnine değil gerçek veritabanına karşı tutuyor:
  `TestTheIdempotencyGuardSurvivedTheDroppedColumn`,
  `TestDroppingTheColumnDidNotTakeTheReservationIndexesWithIt` ve
  `TestDroppingTheColumnDidNotTakeTheCountryIndexWithIt`.

  **Dokuzuncusu açık ve artık YER TUTUCU DEĞİL: muafiyeti soruyu SÖYLÜYOR.**
  Stok lokasyonunda sütunu yazmak yetmiyor ve bu ölçüldü — uygunluk toplamları
  seviye satırlarını lokasyona hiç JOIN yapmadan okuyor, yani yumuşak silinmiş
  bir lokasyon operatörün ekranından kaybolurken stoğunu satmaya devam eder ve
  rezervasyon yolu onu yine dağıtır. Sert silme daha kötü: hem seviye hem
  rezervasyon lokasyona ON DELETE CASCADE ile bağlı, yani stok satırlarını ve
  modülün "asla silinmemeli" dediği rezervasyon geçmişini yok ederdi. SORU
  bu yüzden "silme mi durum mu" değil, kapanan bir lokasyonun neyi borçlu
  olduğudur: seviyeleri taşınacak mı, sıfırlanacak mı, yoksa yalnızca
  uygunluğun dışında mı tutulacak, ve orada hâlâ aktif olan rezervasyonlara ne
  olacak. Muafiyet listesi bugün on bir satır: karar olan D9'un onu, ve bu bir
  soru. "UNCLOSED FINDING" ifadesi listede hiç geçmiyor.

- **Başarılı bir iş koşusu artık KONUŞABİLİYOR: `gobit jobs` listesinin detay
  sütunu bir HATANIN arkasından çıktı** (D21).

  Önce ölçüldü, sonra yazıldı — ve ölçüm brifingin öncülünü doğruladı. Gerçek
  koşucuya karşı atılan tek kullanımlık bir sonda üç durumu da üretti: başarılı
  koşu detayı BOŞ bırakıyordu, detay taşıyan bir hatayla düşen koşu onu
  yazdırıyordu, detay taşımayan bir hatayla düşen koşu yine boş bırakıyordu.
  `Outcome.Detail` yalnızca hatadan besleniyordu ve o da hata boş değilken
  çalışan bir koruma altındaydı. Yani söyleyecek bir şeyi olan ve şikâyet edecek
  bir şeyi olmayan bir geçiş, ancak günlüğe konuşabiliyordu.

  Çözüm koşum kapsamında ve bağlamda taşınan bir raportör: `WithReporter` onu
  kuruyor, `Report` bir satır yazıyor, koşucu `Outcome.Detail`'i ondan besliyor.
  Tamamen EKLEMELİ — hiçbir imza değişmedi, iş tanımı yeni bir dışa açık alan
  kazanmadı, bir gün önce yayımlanmış sözleşmeye dokunulmadı.

  **İki seçenek reddedildi ve birincisi ÖLÇÜMLE reddedildi.** İş gövdesini
  hatanın yanında bir dizge de döndürecek şekilde genişletmek daha baştan ölü:
  `TestEveryJobDefinitionFieldReachesAPluginJob`, zamanlayıcının kendi tanımının
  her dışa açık alanının yayımlanmış `plugin.Job` üzerinde DÖNÜŞTÜRÜLEBİLİR bir
  ikizi olmasını şart koşuyor, o yapının iş alanı ise yalnızca hata döndüren bir
  fonksiyon — ve bir yansıma sondası iki fonksiyon tipini İKİ YÖNDE de
  dönüştürülemez raporluyor. Yani bu seçenek ancak bir gün önce yayımlanmış bir
  sözleşmeyi kırarak ya da kopyayı koruyan kapıyı zayıflatarak alınabilirdi:
  depo içindeki üç işe bir dizge vermek için çok büyük bir fatura, ve aşağı
  akıştaki bir yazarın ödeyeceği sürüm "eklentiniz artık derlenmiyor" olurdu.
  İkincisi — başarılı sonucun uygulayabileceği bir arayüz — pahalı değil,
  İNŞA EDİLEMEZ: başarı `nil` bir hatadır ve `nil` hiçbir arayüzü uygulamaz;
  çalışması için başarısızlık olmayan bir hata döndürmek gerekir, ondan sonra da
  her hata kontrolünün, sonuç sütununun ve listedeki başarısızlık önekinin hangi
  hataların başarısızlık OLMADIĞINI öğrenmesi gerekir.

  **Bedel açıkça ödendi, inkâr edilmedi.** Bağlam kanalı kendini gizler: bir işin
  imzasında rapor verebileceğini söyleyen hiçbir şey yoktur. Üç şeyle
  hafifletildi — gövdenin godoc'u fonksiyonu adıyla anıyor, depodaki üç işin
  üçü de çağırıyor (yani desen depoda okunabilir), ve koşum dışında yapılan bir
  çağrı sessiz bir no-op: bilmemenin bedeli eksik bir satır, asla bir panik
  değil.

  **Öncelik, değişikliği zaten çalışan her şey için görünmez kılıyor.** Kendi
  detayını taşıyan bir hata, koşum ortasında raporlanan her şeyi hâlâ EZİYOR;
  yani düşen bir koşu tam olarak eskiden ne raporluyorsa onu raporluyor. İkisini
  ters çeviren bir mutasyon tam bir testi düşürüyor. Gerçekten yeni olan tek
  davranış — kendi detayı OLMADAN düşen bir koşunun son raporlanan satırı
  koruması — yalnızca yeni fonksiyonu çağıran koddan erişilebilir.

  **Yetenek ÜÇ TÜKETİCİYLE birlikte geldi**, çünkü tüketicisi olmayan bir
  yetenek bu deponun adı konmuş en pahalı kusurudur. Outbox rölesi geçişinin
  VERİMİNİ raporluyor, ve boş hücrenin sakladığı şey buydu: boş geçen bir
  dakika, yetişen bir röle, ve her dakika grup sınırını doldururken yığını büyüyen
  bir röle — üç ayrı olgu, aynı biçimde yazdırılıyordu. Ödeme mutabakatı
  incelediğini, uyuştuğunu, AYRIŞTIĞINI ve ulaşamadığını raporluyor; saga gözcüsü
  terk edilmiş sayısını. Üçünün de bulguları bugüne dek yalnızca günlüğe
  ulaşıyordu. Rölenin ölü mektup yığını ise koşusunu DÜŞÜRMEYE devam ediyor —
  devralınmadı, yeniden karara bağlandı ve paket belgesine yazıldı: SONUÇ
  operatörün taradığı sütundur, detay sonradan okunur, ve bu işin sonucunu
  izleyen her şey bir indirgeme yayımlandığı gün sessizce susardı.

  Zincir her halkanın yaşadığı yerde ayrı ayrı kanıtlandı: rapordan sonuca
  gerçek koşucuya karşı, sonuçtan saklanan satıra ve geri gerçek bir
  PostgreSQL'e karşı, saklanan satırdan yazdırılan listeye kompozisyon kökünde.
  Altı mutasyonun altısı da ısırdı ve her geri yükleme sha256 ile doğrulandı.

  **Yolda bulundu, raporlandı ve YAPILMADI:** `Runner.RunNow`'un kendi testleri
  dışında hiçbir çağıranı yok, ve godoc'u onu `gobit job run` komutunun
  çağırdığını iddia ediyordu. Öyle bir alt komut yok — ikili help, migrate,
  stuck, recover, jobs, deadletters ve seed dağıtıyor. `Definition.Every` de aynı
  var olmayan komutu takvim zamanlaması için kaçış kapısı olarak öneriyordu. İki
  cümle de artık özelliği iddia etmek yerine boşluğu söylüyor.

  **Açık kalan ve aynı gün gelen:** bir EKLENTİ detay raporlayamıyor. Yayımlanmış
  iş sözleşmesinin gövde alanı bağlam alıp hata döndürüyor, raportör ise üçüncü
  tarafın import edemeyeceği bir internal pakette yaşıyor — oysa koşucunun
  donattığı bağlam eklentinin gövdesine zaten veriliyor, yani değer orada,
  ulaşılamayan tek şey anahtar. Canlı örnek aynı gün geldi: `plugins/webhookout`
  teslim geçişi verimini ve geçiş sınırına dayandığını GÜNLÜĞE raporluyor,
  listeye ise ancak DÜŞEREK ulaşıyor. En küçük kapanış, bağlam anahtarını taşıyan
  yayımlanmış küçük bir yaprak paket ve onu yeniden dışa veren internal paket:
  uyumluluk sözüne üç tanımlayıcı ekler, ADR 0026'nın öngördüğü fiyat ise
  zamanlayıcının tamamını yayımlamaktı. Gerekçesiyle reddedilenler: zamanlayıcının
  eklenti yüzeyini import etmesi (yasal, ama bağımlılığı ters çevirir) ve
  eklentinin gövdesini kompozisyon kökünde sarmalamak (sarmalama, eklentinin
  konuşabileceği bir kanalı icat edemez).

- **Bir eklenti artık zamanlanmış iş kaydedebiliyor — ve uzatma noktası İLK
  TÜKETİCİSİYLE birlikte geldi** (B13).

  `Host.RegisterJob` dört alanlı bir `plugin.Job` alıyor: ad, aralık, tek koşum
  sınırı, ve işin kendisi. Rotalar gibi Setup sırasında TOPLANIYOR, kompozisyon
  kökünün `addPluginJobs` fonksiyonu tarafından da iş defteri var olduğu anda
  boşaltılıyor. ADR 0019 bu yüzeyi reddetmemiş, KOŞULA bağlamıştı — "ilk iş
  getiren eklentiyle gelir" — ve o gün bugün.

  **Zamanlayıcı paketinin tamamını yayımlamak REDDEDİLDİ, ve reddin gerekçesi
  yazılı:** bir eklentinin ihtiyacı DÖRT DEĞER, oysa paketi yayımlamak
  koşucuyu, depo sözleşmesini, danışma kilidi sınıfını ve anahtar
  algoritmasını uyumluluk sözüne dondururdu — bir form dağıtmak için makinenin
  tamamını yayımlamak.
  Kopyanın bedeli sürüklenmedir ve bu bedel umutla değil testle ödeniyor:
  `TestEveryJobDefinitionFieldReachesAPluginJob` zamanlayıcının kendi tanımı
  üzerinde yansıma yapıyor ve o tanım, yayımlanmış struct'ın taşımadığı bir alan
  kazandığı gün düşüyor.

  **Hiçbir şey iki kez doğrulanmıyor, ve hiçbir şey ATLANMIYOR.** Eklentinin
  tanımı çekirdeğin kendi üç işiyle aynı `Registry.Add` çağrısından geçiyor:
  adsız bir iş, aralığından uzun bir `MaxRun`, gövdesi olmayan bir iş ya da
  alınmış bir ad AÇILIŞI reddettiriyor. Sessizce düşürmek buradaki en kötü
  sonuç olurdu: `gobit jobs` listesi eksiksiz görünürdü ve operatör o yokluğu
  "o geçişte yapacak bir şey yoktu" diye okurdu. Eklentiler defterin SONUNA
  ekleniyor, çünkü ad çakışmasında ikinci taraf düşer ve bir eklentinin iş adını
  değiştirmek, geçmişi zaten olan bir çekirdek işinin adını değiştirmekten çok
  daha kolaydır.

  **İlk tüketici aynı değişiklikte:** `paymentpaytr` artık saatlik bir
  `pendingWatch` koşuyor. Taşıdığı rapor bugüne dek yalnızca BİR kez
  yapılıyordu — açılışta, `Register` içinden — oysa anlattığı sınıf (PayTR'nin
  hiç geri dönmediği, karşılığında sipariş olmayabilecek para) açılışta gelmez,
  bir haftadır ayakta olan bir süreçte BİRİKİR. İş yazmıyor, denemiyor, iptal
  etmiyor: okuyor ve bildiriyor; ADR 0017'nin çizgisi orada duruyor.

  Aynı yerde iki küçük şey daha düzeldi. Sınırın dolması artık DOLDU olarak
  bildiriliyor: yüz satırlık başlangıç raporu sınıra vardığını söylemiyordu,
  yani dört yüz ödemelik bir olay "yüz" diye anlatılırdı — sessizce "en az bu
  kadar" anlamına gelen bir sayı, hiç sayı olmamasından kötüdür. Ve raporun
  işaret ettiği adres yanlıştı: uyarı satırı bu eklentinin hiç sunmadığı bir
  yolu gösteriyordu, artık rota ile uyarının tek kaynağı olan `PendingPath`
  sabitini gösteriyor.

  Yüzeyin gerçekten dışarıdan kullanılabilir olduğunun kanıtı `examples/plugin`:
  kendi go.mod'u olan, `internal/` ağacına ERİŞEMEYEN ayrı bir modül, ve artık
  o da bir iş bildiriyor. Derleniyor olması kanıtın kendisi — depo içindeki bir
  eklentinin aynı şeyi yapması hiçbir şey kanıtlamazdı, çünkü o zaten
  zamanlayıcıya doğrudan uzanabilirdi.

- **Ölü mektupların artık bir OPERATÖR YÜZÜ var: `gobit deadletters`** (B12).

  Dün bu bölümde şu yazıyordu: *"`Redrive` ile `Discard` var, test edildi ve
  ÜRETİMDE HİÇBİR ÇAĞIRANI YOK — ne komut, ne rota."* Önce ölçüldü, sonra
  yazıldı: iki fiilin depoda test dışında tek bir çağıranı yoktu, `DeadLetters`
  ise tam bir tane taşıyordu — röle işi, yığından beş satırlık bir örnek okuyup
  sayısını bildiriyor. Komut o eksik çağıran.

  Tek ad, üç eylem: `gobit deadletters` listeler (salt okunur),
  `gobit deadletters redrive <id> -confirm <id>` satırı kuyruğa geri koyar,
  `gobit deadletters discard <id> -confirm <id>` atar.

  **Listeleme bir kararın ihtiyaç duyduğu şeyi taşıyor:** yığının TAMAMININ
  sayısı — sayfanınki değil — olay kimliği ve adı, deneme sayısı, son hata, söz
  verildiği ve vazgeçildiği anlar, ne kadar denendiği ve ne zaman öldüğü; yükün
  ise YOK değil ESİRGENMİŞ olduğu açıkça yazılıyor. Eylem yolu yığını yeniden
  okuyup `gobit jobs` alarmının temizlenip temizlenmeyeceğiyle kapanıyor, çünkü
  operatörün geldiği soru bu. Hiçbir satırı değiştirmeyen bir çağrı sessiz bir
  başarı değil HATA, ve üç sebebini birden yazıyor: yanlış kimlik, hiç
  vazgeçilmemiş satır, ya da başkasının çoktan hallettiği bir satır.

  **`-limit` bayrağının maliyeti ölçüldü ve VERİTABANINA değil terminale ait
  çıktı.** Deponun sorgusu yığının boyunu `LIMIT`'ten önce değerlendirilen bir
  pencere fonksiyonuyla hesaplıyor, yani sayfa ne olursa olsun yığının tamamı
  geziliyor: PostgreSQL 16, 2.000 ölü mektup taşıyan 42.300 satırlık
  `event_outbox`, beş koşumun en iyisi — `-limit 1` 0,690 ms, `-limit 50`
  0,690 ms, `-limit 500` 0,757 ms. Rakamı büyüten şey YIĞIN: aynı sorgu 50.000
  mektupluk bir yığında 17,3 ms. Varsayılan 50 bu yüzden veritabanı hakkında
  değil OKUYUCU hakkında bir sayı, ve godoc'u bunu böyle yazıyor.

  **"Hepsini at" REDDEDİLDİ, ve gerekçe maliyet değil — çünkü maliyet ölçüldü.**
  Tek bir atma birincil anahtar üzerinden 0,047 ms, tek bir geri alma 0,183 ms;
  iki bin tanesi veritabanının fark etmeyeceği bir iş. Toplu bir bayrak
  operatöre yalnızca TUŞ VURUŞU kazandırırdı, ve o tuş vuruşunun kendisi
  özellik: `-all`, bu depoda görmezden gelinerek susturulamayan TEK alarmın tek
  tuşluk susturucusu olurdu; satır yükün SON kopyası, yani yığını okumadan
  boşaltmak "neyi vaat edip teslim etmedik" sorusunun cevabını yok eder; ve bir
  kimliği yazmak, yanında basılan olay adını ve son hatayı OKUMUŞ olmak demek.
  Toplu GERİ ALMA daha zor bir karardı ve ayrıca gerekçelendirildi: kesinti
  sonrası yığın homojen değil — kimi mektup alıcı düştüğü için orada ve geri
  alınmayı hak ediyor, kiminin yükü bozuk ve onu geri almak dört saatlik
  denemeyi harcayıp tam olarak aynı yığına dönmek demek. Onay biçiminin
  "hepsi" için tekrarlayabileceği dürüst bir şey de yok: bir sayı kaç tane
  olduğunu söyler, hangileri olduğunu asla, ve listeleme ile koşum arasında
  bayatlar.

  **Onay bir `-force` bayrağı değil, KİMLİĞİN TEKRARI** — `migrate down <owner>`
  ile `recover <id>` neyse o. Bu fiillerde yanlış giden şey neredeyse hiçbir
  zaman "atmak istemiyordum" olmuyor, "BAŞKA bir kimliği kastediyordum" oluyor:
  değer bir listeden, bir satır aşağıdan ya da yukarıdan, olay anında ve hızla
  kopyalanıyor. Sabit bir bayrak hedef hakkında hiçbir bilgi taşımadığı için bu
  hata sınıfını yakalayamaz; üstelik sabit olduğu için bir kabuk takma adına ya
  da runbook satırına göçer ve bir daha yazılmaz. Tekrarlanan kimlik önceden
  yazılamaz, çünkü her seferinde farklıdır ve önce yığından OKUNMAK zorundadır.
  Geri alma da korunuyor, çünkü göründüğü gibi bir okuma değil: `attempts`
  sıfırlanıyor ve `dead_lettered_at` temizleniyor — yani operatöre az önce
  gösterilen ölüm kaydı siliniyor — ve gerçek bir ileti otobüse geri konuyor.

  **Beş mutasyonun beşi de ısırıyor:** onay karşılaştırmasının etkisizleşmesi,
  iki fiilin yer değiştirmesi, hiçbir satırı değiştirmeyen bir çağrının başarı
  sayılması, gönderinin dağıtım kolunun silinmesi, ve raporun yığının sayısı
  yerine sayfanın boyunu basması. Sonuncusu yalnızca birim katmanında düşüyor ve
  bu doğru: integration senaryosunda sayfa yığının tamamı, yani kırpılma durumu
  orada zaten görünmez.

  Depo tarafına tek satır eklenmedi: `DeadLetter` kimliği, adı, deneme sayısını,
  son hatayı ve iki anı zaten taşıyor, yükü taşımaması ise listelemenin yüksek
  sesle söylediği bir tercih.

- **İade isteği ve talep artık GERİ ALINABİLİYOR — iki `UPDATE` ifadesinin
  üretimde hiçbir çağıranı yoktu** (D17).

  Dün ölçülüp açık bırakılmıştı: `CancelReturn` ile `CancelClaim` tam yazılmış,
  test edilmiş ve üretimde ne rotadan, ne akıştan, ne interop'tan çağrılıyordu —
  yani `order_returns.canceled_at` ile `order_claims.canceled_at` üretimde takas
  sütunu kadar yazılmıştı. Kapı onlarda yeşildi, çünkü `UPDATE` metni bir `.sql`
  dosyasında duruyor (D16, üçüncü kör nokta).

  İki yönetim ucu bağlandı —
  `POST /admin/v1/orders/{id}/returns/{returnId}/cancel` ve
  `POST /admin/v1/orders/{id}/claims/{claimId}/cancel` — ikisi de yazma
  kapsamında, ikisi de akıştan değil doğrudan servise gidiyor: teslim ALINMAMIŞ
  bir isteği geri almak ne stok ne para kıpırdatır, yani başka modüle söylenecek
  bir şey yok. Teslim alınmış durum bu savın açığı değil, geçiş tablosunun
  reddettiği bir durum.

  **Alternatif iki metodu SİLMEKTİ** — "bir iade zaten hiç geri alınmaz"
  gerekçesiyle — ve modülün içinde duran üç şey buna itiraz etti. Miktar
  kuralının bir TAHLİYE VALFİ var ve hiç açılamıyordu: `SumReturnedQuantities`
  iptal edilmiş iadeleri kasten dışarıda bırakıyor, yani bir satırın tamamı için
  açılan tek bir istek o satırın iade edilebilir miktarını SONSUZA KADAR
  tüketiyordu — üstelik vitrin ucu böyle bir isteği yalnızca sipariş kimliğini
  bilerek açabiliyor. `ReturnStatus.CancelAction` ile `ClaimStatus.CancelAction`
  üç satırlık geçiş tabloları ve öteki iki satırları yalnızca bu geçişi korumak
  için var. Zaman çizelgesi ise hiçbir satırın taşıyamayacağı bir "iade iptal
  edildi" girdi türünü zaten yayımlıyordu. Talep tarafı simetrikten de kötüydü:
  uzlaştırma yalnızca "requested" durumundaki ve "refund" türündeki talebi kabul
  ediyor, yani yanlış siparişe açılmış, iki kez açılmış ya da müşterinin geri
  çektiği bir talebin HİÇBİR çıkışı yoktu — listede, dükkânın hâlâ borçlu olduğu
  işlerden ayırt edilemez biçimde duruyordu.

  **Kural nerede yaşıyorsa orada kanıtlandı.** Tahliye bir `WHERE` yan tümcesi ve
  sahte bir depo, sahip olmadığı bir yan tümceye itiraz edemez; iki integration
  testi gerçek veritabanına karşı koşuyor. Geri alınan istek birimlerini geri
  veriyor — aynı satır için ikinci istek geri almadan ÖNCE reddediliyor, SONRA
  kabul ediliyor — ve geri alınan talep ikinci tıklamada İLK anı koruyor,
  uzlaştırılmış talep ise çatışmayla reddediliyor. İkisi de damgayı ikinci bir
  sorguyla satırdan okuyor: `RETURNING` yan tümcesi satırın tutmadığı bir değeri
  bildirebilir.

- **Giden teslimat makinesi: artan gecikmeli yeniden deneme ve ÖLÜ MEKTUP — ve
  düzelttiği şeyin bir yavaşlama değil, teslimatın DURMASI olduğu ölçüldü**
  (B12).

  Önceden bir yayımlama hatası tek bir şey yapıyordu: `attempts` bir artıyor ve
  satır bekleyen kalıyordu. Bir dakika sonraki geçiş onu yeniden deniyordu,
  ondan sonraki her geçiş de, kurulum yaşadığı sürece. Bu, bir outbox'ın bağışık
  olması gereken iki arızayı BİRDEN üretiyordu — ve ikisi birbirinin zıddı
  olduğu için yalnızca birine yaslanan bir tasarım ötekine varır.

  **Ölçüm gerçek bir PostgreSQL üzerinde, rölenin kendi koduyla yapıldı ve
  beklenenden ağır çıktı.** Geçiş en ESKİ bekleyen satırları tavanı kadar okur;
  yani kalıcı olarak başarısız olan tavan kadar satır her partiyi doldurur. Art
  arda BEŞ geçiş hiçbir şey yayımlamadı ve onların arkasına yazılan sağlıklı bir
  olay `attempts = 0` ile bitti — bir kez bile denenmemişti. Yığılma teslimatı
  bozmuyor, BİTİRİYOR. "Yeniden deneme yok" bir eksiklik olarak yazılıydı;
  ölçüldüğünde bir veri kaybı sınıfı çıktı.

  **`next_attempt_at` birinci arızayı, `dead_lettered_at` ikincisini kapatıyor.**
  Başarısız satır gecikmesi dolana kadar seçilmez, yani her partide bir yer
  işgal etmeyi bırakır; tavandan sonra ise rölenin indeksinden tamamen çıkar ve
  arkasındaki olayların önü açılır. Varsayılan takvim 1, 2, 4, 8, 16, 32, 60,
  60, 60 dakika ve sonra ölü mektup: on denemeye yayılmış dört saat üç dakika.
  Rakam, hayatta kalması gereken şeye göre seçildi — alıcı kesintisi. Dört
  saatten kısası nöbetçi bir insanın makul olarak onaramayacağı, uzunu ise
  gerçekten zehirli bir olayı kimseye haber vermeden ertesi iş gününe taşıyan
  süre.

  **Vazgeçmek bir DÜŞÜRME değil, bir YAZMA.** Satır, vazgeçilen anı, deneme
  sayısını ve son hatayı taşır; röle işi her geçişte yığını okur ve yığın boş
  değilken koşumunu BAŞARISIZ sayar. Sebep tasarım değil mekanizma: iş
  koşucusunun DETAY sütununu yalnızca bir hatadan doldurduğu için, başarılı bir
  koşum hiç detay kaydetmez — yani seçenek "hata mı detay mı" değildi, "hata mı,
  yoksa yığın operatörün baktığı tek listede GÖRÜNMEZ mi" idi. Okuyucusu olmayan
  bir defter bu deponun `audit_log` ile bir kez yaptığı hata.

  **İndeks değişikliğinin üç cümlesi de ölçülü** (PostgreSQL 16; 40.000
  yayımlanmış, 2.000 ölü mektup, 300 bekleyen satır; rölenin kendi seçimi,
  `LIMIT` 200):

  | indeks | süre | buffer | elenen satır |
  | --- | --- | --- | --- |
  | yeni kısmi yüklem | 0,18 ms | 411 | 0 |
  | 000001'in yüklemi, aynı anahtar | 1,38 ms | 840 | 1.900 |
  | anahtar `(next_attempt_at, created_at, id)` | 0,31 ms | — | Sort düğümü |

  Orta satırda önemli olan milisaniye değil, 1.900: bunlar taramanın 200 canlı
  satırı bulmak için üzerinden geçtiği ÖLÜ satırlar. 000001'in indeksinden asla
  çıkmadıkları için o rakam kurulum yaşadığı sürece büyür — yavaşlayan bir sorgu
  ile yavaşlamaya DEVAM EDEN bir sorgu arasındaki fark bu. Anahtar `created_at`
  ile başlamayı sürdürüyor çünkü sıralama ondan; başa `next_attempt_at` konsa
  planlayıcı sıralama düğümü ekliyor.

  **Ölü mektupların kendi indeksi bir eniyileme değil.** Röle "insanın bakması
  gereken bir şey var mı?" sorusunu her geçişte, dakikada bir, sonsuza kadar
  sorar; o indeks olmadan bu soru bütün geçmişin sıralı taraması olurdu ve o
  kadar pahalı bir soru er geç sorulmaz olur.

  **Eksik kalan yarı yazıldı, gizlenmedi: `Redrive` ile `Discard` var, test
  edildi ve ÜRETİMDE HİÇBİR ÇAĞIRANI YOK** — ne komut, ne rota. Yani bugün
  alarmın SQL'e inmeden ulaşılabilen bir kapatma düğmesi yok, ve tasarımın
  kendisi "kapatılamayan alarm sonunda susturulur" diyerek o düğmeyi gerekçe
  yapıyor. Kaydedildi (B12), kapatılmadı.

- **Siparişin arşivlenmesi artık TARİHLENİYOR, ve takasın tablosu ilk `UPDATE`
  ifadesini aldı** (D5, D4).

  İkisi de aynı sınıftan: durumu çeviren ama ne zaman çevrildiğini kaydetmeyen
  bir geçiş. Dört sipariş durumundan üçü zaten kendi anını taşıyordu
  (`placed_at`, `completed_at`, `canceled_at`) ve üçünden ikisi durumuna bir
  ayna CHECK'i ile bağlıydı; arşivleme, durumu çevirip hiçbir şey yazmayan TEK
  geçişti. Migrasyon 000007 `orders.archived_at` sütununu ekliyor ve
  `ArchiveOrder` onu, durumu çeviren AYNI ifadede veritabanı saatinden yazıyor.
  Damga modele, yönetim DTO'suna, okuma katmanının `order` sağlayıcısına
  (`FieldArchivedAt`) ve zaman çizelgesine ulaşıyor.

  **Tarihlenmiş geçişler tablosu REDDEDİLDİ ve gerekçe ölçüldü:** satırda üç
  damga, yan tabloda dördüncü bir damga olurdu ve hangisinin yetkili olduğunu
  söyleyen bir kural olmazdı — ayrıca var olan iki ayna CHECK'i uygulanamaz hâle
  gelirdi, çünkü bir CHECK başka bir tablonun satırlarını göremez.

  **`DEFAULT now()` de reddedildi ve bu, ucuz görünen yanlış.** Varsayılan,
  sütunu SATIR yazıldığında damgalar; yani her sipariş, verildiği anda
  arşivlenmiş olduğunu iddia ederdi. Üstelik sütunu, sütun denetiminin kendi
  kuralı gereği denetim kapsamının DIŞINA çıkarırdı — güvenlik değil sessizlik
  satın alırdı. CHECK ise kardeşlerinin aksine TEK YÖNLÜ
  (`archived_at IS NULL OR status = 'archived'`): sütun var olmadan önce
  arşivlenmiş siparişler durumu taşıyor ve anı taşımıyor, ters yönü eklemenin
  tek yolu kimsenin kaydetmediği bir anı geriye doldurmak olurdu. Depo aynı
  soruyu migrasyon 000003'te `received_location_id` için aynı biçimde
  cevaplamıştı.

  **Takasta ise bir sütun YAZAN kazandı, öteki DÜŞTÜ.** `CancelOrderExchange`,
  `order_exchanges` tablosunun bugüne kadarki ilk `UPDATE` ifadesi ve
  `POST /admin/v1/orders/{id}/exchanges/{exchangeId}/cancel` uçuna bağlı —
  bağlanmasaydı sütun denetimi yeşile döner, sütun ise tam olarak eskisi kadar
  yazılamaz kalırdı (bkz. Düzeltildi, üçüncü kör nokta). Migrasyon 000008 ise
  `completed_at` sütununu ve durum CHECK'inden `completed` değerini DÜŞÜRÜYOR:
  takasın tamamlanması, var olan bir siparişe karşı mal SEVK EDİLMESİNİ — bu
  çerçevede hiçbir yerde olmayan bir yetenek — VE pozitif farkın tahsil
  edilmesini gerektiriyor, ikincisini `order_payment` bağının birebirliği
  yasaklıyor. Depo bunu üç ayrı yerde zaten yazmıştı. Hiçbir şeyin giremediği ve
  hiçbir anın tarihleyemeyeceği bir durum bir eksiklik değildir; var olmaması
  gereken bir durumdur. Elle yazılmış bir satır `completed` taşıyorsa migrasyon
  onu düzeltmek yerine DÜŞÜYOR — o satırı bu depodaki hiçbir kod üretemezdi ve
  sessizce sıfırlamak, sistem dışında ne yapıldığının tek kanıtını yok ederdi.

  Takasın `canceled_at`'i TAM ayna CHECK'ini aldı — `(status = 'canceled')` ile
  `(canceled_at IS NOT NULL)` eşit — ve bunu tam da sütun ÖLÜ olduğu için
  alabildi: hiçbir mevcut satır `canceled` taşımıyor, yani her satır iki yönü de
  sağlıyor. Siparişin `archived_at`'i alamadı ve fark yazıldı.

  **Zaman çizelgesinin godoc'u sahip olmadığı bir davranışı anlatıyordu.**
  Tarihsiz bir olgunun — biten bir takasın — düşürülmek yerine EN SONA
  konduğunu söylüyordu; girdi üretici öyle bir girdi hiç üretmiyordu ve o dalı
  üretecek koşul zaten erişilemezdi. Dal silindi, yerine TARİHLİ bir geri çekme
  girdisi geldi. Çizelge kurulduğu gün (2026-09-05) hiç testi yoktu; şimdi girdi
  üreticinin testleri var, bileşik okumanın hâlâ yok ve sebebi yazıldı.

- **Vergi modülü işlemi CONTEXT'te taşıyor, ve modülün ilk paylaşımlı kilidi
  var** (D6'nın vergi yarısı).

  Depo içindeki özel `inTx(ctx, func(q *taxdb.Queries) error)` gitti; yerine
  işlemi context'e koyan dışa açık `WithTx(ctx, func(ctx context.Context) error)`
  geldi — öteki altı modülün zaten sahip olduğu ortam-işlem tesisatı. Böylece
  sınırı servis kuruyor ve iki depo çağrısı tek işlemde birleşebiliyor. İmzanın
  iki tarafın paylaştığı tiplere inmesi zorunluydu: ADR 0001 servisin depo
  paketini import etmesini yasakladığı için o paketin hiçbir tipi, servisin
  kendi tanımladığı dar arayüzde geçemez.

  **Ve kayıtlı gerekçenin yanlış olan yarısı şuydu: "bugün hiçbir çağıran bunu
  yapmıyor".** Yapıyordu — ve bir godoc, yapılmamış olanı yapılmış gibi
  anlatıyordu. `DeleteTaxRegion`, aldığı kilidin "aynı bölgeye eşzamanlı bir
  oran ekleme akışıyla yarışı engellediğini, çünkü o akışın da bölgeyi
  paylaşımlı kilitle okuduğunu" söylüyordu. ÖLÇÜLDÜ: modülde tek bir `FOR SHARE`
  sorgusu yoktu. Oran ekleme bölgeyi kilitsiz ve AYRI bir işlemde okuyordu,
  yani denetimle yazma arasına giren bir silme ona görünmüyordu. Foreign key
  bunu yakalayamaz çünkü silme YUMUŞAK: ana satır yerinde durur. Sonuç yalnızca
  yetim bir satır da değil — `ResolveTaxRegions` eyalet satırını kendi başına
  eşleştirir ve zincir en özelden genele yürür, yani o eyaletteki her sepet,
  operatörün sildiğini sandığı bir bölgeden vergilenmeye devam ederdi; ülkeye
  yeni bir kök açıldıktan sonra bile.

  Cümle bugün doğru: `GetTaxRegionForShare` modülün ilk paylaşımlı kilidi,
  `LockTaxRegion` işlem DIŞINDA çağrılırsa hata döner (işlemsiz alınan bir
  `FOR SHARE` hemen serbest kalır, yani hiçbir şeyi korumaz ama koruduğu
  sanılır), ve hem eyalet ekleme hem oran ekleme onu yazmayla AYNI işlemin
  içinde alır. İki yarış da gerçek veritabanında, uyku yerine bekleme durumuna
  bakan bir kurguyla bağlandı; işlem çerçevesinin kendisi ise hem geri aldığı
  hem de çerçevesiz bırakıldığında YARIM kaldığı gösterilerek kanıtlandı.

  **`region` ÖLÇÜLDÜ ve kusur orada YOK** (6 Eylül 2026). Özgün cümle iki
  modülü, paylaştıkları BİÇİM üzerinden eşleştirmişti (işlemin bir repository
  metodunun içinde açılması); oysa kusur bu değil, bir SERVİS metodunun okuyup
  karar verip sonra yazması ve bu ikisinin ayrı autocommit ifadeleri olmasıdır.
  region'da yazan her servis metodu tam olarak BİR repository çağrısı yapıyor,
  yani kapsanacak bir servis metodu yok. Yumuşak silme tehlikesi orada da
  gerçek — ülkenin foreign key'i bölge satırına bakar ve `deleted_at`'i göremez
  — ama yazmadan önce alınan paylaşımlı kilit onu zaten kapatıyor; o tek satır
  silinince mevcut bir test, önlemek için yazıldığı yetim satırla düşüyor.

  region'da eksik olan şey KANITTI ve o kapatıldı: bölge silmenin işlem
  çerçevesi tamamen kaldırılıp iki yazma ayrı autocommit ifadesine
  çevrildiğinde — yani tam olarak tax'ın şekli — modülün bütün entegrasyon
  suite'i YEŞİL kaldı. Çerçeveyi tutan hiçbir şey yokmuş. Artık silme
  bloklanmışken üçüncü bir bağlantının gözleyebildiği ara durumu iğneleyen bir
  test var ve o mutasyonda düşüyor.

  **Hâlâ ölçülmemiş:** aynı BİÇİM `pricing`, `promotion`, `auth` ve `customer`
  modüllerinde de duruyor. Biçim kusur değil — bu cümlenin kendi hatası onu
  kusur sanmaktı — o yüzden her birine soru ayrı ayrı sorulmalı: orada bir
  servis metodu okuyup, karar verip, sonra yazıyor mu?

- **Panelin kataloğunda artık ARAMA KUTUSU var: okuma katmanı `q`'yu öğrendi —
  ve maliyeti 52.004 üründe ÖLÇÜLDÜ** (B2'nin son süzgeci, D12'nin kalan yarısı).

  Aynı günün ikinci yarısı buydu. Kategori süzgeci geldiğinde geriye tek bir fark
  kalmıştı: vitrin listelemesi metin araması yapıyor, okuma katmanının `product`
  sağlayıcısı yapmıyordu. Yani dükkânın MÜŞTERİSİ katalogda arayabiliyor,
  kataloğu tutan OPERATÖR arayamıyordu — panel bir modülü import edemez (ADR
  0011), tek yolu okuma katmanıdır.

  **Süzgecin kendisi SIFIR SQL istedi.** Terim `ProductFilter.Search` alanına
  gidiyor ve paylaşılan süzgeç gövdesi onu hem listelemede hem SAYIMDA
  `title ILIKE '%' || $4 || '%'` yapıyor: tek tanım, iki sorgu, Go'da yüklem yok.
  İş, yazılacak koddan çok verilecek kararlardaydı.

  **Ad "q", çünkü vitrinin kendi adı o** (REST'te sorgu dizesi, GraphQL'de aynı
  adlı argüman). "search" tek başına daha iyi okunurdu; bedeli tek kavrama
  ÜÇÜNCÜ bir ad — panel ile dükkân TEK kurulumdur ve aralarında hiçbir şeyin
  doğrulamadığı bir çeviri tablosu kalırdı. Panelin ADRES ÇUBUĞU ise ayrı bir
  karar: orada `?search=` yazıyor, çünkü bir yer imi bir sürümden uzun yaşar ve
  URL'yi insan okur. İki ad tek bir yerde, panelin listeleme fonksiyonunda
  buluşuyor (`internal/adminui/catalog.go`).

  **Boş ya da yalnızca boşluktan oluşan terim HİÇ SÜZGEÇ DEĞİL.** Ham geçirmenin
  iki yanlışı TERS yönlere bakıyor: `''` SQL'e `ILIKE '%%'` olarak varır ve her
  satırı eşler (arama yaptığını sanan istemciye bütün katalog), `'   '` ise
  hiçbirini (hiçbir şey aramamış olana boş dükkân). İkisi de sessiz ve istemci
  hangisini aldığını ayırt edemez. Seçilen yön, bu deponun aynı girdiye zaten her
  yerde verdiği cevap: REST boş parametreyi verilmemiş sayıyor, GraphQL argümanı
  kırpıp `nil`'e indiriyor ve `graph` paketindeki boş-metin testi vitrin
  listelemesinin BÜTÜN metin argümanlarını bu kurala tutuyor.

  **`q` ile `id`/`ids` BİRLİKTE verilirse istek REDDEDİLİYOR ve gerekçe
  ÖLÇÜLDÜ.** Başlık bir skaler sütun olduğu için Go tarafında yeniden
  denetlenebilirdi; engel şu: ILIKE'ın harf katlaması KÜMENİN CTYPE'ından gelir,
  Go'nunki Unicode'dan. Bu çalışma alanındaki C-CTYPE kümesinde denenen iki
  ASCII-dışı çift — büyük C-sedilla ile küçüğü, noktalı büyük I ile düz `i` —
  SQL'de EŞLEŞMİYOR, Go'da eşleşiyor; C.UTF-8 kümesinde üçü de uyuşuyor. Yani
  Go kopyasının doğru olup olmadığı `initdb`'nin nasıl koşulduğuna bağlı olurdu.
  Depo bunu zaten açılışta yokluyor (`core/db/casefold.go`, ADR 0015) ve
  `deploy/docker-compose.yml` C.UTF-8 ayarını ancak o kusurdan beri taşıyor —
  yerel ayar `initdb` anında sabitlendiği için o düzeltmeden önce yaratılmış her
  veri dizini hâlâ yalnızca ASCII katlıyor.

  **Ölçüm: 52.004 ürün, 54.000 varyant, tamamı `docs/catalog-search-cost.md`.**
  Yapısal yarısı koşmadan biliniyordu — `title` üzerinde indeks yok, desen
  BAŞTAN joker, yani hiçbir B-tree yardım edemez. Ondan çıkan "öyleyse arama
  yavaştır" sonucu YARIM YANLIŞ, ve yanlış olan yarı ne yapılacağına karar veren
  yarı. Listeleme, kanal süzgeci yok, `LIMIT 25`:

  | süzgeç | plan | süre | buffer |
  | --- | --- | --- | --- |
  | yok | sıralama indeksi taraması | 0,03 ms | 7 |
  | 52.000 başlığı eşleyen terim | aynı tarama, terim Filter olarak | 0,03 ms | 9 |
  | 111 başlığı eşleyen terim | aynı tarama, 12.473 indeks girdisi yürünüyor | 2,6 ms | 2.635 |
  | 1 başlığı eşleyen terim | sıralı tarama + sort | 9,1 ms | 730 |

  **Maliyet terimi değil, sayfanın son eşleşmesinin sıralamada ne kadar
  aşağıda olduğunu izliyor: GENİŞ arama bedava, SEÇİCİ arama pahalı — ve bir
  arama kutusuna gelen seçici olanıdır.** Taşınmaya değer üç sonuç:

  - **Sayım İKİ YÖNE birden hareket ediyor.** Kanal süzgeci açıkken süzgeçsiz
    sayım ~74 ms ve 156.743 buffer; tek ürün eşleyen bir terim onu 12,9 ms ve
    734 buffer'a DÜŞÜRÜYOR, çünkü `ILIKE` satır başına koşan görünürlük
    altsorgusunun ÖNÜNE geçip 52.003 çağrısını siliyor. Geniş terim ise ~84 ms'ye
    çıkarıyor. Sayımdaki duvar aramanın getirdiği bir şey değil; bu deponun
    zaten ölçüp yazdığı görünürlük probu.
  - **Bir plan hazırlanmış deyim altında SESSİZCE bozuluyor.** Kanal süzgeçli
    listelemenin ilk beş koşumu özel planla 14,4 ms ve 734 buffer; ALTINCIDAN
    itibaren PostgreSQL genel plana geçiyor — bütün sıralama indeksini yürüyen
    bir tarama, ~25 ms ve 10.982 buffer — ve geri dönmüyor. Bu, imleç sınırının
    `OR` yerine `COALESCE` nöbetçisiyle yazılmasının sebebi olan mekanizmanın
    ta kendisi, bu kez başka bir yan tümcede. Bağlantının hangi plana düştüğü
    ilk beş koşumun taşıdığı terimlere bağlı, yani sabit bir vergi değil
    bağlantılar arası VARYANS: hata raporundan yeniden üretilmesi en zor cins.
  - **Tavan gecikmede değil, ÜRETİMDE.** pgbench, terim işlem başına
    rastgele, 16 istemci: süzgeçsiz listeleme saniyede 11.564, seçici arama
    kanalsız 856 ve kanallı 638 — **kırk kat gecikme, on üçte bir üretim.** Bir
    avuç operatörün kullandığı panel için önemsiz; vitrin arama kutusu için bu,
    katalog büyümeden çok önce çarpılacak ilk tavan.

  **Nerede biter.** Tarama 10.000 satırdan itibaren doğrusal, satır başına
  0,18–0,235 mikrosaniye, dizde kırılma yok; buradan seçici arama 100 bin üründe
  ~20 ms, 250 binde ~50 ms, 500 binde ~100 ms eder. Bu bir ÖLÇÜM DEĞİL, ölçülen
  eğimden uzatmadır ve üç şey doğru kaldığı sürece geçerlidir: satırlar bu kadar
  dar kalır (bu başlıklar ortalama 15,5 karakter ve açıklamalar boş, yani gerçek
  bir katalog şimdiden daha pahalı), tablo bellekte kalır, eşzamanlılık düşük
  kalır. Dürüst sınır bir satır sayısı değil, bir çift koşul: **katalog belleğe
  sığmaz olduğunda ya da eşzamanlı arama birkaç yüz/saniyeyi geçtiğinde —
  hangisi önce gelirse.** Bu donanımda ikincisi önce geliyor.

  **Ölçülemeyen ve neden ölçülemediği yazılan şey:** düzenekte `product_category`
  ve `product_category_map` BOŞ, yani "kategori içinde ara" — kutunun asıl var
  oluş sebebi — için verilen her rakam bir TABAN. Yine de plandan okunabilen bir
  şey var: süzgeç gövdesi her isteğe bağlı yüklemi `($n IS NULL OR …)` diye
  yazıyor, taksonomi süzgeçlerinde bu `OR`'un ikinci yarısı bir `EXISTS`, ve
  PostgreSQL `EXISTS`'i yarı-birleştirmeye sabit katlamasından ÖNCE çıkardığı
  için `OR` içindeki `EXISTS` hiç aday olmuyor: altsorgu katalog satırı başına
  bir kez koşuyor ve şemanın açtığı indeks sorgudan ERİŞİLEMEZ kalıyor. 0,03 ms
  eden bir aramaya kategori eklemek onu en az 29 ms yapıyor — üstelik iç taraf
  BOŞKEN. Bu bir ipucu, tavsiye değil: aynı `OR` deyimi tek bir deyimin bütün
  süzgeç bileşimlerine hizmet etmesini sağlayan şey.

  **En keskin ayrım yine mutasyondan geldi.** Depodaki `ILIKE` `LIKE` yapıldığında
  BÜTÜN birim takımı yeşil kalıyor — sahte depo ILIKE'ı kanıtlayamaz — ve yalnızca
  yeni entegrasyon testi düşüyor. Entegrasyon testinin neden var olmak zorunda
  olduğu iddia edilmedi, ölçüldü.

  **Elle kopyalanan SÜZGEÇ adları artık bağlı** (`internal/arch/constants_test.go`).
  `q` iki tarafta da dışa aktarılmamış bir sabit, yani derleyicinin
  bağlayabileceği bir çift YOK — sabitler çalışma zamanında zaten var olmaz,
  derleyici onları yerinde açar. Yeni kapı bu yüzden derleyicinin baktığı yere,
  KAYNAĞA bakıyor: `TestThePanelCatalogFilterKeysAgree` iki paketin kaynağından
  ortak dört süzgeç sabitini (`id`, `product_id`, `category_id`, `q`) okuyup
  değerlerini karşılaştırıyor. Eşleme AD üzerinden olduğu için tek başına
  sessizce küçülebilirdi — panel kendi kopyasını yeniden adlandırsa kesişim bir
  üye kaybeder ve test yeşil kalırdı — bu yüzden dört ad ayrıca VERİ olarak
  yazılı ve eksilmesi hata. İki mutasyonla kanıtlandı: beklenen adı değiştirmek
  iki tarafı da adıyla bildiriyor, karşılaştırılan değeri kaydırmak dördünü de
  düşürüyor. Bağlanamayan tek şey ALAN adları — modül onları kayıt kurucusunda
  düz dizge olarak yazıyor, okunacak sabit yok — ve bu sınır testin kendi
  "kapsamadıkları" bölümünde duruyor.

- **Ölçüm düzeneği artık DEPODAN kuruluyor: `gobit seed`** (D13).

  Her zamanlama cümlesinin dayandığı 52.004 ürünlük katalog TEK bir Docker
  biriminde yaşıyordu ve depoda onu yeniden kuracak hiçbir şey yoktu: seed
  dosyası yok, hedefi yok, programı yok; compose dosyası o veritabanını hiç
  yaratmıyor, yani temiz bir makinede düzenek YOK ve elde etme yolu da yok.
  **Test dışı 28 dosya bu düzeneğe dayanan rakam taşıyor.** Yani
  `docker compose down -v`, ölçülmüş bir iddia ile doğrulanamaz düzyazı
  arasındaki bütün mesafeydi — deponun en yüksek sesli kuralı bir Docker
  birimine bağlıydı.

  Yeniden kurma AYRI BİR BETİK DEĞİL, sunucu ikilisinin alt komutu
  (`internal/rig`, `internal/app/seed.go`, `make seed`) ve bu zorunlu: şema
  modüllerin KENDİ migration'larından gelmeli, üstelik düzeneğin ihtiyaç duyduğu
  üç tablo — `link_product_variant_price_set`, `link_product_variant_inventory`,
  `link_product_sales_channel` — hiçbir migration'da yok; onları `core/link`
  AÇILIŞTA yaratıyor, yani saf bir `psql -f` ilk link INSERT'ünde patlardı.

  **13,6 saniye**, tek işlemde, `generate_series`'ten; kimlikler satır
  numarasından türediği için ikinci koşum hiçbir şey eklemiyor. Kabul testi bir
  KRONOMETRE değil bir PLAN — yavaş makinede kızarıp okuyucuya "bunu boş ver"
  öğreten bir eşik yok: yeniden kurulan düzenek sayım sorgusunun kayıtlı planını
  buffer'ına kadar üretiyor (52.004 satır, 52.004 altsorgu döngüsü, Heap Fetches
  0, 156.743 shared hit, bunun 156.013'ü altsorgunun).

  **İki bulgu kurmanın kendisinden çıktı.** `ANALYZE` tek başına yetmiyor:
  VACUUM olmadan görünürlük haritası hiçbir yerde kurulmuyor ve aynı deyim 0 ve
  156.743 yerine **52.000 heap fetch ve 208.742 buffer** bildiriyor — yani üçte
  bir fazla trafik, ve godoc'taki rakamla karşılaştıran herkes farkında olmadan
  BAŞKA bir veritabanıyla karşılaştırıyor olurdu. İkincisi: hayatta kalan
  düzeneğin şeması depodan **beş order, bir payment ve bir product migration
  geride**, invoice/job/outbox/audit şemaları ise hiç yok — elle yazılmış bir
  seed dosyasının kurumsallaştıracağı sürüklenmenin ta kendisi.

  Gizlenmeyip yazılan bedel: silme YAVAŞ, **13,6 saniyenin yazdığını silmek
  4 dakika 31 saniye**, çünkü yabancı anahtar denetimi modüllerin KISMİ
  indekslerini kullanamıyor ve silinen her ebeveyn için sıralı taramaya düşüyor.
  TRUNCATE hızlı olurdu ve kurulumun gerçek kataloğunu da silerdi; denetim için
  geçici indeks yaratmak bir geliştirme aracının şemayı değiştirmesi olurdu.
  İkisi de reddedildi, sayı yüksek sesle söylendi.


- **Panelin kataloğu artık kategoriye göre daraltılabiliyor: okuma katmanı
  taksonomi süzgeçlerini ve bir `category` varlığını öğrendi** (B2, D12).

  Aynı gün ölçülen boşluk şuydu: vitrin listelemesi `category_id` ve `tag_id`
  kabul ederken okuma katmanının `product` sağlayıcısı yalnızca `status`,
  `handle`, `collection_id` ve `id`/`ids` kabul ediyordu — yani dükkânın
  MÜŞTERİSİ kataloğu kategoriye göre daraltabiliyor, dükkânın OPERATÖRÜ
  daraltamıyordu. Panel bir modülü import edemez (ADR 0011), tek yolu okuma
  katmanıdır; bu yüzden boşluk panelin değil, sağlayıcının boşluğuydu.

  **Ölçülen ilk şey maliyet: iki `switch` dalı ve SIFIR yeni SQL.**
  `ProductFilter.CategoryID` ve `ProductFilter.TagID` hem listelemeye hem sayıma
  zaten EXISTS altsorgusu olarak bağlıydı (join değil — birkaç kategoride olan
  ürün bir kez döner). Veritabanının en başından beri cevaplayabildiği bir soruya
  yüzey bir `case` uzaklıktaydı. Bu boşluk sınıfının şekli budur: eksik olan
  makine değil, makineyi sunan yüzeydeki dal.

  **`id`/`ids` ile bir taksonomi süzgeci BİRLİKTE verilirse istek REDDEDİLİYOR**,
  cevaplanmıyor. Gerekçe varsayılmadı, koda bakılarak doğrulandı: kimlik
  yolundan dönen ürünlerin kategori ve etiket dilimleri hiçbir zaman
  doldurulmuyor (satır→model çevirimi onları hiç yazmıyor), yani Go tarafında
  yapılacak bir üyelik denetimi DERLENİR, hiçbir şeyle eşleşmez ve kendinden
  emin BOŞ bir sayfa döndürür. Üyelikleri ayrıca çekmek de reddedildi: üyelik
  yüklemi SQL'deki EXISTS'in yanında ikinci kez Go'da yazılmış olurdu ve kategori
  bir AĞAÇ — SQL bir gün alt kategorileri de eşleştirdiğinde Go kopyası doğrudan
  üyeliği cevaplamaya devam eder, tek süzgeç iki cevap verir. Reddin VERİDEN
  BAĞIMSIZ olduğu testle sabitlendi: kimlik gerçekten o kategorideyken de düşer.

  **Panelin bir sözlüğe ihtiyacı vardı, çünkü operatör `pcat_…` bilmez.** Ürün
  modülü okuma katmanına ikinci bir varlık sunuyor: `category`
  (`internal/modules/product/service/category_provider.go`). O da yeni SQL
  istemedi — kimliklere göre okuyan sorgu zaten yazılmış ve üretilmişti, onu
  saran hiçbir repository metodu yoktu (`Repo.ListCategoriesByIDs`).

  Sağlayıcı VARSAYILAN olarak `is_active`/`is_internal` daraltması YAPMIYOR:
  panel vitrin değildir ve kapattığı kategoriyi göremeyen bir satıcı onu geri
  açamaz. Vitrinin daraltması `public_only` olarak açık bir tercihe dönüştü,
  kayıt iki bayrağı da BİLDİRİYOR — böylece geniş liste tuzak değil okunur olur
  — ve entegrasyon testi `public_only`'nin servisin kendi listelemesiyle birebir
  aynı kümeyi döndürdüğünü sabitliyor.

  **En keskin ölçüm mutasyondan geldi.** SQL'deki kategori EXISTS altsorgusu
  etkisizleştirildiğinde SAHTE depoya dayanan bütün birim takımı YEŞİL kalıyor
  ve yalnızca yeni entegrasyon testi kırmızıya dönüyor. Entegrasyon testinin
  neden var olmak zorunda olduğu iddia edilmedi, ÖLÇÜLDÜ: sahte de sorgu da aynı
  niyetten aynı elle yazıldığı için ikisi BİRLİKTE yanlış olup yine de birbirine
  uyabilir.

  Panel tarafında süzgeç adres çubuğunda taşınıyor — daraltılmış katalog yer
  imine eklenip başkasına gönderilebilen bir görünümdür — ve sayfalama bağları da
  onu taşıyor, yoksa "sonraki sayfa" okuyucuyu hiçbir şey söylemeden süzülmemiş
  kataloğa taşırdı. Sözlük İKİNCİ bir okuma ve başarısızlığı SAYFAYI değil
  KONTROLÜ düşürüyor: satırlar zaten elde ve doğru, bir açılır liste
  dolduramadığı için "Katalog okunamadı" demek ekranı bir kolaylık uğruna elden
  almak olurdu. Ama sessizce de düşmüyor — "sözlük okunamadı", "sözlüğün tamamı
  okundu ve bu kimlik içinde yok" ve "sözlük 200'de kesildi, bu kimlik
  doğrulanamadı" ÜÇ AYRI cümle, çünkü üçü üç ayrı fakt; tek bir "bozuk"
  bayrağına indirmek, var olan bir kategori için "böyle bir kategori yok"
  dedirtirdi.

  **Elle kopyalanan ad sabitlendi** (`internal/arch/constants_test.go`):
  `TestThePanelCatalogNamesAgree` panelin `category` dizgisini modülün kendi
  sabitine derleme zamanında bağlıyor; yeni pin mutasyonla kanıtlandı. SÜZGEÇ
  adı bağlanamıyor — `category_id` panelde de modülde de dışa aktarılmamış bir
  sabit, yani karşılaştırılacak bir çift yok. Bu sınır testin kendi
  "kapsamadıkları" bölümüne yazıldı, çünkü kayma bu ekranda tek cinsten değil:
  süzgeç adının kayması GÜRÜLTÜLÜ (sağlayıcı tanımadığı süzgeci reddeder, sayfa
  500 olur), ALAN adının kayması değil — sözlük okuması sayfayı düşürmediği için
  loga bir uyarı ve ekrana bir not olarak gelir, ve alan denetimi KAYIT BAŞINA
  olduğu için hiç kategorisi olmayan bir dükkânda hiç rapor edilmez.

  Yapılmadı: metin araması (`q`). Vitrin listelemesi arıyor, sağlayıcı aramıyor,
  yani panelde arama kutusu yok — bilerek, ve sağlayıcının godoc'una yazılarak:
  tüketicisi olmayan bir yetenek, doğruluğu hiçbir yerde sınanmayan bir yüzeydir
  (ADR 0009). Aynı kural `tag_id` için de geçerli ve orada daha rahatsız edici:
  sunuluyor, birim ve entegrasyon testleriyle örtülü, hiç kimse çağırmıyor —
  panel etiket kontrolü sunmuyor, çünkü etiket serbest metindir ve açılır listesi
  yoktur. Yazıldı; "yapıldı" diye sayılmadı.

  **Metin araması aynı günün ilerleyen saatlerinde yapıldı** — tüketicisi de
  birlikte geldiği için kural çiğnenmedi; bu bölümün başındaki arama kutusu
  girdisine bakın. `tag_id` için yazılan yukarıdaki kural olduğu gibi duruyor:
  hâlâ tüketicisi yok.

- **Bir modülün SQL'i yalnızca KENDİ tablolarını adlandırabiliyor**
  (`internal/arch/module_sql_test.go`).

  ADR 0001 modüller arası doğrudan okumayı yasaklıyordu ama SQL tarafında bunu
  hiçbir şey denetlemiyordu — ve bu, kapatılması en zor kusur sınıfının
  şeklidir: **ihlal ÇALIŞIR.** Her modül aynı bağlantı havuzunu alıyor, yani
  product'ın sorgusuna `JOIN inventory_levels` yazmak bugün doğru sonuç verir,
  hiçbir test kırılmaz, ve bağ ancak envanter kendi veritabanına ya da kendi
  servisine taşındığı gün "relation does not exist" olarak ortaya çıkar.

  Sahiplik haritası TAHMİN edilmiyor, migration'lardan türetiliyor: **71 tablo,
  16 modül.** Ölçümün kendisi bir şeyi düzeltti — `sales_channel` product'ın
  değil AUTH'un tablosu.

  KULLANIM iki değil ÜÇ yüzeyden okunuyor: sorgu dosyaları, modülün KENDİ
  migration'ları ve Go dizgi sabitleri. Migration'ların dahil edilmesi sonradan
  eklenen bir titizlik değil, kapının varlık sebebi: modülün SQL'inin üçte
  birini okumayan bir kural, tam da kapatmaya çalıştığı boşluğun aynısını
  bırakır ve "modüller arası okumanı bir backfill olarak yaz" açık bir kapı
  olurdu. Go tarafında sabitler katlanıyor (dizgi + aynı paketteki sabit), yani
  elle yazılmış kanal kapsamlı sorgu da GÖRÜLÜYOR.

  **Kapı bugünkü ağaçta GEÇİYOR ve muafiyet defteri BOŞ.** Bu depoda modüller
  arası SQL okuması yok; kural bunu söyleyebilmek için gevşetilmedi ve tek bir
  testdata satırı eklenmedi.

  Yazılmadan önce iki körlük bulundu, ikisi de kapı hiç iş görmeden:

  - Çıkarıcı "adı takip eden `(` bir fonksiyon çağrısıdır" kuralını her cümleye
    uyguluyordu, yani `INSERT INTO price (id, amount)` hedefini yutuyordu. Bu
    depodaki her insert o biçimde yazılı — kapı, ihlalin EN GÜRÜLTÜLÜ yarısına,
    modüller arası YAZMAYA kör olacaktı ve çalışıyor gibi görünecekti.
  - Sabit katlama derinliği 12 diye tahmin edilmişti; gerçek en derin zincir 23.
    Yani gerçek ifadeler kırpılıyor ve kuyruk yerine bir yer tutucu okunuyordu —
    sessizce. Sınır 64 oldu ve çarpılırsa kapı AÇIKLAMAYLA DÜŞÜYOR; sonu
    kesilmiş bir sorguyu okumaktansa durmak doğrusu.

  Link tablosunu okumak muafiyetle değil YAPI GEREĞİ serbest kalıyor: `core/link`
  o tabloları çalışma zamanında yaratıyor (ADR 0005), yani hiçbir migration
  sahiplenmiyor ve "kimsenin sahibi olmadığı" tablo serbest. Bir kontrol testi
  link tablosunun raporlanMADIĞINI iddia ediyor — gelecekteki bir okuyucu kapıyı
  "düzeltmek" için onu yasaklarsa, test ona vitrinin satış kanalı süzgecini
  sildiğini söyleyecek.

  Kapsam dışı bırakılan iki şey de ölçülerek bırakıldı: test dosyaları taranmıyor
  (modül ağaçlarındaki 183 SQL biçimli test sabitinin hiçbiri başka modülün
  tablosunu adlandırmıyor, ve birkaç modüle yayılan bir entegrasyon testinin
  hepsinde durum hazırlaması meşru), eklenti tablolarının sahibi de yok — sonuncu
  gerçek bir delik ve kapatılmadı, adıyla yazıldı.

- **Bir yapı dosyasındaki her `-run` deseni gerçek bir test adlandırmak zorunda**
  (`internal/arch/build_files_test.go`).

  Yukarıdaki `make load-test` arızasının sınıf kapısı. `go test` eşleşmeyen bir
  seçiciye sıfır çıkış koduyla "no tests to run" der, yani bir yapı dosyasındaki
  ölü seçici HİÇBİR YERDE hata olarak görünmez — CI'da ise bedeli en yüksek
  yerdedir: yeşil bir adım, koşmayan bir test.

  Kapı hem Makefile'ı hem `.github/workflows` altındaki iş akışlarını okuyor ve
  her `-run` desenini deponun gerçek test, benchmark, fuzz hedefi ve örnek
  adlarına karşı çözüyor. Tek kabul edilen istisna, boş seçicinin `-bench` ile
  eşlendiği hâl — benchmark hedeflerinin "test yok, yalnızca benchmark" deme
  biçimi bu; `-benchmem` tek başına bunu satın almıyor ve o ayrım mutasyonla
  kanıtlandı.

- **Satılan SATIR artık okunabiliyor: `order_line_item` varlığı ve panelde
  Satışlar bölümü** (B14).

  Sipariş modülü okuma katmanına İKİNCİ bir varlık sunuyor — bir modül birden
  çok varlık sunabilir, fulfillment kargo seçeneğinin yanında gönderiyi zaten
  öyle sunuyor. Gerekçesi `order` varlığının kendi belgesinde yazılıydı:
  satırlar sipariş başına değişken uzunlukta, sayfalanmamış bir kümedir ve bir
  Record'a gömülmeleri okuma katmanının "kayıt başına tek id" sözleşmesini
  kırardı. O doğruydu ve bir delik bırakmıştı: hiçbir şey "hangi dönemde hangi
  varyanttan kaç adet satıldı" diye soramıyordu, çünkü siparişin süzgeçleri
  id/customer_id/region_id/status — tarih yok, satırlara inen yol yok.

  Tarih süzgeci (`placed_from`/`placed_to`) YARI AÇIK ve satırın kendi
  `created_at`'ine değil, join ile SİPARİŞİN `placed_at`'ine bakıyor. Satırın
  `created_at`'i satırın YAZILDIĞI gündür: mevcut bir siparişe değişimle eklenen
  satır değişimin gününü taşır ve taze bir satış gibi okunurdu. Tarihi satıra
  KOPYALAMAK daha ucuz olurdu (tek tablo üzerinde aralık taraması), alınmadı:
  tek fakt için ikinci bir doğruluk kaynağı olurdu ve şemada onu doğru tutacak
  hiçbir şey yok — doğduğu anda doğru, sonrasında sessizce ayrışabilir. 000006
  migration'ı iki indeks getiriyor (`orders_placed_at_idx`,
  `order_line_items_variant_idx`), böylece "geçen ay" sorusunun bedeli bütün
  satış geçmişi değil, o ay oluyor.

  **Tüketicisi var, ve tüketici panelde.** Dal, tüketicisi olmadığı için main'e
  alınmamıştı — bu deponun adını koyduğu hata sınıfı (ADR 0009).
  `/admin/ui/sales` bir dönemde satılan satırları yeniden eskiye listeliyor;
  dönem adres çubuğunda taşınıyor, yani sayfa yer imine eklenip başkasına
  gönderilebiliyor. İki okuma yapıyor: önce satırlar, sonra arkalarındaki
  siparişler TEK toplu istekle — okuma katmanı BAĞLAR üzerinden join yapar ve
  tek modülün iki varlığı birbirine bağlı değildir; alternatif satır başına
  okumaydı, yani okuma katmanının önlemek için var olduğu N+1. Süzgecin üst
  sınırı dışlayan, ekranın bastığı son gün ise KAPSAYAN gün: operatörün yazdığı
  gün ile raporun kapsadığı gün ayrışamıyor.

  **TOPLAM YOK, varyant başına özet YOK — bilerek.** Okuma katmanı hiç toplama
  yapmıyor (sağlayıcı KAYIT döner, toplam değil), elde olan tek şey bir SAYFA:
  en çok 25 satır. Onların toplamı "Satışlar" başlığı altında dönemin cirosu
  diye okunurdu, oysa yalnızca sıralamada öne düşen 25 satırın toplamı olurdu.
  Operatörün göremediği yanlış bir sayı, olmayan sayıdan kötüdür: olmayan toplam
  birini sorguyu yazmaya yollar, yanlış olan soruyu bitirir.

  **ÖLÇÜLEN: bu SQL bugüne kadar bir kez bile koşturulmamıştı.** Dalın kendi
  commit'i itiraf ediyordu — join, yarı açık aralık, sıralama ve silinmiş
  sipariş koşulu yalnızca sqlc'nin tip denetiminden geçmişti; birim testleri
  SAHTE deponun öyle davrandığını kanıtlıyordu, sorgunun değil. Sahte, sorguyla
  aynı niyetten aynı elle yazıldığı için ikisi BİRLİKTE yanlış olup yine de
  birbirine uyabilir. Sekiz entegrasyon testi gerçek PostgreSQL'e karşı koştu ve
  sahtenin söyleyemeyeceği dört şeyi söyledi: verilmeyen bir kriterin `IS NULL`
  dalının "sorulmadı" demek olduğunu ("hiçbir şeyle eşleşme" değil —
  SQL'de NULL karşılaştırması true değil NULL'dur), aralığın satırın değil
  siparişin anına baktığını (sahte iki damgayı aynı struct'ta tutar, ayırt
  edemez), silinmiş bir siparişin CANLI satırlarının iki okumada da
  gizlendiğini, ve bir dizinin tek deyimde `= ANY` ile bağlandığını. İndekslerin
  yalnızca ADI değil TANIMI da `pg_indexes`'ten doğrulandı: yanlış sütuna
  kurulmuş ya da kısmi olmayan bir indeks, adı tutan ama işi görmeyen bir indeks
  olurdu.

  Yapılmadı: toplama yüzeyi; satır ↔ varyant BAĞI yok, o yüzden bir Graph isteği
  satılan satırdan ürüne genişleyemiyor (satır, varyant kimliğini başka bir
  modülün kimliği olarak taşır ve doğrulamaz — Prensip 2.2); ve tahminin
  kendisi. Bu, tahminin ihtiyaç duyduğu OKUMA yüzeyi — tahmin değil. Öteki
  yarısı hâlâ B7'de: stok bir defter değil, üzerine yazılan bir sütun.

- **Panelin bir çerçevesi ve ikinci bir bölümü var** (stil, menü, siparişler).

  `corehttp.WriteAsset`'in ilk çağıranı geldi. ADR 0011'de panel stili için
  yazılmıştı ve hiç çağrılmamıştı — deponun adını koyduğu "tüketicisi olmayan
  yetenek" sınıfı (ADR 0009). Stil ikiliye gömülü, ETag'i kendi baytlarından
  türetiliyor (yani tarayıcı tam da dosya değiştiğinde yeniden çekiyor) ve
  girişin yanında kimliksiz açılan tek yol o: giriş sayfasının ona ihtiyacı var
  ve stili girişin arkasında kaldığı için stilsiz açılan bir giriş ekranı bir
  çerçeve için kötü bir ilk izlenim.

  Menü, şablondaki işaretlemeden değil Go tarafının verdiği bir LİSTEDEN
  kuruluyor; böylece panele eklenen bir bölüm menüye, kendisini sunan rotanın
  yanında giriyor. Açık bölüm `aria-current` ile işaretleniyor — stilin
  dayandığı şeyle ekran okuyucunun duyurduğu şey aynı, ayrışabilecek iki fakt
  değil.

  **Canlı koşum gerçek bir kusur buldu.** Sipariş sayfası okuma katmanından tek
  siparişi kimliğiyle istedi ve 422 aldı: sipariş Query sağlayıcısı
  `customer_id`, `region_id` ve `status` sunuyordu ama `id` sunmuyordu — ürün
  sağlayıcısı ise sunuyor. Yetenek vardı (`FetchByIDs`, genişletme yolu),
  süzgeç yoktu. Artık var; toplu biçimleriyle ve başka bir süzgeçle
  birleştirilmeyi reddederek — kısa devre toplu okumadan cevaplıyor, yani ikinci
  süzgeç sessizce yok sayılırdı.

  Müşteriler ve envanter de eklendi: panel artık bir dükkân operatörünün
  gerçekten baktığı dört şeyi kapsıyor — katalog, siparişler, müşteriler,
  envanter. Müşteri ekranı kayıtlı hesabı misafirden ayırıyor (satırdaki başka
  hiçbir şey, hangi tür kayıt olduğu bilinmeden düşünülen anlama gelmiyor).
  Envanter listesi, toplam tam sayı olarak OKUNAMADIĞINDA 0 değil "unknown"
  yazıyor: gerçekte okunamamış bir değeri sıfır diye basmak, birini rafta duran
  stoğu aramaya yollar.

  Envanterin listesi var, detay sayfası YOK ve bu bir karar: bir kalemin detayı
  konum bazlı seviyeleridir ve panel onları zaten varyant sayfasında gösteriyor;
  tek kaleme kimlikle ulaşmak ise envanter sağlayıcısının sunmadığı bir süzgeç
  isterdi. Başka bir ekranı tekrarlayan bir ekran için bir modülün yayımlanmış
  sözleşmesini genişletmek değer bir takas değil.

  Açık kalan: on bir modülün ekranı yok — hepsi günlük iş değil YAPILANDIRMA
  (bölgeler, vergi oranları, kargo seçenekleri, promosyonlar, anahtarlar) —
  panelden hiçbir şey yaratılıp silinemiyor, ve bir eklentinin ekran
  ekleyebileceği uzatma noktası yok.

- **Fatura modülü** (ADR 0024) — belge, satırları, tarafları, durumu ve
  **boşluksuz** numaralandırması.

  Envanterin "çerçevenin hiçbir yerinin dokunmadığı tek madde" dediği şeydi;
  kod tabanındaki her "fatura" bir ADRESTİ.

  **Bir çerçevenin kapatabileceği ve kapatamayacağı kısım.** Tacir adına fatura
  kesemez — bu, tacirin kendi sertifikasını ve bir entegratör sözleşmesini
  gerektirir. Çerçevenin borcu belge, numaralandırma ve iletimin takılacağı
  yuva. İlk ikisi tamam.

  Kararın bulunduğu yer numaralandırma. Fatura numarası, belgeyi yazan İŞLEMİN
  İÇİNDE bir seri SATIRI üzerindeki UPDATE ile alınıyor; sequence ile değil — ki
  bu, sipariş modülünün sipariş numaraları için yaptığının tam tersi ve sebebi
  teknik değil hukuki. Sequence işlemin dışında ilerler, geri alma numarayı
  yakar; sipariş numarasında o boşluk zararsız, fatura serisinde ise vergi
  idaresinin "kesilip sonra saklanmış belge" diye okuduğu şey.

  Üç sonuç doğuyor ve üçü de zorunlu: **taslak durumu yok** (taslak bir şeyin
  taslağı olmak için numaraya ihtiyaç duyar, ve terk edilen bir taslağa verilen
  numara boşluğun ta kendisi), **iptal edilen belge numarasını koruyor ve
  tabloda kalıyor** (silmek boşluğu öbür uçtan açardı), **kesilen belge
  değişmez** (bir hata iptal + yeni belgeyle düzeltilir, hukuk da öyle
  yapıyor).

  Yalnızca bir UPDATE var, yanında `SELECT ... FOR UPDATE` YOK: UPDATE satır
  kilidini kendisi alıp işlem sonuna kadar tutuyor ve kilidi aldıktan sonra
  satırı yeniden okuyor. Zaten alınacak bir kilidi önceden almak, koruma gibi
  görünüp hiçbir şey eklemeyen bir koruma olurdu.

  **Eşzamanlılık testi yazım sırasında gerçek bir kusur buldu.** Yeni yılın
  serisini "ara, yoksa yarat, sonra ilerlet" düzeni kendi yarışından
  kurtulamıyor: iki çağıran da hiçbir şey bulup ikisi de INSERT ediyor, biri
  unique ihlali alıyor ve PostgreSQL'de işlem içindeki hata İŞLEMİ ZEHİRLİYOR
  (25P02) — "kazananın satırını oku" telafisinin koşacağı bir şey kalmıyor.
  Artık tek bir `INSERT ... ON CONFLICT` deyimi.

  İki iddia da gerçek veritabanına karşı kanıtlandı ve sahte depoyla
  kanıtlanamaz: başarısız bir kesim numarasını GERİ VERİYOR (numarayı çağıranın
  işleminin dışında commit eden mutasyon testi düşürüyor ve yanan numarayı
  mesajda gösteriyor), ve tek seriye aynı anda yapılan yirmi kesim 1..20
  üretiyor, eksiksiz ve tekrarsız (ON CONFLICT'i kaldıran mutasyon testi
  düşürüyor).

  Bir mutasyon İLK DENEMEDE yakalanmadı ve suç testteydi: yardımcı, gerçek
  numaranın yalnızca ilk üç harfini alıp beklenen diziyi kendisi kuruyordu —
  yani sabiti sabitle karşılaştırıyordu. Artık numaranın kendi dizi kısmını
  okuyor.

- **Sipariş artık tek çağrıyla faturalanıyor** (`POST /admin/v1/orders/{id}/invoice`).

  Derlemeyi bir WORKFLOW yapıyor, çünkü fatura modülü siparişi bilmiyor, sipariş
  modülü de belgeyi (ADR 0001/0006). İlk yazımda workflow fatura modülünün
  tiplerini import etmişti ve arch testi reddetti: bir workflow bir modülü İKİ
  YÖNDE de import edemez. Fatura modülüne ilkel bir interop yüzeyi eklendi —
  taviz değil, mekanizmanın kendisi.

  İki taraf istek gövdesinden, satırlar siparişten geliyor ve bu ayrım keyfi
  değil: satıcının hukuki bilgileri dükkânın kendi yapılandırması, alıcının VKN
  ve vergi dairesi ise bu deponun müşteri modelinde HİÇ YOK. Tahmin eden bir
  çerçeve, belgenin yanlış olmaması gereken tek yerinde yanlış belge üretirdi.
  Alıcının e-postası siparişin bildiği tek alan, o dolduruluyor.

  Kargo belgeye SATIR olarak giriyor, çünkü basılışı öyle. Toplamlar taşınmayı
  atlatıyor: belgenin ara toplamı kargoyu taşıyor ve genel toplamı siparişin
  toplamının aynısı kalıyor.

  **İki kez kesmek ikinci numarayı harcamıyor.** Sipariş–fatura bağı önce
  okunuyor ve var olan belge dönüyor; 201 yerine 200 ile, ki zaman aşımından
  sonra yeniden deneyen bir istemci ilk denemesinin tuttuğunu anlayabilsin.
  Kalan pencere iddia edilerek kapatılmadı, yazıldı: aynı anda basan iki operatör
  ikisi de kesebilir, ve ikinci bağ kardinalite çakışması olarak REDDEDİLİR —
  yani dükkâna, iki kimlikle birlikte, iptal edilecek bir belgesi olduğu
  söylenir.

  Uçtan uca test HTTP zincirini kanıtlıyor: sipariş modülü akışı konteynerden
  ADLA çözüyor, akış fatura modülünün yüzeyini başka bir adla çözüyor, bağ
  çekirdeğin link servisinden geçiyor. Adlardaki bir harf hatası derlenir ve
  ilk istekte 500 döner; testi o harf hatasıyla koşturmak testi düşürüyor.

  Seri ön ekinde rakam yasağı KALDIRILDI. İlk yazımda "üç harf" diye
  kısıtlamıştım; entegratörler ayrışıyor, kuralın sahibi çerçeve değil ve
  dükkânın kendi entegratörünün kabul ettiği bir ön eki reddetmek, dükkânın
  etrafından dolaşamayacağı bir yanlış olurdu. Uzunluk kesin kaldı — onu numara
  biçimi gerçekten sahipleniyor.

  Açık kalan: iletimin kendisi. O bir eklentinin işi — tacirin sertifikasını ve
  entegratör sözleşmesini gerektirir — ve gelen yok.

- **Sipariş satırı artık hangi ORANDA vergilendiğini söylüyor** (`tax_rate_bps`).

  Hesaplanan oran sınırda atılıyordu: sipariş vergi TUTARINI saklıyor, oranı
  saklamıyordu. Tutardan orana geri dönülemez — vergi satır başına AŞAĞI
  yuvarlanıyor, yani 1899 kuruş hem %20'nin hem %19,99'un ürettiği şey. Fatura
  her satırın oranını basar ve müşterinin ALTINDA ücretlendirildiği oranı
  basmak zorundadır; Türkiye'de KDV oranı e-faturanın türetilmiş değil ZORUNLU
  bir alanı. Kolon, henüz fatura yokken bu yüzden açıldı.

  Her iki vergi yolu da yazıyor: bölgenin düz oranı ve tax modülünün ürün
  bazlı kuralları. Oran sepetin hesabından checkout planına, oradan JSON
  interop sınırını geçip `order_line_items.tax_rate_bps`'e gidiyor.

  Oran DÖRT elden geçiyor — girdi, model, INSERT parametreleri, satır dönüşümü
  — ve herhangi birinden düşmesi hâlâ derleniyor, sahte depo kullanan her birim
  testini hâlâ geçiyor. Dördün ikisi gerçekten onsuz yazılmıştı. Sıfır da
  meşru bir oran olduğu için aşağıda hiçbir şey yanlış görünmezdi; ilk işaret,
  vergili bir satıra %0 KDV basan bir fatura olurdu. Entegrasyon testi tek
  siparişte İKİ FARKLI ve varsayılan olmayan oran yazıp geri okuyor, uçtan uca
  test ise saklanan oranın saklanan vergiyi ÜRETEN oran olduğunu doğruluyor.

  NOT NULL DEFAULT 0 seçildi ve bedeli yazıldı: bu kolondan önceki siparişler 0
  alıyor, 0 da meşru bir oran, yani eski satırlarda ikisi ayırt edilemiyor.
  Nullable kolon bu ayrımı korurdu — hiçbir yeni siparişin nil bırakmayacağı bir
  işaretçi ve yalnızca tarihin üretebileceği bir durumu her okuyucunun ele alma
  zorunluluğu pahasına.

- **Derin sayfa artık ucuz** (cursor pagination).

  Offset, veritabanına atladığı her satırı yürütüp ATTIRIR: sayfanın maliyeti
  derinlikle büyür. 52 bin ürün ve listeleme indeksiyle ölçüldü:

  | sayfa | offset | keyset |
  | --- | --- | --- |
  | ilk | 0,31 ms | 0,06 ms |
  | ~5.000 içeride | 4,63 ms | — |
  | ~50.000 içeride | 34,71 ms | 0,08 ms |

  Derin uçtaki 423 kat asıl mesele değil; ŞEKİL öyle. Offset derinlikle
  doğrusal, keyset düz — çünkü sıralama anahtarı bir sayaca değil indeks
  koşuluna giriyor. Büyüyen bir katalog offset'i kötüleştirir, keyset'i
  bulunduğu yerde bırakır.

  Offset KALDIRILMADI ve değişiklik kırıcı DEĞİL. Sayfa numaralı bir yönetim
  ekranının yedinci sayfaya atlaması gerekir ve cursor bunu yapamaz; o ekranın
  gerçekten indiği derinliklerde offset zaten ucuz. İkisi farklı soruları
  cevaplıyor. `after` eklemeli geldi; ikisi birlikte verilirse REDDEDİLİYOR,
  çünkü iki ayrı konum adlandırıyorlar ve ikisini birden onurlandırmak
  hiçbirinin istemediği bir sayfayı döndürürdü.

  **Taşınacak asıl bulgu SQL'in şekli.** Sınırın apaçık yazımı —
  `@after IS NULL OR (created_at, id) < (...)` — kusursuz ölçülür ve sonra
  bozulur: Postgres bir ifadeyi ilk beş yürütmede çağrı başına planlar ve OR'u
  katlar, yani test Index Cond görür; altıncıda GENERIC plana geçer, OR bir
  Filter olarak hayatta kalır ve arama tam indeks yürüyüşüne döner. Derin uçta
  ölçüldü: 50.001 satır filtreyle atıldı, 0,065 ms yerine 4,3 ms — ve o anda
  kodda hiçbir şey değişmiyor. Sentinel biçiminde
  (`COALESCE(@after, 'infinity')`) hayatta kalacak OR yok; karşılaştırma her iki
  planda da indekse karşı bir ROW kalıyor. Entegrasyon testi süreyi değil PLANI
  okuyor, çünkü küçük bir tabloda süre ikisini birbirinden ayırt edemez.

  Cursor içinde ait olduğu listenin adını taşıyor: başka bir listeye verilen bir
  cursor temiz çözülür, geçerli bir zaman ve kimlik olur ve sessizce yanlış
  satırları seçerdi — eksik veri gibi okunan bir arıza. İmzalanmadı ve nedeni
  yazıldı: sahte bir cursor, çağıranın zaten okumaya yetkili olduğu listenin
  başka bir sayfasını seçer, yani hiçbir şey kazandırmaz.

  Son sayfa cursor TAŞIMIYOR; bitişin işareti bu yokluk. Hep dönseydi istemci
  bittiğini anlamak için boş bir sayfaya bir istek daha yürümek zorunda kalırdı.

  Yürüyüşün her satırı tam bir kez ziyaret ettiği gerçek veritabanında
  kanıtlandı — ve testin ilk hâli bunu kanıtlamıyordu: ürünler tek tek
  yaratıldığı için damgaları farklıydı, anahtarın kimlik yarısı hiç devreye
  girmiyordu ve onu düşüren mutasyon YAKALANMADI. Test, tüm satırları aynı
  created_at'e çekecek şekilde düzeltildi; şimdi kimlik yarısı taşıyıcı.

  Cursor alan dört liste: ürünler (yönetim ve vitrin, REST ve GraphQL),
  siparişler, müşteriler, sepetler. sqlc'den geçen üçünde risk sorgu değil
  parametrenin yolu — cursor pgtype olarak gidiyor ve konum adlandırmayan bir
  cursor'ın SQL NULL varması gerekiyor; sıfır ZAMAN gitseydi ilk sayfa hatasız
  boş dönerdi. Her modülde o mutasyon ayrı ayrı denendi ve yakalandı.

  **Geri kalan listeler yalnızca offset ile kalıyor ve bu bir karar, artık
  değil.** Offset yalnızca DERİNLİKTE pahalı; yapılandırma boyutundaki bir
  listenin o derinliğe inmesi yok. Vergi oranları, kargo seçenekleri, bölgeler,
  para birimleri, ülkeler, satış kanalları, müşteri grupları yüzlerle sayılıyor;
  oraya cursor koymak tören olurdu ve var olan her parametre sonsuza dek
  belgelenmek, test edilmek ve onurlandırılmak zorunda. Bir sonraki liste için
  kural: **satırlar dükkânın TİCARETİYLE büyüyorsa cursor, YAPILANDIRMASIYLA
  büyüyorsa yalnız offset.** Tek bir ebeveyne bağlı listeler — bir ürünün
  varyantları, bir siparişin iadeleri, bir şirketin çalışanları — ebeveyniyle
  sınırlı olduğu için offset tarafında.

- **Go tarafı artık ölçülüyor** (pprof + benchmark).

  Deponun ölçüm disiplini güçlüydü ama tamamı VERİTABANI tarafındaydı:
  entegrasyon testinin içinde okunan `EXPLAIN`, 52 bin satırlık yük fikstürü,
  godoc'larda gerçek rakamlar (2,9 ms; 0,56 ms; 67 ms → 0,65 ms). Go tarafını
  hiçbir şey ölçmüyordu — depoda tek bir `func Benchmark` yoktu.

  İstek başına koşan beş yol ölçüldü (8845HS):

  | benchmark | işlem başına | tahsisat |
  | --- | --- | --- |
  | `StorefrontQuery` (24 ürün, 3'er varyant) | 374 µs | 8.421 |
  | `ComputeDiscounts` (20 satır, 4 promosyon) | 4,5 µs | 18 |
  | `AllocateAcross` (20 satır) | 1,3 µs | 2 |
  | `AssembleTotals` (20 satır) | 124 ns | 0 |
  | `ApplyTaxResponse` (20 satır) | 89 ns | 0 |

  Bulgu aradaki fark. En dikkatle yazılan kısım — kuruş artığı kuralları satır
  satır tartışılan sepet aritmetiği — HİÇ tahsisat yapmıyor. GraphQL okuma
  yüzeyi ise üç bin katı tutuyor ve istek başına 8.421 kez tahsisat yapıyor; bu,
  kıyaslandığı veritabanı işiyle aynı büyüklük sırası (sayım sorgusu
  67 ms → 0,65 ms). Bakılacak ilk yer orası, ve bunu bundan önce hiçbir şey
  söyleyemezdi.

  Her benchmark, ÖLÇTÜĞÜ ŞEYİN gerçekten çalıştığını timer başlamadan önce
  doğruluyor: fikstür indirim üretmiyorsa ya da hata dönüyorsa benchmark
  düşüyor. Reddedilme yolunu ölçen bir benchmark gayet iyi bir rakam bildirir ve
  hiçbir anlam taşımaz.

  pprof AYRI bir dinleyicide, API sunucusunun üstünde değil: bir profil
  kendisinden istenen kadar sürer ve `WRITE_TIMEOUT=30s` ilk elde edilen 30
  saniyelik CPU profilini tam ortasından keserdi. Ticaret yüzeyinin yazma
  bütçesi bir hata ayıklama aracı için genişletilemez, araç da 30 saniyeye
  sığdırılamaz; o yüzden aynı sunucuyu paylaşmıyorlar. Yan faydası: muhafız
  yığınındaki hiçbir hata heap dökümünü genel yüzeye koyamaz, çünkü profiller o
  yüzeyde zaten yok.

  Varsayılan KAPALI. Uçlar kimlik doğrulamasız — ayrı dinleyiciyi ucuz kılan şey
  bu — ve bir heap profili canlı belleğin İÇERİĞİNİ taşır: uçuştaki token,
  müşteri verisi, hash'lenmeyi bekleyen parola. Bu yüzden development dışında
  loopback olmayan bir adres açılışta REDDEDİLİR; ":6060" yerel görünüp tüm
  arayüzleri dinlediği için kendi test vakası var.

  `net/http/pprof` yalnızca IMPORT edilerek altı ucu süreç genelindeki varsayılan
  mux'a kaydeder — yani profilleme kelimesi geçmeyen bir kod, ilgisiz bir
  dinleyiciyi heap dökümü dağıtır hale getirebilir. Bir arch testi hem o
  import'u tek dosyaya hapsediyor hem de varsayılan mux'a dokunulmasını
  yasaklıyor; ikisi birlikte denetleniyor çünkü tek başına her biri zararsız.

  `make bench` ile koşuluyor, CI'da `-benchtime=1x` ile bir kez: rakam değil,
  benchmark'ın hâlâ derlendiği ve hâlâ gerçek yolu ölçtüğü doğrulanıyor.
  Kimsenin koşmadığı bir benchmark, deponun adını koyduğu "tüketicisi olmayan
  yetenek" sınıfının ta kendisi olurdu (ADR 0009).

- **Yönetim yazmaları artık iz bırakıyor** (audit log).

  Admin API her yazmayı kimlik doğrulayıp yetkilendiriyor, sonra olduğunu
  unutuyordu: bir değişikliğin tek kalıcı izi satırın `updated_at`'iydi.

  **Kayıt İSTEĞİ tutuyor, DEĞİŞİKLİĞİ değil** — ve envanterin "zor kısım" dediği
  şey buydu. Diff, on beş modülün her yazma için önce/sonra üretmesi demek: on
  beş yerde sözleşme ve her istekte maliyet. Kuru bir "bir ürün güncellendi" ise
  daha ucuz ve hiçbir işe yaramaz. Satırın cevapladığı şey, bir olayın
  başladığı soru: bu yüzeye kim dokundu, ne zaman, ve başarılı oldu mu. NE
  olduğu zaten kaydın kendisinden okunuyor.

  Yalnızca YAZMALAR ve yalnızca yönetim yüzeyi. Okumalar cevapsız hacim:
  birinin siparişleri listelediğini bilmek kimseye yardım etmiyor. Vitrin ise
  kararla kimliksiz (ADR 0008), oradaki satır "biri" der ve hiçbir şey ifade
  etmezdi.

  Denetim kimlik korumasının DIŞINDA duruyor, bu yüzden REDDEDİLEN yazma da
  kaydediliyor: birinin yetkisi olmayan bir şeyi değiştirmeye çalışması, tam da
  bir olayın aradığı satır.

  Başarısız denetim isteği DÜŞÜRMÜYOR. Değişiklik zaten commit edilmiş; yanıtı
  reddetmek hiçbir şeyi geri almaz, yalnızca bir günlükleme arızasını müşteriye
  görünen bir kesintiye çevirirdi. Kalan risk açıkça yazıldı: satırı kaybolan
  değişiklik izsiz kalır, ve o pencereyi kapatmak audit satırının her modülün
  işlemine katılması demek olurdu — ADR 0023'ün olaylar için kabul ettiği
  bağlanma, ki bu kayıt onu hak etmiyor.

  Yazım isteğin bağlamını AŞAN bir bağlamla yapılıyor: istemcinin bağlantıyı
  kesmesi, zaten olmuş bir değişikliğin kaydını kaybetmek için sebep değil — ve
  iptal edilmiş istek, birinin sonradan "ne koştu" diye soracağı andır.

  Dışarıda durmanın bir bedeli var ve ilk sürüm onu ödemeyi unutmuştu: muhafız
  kimliği TÜRETİLMİŞ bir isteğe koyar, dıştaki ara katman elindeki özgün
  isteğiyle kalır. Yani her satır "kimse" diyordu — tablonun var olma sebebi
  olan tek soru cevapsızdı. Birim testleri bunu göremedi çünkü kimliği isteğe
  kendileri koyuyordu; hatayı canlı ikiliye karşı koşmak gösterdi. Kimlik artık
  muhafızın doldurduğu bir yuvayla YUKARI taşınıyor. İdempotency aynı tehlikeyi
  içeri taşınarak çözmüştü ve bunu godoc'unda yazmıştı; ucuz cevap odur, yuva
  yalnızca dışarıda kalmak zorunda olan tek ara katman için var.

  Aktör kimliği TEXT ve foreign key YOK: kimlikler auth modülünün (Prensip 2.2),
  ve silinen bir kullanıcı iz kaydını da götürmemeli — izin en çok gerektiği an
  tam olarak odur.

  Beş mutasyonun beşi de yakalandı; kayıt ayrıca canlı ikiliye karşı doğrulandı
  (201 aktörlü, 401 aktörsüz, okuma denetlenmiyor, yol tam kaynağı gösteriyor).

- **Söz verilen olay artık onu vaat eden işlemin parçası** (outbox, ADR 0023).

  Modül işini commit ediyor, sonra yayımlıyordu. Bu iki an arasında süreç
  ölürse iş var olmuş ama olay hiç gerçekleşmemiş oluyordu: onay maili
  gönderilmiyor ve borçlu olunduğunu hiçbir yer kaydetmiyordu. Event bus kendi
  garantisini dürüstçe yazmış (in-memory at-most-once, Redis at-least-once) ama
  **hiçbiri o pencereyi kapsamıyordu**, çünkü yayın işlemin parçası değildi.
  Depoda "outbox" kelimesi tam bir kez geçiyordu, bir yorumda varsayım olarak.

  Satır artık işlemin parçası: `event_outbox` çekirdeğin, satırı modül KENDİ
  deposundan yazıyor, ve dakikada bir koşan `internal/jobs/outboxrelay`
  yayımlıyor.

  **Yazıcı işlemi bağlamdan değil ARGÜMANDAN alıyor** ve bu seçim değil zorunluluk:
  her modül işlemini kendi DIŞA KAPALI bağlam anahtarında tutuyor, yani çekirdek
  onu göremiyor. Executor'ı geçirmek, çekirdeğe ait bir tablonun modüle ait bir
  işlemin içinde yazılmasını, iki tarafın da diğerinin içini öğrenmesine gerek
  kalmadan sağlıyor.

  **Doğrudan yayın KALDI, hızlı yol olarak.** Satır GARANTİ, doğrudan yayın HIZ:
  abone çoğu siparişi aynı istekte duyuyor, süreç ölürse satır zaten commit
  edilmiş ve röle gönderiyor. İkisi iki olaya dönüşemiyor ve bunu sağlayan şey
  kimlik: ikisi de aynı kimliği taşıyor, siparişten türetiliyor. Olay kimliğine
  göre idempotent bir abone — ki bus'ın at-least-once sözleşmesi bunu ZATEN
  gerektiriyor — ikisini ayırt edemez.

  Outbox yazımındaki hata artık siparişi DÜŞÜRÜYOR. Yayının aksine hata dönüyor
  ve işlem geri alınıyor: olayı olmayan bir sipariş, tam da bu kararın önlemek
  için var olduğu durum, ve sessizce kabul etmek garantiyi var gibi gösterirdi.

  Röle dakikada bir koşuyor ve bu depodaki tek kısa aralık. `sagawatch` ve
  `paymentrecon` bir süredir yanlış olan şeyleri raporluyor, erken bulmak bir
  şey değiştirmiyor; burada GECİKME ZARARIN KENDİSİ — bekleyen şey, parası çoktan
  alınmış bir sipariş hakkında birinin beklediği mesaj.

  Birden fazla kopya aynı anda röle yapabiliyor: satırlar `FOR UPDATE SKIP
  LOCKED` ile alınıyor, kilit işlem bitene kadar duruyor ve KİLİDİN KENDİSİ
  taleptir — sürelenecek bir şey yok, süpürülecek bir şey yok. ADR 0019'un
  lease yerine advisory lock seçerken kullandığı akıl yürütmenin aynısı.

  Şimdilik yalnızca sipariş modülü bu yoldan yazıyor ve bu bilinçli: `order.placed`
  gerçek abonesi olan olay (notification), ve abonesi olmayan yayıncıları
  dönüştürmek ADR 0009'un adını koyduğu hata olurdu.

  Üç mutasyonun üçü de yakalandı — ve üçüncüsü bir test boşluğu ortaya çıkardı:
  deponun işlem denetimini hiçbir birim testi koşmuyordu (servis testleri sahte
  depoyu kullanıyor), o yüzden entegrasyon testi yazıldı.

- **Tarayıcıdaki vitrin artık API'yi çağırabiliyor** (`CORS_ALLOWED_ORIGINS`).

  Vitrin yüzeyinin kimliği publishable key ve o anahtarın kendi belgesi
  *"SIR DEĞİLDİR, tarayıcıda görünmesi beklenir"* diyor. Ama tarayıcı o yüzeyi
  hiç çağıramıyordu: preflight, anahtar okunmadan ölüyordu. Yani uçtan uca
  çalışan tek topoloji — tarayıcıda misafir vitrini — ulaşılamazdı.

  **ADR 0011'in CORS reddi geri alınmadı.** O karar CORS'u *paneli ayrı bir
  uygulama olarak yayımlamanın yolu* olarak reddediyor, gerekçesi de "jeton
  tarayıcıda saklanmak zorunda kalırdı". Admin yüzeyi hâlâ CORS almıyor, tam da
  o sebeple. Açılan şey, anahtarı zaten tarayıcıda yaşamak üzere tasarlanmış
  olan yüzey.

  **Kimlik bilgisi taşımaya ASLA izin verilmiyor** ve karar bu:
  `Access-Control-Allow-Credentials` hiç yazılmıyor. Vitrin yüzeyi bir BAŞLIKTAN
  kimlik doğruluyor, başlığı da tarayıcı kendiliğinden eklemiyor — bu API'nin
  CSRF bağışıklığı tam olarak oradan geliyor (ADR 0011, karar 3). İzin vermek,
  siteler arası bir sayfanın ortamdaki çereze binmesine kapı açardı.

  Varsayılan KAPALI: origin yapılandırılmadıysa hiçbir CORS başlığı yazılmıyor.
  Varsayılan-açık bir politika, kimsenin vermediği bir güvenlik kararıdır.
  `"*"` açıkça yapılandırılabiliyor ve herkese açık bir vitrin API'si için
  dürüst; kimlik bilgisi zaten hiçbir hâlde taşınmadığı için joker de ortam
  çerezine binilemez.

  Sıra yük taşıyor: CORS koruma yığınının BAŞINDA. Preflight ne kimlik ne
  idempotency anahtarı taşır, dolayısıyla izin soran tarayıcı, çağrının izinli
  olup olmadığını öğrenmeden kimlik korumasına takılırdı. Preflight'ı
  korumalardan önce yanıtlamak, yanıtın EKSİK ANAHTAR hakkında değil POLİTİKA
  hakkında olmasını sağlıyor.

  İzin verilen başlık listesi KAPALI, isteğin söylediği yansıtılmıyor: yansıtmak
  izin listesini formaliteye çevirirdi, çünkü soran taraf denetlenen taraftır.
  Ve `Vary: Origin` politika VARSA her yanıta yazılıyor — reddedilenlere de —
  yoksa bir önbellek bir origin'in yanıtını başkasına verirdi.

  Dört mutasyonun dördü de yakalandı.

- **Para iadesi geldi ve ADR 0022'nin açık bıraktığı yarıyı kapattı** — B2B
  bütçe hatası dahil.

  İadeler ödeme modülünün kendi yönetim API'sinden yapılıyordu ve sipariş
  tarafında çağıranı yoktu, dolayısıyla SONRADAN yapılan bir iade siparişin
  özetine hiç ulaşmıyordu. Adı konmuş kurbanı vardı: B2B harcama penceresi
  `order_summaries.refunded_total`'ı düşüyor, yani iade edilmiş bir B2B siparişi
  çalışanın bütçesini geri vermiyordu. Artık veriyor.

  **Çağıran bir TAHSİLAT adlandırmak zorunda değil.** `RefundPayment` bir ödeme
  kimliği istiyor ve modül dışındaki bir çağıranın onu elde etme yolu yok —
  üstelik olmamalı da: tahsil edilmiş tutarın tahsilatlara nasıl bölündüğü bu
  modülün defter tutması. Yeni `RefundCollection` "şu siparişe şu kadarını geri
  ver" cümlesini karşılıyor; tutarı tahsilatlara en eskisinden başlayarak
  yayıyor, ki iade edilebilir bakiye en yeni tahsilatlarda toplansın — sonraki
  kısmi bir iadenin muhtemelen onlarla ilgili olacağı için.

  Plan para HAREKET ETMEDEN kuruluyor: koleksiyonun verebileceğinden fazlası
  reddediliyor. Tersi, elinden geleni iade edip sonra hata dönmek olurdu ve
  çağıranı istemediği kısmi bir iadeyle bırakırdı.

  **Mal önce gelmeli.** Teslim alınmamış iade için para gönderilmiyor: kimsenin
  görmediği mal için para iadesi, çerçevenin dükkân adına seçemeyeceği bir
  politika. Muayeneden önce ödemek isteyen dükkân önce teslim alıp hemen iade
  eder — aynı iki olgu, aynı sırayla, ama açıkça.

  **Sipariş EN SON haberdar ediliyor** ve oradaki hata parayı geri getirmiyor.
  İade bir sağlayıcıya ulaşıyor, özet yazımı yerel; yereli önce yapmak,
  sağlayıcının göndermeyi reddettiği bir parayı kaydetmek olurdu. Ters risk —
  para gitti, kayıt yok — RAPOR ediliyor: sonuçta `summary_recorded` false,
  logda ERROR, ve siparişin koleksiyonuyla karşılaştırılması farkı görünür
  kılıyor (ADR 0020'nin argümanı bir kat yukarıda).

  Siparişe yazılan rakam BU ÇAĞRININ tutarı değil, koleksiyonun YAŞAM BOYU
  toplamı: özet kümülatif, ve delta yazmak ikinci iade var olduğu anda yanlış
  olurdu. Toplam yazmak aynı zamanda tekrarı güvenli kılıyor, çünkü sipariş
  tarafındaki birleştirme büyük olanı tutuyor.

  Kısmen giden para "hiç gitmedi" diye raporlanmıyor: yalnızca hata dönmek
  çağıranı hiçbir şey kaydetmemeye ve tüm tutarı yeniden denemeye — yani
  parçayı ikinci kez göndermeye — iterdi.

  Dört mutasyonun dördü de yakalandı.

- **Talepler (claim) artık çözülüyor — ama yalnızca parayla, ve ötekini
  REDDEDEREK.**

  Talep ya parayla ya da yeni malla çözülüyor. Bu akış birincisini yapıyor.
  İkincisi, var olan bir siparişe karşı mal SEVK ETMEK demek ve çerçevede böyle
  bir yetenek hiçbir yerde yok — o yüzden `replace` türündeki talep sessizce
  "tamamlandı" damgalanmıyor, gerekçesini söyleyen bir çakışmayla REDDEDİLİYOR.
  Damgalamak, müşteriye bir şey gönderilmiş gibi kaydetmek olurdu.

  Sıfır tutar burada iadedekinden BAŞKA bir şey demek: talebin kendi rakamı.
  İadede sıfır "koleksiyonun tamamı" demekti çünkü müşteri siparişini geri
  veriyor; talep ise mutabık kalınan tutarı taşıyor ve koleksiyonun tamamına
  düşmek "şu talebi çöz"ü "siparişi iade et"e çevirirdi.

  Talep EN SON damgalanıyor. Para her hâlükârda gitti; yazılamayan bir damga
  operatörün yeniden çözebileceği bir talep bırakıyor — ki bu görünür — oysa
  önce damgalamak, hiçbir şey gönderilmemişken çözülmüş görünen bir talep
  bırakırdı.

- **Müşteri artık iade talebi açabiliyor** (`POST /store/v1/orders/{id}/returns`).

  Kayıt yalnızca yönetim tarafından açılabiliyordu, yani bir dükkânın müşteriye
  iade başlatma imkânı hiç yoktu.

  Yetkilendirme sınırı kardeş ucuyla aynı ve aynı gerekçeyle: siparişin talep
  eden müşteriye ait olduğunu doğrulamak GÖMEN UYGULAMANIN işi (ADR 0008).
  Buradaki bedeli sınırlı ve söylenmeye değer: talep TALEPTİR — ne stok ne para
  kımıldıyor, bir operatör teslim almadan hiçbir şey olmuyor, ve adet kuralı
  zaten alınandan fazlasını reddediyor. EYLEYEN uçlar admin ve kapsamlı.

  **Müşteri satır adlandırıyor, tutar değil.** İade tutarı sıfır bırakılıp
  dükkâna bırakılıyor: gövdenin "bu iade ne eder" diyebilmesi, müşterinin kendi
  iadesine karar vermesi olurdu — kargo fiyatı deliğinin başka bir yerdeki
  hâli. Alan gövdede hiç yok ve API tanımadığı alanı reddediyor.

  İptal TALEBİ bilerek hâlâ yok: iptal paraya ve stoğa uzanıyor, üstelik parası
  alınmış sipariş zaten iptal edilemiyor — geri dönüş yolu iade.

  Üç mutasyonun üçü de yakalandı. Değişim (exchange) tamamlama ve `replace`
  talebi BİLEREK yapılmadı: ikisi de var olmayan yetenekler istiyor (mevcut
  siparişe karşı mal sevki, ve pozitif farkın tahsili — ki `order_payment`
  bire-bir kardinalitesi bugün onu zaten engelliyor). Tüketicisi olmayan
  yetenek inşa etmek ADR 0009'un adını koyduğu hata.

- **İade artık EYLİYOR: teslim alınan malın stoğu geri konuyor**
  (`internal/workflows/returns`, satış sonrası 2/3).

  Satış sonrası kayıtları açılabiliyor ve okunabiliyordu, başka hiçbir şey
  yapamıyordu; `aftersales.go` bunu yazmış ve eylemeyi "sonraki fazlara"
  ertelemişti. O fazları içeren yol haritası kapandı.

  **Mal gelmesi para iadesi DEĞİLDİR ve akış burada duruyor.** Para iadesi
  operatörün gelen mala bakarak verdiği ayrı bir karar — mal hasarlı, eksik ya
  da pencere dışında gelebilir — ve teslim alınca otomatik iade etmek dükkânın
  vermesi gereken kararı çerçevenin vermesi olurdu. İkisi maliyet olarak da
  simetrik değil: stok, paket açıldığı anda fizikî bir olgu; para ise biri
  gönderene kadar geri alınabilir.

  **Önce kayıt, sonra stok**, ve gerekçesi şu: ikisi de diğerini geri
  alamıyor, dolayısıyla soru hangi arızanın elle bitirilebileceği. Damgalanmış
  ama stoğu konmamış bir iade, neyin eksik olduğunu ve nereye gitmesi
  gerektiğini YAZILI olarak söylüyor; stok eklenmiş ama kaydı olmayan bir
  varış ise sayımı bozuyor ve malın geldiğine dair tek kanıt hiç yazılmamış
  oluyor. "Fazlasını iddia eden kayıt" ile "kimsenin açıklayamadığı depo
  sayımı" arasında birincisi düzeltilebilir olan.

  Stok geri koyma hataları çağrıyı DÜŞÜRMÜYOR, uyarı olarak dönüyor: mal zaten
  binada, ve hata dönmek operatöre kolisi elindeyken "teslim alma olmadı"
  demek olurdu — üstelik çalışması gereken kayıt hiç var olmazdı. Bir satırın
  geri konamaması diğerlerini de durdurmuyor; ayrı raflardaki ayrı ürünler,
  bir arızayı ikiye çıkarmanın anlamı yok.

  `Restock` BİLEREK idempotent değil — iki çağrı iki fizikî varış demek. Tek
  çağrıyı garanti eden şey akışın kendisi: iade bir kez teslim alınabiliyor ve
  akış statüyü önce kontrol edip duruyor. Yani modülün geçiş tablosu,
  envanterin idempotent olmamasını güvenli kılıyor.

  Uç servise değil AKIŞA bağlı ve kapalı arızalanıyor: akış yoksa iade hiç
  teslim alınmıyor. Kayıt yazıp stoğu atlamak, malı depoya koyup sayımın
  "burada değil" demesine, üstelik kaydın "başarılı" iddia etmesine yol
  açardı — cart modülünün satır fiyatı için yazdığı kuralın aynısı.

  Arch kapısı bu turda İKİ KEZ durdurdu: kaydedilmemiş akış, ve tüketicisi
  olmayan interop. İkisi de aynı hata sınıfı ve ikisini de kod yakaladı.

  Beş mutasyonun beşi de yakalandı.

- **Sipariş ile ödemesi arasında artık bir yol var** (`order_payment` link'i).

  Koleksiyonun `Reference` alanı sepet kimliğini taşıyor, yani bir siparişten
  onun için tahsil edilen paraya giden hiçbir yol yoktu: "bu siparişe ne
  ödendi" diye soran operatörün, siparişin hangi sepetten geldiğini önceden
  bilmesi gerekiyordu. İki godoc `"order_payment"` link'ini adıyla anıyordu —
  biri ödeme modülünün query sağlayıcısında ("bir sipariş listesi siparişin
  ödeme durumunu bu sağlayıcı ve order_payment link'i üzerinden görür"), biri
  sipariş modülünün paket belgesinde — ve **hiçbir şey onu tanımlamıyordu.**
  Vaadin ne tüketicisi ne üreticisi vardı.

  Tanımı hangi modülün yazacağına depo kendi kuralıyla karar vermiş: sipariş
  modülünün paket belgesi *"tanım, bağın taşıdığı kaydı YAZAN taraf tarafından
  bildirilir — payment, fulfillment"* diyor, ve bir tanım ADR 0005 gereği
  yalnızca BİR KEZ bildirilebilir. Koleksiyon ödeme modülünün kaydı, dolayısıyla
  tanım orada.

  `Reference` alanını siparişe çevirmek seçilmedi: alanın kendi godoc'u zaten
  *"Prensip 2.2 — bağ Module Links üzerinden kurulur"* diyor, ve anlamını
  değiştirmek mevcut her satırdaki sepet anlamını bozar, üstelik ilişkiyi
  sipariş tarafından sorgulanabilir yapmazdı.

  Bağ saga'da, koleksiyon açıldıktan hemen SONRA ve yetkilendirmeden ÖNCE
  kuruluyor. Yer seçimi bilinçli: o noktada kartta henüz bloke yok, dolayısıyla
  yazılamayan bir bağın bedeli yalnızca geri alınan bir rezervasyon —
  boş-kimlik denetimlerinin kullandığı aynı ucuz kırılma noktası. Sonrasına
  bırakmak, bloke edilmiş bir kartla ulaşılamayan bir ödeme arasında seçim
  yapmak olurdu.

  Telafi bağı SİLMİYOR: geri alınan saga siparişi iptal ediyor ama koleksiyon
  ayakta kalıyor ("koleksiyon para tutmaz, yalnızca 'şu kadar tahsil edilecekti'
  diyen bir defter satırıdır"). Bağ o satırın hangi siparişe ait olduğunu
  söyleyen şey; silmek gerçekten olmuş bir denemenin izini silmek olurdu.

  Kardinalite bire bir, bugün doğru olan EN SIKI kısıt: saga sipariş başına tam
  bir koleksiyon açıyor. Deponun kendi ilkesi bu yönde — bildirilmemiş
  kardinalite en sıkısına düşer, çünkü fazla sıkı bir kısıt YÜKSEK SESLE
  patlarken fazla gevşek olanı yanlış satırı sessizce içeri alır. Yeniden
  açılma koşulu şemada zaten görünür: `difference_due` pozitif olan bir değişim,
  var olan bir siparişe karşı tahsil edilen paradır.

  **Arch kapısı bu turda bir tasarım hatası yakaladı ve haklıydı.**
  `TestTheLinkDefinitionsAreTraversed`: bildirilen ve YAZILAN ama hiç OKUNMAYAN
  bir link veriyi yazar, maliyetini öder ve hiçbir davranış üretmez — "satış
  kanalı hatası tam olarak buydu". İlk hâlinde üretici vardı, tüketici yoktu;
  yani bu oturumun peşinde olduğu hata sınıfının ta kendisi, bu sefer benim
  elimden.

  Okuyucu uydurulmadı, godoc'un zaten verdiği söz yerine getirildi:
  `GET /admin/v1/orders/{id}/payment` siparişin canlı ödeme koleksiyonunu link
  üzerinden okuyor. Sipariş detayına alan olarak EKLENMEDİ — her sipariş
  okumasını, çoğu zaman gerekmeyen bir yolda başka bir modüle uzatırdı.

  İkisinin birden olması çoğaltma değil, amacın kendisi: sipariş kendi kaydında
  ne ödendiğine İNANDIĞINI taşıyor (ADR 0022), bu uç ödeme modülünün ŞU AN ne
  dediğini veriyor. Elinde ikisi olan operatör kaydedilmiş ödemeyi gerçek
  olandan ayırabiliyor — ADR 0020'nin oturum ile sağlayıcı için kurduğu
  argümanın aynısı. "Sipariş yok" ile "bu siparişin ödemesi yok" da ayrı
  kodlarla ayrılıyor, çünkü saga koleksiyonu siparişten SONRA bağlıyor ve
  arada ölen bir ödeme gerçek bir durum.

  Üç mutasyonun üçü de yakalandı. Bu, iade akışının (2/3) önkoşuluydu ve ADR
  0022'nin açık bıraktığı iade yarısının da yolunu açıyor.

- **İade kaydı artık kımıldayabiliyor ve hangi satırların geldiğini söylüyor**
  (satış sonrası, 1/3).

  Üç kayıt tipi de — iade, değişim, talep — `requested` doğuyor ve orada
  kalıyordu: üç sorgu dosyasında SIFIR `UPDATE` vardı ve iskelet bunu açıkça
  yazmıştı, geçişleri "sonraki fazlara" erteleyerek. Yol haritası kapandı, faz
  gelmedi.

  Bu tur o üçünün de geçiş tablosunu yazıyor
  (`models/aftersales_status.go`), ödeme modülünün oturum durum makinesiyle
  aynı desende: saf, veritabanısız fonksiyonlar, ve servis yalnızca sonucu
  tipli hataya çeviriyor. Üç yanıt var — `proceed`, `noop`, `conflict` — ve
  `noop`'un ayrı olması işin özü: ikinci kez "teslim alındı" demek yeniden
  yazmak DEĞİL, çünkü `received_at` malın geldiği andır ve yeniden damgalamak
  kaydı "biri ikinci kez tıkladığında geldi" der hâle getirirdi.

  Tablodaki ağırlıklı satır: **teslim alınmış iade geri çekilemez.** Mal fizikî
  olarak depoda ve kayıt onun nereden geldiğini söyleyen tek şey; talebi geri
  çekmek malı geri göndermez.

  **Satır kaydı geldi** (`order_return_items`). 000001 bunu bilerek dışarıda
  bırakmıştı — "akış yazılmadan tasarlanan çocuk şema, akış gelince değişir" —
  ve o gerekçe artık tükendi: akış sıradaki adım, şema da onun yapacağı iki
  işten türetildi. Stoğu geri koymak SATIR ve ADET ister, parayı geri ödemek
  TUTAR ister; başka bir şey yok, o yüzden başka sütun da yok. Varyant sütunu
  yok: işaret ettiği sipariş satırı onu zaten taşıyor ve sipariş satırı
  yazıldıktan sonra değişmez.

  Satırlar arası kural servise kondu ve sebebi yapısal: **bir CHECK kendi
  satırından başkasını göremez.** "Alınandan fazlası iade edilemez" kuralı aynı
  satırın DİĞER canlı iadelerine bağlı, o yüzden toplam siparişin kilidi altında
  okunuyor. Geri çekilmiş iade birimlerini serbest bırakıyor; teslim alınmış ve
  hâlâ istenen iadeler saymaya devam ediyor — iki açık talep yoksa her biri
  satırın tamamını isteyip birlikte alınandan fazlasını iade ettirebilirdi.

  Dört mutasyonun dördü de yakalandı. Sıradaki adım iade akışı: stoğu geri
  ekleme ve parayı iade etme, saga olarak.

- **Parası alınmış sipariş artık iptal edilemiyor** — ediliyordu, ve iptal
  hiçbir şeyi geri almıyordu.

  `CancelOrder`'ın godoc'u doğru kuralı zaten yazmıştı: *"tamamlanmış siparişin
  parası tahsil edilmiştir; 'iptal edildi' damgası vurmak, tahsil edilmiş bir
  tutarı hiçbir siparişe bağlı olmayan bir tutara çevirirdi."* Ama kural STATÜYE
  bakıyordu, ve statü o olgunun vekili değil.

  Ölçüm: **checkout saga'sı yerleştirdiği siparişi hiç tamamlamıyor** —
  `CompleteOrder`'ı bir admin ucundan başka çağıran yok. Yani parası alınmış,
  stoğu düşülmüş, sepeti kapanmış bir sipariş, operatör "tamamlandı" işaretini
  koyana kadar `pending` oturuyor. `POST /admin/v1/orders/{id}/cancel` onu
  damgalayabiliyordu: iade yok, stok geri gelmiyor (rezervasyonlar ödeme anında
  ONAYLANIYOR, yani stok tutulmuyor DÜŞÜLÜYOR), olay yayımlanmıyor.

  Denetim artık vekile değil OLGUYA bağlı: tahsil edilmiş tutarı olan sipariş
  iptal edilemiyor, hata mesajı da doğru yolu adıyla söylüyor. Bu ancak ADR
  0022'den sonra mümkün oldu — öncesinde `paid_total` her siparişte sıfırdı ve
  bu denetim dekordan ibaret kalırdı.

  Saga telafisini ENGELLEMİYOR: yakalama denendikten sonra telafi zaten
  tümüyle atlanıyor (`skipAfterCapture`) ve özet yakalamadan SONRA yazılıyor,
  dolayısıyla telafi eden bir saga buraya hep sıfır tutarla geliyor. Bir test
  bunu ayrıca sabitliyor.

  İade edilmiş sipariş de iptal edilemiyor ve bu bilinçli: `PaidTotal` hiç
  küçülmüyor — iade onun yanına yazılıyor, ondan düşülmüyor — yani parası alınıp
  tamamen iade edilmiş sipariş hâlâ tahsil edilmiş tutar taşıyor. Para iki kez
  hareket etti ve iki hareket de o siparişe ait.

  Kalan pencere yazıldı: parası hareket etmiş ama özeti hiç yazılmamış sipariş
  (saga yakalamayla defter tutma arasında ölmüşse) bu denetimden geçiyor. ADR
  0020 ile 0022'nin ikisinin de açık bıraktığı aynı pencere, ve `gobit stuck`
  onu raporluyor; onu ödenmemiş bir siparişten ayıran yerel bir olgu yok.

- **Vergi artık doğru girdilerden hesaplanıyor** — karışık sepet baştan sona en
  yüksek orandan vergileniyordu.

  Vergi modülü eksiksiz ve kurallarını ÜRÜN üstünden eşliyor. Sepet dikişi ise
  her kalemi yalnızca kimlik ve tutarla kuruyordu; `product_id` alanı şemada
  vardı ve BOŞ gidiyordu, gerekçesi de "bu fazda: sepet satırı varyantı bilir,
  ürünü bilmez". Sonuç: her satır bölgenin VARSAYILAN oranına düşüyordu. %1
  kitap, %8 gıda ve %20 elektronik karışık bir sepet baştan sona %20 ödüyordu ve
  yanıtta bunu söyleyen hiçbir şey yoktu.

  Oysa `product_id` varyant kaydında zaten var (`variantRecord`) ve sepet akışı
  zaten katalog okuyor. Eksik olan tek şey istemekti. Artık varyanttan ürüne
  TEK toplu sorguyla geçiliyor — `CalculateTotals` her sepet güncellemesinde
  koşuyor, satır başına okuma N+1 olurdu ve bir test bunu sabitliyor.

  İki davranış ayrı ayrı korunuyor: kataloğun DÖNMEDİĞİ bir varyant (silinmiş ya
  da kanal dışı) ürünsüz kalıyor ve o satır varsayılan orana düşüyor — bütün
  ödemeyi düşürmek, tek satırdaki yanlış oranı hiç satış olmamasıyla takas
  etmek olurdu. Katalog HATASI ise dönüyor: yokluk bir olgudur, arıza değildir,
  ve geçici bir erişilemezlik yüzünden satırı sessizce varsayılan orana kaydırmak
  tam olarak kaçınılan şey.

  `product_type_id` boş kalmaya devam ediyor ve bu erteleme değil: gobit'te ürün
  tipi kavramı YOK. Alan şemada duruyor çünkü vergi modülü onu kabul ediyor;
  katalog tip kazandığı gün değer oraya yazılır, başka hiçbir şey değişmez.

- **Sipariş artık üzerine ne ödendiğini biliyor** (ADR 0022) — `paid_total` her
  gerçek siparişte sıfırdı.

  `SetOrderSummaryTotals`'ın servis metodu, depo metodu ve üretilmiş sorgusu
  vardı; ÜRETİM ÇAĞIRANI YOKTU. Metot taslak da değil — godoc'u yazımın neden
  MERGE olduğunu (at-least-once bir çağıran toplamı küçültememeli), küçülen bir
  raporun neden hata değil yok sayıldığını (hata, aboneyi sonsuz denemeye
  sokardı), neden siparişin kilidi altında koştuğunu ve fazla tahsilatın neden
  kırpılmadığını tek tek savunuyor. Kimin çağıracağını bile yazmış: "complete_cart
  akışı ya da ödeme olaylarını dinleyen bir abone". İkisi de yoktu.

  Sonuç her siparişe ulaşıyordu: `paid_total: 0`, `outstanding: <tüm tutar>` —
  hem yönetim hem vitrin okumasında. **Operatör ödenmiş siparişi ödenmemişten
  ayıramıyordu**, ki bir sipariş hakkında sorulan ilk soru budur.

  Yazım `clearCartStep`'in içinde, PİVOT SONRASI: hata saga'yı düşürmüyor, ERROR
  loglanıp `Warnings`'e yazılıyor. Para hareket etmiş ve sipariş verilmişken hata
  dönmek, başarılı bir akış için müşteriye başarısızlık göstermek olurdu.
  `CompleteCartResult` artık `PaymentTotalsRecorded` taşıyor.

  Yazılan sayı PLANIN değil KOLEKSİYONUN: sağlayıcı istenenden azını tahsil
  edebilir, fazla tahsilat gerçek bir olgudur, ve aynı koleksiyona karşı çoktan
  bir iade durabilir. Ödeme modülünün kendi rakamını yazmak, sayıyı
  karşılaştırmaya değer kılan şey — özeti koleksiyonuyla uyuşmayan sipariş o
  zaman gerçek bir ayrışmadır.

  **Yarısı hâlâ açık ve bunu söylemek kararın parçası:** iadeler ödeme modülünün
  kendi yönetim API'sinden yapılıyor ve sipariş tarafında çağıranı yok, yani
  SONRADAN yapılan bir iade özete hiç ulaşmıyor. Bunun adı konmuş bir kurbanı
  var: B2B harcama penceresi `order_summaries.refunded_total`'ı düşüyor, dolayısıyla
  **iade edilmiş bir B2B siparişi çalışanın bütçesini hâlâ geri vermiyor.**

  Bir e2e testi `PaidTotal == 0`'ı BİLEREK doğruluyordu ve yorumu gerçek bir
  karşı-argüman taşıyordu: "aynı anda iki yerde tutulan bir ödenen tutar
  ayrışabilir". Cevaplandı, atlanmadı. Çoğaltma zaten kararlaştırılmıştı —
  `order_summaries` tablosu o sütunlarla var, `SetOrderSummaryTotals` onları
  doldurmak için yazılmış, ve B2B harcama penceresi `refunded_total`'ı ZATEN
  okuyor; eksik olan karar değil, yazandı. Özet TÜRETİLMİŞ bir rapor, koleksiyon
  hakikat kaynağı olarak kalıyor. Ayrışma da iki rakam olmasının yarattığı bir
  risk değil, iki rakam olduğu için GÖRÜLEBİLİR hâle gelen bir durum — ADR
  0020'nin oturum ile sağlayıcı için kurduğu argümanın aynısı.

  Abone seçeneği neden seçilmedi: ödeme modülü HİÇ olay yayımlamıyor. Ödeme için
  bir olay yüzeyi kurmak bundan büyük bir karar ve önce "ödeme olayı ne taşır"
  sorusunu yanıtlamak gerekir. O gün geldiğinde bu karar geçersizleşir — abone
  daha iyi bir ev, çünkü siparişin bütün ömrünü kapsar ve iade boşluğunun
  ihtiyacı tam olarak budur.

- **Alışverişçi artık kendi kargo fiyatını belirleyemiyor** (ADR 0021) — bu bir
  özellik değil, sömürülebilir bir açığın kapatılması.

  `POST /store/v1/carts/{id}/shipping-methods` bir VİTRİN ucuydu ve gövdeden
  gelen `amount`'ı olduğu gibi yazıyordu; tek denetim `MaxAmount`'tı.
  `calculate_totals` o sayıyı topluyor, checkout planı aritmetiği doğruluyordu —
  yani kendi içinde tutarlı ve yanlış. Geçerli bir `shipping_option_id` ile
  `amount: 0` gönderen alışverişçinin siparişi o fiyattan oluşuyor VE tahsil
  ediliyordu.

  İki şey bunu gözden kaçmış bir hatadan daha kötü yapıyor.

  **Doğru sayıyı üreten motor zaten kuruluydu ve kimse sormuyordu.**
  `fulfillment/service.Interop.ListOptionsJSON` sepete uygun seçenekleri
  kurallılarıyla birlikte fiyatlandırıyor ve godoc'u "yalnızca cart/order
  akışlarınca çözülür" diyor. TÜKETİCİSİ SIFIRDI. Yani depo aynı anda iki yarıyı
  birden taşıyordu: doğrulanmayan bir fiyat ve çağrılmayan bir doğrulayıcı.

  **Bunu yasaklayan kural da zaten yazılıydı — öteki fiyat hakkında.** Cart
  modülünün kendi API paket belgesi `AddLineItem`'ın neden yüzeyde olmadığını
  anlatıyor: *"FİYATI SUNUCU belirler… Metot yüzeyde kalsaydı, ona bağlanan bir
  handler hem fiyatlamayı hem tavanı SESSİZCE atlardı."* Cümlenin her kelimesi
  kargo fiyatı için de doğru. `AddShippingMethod` yine de yüzeyde kalmıştı, ve
  ona bağlı handler tam olarak cümlenin öngördüğü şeyi yapıyordu.

  Çözüm o cümlenin ikinci kez uygulanması: yeni akış
  `workflows/cart.Workflows.AddQuotedShippingMethod` seçeneği fulfillment'a
  sorup TEKLİF EDİLEN tutarı yazıyor; `AddShippingMethod` cart API'sinin `Carts`
  arayüzünden KALDIRILDI, yani hiçbir handler servise geri uzanamıyor. Teklifin
  hesaplandığı her olgu — bölge, para birimi, ülke, ara toplam, kalem sayısı —
  sepetin kendi kaydından okunuyor.

  **KIRICI:** gövdeden `amount` ve `name` kaldırıldı. Bu API tanımadığı alanı
  reddettiği için, hâlâ gönderen istemci 422 alıyor. Sessizce yok saymak,
  entegratörü fiyatı hâlâ kendisinin belirlediğine inandırmak olurdu.

  Akış KAPALI ARIZALANIYOR: fulfillment yüzeyi bağlı değilse yöntem eklenmiyor.
  Vergi yüzeyiyle karşılaştırma öğretici — eksik vergi yüzeyi bölgenin oranına
  düşer, ki bu gerçek bir kayıttan hesaplanmış gerçek bir cevaptır; eksik kargo
  yüzeyinin öyle bir cevabı yok, tek alternatif kaynak çağıranın kendisi.

  Ara toplam İNDİRİM SONRASI alınıyor, `computeTotals` ile aynı sırada: eşik
  kuralları ("500 üstü kargo bedava") bu sayıyı okuyor ve indirim öncesini
  vermek aynı indirimi iki kez harcamak olurdu.

- **Ödeme mutabakatı** (`internal/jobs/paymentrecon`) — deponun adı konmuş tek
  tutulmamış periyodik sözü, ve para hakkında.

  Ödeme modülü sağlayıcı çağrısını KENDİ işleminin içinde yapar; bu bilinçli
  bir takas ve "tek yetkilendirme" garantisini satın alır. Bedeli şu: para
  alındıktan SONRA commit patlarsa geri alma, paranın tek yerel izini de
  götürür. Oturum burada `authorized` kalır, sağlayıcıda `captured`'dır — ve
  **bunu hiçbir yerel sorgu göremez**, çünkü farkı gösterecek her kayıt geri
  alınmış olan kayıttır. Sonuç soyut değil: saga koleksiyonu okur, tahsilat
  görmez ve ödenmiş bir siparişi telafi eder.

  `checkout/doc.go` bunu Faz 7'den beri hem sonucuyla hem çaresiyle yazmıştı:
  *"(2)'yi kapatmanın tek doğru yolu sağlayıcıya SORMAKTIR."* ADR 0019
  zamanlayıcıyı kurarken bu maddeyi tutulmamış söz olarak kaydetti. Bu, o söz.

  **Sorar, RAPOR eder, hiçbir şey yazmaz.** Bir karşılaştırmadan yola çıkıp
  tahsilat kaydetmek, modülün tek başına ve izlenmeden "para hareket etti"
  demesi olurdu; ADR 0017 aynı akıl yürütmeyi telafiler için — yani daha ucuz
  bir şey için — dört yerde reddediyor. Onarım, iki defter önünde açık bir
  insanın işi kalır; değişen tek şey insanın bakılacak bir şey olduğunu
  öğrenmesi.

  **`SessionInspector` İSTEĞE BAĞLI bir arayüzdür ve mesele tam olarak budur.**
  `PaymentProvider`'a metot eklemek her sağlayıcıyı bir sağlayıcının yeteneği
  için değiştirirdi — ve daha kötüsü, hepsini bir şey yanıtlamaya zorlardı. En
  ucuz yanıt sıfırdır, sıfır da "hiçbir şey tahsil edilmedi"den ayırt edilemez.
  Bu kararın dayandığı tek garanti şudur: *"iki defter uyuşuyor"* ile *"kimseye
  sorulamadı"* asla aynı görünmez. Sorulamayan sağlayıcı kendi alanında SAYILIR
  ve her turda yüksek sesle söylenir.

  Sorgu kümesi dar tutuldu — burada yetkilendirilmiş, bir yerleşme penceresini
  aşmış oturumlar — çünkü uçuştaki bir tahsilat tam olarak o durumda durur ve
  pencere olmasa her olağan ödeme "ayrışma" diye raporlanırdı. Bulgu üç sınıfa
  ayrıldı: **ayrışma** (muhasebeye), **ulaşılamayan sağlayıcı** (entegrasyon
  sahibine), **sağlayıcının tanımadığı oturum** (yanlış hesaba açılmış bir
  yetkilendirmenin buradan görünüşü). Tek satıra katlamak üçünü de ilk okuyana
  yollardı.

  **Kısmi indeks ölçüldü, varsayılmadı.** 200.000 oturumluk düzenekte listeleme
  indeks taramasıyla 0,56 ms / 52 tampon; indeks düşürülünce aynı sorgu paralel
  sıralı taramayla 12,0 ms / 3.618 tampon — ve o taraf, kurulumun aldığı her
  ödemeyle büyür. Bir entegrasyon testi PLANI okur, çünkü bu depo daha önce
  "indeks kullanılır" diyen ama plancının katılmadığı bir godoc yayımladı.

  **`gobit jobs` artık tüm uygulamayı açıyor.** Bağımlılığı bir modül olan iş
  ancak modülleri olan bir container'dan kurulabilir, ve listeleme koşucunun
  çağırdığı AYNI `registerJobs`'u çağırır. Daha ince bir container'dan üretilen
  listeleme, koşandan başka bir iş kümesini anlatırdı — o komutun var olma
  sebebi tam olarak bunu yapmamak.

  ADR 0020, `docs/adr/0020-reconciliation-asks-the-provider-and-reports.md`.

- **Zamanlanmış iş geldi** (`internal/core/job`) — ama planladığımın onda biri
  kadarıyla, ve asıl değeri kodda değil ÖLÇÜMDE.

  Yol haritası bu maddeyi "4 tüketici" diye yazmıştı. 22 ajanlı tasarım turu
  dördünü de çürüttü ve ölçüm doğrulandı: `payout` gobit'te **sıfır dosyada**
  geçiyor; reddedilen başvuru kavramı yok (b2b'de yalnızca `b2b_company` ve
  `b2b_company_employee`); kampanya süresi zaten OKUMA anında uygulanıyor
  (`campaignUsable(candidate, in.At)`) — durum çeviren bir iş gözlenebilir
  hiçbir şeyi değiştirmezdi; ve terk edilmiş sepet kurtarma **eksik değil,
  reddedilmiş** — dört ayrı yerde yazılı. Yani dördü de ADR 0009'un sınavını
  geçmiyordu, ve inşa etmek deponun kendi adını koyduğu ikinci hata sınıfını
  çekirdeğe koymak olurdu.

  **Kararı mümkün kılan şey mevcut yasağın İÇİNDEKİ ayrım.** ADR 0017 *eylemi*
  yasaklıyor: kurtarma telafi koşturur, telafi yan etkidir, zamanlanmış bir iş
  bunu izlenmeden yapardı. ADR 0016 ise öteki yarıyı açıkça sahipsiz bırakmış:
  *"Bu bir anlık görüntü, uyarı değil. Kimseye sepetin takıldığı söylenmiyor."*
  `stuck.go` bunun bedelini de yazmış: o kayıt "sonsuza dek running kalır, stok
  tutar ve hiçbir şey tarafından anılmaz". Tutulan iş ayrılmış stoktur —
  ergonomi değil doğruluk maliyeti.

  Dolayısıyla tek iş var: **`sagawatch`**, ve yalnızca RAPOR eder. Hiçbir şeye
  dokunmaz. Onarım hâlâ `gobit recover <id> -confirm`, bakan bir insanın işi;
  değişen tek şey, insanın artık bakılacak bir şey olduğunu ÖĞRENMESİ.

  **Seçim satırla, canlılık kilitle.** İkisi farklı soru yanıtladığı için ikisi
  birden. Yalnız kilit eşzamanlılığı engeller ama SIKLIĞI değil: üç kopya farklı
  fazlarda tikleyip günlük bir işi gecede üç kez koşar — ve arıza kopya
  ekledikçe kötüleşir. Yalnız lease ise asılıyı ölüden ayıramaz: takılmış ama
  canlı süreç kalp atışı gönderip listede en sağlıklı satır olur. Üstelik
  gobit'in lease'inin **deposu yok** — yürütme şemasında lease sütunu, sahip
  alanı, kalp atışı ve süpürücü yok; `WithLease` çağıran tarafta `updated_at`
  üzerine bir yüklem.

  Oturum kapsamlı advisory lock'ın hiç ayarı yok: arka uç çıkar, PostgreSQL
  kilidi zamanlayıcısız toplar. Gerçek Postgres'e karşı kanıtlandı — kilidi
  tutan bağlantı kapatıldığında kilit serbest kalıyor.

  **Occurrence epoch'a bağlı**, sürecin başlangıcına değil; seçimin tamamı bu.
  Dakikalar arayla açılmış iki kopya AYNI anı hesaplamazsa aynı satır için
  yarışmaz ve her kopya her işi koşar. Mutasyonla kanıtlandı.

  **Anahtar sınıfı 2, ve sınıf süs değil.** Sınıf 0'ı golang-migrate bütünüyle
  işgal ediyor (uint32) ve kendi kilidini `context.Background()` üzerinde
  bekliyor — yani sınırsız VE iptal edilemez bir bekleme. İşin adının çıplak
  hash'i o aralığa düşerdi ve açılıştaki bir migration'ı kimsenin
  kesemeyeceği bir beklemede kilitleyebilirdi. Bu da mutasyonla kanıtlandı.

  16 goroutine gerçek Postgres'te aynı occurrence'ı talep ediyor: tam olarak
  biri kazanıyor. Sahte depoyla kanıtlanamayacak tek iddia buydu.

  `gobit jobs` uç değil alt komut — "dün gece koştu mu" sorusu olay anında
  terminalden sorulur, yönetim API'sinin kendisi bozuk olabilir. Liste koşanı
  ölenden ayıramaz ve bunu SÖYLER ("unfinished (running now, or the process
  died)") — tahmin etmez; ayıran şey kilittir.

  Karar ADR 0019'da, reddedilen altı seçenekle birlikte. Eklentiler için bir
  iş kaydı metodu ertelendi — reddedilmedi: kaydedilecek eklenti işi sıfırken
  o da aynı hata sınıfı. ADR'nin REDDEDİLENLER bölümünde duruyor, çünkü
  inşa edilmemiş bir şeyin adını gövdede anmak okuru var olmayan bir üyeyi
  aramaya yollar.

- **PayTR ile ödeme geldi** (`payment-paytr` eklentisi) — ve web push'la
  **aynı bulguya** çıktı, ters yönden.

  PayTR SÜRÜLEMEZ: müşteri PayTR'ın kendi iframe'inde öder ve sonucu bize
  callback ile bildirir. Sözleşme ise `Authorize(ctx, sessionID)` diye soruyor —
  "para bloke mi" — ve sağlayıcının yalnızca oturum kimliğinden cevap
  verebilmesini bekliyor. Sürülebilen bir geçit (Stripe) API çağrısıyla cevap
  verir; PayTR'de o cevap **callback'in yazdığı satırda** durur ve callback ile
  soru arasındaki yeniden başlatmayı atlatmak zorundadır. Yani yine kalıcı
  durum, yani yine kendi modülünü getiren bir eklenti. ADR 0018'in bulgusu
  ikinci kez, başka bir sebepten doğrulandı: **sağlayıcı yuvası tek başına,
  sorulmak yerine haber veren bir tarafı ifade edemiyor.**

  **İmzalar dış vektörlere karşı kilitlendi.** Üç formül var ve ikisi bir
  kurala, biri başkasına uyuyor: `callback` salt'ı gövdenin İÇİNE koyuyor,
  ötekiler sona ekliyor. İkisinden kuralı öğrenen okur üçüncüyü yanlış yazar —
  ve sonuç sessiz değil ama teşhis edilemez: her gerçek callback reddedilir,
  PayTR aldığını saymadığı için sonsuza dek yeniden dener, logda ise imza
  uyuşmazlığı görünür ve saldırı gibi okunur.

  Beklenen değerler kendi çıktımız değil: çalışan bir PayTR entegrasyonunun
  sabit kimliklerle bağımsız HMAC-SHA256'dan ürettiği kilitli vektörler.
  Mutasyonla sınandı — callback "genel kurala" uydurulduğunda iki test birden
  düşüyor.

  **İki farklı tutar biçimi tek entegrasyonda:** get-token minör birim tamsayı
  ("10000"), iade ise majör birim iki ondalıklı ("100.00"). Birini ötekinin
  yerine göndermek yüz kat sapar, PayTR tarafından KABUL EDİLİR ve müşteriye yüz
  kat eksik ya da fazla para döner. Dönüşüm tamsayı aritmetiğiyle yapılıyor;
  para bu depoda float'tan geçmez (plan Bölüm 8) ve buradaki sonuç müşterinin
  geri aldığı tutar.

  **Callback'in yanıtı tam olarak `OK` olmak zorunda** — PayTR gövdeyi okuyor,
  durum kodunu değil. Çekirdeğin JSON zarfı orada sonsuz bir yeniden deneme
  döngüsü üretirdi. Bu yüzden o uç `internal/arch/error_path_test.go`'daki
  muafiyet listesine gerekçesiyle yazıldı; aynı dosyadaki operatör ucu
  çekirdekten geçiyor ve test bunu kontrol ediyor.

  **Kapanmayan boşluk adıyla yazıldı:** ödeyip tarayıcıyı kapatan müşterinin
  parası alınmış ama siparişi yoktur. Callback yalnızca kaydeder; sepeti
  siparişe çevirmek checkout workflow'unun işi ve ödeme eklentisinin oraya
  uzanması sipariş akışını iki yere koyardı. Bekleyenler açılışta sayılıp
  uyarı olarak loglanıyor ve `GET /admin/v1/paytr/pending` ile listeleniyor —
  gobit'in yarım kalmış iş için zaten kelimesi var (ADR 0016/0017).

  **Yolda bir kör nokta daha:** `TestHTTPSurfacesLiveOnlyInApiPackages` ile
  `TestNonModuleHTTPSurfacesWriteThroughTheCore` aynı muafiyet listesini
  paylaşıyor gibi görünüyor ama biri yalnızca `internal/modules/`'ü geziyor —
  yani bir eklenti oraya asla "görünmüyor" ve muafiyet "kullanılmıyor" diye
  reddediliyor. Doğru liste `coreWriterExemptions`; bu, eklenti yazacak bir
  sonraki kişinin de düşeceği tuzak.

- **Tarayıcı push bildirimi geldi** (`web-push` eklentisi) — ve sağlayıcı
  yuvasına GİRMEDİ. Bu turun asıl bulgusu kodu değil, kararı değiştirdi.

  **`NotificationProvider` sözleşmesi push'u ifade edemiyor.** `To string`
  "e-posta adresi ya da telefon numarası" diye belgeli; push'un hedefi bir adres
  değil, tarayıcının ürettiği ÜÇ değer (endpoint, P-256 açık anahtarı, 16
  baytlık sır) ve çerçevenin onları SAKLAMIŞ olması gerekiyor. Yani soru
  "hangi sağlayıcı" değil, "bu durum nerede yaşıyor"muş.

  Karar 17 ajanlı bir tasarım turuyla verildi: dört keşif, üç bağımsız tasarım,
  dokuz hasım yargı, bir sentez. Çekirdeği değiştiren iki tasarım 8/15, provider
  yuvasına zorlayan 6/15 aldı; üç yargı merceği de eklentinin KENDİ MODÜLÜNÜ
  getirmesinde birleşti (searchpg şekli). Çekirdekte **sıfır** satır değişti.
  Tamamı ADR 0018'de, reddedilen altı seçenekle birlikte.

  **En keskin gerekçe defterin yalan söyleyecek olmasıydı:** `Send` tek bir
  `error` döner, yani sıfır cihaza fan-out `nil` döner ve defter `sent` yazar —
  ve `ClaimDelivery` tekrar talebini önceki durumdan bağımsız atladığı için o
  yalan `sent`, modülün kendi godoc'unun "insanın kararı olarak kalmalı" dediği
  yeniden göndermeyi KALICI olarak kapatır.

  **Kripto sıfır bağımlılıkla, RFC vektörüne karşı kanıtlandı.** RFC 8291
  (şifreleme) ve 8292 (VAPID) `crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa` ve
  `crypto/aes` ile yazıldı. Bu dosyadaki her hatanın AYNI belirtisi var: push
  servisi 201 döner, tarayıcı açamadığı bir olay alır, hiçbir yerde arıza
  görünmez. Bu yüzden beklenen değer kendi çıktımız değil, **RFC 8291
  Appendix A vektörü** — ve birebir tutuyor. İki mutasyonla sınandı: `key_info`
  içindeki anahtar sırası ters çevrildiğinde ve `FillBytes` yerine `Bytes`
  kullanıldığında testler düşüyor (ikincisi ~256'da bir kısa imza üretir, yani
  "ara sıra 401" diye görünen ve elle üretilemeyen bir arıza).

  **401 bir aboneliği ASLA silmez, 410 siler.** Eklentinin en keskin kuralı.
  410 "bu abonelik yok" demektir ve bunu söyleyebilecek tek yetkili kaynak push
  servisidir. 401 ise jetonun reddi demektir — VAPID anahtarı döndüğünde ya da
  saat kaydığında olur, HER cihazı aynı anda vurur ve onarılabilir. Silseydi,
  biri anahtarı döndürdüğü öğleden sonra tüm cihaz defteri silinirdi ve sunucu
  tarafından geri koymanın yolu yoktur; yalnızca tarayıcılar, teker teker.
  Mutasyonla kanıtlandı.

  Bunun bedeli görünmez bir mezarlık: 401 silmediği için dönmüş anahtarla
  basılmış satırlar sonsuza dek kalır. Her satır hangi anahtarla basıldığını
  (`vapid_fingerprint`) taşıyor; açılışta sayılıp ERROR loglanıyor ve gönderim
  yolunda siliniyor — kendini boşaltan bir mezarlık, görünmeyenden iyidir.

  **Yolda ölçülen iki şey:** `ChannelSMS` zaten tüketicisiz bir yetenek (tüm
  ağaçta 5 referans, hiçbiri sağlayıcı değil), ve **eklenti migration'ları
  hiçbir arch kapısında değil** — iki rollback testi de `moduleNames(t)`'i
  geziyor, o da yalnızca `internal/modules/`'ü okuyor. searchpg'nin up/down
  çiftini bugün hiçbir şey doğrulamıyor. Bu eklenti kendi rollback testini
  taşıyor ve bu, ADR 0018'in şartı.

- **Yüklemeler artık nesne deposuna gidebiliyor** (`file-s3` eklentisi).
  Kutudan çıkan `local` sağlayıcısı TEK süreç için doğrudur ve İKİ süreç için
  yanlıştır: dosya, yüklemeyi karşılayan örneğin diskine düşer ve başka örneğe
  yönlenen her istek 404 alır — hiçbir hata görünmeden, çünkü o örnek
  açısından anahtar gerçekten yoktur. AWS S3, MinIO ve R2 ile çalışır.

  **AWS SDK KULLANILMADI.** SigV4 elle yazıldı; ADR 0014'ün eklenti bağımlılığı
  kararının aynısı. Sağlayıcı yalnızca iki çağrı yapıyor (PUT, DELETE) ve
  onların istediği imzalama sabit bir tarif.

  **Gövde neden tamponlanıyor, ve neden DİSKE.** Sözleşme akış istiyor ve
  gerekçesi bellek: 50 MB'lık bir yükleme `[]byte` olarak 50 MB süreç
  belleğidir. Bu gerekçeye uyuldu — tampon geçici bir DOSYA, dilim değil.
  Tamponlamanın kendisi HTTP ve SigV4 tarafından zorunlu kılınıyor: PUT ya
  Content-Length ya chunked ister, S3 chunked'ı kendi akış imzası olmadan kabul
  etmez, `io.Reader` ise ne uzunluğunu ne özetini bilir.

  Bunun beklenmedik bir kazancı var: sözleşmenin "yarıda kesilen okuma yarım
  nesne bırakmamalı" şartı **gereksiz** hâle geliyor, karşılanmış olmuyor.
  Okuma isteğin kurulmasından ÖNCE düşüyor, yani hiç nesne yaratılmıyor —
  temizlenecek bir şey de, kendisi arızalanabilecek bir temizlik yolu da yok.

  **Adres İMZASIZ ve kalıcı.** `File.URL` ürün görseli kaydına yazılıp orada
  kalıyor; imzalı bir adres süresi dolduğunda sessizce çürürdü. Bu yüzden CDN
  önekini türetilen değer yanlış olduğunda `S3_PUBLIC_BASE_URL` veriyor ve
  türetilen değer AÇILIŞTA loglanıyor — ilk log satırında görülebilsin diye.

  **İmzanın DOĞRULUĞU gerçek bir S3'e karşı kanıtlandı.** Birim testleri
  imzanın şeklini ve DUYARLILIĞINI kanıtlıyor (kapsanması gereken her girdi
  çıktıyı değiştiriyor mu), ama doğruluğunu kanıtlayamaz: elde edebilecekleri
  tek beklenen değer, aynı kodu okuyarak üretilmiş olurdu. Bu yüzden
  `s3_integration_test.go` testcontainers ile gerçek bir MinIO kaldırıyor.
  Kanonik biçim tek bir satırsonu kadar yanlış olsa, S3'ün yol kodlama istisnası
  atlansa ya da imzalanan bir başlık bildirilmese, her test 403
  SignatureDoesNotMatch ile düşer.

  Testlerden biri kasten kötü bir sırla imzalıyor: sunucunun gerçekten
  doğruladığını kanıtlamasaydı, diğer testlerin hepsi değersiz olurdu.

  Ölçülen bir ayrıntı yazıldı: MinIO'nun `/health/live` ucu süreç ayakta demek,
  S3 API'si hazır demek değil — arada 503 `XMinioServerNotInitialized` penceresi
  var. Yoklama `/health/cluster`'a alındı ve yalnızca 503 için sınırlı bir
  tekrar kondu; 403 anında dönüyor, çünkü bir imza arızası tekrar döngüsünün
  arkasında saklanıp sonunda zaman aşımı olarak raporlanmamalı.

- **Bildirimler artık GERÇEKTEN gidiyor** (`notification-smtp` eklentisi).
  Kutudan çıkan tek bildirim sağlayıcısı `logonly`'ydi ve adı ne yaptığını
  dürüstçe söylüyordu: bir log satırı yazar, hiçbir yere göndermez. Yani
  bildirim yuvası bugüne dek çerçevenin tutmadığı bir sözdü — sağlayıcı
  soyutlaması vardı, çalışan uygulaması yoktu.

  **Yeni bağımlılık YOK.** `net/smtp`, `crypto/tls`, `mime` ve `text/template`
  standart kütüphanededir; bu, ADR 0014'te OTLP gövdesi ve Sentry zarfı için
  verilen kararın aynısıdır.

  **E-posta METNİ çerçeveden gelmiyor.** Şablonlar kurulumun sahip olduğu bir
  dizinden (`SMTP_TEMPLATE_DIR`) okunur ve dizin verilmezse eklenti AÇILMAZ.
  Hazır İngilizce metin gömmek reddedildi: müşterisine yanlış dilde yazan bir
  mağaza üretirdi ve fark edildiği gün müşterinin şikâyet ettiği gün olurdu.
  Şablonlar AÇILIŞTA ayrıştırılır — bozuk bir şablon açılışı durdurur, gece
  yarısı bir olay tüketicisinin içinde sessizce gitmemiş bir bildirime dönüşmez.

  **En önemli iş başlık enjeksiyonunda.** Konu satırı ŞABLON VERİSİNDEN
  üretiliyor, o veri de olay yükünden geliyor — müşteri adı, ürün başlığı;
  bu sürecin yazmadığı dizeler. İçlerindeki bir `\r\n`, Subject başlığını
  bitirip ardındakini yeni bir başlık olarak okutur: mesajı başka yere
  kopyalayan bir `Bcc`, göndereni taklit eden ikinci bir `From`. Kontrol bu
  yüzden RENDER'DAN SONRA yapılıyor ve değer TEMİZLENMİYOR, REDDEDİLİYOR —
  temizlemek, çağıranın yazmadığı bir mesajı sessizce göndermek olurdu.

  Mutasyonla kanıtlandı: kontrol kaldırıldığında
  `TestSendRefusesASubjectRenderedWithALineBreak` `smtp_header_injection`
  yerine `smtp_send_failed` görüyor — yani enjekte edilmiş mesaj sunucuya
  kadar gidiyor.

  Diğer üç ret de sessiz bir arızanın yerini alıyor: hizmet etmediği kanal,
  bilinmeyen şablon (jenerik bir gövdeye düşmek, hiçbir şey söylemeyen bir
  e-posta ve teslim günlüğünde bir BAŞARI kaydı üretirdi) ve — kurulum açıkça
  vazgeçmedikçe — şifresiz bağlantı. Sonuncusu `net/smtp`'nin parolayı
  şifresiz göndermeyi zaten reddetmesine RAĞMEN var: kimlik doğrulaması
  istemeyen bir aktarıcıda her sipariş onayı açık metin olarak ağdan geçerdi.

  Konu RFC 2047 ile kodlanıyor, gövde base64: ikisi de aynı olgudan çıkıyor —
  bu deponun dili ASCII değil. Base64 tercihi estetik değil; RFC 5321 satırı
  1000 sekizliyle sınırlıyor ve uzun bir URL üreten şablon bunu aşıyor.

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

- **`cart`, `order` ve `auth` modüllerinde Türkçe kalmadı** (ADR 0012'nin
  cırcırı). Doksan beş dosya, paket başına bir ajan olmak üzere on sekiz ajanla
  iki aşamada çevrildi; içerik defteri 397 dosyadan **302**'ye, yol defteri 16
  satırdan **9**'a indi.

  **Çeviri biriminin DOSYA değil PAKET olması, merkezî bir adımı tümüyle
  kaldırdı.** Önceki turlarda paylaşılan tanımlayıcılar dalgadan ÖNCE merkezden
  yeniden adlandırılmak zorundaydı, çünkü aynı adı iki ajan farklı İngilizceye
  çevirebiliyordu. Paylaşılan ad, tanımı gereği bir Go paketinin içinde yaşar;
  birimi pakete çekince o sınıf çakışma imkânsızlaştı ve dalga öncesi tur
  gerekmedi.

  Yedi test dosyası da yeniden adlandırıldı (`yetki_test.go` →
  `authorization_test.go`, `cikis_test.go` → `logout_test.go`,
  `describe_yonetim_internal_test.go` → `describe_admin_internal_test.go`).
  README'nin `order`'daki iki teste yaptığı atıf, adlar değiştiği için
  güncellendi; kırılmayı `TestTheTestsMentionedInTheDocsAreReal` yakaladı.

- **Cırcırın GÖREMEDİĞİ bir borç sınıfı bulundu ve kapatıldı: diyakritiksiz
  Türkçe.** Üç şeritli dedektör Türkçe harfleri, kelime listesini ve AST
  TANIYICILARINI tarıyor; bir yorum ya da dize sabiti içinde `"limit negatif
  olamaz: %d"` gibi tümü ASCII yazılmış Türkçe üç şeridin de dışında kalıyor.
  Sonuç: dedektöre göre TEMİZ olan, dolayısıyla deftere hiç girmeyen, dolayısıyla
  hiçbir ajana atanmayan dosyalar Türkçe taşımaya devam ediyordu.

  Ölçüldü: defterin temiz saydığı 204 dosyada 508 aday satır; yüksek güvenli
  sözcüklere daraltıldığında **23 dosyada 100 gerçek satır**. Hepsi çevrildi.
  Bunların içinde `core/query`, `core/http`,
  `internal/core/config` ve `internal/modules/fulfillment` gibi ÖNCEKİ TURLARDA
  "tümüyle İngilizce" ilan edilmiş ağaçlar da vardı — yani ilan, dedektörün
  körlüğü kadar doğruydu.

  Aynı sınıfın ikinci yüzü: `internal/modules/{file,inventory,notification}`
  altındaki `Page.normalize` metinleri de bu yolla görünmezdi. Modülleri henüz
  çevrilmediği için deftere de girmiyorlardı; kendi turları geldiğinde de
  girmeyeceklerdi. Merkezden çevrildiler.

- **Dedektörün kök listesinden silinmiş bir kök geri kondu.** `turkishStems`
  içindeki `"ayristir"` girdisi, `5b0778c`'de bir tanımlayıcı yeniden
  adlandırmasıyla `"parseDir"` hâline gelmişti: liste KAYNAK değil VERİ, ve
  toplu yeniden adlandırma onu sessizce yedi. Suite yeşil kaldığı için kimse
  görmedi. Körlüğün bedeli ölçülebilir — `internal/arch/configuration_test.go`
  o günden beri `ayrisik`/`ayristirmaHatasi` tanımlayıcılarını taşıyordu ve
  dosya "çevrildi" sayılmıştı. Aynı sınıfın üçüncü tekrarı (öncekiler:
  `denetim` → `auditCtx`, `gunlukBekle` → `waitForLog`).

- **Cırcırın dışında kalan 19 YAML dosyası çevrildi.** Dedektör `.go`, `.sql`,
  `.gohtml`, `.md` ve `.graphqls` tarıyor; YAML hiç taranmıyor, dolayısıyla bu
  borç defterde HİÇ görünmüyordu. On beş `sqlc.yaml`, `gqlgen.yml`,
  `.golangci.yml`, `.github/workflows/ci.yml` ve `deploy/docker-compose.yml`.

  `.golangci.yml`'de 211 `depguard` `desc:` dizesi ve 16 kural adı çevrildi;
  kural SAYISI korundu (`solid_test.go` godoc'u 211'i anıyor). CI iş adları da
  çevrildi (`Entegrasyon` → `Integration`); `main` korumasız olduğu için
  gerekli statü kontrolü kırılmadı — bakıldı, varsayılmadı.

  İki dize BİLİNÇLİ olarak Türkçe bırakıldı, çünkü ikisi de VERİ: compose
  dosyasındaki `"çanta"`/`"Çanta"` çifti C locale'in harf katlamasını gösteren
  örneğin ta kendisi, `misspell` istisna listesi (`paralel`, `mamal`, `adres`)
  ise davranış taşıyan bir liste — girdi düşürmek çeviri değil davranış
  değişikliğidir ve cırcır sıfıra indiği güne aittir.

- **`internal/core/workflow` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı).
  Beş turda motorun kendisi, `pgstore` ve ikisinin TÜM test dosyaları çevrildi;
  defter 715 dosyadan **708**'e indi.

  Test dosyalarında çeviri üretim dosyalarından farklı bir iştir: yorumlar kadar
  TEST VERİSİ de dile bağlıdır. Adım adları, workflow adları, idempotency
  anahtarları, yürütme kimlikleri, hata kodları ve paylaşılan durum anahtarları
  hep dize sabitidir; hepsi çevrildi ve doğrulamayı derleyici değil testlerin
  kendisi yaptı (iddialar aynı dizelerden türüyor).

  **Test ADLARININ çevrilmesi ek bir bağ getiriyor:** depo tüm test adlarını
  indeksliyor (`TestTheReferencesInTheDocsResolve`) ve markdown'da ters tırnak
  içinde anılan bir ad çözülmezse arch suite'i düşer. Bu turda iki atıf
  güncellendi — `workflow_test.go`'nun pgstore testine yaptığı godoc atfı ve
  ADR 0017'nin tekellik ölçümünü anan satırı.

  **Üç şeritli dedektör işini gösterdi:** diyakritikler temizlendikten sonra
  suite yeşildi ama dil testi hâlâ düşüyordu; "yok", "eski", "guncel", "talep"
  gibi Türkçe harf taşımayan sözcükler kelime şeridinden yakalandı.

  Davranış değişmedi; üretim koduna dokunulmadı.

- **`internal/core` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı). Workflow
  turunun ardından gelen dört turda `core/http`'nin kalan dosyaları,
  `redisguard`, `core/query`, `internal/core/openapi` ve `internal/core/config` çevrildi; defter
  708 dosyadan **680**'e indi ve defterde artık `internal/core/` ile başlayan
  TEK BİR satır yok. Kalan borç `internal/modules/*`, `internal/e2e`,
  `internal/arch`'ın kendi testleri ve ADR 0001-0011'de.

  Çeviri, tanımlayıcıları da taşıdığı için paket sınırını iki yerde aştı:
  `MemoryIdempotencyStore.Butce()` erişimcisi `Budget()` oldu (bileşim kökü onu
  sınıyor, `internal/app/setup_test.go` da güncellendi) ve `config`'in hata
  metinleri İngilizceye geçince `internal/smoke/graphql_test.go`'nun iddia
  dizesi yenilendi. İkinci dosya defterde ve Türkçe KALIYOR — çevrilen yalnızca
  aradığı metin.

  **Çeviri turu üç gerçek sapma buldu**, üçü de yalnızca metni taşırken:

  - `openapi` paketinde bir godoc tanımından KOPMUŞTU. `alan` tipinin godoc'u
    zaten İngilizce yazılmıştı ("field is what schema generation needs…"), yani
    `TestGodocFormat`'nin "godoc, bağlandığı tanımın ADIYLA başlar" kuralını tip
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

- **On beş modülün KIRICI YÜZEYİ İngilizceye geçti** ve sıralamanın kendisi bir
  karardır. Borç ~45 bin satırdı; bütçe ortada biterse geriye kalanın yarısının
  ZARARSIZ olması için önce çatallayan/vendor'layan bir kurulumun DERLENDİĞİ
  yüzey çevrildi:

      models/{models,filters,ids}.go   Module Links ve interop'tan geçen DTO'lar
      service/interop.go               yayımlanan dar arayüz
      service/provider.go              Query sağlayıcı yüzeyi
      service/service.go               port tanımları
      api/dto.go                       istek/yanıt tipleri

  Ertelenenler derlemeye HİÇ girmiyor: tüm `*_test.go`, `repository/*.go`,
  handler gövdeleri, `queries/*.sql`, `migrations/*.sql`, `docs/`.

  Aciliyeti bir tık düşüren şey Go'nun kendi kuralı: `internal/...` başka bir
  modülden import EDİLEMEZ, yani etkilenen kitle `go get` kullanıcıları değil,
  çerçeveyi gömen kurulumlardır.

- **`internal/arch` ve `internal/core` ağaçlarında Türkçe kalmadı**, `internal/e2e`
  yarılandı. Defter 715 dosyadan **559**'a, yol defteri 37'den 29'a indi.

- **Çeviri paralelleştirildi** (ajan başına bir dosya ya da bir modül) ve iki adımın
  MERKEZÎ kalması gerektiği ölçülerek görüldü. Paylaşılan tanıtıcılar dalgadan
  ÖNCE tek elden çevrilmezse iki ajan aynı adı iki farklı İngilizceye çevirir;
  paylaşılan hata METİNLERİ ise dalgadan SONRA çevrilmek zorunda, çünkü on iki
  modülün `provider.go`'su bayt bayt aynı boilerplate'i taşıyor ve "başkasının
  dosyasındaki dizeye dokunma" kuralı yüzünden hiçbir ajan kendi başına
  temizleyemiyor.

- **Toplu yeniden adlandırma dedektörün kendi VERİSİNİ bozabiliyor.** Bu turda
  bozdu: `denetim` → `auditCtx` yeniden adlandırması `language_test.go`'daki
  `turkishStems` listesinde duran `"denetim"` girdisini de değiştirdi, yani
  dedektörden bir kök silindi. Suite yeşil kaldı, çünkü `TestDetectorIsNotBlind`
  liste BOYUNUN tabanını pinliyor, tek tek girdileri değil. Kök geri kondu; ders
  ADR 0012'nin kendi cümlesinin tekrarı — dize sabitleri kaynak değil VERİ
  olabilir.

- Hata KODLARI, entity/link/kayıt ADLARI, süzgeç anahtarları, JSON etiketleri ve
  ID önekleri bu turların HİÇBİRİNDE değişmedi. v0.8.0'da motor için verilen
  kararın aynısı: mesaj İngilizceye geçer, sözleşme yerinde kalır.

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

- **`core/link` ve `core/eventbus` İngilizceye çevrildi**
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

- **Açılışta artık bu sınanıyor** (`core/db/casefold.go`). Havuz
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
  sütunu boşalır. `TestThePanelCatalogNamesAgree` bu bağı derleme zamanına
  taşıyor — `TestTheProviderRegistryNamesAgree` ile aynı gerekçe, aynı yer.
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

- Kablolama değişmezi (`TestTheAdminPanelIsSetUpInTheCompositionRoot`) ve modül-izolasyonu
  denetimi (`TestTheAdminPanelDoesNotImportModules`) dördüncü ağacı da kapsıyor. Önek
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

- `core/http/auth.go` İngilizceye çevrildi. Kimlik doğrulama
  yanıtlarının mesajları değişti (`"authentication is required"`); kodlar
  (`unauthenticated`, `forbidden`) değişmedi.

- **Çekirdeğin sekiz paketi İngilizceye çevrildi** (ADR 0012): `core/errors`,
  `core/container`, `core/module`, `core/provider`,
  `internal/core/logger`, `core/db` (migration testdata'sı dâhil),
  `internal/core/observability`, `core/plugin`, ve `core/http`
  içinde `response.go`, `auth.go`, `router.go`, `server.go`, `middleware.go`,
  ve `core/query`'nin üretim dosyaları.

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
  `belge_test.go` → `docs_test.go`; `core/http` içinde `response.go`
  ve testi. Davranış değişmedi, ama açılış LOG MESAJLARI
  ve kullanıcıya dönen genel iç hata mesajı artık İngilizce
  (`"an unexpected server error occurred"`). Hata KODLARI değişmedi ve
  değişmeyecek: kod makine sözleşmesidir, mesaj insan içindir.

  Yeniden adlandırmalar sırasında kayıt denetimi gerçek bir tuzağı yakaladı:
  eklenti kaydının yerel değişkenine `registry` demek, denetimin alıcıyı ADIYLA
  tanıması yüzünden o satırı modül kaydı gibi gösteriyordu.

  İçerik defteri 784 → 777, yol defteri 41 → 38.

- ADR seçenek bölümü başlıklarını tanıyan liste İKİ DİLLİ oldu
  (`internal/arch/doc_references_test.go`). Yalnızca Türkçe başlık tanıyan
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

  Gerçek yığında ölçüldü: `internal/e2e/multi_warehouse_test.go`, gerçek Postgres ve
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
  `internal/smoke/keys_test.go` içindeki
  `TestPublishableKeyWithoutChannelIsRejectedByStorefront`, README'nin publishable
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
  denetimsiz KALMADIĞI yazıldı — `TestModulesDoNotImportEachOther` modül
  ağacını gezer, `.golangci.yml`'den haberi yoktur.

- README, müşteri oturumunu "Faz 8" diye anıyordu; aynı belgenin "Faz durumu"
  tablosunda Faz 8 (Auth · admin user · API key · RBAC) **tamamlanmış**
  görünüyor. Okuyan için çelişkili işaret: yapılmış bir fazın kapsamı olarak
  gösterilen şey aslında hiçbir fazın kapsamında değil. Faz numarası
  kaldırıldı, kapsam açıkça yazıldı.

### Kaldırıldı

- **Hız sınırının dışa açık anahtar yardımcısı KALDIRILDI** —
  `core/http` paketindeki `PrincipalKey`. (Ad burada paketiyle
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
  (`internal/e2e/channel_cart_test.go`).

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
  sabitlendi (`TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule`,
  `TestTheSpendingRuleIsAppliedToTheDeclaredCustomer`). İki test bir yeteneği
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
  yanlış negatifi ÖLÇÜLDÜ.** `TestEveryWorkflowIsSetUpInTheCompositionRoot`, "yanlış
  yapılandırma açılışı durdurabilir mi" sorusunu "kuruluma giden yol bir `go`
  ifadesinden geçiyor mu" diye sorar. `go` tek satırlık bir dolaylamanın
  arkasına saklandığında denetim GEÇER, oysa özellik sağlanmaz: gerçek süreçte
  ölçüldü — senkron ikili, kurulum hatasında çıkış kodu 1 verirken o biçimdeki
  ikili sağlıklı açılıp arızayı tek bir ERROR satırına indiriyor. Vekil yine de
  tutuluyor çünkü YAKALADIĞI biçimler (çıplak `go`, kapanış, çok halkalı
  zincir) kazara yazılanlardır; kaçırdığı biçim bilerek yazılmayı gerektirir.
  Kapsam `internal/arch/registration_test.go`'da yazılıdır ve orada "bu değişmez
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
  kalamıyor; `TestEveryModuleIsRegisteredInTheCompositionRoot` satırı silen kişiden kararı
  gerekçesiyle yazmasını istiyor. Modülü B2C kurulumda bırakmanın bedeli de
  küçük ve görünür: iki boş tablo ve hiç tetiklenmeyen bir kural.

Sepet akışlarının bağlanmasını izleyen bağımsız doğrulamanın çıkardığı bulgular:

- **Saga adım hatası, alt hatanın KODUNU kaybediyordu.** `internal/core/workflow`
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
  Bulan şey `internal/arch/consumers_test.go`'daki `TestTheLinkDefinitionsAreTraversed`
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
    bkz. `core/link/service.go`), defteri koda karşı taramaz.
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
