package service_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// testRegionID is the destination shipping region of the selection tests. This
// module keeps the region identifier OPAQUE (there is no foreign key), so the
// value does not have to correspond to a real region record.
const testRegionID = "reg_tr"

// fakeStore is the in-memory counterpart of service.Store.
//
// It imitates four behaviors of the real store DELIBERATELY, because the
// service's correctness rests on them:
//
//  1. A method that takes a lock returns an error if it is called OUTSIDE a
//     transaction. If the service forgets WithTx on some flow, the unit test
//     catches it; in a real database that fault would, because of the unlocked
//     read, only show up under a race.
//  2. If the transaction ends with an error, what was written is ROLLED BACK.
//     The claim "it returned an error and nothing was written" can only be
//     exercised this way.
//  3. The idempotency key is unique among the LIVING fulfillments; it is the
//     counterpart of the unique index and CreateFulfillment's idempotency rests
//     on it.
//  4. The same order line cannot appear twice in one fulfillment.
type fakeStore struct {
	mu       sync.Mutex
	profiles map[string]models.ShippingProfile
	options  map[string]models.ShippingOption
	rules    map[string]models.ShippingOptionRule
	fuls     map[string]models.Fulfillment
	items    map[string]models.FulfillmentItem
	// locations are the warehouse shipping policies; the key is the location
	// identifier.
	locations map[string]models.ShippingLocation

	// locks records the locks taken in order; the claim that a lock is taken has
	// to be directly readable.
	locks []string
	// fulWrites counts how many times the fulfillment row was written to; that
	// the idempotent branches DO NOT TOUCH the row A SECOND TIME is proven with
	// it.
	fulWrites int

	// failCreateItem, when set, makes CreateFulfillmentItem return this error;
	// it is used to exercise the transaction rollback path.
	failCreateItem error
}

// newFakeStore produces an empty fake store.
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

// That the fake store satisfies the surface the service expects is verified at
// compile time.
var _ service.Store = (*fakeStore)(nil)

// WithTx runs fn inside a "transaction"; if it returns an error the state is
// rolled back.
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

// requireTx verifies that the lock-taking methods were called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s was called outside a transaction", op)
	}
	return nil
}

// lockOrder returns the recorded lock order.
func (f *fakeStore) lockOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.locks)
}

// resetLocks empties the lock ledger.
//
// The SETUP steps of a test take locks as well (e.g. creating an option locks
// the profile in shared mode); so that the locks of the flow under test can be
// seen, the ledger is reset after the setup.
func (f *fakeStore) resetLocks() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locks = nil
}

// fulfillmentWriteCount returns the number of writes made to the fulfillment
// row.
func (f *fakeStore) fulfillmentWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fulWrites
}

// --- shipping profiles -------------------------------------------------------

func (f *fakeStore) CreateShippingProfile(
	_ context.Context,
	profile models.ShippingProfile,
) (models.ShippingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.profiles {
		if existing.DeletedAt == nil && existing.Name == profile.Name {
			return models.ShippingProfile{}, errors.Conflict("fake_profile_name_exists",
				"a shipping profile with this name already exists")
		}
	}
	profile.CreatedAt = testNow
	profile.UpdatedAt = testNow
	f.profiles[profile.ID] = profile
	return profile, nil
}

func (f *fakeStore) GetShippingProfile(_ context.Context, id string) (models.ShippingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	profile, ok := f.profiles[id]
	if !ok || profile.DeletedAt != nil {
		return models.ShippingProfile{}, errors.NotFound("fake_profile_not_found",
			"shipping profile not found: %s", id)
	}
	return profile, nil
}

// LockShippingProfile reads the profile "with a lock".
//
// In the real store this is FOR UPDATE, and calling it outside a transaction
// protects nothing; the fake REJECTS the out-of-transaction call as well, so
// that the unit test catches it when the service forgets WithTx on some flow.
func (f *fakeStore) LockShippingProfile(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	if err := requireTx(ctx, "LockShippingProfile"); err != nil {
		return models.ShippingProfile{}, err
	}

	f.mu.Lock()
	f.locks = append(f.locks, "profile")
	f.mu.Unlock()

	return f.GetShippingProfile(ctx, id)
}

