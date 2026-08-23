package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// Bu dosya CalculatePrice'ın seçim kuralının HER DALINI kanıtlar.
//
// Testlerin ortak tasarım ilkesi: bir ölçütün kanıtlandığı senaryoda DİĞER TÜM
// ölçütler kaybedeni kayırır. Böylece bir ölçüt koddan çıkarıldığında test
// kaçınılmaz olarak düşer — "yanlış nedenle geçen test" bu yolla engellenir.

// TestSelectPrefersOverrideOverSale liste önceliğinin ilk ölçüt olduğunu
// kanıtlar: override, sale'i yener.
//
// Kaybeden (sale) DAHA UCUZ ve DAHA DAR aralıklıdır; yani sonraki tüm ölçütler
// onu kayırır. Kazanan yalnızca liste önceliğiyle kazanabilir.
func TestSelectPrefersOverrideOverSale(t *testing.T) {
	sale := withList(basePrice("price_a", "TRY", 1000, 5, ptr(int32(6))), "plist_sale",
		activeList("plist_sale", models.PriceListSale))
	override := withList(basePrice("price_b", "TRY", 9000, 1, nil), "plist_ovr",
		activeList("plist_ovr", models.PriceListOverride))

	got, ok := selectPrice([]models.PriceCandidate{sale, override}, "TRY", 5, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "override listesi sale'i yenmeli")
	assert.Equal(t, models.PriceListOverride, got.PriceListType)
}

// TestSelectPrefersListOverBase liste fiyatının taban fiyatı yendiğini
// kanıtlar. Taban fiyat daha ucuzdur; yalnızca öncelik ölçütü karar verebilir.
func TestSelectPrefersListOverBase(t *testing.T) {
	base := basePrice("price_a", "TRY", 1000, 1, nil)
	sale := withList(basePrice("price_b", "TRY", 9000, 1, nil), "plist_sale",
		activeList("plist_sale", models.PriceListSale))

	got, ok := selectPrice([]models.PriceCandidate{base, sale}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "kampanya listesi taban fiyatı yenmeli")
}

// TestSelectPrefersMoreSpecificRules kural sayısının ikinci ölçüt olduğunu
// kanıtlar.
//
// İki aday da AYNI listededir (öncelik eşit). Çok kurallı aday DAHA PAHALI ve
// DAHA GENİŞ aralıklıdır; span ve tutar ölçütleri az kurallıyı kayırır.
func TestSelectPrefersMoreSpecificRules(t *testing.T) {
	attrs := map[string]string{"region_id": "reg_1", "customer_group_id": "vip"}

	single := withRules(basePrice("price_a", "TRY", 1000, 5, ptr(int32(6))),
		rule("region_id", models.OpEq, "reg_1"))
	double := withRules(basePrice("price_b", "TRY", 9000, 1, nil),
		rule("region_id", models.OpEq, "reg_1"),
		rule("customer_group_id", models.OpEq, "vip"))

	got, ok := selectPrice([]models.PriceCandidate{single, double}, "TRY", 5, attrs, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "daha çok kural sağlayan fiyat daha belirgindir")
	assert.Equal(t, 2, got.MatchedRules)
}

// TestSelectPrefersNarrowerQuantityRange aralık genişliğinin üçüncü ölçüt
// olduğunu kanıtlar.
//
// Kural sayısı ve liste önceliği eşittir; dar aralıklı aday DAHA PAHALIDIR,
// yani tutar ölçütü geniş olanı kayırır.
func TestSelectPrefersNarrowerQuantityRange(t *testing.T) {
	wide := basePrice("price_a", "TRY", 1000, 1, nil)
	narrow := basePrice("price_b", "TRY", 9000, 10, ptr(int32(20)))

	got, ok := selectPrice([]models.PriceCandidate{wide, narrow}, "TRY", 10, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "dar adet aralığı toptan kademesini kazandırmalı")
}

// TestSelectPrefersLowerAmount tutar ölçütünün dördüncü sırada olduğunu
// kanıtlar. Diğer tüm ölçütler eşittir.
func TestSelectPrefersLowerAmount(t *testing.T) {
	expensive := basePrice("price_a", "TRY", 9000, 1, ptr(int32(10)))
	cheap := basePrice("price_b", "TRY", 1000, 1, ptr(int32(10)))

	got, ok := selectPrice([]models.PriceCandidate{expensive, cheap}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "eşdeğer adaylarda müşteri lehine karar verilmeli")
}

