package manual_test

import (
	"context"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
)

// yeniSaglayici bellek içi defter üzerinde çalışan bir manuel sağlayıcı kurar.
func yeniSaglayici() (*manual.Provider, *memStore) {
	store := newMemStore()
	return manual.New(store, nil), store
}

// TestSaglayiciKimligi kayıt adının sabit olduğunu kanıtlar.
func TestSaglayiciKimligi(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	assert.Equal(t, "manual", provider.ID())
	assert.Equal(t, manual.ID, provider.ID())
}

// TestQuoteYanEtkisizdir çekirdek sözleşmesinin en sert şartını kanıtlar.
//
// Quote sepet toplamı hesaplanırken defalarca çağrılır; deftere tek bir satır
// yazsaydı, sepeti açık bırakan bir müşteri yüzlerce kayıt üretirdi.
func TestQuoteYanEtkisizdir(t *testing.T) {
	t.Parallel()

	provider, store := yeniSaglayici()
	girdi := coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		ItemCount:    3,
		TotalWeight:  1_200,
		Data:         map[string]any{manual.DataKeyBaseAmount: 1_000},
	}

	ilk, err := provider.Quote(context.Background(), girdi)
	require.NoError(t, err)

	for range 5 {
		tekrar, err := provider.Quote(context.Background(), girdi)
		require.NoError(t, err)
		assert.Equal(t, ilk.Amount, tekrar.Amount, "aynı girdi aynı ücreti dönmeli")
	}

	assert.Zero(t, store.yazmaSayisi(), "Quote deftere HİÇ yazmamalı")
}

// TestQuoteFormulu ücret bileşenlerinin belgelendiği gibi toplandığını
// kanıtlar.
//
// Ağırlık BAŞLANAN kilograma yuvarlanır: 1200 gram İKİ kilogram sayılır.
// Aşağı yuvarlamak, 1999 gramlık bir paketi bir kilogram ücretine taşımak
// demek olurdu.
func TestQuoteFormulu(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		ad       string
		data     map[string]any
		adet     int64
		agirlik  int64
		beklenen int64
	}{
		{"yapılandırmasız seçenek ücretsizdir", nil, 3, 5_000, 0},
		{"yalnızca taban", map[string]any{manual.DataKeyBaseAmount: 2_500}, 3, 5_000, 2_500},
		{
			"taban + kalem",
			map[string]any{manual.DataKeyBaseAmount: 1_000, manual.DataKeyPerItemAmount: 250},
			4, 0, 2_000,
		},
		{
			"tam kilogram yukarı yuvarlanmaz",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1_000, 500,
		},
		{
			"başlanan kilogram tam sayılır",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1_001, 1_000,
		},
		{
			"bir gram bile bir kilogramdır",
			map[string]any{manual.DataKeyPerKilogramAmount: 500},
			0, 1, 500,
		},
		{
			"tüm bileşenler",
			map[string]any{
				manual.DataKeyBaseAmount:        1_000,
				manual.DataKeyPerItemAmount:     100,
				manual.DataKeyPerKilogramAmount: 400,
			},
			3, 2_500, 1_000 + 300 + 1_200,
		},
		{
			"doğrudan tutar bileşenleri ezer",
			map[string]any{
				manual.DataKeyQuoteAmount:   7_777,
				manual.DataKeyBaseAmount:    1_000,
				manual.DataKeyPerItemAmount: 100,
			},
			5, 9_000, 7_777,
		},
		{
			"sıfır tutar geçerlidir",
			map[string]any{manual.DataKeyQuoteAmount: 0, manual.DataKeyBaseAmount: 5_000},
			1, 1_000, 0,
		},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			provider, _ := yeniSaglayici()
			quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
				OptionID:     "sopt_1",
				CurrencyCode: "TRY",
				ItemCount:    durum.adet,
				TotalWeight:  durum.agirlik,
				Data:         durum.data,
			})
			require.NoError(t, err)
			assert.Equal(t, durum.beklenen, quote.Amount)
			assert.Equal(t, "TRY", quote.CurrencyCode)
			assert.Equal(t, "sopt_1", quote.OptionID)
		})
	}
}

