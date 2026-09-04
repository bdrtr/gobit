//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// expectedTotal bir hesap turunun ELLE hesaplanmış sonucudur.
//
// Alanlar üretim kodundaki formülle değil, senaryonun içinde kâğıt üstünde
// hesaplanıp sabit yazılan sayılarla doldurulur; gerekçesi paket yorumundadır.
type expectedTotal struct {
	subtotal int64
	discount int64
	tax      int64
	shipping int64
	total    int64
}

// assertTotals hesaplanan sepet toplamlarını elle yazılmış beklentiyle
// karşılaştırır.
//
// asama, hangi adımdan sonraki hesabın sınandığını söyler; aynı testte birden
// çok tur koştuğu için hata mesajının turu adlandırması şarttır.
func assertTotals(t *testing.T, gercek cartwf.Totals, beklenen expectedTotal, asama string) {
	t.Helper()

	require.Equal(t, beklenen.subtotal, gercek.Subtotal,
		"%s: ara total yanlış. Ara total satırların (birim fiyat × adet) toplamıdır; "+
			"yanlışsa müşteriye maldan farklı bir bedel gösterilir.", asama)
	require.Equal(t, beklenen.discount, gercek.DiscountTotal,
		"%s: discount yanlış. İndirim Faz 7'den beri promotion modülünden gelir ve "+
			"yalnızca kendi hedef kuralıyla eşleşen kalemlere iner; beklenenden farklı "+
			"bir değer, ya hesabın kalemi yanlış tanıdığını ya da bir promosyonun "+
			"hedeflemediği sepetlere sızdığını gösterir.", asama)
	require.Equal(t, beklenen.tax, gercek.TaxTotal,
		"%s: tax yanlış. Vergi satır başına, discount SONRASI taban üzerinden ve AŞAĞI "+
			"yuvarlanarak hesaplanır (workflows/cart, \"Vergi sözleşmesi\"); sapma "+
			"doğrudan yanlış tahsilattır.", asama)
	require.Equal(t, beklenen.shipping, gercek.ShippingTotal,
		"%s: shipping yanlış. Kargo, sepete seçilmiş yöntemlerin toplamıdır; yöntem "+
			"seçilmemişken sıfırdan farklı olması, hesaba kaynağı belirsiz bir tutarın "+
			"girdiğini gösterir.", asama)
	require.Equal(t, beklenen.total, gercek.Total,
		"%s: genel total yanlış. Kimlik her turda sağlanmalı: "+
			"total = subtotal - discount + tax + shipping. Sağlanmazsa cart modülü "+
			"yazmayı zaten reddeder, yani bu sapma sepetin hiç güncellenemediği "+
			"anlamına da gelir.", asama)
}

// Misafir sepeti senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir:
//
//	2 adet: 12_500 × 2 = 25_000 ara toplam
//	        25_000 × %20 = 5_000 vergi
//	        25_000 - 0 + 5_000 + 0 = 30_000 genel toplam
//	3 adet: 12_500 × 3 = 37_500 ara toplam
//	        37_500 × %20 = 7_500 vergi
//	        37_500 - 0 + 7_500 + 0 = 45_000 genel toplam
const (
	misafirBirimFiyat     int64 = 12_500
	misafirAraToplam2Adet int64 = 25_000
	misafirVergi2Adet     int64 = 5_000
	misafirToplam2Adet    int64 = 30_000
	misafirAraToplam3Adet int64 = 37_500
	misafirVergi3Adet     int64 = 7_500
	misafirToplam3Adet    int64 = 45_000
)

