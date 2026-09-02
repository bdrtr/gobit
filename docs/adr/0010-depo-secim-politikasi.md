# ADR 0010 — Depo seçimi: kapsam bir KISIT, tercih bir SIRA

- **Durum:** Kabul edildi
- **Tarih:** 2026-09-02
- **Faz:** 10 sonrası (çoklu-depo turu)

## Bağlam

Çoklu depo Faz 6'dan beri destekleniyor: bir siparişin satırları farklı
depolardan ayrılabiliyor. Dikiş iki modüle bölünmüş durumda ve bölünme
bilinçliydi — "hangi depolarda yeterli stok var" bir **olgu** (stok modülü),
"hangisinden gönderelim" bir **karar** (kargo modülü).

Kararın bir sorunu vardı ve `README.md` onu bilinen sınırlar arasında yazıyordu:

> **Depo seçimi bir POLİTİKA taşımaz.** Çoklu depo desteklenir (adaylar stok
> olgusundan gelir, seçimi kargo modülü yapar) ama bugünkü kural "kimliği en
> küçük aday"dır: yakınlık, maliyet ve stok dağılımı İFADE EDİLEMEZ, çünkü
> modülün bir lokasyon modeli yoktur.

Yani karar doğru yerdeydi ama karar verecek veri yoktu. İşletmeci "önce
İstanbul deposundan gönder" ya da "Avrupa siparişlerini Almanya deposundan
çıkar" diyemiyordu; sonucu kimliklerin sözlüksel sırası belirliyordu.

Bu ADR o veriyi getiren kararı ve onunla birlikte gelen üç yeni tuzağı kayda
geçiriyor.

## Karar

### 1. Lokasyon modeli fulfillment'ın KENDİ şemasındadır

İki tablo eklendi (`shipping_locations`, `shipping_location_regions`). Depo
kimliği stok modülünündür, **opaktır ve foreign key değildir** (Prensip 2.2).
Yabancı bir kimliği böyle tutmak yeni değil — `shipping_options.region_id` de
öyle — ama o kimliğin **birincil anahtar** olması yeni bir kalıptır ve
gerekçesi migration'ın başında durur: politika satırının deposundan bağımsız bir
varlığı yoktur.

Modül **ad ve adres kopyalamaz**. Deponun nerede olduğu stok modülünün
verisidir; burada duran şey yalnızca kargo niteliğidir.

### 2. Politikanın girdisi sepetin BÖLGESİDİR

`checkoutPlan` zaten bölge taşıyordu. Bu yüzden:

- Vitrin sözleşmesi (`POST /store/v1/carts/{id}/complete`) **değişmedi** —
  müşteriye depo seçtirme yasağı korundu.
- Yürütme kaydına **kişisel veri girmedi**; teslimat adresi bilinçli olarak
  taşınmadı (plan Bölüm 8).
- Sepet anlık görüntüsü değişmedi.

"Yakınlık" bu sistemde coğrafi mesafe **değil**, kargo bölgesi kapsamıdır.
Depoların koordinatı yoktur ve uydurulmadı.

### 3. Yüzey tek lokasyon değil TERCİH SIRASI döner

`SelectLocation(ctx, candidates) (string, error)` kaldırıldı; yerine
`RankLocations(ctx, destinationRegionID, candidates) ([]string, error)` geldi.
İki değişiklik birden var ve ikisinin de ayrı sebebi var.

**Bölge parametresi**, godoc'ta yazılı bir taahhüdü kırıyor: eski metin
"politika bu metodun İÇİNDE zenginleşir; çağıranın gördüğü imza değişmez"
diyordu. Taahhüt yanlıştı ve nerede yanlış olduğu ölçülebilir: eksik olan
yalnızca deponun kendisi değil, gönderinin NEREYE gittiğiydi. İkincisi modülün
içinde zenginleşmeyle elde edilemez — çağıranın elindedir.

