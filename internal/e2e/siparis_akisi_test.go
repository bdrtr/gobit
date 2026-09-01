//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Bu dosya planın Faz 6 DoD'sini GERÇEK modüllerle kanıtlar.
//
// DoD tek cümleyle: "Uçtan uca sepet -> sipariş akışı test provider ile
// çalışıyor; ödeme adımı başarısızken STOK REZERVASYONU VE SİPARİŞ GERİ
// ALINIYOR (saga testi); order.placed eventi yayınlanıyor."
//
// # Neden birim testleri yetmez
//
// checkout paketinin birim testleri saga'nın kararlarını SAHTE yüzeylerle
// sınar: "telafi çağrıldı mı", "hangi sırayla". Bunlar doğru sorulardır ama
// tek başına Faz 6'yı kanıtlamaz, çünkü DoD'nin iddiası çağrıların yapılması
// değil SONUCUDUR — siparişin gerçekten "canceled" olması, ayrılan stoğun
// gerçekten satılabilir hâle dönmesi, paranın gerçekten çekilmiş olması.
// Bunların hepsi modüllerin veritabanı işlemlerinde ve durum makinelerinde
// yaşar; ancak gerçek modüllerle görülebilir.
//
// # Beklenen tutarlar neden elle yazılıyor
//
// Faz 5 testlerindeki gerekçenin aynısı geçerlidir (bkz. paket yorumu): her
// senaryonun ara toplamı, vergisi ve genel toplamı testin İÇİNDE kâğıt üstünde
// hesaplanmış SABİTLERDİR. Üretim formülünü testte tekrar etmek, aynı hatayı
// iki yerde birden yapmak olurdu.
//
// # Ödeme sağlayıcısı
//
// Sağlayıcı manuel/test sağlayıcısıdır (internal/modules/payment/manual) ve
// davranışı oturum verisiyle yönlendirilir: [manual.DataKeyOutcome] anahtarı
// yetkilendirmenin kabul mü ret mi edileceğini söyler. Veri akışa
// [checkoutwf.CompleteCartInput.PaymentData] alanından verilir ve sağlayıcıya
// OLDUĞU GİBİ iletilir; yani saga'nın kendisinde hiçbir test kancası yoktur.

// Mutlu yol senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir:
//
//	45_000 × 2 = 90_000 ara toplam
//	90_000 × %20 = 18_000 vergi
//	90_000 - 0 + 18_000 + 0 = 108_000 genel toplam
const (
	mutluBirimFiyat    int64 = 45_000
	mutluAdet          int64 = 2
	mutluAraToplam     int64 = 90_000
	mutluVergi         int64 = 18_000
	mutluToplam        int64 = 108_000
	mutluBaslangicStok int64 = 10
	// mutluKalanStok tahsilat sonrası beklenen fiziksel adettir: 10 - 2.
	mutluKalanStok int64 = 8
)

