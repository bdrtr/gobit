package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// CreateOptionInput is the input of an option (and of its values).
type CreateOptionInput struct {
	Title  string
	Values []string
	Rank   int32
}

// CreateVariantInput is the input of a new variant.
//
// The option values of the variant can be given in TWO ways:
//
//   - OptionValueIDs: when the ids of the values are known (while a variant is
//     being added to an existing product).
//   - Options: an option TITLE -> VALUE mapping. When the product and its
//     options are created in the same request the ids of the values are not yet
//     known; this way is the only practical solution in that case.
//
// Both can be given at once; if two different values fall onto the same option
// the request is rejected (had one been picked silently, which variant was
// created would be unpredictable).
type CreateVariantInput struct {
	Title           string
	SKU             *string
	Barcode         *string
	EAN             *string
	UPC             *string
	ManageInventory *bool
	AllowBackorder  *bool
	Weight          *int32
	Rank            *int32
	Metadata        map[string]any
	OptionValueIDs  []string
	Options         map[string]string
}

// UpdateVariantInput is the partial update of a variant; a nil field does not
// change.
type UpdateVariantInput struct {
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
	// OptionValueIDs, when nil, leaves the option values unchanged; when given,
	// ALL of the variant's option values are replaced with this set.
	OptionValueIDs []string
}

// ListVariantsOptions is the criteria of a variant listing.
type ListVariantsOptions struct {
	ProductID *string
	Limit     int
	Offset    int
	// WithOptionValues, when true, fills the option values of the variants with
	// a SINGLE query.
	WithOptionValues bool
}

// binding shows the variant's value under one option.
type binding struct {
	optionID string
	valueID  string
}

// CreateVariant adds a variant to the product.
//
// If the product does not exist (or has been deleted) errors.NotFound is
// returned: writing the variant without its owner would produce a record that
// shows up in no listing.
//
// The product check is done IN THE SAME TRANSACTION as the variant and with a
// row lock. Had it been done outside the transaction, a concurrent DeleteProduct
// could slip between the check and the INSERT: because the delete is SOFT, the
// foreign key on product_variant does not close the gap and out would come a
// variant whose deleted_at is NULL but whose owner is deleted — a record visible
// on the admin endpoints and in the "variant.query" provider but bound to no
// product. The lock lines the two requests up (see
// repository.Store.GetProductForUpdate).
func (s *Service) CreateVariant(ctx context.Context, productID string, in CreateVariantInput) (models.Variant, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return models.Variant{}, err
	}

	var created models.Variant
	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.GetProductForUpdate(ctx, productID); err != nil {
			return err
		}
		v, err := createVariantTx(ctx, tx, productID, in, 0)
		if err != nil {
			return err
		}
		created = v
		return nil
	})
	if err != nil {
		return models.Variant{}, err
	}

	return s.GetVariant(ctx, created.ID)
}

// GetVariant returns the variant together with its option values.
func (s *Service) GetVariant(ctx context.Context, id string) (models.Variant, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Variant{}, err
	}

	variant, err := s.repo.GetVariant(ctx, id)
	if err != nil {
		return models.Variant{}, err
	}

	variants := []models.Variant{variant}
	if err := s.attachVariantOptionValues(ctx, variants); err != nil {
		return models.Variant{}, err
	}
	return variants[0], nil
}

// ListVariants returns the variants matching the criteria, paginated.
func (s *Service) ListVariants(ctx context.Context, opts ListVariantsOptions) (ListResult[models.Variant], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}

	filter := repository.VariantFilter{ProductID: opts.ProductID, Limit: limit, Offset: offset}
	variants, err := s.repo.ListVariants(ctx, filter)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}
	count, err := s.repo.CountVariants(ctx, filter)
	if err != nil {
		return ListResult[models.Variant]{}, err
	}
	if opts.WithOptionValues {
		if err := s.attachVariantOptionValues(ctx, variants); err != nil {
			return ListResult[models.Variant]{}, err
		}
	}

	return ListResult[models.Variant]{Items: variants, Count: &count, Offset: offset, Limit: limit}, nil
}

// UpdateVariant updates the variant partially.
func (s *Service) UpdateVariant(ctx context.Context, id string, in UpdateVariantInput) (models.Variant, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Variant{}, err
	}

	patch := repository.VariantPatch{
		SKU:             in.SKU,
		Barcode:         in.Barcode,
		EAN:             in.EAN,
		UPC:             in.UPC,
		ManageInventory: in.ManageInventory,
		AllowBackorder:  in.AllowBackorder,
		Weight:          in.Weight,
		Rank:            in.Rank,
		Metadata:        in.Metadata,
	}
	if in.Title != nil {
		title, err := requireText("title", *in.Title, maxTitleLen)
		if err != nil {
			return models.Variant{}, err
		}
		patch.Title = &title
	}

	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		updated, err := tx.UpdateVariant(ctx, id, patch)
		if err != nil {
			return err
		}
		if in.OptionValueIDs == nil {
			return nil
		}
		return replaceOptionValues(ctx, tx, updated.ProductID, id, in.OptionValueIDs)
	})
	if err != nil {
		return models.Variant{}, err
	}

	return s.GetVariant(ctx, id)
}

