package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// VariantFilter holds the criteria of the variant listing.
type VariantFilter struct {
	ProductID *string
	Limit     int
	Offset    int
}

// VariantPatch is a partial update of a variant; a nil field does not change.
type VariantPatch struct {
	Title           *string
	SKU             *string
	Barcode         *string
	EAN             *string
	UPC             *string
	ManageInventory *bool
	AllowBackorder  *bool
	Weight          *int32
	Rank            *int32
	Metadata        map[string]any
}

// CreateVariant writes a new variant and returns it as it is stored.
func (r *Repo) CreateVariant(ctx context.Context, v models.Variant) (models.Variant, error) {
	meta, err := fromMetadata(v.Metadata)
	if err != nil {
		return models.Variant{}, err
	}

	row, err := r.q.CreateVariant(ctx, productdb.CreateVariantParams{
		ID:              v.ID,
		ProductID:       v.ProductID,
		Title:           v.Title,
		Sku:             v.SKU,
		Barcode:         v.Barcode,
		Ean:             v.EAN,
		Upc:             v.UPC,
		ManageInventory: v.ManageInventory,
		AllowBackorder:  v.AllowBackorder,
		Weight:          v.Weight,
		Rank:            v.Rank,
		Metadata:        meta,
	})
	if err != nil {
		return models.Variant{}, wrapDB(err, "could not create variant (product: %s)", v.ProductID)
	}
	return toVariant(row)
}

// GetVariant returns the variant by id.
func (r *Repo) GetVariant(ctx context.Context, id string) (models.Variant, error) {
	row, err := r.q.GetVariant(ctx, id)
	if err != nil {
		return models.Variant{}, wrapDB(err, "variant not found: %s", id)
	}
	return toVariant(row)
}

// ListVariants returns the variants matching the criteria, paginated.
func (r *Repo) ListVariants(ctx context.Context, f VariantFilter) ([]models.Variant, error) {
	rows, err := r.q.ListVariants(ctx, productdb.ListVariantsParams{
		ProductID: f.ProductID,
		Lim:       toInt32(f.Limit),
		Off:       toInt32(f.Offset),
	})
	if err != nil {
		return nil, wrapDB(err, "could not list variants")
	}
	return toVariants(rows)
}

// CountVariants returns the total number of variants matching the criteria.
func (r *Repo) CountVariants(ctx context.Context, f VariantFilter) (int, error) {
	n, err := r.q.CountVariants(ctx, f.ProductID)
	if err != nil {
		return 0, wrapDB(err, "could not read variant count")
	}
	return int(n), nil
}

// ListVariantsByProductIDs returns the variants of the given products in a
// SINGLE query.
//
// The product listing fills in its variants over this; a query per product
// would mean N+1.
func (r *Repo) ListVariantsByProductIDs(ctx context.Context, productIDs []string) ([]models.Variant, error) {
	if len(productIDs) == 0 {
		return []models.Variant{}, nil
	}
	rows, err := r.q.ListVariantsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the products' variants (%d products)", len(productIDs))
	}
	return toVariants(rows)
}

// ListVariantsByIDs returns the variants of the given ids in a SINGLE query.
func (r *Repo) ListVariantsByIDs(ctx context.Context, ids []string) ([]models.Variant, error) {
	if len(ids) == 0 {
		return []models.Variant{}, nil
	}
	rows, err := r.q.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read variants by id (%d ids)", len(ids))
	}
	return toVariants(rows)
}

// UpdateVariant updates the variant partially.
func (r *Repo) UpdateVariant(ctx context.Context, id string, patch VariantPatch) (models.Variant, error) {
	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.Variant{}, err
	}

	row, err := r.q.UpdateVariant(ctx, productdb.UpdateVariantParams{
		ID:              id,
		Title:           patch.Title,
		Sku:             patch.SKU,
		Barcode:         patch.Barcode,
		Ean:             patch.EAN,
		Upc:             patch.UPC,
		ManageInventory: patch.ManageInventory,
		AllowBackorder:  patch.AllowBackorder,
		Weight:          patch.Weight,
		Rank:            patch.Rank,
		Metadata:        meta,
	})
	if err != nil {
		return models.Variant{}, wrapDB(err, "could not update variant: %s", id)
	}
	return toVariant(row)
}

// SoftDeleteVariant deletes the variant; if there is no record it returns
// errors.NotFound.
func (r *Repo) SoftDeleteVariant(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteVariant(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete variant: %s", id)
	}
	if n == 0 {
		return notFound("variant", id)
	}
	return nil
}

// CreateOption adds an option to the product.
func (r *Repo) CreateOption(ctx context.Context, o models.Option) (models.Option, error) {
	row, err := r.q.CreateOption(ctx, productdb.CreateOptionParams{
		ID:        o.ID,
		ProductID: o.ProductID,
		Title:     o.Title,
		Rank:      o.Rank,
	})
	if err != nil {
		return models.Option{}, wrapDB(err, "could not create option (product: %s)", o.ProductID)
	}
	return toOption(row), nil
}

