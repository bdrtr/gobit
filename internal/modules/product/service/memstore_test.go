package service_test

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// memStore is the in-memory implementation of [repository.Store].
//
// Its purpose is to make the RULES of the service verifiable without a
// database: id generation, validation, the conflict check, the transaction
// boundary and the number of batch reads. The database-specific claims (a
// partial unique index separating two concurrent requests, the soft delete
// being filtered on the SQL side) are proven NOT HERE but in the integration
// tests — a fake repository cannot verify the rule it wrote itself.
//
// The repository calls are counted (calls): that is the evidence for the claim
// "no query is made per record".
type memStore struct {
	mu sync.Mutex

	products    map[string]models.Product
	variants    map[string]models.Variant
	options     map[string]models.Option
	values      map[string]models.OptionValue
	images      map[string]models.Image
	collections map[string]models.Collection
	categories  map[string]models.Category
	tags        map[string]models.Tag

	// variantValues is the variant -> (option -> value) mapping.
	variantValues map[string]map[string]string
	productTags   map[string][]string
	productCats   map[string][]string

	// links is the fake link service the sales channel links are read from.
	//
	// In reality the product <-> channel link lives in a SINGLE table: the
	// service WRITES it through core/link, while the repository READS it with
	// the EXISTS condition of its own query. Had the fake repository kept its
	// own separate copy, the write and the read would drift apart and the tests
	// would "prove" a consistency that does not really exist — that is why the
	// read looks at where the write goes, at [fakeLinker].
	//
	// If it is nil no product has an assignment; by the rule they are all
	// visible in every channel.
	links *fakeLinker

	// calls is the call counter by method name.
	calls map[string]int
	// failOn, for a method name that is set, is the error that call returns.
	failOn map[string]error
	// inTx shows whether we are inside a transaction.
	inTx bool
}

var _ repository.Store = (*memStore)(nil)

// newMemStore builds an empty in-memory repository.
func newMemStore() *memStore {
	return &memStore{
		products:      map[string]models.Product{},
		variants:      map[string]models.Variant{},
		options:       map[string]models.Option{},
		values:        map[string]models.OptionValue{},
		images:        map[string]models.Image{},
		collections:   map[string]models.Collection{},
		categories:    map[string]models.Category{},
		tags:          map[string]models.Tag{},
		variantValues: map[string]map[string]string{},
		productTags:   map[string][]string{},
		productCats:   map[string][]string{},
		calls:         map[string]int{},
		failOn:        map[string]error{},
	}
}

// track counts the call and returns the injected error if there is one.
func (m *memStore) track(name string) error {
	m.calls[name]++
	return m.failOn[name]
}

// callCount returns how many times the given method was called.
func (m *memStore) callCount(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[name]
}

// fail makes the given method return an error from now on.
func (m *memStore) fail(name string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failOn[name] = err
}

func (m *memStore) InTx(ctx context.Context, fn func(ctx context.Context, s repository.Store) error) error {
	m.mu.Lock()
	if err := m.track("InTx"); err != nil {
		m.mu.Unlock()
		return err
	}
	nested := m.inTx
	m.inTx = true
	m.mu.Unlock()

	err := fn(ctx, m)

	if !nested {
		m.mu.Lock()
		m.inTx = false
		m.mu.Unlock()
	}
	return err
}

func (m *memStore) CreateProduct(_ context.Context, p models.Product) (models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateProduct"); err != nil {
		return models.Product{}, err
	}
	p.CreatedAt, p.UpdatedAt = creationTime, creationTime
	// The maps are walked by key: the record structs are large and copying by
	// value carries a few hundred bytes on every round.
	for id := range m.products {
		if existing := m.products[id]; existing.DeletedAt == nil && existing.Handle == p.Handle {
			return models.Product{}, errors.Conflict("product_handle_taken",
				"the handle is already in use: %s", p.Handle)
		}
	}
	m.products[p.ID] = p
	return p, nil
}

