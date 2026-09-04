//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Bu dosya B2B harcama limitinin GERÇEKTEN uygulandığını uçtan uca kanıtlar.
//
// # Neden modül testleri yetmez
//
// b2b modülünün kendi testleri limiti SAKLADIĞINI, order modülünün testleri
// ise sahte bir kural yüzeyi verildiğinde siparişi REDDETTİĞİNİ gösterir.
// İkisi de doğrudur ve ikisi birlikte bile asıl iddiayı kanıtlamaz: iki modül
// birbirini import edemediği için aralarındaki sözleşme JSON'dur ve derleyici
// onu denetlemez (bkz. [b2bsvc] interop belgesi). Alan adlarından biri
// ayrışsaydı her iki paketin testleri de yeşil kalır, üretimde ise limit
// SESSİZCE kalkardı — "limited" alanı çözülemeyince kural "yok" sayılırdı.
// Bu dosya sözleşmenin iki ucunu gerçek container üzerinden birleştirir.
//
// # Kontrolün yeri
//
// Kural [ordersvc.Service.CreateOrder] içinde, siparişin yazıldığı işlemin
// içinde uygulanır. Bunun iki sonucu vardır ve ikisi de burada sınanır:
// complete_cart saga'sında create_order adımı authorize_payment'tan ÖNCE
// koştuğu için reddedilen alışverişte PARA HİÇ YETKİLENDİRİLMEZ; ve kontrol
// ile yazma aynı işlemde olduğu için iki eşzamanlı sipariş limiti birlikte
// aşamaz.

// B2B senaryolarının ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir ve kargo yöntemi seçilmemiştir:
//
//	50_000 × 2 = 100_000 ara toplam
//	100_000 × %20 = 20_000 vergi
//	100_000 - 0 + 20_000 + 0 = 120_000 genel toplam
const (
	b2bBirimFiyat int64 = 50_000
	b2bAdet       int64 = 2
	b2bToplam     int64 = 120_000
	// b2bStok iki siparişe yetecek kadardır: birikme senaryosu aynı çalışanla
	// iki alışveriş yapar ve ikincisinin stok yüzünden değil LİMİT yüzünden
	// düştüğü görülebilmelidir.
	b2bStok int64 = 20
)

// b2bCalisan müşteriyi yeni bir şirketin çalışanı yapar ve şirketi döner.
//
// Her senaryo KENDİ şirketini kurar: şirket paylaşılsaydı, bir senaryonun
// limiti değiştirmesi komşu senaryonun beklentisini bozar ve testler tek
// başına koşturulduğunda başka, birlikte koşturulduğunda başka sonuç verirdi.
//
// limit nil geçilirse çalışan SINIRSIZDIR (bkz. [b2bsvc.EmployeeInput]).
func b2bCalisan(
	ctx context.Context,
	t *testing.T,
	musteriID string,
	limit *int64,
	periyot b2bmodels.SpendingResetPeriod,
) (sirketID string) {
	t.Helper()

	sirket, err := b2bSvc.CreateCompany(ctx, b2bsvc.CompanyInput{
		Name:  "E2E B2B " + musteriID,
		Email: "b2b-" + musteriID + "@ornek.test",
		// Şirketin para birimi sepetin para birimiyle AYNI seçilir: farklı
		// olsaydı kural para birimi uyuşmazlığından düşerdi ve senaryonun
		// sınadığı şey limit değil, o kontrol olurdu (o kontrolün kendi
		// senaryosu ayrıdır).
		CurrencyCode:             taxedCurrency,
		SpendingLimitResetPeriod: string(periyot),
	})
	require.NoError(t, err, "b2b şirketi kurulamadı")

	_, err = b2bSvc.CreateEmployee(ctx, b2bsvc.EmployeeInput{
		CompanyID:     sirket.ID,
		CustomerID:    musteriID,
		SpendingLimit: limit,
	})
	require.NoError(t, err, "müşteri şirkete çalışan olarak eklenemedi")

	return sirket.ID
}

// b2bSepetiTamamla hazır bir B2B sepetini akışla tamamlamayı dener.
func b2bSepetiTamamla(
	ctx context.Context,
	t *testing.T,
	sepetID, eposta string,
) (checkoutwf.CompleteCartResult, error) {
	t.Helper()

	return orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     b2bToplam,
	})
}

