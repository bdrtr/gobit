package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// This file carries the module's password decisions.
//
// # Why bcrypt (and not argon2id)
//
// Both are acceptable choices; bcrypt was picked for three concrete reasons:
//
//  1. The cost parameter is encoded INSIDE the hash. As hardware gets faster
//     the cost is raised and old hashes DO NOT become invalid — every hash is
//     verified with its own cost, and the user can be silently carried over to
//     the new cost on their next login. The argon2id API in x/crypto returns
//     raw bytes; the salt and the three parameters (memory, rounds,
//     parallelism) have to be encoded and parsed BY HAND. That encoding would
//     be a format this module writes and can then never change again; its
//     mistakes would be silent too.
//  2. Verification is a single call and salt management lives in the library;
//     every line that produces a salt by hand is a risk of writing a fixed salt
//     one day.
//  3. The known limit of bcrypt (72 bytes) is enforced EXPLICITLY (see
//     [MaxPasswordLen]); argon2id has no such limit, but in its place comes the
//     silent risk of picking the memory parameter wrong.
//
// The superiority of argon2id against GPUs is real; the reason it is not
// decisive for this module is that the passwords belong only to ADMIN users and
// that their number is counted in tens. Should the decision ever be changed,
// [Service.SetPassword] and [Service.Login] are the single point of contact.
//
// # Why there is a lock (and why it is short)
//
// After consecutive failed attempts the identity is locked for
// [Options.LoginLockDuration]. The decision is a choice between two evils:
//
//   - Had there been no counter at all, an attacker working against a known
//     admin email address would be limited only by the speed of bcrypt; at cost
//     12 that is ~4 attempts/second per core, which comes to hundreds of
//     thousands of attempts a day. For a weak password that is enough.
//   - A permanent lock, on the other hand, is a targeted denial-of-service
//     tool: an attacker who knows the admin's email address could keep the
//     account closed indefinitely.
//
// This is why the lock is SHORT and OPENS BY ITSELF; it requires no
// administrator intervention. The lock is per account; a per-IP rate limit IS
// NOT THIS MODULE'S JOB and has been left to Phase 9's "rate limiting"
// middleware — the two do not replace each other, they complete each other.
//
// A locked account IS INDISTINGUISHABLE from a wrong password with no lock: the
// same error, the same duration. Saying "your account is locked" would be
// confirming that the account exists. The administrator sees the situation in
// the log.

// dummyPassword is the fixed dummy password used for timing equality.
//
// THIS IS NOT A CREDENTIAL: it belongs to no account, it is written nowhere,
// and its only job is to produce the duration of a bcrypt comparison.
const dummyPassword = "gobit-dummy-password-for-timing-equality"

// newDummyHash sets up a dummy hash producer at the given cost.
//
// Production is LAZY (sync.OnceValue): at cost 12 one bcrypt run takes ~250 ms
// and paying that on every service setup would slow down both startup and every
// unit test. The price is that the first "no such user" attempt is slow one
// more time; this one-off deviation gives away the existence of no account.
func newDummyHash(cost int) func() []byte {
	return sync.OnceValue(func() []byte {
		hash, err := bcrypt.GenerateFromPassword([]byte(dummyPassword), cost)
		if err != nil {
			// Since [New] pulls the cost into range this branch is not reached;
			// if it is, [Service.equalizeTiming] switches to a path of
			// equivalent cost, it does not silently speed up.
			return nil
		}
		return hash
	})
}

// equalizeTiming runs a bcrypt round even when the identity could not be found.
//
// The whole of the timing equality is here: the "no such user", "no identity",
// "no password assigned" and "account locked" branches all make this call, so
// that all four cases take as long as a real password comparison. Had it not
// been done, the response time would be the answer to the question "is this
// email registered?" and an attacker could enumerate the admin addresses.
//
// The result is IGNORED deliberately; the caller's branch is already decided.
func (s *Service) equalizeTiming(password string) {
	if hash := s.dummyHash(); len(hash) > 0 {
		_ = bcrypt.CompareHashAndPassword(hash, []byte(password))
		return
	}
	// If the dummy hash could not be produced, a second path of equivalent cost:
	// production and verification both run the same number of bcrypt rounds.
	_, _ = bcrypt.GenerateFromPassword([]byte(dummyPassword), s.cost)
}

// hashPassword hashes the password with bcrypt at the configured cost.
//
// The password DOES NOT APPEAR in the error message.
func (s *Service) hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cost)
	if err != nil {
		return "", errors.Wrap(err, errors.KindInternal, CodeWeakPassword,
			"the password could not be hashed")
	}
	return string(hash), nil
}

// SetPassword sets the user's password; it creates the login identity if there
// is none.
//
// The password policy is enforced by [validatePassword]. The plaintext password
// lives only inside this call: it is hashed, the hash is stored and the
// password itself passes into no struct, no log line and no error message.
//
// A successful call also resets the lock counters: a user who changes their
// password should not run into the lock left behind by attempts made with the
// old password.
//
// # Existing sessions DROP
//
// The write carries the identity's [sessionAnchor] value up to this moment and
// session tokens produced earlier are rejected on their next requests (see
// [Service.principalFromToken]). This is not a side effect of the password
// change but its PURPOSE: a leaked admin token would stay valid until it
// expired if the password had not changed.
//
// The row that is advanced is ONLY that of the [models.ProviderEmailPass]
// identity; the password is that identity's information and it has no
// counterpart in the rows of other providers. The sessions that drop are still
// ALL of them, because verification does not pick the anchor by provider: it
// reads the user's newest anchor. This is why, unlike logout, there is no need
// to touch every row here.
//
// Changing the password IS NOT NECESSARY in order to close sessions: the
// endpoint that advances the same anchor without touching the credential is
// [Service.Logout].
func (s *Service) SetPassword(ctx context.Context, userID, password string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(userID, models.UserIDPrefix, "the user identifier"); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}

	hash, err := s.hashPassword(password)
	if err != nil {
		return err
	}

	if _, err := s.repo.SetPasswordHash(
		ctx, user.ID, models.ProviderEmailPass, user.Email, hash, s.clock(),
	); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "user password updated",
		slog.String("user_id", user.ID),
	)
	return nil
}

