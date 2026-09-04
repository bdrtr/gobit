package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// The entity names offered to the Query layer.
//
// The providers are registered in the container under the name "<entity>.query"
// (ADR 0004) and THE ENDS OF THE LINK DEFINITIONS are written with these names
// too — because when Query resolves a link it reads the module field of the
// far end as the ENTITY NAME and looks the provider up under that name (see
// core/query targetSide + provider).
const (
	// EntityProduct is the entity name of the product records.
	// ModuleName is the name of this module; it is used on the link ends to
	// declare the owner.
	ModuleName    = "product"
	EntityProduct = "product"
	// EntityVariant is the entity name of the variant records.
	EntityVariant = "variant"
	// EntitySalesChannel is the sales channel entity name of the auth module.
	//
	// The module's name is "auth", its entity is "sales_channel": auth registers
	// its provider under exactly this name (auth.ProviderName = service.Entity +
	// query.ProviderSuffix) and this is also the name that has to be written on
	// the To end of the link. Writing "auth" here would mean errors.NotFound at
	// runtime; for the full rationale see the block comment above and the
	// [Definitions] godoc.
	//
	// That the name in the two packages coincides is pinned by a test inside
	// internal/arch; this package CANNOT import auth (Principle 2.4, ADR 0001).
	EntitySalesChannel = "sales_channel"
)

// Link names. These names are a CROSS-MODULE CONTRACT: the pricing and
// inventory modules use exactly these names when they link to a variant or read
// in the reverse direction. Changing them produces errors.Conflict at startup
// because it would clash with the registered definition (see link.Define).
const (
	// LinkVariantPriceSet links the variant to the price set in the pricing module.
	LinkVariantPriceSet = "product_variant_price_set"
	// LinkVariantInventory links the variant to the stock item in the inventory
	// module.
	LinkVariantInventory = "product_variant_inventory"
	// LinkProductSalesChannel links the product to the sales channel in the auth
	// module.
	//
	// This link is at the PRODUCT level (not the variant): which storefronts a
	// product is sold in does not change from variant to variant.
	LinkProductSalesChannel = "product_sales_channel"
)

// Definitions are the link definitions the product module declares.
//
// # Why the "module" field on the ends is an entity name
//
// The product side of the link points not at the product but at the VARIANT:
// price and stock are per variant. When the Query layer resolves an expansion it
// reads the Module field on the link's ends as the entity name and looks the
// provider up under the name "<Module>.query"; it also matches the records over
// the "id" field of that provider.
//
// That is why the From end is not "product" but "variant": the link table holds
// variant ids as from_id and the root of the expansion is the variant provider
// as well. Had "product" been written, Query would ask the PRODUCT provider for
// a link table full of variant ids and no record would match — it would return
// empty prices/stock without an error, which is the most expensive kind of bug
// to find. The field name (variant_id) documents the ownership on top of that.
//
// # The sales channel link
//
// [LinkProductSalesChannel] is the PRODUCT-level instance of the same rule: on
// the To end it is [EntitySalesChannel] that is written, not the module name
// ("auth") — the very same rationale applies, and it is not repeated.
//
// Its cardinality is ManyToMany, and that is the real relationship itself: a
// product can be sold in several storefronts, and a storefront holds thousands
// of products. Choosing a narrower cardinality (OneToMany) would turn adding a
// second product to a channel into a conflict.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkVariantPriceSet,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityVariant, Field: "variant_id"},
			To:          link.LinkSide{Module: "pricing", Entity: "price_set", Field: "price_set_id"},
			Cardinality: link.OneToOne,
		},
		{
			Name:        LinkVariantInventory,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityVariant, Field: "variant_id"},
			To:          link.LinkSide{Module: "inventory", Entity: "inventory_item", Field: "inventory_item_id"},
			Cardinality: link.OneToOne,
		},
		{
			Name:        LinkProductSalesChannel,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityProduct, Field: "product_id"},
			To:          link.LinkSide{Module: EntitySalesChannel, Entity: EntitySalesChannel, Field: "sales_channel_id"},
			Cardinality: link.ManyToMany,
		},
	}
}

