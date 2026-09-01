package graph

import (
	"context"
	"fmt"

	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// GraphQL ucunun REST'ten FARKLI riski ve onu kapatan varsayılan sınırlar.
//
// REST'te bir isteğin maliyetini SUNUCU belirler: yol sabittir, dönen gövde
// sabittir, bir istek bir sorgudur. GraphQL'de maliyeti İSTEMCİ belirler —
// sorgunun şeklini o yazar. Hız sınırlayıcı ise her iki yüzeyde de aynı şeyi
// sayar: BİR istek. Aynı kotayla bin kat iş yaptırmanın yolları ayrı ayrı
// kapılarla kapatılır:
//
//  1. AÇILIM — fragment'lar açıldıktan sonraki ağacın büyüklüğü
//     ([DefaultMaxSelections]). Diğer kapılardan ÖNCE koşar ve onları korur.
//  2. DERİNLİK — iç içe geçen alanlar ([DefaultMaxDepth]).
//  3. GENİŞLİK/ÇARPAN — sığ ama pahalı sorgu ([DefaultMaxComplexity]).
//  4. TEKRAR — aynı alanın aynı nesne altında yığılması
//     ([DefaultMaxFieldRepetition]).
//  5. İÇ GÖZLEM — 2 ve 3'ün göremediği __schema/__type ağacı
//     ([DefaultMaxIntrospectionRoots], [DefaultMaxIntrospectionDepth]).
//  6. AYRIŞTIRMA — belgenin kendisinin boyutu ([maxSorguBayt],
//     [maxSorguJeton]; handler.go).
//  7. ÇIKTI — yanıtın GERÇEKLEŞEN baytı ([DefaultMaxResponseBytes];
//     handler.go).
//
// Hepsi ayrı ayrı gereklidir çünkü her biri ötekinin göremediği bir belgeyi
// yakalar ve bu bir tahmin değil, ÖLÇÜMDÜR:
//
//   - Derinliği 3 olan bir belge takma adlarla yüzlerce kök sorgu taşıyabilir;
//     karmaşıklık yakalar.
//   - Karmaşıklığı düşük bir belge döngüsel bir alanda sonsuza inebilir;
//     derinlik yakalar.
//   - İkisi de belge ancak AYRIŞTIRILDIKTAN sonra ölçülebildiği için 10
//     MiB'lık bir gövdenin ayrıştırma maliyetini geri veremez; gövde ve jeton
//     sınırları yakalar.
//
// # Neden alan sayısı yetmiyor: BAYT
//
// Karmaşıklık modeli çözülecek ALAN SAYISINI fiyatlar, BAYT'ı değil. Bir
// alanın yanıttaki ağırlığı ise sayısıyla değil İÇERİĞİYLE belirlenir ve
// takma adlar aynı alanı sınırsız kez seçmeye izin verir:
//
//	products(limit: 100) { items { a0: description … a488: description } }
//
// Bu belgenin ölçülen maliyeti 50.000'dir, yani tavana TAM oturur — aşmadığı
// için eski kapılardan geçerdi. Ölçüldü: 8.729 baytlık istek 204,9 MiB yanıt
// üretiyordu (24.620 kat) ve hız sınırlayıcı bunu BİR istek sayıyordu.
// Varsayılan sayfayla (20 ürün) 1500 takma ad, 27.415 bayttan 125,7 MiB
// üretiyordu.
//
// Buradaki asimetri REST'te YOKTUR ve "sınır REST'ten katı değildir" gerekçesi
// tam olarak burada kopar: REST istemcisi aynı alanı 489 kez isteyemez,
// GET /store/v1/products?limit=100 yanıtı aynı veriyle ~450 KiB'dır. Yani
// GraphQL'in eklediği şey daha çok kayıt değil, AYNI kaydın tekrar tekrar
// serileştirilmesidir; bunu ancak [alanTekrariSiniri] (tahminle, çalıştırmadan
// önce) ve yanıt bayt sınırı (gerçekleşenle, yazarken) birlikte kapatır.
//
// # Neden iç gözlemin AYRI kapısı var
//
// İç gözlem ağacı bir zamanlar her iki hesabın da dışındaydı: derinlik sayımı
// __schema/__type köklerini atlıyordu, gqlgen'in karmaşıklık yürüyüşü de
// __Schema tipli alanı atlar (complexity/complexity.go). Yani ölçülen derinlik
// 0, ölçülen karmaşıklık 0'dı ve operatörün elinde kapatacak bir ayar yoktu.
//
// Ölçüldü: 302 takma adlı bir __schema belgesi (45.796 bayt) 5,00 MiB yanıt
// üretiyor ve Options{MaxDepth: 1, MaxComplexity: 1} ile AYNI belge yine 200 ve
// yine 5,00 MiB dönüyordu — aynı ayarla "products { count }" sorgusu "depth 2
// exceeds the limit of 1" ile reddedilirken. En küçük meşru veri sorgusu
// reddedilirken 5 MiB'lık iç gözlem seli geçiyordu.
//
// Bugün iç gözlem SAYILIR ama kendi tavanına göre sayılır
// ([DefaultMaxIntrospectionDepth]) ve kök sayısı ayrıca sınırlanır
// ([DefaultMaxIntrospectionRoots]). Ayrı tavan şart: standart iç gözlem sorgusu
// 13 seviye derindir (ofType zinciri), tek bir tavan kullanılsaydı VERİ
// yüzeyinin sınırını 13'ün üstüne çıkarmak zorunda kalırdık.
//
// Sabitler DIŞA AÇIKTIR ki çekirdeğin yapılandırmasındaki envDefault
// etiketleriyle uyumları bir testle sabitlenebilsin (bkz. internal/arch):
// çekirdek modülleri import EDEMEDİĞİ için (Prensip 2.4) config bu sabitlere
// bağlanamaz, değerlerini elle tekrarlar. Ayrışırlarsa gömülü bir kurulum
// (product tek başına da dağıtılabilir) belgede yazandan başka bir sınırla
// çalışırdı. Bugün config yalnızca derinliği, karmaşıklığı ve iç gözlem
// anahtarını okur; yeni kapıların ortam değişkenleri çekirdek tarafında ayrı
// bir değişikliktir ve sabitlerin dışa açık olması, o değişikliğin bağlanacağı
// yeri şimdiden belirler.
const (
	// DefaultMaxDepth tek bir belgede iç içe geçebilecek alan sayısının
	// varsayılan üst sınırıdır.
	//
	// Şemanın bugünkü en derin MEŞRU yolu 5'tir
	// (products → items → variants → optionValues → optionTitle), yani 10 iki
	// kat boşluk bırakır. Daha cömert bir varsayılan seçilmedi: sınırın var
	// olma sebebi bugünün şeması değil, YARININ şemasıdır — bir alan geri
	// referans verdiği an (variant → product → variants → …) sorgu şemanın
	// izin verdiği yere kadar değil, istemcinin yazdığı yere kadar iner ve
	// her seviye maliyeti çarpar.
	//
	// Sınır yalnızca VERİ ağacına uygulanır; iç gözlemin kendi tavanı vardır
	// ([DefaultMaxIntrospectionDepth]).
	DefaultMaxDepth = 10

	// DefaultMaxComplexity tek bir belgenin tahmini maliyet tavanıdır.
	//
	// Birim "kaç alan çözülür"dür; liste alanlarında ELEMAN SAYISIYLA çarpılır
	// ve kök sorgular ayrıca bir veritabanı gidiş-dönüşü sayılır
	// (bkz. [karmasiklikMaliyetleri], [kokSorguMaliyeti]).
	//
	// Değer TAHMİN EDİLMEDİ, ölçüldü; belgeler ve sayıları
	// graph/limits_test.go içindeki kalibrasyon tablosunda SABİTLENMİŞTİR
	// (bkz. kalibrasyonBelgeleri). Bayt sütunu, aynı dosyadaki ölçüm
	// fikstürüyle (4 KiB açıklamalı ürün) alınmıştır:
	//
	//	belge                                      istek  karmaşıklık     yanıt
	//	ürün sayfası (PDP, her şey dâhil)           643 B        2.368   6,8 KiB
	//	kategori listesi (24 ürün, kart + fiyat)    118 B        2.344  15,1 KiB
	//	varsayılan sayfada TÜM alanlar (20 ürün)    655 B       28.440   136 KiB
	//	limit=100 ile TÜM alanlar                   667 B      138.200   680 KiB
	//	400 takma adlı products { count }          9,7 KiB     408.000   8,5 KiB
	//	489 takma adlı description (limit=100)     8,5 KiB      50.000 204,9 MiB
	//	1500 takma adlı description (20 ürün)     26,8 KiB      31.020 125,7 MiB
	//
	// Son iki satırın yanıt sütunu, kapılar eklenmeden ÖNCE ölçülmüştür;
	// bugün ikisi de çalıştırılmıyor. Üstlerindeki "limit=100 ile TÜM alanlar"
	// satırı ise karşılaştırma noktasıdır: aynı sayfayı REST'ten çekmek de
	// aynı mertebede (680 KiB) bir gövde demektir. Yani 204,9 MiB'ı üreten şey
	// daha çok KAYIT değil, aynı kaydın tekrar tekrar serileştirilmesidir.
	//
	// 50.000, en ağır meşru belgeye (28.440) rahat bir pay bırakır: şemaya
	// alan eklendiğinde o sorgu sınıra dayanmaz. Daha dar bir tavan bugünü
	// kurtarır, yarın alan ekleyen kişiyi bir ayar değişikliğine zorlardı.
	//
	// Tablonun son iki satırı tavanın NE ÖLÇMEDİĞİNİ gösterir ve tam da bu
	// yüzden eklendi: 489 takma adlı belge 50.000'e TAM oturur (tavan
	// aşılmadığı için geçerdi) ve 204,9 MiB yanıt üretir. Maliyeti tahmin
	// eden bir modelin bilemeyeceği tek şey alanın İÇERİĞİDİR; boşluğu
	// [DefaultMaxFieldRepetition] ve [DefaultMaxResponseBytes] kapatır.
	DefaultMaxComplexity = 50000

	// DefaultMaxFieldRepetition aynı alanın aynı nesne altında kaç kez
	// seçilebileceğinin varsayılan üst sınırıdır.
	//
	// Sayım KARDEŞ kapsamlıdır: bir seçim kümesindeki (nesne, alan) çiftleri
	// sayılır, takma adlar YOK SAYILIR. "a0: description a1: description …"
	// aynı çifti tekrarlar; "ofType { ofType { … } }" ise her seviyede tek bir
	// seçimdir ve tekrar sayılmaz. Ayrım önemlidir: sayım belge geneli olsaydı
	// standart iç gözlem sorgusu (TypeRef fragment'ı __Type.ofType'ı onlarca
	// kez taşır) reddedilirdi — ölçüldü, kardeş kapsamda en yüksek tekrar 1'dir.
	//
	// 20 bir ölçüm değil, meşru kullanımın ÜSTÜNDEKİ ilk rahat sayıdır: bir
	// ana sayfa aynı kök sorguyu birkaç vitrin şeridi için takma adla
	// tekrarlar (öne çıkanlar, yeniler, indirimdekiler…) ve bu bir elin
	// parmaklarını geçmez. Aynı ürün altında AYNI alanı ikiden fazla istemenin
	// meşru bir sebebi ise yoktur. Ölçülen saldırılar 489, 1500, 302 ve 448
	// tekrarlıydı; sınır ile meşru kullanım arasında bir mertebe fark vardır.
	DefaultMaxFieldRepetition = 20

	// DefaultMaxIntrospectionRoots bir belgedeki iç gözlem KÖKÜ sayısının
	// varsayılan üst sınırıdır.
	//
	// Kök, belgenin tepesindeki __schema ya da __type alanıdır. 2 seçildi
	// çünkü hiçbir araç aynı belgede iki kez __schema istemez; isteyen
	// araçlar (şema tarayıcıları) en fazla bir __schema ile bir __type
	// gönderir. Ölçülen sel 302 kökle geliyordu.
	//
	// Kapı [DefaultMaxFieldRepetition] ile ÖRTÜŞÜR ama gereksiz değildir:
	// tekrar sınırı 20 köke kadar izin verirdi ve 20 kök, ölçülen 5,00 MiB'ın
	// on beşte biri kadar yanıt demektir. İç gözlem tek bir istekte yüzeyin
	// TAMAMINI verdiği için burada daha dar bir sayı doğrudur.
	DefaultMaxIntrospectionRoots = 2

	// DefaultMaxIntrospectionDepth iç gözlem alt ağacının varsayılan derinlik
	// tavanıdır.
	//
	// Veri tavanından ayrıdır ve ondan yüksektir çünkü ölçüldü: istemci
	// araçlarının gönderdiği standart iç gözlem sorgusu (gqlgen'in
	// introspection.Query'si) 13 seviye derindir — ofType zinciri tip
	// sarmalayıcılarını (NonNull, List) açmak için o kadar iner. Tek bir tavan
	// kullanılsaydı VERİ yüzeyinin sınırını da 13'ün üstüne çıkarmak zorunda
	// kalırdık ve gevşeme asıl korumak istediğimiz yerde olurdu.
	//
	// 15, o sorguya iki seviye pay bırakır. Alt ağacın ayrıca bizden bağımsız
	// bir tavanı daha vardır: gqlparser'ın MaxIntrospectionDepth kuralı iç içe
	// __Type listelerini (fields, interfaces, possibleTypes, inputFields) üç
	// seviyede keser.
	DefaultMaxIntrospectionDepth = 15

	// DefaultMaxResponseBytes tek bir yanıtın istemciye yazılabilecek en fazla
	// bayt sayısıdır.
	//
	// Bu kapı ötekilerden farklı bir soru sorar: hepsi belgeye bakıp maliyeti
	// TAHMİN ederken bu, yazılan baytı SAYAR. Tahmin ne kadar iyi olursa olsun
	// alanın içeriğini bilemez — açıklaması 40 KiB olan bir katalog ile 400
	// bayt olanı aynı fiyatlar. Gerçekleşene bakan bir kapı olmadan üst sınır
	// yoktur.
	//
	// 4 MiB ölçüme dayanır: bugünkü tavanlardan geçen EN AĞIR meşru yanıt
	// (varsayılan sayfa × tüm alanlar, açıklaması 4 KiB'lık ürünlerle) 136
	// KiB'dır, yani sınır yaklaşık 30 kat pay bırakır — uzun açıklamalı,
	// zengin metadata'lı bir katalog rahatça altında kalır. Ölçülen saldırı ise
	// 204,9 MiB üretiyordu; sınır onu 50 kattan fazla kısar.
	//
	// Sınıra çarpıldığında ne olacağı ayrı bir karardır ve gerekçesi
	// [yanitSayaci]'ndadır: yarım JSON gönderilmez.
	DefaultMaxResponseBytes = 4 << 20

	// DefaultMaxSelections belgenin fragment'ları AÇILDIKTAN sonra kaç seçim
	// taşıyabileceğinin varsayılan üst sınırıdır.
	//
	// Bu kapı ötekilerden önce koşar ve onları KORUR. Sebep ölçüldü: fragment
	// açılımı ÜSSEL olabilir ve bunu yapan belge küçüktür.
	//
	//	fragment f0 on Product { id }
	//	fragment f1 on Product { ...f0 ...f0 }
	//	fragment f2 on Product { ...f1 ...f1 }
	//	…
	//
	// Belge geçerlidir, döngü YOKTUR (doğrulamanın reddettiği tek şey odur) ve
	// 26 seviyede 1.127 BAYTTIR — ama açılımı 2²⁶ seçimdir. Ölçüldü: bu belge
	// ucu on saniyede bitiremiyordu. Ağacı gezen her hesap aynı tuzağa
	// düşüyordu: derinlik sayımı, alan tekrarı sayımı ve gqlgen'in kendi
	// karmaşıklık yürüyüşü (complexity/complexity.go da fragment tanımına
	// belleksiz iner). Yani sorun tek bir yürüyüşü düzeltmekle kapanmaz;
	// kapanma yolu, HİÇBİR yürüyüşün başlamadan önce ağacın büyüklüğünü
	// bağlamaktır.
	//
	// Sayım bütçelidir: bütçe bittiği anda gezinme YARIDA kesilir, yani bu
	// kapının kendi maliyeti de sınırın kendisidir.
	//
	// 10.000, jeton sınırının ([maxSorguJeton], 8.192) hemen üstündedir ve bu
	// bilinçlidir: fragment kullanmayan bir belgenin açılımı zaten jeton
	// sayısından küçüktür, yani sınır yalnızca AÇILIMI kendi metninden büyük
	// olan belgelere dokunur. Vitrinin en ağır meşru belgesi 90 küsur seçimdir.
	DefaultMaxSelections = 10000
)

// koleksiyonTahmini adedi ÖNCEDEN BİLİNEMEYEN liste alanlarının maliyet
// çarpanıdır.
//
// products'ın kaç kayıt döneceği argümanından okunur (bkz. [sayfaBoyutu]) ama
// bir ürünün kaç varyantı, kaç görseli olduğu ancak sorgu ÇALIŞINCA bellidir;
// karmaşıklık ise çalıştırmadan önce hesaplanmak zorundadır. Geriye tahmin
// kalır ve tahminin yönü önemlidir: OLDUĞUNDAN AZ göstermek, tam da pahalı
// olan sorguyu ucuz gösterir.
//
// 10 bir ölçüm değildir, tahminin ucuz olmaktan çıktığı yerdir: 40 varyantlı
// bir ürün modelin söylediğinden 4 kat pahalıdır, ama liste alanına sabit
// maliyet (1) verilseydi aynı ürün 40 kat ucuz görünürdü — ve sınır tam da o
// sorguyu geçirirdi.
//
// Alan başına ayrı çarpanlar (varyant 10, görsel 5, etiket 3…) denenmedi:
// ikinci bir maliyet modeli, bozulduğunda meşru sorguları SESSİZCE reddeder
// ve kimse bir alanın çarpanının neden başka olduğunu hatırlamaz.
const koleksiyonTahmini = 10

// kokSorguMaliyeti bir kök sorgunun (products, product) sabit maliyetidir.
//
// Modelin geri kalanı ALAN ÇÖZÜMÜ sayar, ama bir kök sorgunun asıl bedeli
// çözülen alanlar değildir: her biri veritabanına ayrı bir gidiş-dönüş, süzülen
// katalog üzerinde bir COUNT ve ardından link/batch okumalarıdır. Bu maliyet,
// istemci sonuçtan daha az alan seçince DÜŞMEZ.
//
// Sabit maliyet olmasaydı sınır tam da GraphQL'e özgü saldırıyı kaçırırdı:
// "{ a: products { count } b: products { count } … }" biçiminde 400 takma adlı
// bir belge, alan sayımına göre ucuzdur (her biri tek bir sayı) ama sunucuya
// 400 katalog sorgusu yaptırır — ve hız sınırlayıcı bunu BİR istek sayar.
// REST'te aynı yükü bindirmek 400 istek, yani 400 kota harcamak demektir.
//
// 1000, gerçekçi bir kategori listesi sorgusunun (~1,3 bin) mertebesindedir:
// yani 30 kök sorgu taşıyan bir belge, 30 kategori sayfası kadar fiyatlanır —
// ki tam olarak odur.
const kokSorguMaliyeti = 1000

// Sınır aşımlarının hata kodları.
//
// Biçim çekirdeğin snake_case kodlarına değil gqlgen'in BÜYÜK_HARF kodlarına
// benzer ve bu bilinçlidir: bunlar SERVİS hatası değil, belgenin hiç
// çalıştırılmadığını bildiren protokol hatalarıdır ve kardeşleri
// (COMPLEXITY_LIMIT_EXCEEDED) zaten gqlgen'den aynı biçimde gelir. Aynı sınıfı
// iki farklı biçimde döndürmek, istemciye iki ayrı hata sınıfı olduklarını
// düşündürürdü.
//
// Kodlar errcode.RegisterErrorType ile KAYDEDİLMEZ; o çağrı süreç genelindeki
// bir haritayı değiştirir ve tek bir modülün, kütüphaneyi kullanan herkesin
// HTTP durum kodunu değiştirmesi olurdu. Bedeli, yanıtın 200 dönmesidir —
// GraphQL'de zaten olağan durum budur ve hata gövdedeki errors dizisindedir.
const (
	// kodDerinlikAsimi derinlik sınırını aşan belgenin hata kodudur.
	kodDerinlikAsimi = "DEPTH_LIMIT_EXCEEDED"

	// kodAlanTekrariAsimi aynı alanı aynı nesne altında fazlaca tekrarlayan
	// belgenin hata kodudur.
	kodAlanTekrariAsimi = "FIELD_REPETITION_LIMIT_EXCEEDED"

	// kodIcGozlemAsimi iç gözlem kapılarından birini aşan belgenin hata
	// kodudur.
	//
	// Derinlik aşımından AYRI bir kod verilir çünkü istemcinin yapacağı şey
	// farklıdır: veri sorgusunu sadeleştirmek ile iç gözlem sorgusunu bölmek
	// aynı düzeltme değildir.
	kodIcGozlemAsimi = "INTROSPECTION_LIMIT_EXCEEDED"

	// kodIcGozlemKapali iç gözlem kapalıyken __schema/__type isteyen belgenin
	// hata kodudur.
	//
	// Aşımdan AYRI bir koddur ve ayrım istemci için gerçektir: aşım "daha az
	// iste" demektir, bu kod "bu kurulumda hiç isteme" demektir.
	kodIcGozlemKapali = "INTROSPECTION_DISABLED"

	// kodYanitAsimi yanıt bayt sınırını aşan isteğin hata kodudur.
	kodYanitAsimi = "RESPONSE_LIMIT_EXCEEDED"

	// kodSecimButcesiAsimi fragment açılımı bütçeyi aşan belgenin hata kodudur.
	kodSecimButcesiAsimi = "SELECTION_BUDGET_EXCEEDED"
)

// Options GraphQL ucunun sertleştirme ayarlarıdır.
//
// SIFIR DEĞER GEÇERLİDİR ve paket varsayılanlarını verir; alanların sıfırı
// "sınırsız" DEĞİL, "varsayılanı kullan" demektir. Ayrım bilinçlidir: sıfır
// "sınırsız" olsaydı, ayarı doldurmayı unutan bir kurulum korumasız bir uç
// açar ve bunu hiçbir hata vermeden yapardı — sertleştirmenin sessizce
// kaybolduğu tek yol budur.
//
// "Sınırsız" seçeneği HİÇ YOKTUR ve bu da bilinçlidir: sınırsız bir GraphQL
// ucu, kaynak tüketimini istemciye devretmektir. Sınır YÜKSELTİLEBİLİR,
// kaldırılamaz.
type Options struct {
	// MaxDepth tek bir belgede iç içe geçebilecek alan sayısının üst sınırıdır;
	// 0 ise [DefaultMaxDepth].
	MaxDepth int

	// MaxComplexity tek bir belgenin tahmini maliyet tavanıdır; 0 ise
	// [DefaultMaxComplexity].
	MaxComplexity int

	// MaxFieldRepetition aynı alanın aynı nesne altında kaç kez
	// seçilebileceğidir; 0 ise [DefaultMaxFieldRepetition].
	MaxFieldRepetition int

	// MaxIntrospectionRoots bir belgedeki __schema/__type kökü sayısının üst
	// sınırıdır; 0 ise [DefaultMaxIntrospectionRoots].
	MaxIntrospectionRoots int

	// MaxIntrospectionDepth iç gözlem alt ağacının derinlik tavanıdır; 0 ise
	// [DefaultMaxIntrospectionDepth].
	MaxIntrospectionDepth int

	// MaxResponseBytes tek bir yanıtın en fazla kaç bayt olabileceğidir; 0 ise
	// [DefaultMaxResponseBytes].
	MaxResponseBytes int

	// MaxSelections belgenin fragment'ları açıldıktan sonraki seçim sayısının
	// üst sınırıdır; 0 ise [DefaultMaxSelections].
	MaxSelections int

	// IntrospectionDisabled iç gözlemi (introspection) kapatır.
	//
	// Alan OLUMSUZ adlandırıldı çünkü sıfır değer paketin varsayılanını
	// vermelidir ve varsayılan AÇIKTIR: "Introspection bool" olsaydı,
	// Options{} ile kurulan her handler iç gözlemi sessizce kapatır ve şema
	// araçları hiçbir gerekçe görünmeden körleşirdi.
	//
	// # Varsayılanın gerekçesi
	//
	// İç gözlem tüm yüzeyi TEK istekte verir; kapatmak ilk bakışta bedava bir
	// sertleştirme gibi görünür. Bu vitrin için değildir: şema bu deponun
	// içinde duran bir DOSYADIR (graph/schema.graphqls) ve her gobit kurulumu
	// aynısını sunar. Kapatmak, saldırganın "git clone" ile okuyabildiği bir
	// şeyi yalnızca istemci araçlarından (kod üreteçleri, IDE eklentileri,
	// şema-diff akışları) saklar. Uç zaten publishable anahtarın ve hız
	// sınırının arkasındadır, maliyeti de bu dosyadaki sınırlar bağlar.
	//
	// "Maliyeti sınırlar bağlar" cümlesi bir zamanlar DOĞRU DEĞİLDİ: iç gözlem
	// hem derinlik hem karmaşıklık hesabının dışındaydı ve anahtarı kapatmak
	// ucun tek savunmasıydı. Bugün iç gözlemin kendi kapıları var
	// ([MaxIntrospectionRoots], [MaxIntrospectionDepth]), yani bu anahtar artık
	// bir acil durum vanası değil, bir yüzey kararıdır.
	//
	// Hesap DEĞİŞTİĞİNDE anahtar buradadır: şemasına kendi alanlarını ekleyen
	// bir çatal (fork) ya da genişlettiği yüzeyi ilan etmek istemeyen bir
	// kurulum tek satırla kapatır. Anahtarın var olması, "yüzey görünmüyor"
	// durumunu bir kaza değil bir KARAR hâline getirir.
	//
	// # Anahtar ÖNERİLERİ de kapatır
	//
	// İç gözlemi kapatmak bir zamanlar şemayı GİZLEMİYORDU ve anahtar tam da
	// vaat ettiği şeyi yapmıyordu: doğrulayıcı, __schema kapalıyken bile
	// şemanın adlarını perakende dağıtıyordu. Ölçüldü (hepsi tek istekte, tek
	// yanıtta):
	//
	//	prodcts             → Did you mean "products" or "product"?
	//	itemz               → Did you mean "items"?
	//	fragment on Prodct  → Unknown type "Prodct". Did you mean "Product"?
	//	products(limitt: …) → Unknown argument "limitt"… Did you mean "limit"?
	//
	// Doğrulayıcı bir belgedeki BÜTÜN hataları tek yanıtta topladığı için bir
	// istekte onlarca ad denenebilir; hız sınırı buna engel değildir, çünkü o
	// da bunu bir istek sayar.
	//
	// Bu yüzden anahtar iki yarımdır ve ikisi de [NewHandler]'da kurulur:
	// gqlgen'in SetDisableSuggestion'ı ulaşabildiği iki kuralın önerisini hiç
	// HESAPLAMAZ (levenshtein, her bilinmeyen alan için tipin bütün adları
	// üzerinde koşar), ulaşamadığı kuralların cümlesi ise [protokolHatasi]'nda
	// kesilir.
	//
	// Kapanan şey ADLARIN SAYILMASIDIR, adların TAHMİN EDİLMESİ değil: geçersiz
	// bir alan yine de bir doğrulama hatası üretir, yani tek tek deneme hâlâ
	// mümkündür. Onu da kapatmanın tek yolu doğrulama mesajlarını tümüyle
	// silmektir ve o zaman yüzey meşru istemci için de hata ayıklanamaz hâle
	// gelirdi — kapatılan, saldırganın işini n denemeden bir denemeye indiren
	// listedir.
	IntrospectionDisabled bool
}

// maxDepth uygulanacak derinlik sınırını döner.
func (o Options) maxDepth() int {
	return sinir(o.MaxDepth, DefaultMaxDepth)
}

// maxComplexity uygulanacak karmaşıklık sınırını döner.
func (o Options) maxComplexity() int {
	return sinir(o.MaxComplexity, DefaultMaxComplexity)
}

// maxFieldRepetition uygulanacak alan tekrarı sınırını döner.
func (o Options) maxFieldRepetition() int {
	return sinir(o.MaxFieldRepetition, DefaultMaxFieldRepetition)
}

// maxIntrospectionRoots uygulanacak iç gözlem kökü sınırını döner.
func (o Options) maxIntrospectionRoots() int {
	return sinir(o.MaxIntrospectionRoots, DefaultMaxIntrospectionRoots)
}

// maxIntrospectionDepth uygulanacak iç gözlem derinliği sınırını döner.
func (o Options) maxIntrospectionDepth() int {
	return sinir(o.MaxIntrospectionDepth, DefaultMaxIntrospectionDepth)
}

// maxResponseBytes uygulanacak yanıt bayt sınırını döner.
func (o Options) maxResponseBytes() int {
	return sinir(o.MaxResponseBytes, DefaultMaxResponseBytes)
}

// maxSelections uygulanacak seçim bütçesini döner.
func (o Options) maxSelections() int {
	return sinir(o.MaxSelections, DefaultMaxSelections)
}

// sinir verilen ayarı, geçersizse varsayılanı döner.
//
// Tek bir yardımcı olmasının sebebi ayarların ÇOĞALMASIDIR: her alan kendi
// if'ini taşısaydı, yeni bir sınır eklerken "0 varsayılana düşer" kuralını
// unutmak yalnızca bir satır uzaklıkta olurdu — ve unutulduğu alan sessizce
// "sınırsız" hâline gelirdi.
func sinir(deger, varsayilan int) int {
	if deger <= 0 {
		return varsayilan
	}

	return deger
}

// secimButcesi fragment açılımının büyüklüğünü sınırlayan gqlgen eklentisidir.
//
// Diğer kapılardan ÖNCE koşar ve tek işi onları korumaktır: kendisinden sonraki
// her hesap belge ağacını gezer ve fragment'lar üssel açılabildiği için o
// ağacın büyüklüğü belgenin boyutundan bağımsızdır. Gerekçenin ölçümü
// [DefaultMaxSelections]'dadır.
type secimButcesi struct{ sinir int }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = secimButcesi{}

// ExtensionName eklentinin adını döner.
func (secimButcesi) ExtensionName() string { return "SelectionBudget" }

// Validate eklentinin geçerli bir sınırla kurulduğunu doğrular.
func (s secimButcesi) Validate(graphql.ExecutableSchema) error {
	if s.sinir < 1 {
		return fmt.Errorf("graph: seçim bütçesi en az 1 olmalı, %d verildi", s.sinir)
	}

	return nil
}

// MutateOperationContext belgenin açılımını bütçeye karşı ölçer.
func (s secimButcesi) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	kalan := s.sinir
	if secimleriSay(opCtx.Operation.SelectionSet, &kalan) {
		return nil
	}

	hata := gqlerror.Errorf(
		"operation expands to more than %d selections, which exceeds the limit", s.sinir)
	errcode.Set(hata, kodSecimButcesiAsimi)

	return hata
}

