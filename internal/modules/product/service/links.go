package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/models"
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
	// EntityCategory is the entity name of the category records.
	//
	// Unlike [EntityImage] it DOES register a provider ("category.query", see
	// [NewCategoryProvider]) and unlike [EntityProduct] it appears on NO link
	// end. That is the honest shape of the thing: a category is a vocabulary
	// entry a consumer reads to turn a word into an id, and the membership
	// between a product and a category is not a link but a table of this
	// module's own (product_category_map, see migrations/000001_product_init.up.sql).
	// Declaring a link for it would move a relation the module owns into the
	// link layer and give two writers to one truth.
	EntityCategory = "category"
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
	// EntityImage is the entity name of the product image records.
	//
	// It has NO Query provider and it is not meant to get one: an image is
	// never a root record, it is always read as part of its product. The name
	// exists so that the end of the upload binding says WHICH of this module's
	// records it lands on — writing [EntityProduct] there would claim that the
	// link table holds product ids, and the reverse read would then join image
	// ids against products and quietly match nothing.
	EntityImage = "image"
	// EntityUpload is the upload entity name of the file module.
	//
	// The module's name is "file" and its record is the upload; the two are
	// separate for the reason the sales channel end is separate above. Unlike
	// the sales channel, though, this name is NOT a promise of a provider: the
	// file module registers no Query provider, and the binding does not need
	// one (see [LinkUploadProductImage]).
	EntityUpload = "upload"
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
	// LinkUploadProductImage binds an upload to the product images made from it.
	//
	// # Why THIS module declares a link whose left side is an upload
	//
	// A definition may be declared only ONCE (ADR 0005), so somebody has to own
	// it, and the rule is that it belongs to the side that WRITES the record the
	// binding carries — the reasoning the fulfillment module wrote down for
	// "order_fulfillment", where the shipment is written by the declaring side.
	// Here the binding is carried by the IMAGE: the image is the record that
	// comes into being knowing which upload it was made from, and the image is
	// this module's row. The upload is written earlier, by a client uploading a
	// file, and the file module cannot know that a product will later point at
	// it — its package doc says so in as many words ("it does not know WHAT the
	// file belongs to"). A module that cannot know a relation exists cannot
	// declare it.
	//
	// # Why the UPLOAD is the From end
	//
	// The direction is not the reading direction, it is where the cardinality
	// bites. The declaration is the strictest one that is TRUE (the rule
	// "order_fulfillment" states): the TO end is unique, so an image binds to AT
	// MOST ONE upload, which is exactly what the single upload_id column can
	// hold; the FROM end is free, so one upload may back MANY images, which it
	// legitimately does when the same file is used by two products. Turning the
	// ends around would have needed ManyToMany — the only cardinality that lets
	// one image be bound to two uploads, a state nothing here can produce and no
	// reader could resolve.
	//
	// # What the link is FOR, given that the column already answers the question
	//
	// The forward question ("which upload is this image") is answered by the
	// image's own upload_id column, in the same row read, with no join and no
	// second service. The link answers the REVERSE one ("which images use this
	// upload"), which is the question somebody has before DELETING a file, and
	// which the column cannot answer for anyone who is not this module: a
	// filter on upload_id would need an index this module has no reader for
	// (migration 000002), and product_image cannot be seen from outside at all.
	//
	// The reverse read has two roads and both are open on purpose. This module
	// reads it for its own admin endpoint ([Service.ImagesOfUpload]), which
	// returns the records; anyone holding core.link — a flow, a plugin, another
	// module — reads the ids straight from the link table, without an endpoint
	// and without importing this package.
	//
	// The two are kept from drifting by the WRITE ORDER, not by hope: the link
	// row is written BEFORE the image row (see [Service.linkImageUploads]). A
	// committed image therefore always has its link row, while a failed write
	// can leave a link row for an image id that never existed — harmless,
	// because ids are never reused, and it is the same residue the repository
	// already accepts for the price and stock links.
	//
	// # It is NOT expandable through Query
	//
	// Query resolves an expansion by looking the far end's entity up under
	// "<Entity>.query" and neither "image" nor "upload" registers a provider.
	// The binding is therefore readable through the link service (List /
	// ListManyByTo) and not through a Query request. Giving uploads a Query
	// provider is a separate step with its own consumer; it is not needed to
	// bind the two records, and a provider with no consumer is a surface that
	// can never be closed again.
	LinkUploadProductImage = "upload_product_image"
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
//
// # The upload link
//
// [LinkUploadProductImage] is the one definition here whose FROM end belongs to
// another module: the upload is the file module's record and the image is ours.
// Why this module declares it all the same, and why the ends are that way round,
// is written out in that constant's own doc.
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
		{
			Name: LinkUploadProductImage,
			// The module name is a literal rather than file.ModuleName: this
			// module cannot import the file module at all (Principle 2.4, and
			// the ban is enforced by depguard). The repetition is the accepted
			// price of isolation, the same one the workflows pay.
			From:        link.LinkSide{Module: "file", Entity: EntityUpload, Field: "upload_id"},
			To:          link.LinkSide{Module: ModuleName, Entity: EntityImage, Field: "image_id"},
			Cardinality: link.OneToMany,
		},
	}
}

