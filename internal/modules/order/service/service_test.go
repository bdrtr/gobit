package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// Test verisinde kullanılan sabitler. Bölge, müşteri ve varyant kimlikleri
// BAŞKA modüllere aittir; bu modül onların varlığını doğrulamaz (Prensip 2.2).
const (
	testRegionID   = "reg_TEST"
	testCustomerID = "cus_TEST"
	testVariantID  = "variant_TEST"
	testCurrency   = "TRY"
)

// ortam bir test için kurulmuş servis ve sahtelerini taşır.
type ortam struct {
	svc   *service.Service
	store *fakeStore
	bus   *fakeBus
}

// yeniOrtam sahte bağımlılıklarla kurulmuş bir servis üretir.
func yeniOrtam(t *testing.T) ortam {
	t.Helper()

	store := newFakeStore()
	bus := newFakeBus()

	svc, err := service.New(service.Options{Repo: store, Events: bus})
	require.NoError(t, err)

	return ortam{svc: svc, store: store, bus: bus}
}

// gecerliGirdi tutarlı bir sipariş girdisi üretir.
//
// Rakamlar bilinçli olarak "gerçekçi"dir: 3 × 1000 = 3000 ara toplam, %20 vergi
// 600, kargo 2500 -> toplam 6100. Testler bu tabanı bozarak tek tek kuralları
// sınar; her testin kendi girdisini kurması, hangi alanın DEĞİŞTİRİLDİĞİNİ
// görünmez kılardı.
func gecerliGirdi() service.CreateOrderInput {
	return service.CreateOrderInput{
		RegionID:      testRegionID,
		CustomerID:    testCustomerID,
		Email:         "Musteri@Ornek.COM",
		CurrencyCode:  "try",
		CartID:        "cart_TEST",
		Subtotal:      3000,
		DiscountTotal: 0,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		Metadata:      map[string]any{"kanal": "web"},
		Items: []service.CreateOrderItemInput{
			{
				VariantID: testVariantID,
				Title:     "Kırmızı Tişört",
				Quantity:  3,
				UnitPrice: 1000,
				Subtotal:  3000,
				TaxTotal:  600,
				Total:     3600,
			},
		},
	}
}

// TestCreateOrderSiparisiSatirlariniVeOzetiniYazar mutlu yolu doğrular.
func TestCreateOrderSiparisiSatirlariniVeOzetiniYazar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(siparis.ID, models.OrderIDPrefix))
	assert.Equal(t, models.OrderPending, siparis.Status)
	assert.Equal(t, "TRY", siparis.CurrencyCode, "para birimi büyük harfe çevrilmeli")
	assert.Equal(t, "musteri@ornek.com", siparis.Email, "e-posta küçük harfe çevrilmeli")
	assert.Equal(t, int64(6100), siparis.Total)

	// Numarayı DEPO üretir; servis kendi üretmez.
	assert.Equal(t, int64(1), siparis.DisplayID)

	detay, err := o.svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, err)
	require.Len(t, detay.Items, 1)
	assert.Equal(t, testVariantID, detay.Items[0].VariantID)
	assert.Equal(t, int64(3600), detay.Items[0].Total)
	assert.True(t, strings.HasPrefix(detay.Items[0].ID, models.LineItemIDPrefix))

	// Özet siparişle birlikte ve SIFIRLANMIŞ doğar.
	assert.Equal(t, siparis.ID, detay.Summary.OrderID)
	assert.Equal(t, int64(0), detay.Summary.PaidTotal)
	assert.Equal(t, int64(6100), detay.Summary.Outstanding(detay.Total))
}

// TestCreateOrderIkinciSiparisSonrakiNumarayiAlir numaranın ARTAN olduğunu
// doğrular.
func TestCreateOrderIkinciSiparisSonrakiNumarayiAlir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	ilk, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	ikinci, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	assert.Equal(t, int64(1), ilk.DisplayID)
	assert.Equal(t, int64(2), ikinci.DisplayID)
	assert.NotEqual(t, ilk.ID, ikinci.ID)
}

