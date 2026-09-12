package service_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Testlerde kullanılan sabitler.
const (
	saglayiciID = "fake"
	referans    = "cart_TEST"
	paraKodu    = "TRY"
	tutar       = int64(10_000)
)

// yeniServis sahte depo ve sahte sağlayıcı üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) (*service.Service, *fakeStore, *fakeProvider) {
	t.Helper()

	store := newFakeStore()
	prov := newFakeProvider(saglayiciID)
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: newFakeBus(),
	})
	require.NoError(t, err)

	return svc, store, prov
}

// yeniServisOtobusle yeniServis ile aynı servisi kurar ve otobüsü de döner.
//
// Ayrı bir yardımcı olmasının sebebi çağıranların çoğu: testlerin neredeyse
// tamamı otobüsle ilgilenmez ve dördüncü bir dönüş değeri hepsine "_" yazdırırdı.
// Yayımlanan olaya bakan testler bunu çağırır.
func yeniServisOtobusle(t *testing.T) (*service.Service, *fakeStore, *fakeBus) {
	t.Helper()

	store := newFakeStore()
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider(saglayiciID)))

	bus := newFakeBus()
	svc, err := service.New(service.Options{
		Store: store, Providers: registry, Events: bus,
	})
	require.NoError(t, err)

	return svc, store, bus
}

// fakeBus olayları toplar ve hiçbirini yaymaz.
type fakeBus struct {
	mu        sync.Mutex
	published []eventbus.Event
}

// newFakeBus boş bir otobüs verir.
func newFakeBus() *fakeBus { return &fakeBus{} }

// Publish olayı kaydeder.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, e)

	return nil
}

// events yayımlanmış olayların kopyasını verir.
func (b *fakeBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	return slices.Clone(b.published)
}

// koleksiyonAc test için bir ödeme koleksiyonu açar.
func koleksiyonAc(t *testing.T, svc *service.Service, amount int64) models.PaymentCollection {
	t.Helper()

	col, err := svc.CreatePaymentCollection(context.Background(), service.CreateCollectionInput{
		Reference:    referans,
		Amount:       amount,
		CurrencyCode: paraKodu,
	})
	require.NoError(t, err)
	return col
}

// oturumAc test için bir ödeme oturumu açar.
func oturumAc(t *testing.T, svc *service.Service, collectionID, key string) models.PaymentSession {
	t.Helper()

	ses, err := svc.CreateSession(context.Background(), collectionID, saglayiciID,
		service.CreateSessionInput{IdempotencyKey: key})
	require.NoError(t, err)
	return ses
}

// TestServisEksikBagimlilikIleKurulamaz kurulum hatasının AÇIKÇA döndüğünü
// doğrular.
//
// nil bir depoyla kurulmuş servis ilk istekte panik üretirdi ve hata,
// kurulumdan çok sonra ortaya çıkardı.
func TestServisEksikBagimlilikIleKurulamaz(t *testing.T) {
	_, err := service.New(service.Options{Providers: service.NewProviderRegistry()})
	require.Error(t, err)

	_, err = service.New(service.Options{Store: newFakeStore()})
	require.Error(t, err)
}

// TestCreatePaymentCollectionNotPaidDogar yeni koleksiyonun durumunu ve
// alanlarını doğrular.
func TestCreatePaymentCollectionNotPaidDogar(t *testing.T) {
	svc, _, _ := yeniServis(t)

	col, err := svc.CreatePaymentCollection(context.Background(), service.CreateCollectionInput{
		Reference:    "  " + referans + "  ",
		Amount:       tutar,
		CurrencyCode: "try",
		Metadata:     map[string]any{"kaynak": "test"},
	})

	require.NoError(t, err)
	assert.Equal(t, models.CollectionNotPaid, col.Status)
	assert.Equal(t, referans, col.Reference, "referans kırpılmalı")
	assert.Equal(t, "TRY", col.CurrencyCode, "para birimi BÜYÜK harfe çevrilmeli")
	assert.Equal(t, tutar, col.Amount)
	assert.Zero(t, col.AuthorizedAmount)
	assert.Zero(t, col.CapturedAmount)
	assert.Zero(t, col.RefundedAmount)
	assert.Equal(t, models.PaymentCollectionIDPrefix, col.ID[:len(models.PaymentCollectionIDPrefix)])
}