// DeleteVariant SOFT deletes the variant and cleans up its price/stock links.
func (s *Service) DeleteVariant(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}
	if err := s.repo.SoftDeleteVariant(ctx, id); err != nil {
		return err
	}

	s.cleanupVariantLinks(ctx, id)
	return nil
}

// SetVariantOptionValues replaces the variant's option values with the given set.
func (s *Service) SetVariantOptionValues(ctx context.Context, variantID string, valueIDs []string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}

	variant, err := s.repo.GetVariant(ctx, variantID)
	if err != nil {
		return err
	}
	return s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		return replaceOptionValues(ctx, tx, variant.ProductID, variantID, valueIDs)
	})
}

// CreateOption adds an option (and the values given with it) to the product.
//
// The returned record is the STORED row, not the in-memory model: the
// timestamps are produced only in the database, and returning the model's zero
// timestamps would produce "0001-01-01T00:00:00Z" in the response. Every other
// create endpoint returns the row the database gave back too; this contract is
// shared.
func (s *Service) CreateOption(ctx context.Context, productID string, in CreateOptionInput) (models.Option, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return models.Option{}, err
	}
	options, err := buildOptions(productID, []CreateOptionInput{in})
	if err != nil {
		return models.Option{}, err
	}

	var created models.Option
	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		// The existence of the product is verified INSIDE THE TRANSACTION and
		// with a row lock. If it is verified outside, an intervening DELETE
		// /admin/v1/products/{id} leaves an option whose owner is deleted but
		// whose deleted_at is NULL; because the delete is SOFT the foreign key
		// does not close that gap (see the same pattern in CreateVariant).
		if _, err := tx.GetProductForUpdate(ctx, productID); err != nil {
			return err
		}

		stored, err := writeOptions(ctx, tx, options)
		if err != nil {
			return err
		}
		created = stored[0]
		return nil
	})
	if err != nil {
		return models.Option{}, err
	}
	return created, nil
}

// ListOptions returns the product's options together with their values.
func (s *Service) ListOptions(ctx context.Context, productID string) ([]models.Option, error) {
	if _, err := requireID("product_id", productID); err != nil {
		return nil, err
	}

	options, err := s.repo.ListOptionsByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	return s.attachOptionValues(ctx, options)
}

// AddOptionValue adds a value to an existing option.
//
// The new value is put at the END of the list: its rank is one more than the
// option's current largest rank. Had the rank not been filled in, the zero value
// (0) would be written, and because reads are ordered by rank an "XL" added to
// an option defined as "S(0), M(1), L(2)" would fall not at the end of the list
// but at its HEAD: "S, XL, M, L".
func (s *Service) AddOptionValue(ctx context.Context, optionID, value string) (models.OptionValue, error) {
	if _, err := requireID("option_id", optionID); err != nil {
		return models.OptionValue{}, err
	}
	clean, err := requireText("value", value, maxValueLen)
	if err != nil {
		return models.OptionValue{}, err
	}
	option, err := s.repo.GetOption(ctx, optionID)
	if err != nil {
		return models.OptionValue{}, err
	}
	existing, err := s.repo.ListOptionValuesByOptionIDs(ctx, []string{option.ID})
	if err != nil {
		return models.OptionValue{}, err
	}

	return s.repo.CreateOptionValue(ctx, models.OptionValue{
		ID:       newID(prefixOptionValue),
		OptionID: option.ID,
		Value:    clean,
		Rank:     nextRank(existing),
	})
}

// nextRank produces the rank to be appended AFTER the given values.
//
// On an empty list it is 0. Overflow saturates at math.MaxInt32: an overflowing
// rank would turn negative and an "append to the end" request would move the
// value to the head of the list.
func nextRank(values []models.OptionValue) int32 {
	highest := int32(-1)
	for i := range values {
		if values[i].Rank > highest {
			highest = values[i].Rank
		}
	}
	if highest == math.MaxInt32 {
		return math.MaxInt32
	}
	return highest + 1
}

// DeleteOption SOFT deletes the option.
func (s *Service) DeleteOption(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}
	return s.repo.SoftDeleteOption(ctx, id)
}