// TestCreateOrderTutarsizGirdiyiReddeder toplam doğrulamasının her katmanını
// tek tek sınar.
//
// Her satır TEK bir alanı bozar: hangi kuralın hangi girdiyi yakaladığı
// böylece okunur. Girdinin tamamı geçerli tabandan türetildiği için, bir
// kontrol kaldırılırsa YALNIZCA onun satırı düşer.
func TestCreateOrderTutarsizGirdiyiReddeder(t *testing.T) {
	testler := map[string]struct {
		boz  func(in *service.CreateOrderInput)
		kod  string
		icer string
	}{
		"sipariş toplamı kimliği tutmuyor": {
			boz:  func(in *service.CreateOrderInput) { in.Total = 6099 },
			kod:  service.CodeTotalsInconsistent,
			icer: "sipariş toplamı tutarsız",
		},
		"indirim ara toplamı aşıyor ama kimlik sağlanıyor": {
			// subtotal=3000, discount=4000, tax=600, shipping=2500 -> total=2100.
			// Kimlik SAĞLANIR; yakalayan tek şey indirim sınırıdır.
			boz: func(in *service.CreateOrderInput) {
				in.DiscountTotal = 4000
				in.Total = 2100
			},
			kod:  service.CodeTotalsInconsistent,
			icer: "indirim ara toplamı aşamaz",
		},
		"satır ara toplamı adetle çarpımı tutmuyor": {
			boz: func(in *service.CreateOrderInput) {
				in.Items[0].Quantity = 2
			},
			kod:  service.CodeTotalsInconsistent,
			icer: "satır ara toplamı tutarsız",
		},
		"satır toplamı kimliği tutmuyor": {
			boz: func(in *service.CreateOrderInput) {
				in.Items[0].Total = 3599
			},
			kod:  service.CodeTotalsInconsistent,
			icer: "satır toplamı tutarsız",
		},
		"satır indirimi ara toplamı aşıyor": {
			boz: func(in *service.CreateOrderInput) {
				in.Items[0].DiscountTotal = 4000
				in.Items[0].Total = -400 + 600 // subtotal - discount + tax
			},
			kod:  service.CodeTotalsInconsistent,
			icer: "ara toplamı aşamaz",
		},
		"sipariş ara toplamı satırların toplamına eşit değil": {
			// Satırları göndermeyi "unutan" hesabın karşılığı: tek satır 1000
			// eder ama sipariş 3000 iddia eder.
			boz: func(in *service.CreateOrderInput) {
				in.Items[0].Quantity = 1
				in.Items[0].Subtotal = 1000
				in.Items[0].TaxTotal = 600
				in.Items[0].Total = 1600
			},
			kod:  service.CodeTotalsInconsistent,
			icer: "satırların ara toplamlarına eşit olmalı",
		},
		"satırsız sipariş": {
			boz:  func(in *service.CreateOrderInput) { in.Items = nil },
			kod:  service.CodeOrderEmpty,
			icer: "en az bir satır",
		},
		"negatif toplam": {
			boz:  func(in *service.CreateOrderInput) { in.Total = -1; in.Subtotal = -1 },
			kod:  service.CodeInvalidInput,
			icer: "negatif olamaz",
		},
	}

	for ad, tc := range testler {
		t.Run(ad, func(t *testing.T) {
			ctx := context.Background()
			o := yeniOrtam(t)

			in := gecerliGirdi()
			tc.boz(&in)

			_, err := o.svc.CreateOrder(ctx, in)

			require.Error(t, err, "tutarsız girdi kabul edilmemeli")
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, tc.kod, errors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.icer)

			// Reddedilen istek HİÇBİR ŞEY yazmamalı.
			siparisler, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
			require.NoError(t, listErr)
			assert.Zero(t, sayi)
			assert.Empty(t, siparisler)
			assert.Empty(t, o.bus.events(), "reddedilen istek olay yayımlamamalı")
		})
	}
}

