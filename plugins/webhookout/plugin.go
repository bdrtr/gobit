// Package webhookout delivers gobit's events to receivers an operator
// registered, signed, and with a retry that gives up in the open.
//
// # What it is
//
// The SENDER half of gaps.md C5. Everything under it was already built: the
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
// brings a module, two tables, a migration, an admin surface, four event
// subscriptions and one scheduled job, and that is named nowhere except one
// line in the composition root's catalog. It is removable with
// `rm -rf plugins/webhookout` plus that line.
//
// # The four moving parts
//
//  1. A receiver is REGISTERED over the admin API, which mints its signing
//     secret and returns it exactly once.
//  2. An event on the bus enqueues one delivery row per receiver that asked for
//     that topic. That is all the subscriber does — see [webhookModule.onEvent]
//     for why it does not send.
//  3. A scheduled pass claims what is due and POSTs it, signed. See
//     signature.go for the scheme a receiver verifies with.
//  4. A delivery that keeps failing is retried on a ladder for more than a day
//     and then DEAD-LETTERED, which fails the job's run — the one channel that
//     reaches `gobit jobs`. `GET /admin/v1/webhooks/deliveries?state=dead` is
//     what a human reads, and redrive and discard are the two ways out.
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

// Setup adds the module, the subscriptions and the delivery job.
//
// # Every topic is subscribed to individually, by name
//
// The bus offers no wildcard, and the arch gates make that a good thing rather
// than an inconvenience: a subscription to a topic nobody publishes fails the
// build, so the four names below are checked against the publishers on every
// run of the suite. See [ForwardedTopics] for what happens the day a fifth
// topic exists.
//
// # The job arrives WITH its consumer
//
// [coreplugin.Host.RegisterJob] was built for a plugin that needed a periodic
// pass; this is the second one to use it, and the pass is not optional
// scaffolding. Without it a delivery row is written and never sent: the
// subscriber only enqueues.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	p.mod = newModule(h.Logger().With("module", ModuleName))

	h.AddModule(p.mod)

	// Four calls rather than a range over [ForwardedTopics], and the reason is
	// the gate: TestEverySubscribedTopicHasAPublisher resolves a subscription's
	// name statically and SKIPS one it cannot, so a loop variable would make
	// these four the only subscriptions in the repository nothing checks.
	h.Subscribe(topicOrderPlaced, p.mod.onEvent)
	h.Subscribe(topicProductCreated, p.mod.onEvent)
	h.Subscribe(topicProductUpdated, p.mod.onEvent)
	h.Subscribe(topicProductDeleted, p.mod.onEvent)

	h.RegisterJob(coreplugin.Job{
		Name:   jobName,
		Every:  every,
		MaxRun: maxRun,
		Run:    p.mod.deliverPass,
	})

	return nil
}
