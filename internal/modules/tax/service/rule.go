package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// CreateRateRuleInput yeni bir vergi kuralının yazma girdisidir.
type CreateRateRuleInput struct {
	// TaxRateID kuralın bağlanacağı orandır; zorunludur.
	TaxRateID string
	// Reference kalemin türüdür: "product", "product_type" ya da
	// "shipping_option".
	Reference string
	// ReferenceID o türdeki kimliktir; zorunludur.
	ReferenceID string
}

// CreateRateRule bir orana kural ekler.
//
// Kural, oranın KAPSAMINI daraltır: kurallı bir oran yalnızca kuralıyla
// eşleşen kaleme uygulanır. Bu yüzden VARSAYILAN bir orana kural EKLENEMEZ
// (errors.Conflict) — "her şeye uygulanan oran" ile "yalnızca eşleşene
// uygulanan oran" aynı satırda birleşseydi oranın kapsamı okunamaz hâle
// gelirdi. Denetim depo katmanında, oranın satır KİLİDİ altında yapılır.
//
// # ReferenceID doğrulanmaz
//
// Kimlik başka modüllere (product, fulfillment) aittir ve bu modül onları
// tanımaz (ADR 0001, Prensip 2.2). Var olmayan bir kimliğe yazılmış kural
// zararsızdır: o kimlikle hesaba giren bir kalem hiç gelmez, dolayısıyla kural
// hiç eşleşmez.
func (s *Service) CreateRateRule(ctx context.Context, in CreateRateRuleInput) (models.TaxRateRule, error) {
	if err := s.ready(); err != nil {
		return models.TaxRateRule{}, err
	}
	if err := requireID(in.TaxRateID, models.TaxRateIDPrefix, "vergi oranı kimliği"); err != nil {
		return models.TaxRateRule{}, err
	}

	reference := models.RuleReference(in.Reference)
	if !reference.Valid() {
		return models.TaxRateRule{}, errors.Invalid(CodeInvalidInput,
			"kural referansı %q, %q ya da %q olmalı; %q verildi",
			models.ReferenceProduct, models.ReferenceProductType,
			models.ReferenceShippingOption, in.Reference)
	}
	if err := requireReferenceID(in.ReferenceID); err != nil {
		return models.TaxRateRule{}, err
	}

	now := s.clock()
	return s.repo.CreateTaxRateRule(ctx, models.TaxRateRule{
		ID:          models.NewTaxRateRuleID(now),
		TaxRateID:   in.TaxRateID,
		Reference:   reference,
		ReferenceID: in.ReferenceID,
	}, now)
}

// ListRateRules bir oranın kurallarını döner.
func (s *Service) ListRateRules(ctx context.Context, rateID string) ([]models.TaxRateRule, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(rateID, models.TaxRateIDPrefix, "vergi oranı kimliği"); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetTaxRate(ctx, rateID); err != nil {
		return nil, err
	}
	return s.repo.ListTaxRateRules(ctx, rateID)
}

// DeleteRateRule kuralı yumuşak siler; yoksa errors.NotFound.
//
// Kuralları kalmayan bir oran, hiçbir kalemle eşleşmez ve fiilen ölür; VARSAYILAN
// hâline GELMEZ. Bu bilinçlidir — son kuralın silinmesiyle bir oranın sepetteki
// her kaleme uygulanmaya başlaması, sessiz ve geri alınması pahalı bir
// değişiklik olurdu.
func (s *Service) DeleteRateRule(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.TaxRateRuleIDPrefix, "vergi kuralı kimliği"); err != nil {
		return err
	}
	return s.repo.DeleteTaxRateRule(ctx, id, s.clock())
}
