// Package checkout sepeti siparişe çeviren complete_cart saga'sıdır
// (plan Faz 6).
//
// Tek bir akış sunar: [Workflows.CompleteCart]. Akış beş adımdan oluşur ve
// core/workflow'un saga motoruyla yürütülür:
//
//	reserve_inventory -> create_order -> authorize_payment -> capture_payment -> clear_cart
//
// Bir adım patlarsa motor, o ana kadar BAŞARILI olmuş adımların
// Compensate'lerini TERS SIRADA çağırır: ödeme iptal edilir, sipariş iptal
// edilir, rezervasyonlar geri bırakılır. Faz 6'nın DoD'si budur.
//
// # Neden GERÇEK bir saga
//
// Faz 5'in sepet akışları çok modülden OKUR ama tek modüle YAZARDI ve bu
// yüzden saga değildi (bkz. internal/workflows/cart paket yorumu). Bu akış
// ondan yapısal olarak farklıdır: inventory, order ve payment modüllerinin
// ÜÇÜNDE de geri alınması gereken yan etki bırakır. Üç modül ayrı tablolara
// (ileride ayrı servislere) sahip olduğu için tek bir veritabanı işlemiyle
// sarılamazlar; dağıtık işlemin yerini tutan şey telafi zinciridir.
//
// # Modüllere erişim
//
// Bu paket internal/modules altındaki HİÇBİR paketi import etmez (ADR 0006).
// İhtiyaç duyduğu her yüzey burada DAR bir arayüz olarak tanımlıdır ([Carts],
// [Inventory], [Fulfillment], [Orders], [Payments], [Links], [Catalog]) ve
// somut servis container'dan ADLA çözülür (bkz. [FromContainer]). Kural
// internal/arch'taki TestWorkflowlarModulleriImportEtmez ile denetlenir.
//
// Arayüzlerin imzaları yalnızca ilkel ve stdlib tipleri kullanır; sebebi Go'nun
// yapısal uyum kuralıdır (ADR 0001). Bileşik veri (sepetin anlık şekli,
// siparişin görüntüsü) sınırı JSON olarak geçer.
//
// Arayüzler BİLİNÇLİ OLARAK dardır: modül yüzeyinde var olan ama bu akışın
// kullanmadığı metotlar (inventory.AvailableQuantity, payment.Refund,
// order.CompleteOrder, fulfillment.CreateFulfillment …) buraya YAZILMAZ.
// Gerekçeler ilgili arayüzlerin godoc'undadır.
//
// # internal/workflows/cart bağımlılığı
//
// Bu paket internal/workflows/cart'ı import EDER ve bu, ADR 0006'nın yasağının
// dışındadır: yasak internal/modules içindir, kardeş bir orkestrasyon paketi
// için değil. Bağımlılık kaçınılmazdır ve iki sebebi vardır:
//
//  1. Siparişin SATIR BAŞINA tutara ihtiyacı vardır (birim fiyat, ara toplam,
//     vergi) ve cart modülünün ilkel yüzeyi ("cart.interop") satırların yalnızca
//     kimliğini, varyantını ve adedini yayımlar — tutarları yayımlamaz. Satır
//     tutarlarını üreten TEK yer calculate_totals akışıdır; order modülünün
//     interop belgesi de bu birleştirmeyi açıkça workflow'a verir.
//  2. cart modülünün MarkCompleted'ı BAYAT toplamlı sepeti reddeder
//     (totals_revision ≠ revision). Hesap checkout'un başında yenilenmezse
//     saga'nın SON adımı, para çekildikten sonra düşerdi.
//
// # Hesap saga'dan ÖNCE yapılır
//
// [Workflows.CompleteCart] önce hazırlık yapar (hesap, anlık görüntü, başlık,
// stok kalemi çözümü), sonra saga'yı başlatır. Hazırlığın adım OLMAMASI
// bilinçlidir: hiçbiri geri alınacak yan etki bırakmaz (toplam yazmak
// idempotenttir ve bayatlık zaten görünür bir durumdur), oysa saga adımı olmak
// her birine gereksiz bir telafi ve yürütme kaydı maliyeti yüklerdi. Ayrıca
// hazırlıkta bulunan bir hata (fiyatsız varyant, stok kalemi olmayan ürün)
// HİÇBİR yan etki uygulanmadan döner.
//
// # Adım adım kararlar
//
// reserve_inventory — sepetin her satırı için önce lokasyon belirlenir, sonra
// stok ayrılır; telafisi ReleaseReservation'dır ve İDEMPOTENTTİR. Adım kendi
// içinde bileşiktir: bir satır patlarsa o ana kadar alınmış rezervasyonları
// KENDİ bırakır, çünkü motor tek denemede patlayan bir adımı telafi etmez
// (bkz. core/workflow paket yorumu). Kendi temizliği de patlarsa hata
// [workflow.ErrUncompensated] ile sarılır ve yürütme compensation_failed
// yazılır. Lokasyonun nasıl belirlendiği için bkz. "Lokasyon".
//
// create_order — sipariş, hazırlıkta kurulan görüntüden açılır; telafisi
// CancelOrder'dır ve İDEMPOTENTTİR. Görüntüye yürütme kimliği idempotency
// anahtarı olarak konur, böylece aynı yürütmede ikinci bir çağrı yeni sipariş
// açmaz.
//
// authorize_payment — koleksiyon açılır, oturum açılır ve yetkilendirilir;
// telafisi Cancel'dır ve blokajı serbest bırakır.
//
// capture_payment — tahsilat yapılır ve tutar Collection ile DOĞRULANIR.
// Telafisi YOKTUR (bkz. "Dönüşü olmayan nokta"). Çağrının hata dönmesi "para
// gitmedi" demek değildir; hata yolu soruşturulur (bkz. "Belirsiz tahsilat").
//
// clear_cart — sepet tamamlanmış işaretlenir ve rezervasyonlar kesinleştirilir.
// Telafisi yoktur; hem son adımdır hem de ConfirmReservation geri alınamaz.
//
// # Lokasyon: stok OLGUSU ile kargo KARARI ayrı yerlerde durur
//
// [CompleteCartInput.LocationID] OPSİYONELDİR. Doluysa akış hiçbir seçim yapmaz
// ve sepetin tüm satırları o lokasyondan ayrılır — bildirilen lokasyon bir
// tercih değil talimattır. Boşsa lokasyon SATIR BAŞINA belirlenir ve soru ikiye
// bölünür:
//
//  1. "Bu kalemden bu adet hangi depolarda ayrılabilir" bir STOK OLGUSUDUR;
//     cevabı [Inventory.LocationsWithStock] verir ve bir tercih sırası taşımaz.
//  2. "Bu adaylar hangi sırayla denensin" bir KARGO KARARIDIR; cevabı
//     [Fulfillment.RankLocations] verir. Kargo modülü hedef bölgeye hizmet
//     etmeyen depoları eler ve kalanları işletmecinin öncelik sırasına dizer;
//     buradan geçen tek bağlam siparişin bölgesidir. Sıra satır başına BİR KEZ
//     sorulur, tükenen her adaydan sonra değil.
//
// İkisini tek modülde toplamak, stok sorgusunu kargo politikasına ya da kargo
// politikasını stok şemasına bağımlı kılardı. Seçimi bu paketin yapması ise
// ADR 0006'yı çiğnemeden mümkün olurdu ama yine yanlış olurdu: sepet akışının
// depo politikası hakkında söyleyecek bir sözü yoktur.
//
// Sonucu şudur: bir siparişin satırları FARKLI depolardan ayrılabilir. Telafi
// bundan etkilenmez, çünkü rezervasyonlar KİMLİK başına bırakılır ve hangi
// depodan alındıkları bırakmayı değiştirmez. Hiçbir lokasyonda yeterli stok
// bulunamayan bir satır ise ayırmanın patladığı durumla AYNI yoldan raporlanır
// (errors.Conflict, [CodeReservationFailed]) ve o ana kadar alınmış
// rezervasyonlar adımın KENDİ temizliğiyle geri bırakılır — çok depolu bir
// sepette bu, tek depoluya göre daha kolay oluşan bir durumdur.
//
// # TAM ÖDEME KURALI
//
// Yetkilendirmenin BLOKE ETTİĞİ tutar koleksiyonun tutarını karşılamıyorsa
// (authorized < amount) authorize_payment BAŞARISIZ sayılır ve saga geri
// alınır. Kural tek satırdır ve [Payments.Authorize]'ın döndürdüğü SAYIYA
// bakar, sağlayıcının durum dizesine değil: sağlayıcı KISMİ yetkilendirdiğinde
// durum yine "authorized" olur ve yalnızca duruma bakan bir saga ödenmemiş bir
// siparişi onaylardı. Aynı ölçü tahsilattan sonra da uygulanır: koleksiyon
// yeniden okunur ve captured >= amount doğrulanır.
//
// # Dönüşü olmayan nokta (pivot)
//
// capture_payment saga'nın PIVOT adımıdır: para çekildikten sonra otomatik
// geri dönüş YOKTUR. İade, tahsilatın telafisi değil AYRI bir akıştır (plan
// Faz 7+) ve müşteriye, siparişe, muhasebeye ayrı ayrı dokunur; onu sessizce
// bir telafi adımına gizlemek, saga'nın "geri alındı" dediği yerde gerçekte
// para hareketi yaratması demek olurdu.
//
// Bunun üç sonucu vardır ve üçü de bilinçlidir:
//
//   - capture_payment'ın Compensate'i tahsilat GERÇEKLEŞMİŞSE hata döner
//     (errors.Conflict). Motor yürütmeyi compensation_failed yazar ve bu
//     ELLE MÜDAHALE sinyalidir. nil dönmek "geri alındı" demek olurdu ve o
//     kayıt yalan olurdu.
//   - Tahsilattan ÖNCEKİ adımların telafileri, tahsilat yapılmışsa ÇALIŞMAZ:
//     sipariş iptal edilmez, stok geri bırakılmaz, blokaj serbest bırakılmaz
//     (bkz. Workflows.skipAfterCapture). Parası çekilmiş bir siparişi geri
//     almak müşteriyi hem parasından hem siparişinden ederdi; stoğu bırakmak
//     ise ayakta duran bir siparişin malını ikinci kez satmak olurdu. Blokajın
//     iptali zaten anlamsızdır: tahsilat blokajı kapatır (bkz. payment
//     modülünde CapturePayment). Her atlama ERROR olarak loglanır ve yürütme
//     yine compensation_failed yazılır.
//   - Pivot koruması tahsilatın BAŞARISINA değil, DENENMİŞ olmasına bakar
//     (bkz. Workflows.skipAfterCapture). Başarıya bakan bir koruma, korumanın
//     en çok gerektiği yerde kapanırdı — bkz. "Belirsiz tahsilat".
//   - clear_cart pivot'tan SONRA çalıştığı için modül arızalarını hata olarak
//     DÖNDÜRMEZ.
//     Sepet damgası ya da rezervasyon onayı düşerse olay ERROR olarak loglanır,
//     [CompleteCartResult.Warnings] alanına yazılır ve akış BAŞARILI biter.
//     Alternatifi, ödemesi alınmış bir siparişi iptal edip stoğu serbest
//     bırakmaktı. Kalan tutarsızlık (açık kalmış sepet, "active" kalmış
//     rezervasyon) görünür ve elle onarılabilir; iade edilmemiş bir tahsilat
//     değildir.
//
// # Belirsiz tahsilat: hata "para gitmedi" DEMEK DEĞİLDİR
//
// Klasik dağıtık işlem sorunu buradadır: sağlayıcı parayı çeker ve yanıt
// kaybolur (ağ zaman aşımı). Capture hata döner, geriye hiçbir tahsilat kimliği
// kalmaz ve KİMLİĞE bakan bir pivot koruması kapanırdı — saga siparişi iptal
// eder, stoğu bırakır ve müşteri hem parasından hem siparişinden olurdu.
//
// Bu yüzden hata yolu SORUŞTURULUR: koleksiyon yeniden okunur ve saga yalnızca
// koleksiyon hiçbir tahsilat GÖRMEDİĞİNDE geri alınır. Aksi hâlde (okuma da
// patladıysa ya da tahsilat görünüyorsa) geri alma YAPILMAZ; hata
// [workflow.ErrUncompensated] taşır, yürütme compensation_failed yazılır ve
// düzeltme elle yapılır.
//
// # KALAN RİSK: koleksiyon YEREL defterdir, sağlayıcının kendisi değil
//
// Yukarıdaki soruşturma riski DARALTIR, ORTADAN KALDIRMAZ ve bu sınır burada
// açıkça yazılmalıdır.
//
// Ödeme modülü sağlayıcı çağrısını KENDİ veritabanı işleminin İÇİNDE yapar
// (internal/modules/payment/service/capture.go). Sağlayıcı parayı çektikten
// SONRA modülün bir yazması ya da commit'i patlarsa işlem geri sarılır: para
// gitmiştir ama koleksiyonda hiçbir iz YOKTUR. Saga o koleksiyonu okur,
// "tahsilat görünmüyor" der ve TAM GERİ ALMA yapar — yani tam olarak bu
// bölümün önlemeye çalıştığı arıza, bir katman aşağıda hâlâ mümkündür.
//
// Yanıtın kaybolabileceği İKİ yer vardır ve soruşturma yalnızca birincisini
// kapatır:
//
//	sağlayıcı  <--(1)-->  ödeme modülü  <--(2)-->  ödeme modülünün commit'i
//
// (1) kapalıdır: modülün KAYDETTİĞİ bir tahsilat saga tarafından görülür.
// (2) AÇIKTIR: modül kaydedemediği bir tahsilatı saga'ya bildiremez.
//
// (2)'yi kapatmanın tek doğru yolu sağlayıcıya SORMAKTIR — yani mutabakat
// (reconciliation): sağlayıcının kendi defteriyle periyodik karşılaştırma.
// Plan bunu bu faza koymuyor; Faz 7+ işidir. O gelene kadar bu sınıf arıza
// operasyonel olarak (sağlayıcı panelinden) yakalanmalıdır.
//
// İkincil bir iyileştirme, sağlayıcı çağrısını modülün işleminin DIŞINA almak
// ve "capturing" ara durumuyla iki fazlı yazmaktır; bu pencereyi daraltır ama
// yine kapatmaz (bu sefer ara durumu yazan işlem patlayabilir).
//
// Seçenekler tartıldı ve karar bilinçlidir:
//
//   - Her hatayı "tahsil edilmedi" saymak (eski davranış) EN UCUZ koddur ama en
//     pahalı arızayı üretir: ödenmiş bir siparişin yok edilmesi. Reddedildi.
//   - Hata yolunda oturumu/koleksiyonu SORGULAMAK bir ağ çağrısı ekler ve
//     yalnızca hata yolunda ödenir; mutlu yol değişmez. SEÇİLDİ.
//   - Tahsilat kanıtlandığında akışı İLERİ taşımak (tahsilatı başarılı sayıp
//     sepeti kapatmak) cazip görünür ama elimizde tahsilat kimliği yoktur:
//     siparişe ve muhasebeye yazılacak iz olmadan "başarılı" demek,
//     doğrulanamayan bir ödemeyi doğrulanmış göstermek olurdu. Kaybolmuş yanıtın
//     mutabakatı ayrı bir akıştır (plan Faz 7+).
//
// Karar asimetriktir çünkü bedeller asimetriktir: yanlışlıkla geri almanın
// bedeli iade, muhasebe ve müşteri temasıdır; yanlışlıkla geri ALMAMANIN bedeli
// bekleyen bir sipariş, ayrılmış stok ve kartta kalan bir blokajdır — hepsi
// görünür ve onarılabilir.
//
// # Saga çağıranın İPTALİNDEN ayrılır
//
// Hazırlık çağıranın bağlamıyla koşar; saga ise ondan ayrılıp kendi süre
// bütçesine bağlanır ([SagaTimeout]). Sebep yine pivot'tadır: motor her adımdan
// önce bağlamı denetler ve tahsilat sırasında gelen bir iptal, clear_cart'ı
// tümüyle atlatırdı — para çekilmiş, sipariş "pending", sepet kilitli, stok
// "active" kalır ve idempotency anahtarı yandığı için o sepet bir daha
// denenemezdi. Yarım bırakılan iş, tamamlananın maliyetinden pahalıdır.
//
// # Idempotency
//
// Yürütme, sepet kimliğinden türetilen bir anahtara bağlanır
// ([IdempotencyKeyPrefix] + cart_id). Aynı sepet için yapılan ikinci çağrı
// adımları TEKRAR ÇALIŞTIRMAZ: sürüyorsa ya da başarısız olmuşsa
// errors.Conflict döner (bkz. [workflow.Executor]).
//
// Motorun "tamamlanmış yürütmenin çıktısını dön" yolu (replay) bu akışta
// pratikte ERİŞİLEMEZDİR: hazırlık motorun denetiminden ÖNCE çalışır ve ilk iş
// olarak hesabı yeniler, oysa başarılı bir yürütme sepeti tamamlanmış damgalar
// ve tamamlanmış sepette hesap yapılamaz. Gerçek kurulumda ikinci çağrının
// cevabı bu yüzden "aynı sonuç" değil, [CodeCartCompleted]'dır; replay yolu
// yalnızca sepetin damgalanamadığı (clear_cart uyarısı bıraktığı) durumda
// görülebilir. Zararsız yönde bir sapmadır — iki koruma da aynı şeyi engeller:
// aynı sepetten ikinci bir sipariş doğmaz.
//
// Başarısız bir denemeden sonra AYNI sepetin yeniden denenemeyeceği kabul
// edilen bedeldir: motor anahtarı "bir denemenin sonucu" olarak tanımlar,
// sonsuz bir tekrar hakkı olarak değil. Reddedilen bir ödemeden sonra yeni bir
// deneme başlatmak (yeni anahtar üretmek) bu akışın değil, onu çağıran uç
// noktanın kararıdır ve plan Faz 7+'ya aittir.
//
// Koruma tek katlı da değildir: başarılı bir yürütme sepeti tamamlanmış
// damgalar, tamamlanmış sepette hesap yapılamaz ve MarkCompleted ikinci kez
// başarılı olmaz. Yani anahtar kaybolsa bile aynı sepetten ikinci bir sipariş
// doğmaz.
//
// # order.placed olayını bu paket YAYIMLAMAZ
//
// Olayı order modülü KENDİ yayımlar: servisinin CreateOrder metodu siparişi
// yazdıktan sonra "order.placed" olayını veri yoluna koyar (bkz. order
// modülünde events.go, EventOrderPlaced). Ayrıca yayımlamak ÇİFT OLAY üretirdi
// ve aboneler (bildirim, muhasebe, arama indeksi) aynı siparişi iki kez
// işlerdi. Bu yüzden bu paket "core.eventbus" adını hiç çözmez ve hiçbir olay
// yayımlamaz.
//
// Aynı sebeple telafi de olay yayımlamaz: siparişin iptali order modülünün
// kendi kararıdır ve duyurusu da oraya aittir.
//
// order modülünün olayı GERÇEKTEN yayımladığı, ancak gerçek modüllerle koşan
// bir entegrasyon testiyle kanıtlanabilir — bu paket o modülü import edemediği
// için derleyici de birim testi de o bağı göremez (ADR 0006'nın kabul edilen
// bedeli).
//
// # Yeniden deneme politikası
//
// Adımlar YENİDEN DENENMEZ (motorun varsayılanı). Sebep adımların
// idempotentliğidir: inventory.Reserve iki kez çağrılırsa İKİ rezervasyon
// üretir ve payment.Capture'ın tekrarı gerçek bir ödeme sağlayıcısında tekrar
// para hareketi denemesidir. Motor bir adımı kendi kararıyla tekrarlarsa onu
// en iyi çaba ile telafi eder, ama en iyi çaba burada yeterli değildir.
//
// TELAFİ ise yeniden denenir (bkz. [workflow.WithCompensationRetry]): başarısız
// bir Invoke'un bedeli yürütmenin geri alınmasıdır, başarısız bir Compensate'in
// bedeli elle müdahaledir. Geçici bir arızada telafide ısrar etmek bu yüzden
// karşılığını verir.
package checkout