// TestMisafirSepetiTamAkis Faz 5 DoD'sinin misafir yolunu uçtan uca koşturur.
//
// Zincir: bölge/para birimi çözümü -> sepet açma -> katalogdan başlık kopyalama
// -> link üzerinden fiyat kümesi bulma -> pricing'den fiyat -> satır yazma ->
// hesap turu -> adet güncelleme -> ikinci hesap turu -> sepetin veritabanından
// yeniden okunması. Halkalardan biri koparsa hata burada görünür; birim
// testleri bu zinciri sahte bağımlılıklarla kurduğu için göremezdi.
func TestMisafirSepetiTamAkis(t *testing.T) {
	ctx := t.Context()

	const varyantBaslik = "E2E Misafir Ürünü"
	varyantID := newVariant(ctx, t, varyantBaslik, map[string]int64{
		taxedCurrency: misafirBirimFiyat,
	})

	sepet, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err,
		"misafir sepeti açılabilmeli: hesabı olmayan bir müşterinin alışverişe başlaması "+
			"customer modülünün ayakta olmasına bağlı olmamalıdır")
	require.True(t, sepet.Guest,
		"customer_id verilmediğinde sepet MİSAFİR sayılmalı; aksi hâlde akış olmayan bir "+
			"müşteriyi doğrulamaya çalışır ve misafir alışverişi tümden kapanır")
	require.Equal(t, taxedRegionID, sepet.RegionID,
		"bölge ülke kodundan çözülmeli; yanlış bölge yanlış tax oranı ve yanlış fiyat "+
			"bağlamı demektir")
	require.Equal(t, taxedCurrency, sepet.CurrencyCode,
		"para birimi bölgeden KOPYALANMALI; sepet başka bir para biriminde açılırsa "+
			"pricing o para biriminde fiyat bulamaz ve satır hiç eklenemez")

	// --- satır ekleme ve ilk hesap turu ---

	eklendi, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  2,
	})
	require.NoError(t, err, "fiyatı tanımlı bir varyant sepete eklenebilmeli")
	require.Equal(t, varyantBaslik, eklendi.Title,
		"satırın başlığı KATALOGDAN kopyalanmalı (Query katmanı üzerinden); çağıranın "+
			"gönderdiği serbest metne güvenmek, var olmayan bir varyantın sepete "+
			"girmesine izin verirdi")
	require.Equal(t, misafirBirimFiyat, eklendi.UnitPrice,
		"birim fiyat pricing'den gelmeli; sepetin kendi tuttuğu bir tutara güvenmek "+
			"katalogda değişen fiyatı sepette dondururdu")

	assertTotals(t, eklendi.Totals, expectedTotal{
		subtotal: misafirAraToplam2Adet,
		discount: 0,
		tax:      misafirVergi2Adet,
		shipping: 0,
		total:    misafirToplam2Adet,
	}, "2 adet eklendikten sonra")

	require.Len(t, eklendi.Totals.Lines, 1,
		"hesap sepetin TÜM satırlarını kapsamalı; eksik satır cart modülünce reddedilir "+
			"ve sepet hiç güncellenmez")
	satir := eklendi.Totals.Lines[0]
	require.Equal(t, eklendi.LineItemID, satir.LineItemID,
		"satır tutarları eklenen satıra ait olmalı; başka bir satıra yazılan tutar "+
			"sessizce yanlış fatura üretir")
	require.Equal(t, misafirAraToplam2Adet, satir.Subtotal,
		"satır ara toplamı birim fiyat × adet olmalı; cart modülü bu çarpımı ayrıca "+
			"doğruladığı için sapma yazmayı düşürür")
	require.Equal(t, misafirVergi2Adet, satir.TaxTotal,
		"satır vergisi satırın discount sonrası tabanı üzerinden hesaplanmalı; faturada "+
			"her satırın vergisi tek tek açıklanabilir olmalıdır")
	require.Equal(t, int64(0), satir.DiscountTotal,
		"satır indirimi Faz 5'te daima sıfır olmalı; sıfırdan farklıysa discount kaynağı "+
			"olmayan bir hesap tutar düşürüyor demektir")

	// --- adet güncelleme ve ikinci hesap turu ---

	guncel, err := workflows.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     sepet.CartID,
		LineItemID: eklendi.LineItemID,
		Quantity:   3,
	})
	require.NoError(t, err, "satır adedi güncellenebilmeli")
	require.False(t, guncel.Removed,
		"pozitif adet satırı KALDIRMAMALI; kaldırma yalnızca sıfır adedin niyetidir")
	require.Equal(t, int64(3), guncel.Quantity,
		"adet MUTLAK değer olarak yazılmalı; eklenmiş gibi yorumlanırsa müşteri "+
			"istediğinden fazlasını öder")

	assertTotals(t, guncel.Totals, expectedTotal{
		subtotal: misafirAraToplam3Adet,
		discount: 0,
		tax:      misafirVergi3Adet,
		shipping: 0,
		total:    misafirToplam3Adet,
	}, "adet 3'e çıkarıldıktan sonra")

	// --- sepetin veritabanından yeniden okunması ---

	detay, err := cartSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.False(t, detay.TotalsStale(),
		"toplamlar sepetin GÜNCEL şekline damgalanmış olmalı; bayat kalırlarsa sepetin "+
			"sipariş olması ayrıca reddedilir ve müşteri ödeme adımında takılır")
	require.Equal(t, guncel.Totals.Subtotal, detay.Subtotal,
		"okunan ara total akışın döndürdüğüyle BİREBİR aynı olmalı; ayrışma, müşteriye "+
			"gösterilen tutarla tahsil edilen tutarın farklı olması demektir")
	require.Equal(t, guncel.Totals.DiscountTotal, detay.DiscountTotal,
		"okunan discount akışın döndürdüğüyle birebir aynı olmalı")
	require.Equal(t, guncel.Totals.TaxTotal, detay.TaxTotal,
		"okunan tax akışın döndürdüğüyle birebir aynı olmalı")
	require.Equal(t, guncel.Totals.ShippingTotal, detay.ShippingTotal,
		"okunan shipping akışın döndürdüğüyle birebir aynı olmalı")
	require.Equal(t, guncel.Totals.Total, detay.Total,
		"okunan genel total akışın döndürdüğüyle birebir aynı olmalı; ödeme adımı bu "+
			"sayıyı kullanır")

	require.Len(t, detay.Items, 1,
		"aynı varyant tek satırda toplanmalı; ikinci satır, fiyat kademesini ve Faz 6'daki "+
			"stok rezervasyonunu bölerdi")
	require.Equal(t, int64(3), detay.Items[0].Quantity,
		"saklanan adet güncellenmiş değer olmalı")
	require.Equal(t, misafirBirimFiyat, detay.Items[0].UnitPrice,
		"saklanan birim fiyat hesap turunda pricing'den yeniden alınan değer olmalı")
	require.Equal(t, misafirAraToplam3Adet, detay.Items[0].Subtotal,
		"saklanan satır ara toplamı elle hesaplanan değerle aynı olmalı")
	require.Equal(t, misafirVergi3Adet, detay.Items[0].TaxTotal,
		"saklanan satır vergisi elle hesaplanan değerle aynı olmalı")
}

