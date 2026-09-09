package service_test

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
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

	products map[string]models.Product
	variants map[string]models.Variant
	options  map[string]models.Option
	values   map[string]models.OptionValue
	// valuesFolded is the matching form per value id, standing in for the
	// value_folded column and its per-option unique index.
	valuesFolded map[string]string
	images       map[string]models.Image
	collections  map[string]models.Collection
	categories   map[string]models.Category
	tags         map[string]models.Tag

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

// liveOptionValues counts the values that are not deleted; with an empty
// optionID it counts them all.
//
// It reads the fake's own map rather than going through a repository method on
// purpose: what the cascade tests need to see is the state NO read exposes —
// a value that is still alive under a deleted option is invisible to every
// query in the module, which is exactly how it stayed unwritten for so long.
func (m *memStore) liveOptionValues(optionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id := range m.values {
		value := m.values[id]
		if value.DeletedAt != nil {
			continue
		}
		if optionID == "" || value.OptionID == optionID {
			n++
		}
	}
	return n
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
	case f.OptionValueFolded != nil && !m.offersOptionValue(p.ID, *f.OptionValueFolded):
		return false
	default:
		return m.visibleIn(p.ID, f.SalesChannelIDs)
	}
}

// offersOptionValue is the fake counterpart of the option-value filter.
//
// The real rule is an EXISTS in SQL (repository/saleschannel.go,
// optionValueFilterSQL); this repeats it because the fake has no database, and
// the same scenarios run against a real PostgreSQL in the integration package so
// the two cannot drift apart unnoticed.
//
// It compares the STORED matching form (valuesFolded, this fake's stand-in for
// the value_folded column) rather than folding the value again here. The
// difference shows in exactly the case ADR 0039's convergence pass is about: on
// a cluster whose SQL backfill left a stale form behind, the column and the Go
// fold disagree, and the filter matches what the COLUMN says. Folding here would
// hide that disagreement and report a filter working that the database answers
// differently.
//
// The soft-delete guard is on the value AND on its option, which is the half of
// the rule that is easy to leave out: deleting an option stamps the option and
// leaves its values standing.
func (m *memStore) offersOptionValue(productID, folded string) bool {
	for id := range m.values {
		value := m.values[id]
		if value.DeletedAt != nil {
			continue
		}

		option, ok := m.options[value.OptionID]
		if !ok || option.DeletedAt != nil || option.ProductID != productID {
			continue
		}
		if m.valuesFolded[value.ID] == folded {
			return true
		}
	}

	return false
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

	return sliceWindow(afterCursor(out, f.After), f.Limit, f.Offset), nil
}

// afterCursor is the fake counterpart of the keyset seek.
//
// The real one is a comparison on (created_at, id) that moves with the listing
// order (see repository.keysetSeek); this one compares the id alone, because
// [memStore.liveProducts] orders by id and a fake's seek has to agree with the
// fake's ORDER rather than with the database's.
//
// It exists because the fake used to IGNORE the cursor entirely, and that is
// invisible until something pages: a caller that walks the catalog one page at
// a time was handed page one every time, so a filter that pages -- the in-stock
// and price filters, which read a chunk, keep what matches and resume -- could
// return the same products for ever and no unit test would see it.
//
// An id the fake does not hold does NOT restart the walk: the comparison is
// "strictly after this id", so a cursor pointing at a deleted product resumes
// where it stood instead of at the top.
func afterCursor(items []models.Product, after corepage.Cursor) []models.Product {
	if after.IsZero() {
		return items
	}

	out := make([]models.Product, 0, len(items))
	for i := range items {
		if items[i].ID > after.ID {
			out = append(out, items[i])
		}
	}

	return out
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
			for valueID := range m.values {
				if v := m.values[valueID]; v.OptionID == id && v.DeletedAt == nil {
					v.DeletedAt = &deletionTime
					m.values[valueID] = v
				}
			}
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

func (m *memStore) SoftDeleteOptionValuesByOption(_ context.Context, optionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteOptionValuesByOption"); err != nil {
		return err
	}
	for id := range m.values {
		if v := m.values[id]; v.OptionID == optionID && v.DeletedAt == nil {
			v.DeletedAt = &deletionTime
			m.values[id] = v
		}
	}
	return nil
}