// TestQuoteGirdiDogrulamasi geçersiz girdinin reddedildiğini kanıtlar.
func TestQuoteGirdiDogrulamasi(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		ad    string
		girdi coreprovider.QuoteInput
	}{
		{"seçenek yok", coreprovider.QuoteInput{CurrencyCode: "TRY"}},
		{"para birimi bozuk", coreprovider.QuoteInput{OptionID: "sopt_1", CurrencyCode: "TR"}},
		{"negatif adet", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY", ItemCount: -1,
		}},
		{"negatif ağırlık", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY", TotalWeight: -1,
		}},
		{"negatif ücret bileşeni", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY",
			Data: map[string]any{manual.DataKeyBaseAmount: -1},
		}},
		{"tanınmayan davranış", coreprovider.QuoteInput{
			OptionID: "sopt_1", CurrencyCode: "TRY",
			Data: map[string]any{manual.DataKeyOutcome: "explode"},
		}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			provider, _ := yeniSaglayici()
			_, err := provider.Quote(context.Background(), durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		})
	}
}

// TestQuoteHataEnjeksiyonu saga testlerinin kargo adımını patlatabildiğini
// kanıtlar.
func TestQuoteHataEnjeksiyonu(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	_, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		Data:         map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.Error(t, err)
	assert.Equal(t, manual.CodeSimulatedFailure, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"enjekte edilen hata YENİDEN DENENEBİLİR olmalı: %v", err)
}

// TestQuoteTasmaYakalanir kalem adedi ve ağırlık dışarıdan geldiği için
// çarpımın taşma denetimi olduğunu kanıtlar.
//
// Taşsaydı sessizce NEGATİF bir kargo ücreti çıkardı — yani müşteriye para
// ödeyen bir sipariş.
func TestQuoteTasmaYakalanir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	_, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		ItemCount:    1 << 40,
		Data:         map[string]any{manual.DataKeyPerItemAmount: 1_000_000_000},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestQuoteKilogramYuvarlamasiTasmaz yuvarlamanın ARA ADIMINDA taşmadığını
// kanıtlar.
//
// Regresyon: yuvarlama "(gram + 999) / 1000" biçimindeydi ve toplama int64'ün
// tepesinde TAŞIP negatif bir kilogram üretiyordu. Negatif kilogram, yalnızca
// pozitif operanda bakan taşma denetimlerinden geçiyor ve Quote hatasız bir
// NEGATİF ücret dönüyordu (ölçüldü: -9223372036854774000). Bu, sağlayıcının
// "hiçbir girdi için negatif ücret dönmem" sözleşmesinin ihlaliydi.
func TestQuoteKilogramYuvarlamasiTasmaz(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	for _, agirlik := range []int64{
		math.MaxInt64,
		math.MaxInt64 - 500,
		math.MaxInt64 - 999,
	} {
		quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
			OptionID:     "sopt_1",
			CurrencyCode: "TRY",
			TotalWeight:  agirlik,
			Data:         map[string]any{manual.DataKeyPerKilogramAmount: 1_000},
		})
		require.Error(t, err, "%d gram için hata dönmeli", agirlik)
		assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		assert.GreaterOrEqual(t, quote.Amount, models.MinAmount,
			"hata dalında bile negatif ücret sızmamalı: %d", quote.Amount)
	}
}

// TestQuoteSinirdakiAgirlikTasmadanFiyatlanir modülün izin verdiği EN BÜYÜK
// ağırlığın hâlâ hesaplanabildiğini kanıtlar.
//
// Taşma düzeltmesinin fiyatı olmadığını gösterir: sınırlar geçerli girdiyi
// dışarıda bırakmaz.
func TestQuoteSinirdakiAgirlikTasmadanFiyatlanir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	quote, err := provider.Quote(context.Background(), coreprovider.QuoteInput{
		OptionID:     "sopt_1",
		CurrencyCode: "TRY",
		TotalWeight:  models.MaxTotalWeight,
		Data:         map[string]any{manual.DataKeyPerKilogramAmount: 1},
	})
	require.NoError(t, err)
	assert.Equal(t, models.MaxTotalWeight/1_000, quote.Amount)
}

