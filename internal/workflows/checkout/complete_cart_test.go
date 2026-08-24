package checkout

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// indexOf bir çağrının kayıttaki sırasını döner; yoksa -1.
func indexOf(calls []string, call string) int {
	return slices.Index(calls, call)
}

// TestMutluYolBesAdimiSirayaCalistirir tüm adımların çalıştığını ve sonucun
// siparişi, tahsilatı ve kesinleşen stoğu bildirdiğini doğrular.
func TestMutluYolBesAdimiSirayaCalistirir(t *testing.T) {
	h := newHarness(t)

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, testCartID, out.CartID)
	assert.Equal(t, testOrderID, out.OrderID)
	assert.Equal(t, testCollectionID, out.PaymentCollectionID)
	assert.Equal(t, testSessionID, out.PaymentSessionID)
	assert.Equal(t, testPaymentID, out.PaymentID)
	assert.Equal(t, testAmount, out.Amount)
	assert.Equal(t, testCurrency, out.CurrencyCode)
	assert.Equal(t, []string{"res_" + testLineA, "res_" + testLineB}, out.ReservationIDs)
	assert.True(t, out.CartCompleted)
	assert.True(t, out.ReservationsConfirmed)
	assert.Empty(t, out.Warnings)

	assert.Equal(t, []string{
		"totals:calculate",
		"cart:snapshot",
		"catalog:graph",
		"link:list_many:" + LinkVariantInventory,
		"inventory:reserve:" + testLineA,
		"inventory:reserve:" + testLineB,
		"order:place",
		"payment:collection",
		"payment:session",
		"payment:authorize",
		"payment:capture",
		"payment:read_collection",
		"cart:complete",
		"inventory:confirm:res_" + testLineA,
		"inventory:confirm:res_" + testLineB,
	}, h.rec.snapshot())
}

// TestSiparisGoruntusuHesapVeKatalogdanKurulur siparişe giden gövdenin
// sepetten, hesaptan ve katalogdan doğru birleştirildiğini doğrular.
//
// Şema order modülünün beklediğiyle birebir aynı olmak zorundadır ve derleyici
// bunu göremez (ADR 0006); bu yüzden alanlar tek tek sınanır.
func TestSiparisGoruntusuHesapVeKatalogdanKurulur(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)
	require.Len(t, h.orders.placed, 1)

	placed := h.orders.placed[0]
	assert.Equal(t, testCartID, placed.CartID)
	assert.Equal(t, testRegionID, placed.RegionID)
	assert.Equal(t, testCustomerID, placed.CustomerID)
	assert.Equal(t, "musteri@example.com", placed.Email)
	assert.Equal(t, testCurrency, placed.CurrencyCode)
	assert.NotEmpty(t, placed.IdempotencyKey, "sipariş idempotency anahtarı DOLDURULMALI")
	assert.Equal(t, int64(2500), placed.Subtotal)
	assert.Equal(t, int64(500), placed.TaxTotal)
	assert.Equal(t, testAmount, placed.Total)

	require.Len(t, placed.Items, 2)
	assert.Equal(t, orderSnapshotItem{
		VariantID: testVariantA, Title: testTitleA, Quantity: 2,
		UnitPrice: 1000, Subtotal: 2000, TaxTotal: 400, Total: 2400,
	}, placed.Items[0])
	assert.Equal(t, orderSnapshotItem{
		VariantID: testVariantB, Title: testTitleB, Quantity: 1,
		UnitPrice: 500, Subtotal: 500, TaxTotal: 100, Total: 600,
	}, placed.Items[1])

	// Tahsilat AÇIK tutarla yapılır; sıfır "bloke olanın tamamı" demek olurdu.
	assert.Equal(t, []int64{testAmount}, h.payments.captureAmounts)
}

// TestOdemePatlayincaSiparisVeStokGeriAlinir Faz 6'nın DoD testidir.
//
// Ödeme adımı patladığında sipariş İPTAL EDİLMELİ, stok rezervasyonu GERİ
// BIRAKILMALI ve telafiler TERS SIRADA çalışmalıdır.
func TestOdemePatlayincaSiparisVeStokGeriAlinir(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "kart reddedildi")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "ret bir çakışmadır, sunucu arızası değil")

	calls := h.rec.snapshot()

	// İLERİ yön: önce stok, sonra sipariş, sonra ödeme.
	assert.Less(t, indexOf(calls, "inventory:reserve:"+testLineA), indexOf(calls, "order:place"))
	assert.Less(t, indexOf(calls, "order:place"), indexOf(calls, "payment:authorize"))

	// Telafi GERÇEKTEN çalıştı.
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))

	// TERS sıra: sipariş iptali stok iadesinden ÖNCE gelir.
	assert.Less(t, indexOf(calls, "order:cancel"), indexOf(calls, "inventory:release:res_"+testLineA),
		"telafi ters sırada çalışmalı: create_order, reserve_inventory'den ÖNCE geri alınır")

	// Ödeme oturumu da kapatıldı; tahsilat hiç denenmedi.
	assert.Equal(t, 1, h.rec.count("payment:cancel"))
	assert.Equal(t, 0, h.rec.count("payment:capture"))
	assert.Equal(t, 0, h.rec.count("cart:complete"))
}