// secimleriSay açılmış ağacı bütçe tükenene kadar sayar.
//
// Bütçe SAYAÇ değil KALAN olarak taşınır ve tükendiği anda gezinme yarıda
// kesilir. Ayrım kapının var olma sebebiyle aynıdır: önce sayıp sonra
// karşılaştıran bir uygulama, ölçmeye çalıştığı üssel ağacı sonuna kadar
// gezmek zorunda kalırdı — yani sınırı uygularken tam da sınırın engellediği
// işi yapardı.
func secimleriSay(secimler ast.SelectionSet, kalan *int) bool {
	for _, secim := range secimler {
		if *kalan <= 0 {
			return false
		}

		*kalan--

		var alt ast.SelectionSet

		switch s := secim.(type) {
		case *ast.Field:
			alt = s.SelectionSet
		case *ast.FragmentSpread:
			alt = s.Definition.SelectionSet
		case *ast.InlineFragment:
			alt = s.SelectionSet
		}

		if !secimleriSay(alt, kalan) {
			return false
		}
	}

	return true
}

// derinlikSiniri belgenin iç içe geçme derinliğini sınırlayan gqlgen
// eklentisidir.
//
// gqlgen'de derinlik sınırı YOKTUR (karmaşıklık sınırı vardır) ve ikisi
// birbirinin yerine geçmez: karmaşıklık, çözülecek alan SAYISINI ölçer ve
// döngüsel bir yolda her seviyenin maliyeti aynı kaldığı sürece derinliği
// cezalandırmaz.
//
// İki tavan taşır çünkü iki ayrı ağaç ölçülür: veri ağacı ve iç gözlem ağacı.
// Gerekçe [DefaultMaxIntrospectionDepth]'tedir.
type derinlikSiniri struct {
	sinir          int
	icGozlemSiniri int
}

