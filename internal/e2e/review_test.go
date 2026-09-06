//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves the review module's one claim over HTTP:
//
//	A review nobody approved is INVISIBLE on the storefront.
//
// # Why the module's own tests are not enough
//
// They cover the SQL and the rules; what they cannot cover is the surface a
// stranger actually reaches. The storefront request here goes through the
// production guard stack — the publishable-key check, the single rate limit the
// prefix carries, the idempotency ring — assembled by the same
// corehttp.APIGuards call cmd/server makes, and the moderation goes through the
// admin scope check. A module whose service filtered correctly while its route
// was mounted on the wrong prefix, or whose admin endpoint was left unscoped,
// would pass every test in its own package and fail here.
//
// # Why the subject is a REAL product
//
// The review module deliberately does not validate the product identifier
// (Principle 2.2: it belongs to another module), so a made-up string would work
// just as well and would prove less. Using the id of a product the storefront
// can actually fetch is what makes the scenario the one a shop runs: read the
// product page, read its reviews, write one.

// reviewFixtureProduct creates a published product and returns its id.
//
// It is a product rather than the variant [newVariant] returns, because a review
// is ABOUT a product: the storefront can address one and cannot address a
// variant at all.
func reviewFixtureProduct(ctx context.Context, t *testing.T, handle string) string {
	t.Helper()

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: handle,
		Title:  "E2E Review Subject",
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err, "could not create the product the review is about")

	return product.ID
}

// submitReview posts a review through the storefront and returns its id.
func submitReview(t *testing.T, productID string, rating int, body string) string {
	t.Helper()

	recorder := storefrontRequest(t, http.MethodPost,
		"/store/v1/products/"+productID+"/reviews",
		fmt.Sprintf(`{"rating":%d,"title":"a title","body":%q,"author_name":"A customer"}`,
			rating, body))
	require.Equal(t, http.StatusCreated, recorder.Code,
		"the storefront must accept the review; body: %s", recorder.Body.String())

	id, ok := storefrontData(t, recorder)["id"].(string)
	require.True(t, ok, "the stored review must carry an identity; body: %s", recorder.Body.String())

	return id
}

// storefrontReviewIDs returns the ids the storefront listing shows.
func storefrontReviewIDs(t *testing.T, productID string) []string {
	t.Helper()

	recorder := storefrontRequest(t, http.MethodGet,
		"/store/v1/products/"+productID+"/reviews", "")
	require.Equal(t, http.StatusOK, recorder.Code,
		"the storefront listing must answer; body: %s", recorder.Body.String())

	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Count int64 `json:"count"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"could not decode the listing; body: %s", recorder.Body.String())

	ids := make([]string, 0, len(envelope.Data))
	for i := range envelope.Data {
		ids = append(ids, envelope.Data[i].ID)
	}

	require.Equal(t, int64(len(ids)), envelope.Count,
		"the count and the page have to agree on this fixture's small numbers")

	return ids
}

// storefrontSummary returns the count and the average the product page shows.
func storefrontSummary(t *testing.T, productID string) (count, averageHundredths int64) {
	t.Helper()

	recorder := storefrontRequest(t, http.MethodGet,
		"/store/v1/products/"+productID+"/review-summary", "")
	require.Equal(t, http.StatusOK, recorder.Code,
		"the summary must answer; body: %s", recorder.Body.String())

	var envelope struct {
		Data struct {
			Count             int64 `json:"count"`
			AverageHundredths int64 `json:"average_hundredths"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"could not decode the summary; body: %s", recorder.Body.String())

	return envelope.Data.Count, envelope.Data.AverageHundredths
}

// moderateReview makes the operator's decision through the admin endpoint.
func moderateReview(t *testing.T, reviewID, status, note string) *httptest.ResponseRecorder {
	t.Helper()

	recorder, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/reviews/"+reviewID+"/status",
		map[string]any{"status": status, "note": note})
	require.NoError(t, err, "the moderation request could not be made")

	return recorder
}