// Kayıtlı müşteri senaryosunun ELLE hesaplanmış tutarları.
//
// Fiyat, verginin AŞAĞI yuvarlandığını görünür kılmak için seçilmiştir:
//
//	9_999 × 2 = 19_998 ara toplam
//	19_998 × %20 = 3_999,6 -> AŞAĞI yuvarlanır -> 3_999 vergi
//	19_998 - 0 + 3_999 + 0 = 23_997 genel toplam
//
// Yakına yuvarlansaydı vergi 4_000 çıkardı; aradaki 1 minor unit daima
// müşterinin LEHİNE bırakılır (workflows/cart, "Vergi sözleşmesi", 3. karar).
const (
	kayitliBirimFiyat int64 = 9_999
	kayitliAraToplam  int64 = 19_998
	kayitliVergi      int64 = 3_999
	kayitliToplam     int64 = 23_997
)

// TestKayitliMusteriSepeti Faz 5 DoD'sinin kayıtlı müşteri yolunu uçtan uca
// koşturur.
//
// Misafir yolundan iki farkı vardır ve ikisi de burada sınanır: müşteri
// DOĞRULANIR (yoksa sepet hiç açılmaz) ve sepet müşteriye BAĞLANIR. Bağın
// gerçekten kurulduğu, sepetin kendi sütunundan değil link servisinden
// okunarak doğrulanır.
func TestKayitliMusteriSepeti(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := newCustomer(ctx, t)
	varyantID := newVariant(ctx, t, "E2E Kayıtlı Müşteri Ürünü", map[string]int64{
		taxedCurrency: kayitliBirimFiyat,
	})

	sepet, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		CustomerID:  musteriID,
	})
	require.NoError(t, err, "kayıtlı müşteri sepeti açılabilmeli")
	require.False(t, sepet.Guest,
		"customer_id verildiğinde sepet misafir SAYILMAMALI; misafir sayılırsa müşterinin "+
			"sepeti hesabıyla ilişkilendirilmez")
	require.Equal(t, musteriID, sepet.CustomerID,
		"sepetin sahibi istenen müşteri olmalı")
	require.Equal(t, eposta, sepet.Email,
		"e-posta verilmediğinde müşterinin KAYITLI adresi sepete geçmeli; adressiz bir "+
			"sepet, aynı bilgiyi ödeme adımında yeniden sormak demektir")

	// --- sepetin bölgesi de akışın kurduğu bölge olmalı ---

	require.Equal(t, taxedRegionID, sepet.RegionID,
		"sepet, akışa verilen bölgeyle açılmalı; bölge sepetin KENDİ sütunudur ve "+
			"tax ile para birimi tam olarak oradan okunur")

	// --- misafir yolundaki hesap zinciri kayıtlı müşteride de aynı çalışır ---

	eklendi, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  2,
	})
	require.NoError(t, err, "kayıtlı müşterinin sepetine satır eklenebilmeli")

	assertTotals(t, eklendi.Totals, expectedTotal{
		subtotal: kayitliAraToplam,
		discount: 0,
		tax:      kayitliVergi,
		shipping: 0,
		total:    kayitliToplam,
	}, "kayıtlı müşteri sepetine 2 adet eklendikten sonra")

	guncel, err := workflows.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     sepet.CartID,
		LineItemID: eklendi.LineItemID,
		Quantity:   1,
	})
	require.NoError(t, err, "kayıtlı müşterinin satır adedi güncellenebilmeli")

	// 9_999 × 1 = 9_999 ara toplam; 9_999 × %20 = 1_999,8 -> 1_999 vergi;
	// 9_999 - 0 + 1_999 + 0 = 11_998 genel toplam.
	assertTotals(t, guncel.Totals, expectedTotal{
		subtotal: 9_999,
		discount: 0,
		tax:      1_999,
		shipping: 0,
		total:    11_998,
	}, "adet 1'e düşürüldükten sonra")

	detay, err := cartSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.Equal(t, musteriID, detay.CustomerID,
		"sepetin sütunundaki müşteri kimliği de doğru olmalı; sütun kaynak, link ise "+
			"onun aynasıdır ve ikisi ayrışmamalıdır")
	require.Equal(t, guncel.Totals.Total, detay.Total,
		"okunan genel total akışın döndürdüğüyle birebir aynı olmalı")
	require.False(t, detay.TotalsStale(),
		"toplamlar güncel şekle damgalanmış olmalı")
}

