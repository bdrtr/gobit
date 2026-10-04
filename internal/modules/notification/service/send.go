package service

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// sendTimeout is the time granted to a single provider call.
//
// The bound is MANDATORY and cannot be inherited from the caller: the only
// caller of this service is an event handler, and according to the
// [eventbus.Handler] contract the handler's ctx DOES NOT INHERIT the
// cancellation of the request, that is, it has no deadline. An unbounded
// SMTP/HTTP call queues all the events of the same stream behind it on the
// Redis backend (the same reasoning as in searchpg/events.go) and piles up
// goroutines on the InMemory backend. Fifteen seconds is long enough for a slow
// e-mail gateway, short enough not to lock up an event stream.
const sendTimeout = 15 * time.Second

// maxErrorLen is the upper bound of the error text written into the log.
//
// The error the provider returns is a text coming from the outside and its
// length is not under the control of this module; writing it unbounded would
// have meant a single fault bloating the table.
const maxErrorLen = 512

// messageNoAddress is the explanation written into a record skipped for having
// no address.
//
// The Error column carries an error only in the "failed" status; here this is
// NOT an error, but the person reading the record has to be able to answer the
// question "why was it skipped" from a single line. The text DOES NOT CONTAIN
// THE RECIPIENT ADDRESS — there is none anyway.
const messageNoAddress = "there is no address to send to; the provider was not reached"

// NotifyInput is the input of a single notification send.
//
// ALL the fields are PRIMITIVE and the values of Data are strings too; the
// reasoning is in the [coreprovider.Notification] documentation in the core
// (the summary: the source of the payload is an event, the event is turned into
// JSON in production and JSON has a single number type — had any been carried,
// money would travel over a float).
type NotifyInput struct {
	// Template is the name of the template to use (e.g. "order.placed").
	// It is the first half of the idempotency key.
	Template string
	// Channel is the send channel ([coreprovider.ChannelEmail] or
	// [coreprovider.ChannelSMS]).
	Channel string
	// Reference is the identifier of the record the notification is tied to
	// (the order). It is the second half of the idempotency key.
	Reference string
	// To is the recipient address. It IS NOT WRITTEN INTO THE LOG and is not
	// logged; it is only passed on to the provider. It may be empty; see
	// [Service.Notify].
	To string
	// Data holds the values to be passed to the template.
	Data map[string]string
}

// normalize validates the input and trims the whitespace.
func (in NotifyInput) normalize() (NotifyInput, error) {
	in.Template = strings.TrimSpace(in.Template)
	in.Channel = strings.TrimSpace(in.Channel)
	in.Reference = strings.TrimSpace(in.Reference)
	in.To = strings.TrimSpace(in.To)

	if in.Template == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "the template name is required")
	}
	if in.Channel == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "the channel is required")
	}
	if in.Reference == "" {
		return NotifyInput{}, errors.Invalid(CodeInvalidInput, "the reference is required")
	}
	return in, nil
}

// Notify sends the notification to the selected provider and writes the attempt
// into the log.
//
// # The order: first the RECORD, then the send
//
// The record is unique over the (template, reference) pair and it is opened
// BEFORE the provider is reached. The reverse order — send first, record
// afterwards — would not prevent a duplicate notification at all: two
// concurrent handlers would both go to the provider, and the uniqueness
// violation would only become visible after TWO e-mails had gone out.
//
// # If the record already exists the send is SKIPPED; whatever its status
//
// The second call DOES NOT return an error, it skips silently and logs that at
// the info level. The skipping holds for a FAILED record too and this is
// deliberate: the core contract (see
// [coreprovider.NotificationProvider.Send]) says that returning an error DOES
// NOT MEAN the notification did not go out — a request that timed out may have
// been processed on the other side. That is why the rule "retry if it failed"
// can produce a duplicate e-mail and asks for a real retry policy (an attempt
// counter, backoff, a dead letter queue); that policy is not within the scope
// of this module. Resending has to be the DELIBERATE decision of a human being
// looking at the log.
//
// # A notification without an address IS NOT AN ERROR
//
// When To is empty the provider is NOT reached AT ALL, the record is closed as
// [models.DeliverySkipped] and nil is returned. Returning an error would be
// wrong: the caller is an event handler and for it "there is no address" is a
// PERMANENT state — if it cannot be told apart from a fault that will be
// retried, either it is retried forever or the real faults get swallowed too
// (the same reasoning is written in the order module's OrderContactJSON
// documentation as well).
//
// # The returned error
//
// The provider's error IS HANDED BACK TO THE CALLER (after it has been written
// into the record): the event handler returns it to the bus and the bus logs
// the error at the ERROR level with the event name, the event identifier and
// the error chain. Had it been swallowed, the notification not going out would
// only be visible to somebody looking at the table.
func (s *Service) Notify(ctx context.Context, in NotifyInput) error {
	in, err := in.normalize()
	if err != nil {
		return err
	}

	// The provider is resolved BEFORE the record: a record opened with an
	// unknown provider name consumes the idempotency key of a notification that
	// was never sent, and after the configuration was fixed that notification
	// could never be sent again.
	provider, err := s.providers.Get(s.providerID)
	if err != nil {
		return err
	}

	record, isNew, err := s.store.ClaimDelivery(ctx, models.Delivery{
		ID:         models.NewDeliveryID(time.Now()),
		Template:   in.Template,
		Channel:    in.Channel,
		Reference:  in.Reference,
		ProviderID: provider.ID(),
		Status:     models.DeliveryPending,
	})
	if err != nil {
		return err
	}
	if !isNew {
		// The record may be sent, skipped, failed or still pending: any of them
		// is an earlier attempt, and a failed one is sent again only by an
		// operator (ADR 0243). The bus's second call after a failure lands here.
		s.log.InfoContext(ctx, "the notification was attempted before; skipped",
			"template", in.Template, "reference", in.Reference)
		return nil
	}

	return s.deliver(ctx, record, provider, in)
}