// TestCreateIdempotenttir aynı anahtarla ikinci çağrının YENİ gönderi
// açmadığını kanıtlar.
func TestCreateIdempotenttir(t *testing.T) {
	t.Parallel()

	provider, store := yeniSaglayici()
	girdi := coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "anahtar-1",
	}

	ilk, err := provider.Create(context.Background(), girdi)
	require.NoError(t, err)
	yazma := store.yazmaSayisi()

	ikinci, err := provider.Create(context.Background(), girdi)
	require.NoError(t, err)
	assert.Equal(t, ilk.ID, ikinci.ID, "aynı anahtar aynı gönderiyi dönmeli")
	assert.Equal(t, yazma, store.yazmaSayisi(), "ikinci çağrı deftere yazmamalı")
}

// TestCreateAyniAnahtarFarkliGovdeCakisir idempotency'nin "aynı isteği
// tekrarlamak" demek olduğunu kanıtlar.
func TestCreateAyniAnahtarFarkliGovdeCakisir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "anahtar-1",
	})
	require.NoError(t, err)

	_, err = provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_2", OptionID: "sopt_1", IdempotencyKey: "anahtar-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateAyniAnahtarFarkliSecenekCakisir karşılaştırmanın İKİNCİ yarısını
// sınar: referans aynı, kargo SEÇENEĞİ farklı.
//
// Yalnızca referansı değiştiren bir test, seçenek karşılaştırmasını silen bir
// mutasyonu yakalayamıyordu; "aynı anahtar farklı seçenekle kullanılamaz"
// iddiasının kanıtı buydu ve yoktu. Sessizce kabul edilseydi, çağıran hızlı
// kargo isteyip standart kargoyla açılmış bir gönderi alırdı.
func TestCreateAyniAnahtarFarkliSecenekCakisir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "anahtar-1",
	})
	require.NoError(t, err)

	_, err = provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_2", IdempotencyKey: "anahtar-1",
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, manual.CodeIdempotencyMismatch, errors.CodeOf(err))
}

// TestCreateEszamanliTekGonderiUretir yarışın defter düzeyinde çözüldüğünü
// kanıtlar.
func TestCreateEszamanliTekGonderiUretir(t *testing.T) {
	t.Parallel()

	provider, store := yeniSaglayici()

	const eszamanli = 8
	kimlikler := make([]string, eszamanli)
	hatalar := make([]error, eszamanli)

	var basla sync.WaitGroup
	var bitti sync.WaitGroup
	basla.Add(1)
	bitti.Add(eszamanli)

	for i := range eszamanli {
		go func() {
			defer bitti.Done()
			basla.Wait()
			ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
				Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "anahtar-yaris",
			})
			kimlikler[i], hatalar[i] = ful.ID, err
		}()
	}
	basla.Done()
	bitti.Wait()

	for i, err := range hatalar {
		require.NoErrorf(t, err, "%d. çağrı hata döndü", i)
	}
	for i := 1; i < eszamanli; i++ {
		assert.Equal(t, kimlikler[0], kimlikler[i], "tüm çağrılar aynı gönderiyi dönmeli")
	}
	assert.Equal(t, 1, store.yazmaSayisi(), "deftere TAM OLARAK bir satır yazılmalı")
}

// TestCreateHataEnjeksiyonu gönderi açma adımının patlatılabildiğini ve
// defterin BOŞ kaldığını kanıtlar.
func TestCreateHataEnjeksiyonu(t *testing.T) {
	t.Parallel()

	provider, store := yeniSaglayici()
	_, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "anahtar-1",
		Data:           map[string]any{manual.DataKeyOutcome: manual.OutcomeError},
	})
	require.Error(t, err)
	assert.Equal(t, manual.CodeSimulatedFailure, errors.CodeOf(err))
	assert.Zero(t, store.yazmaSayisi(), "patlayan çağrı deftere yazmamalı")
}