// TestSepetAkislariUretimKablolamasiylaKurulur sepet akışlarının ÜRETİM
// kablolamasıyla kurulabildiğini doğrular.
//
// Regresyon: cart.service, akışların beklediği ilkel yüzeyi karşılamıyordu;
// cartwf.FromContainer tipli bir uyumsuzlukla düşüyordu ve DoD ancak testte
// kurulan bir köprüyle sınanabiliyordu. Eksik olan, region/pricing/customer'da
// zaten bulunan şeydi: interop.go. Artık cart da onu yayımlıyor ve
// "cart.interop" adıyla kayıtlı.
//
// Bu test kablolamayı AYRICA sınar: diğer senaryolar akışları kullanır, bu
// test onların KURULABİLDİĞİNİ doğrular ve bir imza kayması olduğunda hangi
// yüzeyin eksik olduğunu container'ın tipli hatasıyla söyler.
func TestSepetAkislariUretimKablolamasiylaKurulur(t *testing.T) {
	workflows, err := cartwf.FromContainer(ctr)
	require.NoError(t, err,
		"sepet akışları ctr'daki ÜRETİM kayıtlarıyla kurulabilmeli; "+
			"hata, hangi yüzeyin eksik olduğunu yazar")
	require.NotNil(t, workflows)
}