// TestSepettenSipariseMutluYol Faz 6 DoD'sinin ilk yarısını uçtan uca
// koşturur: sepet siparişe dönüşür, para çekilir, stok düşer, sepet kapanır.
//
// Zincir: ürün + varyant + fiyat + stok kalemi + stok seviyesi -> sepet ->
// satır -> hesap -> complete_cart -> sipariş + tahsilat + kesinleşmiş
// rezervasyon + kapanmış sepet. Halkalardan biri koparsa hata burada görünür.
func TestSepettenSipariseMutluYol(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	const varyantBaslik = "E2E Mutlu Yol Ürünü"
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, varyantBaslik, map[string]int64{
		vergiliParaBirimi: mutluBirimFiyat,
	}, mutluBaslangicStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, mutluAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: mutluAraToplam,
		indirim:   0,
		vergi:     mutluVergi,
		kargo:     0,
		toplam:    mutluToplam,
	}, "mutlu yol sepeti hazırlandıktan sonra")

	require.Equal(t, mutluBaslangicStok, satilabilirAdet(ctx, t, stokKalemID),
		"sepete satır eklemek stok AYIRMAMALI; rezervasyon sipariş anında yapılır, "+
			"aksi hâlde vazgeçilen her sepet stoğu kilitlerdi")

	// --- akışın kendisi ---

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     mutluToplam,
	})
	require.NoError(t, err, "hazır bir sepet siparişe çevrilebilmeli")

	require.Equal(t, sepetID, sonuc.CartID, "sonuç siparişin doğduğu sepeti bildirmeli")
	require.NotEmpty(t, sonuc.OrderID, "sonuç oluşan siparişin kimliğini taşımalı")
	require.Equal(t, mutluToplam, sonuc.Amount,
		"akışın tahsil ettiği tutar ELLE hesaplanan genel toplam olmalı; ExpectedTotal "+
			"denetimi zaten geçtiği için buradaki sapma sonucun yanlış alanı taşıdığını gösterir")
	require.Equal(t, vergiliParaBirimi, sonuc.CurrencyCode,
		"tahsilat sepetin para biriminde yapılmalı")
	require.True(t, sonuc.CartCompleted,
		"sepet tamamlanmış damgalanmalı; damgalanmazsa aynı sepet ikinci bir siparişe "+
			"kaynak olabilirdi")
	require.True(t, sonuc.ReservationsConfirmed,
		"rezervasyonlar kesinleştirilmeli; kesinleşmeyen rezervasyon stoğu 'ayrılmış' "+
			"bırakır ve satılan mal fiziksel stoktan hiç düşmez")
	require.Empty(t, sonuc.Warnings,
		"mutlu yolda uyarı OLMAMALI; uyarı, pivot'tan sonra bir modülün patladığını ve "+
			"elle onarım gerektiğini bildirir")

	// --- 1) sipariş oluştu mu, toplamları sepetle AYNI mı ---

	siparis, err := siparisSvc.GetOrder(ctx, sonuc.OrderID)
	require.NoError(t, err, "oluşan sipariş sipariş modülünden okunabilmeli")
	require.Equal(t, ordermodels.OrderPending, siparis.Status,
		"sipariş bu akıştan 'pending' çıkmalı; 'completed' damgası teslimatın sonucudur "+
			"(Faz 7) ve tamamlanmış bir sipariş artık İPTAL EDİLEMEZ — saga kendi "+
			"telafisini imkânsız kılmış olurdu")
	require.Equal(t, sepetID, siparis.CartID,
		"sipariş doğduğu sepeti belgelemeli; köken kaybolursa mutabakat elle yapılamaz")
	require.Equal(t, musteriID, siparis.CustomerID,
		"sipariş sepetin müşterisine yazılmalı")
	require.Equal(t, eposta, siparis.Email,
		"siparişin iletişim e-postası akışa verilen değer olmalı; sepetin e-postası "+
			"modüller arası yüzeyde yayımlanmaz, bu yüzden ödeme adımında sorulup akışa "+
			"verilir")
	require.Equal(t, vergiliBolgeID, siparis.RegionID,
		"sipariş sepetin bölgesine yazılmalı; bölge vergi oranının ve para biriminin "+
			"bağlamıdır")
	require.Equal(t, vergiliParaBirimi, siparis.CurrencyCode,
		"sipariş sepetin para biriminde açılmalı")

	require.Equal(t, mutluAraToplam, siparis.Subtotal,
		"siparişin ara toplamı sepetinkiyle AYNI olmalı; ayrışma, müşteriye gösterilen "+
			"tutarla faturalanan tutarın farklı olması demektir")
	require.Equal(t, int64(0), siparis.DiscountTotal,
		"indirim üretecek bir kaynak yokken sipariş indirim taşımamalı")
	require.Equal(t, mutluVergi, siparis.TaxTotal,
		"siparişin vergisi sepetinkiyle AYNI olmalı")
	require.Equal(t, int64(0), siparis.ShippingTotal,
		"kargo yöntemi seçilmemişken sipariş kargo tutarı taşımamalı")
	require.Equal(t, mutluToplam, siparis.Total,
		"siparişin genel toplamı sepetinkiyle ve TAHSİL EDİLEN tutarla aynı olmalı")
	require.True(t, siparis.TotalsConsistent(),
		"sipariş toplam kimliğini sağlamalı: total = subtotal - discount + tax + shipping")

	require.Len(t, siparis.Items, 1,
		"sepetin tek satırı siparişe tek satır olarak geçmeli")
	satir := siparis.Items[0]
	require.Equal(t, varyantID, satir.VariantID, "sipariş satırı sepetteki varyantı göstermeli")
	require.Equal(t, varyantBaslik, satir.Title,
		"satır başlığı KATALOGDAN kopyalanmalı; katalog sonradan değişse bile faturadaki "+
			"ad değişmemelidir")
	require.Equal(t, mutluAdet, satir.Quantity, "sipariş satırının adedi sepettekiyle aynı olmalı")
	require.Equal(t, mutluBirimFiyat, satir.UnitPrice,
		"birim fiyat hesap turunda pricing'den alınan değer olmalı")
	require.Equal(t, mutluAraToplam, satir.Subtotal, "satır ara toplamı birim fiyat × adet olmalı")
	require.Equal(t, mutluVergi, satir.TaxTotal, "satır vergisi sepet satırınınkiyle aynı olmalı")
	require.Equal(t, mutluToplam, satir.Total, "satır toplamı sepet satırınınkiyle aynı olmalı")

	// Özet BİLİNÇLİ OLARAK boştur: complete_cart'ın sipariş yüzeyinde
	// (checkoutwf.Orders) özet yazan bir metot yoktur ve tahsil edilen tutarın
	// izi ödeme koleksiyonundadır. İki yerde birden tutulan bir "ödenen tutar"
	// ayrışabilirdi; sipariş ile ödemenin mutabakatı plan Faz 7+'nın işidir.
	require.Equal(t, int64(0), siparis.Summary.PaidTotal,
		"sipariş özeti bu akışta YAZILMAZ; sıfırdan farklı bir değer, ödemenin izinin "+
			"iki yerde birden tutulmaya başlandığını gösterir")

	// Siparişin müşterisi ve bölgesi KENDİ sütunlarındadır; sepetten siparişe
	// taşınan tek yer burasıdır ve süzme de tam olarak bu sütunlardan yapılır.
	require.Equal(t, musteriID, siparis.CustomerID,
		"sipariş, sepetin sahibi olan müşteriye yazılmalı")
	require.Equal(t, vergiliBolgeID, siparis.RegionID,
		"sipariş, sepetin bölgesiyle açılmalı")

	// --- 2) stok rezervasyonu KESİNLEŞTİ mi ---

	require.Len(t, sonuc.ReservationIDs, 1,
		"her sepet satırı için bir rezervasyon alınmalı")
	rezervasyon, err := stokSvc.GetReservation(ctx, sonuc.ReservationIDs[0])
	require.NoError(t, err, "rezervasyon stok modülünden okunabilmeli")
	require.Equal(t, inventorymodels.ReservationConfirmed, rezervasyon.Status,
		"rezervasyon 'confirmed' olmalı: satılan mal fiziksel stoktan DÜŞÜLMÜŞ demektir. "+
			"'active' kalsaydı stok sonsuza kadar ayrılmış görünür, hiç sevk edilmemiş "+
			"gibi davranırdı")
	require.Equal(t, mutluAdet, rezervasyon.Quantity,
		"rezervasyon sepet satırının adedi kadar olmalı")

	require.Equal(t, mutluKalanStok, satilabilirAdet(ctx, t, stokKalemID),
		"satılabilir adet sipariş kadar AZALMALI (%d - %d); azalmazsa aynı mal ikinci "+
			"kez satılabilir", mutluBaslangicStok, mutluAdet)
	seviye := stokSeviyesi(ctx, t, stokKalemID)
	require.Equal(t, mutluKalanStok, seviye.StockedQuantity,
		"FİZİKSEL adet de azalmalı: onay, ayrılan adedi stoktan düşer. Yalnızca "+
			"satılabilir adedin düşmesi rezervasyonun hâlâ 'active' olduğu anlamına gelirdi")
	require.Equal(t, int64(0), seviye.ReservedQuantity,
		"onaydan sonra rezerve adet SIFIRLANMALI; kalması, aynı adedin hem düşülmüş hem "+
			"söz verilmiş sayılması demek olurdu")

	// --- 3) sepet tamamlanmış mı (ikinci yazma Conflict mi) ---

	sepet, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.True(t, sepet.Completed(),
		"sepet tamamlanmış damgalanmalı")

	_, err = akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepetID,
		VariantID: varyantID,
		Quantity:  1,
	})
	require.Error(t, err,
		"tamamlanmış sepete satır EKLENEMEMELİ; eklenebilseydi sipariş edilmiş bir "+
			"sepetin şekli sonradan değişir ve siparişin kökeni yalan olurdu")
	require.True(t, errors.IsConflict(err),
		"tamamlanmış sepete yazma errors.Conflict olmalı (kod: %s); sınıf HTTP'de 409'a "+
			"haritalanır ve istemci 'yeniden dene' ile 'bu sepet kapandı' arasını ayırır. "+
			"Dönen hata: %v", cartwf.CodeCartCompleted, err)

	// --- 4) ödeme tahsil edildi mi ---

	require.NotEmpty(t, sonuc.PaymentID, "sonuç tahsilatın kimliğini taşımalı")
	koleksiyon, err := odemeSvc.GetPaymentCollection(ctx, sonuc.PaymentCollectionID)
	require.NoError(t, err, "ödeme koleksiyonu ödeme modülünden okunabilmeli")
	require.Equal(t, sepetID, koleksiyon.Reference,
		"koleksiyon sepeti referans almalı; referans, ödemeyi hangi işin doğurduğunun "+
			"tek izidir")
	require.Equal(t, mutluToplam, koleksiyon.Amount,
		"koleksiyonun toplanması gereken tutarı siparişin toplamı olmalı")
	require.GreaterOrEqual(t, koleksiyon.CapturedAmount, koleksiyon.Amount,
		"TAHSİL EDİLEN tutar toplanması gerekeni KARŞILAMALI (captured >= amount). "+
			"Kural durum dizesine değil SAYIYA bakar: kısmi tahsilatta durum yine "+
			"'ödeme var' gibi görünebilir ve yalnızca duruma bakan bir kontrol "+
			"ödenmemiş bir siparişi onaylardı")
	require.Equal(t, paymentmodels.CollectionCaptured, koleksiyon.Status,
		"koleksiyonun durumu 'captured' olmalı; durum tutarlardan TÜRETİLİR, dolayısıyla "+
			"bu iddia türetimin de doğru çalıştığını sınar")
	require.Equal(t, int64(0), koleksiyon.AuthorizedAmount,
		"tahsilattan sonra bloke tutar KALMAMALI; kalsaydı aynı para hem müşterinin "+
			"kartında bloke hem mağazada tahsil edilmiş sayılırdı")
	require.Equal(t, int64(0), koleksiyon.RefundedAmount,
		"mutlu yolda iade olmamalı")

	// --- 5) order.placed yayımlandı mı ---

	olay := olayDefteri.bekle(t, sonuc.OrderID)
	require.Equal(t, sonuc.OrderID, olayAlani(t, olay, ordersvc.EventFieldOrderID),
		"olayın yükü siparişin kimliğini taşımalı")
	require.Equal(t, "108000", olayAlani(t, olay, ordersvc.EventFieldTotal),
		"olayın yükü siparişin toplamını ondalıksız DİZE olarak taşımalı")
}

