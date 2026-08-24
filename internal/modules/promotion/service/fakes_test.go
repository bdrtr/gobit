package service

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// memRepo [Repository]'nin bellekte çalışan uygulamasıdır.
//
// Betiklenebilir bir sahte (stub) yerine GERÇEK davranışlı bir taklit
// seçilmiştir: bu modülün servis testlerinin çoğu birden çok kaydın birlikte
// okunmasına dayanır (aday listesi promosyon + yöntem + kural + kampanya
// getirir) ve her testin bunu tek tek betiklemesi, testleri hesabın kendisinden
// çok kurulumun doğruluğunu sınar hâle getirirdi.
//
// Eşzamanlılık GARANTİLERİ burada sınanmaz — kilitler veritabanındadır ve
// iddia yalnızca entegrasyon testinde kanıtlanabilir. Buradaki mutex sadece
// `-race` altında güvenli koşmak içindir.
type memRepo struct {
	mu          sync.Mutex
	campaigns   map[string]models.Campaign
	promotions  map[string]models.Promotion
	methods     map[string]models.ApplicationMethod
	rules       map[string][]models.PromotionRule
	redemptions []models.Redemption

	// errOn metot adı -> dönecek hata; hata enjeksiyonu için.
	errOn map[string]error
	// calls metot adı -> çağrı sayısıdır; toplu (batch) davranışın kanıtı budur.
	calls map[string]int
}

var _ Repository = (*memRepo)(nil)

// newMemRepo boş bir bellek deposu üretir.
func newMemRepo() *memRepo {
	return &memRepo{
		campaigns:  map[string]models.Campaign{},
		promotions: map[string]models.Promotion{},
		methods:    map[string]models.ApplicationMethod{},
		rules:      map[string][]models.PromotionRule{},
		errOn:      map[string]error{},
		calls:      map[string]int{},
	}
}

// hook çağrıyı sayar ve varsa enjekte edilmiş hatayı döner.
func (m *memRepo) hook(name string) error {
	m.calls[name]++
	return m.errOn[name]
}

func (m *memRepo) CreateCampaign(_ context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("CreateCampaign"); err != nil {
		return models.Campaign{}, err
	}
	for id := range m.campaigns {
		if m.campaigns[id].CampaignIdentifier == c.CampaignIdentifier {
			return models.Campaign{}, errors.Conflict(repository.CodeDuplicate,
				"kampanya iş kimliği zaten var: %s", c.CampaignIdentifier)
		}
	}
	c.CreatedAt, c.UpdatedAt = now, now
	m.campaigns[c.ID] = c
	return c, nil
}

func (m *memRepo) GetCampaign(_ context.Context, id string) (models.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetCampaign"); err != nil {
		return models.Campaign{}, err
	}
	c, ok := m.campaigns[id]
	if !ok {
		return models.Campaign{}, errors.NotFound(repository.CodeCampaignNotFound,
			"kampanya bulunamadı: %s", id)
	}
	return c, nil
}

func (m *memRepo) GetCampaignByIdentifier(_ context.Context, identifier string) (models.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetCampaignByIdentifier"); err != nil {
		return models.Campaign{}, err
	}
	for id := range m.campaigns {
		if m.campaigns[id].CampaignIdentifier == identifier {
			return m.campaigns[id], nil
		}
	}
	return models.Campaign{}, errors.NotFound(repository.CodeCampaignNotFound,
		"kampanya bulunamadı: %s", identifier)
}

func (m *memRepo) ListCampaigns(_ context.Context, limit, offset int32) ([]models.Campaign, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListCampaigns"); err != nil {
		return nil, 0, err
	}
	all := make([]models.Campaign, 0, len(m.campaigns))
	for id := range m.campaigns {
		all = append(all, m.campaigns[id])
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return pageOf(all, limit, offset), int64(len(all)), nil
}

