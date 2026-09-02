package service_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// txMarkerKey sahte deponun "işlem içindeyiz" işaretidir.
type txMarkerKey struct{}

// testRegionID seçim testlerinin hedef kargo bölgesidir. Bu modül bölge
// kimliğini OPAK tutar (foreign key yoktur), bu yüzden değer gerçek bir region
// kaydına karşılık gelmek zorunda değildir.
const testRegionID = "reg_tr"

// fakeStore service.Store'un bellek içi karşılığıdır.
//
// Dört davranışı gerçek depodan BİLİNÇLİ olarak taklit eder, çünkü servisin
// doğruluğu bunlara dayanır:
//
//  1. Kilit alan metot işlem DIŞINDA çağrılırsa hata döner. Servis bir akışta
//     WithTx'i unutursa birim testi bunu yakalar; gerçek veritabanında bu hata,
//     kilitsiz okuma yüzünden ancak yarış altında görünürdü.
//  2. İşlem hatayla biterse yazılanlar GERİ ALINIR. "Hata döndü ve hiçbir şey
//     yazılmadı" iddiası ancak böyle sınanabilir.
//  3. Idempotency anahtarı YAŞAYAN gönderiler arasında tektir; benzersiz
//     indeksin karşılığıdır ve CreateFulfillment'ın idempotentliği ona dayanır.
//  4. Aynı sipariş satırı bir gönderide iki kez yer alamaz.
type fakeStore struct {
	mu       sync.Mutex
	profiles map[string]models.ShippingProfile
	options  map[string]models.ShippingOption
	rules    map[string]models.ShippingOptionRule
	fuls     map[string]models.Fulfillment
	items    map[string]models.FulfillmentItem
	// locations depo kargo politikalarıdır; anahtar lokasyon kimliğidir.
	locations map[string]models.ShippingLocation

	// kilitler alınan kilitleri sırasıyla kaydeder; kilit alma iddiası
	// doğrudan okunabilir olmalıdır.
	kilitler []string
	// fulWrites gönderi satırına kaç kez yazıldığını sayar; idempotent
	// dalların satıra İKİNCİ KEZ dokunmadığı bununla kanıtlanır.
	fulWrites int

	// failCreateItem ayarlanırsa CreateFulfillmentItem bu hatayı döner;
	// işlem geri alma yolunu sınamak için kullanılır.
	failCreateItem error
}

// newFakeStore boş bir sahte depo üretir.
func newFakeStore() *fakeStore {
	return &fakeStore{
		profiles:  map[string]models.ShippingProfile{},
		options:   map[string]models.ShippingOption{},
		rules:     map[string]models.ShippingOptionRule{},
		fuls:      map[string]models.Fulfillment{},
		items:     map[string]models.FulfillmentItem{},
		locations: map[string]models.ShippingLocation{},
	}
}

// Sahte deponun servisin beklediği yüzeyi karşıladığı derleme zamanında
// doğrulanır.
var _ service.Store = (*fakeStore)(nil)

// WithTx fn'i "işlem" içinde çalıştırır; hata dönerse durumu geri alır.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	f.mu.Lock()
	snapshot := struct {
		profiles  map[string]models.ShippingProfile
		options   map[string]models.ShippingOption
		rules     map[string]models.ShippingOptionRule
		fuls      map[string]models.Fulfillment
		items     map[string]models.FulfillmentItem
		locations map[string]models.ShippingLocation
	}{
		profiles:  maps.Clone(f.profiles),
		options:   maps.Clone(f.options),
		rules:     maps.Clone(f.rules),
		fuls:      maps.Clone(f.fuls),
		items:     maps.Clone(f.items),
		locations: maps.Clone(f.locations),
	}
	f.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.profiles, f.options, f.rules = snapshot.profiles, snapshot.options, snapshot.rules
		f.fuls, f.items = snapshot.fuls, snapshot.items
		f.locations = snapshot.locations
		f.mu.Unlock()
		return err
	}
	return nil
}

// requireTx kilit alan metotların işlem içinde çağrıldığını doğrular.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s işlem dışında çağrıldı", op)
	}
	return nil
}

