package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestCreatePriceSetValidatesBeforeWriting geçersiz bir fiyat verildiğinde
// KABIN HİÇ OLUŞTURULMADIĞINI kanıtlar.
//
// Bu, "önce yarat sonra doğrula" sırasının bıraktığı yetim kayıtları engelleyen
// davranıştır; sıra tersine dönerse test düşer.
func TestCreatePriceSetValidatesBeforeWriting(t *testing.T) {
	repo := newStubRepo()
	repo.createPriceSetFn = func(_ context.Context, id string, now time.Time) (models.PriceSet, error) {
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	_, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "XX", Amount: 200},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["CreatePriceSet"], "geçersiz fiyat varken kap oluşturulmamalı")
	assert.Zero(t, repo.calls["ReplacePrices"])
}

// TestCreatePriceSetReportsFailingIndex hangi fiyatın reddedildiğinin hatada
// bildirildiğini kanıtlar.
func TestCreatePriceSetReportsFailingIndex(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).CreatePriceSet(context.Background(), []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "USD", Amount: -1},
	})

	require.Error(t, err)
	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 1, typed.Details["index"])
}

// TestCreatePriceSetWithoutPricesSkipsWrite fiyat verilmediğinde yazma
// yolunun hiç açılmadığını kanıtlar.
func TestCreatePriceSetWithoutPricesSkipsWrite(t *testing.T) {
	repo := newStubRepo()
	repo.createPriceSetFn = func(_ context.Context, id string, now time.Time) (models.PriceSet, error) {
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	set, err := newTestService(repo).CreatePriceSet(context.Background(), nil)

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(set.ID, models.PriceSetIDPrefix))
	assert.Equal(t, 1, repo.calls["CreatePriceSet"])
	assert.Zero(t, repo.calls["ReplacePrices"], "fiyat yoksa yazma yapılmamalı")
}

// TestSetPricesNormalizesInput girdinin depoya normalleştirilmiş hâlde
// geçtiğini kanıtlar: para birimi büyük harf, asgari adet varsayılanı 1,
// kimlikler önekli.
func TestSetPricesNormalizesInput(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", []PriceInput{{
		CurrencyCode: "try",
		Amount:       1999,
		Rules:        []RuleInput{{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}}},
	}})

	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.Equal(t, "TRY", written[0].CurrencyCode)
	assert.Equal(t, int32(1), written[0].MinQuantity)
	assert.Nil(t, written[0].MaxQuantity)
	assert.True(t, strings.HasPrefix(written[0].ID, models.PriceIDPrefix))

	require.Len(t, written[0].Rules, 1)
	assert.True(t, strings.HasPrefix(written[0].Rules[0].ID, models.PriceRuleIDPrefix))
	assert.Equal(t, written[0].ID, written[0].Rules[0].PriceID,
		"kural, ait olduğu fiyata bağlanmalı")
}

// TestSetPricesRejectsInvalidBeforeWriting geçersiz girdide depoya HİÇ
// gidilmediğini kanıtlar; atomikliğin uygulama tarafındaki yarısı budur.
func TestSetPricesRejectsInvalidBeforeWriting(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", []PriceInput{
		{CurrencyCode: "TRY", Amount: 100},
		{CurrencyCode: "TRY", Amount: models.MaxAmount + 1},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.calls["ReplacePrices"], "geçersiz girdi depoya gitmemeli")
}

// TestSetPricesAcceptsEmptySlice boş dilimin "tüm fiyatları kaldır" anlamına
// geldiğini kanıtlar.
func TestSetPricesAcceptsEmptySlice(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	_, err := newTestService(repo).SetPrices(context.Background(), "pset_1", nil)

	require.NoError(t, err)
	assert.Empty(t, written)
	assert.Equal(t, 1, repo.calls["ReplacePrices"])
}

// TestSetPricesRejectsWrongIDPrefix yanlış türde bir kimliğin doğrulama
// hatasıyla (bulunamadı değil) döndüğünü kanıtlar.
func TestSetPricesRejectsWrongIDPrefix(t *testing.T) {
	repo := newStubRepo()

	_, err := newTestService(repo).SetPrices(context.Background(), "variant_1", nil)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, repo.calls)
}

