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

// Testlerde kullanılan sabit kimlikler.
const (
	regionID    = "reg_TEST"
	regionOther = "reg_DIGER"
	customerID  = "cust_TEST"
	variantA    = "variant_A"
	variantB    = "variant_B"
	currency    = "TRY"
)

// yeniServis sahte depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	svc, err := service.New(service.Options{Repo: store})
	require.NoError(t, err)
	return svc, store
}

// yeniSepet test için misafir sepeti oluşturur.
func yeniSepet(ctx context.Context, t *testing.T, svc *service.Service) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     regionID,
		CurrencyCode: currency,
	})
	require.NoError(t, err)
	return cart
}

// TestNewEksikBagimlilikKurulumdaPatlar servisin eksik bağımlılıkla
// kurulamadığını doğrular.
//
// Deposuz bir servis her çağrıda panik üretirdi; eksikliğin ilk istekte değil
// açılışta görünmesi için hiçbir sebep yoktur.
func TestNewEksikBagimlilikKurulumdaPatlar(t *testing.T) {
	_, err := service.New(service.Options{})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
}

// TestCreateCartRegionZorunlu bölgesiz bir sepetin reddedildiğini doğrular.
func TestCreateCartRegionZorunlu(t *testing.T) {
	svc, _ := yeniServis(t)

	_, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		CurrencyCode: currency,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestCreateCartParaBirimiDogrulanirVeTeklesir para biriminin biçim
// doğrulamasından geçtiğini ve BÜYÜK harfe tekleştirildiğini doğrular.
//
// Tekleştirme olmasaydı "try" ile "TRY" iki ayrı para birimi gibi saklanır ve
// tutar karşılaştırmaları sessizce yanlış sonuç verirdi.
func TestCreateCartParaBirimiDogrulanirVeTeklesir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: " try ",
	})
	require.NoError(t, err)
	assert.Equal(t, "TRY", cart.CurrencyCode)

	for _, gecersiz := range []string{"", "TR", "TRYY", "TR1"} {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CurrencyCode: gecersiz,
		})
		require.Error(t, err, "geçersiz para birimi kabul edilmemeli: %q", gecersiz)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestCreateCartMisafirSepetiMusterisizAcilir müşteri kimliği verilmeyen
// sepetin MİSAFİR olarak açıldığını doğrular.
//
// Boş kimlik saklanmaz, YOKLUK olarak saklanır: "müşterisi olmayan" ile
// "müşterisi boş dize olan" ayrımı kaybolsaydı carts_customer_idx boş dizeye
// yazılmış sepetlerle dolardı.
func TestCreateCartMisafirSepetiMusterisizAcilir(t *testing.T) {
	svc, _ := yeniServis(t)

	cart, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})

	require.NoError(t, err)
	assert.True(t, cart.Guest(), "müşterisiz sepet misafir sayılmalı")
	assert.Empty(t, cart.CustomerID)
	assert.Equal(t, regionID, cart.RegionID)
}

// TestCreateCartKayitliMusteriSutunlaraYazilir kayıtlı müşteri sepetinin
// bölgesinin ve müşterisinin KENDİ SÜTUNLARINDA durduğunu doğrular.
//
// İlişkinin tek yeri budur; sepet bir de link tablosuna yazılmaz. İddia o
// kararın bekçisidir: ikinci bir kopya eklenirse sütun ile bağ ayrışabilir.
func TestCreateCartKayitliMusteriSutunlaraYazilir(t *testing.T) {
	svc, _ := yeniServis(t)

	cart, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
		Email: "  Musteri@Ornek.COM ",
	})

	require.NoError(t, err)
	assert.False(t, cart.Guest())
	assert.Equal(t, "musteri@ornek.com", cart.Email, "e-posta küçük harfe indirilmeli")
	assert.Equal(t, regionID, cart.RegionID)
	assert.Equal(t, customerID, cart.CustomerID)
}

