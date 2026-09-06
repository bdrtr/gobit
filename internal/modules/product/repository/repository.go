// Package repository is the data access layer of the product module.
//
// It touches ONLY this module's tables (Principle 2.1): it does not read
// another module's table and does not give it a foreign key. Data belonging to
// other modules, such as price and stock, does not come from here but over the
// links and the Query layer.
//
// Layer boundary: the productdb package sqlc generates and the pgtype types
// stay INSIDE this package; only models types and core/errors typed errors
// leave it.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// Error codes. The calling side can look at these with errors.CodeOf; the
// message text may change but the code is part of the contract.
const (
	// codeMetadataInvalid is the jsonb field failing to parse.
	codeMetadataInvalid = "product_metadata_invalid"
	// codeNotFound is the requested record not being found (among the ones
	// that are not deleted).
	codeNotFound = "product_not_found"
	// codeConflict is an unnamed uniqueness violation.
	codeConflict = "product_conflict"
	// codeHandleTaken is the product/collection/category handle being in use.
	codeHandleTaken = "product_handle_taken"
	// codeSKUTaken is the variant SKU being in use.
	codeSKUTaken = "product_sku_taken"
	// codeDuplicate is the same record being added a second time.
	codeDuplicate = "product_duplicate"
	// codeInvalidRef is a reference being made to a record that does not exist.
	codeInvalidRef = "product_invalid_reference"
	// codeCheckFailed is the violation of a database CHECK constraint.
	codeCheckFailed = "product_check_failed"
	// codeDBFailed is a database error that cannot be classified.
	codeDBFailed = "product_db_failed"
	// codeCanceled is the context being canceled.
	codeCanceled = "product_db_canceled"
)

// PostgreSQL SQLSTATE codes (see errcodes-appendix).
const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgCheckViolation      = "23514"
)

