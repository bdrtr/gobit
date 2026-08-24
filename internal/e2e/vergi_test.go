//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Bu dosya planın Faz 7 DoD'sinin VERGİ ayağını kanıtlar: "vergi region'a göre
// hesaplanıyor".
//
// Faz 5'te vergi region modülünün tek düz oranından geliyordu (RegionTax ->
// rateBps, automatic) ve region'ın godoc'u bunu "Faz 7'de tax modülü
// devralacak" diye geçici işaretlemişti. Devralma yapıldı; bu dosya onun
// SONUCUNU sınar.
//
// # Devralmayı "vergi doğru çıktı" ile kanıtlamak YETMEZ
//
// İki yetkili de aynı ülkeye aynı oranı bildirseydi, hesap hangisini
// kullanırsa kullansın sonuç aynı çıkardı ve test yeşil kalırken hiçbir şey
// kanıtlanmazdı. Bu yüzden Faz 7 bölgelerinin fikstürü bilinçli olarak
// ÇELİŞKİLİDİR: region bir oran, tax başka bir oran söyler ve tutar hangisinin
// dinlendiğini tek başına ele verir (bkz. [ikinciBolgeRegionOraniBps],
// [yapilandirilmamisRegionOraniBps]).
//
// # Ülke nereden geliyor
//
// Sepet kargo adresini modüller arası yüzeyde YAYIMLAMAZ; sepet akışı vergi
// ülkesini bölgeden, Query katmanı üzerinden okur ve yalnızca bölge TEK bir
// ülkeye bağlıysa kullanır. Fikstürdeki her bölge tam olarak bir ülke taşır
// (bkz. [bolgeFiksturleriniKur]); taşımasaydı hesap tax'a hiç sormaz, sessizce
// region yoluna düşer ve bu dosyadaki her iddia anlamını yitirirdi.

// İki bölgeli vergi senaryosunun ELLE hesaplanmış tutarları.
//
// Aynı varyant, aynı fiyat, aynı adet ve aynı para birimi; tek değişken vergi
// ORANIDIR.
//
//	ara toplam = 10_000 × 3 = 30_000 (her iki bölgede de)
//
//	[vergiliUlke]      -> tax %20: 30_000 × %20 = 6_000; toplam 36_000
//	[ikinciVergiUlke]  -> tax %10: 30_000 × %10 = 3_000; toplam 33_000
//
// İkinci bölgenin KENDİ (region) oranı %50'dir ve otomatik vergisi AÇIKTIR;
// hesap onu kullansaydı vergi 15_000 çıkardı. Aradaki fark, devralmanın
// gerçekten yapıldığının ölçüsüdür.
const (
	vergiBirimFiyat int64 = 10_000
	vergiAdet       int64 = 3
	vergiAraToplam  int64 = 30_000

	// vergiliBolgeVergisi [vergiliUlke] bölgesinde beklenen vergidir (%20).
	vergiliBolgeVergisi int64 = 6_000
	// vergiliBolgeToplami [vergiliUlke] bölgesinde beklenen genel toplamdır.
	vergiliBolgeToplami int64 = 36_000

	// ikinciBolgeVergisi [ikinciVergiUlke] bölgesinde beklenen vergidir (%10).
	ikinciBolgeVergisi int64 = 3_000
	// ikinciBolgeToplami [ikinciVergiUlke] bölgesinde beklenen genel toplamdır.
	ikinciBolgeToplami int64 = 33_000
	// ikinciBolgeRegionVergisi bölgenin KENDİ oranı uygulansaydı çıkacak
	// vergidir (%50); hiçbir turda görülmemelidir.
	ikinciBolgeRegionVergisi int64 = 15_000
)

