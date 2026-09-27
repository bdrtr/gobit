package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// MaxImportBytes is the largest file an import takes. The export of a 52,000
// product catalog is under 7 MB (measurements/0204).
const MaxImportBytes = 32 << 20

// prefixImport is an import's id prefix.
const prefixImport = "pimp_"

// codeImportInvalid refuses a file an import cannot read.
const codeImportInvalid = "product_import_invalid"

// priceColumn is a price column of the export: a currency's base price at one
// unit, in minor units (ADR 0204), which an import writes through pricing
// (ADR 0207).
var priceColumn = regexp.MustCompile(`^variant_price_[a-z]{3}$`)

// utf8BOM is the byte order mark a spreadsheet may put before the header.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// CreateImport takes a CSV file in the export's columns and keeps it for a job
// to work through (ADR 0205).
//
// The file is read in full here and refused as a whole when it cannot be
// applied at all: not UTF-8, not CSV, a row of another width, a column the
// export does not write, a column twice, or neither product_id nor
// product_handle. A file with price columns is refused as well when this
// installation has no pricing module to write them (ADR 0207). A row that can
// be read but not applied is the job's to refuse, one row at a time.
func (s *Service) CreateImport(ctx context.Context, file []byte) (models.Import, error) {
	header, rows, err := readImportFile(file)
	if err != nil {
		return models.Import{}, err
	}
	if err := s.checkImportPrices(ctx, header); err != nil {
		return models.Import{}, err
	}

	return s.repo.CreateImport(ctx, newID(prefixImport), file, rows)
}

// GetImport reads an import without its file.
func (s *Service) GetImport(ctx context.Context, id string) (models.Import, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Import{}, err
	}

	return s.repo.GetImport(ctx, id)
}

// ApplyImports works through the oldest import with rows left until it ends
// or until the moment given, and returns how many rows it applied.
//
// Each row is recorded as it is applied, so a run that stops resumes where
// it stopped. A row interrupted by the context is not recorded: it runs
// again, and finds what it made, because rows are matched by the product's id
// or handle and the variant's id, SKU or options.
func (s *Service) ApplyImports(ctx context.Context, until time.Time) (int, error) {
	claimed, ok, err := s.repo.ClaimImport(ctx)
	if err != nil || !ok {
		return 0, err
	}

	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(claimed.File, utf8BOM)))
	header, err := reader.Read()
	if err != nil {
		return 0, errors.Wrap(err, errors.KindInternal, codeImportInvalid,
			"import %s's header could not be read again", claimed.ID)
	}

	applied := 0
	for index := 0; index < claimed.RowsTotal; index++ {
		record, readErr := reader.Read()
		if index < claimed.RowsDone {
			continue
		}
		if ctx.Err() != nil || time.Now().After(until) {
			return applied, nil //nolint:nilerr // a run stopped or out of time is not a failed one: the next run carries on
		}

		var outcome models.ImportRowOutcome
		if readErr != nil {
			outcome = refused(index, readErr)
		} else {
			outcome = s.applyImportRow(ctx, importRow{header: header, record: record, index: index})
			if ctx.Err() != nil {
				// The row may have been cut off half way; it runs again.
				return applied, nil //nolint:nilerr // as above: the row is not recorded, so the next run applies it
			}
		}
		if err := s.repo.RecordImportRow(ctx, claimed.ID, index, outcome); err != nil {
			return applied, err
		}
		applied++
	}

	return applied, s.repo.FinishImport(ctx, claimed.ID)
}

// importHeader reads a file's header.
func importHeader(file []byte) ([]string, error) {
	header, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(file, utf8BOM))).Read()
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeImportInvalid, "the header could not be read")
	}

	return header, nil
}