// DB is the connection surface the store needs: it must be able to run queries
// and to open transactions. *pgxpool.Pool satisfies this.
//
// The reason an interface is taken instead of the concrete pool is testing: the
// store's transaction handling must be verifiable without a real pool.
type DB interface {
	productdb.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store is the data access surface of the product module.
//
// The interface stands next to the provider (not next to the consumer): ADR
// 0001's "consumer-side interface" rule is for dependencies BETWEEN MODULES;
// here the consumer and the provider are the same module and the only purpose
// of the interface is to separate the service from the database and make it
// testable with a fake store.
//
// All methods take a context.Context (plan Section 8) and return typed
// core/errors errors.
type Store interface {
	// InTx runs fn in a single database transaction.
	//
	// The Store given to fn is bound to the transaction; if fn returns an
	// error the transaction is rolled back. If it is called on a Store that is
	// already inside a transaction NO new transaction is opened, fn runs in
	// the same transaction — a nested call does not silently grab a second
	// connection.
	InTx(ctx context.Context, fn func(ctx context.Context, s Store) error) error

	CreateProduct(ctx context.Context, p models.Product) (models.Product, error)
	GetProduct(ctx context.Context, id string) (models.Product, error)
	// GetProductForUpdate reads the product with a row lock; it is meaningful
	// only inside InTx (see Repo.GetProductForUpdate).
	GetProductForUpdate(ctx context.Context, id string) (models.Product, error)
	GetProductByHandle(ctx context.Context, handle string) (models.Product, error)
	ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, error)
	CountProducts(ctx context.Context, f ProductFilter) (int, error)
	// ProductVisibleInSalesChannels is the visibility check of the single
	// storefront endpoint; it uses the SAME SQL rule as the list (see
	// saleschannel.go).
	ProductVisibleInSalesChannels(ctx context.Context, productID string, salesChannelIDs []string) (bool, error)

	// VisibleProductIDs returns, out of the given ids, the ones visible in the
	// channels in a SINGLE query.
	//
	// It is the bulk counterpart of the single query and the reason it stands
	// apart is the call pattern: search brings tens of ids at a time, and
	// asking for visibility per id means as many round trips as there are
	// results. The two methods are produced from the SAME SQL template, so the
	// rule is single.
	VisibleProductIDs(ctx context.Context, productIDs []string, salesChannelIDs []string) (map[string]struct{}, error)
	ListProductsByIDs(ctx context.Context, ids []string) ([]models.Product, error)
	UpdateProduct(ctx context.Context, id string, patch ProductPatch) (models.Product, error)
	SoftDeleteProduct(ctx context.Context, id string) error
	SoftDeleteProductChildren(ctx context.Context, productID string) error
	ListVariantIDsByProduct(ctx context.Context, productID string) ([]string, error)

	CreateVariant(ctx context.Context, v models.Variant) (models.Variant, error)
	GetVariant(ctx context.Context, id string) (models.Variant, error)
	ListVariants(ctx context.Context, f VariantFilter) ([]models.Variant, error)
	CountVariants(ctx context.Context, f VariantFilter) (int, error)
	ListVariantsByProductIDs(ctx context.Context, productIDs []string) ([]models.Variant, error)
	ListVariantsByIDs(ctx context.Context, ids []string) ([]models.Variant, error)
	// VisibleVariantIDs returns, out of the given variants, the ones visible
	// in the channels in a SINGLE query.
	//
	// It is produced from the SAME SQL template as [Store.VisibleProductIDs]; a
	// variant has no channel, the product it is bound to has one (see
	// saleschannel.go).
	VisibleVariantIDs(ctx context.Context, variantIDs []string, salesChannelIDs []string) (map[string]struct{}, error)
	UpdateVariant(ctx context.Context, id string, patch VariantPatch) (models.Variant, error)
	SoftDeleteVariant(ctx context.Context, id string) error

	CreateOption(ctx context.Context, o models.Option) (models.Option, error)
	GetOption(ctx context.Context, id string) (models.Option, error)
	ListOptionsByProductIDs(ctx context.Context, productIDs []string) ([]models.Option, error)
	SoftDeleteOption(ctx context.Context, id string) error
	// SoftDeleteOptionValuesByOption stamps the option's values; it belongs in
	// the same transaction as SoftDeleteOption.
	SoftDeleteOptionValuesByOption(ctx context.Context, optionID string) error
	// CountVariantsUsingOptionValue is the guard of SoftDeleteOptionValue: a
	// value a living variant carries cannot be removed from under it.
	CountVariantsUsingOptionValue(ctx context.Context, valueID string) (int, error)
	SoftDeleteOptionValue(ctx context.Context, id string) error
	CreateOptionValue(ctx context.Context, v models.OptionValue) (models.OptionValue, error)
	ListOptionValuesByOptionIDs(ctx context.Context, optionIDs []string) ([]models.OptionValue, error)
	ListOptionValuesByIDs(ctx context.Context, ids []string) ([]models.OptionValueRef, error)
	SetVariantOptionValue(ctx context.Context, variantID, optionID, valueID string) error
	DeleteVariantOptionValues(ctx context.Context, variantID string) error
	ListVariantOptionValues(ctx context.Context, variantIDs []string) (map[string][]models.OptionValue, error)

	CreateCollection(ctx context.Context, c models.Collection) (models.Collection, error)
	GetCollection(ctx context.Context, id string) (models.Collection, error)
	ListCollections(ctx context.Context, limit, offset int) ([]models.Collection, error)
	CountCollections(ctx context.Context) (int, error)
	SoftDeleteCollection(ctx context.Context, id string) error
	// ClearCollectionProducts nulls the collection_id of the products bound to
	// the collection and returns how many it changed; it belongs in the same
	// transaction as SoftDeleteCollection.
	ClearCollectionProducts(ctx context.Context, collectionID string) (int, error)

	CreateCategory(ctx context.Context, c models.Category) (models.Category, error)
	GetCategory(ctx context.Context, id string) (models.Category, error)
	// ListCategoriesByIDs reads the named categories in a SINGLE query; it is
	// what keeps the read layer's category provider free of an N+1 when a
	// caller names several ids (see Repo.ListCategoriesByIDs).
	ListCategoriesByIDs(ctx context.Context, ids []string) ([]models.Category, error)
	ListCategories(ctx context.Context, f CategoryFilter) ([]models.Category, error)
	CountCategories(ctx context.Context, f CategoryFilter) (int, error)
	// CountChildCategories is the guard of SoftDeleteCategory: a node with
	// living children cannot be deleted out from under them.
	CountChildCategories(ctx context.Context, id string) (int, error)
	SoftDeleteCategory(ctx context.Context, id string) error

	CreateTag(ctx context.Context, t models.Tag) (models.Tag, error)
	GetTagByValue(ctx context.Context, value string) (models.Tag, error)
	ListTags(ctx context.Context, limit, offset int) ([]models.Tag, error)
	CountTags(ctx context.Context) (int, error)
	SoftDeleteTag(ctx context.Context, id string) error

	SetProductTags(ctx context.Context, productID string, tagIDs []string) error
	SetProductCategories(ctx context.Context, productID string, categoryIDs []string) error
	ListTagsByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Tag, error)
	ListCategoriesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Category, error)

	CreateImage(ctx context.Context, img models.Image) (models.Image, error)
	ListImagesByProductIDs(ctx context.Context, productIDs []string) (map[string][]models.Image, error)
	// ListImagesByIDs reads images BY THEIR OWN ids; it is what turns the image
	// ids the upload binding returns into records.
	ListImagesByIDs(ctx context.Context, imageIDs []string) ([]models.Image, error)
	DeleteImagesByProduct(ctx context.Context, productID string) error
}

