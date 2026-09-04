package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// This file is inside the package: what is under test is the CONVERSION OF THE
// DRIVER ERROR INTO A TYPED ERROR, and the mapping is not exported. That the
// mapping really works (that is, that Postgres really produces these
// SQLSTATEs) is proven in the integration tests; what is under test here is the
// given error landing in the right class.

// TestWrapDBMapsNoRowsToNotFound verifies that a row not being found is
// NotFound.
func TestWrapDBMapsNoRowsToNotFound(t *testing.T) {
	t.Parallel()

	err := wrapDB(pgx.ErrNoRows, "product not found: %s", "prod_1")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "expected class not_found: %v", err)
	assert.Equal(t, codeNotFound, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "prod_1", "the message must carry the id for diagnosis")
}

// TestWrapDBMapsUniqueViolation verifies that a uniqueness violation falls to
// Conflict and to a stable code according to the constraint name.
func TestWrapDBMapsUniqueViolation(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"product_handle_uniq":            codeHandleTaken,
		"product_collection_handle_uniq": codeHandleTaken,
		"product_variant_sku_uniq":       codeSKUTaken,
		"product_tag_value_uniq":         codeDuplicate,
		"product_option_title_uniq":      codeDuplicate,
		"unknown_constraint":             codeConflict,
		"":                               codeConflict,
	}

	for constraint, wantCode := range cases {
		t.Run(constraint, func(t *testing.T) {
			t.Parallel()

			pgErr := &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: constraint}
			err := wrapDB(pgErr, "could not create product (%s)", "tshirt")

			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "expected class conflict: %v", err)
			assert.Equal(t, wantCode, coreerrors.CodeOf(err))
			assert.ErrorIs(t, err, error(pgErr), "the original driver error must stay in the chain")
		})
	}
}

// TestWrapDBMapsForeignKeyAndCheck verifies that reference and constraint
// violations count as a CLIENT error (Invalid).
//
// The classification matters: binding a product to a collection that does not
// exist is an error the client can correct; returning 500 would make it look
// like a server fault.
func TestWrapDBMapsForeignKeyAndCheck(t *testing.T) {
	t.Parallel()

	fkErr := wrapDB(&pgconn.PgError{Code: pgForeignKeyViolation, ConstraintName: "product_collection_id_fkey"},
		"could not create product")
	assert.True(t, coreerrors.IsInvalid(fkErr), "expected class invalid: %v", fkErr)
	assert.Equal(t, codeInvalidRef, coreerrors.CodeOf(fkErr))
	assert.Contains(t, fkErr.Error(), "product_collection_id_fkey", "the constraint name must stay in the message for diagnosis")

	checkErr := wrapDB(&pgconn.PgError{Code: pgCheckViolation, ConstraintName: "product_status_check"},
		"could not create product")
	assert.True(t, coreerrors.IsInvalid(checkErr), "expected class invalid: %v", checkErr)
	assert.Equal(t, codeCheckFailed, coreerrors.CodeOf(checkErr))
}

// TestWrapDBMapsCancellation verifies that cancellation is Unavailable and NOT
// Internal.
//
// On a context cancellation pgx returns a raw context.Canceled; had it not been
// classified, a request whose budget ran out would look to the client like an
// opaque 500.
func TestWrapDBMapsCancellation(t *testing.T) {
	t.Parallel()

	for _, base := range []error{context.Canceled, context.DeadlineExceeded} {
		err := wrapDB(base, "could not list products")
		require.Error(t, err)
		assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable),
			"expected class unavailable: %v", err)
		assert.Equal(t, codeCanceled, coreerrors.CodeOf(err))
	}
}

// TestWrapDBDefaultsToInternal verifies that an error that cannot be classified
// falls to the safe side (Internal).
func TestWrapDBDefaultsToInternal(t *testing.T) {
	t.Parallel()

	err := wrapDB(errors.New("unexpected"), "could not list products")
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindInternal), "expected class internal: %v", err)
	assert.Equal(t, codeDBFailed, coreerrors.CodeOf(err))
}

// TestWrapDBNilStaysNil verifies that the error-free path produces no error.
func TestWrapDBNilStaysNil(t *testing.T) {
	t.Parallel()

	assert.NoError(t, wrapDB(nil, "nothing"))
}

// TestMetadataRoundTrip verifies that the jsonb conversion loses no data.
func TestMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	raw, err := fromMetadata(map[string]any{"color": "blue", "count": float64(3)})
	require.NoError(t, err)

	got, err := toMetadata(raw)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"color": "blue", "count": float64(3)}, got)
}