// buildOptions validates the option inputs and turns them into models with ids.
func buildOptions(productID string, in []CreateOptionInput) ([]models.Option, error) {
	out := make([]models.Option, 0, len(in))
	seenTitles := make(map[string]struct{}, len(in))

	for i, opt := range in {
		title, err := requireText("options[].title", opt.Title, maxTitleLen)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(title)
		if _, dup := seenTitles[key]; dup {
			return nil, invalid("the same option title was given twice: %q", title)
		}
		seenTitles[key] = struct{}{}

		rank := opt.Rank
		if rank == 0 {
			rank = int32From(i)
		}
		option := models.Option{
			ID:        newID(prefixOption),
			ProductID: productID,
			Title:     title,
			Rank:      rank,
		}

		seenValues := make(map[string]struct{}, len(opt.Values))
		for j, raw := range opt.Values {
			value, err := requireText("options[].values[]", raw, maxValueLen)
			if err != nil {
				return nil, err
			}
			if _, dup := seenValues[strings.ToLower(value)]; dup {
				return nil, invalid("the same value was given twice for the %q option: %q", title, value)
			}
			seenValues[strings.ToLower(value)] = struct{}{}

			option.Values = append(option.Values, models.OptionValue{
				ID:          newID(prefixOptionValue),
				OptionID:    option.ID,
				Value:       value,
				Rank:        int32From(j),
				OptionTitle: title,
			})
		}
		out = append(out, option)
	}
	return out, nil
}

// writeOptions writes the options and their values; it returns the STORED rows.
//
// The returned rows are the ones the database gave back with RETURNING. The
// timestamps are produced there, the in-memory model does not carry them;
// returning the written model would mean showing the client zero timestamps.
func writeOptions(ctx context.Context, tx repository.Store, options []models.Option) ([]models.Option, error) {
	stored := make([]models.Option, 0, len(options))
	for i := range options {
		option, err := tx.CreateOption(ctx, options[i])
		if err != nil {
			return nil, err
		}
		option.Values = make([]models.OptionValue, 0, len(options[i].Values))
		for j := range options[i].Values {
			value, err := tx.CreateOptionValue(ctx, options[i].Values[j])
			if err != nil {
				return nil, err
			}
			// OptionTitle IS NOT A COLUMN (see models.OptionValue); RETURNING
			// does not fill it in, so it is carried over from the written model.
			value.OptionTitle = options[i].Values[j].OptionTitle
			option.Values = append(option.Values, value)
		}
		stored = append(stored, option)
	}
	return stored, nil
}

