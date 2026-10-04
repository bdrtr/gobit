package identitysession

import (
	"context"
)

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
// would read as the change not having happened.
func (m *Module) notifyPasswordChanged(ctx context.Context, customerID, email string) {
	if isNil(m.opts.AccountNotices) {
		return
	}
	if err := m.opts.AccountNotices.SendPasswordChanged(ctx, email); err != nil {
		m.log.WarnContext(ctx, "identity-session could not tell an account its password changed",
			"customer_id", customerID, "error", err)
	}
}

// notifyAddressChanged tells the address an account left that it moved, for
// [Module.notifyPasswordChanged]'s reason logged rather than failed.
func (m *Module) notifyAddressChanged(ctx context.Context, customerID, oldEmail, newEmail string) {
	if isNil(m.opts.AccountNotices) {
		return
	}
	if err := m.opts.AccountNotices.SendAddressChanged(ctx, oldEmail, newEmail); err != nil {
		m.log.WarnContext(ctx, "identity-session could not tell an account's old address it moved",
			"customer_id", customerID, "error", err)
	}
}