// TestCreatePaymentCollectionParaDogrulamasi tutar ve para birimi
// doğrulamasının her dalını sınar.
//
// Sıfır tutarın reddedilmesi bilinçlidir: tutarı sıfır olan bir koleksiyon
// hiçbir zaman "captured" olamayacağı için sonsuza kadar ödeme bekleyen ölü
// bir kayıt olurdu.
func TestCreatePaymentCollectionParaDogrulamasi(t *testing.T) {
	tests := []struct {
		ad string
		in service.CreateCollectionInput
	}{
		{"referanssiz", service.CreateCollectionInput{Amount: tutar, CurrencyCode: paraKodu}},
		{"bosluktan ibaret referans", service.CreateCollectionInput{
			Reference: "   ", Amount: tutar, CurrencyCode: paraKodu,
		}},
		{"sifir tutar", service.CreateCollectionInput{
			Reference: referans, Amount: 0, CurrencyCode: paraKodu,
		}},
		{"negatif tutar", service.CreateCollectionInput{
			Reference: referans, Amount: -1, CurrencyCode: paraKodu,
		}},
		{"tavani asan tutar", service.CreateCollectionInput{
			Reference: referans, Amount: models.MaxAmount + 1, CurrencyCode: paraKodu,
		}},
		{"para birimi yok", service.CreateCollectionInput{Reference: referans, Amount: tutar}},
		{"para birimi uzun", service.CreateCollectionInput{
			Reference: referans, Amount: tutar, CurrencyCode: "TRYX",
		}},
		{"para birimi rakamli", service.CreateCollectionInput{
			Reference: referans, Amount: tutar, CurrencyCode: "TR1",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.ad, func(t *testing.T) {
			svc, _, _ := yeniServis(t)

			_, err := svc.CreatePaymentCollection(context.Background(), tt.in)

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
		})
	}
}

// TestListPaymentCollectionsSuzgecVeSayfalama süzgecin ve sayfalama zarfının
// birlikte doğru çalıştığını doğrular.
//
// Toplam sayının SAYFANIN değil SÜZGECİN sayısı olduğu ayrıca sınanır: sayfa
// boyutu bir olsa da toplam üç dönmelidir, aksi hâlde istemci ikinci sayfanın
// var olduğunu bilemez.
func TestListPaymentCollectionsSuzgecVeSayfalama(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()
	for range 3 {
		koleksiyonAc(t, svc, tutar)
	}
	_, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_BASKA", Amount: tutar, CurrencyCode: paraKodu,
	})
	require.NoError(t, err)

	reference := referans
	sayfa, count, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{
		Reference: &reference,
		Page:      service.Page{Limit: 1},
	})

	require.NoError(t, err)
	assert.Len(t, sayfa, 1, "sayfa boyutu bir olmalı")
	assert.Equal(t, int64(3), count, "toplam süzgecin sayısıdır, sayfanın değil")
}

// TestListPaymentCollectionsSayfalamaDogrulamasi geçersiz sayfalama
// parametrelerinin reddedildiğini doğrular.
func TestListPaymentCollectionsSayfalamaDogrulamasi(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	tests := map[string]service.Page{
		"negatif limit":  {Limit: -1},
		"negatif offset": {Offset: -1},
		"tavani asan":    {Limit: service.MaxLimit + 1},
	}
	for ad, page := range tests {
		t.Run(ad, func(t *testing.T) {
			_, _, err := svc.ListPaymentCollections(ctx, service.ListCollectionsInput{Page: page})

			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
		})
	}
}

// TestListPaymentCollectionsTaninmayanDurumReddedilir süzgece yazılan bir
// yazım hatasının sessizce "sonuç yok" dönmediğini doğrular.
//
// Tanınmayan durumu sessizce süzmek, istemcinin gerçekten hiç kayıt olmadığını
// sanmasına yol açardı.
func TestListPaymentCollectionsTaninmayanDurumReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	durum := "paid"

	_, _, err := svc.ListPaymentCollections(context.Background(), service.ListCollectionsInput{
		Status: &durum,
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)
}

// TestOkumalarBilinmeyenKimlikteNotFound okuma yüzeyinin eksik kayıtta
// NotFound döndüğünü doğrular.
func TestOkumalarBilinmeyenKimlikteNotFound(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	_, err := svc.GetPaymentCollection(ctx, "paycol_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = svc.GetPaymentSession(ctx, "payses_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = svc.GetPayment(ctx, "pay_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = svc.ListPaymentSessions(ctx, "paycol_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = svc.ListPayments(ctx, "paycol_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)

	_, err = svc.ListRefunds(ctx, "pay_YOK")
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "hata: %v", err)
}

// TestBosKimlikInvalidDonurur boş kimliğin "bulunamadı" değil "geçersiz"
// olduğunu doğrular; ikisi çağıran için farklı hatalardır.
func TestBosKimlikInvalidDonurur(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	_, err := svc.GetPaymentCollection(ctx, "")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = svc.CreateSession(ctx, "", saglayiciID, service.CreateSessionInput{IdempotencyKey: "k"})
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	_, err = svc.AuthorizePayment(ctx, " ")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "hata: %v", err)

	assert.True(t, errors.HasKind(svc.CancelPayment(ctx, ""), errors.KindInvalid))
}

// TestProviderIDsKayitliSaglayicilariDoner vitrinin hangi yolları göreceğini
// doğrular.
func TestProviderIDsKayitliSaglayicilariDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)

	assert.Equal(t, []string{saglayiciID}, svc.ProviderIDs(context.Background()))
}

