package repository

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// attributeCountLockClass is the advisory lock class of the attribute count
// (ADR 0219); the key space is one across the database, and internal/arch holds
// the classes apart.
const attributeCountLockClass int64 = 7

// AttributeCountLockKey is the one lock of its class: the count is one number.
// It is exported for the test that holds it from a second transaction.
const AttributeCountLockKey int64 = attributeCountLockClass << 32

// codeAttributeLimit reports a definition or an option past its bound.
const codeAttributeLimit = "product_attribute_limit_reached"

// AttributeValueRow is one row of a product's attribute values: an option, or
// a number, or a boolean.
type AttributeValueRow struct {
	AttributeID string
	OptionID    *string
	Number      *float64
	Boolean     *bool
}

// CreateAttribute writes a definition and its options, while fewer than limit
// definitions stand; the writers that could each add one are serialized first.
func (r *Repo) CreateAttribute(ctx context.Context, a models.Attribute, limit int) (models.Attribute, error) {
	var out models.Attribute
	err := r.inTx(ctx, func(ctx context.Context, repo *Repo) error {
		if _, err := repo.db.Exec(ctx, advisoryLockSQL, AttributeCountLockKey); err != nil {
			return wrapDB(err, "could not serialize the attribute count")
		}
		count, err := repo.q.CountAttributes(ctx)
		if err != nil {
			return wrapDB(err, "could not count the attributes")
		}
		if int(count) >= limit {
			return errors.Conflict(codeAttributeLimit, "%d attributes are defined already, the most there can be", count)
		}
		row, err := repo.q.InsertAttribute(ctx, productdb.InsertAttributeParams{
			ID: a.ID, Handle: a.Handle, Title: a.Title, Kind: a.Kind, Rank: a.Rank,
		})
		if err != nil {
			return wrapDB(err, "could not create attribute (%s)", a.Handle)
		}
		out = toAttribute(row)
		for _, o := range a.Options {
			option, err := repo.q.InsertAttributeOption(ctx, productdb.InsertAttributeOptionParams{
				ID: o.ID, AttributeID: out.ID, Handle: o.Handle, Value: o.Value, Rank: o.Rank,
			})
			if err != nil {
				return wrapDB(err, "could not create option (%s) of attribute (%s)", o.Handle, a.Handle)
			}
			out.Options = append(out.Options, toAttributeOption(option))
		}

		return nil
	})
	if err != nil {
		return models.Attribute{}, err
	}
	return out, nil
}