// TestCreateTakipBilgisiVerilebilir enjekte edilen takip bilgisinin
// gönderiyle birlikte SAKLANDIĞINI kanıtlar.
//
// Saklanmasaydı, sonraki bir okuma (farklı süreçte bile olabilir) takip
// numarasını bulamazdı.
func TestCreateTakipBilgisiVerilebilir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference:      "ful_1",
		OptionID:       "sopt_1",
		IdempotencyKey: "anahtar-1",
		Data: map[string]any{
			manual.DataKeyTrackingNumber: "TK-42",
			manual.DataKeyTrackingURL:    "https://kargo.example/TK-42",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "TK-42", ful.TrackingNumber)
	assert.Equal(t, "https://kargo.example/TK-42", ful.TrackingURL)

	saklanan, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, "TK-42", saklanan.TrackingNumber, "takip bilgisi kalıcı olmalı")
}

// TestCreateGirdiDogrulamasi zorunlu alanların denetlendiğini kanıtlar.
func TestCreateGirdiDogrulamasi(t *testing.T) {
	t.Parallel()

	durumlar := []struct {
		ad    string
		girdi coreprovider.CreateFulfillmentInput
	}{
		{"anahtar yok", coreprovider.CreateFulfillmentInput{Reference: "ful_1", OptionID: "sopt_1"}},
		{"referans yok", coreprovider.CreateFulfillmentInput{OptionID: "sopt_1", IdempotencyKey: "a"}},
		{"seçenek yok", coreprovider.CreateFulfillmentInput{Reference: "ful_1", IdempotencyKey: "a"}},
	}

	for _, durum := range durumlar {
		t.Run(durum.ad, func(t *testing.T) {
			t.Parallel()

			provider, _ := yeniSaglayici()
			_, err := provider.Create(context.Background(), durum.girdi)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		})
	}
}

// TestCancelIdempotenttir saga telafisinin şartını kanıtlar.
func TestCancelIdempotenttir(t *testing.T) {
	t.Parallel()

	provider, store := yeniSaglayici()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "anahtar-1",
	})
	require.NoError(t, err)

	require.NoError(t, provider.Cancel(context.Background(), ful.ID))
	yazma := store.yazmaSayisi()

	require.NoError(t, provider.Cancel(context.Background(), ful.ID),
		"ikinci iptal hata dönmemeli")
	assert.Equal(t, yazma, store.yazmaSayisi(), "ikinci iptal deftere yazmamalı")

	saklanan, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, models.StatusCanceled, saklanan.Status)
}

// TestCancelBilinmeyenKimlikteNotFound idempotentliğin "her şeyi sessizce yut"
// demek OLMADIĞINI kanıtlar.
func TestCancelBilinmeyenKimlikteNotFound(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()

	err := provider.Cancel(context.Background(), "manful_YOKBOYLE")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)

	err = provider.Cancel(context.Background(), "   ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "boş kimlik errors.Invalid olmalı: %v", err)
}

// TestCancelTakipBilgisiniKorur iptal edilen bir gönderinin hangi etiketle
// açıldığının teşhis için okunabilir kaldığını kanıtlar.
func TestCancelTakipBilgisiniKorur(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	ful, err := provider.Create(context.Background(), coreprovider.CreateFulfillmentInput{
		Reference: "ful_1", OptionID: "sopt_1", IdempotencyKey: "anahtar-1",
		Data: map[string]any{manual.DataKeyTrackingNumber: "TK-42"},
	})
	require.NoError(t, err)
	require.NoError(t, provider.Cancel(context.Background(), ful.ID))

	saklanan, err := provider.GetShipment(context.Background(), ful.ID)
	require.NoError(t, err)
	assert.Equal(t, "TK-42", saklanan.TrackingNumber)
}

// TestGetShipmentBosKimligiReddeder teşhis yüzeyinin de doğrulandığını
// kanıtlar.
func TestGetShipmentBosKimligiReddeder(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	_, err := provider.GetShipment(context.Background(), " ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestCekirdekSozlesmesiKarsilanir sağlayıcının çekirdek arayüzünü karşıladığını
// derleme zamanında sabitler.
func TestCekirdekSozlesmesiKarsilanir(t *testing.T) {
	t.Parallel()

	provider, _ := yeniSaglayici()
	var sozlesme coreprovider.FulfillmentProvider = provider
	assert.Equal(t, manual.ID, sozlesme.ID())
}