// Vergi bölgesi yapılandırılmamış ülkenin ELLE hesaplanmış tutarları.
//
//	ara toplam = 30_000; vergi 0; toplam 30_000
//
// Bölgenin kendi oranı %18'dir ve otomatik vergisi AÇIKTIR; hesap region'a
// geri düşseydi vergi 5_400 çıkardı.
const (
	yapilandirilmamisToplam int64 = 30_000
	// yapilandirilmamisRegionVergisi region oranı uygulansaydı çıkacak
	// vergidir; görülmemelidir.
	yapilandirilmamisRegionVergisi int64 = 5_400
)

// TestAyniUrunIkiBolgedeFarkliVergiUretir aynı ürünün iki bölgede FARKLI vergi
// ürettiğini doğrular.
//
// Tek bir varyant iki ayrı sepete konur; sepetlerin bölgesi (dolayısıyla vergi
// ülkesi) farklıdır, geri kalan her şey aynıdır. Ara toplamların eşit olması
// karşılaştırmanın ön koşuludur: eşit olmasalardı vergi farkı orandan değil
// fiyattan gelebilirdi.
func TestAyniUrunIkiBolgedeFarkliVergiUretir(t *testing.T) {
	ctx := t.Context()

	varyantID := yeniVaryant(ctx, t, "E2E İki Bölgeli Ürün", map[string]int64{
		vergiliParaBirimi: vergiBirimFiyat,
	})

	vergili := bolgedeSepetHesabi(ctx, t, vergiliUlke, varyantID, vergiAdet)
	ikinci := bolgedeSepetHesabi(ctx, t, ikinciVergiUlke, varyantID, vergiAdet)

	// --- 1) her iki bölgenin tutarları elle hesaplanan değerlerle aynı mı ---

	toplamlariDogrula(t, vergili, beklenenToplam{
		araToplam: vergiAraToplam,
		indirim:   0,
		vergi:     vergiliBolgeVergisi,
		kargo:     0,
		toplam:    vergiliBolgeToplami,
	}, "%20 vergili bölgede")

	toplamlariDogrula(t, ikinci, beklenenToplam{
		araToplam: vergiAraToplam,
		indirim:   0,
		vergi:     ikinciBolgeVergisi,
		kargo:     0,
		toplam:    ikinciBolgeToplami,
	}, "%10 vergili ikinci bölgede")

	// --- 2) fark GERÇEKTEN bölgeden mi geliyor ---

	require.Equal(t, vergili.Subtotal, ikinci.Subtotal,
		"iki sepetin ara toplamı AYNI olmalı; farklı olsaydı vergi farkının orandan mı "+
			"fiyattan mı geldiği ayırt edilemezdi")
	require.NotEqual(t, vergili.TaxTotal, ikinci.TaxTotal,
		"aynı ürün iki bölgede FARKLI vergi üretmeli. Eşit çıkması, vergi ülkesinin "+
			"sepetin bölgesinden okunmadığını ve iki sepetin de aynı yargı bölgesiyle "+
			"hesaplandığını gösterir")
	require.Equal(t, vergiliBolgeVergisi-ikinciBolgeVergisi, vergili.TaxTotal-ikinci.TaxTotal,
		"vergi farkı elle hesaplanan 3_000 olmalı: 30_000 × (%%20 - %%10)")

	// --- 3) yetkili TAX modülü mü, yoksa hâlâ region mı ---

	require.Equal(t, cartwf.TaxSourceTax, vergili.TaxSource,
		"%s bölgesinin vergisini tax modülü hesaplamış olmalı", vergiliUlke)
	require.Equal(t, cartwf.TaxSourceTax, ikinci.TaxSource,
		"%s bölgesinin vergisini tax modülü hesaplamış olmalı", ikinciVergiUlke)
	require.NotEqual(t, ikinciBolgeRegionVergisi, ikinci.TaxTotal,
		"ikinci bölgenin vergisi, bölgenin KENDİ %%50'lik oranıyla hesaplanmış olmamalı. "+
			"15_000 çıkması devralmanın yapılmadığını, verginin hâlâ region modülünden "+
			"okunduğunu söylerdi — ve fikstür tam olarak bunu görünür kılmak için iki "+
			"yetkiliye farklı oranlar yazar")

	oran, otomatik, err := bolgeSvc.RegionTax(ctx, ikinciVergiBolgeID)
	require.NoError(t, err, "ikinci bölgenin region ayarı okunabilmeli")
	require.True(t, otomatik,
		"ikinci bölge otomatik vergiyi AÇIK tutmalı; kapalı olsaydı region yolu da sıfır "+
			"üretir ve iki yetkiliyi ayırt edemezdik")
	require.Equal(t, ikinciBolgeRegionOraniBps, oran,
		"ikinci bölge region tarafında %%50 taşımalı; taşımasaydı yukarıdaki iddia "+
			"boşa çıkardı")
}