// kilitSirasi kaydedilen kilit sırasını döner.
func (f *fakeStore) kilitSirasi() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.kilitler)
}

// kilitleriSifirla kilit defterini boşaltır.
//
// Testin KURULUM adımları da kilit alır (örn. seçenek oluşturma profili
// paylaşımlı kilitler); sınanan akışın kilitlerini görebilmek için defter
// kurulumdan sonra sıfırlanır.
func (f *fakeStore) kilitleriSifirla() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kilitler = nil
}

// gonderiYazmaSayisi gönderi satırına yapılan yazma sayısını döner.
func (f *fakeStore) gonderiYazmaSayisi() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fulWrites
}

// --- kargo profilleri --------------------------------------------------------

func (f *fakeStore) CreateShippingProfile(
	_ context.Context,
	profile models.ShippingProfile,
) (models.ShippingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.profiles {
		if existing.DeletedAt == nil && existing.Name == profile.Name {
			return models.ShippingProfile{}, errors.Conflict("fake_profile_name_exists",
				"bu adda bir kargo profili zaten var")
		}
	}
	profile.CreatedAt = testAn
	profile.UpdatedAt = testAn
	f.profiles[profile.ID] = profile
	return profile, nil
}

func (f *fakeStore) GetShippingProfile(_ context.Context, id string) (models.ShippingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	profile, ok := f.profiles[id]
	if !ok || profile.DeletedAt != nil {
		return models.ShippingProfile{}, errors.NotFound("fake_profile_not_found",
			"kargo profili bulunamadı: %s", id)
	}
	return profile, nil
}

// LockShippingProfile profili "kilitleyerek" okur.
//
// Gerçek depoda bu FOR UPDATE'tir ve işlem dışında çağrılması hiçbir şeyi
// korumaz; sahte de işlem dışı çağrıyı REDDEDER ki servis bir akışta WithTx'i
// unuttuğunda birim testi bunu yakalasın.
func (f *fakeStore) LockShippingProfile(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	if err := requireTx(ctx, "LockShippingProfile"); err != nil {
		return models.ShippingProfile{}, err
	}

	f.mu.Lock()
	f.kilitler = append(f.kilitler, "profil")
	f.mu.Unlock()

	return f.GetShippingProfile(ctx, id)
}

// LockShippingProfileShared profili paylaşımlı kilitle okur; gerçek depodaki
// karşılığı FOR SHARE'dir.
func (f *fakeStore) LockShippingProfileShared(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	if err := requireTx(ctx, "LockShippingProfileShared"); err != nil {
		return models.ShippingProfile{}, err
	}

	f.mu.Lock()
	f.kilitler = append(f.kilitler, "profil-paylasimli")
	f.mu.Unlock()

	return f.GetShippingProfile(ctx, id)
}

