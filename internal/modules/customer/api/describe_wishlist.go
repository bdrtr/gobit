package api

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// describeWishlist records the wishlist's four endpoints (ADR 0190): the
// storefront lists, saves and removes, and the operator reads.
func describeWishlist(d *openapi.Doc) {
	const (
		storeList = "/store/v1/customers/{id}/wishlist"
		storeItem = storeList + "/{variant_id}"
		adminList = "/admin/v1/customers/{id}/wishlist"
	)

	listing := fmt.Sprintf("The listing is not paged: a wishlist holds at most %d variants, "+
		"a cap checked when one is saved, so \"count\" is the whole of it. It is newest "+
		"first. It holds variant ids only; what each one is, and whether the shop still "+
		"shows it, is the catalog's answer.", models.MaxWishlistItems)

	d.Describe(http.MethodGet, adminList, openapi.Operation{
		Summary: "Lists a customer's wishlist.",
		Description: "The operator's read of any customer's wishlist, reached with an admin " +
			"token. " + listing,
		Responses: map[string]any{
			"200": openapi.Response("The customer's wishlist", d.List(wishlistItemDTO{})),
		},
	})

	storefront := "This is the STOREFRONT surface: the customer named in the path must be " +
		"the customer the installation's identity proves. "

	d.Describe(http.MethodGet, storeList, openapi.Operation{
		Summary:     "Lists the shopper's wishlist.",
		Description: storefront + listing,
		Responses: answers(storefrontIdentityRefusals(), "200",
			openapi.Response("The shopper's wishlist", d.List(wishlistItemDTO{}))),
	})

	full := storefrontIdentityRefusals()
	full["409"] = openapi.ErrorResponse(fmt.Sprintf(
		"The wishlist already holds %d variants and this one is not among them: code "+
			"%q. Removing one makes room.", models.MaxWishlistItems, models.CodeWishlistFull))

	d.Describe(http.MethodPut, storeItem, openapi.Operation{
		Summary: "Saves a variant to the shopper's wishlist.",
		Description: storefront +
			"It can be repeated: a variant already saved comes back as it is, with the " +
			"moment it was first saved. There is no body; the path names the variant." +
			"\n\n" +
			"The variant id is checked for its form only, because the catalog owns it. A " +
			"variant that does not exist is saved and never shown.",
		Responses: answers(full, "200",
			openapi.Response("The saved item", d.Item(wishlistItemDTO{}))),
	})

	d.Describe(http.MethodDelete, storeItem, openapi.Operation{
		Summary: "Removes a variant from the shopper's wishlist.",
		Description: storefront +
			"It can be repeated: a variant that is not on the list leaves the list as " +
			"asked, and the answer is the same 204.",
		Responses: answers(storefrontIdentityRefusals(), "204",
			emptyResponse("The variant is not on the wishlist")),
	})
}