// ListAttributes returns the live definitions with their options, in order.
func (r *Repo) ListAttributes(ctx context.Context, limit int) ([]models.Attribute, error) {
	rows, err := r.q.ListAttributes(ctx, toInt32(limit))
	if err != nil {
		return nil, wrapDB(err, "could not list the attributes")
	}
	out := make([]models.Attribute, 0, len(rows))
	ids := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, toAttribute(rows[i]))
		ids = append(ids, rows[i].ID)
	}
	if err := r.attachOptions(ctx, out, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// GetAttribute returns a live definition with its options.
func (r *Repo) GetAttribute(ctx context.Context, id string) (models.Attribute, error) {
	row, err := r.q.GetAttribute(ctx, id)
	if err != nil {
		return models.Attribute{}, wrapDB(err, "attribute not found: %s", id)
	}
	out := []models.Attribute{toAttribute(row)}
	if err := r.attachOptions(ctx, out, []string{id}); err != nil {
		return models.Attribute{}, err
	}
	return out[0], nil
}

// attachOptions reads the options of the given definitions in one query.
func (r *Repo) attachOptions(ctx context.Context, attributes []models.Attribute, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.q.ListAttributeOptions(ctx, ids)
	if err != nil {
		return wrapDB(err, "could not list the attribute options")
	}
	byAttribute := map[string][]models.AttributeOption{}
	for i := range rows {
		byAttribute[rows[i].AttributeID] = append(byAttribute[rows[i].AttributeID], toAttributeOption(rows[i]))
	}
	for i := range attributes {
		attributes[i].Options = byAttribute[attributes[i].ID]
	}
	return nil
}

// UpdateAttribute changes a definition's title and order; nil keeps either.
func (r *Repo) UpdateAttribute(ctx context.Context, id string, title *string, rank *int32) (models.Attribute, error) {
	if _, err := r.q.UpdateAttribute(ctx, productdb.UpdateAttributeParams{Title: title, Rank: rank, ID: id}); err != nil {
		return models.Attribute{}, wrapDB(err, "attribute not found: %s", id)
	}
	return r.GetAttribute(ctx, id)
}

// DeleteAttribute soft deletes a definition; its values name nothing from then
// on.
func (r *Repo) DeleteAttribute(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteAttribute(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete attribute (%s)", id)
	}
	if n == 0 {
		return notFound("attribute", id)
	}
	return nil
}

// AddAttributeOption adds an option to a live select definition under its row
// lock, while it has fewer than limit options.
func (r *Repo) AddAttributeOption(
	ctx context.Context, o models.AttributeOption, limit int,
) (models.AttributeOption, error) {
	var out models.AttributeOption
	err := r.inTx(ctx, func(ctx context.Context, repo *Repo) error {
		var locked string
		if err := repo.db.QueryRow(ctx,
			`SELECT id FROM product_attribute WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
			o.AttributeID).Scan(&locked); err != nil {
			return wrapDB(err, "attribute not found: %s", o.AttributeID)
		}
		count, err := repo.q.CountAttributeOptions(ctx, o.AttributeID)
		if err != nil {
			return wrapDB(err, "could not count the options of attribute (%s)", o.AttributeID)
		}
		if int(count) >= limit {
			return errors.Conflict(codeAttributeLimit, "attribute %s has %d options already, the most there can be",
				o.AttributeID, count)
		}
		row, err := repo.q.InsertAttributeOption(ctx, productdb.InsertAttributeOptionParams{
			ID: o.ID, AttributeID: o.AttributeID, Handle: o.Handle, Value: o.Value, Rank: o.Rank,
		})
		if err != nil {
			return wrapDB(err, "could not create option (%s)", o.Handle)
		}
		out = toAttributeOption(row)

		return nil
	})
	if err != nil {
		return models.AttributeOption{}, err
	}
	return out, nil
}

// DeleteAttributeOption soft deletes an option; the products that chose it no
// longer carry it.
func (r *Repo) DeleteAttributeOption(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteAttributeOption(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete attribute option (%s)", id)
	}
	if n == 0 {
		return notFound("attribute option", id)
	}
	return nil
}

// SetProductAttributeValues replaces a product's attribute values in one
// transaction.
func (r *Repo) SetProductAttributeValues(ctx context.Context, productID string, rows []AttributeValueRow) error {
	return r.inTx(ctx, func(ctx context.Context, repo *Repo) error {
		if err := repo.q.DeleteProductAttributeValues(ctx, productID); err != nil {
			return wrapDB(err, "could not clear the attribute values of product (%s)", productID)
		}
		for _, row := range rows {
			if err := repo.q.InsertProductAttributeValue(ctx, productdb.InsertProductAttributeValueParams{
				ProductID: productID, AttributeID: row.AttributeID,
				OptionID: row.OptionID, NumberValue: row.Number, BooleanValue: row.Boolean,
			}); err != nil {
				return wrapDB(err, "could not write an attribute value of product (%s)", productID)
			}
		}

		return nil
	})
}

// ListProductAttributeValues reads the attribute values of many products in
// one query, keyed by product and in the definitions' order; a select
// attribute's options are gathered into one value.
func (r *Repo) ListProductAttributeValues(
	ctx context.Context, productIDs []string,
) (map[string][]models.ProductAttributeValue, error) {
	out := map[string][]models.ProductAttributeValue{}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListProductAttributeValues(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not list the attribute values")
	}
	for i := range rows {
		row := &rows[i]
		values := out[row.ProductID]
		if n := len(values); n == 0 || values[n-1].AttributeID != row.AttributeID {
			values = append(values, models.ProductAttributeValue{
				AttributeID: row.AttributeID, Handle: row.AttributeHandle, Title: row.AttributeTitle, Kind: row.Kind,
			})
		}
		last := &values[len(values)-1]
		switch {
		case row.OptionID != nil:
			last.Options = append(last.Options, models.AttributeOption{
				ID: *row.OptionID, AttributeID: row.AttributeID,
				Handle: derefString(row.OptionHandle), Value: derefString(row.OptionValue),
			})
		case row.NumberValue != nil:
			last.Number = row.NumberValue
		case row.BooleanValue != nil:
			last.Boolean = row.BooleanValue
		}
		out[row.ProductID] = values
	}
	return out, nil
}

// toAttribute converts a definition row.
func toAttribute(row productdb.ProductAttribute) models.Attribute {
	return models.Attribute{
		ID: row.ID, Handle: row.Handle, Title: row.Title, Kind: row.Kind, Rank: row.Rank,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}

// toAttributeOption converts an option row.
func toAttributeOption(row productdb.ProductAttributeOption) models.AttributeOption {
	return models.AttributeOption{
		ID: row.ID, AttributeID: row.AttributeID, Handle: row.Handle, Value: row.Value, Rank: row.Rank,
	}
}

// derefString reads a nullable text column; NULL is the empty string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