// Eklentinin gqlgen sözleşmesini karşıladığı derleme zamanında sabitlenir:
// MutateOperationContext'in imzası kayarsa eklenti sessizce hiç çağrılmaz.
var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = derinlikSiniri{}

// ExtensionName eklentinin adını döner.
func (derinlikSiniri) ExtensionName() string { return "DepthLimit" }

// Validate eklentinin geçerli bir sınırla kurulduğunu doğrular.
//
// gqlgen bu metodu KURULUM anında çağırır ve hatası açılışta patlar; sınır
// çalışma zamanında denetlenseydi, sıfır sınırla kurulmuş bir uç her belgeyi
// reddeder ve arıza ancak ilk istekte görünürdü.
func (d derinlikSiniri) Validate(graphql.ExecutableSchema) error {
	if d.sinir < 1 {
		return fmt.Errorf("graph: derinlik sınırı en az 1 olmalı, %d verildi", d.sinir)
	}

	if d.icGozlemSiniri < 1 {
		return fmt.Errorf("graph: iç gözlem derinlik sınırı en az 1 olmalı, %d verildi", d.icGozlemSiniri)
	}

	return nil
}

// MutateOperationContext belgeyi ÇALIŞTIRMADAN önce derinliğini ölçer.
//
// Adım, ayrıştırma ve doğrulamadan SONRA çalışır (bkz. gqlgen executor):
// fragment tanımları o noktada çözülmüş, fragment DÖNGÜLERİ ise doğrulama
// tarafından reddedilmiştir. İkincisi burada hayatidir — döngülü bir belge
// aşağıdaki özyinelemeyi sonsuza sokar ve Go'da yığın taşması kurtarılamaz:
// panik değil, sürecin tamamının ölümüdür.
//
// Döngüsüz ama ÜSSEL açılan fragment'lar doğrulamadan geçer ve aşağıdaki
// yürüyüşü yine de kilitlerdi; onları [secimButcesi] bağlar ve bu eklentiden
// ÖNCE koşar. Bütçe kaldırılırsa buradaki özyineleme yeniden sınırsızdır.
func (d derinlikSiniri) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	veri, icGozlem := derinlikler(opCtx.Operation.SelectionSet)

	// Mesajlar İNGİLİZCEDİR ve bu bilinçlidir: kardeşi olan karmaşıklık
	// hatasını gqlgen üretir, metnini biz seçemeyiz. Aynı belgede iki sınırın
	// iki ayrı dilde konuşması, istemciye iki ayrı hata sınıfı olduklarını
	// düşündürürdü.
	if veri > d.sinir {
		hata := gqlerror.Errorf("operation has depth %d, which exceeds the limit of %d", veri, d.sinir)
		errcode.Set(hata, kodDerinlikAsimi)

		return hata
	}

	if icGozlem > d.icGozlemSiniri {
		hata := gqlerror.Errorf(
			"introspection selection has depth %d, which exceeds the limit of %d",
			icGozlem, d.icGozlemSiniri)
		errcode.Set(hata, kodIcGozlemAsimi)

		return hata
	}

	return nil
}

