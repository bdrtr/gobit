package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// stubRepo [Repository]'nin testler için betiklenebilir uygulamasıdır.
//
// Her metot bir işlev alanına delege eder; alan doldurulmamışsa test o metodun
// çağrılmasını beklemiyordur ve çağrı tipli bir hata döner. Sessiz sıfır değer
// dönmek, testin yanlış nedenle geçmesine yol açardı.
type stubRepo struct {
	createPriceSetFn       func(ctx context.Context, id string, prices []models.Price, now time.Time) (models.PriceSet, error)
	getPriceSetFn          func(ctx context.Context, id string) (models.PriceSet, error)
	listPriceSetsFn        func(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error)
	getPriceSetsByIDsFn    func(ctx context.Context, ids []string) ([]models.PriceSet, error)
	deletePriceSetFn       func(ctx context.Context, id string, now time.Time) error
	listPricesFn           func(ctx context.Context, priceSetID string) ([]models.Price, error)
	listCandidatesBySetsFn func(ctx context.Context, ids []string) (map[string][]models.PriceCandidate, error)
	listCandidatesFn       func(ctx context.Context, priceSetID string) ([]models.PriceCandidate, error)
	replacePricesFn        func(ctx context.Context, priceSetID string, prices []models.Price, now time.Time) ([]models.Price, error)
	getPriceFn             func(ctx context.Context, id string) (models.Price, error)
	createPriceRuleFn      func(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error)
	getPriceRuleFn         func(ctx context.Context, id string) (models.PriceRule, error)
	listPriceRulesFn       func(ctx context.Context, priceID string) ([]models.PriceRule, error)
	deletePriceRuleFn      func(ctx context.Context, id string, now time.Time) error
	createPriceListFn      func(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	getPriceListFn         func(ctx context.Context, id string) (models.PriceList, error)
	listPriceListsFn       func(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error)
	updatePriceListFn      func(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error)
	deletePriceListFn      func(ctx context.Context, id string, now time.Time) error

	// calls metot adı -> çağrı sayısıdır; toplu (batch) davranışın kanıtı budur.
	calls map[string]int
}

var _ Repository = (*stubRepo)(nil)

// newStubRepo boş bir sahte depo üretir.
func newStubRepo() *stubRepo {
	return &stubRepo{calls: map[string]int{}}
}

// record bir çağrıyı sayar.
func (s *stubRepo) record(name string) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[name]++
}

// unset betiklenmemiş bir metot çağrıldığında dönen hatadır.
func unset(name string) error {
	return errors.Internal("stub_unset", "%s testte betiklenmedi", name)
}

func (s *stubRepo) CreatePriceSet(
	ctx context.Context,
	id string,
	prices []models.Price,
	now time.Time,
) (models.PriceSet, error) {
	s.record("CreatePriceSet")
	if s.createPriceSetFn == nil {
		return models.PriceSet{}, unset("CreatePriceSet")
	}
	return s.createPriceSetFn(ctx, id, prices, now)
}

func (s *stubRepo) GetPriceSet(ctx context.Context, id string) (models.PriceSet, error) {
	s.record("GetPriceSet")
	if s.getPriceSetFn == nil {
		return models.PriceSet{}, unset("GetPriceSet")
	}
	return s.getPriceSetFn(ctx, id)
}

func (s *stubRepo) ListPriceSets(ctx context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
	s.record("ListPriceSets")
	if s.listPriceSetsFn == nil {
		return nil, 0, unset("ListPriceSets")
	}
	return s.listPriceSetsFn(ctx, limit, offset)
}

func (s *stubRepo) GetPriceSetsByIDs(ctx context.Context, ids []string) ([]models.PriceSet, error) {
	s.record("GetPriceSetsByIDs")
	if s.getPriceSetsByIDsFn == nil {
		return nil, unset("GetPriceSetsByIDs")
	}
	return s.getPriceSetsByIDsFn(ctx, ids)
}

func (s *stubRepo) DeletePriceSet(ctx context.Context, id string, now time.Time) error {
	s.record("DeletePriceSet")
	if s.deletePriceSetFn == nil {
		return unset("DeletePriceSet")
	}
	return s.deletePriceSetFn(ctx, id, now)
}

func (s *stubRepo) ListPrices(ctx context.Context, priceSetID string) ([]models.Price, error) {
	s.record("ListPrices")
	if s.listPricesFn == nil {
		return nil, unset("ListPrices")
	}
	return s.listPricesFn(ctx, priceSetID)
}

func (s *stubRepo) ListPriceCandidatesBySets(
	ctx context.Context,
	priceSetIDs []string,
) (map[string][]models.PriceCandidate, error) {
	s.record("ListPriceCandidatesBySets")
	if s.listCandidatesBySetsFn == nil {
		return nil, unset("ListPriceCandidatesBySets")
	}
	return s.listCandidatesBySetsFn(ctx, priceSetIDs)
}

func (s *stubRepo) ListPriceCandidates(ctx context.Context, priceSetID string) ([]models.PriceCandidate, error) {
	s.record("ListPriceCandidates")
	if s.listCandidatesFn == nil {
		return nil, unset("ListPriceCandidates")
	}
	return s.listCandidatesFn(ctx, priceSetID)
}