// TestCreateCartAyniBolgedeIkiSepetAcilabilir bir bölgede (ve bir müşteride)
// birden çok sepet açılabildiğini doğrular.
//
// Kural sepetin kendi tabiatıdır: bir müşterinin zaman içinde birden çok
// sepeti olur ve bir bölgede binlerce sepet bulunur. Herhangi bir katmanda
// bölge ya da müşteri başına TEKİLLİK dayatan bir kısıt bu testi düşürür.
func TestCreateCartAyniBolgedeIkiSepetAcilabilir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	first, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	second, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err, "aynı müşteri aynı bölgede ikinci sepet açabilmeli")

	assert.NotEqual(t, first.ID, second.ID)

	_, count, listErr := svc.ListCarts(ctx, service.ListCartsInput{})
	require.NoError(t, listErr)
	assert.Equal(t, int64(2), count)
}

// TestAddLineItemAdetPozitifOlmali sıfır ve negatif adedin reddedildiğini
// doğrular.
func TestAddLineItemAdetPozitifOlmali(t *testing.T) {
	svc, store := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	for _, adet := range []int64{0, -1, -1000} {
		_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: variantA, Title: "Tişört", Quantity: adet,
		})
		require.Error(t, err, "adet %d kabul edilmemeli", adet)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}

	items, err := store.ListLineItems(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, items, "reddedilen ekleme satır yazmamalı")
}

// TestAddLineItemAdetTavaniAsilamaz adet üst sınırının uygulandığını doğrular.
func TestAddLineItemAdetTavaniAsilamaz(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: models.MaxQuantity + 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAddLineItemAyniVaryantAdediArtirir aynı varyantın ikinci kez eklenmesinin
// YENİ SATIR AÇMADIĞINI, var olan satırın adedini artırdığını doğrular.
//
// Karar fiyat kademelerinden gelir: 3 + 2 iki satıra bölünürse pricing her iki
// satırı da "1-4" kademesinden fiyatlar ve müşteri "5+" fiyatını alamaz
// (bkz. Service.AddLineItem).
func TestAddLineItemAyniVaryantAdediArtirir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	first, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 3, UnitPrice: 1000,
	})
	require.NoError(t, err)

	second, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört (yeni başlık)", Quantity: 2, UnitPrice: 9999,
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "aynı satır güncellenmeli")
	assert.Equal(t, int64(5), second.Quantity, "3 + 2 = 5")
	assert.Equal(t, "Tişört", second.Title, "birleştirmede yalnızca adet taşınır")
	assert.Equal(t, int64(1000), second.UnitPrice, "var olan satırın birim fiyatı korunmalı")

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "aynı varyant için ikinci satır açılmamalı")
}

// TestAddLineItemFarkliVaryantYeniSatirAcar farklı varyantların ayrı satır
// olduğunu doğrular.
func TestAddLineItemFarkliVaryantYeniSatirAcar(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantB, Title: "Pantolon", Quantity: 1,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, 2)
}

// TestAddLineItemBirlestirmedeTavanAsilirsaReddedilir birleştirilen adedin
// tavanı aşması hâlinde isteğin reddedildiğini ve satırın DEĞİŞMEDİĞİNİ
// doğrular.
func TestAddLineItemBirlestirmedeTavanAsilirsaReddedilir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: models.MaxQuantity,
	})
	require.NoError(t, err)

	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, models.MaxQuantity, detail.Items[0].Quantity, "satır değişmemeli")
}

// TestUpdateLineItemQuantitySifiriReddeder sıfır adedin satırı SİLMEDİĞİNİ,
// reddedildiğini doğrular.
//
// "Adedi sıfır yap" ile "satırı kaldır" ayrı niyetlerdir; birini diğerine
// çevirmek, adet alanına sıfır gönderen bir hatanın sessizce veri silmesi
// demek olurdu.
func TestUpdateLineItemQuantitySifiriReddeder(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 2,
	})
	require.NoError(t, err)

	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "satır silinmemeli")
	assert.Equal(t, int64(2), detail.Items[0].Quantity, "adet değişmemeli")
}