**Sıra dönmesi** bir maliyet kararıdır. Çağıran, tükenen bir depodan sonra
sıradakini dener; eski yüzeyle bu, her tükenişte politikanın yeniden
sorulması ve aynı politika kayıtlarının yeniden okunması demekti — N adaylı bir
satır için bir sorgu yerine N sorgu. Sıra deterministik olduğu için o N-1 çağrı
zaten aynı cevabı üretiyordu.

Yan kazanç: sepet akışının döngüsünün sonlanması artık modülün ne döndüğünden
bağımsızdır. Eskiden sonlanma, seçilen adayın listeden düşürülebilmesine — yani
modülün aday kümesinin dışına çıkmamasına — bağlıydı; şimdi sonlu bir dilimin
uzunluğuyla sınırlıdır.

### 4. Kural: ELE, SIRALA, EŞİTLİĞİ BOZ

1. **Eleme** — bir depoya en az bir bölge bağlanmışsa ve hedef bölge onların
   arasında değilse aday düşer. Hiç bağı olmayan depo **tüm** bölgelere hizmet
   eder.
2. **Sıralama** — kalanlar önceliğe göre dizilir; küçük olan öne geçer. Kaydı
   olmayan depo sıfır önceliktedir.
3. **Eşitlik bozma** — eşit öncelikte kimliği küçük olan öne geçer.

Politika kaydı hiç yoksa sonuç tek başına üçüncü adımdır: **seçilen depo** bu
değişiklikten önceki davranışın aynısıdır. Katı alternatif (politikası olmayan
depo aday olamaz) açıldığı gün mevcut kurulumların tüm siparişlerini durdururdu.

Aynısı kalmayan iki şey "Sonuçlar"da yazılıdır ve ikisi de kayıtsız kurulumu da
etkiler: satır başına bir SQL sorgusu ve stok ayırma hatalarının kodu.

### 5. Eleme boş küme üretirse sınıf Conflict, kod AYRIDIR

Yeni kod `fulfillment_no_serviceable_location`; "hiç aday yok" hâlinin kodundan
ayrıdır çünkü işletmecinin yapacağı iş de ayrıdır — birinde stok yoktur,
diğerinde bölge kapsamı yanlış kurulmuştur.

Sınıfın Conflict olmasının gerekçesi çağıranın dallanması **değildir**: sepet
akışı seçim hatasını sınıfına bakmadan yukarı verir. Gerçek dayanak ikilidir ve
ikisi de ölçülebilir:

- Sepet akışı adım hatasını sararken **sınıfı devralır** ve HTTP durumu oradan
  gelir. Invalid seçilseydi dünyanın durumundan kaynaklanan bir arıza, müşteriye
  "gövdeni düzelt" diyen 422 olurdu.
- Motorun varsayılan yeniden deneme yüklemi `KindConflict`'i **denemez**,
  `KindInternal`'ı **dener**. Internal seçilseydi, telafi yeniden denemesi
  açıldığı gün işletmecinin elle düzeltmesi gereken bir yapılandırma hatası
  geçici arıza sanılıp tekrarlanırdı.

### 6. Sepet akışı alt hatanın KODUNU korur

Adım hatasını saran yer, kodu kendi sabitiyle eziyordu; artık alt hatanın kodu
devralınır ve `checkout_workflow_reservation_failed` yalnızca kodsuz bir hata
için yedektir.

Bu, bu turda alınmış ikinci bir karardır ve bu özelliğin **ön koşuludur**.
Taşıma katmanı gövdeye tek bir makine okunur alan yazar; kod ezilseydi yanlış
kurulmuş bir bölge bağı, dolu raflarla "stok ayrılamadı" diye raporlanır ve
operatör bakması gereken yeri bulamazdı. Kalıp yeni değildir: motor aynı hatayı
bir tur önce kendi sarmalamasında düzeltmişti ve gerekçesi orada, B2B harcama
limitiyle ölçülmüş hâlde yazılıdır.

### 7. Bildirilen lokasyon yolu DEĞİŞMEDİ

