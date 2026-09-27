//go:build integration

package customer_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// wishlistRows counts a customer's wishlist rows in the table itself.
func wishlistRows(ctx context.Context, t *testing.T, customerID string) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM customer_wishlist_item WHERE customer_id = $1`, customerID).Scan(&n))
	return n
}

// fillWishlist saves n distinct variants on the customer's wishlist.
func fillWishlist(ctx context.Context, t *testing.T, svc *service.Service, customerID string, n int) {
	t.Helper()

	for i := range n {
		_, err := svc.SaveToWishlist(ctx, customerID, fmt.Sprintf("variant_fill_%03d", i))
		require.NoError(t, err)
	}
}

// TestAVariantSavedTwiceIsOneRow shows the pair is the key: the second save
// writes nothing and returns the first save's moment.
func TestAVariantSavedTwiceIsOneRow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	first, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	again, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)

	assert.True(t, first.CreatedAt.Equal(again.CreatedAt), "saving again does not move the item")
	assert.Equal(t, 1, wishlistRows(ctx, t, owner.ID))
}

// TestAFullWishlistRefusesANewVariantAndKeepsASavedOne shows the cap in the
// database: a new variant is refused with the published code, and a variant
// already on the list is still answered.
func TestAFullWishlistRefusesANewVariantAndKeepsASavedOne(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	fillWishlist(ctx, t, svc, owner.ID, models.MaxWishlistItems)

	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_one_too_many")
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(err))

	_, err = svc.SaveToWishlist(ctx, owner.ID, "variant_fill_000")
	require.NoError(t, err, "a variant already saved is not a new one")
	assert.Equal(t, models.MaxWishlistItems, wishlistRows(ctx, t, owner.ID))
}

// TestASaveWaitsForTheLockBeforeItCounts is the reason the count is made under
// the customer row's lock. A rival transaction holds that lock and has written
// the last place, uncommitted; the save under test has to wait and then count
// the rival's row.
//
// The waiting is not the proof: the insert's foreign key takes KEY SHARE on the
// same row, so a save that counted without the lock would wait too, at its
// insert, having counted one place free. What carries the proof is the answer
// after waking: refused as full, with the table at the cap.
func TestASaveWaitsForTheLockBeforeItCounts(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	fillWishlist(ctx, t, svc, owner.ID, models.MaxWishlistItems-1)

	conn, err := testPool.Pool().Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()

	var rivalPID int32
	require.NoError(t, tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&rivalPID))
	_, err = tx.Exec(ctx, `SELECT id FROM customer WHERE id = $1 FOR UPDATE`, owner.ID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`INSERT INTO customer_wishlist_item (customer_id, variant_id) VALUES ($1, 'variant_rival')`, owner.ID)
	require.NoError(t, err)

	result := make(chan error, 1)
	go func() {
		_, saveErr := svc.SaveToWishlist(ctx, owner.ID, "variant_late")
		result <- saveErr
	}()

	requireBlockedRequest(ctx, t, rivalPID)
	require.NoError(t, tx.Commit(ctx))

	var saveErr error
	select {
	case saveErr = <-result:
	case <-time.After(15 * time.Second):
		t.Fatal("the waiting save did not finish in time")
	}

	require.Error(t, saveErr, "the save counted before the rival's row was committed")
	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(saveErr))
	assert.Equal(t, models.MaxWishlistItems, wishlistRows(ctx, t, owner.ID))
}

// TestAnotherCustomersWishlistIsNotRead shows the listing reads one owner's
// rows, and a removal names its owner too.
func TestAnotherCustomersWishlistIsNotRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner, stranger := newAccount(ctx, t, svc), newAccount(ctx, t, svc)

	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	require.NoError(t, svc.RemoveFromWishlist(ctx, stranger.ID, "variant_A"))

	strangers, err := svc.ListWishlist(ctx, stranger.ID)
	require.NoError(t, err)
	assert.Empty(t, strangers)
	owners, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, owners, 1, "the stranger's removal did not reach the owner's row")

	require.NoError(t, svc.RemoveFromWishlist(ctx, owner.ID, "variant_A"))
	assert.Zero(t, wishlistRows(ctx, t, owner.ID), "the owner's removal did")
}

// TestTheWishlistIsReadNewestFirstFromTheTable shows the order the query gives.
func TestTheWishlistIsReadNewestFirstFromTheTable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	for _, variant := range []string{"variant_B", "variant_A", "variant_C"} {
		_, err := svc.SaveToWishlist(ctx, owner.ID, variant)
		require.NoError(t, err)
	}

	items, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	variants := make([]string, 0, len(items))
	for _, item := range items {
		variants = append(variants, item.VariantID)
	}
	assert.Equal(t, []string{"variant_C", "variant_A", "variant_B"}, variants,
		"saved in the order B, A, C, so read C, A, B; alphabetical would be A, B, C")
}

// TestADeletedCustomerHasNoWishlistToWriteOrRead shows the wishlist goes with
// the customer's soft delete: its rows are unreachable, and kept until an
// erasure, as the addresses are.
func TestADeletedCustomerHasNoWishlistToWriteOrRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	_, err := svc.SaveToWishlist(ctx, owner.ID, "variant_A")
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCustomer(ctx, owner.ID))

	_, err = svc.SaveToWishlist(ctx, owner.ID, "variant_B")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	err = svc.RemoveFromWishlist(ctx, owner.ID, "variant_A")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
	_, err = svc.ListWishlist(ctx, owner.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "%v", err)
}

// TestAnErasureDeletesTheWishlist shows the declared column emptied by deleting
// its rows, counted in the receipt, and a second erasure finding nothing.
func TestAnErasureDeletesTheWishlist(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)
	created := personalRecord(ctx, t, svc, true)
	for _, variant := range []string{"variant_A", "variant_B"} {
		_, err := svc.SaveToWishlist(ctx, created.ID, variant)
		require.NoError(t, err)
	}

	first, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Zero(t, wishlistRows(ctx, t, created.ID))
	assert.Equal(t, 1+1+2, first.Rows, "the customer, the address and the two wishlist rows")

	second, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Zero(t, second.Rows, "a second erasure has nothing left to delete")
}

// TestADisclosureShowsTheWishlistInTheDatabase shows the saved variants in the
// person's file, read from the table, each with whether the person asked to be
// told of its stock (ADR 0215).
func TestADisclosureShowsTheWishlistInTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)
	created := personalRecord(ctx, t, svc, true)
	_, err := svc.SaveToWishlist(ctx, created.ID, "variant_A")
	require.NoError(t, err)
	_, err = svc.MarkStockAlert(ctx, created.ID, "variant_B", []string{"sc_1"})
	require.NoError(t, err)
	_, err = svc.MarkPriceAlert(ctx, created.ID, "variant_C", "reg_1", nil)
	require.NoError(t, err)

	disclosure, err := svc.PersonalDataOf(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)

	alerts := map[any][2]any{}
	for _, record := range disclosure.Records {
		if record.Table != service.TableWishlist {
			continue
		}
		fields := map[string]any{}
		for _, field := range record.Fields {
			fields[field.Column] = field.Value
		}
		alerts[fields["variant_id"]] = [2]any{fields["stock_alert"], fields["price_alert"]}
	}
	assert.Equal(t, map[any][2]any{
		"variant_A": {false, false}, "variant_B": {true, false}, "variant_C": {false, true},
	}, alerts, "each item with its stock mark and its price mark")
}

// TestABlankVariantIsRefusedByTheTable shows the CHECK behind the service's
// own refusal, for a writer that reaches the table around it.
func TestABlankVariantIsRefusedByTheTable(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO customer_wishlist_item (customer_id, variant_id) VALUES ($1, '  ')`, owner.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "customer_wishlist_item_variant_check")
}