// TestUpdateLineItemQuantityMutlakYazar adedin MUTLAK değerle yazıldığını
// (artımlı olmadığını) doğrular.
func TestUpdateLineItemQuantityMutlakYazar(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 5,
	})
	require.NoError(t, err)

	updated, err := svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 2)

	require.NoError(t, err)
	assert.Equal(t, int64(2), updated.Quantity)
}

// TestRemoveLineItemBaskaSepetinSatiriniSilemez satır kimliği bilinse bile
// başka bir sepetin satırının silinemediğini doğrular.
func TestRemoveLineItemBaskaSepetinSatiriniSilemez(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	mine := yeniSepet(ctx, t, svc)
	other, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	item, err := svc.AddLineItem(ctx, other.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)

	err = svc.RemoveLineItem(ctx, mine.ID, item.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	detail, err := svc.GetCart(ctx, other.ID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, 1, "diğer sepetin satırı durmalı")
}

// TestYazmaAkislariSepetiKilitler her yazma akışının sepeti KİLİTLEDİĞİNİ
// doğrular.
//
// Sahte depo, kilit metodunu işlem dışında çağıran bir akışta hata döner;
// dolayısıyla WithTx'i atlayan bir akış burada patlar. Ayrıca kilit sayısı,
// kilidin gerçekten alındığını gösterir.
func TestYazmaAkislariSepetiKilitler(t *testing.T) {
	svc, store := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 2)
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "İstanbul"})
	require.NoError(t, err)
	method, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standart", Amount: 0,
	})
	require.NoError(t, err)
	require.NoError(t, svc.RemoveShippingMethod(ctx, cart.ID, method.ID))
	require.NoError(t, svc.RemoveLineItem(ctx, cart.ID, item.ID))
	// Sepet artık boştur; hesap yine de dayandığı şekli bildirmek zorundadır.
	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{Revision: guncel.Revision}))

	assert.Len(t, store.lockedCarts, 7,
		"her yazma akışı sepeti tam bir kez kilitlemeli")
	for _, locked := range store.lockedCarts {
		assert.Equal(t, cart.ID, locked)
	}
	// SetTotals sepetin ŞEKLİNİ değiştirmez, yalnızca hesabı yazar; bu yüzden
	// kilit alan yedi akıştan yalnızca altısı şekil sayacını artırır. Sayaç
	// SetTotals'ta da artsaydı, toplamlar yazıldıkları anda bayat sayılır ve
	// hiçbir sepet tamamlanamazdı.
	assert.Equal(t, 6, store.bumpCalls,
		"yapısal değişiklikler sayacı artırmalı, SetTotals artırmamalı")
}

// TestYapisalDegisiklikToplamlariBayatlatir sepetin şeklini değiştiren her
// işlemin toplamları bayat işaretlediğini doğrular.
//
// Bayatlığın görünür olması, calculate_totals çalışmadan sepetin
// tamamlanmasını engelleyen tek şeydir (bkz. Service.MarkCompleted).
func TestYapisalDegisiklikToplamlariBayatlatir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	assert.False(t, cart.TotalsStale(), "boş sepetin toplamları güncel sayılır")

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 2, UnitPrice: 1000,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "satır eklendikten sonra toplamlar bayat olmalı")
	assert.Equal(t, int64(1), detail.Revision)

	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: detail.Revision,
		Subtotal: 2000, Total: 2000,
		Lines: []service.LineTotals{{
			LineItemID: detail.Items[0].ID, UnitPrice: 1000, Subtotal: 2000, Total: 2000,
		}},
	}))

	detail, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.TotalsStale(), "hesaplandıktan sonra toplamlar güncel olmalı")

	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Ankara"})
	require.NoError(t, err)

	detail, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.True(t, detail.TotalsStale(), "adresin değişmesi vergiyi etkiler, toplamlar bayatlamalı")
}

