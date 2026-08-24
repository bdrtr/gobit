package service_test

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAdminOnlySecenekMagazayaCikmaz Faz 7'nin mağaza şartını sınar.
//
// admin_only bir seçenek vitrinde HİÇ görünmemelidir; süzgeç yalnızca yanıt
// üretilirken uygulansaydı, satır okunur ve bir sonraki refactor'da yanlışlıkla
// sızabilirdi.
func TestAdminOnlySecenekMagazayaCikmaz(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	acikID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	gizliID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Elden teslim",
		ShippingProfileID: profilID,
		Amount:            0,
		AdminOnly:         true,
	})

	magaza, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, magaza, 1, "mağaza yalnızca açık seçeneği görmeli")
	assert.Equal(t, acikID, magaza[0].Option.ID)

	yonetim, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode:     "TRY",
		IncludeAdminOnly: true,
	})
	require.NoError(t, err)
	require.Len(t, yonetim, 2, "yönetim her iki seçeneği de görmeli")

	kimlikler := []string{yonetim[0].Option.ID, yonetim[1].Option.ID}
	assert.Contains(t, kimlikler, gizliID, "admin_only seçenek yönetimde görünmeli")
}

// TestFlatSecenekSaglayiciyaGitmez sabit ücretli seçeneğin sağlayıcıya HİÇ
// sorulmadığını kanıtlar.
//
// Gitseydi sepet her güncellendiğinde gereksiz bir ağ çağrısı yapılır ve
// sağlayıcının erişilemez olduğu bir anda sabit ücretli seçenek de düşerdi.
func TestFlatSecenekSaglayiciyaGitmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, secenekler, 1)
	assert.Equal(t, int64(2_000), secenekler[0].Amount, "sabit ücret satırdan gelmeli")
	assert.Equal(t, "TRY", secenekler[0].CurrencyCode)

	quote, _, _ := kurulum.provider.cagriSayilari()
	assert.Zero(t, quote, "sabit ücretli seçenek için sağlayıcıya gidilmemeli")
}

// TestCalculatedSecenekSaglayicidanFiyatAlir hesaplanan seçeneğin ücretinin
// sağlayıcıdan geldiğini ve seçeneğin yapılandırmasının Quote'a AKTARILDIĞINI
// kanıtlar.
//
// Yapılandırma aktarılmasaydı sağlayıcı kilogram başına ücreti bilemez ve her
// gönderiye aynı fiyatı verirdi.
func TestCalculatedSecenekSaglayicidanFiyatAlir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	kurulum.provider.quoteAmount = 7_350
	profilID := kurulum.profilAc(t, "varsayilan")
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Hesaplanan kargo",
		ShippingProfileID: profilID,
		PriceType:         "calculated",
		Data:              map[string]any{"manual_per_kilogram_amount": 500},
	})

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		CountryCode:  "tr",
		ItemCount:    3,
		TotalWeight:  1_200,
	})
	require.NoError(t, err)
	require.Len(t, secenekler, 1)
	assert.Equal(t, int64(7_350), secenekler[0].Amount, "ücret sağlayıcıdan gelmeli")
	assert.NotEmpty(t, secenekler[0].ProviderData, "sağlayıcının ham verisi taşınmalı")

	girdi := kurulum.provider.sonQuoteGirdisi()
	assert.Equal(t, "TRY", girdi.CurrencyCode)
	assert.Equal(t, "TR", girdi.CountryCode, "ülke kodu büyük harfe çevrilmeli")
	assert.Equal(t, int64(3), girdi.ItemCount)
	assert.Equal(t, int64(1_200), girdi.TotalWeight)
	assert.Equal(t, 500, girdi.Data["manual_per_kilogram_amount"],
		"seçeneğin yapılandırması sağlayıcıya aktarılmalı")
}

