package service

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// MaxBundleComponents is the most variants a bundle is made of (ADR 0234).
//
// A bundle is a set a shop puts together, and checkout reserves each component
// on its own; the bound keeps that to a handful of reservations per line, and
// it is refused rather than cut when exceeded.
const MaxBundleComponents = 20

// MaxBundleComponentQuantity is the most units of one component a bundle holds.
const MaxBundleComponentQuantity = 100

// CodeBundleShape is the code of a bundle write the catalog's state refuses: a
// bundle inside a bundle, a bundle sold past zero or not counted, a bundle with
// a stock item of its own. The input is well formed; another record has to
// change first.
const CodeBundleShape = "product_bundle_shape"

// VariantBundle returns the components of a bundle variant in the operator's
// order, and an empty list for a variant that is no bundle (ADR 0234).
func (s *Service) VariantBundle(ctx context.Context, variantID string) ([]models.BundleComponent, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return nil, err
	}
	byBundle, err := s.repo.ListBundleComponents(ctx, []string{variantID})
	if err != nil {
		return nil, err
	}
	components := byBundle[variantID]
	if components == nil {
		components = []models.BundleComponent{}
	}
	return components, nil
}

// SetVariantBundle replaces what a variant is made of with the given
// components, in that order, and returns them (ADR 0234); an empty list makes
// it a plain variant again.
//
// A component is a live variant of ANOTHER product that is not itself a bundle,
// and not a gift card; the bundle is counted, not sold past zero, has no stock
// item of its own, is not a gift card and is no bundle's component. The write
// is a revision of the bundle's product and locks every variant it names, so a
// component deleted at the same instant is either seen as gone or refused its
// deletion.
func (s *Service) SetVariantBundle(
	ctx context.Context, variantID string, components []models.BundleComponent,
) ([]models.BundleComponent, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return nil, err
	}
	componentIDs, err := bundleComponentIDs(components)
	if err != nil {
		return nil, err
	}
	variant, err := s.repo.GetVariant(ctx, variantID)
	if err != nil {
		return nil, err
	}
	if len(components) > 0 {
		if err := s.requireNoInventoryItem(ctx, variantID); err != nil {
			return nil, err
		}
	}

	err = s.revise(ctx, variant.ProductID, func(ctx context.Context, tx repository.Store) error {
		locked, err := tx.LockLiveVariantsForBundle(ctx, append([]string{variantID}, componentIDs...))
		if err != nil {
			return err
		}
		bundle, ok := locked[variantID]
		if !ok {
			return errors.NotFound(codeNotFound, "no such variant: %s", variantID)
		}
		if len(components) > 0 {
			if err := requireBundleShape(ctx, tx, bundle, componentIDs, locked); err != nil {
				return err
			}
		}
		return tx.ReplaceBundleComponents(ctx, variantID, components)
	})
	if err != nil {
		return nil, err
	}
	return s.VariantBundle(ctx, variantID)
}

// bundleComponentIDs checks the composition as given — its length, each id and
// quantity, no variant twice — and returns its ids. The bundle itself is its
// product's own variant, which requireBundleShape refuses.
func bundleComponentIDs(components []models.BundleComponent) ([]string, error) {
	if len(components) > MaxBundleComponents {
		return nil, invalid("a bundle holds at most %d components, %d given", MaxBundleComponents, len(components))
	}
	ids := make([]string, 0, len(components))
	for _, c := range components {
		if _, err := requireID("components.variant_id", c.VariantID); err != nil {
			return nil, err
		}
		if c.Quantity < 1 || c.Quantity > MaxBundleComponentQuantity {
			return nil, invalid("a component's quantity is between 1 and %d (variant %s: %d)",
				MaxBundleComponentQuantity, c.VariantID, c.Quantity)
		}
		if slices.Contains(ids, c.VariantID) {
			return nil, invalid("the bundle names a component more than once: %s", c.VariantID)
		}
		ids = append(ids, c.VariantID)
	}
	return ids, nil
}