// derinlikler belgenin veri ve iç gözlem ağaçlarının derinliklerini AYRI
// döner.
//
// Ayrım yalnızca TEPEDE yapılır ve bu yeterlidir: __schema ve __type
// şemada Query'nin alanlarıdır, başka hiçbir tipte yoktur; yani bir iç gözlem
// kökü ancak belgenin en üst seçim kümesinde (ya da oraya açılan bir
// fragment'ta) belirebilir. Daha aşağıda [secimDerinligi] tek bir kuralla
// sayar.
func derinlikler(secimler ast.SelectionSet) (veri, icGozlem int) {
	for _, secim := range secimler {
		switch s := secim.(type) {
		case *ast.Field:
			derinlik := 1 + secimDerinligi(s.SelectionSet)
			if icGozlemAlani(s.Name) {
				icGozlem = max(icGozlem, derinlik)
			} else {
				veri = max(veri, derinlik)
			}
		case *ast.FragmentSpread:
			altVeri, altIcGozlem := derinlikler(s.Definition.SelectionSet)
			veri, icGozlem = max(veri, altVeri), max(icGozlem, altIcGozlem)
		case *ast.InlineFragment:
			altVeri, altIcGozlem := derinlikler(s.SelectionSet)
			veri, icGozlem = max(veri, altVeri), max(icGozlem, altIcGozlem)
		}
	}

	return veri, icGozlem
}