// readImportFile checks the file an import is given, and returns its header
// and how many rows it has.
func readImportFile(file []byte) (header []string, rows int, err error) {
	file = bytes.TrimPrefix(file, utf8BOM)
	switch {
	case len(file) == 0:
		return nil, 0, errors.Invalid(codeImportInvalid, "the file is empty")
	case len(file) > MaxImportBytes:
		return nil, 0, errors.Invalid(codeImportInvalid, "the file is larger than %d bytes", MaxImportBytes)
	case !utf8.Valid(file):
		return nil, 0, errors.Invalid(codeImportInvalid, "the file is not UTF-8")
	}

	reader := csv.NewReader(bytes.NewReader(file))
	if header, err = reader.Read(); err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInvalid, codeImportInvalid, "the header could not be read")
	}
	if err := checkImportHeader(header); err != nil {
		return nil, 0, err
	}

	for {
		if _, err := reader.Read(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, 0, errors.Wrap(err, errors.KindInvalid, codeImportInvalid,
				"the file is not CSV the import can read")
		}
		rows++
	}
	if rows == 0 {
		return nil, 0, errors.Invalid(codeImportInvalid, "the file has a header and no rows")
	}

	return header, rows, nil
}

// checkImportHeader refuses a header the import would misread.
func checkImportHeader(header []string) error {
	seen := map[string]bool{}
	for _, column := range header {
		switch {
		case seen[column]:
			return errors.Invalid(codeImportInvalid, "the column %q is in the header twice", column)
		case !slices.Contains(exportColumns, column) && !priceColumn.MatchString(column):
			return errors.Invalid(codeImportInvalid,
				"the column %q is not one the export writes; a misspelled column would be left out "+
					"of every row", column)
		}
		seen[column] = true
	}
	if !seen[columnProductID] && !seen[columnProductHandle] {
		return errors.Invalid(codeImportInvalid,
			"the header names neither %s nor %s, so no row could find its product",
			columnProductID, columnProductHandle)
	}

	return nil
}

// importRow is one row of an import, read by column.
type importRow struct {
	header, record []string
	index          int
}

// text is a cell as the import reads it: a formula-like cell's apostrophe,
// which the export added (ADR 0204), taken off again.
func (r importRow) text(column string) string {
	for i, name := range r.header {
		if name != column {
			continue
		}
		value := r.record[i]
		if len(value) > 1 && value[0] == '\'' {
			switch value[1] {
			case '=', '+', '-', '@', '\t', '\r':
				return value[1:]
			}
		}
		return value
	}

	return ""
}

// optText is a cell as a value to set, nil when the cell is empty.
func (r importRow) optText(column string) *string {
	if value := r.text(column); value != "" {
		return &value
	}

	return nil
}

// optInt32 is a whole-number cell, nil when empty.
func (r importRow) optInt32(column string) (*int32, error) {
	value := r.text(column)
	if value == "" {
		return nil, nil
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("%s is %q, not a whole number", column, value)
	}
	n32 := int32(n)

	return &n32, nil
}

// optBool is a true/false cell, nil when empty.
func (r importRow) optBool(column string) (*bool, error) {
	value := r.text(column)
	if value == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("%s is %q, not true or false", column, value)
	}

	return &b, nil
}

// jsonObject is a JSON object cell, nil when empty.
func jsonObject[T any](r importRow, column string) (map[string]T, error) {
	value := r.text(column)
	if value == "" {
		return nil, nil
	}
	var out map[string]T
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object of the export's shape: %w", column, err)
	}

	return out, nil
}

// ids is a bar-separated id cell, nil when empty.
func (r importRow) ids(column string) []string {
	value := r.text(column)
	if value == "" {
		return nil
	}

	return strings.Split(value, "|")
}

// variantColumns are the columns that make a row a variant's: the export's
// from variant_id on.
var variantColumns = exportColumns[slices.Index(exportColumns, columnVariantID):]

// hasVariant reports whether the row carries anything of a variant.
func (r importRow) hasVariant() bool {
	for _, column := range variantColumns {
		if r.text(column) != "" {
			return true
		}
	}

	return false
}

// refused is the outcome of a row that could not be applied.
func refused(index int, err error) models.ImportRowOutcome {
	return models.ImportRowOutcome{Error: &models.ImportError{Row: index + 2, Message: err.Error()}}
}