func (f *fakeStore) ListShippingProfiles(
	_ context.Context,
	filter models.ProfileFilter,
) ([]models.ShippingProfile, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []models.ShippingProfile
	for _, id := range slices.Sorted(maps.Keys(f.profiles)) {
		profile := f.profiles[id]
		if profile.DeletedAt != nil {
			continue
		}
		if filter.Type != nil && profile.Type.String() != *filter.Type {
			continue
		}
		matched = append(matched, profile)
	}
	return sayfala(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

func (f *fakeStore) UpdateShippingProfile(
	_ context.Context,
	profile models.ShippingProfile,
) (models.ShippingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	current, ok := f.profiles[profile.ID]
	if !ok || current.DeletedAt != nil {
		return models.ShippingProfile{}, errors.NotFound("fake_profile_not_found",
			"kargo profili bulunamadı: %s", profile.ID)
	}
	profile.CreatedAt = current.CreatedAt
	profile.UpdatedAt = testAn
	f.profiles[profile.ID] = profile
	return profile, nil
}

func (f *fakeStore) SoftDeleteShippingProfile(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	profile, ok := f.profiles[id]
	if !ok || profile.DeletedAt != nil {
		return errors.NotFound("fake_profile_not_found", "kargo profili bulunamadı: %s", id)
	}
	silinme := testAn
	profile.DeletedAt = &silinme
	f.profiles[id] = profile
	return nil
}

func (f *fakeStore) CountAliveOptionsByProfile(_ context.Context, profileID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	// Yalnızca ANAHTARLAR gezilir: değerle gezmek her yinelemede seçenek
	// yapısının tamamını kopyalardı.
	for id := range f.options {
		if f.options[id].DeletedAt == nil && f.options[id].ShippingProfileID == profileID {
			count++
		}
	}
	return count, nil
}

// --- kargo seçenekleri -------------------------------------------------------

func (f *fakeStore) CreateShippingOption(
	_ context.Context,
	option models.ShippingOption,
) (models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	option.CreatedAt = testAn
	option.UpdatedAt = testAn
	f.options[option.ID] = option
	return option, nil
}

func (f *fakeStore) GetShippingOption(_ context.Context, id string) (models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	option, ok := f.options[id]
	if !ok || option.DeletedAt != nil {
		return models.ShippingOption{}, errors.NotFound("fake_option_not_found",
			"kargo seçeneği bulunamadı: %s", id)
	}
	// Gerçek depo kuralları DOLDURMAZ; sahte de doldurmamalıdır ki servisin
	// kuralları ayrıca okuduğu görünür kalsın.
	option.Rules = nil
	return option, nil
}

func (f *fakeStore) ListShippingOptions(
	_ context.Context,
	filter models.OptionFilter,
) ([]models.ShippingOption, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []models.ShippingOption
	for _, id := range slices.Sorted(maps.Keys(f.options)) {
		option := f.options[id]
		if option.DeletedAt != nil {
			continue
		}
		if filter.RegionID != nil && option.RegionID != *filter.RegionID {
			continue
		}
		if filter.ProfileID != nil && option.ShippingProfileID != *filter.ProfileID {
			continue
		}
		if filter.ProviderID != nil && option.ProviderID != *filter.ProviderID {
			continue
		}
		if filter.PriceType != nil && option.PriceType.String() != *filter.PriceType {
			continue
		}
		option.Rules = nil
		matched = append(matched, option)
	}
	return sayfala(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

func (f *fakeStore) ShippingOptionsByIDs(_ context.Context, ids []string) ([]models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.ShippingOption, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		option, ok := f.options[id]
		if !ok || option.DeletedAt != nil {
			continue
		}
		option.Rules = nil
		out = append(out, option)
	}
	return out, nil
}

func (f *fakeStore) ListEligibleShippingOptions(
	_ context.Context,
	filter models.EligibilityFilter,
) ([]models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.ShippingOption, 0, len(f.options))
	for _, id := range slices.Sorted(maps.Keys(f.options)) {
		option := f.options[id]
		if option.DeletedAt != nil {
			continue
		}
		// Gerçek sorgu profile JOIN atar ve profili SİLİNMİŞ seçeneği eler;
		// sahte de elemelidir, aksi hâlde birim testi gerçek davranıştan
		// ayrışırdı.
		if profile, ok := f.profiles[option.ShippingProfileID]; !ok || profile.DeletedAt != nil {
			continue
		}
		if option.RegionID != "" && option.RegionID != filter.RegionID {
			continue
		}
		if option.CurrencyCode != filter.CurrencyCode {
			continue
		}
		if option.IsReturn != filter.IsReturn {
			continue
		}
		if !filter.IncludeAdminOnly && option.AdminOnly {
			continue
		}
		if len(filter.ProfileIDs) > 0 && !slices.Contains(filter.ProfileIDs, option.ShippingProfileID) {
			continue
		}

		option.Rules = nil
		for _, ruleID := range slices.Sorted(maps.Keys(f.rules)) {
			rule := f.rules[ruleID]
			if rule.DeletedAt == nil && rule.ShippingOptionID == option.ID {
				option.Rules = append(option.Rules, rule)
			}
		}
		out = append(out, option)
	}
	return out, nil
}

func (f *fakeStore) UpdateShippingOption(
	_ context.Context,
	option models.ShippingOption,
) (models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	current, ok := f.options[option.ID]
	if !ok || current.DeletedAt != nil {
		return models.ShippingOption{}, errors.NotFound("fake_option_not_found",
			"kargo seçeneği bulunamadı: %s", option.ID)
	}
	// Gerçek sorgu sağlayıcıyı ve profili GÜNCELLEMEZ; sahte de aynı davranır
	// ki servisin bu alanları değiştirmediği iddiası burada da tutsun.
	option.ProviderID = current.ProviderID
	option.ShippingProfileID = current.ShippingProfileID
	option.CreatedAt = current.CreatedAt
	option.UpdatedAt = testAn
	option.Rules = nil
	f.options[option.ID] = option
	return option, nil
}

func (f *fakeStore) SoftDeleteShippingOption(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	option, ok := f.options[id]
	if !ok || option.DeletedAt != nil {
		return errors.NotFound("fake_option_not_found", "kargo seçeneği bulunamadı: %s", id)
	}
	silinme := testAn
	option.DeletedAt = &silinme
	f.options[id] = option
	return nil
}

// --- kurallar ----------------------------------------------------------------

func (f *fakeStore) CreateShippingOptionRule(
	_ context.Context,
	rule models.ShippingOptionRule,
) (models.ShippingOptionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule.CreatedAt = testAn
	rule.UpdatedAt = testAn
	f.rules[rule.ID] = rule
	return rule, nil
}

func (f *fakeStore) GetShippingOptionRule(
	_ context.Context,
	id string,
) (models.ShippingOptionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule, ok := f.rules[id]
	if !ok || rule.DeletedAt != nil {
		return models.ShippingOptionRule{}, errors.NotFound("fake_rule_not_found",
			"kural bulunamadı: %s", id)
	}
	return rule, nil
}

func (f *fakeStore) ListShippingOptionRules(
	_ context.Context,
	optionID string,
) ([]models.ShippingOptionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.ShippingOptionRule, 0)
	for _, id := range slices.Sorted(maps.Keys(f.rules)) {
		rule := f.rules[id]
		if rule.DeletedAt == nil && rule.ShippingOptionID == optionID {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (f *fakeStore) SoftDeleteShippingOptionRule(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule, ok := f.rules[id]
	if !ok || rule.DeletedAt != nil {
		return errors.NotFound("fake_rule_not_found", "kural bulunamadı: %s", id)
	}
	silinme := testAn
	rule.DeletedAt = &silinme
	f.rules[id] = rule
	return nil
}

// --- gönderiler --------------------------------------------------------------

func (f *fakeStore) InsertFulfillmentIfAbsent(
	_ context.Context,
	ful models.Fulfillment,
) (models.Fulfillment, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.fuls {
		if f.fuls[id].DeletedAt == nil && f.fuls[id].IdempotencyKey == ful.IdempotencyKey {
			return models.Fulfillment{}, false, nil
		}
	}
	ful.CreatedAt = testAn
	ful.UpdatedAt = testAn
	f.fuls[ful.ID] = ful
	f.fulWrites++
	return ful, true, nil
}

func (f *fakeStore) GetFulfillment(_ context.Context, id string) (models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ful, ok := f.fuls[id]
	if !ok || ful.DeletedAt != nil {
		return models.Fulfillment{}, errors.NotFound("fake_fulfillment_not_found",
			"gönderi bulunamadı: %s", id)
	}
	ful.Items = nil
	return ful, nil
}

func (f *fakeStore) FulfillmentByIdempotencyKey(
	_ context.Context,
	key string,
) (models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range slices.Sorted(maps.Keys(f.fuls)) {
		ful := f.fuls[id]
		if ful.DeletedAt == nil && ful.IdempotencyKey == key {
			ful.Items = nil
			return ful, nil
		}
	}
	return models.Fulfillment{}, errors.NotFound("fake_fulfillment_not_found",
		"bu anahtarla gönderi yok: %s", key)
}

func (f *fakeStore) LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireTx(ctx, "LockFulfillment"); err != nil {
		return models.Fulfillment{}, err
	}

	f.mu.Lock()
	f.kilitler = append(f.kilitler, "fulfillment")
	f.mu.Unlock()

	return f.GetFulfillment(ctx, id)
}

func (f *fakeStore) ListFulfillments(
	_ context.Context,
	filter models.FulfillmentFilter,
) ([]models.Fulfillment, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []models.Fulfillment
	for _, id := range slices.Sorted(maps.Keys(f.fuls)) {
		ful := f.fuls[id]
		if ful.DeletedAt != nil {
			continue
		}
		if filter.Reference != nil && ful.Reference != *filter.Reference {
			continue
		}
		if filter.Status != nil && ful.Status.String() != *filter.Status {
			continue
		}
		ful.Items = nil
		matched = append(matched, ful)
	}
	return sayfala(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

func (f *fakeStore) UpdateFulfillmentProviderResult(
	_ context.Context,
	id, externalID string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
	data []byte,
	shippedAt, deliveredAt, canceledAt *time.Time,
) (models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ful, ok := f.fuls[id]
	if !ok || ful.DeletedAt != nil {
		return models.Fulfillment{}, errors.NotFound("fake_fulfillment_not_found",
			"gönderi bulunamadı: %s", id)
	}
	ful.ExternalID = externalID
	ful.Status = status
	ful.TrackingNumber = trackingNumber
	ful.TrackingURL = trackingURL
	ful.Data = json.RawMessage(data)
	ful.ShippedAt, ful.DeliveredAt, ful.CanceledAt = shippedAt, deliveredAt, canceledAt
	ful.UpdatedAt = testAn
	f.fuls[id] = ful
	f.fulWrites++
	return ful, nil
}

func (f *fakeStore) UpdateFulfillmentStatus(
	_ context.Context,
	id string,
	status models.FulfillmentStatus,
	trackingNumber, trackingURL string,
	shippedAt, deliveredAt, canceledAt *time.Time,
) (models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ful, ok := f.fuls[id]
	if !ok || ful.DeletedAt != nil {
		return models.Fulfillment{}, errors.NotFound("fake_fulfillment_not_found",
			"gönderi bulunamadı: %s", id)
	}
	// Şemadaki fulfillments_*_stamp kısıtlarının karşılığı: durumu damgasız
	// yazmak GERÇEK veritabanında reddedilir, sahtede de reddedilmelidir.
	if (status == models.StatusShipped && shippedAt == nil) ||
		(status == models.StatusDelivered && deliveredAt == nil) ||
		(status == models.StatusCanceled && canceledAt == nil) {
		return models.Fulfillment{}, errors.Internal("fake_stamp_missing",
			"%q durumu zaman damgası olmadan yazılamaz", status)
	}
	ful.Status = status
	ful.TrackingNumber = trackingNumber
	ful.TrackingURL = trackingURL
	ful.ShippedAt, ful.DeliveredAt, ful.CanceledAt = shippedAt, deliveredAt, canceledAt
	ful.UpdatedAt = testAn
	f.fuls[id] = ful
	f.fulWrites++
	return ful, nil
}

func (f *fakeStore) CreateFulfillmentItem(
	_ context.Context,
	item models.FulfillmentItem,
) (models.FulfillmentItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCreateItem != nil {
		return models.FulfillmentItem{}, f.failCreateItem
	}
	for _, existing := range f.items {
		if existing.FulfillmentID == item.FulfillmentID && existing.LineItemID == item.LineItemID {
			return models.FulfillmentItem{}, errors.Conflict("fake_item_exists",
				"aynı sipariş satırı gönderide iki kez yer alamaz")
		}
	}
	item.CreatedAt = testAn
	item.UpdatedAt = testAn
	f.items[item.ID] = item
	return item, nil
}

func (f *fakeStore) ListFulfillmentItems(
	_ context.Context,
	fulfillmentID string,
) ([]models.FulfillmentItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.FulfillmentItem, 0)
	for _, id := range slices.Sorted(maps.Keys(f.items)) {
		item := f.items[id]
		if item.FulfillmentID == fulfillmentID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeStore) FulfillmentItemsByFulfillments(
	_ context.Context,
	fulfillmentIDs []string,
) ([]models.FulfillmentItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.FulfillmentItem, 0)
	for _, id := range slices.Sorted(maps.Keys(f.items)) {
		item := f.items[id]
		if slices.Contains(fulfillmentIDs, item.FulfillmentID) {
			out = append(out, item)
		}
	}
	return out, nil
}

// sayfala bellek içi listeye limit/offset uygular.
func sayfala[T any](items []T, limit, offset int64) []T {
	if offset >= int64(len(items)) {
		return []T{}
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(items)) {
		end = int64(len(items))
	}
	return slices.Clone(items[offset:end])
}

// --- sahte sağlayıcı ---------------------------------------------------------

// fakeProvider FulfillmentProvider'ın sınanabilir karşılığıdır.
//
// Gerçek sağlayıcının aksine hiçbir yere yazmaz; her metodun davranışı test
// tarafından ayarlanır. Amaç servisin sağlayıcıyla KONUŞMA biçimini sınamaktır:
// hangi girdiyi verdiği, hatayı nasıl taşıdığı ve idempotent dallarda
// sağlayıcıya HİÇ gitmediği.
type fakeProvider struct {
	mu sync.Mutex

	id string

	// quoteAmount Quote'un döndüğü tutardır.
	quoteAmount int64
	// quoteCurrency boş değilse Quote bu para birimini döner; sözleşme
	// ihlalini sınamak içindir.
	quoteCurrency string
	// quoteErr ayarlanırsa Quote bu hatayı döner.
	quoteErr error
	// quoteCalls Quote'un kaç kez çağrıldığını sayar.
	quoteCalls int
	// quoteInputs Quote'a verilen girdileri sırasıyla saklar.
	quoteInputs []coreprovider.QuoteInput

	// createErr ayarlanırsa Create bu hatayı döner.
	createErr error
	// createStatus Create'in döndüğü durumdur; boşsa "pending".
	createStatus coreprovider.FulfillmentStatus
	// createCalls Create'in kaç kez çağrıldığını sayar.
	createCalls int
	// createInputs Create'e verilen girdileri sırasıyla saklar.
	createInputs []coreprovider.CreateFulfillmentInput

	// cancelErr ayarlanırsa Cancel bu hatayı döner.
	cancelErr error
	// cancelCalls Cancel'ın kaç kez çağrıldığını sayar; telafinin sağlayıcıya
	// İKİNCİ KEZ gitmediği bununla kanıtlanır.
	cancelCalls int
	// canceledIDs iptal edilen sağlayıcı kimliklerini saklar.
	canceledIDs []string
}

// fakeProvider'ın çekirdek sözleşmesini karşıladığı derleme zamanında
// doğrulanır.
var _ coreprovider.FulfillmentProvider = (*fakeProvider)(nil)

// newFakeProvider verilen kimlikle bir sahte sağlayıcı üretir.
func newFakeProvider(id string) *fakeProvider {
	return &fakeProvider{id: id, quoteAmount: 2_500}
}

// ID sağlayıcının kimliğini döner.
func (p *fakeProvider) ID() string { return p.id }

// Quote ayarlanmış tutarı döner ve girdiyi kaydeder.
func (p *fakeProvider) Quote(
	_ context.Context,
	in coreprovider.QuoteInput,
) (coreprovider.ShippingQuote, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.quoteCalls++
	p.quoteInputs = append(p.quoteInputs, in)
	if p.quoteErr != nil {
		return coreprovider.ShippingQuote{}, p.quoteErr
	}

	currency := p.quoteCurrency
	if currency == "" {
		currency = in.CurrencyCode
	}
	return coreprovider.ShippingQuote{
		OptionID:     in.OptionID,
		Amount:       p.quoteAmount,
		CurrencyCode: currency,
		Data:         json.RawMessage(`{"saglayici":"sahte"}`),
	}, nil
}

// Create sahte bir gönderi kimliği döner ve girdiyi kaydeder.
func (p *fakeProvider) Create(
	_ context.Context,
	in coreprovider.CreateFulfillmentInput,
) (coreprovider.Fulfillment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.createCalls++
	p.createInputs = append(p.createInputs, in)
	if p.createErr != nil {
		return coreprovider.Fulfillment{}, p.createErr
	}

	status := p.createStatus
	if status == "" {
		status = coreprovider.FulfillmentPending
	}
	return coreprovider.Fulfillment{
		ID:             "dis_" + in.IdempotencyKey,
		Status:         status,
		TrackingNumber: "TK-" + in.IdempotencyKey,
		TrackingURL:    "https://kargo.example/TK-" + in.IdempotencyKey,
		Data:           json.RawMessage(`{"etiket":"basildi"}`),
	}, nil
}

// Cancel iptali kaydeder.
func (p *fakeProvider) Cancel(_ context.Context, fulfillmentID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cancelCalls++
	p.canceledIDs = append(p.canceledIDs, fulfillmentID)
	return p.cancelErr
}

// cagriSayilari sağlayıcıya yapılan çağrıları döner.
func (p *fakeProvider) cagriSayilari() (quote, create, cancel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quoteCalls, p.createCalls, p.cancelCalls
}

// sonQuoteGirdisi Quote'a verilen son girdiyi döner.
func (p *fakeProvider) sonQuoteGirdisi() coreprovider.QuoteInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.quoteInputs) == 0 {
		return coreprovider.QuoteInput{}
	}
	return p.quoteInputs[len(p.quoteInputs)-1]
}

// sonCreateGirdisi Create'e verilen son girdiyi döner.
func (p *fakeProvider) sonCreateGirdisi() coreprovider.CreateFulfillmentInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.createInputs) == 0 {
		return coreprovider.CreateFulfillmentInput{}
	}
	return p.createInputs[len(p.createInputs)-1]
}

