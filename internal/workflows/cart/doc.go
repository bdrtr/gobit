// Package cart sepet akışlarının modüller arası orkestrasyonudur (plan Faz 5).
//
// Dört akış sunar: [Workflows.CreateCart], [Workflows.AddLineItem],
// [Workflows.UpdateLineItem] ve [Workflows.CalculateTotals]. Dördü de birden
// çok modüle dokunur ve plan Bölüm 2.5 gereği modül servisinde değil BURADA
// yaşar: sepetin şekli cart'ın, fiyat pricing'in, para birimi region'ın,
// indirim promotion'ın ve vergi tax'ın verisidir; hiçbiri tek başına bir
// sepetin ne tuttuğunu bilemez.
//
// # Modüllere erişim
//
// Bu paket internal/modules altındaki HİÇBİR paketi import etmez (ADR 0006).
// İhtiyaç duyduğu her yüzey burada DAR bir arayüz olarak tanımlıdır ([Carts],
// [Prices], [Regions], [Customers], [Discounts], [Taxes], [Links], [Catalog])
// ve somut servis container'dan ADLA çözülür (bkz. [FromContainer]). Kural
// internal/arch'taki TestWorkflowlarModulleriImportEtmez ile denetlenir.
//
// Sekiz yüzeyin altısı ZORUNLUDUR; [Discounts] ve [Taxes] opsiyoneldir ve
// kayıtlı olmadıklarında hesap degrade bir yolda sürer (bkz. "İndirim" ve
// "Vergi sözleşmesi").
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
//   - CalculateTotals: cart, link, pricing, promotion, tax ve region'dan okur,
//     cart'a yazar.
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
// Vergiyi tax modülü hesaplar ([Taxes.CalculateTaxJSON]); Faz 5'te bu iş
// geçici olarak region'daydı ve region'ın godoc'u devralmayı zaten
// işaretlemişti. Sözleşmenin üç kararı devralmadan ETKİLENMEZ:
//
//  1. TABAN: vergi, İNDİRİM SONRASI satır ara toplamı üzerinden hesaplanır ve
//     KARGO tabana girmez. Vergi fiilen ödenen bedeli izler; indirim öncesi
//     tutarı vergilemek, müşteriden hiç alınmayan bir paranın vergisini almak
//     olurdu. Kargo dışarıda bırakılır çünkü kargonun vergilenip
//     vergilenmediği yargı bölgesine göre değişir; tax modülü bunu
//     ShippingInput.Taxable ile opsiyonel kılar ve bu akış o seçeneği AÇMAZ.
//     Olmayan bir kuralı "malla aynıdır" diye varsaymak sessiz bir tahmindir.
//  2. SATIR BAŞINA: vergi her satır için ayrı hesaplanır, sepetin vergisi de
//     satır vergilerinin TOPLAMIDIR. Sepet tabanını tek seferde vergilemek
//     yuvarlama yüzünden birkaç minor unit farklı sonuç verirdi; satır başına
//     hesaplamak seçilmiştir çünkü (a) faturada her satırın vergisi tek tek
//     açıklanabilir olmalıdır, (b) tax modülü ürün sınıfına göre satır başına
//     FARKLI oranlar uygulayabilir ve o gün tabanın tanımı değişmemelidir.
//  3. YUVARLAMA: baz puan aritmetiği TAM SAYIDIR ve bölme AŞAĞI yuvarlar
//     (taban × oran / 10000). Kabul edilir: hata satır başına bir minor
//     unit'ten küçüktür ve daima müşteri LEHİNEDİR. Yakına yuvarlama
//     (round-half-up) seçilmedi, çünkü müşteriden fazla tahsil eder ve
//     "fazlası nereden geldi" sorusunu mutabakata bırakır; kayan noktalı oran
//     ise plan Bölüm 8 gereği hiç düşünülmez.
//
// # Vergi ÜLKESİ nereden geliyor
//
// tax modülü ülke ister, sepet ise BÖLGE tutar. Ülke, bölgenin Query
// katmanındaki kaydından okunur ve bölge TEK bir ülkeye bağlıysa kullanılır.
// Reddedilen alternatif (sepetin kargo adresi) ve çok ülkeli bölgenin neden
// "çözülemedi" sayıldığı [Workflows.countryForRegion] godoc'undadır.
//
// # Vergi KAYNAĞI sonuçta görünür
//
// Vergiyi kimin hesapladığı [Totals.TaxSource] alanında bildirilir ve üç değer
// alır: [TaxSourceTax], [TaxSourceTaxUnconfigured], [TaxSourceRegion]. tax
// yüzeyi kayıtlı değilse ya da ülke çözülemiyorsa hesap region'ın oranına
// (Faz 5 yoluna) DÜŞER — sıfıra değil. Ladder'ın tamamı ve "neden sıfır değil"
// gerekçesi [Workflows.applyTaxes] godoc'undadır. Kısaca: eksik vergi
// satıcının cebinden sessizce çıkar, eksik indirim ise müşterinin gördüğü bir
// fazlalıktır; iki yönün riski simetrik değildir.
//
// # İndirim
//
// İndirimi promotion modülü hesaplar ([Discounts.ComputeDiscountsJSON]) ve
// sonuç KALEM BAŞINA gelir: satır indirimleri [LineTotals.DiscountTotal],
// toplamları [Totals.DiscountTotal] alanına yazılır. Hesap YAN ETKİSİZDİR —
// kuponu fiilen harcayan çağrı promotion'ın RedeemPromotion metodudur ve o,
// siparişin işidir; bu yüzden [Discounts] yüzeyi onu hiç tanımaz.
//
// promotion yüzeyi kayıtlı DEĞİLSE indirim sıfır kalır ve vitrin çalışmaya
// devam eder; gerekçe [Workflows.applyDiscounts] godoc'undadır.
//
// # Kupon kodları: YALNIZCA otomatik promosyonlar
//
// Sepette kupon alanı YOKTUR ve [Workflows.CalculateTotals] kupon kodu ALMAZ;
// hesaba yalnızca OTOMATİK promosyonlar girer.
//
// Reddedilen alternatif, kodları CalculateTotals'a opsiyonel bir parametre
// olarak vermekti. İki sebeple reddedildi:
//
//   - Toplam hesabı sepetin KENDİ durumundan yeniden üretilebilir olmalıdır.
//     Akış üç yerden çağrılır (doğrudan, satır ekleme ve satır güncelleme
//     sonrası) ve kod yalnızca birine geçseydi sepete YAZILAN indirim, en son
//     hangi uçtan geçildiğine göre görünüp kaybolurdu. Müşteri adedi bir
//     artırdığında kuponun sessizce düşmesi, o tasarımın kaçınılmaz sonucudur.
//   - Kod KALICI DEĞİLDİR. Sepet onu saklayamadığı için sipariş, sepette
//     görülen indirimden farklı bir toplamla oluşabilirdi; Faz 6'nın saga'sı
//     sepetin YAZILI toplamını kullanır.
//
// Kupon alanı sepet modülüne eklendiğinde bağlanacağı yer bellidir ve üç
// noktadır: [Snapshot] şemasına kodları taşıyan bir alan eklenir, o alan
// [Workflows.discountRequestFor] içinde isteğin "codes" dizisine geçirilir ve
// sipariş anında promotion'ın RedeemPromotion'ı çağrılır. Bu turda ilk iki
// nokta boş, üçüncüsü ise bu paketin dışındadır.
//
// # Müşteri segmenti fiyatları
//
// Fiyat bağlamına ("region_id" dışında) müşteri grubu KONMAZ. pricing'in kural
// bağlamı öznitelik başına TEK değer taşır; birden çok gruba üye bir müşteri
// için hangi grubun yazılacağı belirsizdir ve sessizce birini seçmek fiyatı
// harita dolaşım sırasına bağlardı. Seçim kuralı ("müşterinin hakkı olan en
// iyi fiyat") pricing'in kararıdır. Aynı boşluk indirim bağlamında da vardır
// ve aynı sebeple doldurulmaz (bkz. [Workflows.discountRequestFor]).
//
// # [Carts] yüzeyini kim karşılıyor
//
// Bu yüzey cart modülünün SERVİSİYLE değil, onun modüller arası interop
// tipiyle karşılanır ve container'da [ServiceCart] adıyla kayıtlıdır. Ayrım
// zorunludur: servisin imzaları cart'ın kendi models ve service tiplerini
// kullanır, bu paket ise o tipleri adlandıramaz (ADR 0006) — yapısal uyum
// ancak ilkel imzalı bir yüzeyle kurulabilir. Aynı örüntü region, pricing,
// customer, promotion ve tax modüllerinde de vardır.
//
// Uyumu derleyici DENETLEMEZ; yanlış kaydedilmiş bir tip [FromContainer]
// çağrısında tipli bir uyumsuzluk hatası verir ve hata hangi metodun eksik
// olduğunu yazar (ADR 0001, ADR 0002). Alan adlarının uyumu ise ancak
// entegrasyon testiyle kanıtlanabilir (bkz. internal/e2e).
package cart