// applyImportRow applies one row, and says what it did.
func (s *Service) applyImportRow(ctx context.Context, row importRow) models.ImportRowOutcome {
	outcome, err := s.importRow(ctx, row)
	if err != nil {
		return refused(row.index, err)
	}

	return outcome
}

// importRow finds or creates the row's product, then its variant, then writes
// the variant's prices.
//
// The price cells are read before anything is written, so a row whose price
// cannot be read, or which prices no variant, changes nothing.
func (s *Service) importRow(ctx context.Context, row importRow) (models.ImportRowOutcome, error) {
	amounts, err := row.prices()
	if err != nil {
		return models.ImportRowOutcome{}, err
	}
	if len(amounts) > 0 && !row.hasVariant() {
		return models.ImportRowOutcome{}, errors.Invalid(codeImportInvalid,
			"the row has a price and no variant; a price belongs to a variant")
	}

	product, found, err := s.importedProduct(ctx, row)
	if err != nil {
		return models.ImportRowOutcome{}, err
	}
	if !found {
		created, err := s.createImportedProduct(ctx, row)
		if err != nil {
			return models.ImportRowOutcome{}, err
		}
		if len(created.Variants) > 0 {
			if _, err := s.importPrices(ctx, created.Variants[0].ID, amounts); err != nil {
				return models.ImportRowOutcome{}, err
			}
		}

		return models.ImportRowOutcome{Created: true}, nil
	}

	changedProduct, err := s.updateImportedProduct(ctx, row, product)
	if err != nil {
		return models.ImportRowOutcome{}, err
	}
	if !row.hasVariant() {
		return models.ImportRowOutcome{Updated: changedProduct}, nil
	}

	variantID, created, changedVariant, err := s.importVariant(ctx, row, product)
	if err != nil {
		return models.ImportRowOutcome{}, err
	}
	changedPrices, err := s.importPrices(ctx, variantID, amounts)
	if err != nil {
		return models.ImportRowOutcome{}, err
	}
	if created {
		return models.ImportRowOutcome{Created: true}, nil
	}

	return models.ImportRowOutcome{Updated: changedProduct || changedVariant || changedPrices}, nil
}

// importedProduct finds the row's product: by id, or else by handle.
func (s *Service) importedProduct(ctx context.Context, row importRow) (models.Product, bool, error) {
	if id := row.text(columnProductID); id != "" {
		product, err := s.GetProduct(ctx, id)
		if err != nil {
			return models.Product{}, false, err
		}

		return product, true, nil
	}
	handle := row.text(columnProductHandle)
	if handle == "" {
		return models.Product{}, false, fmt.Errorf("the row names neither %s nor %s", columnProductID, columnProductHandle)
	}
	product, err := s.GetProductByHandle(ctx, handle)
	switch {
	case err == nil:
		return product, true, nil
	case errors.IsNotFound(err):
		return models.Product{}, false, nil
	default:
		return models.Product{}, false, err
	}
}

// createImportedProduct creates the row's product, with its variant and the
// options the variant names, in one call.
func (s *Service) createImportedProduct(ctx context.Context, row importRow) (models.Product, error) {
	in := CreateProductInput{
		Handle: row.text(columnProductHandle), Title: row.text("product_title"),
		Subtitle: row.optText("product_subtitle"), Description: row.optText("product_description"),
		Thumbnail: row.optText("product_thumbnail"), Status: models.Status(row.text("product_status")),
		Material: row.optText("product_material"), OriginCountry: row.optText("product_origin_country"),
		CollectionID: row.optText("product_collection_id"), TypeID: row.optText("product_type_id"),
		TagIDs: row.ids("product_tag_ids"), CategoryIDs: row.ids("product_category_ids"),
	}
	var err error
	if in.Metadata, err = jsonObject[any](row, "product_metadata"); err != nil {
		return models.Product{}, err
	}
	if giftcard, err := row.optBool("product_is_giftcard"); err != nil {
		return models.Product{}, err
	} else if giftcard != nil {
		in.IsGiftcard = *giftcard
	}
	if in.Discountable, err = row.optBool("product_discountable"); err != nil {
		return models.Product{}, err
	}
	for column, target := range map[string]**int32{
		columnProductWeight: &in.Weight, columnProductLength: &in.Length,
		columnProductHeight: &in.Height, columnProductWidth: &in.Width,
	} {
		if *target, err = row.optInt32(column); err != nil {
			return models.Product{}, err
		}
	}

	if row.hasVariant() {
		variant, options, err := newImportedVariant(row)
		if err != nil {
			return models.Product{}, err
		}
		for _, title := range slices.Sorted(maps.Keys(options)) {
			in.Options = append(in.Options, CreateOptionInput{Title: title, Values: []string{options[title]}})
		}
		in.Variants = []CreateVariantInput{variant}
	}

	return s.CreateProduct(ctx, in)
}