func (m *memStore) GetProduct(_ context.Context, id string) (models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetProduct"); err != nil {
		return models.Product{}, err
	}
	p, ok := m.products[id]
	if !ok || p.DeletedAt != nil {
		return models.Product{}, errors.NotFound("product_not_found", "the product was not found: %s", id)
	}
	return p, nil
}

// GetProductForUpdate cannot take a lock; the fake repository runs in a single
// goroutine and the real effect of the lock (being lined up against a concurrent
// delete) can only be tested against a real database. What is tested here is
// that the check is done INSIDE THE TRANSACTION.
func (m *memStore) GetProductForUpdate(_ context.Context, id string) (models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetProductForUpdate"); err != nil {
		return models.Product{}, err
	}
	if !m.inTx {
		return models.Product{}, errors.Internal("product_test_no_tx",
			"GetProductForUpdate was called outside a transaction; the lock lines nothing up")
	}
	p, ok := m.products[id]
	if !ok || p.DeletedAt != nil {
		return models.Product{}, errors.NotFound("product_not_found", "the product was not found: %s", id)
	}
	return p, nil
}

func (m *memStore) GetProductByHandle(_ context.Context, handle string) (models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetProductByHandle"); err != nil {
		return models.Product{}, err
	}
	for id := range m.products {
		if p := m.products[id]; p.DeletedAt == nil && p.Handle == handle {
			return p, nil
		}
	}
	return models.Product{}, errors.NotFound("product_not_found", "the product was not found: %s", handle)
}

// liveProducts returns the products that are not deleted, ordered by id.
func (m *memStore) liveProducts() []models.Product {
	out := make([]models.Product, 0, len(m.products))
	for id := range m.products {
		if p := m.products[id]; p.DeletedAt == nil {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b models.Product) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// matches reports whether the product matches the filter.
func (m *memStore) matches(p *models.Product, f repository.ProductFilter) bool {
	switch {
	case f.Status != nil && p.Status.String() != *f.Status:
		return false
	case f.Handle != nil && p.Handle != *f.Handle:
		return false
	case f.CollectionID != nil && (p.CollectionID == nil || *p.CollectionID != *f.CollectionID):
		return false
	case f.Search != nil && !strings.Contains(strings.ToLower(p.Title), strings.ToLower(*f.Search)):
		return false
	case f.CategoryID != nil && !slices.Contains(m.productCats[p.ID], *f.CategoryID):
		return false
	case f.TagID != nil && !slices.Contains(m.productTags[p.ID], *f.TagID):
		return false
	default:
		return m.visibleIn(p.ID, f.SalesChannelIDs)
	}
}

// visibleIn is the fake counterpart of the sales channel visibility rule.
//
// The real rule is in SQL (repository/saleschannel.go); repeating it here is
// unavoidable because the fake repository has no database. That the two do not
// drift apart is proven by the integration tests: the same scenarios run both
// here and against a real PostgreSQL.
//
// A nil slice means "no filtering", an empty slice means "there is an identity
// but it has no channels"; for the rationale of the distinction see
// repository.ProductFilter.SalesChannelIDs.
func (m *memStore) visibleIn(productID string, channelIDs []string) bool {
	if channelIDs == nil {
		return true
	}
	assigned := m.assignedChannels(productID)
	if len(assigned) == 0 {
		return true
	}
	for _, id := range assigned {
		if slices.Contains(channelIDs, id) {
			return true
		}
	}
	return false
}

// assignedChannels reads the channels the product is linked to from the fake
// link service.
func (m *memStore) assignedChannels(productID string) []string {
	if m.links == nil {
		return nil
	}
	return m.links.linked(service.LinkProductSalesChannel, productID)
}

// ProductVisibleInSalesChannels is the visibility check of the single storefront
// endpoint.
func (m *memStore) ProductVisibleInSalesChannels(
	_ context.Context,
	productID string,
	salesChannelIDs []string,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ProductVisibleInSalesChannels"); err != nil {
		return false, err
	}
	return m.visibleIn(productID, salesChannelIDs), nil
}

// VisibleProductIDs computes the batch visibility with the singular rule itself.
//
// The fake guarantees that the two methods give the SAME answer: had it written
// a separate rule, it could hide in the tests a behavior that drifts apart in
// the real repository.
func (m *memStore) VisibleProductIDs(
	_ context.Context,
	productIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.track("VisibleProductIDs"); err != nil {
		return nil, err
	}

	visible := make(map[string]struct{}, len(productIDs))

	for _, id := range productIDs {
		if m.visibleIn(id, salesChannelIDs) {
			visible[id] = struct{}{}
		}
	}

	return visible, nil
}

func (m *memStore) ListProducts(_ context.Context, f repository.ProductFilter) ([]models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListProducts"); err != nil {
		return nil, err
	}

	all := m.liveProducts()
	out := make([]models.Product, 0, len(all))
	for i := range all {
		if m.matches(&all[i], f) {
			out = append(out, all[i])
		}
	}
	return sliceWindow(out, f.Limit, f.Offset), nil
}

