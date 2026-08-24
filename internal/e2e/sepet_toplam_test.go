//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Çok satırlı sepet senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir. Fiyatlar, satır başına yuvarlamanın
// sepet düzeyinde tek seferde yuvarlamadan AYRIŞTIĞI bir nokta seçilerek
// belirlenmiştir:
//
//	A: 3_333 × 1 =  3_333 ara toplam; 3_333 × %20 =   666,6 ->   666 vergi
//	B: 6_667 × 1 =  6_667 ara toplam; 6_667 × %20 = 1_333,4 -> 1_333 vergi
//	C: 10_000 × 2 = 20_000 ara toplam; 20_000 × %20 = 4_000,0 -> 4_000 vergi
//
//	Σ ara toplam = 3_333 + 6_667 + 20_000 = 30_000
//	Σ satır vergisi =   666 + 1_333 +  4_000 =  5_999
//	Genel toplam = 30_000 - 0 + 5_999 + 0 = 35_999
//
// # Yuvarlama farkı NEREDE kalıyor
//
// Sepet ara toplamı tek seferde vergilenseydi 30_000 × %20 = 6_000 çıkardı.
// Sözleşme vergiyi SATIR BAŞINA hesaplar ve her satırda AŞAĞI yuvarlar; A ve B
// satırlarındaki 0,6 + 0,4 kesirleri ayrı ayrı atıldığı için sepet vergisi
// 5_999'da kalır. Aradaki 1 minor unit MÜŞTERİNİN LEHİNEDİR ve kaybolmaz:
// hiçbir satıra yazılmaz, dolayısıyla faturada da görünmez. Satır başına hesap
// bilinçli seçimdir (workflows/cart, "Vergi sözleşmesi", 2. karar): faturada
// her satırın vergisi tek tek açıklanabilir olmalıdır ve Faz 7'de satır başına
// FARKLI oranlar geldiğinde tabanın tanımı değişmemelidir.
const (
	cokSatirFiyatA int64 = 3_333
	cokSatirFiyatB int64 = 6_667
	cokSatirFiyatC int64 = 10_000

	cokSatirAraToplamA int64 = 3_333
	cokSatirAraToplamB int64 = 6_667
	cokSatirAraToplamC int64 = 20_000

	cokSatirVergiA int64 = 666
	cokSatirVergiB int64 = 1_333
	cokSatirVergiC int64 = 4_000

	cokSatirAraToplam int64 = 30_000
	cokSatirVergi     int64 = 5_999
	cokSatirToplam    int64 = 35_999

	// cokSatirSepetDuzeyiVergi sepet ara toplamı TEK SEFERDE vergilenseydi
	// çıkacak tutardır; sözleşme bu yolu seçmez ve test farkın nerede
	// kaldığını bu sabitle belgeler.
	cokSatirSepetDuzeyiVergi int64 = 6_000
)