// Saga senaryosunun ELLE hesaplanmış tutarları.
//
// Fiyat, verginin AŞAĞI yuvarlandığını da görünür kılmak için seçilmiştir:
//
//	33_333 × 3 = 99_999 ara toplam
//	99_999 × %20 = 19_999,8 -> AŞAĞI yuvarlanır -> 19_999 vergi
//	99_999 - 0 + 19_999 + 0 = 119_998 genel toplam
const (
	sagaBirimFiyat    int64 = 33_333
	sagaAdet          int64 = 3
	sagaAraToplam     int64 = 99_999
	sagaVergi         int64 = 19_999
	sagaToplam        int64 = 119_998
	sagaBaslangicStok int64 = 7
)

// TestOdemePatlayincaSagaGeriAlir Faz 6 DoD'sinin ÇEKİRDEĞİDİR: ödeme adımı
// patladığında stok rezervasyonunun ve siparişin GERİ ALINDIĞINI kanıtlar.
//
// Kurgu, saga'nın en pahalı arıza noktasını seçer: rezervasyon alınmış ve
// sipariş açılmışken ödeme reddedilir. Yani telafi zinciri BOŞ değildir; iki
// adım ters sırada geri alınmak zorundadır. Sağlayıcı [manual.OutcomeDecline]
// ile reddeder — bu, sağlayıcının ERİŞİLEMEMESİ değil, bilerek verdiği bir
// RET yanıtıdır ve gerçek hayatta en sık görülen ödeme arızasıdır.
//
// Her iddia ayrı ayrı ve NEDEN önemli olduğu söylenerek yazılmıştır: bu testin
// düşmesi "bir çağrı eksik" demek değil, "müşterinin parası alınmadan malı
// kilitlendi ya da olmayan bir siparişi var" demektir.
func TestOdemePatlayincaSagaGeriAlir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, "E2E Saga Ürünü", map[string]int64{
		vergiliParaBirimi: sagaBirimFiyat,
	}, sagaBaslangicStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, sagaAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: sagaAraToplam,
		indirim:   0,
		vergi:     sagaVergi,
		kargo:     0,
		toplam:    sagaToplam,
	}, "saga sepeti hazırlandıktan sonra")

	// Başlangıç durumu ölçülür: "geri alındı" iddiası ancak bir ÖNCESİ varsa
	// anlamlıdır. Sabiti tekrar yazmak yerine okumak, fikstürün gerçekten
	// beklenen stoğu kurduğunu da doğrular.
	oncekiSatilabilir := satilabilirAdet(ctx, t, stokKalemID)
	require.Equal(t, sagaBaslangicStok, oncekiSatilabilir,
		"fikstür stoğu beklenen adetle kurulmuş olmalı")
	oncekiSeviye := stokSeviyesi(ctx, t, stokKalemID)

	// --- akış PATLAMALI ---

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeDecline),
		Email:             eposta,
		ExpectedTotal:     sagaToplam,
	})
	require.Error(t, err,
		"ödeme reddedilmişken akış BAŞARILI dönmemeli; dönseydi ödenmemiş bir sipariş "+
			"sevkiyata girerdi")
	require.Equal(t, checkoutwf.CompleteCartResult{}, sonuc,
		"hata dönen bir akış yarım bir sonuç SIZDIRMAMALI; çağıran hatayı yok saysa bile "+
			"elinde kullanabileceği bir sipariş kimliği olmamalıdır")

	require.True(t, errors.IsConflict(err),
		"ret errors.Conflict olmalı: sunucu arızası değil, dünyanın durumuyla ilgili bir "+
			"çakışmadır ve istemci kartı değiştirip TEKRAR deneyebilir. Internal'a "+
			"yükseltilseydi telafinin başarısız olduğu (elle müdahale gerektiren) "+
			"durumdan ayırt edilemezdi. Dönen hata: %v", err)
	require.ErrorContains(t, err, paymentsvc.CodeAuthorizationDeclined,
		"hata zinciri RET sebebini taşımalı; taşımazsa operatör arızanın ödemede mi "+
			"stokta mı olduğunu kayıttan okuyamaz")
	require.ErrorContains(t, err, checkoutwf.StepAuthorizePayment,
		"hata zinciri PATLAYAN ADIMIN adını taşımalı; yürütme kaydına da bu ad yazılır")

	// --- 1) SİPARİŞ İPTAL EDİLDİ Mİ ---
	//
	// Sipariş, ödeme adımından ÖNCE açılmıştı: create_order saga'nın ikinci
	// adımıdır. Telafi çalışmasaydı geriye "pending" bir sipariş kalırdı —
	// yani ödemesi hiç alınmamış bir sipariş, sevkiyat listesinde sıradaki iş
	// gibi görünürdü. Kimlik akıştan dönmediği için sipariş müşterinin
	// kayıtlarından okunur; bu, gerçek bir operatörün de yapacağı şeydir.
	siparisler, _, err := siparisSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &musteriID})
	require.NoError(t, err, "müşterinin siparişleri okunabilmeli")
	require.Len(t, siparisler, 1,
		"telafi siparişi SİLMEZ, İPTAL EDER: kayıt kalmalı ki denemenin izi kaybolmasın. "+
			"Kaydın hiç bulunmaması, siparişin yazılmadığı (yani testin yanlış adımı "+
			"sınadığı) anlamına gelirdi")
	iptalEdilen := siparisler[0]
	require.Equal(t, ordermodels.OrderCanceled, iptalEdilen.Status,
		"sipariş 'canceled' olmalı. 'pending' kalsaydı: (1) sevkiyat ekibi ödemesi "+
			"alınmamış bir siparişi hazırlardı, (2) müşteri hesabında var olmayan bir "+
			"sipariş görürdü, (3) raporlarda ciro olmayan bir tutar sayılırdı")
	require.NotNil(t, iptalEdilen.CanceledAt,
		"iptal damgası atılmalı; damga, iptalin NE ZAMAN olduğunun tek kaydıdır")
	require.NotEmpty(t, iptalEdilen.CancelReason,
		"iptal GEREKÇESİ yazılmalı; gerekçesiz bir iptal, müşteri sorduğunda "+
			"cevaplanamaz bir kayıttır")
	require.Equal(t, sagaToplam, iptalEdilen.Total,
		"iptal siparişin TUTARINI değiştirmemeli; sipariş 'o an ne satılmak istendi' "+
			"sorusunun kalıcı yanıtıdır ve iptal yalnızca durumunu değiştirir")

	// --- 2) STOK REZERVASYONU GERİ BIRAKILDI MI ---
	//
	// Rezervasyon saga'nın İLK adımıydı, yani telafi zincirinin SONUNCUSU.
	// Geri bırakılmasaydı, satılmamış bir mal ayrılmış kalır ve bir sonraki
	// müşteri "stok yok" görürdü — üstelik hiçbir sipariş o stoğu tüketmemiş
	// olduğu için hata ancak sayımda fark edilirdi.
	require.Equal(t, oncekiSatilabilir, satilabilirAdet(ctx, t, stokKalemID),
		"satılabilir adet ESKİ değerine dönmeli (%d). Dönmezse ayrılan stok asılı kalır "+
			"ve satılmamış mal satılamaz hâle gelir", oncekiSatilabilir)
	sonrakiSeviye := stokSeviyesi(ctx, t, stokKalemID)
	require.Equal(t, oncekiSeviye.StockedQuantity, sonrakiSeviye.StockedQuantity,
		"FİZİKSEL adet hiç değişmemeli: geri bırakma, onaylanmamış bir sözü siler; "+
			"stoktan düşme yalnızca onayla olur")
	require.Equal(t, int64(0), sonrakiSeviye.ReservedQuantity,
		"rezerve adet SIFIRA dönmeli; sıfırdan farklı kalması, sözün hâlâ ayakta "+
			"olduğu anlamına gelir")

	rezervasyonlar, err := stokSvc.ListInventoryLevels(ctx, stokKalemID)
	require.NoError(t, err, "stok seviyeleri okunabilmeli")
	require.Len(t, rezervasyonlar, 1, "fikstür tek lokasyonda seviyelenmiş olmalı")

	// --- 3) SEPET TAMAMLANMAMIŞ OLMALI (hâlâ değiştirilebilir) ---
	//
	// clear_cart saga'nın son adımıdır ve ödeme adımı patladığı için HİÇ
	// çalışmadı. Sepetin açık kalması bir ayrıntı değil, akışın amacıdır:
	// müşteri kartını değiştirip aynı sepetle yeniden deneyebilmelidir.
	sepet, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.False(t, sepet.Completed(),
		"sepet tamamlanmış damgalanMAMALI; damgalansaydı müşteri hem ödeme yapamamış "+
			"hem de sepetini kaybetmiş olurdu")
	require.Len(t, sepet.Items, 1, "sepetin satırı yerinde durmalı")
	require.Equal(t, sagaAdet, sepet.Items[0].Quantity, "sepet satırının adedi değişmemeli")

	guncel, err := akislar.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     sepetID,
		LineItemID: sepet.Items[0].ID,
		Quantity:   1,
	})
	require.NoError(t, err,
		"sepet HÂLÂ DEĞİŞTİRİLEBİLİR olmalı; okunabilir ama yazılamaz bir sepet, "+
			"müşteriye 'yeniden dene' demenin imkânsız olduğu bir durumdur")
	require.Equal(t, int64(1), guncel.Quantity, "güncelleme gerçekten uygulanmalı")

	// --- 4) order.placed: GÖZLENEN davranış ---
	//
	// Olay YAYIMLANMIŞTIR ve bu doğrudur: sipariş bir an için GERÇEKTEN
	// oluşmuştu ve olay, olmuş bir olgunun duyurusudur. Ödeme sonradan
	// reddedildiği için sipariş iptal edildi, ama "önce oldu sonra iptal
	// edildi" ile "hiç olmadı" farklı şeylerdir ve olay ilkini anlatır.
	//
	// Bunun tüketiciler için bir SONUCU vardır ve test onu belgelemek için
	// vardır: "order.placed" tek başına "bu sipariş geçerli" anlamına GELMEZ.
	// Aboneler (fatura, bildirim, muhasebe) siparişin güncel durumunu okumak
	// zorundadır. Yükte durum alanı bulunması da tam bu yüzden anlamlıdır.
	//
	// İptali duyuran ayrı bir olay (örn. "order.canceled") bugün YOKTUR; plan
	// Faz 6 yalnızca order.placed'ı ister. Eklendiği gün bu blok, aboneye iki
	// olayın da geldiğini iddia edecek biçimde büyümelidir.
	olay := olayDefteri.bekle(t, iptalEdilen.ID)
	require.Equal(t, ordermodels.OrderPending.String(), olayAlani(t, olay, ordersvc.EventFieldStatus),
		"olay siparişin YAYIM ANINDAKİ durumunu taşır ve o an 'pending'di; olay "+
			"yayımlandıktan sonraki iptal yükü geriye dönük DEĞİŞTİRMEZ. Abone güncel "+
			"durumu siparişten okumak zorundadır")
	require.Equal(t, "119998", olayAlani(t, olay, ordersvc.EventFieldTotal),
		"olay siparişin toplamını ondalıksız DİZE olarak taşımalı")
}