// Login authenticates with an email address and a password and produces a
// session token.
//
// # One error, one duration
//
// Every failing path produces THE SAME error ([CodeInvalidCredentials]) and
// ROUGHLY THE SAME duration:
//
//	no such user · no login identity · no password assigned · account locked ·
//	wrong password
//
// Had a distinction been made, the response itself would hand over the
// information "this email is registered" and the attacker would first collect
// the valid admin addresses and then bear down on those alone. The duration
// equality is provided by [Service.equalizeTiming]; the real reason is written
// only to the LOG and does not go to the client.
//
// The duration equality is provided roughly: a difference of one database query
// may remain between the branches, but that difference (sub-millisecond) is
// immeasurable next to the bcrypt round (hundreds of milliseconds) that
// dominates the duration.
//
// # Return
//
// The token and the expiry moment are returned. The token IS A SECRET; the
// caller must not log it, only pass it on in the response body.
func (s *Service) Login(ctx context.Context, email, password string) (string, time.Time, error) {
	if err := s.ready(); err != nil {
		return "", time.Time{}, err
	}
	// Empty input IS NOT a question about an account, it is a client error;
	// returning a separate error gives away the existence of no account.
	if email == "" || password == "" {
		return "", time.Time{}, errors.Invalid(CodeInvalidInput,
			"the email address and the password are required")
	}

	normalized, err := normalizeEmail(email)
	if err != nil {
		// A malformed email address can match no account; it still returns as a
		// credentials error and in an equal duration, because this path is a
		// QUERY ABOUT AN ACCOUNT and the difference between a format error and
		// "no such account" would tell the attacker which addresses are worth
		// trying.
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "the email format is invalid", "")
	}

	user, err := s.repo.GetUserByEmail(ctx, normalized)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", time.Time{}, err
		}
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "the user was not found", "")
	}

	identity, err := s.repo.GetIdentity(ctx, user.ID, models.ProviderEmailPass)
	if err != nil {
		if !errors.IsNotFound(err) {
			return "", time.Time{}, err
		}
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "there is no login identity", user.ID)
	}

	now := s.clock()
	if identity.IsLocked(now) {
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "the account is temporarily locked", user.ID)
	}
	if identity.PasswordHash == "" {
		// An identity with no password assigned (e.g. OAuth only) cannot log in
		// with a password. Handing the empty hash to bcrypt returns a "hash too
		// short" error and that branch is MEASURABLY fast.
		s.equalizeTiming(password)
		return "", time.Time{}, s.failLogin(ctx, "no password has been assigned", user.ID)
	}

	if cmpErr := bcrypt.CompareHashAndPassword(
		[]byte(identity.PasswordHash), []byte(password),
	); cmpErr != nil {
		s.registerFailure(ctx, identity.ID, now)
		return "", time.Time{}, s.failLogin(ctx, "the password did not match", user.ID)
	}

	if err := s.repo.RegisterLoginSuccess(ctx, identity.ID, now); err != nil {
		// If the counter could not be cleared the login is still valid; not
		// letting the user in would mean locking the administration out because
		// of a temporary failure of a counter write. The situation is logged.
		s.log.WarnContext(ctx, "the successful login could not be recorded",
			slog.String("user_id", user.ID), slog.Any("error", err))
	}

	token, expiresAt, err := s.issueToken(user.ID, user.Scopes, now)
	if err != nil {
		return "", time.Time{}, err
	}

	s.log.InfoContext(ctx, "admin login succeeded",
		slog.String("user_id", user.ID),
	)
	return token, expiresAt, nil
}

// failLogin LOGS the reason of a failed login and returns the generic error.
//
// The reason does not go to the client; the email address and the password are
// not written to the log either (plan Section 8: sensitive data is not logged).
// If the user is known only their identifier is written.
func (s *Service) failLogin(ctx context.Context, reason, userID string) error {
	attrs := []any{slog.String("reason", reason)}
	if userID != "" {
		attrs = append(attrs, slog.String("user_id", userID))
	}
	s.log.WarnContext(ctx, "admin login rejected", attrs...)

	return errors.Unauthorized(CodeInvalidCredentials, "the email address or the password is wrong")
}

// registerFailure counts the failed attempt; a counting error does not affect
// the login.
//
// If the counter cannot be written the request is still rejected — the counter
// is a layer of protection, not the source of truth.
func (s *Service) registerFailure(ctx context.Context, identityID string, now time.Time) {
	identity, err := s.repo.RegisterLoginFailure(
		ctx, identityID, s.threshold, now.Add(s.lockFor), now,
	)
	if err != nil {
		s.log.WarnContext(ctx, "the failed login counter could not be updated",
			slog.String("identity_id", identityID), slog.Any("error", err))
		return
	}
	if identity.IsLocked(now) {
		s.log.WarnContext(ctx, "the account has been temporarily locked",
			slog.String("user_id", identity.UserID),
			slog.Int("failed_attempts", identity.FailedAttempts),
			slog.Time("locked_until", *identity.LockedUntil),
		)
	}
}