// TestTamamlanmisSepetDegistirilemez tamamlanmış bir sepette HER yazma
// yolunun errors.Conflict döndürdüğünü doğrular.
//
// Liste bilinçli olarak yazan tüm metotları sayar: yeni bir yazma yolu eklenip
// kontrol unutulursa bu test onu yakalamaz — ama var olanların hiçbirinin
// gevşemediğini garanti eder.
func TestTamamlanmisSepetDegistirilemez(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	method, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standart",
	})
	require.NoError(t, err)
	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: guncel.Revision,
		Subtotal: 500, Total: 500,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: 500, Subtotal: 500, Total: 500,
		}},
	}))

	completed, err := svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)
	require.True(t, completed.Completed())

	yazmalar := map[string]func() error{
		"AddLineItem": func() error {
			_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
				VariantID: variantB, Title: "Pantolon", Quantity: 1,
			})
			return err
		},
		"UpdateLineItemQuantity": func() error {
			_, err := svc.UpdateLineItemQuantity(ctx, cart.ID, item.ID, 3)
			return err
		},
		"RemoveLineItem": func() error {
			return svc.RemoveLineItem(ctx, cart.ID, item.ID)
		},
		"SetShippingAddress": func() error {
			_, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "İzmir"})
			return err
		},
		"SetBillingAddress": func() error {
			_, err := svc.SetBillingAddress(ctx, cart.ID, service.AddressInput{City: "İzmir"})
			return err
		},
		"AddShippingMethod": func() error {
			_, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{Name: "Hızlı"})
			return err
		},
		"RemoveShippingMethod": func() error {
			return svc.RemoveShippingMethod(ctx, cart.ID, method.ID)
		},
		"SetTotals": func() error {
			return svc.SetTotals(ctx, cart.ID, service.Totals{Subtotal: 500, Total: 500})
		},
		"DeleteCart": func() error {
			return svc.DeleteCart(ctx, cart.ID)
		},
		"MarkCompleted": func() error {
			_, err := svc.MarkCompleted(ctx, cart.ID)
			return err
		},
	}

	for ad, yazma := range yazmalar {
		t.Run(ad, func(t *testing.T) {
			err := yazma()
			require.Error(t, err, "%s tamamlanmış sepette hata dönmeli", ad)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err),
				"%s Conflict dönmeli, aldığı: %v", ad, err)
		})
	}

	// Sepetin içeriği gerçekten değişmemiş olmalı.
	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(1), detail.Items[0].Quantity)
	assert.Len(t, detail.ShippingMethods, 1)
	assert.Nil(t, detail.ShippingAddress)
}

// TestMarkCompletedBayatToplamiReddeder toplamları güncel olmayan bir sepetin
// tamamlanamadığını doğrular.
//
// Senaryo gerçektir: ödeme sayfası açıkken sepete satır eklenir. Damga atmak,
// o anki YANLIŞ tutarı sipariş tutarı hâline getirirdi.
func TestMarkCompletedBayatToplamiReddeder(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)

	_, err = svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeTotalsStale, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.Completed(), "reddedilen tamamlama damga atmamalı")
}

// TestMarkCompletedHicHesaplanmamisSepetiReddeder calculate_totals hiç
// koşmadan sepetin tamamlanamadığını doğrular.
//
// Bayatlık ölçütü totals_revision ≠ revision'dır ve yeni bir sepette ikisi de
// SIFIRDIR; "hiç hesaplanmadı" ile "sıfırıncı şekil için hesaplandı" ayırt
// edilemez. Sepet satırsız olduğu için asıl kapı orasıdır: satırsız bir
// sepetten doğacak sipariş, hiçbir şeyin satılmadığı bir sipariştir.
func TestMarkCompletedHicHesaplanmamisSepetiReddeder(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	require.False(t, cart.TotalsStale(), "yeni sepette bayatlık ölçütü sessizdir")

	_, err := svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err, "hiç hesaplanmamış sepet tamamlanamamalı")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.False(t, detail.Completed(), "reddedilen tamamlama damga atmamalı")
}

