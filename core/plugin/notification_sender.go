package plugin

import "context"

// NotificationSenderName is the container name the notification module's
// one-message surface is registered under (ADR 0447). A module or a plugin
// resolves a [NotificationSender] by it:
//
//	sender, err := container.Resolve[plugin.NotificationSender](c, plugin.NotificationSenderName)
//
// It is resolved on first use rather than in Register, since the notification
// module may register after the caller.
const NotificationSenderName = "notification.interop"

// NotificationSender sends one message to one address now, through the
// installation's notification provider (ADR 0447).
//
// It is the way to send what must not travel on the bus: a secret, a link, a
// message to one person. An event is durable in a stream and forwarded to an
// operator's receivers; this is neither.
type NotificationSender interface {
	// Send delivers one message and reports whether the provider took it.
	//
	// template names the copy and channel is core/provider's ChannelEmail or
	// ChannelSMS. (template, reference) is the idempotency key: a repeat is
	// skipped and returns nil, so reference names the caller's own record —
	// the reset, the return — and two messages that must both go carry two
	// references. An empty to closes the record as skipped and returns nil.
	// Every value in data is a string, as a notification's are; a secret may
	// travel there, since the default provider logs the keys and never the
	// values.
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
}