// secimDerinligi seçim kümesindeki en uzun alan zincirinin uzunluğunu döner.
//
// Sayım kuralları:
//
//   - Her alan bir seviyedir; yaprak alanlar da sayılır. "{ products { count } }"
//     2'dir.
//   - Fragment (spread ve satır içi) seviye EKLEMEZ: içeriği, spread'in
//     bulunduğu seviyededir. Aksi hâlde sorgusunu fragment'lara bölen istemci,
//     aynı ağacı isterken sınıra takılırdı — üstelik bölmemesi için hiçbir
//     sebep yokken.
//
// İSTİSNA YOKTUR: iç gözlem alanları da sayılır. Onların ayrı bir tavana
// tabi tutulması burada değil, ağaçları tepede ayıran [derinlikler]'dedir.
func secimDerinligi(secimler ast.SelectionSet) int {
	enDerin := 0

	for _, secim := range secimler {
		var derinlik int

		switch s := secim.(type) {
		case *ast.Field:
			derinlik = 1 + secimDerinligi(s.SelectionSet)
		case *ast.FragmentSpread:
			derinlik = secimDerinligi(s.Definition.SelectionSet)
		case *ast.InlineFragment:
			derinlik = secimDerinligi(s.SelectionSet)
		}

		if derinlik > enDerin {
			enDerin = derinlik
		}
	}

	return enDerin
}