// TestMarkCompletedSatirsizSepetiReddeder satırları KALDIRILMIŞ ve toplamları
// yeniden hesaplanmış bir sepetin de tamamlanamadığını doğrular.
//
// Bu yol bayatlıktan geçmez: satırın silinmesi sayacı artırır, ardından
// çalışan hesap turu sepeti yeniden TAZE damgalar. Geriye tutarı sıfır,
// satırı olmayan, "güncel" bir sepet kalır.
func TestMarkCompletedSatirsizSepetiReddeder(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	require.NoError(t, svc.RemoveLineItem(ctx, cart.ID, item.ID))

	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{Revision: guncel.Revision}))

	guncel, err = svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.False(t, guncel.TotalsStale(), "sepet bayat DEĞİL; kapı bayatlık kapısı olamaz")

	_, err = svc.MarkCompleted(ctx, cart.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCartEmpty, errors.CodeOf(err))
}

// TestGetCartYirtikGorunumDondurmez sepetin dört okumasının TEK anlık görüntü
// üzerinde koştuğunu doğrular.
//
// Sahte depo, salt-okunur işlemin görüntüsünü dondurur ve okumaların ortasına
// bir yazma sokabilir. İşlemsiz hâlde sepet BAŞLIĞI eski, satır listesi yeni
// okunur; müşteriye "toplam 1000 ama satırlar 3000 ediyor" gibi kendi içinde
// tutarsız bir sepet gösterilirdi.
func TestGetCartYirtikGorunumDondurmez(t *testing.T) {
	svc, store := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	first, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	guncel, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: guncel.Revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{
			{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
		},
	}))

	// Sepet başlığı okunduktan SONRA, satırlar okunmadan ÖNCE araya tam bir
	// hesap turu girer.
	store.hookListLineItems = func() {
		second, addErr := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
			VariantID: variantB, Title: "Pantolon", Quantity: 1,
		})
		require.NoError(t, addErr)
		ara, getErr := svc.GetCart(ctx, cart.ID)
		require.NoError(t, getErr)
		require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
			Revision: ara.Revision, Subtotal: 3000, Total: 3000,
			Lines: []service.LineTotals{
				{LineItemID: first.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000},
				{LineItemID: second.ID, UnitPrice: 2000, Subtotal: 2000, Total: 2000},
			},
		}))
	}

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)

	var satirToplami int64
	for i := range detail.Items {
		satirToplami += detail.Items[i].Subtotal
	}
	assert.Equal(t, detail.Subtotal, satirToplami,
		"sepet başlığı ile satırlar AYNI ana ait olmalı")
}

// TestDeleteCartCocuklariTemizler silmenin sepeti ve çocuklarını birlikte
// temizlediğini doğrular.
func TestDeleteCartCocuklariTemizler(t *testing.T) {
	svc, store := yeniServis(t)
	ctx := context.Background()
	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	_, err = svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "İstanbul"})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{Name: "Standart"})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	_, err = svc.GetCart(ctx, cart.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "silinen sepet okunamamalı")

	items, err := store.ListLineItems(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, items, "satırlar da silinmeli")
	addresses, err := store.ListCartAddresses(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, addresses)
	methods, err := store.ListShippingMethods(ctx, cart.ID)
	require.NoError(t, err)
	assert.Empty(t, methods)
}