// Repo is the PostgreSQL implementation of [Store].
type Repo struct {
	q *productdb.Queries
	// db is the connection surface the hand-written queries run on (see
	// saleschannel.go). It is kept as a separate field because sqlc's Queries
	// type DOES NOT EXPOSE its own connection; in a Repo bound to a
	// transaction this field is the transaction itself, so the hand-written
	// queries run in the same transaction as well.
	db productdb.DBTX
	// pool is needed only for opening transactions; in a Repo bound to a
	// transaction it is nil.
	pool DB
}

// Guarantees that Store is satisfied at compile time: if a method signature has
// drifted, the error shows up not in a test but in the build.
var _ Store = (*Repo)(nil)

// New produces a store that works over the given connection pool.
func New(pool DB) *Repo {
	return &Repo{q: productdb.New(pool), db: pool, pool: pool}
}

// InTx runs fn in a single database transaction.
func (r *Repo) InTx(ctx context.Context, fn func(ctx context.Context, s Store) error) error {
	if r.pool == nil {
		// We are already inside the transaction. Opening a nested transaction
		// would grab a SECOND connection from the pool; that connection cannot
		// read the outer transaction's writes, which are not visible yet, and
		// while waiting for a lock it could walk into a deadlock waiting on
		// itself (self-deadlock).
		return fn(ctx, r)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "could not open database transaction")
	}
	// On a committed transaction Rollback is a no-op; on the error path it
	// rolls back.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, &Repo{q: r.q.WithTx(tx), db: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "could not commit database transaction")
	}
	return nil
}

// wrapDB turns a driver error into a typed error.
//
// The classification determines the caller's behavior: a uniqueness violation
// becomes KindConflict (409), a reference that does not exist KindInvalid
// (422), a row that is not found KindNotFound (404). An error that cannot be
// classified stays KindInternal and the HTTP layer suppresses its message —
// the driver text does not leak to the client.
func wrapDB(err error, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgx.ErrNoRows):
		return errors.Wrap(err, errors.KindNotFound, codeNotFound, format, a...)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled,
			format+" (context canceled)", a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			code, reason := conflictReason(pgErr.ConstraintName)
			return errors.Wrap(err, errors.KindConflict, code, format+": %s", append(a, reason)...)
		case pgForeignKeyViolation:
			return errors.Wrap(err, errors.KindInvalid, codeInvalidRef,
				format+": the referenced record was not found (%s)", append(a, pgErr.ConstraintName)...)
		case pgCheckViolation:
			return errors.Wrap(err, errors.KindInvalid, codeCheckFailed,
				format+": the value does not satisfy the constraint (%s)", append(a, pgErr.ConstraintName)...)
		}
	}
	return errors.Wrap(err, errors.KindInternal, codeDBFailed, format, a...)
}

// conflictReason produces a readable reason and a stable error code from the
// constraint that led to the uniqueness violation.
//
// Constraint names come along with the schema; a name that is not mapped here
// falls back to a generic conflict message. Writing the name into the message
// is for diagnosis: which uniqueness rule fired can only be seen this way in
// production.
func conflictReason(constraint string) (code, reason string) {
	switch constraint {
	case "product_handle_uniq", "product_collection_handle_uniq", "product_category_handle_uniq":
		return codeHandleTaken, "this handle is already in use"
	case "product_variant_sku_uniq":
		return codeSKUTaken, "this SKU is already in use"
	case "product_tag_value_uniq":
		return codeDuplicate, "this tag already exists"
	case "product_option_title_uniq":
		return codeDuplicate, "an option with the same title already exists on this product"
	case "product_option_value_uniq":
		return codeDuplicate, "the same value already exists on this option"
	case "":
		return codeConflict, "uniqueness constraint violated"
	default:
		return codeConflict, "uniqueness constraint violated (" + constraint + ")"
	}
}

// notFound produces a typed error for a record that is not found.
func notFound(entity, id string) error {
	return errors.NotFound(codeNotFound, "%s not found: %s", entity, id)
}