// TestSelectIsDeterministicOnFullTie tam eşitlikte kimliğin karar verdiğini ve
// sonucun adayların GELİŞ SIRASINDAN bağımsız olduğunu kanıtlar.
func TestSelectIsDeterministicOnFullTie(t *testing.T) {
	first := basePrice("price_a", "TRY", 1000, 1, ptr(int32(10)))
	second := basePrice("price_b", "TRY", 1000, 1, ptr(int32(10)))

	forward, ok := selectPrice([]models.PriceCandidate{first, second}, "TRY", 1, nil, testNow)
	require.True(t, ok)
	backward, ok := selectPrice([]models.PriceCandidate{second, first}, "TRY", 1, nil, testNow)
	require.True(t, ok)

	assert.Equal(t, "price_a", forward.PriceID)
	assert.Equal(t, forward.PriceID, backward.PriceID, "sonuç aday sırasından bağımsız olmalı")
}

// TestSelectFiltersByCurrency para birimi elemesini kanıtlar.
func TestSelectFiltersByCurrency(t *testing.T) {
	usd := basePrice("price_usd", "USD", 100, 1, nil)
	try := basePrice("price_try", "TRY", 5000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{usd, try}, "TRY", 1, nil, testNow)
	require.True(t, ok)
	assert.Equal(t, "price_try", got.PriceID, "başka para birimindeki daha ucuz fiyat seçilemez")

	_, ok = selectPrice([]models.PriceCandidate{usd}, "EUR", 1, nil, testNow)
	assert.False(t, ok, "hiç eşleşen para birimi yoksa aday kalmamalı")
}

// TestSelectFiltersByQuantityRange adet aralığı elemesini uç değerleriyle
// kanıtlar; sınırlar KAPSAYICIDIR.
func TestSelectFiltersByQuantityRange(t *testing.T) {
	tiered := basePrice("price_tier", "TRY", 1000, 10, ptr(int32(20)))
	candidates := []models.PriceCandidate{tiered}

	for _, tc := range []struct {
		name     string
		quantity int32
		want     bool
	}{
		{"alt sınırın altında", 9, false},
		{"alt sınırda", 10, true},
		{"aralık içinde", 15, true},
		{"üst sınırda", 20, true},
		{"üst sınırın üstünde", 21, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := selectPrice(candidates, "TRY", tc.quantity, nil, testNow)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectSkipsUnusablePriceLists yalnızca durumu active olan listelerin
// fiyat sunabildiğini kanıtlar.
func TestSelectSkipsUnusablePriceLists(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status models.PriceListStatus
		want   bool
	}{
		{"taslak liste fiyat sunmaz", models.PriceListDraft, false},
		{"sonlandırılmış liste fiyat sunmaz", models.PriceListExpired, false},
		{"yayındaki liste fiyat sunar", models.PriceListActive, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &models.PriceListInfo{ID: "plist_1", Type: models.PriceListSale, Status: tc.status}
			candidate := withList(basePrice("price_a", "TRY", 1000, 1, nil), "plist_1", info)

			_, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 1, nil, testNow)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectHonoursPriceListWindow tarih penceresinin uçlarını kanıtlar.
// Uçlar KAPSAYICIDIR: tam başlangıç ve tam bitiş anında liste geçerlidir.
func TestSelectHonoursPriceListWindow(t *testing.T) {
	starts := testNow.Add(-time.Hour)
	ends := testNow.Add(time.Hour)

	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"başlangıçtan önce", starts.Add(-time.Second), false},
		{"tam başlangıçta", starts, true},
		{"pencere içinde", testNow, true},
		{"tam bitişte", ends, true},
		{"bitişten sonra", ends.Add(time.Second), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &models.PriceListInfo{
				ID:       "plist_1",
				Type:     models.PriceListSale,
				Status:   models.PriceListActive,
				StartsAt: &starts,
				EndsAt:   &ends,
			}
			candidate := withList(basePrice("price_a", "TRY", 1000, 1, nil), "plist_1", info)

			_, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 1, nil, tc.at)
			assert.Equal(t, tc.want, ok)
		})
	}
}