// VariantLinks are a variant's counterparts in other modules.
//
// The fields are ids ONLY: the price itself is pricing's data, the stock level
// is inventory's, and this module does not interpret them.
type VariantLinks struct {
	PriceSetID      *string `json:"price_set_id,omitempty"`
	InventoryItemID *string `json:"inventory_item_id,omitempty"`
}

// SetVariantPriceSet links the variant to a price set.
//
// The link is SINGULAR (OneToOne): if the variant is linked to another set, the
// previous link is removed first. Otherwise the link service would return
// Conflict because of the cardinality violation and a "change the price"
// request would look like an error. If the request falls over with a conflict,
// the variant stays linked to its OLD set (see setVariantLink).
func (s *Service) SetVariantPriceSet(ctx context.Context, variantID, priceSetID string) error {
	return s.setVariantLink(ctx, LinkVariantPriceSet, variantID, priceSetID, "price_set_id")
}

// ClearVariantPriceSet removes the variant's price set link.
func (s *Service) ClearVariantPriceSet(ctx context.Context, variantID string) error {
	return s.clearVariantLink(ctx, LinkVariantPriceSet, variantID)
}

// SetVariantInventoryItem links the variant to a stock item.
func (s *Service) SetVariantInventoryItem(ctx context.Context, variantID, itemID string) error {
	return s.setVariantLink(ctx, LinkVariantInventory, variantID, itemID, "inventory_item_id")
}

// ClearVariantInventoryItem removes the variant's stock item link.
func (s *Service) ClearVariantInventoryItem(ctx context.Context, variantID string) error {
	return s.clearVariantLink(ctx, LinkVariantInventory, variantID)
}

// VariantLinkIDs returns the ids of the price set and the stock item the
// variant is linked to.
func (s *Service) VariantLinkIDs(ctx context.Context, variantID string) (VariantLinks, error) {
	if _, err := requireID("variant_id", variantID); err != nil {
		return VariantLinks{}, err
	}
	if s.links == nil {
		return VariantLinks{}, s.linkerMissing()
	}
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return VariantLinks{}, err
	}

	priceSetID, err := s.firstLink(ctx, LinkVariantPriceSet, variantID)
	if err != nil {
		return VariantLinks{}, err
	}
	itemID, err := s.firstLink(ctx, LinkVariantInventory, variantID)
	if err != nil {
		return VariantLinks{}, err
	}
	return VariantLinks{PriceSetID: priceSetID, InventoryItemID: itemID}, nil
}

// AddProductSalesChannel links the product to a sales channel.
//
// The link is MANY TO MANY: the previous links are not removed, the new one is
// added (the "delete first, then write" pattern of the price/stock links would
// be WRONG here — the moment the product was added to a second channel it would
// drop out of the first).
//
// The call is idempotent: if the same pair is linked a second time the link
// service performs a no-op (see core/link LinkService.Create).
//
// Whether the channel really exists is NOT VERIFIED HERE and cannot be: the
// sales channel is the auth module's data and this module cannot import it
// (Principle 2.4). A link made to a channel that does not exist is harmless — no
// request's channel list contains it, so the product shows up in no storefront
// because of that link.
func (s *Service) AddProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	if err := s.checkProductSalesChannel(ctx, productID, salesChannelID); err != nil {
		return err
	}
	if err := s.links.Create(ctx, LinkProductSalesChannel, productID, salesChannelID); err != nil {
		return wrapLink(err, "the %q link could not be created (product: %s -> channel: %s)",
			LinkProductSalesChannel, productID, salesChannelID)
	}
	return nil
}

