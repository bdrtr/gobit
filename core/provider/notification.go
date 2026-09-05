package provider

import "context"

// The notification channels; [Notification.Channel] carries one of these
// values.
//
// The constants live in the core because the value is written both by the
// sending side (the notification module) and by the consuming side (the
// provider in a plugin), and the two CANNOT import each other (Principle 2.4).
// Two hand-repeated "sms" strings would mean the provider not recognizing the
// channel at all and the notification being dropped in silence.
const (
	// ChannelEmail is the email channel; To is an email address.
	ChannelEmail = "email"
	// ChannelSMS is the SMS channel; To is a phone number.
	ChannelSMS = "sms"
)

// Notification is a single notification to be sent.
type Notification struct {
	// Channel is the delivery channel: [ChannelEmail] or [ChannelSMS].
	// A provider that sees a channel it does not support sends nothing and
	// returns an error.
	Channel string
	// To is the recipient address; its meaning depends on the channel (an
	// email address or a phone number).
	To string
	// Template is the name of the template to use (e.g. "order.placed").
	//
	// Choosing the template name to match the event name is deliberate: a
	// notification is triggered by an event, and using two separate names
	// would turn "which event triggers which template" into a question you
	// answer by reading code. The text itself is NOT in the core; the provider
	// resolves the template.
	Template string
	// Data holds the values passed to the template; EVERY value is a STRING.
	//
	// map[string]any looks more flexible but would be WRONG, and the reason is
	// exactly the event payload's: a notification's source is the
	// "order.placed" event, that event is written to Redis Streams as JSON in
	// production, and JSON has a single number type. A field put in as int64
	// reaches the subscriber as float64, while on the InMemory backend it
	// stays int64 — the same field arrives with a DIFFERENT Go type in
	// development and in production. Were any carried, the provider would have
	// to format the value, large amounts would print as "1.2345678e+13" and
	// anything above 2^53 minor units would round silently — money would be
	// passing through a float (plan Section 8: NEVER a float).
	//
	// A string gives the SAME Go type and the EXACT value on both backends.
	// The formatting decision thus falls to the caller producing the value
	// rather than to the provider's guess.
	Data map[string]string
}

// NotificationProvider is the contract a notification provider offers the core
// (plan Section 5.6).
//
// # Why a single method
//
// [PaymentProvider] and [FulfillmentProvider] are multi-step because they hold
// state a saga can undo. A notification holds no such state: a sent email
// cannot be taken back, so there can be no compensation (Cancel) path either.
// Adding delivery-status queries to the contract would force every provider to
// imitate a capability it does not support.
//
// # No idempotency is EXPECTED
//
// Unlike the other providers there is no IdempotencyKey here. A notification is
// triggered from an event subscriber and
// [github.com/bdrtr/gobit/core/eventbus]'s Redis backend delivers AT
// LEAST ONCE; the same notification may be sent twice. That is deliberately
// accepted: preventing the repeat would require a durable "sent" record on the
// provider side, and that record is the sending module's job, not the core's.
// The cost is not symmetric either — a duplicate email annoys, a duplicate
// charge takes the customer's money.
type NotificationProvider interface {
	Provider

	// Send sends the notification.
	//
	// The call BLOCKS and goes to an external service; the caller must put a
	// deadline on ctx. An error does not mean the notification did not go out:
	// a request that timed out may have been processed on the other side.
	Send(ctx context.Context, n Notification) error
}
