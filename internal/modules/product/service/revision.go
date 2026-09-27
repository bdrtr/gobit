package service

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// codeRevisionInvalid reports a revision snapshot that cannot be encoded or
// read back; it is a fault of this module, not of the caller.
const codeRevisionInvalid = "product_revision_invalid"

// volatileFields are the keys a revision's snapshot leaves out, at every depth:
// a write stamps them whether or not it changed anything.
var volatileFields = []string{"created_at", "updated_at", "deleted_at"}

// RestoreResult is a restored product, with what the revision named that no
// longer stands (ADR 0221).
type RestoreResult struct {
	Product models.Product
	// Dropped names the collection, type, tags, categories, attributes and
	// options the revision held that were removed since, each as kind:id, in
	// that order; the restore left them out.
	Dropped []string
}

// revise runs a write to one product's content in a transaction that holds the
// product's row lock, and records a revision when the product's admin view
// changed (ADR 0221).
//
// A product that has no revision yet, written before revisions began, first
// gets the view before the write as its first revision, so the history starts
// with what the product was.
func (s *Service) revise(
	ctx context.Context, productID string, write func(ctx context.Context, tx repository.Store) error,
) error {
	return s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		product, err := tx.GetProductForUpdate(ctx, productID)
		if err != nil {
			return err
		}
		if product.Version == 0 {
			if err := s.recordRevision(ctx, tx, productID); err != nil {
				return err
			}
		}
		if err := write(ctx, tx); err != nil {
			return err
		}
		return s.recordRevision(ctx, tx, productID)
	})
}

// recordRevision reads the product's admin view in the transaction and
// appends it as the next revision, unless it is the latest one's.
func (s *Service) recordRevision(ctx context.Context, tx repository.Store, productID string) error {
	product, err := tx.GetProduct(ctx, productID)
	if err != nil {
		return err
	}
	products := []models.Product{product}
	if err := s.attachRelationsFrom(ctx, tx, products); err != nil {
		return err
	}
	snapshot, err := revisionSnapshot(products[0])
	if err != nil {
		return err
	}

	latest, ok, err := tx.LatestProductRevision(ctx, productID)
	if err != nil {
		return err
	}
	version, changed := int64(1), []string{}
	if ok {
		previous, err := canonicalJSON(latest.Snapshot)
		if err != nil {
			return err
		}
		if bytes.Equal(previous, snapshot) {
			return nil
		}
		version = latest.Version + 1
		if changed, err = changedFields(previous, snapshot); err != nil {
			return err
		}
	}

	rev := models.Revision{
		ID: newID(prefixRevision), ProductID: productID, Version: version, RecordedAt: s.now(),
		Changed: changed, Snapshot: snapshot,
	}
	if id := corehttp.RequestIDFromContext(ctx); id != "" {
		rev.RequestID = &id
	}
	return tx.AppendProductRevision(ctx, rev)
}

// revisionSnapshot is the product's admin view as a revision keeps it: JSON
// with sorted keys and no timestamps.
func revisionSnapshot(product models.Product) (json.RawMessage, error) {
	raw, err := json.Marshal(product)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid, "product %s could not be encoded", product.ID)
	}
	return canonicalJSON(raw)
}

// canonicalJSON re-encodes a JSON value with sorted keys and without the
// volatile fields, so a snapshot read back from JSONB, whose key order and
// number spelling are its own, compares equal to the one it was written from.
func canonicalJSON(raw []byte) (json.RawMessage, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid, "a revision snapshot could not be decoded")
	}
	out, err := json.Marshal(withoutVolatile(value))
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid, "a revision snapshot could not be encoded")
	}
	return out, nil
}

// withoutVolatile drops the volatile fields from every object in the value.
func withoutVolatile(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range volatileFields {
			delete(v, key)
		}
		for key, inner := range v {
			v[key] = withoutVolatile(inner)
		}
		return v
	case []any:
		for i := range v {
			v[i] = withoutVolatile(v[i])
		}
		return v
	default:
		return v
	}
}