func (m *memRepo) GetCampaignsByIDs(_ context.Context, ids []string) ([]models.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetCampaignsByIDs"); err != nil {
		return nil, err
	}
	out := make([]models.Campaign, 0, len(ids))
	for _, id := range ids {
		if c, ok := m.campaigns[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (m *memRepo) UpdateCampaign(_ context.Context, c models.Campaign, now time.Time) (models.Campaign, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("UpdateCampaign"); err != nil {
		return models.Campaign{}, err
	}
	existing, ok := m.campaigns[c.ID]
	if !ok {
		return models.Campaign{}, errors.NotFound(repository.CodeCampaignNotFound,
			"kampanya bulunamadı: %s", c.ID)
	}
	// Sayaç sıfır değilken bütçenin BİRİMİ dondurulur; gerçek depoda bunu
	// UpdateCampaign sorgusunun WHERE koşulu zorlar.
	if existing.BudgetUsed != 0 &&
		(existing.BudgetType != c.BudgetType || existing.BudgetCurrencyCode != c.BudgetCurrencyCode) {
		return models.Campaign{}, errors.Conflict(repository.CodeBudgetUnitLocked,
			"kampanyanın bütçe sayacı %d; sayaç sıfırlanmadan bütçe türü ya da para birimi değiştirilemez",
			existing.BudgetUsed)
	}
	// Sayaç yönetim yolundan DEĞİŞMEZ; gerçek deponun sorgusu da onu dışarıda
	// bırakır.
	c.BudgetUsed = existing.BudgetUsed
	c.CreatedAt = existing.CreatedAt
	c.UpdatedAt = now
	m.campaigns[c.ID] = c
	return c, nil
}

func (m *memRepo) DeleteCampaign(_ context.Context, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("DeleteCampaign"); err != nil {
		return err
	}
	if _, ok := m.campaigns[id]; !ok {
		return errors.NotFound(repository.CodeCampaignNotFound, "kampanya bulunamadı: %s", id)
	}
	delete(m.campaigns, id)
	return nil
}

func (m *memRepo) CreatePromotion(_ context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("CreatePromotion"); err != nil {
		return models.Promotion{}, err
	}
	for id := range m.promotions {
		if m.promotions[id].Code == p.Code {
			return models.Promotion{}, errors.Conflict(repository.CodeDuplicate,
				"kupon kodu zaten var: %s", p.Code)
		}
	}
	p.UsageCount = 0
	p.CreatedAt, p.UpdatedAt = now, now
	m.promotions[p.ID] = p
	return p, nil
}

func (m *memRepo) GetPromotion(_ context.Context, id string) (models.Promotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetPromotion"); err != nil {
		return models.Promotion{}, err
	}
	p, ok := m.promotions[id]
	if !ok {
		return models.Promotion{}, errors.NotFound(repository.CodePromotionNotFound,
			"promosyon bulunamadı: %s", id)
	}
	return p, nil
}

func (m *memRepo) GetPromotionByCode(_ context.Context, code string) (models.Promotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetPromotionByCode"); err != nil {
		return models.Promotion{}, err
	}
	for id := range m.promotions {
		if m.promotions[id].Code == code {
			return m.promotions[id], nil
		}
	}
	return models.Promotion{}, errors.NotFound(repository.CodePromotionNotFound,
		"promosyon bulunamadı: %s", code)
}

func (m *memRepo) ListPromotions(
	_ context.Context,
	status, campaignID *string,
	limit, offset int32,
) ([]models.Promotion, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListPromotions"); err != nil {
		return nil, 0, err
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetPromotionsByIDs"); err != nil {
		return nil, err
	}
	out := make([]models.Promotion, 0, len(ids))
	for _, id := range ids {
		if p, ok := m.promotions[id]; ok {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memRepo) UpdatePromotion(_ context.Context, p models.Promotion, now time.Time) (models.Promotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("UpdatePromotion"); err != nil {
		return models.Promotion{}, err
	}
	existing, ok := m.promotions[p.ID]
	if !ok {
		return models.Promotion{}, errors.NotFound(repository.CodePromotionNotFound,
			"promosyon bulunamadı: %s", p.ID)
	}
	for id := range m.promotions {
		if id != p.ID && m.promotions[id].Code == p.Code {
			return models.Promotion{}, errors.Conflict(repository.CodeDuplicate,
				"kupon kodu zaten var: %s", p.Code)
		}
	}
	p.UsageCount = existing.UsageCount
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = now
	m.promotions[p.ID] = p
	return p, nil
}

func (m *memRepo) DeletePromotion(_ context.Context, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("DeletePromotion"); err != nil {
		return err
	}
	if _, ok := m.promotions[id]; !ok {
		return errors.NotFound(repository.CodePromotionNotFound, "promosyon bulunamadı: %s", id)
	}
	delete(m.promotions, id)
	return nil
}

func (m *memRepo) ListCandidates(_ context.Context, codes []string) ([]models.PromotionCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListCandidates"); err != nil {
		return nil, err
	}

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

func (m *memRepo) SetApplicationMethod(
	_ context.Context,
	method models.ApplicationMethod,
	now time.Time,
) (models.ApplicationMethod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("SetApplicationMethod"); err != nil {
		return models.ApplicationMethod{}, err
	}
	method.CreatedAt, method.UpdatedAt = now, now
	m.methods[method.PromotionID] = method
	return method, nil
}

func (m *memRepo) GetApplicationMethod(_ context.Context, promotionID string) (models.ApplicationMethod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetApplicationMethod"); err != nil {
		return models.ApplicationMethod{}, err
	}
	method, ok := m.methods[promotionID]
	if !ok {
		return models.ApplicationMethod{}, errors.NotFound(repository.CodeApplicationMethodNotFound,
			"promosyonun uygulama yöntemi yok: %s", promotionID)
	}
	return method, nil
}