func (m *memStore) CountProducts(_ context.Context, f repository.ProductFilter) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountProducts"); err != nil {
		return 0, err
	}

	all := m.liveProducts()
	count := 0
	for i := range all {
		if m.matches(&all[i], f) {
			count++
		}
	}
	return count, nil
}

func (m *memStore) ListProductsByIDs(_ context.Context, ids []string) ([]models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListProductsByIDs"); err != nil {
		return nil, err
	}

	out := make([]models.Product, 0, len(ids))
	for _, id := range ids {
		if p, ok := m.products[id]; ok && p.DeletedAt == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memStore) UpdateProduct(_ context.Context, id string, patch repository.ProductPatch) (models.Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UpdateProduct"); err != nil {
		return models.Product{}, err
	}
	p, ok := m.products[id]
	if !ok || p.DeletedAt != nil {
		return models.Product{}, errors.NotFound("product_not_found", "the product was not found: %s", id)
	}

	if patch.Handle != nil {
		p.Handle = *patch.Handle
	}
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	if patch.Status != nil {
		p.Status = models.Status(*patch.Status)
	}
	if patch.Subtitle != nil {
		p.Subtitle = patch.Subtitle
	}
	if patch.Metadata != nil {
		p.Metadata = patch.Metadata
	}
	m.products[id] = p
	return p, nil
}

func (m *memStore) SoftDeleteProduct(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteProduct"); err != nil {
		return err
	}
	p, ok := m.products[id]
	if !ok || p.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the product was not found: %s", id)
	}
	p.DeletedAt = &deletionTime
	m.products[id] = p
	return nil
}

func (m *memStore) SoftDeleteProductChildren(_ context.Context, productID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteProductChildren"); err != nil {
		return err
	}
	for id := range m.variants {
		if v := m.variants[id]; v.ProductID == productID && v.DeletedAt == nil {
			v.DeletedAt = &deletionTime
			m.variants[id] = v
		}
	}
	for id := range m.options {
		if o := m.options[id]; o.ProductID == productID && o.DeletedAt == nil {
			o.DeletedAt = &deletionTime
			m.options[id] = o
		}
	}
	return nil
}

func (m *memStore) ListVariantIDsByProduct(_ context.Context, productID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListVariantIDsByProduct"); err != nil {
		return nil, err
	}

	out := []string{}
	for id := range m.variants {
		if v := m.variants[id]; v.ProductID == productID && v.DeletedAt == nil {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (m *memStore) CreateVariant(_ context.Context, v models.Variant) (models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateVariant"); err != nil {
		return models.Variant{}, err
	}
	v.CreatedAt, v.UpdatedAt = creationTime, creationTime
	m.variants[v.ID] = v
	return v, nil
}

func (m *memStore) GetVariant(_ context.Context, id string) (models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetVariant"); err != nil {
		return models.Variant{}, err
	}
	v, ok := m.variants[id]
	if !ok || v.DeletedAt != nil {
		return models.Variant{}, errors.NotFound("product_not_found", "the variant was not found: %s", id)
	}
	return v, nil
}

