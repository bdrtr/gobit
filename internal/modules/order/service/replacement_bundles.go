package service

import (
	"context"
	"math"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The query layer's names a bundle's composition is read under (ADR 0244).
// They are repeated as literals, as the checkout repeats them: the product
// module's constants are not this module's to import. internal/arch's
// TestTheBundleNamesAgree holds them to the product module's.
const (
	CatalogEntityVariant                 = "variant"
	CatalogFieldBundleComponents         = "bundle_components"
	CatalogFieldBundleComponentVariantID = "variant_id"
	CatalogFieldBundleComponentQuantity  = "quantity"
	CatalogFilterIDs                     = "ids"
)

// variantBundleParts reads what each named variant is made of from the catalog,
// for a replacement item that names a variant rather than a line (ADR 0244).
// A variant that is no bundle is absent from the answer.
//
// # The composition is the catalog's at the moment the replacement is recorded
//
// A line keeps what it sold (ADR 0235) and its replacement sends that
// (ADR 0238). An item that names a variant sends goods the order never sold, so
// there is no sale to keep; what it promises is the bundle as the shop makes it
// when the promise is recorded, and the record keeps that, whatever the bundle
// is made of by the time the parcel leaves.
//
// # Without a catalog nothing is read
//
// The catalog is optional on this service. Without it a bundle is not
// recognized, its item carries no parts, and the dispatch refuses it for having
// no inventory item, as it did before this record.
func (s *Service) variantBundleParts(
	ctx context.Context, variantIDs []string,
) (map[string][]models.ReplacementItemPart, error) {
	out := map[string][]models.ReplacementItemPart{}
	if s.catalog == nil || len(variantIDs) == 0 {
		return out, nil
	}

	records, err := s.catalog.Graph(ctx, query.GraphSpec{
		Entity:  CatalogEntityVariant,
		Fields:  []string{query.IDField, CatalogFieldBundleComponents},
		Filters: map[string]any{CatalogFilterIDs: variantIDs},
		Limit:   len(variantIDs),
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"what the replacement's variants are made of could not be read")
	}

	for _, record := range records {
		id, _ := record[query.IDField].(string)
		entries, ok := componentEntries(record[CatalogFieldBundleComponents])
		if id == "" || !ok {
			return nil, errors.Internal(CodeCatalogReadFailed,
				"the catalog answered a variant %q whose composition cannot be read", id)
		}
		parts := make([]models.ReplacementItemPart, 0, len(entries))
		for _, entry := range entries {
			variant, _ := entry[CatalogFieldBundleComponentVariantID].(string)
			units, whole := wholeUnits(entry[CatalogFieldBundleComponentQuantity])
			if variant == "" || !whole || units < 1 || units > MaxLineComponentQuantity {
				return nil, errors.Internal(CodeCatalogReadFailed,
					"the catalog answered a part of variant %s that cannot be sent: %v", id, entry)
			}
			parts = append(parts, models.ReplacementItemPart{VariantID: variant, Quantity: units})
		}
		if len(parts) > 0 {
			out[id] = parts
		}
	}

	return out, nil
}

// componentEntries reads a composition in the shapes the query layer hands
// over: records in process, maps once they crossed JSON. A missing field is an
// empty composition.
func componentEntries(value any) ([]map[string]any, bool) {
	switch list := value.(type) {
	case nil:
		return nil, true
	case []query.Record:
		out := make([]map[string]any, 0, len(list))
		for _, entry := range list {
			out = append(out, entry)
		}
		return out, true
	case []map[string]any:
		return list, true
	case []any:
		out := make([]map[string]any, 0, len(list))
		for _, raw := range list {
			switch entry := raw.(type) {
			case map[string]any:
				out = append(out, entry)
			case query.Record:
				out = append(out, entry)
			default:
				return nil, false
			}
		}
		return out, true
	default:
		return nil, false
	}
}

// wholeUnits reads a count that is an integer in process and a float once it
// crossed JSON; a fraction is not a count.
func wholeUnits(value any) (int64, bool) {
	switch n := value.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		if n != math.Trunc(n) || n > math.MaxInt32 || n < math.MinInt32 {
			return 0, false
		}
		return int64(n), true
	default:
		return 0, false
	}
}