// TestCreateOrderZorunluAlanlariDogrular kimlik ve kod alanlarının
// doğrulandığını gösterir.
func TestCreateOrderZorunluAlanlariDogrular(t *testing.T) {
	testler := map[string]func(in *service.CreateOrderInput){
		"bölge boş":              func(in *service.CreateOrderInput) { in.RegionID = "" },
		"para birimi boş":        func(in *service.CreateOrderInput) { in.CurrencyCode = "" },
		"para birimi harf değil": func(in *service.CreateOrderInput) { in.CurrencyCode = "TR1" },
		"e-posta bozuk":          func(in *service.CreateOrderInput) { in.Email = "musteri" },
		"satır varyantı boş":     func(in *service.CreateOrderInput) { in.Items[0].VariantID = "" },
		"satır başlığı boş":      func(in *service.CreateOrderInput) { in.Items[0].Title = "" },
		"adet sıfır": func(in *service.CreateOrderInput) {
			in.Items[0].Quantity = 0
			in.Items[0].Subtotal = 0
			in.Items[0].TaxTotal = 0
			in.Items[0].Total = 0
			in.Subtotal, in.TaxTotal, in.Total = 0, 0, 2500
		},
		"müşteri kimliği boşluklu": func(in *service.CreateOrderInput) { in.CustomerID = " cus_1" },
	}

	for ad, boz := range testler {
		t.Run(ad, func(t *testing.T) {
			ctx := context.Background()
			o := yeniOrtam(t)

			in := gecerliGirdi()
			boz(&in)

			_, err := o.svc.CreateOrder(ctx, in)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestCreateOrderBolgeVeMusteriyiSutunaYazar siparişin bölgesinin ve
// müşterisinin KENDİ SÜTUNLARINDA durduğunu doğrular.
//
// İlişkinin tek yeri budur: sipariş bir de link tablosuna yazılmaz ve bu iddia
// o kararın bekçisidir — ikinci bir kopya eklenirse sütun ile bağ ayrışabilir.
func TestCreateOrderBolgeVeMusteriyiSutunaYazar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	assert.Equal(t, testRegionID, siparis.RegionID)
	assert.Equal(t, testCustomerID, siparis.CustomerID)
}

// TestCreateOrderMisafirSiparisiMusterisizAcilir müşteri kimliği verilmeyen
// siparişin MİSAFİR olarak açıldığını doğrular.
func TestCreateOrderMisafirSiparisiMusterisizAcilir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	in := gecerliGirdi()
	in.CustomerID = ""

	siparis, err := o.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	assert.True(t, siparis.Guest())
	assert.Empty(t, siparis.CustomerID)
	assert.Equal(t, testRegionID, siparis.RegionID)
}

// TestCreateOrderNumarasizSiparisiGeriAlir depo kullanılabilir bir numara
// vermezse siparişin yazılı kalmadığını doğrular.
//
// Numarasız sipariş, müşterinin hiçbir yerde bulamayacağı bir siparişdir.
func TestCreateOrderNumarasizSiparisiGeriAlir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	bozukNumara := int64(0)
	o.store.forceDisplayID = &bozukNumara

	_, err := o.svc.CreateOrder(ctx, gecerliGirdi())

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeDisplayIDInvalid, errors.CodeOf(err))

	_, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, listErr)
	assert.Zero(t, sayi, "numarasız sipariş geri alınmalı")
	assert.Empty(t, o.bus.events())
}

// TestCreateOrderSatirYazilamazsaHicbirSeyYazilmaz siparişin ve satırlarının
// TEK işlemde yazıldığını doğrular.
func TestCreateOrderSatirYazilamazsaHicbirSeyYazilmaz(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	o.store.failCreateLineItem = errors.Internal("depo_patladi", "satır yazılamadı")

	_, err := o.svc.CreateOrder(ctx, gecerliGirdi())

	require.Error(t, err)
	_, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, listErr)
	assert.Zero(t, sayi, "satır yazılamayınca sipariş de yazılmamalı")
	assert.Empty(t, o.bus.events())
}

// TestCreateOrderIdempotencyAnahtariIkinciSiparisiEngeller aynı anahtarla
// yapılan ikinci çağrının MEVCUT siparişi döndürdüğünü doğrular.
func TestCreateOrderIdempotencyAnahtariIkinciSiparisiEngeller(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	in := gecerliGirdi()
	in.IdempotencyKey = "wf_ADIM_1"

	ilk, err := o.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	ikinci, err := o.svc.CreateOrder(ctx, in)
	require.NoError(t, err, "aynı anahtarla ikinci çağrı hata vermemeli")

	assert.Equal(t, ilk.ID, ikinci.ID)
	assert.Equal(t, ilk.DisplayID, ikinci.DisplayID)

	_, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, listErr)
	assert.Equal(t, int64(1), sayi, "ikinci çağrı yeni sipariş açmamalı")
	assert.Len(t, o.bus.events(), 1, "ikinci çağrı ikinci olay yayımlamamalı")
}