// TestSaglayiciHatasiYalnizcaOSecenegiDusurur hesaplanan bir seçeneğin
// sağlayıcısı patladığında SABİT ücretli seçeneklerin ayakta kaldığını
// kanıtlar.
//
// İddia, tek bir kargo firmasının erişilemez olmasının ödeme adımını
// kapatmaması gerektiğidir (bkz. ListShippingOptionsFor godoc'u).
func TestSaglayiciHatasiYalnizcaOSecenegiDusurur(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	kurulum.provider.quoteErr = errors.Unavailable("test_saglayici_dustu", "kargo firmasına ulaşılamadı")
	profilID := kurulum.profilAc(t, "varsayilan")

	sabitID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Hesaplanan kargo",
		ShippingProfileID: profilID,
		PriceType:         "calculated",
	})

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err, "tek bir sağlayıcı hatası tüm isteği düşürmemeli")
	require.Len(t, secenekler, 1)
	assert.Equal(t, sabitID, secenekler[0].Option.ID)
}

// TestSaglayiciFarkliParaBirimiDonerseSecenekDuser sözleşme ihlalinin
// yakalandığını kanıtlar.
//
// Yakalanmasaydı, dolar cinsinden bir kargo ücreti lira sepetine sessizce
// eklenir ve fark ancak muhasebede görülürdü.
func TestSaglayiciFarkliParaBirimiDonerseSecenekDuser(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	kurulum.provider.quoteCurrency = "USD"
	profilID := kurulum.profilAc(t, "varsayilan")
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Hesaplanan kargo",
		ShippingProfileID: profilID,
		PriceType:         "calculated",
	})

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, secenekler, "farklı para biriminde fiyat veren seçenek listelenmemeli")
}

// TestUcretsizKargoKurali plan Faz 7'deki örnek kuralı sınar: "toplam >= 50000
// ise ücretsiz kargo".
//
// İki seçenek vardır ve ikisi de aynı profildedir; ayıran tek şey kuraldır.
// Ara toplam eşiğin altındayken ücretsiz seçenek SUNULMAZ, üstündeyken sunulur
// ve ucuz olduğu için LİSTENİN BAŞINA geçer.
func TestUcretsizKargoKurali(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	ucretsizID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Ücretsiz kargo",
		ShippingProfileID: profilID,
		Amount:            0,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), ucretsizID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	// Bağlam GÜVENİLİR işaretlenir: kurala bağlı seçenekler yalnızca sepet
	// olgularını sunucu tarafında üreten çağıranlara listelenir
	// (bkz. TestGuvenilmeyenBaglamdaKuralaBagliSecenekListelenmez).
	altinda, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     49_999,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, altinda, 1, "eşiğin altında ücretsiz kargo sunulmamalı")
	assert.Equal(t, int64(2_000), altinda[0].Amount)

	ustunde, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     50_000,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, ustunde, 2, "eşikte ücretsiz kargo da sunulmalı")
	assert.Equal(t, ucretsizID, ustunde[0].Option.ID, "ucuz olan listenin başında olmalı")
	assert.Equal(t, int64(0), ustunde[0].Amount)
	assert.Equal(t, int64(2_000), ustunde[1].Amount)

	// BASAMAK SAYISI eşikten fazla olan bir ara toplam: karşılaştırma dizgesel
	// olsaydı "100000" < "50000" çıkar ve eşiğin çok üstündeki bir sepet
	// ücretsiz kargoyu KAYBEDERDİ. Bu dal, karşılaştırmanın gerçekten sayısal
	// olduğunu kanıtlar.
	cokUstunde, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     100_000,
		TrustedFacts: true,
	})
	require.NoError(t, err)
	require.Len(t, cokUstunde, 2, "eşiğin çok üstünde de ücretsiz kargo sunulmalı")
	assert.Equal(t, ucretsizID, cokUstunde[0].Option.ID)
}

