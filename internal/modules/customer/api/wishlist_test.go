package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

func (s *stubCustomer) SaveToWishlist(ctx context.Context, customerID, variantID string) (models.WishlistItem, error) {
	if s.saveToWishlistFn == nil {
		return models.WishlistItem{}, unset("SaveToWishlist")
	}
	return s.saveToWishlistFn(ctx, customerID, variantID)
}

func (s *stubCustomer) ListWishlist(ctx context.Context, customerID string) ([]models.WishlistItem, error) {
	if s.listWishlistFn == nil {
		return nil, unset("ListWishlist")
	}
	return s.listWishlistFn(ctx, customerID)
}

func (s *stubCustomer) RemoveFromWishlist(ctx context.Context, customerID, variantID string) error {
	if s.removeFromWishlistFn == nil {
		return unset("RemoveFromWishlist")
	}
	return s.removeFromWishlistFn(ctx, customerID, variantID)
}

func (s *stubCustomer) MarkStockAlert(
	ctx context.Context, customerID, variantID string, channels []string,
) (models.WishlistItem, error) {
	if s.markStockAlertFn == nil {
		return models.WishlistItem{}, unset("MarkStockAlert")
	}
	return s.markStockAlertFn(ctx, customerID, variantID, channels)
}

func (s *stubCustomer) UnmarkStockAlert(ctx context.Context, customerID, variantID string) error {
	if s.unmarkStockAlertFn == nil {
		return unset("UnmarkStockAlert")
	}
	return s.unmarkStockAlertFn(ctx, customerID, variantID)
}

func (s *stubCustomer) MarkPriceAlert(
	ctx context.Context, customerID, variantID, regionID string, channels []string,
) (models.WishlistItem, error) {
	if s.markPriceAlertFn == nil {
		return models.WishlistItem{}, unset("MarkPriceAlert")
	}
	return s.markPriceAlertFn(ctx, customerID, variantID, regionID, channels)
}

func (s *stubCustomer) UnmarkPriceAlert(ctx context.Context, customerID, variantID string) error {
	if s.unmarkPriceAlertFn == nil {
		return unset("UnmarkPriceAlert")
	}
	return s.unmarkPriceAlertFn(ctx, customerID, variantID)
}

// TestAVariantIsSavedForTheProvenCustomer shows the save reaches the service
// with the proven customer and the variant the path names, and answers with
// the item.
func TestAVariantIsSavedForTheProvenCustomer(t *testing.T) {
	t.Parallel()

	saved := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	var askedCustomer, askedVariant string
	svc := &stubCustomer{
		saveToWishlistFn: func(_ context.Context, customerID, variantID string) (models.WishlistItem, error) {
			askedCustomer, askedVariant = customerID, variantID
			return models.WishlistItem{CustomerID: customerID, VariantID: variantID, CreatedAt: saved}, nil
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, http.MethodPut, "/store/v1/customers/"+provenCustomer+"/wishlist/variant_X", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, provenCustomer, askedCustomer)
	assert.Equal(t, "variant_X", askedVariant)

	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"customer_id": provenCustomer,
		"variant_id":  "variant_X",
		"created_at":  "2026-09-26T10:00:00Z",
		"stock_alert": false,
		"price_alert": false,
	}, body.Data)
}

// TestTheWishlistRefusesAnIdentityProvingSomebodyElse shows a save and a
// removal naming another customer are refused before the service is asked.
func TestTheWishlistRefusesAnIdentityProvingSomebodyElse(t *testing.T) {
	t.Parallel()

	r := routerWithIdentity(refusingService(t), &fixedIdentity{customerID: provenCustomer})

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		rec := send(t, r, method, "/store/v1/customers/"+claimedByAnother+"/wishlist/variant_X", "")

		require.Equal(t, http.StatusForbidden, rec.Code, "%s: %s", method, rec.Body.String())
		assert.Equal(t, corehttp.CodeIdentityMismatch, errorCode(t, rec), method)
	}
}

// TestAFullWishlistIsAConflict shows the refusal's status and code.
func TestAFullWishlistIsAConflict(t *testing.T) {
	t.Parallel()

	svc := &stubCustomer{
		saveToWishlistFn: func(context.Context, string, string) (models.WishlistItem, error) {
			return models.WishlistItem{}, errors.Conflict(models.CodeWishlistFull, "full")
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, http.MethodPut, "/store/v1/customers/"+provenCustomer+"/wishlist/variant_X", "")

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, models.CodeWishlistFull, errorCode(t, rec))
}

