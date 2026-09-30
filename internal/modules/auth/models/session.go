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
}