// liveVariants returns the variants that are not deleted, in (product, rank, id)
// order.
func (m *memStore) liveVariants() []models.Variant {
	out := make([]models.Variant, 0, len(m.variants))
	for id := range m.variants {
		if v := m.variants[id]; v.DeletedAt == nil {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b models.Variant) int {
		if a.ProductID != b.ProductID {
			return strings.Compare(a.ProductID, b.ProductID)
		}
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (m *memStore) ListVariants(_ context.Context, f repository.VariantFilter) ([]models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListVariants"); err != nil {
		return nil, err
	}

	all := m.liveVariants()
	out := make([]models.Variant, 0, len(all))
	for i := range all {
		if f.ProductID == nil || all[i].ProductID == *f.ProductID {
			out = append(out, all[i])
		}
	}
	return sliceWindow(out, f.Limit, f.Offset), nil
}

func (m *memStore) CountVariants(_ context.Context, f repository.VariantFilter) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountVariants"); err != nil {
		return 0, err
	}

	all := m.liveVariants()
	count := 0
	for i := range all {
		if f.ProductID == nil || all[i].ProductID == *f.ProductID {
			count++
		}
	}
	return count, nil
}

func (m *memStore) ListVariantsByProductIDs(_ context.Context, productIDs []string) ([]models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListVariantsByProductIDs"); err != nil {
		return nil, err
	}

	all := m.liveVariants()
	out := make([]models.Variant, 0, len(all))
	for i := range all {
		if slices.Contains(productIDs, all[i].ProductID) {
			out = append(out, all[i])
		}
	}
	return out, nil
}

func (m *memStore) ListVariantsByIDs(_ context.Context, ids []string) ([]models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListVariantsByIDs"); err != nil {
		return nil, err
	}

	all := m.liveVariants()
	out := make([]models.Variant, 0, len(all))
	for i := range all {
		if slices.Contains(ids, all[i].ID) {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// VisibleVariantIDs computes the variant visibility with the PRODUCT rule
// itself.
//
// The fake does exactly what the real repository does: a variant has no channel
// of its own, the product it belongs to has one (see
// repository/saleschannel.go). Had it written a separate rule, it could hide in
// the tests a behavior that drifts apart from the real SQL.
//
// A deleted variant counts as invisible; [memStore.liveVariants] returns only
// the living ones anyway.
func (m *memStore) VisibleVariantIDs(
	_ context.Context,
	variantIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.track("VisibleVariantIDs"); err != nil {
		return nil, err
	}

	visible := make(map[string]struct{}, len(variantIDs))

	// The loop is walked by index: the variant struct is large and copying by
	// value carries a few hundred bytes on every round for nothing.
	all := m.liveVariants()
	for i := range all {
		if !slices.Contains(variantIDs, all[i].ID) {
			continue
		}
		if m.visibleIn(all[i].ProductID, salesChannelIDs) {
			visible[all[i].ID] = struct{}{}
		}
	}

	return visible, nil
}

func (m *memStore) UpdateVariant(_ context.Context, id string, patch repository.VariantPatch) (models.Variant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UpdateVariant"); err != nil {
		return models.Variant{}, err
	}
	v, ok := m.variants[id]
	if !ok || v.DeletedAt != nil {
		return models.Variant{}, errors.NotFound("product_not_found", "the variant was not found: %s", id)
	}

	if patch.Title != nil {
		v.Title = *patch.Title
	}
	if patch.SKU != nil {
		v.SKU = patch.SKU
	}
	if patch.Rank != nil {
		v.Rank = *patch.Rank
	}
	m.variants[id] = v
	return v, nil
}

func (m *memStore) SoftDeleteVariant(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteVariant"); err != nil {
		return err
	}
	v, ok := m.variants[id]
	if !ok || v.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the variant was not found: %s", id)
	}
	v.DeletedAt = &deletionTime
	m.variants[id] = v
	return nil
}