// TestYetersizStokSiparisOlusturmaz ilk satırda ayırma patladığında hiçbir
// yan etki uygulanmadığını doğrular: telafi edilecek bir şey yoktur.
func TestYetersizStokSiparisOlusturmaz(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(context.Context, string, string, int64, string) (string, error) {
		return "", errors.Conflict("inventory_insufficient_stock", "yetersiz stok")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.True(t, hasCode(err, CodeReservationFailed), "hata: %v", err)

	assert.Equal(t, 0, h.rec.count("order:place"))
	assert.Equal(t, 0, h.rec.count("payment:collection"))
	assert.Empty(t, h.orders.canceled)
	for _, call := range h.rec.snapshot() {
		assert.NotContains(t, call, "inventory:release", "hiç rezervasyon alınmadıysa bırakılacak da yoktur")
	}
}

// TestIkinciSatirdaYetersizStokIlkiniBirakir yarıda kalan adımın KENDİ
// temizliğini yaptığını doğrular.
//
// Motor tek denemede patlayan adımı telafi ETMEZ; temizlik borcu adımındır.
func TestIkinciSatirdaYetersizStokIlkiniBirakir(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", errors.Conflict("inventory_insufficient_stock", "yetersiz stok")
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"ilk satırın rezervasyonu adımın kendi temizliğiyle bırakılmalı")
	assert.Equal(t, 0, h.rec.count("order:place"))
}

// TestSiparisPatlayincaRezervasyonGeriBirakilir sipariş adımının hatasının
// stoğu geri bıraktığını ve ödemeye hiç geçilmediğini doğrular.
func TestSiparisPatlayincaRezervasyonGeriBirakilir(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", errors.Internal("order_store_unavailable", "sipariş yazılamadı")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
	assert.Empty(t, h.orders.canceled, "hiç sipariş açılmadıysa iptal edilecek de yoktur")
	assert.Equal(t, 0, h.rec.count("payment:collection"))
}

// TestKismiYetkilendirmeAdimiDusurur TAM ÖDEME KURALINI doğrular.
//
// Sağlayıcı istenenden AZINI bloke ettiğinde durum yine "authorized" olur;
// yalnızca duruma bakan bir saga ödenmemiş bir siparişi onaylardı.
func TestKismiYetkilendirmeAdimiDusurur(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "authorized", testAmount - 1, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.True(t, hasCode(err, CodePaymentUnderauthorized), "hata: %v", err)
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 0, h.rec.count("payment:capture"), "eksik blokajla tahsilat DENENMEZ")
	assert.Equal(t, 1, h.rec.count("payment:cancel"), "kısmi blokaj serbest bırakılmalı")
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
}

// TestTahsilatEksikKalirsaTelafiEdilmemisIsBildirilir tahsilattan SONRA
// doğrulamanın patladığı durumu sınar.
//
// Para çekilmiştir: sipariş iptal EDİLMEZ, stok geri BIRAKILMAZ ve yürütme
// "geri alındı" değil, elle müdahale isteyen bir hata ile biter.
func TestTahsilatEksikKalirsaTelafiEdilmemisIsBildirilir(t *testing.T) {
	h := newHarness(t)
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "partially_captured", testAmount, 0, testAmount - 1, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"tahsil edilmiş tutar asılı yan etkidir")

	assert.Empty(t, h.orders.canceled, "ödenmiş sipariş iptal edilmez")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA), "ödenmiş siparişin stoğu bırakılmaz")
	assert.Equal(t, 0, h.rec.count("payment:cancel"), "tahsilat blokajı zaten kapatmıştır")
}

// TestTelafiPatlarsaKalanTelafilerYineCalisir bir Compensate'in hatasının
// zinciri DURDURMADIĞINI doğrular.
func TestTelafiPatlarsaKalanTelafilerYineCalisir(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "kart reddedildi")
	}
	h.orders.cancelFn = func(context.Context, string, string) error {
		return errors.Internal("order_store_unavailable", "sipariş iptal edilemedi")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"telafi tamamlanamadıysa sınıf Internal'a yükseltilir")

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA),
		"sipariş iptali patlasa bile stok geri bırakılmalı")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
}

// TestAyniAnahtarlaIkinciCagriAdimlariTekrarCalistirmaz idempotency anahtarının
// yürütmeyi tekilleştirdiğini doğrular.
//
// Sahte sepet tamamlanmayı KAYDETMEDİĞİ için hazırlık ikinci kez de geçer;
// gerçek kurulumda sepet zaten tamamlanmış olur ve akış daha erken durur
// (bkz. [TestTamamlanmisSepetReddedilir]). Burada sınanan tek şey, adımların
// motor tarafından tekrar ÇALIŞTIRILMAMASIDIR.
func TestAyniAnahtarlaIkinciCagriAdimlariTekrarCalistirmaz(t *testing.T) {
	h := newHarness(t)

	first, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	second, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, first, second, "ikinci çağrı ilk yürütmenin ÇIKTISINI döner")

	assert.Equal(t, 1, h.rec.count("inventory:reserve:"+testLineA))
	assert.Equal(t, 1, h.rec.count("order:place"))
	assert.Equal(t, 1, h.rec.count("payment:collection"))
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Equal(t, 1, h.rec.count("cart:complete"))
}

// TestClearCartArizasiSiparisiDusurmez pivot'tan sonraki adımın hata
// DÖNDÜRMEDİĞİNİ, arızayı uyarı olarak bildirdiğini doğrular.
func TestClearCartArizasiSiparisiDusurmez(t *testing.T) {
	h := newHarness(t)
	h.carts.markCompletedFn = func(context.Context, string) error {
		return errors.Conflict("cart_totals_stale", "toplamlar güncel değil")
	}
	h.inventory.confirmFn = func(_ context.Context, reservationID string) error {
		if reservationID == "res_"+testLineB {
			return errors.Internal("inventory_unavailable", "kesinleştirilemedi")
		}
		return nil
	}

	out, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err, "ödemesi alınmış sipariş bir sepet damgası yüzünden düşmez")

	assert.Equal(t, testOrderID, out.OrderID)
	assert.False(t, out.CartCompleted)
	assert.False(t, out.ReservationsConfirmed)
	assert.Len(t, out.Warnings, 2)

	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
}