// TestAddLineItemHataHalindeHicbirSeyYazilmaz depo hatası aldığında işlemin
// tamamının geri alındığını doğrular.
//
// Özellikle şekil sayacı: satır yazılamadığı hâlde sayaç artsaydı, sepetin
// toplamları hiçbir sebep yokken bayat görünürdü.
func TestAddLineItemHataHalindeHicbirSeyYazilmaz(t *testing.T) {
	svc, store := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	store.failCreateLineItem = errors.Internal("cart_query_failed", "veritabanı düştü")

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})

	require.Error(t, err)
	detail, getErr := svc.GetCart(ctx, cart.ID)
	require.NoError(t, getErr)
	assert.Empty(t, detail.Items)
	assert.Equal(t, int64(0), detail.Revision, "başarısız işlem şekil sayacını artırmamalı")
}

// TestGetCartCocuklariylaDoner tam sepetin satır, adresi ve kargo yöntemiyle
// birlikte döndüğünü doğrular.
func TestGetCartCocuklariylaDoner(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)
	_, err = svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		City: "İstanbul", CountryCode: "tr",
	})
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, cart.ID, service.AddressInput{City: "Ankara"})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standart", Amount: 2500,
	})
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)

	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.ShippingAddress)
	require.NotNil(t, detail.BillingAddress)
	assert.Equal(t, "İstanbul", detail.ShippingAddress.City)
	assert.Equal(t, "TR", detail.ShippingAddress.CountryCode, "ülke kodu büyük harfe indirilmeli")
	assert.Equal(t, "Ankara", detail.BillingAddress.City)
	require.Len(t, detail.ShippingMethods, 1)
	assert.Equal(t, int64(2500), detail.ShippingMethods[0].Amount)
}

// TestSetAddressAyniTurdeIkinciKayitAcmaz aynı türden ikinci adresi yazmanın
// yeni kayıt AÇMADIĞINI, var olanı güncellediğini doğrular.
func TestSetAddressAyniTurdeIkinciKayitAcmaz(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	first, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "İstanbul"})
	require.NoError(t, err)
	second, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{City: "Bursa"})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "adresin kimliği sabit kalmalı")
	assert.Equal(t, "Bursa", second.City)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.ShippingAddress)
	assert.Equal(t, "Bursa", detail.ShippingAddress.City)
	assert.Nil(t, detail.BillingAddress, "kargo adresi fatura adresine yazılmamalı")
}

// TestSetAddressUlkeKoduHarfOlmali ülke kodunun yalnızca uzunluğuna değil
// HARFLERİNE de bakıldığını doğrular.
//
// Ülke kodu Faz 7'de vergi bölgesi ve kargo seçeneği eşlemesinin ANAHTARIDIR;
// "12" ya da "T1" gibi biçimsiz bir kod sepette sessizce durur ve hatasını çok
// sonra, eşleme aşamasında verirdi.
func TestSetAddressUlkeKoduHarfOlmali(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	for _, kod := range []string{"12", "T1", "1R", "t-"} {
		t.Run(kod, func(t *testing.T) {
			_, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
				City: "İstanbul", CountryCode: kod,
			})

			require.Error(t, err, "%q ülke kodu kabul edilmemeli", kod)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
		})
	}

	// Geçerli kod hâlâ büyük harfe tekleştirilir.
	addr, err := svc.SetShippingAddress(ctx, cart.ID, service.AddressInput{
		City: "İstanbul", CountryCode: "tr",
	})
	require.NoError(t, err)
	assert.Equal(t, "TR", addr.CountryCode)
}

// TestUpdateCartMisafirSepetiMusteriyeDevredilir misafir sepetin kayıtlı
// müşteriye devrini doğrular.
//
// Gerçek akış budur: müşteri sepeti misafir olarak açar, e-postasını ödeme
// adımında girer ve/veya araya giriş yapar. Bu yol olmadan sepetin baştan
// kurulması gerekirdi ve satırlar kaybolurdu.
func TestUpdateCartMisafirSepetiMusteriyeDevredilir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)
	require.True(t, cart.Guest())

	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})
	require.NoError(t, err)

	posta := "Musteri@Ornek.COM"
	updated, err := svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{
		Email: &posta, CustomerID: customerID,
	})

	require.NoError(t, err)
	assert.Equal(t, "musteri@ornek.com", updated.Email, "e-posta tekleştirilmeli")
	assert.Equal(t, customerID, updated.CustomerID)
	assert.False(t, updated.Guest())

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1, "devir satırları kaybetmemeli")
	assert.True(t, detail.TotalsStale(),
		"sahibi değişen sepetin fiyatı da değişebilir; toplamlar bayatlamalı")
}

