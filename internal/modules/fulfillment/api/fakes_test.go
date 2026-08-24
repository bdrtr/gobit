package api_test

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// fakeFulfillments api.Fulfillments'in sınanabilir karşılığıdır.
//
// İş kuralı İÇERMEZ: her metot önceden ayarlanmış sonucu döner ve aldığı
// girdiyi saklar. HTTP katmanının sınanan davranışı budur — gövdeyi doğru
// çözüyor mu, servise doğru girdiyi veriyor mu, hatayı doğru status koda
// çeviriyor mu ve mağaza yanıtına neyi YAZMIYOR.
type fakeFulfillments struct {
	providerIDs []string

	profile     models.ShippingProfile
	profiles    []models.ShippingProfile
	option      models.ShippingOption
	options     []models.ShippingOption
	rule        models.ShippingOptionRule
	rules       []models.ShippingOptionRule
	quoted      []service.QuotedOption
	fulfillment models.Fulfillment
	list        []models.Fulfillment
	count       int64

	err error

	// Servise geçirilen son girdiler; handler'ın çeviriyi doğru yaptığı
	// bunlarla kanıtlanır.
	sonOptionInput   service.CreateOptionInput
	sonListeInput    service.ListOptionsInput
	sonCreateInput   service.CreateFulfillmentInput
	sonRuleInput     service.CreateRuleInput
	sonUpdateOption  service.UpdateOptionInput
	sonUpdateProfile service.UpdateProfileInput
	sonShipTracking  [2]string
	sonIptalEdilen   string
}

// Sahte servisin handler'ların beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ api.Fulfillments = (*fakeFulfillments)(nil)

func (f *fakeFulfillments) ProviderIDs(_ context.Context) []string { return f.providerIDs }

func (f *fakeFulfillments) CreateShippingProfile(
	_ context.Context,
	_ service.CreateProfileInput,
) (models.ShippingProfile, error) {
	return f.profile, f.err
}

func (f *fakeFulfillments) GetShippingProfile(
	_ context.Context,
	_ string,
) (models.ShippingProfile, error) {
	return f.profile, f.err
}

func (f *fakeFulfillments) ListShippingProfiles(
	_ context.Context,
	_ service.ListProfilesInput,
) ([]models.ShippingProfile, int64, error) {
	return f.profiles, f.count, f.err
}

func (f *fakeFulfillments) UpdateShippingProfile(
	_ context.Context,
	_ string,
	in service.UpdateProfileInput,
) (models.ShippingProfile, error) {
	f.sonUpdateProfile = in
	return f.profile, f.err
}

func (f *fakeFulfillments) DeleteShippingProfile(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) CreateShippingOption(
	_ context.Context,
	in service.CreateOptionInput,
) (models.ShippingOption, error) {
	f.sonOptionInput = in
	return f.option, f.err
}

func (f *fakeFulfillments) GetShippingOption(
	_ context.Context,
	_ string,
) (models.ShippingOption, error) {
	return f.option, f.err
}

func (f *fakeFulfillments) ListShippingOptions(
	_ context.Context,
	_ service.ListOptionsAdminInput,
) ([]models.ShippingOption, int64, error) {
	return f.options, f.count, f.err
}

func (f *fakeFulfillments) UpdateShippingOption(
	_ context.Context,
	_ string,
	in service.UpdateOptionInput,
) (models.ShippingOption, error) {
	f.sonUpdateOption = in
	return f.option, f.err
}

func (f *fakeFulfillments) DeleteShippingOption(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) CreateShippingOptionRule(
	_ context.Context,
	_ string,
	in service.CreateRuleInput,
) (models.ShippingOptionRule, error) {
	f.sonRuleInput = in
	return f.rule, f.err
}

func (f *fakeFulfillments) ListShippingOptionRules(
	_ context.Context,
	_ string,
) ([]models.ShippingOptionRule, error) {
	return f.rules, f.err
}

func (f *fakeFulfillments) DeleteShippingOptionRule(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) ListShippingOptionsFor(
	_ context.Context,
	in service.ListOptionsInput,
) ([]service.QuotedOption, error) {
	f.sonListeInput = in
	return f.quoted, f.err
}

func (f *fakeFulfillments) CreateFulfillment(
	_ context.Context,
	in service.CreateFulfillmentInput,
) (models.Fulfillment, error) {
	f.sonCreateInput = in
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) GetFulfillment(_ context.Context, _ string) (models.Fulfillment, error) {
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) ListFulfillments(
	_ context.Context,
	_ service.ListFulfillmentsInput,
) ([]models.Fulfillment, int64, error) {
	return f.list, f.count, f.err
}

func (f *fakeFulfillments) CancelFulfillment(_ context.Context, id string) error {
	f.sonIptalEdilen = id
	return f.err
}

func (f *fakeFulfillments) MarkShipped(
	_ context.Context,
	_, trackingNumber, trackingURL string,
) (models.Fulfillment, error) {
	f.sonShipTracking = [2]string{trackingNumber, trackingURL}
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) MarkDelivered(_ context.Context, _ string) (models.Fulfillment, error) {
	return f.fulfillment, f.err
}

// notFoundHatasi testlerde kullanılan tipli bir bulunamadı hatasıdır.
func notFoundHatasi() error {
	return errors.NotFound("fulfillment_not_found", "gönderi bulunamadı")
}

// conflictHatasi testlerde kullanılan tipli bir çakışma hatasıdır.
func conflictHatasi() error {
	return errors.Conflict(service.CodeInvalidTransition, "teslim edilmiş gönderi iptal edilemez")
}
