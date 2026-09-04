// Package models defines the domain models of the auth module.
//
// The types here are STRIPPED of database types: pgtype does not enter this
// package, the conversion is done in the repository wrapper. This way the
// service and API layers are not bound to a storage detail. Times are UTC;
// deletion is SOFT.
//
// # Secrets
//
// There are two secret fields in this package and both of them carry only a
// HASH: [AuthIdentity.PasswordHash] (bcrypt) and [APIKey.TokenHash]
// (SHA-256). A plaintext password EXISTS as a field on no type — had it
// existed, logging a struct with "%+v" would have written the password to
// disk. The plaintext of a key travels only as the RETURN VALUE of the
// creation call, placed in no struct at all.
package models

import (
	"strings"
	"time"
)

// Field length limits.
//
// The limits are not arbitrary: 320 characters for email is the upper bound of
// RFC 5321's local part (64) + "@" + domain name (255). The others are
// reasonable ceilings that keep a single request from writing unbounded text
// into the database, and they are enforced a second time by the CHECK
// constraints in the migration.
const (
	// MaxEmailLen is the maximum length of an email address.
	MaxEmailLen = 320
	// MaxNameLen is the maximum length of short text fields such as first
	// name, last name and title.
	MaxNameLen = 255
	// MaxURLLen is the maximum length of URL fields such as the avatar
	// address.
	MaxURLLen = 2048
	// MaxDescriptionLen is the maximum length of description fields.
	MaxDescriptionLen = 1024
	// MaxScopeLen is the maximum length of a single scope name.
	MaxScopeLen = 64
	// MaxScopeCount is the maximum number of scopes that can be granted to one
	// identity.
	MaxScopeCount = 64
)

// ProviderEmailPass is the name of the email + password login provider.
//
// For now this is the only provider. When OAuth providers such as "google" or
// "github" are added later, a second [AuthIdentity] is attached to the SAME
// user; the user record is not touched.
const ProviderEmailPass = "emailpass"

// User is an ADMIN user (the person who enters the admin surface).
//
// It must not be confused with the person shopping in the store: that one is
// the customer module's data. The two concepts living in separate modules is
// deliberate — there is no path by which a customer gains admin scope.
//
// The password IS NOT HERE: the authentication method lives in the
// [AuthIdentity] record (the reasoning is written there).
type User struct {
	// ID is the "user_" prefixed, time-ordered identifier.
	ID string
	// Email is the user's email address; it is always stored normalized to
	// LOWER case (see [NormalizeEmail]) and is unique among live users.
	Email string
	// FirstName is the user's first name; it may be empty.
	FirstName string
	// LastName is the user's last name; it may be empty.
	LastName string
	// AvatarURL is the address of the profile image; it may be empty.
	AvatarURL string
	// Scopes are the user's scopes. The default single scope is [ScopeAdmin];
	// finer-grained roles are defined by adding new names to this slice.
	Scopes []string
	// Metadata is structured context the caller writes freely; it may be
	// empty.
	Metadata map[string]any
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// ScopeAdmin is the superior scope that covers all scopes.
//
// The value must be the SAME as corehttp.ScopeAdmin in the core. The constant
// is repeated here because the models package does not know the HTTP layer; a
// test in the service package proves the equality.
const ScopeAdmin = "admin"

// FullName returns the user's display name; if the first and last name are
// empty it falls back to the email.
func (u User) FullName() string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if name == "" {
		return u.Email
	}
	return name
}

