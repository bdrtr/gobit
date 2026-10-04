package identitypasskey

import (
	"context"
	"reflect"
)

// KeyNotices tells the owner of an account that its passkeys changed (ADR
// 0381): the message that lets somebody whose session was used by someone else
// notice a key they did not register, or one of theirs gone, and somebody whose
// key was copied learn that it was (ADR 0382).
//
// It is given the customer id and not an address, because this module keeps
// none: the installation reads the address from its own record of the customer,
// so nothing in the request that made the change chooses who is told. Its
// method names differ from the session module's AccountNotices, so one
// messenger can be both.
//
// A notice is sent inside the request, once the change is written, on a context
// the caller hanging up does not cancel and that ends after
// [identitysession.DefaultNoticeTimeout]; a messenger that needs longer queues
// the message and returns.
type KeyNotices interface {
	// SendPasskeyAdded tells the account that a key it did not hold was
	// registered on it; keyID is the key's id as the listing shows it.
	SendPasskeyAdded(ctx context.Context, customerID, keyID string) error
	// SendPasskeyRemoved tells the account that one of its keys was removed;
	// keyID is the id it had, which the listing no longer shows.
	SendPasskeyRemoved(ctx context.Context, customerID, keyID string) error
	// SendPasskeySuspended tells the account that one of its keys signed with a
	// counter that did not advance, which is what a copied key does, and signs
	// nobody in until it is removed (ADR 0382). It is sent once, by the sign-in
	// that suspended the key.
	SendPasskeySuspended(ctx context.Context, customerID, keyID string) error
}

// notifyKeyAdded tells the account a key was added. The key is stored by the
// time it is called, so a notice that cannot be sent is logged rather than
// answered as a failure the person would read as the key not being there.
func (m *Module) notifyKeyAdded(ctx context.Context, customerID string, credentialID []byte) {
	if m.keyNotices == nil {
		return
	}
	m.tell(ctx, customerID, credentialID, "added", m.keyNotices.SendPasskeyAdded)
}

// notifyKeyRemoved tells the account a key was removed, for
// [Module.notifyKeyAdded]'s reason logged rather than failed.
func (m *Module) notifyKeyRemoved(ctx context.Context, customerID string, credentialID []byte) {
	if m.keyNotices == nil {
		return
	}
	m.tell(ctx, customerID, credentialID, "removed", m.keyNotices.SendPasskeyRemoved)
}

// notifyKeySuspended tells the account a key was suspended. The suspension is
// committed by the time it is called, so for [Module.notifyKeyAdded]'s reason a
// notice that cannot be sent is logged and the sign-in is answered as it was.
func (m *Module) notifyKeySuspended(ctx context.Context, customerID string, credentialID []byte) {
	if m.keyNotices == nil {
		return
	}
	m.tell(ctx, customerID, credentialID, "suspended", m.keyNotices.SendPasskeySuspended)
}

// tell sends one notice on [Module.noticeContext] and logs one that fails.
func (m *Module) tell(
	ctx context.Context, customerID string, credentialID []byte, what string,
	send func(ctx context.Context, customerID, keyID string) error,
) {
	keyID := encodeCredentialID(credentialID)
	sendCtx, cancel := m.noticeContext(ctx)
	defer cancel()
	if err := send(sendCtx, customerID, keyID); err != nil {
		m.log.WarnContext(ctx, "identity-passkey: an account could not be told a passkey was "+what,
			"customer_id", customerID, "key_id", keyID, "error", err)
	}
}

// noticeContext is the context a notice is sent on: the request's values,
// without its cancellation, since the person a notice is for is not the one
// holding the connection, and ended by [Module.noticeTimeout] instead.
func (m *Module) noticeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), m.noticeTimeout)
}

// isNil answers whether a bound seam is really there: an interface holding a
// nil pointer is not nil, and calling it would panic after the key was written.
// It is the session module's rule, whose function is not exported.
func isNil(seam any) bool {
	if seam == nil {
		return true
	}

	value := reflect.ValueOf(seam)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}
