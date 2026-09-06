package api_test

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// memRepo service.Repository'nin API testleri için bellekte çalışan
// uygulamasıdır.
//
// Servis paketindeki taklidin ikizidir ve bu tekrar bilinçlidir: API testleri
// dış paketten (api_test) koşar ve servis paketinin test dosyalarını göremez.
// Alternatif, taklidi üretim koduna taşımak olurdu — test yardımcısının
// dağıtılan ikiliye girmesi bu tekrardan daha pahalıdır.
//
// Buradaki iş kuralları BİLİNÇLİ olarak sadedir: API testinin sınadığı şey
// yönlendirme, zarf, status kodu ve MÜŞTERİ YÜZEYİNİN SIZDIRMAMASIDIR; hesap
// aritmetiği servis paketinde sınanır.
type memRepo struct {
	campaigns   map[string]models.Campaign
	promotions  map[string]models.Promotion
	methods     map[string]models.ApplicationMethod
	rules       map[string][]models.PromotionRule
	redemptions []models.Redemption
}

var _ service.Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek deposu üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		campaigns:  map[string]models.Campaign{},
		promotions: map[string]models.Promotion{},
		methods:    map[string]models.ApplicationMethod{},
		rules:      map[string][]models.PromotionRule{},
	}
}

// notFound tipli "bulunamadı" hatası üretir.
func notFound(what, id string) error {
	return errors.NotFound("promotion_test_not_found", "%s bulunamadı: %s", what, id)
}

func (m *memRepo) CreateCampaign(_ context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	for id := range m.campaigns {
		if m.campaigns[id].CampaignIdentifier == c.CampaignIdentifier {
			return models.Campaign{}, errors.Conflict("promotion_test_duplicate",
				"kampanya iş kimliği zaten var: %s", c.CampaignIdentifier)
		}
	}
	c.CreatedAt, c.UpdatedAt = now, now
	m.campaigns[c.ID] = c
	return c, nil
}

func (m *memRepo) GetCampaign(_ context.Context, id string) (models.Campaign, error) {
	c, ok := m.campaigns[id]
	if !ok {
		return models.Campaign{}, notFound("kampanya", id)
	}
	return c, nil
}

func (m *memRepo) GetCampaignByIdentifier(_ context.Context, identifier string) (models.Campaign, error) {
	for id := range m.campaigns {
		if m.campaigns[id].CampaignIdentifier == identifier {
			return m.campaigns[id], nil
		}
	}
	return models.Campaign{}, notFound("kampanya", identifier)
}

