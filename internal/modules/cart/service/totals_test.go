package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// toplamliSepet iki satırlı bir sepet kurar ve satırları döner.
//
// Fikstür bilinçlidir: satırların adetleri ve birim fiyatları FARKLIDIR, yani
// ara toplamı yalnızca doğru çarpım ve doğru toplama tutturabilir.
func toplamliSepet(ctx context.Context, t *testing.T, svc *service.Service) (cart models.Cart, first, second models.LineItem) {
	t.Helper()

	var err error
	cart, err = svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	first, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 3,
	})
	require.NoError(t, err)
	second, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "Pantolon", Quantity: 2,
	})
	require.NoError(t, err)

	// Sepet GÜNCEL hâliyle dönülür: SetTotals hesabın dayandığı şekil sayacını
	// çağırandan ister ve gerçek workflow da yazmadan önce sepeti okur.
	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	return detail.Cart, first, second
}

// TestSetTotalsBasariliYazarVeDamgalar tutarlı bir hesabın yazıldığını ve
// toplamların artık bayat OLMADIĞINI doğrular.
func TestSetTotalsBasariliYazarVeDamgalar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	// 3 × 1000 = 3000, 2 × 2500 = 5000 -> ara toplam 8000.
	// İndirim 500, vergi 1500, kargo 2000 -> toplam 11000.
	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision:      cart.Revision,
		Subtotal:      8000,
		DiscountTotal: 500,
		TaxTotal:      1500,
		ShippingTotal: 2000,
		Total:         11000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, TaxTotal: 540, Total: 3540},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, DiscountTotal: 500, TaxTotal: 810, Total: 5310},
		},
	})

	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(8000), detail.Subtotal)
	assert.Equal(t, int64(500), detail.DiscountTotal)
	assert.Equal(t, int64(1500), detail.TaxTotal)
	assert.Equal(t, int64(2000), detail.ShippingTotal)
	assert.Equal(t, int64(11000), detail.Total)
	assert.True(t, detail.TotalsConsistent())
	assert.False(t, detail.TotalsStale(), "yazılan toplamlar sepetin güncel şekline ait olmalı")

	require.Len(t, detail.Items, 2)
	assert.Equal(t, int64(1000), detail.Items[0].UnitPrice)
	assert.Equal(t, int64(3540), detail.Items[0].Total)
	assert.Equal(t, int64(5310), detail.Items[1].Total)
}

// TestSetTotalsSepetKimliginiZorlar sepet toplam kimliğini sağlamayan bir
// hesabın reddedildiğini doğrular.
//
// Bu, SetTotals'ın var oluş sebebidir: workflow'daki bir hesap hatası sessizce
// veritabanına yazılamaz.
func TestSetTotalsSepetKimliginiZorlar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	tutarli := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}

	testler := map[string]service.Totals{
		"toplam eksik": {
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 500, TaxTotal: 1500, ShippingTotal: 2000,
			Total: 10999, Lines: tutarli,
		},
		"indirim eklenmiş": {
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 500, Total: 8500, Lines: tutarli,
		},
		"vergi unutulmuş": {
			Revision: cart.Revision,
			Subtotal: 8000, TaxTotal: 1440, Total: 8000, Lines: tutarli,
		},
		"kargo unutulmuş": {
			Revision: cart.Revision,
			Subtotal: 8000, ShippingTotal: 2000, Total: 8000, Lines: tutarli,
		},
	}

	for ad, girdi := range testler {
		t.Run(ad, func(t *testing.T) {
			err := svc.SetTotals(ctx, cart.ID, girdi)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
		})
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Zero(t, detail.Total, "reddedilen hesap yazılmamalı")
	assert.Zero(t, detail.Items[0].UnitPrice, "reddedilen hesap satırlara da yazılmamalı")
}

// TestSetTotalsSatirKimliginiZorlar satır toplam kimliğini sağlamayan bir
// hesabın reddedildiğini doğrular.
func TestSetTotalsSatirKimliginiZorlar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			// 3000 - 100 + 200 = 3100 olmalı, 3000 verildi.
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, DiscountTotal: 100, TaxTotal: 200, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
}