// icGozlemAlani alanın bir iç gözlem kökü olduğunu bildirir.
//
// __typename bilinçli olarak DIŞARIDADIR: o bir kök değil, her tipte bulunan
// ve tek bir dize döndüren bir yapraktır; iç gözlem kotasından saymak, her
// normalize eden istemcinin (Apollo, urql — __typename'i kendileri ekler)
// kotasını boşa harcardı.
func icGozlemAlani(ad string) bool {
	return ad == "__schema" || ad == "__type"
}

// icGozlemKokSiniri belgedeki __schema/__type kökü sayısını sınırlayan gqlgen
// eklentisidir.
//
// Derinlikten AYRI bir eklentidir çünkü ölçtüğü şey ayrıdır: derinlik ağacın
// ne kadar indiğini, bu kapı aynı ağacın kaç kez istendiğini sorar. 302 kökün
// her biri sığdır (ölçülen belgede 4 seviye), yani derinlik kapısı onu asla
// göremezdi.
//
// İç gözlem KAPALIYKEN aynı kapı belgeyi tümüyle reddeder ve bu, gqlgen'in
// kendi davranışının önüne geçmek içindir: gqlgen alanı çalıştırma anında
// düz bir errors.New ile reddeder, o hata da resolver hatalarından ayırt
// edilemez ve [hataSunucusu] tarafından haklı olarak sunucu hatası sayılırdı —
// yani her iç gözlem denemesi bir ERROR satırı üretirdi. Burada reddetmek
// belgeyi hiç çalıştırmaz ve kararı belgenin kendi kapılarının yanına koyar.
type icGozlemKokSiniri struct {
	sinir  int
	kapali bool
}

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = icGozlemKokSiniri{}

