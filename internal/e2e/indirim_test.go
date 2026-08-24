//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Bu dosya planın Faz 7 DoD'sinin İNDİRİM ayağını kanıtlar: "sepete indirim
// uygulanıp toplam DOĞRU güncelleniyor".
//
// Faz 5'te sepet toplamının indirim alanı DAİMA SIFIRDI ve
// internal/workflows/cart bunu "Faz 7'de promotion devralacak" notuyla
// bırakmıştı. Devralmanın gerçekten yapıldığı ancak GERÇEK promotion modülüyle
// görülebilir: sepet akışının birim testleri indirimi bir sahteden okur ve o
// sahte, promotion'ın yüzde aritmetiğini, yuvarlama yönünü ya da JSON şemasını
// paylaşmaz.
//
// # Sınırın iki tarafını derleyici denetlemiyor
//
// internal/workflows/cart promotion'ı import EDEMEZ (ADR 0006) ve indirim
// isteği/yanıtı iki pakette AYRI AYRI tanımlanmış JSON şemalarıyla taşınır.
// promotion bilinmeyen alanları REDDEDER, yani bir alan adı kayarsa hesap
// çalışma zamanında düşer. Bu dosya, o şemaların birbirini gerçekten
// karşıladığının tek kanıtıdır.

// attrVaryantID promosyon HEDEF kuralının baktığı kalem özniteliğidir.
//
// Değer internal/workflows/cart içindeki attrVariantID sabitiyle BİREBİR aynı
// olmak zorundadır ve orası unexported olduğu için burada tekrarlanır. Tekrar
// bilinçlidir: sepet akışı bu adı her kaleme kendisi yazar; ad kayarsa hedef
// kuralı hiçbir kalemle eşleşmez, promosyon sessizce hiç indirim üretmez ve
// "indirim çalışmıyor" hatası hiçbir yerde patlamaz.
const attrVaryantID = "variant_id"

// Otomatik promosyon senaryosunun ELLE hesaplanmış tutarları.
//
// Kurulum: %10 (1000 baz puan) YÜZDE indirimi, hedef "items", tahsis "each";
// bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir.
//
//	A: 12_345 × 2 = 24_690 ara toplam
//	   indirim 24_690 × %10 = 2_469,0 -> 2_469
//	   vergi tabanı 24_690 - 2_469 = 22_221
//	   vergi 22_221 × %20 = 4_444,2 -> 4_444
//
//	B:  7_777 × 1 =  7_777 ara toplam
//	   indirim  7_777 × %10 =   777,7 ->   777
//	   vergi tabanı  7_777 -   777 =  7_000
//	   vergi  7_000 × %20 = 1_400,0 -> 1_400
//
//	ara toplam = 24_690 +  7_777 = 32_467
//	indirim    =  2_469 +    777 =  3_246
//	vergi      =  4_444 +  1_400 =  5_844
//	toplam     = 32_467 - 3_246 + 5_844 + 0 = 35_065
//
// Hem indirim hem vergi SATIR BAŞINA ve AŞAĞI yuvarlanır. İki yönün de aşağı
// olması aynı tarafı kayırmaz: aşağı yuvarlanan indirim SATICININ, aşağı
// yuvarlanan vergi MÜŞTERİNİN lehinedir (workflows/cart, assembleTotals).
const (
	indirimOraniBps int64 = 1_000

	indirimFiyatA int64 = 12_345
	indirimAdetA  int64 = 2
	indirimFiyatB int64 = 7_777
	indirimAdetB  int64 = 1

	indirimAraToplamA int64 = 24_690
	indirimAraToplamB int64 = 7_777

	indirimSatirA int64 = 2_469
	indirimSatirB int64 = 777

	indirimVergiA int64 = 4_444
	indirimVergiB int64 = 1_400

	indirimAraToplam int64 = 32_467
	indirimToplami   int64 = 3_246
	indirimVergi     int64 = 5_844
	indirimGenel     int64 = 35_065
)