// TestSetTotalsSatirAraToplamiCarpimiZorlar satır ara toplamının birim fiyat ×
// adet olduğunu doğrular.
//
// Adet sepetin KENDİ verisidir; bu çarpımı doğrulayabilen tek yer burasıdır.
// Yanlış adetle fiyatlanmış bir satır başka hiçbir kapıda yakalanmazdı.
func TestSetTotalsSatirAraToplamiCarpimiZorlar(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 6000, Total: 6000,
		Lines: []service.LineTotals{
			// Satırın adedi 3; 1000 × 3 = 3000 olmalı, 1000 verilmiş
			// (adet 1 sanılmış).
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "quantity", "hata hangi çarpımın tutmadığını yazmalı")
}

// TestSetTotalsAraToplamSatirlarinToplamiOlmali sepet ara toplamının
// satırların ara toplamlarına eşit olmasını zorladığını doğrular.
func TestSetTotalsAraToplamSatirlarinToplamiOlmali(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		// Satırlar 3000 + 5000 = 8000 ediyor; 7999 verildi.
		Subtotal: 7999, Total: 7999,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
}

// TestSetTotalsFiyatlanmamisSatirSifirTutarlaGecemez sepetin TÜM satırlarının
// kapsanmasının zorunlu olduğunu doğrular.
//
// Kapsama zorunlu olmasaydı, tutarı verilmeyen satırın SAKLI değerleri
// korunurdu; yeni açılmış bir satırın saklı ara toplamı ise SIFIRDIR. Yani
// satırları göndermeyi unutan bir hesap turu, 300000 tutarındaki bir sepeti
// "subtotal 0, total 0" ile TUTARLI gösterir ve sepet bedavaya tamamlanırdı.
func TestSetTotalsFiyatlanmamisSatirSifirTutarlaGecemez(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 3, UnitPrice: 100000,
	})
	require.NoError(t, err)
	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: guncel.Revision, Subtotal: 0, Total: 0,
	})

	require.Error(t, err, "fiyatlanmamış satırı atlayan hesap kabul edilmemeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), item.ID, "hata hangi satırın atlandığını yazmalı")

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "reddedilen tur sepeti taze damgalamamalı")
}

// TestSetTotalsAdetDegistiktenSonraSatirsizYazmaReddedilir adet değişiminden
// sonra satırları göndermeyen bir hesabın reddedildiğini doğrular.
//
// Senaryo gerçektir: satır BİR KEZ doğru fiyatlanır, sonra müşteri adedi
// artırır ve yeni hesap turu satırları göndermeyi unutur. Saklı tutarlara
// güvenilseydi Σ değişmediği için hesap tutarlı görünür, sepet 10 adetlik malı
// 1 adet fiyatına TAZE damgayla tamamlardı.
func TestSetTotalsAdetDegistiktenSonraSatirsizYazmaReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: guncel.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	}))

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 10)
	require.NoError(t, err)
	guncel, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: guncel.Revision, Subtotal: 1000, ShippingTotal: 500, Total: 1500,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "reddedilen tur sepeti taze damgalamamalı")
	assert.Equal(t, int64(1000), detail.Total, "önceki geçerli hesap korunmalı")
}

// TestSetTotalsKargoGuncellemesiDeTumSatirlariIster kısmi güncellemenin hiçbir
// biçiminin kabul edilmediğini doğrular.
//
// "Yalnızca kargoyu yaz" makul görünen ama sözleşmeyi delen çağrıdır: aynı
// kapıdan fiyatlanmamış satır da geçer. Kargo değişimi zaten sepetin şeklini
// değiştirir ve hesabın baştan koşmasını gerektirir.
func TestSetTotalsKargoGuncellemesiDeTumSatirlariIster(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	tumSatirlar := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, Total: 8000, Lines: tumSatirlar,
	}))

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, ShippingTotal: 2000, Total: 10000,
	})
	require.Error(t, err, "satırsız kısmi güncelleme kabul edilmemeli")
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	// Aynı tur satırlarla birlikte gönderilince geçer.
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision, Subtotal: 8000, ShippingTotal: 2000, Total: 10000,
		Lines: tumSatirlar,
	}))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(10000), detail.Total)
	assert.False(t, detail.TotalsStale())
}