// --- ortak kurulum -----------------------------------------------------------

// testAn testlerin sabit saatidir; zaman damgası iddiaları kesin olsun diye
// gerçek saat kullanılmaz.
var testAn = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// testKurulum bir testin kullandığı bileşenleri taşır.
type testKurulum struct {
	svc      *service.Service
	store    *fakeStore
	provider *fakeProvider
}

// yeniKurulum sahte depo ve sahte sağlayıcı üzerinde çalışan bir servis kurar.
func yeniKurulum(t interface{ Fatalf(string, ...any) }) testKurulum {
	store := newFakeStore()
	provider := newFakeProvider("sahte")

	registry := service.NewProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("sağlayıcı kaydedilemedi: %v", err)
	}

	svc, err := service.New(service.Options{
		Store:     store,
		Providers: registry,
		Clock:     func() time.Time { return testAn },
	})
	if err != nil {
		t.Fatalf("servis kurulamadı: %v", err)
	}
	return testKurulum{svc: svc, store: store, provider: provider}
}

// profilAc test için bir kargo profili oluşturur ve kimliğini döner.
func (k testKurulum) profilAc(t interface {
	Fatalf(string, ...any)
	Helper()
}, ad string) string {
	t.Helper()
	profile, err := k.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: ad,
	})
	if err != nil {
		t.Fatalf("kargo profili oluşturulamadı: %v", err)
	}
	return profile.ID
}

