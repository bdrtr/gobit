package repository

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// ListBundleComponents returns the components of every given bundle variant in
// one statement, each in the operator's order (ADR 0234); a variant that is no
// bundle is absent from the map.
func (r *Repo) ListBundleComponents(
	ctx context.Context, bundleIDs []string,
) (map[string][]models.BundleComponent, error) {
	out := make(map[string][]models.BundleComponent)
	if len(bundleIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListBundleComponentsOfVariants(ctx, bundleIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the components of %d variants", len(bundleIDs))
	}
	for _, row := range rows {
		out[row.BundleVariantID] = append(out[row.BundleVariantID], models.BundleComponent{
			VariantID: row.ComponentVariantID, Quantity: row.Quantity,
		})
	}
	return out, nil
}

// ReplaceBundleComponents writes a bundle's composition: the old one is deleted
// and the new one written with the position as the rank. It has to run inside a
// transaction, or a reader between the two statements sees no bundle.
func (r *Repo) ReplaceBundleComponents(
	ctx context.Context, bundleID string, components []models.BundleComponent,
) error {
	if err := r.q.DeleteBundleComponents(ctx, bundleID); err != nil {
		return wrapDB(err, "could not clear the components of variant: %s", bundleID)
	}
	if len(components) == 0 {
		return nil
	}
	ids := make([]string, 0, len(components))
	quantities := make([]int32, 0, len(components))
	for _, c := range components {
		ids = append(ids, c.VariantID)
		quantities = append(quantities, c.Quantity)
	}
	if err := r.q.InsertBundleComponents(ctx, productdb.InsertBundleComponentsParams{
		BundleID: bundleID, ComponentIds: ids, Quantities: quantities,
	}); err != nil {
		return wrapDB(err, "could not write the components of variant: %s", bundleID)
	}
	return nil
}

// BundleCandidate is a live variant as a bundle write checks it: its product,
// whether it is counted and sold past zero, and whether its product is a gift
// card.
type BundleCandidate struct {
	ID              string
	ProductID       string
	ManageInventory bool
	AllowBackorder  bool
	IsGiftcard      bool
}

// LockLiveVariantsForBundle returns the live variants among the given ids and
// holds their rows until the transaction ends, taken in id order so that two
// bundle writes naming the same variants do not deadlock. A variant a
// concurrent deletion took is absent once that deletion commits. It has to run
// inside a transaction.
func (r *Repo) LockLiveVariantsForBundle(ctx context.Context, ids []string) (map[string]BundleCandidate, error) {
	out := make(map[string]BundleCandidate, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.LockLiveVariantsForBundle(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "could not lock %d variants", len(ids))
	}
	for _, row := range rows {
		out[row.ID] = BundleCandidate{
			ID: row.ID, ProductID: row.ProductID, ManageInventory: row.ManageInventory,
			AllowBackorder: row.AllowBackorder, IsGiftcard: row.IsGiftcard,
		}
	}
	return out, nil
}

// ListBundlesContaining returns, for each given variant, the bundle variants
// that hold it as a component; a variant no bundle holds is absent. A bundle's
// rows go with its deletion, so every bundle returned is live.
func (r *Repo) ListBundlesContaining(ctx context.Context, componentIDs []string) (map[string][]string, error) {
	out := make(map[string][]string)
	if len(componentIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListBundlesContaining(ctx, componentIDs)
	if err != nil {
		return nil, wrapDB(err, "could not read the bundles holding %d variants", len(componentIDs))
	}
	for _, row := range rows {
		out[row.ComponentVariantID] = append(out[row.ComponentVariantID], row.BundleVariantID)
	}
	return out, nil
}