// TestCreateOrderEszamanliIdempotentCagriYarisiKaybedeniDeCevaplar veritabanı
// benzersizlik ihlaliyle dönen çağrının da mevcut siparişi döndürdüğünü
// doğrular.
//
// Senaryo, ucuz yolun (önce ara) yakalayamadığı YARIŞTIR: iki çağrı da anahtarı
// bulamaz, ikisi de yazmaya kalkar ve indeks ikincisini reddeder. Yarış
// zamanlamaya bağlı olduğu için sahte deponun kancasıyla deterministik
// kurulur.
func TestCreateOrderEszamanliIdempotentCagriYarisiKaybedeniDeCevaplar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	in := gecerliGirdi()
	in.IdempotencyKey = "wf_ADIM_1"

	// Kancanın kendisi rakip çağrıyı yapar: bizim çağrımız anahtarı bulamadan
	// yola çıkar, tam yazacakken rakip kaydı oluşur ve indeks bizi reddeder.
	var rakip models.Order
	o.store.hookCreateOrder = func() {
		var hataRakip error
		rakip, hataRakip = o.svc.CreateOrder(ctx, in)
		require.NoError(t, hataRakip)
	}

	sonuc, err := o.svc.CreateOrder(ctx, in)

	require.NoError(t, err, "yarışı kaybeden çağrı hata değil mevcut siparişi dönmeli")
	assert.Equal(t, rakip.ID, sonuc.ID)

	_, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, listErr)
	assert.Equal(t, int64(1), sayi)
}

// TestCreateOrderAnahtarsizCagriHerSeferindeYeniSiparisAcar idempotency
// anahtarının OPSİYONEL olduğunu ve verilmediğinde koruma sağlamadığını
// gösterir.
func TestCreateOrderAnahtarsizCagriHerSeferindeYeniSiparisAcar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	ilk, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	ikinci, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	assert.NotEqual(t, ilk.ID, ikinci.ID)
	_, sayi, listErr := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, listErr)
	assert.Equal(t, int64(2), sayi)
}

// TestCreateOrderPlacedOlayiYayimlar DoD şartını doğrular: sipariş
// oluşturulduğunda "order.placed" yayımlanır ve yükü gerekli alanları taşır.
func TestCreateOrderPlacedOlayiYayimlar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	olaylar := o.bus.events()
	require.Len(t, olaylar, 1)
	assert.Equal(t, service.EventOrderPlaced, olaylar[0].Name)

	veri := olaylar[0].Data
	assert.Equal(t, siparis.ID, veri[service.EventFieldOrderID])
	assert.Equal(t, "1", veri[service.EventFieldDisplayID])
	assert.Equal(t, "6100", veri[service.EventFieldTotal])
	assert.Equal(t, siparis.CurrencyCode, veri[service.EventFieldCurrencyCode])
	assert.Equal(t, siparis.CustomerID, veri[service.EventFieldCustomerID])
	assert.Equal(t, siparis.RegionID, veri[service.EventFieldRegionID])
	assert.Equal(t, models.OrderPending.String(), veri[service.EventFieldStatus])
	assert.Equal(t, "1", veri[service.EventFieldItemCount])

	// E-posta olaya KONMAZ: olaylar kalıcı bir akışa yazılır ve kişisel veri
	// orada gereksiz yere yayılırdı.
	assert.NotContains(t, veri, "email")
}