// stockAlertsOf pages every mark through the interop and keeps the given
// customer's, as the stock alert flow reads them.
func stockAlertsOf(ctx context.Context, t *testing.T, svc *service.Service, customerID string) []map[string]any {
	t.Helper()

	var mine []map[string]any
	afterCustomer, afterVariant := "", ""
	for {
		raw, err := svc.WishlistAlertsJSON(ctx, afterCustomer, afterVariant, 500)
		require.NoError(t, err)
		var page []map[string]any
		require.NoError(t, json.Unmarshal(raw, &page))
		for _, alert := range page {
			if alert["customer_id"] == customerID {
				mine = append(mine, alert)
			}
		}
		if len(page) < 500 {
			return mine
		}
		afterCustomer, _ = page[len(page)-1]["customer_id"].(string)
		afterVariant, _ = page[len(page)-1]["variant_id"].(string)
	}
}

// TestAStockAlertLivesOnTheRealSchema is ADR 0215 in the module: a mark saves
// and marks under the list's cap, the page names it with its channels, it is
// armed once, a clear takes only the arming it was sent for, and the schema
// refuses an arming without a mark.
func TestAStockAlertLivesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	item, err := svc.MarkStockAlert(ctx, owner.ID, "variant_alert", []string{"sc_1", "sc_2"})
	require.NoError(t, err)
	assert.True(t, item.StockAlert)
	assert.Equal(t, 1, wishlistRows(ctx, t, owner.ID), "the mark saved the variant")
	_, err = svc.MarkStockAlert(ctx, owner.ID, "variant_nochannel", nil)
	require.NoError(t, err)

	alerts := stockAlertsOf(ctx, t, svc, owner.ID)
	require.Len(t, alerts, 2)
	byVariant := map[any]map[string]any{}
	for _, alert := range alerts {
		byVariant[alert["variant_id"]] = alert
	}
	assert.Equal(t, []any{"sc_1", "sc_2"}, byVariant["variant_alert"]["sales_channel_ids"])
	assert.Nil(t, byVariant["variant_nochannel"]["sales_channel_ids"], "no channel is not the empty set")

	armed, err := svc.ArmStockAlert(ctx, owner.ID, "variant_alert")
	require.NoError(t, err)
	assert.True(t, armed)
	again, err := svc.ArmStockAlert(ctx, owner.ID, "variant_alert")
	require.NoError(t, err)
	assert.False(t, again, "armed once")

	items, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	var armedAt time.Time
	for _, i := range items {
		if i.VariantID == "variant_alert" {
			require.NotNil(t, i.StockAlertArmedAt)
			armedAt = *i.StockAlertArmedAt
		}
	}
	stale, err := svc.ClearStockAlert(ctx, owner.ID, "variant_alert", armedAt.Add(-time.Second))
	require.NoError(t, err)
	assert.False(t, stale, "a clear for another arming takes nothing")
	cleared, err := svc.ClearStockAlert(ctx, owner.ID, "variant_alert", armedAt)
	require.NoError(t, err)
	assert.True(t, cleared)
	assert.Len(t, stockAlertsOf(ctx, t, svc, owner.ID), 1, "the cleared mark is gone, the other stays")
	assert.Equal(t, 2, wishlistRows(ctx, t, owner.ID), "clearing leaves the items")

	_, err = testPool.Pool().Exec(ctx, `UPDATE customer_wishlist_item SET stock_alert_armed_at = now()
        WHERE customer_id = $1 AND variant_id = 'variant_alert'`, owner.ID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "customer_wishlist_item_alert_marked", pgErr.ConstraintName)
}