// TestCokSatirliSepetToplamTutarliligi üç farklı fiyatlı satırda sepet
// toplamlarının satır toplamlarıyla tutarlı olduğunu doğrular.
//
// İki iddia sınanır: Σ(satır ara toplamı) sepetin ara toplamına EŞİTTİR (cart
// modülü bunu ayrıca doğrular ve sağlanmazsa hiçbir şey yazılmaz) ve
// Σ(satır vergisi) sepetin vergisine EŞİTTİR — sepet vergisi tanım gereği satır
// vergilerinin toplamıdır, bağımsız bir hesap değildir.
func TestCokSatirliSepetToplamTutarliligi(t *testing.T) {
	ctx := t.Context()

	varyantA := yeniVaryant(ctx, t, "E2E Çok Satır A", map[string]int64{vergiliParaBirimi: cokSatirFiyatA})
	varyantB := yeniVaryant(ctx, t, "E2E Çok Satır B", map[string]int64{vergiliParaBirimi: cokSatirFiyatB})
	varyantC := yeniVaryant(ctx, t, "E2E Çok Satır C", map[string]int64{vergiliParaBirimi: cokSatirFiyatC})

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergiliUlke})
	require.NoError(t, err, "sepet açılabilmeli")

	satirA, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: sepet.CartID, VariantID: varyantA, Quantity: 1,
	})
	require.NoError(t, err, "A satırı eklenebilmeli")
	satirB, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: sepet.CartID, VariantID: varyantB, Quantity: 1,
	})
	require.NoError(t, err, "B satırı eklenebilmeli")
	sonuc, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: sepet.CartID, VariantID: varyantC, Quantity: 2,
	})
	require.NoError(t, err, "C satırı eklenebilmeli")

	toplamlariDogrula(t, sonuc.Totals, beklenenToplam{
		araToplam: cokSatirAraToplam,
		indirim:   0,
		vergi:     cokSatirVergi,
		kargo:     0,
		toplam:    cokSatirToplam,
	}, "üç satır eklendikten sonra")

	require.Len(t, sonuc.Totals.Lines, 3,
		"hesap sepetin ÜÇ satırını da kapsamalı; eksik satır cart modülünce reddedilir")

	// Satırlar kimliğe göre eşlenir: sıralamaya bağlı bir iddia, satır sırasının
	// ileride değişmesi hâlinde yanlış satırı doğrulamış olurdu.
	satirlar := make(map[string]cartwf.LineTotals, len(sonuc.Totals.Lines))
	var araToplamlarinToplami, vergilerinToplami int64
	for _, satir := range sonuc.Totals.Lines {
		satirlar[satir.LineItemID] = satir
		araToplamlarinToplami += satir.Subtotal
		vergilerinToplami += satir.TaxTotal
	}

	require.Equal(t, araToplamlarinToplami, sonuc.Totals.Subtotal,
		"Σ(satır ara toplamı) sepetin ara toplamına EŞİT olmalı; eşit değilse cart modülü "+
			"hesabı hiç yazmaz ve sepetin toplamları bayat kalır")
	require.Equal(t, cokSatirAraToplam, araToplamlarinToplami,
		"satırların ara toplamları elle hesaplanan 30_000'e eşit olmalı")
	require.Equal(t, vergilerinToplami, sonuc.Totals.TaxTotal,
		"Σ(satır vergisi) sepetin vergisine EŞİT olmalı; sepet vergisi bağımsız bir hesap "+
			"değil, satır vergilerinin toplamıdır")

	// Satır başına elle hesaplanan tutarlar.
	for _, beklenen := range []struct {
		ad        string
		id        string
		birim     int64
		araToplam int64
		vergi     int64
	}{
		{"A", satirA.LineItemID, cokSatirFiyatA, cokSatirAraToplamA, cokSatirVergiA},
		{"B", satirB.LineItemID, cokSatirFiyatB, cokSatirAraToplamB, cokSatirVergiB},
		{"C", sonuc.LineItemID, cokSatirFiyatC, cokSatirAraToplamC, cokSatirVergiC},
	} {
		satir, bulundu := satirlar[beklenen.id]
		require.True(t, bulundu, "%s satırının tutarları hesapta bulunmalı", beklenen.ad)
		require.Equal(t, beklenen.birim, satir.UnitPrice,
			"%s satırının birim fiyatı pricing'in seçtiği fiyat olmalı; her satır KENDİ "+
				"fiyat kümesinden fiyatlanır ve kümeler karışırsa yanlış ürün ücretlendirilir",
			beklenen.ad)
		require.Equal(t, beklenen.araToplam, satir.Subtotal,
			"%s satırının ara toplamı elle hesaplanan değere eşit olmalı", beklenen.ad)
		require.Equal(t, beklenen.vergi, satir.TaxTotal,
			"%s satırının vergisi kendi tabanı üzerinden AŞAĞI yuvarlanarak hesaplanmalı",
			beklenen.ad)
		require.Equal(t, beklenen.araToplam+beklenen.vergi, satir.Total,
			"%s satırının toplamı subtotal - discount + tax olmalı", beklenen.ad)
	}

	// Yuvarlama farkının NEREDE kaldığı açıkça belgelenir.
	require.Equal(t, int64(1), cokSatirSepetDuzeyiVergi-sonuc.Totals.TaxTotal,
		"satır başına yuvarlama ile sepet düzeyinde tek seferde yuvarlama arasındaki fark "+
			"tam olarak 1 minor unit olmalı: A satırındaki 0,6 ve B satırındaki 0,4 kesirleri "+
			"ayrı ayrı AŞAĞI atılır. Fark hiçbir satıra yazılmaz, yani faturada görünmez ve "+
			"müşteriden tahsil edilmez")
	require.Less(t, sonuc.Totals.TaxTotal, cokSatirSepetDuzeyiVergi,
		"yuvarlama daima MÜŞTERİ LEHİNE olmalı; yakına yuvarlama müşteriden fazla tahsil "+
			"eder ve \"fazlası nereden geldi\" sorusunu mutabakata bırakırdı")

	detay, err := sepetSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.Len(t, detay.Items, 3, "üç ayrı varyant üç ayrı satır olmalı")
	require.Equal(t, cokSatirToplam, detay.Total,
		"saklanan genel toplam elle hesaplanan değere eşit olmalı")
	require.False(t, detay.TotalsStale(), "toplamlar güncel şekle damgalanmış olmalı")
}