// TestKuralBaglamdaOlmayanAlanaBakarsaEslesmez kuralın baktığı alan bağlamda
// yoksa seçeneğin ELENDİĞİNİ kanıtlar — olumsuz işleçte bile.
//
// Aksi hâlde bağlamı boş bir istek, tüm olumsuz kuralları sağlayarak kısıtlı
// seçenekleri herkese açardı.
func TestKuralBaglamdaOlmayanAlanaBakarsaEslesmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "VIP kargo",
		ShippingProfileID: profilID,
		Amount:            1_000,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{
			Attribute: "customer_group_id",
			Operator:  "ne",
			Values:    []string{"blocked"},
		})
	require.NoError(t, err)

	baglamsiz, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, baglamsiz, "alanı taşımayan bağlam olumsuz kuralı SAĞLAMAMALI")

	baglamli, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Attributes:   map[string]string{"customer_group_id": "vip"},
	})
	require.NoError(t, err)
	require.Len(t, baglamli, 1, "alan verildiğinde kural değerlendirilmeli")
	assert.Equal(t, secenekID, baglamli[0].Option.ID)
}

// TestCagiranSepetOlgusunuEzemez çağıranın gönderdiği serbest alanların
// sepetin OLGULARININ üzerine yazamayacağını kanıtlar.
//
// Ezebilseydi vitrinden gelen tek bir "subtotal" değeri ücretsiz kargo
// kuralını atlatırdı.
func TestCagiranSepetOlgusunuEzemez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Ücretsiz kargo",
		ShippingProfileID: profilID,
		Amount:            0,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), secenekID,
		service.CreateRuleInput{
			Attribute: service.AttrSubtotal,
			Operator:  "gte",
			Values:    []string{"50000"},
		})
	require.NoError(t, err)

	// TrustedFacts=true: sınanan şey, GÜVENİLİR bir bağlamda bile çağıranın
	// serbest alanının sepetin olgusunu ezemediğidir. Bayrak verilmeseydi
	// seçenek zaten listelenmezdi ve iddia boşa düşerdi.
	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		Subtotal:     100,
		Attributes:   map[string]string{service.AttrSubtotal: "999999"},
		TrustedFacts: true,
	})
	require.NoError(t, err)
	assert.Empty(t, secenekler, "çağıranın verdiği subtotal, sepetin gerçek ara toplamını ezmemeli")
}

// TestGuvenilmeyenBaglamdaKuralaBagliSecenekListelenmez mağaza yüzeyinin
// KURAL ORACLE'ı olmadığını kanıtlar.
//
// Regresyon: sepet olguları (ara toplam, kalem adedi, ağırlık) mağaza ucunda
// doğrudan sorgu parametresinden geliyordu ve bu modül onları doğrulayamaz.
// Boş sepetle "subtotal=50000" gönderen bir müşteri, kendisine KAPALI olan
// ücretsiz kargo seçeneğini ve ücretini görüyordu. Artık güvenilmeyen bağlamda
// bu olgulara bağlı kuralı olan seçenek, olgu eşleşse bile listeye girmez.
func TestGuvenilmeyenBaglamdaKuralaBagliSecenekListelenmez(t *testing.T) {
	t.Parallel()

	for _, durum := range []struct {
		ad    string
		alan  string
		deger string
		girdi service.ListOptionsInput
	}{
		{"ara toplam", service.AttrSubtotal, "50000", service.ListOptionsInput{
			CurrencyCode: "TRY", Subtotal: 50_000,
		}},
		{"kalem adedi", service.AttrItemCount, "3", service.ListOptionsInput{
			CurrencyCode: "TRY", ItemCount: 3,
		}},
		{"toplam ağırlık", service.AttrTotalWeight, "1000", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: 1_000,
		}},
	} {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			kurulum := yeniKurulum(t)
			profilID := kurulum.profilAc(t, "varsayilan")
			acikID := kurulum.secenekAc(t, service.CreateOptionInput{
				Name:              "Standart kargo",
				ShippingProfileID: profilID,
				Amount:            2_000,
			})
			kisitliID := kurulum.secenekAc(t, service.CreateOptionInput{
				Name:              "Kurala bağlı kargo",
				ShippingProfileID: profilID,
				Amount:            0,
			})
			_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), kisitliID,
				service.CreateRuleInput{
					Attribute: durum.alan,
					Operator:  "gte",
					Values:    []string{durum.deger},
				})
			require.NoError(t, err)

			guvenilmeyen, err := kurulum.svc.ListShippingOptionsFor(context.Background(), durum.girdi)
			require.NoError(t, err)
			require.Len(t, guvenilmeyen, 1,
				"uydurulabilir bir olguya bağlı seçenek güvenilmeyen bağlamda listelenmemeli")
			assert.Equal(t, acikID, guvenilmeyen[0].Option.ID)

			// Aynı bağlam GÜVENİLİR işaretlendiğinde (sepet akışı) seçenek
			// görünür; süzgeç kuralı devre dışı bırakmaz, yalnızca yüzeye göre
			// uygular.
			guvenilir := durum.girdi
			guvenilir.TrustedFacts = true
			listelenen, err := kurulum.svc.ListShippingOptionsFor(context.Background(), guvenilir)
			require.NoError(t, err)
			require.Len(t, listelenen, 2, "güvenilir bağlamda kurala bağlı seçenek listelenmeli")
			assert.Equal(t, kisitliID, listelenen[0].Option.ID)
		})
	}
}