// TestTamamlanmisSepetReddedilir tamamlanmış bir sepetin hiçbir yan etki
// uygulanmadan reddedildiğini doğrular.
func TestTamamlanmisSepetReddedilir(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Revision: testRevision, Completed: true,
			Items: []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}},
		})
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestSepetHesaptanSonraDegisirseReddedilir hesabın ve anlık görüntünün AYNI
// şekle ait olması gerektiğini doğrular.
func TestSepetHesaptanSonraDegisirseReddedilir(t *testing.T) {
	h := newHarness(t)
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.Revision = testRevision - 1
		return totals, err
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCartChanged, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestOnaylananTutarDegistiyseReddedilir müşterinin onayladığı tutarla
// hesaplanan tutarın ayrışmasının sessiz kalmadığını doğrular.
func TestOnaylananTutarDegistiyseReddedilir(t *testing.T) {
	h := newHarness(t)

	in := h.input()
	in.ExpectedTotal = testAmount - 100

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, CodeTotalMismatch, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestStokKalemsizVaryantReddedilir stok kalemine bağlı olmayan bir varyantın
// SESSİZCE ATLANMADIĞINI doğrular.
func TestStokKalemsizVaryantReddedilir(t *testing.T) {
	h := newHarness(t)
	h.links.listManyFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{testVariantA: {testItemA}}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantNotStocked, errors.CodeOf(err))
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA))
}

// TestBirdenCokStokKalemiReddedilir tekil olması gereken bağın çoğullanmasının
// sıralama tesadüfüne bırakılmadığını doğrular.
func TestBirdenCokStokKalemiReddedilir(t *testing.T) {
	h := newHarness(t)
	h.links.listManyFn = func(context.Context, string, []string) (map[string][]string, error) {
		return map[string][]string{
			testVariantA: {testItemA, "inv_x"},
			testVariantB: {testItemB},
		}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantInventoryAmbiguous, errors.CodeOf(err))
}

// TestKatalogdaOlmayanVaryantReddedilir başlıksız bir sipariş satırının
// yazılmadığını doğrular.
func TestKatalogdaOlmayanVaryantReddedilir(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(context.Context, query.GraphSpec) ([]query.Record, error) {
		return []query.Record{{query.IDField: testVariantA, FieldTitle: testTitleA}}, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err))
}

// TestKatalogArizasiVaryantYokSayilmaz altyapı hatasının iş durumu gibi
// raporlanmadığını doğrular.
func TestKatalogArizasiVaryantYokSayilmaz(t *testing.T) {
	h := newHarness(t)
	h.catalog.graphFn = func(context.Context, query.GraphSpec) ([]query.Record, error) {
		return nil, errors.Unavailable("query_unavailable", "okuma katmanı erişilemiyor")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeCatalogReadFailed, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "geçici arıza kalıcı sayılmaz")
}

// TestBozukHesapYanEtkiOncesindeYakalanir hesabın aritmetiğinin sepet
// modülünden geldiği için sorgusuz kabul EDİLMEDİĞİNİ doğrular.
func TestBozukHesapYanEtkiOncesindeYakalanir(t *testing.T) {
	tests := map[string]func(*cartwf.Totals){
		"satır ara toplamı birim fiyat × adet değil": func(totals *cartwf.Totals) {
			totals.Lines[0].Subtotal = 1999
		},
		"satır toplamı kimliği bozuk": func(totals *cartwf.Totals) {
			totals.Lines[0].Total = 2399
		},
		"sepetin ara toplamı satırların toplamı değil": func(totals *cartwf.Totals) {
			totals.Subtotal = 2400
		},
		"sepetin toplam kimliği bozuk": func(totals *cartwf.Totals) {
			totals.Total = 2999
		},
		// Aşağıdaki üç satır sepet DÜZEYİNDEKİ indirim ve verginin aralık
		// denetimine girmediği boşluğu kapatır. Üçünde de toplam kimliği
		// SAĞLANIR — hesap kendi içinde tutarlıdır — ama sayılar anlamsızdır.
		"sepet indirimi negatif": func(totals *cartwf.Totals) {
			// Negatif indirim tahsil edilecek tutarı ŞİŞİRİR: müşteriden
			// satırların toplamından fazlası çekilirdi.
			totals.DiscountTotal = -500
			totals.Total = 3500
		},
		"sepet vergisi negatif": func(totals *cartwf.Totals) {
			// Negatif vergi tersini yapar: müşteriden satırların toplamının
			// altında bir tutar çekilirdi.
			totals.TaxTotal = -2000
			totals.Total = 500
		},
		"sepet vergisi ve indirimi int64'ü taşırıyor": func(totals *cartwf.Totals) {
			// İki uç değer birbirini götürür ve kimlik ham int64 aritmetiğinde
			// "sağlanır"; denetimsiz bırakılırsa sipariş MaxInt64 vergiyle
			// açılırdı.
			totals.TaxTotal = math.MaxInt64
			totals.DiscountTotal = math.MaxInt64 - 500
			totals.Total = testAmount
		},
	}

	for name, bozguncu := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
				totals, err := defaultTotals(ctx, cartID)
				bozguncu(&totals)
				return totals, err
			}

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.Error(t, err)
			assert.Equal(t, CodeAmountInvalid, errors.CodeOf(err))
			assert.Equal(t, 0, h.rec.count("inventory:reserve:"+testLineA),
				"bozuk hesabın bedeli ayrılıp geri bırakılan stok olmamalı")
		})
	}
}

// TestEksikHesapSatiriReddedilir hesabın sepetin TÜM satırlarını kapsamasının
// zorunlu olduğunu doğrular.
func TestEksikHesapSatiriReddedilir(t *testing.T) {
	h := newHarness(t)
	h.totals.calculateFn = func(ctx context.Context, cartID string) (cartwf.Totals, error) {
		totals, err := defaultTotals(ctx, cartID)
		totals.Lines = totals.Lines[:1]
		totals.Subtotal = 2000
		totals.TaxTotal = 400
		totals.Total = 2400
		return totals, err
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)
	assert.Equal(t, CodeTotalsInvalid, errors.CodeOf(err))
}