func (m *memStore) CreateOption(_ context.Context, o models.Option) (models.Option, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateOption"); err != nil {
		return models.Option{}, err
	}
	// The option values are SEPARATE rows; RETURNING does not return them.
	o.Values = nil
	o.CreatedAt, o.UpdatedAt = creationTime, creationTime
	m.options[o.ID] = o
	return o, nil
}

func (m *memStore) GetOption(_ context.Context, id string) (models.Option, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetOption"); err != nil {
		return models.Option{}, err
	}
	o, ok := m.options[id]
	if !ok || o.DeletedAt != nil {
		return models.Option{}, errors.NotFound("product_not_found", "the option was not found: %s", id)
	}
	return o, nil
}

func (m *memStore) ListOptionsByProductIDs(_ context.Context, productIDs []string) ([]models.Option, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListOptionsByProductIDs"); err != nil {
		return nil, err
	}

	out := make([]models.Option, 0, len(m.options))
	for id := range m.options {
		if o := m.options[id]; o.DeletedAt == nil && slices.Contains(productIDs, o.ProductID) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b models.Option) int {
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *memStore) SoftDeleteOption(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteOption"); err != nil {
		return err
	}
	o, ok := m.options[id]
	if !ok || o.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the option was not found: %s", id)
	}
	o.DeletedAt = &deletionTime
	m.options[id] = o
	return nil
}

func (m *memStore) CreateOptionValue(_ context.Context, v models.OptionValue) (models.OptionValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateOptionValue"); err != nil {
		return models.OptionValue{}, err
	}
	// OptionTitle is not a column; the real repository cannot return it from
	// RETURNING.
	v.OptionTitle = ""
	v.CreatedAt, v.UpdatedAt = creationTime, creationTime
	m.values[v.ID] = v
	return v, nil
}

func (m *memStore) ListOptionValuesByOptionIDs(_ context.Context, optionIDs []string) ([]models.OptionValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListOptionValuesByOptionIDs"); err != nil {
		return nil, err
	}

	out := make([]models.OptionValue, 0, len(m.values))
	for id := range m.values {
		if v := m.values[id]; v.DeletedAt == nil && slices.Contains(optionIDs, v.OptionID) {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b models.OptionValue) int {
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *memStore) ListOptionValuesByIDs(_ context.Context, ids []string) ([]models.OptionValueRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListOptionValuesByIDs"); err != nil {
		return nil, err
	}

	out := make([]models.OptionValueRef, 0, len(ids))
	for _, id := range ids {
		value, ok := m.values[id]
		if !ok || value.DeletedAt != nil {
			continue
		}
		option, ok := m.options[value.OptionID]
		if !ok || option.DeletedAt != nil {
			continue
		}
		value.OptionTitle = option.Title
		out = append(out, models.OptionValueRef{OptionValue: value, ProductID: option.ProductID})
	}
	return out, nil
}

func (m *memStore) SetVariantOptionValue(_ context.Context, variantID, optionID, valueID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SetVariantOptionValue"); err != nil {
		return err
	}
	if m.variantValues[variantID] == nil {
		m.variantValues[variantID] = map[string]string{}
	}
	m.variantValues[variantID][optionID] = valueID
	return nil
}

func (m *memStore) DeleteVariantOptionValues(_ context.Context, variantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("DeleteVariantOptionValues"); err != nil {
		return err
	}
	delete(m.variantValues, variantID)
	return nil
}

func (m *memStore) ListVariantOptionValues(_ context.Context, variantIDs []string) (map[string][]models.OptionValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListVariantOptionValues"); err != nil {
		return nil, err
	}

	out := map[string][]models.OptionValue{}
	for _, variantID := range variantIDs {
		optionIDs := make([]string, 0, len(m.variantValues[variantID]))
		for optionID := range m.variantValues[variantID] {
			optionIDs = append(optionIDs, optionID)
		}
		slices.Sort(optionIDs)

		for _, optionID := range optionIDs {
			value := m.values[m.variantValues[variantID][optionID]]
			value.OptionTitle = m.options[optionID].Title
			out[variantID] = append(out[variantID], value)
		}
	}
	return out, nil
}