// changedFields names the top-level fields whose values differ between two
// canonical snapshots, a field present in one only included, in name order.
func changedFields(previous, next []byte) ([]string, error) {
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal(previous, &before); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid, "a revision snapshot is not an object")
	}
	if err := json.Unmarshal(next, &after); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid, "a revision snapshot is not an object")
	}
	keys := map[string]bool{}
	for key := range maps.Keys(before) {
		keys[key] = true
	}
	for key := range maps.Keys(after) {
		keys[key] = true
	}
	changed := []string{}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if !bytes.Equal(before[key], after[key]) {
			changed = append(changed, key)
		}
	}
	return changed, nil
}

// ListRevisions returns a page of a product's revisions, newest first and
// without their snapshots.
func (s *Service) ListRevisions(
	ctx context.Context, productID string, limit, offset int,
) (ListResult[models.Revision], error) {
	if _, err := requireID("id", productID); err != nil {
		return ListResult[models.Revision]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return ListResult[models.Revision]{}, err
	}
	if _, err := s.repo.GetProduct(ctx, productID); err != nil {
		return ListResult[models.Revision]{}, err
	}
	items, count, err := s.repo.ListProductRevisions(ctx, productID, limit, offset)
	if err != nil {
		return ListResult[models.Revision]{}, err
	}
	total := int(count)
	return ListResult[models.Revision]{Items: items, Count: &total, Offset: offset, Limit: limit}, nil
}

// GetRevision returns one revision of a product with its snapshot.
func (s *Service) GetRevision(ctx context.Context, productID string, version int64) (models.Revision, error) {
	if _, err := requireID("id", productID); err != nil {
		return models.Revision{}, err
	}
	if version < 1 {
		return models.Revision{}, invalid("a revision's version is 1 or more (given: %d)", version)
	}
	if _, err := s.repo.GetProduct(ctx, productID); err != nil {
		return models.Revision{}, err
	}
	return s.repo.GetProductRevision(ctx, productID, version)
}

// RestoreRevision writes a revision's descriptive content back to the product
// as a new revision (ADR 0221): its own fields, collection, type, tags,
// categories and attribute values.
//
// The status, the schedule, the variants, the options and the images are not
// restored: a status and a schedule have their own writes, and the others are
// records other modules hold by id. What the revision names that was removed
// since is left out and named in the result; a handle another product has taken
// since is refused.
func (s *Service) RestoreRevision(ctx context.Context, productID string, version int64) (RestoreResult, error) {
	rev, err := s.GetRevision(ctx, productID, version)
	if err != nil {
		return RestoreResult{}, err
	}
	var snapshot models.Product
	if err := json.Unmarshal(rev.Snapshot, &snapshot); err != nil {
		return RestoreResult{}, errors.Wrap(err, errors.KindInternal, codeRevisionInvalid,
			"revision %d of product %s could not be decoded", version, productID)
	}
	if err := s.ensureHandleFree(ctx, snapshot.Handle, productID); err != nil {
		return RestoreResult{}, err
	}

	var dropped []string
	err = s.revise(ctx, productID, func(ctx context.Context, tx repository.Store) error {
		content, lost, err := s.restorableContent(ctx, tx, &snapshot)
		if err != nil {
			return err
		}
		dropped = lost
		if err := tx.RestoreProductContent(ctx, productID, content.own); err != nil {
			return err
		}
		if err := tx.SetProductTags(ctx, productID, content.tagIDs); err != nil {
			return err
		}
		if err := tx.SetProductCategories(ctx, productID, content.categoryIDs); err != nil {
			return err
		}
		return tx.SetProductAttributeValues(ctx, productID, content.attributes)
	})
	if err != nil {
		return RestoreResult{}, err
	}

	restored, err := s.GetProduct(ctx, productID)
	if err != nil {
		return RestoreResult{}, err
	}
	s.publishProductEvent(ctx, EventProductUpdated, restored.ID, restored.Status)
	return RestoreResult{Product: restored, Dropped: dropped}, nil
}

// restorable is what a restore writes.
type restorable struct {
	own         repository.ProductContent
	tagIDs      []string
	categoryIDs []string
	attributes  []repository.AttributeValueRow
}