// CountVariantsUsingOptionValue counts the LIVE variants carrying the value.
//
// The liveness check is not decoration: variantValues keeps the binding of a
// deleted variant too, exactly as product_variant_option_value keeps its row,
// and a count that ignored it would refuse a delete the real query allows.
func (m *memStore) CountVariantsUsingOptionValue(_ context.Context, valueID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountVariantsUsingOptionValue"); err != nil {
		return 0, err
	}
	n := 0
	for variantID, byOption := range m.variantValues {
		if v, ok := m.variants[variantID]; !ok || v.DeletedAt != nil {
			continue
		}
		for _, id := range byOption {
			if id == valueID {
				n++
				break
			}
		}
	}
	return n, nil
}

func (m *memStore) SoftDeleteOptionValue(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteOptionValue"); err != nil {
		return err
	}
	v, ok := m.values[id]
	if !ok || v.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the option value was not found: %s", id)
	}
	v.DeletedAt = &deletionTime
	m.values[id] = v
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

	// The matching form is written BY THE INSERT, not by a later pass: the real
	// statement names value_folded and [repository.Repo.CreateOptionValue] fills
	// it from models.FoldOptionValue. Leaving it out here would make every
	// freshly created value invisible to the catalog's option-value filter in
	// unit tests while the database matched it perfectly.
	if m.valuesFolded == nil {
		m.valuesFolded = map[string]string{}
	}
	m.valuesFolded[v.ID] = models.FoldOptionValue(v.Value)

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

// CountCollections counts the LIVE collections.
//
// It counted len(m.collections) until the delete existed, which was the same
// divergence [memStore.matchingCategories] was written to end: the count
// disagreeing with the listing it stands next to.
func (m *memStore) CountCollections(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountCollections"); err != nil {
		return 0, err
	}
	n := 0
	for _, c := range m.collections {
		if c.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *memStore) SoftDeleteCollection(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteCollection"); err != nil {
		return err
	}
	c, ok := m.collections[id]
	if !ok || c.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the collection was not found: %s", id)
	}
	c.DeletedAt = &deletionTime
	m.collections[id] = c
	return nil
}

func (m *memStore) ClearCollectionProducts(_ context.Context, collectionID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ClearCollectionProducts"); err != nil {
		return 0, err
	}
	released := 0
	for id := range m.products {
		p := m.products[id]
		if p.DeletedAt != nil || p.CollectionID == nil || *p.CollectionID != collectionID {
			continue
		}
		p.CollectionID = nil
		m.products[id] = p
		released++
	}
	return released, nil
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

