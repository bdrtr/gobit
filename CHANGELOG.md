# Değişiklik günlüğü

Biçim [Keep a Changelog](https://keepachangelog.com/tr/1.1.0/) ölçütlerine,
sürümleme [Semantic Versioning](https://semver.org/lang/tr/) kurallarına
uyar.

`0.x` boyunca **kırıcı değişiklikler minor sürümlerde gelebilir**: API yüzeyi
henüz sabitlenmemiştir ve bir uç, daha doğru bir tasarım uğruna taşınabilir.
Sabitlenme `1.0.0` ile olur.

## [Yayımlanmamış]

Her madde **bir satırdır ve kararını adlandırır**. Gerekçe, ölçüm ve karşı
okuma burada değil: karar `docs/adr/` içindeki kayıtta, kararı üreten tartışma
onu getiren commit mesajında, sayılar `docs/measurements/` altında durur. Bu
bölüm 2026-09-09'da 4.604 satırdan bu listeye indirildi — anlatının tamamı git
geçmişinde duruyor. Elli iki karar 2026-09-09'da toplu olarak eklendi: hepsi
verilmiş ve hiçbiri duyurulmamıştı, ve bunu soran bir şey yoktu —
`TestTheChangelogNamesEveryUnreleasedDecision` artık soruyor (ADR 0098).

### Düzeltmeler

- **Üretim kodunun bir TEST paketini import etmesini hiçbir şey engellemiyordu**
  (D106). Kural tek bir godoc'ta yaşıyordu — GraphQL handler'ının yakalama
  yazıcısı, httptest'in "test ikilisine ait olduğunu" söyleyip tam bu yüzden dokuz
  satırı elle yazıyor. Düzeltmeden önce mutasyonla kanıtlandı: YAYIMLANMIŞ ağaçtaki
  bir dosyaya eklenen httptest import'u lint'ten ve bütün arch kapılarından geçti.
  Artık bir kapı `net/http/httptest`, `testing`, `testing/fstest` ve
  `testing/iotest`'i üretim dosyalarında reddediyor. Muafiyet LİSTELENMİŞ değil
  TÜRETİLMİŞ: hiçbir test-dışı dosyanın import etmediği paket, adı ne olursa olsun
  test desteğidir — bu, yayımlanmış uyumluluk paketi `core/identitytest`'i ve yerel
  `internal/benchbudget`'ı ikisini de adlandırmadan kapsıyor ve birinin üretimden
  erişilmeye başladığı günü yakalıyor. Türetimin kendisi yük taşıdığı için boyutu
  İKİ yönde de denetleniyor: her şeyi test desteği sayacak şekilde bozulduğunda
  gerçek bir ihlali yutup kapıyı yeşil bıraktı — ölçüldü, ve artık muafiyet sayısı
  genişlediğinde düşüyor.

- **Korumasız durum-değiştiren rotayı reddeden kapı, yalnız chi'nin FİİL
  metotlarını sayıyordu** (D103). `Handle`, `HandleFunc` ve `Mount` — üçü de HER
  metodu bağlar, yani POST'u da — nüfusun dışındaydı. Sonuç, kapının kaçakçılığın
  dürüst biçimini reddedip kaçak biçimini kabul etmesiydi: kompozisyon köküne
  konan `r.Post("/mcp", h)` "korunan hiçbir önekin dışında bağlanmış" diye düşüyor,
  `r.Handle("/mcp", h)` ise geçiyordu — ikisi de hiçbir şeyin doğrulamadığı,
  kotalamadığı ve kaydetmediği bir POST bağlarken. MCP sunucusunun nereye
  monte edilebileceği ölçülürken, kapıyı OKUYARAK değil oraya bir şey KOYMAYA
  çalışarak bulundu. Ağaçta saklanan bir şey yoktu: üretimdeki tek
  `Handle`/`HandleFunc` çağrıları operatör ve profil mux'larında ve o dosyalar chi
  import etmiyor, yani zaten nüfusun dışında; `callbacks.Mount(router)` ise TEK
  argümanlı ve toplayıcının mevcut arite kuralı onu adıyla muaf tutmadan dışarıda
  bırakıyor. Bu, kapının kendisinin karşı yazıldığı sınıf — denetlediği nüfus
  söylediği cümleden dar olan kural — ve doğrulanmamış bir POST'un bir ödemeyi
  "ödendi"ye çevirmesi yüzünden var olan kapıda.

- **CI ayrı modülleri hiç lint'lemiyordu** (D100, D102). Lint işi kökü
  `golangci-lint-action` ile geçiriyor, sonra `make vuln` koşuyordu; `make lint`
  hiç koşmuyordu — ki ayrı modülleri dolaşan hedef o. Aksiyon içinde durduğu modülü
  lint'ler, bu depo ise ALTI modül: kök, üç örnek, iki contrib ağacı. `examples/` ve
  `contrib/`e ulaşan tek linter pre-push kancasıydı ve `git push --no-verify` onu
  atlatıyor. Kuralı iş dosyasının KENDİ yorumu zaten söylüyordu, bir adım aşağıda:
  "`make vuln` üzerinden koşuyor, çıplak bir komutla değil, ki geliştiricinin
  yerelde koştuğu hedef CI'nın koştuğu hedef olsun". Artık lint de öyle, ve sürümün
  iki yerine tek evi var — aksiyon Makefile'ınkinin yanında kendi kopyasını
  pinliyordu. Bir kapı aksiyonu ADIYLA reddediyor ve dört hedefi `run:` satırlarında
  arıyor; ilk sürümü bütün dosyada arıyordu ve `make vuln`'den söz eden bir YORUM
  onu tatmin ediyordu, yani o adımı silmek yeşil kalıyordu. Aynı turda vuln
  hedefinin yorumundaki "ÜÇ modülde" sayımı da düzeltildi (D102): liste altıya
  çıkmıştı, cümle üçte kalmıştı — elle yazılmış bir sayım, saydığı şey büyüdüğünde
  sessizce yanlış olur.

- **Panelin varlıkları hiçbir zaman yeniden çekilmiyordu ve yetki arkasındakiler
  PAYLAŞILAN önbelleğe açıktı** (D94, D95). Damga yalnızca ETag'e giriyordu, adres
  hiç değişmiyordu ve yanıt `immutable` diyor — yani tarayıcıya bir yıl boyunca
  sormaması söyleniyordu. Kusur kendi godoc'unda yazılıydı: damganın baytlardan
  türediğini söyleyip "böylece operatörün tarayıcısı dosya gerçekten değiştiğinde
  tam o zaman yeniden çeker" diye bitiyordu, oysa hiçbir koşullu istek yapılmıyor
  ve yazıcıda `If-None-Match` dalı yok. Yanındaki test aynı iddiayı ADINDA taşıyıp
  yalnızca ETag'i doğruluyordu. Artık damga ADRESTE de duruyor, yani baytlar
  değişince adres değişiyor ve `immutable` dürüst hâle geliyor. İkincisi bir önceki
  commit'in kendi ürettiği kusur: ADR 0156 reviews betiğini ve her kayıtlı ekranın
  betiğini yetki arkasına aldı, `Cache-Control` ise `public` kalmıştı — araya giren
  bir vekilin, panelin reddettiği çağırana o baytları vermesi daveti.
  `core/http.WritePrivateAsset` yayımlandı ve panel iki yazıcı arasında REDDİ
  kuran AYNI kapsam tablosuna bakarak seçiyor, böylece yetki kazanan bir yol aynı
  düzenlemede herkese açık önbelleklenebilir olmaktan çıkıyor. Korunan şey baytlar
  değil — kurulumdan kuruluma aynılar — KURAL: reddeden bir uç, önündeki bir şeyin
  cevaplayabildiği bir kural koymamış olur. Kapı router'ı yürüyüp damga taşıyan her
  yanıtı buluyor ve İKİ yönü de iddia ediyor.
- **Sipariş ekranının satırları neden göstermediğine dair gerekçesi yanlıştı**
  (D96), iki kopyada. "Okuma katmanı BAĞLAR üzerinden birleştirir, modül İÇİNDE
  değil" cümlesi iki yarısında da yanlış: sipariş satırı kendi başına bir okuma
  varlığı, `order_id` süzgeci kabul ediyor, ve panelin KENDİ satış raporu onu aynı
  yüzeyden bir dosya öteden okuyor. Aynı cümle MÜŞTERİ adresleri için iki kez daha
  geçiyor ve orada DOĞRU — müşteri modülü tek bir varlık yayımlıyor, adres varlığı
  yok — bu yüzden ifade her yerde değiştirilmedi, her kopyanın ÖZNESİ ayrıldı.

- **Yüz otuz beş test dosyası, Docker'sız koşan HİÇBİR şeridin görmediği yerde
  duruyordu** (D88). Karşılanmayan bir derleme kısıtı dosyayı Go araç zincirine
  görünmez yapıyor: `go build ./...`, `go vet ./...`, `go test ./...` ve
  `golangci-lint run ./...` onu ATLIYOR, ve o dosyaları bugüne kadar derleyen tek
  şey `make test-integration` — bir kap isteyen, dakikalar süren, yani insanın EN
  SON koştuğu şerit. ADR 0152 için `Interop.CreateCollection` genişletildiğinde
  `payment_integration_test.go` içindeki üç çağrı yeri artık uymuyordu ve bunu
  elle koşulan `go vet -tags integration ./...` dışında söyleyen olmadı.
  `.golangci.yml` artık `run.build-tags` içinde `integration` adını taşıyor.
  Etiketin açılmasıyla hemen ortaya çıkanlar: ADR 0012'den beri her yerde
  reddedilen US yazım kuralına aykırı elli iki İngiliz imlası, on gocritic
  bulgusu, yutulmuş bir hata ve hiç çağrılmayan bir yardımcı — otuz sekiz
  dosyada altmış dört bulgu, hiçbiri yeni değil ve hepsi görünmezdi. Etiket
  listesi bilerek TEK: `smoke` dosyaları gerçek süreç başlatıyor, `load`
  dosyaları benchmark, ikisinin de kendi şeridi var ve hiçbiri bir refactor'ın
  sessizce kırdığı şey olmadı.

- **Kapatılmış bir sınır hâlâ SINIR olarak yayımlanıyordu** (D81). ADR 0136
  modüller arası METOT KÜMESİNİ derleme zamanı denetimine çevirdi; ama iki
  "bilinen sınırlar" belgesi de hâlâ "Cross-module signatures are not checked at
  compile time" diye açılıyordu. Daha kötüsü: `docs/mimari.md` KENDİSİYLE
  çelişiyordu — bölüm 5 bir önceki commit'te düzeltilmiş, bölüm 12'nin tablosu
  ve bölüm 5'in içindeki bir cümle bırakılmıştı. Ağaç noktasal değil SÜPÜRÜLDÜ:
  "derleyici denetlemiyor" cümlesinin yirmi kopyası var ve ON BEŞİ DOĞRU,
  bilerek dokunulmadı — öznesi JSON ŞEMASI ya da sınırdan geçen DEĞERLER, ki
  pin onlara dokunmuyor. Ayrım satırın tamamı: imza denetleniyor, ANLAM
  denetlenmiyor. Ve `docs/mimari.md`'nin kendi "Known limits" bölümü yirmi dokuz
  maddenin onunu taşıyan bir ALT KÜME olduğunu artık söylüyor.

- **Entegrasyon şeridi bu makinede bir yıllık ÖNBELLEK sayesinde yeşildi**
  (D80). CI "pull access denied for minio/minio" ile düştü; bu ileti eksik
  etiket gibi okunuyor ama değil — Docker Hub, sabitlenmiş etiketi de `latest`i
  de anonim çekime kapatıyor, yani sorun etiket değil DEPO. Aynı etiket MinIO'nun
  kendi kayıt defterinde (`quay.io/minio/minio`) kimliksiz servis ediliyor.
  Sabitleme hiç sorun değildi; değişen şey sabitin ALTINDAKİ erişimdi — bir
  pinin savunamayacağı sınıf. Yerelde her koşunun geçmesinin sebebi on iki aylık
  önbellekti; düzeltmenin gerçek olduğunun kanıtı da önbellekte `quay.io/...`
  adının bulunmaması: test çekmek ZORUNDA kaldı ve çekti.

- **Entegrasyon şeridi yirmi koşuda bir, bir test sahtesindeki VERİ YARIŞINDAN
  düşüyordu** (D79). Sahte, gönderim sınırına kaç soru sorulduğunu sayıyor — bir
  tekrarın HİÇBİR ŞEY sormadığını kanıtlayan şey bu — ve sayaç, iki koliyi iki
  goroutine'de açan test altında korumasız artırılıyordu. Yapısı gereği yarış;
  şeridin sessizliği dedektörün yalnızca gördüğünü bildirmesiydi. HEAD'de
  ölçüldü: yirmi koşuda bir. Sayaç testin goroutine'inden de OKUNUYOR, o yüzden
  okuyucu da korundu — yalnızca yazanı düzeltmek yarısını ayakta bırakırdı.

- **Mimari anlatısı her akışa saga diyordu, ve sayıları denetleyen kapı ona
  KATILIYORDU** (D78). `internal/workflows` altındaki yedi paketten yalnızca
  biri saga motorunu kullanıyor; belge okuyucuya her akışın bir yürütme kaydı,
  telafi zinciri ve idempotency anahtarı olduğunu öğretiyordu. Hiç anılmayan
  şekil ise iki kusura mal olan şekildi: tamamen otobüsle sürülen, hiçbir şeyin
  çözmediği akış. Düzyazıdan kötüsü DENETİMDİ — sayım sözlüğü `saga`yı
  `workflows`un eşanlamlısı sayıp dizin sayısına karşı fiyatlıyordu, yani
  "yedi saga" cümlesi onaylanırdı. Nüfus artık ithalden türetiliyor ve iki ad
  ayrı girdi. Aynı belgede iki cümle daha bayattı: bağın kanıtının e2e testi
  olduğu (oysa ADR 0136'dan beri derleme-zamanı pini) ve `<module>.interop`'un
  "sagalar/çekirdek için" olduğu (oysa modüller birbirininkini çözüyor).

### Kararlar

- **Bir MODEL istemcisi artık bu kuruluma soru sorabiliyor** (ADR 0161). Yönetim
  API'si yüz yirmi bir okuma işlemi cevaplıyor ve bugüne kadarki tek çağıranları
  bir tarayıcı ile curl'dü. "Hangi siparişler askıda" diye soran bir model
  istemcisinin, biri sarmalayıcı yazmadan girebileceği bir yol yoktu — ve bir
  sarmalayıcı, bayatlamakta özgür ikinci bir uç listesidir. Artık `gobit mcp`
  stdin'de JSON-RPC okuyup dört metot cevaplıyor: initialize, ping, tools/list ve
  tools/call. ARAÇ LİSTESİ açılışta, bu sürecin SERVİS ETTİĞİ belgeden türetiliyor
  — yönetim öneki altındaki her GET işlemi için bir araç — ve bir çağrı, bu
  sürecin monte ettiği router üzerinden süreç içi bir GET. Hiçbir şey yeniden
  yazılmıyor: bir araç ancak bir uç varsa var oluyor, ucun kendi describe bloğunun
  söylediğini söylüyor, ve çağrı yönetim yüzeyinin bütün halkalarından geçiyor —
  kimlik, kapsam, kota ve denetim kaydı. SALT-OKUNUR olması bir söz değil iki
  şeyin özelliği: liste yalnız GET'lerden kuruluyor, ve kimlik bilgisi üstün
  kapsamı taşıyorsa sunucu açılmadan reddediliyor (kurulumun kendisine, herhangi
  bir istemcinin kullanacağı uçtan sorularak). Adlar YOLDAN türetiliyor, çünkü bu
  belgede hiçbir işlem `operationId` taşımıyor — ölçüldü; yani bir araç ucu
  taşındığında taşınıyor, ki dürüst hata bu: taşınmayı atlatan bir araç başka bir
  şey hakkında cevap verirdi. Belge hangi aracın hangi YETKİYİ istediğini
  söyleyemiyor (kapsam taşımıyor), o yüzden anahtarın kapsamları karar veriyor ve
  reddedilen çağrı API'nin kendi hata zarfıyla dönüyor — eksik yetkiyi o
  adlandırıyor. İki sınır kaydedildi: bu ve belgenin düzyazısının bir kısmının
  Türkçe olması (yüz yirmi araç açıklamasının kırk üçü; defter DOSYALARI yönetiyor,
  içlerindeki metni değil).

- **Bir KOMUT artık sunucunun olaylarını almıyor** (ADR 0160, D104, D105). Dispatch'teki
  her fiil bütün uygulamayı açıyor — bilinçli, ve `seed`'in şemayı modüllerin
  kendisinden alması, `recover`'ın onardığı servislere ulaşması bu yüzden mümkün.
  Ama uygulamayı açmak modülleri kaydediyor, modülleri kaydetmek de onları olay
  otobüsüne ABONE ediyor. Bellek içi otobüste bu zararsız: otobüs sürecin kendisi.
  Redis'te değil — orada abone olmak bir bildirim değil, tüketici grubunu yoksa
  yaratıp ondan okuyan bir goroutine başlatmak, ve aynı gruptaki tüketiciler her
  mesajı YALNIZCA BİR KEZ alıyor. Yani `gobit seed`, Redis'li bir kurulumda
  sunucunun grubuna katılıp koştuğu sürece `order.placed`, `payment.captured` ve
  modüllerin dinlediği her topiği aldı. Aldığını da KOŞTURDU: bildirim modülünün
  abonesi, abone olan aynı `Register`'da kayıtlı, yani bir seed komutu sipariş
  onaylarını gönderdi. Ve çıkmadan önce onaylayamadığı mesaj, bir daha asla
  dönmeyecek `<hostname>-<pid>` tüketicisinin bekleyen listesinde kaldı — otobüste
  ne XAUTOCLAIM var ne bekleyen listesi süpürmesi (D105, AÇIK). Artık montaj bir
  olay ROLÜ alıyor: istek cevaplayan iki yol (sunucu ve facade'ın süreç-içi koşum
  takımı) tüketiyor, beş fiil ise gerçek otobüse YAYINLIYOR ve hiçbir şeye abone
  olmuyor. Yayın bilerek dokunulmadan bırakıldı — komut bir servis üzerinden
  yazarken aynı işlemde outbox satırı yazıp commit'ten sonra doğrudan yayımlıyor;
  otobüsü bellek içiyle değiştirmek o doğrudan yarıyı sessizce düşürürdü. Nüfus
  kapısı ÇAĞRI YERLERİNİ denetliyor: her `openApplication` çağrısı bir rol
  adlandırmak zorunda ve istek cevaplamayan bir tüketen çağrı reddediliyor — gelecek
  yılın fiili bir komşuyu kopyalayarak yazılacak ve her komşu bir komut.

- **Bir TARAYICI artık gobit'e karşı alışveriş edebiliyor** (ADR 0159, D99, D100).
  Bu depoda gobit'in önüne tarayıcı koyan hiçbir şey yoktu: vitrin API'si kırk sekiz
  rota, otuz yedisi misafire açık, ve herhangi birinin çalıştığının tek kanıtı onları
  HTTP üzerinden süren bir Go koşum takımıydı. "Bunun üstüne dükkân kurabilir miyim"
  diye soran birinin iki seçeneği vardı: testleri okumak ya da README'ye inanmak.
  Satırın istediği Next.js/SvelteKit şekli ÖLÇÜLDÜ ve reddedildi: bütün depoda ona
  değen tek kapı yol-dili kontrolü, yani bir node zinciri kendi kilit dosyası ve kendi
  açık yüzeyiyle DENETİMSİZ girerdi. Onun yerine `examples/storefront` öteki dördü
  gibi bir Go modülü: `main.go` yayımlanmış facade artı kendi modülü, modül de üç
  kabuk ve çerçevesiz tek bir betik servis ediyor. Sayfalar gobit'in KENDİ sürecinde
  koşuyor, yani tarayıcı `/store/v1` ile AYNI KÖKENDE ve hiçbir kurulumun örnek
  çalışsın diye CORS açması gerekmiyor. Dükkân hiçbir servis tutmuyor ve veritabanı
  okumuyor — her sayfadaki her rakamı tarayıcı çekiyor, ki örneği örnek hakkında değil
  YÜZEY hakkında bir iddia yapan şey bu. Keşfedemediği iki değeri (publishable key ve
  satış kanalı) AÇILIŞTA reddediyor: onlarsız başlayan bir dükkân sayfanın yaptığı her
  isteğe 401 verir ve boş katalog gibi görünür. Kendi içerik politikasını taşıyor ve
  panelinki OLAMAZ — katalog ürün görseli gösterir, panelin politikası ise `img-src`
  olmayan `default-src 'none'` ile başlar; bir gömenin kendi sayfaları için yayımlanmış
  politika yok, yani üç başlığı gömen kendisi yazıyor. Aynı değişiklikte `.js` dil
  taramasına girdi (D99): depo zaten içeriği hiç okunmamış iki elle yazılmış betik
  gönderiyordu ve bu örnek üçüncüsü olacaktı — operatörün SAYFANIN İÇİNDE okuduğu
  düzyazı, hiçbir şeyin denetlemediği tek gönderilen metin. Ve ayrı modüllerin CI'da
  hiç lint'lenmediği ortaya çıktı (D100, AÇIK): Lint işi kökü action ile geçiriyor ve
  `make vuln` koşuyor, `make lint` koşmuyor — D88'in bir ağaç ötedeki şekli. Ve
  beşinci modülü eklerken üçüncü bir şey çıktı (D101): hangi ayrı modüllerin
  YAYIMLANMIŞ yüzeye karşı derlendiğini söyleyen tablo elle yazılıydı ve hiçbir şeye
  bağlı değildi — taze girdiyi silmek bütün arch takımını yeşil bıraktı. Artık
  nüfusu Makefile'ın `SEPARATE_MODULES`'ından geliyor, ki onu da başka bir kapı
  diskteki go.mod'lara bağlıyor: zincir disk → Makefile → tablo.

- **İlk çalıştırma artık şeritlerin KOŞTURDUĞU bir belge** (ADR 0158, D98). Boş bir
  veritabanından bir alışverişçinin siparişine giden yol on beş çağrı ve on biri
  hiçbir yere yazılmamıştı: iki koşum takımının içinde yaşıyorlardı — smoke'un
  vitrin senaryosu ve `internal/e2e`'nin düzeneği, her biri kendi bölgesini, fiyat
  bağını ve stoğunu kendisi kuruyor. Bütün şeritler yeşildi ve boşluk TAM DA bu
  yüzden görünmezdi. `security.md` boş veritabanından yürüyen tek belgeydi ve
  katalogu okumakta bitiyor — boş veritabanında o boş bir liste, yani çağrının
  başarısı satın alınabilir bir şey olup olmadığı hakkında hiçbir şey söylemiyor.
  `gobit seed` de kapatmıyor: o fiil yük rig'ini kuruyor (elli iki bin ürün, toplu
  SQL) ve bölge yaratmıyor. Artık `docs/first-run.md` yolun tamamını yapıştırılabilir
  bir blok olarak taşıyor ve bir smoke senaryosu onu gerçek ikiliye karşı koşturup
  her BAĞ için bir durum kodunu ve sondaki siparişi doğruluyor. Yazmak, iki koşum
  takımının göremediği şeyi buldu: vitrin yardımcısı bölgesine İKİ ülke bağlıyor ve
  bu yüzden "tek yargı yetkisi adlandırılamıyor" dalından, BÖLGENİN oranıyla
  vergileniyor; tek ülkeli bir bölge — yani sıradan ilk kurulum — tax modülü
  tarafından vergileniyor ve orada vergi bölgesi yoksa cevap sıfır. Bu bir kusur
  değil (sunucu `tax_source=tax_unconfigured` ile uyarıyor) ama bunu bir operatöre
  söyleyen hiçbir şey yoktu. Nüfus kuralı ZATEN VARDI —
  `TestEveryChainedCurlFlowIsExecuted` belgeden türetilmiş anahtarlarla bir
  belge→tanık haritası tutuyor — ve yeni belge oraya bir HATA olarak geldi; o
  bulunmadan önce aynı iş için ikinci bir kapı yazılıp silindi.

- **Panelin ADRESİ artık panelin** (ADR 0157, D97). ADR 0155 içerik politikasını
  panelin rotalarının girdiği TEK bir chi grubuna kurmuştu; gerekçe doğruydu,
  ÖZNESİ yanlıştı: bir grup panelin BAĞLADIĞI her rotayı kapsar, politikanın
  söylediği cümle ise bir ADRES hakkında. Bir eklentinin `AddRoutes`'u aynı
  router'da, panelden SONRA koşuyor ve tek kontrolü desen çakışması — çakışmayan
  bir desen grubun dışına bağlanıyor. Probla ölçüldü, tartışılmadı:
  `/admin/ui/rogue` boş `Content-Security-Policy` ve `X-Frame-Options` olmadan 200
  dönüyordu, yanındaki panel rotası ikisini de taşıyordu. Delik ÜÇ halka değil BİR
  halka derindi — kompozisyon kökü öteki iki panel halkasını (köken ve kimlik)
  zaten ÖNEKE kuruyor, yani sayfa operatörün oturumunun içindeydi ve üzerinde
  hangi betiklerin koşabileceğini söyleyen tek kural yoktu. Politika artık o iki
  halkanın yanına, önekin tamamına ve ONLARDAN ÖNCE kuruluyor: bir reddi ve bir
  404'ü de kapsıyor. YETKİ ise politikanın kapatamadığı İKİNCİ delikti — ADR 0156
  her panel yolunu panelin kendi tablosunda fiyatlıyor, panelin bağlamadığı bir
  rota hiçbir tabloda değil, yani hiç yetkisi olmayan bir operatör ona ulaşıyordu.
  Bu yüzden karar iki yarım: bir eklenti artık panelin adresinin içine rota
  BAĞLAYAMIYOR, kayıt açılışta reddediliyor ve ret iletisi GİRİŞ YOLUNU
  adlandırıyor — `RegisterAdminPage`, bir yetki bildiren ve panelin kendi
  kaynağından servis ettiği bir betik veren yol. Önek iki yerde yazılı (panel ve
  `core/plugin`, ki o `internal`'ı import edemez) ve ikisini DAVRANIŞ bağlıyor:
  panelin kendi sabiti registry'ye veriliyor ve reddin ateşlenmesi gerekiyor —
  kaymış bir kopya, hiçbir şeyin servis etmediği bir adresi korurdu, ki bu kural
  gibi okunup kural olmayan şeydir. Ret SEGMENT sınırında eşleşiyor, yani
  `/admin/uipload` hâlâ bağlanıyor. Ağaçta panelin adresine bağlanan hiçbir şey
  yoktu; panele ulaşan tek eklenti zaten sanctioned yolu kullanıyor.

- **Bir panel ekranı artık bir YETKİYE mal oluyor** (ADR 0156, D92, D93). Yönetim
  API'si yetmiş bir rotada bir kapsam adlandırıyor ve `internal/e2e`'nin yetki
  matrisi, kapsamı olmayan geçerli bir kimliğin 403 aldığını uçtan uca
  kanıtlıyor. Panel aynı verinin İKİNCİ kapısıydı ve yalnızca kimliğe bakıyordu:
  halkası principal'ı çözüp bağlama koyuyor, çerçeve de ondan tek bir bit
  okuyordu — `_, signedIn := PrincipalFromContext(...)`. Kapsam listesi her
  istekte oradaydı ve atamada düşürülüyordu. Bu teorik bir hesap değildi:
  `POST /admin/v1/users` bir kapsam listesi alıyor, `PATCH` onu değiştiriyor, ve
  böyle bir hesap panele girip bütün katalogu, her müşterinin adını ve adresini,
  stok seviyelerini ve satış raporunu okuyordu — üstelik ürün başlığını
  DEĞİŞTİREBİLİYORDU, çünkü panel modül yazma yüzeylerine doğrudan çağırıyor.
  Artık her panel yolu, modülün kendi API'sinin kullandığı dizeyle yazılmış bir
  yetkiyle TEK bir tabloda listeli; rota tutmayan operatörü panelin kendi 403
  sayfasıyla ve eksik yetkiyi ADLANDIRARAK reddediyor, menü açılamayacak girdiyi
  düşürüyor, kapı da operatörü açabileceği İLK ekrana gönderiyor. Bir eklentinin
  kaydettiği ekran da yetkisini bildiriyor ve bildirmeyen bir kayıt AÇILIŞTA
  reddediliyor. Yetki EKRAN başına: açılmış bir ekranın GÖSTERDİĞİNİ daraltmıyor
  ve okuma katmanı principal'dan habersiz kalıyor (bilinen sınırlar). Kapıyı
  router'ı İKİ kez yürüyen denetim tutuyor — biri her rotanın yetkisiz operatörü
  reddettiğini, öteki her rotanın KENDİ yolunun listelendiği yetkiyi istediğini
  kanıtlıyor; ikincisi var çünkü bağlama satırı yolunu iki kez adlandırıyor ve
  bir ekranın yolunu başkasının handler'ıyla eşleyen satır yetkisiz operatörü
  aynı doğrulukla reddediyor. Aynı turda ADR 0155'in politika kapısının NIL
  sayfalarla kurulduğu ve bir eklentinin ekranını hiç yürümediği çıktı (D93).

- **Bir eklenti artık yönetim PANELİNE ekran koyabiliyor** (ADR 0155, D91). Panel
  altı ekranla geliyordu ve yedinciyi eklemenin yolu yoktu: `sections()` altı
  elemanlı PAKET-ÖZEL bir dilim ve `internal/adminui` `internal/` altında, yani
  dışarıdan adlandırılamıyor. Bir eklenti yönetim UCU açabiliyordu —
  `plugins/analytics` tam bu şekildi — ve onu okumanın tek yolu curl'du. Artık
  `core/plugin.AdminPage{Label, Path, Script []byte}` yayımlanmış ve
  `Host.RegisterAdminPage` onları topluyor; panel bunları kurucu argümanı olarak
  alıyor, bozuk bir kaydı AÇILIŞTA reddediyor, ve kabuğu, betiği ve menü girdisini
  TEK bir listeden bağlıyor. Betik URL değil BAYT ve politikayı mümkün kılan şey
  bu: panel onu KENDİ kaynağından servis ediyor, yani `script-src 'self'` yeterli
  ve hiçbir kurulumun politikası üçüncü bir köken için açılmıyor — eklenti
  kurmamış olanlar dahil. Eklenti şablon göndermiyor ve ADR 0030'un reddettiği
  alternatif reddedilmiş kalıyor: burada sahiplenilen TEK bir kabuk her kayıtlı
  ekranı çiziyor, betik onu /admin/v1'den operatörün kendi oturumuyla dolduruyor.
  Ve bu dilimin ikinci yarısı: panel bugüne kadar HİÇBİR içerik politikası
  taşımıyordu — ağaçta `Content-Security-Policy` sıfır kez geçiyordu, `X-Frame-Options`
  ve `Referrer-Policy` de. Bir operatörün oturum açtığı HTML'i çizen bir yüzey için
  bu zaten yanlıştı; ADR 0030'dan beri daha kötüydü, çünkü panelin yeni ekranları
  /admin/v1'in İSTEMCİSİ, yani panelin servis ettiği bir betik operatörün
  oturumunu taşıyor. Politika nonce'suz sıkı olabildi çünkü panel bunu YAPISIYLA
  hak etmişti ve bu ölçüldü: on dört şablon ve stil dosyası boyunca tam bir
  `<script>` var (defer'li src), satır içi stil yok, olay niteliği yok, görsel
  yok, `url()` yok, `template.HTML` yok. Politika her panel rotasını tutan TEK bir
  chi grubuna kuruldu ve router'ı YÜRÜYEN bir kapı yirmisinin de taşıdığını
  kanıtlıyor — handler başına bir çağrı, biri handler ekleyene kadar tutan bir
  kuraldır. İlk tüketici `plugins/analytics`: huni ekranı artık panelin menüsünde.

- **Bir proje artık BINARY'DEN başlatılabiliyor** (ADR 0154, D90). gobit bir
  kütüphane ve ön kapısı kapalıydı: hiçbir şey proje üretmiyordu, yani bir
  yazarın ilk adımı `cmd/server/main.go`'yu okuyup bir `go.mod` tahmin etmek,
  hangi ayarların var olduğunu tahmin etmek ve hangi servisleri kaldıracağını
  tahmin etmekti. `gobit new <dir>` artık binary'nin İÇİNE gömülü şablonlardan
  bir proje yazıyor. Ve dilimin şeklini belirleyen olgu şu — ölçüldü, tartışılmadı:
  `github.com/bdrtr/gobit@latest` v0.8.0'a çözülüyor ve o etiket kök paketi
  İÇERMİYOR (facade ondan sonra geldi), yani `require ... v0.8.0` + `import
  "github.com/bdrtr/gobit"` `go mod tidy`'de "does not contain package" ile
  düşüyor; `@latest` de aynı şekilde. Bugün import edilebilen tek sürüm bir
  commit'in pseudo-version'ı, ve üretilen `go.mod` onu yazıyor: ÜRETEN
  binary'nin derlendiği sürüm — etiketten derlendiyse etiket, değilse commit'in
  pseudo-version'ı. Hiçbirini bilemeyen bir derleme TAHMİN ETMİYOR, reddediyor ve
  `-replace` ile bir checkout'u gösteriyor. Biçim de önemli: bir etiket
  erişilebilirse proxy YAMAYI artırıp damgayı `-0.` ile öneklendiriyor, yoksa
  `v0.0.0-<zaman>-<hash>` veriyor — yanlışını yazan bir binary proxy'nin servis
  ETMEDİĞİ bir sürümü adlandırır ve üretilen projede `go mod tidy` onu sessizce
  üreticinin hiç seçmediği bir şeye çeviriri. Üç şablon tuzağı dosya ADLARIYLA
  kapatıldı ve üçü de ölçüldü: içinde `go.mod` bulunan bir dizin embed
  kümesinden SESSİZCE çıkıyor (`all:` bunu kaldırmıyor, ve satırın kendi önerisi
  olan "`examples/starter`'ı göm" tam bu yüzden imkânsız), `.go` ile biten bir
  şablon ağacın her üretim Go dosyasını ayrıştıran iki kapıyı ve `go build`'i
  aynı anda kırıyor, `.env.tmpl` ise deponun kendi `.gitignore`'u tarafından
  izlenmiyor. Üretilen proje DERLENİP KOŞULARAK kanıtlanıyor; o şeridin
  kanıtlayamadığı şey de yazılı: go.mod'u bu checkout'a çevirdiği için
  "şablon pinlediği sürümde çalışıyor" ile "ağacın ucunda çalışıyor" arasını
  ayırt edemiyor. Dil kapısı artık `.tmpl` tarıyor — bir şablonun düzyazısı
  başkasının projesine render ediliyor, yani orada kalan Türkçe burada kalmıyor,
  SEVK EDİLİYOR.

- **Bir mağaza artık sepetlerinin NEREYE gittiğini görebiliyor** (ADR 0153). Bir
  mağazanın vitrini hakkındaki ilk sorusu bir orandır: açılan sepetlerin kaçı
  siparişe döndü. Pay, sipariş modülü var olduğundan beri otobüstaydı; PAYDA
  hiçbir yerde yoktu, çünkü sepet modülü hiçbir şey yayımlamıyor ve
  `core/eventbus`'ı SIFIR kez import ediyordu — yani her alışverişçinin ilk
  dokunduğu modül, kendisi hakkında hiçbir şey söylemeyen modüldü. Artık iki olay
  yayımlıyor: `cart.created` ve `cart.completed`, ev deseniyle — işlemin İÇİNDE
  outbox satırı, commit'ten SONRA doğrudan yayım — ve gövdeyi TEK yerde kuruyor
  (sipariş modülü onu iki kez elle kuruyor ve iki kopyayı karşılaştıran hiçbir şey
  yok; ödeme modülünün notu bunu yazıyordu, bu onu izleyen üçüncü modül).
  Tüketicisi `plugins/analytics`: üç topiğe abone oluyor, olay BAŞINA BİR SATIR
  yazıyor ve `GET /admin/v1/analytics/funnel` ucunu açıyor. Tamamlama ile sipariş
  AYRI tutuluyor ve ucun gösterdiği en yararlı şey bu: saga siparişi İKİNCİ
  adımında veriyor, sepeti SON adımında tamamlıyor, yani arada düşen bir sipariş
  tamamlanmamış bir sepetle birlikte duruyor. Sayım TABLONUN özelliği: otobüs en
  az bir kez teslim ediyor ve yayımcılar olay kimliğini kayıttan TÜRETİYOR, o
  yüzden satırın anahtarı olayın kimliği ve `ON CONFLICT DO NOTHING`; artırılan
  bir sayaç tek bir yeniden teslimde olmamış bir orana dönüşürdü. Kimliği OLMAYAN
  bir olay yazılmıyor REDDEDİLİYOR — boş anahtar birincil anahtarı kapar ve
  sonraki her olay onun tekrarı gibi görünürdü (ürün modülünün üç topiği kimlik
  taşımıyor, yani bu varsayımsal bir şekil değil). Satırın önerdiği SIRA ölçülüp
  reddedildi: `core/provider`'a bir `Analytics` arayüzü + yayımlanmış-adlar
  defterine üç satır eklenip hiçbir gerçekleme yazılmadığında bütün `internal/arch`
  şeridi YEŞİL kalıyor — yani o dilim, var olmayan bir tüketici için 1.0.0'a
  verilmiş bir söz (ADR 0063 tam bunu reddediyor). Bedeli açık: iki topik daha
  ZORUNLU olarak iletiliyor ve `cart.created` ağacın en yüksek hacimli topiği —
  terk edilen her sepet artık bir webhook teslimi. Bu yüzden SATIR başına topik
  yok.

- **Mağaza artık müşteri için PARA TUTABİLİYOR** (ADR 0152). Geç kalan bir
  teslimattan sonra müşteriyi elde tutmanın iki yolu var — parayı geri göndermek
  ya da müşterinin hesabına yazmak — ve bu depo yalnızca birincisini
  yapabiliyordu: hiçbir modülün hiçbir tablosu bakiye tutmuyordu, yani
  "hesabınıza 200 lira yazdık" bir tabloda değil bir excel dosyasında duran bir
  sözdü. Artık ödeme modülünün yalnızca EKLENEN bir defteri var — müşteri ve para
  birimi başına işaretli tutar — ve onu kartın harcandığı yuvadan harcayan bir
  `store_credit` sağlayıcısı. Bakiye satırların TOPLAMI ve hiçbir yerde
  saklanmıyor: yetkilendirme EKSİ bir blokaj yazıyor, tahsilat hiçbir şey
  yazmıyor, iptal serbest bırakıyor — yani müşterinin harcayabileceği tutarın
  içinden açık blokajlar zaten düşülmüş oluyor ve bir düzeltme yeni bir SATIR.
  Kararı tablodan ayıran şey HARCAMA yarısıydı: bir kişiye ait parayı yalnızca o
  kişi harcayabilir, oysa ödemenin sahibini söyleyen taraf İSTEMCİYDİ — tahsilat
  bir referans ve bir tutar taşıyor, kimseyi adlandırmıyordu. Artık tahsilat
  müşteriyi taşıyor ve o müşteri sepetten geliyor (ADR 0125'ten beri KANITLANMIŞ
  olan alan), yani bir misafir sepeti krediyle ödeyemiyor — sağlayıcı kimseyi
  adlandırmayan oturumu reddediyor. Tehlikeli birleşim YAPILANDIRILAMIYOR:
  `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` açık bir kurulumda sağlayıcı hiç
  kaydedilmiyor, yani ödeme yöntemi KAYBOLUYOR — başkasının bakiyesini harcama
  yoluna dönüşmüyor. Kilit kararın korrektlik argümanı ve gerçek bir sunucuda,
  rakip bir işlemle kanıtlandı; ilk yazılan eşzamanlılık testi kilit
  KALDIRILDIĞINDA da geçiyordu (yerel sunucu her işlemi bir sonraki goroutine
  başlamadan bitiriyordu), o yüzden çakışmayı UMAN test yerine ÜRETEN test
  yazıldı.

- **Katalog artık ne kadar süre YENİDEN KULLANILABİLECEĞİNİ söylüyor** (ADR 0151).
  ADR 0044 satış kanalını katalog YOLUNA taşımıştı — "paylaşılan bir önbelleğin
  saklayabileceği şey budur" — ve bilerek hiçbir önbellek açmamış, tazelik
  politikasını da seçmemişti. Politikayı belirleyen olgu orada ÖLÇÜLMÜŞ: katalog
  gövdesi YAZMA OLMADAN değişebiliyor, çünkü fiyat listesi penceresi SAATE karşı
  açılıyor (`listablePrices` saati argüman alıyor). Yani yazmada geçersiz kılma
  asla tam olamaz — 09:00 geldiğinde hiçbir şey yazmıyor — ve tek tam olabilecek
  araç TTL. Artık üç kanal-kapsamlı okuma BAŞARI yolunda
  `Cache-Control: <kapsam>, max-age=<ttl>` yazıyor; TTL
  `STOREFRONT_CATALOG_CACHE_TTL`'den geliyor (sıfır — varsayılan — hiçbir başlık
  yazmıyor) ve kapsam `STOREFRONT_CATALOG_CACHE_SHARED` açık değilse `private`.
  İki ayar iki AYRI soru: TTL tazelik, `shared` GÜVENLİK — ADR 0044'ten beri
  publishable key gövdeye etki etmeyen bir KAPI, yani `public` bir CDN'in saklanmış
  gövdeyi ANAHTARSIZ çağırana servis etmesine izin verir. Çoğu mağaza tam bunu
  istiyor (kanalın kataloğu vitrinin dünyaya gösterdiği şey, anahtar da tarayıcıda
  duruyor) ama bu deponun onlar adına vereceği bir karar değil: varsayılan false ve
  paylaşılan bir kurulum açılışta UYARI alıyor. Başlık handler başına ve başarı
  yolunda yazılıyor; middleware rota tablosunu ikinci kez bilmek zorunda kalırdı ve
  handler'ın gövdeyle mi retle mi cevaplayacağını GÖREMEZ — CDN'in TTL boyunca
  sakladığı bir 404, düzeltildikten sonra da kayıp kalan bir ürün demek. Negatif
  TTL açılışı durduruyor, sıfır kabul ediliyor: sıfır bir cevap, `-1h` ise
  yaptığını söylediğini sanan bir yazım hatası. Beş mutasyondan biri hayatta kaldı
  ve kusur KODDA değil TESTTEydi: doğrulayıcı `-1h`'yi reddediyordu ve bunu hiçbir
  test tutmuyordu.

- **gobit'i GÖMEN bir program artık onu kendi testinde ayağa kaldırabiliyor**
  (ADR 0150). gobit bir KÜTÜPHANE (ADR 0025) ama onu gömen bir programın ona karşı
  test yazma yolu yoktu: facade yalnızca `Main(args, out)` sunuyor — porta bağlanıp
  blokluyor — ve arkasındaki her şey (göçler, modül kaydı, router, koruma
  halkaları) `internal/` altında, dışarıdan erişilemez. Kalan iki seçenek ikiliyi
  çalıştırıp sokete konuşmak ya da montajı kendi testinde YENİDEN YAZMAKTI; bu
  deponun ikincisinin bedelini bildiği bir kaydı var (ADR 0141 tam o kopya
  kaydığı için var). Artık `App.InProcess(ctx)` bütün kurulumu ayağa kaldırıp
  `Main`'in sunacağı `http.Handler`'ı dönüyor — ve AYNI montaj fonksiyonundan
  geçerek, çünkü kendi montajı olan bir koşum takımı "kurulum nedir" sorusuna
  ikinci bir cevap olurdu ve testlerin güvendiği cevap kimsenin deploy etmediği
  olurdu. PORTU ya da SAATİ olan hiçbir şey başlamıyor: HTTP sunucusu yok,
  operatör dinleyicileri yok, ZAMANLI İŞ yok — bir relay'in testin kendi
  iddialarının altında tıklaması, arızayı testin ne zaman baktığına bağlar. Bedeli
  yazılı: olay abonesine yalnızca DOĞRUDAN yayımla ulaşıyor, outbox satırının
  verdiği sözü tutan relay çalışmıyor. Yapılandırma ORTAMDAN okunuyor, tıpkı
  `Main` gibi (ikinci bir yapılandırma yolu, hiçbir deployment'ın kullanmadığı
  varsayılanlarla koşan bir test demek), ve bunun bedeli böyle bir testin
  `t.Parallel` olamaması. Facade'ı genişletmek yayımlanmış yüzeyi denetleyen
  kapıda bir delik de buldu (D86): ad envanteri yalnızca `core/` ağacını
  yürüyordu, oysa paket listesi facade'ı da yayımlanmış ilan ediyor — yani
  `gobit.App` ve metotları 1.0.0'a kadar tutulacak, hiçbir şeyin denetlemediği
  sözlerdi ve `InProcess` eklendiğinde kapı yeşil kaldı.

- **Bir taşıyıcıya artık kolinin NEREDE olduğu sorulabiliyor** (ADR 0149). Takip
  numarası sevk anında iliştirilebiliyordu ve çizelge beş anı taşıyordu, yani ELLE
  yarısı tamdı; SAĞLAYICI yarısı yoktu — kargo sözleşmesi üç metottu (`Quote`,
  `Create`, `Cancel`) ve etiket basıldıktan sonra taşıyıcıya hiçbir şey
  sormuyordu. Boşluğu kutudaki sağlayıcının kendi godoc'u adlandırıyordu:
  `GetShipment` "çekirdek sözleşmenin parçası DEĞİL… iki defterin birbirinden
  ayrıştığı bir hata ancak böyle görülebilir". İki defter bilerek ayrı tablolar ve
  gerçekten ayrışıyorlar: sevk işaretlendiğinde modül `shipped` derken sağlayıcının
  satırı `pending` kalıyor, ve operatörün yazdığı takip numarası etiketin açıldığı
  numaranın yanında duruyor — yani yanlış numarayla kaydedilmiş bir koli artık
  GÖRÜNÜYOR. `core/provider` artık İSTEĞE BAĞLI bir `ShipmentTracker` yayımlıyor
  (`Track`, bir OKUMA, sağlayıcının kendi kimliğiyle) ve
  `GET /admin/v1/fulfillments/{id}/tracking` taşıyıcının görüşünü modülün kaydının
  YANINDA veriyor, hiçbir şey YAZMADAN: hangi tarafın yetkili olduğu sağlayıcıya
  bağlı — gerçek taşıyıcı kolinin nerede olduğunu bilir, kutudaki sağlayıcı ise
  mağazanın kendisi, orada operatörün kaydı doğrudur — ve yazmak iki durumdan
  birinde yanlış tarafı seçmek olurdu. Cevap BEŞ biçimli ve istemci ADA bakıyor,
  boşluğa değil: "pending" diyen bir taşıyıcı ile sorulamayan bir taşıyıcı aynı boş
  alanları üretir. Yedi mutasyon ısırdı, biri hayatta kaldı ve kusur KODDA değil
  TESTTEydi: fikstürlerin hiçbirinde modül tarafı boş değildi, o yüzden iki numara
  zaten farklıydı — kapatan test iki tarafı da boş olan koli.

- **Bir kural artık ürünün NEYE AİT olduğunu sorabiliyor** (ADR 0148). ADR 0144
  kümeyi okuyan işleci (`any_in`) getirdi ve bağlam tarafına kümeyi verdi; satır
  tarafına veremedi, çünkü bir ürünün kategorileri ve etiketleri ürün satırının
  kolonu değil ve hiçbir şey onları yayımlamıyordu. Yani işleç vardı, sorularının
  yarısı sorulamıyordu: tüccar "bu ürüne %20" ve "bu koleksiyona %20" yazabiliyor,
  mağazanın gerçekten yürüttüğü kampanyayı — bir KATEGORİYE indirim —
  yazamıyordu. Kural satırı kaydediliyor, yönetim ucu 200 dönüyor ve indirim
  sessizce sıfır kalıyordu. Artık ürün kaydı `category_ids` ve `tag_ids`
  yayımlıyor ve sepet her satırın listelerini gönderiyor. Okumalar YALNIZCA alan
  adlandırıldığında yapılıyor: panelin ızgarası, bağ çözümü ve verginin tür
  okuması hiçbir şey ödemiyor — ama BOŞ alan seçimi (yani "kaydın tamamı")
  ödüyor, çünkü tamamı doğru olmak zorunda. Üyelik DOĞRUDAN, ki sağlayıcının
  `category_id` SÜZGECİNİN verdiği cevabın aynısı: üst kategoriyi adlandıran bir
  kural, yalnızca alt kategorilerde dosyalanmış ürünlere ulaşmıyor ve bu sınır
  known-limits'te süzgecinkinin yanında duruyor. Kargo yöntemi liste TAŞIMIYOR ve
  taşımayacak — hiçbir kategoride değildir — yani kargo hedefli bir kategori
  kuralı hiçbir şey seçmiyor, ki doğru cevap budur. Ve bu turda düzyazıda kalmış
  bir kural kapıya çevrildi: promosyon modülü aynı hesabı İKİ yüzeyde cevaplıyor
  (sepetin interop'u ve `POST /admin/v1/promotions/compute`), ikisinin godoc'u da
  şekillerin BİREBİR aynı kalmasını söylüyordu — alan birine eklendi, ötekine
  eklenmedi ve hiçbir şey düşmedi (D85).

- **İkinci etken artık İSTENİYOR** (ADR 0147). ADR 0143 yöneticiye ikinci etkeni
  TUTACAK yeri verdi — mühürlü TOTP sırrı, iki uç, RFC 6238 — ama "bu kişi
  telefonunu kanıtladı mı" sorusunu soran metodun tek çağıranı kendi testiydi:
  kişi kaydolup onaylıyor, sonra parolasıyla hiçbir şey olmamış gibi giriyordu.
  Bu deponun kendi tekrarlayan kusuru, ve burada daha kötüsü — yaptığını iddia
  ettiği şey yönetimi korumak, ve açan kurulum korunduğuna inanıyordu. Artık
  `Login` jetonu imzalamadan önce hesabın kanıtlanmış etkenini soruyor; kod giriş
  gövdesinde geliyor ve retler KENDİLERİNİ adlandırıyor (`auth_mfa_required`,
  `auth_mfa_code_wrong`), ikisi de yalnızca parola DOĞRU çıktıktan sonra, yani
  yabancıya hesap hakkında bir şey söylemiyorlar. Yanlış kod bir deneme sayılıyor
  (altı haneyi tahmin etmenin tek sınırı o sayaç), eksik kod sayılmıyor (sıradan
  iki adımlı girişin ilk yarısı). ADR 0143'ün bıraktığı üç soru ŞEKİLLE
  cevaplandı: makine etkilenmiyor (talep parola girişinde, anahtarın
  authenticator'ı yok), kanıtlanmamış kayıt hiçbir şey istemiyor (yarıda kalan
  tarama kimseyi kilitlemiyor), ve yeniden kayıt artık onayı SİLMİYOR — yeni sır
  `pending_secret`'ta kanıtlanmışın YANINDA bekliyor, çünkü silmek ikinci etkenden
  hiç sır gerektirmeyen bir çıkış yolu olurdu. Telefonunu kaybeden artık kendi
  başına düzeltemiyor ve hiçbir uç onun yerine düzeltmiyor: meslektaşının etkenini
  kaldırabilen bir yönetici, çalınmış TEK bir oturumu her hesaba parolayla girmeye
  yeterli kılardı. Kalan yol makinede: `gobit mfa-reset <email> -confirm <email>`.
  Hiçbir şey mağaza genelinde zorunlu değil (kimse kaydolmadan zorunluluk herkesi
  aynı anda kilitler) ve bu known-limits'e yazıldı. Ve `Login`'i genişletmek
  `adminui.Session`'ı derlenen her şeritte YEŞİL kalarak kırdı: panel beş yüzeyi
  adla çözüyor ve hiçbiri sabitlenmemişti — arıza AÇILIŞTA bekliyordu; beşi de
  artık sabitli.

- **Bir operatör artık TELEFONDAN sipariş alabiliyor** (ADR 0146). Sepetin
  yönetici yüzeyi kararla salt okunurdu: panelden yapılan bir düzeltme,
  müşterinin baktığı tutarı arkasından değiştirmek demekti. O gerekçe bir sepeti
  DEĞİŞTİRMEYİ kapsıyor, AÇMAYI değil — ve sipariş modülü boşluğu dolduramıyor,
  çünkü `CreateOrder`'ın rotası bilerek yok: HTTP üzerinden açılan bir sipariş
  çağıranın belirlediği bir toplamı taşır. Tutarı sunucunun yapan şey sepettir.
  Yüzey tam olarak iki yazma kazandı — sepeti açmak ve FİYATLANMIŞ bir satır
  eklemek — ve satır yazması ZORUNLU bir `sales_channel_id` taşıyor: yönetici
  anahtarı kanal taşımaz, kanalsız bir kimlik "kanalsız" değil "hiçbir kanala
  bağlı" demektir, yani talep operatörün kataloğunun tamamını yazdığı varyant
  kimliği hakkında bir iletiyle reddederdi. Handler kanalı principal'a YAZIYOR,
  böylece sepetin mevcut kapsam kuralı atlanmak yerine olduğu gibi koşuyor.
  Sepet açmak kanal İSTEMİYOR: o yolda kanalı hiçbir şey okumuyor, istemek
  kimsenin bakmadığı bir bağlama yazılan bir iddia olurdu. Bedeli yazılı:
  kapsamı sunucu kanıtlamadı, iddia her istekte ayrı yapılıyor (bir sepetin iki
  satırı iki kanal altında yazılabilir) ve operatör müşterinin elindeki sepete
  satır ekleyebiliyor — ama gördüğü şeyi DEĞİŞTİREN hiçbir şeyi yapamıyor ve
  parayı hâlâ müşteri, mağaza yüzeyinden, önündeki toplama karşı ödüyor.

- **Bir değişim artık BAŞKA BİR ÜRÜN gönderebiliyor** (ADR 0145). Modüldeki her
  satış-sonrası kalemi var olan bir sipariş satırını NOT NULL bir yabancı
  anahtarla gösteriyordu; müşterinin zaten sahip olduğu maldan söz eden kayıtlar
  için doğru, bir DEĞİŞİM için değil. "Aynı gömleği bir beden büyük gönder"
  sıradan değişimdir, ama `order_replacement_items` yalnızca siparişte zaten
  olan varyantın birimlerini ifade edebiliyordu — ve değişimin para yarısı
  ADR 0120'den beri fark tahsil edebiliyor, cevaplayacağı mal olmadan. Kalem
  artık satır YERİNE bir varyant adlandırabiliyor (şemada CHECK: tam olarak
  biri). Aşağı akışta hiçbir şey değişmedi ve bu şans değil ölçümün bulgusu:
  sevkiyat akışı zaten yalnızca varyanttan çalışıyordu, satır oradaydı çünkü
  satırın kendisi ne gönderdiğini söyleyemiyordu. Bedeli yazılı: satır kalemini
  "alınandan fazlası olamaz" sınırlıyor, varyant kalemini ise yalnızca
  operatörün YAZDIĞI fark tutarı — bu known-limits'e eklendi.

- **Bir kural artık "şu gruplardan HERHANGİ BİRİNDE mi" diye sorabiliyor**
  (ADR 0144). Müşteri tüccarın koyduğu kadar grupta olur, ama sepet yalnızca
  BİRİNİ gönderebiliyordu: sıralı baş (ADR 0049). Yani {retail, vip}
  gruplarındaki, başı retail olan bir müşteri `customer_group_id in [vip]`
  kuralına UYMUYORDU — segmentin İÇİNDEKİ birine segment indiriminin sessizce
  uygulanmaması, ki ADR 0103 tam bu kusurla açılıyor. Dokuzuncu bir işleç
  (`any_in`) bağlamın değer KÜMESİNİ okuyor ve sepet bütün grupları sıralı başın
  YANINDA gönderiyor. Eski işleçler listeye BAKMIYOR: gönderilmiş bir `in`
  kuralının cevabı aynı kalmalı, yoksa canlı bir indirim hiçbir şey duyurmadan
  genişlerdi. Ölçüm ayrıca satırın "eksik" dediği üç hedefin de bağlı olduğunu
  ve `cart/discount.go`'daki bir cümlenin ("müşteri grubu bağlama KONMUYOR")
  uzun süredir yanlış olduğunu buldu.

- **Bir yönetici artık İKİNCİ BİR ETKEN taşıyabiliyor** (ADR 0143). `auth_mfa_
  credential` göçü, ve `/admin/v1/auth/mfa` altında iki uç: kayıt ve onay.
  Uçlar hiçbir kullanıcı adlandırmıyor — çağıranın KENDİSİNE etki ediyorlar,
  çünkü meslektaşı adına kayıt açabilen bir yönetici onun telefonunun sırrını
  elinde tutardı; api_key ile yapılan istek de reddediliyor, makinenin
  doğrulayıcısı yok. Kayıt, ilk doğru koda kadar SAYILMIYOR.

  Bu, modülün geri okuyabildiği İLK sır: parola argon2id, api anahtarı ve davet
  jetonu SHA-256, hiçbiri geri getirilemez — ama altı haneyi doğrulamak onu
  yeniden hesaplamak demek. Sır AES-GCM ile mühürleniyor ve anahtarı kurulum
  veriyor (`MFA_SECRET_KEY`); anahtar yoksa kayıt REDDEDİLİYOR, çünkü öntanımlı
  bir anahtar anahtar değildir ve düz metin, adının yaptığından azını sessizce
  yapan bir güvenlik özelliğidir. Anahtar `JWT_SECRET`'tan AYRI: ikisi farklı
  saatlerde döndürülür. TOTP bağımlılık olarak değil YAZILARAK geldi ve RFC
  6238'in kendi test vektörlerine karşı doğrulanıyor. Giriş akışı henüz
  dokunulmadı — zorunlu kılmak ayrı bir karar.

- **İki eylem de AYNI HEDEFİ hesaplıyor** (ADR 0142, D82). Bir satırın iptal
  edilen birimlerini rafa iki eylem koyuyor: yazımın kendisi ve gerisini tutan
  kolinin iptali. İkisi de FARK hesaplıyordu, ve koli eylemi "yazımın zaten geri
  koyduğu" terimini çıkarıyordu — okumadığı, VARSAYDIĞI bir sayı. Otobüs sıra
  vaat etmiyor: doğrudan yayımı kaybolan bir yazım, outbox aktarıcısıyla bir
  dakika sonra, operatör koliyi iptal ettikten SONRA geliyor. Ölçüldü: beş
  birimlik bir iptal için rafa SEKİZ birim yazıldı, ve iki eylemin referansları
  farklı olduğu için defterin tekilliği bunu göremiyordu. Artık ikisi de
  `min(iptal, satılan − kolideki)` hedefini hesaplıyor ve modül, kilidin altında
  farkı hareket ettiriyor; sıra önemsizleşiyor ve yeniden teslim edilen olay
  hedefi zaten karşılanmış buluyor. `inventory_movements` satır kimliği taşıyan
  bir sütun kazandı, referansın tekil indeksi ise DÜŞTÜ — aynı eylem hedefi
  büyüdüğünde meşru biçimde ikinci kez yazıyor.

- **Uçtan uca zemin, üretimin bağladığı her akışı bağlıyor** (ADR 0141).
  `internal/e2e` modül ve akış kümesini ELLE kuruyor — bilerek, çünkü gerçek
  kurulumu çağıran bir zemin modülleri değil kurulumu sınardı. O kopyanın
  bedeli kopya olmasıydı: üretim yedi akış bağlıyordu, zemin altı. Eksik olan,
  deponun YALNIZCA otobüsle sürülen tek akışıydı — hiçbir şey onu çözmüyor,
  hiçbir şey çağırmıyor, yani bağlanmamış hâli hiçbir isteği kırmıyor ve hiçbir
  testi kızartmıyor; yalnızca stok rakamı eksik kalıyor. D75 ile D76 tam orada
  haftalarca durdu. Kapı artık iki kökün İTHAL ettiği akış paketlerini
  karşılaştırıyor, ve zemin ADR 0134/0135/0139/0140'ı aynı anda gören ilk
  senaryoyu koşuyor.

- **Bir koli artık hangi sipariş için açıldığını KAYDEDİYOR** (ADR 0140). Koli
  açmanın iki yolu var ve hiçbiri ikisini birden yapmıyordu: akışın açtığı koli
  bağlıydı ama kalem taşımıyordu (açtığı yüzey kalem almıyor), modülün yönetici
  ucunun açtığı koli kalem taşıyordu ama hiçbir şeye bağlı değildi. Gerçek bir
  veritabanına karşı ölçüldü: üç birimlik bir koli `CommittedQuantities`'e
  `{oli: 3}` diyor, bağ listesine BOŞ. Yani `committed` terimi, katkısı olan her
  koli için yapısal olarak sıfırdı — ve o terim ÜÇ kararın ortasında duruyor
  (ADR 0134, 0135, 0139). Bağı artık tanımın sahibi olan modül yazıyor; akıştaki
  yazım kaldırıldı, çünkü aynı kuralın iki yerde olması onun bir yerde
  unutulmasının sebebiydi.

- **İptal edilen bir koli, tuttuğu birimleri geri veriyor** (ADR 0139). Bir
  satırın kaç biriminin rafa ait olduğu `min(iptal, satılan − canlı kolide)`
  ve bu ifadenin İKİ tarafı da oynuyor; ama yalnızca birinin olayı vardı.
  Açık bir kolinin altında iptal edilen satır, kutunun dışındaki birimleri geri
  koyuyor ve gerisini doğru biçimde bırakıyordu — ADR 0135 tam o duruma bakıp
  "çerçeve kimsenin sevkiyatını kendi başına geri çekmez" dedi ve dükkânın
  çözümünü aynı cümlede adlandırdı: koliyi iptal et. Koliyi iptal etmek bir
  durumu çeviriyor, stoğa dokunmuyor ve KİMSEYE söylemiyordu; fulfillment
  modülü hiç olay yayımlamamıştı. Böylece o birimler ne kolide, ne müşteriye
  borçlu, ne rafta kalıyordu. Modül artık `fulfillment.canceled` yayımlıyor ve
  iptal akışı ikinci olayı olarak dinliyor: bıraktığı miktar, iki pencerenin
  FARKI — durumların farkı olduğu için akışın daha önce ne iade ettiğini
  hatırlamasına gerek yok, ve iki koli hangi sırayla iptal edilirse edilsin
  toplam aynı.

- **Konteyner toplayıcısı, makinenin çöpe atıldığı yerde kapalı** (ADR 0138).
  Doğrulama şeridi 11 Eylül'de iki kez, birbiriyle ilgisiz iki pakette,
  altmışar saniye bekledikten sonra kırmızıya döndü; beklenen şey testin
  istediği Postgres değil, süreçler arasında PAYLAŞILAN Ryuk konteyneriydi —
  son istemcisi ayrıldıktan on saniye sonra kendini sonlandırıyor, kaydı
  silinene kadar etiket aramasına yakalanıyor, ve ölmüş bir konteynerin asla
  yayımlamayacağı bir port için altmış saniye bekleniyor. GitHub koşucusu iş
  bitince yok edildiğinden toplayıcının koruyacağı bir şey yok: iki işte
  kapatıldı, ve kararı meşrulaştıran iddia — koşucunun geçici olduğu — bir
  arch kapısıyla iki yönde çivilendi.

- **Bir meslektaş artık KENDİ ilk parolasını belirliyor** (ADR 0137). Bugüne
  kadar bir kullanıcı eklemenin iki yolu vardı ve ikisi de yanlıştı: ya
  `CreateUser`'a parolayı siz yazıyordunuz — yani bir kişi bir başkasının sırrını
  biliyordu ve "bu kullanıcı olarak kim davranabilir" kaydı ilk dakikadan yanlıştı
  — ya da parolasız yaratıp hiç `auth_identity` satırı yazmıyordunuz, ki o da giriş
  yapamayan ve sebebini hiçbir yerin söylemediği bir hesap. Artık davet var:
  `POST /admin/v1/users/{id}/invitations` açıyor, `POST /admin/v1/auth/accept-invitation`
  harcıyor. Jeton yanıtta DEĞİL — davet API'den geri verilseydi yönetici gene
  meslektaşının ilk-parola bağlantısını tutuyor olurdu. Kabul ucu ikinci korumasız
  admin yolu ve öyle olmak zorunda: onu çağıran kişinin henüz kimlik doğrulayacağı
  bir hesabı yok. **Ve bunu mümkün kılan şey**: bildirim modülü artık modüller-arası
  bir yüzeye sahip — o güne kadar gobit içinden posta göndermenin tek yolu bir
  OLAYDI, ve bir olay kalıcı akışta durur, en az bir kez teslim edilir ve
  operatörün üçüncü taraf uçlarına İLETİLİR; tek kullanımlık bir davet jetonu
  bunların hiçbiri olamaz.

- **Derleyici artık HER interop çiftini denetliyor** (ADR 0136). Bir tüketici,
  ihtiyaç duyduğu dar arayüzü KENDİ paketinde tanımlıyor ve somut değeri
  container'dan isimle çözüyor; iki taraf birbirini ithal etmediği için bir imza
  kayması iki tarafta da derleniyor ve ancak ÇÖZÜM anında patlıyor — istek
  yolunda, `sync.Once` ile önbelleğe alınmış hâlde, başlangıç yeşilken. Ve
  patlamıştı: `*cart.Interop` ne `ApplyPromotionCode` ne `RemovePromotionCode`
  taşıyordu, yani iki vitrin kupon ucu kod yazan ilk müşteriye 500 dönüyordu
  (D73). Bunu yazmayı engelleyen şey başka bir modülün godoc'undaki bir cümleydi:
  "derleyici ikisini asla birlikte görmez" — yanlış; aynı Go modülündeki ÜÇÜNCÜ
  bir paket ikisini de ithal edebiliyor. `internal/arch/interop_pins_test.go`
  artık otuz yedi atama taşıyor, ve nüfusu diskten türeten bir kapı listenin tam
  kalmasını sağlıyor (ilk koşumunda elle yazdığım listede iki eksik buldu).

- **Bir koli artık siparişin BORÇLU olduğundan fazlasını taşıyamıyor**
  (ADR 0135). `POST /admin/v1/fulfillments` satır kimliği ve adet alıyordu ve
  hiçbirini siparişe karşı denetlemiyordu — okuyarak doğrulandı: boş kimlik,
  global bir adet aralığı ve aynı satırın iki kez geçmesi reddediliyor, başka
  hiçbir şey. Ne satırın o siparişe ait olduğu, ne adedin satılanın içinde
  kaldığı, ne birimlerin başka bir kolide olduğu, ne de iptal edilmiş oldukları.
  Yani bir operatör, müşteriye iptal edildiği söylenen malı sevk edebiliyordu —
  ve ADR 0134'ten beri o birimlerin stoğu rafa geri döndüğü için aynı mal iki kez
  çıkıyor, sayım farkı kadar eksiliyordu. Artık fulfillment modülü `fulfilling`
  akışını istek anında çözüp `alınan − iptal − canlı kolideki` sınırını soruyor ve
  aşan kalemi reddediyor. Uç KIMILDAMIYOR, ve sınır okunamazsa koli açılmıyor —
  okunamayan bir sınır sınır değildir (D72).

- **Bilinen sınırlar belgesi artık contrib kimlik modüllerini de KAPSIYOR**
  (D71). `docs/known-limits.md`, gobit'in yapmadığı şeyleri okumak için açılan
  belge, ve kimlik bölümü reddeden dört rotayı kapatmanın yolunu "tek satır
  bağlama: bir doğrulayıcı bağla" diye bitiriyor. ADR 0127'den beri bağlanacak
  BİR TANESİ var — bu depoda — ve dosyada `contrib` kelimesi hiç geçmiyordu.
  Yani belgenin kendi tavsiyesini izleyen okur ne onun var olduğunu ne de neyi
  kapatmadığını öğreniyordu: imzalı çerez süresi dolmadan iptal edilemez, çalınmış
  bir çerez kendi passkey'ini kaydedip sahibinin anahtarını kaldırabilir,
  kurulumun bağladığı bir kimlik bilgisi deposu hiçbir veri-sahibi yeteneğini
  yanıtlamayabilir, `Options.RPID` değişimi kayıtlı her anahtarı terk eder, ve
  kaydolmanın varsayılan hız sınırı SÜREÇ başına. Hepsi bir ADR'de yazılıydı;
  hiçbiri birinin sınır aradığı yerde değildi.

- **İptal edilen birimler artık RAFA geri dönüyor** (ADR 0134). Checkout'un son
  adımı rezervasyonları onaylıyor, yani stoku DÜŞÜYOR — o hâlde var olan bir
  siparişin birimleri satılabilir sayıdan çıkmış oluyor, ve sonradan silinen bir
  satır hem kimsenin göndermeyeceği hem de stok sayılmayan bir birim. Hiçbir şey
  onu geri koymuyordu: ne tam sipariş iptali, ne ADR 0113'ün kısmi iptali, ve
  order modülü koyamaz (birimler başka bir modülde). Artık order
  `order.line_canceled` yayımlıyor (outbox + doğrudan) ve YENİ bir akış abone
  oluyor: fulfillment'a canlı kolinin kaç birim tuttuğunu soruyor ve
  `min(iptal toplamı, alınan − kolideki)`'nin artışını geri koyuyor — yani ikinci
  iptal çifte saymıyor ve sevk edilmiş birim rafa dönmüyor. Deponun İLK yalnızca
  dinleyen akışı. Stok geri koyma ilk kez İDEMPOTENT: veri yolu en az bir kez
  teslim ediyor, o yüzden iptal kimliği hareketin referansı ve defter onu tekil
  tutuyor (D70).

- **Bir müşteri artık KENDİ hesabını açabiliyor** (ADR 0133).
  `contrib/identity-session` yalnızca giriş yapıyor ve bir operatörün kimlik
  bilgisi yazmasına izin veriyordu; bir müşteri hesap açamıyordu. İki uç eklendi:
  kaydolma ve doğrulama. Kaydolma kişi hakkında HİÇBİR ŞEY yaratmıyor — ne
  müşteri, ne kimlik bilgisi, ne oturum; yalnızca bu modülün kendi tablosunda
  adresi, parolanın argon2id özetini ve token'ın özetini tutan bir satır. Hesabı
  olan adres için de AYNI 202 dönüyor, yoksa "bu kişi burada alışveriş ediyor mu"
  sorusu herkese cevaplanır; farklı olan gönderilen mesaj. Token
  `DELETE ... RETURNING` ile tüketiliyor, yani tek-kullanımlık olması kilide
  ihtiyaç duymuyor, ve hesap açılmadan ÖNCE harcanıyor. Müşteri kaydını kim
  yaratacağı kurulumun bağladığı bir seam: `customer.service`'in
  `RegisterGuestCustomer`'ı "aynı e-posta engel değil" diyor, yani kaydolma için
  yanlış semantik. Uçlar seam bağlanmadıkça YOK — tipli nil de bağlanmamış sayılır.

- **İki contrib kimlik modülü artık bir VERİ SAHİBİNE cevap veriyor**
  (ADR 0132). `contrib/identity-session` ve `contrib/identity-passkey`, ADR
  0029'un üç veri-sahibi yeteneğinden hiçbirini gerçeklemiyordu — oysa aralarında
  bir e-posta adresi, bir argon2id parola özeti, bir müşteri kimliği, cihaz başına
  bir kimlik bilgisi ve dört zaman damgası tutuyorlardı. Yani bir mağaza silme
  talebini yerine getirip, silinen kişiyi içeri alan kimlik bilgilerini yerinde
  bırakabiliyordu. Bunu yakalamak için yazılmış denetim de onları göremiyordu:
  yalnızca `plugins/` altını geziyordu ve ayrı bir go.mod bir tabloyu daha az
  kişisel yapmıyor — kökler artık DİSKE karşı doğrulanıyor. Passkey silmesi
  bilinçli olarak RP kapsamı DIŞINDA: ötekiler "hangi anahtarlar bu kişiyi içeri
  alır" sorusunu yanıtlıyor, bu ise "onun hakkında ne tutuluyor". Parola özeti
  beyan ediliyor ama değeri üretilmiyor — sütunu düşürmek cevabı yanlış yapardı,
  değerini basmak kişinin kendi sırrını dosyaya koyardı (D69).

- **Bir passkey artık TEK bir doğrulayan tarafa ait** (ADR 0131). Passkey'i
  üreten doğrulayıcı onu bir RP kimliğine bağlar: `Options.RPID` değişen bir
  kurulum — alan adı taşınması, ya da bir alt alan adının düşürülmesi — kayıtlı
  her anahtarı kullanılamaz bırakıyor. Satırlar kalıyordu ve hangi tarafa ait
  oldukları hiçbir yerde yazmıyordu, yani bir commit önce gönderilen "son giriş
  yolunu koruma" kuralı onları SAYIYORDU: bir terk edilmiş ve bir yeni anahtar
  tutan kişiye "iki yolunuz var" deniyor, YENİ olanı kaldırmaya izin veriliyordu
  — koruma, önlemek için yazıldığı kilitlenmeyi üretiyordu. Sunucunun da bir
  görüşü yoktu ve bu yarısı ölçüldü: `example.test` altında kaydedilmiş satır,
  `moved.test` altında birini 204 ile içeri aldı. Artık `rp_id` bir sütun ve
  deponun her okuması/yazması onunla kapsamlı; NULL, sütundan önceki satır
  demek ve yapılandırılmış taraf olarak okunuyor — yani yükseltme kimseyi
  dışarı atmıyor (D68).

- **Bir kişi artık passkey'lerini GÖREBİLİYOR ve birini kaldırabiliyor**
  (ADR 0130). `contrib/identity-passkey` yalnızca kayıt ve giriş sunuyordu:
  telefonunu kaybeden biri hesabını neyin açtığını göremiyor, o cihazı iptal
  edemiyordu. İki uç eklendi — çağıranın kendi anahtarlarının listesi ve birini
  kaldırma. Kaldırma, hesabı girişsiz bırakacaksa reddediliyor ve bu kural bir
  KOŞUL değil bir KİLİT: READ COMMITTED altında DELETE'in içine yazılan aynı
  kontrol, eşzamanlı iki kaldırmada sıfır anahtar bırakıyor — ölçüldü, her
  koşuda. "Başka bir giriş yolu var mı" sorusu bu modülün KENDİ sorusu, ve
  işlem açılmadan önce soruluyor: satır kilidi tutarken başka bir modülü
  sorgulamak aynı havuzdan ikinci bir bağlantı ister. "Bakamadık" asla "başka
  yolunuz yok" değil — biri 500, diğeri 409.

- **İmzalama anahtarı artık kimseyi dışarı atmadan DÖNDÜRÜLEBİLİYOR** (ADR 0129).
  `contrib/identity-session` tek bir anahtarla imzalıyor ve doğruluyordu; onu
  değiştirmek, her tarayıcıdaki her çerezin aynı anda doğrulanmaz olması demekti.
  Yani bir döndürmenin bedeli her alışverişçinin oturumuydu — ki anahtarların
  neden döndürülmediğinin sebebi bu, döndürülmemesi gerektiğinin değil. Modülün
  kendi paket belgesi bunu bir cümleyle söylüyordu; yazılı bir sınır,
  kapatılabilen bir sınırdır. `identitysession.Options.RetiredSecrets` çerezin hâlâ taşıyabileceği
  ama hiçbir şeyin İMZALAMADIĞI anahtarları tutuyor. Sıra tam olarak özelliğin
  kendisi: ikisini de kabul edip ESKİSİYLE imzalamaya devam eden bir gerçekleme,
  "oturumlar çalışıyor" diyen her testi geçer ve hiçbir şey döndürmemiş olur.
  SIZAN bir anahtar emekliye ayrılmaz, doğrudan atılır — bu herkesi dışarı atar ve
  doğru bedel odur.

- **Passkey'ler KENDİ modülünde** (ADR 0128). `contrib/identity-passkey` her iki
  WebAuthn törenini de yapıyor, kendi kimlik bilgisi tablosunu tutuyor ve kişiyi
  parolanın açtığı AYNI oturum çerezine sokuyor. Ayrı bir `go.mod`, çünkü ölçüldü:
  go-webauthn'ı import etmek gobit'in grafiğinde OLMAYAN dokuz modül ekliyor —
  `go-tpm` ve `go-tpm-tools` dahil, yani çoğu dükkânın hiç görmeyeceği donanımın
  attestation desteği. `contrib/identity-session`'ı parola için import eden bir
  kurulum bunu taşımamalı. Tören durumu, oturum modülünün anahtarıyla mühürlenmiş
  kısa ömürlü bir çerez; bu, o modülün MAC'ine "bu imza NE İÇİN" bilgisini
  eklettirdi — tek anahtarın iki şekli imzalaması onları birbirinin yerine
  geçirilebilir yapar. Giriş kimseyi ADLANDIRMIYOR: doğrulayıcı kişiye hangi
  anahtarını kullanacağını soruyor, ki bu hem daha iyi akış hem de hesap sayımı
  OLMAYAN tek akış. Törenler gerçek bir yazılım doğrulayıcısıyla KOŞULUYOR, çünkü
  bu modülün yapabileceği her hata bir challenge, bir origin ya da bir kullanıcı
  tutamağı hakkında ve handler'a dair hiçbir iddia bunların hiçbirini görmez.

- **Çalışan bir müşteri kimliği artık AĞAÇTA — ama modülün DIŞINDA** (ADR 0127).
  `contrib/identity-session`: imzalı çerez oturumu, argon2id parolalar, kendi
  tablosu ve iki vitrin ucu; gömen import edip `Add` ediyor. ADR 0125 müşteri
  adlandıran her vitrin ucunu bir doğrulayıcı bağlanana kadar kapattı, ADR 0126
  kuralları yayımladı — ama bağlanacak bir şey yoktu: buradaki her gerçekleme bir
  test sahtesi. Yeri KARARDI: `plugins/*` ana modülde, yani oraya girecek bir
  WebAuthn kütüphanesi ürün kataloğu isteyen bir dükkânın grafiğine, güvenlik
  taramasına ve hukuk incelemesine düşer — bağımlılık kapısının kendi cümlesi. Ayrı
  bir `go.mod` onu dışarıda tutuyor. İlk dilim kimseye hiçbir bağımlılık eklemiyor:
  argon2id `golang.org/x/crypto` istiyor ve gobit onu zaten DOĞRUDAN require
  ediyor. Passkey kendi kaydına kaldı. Dört kapı ve iki şerit yeni ağacı öğrendi ve
  her biri bir şey buldu — en keskini, hiçbir şeyin koşmadığı yirmi sekiz test.

- **Bir müşteri kimliği artık YAYIMLANMIŞ bir süitten geçiyor** (ADR 0126).
  `corehttp.Identity` bu çerçevenin istediği ve gerçeklemediği tek arayüz, ve
  gömenin ne yazdığını hiçbir şey denetlemiyordu — `docs/known-limits.md` bunu iki
  kayıttır bir cümleyle söylüyordu: iddia edilen kimliği geri veren bir gerçekleme
  arayüzü karşılar ve çerçeve bunu ayırt edemez. ADR 0125 bunu canlı bir soruya
  çevirdi: müşteri adlandıran her vitrin ucu artık bir doğrulayıcı bağlanana kadar
  reddediyor. Bariz kural ise İŞE YARAMIYOR — arayüzün kendi sözleşmesi "yukarı
  akıştaki bir vekilin yazdığı başlık"ı meşru bir kaynak sayıyor ve haklı: süzen
  bir geçidin arkasında o başlık kanıttır. İkisini ayıran şey başlık değil, onu
  süzen bir şey olup olmadığı — ve isteği tutan hiçbir test geçidi göremez. Çözüm:
  gerçekleme bunu BEYAN ediyor (`identitytest.UpstreamTrust`) ve süit o başlığı
  sondalamıyor. Süitin kendi testlerindeki iki gerçekleme aynı kod: beyan eden
  geçiyor, etmeyen ise implemente edilecek arayüzü ADIYLA söyleyen bir hatayla
  düşüyor.

- **Doğrulanmamış bir müşteri iddiasını sunmak artık bir SEÇİM** (ADR 0125).
  ADR 0057 müşteri adlandıran on iki vitrin ucunu tek bir karşılaştırmaya bağladı
  ve dördünün, hiçbir doğrulayıcı bağlı değilken iddiayı DENETLENMEDEN sunmasını
  bilerek seçti — gerekçesi yanlış da değildi: reddetmek, hiçbir yanlış yapmamış
  bir gömenden çalışan bir yüzeyi geri çeker. Kalıntı açıkça yazıldı ve
  `docs/known-limits.md`'ye kondu: bir müşteri kimliğini bilen (o kimlik her
  sipariş yanıtında geziyor) biri, o kişinin şirketini ve harcama sınırını okuyor
  ve onun adına sepet açıyordu — sepet yarısı o kişinin B2B ödeneğini harcıyor.
  O kaydın yapamadığı şey bunu bir KARAR hâline getirmekti: bir kurulum, açık
  cevabı sorunun var olduğunu bilmeyerek alıyordu ve açılışta bir WARN bir seçim
  değildir. Dördü artık varsayılan olarak reddediyor — adres defterinin zaten
  yaptığı gibi — ve eski cevap tek bir ayar uzakta
  (`STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM`). Geri çekilen şey yüzey değil,
  onu KARAR VERMEDEN almak. Varsayılan aynı zamanda SIFIR DEĞER: alan olumlu
  adlandırıldı, çünkü `internal/e2e` bileşim kökünü sıfır Options ile taklit
  ediyor ve modülleri elle kuran her gömen de öyle. Misafir trafiği iki değerde de
  aynı — kimseyi adlandırmayan bir gövde hiç sorgulanmıyor, ki ADR 0057'nin bütün
  karşılaştırmayı üzerine kurduğu cümle bu.

- **Sevkiyat artık paranın HÂLÂ ORADA olup olmadığını SORUYOR** (ADR 0124).
  ADR 0120 değişimin farkını alabilmesini sağladı ve satır, paranın orada olduğu
  ANI tutuyor — bir sipariş satırının payment'ın sahip olduğu bir rakam hakkında
  tutabileceği tek şey o (ADR 0119). Ama bir AN, bir BAKİYE değil: koleksiyon
  payment'ın kendi iade rotasından erişilebilir kalıyor ve o yolda hiçbir akış
  yok. Ölçüldü: fonla, koleksiyonu iade et, sevk et — 200 döndü, gerçek bir koli
  açıldı, birimler raftan indi ve değişim `completed` işaretlendi; koleksiyonda
  hiçbir şey yokken (D61). Kusur eksik bir kural değil, DEĞİŞEN bir şey hakkında
  BİR KEZ sorulmuş bir kural. Akış artık stok hareket etmeden önce soruyor —
  reddin hâlâ bedelsiz olduğu yerde — ve kaydı kapatmadan önce bir daha, çünkü o
  adım yeniden deneme yolunda da koşuyor.

- **Canlı kodda anılan bir şema adı ARTIK ÇÖZÜLÜYOR** (ADR 0123). D59'un on
  bayat cümlesinden dördü, ADR 0120'nin düşürdüğü bir CHECK'i adlandırıyordu:
  okuyucuya iddiayı doğrulayacağı bir ad veriyor, ad ise hiçbir şeye çözülüyordu.
  Bu, `doc_references_test.go`'nun yazıldığı sınıf — atıf okuyucuyu ARAMAYA
  gönderir ve aranan şey yoktur — ama onun ulaşamadığı boyutta, çünkü bir kısıt
  adı Go sembolü de yol da değil, bir yorumdaki kelime. Denetlenen dağarcık
  göçlerin KENDİSİNDEN türetiliyor: şemanın hiç tanımladığı her ad. Göçler SIRAYLA
  yürünüyor ve sıra işin kendisi — bir ad rutin olarak düşürülüp bir satır sonra
  geri ekleniyor, bir CHECK böyle genişletiliyor.

- **Üretilen kod artık YENİDEN ÜRETİLEREK doğrulanıyor** (ADR 0122). Depo 75
  sqlc dosyasını ve 14 gqlgen dosyasını ağaçta tutuyor ve hiçbir şey onları
  kaynaklarıyla karşılaştırmıyordu. Kusur bir yorumla ortaya çıktı (D60), ama
  ölçüm daha kötüsünü buldu: kaynak `.sql` veritabanının koştuğu sorgu DEĞİL —
  koşulan şey üretilen dosyadaki dizge — yani yalnızca kaynağı düzenlemek
  hiçbir şey tarafından çalıştırılmıyor. B2B harcama penceresini kaynakta bin
  katına çıkarmak `go build`'i, `go vet`'i, birim şeridini ve gerçek
  PostgreSQL'e karşı entegrasyon şeridini temiz bırakıyor; hepsi ESKİ sorguyu
  koşuyor, ki doğru olan o. Düzenleme tam da yanlışken görünmez. CI artık
  üreteçleri koşup farkı soruyor — go.mod için zaten sorduğu soru.

- **Ödeme modülü artık paranın ne zaman hareket ettiğini SÖYLÜYOR** (ADR 0121).
  Siparişin özeti, payment'ın tuttuğunun bir RAPORU, ve onu yalnızca iki akış
  yazıyordu. Payment ise tahsilat ve iade rotalarını kendi yayımlıyor, o yollarda
  hiçbir akış yok — yani para hareket ediyor ve siparişin kaydı hiç öğrenmiyordu.
  ADR 0022 bunu üç gün önce görmüş, aboneyi "daha iyi ev" diye adlandırmış ve tek
  bir sebeple reddetmişti: payment hiçbir şey yayımlamıyordu. Aynı cümlede de
  önce cevaplanması gereken soruyu bırakmıştı — **bir ödeme olayı ne taşır?**
  Cevap: koleksiyonun KİMLİĞİNİ ve anı, tutarı DEĞİL. Üç sebebi var ve üçü de
  ölçüldü. İade bilerek idempotent değil, yani yükteki bir tutar ARTIM olurdu ve
  otobüs en az bir kez teslim ediyor — tekrar teslim edilen bir artım, hiç
  olmamış bir toplam bildirir. Tüketicinin yazması ise yalnızca KÜMÜLATİF sayıda
  doğru çalışan bir birleştirme. Ve yayımlanan her konu kurulum dışına
  iletiliyor: yükteki para, operatörün kaydettiği üçüncü taraf uçlarına giderdi.
  Abone siparişe `order_payment` bağı üzerinden TERS yönde ulaşıyor, çünkü
  koleksiyonun `reference`'ı SEPET kimliği taşıyor — yayımlanan OpenAPI tarifi
  bunu "an order id in practice" diye yanlış anlatıyordu, o da düzeltildi.
  ADR 0119 bükülmüyor: yasak olan bir order satırının payment'ın rakamını KENDİ
  GERÇEĞİ gibi tutması, rapor tutması değil — ve abone raporu YAZDIĞI AN sorarak
  üretiyor. Kusurlar: D55 kapandı, ve kardeşi D57 açılıp aynı commit'te kapandı —
  tahsilat rotası da aynı sessizliği taşıyordu ve D55 yalnızca bakılan rotadan
  yazıldığı için kaçmıştı.

- **Bir değişim artık farkını ALABİLİYOR** (ADR 0120). Göç 000008 değişimin
  tamamlanmasını kaldırırken geri getirecek şeyi adıyla yazmıştı: mal çıkışı, ve
  fark sıfır değilse para girişi. Malı ADR 0090 getirdi; para üç kayıt sürdü —
  0117 satışın bağını yerinde tuttu, 0118 koleksiyonun kapısını kalan kapasiteye
  çevirdi, 0119 order satırının payment'ın tutarını AYNALAMAYACAĞINA karar verdi.
  Geriye tek soru kalmıştı: geri çekme muhafızı nereye konur. Ölçüldü ve geri
  çekmeyi koruyan BEŞ şeklin hiçbiri ayakta kalmadı — parayı orada okumak bu
  modülün soramayacağı bir modülü ister, sayacı okumak ise geri alınamayan bir
  kayıt ve bir daha unutulamayan bir sipariş üretir.
  Cevap soruyu taşıdı: **muhafız geri çekmede değil BAĞLAMADA duruyor.** Parayı
  aldığı an değişim `requested`'dan çıkıyor ve geçiş tablosu bunun ne demek
  olduğunu kendisi söylüyor — iadenin `received → conflict` satırının aynısı.
  Karar: pozitif fark, operatörün parayı topladığı koleksiyonu ADLANDIRARAK
  fonlanıyor; satır o koleksiyonun KİMLİĞİNİ ve ANI tutuyor, tutarını asla.
  Fonlanmış bir değişimin malı onu tamamlayabiliyor, olağan geri çekme onu
  reddediyor, ve çıkışı parayı geri gönderip isteği de geri alan TEK bir eylem.
  Bedeller açık: statü sözlüğü üçten dörde çıktı ve yayımlandı. Tamamlanmanın
  sınırı şemada KALDI, ve bu ancak satır bir KİMLİK tuttuğu için mümkün — bir
  CHECK kolonu görür, link katmanına yazılmış bir bağı görmez. Bu, ADR 0117'nin
  ikinci cümlesini geçersiz kılıyor: o kayıt link demişti çünkü görünen şekil
  oydu; ağacın kendi şekli, iki tarafı birden tutan akışın yazdığı çapraz-modül
  kimliği için bir KOLON (göç 000012 bunu gerekçesiyle yazmış).
  Ve yeni statünün açacağı deliği kapattım: silme süpürgesi `requested` arıyordu,
  `funded` onu tetiklemezdi — yani parası tutulan bir sipariş UNUTULABİLİR hâle
  gelirdi. Dal ikisini birden okuyor.

- **Bir sipariş paranın ikinci bir kopyasını TUTMUYOR** (ADR 0119). Değişimin
  farkı üç kez tasarlandı, altı aday üretildi ve altısı da bağımsız okumalarla
  yıkıldı. Neredeyse her şeyde ayrışıyorlardı ve tek bir şeyden öldüler: her biri
  değişimin satırına bir PARA sayısı koyup kuralı satır-yerel bir CHECK'e
  bağlıyordu — göç 000017'nin tamamlanmayı sınırlarken savunduğu şekil.
  Başka bir modülün sahibi olduğu para için o gerekçe taşımıyor. Ölçüldü: payment
  bir tahsilatı iade eden bir rota YAYIMLIYOR, hiç olay yayımlamıyor (sıfır
  `Publish`, sıfır konu, sıfır abone), ve `order_exchanges` üzerindeki bir kısıt
  `payment_collections`'a yazılan bir satırı GÖREMEZ. Yani order tarafındaki bir
  kopya, yayımlanmış bir rotanın sessizce geçersiz kılabildiği bir iddia, ve
  şemanın sunduğu en güçlü muhafız tam da onu fark edemeyen muhafız.
  Karar: payment'ın sahip olduğu bir tutar order satırına AYNALANMIYOR; o tutar
  bir şeye karar veriyorsa, karar anında payment'a SORULUYOR. Order satırının
  kaydedebileceği şey bir SORUNUN CEVAPLANDIĞI AN'dır, cevabın aritmetiği değil.
  Bedeli açıkça ödeniyor: 000017'nin satır-yerel sınırı bu olgu için bırakılıyor
  (o sınırın gerekçesi olgunun satırda olmasıydı, bu olgu satırda değil), ve ölçüm
  ile yazma arasında modüller arası işlem olmadığı için kapanmayan bir pencere
  kalıyor — bir tamamlama, yazıldığı ANI beyan eder, sonraki her anı değil.
  Ağaçta zaten böyle bir kopya var ve artık adı konuldu: `order_summaries`'in
  toplamları bir RAPOR, kaynak değil. O yüzeyin godoc'u kendi merge semantiğini
  "payment olaylarını dinleyen bir abone" ile gerekçelendiriyordu ve öyle bir
  abone VAR OLAMAZ; cümle düzeltildi, bayatlık düzeltilmedi — tetiği ADR 0022'nin
  kendi tetiği. Kusur D55.
  Üç turun kapattıkları bir sonraki kayda devrediliyor (bağ adı ve bire-bir
  kardinalitesi, yalnızca-pozitif yüklem, tabanın değil EŞİTLİĞİN ölçü olması,
  yapısal tavan, yeniden yazan `down`, imza genişletmek yerine metot eklemek), ve
  bilerek açık bırakılan tek soru da: geri çekme muhafızı.

- **Bir koleksiyonun kapısı artık KALANI okuyor** (ADR 0118). Kapı
  "bu koleksiyon hiç bir şey aldı mı" diye soruyordu, ve altındaki hesap tahsil
  edileni hiç okumuyordu — yalnızca canlı oturumların rezervini düşüyordu. Yani
  bayrak bir kısayol değildi, ikinci bir oturumla ikinci bir tahsilat arasındaki
  TEK duvardı; kaba olmasının sebebi buydu, ve kısmi bir tahsilatın kalanını da
  o kabalıkla sonsuza kadar toplanamaz yapıyordu.
  Kısmi tahsilat bir köşe durumu değil: admin tahsilat ucu tutarı İSTEĞE BAĞLI
  alıyor ve yalnızca yukarıdan sınırlıyor, bir sağlayıcı kısmen yetkilendirince
  operatör hiçbir şey seçmeden aynı yere geliniyor, ve türetilmiş durum sözlüğü
  o hâli yazıldığı günden beri adıyla tanıyor. Modül kendi sözünü de çiğniyordu:
  oturum testi "iptal edilen bir oturum koleksiyonu sonsuza kadar kilitlememeli,
  müşteri yeni bir ödeme yolu deneyebilmeli" diyor, ve bu yalnızca hiçbir şey
  tahsil edilmemişken tutuyordu.
  Karar: oturum açmak KALANI soruyor — tutar eksi tahsil edilen eksi canlı
  oturumların rezervi — ve yalnızca bu sıfırken reddediyor. Çift tahsilatın
  bariyeri bir bayraktan aritmetiğe taşınıyor, üstelik ikinci duvarın zaten
  durduğu yere: `captured_amount <= amount` kısıtı bugüne kadar ancak sağlayıcı
  parayı ÇEKTİKTEN sonra ateşlenebiliyordu.
  Tam iade edilmiş bir koleksiyon YENİDEN AÇILMIYOR ve bu bilinçli: iade tahsil
  edileni küçültmüyor, kalan kapasite sıfır kalıyor. ADR 0117'nin tetiği bu ama
  tüketicisi yok — üretimdeki iki iade çağıranı da parayı yalnızca geri
  gönderiyor (ADR 0063). Yan taraftan bir düzeltme: ADR 0117 "engel bağ değil
  koleksiyon" diyordu; ölçüm daha dar bir cevap verdi — var olan koleksiyon
  yeniden kullanılabilir olsa bile farkı ALAMAZ, çünkü `amount` bir daha
  yazılmıyor. Fark İKİNCİ bir koleksiyon ister, ve onun adı hâlâ ertelenmiş
  durumda. Kusurlar D53 ve D54.

- **Satışın ödeme bağı yalnızca satışı taşıyor** (ADR 0117). ADR 0116 bir
  kardinaliteyi genişletilebilir yaptı ve tek soruyu yazılı olarak açık bıraktı:
  değişimin tahsilatı kendi adını ister mi. O güne kadar cevap zorunluydu —
  `order_payment`'ı genişletmek açılışı durduruyordu — ve artık durdurmuyor.
  Ölçüldü: reddeden şey mekanizma değil SATIR. Bir link satırı iki kimlik ve bir
  an taşıyor, o anı hiçbir okuma ifadesi SELECT etmiyor, ve genişletilmiş bir
  `order_payment` altında checkout'un açtığı tahsilat ile bir değişimi
  karşılayan tahsilatı ayırt edecek veri kalmıyor — üç okuyucunun üçü de eline
  geçen ilkini alıyor. En pahalısı iade akışı: yorumu bir tarif değil GEREKÇE,
  "bire-bir, o yüzden birden fazlası bir seçim değil veri hatasıdır" diyor, ve
  genişletme davranışı aynı bırakıp gerekçeyi yalanlardı.
  Karar: `order_payment` bire-bir kalıyor; bir siparişe karşı başka bir sebeple
  toplanan para, satırın taşıyamadığı ayrımı ADIYLA taşıyan kendi bağına
  bağlanacak. O bağın adı ve uçları burada KARARLAŞTIRILMIYOR — tüketicisi yok,
  ve bu depo okunmayan bir adı yayımlamıyor.
  Aynı turda ADR 0114'ün geride bıraktığı okuma yüzeyleri düzeltildi (D52):
  admin değişim kaydı, durumunun zaten yayımladığı ANI kazandı, ve çizelge bir
  değişimin iki bitişini birden bildiriyor. İkisi de mutasyonla kanıtlandı.
  Farkı olan bir değişim hâlâ kapanmıyor, ve ölçülen engel bağ değil koleksiyon:
  bir tahsilat koleksiyonu bir şey aldıktan sonra terk edilemiyor. Tetik yazılı.

- **Bir bağın kardinalitesi artık GENİŞLEYEBİLİYOR** (`core/link`, ADR 0116).
  Ağaçta üç yer aynı adımı adlandırıyordu — ADR 0114'ün açık bıraktığı sınır,
  payment modülünün bağ tanımı ("o gün bu OneToMany olur ve **başka hiçbir şey
  değişmez**") ve yetenek listesi. O cümle yanlıştı ve en sert biçimde yanlıştı:
  `core/link` her tanımı kalıcı bir deftere yazıp gelen tanımla EŞİTLİK üzerinden
  karşılaştırıyor, yani değişmiş bir kardinalite açılışta çakışmadır — uygulama
  hiç başlamazdı. Şema yarısı da aynı şekildeydi: DDL baştan sona `IF NOT
  EXISTS` olduğu için gevşek bir bildirim hiçbir şey yaratmıyor ve hiçbir şeyi
  KALDIRMIYORDU; eski kardinalite altında kurulan tekil indeks hayatta kalıp
  eskisini dayatmaya devam ederdi.
  Karar: iki ucu değişmemiş ve kardinalitesi depodakinden GENİŞ olan bir bildirim
  uygulanıyor — defter satırı taşınıyor ve yeni kardinalitenin istemediği
  indeksler, bildirimin zaten tuttuğu kilidin altında, aynı işlemde düşürülüyor.
  Güvenlik gerekçesi tek cümle: dar bir kardinalitenin kabul ettiği her çift
  geniş olanınca da kabul edilir, yani diskteki satırlar yeni kısıtı eskisini
  sağlamış olmakla sağlar — genişleme güvenli olduğunu bilmek için hiçbir veri
  okumaz. DARALTMA okumak zorundadır ve reddedilmeye devam ediyor.
  Bedelleri: bir bağı genişleten sürüm bu yoldan GERİ ALINAMAZ (eski ikili dar
  kardinaliteyi bildirir ve açılışta reddedilir), ve ret mesajı artık izin
  verilen yönü söylüyor çünkü oraya çarpan okur çoğunlukla geri alıyordur.
  `verifySchema` sorusunun öteki yarısını kazandı: gereken indeksler var mı diye
  soruyordu, artık gerekmeyenler GİTTİ Mİ diye de soruyor. Ve `OneToOne`'ı geçen
  bir genişleme bir EŞZAMANLILIK güvencesi HARCIYOR: `from_uniq`, aynı sol taraf
  kaydına iki hedef bağlanmasının tek yapısal engeli — akışlar bağı okuyup sonra
  yazıyor ve advisory kilit yalnızca `Define`'ın etrafında. Bedel `OneToMany`'nin
  kendi anlamı, ama onu erişilebilir yapan kayıt bu.
  `order_payment` burada genişletilMİYOR: okuyucuları tek tahsilat varsayıyor ve
  bunu yazıyor (iade akışı ikincisini "seçim değil veri hatası" sayıp ilkini
  alır), ikisi arasında seçim kuralı ise ikincisinin bir anlamı olmadan
  yazılamaz. ADR 0089/0090'ın yaptığı ayrımın aynısı.

- **Mağaza artık kim olduğunu SÖYLÜYOR** (yeni `settings` modülü, ADR 0115).
  Faturalama akışı iki tarafı da ÇAĞIRANINDAN alıyordu ve satıcının neden orada
  olduğunu kendi godoc'u yazmıştı: "satıcının yasal bilgileri mağazanın kendi
  yapılandırmasıdır ve burada hiçbir modülde yaşamıyor." Bunun iki sonucu vardı:
  aynı mağazadan kesilen iki belge iki farklı satıcı adlandırabiliyordu, ve her
  faturaya basılan kimlik, operatörün düzenleyemediği tek şeydi — fiyatı, ürünü,
  siparişi değiştirebilen kişi mağazanın vergi dairesini düzeltmek için yeniden
  dağıtım istemek zorundaydı.
  Karar: `settings` modülü TEK bir `store_profile` tutuyor — yasal ad, vergi
  numarası, vergi dairesi, e-posta, adres, ülke — ve faturalama akışı satıcıyı
  ondan okuyor. Satıcı artık hiçbir isteğin parçası değil.
  Bedelleri: profil yazılmadan belge kesilmesi REDDEDİLİYOR ve mesaj ucun adını
  söylüyor (ilk faturasını kesen operatör tam da profili doldurmamış kişidir).
  Yönetim gövdesi `seller` alanını KAYBEDİYOR — yayımlanmış bir yüzeyi
  daraltmak burada yan etki değil kararın kendisi: çağıranın verebildiği bir
  alan, iki çağıranın farklı verebildiği bir alandır. PATCH değil PUT, çünkü
  kayıt bir KİMLİK ve kısmi yazma bir düzenlemeden gelen yasal adla başka bir
  düzenlemeden gelen vergi numarasını yan yana bırakırdı. Kurulum başına TEK
  profil (ADR 0009 çok kiracılılığı kurulum sınırına koyar).
  Modül kolonlarını KİŞİSEL VERİ olarak beyan ediyor ama silici GERÇEKLEMİYOR:
  şahıs şirketi bir kişidir, ama silinmeyi isteyen özne MÜŞTERİDİR ve
  denetleyicinin kendi kimliğini silmek, kesilmiş belgeler onu basmaya devam
  ederken mağazayı kendi kurulumundan silmek olurdu. E-posta HİÇBİR YERDE
  katlanmıyor ve muafiyet bedelini yazıyor: bu adres basılıyor, eşleştirilmiyor.
- **Bir değişim artık malını gönderebiliyor** (`order_replacements` ikinci bir
  kaynak tanıyor, ADR 0114). Değişim, yalnızca geri çekilebilen bir istekti. Göç
  000008 onun "tamamlandı" durumunu kaldırırken geri getirecek koşulu adıyla
  yazmıştı: mal ÇIKMALI ve fark sıfır değilse para HAREKET ETMELİ, "ve çerçevede
  ikisi de yok". İkisinden biri geldi — ADR 0090 mal çıkış akışını inşa etti ve
  `order_replacements` onun gönderdiği kayıt. Ama o kayıt yalnızca bir TALEPTEN
  beslenebiliyordu, çünkü yazıldığı gün mal isteyebilen tek şey bir talepti; oysa
  değişim zaten "gelen mala karşılık giden mal" demek.
  Karar: bir gönderim kaydı TEK bir kaynak adlandırır — talep ya da değişim — ve
  gönderildiğinde o kaynağı kapatır. Değişim yalnızca `difference_due` sıfırsa
  kapanır, ve bu sınırı veritabanı tutar (`order_exchanges_completed_owes_nothing`).
  Farkı olan bir değişim, malı çıktıktan SONRA da açık kalır. Bu bir boşluk değil
  dürüst hâl: malın yarısı gönderim kaydında duruyor, paranın yarısı ise bu
  çerçevenin göremediği bir yerde oldu. Gerisinin tetiği payment modülünün kendi
  link tanımında zaten yazılı — sipariş↔ödeme bağının bire-çok olduğu gün.
  Modüller arası tel `claim_id`/`claim_status` yerine
  `source_kind`/`source_id`/`source_status` taşıyor: biri hep boş iki çift, her
  okuyucuya "hangisi doluydu" sorusunu sordururdu. İki uç birbirini import
  edemiyor (ADR 0006), yani derleyici bu dikişi görmüyor; kanıt entegrasyon
  şeridinde.
  Değişimin artık İKİ geçişi var, yani `CancelExchange`'in godoc'unun "tek geçiş
  için yazmaya değmez" dediği ortak çerçeve yazıldı: iki geçişte, "ikinci çağrı
  İLK anı korur" kuralı yoksa iki yere yazılırdı.
- **Bir satır artık KISMEN iptal edilebiliyor** (`order_line_cancellations`,
  ADR 0113). İptal ya hep ya hiçti: `CancelOrder` siparişin tamamını alır ve
  tahsilatı olan bir siparişi reddeder — ki olduğu şey için doğrudur, o
  checkout sagasının telafisidir ve hiçbir şey sevk edilmemiş, hiçbir şey
  çekilmemişken koşar. İfade edemediği şey sıradan olandı: canlı bir siparişin
  bir satırı stoktan düşer, depoda hasarlanır ya da müşteri onu bırakırken
  siparişin gerisi sevk olur. Modülde bunu söyleyecek hiçbir şey yoktu, ve
  kaydetmenin iki yolu da yanlıştı — ya tüm siparişi iptal et, ya da sepetin
  anlık görüntüsü olan satırın adedini düzenle.
  Karar: kayıt satırın KAÇ biriminin teslim edilmeyeceğini, sebebiyle birlikte
  tutuyor; tavan — alınan eksi iade istenen eksi zaten iptal edilen — SİPARİŞİN
  KİLİDİ altında denetleniyor. İade yolu aynı toplamı okuyor, yani bir birim
  hangi eylem konuştuysa BİR KEZ konuşulmuş oluyor: üç birimlik bir satır iki
  kez iade istenip bir kez iptal edilemiyor.
  Bedelleri adıyla yazılı. Siparişin TOPLAMI da satırın ADEDİ de kımıldamıyor
  (ikisi de anlık görüntü, ve toplam satırlara CHECK ile çivili). DURUM da
  kımıldamıyor: son satırı da iptal edilmiş bir sipariş hâlâ birinin kapatması
  gereken bir sipariştir. PARA ikinci bir eylem — ödenmiş ama gelmeyecek bir
  birim iade ya da kredidir (ADR 0105) ve hangisi olduğu bu modülün tutmadığı
  bir politikaya bağlı; ödemeden önceki bir iptal ise hiçbir şey borçlu değil.
  STOK geri konmuyor, ve adlandırılmaya değer sınır bu: order modülü
  inventory'ye uzanamaz (ADR 0006), yani rezervasyonu bırakmak üstteki bir akışın
  işidir ve henüz isteyen bir akış yok — tetik, isteyen ilk akıştır.
  Eşzamanlılık iddiası gerçek Postgres üzerinde kanıtlı: kilit kaldırıldığında
  on altı çağıranın on altısı da kazanıyor ve üç birimlik satıra on altı birim
  yazılıyor. İlk yazdığım test bunu YAKALAMIYORDU — goroutine'leri yalnızca
  başta buluşturmak yetmiyor; toplamın okunduğu yere yapısal bir gecikme
  konunca mutasyon kesin biçimde kırmızıya döndü.
- **Bir alım artık bir birim kazandırıyor** ("al X, kazan Y" mekaniği,
  ADR 0112). `buyget` bir enum'da kelimeydi ve başka bir şey değildi: tür
  yazılabiliyor, promosyon yayına alınamıyor, hesap da onu atlıyordu — hiç
  inşa edilmemiş bir mekaniğin yerinde duran üç ret. Eksik olan üç şeydi ve
  üçü ayrı cinstendi: kurallar `context` ile `target`tı, yani hangi satırın
  ALINDIĞINI hangisinin ÖDÜLLENDİRİLDİĞİNDEN ayıran bir şey yoktu; uygulama
  yöntemi tutar ölçüyordu, adet değil, yani ödülün kaç birime ineceğini
  söyleyen bir alan yoktu; ve hesap girdisi satır tutarını taşıyordu, birim
  fiyatı değil — ödül ise birim başına fiyatlanır ve türetme tek bir bölmedir,
  bölme de yuvarlar.
  Karar: `buy` kuralların seçtiği birimler `buy_quantity`'ye karşı sayılır,
  sonra hedef kuralların seçtiği ve alımın TÜKETMEDİĞİ birimlerin EN UCUZ
  `apply_to_quantity` tanesi indirilir. Alınan bir birim aynı zamanda
  ödüllendirilen birim değildir, yani "al 2, birini kazan" sepette ÜÇ birim
  ister; öteki okuma, aynı sözle müşteriye iki tanesini bir fiyatına verirdi.
  Alımı EN PAHALI birimler karşılar, ödül en ucuza iner — süpermarketin kendi
  kuralı ve tacir açısından güvenli yön. Ödül hesap başına BİR KEZ verilir;
  tekrarlayan merdiven ("her üçüncüsü bedava") ayrı bir sözdür ve tetiği bir
  tacirin onu yazmasıdır.
  Bedeli iki yerde ödendi. Birim fiyat artık indirim isteğinin ZORUNLU alanıdır
  ve kimliği (birim × adet = tutar) zorlanır — iki çağıran da (sepet akışı ve
  yönetim hesabı ucu) gönderir, çünkü isteğe bağlı bir alan mekaniği bir
  çağıranda çalıştırıp diğerinde sessizce çalıştırmazdı. Ve mekanik ile yöntem
  UYUŞMAK zorundadır: sayı çifti olmayan bir buyget de, çifti taşıyan bir
  standart promosyon da `reward_mismatch` ile elenir ve operatöre söylenir
  (ADR 0110). Uygulamamak güvenli yöndür; eşleşmenin kendisi bir CHECK'tir,
  yani elle yazılan bir satır da yarım kalamaz. `allocation` ile `max_quantity`
  bu yolda okunmaz, ve `not_standard` eleme kelimesi kalktı — motor artık iki
  mekaniği de uyguluyor.
  Vitrinin kupon sorgusu da düzeldi: buyget kuponu artık MEKANİĞİ ve iki sayısı
  ile dönüyor, ve hesabın eleyeceği bir kuponu müşteriye sunmuyor. İkisi de aynı
  yüklemi kullanıyor; ayrı yazılsalardı müşteri kodu yazar, hiçbir şey olmaz ve
  hiçbir yerde bir sebep durmazdı. Mekaniğin gövdede olması şart: "al 2, birini
  kazan" kuponu on bin baz puan taşır ve mekanik söylenmeseydi vitrin onu
  "%100 indirim" diye gösterirdi.
- **Sepetin KENDI verisi artik bir promosyon kuralini yonetebiliyor**
  (`cart.` onekiyle bağlam, ADR 0111). Indirim motorunun kural baglamı sepet
  akisinin karar verdigi IKI addan kuruluyordu -- bolge ve musteri grubu -- ve
  ucuncusunu ekleyen bir sey yoktu: `internal/app.Options` yalnizca
  `Modules`/`Plugins` aliyor, yani gomen kisinin hicbir diki yeri yoktu. Bu,
  siradan promosyonlari yazilamaz yapiyordu: tek kurulumdan iki marka satan bir
  dukkan, "yuzde on, yalnizca A markasi" diyemiyordu -- cart modulunde o
  modulun hic duymadigi bir kavram icin kolon acmadan. Bir cerceve icin bunun
  tersi olmali.
  Onek SUS DEGIL: metadata'sinda `customer_group_id` tasiyan bir sepet, aksi
  halde o cantayi YAZAN tarafa kendine segment indirimi verdirirdi. Nokta
  bilincli -- iki sabit adin ikisinde de nokta yok, yani iki uzay hicbir
  yazimla carpisamaz. Yalnizca DIZE degerler geciyor: motor butun degerleri
  karsilastiriyor, yani bir sayi bicimlendirme kurali isterdi ve 1 ile 1.0 ayni
  sayi ama iki farkli oznitelik degeri. Sayi isteyen tacir sayiyi dize yazar;
  sayisal islecler onu zaten cozuyor. Sayi SINIRLI, cunku her oznitelik her
  toplam turunda indirim istegine kopyalaniyor.

- **Elenen bir promosyon artik NEDEN elendigini soyluyor** — ve yalnizca
  operatore (`skipped[]`, ADR 0110). `eligible()` bool donuyor ve sebebi
  dusuruyordu: dokuz kapi tek bir `false` uretiyordu, yani hesap NEYIN
  uygulandigini soyleyebiliyor ve tacirin yayimladigi kuponun neden
  uygulanmadigini soyleyemiyordu -- sifir indirim goruyor ve dokuz hipotezle
  kaliyordu. ADR 0109 soruyu SIRADAN yapti: musteriler artik kod yazabiliyor.
  Engel aday sorgusuydu. `ListApplicablePromotions` `status = 'active'` tasiyor,
  yani yayimlanip AKTIF EDILMEMIS bir promosyon hic aday olmuyor -- ne uygulandi
  ne elendi diye doner. Bir kodun hicbir sey yapmamasinin en sik sebebi tam
  olarak bu, ve ucun veremedigi tek cevap oydu. `ExplainDiscounts` durum
  suzgeci OLMAYAN okumayi kullaniyor; iki yolun TUTARLARI birebir ayni ve oyle
  olmak zorunda, cunku tacire bir yolun sayilari gosteriliyor ve musteriden
  otekinin sayilari tahsil ediliyor.
  Sebep YALNIZCA yonetim ucunda: musteriye "bu kod var ama kampanyasi henuz
  baslamadi" demek, kod tahmin eden birine kampanya takvimi cikarma imkani
  tanir. Kampanyanin uc hali (silinmis, penceresi kapali, butcesi bitmis) TEK
  kelime -- tacire ayni cevap, ve ayirmak kampanyanin takvimini yanita koymak
  olurdu.
  Sebep kumesi KAPALI ve sozluk ikinci kez yazildi, cunku Go adlandirilmis bir
  dize tipinin uyelerini sayamiyor: kimsenin uretemedigi bir kelime, ucun vaat
  edip hic vermedigi bir cevaptir -- ve bu varsayimsal degil, bu degisikligin
  ILK hali sorgu genisletilmeden once `not_active` ile tam olarak onu yapiyordu.

- **Musterinin YAZDIGI kupon artik sepete iniyor, ve siparis onu HARCIYOR**
  (`cart_promotion_code` + saga adimi, ADR 0109). Promosyon motoru kurulduğundan
  beri kupon kodu aliyordu ve kimse ona kod GONDERMIYORDU: sepetin kodu
  koyacak yeri yoktu, indirim isteginin "codes" dizisi hep bostu, yani yalnizca
  OTOMATIK promosyonlar bir sepete ulasabiliyordu -- tacir kuponu yayimliyor ve
  yazan her musterinin hicbir sey almadigini izliyordu. Sessiz olan yarisi daha
  kotuydu: kullanim sayacini ve kampanya butcesini hareket ettiren
  `RedeemPromotion`'i bu depoda HICBIR SEY cagirmiyordu, yani tek kullanimlik
  bir kupon hic sinirlanamiyordu.
  Kod YAZILMADAN ONCE soruluyor; tersi, tur suresince kullanilamaz bir kodu
  sepette tutar ve sonra geri almak zorunda kalir -- o geri almanin basarisizligi
  musteriyi hicbir seyin karsilamayacagi bir kuponla birakir. HICBIR SEY
  indirmeyen bir kupon yine de uygulaniyor: hedefine uyan satiri olmayan gecerli
  bir kod gecersiz degildir, yalnizca bugun ise yaramamistir.
  Kuponlar siparis ACILMADAN once harcaniyor, ve referans SEPET: son hakki
  musteri odeme sayfasindayken alinan bir promosyon alisverisi reddetmeli, ve
  siparis var olduktan sonra reddetmek hic acilmamasi gereken bir siparisi iptal
  etmek demek. **Yukseltme aninda yarim kalmis bir alisveris kurtarilamaz** --
  motor adim ADLARINI kayitla eslestiriyor ve bes adimlik kayit alti adimlik
  tanimla uyusmuyor; bedel ADR'de yazili.

- **Bir gorsel artik DUZELTILEBILIYOR, adresi ise degistirilemiyor**
  (uc admin ucu, ADR 0108). ADR 0104 `alt_text`'i vitrinde ve GraphQL tipinde
  YAYIMLADI ve duzeltilebilir birakmadi: `CreateProduct` gorselleri aliyordu ve
  tabloyu baska hicbir sey yazmiyordu, yani yanlis yazilmis bir alt metin urun
  yasadigi surece kaliyordu -- ve yayimlanmis yanlis bir metin, hic olmayan bir
  metinden KOTUDUR, cunku ekran okuyucu artik hatayi okuyor. Yama alt metne,
  siraya ve metadata'ya ulasiyor; ADRESE ULASMIYOR: `url` ile yukleme bagi ayni
  cagrida yazildi, ve birini otekini birakmadan tasimak satirin kendi kolonuyla
  bag kaydini ayri dosyalari gosterir hale getirir -- modulun "bu gorseli su
  yuklemeye bagla" ucunu tam olarak bu yuzden acmadigi durum. Resmi degistirmek
  YENI bir gorsel ve eskisinin silinmesi, yani ne yaptigini soyleyen iki cagri.
  Her sorgu IKI kimlik tasiyor: yalnizca gorselin kimligiyle adreslenen bir uc,
  cagirana kendi urununu adlandirip baskasinin resmini duzenletirdi.
  `alt_text` bir uzunluk siniri kazandi, ve HER IKI yazma yolunda: iki yoldan
  birinde duran sinir, sinir degildir.

- **Iki sepet artik BIRLESEBILIYOR, ve adet TOPLANIYOR** (ADR 0107). Giris
  yapmak bir sepeti DEVREDEBILIYORDU ve KATLAYAMIYORDU: uyenin kendi sepeti
  varsa devir reddediliyordu ve musteri iki sepetle kaliyordu, birini bir daha
  gormemek uzere. Cakisan adetin toplanmasi YENI bir karar degil --
  `AddLineItem`'in karari, bir yigina uygulanmis hali: ayni varyanti iki kez
  eklemek tek satirin adedini yukseltir (fiyat kademesi toplam adetten
  seciliyor, tek satir tek rezervasyon demek, ayni urun iki kez iki urun gibi
  okunuyor), ve ayni iki ekleme IKI OTURUMDA yapildi diye baska cevap
  vermemeli. Kilit ROLE gore degil KIMLIGE gore aliniyor: ters yonde kosan iki
  birlestirme yoksa her biri otekinin bekledigi satiri tutar ve PostgreSQL
  birini oldurerek cozer -- entegrasyon testi bu mutasyonu gerceklestirdiginde
  tam olarak oyle oldu.

- **Bir talep artik NE OLDUGUNU GOSTEREBILIYOR** (`order_claim_evidence`,
  ADR 0106). Talep bir gerekce ve bir not tasiyordu, yani "kutu ezilmis geldi"
  bir CUMLEYDI ve hicbir zaman bir fotograf degildi. Baglama yuklemenin
  KIMLIGIYLE kuruluyor, adresiyle degil: `product_image` ikisini birden tasir
  cunku adresi her urun goruntulemesinde bir sayfaya yaziliyor; bir talebin
  kaniti aylar sonra, tek operator tarafindan, tek talep icin aciliyor ve
  imzali bir adres o zamana kadar suresini doldurmus oluyor. Ayni dosya bir
  talebin kaniti BIR KEZ olur -- cift tiklama ikinci bir fotograf degildir --
  ama iki ayri talebin kaniti olabilir.

- **Bir siparisin BORCU dusurulebiliyor, SATILAN degismeden**
  (`order_credit_lines`, ADR 0105). Siparisin toplami sepetin anlik goruntusudur
  ve kendi satirlarina bir CHECK ile civilidir; satistan sonra verilen bir taviz
  musterinin ne aldigini degil ne odeyecegini degistirir. Tavan siparisin TOPLAMI
  ve siparisin KILIDI altinda denetleniyor; odemeden sonra verilen bir taviz
  bakiyeyi eksiye dusurur, ki bu "dukkan musteriye borclu" demektir ve bir iade
  onu kapatir.

- **Bir gorsel artik NE GOSTERDIGINI soyluyor** (`product_image.alt_text`) —
  vitrinde ve GraphQL tipinde yayimlaniyor. Bos deger EKSIK degil CEVAP: HTML
  `alt=""`'a "bu gorsel bilgi tasimaz" anlamini veriyor, yani dekoratif resmin
  kendisi. Kolon bu yuzden nullable degil, ve degeri kirpiliyor -- tek bosluktan
  ibaret bir alt metin, birinin verdigini sandigi bir aciklamadir (ADR 0104).

- **Bir promosyon kurali artik URUNU ve KOLEKSIYONU adlandirabiliyor.** Satir
  yalnizca VARYANTINI tasiyordu, yani bir urune indirim yazan tacir her
  varyantini tek tek saymak zorundaydi. Iki anahtar da turun ZATEN okudugu urun
  satirindan geliyor, yani ek maliyet yok. Kategori ve etiket LISTEDIR ve satir
  niteligi tek bir dizedir; onlari tasimak motorun sozlesmesini degistirmek
  demek, ve o karar burada YAZILI olarak erteleniyor (ADR 0103).

- **Siparis belgeye tahsil ettigi HER orani veriyor** — ADR 0097'nin zaten
  yaptigini soyledigi sey. Yapmiyordu: siparisin fatura yuzeyinde
  `tax_components` alani hic yoktu, okuyucu ureticinin hic yazmadigi bir anahtari
  okuyordu, ve butun toplamlar yine tutuyordu. Sakli tutan sey akisin kendi
  SAHTESIYDI: tuketicinin paketinde ELLE yazilmis bir siparis sekli, gercek
  ureticinin soyleyemedigini soyluyordu. Iki test artik hopu bagliyor (ADR 0102,
  D50).

- **Bir urun artik bir TIP giyiyor, ve bir vergi kurali onu adlandirabiliyor.**
  Vergi modulunun tuketicisi yazildigi gunden beri BAGLIYDI ve hep BOS geliyordu:
  tacir "kitaplar %1" diyemiyor, her kitabi tek tek adlandiriyordu. Tip, toplam
  yolunun ZATEN yaptigi katalog okumasindan geliyor -- ayni satir hem indirim
  bayraklarini hem tipi tasiyor ve BIR KEZ okunuyor. Tip silinince urunler ayni
  islemde serbest birakiliyor, cunku bayat bir tip isaretcisi para demek
  (ADR 0101).

- **Musteri artik kendi siparisinin ZAMAN CIZELGESINI goruyor**
  (`GET /store/v1/orders/{id}/timeline`) — ayni bilesim, siparisin ve MALIN
  anlarina daraltilmis. Para anlari ve arsivleme gecmiyor, ve vitrin yanit tipi
  tutar alanini HIC tasimiyor: kopyalayan bir duzenleme derlenmez. Yeni bir tur
  eklendiginde vitrinde GORUNMEZ olur, ki guvenli yon budur (ADR 0100).

- **Fiyat listesi de artik `metadata` tasiyor, ve HANGI kaydin tasidigi bir
  kurala baglandi.** Tacirin YAZDIGI kayit tasir (baslik, aciklama, pencere);
  merdivenin uzerinde hesap yaptigi `price`, `price_set` ve `price_rule`
  tasimaz. Guncelleme alani BIRLESTIRMEZ, DEGISTIRIR — birlestirme bir anahtari
  silmenin yolunu birakmazdi (ADR 0099).

- **Degisiklik gunlugu artik SON SURUMDEN BERI alinan her karari anmak
  zorunda, ve bunu bir kapi tutuyor.** Nufus iki belgenin kendi
  tarihlerinden turetiliyor: git komutu yok, elle yazilmis bir taban yok.
  Bedeli, bir kararin duyurulmasinin artik onu vermenin parcasi olmasi
  (ADR 0098).

- **Vergi kirilimi BELGEYE ulasti: fatura satiri artik her orani ayri
  yaziyor.** Faturalama akisi siparisin `tax_components` alanini okuyor,
  fatura modulu `invoice_line_taxes` icine yaziyor; bedeli ayni bes alanin
  dorduncu kopyasi. Yeni tablo saklama korumasina alindi ve ADR 0095'in
  acik biraktigi sinir kapandi (ADR 0097).

- **Bir satir artik kendisini vergileyen HER orani hatirliyor.** Kirilim
  vergiden sepete, kasadan `order_line_taxes` tablosuna kadar satirla
  birlikte gidiyor; satirin kendi `tax_rate_bps`'i yiginin TABANI olarak
  kaliyor, liste ise butunu. Yolun uzerindeki iki sinir bilmedigi alani
  sessizce dusurdugu icin her sema TEK commit'te degisti (ADR 0096).

- **Bir oran baska bir oranin USTUNDE durabiliyor.** Secim degismiyor: yine
  tek oran secilir, sonra basini cektigi yigina genisletilir, her bilesen
  kendi tabaninda yuvarlanir ve satirin vergisi bunlarin toplami olur.
  Bedeli, satirda saklanan oranin yiginin TABANI olmasi — fatura simdilik
  yalniz taban orani yaziyor (ADR 0095).

- **Bir urun artik bir vergi SINIFI giyiyor** (ADR 0094) — `tax_class`
  sinifi adlandirir, `tax_class_member` urunu baglar, ve bir oran kurali tek
  tek urunlere degil bir sinifa yazilabiliyor. Sinifi vergi modulu kendi
  tablolarindan cozer: kimse gondermez, telde hicbir sey degismez. Bir urun
  en cok BIR sinifta olur; ozgullukte urunun arkasinda, tipin onunde gelir.

- **Vitrinin stok rozeti artik yalnizca kanalin depolarini sayiyor.**
  Envanter ayni toplami DEPO KIRILIMIYLA da yayimliyor, vitrin onu ikinci
  bir genisletmeyle topluyor: rozet ile kasa artik ayni baglamayi okuyor.
  Daraltmayan okuma hicbir sey odemiyor; kirilim eksikse cevap toplam degil
  SIFIR (ADR 0093).

- **Satis kanali artik KENDI depolarindan sevk ediyor** (ADR 0092) — stok
  konumu ile kanal arasina bir bag kondu, ve kasa rezervasyonu siparisin
  kanalina hizmet eden depolarla sinirlaniyor. Baga sahip olmayan kanal
  hicbir seyi daraltmaz. Vitrin rozeti daraltilmadi: stokta gorunen urun
  KASADA reddedilebilir.

- **Bir kategoriyi tasimak artik butun agacin KILIDINI aliyor.**
  `pg_advisory_xact_lock` ikinci tasiyani bekletiyor, ifadenin dongu
  muhafizi de bekleyisin ardindan onu reddediyor. Halka kapatamayan bir
  yazma — ad, sira, bayrak, ebeveyni bosaltma — kilit almiyor; bedel iki
  tasimanin artik ayni anda kosmamasidir (ADR 0091, ADR 0085'i tadil eder).

- **Bir talep artik yalnizca parayla degil MALLA da kapaniyor.**
  `internal/workflows/returns.DispatchReplacement` mali ayirir, siparise
  koli acar ve stoktan duser. Rezervasyon artik bir AMAC tasiyor: ayrilan
  mal defterden `replacement` olarak cikiyor, satisla karismiyor. Bedeli,
  bir akisin baska bir akisi adiyla cozmesi (ADR 0090).

- **Mal ile cozulecek bir talep artik NE gonderilecegini soyluyor.**
  `order_replacements` ve `order_replacement_items` siparisin yanina
  kuruldu; dort yonetim ucu kaydediyor, okuyor, listeliyor ve geri aliyor.
  Hicbir sey gonderilmiyor — `claim.go` bir `replace` talebini hala
  cozmuyor, ama artik tahmine degil bir KAYDA karsi yazilabilir (ADR 0089).

- **Iptal edilmis bir koliyi adlandiran anahtar REDDEDILIYOR** (ADR 0088) —
  "zaten acik" cevabi, koliden sag kalmis bir baglantidan geliyordu. Durum
  akisin zaten sahip oldugu dar yuzeyden okunuyor, yani modul siniri
  genislemiyor; bedeli her acilista bir cagri. Hata sevkiyati adlandirir ve
  YENI bir anahtarin gerektigini soyler.

- **Bilinen sinirlarin grup ADLARI da tutuluyor, yalnizca sayilari degil.**
  README'nin listesi `docs/known-limits.md` basliklarina kucuk harfle ve
  SIRAYLA esitlendi: ortaya eklenip sona yazilan bir grup, fiyatladigi
  belgeden baska bir belgeyi anlatir. Bir maddenin DOGRU baslik altinda
  olup olmadigini yine hicbir kapi tutamaz (ADR 0087).

- **Bir fiyat vergisini ICINDE tasiyabiliyor** — ve cikarma, duz hesabin
  tersi degil, KENDI aritmetigi. Bayrak vergi BOLGESINDE durur ve NULL
  DEVRALMA demektir; hicbir sey soylemeyen bir zincir vergi haric kalir,
  yani mevcut kurulumlarda degisen bir sey yok. Etikette yazan tutar artik
  kasada odenen tutar (ADR 0086).

- **Kategori artik DEGISTIRILEBILIYOR: `PATCH
  /admin/v1/product-categories/{id}`** — ad, ust kategori ve bayraklar
  yazilabiliyor, kapali dogan bir kategori nihayet acilabiliyor. Halka
  kapatacak bir tasimayi IFADENIN kendisi reddeder, yaninda duran bir
  kontrol degil (ADR 0085).

- **`docs/gaps.md`'nin numaralari artik tekil ve YOGUN.** Defterin ilk
  paragrafinda zaten duran kurali nihayet
  `internal/arch/gap_ledger_test.go` tutuyor. Ayni adrese oturan uc satir
  D36-D38 olarak yeniden numaralandi; onlari getiren commit mesajlari eski
  numaralari adlandirmaya devam ediyor (ADR 0084).

- **Belgedeki zincirli komut blogu artik KOSULUYOR, yeniden yazilmiyor**
  (ADR 0083). `docs/security.md`'nin blogu oldugu gibi `sh`'e veriliyor ve
  cevaplari sirayla okunuyor: rota kapisinin goremedigi baslik adi, govde
  alani ve `jq` yolu ilk kez tutuluyor. Bedeli, testin `curl` ile `jq`
  istemesi ve onlarsiz atlamak yerine DUSMESI.

- **Es zamanlilik sozu veren her tip artik IKI goroutine'den kosuluyor**
  (ADR 0082) — nufus, paketin sozu ile tipin tasidigi ilkelin kesisiminden
  geliyor ve tanigi `internal/arch/concurrency_promise_test.go` icindeki
  yazili harita adlandiriyor. `Bootstrap`'in godoc'u da artik garantisinin
  neyi kapsamadigini soyluyor: bir abone rota degildir.

- **Baslangic yoklamasinin olctugu her yol artik bir TANIK test tasiyor.**
  Oznesi kod degil KUME oldugu icin, sozlesmeyi bozan bir kume suiti uc
  yerde kirmiziya ceviriyor; nufus bir listeden degil yoklamanin kendi
  SQL'inden turuyor (`internal/arch/cluster_contract_test.go`). Suitin
  kaplari uretimin initdb argumanlarina baglanmadi (ADR 0081).

- **Uc fuzz hedefi yayimlandi, ve onemli tohumlar bulunmadi: HESAPLANDI.**
  `go test` bir hedefin yalnizca TOHUMLARINI kosar; her hedef en az uc tohum
  tasiyor (`internal/arch/fuzz_seed_test.go`), `make fuzz` ise CI'da
  kosmuyor. Kalan kural: hedefi yazdiktan sonra korudugu kodu mutasyona
  ugrat, uretilen girdi bulamazsa siniri hesapla ve tohumla (ADR 0080).

- **Her benchmark bir `benchbudget.Budget` tasiyor, ve tavan islem basina
  TAHSIS.** Butceler siradan test seridinde kosuyor; nufusu
  `internal/arch/benchmark_budget_test.go` dosya adindan degil BILDIRIMDEN
  turetiyor. Fiyatlanan bir yola eklenen tahsis artik bir testi kiriyor;
  tahsis etmeden yavaslayan degisiklik ise hala gorunmuyor (ADR 0079).

- **Her DOGRUDAN bagimlilik bir gerekce cumlesi, her DOLAYLI olan bir satir
  tasiyor** — cumleyi bagimliligi secen yazar, kesfeden tuketici degil
  (`internal/arch/dependency_allowlist_test.go`). `govulncheck` kokte ve iki
  ornek modulde kosar, bilinen bir acik derlemeyi KIRAR, ve muafiyet
  mekanizmasi yok (ADR 0078).

- **`core/providertest` YAYIMLANDI: bir saglayici artik yazili bir cumleyi
  degil, KOSULABILIR bir uyum suite'ini geciyor.** Yuzey on sekiz pakete
  cikti ve agactaki on iki saglayici, bir gomenin kosacagi suite'in AYNISINI
  kendi paketinden kosuyor. Suite yalnizca servise gitmeden tutani denetler
  ve bunu soyler: yesil "calisiyor" demek degil (ADR 0077).

- **ADR 0030'un gocu BASLADI: panelin ilk `/admin/v1` ekrani moderasyon
  kuyrugu** (ADR 0076). Ekran bir kabuk ve bir betik, yeni modul sozlesmesi
  yok. Oturum cerezi artik `/admin` agacinin tamamina gidiyor: yonetim
  API'sinin CSRF bagisikligi bir YOKLUKtu, yerine bir savunma kondu —
  cerezle gelen durum-degistirici istek ayni kokenli bir `Origin` istiyor.

- **`docs/measurements/README.md` artik DENETLENIYOR: her satir raporunun
  gercek uzunlugunu soyler, ve her raporun bir satiri vardir.** Asil kazanc
  ikinci yon: indekslenmemis kanit, adini bilmeyenin BULAMADIGI kanittir.
  Bedeli tek satir -- raporu buyuten commit indeksin satirini da tasir
  (ADR 0075).

- **Model ile operatorlerin uyusmasi artik SAYILIYOR.**
  `GET /admin/v1/reviews/suggestion-agreement` model basina iki sayi verir:
  karara baglanmis kac yorumda oneri var, ve onerilerin kaci yorumun
  bittigi statuyu adlandirmis. ORAN yok; paydayi goren istemci kendi
  hesaplar. Rapor okumada hesaplanir, hicbir sey saklanmaz (ADR 0074).

- **Bir oneri verilen karardan SAG CIKAR: moderasyon onu silmez.** Yonetim
  listesi artik `?suggested=` ile daraliyor, taninmayan deger bos sayfa
  degil RED aliyor ve suzgeci kuyrukla sinirli `reviews_suggestion_idx`
  tasiyor. Bedeli buyuyen bir tablo, aldigi sey modelle insanin anlasmasini
  olcebilecek TEK korpus (ADR 0073).

- **Model KAPALI bir soruyu yanitliyor, ve soruyu zamanlanmis bir is
  soruyor.** `core/provider` bir siniflandirma sozlesmesi yayimliyor, tekil
  `ai.provider` yuvasini `ai-anthropic` eklentisi dolduruyor, ve is yalnizca
  yuva doluysa kaydediliyor. Eklentiyi adlandiran kurulum bir ALT ISLEYICI
  ustlenir, ve hicbir dogruluk iddia edilmiyor: olculmedi (ADR 0072).

- **Bir modelin onerisi yorumun YANINDA saklaniyor, kararinda degil.** Dort
  kolon kendi basina duruyor; `status` ve `moderated_at`'e dokunulmuyor,
  boylece ayna hala bir insanin karar verdigini soyluyor. Oneri butun olarak
  var ya da hic yok, karari verilmis bir yoruma yazilmiyor, ve alisverisciye
  hicbir yerde gorunmuyor. Henuz oneriyi yazan bir sey yok (ADR 0071).

- **Bir sayi iddiasi KAPALI bir kelime dagarcigina karsi denetleniyor**
  (ADR 0070) — sekiz nufus var, ve bir cumle kapiya ancak nufusun YOLUNU
  ayni satirda yazarak giriyor. Kapi acildigi gun README'lerde yanlis
  sayilar buldu. Her ADR kaydi ve bu dosya kapsam disi; evrensel
  olumsuzlama ise kapisiz kaliyor, cunku nesnesi bir yuklem.

- **Is raporunun KANALI yayimlandi, zamanlayici yayimlanmadi.**
  `core/jobreport` uc fonksiyon tasiyor; kosucu `internal/core/job`'da kaldi
  ve cekirdegin kendi isleri de ayni yayimlanmis paketten rapor veriyor —
  TEK mekanizma. Artik bir eklentinin basarili kosusu da `gobit jobs`
  detayinda konusabiliyor; bedeli `core/`'un on yedinci paketi (ADR 0069).

- **Fiziksel stogun her degisimi bir SATIR birakiyor, ama sayiyi hala
  `stocked_quantity` tutuyor** (`inventory_movements`). Hareket kolonla ayni
  islemde yazilir ve `stocked_after` tasir; kayma tek satirda gorunur.
  Ayirma bir hareket degil, satirin arkasinda aktor degil bir SEBEP var, ve
  hicbir sey satiri silmiyor; defter yonetim ucuyla geldi (ADR 0068).

- **`province` ulke altindaki birimdir, ilce DEGILDIR.** Elle yazilan her
  bildirim artik bunu soyluyor; `internal/arch/province_test.go` SESSIZ bir
  yenisini reddediyor. Uctan uca adres duzeltildi, ilcenin hala bir alani
  yok (ADR 0067).

- **Oneri deposu KURULMUYOR; bir oneri, konusu olan satirin sahibi modulde
  durur ve o modulun yazma yolundan uygulanir** (ADR 0066). Tetik, bir
  sorgunun yeniden uretemedigi ilk oneridir; uygulayan sey gobit degil,
  mevcut ucu cagiran insandir. Bedeli: bugun oneri isteyen operator hicbir
  sey bulmuyor, ve iki module yayilan bir onerinin burada evi yok.

- **`coreprovider.QuoteInput` GENISLETILMEDI: ilce ve desi, agac bir koliyi
  adresleyip olcebildigi gun gelir.** Bedeli, ilceye gore fiyatlayan bir
  kargo entegrasyonunun duz tarifede kalmasi. Kurali bir kapi tutuyor:
  `TestEveryQuoteInputFieldIsFilledByTheTree`, agacta hicbir uretim
  dosyasinin doldurmadigi alani yayimlanmis girdide reddediyor (ADR 0065).

- **Kayitli odeme araci beklemede, ve bekledigi sey bir ozellik degil bir
  SAGLAYICI.** Depolanmis bir token'la odeme bugun uctan uca calisiyor;
  eksik olan, boyle bir token'i URETEN bir ust akis. Alisverisci her kasada
  kartini yeniden yaziyor, ve yayimlanmis yuzey hicbir uygulayicisi olmayan
  bir sekle harcanmiyor (B9 → ADR 0064).

- **Stok olayi ve dosya olayi YAYIMLANMADI: ileten bir eklenti bir konunun
  ilk abonesi degildir.** `plugins/webhookout` her konuyu zorunlulukla tasir
  ve `TestEveryTopicHasASubscriberThatChoseIt` bunu artik reddediyor.
  Bedeli, her kapiyi gecebilecek iki olayin gonderilmemesi; B15 ile B7'nin
  olay yarisi bosluk olarak degil KARAR olarak kapandi (ADR 0063).

- **Bir geri cagrinin defteri, onu ALAN modulun kendi tablosudur** — kendine
  ait bir `callback_log` yok. Okuyucu, kapsam ve saklama sorusu zaten
  `paytr_payment` tarafinda cevaplanmis; isleyicisi hic kosmamis bir cagri
  ise yalnizca log'ta kalir ve gobit ona saklama sozu vermez. Karar
  IKINCI bir saglayici agaca girdigi gun yeniden acilir (ADR 0062).

- **gobit dil ekseninin iki yarisini da kurmuyor, ve bu bir eksiklik degil
  KARAR: A11 defterden bir cevapla cikti.** Kaydi olmayan bir yerel ayari
  iki kapi reddediyor — `plugins/webpush` disinda bir Go adi ya da struct
  etiketi, cihaz kaydi disinda bir SQL kolonu. Bedeli, yol ya da sorgu
  anahtari olarak gelen bir yerel ayarin izlenmemesi (ADR 0061).

- **Migration rolu ile runtime rolunu ayirmak OPERATORUN isi; gobit'in
  ikili dosyasi degismiyor.** Tek DSN kalir, rol yonetimi ve acilis
  sinamasi gelmez; verilen sey bir ayar degil, `security.md`'de yayimlanan
  yetki listesi. Kutudan cikan tek superuser kurulumu ise hem degismeden
  hem korumasiz kalir (ADR 0060).

- **Olcum duzenegi artik CARPIK bir taksonomiyi de kurabiliyor:
  `Spec.SkewedCategorySize` iki kucuk kategori dogurur, sifir hicbirini.**
  Kucuk kategori vakasi artik elle kurulan bir deneme veritabanini degil bir
  KOMUTU istiyor; bedeli, bir urunun ilk kez iki kategoriye ait olmasi ve
  konumsal uyeliklerin boyu degistirirken sifirlanmayi istemesi (ADR 0058).

- **Musteri adlandiran her vitrin ucu iddiasini TEK bir karsilastirmaya
  veriyor:** `corehttp.ProvenCustomer`. b2b vitrini ile sepet ona baglandi,
  adres defteri de onun uzerine tasindi. Sorgulanan sey UC degil IDDIA;
  dogrulayici baglanmamis kurulumda hicbir sey geri cekilmiyor, yalnizca
  WARN dusuyor (ADR 0057).

- **Bir geri cagri `audit_log` satiri OLMUYOR; kaydi `CallbackRegistry`'nin
  kendi gunlugu.** Her sonuc oraya bir satir birakiyor, reddedilenler dahil.
  Bedeli, operatorun sorgu bekledigi yerde bir gunluk aramasi yapmasi:
  `GET /admin/v1/audit-log` hicbir geri cagri gostermiyor (ADR 0056).

- **Bir stok konumu BOS kapanir, ve kapali satir okunabilir kalir.**
  Uzerinde birim ya da canli bir ayirma duran konum kapanmayi REDDEDER,
  kapali konum stok yazmasi kabul etmez; uygunluk okumalari boylece
  join'siz kaliyor. `deleted_at` yerini `closed_at`'e birakti, ve kapanis
  nihaidir: geri acma yok (ADR 0055).

- **Siparis ve odeme SILINMIYOR: on `deleted_at` kolonu dusuruldu, ve para
  olaylarinin yuzeyi ucuncu bir ani kazanmadi.** Bir siparis STATU ile emekli
  olur, bir para kaydi saklanir; artik hicbir kayit gizlenemez. Dort
  benzersizlik kurali da nihayet HER satiri kapiyor: bir idempotency anahtari
  elle damgalanarak serbest birakilamiyor (ADR 0054).

- **gobit TEK dil saklar, ikinci dil gomen programin.** Locale kolonu,
  ceviri tablosu ve ceviri modulu yok; ikinci dili bugun tasiyabilen tek yer
  `metadata` alani olan tablolar, ve kategori, etiket, secenek ile gobit'in
  seed ettigi ulke ve para birimi adlari o yolun disinda. Vitrine ulasan
  hicbir istek henuz DIL soyleyemedigi icin A11 acik kaliyor (ADR 0050).

- **Fiyati BIR grup belirliyor, ve gruplari SATICI siralar.** Sepet,
  musterinin en yuksek sirali grubunu tek bir `customer_group_id` degeri
  olarak yaziyor; `customer_group` bir `rank` kolonu kazandi. Fiyatlama,
  kampanya ve teslimat hic degismedi; sirayi hic kurmayan magaza kimlik
  sirasini alir (ADR 0049).

- **Kume sozlesmesi KIMILDAMIYOR: pgvector istege bagli ayri bir eklenti
  modulu olarak gelir** (ADR 0045). Uzanti satiri `none` kaliyor, cunku
  kimsenin kurmak zorunda olmadigi bir eklenti hicbir kurulumun ne
  saglamasi gerektigini degistirmez. `CREATE EXTENSION` yalnizca o modulun
  kendi migration'ina ait; disina cikarsa ADR 0015 ayni degisiklikte acilir.

- **Musterinin odedigi ile saticinin aldigi ayni sayi KALIYOR.** Bir fark
  gerektiginde esitlik gevsetilmez; fark, kendi karsi tarafini tasiyan ayri
  bir MUTABAKAT SATIRI olarak gelir ve satirin sekline ilk tuketici karar
  verir. Esitligi tutan dort katman ilk kez tek tek adlandirildi; bedeli,
  taksit vade farkinin bugun hala mumkun olmamasi (ADR 0042).

- **Bir e-posta adresinin saklanma bicimi TEK kural: kirpilir, sonra GO
  tarafinda kucuk harfe cevrilir, veritabaninda asla.** `invoice` da artik
  Go'da katliyor: `buyer_email` belgenin dedigini aynen tutuyor, esitlik
  `buyer_email_folded` uzerinden kuruluyor. Alti kopya yerinde kaliyor,
  onlari `internal/arch/email_test.go` bir arada tutuyor (ADR 0038).

- **Bir insan, silinebildigi seyi artik GOREBILIYOR** (ADR 0034) — kisisel
  veri aciklamasi silmenin yanina yayimlandi ve bir kisinin dosyasi, onu
  silen ayni supurge tarafindan toplaniyor. Cevap veremeyen bir tutucu
  dosyanin icinde `Unresolvable` olarak gorunuyor: eksiklik bir deftere
  degil, kisinin aldigi belgeye yaziliyor.

- **Gelen bir saglayici cagrisi BAGLANMIYOR, KAYDEDILIYOR** (ADR 0028) —
  eklenti rotayi `Host.RegisterCallback` ile bildirir, baglamayi cekirdek
  yapar; kota, govde siniri, zaman asimi, imza dogrulamasi ve tekrar
  penceresi hepsine uygulanir. Dogrulayicisi olmayan bir rota acilista
  reddedilir: korumasiz bir uc artik IFADE EDILEMIYOR.

- **Bilesim koku `internal/app`'e tasindi, ve modul kokundeki YAYIMLANMIS
  cephe onu cagiriyor: `cmd/server` artik on bes satir.** Agac disinda bir
  uygulama boylece mumkun, ve operator altkomutlari kutuphaneyle birlikte
  geliyor. Cephe dort metottur; yasam dongusu yayimlanmadi ve `internal/`
  agacini yalnizca o import edebilir (ADR 0027).

- **Iki saat KALIYOR, ve her an kendi saatini adlandiriyor** (ADR 0053).

- **`returned_at` nihayet moduller arasi okuma katmanina ulasti.** Kolonda,
  modelde ve yonetim govdesinde vardi; yalnizca baska bir modulun ne
  okuyabilecegine karar veren haritada yoktu. Eksigi hicbir sey RAPOR
  EDEMEZDI, cunku o harita bilinmeyen alani sifirla yanitlamaz, REDDEDER
  (ADR 0004) -- yani kimse dordunca ani istemedi ve kimse isteyemedigini
  ogrenmedi. Siparis timeline'i artik geri donen koliyi de gosteriyor
  (`shipment.returned`).

- **Migration iptali zarif katmanini birakti** (ADR 0052, ADR 0003'u tadil eder)
  — D31 teshis edildi ve KAPANDI.

- **D10'un artigi kapandi: sahiplik butun agacin, modullerin degil**
  (`internal/arch/module_sql_test.go`).

- **Kayitlarin sekli kurala baglandi: ADR 80 satir, olcumler ayri agacta.**

- **`docs/measurements/` acildi.** gaps.md'nin icindeki 15 olcum raporu (2.757
  satir) ve `catalog-search-cost.md` oraya tasindi, degistirilmeden. Bir ADR
  olcume tek satirla baglanir. Olcum dosyasi istedigi kadar uzun olabilir --
  kimse onu okumak zorunda degil; ADR'yi herkes okumak zorunda.

- **`docs/adr/README.md` indeksi.** Numara, baslik, tek cumlelik karar, durum
  (gecerli / hangi kayit degistirdi). Yeni gelen once bunu okur.
  `TestTheADRIndexNamesEveryRecord` iki yonu de tutuyor.

- **`docs/gaps.md` 4.615 -> 156 satir.** Her boslugun bir satiri var: soru, ve
  cevabin nerede oldugu. Kapali satir ADR'sini soyler ve susar; gerekce ADR'de,
  tarih git'te. Cizili metinler ve "orijinal adlandirma asagida" bloklari
  silindi. Sadelestirme sirasinda hicbir sey KARARA baglanmadi ya da yeniden
  acilmadi -- ama iki satirin BAYAT oldugu ortaya cikti: D22 (webhookout artik
  kurulabilir) ve D25 (parametre kapisi yazilmis) defterde hala acik
  gorunuyordu.

- **Magaza aramasi da satis kanalini YOLUNDA tasiyor** (ADR 0044'un dorduncu
  rotasi) — ve kural artik bir iddia degil, bir MEKANIZMA.

- **Magaza katalogu UC filtre kazandi** (ADR 0039, ADR 0040, ADR 0041) — secenek
  degeri, stok durumu ve fiyat araligi; hepsi tek bir yuzey.

- **Satis kanali katalog YOLUNA tasindi** (ADR 0044) — magaza katalogu artik
  `/store/v1/sales-channels/{sales_channel_id}/products` altinda.

- **Metrikler SCRAPE ile cikiyor** (ADR 0046) — `METRICS_ADDR` verilirse bir
  `/metrics` ucu acilir; OTLP izlerde kalir.

- **Yerine konan fiyat SILINIYOR** (ADR 0047) — ve bir replace'ten sag kalan sey
  zaten bir tarihce degildi.

- **Tasinan dort bayrak nihayet OKUNUYOR** (ADR 0048) — ikisi kasada, ikisi
  kampanya motorunda.

- **Denetim gunlugu (`audit_log`) nihayet OKUNABILIYOR** (ADR 0037) — ve
  okunmasi da kaydediliyor.

- **search eklentisi Ingilizceye cevrildi ve iki ucu anlatildi** — sema defteri
  SIFIRA indi, dil defteri 214'ten 202'ye.

- **Bilesen adi artik sahibi olan modulu tasiyor** (ADR 0036) — ve dun acilan
  otuz sekiz kisilik defterin yirmi besi ayni gun odendi.

- **Webhook eklentisinin operator yuzeyi hem TIPLENDI hem anlatildi**, ve
  tiplemek bir kusur ortaya cikardi.

- **Sema kelime dagarcigi YAYIMLANDI, ve sessizlige bir ses kondu** (ADR 0035;
  ADR 0026'ya ek not — on altinci paket).

- **Saga deposu artik kendi satirlarini siliyor, ve "budama" degil DUZENLEME
  cikti** (ADR 0033'e ek not).

- **KVKK silme sözleşmesi kuruldu ve bu on yedi kararın beşincisi oldu**
  (B17 → ADR 0033; ADR 0026 ve ADR 0032'ye birer ek not).

- **On yedi kararın ilk dördü verildi ve ADR olarak yazıldı: kök, çift ve tek
  canlı tehlike** (A2 → ADR 0029, A7 → ADR 0030, A12 → ADR 0031, A4 → ADR 0032).

- **"Bir kararın arkasında" diyen bir satırın kararı yazılmamıştı; yazıldı ve
  karar metin eşleşmesi çıktı** (A18, B2'nin OPTION VALUE yarısı).

- **Bir kararın adı, onu bulan ÖZELLİĞİN adı olursa, sonraki tur aynı soruyu
  ikinci kez öder** (A15, B4).

- **A15 uygulandı, alıntılanmadı: cevabı taşıyan şey SQL — bir yorum satırı
  değil** (A15, B4).

- **Yazılmış, belgelenmiş, uçtan uca test edilmiş bir eklenti KURULAMIYORDU —
  ve bu sınıf için yazılmış kapı YEŞİLDİ** (C5, D22).

- **`internal/app`'in migrate-down testleri, kanıtın kendisinde değil ÖN
  KOŞULUNDA düşüyordu.** İki test region'ın "tam iki migration"ı olduğunu elle
  yazmıştı; modül üçüncüsünü kazanınca ikisi de kırıldı. Sayı artık okunuyor:
  biri geri alınacak adım sayısını mevcut sürümden türetiyor, diğeri
  dokunulmayan sahibin sürümünü geri almadan ÖNCE okuyup onunla karşılaştırıyor.
- **Dört temel satırı, kendi satırlarının adlandırmadığı bir engel taşıyordu —
  ve ölçünce açık sekiz satırın HEPSİNİN engelli olduğu çıktı** (B9, B15, B16;
  B8 aynı gün).

- **Karar listesi bir SIRA olduğunu söylüyordu ama sırayı hiçbir yerde
  yazmıyordu; ölçülüp yazıldı, ve bir kök ile bir ÇİFT çıktı.**

- **İki modülün `api` paketinde hiç test yoktu, on üçünde vardı, ve bunu hiçbir
  şey sormuyordu** (D26).

- **Aynı makinenin öteki yönü de türetildi, ve iki yönü yazmak tarayıcıda DÖRT
  düzeltme gerektirdi** (D25'in devamı).

- **D25'in kapısı yazıldı ve ilk koşusunda iki canlı bulgu buldu** — ve
  reddettiğim naif biçim kayıt olarak duruyor.

- **Bir handler, hiç belgelemediği bir sorgu parametresini okuyabiliyor ve
  deponun bütün kapıları yeşil kalıyor** (D25).

- **gobit bir KÜTÜPHANE olacak, kopyalanan bir şablon değil** (ADR 0025).

- **Kimliği bilinmeyen bir yazma artık ŞEMA tarafından da tutuluyor** (ADR 0051)
  — kararın üçüncü parçası, kaydın kendisinin "yapılmadı" diye taşıdığı parça.

- **Adres defteri artık KANITLANMIŞ bir kimlik istiyor** (ADR 0043) — ve gobit
  o kimliği hâlâ ÜRETMİYOR.

### Düzeltildi

- **Yayımlanmış bir API tarifi, ucun yaptığının TERSİNİ söylüyordu** (D62).
  ADR 0125 dört vitrin ucunu varsayılan olarak reddeder yaptı; eski davranışı
  anlatan sekiz pasaj peşinden gitmedi. Üçü OpenAPI tarifiydi — yani
  entegratöre verilmiş söz (ADR 0026) — ve belgeyi okuyan biri, ucun artık
  VARSAYILAN olarak döndüğü bir statüye karşı kod yazardı. Diğer beşi Go'daydı;
  ikisi `IdentityLookup`'ın var olma gerekçesini anlatırken reddi "ADR 0057'nin
  kaçınmak için yeniden yazıldığı kırıcı değişiklik" diye tarif ediyordu — ki ADR
  0125 tam onu, bilerek ve geri dönüş yolu vererek yaptı. Hiçbir kapı yakalamadı
  ve yakalayamaz: sayım kapısı nüfus fiyatlar, şema-adı kapısı tanımlayıcı
  çözer; ikisi de bir cümleyi ANLAMI için okumaz.

- **Fonlanmış bir değişim, malı çıktıktan sonra sonsuza kadar AÇIK kalıyordu**
  (D59). ADR 0120 değişime `funded` durumunu verdi ve kapanışın iki muhafızını
  da onun için genişletti; sevkiyat akışı ise kaynağı kapatırken kendi üçüncü
  koşulunu tutuyordu — sahibi olmadığı bir sözcük dağarcığının kopyasını. Yeni
  sözcük kopyaya girmedi, yani operatör farkı tahsil edip malı gönderiyor ve
  kayıt `funded` kalıyordu. Hiçbir şey düşmedi, hiçbir şey loglanmadı: o
  muhafızın bütün işi sessiz olmak. Akış artık sipariş modülünün CEVABINA
  bakıyor (`source_open`), duruma değil. Aynı commit'te ADR 0120 öncesi dünyayı
  anlatan on cümle düzeltildi — biri yayımlanmış OpenAPI tarifi.

- **Dört karar satırı bir KONU adlandırıyordu; ölçülüp SORUYA çevrildi — ve ilk
  taslak yirmi dört yanlış iddia taşıyordu** (A4, A5, A11, A12).

- **Belgelerin tamamı koda karşı ölçüldü: otuz sekiz iddia yanlıştı, ve üçü
  gaps.md'nin KENDİ tablosuyla çelişiyordu.**

- **Bir kök eklemek için yapılan temizlik, godoc'ların yirmi beş ölü dosya
  adına atıf yaptığını ortaya çıkardı — ve o sınıfı hiçbir kapı görmüyordu.**

- **Yol denetiminin kök listesinde beş dosyalık bir delik vardı, ve deliği
  bulan şey deliğe düşen bir dosya oldu.**

- **Hiçbir şeyin OKUMADIĞI bayrağın VARSAYILANINI da hiçbir şey tutmuyordu — ve
  `allow_backorder` yalnız değil, dördün biri** (A6, D2).

- **Kapıların kendisi denetlendi: 89 kapıdan üçü, yazılma sebebi olan kusurun
  tam üzerinde YEŞİLDİ** (D23).

- **Kargonun olayları SIRASIZ gelir; sevkiyat durum makinesi hepsini
  reddediyordu — tekrarları ise hoş görüyordu** (B10, D24).

- **Tax'ın şekli dört modülde daha arandı: ikisinde KUSUR çıktı, ikisinde
  çıkmadı — ve çıkmayışı da ölçüldü** (D6, D19, D20).

- **Sütun denetiminin üç kör noktasından İKİSİ kapandı, DÖRDÜNCÜSÜ kapatırken
  bulundu — ve düzeltme ilk koşumunda dokuz canlı bulgu çıkardı** (D16, D18).

- **Denetim düzeltilir düzeltilmez görünen dokuz sütun: hiçbir şeyin yazmadığı
  dokuz `deleted_at`** (D18).

- **Bir sütunun yazılmadığını yakalamak için kurulmuş denetim, godoc'unda ÖRNEK
  olarak verdiği bulguyu hiç yakalamamıştı — üç kör noktası da mutasyonla
  ölçüldü** (gaps.md D16).

- **Verilmeyen bir ölçüt artık HİÇ YAN TÜMCE YAZMIYOR — ve kategori süzgecinin
  gerçek maliyeti ilk kez ÖLÇÜLDÜ.**

- **Ürün modülünün sekiz kayıtlı performans rakamı yeniden ölçüldü; BEŞİ yanlış
  çıktı** (D15).

- **`make load-test` BOŞ bir katalog ölçüyordu** (D14) — ve bu, D11'in bir kat
  altı.

- **`make load-test` hiçbir şey ölçmüyordu — ve yeşil görünüyordu.**

- **`docs/gaps.md`: B2'nin kalan dört filtresi tek bir iş DEĞİLMİŞ — ölçüldü ve
  bölündü.**

- **`docs/gaps.md`: okuma önbelleği maddesinin dayanağı ÖLÇÜMLE çürüdü.**

- **`docs/gaps.md`: "yönetici oturumu iptal edilemiyor" maddesi YANLIŞTI.**

- **`docs/gaps.md`: misafir sepeti devralınamıyor maddesi YANLIŞTI.**

### Eklendi

- **Vitrinin dördüncü sözlük ucu kuruldu — ve tek metin döndüreni o**
  (B2'nin OPTION VALUE yarısının ön koşulu).

- **Vitrin kataloğu artık sıralanabiliyor — ve satırın "tek tuzağı" dediği şey,
  bindiği sözleşme tarafından zaten çözülmüştü** (B2'nin SORT yarısı).

- **Yorum modülü: bir müşteri yorum yazabiliyor, ve onaylanana kadar hiçbir
  yerde görünmüyor** (B4). On yedinci modül.

- **Bir koli GERİ DÖNEBİLİR: `returned` beşinci sevkiyat statüsü, ve tablo artık
  BİLDİRİM ile KOMUT'u ayırıyor** (B10).

- **Giden webhook'ların GÖNDERİCİSİ yazıldı — ve bugün KURULAMIYOR; bu
  varsayılmadı, ölçüldü** (C5, D22).

- **Hiçbir şeyin hiç yazmadığı dokuz sütun cevaplandı — ve dokuzun TEK bir
  cevabı yokmuş** (D18).

- **Başarılı bir iş koşusu artık KONUŞABİLİYOR: `gobit jobs` listesinin detay
  sütunu bir HATANIN arkasından çıktı** (D21).

- **Bir eklenti artık zamanlanmış iş kaydedebiliyor — ve uzatma noktası İLK
  TÜKETİCİSİYLE birlikte geldi** (B13).

- **Ölü mektupların artık bir OPERATÖR YÜZÜ var: `gobit deadletters`** (B12).

- **İade isteği ve talep artık GERİ ALINABİLİYOR — iki `UPDATE` ifadesinin
  üretimde hiçbir çağıranı yoktu** (D17).

- **Giden teslimat makinesi: artan gecikmeli yeniden deneme ve ÖLÜ MEKTUP — ve
  düzelttiği şeyin bir yavaşlama değil, teslimatın DURMASI olduğu ölçüldü**
  (B12).

- **Siparişin arşivlenmesi artık TARİHLENİYOR, ve takasın tablosu ilk `UPDATE`
  ifadesini aldı** (D5, D4).

- **Vergi modülü işlemi CONTEXT'te taşıyor, ve modülün ilk paylaşımlı kilidi
  var** (D6'nın vergi yarısı).

- **Panelin kataloğunda artık ARAMA KUTUSU var: okuma katmanı `q`'yu öğrendi —
  ve maliyeti 52.004 üründe ÖLÇÜLDÜ** (B2'nin son süzgeci, D12'nin kalan yarısı).

- **Ölçüm düzeneği artık DEPODAN kuruluyor: `gobit seed`** (D13).

- **Panelin kataloğu artık kategoriye göre daraltılabiliyor: okuma katmanı
  taksonomi süzgeçlerini ve bir `category` varlığını öğrendi** (B2, D12).

- **Bir modülün SQL'i yalnızca KENDİ tablolarını adlandırabiliyor**
  (`internal/arch/module_sql_test.go`).

- **Bir yapı dosyasındaki her `-run` deseni gerçek bir test adlandırmak zorunda**
  (`internal/arch/build_files_test.go`).

- **Satılan SATIR artık okunabiliyor: `order_line_item` varlığı ve panelde
  Satışlar bölümü** (B14).

- **Panelin bir çerçevesi ve ikinci bir bölümü var** (stil, menü, siparişler).

- **Fatura modülü** (ADR 0024) — belge, satırları, tarafları, durumu ve
  **boşluksuz** numaralandırması.

- **Sipariş artık tek çağrıyla faturalanıyor** (`POST /admin/v1/orders/{id}/invoice`).

- **Sipariş satırı artık hangi ORANDA vergilendiğini söylüyor** (`tax_rate_bps`).

- **Derin sayfa artık ucuz** (cursor pagination).

- **Go tarafı artık ölçülüyor** (pprof + benchmark).

- **Yönetim yazmaları artık iz bırakıyor** (audit log).

- **Söz verilen olay artık onu vaat eden işlemin parçası** (outbox, ADR 0023).

- **Tarayıcıdaki vitrin artık API'yi çağırabiliyor** (`CORS_ALLOWED_ORIGINS`).

- **Para iadesi geldi ve ADR 0022'nin açık bıraktığı yarıyı kapattı** — B2B
  bütçe hatası dahil.

- **Talepler (claim) artık çözülüyor — ama yalnızca parayla, ve ötekini
  REDDEDEREK.**

- **Müşteri artık iade talebi açabiliyor** (`POST /store/v1/orders/{id}/returns`).

- **İade artık EYLİYOR: teslim alınan malın stoğu geri konuyor**
  (`internal/workflows/returns`, satış sonrası 2/3).

- **Sipariş ile ödemesi arasında artık bir yol var** (`order_payment` link'i).

- **İade kaydı artık kımıldayabiliyor ve hangi satırların geldiğini söylüyor**
  (satış sonrası, 1/3).

- **Parası alınmış sipariş artık iptal edilemiyor** — ediliyordu, ve iptal
  hiçbir şeyi geri almıyordu.

- **Vergi artık doğru girdilerden hesaplanıyor** — karışık sepet baştan sona en
  yüksek orandan vergileniyordu.

- **Sipariş artık üzerine ne ödendiğini biliyor** (ADR 0022) — `paid_total` her
  gerçek siparişte sıfırdı.

- **Alışverişçi artık kendi kargo fiyatını belirleyemiyor** (ADR 0021) — bu bir
  özellik değil, sömürülebilir bir açığın kapatılması.

- **Ödeme mutabakatı** (`internal/jobs/paymentrecon`, ADR 0020) — deponun adı
  konmuş tek tutulmamış periyodik sözü, ve para hakkında.

- **Zamanlanmış iş geldi** (`internal/core/job`, ADR 0019) — ama planladığımın
  onda biri kadarıyla, ve asıl değeri kodda değil ÖLÇÜMDE.

- **PayTR ile ödeme geldi** (`payment-paytr` eklentisi) — ve web push'la
  **aynı bulguya** çıktı, ters yönden.

- **Tarayıcı push bildirimi geldi** (`web-push` eklentisi, ADR 0018) — ve
  sağlayıcı yuvasına GİRMEDİ. Bu turun asıl bulgusu kodu değil, kararı
  değiştirdi.

- **Yüklemeler artık nesne deposuna gidebiliyor** (`file-s3` eklentisi).
  Kutudan çıkan `local` sağlayıcısı TEK süreç için doğrudur ve İKİ süreç için
  yanlıştır: dosya, yüklemeyi karşılayan örneğin diskine düşer ve başka örneğe
  yönlenen her istek 404 alır — hiçbir hata görünmeden, çünkü o örnek
  açısından anahtar gerçekten yoktur. AWS S3, MinIO ve R2 ile çalışır.

- **Bildirimler artık GERÇEKTEN gidiyor** (`notification-smtp` eklentisi).
  Kutudan çıkan tek bildirim sağlayıcısı `logonly`'ydi ve adı ne yaptığını
  dürüstçe söylüyordu: bir log satırı yazar, hiçbir yere göndermez. Yani
  bildirim yuvası bugüne dek çerçevenin tutmadığı bir sözdü — sağlayıcı
  soyutlaması vardı, çalışan uygulaması yoktu.

- **İKİNCİ bir hata raporlayıcı yazıldı ve ADR 0014'ün sınavı böylece koşuldu**
  (`error-otlp`). ADR "sözleşmenin doğru ŞEKİLDE mi olduğunu yoksa yalnızca
  Sentry'nin istediği şekil mi olduğunu ancak ikinci bir uygulama gösterir"
  diyordu; ikinci uygulama, modeli Sentry'den en uzak olanı seçti.
  OpenTelemetry log modelinde "issue" yok, gruplama anahtarı yok, tekilleştirme
  yok: bir kayıt zaman, önem derecesi, gövde ve özniteliklerdir.

- **`gobit recover <execution-id> -confirm <execution-id>`: yarım kalmış bir
  saga artık ELLE telafi edilebiliyor.** v0.8.0 kesintiye uğrayan ödemeyi
  GÖRÜNÜR (`gobit stuck`) ve GERİ ALINABİLİR (kayıtlardan telafi) yapmıştı, ama
  geri almayı yalnızca aynı anahtarla dönen bir çağıran tetikleyebiliyordu. Bu,
  yeniden deneyen müşteriyi kapsar ve başkasını değil: terk edilmiş sepetin
  dönen çağıranı yoktur, kayıt sonsuza dek `running` kalır ve ayırdığı stoğu
  BIRAKACAK KİMSE olmaz. Operatörün elinde üzerinde işlem yapamadığı bir liste
  vardı.

### Değiştirildi

- **`cart`, `order` ve `auth` modüllerinde Türkçe kalmadı** (ADR 0012'nin
  cırcırı). Doksan beş dosya, paket başına bir ajan olmak üzere on sekiz ajanla
  iki aşamada çevrildi; içerik defteri 397 dosyadan **302**'ye, yol defteri 16
  satırdan **9**'a indi.

- **Cırcırın GÖREMEDİĞİ bir borç sınıfı bulundu ve kapatıldı: diyakritiksiz
  Türkçe.** Üç şeritli dedektör Türkçe harfleri, kelime listesini ve AST
  TANIYICILARINI tarıyor; bir yorum ya da dize sabiti içinde `"limit negatif
  olamaz: %d"` gibi tümü ASCII yazılmış Türkçe üç şeridin de dışında kalıyor.
  Sonuç: dedektöre göre TEMİZ olan, dolayısıyla deftere hiç girmeyen, dolayısıyla
  hiçbir ajana atanmayan dosyalar Türkçe taşımaya devam ediyordu.

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

- **`internal/core/workflow` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı).
  Beş turda motorun kendisi, `pgstore` ve ikisinin TÜM test dosyaları çevrildi;
  defter 715 dosyadan **708**'e indi.

- **`internal/core` ağacında Türkçe kalmadı** (ADR 0012'nin cırcırı). Workflow
  turunun ardından gelen dört turda `core/http`'nin kalan dosyaları,
  `redisguard`, `core/query`, `core/openapi` ve `internal/core/config` çevrildi; defter
  708 dosyadan **680**'e indi ve defterde artık `internal/core/` ile başlayan
  TEK BİR satır yok. Kalan borç `internal/modules/*`, `internal/e2e`,
  `internal/arch`'ın kendi testleri ve ADR 0001-0011'de.

- **On beş modülün KIRICI YÜZEYİ İngilizceye geçti** ve sıralamanın kendisi bir
  karardır. Borç ~45 bin satırdı; bütçe ortada biterse geriye kalanın yarısının
  ZARARSIZ olması için önce çatallayan/vendor'layan bir kurulumun DERLENDİĞİ
  yüzey çevrildi:

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
  (bu testin adı 2026-09-09'da
  `TestThePanelSessionReachesTheAdminAPIOnlyUnderTheOriginCheck` oldu: ADR 0030
  uygulanınca çerez artık kabul EDİLİYOR ve bağışıklığın yerini bir savunma
  aldı — test daha sıkı, çünkü bir yokluk tek iddia ister, bir savunma matris). ADR 0011'in taşıyıcı iddiası
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

**Kırıcı değişiklik YOKTUR.** `core/openapi` paketinin dışa açık
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
