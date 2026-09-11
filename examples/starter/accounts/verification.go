package accounts

import (
	"context"
	"log/slog"
)

// LogOnlyVerification writes a sign-up link to the log instead of sending it.
//
// # It must not ship
//
// A log file is read by more people than a mailbox, kept longer, and shipped to
// wherever logs go. A sign-up link in one is an account anybody with log access
// can take, and the person it belongs to never sees it happen. This type exists
// so the starter can DEMONSTRATE the seam, and it says so at WARN on every send
// rather than in a comment somebody has to find.
//
// What replaces it is a dozen lines against whatever the shop already sends mail
// with. The interface is two methods and neither returns anything but an error.
type LogOnlyVerification struct {
	log *slog.Logger
}

// NewLogOnlyVerification makes one.
func NewLogOnlyVerification(log *slog.Logger) LogOnlyVerification {
	if log == nil {
		log = slog.Default()
	}

	return LogOnlyVerification{log: log}
}

// SendVerification logs the token, loudly.
func (v LogOnlyVerification) SendVerification(ctx context.Context, email, token string) error {
	v.log.WarnContext(ctx,
		"DEVELOPMENT ONLY: a sign-up link was written to the log instead of being sent; "+
			"replace accounts.LogOnlyVerification before this reaches anybody",
		"email", email, "token", token)

	return nil
}

// SendAlreadyRegistered logs that somebody already has an account.
//
// It carries no token, so this one leaks nothing an attacker could use — but it
// does record that a particular address has an account, which is the very
// question the registration endpoint refuses to answer on the wire. Another
// reason this type is a stand-in.
func (v LogOnlyVerification) SendAlreadyRegistered(ctx context.Context, email string) error {
	v.log.WarnContext(ctx,
		"DEVELOPMENT ONLY: an already-registered message was written to the log instead "+
			"of being sent",
		"email", email)

	return nil
}