// ExtensionName eklentinin adını döner.
func (icGozlemKokSiniri) ExtensionName() string { return "IntrospectionRootLimit" }

// Validate eklentinin geçerli bir sınırla kurulduğunu doğrular.
func (i icGozlemKokSiniri) Validate(graphql.ExecutableSchema) error {
	if i.sinir < 1 {
		return fmt.Errorf("graph: iç gözlem kökü sınırı en az 1 olmalı, %d verildi", i.sinir)
	}

	return nil
}

// MutateOperationContext belgedeki iç gözlem köklerini sayar.
func (i icGozlemKokSiniri) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	kok := icGozlemKokSayisi(opCtx.Operation.SelectionSet)
	if kok == 0 {
		return nil
	}

	if i.kapali {
		hata := gqlerror.Errorf("introspection is disabled on this endpoint")
		errcode.Set(hata, kodIcGozlemKapali)

		return hata
	}

	if kok <= i.sinir {
		return nil
	}

	hata := gqlerror.Errorf(
		"operation selects %d introspection roots, which exceeds the limit of %d", kok, i.sinir)
	errcode.Set(hata, kodIcGozlemAsimi)

	return hata
}

// icGozlemKokSayisi belgenin tepesindeki iç gözlem köklerini sayar.
func icGozlemKokSayisi(secimler ast.SelectionSet) int {
	sayi := 0

	for _, secim := range secimler {
		switch s := secim.(type) {
		case *ast.Field:
			if icGozlemAlani(s.Name) {
				sayi++
			}
		case *ast.FragmentSpread:
			sayi += icGozlemKokSayisi(s.Definition.SelectionSet)
		case *ast.InlineFragment:
			sayi += icGozlemKokSayisi(s.SelectionSet)
		}
	}

	return sayi
}

// alanTekrariSiniri aynı alanın aynı nesne altında kaç kez seçilebileceğini
// sınırlayan gqlgen eklentisidir.
//
// Karmaşıklık kapısının GÖREMEDİĞİ şeyi görür. Karmaşıklık alan sayısını
// fiyatlar ve "a0: description … a488: description" 489 ucuz alandır; oysa
// yanıtta 489 kez SERİLEŞTİRİLEN bir metindir. Ölçüldü: sayfa başına 100
// ürünle bu belgenin maliyeti tam 50.000'dir — tavana oturur, aşmaz, geçerdi —
// ve 204,9 MiB yanıt üretiyordu.
//
// Kapı [DefaultMaxResponseBytes] ile birlikte çalışır ve onun yerine geçmez:
// bu kapı ÇALIŞTIRMADAN ÖNCE reddeder (sunucu hiç iş yapmaz), öteki ise
// tahminin kaçırdığını yazarken yakalar.
type alanTekrariSiniri struct{ sinir int }

var _ interface {
	graphql.HandlerExtension
	graphql.OperationContextMutator
} = alanTekrariSiniri{}

// ExtensionName eklentinin adını döner.
func (alanTekrariSiniri) ExtensionName() string { return "FieldRepetitionLimit" }

// Validate eklentinin geçerli bir sınırla kurulduğunu doğrular.
func (a alanTekrariSiniri) Validate(graphql.ExecutableSchema) error {
	if a.sinir < 1 {
		return fmt.Errorf("graph: alan tekrarı sınırı en az 1 olmalı, %d verildi", a.sinir)
	}

	return nil
}

// MutateOperationContext en çok tekrarlanan alanı bulur ve sınırla karşılaştırır.
func (a alanTekrariSiniri) MutateOperationContext(
	_ context.Context,
	opCtx *graphql.OperationContext,
) *gqlerror.Error {
	alan, tekrar := enCokTekrarlananAlan(opCtx.Operation.SelectionSet)
	if tekrar <= a.sinir {
		return nil
	}

	hata := gqlerror.Errorf(
		"field %s is selected %d times under the same object, which exceeds the limit of %d",
		alan, tekrar, a.sinir)
	errcode.Set(hata, kodAlanTekrariAsimi)

	return hata
}