// TestVergiBolgesiOlmayanUlkedeVergiSifir tax modülünde vergi bölgesi
// YAPILANDIRILMAMIŞ bir ülkede verginin sıfır kaldığını ve region'a geri
// DÜŞÜLMEDİĞİNİ doğrular.
//
// Sınanan şey bir tutar değil bir OTORİTE kuralıdır (bkz. cartwf applyTaxes,
// "Otorite TEK'tir ve KURULUMDA seçilir"): tax çağrılmış ve "bu ülkenin vergi
// bölgesi yok" diye YETKİLİ bir cevap vermiştir; cevabı olmayan bir otorite
// değil, cevabı olan bir otorite konuşmuştur ve önceki yetkili devreye
// alınmaz.
//
// Bölge, sıfır OLMAYAN bir oran taşır ve otomatik vergisi açıktır. Bu
// bilinçlidir: sıfır oranlı bir bölgeyle kurulsaydı "region'a düşülmedi" ile
// "region'a düşüldü ama oran zaten sıfırdı" ayırt edilemezdi.
func TestVergiBolgesiOlmayanUlkedeVergiSifir(t *testing.T) {
	ctx := t.Context()

	oran, otomatik, err := bolgeSvc.RegionTax(ctx, yapilandirilmamisBolgeID)
	require.NoError(t, err, "bölgenin region ayarı okunabilmeli")
	require.True(t, otomatik,
		"bölge otomatik vergiyi AÇIK tutmalı; kapalı olsaydı verginin sıfır çıkması "+
			"hiçbir şey kanıtlamazdı")
	require.Equal(t, yapilandirilmamisRegionOraniBps, oran,
		"bölge sıfır OLMAYAN bir region oranı taşımalı")

	_, bulundu, err := vergiInterop.RateForCountry(ctx, yapilandirilmamisUlke)
	require.NoError(t, err, "tax yüzeyinden oran sorgulanabilmeli")
	require.False(t, bulundu,
		"%s ülkesinin tax modülünde vergi bölgesi OLMAMALI; olsaydı bu senaryo "+
			"yapılandırma yokluğunu değil, yapılandırılmış bir oranı sınardı",
		yapilandirilmamisUlke)

	varyantID := yeniVaryant(ctx, t, "E2E Vergi Bölgesiz Ürün", map[string]int64{
		vergiliParaBirimi: vergiBirimFiyat,
	})
	toplamlar := bolgedeSepetHesabi(ctx, t, yapilandirilmamisUlke, varyantID, vergiAdet)

	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: vergiAraToplam,
		indirim:   0,
		vergi:     0,
		kargo:     0,
		toplam:    yapilandirilmamisToplam,
	}, "vergi bölgesi yapılandırılmamış ülkede")

	require.NotEqual(t, yapilandirilmamisRegionVergisi, toplamlar.TaxTotal,
		"vergi, bölgenin %%18'lik region oranıyla hesaplanmış OLMAMALI. 5_400 çıkması, "+
			"tax'ın yetkili \"bölge yok\" cevabının yok sayılıp önceki yetkiliye geri "+
			"dönüldüğünü gösterirdi; o zaman verginin hangi otoriteden geldiği, hangi "+
			"ülkeye kayıt girildiğine göre sessizce değişirdi")
	require.Equal(t, cartwf.TaxSourceTaxUnconfigured, toplamlar.TaxSource,
		"kaynak %q olmalı. Bu değer %q'dan ayrı tutulur çünkü sıfır vergi iki farklı "+
			"sebepten doğar: oran gerçekten sıfırdır ya da o ülke için hiç yapılandırma "+
			"yoktur. Ayrımı yutan bir alan, eksik kurulumu \"vergisiz ülke\" sanmaya "+
			"davetiye olurdu",
		cartwf.TaxSourceTaxUnconfigured, cartwf.TaxSourceTax)
	require.NotEqual(t, cartwf.TaxSourceRegion, toplamlar.TaxSource,
		"kaynak %q OLMAMALI; region yoluna düşülmüş olsaydı vergi zaten sıfır "+
			"çıkmazdı ve bu alan, iki yolun karıştığını gösteren tek işaret olurdu",
		cartwf.TaxSourceRegion)
}