Çağıran lokasyon bildirirse politika hiç çalışmaz ve hiçbir modüle sorulmaz.
Bildirilen lokasyon bir tercih değil talimattır.

## Sonuçlar

**Olumlu**

- İşletmeci tercih sırasını ve hizmet kapsamını ifade edebilir; sonucu
  kimliklerin sözlüksel sırası belirlemez.
- Politika okuması satır başına tek sorgudur ve sıra bir kez hesaplanır.
- Eleme yüzünden düşen bir sipariş, stok yetersizliğinden **ayırt edilebilir**.
  Ayrımı taşıyan şey KODDUR ve vitrine ulaşan tek şey odur; mesaj her üç
  durumda da aynıdır çünkü taşıma katmanı gövdeye yalnızca en dıştaki mesajı
  yazar. Adayların bölge dökümünü içeren metin **sunucu logunda ve yürütme
  kaydındadır**, yani okuyucusu operatördür.
- Geriye uyumluluk kayıtsız kurulumlarda SEÇİLEN DEPO için tamdır ve testle
  sabitlenmiştir. Hata KODU için değildir (bkz. Karar 6) ve satır başına bir
  SQL sorgusu eklenmiştir — eski seçim saf bir fonksiyondu ve veritabanına hiç
  dokunmuyordu, yani kayıtsız bir kurulum bile artık bu yolda bir arıza
  görebilir.

**Olumsuz — kabul edilen bedeller**

- **Yanlış bir bölge bağı mağazayı kapatır.** Var olmayan bir bölge kimliği
  bağlamak (ya da bir bölgeyi silip aynı adla yeniden açmak — yeni kayıt yeni
  kimlik alır) o depoyu her sepette eler. Tek depolu bir kurulumda sonucu, dolu
  bir katalogla her tamamlamanın reddedilmesidir.

  Bedelin ağırlığı burada bitmiyor: tamamlama akışının idempotency anahtarı
  sepet kimliğinden türer ve başarısız bir yürütme aynı anahtarla tekrar
  koşamaz. Yani eleme yüzünden düşen sepet **kalıcı olarak** tükenir; müşteri
  yeni bir sepet açmak zorundadır. Bu yakma bugün de vardı ama tetikleyicisi bir
  stok olgusuydu; artık tek bir yönetim yazması da tetikleyebiliyor.

  Karşılığı: arıza görünürdür ve geri dönüşü tek bir yönetim yazmasıdır. Ama
  görünürlüğün SINIRI da yazılmalı: vitrin istemcisi yalnızca kodu görür
  (`fulfillment_no_serviceable_location`), adayların bölge dökümünü içeren
  mesaj sunucu logunda ve yürütme kaydında kalır. Yani geri dönüş yolu,
  operatörün loga ya da yürütme kaydına erişebilmesine bağlıdır.

- **Bağ bir TERCİH değil KISITTIR ve geri düşme kümesini daraltır.** İki depoyu
  ayrı bölgelere bağlayan bir işletmeci, ilk deponun stoğu yarışta tükendiğinde
  siparişin düşmesini kabul etmiş olur — oysa politika olmadan o sipariş
  diğerinden çıkardı. "İstanbul'u tercih et ama tükenirse Ankara'dan gönder"
  bölge bağıyla yazılmaz, **öncelikle** yazılır. Bölge bağı yalnızca "bu depo
  oraya gönderemez" için doğrudur.

- **Son bağı silmek depoyu gizlemez, tüm bölgelere açar.** Kural satış kanalı
  kapsamınınkiyle aynıdır ve aynı tuzağı taşır; asimetri şudur: orada yanlış bir
  kapsam ürünü gizler, burada siparişi düşürür.

- **Yönetim listelemesi yetim satır gösterebilir.** Stok modülünde silinmiş bir
  depo için kalan politika satırı asla **seçilemez** ama listede **görünür**;
  ad ve adres taşımadığı için ekranda çözülemeyen opak bir kimlik olarak durur.