// UpdateCategory mimics the real statement, INCLUDING its refusal.
//
// The rule that a category may not be moved under itself or one of its own
// descendants lives in SQL (queries/taxonomy.sql, UpdateCategory) and this is a
// second implementation of it, which is a cost paid deliberately: a fake that
// ACCEPTED a move the database refuses would let a service test go green on a
// tree the real one will not store, and this repository has been bitten by a
// fake that disagreed with its subject before. What proves the real rule is
// TestACategoryCannotBeMovedUnderItsOwnDescendant in the integration suite;
// this only keeps the fake from lying about it.
func (m *memStore) UpdateCategory(
	_ context.Context, id string, in repository.UpdateCategory,
) (models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("UpdateCategory"); err != nil {
		return models.Category{}, err
	}

	// An unknown id answers with the REFUSAL and not with NotFound, because that
	// is what the real statement produces: it matches no row for a missing id
	// and for a refused move alike, and the repository cannot tell them apart.
	// The fake said NotFound here at first, and a mutation caught it — removing
	// the service's id resolution changed nothing in the unit tests while it
	// would have turned every missing category into "that move is refused"
	// against a database.
	current, ok := m.categories[id]
	if !ok || current.DeletedAt != nil {
		return models.Category{}, errors.Invalid("product_category_cycle",
			"the category (%s) could not be moved: the new parent is the category itself "+
				"or one of its descendants, or the tree above it is too deep to verify", id)
	}

	if in.ParentID != nil {
		// The walk goes UP from the new parent, exactly as the recursive term
		// does, and the bound is the statement's bound for the same reason: an
		// ancestry that already holds a ring must not spin here either.
		for step, at := 0, in.ParentID; at != nil; step++ {
			if *at == id || step >= 64 {
				return models.Category{}, errors.Invalid("product_category_cycle",
					"the category (%s) could not be moved: the new parent is the category "+
						"itself or one of its descendants, or the tree above it is too deep "+
						"to verify", id)
			}
			parent, found := m.categories[*at]
			if !found || parent.DeletedAt != nil {
				break
			}
			at = parent.ParentID
		}
	}

	if in.Name != nil {
		current.Name = *in.Name
	}
	if in.Handle != nil {
		current.Handle = *in.Handle
	}
	if in.Description != nil {
		current.Description = in.Description
	}
	switch {
	case in.ClearParent:
		current.ParentID = nil
	case in.ParentID != nil:
		current.ParentID = in.ParentID
	}
	if in.IsActive != nil {
		current.IsActive = *in.IsActive
	}
	if in.IsInternal != nil {
		current.IsInternal = *in.IsInternal
	}
	if in.Rank != nil {
		current.Rank = *in.Rank
	}
	current.UpdatedAt = creationTime
	m.categories[id] = current

	return current, nil
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

// ListCategoriesByIDs returns the named categories WITHOUT applying the
// listing's flags.
//
// The fake follows the real query on both halves that matter: a deleted
// category does not come back, and is_active/is_internal are NOT looked at (see
// Repo.ListCategoriesByIDs). Had the fake filtered them here, the category
// provider's id path would look narrower in the tests than it is in production
// and the re-check the provider does itself would be tested against a store
// that had already done it.
func (m *memStore) ListCategoriesByIDs(_ context.Context, ids []string) ([]models.Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListCategoriesByIDs"); err != nil {
		return nil, err
	}

	out := make([]models.Category, 0, len(ids))
	for _, id := range ids {
		if c, ok := m.categories[id]; ok && c.DeletedAt == nil {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b models.Category) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
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

func (m *memStore) CountChildCategories(_ context.Context, id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountChildCategories"); err != nil {
		return 0, err
	}
	n := 0
	for key := range m.categories {
		c := m.categories[key]
		if c.DeletedAt == nil && c.ParentID != nil && *c.ParentID == id {
			n++
		}
	}
	return n, nil
}

func (m *memStore) SoftDeleteCategory(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteCategory"); err != nil {
		return err
	}
	c, ok := m.categories[id]
	if !ok || c.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the category was not found: %s", id)
	}
	c.DeletedAt = &deletionTime
	m.categories[id] = c
	return nil
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

// CountTags counts the LIVE tags; see [memStore.CountCollections].
func (m *memStore) CountTags(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountTags"); err != nil {
		return 0, err
	}
	n := 0
	for _, t := range m.tags {
		if t.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *memStore) SoftDeleteTag(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SoftDeleteTag"); err != nil {
		return err
	}
	t, ok := m.tags[id]
	if !ok || t.DeletedAt != nil {
		return errors.NotFound("product_not_found", "the tag was not found: %s", id)
	}
	t.DeletedAt = &deletionTime
	m.tags[id] = t
	return nil
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

