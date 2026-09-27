package api

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// describeWishlist records the wishlist's endpoints (ADR 0190): the storefront
// lists, saves and removes, marks and unmarks a stock alert (ADR 0215), and the
// operator reads.
func describeWishlist(d *openapi.Doc) {
	const (
		storeList  = "/store/v1/customers/{id}/wishlist"
		storeItem  = storeList + "/{variant_id}"
		storeAlert = storeItem + "/stock-alert"
		adminList  = "/admin/v1/customers/{id}/wishlist"
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

	d.Describe(http.MethodPut, storeAlert, openapi.Operation{
		Summary: "Asks to be told when a wishlist variant is back in stock.",
		Description: storefront +
			"The variant is saved to the wishlist if it is not there, under the same cap, " +
			"and marked. The shopper is mailed ONCE, at the address on their own customer " +
			"record, when the variant is next in stock in the request's sales channels " +
			"after having been seen out of it: a mark set on a variant that is in stock " +
			"waits for it to run out and come back. The mail clears the mark; marking again " +
			"starts the wait again. There is no body (ADR 0215).",
		Responses: answers(full, "200",
			openapi.Response("The marked item", d.Item(wishlistItemDTO{}))),
	})

	d.Describe(http.MethodDelete, storeAlert, openapi.Operation{
		Summary: "Stops waiting for a wishlist variant's stock.",
		Description: storefront +
			"The mark is taken off and the variant stays on the wishlist. It can be " +
			"repeated: an item that is not marked answers the same 204.",
		Responses: answers(storefrontIdentityRefusals(), "204",
			emptyResponse("The variant is not marked")),
	})
}