// verifyImageUploads checks that the uploads the images point at REALLY EXIST.
//
// # Why it is checked at all
//
// Nothing else can check it. The database cannot: a cross-module foreign key is
// banned (Principle 2.2). The link service cannot: it sees the ends as
// free-form strings and knows no module's schema — the sentence that already
// justifies the product check in [Service.checkProductSalesChannel]. So an
// upload id carrying a typo would be recorded, bound, and returned by every
// read, and the fault would only surface at the far end, in the caller that
// wanted to look at the file and got nothing. The write is the last place where
// the fault is still cheap to report.
//
// # Why an ABSENT file module is not a rejection
//
// A reader that cannot answer (nil body, no error) means the file module is not
// installed in this setup; gobit is a library and its modules are chosen one by
// one (ADR 0025). The id is then recorded UNVERIFIED, because the alternative —
// rejecting every upload id in an installation that stores its files elsewhere
// — would make the field unusable exactly for the people who need it to carry
// a foreign system's id.
//
// # Why a reader ERROR fails the write
//
// If the file module IS installed and its surface answers with an error, this
// returns it with its kind preserved instead of falling back to "unverified".
// The two states must not be confused: "there is no file module" is a setup
// decision, while "the file module is broken" is a fault, and recording an
// unchecked binding during a fault is how a dangling id gets in on the one day
// nobody is watching.
func (s *Service) verifyImageUploads(ctx context.Context, images []models.Image) error {
	if s.uploads == nil {
		return nil
	}

	// The same file may back several images of one product; it is asked about
	// ONCE. Without this, a create carrying eight images of one upload would
	// make eight identical cross-module reads.
	seen := make(map[string]struct{}, len(images))
	// The slice is walked BY INDEX: an image record is a large struct and
	// copying one per iteration for the sake of two fields is a cost the
	// linter counts (gocritic rangeValCopy).
	for i := range images {
		if images[i].UploadID == nil {
			continue
		}
		id := *images[i].UploadID
		if _, done := seen[id]; done {
			continue
		}
		seen[id] = struct{}{}

		body, err := s.uploads.UploadJSON(ctx, id)
		switch {
		case errors.IsNotFound(err):
			return invalid("images[].upload_id does not point at an upload: %s", id)
		case err != nil:
			return errors.Wrap(err, errors.KindOf(err), codeUploadReadFailed,
				"the upload record could not be read (%s); the image was not written", id)
		case len(body) == 0:
			// The file module is not installed; the id stays unverified. It is
			// logged at debug rather than warned: in that setup this is the
			// normal state, not an incident.
			s.log.DebugContext(ctx, "the upload id was recorded without verification",
				"upload_id", id, "reason", "the file module is not installed in this setup")
		}
	}
	return nil
}