// order.placed senaryosunun ELLE hesaplanmış tutarları.
//
//	12_000 × 1 = 12_000 ara toplam
//	12_000 × %20 = 2_400 vergi
//	12_000 - 0 + 2_400 + 0 = 14_400 genel toplam
const (
	olayBirimFiyat    int64 = 12_000
	olayAdet          int64 = 1
	olayAraToplam     int64 = 12_000
	olayVergi         int64 = 2_400
	olayToplam        int64 = 14_400
	olayBaslangicStok int64 = 5
)

// TestSiparisPlacedOlayiYayimlanir Faz 6 DoD'sinin olay yarısını kanıtlar:
// "order.placed" GERÇEKTEN yayımlanır ve yükü sözleşmedeki alanları taşır.
//
// Abone core.eventbus'a TestMain'de bağlanmıştır (bkz. [siparisOlayDefteri]);
// üretimdeki veri yolunun aynısıdır ve testte hiçbir sahte yoktur. Olayın
// yalnızca "geldiğini" görmek yetmez: her alan MODÜLLER ARASI SÖZLEŞMEDİR ve
// eksik ya da tipi kaymış bir alan, aboneleri sessizce düşürür.
func TestSiparisPlacedOlayiYayimlanir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, _ := yeniStokluVaryant(ctx, t, "E2E Olay Ürünü", map[string]int64{
		vergiliParaBirimi: olayBirimFiyat,
	}, olayBaslangicStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, olayAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: olayAraToplam,
		indirim:   0,
		vergi:     olayVergi,
		kargo:     0,
		toplam:    olayToplam,
	}, "olay sepeti hazırlandıktan sonra")

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     olayToplam,
	})
	require.NoError(t, err, "sepet siparişe çevrilebilmeli")

	olay := olayDefteri.bekle(t, sonuc.OrderID)

	require.Equal(t, ordersvc.EventOrderPlaced, olay.Name,
		"olayın adı sözleşmedeki ad olmalı; Redis backend'inde ad aynı zamanda STREAM "+
			"adıdır ve değişmesi tüm abonelerin sessizce olay almayı bırakması demektir")
	require.NotEmpty(t, olay.ID,
		"olayın kimliği dolu olmalı; tüketiciler onu idempotency anahtarı olarak kullanır")
	require.False(t, olay.OccurredAt.IsZero(), "olayın gerçekleşme anı damgalanmalı")

	siparis, err := siparisSvc.GetOrder(ctx, sonuc.OrderID)
	require.NoError(t, err, "sipariş okunabilmeli")

	require.Equal(t, sonuc.OrderID, olayAlani(t, olay, ordersvc.EventFieldOrderID),
		"yük siparişin kimliğini taşımalı; abonenin ayrıntıya ulaşmasının TEK yolu odur")
	require.Equal(t, "14400", olayAlani(t, olay, ordersvc.EventFieldTotal),
		"yük siparişin toplamını ondalıksız DİZE olarak taşımalı: %d minor unit -> %q. "+
			"Sayı olarak taşınsaydı Redis backend'inde float64'e çözülür ve 2^53 üstü "+
			"tutarlar sessizce yuvarlanırdı (plan Bölüm 8: float ASLA)", olayToplam, "14400")
	require.Equal(t, ordermodels.OrderPending.String(), olayAlani(t, olay, ordersvc.EventFieldStatus),
		"yük siparişin durumunu taşımalı")
	require.Equal(t, vergiliBolgeID, olayAlani(t, olay, ordersvc.EventFieldRegionID),
		"yük siparişin bölgesini taşımalı")
	require.Equal(t, musteriID, olayAlani(t, olay, ordersvc.EventFieldCustomerID),
		"yük siparişin müşterisini taşımalı")
	require.Equal(t, vergiliParaBirimi, olayAlani(t, olay, ordersvc.EventFieldCurrencyCode),
		"yük siparişin para birimini taşımalı; tutar tek başına anlamsızdır")
	require.Equal(t, "1", olayAlani(t, olay, ordersvc.EventFieldItemCount),
		"yük satır sayısını ondalıksız DİZE olarak taşımalı")
	require.NotEmpty(t, olayAlani(t, olay, ordersvc.EventFieldDisplayID),
		"yük müşteriye gösterilen sipariş numarasını taşımalı")
	require.Equal(t, siparis.PlacedAt.UTC().Format("2006-01-02"),
		olayAlani(t, olay, ordersvc.EventFieldPlacedAt)[:len("2006-01-02")],
		"yükteki zaman damgası siparişin PlacedAt değerinden gelmeli")

	// E-posta BİLİNÇLİ OLARAK yükte yoktur: olaylar Redis'e yazılır ve orada
	// KALICIDIR; kişisel veriyi kalıcı bir akışa koymak, siparişin kendisinde
	// zaten duran bir bilgi için gereksiz bir yayılımdır (plan Bölüm 8).
	require.NotContains(t, olay.Data, "email",
		"yük e-posta TAŞIMAMALI; kalıcı bir olay akışına kişisel veri konmaz")
}

