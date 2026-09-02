-- Depo seçim POLİTİKASININ şeması.
--
-- 000001 şu sınırla kapanmıştı: kargo modülünün bir LOKASYON MODELİ yoktu.
-- Adaylar stok modülünden geliyordu (bir olgu), seçimi kargo modülü yapıyordu
-- (bir karar) ama karar verecek hiçbir veriye sahip değildi ve kural "kimliği
-- en küçük aday" olarak kalmıştı. Buradaki iki tablo o veriyi getirir.
--
-- Sahiplik: iki tablo da YALNIZCA fulfillment modülüne aittir.
-- shipping_locations.location_id stok modülünün lokasyon kimliğidir ve FK
-- DEĞİLDİR (Prensip 2.2 — cross-module FK yasağı). Yabancı bir kimliği opak ve
-- FK'sız tutmak yeni değildir: shipping_options.region_id de öyle yapar. Yeni
-- olan, o kimliğin BİRİNCİL ANAHTAR olmasıdır; gerekçesi tablonun başındadır.
--
-- Modül ADRES ya da AD KOPYALAMAZ. Deponun nerede olduğu stok modülünün
-- bilgisidir ve orada kalır; burada duran şey yalnızca deponun KARGO
-- niteliğidir: hangi bölgelere hizmet eder ve hangi sırayla tercih edilir.
-- İkinci bir ad/adres kopyası, iki modülde iki doğruluk kaynağı demek olurdu.

-- shipping_locations bir stok lokasyonunun kargo politikasıdır.
--
-- # Satırın YOKLUĞU da bir cevaptır
--
-- Politika satırı olmayan depo geçerli bir depodur: varsayılan öncelikte (0) ve
-- TÜM bölgelere hizmet eder. Bu yüzden tabloda hiç satır yokken seçim, bu
-- migration'dan ÖNCEKİ davranışın tam olarak aynısıdır — eşitliği kimliği en
-- küçük aday bozar. Katı alternatif (politikası olmayan depo aday olamaz)
-- açıldığı gün mevcut kurulumların TÜM siparişlerini durdururdu.
--
-- # BİRİNCİL ANAHTAR YABANCI BİR KİMLİKTİR
--
-- Satırın kimliği stok modülünün ürettiği kimliktir ve depoda bunun EMSALİ
-- YOKTUR: shipping_options.region_id de yabancı ve FK'sızdır ama orada satırın
-- kendi kimliği (id) vardır, region_id bir NİTELİKTİR. Ortak olan şey opaklık
-- ve FK'sızlıktır, anahtar olması değil.
--
-- Yeni kalıp bilinçlidir çünkü buradaki satırın bağımsız bir varlığı yoktur:
-- bir deponun EN FAZLA bir politikası olur ve politika, deposu olmadan hiçbir
-- şey ifade etmez. Ayrı bir id vermek, hiçbir şeyin ihtiyaç duymadığı ikinci
-- bir kimlik üretir ve aynı depo için iki satır yazılmasını mümkün kılardı —
-- engellemek için yine location_id üzerinde bir benzersizlik kısıtı gerekirdi.
--
-- Bedeli: yazma yolu var OLMAYAN bir depo için de satır üretebilir; modül stok
-- modülünü bilmediği için kimliği doğrulayamaz. Böyle bir satır ASLA SEÇİLEMEZ
-- (politika yalnızca stok modülünün ürettiği adayları eler ve sıralar, kümeye
-- eleman ekleyemez) ama yönetim listelemesinde GÖRÜNÜR: ad ve adres taşımadığı
-- için ekranda çözülemeyen opak bir kimlik olarak durur.
--
-- # SİLME YUMUŞAK DEĞİLDİR
--
-- Modülde silme kural olarak yumuşaktır (deleted_at); bu iki tablo, 000001'in
-- başındaki listeye eklenen üçüncü ve dördüncü istisnadır. Gerekçe şudur:
-- buradaki satır OLMUŞ BİR ŞEYİN KAYDI DEĞİL, bir YAPILANDIRMADIR. Yumuşak silinmiş bir
-- politika satırının etkisi, hiç var olmamış bir satırınkiyle BİREBİR AYNIDIR
-- (ikisi de "varsayılan" demektir), yani ayrımın taşıyacağı bir anlam yoktur.
-- Dahası zararlıdır: location_id birincil anahtar olduğu için yumuşak silinmiş
-- bir satır, aynı depo için yeni politika yazılmasını engellerdi ve her yazma
-- yolu "önce dirilt" adımını öğrenmek zorunda kalırdı.
--
-- # priority: KÜÇÜK OLAN KAZANIR, negatif SERBESTTİR
--
-- 0 varsayılandır ve politika satırı OLMAYAN depoyla aynı sıradadır. Negatife
-- izin verilmesinin somut sebebi var: üç depodan birini öne almak isteyen
-- operatör, tek satır yazıp o depoya -1 verebilmelidir. Yalnızca negatif
-- olmayan değerlere izin verilseydi aynı iş, öne almak İSTEMEDİĞİ diğer iki
-- depoya satır yazmayı gerektirirdi.
CREATE TABLE IF NOT EXISTS shipping_locations (
    location_id TEXT        PRIMARY KEY,
    priority    BIGINT      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT shipping_locations_location_check CHECK (location_id <> '')
);