// TestGirdiDogrulamasi zorunlu alanların hiçbir yan etki uygulanmadan
// denetlendiğini doğrular.
func TestGirdiDogrulamasi(t *testing.T) {
	tests := map[string]func(*CompleteCartInput){
		"cart_id boş":             func(in *CompleteCartInput) { in.CartID = "" },
		"location_id boş":         func(in *CompleteCartInput) { in.LocationID = "" },
		"payment_provider_id boş": func(in *CompleteCartInput) { in.PaymentProviderID = "" },
		"cart_id boşluklu":        func(in *CompleteCartInput) { in.CartID = " cart_1" },
		"expected_total negatif":  func(in *CompleteCartInput) { in.ExpectedTotal = -1 },
	}

	for name, boz := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			in := h.input()
			boz(&in)

			_, err := h.wf.CompleteCart(context.Background(), in)
			require.Error(t, err)
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
			assert.Empty(t, h.rec.snapshot(), "geçersiz girdi hiçbir modüle dokunmaz")
		})
	}
}

// TestUzunSepetKimligiReddedilir idempotency anahtarının bütçesinin girdi
// doğrulamasında uygulandığını doğrular.
func TestUzunSepetKimligiReddedilir(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.CartID = strings.Repeat("c", MaxCartIDLen+1)

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "cart_id")
}

// TestOdemeVerisiSaglayiciyaGecer sağlayıcıya iletilecek serbest verinin
// oturuma taşındığını doğrular.
//
// Entegrasyon testinin ödeme adımını patlatabilmesi buna bağlıdır: manuel
// sağlayıcının davranışı oturum verisinden okunur.
func TestOdemeVerisiSaglayiciyaGecer(t *testing.T) {
	h := newHarness(t)
	in := h.input()
	in.PaymentData = json.RawMessage(`{"manual_outcome":"authorize"}`)

	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, []string{`{"manual_outcome":"authorize"}`}, h.payments.sessionData)
}

// TestOdemeVerisiYurutmeKaydinaYazilmaz hassas verinin kalıcı deftere
// düşmediğini doğrular (plan Bölüm 8).
func TestOdemeVerisiYurutmeKaydinaYazilmaz(t *testing.T) {
	plan := &checkoutPlan{
		CartID:      testCartID,
		PaymentData: json.RawMessage(`{"card_token":"tok_gizli"}`),
	}

	payload, err := json.Marshal(plan)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "tok_gizli")
	assert.NotContains(t, string(payload), "card_token")
}

// TestTahsilatDogrulamasiYerelTutaraDemirli tahsilat sonrası doğrulamanın
// ödeme modülünün KENDİ bildirdiği tutara değil, saga'nın YEREL olarak bildiği
// tutara demirlendiğini kanıtlar.
//
// Regresyon: doğrulama "captured < amount" idi ve her iki değer de aynı
// Collection çağrısından geliyordu. Soru böylece "koleksiyon kendi içinde
// tutarlı mı"ya iniyordu; koleksiyon "0 toplanacaktı, 0 toplandı" dediğinde
// 3000 birimlik sipariş SIFIR tahsilatla başarılı yazılıyordu. Yetkilendirme
// kuralı (authorized < plan.Amount) zaten yerel tutara demirliydi; bu onun
// ikizidir.
func TestTahsilatDogrulamasiYerelTutaraDemirli(t *testing.T) {
	h := newHarness(t)
	// Koleksiyon kendi içinde TUTARLI ama saga'nın beklediği tutarla ilgisiz.
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", 0, 0, 0, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err,
		"koleksiyon sıfır tahsilat bildirirken akış BAŞARILI sayılamaz")
	assert.True(t, hasCode(err, CodePaymentUndercaptured),
		"hata eksik tahsilatı bildirmeli: %v", err)

	// Tahsilat yapılmıştı: bu bir ASILI yan etkidir, telafi ile kapanmaz.
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Equal(t, 0, h.rec.count("cart:complete"),
		"doğrulama düşerken sepet tamamlanmış işaretlenmemeli")
}

// TestTahsilatPatlayincaTamGeriAlmaTersSiradaCalisir tahsilat adımının
// KENDİSİ patladığında tüm telafi zincirinin ters sırada çalıştığını doğrular.
//
// Kapsam boşluğuydu: hiçbir test tahsilatı patlatmıyordu, dolayısıyla
// authorizePaymentStep.Compensate hiç çalışmıyordu. Testlerin saydığı
// "payment:cancel" izi Invoke içindeki blokaj bırakmadan geliyordu — yani
// müşterinin kartındaki blokajı serbest bırakan TELAFİ yolu sıfır kapsamdaydı.
//
// Geri almanın ÖNKOŞULU koleksiyonun hiçbir tahsilat bildirmemesidir: tahsilat
// çağrısının hata dönmesi tek başına "para gitmedi" demek değildir ve saga
// artık kanıt olmadan geri almaz (bkz.
// [TestBelirsizTahsilatOdenmisSiparisiGeriAlmaz]). Sahne bu yüzden açıkça
// kurulur — sağlayıcıya hiç ulaşılamamıştır, koleksiyonda hareket yoktur.
func TestTahsilatPatlayincaTamGeriAlmaTersSiradaCalisir(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_down", "sağlayıcı erişilemez")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "authorized", testAmount, testAmount, 0, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	calls := h.rec.snapshot()

	// Geri alma KANITA dayanır: koleksiyon okunmadan zincir yürümez.
	assert.Equal(t, 1, h.rec.count("payment:read_collection"),
		"tahsilat hatasından sonra koleksiyon SORULMALI")

	// Yetkilendirme BAŞARILI olmuştu; blokaj telafi ile serbest bırakılmalı.
	assert.Equal(t, 1, h.rec.count("payment:authorize"))
	assert.Equal(t, 1, h.rec.count("payment:cancel"),
		"yetkilendirilmiş blokaj serbest bırakılmalı; aksi hâlde müşterinin kartında asılı kalır")

	// Sipariş ve stok da geri alındı.
	assert.Equal(t, []string{testOrderID}, h.orders.canceled)
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))

	// TERS sıra: ödeme -> sipariş -> stok.
	assert.Less(t, indexOf(calls, "payment:cancel"), indexOf(calls, "order:cancel"),
		"telafi ters sırada: authorize_payment, create_order'dan ÖNCE geri alınır")
	assert.Less(t, indexOf(calls, "order:cancel"), indexOf(calls, "inventory:release:res_"+testLineA),
		"telafi ters sırada: create_order, reserve_inventory'den ÖNCE geri alınır")

	assert.Equal(t, 0, h.rec.count("cart:complete"))
}

