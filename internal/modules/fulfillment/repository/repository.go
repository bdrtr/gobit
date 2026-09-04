// Package repository is the database access of the fulfillment module.
//
// It touches ONLY this module's tables (plan Section 4). The sqlc-generated code
// lives under repository/fulfillmentdb and is not edited by hand; this package
// adds two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS PACKAGE,
//     they are converted to models types.
//   - Classification: driver errors are converted into core/errors typed errors;
//     a missing row becomes NotFound, a uniqueness violation becomes Conflict.
//
// # Carrying the transaction
//
// [Repository.WithTx] opens a transaction and puts it into the CONTEXT; every
// repository method called during the transaction runs in that same transaction
// as long as it receives that context. The alternative was to put a separate
// interface type carrying the transaction handle into the method signatures; in
// that case the service could not have matched this package STRUCTURALLY with
// the narrow interface it declares in its own package — in Go the named types in
// a signature have to be identical one for one, meaning the service would have
// been forced to import the repository. Carrying it in the context reduces the
// signatures to the types both sides share (context.Context, models.*).
//
// Locking methods (Lock...) return an error if they are called OUTSIDE a
// transaction: because a FOR UPDATE lock is released once the transaction ends,
// a lock without a transaction would silently protect nothing.
//
// # Two separate ledgers
//
// This package serves the data of two different owners: the domain tables of the
// fulfillment module (shipping_profiles, shipping_options, shipping_option_rules,
// fulfillments fulfillment_items) and the MANUAL PROVIDER's own ledger
// (fulfillment_manual_shipments). The second is not the module's domain data; it
// is the state of the imitated external system and only the manual package
// touches it. The separation is preserved physically as well: the service's
// service.Store interface has NO manual ledger methods.
package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// rollbackTimeout is the time granted to a rollback on a canceled context.
// The rollback must be attempted even if the caller's ctx has expired;
// otherwise the transaction would stay open until the connection returned to
// the pool.
const rollbackTimeout = 5 * time.Second

// txKeyType is the type of the context key; it is not exported so that it
// cannot be produced from the outside.
type txKeyType struct{}

// txKey is the key of the transaction handle in the context.
var txKey = txKeyType{}

// Repository is the access to the fulfillment tables. It is safe for concurrent
// use.
type Repository struct {
	pool *pgxpool.Pool
}

// New produces a Repository working on the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx runs fn in a single database transaction.
//
// The context given to fn carries the transaction; every repository method
// called with that context runs in the same transaction. If fn returns an error
// or panics, the transaction is rolled back and the error (on a panic, the
// panic) is passed upwards.
//
// If the call nests, a new transaction is NOT opened, the existing one is used:
// opening a nested transaction means a savepoint in PostgreSQL and would give a
// misleading confidence about the atomicity of the outer transaction. Because
// the manual provider shares the same store, this is how its calls JOIN the
// service's transaction.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, codeTxBeginFailed, "could not begin transaction")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A short-lived context independent of the caller's is used: if the
		// caller's ctx has been canceled, a rollback made with it would drop
		// instantly too.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, codeTxCommitFailed, "could not commit transaction")
	}
	committed = true
	return nil
}

// txFromContext returns the transaction handle in the context.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries returns the query set appropriate for the context: the one bound to
// the transaction if there is one, otherwise the one bound to the pool.
func (r *Repository) queries(ctx context.Context) *fulfillmentdb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return fulfillmentdb.New(tx)
	}
	return fulfillmentdb.New(r.pool)
}

// requireTx verifies that locking methods are called inside a transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s must be called inside a transaction; a FOR UPDATE lock without a transaction protects nothing", op)
	}
	return nil
}