- **`fulfillment:write` artık sipariş yolunu durdurabilir.** Yetki sözlüğü
  değişmedi; gerekçesi "Reddedilen seçenekler"dedir.

- **Kırıcı değişiklik.** `fulfillment.interop` yüzeyinin bir metodu adıyla ve
  imzasıyla değişti; gömülü kullanan kodu etkiler. Derleyicinin denetlemediği
  tek dikiş, arayüzün container'dan **adla** çözüldüğü yerdir ve onun kanıtı
  yalnızca uçtan uca testtir.

## İFADE EDİLEMEYENLER

Yüzeyin ne garanti etmediği, garanti ettiği kadar önemlidir:

- **Stok dağılımı.** "En çok stoğu olan depoyu öne al" yazılamaz. İki sebebi
  var: lokasyon kırılımında satılabilir adet stok modülünün ilkel yüzeyinde
  yoktur ve o yüzeye eklemek, mağazaya lokasyon kırılımı sızdırmama kararıyla
  temas eder; ikincisi ve daha ağırı, determinizmin dayanağını değiştirir —
  politika **işletmecinin ayarıdır** ve değişmesi beklenen bir sonuçtur, oysa
  stok hızlı değişen bir olgudur ve aynı savunma orada çalışmaz.
- **Maliyet.** Depo ile taşıyıcı arasında bir tarife modeli yoktur; yazılsaydı
  dayandığı veri uydurma olurdu.
- **Sipariş düzeyinde karar.** Sıra satır başına sorulur ve yüzey sepetin
  tamamını görmez; "tüm satırları tek depodan çıkar" ya da "gönderi sayısını
  azalt" ifade edilemez.
- **(depo, bölge) çifti başına tercih.** Öncelik **depo** başınadır. "R1 için
  önce A, R2 için önce B" yazılamaz; bölge başına yazılabilen tek şey
  dışlamadır.

## Reddedilen seçenekler

**Lokasyon detayını stok modülünün ilkel yüzeyine eklemek.** En az kod isteyen
yol buydu ve tek doğruluk kaynağını korurdu. Reddedildi çünkü o yüzeyin godoc'u
kapıyı yazılı olarak kapatmış durumda: "hangi depodan gönderelim" sorusunu
taşımaz, o bir kargo kararıdır. Kapıyı açmak, stok sorgusunu kargo politikasına
bağımlı kılardı — bu ADR'nin korumaya çalıştığı bölünmenin ta kendisi.

**Depoyu Query katmanına ikinci bir entity olarak açmak.** Okuma yolu tek
noktadan geçerdi ve süzme bedava gelirdi. Reddedildi çünkü stok modülünün
yönetim yüzeyi "mağazaya lokasyon kırılımı sızmaz" sınırını yazılı olarak
koyuyor ve ikinci bir entity, o sınırla temas eden yeni bir okuma yolu açardı.

**Bölge bağını sıralama anahtarı yapmak, katı kesiği bir bayrağın arkasına
almak.** Hizmet eden depolar öne, etmeyenler sona dizilirdi; "hizmet eden depo
yok" hatası stok varken asla oluşmazdı. Reddedildi çünkü kavramı bozar:
"hizmet ettiği bölgeler" bir tercih değil, taşıyıcının kapsama alanıdır ve
kapsam dışına göndermek graceful bir geri düşüş değil, imkânsız bir gönderidir.
Tercih zaten ifade edilebiliyor — öncelikle. İki kavramı tek alana yüklemek,
işletmeciye hangisini yazdığını sormaz hâle getirirdi. Bedeli "Olumsuz"da
adıyla yazılıdır.

**Politika yazmaya üçüncü bir yetki (`fulfillment:policy`) vermek.** Bu ucun
etki alanı modüldeki diğer yazma uçlarından gerçekten geniştir. Reddedildi
çünkü yetki dağarcığı tek bir kuraldan türer (`<modül>:read` / `<modül>:write`,
`admin` üst yetki) ve yüzlerce yönetim ucu bu kuralla denetlenir. Tek bir uca
özel bir ad, kuralı öğrenilemez ve denetlenemez kılardı; kazanç ise sınırlı
olurdu, çünkü `fulfillment:write` taşıyan kimlik zaten bir yönetim kimliğidir.

