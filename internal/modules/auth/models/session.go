package models

import "time"

// Session is one sign-in, which its token names (ADR 0267).
type Session struct {
	ID     string
	UserID string
	// CreatedAt is the moment of the sign-in; it is the token's "iat".
	CreatedAt time.Time
	// ExpiresAt is the token's "exp".
	ExpiresAt time.Time
	// RevokedAt is when this session was closed by itself, or nil. Closing
	// every session moves the identity's anchor instead.
	RevokedAt *time.Time
	// UserAgent is what the browser said it was at the sign-in, bounded to
	// [MaxUserAgent] bytes; empty for a session opened before it was kept
	// (ADR 0276). It is a label a person recognizes, not a fact anything
	// decides on.
	UserAgent string
}

// MaxUserAgent bounds the browser's description a session keeps.
const MaxUserAgent = 512