// TestOrderPlacedYukuJSONTuruDegistirmez olay yükünün üretim veri yolundan
// geçtiğinde TİP ve DEĞER değiştirmediğini doğrular.
//
// Üretimdeki Redis Streams backend'i Data'yı json.Marshal ile yazar ve okurken
// map[string]any içine çözer (bkz. core/eventbus redis.go). JSON'un tek sayı
// tipi olduğu için int64 olarak konan her alan aboneye float64 olarak ulaşırdı:
// sözleşmeye göre yazılmış bir abone geliştirmede (InMemory) çalışır, üretimde
// düşerdi ve 2^53 üstündeki tutarlar sessizce yuvarlanırdı — yani para float
// üzerinden geçerdi (plan Bölüm 8: float ASLA).
//
// Test o dönüşümü BİREBİR taklit eder ve iki şeyi ister: her değer string
// KALMALI ve tutar TAM olarak geri okunabilmelidir.
func TestOrderPlacedYukuJSONTuruDegistirmez(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	// 2^53'ün üstünde bir tutar: float64'e uğrayan bir yol burada yuvarlar.
	const buyukTutar int64 = 9_007_199_254_740_993

	in := gecerliGirdi()
	in.ShippingTotal = buyukTutar - 3600
	in.Total = buyukTutar
	siparis, err := o.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	olaylar := o.bus.events()
	require.Len(t, olaylar, 1)

	ham, err := json.Marshal(olaylar[0].Data)
	require.NoError(t, err)
	var teslim map[string]any
	require.NoError(t, json.Unmarshal(ham, &teslim))

	for anahtar, deger := range teslim {
		assert.IsType(t, "", deger,
			"%q alanı veri yolundan geçince dize kalmalı", anahtar)
	}

	hamTutar, ok := teslim[service.EventFieldTotal].(string)
	require.True(t, ok, "tutar dize olarak taşınmalı")
	okunan, err := strconv.ParseInt(hamTutar, 10, 64)
	require.NoError(t, err)
	assert.Equal(t, siparis.Total, okunan, "tutar yuvarlanmadan geri okunabilmeli")
	assert.Equal(t, buyukTutar, okunan)
}

// TestCreateOrderOlayYayimiDusersaSiparisYazilmisKalir yayım hatasının
// siparişi düşürmediğini doğrular.
//
// Karar bilinçlidir: sipariş KAYITTIR, olay ise duyurudur. Veri yolunun bir
// saniyelik erişilemezliği yüzünden ödemesi alınmış bir siparişi geri almak,
// korumaya çalıştığı şeyden pahalı bir kayıp olurdu.
func TestCreateOrderOlayYayimiDusersaSiparisYazilmisKalir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	o.bus.failErr = errors.Unavailable("eventbus_publish_failed", "veri yolu erişilemez")

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())

	require.NoError(t, err, "olay yayımı hatası siparişi düşürmemeli")
	assert.NotEmpty(t, siparis.ID)

	okunan, getErr := o.svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, getErr, "sipariş yazılmış olmalı")
	assert.Equal(t, siparis.ID, okunan.ID)
}

// TestCancelOrderIdempotenttir saga telafisinin ikinci kez çağrılabildiğini
// doğrular (DoD şartı).
func TestCancelOrderIdempotenttir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	require.NoError(t, o.svc.CancelOrder(ctx, siparis.ID, "ödeme reddedildi"))
	require.NoError(t, o.svc.CancelOrder(ctx, siparis.ID, "telafi tekrarı"),
		"ikinci iptal hata vermemeli")

	iptal, err := o.svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, iptal.Status)
	require.NotNil(t, iptal.CanceledAt)
	assert.Equal(t, "ödeme reddedildi", iptal.CancelReason,
		"ilk iptalin gerekçesi korunmalı; iptal gerçekte orada olmuştur")
}

// TestCancelOrderKilitAlir iptalin siparişin satır kilidini aldığını doğrular.
//
// Kilit bir EŞZAMANLILIK sözleşmesidir: kilitsiz bir "durumu oku, durumu yaz"
// akışında eşzamanlı bir iptal ile tamamlama birbirini ezerdi. Gerçek
// veritabanında ihlal ancak yarış altında görünür; burada doğrudan okunur.
func TestCancelOrderKilitAlir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	require.NoError(t, o.svc.CancelOrder(ctx, siparis.ID, ""))

	assert.Contains(t, o.store.lockedOrders, siparis.ID,
		"iptal siparişin kilidini almalı")
}

// TestCancelOrderTamamlanmisSiparisteConflict tamamlanmış siparişin iptal
// edilemediğini doğrular.
//
// Tamamlanmış siparişin ödemesi tahsil edilmiştir; "iptal" damgası tahsil
// edilmiş tutarı hiçbir siparişe bağlı olmayan bir tutar hâline getirirdi.
func TestCancelOrderTamamlanmisSiparisteConflict(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	_, err = o.svc.CompleteOrder(ctx, siparis.ID)
	require.NoError(t, err)

	err = o.svc.CancelOrder(ctx, siparis.ID, "vazgeçildi")

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "iade/değişim")

	guncel, err := o.svc.GetOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, guncel.Status, "durum değişmemeli")
}