**Politikayı kapatan bir ortam değişkeni.** Depoda bir emsal aranabilir ama
bulunan şey tam olarak bu değildir: b2b modülünü kapatan bir anahtarın neden
EKLENMEDİĞİ `CHANGELOG.md`'de yazılıdır ("yanlışlıkla `false` verilen bir
anahtar harcama limitini hiçbir hata üretmeden kaldırırdı") ve o gerekçe buraya
**doğrudan taşınamaz**: oradaki bayrak bir korumayı fail-open yapardı, buradaki
ise sipariş düşüren bir kuralı kaldırırdı — ters yön. ADR 0007 bir bayraktan
değil, arızada DAVRANIŞTAN söz eder; ADR 0009'da konu hiç geçmez.

Bayrak yine de eklenmedi ve sebebi kendi ölçütünden gelir: geri dönüş yolu
zaten yönetim API'sindedir ve arıza kendi kodunu taşır.
Bir bayrak, aynı işi yapan ikinci bir yol açar ve iki yolun hangisinin geçerli
olduğu bir sonraki turda sorulur. **Bu reddin ön koşulu, "Karar 6"dır**: sebebi
görünmeyen bir arıza için "yönetim ucundan geri alınır" demek boş bir söz
olurdu.

**Yumuşak silme.** Modülün kuralı yumuşak silmedir. Reddedildi çünkü yumuşak
silinmiş bir politika satırının etkisi, hiç var olmamış bir satırınkiyle birebir
aynıdır (ikisi de "varsayılan" demektir) ve ayrımın taşıyacağı bir anlam yoktur;
dahası birincil anahtar depo kimliği olduğu için ölü bir satır, aynı depo için
yeni politika yazılmasını engellerdi.

## Kararın yeniden açılması

Üç veri bu kararı yeniden açar:

1. **Depolara koordinat gelirse** yakınlık gerçekten hesaplanabilir hâle gelir
   ve "kapsam" ile "mesafe" ayrı iki kural olur. Bugünkü eleme o gün bir
   sıralama girdisine dönüşebilir.
2. **Lokasyon kırılımında satılabilir adet stok modülünün yüzeyine girerse**
   stok dağılımı ifade edilebilir olur; o gün cevaplanacak soru determinizmin
   ne anlama geleceğidir.
3. **"Yanlış bağ mağazayı kapattı" olayı gerçekten yaşanırsa** — ölçüsü,
   `fulfillment_no_serviceable_location` kodunun üretimde görülmesidir — yazma
   yolunda bölge kimliğini doğrulatmak (region modülünün yüzeyine sormak)
   yeniden değerlendirilir. Bugün yapılmadı çünkü modülün hiçbir yerde başka bir
   modüle sormayan yapısını tek bir doğrulama için bozmak, bedelini kendisi
   ödetmeyen bir karardır.

## İlgili

- [ADR 0001](0001-modul-arasi-iletisim.md) — dar arayüz + adla çözüm; bu ADR'nin
  imza değişikliğinin derleyicisiz kaldığı yer.
- [ADR 0004](0004-query-veri-erisimi.md) — reddedilen ikinci seçeneğin dayanağı.
- [ADR 0006](0006-workflow-modul-erisimi.md) — sepet akışının modüllere nasıl
  eriştiği.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — arızada davranışın tek tip
  olmadığı kararı. Ortam değişkeni reddi oradan TÜRETİLMEZ; ADR 0007 bayraklardan
  söz etmez, yalnızca bileşen başına arıza davranışını karara bağlar.
- [ADR 0009](0009-cok-kiracililik-kurulum-siniri.md) — kurulum sınırı kararı; bu
  ADR'nin "her kurulum tek kiracılıdır" varsayımı depo politikasının da
  kurulum düzeyinde yaşamasını mümkün kılar.
