package service

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// exportPage is how many products one read of the export covers.
const exportPage = 100

// entityRegion is the region module's entity: a region's currency is one the
// shop sells in, and those are the export's price columns (ADR 0204).
const entityRegion = "region"

// ExportOptions narrows what an export covers.
type ExportOptions struct {
	// Status exports only the products in that status; nil exports all.
	Status *models.Status
}

// The columns an import matches rows by (ADR 0204, 0205).
const (
	columnProductID     = "product_id"
	columnProductHandle = "product_handle"
	columnVariantID     = "variant_id"
)

// The columns the export writes and an import reads in more than one place.
const (
	columnProductWeight          = "product_weight"
	columnProductLength          = "product_length"
	columnProductHeight          = "product_height"
	columnProductWidth           = "product_width"
	columnVariantManageInventory = "variant_manage_inventory"
	columnVariantAllowBackorder  = "variant_allow_backorder"
	columnVariantWeight          = "variant_weight"
)

// exportColumns are the columns every export starts with, in order. The
// price columns follow, one per currency a region sells in, and then the cost
// columns, one per the same currency (ADR 0424).
var exportColumns = []string{
	columnProductID, columnProductHandle, "product_title", "product_subtitle", "product_description",
	"product_status", "product_thumbnail", "product_is_giftcard", "product_discountable",
	columnProductWeight, columnProductLength, columnProductHeight, columnProductWidth,
	"product_material", "product_origin_country", "product_collection_id", "product_type_id",
	"product_tag_ids", "product_category_ids", "product_metadata",
	columnVariantID, "variant_title", "variant_sku", "variant_barcode", "variant_ean", "variant_upc",
	columnVariantManageInventory, columnVariantAllowBackorder, columnVariantWeight, "variant_options",
	"variant_metadata",
}