// Stok yetersizliği senaryosunun sabitleri.
//
// Sepetin adedi stoktan FAZLADIR; tutarların doğruluğu bu senaryonun konusu
// değildir, çünkü akış hesabı yaptıktan sonra İLK adımda durur.
const (
	yetersizBirimFiyat    int64 = 20_000
	yetersizAdet          int64 = 5
	yetersizBaslangicStok int64 = 2
)

// TestStokYetersizIkenSiparisOlusmaz stoktan fazlasını isteyen bir sepetin
// siparişe DÖNMEDİĞİNİ doğrular.
//
// Adım saga'nın İLKİDİR, yani telafi edilecek hiçbir şey yoktur; sınanan şey
// akışın burada DURMASI ve arkasında hiçbir iz bırakmamasıdır. Stok kontrolü
// ayırma çağrısının İÇİNDE, kilit altında yapılır: önceden yapılan bir "yeterli
// mi" okuması yarışa açık bir kopya olurdu (bkz. checkoutwf.Inventory).
func TestStokYetersizIkenSiparisOlusmaz(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, "E2E Yetersiz Stok Ürünü", map[string]int64{
		vergiliParaBirimi: yetersizBirimFiyat,
	}, yetersizBaslangicStok)

	sepetID, _ := sepetHazirla(ctx, t, musteriID, varyantID, yetersizAdet)

	_, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
	})
	require.Error(t, err,
		"stoktan fazlası sipariş EDİLEMEMELİ; edilebilseydi mağaza teslim edemeyeceği "+
			"bir malın parasını tahsil ederdi")
	require.True(t, errors.IsConflict(err),
		"yetersiz stok errors.Conflict olmalı: girdi geçerlidir, dünyanın durumu "+
			"elverişsizdir ve istemci adedi düşürüp TEKRAR deneyebilir. Dönen hata: %v", err)
	require.ErrorContains(t, err, checkoutwf.StepReserveInventory,
		"hata PATLAYAN ADIMI adlandırmalı; adım adı yürütme kaydına da yazılır ve "+
			"operatörün gördüğü tek şeydir")

	// --- sipariş OLUŞMAMALI ---
	siparisler, toplamSayi, err := siparisSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &musteriID})
	require.NoError(t, err, "müşterinin siparişleri okunabilmeli")
	require.Empty(t, siparisler,
		"sipariş HİÇ oluşmamalı: create_order, reserve_inventory'den SONRA gelir ve "+
			"stok adımı patladığı için hiç çalışmamalıdır. İptal edilmiş bile olsa bir "+
			"sipariş kaydı, hiç denenmemiş bir siparişin var olduğu anlamına gelirdi")
	require.Zero(t, toplamSayi, "sayaç da sıfır olmalı")

	// --- ödeme adımına HİÇ gidilmemeli ---
	require.Equal(t, yetersizBaslangicStok, satilabilirAdet(ctx, t, stokKalemID),
		"stok hiç DOKUNULMAMIŞ olmalı; kısmi bir ayırma (örneğin eldeki 2 adet) yapılıp "+
			"bırakılsaydı, sepetin tamamı karşılanamadığı hâlde stok geçici olarak "+
			"kilitlenirdi")
	seviye := stokSeviyesi(ctx, t, stokKalemID)
	require.Equal(t, int64(0), seviye.ReservedQuantity,
		"rezerve adet sıfır kalmalı; ayırma ya TAMAMEN olur ya hiç olmaz")

	sepet, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "sepet okunabilmeli")
	require.False(t, sepet.Completed(),
		"sepet açık kalmalı; müşteri adedi düşürüp yeniden deneyebilmelidir")
}