// Vergisiz bölge senaryosunun ELLE hesaplanmış tutarları.
//
//	5_000 × 2 = 10_000 ara toplam
//	vergi 0 (ülkenin tax modülünde vergi bölgesi YOKTUR; bölgenin taşıdığı %19
//	         oran da uygulanmaz)
//	10_000 - 0 + 0 + 0 = 10_000 genel toplam
const (
	vergisizBirimFiyat int64 = 5_000
	vergisizAraToplam  int64 = 10_000
	vergisizToplam     int64 = 10_000
)

// TestVergisizBolgedeVergiSifir vergisi olmayan bir bölgede sepetin sıfır vergi
// taşıdığını doğrular.
//
// # Faz 7'de verginin sıfır kalmasının SEBEBİ değişti
//
// Faz 5'te sebep bölgenin bayrağıydı: automatic_taxes kapalıydı ve akış onu
// dinliyordu. Faz 7'de vergiyi tax modülü devraldı ve bu ülkeye bir vergi
// bölgesi kurulmadı ([vergiFiksturleriniKur]); tax "yapılandırma yok" diye
// YETKİLİ bir cevap verir ve region'a geri düşülmez.
//
// İki sebep AYNI tutarı üretir, yani sepetin toplamı hangisinin geçerli
// olduğunu söylemez. Ayrımı yapan tek şey [cartwf.Totals.TaxSource] alanıdır ve
// bu senaryo tam da onu sınar: alan olmasaydı, devralmanın bu bölgede hiç
// çalışmadığı da aynı sayılarla gizlenebilirdi.
//
// Bölgenin hâlâ sıfır OLMAYAN bir oran taşıması ([vergisizOranBps]) bir kalıntı
// değildir: region yolu SİLİNMEDİ, geri düşüş yolu olarak duruyor ve o yola
// düşülseydi verginin görülebilir bir değeri olurdu.
func TestVergisizBolgedeVergiSifir(t *testing.T) {
	ctx := t.Context()

	oran, otomatik, err := bolgeSvc.RegionTax(ctx, vergisizBolgeID)
	require.NoError(t, err, "bölgenin vergi ayarı okunabilmeli")
	require.False(t, otomatik,
		"fikstür bölgesi otomatik vergiyi KAPALI tutmalı; Faz 5'te verginin sıfır "+
			"çıkmasının sebebi buydu ve fikstür o hâliyle korunur")
	require.Equal(t, vergisizOranBps, oran,
		"fikstür bölgesi sıfır OLMAYAN bir oran taşımalı; verginin sıfır çıkması oranın "+
			"küçüklüğünden gelmemelidir")

	_, bulundu, err := vergiInterop.RateForCountry(ctx, vergisizUlke)
	require.NoError(t, err, "tax yüzeyinden oran sorgulanabilmeli")
	require.False(t, bulundu,
		"%s ülkesinin tax modülünde vergi bölgesi OLMAMALI; bu senaryonun sıfırı "+
			"yapılandırma yokluğundan gelir", vergisizUlke)

	varyantID := yeniVaryant(ctx, t, "E2E Vergisiz Ürün", map[string]int64{
		vergisizParaBirimi: vergisizBirimFiyat,
	})

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergisizUlke})
	require.NoError(t, err, "vergisiz bölgede sepet açılabilmeli")
	require.Equal(t, vergisizBolgeID, sepet.RegionID,
		"sepet vergisiz bölgeye bağlanmalı")
	require.Equal(t, vergisizParaBirimi, sepet.CurrencyCode,
		"sepetin para birimi vergisiz bölgeninkiyle aynı olmalı; farklı olsaydı fiyat hiç "+
			"bulunamaz ve test verginin değil fiyatın yokluğunu sınardı")

	eklendi, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  2,
	})
	require.NoError(t, err, "vergisiz bölgede de satır eklenebilmeli")

	toplamlariDogrula(t, eklendi.Totals, beklenenToplam{
		araToplam: vergisizAraToplam,
		indirim:   0,
		vergi:     0,
		kargo:     0,
		toplam:    vergisizToplam,
	}, "vergisiz bölgede 2 adet eklendikten sonra")

	require.Equal(t, cartwf.TaxSourceTaxUnconfigured, eklendi.Totals.TaxSource,
		"vergiyi TAX modülü hesaplamış ve ülkenin YAPILANDIRILMAMIŞ olduğunu bildirmiş "+
			"olmalı. Kaynak %q çıksaydı hesap region yoluna düşmüş olurdu; tutar yine "+
			"sıfır olacağı için fark başka hiçbir iddiada görünmez, yani devralmanın bu "+
			"bölgede hiç çalışmadığı sessizce geçerdi",
		cartwf.TaxSourceRegion)

	require.Len(t, eklendi.Totals.Lines, 1, "tek satır beklenir")
	require.Equal(t, int64(0), eklendi.Totals.Lines[0].TaxTotal,
		"satır vergisi de sıfır olmalı; sepet vergisi satır vergilerinin toplamı olduğu "+
			"için satırda kalan bir vergi sepette de görünürdü")
	require.Equal(t, vergisizAraToplam, eklendi.Totals.Lines[0].Total,
		"vergisiz satırın toplamı ara toplamına eşit olmalı")
}