// TestSelectSkipsPriceWithDeletedList listesi silinmiş bir fiyatın (liste
// kimliği dolu ama üstverisi yok) elendiğini kanıtlar.
func TestSelectSkipsPriceWithDeletedList(t *testing.T) {
	orphan := withList(basePrice("price_orphan", "TRY", 100, 1, nil), "plist_gone", nil)
	base := basePrice("price_base", "TRY", 5000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{orphan, base}, "TRY", 1, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_base", got.PriceID, "listesi silinmiş fiyat hesaba katılmamalı")
}

// TestSelectRequiresAllRulesToMatch kuralların VE ile birleştiğini kanıtlar.
func TestSelectRequiresAllRulesToMatch(t *testing.T) {
	candidate := withRules(basePrice("price_a", "TRY", 100, 1, nil),
		rule("region_id", models.OpEq, "reg_1"),
		rule("customer_group_id", models.OpEq, "vip"))
	candidates := []models.PriceCandidate{candidate}

	_, ok := selectPrice(candidates, "TRY", 1,
		map[string]string{"region_id": "reg_1", "customer_group_id": "vip"}, testNow)
	assert.True(t, ok, "tüm kurallar sağlanınca fiyat geçerli olmalı")

	_, ok = selectPrice(candidates, "TRY", 1,
		map[string]string{"region_id": "reg_1", "customer_group_id": "normal"}, testNow)
	assert.False(t, ok, "tek bir kural bile sağlanmazsa fiyat elenmeli")
}

// TestSelectComputesTotal sonucun toplam alanının tutar × adet olduğunu
// kanıtlar.
func TestSelectComputesTotal(t *testing.T) {
	candidate := basePrice("price_a", "TRY", 1250, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{candidate}, "TRY", 4, nil, testNow)

	require.True(t, ok)
	assert.Equal(t, int64(1250), got.Amount)
	assert.Equal(t, int32(4), got.Quantity)
	assert.Equal(t, int64(5000), got.Total)
}

// TestSelectReturnsNoCandidate boş aday kümesinde seçim yapılamadığını
// bildirir.
func TestSelectReturnsNoCandidate(t *testing.T) {
	_, ok := selectPrice(nil, "TRY", 1, nil, testNow)
	assert.False(t, ok)
}

// TestQuantitySpanUnbounded üst sınırsız aralığın azami genişlik saydığını
// kanıtlar; "dar olan kazanır" kuralı buna dayanır.
func TestQuantitySpanUnbounded(t *testing.T) {
	assert.Equal(t, int64(math.MaxInt64), quantitySpan(models.Price{MinQuantity: 1}))
	assert.Equal(t, int64(9), quantitySpan(models.Price{MinQuantity: 1, MaxQuantity: ptr(int32(10))}))
}

// TestMatchRuleOperators her işlecin hem eşleşen hem eşleşmeyen dalını
// kanıtlar.
func TestMatchRuleOperators(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rule  models.PriceRule
		attrs map[string]string
		want  bool
	}{
		{"eq eşleşir", rule("k", models.OpEq, "a"), map[string]string{"k": "a"}, true},
		{"eq eşleşmez", rule("k", models.OpEq, "a"), map[string]string{"k": "b"}, false},
		{"ne eşleşir", rule("k", models.OpNe, "a"), map[string]string{"k": "b"}, true},
		{"ne eşleşmez", rule("k", models.OpNe, "a"), map[string]string{"k": "a"}, false},
		{"in eşleşir", rule("k", models.OpIn, "a", "b"), map[string]string{"k": "b"}, true},
		{"in eşleşmez", rule("k", models.OpIn, "a", "b"), map[string]string{"k": "c"}, false},
		{"nin eşleşir", rule("k", models.OpNin, "a", "b"), map[string]string{"k": "c"}, true},
		{"nin eşleşmez", rule("k", models.OpNin, "a", "b"), map[string]string{"k": "a"}, false},
		{"gt eşleşir", rule("k", models.OpGt, "10"), map[string]string{"k": "11"}, true},
		{"gt sınırda eşleşmez", rule("k", models.OpGt, "10"), map[string]string{"k": "10"}, false},
		{"gte sınırda eşleşir", rule("k", models.OpGte, "10"), map[string]string{"k": "10"}, true},
		{"gte eşleşmez", rule("k", models.OpGte, "10"), map[string]string{"k": "9"}, false},
		{"lt eşleşir", rule("k", models.OpLt, "10"), map[string]string{"k": "9"}, true},
		{"lt sınırda eşleşmez", rule("k", models.OpLt, "10"), map[string]string{"k": "10"}, false},
		{"lte sınırda eşleşir", rule("k", models.OpLte, "10"), map[string]string{"k": "10"}, true},
		{"lte eşleşmez", rule("k", models.OpLte, "10"), map[string]string{"k": "11"}, false},

		{"alan bağlamda yoksa eşleşmez", rule("k", models.OpEq, "a"), map[string]string{"x": "a"}, false},
		{"olumsuz işleç de alan yokluğunda eşleşmez",
			rule("k", models.OpNe, "a"), map[string]string{"x": "a"}, false},
		{"sayısal işleç metin bağlamda eşleşmez",
			rule("k", models.OpGt, "10"), map[string]string{"k": "abc"}, false},
		{"tanınmayan işleç eşleşmez",
			rule("k", models.RuleOperator("regex"), "a"), map[string]string{"k": "a"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, matchRule(tc.rule, tc.attrs))
		})
	}
}