// GetOption returns the option by id.
func (r *Repo) GetOption(ctx context.Context, id string) (models.Option, error) {
	row, err := r.q.GetOption(ctx, id)
	if err != nil {
		return models.Option{}, wrapDB(err, "option not found: %s", id)
	}
	return toOption(row), nil
}

// ListOptionsByProductIDs returns the options of the given products in a SINGLE
// query.
func (r *Repo) ListOptionsByProductIDs(ctx context.Context, productIDs []string) ([]models.Option, error) {
	if len(productIDs) == 0 {
		return []models.Option{}, nil
	}
	rows, err := r.q.ListOptionsByProductIDs(ctx, productIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the products' options (%d products)", len(productIDs))
	}

	out := make([]models.Option, 0, len(rows))
	for i := range rows {
		out = append(out, toOption(rows[i]))
	}
	return out, nil
}

// SoftDeleteOption deletes the option; if there is no record it returns
// errors.NotFound.
func (r *Repo) SoftDeleteOption(ctx context.Context, id string) error {
	n, err := r.q.SoftDeleteOption(ctx, id)
	if err != nil {
		return wrapDB(err, "could not delete option: %s", id)
	}
	if n == 0 {
		return notFound("option", id)
	}
	return nil
}

// CreateOptionValue adds a value to the option.
func (r *Repo) CreateOptionValue(ctx context.Context, v models.OptionValue) (models.OptionValue, error) {
	row, err := r.q.CreateOptionValue(ctx, productdb.CreateOptionValueParams{
		ID:       v.ID,
		OptionID: v.OptionID,
		Value:    v.Value,
		Rank:     v.Rank,
	})
	if err != nil {
		return models.OptionValue{}, wrapDB(err, "could not create option value (option: %s)", v.OptionID)
	}
	return toOptionValue(row), nil
}

// ListOptionValuesByOptionIDs returns the values of the given options in a
// SINGLE query.
func (r *Repo) ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []string) ([]models.OptionValue, error) {
	if len(optionIDs) == 0 {
		return []models.OptionValue{}, nil
	}
	rows, err := r.q.ListOptionValuesByOptionIDs(ctx, optionIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read option values (%d options)", len(optionIDs))
	}

	out := make([]models.OptionValue, 0, len(rows))
	for i := range rows {
		out = append(out, toOptionValue(rows[i]))
	}
	return out, nil
}

// ListOptionValuesByIDs returns the given values together with the PRODUCT id
// they belong to.
//
// The product id is there for validation: only its own product's option values
// can be bound to a variant.
func (r *Repo) ListOptionValuesByIDs(ctx context.Context, ids []string) ([]models.OptionValueRef, error) {
	if len(ids) == 0 {
		return []models.OptionValueRef{}, nil
	}
	rows, err := r.q.ListOptionValuesByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not read option values (%d ids)", len(ids))
	}

	out := make([]models.OptionValueRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.OptionValueRef{
			OptionValue: models.OptionValue{
				ID:          row.ID,
				OptionID:    row.OptionID,
				Value:       row.Value,
				Rank:        row.Rank,
				OptionTitle: row.OptionTitle,
			},
			ProductID: row.ProductID,
		})
	}
	return out, nil
}

// SetVariantOptionValue writes the variant's value on one option.
//
// A second call for the same option produces not a new row but an UPDATE; the
// rule stands in the schema as the primary key (variant_id, option_id).
func (r *Repo) SetVariantOptionValue(ctx context.Context, variantID, optionID, valueID string) error {
	err := r.q.SetVariantOptionValue(ctx, productdb.SetVariantOptionValueParams{
		VariantID: variantID,
		OptionID:  optionID,
		ValueID:   valueID,
	})
	if err != nil {
		return wrapDB(err, "could not write the variant's option value (variant: %s)", variantID)
	}
	return nil
}

// DeleteVariantOptionValues removes all of the variant's option values.
//
// These binding rows have no soft delete: the row itself is a relation, not its
// history.
func (r *Repo) DeleteVariantOptionValues(ctx context.Context, variantID string) error {
	if err := r.q.DeleteVariantOptionValues(ctx, variantID); err != nil {
		return wrapDB(err, "could not remove the variant's option values: %s", variantID)
	}
	return nil
}

// ListVariantOptionValues returns the option values of the given variants in a
// SINGLE query; the result is grouped by variant id.
func (r *Repo) ListVariantOptionValues(ctx context.Context, variantIDs []string) (map[string][]models.OptionValue, error) {
	if len(variantIDs) == 0 {
		return map[string][]models.OptionValue{}, nil
	}
	rows, err := r.q.ListVariantOptionValuesByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the variants' option values (%d variants)", len(variantIDs))
	}

	out := make(map[string][]models.OptionValue, len(variantIDs))
	for _, row := range rows {
		out[row.VariantID] = append(out[row.VariantID], models.OptionValue{
			ID:          row.ID,
			OptionID:    row.OptionID,
			Value:       row.Value,
			Rank:        row.Rank,
			OptionTitle: row.OptionTitle,
		})
	}
	return out, nil
}