// TestUpdateCartBaskaMusteriyeDevredilemez dolu bir sepetin başka bir müşteriye
// geçirilemediğini doğrular.
//
// İki farklı müşterinin aynı sepeti sahiplenmesi, siparişin kime yazılacağı
// sorusunu yanıtsız bırakırdı.
func TestUpdateCartBaskaMusteriyeDevredilemez(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	_, err = svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{CustomerID: "cust_BASKA"})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCustomerMismatch, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, customerID, detail.CustomerID, "reddedilen devir yazılmamalı")

	// Aynı müşterinin yeniden yazılması geçerlidir.
	_, err = svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{CustomerID: customerID})
	require.NoError(t, err)
}

// TestUpdateCartBosGirdiReddedilir hiçbir alan taşımayan güncellemenin
// reddedildiğini doğrular; sessizce başarılı sayılsaydı çağıran, gönderdiğini
// sandığı değişikliğin uygulandığını sanırdı.
func TestUpdateCartBosGirdiReddedilir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.UpdateCart(ctx, cart.ID, service.UpdateCartInput{})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestAddShippingMethodTutarNegatifOlamaz negatif kargo tutarının
// reddedildiğini doğrular.
func TestAddShippingMethodTutarNegatifOlamaz(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()
	cart := yeniSepet(ctx, t, svc)

	_, err := svc.AddShippingMethod(ctx, cart.ID, service.AddShippingMethodInput{
		Name: "Standart", Amount: -1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestOlmayanSepeteYazmaNotFound olmayan bir sepete yazmanın NotFound
// döndürdüğünü doğrular.
func TestOlmayanSepeteYazmaNotFound(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	_, err := svc.AddLineItem(ctx, "cart_YOK", service.AddLineItemInput{
		VariantID: variantA, Title: "Tişört", Quantity: 1,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestListCartsSuzerVeSayfalar listelemenin süzgeç ve sayfalama uyguladığını
// doğrular.
func TestListCartsSuzerVeSayfalar(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
		})
		require.NoError(t, err)
	}
	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	musteri := customerID
	carts, count, err := svc.ListCarts(ctx, service.ListCartsInput{
		CustomerID: &musteri,
		Page:       service.Page{Limit: 2},
	})

	require.NoError(t, err)
	assert.Equal(t, int64(3), count, "count sayfanın değil filtrenin sayısı olmalı")
	assert.Len(t, carts, 2, "sayfa boyutu uygulanmalı")

	bolge := regionOther
	_, count, err = svc.ListCarts(ctx, service.ListCartsInput{RegionID: &bolge})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestListCartsLimitTavaniAsilamaz sayfa boyutu tavanının uygulandığını
// doğrular.
func TestListCartsLimitTavaniAsilamaz(t *testing.T) {
	svc, _ := yeniServis(t)

	_, _, err := svc.ListCarts(context.Background(), service.ListCartsInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestKimlikBosluklaGelirseReddedilir baş/son boşluk taşıyan kimliğin
// KIRPILMADIĞINI, reddedildiğini doğrular.
//
// Kırpma, çağıranın gönderdiği kimlikle saklanan kimliği ayırır ve fark ancak
// veri bozulduktan sonra görünür; core/link de aynı sözleşmeyi uygular.
func TestKimlikBosluklaGelirseReddedilir(t *testing.T) {
	svc, _ := yeniServis(t)
	ctx := context.Background()

	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: " reg_TEST\n", CurrencyCode: currency,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}