// deliver sends a claimed record's notification and writes the outcome: the
// half of [Service.Notify] after the claim, shared with [Service.ResendDelivery].
func (s *Service) deliver(
	ctx context.Context,
	record models.Delivery,
	provider coreprovider.NotificationProvider,
	in NotifyInput,
) error {
	if in.To == "" {
		s.finish(ctx, record, models.DeliverySkipped, messageNoAddress)
		s.log.InfoContext(ctx, "the notification was skipped: there is no address",
			"delivery_id", record.ID, "template", in.Template, "reference", in.Reference)
		return nil
	}

	sendErr := s.send(ctx, provider, in)

	status, message := models.DeliverySent, ""
	if sendErr != nil {
		status, message = models.DeliveryFailed, truncate(withoutAddresses(sendErr.Error(), in.To), maxErrorLen)
	}
	s.finish(ctx, record, status, message)

	if sendErr != nil {
		return errors.Wrap(sendErr, errors.KindOf(sendErr), CodeSendFailed,
			"the %q notification could not be sent (%s, reference %s)",
			in.Template, provider.ID(), in.Reference)
	}

	s.log.InfoContext(ctx, "the notification was sent",
		"delivery_id", record.ID,
		"template", in.Template,
		"channel", in.Channel,
		"reference", in.Reference,
		"provider_id", provider.ID())
	return nil
}

// orderTemplate says whether a template is one of the order mails this module
// builds itself from the order record: the confirmation and the completion
// notice (ADR 0386). Only those are resent here.
func orderTemplate(template string) bool {
	return template == TemplateOrderPlaced || template == TemplateOrderCompleted
}

// ResendDelivery sends a FAILED order mail again, on an operator's
// decision (ADR 0243), or one a dead attempt left PENDING (ADR 0245): such a
// record cannot say whether the mail went, as a failed one cannot, and the
// operator who examined it has this to act with.
//
// # Why an operator, and why only these templates
//
// A provider's error does not say the message did not go out (see [Service.Notify]),
// so a failure is not sent again by itself: that would be a second e-mail for
// every timeout the provider did process. This module said the resend was a
// person's decision and gave the person no way to make it. It rebuilds the
// message from the record, as the event does, which it can do for the order
// confirmation and the completion notice it sends itself (ADR 0386); the
// other templates are sent through its
// interop by the modules that hold their content — an invitation, a gift card's
// code, a stock alert — and are resent by them.
//
// The record is reopened only while it is failed, so a sent one is not sent
// again here and two operators pressing at once send it once.
func (s *Service) ResendDelivery(ctx context.Context, deliveryID string) (models.Delivery, error) {
	record, err := s.store.GetDelivery(ctx, deliveryID)
	if err != nil {
		return models.Delivery{}, err
	}
	if !orderTemplate(record.Template) {
		return models.Delivery{}, errors.Conflict(CodeNotResendable,
			"delivery %s is a %q notification, which the module that sent it resends", deliveryID, record.Template)
	}
	if !resendable(record) {
		return models.Delivery{}, errors.Conflict(CodeNotResendable,
			"delivery %s is %s; only a failed one, or one an attempt left pending more than %s ago, "+
				"is sent again", deliveryID, record.Status, staleAttempt)
	}

	in, err := s.orderMail(ctx, record.Reference, record.Template)
	if err != nil {
		return models.Delivery{}, err
	}
	in, err = in.normalize()
	if err != nil {
		return models.Delivery{}, err
	}
	provider, err := s.providers.Get(s.providerID)
	if err != nil {
		return models.Delivery{}, err
	}

	reopened, ok, err := s.store.ReopenForResend(ctx, deliveryID, provider.ID(), staleAttempt)
	if err != nil {
		return models.Delivery{}, err
	}
	if !ok {
		return models.Delivery{}, errors.Conflict(CodeNotResendable,
			"delivery %s is no longer waiting to be sent again; another resend reached it first", deliveryID)
	}

	sendErr := s.deliver(ctx, reopened, provider, in)
	after, err := s.store.GetDelivery(ctx, deliveryID)
	if err != nil {
		return models.Delivery{}, err
	}

	return after, sendErr
}

