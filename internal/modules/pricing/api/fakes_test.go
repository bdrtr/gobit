package api_test

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// memRepo [service.Repository]'nin bellek içi uygulamasıdır.
//
// HTTP katmanı testleri gerçek servisi kullanır; yalnızca depo taklit edilir.
// Böylece doğrulama, hata sınıflandırması ve zarf biçimi uçtan uca sınanır ve
// handler'ların status kodu seçmediği (core/http'nin seçtiği) kanıtlanabilir.
type memRepo struct {
	sets   map[string]models.PriceSet
	prices map[string][]models.Price
	lists  map[string]models.PriceList
	rules  map[string][]models.PriceRule
}

var _ service.Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek içi depo üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		sets:   map[string]models.PriceSet{},
		prices: map[string][]models.Price{},
		lists:  map[string]models.PriceList{},
		rules:  map[string][]models.PriceRule{},
	}
}

func (m *memRepo) CreatePriceSet(_ context.Context, id string, now time.Time) (models.PriceSet, error) {
	set := models.PriceSet{ID: id, CreatedAt: now, UpdatedAt: now}
	m.sets[id] = set
	return set, nil
}

func (m *memRepo) GetPriceSet(_ context.Context, id string) (models.PriceSet, error) {
	set, ok := m.sets[id]
	if !ok {
		return models.PriceSet{}, errors.NotFound("price_set_not_found", "price set bulunamadı: %s", id)
	}
	return set, nil
}

func (m *memRepo) ListPriceSets(_ context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
	ids := slices.Sorted(maps.Keys(m.sets))
	total := int64(len(ids))

	out := []models.PriceSet{}
	for i, id := range ids {
		if int32(i) < offset {
			continue
		}
		if int32(len(out)) >= limit {
			break
		}
		out = append(out, m.sets[id])
	}
	return out, total, nil
}

func (m *memRepo) GetPriceSetsByIDs(_ context.Context, ids []string) ([]models.PriceSet, error) {
	out := []models.PriceSet{}
	for _, id := range ids {
		if set, ok := m.sets[id]; ok {
			out = append(out, set)
		}
	}
	return out, nil
}

func (m *memRepo) DeletePriceSet(_ context.Context, id string, _ time.Time) error {
	if _, ok := m.sets[id]; !ok {
		return errors.NotFound("price_set_not_found", "price set bulunamadı: %s", id)
	}
	delete(m.sets, id)
	delete(m.prices, id)
	return nil
}

func (m *memRepo) ListPrices(_ context.Context, priceSetID string) ([]models.Price, error) {
	return slices.Clone(m.prices[priceSetID]), nil
}

func (m *memRepo) ListPricesBySets(_ context.Context, ids []string) (map[string][]models.Price, error) {
	out := map[string][]models.Price{}
	for _, id := range ids {
		out[id] = slices.Clone(m.prices[id])
	}
	return out, nil
}

func (m *memRepo) ListPriceCandidates(_ context.Context, priceSetID string) ([]models.PriceCandidate, error) {
	out := []models.PriceCandidate{}
	for i := range m.prices[priceSetID] {
		price := m.prices[priceSetID][i]
		candidate := models.PriceCandidate{Price: price}
		if price.PriceListID != nil {
			if list, ok := m.lists[*price.PriceListID]; ok {
				candidate.List = &models.PriceListInfo{
					ID:       list.ID,
					Type:     list.Type,
					Status:   list.Status,
					StartsAt: list.StartsAt,
					EndsAt:   list.EndsAt,
				}
			}
		}
		out = append(out, candidate)
	}
	return out, nil
}

func (m *memRepo) ReplacePrices(
	_ context.Context,
	priceSetID string,
	prices []models.Price,
	now time.Time,
) ([]models.Price, error) {
	if _, ok := m.sets[priceSetID]; !ok {
		return nil, errors.NotFound("price_set_not_found", "price set bulunamadı: %s", priceSetID)
	}

	written := make([]models.Price, 0, len(prices))
	for i := range prices {
		price := prices[i]
		price.PriceSetID = priceSetID
		price.CreatedAt, price.UpdatedAt = now, now
		if price.Rules == nil {
			price.Rules = []models.PriceRule{}
		}
		m.rules[price.ID] = slices.Clone(price.Rules)
		written = append(written, price)
	}
	m.prices[priceSetID] = written
	return slices.Clone(written), nil
}

func (m *memRepo) GetPrice(_ context.Context, id string) (models.Price, error) {
	for _, prices := range m.prices {
		for i := range prices {
			if prices[i].ID == id {
				return prices[i], nil
			}
		}
	}
	return models.Price{}, errors.NotFound("price_not_found", "fiyat bulunamadı: %s", id)
}

func (m *memRepo) CreatePriceRule(
	_ context.Context,
	rule models.PriceRule,
	now time.Time,
) (models.PriceRule, error) {
	rule.CreatedAt, rule.UpdatedAt = now, now
	m.rules[rule.PriceID] = append(m.rules[rule.PriceID], rule)
	return rule, nil
}

func (m *memRepo) GetPriceRule(_ context.Context, id string) (models.PriceRule, error) {
	for _, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				return rules[i], nil
			}
		}
	}
	return models.PriceRule{}, errors.NotFound("price_rule_not_found", "kural bulunamadı: %s", id)
}

func (m *memRepo) ListPriceRules(_ context.Context, priceID string) ([]models.PriceRule, error) {
	return slices.Clone(m.rules[priceID]), nil
}

func (m *memRepo) DeletePriceRule(_ context.Context, id string, _ time.Time) error {
	for priceID, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				m.rules[priceID] = slices.Delete(rules, i, i+1)
				return nil
			}
		}
	}
	return errors.NotFound("price_rule_not_found", "kural bulunamadı: %s", id)
}

func (m *memRepo) CreatePriceList(
	_ context.Context,
	list models.PriceList,
	now time.Time,
) (models.PriceList, error) {
	list.CreatedAt, list.UpdatedAt = now, now
	m.lists[list.ID] = list
	return list, nil
}

func (m *memRepo) GetPriceList(_ context.Context, id string) (models.PriceList, error) {
	list, ok := m.lists[id]
	if !ok {
		return models.PriceList{}, errors.NotFound("price_list_not_found", "fiyat listesi bulunamadı: %s", id)
	}
	return list, nil
}

func (m *memRepo) ListPriceLists(_ context.Context, limit, offset int32) ([]models.PriceList, int64, error) {
	ids := slices.Sorted(maps.Keys(m.lists))
	total := int64(len(ids))

	out := []models.PriceList{}
	for i, id := range ids {
		if int32(i) < offset {
			continue
		}
		if int32(len(out)) >= limit {
			break
		}
		out = append(out, m.lists[id])
	}
	return out, total, nil
}

func (m *memRepo) UpdatePriceList(
	_ context.Context,
	list models.PriceList,
	now time.Time,
) (models.PriceList, error) {
	existing, ok := m.lists[list.ID]
	if !ok {
		return models.PriceList{}, errors.NotFound("price_list_not_found", "fiyat listesi bulunamadı: %s", list.ID)
	}
	list.CreatedAt = existing.CreatedAt
	list.UpdatedAt = now
	m.lists[list.ID] = list
	return list, nil
}

func (m *memRepo) DeletePriceList(_ context.Context, id string, _ time.Time) error {
	if _, ok := m.lists[id]; !ok {
		return errors.NotFound("price_list_not_found", "fiyat listesi bulunamadı: %s", id)
	}
	delete(m.lists, id)
	return nil
}