func (m *memStore) CreateCollection(_ context.Context, c models.Collection) (models.Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateCollection"); err != nil {
		return models.Collection{}, err
	}
	c.CreatedAt, c.UpdatedAt = creationTime, creationTime
	m.collections[c.ID] = c
	return c, nil
}

func (m *memStore) GetCollection(_ context.Context, id string) (models.Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCollection"); err != nil {
		return models.Collection{}, err
	}
	c, ok := m.collections[id]
	if !ok || c.DeletedAt != nil {
		return models.Collection{}, errors.NotFound("product_not_found", "the collection was not found: %s", id)
	}
	return c, nil
}

func (m *memStore) ListCollections(_ context.Context, limit, offset int) ([]models.Collection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCollections"); err != nil {
		return nil, err
	}

	out := make([]models.Collection, 0, len(m.collections))
	for _, c := range m.collections {
		if c.DeletedAt == nil {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b models.Collection) int { return strings.Compare(a.ID, b.ID) })
	return sliceWindow(out, limit, offset), nil
}

func (m *memStore) CountCollections(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountCollections"); err != nil {
		return 0, err
	}
	return len(m.collections), nil
}

func (m *memStore) CreateCategory(_ context.Context, c models.Category) (models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateCategory"); err != nil {
		return models.Category{}, err
	}
	c.CreatedAt, c.UpdatedAt = creationTime, creationTime
	m.categories[c.ID] = c
	return c, nil
}

func (m *memStore) GetCategory(_ context.Context, id string) (models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetCategory"); err != nil {
		return models.Category{}, err
	}
	c, ok := m.categories[id]
	if !ok || c.DeletedAt != nil {
		return models.Category{}, errors.NotFound("product_not_found", "the category was not found: %s", id)
	}
	return c, nil
}

func (m *memStore) ListCategories(_ context.Context, f repository.CategoryFilter) ([]models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCategories"); err != nil {
		return nil, err
	}

	out := m.matchingCategories(f)
	slices.SortFunc(out, func(a, b models.Category) int { return strings.Compare(a.ID, b.ID) })
	return sliceWindow(out, f.Limit, f.Offset), nil
}

// matchingCategories applies the filter the SQL applies.
//
// It is shared by the listing and the count on purpose: the count used to
// return len(m.categories) whatever the filter said, so a test could see a
// page of one and a count of ten and call that correct — the fake disagreeing
// with the query it stands in for, which is the one thing a fake must not do.
func (m *memStore) matchingCategories(f repository.CategoryFilter) []models.Category {
	out := make([]models.Category, 0, len(m.categories))
	for id := range m.categories {
		c := m.categories[id]
		switch {
		case c.DeletedAt != nil:
			continue
		case f.ParentID != nil && (c.ParentID == nil || *c.ParentID != *f.ParentID):
			continue
		case f.PublicOnly && (!c.IsActive || c.IsInternal):
			continue
		}
		out = append(out, c)
	}

	return out
}

func (m *memStore) CountCategories(_ context.Context, f repository.CategoryFilter) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountCategories"); err != nil {
		return 0, err
	}
	return len(m.matchingCategories(f)), nil
}

func (m *memStore) CreateTag(_ context.Context, t models.Tag) (models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateTag"); err != nil {
		return models.Tag{}, err
	}
	t.CreatedAt, t.UpdatedAt = creationTime, creationTime
	m.tags[t.ID] = t
	return t, nil
}

func (m *memStore) GetTagByValue(_ context.Context, value string) (models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetTagByValue"); err != nil {
		return models.Tag{}, err
	}
	for _, t := range m.tags {
		if t.DeletedAt == nil && t.Value == value {
			return t, nil
		}
	}
	return models.Tag{}, errors.NotFound("product_not_found", "the tag was not found: %s", value)
}