// secenekAc test için bir kargo seçeneği oluşturur ve kimliğini döner.
func (k testKurulum) secenekAc(t interface {
	Fatalf(string, ...any)
	Helper()
}, in service.CreateOptionInput) string {
	t.Helper()
	if strings.TrimSpace(in.ProviderID) == "" {
		in.ProviderID = "sahte"
	}
	if strings.TrimSpace(in.CurrencyCode) == "" {
		in.CurrencyCode = "TRY"
	}
	option, err := k.svc.CreateShippingOption(context.Background(), in)
	if err != nil {
		t.Fatalf("kargo seçeneği oluşturulamadı: %v", err)
	}
	return option.ID
}

// --- depo kargo politikaları -------------------------------------------------

// Politika satırları da işlem anlık görüntüsüne girer: geri alma iddiası
// yalnızca eski tablolar için doğru olsaydı, yeni yazma yolunun atomikliği
// sınanmamış kalırdı.

func (f *fakeStore) UpsertShippingLocation(
	_ context.Context,
	locationID string,
	priority int64,
) (models.ShippingLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	loc, exists := f.locations[locationID]
	if !exists {
		loc = models.ShippingLocation{LocationID: locationID, CreatedAt: now}
	}
	loc.Priority = priority
	loc.UpdatedAt = now
	f.locations[locationID] = loc
	return loc, nil
}