// TestBelirsizTahsilatOdenmisSiparisiGeriAlmaz saga'nın en pahalı arızasını
// kilitler: sağlayıcı parayı çeker, yanıt kaybolur ve Capture hata döner.
//
// Regresyon: pivot koruması TAHSİLAT KİMLİĞİNE bağlıydı ve bu yolda kimlik hiç
// yazılmadığı için koruma kapanıyordu. Ölçülen sonuç, paket yorumunun "asla
// olmamalı" dediği durumdu — çağrı izi
// "payment:capture -> payment:cancel -> order:cancel -> inventory:release x2",
// yani müşteri hem parasından hem siparişinden oluyor, malı da serbest
// bırakılıyordu. Artık hata yolu SORUŞTURULUYOR: koleksiyon tahsilat
// gördüğünde saga ileri tarafta kalır ve elle mutabakat istenir.
func TestBelirsizTahsilatOdenmisSiparisiGeriAlmaz(t *testing.T) {
	h := newHarness(t)
	// Sağlayıcı parayı ÇEKTİ, sonra yanıt zaman aşımına uğradı.
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_timeout", "sağlayıcı yanıtı zaman aşımına uğradı")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", testAmount, testAmount, testAmount, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"sonucu bilinmeyen tahsilat asılı yan etkidir")
	assert.True(t, hasCode(err, CodeCaptureAmbiguous), "hata: %v", err)

	// Soruşturma GERÇEKTEN yapıldı.
	assert.Equal(t, 1, h.rec.count("payment:read_collection"),
		"tahsilat hatasından sonra koleksiyon SORULMALI")

	// Ödenmiş sipariş ayakta kalır: hiçbir telafi çalışmaz.
	assert.Empty(t, h.orders.canceled, "ödenmiş sipariş iptal edilmez")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"ödenmiş siparişin stoğu bırakılmaz; aksi hâlde aynı mal ikinci kez satılır")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestKoleksiyonOkunamayincaGeriAlmaYapilmaz kanıt bulunamadığında saga'nın
// geri ALMADIĞINI doğrular.
//
// Belirsizliğin en tipik sebebi ödeme sağlayıcısına ulaşılamamasıdır; o hâlde
// koleksiyon okuması da patlar. Kanıtsız geri alma, ödenmiş bir siparişi yok
// etme riskidir ve bu akış şüphe hâlinde UCUZ olan hatayı seçer: bekleyen bir
// sipariş ve ayrılmış stok görünürdür, iade edilmemiş bir tahsilat değildir.
func TestKoleksiyonOkunamayincaGeriAlmaYapilmaz(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", errors.Unavailable("psp_timeout", "sağlayıcı yanıtı zaman aşımına uğradı")
	}
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "", 0, 0, 0, 0, errors.Unavailable("psp_down", "koleksiyon okunamadı")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, errors.Is(err, workflow.ErrUncompensated))
	assert.True(t, hasCode(err, CodeCaptureAmbiguous), "hata: %v", err)

	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestBelirsizTahsilatinTelafisiGeriAlindiDemez sonucu bilinmeyen bir
// tahsilatın telafisinin nil DÖNMEDİĞİNİ doğrular.
//
// Telafi doğrudan çağrılır: motor tek denemede patlayan adımı telafi etmez,
// dolayısıyla bu yol akış üzerinden erişilemez. Yine de sözleşmenin kendisi
// sınanır — nil dönmek, yürütmeyi "iş yapıldı ve GERİ ALINDI" diye kaydeden
// bir yalan olurdu ve adımın yeniden denenebilir hâle getirildiği gün o yalan
// sessizce üretime çıkardı.
func TestBelirsizTahsilatinTelafisiGeriAlindiDemez(t *testing.T) {
	h := newHarness(t)
	step := &capturePaymentStep{w: h.wf, plan: &checkoutPlan{
		CartID: testCartID, Amount: testAmount, CurrencyCode: testCurrency,
	}}

	t.Run("tahsilat denenmedi: no-op", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{}}
		require.NoError(t, step.Compensate(context.Background(), sc))
	})

	t.Run("tahsilat denendi, sonucu bilinmiyor", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{sharedCaptureAttempted: true}}
		err := step.Compensate(context.Background(), sc)
		require.Error(t, err)
		assert.True(t, hasCode(err, CodeCaptureAmbiguous), "hata: %v", err)
		assert.True(t, errors.IsConflict(err), "kalıcı durum yeniden denenmez")
	})

	t.Run("tahsilat yapıldı", func(t *testing.T) {
		sc := &workflow.StepContext{Shared: map[string]any{
			sharedCaptureAttempted: true,
			sharedPaymentID:        testPaymentID,
		}}
		err := step.Compensate(context.Background(), sc)
		require.Error(t, err)
		assert.True(t, hasCode(err, CodeCaptureIrreversible), "hata: %v", err)
	})
}