// Tekrar senaryosunun ELLE hesaplanmış tutarları.
//
//	25_000 × 2 = 50_000 ara toplam
//	50_000 × %20 = 10_000 vergi
//	50_000 - 0 + 10_000 + 0 = 60_000 genel toplam
const (
	tekrarBirimFiyat    int64 = 25_000
	tekrarAdet          int64 = 2
	tekrarAraToplam     int64 = 50_000
	tekrarVergi         int64 = 10_000
	tekrarToplam        int64 = 60_000
	tekrarBaslangicStok int64 = 4
)

// TestAyniSepetIkiKezTamamlanamaz başarıyla sipariş edilmiş bir sepetin ikinci
// kez sipariş edilemediğini doğrular ve ikinci çağrının NEREDE durduğunu
// belgeler.
//
// # Gözlenen davranış
//
// İkinci çağrı saga motoruna HİÇ ULAŞMAZ. Hazırlık aşaması (prepare) motorun
// idempotency denetiminden ÖNCE çalışır ve ilk işi hesabı yenilemektir; oysa
// başarılı ilk yürütme sepeti tamamlanmış damgalamıştır ve cart modülü
// tamamlanmış bir sepette hesap yapmayı reddeder. Dolayısıyla dönen hata sepet
// hesabının çakışmasıdır (kod: cartwf.CodeCartCompleted), motorun "bu anahtar
// zaten kullanıldı" cevabı değil.
//
// # Neden bu DOĞRU davranıştır
//
// Üç savunma hattı vardır ve ikinci çağrı EN UCUZUNA çarpar:
//
//  1. Sepet damgası (buradaki hat): hiçbir dış çağrı yapılmadan durur.
//  2. Motorun idempotency anahtarı ("complete_cart:<sepet>"): kayıt
//     veritabanındadır, yani İKİ REPLİKA aynı sepeti aynı anda sipariş edemez.
//     Damga tek başına buna yetmezdi.
//  3. Modüllerin kendi idempotency korumaları (sipariş anahtarı, ödeme oturumu
//     anahtarı): saga bir adımı yeniden denerse ikinci bir sipariş ya da ikinci
//     bir tahsilat doğmaz.
//
// Hattın ucuz olanı önce çarpması istenendir: ikinci çağrı ne stok ayırır, ne
// sipariş açar, ne ödeme sağlayıcısına gider. Hatanın SINIFI da bu yüzden
// önemlidir — Conflict, istemciye "bu iş zaten yapıldı" der; Internal deseydi
// istemci gereksiz yere yeniden denerdi.
func TestAyniSepetIkiKezTamamlanamaz(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	varyantID, stokKalemID := yeniStokluVaryant(ctx, t, "E2E Tekrar Ürünü", map[string]int64{
		vergiliParaBirimi: tekrarBirimFiyat,
	}, tekrarBaslangicStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, tekrarAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: tekrarAraToplam,
		indirim:   0,
		vergi:     tekrarVergi,
		kargo:     0,
		toplam:    tekrarToplam,
	}, "tekrar sepeti hazırlandıktan sonra")

	girdi := checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     tekrarToplam,
	}

	ilk, err := siparisAkislari.CompleteCart(ctx, girdi)
	require.NoError(t, err, "ilk çağrı sepeti siparişe çevirebilmeli")
	require.True(t, ilk.CartCompleted, "ilk çağrı sepeti tamamlanmış damgalamalı")

	// --- ikinci çağrı ---

	ikinci, err := siparisAkislari.CompleteCart(ctx, girdi)
	require.Error(t, err,
		"aynı sepet İKİNCİ KEZ sipariş edilememeli; edilebilseydi müşteriden aynı "+
			"sepet için iki kez tahsilat yapılır ve stok iki kez düşülürdü")
	require.Equal(t, checkoutwf.CompleteCartResult{}, ikinci,
		"ikinci çağrı yarım bir sonuç SIZDIRMAMALI")
	require.True(t, errors.IsConflict(err),
		"ikinci çağrı errors.Conflict olmalı; istemci bunu 'bu iş zaten yapıldı' diye "+
			"okur ve yeniden denemez. Dönen hata: %v", err)
	require.Equal(t, cartwf.CodeCartCompleted, errors.CodeOf(err),
		"ikinci çağrı SEPET HESABINDA durmalı (kod: %s): hazırlık motorun idempotency "+
			"denetiminden ÖNCE çalışır ve tamamlanmış sepette hesap yapılamaz. Kodun "+
			"değişmesi, akışın daha pahalı bir hatta (motor ya da modül koruması) "+
			"çarpmaya başladığını gösterir ve o hat dış çağrı yapmış olabilir. "+
			"Dönen hata: %v", cartwf.CodeCartCompleted, err)

	// --- ikinci çağrı hiçbir yan etki bırakmamalı ---

	siparisler, _, err := siparisSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &musteriID})
	require.NoError(t, err, "müşterinin siparişleri okunabilmeli")
	require.Len(t, siparisler, 1,
		"yalnızca TEK sipariş oluşmalı; ikinci bir sipariş, aynı sepetin iki kez "+
			"satıldığı anlamına gelirdi")
	require.Equal(t, ilk.OrderID, siparisler[0].ID,
		"kalan sipariş ilk çağrının siparişi olmalı")

	require.Equal(t, tekrarBaslangicStok-tekrarAdet, satilabilirAdet(ctx, t, stokKalemID),
		"stok yalnızca BİR KEZ düşmeli (%d - %d); ikinci bir düşüm, hiç satılmamış malı "+
			"stoktan silerdi", tekrarBaslangicStok, tekrarAdet)

	koleksiyon, err := odemeSvc.GetPaymentCollection(ctx, ilk.PaymentCollectionID)
	require.NoError(t, err, "ödeme koleksiyonu okunabilmeli")
	require.Equal(t, tekrarToplam, koleksiyon.CapturedAmount,
		"tahsil edilen tutar TEK siparişin toplamı kadar olmalı; iki katı olsaydı "+
			"müşteriden aynı sepet için iki kez para çekilmiş olurdu")
}