func (m *memRepo) ListCampaigns(_ context.Context, limit, offset int32) ([]models.Campaign, int64, error) {
	all := make([]models.Campaign, 0, len(m.campaigns))
	for id := range m.campaigns {
		all = append(all, m.campaigns[id])
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return pageOf(all, limit, offset), int64(len(all)), nil
}

func (m *memRepo) GetCampaignsByIDs(_ context.Context, ids []string) ([]models.Campaign, error) {
	out := make([]models.Campaign, 0, len(ids))
	for _, id := range ids {
		if c, ok := m.campaigns[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memRepo) UpdateCampaign(_ context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	existing, ok := m.campaigns[c.ID]
	if !ok {
		return models.Campaign{}, notFound("kampanya", c.ID)
	}
	c.BudgetUsed = existing.BudgetUsed
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = now
	m.campaigns[c.ID] = c
	return c, nil
}

func (m *memRepo) DeleteCampaign(_ context.Context, id string, _ time.Time) error {
	if _, ok := m.campaigns[id]; !ok {
		return notFound("kampanya", id)
	}
	delete(m.campaigns, id)
	return nil
}

func (m *memRepo) CreatePromotion(_ context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	for id := range m.promotions {
		if m.promotions[id].Code == p.Code {
			return models.Promotion{}, errors.Conflict("promotion_test_duplicate",
				"kupon kodu zaten var: %s", p.Code)
		}
	}
	p.UsageCount = 0
	p.CreatedAt, p.UpdatedAt = now, now
	m.promotions[p.ID] = p
	return p, nil
}

func (m *memRepo) GetPromotion(_ context.Context, id string) (models.Promotion, error) {
	p, ok := m.promotions[id]
	if !ok {
		return models.Promotion{}, notFound("promosyon", id)
	}
	return p, nil
}

func (m *memRepo) GetPromotionByCode(_ context.Context, code string) (models.Promotion, error) {
	for id := range m.promotions {
		if m.promotions[id].Code == code {
			return m.promotions[id], nil
		}
	}
	return models.Promotion{}, notFound("promosyon", code)
}

func (m *memRepo) ListPromotions(
	_ context.Context,
	status, campaignID *string,
	limit, offset int32,
) ([]models.Promotion, int64, error) {
	all := make([]models.Promotion, 0, len(m.promotions))
	for id := range m.promotions {
		p := m.promotions[id]
		if status != nil && string(p.Status) != *status {
			continue
		}
		if campaignID != nil && (p.CampaignID == nil || *p.CampaignID != *campaignID) {
			continue
		}
		all = append(all, p)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return pageOf(all, limit, offset), int64(len(all)), nil
}

func (m *memRepo) GetPromotionsByIDs(_ context.Context, ids []string) ([]models.Promotion, error) {
	out := make([]models.Promotion, 0, len(ids))
	for _, id := range ids {
		if p, ok := m.promotions[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memRepo) UpdatePromotion(_ context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	existing, ok := m.promotions[p.ID]
	if !ok {
		return models.Promotion{}, notFound("promosyon", p.ID)
	}
	p.UsageCount = existing.UsageCount
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = now
	m.promotions[p.ID] = p
	return p, nil
}

func (m *memRepo) DeletePromotion(_ context.Context, id string, _ time.Time) error {
	if _, ok := m.promotions[id]; !ok {
		return notFound("promosyon", id)
	}
	delete(m.promotions, id)
	return nil
}

func (m *memRepo) ListCandidates(_ context.Context, codes []string) ([]models.PromotionCandidate, error) {
	out := make([]models.PromotionCandidate, 0, len(m.promotions))
	for id := range m.promotions {
		p := m.promotions[id]
		if p.Status != models.PromotionActive {
			continue
		}
		if !p.IsAutomatic && !slices.Contains(codes, p.Code) {
			continue
		}
		candidate := models.PromotionCandidate{Promotion: p, Rules: slices.Clone(m.rules[p.ID])}
		if method, ok := m.methods[p.ID]; ok {
			candidate.Method = &method
		}
		if p.CampaignID != nil {
			if campaign, ok := m.campaigns[*p.CampaignID]; ok {
				candidate.Campaign = &campaign
			}
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Promotion.ID < out[j].Promotion.ID })
	return out, nil
}

// canliPromosyon promosyonun altına satır yazan taklit metotların ortak
// denetimidir.
//
// Taklit bunu taşımak ZORUNDADIR: gerçek depo yazmayı promosyon satırı
// PAYLAŞIMLI kilit altındayken ve aynı işlemde yapar (bkz.
// repository.CreatePromotionRule). Denetimsiz bir taklit, silinmiş ya da hiç
// var olmayan bir promosyona yöntem yazılmasını KABUL eder ve API katmanının
// 404 döndüğünü sanan test yeşil kalırdı.
func (m *memRepo) canliPromosyon(id string) error {
	if _, ok := m.promotions[id]; !ok {
		return notFound("promosyon", id)
	}
	return nil
}

func (m *memRepo) SetApplicationMethod(
	_ context.Context,
	method models.ApplicationMethod,
	now time.Time,
) (models.ApplicationMethod, error) {
	if err := m.canliPromosyon(method.PromotionID); err != nil {
		return models.ApplicationMethod{}, err
	}
	method.CreatedAt, method.UpdatedAt = now, now
	m.methods[method.PromotionID] = method
	return method, nil
}

func (m *memRepo) GetApplicationMethod(_ context.Context, promotionID string) (models.ApplicationMethod, error) {
	method, ok := m.methods[promotionID]
	if !ok {
		return models.ApplicationMethod{}, notFound("uygulama yöntemi", promotionID)
	}
	return method, nil
}

func (m *memRepo) DeleteApplicationMethod(_ context.Context, promotionID string, _ time.Time) error {
	if _, ok := m.methods[promotionID]; !ok {
		return notFound("uygulama yöntemi", promotionID)
	}
	delete(m.methods, promotionID)
	return nil
}

func (m *memRepo) CreatePromotionRule(
	_ context.Context,
	rule models.PromotionRule,
	now time.Time,
) (models.PromotionRule, error) {
	if err := m.canliPromosyon(rule.PromotionID); err != nil {
		return models.PromotionRule{}, err
	}
	rule.CreatedAt, rule.UpdatedAt = now, now
	m.rules[rule.PromotionID] = append(m.rules[rule.PromotionID], rule)
	return rule, nil
}

func (m *memRepo) GetPromotionRule(_ context.Context, id string) (models.PromotionRule, error) {
	for _, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				return rules[i], nil
			}
		}
	}
	return models.PromotionRule{}, notFound("promosyon kuralı", id)
}

func (m *memRepo) ListPromotionRules(_ context.Context, promotionID string) ([]models.PromotionRule, error) {
	return slices.Clone(m.rules[promotionID]), nil
}

func (m *memRepo) DeletePromotionRule(_ context.Context, id string, _ time.Time) error {
	for promotionID, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				m.rules[promotionID] = slices.Delete(rules, i, i+1)
				return nil
			}
		}
	}
	return notFound("promosyon kuralı", id)
}