// TestGuvenilmeyenBaglamKuralsizSecenegiDusurmez süzgecin fazla geniş
// OLMADIĞINI kanıtlar.
//
// Kuralsız (koşulsuz) seçenekler ve uydurulamayan alanlara bağlı kurallar
// güvenilmeyen bağlamda da listelenmelidir; aksi hâlde düzeltme, mağaza
// vitrinini tümüyle boşaltırdı.
func TestGuvenilmeyenBaglamKuralsizSecenegiDusurmez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	kuralsizID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	bolgeliID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Bölgesel kargo",
		ShippingProfileID: profilID,
		Amount:            1_000,
	})
	_, err := kurulum.svc.CreateShippingOptionRule(context.Background(), bolgeliID,
		service.CreateRuleInput{
			Attribute: service.AttrCountryCode,
			Operator:  "eq",
			Values:    []string{"TR"},
		})
	require.NoError(t, err)

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		CountryCode:  "TR",
	})
	require.NoError(t, err)
	require.Len(t, secenekler, 2, "kuralsız ve kapsam alanına bağlı seçenekler düşmemeli")
	assert.Equal(t, bolgeliID, secenekler[0].Option.ID)
	assert.Equal(t, kuralsizID, secenekler[1].Option.ID)
}

// TestProfilSuzgeci sepetin ürünlerinin bağlı olmadığı profillerin seçeneğinin
// sunulmadığını kanıtlar; profil verilmezse süzgeç uygulanmaz.
func TestProfilSuzgeci(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	varsayilanID := kurulum.profilAc(t, "varsayilan")
	agirID := kurulum.profilAc(t, "agir-yuk")

	standartID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: varsayilanID,
		Amount:            2_000,
	})
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Ağır yük kargosu",
		ShippingProfileID: agirID,
		Amount:            20_000,
	})

	suzulmus, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode:       "TRY",
		ShippingProfileIDs: []string{varsayilanID},
	})
	require.NoError(t, err)
	require.Len(t, suzulmus, 1)
	assert.Equal(t, standartID, suzulmus[0].Option.ID)

	suzgecsiz, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Len(t, suzgecsiz, 2, "profil verilmezse süzgeç uygulanmamalı")
}

