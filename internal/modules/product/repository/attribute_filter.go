package repository

import (
	"context"
	"strconv"

	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// AttributeFilter keeps the products whose value of one attribute matches
// (ADR 0219): one of the options of a select attribute, a number within the
// bounds, or the boolean.
type AttributeFilter struct {
	AttributeID string
	Kind        string
	// OptionIDs are the live options a select value may be any of.
	OptionIDs []string
	// Min and Max bound a number, each inclusive; nil leaves that side open.
	Min, Max *float64
	// Boolean is the value a boolean attribute has to hold.
	Boolean *bool
}

// attributeFilterSQL writes one attribute's clause.
//
// It is an EXISTS like the taxonomy filters and for their reason: a product
// holding two of the named options comes back once. A select filter names its
// options, and an option names one attribute, so the option ids alone are the
// attribute; a number or a boolean names the attribute and the value. An
// attribute with no bound at all still asks that the product HOLDS a number.
func attributeFilterSQL(f *AttributeFilter, param func(any) string) string {
	const open = "\n  AND EXISTS (\n    SELECT 1 FROM product_attribute_value pav\n    WHERE pav.product_id = product.id"
	switch f.Kind {
	case models.AttributeSelect:
		return open + "\n      AND pav.option_id = ANY (" + param(f.OptionIDs) + "::text[])\n  )"
	case models.AttributeBoolean:
		return open + "\n      AND pav.attribute_id = " + param(f.AttributeID) + "::text" +
			"\n      AND pav.boolean_value = " + param(*f.Boolean) + "::boolean\n  )"
	default:
		clause := open + "\n      AND pav.attribute_id = " + param(f.AttributeID) + "::text" +
			"\n      AND pav.number_value IS NOT NULL"
		if f.Min != nil {
			clause += "\n      AND pav.number_value >= " + param(*f.Min) + "::float8"
		}
		if f.Max != nil {
			clause += "\n      AND pav.number_value <= " + param(*f.Max) + "::float8"
		}
		return clause + "\n  )"
	}
}

// FacetRow is one count of a facet: the products holding one option, or one
// boolean value, or a number of one attribute with its smallest and largest.
type FacetRow struct {
	AttributeID string
	OptionID    *string
	Boolean     *bool
	Products    int64
	Min, Max    *float64
}

// AttributeFacets counts, among the products the filter keeps, how many hold
// each option and each boolean value of the given attributes, and the range of
// their numbers. The filter is [productFilterSQL]'s, so the facets count the
// listing's own set.
func (r *Repo) AttributeFacets(ctx context.Context, f ProductFilter, attributeIDs []string) ([]FacetRow, error) {
	body, args := productFilterSQL(f)
	args = append(args, attributeIDs)
	query := `SELECT pav.attribute_id, pav.option_id, pav.boolean_value,
       count(DISTINCT pav.product_id)::bigint, min(pav.number_value), max(pav.number_value)
FROM product_attribute_value pav
LEFT JOIN product_attribute_option pao ON pao.id = pav.option_id
WHERE pav.product_id IN (SELECT id FROM product
` + body + `)
  AND pav.attribute_id = ANY ($` + strconv.Itoa(len(args)) + `::text[])
  AND (pav.option_id IS NULL OR pao.deleted_at IS NULL)
GROUP BY pav.attribute_id, pav.option_id, pav.boolean_value
ORDER BY pav.attribute_id, pav.option_id, pav.boolean_value`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, wrapDB(err, "could not count the attribute facets")
	}
	defer rows.Close()
	var out []FacetRow
	for rows.Next() {
		var row FacetRow
		if err := rows.Scan(&row.AttributeID, &row.OptionID, &row.Boolean, &row.Products, &row.Min, &row.Max); err != nil {
			return nil, wrapDB(err, "could not read an attribute facet")
		}
		out = append(out, row)
	}
	return out, wrapDB(rows.Err(), "could not read the attribute facets")
}