// TestAnUnapprovedReviewIsInvisibleOnTheStorefront is the assertion the whole
// module is designed around.
//
// It is the reason decision A15 in docs/gaps.md is answered for reviews at all:
// the storefront write is accepted from a party this framework cannot identify,
// and what makes that acceptable is that a person stands between the write and
// its effect. If this test ever goes red, that sentence stops being true and the
// module is publishing anonymous text on a shop's product page.
func TestAnUnapprovedReviewIsInvisibleOnTheStorefront(t *testing.T) {
	ctx := t.Context()
	productID := reviewFixtureProduct(ctx, t, "e2e-review-invisible")

	reviewID := submitReview(t, productID, 1, "this is not on the page yet")

	assert.Empty(t, storefrontReviewIDs(t, productID),
		"a review nobody approved must not appear on the storefront")

	count, average := storefrontSummary(t, productID)
	assert.Equal(t, int64(0), count,
		"and it must not be counted either")
	assert.Equal(t, int64(0), average,
		"a one-star review nobody approved must not move the printed rating")

	// It really was stored: the absence above is the module's filter and not a
	// write that failed quietly. The proof runs over the ADMIN surface, which
	// is the only place the row is visible — and that asymmetry is the design.
	recorder, err := adminRequestWithBody(http.MethodGet, "/admin/v1/reviews/"+reviewID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code,
		"an operator has to be able to see the waiting review; body: %s", recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"status":"submitted"`)
}

// TestAnApprovedReviewReachesTheProductPage closes the loop over HTTP.
func TestAnApprovedReviewReachesTheProductPage(t *testing.T) {
	ctx := t.Context()
	productID := reviewFixtureProduct(ctx, t, "e2e-review-approved")

	reviewID := submitReview(t, productID, 4, "it fits well")
	require.Empty(t, storefrontReviewIDs(t, productID),
		"precondition: nothing is published before the decision")

	recorder := moderateReview(t, reviewID, "approved", "")
	require.Equal(t, http.StatusOK, recorder.Code,
		"the operator's approval must land; body: %s", recorder.Body.String())

	assert.Equal(t, []string{reviewID}, storefrontReviewIDs(t, productID),
		"what a person approved is what a shopper reads")

	count, average := storefrontSummary(t, productID)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(400), average, "four stars is 400 hundredths")

	// And the way back down exists, which is the edge a two-verb API would have
	// left out.
	recorder = moderateReview(t, reviewID, "rejected", "it names a person")
	require.Equal(t, http.StatusOK, recorder.Code,
		"an approved review must be removable; body: %s", recorder.Body.String())

	assert.Empty(t, storefrontReviewIDs(t, productID),
		"a review taken back down must leave the storefront")
}

// TestTheStorefrontCannotPublishItsOwnReview is the attack the design exists to
// refuse.
//
// The storefront's only principal is the publishable key, so if any store-side
// input could reach the status, an anonymous writer would be publishing to a
// shop's product page. Two doors are tried: naming the status in the submission
// body, and asking the listing for a different one.
func TestTheStorefrontCannotPublishItsOwnReview(t *testing.T) {
	ctx := t.Context()
	productID := reviewFixtureProduct(ctx, t, "e2e-review-self-publish")

	// The body carries a status. An unknown field is refused outright rather
	// than ignored, so the attempt cannot succeed quietly.
	recorder := storefrontRequest(t, http.MethodPost,
		"/store/v1/products/"+productID+"/reviews",
		`{"rating":5,"body":"mine","author_name":"me","status":"approved"}`)
	// 422 and not 400: the core maps an invalid request to Unprocessable
	// Entity, and what matters here is that the field is REFUSED rather than
	// ignored — an ignored "status" would let a caller believe it published
	// itself while the review sat in the queue, which is the worse of the two
	// failures.
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code,
		"a status in the submission body has to be REFUSED, not ignored; body: %s",
		recorder.Body.String())

	// A review that really was submitted, then a listing asked for the other
	// statuses. The parameter does not exist and must not become one: the
	// query carries the literal.
	reviewID := submitReview(t, productID, 5, "still waiting")

	for _, status := range []string{"submitted", "rejected", "approved"} {
		recorder := storefrontRequest(t, http.MethodGet,
			"/store/v1/products/"+productID+"/reviews?status="+status, "")
		require.Equal(t, http.StatusOK, recorder.Code,
			"an unknown query parameter is ignored rather than refused; body: %s",
			recorder.Body.String())
		assert.NotContains(t, recorder.Body.String(), reviewID,
			"?status=%s must not widen the storefront listing", status)
	}
}

// TestTheModerationEndpointsRefuseAStorefrontCaller is the other half of the
// same claim: the human in the middle has to be an ADMIN.
//
// A publishable key is what a storefront has, and it is in the browser. If the
// moderation endpoint answered it, the approval step would be a formality
// anybody could perform on themselves.
func TestTheModerationEndpointsRefuseAStorefrontCaller(t *testing.T) {
	ctx := t.Context()
	productID := reviewFixtureProduct(ctx, t, "e2e-review-store-cannot-moderate")

	reviewID := submitReview(t, productID, 5, "please publish me")

	recorder := storefrontRequest(t, http.MethodPost,
		"/admin/v1/reviews/"+reviewID+"/status", `{"status":"approved"}`)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code,
		"a publishable key must not reach the moderation endpoint; body: %s",
		recorder.Body.String())

	assert.Empty(t, storefrontReviewIDs(t, productID),
		"and the review must still be invisible")
}