// linkImageUploads binds the images ABOUT TO BE WRITTEN to their uploads.
//
// # Why BEFORE the image is written
//
// The image rows go in inside a transaction; the link table belongs to the core
// and cannot join it (ADR 0005). One of the two writes therefore has to go
// first, and the order decides which inconsistency is possible. In this order
// the only possible residue is a link row whose image was never committed:
// harmless, because ids are never reused, so it can never attach itself to a
// later record — the very argument [Service.cleanupVariantLinks] already
// relies on. The reverse order would have produced the opposite residue, an
// image whose binding is missing, and that one is invisible: the image reads
// fine, the reverse question just quietly answers "nobody uses this upload"
// about a file that is on a product page.
//
// A failure here therefore aborts the create BEFORE anything is written, which
// is why the error is returned rather than logged.
func (s *Service) linkImageUploads(ctx context.Context, images []models.Image) error {
	for i := range images {
		if images[i].UploadID == nil {
			continue
		}
		if s.links == nil {
			// The request explicitly asked for a binding this setup cannot
			// make. Writing the image anyway would drop the caller's id from
			// the half of the binding other modules read, and drop it
			// silently.
			return s.linkerMissing()
		}
		if err := s.links.Create(ctx, LinkUploadProductImage, *images[i].UploadID, images[i].ID); err != nil {
			return wrapLink(err, "the %q link could not be created (upload: %s -> image: %s)",
				LinkUploadProductImage, *images[i].UploadID, images[i].ID)
		}
	}
	return nil
}

// ImagesOfUpload returns the product images made from the given upload.
//
// This is the REVERSE direction of the binding and the reason the link exists
// at all: the forward question ("which upload is this image") is answered by the
// image's own column, while this one has to be asked by someone who cannot see
// product_image — an operator, or a flow, deciding whether a file is still in
// use before removing it. Answering it from the column would mean a query
// filtered by upload_id, which this module deliberately does not index (see
// migration 000002); answering it from the link is an indexed primary-key read
// of the core's own table.
//
// # What an EMPTY result means
//
// "No live image uses this upload." It does NOT mean the upload exists: this
// module cannot see the file module's records and does not claim to. An id that
// belongs to no upload at all and an upload nobody uses give the same answer,
// and the caller that needs to tell them apart asks the file module.
//
// The records are read by id, so a binding whose image was never committed —
// the residue [Service.linkImageUploads] accepts — drops out silently instead of
// being reported as a missing record.
func (s *Service) ImagesOfUpload(ctx context.Context, uploadID string) ([]models.Image, error) {
	if _, err := requireID("upload_id", uploadID); err != nil {
		return nil, err
	}
	if s.links == nil {
		return nil, s.linkerMissing()
	}

	imageIDs, err := s.links.List(ctx, LinkUploadProductImage, uploadID)
	if err != nil {
		return nil, wrapLink(err, "the %q link could not be read (upload: %s)",
			LinkUploadProductImage, uploadID)
	}
	if len(imageIDs) == 0 {
		return []models.Image{}, nil
	}

	return s.repo.ListImagesByIDs(ctx, imageIDs)
}

// imagesForLinkCleanup reads the product's images so that their bindings can be
// removed after the deletion.
//
// It returns EMPTY without touching the database when there is no link service:
// in that setup no binding can have been written either (see
// [Service.linkImageUploads]), so the read would only pay for a cleanup with
// nothing to clean.
//
// An error IS returned rather than logged: the read happens before the deletion,
// where the caller can still be told that nothing happened. Deleting first and
// discovering afterwards that the bindings cannot be read would leave links to
// a product that no longer exists, with nobody to notice.
func (s *Service) imagesForLinkCleanup(ctx context.Context, productID string) ([]models.Image, error) {
	if s.links == nil {
		return nil, nil
	}
	byProduct, err := s.repo.ListImagesByProductIDs(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	return byProduct[productID], nil
}

// cleanupImageUploadLinks removes the upload bindings of a deleted product's
// images.
//
// It does NOT return an error, it logs a warning; the rationale is the one in
// [Service.cleanupVariantLinks] — the deletion has already been committed and
// an error here would tell the caller "the product was not deleted", which is
// false. The cleanup is done all the same: the reverse read exists so that a
// cleanup can ask whether an upload is still in use, and an image that no
// storefront shows any more must not keep answering "yes".
func (s *Service) cleanupImageUploadLinks(ctx context.Context, images []models.Image) {
	if s.links == nil {
		return
	}
	for i := range images {
		if images[i].UploadID == nil {
			continue
		}
		if err := s.links.Delete(ctx, LinkUploadProductImage, *images[i].UploadID, images[i].ID); err != nil {
			s.log.WarnContext(ctx, "the upload link of the deleted image could not be cleaned up",
				"link", LinkUploadProductImage, "image_id", images[i].ID,
				"upload_id", *images[i].UploadID, "error", err)
		}
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