func (s *stubRepo) ReplacePrices(
	ctx context.Context,
	priceSetID string,
	prices []models.Price,
	now time.Time,
) ([]models.Price, error) {
	s.record("ReplacePrices")
	if s.replacePricesFn == nil {
		return nil, unset("ReplacePrices")
	}
	return s.replacePricesFn(ctx, priceSetID, prices, now)
}

func (s *stubRepo) GetPrice(ctx context.Context, id string) (models.Price, error) {
	s.record("GetPrice")
	if s.getPriceFn == nil {
		return models.Price{}, unset("GetPrice")
	}
	return s.getPriceFn(ctx, id)
}

func (s *stubRepo) CreatePriceRule(ctx context.Context, rule models.PriceRule, now time.Time) (models.PriceRule, error) {
	s.record("CreatePriceRule")
	if s.createPriceRuleFn == nil {
		return models.PriceRule{}, unset("CreatePriceRule")
	}
	return s.createPriceRuleFn(ctx, rule, now)
}

func (s *stubRepo) GetPriceRule(ctx context.Context, id string) (models.PriceRule, error) {
	s.record("GetPriceRule")
	if s.getPriceRuleFn == nil {
		return models.PriceRule{}, unset("GetPriceRule")
	}
	return s.getPriceRuleFn(ctx, id)
}

func (s *stubRepo) ListPriceRules(ctx context.Context, priceID string) ([]models.PriceRule, error) {
	s.record("ListPriceRules")
	if s.listPriceRulesFn == nil {
		return nil, unset("ListPriceRules")
	}
	return s.listPriceRulesFn(ctx, priceID)
}

func (s *stubRepo) DeletePriceRule(ctx context.Context, id string, now time.Time) error {
	s.record("DeletePriceRule")
	if s.deletePriceRuleFn == nil {
		return unset("DeletePriceRule")
	}
	return s.deletePriceRuleFn(ctx, id, now)
}

func (s *stubRepo) CreatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error) {
	s.record("CreatePriceList")
	if s.createPriceListFn == nil {
		return models.PriceList{}, unset("CreatePriceList")
	}
	return s.createPriceListFn(ctx, list, now)
}

func (s *stubRepo) GetPriceList(ctx context.Context, id string) (models.PriceList, error) {
	s.record("GetPriceList")
	if s.getPriceListFn == nil {
		return models.PriceList{}, unset("GetPriceList")
	}
	return s.getPriceListFn(ctx, id)
}

func (s *stubRepo) ListPriceLists(ctx context.Context, limit, offset int32) ([]models.PriceList, int64, error) {
	s.record("ListPriceLists")
	if s.listPriceListsFn == nil {
		return nil, 0, unset("ListPriceLists")
	}
	return s.listPriceListsFn(ctx, limit, offset)
}

func (s *stubRepo) UpdatePriceList(ctx context.Context, list models.PriceList, now time.Time) (models.PriceList, error) {
	s.record("UpdatePriceList")
	if s.updatePriceListFn == nil {
		return models.PriceList{}, unset("UpdatePriceList")
	}
	return s.updatePriceListFn(ctx, list, now)
}

func (s *stubRepo) DeletePriceList(ctx context.Context, id string, now time.Time) error {
	s.record("DeletePriceList")
	if s.deletePriceListFn == nil {
		return unset("DeletePriceList")
	}
	return s.deletePriceListFn(ctx, id, now)
}

// --- test yardımcıları ------------------------------------------------------

// testNow testlerin sabit saatidir; zamana bağlı dallar belirlenimci olur.
var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// newTestService sabit saatli bir servis üretir.
func newTestService(repo Repository) *Service {
	return New(repo, Options{Now: func() time.Time { return testNow }})
}

// ptr bir değerin adresini döner; testlerde isteğe bağlı alanlar için.
func ptr[T any](v T) *T { return &v }

// basePrice kuralsız, listesiz bir fiyat adayı üretir.
func basePrice(id, currency string, amount int64, minQty int32, maxQty *int32) models.PriceCandidate {
	return models.PriceCandidate{
		Price: models.Price{
			ID:           id,
			PriceSetID:   "pset_1",
			CurrencyCode: currency,
			Amount:       amount,
			MinQuantity:  minQty,
			MaxQuantity:  maxQty,
		},
	}
}

// withList bir adayı verilen fiyat listesine bağlar.
func withList(c models.PriceCandidate, listID string, info *models.PriceListInfo) models.PriceCandidate {
	c.Price.PriceListID = &listID
	c.List = info
	return c
}

// withRules bir adaya kural ekler.
func withRules(c models.PriceCandidate, rules ...models.PriceRule) models.PriceCandidate {
	c.Price.Rules = append(c.Price.Rules, rules...)
	return c
}

// rule tek bir kural üretir.
func rule(attribute string, op models.RuleOperator, values ...string) models.PriceRule {
	return models.PriceRule{Attribute: attribute, Operator: op, Values: values}
}

// activeList her zaman kullanılabilir bir liste üstverisi üretir.
func activeList(id string, listType models.PriceListType) *models.PriceListInfo {
	return &models.PriceListInfo{ID: id, Type: listType, Status: models.PriceListActive}
}