// TestCancelOrderOlmayanSiparisNotFound eksik siparişin NotFound döndüğünü
// doğrular.
func TestCancelOrderOlmayanSiparisNotFound(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	err := o.svc.CancelOrder(ctx, "order_YOK", "")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestCompleteOrderIkinciCagridaConflict tamamlamanın idempotent OLMADIĞINI
// doğrular.
//
// İptalin aksine tamamlama bir telafi değil, ileri yönlü bir adımdır. Sessizce
// başarı saymak, aynı siparişin iki kez kapatıldığı bir akışı görünmez kılardı.
func TestCompleteOrderIkinciCagridaConflict(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	tamamlanan, err := o.svc.CompleteOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, tamamlanan.Status)
	require.NotNil(t, tamamlanan.CompletedAt)

	_, err = o.svc.CompleteOrder(ctx, siparis.ID)

	require.Error(t, err, "ikinci tamamlama hata dönmeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
}

// TestCompleteOrderIptalEdilmisSiparisteConflict iptalin DURAK olduğunu
// doğrular.
func TestCompleteOrderIptalEdilmisSiparisteConflict(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	require.NoError(t, o.svc.CancelOrder(ctx, siparis.ID, "stok yok"))

	_, err = o.svc.CompleteOrder(ctx, siparis.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestArchiveOrderYalnizcaTamamlanmisSiparisi arşivlemenin ön koşulunu
// doğrular.
func TestArchiveOrderYalnizcaTamamlanmisSiparisi(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	_, err = o.svc.ArchiveOrder(ctx, siparis.ID)
	require.Error(t, err, "tamamlanmamış sipariş arşivlenememeli")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotCompleted, errors.CodeOf(err))

	tamamlanan, err := o.svc.CompleteOrder(ctx, siparis.ID)
	require.NoError(t, err)

	arsivlenen, err := o.svc.ArchiveOrder(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, arsivlenen.Status)
	require.NotNil(t, arsivlenen.CompletedAt)
	assert.Equal(t, *tamamlanan.CompletedAt, *arsivlenen.CompletedAt,
		"arşivleme tamamlanma damgasına dokunmamalı")
}

// TestGetOrderByDisplayIDNumarayaGoreOkur destek akışının giriş kapısını
// doğrular.
func TestGetOrderByDisplayIDNumarayaGoreOkur(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	detay, err := o.svc.GetOrderByDisplayID(ctx, siparis.DisplayID)
	require.NoError(t, err)
	assert.Equal(t, siparis.ID, detay.ID)
	require.Len(t, detay.Items, 1)

	_, err = o.svc.GetOrderByDisplayID(ctx, 0)
	require.Error(t, err, "sıfır numara geçersiz olmalı")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = o.svc.GetOrderByDisplayID(ctx, 9999)
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestListOrdersFiltreVeSayfalama listeleme ölçütlerini doğrular.
func TestListOrdersFiltreVeSayfalama(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	misafir := gecerliGirdi()
	misafir.CustomerID = ""
	_, err := o.svc.CreateOrder(ctx, misafir)
	require.NoError(t, err)

	kayitli, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)
	require.NoError(t, o.svc.CancelOrder(ctx, kayitli.ID, "test"))

	musteri := testCustomerID
	siparisler, sayi, err := o.svc.ListOrders(ctx, service.ListOrdersInput{CustomerID: &musteri})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
	require.Len(t, siparisler, 1)
	assert.Equal(t, kayitli.ID, siparisler[0].ID)

	iptal := models.OrderCanceled
	_, sayi, err = o.svc.ListOrders(ctx, service.ListOrdersInput{Status: &iptal})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)

	_, _, err = o.svc.ListOrders(ctx, service.ListOrdersInput{
		Status: func() *models.OrderStatus { s := models.OrderStatus("shipped"); return &s }(),
	})
	require.Error(t, err, "tanımsız durum reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, _, err = o.svc.ListOrders(ctx, service.ListOrdersInput{
		Page: service.Page{Limit: service.MaxLimit + 1},
	})
	require.Error(t, err, "sayfa tavanı aşılamaz")
}