// staleAttempt is how long an attempt can still be running after it claimed
// its record: the provider's call and the outcome's write are each bounded by
// [sendTimeout] (ADR 0245). A record pending for longer is an attempt that died.
const staleAttempt = 2 * sendTimeout

// resendable reports whether an operator may send the record again: a failed
// one, or one a dead attempt left pending (ADR 0245). The store's reopen holds
// the same rule on its own clock; this is the read that answers why not.
func resendable(record models.Delivery) bool {
	switch record.Status {
	case models.DeliveryFailed:
		return true
	case models.DeliveryPending:
		return time.Since(record.UpdatedAt) > staleAttempt
	default:
		return false
	}
}

// send calls the provider with a TIME-BOUNDED context.
//
// The reason it is a separate method is that the time bound is established in a
// single place and without covering any step other than the send: had the
// writing of the record stayed under the same bound too, the call that writes
// the RESULT after a slow provider would also be made with an expired context
// and the record would stay "pending".
func (s *Service) send(
	ctx context.Context,
	provider coreprovider.NotificationProvider,
	in NotifyInput,
) error {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	return provider.Send(sendCtx, coreprovider.Notification{
		Channel:  in.Channel,
		To:       in.To,
		Template: in.Template,
		Data:     in.Data,
	})
}

// finish writes the outcome of the attempt into the log; when it cannot write,
// it logs at ERROR.
//
// A write failure IS NOT RETURNED TO THE CALLER and this is deliberate: at this
// point the provider has already been reached, that is, the actual event has
// happened. Returning an error would be showing a notification that was sent as
// if it were "failed"; whereas the real state is "it was sent but its outcome
// could not be written" and the record staying "pending" says exactly that (see
// [models.DeliveryPending]).
//
// The context is STRIPPED OF CANCELLATION: the outcome not being writable when
// the send times out or when the event handling context is canceled would
// leave the record permanently "pending" — that is, the log would go into its
// most silent state in exactly the situation where the most information is
// needed.
func (s *Service) finish(
	ctx context.Context,
	record models.Delivery,
	status models.DeliveryStatus,
	message string,
) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendTimeout)
	defer cancel()

	if _, err := s.store.FinishDelivery(writeCtx, record.ID, status, message); err != nil {
		s.log.ErrorContext(ctx,
			"the notification outcome could not be written into the log; the record stayed 'pending'",
			"delivery_id", record.ID,
			"template", record.Template,
			"reference", record.Reference,
			"status", status.String(),
			"error", err)
	}
}

// withoutAddresses takes the recipient out of a provider's error before the log
// keeps it (D191).
//
// The table keeps no address on purpose — the order holds it, and a second
// copy is a place an erasure has to know about — and a provider's error
// routinely quotes the address it refused ("550 5.1.1 <ayse@example.com>:
// recipient rejected"). The recipient is removed as sent, then anything that
// reads as an e-mail address or a phone number, since a provider may quote it
// folded, reformatted or beside another one. It runs before the text is
// truncated, so a cut cannot leave half an address behind. What a provider
// writes about a person in other words is not recognized here.
func withoutAddresses(text, recipient string) string {
	if recipient = strings.TrimSpace(recipient); recipient != "" {
		text = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(recipient)).ReplaceAllString(text, redactedAddress)
	}
	text = emailInText.ReplaceAllString(text, redactedAddress)

	return numberInText.ReplaceAllString(text, redactedAddress)
}

// redactedAddress stands where an address was taken out.
const redactedAddress = "<address>"

// The shapes of an address in a provider's words: an e-mail address, and a
// phone number of seven digits or more however it is spaced.
var (
	emailInText  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+`)
	numberInText = regexp.MustCompile(`\+?\d(?:[\s\-().]*\d){6,}`)
)

// truncate clips the text to the given length.
//
// The clipping is done on a RUNE boundary: cutting on a byte boundary splits a
// multi-byte character down the middle, producing invalid UTF-8, and that text
// would be silently corrupted while being encoded into JSON.
func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