// TestFiyatiOlmayanVaryantSepeteGiremez fiyatsız bir varyantın reddedildiğini
// doğrular.
//
// Beklenen karar paket godoc'unda yazılıdır (workflows/cart, priceSetsFor):
// istek errors.Invalid ile REDDEDİLİR. NotFound DEĞİLDİR, çünkü varyant
// VARDIR; eksik olan onun satılabilir olmasıdır ve çağıran isteği düzeltebilir.
// İzin verilseydi birim fiyatı sıfır olan bir satır açılır ve sepet sessizce
// ucuzlardı — cart modülünün toplam sözleşmesinin kapatmaya çalıştığı sessiz
// para kaybı tam olarak budur.
func TestFiyatiOlmayanVaryantSepeteGiremez(t *testing.T) {
	ctx := t.Context()

	// Fiyat kümesi HİÇ kurulmaz: varyant "product_variant_price_set" bağına
	// sahip değildir.
	fiyatsizVaryant := yeniVaryant(ctx, t, "E2E Fiyatsız Ürün", nil)

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergiliUlke})
	require.NoError(t, err, "sepet açılabilmeli")

	_, err = akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: fiyatsizVaryant,
		Quantity:  1,
	})
	require.Error(t, err,
		"fiyatı olmayan varyant sepete GİRMEMELİ; girseydi birim fiyatı sıfır olan bir "+
			"satır açılır ve sepet sessizce ucuzlardı")
	require.True(t, errors.IsInvalid(err),
		"hata errors.Invalid olmalı (422): varyant VARDIR, eksik olan satılabilir "+
			"olmasıdır ve çağıran isteği düzeltebilir. NotFound (404) olsaydı istemci "+
			"düzeltilebilir bir durumu kayıp sanardı. Gelen: %v", err)
	require.Equal(t, cartwf.CodeVariantNotPriced, errors.CodeOf(err),
		"hata kodu %q olmalı; istemciler mesaja değil koda göre dallanır",
		cartwf.CodeVariantNotPriced)

	detay, err := sepetSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err, "sepet reddedilen istekten sonra da okunabilmeli")
	require.Empty(t, detay.Items,
		"reddedilen istek sepete DOKUNMAMALI; yarım yazılmış bir satır, müşterinin hiç "+
			"onaylamadığı bir ürünü sepette bırakırdı")
	require.Equal(t, int64(0), detay.Total,
		"dokunulmamış sepetin toplamı sıfır kalmalı")
}