// enCokTekrarlananAlan en çok tekrarlanan (nesne, alan) çiftini ve tekrar
// sayısını döner.
//
// Sayım KARDEŞ kapsamlıdır: her seçim kümesi kendi içinde sayılır, alt
// seviyeler ayrıca. Belge geneli sayılsaydı ölçüm yanlış şeyi cezalandırırdı —
// standart iç gözlem sorgusunda __Type.ofType, ayrı zincirlerde onlarca kez
// geçer ve hiçbiri bir yığma değildir; oysa saldırının şekli tam olarak
// KARDEŞ yığmadır (aynı seçim kümesinde 489 takma ad).
//
// Takma ad anahtara GİRMEZ: saldırının tek aracı takma addır, ona bakan bir
// sayım hiçbir şey saymazdı.
//
// Yürüyüşün maliyeti belgenin metnine DEĞİL açılımına bağlıdır; üssel açılan
// fragment'lar onu kilitlerdi ve bu yüzden [secimButcesi] bu eklentiden önce
// koşar. Fragment döngüleri ise doğrulamada reddedilmiştir; gerekçe
// [derinlikSiniri.MutateOperationContext]'tedir.
func enCokTekrarlananAlan(secimler ast.SelectionSet) (cift string, tekrar int) {
	alanlar := kardesAlanlar(secimler)

	sayac := make(map[string]int, len(alanlar))
	for _, alan := range alanlar {
		anahtar := alanAnahtari(alan)

		sayac[anahtar]++
		if sayac[anahtar] > tekrar {
			cift, tekrar = anahtar, sayac[anahtar]
		}
	}

	for _, alan := range alanlar {
		altCift, altTekrar := enCokTekrarlananAlan(alan.SelectionSet)
		if altTekrar > tekrar {
			cift, tekrar = altCift, altTekrar
		}
	}

	return cift, tekrar
}

// kardesAlanlar seçim kümesinin AYNI seviyedeki alanlarını, fragment'ları
// açarak toplar.
//
// Fragment'lar seviye eklemez (bkz. [secimDerinligi]) ve burada da eklememeli:
// yığmasını bir fragment'a taşıyan istemci sayımdan kaçabilseydi kapı süs
// olurdu.
func kardesAlanlar(secimler ast.SelectionSet) []*ast.Field {
	var alanlar []*ast.Field

	for _, secim := range secimler {
		switch s := secim.(type) {
		case *ast.Field:
			alanlar = append(alanlar, s)
		case *ast.FragmentSpread:
			alanlar = append(alanlar, kardesAlanlar(s.Definition.SelectionSet)...)
		case *ast.InlineFragment:
			alanlar = append(alanlar, kardesAlanlar(s.SelectionSet)...)
		}
	}

	return alanlar
}

// alanAnahtari alanı "Tip.alan" biçiminde adlandırır.
//
// Nesne adı anahtara girer çünkü sınır "aynı alan" değil "AYNI NESNE ALTINDA
// aynı alan" hakkındadır: satır içi fragment'larla farklı tiplerden aynı adlı
// alanları seçmek meşrudur ve tek bir sayaca düşerse meşru belge reddedilirdi.
//
// ObjectDefinition doğrulamadan sonra doludur; boş kalması yalnızca şemada
// olmayan bir alan için mümkündür ve o belge zaten doğrulamada reddedilmiştir.
// Yine de nil ele alınır: burada panik, geçersiz bir belgeyi 500'e çevirirdi.
func alanAnahtari(alan *ast.Field) string {
	if alan.ObjectDefinition == nil {
		return alan.Name
	}

	return alan.ObjectDefinition.Name + "." + alan.Name
}

// karmasiklikMaliyetleri liste alanlarının maliyetini ELEMAN SAYISIYLA çarpar.
//
// gqlgen'in varsayılan hesabı her alana 1 + alt maliyet verir; yani
// "products(limit: 100) { items { … } }" ile "products(limit: 1) { items { … } }"
// AYNI maliyette görünür. Oysa aradaki fark tam olarak yüz kattır ve pahalı
// sorguyu ucuz gösteren bir maliyet modeli, sınırı süs hâline getirir.
//
// Çarpan iki kaynaktan gelir:
//
//   - products'ta ARGÜMANDAN: kaç kayıt döneceğini istemci söylemiştir
//     (bkz. [sayfaBoyutu]).
//   - iç içe listelerde TAHMİNDEN: adet çalışma zamanında bellidir, oysa
//     karmaşıklık çalıştırmadan önce hesaplanır (bkz. [koleksiyonTahmini]).
//
// Çarpan ProductList.items'a DEĞİL Query.products'a konur: ikisine birden
// konsaydı sayfa boyutu KAREsine çıkardı. products'ın çarpanı zarfın
// count/offset/limit alanlarını da kapsar — birkaç birimlik bu fazla sayım,
// tavanı güvenli yönde kaydırdığı için düzeltilmedi.
//
// Kök sorgular ayrıca sabit bir taban taşır ([kokSorguMaliyeti]): çarpım
// yalnızca ÇÖZÜLEN ALANI fiyatlar, oysa kök sorgunun bedeli veritabanı
// gidiş-dönüşüdür ve seçilen alan azalınca düşmez.
func karmasiklikMaliyetleri(maliyet *ComplexityRoot) {
	maliyet.Query.Products = func(alt int, limit, _ *int, _, _ *string) int {
		return kokSorguMaliyeti + sayfaBoyutu(limit)*alt
	}

	maliyet.Query.Product = func(alt int, _, _ *string) int {
		return kokSorguMaliyeti + alt
	}

	maliyet.Product.Variants = koleksiyon
	maliyet.Product.Options = koleksiyon
	maliyet.Product.Images = koleksiyon
	maliyet.Product.Tags = koleksiyon
	maliyet.Product.Categories = koleksiyon
	maliyet.Option.Values = koleksiyon
	maliyet.Variant.OptionValues = koleksiyon
}

// koleksiyon adedi bilinmeyen bir liste alanının maliyetini döner.
func koleksiyon(alt int) int {
	return koleksiyonTahmini * alt
}

// sayfaBoyutu products çağrısının en fazla kaç kayıt döndürebileceğini tahmin
// eder.
//
// Sayfalama kuralının ikinci bir TANIMI değildir; servisin uygulayacağı
// sonucun TAHMİNİDİR ve ayrışırlarsa dönen sayfa değil yalnızca maliyet
// tahmini şaşar. Yine de servisin sabitlerinden okunur ki tavan
// değiştiğinde tahmin kendiliğinden düzelsin.
//
// Negatif limit de varsayılana düşer: onu servis reddedecektir, burada
// yapılacak tek şey maliyeti sıfırlamamaktır (sıfır maliyet, sınırı hiç
// uygulanmamış hâle getirirdi).
func sayfaBoyutu(limit *int) int {
	switch {
	case limit == nil || *limit <= 0:
		return service.DefaultLimit
	case *limit > service.MaxLimit:
		return service.MaxLimit
	default:
		return *limit
	}
}