// optionValuePairs mirrors the observable behavior of the real vocabulary
// query: the product the value hangs from decides visibility, the soft-delete
// guard is applied at all THREE levels, the pairs are DISTINCT and the order is
// (title, value).
//
// A fake that skipped any of those would let a unit test pass on a rule the
// database does not follow, which is the failure this repository has already
// paid for once.
func (m *memStore) optionValuePairs(f repository.OptionValueFilter) []models.OptionValuePair {
	visible := repository.ProductFilter{Status: f.Status, SalesChannelIDs: f.SalesChannelIDs}

	seen := map[models.OptionValuePair]struct{}{}
	for id := range m.values {
		value := m.values[id]
		if value.DeletedAt != nil {
			continue
		}
		option, ok := m.options[value.OptionID]
		if !ok || option.DeletedAt != nil {
			continue
		}
		product, ok := m.products[option.ProductID]
		if !ok || product.DeletedAt != nil {
			continue
		}
		if !m.matches(&product, visible) {
			continue
		}
		seen[models.OptionValuePair{OptionTitle: option.Title, Value: value.Value}] = struct{}{}
	}

	out := make([]models.OptionValuePair, 0, len(seen))
	for pair := range seen {
		out = append(out, pair)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OptionTitle != out[j].OptionTitle {
			return out[i].OptionTitle < out[j].OptionTitle
		}

		return out[i].Value < out[j].Value
	})

	return out
}

func (m *memStore) ListOptionValues(_ context.Context, f repository.OptionValueFilter) ([]models.OptionValuePair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListOptionValues"); err != nil {
		return nil, err
	}

	return sliceWindow(m.optionValuePairs(f), f.Limit, f.Offset), nil
}

func (m *memStore) CountOptionValues(_ context.Context, f repository.OptionValueFilter) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CountOptionValues"); err != nil {
		return 0, err
	}

	return len(m.optionValuePairs(f)), nil
}

// ListNonAsciiOptionValuesForRefold serves the startup convergence from the
// values this fake holds, applying the same non-ASCII narrowing the query does.
func (m *memStore) ListNonAsciiOptionValuesForRefold(
	_ context.Context, afterID string, limit int32,
) ([]models.OptionValueHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]string, 0, len(m.values))
	for id := range m.values {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]models.OptionValueHandle, 0, limit)
	for _, id := range ids {
		if id <= afterID || int32(len(out)) >= limit {
			continue
		}

		value := m.values[id]
		if value.Value == "" || isASCII(value.Value) {
			continue
		}

		out = append(out, models.OptionValueHandle{
			ID:       value.ID,
			OptionID: value.OptionID,
			Value:    value.Value,
			Folded:   m.valuesFolded[value.ID],
		})
	}

	return out, nil
}

// SetOptionValueFolded records a matching form, refusing one another value in the
// same option already holds — which is the unique index this fake stands in for.
func (m *memStore) SetOptionValueFolded(_ context.Context, id, folded string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	target, known := m.values[id]
	if !known {
		return errors.NotFound("product_option_value_not_found", "no such option value")
	}

	// By key rather than by value: models.OptionValue is wide enough that gocritic
	// counts the per-iteration copy, and only two of its fields are read here.
	for otherID := range m.values {
		if otherID != id && m.values[otherID].OptionID == target.OptionID &&
			m.valuesFolded[otherID] == folded {
			return errors.Conflict("product_option_value_folded_conflict",
				"another value in this option already folds to %q", folded)
		}
	}

	if m.valuesFolded == nil {
		m.valuesFolded = map[string]string{}
	}
	m.valuesFolded[id] = folded

	return nil
}

// isASCII reports whether every byte is ASCII, the same narrowing the query's
// regex performs.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > 127 {
			return false
		}
	}

	return true
}

// setFolded seeds a stored matching form, standing in for what migration 000003's
// SQL backfill left in value_folded.
func (m *memStore) setFolded(id, folded string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.valuesFolded == nil {
		m.valuesFolded = map[string]string{}
	}
	m.valuesFolded[id] = folded
}

// foldedOf returns the stored matching form of a value.
func (m *memStore) foldedOf(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.valuesFolded[id]
}