// TestTahsilattanSonraPatlayanAdimPivotuKorur pivot kararını kilitler:
// tahsilat BAŞARILI olduktan sonra bir adım düşerse hiçbir şey geri alınmaz.
//
// Kapsam boşluğuydu: capturePaymentStep.Compensate'in gövdesi "return nil" ile
// değiştirildiğinde tek bir test bile düşmüyordu, çünkü hiçbir test BAŞARILI
// bir tahsilattan SONRA bir adımı patlatmıyordu. Sahne, sepet modülünün
// paniklemesiyle kurulur — motor paniği adım hatasına çevirir ve telafi zinciri
// başlar; asıl sınanan, o zincirin pivot'ta DURMASIDIR.
func TestTahsilattanSonraPatlayanAdimPivotuKorur(t *testing.T) {
	h := newHarness(t)
	h.carts.markCompletedFn = func(context.Context, string) error {
		panic("cart modülü çöktü")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err),
		"tahsilatın telafisi hata döndüğü için yürütme compensation_failed olmalı")
	assert.True(t, hasCode(err, CodeCaptureIrreversible),
		"telafi, çekilmiş paranın geri alınamadığını BİLDİRMELİ: %v", err)

	// Tahsilat gerçekleşmişti: ödenmiş sipariş ayakta kalır.
	assert.Equal(t, 1, h.rec.count("payment:capture"))
	assert.Empty(t, h.orders.canceled, "ödenmiş sipariş iptal edilmez")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"ödenmiş siparişin stoğu bırakılmaz")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"tahsilat blokajı zaten kapatmıştır")
}

// TestTahsilattanSonraGelenIptalSepetiKilitlemez saga'nın çağıranın
// iptalinden AYRILDIĞINI doğrular.
//
// Regresyon: saga çağıranın bağlamıyla koşuyordu ve motor her adımdan ÖNCE
// bağlamı denetlediği için, tahsilat sırasında düşen bir istemci clear_cart'ı
// tümüyle atlatıyordu. Ölçülen sonuç: cart:complete=0, inventory:confirm=0,
// yürütme compensation_failed — yani para çekilmiş, sipariş "pending", sepet
// kilitli ve stok "active" kalıyordu; idempotency anahtarı da yandığı için
// aynı sepet bir daha hiç denenemiyordu.
func TestTahsilattanSonraGelenIptalSepetiKilitlemez(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// İstemci tam tahsilat sırasında düşüyor.
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		cancel()
		return testPaymentID, nil
	}

	out, err := h.wf.CompleteCart(ctx, h.input())
	require.NoError(t, err,
		"tahsilat yapılmışken akış istemcinin gitmesi yüzünden yarıda kalamaz")

	assert.True(t, out.CartCompleted, "sepet damgalanmalı; aksi hâlde kalıcı olarak kilitlenir")
	assert.True(t, out.ReservationsConfirmed)
	assert.Empty(t, out.Warnings)

	assert.Equal(t, 1, h.rec.count("cart:complete"))
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineA))
	assert.Equal(t, 1, h.rec.count("inventory:confirm:res_"+testLineB))
	assert.Empty(t, h.orders.canceled)
}

// TestSagaBaglamiCagirandanAyrilir [sagaContext]'in iptali TAŞIMADIĞINI ve
// kendi bütçesini kurduğunu doğrular.
func TestSagaBaglamiCagirandanAyrilir(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sctx, stop := sagaContext(ctx)
	defer stop()

	require.NoError(t, sctx.Err(), "çağıranın iptali saga'yı ölü doğurmamalı")

	deadline, ok := sctx.Deadline()
	require.True(t, ok, "ayrılmış bağlamın kendi süre bütçesi OLMALI")
	assert.WithinDuration(t, time.Now().Add(SagaTimeout), deadline, time.Minute)
}

// TestTemizlikBaglamiIptaldenEtkilenmez [cleanupContext]'in iptal edilmiş bir
// bağlamdan bile canlı bir bağlam ürettiğini doğrular.
//
// Temizliğin en çok gerektiği anlardan biri tam da bağlamın ölmesidir: yarıda
// kalan bir ayırmayı ölü bir bağlamla geri bırakmaya çalışmak, her denemenin
// anında düşmesi demektir.
func TestTemizlikBaglamiIptaldenEtkilenmez(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cctx, stop := cleanupContext(ctx)
	defer stop()

	require.NoError(t, cctx.Err(), "temizlik bağlamı ölü doğmamalı")

	deadline, ok := cctx.Deadline()
	require.True(t, ok, "temizliğin kendi süre bütçesi OLMALI")
	assert.WithinDuration(t, time.Now().Add(CompensationTimeout), deadline, time.Minute)
}

// TestBosRezervasyonKimligiBasariSayilmaz stok modülünün hata dönmeden BOŞ
// kimlik döndürmesinin başarı SAYILMADIĞINI doğrular.
//
// Boş kimlik "ayırma yapılmadı" demek değildir; "yapıldı ama izi yok" demektir.
// Sessizce kabul edilirse ne bu adım ne telafi onu geri bırakabilir.
func TestBosRezervasyonKimligiBasariSayilmaz(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", nil
		}
		return "res_" + lineItemID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "hata: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"izi olmayan rezervasyon asılı yan etkidir")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	// İzi OLAN rezervasyon yine de geri bırakılır.
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("order:place"), "kimliksiz ayırmayla sipariş açılmaz")
}

// TestBosSiparisKimligiBasariSayilmaz sipariş modülünün hata dönmeden BOŞ
// kimlik döndürmesinin YETİM sipariş üretmediğini doğrular.
//
// Regresyon: boş kimlik paylaşılan haritaya yazılıyor, telafi "sipariş hiç
// açılmadı" sanıp no-op yapıyordu; ölçülen sonuç order:place=1 iken
// order:cancel=0 idi — order modülünde açık bir sipariş kalıyordu.
func TestBosSiparisKimligiBasariSayilmaz(t *testing.T) {
	h := newHarness(t)
	h.orders.placeFn = func(context.Context, json.RawMessage) (string, error) {
		return "", nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "hata: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated),
		"izi olmayan sipariş asılı yan etkidir; yürütme 'geri alındı' diyemez")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	assert.Equal(t, 0, h.rec.count("payment:collection"), "kimliksiz siparişle ödeme açılmaz")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA), "stok yine de geri bırakılır")
	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
}

