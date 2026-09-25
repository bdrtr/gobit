package service

import (
	"context"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// MaxRelations is the longest list one kind of relation holds for a product.
//
// A product page shows a handful of neighbors; the bound keeps the storefront
// read — which enriches every one of them with prices and stock — to one page's
// worth, and it is refused rather than cut when exceeded.
const MaxRelations = 50

// CodeRelationTypeUnknown reports a kind outside the closed set.
const CodeRelationTypeUnknown = "product_relation_type_unknown"

// ProductRelations returns a product's relations, every kind present and each in
// the operator's order (ADR 0180).
func (s *Service) ProductRelations(ctx context.Context, id string) (map[models.RelationType][]string, error) {
	if _, err := requireID("id", id); err != nil {
		return nil, err
	}
	if _, err := s.GetProduct(ctx, id); err != nil {
		return nil, err
	}

	stored, err := s.repo.ListProductRelations(ctx, id)
	if err != nil {
		return nil, err
	}

	// Every kind is present, an empty list included: a client asking "what are
	// the up-sells" should not have to tell a missing key from an empty list.
	out := make(map[models.RelationType][]string, len(models.RelationTypes()))
	for _, kind := range models.RelationTypes() {
		ids := stored[kind]
		if ids == nil {
			ids = []string{}
		}
		out[kind] = ids
	}

	return out, nil
}

// SetProductRelations replaces one kind of a product's relations with the given
// ids, in that order (ADR 0180), and returns all of the product's relations.
//
// Every id has to name a product that exists and is not deleted; one that does
// not is refused, naming it, rather than dropped — a list the operator typed and
// the store quietly shortened is a list they cannot trust. The related products
// are NOT required to be published or in any channel: that is decided when the
// storefront reads the list, so a draft can be lined up before it launches.
func (s *Service) SetProductRelations(
	ctx context.Context, id string, kind models.RelationType, relatedIDs []string,
) (map[models.RelationType][]string, error) {
	if _, err := requireID("id", id); err != nil {
		return nil, err
	}
	if !kind.Valid() {
		return nil, relationTypeUnknown(kind)
	}
	wanted, err := uniqueIDs("product_ids", relatedIDs)
	if err != nil {
		return nil, err
	}
	if len(wanted) != len(relatedIDs) {
		return nil, invalid("product_ids names a product more than once")
	}
	if len(wanted) > MaxRelations {
		return nil, invalid("a product holds at most %d %s relations, %d given", MaxRelations, kind, len(wanted))
	}
	if slices.Contains(wanted, id) {
		return nil, invalid("a product cannot be related to itself: %s", id)
	}

	if _, err := s.GetProduct(ctx, id); err != nil {
		return nil, err
	}
	if len(wanted) > 0 {
		found, err := s.repo.ListProductsByIDs(ctx, wanted)
		if err != nil {
			return nil, err
		}
		known := make(map[string]struct{}, len(found))
		for i := range found {
			known[found[i].ID] = struct{}{}
		}
		var missing []string
		for _, related := range wanted {
			if _, ok := known[related]; !ok {
				missing = append(missing, related)
			}
		}
		if len(missing) > 0 {
			return nil, invalid("no such product: %s", strings.Join(missing, ", "))
		}
	}

	if err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		return tx.ReplaceProductRelations(ctx, id, kind, wanted)
	}); err != nil {
		return nil, err
	}

	return s.ProductRelations(ctx, id)
}

// StoreRelatedProducts returns one kind of a product's relations as the
// storefront shows them (ADR 0180).
//
// The product it starts from has to be one the storefront may show — the single
// endpoint's rule, [Service.visibleStoreProduct] — or the answer is NotFound, so
// a relation list cannot be read off a draft or another channel's product. The
// related products then go through [Service.StoreProductsByIDs], the rule search
// uses: published, visible in the request's channels, in the operator's order,
// and the ones that are not are skipped without saying so.
func (s *Service) StoreRelatedProducts(
	ctx context.Context, idOrHandle string, kind models.RelationType, salesChannelIDs []string,
) ([]StoreProduct, error) {
	if !kind.Valid() {
		return nil, relationTypeUnknown(kind)
	}
	product, err := s.visibleStoreProduct(ctx, idOrHandle, salesChannelIDs)
	if err != nil {
		return nil, err
	}

	ids, err := s.repo.ListProductRelationsOfType(ctx, product.ID, kind)
	if err != nil {
		return nil, err
	}

	return s.StoreProductsByIDs(ctx, ids, salesChannelIDs)
}

// relationTypeUnknown is the refusal for a kind outside the closed set.
func relationTypeUnknown(kind models.RelationType) error {
	return errors.Invalid(CodeRelationTypeUnknown,
		"%q is not a relation type (expected: %s, %s or %s)",
		kind, models.RelationCrossSell, models.RelationUpSell, models.RelationSubstitute)
}
