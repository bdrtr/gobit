//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the coupon table's own refusals.
// The service absorbs a repeated code and refuses a non-ASCII one, and a fake
// that imitates both cannot say whether the CONSTRAINTS behind them exist.
package cart_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestACouponCodeIsWrittenAndReadBackOnTheRealSchema is the round trip that
// proves the migration and the query agree.
func TestACouponCodeIsWrittenAndReadBackOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	codes, err := svc.AddPromotionCode(ctx, cart.ID, "  summer20 ")
	require.NoError(t, err)
	assert.Equal(t, []string{"SUMMER20"}, codes)

	_, err = svc.AddPromotionCode(ctx, cart.ID, "FREESHIP")
	require.NoError(t, err)

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"SUMMER20", "FREESHIP"}, detail.PromotionCodes,
		"in the order they were typed")
}

// TestTheSchemaAbsorbsTheSameCodeTwice is the ON CONFLICT, read off the real
// query.
//
// The service checks first, so through that path the check answers. This goes
// AROUND it: two requests racing both pass the check, and the second INSERT hits
// the primary key. Without the ON CONFLICT that would be a 500 on a double
// click.
func TestTheSchemaAbsorbsTheSameCodeTwice(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	store := repository.New(testPool.Pool())

	require.NoError(t, store.AddPromotionCode(ctx, cart.ID, "SUMMER20"))
	require.NoError(t, store.AddPromotionCode(ctx, cart.ID, "SUMMER20"),
		"the second insert is absorbed, not refused")

	held, err := store.ListPromotionCodes(ctx, cart.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"SUMMER20"}, held, "and it is one row")
}

// TestTheColumnRefusesACodeThatIsNotUpperCase is the CHECK the service's
// normalization exists to satisfy.
//
// It goes around the service for the reason above: `code = upper(code)` is what
// keeps two spellings of one coupon out of a cart, and only a write that skips
// the Go side can tell whether the row asserts it.
func TestTheColumnRefusesACodeThatIsNotUpperCase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	store := repository.New(testPool.Pool())

	err := store.AddPromotionCode(ctx, cart.ID, "summer20")

	require.Error(t, err, "the column refuses a lower-case code")
}

// TestTheColumnRefusesAnEmptyCode keeps out the one value that claims a coupon
// while naming none.
func TestTheColumnRefusesAnEmptyCode(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	store := repository.New(testPool.Pool())

	err := store.AddPromotionCode(ctx, cart.ID, "")

	require.Error(t, err)
}

// TestRemovingACodeTheCartDoesNotHoldIsNotFoundOnTheRealQuery is the row count.
//
// A delete that matched nothing and reported success would leave a code on the
// shopper's screen that nothing will ever take off.
func TestRemovingACodeTheCartDoesNotHoldIsNotFoundOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	store := repository.New(testPool.Pool())

	err := store.RemovePromotionCode(ctx, cart.ID, "NEVER_APPLIED")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestTheCodesFollowTheCartWhenItIsDeleted keeps a binding from outliving what
// it binds.
func TestTheCodesFollowTheCartWhenItIsDeleted(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := svc.AddPromotionCode(ctx, cart.ID, "SUMMER20")
	require.NoError(t, err)
	require.NoError(t, svc.DeleteCart(ctx, cart.ID))

	var rows int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM cart_promotion_code WHERE cart_id = $1`, cart.ID).Scan(&rows))
	assert.Zero(t, rows, "the rows are DELETED, not stamped: a binding leaves no trace")
}

// TestTheCartCeilingHoldsAgainstTheRealSchema fills a cart to the limit.
//
// The number is the promotion module's `MaxCodesPerCompute`, restated in this
// module because it cannot import that one. What binds the two is behavior: a
// cart filled to THIS ceiling has to be one the discount round still accepts,
// and the end-to-end suite prices exactly such a cart.
func TestTheCartCeilingHoldsAgainstTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	for i := range service.MaxPromotionCodes {
		_, err := svc.AddPromotionCode(ctx, cart.ID, codeNumber(i))
		require.NoError(t, err, "code %d", i)
	}

	_, err := svc.AddPromotionCode(ctx, cart.ID, "ONE_TOO_MANY")
	require.Error(t, err)
	assert.Equal(t, service.CodeTooManyPromotionCodes, errors.CodeOf(err))

	detail, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	assert.Len(t, detail.PromotionCodes, service.MaxPromotionCodes)
}

// codeNumber produces a distinct coupon code for the ceiling test.
func codeNumber(i int) string {
	return "CODE" + string(rune('A'+i/10)) + string(rune('0'+i%10))
}
