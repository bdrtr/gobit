package models

import "time"

// MFACredential is an administrator's enrolled authenticator app.
//
// # Enrolled is not proven
//
// [MFACredential.ConfirmedAt] is nil between the moment the secret is written and
// the moment the first correct code arrives. In that window the person has a QR
// code on screen and may never scan it, so a credential that counted as a second
// factor before it was confirmed would lock out everybody whose enrollment went
// half way — which is the ordinary outcome of a phone that runs out of battery
// mid-scan.
type MFACredential struct {
	// UserID is the administrator the credential belongs to; it is the key,
	// because a person has one authenticator and re-enrolling replaces it.
	UserID string
	// Secret is the SEALED TOTP secret: nonce || AES-GCM ciphertext.
	//
	// It leaves the repository as bytes and is opened by the service, which is
	// the only place in this module that holds the key. Nothing that crosses a
	// module or an HTTP boundary ever carries it.
	Secret []byte
	// ConfirmedAt is when the first correct code arrived, or nil.
	ConfirmedAt *time.Time
	// CreatedAt is when the secret was written.
	CreatedAt time.Time
}

// Confirmed reports whether the credential has been proven.
func (c MFACredential) Confirmed() bool { return c.ConfirmedAt != nil }
