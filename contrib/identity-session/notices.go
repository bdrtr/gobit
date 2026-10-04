package identitysession

import (
	"context"
	"time"
)

// DefaultNoticeTimeout bounds one account notice (D231). A notice outlives the
// caller hanging up, so the request's cancellation no longer bounds it, and a
// mailer that stalls would otherwise hold the handler with nothing to end it.
// It is the notification module's budget for one provider call.
const DefaultNoticeTimeout = 15 * time.Second

// AccountNotices tells a person what changed on their account (ADR 0379):
// the message that lets somebody whose password or address was changed by
// someone else notice it.
type AccountNotices interface {
	// SendPasswordChanged tells the address of an account that its password
	// was replaced, by a reset link or by its owner.
	SendPasswordChanged(ctx context.Context, email string) error
	// SendAddressChanged tells the address an account USED to have that the
	// account moved to another one; it is the old address that has to hear it.
	SendAddressChanged(ctx context.Context, oldEmail, newEmail string) error
}

// notifyPasswordChanged tells the account's address that its password was
// replaced. The change has happened by the time it is called, so a message
// that cannot be sent is logged rather than turned into a failure the person
// would read as the change not having happened, and the caller hanging up does
// not cancel it: the person a notice is for is not the one holding the
// connection. [DefaultNoticeTimeout] bounds it instead (D231).
func (m *Module) notifyPasswordChanged(ctx context.Context, customerID, email string) {
	if m.notices == nil {
		return
	}
	sendCtx, cancel := m.noticeContext(ctx)
	defer cancel()
	if err := m.notices.SendPasswordChanged(sendCtx, email); err != nil {
		m.log.WarnContext(ctx, "identity-session could not tell an account its password changed",
			"customer_id", customerID, "error", err)
	}
}

// notifyAddressChanged tells the address an account left that it moved, for
// [Module.notifyPasswordChanged]'s reasons logged rather than failed and sent
// past a hang-up.
func (m *Module) notifyAddressChanged(ctx context.Context, customerID, oldEmail, newEmail string) {
	if m.notices == nil {
		return
	}
	sendCtx, cancel := m.noticeContext(ctx)
	defer cancel()
	if err := m.notices.SendAddressChanged(sendCtx, oldEmail, newEmail); err != nil {
		m.log.WarnContext(ctx, "identity-session could not tell an account's old address it moved",
			"customer_id", customerID, "error", err)
	}
}

// noticeContext is the context a notice is sent on: the request's values,
// without its cancellation, ended by [Module.noticeTimeout].
func (m *Module) noticeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), m.noticeTimeout)
}