// TestVergiYuzeyiUlkeyeGoreFarkliOranBildirir tax modülünde iki ülkeye FARKLI
// oran kurulduğunu ve yüzeyin ikisini ayırt ettiğini doğrular.
//
// Sepet hesabı bu metodu hiç çağırmaz — kalem başına vergi ister ve daima
// CalculateTaxJSON kullanır. [vergiYuzeyi.RateForCountry] yine de sınanır:
// region modülünün geçici RegionTax metodunun birebir karşılığı odur ve
// devralmanın "eski yüzeyin yerine geçen yeni yüzey" tarafını başka hiçbir
// üretim paketi çağırmıyor.
//
// İkinci dönüş değerinin ANLAMI da burada sabitlenir: region'daki bayrak
// "vergiyi uygula/uygulama" tercihiydi, buradaki ise "yapılandırma var mı"
// bilgisidir.
func TestVergiYuzeyiUlkeyeGoreFarkliOranBildirir(t *testing.T) {
	ctx := t.Context()

	for _, senaryo := range []struct {
		ulke     string
		oran     int32
		bulundu  bool
		aciklama string
	}{
		{vergiliUlke, vergiOraniBps, true, "Faz 5/6 senaryolarının dayandığı oran"},
		{ikinciVergiUlke, ikinciVergiOraniBps, true, "ikinci bölgenin oranı"},
		{yapilandirilmamisUlke, 0, false, "vergi bölgesi kurulmamış ülke"},
	} {
		oran, bulundu, err := vergiInterop.RateForCountry(ctx, senaryo.ulke)
		require.NoError(t, err, "%s için oran sorgulanabilmeli", senaryo.ulke)
		require.Equal(t, senaryo.bulundu, bulundu,
			"%s (%s): yapılandırmanın VARLIĞI doğru bildirilmeli. Bu bayrak olmadan "+
				"çağıran, oranı gerçekten sıfır olan bir ülkeyi hiç yapılandırılmamış "+
				"ülkeden ayıramaz ve eksik kurulumla sessizce satış yapardı",
			senaryo.ulke, senaryo.aciklama)
		require.Equal(t, senaryo.oran, oran,
			"%s (%s): oran fikstürde yazılan değer olmalı", senaryo.ulke, senaryo.aciklama)
	}

	require.NotEqual(t, vergiOraniBps, ikinciVergiOraniBps,
		"iki ülkenin oranı FARKLI kurulmuş olmalı; aynı olsalardı \"aynı ürün iki "+
			"bölgede farklı vergi üretir\" iddiası sınanamazdı")
}

