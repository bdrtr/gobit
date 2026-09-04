package models

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

// Prefixes of the API key plaintext.
//
// The prefix IS NOT DECORATION: the admin and store surfaces tell the incoming
// credential apart without going to the database at all and reject a key of
// the wrong type at the very first step. The second gate is the [APIKey.Type]
// field; the two gates are independent of each other and no identity is
// established without passing both.
//
// The prefixes are also useful to leak scanners: when a string starting with
// "sk_" lands in a repository or in a log, it can be found by pattern
// matching.
const (
	// SecretKeyPrefix is the prefix of secret keys.
	SecretKeyPrefix = "sk_"
	// PublishableKeyPrefix is the prefix of publishable keys.
	PublishableKeyPrefix = "pk_"
)

// tokenEntropyBytes is the byte count of a key's random body.
//
// 32 bytes = 256 bits. This equals the width of the stored SHA-256 digest and
// is computationally impossible to guess; it is also the reason the key does
// not need a slow hash the way a password does (see [HashToken]).
const tokenEntropyBytes = 32

// redactedTailLen is the number of trailing characters left visible in the
// masked display.
//
// Four characters are enough to tell two keys apart in a list and give a
// 24-bit hint out of a 256-bit body — the remaining search space is still
// 2^232, which means the hint has no practical value.
const redactedTailLen = 4

// redactedMask is the marker put in place of the hidden part in the masked
// display.
const redactedMask = "..."

// ErrUnknownKeyType reports that an unrecognized key type was given.
//
// It does not use the typed errors of the errors package: the models layer
// does not know HTTP status codes and this error is classified by the calling
// service.
var ErrUnknownKeyType = errors.New("auth: unknown api key type")

// TokenPrefix returns the plaintext prefix of the given type.
func TokenPrefix(t APIKeyType) (string, error) {
	switch t {
	case APIKeySecret:
		return SecretKeyPrefix, nil
	case APIKeyPublishable:
		return PublishableKeyPrefix, nil
	default:
		return "", ErrUnknownKeyType
	}
}

// TypeForToken derives the key type from the prefix of the plaintext.
//
// This is the FIRST gate of authentication: a string with an "sk_" prefix
// arriving at the store surface is rejected without any database read being
// made.
func TypeForToken(plaintext string) (APIKeyType, error) {
	switch {
	case strings.HasPrefix(plaintext, SecretKeyPrefix):
		return APIKeySecret, nil
	case strings.HasPrefix(plaintext, PublishableKeyPrefix):
		return APIKeyPublishable, nil
	default:
		return "", ErrUnknownKeyType
	}
}

// NewToken produces a new plaintext API key for the given type.
//
// The body is 32 bytes of cryptographic randomness and is encoded with
// unpadded base64url; the result can travel unescaped in a URL, in a header
// and in an environment variable. Unlike [NewID] it CARRIES no timestamp:
// sortability is an identifier property, and added to a secret it would have
// narrowed the search space.
//
// The returned value is the ONLY copy in the caller's hands; it is put in no
// struct and is not stored (see [APIKey]).
func NewToken(t APIKeyType) (string, error) {
	prefix, err := TokenPrefix(t)
	if err != nil {
		return "", err
	}

	buf := make([]byte, tokenEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		// Even though crypto/rand.Read does not return an error, the case
		// where it does cannot be passed over silently: a key produced with
		// weak randomness would be guessable.
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken produces the stored digest of the plaintext: SHA-256, lower case
// hex.
//
// # Why not bcrypt
//
// Password hashes are DELIBERATELY slow; what they protect against is an
// offline dictionary attack on a human-chosen, low-entropy secret. An API key
// is not that kind of secret: [NewToken] PRODUCES it with 256 bits of
// randomness, which means that even if the database leaks in full, brute force
// is computationally impossible and the protection a slow hash adds is zero.
//
// Against that, its cost is not zero: this digest is computed on EVERY
// REQUEST. bcrypt would add ~250 ms to every admin request and authentication
// itself would turn into a denial-of-service surface. What is more, bcrypt's
// per-row salt would require scanning the WHOLE table to find which row the
// incoming key belongs to and running a bcrypt on every row; SHA-256 is a
// single and indexable lookup.
//
// The comparison is done in CONSTANT TIME with [TokenHashesEqual].
func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// TokenHashesEqual compares two key digests in CONSTANT TIME.
//
// Even though the lookup itself is made over an index, this comparison is
// still necessary: leaving equality to the database alone carries the risk
// that the query one day turns into a prefix match or into a case-insensitive
// comparison. The check here verifies on the application side as well that the
// stored digest is BYTE FOR BYTE the same as the incoming digest, and it does
// so without an early exit.
func TokenHashesEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RedactToken produces the masked form of the plaintext for display
// (e.g. "pk_...a1b2").
//
// The masked value IS NOT a key and cannot be used to authenticate; its only
// job is to tell two keys apart in a list.
func RedactToken(plaintext string) string {
	prefix := ""
	body := plaintext
	if t, err := TypeForToken(plaintext); err == nil {
		prefix, _ = TokenPrefix(t)
		body = strings.TrimPrefix(plaintext, prefix)
	}

	if len(body) <= redactedTailLen {
		// This branch is not reached for a key of the expected length; if it
		// is reached, showing none of the body is the right thing rather than
		// showing all of it.
		return prefix + redactedMask
	}
	return prefix + redactedMask + body[len(body)-redactedTailLen:]
}