// Aynı sepetin İNDİRİMSİZ hâlinin ELLE hesaplanmış tutarları.
//
// Fiyatlar ve adetler yukarıdakinin aynısıdır; tek fark, varyantların
// promosyonun hedef kuralına GİRMEMESİDİR:
//
//	A': 24_690 × %20 = 4_938,0 -> 4_938
//	B':  7_777 × %20 = 1_555,4 -> 1_555
//	vergi = 6_493, toplam = 32_467 - 0 + 6_493 + 0 = 38_960
//
// Vergi farkı satır satır açıklanabilir:
//
//	A: 4_938 - 4_444 =   494
//	B: 1_555 - 1_400 =   155
//	                   -----
//	                     649
//
// Fark, verginin tabanının İNDİRİM SONRASI olduğunun doğrudan ölçüsüdür.
// Taban indirim ÖNCESİ olsaydı iki sepetin vergisi birbirinin aynısı çıkardı
// ve müşteri, hiç ödemediği 3_246 birimin vergisini de öderdi.
const (
	indirimsizVergiA int64 = 4_938
	indirimsizVergiB int64 = 1_555
	indirimsizVergi  int64 = 6_493
	indirimsizGenel  int64 = 38_960

	// indirimVergiFarkiA ve indirimVergiFarkiB satır başına vergi farkıdır.
	indirimVergiFarkiA int64 = 494
	indirimVergiFarkiB int64 = 155
	// indirimVergiFarki iki sepetin vergisi arasındaki toplam farktır.
	indirimVergiFarki int64 = 649
)

