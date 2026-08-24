// Package cart sepet akışlarının modüller arası orkestrasyonudur (plan Faz 5).
//
// Dört akış sunar: [Workflows.CreateCart], [Workflows.AddLineItem],
// [Workflows.UpdateLineItem] ve [Workflows.CalculateTotals]. Dördü de birden
// çok modüle dokunur ve plan Bölüm 2.5 gereği modül servisinde değil BURADA
// yaşar: sepetin şekli cart'ın, fiyat pricing'in, para birimi ve vergi
// region'ın verisidir; hiçbiri tek başına bir sepetin ne tuttuğunu bilemez.
//
// # Modüllere erişim
//
// Bu paket internal/modules altındaki HİÇBİR paketi import etmez (ADR 0006).
// İhtiyaç duyduğu her yüzey burada DAR bir arayüz olarak tanımlıdır ([Carts],
// [Prices], [Regions], [Customers], [Links], [Catalog]) ve somut servis
// container'dan ADLA çözülür (bkz. [FromContainer]). Kural
// internal/arch'taki TestWorkflowlarModulleriImportEtmez ile denetlenir.
//
// Arayüzlerin imzaları yalnızca ilkel ve stdlib tipleri kullanır. Sebep Go'nun
// yapısal uyum kuralıdır: bir modülün tipini adlandıramayan tüketici, o tipi
// kendi paketinde yeniden tanımladığı anda BAŞKA bir tip elde eder ve somut
// servis arayüzü karşılamaz. Aynı sebeple yapısal veri (sepetin anlık şekli ve
// hesaplanan toplamlar) sınırı JSON olarak geçer; bkz. [Carts].
//
// # Neden hiçbir akış saga değil
//
// Saga, bir akış BİRDEN ÇOK modülde geri alınması gereken yan etki bıraktığında
// kazanır: telafi zinciri, tek bir veritabanı işlemiyle sarılamayan yazmaları
// birlikte geri alır (bkz. internal/core/workflow paket yorumu). Bu dört akışın
// hiçbiri öyle değildir — hepsi ÇOK modülden OKUR ama yalnızca TEK modüle
// (cart) YAZAR:
//
//   - CreateCart: region ve customer'dan okur, cart'a bir kez yazar.
//   - AddLineItem / UpdateLineItem: catalog, link ve pricing'den okur, cart'a yazar.
//   - CalculateTotals: cart, link, pricing ve region'dan okur, cart'a yazar.
//
// Tek yazma patlarsa geri alınacak bir şey yoktur; adım hiç olmamıştır. İki
// yazma yapan tek yol satır ekleme/güncellemedir (önce satır, sonra toplamlar)
// ve ikinci yazmanın patlaması TELAFİ GEREKTİRMEZ: geriye kalan durum, cart
// modelinin açıkça tanıdığı BAYAT TOPLAM durumudur ve o sepetin sipariş olması
// zaten reddedilir (bkz. cart modülünde MarkCompleted). Satırı geri almak,
// geçici bir pricing arızası yüzünden müşterinin isteğini SİLMEK olurdu.
//
// Bu yüzden core/workflow Executor'ı bu turda KULLANILMAZ ve "core.workflow"
// adı çözülmez. Telafisi olmayan tek adımlı bir işi motora sarmak, yürütme
// kaydı ve telafi mekaniğinin bedelini öder ama karşılığında hiçbir güvence
// almaz; motorun sunduğu tek şey olan "ters sırada geri alma" burada boş
// kümedir.
//
// Faz 6'daki complete_cart GERÇEK bir saga olacaktır: rezervasyon (inventory),
// sipariş (order) ve ödeme (payment) ÜÇ AYRI modülde yan etki bırakır ve
// ödeme patladığında ilk ikisi geri alınmalıdır. Bu paket ona şu zemini
// bırakır: [Deps] ve [FromContainer] olduğu gibi genişletilir,
// [Workflows.CalculateTotals] ise saga'nın ilk adımının gövdesi olur —
// o adımın Compensate'i boş kalabilir, çünkü toplam yazmak idempotenttir ve
// bayatlık zaten görünür bir durumdur.
//
// # Vergi sözleşmesi
//
// Vergi bu fazda region'dan tek bir baz puan oranı olarak gelir
// ([Regions.RegionTax]); plan Faz 7'de tax modülü devralacaktır. Devralmanın
// sözleşmesi şu üç karardır:
//
//  1. TABAN: vergi, İNDİRİM SONRASI satır ara toplamı üzerinden hesaplanır ve
//     KARGO tabana girmez. Vergi fiilen ödenen bedeli izler; indirim öncesi
//     tutarı vergilemek, müşteriden hiç alınmayan bir paranın vergisini almak
//     olurdu. Kargo dışarıda bırakılır çünkü kargonun vergilenip
//     vergilenmediği yargı bölgesine göre değişir, region ise tek bir düz oran
//     taşır ve kargo için ayrı bir kural taşımaz. Olmayan bir kuralı "malla
//     aynıdır" diye varsaymak sessiz bir tahmindir; dışarıda bırakmak hem
//     muhafazakâr hem de Faz 7'de geri alınabilir seçimdir.
//  2. SATIR BAŞINA: vergi her satır için ayrı hesaplanır, sepetin vergisi de
//     satır vergilerinin TOPLAMIDIR. Sepet tabanını tek seferde vergilemek
//     yuvarlama yüzünden birkaç minor unit farklı sonuç verebilir; satır
//     başına hesaplamak seçilmiştir çünkü (a) faturada her satırın vergisi tek
//     tek açıklanabilir olmalıdır, (b) Faz 7'de ürün sınıfına göre satır
//     başına FARKLI oranlar gelecektir ve o gün tabanın tanımı değişmemelidir.
//  3. YUVARLAMA: baz puan aritmetiği TAM SAYIDIR ve bölme AŞAĞI yuvarlar
//     (taban × oran / 10000). Kabul edilir: hata satır başına bir minor
//     unit'ten küçüktür ve daima müşteri LEHİNEDİR. Yakına yuvarlama
//     (round-half-up) seçilmedi, çünkü müşteriden fazla tahsil eder ve
//     "fazlası nereden geldi" sorusunu mutabakata bırakır; kayan noktalı oran
//     ise plan Bölüm 8 gereği hiç düşünülmez.
//
// [Regions.RegionTax] verginin OTOMATİK uygulanmadığını bildirirse oran
// hesaba hiç girmez ve vergi sıfırdır: bölge, vergiyi kendi hesaplamak yerine
// dışarıda bırakmayı seçmiştir.
//
// # İndirim
//
// [Totals.DiscountTotal] ve [LineTotals.DiscountTotal] bu fazda DAİMA sıfırdır.
// İndirim hesabını plan Faz 7'de promotion modülü DEVRALACAKTIR; alanlar
// şimdiden taşınır ki devralma toplam kimliğini (total = subtotal - discount +
// tax + shipping) değiştirmek zorunda kalmasın. Vergi tabanı da bugünden
// indirim sonrası tanımlanmıştır (yukarıdaki 1. karar), yani promotion
// geldiğinde vergi kendiliğinden doğru tabana oturur.
//
// # Müşteri segmenti fiyatları
//
// Fiyat bağlamına ("region_id" dışında) müşteri grubu KONMAZ. pricing'in kural
// bağlamı öznitelik başına TEK değer taşır; birden çok gruba üye bir müşteri
// için hangi grubun yazılacağı belirsizdir ve sessizce birini seçmek fiyatı
// harita dolaşım sırasına bağlardı. Seçim kuralı ("müşterinin hakkı olan en
// iyi fiyat") pricing'in kararıdır ve promotion ile birlikte Faz 7'ye aittir.
//
// # cart.service bu yüzeyi BUGÜN karşılamıyor
//
// [Carts] arayüzünün altı metodundan yalnızca RemoveLineItem cart modülünün
// servisinde birebir aynı imzayla vardır. Kalan beşi cart'ın kendi models ve
// service tiplerini kullandığı için yapısal olarak KARŞILANAMAZ; sonuç,
// [FromContainer] çağrısının "cart.service" adında tipli bir uyumsuzluk
// hatasıyla dönmesidir ve hata hangi metodun eksik olduğunu yazar
// (ADR 0001, ADR 0002).
//
// Eksik olan şey region, pricing ve customer modüllerinde ZATEN bulunan
// şeydir: ilkel imzalı bir modüller arası yüzey (örn.
// internal/modules/region/service/interop.go). cart'ın karşılığı
// internal/modules/cart/service/interop.go olarak yazılmalı ve [Carts]
// arayüzündeki altı imzayı aynen yayımlamalıdır; her biri cart'ın var olan
// metotlarının ince bir sarmalayıcısıdır. O dosya bu turun kapsamı dışındadır
// (bu tur yalnızca internal/workflows altına yazar), bu yüzden sözleşme tek
// yerde — arayüzün kendisinde — durur.
package cart
