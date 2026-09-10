package identitysession

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The argon2id parameters this package writes.
//
// They are the shape a hash is written in, not a preference: the values are
// recorded IN the hash string, so a stored credential keeps the cost it was
// written with and raising these numbers does not invalidate anybody's password.
// A verify reads the parameters out of the stored hash and ignores these.
//
// 64 MiB and one pass over four lanes is the OWASP recommendation for argon2id.
// The one number that is a judgment rather than a citation is the memory: it is
// per SIGN-IN, so a shop taking a thousand sign-ins a second would be renting
// 64 GiB to check passwords, and such a shop tunes it down and knows why.
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// ErrPasswordMismatch is what a wrong password answers.
//
// It is deliberately the same error a MISSING credential produces
// ([Store.Credential] returning not-found is mapped to it): the two must not be
// distinguishable by a caller, or the sign-in endpoint becomes an oracle telling
// an attacker which e-mail addresses have accounts.
var ErrPasswordMismatch = errors.New("identity-session: the password does not match")

// HashPassword produces the stored form of a password.
//
// The salt is per PASSWORD and random: a shared salt would let one table-wide
// precomputation answer every credential at once, which is the whole reason a
// salt exists.
func HashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", errors.New("identity-session: the password is empty")
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("identity-session: the salt could not be read: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword compares a password with a stored hash.
//
// # Why the comparison is constant time
//
// A byte-by-byte compare that returns early leaks how much of the derived key
// matched, and a derived key is attacker-influenced input. [subtle.ConstantTimeCompare]
// is the whole difference between a comparison and a measurement.
func VerifyPassword(stored, password string) error {
	fields := strings.Split(stored, "$")
	if len(fields) != 6 || fields[1] != "argon2id" {
		return fmt.Errorf("identity-session: the stored hash is not argon2id: %w", ErrPasswordMismatch)
	}

	var version int
	if _, err := fmt.Sscanf(fields[2], "v=%d", &version); err != nil || version != argon2.Version {
		return fmt.Errorf("identity-session: the stored hash's version is unreadable: %w", ErrPasswordMismatch)
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(fields[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return fmt.Errorf("identity-session: the stored hash's cost is unreadable: %w", ErrPasswordMismatch)
	}

	salt, err := base64.RawStdEncoding.DecodeString(fields[4])
	if err != nil {
		return fmt.Errorf("identity-session: the stored salt is unreadable: %w", ErrPasswordMismatch)
	}
	want, err := base64.RawStdEncoding.DecodeString(fields[5])
	if err != nil {
		return fmt.Errorf("identity-session: the stored key is unreadable: %w", ErrPasswordMismatch)
	}

	// The key length is read from the STORED hash, so it is attacker-influenced
	// and is bounded before it is narrowed: a row claiming a four-gigabyte key
	// would otherwise ask argon2 for one.
	if len(want) < 16 || len(want) > 1024 {
		return fmt.Errorf("identity-session: the stored key's length is not plausible: %w",
			ErrPasswordMismatch)
	}

	// The parameters come from the STORED hash and not from the constants above,
	// so a credential written before a cost change still verifies.
	//nolint:gosec // G115: the length is bounded to [16, 1024] three lines up, and
	// the bound is there for exactly the reason this rule exists.
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}

	return nil
}