// newImportedVariant is the row's variant as a new one.
func newImportedVariant(row importRow) (CreateVariantInput, map[string]string, error) {
	options, err := jsonObject[string](row, "variant_options")
	if err != nil {
		return CreateVariantInput{}, nil, err
	}
	in := CreateVariantInput{
		Title: row.text("variant_title"), SKU: row.optText("variant_sku"),
		Barcode: row.optText("variant_barcode"), EAN: row.optText("variant_ean"), UPC: row.optText("variant_upc"),
		Options: options,
	}
	if in.ManageInventory, err = row.optBool(columnVariantManageInventory); err != nil {
		return CreateVariantInput{}, nil, err
	}
	if in.AllowBackorder, err = row.optBool(columnVariantAllowBackorder); err != nil {
		return CreateVariantInput{}, nil, err
	}
	if in.Weight, err = row.optInt32(columnVariantWeight); err != nil {
		return CreateVariantInput{}, nil, err
	}
	if in.Metadata, err = jsonObject[any](row, "variant_metadata"); err != nil {
		return CreateVariantInput{}, nil, err
	}

	return in, options, nil
}

// updateImportedProduct writes the row's non-empty product cells that differ
// from the product, and reports whether it wrote any.
func (s *Service) updateImportedProduct(ctx context.Context, row importRow, p models.Product) (bool, error) {
	var in UpdateProductInput
	changed := false
	setText := func(column string, current *string, target **string) {
		if value := row.optText(column); value != nil && (current == nil || *current != *value) {
			*target, changed = value, true
		}
	}
	title := p.Title
	setText("product_title", &title, &in.Title)
	handle := p.Handle
	setText(columnProductHandle, &handle, &in.Handle)
	setText("product_subtitle", p.Subtitle, &in.Subtitle)
	setText("product_description", p.Description, &in.Description)
	setText("product_thumbnail", p.Thumbnail, &in.Thumbnail)
	setText("product_material", p.Material, &in.Material)
	setText("product_origin_country", p.OriginCountry, &in.OriginCountry)
	setText("product_collection_id", p.CollectionID, &in.CollectionID)
	setText("product_type_id", p.TypeID, &in.TypeID)

	if status := row.text("product_status"); status != "" && status != p.Status.String() {
		value := models.Status(status)
		in.Status, changed = &value, true
	}
	discountable, err := row.optBool("product_discountable")
	if err != nil {
		return false, err
	}
	if discountable != nil && *discountable != p.Discountable {
		in.Discountable, changed = discountable, true
	}
	for column, pair := range map[string][2]any{
		columnProductWeight: {p.Weight, &in.Weight}, columnProductLength: {p.Length, &in.Length},
		columnProductHeight: {p.Height, &in.Height}, columnProductWidth: {p.Width, &in.Width},
	} {
		value, err := row.optInt32(column)
		if err != nil {
			return false, err
		}
		current, _ := pair[0].(*int32)
		if value != nil && (current == nil || *current != *value) {
			target, _ := pair[1].(**int32)
			*target, changed = value, true
		}
	}
	metadata, err := jsonObject[any](row, "product_metadata")
	if err != nil {
		return false, err
	}
	if metadata != nil && !sameJSON(metadata, p.Metadata) {
		in.Metadata, changed = metadata, true
	}
	if tags := row.ids("product_tag_ids"); tags != nil && !sameIDs(tags, tagIDs(p.Tags)) {
		in.TagIDs, changed = tags, true
	}
	if categories := row.ids("product_category_ids"); categories != nil &&
		!sameIDs(categories, categoryIDs(p.Categories)) {
		in.CategoryIDs, changed = categories, true
	}

	if !changed {
		return false, nil
	}
	_, err = s.UpdateProduct(ctx, p.ID, in)

	return err == nil, err
}

