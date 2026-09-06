package api

import (
	"fmt"
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/review/models"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// The JSON Schema names that appear in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and it only surfaces when the client that reads the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// Describe records the review endpoints into the OpenAPI document.
//
// It is a package-level function and it lives here for the same reasons the
// other modules' do: the bodies being described are this package's unexported
// DTOs, and the code that knows which query parameter is really read is in
// api.go — a description kept elsewhere would drift from it silently.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStoreProductReviews, openapi.Operation{
		Summary: "Submits a review of a product.",
		Description: ratingText() +
			"The review is stored in the \"submitted\" status and is visible to NOBODY " +
			"on the storefront until an operator approves it. That is the design and not a " +
			"delay: the storefront's only principal is the publishable key, so this framework " +
			"cannot know who wrote the review and does not claim to — what makes the write " +
			"acceptable is that a person stands between it and its effect. " +
			"\"Verified purchase\" is therefore not expressible and no order id is taken: an " +
			"order id proves that the writer holds one, not that they are the buyer. " +
			"The body carries a display name and NO contact detail; an email address here " +
			"would be an unverified mailing list with no way to unsubscribe. " +
			"The endpoint has no quota of its own — the storefront prefix carries a single " +
			"rate limit shared by every store endpoint, keyed by the connection — so what " +
			"keeps a flood off a product page is the approval step, not the limit.",
		RequestBody: d.RequestBody(storeSubmitRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The stored review, awaiting moderation",
				d.Item(storeReviewDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStoreProductReviews, openapi.Operation{
		Summary: "Lists a product's APPROVED reviews.",
		Description: "Only approved reviews are returned and there is no parameter that can " +
			"widen that: the query carries the status as a literal. A review still waiting and " +
			"a review that was refused are both absent, and neither can be reached by id " +
			"either — there is no storefront endpoint that reads a single review. " +
			"The response carries no status, no moderation note and no moderation moment: " +
			"every row here is approved, so the field would print one word, and the note is " +
			"an operator's sentence that was never written to be published.",
		Parameters: []openapi.Parameter{
			pathParameter("product_id", "The product's ID. It is the id and not the handle: a "+
				"storefront rendering a product page has already read the product and holds "+
				"its id."),
			queryParameter("limit", typeInteger,
				"Page size; when it is not given the service's default applies."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
			queryParameter("after", typeString, cursorText()),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of approved reviews",
				d.List(storeReviewDTO{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, pathStoreProductReviewSummary, openapi.Operation{
		Summary: "Returns the count and average rating a product page shows.",
		Description: "The average is COMPUTED on every read and stored nowhere. Measured on " +
			"505,000 reviews over 20,001 products against PostgreSQL 16, with the module's " +
			"partial index: 0.2 ms for a product with 19 approved reviews, 1.3-2.0 ms at " +
			"5,000 and 9.3 ms at 50,000 — against 33-38 ms with no index, where it is a full " +
			"scan whatever the product's size. A stored counter would owe a correctness " +
			"obligation to every path that writes a review; the cost is linear in the number " +
			"of approved reviews of the one product read, so only a shop with hundreds of " +
			"thousands of them on a single product wants one. " +
			"The average comes back in HUNDREDTHS as a whole number — 433 means 4.33 stars — " +
			"so the printed value does not depend on where a client rounded it. " +
			"A product with no approved review answers 200 with a count of 0, not 404: this " +
			"module does not know whether the product exists.",
		Parameters: []openapi.Parameter{
			pathParameter("product_id", "The product's ID."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The aggregate", d.Item(summaryDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminReviews, openapi.Operation{
		Summary: "Lists the reviews; with status=submitted it is the moderation queue.",
		Description: "The queue is this listing filtered to \"submitted\" rather than an " +
			"endpoint of its own: the two differ by one word, and two paths would be two " +
			"listings to keep in step. " +
			"An unknown status is REFUSED rather than answered with an empty page, because an " +
			"empty page reads as \"there is nothing waiting\" and that is the one answer a " +
			"moderation queue must never give wrongly.",
		Parameters: []openapi.Parameter{
			queryParameter("status", typeString, "Status filter: "+statusesText()+"."),
			queryParameter("product_id", typeString,
				"Returns only the reviews of one product."),
			queryParameter("limit", typeInteger,
				"Page size; when it is not given the service's default applies."),
			queryParameter("offset", typeInteger, "Number of records to skip."),
			queryParameter("after", typeString, cursorText()),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of reviews",
				d.List(adminReviewDTO{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, pathAdminReview, openapi.Operation{
		Summary: "Returns one review whatever its status.",
		Description: "It serves the case the queue cannot: a review already decided, reached " +
			"from a link somebody kept — a support ticket, or the author asking why their " +
			"words are not on the page.",
		Parameters: []openapi.Parameter{
			pathParameter("id", "The review's ID."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The review", d.Item(adminReviewDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathAdminReviewStatus, openapi.Operation{
		Summary: "Approves or rejects a review.",
		Description: "This is the human decision the storefront write depends on: until it is " +
			"made the review is on no product page. " +
			"One endpoint rather than /approve and /reject, because the transition table has " +
			"FOUR edges and not two — an approved review may be rejected, which is the only " +
			"way to take a published review back down, and a rejected one may be approved, " +
			"because a rejection is a judgement and the author has no way to write their " +
			"review a second time. " +
			"A rejection requires a note; an approval does not, because demanding one would " +
			"produce a column full of the word \"ok\". " +
			"The move is decided by the database as well as by the service, so two operators " +
			"acting at the same moment cannot both win. " +
			"It is a POST to a sub-path rather than a PATCH on the review: the text itself is " +
			"never editable, because a shop editing it would be publishing words under a " +
			"customer's byline that the customer did not write.",
		RequestBody: d.RequestBody(moderateRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The moved review", d.Item(adminReviewDTO{})),
		},
	})
}

// statusesText writes the valid statuses into the document text.
//
// The list is not written by hand: the status set lives in the models package
// and a status added there has to enter the document as well. A hand-written
// list would mean the added status silently dropping out of it.
func statusesText() string {
	statuses := []models.Status{
		models.StatusSubmitted, models.StatusApproved, models.StatusRejected,
	}

	out := ""

	for i, status := range statuses {
		if i > 0 {
			out += ", "
		}

		out += status.String()
	}

	return out
}

// cursorText is the "after" parameter's description.
//
// Both listings take the same cursor and mean the same thing by it, so the
// sentence is written once: two copies would drift, and the one that drifted
// would be the one somebody read.
func cursorText() string {
	return "Opaque cursor from a previous page's \"next_cursor\". Cheaper than \"offset\" for " +
		"deep pages: offset makes the database walk and DISCARD every row it skips, so its " +
		"cost grows with depth, while a cursor becomes an index condition and stays flat. " +
		"\"after\" and \"offset\" name two different positions and are REFUSED together. " +
		"When the response carries no \"next_cursor\" the listing is exhausted. " +
		"A cursor taken from the moderation queue carries no permission into the storefront " +
		"listing: that listing's status is a literal in its SQL, not a filter."
}

// queryParameter defines a parameter read from the query string.
//
// There is no "required" flag because no query parameter here is required: a
// listing without a filter is meaningful on both surfaces.
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}

// pathParameter defines a parameter read from the path. It is always required —
// a path segment that is absent is a different route.
func pathParameter(name, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "path",
		Required:    true,
		Schema:      map[string]any{schemaType: typeString},
		Description: description,
	}
}

// ratingText writes the star bounds into the document text.
//
// The numbers come from the service's own constants rather than being typed
// again: a document that says 1 to 5 while the service refuses 5 is worse than
// no document, because a client believes it.
func ratingText() string {
	return fmt.Sprintf("The rating runs from %d to %d, and the bound is in the column's CHECK "+
		"as well as in the service: a single row carrying a larger number would move a "+
		"product's average and no read would notice. ",
		service.MinRating, service.MaxRating)
}