// TestADeletedCustomersMarksAreNotRead: the flow would mail a person who is no
// longer a customer.
func TestADeletedCustomersMarksAreNotRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	_, err := svc.MarkStockAlert(ctx, owner.ID, "variant_alert", []string{"sc_1"})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCustomer(ctx, owner.ID))

	assert.Empty(t, stockAlertsOf(ctx, t, svc, owner.ID))
}

// TestAFullWishlistTakesNoNewMark: the mark's save is under the list's cap.
func TestAFullWishlistTakesNoNewMark(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	fillWishlist(ctx, t, svc, owner.ID, models.MaxWishlistItems)

	_, err := svc.MarkStockAlert(ctx, owner.ID, "variant_one_more", nil)
	assert.Equal(t, models.CodeWishlistFull, errors.CodeOf(err))
	_, err = svc.MarkStockAlert(ctx, owner.ID, "variant_fill_000", nil)
	require.NoError(t, err, "a variant already on a full list is marked")
}

// TestAMarkSetAgainWaitsAgain: marking an armed item forgets the arming, so the
// variant has to be seen out of stock again before it mails.
func TestAMarkSetAgainWaitsAgain(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	_, err := svc.MarkStockAlert(ctx, owner.ID, "variant_again", []string{"sc_1"})
	require.NoError(t, err)
	armed, err := svc.ArmStockAlert(ctx, owner.ID, "variant_again")
	require.NoError(t, err)
	require.True(t, armed)

	again, err := svc.MarkStockAlert(ctx, owner.ID, "variant_again", []string{"sc_2"})

	require.NoError(t, err)
	assert.Nil(t, again.StockAlertArmedAt, "the wait starts again")
	assert.Equal(t, []string{"sc_2"}, again.StockAlertChannels, "and in the new request's channels")
}