// TestB2BLimitiAsanSiparisReddedilirVeParaCekilmez asıl iddiayı kanıtlar:
// limitin üstündeki bir alışveriş siparişe dönüşmez VE parası çekilmez.
//
// Paranın çekilmediğini ayrıca sınamak gereksiz görünebilir ama değildir:
// kural order modülünde yaşar ve saga'nın adım sırası değişirse (create_order
// authorize_payment'ın ARDINA alınırsa) test yine "sipariş yok" der ama
// müşterinin parası çekilip iade edilmiş olurdu. Stok da aynı sebeple
// sınanır — reddedilen alışveriş stoğu tutuyor olsaydı, limiti dolmuş bir
// çalışan denemeye devam ederek katalogun stoğunu kilitleyebilirdi.
func TestB2BLimitiAsanSiparisReddedilirVeParaCekilmez(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := newCustomer(ctx, t)
	varyantID, stokKalemID := newStockedVariant(ctx, t, "E2E B2B Limit Aşımı",
		map[string]int64{taxedCurrency: b2bBirimFiyat}, b2bStok)

	// Limit sepetin toplamının ALTINDA: 50_000 < 120_000.
	limit := int64(50_000)
	b2bCalisan(ctx, t, musteriID, &limit, b2bmodels.ResetNever)

	sepetID, _ := prepareCart(ctx, t, musteriID, varyantID, b2bAdet)

	_, err := b2bSepetiTamamla(ctx, t, sepetID, eposta)

	require.Error(t, err, "limitin üstündeki alışveriş siparişe DÖNMEMELİ")
	require.True(t, errors.IsConflict(err),
		"limit aşımı çakışmadır (422 değil 409): istek biçimsel olarak geçerlidir, "+
			"reddin sebebi sistemin O ANDAKİ durumudur; gövde: %v", err)
	// Kod DIŞTAN okunur, zincirin içinden değil: saga adım hatasını sararken
	// alt hatanın kodunu KORUR (bkz. workflow.CodeStepFailed). Fark tüketicide
	// görünür — taşıma katmanı gövdeye tek bir makine okunur alan yazar ve o
	// alan motorun kendi sabitiyle dolsaydı, vitrin "limitiniz yetmedi" ile
	// "geçici çakışma, tekrar deneyin"i ayırt edemezdi.
	require.Equal(t, ordersvc.CodeSpendingLimitExceeded, errors.CodeOf(err),
		"reddin kodu harcama limiti olmalı; başka bir kod, siparişin BAŞKA bir "+
			"sebeple düştüğünü ve testin limiti hiç sınamadığını gösterir")
	require.ErrorContains(t, err, checkoutwf.StepCreateOrder,
		"redde düşen adım create_order olmalı — PARANIN ÇEKİLMEDİĞİNİN kanıtı "+
			"budur: authorize_payment ondan SONRA gelir ve hiç koşmamıştır")

	require.Equal(t, b2bStok, sellableQuantity(ctx, t, stokKalemID),
		"reddedilen alışveriş stoğu SERBEST bırakmalı; rezervasyon adımı "+
			"create_order'dan önce koştuğu için telafinin çalıştığının kanıtı budur")

	sepet, err := cartSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "reddedilen alışverişin sepeti hâlâ okunabilmeli")
	require.Nil(t, sepet.CompletedAt,
		"reddedilen alışverişin sepeti KAPANMAMALI; kapansaydı müşteri aynı sepetle "+
			"tekrar deneyemez, limiti düştüğünde sepetini yeniden kurmak zorunda kalırdı")
}

