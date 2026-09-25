package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
)

// SaveToWishlist is the in-memory twin of the repository method: a variant
// already saved comes back unchanged, and a new one past the limit is refused.
// The real cap is held under the customer row's lock, which only the
// integration test can show.
func (m *memRepo) SaveToWishlist(
	_ context.Context,
	customerID, variantID string,
	limit int64,
	now time.Time,
) (models.WishlistItem, error) {
	m.record("SaveToWishlist")
	m.calls["SaveToWishlist.limit"] = int(limit)

	if _, ok := m.liveCustomer(customerID); !ok {
		return models.WishlistItem{}, errors.NotFound(repository.CodeCustomerNotFound,
			"customer not found: %s", customerID)
	}
	if m.wishlist == nil {
		m.wishlist = map[string][]models.WishlistItem{}
	}

	for _, item := range m.wishlist[customerID] {
		if item.VariantID == variantID {
			return item, nil
		}
	}
	if int64(len(m.wishlist[customerID])) >= limit {
		return models.WishlistItem{}, errors.Conflict(models.CodeWishlistFull, "the wishlist is full")
	}

	item := models.WishlistItem{CustomerID: customerID, VariantID: variantID, CreatedAt: now}
	m.wishlist[customerID] = append(m.wishlist[customerID], item)
	return item, nil
}

// ListWishlist returns the items newest first, as the query orders them.
func (m *memRepo) ListWishlist(_ context.Context, customerID string) ([]models.WishlistItem, error) {
	m.record("ListWishlist")

	out := slices.Clone(m.wishlist[customerID])
	slices.SortFunc(out, func(a, b models.WishlistItem) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.VariantID, b.VariantID)
	})
	return out, nil
}

// RemoveFromWishlist drops the item if it is there.
func (m *memRepo) RemoveFromWishlist(_ context.Context, customerID, variantID string) error {
	m.record("RemoveFromWishlist")

	if _, ok := m.liveCustomer(customerID); !ok {
		return errors.NotFound(repository.CodeCustomerNotFound, "customer not found: %s", customerID)
	}
	m.wishlist[customerID] = slices.DeleteFunc(m.wishlist[customerID], func(item models.WishlistItem) bool {
		return item.VariantID == variantID
	})
	return nil
}

// WishlistForDisclosure reads the items of every given customer.
func (m *memRepo) WishlistForDisclosure(_ context.Context, customerIDs []string) ([]models.WishlistItem, error) {
	m.record("WishlistForDisclosure")

	var out []models.WishlistItem
	for _, id := range customerIDs {
		out = append(out, m.wishlist[id]...)
	}
	return out, nil
}

// newWishlistService builds a service whose clock the test moves.
func newWishlistService(t *testing.T) (*Service, *memRepo, *time.Time) {
	t.Helper()

	repo := newMemRepo()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	return New(repo, Options{Now: func() time.Time { return now }}), repo, &now
}

// newWishlistCustomer opens a customer to save variants on.
func newWishlistCustomer(ctx context.Context, t *testing.T, svc *Service) models.Customer {
	t.Helper()

	created, err := svc.CreateCustomer(ctx, CustomerInput{Email: "wish@example.com"})
	require.NoError(t, err)
	return created
}