// TestBolgeSuzgeci bölgesi boş olan seçeneğin HER bölgede sunulduğunu,
// bölgesi olanın ise yalnızca kendi bölgesinde sunulduğunu kanıtlar.
func TestBolgeSuzgeci(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	genelID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Genel kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	bolgeselID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Bölgesel kargo",
		ShippingProfileID: profilID,
		Amount:            1_000,
		RegionID:          "reg_tr",
	})

	trBolgesi, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		RegionID:     "reg_tr",
	})
	require.NoError(t, err)
	require.Len(t, trBolgesi, 2)
	assert.Equal(t, bolgeselID, trBolgesi[0].Option.ID, "ucuz olan başta olmalı")

	deBolgesi, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		RegionID:     "reg_de",
	})
	require.NoError(t, err)
	require.Len(t, deBolgesi, 1, "başka bölgede yalnızca bölgesiz seçenek sunulmalı")
	assert.Equal(t, genelID, deBolgesi[0].Option.ID)
}

// TestIadeSecenekleriAyriListelenir normal akışta iade seçeneklerinin
// sunulmadığını kanıtlar.
func TestIadeSecenekleriAyriListelenir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")

	satisID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	iadeID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "İade kargosu",
		ShippingProfileID: profilID,
		Amount:            0,
		IsReturn:          true,
	})

	satis, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, satis, 1)
	assert.Equal(t, satisID, satis[0].Option.ID)

	iade, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
		IsReturn:     true,
	})
	require.NoError(t, err)
	require.Len(t, iade, 1)
	assert.Equal(t, iadeID, iade[0].Option.ID)
}

// TestParaBirimiSuzgeci başka para biriminde fiyatlanmış seçeneğin
// sunulmadığını kanıtlar.
//
// Sunulsaydı, iki para biriminin tutarları aynı sepette toplanırdı.
func TestParaBirimiSuzgeci(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Dolar kargosu",
		ShippingProfileID: profilID,
		Amount:            500,
		CurrencyCode:      "USD",
	})

	secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	assert.Empty(t, secenekler)
}

// TestUygunlukSiralamasiBelirlenimcidir aynı ücretteki seçeneklerin KİMLİĞE
// göre sıralandığını ve sonucun çağrıdan çağrıya değişmediğini kanıtlar.
func TestUygunlukSiralamasiBelirlenimcidir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	for _, ad := range []string{"A kargo", "B kargo", "C kargo"} {
		kurulum.secenekAc(t, service.CreateOptionInput{
			Name:              ad,
			ShippingProfileID: profilID,
			Amount:            2_000,
		})
	}

	ilk, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	require.Len(t, ilk, 3)

	for i := 1; i < len(ilk); i++ {
		assert.Less(t, ilk[i-1].Option.ID, ilk[i].Option.ID,
			"eşit ücrette sıralama kimliğe göre artan olmalı")
	}

	ikinci, err := kurulum.svc.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	for i := range ilk {
		assert.Equal(t, ilk[i].Option.ID, ikinci[i].Option.ID, "sıra çağrıdan çağrıya değişmemeli")
	}
}

// TestUygunlukGirdisiDogrulanir geçersiz girdinin errors.Invalid ile
// reddedildiğini kanıtlar.
func TestUygunlukGirdisiDogrulanir(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		ad    string
		girdi service.ListOptionsInput
	}{
		{"para birimi yok", service.ListOptionsInput{}},
		{"para birimi bozuk", service.ListOptionsInput{CurrencyCode: "TR"}},
		{"negatif ara toplam", service.ListOptionsInput{CurrencyCode: "TRY", Subtotal: -1}},
		{"negatif kalem adedi", service.ListOptionsInput{CurrencyCode: "TRY", ItemCount: -1}},
		{"negatif ağırlık", service.ListOptionsInput{CurrencyCode: "TRY", TotalWeight: -1}},
		// ÜST sınırlar: yalnızca negatifliğe bakan bir denetim, tek bir sorgu
		// parametresiyle sağlayıcının çarpımını taşırmaya izin verirdi.
		{"ara toplam üst sınırı aşıyor", service.ListOptionsInput{
			CurrencyCode: "TRY", Subtotal: models.MaxAmount + 1,
		}},
		{"kalem adedi üst sınırı aşıyor", service.ListOptionsInput{
			CurrencyCode: "TRY", ItemCount: models.MaxItemCount + 1,
		}},
		{"ağırlık üst sınırı aşıyor", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: models.MaxTotalWeight + 1,
		}},
		{"ağırlık int64 tepesinde", service.ListOptionsInput{
			CurrencyCode: "TRY", TotalWeight: math.MaxInt64,
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			kurulum := yeniKurulum(t)
			_, err := kurulum.svc.ListShippingOptionsFor(context.Background(), durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		})
	}
}