CREATE INDEX IF NOT EXISTS shipping_locations_priority_idx
    ON shipping_locations (priority, location_id);

-- shipping_location_regions deponun HİZMET ETTİĞİ kargo bölgeleridir.
--
-- region_id region modülünün kimliğidir ve FK DEĞİLDİR (Prensip 2.2), tıpkı
-- shipping_options.region_id gibi. location_id ise MODÜL İÇİ bir FK'dır ve
-- serbesttir; politika satırı silinince bölgeleri de düşer (ON DELETE CASCADE),
-- çünkü sahipsiz bir bölge bağı hiçbir kararı etkileyemez.
--
-- # Bağı OLMAYAN depo TÜM bölgelere hizmet eder
--
-- Kural, satış kanalı kapsamının kuralıyla aynıdır ("kanal ataması olmayan ürün
-- tüm kanallarda görünür") ve aynı tuzağı taşır: bir deponun SON bölge bağını
-- silmek onu kapatmaz, TÜM bölgelere açar. Kapatmak için depo stok modülünde
-- silinir ya da stoğu sıfırlanır — kargo politikası bir depoyu var/yok yapamaz,
-- yalnızca adaylar arasında eler ve sıralar.
--
-- Tuzağın bilinçli olmasının sebebi tabloya girmeden önceki hâlle aynı:
-- boş kümeyi "hiçbir bölgeye hizmet etmez" saymak, bugün politikası olmayan her
-- kurulumu sipariş veremez hâle getirirdi.
--
-- # TERS YÖNDEKİ TUZAK DAHA AĞIRDIR
--
-- Satış kanalı kuralıyla asimetri buradadır: orada yanlış bir kapsam ürünü
-- GİZLER, burada siparişi DÜŞÜRÜR. Var olmayan bir bölge kimliği bağlamak — ya
-- da bir bölgeyi silip aynı adla yeniden açmak, çünkü yeni kayıt yeni bir kimlik
-- alır — o deponun her sepette elenmesi demektir; tek depolu bir kurulumda
-- sonucu, katalog dolu olduğu hâlde her tamamlamanın reddedilmesidir.
--
-- Bağ bu yüzden bir TERCİH DEĞİL, bir KISITTIR. "İstanbul'u tercih et ama
-- tükenirse Ankara'dan gönder" bölge bağıyla YAZILMAZ, öncelikle yazılır:
-- ikisine de bağ verilmez, İstanbul'a küçük bir priority verilir. Bölge bağı
-- yalnızca "bu depo oraya gönderemez" için doğrudur.
--
-- Arıza görünürdür ama görünürlüğün SINIRI vardır: eleme sonucu boş kalınca
-- kargo modülü KENDİ hata kodunu döner ve o kod vitrine kadar ulaşır. Adayların
-- gerçekte hangi bölgelere bağlı olduğunu yazan döküm ise SUNUCU LOGUNDA ve
-- yürütme kaydındadır — ölü bir kimlik ancak oraya bakan operatör tarafından
-- teşhis edilebilir.
CREATE TABLE IF NOT EXISTS shipping_location_regions (
    location_id TEXT        NOT NULL REFERENCES shipping_locations (location_id) ON DELETE CASCADE,
    region_id   TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (location_id, region_id),
    CONSTRAINT shipping_location_regions_region_check CHECK (region_id <> '')
);

-- Bölgeden depoya doğru ("bu bölgeye hangi depolar hizmet eder") bir okuma
-- BUGÜN YOKTUR ve o yön İNDEKSLENMEDİ. Birincil anahtar (location_id,
-- region_id) yönünü zaten indeksler ve tüm sorgular o yönden okur; kullanıcısı
-- olmayan bir indeks, her yazmaya bedel bindirir ve okuyucuya var olmayan bir
-- kullanım anlatır. Gerektiğinde eklenir.