// TestBosTahsilatKimligiPivotuKapatmaz ödeme modülünün hata dönmeden BOŞ
// tahsilat kimliği döndürmesinin pivot korumasını DÜŞÜRMEDİĞİNİ doğrular.
//
// Regresyon: koruma bir dizgenin boşluğuna bağlıydı. Boş kimlikte
// skipAfterCapture false dönüyor ve tahsilat yapılmışken order:cancel ile iki
// inventory:release çalışıyordu.
func TestBosTahsilatKimligiPivotuKapatmaz(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		return "", nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.True(t, hasCode(err, CodeEmptyIdentifier), "hata: %v", err)
	assert.True(t, errors.Is(err, workflow.ErrUncompensated))
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	assert.Empty(t, h.orders.canceled, "para çekilmişken sipariş iptal edilmez")
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA))
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineB))
	assert.Equal(t, 0, h.rec.count("payment:cancel"))
}

// TestBosOdemeKimlikleriYetkilendirmedeDurdurulur boş koleksiyon ve oturum
// kimliklerinin EN UCUZ noktada reddedildiğini doğrular.
//
// Bu noktada müşterinin kartında bloke bir tutar yoktur; tek bedel geri alınan
// bir rezervasyondur.
func TestBosOdemeKimlikleriYetkilendirmedeDurdurulur(t *testing.T) {
	tests := map[string]func(*harness){
		"koleksiyon kimliği boş": func(h *harness) {
			h.payments.createCollectionFn = func(context.Context, string, string, int64) (string, error) {
				return "", nil
			}
		},
		"oturum kimliği boş": func(h *harness) {
			h.payments.openSessionFn = func(context.Context, string, string, string, json.RawMessage) (string, error) {
				return "", nil
			}
		},
	}

	for name, boz := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			boz(h)

			_, err := h.wf.CompleteCart(context.Background(), h.input())
			require.Error(t, err)

			assert.True(t, hasCode(err, CodeEmptyIdentifier), "hata: %v", err)
			assert.False(t, errors.Is(err, workflow.ErrUncompensated),
				"yetkilendirme öncesinde asılı kalan bir yan etki yoktur")

			assert.Equal(t, 0, h.rec.count("payment:authorize"))
			assert.Equal(t, 0, h.rec.count("payment:capture"))
			assert.Equal(t, []string{testOrderID}, h.orders.canceled)
			assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineA))
			assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB))
		})
	}
}

// TestStokTelafisiPatlarsaSizanRezervasyonBildirilir stok telafisinin hata
// BİLDİRME yolunu ve releaseAll'un "zincir ilk hatada DURMAZ" sözünü birlikte
// kilitler.
//
// İki kapsam boşluğuydu: (1) Compensate'in hatayı yutup nil dönmesi tek bir
// testi bile düşürmüyordu — oysa nil dönmek, motorun yürütmeyi "iş yapıldı ve
// GERİ ALINDI" yazması demektir ve gerçekte ayrılmış stok asılı kalır;
// (2) releaseAll'daki "continue" yerine "break" konduğunda ikinci
// rezervasyonun sessizce asılı kalması fark edilmiyordu.
//
// Kalan listenin BUDANDIĞI da burada görünür: telafi yeniden denendiğinde
// yalnızca bırakılamayan rezervasyon denenir (res_li_a üç kez, res_li_b bir
// kez).
func TestStokTelafisiPatlarsaSizanRezervasyonBildirilir(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "kart reddedildi")
	}
	h.inventory.releaseFn = func(_ context.Context, reservationID string) error {
		if reservationID == "res_"+testLineA {
			return errors.Internal("inventory_unavailable", "rezervasyon bırakılamadı")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))
	assert.True(t, hasCode(err, CodeReservationLeaked),
		"asılı kalan rezervasyon BİLDİRİLMELİ: %v", err)

	assert.Equal(t, 1, h.rec.count("inventory:release:res_"+testLineB),
		"bir rezervasyonun bırakılamaması diğerinin asılı kalması için gerekçe değildir")
	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("inventory:release:res_"+testLineA),
		"telafi yeniden denenir ve her denemede YALNIZCA kalan rezervasyon denenir")
}

// TestYaridaKalanTemizlikTelafiPolitikasiylaYenidenDenenir adımın KENDİ
// temizliğinin motorun telafisiyle aynı ısrarı gösterdiğini doğrular.
//
// Geçici bir arıza, yalnızca hangi yolda yakalandığına göre elle müdahale
// üretmemelidir: adım içi temizlik tek denemeye sahipken motorun telafisi üç
// kez deneniyordu.
func TestYaridaKalanTemizlikTelafiPolitikasiylaYenidenDenenir(t *testing.T) {
	h := newHarness(t)
	h.inventory.reserveFn = func(_ context.Context, _, _ string, _ int64, lineItemID string) (string, error) {
		if lineItemID == testLineB {
			return "", errors.Conflict("inventory_insufficient_stock", "yetersiz stok")
		}
		return "res_" + lineItemID, nil
	}

	var releases int
	h.inventory.releaseFn = func(context.Context, string) error {
		releases++
		if releases < compensationRetry().MaxAttempts {
			return errors.Unavailable("inventory_unavailable", "stok servisi geçici olarak erişilemez")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("inventory:release:res_"+testLineA),
		"geçici arızada temizlik ısrar etmeli")
	assert.False(t, errors.Is(err, workflow.ErrUncompensated),
		"temizlik sonunda başarılı olduysa asılı iş YOKTUR")
	assert.True(t, hasCode(err, CodeReservationFailed), "hata: %v", err)
}

// TestTamamlanmisSepetIkinciCagridaHazirliktaDurur idempotency godoc'unun
// GERÇEK kurulumdaki karşılığını kilitler.
//
// Motorun "tamamlanmış yürütmenin çıktısını dön" yolu bu akışta erişilemezdir:
// hazırlık motorun denetiminden ÖNCE çalışır ve başarılı bir yürütme sepeti
// tamamlanmış damgalar. İkinci çağrının cevabı bu yüzden "aynı sonuç" değil,
// CodeCartCompleted'dır — ve önemli olan, ikinci bir siparişin DOĞMAMASIDIR.
func TestTamamlanmisSepetIkinciCagridaHazirliktaDurur(t *testing.T) {
	h := newHarness(t)

	var completed bool
	h.carts.markCompletedFn = func(context.Context, string) error {
		completed = true
		return nil
	}
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		if !completed {
			return defaultSnapshot(ctx, cartID)
		}
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CustomerID: testCustomerID,
			CurrencyCode: testCurrency, Revision: testRevision, Completed: true,
			Items: []SnapshotItem{
				{ID: testLineA, VariantID: testVariantA, Quantity: 2},
				{ID: testLineB, VariantID: testVariantB, Quantity: 1},
			},
		})
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	_, err = h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err, "tamamlanmış sepet ikinci kez sipariş edilemez")
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.True(t, errors.IsConflict(err))

	assert.Equal(t, 1, h.rec.count("order:place"), "aynı sepetten ikinci sipariş DOĞMAZ")
	assert.Equal(t, 1, h.rec.count("payment:capture"))
}