// LockShippingProfileShared reads the profile with a shared lock; its
// counterpart in the real store is FOR SHARE.
func (f *fakeStore) LockShippingProfileShared(
	ctx context.Context,
	id string,
) (models.ShippingProfile, error) {
	if err := requireTx(ctx, "LockShippingProfileShared"); err != nil {
		return models.ShippingProfile{}, err
	}

	f.mu.Lock()
	f.locks = append(f.locks, "profile-shared")
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
	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
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
			"shipping profile not found: %s", profile.ID)
	}
	profile.CreatedAt = current.CreatedAt
	profile.UpdatedAt = testNow
	f.profiles[profile.ID] = profile
	return profile, nil
}

func (f *fakeStore) SoftDeleteShippingProfile(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	profile, ok := f.profiles[id]
	if !ok || profile.DeletedAt != nil {
		return errors.NotFound("fake_profile_not_found", "shipping profile not found: %s", id)
	}
	deletedAt := testNow
	profile.DeletedAt = &deletedAt
	f.profiles[id] = profile
	return nil
}

func (f *fakeStore) CountAliveOptionsByProfile(_ context.Context, profileID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	// Only the KEYS are walked: walking by value would copy the whole option
	// struct on every iteration.
	for id := range f.options {
		if f.options[id].DeletedAt == nil && f.options[id].ShippingProfileID == profileID {
			count++
		}
	}
	return count, nil
}

// --- shipping options --------------------------------------------------------

func (f *fakeStore) CreateShippingOption(
	_ context.Context,
	option models.ShippingOption,
) (models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	option.CreatedAt = testNow
	option.UpdatedAt = testNow
	f.options[option.ID] = option
	return option, nil
}

func (f *fakeStore) GetShippingOption(_ context.Context, id string) (models.ShippingOption, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	option, ok := f.options[id]
	if !ok || option.DeletedAt != nil {
		return models.ShippingOption{}, errors.NotFound("fake_option_not_found",
			"shipping option not found: %s", id)
	}
	// The real store DOES NOT FILL IN the rules; the fake must not fill them in
	// either, so that the service reading the rules separately stays visible.
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
	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
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
		// The real query JOINs the profile and eliminates an option whose profile
		// is DELETED; the fake has to eliminate it too, otherwise the unit test
		// would diverge from the real behavior.
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
			"shipping option not found: %s", option.ID)
	}
	// The real query DOES NOT UPDATE the provider and the profile; the fake
	// behaves the same way so that the claim that the service does not change
	// these fields holds here as well.
	option.ProviderID = current.ProviderID
	option.ShippingProfileID = current.ShippingProfileID
	option.CreatedAt = current.CreatedAt
	option.UpdatedAt = testNow
	option.Rules = nil
	f.options[option.ID] = option
	return option, nil
}

func (f *fakeStore) SoftDeleteShippingOption(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	option, ok := f.options[id]
	if !ok || option.DeletedAt != nil {
		return errors.NotFound("fake_option_not_found", "shipping option not found: %s", id)
	}
	deletedAt := testNow
	option.DeletedAt = &deletedAt
	f.options[id] = option
	return nil
}

// --- rules -------------------------------------------------------------------

func (f *fakeStore) CreateShippingOptionRule(
	_ context.Context,
	rule models.ShippingOptionRule,
) (models.ShippingOptionRule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	rule.CreatedAt = testNow
	rule.UpdatedAt = testNow
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
			"rule not found: %s", id)
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
		return errors.NotFound("fake_rule_not_found", "rule not found: %s", id)
	}
	deletedAt := testNow
	rule.DeletedAt = &deletedAt
	f.rules[id] = rule
	return nil
}

// --- fulfillments ------------------------------------------------------------

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
	ful.CreatedAt = testNow
	ful.UpdatedAt = testNow
	f.fuls[ful.ID] = ful
	f.fulWrites++
	return ful, true, nil
}

func (f *fakeStore) FulfillmentsByIDs(_ context.Context, ids []string) ([]models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Sorted, deleted rows skipped, missing ids simply absent: the same three
	// things the query does. A fake that answered any of them differently would
	// let a test pass over behavior the database does not have.
	out := make([]models.Fulfillment, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		ful, ok := f.fuls[id]
		if !ok || ful.DeletedAt != nil {
			continue
		}
		ful.Items = nil
		out = append(out, ful)
	}

	return out, nil
}

func (f *fakeStore) GetFulfillment(_ context.Context, id string) (models.Fulfillment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ful, ok := f.fuls[id]
	if !ok || ful.DeletedAt != nil {
		return models.Fulfillment{}, errors.NotFound("fake_fulfillment_not_found",
			"fulfillment not found: %s", id)
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
		"there is no fulfillment with this key: %s", key)
}