// TestOtomatikPromosyonSepetToplaminiDusurur Faz 7 DoD'sinin indirim ayağını
// uçtan uca koşturur.
//
// Zincir: promosyon + uygulama yöntemi + hedef kuralı -> ürün/varyant/fiyat ->
// sepet -> satırlar -> hesap. Hesabın indirim ayağı promotion modülünde,
// vergi ayağı tax modülünde, birleştirme ise sepet akışında yapılır; üçü de
// GERÇEKTİR.
//
// Sınanan dört şey vardır ve her biri ayrı bir arızayı yakalar: indirimin
// TUTARI (yüzde aritmetiği ve yuvarlama), Σ kimliği (satırlara yazılanla
// sepete yazılanın ayrışmaması), verginin TABANI (indirim sonrası) ve genel
// toplam kimliği.
func TestOtomatikPromosyonSepetToplaminiDusurur(t *testing.T) {
	ctx := t.Context()

	varyantA := yeniVaryant(ctx, t, "E2E İndirimli A", map[string]int64{
		vergiliParaBirimi: indirimFiyatA,
	})
	varyantB := yeniVaryant(ctx, t, "E2E İndirimli B", map[string]int64{
		vergiliParaBirimi: indirimFiyatB,
	})

	promosyonID := otomatikYuzdePromosyonu(ctx, t, "E2E-OTOMATIK-10", indirimOraniBps,
		[]string{varyantA, varyantB})

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergiliUlke})
	require.NoError(t, err, "sepet açılabilmeli")

	satirA, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: sepet.CartID, VariantID: varyantA, Quantity: indirimAdetA,
	})
	require.NoError(t, err, "A satırı eklenebilmeli")
	sonuc, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: sepet.CartID, VariantID: varyantB, Quantity: indirimAdetB,
	})
	require.NoError(t, err, "B satırı eklenebilmeli")

	// --- 1) sepetin toplamları elle hesaplanan değerlerle aynı mı ---

	toplamlariDogrula(t, sonuc.Totals, beklenenToplam{
		araToplam: indirimAraToplam,
		indirim:   indirimToplami,
		vergi:     indirimVergi,
		kargo:     0,
		toplam:    indirimGenel,
	}, "otomatik promosyonlu sepette")

	require.Equal(t, cartwf.TaxSourceTax, sonuc.Totals.TaxSource,
		"vergiyi TAX modülü hesaplamış olmalı. Kaynak %q olsaydı hesap Faz 5'in "+
			"region oranına düşmüş olurdu ve bu senaryonun vergi iddiası, devralmanın "+
			"yapıldığını değil yapılMADIĞINI kanıtlardı",
		cartwf.TaxSourceRegion)

	// --- 2) Σ(satır indirimi) sepetin indirimine EŞİT mi ---

	satirlar := satirlariEsle(t, sonuc.Totals)
	var indirimlerinToplami int64
	for i := range sonuc.Totals.Lines {
		indirimlerinToplami += sonuc.Totals.Lines[i].DiscountTotal
	}
	require.Equal(t, indirimlerinToplami, sonuc.Totals.DiscountTotal,
		"Σ(satır indirimi) sepetin indirimine EŞİT olmalı. Sepet indirimi bağımsız bir "+
			"hesap değil, satır indirimlerinin toplamıdır; ayrışırlarsa müşteriye "+
			"gösterilen indirim ile satırlarda yazan indirim farklı olur ve fatura satır "+
			"satır açıklanamaz")
	require.Equal(t, indirimToplami, indirimlerinToplami,
		"satır indirimlerinin toplamı elle hesaplanan 3_246'ya eşit olmalı")

	// --- 3) satır başına indirim, vergi ve toplam ---

	for _, beklenen := range []struct {
		ad        string
		id        string
		araToplam int64
		indirim   int64
		vergi     int64
	}{
		{"A", satirA.LineItemID, indirimAraToplamA, indirimSatirA, indirimVergiA},
		{"B", sonuc.LineItemID, indirimAraToplamB, indirimSatirB, indirimVergiB},
	} {
		satir, bulundu := satirlar[beklenen.id]
		require.True(t, bulundu, "%s satırının tutarları hesapta bulunmalı", beklenen.ad)
		require.Equal(t, beklenen.araToplam, satir.Subtotal,
			"%s satırının ara toplamı birim fiyat × adet olmalı", beklenen.ad)
		require.Equal(t, beklenen.indirim, satir.DiscountTotal,
			"%s satırının indirimi, satırın KENDİ ara toplamının %%10'u olmalı (aşağı "+
				"yuvarlanmış). Sepet toplamı üzerinden hesaplanıp dağıtılsaydı kuruş artığı "+
				"başka bir satıra kayar ve satırın indirimi kendi oranının söylediğinden "+
				"farklı olurdu", beklenen.ad)
		require.Equal(t, beklenen.vergi, satir.TaxTotal,
			"%s satırının vergisi İNDİRİM SONRASI taban üzerinden hesaplanmalı", beklenen.ad)
		require.Equal(t, beklenen.araToplam-beklenen.indirim+beklenen.vergi, satir.Total,
			"%s satırının toplamı subtotal - discount + tax olmalı", beklenen.ad)
		require.LessOrEqual(t, satir.DiscountTotal, satir.Subtotal,
			"%s satırının indirimi ara toplamını ASLA aşmamalı; aşarsa satır negatif "+
				"bedelle satılmış olur ve cart modülü hesabın tamamını reddeder", beklenen.ad)
	}

	// --- 4) hesap sepete GERÇEKTEN yazıldı mı ---

	detay, err := sepetSvc.GetCart(ctx, sepet.CartID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.Equal(t, indirimToplami, detay.DiscountTotal,
		"indirim sepete YAZILMIŞ olmalı. Akışın döndürdüğü tutar doğru olup sepete "+
			"yazılan yanlış olsaydı, siparişi oluşturan Faz 6 saga'sı sepetin yazılı "+
			"toplamını okuduğu için müşteri indirimsiz tutarı öderdi")
	require.Equal(t, indirimGenel, detay.Total,
		"saklanan genel toplam elle hesaplanan değere eşit olmalı")
	require.True(t, detay.TotalsConsistent(),
		"sepet toplam kimliğini sağlamalı: total = subtotal - discount + tax + shipping")
	require.False(t, detay.TotalsStale(),
		"toplamlar sepetin güncel şekline damgalanmış olmalı")

	// --- 5) VERGİ TABANI indirim sonrası mı: indirimsiz aynı sepetle karşılaştır ---

	kontrolVaryantA := yeniVaryant(ctx, t, "E2E İndirimsiz A", map[string]int64{
		vergiliParaBirimi: indirimFiyatA,
	})
	kontrolVaryantB := yeniVaryant(ctx, t, "E2E İndirimsiz B", map[string]int64{
		vergiliParaBirimi: indirimFiyatB,
	})

	kontrolSepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: vergiliUlke})
	require.NoError(t, err, "kontrol sepeti açılabilmeli")
	kontrolSatirA, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: kontrolSepet.CartID, VariantID: kontrolVaryantA, Quantity: indirimAdetA,
	})
	require.NoError(t, err, "kontrol sepetine A satırı eklenebilmeli")
	kontrolSonuc, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: kontrolSepet.CartID, VariantID: kontrolVaryantB, Quantity: indirimAdetB,
	})
	require.NoError(t, err, "kontrol sepetine B satırı eklenebilmeli")

	toplamlariDogrula(t, kontrolSonuc.Totals, beklenenToplam{
		araToplam: indirimAraToplam,
		indirim:   0,
		vergi:     indirimsizVergi,
		kargo:     0,
		toplam:    indirimsizGenel,
	}, "promosyonun hedeflemediği varyantlarla kurulmuş kontrol sepetinde")

	require.Equal(t, indirimAraToplam, kontrolSonuc.Totals.Subtotal,
		"kontrol sepetinin ara toplamı indirimli sepetinkiyle AYNI olmalı; aynı "+
			"olmasaydı vergi farkı orandan değil fiyattan gelirdi ve karşılaştırma "+
			"hiçbir şey kanıtlamazdı")
	require.Zero(t, kontrolSonuc.Totals.DiscountTotal,
		"promosyonun HEDEF kuralı yalnızca kendi varyantlarını seçmeli. Sıfırdan farklı "+
			"bir indirim, otomatik bir promosyonun bütün sepetlere sızdığını ve diğer "+
			"senaryoların tutarlarının da bozulduğunu gösterir")

	require.NotEqual(t, kontrolSonuc.Totals.TaxTotal, sonuc.Totals.TaxTotal,
		"indirimli ve indirimsiz sepetin vergisi FARKLI olmalı. Eşit olsalardı vergi "+
			"tabanı indirim ÖNCESİ tutar olurdu ve müşteri, hiç ödemediği paranın "+
			"vergisini öderdi")
	require.Equal(t, indirimVergiFarki, kontrolSonuc.Totals.TaxTotal-sonuc.Totals.TaxTotal,
		"vergi farkı elle hesaplanan 649 olmalı: A satırında %d, B satırında %d",
		indirimVergiFarkiA, indirimVergiFarkiB)

	kontrolSatirlar := satirlariEsle(t, kontrolSonuc.Totals)
	require.Equal(t, indirimVergiFarkiA,
		kontrolSatirlar[kontrolSatirA.LineItemID].TaxTotal-satirlar[satirA.LineItemID].TaxTotal,
		"A satırının vergi farkı %d olmalı: 4_938 - 4_444. Fark satır satır "+
			"açıklanabilir olmalıdır; yalnızca sepet düzeyinde tutması, tabanın bir "+
			"satırda doğru bir satırda yanlış olduğunu gizleyebilirdi", indirimVergiFarkiA)
	require.Equal(t, indirimVergiFarkiB,
		kontrolSatirlar[kontrolSonuc.LineItemID].TaxTotal-satirlar[sonuc.LineItemID].TaxTotal,
		"B satırının vergi farkı %d olmalı: 1_555 - 1_400", indirimVergiFarkiB)

	require.Equal(t, indirimToplami+indirimVergiFarki, indirimsizGenel-indirimGenel,
		"iki sepetin genel toplamı arasındaki fark, indirimin KENDİSİ artı o indirimin "+
			"düşürdüğü vergi kadar olmalı. Müşterinin cebinde kalan para budur ve iki "+
			"bileşeni de elle hesaplanabilir olmalıdır")

	// --- 6) hesap kupon SAYACINI tüketmedi mi ---

	promosyon, err := promosyonSvc.GetPromotion(ctx, promosyonID)
	require.NoError(t, err, "promosyon promotion modülünden okunabilmeli")
	require.Zero(t, promosyon.UsageCount,
		"sepet hesabı promosyonun kullanım sayacını TÜKETMEMELİ. Sepet her "+
			"değiştiğinde yeniden hesaplanır; her hesabın bir kuponu harcaması, sepete "+
			"bakmakla kuponu kullanmayı aynı şey yapardı. Sayacı harcayan tek yol "+
			"RedeemPromotion'dır ve onu sipariş çağırır")
}