// importVariant finds the row's variant — by id, SKU or options — and updates
// it, or creates it, adding the options it names that the product lacks.
func (s *Service) importVariant(
	ctx context.Context, row importRow, p models.Product,
) (variantID string, created, changed bool, err error) {
	options, err := jsonObject[string](row, "variant_options")
	if err != nil {
		return "", false, false, err
	}
	addedOptions, err := s.ensureImportedOptions(ctx, p, options)
	if err != nil {
		return "", false, false, err
	}

	variant, found, err := importedVariant(row, p, options)
	if err != nil {
		return "", false, false, err
	}
	if !found {
		in, _, err := newImportedVariant(row)
		if err != nil {
			return "", false, false, err
		}
		createdVariant, err := s.CreateVariant(ctx, p.ID, in)
		if err != nil {
			return "", false, false, err
		}

		return createdVariant.ID, true, false, nil
	}

	in, variantChanged, err := variantUpdate(row, variant)
	if err != nil {
		return "", false, false, err
	}
	if options != nil && !sameOptions(variant.OptionValues, options) {
		ids, err := s.optionValueIDs(ctx, p.ID, options)
		if err != nil {
			return "", false, false, err
		}
		in.OptionValueIDs, variantChanged = ids, true
	}
	if !variantChanged {
		return variant.ID, false, addedOptions, nil
	}
	if _, err := s.UpdateVariant(ctx, variant.ID, in); err != nil {
		return "", false, false, err
	}

	return variant.ID, false, true, nil
}

// importedVariant finds the row's variant among the product's.
func importedVariant(row importRow, p models.Product, options map[string]string) (models.Variant, bool, error) {
	if id := row.text(columnVariantID); id != "" {
		for i := range p.Variants {
			if p.Variants[i].ID == id {
				return p.Variants[i], true, nil
			}
		}

		return models.Variant{}, false, fmt.Errorf("variant %s is not a variant of product %s", id, p.ID)
	}
	if sku := row.text("variant_sku"); sku != "" {
		for i := range p.Variants {
			if p.Variants[i].SKU != nil && *p.Variants[i].SKU == sku {
				return p.Variants[i], true, nil
			}
		}

		return models.Variant{}, false, nil
	}
	if options != nil {
		for i := range p.Variants {
			if sameOptions(p.Variants[i].OptionValues, options) {
				return p.Variants[i], true, nil
			}
		}

		return models.Variant{}, false, nil
	}

	return models.Variant{}, false, fmt.Errorf(
		"a new variant needs a SKU or options, so that the row finds it again if it runs twice")
}