func (m *memStore) ListTags(_ context.Context, limit, offset int) ([]models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListTags"); err != nil {
		return nil, err
	}

	out := make([]models.Tag, 0, len(m.tags))
	for _, t := range m.tags {
		if t.DeletedAt == nil {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b models.Tag) int { return strings.Compare(a.Value, b.Value) })
	return sliceWindow(out, limit, offset), nil
}

func (m *memStore) CountTags(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountTags"); err != nil {
		return 0, err
	}
	return len(m.tags), nil
}

func (m *memStore) SetProductTags(_ context.Context, productID string, tagIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SetProductTags"); err != nil {
		return err
	}
	m.productTags[productID] = slices.Clone(tagIDs)
	return nil
}

func (m *memStore) SetProductCategories(_ context.Context, productID string, categoryIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SetProductCategories"); err != nil {
		return err
	}
	m.productCats[productID] = slices.Clone(categoryIDs)
	return nil
}

func (m *memStore) ListTagsByProductIDs(_ context.Context, productIDs []string) (map[string][]models.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListTagsByProductIDs"); err != nil {
		return nil, err
	}

	out := map[string][]models.Tag{}
	for _, productID := range productIDs {
		for _, tagID := range m.productTags[productID] {
			if tag, ok := m.tags[tagID]; ok {
				out[productID] = append(out[productID], tag)
			}
		}
	}
	return out, nil
}

func (m *memStore) ListCategoriesByProductIDs(_ context.Context, productIDs []string) (map[string][]models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCategoriesByProductIDs"); err != nil {
		return nil, err
	}

	out := map[string][]models.Category{}
	for _, productID := range productIDs {
		for _, categoryID := range m.productCats[productID] {
			if category, ok := m.categories[categoryID]; ok {
				out[productID] = append(out[productID], category)
			}
		}
	}
	return out, nil
}

func (m *memStore) CreateImage(_ context.Context, img models.Image) (models.Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateImage"); err != nil {
		return models.Image{}, err
	}
	img.CreatedAt, img.UpdatedAt = creationTime, creationTime
	m.images[img.ID] = img
	return img, nil
}

func (m *memStore) ListImagesByProductIDs(_ context.Context, productIDs []string) (map[string][]models.Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListImagesByProductIDs"); err != nil {
		return nil, err
	}

	out := map[string][]models.Image{}
	for id := range m.images {
		img := m.images[id]
		if img.DeletedAt == nil && slices.Contains(productIDs, img.ProductID) {
			out[img.ProductID] = append(out[img.ProductID], img)
		}
	}
	for productID := range out {
		slices.SortFunc(out[productID], func(a, b models.Image) int {
			if a.Rank != b.Rank {
				return int(a.Rank - b.Rank)
			}
			return strings.Compare(a.ID, b.ID)
		})
	}
	return out, nil
}

// ListImagesByIDs returns the live images among the given ids.
//
// The SOFT DELETE filter is imitated on purpose: the real query carries
// "deleted_at IS NULL", and a fake that returned deleted rows would let a test
// "prove" that a deleted product still claims its upload.
func (m *memStore) ListImagesByIDs(_ context.Context, imageIDs []string) ([]models.Image, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListImagesByIDs"); err != nil {
		return nil, err
	}

	out := []models.Image{}
	for _, id := range imageIDs {
		img, ok := m.images[id]
		if !ok || img.DeletedAt != nil {
			continue
		}
		out = append(out, img)
	}
	slices.SortFunc(out, func(a, b models.Image) int {
		if a.ProductID != b.ProductID {
			return strings.Compare(a.ProductID, b.ProductID)
		}
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *memStore) DeleteImagesByProduct(_ context.Context, productID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("DeleteImagesByProduct"); err != nil {
		return err
	}
	for id := range m.images {
		img := m.images[id]
		if img.ProductID == productID {
			img.DeletedAt = &deletionTime
			m.images[id] = img
		}
	}
	return nil
}

// sliceWindow applies the limit/offset window.
func sliceWindow[T any](items []T, limit, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	items = items[offset:]
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	return items
}