// ReplaceShippingLocationRegions gerçek deponun işlem şartını taklit eder:
// işlem dışında çağrılırsa hata döner. Şart bir yorum değil, sınanan bir
// davranıştır — iki deyimli bir yazma işlemsiz kalırsa depo bir an için TÜM
// bölgelere açık görünür.
func (f *fakeStore) ReplaceShippingLocationRegions(
	ctx context.Context,
	locationID string,
	regionIDs []string,
) error {
	if err := requireTx(ctx, "ReplaceShippingLocationRegions"); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	loc, exists := f.locations[locationID]
	if !exists {
		return errors.NotFound("fake_location_not_found", "politika yok: %s", locationID)
	}
	// Gerçek depo bağları KİMLİĞE göre sıralı döner (okuma sorguları
	// ORDER BY region_id uygular); sahte de sıralar. Sıralamasaydı birim
	// testleri girdinin sırasını korunmuş sanar ve gerçek depoya karşı koşan
	// bir iddia sessizce ayrışırdı.
	sirali := slices.Clone(regionIDs)
	slices.Sort(sirali)
	loc.RegionIDs = sirali
	f.locations[locationID] = loc
	return nil
}

func (f *fakeStore) GetShippingLocation(
	_ context.Context,
	locationID string,
) (models.ShippingLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc, ok := f.locations[locationID]
	if !ok {
		return models.ShippingLocation{}, errors.NotFound(
			"fulfillment_shipping_location_not_found", "politika yok: %s", locationID)
	}
	return loc, nil
}