// TestB2BPencereIcindekiHarcamaBirikir limitin TEK sipariş için değil DÖNEM
// TOPLAMI için uygulandığını kanıtlar.
//
// Bu, kuralın gerçekten sipariş verisinden hesaplandığının kanıtıdır: limit
// yalnızca sipariş tutarıyla karşılaştırılsaydı iki alışveriş de tek tek
// limitin altında olduğu için GEÇERDİ ve çalışan limitin iki katını
// harcayabilirdi.
func TestB2BPencereIcindekiHarcamaBirikir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := newCustomer(ctx, t)
	varyantID, _ := newStockedVariant(ctx, t, "E2E B2B Birikme",
		map[string]int64{taxedCurrency: b2bBirimFiyat}, b2bStok)

	// Limit tek siparişi geçirir (120_000 ≤ 200_000) ama ikisini geçirmez
	// (240_000 > 200_000).
	limit := int64(200_000)
	b2bCalisan(ctx, t, musteriID, &limit, b2bmodels.ResetNever)

	ilkSepet, _ := prepareCart(ctx, t, musteriID, varyantID, b2bAdet)
	ilk, err := b2bSepetiTamamla(ctx, t, ilkSepet, eposta)
	require.NoError(t, err, "limitin altındaki İLK alışveriş geçmeli")
	require.NotEmpty(t, ilk.OrderID, "ilk alışveriş sipariş üretmeli")

	ikinciSepet, _ := prepareCart(ctx, t, musteriID, varyantID, b2bAdet)
	_, err = b2bSepetiTamamla(ctx, t, ikinciSepet, eposta)

	require.Error(t, err,
		"İKİNCİ alışveriş reddedilmeli: tek başına limitin altında ama dönem "+
			"toplamı (120_000 + 120_000 = 240_000) limitin üstünde")
	require.Equal(t, ordersvc.CodeSpendingLimitExceeded, errors.CodeOf(err),
		"reddin sebebi harcama limiti olmalı; gövde: %v", err)
}

// TestB2BSinirsizCalisanEtkilenmez limiti nil olan çalışanın kısıtlanmadığını
// kanıtlar.
//
// nil ile 0 farklı cümlelerdir (bkz. [b2bsvc.EmployeeInput.SpendingLimit]):
// nil "sınır yok", 0 ise "hiçbir şey harcayamaz" demektir. İkisi karışsaydı
// limiti girilmemiş her çalışan alışveriş yapamaz hâle gelirdi.
func TestB2BSinirsizCalisanEtkilenmez(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := newCustomer(ctx, t)
	varyantID, _ := newStockedVariant(ctx, t, "E2E B2B Sınırsız",
		map[string]int64{taxedCurrency: b2bBirimFiyat}, b2bStok)

	b2bCalisan(ctx, t, musteriID, nil, b2bmodels.ResetMonthly)

	sepetID, _ := prepareCart(ctx, t, musteriID, varyantID, b2bAdet)
	sonuc, err := b2bSepetiTamamla(ctx, t, sepetID, eposta)

	require.NoError(t, err, "limiti olmayan çalışanın alışverişi geçmeli")
	require.NotEmpty(t, sonuc.OrderID, "sınırsız çalışan sipariş üretebilmeli")
}

// TestB2BOlmayanMusteriEtkilenmez b2b modülünün KAYITLI olmasının B2C akışını
// değiştirmediğini kanıtlar.
//
// Bu testin değeri gerileme tarafındadır: harcama kuralı order modülünde HER
// sipariş için sorulur ve hiçbir şirketin çalışanı olmayan müşteri için
// "kural yok" cevabının BAŞARILI sayılması gerekir. Cevap hata sayılsaydı,
// b2b modülünü kurmak kurulumdaki bütün B2C siparişlerini düşürürdü — ve bunu
// yalnızca b2b kayıtlıyken koşan bir test görebilir.
func TestB2BOlmayanMusteriEtkilenmez(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := newCustomer(ctx, t)
	varyantID, _ := newStockedVariant(ctx, t, "E2E B2C Etkilenmez",
		map[string]int64{taxedCurrency: b2bBirimFiyat}, b2bStok)

	// Bilerek b2bCalisan ÇAĞRILMIYOR: müşteri hiçbir şirkete bağlı değil.
	sepetID, _ := prepareCart(ctx, t, musteriID, varyantID, b2bAdet)
	sonuc, err := b2bSepetiTamamla(ctx, t, sepetID, eposta)

	require.NoError(t, err,
		"şirkete bağlı olmayan müşterinin alışverişi, b2b modülü kayıtlıyken de geçmeli")
	require.NotEmpty(t, sonuc.OrderID, "B2C müşterisi sipariş üretebilmeli")
}
