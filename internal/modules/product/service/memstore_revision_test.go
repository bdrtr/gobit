package service_test

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// The revision half of memStore (ADR 0221). It imitates queries/revision.sql;
// the real statements are proven in the integration package.

// revisionsOf returns a product's revisions; the caller holds m.mu.
func (m *memStore) revisionsOf(productID string) []models.Revision {
	if m.revisions == nil {
		m.revisions = map[string][]models.Revision{}
	}
	return m.revisions[productID]
}

func (m *memStore) AppendProductRevision(_ context.Context, rev models.Revision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("AppendProductRevision"); err != nil {
		return err
	}
	product, ok := m.products[rev.ProductID]
	if !ok || product.DeletedAt != nil {
		return errors.NotFound("product_not_found", "product not found: %s", rev.ProductID)
	}
	existing := m.revisionsOf(rev.ProductID)
	if slices.ContainsFunc(existing, func(r models.Revision) bool { return r.Version == rev.Version }) {
		return errors.Conflict("product_conflict", "revision %d of %s exists", rev.Version, rev.ProductID)
	}
	m.revisions[rev.ProductID] = append(existing, rev)
	product.Version = rev.Version
	m.products[rev.ProductID] = product
	return nil
}

func (m *memStore) LatestProductRevision(_ context.Context, productID string) (models.Revision, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("LatestProductRevision"); err != nil {
		return models.Revision{}, false, err
	}
	revisions := m.revisionsOf(productID)
	if len(revisions) == 0 {
		return models.Revision{}, false, nil
	}
	return revisions[len(revisions)-1], true, nil
}

func (m *memStore) GetProductRevision(_ context.Context, productID string, version int64) (models.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("GetProductRevision"); err != nil {
		return models.Revision{}, err
	}
	for _, rev := range m.revisionsOf(productID) {
		if rev.Version == version {
			return rev, nil
		}
	}
	return models.Revision{}, errors.NotFound("product_not_found", "product %s has no revision %d", productID, version)
}

func (m *memStore) ListProductRevisions(
	_ context.Context, productID string, limit, offset int,
) ([]models.Revision, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListProductRevisions"); err != nil {
		return nil, 0, err
	}
	revisions := slices.Clone(m.revisionsOf(productID))
	slices.Reverse(revisions)
	out := []models.Revision{}
	for i := offset; i < len(revisions) && len(out) < limit; i++ {
		rev := revisions[i]
		rev.Snapshot = nil
		out = append(out, rev)
	}
	return out, int64(len(revisions)), nil
}

func (m *memStore) RestoreProductContent(_ context.Context, productID string, c repository.ProductContent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("RestoreProductContent"); err != nil {
		return err
	}
	p, ok := m.products[productID]
	if !ok || p.DeletedAt != nil {
		return errors.NotFound("product_not_found", "product not found: %s", productID)
	}
	p.Handle, p.Title, p.Subtitle, p.Description, p.Thumbnail = c.Handle, c.Title, c.Subtitle, c.Description, c.Thumbnail
	p.Discountable, p.Weight, p.Length, p.Height, p.Width = c.Discountable, c.Weight, c.Length, c.Height, c.Width
	p.Material, p.OriginCountry, p.CollectionID, p.TypeID = c.Material, c.OriginCountry, c.CollectionID, c.TypeID
	p.Metadata = c.Metadata
	m.products[productID] = p
	return nil
}

func (m *memStore) LiveTagIDs(_ context.Context, ids []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("LiveTagIDs"); err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if tag, ok := m.tags[id]; ok && tag.DeletedAt == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *memStore) LiveCategoryIDs(_ context.Context, ids []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("LiveCategoryIDs"); err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		if category, ok := m.categories[id]; ok && category.DeletedAt == nil {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *memStore) OptionValueProductID(_ context.Context, valueID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("OptionValueProductID"); err != nil {
		return "", err
	}
	value, ok := m.values[valueID]
	if ok && value.DeletedAt == nil {
		if option, ok := m.options[value.OptionID]; ok && option.DeletedAt == nil {
			return option.ProductID, nil
		}
	}
	return "", errors.NotFound("product_not_found", "option value not found: %s", valueID)
}