// ExportProducts writes the catalog as CSV to out, one row per variant and one
// for a product with none (ADR 0204).
//
// A variant's price in each currency a region sells in is its base price at
// one unit, bound to no list and read by ADR 0041's definition, the one the
// catalog's price filter compares. Its unit cost in each of those currencies
// follows (ADR 0401, ADR 0424): an order is placed in a region's currency, so a
// cost in another reaches no order and has no column. Amounts are in minor
// units, as everywhere else.
//
// The pages are read by cursor, newest first, so a product created while the
// export runs is not in it and none is written twice. afterPage runs once each
// page has been written, so the caller can flush it and move its deadline.
//
// Nothing is written until the currencies and the first page have been read:
// a failure there is an error the caller can still answer with a status. A
// failure after that leaves out holding a partial file, and the caller has to
// make that visible rather than let it pass for a whole one.
func (s *Service) ExportProducts(
	ctx context.Context, out io.Writer, opts ExportOptions, afterPage func() error,
) error {
	currencies, err := s.exportCurrencies(ctx)
	if err != nil {
		return err
	}
	list := ListProductsOptions{Status: opts.Status, Limit: exportPage, WithRelations: true, SkipCount: true}
	page, err := s.ListProducts(ctx, list)
	if err != nil {
		return err
	}

	writer := csv.NewWriter(out)
	header := slices.Clone(exportColumns)
	for _, currency := range currencies {
		header = append(header, "variant_price_"+strings.ToLower(currency))
	}
	for _, currency := range currencies {
		header = append(header, costColumnPrefix+strings.ToLower(currency))
	}
	if err := writer.Write(header); err != nil {
		return errors.Wrap(err, errors.KindInternal, codeQueryFailed, "the export's header could not be written")
	}

	for {
		prices, err := s.basePrices(ctx, page.Items)
		if err != nil {
			return err
		}
		costs, err := s.exportCosts(ctx, page.Items, currencies)
		if err != nil {
			return err
		}
		for i := range page.Items {
			for _, row := range exportRows(&page.Items[i], prices, costs, currencies) {
				if err := writer.Write(row); err != nil {
					return errors.Wrap(err, errors.KindInternal, codeQueryFailed,
						"the export's row for product %s could not be written", page.Items[i].ID)
				}
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return errors.Wrap(err, errors.KindInternal, codeQueryFailed, "the export could not be written")
		}
		if afterPage != nil {
			if err := afterPage(); err != nil {
				return err
			}
		}

		if page.NextCursor == "" {
			return nil
		}
		list.After, err = corepage.Decode(ProductListingFor(list.Order), page.NextCursor)
		if err != nil {
			return err
		}
		if page, err = s.ListProducts(ctx, list); err != nil {
			return err
		}
	}
}

// exportCurrencies are the currencies the regions sell in, upper case and
// sorted; none when the region module is not registered.
func (s *Service) exportCurrencies(ctx context.Context) ([]string, error) {
	if s.graph == nil {
		return nil, nil
	}

	var currencies []string
	for offset := 0; ; offset += exportPage {
		records, err := s.graph.Graph(ctx, query.GraphSpec{
			Entity: entityRegion, Fields: []string{foreignRegionCurrencyCode}, Limit: exportPage, Offset: offset,
		})
		if err != nil {
			if errors.CodeOf(err) == codeProviderNotFound {
				s.log.WarnContext(ctx, "the region provider is not registered; the export has no price columns")
				return nil, nil
			}
			return nil, errors.Wrap(err, errors.KindOf(err), codeQueryFailed,
				"the regions' currencies could not be read for the export")
		}
		for _, record := range records {
			code, _ := recordString(record, foreignRegionCurrencyCode)
			code = strings.ToUpper(strings.TrimSpace(code))
			if code != "" && !slices.Contains(currencies, code) {
				currencies = append(currencies, code)
			}
		}
		if len(records) < exportPage {
			break
		}
	}
	slices.Sort(currencies)

	return currencies, nil
}

// basePrices reads the page's variants' base prices, by variant and currency,
// in one read of the price sets.
func (s *Service) basePrices(ctx context.Context, products []models.Product) (map[string]map[string]int64, error) {
	out := map[string]map[string]int64{}
	var variantIDs []string
	for i := range products {
		for j := range products[i].Variants {
			variantIDs = append(variantIDs, products[i].Variants[j].ID)
		}
	}
	if len(variantIDs) == 0 || s.graph == nil {
		return out, nil
	}

	records, err := s.graph.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{filterID},
		Filters: map[string]any{filterIDs: variantIDs},
		Limit:   len(variantIDs),
		Expand: []query.Expansion{{
			Link: LinkVariantPriceSet, As: keyPriceSet, Fields: []string{foreignPrices},
		}},
	})
	if err != nil {
		if errors.CodeOf(err) == codeProviderNotFound {
			s.log.WarnContext(ctx, "the price provider is not registered; the export's prices are empty")
			return out, nil
		}
		return nil, errors.Wrap(err, errors.KindOf(err), codeQueryFailed,
			"the prices of the export's variants could not be read (%d variants)", len(variantIDs))
	}

	for _, record := range records {
		id, _ := record[filterID].(string)
		set := asRecord(record[keyPriceSet])
		if id == "" || set == nil {
			continue
		}
		for _, price := range recordSlice(set, foreignPrices) {
			// ADR 0041's base price: bound to no list, and covering one unit.
			if !isBasePrice(price) || !coversQuantityOne(price) {
				continue
			}
			currency, ok := recordString(price, foreignCurrencyCode)
			amount, isAmount := recordInt(price, foreignAmount)
			if !ok || !isAmount {
				continue
			}
			currency = strings.ToUpper(currency)
			if out[id] == nil {
				out[id] = map[string]int64{}
			}
			if _, seen := out[id][currency]; !seen {
				out[id][currency] = amount
			}
		}
	}

	return out, nil
}