// requireBundleShape checks the locked bundle and components against each other
// and against the bundles already written; the rows are held, so what it reads
// stays true until the write commits.
func requireBundleShape(
	ctx context.Context, tx repository.Store, bundle repository.BundleCandidate,
	componentIDs []string, locked map[string]repository.BundleCandidate,
) error {
	switch {
	case bundle.IsGiftcard:
		return invalid("a gift card variant cannot be a bundle: %s", bundle.ID)
	case !bundle.ManageInventory || bundle.AllowBackorder:
		return errors.Conflict(CodeBundleShape,
			"a bundle is counted and is not sold past zero on its own; set manage_inventory and clear allow_backorder first (%s)",
			bundle.ID)
	}

	var missing, own, giftcards []string
	for _, id := range componentIDs {
		c, ok := locked[id]
		switch {
		case !ok:
			missing = append(missing, id)
		case c.ProductID == bundle.ProductID:
			own = append(own, id)
		case c.IsGiftcard:
			giftcards = append(giftcards, id)
		}
	}
	if len(missing) > 0 {
		return invalid("no such variant: %s", strings.Join(missing, ", "))
	}
	if len(own) > 0 {
		return invalid("a product's own variant cannot be its bundle's component: %s", strings.Join(own, ", "))
	}
	if len(giftcards) > 0 {
		return invalid("a gift card variant cannot be a bundle's component: %s", strings.Join(giftcards, ", "))
	}

	nested, err := tx.ListBundleComponents(ctx, componentIDs)
	if err != nil {
		return err
	}
	if len(nested) > 0 {
		return errors.Conflict(CodeBundleShape, "a bundle cannot hold a bundle: %s",
			strings.Join(slices.Sorted(maps.Keys(nested)), ", "))
	}
	holders, err := tx.ListBundlesContaining(ctx, []string{bundle.ID})
	if err != nil {
		return err
	}
	if held := holders[bundle.ID]; len(held) > 0 {
		return errors.Conflict(CodeBundleShape, "a bundle's component cannot be a bundle; %s is held by %s",
			bundle.ID, strings.Join(held, ", "))
	}
	return nil
}

// requireNoInventoryItem refuses to make a bundle of a variant linked to a stock
// item: a bundle's stock is its components'. The link lives in the link
// service, outside the product transaction.
func (s *Service) requireNoInventoryItem(ctx context.Context, variantID string) error {
	if s.links == nil {
		return nil
	}
	items, err := s.links.List(ctx, LinkVariantInventory, variantID)
	if err != nil {
		return wrapLink(err, "the %q link could not be read (variant: %s)", LinkVariantInventory, variantID)
	}
	if len(items) > 0 {
		return errors.Conflict(CodeBundleShape,
			"a bundle's stock is its components'; clear the variant's inventory item first (%s)", variantID)
	}
	return nil
}

// requireNotBundle refuses to link a bundle variant to a stock item, the other
// side of [Service.requireNoInventoryItem].
func (s *Service) requireNotBundle(ctx context.Context, variantID string) error {
	byBundle, err := s.repo.ListBundleComponents(ctx, []string{variantID})
	if err != nil {
		return err
	}
	if len(byBundle[variantID]) > 0 {
		return errors.Conflict(CodeBundleShape,
			"a bundle's stock is its components'; it takes no inventory item of its own (%s)", variantID)
	}
	return nil
}

// requireBundleCounted refuses an update that would leave a bundle variant
// uncounted or sold past zero, given the flags the update would write; the
// update and a bundle write revise the same product, so they do not interleave.
func requireBundleCounted(ctx context.Context, tx repository.Store, id string, manage, backorder bool) error {
	if manage && !backorder {
		return nil
	}
	byBundle, err := tx.ListBundleComponents(ctx, []string{id})
	if err != nil {
		return err
	}
	if len(byBundle[id]) > 0 {
		return errors.Conflict(CodeBundleShape,
			"a bundle is counted and is not sold past zero on its own; take its components off first (%s)", id)
	}
	return nil
}

// requireNotComponent refuses the deletion of variants a live bundle holds. It
// locks their rows first, which a concurrent bundle write naming them waits on
// or has taken, so the answer holds until the deleting transaction ends; the
// caller runs it inside that transaction, before the soft delete.
func requireNotComponent(ctx context.Context, tx repository.Store, variantIDs []string) error {
	if _, err := tx.LockLiveVariantsForBundle(ctx, variantIDs); err != nil {
		return err
	}
	holders, err := tx.ListBundlesContaining(ctx, variantIDs)
	if err != nil {
		return err
	}
	if len(holders) == 0 {
		return nil
	}
	var parts []string
	for _, id := range slices.Sorted(maps.Keys(holders)) {
		parts = append(parts, id+" (held by "+strings.Join(holders[id], ", ")+")")
	}
	return errors.Conflict(codeInUse,
		"a bundle's component cannot be deleted; take it out of the bundle first: %s", strings.Join(parts, "; "))
}

// attachBundleComponents fills the components of the variants in a SINGLE query.
func attachBundleComponents(ctx context.Context, store repository.Store, variants []models.Variant) error {
	if len(variants) == 0 {
		return nil
	}
	ids := make([]string, 0, len(variants))
	for i := range variants {
		ids = append(ids, variants[i].ID)
	}
	byBundle, err := store.ListBundleComponents(ctx, ids)
	if err != nil {
		return err
	}
	for i := range variants {
		variants[i].BundleComponents = byBundle[variants[i].ID]
	}
	return nil
}

// valueOr is the value a patch field would write: the given one, or the current
// one when the patch leaves the field out.
func valueOr[T any](given *T, current T) T {
	if given != nil {
		return *given
	}
	return current
}