// TestMatchRulesEmptyIsUnconditional kuralsız fiyatın koşulsuz olduğunu
// kanıtlar.
func TestMatchRulesEmptyIsUnconditional(t *testing.T) {
	assert.True(t, matchRules(nil, nil))
	assert.True(t, matchRules([]models.PriceRule{}, map[string]string{"k": "v"}))
}

// TestCalculatePriceUsesServiceClock At verilmediğinde servisin saatinin
// kullanıldığını kanıtlar: pencere dışında kalan liste elenir.
func TestCalculatePriceUsesServiceClock(t *testing.T) {
	ended := testNow.Add(-time.Minute)
	info := &models.PriceListInfo{
		ID:     "plist_1",
		Type:   models.PriceListSale,
		Status: models.PriceListActive,
		EndsAt: &ended,
	}

	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{
			withList(basePrice("price_sale", "TRY", 100, 1, nil), "plist_1", info),
			basePrice("price_base", "TRY", 500, 1, nil),
		}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Equal(t, "price_base", got.PriceID, "süresi dolmuş kampanya seçilmemeli")
}

// TestCalculatePriceNormalizesCurrency küçük harfli para biriminin
// büyütüldüğünü kanıtlar.
func TestCalculatePriceNormalizesCurrency(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 100, 1, nil)}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: " try "})

	require.NoError(t, err)
	assert.Equal(t, "TRY", got.CurrencyCode)
}

// TestCalculatePriceDefaultsQuantityToOne adet verilmediğinde 1 kabul
// edildiğini kanıtlar.
func TestCalculatePriceDefaultsQuantityToOne(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 700, 1, nil)}, nil
	}

	got, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Equal(t, int32(1), got.Quantity)
	assert.Equal(t, int64(700), got.Total)
}

// TestCalculatePriceDistinguishesMissingSetFromMissingPrice iki NotFound
// durumunun FARKLI kodlarla döndüğünü kanıtlar: kap yok vs kap boş.
func TestCalculatePriceDistinguishesMissingSetFromMissingPrice(t *testing.T) {
	t.Run("kap yok", func(t *testing.T) {
		repo := newStubRepo()
		repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
			return nil, nil
		}
		repo.getPriceSetFn = func(context.Context, string) (models.PriceSet, error) {
			return models.PriceSet{}, errors.NotFound("price_set_not_found", "yok")
		}

		_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
			CalculateParams{CurrencyCode: "TRY"})

		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, "price_set_not_found", errors.CodeOf(err))
	})

	t.Run("kap var ama fiyatı yok", func(t *testing.T) {
		repo := newStubRepo()
		repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
			return nil, nil
		}
		repo.getPriceSetFn = func(_ context.Context, id string) (models.PriceSet, error) {
			return models.PriceSet{ID: id}, nil
		}

		_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
			CalculateParams{CurrencyCode: "TRY"})

		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeNotCalculable, errors.CodeOf(err))
	})
}

// TestCalculatePriceSkipsExistenceCheckWhenPricesExist mutlu yolun TEK gidiş
// dönüş yaptığını kanıtlar; ikinci sorgu yalnızca boş sonuçta açılır.
func TestCalculatePriceSkipsExistenceCheckWhenPricesExist(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{basePrice("price_a", "TRY", 100, 1, nil)}, nil
	}

	_, err := newTestService(repo).CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Zero(t, repo.calls["GetPriceSet"], "fiyat varken kap varlığı ayrıca sorulmamalı")
	assert.Equal(t, 1, repo.calls["ListPriceCandidates"])
}