// AuthIdentity is ONE authentication method of a user.
//
// # Why separate from User
//
// A user may have more than one login path. Today there is only
// [ProviderEmailPass]; tomorrow, when OAuth is added, a second identity row is
// attached to the same user and the user record is not touched at all. Had the
// password field been on [User], a user without a password (one who logs in
// only through OAuth) would either be inexpressible or would be represented
// with an empty password; the latter would have brought a login attempt with
// an empty password to within one coding mistake.
//
// # PasswordHash
//
// The field is bcrypt output; the plaintext password is neither stored nor
// logged, nor does it appear in error messages. The bcrypt cost is encoded
// INSIDE the hash, which is why old hashes continue to be verified with their
// own cost when the cost is raised later.
type AuthIdentity struct {
	// ID is the "authid_" prefixed identifier.
	ID string
	// UserID is the user the identity is attached to.
	UserID string
	// Provider is the authentication provider (e.g. [ProviderEmailPass]).
	Provider string
	// ProviderIdentity is the identity as the provider knows it; for emailpass
	// it is the user's normalized email.
	ProviderIdentity string
	// PasswordHash is the bcrypt hash; it is empty when no password has been
	// assigned and login is then REJECTED.
	PasswordHash string
	// FailedAttempts is the number of consecutive failed logins; it is reset
	// on a successful login.
	FailedAttempts int
	// LockedUntil is the moment the temporary lock ends; if nil there is no
	// lock.
	LockedUntil *time.Time
	// LastLoginAt is the moment of the last SUCCESSFUL login; if nil no login
	// has ever happened.
	LastLoginAt *time.Time
	// Metadata is structured context the caller writes freely; it may be
	// empty.
	Metadata map[string]any
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// IsLocked reports whether the identity is temporarily locked at the given
// moment.
func (i AuthIdentity) IsLocked(at time.Time) bool {
	return i.LockedUntil != nil && at.Before(*i.LockedUntil)
}

// APIKeyType is the type of an API key.
type APIKeyType string

// API key types. The two are NOT THE SAME THING and CANNOT be used in place of
// one another; how the distinction is enforced is explained in the [APIKey]
// godoc.
const (
	// APIKeyPublishable is the NON-SECRET key used on the store surface.
	APIKeyPublishable APIKeyType = "publishable"
	// APIKeySecret is the SECRET that reaches the admin surface.
	APIKeySecret APIKeyType = "secret"
)

// Valid reports whether the type is one of the defined ones.
//
// The type is exported and a caller can construct a value outside the enum; an
// unvalidated value would trip the CHECK constraint in the database and the
// client would see a meaningless constraint error.
func (t APIKeyType) Valid() bool {
	return t == APIKeyPublishable || t == APIKeySecret
}

// String returns the textual form of the type.
func (t APIKeyType) String() string { return string(t) }

// APIKey is a machine identity.
//
// # Two types, two different trust models
//
// [APIKeySecret] is a SECRET: it reaches the admin surface, is kept on the
// server, is never handed to the browser, and its leaking means admin access.
//
// [APIKeyPublishable] IS NOT A SECRET: it is visible in the browser, inside
// the storefront bundle, even in the page source. Its only job is to BIND the
// request to a sales channel; it carries no scope and on its own opens no
// data. There is therefore no such event as it "leaking" — its misuse is
// somebody reading another store's storefront catalog with that store's
// channel identity, and that catalog is already open to everyone.
//
// The distinction is enforced by two independent gates: the PREFIX of the
// plaintext ("sk_" / "pk_") and the [APIKey.Type] field on this record. The
// admin surface accepts only secret, the store surface only publishable;
// presenting one in place of the other is rejected at both gates.
//
// # Plaintext
//
// The key itself IS NOT STORED; only [APIKey.TokenHash] (SHA-256) is kept. The
// plaintext is handed out ONLY once, as the return value of the creation call,
// and can never again be read from anywhere. A lost key cannot be brought
// back; the thing to do is to revoke it and produce a new one.
//
// The decision is the same for publishable keys as well — even though they are
// not secrets, only their hash is stored too. Uniform storage removes a whole
// class of mistake outright: a code path that "returns the plaintext" cannot
// accidentally show a secret key because of a mistake in the type field, since
// there is no plaintext to return on any row.
type APIKey struct {
	// ID is the "apikey_" prefixed identifier.
	ID string
	// Type is the key's type: [APIKeyPublishable] or [APIKeySecret].
	Type APIKeyType
	// Title is the human-readable name of the key (e.g. "Web storefront").
	Title string
	// TokenHash is the SHA-256 hash of the plaintext (lower case hex, 64
	// characters).
	TokenHash string
	// Redacted is the masked form for display (e.g. "pk_…a1b2"); it does not
	// stand in for the plaintext and CANNOT be used to verify.
	Redacted string
	// Scopes are the key's scopes. On publishable keys it is always EMPTY.
	Scopes []string
	// CreatedBy is the identity of whoever produced the key; it may be a user
	// or another secret key, which is why it carries no foreign key.
	CreatedBy string
	// LastUsedAt is the moment the key was last used; if nil it has never been
	// used. The value is APPROXIMATE (see service, usageThrottle).
	LastUsedAt *time.Time
	// RevokedAt is the revocation moment; if non-nil the key is no longer
	// accepted.
	RevokedAt *time.Time
	// RevokedBy is the identity of whoever performed the revocation; it may be
	// empty.
	RevokedBy string
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// IsRevoked reports whether the key has been revoked.
func (k APIKey) IsRevoked() bool { return k.RevokedAt != nil }

// SalesChannel is a sales channel (e.g. "Web", "Mobile app", "Dealer").
//
// Publishable keys are attached to channels; a store request learns which
// channel it came from through that attachment. Which product appears in which
// channel is established by the product ↔ sales_channel link, and auth never
// sees that link (Principle 2.2).
type SalesChannel struct {
	// ID is the "sc_" prefixed identifier.
	ID string
	// Name is the channel's display name; it is unique among live channels.
	Name string
	// Description is the channel's description; it may be empty.
	Description string
	// IsDisabled reports that the channel is disabled. A disabled channel is
	// IGNORED in store authentication.
	IsDisabled bool
	// Metadata is structured context the caller writes freely; it may be
	// empty.
	Metadata map[string]any
	// CreatedAt is the moment the record was created (UTC).
	CreatedAt time.Time
	// UpdatedAt is the moment the record was last updated (UTC).
	UpdatedAt time.Time
	// DeletedAt is the soft delete moment; if nil the record is live.
	DeletedAt *time.Time
}

// NormalizeEmail converts the email into its storage form: it is trimmed and
// lowered to LOWER case.
//
// Normalization is done on STORAGE, not on reading. The uniqueness index is on
// the raw column; if "Ali@X.com" and "ali@x.com" are to point at the same
// user, both have to land on the same bytes. Normalizing at read time would
// not have prevented two different spellings from entering the table.
//
// Lowercasing the local part (before the @) can technically be considered
// contrary to the RFC — RFC 5321 leaves the local part case sensitive — but in
// practice no provider uses that distinction, and leaving it sensitive would
// have let the same person open two admin accounts. A login matching a single
// row depends on this equalization.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