// TestMetadataEmptyBecomesObject verifies that empty metadata is written as the
// empty object suited to the NOT NULL column, and turns back into nil when it
// is read.
func TestMetadataEmptyBecomesObject(t *testing.T) {
	t.Parallel()

	raw, err := fromMetadata(nil)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(raw))

	got, err := toMetadata(raw)
	require.NoError(t, err)
	assert.Nil(t, got, "the empty object must be converted to nil, not to a map (in JSON the field does not show up at all)")

	got, err = toMetadata(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = toMetadata([]byte("null"))
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestPatchMetadataDistinguishesUnsetFromEmpty verifies that the distinction
// between "do not change" and "empty it out" is preserved.
func TestPatchMetadataDistinguishesUnsetFromEmpty(t *testing.T) {
	t.Parallel()

	unset, err := patchMetadata(nil)
	require.NoError(t, err)
	assert.Nil(t, unset, "a nil map must go to the query as NULL; COALESCE keeps the old value")

	empty, err := patchMetadata(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(empty), "an empty map must empty the metadata out")
}

// TestToMetadataRejectsBrokenJSON verifies that broken jsonb content is not
// silently ignored.
func TestToMetadataRejectsBrokenJSON(t *testing.T) {
	t.Parallel()

	_, err := toMetadata([]byte("{broken"))
	require.Error(t, err)
	assert.Equal(t, codeMetadataInvalid, coreerrors.CodeOf(err))
}

// TestToInt32Clamps verifies that the pagination narrowing does not change
// sign.
func TestToInt32Clamps(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(0), toInt32(-1))
	assert.Equal(t, int32(20), toInt32(20))
	assert.Equal(t, int32(2147483647), toInt32(1<<40))
}

// TestNotFoundCarriesEntityAndID verifies that the not-found error carries the
// diagnostic information.
func TestNotFoundCarriesEntityAndID(t *testing.T) {
	t.Parallel()

	err := notFound("variant", "variant_1")
	assert.True(t, coreerrors.IsNotFound(err))
	// The entity is asserted together with the rest of the message: the id
	// fixture starts with the same word, so a bare "variant" would hold even if
	// the entity name were dropped.
	assert.Contains(t, err.Error(), "variant not found", "the message must name the entity")
	assert.Contains(t, err.Error(), "variant_1", "the message must carry the id")
}

// TestEmptyIDListSkipsQuery verifies that an empty id list never goes to the
// database.
//
// The store is built with a nil connection: had a query been made, the test
// would fall with a panic. Sending a "WHERE id = ANY('{}')" query with an empty
// list is a round trip for nothing.
func TestEmptyIDListSkipsQuery(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	ctx := context.Background()

	products, err := repo.ListProductsByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, products)

	variants, err := repo.ListVariantsByIDs(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, variants)

	byProduct, err := repo.ListVariantsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, byProduct)

	options, err := repo.ListOptionsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, options)

	values, err := repo.ListOptionValuesByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, values)

	images, err := repo.ListImagesByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, images)

	tags, err := repo.ListTagsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, tags)

	categories, err := repo.ListCategoriesByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, categories)

	optionValues, err := repo.ListVariantOptionValues(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, optionValues)
}

// TestInTxWithoutPoolReusesSameStore verifies that a store bound to a
// transaction does not open a NESTED transaction.
//
// A second transaction would grab a separate connection from the pool; that
// connection cannot read the outer transaction's writes, which are not visible
// yet, and while waiting for a lock it could walk into a deadlock waiting on
// itself.
func TestInTxWithoutPoolReusesSameStore(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	var got Store

	require.NoError(t, repo.InTx(context.Background(), func(_ context.Context, s Store) error {
		got = s
		return nil
	}))
	assert.Same(t, repo, got, "the store inside the transaction must be the same instance")
}

// TestInTxPropagatesError verifies that fn's error is returned as it is.
func TestInTxPropagatesError(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	want := coreerrors.Invalid("test", "did not work")

	err := repo.InTx(context.Background(), func(context.Context, Store) error { return want })
	assert.ErrorIs(t, err, want)
}

// TestStoreInterfaceIsSatisfied pins down at compile time that the concrete
// store satisfies the contract.
func TestStoreInterfaceIsSatisfied(t *testing.T) {
	t.Parallel()

	var store Store = &Repo{}
	assert.NotNil(t, store)

	// It also pins down that the models package is visible from this layer; the
	// store speaks only in domain types, pgtype does not leak out.
	var product models.Product
	assert.Empty(t, product.ID)
}