// TestSetOrderSummaryTotalsIdempotentVeSinirli özet yazımının kurallarını
// doğrular.
func TestSetOrderSummaryTotalsIdempotentVeSinirli(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	ozet, err := o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), ozet.PaidTotal)
	assert.Equal(t, int64(0), ozet.Outstanding(siparis.Total))

	// Aynı değerin ikinci kez yazılması zararsızdır: mutlak yazma tekrarlanan
	// ödeme olaylarını idempotent kılar.
	tekrar, err := o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)
	assert.Equal(t, int64(6100), tekrar.PaidTotal)

	// Fazla tahsilat REDDEDİLMEZ; kalan negatif görünür.
	fazla, err := o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6500})
	require.NoError(t, err)
	assert.Equal(t, int64(-400), fazla.Outstanding(siparis.Total))

	// Tahsil edilmemiş tutar iade edilemez.
	_, err = o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 100, RefundedTotal: 200})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeSummaryInvalid, errors.CodeOf(err))
}

// TestSetOrderSummaryTotalsGecikmisBildirimIadeyiSilmez özet yazımının
// SIRADAN BAĞIMSIZ olduğunu doğrular.
//
// Ödeme olayları en az bir kez teslim edilir ve SIRA GARANTİSİ YOKTUR. Üzerine
// yazan bir uçta, gecikmiş bir tahsilat olayının yeniden işlenmesi kendisinden
// sonra kaydedilmiş bir iadeyi sıfırlardı: çağrı hatasız döner, kayıtlı para
// kaybolurdu ve order_summaries_refund_within_paid kısıtı da bir ARALIK
// kontrolü olduğu için tetiklenmezdi.
func TestSetOrderSummaryTotalsGecikmisBildirimIadeyiSilmez(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	siparis, err := o.svc.CreateOrder(ctx, gecerliGirdi())
	require.NoError(t, err)

	_, err = o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100})
	require.NoError(t, err)

	_, err = o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 1000})
	require.NoError(t, err)

	// Gecikmiş tahsilat olayı yeniden işleniyor: iadeyi bilmiyor.
	gecikmis, err := o.svc.SetOrderSummaryTotals(ctx, siparis.ID,
		service.SummaryTotalsInput{PaidTotal: 6100, RefundedTotal: 0})
	require.NoError(t, err, "gecikmiş teslim hata değil, yok sayma olmalı")
	assert.Equal(t, int64(1000), gecikmis.RefundedTotal,
		"kaydedilmiş iade gecikmiş bir bildirimle silinemez")
	assert.Equal(t, int64(6100), gecikmis.PaidTotal)

	okunan, err := o.svc.GetOrderSummary(ctx, siparis.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1000), okunan.RefundedTotal)

	// Para yazan yol da siparişin kilidini alır; modülün geri kalanıyla aynı
	// eşzamanlılık disiplinine tabidir.
	assert.Contains(t, o.store.lockedOrders, siparis.ID,
		"özet yazımı siparişin kilidini almalı")
}

// TestSetOrderSummaryTotalsOlmayanSiparisNotFound özet yazımının SİPARİŞİN
// varlığını doğruladığını gösterir.
//
// Kontrol özet satırına değil siparişe bakar: kilitsiz bir yazmada eksik bir
// sipariş "özet bulunamadı" gibi görünür ve hatanın hangi kaydı işaret ettiği
// kaybolurdu.
func TestSetOrderSummaryTotalsOlmayanSiparisNotFound(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)

	_, err := o.svc.SetOrderSummaryTotals(ctx, "order_YOK",
		service.SummaryTotalsInput{PaidTotal: 100})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "aldığı: %v", err)
	assert.Contains(t, o.store.lockedOrders, "order_YOK",
		"varlık kontrolü siparişin kilidiyle yapılmalı")
}

// TestNewEksikBagimlilikliKurulumuReddeder servisin eksik bağımlılıkla
// kurulamadığını doğrular.
//
// Özellikle olay veri yolu: opsiyonel olsaydı, kaydı unutulmuş bir kurulumda
// sipariş sessizce yazılır ama "order.placed" hiç yayımlanmazdı.
func TestNewEksikBagimlilikliKurulumuReddeder(t *testing.T) {
	testler := map[string]service.Options{
		"depo yok":      {Events: newFakeBus()},
		"veri yolu yok": {Repo: newFakeStore()},
	}

	for ad, opts := range testler {
		t.Run(ad, func(t *testing.T) {
			_, err := service.New(opts)

			require.Error(t, err)
			assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
		})
	}
}