// TestSiparisAkisiUretimKablolamasiylaKurulur sipariş tamamlama akışının ÜRETİM
// kayıtlarıyla kurulabildiğini AYRICA doğrular.
//
// Diğer senaryolar akışı KULLANIR; bu test onun KURULABİLDİĞİNİ sınar ve bir
// imza kayması olduğunda hangi yüzeyin eksik ya da uyumsuz olduğunu
// container'ın tipli hatasıyla söyler. Ayrım önemlidir çünkü kablolama derleme
// zamanında denetlenmez: yüzeyler adla çözülür ve modüller bu paketi (ADR 0006
// gereği) tanımaz, dolayısıyla uyum ancak çalışma zamanında kanıtlanabilir.
func TestSiparisAkisiUretimKablolamasiylaKurulur(t *testing.T) {
	akis, err := checkoutwf.FromContainer(kap)
	require.NoError(t, err,
		"sipariş tamamlama akışı container'daki ÜRETİM kayıtlarıyla kurulabilmeli; "+
			"hata, hangi yüzeyin eksik olduğunu yazar")
	require.NotNil(t, akis)
}

// sepetHazirla kayıtlı bir müşteriye sepet açar, tek satır ekler ve sepetin
// kimliğiyle hesaplanmış toplamlarını döner.
//
// Sepet KAYITLI müşteriye açılır çünkü Faz 6 senaryoları siparişleri müşteriye
// göre okur: misafir siparişinin customer_id'si boştur ve testler birbirinin
// siparişini görürdü. Sepet açma ve satır ekleme sepet AKIŞLARIYLA yapılır
// (cart modülünün servisiyle değil), böylece fiyat ve vergi tam olarak Faz
// 5'teki yoldan gelir.
func sepetHazirla(
	ctx context.Context,
	t *testing.T,
	musteriID, varyantID string,
	adet int64,
) (sepetID string, toplamlar cartwf.Totals) {
	t.Helper()

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: vergiliUlke,
		CustomerID:  musteriID,
	})
	require.NoError(t, err, "fikstür sepeti açılamadı")

	eklendi, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  adet,
	})
	require.NoError(t, err, "fikstür sepetine satır eklenemedi")

	return sepet.CartID, eklendi.Totals
}

// odemeDavranisi manuel sağlayıcının yetkilendirme davranışını belirleyen
// oturum verisini üretir.
//
// Veri akışa PaymentData olarak verilir ve sağlayıcıya OLDUĞU GİBİ iletilir;
// saga'nın kendisinde hiçbir test kancası yoktur. Anahtar ve değerler manual
// paketinin sabitlerinden gelir: dize olarak yazılsalardı, sağlayıcı sözleşmeyi
// değiştirdiğinde testler derlenmeye devam eder ama sessizce VARSAYILAN
// davranışı sınamaya başlardı.
func odemeDavranisi(t *testing.T, sonuc string) json.RawMessage {
	t.Helper()

	ham, err := json.Marshal(map[string]string{manual.DataKeyOutcome: sonuc})
	require.NoError(t, err, "ödeme davranış verisi kodlanamadı")
	return ham
}