// TestSetTotalsBayatHesapReddedilir hesabın dayandığı sepet şekli değişmişse
// yazmanın reddedildiğini doğrular.
//
// Bu, modülün savunduğunu iddia ettiği yarıştır: workflow sepeti okur, hesabı
// KİLİDİN DIŞINDA yapar, araya bir satır girer ve bayat hesap yazılmak istenir.
// Damga yazma anındaki şekilden alınsaydı bayat hesap GÜNCEL diye damgalanır,
// MarkCompleted'ın bayatlık kapısı açılır ve müşteri sepetindeki maldan azını
// öderdi.
func TestSetTotalsBayatHesapReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	a, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "A", Quantity: 1,
	})
	require.NoError(t, err)

	// Workflow sepeti okur ve hesabını bu şekle göre yapar.
	hesaplanan, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	// Araya ikinci satır girer.
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "B", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: hesaplanan.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: a.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Zero(t, detail.Total, "bayat hesap yazılmamalı")
	assert.True(t, detail.TotalsStale(), "sepet bayat kalmalı")

	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.Error(t, err, "bayat sepet tamamlanamamalı")
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsSatirDisiDegisiklikDeYakalanir satır kümesi
// değişmeden de yarışın yakalandığını doğrular.
//
// Kargo yöntemi eklemek satırlara dokunmaz; kapsama kontrolü bu turu geçerdi.
// Yakalayan tek şey, hesabın dayandığı şeklin çağırandan alınmasıdır.
func TestSetTotalsSatirDisiDegisiklikDeYakalanir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	hesaplanan, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	// Müşteri hesap sürerken kargo yöntemi seçer; satır kümesi AYNI kalır.
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standart", Amount: 2500,
	})
	require.NoError(t, err)

	err = svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: hesaplanan.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	})

	require.Error(t, err, "kargosu ödenmemiş bayat hesap kabul edilmemeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsSifirSurumEskiDavranisaDusmez doldurulmayan Revision alanının
// "verilmedi" sayılmadığını doğrular.
//
// Sıfır GERÇEK bir şekil değeridir (hiç değiştirilmemiş sepet). Geçiş kolaylığı
// için sıfırda eski davranışa düşülseydi, alanı doldurmayı unutan her çağıran
// tam da kapatılmak istenen yarışı geri getirirdi.
func TestSetTotalsSifirSurumEskiDavranisaDusmez(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)
	require.Positive(t, cart.Revision, "fikstür sepeti değiştirmiş olmalı")

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))
}

// TestSetTotalsBosSepetteAraToplamSifirOlmali satırsız bir sepette sıfırdan
// farklı bir ara toplamın reddedildiğini doğrular.
func TestSetTotalsBosSepetteAraToplamSifirOlmali(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{Subtotal: 100, Total: 100})

	require.Error(t, err)
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		ShippingTotal: 2500, Total: 2500,
	}), "satırsız sepete yalnızca kargo yazılabilmeli")
}

// TestSetTotalsBilinmeyenSatirReddedilir başka bir sepetin (ya da olmayan bir)
// satırının tutarının yazılamayacağını doğrular.
func TestSetTotalsBilinmeyenSatirReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, _ := toplamliSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 3000, Total: 3000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: "li_BASKA", UnitPrice: 100, Subtotal: 100, Total: 100},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, service.CodeLineItemNotFound, errors.CodeOf(err))
}