// TestAPriceAlertLivesOnTheRealSchema is ADR 0216 in the module: a mark names
// its region and channels and no price, the price at the mark is recorded once
// and only for the mark it was read for, a mark set again forgets it, a clear
// takes only its own mark and leaves the stock mark beside it, and the schema
// refuses half a price.
func TestAPriceAlertLivesOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)

	item, err := svc.MarkPriceAlert(ctx, owner.ID, "variant_price", "reg_1", []string{"sc_1"})
	require.NoError(t, err)
	assert.True(t, item.PriceAlert)
	assert.Nil(t, item.PriceAlertAmount, "the request records no price")
	require.NotNil(t, item.PriceAlertMarkedAt)
	markedAt := *item.PriceAlertMarkedAt
	_, err = svc.MarkStockAlert(ctx, owner.ID, "variant_price", nil)
	require.NoError(t, err)

	priceOnly, err := svc.MarkPriceAlert(ctx, owner.ID, "variant_price_only", "reg_1", nil)
	require.NoError(t, err)
	alerts := stockAlertsOf(ctx, t, svc, owner.ID)
	require.Len(t, alerts, 2, "a price mark alone is paged")
	require.NoError(t, svc.UnmarkPriceAlert(ctx, owner.ID, priceOnly.VariantID))

	alerts = stockAlertsOf(ctx, t, svc, owner.ID)
	require.Len(t, alerts, 1, "one item carries both marks, and an unmarked item is not paged")
	assert.Equal(t, true, alerts[0]["price_alert"])
	assert.Equal(t, true, alerts[0]["stock_alert"])
	assert.Equal(t, "reg_1", alerts[0]["price_region_id"])
	assert.Equal(t, []any{"sc_1"}, alerts[0]["price_sales_channel_ids"])
	assert.Nil(t, alerts[0]["price_amount"])

	stale, err := svc.RecordPriceBaseline(ctx, owner.ID, "variant_price", markedAt.Add(-time.Second), "TRY", 2_000)
	require.NoError(t, err)
	assert.False(t, stale, "a price read for another mark is not recorded")
	recorded, err := svc.RecordPriceBaseline(ctx, owner.ID, "variant_price", markedAt, "TRY", 2_000)
	require.NoError(t, err)
	assert.True(t, recorded)
	again, err := svc.RecordPriceBaseline(ctx, owner.ID, "variant_price", markedAt, "TRY", 1_000)
	require.NoError(t, err)
	assert.False(t, again, "recorded once")
	alerts = stockAlertsOf(ctx, t, svc, owner.ID)
	require.Len(t, alerts, 1)
	assert.Equal(t, "TRY", alerts[0]["price_currency_code"])
	assert.Equal(t, float64(2_000), alerts[0]["price_amount"])

	remarked, err := svc.MarkPriceAlert(ctx, owner.ID, "variant_price", "reg_2", nil)
	require.NoError(t, err)
	assert.Nil(t, remarked.PriceAlertAmount, "a mark set again waits for its own price")
	assert.Empty(t, remarked.PriceAlertCurrency)
	assert.Equal(t, "reg_2", remarked.PriceAlertRegionID)
	require.NotNil(t, remarked.PriceAlertMarkedAt)
	assert.True(t, remarked.PriceAlertMarkedAt.After(markedAt), "a new mark")

	cleared, err := svc.ClearPriceAlert(ctx, owner.ID, "variant_price", markedAt)
	require.NoError(t, err)
	assert.False(t, cleared, "a clear for the old mark takes nothing")
	cleared, err = svc.ClearPriceAlert(ctx, owner.ID, "variant_price", *remarked.PriceAlertMarkedAt)
	require.NoError(t, err)
	assert.True(t, cleared)
	alerts = stockAlertsOf(ctx, t, svc, owner.ID)
	require.Len(t, alerts, 1, "the stock mark stays")
	assert.Equal(t, false, alerts[0]["price_alert"])
	assert.Equal(t, true, alerts[0]["stock_alert"])

	for constraint, update := range map[string]string{
		"customer_wishlist_item_price_marked":         `SET price_alert = true`,
		"customer_wishlist_item_price_unmarked_empty": `SET price_alert_channels = '{sc_1}'`,
		"customer_wishlist_item_price_baseline_whole": `SET price_alert = true, price_alert_marked_at = now(),
            price_alert_region_id = 'reg_1', price_alert_currency = 'TRY'`,
		"customer_wishlist_item_price_amount_nonneg": `SET price_alert = true, price_alert_marked_at = now(),
            price_alert_region_id = 'reg_1', price_alert_currency = 'TRY', price_alert_amount = -1`,
	} {
		_, err = testPool.Pool().Exec(ctx, `UPDATE customer_wishlist_item `+update+`
            WHERE customer_id = $1 AND variant_id = 'variant_price'`, owner.ID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr, constraint)
		assert.Equal(t, constraint, pgErr.ConstraintName)
	}
}

// TestUnmarkingAPriceLeavesTheStockMark: the two marks on one item are taken
// off one at a time.
func TestUnmarkingAPriceLeavesTheStockMark(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	owner := newAccount(ctx, t, svc)
	_, err := svc.MarkPriceAlert(ctx, owner.ID, "variant_both", "reg_1", nil)
	require.NoError(t, err)
	_, err = svc.MarkStockAlert(ctx, owner.ID, "variant_both", nil)
	require.NoError(t, err)

	require.NoError(t, svc.UnmarkPriceAlert(ctx, owner.ID, "variant_both"))

	items, err := svc.ListWishlist(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].PriceAlert)
	assert.Empty(t, items[0].PriceAlertRegionID)
	assert.True(t, items[0].StockAlert, "the stock mark is the other half of the item")
	require.NoError(t, svc.UnmarkPriceAlert(ctx, owner.ID, "variant_both"), "unmarking again is not an error")
}