// exportRows are a product's rows: one per variant, or one with the variant
// columns empty.
func exportRows(p *models.Product, prices, costs map[string]map[string]int64, currencies []string) [][]string {
	product := []string{
		p.ID, cell(p.Handle), cell(p.Title), cell(deref(p.Subtitle)), cell(deref(p.Description)),
		p.Status.String(), cell(deref(p.Thumbnail)), strconv.FormatBool(p.IsGiftcard),
		strconv.FormatBool(p.Discountable),
		number(p.Weight), number(p.Length), number(p.Height), number(p.Width),
		cell(deref(p.Material)), cell(deref(p.OriginCountry)), deref(p.CollectionID), deref(p.TypeID),
		joinIDs(p.Tags, func(t models.Tag) string { return t.ID }),
		joinIDs(p.Categories, func(c models.Category) string { return c.ID }),
		cell(jsonCell(p.Metadata)),
	}
	emptyVariant := make([]string, len(exportColumns)-len(product)+2*len(currencies))

	if len(p.Variants) == 0 {
		return [][]string{append(slices.Clone(product), emptyVariant...)}
	}

	rows := make([][]string, 0, len(p.Variants))
	for i := range p.Variants {
		v := &p.Variants[i]
		options := map[string]string{}
		for j := range v.OptionValues {
			options[v.OptionValues[j].OptionTitle] = v.OptionValues[j].Value
		}
		row := append(slices.Clone(product),
			v.ID, cell(v.Title), cell(deref(v.SKU)), cell(deref(v.Barcode)), cell(deref(v.EAN)),
			cell(deref(v.UPC)), strconv.FormatBool(v.ManageInventory), strconv.FormatBool(v.AllowBackorder),
			number(v.Weight), cell(jsonCell(options)), cell(jsonCell(v.Metadata)))
		row = appendAmounts(row, prices[v.ID], currencies)
		row = appendAmounts(row, costs[v.ID], currencies)
		rows = append(rows, row)
	}

	return rows
}

// appendAmounts appends one cell per currency: the amount in minor units, or
// empty when there is none.
func appendAmounts(row []string, amounts map[string]int64, currencies []string) []string {
	for _, currency := range currencies {
		if amount, ok := amounts[currency]; ok {
			row = append(row, strconv.FormatInt(amount, 10))
		} else {
			row = append(row, "")
		}
	}

	return row
}

// exportCosts reads the page's variants' unit costs, by variant and currency,
// in one read (ADR 0424). There is nothing to read when no region sells in a
// currency, since the export then has no cost column.
func (s *Service) exportCosts(
	ctx context.Context, products []models.Product, currencies []string,
) (map[string]map[string]int64, error) {
	if len(currencies) == 0 {
		return nil, nil
	}
	var variantIDs []string
	for i := range products {
		for j := range products[i].Variants {
			variantIDs = append(variantIDs, products[i].Variants[j].ID)
		}
	}
	if len(variantIDs) == 0 {
		return nil, nil
	}
	byVariant, err := s.repo.ListVariantCosts(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]int64, len(byVariant))
	for id, costs := range byVariant {
		amounts := make(map[string]int64, len(costs))
		for _, c := range costs {
			amounts[c.CurrencyCode] = c.Amount
		}
		out[id] = amounts
	}

	return out, nil
}

// cell guards a text cell against being read as a formula.
//
// A spreadsheet runs a cell that starts with =, +, - or @ (and a tab or a
// carriage return ahead of one), so a product title an operator typed becomes
// a formula on the next person's machine. The cell is prefixed with an
// apostrophe, which a spreadsheet shows as text; an import takes one leading
// apostrophe off a cell that starts with one of those characters after it.
func cell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

// jsonCell writes a map as JSON, and nothing for an empty one.
func jsonCell[T any](value map[string]T) string {
	if len(value) == 0 {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}

	return string(raw)
}

// joinIDs joins the items' ids with a bar; an id never holds one.
func joinIDs[T any](items []T, id func(T) string) string {
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, id(items[i]))
	}

	return strings.Join(ids, "|")
}

// number writes an optional whole number, and nothing when there is none.
func number(value *int32) string {
	if value == nil {
		return ""
	}

	return strconv.FormatInt(int64(*value), 10)
}