// TestSetTotalsTekrarlananSatirReddedilir aynı satır için iki tutar
// verilmesinin reddedildiğini doğrular.
//
// Sessizce sonuncusu kazansaydı, birbirini ezen iki hesap arasındaki fark
// yalnızca sıraya bağlı olurdu.
func TestSetTotalsTekrarlananSatirReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetTotalsNegatifTutarReddedilir negatif toplamların reddedildiğini
// doğrular.
func TestSetTotalsNegatifTutarReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	testler := map[string]service.Totals{
		"negatif ara toplam": {Subtotal: -1, Total: -1},
		"negatif indirim":    {DiscountTotal: -100, Total: 100},
		"negatif vergi":      {TaxTotal: -100, Total: -100},
		"negatif kargo":      {ShippingTotal: -100, Total: -100},
		"negatif toplam":     {Total: -1},
	}

	for ad, girdi := range testler {
		t.Run(ad, func(t *testing.T) {
			err := svc.SetTotals(ctx, cart.ID, girdi)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestSetTotalsTavaniAsanTutarReddedilir üst sınırı aşan tutarların
// reddedildiğini doğrular; sınır taşmayı yapısal olarak imkânsız kılar.
func TestSetTotalsTavaniAsanTutarReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		ShippingTotal: models.MaxTotal + 1,
		Total:         models.MaxTotal + 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetTotalsHataHalindeHicbirSatirYazilmaz satır yazımı sırasında hata
// çıkarsa daha önce yazılanların GERİ ALINDIĞINI doğrular.
//
// Kısmen yazılmış bir hesap turu, sepetin ara toplamı ile satırlarının
// birbirini tutmadığı bir duruma yol açardı.
func TestSetTotalsHataHalindeHicbirSatirYazilmaz(t *testing.T) {
	svc, store, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)
	store.failSetLineItemTotals = errors.Internal("cart_query_failed", "veritabanı düştü")

	err := svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: cart.Revision,
		Subtotal: 8000, Total: 8000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
			{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
		},
	})

	require.Error(t, err)
	detail, getErr := svc.GetCart(ctx, cart.ID)
	require.NoError(t, getErr)
	assert.Zero(t, detail.Subtotal, "sepet toplamı yazılmamalı")
	for _, item := range detail.Items {
		assert.Zero(t, item.Subtotal, "hiçbir satır yazılmamalı")
	}
}

// TestSetTotalsOlmayanSepetNotFound olmayan bir sepete yazmanın NotFound
// döndürdüğünü doğrular.
func TestSetTotalsOlmayanSepetNotFound(t *testing.T) {
	svc, _, _ := yeniServis(t)

	err := svc.SetTotals(context.Background(), "cart_YOK", service.Totals{})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSetTotalsIndirimAraToplamiAsamaz aşırı indirimin vergi/kargo tarafından
// YUTULARAK kimlik kontrolünden geçmesini engelleyen kuralı sabitler.
//
// Regresyon: doğrulama yalnızca (a) aralık ve (b) kimlik
// (total = subtotal - discount + tax + shipping) kontrol ediyordu. Aşırı bir
// indirim kargo tarafından yutulduğunda kimlik SAĞLANIR ve toplam negatif bile
// olmaz: subtotal=1000, discount=3000, shipping=2500 -> total=500 kabul
// ediliyordu. Müşteri 1000'lik mala 2500'lük kargoyla birlikte 500 öder ve ne
// servis ne de carts_totals_consistent kısıtı bunu görürdü.
func TestSetTotalsIndirimAraToplamiAsamaz(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	cart, first, second := toplamliSepet(ctx, t, svc)

	tutarli := []service.LineTotals{
		{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, Total: 3000},
		{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
	}

	t.Run("sepet duzeyinde asiri indirim reddedilir", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 20000, ShippingTotal: 15000,
			Total: 3000, // kimlik SAĞLANIYOR: 8000 - 20000 + 0 + 15000 = 3000
			Lines: tutarli,
		})
		require.Error(t, err, "kimlik sağlansa da indirim ara toplamı aşamaz")
		assert.True(t, errors.IsInvalid(err), "sınıf Invalid olmalı: %v", err)
		assert.Contains(t, err.Error(), "indirim")
	})

	t.Run("satir duzeyinde asiri indirim reddedilir", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, Total: 8000,
			Lines: []service.LineTotals{
				// 3000'lik satıra 9000 indirim; vergi yutuyor, kimlik sağlanıyor.
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000,
					DiscountTotal: 9000, TaxTotal: 9000, Total: 3000},
				{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, Total: 5000},
			},
		})
		require.Error(t, err, "satır indirimi de ara toplamı aşamaz")
		assert.True(t, errors.IsInvalid(err), "sınıf Invalid olmalı: %v", err)
	})

	t.Run("ara toplama esit indirim gecerlidir", func(t *testing.T) {
		err := svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: cart.Revision,
			Subtotal: 8000, DiscountTotal: 8000, ShippingTotal: 2000,
			Total: 2000,
			Lines: []service.LineTotals{
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 3000, DiscountTotal: 3000, Total: 0},
				{LineItemID: second.ID, UnitPrice: 2500, Subtotal: 5000, DiscountTotal: 5000, Total: 0},
			},
		})
		assert.NoError(t, err, "ara toplama EŞİT indirim sınırdadır ve geçerlidir")
	})
}