// TestListPricesChecksSetExists olmayan bir kabın fiyat listesinin boş dilim
// değil NotFound döndüğünü kanıtlar.
func TestListPricesChecksSetExists(t *testing.T) {
	repo := newStubRepo()
	repo.getPriceSetFn = func(context.Context, string) (models.PriceSet, error) {
		return models.PriceSet{}, errors.NotFound("price_set_not_found", "yok")
	}

	_, err := newTestService(repo).ListPrices(context.Background(), "pset_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListPrices"], "kap yoksa fiyat sorgusu açılmamalı")
}

// TestListPriceSetsReportsAppliedPaging zarfa yazılan limit/offset'in
// UYGULANAN değerler olduğunu kanıtlar.
func TestListPriceSetsReportsAppliedPaging(t *testing.T) {
	var gotLimit, gotOffset int32
	repo := newStubRepo()
	repo.listPriceSetsFn = func(_ context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
		gotLimit, gotOffset = limit, offset
		return []models.PriceSet{{ID: "pset_1"}}, 42, nil
	}

	page, err := newTestService(repo).ListPriceSets(context.Background(), MaxLimit+50, 10)

	require.NoError(t, err)
	assert.Equal(t, MaxLimit, gotLimit, "depoya kırpılmış limit gitmeli")
	assert.Equal(t, int32(10), gotOffset)
	assert.Equal(t, MaxLimit, page.Limit, "zarf uygulanan limiti bildirmeli")
	assert.Equal(t, int32(10), page.Offset)
	assert.Equal(t, int64(42), page.Count)
	assert.Len(t, page.Items, 1)
}

// TestCreatePriceListDefaultsToDraft durum verilmediğinde listenin YAYINA
// alınmadığını kanıtlar.
func TestCreatePriceListDefaultsToDraft(t *testing.T) {
	var written models.PriceList
	repo := newStubRepo()
	repo.createPriceListFn = func(_ context.Context, list models.PriceList, _ time.Time) (models.PriceList, error) {
		written = list
		return list, nil
	}

	_, err := newTestService(repo).CreatePriceList(context.Background(), PriceListInput{
		Title: "Yaz kampanyası",
		Type:  models.PriceListSale,
	})

	require.NoError(t, err)
	assert.Equal(t, models.PriceListDraft, written.Status)
	assert.True(t, strings.HasPrefix(written.ID, models.PriceListIDPrefix))
}

// TestCreatePriceListValidation fiyat listesi doğrulamasının her dalını
// kanıtlar.
func TestCreatePriceListValidation(t *testing.T) {
	early := testNow
	late := testNow.Add(time.Hour)

	for _, tc := range []struct {
		name string
		in   PriceListInput
		ok   bool
	}{
		{"geçerli", PriceListInput{Title: "K", Type: models.PriceListSale}, true},
		{"pencere sıralı", PriceListInput{
			Title: "K", Type: models.PriceListOverride, StartsAt: &early, EndsAt: &late}, true},
		{"başlık boş", PriceListInput{Title: "   ", Type: models.PriceListSale}, false},
		{"tür tanımsız", PriceListInput{Title: "K", Type: models.PriceListType("bogus")}, false},
		{"tür boş", PriceListInput{Title: "K"}, false},
		{"durum tanımsız", PriceListInput{
			Title: "K", Type: models.PriceListSale, Status: models.PriceListStatus("bogus")}, false},
		{"pencere ters", PriceListInput{
			Title: "K", Type: models.PriceListSale, StartsAt: &late, EndsAt: &early}, false},
		{"pencere uçları aynı", PriceListInput{
			Title: "K", Type: models.PriceListSale, StartsAt: &early, EndsAt: &early}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubRepo()
			repo.createPriceListFn = func(_ context.Context, list models.PriceList, _ time.Time) (models.PriceList, error) {
				return list, nil
			}

			_, err := newTestService(repo).CreatePriceList(context.Background(), tc.in)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["CreatePriceList"])
		})
	}
}

