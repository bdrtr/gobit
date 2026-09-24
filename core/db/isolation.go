package db

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
)

// This file refuses a connection that does not start its transactions at READ
// COMMITTED (ADR 0166).
//
// # Why the level is a precondition and not a preference
//
// Every lock in this repository that guards a total — the payment ledgers'
// balance lock, the order module's spending lock, the inventory reservation,
// the promotion budget, the category reparent lock — is taken first and the
// total read after, and the read is safe only because it is a FRESH statement
// with a FRESH snapshot: the transaction that waited sees what the one it waited
// on wrote. That is READ COMMITTED. At REPEATABLE READ the snapshot is taken at
// the transaction's first statement, before the wait, and the read after the
// wait is the world from before the other commit. The lock still serializes the
// two transactions, and both decide on the same stale total.
//
// It was measured rather than argued. The whole integration lane, run with every
// connection defaulting to REPEATABLE READ, failed forty tests in fifteen
// packages. Most failed loudly — a serialization error where a waiter was meant
// to proceed, a 500 where a write into a row being deleted was meant to be a
// 404. Five failed SILENTLY, with the invariant broken and every call answered
// with success: eight orders through a spending limit that covers one, sixteen
// cancellations of a three-unit line, a credit of 10,000 on a 6,100 order, a
// category ring, a stock write into a location being closed. That is the class
// nothing reports in production either (measurement 0166).
//
// # Why at the connection and not at each BEGIN
//
// A transaction can name its level, and the payment repository's does (D119).
// But a single statement outside any transaction runs at the session's default
// too, and the one payment test that still failed under REPEATABLE READ was
// exactly that: an INSERT … ON CONFLICT DO NOTHING that a concurrent insert
// turned into a serialization error instead of an answer. Naming the level at
// sixteen BEGINs would have left every such statement to the server.
//
// # Why refuse rather than set
//
// Setting the level as a startup parameter overrides what an operator
// configured without telling them, and connection poolers refuse startup
// parameters they do not track — or, told to ignore them, drop the setting
// silently, which is the state this file exists to end. A SET after connecting
// is session state a transaction-mode pooler does not keep. Reading the level
// works through all of them, and a refusal names the fix.
//
// # Why every connection and not once at startup
//
// The default is read when a session starts, and a pool opens sessions for as
// long as it runs. A role altered under a running process would reach it through
// the next connection the pool opens; checked there, the change turns into
// refused connections — loud errors — instead of a quiet second spend.

// codeIsolationUnsupported reports a session whose transactions would start at a
// level this installation's locks are not written for.
const codeIsolationUnsupported = "db_isolation_unsupported"

// requiredIsolation is the only default a connection is accepted with, in the
// form SHOW prints it.
const requiredIsolation = "read committed"

// isolationProbe reads the level the session's transactions start at: the
// server's, the database's or the role's default, or the connection's own
// options, whichever applies last.
const isolationProbe = `SHOW default_transaction_isolation`

// requireReadCommitted is the pool's AfterConnect: it refuses a connection whose
// transactions would start at any level but READ COMMITTED.
//
// SERIALIZABLE is refused as well. It would not corrupt a total, but every lock
// here waits and proceeds, and at that level the waiter fails with a
// serialization error that no caller retries — an installation that chose it
// would trade silent errors for loud ones on every contended write.
func requireReadCommitted(ctx context.Context, conn *pgx.Conn) error {
	var level string
	if err := conn.QueryRow(ctx, isolationProbe).Scan(&level); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeIsolationUnsupported,
			"the session's default transaction isolation could not be read")
	}
	if level == requiredIsolation {
		return nil
	}

	return errors.Internal(codeIsolationUnsupported,
		"this database starts transactions at %q and gobit is written for %q: its locks "+
			"read a total after waiting, and at %q that read is the snapshot from before "+
			"the wait (ADR 0166). Set it back for the role or the database — ALTER ROLE "+
			"<role> SET default_transaction_isolation = 'read committed' — or remove it from "+
			"the connection's options",
		level, requiredIsolation, level)
}
