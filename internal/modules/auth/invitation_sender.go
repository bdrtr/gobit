package auth

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// notificationInteropName is the container name of the notification module's
// cross-module surface.
//
// It is re-spelled as a literal rather than imported: a module does not import
// another module (Principle 2.4), and the tie between the two is this string.
const notificationInteropName = "notification.interop"

// InvitationTemplate is the template name the invitation message is sent under.
//
// It is also half the notification module's idempotency key; the other half is the
// invitation's own token hash, so re-inviting — which mints a NEW token — really
// does send a new message, and a retried delivery of one invitation does not.
const InvitationTemplate = "auth.user_invited"

// The fields the invitation message carries.
//
// The TOKEN is one of them, which is the whole reason this does not go through the
// event bus: an event is durable in a stream and forwarded to an operator's
// third-party endpoints (ADR 0137). It reaches the provider and nowhere else — the
// default provider logs the KEYS of this map and never the values.
const (
	invitationFieldToken   = "token"
	invitationFieldUserID  = "user_id"
	invitationFieldExpires = "expires_in_hours"
)

// messenger is the narrow surface this module needs from the notification module.
//
// Declared HERE with primitive types only, because this module cannot import that
// one; the compiler is shown the pair in internal/arch (ADR 0136).
type messenger interface {
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
}

// invitationSender resolves the notification module on FIRST USE.
//
// Lazy for the reason every other cross-module wrapper in this repository is: the
// resolution cannot happen during Register, because the module being resolved may
// not have registered yet.
type invitationSender struct {
	c   *container.Container
	log *slog.Logger

	once sync.Once
	svc  messenger
	err  error
}

// newInvitationSender holds the container until the first invitation.
func newInvitationSender(c *container.Container, log *slog.Logger) *invitationSender {
	return &invitationSender{c: c, log: log}
}

// SendInvitation carries the token to the address on the account.
//
// A missing notification module is an ERROR rather than a silent skip: the service
// writes the invitation row first and then asks for it to be sent, so swallowing
// the failure would leave a row nobody was told about — an invitation that exists
// and that its owner will never hear of.
func (s *invitationSender) SendInvitation(ctx context.Context, email, token, reference string) error {
	s.once.Do(func() {
		s.svc, s.err = container.Resolve[messenger](s.c, notificationInteropName)
		if s.err != nil {
			s.err = errors.Wrap(s.err, errors.KindInternal, codeSetupFailed,
				"the %s module could not resolve the notification surface (%q); an "+
					"invitation cannot be sent", ModuleName, notificationInteropName)

			return
		}
		s.log.InfoContext(ctx, "invitation sender bound", "surface", notificationInteropName)
	})
	if s.err != nil {
		return s.err
	}

	return s.svc.Send(ctx, InvitationTemplate, coreprovider.ChannelEmail, reference, email,
		map[string]string{
			invitationFieldToken:   token,
			invitationFieldUserID:  reference,
			invitationFieldExpires: "72",
		})
}