// RemoveProductSalesChannel removes the product's link with a sales channel.
//
// If the link is not there already the call is a no-op (the core/link contract):
// "absent" is the desired outcome itself, and a retried removal request
// returning an error would mislead the client.
//
// CAREFUL: a product whose last channel link is removed is not hidden, it
// becomes visible in ALL channels — that is the direct consequence of the rule
// "a product with no assignment is visible everywhere" (see
// [Service.ListStoreProducts]).
func (s *Service) RemoveProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	if err := s.checkProductSalesChannel(ctx, productID, salesChannelID); err != nil {
		return err
	}
	if err := s.links.Delete(ctx, LinkProductSalesChannel, productID, salesChannelID); err != nil {
		return wrapLink(err, "the %q link could not be removed (product: %s -> channel: %s)",
			LinkProductSalesChannel, productID, salesChannelID)
	}
	return nil
}

// ProductSalesChannelIDs returns the ids of the sales channels the product is
// linked to.
//
// An empty result means "no channels" and that does NOT mean the product is
// visible NOWHERE: a product with no assignment is visible in all channels.
func (s *Service) ProductSalesChannelIDs(ctx context.Context, productID string) ([]string, error) {
	if _, err := requireID("id", productID); err != nil {
		return nil, err
	}
	if s.links == nil {
		return nil, s.linkerMissing()
	}
	if _, err := s.repo.GetProduct(ctx, productID); err != nil {
		return nil, err
	}

	ids, err := s.links.List(ctx, LinkProductSalesChannel, productID)
	if err != nil {
		return nil, wrapLink(err, "the %q link could not be read (product: %s)", LinkProductSalesChannel, productID)
	}
	return ids, nil
}

// checkProductSalesChannel is the shared pre-check of the sales channel link
// ends.
//
// That the product really exists is verified HERE: the link service sees the
// ids as free-form strings and knows no module's schema, that is, a product id
// carrying a typo would be linked silently and that link would never show up in
// any query.
func (s *Service) checkProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	if _, err := requireID("id", productID); err != nil {
		return err
	}
	if _, err := requireID("sales_channel_id", salesChannelID); err != nil {
		return err
	}
	if s.links == nil {
		return s.linkerMissing()
	}
	_, err := s.repo.GetProduct(ctx, productID)
	return err
}

// cleanupProductSalesChannels cleans up the sales channel links of a deleted
// product.
//
// It does NOT return an error, it logs a warning; the rationale is the same as
// [Service.cleanupVariantLinks]. The cleanup is done all the same: if the link
// remained, a read in the reverse direction on the auth side (which products
// are in this channel) would land on a deleted product.
func (s *Service) cleanupProductSalesChannels(ctx context.Context, productID string) {
	if s.links == nil {
		return
	}
	existing, err := s.links.List(ctx, LinkProductSalesChannel, productID)
	if err != nil {
		s.log.WarnContext(ctx, "the sales channel links of the deleted product could not be read",
			"link", LinkProductSalesChannel, "product_id", productID, "error", err)
		return
	}
	for _, channelID := range existing {
		if err := s.links.Delete(ctx, LinkProductSalesChannel, productID, channelID); err != nil {
			s.log.WarnContext(ctx, "a sales channel link of the deleted product could not be cleaned up",
				"link", LinkProductSalesChannel, "product_id", productID,
				"sales_channel_id", channelID, "error", err)
		}
	}
}

// setVariantLink links the variant to the target record over the given link.
//
// # Order and compensation
//
// Under a OneToOne cardinality BOTH ends are unique. The variant's (the FROM
// end) new link cannot be created before the old one is removed; that is why it
// is deleted first. But the target (the TO end) being linked to another variant
// is a violation too and Create returns Conflict in that case — and because the
// delete has already happened, the variant would be left without a price/stock.
// Since the request returns 409 the operator reads "nothing changed", whereas
// the storefront publishes that variant without a price: silent data loss.
//
// That is why, if Create fails, the removed links are RESTORED and the error is
// returned; a failed request does not break the variant's existing link. If the
// compensation fails too (another request grabbed the target in between) the
// situation is logged as a warning — shadowing the original error would show
// the caller the wrong cause.
func (s *Service) setVariantLink(ctx context.Context, name, variantID, toID, field string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}
	if _, err := requireID(field, toID); err != nil {
		return err
	}
	if s.links == nil {
		return s.linkerMissing()
	}
	// That the variant really exists is verified HERE: the link service sees the
	// ids as free-form strings and knows no module's schema, that is, an id
	// carrying a typo would be linked silently.
	if _, err := s.repo.GetVariant(ctx, variantID); err != nil {
		return err
	}

	existing, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return wrapLink(err, "the %q link could not be read (variant: %s)", name, variantID)
	}
	removed := make([]string, 0, len(existing))
	for _, current := range existing {
		if current == toID {
			continue
		}
		if err := s.links.Delete(ctx, name, variantID, current); err != nil {
			s.restoreVariantLinks(ctx, name, variantID, removed)
			return wrapLink(err, "the previous %q link could not be removed (variant: %s)", name, variantID)
		}
		removed = append(removed, current)
	}

	if err := s.links.Create(ctx, name, variantID, toID); err != nil {
		s.restoreVariantLinks(ctx, name, variantID, removed)
		return wrapLink(err, "the %q link could not be created (variant: %s -> %s)", name, variantID, toID)
	}
	return nil
}