// TestBlokajBirakmasiTelafiPolitikasiylaYenidenDenenir yarıda kalan
// yetkilendirmenin blokajını bırakmanın da motorun telafisiyle aynı ısrarı
// gösterdiğini doğrular.
//
// Müşterinin kartındaki blokajı geçici bir arıza yüzünden asılı bırakmak,
// aynı arıza telafi zincirinde yakalansaydı olmayacak bir sonuçtur.
func TestBlokajBirakmasiTelafiPolitikasiylaYenidenDenenir(t *testing.T) {
	h := newHarness(t)
	h.payments.authorizeFn = func(context.Context, string) (string, int64, error) {
		return "", 0, errors.Conflict("payment_authorization_declined", "kart reddedildi")
	}

	var cancels int
	h.payments.cancelFn = func(context.Context, string) error {
		cancels++
		if cancels < compensationRetry().MaxAttempts {
			return errors.Unavailable("psp_down", "sağlayıcı geçici olarak erişilemez")
		}
		return nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	assert.Equal(t, compensationRetry().MaxAttempts, h.rec.count("payment:cancel"),
		"geçici arızada blokajın serbest bırakılmasında ısrar edilmeli")
	assert.False(t, errors.Is(err, workflow.ErrUncompensated),
		"blokaj sonunda bırakıldıysa asılı iş YOKTUR")
	assert.True(t, errors.IsConflict(err), "temizlik başarılıysa hata KART REDDİ olarak kalır")
}

// TestTahsilatPanigindePivotKorumasiCalisir tahsilat işaretinin Capture
// çağrısından ÖNCE konmasını kilitler.
//
// Regresyon: işaret çağrıdan SONRA konsaydı, çağrı sırasında oluşan bir panik
// (sağlayıcı adaptörünün hatası) pivot korumasını hiç devreye sokmadan telafi
// zincirini çalıştırırdı — ÖDENMİŞ OLABİLECEK bir sipariş iptal edilir, stok
// bırakılırdı. Panik motor tarafından adım hatasına çevrildiği için bu senaryo
// gerçekten erişilebilirdir.
//
// İşaret çağrıdan önce konduğunda, o noktadan sonraki HER arıza (hata, panik,
// süre aşımı) "para gitmiş olabilir" sayılır ve geri alma YAPILMAZ.
func TestTahsilatPanigindePivotKorumasiCalisir(t *testing.T) {
	h := newHarness(t)
	h.payments.captureFn = func(context.Context, string, int64) (string, error) {
		panic("sağlayıcı adaptörü çöktü")
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err)

	// Pivot korunmalı: para gitmiş OLABİLİR, geri alma yapılamaz.
	assert.Equal(t, 0, h.rec.count("order:cancel"),
		"tahsilat denenmişken sipariş iptal edilemez; müşteri hem parasından hem siparişinden olurdu")
	assert.Empty(t, h.orders.canceled)
	assert.Equal(t, 0, h.rec.count("inventory:release:res_"+testLineA),
		"tahsilat denenmişken stok bırakılamaz; ayakta duran siparişin malı ikinci kez satılırdı")
	assert.Equal(t, 0, h.rec.count("payment:cancel"),
		"blokaj zaten tahsilatla kapanmış olabilir")
}

// TestKoleksiyonTutariPlanlaAyrisirsaAdimDuser ödeme koleksiyonunun saga'nın
// açtığından FARKLI bir tutarla açılmış olmasının yakalandığını doğrular.
//
// Tahsilat doğrulaması yerel tutara (plan.Amount) demirlidir; koleksiyonun
// kendi tutarı ise ayrı bir tutarlılık iddiasıdır. Ayrışma, ödeme koleksiyonunun
// beklenenden başka bir tutarla açıldığı anlamına gelir ve sessizce geçilmemeli.
func TestKoleksiyonTutariPlanlaAyrisirsaAdimDuser(t *testing.T) {
	h := newHarness(t)
	// Tahsilat planı KARŞILIYOR ama koleksiyonun tutarı bambaşka.
	h.payments.collectionFn = func(context.Context, string) (string, int64, int64, int64, int64, error) {
		return "captured", testAmount * 2, testAmount, testAmount, 0, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.Error(t, err, "koleksiyon tutarı planla ayrışmışsa adım düşmeli")
	assert.True(t, hasCode(err, CodePaymentUndercaptured), "hata: %v", err)
	assert.Equal(t, 0, h.rec.count("cart:complete"))
}