// --- mağaza kredisi ----------------------------------------------------------

// Mağaza kredisinin SERVİS yarısı (ADR 0152): operatörün verdiği kredi ve
// okunan bakiye. Harcama tarafı sağlayıcıda ve orada sınanıyor.

// TestVerilenKrediBakiyeyeGirer kararın en kısa hâli.
func TestVerilenKrediBakiyeyeGirer(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	entry, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID:   "cus_1",
		CurrencyCode: "try",
		Amount:       5_000,
		Reason:       "iade yerine kredi",
	})
	require.NoError(t, err)

	assert.Equal(t, models.StoreCreditIssue, entry.Kind)
	assert.Equal(t, int64(5_000), entry.Amount)
	assert.Equal(t, "TRY", entry.CurrencyCode,
		"para birimi NORMALLEŞTİRİLİR: 'try' ile 'TRY' aynı defteri okumalı, yoksa "+
			"aynı müşterinin iki bakiyesi olurdu")

	balance, err := svc.StoreCreditBalance(ctx, "cus_1", "TRY")
	require.NoError(t, err)
	assert.Equal(t, int64(5_000), balance)
}

// TestGerekcesizKrediReddedilir bakiye kolonunun tutamadığı yarıyı korur.
func TestGerekcesizKrediReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)

	_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
		CustomerID:   "cus_1",
		CurrencyCode: "TRY",
		Amount:       5_000,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeStoreCreditInvalidInput, errors.CodeOf(err))
}

// TestEksiKrediReddedilir operatörün bakiyeyi sıfırın altına indirmesini engeller.
//
// "Krediyi geri al" ayrı bir karar: eksi tutar yazmak, müşterinin harcadığı parayı
// geri almanın gizli yolu olurdu ve defterde bir GERİ ALMA olarak değil bir VERİŞ
// olarak görünürdü.
func TestEksiKrediReddedilir(t *testing.T) {
	svc, _, _ := yeniServis(t)

	for ad, tutar := range map[string]int64{"eksi": -100, "sıfır": 0} {
		t.Run(ad, func(t *testing.T) {
			_, err := svc.IssueCredit(context.Background(), service.IssueCreditInput{
				CustomerID:   "cus_1",
				CurrencyCode: "TRY",
				Amount:       tutar,
				Reason:       "deneme",
			})

			require.Error(t, err)
			assert.Equal(t, service.CodeStoreCreditInvalidInput, errors.CodeOf(err))
		})
	}
}

// TestBakiyePARABIRIMIBasinadir bir para birimindeki kredinin ötekinde kredi
// olmadığını çiviler.
//
// Kur çevirmek bu modülün işi değil ve sessizce çevirmek müşteriye vaat edilenden
// başka bir tutar vermek olurdu.
func TestBakiyePARABIRIMIBasinadir(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 5_000, Reason: "x",
	})
	require.NoError(t, err)

	other, err := svc.StoreCreditBalance(ctx, "cus_1", "EUR")
	require.NoError(t, err)
	assert.Zero(t, other, "TRY kredisi EUR bakiyesinde görünmemeli")
}

// TestKredisiOlmayanMusteriSIFIRDondurur yokluğu sıfırdan ayırmaz.
//
// Hiç kredi verilmemiş biriyle verilip tamamı harcanmış biri aynı miktarda paraya
// sahiptir; aradaki farkı defterin kendisi anlatıyor.
func TestKredisiOlmayanMusteriSIFIRDondurur(t *testing.T) {
	svc, _, _ := yeniServis(t)

	balance, err := svc.StoreCreditBalance(context.Background(), "cus_yok", "TRY")

	require.NoError(t, err)
	assert.Zero(t, balance)
}

// TestKrediGecmisiYenidenEskiyeDoner operatörün "neden" sorusunu cevaplar.
func TestKrediGecmisiYenidenEskiyeDoner(t *testing.T) {
	svc, _, _ := yeniServis(t)
	ctx := context.Background()

	for _, reason := range []string{"birinci", "ikinci"} {
		_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
			CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 1_000, Reason: reason,
		})
		require.NoError(t, err)
	}

	entries, total, err := svc.ListStoreCredit(ctx, service.ListStoreCreditInput{
		CustomerID: "cus_1", CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2), total)
	require.Len(t, entries, 2)
	assert.Equal(t, "ikinci", entries[0].Reason, "en yeni satır başta")
}
