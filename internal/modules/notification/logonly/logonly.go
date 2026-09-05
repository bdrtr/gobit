// Package logonly is the default notification provider that ONLY LOGS the
// notification and SENDS IT NOWHERE (plan Section 5.6).
//
// [Provider] satisfies the NotificationProvider contract in
// core/provider and is the only provider that comes in the box: gobit
// is a framework and cannot know which email/SMS service will be used, yet it
// has to show that the notification path is standing.
//
// # WHY it is named "log"
//
// The payment module's "manual" provider does not reach a real institution
// either, but the pretense there is HARMLESS: manual payment is a real business
// model and the provider keeps a consistent state in its own ledger. Here there
// is no such state — an email that was not sent does not sit anywhere as
// "waiting". That is why the name TELLS the behavior: "log" states plainly that
// a record was made, not a send. Had a name like "smtp", "default" or "noop"
// been chosen, the owner of the installation could have believed the
// notification went out, and the price of that illusion is concrete: the
// customer, having received no order confirmation, believes their order did not
// go through — and no error shows up in the system at all.
//
// For the same reason the send is not counted as FAILED: returning an error
// would paint the delivery log red as if there were a real fault, and would
// make a configuration mistake indistinguishable from a provider outage. The
// provider says "I took the request"; where the request it took did not go is
// said by its name and by this document.
//
// # THE RECIPIENT ADDRESS IS NOT LOGGED
//
// The log line carries neither an email nor a phone number (plan Section 8:
// sensitive data is not logged). The log collector is open to a far wider
// audience than the admin surface; an address landing there would make data
// deliberately not kept in the delivery log permanent through the back door.
package logonly

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// ID is the identity of the provider; it is the NOTIFICATION_PROVIDER default.
const ID = "log"

// Provider is the provider that only logs the notification.
// It is safe for concurrent use.
type Provider struct {
	log *slog.Logger
}

// That Provider satisfies the core contract is verified at compile time; a
// signature drift does not survive until run time.
var _ coreprovider.NotificationProvider = (*Provider)(nil)

// New produces a log provider. When log is given as nil the logs are dropped.
func New(log *slog.Logger) *Provider {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Provider{log: log}
}

// ID returns the identity of the provider.
func (p *Provider) ID() string { return ID }

// Send logs the notification and SENDS IT NOWHERE.
//
// The log level is WARN, not INFO: this line is not the record of a normal
// operation but the warning that "the installation has not picked a real
// provider". Seeing a warning on every order in production is unwanted noise,
// and that is exactly why it is right — the way to silence it is to turn
// NOTIFICATION_PROVIDER towards a plugin provider.
//
// The VALUES of the payload are not logged, only its KEYS are written. Today's
// single template ("order.placed") carries no personal data, but template data
// is free by definition and tomorrow it may carry a customer name; the list of
// keys answers the question "was the template filled in?" without leaking the
// values.
func (p *Provider) Send(ctx context.Context, n coreprovider.Notification) error {
	p.log.WarnContext(ctx, "notification NOT SENT: the 'log' provider only records",
		"provider", ID,
		"template", n.Template,
		"channel", n.Channel,
		"data_keys", slices.Sorted(maps.Keys(n.Data)))
	return nil
}