// TestAVariantSavedTwiceIsOneItem shows the save can be repeated: the second
// save returns the first one's item, moment included.
func TestAVariantSavedTwiceIsOneItem(t *testing.T) {
	ctx := context.Background()
	svc, _, now := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	first, err := svc.SaveToWishlist(ctx, customer.ID, "variant_A")
	require.NoError(t, err)
	*now = now.Add(time.Hour)
	again, err := svc.SaveToWishlist(ctx, customer.ID, "variant_A")
	require.NoError(t, err)

	assert.Equal(t, first, again, "saving again does not move the item")
	items, err := svc.ListWishlist(ctx, customer.ID)
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

// TestTheWishlistIsReadNewestFirst shows the order the listing promises.
func TestTheWishlistIsReadNewestFirst(t *testing.T) {
	ctx := context.Background()
	svc, _, now := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	for _, variant := range []string{"variant_A", "variant_B", "variant_C"} {
		_, err := svc.SaveToWishlist(ctx, customer.ID, variant)
		require.NoError(t, err)
		*now = now.Add(time.Minute)
	}

	items, err := svc.ListWishlist(ctx, customer.ID)
	require.NoError(t, err)
	variants := make([]string, 0, len(items))
	for _, item := range items {
		variants = append(variants, item.VariantID)
	}
	assert.Equal(t, []string{"variant_C", "variant_B", "variant_A"}, variants)
}

// TestTheWishlistCapIsTheModelsConstant shows the service hands the storage the
// published cap, which the storage holds under the customer's lock.
func TestTheWishlistCapIsTheModelsConstant(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	_, err := svc.SaveToWishlist(ctx, customer.ID, "variant_A")
	require.NoError(t, err)
	assert.Equal(t, models.MaxWishlistItems, repo.calls["SaveToWishlist.limit"])
}

// TestAVariantIDIsCheckedForItsFormOnly shows what the module can say about an
// id it does not own: that it is there, trimmed and bounded, and nothing about
// its prefix.
func TestAVariantIDIsCheckedForItsFormOnly(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	for name, variant := range map[string]string{
		"empty":    "",
		"padded":   " variant_A",
		"too long": strings.Repeat("v", maxIDLen+1),
	} {
		_, err := svc.SaveToWishlist(ctx, customer.ID, variant)
		require.Error(t, err, name)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), name)

		err = svc.RemoveFromWishlist(ctx, customer.ID, variant)
		require.Error(t, err, name)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), name)
	}
	assert.Zero(t, repo.calls["SaveToWishlist"]+repo.calls["RemoveFromWishlist"],
		"a malformed id never reaches the storage")

	_, err := svc.SaveToWishlist(ctx, customer.ID, "sku-without-a-known-prefix")
	require.NoError(t, err, "the prefix is the product module's to choose")
}

// TestTheWishlistOfAnUnknownCustomerIsNotFound shows the listing reads the
// customer first: an empty list would say "nothing saved" where the answer is
// that there is nobody.
func TestTheWishlistOfAnUnknownCustomerIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := newWishlistService(t)

	_, err := svc.ListWishlist(ctx, models.NewCustomerID(time.Now()))
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Zero(t, repo.calls["ListWishlist"])
}

// TestRemovingAVariantNotSavedIsNotAnError shows the removal can be repeated.
func TestRemovingAVariantNotSavedIsNotAnError(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)

	_, err := svc.SaveToWishlist(ctx, customer.ID, "variant_A")
	require.NoError(t, err)
	require.NoError(t, svc.RemoveFromWishlist(ctx, customer.ID, "variant_A"))
	require.NoError(t, svc.RemoveFromWishlist(ctx, customer.ID, "variant_A"))

	items, err := svc.ListWishlist(ctx, customer.ID)
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestADisclosureListsTheWishlist shows a saved variant in the person's file,
// after their customer record, under the declared column.
func TestADisclosureListsTheWishlist(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newWishlistService(t)
	customer := newWishlistCustomer(ctx, t, svc)
	_, err := svc.SaveToWishlist(ctx, customer.ID, "variant_A")
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: customer.ID})
	require.NoError(t, err)

	require.Len(t, disclosure.Records, 2)
	assert.Equal(t, TableCustomer, disclosure.Records[0].Table)
	wished := disclosure.Records[1]
	assert.Equal(t, TableWishlist, wished.Table)
	assert.Empty(t, wished.ID, "the row's key is the pair, so the record names no id")
	assert.Equal(t, declaredColumnsOf(TableWishlist), columnsOf(wished))
	assert.Equal(t, "variant_A", valueOf(t, wished, "variant_id"))
}