func (f *fakeStore) LockFulfillment(ctx context.Context, id string) (models.Fulfillment, error) {
	if err := requireTx(ctx, "LockFulfillment"); err != nil {
		return models.Fulfillment{}, err
	}

	f.mu.Lock()
	f.locks = append(f.locks, "fulfillment")
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
	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
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
			"fulfillment not found: %s", id)
	}
	ful.ExternalID = externalID
	ful.Status = status
	ful.TrackingNumber = trackingNumber
	ful.TrackingURL = trackingURL
	ful.Data = json.RawMessage(data)
	ful.ShippedAt, ful.DeliveredAt, ful.CanceledAt = shippedAt, deliveredAt, canceledAt
	ful.UpdatedAt = testNow
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
			"fulfillment not found: %s", id)
	}
	// The counterpart of the fulfillments_*_stamp constraints in the schema:
	// writing a status without its stamp is rejected in the REAL database, and it
	// has to be rejected in the fake as well.
	if (status == models.StatusShipped && shippedAt == nil) ||
		(status == models.StatusDelivered && deliveredAt == nil) ||
		(status == models.StatusCanceled && canceledAt == nil) {
		return models.Fulfillment{}, errors.Internal("fake_stamp_missing",
			"the %q status cannot be written without a timestamp", status)
	}
	ful.Status = status
	ful.TrackingNumber = trackingNumber
	ful.TrackingURL = trackingURL
	ful.ShippedAt, ful.DeliveredAt, ful.CanceledAt = shippedAt, deliveredAt, canceledAt
	ful.UpdatedAt = testNow
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
				"the same order line cannot appear twice in a fulfillment")
		}
	}
	item.CreatedAt = testNow
	item.UpdatedAt = testNow
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

// paginate applies limit/offset to an in-memory list.
func paginate[T any](items []T, limit, offset int64) []T {
	if offset >= int64(len(items)) {
		return []T{}
	}
	end := offset + limit
	if limit <= 0 || end > int64(len(items)) {
		end = int64(len(items))
	}
	return slices.Clone(items[offset:end])
}

// --- fake provider -----------------------------------------------------------

// fakeProvider is the exercisable counterpart of FulfillmentProvider.
//
// Unlike the real provider it writes nowhere; the behavior of every method is
// set by the test. The purpose is to exercise the way the service TALKS to the
// provider: which input it gives, how it carries the error, and that on the
// idempotent branches it does NOT go to the provider at all.
type fakeProvider struct {
	mu sync.Mutex

	id string

	// quoteAmount is the amount Quote returns.
	quoteAmount int64
	// quoteCurrency, when not empty, makes Quote return this currency; it is
	// there to exercise the contract violation.
	quoteCurrency string
	// quoteErr, when set, makes Quote return this error.
	quoteErr error
	// quoteCalls counts how many times Quote was called.
	quoteCalls int
	// quoteInputs stores the inputs handed to Quote in order.
	quoteInputs []coreprovider.QuoteInput

	// createErr, when set, makes Create return this error.
	createErr error
	// createStatus is the status Create returns; if empty, "pending".
	createStatus coreprovider.FulfillmentStatus
	// createCalls counts how many times Create was called.
	createCalls int
	// createInputs stores the inputs handed to Create in order.
	createInputs []coreprovider.CreateFulfillmentInput

	// cancelErr, when set, makes Cancel return this error.
	cancelErr error
	// cancelCalls counts how many times Cancel was called; that the compensation
	// does not go to the provider A SECOND TIME is proven with it.
	cancelCalls int
	// canceledIDs stores the canceled provider identifiers.
	canceledIDs []string
}

// That fakeProvider satisfies the core contract is verified at compile time.
var _ coreprovider.FulfillmentProvider = (*fakeProvider)(nil)

// newFakeProvider produces a fake provider with the given identifier.
func newFakeProvider(id string) *fakeProvider {
	return &fakeProvider{id: id, quoteAmount: 2_500}
}

// ID returns the provider's identifier.
func (p *fakeProvider) ID() string { return p.id }

// Quote returns the configured amount and records the input.
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
		Data:         json.RawMessage(`{"provider":"fake"}`),
	}, nil
}

// Create returns a fake fulfillment identifier and records the input.
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
		ID:             "ext_" + in.IdempotencyKey,
		Status:         status,
		TrackingNumber: "TK-" + in.IdempotencyKey,
		TrackingURL:    "https://shipping.example/TK-" + in.IdempotencyKey,
		Data:           json.RawMessage(`{"label":"printed"}`),
	}, nil
}