// TestKayitliOlmayanSaglayiciSecenegiDuser kaydı sonradan kaybolmuş bir
// sağlayıcının seçeneğinin listeden düştüğünü ve isteğin düşmediğini kanıtlar.
//
// Seçenek KAYITLI bir sağlayıcıyla açılır, sonra servis o sağlayıcıyı
// tanımayan yeni bir kayıtla yeniden kurulur; kurulum hatası tam olarak böyle
// görünür.
func TestKayitliOlmayanSaglayiciSecenegiDuser(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	sabitID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
	})
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Kayıp sağlayıcı kargosu",
		ShippingProfileID: profilID,
		PriceType:         "calculated",
	})

	bosKayit := service.NewProviderRegistry()
	kayipsiz, err := service.New(service.Options{Store: kurulum.store, Providers: bosKayit})
	require.NoError(t, err)

	secenekler, err := kayipsiz.ListShippingOptionsFor(context.Background(), service.ListOptionsInput{
		CurrencyCode: "TRY",
	})
	require.NoError(t, err, "kayıp sağlayıcı tüm isteği düşürmemeli")
	require.Len(t, secenekler, 1)
	assert.Equal(t, sabitID, secenekler[0].Option.ID)
}

// TestBozukKuralSecenegiHerkeseAcmaz uygunluk hesabının veritabanından okuduğu
// HER satıra dayanıklı olduğunu kanıtlar.
//
// Servis doğrulaması değersiz ya da tanınmayan işleçli bir kural üretmez; ama
// doğrudan SQL çalıştıran bir bakım betiği ya da kısmi bir geri yükleme böyle
// bir satır bırakabilir. Okunamayan bir koşul, kuralı sessizce devre dışı
// bırakıp seçeneği HERKESE AÇMAMALIDIR — bu yüzden bozuk kural EŞLEŞMEZ.
//
// Kurallar sahte deponun içine doğrudan yazılır; servisin kapısından geçen bir
// girdi bu satırları hiç üretemez.
func TestBozukKuralSecenegiHerkeseAcmaz(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		ad    string
		bozuk models.ShippingOptionRule
	}{
		{"değersiz kural", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.OpGte, Values: nil,
		}},
		{"tanınmayan işleç", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.RuleOperator("like"),
			Values: []string{"1"},
		}},
		{"sayısal işlece metin değer", models.ShippingOptionRule{
			Attribute: service.AttrSubtotal, Operator: models.OpGte,
			Values: []string{"elli bin"},
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			kurulum := yeniKurulum(t)
			profilID := kurulum.profilAc(t, "varsayilan")
			secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
				Name:              "Kısıtlı kargo",
				ShippingProfileID: profilID,
				Amount:            2_000,
			})

			kural := durum.bozuk
			kural.ID = models.NewShippingOptionRuleID()
			kural.ShippingOptionID = secenekID
			kurulum.store.mu.Lock()
			kurulum.store.rules[kural.ID] = kural
			kurulum.store.mu.Unlock()

			secenekler, err := kurulum.svc.ListShippingOptionsFor(context.Background(),
				service.ListOptionsInput{CurrencyCode: "TRY", Subtotal: 999_999, TrustedFacts: true})
			require.NoError(t, err, "bozuk kural PANİK ya da hata üretmemeli")
			assert.Empty(t, secenekler, "okunamayan koşul seçeneği açmamalı")
		})
	}
}
