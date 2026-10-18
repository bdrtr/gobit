// Package webhookout delivers gobit's events to receivers an operator
// registered, signed, and with a retry that gives up in the open.
//
// # What it is
//
// The SENDER half of the outbound webhook feature. Everything under it was
// already built: the
// outbox makes an event survive the transaction that promised it, the relay
// retries a failed publish with a ceiling and a dead letter (B12), and a plugin
// can own a periodic pass (B13). What was missing was the thing that turns a
// published event into an HTTP request somebody outside gobit can receive.
//
// # Why a plugin with its own module, and not a provider
//
// The same question ADR 0018 answered for web push, and the answer comes out
// the same. A provider is CHOSEN per unit of work out of a registry; what an
// outbound webhook needs is STATE the framework must already hold — a URL, a
// secret and a topic set, registered by a human before any event can go
// anywhere. The notification provider slot has one destination field, `To
// string`, documented as "an email address or a phone number", and a webhook
// destination is three values, two of which have nowhere to live in that
// contract.
//
// So this is the plugins/searchpg and plugins/webpush shape: a plugin that
// brings a module, two tables, a migration, an admin surface, a subscription
// per forwarded topic, one scheduled job and one worker, and that is named
// nowhere except one line in the composition root's catalog. It is removable
// with `rm -rf plugins/webhookout` plus that line.
//
// # The four moving parts
//
//  1. A receiver is REGISTERED over the admin API, which mints its signing
//     secret and returns it exactly once.
//  2. An event on the bus enqueues one delivery row per receiver that asked for
//     that topic. That is all the subscriber does — see [webhookModule.onEvent]
//     for why it does not send.
//  3. The process that queued a delivery attempts it, signed, by the first
//     claim of the worker every serving process runs, and a failed one by the
//     first claim after its step of the ladder ends: at once when the worker
//     is idle, or when its pass in flight ends. Receivers take turns in a
//     claim. A scheduled pass a minute sends what no worker took (ADR 0436). The scheme a receiver verifies with, and the envelope it
//     decodes, are published as core/webhook (ADR 0435); what each topic
//     carries is described under "webhooks" in /openapi.json.
//  4. A delivery that keeps failing is retried on a ladder for more than a day
//     and then DEAD-LETTERED, which fails the job's run — the one channel that
//     reaches `gobit jobs`. `GET /admin/v1/webhooks/deliveries?state=dead` is
//     what a human reads, and redrive and discard are the two ways out.
//
// # Order
//
// Deliveries keep no order. Eight go out at once from each process, a failed
// one arrives after later ones, and the bus that feeds the queue promises no
// order either. A receiver reads the record the event names, or computes each
// figure as a target, rather than trusting the sequence it is sent. Delivery
// is at least once, and the delivery id, the same on every attempt, is the key
// a receiver drops a repeat by.
//
// # The endpoint secrets are durable state on the order of the database
//
// They are stored recoverable, because a MAC has to be computed with them on
// every attempt, and they are minted server-side and shown once. Losing the
// table means re-issuing every integration's secret and having every receiver
// reconfigured; nothing on this side can repair it. That puts them in the same
// class as the webpush VAPID key, and the down migration says so where an
// operator will be standing when it matters.
//
// # Usage
//
//	PLUGINS=webhook-out
//
// There is no other setting, and that is deliberate: everything an installation
// configures about a webhook — the URL, the topics, the secret — belongs to a
// receiver rather than to the process, and a receiver registered in the
// environment could not be added without a deploy.
package webhookout

import (
	"context"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the registry; the PLUGINS list recognizes it.
const Name = "webhook-out"

// ModuleName is the module's name, and it differs from the plugin's on purpose.
//
// A module name becomes the prefix of its migration ledger table, which the
// core validates against a strict identifier pattern — a hyphen is refused.
// searchpg and webpush carry the same split for the same reason.
const ModuleName = "webhookout"

// Plugin is the outbound webhook plugin.
type Plugin struct {
	mod *webhookModule
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup adds the module, the subscriptions, the delivery job and the worker.
//
// # Every topic is subscribed to individually, by name
//
// The bus offers no wildcard, and the arch gates make that a good thing rather
// than an inconvenience: a subscription to a topic nobody publishes fails the
// build, so the names below are checked against the publishers on every run of
// the suite. See [ForwardedTopics] for what happens the day another topic
// exists.
//
// No count is written here on purpose. The list grew from four to six the day
// the payment module started publishing (ADR 0121), and every sentence in these
// two files that had priced it stayed at four — the same defect this repository
// keeps producing. The one place the size IS stated names the path, so the
// count gate audits it.
//
// # The senders arrive WITH their consumer
//
// The subscriber only enqueues. The worker's first claim after a row is queued
// or due sends it ([coreplugin.Host.RegisterWorker], ADR 0436), and the job's
// pass is the backstop for what no worker took — a process whose worker died,
// a capped receiver's next minute — and the one alarm `gobit jobs` shows for
// the pile. Without the two of them a delivery row is written and never sent.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	p.mod = newModule(h.Logger().With("module", ModuleName))

	h.AddModule(p.mod)

	// Written out rather than ranged over [ForwardedTopics], and the reason is
	// the gate: TestEverySubscribedTopicHasAPublisher resolves a subscription's
	// name statically and SKIPS one it cannot, so a loop variable would make
	// these the only subscriptions in the repository nothing checks.
	h.Subscribe(topicOrderPlaced, p.mod.onEvent)
	h.Subscribe(topicPaymentCaptured, p.mod.onEvent)
	h.Subscribe(topicPaymentRefunded, p.mod.onEvent)
	h.Subscribe(topicProductCreated, p.mod.onEvent)
	h.Subscribe(topicProductUpdated, p.mod.onEvent)
	h.Subscribe(topicProductDeleted, p.mod.onEvent)
	h.Subscribe(topicOrderLineCanceled, p.mod.onEvent)
	h.Subscribe(topicFulfillmentCanceled, p.mod.onEvent)
	h.Subscribe(topicFulfillmentReturned, p.mod.onEvent)
	h.Subscribe(topicCartCreated, p.mod.onEvent)
	h.Subscribe(topicCartCompleted, p.mod.onEvent)
	h.Subscribe(topicOrderCanceled, p.mod.onEvent)
	h.Subscribe(topicOrderCompleted, p.mod.onEvent)
	h.Subscribe(topicFulfillmentDelivered, p.mod.onEvent)
	h.Subscribe(topicOrderReturnOpened, p.mod.onEvent)
	h.Subscribe(topicOrderReturnCanceled, p.mod.onEvent)
	h.Subscribe(topicWishlistBackInStock, p.mod.onEvent)

	h.RegisterJob(coreplugin.Job{
		Name:   jobName,
		Every:  every,
		MaxRun: maxRun,
		Run:    p.mod.deliverPass,
	})
	h.RegisterWorker(coreplugin.Worker{
		Name: workerName,
		Run:  p.mod.serve,
	})

	return nil
}