func (m *memRepo) Redeem(_ context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error) {
	promo, ok := m.promotions[req.PromotionID]
	if !ok {
		return models.Redemption{}, false, notFound("promosyon", req.PromotionID)
	}
	for i := range m.redemptions {
		existing := m.redemptions[i]
		if existing.PromotionID == req.PromotionID && existing.Reference == req.Reference && !existing.Released() {
			return existing, false, nil
		}
	}
	if promo.UsageLimit != nil && promo.UsageCount+1 > *promo.UsageLimit {
		return models.Redemption{}, false, errors.Conflict("promotion_test_usage_limit",
			"promosyonun kullanım hakkı bitti: %s", promo.ID)
	}
	promo.UsageCount++
	m.promotions[promo.ID] = promo

	redemption := models.Redemption{
		ID:           req.ID,
		PromotionID:  req.PromotionID,
		Reference:    req.Reference,
		Amount:       req.Amount,
		CurrencyCode: req.CurrencyCode,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	m.redemptions = append(m.redemptions, redemption)
	return redemption, true, nil
}

func (m *memRepo) Release(
	_ context.Context,
	promotionID, reference string,
	now time.Time,
) (models.Redemption, bool, error) {
	promo, ok := m.promotions[promotionID]
	if !ok {
		return models.Redemption{}, false, notFound("promosyon", promotionID)
	}
	for i := range m.redemptions {
		redemption := m.redemptions[i]
		if redemption.PromotionID != promotionID || redemption.Reference != reference || redemption.Released() {
			continue
		}
		promo.UsageCount = max(promo.UsageCount-1, 0)
		m.promotions[promo.ID] = promo

		released := now
		redemption.ReleasedAt = &released
		m.redemptions[i] = redemption
		return redemption, true, nil
	}
	return models.Redemption{}, false, nil
}

func (m *memRepo) GetRedemption(_ context.Context, promotionID, reference string) (models.Redemption, error) {
	for i := range m.redemptions {
		r := m.redemptions[i]
		if r.PromotionID == promotionID && r.Reference == reference && !r.Released() {
			return r, nil
		}
	}
	return models.Redemption{}, notFound("kullanım kaydı", reference)
}

func (m *memRepo) ListRedemptions(
	_ context.Context,
	promotionID string,
	limit, offset int32,
) ([]models.Redemption, int64, error) {
	all := make([]models.Redemption, 0, len(m.redemptions))
	for i := range m.redemptions {
		if m.redemptions[i].PromotionID == promotionID {
			all = append(all, m.redemptions[i])
		}
	}
	return pageOf(all, limit, offset), int64(len(all)), nil
}

// pageOf bir dilimden sayfa keser.
func pageOf[T any](all []T, limit, offset int32) []T {
	if offset >= int32(len(all)) {
		return []T{}
	}
	end := offset + limit
	if end > int32(len(all)) {
		end = int32(len(all))
	}
	return slices.Clone(all[offset:end])
}
