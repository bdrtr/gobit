package service

import "context"

// This file is the CROSS-MODULE surface of the notification module (ADR 0001,
// ADR 0006, ADR 0137).
//
// # Why it did not exist until now
//
// Until this file, the only way anything in gobit could cause a message to go out
// was to PUBLISH AN EVENT and have this module subscribe to it. That is right for a
// notification that follows a fact the whole installation may care about — an order
// was placed — and it is wrong for everything else, in a way that is not a matter
// of taste:
//
//   - an event is written to the outbox and, on the Redis backend, to a durable
//     STREAM. A secret in a payload is a secret at rest in a queue.
//   - every published topic is FORWARDED to whatever endpoints an operator
//     registered, and a gate fails the build in both directions
//     (TestTheForwardedTopicsAreEveryPublishedTopic). A topic exists to be
//     forwarded; a one-time invitation token must never be.
//   - an event is delivered at least once. A message triggered by one is therefore
//     idempotent only because of the (template, reference) key this module already
//     holds — which is fine, and is not a reason to route everything through a bus.
//
// So a caller that wants to send ONE message to ONE address, now, with a secret in
// it, had nowhere to go. The admin invitation is the first such caller (ADR 0137);
// a password reset and an MFA enrolment are the next two.
//
// The signature carries only primitive and stdlib types, so a consumer can declare
// the interface in its own package without importing this one, and the compiler is
// shown the pair in internal/arch (ADR 0136).

// Interop is the notification module's cross-module surface.
type Interop struct {
	svc *Service
}

// NewInterop builds the surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// Send delivers one message and reports whether the provider accepted it.
//
// # It is the same call the event handlers make
//
// Every guarantee [Service.Notify] documents holds here unchanged, and the two
// share one body rather than one shape: the (template, reference) pair is the
// idempotency key, a repeat is a silent skip rather than a second send, an empty
// address closes the record as skipped and returns nil, and the provider is
// resolved before any row is written so a bad configuration never consumes a key.
//
// # What the caller has to decide
//
// The REFERENCE is the caller's own record — the invitation, the order, the reset —
// and it is half the idempotency key. Two sends that must both happen carry two
// references; two deliveries of one act carry one. A caller that passes something
// per-request, a timestamp say, has turned the key off.
//
// Every value in data is a STRING, which is [coreprovider.Notification]'s own rule
// and not this surface's: the bus backend encodes a payload as JSON, JSON has one
// number type, and a field put in as an int64 comes back as a float64 in production
// and an int64 in development.
//
// A secret may travel in data. The default provider logs the KEYS of the map and
// never the values, which is what makes that safe by default rather than by
// convention — and a provider that logged values would be leaking whatever the
// caller sent, not something this surface can prevent.
func (i *Interop) Send(
	ctx context.Context,
	template, channel, reference, to string,
	data map[string]string,
) error {
	return i.svc.Notify(ctx, NotifyInput{
		Template:  template,
		Channel:   channel,
		Reference: reference,
		To:        to,
		Data:      data,
	})
}