// TestSepetinParaBirimindeFiyatiOlmayanVaryantReddedilir fiyat kümesi olan ama
// sepetin para biriminde fiyatı bulunmayan varyantı sınar.
//
// Bu, fiyatsız varyanttan AYRI bir durumdur ve akış ikisini ayrı kodlarla
// bildirir: burada bağ vardır ve okunur, eksik olan yalnızca o para birimindeki
// fiyattır. Ayrımın sınanması gerekir, çünkü tek bir koda indirgenseydi
// "ürünün fiyatı yok" ile "bu ülkede satılmıyor" aynı görünürdü.
func TestSepetinParaBirimindeFiyatiOlmayanVaryantReddedilir(t *testing.T) {
	ctx := t.Context()

	// Fiyat yalnızca USD'de tanımlıdır; sepet ise TRY para biriminde açılacaktır.
	yanlisParaBirimliVaryant := yeniVaryant(ctx, t, "E2E Yalnızca USD Fiyatlı Ürün",
		map[string]int64{"USD": 4_200})

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergiliUlke})
	require.NoError(t, err, "sepet açılabilmeli")

	_, err = akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: yanlisParaBirimliVaryant,
		Quantity:  1,
	})
	require.Error(t, err,
		"sepetin para biriminde fiyatı olmayan varyant sepete girmemeli")
	require.True(t, errors.IsInvalid(err),
		"hata errors.Invalid olmalı; pricing'in NotFound'u burada yeniden sınıflandırılır, "+
			"çünkü eksik olan varyant değil o para birimindeki fiyattır. Gelen: %v", err)
	require.Equal(t, cartwf.CodePriceUnavailable, errors.CodeOf(err),
		"hata kodu %q olmalı ve fiyatsız varyantın kodundan (%q) AYRI kalmalı; ikisi farklı "+
			"düzeltmeler gerektirir",
		cartwf.CodePriceUnavailable, cartwf.CodeVariantNotPriced)
}

// TestKargoluSepetVergiTabaninaGirmez kargo yolunu uçtan uca sınar.
//
// Kapsam boşluğuydu: beş senaryonun hiçbiri kargo yöntemi eklemiyordu, yani
// ShippingTotal her turda 0 bekleniyor ve kargo hesabı GERÇEK yığında hiç
// çalışmıyordu. workflows/cart'ın "Vergi sözleşmesi" başlığındaki kararı
// (kargo vergi tabanına GİRMEZ), kargonun cart_shipping_methods tablosundan
// okunup anlık görüntüye taşınması ve cart.SetTotals'ın shipping_total
// sütunuyla kimlik doğrulaması — üçü de yalnızca birim testlerde, sahte
// anlık görüntü üzerinde sınanıyordu.
func TestKargoluSepetVergiTabaninaGirmez(t *testing.T) {
	ctx := t.Context()

	const birimFiyat int64 = 10_000
	varyantID := yeniVaryant(ctx, t, "Kargolu ürün", map[string]int64{vergiliParaBirimi: birimFiyat})

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: vergiliUlke,
		Email:       "kargo@ornek.test",
	})
	require.NoError(t, err)

	_, err = akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  1,
	})
	require.NoError(t, err)

	// Kargo yöntemi sepet servisinden eklenir; akış onu anlık görüntüde görmeli.
	const kargoTutari int64 = 4_990
	_, err = sepetSvc.AddShippingMethod(ctx, sepet.CartID, cartsvc.AddShippingMethodInput{
		Name:   "Standart kargo",
		Amount: kargoTutari,
	})
	require.NoError(t, err, "kargo yöntemi eklenebilmeli")

	toplamlar, err := akislar.CalculateTotals(ctx, sepet.CartID)
	require.NoError(t, err)

	// Beklentiler ELLE hesaplanır; üretim formülü tekrar edilmez.
	//   ara toplam = 10.000
	//   vergi tabanı = ara toplam (kargo HARİÇ) -> 10.000 × %20 = 2.000
	//   toplam = 10.000 - 0 + 2.000 + 4.990 = 16.990
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: 10_000,
		indirim:   0,
		vergi:     2_000,
		kargo:     4_990,
		toplam:    16_990,
	}, "kargo eklendikten sonra")

	// Sözleşmenin ASIL iddiası: kargo vergilenmiş olsaydı vergi 2.998 olurdu
	// ((10.000 + 4.990) × %20). Bu ayrı iddia, yukarıdaki 2.000'in tesadüfen
	// değil KARAR GEREĞİ o değer olduğunu belgeler.
	const kargoVergilenseydi int64 = 2_998
	require.NotEqual(t, kargoVergilenseydi, toplamlar.TaxTotal,
		"kargo vergi tabanına GİRMEMELİ; girseydi vergi %d olurdu", kargoVergilenseydi)

	// Sepetten okunan toplam, akışın döndürdüğüyle birebir aynı olmalı.
	detay, err := sepetSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err)
	require.Equal(t, toplamlar.ShippingTotal, detay.ShippingTotal,
		"kargo tutarı veritabanına yazılmalı; SetTotals'ın kimlik kontrolü buna dayanır")
	require.Equal(t, toplamlar.Total, detay.Total)
}