// restorableContent turns a revision's snapshot into what a restore writes,
// leaving out and naming what was removed since.
func (s *Service) restorableContent(
	ctx context.Context, tx repository.Store, snapshot *models.Product,
) (restorable, []string, error) {
	out := restorable{own: repository.ProductContent{
		Handle: snapshot.Handle, Title: snapshot.Title, Subtitle: snapshot.Subtitle,
		Description: snapshot.Description, Thumbnail: snapshot.Thumbnail, Discountable: snapshot.Discountable,
		Weight: snapshot.Weight, Length: snapshot.Length, Height: snapshot.Height, Width: snapshot.Width,
		Material: snapshot.Material, OriginCountry: snapshot.OriginCountry,
		CollectionID: snapshot.CollectionID, TypeID: snapshot.TypeID, Metadata: snapshot.Metadata,
	}}
	dropped := []string{}

	if id := snapshot.CollectionID; id != nil {
		if _, err := tx.GetCollection(ctx, *id); errors.IsNotFound(err) {
			out.own.CollectionID, dropped = nil, append(dropped, "collection:"+*id)
		} else if err != nil {
			return restorable{}, nil, err
		}
	}
	if id := snapshot.TypeID; id != nil {
		if _, err := tx.GetProductType(ctx, *id); errors.IsNotFound(err) {
			out.own.TypeID, dropped = nil, append(dropped, "type:"+*id)
		} else if err != nil {
			return restorable{}, nil, err
		}
	}

	tagIDs := make([]string, 0, len(snapshot.Tags))
	for i := range snapshot.Tags {
		tagIDs = append(tagIDs, snapshot.Tags[i].ID)
	}
	liveTags, err := tx.LiveTagIDs(ctx, tagIDs)
	if err != nil {
		return restorable{}, nil, err
	}
	out.tagIDs, dropped = keepLive(tagIDs, liveTags, "tag", dropped)

	categoryIDs := make([]string, 0, len(snapshot.Categories))
	for i := range snapshot.Categories {
		categoryIDs = append(categoryIDs, snapshot.Categories[i].ID)
	}
	liveCategories, err := tx.LiveCategoryIDs(ctx, categoryIDs)
	if err != nil {
		return restorable{}, nil, err
	}
	out.categoryIDs, dropped = keepLive(categoryIDs, liveCategories, "category", dropped)

	out.attributes, dropped, err = s.restorableAttributes(ctx, tx, snapshot.Attributes, dropped)
	if err != nil {
		return restorable{}, nil, err
	}
	return out, dropped, nil
}

// keepLive keeps the ids that stand, in their order, and adds the others to
// dropped.
func keepLive(ids, live []string, kind string, dropped []string) (kept, stillDropped []string) {
	kept = make([]string, 0, len(ids))
	for _, id := range ids {
		if slices.Contains(live, id) {
			kept = append(kept, id)
		} else {
			dropped = append(dropped, kind+":"+id)
		}
	}
	return kept, dropped
}

// restorableAttributes turns a snapshot's attribute values into rows against
// today's definitions: a removed attribute or option is left out and named, and
// a select whose options were all removed is left out with them.
func (s *Service) restorableAttributes(
	ctx context.Context, tx repository.Store, values []models.ProductAttributeValue, dropped []string,
) ([]repository.AttributeValueRow, []string, error) {
	if len(values) == 0 {
		return nil, dropped, nil
	}
	definitions, err := tx.ListAttributes(ctx, models.MaxAttributes)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]*models.Attribute, len(definitions))
	for i := range definitions {
		byID[definitions[i].ID] = &definitions[i]
	}

	var rows []repository.AttributeValueRow
	for i := range values {
		value := &values[i]
		attribute, ok := byID[value.AttributeID]
		if !ok {
			dropped = append(dropped, "attribute:"+value.AttributeID)
			continue
		}
		switch attribute.Kind {
		case models.AttributeSelect:
			for _, option := range value.Options {
				if !slices.ContainsFunc(attribute.Options, func(o models.AttributeOption) bool { return o.ID == option.ID }) {
					dropped = append(dropped, "option:"+option.ID)
					continue
				}
				id := option.ID
				rows = append(rows, repository.AttributeValueRow{AttributeID: attribute.ID, OptionID: &id})
			}
		case models.AttributeNumber:
			if value.Number != nil {
				rows = append(rows, repository.AttributeValueRow{AttributeID: attribute.ID, Number: value.Number})
			}
		default:
			if value.Boolean != nil {
				rows = append(rows, repository.AttributeValueRow{AttributeID: attribute.ID, Boolean: value.Boolean})
			}
		}
	}
	return rows, dropped, nil
}