func (m *memRepo) DeleteApplicationMethod(_ context.Context, promotionID string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("DeleteApplicationMethod"); err != nil {
		return err
	}
	if _, ok := m.methods[promotionID]; !ok {
		return errors.NotFound(repository.CodeApplicationMethodNotFound,
			"promosyonun uygulama yöntemi yok: %s", promotionID)
	}
	delete(m.methods, promotionID)
	return nil
}

func (m *memRepo) CreatePromotionRule(
	_ context.Context,
	rule models.PromotionRule,
	now time.Time,
) (models.PromotionRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("CreatePromotionRule"); err != nil {
		return models.PromotionRule{}, err
	}
	rule.CreatedAt, rule.UpdatedAt = now, now
	m.rules[rule.PromotionID] = append(m.rules[rule.PromotionID], rule)
	return rule, nil
}

func (m *memRepo) GetPromotionRule(_ context.Context, id string) (models.PromotionRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetPromotionRule"); err != nil {
		return models.PromotionRule{}, err
	}
	for _, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				return rules[i], nil
			}
		}
	}
	return models.PromotionRule{}, errors.NotFound(repository.CodePromotionRuleNotFound,
		"promosyon kuralı bulunamadı: %s", id)
}

func (m *memRepo) ListPromotionRules(_ context.Context, promotionID string) ([]models.PromotionRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListPromotionRules"); err != nil {
		return nil, err
	}
	return slices.Clone(m.rules[promotionID]), nil
}

func (m *memRepo) DeletePromotionRule(_ context.Context, id string, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("DeletePromotionRule"); err != nil {
		return err
	}
	for promotionID, rules := range m.rules {
		for i := range rules {
			if rules[i].ID == id {
				m.rules[promotionID] = slices.Delete(rules, i, i+1)
				return nil
			}
		}
	}
	return errors.NotFound(repository.CodePromotionRuleNotFound,
		"promosyon kuralı bulunamadı: %s", id)
}

