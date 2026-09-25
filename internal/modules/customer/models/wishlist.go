package models

import "time"

// MaxWishlistItems is how many variants one customer's wishlist holds (ADR 0190).
//
// The cap is what bounds the listing, which is not paged: it is checked under
// the customer row's lock when a variant is saved, so the list never grows past
// it. It is a ceiling on one person's bookmarks, not on the catalog, and a
// variant already saved does not count against it a second time.
const MaxWishlistItems = 200

// CodeWishlistFull is the error code of a save refused because the wishlist
// holds [MaxWishlistItems] variants already. It is here beside the cap rather
// than with the storage's codes because the transport describes it too.
const CodeWishlistFull = "customer_wishlist_full"

// WishlistItem is one variant a customer saved to their wishlist.
//
// The pair of ids is the item's identity; there is no id of its own, because
// saving the same variant twice is the same item. The variant id belongs to the
// product module and is not checked against it when saved: whether the variant
// can still be shown is the catalog's answer when the list is read.
type WishlistItem struct {
	// CustomerID is the customer that saved the variant.
	CustomerID string
	// VariantID is the saved variant.
	VariantID string
	// CreatedAt is when the variant was first saved. Saving it again does not
	// move it.
	CreatedAt time.Time
}
