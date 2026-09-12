package repository

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
)

// WriteOutboxEvent records an event inside the CURRENT transaction.
//
// # Why this module writes a core-owned table
//
// The outbox row has to commit with the cart's own write, and only this side is
// inside that transaction: every module keeps its transaction under its own
// unexported context key, so the core cannot see it. The core owns the table and
// the writing rule; this method is the hand that reaches into the transaction and
// does nothing else. It is the same hand the order, payment and fulfillment
// modules already carry, written the same way on purpose — a second shape here
// would be a second place for the rule to drift.
//
// # Outside a transaction it REFUSES
//
// An outbox row written outside one is an event promised for work that may never
// commit — the exact fault the outbox exists to prevent, wearing the appearance
// of preventing it.
func (r *Repository) WriteOutboxEvent(
	ctx context.Context, id, name string, data map[string]any,
) error {
	tx, inTx := txFromContext(ctx)
	if !inTx {
		return errors.Internal(codeQueryFailed,
			"an outbox event may only be written inside a transaction (%s); outside one it "+
				"promises an event for work that may never commit", name)
	}

	return outbox.Write(ctx, tx, eventbus.Event{ID: id, Name: name, Data: data})
}
