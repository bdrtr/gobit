package app

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/eventbus"
)

// A process that is not serving must not CONSUME events.
//
// Every verb in the dispatch opens the whole application, and opening it
// subscribes the modules to the bus. This file is what keeps a command from
// taking a message the server is owed.

// commandBus is the event bus a process gets when it runs a verb rather than the
// server: it publishes to the real bus and consumes nothing.
//
// # What goes wrong without this
//
// Every verb in the dispatch opens the whole application — that is how `seed`
// gets the schema from the modules themselves and how `recover` reaches the
// services it repairs — and opening it registers the modules, which subscribe.
// On the Redis bus a subscription is not a declaration: it creates the consumer
// group if it is missing and starts a goroutine reading from it. Consumers in
// one group receive each message ONCE, which is how the bus scales, and it is
// also how a command steals.
//
// So `gobit seed` against a Redis installation joined the server's group and
// took order.placed, payment.captured and every other topic the modules listen
// for, for as long as it ran — minutes, on the measured rig. What it took, it
// RAN: the notification module's subscriber is registered in the same Register
// that subscribed, so a seed command sent the order confirmations. And what it
// had not acknowledged when the process exited stayed in the pending list under
// a consumer name that never comes back (`<hostname>-<pid>`), where nothing
// reclaims it — the bus has no XAUTOCLAIM and no pending sweep (D104).
//
// # Why publishing is left alone
//
// A command that writes through a service writes an outbox row in the same
// transaction and publishes directly after the commit (the house pattern, ADR
// 0153). Swapping the bus for an in-memory one would have silently dropped that
// direct publish; the outbox would still carry the event, but "the two halves
// agree" is a property worth keeping rather than one worth arguing about. So
// Publish goes to the real bus, unchanged.
//
// # Why Subscribe succeeds
//
// Refusing it would fail Register, and a module that cannot be registered is a
// command that cannot run. What this type does instead is accept the
// subscription and consume nothing — and say so, once, in the log, because a
// process that quietly declines to do what it was asked is the shape this
// repository keeps paying for.
type commandBus struct {
	inner eventbus.EventBus
	log   *slog.Logger
	// announced keeps the log line to one, however many modules subscribe.
	announced bool
}

var _ eventbus.EventBus = (*commandBus)(nil)

// publishOnly wraps a bus for a process that will not serve.
func publishOnly(inner eventbus.EventBus, log *slog.Logger) *commandBus {
	return &commandBus{inner: inner, log: log}
}

// Publish hands the event to the real bus.
func (b *commandBus) Publish(ctx context.Context, event eventbus.Event) error {
	return b.inner.Publish(ctx, event)
}

// Subscribe accepts the registration and consumes nothing.
func (b *commandBus) Subscribe(eventName string, _ eventbus.Handler) error {
	if eventName == "" {
		// The inner bus would refuse this and so does the wrapper: a caller
		// asking for the empty topic has a bug, and hiding it here would make
		// the wrapper's behavior differ from the bus it stands in for.
		return b.inner.Subscribe(eventName, nil)
	}

	if !b.announced {
		b.announced = true
		b.log.Info("this process does not consume events",
			"reason", "it runs a command rather than the server",
			"effect", "subscribers are registered and no message is taken from the bus")
	}

	return nil
}

// Shutdown closes the real bus.
func (b *commandBus) Shutdown(ctx context.Context) error { return b.inner.Shutdown(ctx) }