// restoreVariantLinks restores the links removed during a failed relink.
//
// It does NOT return an error: the caller will return the original error
// anyway, and if the compensation's own error shadowed it the client would see
// a meaningless cause instead of a fixable conflict. A link that cannot be
// restored is logged as a warning.
func (s *Service) restoreVariantLinks(ctx context.Context, name, variantID string, removed []string) {
	for _, toID := range removed {
		if err := s.links.Create(ctx, name, variantID, toID); err != nil {
			s.log.WarnContext(ctx, "the previous link could not be restored after a failed relink",
				"link", name, "variant_id", variantID, "to_id", toID, "error", err)
		}
	}
}

// clearVariantLink removes all of the variant's links under the given link name.
func (s *Service) clearVariantLink(ctx context.Context, name, variantID string) error {
	if _, err := requireID("variant_id", variantID); err != nil {
		return err
	}
	if s.links == nil {
		return s.linkerMissing()
	}

	existing, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return wrapLink(err, "the %q link could not be read (variant: %s)", name, variantID)
	}
	for _, current := range existing {
		if err := s.links.Delete(ctx, name, variantID, current); err != nil {
			return wrapLink(err, "the %q link could not be removed (variant: %s)", name, variantID)
		}
	}
	return nil
}

// firstLink returns the variant's first link under the given link name; nil if
// there is none.
func (s *Service) firstLink(ctx context.Context, name, variantID string) (*string, error) {
	ids, err := s.links.List(ctx, name, variantID)
	if err != nil {
		return nil, wrapLink(err, "the %q link could not be read (variant: %s)", name, variantID)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// cleanupVariantLinks cleans up the price/stock links of a deleted variant.
//
// It does NOT return an error, it logs a warning: the delete has already
// completed and returning an error to the caller would give the impression that
// "the product was not deleted". A link that cannot be cleaned up is harmless —
// because ids are never reused, the orphan row cannot attach itself to another
// record; it is logged all the same so that it stays visible.
func (s *Service) cleanupVariantLinks(ctx context.Context, variantID string) {
	if s.links == nil {
		return
	}
	for _, name := range []string{LinkVariantPriceSet, LinkVariantInventory} {
		if err := s.clearVariantLink(ctx, name, variantID); err != nil {
			s.log.WarnContext(ctx, "a link of the deleted variant could not be cleaned up",
				"link", name, "variant_id", variantID, "error", err)
		}
	}
}

// linkerMissing is the typed error of a service built without a link service.
func (s *Service) linkerMissing() error {
	return errors.Unavailable(codeNotReady,
		"the link service is not registered; price/stock links cannot be managed in this setup")
}

// wrapLink wraps the error coming from the link service PRESERVING ITS KIND.
//
// Preserving the kind is essential: a cardinality violation has to stay a
// Conflict (409) and an undefined link name a NotFound (404); turning them all
// into Internal would show the client a fixable error as a server error.
func wrapLink(err error, format string, a ...any) error {
	return errors.Wrap(err, errors.KindOf(err), codeLinkFailed, format, a...)
}