// TestARemovalFromTheWishlistAnswersNoContent shows the removal's answer and
// what reached the service.
func TestARemovalFromTheWishlistAnswersNoContent(t *testing.T) {
	t.Parallel()

	var askedCustomer, askedVariant string
	svc := &stubCustomer{
		removeFromWishlistFn: func(_ context.Context, customerID, variantID string) error {
			askedCustomer, askedVariant = customerID, variantID
			return nil
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})

	rec := send(t, r, http.MethodDelete, "/store/v1/customers/"+provenCustomer+"/wishlist/variant_X", "")

	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, provenCustomer, askedCustomer)
	assert.Equal(t, "variant_X", askedVariant)
}

// TestTheOperatorReadsAnyWishlist shows the admin read takes the customer from
// the path and asks no customer identity, as the admin address book does.
func TestTheOperatorReadsAnyWishlist(t *testing.T) {
	t.Parallel()

	var asked string
	svc := &stubCustomer{
		listWishlistFn: func(_ context.Context, customerID string) ([]models.WishlistItem, error) {
			asked = customerID
			return []models.WishlistItem{{CustomerID: customerID, VariantID: "variant_X"}}, nil
		},
	}
	r := routerWithIdentity(svc, nil)

	req := httptest.NewRequestWithContext(
		corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}),
		http.MethodGet, "/admin/v1/customers/"+claimedByAnother+"/wishlist", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, claimedByAnother, asked)

	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	assert.Equal(t, "variant_X", body.Data[0]["variant_id"])
	assert.Equal(t, int64(1), body.Count)
}

// TestAStockAlertCarriesTheRequestsChannels is ADR 0215: the mark is set for
// the proven customer with the sales channels the request's key holds, which
// is what the variant will be judged in stock over.
func TestAStockAlertCarriesTheRequestsChannels(t *testing.T) {
	t.Parallel()

	var askedCustomer, askedVariant string
	var askedChannels []string
	svc := &stubCustomer{
		markStockAlertFn: func(_ context.Context, customerID, variantID string, channels []string) (models.WishlistItem, error) {
			askedCustomer, askedVariant, askedChannels = customerID, variantID, channels
			return models.WishlistItem{CustomerID: customerID, VariantID: variantID, StockAlert: true}, nil
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})
	ctx := corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID: "pk_1", Kind: "publishable_key", SalesChannelIDs: []string{"sc_ours"},
	})
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/store/v1/customers/"+provenCustomer+"/wishlist/variant_X/stock-alert", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, provenCustomer, askedCustomer)
	assert.Equal(t, "variant_X", askedVariant)
	assert.Equal(t, []string{"sc_ours"}, askedChannels)
	assert.Contains(t, rec.Body.String(), `"stock_alert":true`)
}

// TestAPriceAlertCarriesTheRegionAndTheRequestsChannels is ADR 0216: the mark
// is set for the proven customer in the body's region with the sales channels
// the request's key holds, which is where the variant will be judged shown.
func TestAPriceAlertCarriesTheRegionAndTheRequestsChannels(t *testing.T) {
	t.Parallel()

	var askedCustomer, askedVariant, askedRegion string
	var askedChannels []string
	svc := &stubCustomer{
		markPriceAlertFn: func(
			_ context.Context, customerID, variantID, regionID string, channels []string,
		) (models.WishlistItem, error) {
			askedCustomer, askedVariant, askedRegion, askedChannels = customerID, variantID, regionID, channels
			return models.WishlistItem{
				CustomerID: customerID, VariantID: variantID, PriceAlert: true, PriceAlertRegionID: regionID,
			}, nil
		},
	}
	r := routerWithIdentity(svc, &fixedIdentity{customerID: provenCustomer})
	ctx := corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID: "pk_1", Kind: "publishable_key", SalesChannelIDs: []string{"sc_ours"},
	})
	req := httptest.NewRequestWithContext(ctx, http.MethodPut,
		"/store/v1/customers/"+provenCustomer+"/wishlist/variant_X/price-alert",
		strings.NewReader(`{"region_id":"reg_tr"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, provenCustomer, askedCustomer)
	assert.Equal(t, "variant_X", askedVariant)
	assert.Equal(t, "reg_tr", askedRegion)
	assert.Equal(t, []string{"sc_ours"}, askedChannels)
	assert.Contains(t, rec.Body.String(), `"price_alert":true`)
	assert.Contains(t, rec.Body.String(), `"price_alert_region_id":"reg_tr"`)
}
