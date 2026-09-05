package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// This file covers the two transitions that used to change a status and record
// nothing: archiving an order, and the exchange record's total inability to
// move at all.
//
// Both were the same class of defect and both were invisible to the type
// system. An archived order had a status saying it had happened and no moment
// saying when; an exchange had two stamp columns, three status values and not a
// single UPDATE, so two of the three statuses and both of the stamps were
// unreachable on every row that has ever existed.

// TestArchivingStampsItsOwnMoment is the fact that was missing.
//
// Before this, archiving flipped the status and wrote nothing but updated_at.
// The assertion that matters is not that ArchivedAt is non-nil — it is that it
// is a DIFFERENT moment from CompletedAt, because the argument for a separate
// column stands or falls on the two being different facts.
func TestArchivingStampsItsOwnMoment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	completed, err := e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)
	require.Nil(t, completed.ArchivedAt, "completing is not archiving")

	archived, err := e.svc.ArchiveOrder(ctx, order.ID)
	require.NoError(t, err)

	require.NotNil(t, archived.ArchivedAt, "archiving has to record WHEN")
	require.NotNil(t, archived.CompletedAt)
	assert.Equal(t, *completed.CompletedAt, *archived.CompletedAt,
		"archiving must not touch the completion stamp")
	assert.NotEqual(t, *archived.CompletedAt, *archived.ArchivedAt,
		"the two moments are different facts; one column could not carry both")
}

// TestAnOrderThatWasNotArchivedCarriesNoStamp is the other direction of the
// database's constraint, checked where a caller can see it.
//
// A stamp implies the status. Nothing else in this module may leave a moment
// on an order that is not in the archive.
func TestAnOrderThatWasNotArchivedCarriesNoStamp(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	assert.Nil(t, order.ArchivedAt, "a fresh order is not archived")

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "the customer changed their mind"))

	canceled, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Nil(t, canceled.ArchivedAt, "a canceled order was never archived")
}

// TestAnExchangeCanBeWithdrawn is the transition the record could not make.
//
// Every exchange ever written was born "requested" and stayed there: the module
// had no UPDATE against the table, so this is the first time the row moves.
func TestAnExchangeCanBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID: order.ID, DifferenceDue: -500,
	})
	require.NoError(t, err)
	require.Equal(t, models.ExchangeRequested, exchange.Status)
	require.Nil(t, exchange.CanceledAt)

	canceled, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	assert.Equal(t, models.ExchangeCanceled, canceled.Status)
	require.NotNil(t, canceled.CanceledAt, "the withdrawal has to record WHEN")
}

// TestASecondWithdrawalOfAnExchangeKeepsTheFirstMoment is why the no-op branch
// is not a re-write.
//
// An operator clicking twice has already achieved what they wanted. Stamping
// again would make the record claim it was withdrawn at the moment of the
// second click, which is the failure [models.AfterSalesNoop] exists to prevent.
func TestASecondWithdrawalOfAnExchangeKeepsTheFirstMoment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID})
	require.NoError(t, err)

	first, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)
	second, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err, "a second withdrawal is a no-op, not a conflict")

	require.NotNil(t, second.CanceledAt)
	assert.Equal(t, *first.CanceledAt, *second.CanceledAt,
		"the record must keep the moment it was ACTUALLY withdrawn")
}

// TestWithdrawingAnExchangeTakesItsLock proves the read and the write cannot be
// split by a second operator.
//
// Without the lock two operators withdrawing at the same moment would both read
// "requested", both proceed, and the record would keep the later stamp — the
// exact failure the no-op branch above is written to avoid, reintroduced by
// concurrency instead of by a second click.
func TestWithdrawingAnExchangeTakesItsLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID})
	require.NoError(t, err)

	_, err = e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err)

	assert.Contains(t, e.store.lockedExchanges, exchange.ID,
		"a transition that did not lock could be split by a second operator")
}

// TestWithdrawingAnExchangeThatIsNotThereIsNotFound keeps the error KIND right.
//
// It matters because the transition runs inside a transaction: a NotFound that
// came back as an Internal would tell an operator the system broke when what
// happened is that they typed the wrong id.
func TestWithdrawingAnExchangeThatIsNotThereIsNotFound(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.CancelExchange(ctx, "exch_nothing")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestWithdrawingAnExchangeNeedsAnID refuses the empty identifier before any
// transaction is opened.
func TestWithdrawingAnExchangeNeedsAnID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	_, err := e.svc.CancelExchange(ctx, "")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestWithdrawingAnExchangeDoesNotNeedALiveOrder is the deliberate difference
// from opening one.
//
// [service.Service.CreateExchange] requires a live order, because opening a
// record against a canceled order would be opening work nobody will do.
// Withdrawing is the opposite motion: refusing it because the order moved would
// strand the record open forever, with no way to close it.
func TestWithdrawingAnExchangeDoesNotNeedALiveOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	exchange, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID})
	require.NoError(t, err)

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "out of stock"))

	canceled, err := e.svc.CancelExchange(ctx, exchange.ID)
	require.NoError(t, err, "an open request on a canceled order must still be closable")
	assert.Equal(t, models.ExchangeCanceled, canceled.Status)
}