// Cancel records the cancellation.
func (p *fakeProvider) Cancel(_ context.Context, fulfillmentID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cancelCalls++
	p.canceledIDs = append(p.canceledIDs, fulfillmentID)
	return p.cancelErr
}

// callCounts returns the calls made to the provider.
func (p *fakeProvider) callCounts() (quote, create, cancel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quoteCalls, p.createCalls, p.cancelCalls
}

// lastQuoteInput returns the last input handed to Quote.
func (p *fakeProvider) lastQuoteInput() coreprovider.QuoteInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.quoteInputs) == 0 {
		return coreprovider.QuoteInput{}
	}
	return p.quoteInputs[len(p.quoteInputs)-1]
}

// lastCreateInput returns the last input handed to Create.
func (p *fakeProvider) lastCreateInput() coreprovider.CreateFulfillmentInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.createInputs) == 0 {
		return coreprovider.CreateFulfillmentInput{}
	}
	return p.createInputs[len(p.createInputs)-1]
}

// --- shared setup ------------------------------------------------------------

// testNow is the fixed clock of the tests; the real clock is not used so that
// the timestamp claims can be exact.
var testNow = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// testSetup carries the components a test uses.
type testSetup struct {
	svc      *service.Service
	store    *fakeStore
	provider *fakeProvider
}

// newSetup builds a service running on a fake store and a fake provider.
func newSetup(t interface{ Fatalf(string, ...any) }) testSetup {
	store := newFakeStore()
	provider := newFakeProvider("fake")

	registry := service.NewProviderRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("the provider could not be registered: %v", err)
	}

	svc, err := service.New(service.Options{
		Store:     store,
		Providers: registry,
		Clock:     func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("the service could not be built: %v", err)
	}
	return testSetup{svc: svc, store: store, provider: provider}
}

// createProfile creates a shipping profile for the test and returns its
// identifier.
func (k testSetup) createProfile(t interface {
	Fatalf(string, ...any)
	Helper()
}, name string) string {
	t.Helper()
	profile, err := k.svc.CreateShippingProfile(context.Background(), service.CreateProfileInput{
		Name: name,
	})
	if err != nil {
		t.Fatalf("the shipping profile could not be created: %v", err)
	}
	return profile.ID
}

// createOption creates a shipping option for the test and returns its
// identifier.
func (k testSetup) createOption(t interface {
	Fatalf(string, ...any)
	Helper()
}, in service.CreateOptionInput) string {
	t.Helper()
	if strings.TrimSpace(in.ProviderID) == "" {
		in.ProviderID = "fake"
	}
	if strings.TrimSpace(in.CurrencyCode) == "" {
		in.CurrencyCode = "TRY"
	}
	option, err := k.svc.CreateShippingOption(context.Background(), in)
	if err != nil {
		t.Fatalf("the shipping option could not be created: %v", err)
	}
	return option.ID
}

// --- warehouse shipping policies ---------------------------------------------

// The policy rows enter the transaction snapshot as well: had the rollback claim
// only been true for the older tables, the atomicity of the new write path would
// have stayed unexercised.

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

// ReplaceShippingLocationRegions imitates the real store's transaction
// requirement: it returns an error if it is called outside a transaction. The
// requirement is not a comment but an exercised behavior — if a two-statement
// write is left without a transaction, the warehouse looks for a moment as if it
// were open to ALL regions.
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
		return errors.NotFound("fake_location_not_found", "no policy: %s", locationID)
	}
	// The real store returns the links sorted BY IDENTIFIER (the read queries
	// apply ORDER BY region_id); the fake sorts them too. Had it not sorted, the
	// unit tests would believe the input's order was preserved and a claim
	// running against the real store would silently diverge.
	sorted := slices.Clone(regionIDs)
	slices.Sort(sorted)
	loc.RegionIDs = sorted
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
			"fulfillment_shipping_location_not_found", "no policy: %s", locationID)
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
			"fulfillment_shipping_location_not_found", "no policy: %s", locationID)
	}
	delete(f.locations, locationID)
	return nil
}

// LocationPolicies imitates the distinction the real query makes: a candidate
// that HAS NO record does not appear in the returned slice AT ALL, and the region
// links are carried not as a flag but as an ARRAY OF IDENTIFIERS.
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