// TestListPriceRulesChecksPriceExists olmayan bir fiyatın kurallarının boş
// dilim değil NotFound döndüğünü kanıtlar.
func TestListPriceRulesChecksPriceExists(t *testing.T) {
	repo := newStubRepo()
	repo.getPriceFn = func(context.Context, string) (models.Price, error) {
		return models.Price{}, errors.NotFound("price_not_found", "yok")
	}

	_, err := newTestService(repo).ListPriceRules(context.Background(), "price_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListPriceRules"])
}

// TestServiceRejectsWrongIDPrefixes her uç noktanın kendi kimlik önekini
// beklediğini kanıtlar.
func TestServiceRejectsWrongIDPrefixes(t *testing.T) {
	ctx := context.Background()
	repo := newStubRepo()
	svc := newTestService(repo)

	for name, call := range map[string]func() error{
		"GetPriceSet":     func() error { _, err := svc.GetPriceSet(ctx, "price_1"); return err },
		"DeletePriceSet":  func() error { return svc.DeletePriceSet(ctx, "plist_1") },
		"GetPriceList":    func() error { _, err := svc.GetPriceList(ctx, "pset_1"); return err },
		"DeletePriceList": func() error { return svc.DeletePriceList(ctx, "pset_1") },
		"GetPriceRule":    func() error { _, err := svc.GetPriceRule(ctx, "price_1"); return err },
		"DeletePriceRule": func() error { return svc.DeletePriceRule(ctx, "price_1") },
		"CreatePriceRule": func() error {
			_, err := svc.CreatePriceRule(ctx, "pset_1",
				RuleInput{Attribute: "k", Operator: models.OpEq, Values: []string{"v"}})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
	assert.Empty(t, repo.calls, "önek hataları depoya hiç gitmemeli")
}

// TestUnconfiguredServiceFailsTyped deposuz servisin panik değil tipli hata
// döndüğünü kanıtlar.
func TestUnconfiguredServiceFailsTyped(t *testing.T) {
	svc := New(nil, Options{})

	_, err := svc.GetPriceSet(context.Background(), "pset_1")

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}

// TestSetBasePricesIsDeterministic modüller arası yüzeyin para birimlerini
// SIRALI yazdığını kanıtlar; harita dolaşım sırası rastgeledir.
func TestSetBasePricesIsDeterministic(t *testing.T) {
	var seen []string
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		seen = nil
		for i := range prices {
			seen = append(seen, prices[i].CurrencyCode)
		}
		return prices, nil
	}
	svc := newTestService(repo)

	amounts := map[string]int64{"usd": 500, "try": 19900, "eur": 450, "gbp": 400}
	for range 5 {
		require.NoError(t, svc.SetBasePrices(context.Background(), "pset_1", amounts))
		assert.Equal(t, []string{"EUR", "GBP", "TRY", "USD"}, seen)
	}
}

// TestSetBasePricesWritesBasePrices yazılan fiyatların listesiz ve kuralsız
// olduğunu kanıtlar; "taban" tanımı budur.
func TestSetBasePricesWritesBasePrices(t *testing.T) {
	var written []models.Price
	repo := newStubRepo()
	repo.replacePricesFn = func(
		_ context.Context, _ string, prices []models.Price, _ time.Time,
	) ([]models.Price, error) {
		written = prices
		return prices, nil
	}

	err := newTestService(repo).SetBasePrices(context.Background(), "pset_1",
		map[string]int64{"TRY": 19900})

	require.NoError(t, err)
	require.Len(t, written, 1)
	assert.Nil(t, written[0].PriceListID, "taban fiyat listeye bağlanmamalı")
	assert.Empty(t, written[0].Rules, "taban fiyat koşulsuz olmalı")
	assert.Equal(t, int64(19900), written[0].Amount)
}

// TestCreateEmptyPriceSetReturnsID modüller arası yüzeyin yalnızca kimlik
// döndüğünü kanıtlar.
func TestCreateEmptyPriceSetReturnsID(t *testing.T) {
	repo := newStubRepo()
	repo.createPriceSetFn = func(_ context.Context, id string, now time.Time) (models.PriceSet, error) {
		return models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}, nil
	}

	id, err := newTestService(repo).CreateEmptyPriceSet(context.Background())

	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, models.PriceSetIDPrefix))
	assert.Zero(t, repo.calls["ReplacePrices"])
}
