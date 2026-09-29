package service

import (
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// MaxAddOns is the longest add-on list a product holds (ADR 0228).
//
// A product page offers a handful of add-ons; the bound keeps the storefront
// read, which enriches each one's product with prices and stock, to a page's
// worth, and it is refused rather than cut when exceeded.
const MaxAddOns = 20

// StoreAddOn is one add-on as the storefront shows it: the variant a line may
// carry and its product, with the product's prices and stock.
type StoreAddOn struct {
	// VariantID is the add-on variant a cart line of the product may carry.
	VariantID string `json:"variant_id"`
	// Product is the add-on's product as the storefront reads it; its
	// variants include VariantID.
	Product StoreProduct `json:"product"`
}

// ProductAddOns returns the variants a product's cart lines may carry as
// add-ons, in the operator's order (ADR 0228).
func (s *Service) ProductAddOns(ctx context.Context, id string) ([]string, error) {
	if _, err := requireID("id", id); err != nil {
		return nil, err
	}
	if _, err := s.GetProduct(ctx, id); err != nil {
		return nil, err
	}
	ids, err := s.repo.ListProductAddOns(ctx, id)
	if err != nil {
		return nil, err
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// SetProductAddOns replaces a product's add-on list with the given variants, in
// that order, and returns it (ADR 0228).
//
// Every id has to name a live variant of ANOTHER product; one that does not is
// refused, naming it, rather than dropped, for the relations' reason (ADR 0180).
// The add-on's product is not required to be published or in a channel: the
// storefront read and the cart decide that, so an add-on can be lined up before
// it launches.
func (s *Service) SetProductAddOns(ctx context.Context, id string, variantIDs []string) ([]string, error) {
	if _, err := requireID("id", id); err != nil {
		return nil, err
	}
	wanted, err := uniqueIDs("variant_ids", variantIDs)
	if err != nil {
		return nil, err
	}
	if len(wanted) != len(variantIDs) {
		return nil, invalid("the add-on list names a variant more than once")
	}
	if len(wanted) > MaxAddOns {
		return nil, invalid("a product holds at most %d add-ons, %d given", MaxAddOns, len(wanted))
	}
	if _, err := s.GetProduct(ctx, id); err != nil {
		return nil, err
	}
	if err := s.requireAddOnVariants(ctx, id, wanted); err != nil {
		return nil, err
	}

	if err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		return tx.ReplaceProductAddOns(ctx, id, wanted)
	}); err != nil {
		return nil, err
	}
	return s.ProductAddOns(ctx, id)
}

// ResolveVariantRefs turns references an operator typed — a variant id, or a
// SKU — into variant ids, in the given order (ADR 0232).
//
// A reference starting with the variant id prefix is an id, and anything else a
// SKU, which is how an operator knows a variant; a SKU no live variant carries
// is refused, naming it as it was typed, and so is an empty reference. An id is
// passed through: whether it names a live variant is the write's question.
func (s *Service) ResolveVariantRefs(ctx context.Context, refs []string) ([]string, error) {
	var skus []string
	for _, ref := range refs {
		if _, err := requireID("variant", ref); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(ref, prefixVariant) {
			skus = append(skus, ref)
		}
	}

	bySKU := make(map[string]string, len(skus))
	if len(skus) > 0 {
		found, err := s.repo.ListVariantsBySKUs(ctx, skus)
		if err != nil {
			return nil, err
		}
		for i := range found {
			if found[i].SKU != nil {
				bySKU[*found[i].SKU] = found[i].ID
			}
		}
	}

	out := make([]string, 0, len(refs))
	var missing []string
	for _, ref := range refs {
		if strings.HasPrefix(ref, prefixVariant) {
			out = append(out, ref)
			continue
		}
		id, ok := bySKU[ref]
		if !ok {
			missing = append(missing, ref)
			continue
		}
		out = append(out, id)
	}
	if len(missing) > 0 {
		return nil, invalid("no variant has the SKU: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// requireAddOnVariants refuses, naming them, the ids no live variant carries
// and the variants of the product itself: a line cannot carry its own product
// as an add-on.
func (s *Service) requireAddOnVariants(ctx context.Context, productID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	found, err := s.repo.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return err
	}
	owner := make(map[string]string, len(found))
	for i := range found {
		owner[found[i].ID] = found[i].ProductID
	}
	var missing, own []string
	for _, id := range ids {
		product, ok := owner[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case product == productID:
			own = append(own, id)
		}
	}
	if len(missing) > 0 {
		return invalid("no such variant: %s", strings.Join(missing, ", "))
	}
	if len(own) > 0 {
		return invalid("a product's own variant cannot be its add-on: %s", strings.Join(own, ", "))
	}
	return nil
}

// StoreProductAddOns returns a product's add-ons as the storefront shows them
// (ADR 0228).
//
// The product it starts from has to be one the storefront may show, the single
// endpoint's rule, or the answer is NotFound. Each add-on's product then goes
// through [Service.StoreProductsByIDs], the rule the related products use:
// published, visible in the request's channels, and an add-on whose product is
// not is skipped without saying so. The order is the operator's.
func (s *Service) StoreProductAddOns(
	ctx context.Context, idOrHandle string, salesChannelIDs []string,
) ([]StoreAddOn, error) {
	product, err := s.visibleStoreProduct(ctx, idOrHandle, salesChannelIDs)
	if err != nil {
		return nil, err
	}
	ids, err := s.repo.ListProductAddOns(ctx, product.ID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []StoreAddOn{}, nil
	}
	variants, err := s.repo.ListVariantsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	owner := make(map[string]string, len(variants))
	var productIDs []string
	for i := range variants {
		owner[variants[i].ID] = variants[i].ProductID
		if !slices.Contains(productIDs, variants[i].ProductID) {
			productIDs = append(productIDs, variants[i].ProductID)
		}
	}
	shown, err := s.StoreProductsByIDs(ctx, productIDs, salesChannelIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]StoreProduct, len(shown))
	for i := range shown {
		byID[shown[i].ID] = shown[i]
	}

	out := make([]StoreAddOn, 0, len(ids))
	for _, id := range ids {
		if addOn, visible := byID[owner[id]]; visible {
			out = append(out, StoreAddOn{VariantID: id, Product: addOn})
		}
	}
	return out, nil
}