func (m *memRepo) Redeem(_ context.Context, req models.Redemption, now time.Time) (models.Redemption, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("Redeem"); err != nil {
		return models.Redemption{}, false, err
	}

	promo, ok := m.promotions[req.PromotionID]
	if !ok {
		return models.Redemption{}, false, errors.NotFound(repository.CodePromotionNotFound,
			"promosyon bulunamadı: %s", req.PromotionID)
	}
	for i := range m.redemptions {
		existing := m.redemptions[i]
		if existing.PromotionID == req.PromotionID && existing.Reference == req.Reference && !existing.Released() {
			return existing, false, nil
		}
	}

	// Uygunluk denetimleri gerçek deponun sırasını KORUR: idempotency önce gelir,
	// durum ve pencere sonra (bkz. repository.Redeem godoc'u).
	if promo.Status != models.PromotionActive {
		return models.Redemption{}, false, errors.Conflict(repository.CodePromotionNotActive,
			"promosyon yayında değil: %s (durum: %s)", req.PromotionID, promo.Status)
	}

	var (
		delta      int64
		campaignID *string
	)
	if promo.CampaignID != nil {
		campaign, found := m.campaigns[*promo.CampaignID]
		if !found {
			return models.Redemption{}, false, errors.Conflict(repository.CodeCampaignNotFound,
				"kampanya kullanım sırasında kayboldu: %s", *promo.CampaignID)
		}
		if !campaign.WindowContains(now) {
			return models.Redemption{}, false, errors.Conflict(repository.CodeCampaignWindowClosed,
				"kampanyanın tarih penceresi kullanım anını kapsamıyor: %s", campaign.ID)
		}
		if campaign.BudgetType == models.BudgetSpend && campaign.BudgetCurrencyCode != req.CurrencyCode {
			return models.Redemption{}, false, errors.Conflict(repository.CodeBudgetCurrencyMismatch,
				"kampanya bütçesi para birimi uyuşmuyor: %s", campaign.ID)
		}
		delta = campaign.BudgetDeltaFor(req.Amount)
		if campaign.BudgetLimit != nil && campaign.BudgetUsed+delta > *campaign.BudgetLimit {
			return models.Redemption{}, false, errors.Conflict(repository.CodeBudgetExceeded,
				"kampanya bütçesi yetmiyor: %s", campaign.ID)
		}
		campaign.BudgetUsed += delta
		m.campaigns[campaign.ID] = campaign
		id := campaign.ID
		campaignID = &id
	}

	if promo.UsageLimit != nil && promo.UsageCount+1 > *promo.UsageLimit {
		// Bütçe zaten artırılmışsa geri alınır; gerçek depoda bunu işlem yapar.
		if campaignID != nil && delta > 0 {
			campaign := m.campaigns[*campaignID]
			campaign.BudgetUsed -= delta
			m.campaigns[*campaignID] = campaign
		}
		return models.Redemption{}, false, errors.Conflict(repository.CodeUsageLimitReached,
			"promosyonun kullanım hakkı bitti: %s", promo.ID)
	}
	promo.UsageCount++
	m.promotions[promo.ID] = promo

	redemption := models.Redemption{
		ID:           req.ID,
		PromotionID:  req.PromotionID,
		CampaignID:   campaignID,
		Reference:    req.Reference,
		Amount:       req.Amount,
		CurrencyCode: req.CurrencyCode,
		BudgetDelta:  delta,
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("Release"); err != nil {
		return models.Redemption{}, false, err
	}

	promo, ok := m.promotions[promotionID]
	if !ok {
		return models.Redemption{}, false, errors.NotFound(repository.CodePromotionNotFound,
			"promosyon bulunamadı: %s", promotionID)
	}

	for i := range m.redemptions {
		redemption := m.redemptions[i]
		if redemption.PromotionID != promotionID || redemption.Reference != reference || redemption.Released() {
			continue
		}
		if redemption.CampaignID != nil && redemption.BudgetDelta > 0 {
			if campaign, found := m.campaigns[*redemption.CampaignID]; found {
				campaign.BudgetUsed = max(campaign.BudgetUsed-redemption.BudgetDelta, 0)
				m.campaigns[campaign.ID] = campaign
			}
		}
		promo.UsageCount = max(promo.UsageCount-1, 0)
		m.promotions[promo.ID] = promo

		released := now
		redemption.ReleasedAt = &released
		redemption.UpdatedAt = now
		m.redemptions[i] = redemption
		return redemption, true, nil
	}
	return models.Redemption{}, false, nil
}

func (m *memRepo) GetRedemption(_ context.Context, promotionID, reference string) (models.Redemption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("GetRedemption"); err != nil {
		return models.Redemption{}, err
	}
	for i := range m.redemptions {
		r := m.redemptions[i]
		if r.PromotionID == promotionID && r.Reference == reference && !r.Released() {
			return r, nil
		}
	}
	return models.Redemption{}, errors.NotFound(repository.CodePromotionNotFound,
		"kullanım kaydı bulunamadı: %s/%s", promotionID, reference)
}

func (m *memRepo) ListRedemptions(
	_ context.Context,
	promotionID string,
	limit, offset int32,
) ([]models.Redemption, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ListRedemptions"); err != nil {
		return nil, 0, err
	}
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