// variantUpdate is the row's non-empty variant cells that differ from the
// variant.
func variantUpdate(row importRow, v models.Variant) (UpdateVariantInput, bool, error) {
	var in UpdateVariantInput
	changed := false
	setText := func(column string, current *string, target **string) {
		if value := row.optText(column); value != nil && (current == nil || *current != *value) {
			*target, changed = value, true
		}
	}
	title := v.Title
	setText("variant_title", &title, &in.Title)
	setText("variant_sku", v.SKU, &in.SKU)
	setText("variant_barcode", v.Barcode, &in.Barcode)
	setText("variant_ean", v.EAN, &in.EAN)
	setText("variant_upc", v.UPC, &in.UPC)

	for column, pair := range map[string]struct {
		current bool
		target  **bool
	}{
		columnVariantManageInventory: {v.ManageInventory, &in.ManageInventory},
		columnVariantAllowBackorder:  {v.AllowBackorder, &in.AllowBackorder},
	} {
		value, err := row.optBool(column)
		if err != nil {
			return UpdateVariantInput{}, false, err
		}
		if value != nil && *value != pair.current {
			*pair.target, changed = value, true
		}
	}
	weight, err := row.optInt32(columnVariantWeight)
	if err != nil {
		return UpdateVariantInput{}, false, err
	}
	if weight != nil && (v.Weight == nil || *v.Weight != *weight) {
		in.Weight, changed = weight, true
	}
	metadata, err := jsonObject[any](row, "variant_metadata")
	if err != nil {
		return UpdateVariantInput{}, false, err
	}
	if metadata != nil && !sameJSON(metadata, v.Metadata) {
		in.Metadata, changed = metadata, true
	}

	return in, changed, nil
}

// ensureImportedOptions adds the options and values the row names that the
// product lacks, and reports whether it added any.
func (s *Service) ensureImportedOptions(ctx context.Context, p models.Product, options map[string]string) (bool, error) {
	added := false
	for _, title := range slices.Sorted(maps.Keys(options)) {
		value := options[title]
		index := slices.IndexFunc(p.Options, func(o models.Option) bool { return sameName(o.Title, title) })
		if index < 0 {
			if _, err := s.CreateOption(ctx, p.ID, CreateOptionInput{Title: title, Values: []string{value}}); err != nil {
				return false, err
			}
			added = true

			continue
		}
		if slices.ContainsFunc(p.Options[index].Values, func(v models.OptionValue) bool { return sameName(v.Value, value) }) {
			continue
		}
		if _, err := s.AddOptionValue(ctx, p.Options[index].ID, value); err != nil {
			return false, err
		}
		added = true
	}

	return added, nil
}

// optionValueIDs resolves the row's options to the product's value ids.
func (s *Service) optionValueIDs(ctx context.Context, productID string, options map[string]string) ([]string, error) {
	current, err := s.ListOptions(ctx, productID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(options))
	for _, title := range slices.Sorted(maps.Keys(options)) {
		for i := range current {
			if !sameName(current[i].Title, title) {
				continue
			}
			for j := range current[i].Values {
				if sameName(current[i].Values[j].Value, options[title]) {
					ids = append(ids, current[i].Values[j].ID)
				}
			}
		}
	}
	if len(ids) != len(options) {
		return nil, fmt.Errorf("the options %v could not all be found on product %s", options, productID)
	}

	return ids, nil
}

// sameOptions reports whether a variant's values are exactly the row's options.
func sameOptions(values []models.OptionValue, options map[string]string) bool {
	if len(values) != len(options) {
		return false
	}
	for i := range values {
		matched := false
		for title, wanted := range options {
			if sameName(values[i].OptionTitle, title) && sameName(values[i].Value, wanted) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// sameName compares two option titles or values as resolveByTitle does:
// trimmed and lowered. strings.EqualFold is not the same comparison: it does not
// take U+0130, the capital I with a dot, for "i", and resolveByTitle does.
func sameName(a, b string) bool {
	return strings.ToLower(strings.TrimSpace(a)) == strings.ToLower(strings.TrimSpace(b)) //nolint:staticcheck // SA6005: see above
}

// sameJSON compares two metadata maps by their JSON.
func sameJSON(a, b map[string]any) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)

	return errLeft == nil && errRight == nil && bytes.Equal(left, right)
}

// sameIDs compares two id sets, order aside.
func sameIDs(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

func tagIDs(tags []models.Tag) []string {
	out := make([]string, 0, len(tags))
	for i := range tags {
		out = append(out, tags[i].ID)
	}

	return out
}

func categoryIDs(categories []models.Category) []string {
	out := make([]string, 0, len(categories))
	for i := range categories {
		out = append(out, categories[i].ID)
	}

	return out
}