// TestCalculatePriceRejectsBadInput girdi doğrulamasının veritabanına GİTMEDEN
// önce çalıştığını kanıtlar.
func TestCalculatePriceRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setID  string
		params CalculateParams
	}{
		{"kimlik öneki yanlış", "variant_1", CalculateParams{CurrencyCode: "TRY"}},
		{"kimlik boş", "", CalculateParams{CurrencyCode: "TRY"}},
		{"para birimi eksik", "pset_1", CalculateParams{}},
		{"para birimi dört harf", "pset_1", CalculateParams{CurrencyCode: "TRYX"}},
		{"para birimi harf değil", "pset_1", CalculateParams{CurrencyCode: "T1L"}},
		{"adet negatif", "pset_1", CalculateParams{CurrencyCode: "TRY", Quantity: -1}},
		{"adet sınırın üstünde", "pset_1",
			CalculateParams{CurrencyCode: "TRY", Quantity: models.MaxQuantity + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()

			_, err := newTestService(repo).CalculatePrice(context.Background(), tc.setID, tc.params)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Empty(t, repo.calls, "geçersiz girdi depoya hiç gitmemeli")
		})
	}
}

// TestCalculateAmountMatchesCalculatePrice modüller arası dar yüzeyin aynı
// seçim kuralını kullandığını kanıtlar.
func TestCalculateAmountMatchesCalculatePrice(t *testing.T) {
	repo := newStubRepo()
	repo.listCandidatesFn = func(context.Context, string) ([]models.PriceCandidate, error) {
		return []models.PriceCandidate{
			basePrice("price_wide", "TRY", 1000, 1, nil),
			basePrice("price_tier", "TRY", 800, 10, ptr(int32(20))),
		}, nil
	}
	svc := newTestService(repo)

	amount, err := svc.CalculateAmount(context.Background(), "pset_1", "TRY", 10, nil)
	require.NoError(t, err)

	full, err := svc.CalculatePrice(context.Background(), "pset_1",
		CalculateParams{CurrencyCode: "TRY", Quantity: 10})
	require.NoError(t, err)

	assert.Equal(t, full.Amount, amount)
	assert.Equal(t, int64(800), amount)
}

// TestMatchRuleWithoutValuesDoesNotMatch değersiz bir kuralın PANİK ETMEDEN
// eşleşmez sayıldığını kanıtlar.
//
// Servis doğrulaması boş değer listesini reddeder, ama veritabanındaki CHECK
// kısıtı tek başına yeterli bir kapı değildir (bkz. migration 000002) ve
// doğrudan SQL çalıştıran bir bakım betiği böyle bir satır üretebilir. Kural
// değeri okunamıyorsa doğru davranış, tanınmayan işleçteki gerekçenin aynısıdır:
// kural sessizce devre dışı kalıp fiyatı herkese AÇMAMALIDIR. "nin" ve "ne"
// dalları bu yüzden ayrıca kanıtlanır — sınır denetimi olmasa ikisi de değersiz
// kuralı SAĞLANMIŞ sayardı.
func TestMatchRuleWithoutValuesDoesNotMatch(t *testing.T) {
	for _, op := range []models.RuleOperator{
		models.OpEq, models.OpNe, models.OpIn, models.OpNin,
		models.OpGt, models.OpGte, models.OpLt, models.OpLte,
	} {
		t.Run(string(op), func(t *testing.T) {
			empty := models.PriceRule{Attribute: "k", Operator: op, Values: []string{}}
			assert.False(t, matchRule(empty, map[string]string{"k": "10"}),
				"değersiz kural eşleşmemeli")

			nilValues := models.PriceRule{Attribute: "k", Operator: op}
			assert.False(t, matchRule(nilValues, map[string]string{"k": "10"}),
				"nil değerli kural eşleşmemeli")
		})
	}
}

// TestSelectSkipsPriceWithValuelessRule değersiz kuralı olan fiyatın seçime hiç
// girmediğini ve hesabı düşürmediğini kanıtlar.
//
// Değersiz kurallı aday DAHA UCUZ ve DAHA BELİRGİNDİR; elenmezse kazanırdı.
func TestSelectSkipsPriceWithValuelessRule(t *testing.T) {
	broken := withRules(basePrice("price_a", "TRY", 1, 1, nil),
		models.PriceRule{Attribute: "region_id", Operator: models.OpEq})
	base := basePrice("price_b", "TRY", 10000, 1, nil)

	got, ok := selectPrice([]models.PriceCandidate{broken, base}, "TRY", 1,
		map[string]string{"region_id": "reg_1"}, testNow)

	require.True(t, ok)
	assert.Equal(t, "price_b", got.PriceID, "değersiz kurallı fiyat elenmeli")
}