// createVariantTx writes the variant and its option bindings IN AN OPEN
// TRANSACTION.
//
// fallbackRank is the submission order used when no rank is given.
func createVariantTx(
	ctx context.Context,
	tx repository.Store,
	productID string,
	in CreateVariantInput,
	fallbackRank int32,
) (models.Variant, error) {
	title, err := requireText("variants[].title", in.Title, maxTitleLen)
	if err != nil {
		return models.Variant{}, err
	}
	sku, err := trimOptional(in.SKU, "sku", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	barcode, err := trimOptional(in.Barcode, "barcode", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	ean, err := trimOptional(in.EAN, "ean", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}
	upc, err := trimOptional(in.UPC, "upc", maxValueLen)
	if err != nil {
		return models.Variant{}, err
	}

	manageInventory := true
	if in.ManageInventory != nil {
		manageInventory = *in.ManageInventory
	}
	allowBackorder := false
	if in.AllowBackorder != nil {
		allowBackorder = *in.AllowBackorder
	}
	rank := fallbackRank
	if in.Rank != nil {
		rank = *in.Rank
	}

	bindings, err := resolveBindings(ctx, tx, productID, in.OptionValueIDs, in.Options)
	if err != nil {
		return models.Variant{}, err
	}

	variant, err := tx.CreateVariant(ctx, models.Variant{
		ID:              newID(prefixVariant),
		ProductID:       productID,
		Title:           title,
		SKU:             sku,
		Barcode:         barcode,
		EAN:             ean,
		UPC:             upc,
		ManageInventory: manageInventory,
		AllowBackorder:  allowBackorder,
		Weight:          in.Weight,
		Rank:            rank,
		Metadata:        in.Metadata,
	})
	if err != nil {
		return models.Variant{}, err
	}

	for _, b := range bindings {
		if err := tx.SetVariantOptionValue(ctx, variant.ID, b.optionID, b.valueID); err != nil {
			return models.Variant{}, err
		}
	}
	return variant, nil
}

// replaceOptionValues replaces the variant's option values with the given set.
func replaceOptionValues(ctx context.Context, tx repository.Store, productID, variantID string, valueIDs []string) error {
	bindings, err := resolveBindings(ctx, tx, productID, valueIDs, nil)
	if err != nil {
		return err
	}
	if err := tx.DeleteVariantOptionValues(ctx, variantID); err != nil {
		return err
	}
	for _, b := range bindings {
		if err := tx.SetVariantOptionValue(ctx, variantID, b.optionID, b.valueID); err != nil {
			return err
		}
	}
	return nil
}

// resolveBindings turns the given value ids and the title->value mapping into
// the bindings to be written onto the variant.
//
// Two validations are mandatory:
//
//   - The value has to REALLY exist. Had an id that does not exist been skipped
//     silently, the variant would be created with missing options and the bug
//     would only be seen in the storefront.
//   - The value has to come from an option of the SAME PRODUCT. Could another
//     product's value be bound, this variant would break too when that
//     product's option was deleted.
func resolveBindings(
	ctx context.Context,
	store repository.Store,
	productID string,
	valueIDs []string,
	byTitle map[string]string,
) ([]binding, error) {
	byOption := make(map[string]string, len(valueIDs)+len(byTitle))

	if len(valueIDs) > 0 {
		ids, err := uniqueIDs("option_value_ids", valueIDs)
		if err != nil {
			return nil, err
		}
		refs, err := store.ListOptionValuesByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		found := make(map[string]models.OptionValueRef, len(refs))
		for i := range refs {
			found[refs[i].ID] = refs[i]
		}
		for _, id := range ids {
			ref, ok := found[id]
			if !ok {
				return nil, invalid("the option value was not found: %s", id)
			}
			if ref.ProductID != productID {
				return nil, invalid(
					"the option value %s belongs to another product (%s); only the values of its own product can be bound to a variant",
					id, ref.ProductID)
			}
			if err := bind(byOption, ref.OptionID, ref.ID, ref.OptionTitle); err != nil {
				return nil, err
			}
		}
	}

	if len(byTitle) > 0 {
		resolved, err := resolveByTitle(ctx, store, productID, byTitle)
		if err != nil {
			return nil, err
		}
		for optionID, value := range resolved {
			if err := bind(byOption, optionID, value.id, value.title); err != nil {
				return nil, err
			}
		}
	}

	out := make([]binding, 0, len(byOption))
	for optionID, valueID := range byOption {
		out = append(out, binding{optionID: optionID, valueID: valueID})
	}
	// The order has to be deterministic: otherwise the same input produces a
	// different write order on every run and the tests become flaky.
	slices.SortFunc(out, func(a, b binding) int { return strings.Compare(a.optionID, b.optionID) })
	return out, nil
}

// bind assigns a value to an option; if a DIFFERENT second value arrives for the
// same option it returns an error.
func bind(byOption map[string]string, optionID, valueID, optionTitle string) error {
	if existing, ok := byOption[optionID]; ok && existing != valueID {
		return invalid("two different values were given for the %q option (%s and %s)", optionTitle, existing, valueID)
	}
	byOption[optionID] = valueID
	return nil
}

// titleValue is the id and the option title of a value resolved by title.
type titleValue struct {
	id    string
	title string
}

// resolveByTitle turns an "option title -> value" mapping into ids.
//
// The matching is case-insensitive: the client writing "Size" or "size" has to
// point at the same option.
func resolveByTitle(
	ctx context.Context,
	store repository.Store,
	productID string,
	byTitle map[string]string,
) (map[string]titleValue, error) {
	options, err := store.ListOptionsByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	if len(options) == 0 {
		return nil, invalid("the product has no defined option; an option value cannot be given (product: %s)", productID)
	}

	optionIDs := make([]string, 0, len(options))
	optionByTitle := make(map[string]models.Option, len(options))
	for i := range options {
		optionIDs = append(optionIDs, options[i].ID)
		optionByTitle[strings.ToLower(options[i].Title)] = options[i]
	}

	values, err := store.ListOptionValuesByOptionIDs(ctx, optionIDs)
	if err != nil {
		return nil, err
	}
	valueKey := func(optionID, value string) string { return optionID + "\x00" + strings.ToLower(value) }
	valueByKey := make(map[string]models.OptionValue, len(values))
	for i := range values {
		valueByKey[valueKey(values[i].OptionID, values[i].Value)] = values[i]
	}

	out := make(map[string]titleValue, len(byTitle))
	for title, value := range byTitle {
		option, ok := optionByTitle[strings.ToLower(strings.TrimSpace(title))]
		if !ok {
			return nil, invalid("the product has no option titled %q", title)
		}
		match, ok := valueByKey[valueKey(option.ID, strings.TrimSpace(value))]
		if !ok {
			return nil, invalid("the %q option has no value %q defined", option.Title, value)
		}
		out[option.ID] = titleValue{id: match.ID, title: option.Title}
	}
	return out, nil
}
