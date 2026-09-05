package repository

import (
	"encoding/json"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// This file is the ONE place for pgtype <-> domain model conversions.
//
// The boundary being here is deliberate: driver-specific types
// (pgtype.Timestamptz, []byte for jsonb) do not leave the repository. The
// service and the API layer see time.Time and map[string]any; if the driver
// changes one day, the only place that changes is this one.

// toInt32 narrows a pagination value SAFELY to the int32 the query expects.
//
// A negative value is pulled to zero, a value exceeding int32 to the upper
// bound: otherwise the narrowing silently changes sign and would produce a
// query such as "LIMIT -2147483648". The bound check is not left to the
// caller's validation; this is the last line of defense.
func toInt32(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// toTime converts a timestamptz value into a UTC time.Time.
//
// An invalid (NULL) value returns the zero time: on NOT NULL columns this case
// does not arise, so seeing the zero time is a sign of data corruption.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamptz value into a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// toMetadata converts the jsonb column into a map.
//
// An empty or JSON null value returns a nil map; that way the field does not
// show up at all in the API response instead of "metadata": null (omitempty).
func toMetadata(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeMetadataInvalid,
			"could not parse the metadata field")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromMetadata converts the map into the bytes to be written to the jsonb
// column.
//
// A nil map is converted into the empty object ('{}'): the column is NOT NULL
// and the distinction between "no metadata" and "empty metadata" means nothing
// in this module.
func fromMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeMetadataInvalid,
			"could not convert the metadata field to JSON")
	}
	return raw, nil
}

// patchMetadata produces the metadata parameter for an update.
//
// A nil map means "do not change" and NULL goes to the query (the COALESCE
// pattern keeps the old value); a filled map writes the new value.
func patchMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return fromMetadata(m)
}

// toProduct converts a database row into the domain model.
func toProduct(row productdb.Product) (models.Product, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Product{}, err
	}
	return models.Product{
		ID:            row.ID,
		Handle:        row.Handle,
		Title:         row.Title,
		Subtitle:      row.Subtitle,
		Description:   row.Description,
		Thumbnail:     row.Thumbnail,
		Status:        models.Status(row.Status),
		IsGiftcard:    row.IsGiftcard,
		Discountable:  row.Discountable,
		Weight:        row.Weight,
		Length:        row.Length,
		Height:        row.Height,
		Width:         row.Width,
		Material:      row.Material,
		OriginCountry: row.OriginCountry,
		CollectionID:  row.CollectionID,
		Metadata:      meta,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
		DeletedAt:     toTimePtr(row.DeletedAt),
	}, nil
}

// toProducts converts a row slice into a domain model slice.
func toProducts(rows []productdb.Product) ([]models.Product, error) {
	out := make([]models.Product, 0, len(rows))
	// The loop is walked by index: row structs are large and copying them by
	// value carries a few hundred bytes for nothing on every turn.
	for i := range rows {
		p, err := toProduct(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// toVariant converts a database row into the domain model.
func toVariant(row productdb.ProductVariant) (models.Variant, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Variant{}, err
	}
	return models.Variant{
		ID:              row.ID,
		ProductID:       row.ProductID,
		Title:           row.Title,
		SKU:             row.Sku,
		Barcode:         row.Barcode,
		EAN:             row.Ean,
		UPC:             row.Upc,
		ManageInventory: row.ManageInventory,
		AllowBackorder:  row.AllowBackorder,
		Weight:          row.Weight,
		Rank:            row.Rank,
		Metadata:        meta,
		CreatedAt:       toTime(row.CreatedAt),
		UpdatedAt:       toTime(row.UpdatedAt),
		DeletedAt:       toTimePtr(row.DeletedAt),
	}, nil
}

// toVariants converts a row slice into a domain model slice.
func toVariants(rows []productdb.ProductVariant) ([]models.Variant, error) {
	out := make([]models.Variant, 0, len(rows))
	for i := range rows {
		v, err := toVariant(rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// toOption converts an option row into the domain model.
func toOption(row productdb.ProductOption) models.Option {
	return models.Option{
		ID:        row.ID,
		ProductID: row.ProductID,
		Title:     row.Title,
		Rank:      row.Rank,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toOptionValue converts an option value row into the domain model.
func toOptionValue(row productdb.ProductOptionValue) models.OptionValue {
	return models.OptionValue{
		ID:        row.ID,
		OptionID:  row.OptionID,
		Value:     row.Value,
		Rank:      row.Rank,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toCollection converts a collection row into the domain model.
func toCollection(row productdb.ProductCollection) (models.Collection, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Collection{}, err
	}
	return models.Collection{
		ID:        row.ID,
		Title:     row.Title,
		Handle:    row.Handle,
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toCategory converts a category row into the domain model.
func toCategory(row productdb.ProductCategory) models.Category {
	return models.Category{
		ID:          row.ID,
		Name:        row.Name,
		Handle:      row.Handle,
		Description: row.Description,
		ParentID:    row.ParentID,
		IsActive:    row.IsActive,
		IsInternal:  row.IsInternal,
		Rank:        row.Rank,
		CreatedAt:   toTime(row.CreatedAt),
		UpdatedAt:   toTime(row.UpdatedAt),
		DeletedAt:   toTimePtr(row.DeletedAt),
	}
}

// toTag converts a tag row into the domain model.
func toTag(row productdb.ProductTag) models.Tag {
	return models.Tag{
		ID:        row.ID,
		Value:     row.Value,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}
}

// toImage converts an image row into the domain model.
func toImage(row productdb.ProductImage) (models.Image, error) {
	meta, err := toMetadata(row.Metadata)
	if err != nil {
		return models.Image{}, err
	}
	return models.Image{
		ID:        row.ID,
		ProductID: row.ProductID,
		URL:       row.Url,
		UploadID:  row.UploadID,
		Rank:      row.Rank,
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}
