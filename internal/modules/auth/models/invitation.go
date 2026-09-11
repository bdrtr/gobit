package models

import "time"

// UserInvitation is the right to set a user's FIRST password.
//
// It is not an account and it is not a credential: it is a deadline and an owner.
// Whoever holds the token it stands for may set the password of the named user
// once, until the deadline passes (ADR 0137).
type UserInvitation struct {
	// TokenHash is the SHA-256 of the token that was sent, hex.
	//
	// The token itself is never stored. A table of tokens would be a table of
	// working administrator accounts, which is the same reasoning the api_key
	// table carries one file over.
	TokenHash string
	// UserID is the account this invitation opens.
	UserID string
	// InvitedBy is the user who sent it, for the trail an operator reads
	// afterwards.
	InvitedBy string
	// ExpiresAt is when the link stops working.
	ExpiresAt time.Time
	// CreatedAt is when it was sent.
	CreatedAt time.Time
}