// otomatikYuzdePromosyonu belirli varyantları hedefleyen OTOMATİK bir yüzde
// promosyonu kurar ve kimliğini döner.
//
// # Neden hedef kuralı ZORUNLU
//
// Otomatik bir promosyon kodsuz uygulanır, yani kural konmazsa aynı veritabanını
// paylaşan HER sepete iner. Testler tek bir Postgres örneği üzerinde sırayla
// koşar; kuralsız bir promosyon, kendisinden sonra çalışan bütün senaryoların
// elle yazılmış tutarlarını sessizce düşürürdü ve arıza, promosyonu kuran testte
// değil bambaşka bir testte görünürdü.
//
// Kural, kalemin varyantına bakar ([attrVaryantID]) çünkü sepet akışının kalem
// hakkında promotion'a bildirdiği tek katalog gerçeği odur. Her senaryo kendi
// varyantlarını oluşturduğu için bu, promosyonu senaryonun içine hapseder.
func otomatikYuzdePromosyonu(
	ctx context.Context,
	t *testing.T,
	kod string,
	oranBps int64,
	varyantIDler []string,
) string {
	t.Helper()

	promosyon, err := promosyonSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        kod,
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "fikstür promosyonu oluşturulamadı")
	require.True(t, promosyon.IsAutomatic,
		"promosyon OTOMATİK olmalı; kupon kodu isteyen bir promosyon sepet akışına hiç "+
			"ulaşmazdı, çünkü akış kod GÖNDERMEZ (bkz. cartwf discountRequestFor)")

	_, err = promosyonSvc.SetApplicationMethod(ctx, promosyon.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      oranBps,
	})
	require.NoError(t, err, "fikstür promosyonunun uygulama yöntemi yazılamadı")

	_, err = promosyonSvc.AddPromotionRule(ctx, promosyon.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVaryantID,
		Operator:  promotionmodels.OpIn,
		Values:    varyantIDler,
	})
	require.NoError(t, err, "fikstür promosyonunun hedef kuralı yazılamadı")

	return promosyon.ID
}

// satirlariEsle hesaplanan satırları KİMLİĞE göre eşler.
//
// Eşleme sıraya değil kimliğe dayanır: sıraya dayanan bir iddia, satır sırası
// ileride değiştiğinde yanlış satırı doğrulamış olur ve test yeşil kalırken
// iddia anlamını yitirirdi.
func satirlariEsle(t *testing.T, toplamlar cartwf.Totals) map[string]cartwf.LineTotals {
	t.Helper()

	out := make(map[string]cartwf.LineTotals, len(toplamlar.Lines))
	for i := range toplamlar.Lines {
		out[toplamlar.Lines[i].LineItemID] = toplamlar.Lines[i]
	}
	require.Len(t, out, len(toplamlar.Lines),
		"her satır BENZERSİZ bir kimlikle dönmeli; tekrar eden bir kimlik, indirim ve "+
			"verginin hangi satıra ait olduğunu belirsizleştirirdi")
	return out
}