func (f *fakeStore) ListShippingLocations(
	_ context.Context,
	filter models.LocationFilter,
) ([]models.ShippingLocation, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := slices.Collect(maps.Values(f.locations))
	slices.SortFunc(out, func(a, b models.ShippingLocation) int {
		if a.Priority != b.Priority {
			return int(a.Priority - b.Priority)
		}
		return strings.Compare(a.LocationID, b.LocationID)
	})

	total := int64(len(out))
	if filter.Offset >= total {
		return nil, total, nil
	}
	out = out[filter.Offset:]
	if int64(len(out)) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, total, nil
}

func (f *fakeStore) DeleteShippingLocation(_ context.Context, locationID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.locations[locationID]; !ok {
		return errors.NotFound(
			"fulfillment_shipping_location_not_found", "politika yok: %s", locationID)
	}
	delete(f.locations, locationID)
	return nil
}

// LocationPolicies gerçek sorgunun ayrımını taklit eder: kaydı OLMAYAN aday
// dönen dilimde HİÇ yer almaz ve bölge bağları bayrak olarak değil KİMLİK
// DİZİSİ olarak taşınır.
func (f *fakeStore) LocationPolicies(
	_ context.Context,
	locationIDs []string,
) ([]models.LocationPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.LocationPolicy, 0, len(locationIDs))
	for _, id := range locationIDs {
		loc, ok := f.locations[id]
		if !ok {
			continue
		}
		out = append(out, models.LocationPolicy{
			LocationID: loc.LocationID,
			Priority:   loc.Priority,
			RegionIDs:  slices.Clone(loc.RegionIDs),
		})
	}
	slices.SortFunc(out, func(a, b models.LocationPolicy) int {
		return strings.Compare(a.LocationID, b.LocationID)
	})
	return out, nil
}