// bolgedeSepetHesabi verilen ülkede bir sepet açar, tek satır ekler ve hesabın
// sonucunu döner.
//
// Sepet ülke KODUYLA açılır, bölge kimliğiyle değil: bölgeyi ülkeden çözen
// adım da akışın kendi işidir ve fikstürün onu atlaması, yanlış bölgeye
// bağlanan bir sepeti testten gizlerdi.
func bolgedeSepetHesabi(
	ctx context.Context,
	t *testing.T,
	ulkeKodu, varyantID string,
	adet int64,
) cartwf.Totals {
	t.Helper()

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: ulkeKodu})
	require.NoError(t, err, "%s ülkesinde sepet açılamadı", ulkeKodu)

	eklendi, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: varyantID,
		Quantity:  adet,
	})
	require.NoError(t, err, "%s sepetine satır eklenemedi", ulkeKodu)

	return eklendi.Totals
}

// TestCokUlkeliBolgedeVergiRegiondanHesaplanir verginin REGION'a düştüğü yolu
// gerçek kablolamayla kanıtlar.
//
// Kapsam boşluğuydu: fikstürlerin hepsi TEK ülkeli bölgeler taşıyordu ve tax
// daima kayıtlıydı, dolayısıyla hiçbir e2e turunda TaxSource "region"
// ÜRETİLMİYORDU. Geri düşüş yolu yalnızca bellek içi sahtelerle sınanıyordu.
//
// Tetikleyici üretimde son derece sıradan: çok ülkeli bir "Avrupa" bölgesi.
// Sepet hesabı vergi ülkesini bölgeden okur; bölge birden çok ülke taşıdığında
// hangisinin sorulacağı bilinemez, tax'a HİÇ sorulmaz ve region oranı kullanılır.
// Bu yol sessizce vergi otoritesini değiştirir, o yüzden kaynağın raporlanması
// ve burada kilitlenmesi şarttır.
func TestCokUlkeliBolgedeVergiRegiondanHesaplanir(t *testing.T) {
	ctx := t.Context()

	// Ön koşul: bölge GERÇEKTEN çok ülkeli olmalı, yoksa test başka bir yolu sınar.
	require.Len(t, cokUlkeliUlkeler, 2,
		"senaryo bölgenin BİRDEN ÇOK ülke taşımasına dayanır")

	// Ön koşul: bu ülkelerin tax modülünde bölgesi OLMAMALI; olsaydı testin
	// sınadığı şey "ülke çözülemedi" değil, "tax cevap verdi" olurdu.
	for _, ulke := range cokUlkeliUlkeler {
		_, bulundu, err := vergiInterop.RateForCountry(ctx, ulke)
		require.NoError(t, err)
		require.False(t, bulundu, "%s tax modülünde yapılandırılmamış olmalı", ulke)
	}

	varyantID := yeniVaryant(ctx, t, "E2E Çok Ülkeli Bölge Ürünü", map[string]int64{
		vergisizParaBirimi: vergiBirimFiyat,
	})
	toplamlar := bolgedeSepetHesabi(ctx, t, cokUlkeliUlkeler[0], varyantID, vergiAdet)

	// Beklenen ELLE hesaplanır: 30.000 × %30 = 9.000, toplam 39.000.
	const beklenenVergi int64 = 9_000
	const beklenenToplamTutar int64 = 39_000

	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: vergiAraToplam,
		indirim:   0,
		vergi:     beklenenVergi,
		kargo:     0,
		toplam:    beklenenToplamTutar,
	}, "çok ülkeli bölgede")

	require.Equal(t, cartwf.TaxSourceRegion, toplamlar.TaxSource,
		"kaynak %q olmalı: bölgeden ülke çözülemediği için tax'a hiç sorulmadı ve "+
			"hesap region oranına düştü. Bu alan, otoritenin sessizce değiştiğini "+
			"gösteren TEK işarettir",
		cartwf.TaxSourceRegion)
	require.NotEqual(t, cartwf.TaxSourceTaxUnconfigured, toplamlar.TaxSource,
		"tax'a sorulup 'bölge yok' cevabı alınmış OLMAMALI; ülke hiç çözülemedi")
}
